package grpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/constant"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/pkg/proto"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// pattern decodes a Quamina pattern of Agent attributes.
func pattern(t *testing.T, s string) v1alpha2.AgentAttributes {
	t.Helper()
	var a v1alpha2.AgentAttributes
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func serialPolicy(t *testing.T, name, namespace string, patterns ...string) v1alpha2.Policy {
	t.Helper()
	rule := v1alpha2.WorkflowRule{
		Config: v1alpha2.WorkflowConfig{
			AddAttributes:  true,
			Disabled:       toPtr(false),
			Globals:        &v1alpha2.Extra{TemplateMap: map[string]string{"site": "lab"}},
			References:     map[string]v1alpha2.Reference{"cfg": {Name: "cfg", Resource: "configmaps", Version: "v1"}},
			TimeoutSeconds: toPtr(int64(1800)),
			Tasks: []v1alpha2.WorkflowTaskConfig{
				{TaskRef: v1alpha2.SimpleReference{Name: "inventory"}, TimeoutSeconds: toPtr(int64(300)), Extra: &v1alpha2.Extra{Env: map[string]string{"A": "b"}}},
				{TaskRef: v1alpha2.SimpleReference{Name: "provision", Namespace: namespace}},
			},
		},
	}
	for _, p := range patterns {
		rule.Rules = append(rule.Rules, pattern(t, p))
	}
	return v1alpha2.Policy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID("uid-" + name)},
		Spec:       v1alpha2.PolicySpec{Rules: v1alpha2.Rules{WorkflowAutoCreation: []v1alpha2.WorkflowRule{rule}}},
	}
}

type enrollmentSources struct {
	policies []v1alpha2.Policy
	ruleSets []tinkerbell.WorkflowRuleSet
	created  *v1alpha2.Workflow
}

func (e *enrollmentSources) ListPolicies(context.Context, data.WorkflowFilter) ([]v1alpha2.Policy, error) {
	return e.policies, nil
}

func (e *enrollmentSources) ListWorkflowRuleSets(context.Context, data.WorkflowFilter) ([]tinkerbell.WorkflowRuleSet, error) {
	return e.ruleSets, nil
}

func (e *enrollmentSources) CreateWorkflowV1Alpha2(_ context.Context, wf *v1alpha2.Workflow) error {
	e.created = wf.DeepCopy()
	return nil
}

func (e *enrollmentSources) handler() *Handler {
	return &Handler{
		Logger:  logr.Discard(),
		Backend: &mockBackendReadWriter{},
		AutoCapabilities: AutoCapabilities{Enrollment: AutoEnrollment{
			Enabled: true, PolicyLister: e, WorkflowRuleSetLister: e, WorkflowCreator: e,
		}},
	}
}

var serialAttributes = &proto.AgentAttributes{Chassis: &proto.Chassis{Serial: toPtr("12345")}}

func enrollmentHardware(namespace string, enabled bool) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw", Namespace: namespace},
		Spec:       v1alpha2.HardwareSpec{AgentID: "agent-1", Auto: v1alpha2.AutoCapabilities{EnrollmentEnabled: enabled}},
	}
}

func TestEnrollPolicy(t *testing.T) {
	src := &enrollmentSources{policies: []v1alpha2.Policy{serialPolicy(t, "enroll", "tink", `{"chassis":{"serial":["12345"]}}`)}}
	err := src.handler().enroll(context.Background(), "agent-1", convert(serialAttributes), enrollmentHardware("tink", true))
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound until the Workflow renders", err)
	}
	if src.created == nil {
		t.Fatal("no Workflow created")
	}
	hwRef := &v1alpha2.WorkflowHardware{HardwareRef: &v1alpha2.SimpleReference{Name: "hw", Namespace: "tink"}}
	want := v1alpha2.WorkflowSpec{
		Disabled:       toPtr(false),
		Globals:        &v1alpha2.Extra{TemplateMap: map[string]string{"site": "lab"}},
		References:     map[string]v1alpha2.Reference{"cfg": {Name: "cfg", Resource: "configmaps", Version: "v1"}},
		TimeoutSeconds: toPtr(int64(1800)),
		Tasks: []v1alpha2.WorkflowTask{
			{AgentID: "agent-1", TaskRef: v1alpha2.SimpleReference{Name: "inventory"}, TimeoutSeconds: toPtr(int64(300)), Extra: &v1alpha2.Extra{Env: map[string]string{"A": "b"}}, Hardware: hwRef},
			{AgentID: "agent-1", TaskRef: v1alpha2.SimpleReference{Name: "provision", Namespace: "tink"}, Hardware: hwRef},
		},
	}
	if d := cmp.Diff(want, src.created.Spec); d != "" {
		t.Errorf("spec (-want +got):\n%s", d)
	}
	m := src.created.ObjectMeta
	if m.Name != "enrollment-agent-1" || m.Namespace != "tink" || m.Labels["tinkerbell.org/auto-enrollment"] != "true" || m.Annotations[constant.AttributesAnnotation] == "" {
		t.Errorf("metadata = %+v", m)
	}
	if d := cmp.Diff([]metav1.OwnerReference{{APIVersion: "tinkerbell.org/v1alpha2", Kind: "Policy", Name: "enroll", UID: "uid-enroll"}}, m.OwnerReferences); d != "" {
		t.Errorf("owner (-want +got):\n%s", d)
	}
	if _, ok := m.Annotations[conversion.AnnotationTemplate]; ok {
		t.Error("a Policy's Workflow is native")
	}
}

// TestEnrollTranslatedRuleSet verifies a rule set the Tink controller translated enrolls only through its
// Policy, whose Workflow runs the Template's Tasks.
func TestEnrollTranslatedRuleSet(t *testing.T) {
	tpl := `version: "0.1"
name: enroll
global_timeout: 1800
tasks:
  - name: inventory
    worker: "{{.device_id}}"
    actions:
      - name: report
        image: quay.io/x/report
        environment:
          SITE: "{{ .site }}"
          AGENT: "{{ .device_id }}"
`
	conv, err := conversion.ConvertTemplate(tpl, funcmap.New())
	if err != nil {
		t.Fatal(err)
	}
	rs := tinkerbell.WorkflowRuleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "tink", UID: "rs-uid"},
		Spec: tinkerbell.WorkflowRuleSetSpec{
			Rules: []string{`{"chassis":{"serial":["12345"]}}`},
			Workflow: tinkerbell.WorkflowRuleSetWorkflow{
				Template: tinkerbell.TemplateConfig{Ref: "enroll", AgentValue: "device_id", KVs: map[string]string{"site": "lab"}},
			},
		},
	}
	policy, err := conversion.WorkflowRuleSetToPolicy(&rs, conv)
	if err != nil {
		t.Fatal(err)
	}
	policy.UID = "policy-uid"
	policy.OwnerReferences = []metav1.OwnerReference{{APIVersion: "tinkerbell.org/v1alpha1", Kind: "WorkflowRuleSet", Name: "rs", UID: "rs-uid", Controller: toPtr(true)}}

	src := &enrollmentSources{policies: []v1alpha2.Policy{*policy}, ruleSets: []tinkerbell.WorkflowRuleSet{rs}}
	_ = src.handler().enroll(context.Background(), "agent-1", convert(serialAttributes), nil)
	if src.created == nil {
		t.Fatal("no Workflow created")
	}
	if ref := metav1.GetControllerOf(src.created); ref != nil || src.created.OwnerReferences[0].Kind != "Policy" {
		t.Errorf("owners = %+v, want the Policy", src.created.OwnerReferences)
	}
	want := []v1alpha2.WorkflowTask{{AgentID: "agent-1", TaskRef: v1alpha2.SimpleReference{Name: "enroll-inventory", Namespace: "tink"}}}
	if d := cmp.Diff(want, src.created.Spec.Tasks); d != "" {
		t.Errorf("tasks (-want +got):\n%s", d)
	}
	if src.created.Spec.Globals.TemplateMap["site"] != "lab" || *src.created.Spec.TimeoutSeconds != 1800 {
		t.Errorf("spec = %+v", src.created.Spec)
	}

	// Without the Policy, the rule set enrolls as before.
	src = &enrollmentSources{ruleSets: []tinkerbell.WorkflowRuleSet{rs}}
	_ = src.handler().enroll(context.Background(), "agent-1", convert(serialAttributes), nil)
	if src.created == nil || src.created.Annotations[conversion.AnnotationTemplate] != "enroll" {
		t.Fatalf("created = %+v, want a v1alpha1 compatibility mode Workflow", src.created)
	}
}

func TestEnrollBestMatch(t *testing.T) {
	ruleSet := func(patterns ...string) tinkerbell.WorkflowRuleSet {
		return tinkerbell.WorkflowRuleSet{
			ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "tink", UID: "rs-uid"},
			Spec: tinkerbell.WorkflowRuleSetSpec{
				Rules:    patterns,
				Workflow: tinkerbell.WorkflowRuleSetWorkflow{Template: tinkerbell.TemplateConfig{Ref: "tpl", AgentValue: "device_1"}},
			},
		}
	}
	exact := `{"chassis":{"serial":["12345"]}}`
	prefix := `{"chassis":{"serial":[{"prefix":"123"}]}}`
	tests := map[string]struct {
		policies   []v1alpha2.Policy
		ruleSets   []tinkerbell.WorkflowRuleSet
		hardware   *v1alpha2.Hardware
		wantPolicy string // the Policy the Workflow comes from; "" for the rule set, "-" for none
	}{
		"Policy matching more patterns wins": {
			policies:   []v1alpha2.Policy{serialPolicy(t, "p", "tink", exact, prefix)},
			ruleSets:   []tinkerbell.WorkflowRuleSet{ruleSet(exact)},
			wantPolicy: "p",
		},
		"rule set matching more patterns wins": {
			policies: []v1alpha2.Policy{serialPolicy(t, "p", "tink", exact)},
			ruleSets: []tinkerbell.WorkflowRuleSet{ruleSet(exact, prefix)},
		},
		"Policy that does not match": {
			policies: []v1alpha2.Policy{serialPolicy(t, "p", "tink", `{"chassis":{"serial":["999"]}}`)},
			ruleSets: []tinkerbell.WorkflowRuleSet{ruleSet(exact)},
		},
		"Policy outside the Hardware's namespace": {
			policies:   []v1alpha2.Policy{serialPolicy(t, "p", "other", exact)},
			hardware:   enrollmentHardware("tink", true),
			wantPolicy: "-",
		},
		"Policy rule naming another namespace": {
			policies: func() []v1alpha2.Policy {
				p := serialPolicy(t, "p", "tink", exact)
				p.Spec.Rules.WorkflowAutoCreation[0].Config.Namespace = "other"
				return []v1alpha2.Policy{p}
			}(),
			wantPolicy: "-",
		},
		"Policy rule without tasks": {
			policies: func() []v1alpha2.Policy {
				p := serialPolicy(t, "p", "tink", exact)
				p.Spec.Rules.WorkflowAutoCreation[0].Config.Tasks = nil
				return []v1alpha2.Policy{p}
			}(),
			wantPolicy: "-",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			src := &enrollmentSources{policies: tt.policies, ruleSets: tt.ruleSets}
			err := src.handler().enroll(context.Background(), "agent-1", convert(serialAttributes), tt.hardware)
			if status.Code(err) != codes.NotFound {
				t.Fatalf("err = %v, want NotFound", err)
			}
			switch {
			case tt.wantPolicy == "-":
				if src.created != nil {
					t.Fatalf("created %+v, want none", src.created.OwnerReferences)
				}
			case src.created == nil:
				t.Fatal("no Workflow created")
			case tt.wantPolicy == "":
				if src.created.OwnerReferences[0].Kind != "WorkflowRuleSet" {
					t.Errorf("owner = %+v, want the rule set", src.created.OwnerReferences)
				}
			default:
				if o := src.created.OwnerReferences[0]; o.Kind != "Policy" || o.Name != tt.wantPolicy {
					t.Errorf("owner = %+v, want Policy %s", o, tt.wantPolicy)
				}
			}
		})
	}
}

func TestEnrollPolicyDisabledHardware(t *testing.T) {
	src := &enrollmentSources{policies: []v1alpha2.Policy{serialPolicy(t, "p", "tink", `{"chassis":{"serial":["12345"]}}`)}}
	err := src.handler().enroll(context.Background(), "agent-1", convert(serialAttributes), enrollmentHardware("tink", false))
	if status.Code(err) != codes.FailedPrecondition || src.created != nil {
		t.Fatalf("err = %v, created = %v; want enrollment refused", err, src.created)
	}
}
