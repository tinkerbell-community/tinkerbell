package kube

import (
	"context"
	"errors"
	"slices"
	"testing"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func hardwareV1Alpha2(name, mac, ipv4, ipv6 string) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tink"},
		Spec: v1alpha2.HardwareSpec{
			AgentID:  name + "-agent",
			Instance: &v1alpha2.Instance{LookupID: name + "-lookup"},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{
				v1alpha2.MAC(mac): {IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: ipv4}, IPv6: &v1alpha2.IP{Address: ipv6}}},
			},
		},
	}
}

func TestFilterHardware(t *testing.T) {
	b := newFakeBackend(t, Backend{},
		hardwareV1Alpha2("machine1", "3c:ec:ef:4c:4f:54", "172.16.10.100", "2001:db8::100"),
		hardwareV1Alpha2("machine2", "3c:ec:ef:4c:4f:55", "172.16.10.101", "2001:db8::101"),
		templatedV1Alpha1(),
	)
	tests := map[string]struct {
		filter       data.HardwareFilter
		want         string
		wantNotFound bool
		wantMultiple bool
	}{
		"MAC":                      {filter: data.HardwareFilter{ByMACAddress: "3c:ec:ef:4c:4f:54"}, want: "machine1"},
		"MAC in upper case":        {filter: data.HardwareFilter{ByMACAddress: "3C:EC:EF:4C:4F:55"}, want: "machine2"},
		"IPv4":                     {filter: data.HardwareFilter{ByIPAddress: "172.16.10.101"}, want: "machine2"},
		"IPv6 written differently": {filter: data.HardwareFilter{ByIPAddress: "2001:DB8:0:0::100"}, want: "machine1"},
		"name":                     {filter: data.HardwareFilter{ByName: "machine2", InNamespace: "tink"}, want: "machine2"},
		"agent ID":                 {filter: data.HardwareFilter{ByAgentID: "machine1-agent"}, want: "machine1"},
		"instance ID is lookup ID": {filter: data.HardwareFilter{ByInstanceID: "machine2-lookup"}, want: "machine2"},
		"several filters":          {filter: data.HardwareFilter{ByName: "machine1", ByMACAddress: "3c:ec:ef:4c:4f:54"}, want: "machine1"},
		"filters disagree":         {filter: data.HardwareFilter{ByName: "machine1", ByMACAddress: "3c:ec:ef:4c:4f:55"}, wantNotFound: true},
		"other namespace":          {filter: data.HardwareFilter{ByName: "machine1", InNamespace: "other"}, wantNotFound: true},
		"v1alpha1 is not read":     {filter: data.HardwareFilter{ByName: "m1"}, wantNotFound: true},
		"several match":            {filter: data.HardwareFilter{InNamespace: "tink"}, wantMultiple: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := b.FilterHardware(context.Background(), tt.filter)
			switch {
			case tt.wantNotFound:
				if !apierrors.IsNotFound(err) {
					t.Fatalf("err = %v, want not found", err)
				}
			case tt.wantMultiple:
				var multiple interface{ MultipleFound() bool }
				if !errors.As(err, &multiple) || !multiple.MultipleFound() {
					t.Fatalf("err = %v, want multiple found", err)
				}
			case err != nil:
				t.Fatal(err)
			case got.Name != tt.want:
				t.Fatalf("got %s, want %s", got.Name, tt.want)
			}
		})
	}
}

func TestListHardware(t *testing.T) {
	b := newFakeBackend(t, Backend{},
		hardwareV1Alpha2("machine1", "3c:ec:ef:4c:4f:54", "172.16.10.100", ""),
		hardwareV1Alpha2("machine2", "3c:ec:ef:4c:4f:55", "172.16.10.101", ""),
	)
	all, err := b.ListHardware(context.Background(), data.HardwareFilter{InNamespace: "tink"})
	if err != nil || len(all) != 2 {
		t.Fatalf("listed %d Hardware, %v; want 2", len(all), err)
	}
	one, err := b.ListHardware(context.Background(), data.HardwareFilter{ByIPAddress: "172.16.10.101"})
	if err != nil || len(one) != 1 || one[0].Name != "machine2" {
		t.Fatalf("listed %v, %v; want machine2", one, err)
	}
}

func TestReadHardware(t *testing.T) {
	hw := hardwareV1Alpha2("machine1", "3c:ec:ef:4c:4f:54", "172.16.10.100", "")
	tests := map[string]struct {
		namespaces   []string
		name         string
		wantNotFound bool
	}{
		"found":                         {name: "machine1"},
		"inside the backend namespace":  {namespaces: []string{"other", "tink"}, name: "machine1"},
		"missing":                       {name: "machine2", wantNotFound: true},
		"outside the backend namespace": {namespaces: []string{"other"}, name: "machine1", wantNotFound: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := newFakeBackend(t, Backend{}, hw.DeepCopy())
			b.Namespaces = tt.namespaces
			got, err := b.ReadHardware(context.Background(), tt.name, "tink")
			if tt.wantNotFound {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("err = %v, want not found", err)
				}
				return
			}
			if err != nil || got.Spec.AgentID != "machine1-agent" {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestCreateAndUpdateHardware(t *testing.T) {
	ctx := context.Background()
	b := newFakeBackend(t, Backend{})
	hw := hardwareV1Alpha2("machine1", "3c:ec:ef:4c:4f:54", "172.16.10.100", "")
	if err := b.CreateHardware(ctx, hw); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateHardware(ctx, hardwareV1Alpha2("machine1", "", "", "")); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("err = %v, want already exists", err)
	}
	read := func() *v1alpha2.Hardware {
		t.Helper()
		got, err := b.ReadHardware(ctx, "machine1", "tink")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	updated := read()
	updated.Spec.Arch = "arm64"
	if err := b.UpdateHardware(ctx, updated, data.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	original := read()
	patched := original.DeepCopy()
	patched.Spec.AgentID = "patched"
	if err := b.UpdateHardware(ctx, patched, data.UpdateOptions{PatchFrom: original}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Spec.Arch != "arm64" || got.Spec.AgentID != "patched" {
		t.Fatalf("spec = %+v, want the update and the patch", got.Spec)
	}

	status := read()
	status.Status.Conditions = append(status.Status.Conditions, bmcv1alpha2.Condition{Type: "Ready", Status: "True"})
	status.Spec.Arch = "ignored"
	if err := b.UpdateHardware(ctx, status, data.UpdateOptions{StatusOnly: true}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"spec":{"arch":"x86_64"}}`)
	if err := b.UpdateHardware(ctx, read(), data.UpdateOptions{RawPatch: raw, RawPatchType: string(types.MergePatchType)}); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Spec.Arch != "x86_64" || !slices.ContainsFunc(got.Status.Conditions, func(c bmcv1alpha2.Condition) bool { return c.Type == "Ready" }) {
		t.Fatalf("got spec %+v status %+v; want the status update without its spec change, then the raw patch", got.Spec, got.Status)
	}
	if err := b.UpdateHardware(ctx, got, data.UpdateOptions{RawPatch: raw}); err == nil {
		t.Fatal("want an error for a raw patch without a type")
	}
	missing := hardwareV1Alpha2("missing", "", "", "")
	if err := b.UpdateHardware(ctx, missing, data.UpdateOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
	if err := b.UpdateHardware(ctx, missing, data.UpdateOptions{StatusOnly: true}); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
	if err := b.UpdateHardware(ctx, missing, data.UpdateOptions{StatusOnly: true, PatchFrom: missing.DeepCopy()}); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
	if err := b.UpdateHardware(ctx, missing, data.UpdateOptions{PatchFrom: missing.DeepCopy()}); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
	if err := b.UpdateHardware(ctx, missing, data.UpdateOptions{PatchFrom: "not an object"}); err == nil {
		t.Fatal("want an error for invalid patch options")
	}
}
