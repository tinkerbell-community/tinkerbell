package dhcp_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/crd"
	"github.com/tinkerbell/tinkerbell/pkg/conversionwebhook"
	"github.com/tinkerbell/tinkerbell/smee/internal/dhcp"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestConvertParityThroughAPIServer is TestConvertParity with the v1alpha2 form
// of each fixture served by a real API server: the fixture is created as
// v1alpha1, converted by the conversion webhook, stored as v1alpha2, and read
// back as v1alpha2 with the CRD defaults applied. It needs a kube-apiserver and
// etcd: set KUBEBUILDER_ASSETS, e.g. KUBEBUILDER_ASSETS=$(setup-envtest use -p path).
func TestConvertParityThroughAPIServer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := startAPIServer(ctx, t)

	for _, f := range paritytest.Fixtures() {
		old := f.Hardware.DeepCopy()
		old.ResourceVersion, old.CreationTimestamp = "", metav1.Time{}
		if err := c.Create(ctx, old); err != nil {
			t.Fatalf("%s: creating it as v1alpha1: %v", f.Name, err)
		}
		served := &v1alpha2.Hardware{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(old), served); err != nil {
			t.Fatal(err)
		}
		// The other parity tests stand in paritytest.V1Alpha2 for the API server.
		want := paritytest.V1Alpha2(t, f.Hardware)
		if diff := cmp.Diff(want.Spec, served.Spec); diff != "" {
			t.Errorf("%s: served spec differs from paritytest.V1Alpha2 (-paritytest +served):\n%s", f.Name, diff)
		}
		if served.Annotations[conversion.AnnotationV1Alpha1] != want.Annotations[conversion.AnnotationV1Alpha1] {
			t.Errorf("%s: served conversion annotation differs from paritytest.V1Alpha2", f.Name)
		}
		for mac, ni := range served.Spec.NetworkInterfaces {
			if ni.DHCP != nil && ni.DHCP.IPv4 != nil && ni.DHCP.IPv4.LeaseTimeSeconds == nil {
				t.Errorf("%s %s: dhcp.ipv4.leaseTimeSeconds is not defaulted", f.Name, mac)
			}
		}
		for _, mac := range f.MACs() {
			t.Run(f.Name+"/by MAC "+mac.String(), func(t *testing.T) {
				want, err := paritytest.ConvertByMac(mac, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				got, err := dhcp.ConvertByMac(ctx, mac, served)
				if err != nil {
					t.Fatal(err)
				}
				compareParity(t, want, got)
			})
		}
		for _, ip := range f.IPs() {
			t.Run(f.Name+"/by IP "+ip.String(), func(t *testing.T) {
				want, err := paritytest.ConvertByIP(ip, f.Hardware)
				if err != nil {
					t.Fatalf("v1alpha1 conversion: %v", err)
				}
				got, err := dhcp.ConvertByIP(ctx, ip, served)
				if err != nil {
					t.Fatal(err)
				}
				compareParity(t, want, got)
			})
		}
	}
}

// startAPIServer starts an API server with the Tinkerbell CRDs installed, as
// Tinkerbell installs them, and the conversion webhook.
func startAPIServer(ctx context.Context, t *testing.T) client.Client {
	t.Helper()
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tink"}}, metav1.CreateOptions{}); err != nil {
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

	scheme, err := conversionwebhook.Scheme()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
