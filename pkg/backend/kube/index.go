package kube

import (
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type IndexType string

const (
	IndexTypeMachineName IndexType = "machine.metadata.name"

	// NameIndex is an index used with a controller-runtime client to lookup objects by name.
	NameIndex = ".metadata.name"

	// HardwareAgentIDIndex is an index used with a controller-runtime client to lookup hardware by their spec agent id.
	HardwareAgentIDIndex = ".spec.agentID"
)

// Indexes that are currently known. Each index registered on a kind makes the
// backend watch that kind, so Hardware and Workflows are only indexed as
// v1alpha2, the version every component reads.
var Indexes = map[IndexType]Index{
	// The deprecated bmc Machine only exists in v1alpha1. SecondStar reads the
	// one a Hardware created through v1alpha1 names in its bmcRef.
	IndexTypeMachineName: {
		Obj:          &bmc.Machine{},
		Field:        NameIndex,
		ExtractValue: MachineName,
	},
	IndexTypeMACAddrV1Alpha2: {
		Obj:          &v1alpha2.Hardware{},
		Field:        MACAddrIndexV1Alpha2,
		ExtractValue: MACAddrsV1Alpha2,
	},
	IndexTypeIPAddrV1Alpha2: {
		Obj:          &v1alpha2.Hardware{},
		Field:        IPAddrIndexV1Alpha2,
		ExtractValue: IPAddrsV1Alpha2,
	},
	IndexTypeHardwareNameV1Alpha2: {
		Obj:          &v1alpha2.Hardware{},
		Field:        NameIndex,
		ExtractValue: HardwareNameV1Alpha2,
	},
	IndexTypeHardwareAgentIDV1Alpha2: {
		Obj:          &v1alpha2.Hardware{},
		Field:        HardwareAgentIDIndex,
		ExtractValue: HardwareAgentIDV1Alpha2,
	},
	IndexTypeLookupIDV1Alpha2: {
		Obj:          &v1alpha2.Hardware{},
		Field:        LookupIDIndexV1Alpha2,
		ExtractValue: LookupIDV1Alpha2,
	},
	IndexTypeWorkflowAgentIDV1Alpha2: {
		Obj:          &v1alpha2.Workflow{},
		Field:        WorkflowAgentIDIndexV1Alpha2,
		ExtractValue: WorkflowAgentIDV1Alpha2,
	},
	IndexTypeWorkflowTaskAgentIDV1Alpha2: {
		Obj:          &v1alpha2.Workflow{},
		Field:        WorkflowTaskAgentIDIndexV1Alpha2,
		ExtractValue: WorkflowTaskAgentIDsV1Alpha2,
	},
}

// MachineName extracts the name of a BMC Machine object for field indexing.
func MachineName(obj client.Object) []string {
	m, ok := obj.(*bmc.Machine)
	if !ok {
		return nil
	}
	return []string{m.Name}
}
