package reservation

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/pkg/otel"
	"github.com/tinkerbell/tinkerbell/smee/internal/dhcp"
	dhcpotel "github.com/tinkerbell/tinkerbell/smee/internal/dhcp/otel"
)

// setDHCPOpts takes a client dhcp packet and data (typically from a backend) and creates a slice of DHCP packet modifiers.
// m is the DHCP request from a client. d is the data to use to create the DHCP packet modifiers.
// This is most likely the place where we would have any business logic for determining DHCP option setting.
func (h *Handler) setDHCPOpts(_ context.Context, _ *dhcpv4.DHCPv4, d *data.DHCP) []dhcpv4.Modifier {
	mods := []dhcpv4.Modifier{
		dhcpv4.WithLeaseTime(d.LeaseTime),
		dhcpv4.WithYourIP(d.IPAddress.AsSlice()),
	}
	if len(d.NameServers) > 0 {
		mods = append(mods, dhcpv4.WithDNS(d.NameServers...))
	}
	if len(d.DomainSearch) > 0 {
		mods = append(mods, dhcpv4.WithDomainSearchList(d.DomainSearch...))
	}
	if len(d.NTPServers) > 0 {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptNTPServers(d.NTPServers...)))
	}
	if d.BroadcastAddress.Compare(netip.Addr{}) != 0 {
		mods = append(mods, dhcpv4.WithGeneric(dhcpv4.OptionBroadcastAddress, d.BroadcastAddress.AsSlice()))
	}
	if d.DomainName != "" {
		mods = append(mods, dhcpv4.WithGeneric(dhcpv4.OptionDomainName, []byte(d.DomainName)))
	}
	if d.Hostname != "" {
		mods = append(mods, dhcpv4.WithGeneric(dhcpv4.OptionHostName, []byte(d.Hostname)))
	}
	if len(d.SubnetMask) > 0 {
		mods = append(mods, dhcpv4.WithNetmask(d.SubnetMask))
	}
	if d.DefaultGateway.Compare(netip.Addr{}) != 0 {
		mods = append(mods, dhcpv4.WithRouter(d.DefaultGateway.AsSlice()))
	}
	if h.SyslogAddr.Compare(netip.Addr{}) != 0 {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.OptionLogServer, h.SyslogAddr.AsSlice())))
	}
	if len(d.ClasslessStaticRoutes) > 0 {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptClasslessStaticRoute(d.ClasslessStaticRoutes...)))
	}
	if d.TFTPServerName != "" {
		mods = append(mods, dhcpv4.WithGeneric(dhcpv4.OptionTFTPServerName, []byte(d.TFTPServerName)))
	}
	if d.BootFileName != "" {
		mods = append(mods, dhcpv4.WithGeneric(dhcpv4.OptionBootfileName, []byte(d.BootFileName)))
		// Also set the DHCP header field for BootFileName
		bootFileName := d.BootFileName
		mods = append(mods, func(pkt *dhcpv4.DHCPv4) {
			pkt.BootFileName = bootFileName
		})
	}

	return mods
}

// setNetworkBootOpts purpose is to sets 3 or 4 values. 2 DHCP headers, option 43 and optionally option (60).
// These headers and options are returned as a dhcvp4.Modifier that can be used to modify a dhcp response.
// github.com/insomniacslk/dhcp uses this method to simplify packet manipulation.
//
// DHCP Headers (https://datatracker.ietf.org/doc/html/rfc2131#section-2)
// 'siaddr': IP address of next bootstrap server. represented below as `.ServerIPAddr`.
// 'file': Client boot file name. represented below as `.BootFileName`.
//
// DHCP option
// option 60: Class Identifier. https://www.rfc-editor.org/rfc/rfc2132.html#section-9.13
// option 60 is set if the client's option 60 (Class Identifier) starts with HTTPClient.
func (h *Handler) setNetworkBootOpts(ctx context.Context, m *dhcpv4.DHCPv4, n *data.Netboot) dhcpv4.Modifier {
	// m is a received DHCPv4 packet.
	// d is the reply packet we are building.
	withNetboot := func(d *dhcpv4.DHCPv4) {
		// if the client sends opt 60 with HTTPClient then we need to respond with opt 60
		// This is outside of the n.AllowNetboot check because we will be sending "/netboot-not-allowed" regardless.
		if val := m.Options.Get(dhcpv4.OptionClassIdentifier); val != nil {
			if strings.HasPrefix(string(val), dhcp.HTTPClient.String()) {
				d.UpdateOption(dhcpv4.OptGeneric(dhcpv4.OptionClassIdentifier, []byte(dhcp.HTTPClient)))
			}
		}
		d.BootFileName = "/netboot-not-allowed"
		if slices.ContainsFunc(d.ClientArch(), func(a iana.Arch) bool {
			return a == iana.UBOOT_ARM64 || a == iana.UBOOT_ARM32 || a == iana.Arch(41)
		}) {
			d.BootFileName = ""
		}
		d.ServerIPAddr = net.IPv4(0, 0, 0, 0)
		if n.AllowNetboot {
			i := dhcp.NewInfo(m, dhcp.WithMacAddrFormat(h.Netboot.InjectMacAddrFormat), dhcp.WithIPXEBinary(n.IPXEBinary), dhcp.WithArchMappingOverride(h.Netboot.IPXEArchMapping))
			if i.IPXEBinary == "" {
				return
			}
			var ipxeScript *url.URL
			// If the global IPXEScriptURL is set, use that.
			if h.Netboot.IPXEScriptURL != nil {
				ipxeScript = h.Netboot.IPXEScriptURL(m)
			}
			// If the IPXE script URL is set on the hardware record, use that.
			if n.IPXEScriptURL != nil {
				ipxeScript = n.IPXEScriptURL
			}
			d.BootFileName, d.ServerIPAddr = h.bootfileAndNextServer(ctx, m, h.Netboot.UserClass, h.Netboot.IPXEBinServerTFTP, h.Netboot.IPXEBinServerHTTP, ipxeScript, i, n.IPXEBinary)
			pxe := dhcpv4.Options{ // FYI, these are suboptions of option43. ref: https://datatracker.ietf.org/doc/html/rfc2132#section-8.4
				// PXE Boot Server Discovery Control - bypass, just boot from filename.
				6:  []byte{8},
				69: dhcpotel.TraceparentFromContext(ctx),
			}
			d.UpdateOption(dhcpv4.OptGeneric(dhcpv4.OptionVendorSpecificInformation, i.AddRPIOpt43(pxe)))
		}
	}

	return withNetboot
}

// bootfileAndNextServer returns the bootfile (string) and next server (net.IP).
// input arguments `tftp`, `ipxe` and `iscript` use non string types so as to attempt to be more clear about the expectation around what is wanted for these values.
// It also helps us avoid having to validate a string in multiple ways.
func (h *Handler) bootfileAndNextServer(ctx context.Context, pkt *dhcpv4.DHCPv4, customUC dhcp.UserClass, tftp netip.AddrPort, ipxe, iscript *url.URL, i dhcp.Info, ipxeBinaryOverride string) (string, net.IP) {
	var nextServer net.IP
	var bootfile string
	if i.Pkt == nil {
		i = dhcp.NewInfo(pkt, dhcp.WithMacAddrFormat(h.Netboot.InjectMacAddrFormat), dhcp.WithIPXEBinary(ipxeBinaryOverride), dhcp.WithArchMappingOverride(h.Netboot.IPXEArchMapping))
	}

	if tp := otel.TraceparentStringFromContext(ctx); h.OTELEnabled && tp != "" {
		i.IPXEBinary = fmt.Sprintf("%s-%v", i.IPXEBinary, tp)
	}
	nextServer = i.NextServer(ipxe, tftp, h.IPAddr)
	bootfile = i.Bootfile(customUC, iscript, ipxe, tftp)

	return bootfile, nextServer
}
