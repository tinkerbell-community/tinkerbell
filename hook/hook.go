package hook

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// Config holds the configuration for the hook service
type Config struct {
	// ImagePath is the directory where hook images are stored
	ImagePath string
	// Version specifies the hook version to download (e.g., "latest", "v1.2.3")
	Version string
	// DownloadTimeout for downloading hook archives
	DownloadTimeout time.Duration
	// HTTPAddr is the address to bind the HTTP server to
	HTTPAddr netip.AddrPort
	// EnableHTTPServer controls whether to start the HTTP file server
	EnableHTTPServer bool
}

// Option functions for configuring the hook service
type Option func(*Config)

func WithImagePath(path string) Option {
	return func(c *Config) {
		c.ImagePath = path
	}
}

func WithVersion(version string) Option {
	return func(c *Config) {
		c.Version = version
	}
}

func WithDownloadTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.DownloadTimeout = timeout
	}
}

func WithHTTPAddr(addr netip.AddrPort) Option {
	return func(c *Config) {
		c.HTTPAddr = addr
	}
}

func WithEnableHTTPServer(enable bool) Option {
	return func(c *Config) {
		c.EnableHTTPServer = enable
	}
}

// NewConfig creates a new hook service configuration with defaults
func NewConfig(opts ...Option) *Config {
	defaults := &Config{
		ImagePath:        "/var/lib/hook",
		Version:          "latest",
		DownloadTimeout:  10 * time.Minute,
		EnableHTTPServer: true,
	}

	for _, opt := range opts {
		opt(defaults)
	}

	return defaults
}

// service manages hook image downloads and serving
type service struct {
	config       *Config
	log          logr.Logger
	downloadOnce sync.Once
	mutex        sync.RWMutex
	ready        bool
	httpServer   *http.Server
}

// RequiredFiles lists the required files for each architecture
var RequiredFiles = map[string][]string{
	"x86_64": {"initramfs-x86_64", "vmlinuz-x86_64"},
	"arm64":  {"initramfs-arm64", "vmlinuz-arm64"},
}

// ArchitectureMap maps architectures to their download URL suffixes and file suffixes
var ArchitectureMap = map[string]struct {
	URLSuffix  string
	FileSuffix string
}{
	"x86_64": {
		URLSuffix:  "latest-lts-x86_64",
		FileSuffix: "latest-lts-x86_64",
	},
	"arm64": {
		URLSuffix:  "armbian-bcm2711-current",
		FileSuffix: "armbian-bcm2711-current",
	},
}

// Start initializes and starts the hook service
func (c *Config) Start(ctx context.Context, log logr.Logger) error {
	log.Info("starting hook service", "version", c.Version, "imagePath", c.ImagePath, "httpEnabled", c.EnableHTTPServer)

	svc := &service{
		config: c,
		log:    log,
	}

	// Create image directory
	if err := os.MkdirAll(c.ImagePath, 0o755); err != nil {
		return fmt.Errorf("failed to create image directory: %w", err)
	}

	// Check if required files exist
	if svc.allFilesExist() {
		log.Info("all required hook files exist, skipping download")
		svc.ready = true
	} else {
		log.Info("required hook files missing, will download all architectures in background")
	}

	// Start background download
	go func() {
		if err := svc.downloadAndExtractHook(ctx); err != nil {
			log.Error(err, "failed to download hook files")
		} else {
			svc.mutex.Lock()
			svc.ready = true
			svc.mutex.Unlock()
			log.Info("hook files downloaded and ready")
		}
	}()

	// Start HTTP server if enabled
	if c.EnableHTTPServer && c.HTTPAddr.IsValid() {
		return svc.startHTTPServer(ctx)
	}

	// If HTTP server is not enabled, just wait for context cancellation
	<-ctx.Done()
	return nil
}

// allFilesExist checks if all required files exist
func (s *service) allFilesExist() bool {
	for arch, files := range RequiredFiles {
		for _, file := range files {
			filePath := filepath.Join(s.config.ImagePath, file)
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				s.log.Info("missing required file", "arch", arch, "file", file)
				return false
			}
		}
	}
	return true
}

// downloadAndExtractHook downloads and extracts the hook tar.gz file
func (s *service) downloadAndExtractHook(ctx context.Context) error {
	var err error
	s.downloadOnce.Do(func() {
		err = s.doDownloadAndExtract(ctx)
		if err != nil {
			s.log.Error(err, "failed to download and extract hook")
		}
	})
	return err
}

// doDownloadAndExtract performs the actual download and extraction
func (s *service) doDownloadAndExtract(ctx context.Context) error {
	log := s.log.WithValues("event", "download_hook", "imagePath", s.config.ImagePath)
	log.Info("downloading hook archives for all architectures")

	// Create image directory if it doesn't exist
	if err := os.MkdirAll(s.config.ImagePath, 0o755); err != nil {
		return fmt.Errorf("failed to create image directory: %w", err)
	}

	extractedFiles := make(map[string]string) // original filename -> extracted path

	// Download and extract for each architecture defined in ArchitectureMap
	// This ensures we have hook files available for all supported architectures
	for arch := range ArchitectureMap {
		log.Info("downloading hook archive", "architecture", arch)

		archExtracted, err := s.downloadAndExtractArch(ctx, arch)
		if err != nil {
			log.Error(err, "failed to download architecture", "arch", arch)
			return fmt.Errorf("failed to download %s architecture: %w", arch, err)
		}

		// Merge extracted files
		for filename, path := range archExtracted {
			extractedFiles[filename] = path
		}
	}

	// Create architecture-specific symlinks
	if err := s.createArchSymlinks(extractedFiles); err != nil {
		return fmt.Errorf("failed to create architecture symlinks: %w", err)
	}

	log.Info("all hook archives extracted successfully")
	return nil
}

// downloadAndExtractArch downloads and extracts hook files for a specific architecture
func (s *service) downloadAndExtractArch(ctx context.Context, arch string) (map[string]string, error) {
	hookDownloadURL := s.getDownloadURL(arch)

	log := s.log.WithValues("event", "download_hook_arch", "architecture", arch, "url", hookDownloadURL)
	log.Info("downloading hook archive for architecture")

	// Download the tar.gz file
	req, err := http.NewRequestWithContext(ctx, "GET", hookDownloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{
		Timeout: s.config.DownloadTimeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download hook archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download hook archive: HTTP %d", resp.StatusCode)
	}

	// Create gzip reader
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	// Create tar reader
	tr := tar.NewReader(gzr)

	// Extract files
	extractedFiles := make(map[string]string) // original filename -> extracted path

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		filename := filepath.Base(header.Name)

		// Only extract initramfs and kernel files
		if strings.HasPrefix(filename, "initramfs-") || strings.HasPrefix(filename, "vmlinuz-") {
			targetPath := filepath.Join(s.config.ImagePath, filename)
			log.Info("extracting file", "filename", filename, "targetPath", targetPath, "size", header.Size)

			file, err := os.Create(targetPath)
			if err != nil {
				return nil, fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}

			written, err := io.Copy(file, tr)
			file.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to extract file %s: %w", filename, err)
			}

			extractedFiles[filename] = targetPath
			log.Info("file extracted", "filename", filename, "bytesWritten", written)
		}
	}

	log.Info("hook archive extracted for architecture", "filesExtracted", len(extractedFiles))
	return extractedFiles, nil
}

// createArchSymlinks creates symlinks for architecture-specific files
func (s *service) createArchSymlinks(extractedFiles map[string]string) error {
	// Determine architecture from extracted files and create symlinks
	for originalFile, extractedPath := range extractedFiles {
		var targetArch string
		var targetName string

		// Determine target architecture based on file suffix patterns
		found := false
		for arch, archInfo := range ArchitectureMap {
			if strings.Contains(originalFile, archInfo.FileSuffix) {
				targetArch = arch
				found = true
				break
			}
		}

		// Fallback to legacy pattern matching if no explicit match found
		if !found {
			if strings.Contains(originalFile, "aarch64") || strings.Contains(originalFile, "arm64") {
				targetArch = "arm64"
			} else if strings.Contains(originalFile, "x86_64") || strings.Contains(originalFile, "amd64") {
				targetArch = "x86_64"
			} else {
				// Default to arm64 for Raspberry Pi images
				targetArch = "arm64"
			}
		}

		if strings.HasPrefix(originalFile, "initramfs-") {
			targetName = fmt.Sprintf("initramfs-%s", targetArch)
		} else if strings.HasPrefix(originalFile, "vmlinuz-") {
			targetName = fmt.Sprintf("vmlinuz-%s", targetArch)
		} else {
			continue
		}

		symlinkPath := filepath.Join(s.config.ImagePath, targetName)

		// Remove existing symlink if it exists
		if _, err := os.Lstat(symlinkPath); err == nil {
			if err := os.Remove(symlinkPath); err != nil {
				s.log.Error(err, "failed to remove existing symlink", "path", symlinkPath)
			}
		}

		// Create new symlink
		if err := os.Symlink(filepath.Base(extractedPath), symlinkPath); err != nil {
			s.log.Error(err, "failed to create symlink", "target", extractedPath, "link", symlinkPath)
			return err
		}

		s.log.Info("created architecture symlink", "arch", targetArch, "target", targetName, "source", originalFile)
	}

	return nil
}

// getDownloadURL constructs the download URL based on version and architecture
func (s *service) getDownloadURL(arch string) string {
	archInfo, exists := ArchitectureMap[arch]
	if !exists {
		// Fallback to arm64 if architecture not found
		archInfo = ArchitectureMap["arm64"]
	}
	return fmt.Sprintf("https://github.com/tinkerbell/hook/releases/download/%s/hook_%s.tar.gz", s.config.Version, archInfo.URLSuffix)
}

// startHTTPServer starts the HTTP file server
func (s *service) startHTTPServer(ctx context.Context) error {
	mux := http.NewServeMux()

	// Serve hook files
	mux.Handle("/", http.FileServerFS(os.DirFS(s.config.ImagePath)))

	s.httpServer = &http.Server{
		Addr:    s.config.HTTPAddr.String(),
		Handler: mux,
	}

	s.log.Info("starting hook HTTP server", "addr", s.config.HTTPAddr.String())

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.log.Error(err, "failed to shutdown HTTP server")
		}
	}()

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	return nil
}
