package kube

import (
	"context"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListWorkflowRuleSets lists the deprecated v1alpha1 WorkflowRuleSets. Once
// their CRD is removed, there are none.
func (b *Backend) ListWorkflowRuleSets(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha1.WorkflowRuleSet, error) {
	list := &v1alpha1.WorkflowRuleSetList{}
	lo := []client.ListOption{}
	if opts.InNamespace != "" {
		lo = append(lo, client.InNamespace(opts.InNamespace))
	}
	err := b.cluster.GetClient().List(ctx, list, lo...)
	if meta.IsNoMatchError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return list.Items, nil
}
