package crd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	apiv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeprecatedCRDs are the CRDs of the kinds that only exist in v1alpha1, in the
// order RemoveDeprecated removes them: WorkflowRuleSets read their Template, so
// they go before Templates.
var DeprecatedCRDs = []string{
	"workflowrulesets.tinkerbell.org",
	"templates.tinkerbell.org",
	"machines.bmc.tinkerbell.org",
	"tasks.bmc.tinkerbell.org",
}

const (
	// annotationTranslation is set by the Tink controller on a Template or
	// WorkflowRuleSet that has no v1alpha2 translation, to the reason why.
	annotationTranslation = "tinkerbell.org/v1alpha2-translation"

	// crdDeletionTimeout bounds how long RemoveDeprecated waits for the API
	// server to delete a CRD's objects and the CRD.
	crdDeletionTimeout = 2 * time.Minute

	// maxLoggedBlockers is how many of the reasons a CRD is kept are logged.
	maxLoggedBlockers = 10
)

func isDeprecated(name string) bool { return slices.Contains(DeprecatedCRDs, name) }

// deprecatedScheme holds the kinds RemoveDeprecated reads.
func deprecatedScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, v1alpha2.AddToScheme, bmcv1.AddToScheme, bmcv2.AddToScheme} {
		if err := add(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// managed reports whether the migration installs the CRD name, waits for it and
// migrates its objects. With deprecated CRD removal, a deprecated CRD is only
// managed while it exists and is not being deleted, so that one that was
// removed is not installed again.
func (t Tinkerbell) managed(ctx context.Context, name string) (bool, error) {
	if !t.removeDeprecated || !isDeprecated(name) {
		return true, nil
	}
	c, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get CRD %s: %w", name, err)
	}
	return c.DeletionTimestamp == nil, nil
}

// RemoveDeprecated removes the CRDs of the kinds that only exist in v1alpha1
// (DeprecatedCRDs) once their objects have moved to v1alpha2. The Tink
// controller and Rufio move them while they run: Templates to Tasks,
// WorkflowRuleSets to Policies, bmc Machines to the BMC connection of the
// Hardware that refers to them (see docs/technical/v1alpha2/MIGRATION.md).
//
// A CRD is kept while any of its objects has not moved, or is still needed:
// a Workflow that has not started yet and runs its Template in v1alpha1
// compatibility mode, a bmc Job that has not finished and reads its connection
// from a Machine or continues from the Tasks an older Rufio created, a bmc Task
// that has not finished. The reasons are logged, and the CRD is checked again
// the next time the migration runs.
//
// Before a CRD is deleted, which deletes its objects, the objects that moved
// are detached from it: owner references to its kind are removed from the
// objects of the other Tinkerbell CRDs, so that the garbage collector does not
// delete the translations (and the Workflows a WorkflowRuleSet created) along
// with the objects they came from; and Hardware whose BMC connection was
// translated from a bmc Machine stops following the Machine and keeps the
// connection as its own.
func (t Tinkerbell) RemoveDeprecated(ctx context.Context) error {
	if t.kube == nil || t.Dynamic == nil {
		return errors.New("removing deprecated CRDs needs a rest config")
	}
	for _, name := range DeprecatedCRDs {
		c, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to get CRD %s: %w", name, err)
		}
		if c.DeletionTimestamp == nil {
			blockers, err := t.blockers(ctx, name)
			if err != nil {
				return fmt.Errorf("checking whether deprecated CRD %s can be removed: %w", name, err)
			}
			if len(blockers) > 0 {
				t.info("keeping deprecated CRD: some of its objects have not moved to v1alpha2 or are still in use",
					"crd", name, "count", len(blockers), "reasons", blockers[:min(len(blockers), maxLoggedBlockers)])
				continue
			}
			if err := t.detach(ctx, c); err != nil {
				return fmt.Errorf("detaching v1alpha2 objects from deprecated CRD %s: %w", name, err)
			}
			if err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, name, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &c.UID},
			}); err != nil && !isNotFound(err) {
				return fmt.Errorf("failed to delete CRD %s: %w", name, err)
			}
		}
		if gone, err := t.waitDeleted(ctx, name); err != nil {
			return err
		} else if !gone {
			t.info("deprecated CRD is still being deleted; its objects may have finalizers", "crd", name)
			continue
		}
		t.info("removed deprecated CRD", "crd", name)
	}
	return nil
}

// waitDeleted waits for the CRD name to be deleted. It reports false if it is
// still there after crdDeletionTimeout.
func (t Tinkerbell) waitDeleted(ctx context.Context, name string) (bool, error) {
	err := wait.PollUntilContextTimeout(ctx, time.Second, crdDeletionTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if isNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err == nil {
		return true, nil
	}
	if ctx.Err() == nil && wait.Interrupted(err) {
		return false, nil
	}
	return false, fmt.Errorf("waiting for CRD %s to be deleted: %w", name, err)
}

func (t Tinkerbell) info(msg string, kv ...any) {
	if t.Logger.GetSink() != nil {
		t.Logger.Info(msg, kv...)
	}
}

// blockers returns why the deprecated CRD name cannot be removed yet, one
// reason per object; none if it can.
func (t Tinkerbell) blockers(ctx context.Context, name string) ([]string, error) {
	switch name {
	case "workflowrulesets.tinkerbell.org":
		return t.ruleSetBlockers(ctx)
	case "templates.tinkerbell.org":
		return t.templateBlockers(ctx)
	case "machines.bmc.tinkerbell.org":
		return t.machineBlockers(ctx)
	case "tasks.bmc.tinkerbell.org":
		return t.bmcTaskBlockers(ctx)
	}
	return nil, fmt.Errorf("unknown deprecated CRD %s", name)
}

// controllerUID returns the UID of the controller of obj, if it has one.
func controllerUID(obj metav1.Object) types.UID {
	if ref := metav1.GetControllerOf(obj); ref != nil {
		return ref.UID
	}
	return ""
}

func key(namespace, name string) string { return namespace + "/" + name }

// ruleSetBlockers: a WorkflowRuleSet has moved when the Tink controller
// translated it to a Policy of its name, which it controls.
func (t Tinkerbell) ruleSetBlockers(ctx context.Context) ([]string, error) {
	ruleSets := &v1alpha1.WorkflowRuleSetList{}
	if err := t.kube.List(ctx, ruleSets); err != nil {
		return nil, fmt.Errorf("listing WorkflowRuleSets: %w", err)
	}
	if len(ruleSets.Items) == 0 {
		return nil, nil
	}
	policies := &v1alpha2.PolicyList{}
	if err := t.kube.List(ctx, policies); err != nil {
		return nil, fmt.Errorf("listing Policies: %w", err)
	}
	policyOwner := map[string]types.UID{}
	for i := range policies.Items {
		p := &policies.Items[i]
		policyOwner[key(p.Namespace, p.Name)] = controllerUID(p)
	}

	var out []string
	for _, rs := range ruleSets.Items {
		switch {
		case rs.DeletionTimestamp != nil, policyOwner[key(rs.Namespace, rs.Name)] == rs.UID:
		case rs.Annotations[annotationTranslation] != "":
			out = append(out, fmt.Sprintf("WorkflowRuleSet %s/%s has no v1alpha2 Policy: %s", rs.Namespace, rs.Name, rs.Annotations[annotationTranslation]))
		default:
			out = append(out, fmt.Sprintf("WorkflowRuleSet %s/%s is not translated to a Policy yet", rs.Namespace, rs.Name))
		}
	}
	return out, nil
}

// templateBlockers: a Template has moved when the Tink controller translated it
// to Tasks, one per template task, which it controls. It is still needed by
// the WorkflowRuleSets that use it, and by Workflows that have not started and
// run it in v1alpha1 compatibility mode: the Workflow engine renders a
// Template when a Workflow starts.
func (t Tinkerbell) templateBlockers(ctx context.Context) ([]string, error) {
	if _, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, "workflowrulesets.tinkerbell.org", metav1.GetOptions{}); err == nil {
		return []string{"the WorkflowRuleSet CRD is not removed yet, and WorkflowRuleSets read their Template"}, nil
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("failed to get CRD workflowrulesets.tinkerbell.org: %w", err)
	}

	templates := &v1alpha1.TemplateList{}
	if err := t.kube.List(ctx, templates); err != nil {
		return nil, fmt.Errorf("listing Templates: %w", err)
	}
	if len(templates.Items) == 0 {
		return nil, nil
	}
	tasks := &v1alpha2.TaskList{}
	if err := t.kube.List(ctx, tasks); err != nil {
		return nil, fmt.Errorf("listing Tasks: %w", err)
	}
	taskOwner := map[string]types.UID{}
	for i := range tasks.Items {
		task := &tasks.Items[i]
		taskOwner[key(task.Namespace, task.Name)] = controllerUID(task)
	}
	workflows := &v1alpha2.WorkflowList{}
	if err := t.kube.List(ctx, workflows); err != nil {
		return nil, fmt.Errorf("listing Workflows: %w", err)
	}

	var out []string
	exists := map[string]bool{}
	for i := range templates.Items {
		tpl := &templates.Items[i]
		exists[key(tpl.Namespace, tpl.Name)] = true
		if tpl.DeletionTimestamp != nil {
			continue
		}
		if reason := tpl.Annotations[annotationTranslation]; reason != "" {
			out = append(out, fmt.Sprintf("Template %s/%s has no v1alpha2 translation: %s", tpl.Namespace, tpl.Name, reason))
			continue
		}
		if tpl.Spec.Data == nil {
			out = append(out, fmt.Sprintf("Template %s/%s has no data", tpl.Namespace, tpl.Name))
			continue
		}
		conv, err := conversion.ConvertTemplate(*tpl.Spec.Data, funcmap.New())
		if err != nil {
			out = append(out, fmt.Sprintf("Template %s/%s has no v1alpha2 translation: %v", tpl.Namespace, tpl.Name, err))
			continue
		}
		for _, task := range conversion.TemplateTaskNames(tpl.Name, conv) {
			if taskOwner[key(tpl.Namespace, task)] != tpl.UID {
				out = append(out, fmt.Sprintf("Template %s/%s is not translated to Tasks yet: Task %s is missing", tpl.Namespace, tpl.Name, task))
				break
			}
		}
	}
	for i := range workflows.Items {
		wf := &workflows.Items[i]
		tpl := wf.Annotations[conversion.AnnotationTemplate]
		if tpl == "" || !exists[key(wf.Namespace, tpl)] || !compat(wf) || wf.DeletionTimestamp != nil ||
			wf.Status.Metadata.Workflow.State != 0 || len(wf.Status.RenderedTasks) > 0 {
			continue
		}
		out = append(out, fmt.Sprintf("Workflow %s/%s has not started and runs Template %s in v1alpha1 compatibility mode", wf.Namespace, wf.Name, tpl))
	}
	return out, nil
}

// compat reports whether wf runs in v1alpha1 compatibility mode: it was
// created through v1alpha1 and was not rewritten onto Tasks.
func compat(wf *v1alpha2.Workflow) bool {
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

// jobFinished reports whether a bmc Job has completed or failed.
func jobFinished(job *bmcv2.Job) bool {
	return bmcv2.HasConditionStatus(job.Status.Conditions, bmcv2.ConditionTypeCompleted, bmcv2.ConditionStatusTrue) ||
		bmcv2.HasConditionStatus(job.Status.Conditions, bmcv2.ConditionTypeFailed, bmcv2.ConditionStatusTrue)
}

// machineBlockers: a bmc Machine has moved when Rufio translated it into the
// BMC connection of a Hardware that refers to it, which then carries the
// conversion.AnnotationMachine annotation. It is still needed by Hardware that
// refers to it and has no connection yet, and by Jobs that have not finished
// and read their connection from it.
func (t Tinkerbell) machineBlockers(ctx context.Context) ([]string, error) {
	machines := &bmcv1.MachineList{}
	if err := t.kube.List(ctx, machines); err != nil {
		return nil, fmt.Errorf("listing bmc Machines: %w", err)
	}
	if len(machines.Items) == 0 {
		return nil, nil
	}
	exists := map[string]bool{}
	for _, m := range machines.Items {
		exists[key(m.Namespace, m.Name)] = true
	}
	hardware := &v1alpha2.HardwareList{}
	if err := t.kube.List(ctx, hardware); err != nil {
		return nil, fmt.Errorf("listing Hardware: %w", err)
	}
	jobs := &bmcv2.JobList{}
	if err := t.kube.List(ctx, jobs); err != nil {
		return nil, fmt.Errorf("listing bmc Jobs: %w", err)
	}

	var out []string
	translated := map[string]bool{}
	for i := range hardware.Items {
		hw := &hardware.Items[i]
		hasConnection := hw.Spec.BMC != nil && hw.Spec.BMC.Connection != nil
		if m := hw.Annotations[conversion.AnnotationMachine]; m != "" && hasConnection {
			translated[m] = true
		}
		if ref := conversion.HardwareBMCRef(hw); ref != nil && ref.Kind == "Machine" && !hasConnection &&
			(ref.APIGroup == nil || *ref.APIGroup == "" || *ref.APIGroup == bmcv1.GroupVersion.Group) && exists[key(hw.Namespace, ref.Name)] {
			out = append(out, fmt.Sprintf("Hardware %s/%s refers to bmc Machine %s and has no BMC connection yet", hw.Namespace, hw.Name, ref.Name))
		}
	}
	for _, m := range machines.Items {
		if m.DeletionTimestamp == nil && !translated[key(m.Namespace, m.Name)] {
			out = append(out, fmt.Sprintf("bmc Machine %s/%s is not translated into the BMC connection of a Hardware (yet, or no Hardware refers to it)", m.Namespace, m.Name))
		}
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		m := job.Annotations[conversion.AnnotationMachine]
		if m == "" || !exists[m] || jobFinished(job) || job.Spec.Connection != nil || (job.Spec.HardwareRef != nil && job.Spec.HardwareRef.Name != "") {
			continue
		}
		out = append(out, fmt.Sprintf("bmc Job %s/%s has not finished and reads its BMC connection from Machine %s", job.Namespace, job.Name, m))
	}
	return out, nil
}

// bmcTaskBlockers: bmc Tasks are not translated. Rufio runs them until they
// finish, and continues a Job an older Rufio started from the Tasks it created.
func (t Tinkerbell) bmcTaskBlockers(ctx context.Context) ([]string, error) {
	tasks := &bmcv1.TaskList{}
	if err := t.kube.List(ctx, tasks); err != nil {
		return nil, fmt.Errorf("listing bmc Tasks: %w", err)
	}
	if len(tasks.Items) == 0 {
		return nil, nil
	}
	jobs := &bmcv2.JobList{}
	if err := t.kube.List(ctx, jobs); err != nil {
		return nil, fmt.Errorf("listing bmc Jobs: %w", err)
	}
	running := map[types.UID]*bmcv2.Job{}
	for i := range jobs.Items {
		if job := &jobs.Items[i]; !jobFinished(job) {
			running[job.UID] = job
		}
	}

	var out []string
	reported := map[types.UID]bool{}
	for _, task := range tasks.Items {
		if task.DeletionTimestamp != nil {
			continue
		}
		if !task.HasCondition(bmcv1.TaskCompleted, bmcv1.ConditionTrue) && !task.HasCondition(bmcv1.TaskFailed, bmcv1.ConditionTrue) {
			out = append(out, fmt.Sprintf("bmc Task %s/%s has not finished", task.Namespace, task.Name))
			continue
		}
		if uid := controllerUID(&task); running[uid] != nil && !reported[uid] {
			reported[uid] = true
			out = append(out, fmt.Sprintf("bmc Job %s/%s has not finished and continues from the Tasks an older Rufio created", running[uid].Namespace, running[uid].Name))
		}
	}
	return out, nil
}

// detach prepares the deletion of the deprecated CRD c (see RemoveDeprecated).
func (t Tinkerbell) detach(ctx context.Context, c *apiv1.CustomResourceDefinition) error {
	gk := schema.GroupKind{Group: c.Spec.Group, Kind: c.Spec.Names.Kind}
	for name := range t.CRDs {
		if name == c.Name {
			continue
		}
		other, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to get CRD %s: %w", name, err)
		}
		if err := t.removeOwnerReferences(ctx, other, gk); err != nil {
			return err
		}
	}
	if c.Name == "machines.bmc.tinkerbell.org" {
		return t.keepTranslatedConnections(ctx)
	}
	return nil
}

// removeOwnerReferences removes the owner references to objects of kind gk
// from the objects of the CRD c.
func (t Tinkerbell) removeOwnerReferences(ctx context.Context, c *apiv1.CustomResourceDefinition, gk schema.GroupKind) error {
	version := ""
	for _, v := range c.Spec.Versions {
		if v.Storage {
			version = v.Name
		}
	}
	if version == "" {
		return nil
	}
	res := t.Dynamic.Resource(gvr(c, version))
	owned := func(refs []metav1.OwnerReference) ([]metav1.OwnerReference, bool) {
		kept := slices.DeleteFunc(slices.Clone(refs), func(ref metav1.OwnerReference) bool {
			return schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind).GroupKind() == gk
		})
		return kept, len(kept) != len(refs)
	}

	opts := metav1.ListOptions{Limit: 250}
	for {
		list, err := res.List(ctx, opts)
		if err != nil {
			return fmt.Errorf("listing %s: %w", c.Spec.Names.Plural, err)
		}
		for _, item := range list.Items {
			if _, ok := owned(item.GetOwnerReferences()); !ok {
				continue
			}
			ri := res.Namespace(item.GetNamespace())
			current := &item
			if err := retry.Do(func() error {
				kept, ok := owned(current.GetOwnerReferences())
				if !ok {
					return nil
				}
				obj := current.DeepCopy()
				obj.SetOwnerReferences(kept)
				_, err := ri.Update(ctx, obj, metav1.UpdateOptions{FieldManager: fieldManager})
				if err == nil || isNotFound(err) {
					return nil
				}
				if isConflict(err) {
					fresh, gerr := ri.Get(ctx, current.GetName(), metav1.GetOptions{})
					if gerr != nil {
						if isNotFound(gerr) {
							return nil
						}
						return gerr
					}
					current = fresh
				}
				return err
			}, retry.Attempts(5), retry.Delay(200*time.Millisecond), retry.Context(ctx), retry.LastErrorOnly(true)); err != nil {
				return fmt.Errorf("removing owner references to %s from %s %s/%s: %w", gk, c.Spec.Names.Singular, item.GetNamespace(), item.GetName(), err)
			}
		}
		if list.GetContinue() == "" {
			return nil
		}
		opts.Continue = list.GetContinue()
	}
}

// keepTranslatedConnections makes the BMC connections Rufio translated from bmc
// Machines the Hardware's own: it removes the conversion.AnnotationMachine
// annotation, which makes Rufio keep the connection in sync with the Machine
// and report the Machine's state, so that Rufio polls the BMC itself.
func (t Tinkerbell) keepTranslatedConnections(ctx context.Context) error {
	hardware := &v1alpha2.HardwareList{}
	if err := t.kube.List(ctx, hardware); err != nil {
		return fmt.Errorf("listing Hardware: %w", err)
	}
	for i := range hardware.Items {
		hw := &hardware.Items[i]
		if hw.Annotations[conversion.AnnotationMachine] == "" {
			continue
		}
		if err := retry.Do(func() error {
			if hw.Annotations[conversion.AnnotationMachine] == "" {
				return nil
			}
			patched := hw.DeepCopy()
			delete(patched.Annotations, conversion.AnnotationMachine)
			err := t.kube.Patch(ctx, patched, client.MergeFromWithOptions(hw, client.MergeFromWithOptimisticLock{}), client.FieldOwner(fieldManager))
			if err == nil || isNotFound(err) {
				return nil
			}
			if isConflict(err) {
				fresh := &v1alpha2.Hardware{}
				if gerr := t.kube.Get(ctx, client.ObjectKeyFromObject(hw), fresh); gerr != nil {
					return client.IgnoreNotFound(gerr)
				}
				hw = fresh
			}
			return err
		}, retry.Attempts(5), retry.Delay(200*time.Millisecond), retry.Context(ctx), retry.LastErrorOnly(true)); err != nil {
			return fmt.Errorf("keeping the BMC connection of Hardware %s/%s: %w", hw.Namespace, hw.Name, err)
		}
	}
	return nil
}
