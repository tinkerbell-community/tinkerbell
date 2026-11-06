package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	common "github.com/bmc-toolbox/common"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/inventory"
	"github.com/tinkerbell/tinkerbell/rufio/internal/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// hardwareWithConnection is a v1alpha2 Hardware with a BMC connection of its own.
func hardwareWithConnection() *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha2.GroupVersion.String(), Kind: "Hardware"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-hw", Namespace: "test-namespace"},
		Spec:       v1alpha2.HardwareSpec{BMC: &v1alpha2.BMC{Connection: newConnection("10.0.0.1")}},
	}
}

// translatedHardware is a Hardware whose connection was translated from the createMachine Machine.
func translatedHardware(t *testing.T) *v1alpha2.Hardware {
	t.Helper()
	hw := createHardwareForMachine("test-bm")
	hw.Spec.BMC = &v1alpha2.BMC{Connection: machineConnection(t, createMachine())}
	hw.Annotations[conversion.AnnotationMachine] = "test-namespace/test-bm"
	return hw
}

func conditionsWithoutTimes(conds []bmcv2.Condition) []bmcv2.Condition {
	out := make([]bmcv2.Condition, 0, len(conds))
	for _, c := range conds {
		c.LastUpdateTime = metav1.Time{}
		c.ObservedGeneration = 0
		out = append(out, c)
	}
	return out
}

func TestHardwarePollsItsOwnConnection(t *testing.T) {
	ctx := context.Background()
	hw := hardwareWithConnection()
	cl := newInventoryTestClient(hw, createSecret())
	provider := &testProvider{
		Powerstate:      "on",
		InventoryDevice: &common.Device{BIOS: &common.BIOS{Common: common.Common{Vendor: "Dell Inc."}}},
	}
	r := controller.NewHardwareReconciler(cl, events.NewFakeRecorder(4), newTestClient(provider), time.Minute, 0, true)

	result, err := r.Reconcile(ctx, hardwareRequest(hw))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("Reconcile() result = %+v, want a requeue after the power check interval", result)
	}
	got := getHardware(t, cl, hw)
	want := []bmcv2.Condition{
		{Type: bmcv2.ConditionTypeBMCContactable, Status: bmcv2.ConditionStatusTrue},
		{Type: bmcv2.ConditionTypeBMCPowerState, Status: bmcv2.ConditionStatusOn},
	}
	if diff := cmp.Diff(want, conditionsWithoutTimes(got.Status.Conditions)); diff != "" {
		t.Errorf("conditions (-want +got):\n%s", diff)
	}
	if inv := controller.OutOfBandAttributes(got); inv == nil || inv.BIOS == nil || inv.BIOS.Vendor != "Dell Inc." {
		t.Errorf("status.attributes.outOfBand = %+v, want the collected inventory", inv)
	}

	// Polling again: same state, so the conditions keep their times, and the
	// inventory is not due.
	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	again := getHardware(t, cl, hw)
	if diff := cmp.Diff(got.Status.Conditions, again.Status.Conditions); diff != "" {
		t.Errorf("conditions changed while the BMC state did not (-before +after):\n%s", diff)
	}
	calls, opens := provider.recorded()
	if diff := cmp.Diff([]string{"PowerStateGet", "Inventory", "PowerStateGet"}, calls); diff != "" {
		t.Errorf("BMC calls (-want +got):\n%s", diff)
	}
	if opens != 2 {
		t.Errorf("opened %d BMC sessions, want one per poll", opens)
	}

	// Powered off.
	provider.setPowerState("off")
	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got = getHardware(t, cl, hw)
	if !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCPowerState, bmcv2.ConditionStatusOff) {
		t.Errorf("conditions = %v, want PowerState Off", conditionSummary(got.Status.Conditions))
	}
}

func TestHardwareUnreachableBMC(t *testing.T) {
	hw := hardwareWithConnection()
	cl := newInventoryTestClient(hw, createSecret())
	provider := &testProvider{ErrOpen: errors.New("unreachable")}
	r := controller.NewHardwareReconciler(cl, events.NewFakeRecorder(4), newTestClient(provider), time.Minute, 0, true)

	result, err := r.Reconcile(context.Background(), hardwareRequest(hw))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("Reconcile() result = %+v, want a requeue after the power check interval", result)
	}
	got := getHardware(t, cl, hw)
	contactable := condition(got.Status.Conditions, bmcv2.ConditionTypeBMCContactable)
	if contactable == nil || contactable.Status != bmcv2.ConditionStatusFalse || contactable.Message == "" {
		t.Errorf("Contactable = %+v, want False with the error", contactable)
	}
	if !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCPowerState, bmcv2.ConditionStatusUnknown) {
		t.Errorf("conditions = %v, want PowerState Unknown", conditionSummary(got.Status.Conditions))
	}
}

// TestHardwareTranslatedFromMachine checks that the BMC of a Hardware whose
// connection was translated from a Machine is not polled a second time: the
// Machine controller polls the Machine, and the Hardware shows its state.
func TestHardwareTranslatedFromMachine(t *testing.T) {
	ctx := context.Background()
	machine := createMachine()
	changed := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	machine.Status = bmc.MachineStatus{
		Power:      bmc.Off,
		Conditions: []bmc.MachineCondition{{Type: bmc.Contactable, Status: bmc.ConditionTrue, LastUpdateTime: changed, Message: "ok"}},
	}
	hw := translatedHardware(t)
	cl := newInventoryTestClient(hw, machine, createSecret())
	provider := &testProvider{Powerstate: "on"}
	r := controller.NewHardwareReconciler(cl, events.NewFakeRecorder(4), newTestClient(provider), time.Minute, 0, true)

	result, err := r.Reconcile(ctx, hardwareRequest(hw))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.IsZero() {
		t.Errorf("Reconcile() result = %+v, want no requeue: Machine changes trigger it", result)
	}
	if calls, opens := provider.recorded(); len(calls) != 0 || opens != 0 {
		t.Errorf("BMC calls = %v in %d sessions, want none", calls, opens)
	}
	got := getHardware(t, cl, hw)
	contactable := condition(got.Status.Conditions, bmcv2.ConditionTypeBMCContactable)
	if contactable == nil || contactable.Status != bmcv2.ConditionStatusTrue || contactable.Message != "ok" || !contactable.LastUpdateTime.Equal(&changed) {
		t.Errorf("Contactable = %+v, want the Machine's", contactable)
	}
	if !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCPowerState, bmcv2.ConditionStatusOff) {
		t.Errorf("conditions = %v, want PowerState Off, as the Machine", conditionSummary(got.Status.Conditions))
	}

	// The Machine is gone.
	if err := cl.Delete(ctx, machine); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	got = getHardware(t, cl, hw)
	if !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCContactable, bmcv2.ConditionStatusUnknown) {
		t.Errorf("conditions = %v, want Contactable Unknown", conditionSummary(got.Status.Conditions))
	}
}

func TestHardwareWithoutBMCDropsConditions(t *testing.T) {
	hw := hardwareWithConnection()
	hw.Spec.BMC = nil
	hw.Status.Conditions = []bmcv2.Condition{
		{Type: bmcv2.ConditionTypeBMCContactable, Status: bmcv2.ConditionStatusTrue},
		{Type: bmcv2.ConditionTypeBMCPowerState, Status: bmcv2.ConditionStatusOn},
	}
	cl := newInventoryTestClient(hw)
	provider := &testProvider{}
	r := controller.NewHardwareReconciler(cl, events.NewFakeRecorder(4), newTestClient(provider), time.Minute, 0, true)

	if _, err := r.Reconcile(context.Background(), hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := getHardware(t, cl, hw); len(got.Status.Conditions) != 0 {
		t.Errorf("conditions = %v, want none", conditionSummary(got.Status.Conditions))
	}
	if _, opens := provider.recorded(); opens != 0 {
		t.Errorf("opened %d BMC sessions, want none", opens)
	}
}

func TestHardwareRefreshInventoryAnnotation(t *testing.T) {
	ctx := context.Background()
	hw := hardwareWithConnection()
	fresh := metav1.Now()
	hw.Annotations = map[string]string{"tinkerbell.org/refresh-inventory": trueString}
	hw.Status.Attributes = &inventory.HardwareAttributes{OutOfBand: &inventory.Attributes{LastUpdated: &fresh}}
	cl := newInventoryTestClient(hw, createSecret())
	provider := &testProvider{Powerstate: "on", InventoryDevice: &common.Device{BIOS: &common.BIOS{Common: common.Common{Vendor: "Dell Inc."}}}}
	r := controller.NewHardwareReconciler(cl, events.NewFakeRecorder(4), newTestClient(provider), time.Minute, 0, true)

	if _, err := r.Reconcile(ctx, hardwareRequest(hw)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if provider.InventoryCalls != 1 {
		t.Errorf("Inventory() called %d times, want 1: the annotation forces a refresh", provider.InventoryCalls)
	}
	got := getHardware(t, cl, hw)
	if _, ok := got.Annotations["tinkerbell.org/refresh-inventory"]; ok {
		t.Error("refresh-inventory annotation still present after a successful collection")
	}
	if len(got.Status.Conditions) != 2 {
		t.Errorf("conditions = %v, want Contactable and PowerState", conditionSummary(got.Status.Conditions))
	}
}

// TestMachineSkipsHardwareWithOwnConnection checks that the Machine controller
// does not collect inventory for a Hardware that refers to it but has a BMC
// connection of its own: the Hardware controller collects it from that BMC.
func TestMachineSkipsHardwareWithOwnConnection(t *testing.T) {
	bm := createMachine()
	hw := createHardwareForMachine(bm.Name)
	hw.Spec.BMC = &v1alpha2.BMC{Connection: newConnection("10.0.0.1")}
	provider := &testProvider{InventoryDevice: &common.Device{BIOS: &common.BIOS{Common: common.Common{Vendor: "Dell Inc."}}}}
	cl := newInventoryTestClient(hw)
	r := controller.NewMachineReconciler(cl, events.NewFakeRecorder(2), newTestClient(provider), 0, 0, true)

	r.ReconcileInventoryIfDueForTest(context.Background(), logr.Discard(), newOpenTestBMCClient(t, provider), bm)

	if provider.InventoryCalls != 0 {
		t.Errorf("Inventory() called %d times, want 0", provider.InventoryCalls)
	}
}

func TestHardwareBMCChangedPredicate(t *testing.T) {
	base := hardwareWithConnection()
	tests := map[string]struct {
		change func(*v1alpha2.Hardware)
		want   bool
	}{
		"status only": {
			change: func(h *v1alpha2.Hardware) {
				h.Status.Conditions = []bmcv2.Condition{{Type: bmcv2.ConditionTypeBMCPowerState, Status: bmcv2.ConditionStatusOn}}
			},
		},
		"another part of the spec": {
			change: func(h *v1alpha2.Hardware) { h.Spec.AgentID = "52:54:00:00:00:01" },
		},
		"another annotation": {
			change: func(h *v1alpha2.Hardware) { h.Annotations = map[string]string{"example.com/x": "y"} },
		},
		"the BMC connection": {
			change: func(h *v1alpha2.Hardware) { h.Spec.BMC.Connection.Host = "10.0.0.2" },
			want:   true,
		},
		"the translated Machine": {
			change: func(h *v1alpha2.Hardware) {
				h.Annotations = map[string]string{conversion.AnnotationMachine: "test-namespace/test-bm"}
			},
			want: true,
		},
		"a requested inventory refresh": {
			change: func(h *v1alpha2.Hardware) {
				h.Annotations = map[string]string{"tinkerbell.org/refresh-inventory": trueString}
			},
			want: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			newer := base.DeepCopy()
			tt.change(newer)
			if got := controller.HardwareBMCChanged.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: newer}); got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
	if !controller.HardwareBMCChanged.Create(event.CreateEvent{Object: base}) {
		t.Error("Create() = false, want true")
	}
}
