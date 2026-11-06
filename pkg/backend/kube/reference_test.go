package kube

import (
	"context"
	"errors"
	"slices"
	"testing"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// resolveHardware resolves the references of hw with hw as the source, as the
// render store does.
func resolveHardware(b *Backend, hw *v1alpha2.Hardware) (map[string]any, error) {
	return b.ResolveReferencesFor(context.Background(), client.ObjectKeyFromObject(hw), hw.Spec.References)
}

func TestResolveReferences(t *testing.T) {
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink-system"},
		Spec: v1alpha2.HardwareSpec{References: map[string]v1alpha2.Reference{
			"cm":     {Name: "cm1", Namespace: "tink-system", Version: "v1", Resource: "configmaps"},
			"secret": {Name: "s1", Namespace: "tink-system", Version: "v1", Resource: "secrets"},
		}},
	}

	tests := map[string]struct {
		allow, deny []string
		readErr     error
		want        []string
		wantErr     bool
	}{
		"deny all by default": {
			wantErr: true,
		},
		"allow list overrides the default deny": {
			allow: []string{`{"source":{"namespace":["tink-system"]}}`},
			want:  []string{"cm", "secret"},
		},
		"deny list without allow list": {
			deny:    []string{`{"reference":{"resource":["secrets"]}}`},
			want:    []string{"cm"},
			wantErr: true,
		},
		"consumer-specific rules do not authorize Hardware references": {
			allow:   []string{`{"consumer":["tink-controller"],"reference":{"resource":["configmaps"]}}`},
			wantErr: true,
		},
		"invalid rule": {
			allow:   []string{"not a rule"},
			wantErr: true,
		},
		"read error": {
			allow:   []string{`{"source":{"namespace":["tink-system"]}}`},
			readErr: errors.New("boom"),
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := &Backend{
				dynamicClient:                   &fakeDynamicClient{gvr: schema.GroupVersionResource{Version: "v1"}, error: tt.readErr},
				Namespaces:                      []string{"tink-system"},
				HardwareReferenceAllowListRules: tt.allow,
				HardwareReferenceDenyListRules:  tt.deny,
			}
			got, err := resolveHardware(b, hw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			var names []string
			for k := range got {
				names = append(names, k)
			}
			slices.Sort(names)
			if !slices.Equal(names, tt.want) {
				t.Errorf("resolved %v, want %v", names, tt.want)
			}
		})
	}
}

func TestResolveReferencesRejectsOutOfScopeNamespace(t *testing.T) {
	tests := map[string]string{
		"different namespace": "shared",
		"cluster-scoped":      "",
	}
	for name, referenceNamespace := range tests {
		t.Run(name, func(t *testing.T) {
			dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			b := &Backend{
				Namespaces:                      []string{"tink-system", "tink-other"},
				dynamicClient:                   dynamicClient,
				HardwareReferenceAllowListRules: []string{`{"source":{"namespace":["tink-system"]}}`},
			}
			hw := &v1alpha2.Hardware{
				ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink-system"},
				Spec: v1alpha2.HardwareSpec{References: map[string]v1alpha2.Reference{
					"cm": {Name: "cm1", Namespace: referenceNamespace, Version: "v1", Resource: "configmaps"},
				}},
			}

			got, err := resolveHardware(b, hw)
			if err == nil {
				t.Fatal("expected out-of-scope reference error")
			}
			if len(got) != 0 {
				t.Fatalf("resolved out-of-scope references: %v", got)
			}
			if actions := dynamicClient.Actions(); len(actions) != 0 {
				t.Fatalf("made API calls for out-of-scope reference: %v", actions)
			}
		})
	}
}

func TestResolveReferencesRejectsIncompleteReferences(t *testing.T) {
	tests := map[string]v1alpha2.Reference{
		"name":     {Namespace: "tink-system", Version: "v1", Resource: "configmaps"},
		"version":  {Namespace: "tink-system", Name: "cm1", Resource: "configmaps"},
		"resource": {Namespace: "tink-system", Name: "cm1", Version: "v1"},
	}
	for missingField, reference := range tests {
		t.Run(missingField, func(t *testing.T) {
			b := &Backend{
				dynamicClient:                   &fakeDynamicClient{},
				HardwareReferenceAllowListRules: []string{`{"source":{"namespace":["tink-system"]}}`},
			}
			hw := &v1alpha2.Hardware{
				ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink-system"},
				Spec:       v1alpha2.HardwareSpec{References: map[string]v1alpha2.Reference{"cm": reference}},
			}

			got, err := resolveHardware(b, hw)
			if err == nil {
				t.Fatal("expected incomplete reference error")
			}
			if len(got) != 0 {
				t.Errorf("resolved incomplete reference: %v", got)
			}
		})
	}
}

func TestResolveReferencesClusterScoped(t *testing.T) {
	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]any{"name": "node1"},
	}}
	b := &Backend{
		dynamicClient:                   dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), node),
		HardwareReferenceAllowListRules: []string{`{"reference":{"resource":["nodes"]}}`},
	}
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink-system"},
		Spec: v1alpha2.HardwareSpec{References: map[string]v1alpha2.Reference{
			"node": {Name: "node1", Version: "v1", Resource: "nodes"},
		}},
	}

	got, err := resolveHardware(b, hw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got["node"]; !ok {
		t.Errorf("cluster-scoped reference not resolved: %v", got)
	}
}

func TestResolveReferencesAppliesPolicyToNormalizedResource(t *testing.T) {
	b := &Backend{
		dynamicClient: &fakeDynamicClient{},
		HardwareReferenceDenyListRules: []string{
			`{"reference":{"group":["example.org"],"resource":["secrets"]}}`,
		},
	}
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "machine1", Namespace: "tink-system"},
		Spec: v1alpha2.HardwareSpec{References: map[string]v1alpha2.Reference{
			"secret": {Name: "s1", Group: "EXAMPLE.ORG", Namespace: "tink-system", Version: "v1", Resource: "SECRETS"},
		}},
	}

	got, err := resolveHardware(b, hw)
	if err == nil {
		t.Fatal("expected normalized reference to match deny rule")
	}
	if len(got) != 0 {
		t.Errorf("resolved denied reference: %v", got)
	}
}

// TestResolveReferencesFor resolves the references of objects other than
// Hardware, such as Tasks and Workflows, with the same policy.
func TestResolveReferencesFor(t *testing.T) {
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "cm1", "namespace": "tink-system"},
		"data":       map[string]any{"domain": "example.org"},
	}}
	refs := map[string]v1alpha2.Reference{
		"cm": {Name: "cm1", Namespace: "tink-system", Version: "v1", Resource: "ConfigMaps"},
	}
	tests := map[string]struct {
		source      types.NamespacedName
		namespaces  []string
		allow, deny []string
		refs        map[string]v1alpha2.Reference
		wantErr     bool
	}{
		"allowed by source name": {
			source: types.NamespacedName{Namespace: "tink-system", Name: "install"},
			allow:  []string{`{"source":{"name":["install"]}}`},
			refs:   refs,
		},
		"source does not match the allow list": {
			source:  types.NamespacedName{Namespace: "tink-system", Name: "other"},
			allow:   []string{`{"source":{"name":["install"]}}`},
			refs:    refs,
			wantErr: true,
		},
		"allow overrides deny": {
			source: types.NamespacedName{Namespace: "tink-system", Name: "install"},
			allow:  []string{`{"source":{"name":["install"]}}`},
			deny:   []string{`{"reference":{"resource":["configmaps"]}}`},
			refs:   refs,
		},
		"outside the backend namespaces": {
			source:     types.NamespacedName{Namespace: "tink-system", Name: "install"},
			namespaces: []string{"other"},
			allow:      []string{`{"source":{"name":["install"]}}`},
			refs:       refs,
			wantErr:    true,
		},
		"no references": {source: types.NamespacedName{Namespace: "tink-system", Name: "install"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := &Backend{
				dynamicClient:                   dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm),
				Namespaces:                      tt.namespaces,
				HardwareReferenceAllowListRules: tt.allow,
				HardwareReferenceDenyListRules:  tt.deny,
			}
			got, err := b.ResolveReferencesFor(context.Background(), tt.source, tt.refs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got == nil {
				t.Fatal("want a map even when nothing resolves")
			}
			if wantResolved := len(tt.refs) > 0 && !tt.wantErr; wantResolved != (got["cm"] != nil) {
				t.Fatalf("resolved %v, want the ConfigMap resolved: %v", got, wantResolved)
			}
		})
	}
}

// TestDocumentedRulesMatch guards the rule examples in docs/technical/REFERENCES.md.
func TestDocumentedRulesMatch(t *testing.T) {
	ed := evaluationData{
		Source:    source{Name: "example1", Namespace: "tink-system"},
		Reference: v1alpha2.Reference{Namespace: "example", Name: "exampleLVM", Group: "example.org", Version: "v1alpha1", Resource: "lvms"},
	}
	for _, rule := range []string{
		`{"source":{"namespace":["tink-system"]}}`,
		`{"reference":{"resource":["lvms"]}}`,
		`{"source":{"namespace":["tink-system"]},"reference":{"resource":["lvms"]}}`,
		`{"source":{"name":["example1"],"namespace":["tink-system"]},"reference":{"name":["exampleLVM"],"namespace":["example"],"resource":["lvms"]}}`,
	} {
		matched, _, err := evaluate(context.Background(), []string{rule}, ed)
		if err != nil || !matched {
			t.Errorf("rule %s: matched = %v, err = %v", rule, matched, err)
		}
	}
}

func TestMatch(t *testing.T) {
	tests := map[string]struct {
		rules         []string
		data          evaluationData
		expectedMatch bool
		expectedRules string
		expectedErr   bool
	}{
		"no match empty rules": {
			rules: []string{},
			data: evaluationData{
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "hardware",
				},
			},
			expectedMatch: false,
		},
		"no match empty data struct": {
			rules:         []string{`{"reference":{"name":[{"wildcard":"*"}]}}`},
			data:          evaluationData{Reference: v1alpha2.Reference{}},
			expectedMatch: false,
		},
		"no match": {
			rules: []string{`{"reference":{"resource":["workflows"]}},{"version":["example"]}`},
			data: evaluationData{
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "hardware",
				},
			},
			expectedMatch: false,
		},
		"match": {
			rules: []string{`{"reference":{"name":["example"]}}`},
			data: evaluationData{
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "hardware",
				},
			},
			expectedMatch: true,
			expectedRules: `pattern-{"reference":{"name":["example"]}}`,
		},
		"deny all": {
			rules: []string{`{"reference":{"name":[{"wildcard":"*"}]}}`},
			data: evaluationData{
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "hardware",
				},
			},
			expectedMatch: true,
			expectedRules: `pattern-{"reference":{"name":[{"wildcard":"*"}]}}`,
		},
		"bad rule": {
			rules: []string{"this is not the rule format"},
			data: evaluationData{
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "hardware",
				},
			},
			expectedMatch: false,
			expectedErr:   true,
		},
		"match reference and source": {
			rules: []string{`{"reference":{"resource":["hardware"],"namespace":["tink"]},"source":{"namespace":["tink-system"]}}`},
			data: evaluationData{
				Source: source{
					Namespace: "tink-system",
				},
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "hardware",
				},
			},
			expectedMatch: true,
			expectedRules: `pattern-{"reference":{"resource":["hardware"],"namespace":["tink"]},"source":{"namespace":["tink-system"]}}`,
		},
		"case insensitive no match": {
			rules: []string{`{"reference":{"resource":["hardware"],"namespace":["tink"]},"source":{"namespace":["tink-system"]}}`},
			data: evaluationData{
				Source: source{
					Namespace: "tink-system",
				},
				Reference: v1alpha2.Reference{
					Namespace: "tink",
					Name:      "example",
					Group:     "tinkerbell.org",
					Version:   "v1alpha1",
					Resource:  "Hardware",
				},
			},
			expectedMatch: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, rules, err := evaluate(context.TODO(), test.rules, test.data)
			if err != nil && !test.expectedErr {
				t.Fatalf("match() error = %v", err)
			}
			if got != test.expectedMatch {
				t.Errorf("match() found: got = %v, want %v", got, test.expectedMatch)
			}
			if rules != test.expectedRules {
				t.Errorf("match() rules: got = %v, want %v", rules, test.expectedRules)
			}
		})
	}
}
