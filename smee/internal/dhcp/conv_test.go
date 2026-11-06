package dhcp

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var testMAC = net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}

func hardwareWith(ni v1alpha2.NetworkInterface) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw", Namespace: "tink"},
		Spec: v1alpha2.HardwareSpec{
			NetworkInterfaces: v1alpha2.NetworkInterfaces{"aa:bb:cc:dd:ee:ff": ni},
		},
	}
}

func TestConvertByMac(t *testing.T) {
	tests := map[string]struct {
		hw        *v1alpha2.Hardware
		mac       net.HardwareAddr
		want      Hardware
		shouldErr bool
	}{
		"nil hardware":     {shouldErr: true},
		"no interfaces":    {hw: &v1alpha2.Hardware{}, shouldErr: true},
		"no matching MAC":  {hw: hardwareWith(v1alpha2.NetworkInterface{}), mac: net.HardwareAddr{0, 1, 2, 3, 4, 5}, shouldErr: true},
		"bad iPXE URL":     {hw: hardwareWith(v1alpha2.NetworkInterface{Netboot: &v1alpha2.Netboot{IPXE: &v1alpha2.IPXE{URL: "bad"}}}), shouldErr: true},
		"bad address":      {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "bad"}}}), shouldErr: true},
		"IPv4 no prefix":   {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "192.168.2.4"}}}), shouldErr: true},
		"IPv4 bad prefix":  {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "192.168.2.4", Prefix: "33"}}}), shouldErr: true},
		"bad gateway":      {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "192.168.2.4", Prefix: "24", Gateway: "bad"}}}), shouldErr: true},
		"bad IPv6 address": {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv6: &v1alpha2.IP{Address: "bad"}}}), shouldErr: true},
		"bad route": {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{
			ClasslessStaticRoutes: []v1alpha2.ClasslessStaticRoute{{DestinationDescriptor: "10.0.0.0/33", Router: "192.168.2.1"}},
		}}}), shouldErr: true},
		"bad route router": {hw: hardwareWith(v1alpha2.NetworkInterface{IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{
			ClasslessStaticRoutes: []v1alpha2.ClasslessStaticRoute{{DestinationDescriptor: "10.0.0.0/8", Router: "bad"}},
		}}}), shouldErr: true},
		"bad ISO URL": {
			hw: func() *v1alpha2.Hardware {
				hw := hardwareWith(v1alpha2.NetworkInterface{})
				hw.Spec.Instance = &v1alpha2.Instance{OSIE: &v1alpha2.OSIE{ISOURL: "http://[::1"}}
				return hw
			}(),
			shouldErr: true,
		},
		"empty interface": {
			hw: hardwareWith(v1alpha2.NetworkInterface{}),
			want: Hardware{
				DHCP:    &DHCP{MACAddress: testMAC, LeaseTime: defaultLeaseTime},
				DHCPv6:  &DHCP{MACAddress: testMAC},
				Netboot: &Netboot{AllowNetboot: true},
			},
		},
		"MAC is matched case insensitively": {
			hw: &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{NetworkInterfaces: v1alpha2.NetworkInterfaces{"AA:BB:CC:DD:EE:FF": {}}}},
			want: Hardware{
				DHCP:    &DHCP{MACAddress: testMAC, LeaseTime: defaultLeaseTime},
				DHCPv6:  &DHCP{MACAddress: testMAC},
				Netboot: &Netboot{AllowNetboot: true},
			},
		},
		"dual stack": {
			hw: &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{
				AgentID: "agent",
				Arch:    "aarch64",
				Instance: &v1alpha2.Instance{OSIE: &v1alpha2.OSIE{
					KernelURL:    "http://osie/vmlinuz-${arch}",
					InitrdURL:    "initramfs",
					KernelParams: []string{"a=1"},
					ISOURL:       "http://iso/hook.iso",
				}},
				NetworkInterfaces: v1alpha2.NetworkInterfaces{"aa:bb:cc:dd:ee:ff": {
					IPAM: &v1alpha2.IPAM{
						IPv4: &v1alpha2.IP{
							Address:          "192.168.2.10",
							Prefix:           "24",
							Gateway:          "192.168.2.1",
							Hostname:         ptrTo("four"),
							VLANID:           ptrTo("40"),
							Nameservers:      []v1alpha2.Nameserver{"1.1.1.1", "dns.example.com"},
							NTPServers:       []v1alpha2.Timeserver{"132.163.96.2"},
							DomainSearchList: []string{"example.com", "example.org"},
							ClasslessStaticRoutes: []v1alpha2.ClasslessStaticRoute{
								{DestinationDescriptor: "10.0.0.0/8", Router: "192.168.2.254"},
							},
						},
						IPv6: &v1alpha2.IP{
							Address:          "2001:db8::10",
							Prefix:           "64",
							Gateway:          "2001:db8::1",
							Hostname:         ptrTo("six"),
							VLANID:           ptrTo("60"),
							Nameservers:      []v1alpha2.Nameserver{"2001:db8::53"},
							NTPServers:       []v1alpha2.Timeserver{"2001:db8::123"},
							DomainSearchList: []string{"six.example.com"},
						},
					},
					DHCP: &v1alpha2.DHCP{
						IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo[int64](3600)},
						IPv6: &v1alpha2.DHCPv6{Disabled: true, ValidLifetime: ptrTo[int64](7200)},
					},
					Netboot: &v1alpha2.Netboot{
						IPv4:     &v1alpha2.Netbootv4{TFTPServerName: "192.168.2.5", BootFileName: "boot.efi"},
						IPXE:     &v1alpha2.IPXE{URL: "http://boot/custom.ipxe", Binary: "ipxe.efi"},
						PXELINUX: &v1alpha2.PXELINUX{Config: "default deploy"},
						RPI:      &v1alpha2.RPI{SerialNum: "1234abcd", FirmwarePath: "fw", ConfigTxt: "x=1"},
					},
				}},
			}},
			want: Hardware{
				AgentID: "agent",
				DHCP: &DHCP{
					MACAddress:       testMAC,
					IPAddress:        netip.MustParseAddr("192.168.2.10"),
					SubnetMask:       net.CIDRMask(24, 32),
					DefaultGateway:   netip.MustParseAddr("192.168.2.1"),
					NameServers:      []net.IP{net.ParseIP("1.1.1.1")},
					Hostname:         "four",
					DomainName:       "example.com",
					BroadcastAddress: netip.MustParseAddr("192.168.2.255"),
					NTPServers:       []net.IP{net.ParseIP("132.163.96.2")},
					VLANID:           "40",
					LeaseTime:        3600,
					TFTPServerName:   "192.168.2.5",
					BootFileName:     "boot.efi",
					Arch:             "aarch64",
					DomainSearch:     []string{"example.com", "example.org"},
					ClasslessStaticRoutes: dhcpv4.Routes{{
						Dest:   &net.IPNet{IP: net.IPv4(10, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)},
						Router: net.ParseIP("192.168.2.254"),
					}},
				},
				DHCPv6: &DHCP{
					MACAddress:     testMAC,
					IPAddress:      netip.MustParseAddr("2001:db8::10"),
					DefaultGateway: netip.MustParseAddr("2001:db8::1"),
					NameServers:    []net.IP{net.ParseIP("2001:db8::53")},
					Hostname:       "six",
					DomainName:     "six.example.com",
					NTPServers:     []net.IP{net.ParseIP("2001:db8::123")},
					VLANID:         "60",
					LeaseTime:      7200,
					Arch:           "aarch64",
					DomainSearch:   []string{"six.example.com"},
					Disabled:       true,
				},
				Netboot: &Netboot{
					AllowNetboot:  true,
					IPXEScriptURL: &url.URL{Scheme: "http", Host: "boot", Path: "/custom.ipxe"},
					IPXEBinary:    "ipxe.efi",
					OSIE:          OSIE{KernelURL: "http://osie/vmlinuz-${arch}", InitrdURL: "initramfs", KernelParams: []string{"a=1"}},
					PXELINUX:      PXELINUX{Config: "default deploy"},
					RPI:           RPI{SerialNum: "1234abcd", FirmwarePath: "fw", ConfigTxt: "x=1"},
				},
				Isoboot: &Isoboot{SourceISO: &url.URL{Scheme: "http", Host: "iso", Path: "/hook.iso"}},
			},
		},
		"IPv4 only is served the IPv6 servers of it over DHCPv6": {
			hw: hardwareWith(v1alpha2.NetworkInterface{
				IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{
					Address:          "192.168.2.10",
					Prefix:           "24",
					Hostname:         ptrTo("four"),
					Nameservers:      []v1alpha2.Nameserver{"1.1.1.1", "2001:db8::53"},
					NTPServers:       []v1alpha2.Timeserver{"2001:db8::123"},
					DomainSearchList: []string{"example.com"},
				}},
				DHCP:    &v1alpha2.DHCP{IPv4: &v1alpha2.DHCPv4{Disabled: true}},
				Netboot: &v1alpha2.Netboot{Disabled: true, IPXE: &v1alpha2.IPXE{Script: "#!ipxe\nexit"}},
			}),
			want: Hardware{
				DHCP: &DHCP{
					MACAddress:       testMAC,
					IPAddress:        netip.MustParseAddr("192.168.2.10"),
					SubnetMask:       net.CIDRMask(24, 32),
					NameServers:      []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("2001:db8::53")},
					NTPServers:       []net.IP{net.ParseIP("2001:db8::123")},
					Hostname:         "four",
					DomainName:       "example.com",
					DomainSearch:     []string{"example.com"},
					BroadcastAddress: netip.MustParseAddr("192.168.2.255"),
					LeaseTime:        defaultLeaseTime,
					Disabled:         true,
				},
				DHCPv6: &DHCP{
					MACAddress:   testMAC,
					NameServers:  []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("2001:db8::53")},
					NTPServers:   []net.IP{net.ParseIP("2001:db8::123")},
					DomainName:   "example.com",
					DomainSearch: []string{"example.com"},
				},
				Netboot: &Netboot{IPXEScript: "#!ipxe\nexit"},
			},
		},
		"lease times that do not fit are the defaults": {
			hw: hardwareWith(v1alpha2.NetworkInterface{DHCP: &v1alpha2.DHCP{
				IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo[int64](1 << 33)},
				IPv6: &v1alpha2.DHCPv6{ValidLifetime: ptrTo[int64](-1)},
			}}),
			want: Hardware{
				DHCP:    &DHCP{MACAddress: testMAC, LeaseTime: defaultLeaseTime},
				DHCPv6:  &DHCP{MACAddress: testMAC},
				Netboot: &Netboot{AllowNetboot: true},
			},
		},
		"DHCPv6 lifetimes, refresh time and boot file": {
			hw: hardwareWith(v1alpha2.NetworkInterface{
				DHCP: &v1alpha2.DHCP{IPv6: &v1alpha2.DHCPv6{
					ValidLifetime:          ptrTo[int64](7200),
					PreferredLifetime:      ptrTo[int64](3600),
					InformationRefreshTime: ptrTo[int64](900),
				}},
				Netboot: &v1alpha2.Netboot{IPv6: &v1alpha2.Netbootv6{BootFileURL: "tftp://[2001:db8::1]/boot.efi", BootFileParams: []string{"a=1"}}},
			}),
			want: Hardware{
				DHCP:    &DHCP{MACAddress: testMAC, LeaseTime: defaultLeaseTime},
				DHCPv6:  &DHCP{MACAddress: testMAC, LeaseTime: 7200, PreferredLifetime: 3600, InformationRefreshTime: 900},
				Netboot: &Netboot{AllowNetboot: true, BootFileURL: "tftp://[2001:db8::1]/boot.efi", BootFileParams: []string{"a=1"}},
			},
		},
		"a zero lease time is zero": {
			hw: hardwareWith(v1alpha2.NetworkInterface{DHCP: &v1alpha2.DHCP{
				IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo[int64](0)},
			}}),
			want: Hardware{
				DHCP:    &DHCP{MACAddress: testMAC},
				DHCPv6:  &DHCP{MACAddress: testMAC},
				Netboot: &Netboot{AllowNetboot: true},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			mac := testMAC
			if tt.mac != nil {
				mac = tt.mac
			}
			got, err := ConvertByMac(context.Background(), mac, tt.hw)
			if tt.shouldErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateComparable(netip.Addr{})); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestConvertByIP(t *testing.T) {
	hw := &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{NetworkInterfaces: v1alpha2.NetworkInterfaces{
		"aa:bb:cc:dd:ee:01": {IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "192.168.2.1", Prefix: "24"}}},
		"aa:bb:cc:dd:ee:02": {IPAM: &v1alpha2.IPAM{
			IPv4: &v1alpha2.IP{Address: "192.168.2.2", Prefix: "24"},
			IPv6: &v1alpha2.IP{Address: "2001:db8::2"},
		}},
	}}}
	tests := map[string]struct {
		ip        net.IP
		wantMAC   string
		shouldErr bool
	}{
		"IPv4":                   {ip: net.ParseIP("192.168.2.1"), wantMAC: "aa:bb:cc:dd:ee:01"},
		"IPv4 of a dual stack":   {ip: net.IPv4(192, 168, 2, 2).To4(), wantMAC: "aa:bb:cc:dd:ee:02"},
		"IPv6":                   {ip: net.ParseIP("2001:db8:0::2"), wantMAC: "aa:bb:cc:dd:ee:02"},
		"no interface has it":    {ip: net.ParseIP("192.168.2.3"), shouldErr: true},
		"no IP":                  {shouldErr: true},
		"IPv4 address of a ipv6": {ip: net.ParseIP("::ffff:192.168.2.1"), wantMAC: "aa:bb:cc:dd:ee:01"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ConvertByIP(context.Background(), tt.ip, hw)
			if tt.shouldErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.DHCP.MACAddress.String() != tt.wantMAC {
				t.Fatalf("got interface %v, want %v", got.DHCP.MACAddress, tt.wantMAC)
			}
		})
	}
	if _, err := ConvertByIP(context.Background(), net.ParseIP("192.168.2.1"), nil); err == nil {
		t.Fatal("expected an error for nil hardware")
	}
}

// TestConvertV1Alpha1Compat covers what Hardware created through v1alpha1 is
// served from its v1alpha1 form.
func TestConvertV1Alpha1Compat(t *testing.T) {
	old := &v1alpha1.Hardware{Spec: v1alpha1.HardwareSpec{
		Metadata: &v1alpha1.HardwareMetadata{Facility: &v1alpha1.MetadataFacility{FacilityCode: "onprem"}},
		Interfaces: []v1alpha1.Interface{{
			DHCP:    &v1alpha1.DHCP{MAC: testMAC.String(), IP: &v1alpha1.IP{Address: "192.168.2.10", Netmask: "255.255.255.0"}},
			Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(true), OSIE: &v1alpha1.OSIE{BaseURL: "http://osie:8080", Kernel: "k"}},
		}},
	}}
	converted := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(old, converted); err != nil {
		t.Fatal(err)
	}
	// The API server defaults it.
	converted.Spec.NetworkInterfaces["aa:bb:cc:dd:ee:ff"].DHCP.IPv4.LeaseTimeSeconds = ptrTo[int64](86400)

	t.Run("with the conversion annotation", func(t *testing.T) {
		got, err := ConvertByMac(context.Background(), testMAC, converted)
		if err != nil {
			t.Fatal(err)
		}
		if got.Netboot.Facility != "onprem" {
			t.Errorf("facility = %q, want onprem", got.Netboot.Facility)
		}
		if got.DHCP.LeaseTime != 86400 || got.DHCPv6.LeaseTime != 0 {
			t.Errorf("lease times = %d, %d, want the v1alpha2 defaults 86400 and 0 (the DHCPv6 default lifetime)", got.DHCP.LeaseTime, got.DHCPv6.LeaseTime)
		}
		want := OSIE{BaseURL: &url.URL{Scheme: "http", Host: "osie:8080"}, Kernel: "k"}
		if diff := cmp.Diff(want, got.Netboot.OSIE); diff != "" {
			t.Errorf("OSIE differs: %s", diff)
		}
	})

	t.Run("without it", func(t *testing.T) {
		hw := converted.DeepCopy()
		delete(hw.Annotations, conversion.AnnotationV1Alpha1)
		got, err := ConvertByMac(context.Background(), testMAC, hw)
		if err != nil {
			t.Fatal(err)
		}
		if got.Netboot.Facility != "" {
			t.Errorf("facility = %q, want none", got.Netboot.Facility)
		}
		if got.DHCP.LeaseTime != 86400 || got.DHCPv6.LeaseTime != 0 {
			t.Errorf("lease times = %d, %d, want 86400 and the DHCPv6 default, 0", got.DHCP.LeaseTime, got.DHCPv6.LeaseTime)
		}
		want := OSIE{KernelURL: "http://osie:8080/k", InitrdURL: "http://osie:8080/initramfs-${arch}"}
		if diff := cmp.Diff(want, got.Netboot.OSIE); diff != "" {
			t.Errorf("OSIE differs: %s", diff)
		}
	})

	t.Run("with a corrupt annotation", func(t *testing.T) {
		hw := converted.DeepCopy()
		hw.Annotations[conversion.AnnotationV1Alpha1] = "{"
		got, err := ConvertByMac(context.Background(), testMAC, hw)
		if err != nil {
			t.Fatal(err)
		}
		if got.Netboot.Facility != "" || got.DHCP.LeaseTime != 86400 {
			t.Errorf("facility, lease time = %q, %d, want the v1alpha2 data alone", got.Netboot.Facility, got.DHCP.LeaseTime)
		}
	})
}

func ptrTo[T any](v T) *T { return &v }
