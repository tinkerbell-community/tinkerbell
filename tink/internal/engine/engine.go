// Package engine holds the rules of v1alpha2 Workflow progress that the Tink
// controller and the Tink server share: which mode a Workflow runs in, which
// boot options apply around each rendered task, and how a Workflow moves from
// one task to the next.
//
// A Workflow's status.renderedTasks are its tasks, rendered. Natively, rendered
// task j is spec.tasks[j], with that task's Agent, Hardware and boot options. A
// Workflow created through v1alpha1 that runs its Template in v1alpha1
// compatibility mode has a single spec.tasks entry holding the Hardware and boot
// options of the whole Workflow, and the rendered tasks are the Template's
// tasks: as in v1alpha1, its boot options apply before the first and after the
// last of them.
package engine

import (
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"k8s.io/apimachinery/pkg/types"
)

// Compat reports whether wf runs in v1alpha1 compatibility mode: it was created
// through v1alpha1 and names a Template, and no task references a Task.
func Compat(wf *v1alpha2.Workflow) bool {
	if wf.Annotations[conversion.AnnotationTemplate] == "" {
		return false
	}
	for _, t := range wf.Spec.Tasks {
		if t.TaskRef.Name != "" {
			return false
		}
	}
	return true
}

// Boot is the boot options a task applies to its Hardware.
type Boot struct {
	// Index is the spec.tasks entry the options come from.
	Index int
	// Options are the boot options.
	Options v1alpha2.BootOptions
	// HardwareRef is the Hardware they apply to, its namespace defaulted to the
	// Workflow's.
	HardwareRef v1alpha2.SimpleReference
}

// Pre reports whether b has work to do before its task runs: toggling netboot
// on, or a boot mode.
func (b *Boot) Pre() bool {
	return b != nil && (b.Options.ToggleNetboot || b.Options.BootMode != "")
}

// Post reports whether b has work to do after its task succeeds: toggling
// netboot off, ejecting an ISO, or customboot post operations.
func (b *Boot) Post() bool {
	if b == nil {
		return false
	}
	switch b.Options.BootMode {
	case v1alpha2.BootModeIsoboot:
		return true
	case v1alpha2.BootModeCustomboot:
		if b.Options.Customboot != nil && len(b.Options.Customboot.PostOperations) > 0 {
			return true
		}
	case v1alpha2.BootModeNetboot:
		// Netbooting needs nothing undone.
	}
	return b.Options.ToggleNetboot
}

// BootBefore returns the boot options to apply before rendered task j, or nil.
func BootBefore(wf *v1alpha2.Workflow, j int) *Boot {
	if Compat(wf) {
		// Before the first task that runs anything.
		first := 0
		for i := range wf.Status.RenderedTasks {
			if len(wf.Status.RenderedTasks[i].Actions) > 0 {
				first = i
				break
			}
		}
		if j != first {
			return nil
		}
		return bootOf(wf, 0)
	}
	return bootOf(wf, j)
}

// BootAfter returns the boot options to apply after rendered task j succeeds,
// or nil.
func BootAfter(wf *v1alpha2.Workflow, j int) *Boot {
	if Compat(wf) {
		// After the last task that runs anything.
		for i := j + 1; i < len(wf.Status.RenderedTasks); i++ {
			if len(wf.Status.RenderedTasks[i].Actions) > 0 {
				return nil
			}
		}
		return bootOf(wf, 0)
	}
	return bootOf(wf, j)
}

func bootOf(wf *v1alpha2.Workflow, i int) *Boot {
	if i < 0 || i >= len(wf.Spec.Tasks) {
		return nil
	}
	h := wf.Spec.Tasks[i].Hardware
	if h == nil || h.BootOptions == nil || h.HardwareRef == nil {
		return nil
	}
	return &Boot{Index: i, Options: *h.BootOptions.DeepCopy(), HardwareRef: defaultNamespace(*h.HardwareRef, wf.Namespace)}
}

// HardwareRef returns the Hardware rendered task j runs against, its namespace
// defaulted to the Workflow's, or nil when it has none.
func HardwareRef(wf *v1alpha2.Workflow, j int) *v1alpha2.SimpleReference {
	i := j
	if Compat(wf) {
		i = 0
	}
	if i < 0 || i >= len(wf.Spec.Tasks) {
		return nil
	}
	h := wf.Spec.Tasks[i].Hardware
	if h == nil || h.HardwareRef == nil || h.HardwareRef.Name == "" {
		return nil
	}
	ref := defaultNamespace(*h.HardwareRef, wf.Namespace)
	return &ref
}

func defaultNamespace(ref v1alpha2.SimpleReference, namespace string) v1alpha2.SimpleReference {
	if ref.Namespace == "" {
		ref.Namespace = namespace
	}
	return ref
}

// TaskDone reports whether a rendered task has nothing left to run: it has no
// actions, or its last action succeeded.
func TaskDone(t *v1alpha2.TaskWithMetadata) bool {
	return len(t.Actions) == 0 || t.Actions[len(t.Actions)-1].Metadata.State == v1alpha2.StateSuccess
}

// NextTask returns the index of the first rendered task after task after that
// has something to run, or -1. NextTask(wf, -1) is the first one.
func NextTask(wf *v1alpha2.Workflow, after int) int {
	for i := after + 1; i < len(wf.Status.RenderedTasks); i++ {
		if !TaskDone(&wf.Status.RenderedTasks[i]) {
			return i
		}
	}
	return -1
}

// TaskIndex returns the index of the rendered task with id, or -1.
func TaskIndex(wf *v1alpha2.Workflow, id types.UID) int {
	if id == "" {
		return -1
	}
	for i := range wf.Status.RenderedTasks {
		if wf.Status.RenderedTasks[i].Metadata.ID == id {
			return i
		}
	}
	return -1
}

// Advance marks rendered task j a success and makes the next task with
// something to run the current one, on its Agent. It returns that task's
// index, or -1 when j was the last; then the current task is left as it is.
func Advance(wf *v1alpha2.Workflow, j int) int {
	if j >= 0 && j < len(wf.Status.RenderedTasks) {
		wf.Status.RenderedTasks[j].Metadata.State = v1alpha2.StateSuccess
	}
	next := NextTask(wf, j)
	if next < 0 {
		if wf.Status.Metadata.Task.ID != "" && j >= 0 && j < len(wf.Status.RenderedTasks) && wf.Status.Metadata.Task.ID == wf.Status.RenderedTasks[j].Metadata.ID {
			wf.Status.Metadata.Task.State = v1alpha2.StateSuccess
		}
		return -1
	}
	t := wf.Status.RenderedTasks[next]
	wf.Status.Metadata.Task = v1alpha2.Metadata{
		AgentID:  t.Metadata.AgentID,
		ID:       t.Metadata.ID,
		Name:     t.Name,
		Hardware: t.Metadata.Hardware,
		State:    v1alpha2.StatePending,
	}
	return next
}
