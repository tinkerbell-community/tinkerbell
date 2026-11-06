package kube

import (
	"context"
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

// FilterBMCMachineV1Alpha2 is FilterBMCMachine for v1alpha2 Hardware: it returns
// the BMC connection of the Hardware matching opts, see BMCConnectionV1Alpha2,
// with the credentials read from their Secret, and the SSH keys of the Hardware
// (spec.instance.sshKeys).
//
// The IPMI port and cipher suite come from the connection's IPMI provider
// options, as for the bmc Machine. The connection's port is not used: Machines
// translated into spec.bmc.connection carry their port there whatever provider
// it was meant for, often Redfish's 443.
func (b *Backend) FilterBMCMachineV1Alpha2(ctx context.Context, opts data.HardwareFilter) (*data.BMCMachine, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.FilterBMCMachineV1Alpha2")
	defer span.End()

	hw, err := b.RenderedReader().FilterHardwareV1Alpha2(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to filter hardware: %w", err)
	}
	response := &data.BMCMachine{}
	if hw.Spec.Instance != nil {
		response.SSHPublicKeys = hw.Spec.Instance.SSHKeys
	}

	conn, err := b.BMCConnectionV1Alpha2(ctx, hw)
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

// BMCConnectionV1Alpha2 returns the BMC connection of hw: spec.bmc.connection,
// else the connection of the deprecated bmc Machine named by the v1alpha1 bmcRef
// of Hardware created through v1alpha1 (conversion.HardwareBMCRef). Like the
// bmcRef, that Machine is in the Hardware's namespace.
func (b *Backend) BMCConnectionV1Alpha2(ctx context.Context, hw *v1alpha2.Hardware) (*bmcv1alpha2.Connection, error) {
	if hw.Spec.BMC != nil && hw.Spec.BMC.Connection != nil {
		return hw.Spec.BMC.Connection.DeepCopy(), nil
	}

	ref := conversion.HardwareBMCRef(hw)
	if ref == nil || ref.Name == "" {
		return nil, fmt.Errorf("hardware %s/%s has no BMC connection: spec.bmc.connection is not set and it does not reference a bmc Machine", hw.Namespace, hw.Name)
	}
	machine, err := b.filterMachine(ctx, hw.Namespace, ref.Name)
	if err != nil {
		return nil, err
	}
	conn, err := conversion.MachineConnectionToV1Alpha2(machine.Spec.Connection)
	if err != nil {
		return nil, fmt.Errorf("bmc machine %s/%s: %w", machine.Namespace, machine.Name, err)
	}

	return conn, nil
}
