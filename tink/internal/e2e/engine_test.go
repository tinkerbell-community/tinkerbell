// Package e2e_test runs the Workflow engine, the tink controller and tink server, against a real API
// server, with Workflows created and read through v1alpha1 the way Cluster API Provider Tinkerbell does.
package e2e_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/crd"
	"github.com/tinkerbell/tinkerbell/pkg/backend/kube"
	"github.com/tinkerbell/tinkerbell/pkg/conversionwebhook"
	"github.com/tinkerbell/tinkerbell/pkg/proto"
	"github.com/tinkerbell/tinkerbell/tink/controller"
	"github.com/tinkerbell/tinkerbell/tink/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

const ns = "default"

// translatable is a Template the controller translates to Tasks, so Workflows using it are rewritten onto
// them. The second is not: its structure depends on the template.
const (
	translatable = `version: "0.1"
name: provision
global_timeout: 600
tasks:
  - name: "os-installation"
    worker: "{{.device_1}}"
    volumes:
      - /dev:/dev
    actions:
      - name: "stream-image"
        image: quay.io/tinkerbell/actions/image2disk:latest
        timeout: 300
        environment:
          DEST_DISK: {{ index .Hardware.Disks 0 }}
          METADATA: "{{ .metadata_url }}/{{ .device_1 }}"
      - name: "reboot"
        image: quay.io/tinkerbell/actions/reboot:latest
        timeout: 90
        pid: host
        command: ["sh", "-c", "echo b > /proc/sysrq-trigger"]
`
	structural = `version: "0.1"
name: provision
global_timeout: 600
tasks:
  - name: "os-installation"
    worker: "{{.device_1}}"
    actions:
{{- range $i, $d := .Hardware.Disks }}
      - name: "wipe-{{ $i }}"
        image: quay.io/tinkerbell/actions/wipe:latest
        environment:
          DISK: {{ $d }}
{{- end }}
`
)

// TestV1Alpha1Workflow creates a Hardware, Template and Workflow through v1alpha1, as Cluster API Provider
// Tinkerbell does, runs the Workflow with an Agent through the tink server, and reads its status through
// v1alpha1 at each step. A Workflow whose Template translates is rewritten onto Tasks and runs natively;
// one whose Template does not runs in v1alpha1 compatibility mode. It needs a kube-apiserver and etcd:
// set KUBEBUILDER_ASSETS, e.g. KUBEBUILDER_ASSETS=$(setup-envtest use -p path). It runs once per test
// process (no -count above 1): controller-runtime allows a controller name only once per process.
func TestV1Alpha1Workflow(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	// The components run in goroutines that may log after the test ends, which testr does not allow.
	ctrllog.SetLogger(logr.Discard())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := startAPIServer(ctx, t)
	agent := startEngine(ctx, t, cfg)

	scheme, err := conversionwebhook.Scheme()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		template    string
		wantActions []string
		wantNative  bool
	}{
		"rewritten onto Tasks":        {template: translatable, wantActions: []string{"stream-image", "reboot"}, wantNative: true},
		"v1alpha1 compatibility mode": {template: structural, wantActions: []string{"wipe-0"}},
	}
	i := 0
	for name, tt := range tests {
		i++
		t.Run(name, func(t *testing.T) {
			hwName, wfName := fmt.Sprintf("hw-%d", i), fmt.Sprintf("machine-%d", i)
			agentID := fmt.Sprintf("52:54:00:00:00:%02x", i)
			createV1Alpha1(ctx, t, c, hwName, wfName, agentID, tt.template)

			// The controller renders the Workflow, allows netboot, and waits for the Agent.
			wf := waitV1Alpha1(ctx, t, c, wfName, func(wf *v1alpha1.Workflow) bool { return wf.Status.State == v1alpha1.WorkflowStatePending })
			if wf.Status.AgentID != agentID || wf.Status.TemplateRendering != v1alpha1.TemplateRenderingSuccessful || len(wf.Status.Tasks) != 1 {
				t.Fatalf("pending workflow status = %+v", wf.Status)
			}
			if wf.Spec.TemplateRef != wfName || wf.Spec.HardwareRef != hwName || !wf.Spec.BootOptions.ToggleAllowNetboot {
				t.Errorf("spec = %+v, want the spec it was created with", wf.Spec)
			}
			if !allowPXE(ctx, t, c, hwName) {
				t.Error("netboot was not allowed for the Workflow")
			}
			stored := &v1alpha2.Workflow{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: wfName}, stored); err != nil {
				t.Fatal(err)
			}
			if native := stored.Spec.Tasks[0].TaskRef.Name != ""; native != tt.wantNative {
				t.Errorf("runs Tasks natively = %v, want %v", native, tt.wantNative)
			}

			// The Agent runs every action.
			var ran []string
			for {
				resp, err := agent.nextAction(ctx, agentID)
				if status.Code(err) == codes.NotFound || status.Code(err) == codes.FailedPrecondition {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				ran = append(ran, resp.GetName())
				agent.report(ctx, t, resp, proto.ActionStatusRequest_RUNNING)
				if wf := readV1Alpha1(ctx, t, c, wfName); wf.Status.State != v1alpha1.WorkflowStateRunning || wf.Status.CurrentState == nil || wf.Status.CurrentState.ActionName != resp.GetName() {
					t.Errorf("while %s runs: state %s, current state %+v", resp.GetName(), wf.Status.State, wf.Status.CurrentState)
				}
				agent.report(ctx, t, resp, proto.ActionStatusRequest_SUCCESS)
				if len(ran) > 5 {
					t.Fatal("too many actions")
				}
			}
			if fmt.Sprint(ran) != fmt.Sprint(tt.wantActions) {
				t.Errorf("ran %v, want %v", ran, tt.wantActions)
			}

			// The controller disallows netboot, and the Workflow succeeds.
			wf = waitV1Alpha1(ctx, t, c, wfName, func(wf *v1alpha1.Workflow) bool { return wf.Status.State == v1alpha1.WorkflowStateSuccess })
			for _, a := range wf.Status.Tasks[0].Actions {
				if a.State != v1alpha1.WorkflowStateSuccess {
					t.Errorf("action %s state %s", a.Name, a.State)
				}
			}
			if allowPXE(ctx, t, c, hwName) {
				t.Error("netboot was not disallowed after the Workflow")
			}
		})
	}
}

func startAPIServer(ctx context.Context, t *testing.T) *rest.Config {
	t.Helper()
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	// The conversion webhook and the CRDs, as Tinkerbell installs them.
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	certs := &conversionwebhook.Certs{Client: kc, Namespace: ns, SecretName: "tinkerbell-conversion-webhook", IPs: []net.IP{net.IPv4(127, 0, 0, 1)}}
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
	return cfg
}

type testAgent struct {
	client proto.WorkflowServiceClient
}

// startEngine starts the tink controller and tink server against the API server, and returns an Agent
// connected to the server.
func startEngine(ctx context.Context, t *testing.T, cfg *rest.Config) *testAgent {
	t.Helper()
	indexes := map[kube.IndexType]kube.Index{}
	for _, it := range []kube.IndexType{kube.IndexTypeWorkflowAgentID, kube.IndexTypeHardwareAgentID} {
		indexes[it] = kube.Indexes[it]
		for _, counterpart := range kube.V1Alpha2Counterparts(it) {
			indexes[counterpart] = kube.Indexes[counterpart]
		}
	}
	b, err := kube.NewBackend(kube.Backend{ClientConfig: cfg, Indexes: indexes})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = b.Start(ctx) }()

	tc := controller.NewConfig(
		controller.WithClient(cfg),
		controller.WithReferenceResolver(b),
		controller.WithEnableLeaderElection(false),
		controller.WithMetadataURL("http://192.0.2.10:7080"),
	)
	go func() {
		if err := tc.Start(ctx, logr.Discard()); err != nil && ctx.Err() == nil {
			t.Errorf("tink controller: %v", err)
		}
	}()

	port := freePort(t)
	ts := server.NewConfig(server.WithBindAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)))
	ts.SetBackends(b)
	go func() {
		if err := ts.Start(ctx, logr.Discard()); err != nil && ctx.Err() == nil {
			t.Errorf("tink server: %v", err)
		}
	}()

	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &testAgent{client: proto.NewWorkflowServiceClient(conn)}
}

// nextAction asks for the Agent's next action, retrying while the server or the cache is not ready
// and the Workflow is not yet in a state to serve one.
func (a *testAgent) nextAction(ctx context.Context, agentID string) (*proto.ActionResponse, error) {
	var lastErr error
	for range 100 {
		resp, err := a.client.GetAction(ctx, &proto.ActionRequest{AgentId: &agentID})
		if code := status.Code(err); code == codes.Unavailable || code == codes.Aborted || code == codes.Internal {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return resp, err
	}
	return nil, lastErr
}

func (a *testAgent) report(ctx context.Context, t *testing.T, action *proto.ActionResponse, state proto.ActionStatusRequest_StateType) {
	t.Helper()
	now := timestamppb.Now()
	req := &proto.ActionStatusRequest{
		WorkflowId: action.WorkflowId, TaskId: action.TaskId, AgentId: action.AgentId, ActionId: action.ActionId,
		ActionName: action.Name, ActionState: &state, ExecutionStart: now, ExecutionStop: now, ExecutionDuration: ptr("1s"),
		Message: &proto.ActionMessage{Message: ptr("")},
	}
	for range 50 {
		_, err := a.client.ReportActionStatus(ctx, req)
		if status.Code(err) == codes.Aborted {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatalf("report %s %s: %v", action.GetName(), state, err)
		}
		return
	}
	t.Fatalf("report %s %s kept conflicting", action.GetName(), state)
}

func createV1Alpha1(ctx context.Context, t *testing.T, c client.Client, hwName, wfName, agentID, template string) {
	t.Helper()
	objs := []client.Object{
		&v1alpha1.Hardware{
			ObjectMeta: metav1.ObjectMeta{Name: hwName, Namespace: ns},
			Spec: v1alpha1.HardwareSpec{
				Disks:    []v1alpha1.Disk{{Device: "/dev/sda"}},
				Metadata: &v1alpha1.HardwareMetadata{Instance: &v1alpha1.MetadataInstance{ID: agentID}},
				Interfaces: []v1alpha1.Interface{{
					DHCP:    &v1alpha1.DHCP{MAC: agentID, IP: &v1alpha1.IP{Address: "10.0.0.5", Netmask: "255.255.255.0"}},
					Netboot: &v1alpha1.Netboot{AllowPXE: ptr(false), AllowWorkflow: ptr(true)},
				}},
			},
		},
		&v1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: wfName, Namespace: ns}, Spec: v1alpha1.TemplateSpec{Data: &template}},
		&v1alpha1.Workflow{
			ObjectMeta: metav1.ObjectMeta{Name: wfName, Namespace: ns},
			Spec: v1alpha1.WorkflowSpec{
				TemplateRef: wfName,
				HardwareRef: hwName,
				HardwareMap: map[string]string{"device_1": agentID},
				BootOptions: v1alpha1.BootOptions{ToggleAllowNetboot: true},
			},
		},
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
}

func readV1Alpha1(ctx context.Context, t *testing.T, c client.Client, name string) *v1alpha1.Workflow {
	t.Helper()
	wf := &v1alpha1.Workflow{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, wf); err != nil {
		t.Fatal(err)
	}
	return wf
}

func waitV1Alpha1(ctx context.Context, t *testing.T, c client.Client, name string, done func(*v1alpha1.Workflow) bool) *v1alpha1.Workflow {
	t.Helper()
	var wf *v1alpha1.Workflow
	for range 300 {
		wf = readV1Alpha1(ctx, t, c, name)
		if done(wf) {
			return wf
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("workflow %s did not get there: %+v", name, wf.Status)
	return nil
}

func allowPXE(ctx context.Context, t *testing.T, c client.Client, name string) bool {
	t.Helper()
	hw := &v1alpha1.Hardware{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, hw); err != nil {
		t.Fatal(err)
	}
	nb := hw.Spec.Interfaces[0].Netboot
	return nb != nil && nb.AllowPXE != nil && *nb.AllowPXE
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v", l.Addr())
	}
	return uint16(addr.Port)
}

func ptr[T any](v T) *T { return &v }
