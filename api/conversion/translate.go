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

// AnnotationTemplateTask is set on Tasks translated from a v1alpha1 Template to
// the name of the template task they came from. The Workflow engine uses it as
// the rendered task's name, so Workflows rewritten onto these Tasks report the
// same task names as before.
const AnnotationTemplateTask = "tinkerbell.org/v1alpha1-template-task"

// TemplateTasks returns the v1alpha2 Tasks for a converted Template, in the
// Template's namespace.
func TemplateTasks(tpl *v1alpha1.Template, c *TemplateConversion) []v1alpha2.Task {
	names := TemplateTaskNames(tpl.Name, c)
	out := make([]v1alpha2.Task, len(c.Tasks))
	for i, t := range c.Tasks {
		out[i] = v1alpha2.Task{
			TypeMeta: metav1.TypeMeta{APIVersion: v1alpha2.GroupVersion.String(), Kind: "Task"},
			ObjectMeta: metav1.ObjectMeta{
				Name: names[i], Namespace: tpl.Namespace,
				Annotations: map[string]string{AnnotationTemplate: tpl.Name, AnnotationTemplateTask: t.Name},
			},
			Spec: *t.Spec.DeepCopy(),
		}
	}
	return out
}

// RewriteWorkflowToTasks turns a Workflow created through v1alpha1, which runs
// its Template in v1alpha1 compatibility mode, into a native v1alpha2 Workflow
// that runs the Template's translated Tasks (see TemplateTasks). c is the
// conversion of tpl. wf is modified in place.
//
// Only a Workflow that has not started is rewritten: one already running or
// finished keeps the Template it started with. Each template task becomes a
// task entry referencing its Task, on the Agent its worker names (the
// hardwareMap value of the worker key, or the literal worker), bound to the
// Workflow's Hardware as all v1alpha1 tasks were. Boot options are per task in
// v1alpha2, so a Workflow with boot options is only rewritten when the Template
// has a single task: with several, netboot would be turned off and post
// operations run after the first task instead of after the last. Workflows
// that cannot be rewritten get a NotConvertibleError and keep running in
// compatibility mode.
func RewriteWorkflowToTasks(wf *v1alpha2.Workflow, tpl *v1alpha1.Template, c *TemplateConversion) error {
	if wf.Annotations[AnnotationTemplate] != tpl.Name || wf.Namespace != tpl.Namespace {
		return notConvertible("workflow %s/%s does not run template %s/%s", wf.Namespace, wf.Name, tpl.Namespace, tpl.Name)
	}
	if wf.Status.Metadata.Workflow.State != 0 || len(wf.Status.RenderedTasks) > 0 {
		return notConvertible("workflow %s/%s has already started", wf.Namespace, wf.Name)
	}
	if len(wf.Spec.Tasks) > 1 {
		return notConvertible("workflow %s/%s already lists several tasks", wf.Namespace, wf.Name)
	}
	var hw *v1alpha2.WorkflowHardware
	for _, t := range wf.Spec.Tasks {
		if t.TaskRef.Name != "" {
			return notConvertible("workflow %s/%s already references Tasks", wf.Namespace, wf.Name)
		}
		hw = t.Hardware
	}
	if hw != nil && hw.BootOptions != nil && len(c.Tasks) > 1 {
		return notConvertible("workflow %s/%s has boot options and template %s has %d tasks; v1alpha2 applies boot options per task", wf.Namespace, wf.Name, tpl.Name, len(c.Tasks))
	}
	var templateMap map[string]string
	if wf.Spec.Globals != nil {
		templateMap = wf.Spec.Globals.TemplateMap
	}

	names := TemplateTaskNames(tpl.Name, c)
	tasks := make([]v1alpha2.WorkflowTask, 0, len(c.Tasks))
	for i, t := range c.Tasks {
		agent := t.Worker
		if t.WorkerKey != "" {
			agent = templateMap[t.WorkerKey]
		}
		if agent == "" {
			return notConvertible("task %q of workflow %s/%s has no Agent: hardwareMap has no %q", t.Name, wf.Namespace, wf.Name, t.WorkerKey)
		}
		wt := v1alpha2.WorkflowTask{AgentID: agent, TaskRef: v1alpha2.SimpleReference{Name: names[i], Namespace: wf.Namespace}}
		if hw != nil && hw.HardwareRef != nil {
			wt.Hardware = &v1alpha2.WorkflowHardware{HardwareRef: hw.HardwareRef.DeepCopy()}
			if hw.BootOptions != nil {
				wt.Hardware.BootOptions = hw.BootOptions.DeepCopy()
			}
		}
		tasks = append(tasks, wt)
	}
	wf.Spec.Tasks = tasks
	if wf.Spec.TimeoutSeconds == nil && c.GlobalTimeout > 0 {
		wf.Spec.TimeoutSeconds = ptr(c.GlobalTimeout)
	}
	return nil
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
