package reservation

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/insomniacslk/dhcp/dhcpv4"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
)

// TestV1Alpha2DualStack covers an interface with both an IPv4 and an IPv6
// configuration: DHCPv4 serves the IPv4 one, under its own DHCP settings.
func TestV1Alpha2DualStack(t *testing.T) {
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	hw := func(dhcp *v1alpha2.DHCP) *v1alpha2.Hardware {
		return &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{NetworkInterfaces: v1alpha2.NetworkInterfaces{
			v1alpha2.MAC(mac.String()): {
				IPAM: &v1alpha2.IPAM{
					IPv4: &v1alpha2.IP{
						Address:          "192.168.2.10",
						Prefix:           "24",
						Gateway:          "192.168.2.1",
						Nameservers:      []v1alpha2.Nameserver{"192.168.2.53", "2001:db8::99"},
						DomainSearchList: []string{"four.example.com", "example.com"},
					},
					IPv6: &v1alpha2.IP{Address: "2001:db8::10", Nameservers: []v1alpha2.Nameserver{"2001:db8::53"}},
				},
				DHCP: dhcp,
			},
		}}}
	}
	tests := map[string]struct {
		dhcp         *v1alpha2.DHCP
		wantDisabled bool
		wantLease    time.Duration
	}{
		"IPv4 address and settings": {wantLease: 24 * time.Hour},
		"IPv4 lease time": {
			dhcp:      &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo[int64](600)}, IPv6: &v1alpha2.DHCPv6{ValidLifetime: ptrTo[int64](7200)}},
			wantLease: 10 * time.Minute,
		},
		"DHCPv6 disabled": {dhcp: &v1alpha2.DHCP{IPv6: &v1alpha2.DHCPv6{Disabled: true}}, wantLease: 24 * time.Hour},
		"DHCPv4 disabled": {dhcp: &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{Disabled: true}}, wantDisabled: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := &Handler{Log: logr.Discard(), Backend: v2Backend{hw: hw(tt.dhcp)}, IPAddr: netip.MustParseAddr("192.168.2.2")}
			d, n, err := h.readBackend(context.Background(), mac)
			if err != nil {
				t.Fatal(err)
			}
			if d.Disabled != tt.wantDisabled {
				t.Fatalf("disabled = %v, want %v", d.Disabled, tt.wantDisabled)
			}
			if tt.wantDisabled {
				return
			}
			pkt := &dhcpv4.DHCPv4{
				OpCode:       dhcpv4.OpcodeBootRequest,
				ClientHWAddr: mac,
				Options:      dhcpv4.OptionsFromList(dhcpv4.OptMessageType(dhcpv4.MessageTypeDiscover)),
			}
			reply := h.updateMsg(context.Background(), pkt, d, n, dhcpv4.MessageTypeOffer)
			if !reply.YourIPAddr.Equal(net.ParseIP("192.168.2.10")) {
				t.Errorf("yiaddr = %v, want 192.168.2.10", reply.YourIPAddr)
			}
			if got := reply.IPAddressLeaseTime(0); got != tt.wantLease {
				t.Errorf("lease time = %v, want %v", got, tt.wantLease)
			}
			if diff := cmp.Diff([]net.IP{net.ParseIP("192.168.2.53").To4()}, reply.DNS()); diff != "" {
				t.Errorf("DNS (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("four.example.com", reply.DomainName()); diff != "" {
				t.Errorf("domain name (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{"four.example.com", "example.com"}, reply.DomainSearch().Labels); diff != "" {
				t.Errorf("domain search (-want +got):\n%s", diff)
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }
