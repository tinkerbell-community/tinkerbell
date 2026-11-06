package engine

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func task(id, agent string, states ...v1alpha2.State) v1alpha2.TaskWithMetadata {
	t := v1alpha2.TaskWithMetadata{Name: "task-" + id, Metadata: v1alpha2.Metadata{ID: types.UID("id-" + id), AgentID: agent}}
	for _, s := range states {
		t.Actions = append(t.Actions, v1alpha2.ActionWithMetadata{Metadata: v1alpha2.Metadata{State: s}})
	}
	return t
}

var (
	hw       = &v1alpha2.SimpleReference{Name: "hw"}
	toggle   = &v1alpha2.BootOptions{ToggleNetboot: true}
	netboot  = &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeNetboot}
	isoboot  = &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeIsoboot, ISOURL: "http://example.com/x.iso"}
	pending  = v1alpha2.StatePending
	success  = v1alpha2.StateSuccess
	running  = v1alpha2.StateRunning
	compatWF = func(bo *v1alpha2.BootOptions, tasks ...v1alpha2.TaskWithMetadata) *v1alpha2.Workflow {
		return &v1alpha2.Workflow{
			ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "ns", Annotations: map[string]string{conversion.AnnotationTemplate: "tpl"}},
			Spec:       v1alpha2.WorkflowSpec{Tasks: []v1alpha2.WorkflowTask{{Hardware: &v1alpha2.WorkflowHardware{HardwareRef: hw, BootOptions: bo}}}},
			Status:     v1alpha2.WorkflowStatus{RenderedTasks: tasks},
		}
	}
)

func TestCompat(t *testing.T) {
	wf := compatWF(nil)
	if !Compat(wf) {
		t.Error("a Workflow created through v1alpha1 without Task references runs in compatibility mode")
	}
	wf.Spec.Tasks[0].TaskRef.Name = "task"
	if Compat(wf) {
		t.Error("a Workflow referencing Tasks is native")
	}
	if Compat(&v1alpha2.Workflow{}) {
		t.Error("a Workflow without a Template is native")
	}
}

func TestBootCompat(t *testing.T) {
	// v1alpha1 boot options apply before the first and after the last task that runs anything.
	wf := compatWF(toggle, task("0", "a"), task("1", "a", pending), task("2", "b", pending), task("3", "b"))
	for j, want := range []bool{false, true, false, false} {
		if got := BootBefore(wf, j) != nil; got != want {
			t.Errorf("BootBefore(%d) = %v, want %v", j, got, want)
		}
	}
	for j, want := range []bool{false, false, true, true} {
		if got := BootAfter(wf, j) != nil; got != want {
			t.Errorf("BootAfter(%d) = %v, want %v", j, got, want)
		}
	}
	b := BootBefore(wf, 1)
	if b.Index != 0 || b.HardwareRef != (v1alpha2.SimpleReference{Name: "hw", Namespace: "ns"}) || !b.Options.ToggleNetboot {
		t.Errorf("boot = %+v", b)
	}
	// Every rendered task runs against the Workflow's Hardware.
	if ref := HardwareRef(wf, 2); ref == nil || ref.Name != "hw" || ref.Namespace != "ns" {
		t.Errorf("HardwareRef(2) = %+v", ref)
	}
}

func TestBootNative(t *testing.T) {
	wf := &v1alpha2.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "ns"},
		Spec: v1alpha2.WorkflowSpec{Tasks: []v1alpha2.WorkflowTask{
			{TaskRef: v1alpha2.SimpleReference{Name: "t0"}},
			{TaskRef: v1alpha2.SimpleReference{Name: "t1"}, Hardware: &v1alpha2.WorkflowHardware{HardwareRef: &v1alpha2.SimpleReference{Name: "hw1", Namespace: "ns"}, BootOptions: netboot}},
			{TaskRef: v1alpha2.SimpleReference{Name: "t2"}, Hardware: &v1alpha2.WorkflowHardware{HardwareRef: hw}},
		}},
		Status: v1alpha2.WorkflowStatus{RenderedTasks: []v1alpha2.TaskWithMetadata{task("0", "a", pending), task("1", "b", pending), task("2", "c", pending)}},
	}
	if BootBefore(wf, 0) != nil || BootAfter(wf, 0) != nil || HardwareRef(wf, 0) != nil {
		t.Error("task 0 has no Hardware")
	}
	if b := BootBefore(wf, 1); b == nil || b.Index != 1 || b.HardwareRef.Name != "hw1" {
		t.Errorf("BootBefore(1) = %+v", b)
	}
	if !BootBefore(wf, 1).Pre() || BootAfter(wf, 1).Post() {
		t.Error("netboot boots before its task and leaves nothing to undo after it")
	}
	if BootBefore(wf, 2) != nil {
		t.Error("task 2 has Hardware but no boot options")
	}
	if ref := HardwareRef(wf, 2); ref == nil || ref.Namespace != "ns" {
		t.Errorf("HardwareRef(2) = %+v, want the Workflow's namespace", ref)
	}
}

func TestPrePost(t *testing.T) {
	tests := map[string]struct {
		opts      *v1alpha2.BootOptions
		pre, post bool
	}{
		"toggle":                 {opts: toggle, pre: true, post: true},
		"netboot":                {opts: netboot, pre: true},
		"isoboot":                {opts: isoboot, pre: true, post: true},
		"customboot without ops": {opts: &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeCustomboot}, pre: true},
		"customboot with post ops": {
			opts: &v1alpha2.BootOptions{BootMode: v1alpha2.BootModeCustomboot, Customboot: &v1alpha2.Customboot{PostOperations: make([]bmc.Operations, 1)}},
			pre:  true, post: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := &Boot{Options: *tt.opts}
			if b.Pre() != tt.pre || b.Post() != tt.post {
				t.Errorf("Pre, Post = %v, %v; want %v, %v", b.Pre(), b.Post(), tt.pre, tt.post)
			}
		})
	}
	var none *Boot
	if none.Pre() || none.Post() {
		t.Error("no boot options have no work")
	}
}

func TestAdvance(t *testing.T) {
	wf := compatWF(nil, task("0", "a", success, success), task("1", "b"), task("2", "c", pending))
	wf.Status.Metadata.Task = v1alpha2.Metadata{AgentID: "a", ID: "id-0", Name: "task-0", State: running}
	if got := NextTask(wf, -1); got != 2 {
		t.Fatalf("NextTask(-1) = %d, want 2: task 0 is done and task 1 runs nothing", got)
	}
	if got := Advance(wf, 0); got != 2 {
		t.Fatalf("Advance(0) = %d, want 2", got)
	}
	want := v1alpha2.Metadata{AgentID: "c", ID: "id-2", Name: "task-2", State: pending}
	if d := cmp.Diff(want, wf.Status.Metadata.Task); d != "" {
		t.Errorf("current task (-want +got):\n%s", d)
	}
	if wf.Status.RenderedTasks[0].Metadata.State != success {
		t.Error("the finished task is not marked a success")
	}
	if TaskIndex(wf, "id-2") != 2 || TaskIndex(wf, "") != -1 || TaskIndex(wf, "nope") != -1 {
		t.Error("TaskIndex")
	}

	// After the last task the current task stays, now a success.
	wf.Status.RenderedTasks[2].Actions[0].Metadata.State = success
	if got := Advance(wf, 2); got != -1 {
		t.Fatalf("Advance(2) = %d, want -1", got)
	}
	if wf.Status.Metadata.Task.ID != "id-2" || wf.Status.Metadata.Task.State != success {
		t.Errorf("current task = %+v, want task 2, a success", wf.Status.Metadata.Task)
	}
}
