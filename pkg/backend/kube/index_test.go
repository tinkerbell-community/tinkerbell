package kube

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMachineName(t *testing.T) {
	tests := map[string]struct {
		obj  client.Object
		want []string
	}{
		"bmc Machine":     {obj: &bmc.Machine{ObjectMeta: metav1.ObjectMeta{Name: "bmc1"}}, want: []string{"bmc1"}},
		"not a Machine":   {obj: &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "bmc1"}}},
		"Machine no name": {obj: &bmc.Machine{}, want: []string{""}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, MachineName(tt.obj)); diff != "" {
				t.Errorf("index values (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHardwareIndexes(t *testing.T) {
	hw := &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{
		AgentID:  "agent1",
		Instance: &v1alpha2.Instance{LookupID: "lookup1"},
		NetworkInterfaces: v1alpha2.NetworkInterfaces{
			"00:00:00:00:00:02": {IPAM: &v1alpha2.IPAM{
				IPv4: &v1alpha2.IP{Address: "192.168.2.2"},
				IPv6: &v1alpha2.IP{Address: "2001:DB8:0::2"},
			}},
			"AA:00:00:00:00:01": {IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "192.168.2.1"}}},
			"00:00:00:00:00:03": {IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{}}},
			"00:00:00:00:00:04": {},
		},
	}}
	hw.Name = "machine1"
	empty := &v1alpha2.Hardware{}
	notHardware := &v1alpha1.Hardware{}

	tests := map[string]struct {
		extract client.IndexerFunc
		obj     client.Object
		want    []string
	}{
		"MACs, in lower case":            {extract: MACAddrs, obj: hw, want: []string{"00:00:00:00:00:02", "00:00:00:00:00:03", "00:00:00:00:00:04", "aa:00:00:00:00:01"}},
		"no MACs":                        {extract: MACAddrs, obj: empty, want: []string{}},
		"MACs of v1alpha1":               {extract: MACAddrs, obj: notHardware},
		"IPv4 and canonical IPv6":        {extract: IPAddrs, obj: hw, want: []string{"192.168.2.1", "192.168.2.2", "2001:db8::2"}},
		"no IPs":                         {extract: IPAddrs, obj: empty, want: []string{}},
		"IPs of v1alpha1":                {extract: IPAddrs, obj: notHardware},
		"name":                           {extract: HardwareName, obj: hw, want: []string{"machine1"}},
		"name of v1alpha1":               {extract: HardwareName, obj: notHardware},
		"agent ID":                       {extract: HardwareAgentID, obj: hw, want: []string{"agent1"}},
		"no agent ID":                    {extract: HardwareAgentID, obj: empty, want: []string{}},
		"agent ID of v1alpha1":           {extract: HardwareAgentID, obj: notHardware},
		"lookup ID":                      {extract: LookupID, obj: hw, want: []string{"lookup1"}},
		"no instance":                    {extract: LookupID, obj: empty, want: []string{}},
		"no lookup ID":                   {extract: LookupID, obj: &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{Instance: &v1alpha2.Instance{}}}, want: []string{}},
		"lookup ID of v1alpha1 Hardware": {extract: LookupID, obj: notHardware},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.extract(tt.obj)); diff != "" {
				t.Errorf("index values (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkflowIndexes(t *testing.T) {
	running := &v1alpha2.Workflow{
		Spec: v1alpha2.WorkflowSpec{Tasks: []v1alpha2.WorkflowTask{{AgentID: "agent2"}, {AgentID: "agent1"}, {AgentID: "agent2"}, {}}},
	}
	running.Status.Metadata.Task.AgentID = "agent2"
	tests := map[string]struct {
		extract client.IndexerFunc
		obj     client.Object
		want    []string
	}{
		"current agent":             {extract: WorkflowAgentID, obj: running, want: []string{"agent2"}},
		"no current agent":          {extract: WorkflowAgentID, obj: &v1alpha2.Workflow{}, want: []string{}},
		"current agent of v1alpha1": {extract: WorkflowAgentID, obj: &v1alpha1.Workflow{}},
		"task agents":               {extract: WorkflowTaskAgentIDs, obj: running, want: []string{"agent1", "agent2"}},
		"no tasks":                  {extract: WorkflowTaskAgentIDs, obj: &v1alpha2.Workflow{}, want: []string{}},
		"task agents of v1alpha1":   {extract: WorkflowTaskAgentIDs, obj: &v1alpha1.Workflow{}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.extract(tt.obj)); diff != "" {
				t.Errorf("index values (-want +got):\n%s", diff)
			}
		})
	}
}
