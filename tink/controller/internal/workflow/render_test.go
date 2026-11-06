package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func nativeHardware() *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw1", Namespace: "default"},
		Spec: v1alpha2.HardwareSpec{
			AgentID:           "agent-hw",
			Instance:          &v1alpha2.Instance{Userdata: valueToPointer("{{ .references.ud.data.v }}")},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{mac1: {Netboot: &v1alpha2.Netboot{Disabled: true}}},
			BMC:               &v1alpha2.BMC{Connection: &bmc.Connection{Host: "10.0.0.1"}},
		},
	}
}

// nativeWorkflow runs task-a on the Hardware hw1 and task-b on agent-2.
func nativeWorkflow() *v1alpha2.Workflow {
	return &v1alpha2.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "default", UID: "wf-uid"},
		Spec: v1alpha2.WorkflowSpec{
			References: map[string]v1alpha2.Reference{"cfg": {Name: "cfg", Namespace: "default", Resource: "configmaps", Version: "v1"}},
			Globals: &v1alpha2.Extra{
				Env:         map[string]string{"G": "{{ .references.cfg.data.v }}", "SHARED": "global"},
				TemplateMap: map[string]string{"k": "v-{{ .hardware.spec.agentID }}"},
				Volumes:     []v1alpha2.Volume{"/g:/g"},
			},
			TimeoutSeconds: valueToPointer(int64(600)),
			Tasks: []v1alpha2.WorkflowTask{
				{
					TaskRef:  v1alpha2.SimpleReference{Name: "task-a"},
					Hardware: &v1alpha2.WorkflowHardware{HardwareRef: &v1alpha2.SimpleReference{Name: "hw1"}},
					Extra:    &v1alpha2.Extra{Env: map[string]string{"SHARED": "extra"}, Volumes: []v1alpha2.Volume{"/e:/e"}},
				},
				{AgentID: "agent-2", TaskRef: v1alpha2.SimpleReference{Name: "task-b"}},
			},
		},
	}
}

func nativeTasks() []client.Object {
	return []client.Object{
		&v1alpha2.Task{
			ObjectMeta: metav1.ObjectMeta{Name: "task-a", Namespace: "default", Annotations: map[string]string{conversion.AnnotationTemplateTask: "os install"}},
			Spec: v1alpha2.TaskSpec{
				Env:        map[string]string{"T": "{{ .task.spec.vars.x }}", "SHARED": "task"},
				Vars:       map[string]string{"x": "y"},
				References: map[string]v1alpha2.Reference{"r": {Name: "r", Namespace: "default", Resource: "secrets", Version: "v1"}},
				Volumes:    []v1alpha2.Volume{"/t:/t"},
				Actions: []v1alpha2.Action{
					{
						Name: "a1", Image: "alpine", Command: "/bin/sh",
						Args: []string{"-c", "echo {{ .task.spec.vars.x }}"},
						Env: map[string]string{
							"UD":  "{{ .hardware.spec.instance.userdata }}",
							"WF":  "{{ .workflow.spec.globals.templateMap.k }}",
							"MD":  "{{ .metadata_url }}",
							"REF": "{{ .references.r.data.z }}",
						},
						TimeoutSeconds: valueToPointer(int64(60)),
					},
					{Name: "a2", Image: "alpine", If: `{{ eq .hardware.spec.agentID "nope" }}`},
					{Name: "a3", Image: "alpine", If: "TRUE"},
				},
			},
		},
		&v1alpha2.Task{
			ObjectMeta: metav1.ObjectMeta{Name: "task-b", Namespace: "default"},
			Spec:       v1alpha2.TaskSpec{Actions: []v1alpha2.Action{{Name: "b1", Image: "alpine", If: "false"}}},
		},
	}
}

func nativeReferences() fakeReferences {
	rendered := nativeHardware()
	rendered.Spec.Instance.Userdata = valueToPointer("#cloud-config")
	return fakeReferences{
		rendered2: rendered,
		refsFor: map[string]map[string]any{
			"wf":     {"cfg": map[string]any{"data": map[string]any{"v": "gv"}}},
			"task-a": {"r": map[string]any{"data": map[string]any{"z": "zz"}}},
		},
	}
}

func TestRenderNative(t *testing.T) {
	wf := nativeWorkflow()
	objs := append(nativeTasks(), nativeHardware(), wf)
	r := NewReconciler(GetFakeClientBuilder().WithObjects(objs...).Build(), nativeReferences(), WithMetadataURL("http://192.0.2.10:7080"))
	r.nowFunc = TestTime.Now

	key := client.ObjectKeyFromObject(wf)
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	got := &v1alpha2.Workflow{}
	if err := r.client.Get(context.Background(), key, got); err != nil {
		t.Fatal(err)
	}

	want := v1alpha2.WorkflowStatus{
		GlobalTimeout: 600,
		Metadata: v1alpha2.WorkflowMetadata{
			Workflow: v1alpha2.Metadata{State: v1alpha2.StatePending},
			Task:     v1alpha2.Metadata{AgentID: "agent-hw"},
		},
		RenderedTasks: []v1alpha2.TaskWithMetadata{
			{
				// The name of the template task the Task was translated from.
				Name: "os install",
				// The Task's environment, with the Workflow's globals and the task's extra over it.
				Env:     map[string]string{"T": "y", "SHARED": "extra", "G": "gv"},
				Volumes: []v1alpha2.Volume{"/t:/t", "/g:/g", "/e:/e"},
				// The task has no agentID: it runs on its Hardware's Agent.
				Metadata: v1alpha2.Metadata{Name: "os install", AgentID: "agent-hw", Hardware: "hw1", State: v1alpha2.StatePending},
				Actions: []v1alpha2.ActionWithMetadata{
					{
						Action: v1alpha2.Action{
							Name: "a1", Image: "alpine", Command: "/bin/sh", Args: []string{"-c", "echo y"},
							Env: map[string]string{
								"UD":  "#cloud-config", // the backend's rendering of the Hardware
								"WF":  "v-agent-hw",    // the Workflow, rendered with the task's Hardware
								"MD":  "http://192.0.2.10:7080",
								"REF": "zz",
							},
							TimeoutSeconds: valueToPointer(int64(60)),
						},
						Metadata: v1alpha2.Metadata{Name: "a1", State: v1alpha2.StatePending},
					},
					// a2 renders if to false and is skipped.
					{Action: v1alpha2.Action{Name: "a3", Image: "alpine", If: "TRUE"}, Metadata: v1alpha2.Metadata{Name: "a3", State: v1alpha2.StatePending}},
				},
			},
			// Every action is skipped: nothing to run.
			{
				Name: "task-b", Env: map[string]string{"G": "gv", "SHARED": "global"}, Volumes: []v1alpha2.Volume{"/g:/g"},
				Metadata: v1alpha2.Metadata{Name: "task-b", AgentID: "agent-2", State: v1alpha2.StateSuccess},
			},
		},
		Conditions: []v1alpha2.WorkflowCondition{{Type: v1alpha2.TemplateRenderedSuccess, Status: metav1.ConditionTrue, Reason: reasonComplete, Message: "template rendered successfully"}},
	}
	if d := cmp.Diff(want, got.Status, cmpopts.IgnoreFields(v1alpha2.Metadata{}, "ID"), cmpopts.IgnoreFields(v1alpha2.WorkflowCondition{}, "Time"), cmpopts.EquateEmpty()); d != "" {
		t.Errorf("status (-want +got):\n%s", d)
	}
	for _, rt := range got.Status.RenderedTasks {
		if rt.Metadata.ID == "" {
			t.Errorf("task %s has no ID", rt.Name)
		}
		for _, a := range rt.Actions {
			if a.Metadata.ID == "" {
				t.Errorf("action %s has no ID", a.Name)
			}
		}
	}
}

func TestRenderNativeErrors(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*v1alpha2.Workflow)
		wantErr string
	}{
		"task without a taskRef": {
			mutate:  func(wf *v1alpha2.Workflow) { wf.Spec.Tasks[1].TaskRef = v1alpha2.SimpleReference{} },
			wantErr: "no taskRef",
		},
		"missing Task": {
			mutate:  func(wf *v1alpha2.Workflow) { wf.Spec.Tasks[1].TaskRef.Name = "missing" },
			wantErr: "missing",
		},
		"Hardware in another namespace": {
			mutate:  func(wf *v1alpha2.Workflow) { wf.Spec.Tasks[0].Hardware.HardwareRef.Namespace = "other" },
			wantErr: "not in the workflow's namespace",
		},
		"no Agent": {
			mutate:  func(wf *v1alpha2.Workflow) { wf.Spec.Tasks[1].AgentID = "" },
			wantErr: "no agentID",
		},
		"template error": {
			mutate:  func(wf *v1alpha2.Workflow) { wf.Spec.Globals.Env["BAD"] = "{{ .nope.nope }}" },
			wantErr: "rendering workflow",
		},
		"no tasks": {
			mutate:  func(wf *v1alpha2.Workflow) { wf.Spec.Tasks = nil },
			wantErr: "no tasks",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			wf := nativeWorkflow()
			tt.mutate(wf)
			r := NewReconciler(GetFakeClientBuilder().WithObjects(append(nativeTasks(), nativeHardware())...).Build(), nativeReferences(), WithMetadataURL("http://192.0.2.10:7080"))
			err := r.processWorkflow(context.Background(), logr.Discard(), wf)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if wf.Status.HasCondition(v1alpha2.TemplateRenderedSuccess, metav1.ConditionTrue) || len(wf.Status.RenderedTasks) != 0 {
				t.Errorf("a failed rendering must not publish tasks: %+v", wf.Status)
			}
		})
	}
}

// TestNativeBootOptionsPerTask runs boot options around the second task of a native Workflow: netboot is
// allowed and the machine netbooted before it, and netboot disallowed after it.
func TestNativeBootOptionsPerTask(t *testing.T) {
	wf := nativeWorkflow()
	wf.Spec.Tasks[1].Hardware = &v1alpha2.WorkflowHardware{
		HardwareRef: &v1alpha2.SimpleReference{Name: "hw1"},
		BootOptions: &v1alpha2.BootOptions{ToggleNetboot: true, BootMode: v1alpha2.BootModeNetboot},
	}
	wf.Spec.Tasks[1].TaskRef.Name = "task-a"
	c := GetFakeClientBuilder().WithObjects(append(nativeTasks(), nativeHardware(), wf)...).Build()
	r := NewReconciler(c, nativeReferences(), WithMetadataURL("http://192.0.2.10:7080"))
	r.nowFunc = TestTime.Now
	ctx := context.Background()
	key := client.ObjectKeyFromObject(wf)
	reconcileOnce := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() *v1alpha2.Workflow {
		t.Helper()
		got := &v1alpha2.Workflow{}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	update := func(f func(*v1alpha2.Workflow)) {
		t.Helper()
		got := read()
		f(got)
		if err := c.Status().Update(ctx, got); err != nil {
			t.Fatal(err)
		}
	}
	netboot := func() bool {
		t.Helper()
		hw := &v1alpha2.Hardware{}
		if err := c.Get(ctx, types.NamespacedName{Name: "hw1", Namespace: "default"}, hw); err != nil {
			t.Fatal(err)
		}
		return !hw.Spec.NetworkInterfaces[mac1].Netboot.Disabled
	}

	// The first task has no boot options: its actions are served straight away.
	reconcileOnce()
	if got := read(); got.Status.Metadata.Workflow.State != v1alpha2.StatePending || got.Status.Metadata.Task.AgentID != "agent-hw" {
		t.Fatalf("state %v, agent %q; want pending on agent-hw", got.Status.Metadata.Workflow.State, got.Status.Metadata.Task.AgentID)
	}

	// The Tink server records the first task's success and, as the next task has boot options, hands over in Post.
	update(func(wf *v1alpha2.Workflow) {
		rt := &wf.Status.RenderedTasks[0]
		for i := range rt.Actions {
			rt.Actions[i].Metadata.State = v1alpha2.StateSuccess
		}
		wf.Status.Metadata.Workflow.State = v1alpha2.StatePost
		wf.Status.Metadata.Task = v1alpha2.Metadata{AgentID: "agent-hw", ID: rt.Metadata.ID, Name: rt.Name, State: v1alpha2.StatePost}
		wf.Status.Metadata.Action = v1alpha2.Metadata{ID: rt.Actions[1].Metadata.ID, Name: "a3", State: v1alpha2.StateSuccess}
	})
	reconcileOnce()
	got := read()
	if got.Status.Metadata.Workflow.State != v1alpha2.StatePreparing || got.Status.Metadata.Task.AgentID != "agent-2" || got.Status.Metadata.Task.ID != got.Status.RenderedTasks[1].Metadata.ID {
		t.Fatalf("state %v, current task %+v; want preparing the second task, on agent-2", got.Status.Metadata.Workflow.State, got.Status.Metadata.Task)
	}

	// Preparing allows netboot and netboots the machine with a Job of the second task.
	reconcileOnce()
	if !netboot() {
		t.Error("netboot was not allowed before the task")
	}
	jobKey := types.NamespacedName{Name: "netboot-wf-1", Namespace: "default"}
	for i := 0; i < 8 && read().Status.Metadata.Workflow.State == v1alpha2.StatePreparing; i++ {
		completeJob(t, c, jobKey)
		reconcileOnce()
	}
	// The Workflow has started, so it continues running.
	if got := read(); got.Status.Metadata.Workflow.State != v1alpha2.StateRunning {
		t.Fatalf("state %v, want running", got.Status.Metadata.Workflow.State)
	}

	// The second task succeeds: its netboot is disallowed again and the Workflow succeeds.
	update(func(wf *v1alpha2.Workflow) {
		rt := &wf.Status.RenderedTasks[1]
		for i := range rt.Actions {
			rt.Actions[i].Metadata.State = v1alpha2.StateSuccess
		}
		wf.Status.Metadata.Workflow.State = v1alpha2.StatePost
		wf.Status.Metadata.Task = v1alpha2.Metadata{AgentID: "agent-2", ID: rt.Metadata.ID, Name: rt.Name, State: v1alpha2.StatePost}
		wf.Status.Metadata.Action = v1alpha2.Metadata{ID: rt.Actions[1].Metadata.ID, Name: "a3", State: v1alpha2.StateSuccess}
	})
	reconcileOnce()
	if netboot() {
		t.Error("netboot was not disallowed after the task")
	}
	got = read()
	if got.Status.Metadata.Workflow.State != v1alpha2.StateSuccess || got.Status.RenderedTasks[1].Metadata.State != v1alpha2.StateSuccess {
		t.Fatalf("state %v, task state %v; want success", got.Status.Metadata.Workflow.State, got.Status.RenderedTasks[1].Metadata.State)
	}
}

// completeJob makes the Job key, if it exists, complete. The fake client gives objects no UID, which the
// engine tracks Jobs by and the API server sets, so the Job is created again with one.
func completeJob(t *testing.T, c client.Client, key types.NamespacedName) {
	t.Helper()
	job := &bmc.Job{}
	if err := c.Get(context.Background(), key, job); err != nil {
		return
	}
	if job.Spec.HardwareRef == nil || job.Spec.HardwareRef.Name != "hw1" || len(job.Spec.Operations) != 3 || job.Spec.Operations[1].BootDevice == nil || job.Spec.Operations[1].BootDevice.Name != bmc.BootDevicePXE {
		t.Errorf("job spec = %+v", job.Spec)
	}
	if job.UID != "" {
		return
	}
	if err := c.Delete(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	job = &bmc.Job{
		ObjectMeta: metav1.ObjectMeta{Name: job.Name, Namespace: job.Namespace, UID: "job-uid", OwnerReferences: job.OwnerReferences},
		Spec:       job.Spec,
		Status:     bmc.JobStatus{Conditions: []bmc.Condition{{Type: bmc.ConditionTypeCompleted, Status: bmc.ConditionStatusTrue}}},
	}
	if err := c.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
}

// TestNativeBootOptionsRendered verifies templated boot options are rendered with the task's Hardware.
func TestNativeBootOptionsRendered(t *testing.T) {
	wf := nativeWorkflow()
	wf.Spec.Tasks[0].Hardware.BootOptions = &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeIsoboot, ISOURL: "http://example.com/{{ .hardware.spec.agentID }}.iso"}
	c := GetFakeClientBuilder().WithObjects(append(nativeTasks(), nativeHardware(), wf)...).Build()
	r := NewReconciler(c, nativeReferences(), WithMetadataURL("http://192.0.2.10:7080"))
	r.nowFunc = TestTime.Now
	ctx := context.Background()
	for range 3 {
		if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(wf)}); err != nil {
			t.Fatal(err)
		}
	}
	job := &bmc.Job{}
	if err := c.Get(ctx, types.NamespacedName{Name: "iso-mount-wf", Namespace: "default"}, job); err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, op := range job.Spec.Operations {
		if op.VirtualMediaAction != nil {
			urls = append(urls, op.VirtualMediaAction.MediaURL)
		}
		if op.BootDevice != nil && !op.BootDevice.EFIBoot {
			t.Error("Hardware without spec.bmc.bootMode BIOS boots UEFI")
		}
	}
	if d := cmp.Diff([]string{"", "http://example.com/agent-hw.iso"}, urls); d != "" {
		t.Errorf("virtual media (-want +got):\n%s", d)
	}
}

func TestTaskTimeout(t *testing.T) {
	wf := nativeWorkflow()
	wf.Spec.Tasks[0].TimeoutSeconds = valueToPointer(int64(10))
	start := metav1.NewTime(TestTime.BeforeSec(1))
	wf.Status = v1alpha2.WorkflowStatus{
		Metadata: v1alpha2.WorkflowMetadata{
			Workflow: v1alpha2.Metadata{State: v1alpha2.StateRunning},
			Task:     v1alpha2.Metadata{AgentID: "agent-hw", ID: "t0"},
			Action:   v1alpha2.Metadata{ID: "a0", State: v1alpha2.StateRunning},
		},
		RenderedTasks: []v1alpha2.TaskWithMetadata{
			{Name: "t0", Metadata: v1alpha2.Metadata{ID: "t0", AgentID: "agent-hw"}, Actions: []v1alpha2.ActionWithMetadata{{Metadata: v1alpha2.Metadata{ID: "a0", State: v1alpha2.StateRunning, StartTime: &start}}}},
			{Name: "t1", Metadata: v1alpha2.Metadata{ID: "t1", AgentID: "agent-2"}, Actions: []v1alpha2.ActionWithMetadata{{Metadata: v1alpha2.Metadata{ID: "a1", State: v1alpha2.StatePending}}}},
		},
	}
	c := GetFakeClientBuilder().WithObjects(wf).Build()
	now := TestTime.Now()
	r := NewReconciler(c, fakeReferences{})
	r.nowFunc = func() time.Time { return now }
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(wf)}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("RequeueAfter = %v, want the task's timeout", res.RequeueAfter)
	}
	now = now.Add(11 * time.Second)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got := &v1alpha2.Workflow{}
	if err := c.Get(context.Background(), req.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Metadata.Workflow.State != v1alpha2.StateTimeout || got.Status.RenderedTasks[0].Metadata.State != v1alpha2.StateTimeout || !strings.Contains(got.Status.Metadata.Workflow.Message, "t0 timed out") {
		t.Errorf("status = %+v, want the task and Workflow timed out", got.Status.Metadata)
	}
}

func TestDisabledNative(t *testing.T) {
	wf := nativeWorkflow()
	wf.Spec.Disabled = valueToPointer(true)
	c := GetFakeClientBuilder().WithObjects(append(nativeTasks(), nativeHardware(), wf)...).Build()
	r := NewReconciler(c, nativeReferences(), WithMetadataURL("http://192.0.2.10:7080"))
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(wf)}); err != nil {
		t.Fatal(err)
	}
	got := &v1alpha2.Workflow{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(wf), got); err != nil {
		t.Fatal(err)
	}
	// The Tink server finds the Workflow by its Agent, so that is all a disabled Workflow records.
	want := v1alpha2.WorkflowStatus{Metadata: v1alpha2.WorkflowMetadata{Task: v1alpha2.Metadata{AgentID: "agent-hw"}}}
	if d := cmp.Diff(want, got.Status, cmpopts.EquateEmpty()); d != "" {
		t.Errorf("status (-want +got):\n%s", d)
	}
}

// TestGlobalTimeoutFirstActionSkipped verifies the Workflow's timeout starts with its first action when its
// first task runs nothing.
func TestGlobalTimeoutFirstActionSkipped(t *testing.T) {
	wf := nativeWorkflow()
	start := metav1.NewTime(TestTime.Now())
	wf.Status = v1alpha2.WorkflowStatus{
		GlobalTimeout: 600,
		Metadata: v1alpha2.WorkflowMetadata{
			Workflow: v1alpha2.Metadata{State: v1alpha2.StateRunning},
			Task:     v1alpha2.Metadata{AgentID: "agent-2", ID: "t1"},
			Action:   v1alpha2.Metadata{ID: "b0", State: v1alpha2.StateRunning},
		},
		RenderedTasks: []v1alpha2.TaskWithMetadata{
			{Name: "t0", Metadata: v1alpha2.Metadata{ID: "t0", AgentID: "agent-hw", State: v1alpha2.StateSuccess}},
			{Name: "t1", Metadata: v1alpha2.Metadata{ID: "t1", AgentID: "agent-2"}, Actions: []v1alpha2.ActionWithMetadata{{Metadata: v1alpha2.Metadata{ID: "b0", State: v1alpha2.StateRunning, StartTime: &start}}}},
		},
	}
	c := GetFakeClientBuilder().WithObjects(wf).Build()
	r := NewReconciler(c, fakeReferences{})
	r.nowFunc = TestTime.Now
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(wf)}); err != nil {
		t.Fatal(err)
	}
	got := &v1alpha2.Workflow{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(wf), got); err != nil {
		t.Fatal(err)
	}
	if got.Status.GlobalExecutionStop == nil {
		t.Fatal("the Workflow's timeout did not start")
	}
}

func TestEFIBoot(t *testing.T) {
	v1 := func(uefi bool) *v1alpha2.Hardware {
		return v2(t, &v1alpha1.Hardware{
			ObjectMeta: metav1.ObjectMeta{Name: "hw", Namespace: "default"},
			Spec:       v1alpha1.HardwareSpec{Interfaces: []v1alpha1.Interface{{DHCP: &v1alpha1.DHCP{MAC: string(mac1), UEFI: uefi}}}},
		}).(*v1alpha2.Hardware)
	}
	withBMC := func(hw *v1alpha2.Hardware, mode v1alpha2.FirmwareMode) *v1alpha2.Hardware {
		hw = hw.DeepCopy()
		hw.Spec.BMC = &v1alpha2.BMC{BootMode: mode, Connection: &bmc.Connection{Host: "10.0.0.1"}}
		return hw
	}
	native := &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "hw", Namespace: "default"}}
	tests := map[string]struct {
		hw   *v1alpha2.Hardware
		want bool
	}{
		"v1alpha1 Hardware booting UEFI": {hw: v1(true), want: true},
		"v1alpha1 Hardware booting BIOS": {hw: v1(false)},
		// spec.bmc translated from the bmc Machine has the default boot mode, which says nothing.
		"v1alpha1 Hardware booting BIOS with a translated BMC": {hw: withBMC(v1(false), v1alpha2.FirmwareModeUEFI)},
		"v1alpha2 Hardware booting UEFI":                       {hw: withBMC(native, v1alpha2.FirmwareModeUEFI), want: true},
		"v1alpha2 Hardware with the default boot mode":         {hw: withBMC(native, ""), want: true},
		"v1alpha2 Hardware booting BIOS":                       {hw: withBMC(native, v1alpha2.FirmwareModeBIOS)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := efiBoot(tt.hw); got != tt.want {
				t.Errorf("efiBoot = %v, want %v", got, tt.want)
			}
		})
	}
}
