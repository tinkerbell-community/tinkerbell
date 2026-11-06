package controller_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/crd"
	"github.com/tinkerbell/tinkerbell/rufio/internal/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// TestRufioWithoutDeprecatedCRDs runs the Rufio controllers against a
// kube-apiserver from which the CRDs of the bmc Machine and Task were removed:
// they start, poll the BMC of a Hardware whose connection was translated from a
// Machine, and run Jobs. It needs a kube-apiserver and etcd: set
// KUBEBUILDER_ASSETS.
func TestRufioWithoutDeprecatedCRDs(t *testing.T) {
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

	// Only v1alpha2 is read and written here, so the conversion webhook is never called.
	tb, err := crd.NewTinkerbell(crd.WithRestConfig(cfg), crd.WithDeprecatedCRDRemoval(),
		crd.WithConversionWebhook(crd.ConversionWebhook{URL: "https://127.0.0.1:1/convert"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range crd.DeprecatedCRDs {
		if _, err := tb.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{}); err == nil {
			t.Fatalf("CRD %s was not removed", name)
		}
	}

	bmcs := &fakeBMCs{
		byHost: map[string]*testProvider{"10.0.0.1": {Powerstate: "on", FollowPowerSet: true, PowerSetOK: true, BootdeviceOK: true}},
		conns:  map[string]int{},
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
	mgrErr := make(chan error, 1)
	go func() { mgrErr <- mgr.Start(ctx) }()

	c, err := client.New(cfg, client.Options{Scheme: newScheme()})
	if err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "bmc-auth"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("secret")},
	}
	// The Hardware still names the Machine its connection was translated from,
	// as when the CRD was removed by something else than Tinkerbell.
	hw := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "hw1", Annotations: map[string]string{conversion.AnnotationMachine: "default/m1"}},
		Spec: v1alpha2.HardwareSpec{BMC: &v1alpha2.BMC{Connection: &bmcv2.Connection{
			Host: "10.0.0.1", CredentialsRef: bmcv2.SimpleReference{Namespace: "default", Name: "bmc-auth"},
		}}},
	}
	job := &bmcv2.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "job1"},
		Spec: bmcv2.JobSpec{
			HardwareRef: &bmcv2.SimpleReference{Namespace: "default", Name: "hw1"},
			Operations:  []bmcv2.Operations{powerOp(bmcv2.PowerActionHardOff)},
		},
	}
	for _, o := range []client.Object{secret, hw, job} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	eventually(t, "the Hardware's BMC polled", func() (bool, string) {
		got := &v1alpha2.Hardware{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(hw), got); err != nil {
			return false, err.Error()
		}
		return bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeBMCContactable, bmcv2.ConditionStatusTrue), fmt.Sprint(conditionSummary(got.Status.Conditions))
	})
	eventually(t, "the Job to complete", func() (bool, string) {
		got := &bmcv2.Job{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(job), got); err != nil {
			return false, err.Error()
		}
		return bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeCompleted, bmcv2.ConditionStatusTrue), fmt.Sprint(conditionSummary(got.Status.Conditions))
	})
	if n := bmcs.sessions("hardware", "10.0.0.1"); n == 0 {
		t.Errorf("sessions = %v, want the Hardware controller polling 10.0.0.1", bmcs.conns)
	}
	select {
	case err := <-mgrErr:
		t.Fatalf("manager stopped: %v", err)
	default:
	}
}
