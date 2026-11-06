package workflow

import (
	"context"
	"strings"
	"testing"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var metadataURLTemplate = `version: "0.1"
name: talos
global_timeout: 1800
tasks:
  - name: "os-installation"
    worker: "{{.device_1}}"
    actions:
      - name: "install"
        image: ghcr.io/example/install:v1
        timeout: 600
        environment:
          TALOS_CONFIG: "{{ .metadata_url }}/2009-04-04/user-data"`

func TestReconcileMetadataURL(t *testing.T) {
	const mac = "3c:ec:ef:4c:4f:54"

	tests := []struct {
		name        string
		opts        []Option
		hardwareMap map[string]string
		wantConfig  string
		// wantRewritten says whether the Workflow is rewritten onto the Template's Tasks, which see
		// .metadata_url too.
		wantRewritten bool
	}{
		{
			name:          "configured URL is available to templates",
			opts:          []Option{WithMetadataURL("http://192.0.2.10:7080")},
			wantConfig:    "http://192.0.2.10:7080/2009-04-04/user-data",
			wantRewritten: true,
		},
		{
			// A Task's .metadata_url is always the configured one, so this Workflow is not rewritten.
			name:        "hardwareMap entry overrides the configured URL",
			opts:        []Option{WithMetadataURL("http://192.0.2.10:7080")},
			hardwareMap: map[string]string{"metadata_url": "http://198.51.100.7:7080"},
			wantConfig:  "http://198.51.100.7:7080/2009-04-04/user-data",
		},
		{
			name:          "unset URL fails the render instead of producing an empty URL",
			wantRewritten: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hardwareMap := map[string]string{"device_1": mac}
			for k, v := range tt.hardwareMap {
				hardwareMap[k] = v
			}
			kc := GetFakeClientBuilder().
				WithObjects(
					&v1alpha1.Template{
						ObjectMeta: metav1.ObjectMeta{Name: "talos", Namespace: "default"},
						Spec:       v1alpha1.TemplateSpec{Data: &metadataURLTemplate},
					},
					v2(t, &v1alpha1.Hardware{
						ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "default"},
						Spec: v1alpha1.HardwareSpec{
							Interfaces: []v1alpha1.Interface{{
								DHCP: &v1alpha1.DHCP{MAC: mac, IP: &v1alpha1.IP{Address: "172.16.10.100"}},
							}},
						},
					}),
				)
			wf := &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{Name: "talos", Namespace: "default"},
				Spec:       v1alpha1.WorkflowSpec{TemplateRef: "talos", HardwareMap: hardwareMap},
			}
			kc = kc.WithObjects(v2(t, wf))

			r := NewReconciler(kc.Build(), fakeReferences{}, tt.opts...)
			r.nowFunc = TestTime.Now

			key := types.NamespacedName{Name: "talos", Namespace: "default"}
			// The first reconcile rewrites a Workflow onto Tasks, the next renders them.
			_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
			if err == nil {
				_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
			}

			stored := &v1alpha2.Workflow{}
			if gerr := r.client.Get(context.Background(), key, stored); gerr != nil {
				t.Fatalf("getting workflow: %v", gerr)
			}
			if rewritten := !engine.Compat(stored); rewritten != tt.wantRewritten {
				t.Fatalf("rewritten onto Tasks = %v, want %v", rewritten, tt.wantRewritten)
			}
			got := v1Workflow(t, stored)

			if tt.wantConfig == "" {
				if err == nil || !strings.Contains(err.Error(), "metadata_url") {
					t.Fatalf("Reconcile error = %v, want a render error naming metadata_url", err)
				}
				if got.Status.TemplateRendering != v1alpha1.TemplateRenderingFailed {
					t.Fatalf("TemplateRendering = %q, want %q", got.Status.TemplateRendering, v1alpha1.TemplateRenderingFailed)
				}
				return
			}

			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if len(got.Status.Tasks) != 1 || len(got.Status.Tasks[0].Actions) != 1 {
				t.Fatalf("rendered tasks = %+v, want one task with one action", got.Status.Tasks)
			}
			if cfg := got.Status.Tasks[0].Actions[0].Environment["TALOS_CONFIG"]; cfg != tt.wantConfig {
				t.Fatalf("TALOS_CONFIG = %q, want %q", cfg, tt.wantConfig)
			}
		})
	}
}
