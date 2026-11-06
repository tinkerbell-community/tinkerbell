package kube

import (
	"context"
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultIPMIPort        = 623
	defaultIPMICipherSuite = "17"
)

// FilterBMCMachine returns the BMC connection of the Hardware matching
// opts, see BMCConnection, with the credentials read from their Secret,
// and the SSH keys of the Hardware (spec.instance.sshKeys).
//
// The IPMI port and cipher suite come from the connection's IPMI provider
// options, as they did from the bmc Machine. The connection's port is not
// used: Machines translated into spec.bmc.connection carry their port there
// whatever provider it was meant for, often Redfish's 443.
func (b *Backend) FilterBMCMachine(ctx context.Context, opts data.HardwareFilter) (*data.BMCMachine, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.FilterBMCMachine")
	defer span.End()

	hw, err := b.RenderedReader().FilterHardware(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to filter hardware: %w", err)
	}
	response := &data.BMCMachine{}
	if hw.Spec.Instance != nil {
		response.SSHPublicKeys = hw.Spec.Instance.SSHKeys
	}

	conn, err := b.BMCConnection(ctx, hw)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	response.Host = conn.Host
	if conn.ProviderOptions != nil && conn.ProviderOptions.IPMI != nil {
		response.Port = ternary(conn.ProviderOptions.IPMI.Port == 0, defaultIPMIPort, conn.ProviderOptions.IPMI.Port)
		response.CipherSuite = ternary(conn.ProviderOptions.IPMI.CipherSuite == "", defaultIPMICipherSuite, conn.ProviderOptions.IPMI.CipherSuite)
	}

	username, password, err := b.ReadAuthSecret(ctx, conn.CredentialsRef.Name, conn.CredentialsRef.Namespace)
	if err != nil {
		return nil, err
	}
	response.User = username
	response.Pass = password

	return response, nil
}

// BMCConnection returns the BMC connection of hw: spec.bmc.connection,
// else the connection of the deprecated bmc Machine named by the v1alpha1 bmcRef
// of Hardware created through v1alpha1 (conversion.HardwareBMCRef). Like the
// bmcRef, that Machine is in the Hardware's namespace.
func (b *Backend) BMCConnection(ctx context.Context, hw *v1alpha2.Hardware) (*bmcv1alpha2.Connection, error) {
	if hw.Spec.BMC != nil && hw.Spec.BMC.Connection != nil {
		return hw.Spec.BMC.Connection.DeepCopy(), nil
	}

	ref := conversion.HardwareBMCRef(hw)
	if ref == nil || ref.Name == "" {
		return nil, fmt.Errorf("hardware %s/%s has no BMC connection: spec.bmc.connection is not set and it does not reference a bmc Machine", hw.Namespace, hw.Name)
	}
	machine, err := b.filterMachine(ctx, hw.Namespace, ref.Name)
	if meta.IsNoMatchError(err) {
		return nil, fmt.Errorf("hardware %s/%s has no BMC connection: spec.bmc.connection is not set and bmc Machines are no longer served", hw.Namespace, hw.Name)
	}
	if err != nil {
		return nil, err
	}
	conn, err := conversion.MachineConnectionToV1Alpha2(machine.Spec.Connection)
	if err != nil {
		return nil, fmt.Errorf("bmc machine %s/%s: %w", machine.Namespace, machine.Name, err)
	}

	return conn, nil
}

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
