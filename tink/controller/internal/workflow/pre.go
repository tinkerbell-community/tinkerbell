package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// currentTask returns the index of the rendered task the Workflow is on: the current task, or before
// any task has been served, the first one with something to run.
func (s *state) currentTask() int {
	if j := engine.TaskIndex(s.workflow, s.workflow.Status.Metadata.Task.ID); j >= 0 {
		return j
	}
	return max(engine.NextTask(s.workflow, -1), 0)
}

// renderedBoot returns boot with its templates rendered for rendered task j.
func (s *state) renderedBoot(ctx context.Context, j int, pick func(*v1alpha2.Workflow, int) *engine.Boot) (*engine.Boot, error) {
	rw, err := s.render(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("rendering the boot options of task %d: %w", j, err)
	}
	return pick(rw, j), nil
}

// prepareWorkflow runs the boot options of the current task before its actions are served.
// The workflow (s.workflow) can be updated even if an error occurs.
// Any patching of the workflow object in a cluster is left up to the caller.
// Boot options require the task have a hardwareRef and the object exists.
func (s *state) prepareWorkflow(ctx context.Context) (reconcile.Result, error) {
	j := s.currentTask()
	// Before anything ran the Workflow waits for its first action, otherwise it continues running.
	next := v1alpha2.StateRunning
	if s.workflow.Status.Metadata.Action.ID == "" {
		next = v1alpha2.StatePending
	}
	if !engine.BootBefore(s.workflow, j).Pre() {
		s.workflow.Status.Metadata.Workflow.State = next
		return reconcile.Result{}, nil
	}
	boot, err := s.renderedBoot(ctx, j, engine.BootBefore)
	if err != nil {
		s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
			Type:    v1alpha2.BootJobSetupFailed,
			Status:  metav1.ConditionFalse,
			Reason:  reasonError,
			Message: err.Error(),
			Time:    &metav1.Time{Time: metav1.Now().UTC()},
		})
		return reconcile.Result{}, err
	}
	bo := boot.Options

	// handle bootoptions
	// 1. Handle toggling netboot in a hardware object if toggleNetboot is true.
	if bo.ToggleNetboot && !s.workflow.Status.BootOptions.AllowNetboot.ToggledTrue {
		journal.Log(ctx, "toggling allowPXE true")
		if err := s.toggleHardware(ctx, boot.HardwareRef, true); err != nil {
			s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
			return reconcile.Result{}, err
		}
	}

	done := func(name jobName) {
		if s.workflow.Status.BootOptions.Jobs[name.String()].Complete && s.workflow.Status.Metadata.Workflow.State == v1alpha2.StatePreparing {
			s.workflow.Status.Metadata.Workflow.State = next
		}
	}

	// 2. Handle booting scenarios.
	switch bo.BootMode {
	case v1alpha2.BootModeNetboot:
		name := jobNameFor(jobNameNetboot, s.workflow.GetName(), boot.Index)
		if js := s.workflow.Status.BootOptions.Jobs[name.String()]; !js.ExistingJobDeleted || js.UID == "" || !js.Complete {
			journal.Log(ctx, "boot mode netboot")
			hw, err := hardwareFrom(ctx, s.client, s.workflow, boot.HardwareRef)
			if err != nil {
				// update a condition to indicate the error
				s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
					Type:    v1alpha2.BootJobSetupFailed,
					Status:  metav1.ConditionFalse,
					Reason:  reasonError,
					Message: fmt.Sprintf("failed to get hardware: %s", err.Error()),
					Time:    &metav1.Time{Time: metav1.Now().UTC()},
				})
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, fmt.Errorf("failed to get hardware: %w", err)
			}
			ops := []bmc.Operations{
				{PowerAction: valueToPointer(bmc.PowerActionHardOff)},
				{BootDevice: &bmc.BootDeviceConfig{Name: bmc.BootDevicePXE, EFIBoot: s.efiBoot(hw)}},
				{PowerAction: valueToPointer(bmc.PowerActionOn)},
			}

			r, err := s.handleJob(ctx, boot.HardwareRef, ops, name)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return r, err
			}
			done(name)
			return r, nil
		}
		// what do i set the state to? I think if we get here then the preparing was successful
	case v1alpha2.BootModeIsoboot:
		name := jobNameFor(jobNameISOMount, s.workflow.GetName(), boot.Index)
		if js := s.workflow.Status.BootOptions.Jobs[name.String()]; !js.ExistingJobDeleted || js.UID == "" || !js.Complete {
			journal.Log(ctx, "boot mode isoboot")
			if bo.ISOURL == "" {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, errors.New("iso url must be a valid url")
			}
			hw, err := hardwareFrom(ctx, s.client, s.workflow, boot.HardwareRef)
			if err != nil {
				// update a condition to indicate the error
				s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
					Type:    v1alpha2.BootJobSetupComplete,
					Status:  metav1.ConditionFalse,
					Reason:  reasonError,
					Message: fmt.Sprintf("failed to get hardware: %s", err.Error()),
					Time:    &metav1.Time{Time: metav1.Now().UTC()},
				})
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, fmt.Errorf("failed to get hardware: %w", err)
			}
			ops := []bmc.Operations{
				{PowerAction: valueToPointer(bmc.PowerActionHardOff)},
				// empty to unmount/eject the media
				{VirtualMediaAction: &bmc.VirtualMediaAction{MediaURL: "", Kind: bmc.VirtualMediaCD}},
				{VirtualMediaAction: &bmc.VirtualMediaAction{MediaURL: bo.ISOURL, Kind: bmc.VirtualMediaCD}},
				{BootDevice: &bmc.BootDeviceConfig{Name: bmc.BootDeviceCDROM, EFIBoot: s.efiBoot(hw)}},
				{PowerAction: valueToPointer(bmc.PowerActionOn)},
			}

			r, err := s.handleJob(ctx, boot.HardwareRef, ops, name)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return r, err
			}
			done(name)
			return r, nil
		}
		// what do i set the state to? I think if we get here then the preparing was successful
	case v1alpha2.BootModeCustomboot:
		name := jobNameFor(jobNameCustombootPreparing, s.workflow.GetName(), boot.Index)
		if js := s.workflow.Status.BootOptions.Jobs[name.String()]; !js.ExistingJobDeleted || js.UID == "" || !js.Complete {
			journal.Log(ctx, "boot mode customboot preparing")
			hw, err := hardwareFrom(ctx, s.client, s.workflow, boot.HardwareRef)
			if err != nil {
				s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
					Type:    v1alpha2.BootJobSetupFailed,
					Status:  metav1.ConditionFalse,
					Reason:  reasonError,
					Message: fmt.Sprintf("failed to get hardware: %s", err.Error()),
					Time:    &metav1.Time{Time: metav1.Now().UTC()},
				})
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, fmt.Errorf("failed to get hardware: %w", err)
			}
			var pre []bmc.Operations
			if bo.Customboot != nil {
				pre = bo.Customboot.PreOperations
			}
			ops, err := s.templateOperations(pre, hw)
			if err != nil {
				s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
					Type:    v1alpha2.BootJobSetupFailed,
					Status:  metav1.ConditionFalse,
					Reason:  reasonError,
					Message: fmt.Sprintf("failed to template actions: %s", err.Error()),
					Time:    &metav1.Time{Time: metav1.Now().UTC()},
				})
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return reconcile.Result{}, fmt.Errorf("failed to template actions: %w", err)
			}
			r, err := s.handleJob(ctx, boot.HardwareRef, ops, name)
			if err != nil {
				s.workflow.Status.Metadata.Workflow.State = v1alpha2.StateFailed
				return r, err
			}
			done(name)
			return r, nil
		}
	default:
		s.workflow.Status.Metadata.Workflow.State = next
	}

	return reconcile.Result{}, nil
}

// efiBoot reports whether the Hardware boots with UEFI. Hardware created through v1alpha1 says so with
// dhcp.uefi on an interface, as v1alpha1 boot Jobs used; other Hardware with spec.bmc.bootMode, UEFI unless
// it is BIOS.
func (s *state) efiBoot(hw *v1alpha2.Hardware) bool {
	if engine.Compat(s.workflow) {
		v1 := &v1alpha1.Hardware{}
		if err := conversion.HardwareToV1Alpha1(hw, v1); err == nil {
			for _, iface := range v1.Spec.Interfaces {
				if iface.DHCP != nil && iface.DHCP.UEFI {
					return true
				}
			}
		}
		return false
	}
	return hw.Spec.BMC == nil || hw.Spec.BMC.BootMode != v1alpha2.FirmwareModeBIOS
}

// templateData holds data available for templating in customboot actions.
type templateData struct {
	// Hardware embeds HardwareSpec to expose Interfaces at the top level.
	Hardware struct {
		v1alpha1.HardwareSpec
	}
}

// templateOperations returns the customboot operations to run. In v1alpha1 compatibility mode the
// VirtualMediaAction.MediaURL of each is templated with the v1alpha1 Hardware, as v1alpha1 customboot
// actions were. Native Workflows render their boot options with the rest of the Workflow.
//
// Available template data in compatibility mode:
//   - {{ (index .Hardware.Interfaces 0).DHCP.MAC }}: MAC address (colon format, e.g., "52:54:00:12:34:01")
//   - {{ (index .Hardware.Interfaces 0).DHCP.MAC | replace ":" "-" }}: MAC in dash format (e.g., "52-54-00-12-34-01")
func (s *state) templateOperations(ops []bmc.Operations, hw *v1alpha2.Hardware) ([]bmc.Operations, error) {
	if !engine.Compat(s.workflow) || hw == nil {
		return ops, nil
	}
	v1 := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, v1); err != nil {
		return nil, fmt.Errorf("converting hardware to v1alpha1: %w", err)
	}
	return templateActions(ops, v1)
}

// templateActions processes BMC operations and templates any string fields that contain Go template syntax.
// Currently supports templating of VirtualMediaAction.MediaURL field.
func templateActions(ops []bmc.Operations, hw *v1alpha1.Hardware) ([]bmc.Operations, error) {
	if hw == nil {
		return ops, nil
	}

	// Build template data by embedding HardwareSpec
	data := templateData{}
	data.Hardware.HardwareSpec = hw.Spec

	// Process each operation
	result := make([]bmc.Operations, len(ops))
	for i, op := range ops {
		result[i] = op

		// Template VirtualMediaAction.MediaURL if present
		if op.VirtualMediaAction != nil {
			templated, err := templateString(op.VirtualMediaAction.MediaURL, data)
			if err != nil {
				return nil, fmt.Errorf("failed to template VirtualMediaAction.MediaURL: %w", err)
			}
			// Create a copy to avoid modifying the original
			vmAction := *op.VirtualMediaAction
			vmAction.MediaURL = templated
			result[i].VirtualMediaAction = &vmAction
		}
	}

	return result, nil
}

// templateString executes a Go template string with the provided data.
func templateString(tmplStr string, data templateData) (string, error) {
	rendered, err := renderTemplate("action", tmplStr, data)
	if err != nil {
		return "", err
	}

	return string(rendered), nil
}
