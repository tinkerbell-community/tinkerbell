package kube

import (
	"context"
	"testing"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestReadWorkflowV1Alpha2 verifies reads come from the API server, not a cache
// that hasn't caught up, and stay within the backend namespace.
func TestReadWorkflowV1Alpha2(t *testing.T) {
	rs := runtime.NewScheme()
	if err := v1alpha2.AddToScheme(rs); err != nil {
		t.Fatal(err)
	}
	meta := metav1.ObjectMeta{Name: "wf", Namespace: "tink"}
	stale := &v1alpha2.Workflow{ObjectMeta: meta}
	current := &v1alpha2.Workflow{
		ObjectMeta: meta,
		Status:     v1alpha2.WorkflowStatus{RenderedTasks: []v1alpha2.TaskWithMetadata{{Name: "task-1"}}},
	}

	tests := map[string]struct {
		backendNamespaces []string
		cached, live      client.Client
		wantNotFound      bool
	}{
		"stale cache": {
			cached: fake.NewClientBuilder().WithScheme(rs).WithObjects(stale).Build(),
			live:   fake.NewClientBuilder().WithScheme(rs).WithObjects(current).Build(),
		},
		"outside the backend namespace": {
			backendNamespaces: []string{"other"},
			cached:            fake.NewClientBuilder().WithScheme(rs).Build(),
			live:              fake.NewClientBuilder().WithScheme(rs).WithObjects(current).Build(),
			wantNotFound:      true,
		},
		"inside one of several backend namespaces": {
			backendNamespaces: []string{"other", "tink"},
			cached:            fake.NewClientBuilder().WithScheme(rs).Build(),
			live:              fake.NewClientBuilder().WithScheme(rs).WithObjects(current).Build(),
		},
		"deleted": {
			cached:       fake.NewClientBuilder().WithScheme(rs).WithObjects(stale).Build(),
			live:         fake.NewClientBuilder().WithScheme(rs).Build(),
			wantNotFound: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := &Backend{Namespaces: tt.backendNamespaces, cluster: readers{cached: tt.cached, live: tt.live}}
			got, err := b.ReadWorkflowV1Alpha2(context.Background(), "wf", "tink")
			if tt.wantNotFound {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("expected a not found error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Status.RenderedTasks) != 1 {
				t.Fatalf("got rendered Tasks %+v, want the API server's version", got.Status.RenderedTasks)
			}
		})
	}
}

func TestListWorkflowsV1Alpha2(t *testing.T) {
	workflow := func(name, namespace, agent string) *v1alpha2.Workflow {
		wf := &v1alpha2.Workflow{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
		wf.Status.Metadata.Task.AgentID = agent
		return wf
	}
	b := newFakeBackend(t, Backend{}, workflow("wf1", "tink", "agent1"), workflow("wf2", "tink", "agent2"), workflow("wf3", "other", "agent1"))
	tests := map[string]struct {
		filter data.WorkflowFilter
		want   int
	}{
		"all":                    {want: 3},
		"namespace":              {filter: data.WorkflowFilter{InNamespace: "tink"}, want: 2},
		"current agent":          {filter: data.WorkflowFilter{ByAgentID: "agent1"}, want: 2},
		"agent in namespace":     {filter: data.WorkflowFilter{ByAgentID: "agent1", InNamespace: "other"}, want: 1},
		"agent without workflow": {filter: data.WorkflowFilter{ByAgentID: "agent3"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := b.ListWorkflowsV1Alpha2(context.Background(), tt.filter)
			if err != nil || len(got) != tt.want {
				t.Fatalf("listed %d Workflows, %v; want %d", len(got), err, tt.want)
			}
		})
	}
}

func TestCreateAndUpdateWorkflowV1Alpha2(t *testing.T) {
	ctx := context.Background()
	b := newFakeBackend(t, Backend{})
	wf := &v1alpha2.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "tink"},
		Spec:       v1alpha2.WorkflowSpec{Tasks: []v1alpha2.WorkflowTask{{AgentID: "agent1"}}},
	}
	if err := b.CreateWorkflowV1Alpha2(ctx, wf); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateWorkflowV1Alpha2(ctx, &v1alpha2.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "tink"}}); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("err = %v, want already exists", err)
	}
	read := func() *v1alpha2.Workflow {
		t.Helper()
		got := &v1alpha2.Workflow{}
		if err := b.cluster.GetClient().Get(ctx, client.ObjectKeyFromObject(wf), got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	updated := read()
	updated.Spec.Vars = map[string]string{"a": "b"}
	if err := b.UpdateWorkflowV1Alpha2(ctx, updated, data.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	status := read()
	status.Status.Metadata.Task.AgentID = "agent1"
	if err := b.UpdateWorkflowV1Alpha2(ctx, status, data.UpdateOptions{StatusOnly: true}); err != nil {
		t.Fatal(err)
	}
	original := read()
	patched := original.DeepCopy()
	patched.Status.Metadata.Task.Name = "task-1"
	if err := b.UpdateWorkflowV1Alpha2(ctx, patched, data.UpdateOptions{StatusOnly: true, PatchFrom: original}); err != nil {
		t.Fatal(err)
	}
	original = read()
	patched = original.DeepCopy()
	patched.Spec.Vars["c"] = "d"
	if err := b.UpdateWorkflowV1Alpha2(ctx, patched, data.UpdateOptions{PatchFrom: original}); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Spec.Vars["a"] != "b" || got.Spec.Vars["c"] != "d" || got.Status.Metadata.Task.AgentID != "agent1" || got.Status.Metadata.Task.Name != "task-1" {
		t.Fatalf("got spec %+v status %+v; want every update and patch", got.Spec, got.Status.Metadata)
	}
	if listed, err := b.ListWorkflowsV1Alpha2(ctx, data.WorkflowFilter{ByAgentID: "agent1"}); err != nil || len(listed) != 1 {
		t.Fatalf("listed %v, %v; want the Workflow by its current agent", listed, err)
	}

	missing := &v1alpha2.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "tink"}}
	for name, opts := range map[string]data.UpdateOptions{
		"update":       {},
		"status":       {StatusOnly: true},
		"patch":        {PatchFrom: missing.DeepCopy()},
		"status patch": {StatusOnly: true, PatchFrom: missing.DeepCopy()},
	} {
		if err := b.UpdateWorkflowV1Alpha2(ctx, missing, opts); !apierrors.IsNotFound(err) {
			t.Errorf("%s: err = %v, want not found", name, err)
		}
	}
	if err := b.UpdateWorkflowV1Alpha2(ctx, missing, data.UpdateOptions{RawPatch: []byte("{}")}); err == nil {
		t.Fatal("want an error for a raw patch without a type")
	}
}
