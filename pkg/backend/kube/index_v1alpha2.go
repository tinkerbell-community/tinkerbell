package kube

import (
	"net/netip"
	"slices"
	"strings"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// v1alpha2 indexes. Field indexes are registered per type: the Hardware name
// and agent ID indexes use NameIndex and HardwareAgentIDIndex.
const (
	IndexTypeMACAddrV1Alpha2             IndexType = "v1alpha2" + MACAddrIndexV1Alpha2
	IndexTypeIPAddrV1Alpha2              IndexType = "v1alpha2" + IPAddrIndexV1Alpha2
	IndexTypeHardwareNameV1Alpha2        IndexType = "v1alpha2.hardware.metadata.name"
	IndexTypeHardwareAgentIDV1Alpha2     IndexType = "v1alpha2.hardware" + HardwareAgentIDIndex
	IndexTypeLookupIDV1Alpha2            IndexType = "v1alpha2" + LookupIDIndexV1Alpha2
	IndexTypeWorkflowAgentIDV1Alpha2     IndexType = "v1alpha2" + WorkflowAgentIDIndexV1Alpha2
	IndexTypeWorkflowTaskAgentIDV1Alpha2 IndexType = "v1alpha2" + WorkflowTaskAgentIDIndexV1Alpha2

	// MACAddrIndexV1Alpha2 indexes v1alpha2 Hardware by the MAC addresses, in lower
	// case, that key spec.networkInterfaces.
	MACAddrIndexV1Alpha2 = ".spec.networkInterfaces"

	// IPAddrIndexV1Alpha2 indexes v1alpha2 Hardware by the IPv4 and IPv6 addresses
	// of its network interfaces.
	IPAddrIndexV1Alpha2 = ".spec.networkInterfaces.ipam.address"

	// LookupIDIndexV1Alpha2 indexes v1alpha2 Hardware by spec.instance.lookupID,
	// the v1alpha2 form of the v1alpha1 metadata instance ID.
	LookupIDIndexV1Alpha2 = ".spec.instance.lookupID"

	// WorkflowAgentIDIndexV1Alpha2 indexes v1alpha2 Workflows by the Agent running
	// their current Task.
	WorkflowAgentIDIndexV1Alpha2 = ".status.metadata.task.agentID"

	// WorkflowTaskAgentIDIndexV1Alpha2 indexes v1alpha2 Workflows by every Agent
	// one of their Tasks is assigned to, so that the Workflows that will run on an
	// Agent can be found before they start.
	WorkflowTaskAgentIDIndexV1Alpha2 = ".spec.tasks.agentID"
)

// MACAddrsV1Alpha2 returns the MAC addresses of a v1alpha2 Hardware, in lower case.
func MACAddrsV1Alpha2(obj client.Object) []string {
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

// IPAddrsV1Alpha2 returns the IPv4 and IPv6 addresses of a v1alpha2 Hardware.
func IPAddrsV1Alpha2(obj client.Object) []string {
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

// HardwareNameV1Alpha2 returns the name of a v1alpha2 Hardware.
func HardwareNameV1Alpha2(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	return []string{hw.Name}
}

// HardwareAgentIDV1Alpha2 returns the spec.agentID of a v1alpha2 Hardware.
func HardwareAgentIDV1Alpha2(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	if hw.Spec.AgentID == "" {
		return []string{}
	}
	return []string{hw.Spec.AgentID}
}

// LookupIDV1Alpha2 returns the spec.instance.lookupID of a v1alpha2 Hardware.
func LookupIDV1Alpha2(obj client.Object) []string {
	hw, ok := obj.(*v1alpha2.Hardware)
	if !ok {
		return nil
	}
	if hw.Spec.Instance == nil || hw.Spec.Instance.LookupID == "" {
		return []string{}
	}
	return []string{hw.Spec.Instance.LookupID}
}

// WorkflowAgentIDV1Alpha2 returns the Agent running the current Task of a
// v1alpha2 Workflow.
func WorkflowAgentIDV1Alpha2(obj client.Object) []string {
	wf, ok := obj.(*v1alpha2.Workflow)
	if !ok {
		return nil
	}
	if wf.Status.Metadata.Task.AgentID == "" {
		return []string{}
	}
	return []string{wf.Status.Metadata.Task.AgentID}
}

// WorkflowTaskAgentIDsV1Alpha2 returns every Agent a Task of a v1alpha2
// Workflow is assigned to.
func WorkflowTaskAgentIDsV1Alpha2(obj client.Object) []string {
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
