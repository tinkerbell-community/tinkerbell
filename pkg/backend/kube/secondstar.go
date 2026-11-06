package kube

import (
	"context"
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultIPMIPort        = 623
	defaultIPMICipherSuite = "17"
)

// filterMachine looks up a single deprecated bmc.Machine object by namespace and
// name, for Hardware created through v1alpha1 that names one in its bmcRef.
// Exactly one result is expected; zero returns a not-found error and multiple returns a multiple-found error.
func (b *Backend) filterMachine(ctx context.Context, namespace, name string) (*bmc.Machine, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.filterMachine")
	defer span.End()

	if name == "" {
		return nil, fmt.Errorf("BMC Machine name is required")
	}

	bmcList := &bmc.MachineList{}
	if err := b.cluster.GetClient().List(ctx, bmcList, client.InNamespace(namespace), &client.MatchingFields{NameIndex: name}); err != nil {
		return nil, fmt.Errorf("failed listing bmc machine for (%v/%v): %w", namespace, name, err)
	}

	if len(bmcList.Items) == 0 {
		err := fmt.Errorf("bmc machine not found: %s/%s", namespace, name)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if len(bmcList.Items) > 1 {
		err := fmt.Errorf("got %d bmc machine objects for name: %s, expected only 1", len(bmcList.Items), name)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	return &bmcList.Items[0], nil
}

// ReadAuthSecret looks up a Secret by name and namespace using a direct Get.
// It expects the Secret to contain "username" and "password" keys in its data.
func (b *Backend) ReadAuthSecret(ctx context.Context, name, namespace string) (string, string, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.ReadAuthSecret")
	defer span.End()

	secret := &v1.Secret{}
	if err := b.cluster.GetClient().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return "", "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}

	username, ok := secret.Data["username"]
	if !ok {
		return "", "", fmt.Errorf("'username' required in Machine secret %s/%s", namespace, name)
	}

	password, ok := secret.Data["password"]
	if !ok {
		return "", "", fmt.Errorf("'password' required in Machine secret %s/%s", namespace, name)
	}

	return string(username), string(password), nil
}
