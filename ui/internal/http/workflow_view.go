package webhttp

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	tinkv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// stateName returns the name of a v1alpha2 state, empty when it is unset.
func stateName(s tinkv1alpha2.State) string {
	if s == 0 {
		return ""
	}
	return s.String()
}

// isWorkflowDisabled reports whether spec.disabled is set and true.
func isWorkflowDisabled(wf *tinkv1alpha2.Workflow) bool {
	return wf.Spec.Disabled != nil && *wf.Spec.Disabled
}

// workflowAgent returns the Agent running the Workflow's current Task.
func workflowAgent(wf *tinkv1alpha2.Workflow) string {
	if a := wf.Status.Metadata.Task.AgentID; a != "" {
		return a
	}
	return wf.Status.Metadata.Workflow.AgentID
}

// workflowTemplate returns the deprecated v1alpha1 Template a Workflow created
// through v1alpha1 runs, and whether it runs it in v1alpha1 compatibility mode:
// it references no Tasks, so the Workflow engine renders the Template.
func workflowTemplate(wf *tinkv1alpha2.Workflow) (string, bool) {
	tpl := wf.Annotations[conversion.AnnotationTemplate]
	if tpl == "" {
		return "", false
	}
	for _, t := range wf.Spec.Tasks {
		if t.TaskRef.Name != "" {
			return tpl, false
		}
	}
	return tpl, true
}

// workflowTaskRefs describes the Tasks of a Workflow.
func workflowTaskRefs(wf *tinkv1alpha2.Workflow) []templates.TaskRef {
	refs := make([]templates.TaskRef, 0, len(wf.Spec.Tasks))
	for _, t := range wf.Spec.Tasks {
		ref := templates.TaskRef{
			Name:      t.TaskRef.Name,
			Namespace: t.TaskRef.Namespace,
			AgentID:   t.AgentID,
			Timeout:   seconds(t.TimeoutSeconds),
		}
		if ref.Name != "" && ref.Namespace == "" {
			ref.Namespace = wf.Namespace
		}
		if h := t.Hardware; h != nil {
			if r := h.HardwareRef; r != nil && r.Name != "" {
				ns := r.Namespace
				if ns == "" {
					ns = wf.Namespace
				}
				ref.Hardware = ns + "/" + r.Name
			}
			if h.BootOptions != nil {
				ref.BootOptions = describeBootOptions(*h.BootOptions)
			}
		}
		refs = append(refs, ref)
	}
	return refs
}

// describeBootOptions summarizes boot options, e.g. "netboot, toggles netboot".
func describeBootOptions(b tinkv1alpha2.BootOptions) string {
	var parts []string
	if b.BootMode != "" {
		parts = append(parts, string(b.BootMode))
	}
	if b.ToggleNetboot {
		parts = append(parts, "toggles netboot")
	}
	if b.ISOURL != "" {
		parts = append(parts, "ISO "+b.ISOURL)
	}
	if c := b.Customboot; c != nil {
		parts = append(parts, fmt.Sprintf("%d pre and %d post operations", len(c.PreOperations), len(c.PostOperations)))
	}
	return strings.Join(parts, ", ")
}

// workflowHardware lists the distinct Hardware a Workflow's Tasks run against.
func workflowHardware(wf *tinkv1alpha2.Workflow) []string {
	var out []string
	for _, t := range wf.Spec.Tasks {
		if t.Hardware == nil || t.Hardware.HardwareRef == nil || t.Hardware.HardwareRef.Name == "" {
			continue
		}
		ref := t.Hardware.HardwareRef
		ns := ref.Namespace
		if ns == "" {
			ns = wf.Namespace
		}
		if hw := ns + "/" + ref.Name; !slices.Contains(out, hw) {
			out = append(out, hw)
		}
	}
	return out
}

// templateRendering returns whether the Workflow's Tasks rendered, the way
// v1alpha1 reported it in status.templateRendering.
func templateRendering(wf *tinkv1alpha2.Workflow) string {
	for _, c := range wf.Status.Conditions {
		if c.Type == tinkv1alpha2.TemplateRenderedSuccess {
			if c.Status == metav1.ConditionTrue {
				return string(tinkv1alpha1.TemplateRenderingSuccessful)
			}
			return string(tinkv1alpha1.TemplateRenderingFailed)
		}
	}
	return ""
}

// toWorkflowRow converts a Workflow resource into its list-view representation.
// CanUpdate is left false; call setCanUpdatePermissions afterwards to fill it
// in for disabled rows.
func toWorkflowRow(wf tinkv1alpha2.Workflow) templates.Workflow {
	tpl, _ := workflowTemplate(&wf)
	var tasks []templates.TaskRef
	for _, t := range workflowTaskRefs(&wf) {
		if t.Name != "" {
			tasks = append(tasks, t)
		}
	}
	return templates.Workflow{
		Name:        wf.Name,
		Namespace:   wf.Namespace,
		TemplateRef: tpl,
		Tasks:       tasks,
		State:       stateName(wf.Status.Metadata.Workflow.State),
		Disabled:    isWorkflowDisabled(&wf),
		Task:        wf.Status.Metadata.Task.Name,
		Action:      wf.Status.Metadata.Action.Name,
		Agent:       workflowAgent(&wf),
		CreatedAt:   wf.GetCreationTimestamp().Format(timeFormat),
	}
}

// workflowDetail builds the workflow detail page data, without the YAML views
// and permissions.
func workflowDetail(wf *tinkv1alpha2.Workflow) templates.WorkflowDetail {
	row := toWorkflowRow(*wf)
	d := templates.WorkflowDetail{
		Name:              wf.Name,
		Namespace:         wf.Namespace,
		TemplateRef:       row.TemplateRef,
		Hardware:          workflowHardware(wf),
		State:             row.State,
		Disabled:          row.Disabled,
		Task:              row.Task,
		Action:            row.Action,
		Agent:             row.Agent,
		TemplateRendering: templateRendering(wf),
		CreatedAt:         row.CreatedAt,
		Labels:            wf.Labels,
		Annotations:       wf.Annotations,
		Tasks:             workflowTaskRefs(wf),
	}
	_, d.CompatibilityMode = workflowTemplate(wf)
	d.Details = []templates.InfoRow{
		{Name: "Timeout", Value: seconds(wf.Spec.TimeoutSeconds)},
		{Name: "References", Value: strings.Join(slices.Sorted(maps.Keys(wf.Spec.References)), ", ")},
		{Name: "Vars", Value: keyValues(wf.Spec.Vars)},
	}
	if g := wf.Spec.Globals; g != nil {
		d.Details = append(d.Details,
			templates.InfoRow{Name: "Template Map", Value: keyValues(g.TemplateMap)},
			templates.InfoRow{Name: "Environment", Value: keyValues(g.Env)},
			templates.InfoRow{Name: "Volumes", Value: joinStrings(g.Volumes)},
		)
	}
	for _, rt := range wf.Status.RenderedTasks {
		task := templates.RenderedTask{Name: rt.Name, AgentID: rt.Metadata.AgentID, State: stateName(rt.Metadata.State)}
		for _, a := range rt.Actions {
			task.Actions = append(task.Actions, templates.RenderedAction{
				Name:     a.Name,
				Image:    a.Image,
				State:    stateName(a.Metadata.State),
				Started:  formatTime(a.Metadata.StartTime),
				Duration: a.Metadata.ExecutionDuration,
				Message:  a.Metadata.Message,
			})
		}
		d.RenderedTasks = append(d.RenderedTasks, task)
	}
	for _, c := range wf.Status.Conditions {
		d.Conditions = append(d.Conditions, templates.Condition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
			Time:    formatTime(c.Time),
		})
	}
	return d
}
