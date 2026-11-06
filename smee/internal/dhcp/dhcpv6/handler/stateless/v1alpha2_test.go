package stateless

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

type v2Backend struct{ hw *v1alpha2.Hardware }

func (b v2Backend) FilterHardware(context.Context, data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return b.hw, nil
}

func v1alpha2Interface(mac net.HardwareAddr, ni v1alpha2.NetworkInterface) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{NetworkInterfaces: v1alpha2.NetworkInterfaces{v1alpha2.MAC(mac.String()): ni}}}
}

// TestHandleV1Alpha2 covers the DHCPv6 settings only v1alpha2 has: the
// information refresh time and boot file of the Hardware, and DHCPv6 that is
// disabled while DHCPv4 is not.
func TestHandleV1Alpha2(t *testing.T) {
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	const bootFile = "http://[2001:db8::5]/bootx64.efi"
	tests := map[string]struct {
		ni          v1alpha2.NetworkInterface
		configured  time.Duration
		wantNoReply bool
		wantRefresh time.Duration
		wantURL     string
		wantParams  []string
		wantDNS     []net.IP
	}{
		"server defaults": {
			ni:          v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv6: &v1alpha2.IP{Nameservers: []v1alpha2.Nameserver{"2001:db8::53"}}}},
			configured:  time.Hour,
			wantRefresh: time.Hour,
			wantURL:     "tftp://[2001:db8::1]/00:01:02:03:04:05/ipxe.efi",
			wantDNS:     []net.IP{net.ParseIP("2001:db8::53")},
		},
		"Hardware refresh time": {
			ni:          v1alpha2.NetworkInterface{DHCP: &v1alpha2.DHCP{IPv6: &v1alpha2.DHCPv6{InformationRefreshTime: ptrTo[int64](7200)}}},
			configured:  time.Hour,
			wantRefresh: 2 * time.Hour,
			wantURL:     "tftp://[2001:db8::1]/00:01:02:03:04:05/ipxe.efi",
		},
		"Hardware refresh time below the minimum": {
			ni:          v1alpha2.NetworkInterface{DHCP: &v1alpha2.DHCP{IPv6: &v1alpha2.DHCPv6{InformationRefreshTime: ptrTo[int64](60)}}},
			wantRefresh: minInformationRefreshTime,
			wantURL:     "tftp://[2001:db8::1]/00:01:02:03:04:05/ipxe.efi",
		},
		"Hardware boot file": {
			ni: v1alpha2.NetworkInterface{Netboot: &v1alpha2.Netboot{IPv6: &v1alpha2.Netbootv6{
				BootFileURL: bootFile, BootFileParams: []string{"console=ttyS0"},
			}}},
			wantRefresh: defaultInformationRefreshTime,
			wantURL:     bootFile,
			wantParams:  []string{"console=ttyS0"},
		},
		"DHCPv6 disabled": {
			ni:          v1alpha2.NetworkInterface{DHCP: &v1alpha2.DHCP{IPv6: &v1alpha2.DHCPv6{Disabled: true}}},
			wantNoReply: true,
		},
		"DHCPv4 disabled": {
			ni:          v1alpha2.NetworkInterface{DHCP: &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{Disabled: true}}},
			wantRefresh: defaultInformationRefreshTime,
			wantURL:     "tftp://[2001:db8::1]/00:01:02:03:04:05/ipxe.efi",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			config := newHandlerConfig(t, &mockBackend{}, false)
			config.Backend = v2Backend{hw: v1alpha2Interface(mac, tt.ni)}
			config.InformationRefreshTime = tt.configured
			handler, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			conn := &recordingPacketConn{}
			msg := messageWithMAC(t, mac, dhcpv6.MessageTypeInformationRequest, nil, iana.EFI_X86_64)
			requestBootURL(msg)
			requestStatelessConfig(msg)

			handler.Handle(context.Background(), conn, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}, msg)

			if tt.wantNoReply {
				if len(conn.writes) != 0 {
					t.Fatalf("got %d replies, want none", len(conn.writes))
				}
				return
			}
			reply := requireSingleMessageReply(t, conn)
			if got := reply.Options.InformationRefreshTime(0); got != tt.wantRefresh {
				t.Errorf("information refresh time = %v, want %v", got, tt.wantRefresh)
			}
			if got := reply.Options.BootFileURL(); got != tt.wantURL {
				t.Errorf("boot file URL = %q, want %q", got, tt.wantURL)
			}
			if diff := cmp.Diff(tt.wantParams, reply.Options.BootFileParam()); diff != "" {
				t.Errorf("boot file parameters (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantDNS, reply.Options.DNS()); diff != "" {
				t.Errorf("DNS (-want +got):\n%s", diff)
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }
