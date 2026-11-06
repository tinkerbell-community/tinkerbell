package kube

import (
	"context"
	"fmt"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

// CreateWorkflowV1Alpha2 is CreateWorkflow for v1alpha2 Workflows.
func (b *Backend) CreateWorkflowV1Alpha2(ctx context.Context, w *v1alpha2.Workflow) error {
	if err := b.cluster.GetClient().Create(ctx, w); err != nil {
		return fmt.Errorf("failed to create workflow %s: %w", w.Name, err)
	}

	return nil
}

// ReadWorkflowV1Alpha2 is ReadWorkflow for v1alpha2 Workflows: it reads from the
// API server, not the cache, and reports a Workflow outside b.Namespaces as not found.
func (b *Backend) ReadWorkflowV1Alpha2(ctx context.Context, name, namespace string) (*v1alpha2.Workflow, error) {
	wflw := &v1alpha2.Workflow{}
	if err := b.readWorkflowInto(ctx, wflw, name, namespace); err != nil {
		return nil, err
	}
	return wflw, nil
}

// ListWorkflowsV1Alpha2 is ListWorkflows for v1alpha2 Workflows. ByAgentID
// matches the Agent running the current Task (status.metadata.task.agentID).
func (b *Backend) ListWorkflowsV1Alpha2(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha2.Workflow, error) {
	stored := &v1alpha2.WorkflowList{}
	if err := b.listWorkflowsInto(ctx, stored, WorkflowAgentIDIndexV1Alpha2, opts); err != nil {
		return nil, err
	}

	return stored.Items, nil
}

// UpdateWorkflowV1Alpha2 is UpdateWorkflow for v1alpha2 Workflows.
func (b *Backend) UpdateWorkflowV1Alpha2(ctx context.Context, wf *v1alpha2.Workflow, opts data.UpdateOptions) error {
	return b.update(ctx, wf, "workflow", wf.Name, opts)
}
