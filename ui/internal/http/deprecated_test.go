package webhttp

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	bmcv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	corev1 "k8s.io/api/core/v1"
)

// The pages of the kinds that only exist in v1alpha1 say they are deprecated
// and link what they were translated to.

func TestDeprecatedListPages(t *testing.T) {
	data := "version: 0.1"
	kubeClient := newFakeKubeClient(
		newTestNamespace("default"),
		newTestTemplate("tpl", &data),
		newTestWorkflowRuleSet("rs", "default", "tpl", nil),
		newTestBMCMachine("bmc-1", "default", "10.0.0.5", bmcv1alpha1.On),
		newTestBMCTask("task-1", "default", bmcv1alpha1.PowerOn),
	)
	for _, tc := range []struct {
		path    string
		handler func(*gin.Context, logr.Logger)
		notice  string
	}{
		{"/templates", HandleTemplateList, templates.DeprecatedTemplate},
		{"/workflows/rulesets", HandleWorkflowRuleSetList, templates.DeprecatedWorkflowRuleSet},
		{"/bmc/machines", HandleBMCMachineList, templates.DeprecatedBMCMachine},
		{"/bmc/tasks", HandleBMCTaskList, templates.DeprecatedBMCTask},
	} {
		t.Run(tc.path, func(t *testing.T) {
			c, w := setupTestContext(tc.path, kubeClient)
			tc.handler(c, testLog)
			body := w.Body.String()
			if !strings.Contains(body, "Deprecated") || !strings.Contains(body, tc.notice[:40]) {
				t.Errorf("%s should show its deprecation notice", tc.path)
			}
		})
	}
}

func TestTemplateDetailTranslation(t *testing.T) {
	data := "version: 0.1"
	translated := newTestTemplate("ubuntu", &data)
	untranslated := newTestTemplate("legacy", &data)
	untranslated.Annotations = map[string]string{annotationTranslation: "control structures around tasks"}
	kubeClient := newFakeKubeClient(newTestNamespace("default"), translated, untranslated, translatedTask(t))

	c, w := setupTestContext("/templates/default/ubuntu", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "ubuntu"}}
	HandleTemplateDetail(c, testLog)
	body := w.Body.String()
	if !strings.Contains(body, "Translated Tasks") || !strings.Contains(body, `href="/tasks/default/ubuntu-os-installation"`) {
		t.Error("a translated Template should link its Tasks")
	}
	if !strings.Contains(body, templates.DeprecatedTemplate[:40]) {
		t.Error("the Template page should say Templates are deprecated")
	}

	c, w = setupTestContext("/templates/default/legacy", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "legacy"}}
	HandleTemplateDetail(c, testLog)
	body = w.Body.String()
	if !strings.Contains(body, "Not translated to Tasks") || !strings.Contains(body, "control structures around tasks") {
		t.Error("an untranslated Template should say why")
	}
}

func TestWorkflowRuleSetDetailPolicy(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTestWorkflowRuleSet("enroll", "default", "tpl", nil), newTestPolicy("enroll"))
	c, w := setupTestContext("/workflows/rulesets/default/enroll", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "enroll"}}

	HandleWorkflowRuleSetDetail(c, testLog)

	if !strings.Contains(w.Body.String(), `href="/policies/default/enroll"`) {
		t.Error("a rule set should link the Policy it was translated to")
	}
}

func TestBMCMachineDetailHardware(t *testing.T) {
	v1 := newTestHardwareV1Alpha1("node-1", "default", "aa:bb:cc:dd:ee:ff", "")
	v1.Spec.BMCRef = &corev1.TypedLocalObjectReference{Kind: "Machine", Name: "bmc-1"}
	kubeClient := newFakeKubeClient(
		newTestNamespace("default"),
		newTestBMCMachine("bmc-1", "default", "10.0.0.5", bmcv1alpha1.On),
		hardwareV1Alpha2(v1),
		newTestHardware("node-2", "default", "aa:bb:cc:dd:ee:01", ""),
	)
	c, w := setupTestContext("/bmc/machines/default/bmc-1", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "bmc-1"}}

	HandleBMCMachineDetail(c, testLog)

	body := w.Body.String()
	if !strings.Contains(body, "Used By Hardware") || !strings.Contains(body, `href="/hardware/default/node-1"`) || strings.Contains(body, "/hardware/default/node-2") {
		t.Error("a Machine should list the Hardware that refer to it, only those")
	}
}

func TestHandleDashboardDefaultsToV1Alpha2(t *testing.T) {
	c, w := setupTestContext("/", newFakeKubeClient(newTestNamespace("default")))

	HandleDashboard(c, testLog)

	body := w.Body.String()
	if !strings.Contains(body, `<option value="v1alpha2" selected`) || !strings.Contains(body, `href="/tasks"`) {
		t.Error("the CRD browser should show v1alpha2 by default, linking the Task pages")
	}
}
