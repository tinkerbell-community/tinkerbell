package paritytest

import (
	"net"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Fixture is v1alpha1 Hardware Smee could serve, named for what it covers.
type Fixture struct {
	Name     string
	Hardware *v1alpha1.Hardware
}

// MACs returns the MAC addresses of the fixture's interfaces.
func (f Fixture) MACs() []net.HardwareAddr {
	var out []net.HardwareAddr
	for _, iface := range f.Hardware.Spec.Interfaces {
		if iface.DHCP == nil {
			continue
		}
		if mac, err := net.ParseMAC(iface.DHCP.MAC); err == nil {
			out = append(out, mac)
		}
	}
	return out
}

// IPs returns the IP addresses of the fixture's interfaces.
func (f Fixture) IPs() []net.IP {
	var out []net.IP
	for _, iface := range f.Hardware.Spec.Interfaces {
		if iface.DHCP == nil || iface.DHCP.IP == nil {
			continue
		}
		if ip := net.ParseIP(iface.DHCP.IP.Address); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

func hardware(name string, spec v1alpha1.HardwareSpec) *v1alpha1.Hardware {
	return &v1alpha1.Hardware{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "Hardware"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tink", CreationTimestamp: metav1.Unix(1700000000, 0)},
		Spec:       spec,
	}
}

func allowed() *v1alpha1.Netboot { return &v1alpha1.Netboot{AllowPXE: ptrTo(true)} }

func withOSIE(o *v1alpha1.OSIE) *v1alpha1.Netboot {
	return &v1alpha1.Netboot{AllowPXE: ptrTo(true), AllowWorkflow: ptrTo(true), OSIE: o}
}

func ipv4(address string) *v1alpha1.IP {
	return &v1alpha1.IP{Address: address, Netmask: "255.255.255.0", Gateway: "192.168.2.1", Family: 4}
}

// Fixtures returns v1alpha1 Hardware covering what Smee reads: every DHCP,
// netboot, OSIE and metadata setting, unset values, and the values the
// conversion cannot carry to v1alpha2 as they are (an unset lease time, the
// facility, OSIE base URLs). Each fixture is served the same through v1alpha2.
func Fixtures() []Fixture {
	return []Fixture{
		{Name: "full IPv4", Hardware: hardware("full-ipv4", v1alpha1.HardwareSpec{
			AgentID: "agent-1",
			Metadata: &v1alpha1.HardwareMetadata{
				Facility: &v1alpha1.MetadataFacility{FacilityCode: "onprem"},
				Instance: &v1alpha1.MetadataInstance{ID: "instance-1"},
				OSIEFiles: []v1alpha1.OSIEFile{
					{Path: "/etc/one", Contents: "one", Mode: "0644"},
					{Path: "two", Contents: "two"},
				},
			},
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{
					MAC:         "52:54:00:00:00:01",
					Hostname:    "full",
					DomainName:  "example.com",
					LeaseTime:   86400,
					Arch:        "x86_64",
					VLANID:      "42",
					NameServers: []string{"1.1.1.1", "2001:db8::53", "dns.example.com"},
					TimeServers: []string{"132.163.96.2", "2001:db8::123"},
					IP:          ipv4("192.168.2.10"),
					ClasslessStaticRoutes: []v1alpha1.ClasslessStaticRoute{
						{DestinationDescriptor: "10.0.0.0/8", Router: "192.168.2.254"},
					},
				},
				Netboot: &v1alpha1.Netboot{
					AllowPXE:      ptrTo(true),
					AllowWorkflow: ptrTo(true),
					IPXE:          &v1alpha1.IPXE{URL: "http://boot.example.com/custom.ipxe", Binary: "ipxe.efi"},
					OSIE: &v1alpha1.OSIE{
						BaseURL:      "http://osie.example.com/hook",
						Kernel:       "vmlinuz-custom",
						Initrd:       "initramfs-custom",
						KernelParams: []string{"console=ttyS0", "extra=1"},
					},
				},
				Isoboot: &v1alpha1.Isoboot{SourceISO: "http://iso.example.com/hook.iso"},
			}},
		})},
		{Name: "unset lease time and allowPXE", Hardware: hardware("unset", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:02", IP: ipv4("192.168.2.11")},
				Netboot: &v1alpha1.Netboot{},
			}},
		})},
		{Name: "allowPXE false", Hardware: hardware("not-allowed", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:03", IP: ipv4("192.168.2.12"), LeaseTime: 3600},
				Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(false), AllowWorkflow: ptrTo(false)},
			}},
		})},
		{Name: "negative lease time", Hardware: hardware("negative-lease", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:04", IP: ipv4("192.168.2.13"), LeaseTime: -5},
				Netboot: allowed(),
			}},
		})},
		{Name: "lease time over 32 bits", Hardware: hardware("huge-lease", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:05", IP: ipv4("192.168.2.14"), LeaseTime: 1 << 33},
				Netboot: allowed(),
			}},
		})},
		{Name: "short lease time", Hardware: hardware("short-lease", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:06", IP: ipv4("192.168.2.15"), LeaseTime: 30},
				Netboot: allowed(),
			}},
		})},
		{Name: "DHCP disabled", Hardware: hardware("disabled", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:        &v1alpha1.DHCP{MAC: "52:54:00:00:00:07", IP: ipv4("192.168.2.16"), LeaseTime: 86400},
				Netboot:     allowed(),
				DisableDHCP: true,
			}},
		})},
		{Name: "IPv6", Hardware: hardware("ipv6", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{
					MAC:         "52:54:00:00:00:08",
					Hostname:    "six",
					DomainName:  "six.example.com",
					LeaseTime:   7200,
					VLANID:      "6",
					NameServers: []string{"2001:db8::53", "1.1.1.1"},
					TimeServers: []string{"2001:db8::123"},
					IP:          &v1alpha1.IP{Address: "2001:db8::10", Gateway: "2001:db8::1", Family: 6},
				},
				Netboot: withOSIE(&v1alpha1.OSIE{KernelParams: []string{"ipv6=1"}}),
			}},
		})},
		{Name: "IPv6 by address", Hardware: hardware("ipv6-by-address", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:09", IP: &v1alpha1.IP{Address: "2001:db8::11"}},
				Netboot: allowed(),
			}},
		})},
		{Name: "IPv6 unset lease time", Hardware: hardware("ipv6-unset", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:0a", IP: &v1alpha1.IP{Address: "2001:db8::12", Family: 6}},
				Netboot: allowed(),
			}},
		})},
		{Name: "no IP address", Hardware: hardware("no-ip", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{
					MAC:         "52:54:00:00:00:0b",
					Hostname:    "no-ip",
					DomainName:  "example.com",
					NameServers: []string{"1.1.1.1", "2001:db8::53"},
					LeaseTime:   600,
				},
				Netboot: allowed(),
			}},
		})},
		{Name: "iPXE script and binary", Hardware: hardware("ipxe-script", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{MAC: "52:54:00:00:00:0c", IP: ipv4("192.168.2.17")},
				Netboot: &v1alpha1.Netboot{
					AllowPXE: ptrTo(true),
					IPXE:     &v1alpha1.IPXE{Contents: "#!ipxe\nautoboot", Binary: "snp-arm64.efi"},
				},
			}},
		})},
		{Name: "PXELINUX and Raspberry Pi", Hardware: hardware("pi", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{MAC: "b8:27:eb:00:00:0d", IP: ipv4("192.168.2.18"), Arch: "aarch64"},
				Netboot: &v1alpha1.Netboot{
					AllowPXE: ptrTo(true),
					PXELINUX: &v1alpha1.PXELINUX{Config: "default deploy"},
					RPI:      &v1alpha1.RPI{SerialNum: "1234abcd", FirmwarePath: "rpi-firmware", ConfigTxt: "arm_64bit=1"},
					OSIE:     &v1alpha1.OSIE{Kernel: "pi-kernel-${arch}", KernelParams: []string{"console=serial0"}},
				},
			}},
		})},
		{Name: "TFTP server and boot file", Hardware: hardware("tftp", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{
					MAC:            "52:54:00:00:00:0e",
					IP:             ipv4("192.168.2.19"),
					TFTPServerName: "192.168.2.5",
					BootFileName:   "onie-installer",
				},
				Netboot: allowed(),
			}},
		})},
		{Name: "OSIE base URL without a path", Hardware: hardware("osie-host", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:0f", IP: ipv4("192.168.2.20")},
				Netboot: withOSIE(&v1alpha1.OSIE{BaseURL: "http://10.0.0.5:8080"}),
			}},
		})},
		{Name: "OSIE base URL with a trailing slash", Hardware: hardware("osie-slash", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:10", IP: ipv4("192.168.2.21")},
				Netboot: withOSIE(&v1alpha1.OSIE{BaseURL: "http://10.0.0.5:8080/images/", Initrd: "initrd.img"}),
			}},
		})},
		{Name: "OSIE base URL only", Hardware: hardware("osie-base", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:11", IP: ipv4("192.168.2.22"), Arch: "aarch64"},
				Netboot: withOSIE(&v1alpha1.OSIE{BaseURL: "http://10.0.0.5:8080/hook"}),
			}},
		})},
		{Name: "OSIE kernel without base URL", Hardware: hardware("osie-kernel", v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:12", IP: ipv4("192.168.2.23")},
				Netboot: withOSIE(&v1alpha1.OSIE{Kernel: "captain/vmlinuz-${arch}"}),
			}},
		})},
		{Name: "two interfaces", Hardware: hardware("two", v1alpha1.HardwareSpec{
			Metadata: &v1alpha1.HardwareMetadata{Facility: &v1alpha1.MetadataFacility{FacilityCode: "console=ttyS1"}},
			Interfaces: []v1alpha1.Interface{
				{
					DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:13", IP: ipv4("192.168.2.24"), LeaseTime: 100, Arch: "x86_64"},
					Netboot: withOSIE(&v1alpha1.OSIE{BaseURL: "http://osie.example.com/hook", KernelParams: []string{"a=1"}}),
					Isoboot: &v1alpha1.Isoboot{SourceISO: "http://iso.example.com/hook.iso"},
				},
				{
					DHCP:    &v1alpha1.DHCP{MAC: "52:54:00:00:00:14", IP: &v1alpha1.IP{Address: "2001:db8::14", Family: 6}, Arch: "x86_64"},
					Netboot: withOSIE(&v1alpha1.OSIE{BaseURL: "http://osie.example.com/hook", KernelParams: []string{"a=1"}}),
					Isoboot: &v1alpha1.Isoboot{SourceISO: "http://iso.example.com/hook.iso"},
				},
			},
		})},
	}
}

func ptrTo[T any](v T) *T { return &v }
