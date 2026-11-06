package webhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	tinkv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestTask(name string, actions ...tinkv1alpha2.Action) *tinkv1alpha2.Task {
	return &tinkv1alpha2.Task{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", CreationTimestamp: metav1.Now()},
		Spec:       tinkv1alpha2.TaskSpec{Actions: actions},
	}
}

// translatedTask returns the Task a v1alpha1 Template with one task translates to.
func translatedTask(t *testing.T) *tinkv1alpha2.Task {
	t.Helper()
	data := `
version: "0.1"
name: ubuntu
global_timeout: 600
tasks:
  - name: "os-installation"
    worker: "{{.device_1}}"
    actions:
      - name: "stream-image"
        image: quay.io/tinkerbell/actions/image2disk:latest
        timeout: 300
        environment:
          DEST_DISK: /dev/sda
`
	tpl := &tinkv1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "ubuntu", Namespace: "default"},
		Spec:       tinkv1alpha1.TemplateSpec{Data: &data},
	}
	c, err := conversion.ConvertTemplate(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := conversion.TemplateTasks(tpl, c)[0]
	task.CreationTimestamp = metav1.Now()
	return &task
}

func TestHandleTaskList(t *testing.T) {
	kubeClient := newFakeKubeClient(
		newTestNamespace("default"),
		newTestTask("plain", tinkv1alpha2.Action{Name: "a", Image: "alpine"}),
		translatedTask(t),
	)
	c, w := setupTestContext("/tasks", kubeClient)

	HandleTaskList(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{"<!doctype html>", "plain", "ubuntu-os-installation", `href="/templates/default/ubuntu"`} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("response should contain %q", want)
		}
	}
}

func TestHandleTaskData(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTestTask("plain"))
	c, w := setupTestContext("/tasks-data?namespace=default", kubeClient)

	HandleTaskData(c, testLog)

	body := w.Body.String()
	if !strings.Contains(body, "plain") || strings.Contains(strings.ToLower(body), "<!doctype html>") {
		t.Errorf("response should be the table alone, listing the Task: %s", body)
	}
}

func TestHandleTaskList_Empty(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"))
	c, w := setupTestContext("/tasks", kubeClient)

	HandleTaskList(c, testLog)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No Tasks found") {
		t.Errorf("status = %d; want the empty list", w.Code)
	}
}

func TestHandleTaskDetail(t *testing.T) {
	timeout := int64(300)
	task := newTestTask("install",
		tinkv1alpha2.Action{
			Name:           "stream",
			Image:          "quay.io/tinkerbell/actions/image2disk:latest",
			Command:        "/bin/sh",
			Args:           []string{"-c", "echo hi"},
			TimeoutSeconds: &timeout,
			Env:            map[string]string{"DEST_DISK": "/dev/sda"},
			Namespaces:     tinkv1alpha2.Namespaces{PID: "host"},
		},
	)
	task.Spec.Vars = map[string]string{"os": "ubuntu"}
	kubeClient := newFakeKubeClient(newTestNamespace("default"), task, translatedTask(t))

	c, w := setupTestContext("/tasks/default/install", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "install"}}
	HandleTaskDetail(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{"image2disk", "/bin/sh -c echo hi", "300s", "DEST_DISK=/dev/sda", "pid namespace: host", "ubuntu"} {
		if !strings.Contains(body, want) {
			t.Errorf("response should contain %q", want)
		}
	}

	c, w = setupTestContext("/tasks/default/ubuntu-os-installation", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "ubuntu-os-installation"}}
	HandleTaskDetail(c, testLog)

	body = w.Body.String()
	if !strings.Contains(body, "Translated From Template") || !strings.Contains(body, `href="/templates/default/ubuntu"`) || !strings.Contains(body, "os-installation") {
		t.Error("a translated Task should link to its Template and name the template task")
	}
}

func TestHandleTaskDetail_NotFound(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"))
	c, w := setupTestContext("/tasks/default/missing", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "missing"}}

	HandleTaskDetail(c, testLog)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func newTestPolicy(name string) *tinkv1alpha2.Policy {
	const namespace = "default"
	disabled := true
	return &tinkv1alpha2.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace, CreationTimestamp: metav1.Now(),
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "tinkerbell.org/v1alpha1", Kind: "WorkflowRuleSet", Name: name}},
		},
		Spec: tinkv1alpha2.PolicySpec{Rules: tinkv1alpha2.Rules{
			WorkflowAutoCreation: []tinkv1alpha2.WorkflowRule{{
				Rules: []tinkv1alpha2.AgentAttributes{{
					Chassis: &tinkv1alpha2.Chassis{Vendor: tinkv1alpha2.FieldPattern{tinkv1alpha2.PatternValue{Raw: json.RawMessage(`"Dell"`)}}},
				}},
				Config: tinkv1alpha2.WorkflowConfig{
					Disabled: &disabled,
					Tasks:    []tinkv1alpha2.WorkflowTaskConfig{{TaskRef: tinkv1alpha2.SimpleReference{Name: "install", Namespace: namespace}}},
				},
			}},
			ReferenceAccess: &tinkv1alpha2.ReferenceRules{},
		}},
	}
}

func TestHandlePolicyList(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTestPolicy("enroll"))
	c, w := setupTestContext("/policies", kubeClient)

	HandlePolicyList(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "enroll") || !strings.Contains(body, `href="/workflows/rulesets/default/enroll"`) {
		t.Error("response should list the Policy and link the rule set it was translated from")
	}

	c, w = setupTestContext("/policies-data", kubeClient)
	HandlePolicyData(c, testLog)
	if !strings.Contains(w.Body.String(), "enroll") {
		t.Error("data response should list the Policy")
	}
}

func TestHandlePolicyDetail(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTestPolicy("enroll"))
	c, w := setupTestContext("/policies/default/enroll", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "enroll"}}

	HandlePolicyDetail(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{"Workflow Auto-Creation Rule 1", "Dell", `href="/tasks/default/install"`, "Reference Access", "Translated From Rule Set"} {
		if !strings.Contains(body, want) {
			t.Errorf("response should contain %q", want)
		}
	}
}

func TestHandlePolicyDetail_NotFound(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"))
	c, w := setupTestContext("/policies/default/missing", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "missing"}}

	HandlePolicyDetail(c, testLog)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGlobalSearch_FindsTasksAndPolicies(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTestTask("install-os"), newTestPolicy("install-policy"))
	c, w := setupTestContext("/api/search?q=install", kubeClient)

	HandleGlobalSearch(c, testLog)

	var resp SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	urls := map[string]bool{}
	for _, r := range resp.Results {
		urls[r.URL] = true
	}
	if !urls["/tasks/default/install-os"] || !urls["/policies/default/install-policy"] {
		t.Errorf("results = %+v, want the Task and the Policy", resp.Results)
	}
}

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	for _, tc := range []struct {
		page, perPage int
		want          []int
		start, end    int
		current       int
	}{
		{page: 1, perPage: 2, want: []int{1, 2}, start: 1, end: 2, current: 1},
		{page: 3, perPage: 2, want: []int{5}, start: 5, end: 5, current: 3},
		{page: 9, perPage: 2, want: []int{5}, start: 5, end: 5, current: 3},
		{page: 0, perPage: 10, want: items, start: 1, end: 5, current: 1},
	} {
		got, p := paginate(items, tc.page, tc.perPage, "/x", "#x")
		if len(got) != len(tc.want) || got[0] != tc.want[0] || p.StartItem != tc.start || p.EndItem != tc.end || p.CurrentPage != tc.current {
			t.Errorf("paginate(page %d, %d per page) = %v, %+v", tc.page, tc.perPage, got, p)
		}
	}
	if got, p := paginate([]int(nil), 1, 10, "/x", "#x"); len(got) != 0 || p.TotalPages != 1 || p.StartItem != 0 {
		t.Errorf("paginate(nil) = %v, %+v", got, p)
	}
}
