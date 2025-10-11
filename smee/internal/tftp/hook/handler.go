package hook

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Config holds the configuration needed for hook file serving.
type Config struct {
	CacheDir string
}

// NewHandler creates a new hook file handler with the given configuration.
func NewHandler(config Config, log logr.Logger) func(filename string, rf io.ReaderFrom) error {
	return func(filename string, rf io.ReaderFrom) error {
		return handleHookFile(filename, rf, config, log)
	}
}

func handleHookFile(filename string, rf io.ReaderFrom, config Config, log logr.Logger) error {
	log = log.WithValues("event", "hook_file", "filename", filename)
	log.Info("handling hook file request")

	// Check if cache directory is configured
	if config.CacheDir == "" {
		return fmt.Errorf("cache directory not configured")
	}

	// Create tracing context
	tracer := otel.Tracer("TFTP-Hook")
	_, span := tracer.Start(context.Background(), "TFTP hook file serve",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	// Construct the full file path
	filePath := filepath.Join(config.CacheDir, filename)

	// Security check - ensure the file is within the configured directory
	if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(config.CacheDir)) {
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
