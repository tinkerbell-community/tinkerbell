package dhcp_test

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/smee/internal/dhcp"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
)

var cmpOpts = cmp.Options{cmpopts.EquateComparable(netip.Addr{})}

// TestConvertParity serves every v1alpha1 fixture through the v1alpha1
// conversion Smee used before, and through ConvertByMac and ConvertByIP on its
// v1alpha2 form as the API server serves it now, and compares what each
// consumer reads.
func TestConvertParity(t *testing.T) {
	ctx := context.Background()
	for _, f := range paritytest.Fixtures() {
		v2 := paritytest.V1Alpha2(t, f.Hardware)
		for _, mac := range f.MACs() {
			t.Run(f.Name+"/by MAC "+mac.String(), func(t *testing.T) {
				want, err := paritytest.ConvertByMac(mac, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				got, err := dhcp.ConvertByMac(ctx, mac, v2)
				if err != nil {
					t.Fatal(err)
				}
				compareParity(t, want, got)
			})
		}
		for _, ip := range f.IPs() {
			t.Run(f.Name+"/by IP "+ip.String(), func(t *testing.T) {
				want, err := paritytest.ConvertByIP(ip, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				got, err := dhcp.ConvertByIP(ctx, ip, v2)
				if err != nil {
					t.Fatal(err)
				}
				compareParity(t, want, got)
			})
		}
	}
}

// compareParity compares want, from v1alpha1, and got, from v1alpha2, the way
// Smee's consumers read them. v1alpha1 had one DHCP structure for both DHCPv4
// and DHCPv6; v1alpha2 has one per family.
func compareParity(t *testing.T, want, got dhcp.Hardware) {
	t.Helper()

	// The DHCPv4 handler answers when DHCP is enabled and there is an IPv4
	// address, and then serves all of it.
	if serves, gotServes := servesDHCPv4(want.DHCP), servesDHCPv4(got.DHCP); serves != gotServes {
		t.Errorf("DHCPv4 served = %v, want %v", gotServes, serves)
	} else if serves {
		if diff := cmp.Diff(want.DHCP, got.DHCP, cmpOpts); diff != "" {
			t.Errorf("DHCPv4 data differs (-v1alpha1 +v1alpha2):\n%s", diff)
		}
	}
	if diff := cmp.Diff(dhcpv6Of(want.DHCP), dhcpv6Of(got.DHCPv6), cmpOpts); diff != "" {
		t.Errorf("DHCPv6 data differs (-v1alpha1 +v1alpha2):\n%s", diff)
	}
	// The iPXE script, TFTP and ISO handlers read the interface's addressing.
	if diff := cmp.Diff(addressingOf(want.DHCP), addressingOf(got.Addressing()), cmpOpts); diff != "" {
		t.Errorf("addressing differs (-v1alpha1 +v1alpha2):\n%s", diff)
	}
	if got.VLANID() != want.DHCP.VLANID {
		t.Errorf("VLAN ID = %q, want %q", got.VLANID(), want.DHCP.VLANID)
	}
	if got.DHCP.Arch != want.DHCP.Arch || got.DHCP.MACAddress.String() != want.DHCP.MACAddress.String() {
		t.Errorf("arch, MAC = %q, %v, want %q, %v", got.DHCP.Arch, got.DHCP.MACAddress, want.DHCP.Arch, want.DHCP.MACAddress)
	}
	if diff := cmp.Diff(want.Netboot, got.Netboot, cmpOpts); diff != "" {
		t.Errorf("netboot data differs (-v1alpha1 +v1alpha2):\n%s", diff)
	}
	if diff := cmp.Diff(want.Isoboot, got.Isoboot, cmpOpts); diff != "" {
		t.Errorf("isoboot data differs (-v1alpha1 +v1alpha2):\n%s", diff)
	}
	if got.AgentID != want.AgentID {
		t.Errorf("agent ID = %q, want %q", got.AgentID, want.AgentID)
	}
}

func servesDHCPv4(d *dhcp.DHCP) bool {
	return d != nil && !d.Disabled && (d.IPAddress.Is4() || d.IPAddress.Is4In6())
}

// dhcpv6Data is what the DHCPv6 handlers read.
type dhcpv6Data struct {
	Disabled     bool
	Reservation  netip.Addr
	LeaseTime    uint32
	NameServers  []net.IP
	NTPServers   []net.IP
	DomainSearch []string
}

func dhcpv6Of(d *dhcp.DHCP) dhcpv6Data {
	out := dhcpv6Data{Disabled: d.Disabled, LeaseTime: d.LeaseTime, DomainSearch: d.DomainSearch}
	if d.IPAddress.Is6() && !d.IPAddress.Is4In6() {
		out.Reservation = d.IPAddress
	}
	if len(out.DomainSearch) == 0 && d.DomainName != "" {
		out.DomainSearch = []string{d.DomainName}
	}
	for _, s := range d.NameServers {
		if s.To4() == nil {
			out.NameServers = append(out.NameServers, s)
		}
	}
	for _, s := range d.NTPServers {
		if s.To4() == nil {
			out.NTPServers = append(out.NTPServers, s)
		}
	}
	return out
}

// addressingOf is what the ISO handler's static IPAM kernel argument reads.
func addressingOf(d *dhcp.DHCP) dhcp.DHCP {
	return dhcp.DHCP{
		MACAddress:     d.MACAddress,
		VLANID:         d.VLANID,
		IPAddress:      d.IPAddress,
		SubnetMask:     d.SubnetMask,
		DefaultGateway: d.DefaultGateway,
		Hostname:       d.Hostname,
		NameServers:    d.NameServers,
		DomainSearch:   d.DomainSearch,
		NTPServers:     d.NTPServers,
	}
}

// TestConvertV1Alpha1Differences covers v1alpha1 Hardware that the v1alpha1
// conversion failed on, or that v1alpha2 cannot represent per interface, and
// what is served for it now.
func TestConvertV1Alpha1Differences(t *testing.T) {
	ctx := context.Background()
	mac := net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, 0x01}

	t.Run("an interface without netboot data gets DHCP without netboot", func(t *testing.T) {
		hw := &v1alpha1.Hardware{Spec: v1alpha1.HardwareSpec{Interfaces: []v1alpha1.Interface{{
			DHCP: &v1alpha1.DHCP{MAC: mac.String(), IP: &v1alpha1.IP{Address: "192.168.2.10", Netmask: "255.255.255.0"}},
		}}}}
		if _, err := paritytest.ConvertByMac(mac, hw); err == nil {
			t.Fatal("v1alpha1 conversion succeeded, want it to fail for want of netboot data")
		}
		got, err := dhcp.ConvertByMac(ctx, mac, paritytest.V1Alpha2(t, hw))
		if err != nil {
			t.Fatal(err)
		}
		if !servesDHCPv4(got.DHCP) || got.Netboot.AllowNetboot {
			t.Fatalf("DHCPv4 served = %v, netboot allowed = %v, want DHCP without netboot", servesDHCPv4(got.DHCP), got.Netboot.AllowNetboot)
		}
	})

	t.Run("an upper case MAC address is found", func(t *testing.T) {
		// The v1alpha1 CRD rejects one, but the file backend took it.
		hw := &v1alpha1.Hardware{Spec: v1alpha1.HardwareSpec{Interfaces: []v1alpha1.Interface{{
			DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:AB", IP: &v1alpha1.IP{Address: "192.168.2.10", Netmask: "255.255.255.0"}},
			Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(true)},
		}}}}
		upper := net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, 0xab}
		if _, err := paritytest.ConvertByMac(upper, hw); err == nil {
			t.Fatal("v1alpha1 conversion succeeded, want it to miss the upper case MAC")
		}
		if _, err := dhcp.ConvertByMac(ctx, upper, paritytest.V1Alpha2(t, hw)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the first interface's OSIE settings apply to every interface", func(t *testing.T) {
		second := net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, 0x02}
		hw := &v1alpha1.Hardware{Spec: v1alpha1.HardwareSpec{Interfaces: []v1alpha1.Interface{
			{
				DHCP:    &v1alpha1.DHCP{MAC: mac.String()},
				Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(true), OSIE: &v1alpha1.OSIE{BaseURL: "http://one.example.com/hook"}},
			},
			{
				DHCP:    &v1alpha1.DHCP{MAC: second.String()},
				Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(true), OSIE: &v1alpha1.OSIE{BaseURL: "http://two.example.com/hook"}},
			},
		}}}
		v2 := paritytest.V1Alpha2(t, hw)
		first, err := dhcp.ConvertByMac(ctx, mac, v2)
		if err != nil {
			t.Fatal(err)
		}
		if first.Netboot.OSIE.BaseURL.String() != "http://one.example.com/hook" {
			t.Fatalf("first interface OSIE = %+v, want its own v1alpha1 base URL", first.Netboot.OSIE)
		}
		got, err := dhcp.ConvertByMac(ctx, second, v2)
		if err != nil {
			t.Fatal(err)
		}
		want := dhcp.OSIE{KernelURL: "http://one.example.com/hook/vmlinuz-${arch}", InitrdURL: "http://one.example.com/hook/initramfs-${arch}"}
		if diff := cmp.Diff(want, got.Netboot.OSIE, cmpOpts); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("OSIE settings changed through v1alpha2 are served as v1alpha2", func(t *testing.T) {
		hw := &v1alpha1.Hardware{Spec: v1alpha1.HardwareSpec{Interfaces: []v1alpha1.Interface{{
			DHCP:    &v1alpha1.DHCP{MAC: mac.String()},
			Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(true), OSIE: &v1alpha1.OSIE{BaseURL: "http://old.example.com"}},
		}}}}
		v2 := paritytest.V1Alpha2(t, hw)
		v2.Spec.Instance.OSIE = &v1alpha2.OSIE{KernelURL: "http://new.example.com/vmlinuz"}
		got, err := dhcp.ConvertByMac(ctx, mac, v2)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(dhcp.OSIE{KernelURL: "http://new.example.com/vmlinuz"}, got.Netboot.OSIE, cmpOpts); diff != "" {
			t.Fatal(diff)
		}
	})
}

func ptrTo[T any](v T) *T { return &v }
