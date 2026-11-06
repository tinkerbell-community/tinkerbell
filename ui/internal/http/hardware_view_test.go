package webhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	tinkv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The legacy* functions are the web UI's v1alpha1 Hardware code as it was
// before the UI read v1alpha2. They are the reference the v1alpha2 code must
// match for Hardware created through v1alpha1.

func legacyHardwareInterfaces(hw tinkv1alpha1.Hardware) [][2]string {
	var out [][2]string
	for _, iface := range hw.Spec.Interfaces {
		if iface.DHCP != nil {
			mac := iface.DHCP.MAC
			ip := ""
			if iface.DHCP.IP != nil {
				ip = iface.DHCP.IP.Address
			}
			if mac != "" || ip != "" {
				out = append(out, [2]string{mac, ip})
			}
		}
	}
	return out
}

func legacyHardwareRow(hw tinkv1alpha1.Hardware) (mac, ip string) {
	mac, ip = "N/A", "N/A"
	if len(hw.Spec.Interfaces) > 0 && hw.Spec.Interfaces[0].DHCP != nil {
		mac = hw.Spec.Interfaces[0].DHCP.MAC
		if hw.Spec.Interfaces[0].DHCP.IP != nil {
			ip = hw.Spec.Interfaces[0].DHCP.IP.Address
		}
	}
	return mac, ip
}

func legacyHardwareStatus(hw tinkv1alpha1.Hardware) string {
	if hw.Status.State == "provisioning" {
		return "Provisioning"
	}
	if hw.Status.State == "failed" {
		return "Offline"
	}
	return "Online"
}

// v1alpha1Interface returns a v1alpha1 interface with mac and, when set, ip.
func v1alpha1Interface(mac, ip string, family int64) tinkv1alpha1.Interface {
	iface := tinkv1alpha1.Interface{DHCP: &tinkv1alpha1.DHCP{MAC: mac}}
	if ip != "" {
		iface.DHCP.IP = &tinkv1alpha1.IP{Address: ip, Family: family}
	}
	return iface
}

// v1alpha1HardwareFixtures are Hardware as v1alpha1 clients create them.
func v1alpha1HardwareFixtures() map[string]*tinkv1alpha1.Hardware {
	withInterfaces := func(ifaces ...tinkv1alpha1.Interface) *tinkv1alpha1.Hardware {
		hw := newTestHardwareV1Alpha1("hw", "default", "", "")
		hw.Spec.Interfaces = ifaces
		return hw
	}
	return map[string]*tinkv1alpha1.Hardware{
		"no interfaces":  withInterfaces(),
		"MAC and IPv4":   withInterfaces(v1alpha1Interface("aa:bb:cc:dd:ee:ff", "192.168.1.100", 4)),
		"MAC only":       withInterfaces(v1alpha1Interface("aa:bb:cc:dd:ee:ff", "", 0)),
		"IPv6 address":   withInterfaces(v1alpha1Interface("aa:bb:cc:dd:ee:ff", "2001:db8::10", 6)),
		"upper case MAC": withInterfaces(v1alpha1Interface("AA:BB:CC:DD:EE:FF", "192.168.1.100", 4)),
		"interfaces out of MAC order": withInterfaces(
			v1alpha1Interface("aa:bb:cc:dd:ee:02", "192.168.1.102", 4),
			v1alpha1Interface("aa:bb:cc:dd:ee:01", "192.168.1.101", 4),
			v1alpha1Interface("aa:bb:cc:dd:ee:03", "", 0),
		),
	}
}

// addresses returns the addresses of a v1alpha2 interface the way v1alpha1
// showed its single address.
func addresses(v4, v6 string) string {
	return strings.Join(strings.Fields(v4+" "+v6), " ")
}

func TestGetHardwareInterfacesV1Alpha1Parity(t *testing.T) {
	for name, v1 := range v1alpha1HardwareFixtures() {
		t.Run(name, func(t *testing.T) {
			want := legacyHardwareInterfaces(*v1)
			got := GetHardwareInterfaces(*hardwareV1Alpha2(v1))
			if len(got) != len(want) {
				t.Fatalf("GetHardwareInterfaces() returned %d interfaces, want %d", len(got), len(want))
			}
			for i, iface := range got {
				if iface.MAC != strings.ToLower(want[i][0]) {
					t.Errorf("Interface[%d].MAC = %s, want %s", i, iface.MAC, want[i][0])
				}
				if ip := addresses(iface.IPv4Address, iface.IPv6Address); ip != want[i][1] {
					t.Errorf("Interface[%d] address = %q, want %q", i, ip, want[i][1])
				}
			}
		})
	}
}

func TestHardwareRowV1Alpha1Parity(t *testing.T) {
	for name, v1 := range v1alpha1HardwareFixtures() {
		t.Run(name, func(t *testing.T) {
			wantMAC, wantIP := legacyHardwareRow(*v1)
			row := hardwareRow(hardwareV1Alpha2(v1))
			if wantMAC != notAvailable {
				wantMAC = strings.ToLower(wantMAC)
			}
			if row.MAC != wantMAC {
				t.Errorf("MAC = %q, want %q", row.MAC, wantMAC)
			}
			ip := addresses(row.IPv4Address, row.IPv6Address)
			if ip == "" {
				ip = "N/A"
			}
			if ip != wantIP {
				t.Errorf("IP = %q, want %q", ip, wantIP)
			}
		})
	}
}

func TestGetHardwareStatusV1Alpha1Parity(t *testing.T) {
	for _, state := range []tinkv1alpha1.HardwareState{"provisioning", "failed", "", "something-else"} {
		t.Run(string(state), func(t *testing.T) {
			v1 := newTestHardwareV1Alpha1("hw", "default", "aa:bb:cc:dd:ee:ff", "")
			v1.Status.State = state
			want := legacyHardwareStatus(*v1)
			if got := GetHardwareStatus(*hardwareV1Alpha2(v1)); got != want {
				t.Errorf("GetHardwareStatus() = %s, want %s", got, want)
			}
		})
	}
	if got := GetHardwareStatus(tinkv1alpha2.Hardware{}); got != "Online" {
		t.Errorf("GetHardwareStatus() of v1alpha2 Hardware = %s, want Online", got)
	}
}

func rowValue(rows []templates.InfoRow, name string) string {
	for _, r := range rows {
		if r.Name == name {
			return r.Value
		}
	}
	return ""
}

func ptrTo[T any](v T) *T { return &v }

// newDualStackHardware returns a v1alpha2 Hardware using what only v1alpha2 can
// express: two address families on one interface, DHCPv6 lifetimes, IPv6
// netboot, the BMC connection and conditions.
func newDualStackHardware() *tinkv1alpha2.Hardware {
	return &tinkv1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw-v2", Namespace: "default", CreationTimestamp: metav1.Now()},
		Spec: tinkv1alpha2.HardwareSpec{
			Arch:    "arm64",
			AgentID: "52:54:00:00:00:02",
			NetworkInterfaces: tinkv1alpha2.NetworkInterfaces{
				"52:54:00:00:00:02": {
					IPAM: &tinkv1alpha2.IPAM{
						IPv4: &tinkv1alpha2.IP{Address: "192.168.2.10", Prefix: "24", Gateway: "192.168.2.1", Nameservers: []tinkv1alpha2.Nameserver{"1.1.1.1", "8.8.8.8"}},
						IPv6: &tinkv1alpha2.IP{Address: "2001:db8::10", Prefix: "64", Hostname: ptrTo("node-1")},
					},
					DHCP: &tinkv1alpha2.DHCP{
						IPv4: &tinkv1alpha2.DHCPv4{LeaseTimeSeconds: ptrTo(int64(86400))},
						IPv6: &tinkv1alpha2.DHCPv6{ValidLifetime: ptrTo(int64(3600)), PreferredLifetime: ptrTo(int64(1800))},
					},
					Netboot: &tinkv1alpha2.Netboot{IPv6: &tinkv1alpha2.Netbootv6{BootFileURL: "tftp://[2001:db8::1]/ipxe.efi"}},
				},
				"52:54:00:00:00:01": {Netboot: &tinkv1alpha2.Netboot{Disabled: true}},
			},
			Instance: &tinkv1alpha2.Instance{
				LookupID: "node-1",
				SSHKeys:  []string{"ssh-ed25519 AAAA"},
				Userdata: ptrTo("#cloud-config\n"),
				OSIE:     &tinkv1alpha2.OSIE{KernelURL: "http://osie/vmlinuz", KernelParams: []string{"console=ttyS0"}},
			},
			BMC: &tinkv1alpha2.BMC{
				BootMode: tinkv1alpha2.FirmwareModeUEFI,
				Connection: &bmcv1alpha2.Connection{
					Host:           "10.0.0.5",
					Port:           443,
					CredentialsRef: bmcv1alpha2.SimpleReference{Name: "bmc-creds", Namespace: "default"},
					ProviderOptions: &bmcv1alpha2.ProviderOptions{
						Redfish: &bmcv1alpha2.RedfishOptions{Port: 8443},
					},
				},
			},
			References: map[string]tinkv1alpha2.Reference{
				"lvm": {Resource: "configmaps", Version: "v1", Name: "lvm", Namespace: "default"},
			},
		},
		Status: tinkv1alpha2.HardwareStatus{
			Conditions: []bmcv1alpha2.Condition{
				{Type: bmcv1alpha2.ConditionTypeBMCContactable, Status: bmcv1alpha2.ConditionStatusTrue, Message: "reachable"},
			},
		},
	}
}

func TestGetHardwareInterfacesV1Alpha2(t *testing.T) {
	got := GetHardwareInterfaces(*newDualStackHardware())
	if len(got) != 2 || got[0].MAC != "52:54:00:00:00:01" || got[1].MAC != "52:54:00:00:00:02" {
		t.Fatalf("interfaces = %+v, want both, in MAC order", got)
	}
	if v := rowValue(got[0].Netboot, "Netboot"); v != "Disabled" {
		t.Errorf("first interface Netboot = %q, want Disabled", v)
	}
	iface := got[1]
	if iface.IPv4Address != "192.168.2.10" || iface.IPv6Address != "2001:db8::10" {
		t.Errorf("addresses = %q, %q; want both families", iface.IPv4Address, iface.IPv6Address)
	}
	for _, tc := range []struct {
		rows       []templates.InfoRow
		name, want string
	}{
		{iface.IPv4, "Address", "192.168.2.10/24"},
		{iface.IPv4, "Gateway", "192.168.2.1"},
		{iface.IPv4, "Nameservers", "1.1.1.1, 8.8.8.8"},
		{iface.IPv6, "Address", "2001:db8::10/64"},
		{iface.IPv6, "Hostname", "node-1"},
		{iface.DHCP, "DHCPv4", "Enabled"},
		{iface.DHCP, "Lease Time", "86400s"},
		{iface.DHCP, "DHCPv6", "Enabled"},
		{iface.DHCP, "Valid Lifetime", "3600s"},
		{iface.DHCP, "Preferred Lifetime", "1800s"},
		{iface.Netboot, "Netboot", "Enabled"},
		{iface.Netboot, "Boot File URL (IPv6)", "tftp://[2001:db8::1]/ipxe.efi"},
	} {
		if got := rowValue(tc.rows, tc.name); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHardwareDetailV1Alpha2(t *testing.T) {
	d := hardwareDetail(newDualStackHardware(), "/ui")
	for _, tc := range []struct {
		rows       []templates.InfoRow
		name, want string
	}{
		{d.Spec, "Architecture", "arm64"},
		{d.Spec, "Agent ID", "52:54:00:00:00:02"},
		{d.Instance, "Lookup ID", "node-1"},
		{d.Instance, "SSH Keys", "1 key"},
		{d.Instance, "OSIE Kernel", "http://osie/vmlinuz"},
		{d.Instance, "Kernel Parameters", "console=ttyS0"},
		{d.BMC, "Boot Mode", "UEFI"},
		{d.BMC, "Host", "10.0.0.5"},
		{d.BMC, "Port", "443"},
		{d.BMC, "Credentials", "default/bmc-creds"},
		{d.References, "lvm", "configmaps/v1 default/lvm"},
	} {
		if got := rowValue(tc.rows, tc.name); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
	if len(d.InstanceData) != 1 || d.InstanceData[0].Name != "User Data" {
		t.Errorf("InstanceData = %+v, want the user data", d.InstanceData)
	}
	if !strings.Contains(d.BMCProviderOptions, "port: 8443") {
		t.Errorf("BMCProviderOptions = %q, want the redfish port", d.BMCProviderOptions)
	}
	if len(d.Conditions) != 1 || d.Conditions[0].Type != "Contactable" || d.Conditions[0].Status != "True" {
		t.Errorf("Conditions = %+v, want Contactable True", d.Conditions)
	}
}

// Hardware created through v1alpha1 may name a deprecated bmc Machine; it is
// shown, linked, since v1alpha2 has no field for it.
func TestHardwareDetailBMCMachine(t *testing.T) {
	v1 := newTestHardwareV1Alpha1("hw", "default", "aa:bb:cc:dd:ee:ff", "")
	v1.Spec.BMCRef = &corev1.TypedLocalObjectReference{Kind: "Machine", Name: "bmc-1"}
	d := hardwareDetail(hardwareV1Alpha2(v1), "/ui")
	for _, r := range d.BMC {
		if r.Name == "BMC Machine (deprecated)" {
			if r.Value != "default/bmc-1" || r.Link != "/ui/bmc/machines/default/bmc-1" {
				t.Errorf("BMC Machine row = %+v", r)
			}
			return
		}
	}
	t.Errorf("BMC rows %+v have no BMC Machine", d.BMC)
}

func TestHandleHardwareDetailV1Alpha2(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newDualStackHardware())
	c, w := setupTestContext("/hardware/default/hw-v2", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "hw-v2"}}

	HandleHardwareDetail(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{"192.168.2.10/24", "2001:db8::10/64", "Valid Lifetime", "10.0.0.5", "Conditions", "Contactable", "User Data"} {
		if !strings.Contains(body, want) {
			t.Errorf("response should contain %q", want)
		}
	}
}

func TestHandleHardwareListV1Alpha2(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newDualStackHardware())
	c, w := setupTestContext("/hardware", kubeClient)

	HandleHardwareList(c, testLog)

	body := w.Body.String()
	// The first interface in MAC order has no addresses.
	if !strings.Contains(body, "52:54:00:00:00:01") || !strings.Contains(body, "N/A") {
		t.Errorf("response should list the first interface, without addresses")
	}
}
