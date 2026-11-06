package conversion

import (
	"maps"
	"reflect"
	"slices"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"k8s.io/apimachinery/pkg/types"
)

// AnnotationTemplate names the deprecated v1alpha1 Template, in the Workflow's
// namespace, that a v1alpha2 Workflow converted from v1alpha1 renders its
// Tasks from. Workflows with this annotation and no Task references are run
// in v1alpha1 compatibility mode: the Template is rendered with v1alpha1
// template data, as before.
const AnnotationTemplate = "tinkerbell.org/v1alpha1-template"

var stateToV2 = map[v1alpha1.WorkflowState]v1alpha2.State{
	v1alpha1.WorkflowStatePreparing: v1alpha2.StatePreparing,
	v1alpha1.WorkflowStatePending:   v1alpha2.StatePending,
	v1alpha1.WorkflowStateRunning:   v1alpha2.StateRunning,
	v1alpha1.WorkflowStatePost:      v1alpha2.StatePost,
	v1alpha1.WorkflowStateSuccess:   v1alpha2.StateSuccess,
	v1alpha1.WorkflowStateFailed:    v1alpha2.StateFailed,
	v1alpha1.WorkflowStateTimeout:   v1alpha2.StateTimeout,
}

// StateToV1Alpha1 returns the v1alpha1 name of a v1alpha2 State.
func StateToV1Alpha1(s v1alpha2.State) v1alpha1.WorkflowState {
	for k, v := range stateToV2 {
		if v == s {
			return k
		}
	}
	return ""
}

// StateToV1Alpha2 returns the v1alpha2 State of a v1alpha1 state name. The
// deprecated "STATE_PENDING" maps to pending.
func StateToV1Alpha2(s v1alpha1.WorkflowState) v1alpha2.State {
	if s == "STATE_PENDING" {
		return v1alpha2.StatePending
	}
	return stateToV2[s]
}

// WorkflowToV1Alpha2 converts a v1alpha1 Workflow to v1alpha2.
func WorkflowToV1Alpha2(src *v1alpha1.Workflow, dst *v1alpha2.Workflow) error {
	dst.ObjectMeta = *src.ObjectMeta.DeepCopy()
	dst.SetGroupVersionKind(v1alpha2.GroupVersion.WithKind("Workflow"))

	var hub stash[v1alpha2.WorkflowSpec, struct{}]
	found, err := takeStash(src, dst, AnnotationV1Alpha2, &hub)
	if err != nil {
		return err
	}
	dropAnnotation(dst, AnnotationV1Alpha1)
	dropAnnotation(dst, AnnotationTemplate)
	if src.Spec.TemplateRef != "" {
		ann := maps.Clone(dst.GetAnnotations())
		if ann == nil {
			ann = map[string]string{}
		}
		ann[AnnotationTemplate] = src.Spec.TemplateRef
		dst.SetAnnotations(ann)
	}

	mapped := workflowSpecToV2(src.Spec, src.Namespace)
	dst.Spec = mapped
	if found {
		h, err := hashOf(src.Spec)
		if err != nil {
			return err
		}
		if h == hub.SpecHash {
			dst.Spec = hub.Spec
		} else {
			restoreWorkflowV2(&dst.Spec, hub.Spec)
		}
	}
	dst.Status = workflowStatusToV2(src.Status)

	h, err := hashOf(dst.Spec)
	if err != nil {
		return err
	}
	return putStash(dst, AnnotationV1Alpha1, stash[v1alpha1.WorkflowSpec, struct{}]{Spec: src.Spec, SpecHash: h})
}

// WorkflowToV1Alpha1 converts a v1alpha2 Workflow to v1alpha1.
func WorkflowToV1Alpha1(src *v1alpha2.Workflow, dst *v1alpha1.Workflow) error {
	dst.ObjectMeta = *src.ObjectMeta.DeepCopy()
	dst.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Workflow"))

	var spoke stash[v1alpha1.WorkflowSpec, struct{}]
	found, err := takeStash(src, dst, AnnotationV1Alpha1, &spoke)
	if err != nil {
		return err
	}
	dropAnnotation(dst, AnnotationV1Alpha2)
	dropAnnotation(dst, AnnotationTemplate)

	mapped := workflowSpecToV1(src.Spec)
	mapped.TemplateRef = src.Annotations[AnnotationTemplate]
	dst.Spec = mapped
	if found {
		h, err := hashOf(src.Spec)
		if err != nil {
			return err
		}
		if h == spoke.SpecHash {
			dst.Spec = spoke.Spec
			if mapped.TemplateRef != "" {
				dst.Spec.TemplateRef = mapped.TemplateRef
			}
		} else {
			restoreWorkflowV1(&dst.Spec, spoke.Spec)
		}
	}
	dst.Status = workflowStatusToV1(src.Status)

	h, err := hashOf(dst.Spec)
	if err != nil {
		return err
	}
	return putStash(dst, AnnotationV1Alpha2, stash[v1alpha2.WorkflowSpec, struct{}]{Spec: src.Spec, SpecHash: h})
}

func workflowSpecToV2(s v1alpha1.WorkflowSpec, namespace string) v1alpha2.WorkflowSpec {
	d := v1alpha2.WorkflowSpec{Disabled: copyPtr(s.Disabled)}
	if len(s.HardwareMap) > 0 {
		d.Globals = &v1alpha2.Extra{TemplateMap: maps.Clone(s.HardwareMap)}
	}
	bo := bootOptionsToV2(s.BootOptions)
	if s.HardwareRef != "" || bo != nil {
		wh := &v1alpha2.WorkflowHardware{BootOptions: bo}
		if s.HardwareRef != "" {
			wh.HardwareRef = &v1alpha2.SimpleReference{Name: s.HardwareRef, Namespace: namespace}
		}
		d.Tasks = []v1alpha2.WorkflowTask{{Hardware: wh}}
	}
	return d
}

func workflowSpecToV1(s v1alpha2.WorkflowSpec) v1alpha1.WorkflowSpec {
	d := v1alpha1.WorkflowSpec{Disabled: copyPtr(s.Disabled)}
	if s.Globals != nil && len(s.Globals.TemplateMap) > 0 {
		d.HardwareMap = maps.Clone(s.Globals.TemplateMap)
	}
	for _, t := range s.Tasks {
		if t.Hardware == nil {
			continue
		}
		if d.HardwareRef == "" && t.Hardware.HardwareRef != nil {
			d.HardwareRef = t.Hardware.HardwareRef.Name
		}
		if t.Hardware.BootOptions != nil && reflect.DeepEqual(d.BootOptions, v1alpha1.BootOptions{}) {
			d.BootOptions = bootOptionsToV1(*t.Hardware.BootOptions)
		}
	}
	return d
}

func bootOptionsToV2(b v1alpha1.BootOptions) *v1alpha2.BootOptions {
	if reflect.DeepEqual(b, v1alpha1.BootOptions{}) {
		return nil
	}
	mode := v1alpha2.BootMode(b.BootMode)
	if b.BootMode == v1alpha1.BootModeISO {
		mode = v1alpha2.BootModeIsoboot
	}
	out := &v1alpha2.BootOptions{ToggleNetboot: b.ToggleAllowNetboot, ISOURL: b.ISOURL, BootMode: mode}
	if c := b.CustombootConfig; len(c.PreparingActions) > 0 || len(c.PostActions) > 0 {
		out.Customboot = &v1alpha2.Customboot{
			PreOperations:  actionsToOperations(c.PreparingActions),
			PostOperations: actionsToOperations(c.PostActions),
		}
	}
	return out
}

func bootOptionsToV1(b v1alpha2.BootOptions) v1alpha1.BootOptions {
	out := v1alpha1.BootOptions{ToggleAllowNetboot: b.ToggleNetboot, ISOURL: b.ISOURL, BootMode: v1alpha1.BootMode(b.BootMode)}
	if b.Customboot != nil {
		out.CustombootConfig = v1alpha1.CustombootConfig{
			PreparingActions: operationsToActions(b.Customboot.PreOperations),
			PostActions:      operationsToActions(b.Customboot.PostOperations),
		}
	}
	return out
}

// restoreWorkflowV2 keeps the v1alpha2 spec v1alpha1 cannot express when a
// v1alpha1 client changed a Workflow that came from v1alpha2.
func restoreWorkflowV2(s *v1alpha2.WorkflowSpec, hub v1alpha2.WorkflowSpec) {
	s.References = hub.References
	s.TimeoutSeconds = hub.TimeoutSeconds
	s.Vars = hub.Vars
	if hub.Globals != nil {
		if s.Globals == nil {
			s.Globals = &v1alpha2.Extra{}
		}
		s.Globals.Env = hub.Globals.Env
		s.Globals.Volumes = hub.Globals.Volumes
	}
	if len(hub.Tasks) > 0 {
		tasks := slices.Clone(hub.Tasks)
		if len(s.Tasks) > 0 {
			tasks[0].Hardware = s.Tasks[0].Hardware
		} else {
			tasks[0].Hardware = nil
		}
		s.Tasks = tasks
	}
}

// restoreWorkflowV1 keeps the v1alpha1 spec v1alpha2 cannot express when a
// v1alpha2 client changed a Workflow that came from v1alpha1.
func restoreWorkflowV1(s *v1alpha1.WorkflowSpec, spoke v1alpha1.WorkflowSpec) {
	if s.TemplateRef == "" {
		s.TemplateRef = spoke.TemplateRef
	}
	if s.BootOptions.BootMode == v1alpha1.BootModeIsoboot && spoke.BootOptions.BootMode == v1alpha1.BootModeISO {
		s.BootOptions.BootMode = v1alpha1.BootModeISO
	}
	// Keep the original BMC actions where they still mean the same operations
	// (e.g. the deprecated oneTimeBootDeviceAction).
	cur, orig := &s.BootOptions.CustombootConfig, spoke.BootOptions.CustombootConfig
	if reflect.DeepEqual(actionsToOperations(cur.PreparingActions), actionsToOperations(orig.PreparingActions)) {
		cur.PreparingActions = orig.PreparingActions
	}
	if reflect.DeepEqual(actionsToOperations(cur.PostActions), actionsToOperations(orig.PostActions)) {
		cur.PostActions = orig.PostActions
	}
}

func workflowStatusToV2(s v1alpha1.WorkflowStatus) v1alpha2.WorkflowStatus {
	d := v1alpha2.WorkflowStatus{
		BootOptions: v1alpha2.BootOptionsStatus{
			AllowNetboot: v1alpha2.AllowNetbootStatus{ToggledTrue: s.BootOptions.AllowNetboot.ToggledTrue, ToggledFalse: s.BootOptions.AllowNetboot.ToggledFalse},
		},
		GlobalTimeout:       s.GlobalTimeout,
		GlobalExecutionStop: s.GlobalExecutionStop.DeepCopy(),
	}
	if s.BootOptions.Jobs != nil {
		d.BootOptions.Jobs = map[string]v1alpha2.JobStatus{}
		for k, j := range s.BootOptions.Jobs {
			d.BootOptions.Jobs[k] = v1alpha2.JobStatus{UID: j.UID, Complete: j.Complete, ExistingJobDeleted: j.ExistingJobDeleted}
		}
	}
	d.Metadata.Workflow.State = StateToV1Alpha2(s.State)
	d.Metadata.Workflow.AgentID = s.AgentID
	d.Metadata.Task.AgentID = s.AgentID
	if c := s.CurrentState; c != nil {
		if c.AgentID != "" {
			d.Metadata.Task.AgentID = c.AgentID
		}
		d.Metadata.Task.ID = types.UID(c.TaskID)
		d.Metadata.Task.Name = c.TaskName
		d.Metadata.Action.ID = types.UID(c.ActionID)
		d.Metadata.Action.Name = c.ActionName
		d.Metadata.Action.State = StateToV1Alpha2(c.State)
	}
	for _, t := range s.Tasks {
		rt := v1alpha2.TaskWithMetadata{
			Name:     t.Name,
			Env:      maps.Clone(t.Environment),
			Volumes:  toVolumes(t.Volumes),
			Metadata: v1alpha2.Metadata{ID: types.UID(t.ID), Name: t.Name, AgentID: t.AgentID},
		}
		for _, a := range t.Actions {
			rt.Actions = append(rt.Actions, actionToV2(a))
		}
		d.RenderedTasks = append(d.RenderedTasks, rt)
	}
	for _, c := range s.Conditions {
		d.Conditions = append(d.Conditions, v1alpha2.WorkflowCondition{
			Type: v1alpha2.ConditionType(c.Type), Status: c.Status, Reason: c.Reason, Message: c.Message, Time: c.Time.DeepCopy(),
		})
	}
	return d
}

func workflowStatusToV1(s v1alpha2.WorkflowStatus) v1alpha1.WorkflowStatus {
	d := v1alpha1.WorkflowStatus{
		AgentID: s.Metadata.Task.AgentID,
		State:   StateToV1Alpha1(s.Metadata.Workflow.State),
		BootOptions: v1alpha1.BootOptionsStatus{
			AllowNetboot: v1alpha1.AllowNetbootStatus{ToggledTrue: s.BootOptions.AllowNetboot.ToggledTrue, ToggledFalse: s.BootOptions.AllowNetboot.ToggledFalse},
		},
		GlobalTimeout:       s.GlobalTimeout,
		GlobalExecutionStop: s.GlobalExecutionStop.DeepCopy(),
	}
	if d.AgentID == "" {
		d.AgentID = s.Metadata.Workflow.AgentID
	}
	if s.BootOptions.Jobs != nil {
		d.BootOptions.Jobs = map[string]v1alpha1.JobStatus{}
		for k, j := range s.BootOptions.Jobs {
			d.BootOptions.Jobs[k] = v1alpha1.JobStatus{UID: j.UID, Complete: j.Complete, ExistingJobDeleted: j.ExistingJobDeleted}
		}
	}
	m := s.Metadata
	if m.Task.ID != "" || m.Action.ID != "" || m.Task.Name != "" || m.Action.Name != "" {
		d.CurrentState = &v1alpha1.CurrentState{
			AgentID: m.Task.AgentID, TaskID: string(m.Task.ID), TaskName: m.Task.Name,
			ActionID: string(m.Action.ID), ActionName: m.Action.Name, State: StateToV1Alpha1(m.Action.State),
		}
	}
	for _, rt := range s.RenderedTasks {
		t := v1alpha1.Task{
			ID: string(rt.Metadata.ID), Name: rt.Name, AgentID: rt.Metadata.AgentID,
			Environment: maps.Clone(rt.Env), Volumes: fromVolumes(rt.Volumes),
		}
		for _, a := range rt.Actions {
			t.Actions = append(t.Actions, actionToV1(a))
		}
		d.Tasks = append(d.Tasks, t)
	}
	for _, c := range s.Conditions {
		d.Conditions = append(d.Conditions, v1alpha1.WorkflowCondition{
			Type: v1alpha1.WorkflowConditionType(c.Type), Status: c.Status, Reason: c.Reason, Message: c.Message, Time: c.Time.DeepCopy(),
		})
		if c.Type == v1alpha2.TemplateRenderedSuccess {
			d.TemplateRendering = v1alpha1.TemplateRenderingFailed
			if c.Status == "True" {
				d.TemplateRendering = v1alpha1.TemplateRenderingSuccessful
			}
		}
	}
	return d
}

// ActionToV1Alpha2 converts a rendered v1alpha1 Action, as found in a v1alpha1
// Workflow's status, to its v1alpha2 form.
func ActionToV1Alpha2(a v1alpha1.Action) v1alpha2.ActionWithMetadata { return actionToV2(a) }

func actionToV2(a v1alpha1.Action) v1alpha2.ActionWithMetadata {
	out := v1alpha2.ActionWithMetadata{
		Action: v1alpha2.Action{
			Name:    a.Name,
			Image:   a.Image,
			Env:     maps.Clone(a.Environment),
			Volumes: toVolumes(a.Volumes),
		},
		Metadata: v1alpha2.Metadata{
			ID: types.UID(a.ID), Name: a.Name, State: StateToV1Alpha2(a.State),
			StartTime: a.ExecutionStart.DeepCopy(), EndTime: a.ExecutionStop.DeepCopy(),
			ExecutionDuration: a.ExecutionDuration, Message: a.Message,
		},
	}
	// v1alpha1 command is the entrypoint followed by its arguments.
	if len(a.Command) > 0 {
		out.Command = a.Command[0]
		out.Args = slices.Clone(a.Command[1:])
	}
	if a.Timeout > 0 {
		out.TimeoutSeconds = ptr(a.Timeout)
	}
	if a.Namespaces != nil {
		out.Namespaces = v1alpha2.Namespaces{Network: a.Namespaces.Network, PID: a.Namespaces.PID}
	}
	if out.Namespaces.PID == "" {
		out.Namespaces.PID = a.Pid //nolint:staticcheck // converting the deprecated field
	}
	return out
}

func actionToV1(a v1alpha2.ActionWithMetadata) v1alpha1.Action {
	out := v1alpha1.Action{
		ID: string(a.Metadata.ID), Name: a.Name, Image: a.Image,
		Timeout:        deref(a.TimeoutSeconds),
		Volumes:        fromVolumes(a.Volumes),
		Environment:    maps.Clone(a.Env),
		State:          StateToV1Alpha1(a.Metadata.State),
		ExecutionStart: a.Metadata.StartTime.DeepCopy(), ExecutionStop: a.Metadata.EndTime.DeepCopy(),
		ExecutionDuration: a.Metadata.ExecutionDuration, Message: a.Metadata.Message,
	}
	if a.Command != "" {
		out.Command = append([]string{a.Command}, a.Args...)
	} else if len(a.Args) > 0 {
		out.Command = slices.Clone(a.Args)
	}
	if a.Namespaces != (v1alpha2.Namespaces{}) {
		out.Namespaces = &v1alpha1.ActionNamespaces{Network: a.Namespaces.Network, PID: a.Namespaces.PID}
	}
	return out
}

func toVolumes(in []string) []v1alpha2.Volume {
	var out []v1alpha2.Volume
	for _, v := range in {
		out = append(out, v1alpha2.Volume(v))
	}
	return out
}

func fromVolumes(in []v1alpha2.Volume) []string {
	var out []string
	for _, v := range in {
		out = append(out, string(v))
	}
	return out
}
