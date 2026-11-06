package controller_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	bmclib "github.com/bmc-toolbox/bmclib/v2"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	tinkerbell "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/crd"
	"github.com/tinkerbell/tinkerbell/pkg/conversionwebhook"
	"github.com/tinkerbell/tinkerbell/rufio/internal/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// controllerSink is a logr.LogSink that logs nothing and remembers the
// "controller" value controller-runtime adds to the logger of each controller,
// so a fake BMC can tell which controller connects to it.
type controllerSink struct{ controller string }

func (s controllerSink) Init(logr.RuntimeInfo)        {}
func (s controllerSink) Enabled(int) bool             { return false }
func (s controllerSink) Info(int, string, ...any)     {}
func (s controllerSink) Error(error, string, ...any)  {}
func (s controllerSink) WithName(string) logr.LogSink { return s }
func (s controllerSink) WithValues(kv ...any) logr.LogSink {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == "controller" {
			s.controller = fmt.Sprint(kv[i+1])
		}
	}
	return s
}

// fakeBMCs are BMCs by host, recording which controller connected to which.
type fakeBMCs struct {
	byHost map[string]*testProvider
	mu     sync.Mutex
	conns  map[string]int // "controller host" -> sessions
}

func (f *fakeBMCs) clientFunc() controller.ClientFunc {
	return func(ctx context.Context, log logr.Logger, host, username, password string, opts *controller.BMCOptions) (*bmclib.Client, error) {
		caller := "?"
		if s, ok := log.GetSink().(controllerSink); ok {
			caller = s.controller
		}
		f.mu.Lock()
		f.conns[caller+" "+host]++
		f.mu.Unlock()
		p, ok := f.byHost[host]
		if !ok {
			return nil, fmt.Errorf("no BMC at %s", host)
		}
		return newTestClient(p)(ctx, log, host, username, password, opts)
	}
}

func (f *fakeBMCs) sessions(controllerName, host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns[controllerName+" "+host]
}

func eventually(t *testing.T, what string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var ok bool
		if ok, last = cond(); ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %s", what, last)
}

// TestRufioAgainstAPIServer runs the Rufio controllers against a kube-apiserver
// with the Tinkerbell CRDs and conversion webhook, as Tinkerbell installs them,
// with v1alpha1 objects as existing clients create them and v1alpha2 objects
// next to them. It needs a kube-apiserver and etcd: set KUBEBUILDER_ASSETS,
// e.g. KUBEBUILDER_ASSETS=$(setup-envtest use -p path).
func TestRufioAgainstAPIServer(t *testing.T) {
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

	// The CRDs and the conversion webhook, as Tinkerbell runs them.
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

	// Rufio, with fake BMCs: the Machine's, and the one of a v1alpha2 Hardware.
	bmcs := &fakeBMCs{
		byHost: map[string]*testProvider{
			"10.0.0.1": {Powerstate: "on", FollowPowerSet: true, PowerSetOK: true, BootdeviceOK: true},
			"10.0.0.2": {Powerstate: "off", FollowPowerSet: true, PowerSetOK: true, BootdeviceOK: true},
		},
		conns: map[string]int{},
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 controller.DefaultScheme(),
		Logger:                 logr.New(controllerSink{}),
		Metrics:                server.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := ctrlcontroller.Options{MaxConcurrentReconciles: 1, SkipNameValidation: toPtr(true)}
	if err := controller.NewReconciler(mgr.GetClient()).SetupWithManager(ctx, mgr, bmcs.clientFunc(), time.Hour, 0, true, opts); err != nil {
		t.Fatal(err)
	}
	go func() { _ = mgr.Start(ctx) }()

	scheme := newScheme()
	if err := tinkerbell.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	// v1alpha1 objects, as existing clients create them.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "bmc-auth"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("secret")},
	}
	machine := &bmc.Machine{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "m1"},
		Spec: bmc.MachineSpec{Connection: bmc.Connection{
			Host: "10.0.0.1", Port: 623, AuthSecretRef: corev1.SecretReference{Namespace: "default", Name: "bmc-auth"},
		}},
	}
	hw1 := &tinkerbell.Hardware{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "hw1"},
		Spec: tinkerbell.HardwareSpec{
			BMCRef: &corev1.TypedLocalObjectReference{APIGroup: toPtr("bmc.tinkerbell.org"), Kind: "Machine", Name: "m1"},
			Interfaces: []tinkerbell.Interface{{DHCP: &tinkerbell.DHCP{
				MAC: "52:54:00:00:00:01", Hostname: "hw1", Arch: "x86_64",
				IP: &tinkerbell.IP{Address: "10.0.1.5", Netmask: "255.255.255.0", Family: 4},
			}}},
		},
	}
	// A v1alpha2 Hardware with a BMC connection of its own.
	hw2 := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "hw2"},
		Spec: v1alpha2.HardwareSpec{BMC: &v1alpha2.BMC{Connection: &bmcv2.Connection{
			Host: "10.0.0.2", CredentialsRef: bmcv2.SimpleReference{Namespace: "default", Name: "bmc-auth"},
		}}},
	}
	for _, o := range []client.Object{secret, machine, hw1, hw2} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	created := hw1.Spec.DeepCopy()

	// The Machine is translated into the Hardware that refers to it, and the
	// Hardware shows the Machine's state.
	eventually(t, "hw1 translated and showing the Machine's state", func() (bool, string) {
		got := &v1alpha2.Hardware{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(hw1), got); err != nil {
			return false, err.Error()
		}
		ok := got.Spec.BMC != nil && got.Spec.BMC.Connection != nil && got.Spec.BMC.Connection.Host == "10.0.0.1" &&
			got.Annotations[conversion.AnnotationMachine] == "default/m1" &&
			bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCContactable, bmcv2.ConditionStatusTrue) &&
			bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCPowerState, bmcv2.ConditionStatusOn)
		return ok, fmt.Sprintf("bmc %+v, annotations %v, conditions %v", got.Spec.BMC, got.Annotations, conditionSummary(got.Status.Conditions))
	})
	// v1alpha1 clients still read the Hardware they created.
	got1 := &tinkerbell.Hardware{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hw1), got1); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(*created, got1.Spec); diff != "" {
		t.Errorf("v1alpha1 Hardware spec after translation (-want +got):\n%s", diff)
	}

	// The v1alpha2 Hardware's own BMC is polled.
	eventually(t, "hw2 polled", func() (bool, string) {
		got := &v1alpha2.Hardware{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(hw2), got); err != nil {
			return false, err.Error()
		}
		return bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCContactable, bmcv2.ConditionStatusTrue) &&
			bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCPowerState, bmcv2.ConditionStatusOff), fmt.Sprint(conditionSummary(got.Status.Conditions))
	})

	// A Job created through v1alpha1 runs against its Machine and reads back,
	// through v1alpha1, as completed. No bmc Tasks are created for it.
	job1 := &bmc.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "job1"},
		Spec: bmc.JobSpec{
			MachineRef: bmc.MachineRef{Namespace: "default", Name: "m1"},
			Tasks:      []bmc.Action{{PowerAction: toPtr(bmc.PowerHardOff)}, {BootDevice: &bmc.BootDeviceConfig{Device: bmc.PXE}}, {PowerAction: toPtr(bmc.PowerOn)}},
		},
	}
	if err := c.Create(ctx, job1); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the v1alpha1 Job to complete", func() (bool, string) {
		got := &bmc.Job{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(job1), got); err != nil {
			return false, err.Error()
		}
		return got.HasCondition(bmc.JobCompleted, bmc.ConditionTrue) && got.HasCondition(bmc.JobRunning, bmc.ConditionTrue) && got.Status.CompletionTime != nil,
			fmt.Sprintf("%+v", got.Status)
	})
	got2 := &bmcv2.Job{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(job1), got2); err != nil {
		t.Fatal(err)
	}
	if got2.Status.CompletedOperations != 3 {
		t.Errorf("completedOperations = %d, want 3", got2.Status.CompletedOperations)
	}
	tasks := &bmc.TaskList{}
	if err := c.List(ctx, tasks, client.InNamespace("default")); err != nil {
		t.Fatal(err)
	}
	if len(tasks.Items) != 0 {
		t.Errorf("%d bmc Tasks created, want none", len(tasks.Items))
	}
	calls, _ := bmcs.byHost["10.0.0.1"].recorded()
	if !containsInOrder(calls, "PowerSet(off)", "BootDeviceSet(pxe, persistent=false, efi=false)", "PowerSet(on)") {
		t.Errorf("BMC calls on the Machine's BMC = %v, want the Job's operations in order", calls)
	}

	// A v1alpha2 Job refers to the Hardware.
	job2 := &bmcv2.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "job2"},
		Spec: bmcv2.JobSpec{
			HardwareRef: &bmcv2.SimpleReference{Namespace: "default", Name: "hw2"},
			Operations:  []bmcv2.Operations{powerOp(bmcv2.PowerActionOn)},
		},
	}
	if err := c.Create(ctx, job2); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the v1alpha2 Job to complete", func() (bool, string) {
		got := &bmcv2.Job{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(job2), got); err != nil {
			return false, err.Error()
		}
		return bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeCompleted, bmcv2.ConditionStatusTrue), fmt.Sprint(conditionSummary(got.Status.Conditions))
	})
	if calls, _ := bmcs.byHost["10.0.0.2"].recorded(); !containsInOrder(calls, "PowerSet(on)") {
		t.Errorf("BMC calls on the Hardware's BMC = %v, want the Job's power action", calls)
	}

	m1Calls, _ := bmcs.byHost["10.0.0.1"].recorded()
	hw2Calls, _ := bmcs.byHost["10.0.0.2"].recorded()
	t.Logf("BMC sessions by controller and host: %v; calls on 10.0.0.1: %v; calls on 10.0.0.2: %v", bmcs.conns, m1Calls, hw2Calls)

	// No BMC is polled twice: the Machine controller polls the Machine's BMC,
	// the Hardware controller only the BMC of the Hardware with its own
	// connection, never the one translated from the Machine.
	if n := bmcs.sessions("hardware", "10.0.0.1"); n != 0 {
		t.Errorf("the Hardware controller opened %d sessions to the BMC translated from the Machine, want 0", n)
	}
	if bmcs.sessions("machine", "10.0.0.1") == 0 || bmcs.sessions("hardware", "10.0.0.2") == 0 {
		t.Errorf("sessions = %v, want the Machine controller polling 10.0.0.1 and the Hardware controller 10.0.0.2", bmcs.conns)
	}
	if n := bmcs.sessions("?", "10.0.0.1") + bmcs.sessions("?", "10.0.0.2"); n != 0 {
		t.Errorf("%d sessions from an unknown caller: the controller name was not found in the logger", n)
	}
}

// containsInOrder reports whether want appear in calls in this order, not
// necessarily next to each other.
func containsInOrder(calls []string, want ...string) bool {
	i := 0
	for _, c := range calls {
		if i < len(want) && c == want[i] {
			i++
		}
	}
	return i == len(want)
}
