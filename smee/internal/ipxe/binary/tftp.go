package binary

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/pin/tftp/v3"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	binary "github.com/tinkerbell/tinkerbell/smee/internal/ipxe/binary/file"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	edk2 "github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/file"
)

// BackendReader is an interface that defines the method to read data from a backend.
type BackendReader interface {
	// Read data (from a backend) based on a mac address
	// and return DHCP headers and options, including netboot info.
	GetByMac(context.Context, net.HardwareAddr) (*data.DHCP, *data.Netboot, error)
}

// TFTP config settings.
type TFTP struct {
	// Backend is the backend to use for getting DHCP data.
	Backend BackendReader

	Log                  logr.Logger
	EnableTFTPSinglePort bool
	Addr                 netip.AddrPort
	Timeout              time.Duration
	Patch                []byte
	BlockSize            int
	Anticipate           uint
	// CacheDir is the directory to cache downloaded hook files (initramfs and kernel).
	// If empty, defaults to "/tmp/tinkerbell-hook-cache".
	CacheDir string
}

var (
	hookDownloadMutex sync.Mutex
	hookDownloadURL   = "https://github.com/tinkerbell/hook/releases/download/latest/hook_armbian-bcm2711-current.tar.gz"
)

// ListenAndServe will listen and serve iPXE binaries over TFTP.
func (h *TFTP) ListenAndServe(ctx context.Context) error {
	a, err := net.ResolveUDPAddr("udp", h.Addr.String())
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", a)
	if err != nil {
		return err
	}

	ts := tftp.NewServer(h.HandleRead, h.HandleWrite)
	ts.SetTimeout(h.Timeout)
	ts.SetBlockSize(h.BlockSize)
	ts.SetAnticipate(h.Anticipate)
	if h.EnableTFTPSinglePort {
		ts.EnableSinglePort()
	}

	go func() {
		<-ctx.Done()
		conn.Close()
		ts.Shutdown()
	}()

	return ts.Serve(conn)
}

// HandleRead handlers TFTP GET requests. The function signature satisfies the tftp.Server.readHandler parameter type.
func (h TFTP) HandleRead(filename string, rf io.ReaderFrom) error {
	client := net.UDPAddr{}
	if rpi, ok := rf.(tftp.OutgoingTransfer); ok {
		client = rpi.RemoteAddr()
	}

	full := filename
	filename = path.Base(filename)
	log := h.Log.WithValues("event", "get", "filename", filename, "uri", full, "client", client)

	// clients can send traceparent over TFTP by appending the traceparent string
	// to the end of the filename they really want
	longfile := filename // hang onto this to report in traces
	ctx, shortfile, err := extractTraceparentFromFilename(context.Background(), filename)
	if err != nil {
		log.Error(err, "failed to extract traceparent from filename")
	}
	if shortfile != filename {
		log = log.WithValues("shortfile", shortfile)
		log.Info("traceparent found in filename", "filenameWithTraceparent", longfile)
		filename = shortfile
	}
	// If a mac address is provided (0a:00:27:00:00:02/snp.efi), parse and log it.
	// Mac address is optional.
	validMac := true
	optionalMac, err := net.ParseMAC(path.Dir(full))
	if err != nil {
		validMac = false
	}
	log = log.WithValues("macFromURI", optionalMac.String())

	tracer := otel.Tracer("TFTP")
	_, span := tracer.Start(ctx, "TFTP get",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("filename", filename)),
		trace.WithAttributes(attribute.String("requested-filename", longfile)),
		trace.WithAttributes(attribute.String("ip", client.IP.String())),
		trace.WithAttributes(attribute.String("mac", optionalMac.String())),
	)
	defer span.End()

	readAll := func(content []byte) error {
		rf.(tftp.OutgoingTransfer).SetSize(int64(len(content)))
		ct := bytes.NewReader(content)
		b, err := rf.ReadFrom(ct)
		if err != nil {
			log.Error(err, "file serve failed", "b", b, "contentSize", len(content))
			span.SetStatus(codes.Error, err.Error())

			return err
		}
		log.Info("file served", "bytesSent", b, "contentSize", len(content))
		span.SetStatus(codes.Ok, filename)

		return nil
	}

	if filepath.Base(shortfile) == edk2.FirmwareFileName && validMac {
		_, netboot, err := h.Backend.GetByMac(ctx, optionalMac)
		if err != nil || netboot == nil || !netboot.AllowNetboot {
			return readAll(edk2.RpiEfi)
		}
		content, err := edk2.Read(optionalMac)
		if err != nil {
			log.Error(err, "failed to read firmware image")
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		return readAll(content)
	}

	if strings.HasPrefix(filepath.Base(shortfile), "initramfs-") || strings.HasPrefix(filepath.Base(shortfile), "vmlinuz-") {
		// Handle initramfs and kernel files - download and cache if necessary
		log.Info("handling hook file request", "filename", filepath.Base(shortfile))
		
		cacheDir := h.CacheDir
		if cacheDir == "" {
			cacheDir = "/tmp/tinkerbell-hook-cache" // Default cache directory
			log.Info("using default cache directory", "cacheDir", cacheDir)
		}

		// Temporarily set the cache directory for this operation
		originalCacheDir := h.CacheDir
		h.CacheDir = cacheDir

		// Ensure hook files are downloaded and cached
		if err := h.downloadAndExtractHook(ctx); err != nil {
			h.CacheDir = originalCacheDir // Restore original
			log.Error(err, "failed to download hook files")
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to download hook files: %w", err)
		}

		// Try to find the requested file in cache
		content, err := h.findFileInCache(filepath.Base(shortfile))
		h.CacheDir = originalCacheDir // Restore original
		if err != nil {
			log.Error(err, "failed to find file in cache", "filename", filepath.Base(shortfile))
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("file not found: %s", filepath.Base(shortfile))
		}

		log.Info("serving hook file from cache", "filename", filepath.Base(shortfile), "size", len(content))
		return readAll(content)
	}

	if content, ok := binary.Files[filepath.Base(shortfile)]; ok {
		content, err = binary.Patch(content, h.Patch)
		if err != nil {
			log.Error(err, "failed to patch binary")
			span.SetStatus(codes.Error, err.Error())
			return err
		}

		return readAll(content)
	}

	if content, ok := edk2.Files[filepath.Base(shortfile)]; ok {
		return readAll(content)
	}

	return nil
}

// HandleWrite handles TFTP PUT requests. It will always return an error. This library does not support PUT.
func (h TFTP) HandleWrite(filename string, wt io.WriterTo) error {
	err := fmt.Errorf("access_violation: %w", os.ErrPermission)
	client := net.UDPAddr{}
	if rpi, ok := wt.(tftp.OutgoingTransfer); ok {
		client = rpi.RemoteAddr()
	}
	h.Log.Error(err, "client", client, "event", "put", "filename", filename)

	return err
}

// downloadAndExtractHook downloads and extracts the hook tar.gz file to the cache directory.
func (h *TFTP) downloadAndExtractHook(ctx context.Context) error {
	hookDownloadMutex.Lock()
	defer hookDownloadMutex.Unlock()

	// Check if files already exist in cache
	if h.hookFilesExist() {
		return nil
	}

	log := h.Log.WithValues("event", "download_hook", "url", hookDownloadURL, "cacheDir", h.CacheDir)
	log.Info("downloading hook archive")

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(h.CacheDir, 0o755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Download the tar.gz file
	req, err := http.NewRequestWithContext(ctx, "GET", hookDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{
		Timeout: 10 * time.Minute, // Set a reasonable timeout for large files
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download hook archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download hook archive: HTTP %d", resp.StatusCode)
	}

	// Create gzip reader
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	// Create tar reader
	tr := tar.NewReader(gzr)

	// Extract files
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		filename := filepath.Base(header.Name)
		// Only extract initramfs and kernel files
		if strings.HasPrefix(filename, "initramfs-") || strings.HasPrefix(filename, "vmlinuz-") {
			targetPath := filepath.Join(h.CacheDir, filename)
			log.Info("extracting file", "filename", filename, "targetPath", targetPath, "size", header.Size)

			file, err := os.Create(targetPath)
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}

			written, err := io.Copy(file, tr)
			file.Close()
			if err != nil {
				return fmt.Errorf("failed to extract file %s: %w", filename, err)
			}
			log.Info("file extracted", "filename", filename, "bytesWritten", written)
		}
	}

	log.Info("hook archive extracted successfully")
	return nil
}

// hookFilesExist checks if any initramfs or kernel files exist in the cache directory.
func (h *TFTP) hookFilesExist() bool {
	if h.CacheDir == "" {
		return false
	}

	entries, err := os.ReadDir(h.CacheDir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			filename := entry.Name()
			if strings.HasPrefix(filename, "initramfs-") || strings.HasPrefix(filename, "vmlinuz-") {
				return true
			}
		}
	}

	return false
}

// findFileInCache looks for a file in the cache directory and returns its content.
func (h *TFTP) findFileInCache(filename string) ([]byte, error) {
	if h.CacheDir == "" {
		return nil, fmt.Errorf("cache directory not configured")
	}

	cachePath := filepath.Join(h.CacheDir, filename)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("file not found in cache: %s", filename)
	}

	content, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cached file %s: %w", filename, err)
	}

	return content, nil
}
