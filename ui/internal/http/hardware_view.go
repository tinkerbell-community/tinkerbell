package webhttp

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	tinkv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	notAvailable = "N/A"
	timeFormat   = "2006-01-02 15:04:05"
	valueYes     = "Yes"
	valueNo      = "No"
	valueEnabled = "Enabled"
	valueOff     = "Disabled"
)

// v1alpha1View returns hw as a v1alpha1 client sees it, for Hardware created
// through v1alpha1: the v1alpha1 data v1alpha2 has no field for (the interface
// order, status.state, the bmcRef) survives only in the conversion annotation.
// It returns nil for other Hardware.
func v1alpha1View(hw *tinkv1alpha2.Hardware) *tinkv1alpha1.Hardware {
	if _, ok := hw.Annotations[conversion.AnnotationV1Alpha1]; !ok {
		return nil
	}
	var v1 tinkv1alpha1.Hardware
	if err := conversion.HardwareToV1Alpha1(hw, &v1); err != nil {
		return nil
	}
	return &v1
}

// interfaceMACs returns the MACs of hw's network interfaces in display order.
// v1alpha2 keys interfaces by MAC, so they have no order of their own: Hardware
// created through v1alpha1 lists them in its v1alpha1 order, which made the
// first interface the one shown in the Hardware list, and the others follow in
// MAC order.
func interfaceMACs(hw *tinkv1alpha2.Hardware) []tinkv1alpha2.MAC {
	macs := make([]tinkv1alpha2.MAC, 0, len(hw.Spec.NetworkInterfaces))
	seen := map[tinkv1alpha2.MAC]bool{}
	if v1 := v1alpha1View(hw); v1 != nil {
		for _, iface := range v1.Spec.Interfaces {
			if iface.DHCP == nil {
				continue
			}
			mac := tinkv1alpha2.MAC(strings.ToLower(iface.DHCP.MAC))
			if _, ok := hw.Spec.NetworkInterfaces[mac]; ok && !seen[mac] {
				seen[mac] = true
				macs = append(macs, mac)
			}
		}
	}
	for _, mac := range slices.Sorted(maps.Keys(hw.Spec.NetworkInterfaces)) {
		if !seen[mac] {
			macs = append(macs, mac)
		}
	}
	return macs
}

// ipamAddresses returns the IPv4 and IPv6 addresses of a network interface.
func ipamAddresses(ni tinkv1alpha2.NetworkInterface) (v4, v6 string) {
	if ni.IPAM == nil {
		return "", ""
	}
	if ni.IPAM.IPv4 != nil {
		v4 = ni.IPAM.IPv4.Address
	}
	if ni.IPAM.IPv6 != nil {
		v6 = ni.IPAM.IPv6.Address
	}
	return v4, v6
}

// hardwareRow converts a Hardware into its list-view representation, showing
// its first network interface.
func hardwareRow(hw *tinkv1alpha2.Hardware) templates.Hardware {
	row := templates.Hardware{
		Name:      hw.Name,
		Namespace: hw.Namespace,
		MAC:       notAvailable,
		CreatedAt: hw.GetCreationTimestamp().Format(timeFormat),
	}
	if macs := interfaceMACs(hw); len(macs) > 0 {
		row.MAC = string(macs[0])
		row.IPv4Address, row.IPv6Address = ipamAddresses(hw.Spec.NetworkInterfaces[macs[0]])
	}
	return row
}

// GetHardwareInterfaces describes the network interfaces of hw, in display order.
func GetHardwareInterfaces(hw tinkv1alpha2.Hardware) []templates.HardwareInterface {
	macs := interfaceMACs(&hw)
	interfaces := make([]templates.HardwareInterface, 0, len(macs))
	for _, mac := range macs {
		ni := hw.Spec.NetworkInterfaces[mac]
		iface := templates.HardwareInterface{MAC: string(mac)}
		iface.IPv4Address, iface.IPv6Address = ipamAddresses(ni)
		if ni.IPAM != nil {
			iface.IPv4 = ipRows(ni.IPAM.IPv4)
			iface.IPv6 = ipRows(ni.IPAM.IPv6)
		}
		iface.DHCP = dhcpRows(ni.DHCP)
		iface.Netboot = netbootRows(ni.Netboot)
		interfaces = append(interfaces, iface)
	}
	return interfaces
}

// GetHardwareStatus determines hardware status from the v1alpha1 status.state,
// which only Hardware created through v1alpha1 has.
func GetHardwareStatus(hw tinkv1alpha2.Hardware) string {
	var state tinkv1alpha1.HardwareState
	if v1 := v1alpha1View(&hw); v1 != nil {
		state = v1.Status.State
	}
	if state == "provisioning" {
		return "Provisioning"
	}
	if state == "failed" {
		return "Offline"
	}
	return "Online"
}

func ipRows(ip *tinkv1alpha2.IP) []templates.InfoRow {
	if ip == nil {
		return nil
	}
	address := ip.Address
	if address != "" && ip.Prefix != "" {
		address += "/" + ip.Prefix
	}
	routes := make([]string, 0, len(ip.ClasslessStaticRoutes))
	for _, r := range ip.ClasslessStaticRoutes {
		routes = append(routes, r.DestinationDescriptor+" via "+r.Router)
	}
	return []templates.InfoRow{
		{Name: "Address", Value: address},
		{Name: "Gateway", Value: ip.Gateway},
		{Name: "Hostname", Value: deref(ip.Hostname)},
		{Name: "VLAN ID", Value: deref(ip.VLANID)},
		{Name: "Nameservers", Value: joinStrings(ip.Nameservers)},
		{Name: "NTP Servers", Value: joinStrings(ip.NTPServers)},
		{Name: "Search Domains", Value: strings.Join(ip.DomainSearchList, ", ")},
		{Name: "Static Routes", Value: strings.Join(routes, ", ")},
	}
}

func dhcpRows(d *tinkv1alpha2.DHCP) []templates.InfoRow {
	if d == nil {
		return nil
	}
	var rows []templates.InfoRow
	if v4 := d.IPv4; v4 != nil {
		rows = append(rows,
			templates.InfoRow{Name: "DHCPv4", Value: enabled(!v4.Disabled)},
			templates.InfoRow{Name: "Lease Time", Value: seconds(v4.LeaseTimeSeconds)},
		)
	}
	if v6 := d.IPv6; v6 != nil {
		rows = append(rows,
			templates.InfoRow{Name: "DHCPv6", Value: enabled(!v6.Disabled)},
			templates.InfoRow{Name: "Valid Lifetime", Value: seconds(v6.ValidLifetime)},
			templates.InfoRow{Name: "Preferred Lifetime", Value: seconds(v6.PreferredLifetime)},
			templates.InfoRow{Name: "Information Refresh", Value: seconds(v6.InformationRefreshTime)},
		)
	}
	return rows
}

// netbootRows describes the netboot options of an interface. Netbooting is
// allowed unless disabled, so an interface with no options shows it enabled.
func netbootRows(n *tinkv1alpha2.Netboot) []templates.InfoRow {
	if n == nil {
		return []templates.InfoRow{{Name: "Netboot", Value: valueEnabled}}
	}
	rows := []templates.InfoRow{{Name: "Netboot", Value: enabled(!n.Disabled)}}
	if v4 := n.IPv4; v4 != nil {
		rows = append(rows,
			templates.InfoRow{Name: "TFTP Server", Value: v4.TFTPServerName},
			templates.InfoRow{Name: "Boot File", Value: v4.BootFileName},
		)
	}
	if v6 := n.IPv6; v6 != nil {
		rows = append(rows,
			templates.InfoRow{Name: "Boot File URL (IPv6)", Value: v6.BootFileURL},
			templates.InfoRow{Name: "Boot File Params (IPv6)", Value: strings.Join(v6.BootFileParams, " ")},
		)
	}
	if x := n.IPXE; x != nil {
		rows = append(rows,
			templates.InfoRow{Name: "iPXE URL", Value: x.URL},
			templates.InfoRow{Name: "iPXE Binary", Value: x.Binary},
			templates.InfoRow{Name: "iPXE Script", Value: customized(x.Script != "")},
		)
	}
	if p := n.PXELINUX; p != nil {
		rows = append(rows, templates.InfoRow{Name: "PXELINUX Config", Value: customized(p.Config != "")})
	}
	if r := n.RPI; r != nil {
		rows = append(rows,
			templates.InfoRow{Name: "Raspberry Pi Serial", Value: r.SerialNum},
			templates.InfoRow{Name: "Raspberry Pi Firmware", Value: r.FirmwarePath},
			templates.InfoRow{Name: "Raspberry Pi config.txt", Value: customized(r.ConfigTxt != "")},
		)
	}
	return rows
}

// hardwareSpecRows describes the Hardware as a whole.
func hardwareSpecRows(s tinkv1alpha2.HardwareSpec) []templates.InfoRow {
	devices := make([]string, 0, len(s.StorageDevices))
	for _, d := range s.StorageDevices {
		devices = append(devices, d.Name)
	}
	return []templates.InfoRow{
		{Name: "Architecture", Value: s.Arch},
		{Name: "Agent ID", Value: s.AgentID},
		{Name: "Auto Enrollment", Value: enabled(s.Auto.EnrollmentEnabled)},
		{Name: "Storage Devices", Value: strings.Join(devices, ", ")},
	}
}

// referenceRows describes references, one row per reference name.
func referenceRows(refs map[string]tinkv1alpha2.Reference) []templates.InfoRow {
	rows := make([]templates.InfoRow, 0, len(refs))
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		r := refs[name]
		resource := r.Resource
		if r.Group != "" {
			resource += "." + r.Group
		}
		if r.Version != "" {
			resource += "/" + r.Version
		}
		target := r.Name
		if r.Namespace != "" {
			target = r.Namespace + "/" + r.Name
		}
		rows = append(rows, templates.InfoRow{Name: name, Value: resource + " " + target})
	}
	return rows
}

// instanceRows describes spec.instance, and returns its cloud-init documents.
func instanceRows(inst *tinkv1alpha2.Instance) ([]templates.InfoRow, []templates.NamedText) {
	if inst == nil {
		return nil, nil
	}
	rows := []templates.InfoRow{
		{Name: "Lookup ID", Value: inst.LookupID},
		{Name: "SSH Keys", Value: count(len(inst.SSHKeys), "key")},
	}
	if o := inst.OSIE; o != nil {
		files := make([]string, 0, len(o.Files))
		for _, f := range o.Files {
			files = append(files, f.Path)
		}
		rows = append(rows,
			templates.InfoRow{Name: "OSIE Kernel", Value: o.KernelURL},
			templates.InfoRow{Name: "OSIE Initrd", Value: o.InitrdURL},
			templates.InfoRow{Name: "OSIE ISO", Value: o.ISOURL},
			templates.InfoRow{Name: "Kernel Parameters", Value: strings.Join(o.KernelParams, " ")},
			templates.InfoRow{Name: "OSIE Files", Value: strings.Join(files, ", ")},
		)
	}
	for _, k := range slices.Sorted(maps.Keys(inst.Vars)) {
		rows = append(rows, templates.InfoRow{Name: "Var " + k, Value: inst.Vars[k]})
	}

	var docs []templates.NamedText
	for _, d := range []struct {
		name string
		text *string
	}{
		{"User Data", inst.Userdata},
		{"Vendor Data", inst.Vendordata},
		{"Meta-Data", inst.Metadata},
		{"Network Config", inst.NetworkConfig},
	} {
		if d.text != nil && *d.text != "" {
			docs = append(docs, templates.NamedText{Name: d.name, Text: *d.text})
		}
	}
	return rows, docs
}

// bmcRows describes spec.bmc and returns the connection's provider options as
// YAML. Hardware created through v1alpha1 may name a deprecated bmc Machine
// instead (its v1alpha1 bmcRef); that is shown too, linked.
func bmcRows(hw *tinkv1alpha2.Hardware, baseURL string) ([]templates.InfoRow, string) {
	var rows []templates.InfoRow
	var options string
	if b := hw.Spec.BMC; b != nil {
		rows = append(rows, templates.InfoRow{Name: "Boot Mode", Value: string(b.BootMode)})
		if c := b.Connection; c != nil {
			rows = append(rows,
				templates.InfoRow{Name: "Host", Value: c.Host},
				templates.InfoRow{Name: "Port", Value: nonZero(c.Port)},
				templates.InfoRow{Name: "Credentials", Value: namespacedName(c.CredentialsRef.Namespace, c.CredentialsRef.Name)},
				templates.InfoRow{Name: "Insecure TLS", Value: yesNo(c.InsecureTLS)},
			)
			options = providerOptionsYAML(c.ProviderOptions)
		}
	}
	if ref := conversion.HardwareBMCRef(hw); ref != nil && ref.Name != "" {
		machine := hw.Namespace + "/" + ref.Name
		rows = append(rows, templates.InfoRow{Name: "BMC Machine (deprecated)", Value: machine, Link: baseURL + "/bmc/machines/" + machine})
	}
	return rows, options
}

func providerOptionsYAML(o *bmcv1alpha2.ProviderOptions) string {
	if o == nil {
		return ""
	}
	b, err := yaml.Marshal(o)
	if err != nil || string(b) == "{}\n" {
		return ""
	}
	return string(b)
}

// bmcConditions converts BMC-style conditions, as found on Hardware and Jobs.
func bmcConditions(conds []bmcv1alpha2.Condition) []templates.Condition {
	out := make([]templates.Condition, 0, len(conds))
	for _, c := range conds {
		out = append(out, templates.Condition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Message: c.Message,
			Time:    formatTime(&c.LastUpdateTime),
		})
	}
	return out
}

// hardwareDetail builds the hardware detail page data, without the YAML views.
func hardwareDetail(hw *tinkv1alpha2.Hardware, baseURL string) templates.HardwareDetail {
	d := templates.HardwareDetail{
		Name:        hw.Name,
		Namespace:   hw.Namespace,
		Interfaces:  GetHardwareInterfaces(*hw),
		Status:      GetHardwareStatus(*hw),
		CreatedAt:   hw.GetCreationTimestamp().Format(timeFormat),
		Labels:      hw.Labels,
		Annotations: hw.Annotations,
		Spec:        hardwareSpecRows(hw.Spec),
		References:  referenceRows(hw.Spec.References),
		Conditions:  bmcConditions(hw.Status.Conditions),
	}
	d.Instance, d.InstanceData = instanceRows(hw.Spec.Instance)
	d.BMC, d.BMCProviderOptions = bmcRows(hw, baseURL)
	if a := hw.Status.Attributes; a != nil {
		d.AgentAttributes = templates.AgentAttributesFromInBand(a.InBand)
		d.OutOfBand = templates.OutOfBandAttributesFromStatus(a.OutOfBand)
	}
	return d
}

// Formatting helpers.

func deref[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func joinStrings[T ~string](in []T) string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}

func seconds(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10) + "s"
}

func enabled(b bool) string {
	if b {
		return valueEnabled
	}
	return valueOff
}

func yesNo(b bool) string {
	if b {
		return valueYes
	}
	return valueNo
}

func customized(b bool) string {
	if b {
		return "Custom"
	}
	return ""
}

func nonZero[T ~int | ~int32 | ~int64](v T) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprint(v)
}

func count(n int, noun string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func namespacedName(namespace, name string) string {
	switch {
	case name == "":
		return ""
	case namespace == "":
		return name
	}
	return namespace + "/" + name
}

func formatTime(t *metav1.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(timeFormat)
}
