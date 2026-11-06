package script

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/smee/internal/hardware"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
	"go.opentelemetry.io/otel/trace"
)

// v2Backend serves one v1alpha2 Hardware, whatever the filter.
type v2Backend struct{ hw *v1alpha2.Hardware }

func (b v2Backend) FilterHardware(context.Context, data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return b.hw, nil
}

// v1alpha1Info is hardware.GetByMac as it was when Smee read v1alpha1 Hardware.
func v1alpha1Info(t *testing.T, mac net.HardwareAddr, hw *v1alpha1.Hardware) hardware.Info {
	t.Helper()
	old, err := paritytest.ConvertByMac(mac, hw)
	if err != nil {
		t.Fatalf("v1alpha1 conversion: %v", err)
	}
	d, n := old.DHCP, old.Netboot
	return hardware.Info{
		AllowNetboot:  n.AllowNetboot,
		MACAddress:    d.MACAddress,
		Arch:          d.Arch,
		VLANID:        d.VLANID,
		AgentID:       old.AgentID,
		Facility:      n.Facility,
		IPXEScript:    n.IPXEScript,
		IPXEScriptURL: n.IPXEScriptURL,
		OSIE:          hardware.OSIE(n.OSIE),
		PXELINUX:      hardware.PXELINUX(n.PXELINUX),
		RPI:           hardware.RPI(n.RPI),
	}
}

// parityHandlers are script handler configurations the OSIE settings depend on.
func parityHandlers() map[string]Handler {
	base := Handler{
		Logger:               logr.Discard(),
		OSIEURL:              "http://smee.example.com:7171/hook",
		OSIEURLv6:            "http://[2001:db8::7]:7171/hook",
		PublicSyslogFQDN:     "syslog.example.com",
		PublicSyslogFQDNV6:   "syslog6.example.com",
		TinkServerGRPCAddr:   "tink.example.com:42113",
		TinkServerGRPCAddrV6: "[2001:db8::7]:42113",
		IPXEScriptRetries:    10,
		IPXEScriptRetryDelay: 3,
		BootstrapPath:        "/bootstrap/",
	}
	names := base
	names.KernelName, names.InitrdName = "captain-kernel", "captain-rootfs"
	noPath := base
	noPath.OSIEURL, noPath.OSIEURLv6 = "http://smee.example.com:7171", "http://[2001:db8::7]:7171"
	return map[string]Handler{"defaults": base, "kernel and initrd names": names, "OSIE URL without a path": noPath}
}

// response is what a client gets for a boot script or TFTP config.
type response struct {
	Status int
	Body   string
}

// TestScriptParity serves the boot scripts and generated TFTP configs of every
// v1alpha1 fixture from its v1alpha1 form, the way Smee did before, and from its
// v1alpha2 form, through the handlers, and compares them.
func TestScriptParity(t *testing.T) {
	initMetrics()
	sp := trace.SpanFromContext(context.Background())
	for hname, base := range parityHandlers() {
		for _, f := range paritytest.Fixtures() {
			v2 := paritytest.V1Alpha2(t, f.Hardware)
			for _, mac := range f.MACs() {
				t.Run(hname+"/"+f.Name+"/"+mac.String(), func(t *testing.T) {
					h := base
					h.Backend = v2Backend{hw: v2}
					want := v1alpha1Info(t, mac, f.Hardware)

					for _, script := range []string{"auto.ipxe", "auto6.ipxe"} {
						rec := httptest.NewRecorder()
						h.HandlerFunc()(rec, httptest.NewRequest(http.MethodGet, "/"+mac.String()+"/"+script, nil))
						got := response{Status: rec.Code, Body: rec.Body.String()}
						if diff := cmp.Diff(v1alpha1Script(t, h, want, script), got); diff != "" {
							t.Errorf("%s differs (-v1alpha1 +v1alpha2):\n%s", script, diff)
						}
					}

					for _, file := range []struct{ name, tmpl string }{
						{"pxelinux.cfg/01-" + mac.String(), pxeTemplate},
						{strings.ReplaceAll(mac.String(), ":", "-") + "/config.txt", rpiConfigTemplate},
						{strings.ReplaceAll(mac.String(), ":", "-") + "/cmdline.txt", rpiCmdlineTemplate},
					} {
						wantBody := ""
						if want.AllowNetboot {
							var err error
							if wantBody, err = h.tftpConfig(sp, want, file.tmpl); err != nil {
								t.Fatal(err)
							}
						}
						var buf bytes.Buffer
						err := h.HandleTFTP(file.name, &buf)
						if (err != nil) == want.AllowNetboot {
							t.Fatalf("%s: error = %v, netboot allowed = %v", file.name, err, want.AllowNetboot)
						}
						if diff := cmp.Diff(wantBody, buf.String()); diff != "" {
							t.Errorf("%s differs (-v1alpha1 +v1alpha2):\n%s", file.name, diff)
						}
					}
				})
			}
		}
	}
}

// TestInfoParity compares the hardware.Info the script, PXELINUX and Raspberry
// Pi routes read, looked up by MAC and by IP address, with what they read from
// the v1alpha1 form before.
func TestInfoParity(t *testing.T) {
	ctx := context.Background()
	for _, f := range paritytest.Fixtures() {
		backend := v2Backend{hw: paritytest.V1Alpha2(t, f.Hardware)}
		for _, mac := range f.MACs() {
			t.Run(f.Name+"/"+mac.String(), func(t *testing.T) {
				got, err := hardware.GetByMac(ctx, mac, backend)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(v1alpha1Info(t, mac, f.Hardware), got); diff != "" {
					t.Errorf("by MAC (-v1alpha1 +v1alpha2):\n%s", diff)
				}
			})
		}
		for _, ip := range f.IPs() {
			t.Run(f.Name+"/"+ip.String(), func(t *testing.T) {
				old, err := paritytest.ConvertByIP(ip, f.Hardware)
				if err != nil {
					t.Fatal(err)
				}
				got, err := hardware.GetByIP(ctx, ip, backend)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(v1alpha1Info(t, old.DHCP.MACAddress, f.Hardware), got); diff != "" {
					t.Errorf("by IP (-v1alpha1 +v1alpha2):\n%s", diff)
				}
			})
		}
	}
}

// v1alpha1Script is what the script handler served for info.
func v1alpha1Script(t *testing.T, h Handler, info hardware.Info, script string) response {
	t.Helper()
	rec := httptest.NewRecorder()
	if !info.AllowNetboot {
		h.serveNetbootNotAllowed(rec, "mac: "+info.MACAddress.String())
	} else {
		family := ipv4
		if script == "auto6.ipxe" {
			family = ipv6
		}
		h.serveBootScript(context.Background(), rec, script, info, h.settingsFor(family))
	}
	return response{Status: rec.Code, Body: rec.Body.String()}
}

// TestOSIEURLsServeV1Alpha1Scripts serves the OSIE settings of the v1alpha1
// fixtures as v1alpha2 alone, without the v1alpha1 form the conversion keeps,
// to show where the v1alpha2 URLs locate the same OSIE files and where Smee
// needs the v1alpha1 form to.
func TestOSIEURLsServeV1Alpha1Scripts(t *testing.T) {
	// Fixture and handler pairs whose OSIE the v1alpha2 URLs locate differently.
	differs := map[string]string{
		"defaults/OSIE base URL without a path":                "v1alpha1 used the default OSIE URL's path with a base URL that has none",
		"kernel and initrd names/OSIE base URL without a path": "v1alpha1 used the default OSIE URL's path with a base URL that has none",
		"defaults/OSIE base URL with a trailing slash":         "v1alpha1 joined file names to the base URL with a second slash",
		"kernel and initrd names/OSIE base URL with a trailing slash": "v1alpha1 joined file names to the base URL with a second slash, " +
			"and used the configured initrd name",
		"OSIE URL without a path/OSIE base URL with a trailing slash": "v1alpha1 joined file names to the base URL with a second slash",
		"kernel and initrd names/OSIE base URL only":                  "v1alpha1 used the configured kernel and initrd names",
		"kernel and initrd names/two interfaces":                      "v1alpha1 used the configured kernel and initrd names",
	}
	sp := trace.SpanFromContext(context.Background())
	for hname, h := range parityHandlers() {
		for _, f := range paritytest.Fixtures() {
			v2 := paritytest.V1Alpha2(t, f.Hardware)
			if v2.Spec.Instance == nil || v2.Spec.Instance.OSIE == nil || v2.Spec.Instance.OSIE.KernelURL == "" {
				continue
			}
			delete(v2.Annotations, conversion.AnnotationV1Alpha1)
			name := hname + "/" + f.Name
			mac := f.MACs()[0]
			t.Run(name, func(t *testing.T) {
				want := v1alpha1Info(t, mac, f.Hardware)
				want.Facility = "" // only kept with the v1alpha1 form
				got, err := hardware.GetByMac(context.Background(), mac, v2Backend{hw: v2})
				if err != nil {
					t.Fatal(err)
				}
				wantScript, err := h.defaultScript(sp, want, h.settingsFor(ipv4))
				if err != nil {
					t.Fatal(err)
				}
				gotScript, err := h.defaultScript(sp, got, h.settingsFor(ipv4))
				if err != nil {
					t.Fatal(err)
				}
				wantFetches, gotFetches := osieFetches(wantScript), osieFetches(gotScript)
				reason, known := differs[name]
				switch {
				case known && reason != "" && wantFetches == gotFetches:
					t.Errorf("v1alpha2 URLs locate the same files, but are expected to differ: %s", reason)
				case (!known || reason == "") && wantFetches != gotFetches:
					t.Errorf("v1alpha2 URLs locate other files: v1alpha1 %s, v1alpha2 %s", wantFetches, gotFetches)
				}
			})
		}
	}
}

// osieFetches returns the kernel and initrd URLs a boot script fetches, with
// its download URLs and file names expanded.
func osieFetches(script string) string {
	vars := map[string]string{}
	for _, line := range strings.Split(script, "\n") {
		if rest, ok := strings.CutPrefix(line, "set "); ok {
			if k, v, ok := strings.Cut(rest, " "); ok {
				vars[k] = v
			}
		}
	}
	expand := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "${"+k+"}", v)
		}
		return s
	}
	var out []string
	for _, line := range strings.Split(script, "\n") {
		for _, cmd := range []string{"kernel ", "initrd "} {
			if rest, ok := strings.CutPrefix(line, cmd); ok && strings.HasPrefix(rest, "${") {
				out = append(out, expand(expand(strings.Fields(rest)[0])))
			}
		}
	}
	return strings.Join(out, " ")
}
