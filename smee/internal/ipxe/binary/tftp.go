package binary

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/pin/tftp/v3"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	tftpmux "github.com/tinkerbell/tinkerbell/smee/internal/tftp"
	"github.com/tinkerbell/tinkerbell/smee/internal/tftp/firmware"
	tftpHook "github.com/tinkerbell/tinkerbell/smee/internal/tftp/hook"
	"github.com/tinkerbell/tinkerbell/smee/internal/tftp/pxelinux"
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

	// CacheDir is the directory where hook images are stored and served from
	CacheDir string

	// Tinkerbell server configuration for cmdline.txt generation
	PublicSyslogFQDN      string // Public syslog FQDN
	TinkServerTLS         bool   // TLS enabled for Tink server
	TinkServerInsecureTLS bool   // Allow insecure TLS
	TinkServerGRPCAddr    string // Tink server gRPC address
	ExtraKernelParams     []string
}

// ListenAndServe will listen and serve files over TFTP using the shared handler system.
func (h *TFTP) ListenAndServe(ctx context.Context) error {
	// Create the shared TFTP server
	mux := tftpmux.NewServeMux(h.Log)

	// Register iPXE binary handler
	binaryConfig := Config{
		Patch: h.Patch,
	}
	binaryHandler := NewHandler(binaryConfig, h.Log)
	// Match common iPXE binary names - this should be more specific based on actual binary names
	mux.HandleFunc(`\.(efi|kpxe|pxe)$`, binaryHandler)

	// Register PXELinux handler for pxelinux.cfg files
	pxeConfig := pxelinux.Config{
		PublicSyslogFQDN:      h.PublicSyslogFQDN,
		TinkServerTLS:         h.TinkServerTLS,
		TinkServerInsecureTLS: h.TinkServerInsecureTLS,
		TinkServerGRPCAddr:    h.TinkServerGRPCAddr,
		ExtraKernelParams:     h.ExtraKernelParams,
	}
	pxeHandler := pxelinux.NewHandler(h.Backend, pxeConfig, h.Log)
	mux.HandleFunc(`^pxelinux\.cfg/`, pxeHandler)

	// Register hook file handler for initramfs and vmlinuz files
	hookConfig := tftpHook.Config{
		CacheDir: h.CacheDir,
	}
	hookHandler := tftpHook.NewHandler(hookConfig, h.Log)
	mux.HandleFunc(`^(initramfs-|vmlinuz-)`, hookHandler)

	// Register firmware handler as catch-all
	firmwareHandler := firmware.NewHandler(h.Log)
	mux.HandleFunc(`.*`, firmwareHandler) // Catch-all pattern

	// Create the underlying TFTP server
	server := tftp.NewServer(mux.ServeTFTP, h.HandleWrite)
	server.SetTimeout(h.Timeout)
	server.SetBlockSize(h.BlockSize)
	server.SetAnticipate(h.Anticipate)

	// Add logging middleware
	loggingMiddleware := &tftpLoggingHook{log: h.Log}
	server.SetHook(loggingMiddleware)

	if h.EnableTFTPSinglePort {
		server.EnableSinglePort()
	}

	go func() {
		<-ctx.Done()
		server.Shutdown()
	}()

	return server.ListenAndServe(h.Addr.String())
}

// HandleWrite handles TFTP PUT requests. It will always return an error. This library does not support PUT.
func (h TFTP) HandleWrite(filename string, wt io.WriterTo) error {
	err := fmt.Errorf("access_violation: %w", os.ErrPermission)
	h.Log.Error(err, "tftp write request rejected", "filename", filename)
	return err
}

// tftpLoggingHook implements tftp.Hook interface for logging TFTP transfer statistics.
type tftpLoggingHook struct {
	log logr.Logger
}

// OnSuccess logs successful TFTP transfers.
func (h *tftpLoggingHook) OnSuccess(stats tftp.TransferStats) {
	h.log.Info("tftp transfer successful",
		"filename", stats.Filename,
		"remoteAddr", stats.RemoteAddr.String(),
		"duration", stats.Duration.String(),
		"datagramsSent", stats.DatagramsSent,
		"datagramsAcked", stats.DatagramsAcked,
		"mode", stats.Mode,
		"tid", stats.Tid,
	)
}

// OnFailure logs failed TFTP transfers.
func (h *tftpLoggingHook) OnFailure(stats tftp.TransferStats, err error) {
	h.log.Error(err, "tftp transfer failed",
		"filename", stats.Filename,
		"remoteAddr", stats.RemoteAddr.String(),
		"duration", stats.Duration.String(),
		"datagramsSent", stats.DatagramsSent,
		"datagramsAcked", stats.DatagramsAcked,
		"mode", stats.Mode,
		"tid", stats.Tid,
	)
}
