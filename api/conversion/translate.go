package conversion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var invalidNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

// TemplateTaskNames returns the names of the Tasks a converted Template's tasks
// become: "<template>-<task>", made a valid object name and unique.
func TemplateTaskNames(template string, c *TemplateConversion) []string {
	names := make([]string, len(c.Tasks))
	seen := map[string]bool{}
	for i, t := range c.Tasks {
		suffix := strings.Trim(invalidNameChars.ReplaceAllString(strings.ToLower(t.Name), "-"), "-")
		if suffix == "" {
			suffix = strconv.Itoa(i)
		}
		name := template + "-" + suffix
		if len(name) > 253 {
			name = strings.TrimRight(name[:253], "-")
		}
		for n := 2; seen[name]; n++ {
			name = fmt.Sprintf("%s-%s-%d", template, suffix, n)
		}
		seen[name] = true
		names[i] = name
	}
	return names
}

// TemplateTasks returns the v1alpha2 Tasks for a converted Template, in the
// Template's namespace.
func TemplateTasks(tpl *v1alpha1.Template, c *TemplateConversion) []v1alpha2.Task {
	names := TemplateTaskNames(tpl.Name, c)
	out := make([]v1alpha2.Task, len(c.Tasks))
	for i, t := range c.Tasks {
		out[i] = v1alpha2.Task{
			TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha2.GroupVersion.String(), Kind: "Task"},
			ObjectMeta: metav1.ObjectMeta{Name: names[i], Namespace: tpl.Namespace},
			Spec:       *t.Spec.DeepCopy(),
		}
	}
	return out
}

// WorkflowRuleSetToPolicy translates a WorkflowRuleSet to a Policy, given the
// conversion of the Template it uses. The Policy creates the same Workflows,
// running the Template's Tasks on the enrolling Agent. That requires every
// task's worker to be the rule set's agentValue, which is the Agent; anything
// else returns a NotConvertibleError.
func WorkflowRuleSetToPolicy(rs *v1alpha1.WorkflowRuleSet, c *TemplateConversion) (*v1alpha2.Policy, error) {
	wf := rs.Spec.Workflow
	rule := v1alpha2.WorkflowRule{
		Config: v1alpha2.WorkflowConfig{
			AddAttributes: wf.AddAttributes,
			Disabled:      copyPtr(wf.Disabled),
		},
	}
	for _, r := range rs.Spec.Rules {
		var a v1alpha2.AgentAttributes
		dec := json.NewDecoder(bytes.NewReader([]byte(r)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			return nil, notConvertible("rule %s is not an Agent attribute pattern v1alpha2 can express: %v", r, err)
		}
		rule.Rules = append(rule.Rules, a)
	}
	if len(wf.Template.KVs) > 0 {
		rule.Config.Globals = &v1alpha2.Extra{TemplateMap: copyMap(wf.Template.KVs)}
	}
	if c.GlobalTimeout > 0 {
		rule.Config.TimeoutSeconds = ptr(c.GlobalTimeout)
	}
	names := TemplateTaskNames(wf.Template.Ref, c)
	for i, t := range c.Tasks {
		if t.WorkerKey == "" || t.WorkerKey != wf.Template.AgentValue {
			return nil, notConvertible("task %q runs on %q, not on the enrolling Agent (%q)", t.Name, orDefault(t.WorkerKey, t.Worker), wf.Template.AgentValue)
		}
		rule.Config.Tasks = append(rule.Config.Tasks, v1alpha2.WorkflowTaskConfig{
			TaskRef: v1alpha2.SimpleReference{Name: names[i], Namespace: rs.Namespace},
		})
	}
	return &v1alpha2.Policy{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha2.GroupVersion.String(), Kind: "Policy"},
		ObjectMeta: metav1.ObjectMeta{Name: rs.Name, Namespace: rs.Namespace},
		Spec:       v1alpha2.PolicySpec{Rules: v1alpha2.Rules{WorkflowAutoCreation: []v1alpha2.WorkflowRule{rule}}},
	}, nil
}

// MachineConnectionToV1Alpha2 converts the connection of a deprecated bmc
// Machine to the v1alpha2 form used in Hardware.spec.bmc.connection and Jobs.
func MachineConnectionToV1Alpha2(c bmcv1.Connection) (*bmcv2.Connection, error) {
	out := &bmcv2.Connection{
		Host:           c.Host,
		Port:           c.Port,
		InsecureTLS:    c.InsecureTLS,
		CredentialsRef: bmcv2.SimpleReference{Name: c.AuthSecretRef.Name, Namespace: c.AuthSecretRef.Namespace},
	}
	if c.ProviderOptions != nil {
		// The provider options have the same JSON form in both versions.
		out.ProviderOptions = &bmcv2.ProviderOptions{}
		if err := jsonCopy(c.ProviderOptions, out.ProviderOptions); err != nil {
			return nil, fmt.Errorf("converting BMC provider options: %w", err)
		}
	}
	return out, nil
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
