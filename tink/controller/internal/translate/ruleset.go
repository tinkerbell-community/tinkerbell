package translate

import (
	"context"
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// RuleSetReconciler keeps the Policy of each WorkflowRuleSet: a Policy of the
// same name, owned by the rule set, that creates the same Workflows by running
// the Tasks of the rule set's Template. The Tink server enrolls Agents through
// that Policy instead of the rule set, so a rule set without a Policy keeps
// creating Workflows as before.
type RuleSetReconciler struct {
	client   client.Client
	recorder events.EventRecorder
}

// NewRuleSetReconciler returns a RuleSetReconciler.
func NewRuleSetReconciler(c client.Client, recorder events.EventRecorder) *RuleSetReconciler {
	return &RuleSetReconciler{client: c, recorder: recorder}
}

// SetupWithManager registers the reconciler, for WorkflowRuleSets, the Policies
// they own and the Templates they use.
func (r *RuleSetReconciler) SetupWithManager(mgr ctrl.Manager, opts controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("workflowruleset-translation").
		WithOptions(opts).
		For(&v1alpha1.WorkflowRuleSet{}).
		Owns(&v1alpha2.Policy{}).
		Watches(&v1alpha1.Template{}, handler.EnqueueRequestsFromMapFunc(r.ruleSetsUsing)).
		Complete(r)
}

// ruleSetsUsing returns the rule sets that use the Template obj.
func (r *RuleSetReconciler) ruleSetsUsing(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &v1alpha1.WorkflowRuleSetList{}
	if err := r.client.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "listing WorkflowRuleSets using a Template", "template", obj.GetName())
		return nil
	}
	var reqs []reconcile.Request
	for _, rs := range list.Items {
		if rs.Spec.Workflow.Template.Ref == obj.GetName() {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rs.Namespace, Name: rs.Name}})
		}
	}
	return reqs
}

// +kubebuilder:rbac:groups=tinkerbell.org,resources=workflowrulesets,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=tinkerbell.org,resources=policies,verbs=get;list;watch;create;update;delete

// Reconcile translates a WorkflowRuleSet to a Policy. A rule set that does not
// translate is annotated with the reason and keeps no Policy.
func (r *RuleSetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rs := &v1alpha1.WorkflowRuleSet{}
	if err := r.client.Get(ctx, req.NamespacedName, rs); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !rs.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	err := r.translate(ctx, rs)
	if err != nil && !IsNotTranslated(err) {
		return reconcile.Result{}, err
	}
	reason := ""
	if err != nil {
		reason = err.Error()
		if derr := r.deletePolicy(ctx, rs); derr != nil {
			return reconcile.Result{}, derr
		}
	}
	changed, perr := setTranslationAnnotation(ctx, r.client, rs, reason)
	if perr != nil {
		return reconcile.Result{}, perr
	}
	if changed && reason != "" {
		ctrl.LoggerFrom(ctx).Info("WorkflowRuleSet has no v1alpha2 Policy; it creates v1alpha1 compatibility mode Workflows", "reason", reason)
		r.recorder.Eventf(rs, nil, corev1.EventTypeWarning, reasonTranslationFailed, "TranslateToPolicy", "not translated to a v1alpha2 Policy: %s", reason)
	}
	return reconcile.Result{}, nil
}

func (r *RuleSetReconciler) translate(ctx context.Context, rs *v1alpha1.WorkflowRuleSet) error {
	// The Tink server skips such rule sets; a Policy would enable them.
	if ns := rs.Spec.Workflow.Namespace; ns != "" && ns != rs.Namespace { //nolint:staticcheck // read only to refuse a mismatch until the field is removed
		return notTranslated("spec.workflow.namespace %q is not the rule set's namespace", ns)
	}
	tpl := &v1alpha1.Template{}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: rs.Spec.Workflow.Template.Ref}, tpl); err != nil {
		if apierrors.IsNotFound(err) {
			return notTranslated("template %q not found", rs.Spec.Workflow.Template.Ref)
		}
		return fmt.Errorf("getting template %s/%s: %w", rs.Namespace, rs.Spec.Workflow.Template.Ref, err)
	}
	conv, err := ConvertTemplate(tpl)
	if err != nil {
		return err
	}
	policy, err := conversion.WorkflowRuleSetToPolicy(rs, conv)
	if err != nil {
		return err
	}
	// The Policy's Workflows run the Template's Tasks.
	if err := EnsureTemplateTasks(ctx, r.client, tpl, conv); err != nil {
		return err
	}
	policy.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: v1alpha1.GroupVersion.String(),
		Kind:       "WorkflowRuleSet",
		Name:       rs.Name,
		UID:        rs.UID,
		Controller: ptr(true),
	}}
	return ensureOwned(ctx, r.client, policy, &v1alpha2.Policy{}, rs.UID, func(existing, want *v1alpha2.Policy) bool {
		if equality.Semantic.DeepEqual(existing.Spec, want.Spec) {
			return false
		}
		existing.Spec = want.Spec
		return true
	})
}

// deletePolicy deletes the Policy translated from rs, if it has one.
func (r *RuleSetReconciler) deletePolicy(ctx context.Context, rs *v1alpha1.WorkflowRuleSet) error {
	policy := &v1alpha2.Policy{}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: rs.Name}, policy); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !controlledBy(policy, rs.UID) {
		return nil
	}
	if err := r.client.Delete(ctx, policy); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting policy %s/%s: %w", policy.Namespace, policy.Name, err)
	}
	return nil
}
