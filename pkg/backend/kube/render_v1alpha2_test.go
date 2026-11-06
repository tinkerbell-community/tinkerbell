package kube

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/inventory"
	"github.com/tinkerbell/tinkerbell/pkg/template/render"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const testMAC = "00:11:22:33:44:55"

func TestRenderHardwareV1Alpha2(t *testing.T) {
	const tmpl = "{{ .references.net.domain }}"
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "machine1",
			Namespace:   "tink",
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": tmpl},
		},
		Spec: v1alpha2.HardwareSpec{
			AgentID:    tmpl,
			References: map[string]v1alpha2.Reference{"net": {Name: tmpl}},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{testMAC: {IPAM: &v1alpha2.IPAM{
				IPv4: &v1alpha2.IP{Address: tmpl, Gateway: "{{ .references.net.gateway }}", Hostname: ptr("{{ .hardware.metadata.name }}")},
				IPv6: &v1alpha2.IP{Address: tmpl},
			}}},
			Instance: &v1alpha2.Instance{
				LookupID: tmpl,
				Metadata: ptr("{{ .hardware.metadata.name }}." + tmpl),
				Userdata: ptr(`fqdn: {{ .hardware.spec.instance.metadata }} {{ (index .hardware.spec.networkInterfaces "` + testMAC + `").ipam.ipv4.gateway }}`),
			},
		},
		Status: v1alpha2.HardwareStatus{Attributes: &inventory.HardwareAttributes{InBand: &inventory.Attributes{CollectionMethod: tmpl}}},
	}
	orig := hw.DeepCopy()
	refs := map[string]any{"net": map[string]any{"domain": "example.org", "gateway": "10.0.0.1"}}

	got, asV1Alpha1, err := renderHardwareV1Alpha2(hw, refs)
	if err != nil {
		t.Fatal(err)
	}
	if asV1Alpha1 != nil {
		t.Fatal("Hardware created as v1alpha2 has no v1alpha1 rendering")
	}
	want := orig.DeepCopy()
	want.Spec.NetworkInterfaces[testMAC].IPAM.IPv4.Gateway = "10.0.0.1"
	want.Spec.NetworkInterfaces[testMAC].IPAM.IPv4.Hostname = ptr("machine1")
	want.Spec.Instance.Metadata = ptr("machine1.example.org")
	want.Spec.Instance.Userdata = ptr("fqdn: machine1.example.org 10.0.0.1")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("rendered (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(orig, hw); diff != "" {
		t.Errorf("input was modified (-want +got):\n%s", diff)
	}
}

func TestRenderHardwareV1Alpha2NothingToRender(t *testing.T) {
	hw := &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{
		Instance:   &v1alpha2.Instance{Userdata: ptr("#cloud-config")},
		References: map[string]v1alpha2.Reference{"net": {Name: "net1"}},
	}}
	got, asV1Alpha1, err := renderHardwareV1Alpha2(hw, nil)
	if err != nil || got != hw || asV1Alpha1 != nil {
		t.Fatalf("render = %v, %v, %v; want the stored object unchanged", got, asV1Alpha1, err)
	}
}

func TestRenderHardwareV1Alpha2Error(t *testing.T) {
	hw := &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{Instance: &v1alpha2.Instance{Userdata: ptr("{{ .references.missing.x }}")}}}
	_, _, err := renderHardwareV1Alpha2(hw, map[string]any{})
	var fe *render.FieldError
	if !errors.As(err, &fe) || fe.Path != "spec.instance.userdata" {
		t.Fatalf("err = %v, want *render.FieldError at spec.instance.userdata", err)
	}
}

func TestRenderHardwareV1Alpha2SkipAnnotation(t *testing.T) {
	const jinja = "## template: jinja\n#cloud-config\nhostname: {{ ds.meta_data.hostname }}\n"
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Annotations: map[string]string{templateSkipAnnotation: `["spec.instance.userdata"]`}},
		Spec:       v1alpha2.HardwareSpec{Instance: &v1alpha2.Instance{Userdata: ptr(jinja), Vendordata: ptr("{{ .hardware.metadata.name }}")}},
	}
	got, _, err := renderHardwareV1Alpha2(hw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if *got.Spec.Instance.Userdata != jinja || *got.Spec.Instance.Vendordata != "machine1" {
		t.Fatalf("instance = %+v, want userdata skipped and vendordata rendered", got.Spec.Instance)
	}
	hw.Annotations[templateSkipAnnotation] = `["spec.userData"]`
	if _, _, err := renderHardwareV1Alpha2(hw, nil); err == nil {
		t.Fatal("v1alpha2 Hardware must name v1alpha2 paths in the skip annotation")
	}
	hw.Annotations[templateSkipAnnotation] = "null"
	if _, _, err := renderHardwareV1Alpha2(hw, nil); err == nil || !strings.Contains(err.Error(), templateSkipAnnotation) {
		t.Fatalf("err = %v, want annotation validation error", err)
	}
}

func TestRenderHardwareV1Alpha2OnlySkippedFields(t *testing.T) {
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "{{ invalid }}"},
		Spec: v1alpha2.HardwareSpec{
			AgentID:    "{{ invalid }}",
			References: map[string]v1alpha2.Reference{"net.name": {Name: "{{ invalid }}"}},
			Instance:   &v1alpha2.Instance{LookupID: "{{ invalid }}"},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{testMAC: {IPAM: &v1alpha2.IPAM{
				IPv4: &v1alpha2.IP{Address: "{{ invalid }}"},
				IPv6: &v1alpha2.IP{Address: "{{ invalid }}"},
			}}},
		},
		Status: v1alpha2.HardwareStatus{Conditions: nil, Attributes: &inventory.HardwareAttributes{InBand: &inventory.Attributes{CollectionMethod: "{{ invalid }}"}}},
	}
	if got, _, err := renderHardwareV1Alpha2(hw, nil); err != nil || got != hw {
		t.Fatalf("render = %v, %v; want unchanged hardware", got, err)
	}
}

func TestRenderHardwareV1Alpha2ProtectedMutation(t *testing.T) {
	iface := `(index .hardware.spec.networkInterfaces "` + testMAC + `")`
	for _, test := range []struct {
		name     string
		template string
		path     string
	}{
		{name: "metadata", template: `{{ $_ := set .hardware.metadata "name" "other" }}`, path: "metadata"},
		{name: "IPv4", template: `{{ $_ := set ` + iface + `.ipam.ipv4 "address" "other" }}`, path: `spec.networkInterfaces["` + testMAC + `"].ipam.ipv4.address`},
		{name: "IPv6", template: `{{ $_ := unset ` + iface + `.ipam.ipv6 "address" }}`, path: `spec.networkInterfaces["` + testMAC + `"].ipam.ipv6.address`},
		{name: "IPv6 numeric", template: `{{ $_ := set ` + iface + `.ipam.ipv6 "address" 42 }}`, path: `spec.networkInterfaces["` + testMAC + `"].ipam.ipv6.address`},
		{name: "MAC added", template: `{{ $_ := set .hardware.spec.networkInterfaces "66:77:88:99:aa:bb" (dict) }}`, path: `spec.networkInterfaces["66:77:88:99:aa:bb"]`},
		{name: "MAC removed", template: `{{ $_ := unset .hardware.spec.networkInterfaces "` + testMAC + `" }}`, path: `spec.networkInterfaces["` + testMAC + `"]`},
		{name: "references", template: `{{ $_ := unset .hardware.spec.references "net" }}`, path: "spec.references"},
		{name: "status", template: `{{ $_ := set .hardware.status "attributes" (dict) }}`, path: "status"},
		{name: "lookup ID", template: `{{ $_ := set .hardware.spec.instance "lookupID" "other" }}`, path: "spec.instance.lookupID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			hw := &v1alpha2.Hardware{
				ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink"},
				Spec: v1alpha2.HardwareSpec{
					Instance:   &v1alpha2.Instance{LookupID: "machine1"},
					References: map[string]v1alpha2.Reference{"net": {Name: "network"}},
					NetworkInterfaces: v1alpha2.NetworkInterfaces{testMAC: {IPAM: &v1alpha2.IPAM{
						IPv4: &v1alpha2.IP{Address: "10.0.0.1"},
						IPv6: &v1alpha2.IP{Address: "2001:db8::1"},
					}}},
					// Outside the objects the templates change, so that they do not read themselves.
					StorageDevices: []v1alpha2.StorageDevice{{Name: test.template}},
				},
			}
			original := hw.DeepCopy()
			got, _, err := renderHardwareV1Alpha2(hw, nil)
			if got != nil || err == nil || !strings.Contains(err.Error(), fmt.Sprintf("protected field %q changed", test.path)) {
				t.Fatalf("render = %v, %v; want protected mutation error at %s", got, err, test.path)
			}
			if diff := cmp.Diff(original, hw); diff != "" {
				t.Errorf("input was modified (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateProtectedHardwareV1Alpha2(t *testing.T) {
	hw := &v1alpha2.Hardware{
		TypeMeta:   metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha2", Kind: "Hardware"},
		ObjectMeta: metav1.ObjectMeta{Name: "machine1"},
		Spec: v1alpha2.HardwareSpec{
			AgentID:    "agent1",
			Instance:   &v1alpha2.Instance{LookupID: "lookup1", Userdata: ptr("literal")},
			References: map[string]v1alpha2.Reference{"net": {Name: "network"}},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{testMAC: {IPAM: &v1alpha2.IPAM{
				IPv4: &v1alpha2.IP{Address: "10.0.0.1"},
				IPv6: &v1alpha2.IP{Address: "2001:db8::1"},
			}}},
		},
	}
	doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hw)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		path   []string
		value  any
		remove bool
		want   string
	}{
		{name: "agentID changed", path: []string{"spec", "agentID"}, value: "other", want: "spec.agentID"},
		{name: "lookupID removed", path: []string{"spec", "instance", "lookupID"}, remove: true, want: "spec.instance.lookupID"},
		{name: "instance replaced", path: []string{"spec", "instance"}, value: "other", want: "spec.instance.lookupID"},
		{name: "interfaces removed", path: []string{"spec", "networkInterfaces"}, remove: true, want: `spec.networkInterfaces["` + testMAC + `"]`},
		{name: "interfaces replaced", path: []string{"spec", "networkInterfaces"}, value: "other", want: "spec.networkInterfaces"},
		{name: "IPAM replaced", path: []string{"spec", "networkInterfaces", testMAC, "ipam"}, value: "other", want: `spec.networkInterfaces["` + testMAC + `"].ipam.ipv4.address`},
		{name: "interface emptied", path: []string{"spec", "networkInterfaces", testMAC}, value: map[string]any{}, want: `spec.networkInterfaces["` + testMAC + `"].ipam.ipv4.address`},
		{name: "unprotected field", path: []string{"spec", "instance", "userdata"}, value: "rendered"},
		{name: "unprotected interface field", path: []string{"spec", "networkInterfaces", testMAC, "ipam", "ipv4", "gateway"}, value: "rendered"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := runtime.DeepCopyJSON(doc)
			after := runtime.DeepCopyJSON(doc)
			if test.remove {
				unstructured.RemoveNestedField(after, test.path...)
			} else if err := unstructured.SetNestedField(after, test.value, test.path...); err != nil {
				t.Fatal(err)
			}
			err := validateProtected(before, after, protectedHardwarePathsV1Alpha2)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != fmt.Sprintf("protected field %q changed", test.want) {
				t.Fatalf("err = %v, want protected field %q changed", err, test.want)
			}
		})
	}
}

func TestRenderHardwareV1Alpha2UnprotectedInterfaceField(t *testing.T) {
	hw := &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{
		Instance: &v1alpha2.Instance{Userdata: ptr(`{{ $_ := set (index .hardware.spec.networkInterfaces "` + testMAC + `").ipam.ipv4 "gateway" "10.0.0.254" }}`)},
		NetworkInterfaces: v1alpha2.NetworkInterfaces{testMAC: {IPAM: &v1alpha2.IPAM{
			IPv4: &v1alpha2.IP{Address: "10.0.0.1"},
		}}},
	}}
	got, _, err := renderHardwareV1Alpha2(hw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gateway := got.Spec.NetworkInterfaces[testMAC].IPAM.IPv4.Gateway; gateway != "10.0.0.254" {
		t.Fatalf("gateway = %q; fields next to the address must stay writable", gateway)
	}
}

func TestSkipRenderV1Alpha2(t *testing.T) {
	quoted := `spec.networkInterfaces["` + testMAC + `"]`
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "apiVersion", want: true},
		{path: "metadata.name", want: true},
		{path: "status.attributes.inBand.collectionMethod", want: true},
		{path: "spec.references.net.name", want: true},
		{path: "spec.agentID", want: true},
		{path: "spec.instance.lookupID", want: true},
		{path: "spec.instance.lookupIDOther"},
		{path: "spec.instance.userdata"},
		{path: quoted + ".ipam.ipv4.address", want: true},
		{path: quoted + ".ipam.ipv6.address", want: true},
		{path: quoted + ".ipam.ipv4.hostname"},
		{path: quoted + ".ipam.ipv4.addressOther"},
		{path: quoted},
		{path: "spec.networkInterfaces.eth0.ipam.ipv4.address", want: true},
		{path: "spec.networkInterfaces.ipam.ipv4.address"},
		{path: "spec.networkInterfaces[0].ipam.ipv4.address"},
		{path: `spec.networkInterfacesOther["` + testMAC + `"].ipam.ipv4.address`},
		{path: `spec.networkInterfaces["` + testMAC + `.ipam.ipv4.address`},
		{path: "spec.interfaces[0].dhcp.mac"},
	} {
		t.Run(test.path, func(t *testing.T) {
			if got := skipPath(test.path, skippedHardwarePathsV1Alpha2); got != test.want {
				t.Fatalf("skip(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

func TestMatchesSkippedPathMapWildcard(t *testing.T) {
	for _, test := range []struct {
		path string
		skip string
		want bool
	}{
		{path: "a.x.b", skip: "a.*.b", want: true},
		{path: `a["x.y"].b`, skip: "a.*.b", want: true},
		{path: `a["x"].b.c`, skip: "a.*.b", want: true},
		{path: `a["x"].b["c"]`, skip: "a.*.b", want: true},
		{path: "a.x.bOther", skip: "a.*.b"},
		{path: "a.x.c", skip: "a.*.b"},
		{path: "a.x", skip: "a.*.b"},
		{path: "a.b", skip: "a.*.b"},
		{path: "a[0].b", skip: "a.*.b"},
		{path: "aOther.x.b", skip: "a.*.b"},
		{path: `a["x"]b`, skip: "a.*.b"},
		{path: "a.x.b[0].c", skip: "a.*.b[].c", want: true},
		{path: `a[1].b["k"].c`, skip: "a[].b.*.c", want: true},
		{path: `a[1].b["k"].d`, skip: "a[].b.*.c"},
	} {
		t.Run(test.path+"/"+test.skip, func(t *testing.T) {
			if got := matchesSkippedPath(test.path, test.skip); got != test.want {
				t.Fatalf("matchesSkippedPath(%q, %q) = %v, want %v", test.path, test.skip, got, test.want)
			}
		})
	}
}

func TestChangedProtectedPathMaps(t *testing.T) {
	before := map[string]any{"groups": map[string]any{
		"k": map[string]any{"mac": "original", "hostname": "literal"},
	}}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "unchanged", mutate: func(map[string]any) {}},
		{name: "value changed", mutate: func(after map[string]any) {
			after["groups"].(map[string]any)["k"].(map[string]any)["mac"] = "changed"
		}, want: `groups["k"].mac`},
		{name: "key added", mutate: func(after map[string]any) {
			after["groups"].(map[string]any)["new"] = map[string]any{}
		}, want: `groups["new"]`},
		{name: "key removed", mutate: func(after map[string]any) {
			delete(after["groups"].(map[string]any), "k")
		}, want: `groups["k"]`},
		{name: "map removed", mutate: func(after map[string]any) { delete(after, "groups") }, want: `groups["k"]`},
		{name: "map replaced", mutate: func(after map[string]any) { after["groups"] = "changed" }, want: "groups"},
		{name: "value replaced", mutate: func(after map[string]any) {
			after["groups"].(map[string]any)["k"] = "changed"
		}, want: `groups["k"]`},
		{name: "unprotected sibling", mutate: func(after map[string]any) {
			after["groups"].(map[string]any)["k"].(map[string]any)["hostname"] = "changed"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := runtime.DeepCopyJSON(before)
			test.mutate(after)
			if got := changedProtectedPath(before, after, "groups.*.mac"); got != test.want {
				t.Fatalf("changed path = %q, want %q", got, test.want)
			}
		})
	}
	if got := changedProtectedPath(map[string]any{}, map[string]any{}, "groups.*.mac"); got != "" {
		t.Fatalf("changed path = %q for an absent map, want none", got)
	}
}

func TestCutMapKey(t *testing.T) {
	for _, test := range []struct {
		path, rest string
		ok         bool
	}{
		{path: ".key.rest", rest: ".rest", ok: true},
		{path: ".key[0]", rest: "[0]", ok: true},
		{path: ".key", rest: "", ok: true},
		{path: `["a.b"].rest`, rest: ".rest", ok: true},
		{path: `["a\"]"].rest`, rest: ".rest", ok: true},
		{path: "..rest"},
		{path: "."},
		{path: "[0].rest"},
		{path: `["unterminated`},
		{path: `["key"`},
		{path: "key"},
	} {
		t.Run(test.path, func(t *testing.T) {
			rest, ok := cutMapKey(test.path)
			if rest != test.rest || ok != test.ok {
				t.Fatalf("cutMapKey(%q) = %q, %v; want %q, %v", test.path, rest, ok, test.rest, test.ok)
			}
		})
	}
}

// v1alpha1Machine is a v1alpha1 Hardware whose templates use the v1alpha1 shape,
// including a field only v1alpha1 has.
func v1alpha1Machine() *v1alpha1.Hardware {
	return &v1alpha1.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink", UID: "uid1", ResourceVersion: "1"},
		Spec: v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{DHCP: &v1alpha1.DHCP{
				MAC: testMAC, Hostname: "host1", IP: &v1alpha1.IP{Address: "10.0.0.2", Netmask: "255.255.255.0"},
			}}},
			Metadata: &v1alpha1.HardwareMetadata{Facility: &v1alpha1.MetadataFacility{FacilityCode: "{{ .references.net.domain }}"}},
			UserData: ptr(`{{ (index .hardware.spec.interfaces 0).dhcp.hostname }}.{{ .references.net.domain }}`),
		},
	}
}

// TestRenderHardwareCreatedAsV1Alpha1 renders a Hardware created through
// v1alpha1 exactly as v1alpha1 rendering did.
func TestRenderHardwareCreatedAsV1Alpha1(t *testing.T) {
	refs := map[string]any{"net": map[string]any{"domain": "example.org"}}
	v1 := v1alpha1Machine()
	direct, err := renderHardware(v1, refs)
	if err != nil {
		t.Fatal(err)
	}
	stored := createdAsV1Alpha1(t, v1)
	orig := stored.DeepCopy()

	got, asV1Alpha1, err := renderHardwareV1Alpha2(stored, refs)
	if err != nil {
		t.Fatal(err)
	}
	if asV1Alpha1 == nil {
		t.Fatal("Hardware created through v1alpha1 must keep its v1alpha1 rendering")
	}
	if diff := cmp.Diff(direct.Spec, asV1Alpha1.Spec); diff != "" {
		t.Errorf("v1alpha1 rendering differs from rendering v1alpha1 directly (-want +got):\n%s", diff)
	}
	if *got.Spec.Instance.Userdata != "host1.example.org" {
		t.Fatalf("userdata = %q, want the v1alpha1 template rendered", *got.Spec.Instance.Userdata)
	}
	if diff := cmp.Diff(direct.Spec, servedAsV1Alpha1(t, got).Spec); diff != "" {
		t.Errorf("v1alpha2 rendering read as v1alpha1 (-want +got):\n%s", diff)
	}
	if got.UID != stored.UID || got.ResourceVersion != stored.ResourceVersion || !rendersAsV1Alpha1(got) {
		t.Fatalf("rendering metadata = %+v, want the stored identity and the v1alpha1 form kept", got.ObjectMeta)
	}
	if diff := cmp.Diff(orig, stored); diff != "" {
		t.Errorf("input was modified (-want +got):\n%s", diff)
	}
}

// TestRenderHardwareVersionDecision renders each Hardware with the shape its
// templates were written for, and only that one.
func TestRenderHardwareVersionDecision(t *testing.T) {
	refs := map[string]any{"net": map[string]any{"domain": "example.org"}}
	v1Template := `{{ (index .hardware.spec.interfaces 0).dhcp.hostname }}`
	v2Template := `{{ (index .hardware.spec.networkInterfaces "` + testMAC + `").ipam.ipv4.hostname }}`

	native := createdAsV1Alpha1(t, v1alpha1Machine())
	delete(native.Annotations, conversion.AnnotationV1Alpha1)
	native.Spec.Instance.Userdata = ptr(v2Template)
	if got, _, err := renderHardwareV1Alpha2(native, refs); err != nil || *got.Spec.Instance.Userdata != "host1" {
		t.Fatalf("native render = %v, %v; want the v1alpha2 template rendered", got, err)
	}
	native.Spec.Instance.Userdata = ptr(v1Template)
	if _, _, err := renderHardwareV1Alpha2(native, refs); err == nil {
		t.Fatal("Hardware created as v1alpha2 must not render v1alpha1 templates")
	}

	compat := v1alpha1Machine()
	compat.Spec.UserData = ptr(v2Template)
	if _, _, err := renderHardwareV1Alpha2(createdAsV1Alpha1(t, compat), refs); err == nil {
		t.Fatal("Hardware created through v1alpha1 must not render v1alpha2 templates")
	}

	skipped := v1alpha1Machine()
	skipped.Spec.Metadata = nil
	skipped.Annotations = map[string]string{templateSkipAnnotation: `["spec.userData"]`}
	skipped.Spec.UserData = ptr("{{ ds.meta_data.hostname }}")
	stored := createdAsV1Alpha1(t, skipped)
	if got, _, err := renderHardwareV1Alpha2(stored, refs); err != nil || got != stored {
		t.Fatalf("render = %v, %v; want v1alpha1 skip paths to apply to Hardware created through v1alpha1", got, err)
	}
}

// TestRenderHardwareCreatedAsV1Alpha1KeepsV1Alpha2Fields checks that the fields
// only v1alpha2 has survive a v1alpha1 rendering, unrendered.
func TestRenderHardwareCreatedAsV1Alpha1KeepsV1Alpha2Fields(t *testing.T) {
	stored := createdAsV1Alpha1(t, v1alpha1Machine())
	stored.Spec.Instance.Vars = map[string]string{"name": "{{ .hardware.metadata.name }}"}
	got, _, err := renderHardwareV1Alpha2(stored, map[string]any{"net": map[string]any{"domain": "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Instance.Vars["name"] != "{{ .hardware.metadata.name }}" || *got.Spec.Instance.Userdata != "host1.example.org" {
		t.Fatalf("instance = %+v, want v1alpha2-only vars kept as written and userdata rendered", got.Spec.Instance)
	}
}

func TestNeedsRenderingV1Alpha2(t *testing.T) {
	onlyV1Alpha1Field := v1alpha1Machine()
	onlyV1Alpha1Field.Spec.UserData = nil
	literal := v1alpha1Machine()
	literal.Spec.UserData, literal.Spec.Metadata = nil, nil
	invalid := createdAsV1Alpha1(t, literal)
	invalid.Annotations[conversion.AnnotationV1Alpha1] = "not JSON"
	badSkip := &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{templateSkipAnnotation: "null"}}}
	native := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"note": "{{ not rendered }}"}},
		Spec:       v1alpha2.HardwareSpec{Instance: &v1alpha2.Instance{LookupID: "{{ skipped }}"}},
	}
	tests := map[string]struct {
		hw   *v1alpha2.Hardware
		want bool
	}{
		"template in a field only v1alpha1 has": {hw: createdAsV1Alpha1(t, onlyV1Alpha1Field), want: true},
		"v1alpha1 without templates":            {hw: createdAsV1Alpha1(t, literal)},
		"unreadable v1alpha1 form":              {hw: invalid, want: true},
		"invalid skip annotation":               {hw: badSkip, want: true},
		"templates only in skipped paths":       {hw: native},
		"native template":                       {hw: templated("1"), want: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := needsRenderingV1Alpha2(tt.hw); got != tt.want {
				t.Fatalf("needsRenderingV1Alpha2 = %v, want %v", got, tt.want)
			}
		})
	}
}
