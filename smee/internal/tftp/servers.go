package tftp

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/pin/tftp/v3"
	"github.com/tinkerbell/tinkerbell/smee/internal/tftp/hook"
	"github.com/tinkerbell/tinkerbell/smee/internal/tftp/rpi"
)

// ConfigTFTP is the configuration for the TFTP server.
type ConfigTFTP struct {
	Anticipate           uint
	BlockSize            int
	CacheDir             string
	EnableTFTPSinglePort bool
	Logger               logr.Logger
	Patch                []byte
	Timeout              time.Duration
}

// ServeTFTP sets up all the TFTP routes using a stdlib mux and starts the TFTP
// server, which will block. App functionality is instrumented in Prometheus and OpenTelemetry.
func (c *ConfigTFTP) ServeTFTP(ctx context.Context, addr string, handlers HandlerMapping) error {
	mux := NewServeMux()
	mux.log = logr.FromContextOrDiscard(ctx)

	// Register Raspberry Pi handler for native RPi netboot paths
	// Pattern matches: <serial-or-mac>/<filename> where serial/mac is 8-12 hex chars or MAC with dashes
	// Examples: b827eb123456/config.txt, b8-27-eb-12-34-56/cmdline.txt
	mux.Handle(`^([0-9a-fA-F]{8,12}|[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2})/.*$`, rpi.Handler{
		Logger:   c.Logger,
		CacheDir: c.CacheDir,
	})

	for pattern, handler := range handlers {
		mux.Handle(pattern, handler)
	}
	mux.SetDefaultHandler(hook.Handler{
		Logger:   c.Logger,
		CacheDir: c.CacheDir,
	})

	// Create the underlying TFTP server
	server := tftp.NewServer(mux.ServeTFTP, c.handleWrite)
	server.SetTimeout(c.Timeout)
	server.SetBlockSize(c.BlockSize)
	server.SetAnticipate(c.Anticipate)

	// Add logging middleware
	loggingMiddleware := &tftpLoggingMiddleware{log: c.Logger}
	server.SetHook(loggingMiddleware)

	if c.EnableTFTPSinglePort {
		server.EnableSinglePort()
	}

	go func() {
		<-ctx.Done()
		server.Shutdown()
	}()

	return server.ListenAndServe(addr)
}

// handleWrite handles TFTP PUT requests. It will always return an error. This library does not support PUT.
func (c ConfigTFTP) handleWrite(filename string, _ io.WriterTo) error {
	err := fmt.Errorf("access_violation: %w", os.ErrPermission)
	c.Logger.Error(err, "tftp write request rejected", "filename", filename)
	return err
}
