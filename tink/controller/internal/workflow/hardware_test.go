package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/google/go-cmp/cmp"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// conflictingClient wraps a fake client to simulate conflict errors on patch.
type conflictingClient struct {
	client.Client
	conflictCount              int
	maxConflicts               int
	resourceVersionTestFailure bool
}

func (c *conflictingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.conflictCount < c.maxConflicts {
		c.conflictCount++
		if c.resourceVersionTestFailure {
			return apierrors.NewBadRequest("json patch test failed: /metadata/resourceVersion")
		}
		return apierrors.NewConflict(
			schema.GroupResource{Group: "tinkerbell.org", Resource: "hardware"},
			obj.GetName(),
			nil,
		)
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

// errorClient wraps a fake client to simulate non-conflict errors on patch.
type errorClient struct {
	client.Client
}

func (c *errorClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return apierrors.NewInternalError(fmt.Errorf("simulated patch error"))
}

func withDuration(duration func() time.Duration) backoffOpts {
	return func(cfg *backoffConfig) {
		cfg.duration = duration
	}
}

const (
	mac1 = v1alpha2.MAC("3c:ec:ef:4c:4f:54")
	mac2 = v1alpha2.MAC("3c:ec:ef:4c:4f:55")
)

func netbootHardware(interfaces v1alpha2.NetworkInterfaces) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hardware", Namespace: "default"},
		Spec:       v1alpha2.HardwareSpec{NetworkInterfaces: interfaces},
	}
}

func TestSetNetboot(t *testing.T) {
	key := client.ObjectKey{Name: "test-hardware", Namespace: "default"}
	tests := map[string]struct {
		hardware     *v1alpha2.Hardware
		allow        bool
		patchErr     bool
		expectError  bool
		wantNetboots map[v1alpha2.MAC]*v1alpha2.Netboot
	}{
		"allow netboot on every interface": {
			hardware: netbootHardware(v1alpha2.NetworkInterfaces{
				mac1: {Netboot: &v1alpha2.Netboot{Disabled: true, IPXE: &v1alpha2.IPXE{URL: "http://example.com/auto.ipxe"}}},
				mac2: {},
			}),
			allow: true,
			wantNetboots: map[v1alpha2.MAC]*v1alpha2.Netboot{
				mac1: {IPXE: &v1alpha2.IPXE{URL: "http://example.com/auto.ipxe"}},
				mac2: {},
			},
		},
		"disallow netboot on every interface": {
			hardware: netbootHardware(v1alpha2.NetworkInterfaces{
				mac1: {Netboot: &v1alpha2.Netboot{}},
				mac2: {},
			}),
			allow: false,
			wantNetboots: map[v1alpha2.MAC]*v1alpha2.Netboot{
				mac1: {Disabled: true},
				mac2: {Disabled: true},
			},
		},
		"no interfaces": {
			hardware:     netbootHardware(nil),
			allow:        true,
			wantNetboots: map[v1alpha2.MAC]*v1alpha2.Netboot{},
		},
		"hardware not found": {
			allow:       true,
			expectError: true,
		},
		"update error that is not conflict": {
			hardware:    netbootHardware(v1alpha2.NetworkInterfaces{mac1: {}}),
			allow:       true,
			patchErr:    true,
			expectError: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(runtimescheme)
			if tc.hardware != nil {
				builder = builder.WithObjects(tc.hardware)
			}
			fakeClient := builder.Build()
			var testClient client.Client = fakeClient
			if tc.patchErr {
				testClient = &errorClient{Client: fakeClient}
			}

			err := setNetboot(context.Background(), testClient, key, nil, tc.allow, withDuration(func() time.Duration { return 0 }))
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := &v1alpha2.Hardware{}
			if err := fakeClient.Get(context.Background(), key, got); err != nil {
				t.Fatal(err)
			}
			netboots := map[v1alpha2.MAC]*v1alpha2.Netboot{}
			for mac, ni := range got.Spec.NetworkInterfaces {
				netboots[mac] = ni.Netboot
			}
			if diff := cmp.Diff(tc.wantNetboots, netboots); diff != "" {
				t.Errorf("unexpected netboot (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSetNetbootV1Alpha1 shows that Hardware created through v1alpha1 reads back through v1alpha1 with
// allowPXE toggled on every interface, as the v1alpha1 engine did.
func TestSetNetbootV1Alpha1(t *testing.T) {
	hw := &v1alpha1.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hardware", Namespace: "default"},
		Spec: v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{
				{DHCP: &v1alpha1.DHCP{MAC: string(mac1), UEFI: true}, Netboot: &v1alpha1.Netboot{AllowPXE: valueToPointer(false), AllowWorkflow: valueToPointer(true)}},
				{DHCP: &v1alpha1.DHCP{MAC: string(mac2)}},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(runtimescheme).WithObjects(v2(t, hw)).Build()
	key := client.ObjectKeyFromObject(hw)
	for _, allow := range []bool{true, false} {
		if err := setNetboot(context.Background(), c, key, nil, allow); err != nil {
			t.Fatal(err)
		}
		got := readV1Hardware(t, c, key)
		for i, iface := range got.Spec.Interfaces {
			// v1alpha1 netboots an interface only when allowPXE is true; no netboot at all is the same as false.
			allowed := iface.Netboot != nil && iface.Netboot.AllowPXE != nil && *iface.Netboot.AllowPXE
			if allowed != allow {
				t.Errorf("allow %v: interface %d netboot = %+v, want allowPXE %v", allow, i, iface.Netboot, allow)
			}
		}
		if !got.Spec.Interfaces[0].DHCP.UEFI || !*got.Spec.Interfaces[0].Netboot.AllowWorkflow {
			t.Errorf("allow %v: v1alpha1 only fields not kept: %+v", allow, got.Spec.Interfaces[0])
		}
	}
}

func TestSetNetbootRetryMechanism(t *testing.T) {
	tests := map[string]struct {
		maxConflicts               int
		resourceVersionTestFailure bool
		expectError                bool
		expectedRetries            int
	}{
		"success after 1 conflict": {
			maxConflicts:    1,
			expectError:     false,
			expectedRetries: 1,
		},
		"retry after resourceVersion test failure": {
			maxConflicts:               1,
			resourceVersionTestFailure: true,
			expectError:                false,
			expectedRetries:            1,
		},
		"success after 2 conflicts": {
			maxConflicts:    2,
			expectError:     false,
			expectedRetries: 2,
		},
		"success after 3 conflicts": {
			maxConflicts:    3,
			expectError:     false,
			expectedRetries: 3,
		},
		"failure after 4 conflicts (exceeds retry limit)": {
			maxConflicts:    4,
			expectError:     true,
			expectedRetries: 4,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			hardware := netbootHardware(v1alpha2.NetworkInterfaces{mac1: {Netboot: &v1alpha2.Netboot{Disabled: true}}})
			baseClient := fake.NewClientBuilder().WithScheme(runtimescheme).WithObjects(hardware).Build()
			conflictClient := &conflictingClient{
				Client:                     baseClient,
				maxConflicts:               tc.maxConflicts,
				resourceVersionTestFailure: tc.resourceVersionTestFailure,
			}

			err := setNetboot(context.Background(), conflictClient, client.ObjectKeyFromObject(hardware), nil, true, withDuration(func() time.Duration { return 0 }))
			if conflictClient.conflictCount != tc.expectedRetries {
				t.Errorf("expected %d conflicts, got %d", tc.expectedRetries, conflictClient.conflictCount)
			}
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			updated := &v1alpha2.Hardware{}
			if err := baseClient.Get(context.Background(), client.ObjectKeyFromObject(hardware), updated); err != nil {
				t.Fatalf("failed to get updated hardware: %v", err)
			}
			if nb := updated.Spec.NetworkInterfaces[mac1].Netboot; nb == nil || nb.Disabled {
				t.Errorf("netboot = %+v, want it allowed after a successful retry", nb)
			}
		})
	}
}

// TestSetNetbootJSONPatchPreservesUnknownInterfaceFields guards against replacing whole interfaces.
func TestSetNetbootJSONPatchPreservesUnknownInterfaceFields(t *testing.T) {
	hw := netbootHardware(v1alpha2.NetworkInterfaces{
		mac1: {Netboot: &v1alpha2.Netboot{Disabled: true}},
		mac2: {},
	})
	hw.Spec.Instance = &v1alpha2.Instance{Userdata: valueToPointer("#cloud-config")}
	cc := interceptor.NewClient(fake.NewClientBuilder().WithScheme(runtimescheme).WithObjects(hw).Build(), interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if patch.Type() != types.JSONPatchType {
				return fmt.Errorf("patch type = %q, want JSON Patch", patch.Type())
			}
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			var body []netbootJSONPatchOperation
			if err := json.Unmarshal(data, &body); err != nil {
				return err
			}

			wantOperations := []netbootJSONPatchOperation{
				{Op: "test", Path: "/metadata/resourceVersion", Value: obj.GetResourceVersion()},
				{Op: "add", Path: "/spec/networkInterfaces/" + string(mac1) + "/netboot/disabled", Value: false},
				{Op: "add", Path: "/spec/networkInterfaces/" + string(mac2) + "/netboot", Value: map[string]any{"disabled": false}},
			}
			if diff := cmp.Diff(wantOperations, body); diff != "" {
				return fmt.Errorf("unexpected patch operations (-want +got):\n%s", diff)
			}

			original, err := json.Marshal(map[string]any{
				"metadata": map[string]any{"resourceVersion": obj.GetResourceVersion()},
				"spec": map[string]any{
					"networkInterfaces": map[string]any{
						string(mac1): map[string]any{
							"futureInterfaceField": "preserve interface extension",
							"netboot": map[string]any{
								"disabled":           true,
								"futureNetbootField": "preserve netboot extension",
							},
						},
						string(mac2): map[string]any{"futureInterfaceField": "preserve interface without netboot"},
					},
				},
			})
			if err != nil {
				return err
			}
			decodedPatch, err := jsonpatch.DecodePatch(data)
			if err != nil {
				return err
			}
			updated, err := decodedPatch.Apply(original)
			if err != nil {
				return err
			}
			var updatedObject map[string]any
			if err := json.Unmarshal(updated, &updatedObject); err != nil {
				return err
			}
			interfaces := updatedObject["spec"].(map[string]any)["networkInterfaces"].(map[string]any)
			first := interfaces[string(mac1)].(map[string]any)
			if first["futureInterfaceField"] != "preserve interface extension" || first["netboot"].(map[string]any)["futureNetbootField"] != "preserve netboot extension" {
				return fmt.Errorf("patch removed an unknown field from an interface: %v", first)
			}
			second := interfaces[string(mac2)].(map[string]any)
			if second["futureInterfaceField"] != "preserve interface without netboot" {
				return fmt.Errorf("patch removed an unknown field from an interface without netboot: %v", second)
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	})

	if err := setNetboot(context.Background(), cc, client.ObjectKeyFromObject(hw), hw.DeepCopy(), true); err != nil {
		t.Fatal(err)
	}
}

func TestHardwareFromOtherNamespace(t *testing.T) {
	wf := &v1alpha2.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "default"}}
	c := fake.NewClientBuilder().WithScheme(runtimescheme).WithObjects(&v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "hw", Namespace: "other"}}).Build()
	if _, err := hardwareFrom(context.Background(), c, wf, v1alpha2.SimpleReference{Name: "hw", Namespace: "other"}); err == nil {
		t.Fatal("a Workflow must not act on Hardware in another namespace")
	}
}
