package kube

import (
	"context"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// newFakeBackend returns a Backend reading objs through a fake client that has
// every index in Indexes. A fake client cannot convert between versions, so an
// object can only be read in the version it was given in.
func newFakeBackend(t *testing.T, cfg Backend, objs ...client.Object) *Backend {
	t.Helper()
	scheme := testScheme(t)
	clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithStatusSubresource(&v1alpha2.Hardware{}, &v1alpha2.Workflow{})
	for _, index := range Indexes {
		clientBuilder = clientBuilder.WithIndex(index.Obj, index.Field, index.ExtractValue)
	}
	kubeClient := clientBuilder.Build()
	options := func(options *cluster.Options) {
		options.NewClient = func(*rest.Config, client.Options) (client.Client, error) { return kubeClient, nil }
		options.MapperProvider = func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return kubeClient.RESTMapper(), nil }
		options.NewCache = func(*rest.Config, cache.Options) (cache.Cache, error) {
			return &informertest.FakeInformers{Scheme: scheme}, nil
		}
	}
	cfg.ClientConfig = &rest.Config{Host: "http://127.0.0.1"}
	b, err := NewBackend(cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testScheme returns a scheme with every kind the backend reads.
func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	builder := runtime.NewSchemeBuilder(corev1.AddToScheme, tinkerbell.AddToScheme, bmc.AddToScheme, v1alpha2.AddToScheme, bmcv1alpha2.AddToScheme)
	if err := builder.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func TestNewBackendRendering(t *testing.T) {
	if b := newFakeBackend(t, Backend{}); b.store != nil {
		t.Fatal("rendering disabled must not build a render store")
	}
	registry := metrics.Registry
	metrics.Registry = prometheus.NewRegistry()
	t.Cleanup(func() { metrics.Registry = registry })
	b := newFakeBackend(t, Backend{Rendering: true})
	if b.store == nil {
		t.Fatal("rendering enabled must build a render store")
	}
	b.store.queue.ShutDown()
}

// TestRenderedHardwareCreatedThroughV1Alpha1 reads Hardware created through
// v1alpha1 in both versions, as v1alpha1 the way the Tink Controller's v1alpha1
// compatibility mode does: its templates render with the v1alpha1 rules, fields
// only v1alpha1 has included.
func TestRenderedHardwareCreatedThroughV1Alpha1(t *testing.T) {
	ctx := context.Background()
	old := templatedV1Alpha1()
	old.Spec.Metadata = &tinkerbell.HardwareMetadata{Facility: &tinkerbell.MetadataFacility{FacilityCode: "{{ .references.net.data.domain }}"}}
	hw := createdAsV1Alpha1(t, old)
	b := newFakeBackend(t, Backend{}, hw)
	filter := data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace}

	stored, err := b.RenderedReader().FilterHardware(ctx, filter)
	if err != nil || *stored.Spec.Instance.Userdata != *old.Spec.UserData {
		t.Fatalf("without rendering got %v, %v; want the stored Hardware", stored, err)
	}
	if got, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); err != nil || *got.Spec.UserData != *old.Spec.UserData {
		t.Fatalf("without rendering got %v, %v; want the stored v1alpha1 Hardware", got, err)
	}

	hws, res := &fakeHardware{}, &fakeResolver{refs: netRefs("example.org")}
	b.store = newTestStore(hws, res, &fakeInformers{})
	defer b.store.queue.ShutDown()
	hws.set(stored)
	if _, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found before the first render", err)
	}
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if got, err := b.RenderedReader().FilterHardware(ctx, filter); err != nil || *got.Spec.Instance.Userdata != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering", got, err)
	}
	got, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored))
	if err != nil || *got.Spec.UserData != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering as v1alpha1", got, err)
	}
	if got.Spec.Metadata.Facility.FacilityCode != "example.org" {
		t.Fatalf("facility code = %q; a field only v1alpha1 has must render too", got.Spec.Metadata.Facility.FacilityCode)
	}
	res.set(nil, nil)
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if _, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want a failed rendering to be not found", err)
	}
}

// TestRenderedReader reads v1alpha2 Hardware in both versions: v1alpha2
// readers get the rendering, v1alpha1 readers the same rendering converted.
func TestRenderedReader(t *testing.T) {
	ctx := context.Background()
	hw := templated("1")
	b := newFakeBackend(t, Backend{}, hw)
	filter := data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace}

	stored, err := b.RenderedReader().FilterHardware(ctx, filter)
	if err != nil || *stored.Spec.Instance.Userdata != *hw.Spec.Instance.Userdata {
		t.Fatalf("without rendering got %v, %v; want the stored Hardware", stored, err)
	}
	if got, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); err != nil || *got.Spec.UserData != *hw.Spec.Instance.Userdata {
		t.Fatalf("without rendering got %v, %v; want the stored v1alpha1 Hardware", got, err)
	}

	hws, res := &fakeHardware{}, &fakeResolver{refs: netRefs("example.org")}
	b.store = newTestStore(hws, res, &fakeInformers{})
	defer b.store.queue.ShutDown()
	hws.set(stored)
	if _, err := b.RenderedReader().FilterHardware(ctx, filter); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found before the first render", err)
	}
	if _, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found for v1alpha1 readers before the first render", err)
	}
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if got, err := b.RenderedReader().FilterHardware(ctx, filter); err != nil || *got.Spec.Instance.Userdata != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering", got, err)
	}
	asV1Alpha1, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored))
	if err != nil || *asV1Alpha1.Spec.UserData != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering as v1alpha1", asV1Alpha1, err)
	}
	asV1Alpha1.Spec.UserData = ptr("mutated")
	if again, _ := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); *again.Spec.UserData != "domain=example.org" {
		t.Fatal("a v1alpha1 caller's copy must not change the stored rendering")
	}
	res.set(nil, nil)
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if _, err := b.RenderedReader().FilterHardware(ctx, filter); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want a failed rendering to be not found", err)
	}
	if _, err := b.RenderedHardwareV1Alpha1(ctx, servedAsV1Alpha1(t, stored)); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want a failed rendering to be not found for v1alpha1 readers", err)
	}
}
