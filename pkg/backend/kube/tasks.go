package kube

import (
	"context"
	"fmt"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadTask returns the Task name in namespace. A Workflow may refer to a Task in
// another namespace, so a Task outside b.Namespaces is reported as not found
// rather than read.
func (b *Backend) ReadTask(ctx context.Context, name, namespace string) (*v1alpha2.Task, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.ReadTask")
	defer span.End()

	if !b.Watches(namespace) {
		err := apierrors.NewNotFound(v1alpha2.GroupVersion.WithResource("tasks").GroupResource(), name)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	task := &v1alpha2.Task{}
	if err := b.cluster.GetClient().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, task); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("failed to get task %s/%s: %w", namespace, name, err)
	}

	return task, nil
}

// ListPolicies returns the Policies matching opts. Only InNamespace applies:
// Policies are not tied to an Agent.
func (b *Backend) ListPolicies(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha2.Policy, error) {
	list := &v1alpha2.PolicyList{}
	los := []client.ListOption{}
	if opts.InNamespace != "" {
		los = append(los, client.InNamespace(opts.InNamespace))
	}
	if err := b.cluster.GetClient().List(ctx, list, los...); err != nil {
		return nil, fmt.Errorf("failed to list policies in namespace %s: %w", opts.InNamespace, err)
	}

	return list.Items, nil
}
