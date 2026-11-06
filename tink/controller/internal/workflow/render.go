package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	"github.com/oklog/ulid/v2"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	"github.com/tinkerbell/tinkerbell/pkg/template/render"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// renderFailed records why rendering failed in the TemplateRenderedSuccess condition.
func renderFailed(wf *v1alpha2.Workflow, message string) {
	wf.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
		Type:    v1alpha2.TemplateRenderedSuccess,
		Status:  metav1.ConditionFalse,
		Reason:  reasonError,
		Message: message,
		Time:    &metav1.Time{Time: metav1.Now().UTC()},
	})
}

func renderSucceeded(wf *v1alpha2.Workflow) {
	wf.Status.SetCondition(v1alpha2.WorkflowCondition{
		Type:    v1alpha2.TemplateRenderedSuccess,
		Status:  metav1.ConditionTrue,
		Reason:  "Complete",
		Message: "template rendered successfully",
		Time:    &metav1.Time{Time: metav1.Now().UTC()},
	})
}

// renderCompat renders a Workflow created through v1alpha1 in v1alpha1 compatibility mode: its Template
// is rendered and parsed exactly as the v1alpha1 engine did, with the v1alpha1 template data (.Hardware,
// .hardware as the v1alpha1 Hardware, the hardwareMap keys, .references and .metadata_url), and the result
// is written as v1alpha2 rendered tasks.
func (r *Reconciler) renderCompat(ctx context.Context, logger logr.Logger, stored *v1alpha2.Workflow) error {
	templateRef := stored.Annotations[conversion.AnnotationTemplate]
	tpl := &v1alpha1.Template{}
	if err := r.client.Get(ctx, ctrlclient.ObjectKey{Name: templateRef, Namespace: stored.Namespace}, tpl); err != nil {
		// No match: the Template CRD was removed, with every Template.
		if kerrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			// Throw an error to raise awareness and take advantage of immediate requeue.
			logger.Error(err, "error getting Template object in processNewWorkflow function")
			journal.Log(ctx, "template not found")
			renderFailed(stored, "template not found")

			return fmt.Errorf(
				"no template found: name=%v; namespace=%v",
				templateRef,
				stored.Namespace,
			)
		}
		renderFailed(stored, err.Error())
		return err
	}

	// v1alpha1 Workflows reference Hardware in their own namespace.
	var hardware v1alpha1.Hardware
	if ref := engine.HardwareRef(stored, 0); ref != nil {
		hw := &v1alpha2.Hardware{}
		err := r.client.Get(ctx, ctrlclient.ObjectKey{Name: ref.Name, Namespace: stored.Namespace}, hw)
		if ctrlclient.IgnoreNotFound(err) != nil {
			logger.Error(err, "error getting Hardware object in processNewWorkflow function")
			journal.Log(ctx, "hardware not found")
			renderFailed(stored, fmt.Sprintf("error getting hardware: %v", err))
			return err
		}
		if kerrors.IsNotFound(err) {
			logger.Error(err, "hardware not found in processNewWorkflow function")
			journal.Log(ctx, "hardware not found")
			renderFailed(stored, fmt.Sprintf("hardware not found: %v", err))
			return fmt.Errorf(
				"hardware not found: name=%v; namespace=%v",
				ref.Name,
				stored.Namespace,
			)
		}
		if err := conversion.HardwareToV1Alpha1(hw, &hardware); err != nil {
			renderFailed(stored, fmt.Sprintf("error converting hardware to v1alpha1: %v", err))
			return fmt.Errorf("convert hardware to v1alpha1: %w", err)
		}
	}

	rendered, err := r.references.RenderedHardwareV1Alpha1(ctx, &hardware)
	if err != nil {
		journal.Log(ctx, "error getting rendered hardware")
		renderFailed(stored, fmt.Sprintf("error getting rendered hardware: %v", err))
		return err
	}
	hardware = *rendered

	data := make(map[string]interface{})
	if r.metadataURL != "" {
		data[templateDataMetadataURL] = r.metadataURL
	}
	if stored.Spec.Globals != nil {
		for key, val := range stored.Spec.Globals.TemplateMap {
			data[key] = val
		}
	}
	contract := toTemplateHardwareData(hardware)
	// Not a JSON round trip: that turns integers into float64 and replaces non-UTF-8 bytes.
	hardwareData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&hardware)
	if err != nil {
		renderFailed(stored, fmt.Sprintf("error converting hardware template data: %v", err))
		return fmt.Errorf("convert hardware template data: %w", err)
	}
	data[templateDataHardware] = hardwareData
	data[templateDataHardwareLegacy] = contract
	refs := make(map[string]v1alpha2.Reference, len(hardware.Spec.References))
	for name, rf := range hardware.Spec.References {
		refs[name] = v1alpha2.Reference(rf)
	}
	references, refErr := r.references.ResolveReferencesFor(ctx, types.NamespacedName{Namespace: hardware.Namespace, Name: hardware.Name}, refs)
	data[templateDataReferences] = references

	tinkWf, err := renderTemplateHardware(stored.Name, pointerToValue(tpl.Spec.Data), data)
	if err != nil {
		journal.Log(ctx, "error rendering template")
		renderFailed(stored, fmt.Sprintf("error rendering template: %v", errors.Join(refErr, err)))

		return err
	}

	// populate Task and Action data
	stored.Status = *YAMLToStatus(tinkWf)
	renderSucceeded(stored)

	return nil
}

// renderNative renders the Tasks of a v1alpha2 Workflow. Objects are rendered in order: the Hardware of a
// task (by the backend), then the Workflow, with that Hardware as .hardware, then the Task, with .hardware,
// the rendered Workflow as .workflow, itself as .task, and .metadata_url. Each object's .references are its
// own spec.references, resolved with the backend's reference policy.
func (r *Reconciler) renderNative(ctx context.Context, stored *v1alpha2.Workflow) error {
	renderedTasks, wf0, err := r.renderTasks(ctx, stored, nil)
	if err != nil {
		journal.Log(ctx, "error rendering tasks", "error", err)
		renderFailed(stored, err.Error())
		return err
	}

	stored.Status = v1alpha2.WorkflowStatus{
		GlobalTimeout: pointerToValue(wf0.Spec.TimeoutSeconds),
		RenderedTasks: renderedTasks,
	}
	if first := engine.NextTask(stored, -1); first >= 0 {
		stored.Status.Metadata.Task.AgentID = renderedTasks[first].Metadata.AgentID
	}
	renderSucceeded(stored)
	return nil
}

// renderTasks renders the tasks of a native Workflow and returns them with the Workflow rendered for the
// first. Tasks named in tasks are used as given instead of being read.
func (r *Reconciler) renderTasks(ctx context.Context, wf *v1alpha2.Workflow, tasks map[types.NamespacedName]*v1alpha2.Task) ([]v1alpha2.TaskWithMetadata, *v1alpha2.Workflow, error) {
	if len(wf.Spec.Tasks) == 0 {
		return nil, nil, fmt.Errorf("workflow %s/%s has no tasks", wf.Namespace, wf.Name)
	}
	rc := r.newRenderContext(wf)
	rc.tasks = tasks
	renderedTasks := make([]v1alpha2.TaskWithMetadata, 0, len(wf.Spec.Tasks))
	var wf0 *v1alpha2.Workflow
	for i, t := range wf.Spec.Tasks {
		rt, rendered, err := rc.renderTask(ctx, i, t)
		if err != nil {
			return nil, nil, fmt.Errorf("error rendering task %d: %w", i, err)
		}
		if wf0 == nil {
			wf0 = rendered
		}
		renderedTasks = append(renderedTasks, rt)
	}
	return renderedTasks, wf0, nil
}

// renderContext renders the objects of one Workflow, rendering each Hardware and
// the Workflow for each Hardware once.
type renderContext struct {
	r  *Reconciler
	wf *v1alpha2.Workflow
	// tasks are used instead of reading the Tasks they name.
	tasks    map[types.NamespacedName]*v1alpha2.Task
	hardware map[types.NamespacedName]renderedHardware
	workflow map[types.NamespacedName]map[string]any
	refs     map[string]any
	refsErr  error
	refsDone bool
}

type renderedHardware struct {
	data map[string]any
	hw   *v1alpha2.Hardware
}

func (r *Reconciler) newRenderContext(wf *v1alpha2.Workflow) *renderContext {
	return &renderContext{
		r:        r,
		wf:       wf,
		hardware: map[types.NamespacedName]renderedHardware{},
		workflow: map[types.NamespacedName]map[string]any{},
	}
}

// hardwareData returns the rendered Hardware of task i as template data, or nil when it has none.
func (rc *renderContext) hardwareData(ctx context.Context, i int) (map[string]any, *v1alpha2.Hardware, error) {
	ref := engine.HardwareRef(rc.wf, i)
	if ref == nil {
		return nil, nil, nil
	}
	// A Workflow only acts on Hardware in its own namespace: it toggles netboot and runs BMC Jobs on it.
	if ref.Namespace != rc.wf.Namespace {
		return nil, nil, fmt.Errorf("hardware %s/%s is not in the workflow's namespace", ref.Namespace, ref.Name)
	}
	key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	if h, ok := rc.hardware[key]; ok {
		return h.data, h.hw, nil
	}
	hw := &v1alpha2.Hardware{}
	if err := rc.r.client.Get(ctx, key, hw); err != nil {
		return nil, nil, fmt.Errorf("getting hardware %s: %w", key, err)
	}
	rendered, err := rc.r.references.RenderedHardware(ctx, hw)
	if err != nil {
		return nil, nil, fmt.Errorf("getting rendered hardware %s: %w", key, err)
	}
	// Not a JSON round trip: that turns integers into float64 and replaces non-UTF-8 bytes.
	d, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rendered)
	if err != nil {
		return nil, nil, fmt.Errorf("converting hardware %s to template data: %w", key, err)
	}
	rc.hardware[key] = renderedHardware{data: d, hw: rendered}
	return d, rendered, nil
}

// workflowHardware returns the index of the task whose Hardware the Workflow is rendered with for task i:
// task i, or when it has no Hardware, the first task with Hardware, so that a task without Hardware (such
// as one on an administrative Agent) sees the Workflow as the others do.
func (rc *renderContext) workflowHardware(i int) int {
	if engine.HardwareRef(rc.wf, i) != nil {
		return i
	}
	for j := range rc.wf.Spec.Tasks {
		if engine.HardwareRef(rc.wf, j) != nil {
			return j
		}
	}
	return i
}

// workflowDoc returns the Workflow rendered for task i, as template data.
func (rc *renderContext) workflowDoc(ctx context.Context, i int) (map[string]any, error) {
	i = rc.workflowHardware(i)
	hw, _, err := rc.hardwareData(ctx, i)
	if err != nil {
		return nil, err
	}
	var key types.NamespacedName
	if ref := engine.HardwareRef(rc.wf, i); ref != nil {
		key = types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	}
	if d, ok := rc.workflow[key]; ok {
		return d, nil
	}
	doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rc.wf)
	if err != nil {
		return nil, fmt.Errorf("converting workflow to template data: %w", err)
	}
	delete(doc, "status")
	if render.HasTemplates(doc, render.WithSkip(skipWorkflowPath)) {
		if !rc.refsDone {
			rc.refs, rc.refsErr = rc.r.references.ResolveReferencesFor(ctx, types.NamespacedName{Namespace: rc.wf.Namespace, Name: rc.wf.Name}, rc.wf.Spec.References)
			rc.refsDone = true
		}
		data := map[string]any{templateDataReferences: rc.refs}
		if hw != nil {
			data[templateDataHardware] = hw
		}
		if _, err := render.Value(doc, data,
			render.WithSelfKey(templateDataWorkflow),
			render.WithFuncs(funcmap.New()),
			render.WithSkip(skipWorkflowPath),
		); err != nil {
			return nil, fmt.Errorf("rendering workflow: %w", errors.Join(rc.refsErr, err))
		}
	}
	rc.workflow[key] = doc
	return doc, nil
}

// renderTask renders spec.tasks[i] of the Workflow. It returns the rendered task and the Workflow
// rendered for it.
func (rc *renderContext) renderTask(ctx context.Context, i int, t v1alpha2.WorkflowTask) (v1alpha2.TaskWithMetadata, *v1alpha2.Workflow, error) {
	var none v1alpha2.TaskWithMetadata
	if t.TaskRef.Name == "" {
		return none, nil, errors.New("it has no taskRef")
	}
	hwData, hw, err := rc.hardwareData(ctx, i)
	if err != nil {
		return none, nil, err
	}
	wfDoc, err := rc.workflowDoc(ctx, i)
	if err != nil {
		return none, nil, err
	}
	wf := &v1alpha2.Workflow{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(wfDoc, wf); err != nil {
		return none, nil, fmt.Errorf("converting rendered workflow: %w", err)
	}
	spec := wf.Spec.Tasks[i]

	taskKey := types.NamespacedName{Namespace: t.TaskRef.Namespace, Name: t.TaskRef.Name}
	if taskKey.Namespace == "" {
		taskKey.Namespace = rc.wf.Namespace
	}
	task := &v1alpha2.Task{}
	if given, ok := rc.tasks[taskKey]; ok {
		task = given.DeepCopy()
	} else if err := rc.r.client.Get(ctx, taskKey, task); err != nil {
		return none, nil, fmt.Errorf("getting task %s: %w", taskKey, err)
	}
	taskDoc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(task)
	if err != nil {
		return none, nil, fmt.Errorf("converting task %s to template data: %w", taskKey, err)
	}
	if render.HasTemplates(taskDoc, render.WithSkip(skipTaskPath)) {
		refs, refErr := rc.r.references.ResolveReferencesFor(ctx, taskKey, task.Spec.References)
		data := map[string]any{templateDataReferences: refs, templateDataWorkflow: wfDoc}
		if hwData != nil {
			data[templateDataHardware] = hwData
		}
		if rc.r.metadataURL != "" {
			data[templateDataMetadataURL] = rc.r.metadataURL
		}
		if _, err := render.Value(taskDoc, data,
			render.WithSelfKey(templateDataTask),
			render.WithFuncs(funcmap.New()),
			render.WithSkip(skipTaskPath),
		); err != nil {
			return none, nil, fmt.Errorf("rendering task %s: %w", taskKey, errors.Join(refErr, err))
		}
		rendered := &v1alpha2.Task{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(taskDoc, rendered); err != nil {
			return none, nil, fmt.Errorf("converting rendered task %s: %w", taskKey, err)
		}
		task = rendered
	}

	agent := spec.AgentID
	if agent == "" && hw != nil {
		agent = hw.Spec.AgentID
	}
	if agent == "" {
		return none, nil, errors.New("it has no agentID, and no Hardware with one")
	}
	name := task.Name
	if n := task.Annotations[conversion.AnnotationTemplateTask]; n != "" {
		// A Task translated from a Template keeps the template task's name.
		name = n
	}
	rt := v1alpha2.TaskWithMetadata{
		Name:    name,
		Env:     mergeEnv(task.Spec.Env, wf.Spec.Globals, spec.Extra),
		Volumes: mergeVolumes(task.Spec.Volumes, wf.Spec.Globals, spec.Extra),
		Metadata: v1alpha2.Metadata{
			ID:      types.UID(ulid.Make().String()),
			Name:    name,
			AgentID: agent,
			State:   v1alpha2.StatePending,
		},
	}
	if hw != nil {
		rt.Metadata.Hardware = hw.Name
	}
	for _, a := range task.Spec.Actions {
		if !runs(a.If) {
			continue
		}
		rt.Actions = append(rt.Actions, v1alpha2.ActionWithMetadata{
			Action: a,
			Metadata: v1alpha2.Metadata{
				ID:    types.UID(ulid.Make().String()),
				Name:  a.Name,
				State: v1alpha2.StatePending,
			},
		})
	}
	if len(rt.Actions) == 0 {
		rt.Metadata.State = v1alpha2.StateSuccess
	}
	return rt, wf, nil
}

// renderedWorkflowFor returns wf with its templates rendered for rendered task j, for the boot options of
// that task. A Workflow in v1alpha1 compatibility mode is not templated.
func (r *Reconciler) renderedWorkflowFor(ctx context.Context, wf *v1alpha2.Workflow, j int) (*v1alpha2.Workflow, error) {
	if engine.Compat(wf) {
		return wf, nil
	}
	rc := r.newRenderContext(wf)
	doc, err := rc.workflowDoc(ctx, j)
	if err != nil {
		return nil, err
	}
	out := &v1alpha2.Workflow{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(doc, out); err != nil {
		return nil, fmt.Errorf("converting rendered workflow: %w", err)
	}
	out.Status = wf.Status
	return out, nil
}

// runs reports whether an Action runs, given its rendered if field: empty or
// "true" in any case runs it, anything else skips it.
func runs(cond string) bool {
	return cond == "" || strings.EqualFold(cond, "true")
}

// mergeEnv returns the Task's environment with the Workflow's globals and the task's extra applied over it.
func mergeEnv(task map[string]string, extras ...*v1alpha2.Extra) map[string]string {
	out := maps.Clone(task)
	for _, e := range extras {
		if e == nil || len(e.Env) == 0 {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		maps.Copy(out, e.Env)
	}
	return out
}

// mergeVolumes returns the Task's volumes followed by those of the Workflow's globals and the task's extra.
func mergeVolumes(task []v1alpha2.Volume, extras ...*v1alpha2.Extra) []v1alpha2.Volume {
	out := slices.Clone(task)
	for _, e := range extras {
		if e != nil {
			out = append(out, e.Volumes...)
		}
	}
	return out
}

// Paths a Workflow does not render: everything but its spec, its references, and the references to the
// Hardware and Task each task uses, which are read before rendering.
var workflowRefPath = regexp.MustCompile(`^spec\.tasks\[\d+\]\.(taskRef|hardware\.hardwareRef)($|[.\[])`)

func skipWorkflowPath(path string) bool {
	return skipOutsideSpec(path) || workflowRefPath.MatchString(path)
}

// skipTaskPath reports the paths a Task does not render: everything but its spec, and its references.
func skipTaskPath(path string) bool {
	return skipOutsideSpec(path)
}

func skipOutsideSpec(path string) bool {
	rest, ok := strings.CutPrefix(path, "spec")
	if !ok || (rest != "" && rest[0] != '.' && rest[0] != '[') {
		return true
	}
	return rest == ".references" || strings.HasPrefix(rest, ".references.") || strings.HasPrefix(rest, ".references[")
}
