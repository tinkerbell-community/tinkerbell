package controller_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	tinkerbell "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/rufio/internal/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func hardwareRequest(hw *v1alpha2.Hardware) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hw)}
}

func getHardware(t *testing.T, c client.Client, hw *v1alpha2.Hardware) *v1alpha2.Hardware {
	t.Helper()
	got := &v1alpha2.Hardware{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(hw), got); err != nil {
		t.Fatalf("get Hardware: %v", err)
	}
	return got
}

func machineConnection(t *testing.T, m *bmc.Machine) *bmcv2.Connection {
	t.Helper()
	conn, err := conversion.MachineConnectionToV1Alpha2(m.Spec.Connection)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// v1alpha1Update changes a stored Hardware the way an update through v1alpha1
// does: converted to v1alpha1, changed, and converted back.
func v1alpha1Update(t *testing.T, hw *v1alpha2.Hardware, change func(*tinkerbell.Hardware)) *v1alpha2.Hardware {
	t.Helper()
	v1 := &tinkerbell.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, v1); err != nil {
		t.Fatal(err)
	}
	change(v1)
	out := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(v1, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMachineTranslation(t *testing.T) {
	ctx := context.Background()
	machine := createMachine()
	hw := createHardwareForMachine(machine.Name)
	original := &tinkerbell.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, original); err != nil {
		t.Fatal(err)
	}
	cl := newClientBuilder().WithObjects(hw, machine).Build()
	r := controller.NewMachineTranslationReconciler(cl)

	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got := getHardware(t, cl, hw)
	if got.Spec.BMC == nil || got.Spec.BMC.Connection == nil {
		t.Fatalf("spec.bmc = %+v, want the Machine's connection", got.Spec.BMC)
	}
	if diff := cmp.Diff(machineConnection(t, machine), got.Spec.BMC.Connection); diff != "" {
		t.Errorf("connection (-want +got):\n%s", diff)
	}
	if a := got.Annotations[conversion.AnnotationMachine]; a != "test-namespace/test-bm" {
		t.Errorf("annotation %s = %q, want test-namespace/test-bm", conversion.AnnotationMachine, a)
	}

	// v1alpha1 clients see the Hardware they created.
	v1 := &tinkerbell.Hardware{}
	if err := conversion.HardwareToV1Alpha1(got, v1); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(original.Spec, v1.Spec); diff != "" {
		t.Errorf("v1alpha1 spec after translation (-want +got):\n%s", diff)
	}

	// The connection follows the Machine while it is the translated one.
	machine = &bmc.Machine{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "test-namespace", Name: "test-bm"}, machine); err != nil {
		t.Fatal(err)
	}
	machine.Spec.Connection.Host = "10.0.0.9"
	if err := cl.Update(ctx, machine); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got = getHardware(t, cl, hw)
	if h := got.Spec.BMC.Connection.Host; h != "10.0.0.9" {
		t.Errorf("host after the Machine changed = %q, want 10.0.0.9", h)
	}

	// Once taken over (the annotation removed), it no longer does.
	delete(got.Annotations, conversion.AnnotationMachine)
	if err := cl.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	machine.Spec.Connection.Host = "10.0.0.10"
	if err := cl.Update(ctx, machine); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got = getHardware(t, cl, hw)
	if h := got.Spec.BMC.Connection.Host; h != "10.0.0.9" {
		t.Errorf("host of a connection that is no longer translated = %q, want it left at 10.0.0.9", h)
	}
}

func TestMachineTranslationCases(t *testing.T) {
	machine := createMachine()
	other := createMachine()
	other.Name = "other-bm"
	other.Spec.Connection.Host = "10.0.0.20"
	userConnection := newConnection("10.0.0.1")

	// translated is a Hardware whose connection was translated from machine.
	translated := func(t *testing.T) *v1alpha2.Hardware {
		t.Helper()
		hw := createHardwareForMachine(machine.Name)
		hw.Spec.BMC = &v1alpha2.BMC{Connection: machineConnection(t, machine)}
		hw.Annotations[conversion.AnnotationMachine] = "test-namespace/test-bm"
		return hw
	}

	tests := map[string]struct {
		hardware       func(t *testing.T) *v1alpha2.Hardware
		wantConnection *bmcv2.Connection
		wantAnnotation string
	}{
		"a connection a user set is never overwritten": {
			hardware: func(*testing.T) *v1alpha2.Hardware {
				hw := createHardwareForMachine(machine.Name)
				hw.Spec.BMC = &v1alpha2.BMC{Connection: userConnection}
				return hw
			},
			wantConnection: userConnection,
		},
		"a Machine that does not exist is not translated": {
			hardware: func(*testing.T) *v1alpha2.Hardware {
				return createHardwareForMachine("no-such-machine")
			},
		},
		"a bmcRef that is not a Machine is not translated": {
			hardware: func(t *testing.T) *v1alpha2.Hardware {
				t.Helper()
				return v1alpha1Update(t, createHardwareForMachine(machine.Name), func(h *tinkerbell.Hardware) {
					h.Spec.BMCRef = &corev1.TypedLocalObjectReference{Kind: "Something", Name: machine.Name}
				})
			},
		},
		"Hardware without a bmcRef is left alone": {
			hardware: func(*testing.T) *v1alpha2.Hardware {
				return &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "test-hw", Namespace: "test-namespace"}}
			},
		},
		"a bmcRef changed through v1alpha1 to another Machine translates that one": {
			hardware: func(t *testing.T) *v1alpha2.Hardware {
				t.Helper()
				return v1alpha1Update(t, translated(t), func(h *tinkerbell.Hardware) { h.Spec.BMCRef.Name = other.Name })
			},
			wantConnection: machineConnection(t, other),
			wantAnnotation: "test-namespace/other-bm",
		},
		"a bmcRef removed through v1alpha1 removes the translated connection": {
			hardware: func(t *testing.T) *v1alpha2.Hardware {
				t.Helper()
				return v1alpha1Update(t, translated(t), func(h *tinkerbell.Hardware) { h.Spec.BMCRef = nil })
			},
		},
		"an up to date translation is left alone": {
			hardware:       translated,
			wantConnection: machineConnection(t, machine),
			wantAnnotation: "test-namespace/test-bm",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			hw := tt.hardware(t)
			cl := newClientBuilder().WithObjects(hw, machine, other).Build()
			r := controller.NewMachineTranslationReconciler(cl)

			if _, err := r.Reconcile(context.Background(), hardwareRequest(hw)); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			got := getHardware(t, cl, hw)
			var conn *bmcv2.Connection
			if got.Spec.BMC != nil {
				conn = got.Spec.BMC.Connection
			}
			if diff := cmp.Diff(tt.wantConnection, conn); diff != "" {
				t.Errorf("connection (-want +got):\n%s", diff)
			}
			if a := got.Annotations[conversion.AnnotationMachine]; a != tt.wantAnnotation {
				t.Errorf("annotation %s = %q, want %q", conversion.AnnotationMachine, a, tt.wantAnnotation)
			}
		})
	}
}
