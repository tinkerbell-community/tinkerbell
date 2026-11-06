package conversion

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTemplateTaskNames(t *testing.T) {
	c := &TemplateConversion{Tasks: []TemplateTask{{Name: "OS Install"}, {Name: "os install"}, {Name: "!!"}}}
	got := TemplateTaskNames("tpl", c)
	if d := cmp.Diff([]string{"tpl-os-install", "tpl-os-install-2", "tpl-2"}, got); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

func TestWorkflowRuleSetToPolicy(t *testing.T) {
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
`
	c, err := ConvertTemplate(tpl, funcmap.New())
	if err != nil {
		t.Fatal(err)
	}
	rs := &v1alpha1.WorkflowRuleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "enroll", Namespace: "tink"},
		Spec: v1alpha1.WorkflowRuleSetSpec{
			Rules: []string{`{"cpu":{"totalCores":[{"numeric":[">=",8]}]}}`, `{"networkInterfaces":{"mac":[{"prefix":"52:54"}]}}`},
			Workflow: v1alpha1.WorkflowRuleSetWorkflow{
				AddAttributes: true,
				Template:      v1alpha1.TemplateConfig{Ref: "enroll", AgentValue: "device_id", KVs: map[string]string{"site": "lab"}},
			},
		},
	}
	p, err := WorkflowRuleSetToPolicy(rs, c)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Spec.Rules.WorkflowAutoCreation[0]
	if p.Name != "enroll" || p.Namespace != "tink" || !r.Config.AddAttributes || deref(r.Config.TimeoutSeconds) != 1800 {
		t.Errorf("policy: %+v", p)
	}
	if r.Config.Globals.TemplateMap["site"] != "lab" || r.Config.Tasks[0].TaskRef != (v1alpha2.SimpleReference{Name: "enroll-inventory", Namespace: "tink"}) {
		t.Errorf("workflow config: %+v", r.Config)
	}
	// The rules are the same Quamina patterns.
	for i, want := range rs.Spec.Rules {
		got, err := json.Marshal(r.Rules[i])
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqual(t, want, string(got)) {
			t.Errorf("rule %d: got %s, want %s", i, got, want)
		}
	}

	rs.Spec.Rules = []string{`{"cpu":{"vendorString":["x"]}}`}
	if _, err := WorkflowRuleSetToPolicy(rs, c); !IsNotConvertible(err) {
		t.Errorf("unknown attribute: want NotConvertibleError, got %v", err)
	}
	rs.Spec.Rules = nil
	rs.Spec.Workflow.Template.AgentValue = "other"
	if _, err := WorkflowRuleSetToPolicy(rs, c); !IsNotConvertible(err) {
		t.Errorf("worker is not the agent: want NotConvertibleError, got %v", err)
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatal(err)
	}
	return cmp.Equal(x, y)
}

func TestMachineConnectionToV1Alpha2(t *testing.T) {
	got, err := MachineConnectionToV1Alpha2(bmcv1.Connection{
		Host: "10.0.80.1", Port: 623, InsecureTLS: true,
		AuthSecretRef: corev1.SecretReference{Name: "creds", Namespace: "tink"},
		ProviderOptions: &bmcv1.ProviderOptions{
			PreferredOrder: []bmcv1.ProviderName{"ipmi"},
			IPMI:           &bmcv1.IPMITOOLOptions{Port: 623, CipherSuite: "17"},
			HomeAssistant:  &bmcv1.HomeAssistantOptions{SwitchEntityID: "switch.node1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := &bmcv2.Connection{
		Host: "10.0.80.1", Port: 623, InsecureTLS: true,
		CredentialsRef: bmcv2.SimpleReference{Name: "creds", Namespace: "tink"},
		ProviderOptions: &bmcv2.ProviderOptions{
			PreferredOrder: []bmcv2.ProviderName{"ipmi"},
			IPMI:           &bmcv2.IPMITOOLOptions{Port: 623, CipherSuite: "17"},
			HomeAssistant:  &bmcv2.HomeAssistantOptions{SwitchEntityID: "switch.node1"},
		},
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}
