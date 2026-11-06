package reservation

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/insomniacslk/dhcp/dhcpv6"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	v6 "github.com/tinkerbell/tinkerbell/smee/internal/dhcp/dhcpv6"
)

// TestHandleV1Alpha2Lifetimes covers the DHCPv6 lifetimes of dhcp.ipv6.
func TestHandleV1Alpha2Lifetimes(t *testing.T) {
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	tests := map[string]struct {
		dhcp                     *v1alpha2.DHCPv6
		preferred, valid, t1, t2 time.Duration
	}{
		"defaults": {
			dhcp:      &v1alpha2.DHCPv6{},
			preferred: 84 * time.Hour, valid: 168 * time.Hour, t1: 42 * time.Hour, t2: 4032 * time.Minute,
		},
		"preferred lifetime": {
			dhcp:      &v1alpha2.DHCPv6{PreferredLifetime: ptrTo[int64](1800), ValidLifetime: ptrTo[int64](7200)},
			preferred: 30 * time.Minute, valid: 2 * time.Hour, t1: 15 * time.Minute, t2: 24 * time.Minute,
		},
		"preferred lifetime over the valid lifetime": {
			dhcp:      &v1alpha2.DHCPv6{PreferredLifetime: ptrTo[int64](7200), ValidLifetime: ptrTo[int64](3600)},
			preferred: time.Hour, valid: time.Hour, t1: 30 * time.Minute, t2: 48 * time.Minute,
		},
		"preferred lifetime only": {
			dhcp:      &v1alpha2.DHCPv6{PreferredLifetime: ptrTo[int64](3600)},
			preferred: time.Hour, valid: 168 * time.Hour, t1: 30 * time.Minute, t2: 48 * time.Minute,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			handler := newV1Alpha2Handler(t, dualStack(mac, &v1alpha2.DHCP{IPv6: tt.dhcp}))
			conn := &recordingPacketConn{}
			msg := messageWithMAC(t, mac, dhcpv6.MessageTypeSolicit)
			addIANA(t, msg, [4]byte{9, 8, 7, 6})

			handler.Handle(context.Background(), conn, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}, msg)

			reply := requireSingleMessage(t, conn, dhcpv6.MessageTypeAdvertise)
			requireReservation(t, reply, [4]byte{9, 8, 7, 6}, net.ParseIP("2001:db8::10"), tt.preferred, tt.valid)
			requireIANATimers(t, reply, tt.t1, tt.t2)
		})
	}
}

// TestHandleV1Alpha2BootFile covers netboot.ipv6: the Hardware's boot file URL
// and parameters are served instead of the boot file Smee chooses.
func TestHandleV1Alpha2BootFile(t *testing.T) {
	mac := net.HardwareAddr{0, 1, 2, 3, 4, 5}
	const bootFile = "tftp://[2001:db8::5]/bootx64.efi"
	tests := map[string]struct {
		netboot      *v1alpha2.Netboot
		disabledBoot bool
		wantURL      string
		wantParams   []string
	}{
		"boot file and parameters": {
			netboot:    &v1alpha2.Netboot{IPv6: &v1alpha2.Netbootv6{BootFileURL: bootFile, BootFileParams: []string{"a=1", "b"}}},
			wantURL:    bootFile,
			wantParams: []string{"a=1", "b"},
		},
		"boot file with Smee's DHCPv6 netboot disabled": {
			netboot:      &v1alpha2.Netboot{IPv6: &v1alpha2.Netbootv6{BootFileURL: bootFile}},
			disabledBoot: true,
			wantURL:      bootFile,
		},
		"netboot disabled": {
			netboot: &v1alpha2.Netboot{Disabled: true, IPv6: &v1alpha2.Netbootv6{BootFileURL: bootFile}},
			wantURL: "http://boot.example/ipxe/script/00:01:02:03:04:05/netboot-not-allowed",
		},
		"parameters without a boot file": {
			netboot: &v1alpha2.Netboot{IPv6: &v1alpha2.Netbootv6{BootFileParams: []string{"a=1"}}},
			wantURL: "tftp://[2001:db8::1]/00:01:02:03:04:05/ipxe.efi",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			hw := dualStack(mac, nil)
			ni := hw.Spec.NetworkInterfaces[v1alpha2.MAC(mac.String())]
			ni.Netboot = tt.netboot
			hw.Spec.NetworkInterfaces[v1alpha2.MAC(mac.String())] = ni
			config := newHandlerConfig(t, &mockBackend{})
			config.Backend = v2Backend{hw: hw}
			if tt.disabledBoot {
				config.BootURLSource = v6.DisabledNetboot{}
			}
			handler, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			conn := &recordingPacketConn{}
			msg := messageWithMAC(t, mac, dhcpv6.MessageTypeSolicit)
			addIANA(t, msg, [4]byte{9, 8, 7, 6})
			requestBootURL(msg)

			handler.Handle(context.Background(), conn, &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}, msg)

			reply := requireSingleMessage(t, conn, dhcpv6.MessageTypeAdvertise)
			if got := reply.Options.BootFileURL(); got != tt.wantURL {
				t.Errorf("boot file URL = %q, want %q", got, tt.wantURL)
			}
			if diff := cmp.Diff(tt.wantParams, reply.Options.BootFileParam()); diff != "" {
				t.Errorf("boot file parameters (-want +got):\n%s", diff)
			}
		})
	}
}
