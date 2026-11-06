package kube

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestHardwareIndexesV1Alpha2(t *testing.T) {
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
		"MACs, in lower case":            {extract: MACAddrsV1Alpha2, obj: hw, want: []string{"00:00:00:00:00:02", "00:00:00:00:00:03", "00:00:00:00:00:04", "aa:00:00:00:00:01"}},
		"no MACs":                        {extract: MACAddrsV1Alpha2, obj: empty, want: []string{}},
		"MACs of v1alpha1":               {extract: MACAddrsV1Alpha2, obj: notHardware},
		"IPv4 and canonical IPv6":        {extract: IPAddrsV1Alpha2, obj: hw, want: []string{"192.168.2.1", "192.168.2.2", "2001:db8::2"}},
		"no IPs":                         {extract: IPAddrsV1Alpha2, obj: empty, want: []string{}},
		"IPs of v1alpha1":                {extract: IPAddrsV1Alpha2, obj: notHardware},
		"name":                           {extract: HardwareNameV1Alpha2, obj: hw, want: []string{"machine1"}},
		"name of v1alpha1":               {extract: HardwareNameV1Alpha2, obj: notHardware},
		"agent ID":                       {extract: HardwareAgentIDV1Alpha2, obj: hw, want: []string{"agent1"}},
		"no agent ID":                    {extract: HardwareAgentIDV1Alpha2, obj: empty, want: []string{}},
		"agent ID of v1alpha1":           {extract: HardwareAgentIDV1Alpha2, obj: notHardware},
		"lookup ID":                      {extract: LookupIDV1Alpha2, obj: hw, want: []string{"lookup1"}},
		"no instance":                    {extract: LookupIDV1Alpha2, obj: empty, want: []string{}},
		"no lookup ID":                   {extract: LookupIDV1Alpha2, obj: &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{Instance: &v1alpha2.Instance{}}}, want: []string{}},
		"lookup ID of v1alpha1 Hardware": {extract: LookupIDV1Alpha2, obj: notHardware},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.extract(tt.obj)); diff != "" {
				t.Errorf("index values (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkflowIndexesV1Alpha2(t *testing.T) {
	running := &v1alpha2.Workflow{
		Spec: v1alpha2.WorkflowSpec{Tasks: []v1alpha2.WorkflowTask{{AgentID: "agent2"}, {AgentID: "agent1"}, {AgentID: "agent2"}, {}}},
	}
	running.Status.Metadata.Task.AgentID = "agent2"
	tests := map[string]struct {
		extract client.IndexerFunc
		obj     client.Object
		want    []string
	}{
		"current agent":             {extract: WorkflowAgentIDV1Alpha2, obj: running, want: []string{"agent2"}},
		"no current agent":          {extract: WorkflowAgentIDV1Alpha2, obj: &v1alpha2.Workflow{}, want: []string{}},
		"current agent of v1alpha1": {extract: WorkflowAgentIDV1Alpha2, obj: &v1alpha1.Workflow{}},
		"task agents":               {extract: WorkflowTaskAgentIDsV1Alpha2, obj: running, want: []string{"agent1", "agent2"}},
		"no tasks":                  {extract: WorkflowTaskAgentIDsV1Alpha2, obj: &v1alpha2.Workflow{}, want: []string{}},
		"task agents of v1alpha1":   {extract: WorkflowTaskAgentIDsV1Alpha2, obj: &v1alpha1.Workflow{}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.extract(tt.obj)); diff != "" {
				t.Errorf("index values (-want +got):\n%s", diff)
			}
		})
	}
}

// TestV1Alpha2Counterparts checks that every v1alpha1 index on a kind v1alpha2
// has comes with registered v1alpha2 counterparts on the v1alpha2 kind.
func TestV1Alpha2Counterparts(t *testing.T) {
	v1alpha2Kinds := map[reflect.Type]reflect.Type{
		reflect.TypeFor[*v1alpha1.Hardware](): reflect.TypeFor[*v1alpha2.Hardware](),
		reflect.TypeFor[*v1alpha1.Workflow](): reflect.TypeFor[*v1alpha2.Workflow](),
	}
	for t1, index := range Indexes {
		v2Kind, hasV2Kind := v1alpha2Kinds[reflect.TypeOf(index.Obj)]
		counterparts := V1Alpha2Counterparts(t1)
		if hasV2Kind != (len(counterparts) > 0) {
			t.Errorf("index %q has v1alpha2 counterparts %v, want them exactly when its kind is in v1alpha2", t1, counterparts)
		}
		for _, t2 := range counterparts {
			counterpart, ok := Indexes[t2]
			if !ok {
				t.Errorf("counterpart %q of %q is not in Indexes", t2, t1)
				continue
			}
			if reflect.TypeOf(counterpart.Obj) != v2Kind {
				t.Errorf("counterpart %q of %q indexes %T, want %v", t2, t1, counterpart.Obj, v2Kind)
			}
		}
	}
}
