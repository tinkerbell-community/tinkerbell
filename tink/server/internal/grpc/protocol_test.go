package grpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// twoAgentWorkflow is a Workflow created through v1alpha1, whose Template has a task on each of two
// Agents, as the v1alpha1 engine rendered it.
func twoAgentWorkflow() *tinkerbell.Workflow {
	return &tinkerbell.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "default"},
		Spec:       tinkerbell.WorkflowSpec{TemplateRef: "tpl", HardwareRef: "hw"},
		Status: tinkerbell.WorkflowStatus{
			State:         tinkerbell.WorkflowStatePending,
			AgentID:       "agent-1",
			GlobalTimeout: 600,
			Tasks: []tinkerbell.Task{
				{
					ID: "t0", Name: "first", AgentID: "agent-1",
					Actions: []tinkerbell.Action{
						{ID: "a0", Name: "a0", Image: "alpine", Timeout: 60, State: tinkerbell.WorkflowStatePending},
						{ID: "a1", Name: "a1", Image: "alpine", Timeout: 60, State: tinkerbell.WorkflowStatePending},
					},
				},
				{
					ID: "t1", Name: "second", AgentID: "agent-2",
					Actions: []tinkerbell.Action{{ID: "b0", Name: "b0", Image: "busybox", Command: []string{"sh", "-c", "true"}, State: tinkerbell.WorkflowStatePending}},
				},
			},
		},
	}
}

func report(agent, task, action, name string, state proto.ActionStatusRequest_StateType, message string) *proto.ActionStatusRequest {
	return &proto.ActionStatusRequest{
		WorkflowId:        toPtr("default/wf"),
		AgentId:           toPtr(agent),
		TaskId:            toPtr(task),
		ActionId:          toPtr(action),
		ActionName:        toPtr(name),
		ActionState:       toPtr(state),
		ExecutionStart:    timestamppb.New(time.Unix(1_700_000_000, 0)),
		ExecutionStop:     timestamppb.New(time.Unix(1_700_000_030, 0)),
		ExecutionDuration: toPtr("30s"),
		Message:           &proto.ActionMessage{Message: toPtr(message)},
	}
}

// TestAgentProtocolV1Alpha1View runs a v1alpha1 Workflow with two Agents through the agent protocol and
// checks its status as v1alpha1 clients, such as Cluster API Provider Tinkerbell, read it at each step.
func TestAgentProtocolV1Alpha1View(t *testing.T) {
	backend := &mockBackendReadWriter{workflowV2: toV2Workflow(twoAgentWorkflow())}
	h := &Handler{Logger: logr.Discard(), Backend: backend, NowFunc: time.Now}
	ctx := context.Background()
	view := func() *tinkerbell.Workflow { return toV1Workflow(backend.workflowV2) }
	getAction := func(agent string) *proto.ActionResponse {
		t.Helper()
		resp, err := h.GetAction(ctx, &proto.ActionRequest{AgentId: toPtr(agent)})
		if err != nil {
			t.Fatalf("GetAction(%s): %v", agent, err)
		}
		return resp
	}
	reportOK := func(r *proto.ActionStatusRequest) {
		t.Helper()
		if _, err := h.ReportActionStatus(ctx, r); err != nil {
			t.Fatalf("ReportActionStatus: %v", err)
		}
	}
	type state struct {
		State        tinkerbell.WorkflowState
		AgentID      string
		CurrentState *tinkerbell.CurrentState
		Actions      []tinkerbell.WorkflowState
	}
	check := func(step string, want state) {
		t.Helper()
		v := view()
		got := state{State: v.Status.State, AgentID: v.Status.AgentID, CurrentState: v.Status.CurrentState}
		for _, task := range v.Status.Tasks {
			for _, a := range task.Actions {
				got.Actions = append(got.Actions, a.State)
			}
		}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("%s (-want +got):\n%s", step, d)
		}
	}
	const (
		pending = tinkerbell.WorkflowStatePending
		running = tinkerbell.WorkflowStateRunning
		success = tinkerbell.WorkflowStateSuccess
	)

	resp := getAction("agent-1")
	if resp.GetActionId() != "a0" || resp.GetTaskId() != "t0" || resp.GetWorkflowId() != "default/wf" || resp.GetTimeout() != 60 {
		t.Fatalf("first action = %v", resp)
	}
	check("first action served", state{
		State: pending, AgentID: "agent-1",
		CurrentState: &tinkerbell.CurrentState{AgentID: "agent-1", TaskID: "t0", TaskName: "first", ActionID: "a0", ActionName: "a0", State: pending},
		Actions:      []tinkerbell.WorkflowState{pending, pending, pending},
	})

	reportOK(report("agent-1", "t0", "a0", "a0", proto.ActionStatusRequest_RUNNING, ""))
	check("first action running", state{
		State: running, AgentID: "agent-1",
		CurrentState: &tinkerbell.CurrentState{AgentID: "agent-1", TaskID: "t0", TaskName: "first", ActionID: "a0", ActionName: "a0", State: running},
		Actions:      []tinkerbell.WorkflowState{running, pending, pending},
	})
	// A server restart re-serves the running action.
	if resp := getAction("agent-1"); resp.GetActionId() != "a0" {
		t.Fatalf("re-served action = %v", resp)
	}

	reportOK(report("agent-1", "t0", "a0", "a0", proto.ActionStatusRequest_SUCCESS, ""))
	if resp := getAction("agent-1"); resp.GetActionId() != "a1" {
		t.Fatalf("second action = %v", resp)
	}
	reportOK(report("agent-1", "t0", "a1", "a1", proto.ActionStatusRequest_RUNNING, ""))
	reportOK(report("agent-1", "t0", "a1", "a1", proto.ActionStatusRequest_SUCCESS, ""))
	// The first task is done: the Workflow is on the second task's Agent straight away. v1alpha1 clients
	// read that Agent as currentState.agentID too.
	check("first task done", state{
		State: running, AgentID: "agent-2",
		CurrentState: &tinkerbell.CurrentState{AgentID: "agent-2", TaskID: "t1", TaskName: "second", ActionID: "a1", ActionName: "a1", State: success},
		Actions:      []tinkerbell.WorkflowState{success, success, pending},
	})
	if _, err := h.GetAction(ctx, &proto.ActionRequest{AgentId: toPtr("agent-1")}); status.Code(err) != codes.NotFound {
		t.Fatalf("the first Agent has nothing left; err = %v", err)
	}

	resp = getAction("agent-2")
	if resp.GetActionId() != "b0" || resp.GetTaskId() != "t1" {
		t.Fatalf("second task action = %v", resp)
	}
	// The v1alpha1 command is the executable followed by its arguments.
	if d := cmp.Diff([]string{"sh", "-c", "true"}, resp.GetCommand()); d != "" {
		t.Errorf("command (-want +got):\n%s", d)
	}
	reportOK(report("agent-2", "t1", "b0", "b0", proto.ActionStatusRequest_RUNNING, ""))
	reportOK(report("agent-2", "t1", "b0", "b0", proto.ActionStatusRequest_SUCCESS, "done"))
	// The last action of the last task: the controller finishes the Workflow in Post.
	check("last action done", state{
		State: tinkerbell.WorkflowStatePost, AgentID: "agent-2",
		CurrentState: &tinkerbell.CurrentState{AgentID: "agent-2", TaskID: "t1", TaskName: "second", ActionID: "b0", ActionName: "b0", State: success},
		Actions:      []tinkerbell.WorkflowState{success, success, success},
	})
	v := view()
	a := v.Status.Tasks[1].Actions[0]
	if a.Message != "done" || a.ExecutionDuration != "30s" || a.ExecutionStart == nil || a.ExecutionStop == nil {
		t.Errorf("reported action = %+v", a)
	}
}

func TestReportActionStatusFailure(t *testing.T) {
	for _, tc := range []struct {
		state proto.ActionStatusRequest_StateType
		want  tinkerbell.WorkflowState
	}{
		{proto.ActionStatusRequest_FAILED, tinkerbell.WorkflowStateFailed},
		{proto.ActionStatusRequest_TIMEOUT, tinkerbell.WorkflowStateTimeout},
	} {
		t.Run(tc.state.String(), func(t *testing.T) {
			wf := twoAgentWorkflow()
			wf.Status.State = tinkerbell.WorkflowStateRunning
			backend := &mockBackendReadWriter{workflowV2: toV2Workflow(wf)}
			h := &Handler{Logger: logr.Discard(), Backend: backend}
			if _, err := h.ReportActionStatus(context.Background(), report("agent-1", "t0", "a0", "a0", tc.state, "disk not found")); err != nil {
				t.Fatal(err)
			}
			if got := toV1Workflow(backend.workflowV2).Status.State; got != tc.want {
				t.Errorf("state = %s, want %s", got, tc.want)
			}
			if msg := backend.workflowV2.Status.Metadata.Workflow.Message; msg != `action "a0" of task "first": `+lower(tc.want)+`: disk not found` {
				t.Errorf("workflow message = %q", msg)
			}
		})
	}
}

func lower(s tinkerbell.WorkflowState) string {
	return strings.ToLower(string(s))
}

func TestReportActionStatusInvalidState(t *testing.T) {
	backend := &mockBackendReadWriter{workflowV2: toV2Workflow(twoAgentWorkflow())}
	h := &Handler{Logger: logr.Discard(), Backend: backend}
	if _, err := h.ReportActionStatus(context.Background(), report("agent-1", "t0", "a0", "a0", proto.ActionStatusRequest_UNSPECIFIED, "")); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument: an unknown state would look like a new Workflow", err)
	}
	if backend.workflowUpdates != 0 {
		t.Errorf("updates = %d, want none", backend.workflowUpdates)
	}
}

// TestReportActionStatusPreparing verifies reports are stale while boot options run between tasks.
func TestReportActionStatusPreparing(t *testing.T) {
	wf := toV2Workflow(twoAgentWorkflow())
	wf.Status.Metadata.Workflow.State = v1alpha2.StatePreparing
	backend := &mockBackendReadWriter{workflowV2: wf}
	h := &Handler{Logger: logr.Discard(), Backend: backend}
	if _, err := h.ReportActionStatus(context.Background(), report("agent-1", "t0", "a0", "a0", proto.ActionStatusRequest_RUNNING, "")); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("err = %v, want FailedPrecondition", err)
	}
	if _, err := h.ReportActionStatus(context.Background(), report("agent-1", "t0", "a0", "a0", proto.ActionStatusRequest_SUCCESS, "")); err != nil {
		t.Fatalf("a completion report is acknowledged: %v", err)
	}
	if backend.workflowUpdates != 0 {
		t.Errorf("updates = %d, want none", backend.workflowUpdates)
	}
}

// nativeWorkflow is a native Workflow, rendered, with a task on each of two Agents.
func nativeWorkflow(first, second *v1alpha2.BootOptions) *v1alpha2.Workflow {
	hw := func(bo *v1alpha2.BootOptions) *v1alpha2.WorkflowHardware {
		return &v1alpha2.WorkflowHardware{HardwareRef: &v1alpha2.SimpleReference{Name: "hw"}, BootOptions: bo}
	}
	action := func(id string) v1alpha2.ActionWithMetadata {
		return v1alpha2.ActionWithMetadata{
			Action:   v1alpha2.Action{Name: id, Image: "alpine", Command: "/bin/sh", Args: []string{"-c", "echo " + id}, Env: map[string]string{"A": id}, Volumes: []v1alpha2.Volume{"/a:/a"}, Namespaces: v1alpha2.Namespaces{Network: "host"}, TimeoutSeconds: toPtr(int64(30))},
			Metadata: v1alpha2.Metadata{ID: types.UID("id-" + id), Name: id, State: v1alpha2.StatePending},
		}
	}
	return &v1alpha2.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "default"},
		Spec: v1alpha2.WorkflowSpec{Tasks: []v1alpha2.WorkflowTask{
			{AgentID: "agent-1", TaskRef: v1alpha2.SimpleReference{Name: "t0"}, Hardware: hw(first)},
			{AgentID: "agent-2", TaskRef: v1alpha2.SimpleReference{Name: "t1"}, Hardware: hw(second)},
		}},
		Status: v1alpha2.WorkflowStatus{
			Metadata: v1alpha2.WorkflowMetadata{Workflow: v1alpha2.Metadata{State: v1alpha2.StatePending}, Task: v1alpha2.Metadata{AgentID: "agent-1"}},
			RenderedTasks: []v1alpha2.TaskWithMetadata{
				{Name: "t0", Env: map[string]string{"A": "task", "T": "t0"}, Volumes: []v1alpha2.Volume{"/t:/t"}, Metadata: v1alpha2.Metadata{ID: "t0", Name: "t0", AgentID: "agent-1", Hardware: "hw", State: v1alpha2.StatePending}, Actions: []v1alpha2.ActionWithMetadata{action("a0")}},
				{Name: "t1", Metadata: v1alpha2.Metadata{ID: "t1", Name: "t1", AgentID: "agent-2", Hardware: "hw", State: v1alpha2.StatePending}, Actions: []v1alpha2.ActionWithMetadata{action("b0")}},
			},
		},
	}
}

func TestGetActionNative(t *testing.T) {
	backend := &mockBackendReadWriter{workflowV2: nativeWorkflow(nil, nil)}
	h := &Handler{Logger: logr.Discard(), Backend: backend}
	resp, err := h.GetAction(context.Background(), &proto.ActionRequest{AgentId: toPtr("agent-1")})
	if err != nil {
		t.Fatal(err)
	}
	want := &proto.ActionResponse{
		WorkflowId:  toPtr("default/wf"),
		TaskId:      toPtr("t0"),
		AgentId:     toPtr("agent-1"),
		ActionId:    toPtr("id-a0"),
		Name:        toPtr("a0"),
		Image:       toPtr("alpine"),
		Timeout:     toPtr(int64(30)),
		Command:     []string{"/bin/sh", "-c", "echo a0"},
		Volumes:     []string{"/t:/t", "/a:/a"},
		Environment: []string{"A=a0", "T=t0"},
		Pid:         toPtr(""),
		Namespaces:  &proto.Namespaces{Network: toPtr("host"), Pid: toPtr("")},
	}
	if d := cmp.Diff(want, resp, protocmp.Transform()); d != "" {
		t.Errorf("response (-want +got):\n%s", d)
	}
	m := backend.workflowV2.Status.Metadata
	if m.Task.ID != "t0" || m.Task.Hardware != "hw" || m.Action.ID != "id-a0" || m.Action.State != v1alpha2.StatePending {
		t.Errorf("current state = %+v", m)
	}
}

// TestNativeTaskHandOff verifies a task's last action hands the Workflow to the next task's Agent
// straight away, unless boot options must run in between, which the controller does in Post.
func TestNativeTaskHandOff(t *testing.T) {
	toggle := &v1alpha2.BootOptions{ToggleNetboot: true}
	tests := map[string]struct {
		first, second *v1alpha2.BootOptions
		wantState     v1alpha2.State
		wantAgent     string
	}{
		"no boot options":                 {wantState: v1alpha2.StateRunning, wantAgent: "agent-2"},
		"first task undoes its netboot":   {first: toggle, wantState: v1alpha2.StatePost, wantAgent: "agent-1"},
		"second task prepares its boot":   {second: toggle, wantState: v1alpha2.StatePost, wantAgent: "agent-1"},
		"netboot needs nothing undone":    {first: &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeNetboot}, wantState: v1alpha2.StateRunning, wantAgent: "agent-2"},
		"isoboot ejects after the task":   {first: &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeIsoboot, ISOURL: "http://x/y.iso"}, wantState: v1alpha2.StatePost, wantAgent: "agent-1"},
		"second task boots in customboot": {second: &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeCustomboot}, wantState: v1alpha2.StatePost, wantAgent: "agent-1"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			wf := nativeWorkflow(tt.first, tt.second)
			wf.Status.Metadata.Workflow.State = v1alpha2.StateRunning
			backend := &mockBackendReadWriter{workflowV2: wf}
			h := &Handler{Logger: logr.Discard(), Backend: backend}
			if _, err := h.ReportActionStatus(context.Background(), report("agent-1", "t0", "id-a0", "a0", proto.ActionStatusRequest_SUCCESS, "")); err != nil {
				t.Fatal(err)
			}
			got := backend.workflowV2.Status
			if got.Metadata.Workflow.State != tt.wantState || got.Metadata.Task.AgentID != tt.wantAgent {
				t.Errorf("state %s on %s, want %s on %s", got.Metadata.Workflow.State, got.Metadata.Task.AgentID, tt.wantState, tt.wantAgent)
			}
			if tt.wantState == v1alpha2.StatePost && (got.Metadata.Task.ID != "t0" || got.RenderedTasks[0].Metadata.State != v1alpha2.StatePost) {
				t.Errorf("current task %+v, want the first task, in Post", got.Metadata.Task)
			}
			if tt.wantState == v1alpha2.StateRunning && (got.Metadata.Task.ID != "t1" || got.RenderedTasks[0].Metadata.State != v1alpha2.StateSuccess) {
				t.Errorf("current task %+v, want the second task, the first a success", got.Metadata.Task)
			}
		})
	}
}

// TestEnrollmentWaitsForAssignedWorkflow verifies an Agent that a Workflow assigns a task to, but that is
// not running it yet, is not enrolled into a second Workflow.
func TestEnrollmentWaitsForAssignedWorkflow(t *testing.T) {
	tests := map[string]struct {
		state      v1alpha2.State
		wantEnroll bool
	}{
		"not rendered yet": {state: 0},
		"another task":     {state: v1alpha2.StateRunning},
		"finished":         {state: v1alpha2.StateSuccess, wantEnroll: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assigned := v1alpha2.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "assigned", Namespace: "default"}}
			assigned.Status.Metadata.Workflow.State = tt.state
			var enrolled bool
			caps := &mockAutoCapabilities{
				ListWorkflowRuleSetsFunc: func(context.Context, data.WorkflowFilter) ([]tinkerbell.WorkflowRuleSet, error) {
					enrolled = true
					return nil, nil
				},
			}
			backend := &mockBackendReadWriter{hardwareErr: notFoundError{}, taskAgentWorkflows: []v1alpha2.Workflow{assigned}}
			h := &Handler{
				Logger:           logr.Discard(),
				Backend:          backend,
				AutoCapabilities: AutoCapabilities{Enrollment: AutoEnrollment{Enabled: true, WorkflowRuleSetLister: caps, WorkflowCreator: caps}},
			}
			_, err := h.GetAction(context.Background(), &proto.ActionRequest{AgentId: toPtr("agent-1")})
			if status.Code(err) != codes.NotFound {
				t.Fatalf("err = %v, want NotFound", err)
			}
			if enrolled != tt.wantEnroll {
				t.Errorf("enrolled = %v, want %v", enrolled, tt.wantEnroll)
			}
			if backend.taskAgentLists != 1 {
				t.Errorf("task agent lists = %d, want 1", backend.taskAgentLists)
			}
		})
	}
}

// TestEnrollCompatibilityWorkflow verifies rule set enrollment stores the Workflow exactly as the v1alpha1
// Workflow it created before would have been stored: in v1alpha1 compatibility mode.
func TestEnrollCompatibilityWorkflow(t *testing.T) {
	var created *v1alpha2.Workflow
	caps := &captureCreator{created: &created, rs: []tinkerbell.WorkflowRuleSet{{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "tink", UID: "rs-uid"},
		Spec: tinkerbell.WorkflowRuleSetSpec{
			Rules: []string{`{"chassis": {"serial": ["12345"]}}`},
			Workflow: tinkerbell.WorkflowRuleSetWorkflow{
				Disabled: toPtr(true),
				Template: tinkerbell.TemplateConfig{Ref: "provision", AgentValue: "device_1", KVs: map[string]string{"site": "lab"}},
			},
		},
	}}}
	h := &Handler{
		Logger:           logr.Discard(),
		Backend:          &mockBackendReadWriter{},
		AutoCapabilities: AutoCapabilities{Enrollment: AutoEnrollment{Enabled: true, WorkflowRuleSetLister: caps, WorkflowCreator: caps}},
	}
	hw := toV2Hardware(&tinkerbell.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw", Namespace: "tink"},
		Spec:       tinkerbell.HardwareSpec{Auto: tinkerbell.AutoCapabilities{EnrollmentEnabled: true}},
	})
	attr := convert(&proto.AgentAttributes{Chassis: &proto.Chassis{Serial: toPtr("12345")}})
	if err := h.enroll(context.Background(), "agent-1", attr, hw); status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound until the Workflow renders", err)
	}
	if created == nil {
		t.Fatal("no Workflow created")
	}

	// The v1alpha2 Workflow runs the Template in compatibility mode.
	if created.Annotations[conversion.AnnotationTemplate] != "provision" || len(created.Spec.Tasks) != 1 || created.Spec.Tasks[0].TaskRef.Name != "" {
		t.Errorf("not a compatibility mode Workflow: %+v", created)
	}
	if ref := created.Spec.Tasks[0].Hardware.HardwareRef; ref == nil || ref.Name != "hw" || ref.Namespace != "tink" {
		t.Errorf("hardwareRef = %+v", ref)
	}

	// v1alpha1 clients read the Workflow the v1alpha1 server created.
	want := &tinkerbell.Workflow{
		TypeMeta: metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha1", Kind: "Workflow"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "enrollment-agent-1", Namespace: "tink",
			Labels:          map[string]string{"tinkerbell.org/auto-enrollment": "true"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "tinkerbell.org/v1alpha1", Kind: "WorkflowRuleSet", Name: "rs", UID: "rs-uid"}},
		},
		Spec: tinkerbell.WorkflowSpec{
			TemplateRef: "provision",
			HardwareRef: "hw",
			Disabled:    toPtr(true),
			HardwareMap: map[string]string{"device_1": "agent-1", "site": "lab"},
		},
	}
	if d := cmp.Diff(want, toV1Workflow(created), cmpopts.EquateEmpty()); d != "" {
		t.Errorf("Workflow as v1alpha1 (-want +got):\n%s", d)
	}
}

type captureCreator struct {
	created **v1alpha2.Workflow
	rs      []tinkerbell.WorkflowRuleSet
}

func (c *captureCreator) ListWorkflowRuleSets(context.Context, data.WorkflowFilter) ([]tinkerbell.WorkflowRuleSet, error) {
	return c.rs, nil
}

func (c *captureCreator) CreateWorkflowV1Alpha2(_ context.Context, wf *v1alpha2.Workflow) error {
	*c.created = wf.DeepCopy()
	return nil
}

// TestDiscoverV1Alpha1View verifies discovered Hardware reads through v1alpha1 as the Hardware the
// v1alpha1 server discovered: an interface per MAC address, which does not netboot, and the disks.
func TestDiscoverV1Alpha1View(t *testing.T) {
	var created *v1alpha2.Hardware
	h := &Handler{
		Logger: logr.Discard(),
		AutoCapabilities: AutoCapabilities{Discovery: AutoDiscovery{
			Enabled: true, Namespace: "tink",
			HardwareFilterer: &mockBackendReadWriter{hardwareErr: notFoundError{}},
			HardwareCreator: createHardwareFunc(func(_ context.Context, hw *v1alpha2.Hardware) error {
				created = hw.DeepCopy()
				return nil
			}),
		}},
	}
	attrs := convert(&proto.AgentAttributes{
		Network: []*proto.Network{{Name: toPtr("eth0"), Mac: toPtr("00:11:22:AA:BB:CC")}, {Name: toPtr("ib0"), Mac: toPtr("00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01")}},
		Block:   []*proto.Block{{Name: toPtr("sda"), SizeBytes: toPtr(int64(1 << 30))}, {Name: toPtr("loop0")}},
	})
	if _, err := h.Discover(context.Background(), "agent-1", attrs); err != nil {
		t.Fatal(err)
	}
	if created == nil {
		t.Fatal("no Hardware created")
	}
	v1 := &tinkerbell.Hardware{}
	if err := conversion.HardwareToV1Alpha1(created, v1); err != nil {
		t.Fatal(err)
	}
	if len(v1.Spec.Interfaces) != 1 || v1.Spec.Interfaces[0].DHCP == nil || v1.Spec.Interfaces[0].DHCP.MAC != "00:11:22:aa:bb:cc" {
		t.Fatalf("interfaces = %+v, want the 48-bit MAC address, in lower case", v1.Spec.Interfaces)
	}
	if nb := v1.Spec.Interfaces[0].Netboot; nb != nil && nb.AllowPXE != nil && *nb.AllowPXE {
		t.Errorf("netboot = %+v, want it not allowed", nb)
	}
	if d := cmp.Diff([]tinkerbell.Disk{{Device: "/dev/sda"}}, v1.Spec.Disks); d != "" {
		t.Errorf("disks (-want +got):\n%s", d)
	}
	if v1.Spec.AgentID != "agent-1" || v1.Labels["tinkerbell.org/auto-discovered"] != "true" {
		t.Errorf("hardware = %+v", v1.ObjectMeta)
	}
}
