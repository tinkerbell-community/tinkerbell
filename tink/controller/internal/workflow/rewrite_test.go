package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	bmcv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// captTemplate is a Template as Cluster API Provider Tinkerbell creates them: one task on the machine's
// Agent (device_1), writing the OS image and rebooting.
const captTemplate = `version: "0.1"
name: talos
global_timeout: 6000
tasks:
  - name: "talos"
    worker: "{{.device_1}}"
    volumes:
      - /dev:/dev
    environment:
      MIRROR: "{{ .mirror }}"
    actions:
      - name: "stream-image"
        image: ghcr.io/sidero-community/actions/talos2disk:latest
        timeout: 600
        environment:
          DEST_DISK: {{ index .Hardware.Disks 0 }}
          USER_DATA: "{{ .hardware.spec.userData }}"
          METADATA: "{{ .metadata_url }}/{{ .device_1 }}"
          ID: '{{ .Hardware.Metadata.Instance.ID | quote }}'
          VENDOR: "{{ .Hardware.VendorData }}"
      - name: "reboot"
        image: ghcr.io/jacobweinstock/waitdaemon:latest
        timeout: 90
        pid: host
        command: ["sh", "-c", "echo b > /proc/sysrq-trigger"]
`

const captMAC = "3c:ec:ef:4c:4f:54"

func captObjects() (*v1alpha1.Template, *v1alpha1.Hardware, *v1alpha1.Workflow) {
	data := captTemplate
	tpl := &v1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "machine-1", Namespace: "default", UID: "tpl-uid"},
		Spec:       v1alpha1.TemplateSpec{Data: &data},
	}
	hw := &v1alpha1.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw-1", Namespace: "default"},
		Spec: v1alpha1.HardwareSpec{
			Disks:      []v1alpha1.Disk{{Device: "/dev/nvme0n1"}},
			UserData:   valueToPointer("#cloud-config"),
			VendorData: valueToPointer("vendor"),
			Metadata:   &v1alpha1.HardwareMetadata{Instance: &v1alpha1.MetadataInstance{ID: captMAC}},
			Interfaces: []v1alpha1.Interface{{
				DHCP:    &v1alpha1.DHCP{MAC: captMAC, IP: &v1alpha1.IP{Address: "172.16.10.100", Netmask: "255.255.255.0"}, UEFI: true},
				Netboot: &v1alpha1.Netboot{AllowPXE: valueToPointer(false), AllowWorkflow: valueToPointer(true)},
			}},
		},
	}
	wf := &v1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "machine-1", Namespace: "default", UID: "wf-uid"},
		Spec: v1alpha1.WorkflowSpec{
			TemplateRef: "machine-1",
			HardwareRef: "hw-1",
			HardwareMap: map[string]string{"device_1": captMAC, "mirror": "https://mirror.example.com"},
			BootOptions: v1alpha1.BootOptions{ToggleAllowNetboot: true},
		},
	}
	return tpl, hw, wf
}

// renderBothWays reconciles the CAPT Workflow in v1alpha1 compatibility mode and rewritten onto Tasks, and
// returns both.
func renderBothWays(t *testing.T) (compat, native *v1alpha2.Workflow, c client.Client) {
	t.Helper()
	tpl, hw, wf := captObjects()
	key := client.ObjectKeyFromObject(wf)
	run := func(compatOnly bool, times int) (*v1alpha2.Workflow, client.Client) {
		c := GetFakeClientBuilder().WithObjects(tpl.DeepCopy(), v2(t, hw), v2(t, wf)).Build()
		r := NewReconciler(c, fakeReferences{}, WithMetadataURL("http://192.0.2.10:7080"))
		r.compatOnly = compatOnly
		r.nowFunc = TestTime.Now
		for range times {
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
		}
		got := &v1alpha2.Workflow{}
		if err := c.Get(context.Background(), key, got); err != nil {
			t.Fatal(err)
		}
		return got, c
	}
	compat, _ = run(true, 1)
	native, c = run(false, 2)
	return compat, native, c
}

// TestRewriteRendersTheSame renders a CAPT Template both ways: in v1alpha1 compatibility mode, and
// rewritten onto the Tasks it translates to. Both run the same actions.
func TestRewriteRendersTheSame(t *testing.T) {
	compat, native, c := renderBothWays(t)

	if !engine.Compat(compat) {
		t.Fatal("the compatibility mode Workflow was rewritten")
	}
	if engine.Compat(native) {
		t.Fatal("the Workflow was not rewritten onto Tasks")
	}
	// The rewrite: one task per template task, on the Agent its worker names, bound to the Hardware with
	// the boot options, and the global timeout.
	wantTasks := []v1alpha2.WorkflowTask{{
		AgentID: captMAC,
		TaskRef: v1alpha2.SimpleReference{Name: "machine-1-talos", Namespace: "default"},
		Hardware: &v1alpha2.WorkflowHardware{
			HardwareRef: &v1alpha2.SimpleReference{Name: "hw-1", Namespace: "default"},
			BootOptions: &v1alpha2.BootOptions{ToggleNetboot: true},
		},
	}}
	if d := cmp.Diff(wantTasks, native.Spec.Tasks); d != "" {
		t.Errorf("rewritten tasks (-want +got):\n%s", d)
	}
	if pointerToValue(native.Spec.TimeoutSeconds) != 6000 {
		t.Errorf("timeoutSeconds = %v, want the template's global_timeout", native.Spec.TimeoutSeconds)
	}
	task := &v1alpha2.Task{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "machine-1-talos", Namespace: "default"}, task); err != nil {
		t.Fatalf("the Template's Task: %v", err)
	}
	if ref := metav1.GetControllerOf(task); ref == nil || ref.UID != "tpl-uid" {
		t.Errorf("Task controller = %+v, want the Template", ref)
	}

	// Rendered, both run the same actions, as the same tasks, on the same Agent, with the same boot options.
	ignore := []cmp.Option{
		cmpopts.IgnoreFields(v1alpha2.Metadata{}, "ID", "Hardware"),
		cmpopts.IgnoreFields(v1alpha2.WorkflowCondition{}, "Time"),
		cmpopts.EquateEmpty(),
	}
	if d := cmp.Diff(compat.Status.RenderedTasks, native.Status.RenderedTasks, ignore...); d != "" {
		t.Errorf("rendered tasks (-compatibility mode +rewritten):\n%s", d)
	}
	for _, s := range []*v1alpha2.WorkflowStatus{&compat.Status, &native.Status} {
		if s.Metadata.Workflow.State != v1alpha2.StatePreparing || s.Metadata.Task.AgentID != captMAC || s.GlobalTimeout != 6000 {
			t.Errorf("status metadata = %+v, global timeout %d; want preparing on %s, 6000", s.Metadata, s.GlobalTimeout, captMAC)
		}
	}
	env := native.Status.RenderedTasks[0].Actions[0].Env
	want := map[string]string{
		"DEST_DISK": "/dev/nvme0n1",
		"USER_DATA": "#cloud-config",
		"METADATA":  "http://192.0.2.10:7080/" + captMAC,
		"ID":        `"` + captMAC + `"`,
		"VENDOR":    "vendor",
	}
	if d := cmp.Diff(want, env); d != "" {
		t.Errorf("rendered environment (-want +got):\n%s", d)
	}
}

// TestRewrittenRunningV1Alpha1View verifies v1alpha1 clients read a rewritten, running Workflow as the
// Workflow they created, with the state, tasks and actions the v1alpha1 engine reported.
func TestRewrittenRunningV1Alpha1View(t *testing.T) {
	_, native, _ := renderBothWays(t)
	_, _, original := captObjects()

	// Netboot is allowed, and the Tink server has served the first action and the Agent reports it running.
	running := native.DeepCopy()
	rt := &running.Status.RenderedTasks[0]
	start := metav1.NewTime(TestTime.Now())
	rt.Actions[0].Metadata.State = v1alpha2.StateRunning
	rt.Actions[0].Metadata.StartTime = &start
	running.Status.Metadata.Workflow.State = v1alpha2.StateRunning
	running.Status.Metadata.Task = v1alpha2.Metadata{AgentID: captMAC, ID: rt.Metadata.ID, Name: rt.Name, State: v1alpha2.StateRunning}
	running.Status.Metadata.Action = v1alpha2.Metadata{ID: rt.Actions[0].Metadata.ID, Name: rt.Actions[0].Name, State: v1alpha2.StateRunning}
	running.Status.BootOptions.AllowNetboot.ToggledTrue = true

	got := v1Workflow(t, running)
	if d := cmp.Diff(original.Spec, got.Spec); d != "" {
		t.Errorf("spec (-created +read):\n%s", d)
	}
	if got.Status.State != v1alpha1.WorkflowStateRunning || got.Status.AgentID != captMAC || got.Status.TemplateRendering != v1alpha1.TemplateRenderingSuccessful || !got.Status.BootOptions.AllowNetboot.ToggledTrue {
		t.Errorf("status = %+v", got.Status)
	}
	wantCurrent := &v1alpha1.CurrentState{
		AgentID: captMAC, TaskID: string(rt.Metadata.ID), TaskName: "talos",
		ActionID: string(rt.Actions[0].Metadata.ID), ActionName: "stream-image", State: v1alpha1.WorkflowStateRunning,
	}
	if d := cmp.Diff(wantCurrent, got.Status.CurrentState); d != "" {
		t.Errorf("currentState (-want +got):\n%s", d)
	}
	if len(got.Status.Tasks) != 1 {
		t.Fatalf("tasks = %+v", got.Status.Tasks)
	}
	task := got.Status.Tasks[0]
	if task.Name != "talos" || task.AgentID != captMAC || len(task.Actions) != 2 {
		t.Fatalf("task = %+v", task)
	}
	if a := task.Actions[0]; a.Name != "stream-image" || a.State != v1alpha1.WorkflowStateRunning || a.Timeout != 600 || a.ExecutionStart == nil {
		t.Errorf("first action = %+v", a)
	}
	reboot := task.Actions[1]
	if reboot.Name != "reboot" || reboot.State != v1alpha1.WorkflowStatePending || reboot.Namespaces == nil || reboot.Namespaces.PID != "host" {
		t.Errorf("second action = %+v", reboot)
	}
	if d := cmp.Diff([]string{"sh", "-c", "echo b > /proc/sysrq-trigger"}, reboot.Command); d != "" {
		t.Errorf("command (-want +got):\n%s", d)
	}
}

func TestRewriteRefused(t *testing.T) {
	twoTasks := `version: "0.1"
name: two
global_timeout: 600
tasks:
  - name: "a"
    worker: "{{.device_1}}"
    actions:
      - name: "a"
        image: alpine
  - name: "b"
    worker: "{{.device_1}}"
    actions:
      - name: "b"
        image: alpine
`
	structural := `version: "0.1"
name: s
global_timeout: 600
tasks:
  - name: "a"
    worker: "{{.device_1}}"
    actions:
{{- range .disks }}
      - name: "wipe"
        image: alpine
{{- end }}
`
	tests := map[string]struct {
		template   string
		mutate     func(*v1alpha1.Workflow)
		mutateHW   func(*v1alpha1.Hardware)
		wantReason string
		// wantRendered says compatibility mode renders the Workflow.
		wantRendered bool
	}{
		"customboot media URL templated with v1alpha1 data": {
			template: captTemplate,
			mutate: func(wf *v1alpha1.Workflow) {
				wf.Spec.BootOptions = v1alpha1.BootOptions{BootMode: v1alpha1.BootModeCustomboot, CustombootConfig: v1alpha1.CustombootConfig{
					PreparingActions: []bmcv1alpha1.Action{{VirtualMediaAction: &bmcv1alpha1.VirtualMediaAction{MediaURL: `http://x/{{ (index .Hardware.Interfaces 0).DHCP.MAC }}.iso`, Kind: bmcv1alpha1.VirtualMediaCD}}},
				}}
			},
			wantReason:   "do not render",
			wantRendered: true,
		},
		"data v1alpha1 renders empty is missing": {
			template:     captTemplate,
			mutateHW:     func(hw *v1alpha1.Hardware) { hw.Spec.VendorData = nil },
			wantReason:   "do not render",
			wantRendered: true,
		},
		"template does not translate": {
			template:   structural,
			wantReason: "range",
		},
		"boot options and several tasks": {
			template:   twoTasks,
			wantReason: "boot options per task",
		},
		"hardwareMap sets metadata_url": {
			template:   captTemplate,
			mutate:     func(wf *v1alpha1.Workflow) { wf.Spec.HardwareMap["metadata_url"] = "http://198.51.100.7" },
			wantReason: "metadata_url",
		},
		"worker missing from hardwareMap": {
			template: twoTasks,
			mutate: func(wf *v1alpha1.Workflow) {
				wf.Spec.BootOptions = v1alpha1.BootOptions{}
				delete(wf.Spec.HardwareMap, "device_1")
			},
			wantReason: "no Agent",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tpl, hw, wf := captObjects()
			tpl.Spec.Data = &tt.template
			if tt.mutate != nil {
				tt.mutate(wf)
			}
			if tt.mutateHW != nil {
				tt.mutateHW(hw)
			}
			c := GetFakeClientBuilder().WithObjects(tpl, v2(t, hw), v2(t, wf)).Build()
			rec := events.NewFakeRecorder(10)
			r := NewReconciler(c, fakeReferences{}, WithMetadataURL("http://192.0.2.10:7080"), WithEventRecorder(rec))
			r.nowFunc = TestTime.Now
			_, _ = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(wf)})

			got := &v1alpha2.Workflow{}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(wf), got); err != nil {
				t.Fatal(err)
			}
			if !engine.Compat(got) {
				t.Fatalf("the Workflow was rewritten: %+v", got.Spec)
			}
			if tt.wantRendered && !got.Status.HasCondition(v1alpha2.TemplateRenderedSuccess, metav1.ConditionTrue) {
				t.Errorf("compatibility mode did not render: %+v", got.Status.Conditions)
			}
			select {
			case e := <-rec.Events:
				if !strings.Contains(e, reasonCompatibility) || !strings.Contains(e, tt.wantReason) {
					t.Errorf("event = %q, want the reason, %q", e, tt.wantReason)
				}
			default:
				t.Error("no event says why the Workflow runs in compatibility mode")
			}
		})
	}
}

// TestRewriteOnlyNewWorkflows verifies a Workflow that started keeps running the Template it started with.
func TestRewriteOnlyNewWorkflows(t *testing.T) {
	tpl, hw, wf := captObjects()
	wf.Status = v1alpha1.WorkflowStatus{State: v1alpha1.WorkflowStatePending, AgentID: captMAC, Tasks: []v1alpha1.Task{{Name: "talos", AgentID: captMAC, Actions: []v1alpha1.Action{{Name: "a", State: v1alpha1.WorkflowStatePending}}}}}
	c := GetFakeClientBuilder().WithObjects(tpl, v2(t, hw), v2(t, wf)).Build()
	r := NewReconciler(c, fakeReferences{})
	if rewritten, err := r.rewriteToTasks(context.Background(), v2Workflow(t, wf)); err != nil || rewritten {
		t.Fatalf("rewritten = %v, %v; want a started Workflow left alone", rewritten, err)
	}
	stored := &v1alpha2.Task{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "machine-1-talos", Namespace: "default"}, stored); err == nil {
		t.Error("Tasks created for a Workflow that is not rewritten")
	}
}
