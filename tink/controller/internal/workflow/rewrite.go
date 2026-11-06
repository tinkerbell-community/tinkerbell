package workflow

import (
	"context"
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"github.com/tinkerbell/tinkerbell/tink/controller/internal/translate"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Event reasons of the rewrite of v1alpha1 Workflows onto Tasks.
const (
	reasonRewritten     = "RewrittenToTasks"
	reasonCompatibility = "V1Alpha1CompatibilityMode"
)

// rewriteToTasks rewrites a Workflow created through v1alpha1, which has not started, to run the Tasks its
// Template translates to, so that it runs natively (see conversion.RewriteWorkflowToTasks). It reports
// whether it rewrote it; the next reconcile then renders the Tasks. A Workflow that cannot be rewritten runs
// in v1alpha1 compatibility mode, and an event says why. Errors are those worth retrying.
func (r *Reconciler) rewriteToTasks(ctx context.Context, wf *v1alpha2.Workflow) (bool, error) {
	if r.compatOnly || !engine.Compat(wf) || wf.Status.Metadata.Workflow.State != 0 || len(wf.Status.RenderedTasks) > 0 {
		return false, nil
	}
	tpl := &v1alpha1.Template{}
	if err := r.client.Get(ctx, ctrlclient.ObjectKey{Name: wf.Annotations[conversion.AnnotationTemplate], Namespace: wf.Namespace}, tpl); err != nil {
		// Compatibility mode reports a missing Template as it always has.
		return false, nil //nolint:nilerr // rendering in compatibility mode reports it
	}
	compat := func(reason string) (bool, error) {
		journal.Log(ctx, "running in v1alpha1 compatibility mode", "reason", reason)
		ctrl.LoggerFrom(ctx).V(1).Info("Workflow runs in v1alpha1 compatibility mode", "reason", reason)
		r.event(wf, corev1.EventTypeNormal, reasonCompatibility, "RewriteToTasks", "runs Template %s in v1alpha1 compatibility mode: %s", tpl.Name, reason)
		return false, nil
	}
	if wf.Spec.Globals != nil {
		// In v1alpha1 a hardwareMap entry overrides .metadata_url; a Task's .metadata_url is the Tink
		// controller's.
		if _, ok := wf.Spec.Globals.TemplateMap[templateDataMetadataURL]; ok {
			return compat("its hardwareMap sets metadata_url")
		}
	}
	conv, err := translate.ConvertTemplate(tpl)
	if err != nil {
		return compat(err.Error())
	}
	rewritten := wf.DeepCopy()
	if err := conversion.RewriteWorkflowToTasks(rewritten, tpl, conv); err != nil {
		return compat(err.Error())
	}
	// The Tasks the Template translates to, as the Template translator keeps them.
	if err := translate.EnsureTemplateTasks(ctx, r.client, tpl, conv); err != nil {
		if translate.IsNotTranslated(err) {
			return compat(err.Error())
		}
		return false, err
	}
	if err := r.client.Update(ctx, rewritten); err != nil {
		return false, fmt.Errorf("rewriting workflow %s/%s onto the Tasks of template %s: %w", wf.Namespace, wf.Name, tpl.Name, err)
	}
	journal.Log(ctx, "rewritten onto Tasks", "template", tpl.Name)
	r.event(wf, corev1.EventTypeNormal, reasonRewritten, "RewriteToTasks", "runs the Tasks Template %s translates to", tpl.Name)
	return true, nil
}

func (r *Reconciler) event(obj *v1alpha2.Workflow, eventtype, reason, action, note string, args ...any) {
	if r.recorder != nil {
		r.recorder.Eventf(obj, nil, eventtype, reason, action, note, args...)
	}
}
