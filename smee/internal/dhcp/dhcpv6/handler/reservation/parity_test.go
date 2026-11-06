package reservation

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v6 "github.com/tinkerbell/tinkerbell/smee/internal/dhcp/dhcpv6"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
)

// TestReplyParity answers DHCPv6 requests for every v1alpha1 fixture with the
// data Smee read from its v1alpha1 form before and with the data it reads from
// its v1alpha2 form now, and compares the replies. The handler derives an
// address when Hardware has no IPv6 reservation, so IPv4 fixtures are answered
// too, with the lease time and servers of their IPv4 configuration.
func TestReplyParity(t *testing.T) {
	ctx := context.Background()
	peer := &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}
	for _, f := range paritytest.Fixtures() {
		hardware := map[string]*tinkerbell.Hardware{}
		for _, mac := range f.MACs() {
			hardware[mac.String()] = f.Hardware
		}
		h := newDerivedHandler(t, hardware, netip.MustParsePrefix("2001:db8:1::/64"), 64)
		for _, mac := range f.MACs() {
			t.Run(f.Name+"/"+mac.String(), func(t *testing.T) {
				old, err := paritytest.ConvertByMac(mac, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				hw, err := h.readBackend(ctx, mac)
				if err != nil {
					t.Fatal(err)
				}
				if hw.DHCPv6.Disabled != old.DHCP.Disabled {
					t.Fatalf("disabled = %v, want %v", hw.DHCPv6.Disabled, old.DHCP.Disabled)
				}
				for _, mt := range []dhcpv6.MessageType{dhcpv6.MessageTypeSolicit, dhcpv6.MessageTypeRequest} {
					msg := messageWithMAC(t, mac, mt)
					if mt == dhcpv6.MessageTypeRequest {
						withHandlerServerID(h)(msg)
					}
					dhcpv6.WithRequestedOptions(dhcpv6.OptionBootfileURL, dhcpv6.OptionDNSRecursiveNameServer,
						dhcpv6.OptionDomainSearchList, dhcpv6.OptionNTPServer)(msg)
					addIANA(t, msg, [4]byte{1, 2, 3, 4})
					i, err := v6.NewInfo(peer, msg, h.infoOptions...)
					if err != nil {
						t.Fatal(err)
					}
					wantAddr := h.addressFor(i, old.DHCP.IPAddress)
					gotAddr := h.addressFor(i, hw.DHCPv6.IPAddress)
					if gotAddr != wantAddr {
						t.Fatalf("%s address = %v, want %v", mt, gotAddr, wantAddr)
					}
					want, wantErr := h.reply(ctx, i, old.DHCP, old.Netboot, wantAddr)
					got, err := h.reply(ctx, i, hw.DHCPv6, hw.Netboot, gotAddr)
					if (err != nil) != (wantErr != nil) {
						t.Fatalf("%s error = %v, want %v", mt, err, wantErr)
					}
					if err != nil {
						continue
					}
					if !bytes.Equal(got.ToBytes(), want.ToBytes()) {
						t.Errorf("%s reply differs:\nv1alpha1: %s\nv1alpha2: %s", mt, want.Summary(), got.Summary())
					}
				}
			})
		}
	}
}

// TestReplyParityPXE checks the boot URL of a PXE client that is not iPXE.
func TestReplyParityPXE(t *testing.T) {
	ctx := context.Background()
	peer := &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}
	for _, f := range paritytest.Fixtures() {
		mac := f.MACs()[0]
		h := newDerivedHandler(t, map[string]*tinkerbell.Hardware{mac.String(): f.Hardware}, netip.MustParsePrefix("2001:db8:1::/64"), 64)
		t.Run(f.Name, func(t *testing.T) {
			old, err := paritytest.ConvertByMac(mac, f.Hardware)
			if err != nil {
				t.Fatal(err)
			}
			hw, err := h.readBackend(ctx, mac)
			if err != nil {
				t.Fatal(err)
			}
			msg := messageWithMAC(t, mac, dhcpv6.MessageTypeSolicit)
			dhcpv6.WithArchType(iana.EFI_ARM64)(msg)
			requestBootURL(msg)
			addIANA(t, msg, [4]byte{1, 2, 3, 4})
			i, err := v6.NewInfo(peer, msg, h.infoOptions...)
			if err != nil {
				t.Fatal(err)
			}
			want, _, wantErr := h.bootURL(ctx, i, old.DHCP, old.Netboot)
			got, _, err := h.bootURL(ctx, i, hw.DHCPv6, hw.Netboot)
			if got != want || (err != nil) != (wantErr != nil) {
				t.Fatalf("boot URL = %q, %v, want %q, %v", got, err, want, wantErr)
			}
		})
	}
}
