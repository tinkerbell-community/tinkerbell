package dhcp

import (
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"go.opentelemetry.io/otel/attribute"
)

// Hardware is the combination of structs that hold all the data about one
// network interface of a piece of hardware.
type Hardware struct {
	// Holds the assigned AgentID from the hardware object
	AgentID string
	// DHCP holds the DHCPv4 headers and options to be set in a DHCP handler response:
	// the interface's ipam.ipv4, dhcp.ipv4 and netboot.ipv4.
	// This is the API between a DHCP handler and a backend.
	DHCP *DHCP
	// DHCPv6 holds the data to be set in a DHCPv6 handler response: the
	// interface's ipam.ipv6 and dhcp.ipv6. An interface without ipam.ipv6 is
	// served the name servers, NTP servers and search domains of its ipam.ipv4.
	DHCPv6 *DHCP
	// Netboot holds info used in netbooting a client.
	Netboot *Netboot
	// Isoboot holds info used in booting a client using an ISO image.
	Isoboot *Isoboot
}

// Addressing returns the interface's IP configuration for uses that take one,
// such as a static IP kernel argument: the IPv4 configuration, or the IPv6
// configuration when the interface has an IPv6 address and no IPv4 address.
func (h Hardware) Addressing() *DHCP {
	if h.DHCP != nil && h.DHCP.IPAddress.IsValid() {
		return h.DHCP
	}
	if h.DHCPv6 != nil && h.DHCPv6.IPAddress.IsValid() {
		return h.DHCPv6
	}
	return h.DHCP
}

// VLANID returns the interface's VLAN ID: that of its IPv4 configuration, or
// of its IPv6 configuration when the IPv4 one sets none.
func (h Hardware) VLANID() string {
	if h.DHCP != nil && h.DHCP.VLANID != "" {
		return h.DHCP.VLANID
	}
	if h.DHCPv6 != nil {
		return h.DHCPv6.VLANID
	}
	return ""
}

// DHCP holds the DHCP headers and options to be set in a DHCP handler response.
// This is the API between a DHCP handler and a backend.
type DHCP struct {
	MACAddress            net.HardwareAddr // chaddr DHCP header.
	IPAddress             netip.Addr       // yiaddr DHCP header.
	SubnetMask            net.IPMask       // DHCP option 1.
	DefaultGateway        netip.Addr       // DHCP option 3.
	NameServers           []net.IP         // DHCP option 6.
	Hostname              string           // DHCP option 12.
	DomainName            string           // DHCP option 15.
	BroadcastAddress      netip.Addr       // DHCP option 28.
	NTPServers            []net.IP         // DHCP option 42.
	VLANID                string           // DHCP option 43.116.
	LeaseTime             uint32           // DHCP option 51; for DHCPv6 the IA address valid lifetime, 0 meaning the default.
	TFTPServerName        string           // DHCP option 66.
	BootFileName          string           // DHCP option 67.
	Arch                  string           // DHCP option 93.
	DomainSearch          []string         // DHCP option 119.
	ClasslessStaticRoutes dhcpv4.Routes    // DHCP option 121 - RFC 3442.
	Disabled              bool             // If true, no DHCP response should be sent.
	// PreferredLifetime is the DHCPv6 IA address preferred lifetime, in seconds.
	// 0 derives it from the valid lifetime.
	PreferredLifetime uint32
	// InformationRefreshTime is DHCPv6 option 32, in seconds. 0 uses the
	// server's.
	InformationRefreshTime uint32
}

// Netboot holds info used in netbooting a client.
type Netboot struct {
	AllowNetboot  bool     // If true, the client will be provided netboot options in the DHCP offer/ack.
	IPXEScriptURL *url.URL // Overrides a default value that is passed into DHCP on startup.
	IPXEScript    string   // Overrides a default value that is passed into DHCP on startup.
	IPXEBinary    string   // Overrides Smee's default architecture to binary mapping.
	// BootFileURL is DHCPv6 option 59. It overrides the boot file Smee chooses.
	BootFileURL string
	// BootFileParams is DHCPv6 option 60, served with BootFileURL.
	BootFileParams []string
	Console        string
	Facility       string
	OSIE           OSIE
	PXELINUX       PXELINUX
	RPI            RPI
}

// Isoboot holds info used in booting a client using an ISO image.
type Isoboot struct {
	// SourceISO is the source url where HookOS, an Operating System Installation Environment (OSIE), ISO lives.
	// It must be a valid url.URL{} object and must have a url.URL{}.Scheme of HTTP or HTTPS.
	//+optional
	SourceISO *url.URL
}

// PXELINUX represents the config, for u-boot "pxelinux.cfg" booting.
type PXELINUX struct {
	Config string `json:"config,omitempty"`
}

// RPI represents the data needed to support RaspberryPi EEPROM firmware netbooting.
type RPI struct {
	SerialNum    string `json:"serialNum"`
	FirmwarePath string `json:"firmwarePath,omitempty"`
	ConfigTxt    string `json:"configTxt,omitempty"`
}

// OSIE or OS Installation Environment is the data about where the OSIE parts are located.
//
// The kernel and initrd are located by KernelURL and InitrdURL, the Hardware's
// instance.osie, or, for Hardware created through v1alpha1, by BaseURL, Kernel
// and Initrd as v1alpha1 held them. Only one of the two forms is set.
type OSIE struct {
	// KernelURL is the URL of the kernel: absolute, or a reference relative to
	// the default OSIE URL. ${arch} in it is the client's architecture.
	KernelURL string
	// InitrdURL is KernelURL for the initrd.
	InitrdURL string
	// BaseURL is the URL where the OSIE parts are located.
	BaseURL *url.URL
	// Kernel is the name of the kernel file.
	Kernel string
	// Initrd is the name of the initrd file.
	Initrd string
	// KernelParams are the kernel command-line parameters; rendered as cmdline.txt for RPi netboot.
	KernelParams []string
}

// EncodeToAttributes returns a slice of opentelemetry attributes that can be used to set span.SetAttributes.
func (d *DHCP) EncodeToAttributes() []attribute.KeyValue {
	var ns []string
	for _, e := range d.NameServers {
		ns = append(ns, e.String())
	}

	var ntp []string
	for _, e := range d.NTPServers {
		ntp = append(ntp, e.String())
	}

	var ip string
	if d.IPAddress.Compare(netip.Addr{}) != 0 {
		ip = d.IPAddress.String()
	}

	var sm string
	if d.SubnetMask != nil {
		sm = net.IP(d.SubnetMask).String()
	}

	var dfg string
	if d.DefaultGateway.Compare(netip.Addr{}) != 0 {
		dfg = d.DefaultGateway.String()
	}

	var ba string
	if d.BroadcastAddress.Compare(netip.Addr{}) != 0 {
		ba = d.BroadcastAddress.String()
	}

	var routes []string
	for _, route := range d.ClasslessStaticRoutes {
		routes = append(routes, route.Dest.String()+"->"+route.Router.String())
	}

	return []attribute.KeyValue{
		attribute.String("DHCP.MACAddress", d.MACAddress.String()),
		attribute.String("DHCP.IPAddress", ip),
		attribute.String("DHCP.SubnetMask", sm),
		attribute.String("DHCP.DefaultGateway", dfg),
		attribute.String("DHCP.NameServers", strings.Join(ns, ",")),
		attribute.String("DHCP.Hostname", d.Hostname),
		attribute.String("DHCP.DomainName", d.DomainName),
		attribute.String("DHCP.BroadcastAddress", ba),
		attribute.String("DHCP.NTPServers", strings.Join(ntp, ",")),
		attribute.Int64("DHCP.LeaseTime", int64(d.LeaseTime)),
		attribute.String("DHCP.DomainSearch", strings.Join(d.DomainSearch, ",")),
		attribute.String("DHCP.ClasslessStaticRoutes", strings.Join(routes, ",")),
	}
}

// EncodeToAttributes returns a slice of opentelemetry attributes that can be used to set span.SetAttributes.
func (n *Netboot) EncodeToAttributes() []attribute.KeyValue {
	a := []attribute.KeyValue{
		attribute.Bool("Netboot.AllowNetboot", n.AllowNetboot),
	}
	if n.IPXEScriptURL != nil {
		a = append(a, attribute.String("Netboot.IPXEScriptURL", n.IPXEScriptURL.String()))
	}
	if n.IPXEBinary != "" {
		a = append(a, attribute.String("Netboot.IPXEBinary", n.IPXEBinary))
	}
	return a
}
