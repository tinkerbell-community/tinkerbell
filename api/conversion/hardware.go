package conversion

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
)

const (
	// defaultKernel and defaultInitrd are the OSIE file names Smee uses when a
	// v1alpha1 Hardware sets an OSIE base URL but no file names. iPXE expands
	// ${arch} itself.
	defaultKernel = "vmlinuz-${arch}"
	defaultInitrd = "initramfs-${arch}"

	// defaultLeaseTimeSeconds mirrors the v1alpha2 CRD default for dhcp.ipv4.leaseTimeSeconds.
	defaultLeaseTimeSeconds int64 = 86400
)

// hardwareV1Status is the v1alpha1 Hardware status data v1alpha2 cannot represent.
type hardwareV1Status struct {
	State v1alpha1.HardwareState `json:"state,omitempty"`
}

// hardwareV2Status is the v1alpha2 Hardware status data v1alpha1 cannot represent.
type hardwareV2Status struct {
	Conditions []bmc.Condition `json:"conditions,omitempty"`
}

// HardwareToV1Alpha2 converts a v1alpha1 Hardware to v1alpha2.
func HardwareToV1Alpha2(src *v1alpha1.Hardware, dst *v1alpha2.Hardware) error {
	dst.ObjectMeta = *src.ObjectMeta.DeepCopy()
	dst.SetGroupVersionKind(v1alpha2.GroupVersion.WithKind("Hardware"))

	var hub stash[v1alpha2.HardwareSpec, hardwareV2Status]
	found, err := takeStash(src, dst, AnnotationV1Alpha2, &hub)
	if err != nil {
		return err
	}
	dropAnnotation(dst, AnnotationV1Alpha1)

	mapped := hardwareSpecToV2(src.Spec)
	dst.Spec = mapped
	if found {
		h, err := hashOf(src.Spec)
		if err != nil {
			return err
		}
		if h == hub.SpecHash {
			dst.Spec = hub.Spec
			refillHardwareV2(&dst.Spec, mapped)
		} else {
			restoreHardwareV2(&dst.Spec, hub.Spec)
		}
		dst.Status.Conditions = hub.Status.Conditions
	}
	if src.Status.Attributes != nil {
		if err := jsonCopy(src.Status.Attributes, &dst.Status.Attributes); err != nil {
			return fmt.Errorf("converting Hardware status attributes: %w", err)
		}
	}

	h, err := hashOf(normalizeHardwareV2(dst.Spec))
	if err != nil {
		return err
	}
	return putStash(dst, AnnotationV1Alpha1, stash[v1alpha1.HardwareSpec, hardwareV1Status]{
		Spec:     stripHardwareV1(src.Spec),
		SpecHash: h,
		Status:   hardwareV1Status{State: src.Status.State},
	})
}

// HardwareToV1Alpha1 converts a v1alpha2 Hardware to v1alpha1.
func HardwareToV1Alpha1(src *v1alpha2.Hardware, dst *v1alpha1.Hardware) error {
	dst.ObjectMeta = *src.ObjectMeta.DeepCopy()
	dst.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Hardware"))

	var spoke stash[v1alpha1.HardwareSpec, hardwareV1Status]
	found, err := takeStash(src, dst, AnnotationV1Alpha1, &spoke)
	if err != nil {
		return err
	}
	dropAnnotation(dst, AnnotationV1Alpha2)

	mapped := hardwareSpecToV1(src.Spec)
	dst.Spec = mapped
	if found {
		h, err := hashOf(normalizeHardwareV2(src.Spec))
		if err != nil {
			return err
		}
		if h == spoke.SpecHash {
			dst.Spec = spoke.Spec
			refillHardwareV1(&dst.Spec, mapped)
		} else {
			restoreHardwareV1(&dst.Spec, spoke.Spec, src.Spec)
		}
		dst.Status.State = spoke.Status.State
	}
	if src.Status.Attributes != nil {
		if err := jsonCopy(src.Status.Attributes, &dst.Status.Attributes); err != nil {
			return fmt.Errorf("converting Hardware status attributes: %w", err)
		}
	}

	h, err := hashOf(dst.Spec)
	if err != nil {
		return err
	}
	return putStash(dst, AnnotationV1Alpha2, stash[v1alpha2.HardwareSpec, hardwareV2Status]{
		Spec:     stripHardwareV2(src.Spec),
		SpecHash: h,
		Status:   hardwareV2Status{Conditions: src.Status.Conditions},
	})
}

// hardwareSpecToV2 maps every v1alpha1 field that has a v1alpha2 equivalent.
func hardwareSpecToV2(s v1alpha1.HardwareSpec) v1alpha2.HardwareSpec {
	d := v1alpha2.HardwareSpec{
		AgentID:    s.AgentID,
		Auto:       v1alpha2.AutoCapabilities{EnrollmentEnabled: s.Auto.EnrollmentEnabled},
		References: referencesToV2(s.References),
	}
	for _, disk := range s.Disks {
		if disk.Device != "" {
			d.StorageDevices = append(d.StorageDevices, v1alpha2.StorageDevice{Name: disk.Device})
		}
	}

	inst := v1alpha2.Instance{Userdata: copyPtr(s.UserData), Vendordata: copyPtr(s.VendorData)}
	osie := v1alpha2.OSIE{}
	if m := s.Metadata; m != nil {
		if m.Instance != nil {
			inst.LookupID = m.Instance.ID
			inst.SSHKeys = slices.Clone(m.Instance.SSHKeys)
		}
		for _, f := range m.OSIEFiles {
			osie.Files = append(osie.Files, v1alpha2.OSIEFile{Path: f.Path, Contents: f.Contents, Mode: f.Mode})
		}
	}

	for _, iface := range s.Interfaces {
		mac, ni, ok := interfaceToV2(iface)
		if !ok {
			continue
		}
		if d.NetworkInterfaces == nil {
			d.NetworkInterfaces = v1alpha2.NetworkInterfaces{}
		}
		d.NetworkInterfaces[mac] = ni
		if d.Arch == "" {
			d.Arch = iface.DHCP.Arch
		}
		mergeOSIE(&osie, interfaceOSIE(iface))
	}
	if !reflect.DeepEqual(osie, v1alpha2.OSIE{}) {
		inst.OSIE = &osie
	}
	if !reflect.DeepEqual(inst, v1alpha2.Instance{}) {
		d.Instance = &inst
	}
	return d
}

// interfaceToV2 maps a v1alpha1 interface. Interfaces without a MAC address
// have no v1alpha2 representation; they survive in the conversion annotation.
func interfaceToV2(iface v1alpha1.Interface) (v1alpha2.MAC, v1alpha2.NetworkInterface, bool) {
	dh := iface.DHCP
	if dh == nil || dh.MAC == "" {
		return "", v1alpha2.NetworkInterface{}, false
	}
	ni := v1alpha2.NetworkInterface{}
	v6 := isV6(dh.IP)

	ip := v1alpha2.IP{
		Hostname:              nonEmpty(dh.Hostname),
		VLANID:                nonEmpty(dh.VLANID),
		Nameservers:           toNameservers(dh.NameServers),
		NTPServers:            toTimeservers(dh.TimeServers),
		ClasslessStaticRoutes: routesToV2(dh.ClasslessStaticRoutes),
	}
	if dh.DomainName != "" {
		ip.DomainSearchList = []string{dh.DomainName}
	}
	if dh.IP != nil {
		ip.Address = dh.IP.Address
		ip.Gateway = dh.IP.Gateway
		ip.Prefix = netmaskToPrefix(dh.IP.Netmask)
	}
	if !reflect.DeepEqual(ip, v1alpha2.IP{}) {
		if v6 {
			ni.IPAM = &v1alpha2.IPAM{IPv6: &ip}
		} else {
			ni.IPAM = &v1alpha2.IPAM{IPv4: &ip}
		}
	}

	dhcp := v1alpha2.DHCP{}
	if v6 {
		dhcp.IPv6 = &v1alpha2.DHCPv6{Disabled: iface.DisableDHCP}
		if dh.LeaseTime > 0 {
			dhcp.IPv6.ValidLifetime = ptr(dh.LeaseTime)
		}
	} else {
		dhcp.IPv4 = &v1alpha2.DHCPv4{Disabled: iface.DisableDHCP}
		if dh.LeaseTime > 0 {
			dhcp.IPv4.LeaseTimeSeconds = ptr(dh.LeaseTime)
		}
	}
	if iface.DisableDHCP {
		// v1alpha1 disableDhcp turns off DHCP for the interface in both families.
		if dhcp.IPv4 == nil {
			dhcp.IPv4 = &v1alpha2.DHCPv4{}
		}
		if dhcp.IPv6 == nil {
			dhcp.IPv6 = &v1alpha2.DHCPv6{}
		}
		dhcp.IPv4.Disabled, dhcp.IPv6.Disabled = true, true
	}
	ni.DHCP = &dhcp

	nb := v1alpha2.Netboot{}
	if n := iface.Netboot; n == nil {
		// v1alpha1 serves no netboot options to an interface without netboot data.
		nb.Disabled = true
	} else {
		// v1alpha1 netboots only when allowPXE is explicitly true.
		nb.Disabled = n.AllowPXE == nil || !*n.AllowPXE
		if n.IPXE != nil && (n.IPXE.URL != "" || n.IPXE.Contents != "" || n.IPXE.Binary != "") {
			nb.IPXE = &v1alpha2.IPXE{Binary: n.IPXE.Binary, Script: n.IPXE.Contents, URL: n.IPXE.URL}
		}
		if n.PXELINUX != nil {
			nb.PXELINUX = &v1alpha2.PXELINUX{Config: n.PXELINUX.Config}
		}
		if n.RPI != nil {
			nb.RPI = &v1alpha2.RPI{ConfigTxt: n.RPI.ConfigTxt, FirmwarePath: n.RPI.FirmwarePath, SerialNum: n.RPI.SerialNum}
		}
	}
	if dh.TFTPServerName != "" || dh.BootFileName != "" {
		nb.IPv4 = &v1alpha2.Netbootv4{TFTPServerName: dh.TFTPServerName, BootFileName: dh.BootFileName}
	}
	ni.Netboot = &nb

	return v1alpha2.MAC(strings.ToLower(dh.MAC)), ni, true
}

// interfaceOSIE returns the instance-wide OSIE settings a v1alpha1 interface
// implies. v1alpha1 configures the OSIE per interface; v1alpha2 per Hardware.
func interfaceOSIE(iface v1alpha1.Interface) v1alpha2.OSIE {
	o := v1alpha2.OSIE{}
	if iface.Isoboot != nil {
		o.ISOURL = iface.Isoboot.SourceISO
	}
	if iface.Netboot == nil || iface.Netboot.OSIE == nil {
		return o
	}
	src := iface.Netboot.OSIE
	o.KernelParams = slices.Clone(src.KernelParams)
	if src.BaseURL != "" || src.Kernel != "" {
		o.KernelURL = joinURL(src.BaseURL, orDefault(src.Kernel, defaultKernel))
	}
	if src.BaseURL != "" || src.Initrd != "" {
		o.InitrdURL = joinURL(src.BaseURL, orDefault(src.Initrd, defaultInitrd))
	}
	return o
}

// mergeOSIE fills the unset fields of dst from src; the first interface wins.
func mergeOSIE(dst *v1alpha2.OSIE, src v1alpha2.OSIE) {
	if dst.ISOURL == "" {
		dst.ISOURL = src.ISOURL
	}
	if dst.KernelURL == "" {
		dst.KernelURL = src.KernelURL
	}
	if dst.InitrdURL == "" {
		dst.InitrdURL = src.InitrdURL
	}
	if dst.KernelParams == nil {
		dst.KernelParams = src.KernelParams
	}
}

// hardwareSpecToV1 maps every v1alpha2 field that has a v1alpha1 equivalent.
func hardwareSpecToV1(s v1alpha2.HardwareSpec) v1alpha1.HardwareSpec {
	d := v1alpha1.HardwareSpec{
		AgentID:    s.AgentID,
		Auto:       v1alpha1.AutoCapabilities{EnrollmentEnabled: s.Auto.EnrollmentEnabled},
		References: referencesToV1(s.References),
	}
	for _, sd := range s.StorageDevices {
		d.Disks = append(d.Disks, v1alpha1.Disk{Device: sd.Name})
	}

	var osie v1alpha2.OSIE
	if inst := s.Instance; inst != nil {
		d.UserData = copyPtr(inst.Userdata)
		d.VendorData = copyPtr(inst.Vendordata)
		if inst.OSIE != nil {
			osie = *inst.OSIE
		}
		md := v1alpha1.HardwareMetadata{}
		if inst.LookupID != "" || len(inst.SSHKeys) > 0 {
			md.Instance = &v1alpha1.MetadataInstance{ID: inst.LookupID, SSHKeys: slices.Clone(inst.SSHKeys)}
		}
		for _, f := range osie.Files {
			md.OSIEFiles = append(md.OSIEFiles, v1alpha1.OSIEFile{Path: f.Path, Contents: f.Contents, Mode: f.Mode})
		}
		if !reflect.DeepEqual(md, v1alpha1.HardwareMetadata{}) {
			d.Metadata = &md
		}
	}
	uefi := s.BMC != nil && s.BMC.BootMode != v1alpha2.FirmwareModeBIOS

	for _, mac := range slices.Sorted(maps.Keys(s.NetworkInterfaces)) {
		d.Interfaces = append(d.Interfaces, interfaceToV1(mac, s.NetworkInterfaces[mac], s.Arch, uefi, osie))
	}
	return d
}

func interfaceToV1(mac v1alpha2.MAC, ni v1alpha2.NetworkInterface, arch string, uefi bool, osie v1alpha2.OSIE) v1alpha1.Interface {
	dh := &v1alpha1.DHCP{MAC: string(mac), Arch: arch, UEFI: uefi}
	var ip *v1alpha2.IP
	v6 := false
	if ni.IPAM != nil {
		switch {
		case ni.IPAM.IPv4 != nil:
			ip = ni.IPAM.IPv4
		case ni.IPAM.IPv6 != nil:
			ip, v6 = ni.IPAM.IPv6, true
		}
	}
	if ip != nil {
		dh.Hostname = deref(ip.Hostname)
		dh.VLANID = deref(ip.VLANID)
		if len(ip.DomainSearchList) > 0 {
			dh.DomainName = ip.DomainSearchList[0]
		}
		for _, n := range ip.Nameservers {
			dh.NameServers = append(dh.NameServers, string(n))
		}
		for _, n := range ip.NTPServers {
			dh.TimeServers = append(dh.TimeServers, string(n))
		}
		dh.ClasslessStaticRoutes = routesToV1(ip.ClasslessStaticRoutes)
		if ip.Address != "" || ip.Gateway != "" || ip.Prefix != "" {
			dh.IP = &v1alpha1.IP{Address: ip.Address, Gateway: ip.Gateway, Family: 4}
			if v6 {
				dh.IP.Family = 6
			} else {
				dh.IP.Netmask = prefixToNetmask(ip.Prefix)
			}
		}
	}

	iface := v1alpha1.Interface{DHCP: dh}
	if ni.DHCP != nil {
		if v6 && ni.DHCP.IPv6 != nil {
			dh.LeaseTime = deref(ni.DHCP.IPv6.ValidLifetime)
		} else if ni.DHCP.IPv4 != nil {
			dh.LeaseTime = deref(ni.DHCP.IPv4.LeaseTimeSeconds)
		}
		iface.DisableDHCP = (ni.DHCP.IPv4 != nil && ni.DHCP.IPv4.Disabled) || (v6 && ni.DHCP.IPv6 != nil && ni.DHCP.IPv6.Disabled)
	}

	n := &v1alpha1.Netboot{AllowPXE: ptr(true), AllowWorkflow: ptr(true)}
	if nb := ni.Netboot; nb != nil {
		n.AllowPXE = ptr(!nb.Disabled)
		if nb.IPXE != nil {
			n.IPXE = &v1alpha1.IPXE{URL: nb.IPXE.URL, Contents: nb.IPXE.Script, Binary: nb.IPXE.Binary}
		}
		if nb.PXELINUX != nil {
			n.PXELINUX = &v1alpha1.PXELINUX{Config: nb.PXELINUX.Config}
		}
		if nb.RPI != nil {
			n.RPI = &v1alpha1.RPI{ConfigTxt: nb.RPI.ConfigTxt, FirmwarePath: nb.RPI.FirmwarePath, SerialNum: nb.RPI.SerialNum}
		}
		if nb.IPv4 != nil {
			dh.TFTPServerName = nb.IPv4.TFTPServerName
			dh.BootFileName = nb.IPv4.BootFileName
		}
	}
	if o := osieToV1(osie); o != nil {
		n.OSIE = o
	}
	iface.Netboot = n
	if osie.ISOURL != "" {
		iface.Isoboot = &v1alpha1.Isoboot{SourceISO: osie.ISOURL}
	}
	return iface
}

// osieToV1 splits the v1alpha2 OSIE URLs into the v1alpha1 base URL and file
// names. It is the inverse of interfaceOSIE for the URLs interfaceOSIE builds.
func osieToV1(o v1alpha2.OSIE) *v1alpha1.OSIE {
	if o.KernelURL == "" && o.InitrdURL == "" && len(o.KernelParams) == 0 {
		return nil
	}
	out := &v1alpha1.OSIE{KernelParams: slices.Clone(o.KernelParams)}
	kBase, kName := splitURL(o.KernelURL)
	iBase, iName := splitURL(o.InitrdURL)
	switch {
	case o.KernelURL == "":
		out.BaseURL = iBase
	case o.InitrdURL == "" || kBase == iBase:
		out.BaseURL = kBase
	default:
		// Different directories cannot be expressed as one base URL; keep the kernel's.
		out.BaseURL = kBase
	}
	if kName != defaultKernel {
		out.Kernel = kName
	}
	if iName != defaultInitrd {
		out.Initrd = iName
	}
	return out
}

// stripHardwareV1 drops the fields that convert without loss and can be large,
// so the annotation stays small. refillHardwareV1 restores them.
func stripHardwareV1(s v1alpha1.HardwareSpec) v1alpha1.HardwareSpec {
	s = *s.DeepCopy()
	s.UserData, s.VendorData = nil, nil
	if s.Metadata != nil {
		for i := range s.Metadata.OSIEFiles {
			s.Metadata.OSIEFiles[i].Contents = ""
		}
	}
	return s
}

func refillHardwareV1(s *v1alpha1.HardwareSpec, from v1alpha1.HardwareSpec) {
	s.UserData, s.VendorData = from.UserData, from.VendorData
	if s.Metadata != nil && from.Metadata != nil && len(s.Metadata.OSIEFiles) == len(from.Metadata.OSIEFiles) {
		for i := range s.Metadata.OSIEFiles {
			s.Metadata.OSIEFiles[i].Contents = from.Metadata.OSIEFiles[i].Contents
		}
	}
}

func stripHardwareV2(s v1alpha2.HardwareSpec) v1alpha2.HardwareSpec {
	s = *s.DeepCopy()
	if s.Instance != nil {
		s.Instance.Userdata, s.Instance.Vendordata = nil, nil
		if s.Instance.OSIE != nil {
			for i := range s.Instance.OSIE.Files {
				s.Instance.OSIE.Files[i].Contents = ""
			}
		}
	}
	return s
}

func refillHardwareV2(s *v1alpha2.HardwareSpec, from v1alpha2.HardwareSpec) {
	if s.Instance == nil || from.Instance == nil {
		return
	}
	s.Instance.Userdata, s.Instance.Vendordata = from.Instance.Userdata, from.Instance.Vendordata
	if s.Instance.OSIE != nil && from.Instance.OSIE != nil && len(s.Instance.OSIE.Files) == len(from.Instance.OSIE.Files) {
		for i := range s.Instance.OSIE.Files {
			s.Instance.OSIE.Files[i].Contents = from.Instance.OSIE.Files[i].Contents
		}
	}
}

// restoreHardwareV2 copies into s, a v1alpha2 spec mapped from a v1alpha1 object
// that was changed, the v1alpha2 data v1alpha1 cannot represent.
func restoreHardwareV2(s *v1alpha2.HardwareSpec, hub v1alpha2.HardwareSpec) {
	s.BMC = hub.BMC
	if hub.Instance != nil {
		if s.Instance == nil {
			s.Instance = &v1alpha2.Instance{}
		}
		s.Instance.Metadata = hub.Instance.Metadata
		s.Instance.NetworkConfig = hub.Instance.NetworkConfig
		s.Instance.Vars = hub.Instance.Vars
	}
	for mac, ni := range s.NetworkInterfaces {
		old, ok := hub.NetworkInterfaces[mac]
		if !ok {
			continue
		}
		if old.IPAM != nil {
			if ni.IPAM == nil {
				ni.IPAM = &v1alpha2.IPAM{}
			}
			// A v1alpha1 interface carries one address; keep the other family.
			if ni.IPAM.IPv4 != nil && ni.IPAM.IPv6 == nil {
				ni.IPAM.IPv6 = old.IPAM.IPv6
			}
			if ni.IPAM.IPv6 != nil && ni.IPAM.IPv4 == nil {
				ni.IPAM.IPv4 = old.IPAM.IPv4
			}
			restoreSearchList(ni.IPAM.IPv4, old.IPAM.IPv4)
			restoreSearchList(ni.IPAM.IPv6, old.IPAM.IPv6)
		}
		if old.DHCP != nil && old.DHCP.IPv6 != nil {
			if ni.DHCP == nil {
				ni.DHCP = &v1alpha2.DHCP{}
			}
			if ni.DHCP.IPv6 == nil {
				ni.DHCP.IPv6 = old.DHCP.IPv6.DeepCopy()
			} else {
				if ni.DHCP.IPv6.InformationRefreshTime == nil {
					ni.DHCP.IPv6.InformationRefreshTime = old.DHCP.IPv6.InformationRefreshTime
				}
				if ni.DHCP.IPv6.PreferredLifetime == nil {
					ni.DHCP.IPv6.PreferredLifetime = old.DHCP.IPv6.PreferredLifetime
				}
			}
		}
		if old.Netboot != nil && old.Netboot.IPv6 != nil && ni.Netboot != nil && ni.Netboot.IPv6 == nil {
			ni.Netboot.IPv6 = old.Netboot.IPv6
		}
		s.NetworkInterfaces[mac] = ni
	}
}

// restoreSearchList keeps the extra v1alpha2 search domains when v1alpha1 only
// carried the first one, unchanged.
func restoreSearchList(cur, old *v1alpha2.IP) {
	if cur == nil || old == nil || len(old.DomainSearchList) < 2 || len(cur.DomainSearchList) > 1 {
		return
	}
	if len(cur.DomainSearchList) == 1 && cur.DomainSearchList[0] == old.DomainSearchList[0] {
		cur.DomainSearchList = slices.Clone(old.DomainSearchList)
	}
}

// restoreHardwareV1 merges a v1alpha1 spec mapped from a changed v1alpha2 object
// (s) with the v1alpha1 spec it was originally converted from (spoke). Whatever
// still converts to the current v1alpha2 data keeps its original v1alpha1 form.
func restoreHardwareV1(s *v1alpha1.HardwareSpec, spoke v1alpha1.HardwareSpec, hub v1alpha2.HardwareSpec) {
	s.BMCRef = spoke.BMCRef
	s.TinkVersion = spoke.TinkVersion
	s.Resources = spoke.Resources

	if spoke.Metadata != nil {
		md := spoke.Metadata.DeepCopy()
		cur := s.Metadata
		if cur == nil {
			cur = &v1alpha1.HardwareMetadata{}
		}
		md.OSIEFiles = cur.OSIEFiles
		if cur.Instance != nil || md.Instance != nil {
			if md.Instance == nil {
				md.Instance = &v1alpha1.MetadataInstance{}
			}
			var id string
			var keys []string
			if cur.Instance != nil {
				id, keys = cur.Instance.ID, cur.Instance.SSHKeys
			}
			md.Instance.ID, md.Instance.SSHKeys = id, keys
		}
		s.Metadata = md
	}

	// Instance-wide OSIE and arch: keep the per-interface originals if they still
	// produce what v1alpha2 holds now.
	origOSIE := v1alpha2.OSIE{}
	origArch := ""
	for _, iface := range spoke.Interfaces {
		if _, _, ok := interfaceToV2(iface); ok {
			mergeOSIE(&origOSIE, interfaceOSIE(iface))
			if origArch == "" {
				origArch = iface.DHCP.Arch
			}
		}
	}
	curOSIE := v1alpha2.OSIE{}
	if hub.Instance != nil && hub.Instance.OSIE != nil {
		curOSIE = *hub.Instance.OSIE.DeepCopy()
		curOSIE.Files = nil
	}
	isoSame := origOSIE.ISOURL == curOSIE.ISOURL
	origOSIE.ISOURL, curOSIE.ISOURL = "", ""
	osieSame := reflect.DeepEqual(normalizeOSIE(origOSIE), normalizeOSIE(curOSIE))
	archSame := origArch == hub.Arch

	byMAC := map[string]v1alpha1.Interface{}
	for _, iface := range s.Interfaces {
		byMAC[iface.DHCP.MAC] = iface
	}
	var out []v1alpha1.Interface
	for _, orig := range spoke.Interfaces {
		mac, ni, ok := interfaceToV2(orig)
		if !ok {
			// No MAC, so v1alpha2 never had it: keep it as it was.
			out = append(out, orig)
			continue
		}
		cur, ok := byMAC[string(mac)]
		if !ok {
			continue // removed in v1alpha2
		}
		delete(byMAC, string(mac))
		if reflect.DeepEqual(normalizeInterface(ni), normalizeInterface(hub.NetworkInterfaces[mac])) {
			// Unchanged: the original representation, but follow instance-wide changes.
			kept := *orig.DeepCopy()
			if !osieSame && kept.Netboot != nil {
				kept.Netboot.OSIE = cur.Netboot.OSIE
			}
			if !isoSame {
				kept.Isoboot = cur.Isoboot
			}
			if !archSame {
				kept.DHCP.Arch = cur.DHCP.Arch
			}
			out = append(out, kept)
			continue
		}
		// Changed: the mapped interface plus the fields v1alpha2 has no place for.
		cur.DHCP.IfaceName = orig.DHCP.IfaceName
		cur.DHCP.UEFI = orig.DHCP.UEFI
		if cur.Netboot != nil && orig.Netboot != nil {
			cur.Netboot.AllowWorkflow = orig.Netboot.AllowWorkflow
		}
		if osieSame && orig.Netboot != nil && cur.Netboot != nil {
			cur.Netboot.OSIE = orig.Netboot.OSIE
		}
		if isoSame {
			cur.Isoboot = orig.Isoboot
		}
		if archSame {
			cur.DHCP.Arch = orig.DHCP.Arch
		}
		out = append(out, cur)
	}
	// Interfaces added in v1alpha2, in MAC order.
	for _, iface := range s.Interfaces {
		if _, ok := byMAC[iface.DHCP.MAC]; ok {
			out = append(out, iface)
		}
	}
	s.Interfaces = out
}

func normalizeOSIE(o v1alpha2.OSIE) v1alpha2.OSIE {
	if len(o.KernelParams) == 0 {
		o.KernelParams = nil
	}
	o.Files = nil
	return o
}

func normalizeInterface(ni v1alpha2.NetworkInterface) v1alpha2.NetworkInterface {
	ni = *ni.DeepCopy()
	if ni.DHCP != nil && ni.DHCP.IPv4 != nil && ni.DHCP.IPv4.LeaseTimeSeconds == nil {
		ni.DHCP.IPv4.LeaseTimeSeconds = ptr(defaultLeaseTimeSeconds)
	}
	return ni
}

// normalizeHardwareV2 applies the v1alpha2 CRD defaults, which the API server
// adds when it reads an object, so that hashes of the same object match.
func normalizeHardwareV2(s v1alpha2.HardwareSpec) v1alpha2.HardwareSpec {
	s = *s.DeepCopy()
	for mac, ni := range s.NetworkInterfaces {
		s.NetworkInterfaces[mac] = normalizeInterface(ni)
	}
	if s.BMC != nil {
		if s.BMC.BootMode == "" {
			s.BMC.BootMode = v1alpha2.FirmwareModeUEFI
		}
		normalizeConnection(s.BMC.Connection)
	}
	return s
}

func normalizeConnection(c *bmc.Connection) {
	if c != nil && c.ProviderOptions != nil && c.ProviderOptions.IntelAMT != nil && c.ProviderOptions.IntelAMT.HostScheme == "" {
		c.ProviderOptions.IntelAMT.HostScheme = "http"
	}
}

func referencesToV2(in map[string]v1alpha1.Reference) map[string]v1alpha2.Reference {
	if in == nil {
		return nil
	}
	out := make(map[string]v1alpha2.Reference, len(in))
	for k, r := range in {
		out[k] = v1alpha2.Reference{Group: r.Group, Name: r.Name, Namespace: r.Namespace, Resource: r.Resource, Version: r.Version}
	}
	return out
}

func referencesToV1(in map[string]v1alpha2.Reference) map[string]v1alpha1.Reference {
	if in == nil {
		return nil
	}
	out := make(map[string]v1alpha1.Reference, len(in))
	for k, r := range in {
		out[k] = v1alpha1.Reference{Group: r.Group, Name: r.Name, Namespace: r.Namespace, Resource: r.Resource, Version: r.Version}
	}
	return out
}

func routesToV2(in []v1alpha1.ClasslessStaticRoute) []v1alpha2.ClasslessStaticRoute {
	var out []v1alpha2.ClasslessStaticRoute
	for _, r := range in {
		out = append(out, v1alpha2.ClasslessStaticRoute{DestinationDescriptor: r.DestinationDescriptor, Router: r.Router})
	}
	return out
}

func routesToV1(in []v1alpha2.ClasslessStaticRoute) []v1alpha1.ClasslessStaticRoute {
	var out []v1alpha1.ClasslessStaticRoute
	for _, r := range in {
		out = append(out, v1alpha1.ClasslessStaticRoute{DestinationDescriptor: r.DestinationDescriptor, Router: r.Router})
	}
	return out
}

func toNameservers(in []string) []v1alpha2.Nameserver {
	var out []v1alpha2.Nameserver
	for _, s := range in {
		out = append(out, v1alpha2.Nameserver(s))
	}
	return out
}

func toTimeservers(in []string) []v1alpha2.Timeserver {
	var out []v1alpha2.Timeserver
	for _, s := range in {
		out = append(out, v1alpha2.Timeserver(s))
	}
	return out
}

// isV6 reports whether a v1alpha1 IP is IPv6, by family or by address.
func isV6(ip *v1alpha1.IP) bool {
	if ip == nil {
		return false
	}
	if ip.Family == 6 {
		return true
	}
	a, err := netip.ParseAddr(ip.Address)
	return err == nil && a.Is6() && !a.Is4In6()
}

// netmaskToPrefix converts a dotted IPv4 netmask to a prefix length. A
// non-contiguous or unparsable netmask has no prefix form and yields "".
func netmaskToPrefix(mask string) string {
	ip := net.ParseIP(mask)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	ones, bits := net.IPMask(ip.To4()).Size()
	if bits == 0 {
		return ""
	}
	return strconv.Itoa(ones)
}

func prefixToNetmask(prefix string) string {
	n, err := strconv.Atoi(prefix)
	if err != nil || n < 0 || n > 32 {
		return ""
	}
	return net.IP(net.CIDRMask(n, 32)).String()
}

// joinURL joins a base URL and a file name the way Smee's iPXE script does
// ("${base}/${name}"). An empty base gives a relative reference, which is
// resolved against the default OSIE URL.
func joinURL(base, name string) string {
	if base == "" {
		return name
	}
	return strings.TrimSuffix(base, "/") + "/" + name
}

// splitURL is the inverse of joinURL.
func splitURL(u string) (base, name string) {
	if u == "" {
		return "", ""
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		if parsed, err := url.Parse(u); err == nil && parsed.Scheme != "" && i < len(parsed.Scheme)+3 {
			return u, "" // only a host, no path
		}
		return u[:i], u[i+1:]
	}
	return "", u
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
