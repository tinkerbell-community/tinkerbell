package reservation

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/insomniacslk/dhcp/dhcpv6"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

type v2Backend struct{ hw *v1alpha2.Hardware }

func (b v2Backend) FilterHardwareV1Alpha2(context.Context, data.HardwareFilter) (*v1alpha2.Hardware, error) {
	return b.hw, nil
}

func newV1Alpha2Handler(t *testing.T, hw *v1alpha2.Hardware) *Handler {
	t.Helper()
	config := newHandlerConfig(t, &mockBackend{})
	config.Backend = v2Backend{hw: hw}
	handler, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// dualStack is v1alpha2 Hardware with both an IPv4 and an IPv6 configuration on
// one interface.
func dualStack(mac net.HardwareAddr, dhcp *v1alpha2.DHCP) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{NetworkInterfaces: v1alpha2.NetworkInterfaces{
		v1alpha2.MAC(mac.String()): {
			IPAM: &v1alpha2.IPAM{
				IPv4: &v1alpha2.IP{
					Address:          "192.168.2.10",
					Prefix:           "24",
					Nameservers:      []v1alpha2.Nameserver{"192.168.2.53", "2001:db8::99"},
					DomainSearchList: []string{"four.example.com"},
				},
				IPv6: &v1alpha2.IP{
					Address:          "2001:db8::10",
					Nameservers:      []v1alpha2.Nameserver{"2001:db8::53"},
					NTPServers:       []v1alpha2.Timeserver{"2001:db8::123"},
					DomainSearchList: []string{"six.example.com", "example.com"},
				},
			},
			DHCP: dhcp,
		},
	}}}
}

func TestHandleV1Alpha2DualStack(t *testing.T) {
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	tests := map[string]struct {
		dhcp      *v1alpha2.DHCP
		wantReply bool
		wantValid time.Duration
	}{
		"IPv6 address and settings": {
			wantReply: true,
			wantValid: defaultLeaseTime,
		},
		"IPv6 valid lifetime": {
			dhcp:      &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo[int64](600)}, IPv6: &v1alpha2.DHCPv6{ValidLifetime: ptrTo[int64](7200)}},
			wantReply: true,
			wantValid: 2 * time.Hour,
		},
		"DHCPv4 disabled": {
			dhcp:      &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{Disabled: true}},
			wantReply: true,
			wantValid: defaultLeaseTime,
		},
		"DHCPv6 disabled": {
			dhcp: &v1alpha2.DHCP{IPv6: &v1alpha2.DHCPv6{Disabled: true}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			handler := newV1Alpha2Handler(t, dualStack(mac, tt.dhcp))
			conn := &recordingPacketConn{}
			msg := messageWithMAC(t, mac, dhcpv6.MessageTypeSolicit)
			addIANA(t, msg, [4]byte{2, 3, 4, 5})
			dhcpv6.WithRequestedOptions(dhcpv6.OptionDNSRecursiveNameServer, dhcpv6.OptionDomainSearchList, dhcpv6.OptionNTPServer)(msg)

			handler.Handle(context.Background(), conn, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}, msg)

			if !tt.wantReply {
				if len(conn.writes) != 0 {
					t.Fatalf("got %d replies, want none", len(conn.writes))
				}
				return
			}
			reply := requireSingleMessage(t, conn, dhcpv6.MessageTypeAdvertise)
			requireReservation(t, reply, [4]byte{2, 3, 4, 5}, net.ParseIP("2001:db8::10"), tt.wantValid/2, tt.wantValid)
			if diff := cmp.Diff([]net.IP{net.ParseIP("2001:db8::53")}, reply.Options.DNS()); diff != "" {
				t.Errorf("DNS (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]net.IP{net.ParseIP("2001:db8::123")}, reply.Options.NTPServers()); diff != "" {
				t.Errorf("NTP (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{"six.example.com", "example.com"}, reply.Options.DomainSearchList().Labels); diff != "" {
				t.Errorf("domain search (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHandleV1Alpha2IPv4Only covers an interface with IPv4 addressing only: it
// has no IPv6 reservation, and is served the IPv6 name servers of its IPv4
// configuration with a derived address.
func TestHandleV1Alpha2IPv4Only(t *testing.T) {
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	hw := dualStack(mac, &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo[int64](600)}})
	ni := hw.Spec.NetworkInterfaces[v1alpha2.MAC(mac.String())]
	ni.IPAM.IPv6 = nil
	hw.Spec.NetworkInterfaces[v1alpha2.MAC(mac.String())] = ni

	t.Run("reservation", func(t *testing.T) {
		conn := &recordingPacketConn{}
		msg := messageWithMAC(t, mac, dhcpv6.MessageTypeSolicit)
		addIANA(t, msg, [4]byte{2, 3, 4, 5})
		newV1Alpha2Handler(t, hw).Handle(context.Background(), conn, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}, msg)
		if len(conn.writes) != 0 {
			t.Fatalf("got %d replies, want none", len(conn.writes))
		}
	})

	t.Run("derived", func(t *testing.T) {
		config := newHandlerConfig(t, &mockBackend{})
		config.Backend = v2Backend{hw: hw}
		config.Derived = &DerivedConfig{DirectAddressPool: netip.MustParsePrefix("2001:db8:1::/64"), RelayAddressPrefix: 64}
		handler, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		conn := &recordingPacketConn{}
		msg := messageWithMAC(t, mac, dhcpv6.MessageTypeSolicit)
		addIANA(t, msg, [4]byte{2, 3, 4, 5})
		dhcpv6.WithRequestedOptions(dhcpv6.OptionDNSRecursiveNameServer, dhcpv6.OptionDomainSearchList)(msg)
		handler.Handle(context.Background(), conn, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}, msg)

		reply := requireSingleMessage(t, conn, dhcpv6.MessageTypeAdvertise)
		addr := reply.Options.OneIANA().Options.OneAddress()
		// The DHCPv4 lease time is not a DHCPv6 lifetime.
		if addr.ValidLifetime != defaultLeaseTime {
			t.Errorf("valid lifetime = %v, want the default %v", addr.ValidLifetime, defaultLeaseTime)
		}
		if diff := cmp.Diff([]net.IP{net.ParseIP("2001:db8::99")}, reply.Options.DNS()); diff != "" {
			t.Errorf("DNS (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"four.example.com"}, reply.Options.DomainSearchList().Labels); diff != "" {
			t.Errorf("domain search (-want +got):\n%s", diff)
		}
	})
}

func ptrTo[T any](v T) *T { return &v }
