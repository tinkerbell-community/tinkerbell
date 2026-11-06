package workflow

import (
	"context"
	"errors"
	"fmt"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// postTask returns the index of the rendered task whose post boot options run: the current task, or the
// last task when there is none.
func (s *state) postTask() int {
	if j := engine.TaskIndex(s.workflow, s.workflow.Status.Metadata.Task.ID); j >= 0 {
		return j
	}
	return len(s.workflow.Status.RenderedTasks) - 1
}

// postActions runs the boot options of the task whose actions all succeeded, then moves the Workflow on
// to its next task, or to its final state.
func (s *state) postActions(ctx context.Context) (reconcile.Result, error) {
	j := s.postTask()
	if !engine.BootAfter(s.workflow, j).Post() {
		s.finishTask(j)
		return reconcile.Result{}, nil
	}
	boot, err := s.renderedBoot(ctx, j, engine.BootAfter)
	if err != nil {
		return reconcile.Result{}, err
	}
	bo := boot.Options

	// 1. Handle toggling netboot in a hardware object if toggleNetboot is true.
	if bo.ToggleNetboot && !s.workflow.Status.BootOptions.AllowNetboot.ToggledFalse {
		journal.Log(ctx, "toggling allowPXE false")
		if err := s.toggleHardware(ctx, boot.HardwareRef, false); err != nil {
			s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
			// TODO: add a status field for why the post Action failed.
			return reconcile.Result{}, err
		}
	}

	// 2. Handle ISO eject scenario.
	switch bo.BootMode {
	case v1alpha2.BootModeIsoboot:
		name := jobNameFor(jobNameISOEject, s.workflow.GetName(), boot.Index)
		if js := s.workflow.Status.BootOptions.Jobs[name.String()]; !js.ExistingJobDeleted || js.UID == "" || !js.Complete {
			journal.Log(ctx, "boot mode isoboot")
			if bo.ISOURL == "" {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, errors.New("iso url must be a valid url")
			}
			ops := []bmc.Operations{
				// empty to unmount/eject the media
				{VirtualMediaAction: &bmc.VirtualMediaAction{MediaURL: "", Kind: bmc.VirtualMediaCD}},
			}

			r, err := s.handleJob(ctx, boot.HardwareRef, ops, name)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return r, err
			}
			if s.workflow.Status.BootOptions.Jobs[name.String()].Complete {
				// Post Action handling must only change the state if it was not a failure state (i.e. not FAILED, TIMEOUT).
				s.finishTask(j)
			}
			return r, nil
		}
	case v1alpha2.BootModeCustomboot:
		name := jobNameFor(jobNameCustombootPost, s.workflow.GetName(), boot.Index)
		if js := s.workflow.Status.BootOptions.Jobs[name.String()]; !js.ExistingJobDeleted || js.UID == "" || !js.Complete {
			journal.Log(ctx, "boot mode customboot post")
			hw, err := hardwareFrom(ctx, s.client, s.workflow, boot.HardwareRef)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, fmt.Errorf("failed to get hardware: %w", err)
			}
			var post []bmc.Operations
			if bo.Customboot != nil {
				post = bo.Customboot.PostOperations
			}
			ops, err := s.templateOperations(post, hw)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, fmt.Errorf("failed to template post actions: %w", err)
			}
			r, err := s.handleJob(ctx, boot.HardwareRef, ops, name)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return r, err
			}
			if s.workflow.Status.BootOptions.Jobs[name.String()].Complete || len(post) == 0 {
				// Post Action handling must only change the state if it was not a failure state (i.e. not FAILED, TIMEOUT).
				s.finishTask(j)
			}
			return r, nil
		}
	case v1alpha2.BootModeNetboot:
		// Nothing to do here for netboot mode.
	}

	s.finishTask(j)
	return reconcile.Result{}, nil
}

// finishTask completes rendered task j once its post boot options ran: the Workflow moves to its next
// task, through Preparing when that task has boot options, or, after the last, takes the state of its last
// Action.
func (s *state) finishTask(j int) {
	wf := s.workflow
	next := engine.Advance(wf, j)
	if next < 0 {
		if st := wf.Status.Metadata.Action.State; st != 0 {
			wf.Status.Metadata.Workflow.State = st
		}
		return
	}
	if engine.BootBefore(wf, next).Pre() {
		// The next task's netboot toggling is its own.
		wf.Status.BootOptions.AllowNetboot = v1alpha2.AllowNetbootStatus{}
		wf.Status.Metadata.Workflow.State = v1alpha2.StatePreparing
		return
	}
	wf.Status.Metadata.Workflow.State = v1alpha2.StateRunning
}
