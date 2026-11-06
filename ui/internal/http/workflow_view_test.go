package webhttp

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	tinkv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The legacy* functions are the web UI's v1alpha1 Workflow code as it was
// before the UI read v1alpha2. They are the reference the v1alpha2 code must
// match for Workflows created through v1alpha1.

func legacyWorkflowRow(wf tinkv1alpha1.Workflow) templates.Workflow {
	task := ""
	action := ""
	agent := ""
	if wf.Status.CurrentState != nil {
		task = wf.Status.CurrentState.TaskName
		action = wf.Status.CurrentState.ActionName
		agent = wf.Status.CurrentState.AgentID
	}
	return templates.Workflow{
		Name:        wf.Name,
		Namespace:   wf.Namespace,
		TemplateRef: wf.Spec.TemplateRef,
		State:       string(wf.Status.State),
		Disabled:    wf.Spec.Disabled != nil && *wf.Spec.Disabled,
		Task:        task,
		Action:      action,
		Agent:       agent,
		CreatedAt:   wf.GetCreationTimestamp().Format("2006-01-02 15:04:05"),
	}
}

// legacyWorkflowDetail returns what the v1alpha1 detail page showed beyond the
// list row: the Hardware it linked and the template rendering.
func legacyWorkflowDetail(wf tinkv1alpha1.Workflow) (hardwareLink, templateRendering string) {
	if wf.Spec.HardwareRef != "" {
		hardwareLink = wf.Namespace + "/" + wf.Spec.HardwareRef
	}
	return hardwareLink, string(wf.Status.TemplateRendering)
}

// v1alpha1WorkflowFixtures are Workflows as v1alpha1 clients create them and
// the v1alpha1 Workflow engine updates them.
func v1alpha1WorkflowFixtures() map[string]*tinkv1alpha1.Workflow {
	disabled := true
	pending := newTestWorkflowV1Alpha1("pending", "ubuntu", tinkv1alpha1.WorkflowStatePending)
	pending.Spec.HardwareRef = "hw-1"
	pending.Spec.HardwareMap = map[string]string{"device_1": "52:54:00:00:00:01"}

	running := newTestWorkflowV1Alpha1("running", "ubuntu", tinkv1alpha1.WorkflowStateRunning)
	running.Spec.HardwareRef = "hw-1"
	running.Status.AgentID = "52:54:00:00:00:01"
	running.Status.CurrentState = &tinkv1alpha1.CurrentState{
		AgentID: "52:54:00:00:00:01", TaskName: "os-installation", ActionName: "stream-image", State: tinkv1alpha1.WorkflowStateRunning,
	}
	running.Status.TemplateRendering = tinkv1alpha1.TemplateRenderingSuccessful
	running.Status.Conditions = []tinkv1alpha1.WorkflowCondition{{Type: tinkv1alpha1.TemplateRenderedSuccess, Status: metav1.ConditionTrue}}

	succeeded := newTestWorkflowV1Alpha1("succeeded", "ubuntu", tinkv1alpha1.WorkflowStateSuccess)
	succeeded.Spec.Disabled = &disabled

	failed := newTestWorkflowV1Alpha1("failed", "broken", tinkv1alpha1.WorkflowStateFailed)
	failed.Status.TemplateRendering = tinkv1alpha1.TemplateRenderingFailed
	failed.Status.Conditions = []tinkv1alpha1.WorkflowCondition{{Type: tinkv1alpha1.TemplateRenderedSuccess, Status: metav1.ConditionFalse, Message: "bad template"}}

	return map[string]*tinkv1alpha1.Workflow{
		"not started": newTestWorkflowV1Alpha1("new", "ubuntu", ""),
		"pending":     pending,
		"running":     running,
		"succeeded":   succeeded,
		"failed":      failed,
		"timed out":   newTestWorkflowV1Alpha1("timeout", "ubuntu", tinkv1alpha1.WorkflowStateTimeout),
	}
}

func TestWorkflowRowV1Alpha1Parity(t *testing.T) {
	for name, v1 := range v1alpha1WorkflowFixtures() {
		t.Run(name, func(t *testing.T) {
			want := legacyWorkflowRow(*v1)
			// v1alpha2 names states rather than upper-casing them: SUCCESS is
			// Success.
			if want.State != "" {
				want.State = conversion.StateToV1Alpha2(tinkv1alpha1.WorkflowState(want.State)).String()
			}
			got := toWorkflowRow(*workflowV1Alpha2(v1))
			if got.Tasks != nil {
				t.Errorf("Tasks = %v, want none: the Workflow runs its Template", got.Tasks)
			}
			got.Tasks = nil
			if !reflect.DeepEqual(got, want) {
				t.Errorf("row = %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestWorkflowDetailV1Alpha1Parity(t *testing.T) {
	for name, v1 := range v1alpha1WorkflowFixtures() {
		t.Run(name, func(t *testing.T) {
			hardware, rendering := legacyWorkflowDetail(*v1)
			d := workflowDetail(workflowV1Alpha2(v1))
			if d.TemplateRef != v1.Spec.TemplateRef || !d.CompatibilityMode {
				t.Errorf("TemplateRef = %q, CompatibilityMode = %v; want %q in compatibility mode", d.TemplateRef, d.CompatibilityMode, v1.Spec.TemplateRef)
			}
			if got := strings.Join(d.Hardware, ","); got != hardware {
				t.Errorf("Hardware = %q, want %q", got, hardware)
			}
			if d.TemplateRendering != rendering {
				t.Errorf("TemplateRendering = %q, want %q", d.TemplateRendering, rendering)
			}
		})
	}
	// The v1alpha1 hardwareMap is the v1alpha2 template map.
	d := workflowDetail(workflowV1Alpha2(v1alpha1WorkflowFixtures()["pending"]))
	if got := rowValue(d.Details, "Template Map"); got != "device_1=52:54:00:00:00:01" {
		t.Errorf("Template Map = %q, want the hardwareMap", got)
	}
}

// A Workflow created through v1alpha1 that the engine rewrote onto its
// Template's translated Tasks runs those Tasks, not the Template.
func TestWorkflowRewrittenOntoTasks(t *testing.T) {
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
`
	tpl := &tinkv1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "ubuntu", Namespace: "default"}, Spec: tinkv1alpha1.TemplateSpec{Data: &data}}
	c, err := conversion.ConvertTemplate(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	wf := workflowV1Alpha2(v1alpha1WorkflowFixtures()["not started"])
	wf.Spec.Globals = &tinkv1alpha2.Extra{TemplateMap: map[string]string{"device_1": "52:54:00:00:00:01"}}
	if err := conversion.RewriteWorkflowToTasks(wf, tpl, c); err != nil {
		t.Fatal(err)
	}

	row := toWorkflowRow(*wf)
	if len(row.Tasks) != 1 || row.Tasks[0].Name != "ubuntu-os-installation" || row.Tasks[0].Namespace != "default" {
		t.Errorf("Tasks = %+v, want the translated Task", row.Tasks)
	}
	d := workflowDetail(wf)
	if d.CompatibilityMode || d.TemplateRef != "ubuntu" {
		t.Errorf("CompatibilityMode = %v, TemplateRef = %q; want the Template named but not run", d.CompatibilityMode, d.TemplateRef)
	}
	if len(d.Tasks) != 1 || d.Tasks[0].AgentID != "52:54:00:00:00:01" {
		t.Errorf("Tasks = %+v, want the Task on the hardwareMap Agent", d.Tasks)
	}
}

func newTaskWorkflow() *tinkv1alpha2.Workflow {
	timeout := int64(600)
	start := metav1.Now()
	return &tinkv1alpha2.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "provision", Namespace: "default", CreationTimestamp: metav1.Now()},
		Spec: tinkv1alpha2.WorkflowSpec{
			TimeoutSeconds: &timeout,
			Tasks: []tinkv1alpha2.WorkflowTask{
				{
					AgentID: "52:54:00:00:00:02",
					TaskRef: tinkv1alpha2.SimpleReference{Name: "install", Namespace: "default"},
					Hardware: &tinkv1alpha2.WorkflowHardware{
						HardwareRef: &tinkv1alpha2.SimpleReference{Name: "hw-v2", Namespace: "default"},
						BootOptions: &tinkv1alpha2.BootOptions{BootMode: tinkv1alpha2.BootModeNetboot, ToggleNetboot: true},
					},
				},
				{AgentID: "52:54:00:00:00:02", TaskRef: tinkv1alpha2.SimpleReference{Name: "reboot"}},
			},
		},
		Status: tinkv1alpha2.WorkflowStatus{
			Metadata: tinkv1alpha2.WorkflowMetadata{
				Workflow: tinkv1alpha2.Metadata{State: tinkv1alpha2.StateRunning},
				Task:     tinkv1alpha2.Metadata{Name: "install", AgentID: "52:54:00:00:00:02"},
				Action:   tinkv1alpha2.Metadata{Name: "stream", State: tinkv1alpha2.StateRunning},
			},
			RenderedTasks: []tinkv1alpha2.TaskWithMetadata{{
				Name:     "install",
				Metadata: tinkv1alpha2.Metadata{AgentID: "52:54:00:00:00:02", State: tinkv1alpha2.StateRunning},
				Actions: []tinkv1alpha2.ActionWithMetadata{
					{Action: tinkv1alpha2.Action{Name: "wipe", Image: "alpine"}, Metadata: tinkv1alpha2.Metadata{State: tinkv1alpha2.StateSuccess, ExecutionDuration: "3s", StartTime: &start}},
					{Action: tinkv1alpha2.Action{Name: "stream", Image: "image2disk"}, Metadata: tinkv1alpha2.Metadata{State: tinkv1alpha2.StateRunning}},
				},
			}},
			Conditions: []tinkv1alpha2.WorkflowCondition{{Type: tinkv1alpha2.TemplateRenderedSuccess, Status: metav1.ConditionTrue, Reason: "Rendered"}},
		},
	}
}

func TestWorkflowDetailV1Alpha2(t *testing.T) {
	wf := newTaskWorkflow()
	row := toWorkflowRow(*wf)
	if row.State != "Running" || row.Task != "install" || row.Action != "stream" || row.Agent != "52:54:00:00:00:02" || row.TemplateRef != "" {
		t.Errorf("row = %+v", row)
	}
	if len(row.Tasks) != 2 || row.Tasks[1].Namespace != "default" {
		t.Errorf("Tasks = %+v, want both, defaulting the namespace to the Workflow's", row.Tasks)
	}

	d := workflowDetail(wf)
	if d.CompatibilityMode {
		t.Error("a v1alpha2 Workflow runs Tasks")
	}
	if len(d.Hardware) != 1 || d.Hardware[0] != "default/hw-v2" {
		t.Errorf("Hardware = %v", d.Hardware)
	}
	if d.Tasks[0].BootOptions != "netboot, toggles netboot" || d.Tasks[0].Hardware != "default/hw-v2" {
		t.Errorf("first task = %+v", d.Tasks[0])
	}
	if got := rowValue(d.Details, "Timeout"); got != "600s" {
		t.Errorf("Timeout = %q", got)
	}
	if len(d.RenderedTasks) != 1 || len(d.RenderedTasks[0].Actions) != 2 {
		t.Fatalf("RenderedTasks = %+v", d.RenderedTasks)
	}
	if a := d.RenderedTasks[0].Actions[0]; a.State != "Success" || a.Duration != "3s" || a.Started == "" {
		t.Errorf("first action = %+v", a)
	}
	if d.TemplateRendering != "successful" || len(d.Conditions) != 1 || d.Conditions[0].Reason != "Rendered" {
		t.Errorf("rendering = %q, conditions = %+v", d.TemplateRendering, d.Conditions)
	}
}

func TestHandleWorkflowDetailV1Alpha2(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTaskWorkflow())
	c, w := setupTestContext("/workflows/default/provision", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "provision"}}

	HandleWorkflowDetail(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{"Rendered Tasks", "image2disk", `href="/tasks/default/install"`, `href="/hardware/default/hw-v2"`, "TemplateRenderedSuccess"} {
		if !strings.Contains(body, want) {
			t.Errorf("response should contain %q", want)
		}
	}
	if strings.Contains(body, "compatibility mode") {
		t.Error("a v1alpha2 Workflow is not in compatibility mode")
	}
}

func TestHandleWorkflowDetailCompatibilityMode(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), workflowV1Alpha2(v1alpha1WorkflowFixtures()["running"]))
	c, w := setupTestContext("/workflows/default/running", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "running"}}

	HandleWorkflowDetail(c, testLog)

	body := w.Body.String()
	for _, want := range []string{"v1alpha1 compatibility mode", `href="/templates/default/ubuntu"`, `href="/hardware/default/hw-1"`, "os-installation", "stream-image"} {
		if !strings.Contains(body, want) {
			t.Errorf("response should contain %q", want)
		}
	}
}
