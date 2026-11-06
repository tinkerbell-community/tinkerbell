package kube

import (
	"context"
	"fmt"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (b *Backend) CreateWorkflow(ctx context.Context, w *v1alpha1.Workflow) error {
	if err := b.cluster.GetClient().Create(ctx, w); err != nil {
		return fmt.Errorf("failed to create workflow %s: %w", w.Name, err)
	}

	return nil
}

// ReadWorkflow returns the Workflow as currently stored by the API server, not as cached,
// so the result is never stale. A Workflow outside b.Namespaces is reported as not found.
func (b *Backend) ReadWorkflow(ctx context.Context, name, namespace string) (*v1alpha1.Workflow, error) {
	wflw := &v1alpha1.Workflow{}
	if err := b.readWorkflowInto(ctx, wflw, name, namespace); err != nil {
		return nil, err
	}
	return wflw, nil
}

// readWorkflowInto reads the Workflow name in namespace into wf, of either version,
// as ReadWorkflow does.
func (b *Backend) readWorkflowInto(ctx context.Context, wf client.Object, name, namespace string) error {
	if !b.Watches(namespace) {
		return apierrors.NewNotFound(v1alpha1.GroupVersion.WithResource("workflows").GroupResource(), name)
	}
	if err := b.cluster.GetAPIReader().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, wf); err != nil {
		return fmt.Errorf("failed to get workflow %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (b *Backend) ListWorkflows(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha1.Workflow, error) {
	stored := &v1alpha1.WorkflowList{}
	if err := b.listWorkflowsInto(ctx, stored, WorkflowAgentIDIndex, "", opts); err != nil {
		return nil, err
	}

	return stored.Items, nil
}

// listWorkflowsInto lists the Workflows matching opts into list, of either version,
// finding them by current agent with the index agentIndex and by task agent with
// taskAgentIndex. A version without a task agent index passes an empty one.
func (b *Backend) listWorkflowsInto(ctx context.Context, list client.ObjectList, agentIndex, taskAgentIndex string, opts data.WorkflowFilter) error {
	los := []client.ListOption{}
	if opts.InNamespace != "" {
		los = append(los, client.InNamespace(opts.InNamespace))
	}
	fields := client.MatchingFields{}
	if opts.ByAgentID != "" {
		fields[agentIndex] = opts.ByAgentID
	}
	if opts.ByTaskAgentID != "" {
		if taskAgentIndex == "" {
			return fmt.Errorf("failed to list workflows: listing by task agent is not supported for %T", list)
		}
		fields[taskAgentIndex] = opts.ByTaskAgentID
	}
	if len(fields) > 0 {
		los = append(los, fields)
	}
	if err := b.cluster.GetClient().List(ctx, list, los...); err != nil {
		return fmt.Errorf("failed to list workflows in namespace %s: %w", opts.InNamespace, err)
	}
	return nil
}

func (b *Backend) UpdateWorkflow(ctx context.Context, wf *v1alpha1.Workflow, opts data.UpdateOptions) error {
	return b.update(ctx, wf, "workflow", wf.Name, opts)
}
