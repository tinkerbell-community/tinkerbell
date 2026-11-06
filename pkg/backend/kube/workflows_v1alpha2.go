package kube

import (
	"context"
	"fmt"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateWorkflowV1Alpha2 creates w.
func (b *Backend) CreateWorkflowV1Alpha2(ctx context.Context, w *v1alpha2.Workflow) error {
	if err := b.cluster.GetClient().Create(ctx, w); err != nil {
		return fmt.Errorf("failed to create workflow %s: %w", w.Name, err)
	}

	return nil
}

// ReadWorkflowV1Alpha2 returns the Workflow as currently stored by the API
// server, not as cached, so the result is never stale. A Workflow outside
// b.Namespaces is reported as not found.
func (b *Backend) ReadWorkflowV1Alpha2(ctx context.Context, name, namespace string) (*v1alpha2.Workflow, error) {
	if !b.Watches(namespace) {
		return nil, apierrors.NewNotFound(v1alpha2.GroupVersion.WithResource("workflows").GroupResource(), name)
	}
	wflw := &v1alpha2.Workflow{}
	if err := b.cluster.GetAPIReader().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, wflw); err != nil {
		return nil, fmt.Errorf("failed to get workflow %s/%s: %w", namespace, name, err)
	}
	return wflw, nil
}

// ListWorkflowsV1Alpha2 returns the Workflows matching opts. ByAgentID matches
// the Agent running the current Task (status.metadata.task.agentID),
// ByTaskAgentID any Agent a Task is assigned to (spec.tasks[].agentID).
func (b *Backend) ListWorkflowsV1Alpha2(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha2.Workflow, error) {
	los := []client.ListOption{}
	if opts.InNamespace != "" {
		los = append(los, client.InNamespace(opts.InNamespace))
	}
	// A single MatchingFields: each one replaces the field selector of the one before.
	fields := client.MatchingFields{}
	if opts.ByAgentID != "" {
		fields[WorkflowAgentIDIndexV1Alpha2] = opts.ByAgentID
	}
	if opts.ByTaskAgentID != "" {
		fields[WorkflowTaskAgentIDIndexV1Alpha2] = opts.ByTaskAgentID
	}
	if len(fields) > 0 {
		los = append(los, fields)
	}
	stored := &v1alpha2.WorkflowList{}
	if err := b.cluster.GetClient().List(ctx, stored, los...); err != nil {
		return nil, fmt.Errorf("failed to list workflows in namespace %s: %w", opts.InNamespace, err)
	}

	return stored.Items, nil
}

// UpdateWorkflowV1Alpha2 writes wf as opts asks, see Backend.update.
func (b *Backend) UpdateWorkflowV1Alpha2(ctx context.Context, wf *v1alpha2.Workflow, opts data.UpdateOptions) error {
	return b.update(ctx, wf, "workflow", wf.Name, opts)
}
