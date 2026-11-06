package conversion

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/randfill"
)

// apiRoundTrip mimics the API server: objects travel as JSON.
func apiRoundTrip[T any](t *testing.T, in *T) *T {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out := new(T)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
	return out
}

var cmpOpts = []cmp.Option{cmpopts.EquateEmpty()}

func v1Hardware() *v1alpha1.Hardware {
	return &v1alpha1.Hardware{
		TypeMeta:   metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha1", Kind: "Hardware"},
		ObjectMeta: metav1.ObjectMeta{Name: "hw1", Namespace: "tink", Labels: map[string]string{"a": "b"}},
		Spec: v1alpha1.HardwareSpec{
			AgentID: "52:54:00:00:00:01",
			BMCRef:  &corev1.TypedLocalObjectReference{Kind: "Machine", Name: "bmc-hw1"},
			Interfaces: []v1alpha1.Interface{
				{
					// Upper case MAC, explicit allowPXE, OSIE base URL only.
					DHCP: &v1alpha1.DHCP{
						MAC: "52:54:00:00:00:01", Hostname: "hw1", DomainName: "example.com", LeaseTime: 3600,
						NameServers: []string{"1.1.1.1"}, TimeServers: []string{"pool.ntp.org"}, Arch: "x86_64", UEFI: true,
						IfaceName: "eno1", VLANID: "10",
						IP:                    &v1alpha1.IP{Address: "10.0.0.5", Netmask: "255.255.255.0", Gateway: "10.0.0.1", Family: 4},
						ClasslessStaticRoutes: []v1alpha1.ClasslessStaticRoute{{DestinationDescriptor: "10.1.0.0/16", Router: "10.0.0.1"}},
					},
					Netboot: &v1alpha1.Netboot{
						AllowPXE: ptr(true), AllowWorkflow: ptr(true),
						OSIE: &v1alpha1.OSIE{BaseURL: "http://osie.example.com/hook", KernelParams: []string{"console=ttyS0"}},
						IPXE: &v1alpha1.IPXE{Binary: "ipxe.efi"},
					},
					Isoboot: &v1alpha1.Isoboot{SourceISO: "http://iso.example.com/hook.iso"},
				},
				{
					// IPv6, netboot data absent.
					DHCP: &v1alpha1.DHCP{
						MAC: "52:54:00:00:00:02", LeaseTime: 7200,
						IP: &v1alpha1.IP{Address: "2001:db8::5", Gateway: "2001:db8::1", Family: 6},
					},
					DisableDHCP: true,
				},
				{
					// No MAC: v1alpha2 cannot hold it.
					DHCP:    &v1alpha1.DHCP{Hostname: "orphan"},
					Netboot: &v1alpha1.Netboot{AllowPXE: ptr(false)},
				},
			},
			References: map[string]v1alpha1.Reference{"cfg": {Name: "c", Namespace: "tink", Resource: "configmaps", Version: "v1"}},
			Metadata: &v1alpha1.HardwareMetadata{
				State:    "provisioning",
				Facility: &v1alpha1.MetadataFacility{FacilityCode: "onprem", PlanSlug: "c3.small"},
				Instance: &v1alpha1.MetadataInstance{
					ID: "hw1-id", Hostname: "hw1", SSHKeys: []string{"ssh-ed25519 AAAA"},
					OperatingSystem: &v1alpha1.MetadataInstanceOperatingSystem{Slug: "talos"},
				},
				OSIEFiles: []v1alpha1.OSIEFile{{Path: "etc/x", Contents: "hello", Mode: "0644"}},
			},
			TinkVersion: 2,
			Disks:       []v1alpha1.Disk{{Device: "/dev/nvme0n1"}},
			Resources:   map[string]resource.Quantity{"cpu": resource.MustParse("8")},
			UserData:    ptr("#cloud-config\n"),
			VendorData:  ptr("vendor"),
		},
		Status: v1alpha1.HardwareStatus{
			State: v1alpha1.HardwareReady,
			Attributes: &v1alpha1.HardwareAttributes{InBand: &v1alpha1.Attributes{
				CollectionMethod: "agent",
				CPU:              &v1alpha1.CPU{TotalCores: 8},
				BlockDevices:     []v1alpha1.BlockDevice{{Name: "nvme0n1", Path: "pci-0000:01:00.0-nvme-1"}},
			}},
		},
	}
}

func toV2(t *testing.T, in *v1alpha1.Hardware) *v1alpha2.Hardware {
	t.Helper()
	out := &v1alpha2.Hardware{}
	if err := HardwareToV1Alpha2(apiRoundTrip(t, in), out); err != nil {
		t.Fatal(err)
	}
	return apiRoundTrip(t, out)
}

func toV1(t *testing.T, in *v1alpha2.Hardware) *v1alpha1.Hardware {
	t.Helper()
	out := &v1alpha1.Hardware{}
	if err := HardwareToV1Alpha1(apiRoundTrip(t, in), out); err != nil {
		t.Fatal(err)
	}
	return apiRoundTrip(t, out)
}

func TestHardwareToV1Alpha2Mapping(t *testing.T) {
	got := toV2(t, v1Hardware())
	s := got.Spec

	if s.Arch != "x86_64" || s.AgentID != "52:54:00:00:00:01" {
		t.Errorf("arch/agentID: got %q %q", s.Arch, s.AgentID)
	}
	if len(s.NetworkInterfaces) != 2 {
		t.Fatalf("want 2 interfaces (the MAC-less one has no v1alpha2 form), got %d", len(s.NetworkInterfaces))
	}
	n1 := s.NetworkInterfaces["52:54:00:00:00:01"]
	wantIP := &v1alpha2.IP{
		Address: "10.0.0.5", Gateway: "10.0.0.1", Prefix: "24", Hostname: ptr("hw1"), VLANID: ptr("10"),
		DomainSearchList: []string{"example.com"}, Nameservers: []v1alpha2.Nameserver{"1.1.1.1"}, NTPServers: []v1alpha2.Timeserver{"pool.ntp.org"},
		ClasslessStaticRoutes: []v1alpha2.ClasslessStaticRoute{{DestinationDescriptor: "10.1.0.0/16", Router: "10.0.0.1"}},
	}
	if d := cmp.Diff(wantIP, n1.IPAM.IPv4, cmpOpts...); d != "" {
		t.Errorf("ipam.ipv4 (-want +got):\n%s", d)
	}
	if n1.Netboot.Disabled || n1.Netboot.IPXE.Binary != "ipxe.efi" || deref(n1.DHCP.IPv4.LeaseTimeSeconds) != 3600 {
		t.Errorf("netboot/lease: %+v %+v", n1.Netboot, n1.DHCP.IPv4)
	}
	n2 := s.NetworkInterfaces["52:54:00:00:00:02"]
	if n2.IPAM.IPv6 == nil || n2.IPAM.IPv6.Address != "2001:db8::5" || n2.IPAM.IPv4 != nil {
		t.Errorf("ipv6 interface: %+v", n2.IPAM)
	}
	if !n2.DHCP.IPv4.Disabled || !n2.DHCP.IPv6.Disabled || deref(n2.DHCP.IPv6.ValidLifetime) != 7200 {
		t.Errorf("disableDhcp/lease should disable both families: %+v %+v", n2.DHCP.IPv4, n2.DHCP.IPv6)
	}
	if !n2.Netboot.Disabled {
		t.Error("an interface without v1alpha1 netboot data must not netboot")
	}

	wantOSIE := &v1alpha2.OSIE{
		ISOURL:       "http://iso.example.com/hook.iso",
		KernelURL:    "http://osie.example.com/hook/vmlinuz-${arch}",
		InitrdURL:    "http://osie.example.com/hook/initramfs-${arch}",
		KernelParams: []string{"console=ttyS0"},
		Files:        []v1alpha2.OSIEFile{{Path: "etc/x", Contents: "hello", Mode: "0644"}},
	}
	if d := cmp.Diff(wantOSIE, s.Instance.OSIE, cmpOpts...); d != "" {
		t.Errorf("osie (-want +got):\n%s", d)
	}
	if s.Instance.LookupID != "hw1-id" || deref(s.Instance.Userdata) != "#cloud-config\n" || len(s.Instance.SSHKeys) != 1 {
		t.Errorf("instance: %+v", s.Instance)
	}
	if len(s.StorageDevices) != 1 || s.StorageDevices[0].Name != "/dev/nvme0n1" {
		t.Errorf("storage devices: %+v", s.StorageDevices)
	}
	if got.Status.Attributes == nil || got.Status.Attributes.InBand.BlockDevices[0].Path != "pci-0000:01:00.0-nvme-1" {
		t.Errorf("status attributes: %+v", got.Status.Attributes)
	}
	if _, ok := got.Annotations[AnnotationV1Alpha1]; !ok {
		t.Error("missing conversion annotation")
	}
}

func TestHardwareRoundTripFromV1Alpha1(t *testing.T) {
	orig := v1Hardware()
	hub := toV2(t, orig)
	// The API server applies v1alpha2 defaults when it reads the stored object.
	hub.Spec = normalizeHardwareV2(hub.Spec)
	back := toV1(t, hub)

	if d := cmp.Diff(apiRoundTrip(t, orig), back, cmpOpts...); d != "" {
		// The only difference allowed is the reverse annotation.
		if _, ok := back.Annotations[AnnotationV1Alpha2]; !ok {
			t.Fatal("missing v1alpha2 conversion annotation")
		}
		delete(back.Annotations, AnnotationV1Alpha2)
		if d := cmp.Diff(apiRoundTrip(t, orig), back, cmpOpts...); d != "" {
			t.Errorf("v1alpha1 -> v1alpha2 -> v1alpha1 changed the object (-want +got):\n%s", d)
		}
	}
}

func v2Hardware() *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		TypeMeta:   metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha2", Kind: "Hardware"},
		ObjectMeta: metav1.ObjectMeta{Name: "hw2", Namespace: "tink"},
		Spec: v1alpha2.HardwareSpec{
			AgentID: "52:54:00:00:00:10",
			Arch:    "aarch64",
			BMC: &v1alpha2.BMC{BootMode: v1alpha2.FirmwareModeUEFI, Connection: &bmc.Connection{
				Host: "10.0.80.1", Port: 443, CredentialsRef: bmc.SimpleReference{Name: "creds", Namespace: "tink"},
			}},
			Instance: &v1alpha2.Instance{
				LookupID: "abc", Metadata: ptr("instance-id: abc"), Vars: map[string]string{"k": "v"},
				Userdata: ptr("big"), OSIE: &v1alpha2.OSIE{KernelURL: "vmlinuz-custom", InitrdURL: "initramfs-custom"},
			},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{
				"52:54:00:00:00:10": {
					IPAM: &v1alpha2.IPAM{
						IPv4: &v1alpha2.IP{Address: "10.0.0.10", Prefix: "24", Gateway: "10.0.0.1", DomainSearchList: []string{"a.example", "b.example"}},
						IPv6: &v1alpha2.IP{Address: "2001:db8::10", Prefix: "64"},
					},
					DHCP: &v1alpha2.DHCP{
						IPv4: &v1alpha2.DHCPv4{LeaseTimeSeconds: ptr(defaultLeaseTimeSeconds)},
						IPv6: &v1alpha2.DHCPv6{PreferredLifetime: ptr[int64](600), ValidLifetime: ptr[int64](1200)},
					},
					Netboot: &v1alpha2.Netboot{IPv6: &v1alpha2.Netbootv6{BootFileURL: "tftp://[2001:db8::1]/x.efi"}},
				},
			},
		},
		Status: v1alpha2.HardwareStatus{Conditions: []bmc.Condition{{Type: bmc.ConditionTypeBMCContactable, Status: bmc.ConditionStatusTrue}}},
	}
}

func TestHardwareRoundTripFromV1Alpha2(t *testing.T) {
	orig := v2Hardware()
	spoke := toV1(t, orig)
	if len(spoke.Spec.Interfaces) != 1 || spoke.Spec.Interfaces[0].DHCP.IP.Address != "10.0.0.10" || spoke.Spec.Interfaces[0].DHCP.IP.Netmask != "255.255.255.0" {
		t.Errorf("v1alpha1 view: %+v", spoke.Spec.Interfaces)
	}
	if o := spoke.Spec.Interfaces[0].Netboot.OSIE; o == nil || o.Kernel != "vmlinuz-custom" || o.BaseURL != "" {
		t.Errorf("relative kernel URL should map to a kernel name: %+v", o)
	}
	back := toV2(t, spoke)
	delete(back.Annotations, AnnotationV1Alpha1)
	if d := cmp.Diff(apiRoundTrip(t, orig), back, cmpOpts...); d != "" {
		t.Errorf("v1alpha2 -> v1alpha1 -> v1alpha2 changed the object (-want +got):\n%s", d)
	}
}

// A v1alpha2 client changes an object that came from v1alpha1: the change shows in
// v1alpha1 and everything v1alpha2 cannot express is still there.
func TestHardwareRestoreAfterV1Alpha2Change(t *testing.T) {
	hub := toV2(t, v1Hardware())
	ni := hub.Spec.NetworkInterfaces["52:54:00:00:00:01"]
	ni.IPAM.IPv4.Address = "10.0.0.99"
	hub.Spec.NetworkInterfaces["52:54:00:00:00:01"] = ni
	hub.Spec.Instance.OSIE.KernelParams = []string{"console=tty0"}

	got := toV1(t, hub)
	s := got.Spec
	if len(s.Interfaces) != 3 {
		t.Fatalf("want 3 interfaces, got %d", len(s.Interfaces))
	}
	if s.Interfaces[0].DHCP.MAC != "52:54:00:00:00:01" || s.Interfaces[2].DHCP.Hostname != "orphan" {
		t.Errorf("interface order not kept: %+v", s.Interfaces)
	}
	i0 := s.Interfaces[0]
	if i0.DHCP.IP.Address != "10.0.0.99" || i0.DHCP.IfaceName != "eno1" || !i0.DHCP.UEFI {
		t.Errorf("changed interface: %+v", i0.DHCP)
	}
	if i0.Netboot.OSIE.KernelParams[0] != "console=tty0" || i0.Netboot.OSIE.BaseURL != "http://osie.example.com/hook" {
		t.Errorf("osie: %+v", i0.Netboot.OSIE)
	}
	// The untouched IPv6 interface converts back exactly.
	if d := cmp.Diff(v1Hardware().Spec.Interfaces[1], s.Interfaces[1], cmpOpts...); d != "" {
		t.Errorf("unchanged interface (-want +got):\n%s", d)
	}
	if s.BMCRef == nil || s.Metadata.Facility.FacilityCode != "onprem" || s.Metadata.Instance.OperatingSystem.Slug != "talos" || s.TinkVersion != 2 {
		t.Errorf("v1alpha1-only data lost: %+v %+v", s.BMCRef, s.Metadata)
	}
	if deref(s.UserData) != "#cloud-config\n" || s.Metadata.OSIEFiles[0].Contents != "hello" || got.Status.State != v1alpha1.HardwareReady {
		t.Error("lossless fields lost")
	}
}

// A v1alpha1 client changes an object that came from v1alpha2.
func TestHardwareRestoreAfterV1Alpha1Change(t *testing.T) {
	spoke := toV1(t, v2Hardware())
	spoke.Spec.Interfaces[0].DHCP.Hostname = "renamed"
	spoke.Labels = map[string]string{"x": "y"}

	got := toV2(t, spoke)
	s := got.Spec
	n := s.NetworkInterfaces["52:54:00:00:00:10"]
	if deref(n.IPAM.IPv4.Hostname) != "renamed" {
		t.Errorf("change lost: %+v", n.IPAM.IPv4)
	}
	if n.IPAM.IPv6 == nil || n.IPAM.IPv6.Address != "2001:db8::10" || len(n.IPAM.IPv4.DomainSearchList) != 2 {
		t.Errorf("v1alpha2-only IPAM lost: %+v", n.IPAM)
	}
	if n.DHCP.IPv6 == nil || deref(n.DHCP.IPv6.PreferredLifetime) != 600 || n.Netboot.IPv6 == nil {
		t.Errorf("v1alpha2-only DHCPv6/netboot lost: %+v %+v", n.DHCP.IPv6, n.Netboot)
	}
	if s.BMC == nil || s.BMC.Connection.Port != 443 || deref(s.Instance.Metadata) != "instance-id: abc" || s.Instance.Vars["k"] != "v" {
		t.Errorf("v1alpha2-only spec lost: %+v %+v", s.BMC, s.Instance)
	}
	if len(got.Status.Conditions) != 1 {
		t.Errorf("conditions lost: %+v", got.Status.Conditions)
	}
}

// Arbitrary v1alpha1 specs survive v1alpha1 -> v1alpha2 -> v1alpha1.
func TestHardwareRoundTripFuzz(t *testing.T) {
	f := randfill.New().NilChance(0.3).NumElements(0, 3).Funcs(
		func(q *resource.Quantity, _ randfill.Continue) { *q = resource.MustParse("1") },
		func(tm *metav1.Time, c randfill.Continue) { *tm = metav1.Unix(c.Int63n(1<<31), 0) },
	)
	for range 300 {
		in := &v1alpha1.Hardware{TypeMeta: metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha1", Kind: "Hardware"}, ObjectMeta: metav1.ObjectMeta{Name: "x"}}
		f.Fill(&in.Spec)
		f.Fill(&in.Status.State)
		in = apiRoundTrip(t, in)
		hub := toV2(t, in)
		hub.Spec = normalizeHardwareV2(hub.Spec)
		back := toV1(t, hub)
		delete(back.Annotations, AnnotationV1Alpha2)
		if d := cmp.Diff(in, back, cmpOpts...); d != "" {
			t.Fatalf("round trip changed the object (-want +got):\n%s", d)
		}
	}
}

func TestHardwareBMCRef(t *testing.T) {
	hub := toV2(t, v1Hardware())
	if ref := HardwareBMCRef(hub); ref == nil || ref.Name != "bmc-hw1" {
		t.Errorf("bmcRef: %+v", ref)
	}
	if ref := HardwareBMCRef(v2Hardware()); ref != nil {
		t.Errorf("native v1alpha2 Hardware has no bmcRef, got %+v", ref)
	}
}
