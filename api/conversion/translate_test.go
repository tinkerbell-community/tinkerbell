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

func TestRewriteWorkflowToTasks(t *testing.T) {
	c, err := ConvertTemplate(captTemplate, funcmap.New())
	if err != nil {
		t.Fatal(err)
	}
	tpl := &v1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "capt", Namespace: "tink"}}
	v1wf := &v1alpha1.Workflow{
		TypeMeta:   metav1.TypeMeta{APIVersion: "tinkerbell.org/v1alpha1", Kind: "Workflow"},
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "tink"},
		Spec: v1alpha1.WorkflowSpec{
			TemplateRef: "capt", HardwareRef: "hw1",
			HardwareMap: map[string]string{"device_1": "52:54:00:00:00:01", "mirror": "http://mirror"},
			BootOptions: v1alpha1.BootOptions{ToggleAllowNetboot: true, BootMode: v1alpha1.BootModeNetboot},
		},
	}
	// As stored: converted from v1alpha1 by the webhook.
	wf := wfToV2(t, v1wf)
	if err := RewriteWorkflowToTasks(wf, tpl, c); err != nil {
		t.Fatal(err)
	}
	want := []v1alpha2.WorkflowTask{{
		AgentID: "52:54:00:00:00:01",
		TaskRef: v1alpha2.SimpleReference{Name: "capt-talos", Namespace: "tink"},
		Hardware: &v1alpha2.WorkflowHardware{
			HardwareRef: &v1alpha2.SimpleReference{Name: "hw1", Namespace: "tink"},
			BootOptions: &v1alpha2.BootOptions{ToggleNetboot: true, BootMode: v1alpha2.BootModeNetboot},
		},
	}}
	if d := cmp.Diff(want, wf.Spec.Tasks); d != "" {
		t.Errorf("tasks (-want +got):\n%s", d)
	}
	if deref(wf.Spec.TimeoutSeconds) != 6000 || wf.Spec.Globals.TemplateMap["mirror"] != "http://mirror" {
		t.Errorf("timeout/globals: %+v", wf.Spec)
	}
	// Cluster API Provider Tinkerbell keeps seeing the Workflow it created.
	view := wfToV1(t, wf)
	if d := cmp.Diff(v1wf.Spec, view.Spec, cmpOpts...); d != "" {
		t.Errorf("v1alpha1 view of the rewritten Workflow (-want +got):\n%s", d)
	}
	// The translated Tasks carry the template task name for status.
	if got := TemplateTasks(tpl, c)[0]; got.Name != "capt-talos" || got.Annotations[AnnotationTemplateTask] != "talos" {
		t.Errorf("task: %s %v", got.Name, got.Annotations)
	}

	// Not rewritten: started, boot options with several tasks, unknown worker.
	started := wfToV2(t, v1wf)
	started.Status.Metadata.Workflow.State = v1alpha2.StateRunning
	if err := RewriteWorkflowToTasks(started, tpl, c); !IsNotConvertible(err) {
		t.Errorf("started workflow: want NotConvertibleError, got %v", err)
	}
	two := *c
	two.Tasks = append(slicesClone(c.Tasks), c.Tasks[0])
	if err := RewriteWorkflowToTasks(wfToV2(t, v1wf), tpl, &two); !IsNotConvertible(err) {
		t.Errorf("boot options with two tasks: want NotConvertibleError, got %v", err)
	}
	noBoot := v1wf.DeepCopy()
	noBoot.Spec.BootOptions = v1alpha1.BootOptions{}
	nb := wfToV2(t, noBoot)
	if err := RewriteWorkflowToTasks(nb, tpl, &two); err != nil || len(nb.Spec.Tasks) != 2 || nb.Spec.Tasks[1].TaskRef.Name != "capt-talos-2" {
		t.Errorf("two tasks without boot options: %v %+v", err, nb.Spec.Tasks)
	}
	noKey := v1wf.DeepCopy()
	noKey.Spec.HardwareMap = map[string]string{"mirror": "x"}
	if err := RewriteWorkflowToTasks(wfToV2(t, noKey), tpl, c); !IsNotConvertible(err) {
		t.Errorf("missing worker: want NotConvertibleError, got %v", err)
	}
}

func slicesClone[T any](s []T) []T { return append([]T(nil), s...) }
