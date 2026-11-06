package translate

import (
	"context"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TemplateReconciler keeps the Tasks of each Template: the Template's tasks
// translated to v1alpha2, when its data allows it.
type TemplateReconciler struct {
	client   client.Client
	recorder events.EventRecorder
}

// NewTemplateReconciler returns a TemplateReconciler.
func NewTemplateReconciler(c client.Client, recorder events.EventRecorder) *TemplateReconciler {
	return &TemplateReconciler{client: c, recorder: recorder}
}

// SetupWithManager registers the reconciler, for Templates and the Tasks they own.
func (r *TemplateReconciler) SetupWithManager(mgr ctrl.Manager, opts controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("template-translation").
		WithOptions(opts).
		For(&v1alpha1.Template{}).
		Owns(&v1alpha2.Task{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=tinkerbell.org,resources=templates,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=tinkerbell.org,resources=tasks,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile translates a Template to Tasks. A Template that does not translate
// is annotated with the reason and keeps no Tasks.
func (r *TemplateReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	tpl := &v1alpha1.Template{}
	if err := r.client.Get(ctx, req.NamespacedName, tpl); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !tpl.DeletionTimestamp.IsZero() {
		// The garbage collector deletes the Tasks it owns.
		return reconcile.Result{}, nil
	}

	conv, err := ConvertTemplate(tpl)
	if err == nil {
		err = EnsureTemplateTasks(ctx, r.client, tpl, conv)
	}
	if err != nil && !IsNotTranslated(err) {
		return reconcile.Result{}, err
	}
	reason := ""
	if err != nil {
		reason = err.Error()
		if derr := EnsureTemplateTasks(ctx, r.client, tpl, nil); derr != nil {
			return reconcile.Result{}, derr
		}
	}
	changed, perr := setTranslationAnnotation(ctx, r.client, tpl, reason)
	if perr != nil {
		return reconcile.Result{}, perr
	}
	if changed && reason != "" {
		ctrl.LoggerFrom(ctx).Info("template has no v1alpha2 translation; Workflows using it run in v1alpha1 compatibility mode", "reason", reason)
		r.recorder.Eventf(tpl, nil, corev1.EventTypeWarning, reasonTranslationFailed, "TranslateToTasks", "not translated to v1alpha2 Tasks: %s", reason)
	}
	return reconcile.Result{}, nil
}
