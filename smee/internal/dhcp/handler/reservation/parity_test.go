package reservation

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
)

type v2Backend struct{ hw *v1alpha2.Hardware }

func (b v2Backend) FilterHardware(context.Context, data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return b.hw, nil
}

// TestReplyParity answers DHCPv4 requests for every v1alpha1 fixture with the
// data Smee read from its v1alpha1 form before and with the data it reads from
// its v1alpha2 form now, and compares the replies.
func TestReplyParity(t *testing.T) {
	ctx := context.Background()
	for _, f := range paritytest.Fixtures() {
		h := &Handler{
			Log:     logr.Discard(),
			Backend: v2Backend{hw: paritytest.V1Alpha2(t, f.Hardware)},
			IPAddr:  netip.MustParseAddr("192.168.2.2"),
			Netboot: Netboot{
				Enabled:           true,
				IPXEBinServerTFTP: netip.MustParseAddrPort("192.168.2.2:69"),
				IPXEBinServerHTTP: &url.URL{Scheme: "http", Host: "192.168.2.2:8080"},
				IPXEScriptURL: func(*dhcpv4.DHCPv4) *url.URL {
					return &url.URL{Scheme: "http", Host: "192.168.2.2:8080", Path: "/auto.ipxe"}
				},
			},
			SyslogAddr:  netip.MustParseAddr("192.168.2.3"),
			DNSDefaults: DNSDefaults{NameServers: []net.IP{net.ParseIP("9.9.9.9")}, DomainSearch: []string{"default.example.com"}},
		}
		for _, mac := range f.MACs() {
			t.Run(f.Name+"/"+mac.String(), func(t *testing.T) {
				old, err := paritytest.ConvertByMac(mac, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				d, n, err := h.readBackend(ctx, mac)
				if err != nil {
					t.Fatal(err)
				}
				serves := !old.DHCP.Disabled && hasIPv4Reservation(old.DHCP)
				if gotServes := !d.Disabled && hasIPv4Reservation(d); gotServes != serves {
					t.Fatalf("answers = %v, want %v", gotServes, serves)
				}
				if !serves {
					return
				}
				for name, pkt := range parityRequests(mac) {
					for _, mt := range []dhcpv4.MessageType{dhcpv4.MessageTypeOffer, dhcpv4.MessageTypeAck} {
						want := h.updateMsg(ctx, pkt, old.DHCP, old.Netboot, mt)
						got := h.updateMsg(ctx, pkt, d, n, mt)
						if diff := cmp.Diff(want.Summary(), got.Summary()); diff != "" {
							t.Errorf("%s %s differs (-v1alpha1 +v1alpha2):\n%s", name, mt, diff)
						}
					}
				}
			})
		}
	}
}

// parityRequests returns DHCPv4 requests from mac: a plain client, a PXE
// client, an HTTP boot client and iPXE.
func parityRequests(mac net.HardwareAddr) map[string]*dhcpv4.DHCPv4 {
	netboot := func(class string, arch iana.Arch, extra ...dhcpv4.Option) *dhcpv4.DHCPv4 {
		opts := []dhcpv4.Option{
			dhcpv4.OptMessageType(dhcpv4.MessageTypeDiscover),
			dhcpv4.OptClassIdentifier(class),
			dhcpv4.OptClientArch(arch),
			dhcpv4.OptGeneric(dhcpv4.OptionClientNetworkInterfaceIdentifier, []byte{0x01, 0x02, 0x03, 0x04}),
			dhcpv4.OptGeneric(dhcpv4.OptionClientMachineIdentifier, make([]byte, 17)),
		}
		return &dhcpv4.DHCPv4{
			OpCode:        dhcpv4.OpcodeBootRequest,
			ClientHWAddr:  mac,
			TransactionID: dhcpv4.TransactionID{1, 2, 3, 4},
			Options:       dhcpv4.OptionsFromList(append(opts, extra...)...),
		}
	}
	return map[string]*dhcpv4.DHCPv4{
		"plain": {
			OpCode:        dhcpv4.OpcodeBootRequest,
			ClientHWAddr:  mac,
			TransactionID: dhcpv4.TransactionID{1, 2, 3, 4},
			Options:       dhcpv4.OptionsFromList(dhcpv4.OptMessageType(dhcpv4.MessageTypeDiscover)),
		},
		"PXE BIOS":  netboot("PXEClient:Arch:00000:UNDI:002001", iana.INTEL_X86PC),
		"HTTP EFI":  netboot("HTTPClient:Arch:00016:UNDI:003001", iana.EFI_X86_64_HTTP),
		"iPXE EFI":  netboot("PXEClient:Arch:00007:UNDI:003016", iana.EFI_X86_64, dhcpv4.OptUserClass("iPXE")),
		"PXE arm64": netboot("PXEClient:Arch:00011:UNDI:003000", iana.EFI_ARM64),
	}
}
