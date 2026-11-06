package dhcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ccoveille/go-safecast/v2"
	"github.com/insomniacslk/dhcp/dhcpv4"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/tinkerbell/tinkerbell"

// defaultLeaseTime is the DHCPv4 lease time, in seconds, of an interface that
// sets none: the v1alpha2 CRD default of dhcp.ipv4.leaseTimeSeconds. The API
// server fills that default in, but Hardware read from elsewhere (the file
// backend) may not have it.
const defaultLeaseTime uint32 = 86400

// ConvertByMac returns the data of the network interface of hw whose MAC
// address is mac.
func ConvertByMac(ctx context.Context, mac net.HardwareAddr, hw *v1alpha2.Hardware) (Hardware, error) {
	tracer := otel.Tracer(tracerName)
	_, span := tracer.Start(ctx, "smee.internal.data.ConvertByMac")
	defer span.End()
	if hw == nil {
		err := errors.New("hardware is nil")
		span.SetStatus(codes.Error, err.Error())
		return Hardware{}, err
	}
	for key, ni := range hw.Spec.NetworkInterfaces {
		if strings.EqualFold(string(key), mac.String()) {
			return convert(span, hw, key, ni)
		}
	}
	err := fmt.Errorf("hardware %s/%s has no network interface with MAC address %s", hw.Namespace, hw.Name, mac)
	span.SetStatus(codes.Error, err.Error())

	return Hardware{}, err
}

// ConvertByIP returns the data of the network interface of hw with the IPv4 or
// IPv6 address ip.
func ConvertByIP(ctx context.Context, ip net.IP, hw *v1alpha2.Hardware) (Hardware, error) {
	tracer := otel.Tracer(tracerName)
	_, span := tracer.Start(ctx, "smee.internal.data.ConvertByIP")
	defer span.End()
	if hw == nil {
		err := errors.New("hardware is nil")
		span.SetStatus(codes.Error, err.Error())
		return Hardware{}, err
	}
	if want, ok := netip.AddrFromSlice(ip); ok {
		// In MAC order, so that an address set on several interfaces always
		// finds the same one.
		for _, key := range slices.Sorted(maps.Keys(hw.Spec.NetworkInterfaces)) {
			if ni := hw.Spec.NetworkInterfaces[key]; interfaceHasIP(ni, want.Unmap()) {
				return convert(span, hw, key, ni)
			}
		}
	}
	err := fmt.Errorf("hardware %s/%s has no network interface with IP address %s", hw.Namespace, hw.Name, ip)
	span.SetStatus(codes.Error, err.Error())

	return Hardware{}, err
}

func interfaceHasIP(ni v1alpha2.NetworkInterface, ip netip.Addr) bool {
	if ni.IPAM == nil {
		return false
	}
	for _, c := range []*v1alpha2.IP{ni.IPAM.IPv4, ni.IPAM.IPv6} {
		if c == nil {
			continue
		}
		if a, err := netip.ParseAddr(c.Address); err == nil && a.Unmap() == ip {
			return true
		}
	}
	return false
}

// convert returns the data of ni, the network interface of hw with MAC address mac.
func convert(span trace.Span, hw *v1alpha2.Hardware, mac v1alpha2.MAC, ni v1alpha2.NetworkInterface) (Hardware, error) {
	fail := func(err error) (Hardware, error) {
		span.SetStatus(codes.Error, err.Error())
		return Hardware{}, err
	}

	hwAddr, err := net.ParseMAC(string(mac))
	if err != nil {
		return fail(err)
	}
	compat, err := v1alpha1CompatFor(hw, mac)
	if err != nil {
		// What only v1alpha1 holds is lost, the v1alpha2 data is still served.
		span.RecordError(err)
	}

	d4, err := dhcpv4Data(hwAddr, hw.Spec.Arch, ni, compat)
	if err != nil {
		return fail(fmt.Errorf("failed to convert hardware to DHCP data: %w", err))
	}
	d6, err := dhcpv6Data(hwAddr, hw.Spec.Arch, ni, compat)
	if err != nil {
		return fail(fmt.Errorf("failed to convert hardware to DHCPv6 data: %w", err))
	}
	var osie *v1alpha2.OSIE
	if hw.Spec.Instance != nil {
		osie = hw.Spec.Instance.OSIE
	}
	n, err := netbootData(ni.Netboot, osie, compat)
	if err != nil {
		return fail(fmt.Errorf("failed to convert hardware to netboot data: %w", err))
	}

	result := Hardware{AgentID: hw.Spec.AgentID, DHCP: d4, DHCPv6: d6, Netboot: n}
	if osie != nil && osie.ISOURL != "" {
		si, err := url.Parse(osie.ISOURL)
		if err != nil {
			return fail(fmt.Errorf("failed to parse source ISO as a URL %q: %w", osie.ISOURL, err))
		}
		result.Isoboot = &Isoboot{SourceISO: si}
	}

	span.SetAttributes(d4.EncodeToAttributes()...)
	span.SetAttributes(n.EncodeToAttributes()...)
	span.SetStatus(codes.Ok, "")

	return result, nil
}

// dhcpv4Data returns the DHCPv4 data of ni: its ipam.ipv4, dhcp.ipv4 and netboot.ipv4.
func dhcpv4Data(mac net.HardwareAddr, arch string, ni v1alpha2.NetworkInterface, compat v1alpha1Compat) (*DHCP, error) {
	d := &DHCP{MACAddress: mac, Arch: arch, LeaseTime: defaultLeaseTime}
	if ni.IPAM != nil {
		if err := setIP(d, ni.IPAM.IPv4, true); err != nil {
			return nil, err
		}
	}
	if ni.DHCP != nil && ni.DHCP.IPv4 != nil {
		d.Disabled = ni.DHCP.IPv4.Disabled
		if l, ok := seconds(ni.DHCP.IPv4.LeaseTimeSeconds); ok {
			d.LeaseTime = l
		}
	}
	if compat.leaseTime != nil {
		d.LeaseTime = *compat.leaseTime
	}
	if ni.Netboot != nil && ni.Netboot.IPv4 != nil {
		d.TFTPServerName = ni.Netboot.IPv4.TFTPServerName
		d.BootFileName = ni.Netboot.IPv4.BootFileName
	}

	return d, nil
}

// dhcpv6Data returns the DHCPv6 data of ni: its ipam.ipv6 and dhcp.ipv6.
//
// An interface without ipam.ipv6 is served the IPv6 name and NTP servers and
// the search domains of its ipam.ipv4, which may list servers of both families.
// v1alpha1 held one address per interface, of either family, and served its
// settings over both DHCPv4 and DHCPv6.
func dhcpv6Data(mac net.HardwareAddr, arch string, ni v1alpha2.NetworkInterface, compat v1alpha1Compat) (*DHCP, error) {
	d := &DHCP{MACAddress: mac, Arch: arch}
	if ni.IPAM != nil {
		switch {
		case ni.IPAM.IPv6 != nil:
			if err := setIP(d, ni.IPAM.IPv6, false); err != nil {
				return nil, err
			}
		case ni.IPAM.IPv4 != nil:
			setServers(d, ni.IPAM.IPv4)
		}
	}
	lifetimeSet := false
	if ni.DHCP != nil && ni.DHCP.IPv6 != nil {
		d.Disabled = ni.DHCP.IPv6.Disabled
		d.LeaseTime, lifetimeSet = seconds(ni.DHCP.IPv6.ValidLifetime)
	}
	if !lifetimeSet && compat.leaseTime != nil {
		d.LeaseTime = *compat.leaseTime
	}

	return d, nil
}

// setIP sets the IP configuration c, an ipam.ipv4 (v4) or ipam.ipv6, in d.
// Fields that are set are checked for correctness of their types.
func setIP(d *DHCP, c *v1alpha2.IP, v4 bool) error {
	if c == nil {
		return nil
	}
	if c.Address != "" {
		a, err := netip.ParseAddr(c.Address)
		if err != nil {
			return err
		}
		d.IPAddress = a
		if v4 && (a.Is4() || a.Is4In6()) {
			// SubnetMask is required for IPv4 addresses
			if d.SubnetMask, err = prefixMask(c.Prefix); err != nil {
				return err
			}
		}
	}

	// Gateway is optional, but should be a valid IP address if present
	if c.Gateway != "" {
		gw, err := netip.ParseAddr(c.Gateway)
		if err != nil {
			return err
		}
		d.DefaultGateway = gw
	}

	setServers(d, c)
	if c.Hostname != nil {
		d.Hostname = *c.Hostname
	}
	if c.VLANID != nil {
		d.VLANID = *c.VLANID
	}

	// broadcast address, computed from IP and netmask
	if d.IPAddress.Is4() && d.SubnetMask != nil {
		ip4 := d.IPAddress.As4()
		var broadcast [4]byte
		// Compute the broadcast address by ORing the IP with the inverted subnet mask,
		// which sets all host bits to 1.
		for i := range 4 {
			broadcast[i] = ip4[i] | ^d.SubnetMask[i]
		}
		d.BroadcastAddress = netip.AddrFrom4(broadcast)
	}

	// classless static routes, optional
	for _, route := range c.ClasslessStaticRoutes {
		_, destNetwork, err := net.ParseCIDR(route.DestinationDescriptor)
		if err != nil {
			return err
		}
		routerIP := net.ParseIP(route.Router)
		if routerIP == nil {
			return fmt.Errorf("failed to parse router IP %q", route.Router)
		}
		d.ClasslessStaticRoutes = append(d.ClasslessStaticRoutes, &dhcpv4.Route{
			Dest:   destNetwork,
			Router: routerIP,
		})
	}

	return nil
}

// setServers sets the name servers, NTP servers and search domains of c in d.
// Servers given by host name are skipped: DHCP serves addresses.
func setServers(d *DHCP, c *v1alpha2.IP) {
	for _, s := range c.Nameservers {
		if ip := net.ParseIP(string(s)); ip != nil {
			d.NameServers = append(d.NameServers, ip)
		}
	}
	for _, s := range c.NTPServers {
		if ip := net.ParseIP(string(s)); ip != nil {
			d.NTPServers = append(d.NTPServers, ip)
		}
	}
	// The first search domain is the domain name, DHCPv4 option 15.
	if len(c.DomainSearchList) > 0 {
		d.DomainName = c.DomainSearchList[0]
		d.DomainSearch = append([]string(nil), c.DomainSearchList...)
	}
}

// prefixMask returns the IPv4 netmask of a prefix length.
func prefixMask(prefix string) (net.IPMask, error) {
	if prefix == "" {
		return nil, errors.New("no prefix")
	}
	n, err := strconv.Atoi(prefix)
	if err != nil || n < 0 || n > 32 {
		return nil, fmt.Errorf("invalid IPv4 prefix %q", prefix)
	}

	return net.CIDRMask(n, 32), nil
}

// seconds returns *s as a DHCP time, which is 32 bits. It reports false when s
// is unset or does not fit.
func seconds(s *int64) (uint32, bool) {
	if s == nil {
		return 0, false
	}
	v, err := safecast.Convert[uint32](*s)
	if err != nil {
		return 0, false
	}

	return v, true
}

// netbootData returns the netboot data of an interface with netboot
// configuration nb, of Hardware with OSIE configuration osie.
func netbootData(nb *v1alpha2.Netboot, osie *v1alpha2.OSIE, compat v1alpha1Compat) (*Netboot, error) {
	// A missing netboot block allows netbooting.
	n := &Netboot{AllowNetboot: nb == nil || !nb.Disabled, Facility: compat.facility}

	if nb != nil {
		if err := setIPXE(n, nb.IPXE); err != nil {
			return nil, err
		}
		if nb.PXELINUX != nil {
			n.PXELINUX.Config = nb.PXELINUX.Config
		}
		if r := nb.RPI; r != nil {
			n.RPI = RPI{SerialNum: r.SerialNum, FirmwarePath: r.FirmwarePath, ConfigTxt: r.ConfigTxt}
		}
	}

	switch {
	case compat.osie != nil:
		n.OSIE = *compat.osie
	case osie != nil:
		n.OSIE = OSIE{KernelURL: osie.KernelURL, InitrdURL: osie.InitrdURL, KernelParams: osie.KernelParams}
	}

	return n, nil
}

// setIPXE sets the iPXE configuration x in n.
func setIPXE(n *Netboot, x *v1alpha2.IPXE) error {
	if x == nil {
		return nil
	}
	// ipxe script url is optional but if provided, it must be a valid url
	if x.URL != "" {
		u, err := url.ParseRequestURI(x.URL)
		if err != nil {
			return err
		}
		n.IPXEScriptURL = u
	}
	n.IPXEScript = x.Script
	n.IPXEBinary = x.Binary

	return nil
}
