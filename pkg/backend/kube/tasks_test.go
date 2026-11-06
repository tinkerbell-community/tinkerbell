package kube

import (
	"context"
	"slices"
	"testing"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReadTask(t *testing.T) {
	task := &v1alpha2.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "install", Namespace: "tink"},
		Spec:       v1alpha2.TaskSpec{Actions: []v1alpha2.Action{{Name: "stream", Image: "image2disk"}}},
	}
	tests := map[string]struct {
		namespaces   []string
		name         string
		wantNotFound bool
	}{
		"found":                         {name: "install"},
		"inside the backend namespace":  {namespaces: []string{"tink"}, name: "install"},
		"missing":                       {name: "missing", wantNotFound: true},
		"outside the backend namespace": {namespaces: []string{"other"}, name: "install", wantNotFound: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := newFakeBackend(t, Backend{}, task.DeepCopy())
			b.Namespaces = tt.namespaces
			got, err := b.ReadTask(context.Background(), tt.name, "tink")
			if tt.wantNotFound {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("err = %v, want not found", err)
				}
				return
			}
			if err != nil || len(got.Spec.Actions) != 1 || got.Spec.Actions[0].Image != "image2disk" {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestListPolicies(t *testing.T) {
	policy := func(name, namespace string) *v1alpha2.Policy {
		return &v1alpha2.Policy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	}
	b := newFakeBackend(t, Backend{}, policy("p1", "tink"), policy("p2", "tink"), policy("p3", "other"))
	tests := map[string]struct {
		filter data.WorkflowFilter
		want   []string
	}{
		"all namespaces": {want: []string{"p1", "p2", "p3"}},
		"one namespace":  {filter: data.WorkflowFilter{InNamespace: "tink"}, want: []string{"p1", "p2"}},
		"empty":          {filter: data.WorkflowFilter{InNamespace: "empty"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := b.ListPolicies(context.Background(), tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, p := range got {
				names = append(names, p.Name)
			}
			slices.Sort(names)
			if !slices.Equal(names, tt.want) {
				t.Fatalf("listed %v, want %v", names, tt.want)
			}
		})
	}
}
