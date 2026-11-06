package webhttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	bmcv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	tinkv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	"github.com/tinkerbell/tinkerbell/crd"
	"github.com/tinkerbell/tinkerbell/pkg/conversionwebhook"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestV1Alpha1ObjectsThroughAPIServer creates objects through v1alpha1, as
// existing clients do, in an API server that stores v1alpha2 and converts with
// the conversion webhook, and checks that the UI, reading v1alpha2 with its own
// client, shows them as before. It needs a kube-apiserver and etcd: set
// KUBEBUILDER_ASSETS, e.g. KUBEBUILDER_ASSETS=$(setup-envtest use -p path).
func TestV1Alpha1ObjectsThroughAPIServer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	// The CRDs and the conversion webhook, as Tinkerbell installs and runs them.
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	certs := &conversionwebhook.Certs{Client: kc, Namespace: "default", SecretName: "tinkerbell-conversion-webhook", IPs: []net.IP{net.IPv4(127, 0, 0, 1)}}
	ca, err := certs.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h, err := conversionwebhook.NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	srv := &conversionwebhook.Server{Addr: "127.0.0.1:0", Handler: h, GetCertificate: certs.GetCertificate}
	l, err := srv.Listen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx, l) }()
	tb, err := crd.NewTinkerbell(crd.WithRestConfig(cfg), crd.WithConversionWebhook(crd.ConversionWebhook{
		URL:      fmt.Sprintf("https://%s%s", l.Addr().String(), conversionwebhook.Path),
		CABundle: ca,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}

	k, err := NewKubeClientFromRestConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// v1alpha1 objects, created with the UI client's scheme, which has both versions.
	hw := newTestHardwareV1Alpha1("hw1", "default", "52:54:00:00:00:01", "10.0.0.5")
	hw.Spec.Interfaces = append(hw.Spec.Interfaces, v1alpha1Interface("52:54:00:00:00:00", "10.0.0.4", 4))
	wf := newTestWorkflowV1Alpha1("wf1", "tpl1", "")
	wf.Spec.HardwareRef = "hw1"
	wf.Spec.Disabled = ptrTo(true)
	job := newTestBMCJobV1Alpha1("job1", "default", "m1", nil)
	job.Spec.Tasks = []bmcv1alpha1.Action{{PowerAction: ptrTo(bmcv1alpha1.PowerHardOff)}}
	for _, o := range []client.Object{hw, wf, job} {
		o.SetCreationTimestamp(metav1.Time{})
		if err := k.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	wf.Status = tinkv1alpha1.WorkflowStatus{
		State:        tinkv1alpha1.WorkflowStateRunning,
		AgentID:      "52:54:00:00:00:01",
		CurrentState: &tinkv1alpha1.CurrentState{AgentID: "52:54:00:00:00:01", TaskName: "install", ActionName: "stream"},
	}
	if err := k.Status().Update(ctx, wf); err != nil {
		t.Fatal(err)
	}

	render := func(path string, handler func(*gin.Context), params ...string) string {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		c.Set("kubeClient", k)
		for i := 0; i+1 < len(params); i += 2 {
			c.Params = append(c.Params, gin.Param{Key: params[i], Value: params[i+1]})
		}
		handler(c)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, w.Code)
		}
		return w.Body.String()
	}
	expect := func(page string, body string, want ...string) {
		t.Helper()
		for _, s := range want {
			if !strings.Contains(body, s) {
				t.Errorf("%s should contain %q", page, s)
			}
		}
	}

	// The first v1alpha1 interface is still the one listed, though it is not first in MAC order.
	hwList := render("/hardware", func(c *gin.Context) { HandleHardwareList(c, testLog) })
	expect("Hardware list", hwList, "hw1", "52:54:00:00:00:01", "10.0.0.5")
	if strings.Contains(hwList, "10.0.0.4") {
		t.Error("Hardware list should show the first v1alpha1 interface, not the first in MAC order")
	}
	expect("Workflow list", render("/workflows", func(c *gin.Context) { HandleWorkflowList(c, testLog) }), "wf1", `href="/templates/default/tpl1"`, "Disabled")
	expect("Workflow detail", render("/workflows/default/wf1", func(c *gin.Context) { HandleWorkflowDetail(c, testLog) }, "namespace", "default", "name", "wf1"),
		"v1alpha1 compatibility mode", `href="/hardware/default/hw1"`, "Running", "install", "stream")
	expect("Job list", render("/bmc/jobs", func(c *gin.Context) { HandleBMCJobList(c, testLog) }), `href="/bmc/machines/default/m1"`, "power off")

	// Enabling the Workflow patches v1alpha2; the v1alpha1 client still sees its spec.
	if _, err := k.EnableWorkflow(ctx, "default", "wf1"); err != nil {
		t.Fatal(err)
	}
	got := &tinkv1alpha1.Workflow{}
	if err := k.Get(ctx, client.ObjectKeyFromObject(wf), got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Disabled == nil || *got.Spec.Disabled || got.Spec.TemplateRef != "tpl1" || got.Spec.HardwareRef != "hw1" {
		t.Errorf("v1alpha1 Workflow after enabling: %+v", got.Spec)
	}
}
