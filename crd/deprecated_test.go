package crd

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const deprecatedTestTemplate = `version: "0.1"
name: provision
global_timeout: 1800
tasks:
  - name: "os install"
    worker: "{{.device_1}}"
    actions:
      - name: "stream"
        image: quay.io/tinkerbell/actions/image2disk:latest
        timeout: 600
`

// TestRemoveDeprecated sets up the objects of the deprecated kinds as the Tink
// controller and Rufio leave them once they have translated what they can,
// with some objects that keep their CRD, and checks that the migration removes
// exactly the CRDs nothing depends on any more, without losing what moved to
// v1alpha2. It needs a kube-apiserver and etcd: set KUBEBUILDER_ASSETS.
func TestRemoveDeprecated(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	// Only objects stored as v1alpha2 are read here, so the conversion webhook
	// is never called.
	webhook := WithConversionWebhook(ConversionWebhook{URL: "https://127.0.0.1:1/convert"})
	install, err := NewTinkerbell(WithRestConfig(cfg), webhook)
	if err != nil {
		t.Fatal(err)
	}
	if err := install.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}
	scheme, err := deprecatedScheme()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	create := func(objs ...client.Object) {
		t.Helper()
		for _, o := range objs {
			if err := c.Create(ctx, o); err != nil {
				t.Fatalf("creating %T %s: %v", o, o.GetName(), err)
			}
		}
	}
	controller := func(apiVersion, kind string, owner client.Object) metav1.OwnerReference {
		return metav1.OwnerReference{APIVersion: apiVersion, Kind: kind, Name: owner.GetName(), UID: owner.GetUID(), Controller: ptrTo(true)}
	}
	const ns = "default"

	// A Template the Tink controller translated to Tasks, and one it could not.
	tpl := &v1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "provision", Namespace: ns}, Spec: v1alpha1.TemplateSpec{Data: ptrTo(deprecatedTestTemplate)}}
	untranslated := &v1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "structural", Namespace: ns, Annotations: map[string]string{annotationTranslation: "a range produces its actions"}},
		Spec:       v1alpha1.TemplateSpec{Data: ptrTo("version: \"0.1\"")},
	}
	create(tpl, untranslated)
	conv, err := conversion.ConvertTemplate(deprecatedTestTemplate, funcmap.New())
	if err != nil {
		t.Fatal(err)
	}
	tasks := conversion.TemplateTasks(tpl, conv)
	for i := range tasks {
		tasks[i].OwnerReferences = []metav1.OwnerReference{controller("tinkerbell.org/v1alpha1", "Template", tpl)}
		create(&tasks[i])
	}

	// A rule set translated to a Policy, and a Workflow it created before.
	rs := &v1alpha1.WorkflowRuleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "enroll", Namespace: ns},
		Spec: v1alpha1.WorkflowRuleSetSpec{
			Rules:    []string{`{"chassis":{"serial":["1234"]}}`},
			Workflow: v1alpha1.WorkflowRuleSetWorkflow{Template: v1alpha1.TemplateConfig{Ref: tpl.Name, AgentValue: "device_1"}},
		},
	}
	create(rs)
	policy, err := conversion.WorkflowRuleSetToPolicy(rs, conv)
	if err != nil {
		t.Fatal(err)
	}
	policy.OwnerReferences = []metav1.OwnerReference{controller("tinkerbell.org/v1alpha1", "WorkflowRuleSet", rs)}
	create(policy)
	workflow := func(name string, owners ...metav1.OwnerReference) *v1alpha2.Workflow {
		v1 := &v1alpha1.Workflow{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, OwnerReferences: owners},
			Spec:       v1alpha1.WorkflowSpec{TemplateRef: tpl.Name, HardwareMap: map[string]string{"device_1": "52:54:00:00:00:01"}},
		}
		wf := &v1alpha2.Workflow{}
		if err := conversion.WorkflowToV1Alpha2(v1, wf); err != nil {
			t.Fatal(err)
		}
		return wf
	}
	started := workflow("started", metav1.OwnerReference{APIVersion: "tinkerbell.org/v1alpha1", Kind: "WorkflowRuleSet", Name: rs.Name, UID: rs.UID})
	pending := workflow("pending")
	create(started, pending)
	started.Status.Metadata.Workflow.State = v1alpha2.StateRunning
	if err := c.Status().Update(ctx, started); err != nil {
		t.Fatal(err)
	}

	// A bmc Machine Rufio translated into the Hardware that refers to it, and
	// one whose Hardware has no connection yet.
	machine := func(name string) *bmcv1.Machine {
		return &bmcv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       bmcv1.MachineSpec{Connection: bmcv1.Connection{Host: "10.0.0.10", AuthSecretRef: corev1.SecretReference{Name: "bmc", Namespace: ns}}},
		}
	}
	hardware := func(name, machine string) *v1alpha2.Hardware {
		v1 := &v1alpha1.Hardware{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       v1alpha1.HardwareSpec{BMCRef: &corev1.TypedLocalObjectReference{APIGroup: ptrTo("bmc.tinkerbell.org"), Kind: "Machine", Name: machine}},
		}
		hw := &v1alpha2.Hardware{}
		if err := conversion.HardwareToV1Alpha2(v1, hw); err != nil {
			t.Fatal(err)
		}
		return hw
	}
	m1, m2 := machine("m1"), machine("m2")
	hw1, hw2 := hardware("hw1", m1.Name), hardware("hw2", m2.Name)
	hw1.Annotations[conversion.AnnotationMachine] = ns + "/" + m1.Name
	hw1.Spec.BMC = &v1alpha2.BMC{Connection: &bmcv2.Connection{Host: "10.0.0.10", CredentialsRef: bmcv2.SimpleReference{Name: "bmc", Namespace: ns}}}
	create(m1, m2, hw1, hw2)

	// A bmc Task that has not finished.
	bmcTask := &bmcv1.Task{ObjectMeta: metav1.ObjectMeta{Name: "power-off", Namespace: ns}, Spec: bmcv1.TaskSpec{
		Task:       bmcv1.Action{PowerAction: ptrTo(bmcv1.PowerHardOff)},
		Connection: bmcv1.Connection{Host: "10.0.0.10", AuthSecretRef: corev1.SecretReference{Name: "bmc", Namespace: ns}},
	}}
	create(bmcTask)

	tb, err := NewTinkerbell(WithRestConfig(cfg), webhook, WithDeprecatedCRDRemoval(), WithLogger(testr.New(t)))
	if err != nil {
		t.Fatal(err)
	}
	present := func(want ...string) {
		t.Helper()
		var got []string
		for _, name := range DeprecatedCRDs {
			if _, err := tb.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{}); err == nil {
				got = append(got, name)
			} else if !isNotFound(err) {
				t.Fatal(err)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("deprecated CRDs = %v, want %v", got, want)
		}
	}

	// The rule set moved; the Templates are needed by the pending Workflow and
	// one has no translation; m2 has not moved; the bmc Task is running.
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}
	present("templates.tinkerbell.org", "machines.bmc.tinkerbell.org", "tasks.bmc.tinkerbell.org")
	if err := c.Get(ctx, client.ObjectKeyFromObject(policy), policy); err != nil {
		t.Fatalf("the Policy of the removed rule set: %v", err)
	}
	if len(policy.OwnerReferences) != 0 {
		t.Errorf("Policy owner references = %v, want none", policy.OwnerReferences)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(started), started); err != nil {
		t.Fatalf("the Workflow the removed rule set created: %v", err)
	}
	if len(started.OwnerReferences) != 0 {
		t.Errorf("Workflow owner references = %v, want none", started.OwnerReferences)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(&tasks[0]), &tasks[0]); err != nil {
		t.Fatal(err)
	}
	if len(tasks[0].OwnerReferences) != 1 {
		t.Errorf("the Tasks of a kept Template lost their owner: %v", tasks[0].OwnerReferences)
	}

	// Running it again does not install the removed CRD again.
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}
	present("templates.tinkerbell.org", "machines.bmc.tinkerbell.org", "tasks.bmc.tinkerbell.org")

	// Clear what kept them.
	for _, o := range []client.Object{untranslated, m2, hw2} {
		if err := c.Delete(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	pending.Status.Metadata.Workflow.State = v1alpha2.StateSuccess
	if err := c.Status().Update(ctx, pending); err != nil {
		t.Fatal(err)
	}
	bmcTask.Status.Conditions = []bmcv1.TaskCondition{{Type: bmcv1.TaskCompleted, Status: bmcv1.ConditionTrue}}
	if err := c.Status().Update(ctx, bmcTask); err != nil {
		t.Fatal(err)
	}
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}
	present()

	// The translations stay, detached from what they came from.
	if err := c.Get(ctx, client.ObjectKeyFromObject(&tasks[0]), &tasks[0]); err != nil {
		t.Fatalf("the Task of the removed Template: %v", err)
	}
	if len(tasks[0].OwnerReferences) != 0 {
		t.Errorf("Task owner references = %v, want none", tasks[0].OwnerReferences)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hw1), hw1); err != nil {
		t.Fatal(err)
	}
	if _, ok := hw1.Annotations[conversion.AnnotationMachine]; ok || hw1.Spec.BMC == nil || hw1.Spec.BMC.Connection == nil || hw1.Spec.BMC.Connection.Host != "10.0.0.10" {
		t.Errorf("Hardware after the Machine CRD was removed: annotations %v, bmc %+v", hw1.Annotations, hw1.Spec.BMC)
	}

	// The kinds that exist in v1alpha2 are still there.
	for name := range TinkerbellDefaults {
		_, err := tb.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if isDeprecated(name) != isNotFound(err) {
			t.Errorf("CRD %s: %v", name, err)
		}
	}
}
