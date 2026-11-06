package kube

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
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
