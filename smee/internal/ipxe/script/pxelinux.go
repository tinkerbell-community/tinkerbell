package script

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tinkerbell/tinkerbell/smee/internal/metric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// PXELinuxScript is the PXELinux/extlinux.conf template used to boot U-Boot machines via PXE.
// This config uses HTTP URLs for kernel/initrd downloads (faster than TFTP).
var PXELinuxScript = `#!pxelinux
default {{ if .AllowNetboot }}deploy{{ else }}local{{ end }}

label deploy
	kernel {{ .Kernel }}
	append console=tty1 console=ttyAMA0,115200 loglevel=7 cgroup_enable=cpuset cgroup_memory=1 cgroup_enable=memory {{- if ne .VLANID "" }} vlan_id={{ .VLANID }} {{- end }} facility={{ .Facility }} syslog_host={{ .SyslogHost }} grpc_authority={{ .TinkGRPCAuthority }} tinkerbell_tls={{ .TinkerbellTLS }} tinkerbell_insecure_tls={{ .TinkerbellInsecureTLS }} worker_id={{ .WorkerID }} hw_addr={{ .HWAddr }} modules=loop,squashfs,sd-mod,usb-storage intel_iommu=on iommu=pt {{- range .ExtraKernelParams}} {{.}} {{- end}}
	initrd {{ .Initrd }}
{{- if .DeviceTreeBlob }}
	fdt {{ .DeviceTreeBlob }}
{{- end }}

label local
	menu label Locally installed kernel
	append root=/dev/sda1
	localboot 1
`

// PXELinuxHook holds the values used to generate PXELinux configs for U-Boot PXE boot.
// This mirrors the Hook struct used for iPXE scripts.
type PXELinuxHook struct {
	AllowNetboot          bool
	Arch                  string   // example arm64
	Console               string   // example ttyS1,115200
	DeviceTreeBlob        string   // HTTP URL to DTB file (for ARM boards)
	DownloadURL           string   // Base URL for kernel/initrd downloads
	ExtraKernelParams     []string // example tink_worker_image=quay.io/tinkerbell/tink-worker:v0.8.0
	Facility              string
	HWAddr                string // example 3c:ec:ef:4c:4f:54
	Initrd                string // name of the initrd file
	Kernel                string // name of the kernel file
	SyslogHost            string
	TinkerbellInsecureTLS bool
	TinkerbellTLS         bool
	TinkGRPCAuthority     string // example 192.168.2.111:42113
	TraceID               string
	VLANID                string // string number between 1-4095
	WorkerID              string // example 3c:ec:ef:4c:4f:54 or worker1
}

// HandlerFuncPXELinux returns a http.HandlerFunc that serves PXELinux configs for U-Boot.
// It expects request paths like /<mac>/pxelinux.cfg or /pxelinux.cfg/01-<mac>.
func (h *Handler) HandlerFuncPXELinux() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Support both path patterns:
		// 1. /pxelinux.cfg/01-aa-bb-cc-dd-ee-ff (DHCP bootfile pattern)
		// 2. /<mac>/pxelinux.cfg (similar to auto.ipxe pattern)
		var mac net.HardwareAddr
		var err error

		pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

		// Check for supported path patterns
		switch {
		case len(pathParts) >= 2 && pathParts[0] == "pxelinux.cfg" && strings.HasPrefix(pathParts[1], "01-"):
			// Pattern: /pxelinux.cfg/01-<mac>
			macStr := strings.TrimPrefix(pathParts[1], "01-")
			mac, err = net.ParseMAC(macStr)
			if err != nil {
				h.Logger.Info("invalid MAC in pxelinux.cfg path", "path", r.URL.Path, "error", err)
				w.WriteHeader(http.StatusNotFound)
				return
			}
		case len(pathParts) >= 2 && pathParts[1] == "pxelinux.cfg":
			// Pattern: /<mac>/pxelinux.cfg
			mac, err = net.ParseMAC(pathParts[0])
			if err != nil {
				h.Logger.Info("invalid MAC in path", "path", r.URL.Path, "error", err)
				w.WriteHeader(http.StatusNotFound)
				return
			}
		default:
			h.Logger.Info("URL path not supported for pxelinux", "path", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		labels := prometheus.Labels{"from": "http", "op": "pxelinux"}
		metric.JobsTotal.With(labels).Inc()
		metric.JobsInProgress.With(labels).Inc()
		defer metric.JobsInProgress.With(labels).Dec()
		timer := prometheus.NewTimer(metric.JobDuration.With(labels))
		defer timer.ObserveDuration()

		ctx := r.Context()

		// Get hardware data from backend
		hw, err := getByMac(ctx, mac, h.Backend)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			h.Logger.Info("hardware not found or backend error", "mac", mac.String(), "error", err)
			return
		}

		if !hw.AllowNetboot {
			w.WriteHeader(http.StatusNotFound)
			h.Logger.Info("netboot not allowed for this machine", "mac", mac.String())
			return
		}

		h.servePXELinuxConfig(ctx, w, hw)
	}
}

func (h *Handler) servePXELinuxConfig(ctx context.Context, w http.ResponseWriter, hw info) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("smee.config_type", "pxelinux"))

	script, err := h.generatePXELinuxConfig(span, hw)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		h.Logger.Error(err, "error generating pxelinux config")
		span.SetStatus(codes.Error, err.Error())
		return
	}

	span.SetAttributes(attribute.String("pxelinux-config", script))

	if _, err := w.Write([]byte(script)); err != nil {
		h.Logger.Error(err, "unable to write pxelinux config")
		span.SetStatus(codes.Error, err.Error())
		return
	}
}

func (h *Handler) generatePXELinuxConfig(span trace.Span, hw info) (string, error) {
	mac := hw.MACAddress
	arch := hw.Arch
	if arch == "" {
		arch = "arm64" // default for U-Boot/Raspberry Pi
	}

	// The worker ID will default to the mac address or use the one specified.
	wID := mac.String()
	if hw.WorkflowID != "" {
		wID = hw.WorkflowID
	}

	// Build kernel and initrd URLs
	// Use HTTP URL from OSIEURL or construct from DownloadURL
	var downloadURL string
	if hw.OSIE.BaseURL != nil && hw.OSIE.BaseURL.String() != "" {
		downloadURL = hw.OSIE.BaseURL.String()
	} else {
		downloadURL = h.OSIEURL
	}

	// Parse the download URL to construct HTTP paths
	baseURL, err := url.Parse(downloadURL)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}

	// Determine kernel and initrd filenames
	kernelName := hw.OSIE.Kernel
	if kernelName == "" {
		kernelName = "vmlinuz-" + arch
	}
	initrdName := hw.OSIE.Initrd
	if initrdName == "" {
		initrdName = "initramfs-" + arch
	}

	// Construct full HTTP URLs for kernel and initrd
	kernelURL := baseURL.JoinPath(kernelName).String()
	initrdURL := baseURL.JoinPath(initrdName).String()

	pxeHook := PXELinuxHook{
		AllowNetboot:          hw.AllowNetboot,
		Arch:                  arch,
		Console:               "",
		DownloadURL:           downloadURL,
		ExtraKernelParams:     h.ExtraKernelParams,
		Facility:              hw.Facility,
		HWAddr:                mac.String(),
		Initrd:                initrdURL, // Full HTTP URL
		Kernel:                kernelURL, // Full HTTP URL
		SyslogHost:            h.PublicSyslogFQDN,
		TinkerbellTLS:         h.TinkServerTLS,
		TinkerbellInsecureTLS: h.TinkServerInsecureTLS,
		TinkGRPCAuthority:     h.TinkServerGRPCAddr,
		VLANID:                hw.VLANID,
		WorkerID:              wID,
	}

	// For ARM boards, add device tree blob if available
	// This would be configured in hardware data if needed
	// pxeHook.DeviceTreeBlob = baseURL.JoinPath("bcm2711-rpi-4-b.dtb").String()

	if span.SpanContext().IsSampled() {
		pxeHook.TraceID = span.SpanContext().TraceID().String()
	}

	return GenerateTemplate(pxeHook, PXELinuxScript)
}

// HandlerFuncPXELinuxDefault serves a default pxelinux config for unrecognized machines.
// This is useful for debugging and provides a fallback boot path.
func (h *Handler) HandlerFuncPXELinuxDefault() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only serve if path is exactly /pxelinux.cfg/default
		if path.Clean(r.URL.Path) != "/pxelinux.cfg/default" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		h.Logger.Info("serving default pxelinux config")

		// Serve a minimal config that tells the machine to boot locally
		defaultConfig := `#!pxelinux
default local

label local
	menu label Boot from local disk
	localboot 1
`
		if _, err := w.Write([]byte(defaultConfig)); err != nil {
			h.Logger.Error(err, "unable to write default pxelinux config")
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}
