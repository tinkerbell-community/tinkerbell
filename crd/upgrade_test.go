package crd

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	apiconversion "github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/conversionwebhook"
	apiv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	sigsyaml "sigs.k8s.io/yaml"
)

// v1alpha1CRDs returns the CRDs as Tinkerbell installed them before v1alpha2:
// v1alpha1 only, stored as v1alpha1.
func v1alpha1CRDs(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for name, raw := range TinkerbellDefaults {
		var c apiv1.CustomResourceDefinition
		if err := sigsyaml.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		var keep []apiv1.CustomResourceDefinitionVersion
		for _, v := range c.Spec.Versions {
			if v.Name == "v1alpha1" {
				v.Storage, v.Deprecated, v.DeprecationWarning = true, false, nil
				keep = append(keep, v)
			}
		}
		if len(keep) == 0 {
			continue // v1alpha2-only kind, did not exist yet
		}
		c.Spec.Versions = keep
		b, err := sigsyaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = b
	}
	return out
}

// TestUpgradeFromV1Alpha1 installs the v1alpha1 CRDs with objects in them, as an
// existing cluster has, then runs the startup CRD migration with the conversion
// webhook and checks that everything is stored as v1alpha2 and still reads the
// same in v1alpha1. It needs a kube-apiserver and etcd: set KUBEBUILDER_ASSETS,
// e.g. KUBEBUILDER_ASSETS=$(setup-envtest use -p path).
func TestUpgradeFromV1Alpha1(t *testing.T) {
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

	old, err := NewTinkerbell(WithRestConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	old.CRDs = v1alpha1CRDs(t)
	if err := old.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := old.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	scheme, err := conversionwebhook.Scheme()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	// Objects as an existing cluster has them.
	hw := &v1alpha1.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "hw1", Namespace: "default"},
		Spec: v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{
				DHCP: &v1alpha1.DHCP{
					MAC: "52:54:00:00:00:01", Hostname: "hw1", Arch: "x86_64", IfaceName: "eno1",
					IP: &v1alpha1.IP{Address: "10.0.0.5", Netmask: "255.255.255.0", Gateway: "10.0.0.1", Family: 4},
				},
				Netboot: &v1alpha1.Netboot{AllowPXE: ptrTo(true), AllowWorkflow: ptrTo(true)},
			}},
			Metadata: &v1alpha1.HardwareMetadata{Facility: &v1alpha1.MetadataFacility{FacilityCode: "onprem"}},
			Disks:    []v1alpha1.Disk{{Device: "/dev/sda"}},
			UserData: ptrTo("#cloud-config"),
		},
	}
	tpl := &v1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "tpl1", Namespace: "default"}, Spec: v1alpha1.TemplateSpec{Data: ptrTo("version: \"0.1\"")}}
	wf := &v1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf1", Namespace: "default"},
		Spec:       v1alpha1.WorkflowSpec{TemplateRef: "tpl1", HardwareRef: "hw1", HardwareMap: map[string]string{"device_1": "52:54:00:00:00:01"}},
	}
	job := &bmcv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "job1", Namespace: "default"},
		Spec:       bmcv1.JobSpec{MachineRef: bmcv1.MachineRef{Name: "m1", Namespace: "default"}, Tasks: []bmcv1.Action{{PowerAction: ptrTo(bmcv1.PowerHardOff)}}},
	}
	for _, o := range []client.Object{hw, tpl, wf, job} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	wf.Status = v1alpha1.WorkflowStatus{
		State: v1alpha1.WorkflowStateRunning, AgentID: "52:54:00:00:00:01", GlobalTimeout: 600,
		Tasks: []v1alpha1.Task{{ID: "t1", Name: "install", AgentID: "52:54:00:00:00:01", Actions: []v1alpha1.Action{
			{ID: "a1", Name: "stream", Image: "quay.io/x/image2disk", Command: []string{"sh", "-c", "true"}, State: v1alpha1.WorkflowStateSuccess, Message: "ok"},
		}}},
	}
	if err := c.Status().Update(ctx, wf); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, o := range []client.Object{hw, wf, job} {
		if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
			t.Fatal(err)
		}
		before[fmt.Sprintf("%T", o)] = o.GetResourceVersion()
	}

	// The conversion webhook, as Tinkerbell runs it.
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

	tb, err := NewTinkerbell(WithRestConfig(cfg), WithConversionWebhook(ConversionWebhook{
		URL:      fmt.Sprintf("https://%s%s", l.Addr().String(), conversionwebhook.Path),
		CABundle: ca,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"hardware.tinkerbell.org", "workflows.tinkerbell.org", "jobs.bmc.tinkerbell.org"} {
		got, err := tb.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if d := cmp.Diff([]string{"v1alpha2"}, got.Status.StoredVersions); d != "" {
			t.Errorf("%s stored versions (-want +got):\n%s", name, d)
		}
	}

	// Every object was rewritten, i.e. is now stored as v1alpha2.
	for _, o := range []client.Object{&v1alpha1.Hardware{}, &v1alpha1.Workflow{}, &bmcv1.Job{}} {
		key := map[string]client.ObjectKey{
			"*tinkerbell.Hardware": client.ObjectKeyFromObject(hw), "*tinkerbell.Workflow": client.ObjectKeyFromObject(wf), "*bmc.Job": client.ObjectKeyFromObject(job),
		}[fmt.Sprintf("%T", o)]
		if err := c.Get(ctx, key, o); err != nil {
			t.Fatal(err)
		}
		if o.GetResourceVersion() == before[fmt.Sprintf("%T", o)] {
			t.Errorf("%T %s was not rewritten", o, key)
		}
	}

	// v1alpha2 sees the converted objects.
	hw2 := &v1alpha2.Hardware{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hw), hw2); err != nil {
		t.Fatal(err)
	}
	if ni := hw2.Spec.NetworkInterfaces["52:54:00:00:00:01"]; ni.IPAM == nil || ni.IPAM.IPv4.Address != "10.0.0.5" || ni.IPAM.IPv4.Prefix != "24" || hw2.Spec.Arch != "x86_64" {
		t.Errorf("v1alpha2 Hardware: %+v", hw2.Spec)
	}
	wf2 := &v1alpha2.Workflow{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(wf), wf2); err != nil {
		t.Fatal(err)
	}
	if wf2.Annotations[apiconversion.AnnotationTemplate] != "tpl1" || wf2.Status.Metadata.Workflow.State != v1alpha2.StateRunning || wf2.Status.RenderedTasks[0].Actions[0].Command != "sh" {
		t.Errorf("v1alpha2 Workflow: %+v %+v", wf2.Annotations, wf2.Status)
	}
	job2 := &bmcv2.Job{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(job), job2); err != nil {
		t.Fatal(err)
	}
	if job2.Annotations[apiconversion.AnnotationMachine] != "default/m1" || len(job2.Spec.Operations) != 1 {
		t.Errorf("v1alpha2 Job: %+v %+v", job2.Annotations, job2.Spec)
	}

	// v1alpha1 clients see exactly what they wrote.
	opts := cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "ManagedFields", "Annotations", "Generation")
	hw1 := &v1alpha1.Hardware{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hw), hw1); err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(hw.Spec, hw1.Spec); d != "" {
		t.Errorf("v1alpha1 Hardware spec changed (-want +got):\n%s", d)
	}
	wf1 := &v1alpha1.Workflow{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(wf), wf1); err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(wf, wf1, opts, cmpopts.IgnoreTypes(metav1.TypeMeta{})); d != "" {
		t.Errorf("v1alpha1 Workflow changed (-want +got):\n%s", d)
	}

	// A v1alpha2 change shows up in v1alpha1, with the v1alpha1-only data intact.
	ni := hw2.Spec.NetworkInterfaces["52:54:00:00:00:01"]
	ni.IPAM.IPv4.Address = "10.0.0.6"
	hw2.Spec.NetworkInterfaces["52:54:00:00:00:01"] = ni
	if err := c.Update(ctx, hw2); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hw), hw1); err != nil {
		t.Fatal(err)
	}
	if got := hw1.Spec.Interfaces[0].DHCP; got.IP.Address != "10.0.0.6" || got.IfaceName != "eno1" || hw1.Spec.Metadata.Facility.FacilityCode != "onprem" {
		t.Errorf("v1alpha1 view after a v1alpha2 change: %+v %+v", got, hw1.Spec.Metadata)
	}

	// New v1alpha1 objects still work.
	hwNew := hw.DeepCopy()
	hwNew.ObjectMeta = metav1.ObjectMeta{Name: "hw2", Namespace: "default"}
	if err := c.Create(ctx, hwNew); err != nil {
		t.Fatal(err)
	}
	got := &v1alpha1.Hardware{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(hwNew), got); err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(hw.Spec, got.Spec); d != "" {
		t.Errorf("new v1alpha1 Hardware (-want +got):\n%s", d)
	}

	// Running the migration again is a no-op.
	if err := tb.MigrateAndReady(ctx); err != nil {
		t.Fatal(err)
	}
}

func ptrTo[T any](v T) *T { return &v }
