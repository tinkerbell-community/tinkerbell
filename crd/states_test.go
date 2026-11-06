package crd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	apiv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestNameStates(t *testing.T) {
	status := map[string]any{
		"metadata": map[string]any{
			"workflow": map[string]any{"state": int64(5), "name": "wf"},
			"task":     map[string]any{"state": float64(2)},
			"action":   map[string]any{"state": "Running"},
		},
		"renderedTasks": []any{map[string]any{
			"taskMetadata": map[string]any{"state": int64(6)},
			"actions":      []any{map[string]any{"metadata": map[string]any{"state": int64(99)}}},
		}},
	}
	if !nameStates(status) {
		t.Fatal("nameStates reported no change")
	}
	want := map[string]any{
		"metadata": map[string]any{
			"workflow": map[string]any{"state": "Success", "name": "wf"},
			"task":     map[string]any{"state": "Pending"},
			"action":   map[string]any{"state": "Running"},
		},
		"renderedTasks": []any{map[string]any{
			"taskMetadata": map[string]any{"state": "Failed"},
			// Not a state: left for the decoder to reject.
			"actions": []any{map[string]any{"metadata": map[string]any{"state": int64(99)}}},
		}},
	}
	if d := cmp.Diff(want, status); d != "" {
		t.Errorf("status (-want +got):\n%s", d)
	}
	if nameStates(status) {
		t.Error("nameStates changed already named states")
	}
}

// v1alpha2WorkflowsCRD returns the Workflow CRD with only its v1alpha2 version, which needs no
// conversion webhook. With integerStates, its states are the integers of Tinkerbell v0.27.
func v1alpha2WorkflowsCRD(t *testing.T, integerStates bool) []byte {
	t.Helper()
	var c apiv1.CustomResourceDefinition
	if err := sigsyaml.Unmarshal(TinkerbellDefaults["workflows."+GroupTinkerbell], &c); err != nil {
		t.Fatal(err)
	}
	var keep []apiv1.CustomResourceDefinitionVersion
	for _, v := range c.Spec.Versions {
		if v.Name == "v1alpha2" {
			keep = append(keep, v)
		}
	}
	c.Spec.Versions = keep
	c.Spec.Conversion = nil
	var integer func(p *apiv1.JSONSchemaProps)
	integer = func(p *apiv1.JSONSchemaProps) {
		for name, sub := range p.Properties {
			if name == "state" && len(sub.Enum) > 0 {
				sub = apiv1.JSONSchemaProps{Type: "integer", Description: sub.Description}
			} else {
				integer(&sub)
			}
			p.Properties[name] = sub
		}
		if p.Items != nil && p.Items.Schema != nil {
			integer(p.Items.Schema)
		}
	}
	if integerStates {
		integer(c.Spec.Versions[0].Schema.OpenAPIV3Schema)
	}
	b, err := sigsyaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestNameWorkflowStates stores a Workflow with the numeric states of Tinkerbell v0.27, upgrades
// the CRD, and checks that the states get their names. It needs a kube-apiserver and etcd: set
// KUBEBUILDER_ASSETS.
func TestNameWorkflowStates(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	old, err := NewTinkerbell(WithRestConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	old.CRDs = map[string][]byte{"workflows." + GroupTinkerbell: v1alpha2WorkflowsCRD(t, true)}
	if err := old.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := old.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	ri := old.Dynamic.Resource(workflowsV1Alpha2).Namespace("default")
	wf := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "tinkerbell.org/v1alpha2",
		"kind":       "Workflow",
		"metadata":   map[string]any{"name": "finished", "namespace": "default", "labels": map[string]any{"keep": "me"}},
		"spec":       map[string]any{"timeoutSeconds": int64(600)},
	}}
	created, err := ri.Create(ctx, wf, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	created.Object["status"] = map[string]any{"metadata": map[string]any{
		"workflow": map[string]any{"state": int64(5)},
		"task":     map[string]any{"state": int64(5), "name": "t1"},
	}}
	if _, err := ri.UpdateStatus(ctx, created, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("storing numeric states with the v0.27 CRD: %v", err)
	}

	tb, err := NewTinkerbell(WithRestConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tb.CRDs = map[string][]byte{"workflows." + GroupTinkerbell: v1alpha2WorkflowsCRD(t, false)}
	if err := tb.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tb.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tb.NameWorkflowStates(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := ri.Get(ctx, "finished", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"metadata": map[string]any{
		"workflow": map[string]any{"state": "Success"},
		"task":     map[string]any{"state": "Success", "name": "t1"},
	}}
	if d := cmp.Diff(want, got.Object["status"]); d != "" {
		t.Errorf("status (-want +got):\n%s", d)
	}
	if got.GetLabels()["keep"] != "me" {
		t.Errorf("labels changed: %v", got.GetLabels())
	}

	// A second run finds nothing to name.
	if err := tb.NameWorkflowStates(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := ri.Get(ctx, "finished", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if again.GetResourceVersion() != got.GetResourceVersion() {
		t.Error("a second run wrote the workflow again")
	}
}
