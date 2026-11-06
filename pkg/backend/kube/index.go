package kube

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type IndexType string

const (
	IndexTypeMACAddr             IndexType = MACAddrIndex
	IndexTypeIPAddr              IndexType = IPAddrIndex
	IndexTypeHardwareName        IndexType = "hardware.metadata.name"
	IndexTypeHardwareAgentID     IndexType = "hardware" + HardwareAgentIDIndex
	IndexTypeLookupID            IndexType = LookupIDIndex
	IndexTypeWorkflowAgentID     IndexType = WorkflowAgentIDIndex
	IndexTypeWorkflowTaskAgentID IndexType = WorkflowTaskAgentIDIndex
	IndexTypeMachineName         IndexType = "machine.metadata.name"

	// NameIndex is an index used with a controller-runtime client to lookup objects by name.
	NameIndex = ".metadata.name"

	// HardwareAgentIDIndex indexes Hardware by spec.agentID.
	HardwareAgentIDIndex = ".spec.agentID"

	// MACAddrIndex indexes Hardware by the MAC addresses, in lower case, that
	// key spec.networkInterfaces.
	MACAddrIndex = ".spec.networkInterfaces"

	// IPAddrIndex indexes Hardware by the IPv4 and IPv6 addresses of its
	// network interfaces.
	IPAddrIndex = ".spec.networkInterfaces.ipam.address"

	// LookupIDIndex indexes Hardware by spec.instance.lookupID, the v1alpha2
	// form of the v1alpha1 metadata instance ID.
	LookupIDIndex = ".spec.instance.lookupID"

	// WorkflowAgentIDIndex indexes Workflows by the Agent running their
	// current Task.
	WorkflowAgentIDIndex = ".status.metadata.task.agentID"

	// WorkflowTaskAgentIDIndex indexes Workflows by every Agent one of their
	// Tasks is assigned to, so that the Workflows that will run on an Agent can
	// be found before they start.
	WorkflowTaskAgentIDIndex = ".spec.tasks.agentID"
)

// Indexes that are currently known. Each index registered on a kind makes the
// backend watch that kind, so Hardware and Workflows are only indexed as
// v1alpha2, the version every component reads.
var Indexes = map[IndexType]Index{
	IndexTypeMACAddr: {
		Obj:          &v1alpha2.Hardware{},
		Field:        MACAddrIndex,
		ExtractValue: MACAddrs,
	},
	IndexTypeIPAddr: {
		Obj:          &v1alpha2.Hardware{},
		Field:        IPAddrIndex,
		ExtractValue: IPAddrs,
	},
	IndexTypeHardwareName: {
		Obj:          &v1alpha2.Hardware{},
		Field:        NameIndex,
		ExtractValue: HardwareName,
	},
	IndexTypeHardwareAgentID: {
		Obj:          &v1alpha2.Hardware{},
		Field:        HardwareAgentIDIndex,
		ExtractValue: HardwareAgentID,
	},
	IndexTypeLookupID: {
		Obj:          &v1alpha2.Hardware{},
		Field:        LookupIDIndex,
		ExtractValue: LookupID,
	},
	IndexTypeWorkflowAgentID: {
		Obj:          &v1alpha2.Workflow{},
		Field:        WorkflowAgentIDIndex,
		ExtractValue: WorkflowAgentID,
	},
	IndexTypeWorkflowTaskAgentID: {
		Obj:          &v1alpha2.Workflow{},
		Field:        WorkflowTaskAgentIDIndex,
		ExtractValue: WorkflowTaskAgentIDs,
	},
	// The deprecated bmc Machine only exists in v1alpha1. SecondStar reads the
	// one a Hardware created through v1alpha1 names in its bmcRef, while the
	// Machine CRD exists.
	IndexTypeMachineName: {
		Obj:          &bmc.Machine{},
		Field:        NameIndex,
		ExtractValue: MachineName,
		Optional:     true,
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

// MACAddrs returns the MAC addresses of a v1alpha2 Hardware, in lower case.
func MACAddrs(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	macs := []string{}
	for mac := range hw.Spec.NetworkInterfaces {
		if mac != "" {
			macs = append(macs, normalizeMAC(string(mac)))
		}
	}
	return sortedUnique(macs)
}

// IPAddrs returns the IPv4 and IPv6 addresses of a v1alpha2 Hardware.
func IPAddrs(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	ips := []string{}
	for _, ni := range hw.Spec.NetworkInterfaces {
		if ni.IPAM == nil {
			continue
		}
		for _, ip := range []*v1alpha2.IP{ni.IPAM.IPv4, ni.IPAM.IPv6} {
			if ip != nil && ip.Address != "" {
				ips = append(ips, normalizeIP(ip.Address))
			}
		}
	}
	return sortedUnique(ips)
}

// HardwareName returns the name of a v1alpha2 Hardware.
func HardwareName(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	return []string{hw.Name}
}

// HardwareAgentID returns the spec.agentID of a v1alpha2 Hardware.
func HardwareAgentID(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	if hw.Spec.AgentID == "" {
		return []string{}
	}
	return []string{hw.Spec.AgentID}
}

// LookupID returns the spec.instance.lookupID of a v1alpha2 Hardware.
func LookupID(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	if hw.Spec.Instance == nil || hw.Spec.Instance.LookupID == "" {
		return []string{}
	}
	return []string{hw.Spec.Instance.LookupID}
}

// WorkflowAgentID returns the Agent running the current Task of a
// v1alpha2 Workflow.
func WorkflowAgentID(obj client.Object) []string {
	wf, ok := obj.(*v1alpha2.Workflow)
	if !ok {
		return nil
	}
	if wf.Status.Metadata.Task.AgentID == "" {
		return []string{}
	}
	return []string{wf.Status.Metadata.Task.AgentID}
}

// WorkflowTaskAgentIDs returns every Agent a Task of a v1alpha2
// Workflow is assigned to.
func WorkflowTaskAgentIDs(obj client.Object) []string {
	wf, ok := obj.(*v1alpha2.Workflow)
	if !ok {
		return nil
	}
	agents := []string{}
	for _, task := range wf.Spec.Tasks {
		if task.AgentID != "" {
			agents = append(agents, task.AgentID)
		}
	}
	return sortedUnique(agents)
}

// normalizeMAC returns mac the way v1alpha2 keys network interfaces: in lower case.
func normalizeMAC(mac string) string { return strings.ToLower(mac) }

// normalizeIP returns ip in its canonical text form, so that differently
// written forms of one IPv6 address match. Unparsable values are kept as is.
func normalizeIP(ip string) string {
	if addr, err := netip.ParseAddr(ip); err == nil {
		return addr.String()
	}
	return ip
}

// sortedUnique sorts values and drops duplicates, so that index values do not
// depend on map iteration order.
func sortedUnique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}
