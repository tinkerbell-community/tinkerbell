package translate

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const ns = "tink"

// twoTasks is a Template that translates: two tasks on the enrolling Agent.
const twoTasks = `version: "0.1"
name: provision
global_timeout: 1800
tasks:
  - name: "os install"
    worker: "{{.device_1}}"
    actions:
      - name: "stream"
        image: quay.io/tinkerbell/actions/image2disk:latest
        timeout: 600
        environment:
          DEST_DISK: "{{ index .Hardware.Disks 0 }}"
          SITE: "{{ .site }}"
  - name: "reboot"
    worker: "{{.device_1}}"
    actions:
      - name: "reboot"
        image: quay.io/tinkerbell/actions/reboot:latest
        timeout: 90
`

// oneTask is twoTasks without its second task.
const oneTask = `version: "0.1"
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

// structural cannot translate: a range produces its actions.
const structural = `version: "0.1"
name: provision
global_timeout: 1800
tasks:
  - name: "os install"
    worker: "{{.device_1}}"
    actions:
{{- range .disks }}
      - name: "wipe {{ . }}"
        image: quay.io/tinkerbell/actions/wipe:latest
{{- end }}
`

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha2.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

// template returns the Template "provision" with data.
func template(data string) *v1alpha1.Template {
	return &v1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "provision", Namespace: ns, UID: types.UID("uid-provision")},
		Spec:       v1alpha1.TemplateSpec{Data: &data},
	}
}

func tasksOf(t *testing.T, c client.Client) map[string]v1alpha2.Task {
	t.Helper()
	list := &v1alpha2.TaskList{}
	if err := c.List(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	out := map[string]v1alpha2.Task{}
	for _, task := range list.Items {
		out[task.Name] = task
	}
	return out
}

func drain(rec *events.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func reconcileTemplate(t *testing.T, r *TemplateReconciler, name string) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}); err != nil {
		t.Fatal(err)
	}
}

func setData(t *testing.T, c client.Client, name, data string) {
	t.Helper()
	tpl := &v1alpha1.Template{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, tpl); err != nil {
		t.Fatal(err)
	}
	tpl.Spec.Data = &data
	if err := c.Update(context.Background(), tpl); err != nil {
		t.Fatal(err)
	}
}

func annotationOf(t *testing.T, c client.Client, obj client.Object) (string, bool) {
	t.Helper()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatal(err)
	}
	v, ok := obj.GetAnnotations()[AnnotationTranslation]
	return v, ok
}

func TestTemplateReconciler(t *testing.T) {
	tpl := template(twoTasks)
	c := newClient(t, tpl)
	rec := events.NewFakeRecorder(10)
	r := NewTemplateReconciler(c, rec)

	// A Template that translates gets one Task per template task, owned by it.
	reconcileTemplate(t, r, tpl.Name)
	conv, err := ConvertTemplate(tpl)
	if err != nil {
		t.Fatal(err)
	}
	want := conversion.TemplateTasks(tpl, conv)
	got := tasksOf(t, c)
	if len(got) != len(want) {
		t.Fatalf("got Tasks %v, want %d", got, len(want))
	}
	for _, w := range want {
		g, ok := got[w.Name]
		if !ok {
			t.Fatalf("Task %s missing", w.Name)
		}
		if d := cmp.Diff(w.Spec, g.Spec); d != "" {
			t.Errorf("Task %s spec (-want +got):\n%s", w.Name, d)
		}
		if g.Annotations[conversion.AnnotationTemplate] != tpl.Name || g.Annotations[conversion.AnnotationTemplateTask] == "" {
			t.Errorf("Task %s annotations = %v", w.Name, g.Annotations)
		}
		if ref := metav1.GetControllerOf(&g); ref == nil || ref.UID != tpl.UID || ref.Kind != "Template" || ref.APIVersion != v1alpha1.GroupVersion.String() {
			t.Errorf("Task %s controller = %+v, want the Template", w.Name, ref)
		}
	}
	if _, ok := annotationOf(t, c, &v1alpha1.Template{ObjectMeta: tpl.ObjectMeta}); ok {
		t.Error("translated Template is annotated")
	}
	if e := drain(rec); len(e) != 0 {
		t.Errorf("events = %v, want none", e)
	}

	// Dropping a template task deletes its Task.
	setData(t, c, tpl.Name, oneTask)
	reconcileTemplate(t, r, tpl.Name)
	if got := tasksOf(t, c); len(got) != 1 || got["provision-os-install"].Name == "" {
		t.Fatalf("got Tasks %v, want only provision-os-install", got)
	}

	// A Template that no longer translates keeps no Tasks, is annotated, and
	// gets one Warning event.
	setData(t, c, tpl.Name, structural)
	reconcileTemplate(t, r, tpl.Name)
	reconcileTemplate(t, r, tpl.Name)
	if got := tasksOf(t, c); len(got) != 0 {
		t.Fatalf("got Tasks %v, want none", got)
	}
	if reason, ok := annotationOf(t, c, &v1alpha1.Template{ObjectMeta: tpl.ObjectMeta}); !ok || !strings.Contains(reason, "range") {
		t.Errorf("annotation = %q, %v; want the reason", reason, ok)
	}
	if e := drain(rec); len(e) != 1 || !strings.HasPrefix(e[0], "Warning TranslationFailed") {
		t.Errorf("events = %v, want one TranslationFailed warning", e)
	}

	// Translating again removes the annotation.
	setData(t, c, tpl.Name, twoTasks)
	reconcileTemplate(t, r, tpl.Name)
	if got := tasksOf(t, c); len(got) != 2 {
		t.Fatalf("got Tasks %v, want 2", got)
	}
	if _, ok := annotationOf(t, c, &v1alpha1.Template{ObjectMeta: tpl.ObjectMeta}); ok {
		t.Error("annotation kept after translating")
	}

	// A missing Template is not an error.
	reconcileTemplate(t, r, "missing")
}

func TestTemplateReconcilerForeignTask(t *testing.T) {
	tpl := template(oneTask)
	foreign := &v1alpha2.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "provision-os-install", Namespace: ns},
		Spec:       v1alpha2.TaskSpec{Actions: []v1alpha2.Action{{Name: "mine", Image: "alpine"}}},
	}
	c := newClient(t, tpl, foreign)
	rec := events.NewFakeRecorder(10)
	reconcileTemplate(t, NewTemplateReconciler(c, rec), tpl.Name)

	got := tasksOf(t, c)["provision-os-install"]
	if d := cmp.Diff(foreign.Spec, got.Spec); d != "" {
		t.Errorf("a Task the Template does not own was changed (-want +got):\n%s", d)
	}
	if reason, ok := annotationOf(t, c, &v1alpha1.Template{ObjectMeta: tpl.ObjectMeta}); !ok || !strings.Contains(reason, "already exists") {
		t.Errorf("annotation = %q, %v; want the conflict", reason, ok)
	}
}

func ruleSet(name, template, agentValue string) *v1alpha1.WorkflowRuleSet {
	return &v1alpha1.WorkflowRuleSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + name)},
		Spec: v1alpha1.WorkflowRuleSetSpec{
			Rules: []string{`{"chassis":{"serial":["1234"]}}`},
			Workflow: v1alpha1.WorkflowRuleSetWorkflow{
				AddAttributes: true,
				Template:      v1alpha1.TemplateConfig{Ref: template, AgentValue: agentValue, KVs: map[string]string{"site": "lab"}},
			},
		},
	}
}

func TestRuleSetReconciler(t *testing.T) {
	tpl := template(twoTasks)
	rs := ruleSet("enroll", tpl.Name, "device_1")
	c := newClient(t, tpl, rs)
	rec := events.NewFakeRecorder(10)
	r := NewRuleSetReconciler(c, rec)
	run := func() {
		t.Helper()
		if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: rs.Name}}); err != nil {
			t.Fatal(err)
		}
	}

	run()
	conv, err := ConvertTemplate(tpl)
	if err != nil {
		t.Fatal(err)
	}
	want, err := conversion.WorkflowRuleSetToPolicy(rs, conv)
	if err != nil {
		t.Fatal(err)
	}
	policy := &v1alpha2.Policy{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: rs.Name}, policy); err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(want.Spec, policy.Spec); d != "" {
		t.Errorf("Policy spec (-want +got):\n%s", d)
	}
	if ref := metav1.GetControllerOf(policy); ref == nil || ref.UID != rs.UID || ref.Kind != "WorkflowRuleSet" {
		t.Errorf("Policy controller = %+v, want the rule set", ref)
	}
	if got := tasksOf(t, c); len(got) != 2 {
		t.Errorf("Tasks = %v, want the Template's two, which the Policy runs", got)
	}

	// The Template going away takes the Policy with it.
	if err := c.Delete(context.Background(), tpl); err != nil {
		t.Fatal(err)
	}
	run()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(policy), &v1alpha2.Policy{}); !apierrors.IsNotFound(err) {
		t.Errorf("Policy still exists (%v); want it deleted", err)
	}
	if reason, ok := annotationOf(t, c, &v1alpha1.WorkflowRuleSet{ObjectMeta: rs.ObjectMeta}); !ok || !strings.Contains(reason, "not found") {
		t.Errorf("annotation = %q, %v; want template not found", reason, ok)
	}
	if e := drain(rec); len(e) != 1 {
		t.Errorf("events = %v, want one", e)
	}
}

func TestRuleSetReconcilerNotTranslated(t *testing.T) {
	tests := map[string]struct {
		ruleSet    *v1alpha1.WorkflowRuleSet
		template   string
		wantReason string
	}{
		"worker is not the enrolling agent": {
			ruleSet:    ruleSet("enroll", "provision", "other"),
			template:   oneTask,
			wantReason: "not on the enrolling Agent",
		},
		"template does not translate": {
			ruleSet:    ruleSet("enroll", "provision", "device_1"),
			template:   structural,
			wantReason: "range",
		},
		"workflow namespace is another namespace": {
			ruleSet: func() *v1alpha1.WorkflowRuleSet {
				rs := ruleSet("enroll", "provision", "device_1")
				rs.Spec.Workflow.Namespace = "elsewhere"
				return rs
			}(),
			template:   oneTask,
			wantReason: "namespace",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// A Policy translated before must go.
			stale := &v1alpha2.Policy{ObjectMeta: metav1.ObjectMeta{
				Name: tt.ruleSet.Name, Namespace: ns,
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "tinkerbell.org/v1alpha1", Kind: "WorkflowRuleSet", Name: tt.ruleSet.Name, UID: tt.ruleSet.UID, Controller: ptr(true)}},
			}}
			c := newClient(t, template(tt.template), tt.ruleSet, stale)
			r := NewRuleSetReconciler(c, events.NewFakeRecorder(10))
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(tt.ruleSet)}); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(stale), &v1alpha2.Policy{}); !apierrors.IsNotFound(err) {
				t.Errorf("Policy still exists (%v); want it deleted", err)
			}
			if reason, ok := annotationOf(t, c, &v1alpha1.WorkflowRuleSet{ObjectMeta: tt.ruleSet.ObjectMeta}); !ok || !strings.Contains(reason, tt.wantReason) {
				t.Errorf("annotation = %q, %v; want it to contain %q", reason, ok, tt.wantReason)
			}
		})
	}
}

func TestRuleSetsUsing(t *testing.T) {
	c := newClient(t, ruleSet("a", "provision", "device_1"), ruleSet("b", "other", "device_1"), ruleSet("c", "provision", "device_1"))
	r := NewRuleSetReconciler(c, events.NewFakeRecorder(1))
	got := r.ruleSetsUsing(context.Background(), template(oneTask))
	want := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: ns, Name: "a"}},
		{NamespacedName: types.NamespacedName{Namespace: ns, Name: "c"}},
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

// TestStoredPolicySpec verifies a translated Policy compares equal to itself as the API server stores it,
// whatever the spacing and key order of the rule set's rules, so it is not updated on every reconcile.
func TestStoredPolicySpec(t *testing.T) {
	tpl := template(oneTask)
	rs := ruleSet("enroll", tpl.Name, "device_1")
	rs.Spec.Rules = []string{`{"networkInterfaces": {"mac": [{"wildcard": "52:54:*"}, {"prefix": "aa"}]}, "cpu": {"totalCores": [{"numeric": [">=", 8]}]}}`}
	c := newClient(t, tpl, rs)
	r := NewRuleSetReconciler(c, events.NewFakeRecorder(10))
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rs)}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	policy := &v1alpha2.Policy{}
	if err := c.Get(context.Background(), req.NamespacedName, policy); err != nil {
		t.Fatal(err)
	}
	stored, err := storedPolicySpec(policy.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(stored, policy.Spec); d != "" {
		t.Errorf("the created Policy is not in stored form (-stored +created):\n%s", d)
	}
	got := string(policy.Spec.Rules.WorkflowAutoCreation[0].Rules[0].NetworkInterfaces.Mac[0].Raw)
	if got != `{"wildcard":"52:54:*"}` {
		t.Errorf("pattern = %s, want it compacted", got)
	}
	rv := policy.ResourceVersion
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), req.NamespacedName, policy); err != nil {
		t.Fatal(err)
	}
	if policy.ResourceVersion != rv {
		t.Error("an unchanged Policy was updated")
	}
}
