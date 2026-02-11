package hook

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// rpiPrefixPattern matches Raspberry Pi netboot path prefixes: <serial-or-mac>/
// Raspberry Pi firmware prepends the serial number (8-12 hex chars) or MAC address (dash-separated)
// to all TFTP file requests during netboot.
var rpiPrefixPattern = regexp.MustCompile(`^([0-9a-fA-F]{8,12}|[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2})/{1,2}(.+)$`)

// Handler holds the configuration needed for hook file serving.
type Handler struct {
	Logger   logr.Logger
	CacheDir string
}

func (h Handler) ServeTFTP(filename string, rf io.ReaderFrom) error {
	log := h.Logger.WithValues("event", "hook_file", "filename", filename)
	log.Info("handling hook file request")

	// Check if cache directory is configured
	if h.CacheDir == "" {
		return fmt.Errorf("cache directory not configured")
	}

	// Create tracing context
	tracer := otel.Tracer("TFTP-Hook")
	_, span := tracer.Start(context.Background(), "TFTP hook file serve",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	// Strip Raspberry Pi netboot prefix (serial or MAC) from the filename.
	// RPi firmware prepends its serial/MAC to all TFTP requests during netboot,
	// e.g. "dc-a6-32-01-02-03/vmlinuz-aarch64" or "abcdef01/initramfs-aarch64".
	cleanName := filename
	if matches := rpiPrefixPattern.FindStringSubmatch(filename); matches != nil {
		cleanName = matches[2]
		log.Info("stripped RPi netboot prefix from filename", "original", filename, "cleaned", cleanName)
	}

	// Construct the full file path
	filePath := filepath.Join(h.CacheDir, cleanName)

	// Security check - ensure the file is within the configured directory
	if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(h.CacheDir)) {
		err := fmt.Errorf("invalid file path: %s", filename)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			err := fmt.Errorf("hook file not found: %s", filename)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		log.Error(err, "failed to open hook file from filesystem")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to open hook file from filesystem: %w", err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		log.Error(err, "failed to stat hook file")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to stat hook file: %w", err)
	}

	if transfer, ok := rf.(interface{ SetSize(int64) }); ok {
		transfer.SetSize(fi.Size())
	}

	// Stream the file directly using ReadFrom
	n, err := rf.ReadFrom(file)
	if err != nil {
		log.Error(err, "failed to serve hook file content")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to serve hook file content: %w", err)
	}

	log.Info("successfully served hook file", "filename", filename, "bytes_transferred", n)
	span.SetStatus(codes.Ok, "file served successfully")
	return nil
}
