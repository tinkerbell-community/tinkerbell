package pxelinux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const pxeLinuxPrefix = "pxelinux.cfg/01-"

// BackendReader is an interface that defines the method to read data from a backend.
type BackendReader interface {
	// Read data (from a backend) based on a mac address
	// and return DHCP headers and options, including netboot info.
	GetByMac(context.Context, net.HardwareAddr) (*data.DHCP, *data.Netboot, error)
}

// Config holds the configuration needed for PXELinux script generation.
type Config struct {
	PublicSyslogFQDN      string
	TinkServerTLS         bool
	TinkServerInsecureTLS bool
	TinkServerGRPCAddr    string
	ExtraKernelParams     []string
}

// Hook represents the data structure for generating hook scripts.
type Hook struct {
	AllowNetboot          bool
	Arch                  string
	Console               string
	ExtraKernelParams     []string
	Facility              string
	HWAddr                string
	Initrd                string
	Kernel                string
	SyslogHost            string
	TinkerbellInsecureTLS bool
	TinkerbellTLS         bool
	TinkGRPCAuthority     string
	TraceID               string
	VLANID                string
	WorkerID              string
}

// OSIE or OS Installation Environment is the data about where the OSIE parts are located.
type OSIE struct {
	Kernel string
	Initrd string
}

type info struct {
	AllowNetboot bool
	Console      string
	MACAddress   net.HardwareAddr
	Arch         string
	VLANID       string
	WorkflowID   string
	Facility     string
	IPXEScript   string
	OSIE         OSIE
}

// pxeTemplate is the PXE extlinux.conf script template used to boot a machine via TFTP.
var pxeTemplate = `
default {{ if .AllowNetboot }}deploy{{ else }}local{{ end }}

label deploy
		kernel {{ .Kernel }}
		append console=tty1 console=ttyAMA0,115200 loglevel=7 cgroup_enable=cpuset cgroup_memory=1 cgroup_enable=memory {{- if ne .VLANID "" }} vlan_id={{ .VLANID }} {{- end }} facility={{ .Facility }} syslog_host={{ .SyslogHost }} grpc_authority={{ .TinkGRPCAuthority }} tinkerbell_tls={{ .TinkerbellTLS }} tinkerbell_insecure_tls={{ .TinkerbellInsecureTLS }} worker_id={{ .WorkerID }} hw_addr={{ .HWAddr }} modules=loop,squashfs,sd-mod,usb-storage intel_iommu=on iommu=pt {{- range .ExtraKernelParams}} {{.}} {{- end}}
		initrd {{ .Initrd }}
		ipappend 2

label local
    menu label Locally installed kernel
    append root=/dev/sda1
    localboot 1
`

// GenerateTemplate generates iPXE script content from template and hook data.
func GenerateTemplate(data Hook) ([]byte, error) {
	t := template.New("pxelinux.cfg")
	t, err := t.Parse(pxeTemplate)
	if err != nil {
		return []byte{}, err
	}
	buffer := new(bytes.Buffer)
	if err := t.Execute(buffer, data); err != nil {
		return []byte{}, err
	}

	return buffer.Bytes(), nil
}

// NewHandler creates a new PXELinux config handler with the given dependencies.
func NewHandler(backend BackendReader, config Config, log logr.Logger) func(filename string, rf io.ReaderFrom) error {
	return func(filename string, rf io.ReaderFrom) error {
		return handlePXELinux(filename, rf, backend, config, log)
	}
}

func handlePXELinux(filename string, rf io.ReaderFrom, backend BackendReader, config Config, log logr.Logger) error {
	// Extract MAC address from pxelinux.cfg/01-XX:XX:XX:XX:XX:XX format
	if !strings.HasPrefix(filename, pxeLinuxPrefix) {
		return fmt.Errorf("invalid pxelinux config filename: %s", filename)
	}

	macStr := strings.TrimPrefix(filename, pxeLinuxPrefix)
	log = log.WithValues("event", "pxelinux", "filename", filename, "macStr", macStr)

	// Parse MAC address
	mac, err := net.ParseMAC(macStr)
	if err != nil {
		log.Info("invalid MAC address for pxelinux.cfg request", "error", err)
		return fmt.Errorf("invalid MAC address for pxelinux.cfg request: %w", err)
	}

	// Create tracing context
	tracer := otel.Tracer("TFTP-PXELinux")
	ctx, span := tracer.Start(context.Background(), "TFTP pxelinux.cfg generation",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	// Get machine data from backend
	hwInfo, err := getByMac(ctx, mac, backend)
	if err != nil {
		log.Error(err, "backend lookup failed, using MAC address defaults", "mac", mac.String())
		return fmt.Errorf("failed to get machine info for MAC %s: %w", mac.String(), err)
	}

	if !hwInfo.AllowNetboot {
		e := errors.New("netboot not allowed for this machine")
		span.SetStatus(codes.Error, e.Error())
		log.Error(e, "mac", mac.String())
		return e
	}

	// Generate the iPXE script content
	content, err := generateFile(span, hwInfo, config)
	if err != nil {
		e := fmt.Errorf("failed to generate pxelinux config: %w", err)
		log.Error(e, "failed to generate pxelinux config")
		span.SetStatus(codes.Error, e.Error())
		return e
	}

	log.Info("serving generated pxelinux config", "size", len(content))

	// Serve the content
	return serveContent(content, rf, log, span, filename)
}

func getByMac(ctx context.Context, mac net.HardwareAddr, backend BackendReader) (info, error) {
	if backend == nil {
		return info{}, errors.New("backend is nil")
	}

	d, n, err := backend.GetByMac(ctx, mac)
	if err != nil {
		return info{}, err
	}

	return info{
		AllowNetboot: n.AllowNetboot,
		Console:      "",
		MACAddress:   d.MACAddress,
		Arch:         d.Arch,
		VLANID:       d.VLANID,
		WorkflowID:   d.MACAddress.String(),
		Facility:     n.Facility,
		IPXEScript:   n.IPXEScript,
		OSIE: OSIE{
			Kernel: n.OSIE.Kernel,
			Initrd: n.OSIE.Initrd,
		},
	}, nil
}

func generateFile(span trace.Span, hw info, config Config) ([]byte, error) {
	arch := hw.Arch
	if arch == "" {
		arch = "arm64"
	}

	// The worker ID will default to the mac address or use the one specified.
	wID := hw.MACAddress.String()
	if hw.WorkflowID != "" {
		wID = hw.WorkflowID
	}

	hook := Hook{
		AllowNetboot:          hw.AllowNetboot,
		Arch:                  arch,
		Console:               "",
		ExtraKernelParams:     config.ExtraKernelParams,
		Facility:              hw.Facility,
		HWAddr:                hw.MACAddress.String(),
		SyslogHost:            config.PublicSyslogFQDN,
		TinkerbellTLS:         config.TinkServerTLS,
		TinkerbellInsecureTLS: config.TinkServerInsecureTLS,
		TinkGRPCAuthority:     config.TinkServerGRPCAddr,
		VLANID:                hw.VLANID,
		WorkerID:              wID,
	}

	if hw.OSIE.Kernel != "" {
		hook.Kernel = hw.OSIE.Kernel
	} else {
		hook.Kernel = "vmlinuz-" + arch
	}

	if hw.OSIE.Initrd != "" {
		hook.Initrd = hw.OSIE.Initrd
	} else {
		hook.Initrd = "initramfs-" + arch
	}

	if span.SpanContext().IsSampled() {
		hook.TraceID = span.SpanContext().TraceID().String()
	}

	return GenerateTemplate(hook)
}

func serveContent(content []byte, rf io.ReaderFrom, log logr.Logger, span trace.Span, filename string) error {
	if transfer, ok := rf.(interface{ SetSize(int64) }); ok {
		transfer.SetSize(int64(len(content)))
	}

	reader := bytes.NewReader(content)
	bytesRead, err := rf.ReadFrom(reader)
	if err != nil {
		log.Error(err, "file serve failed", "bytesRead", bytesRead, "contentSize", len(content))
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	log.Info("file served", "bytesSent", bytesRead, "contentSize", len(content))
	span.SetStatus(codes.Ok, filename)
	return nil
}
