package script

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/tinkerbell/tinkerbell/smee/internal/hardware"
	"go.opentelemetry.io/otel/trace"
)

func TestOSIELocation(t *testing.T) {
	const def = "http://smee:7171/hook"
	tests := map[string]struct {
		ref, wantDir, wantName string
	}{
		"unset":                       {ref: "", wantDir: def, wantName: "default"},
		"absolute":                    {ref: "http://osie/images/vmlinuz", wantDir: "http://osie/images", wantName: "vmlinuz"},
		"absolute with iPXE settings": {ref: "http://osie/${arch}/vmlinuz-${arch}", wantDir: "http://osie/${arch}", wantName: "vmlinuz-${arch}"},
		"absolute with a query":       {ref: "https://osie/vmlinuz?token=x", wantDir: "https://osie", wantName: "vmlinuz?token=x"},
		"absolute without a path":     {ref: "http://osie:8080", wantDir: "http://osie:8080", wantName: "default"},
		"absolute directory":          {ref: "http://osie/images/", wantDir: "http://osie/images", wantName: ""},
		"relative name":               {ref: "vmlinuz-${arch}", wantDir: def, wantName: "vmlinuz-${arch}"},
		"relative path":               {ref: "captain/vmlinuz", wantDir: def, wantName: "captain/vmlinuz"},
		"relative to the host":        {ref: "/images/vmlinuz", wantDir: "http://smee:7171", wantName: "images/vmlinuz"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir, gotName := osieLocation(tt.ref, def, "default")
			if dir != tt.wantDir || gotName != tt.wantName {
				t.Fatalf("osieLocation(%q) = %q, %q, want %q, %q", tt.ref, dir, gotName, tt.wantDir, tt.wantName)
			}
		})
	}
}

// TestDefaultScriptOSIEURLs covers the v1alpha2 OSIE URLs in the iPXE script:
// the kernel and initrd may be in different places.
func TestDefaultScriptOSIEURLs(t *testing.T) {
	h := Handler{
		OSIEURL:              "http://smee:7171/hook",
		IPXEScriptRetries:    1,
		IPXEScriptRetryDelay: 1,
		InitrdName:           "captain-rootfs",
	}
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	tests := map[string]struct {
		osie hardware.OSIE
		want []string
		not  []string
	}{
		"kernel and initrd in one place": {
			osie: hardware.OSIE{KernelURL: "http://osie/hook/vmlinuz-${arch}", InitrdURL: "http://osie/hook/initramfs-${arch}"},
			want: []string{
				"set download-url http://osie/hook\n", "set kernel vmlinuz-${arch}\n", "set initrd initramfs-${arch}\n",
				"kernel ${download-url}/${kernel}", "initrd ${download-url}/${initrd}",
			},
			not: []string{"initrd-download-url"},
		},
		"kernel elsewhere, default initrd": {
			osie: hardware.OSIE{KernelURL: "http://osie/vmlinuz"},
			want: []string{
				"set download-url http://osie\n", "set initrd-download-url http://smee:7171/hook\n",
				"set kernel vmlinuz\n", "set initrd captain-rootfs-x86_64\n",
				"kernel ${download-url}/${kernel}", "initrd ${initrd-download-url}/${initrd}",
			},
		},
		"relative initrd": {
			osie: hardware.OSIE{InitrdURL: "custom/initrd.img"},
			want: []string{
				"set download-url http://smee:7171/hook\n", "set kernel vmlinuz-${arch}\n", "set initrd custom/initrd.img\n",
			},
			not: []string{"initrd-download-url"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sp := trace.SpanFromContext(context.Background())
			got, err := h.defaultScript(sp, hardware.Info{MACAddress: mac, Arch: "x86_64", OSIE: tt.osie}, h.settingsFor(ipv4))
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in:\n%s", w, got)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("want no %q in:\n%s", n, got)
				}
			}
		})
	}
}

func TestTFTPFileName(t *testing.T) {
	tests := map[string]struct {
		fileURL, name, want string
	}{
		"v1alpha1 name":         {name: "vmlinuz-${arch}", want: "vmlinuz-aarch64"},
		"unset":                 {want: ""},
		"relative URL":          {fileURL: "captain/vmlinuz-${arch}", name: "ignored", want: "captain/vmlinuz-aarch64"},
		"absolute URL":          {fileURL: "http://osie/images/vmlinuz-${arch}?x=1", want: "vmlinuz-aarch64"},
		"absolute URL, no file": {fileURL: "http://osie/", want: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tftpFileName(tt.fileURL, tt.name, "aarch64"); got != tt.want {
				t.Fatalf("tftpFileName(%q, %q) = %q, want %q", tt.fileURL, tt.name, got, tt.want)
			}
		})
	}
}
