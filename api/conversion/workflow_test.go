package conversion

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/randfill"
)

func v1Workflow() *v1alpha1.Workflow {
	start := metav1.Unix(1000, 0)
	return &v1alpha1.Workflow{
		TypeMeta:   metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha1", Kind: "Workflow"},
		ObjectMeta: metav1.ObjectMeta{Name: "wf1", Namespace: "tink"},
		Spec: v1alpha1.WorkflowSpec{
			TemplateRef: "tpl1",
			HardwareRef: "hw1",
			HardwareMap: map[string]string{"device_1": "52:54:00:00:00:01"},
			BootOptions: v1alpha1.BootOptions{
				ToggleAllowNetboot: true,
				BootMode:           v1alpha1.BootModeISO, // deprecated alias of isoboot
				ISOURL:             "http://iso.example.com/hook.iso",
				CustombootConfig: v1alpha1.CustombootConfig{
					PreparingActions: []bmcv1.Action{{OneTimeBootDeviceAction: &bmcv1.OneTimeBootDeviceAction{Devices: []bmcv1.BootDevice{"pxe"}, EFIBoot: true}}},
					PostActions:      []bmcv1.Action{{PowerAction: ptr(bmcv1.PowerHardOff)}},
				},
			},
		},
		Status: v1alpha1.WorkflowStatus{
			AgentID: "52:54:00:00:00:01",
			State:   v1alpha1.WorkflowStateRunning,
			BootOptions: v1alpha1.BootOptionsStatus{
				AllowNetboot: v1alpha1.AllowNetbootStatus{ToggledTrue: true},
				Jobs:         map[string]v1alpha1.JobStatus{"netboot": {UID: "u1", Complete: true}},
			},
			TemplateRendering:   v1alpha1.TemplateRenderingSuccessful,
			GlobalTimeout:       1800,
			GlobalExecutionStop: &start,
			CurrentState: &v1alpha1.CurrentState{
				AgentID: "52:54:00:00:00:01", TaskID: "t1", TaskName: "install", ActionID: "a2", ActionName: "write", State: v1alpha1.WorkflowStateRunning,
			},
			Tasks: []v1alpha1.Task{{
				ID: "t1", Name: "install", AgentID: "52:54:00:00:00:01",
				Volumes: []string{"/dev:/dev"}, Environment: map[string]string{"A": "1"},
				Actions: []v1alpha1.Action{
					{
						ID: "a1", Name: "stream", Image: "quay.io/x/image2disk", Timeout: 600, Command: []string{"sh", "-c", "true"},
						Namespaces: &v1alpha1.ActionNamespaces{PID: "host"}, State: v1alpha1.WorkflowStateSuccess,
						ExecutionStart: &start, ExecutionStop: &start, ExecutionDuration: "1s", Message: "done",
					},
					{ID: "a2", Name: "write", Image: "quay.io/x/writefile", State: v1alpha1.WorkflowStateRunning},
				},
			}},
			Conditions: []v1alpha1.WorkflowCondition{{Type: v1alpha1.TemplateRenderedSuccess, Status: metav1.ConditionTrue, Reason: "Complete", Time: &start}},
		},
	}
}

func wfToV2(t *testing.T, in *v1alpha1.Workflow) *v1alpha2.Workflow {
	t.Helper()
	out := &v1alpha2.Workflow{}
	if err := WorkflowToV1Alpha2(apiRoundTrip(t, in), out); err != nil {
		t.Fatal(err)
	}
	return apiRoundTrip(t, out)
}

func wfToV1(t *testing.T, in *v1alpha2.Workflow) *v1alpha1.Workflow {
	t.Helper()
	out := &v1alpha1.Workflow{}
	if err := WorkflowToV1Alpha1(apiRoundTrip(t, in), out); err != nil {
		t.Fatal(err)
	}
	return apiRoundTrip(t, out)
}

func TestWorkflowToV1Alpha2Mapping(t *testing.T) {
	got := wfToV2(t, v1Workflow())
	if got.Annotations[AnnotationTemplate] != "tpl1" {
		t.Errorf("template annotation: %v", got.Annotations)
	}
	if got.Spec.Globals == nil || got.Spec.Globals.TemplateMap["device_1"] != "52:54:00:00:00:01" {
		t.Errorf("hardwareMap -> globals.templateMap: %+v", got.Spec.Globals)
	}
	if len(got.Spec.Tasks) != 1 || got.Spec.Tasks[0].Hardware.HardwareRef.Name != "hw1" || got.Spec.Tasks[0].Hardware.HardwareRef.Namespace != "tink" {
		t.Fatalf("hardware binding: %+v", got.Spec.Tasks)
	}
	bo := got.Spec.Tasks[0].Hardware.BootOptions
	if bo.BootMode != v1alpha2.BootModeIsoboot || !bo.ToggleNetboot || bo.Customboot.PreOperations[0].BootDevice.Name != "pxe" || bo.Customboot.PreOperations[0].BootDevice.Persistent {
		t.Errorf("boot options: %+v", bo)
	}
	st := got.Status
	if st.Metadata.Workflow.State != v1alpha2.StateRunning || st.Metadata.Action.Name != "write" || st.Metadata.Task.AgentID != "52:54:00:00:00:01" {
		t.Errorf("metadata: %+v", st.Metadata)
	}
	a := st.RenderedTasks[0].Actions[0]
	if a.Command != "sh" || len(a.Args) != 2 || a.Namespaces.PID != "host" || deref(a.TimeoutSeconds) != 600 || a.Metadata.Message != "done" {
		t.Errorf("action: %+v", a)
	}
}

func TestWorkflowRoundTripFromV1Alpha1(t *testing.T) {
	orig := v1Workflow()
	back := wfToV1(t, wfToV2(t, orig))
	delete(back.Annotations, AnnotationV1Alpha2)
	if d := cmp.Diff(apiRoundTrip(t, orig), back, cmpOpts...); d != "" {
		t.Errorf("round trip changed the Workflow (-want +got):\n%s", d)
	}
}

// The v1alpha1 Workflow engine keeps writing v1alpha1 status until it moves to
// v1alpha2, so a v1alpha1 status must survive v1alpha2 storage on its own.
func TestWorkflowStatusRoundTrip(t *testing.T) {
	orig := v1Workflow().Status
	back := workflowStatusToV1(workflowStatusToV2(orig))
	if d := cmp.Diff(orig, back, cmpOpts...); d != "" {
		t.Errorf("status round trip (-want +got):\n%s", d)
	}
}

func TestWorkflowRoundTripFromV1Alpha2(t *testing.T) {
	orig := &v1alpha2.Workflow{
		TypeMeta:   metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha2", Kind: "Workflow"},
		ObjectMeta: metav1.ObjectMeta{Name: "wf2", Namespace: "tink"},
		Spec: v1alpha2.WorkflowSpec{
			Globals:        &v1alpha2.Extra{Env: map[string]string{"X": "1"}},
			TimeoutSeconds: ptr[int64](3600),
			Tasks: []v1alpha2.WorkflowTask{
				{
					AgentID: "a1", TaskRef: v1alpha2.SimpleReference{Name: "install", Namespace: "tink"},
					Hardware: &v1alpha2.WorkflowHardware{
						HardwareRef: &v1alpha2.SimpleReference{Name: "hw2", Namespace: "tink"},
						BootOptions: &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeCustomboot, Customboot: &v1alpha2.Customboot{
							PreOperations: []bmcv2.Operations{{PowerAction: ptr(bmcv2.PowerActionCycle), TimeoutSeconds: 30}},
						}},
					},
				},
				{AgentID: "a1", TaskRef: v1alpha2.SimpleReference{Name: "reboot", Namespace: "tink"}},
			},
		},
	}
	spoke := wfToV1(t, orig)
	if spoke.Spec.HardwareRef != "hw2" || spoke.Spec.TemplateRef != "" || spoke.Spec.BootOptions.BootMode != "customboot" {
		t.Errorf("v1alpha1 view: %+v", spoke.Spec)
	}
	back := wfToV2(t, spoke)
	delete(back.Annotations, AnnotationV1Alpha1)
	if d := cmp.Diff(apiRoundTrip(t, orig), back, cmpOpts...); d != "" {
		t.Errorf("round trip changed the Workflow (-want +got):\n%s", d)
	}

	// A v1alpha1 client disables it: the v1alpha2 tasks survive.
	spoke.Spec.Disabled = ptr(true)
	changed := wfToV2(t, spoke)
	if len(changed.Spec.Tasks) != 2 || changed.Spec.Tasks[0].TaskRef.Name != "install" || !deref(changed.Spec.Disabled) || changed.Spec.Globals.Env["X"] != "1" {
		t.Errorf("v1alpha2 spec lost after a v1alpha1 change: %+v", changed.Spec)
	}
}

func TestWorkflowRestoreAfterV1Alpha2Change(t *testing.T) {
	hub := wfToV2(t, v1Workflow())
	hub.Spec.Disabled = ptr(true)
	got := wfToV1(t, hub)
	want := v1Workflow().Spec
	want.Disabled = ptr(true)
	if d := cmp.Diff(want, got.Spec, cmpOpts...); d != "" {
		t.Errorf("v1alpha1 spec after a v1alpha2 change (-want +got):\n%s", d)
	}
}

func TestJobRoundTrips(t *testing.T) {
	v1 := &bmcv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "bmc.tinkerbell.org/v1alpha1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: "j1", Namespace: "tink"},
		Spec: bmcv1.JobSpec{
			MachineRef: bmcv1.MachineRef{Name: "m1", Namespace: "tink"},
			Tasks: []bmcv1.Action{
				{PowerAction: ptr(bmcv1.PowerHardOff), BootDevice: &bmcv1.BootDeviceConfig{Device: "pxe", EFIBoot: true}},
				{VirtualMediaAction: &bmcv1.VirtualMediaAction{MediaURL: "http://x/y.iso", Kind: "CD"}},
				{NetworkBootConfig: &bmcv1.NetworkBootConfig{HTTPBootEnabled: ptr(true)}},
			},
		},
		Status: bmcv1.JobStatus{Conditions: []bmcv1.JobCondition{{Type: bmcv1.JobCompleted, Status: bmcv1.ConditionTrue}}},
	}
	hub := &bmcv2.Job{}
	if err := JobToV1Alpha2(apiRoundTrip(t, v1), hub); err != nil {
		t.Fatal(err)
	}
	if hub.Annotations[AnnotationMachine] != "tink/m1" || len(hub.Spec.Operations) != 4 || hub.Spec.Operations[1].BootDevice.Name != "pxe" {
		t.Errorf("v1alpha2 Job: %+v %+v", hub.Annotations, hub.Spec.Operations)
	}
	back := &bmcv1.Job{}
	if err := JobToV1Alpha1(apiRoundTrip(t, hub), back); err != nil {
		t.Fatal(err)
	}
	delete(back.Annotations, AnnotationV1Alpha2)
	if d := cmp.Diff(apiRoundTrip(t, v1), apiRoundTrip(t, back), cmpOpts...); d != "" {
		t.Errorf("v1alpha1 Job round trip (-want +got):\n%s", d)
	}

	v2 := &bmcv2.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "bmc.tinkerbell.org/v1alpha2", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: "j2", Namespace: "tink"},
		Spec: bmcv2.JobSpec{
			HardwareRef: &bmcv2.SimpleReference{Name: "hw2", Namespace: "tink"},
			Operations:  []bmcv2.Operations{{PowerAction: ptr(bmcv2.PowerActionOn), TimeoutSeconds: 60}},
		},
		Status: bmcv2.JobStatus{CompletedOperations: 1},
	}
	spoke := &bmcv1.Job{}
	if err := JobToV1Alpha1(apiRoundTrip(t, v2), spoke); err != nil {
		t.Fatal(err)
	}
	again := &bmcv2.Job{}
	if err := JobToV1Alpha2(apiRoundTrip(t, spoke), again); err != nil {
		t.Fatal(err)
	}
	delete(again.Annotations, AnnotationV1Alpha1)
	want := apiRoundTrip(t, v2)
	want.Status.CompletedOperations = 0 // status v1alpha1 has no place for; only Rufio writes it, in v1alpha2
	if d := cmp.Diff(want, apiRoundTrip(t, again), cmpOpts...); d != "" {
		t.Errorf("v1alpha2 Job round trip (-want +got):\n%s", d)
	}
}

func TestWorkflowAndJobRoundTripFuzz(t *testing.T) {
	f := randfill.New().NilChance(0.3).NumElements(0, 3).Funcs(
		func(tm *metav1.Time, c randfill.Continue) { *tm = metav1.Unix(c.Int63n(1<<31), 0) },
	)
	for range 300 {
		wf := &v1alpha1.Workflow{TypeMeta: metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha1", Kind: "Workflow"}, ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "n"}}
		f.Fill(&wf.Spec)
		wf = apiRoundTrip(t, wf)
		back := wfToV1(t, wfToV2(t, wf))
		delete(back.Annotations, AnnotationV1Alpha2)
		if d := cmp.Diff(wf, back, cmpOpts...); d != "" {
			t.Fatalf("Workflow round trip (-want +got):\n%s", d)
		}

		j := &bmcv1.Job{TypeMeta: metav1.TypeMeta{APIVersion: "bmc.tinkerbell.org/v1alpha1", Kind: "Job"}, ObjectMeta: metav1.ObjectMeta{Name: "x"}}
		f.Fill(&j.Spec)
		j = apiRoundTrip(t, j)
		hub := &bmcv2.Job{}
		if err := JobToV1Alpha2(j.DeepCopy(), hub); err != nil {
			t.Fatal(err)
		}
		jb := &bmcv1.Job{}
		if err := JobToV1Alpha1(apiRoundTrip(t, hub), jb); err != nil {
			t.Fatal(err)
		}
		delete(jb.Annotations, AnnotationV1Alpha2)
		if d := cmp.Diff(j, apiRoundTrip(t, jb), cmpOpts...); d != "" {
			t.Fatalf("Job round trip (-want +got):\n%s", d)
		}
	}
}
