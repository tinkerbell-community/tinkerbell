package webhttp

import (
	"testing"

	"github.com/tinkerbell/tinkerbell/ui/templates"
)

// Note: HandlePermissions and HandlePermissionCheck require a real Kubernetes clientset
// for the AuthorizationV1().SelfSubjectAccessReviews() call which cannot be easily
// mocked with the controller-runtime fake client. These tests focus on the
// Permission struct and TinkerbellResources.

func TestPermissionStruct(t *testing.T) {
	tests := []struct {
		name       string
		permission templates.Permission
		wantRes    string
		wantGroup  string
		wantVerbs  int
	}{
		{
			name: "basic permission with verbs",
			permission: templates.Permission{
				Resource: "hardware",
				APIGroup: "tinkerbell.org",
				Verbs:    []string{"get", "list", "watch"},
			},
			wantRes:   "hardware",
			wantGroup: "tinkerbell.org",
			wantVerbs: 3,
		},
		{
			name: "namespace-scoped permission",
			permission: templates.Permission{
				Resource:  "workflows",
				APIGroup:  "tinkerbell.org",
				Namespace: "tinkerbell",
				Verbs:     []string{"get", "list"},
			},
			wantRes:   "workflows",
			wantGroup: "tinkerbell.org",
			wantVerbs: 2,
		},
		{
			name: "no access permission",
			permission: templates.Permission{
				Resource: "templates",
				APIGroup: "tinkerbell.org",
				Verbs:    []string{},
			},
			wantRes:   "templates",
			wantGroup: "tinkerbell.org",
			wantVerbs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.permission.Resource != tt.wantRes {
				t.Errorf("Resource = %q, want %q", tt.permission.Resource, tt.wantRes)
			}
			if tt.permission.APIGroup != tt.wantGroup {
				t.Errorf("APIGroup = %q, want %q", tt.permission.APIGroup, tt.wantGroup)
			}
			if len(tt.permission.Verbs) != tt.wantVerbs {
				t.Errorf("Verbs count = %d, want %d", len(tt.permission.Verbs), tt.wantVerbs)
			}
		})
	}
}

func TestPermissionNamespaceScope(t *testing.T) {
	tests := []struct {
		name          string
		namespace     string
		isClusterWide bool
	}{
		{
			name:          "cluster-wide uses empty namespace",
			namespace:     "",
			isClusterWide: true,
		},
		{
			name:          "namespace-scoped",
			namespace:     "tinkerbell",
			isClusterWide: false,
		},
		{
			name:          "different namespace",
			namespace:     "default",
			isClusterWide: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perm := templates.Permission{
				Resource:  "hardware",
				APIGroup:  "tinkerbell.org",
				Namespace: tt.namespace,
				Verbs:     []string{"get"},
			}

			isClusterWide := perm.Namespace == ""
			if isClusterWide != tt.isClusterWide {
				t.Errorf("isClusterWide = %v, want %v", isClusterWide, tt.isClusterWide)
			}
			if perm.Namespace != tt.namespace {
				t.Errorf("Namespace = %q, want %q", perm.Namespace, tt.namespace)
			}
		})
	}
}

func TestTinkerbellResources(t *testing.T) {
	// Verify all expected Tinkerbell resources are defined, as resource.group:
	// tasks exist in both groups (v1alpha2 Tasks and the deprecated bmc Tasks).
	expectedResources := map[string]bool{
		"hardware.tinkerbell.org":         true,
		"workflows.tinkerbell.org":        true,
		"tasks.tinkerbell.org":            true,
		"policies.tinkerbell.org":         true,
		"jobs.bmc.tinkerbell.org":         true,
		"templates.tinkerbell.org":        true,
		"workflowrulesets.tinkerbell.org": true,
		"machines.bmc.tinkerbell.org":     true,
		"tasks.bmc.tinkerbell.org":        true,
	}

	if len(TinkerbellResources) != len(expectedResources) {
		t.Errorf("TinkerbellResources count = %d, want %d", len(TinkerbellResources), len(expectedResources))
	}

	for _, res := range TinkerbellResources {
		if !expectedResources[res.Resource+"."+res.Group] {
			t.Errorf("Unexpected resource %s.%s in TinkerbellResources", res.Resource, res.Group)
		}
	}
}

func TestTinkerbellVerbs(t *testing.T) {
	expectedVerbs := []string{"get", "list", "watch", "create", "update", "patch", "delete"}

	if len(tinkerbellVerbs) != len(expectedVerbs) {
		t.Errorf("tinkerbellVerbs count = %d, want %d", len(tinkerbellVerbs), len(expectedVerbs))
	}

	for i, verb := range tinkerbellVerbs {
		if verb != expectedVerbs[i] {
			t.Errorf("tinkerbellVerbs[%d] = %q, want %q", i, verb, expectedVerbs[i])
		}
	}
}

func TestResourceInfo(t *testing.T) {
	info := templates.ResourceInfo{
		Resource: "hardware",
		APIGroup: "tinkerbell.org",
	}

	if info.Resource != "hardware" {
		t.Errorf("Resource = %q, want %q", info.Resource, "hardware")
	}
	if info.APIGroup != "tinkerbell.org" {
		t.Errorf("APIGroup = %q, want %q", info.APIGroup, "tinkerbell.org")
	}
}
