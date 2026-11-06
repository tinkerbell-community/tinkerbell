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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	scheme := runtime.NewScheme()
	builder := runtime.NewSchemeBuilder(corev1.AddToScheme, tinkerbell.AddToScheme, bmc.AddToScheme, v1alpha2.AddToScheme, bmcv1alpha2.AddToScheme)
	if err := builder.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithStatusSubresource(&tinkerbell.Hardware{}, &tinkerbell.Workflow{}, &v1alpha2.Hardware{}, &v1alpha2.Workflow{})
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

// TestRenderedReader reads Hardware created through v1alpha1 as v1alpha1, the way
// components not yet moved to v1alpha2 do.
func TestRenderedReader(t *testing.T) {
	ctx := context.Background()
	hw := templatedV1Alpha1()
	hw.Spec.Metadata = &tinkerbell.HardwareMetadata{Facility: &tinkerbell.MetadataFacility{FacilityCode: "{{ .references.net.data.domain }}"}}
	b := newFakeBackend(t, Backend{}, hw)
	filter := data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace}

	stored, err := b.RenderedReader().FilterHardware(ctx, filter)
	if err != nil || *stored.Spec.UserData != *hw.Spec.UserData {
		t.Fatalf("without rendering got %v, %v; want the stored Hardware", stored, err)
	}

	hws, res := &fakeHardware{}, &fakeResolver{refs: netRefs("example.org")}
	b.store = newTestStore(hws, res, &fakeInformers{})
	defer b.store.queue.ShutDown()
	hws.set(createdAsV1Alpha1(t, stored))
	if _, err := b.RenderedReader().FilterHardware(ctx, filter); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found before the first render", err)
	}
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	got, err := b.RenderedReader().FilterHardware(ctx, filter)
	if err != nil || *got.Spec.UserData != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering", got, err)
	}
	if got.Spec.Metadata.Facility.FacilityCode != "example.org" {
		t.Fatalf("facility code = %q; a field only v1alpha1 has must render too", got.Spec.Metadata.Facility.FacilityCode)
	}
	res.set(nil, nil)
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if _, err := b.RenderedReader().FilterHardware(ctx, filter); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want a failed rendering to be not found", err)
	}
}

// TestRenderedReaderV1Alpha2 reads v1alpha2 Hardware in both versions: v1alpha2
// readers get the rendering, v1alpha1 readers the same rendering converted.
func TestRenderedReaderV1Alpha2(t *testing.T) {
	ctx := context.Background()
	hw := templated("1")
	b := newFakeBackend(t, Backend{}, hw)
	filter := data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace}

	stored, err := b.RenderedReader().FilterHardwareV1Alpha2(ctx, filter)
	if err != nil || *stored.Spec.Instance.Userdata != *hw.Spec.Instance.Userdata {
		t.Fatalf("without rendering got %v, %v; want the stored Hardware", stored, err)
	}
	if got, err := b.RenderedHardware(ctx, servedAsV1Alpha1(t, stored)); err != nil || *got.Spec.UserData != *hw.Spec.Instance.Userdata {
		t.Fatalf("without rendering got %v, %v; want the stored v1alpha1 Hardware", got, err)
	}

	hws, res := &fakeHardware{}, &fakeResolver{refs: netRefs("example.org")}
	b.store = newTestStore(hws, res, &fakeInformers{})
	defer b.store.queue.ShutDown()
	hws.set(stored)
	if _, err := b.RenderedReader().FilterHardwareV1Alpha2(ctx, filter); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found before the first render", err)
	}
	if _, err := b.RenderedHardware(ctx, servedAsV1Alpha1(t, stored)); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want not found for v1alpha1 readers before the first render", err)
	}
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if got, err := b.RenderedReader().FilterHardwareV1Alpha2(ctx, filter); err != nil || *got.Spec.Instance.Userdata != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering", got, err)
	}
	asV1Alpha1, err := b.RenderedHardware(ctx, servedAsV1Alpha1(t, stored))
	if err != nil || *asV1Alpha1.Spec.UserData != "domain=example.org" {
		t.Fatalf("got %v, %v; want the rendering as v1alpha1", asV1Alpha1, err)
	}
	asV1Alpha1.Spec.UserData = ptr("mutated")
	if again, _ := b.RenderedHardware(ctx, servedAsV1Alpha1(t, stored)); *again.Spec.UserData != "domain=example.org" {
		t.Fatal("a v1alpha1 caller's copy must not change the stored rendering")
	}
	res.set(nil, nil)
	b.store.render(ctx, client.ObjectKeyFromObject(hw))
	if _, err := b.RenderedReader().FilterHardwareV1Alpha2(ctx, filter); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want a failed rendering to be not found", err)
	}
	if _, err := b.RenderedHardware(ctx, servedAsV1Alpha1(t, stored)); !apierrors.IsNotFound(err) {
		t.Fatalf("err = %v, want a failed rendering to be not found for v1alpha1 readers", err)
	}
}

func TestFilterBMCMachineRendered(t *testing.T) {
	ctx := context.Background()
	hw := templatedV1Alpha1()
	hw.Spec.UserData = nil
	hw.Spec.BMCRef = &corev1.TypedLocalObjectReference{Kind: "Machine", Name: "{{ .references.net.data.machine }}"}
	hw.Spec.Metadata = &tinkerbell.HardwareMetadata{Instance: &tinkerbell.MetadataInstance{SSHKeys: []string{"{{ .references.net.data.key }}"}}}
	machine := &bmc.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "tink"}, Spec: bmc.MachineSpec{Connection: bmc.Connection{
		Host: "host", AuthSecretRef: corev1.SecretReference{Name: "credentials", Namespace: "tink"},
	}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "tink"}, Data: map[string][]byte{"username": []byte("user"), "password": []byte("pass")}}
	b := newFakeBackend(t, Backend{}, hw, machine, secret)
	filter := data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace}
	stored, err := b.FilterHardware(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	hws := &fakeHardware{}
	b.store = newTestStore(hws, &fakeResolver{refs: map[string]any{"net": map[string]any{"data": map[string]any{"machine": "m", "key": "k"}}}}, &fakeInformers{})
	defer b.store.queue.ShutDown()
	hws.set(createdAsV1Alpha1(t, stored))
	b.store.render(ctx, client.ObjectKeyFromObject(hw))

	got, err := b.FilterBMCMachine(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "host" || len(got.SSHPublicKeys) != 1 || got.SSHPublicKeys[0] != "k" {
		t.Fatalf("FilterBMCMachine = %+v, want the rendered Machine name and SSH key", got)
	}
}

func TestFilterBMCMachineUsesHardwareNamespace(t *testing.T) {
	machine := func(namespace, host string) *bmc.Machine {
		return &bmc.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: namespace}, Spec: bmc.MachineSpec{Connection: bmc.Connection{
			Host: host, AuthSecretRef: corev1.SecretReference{Name: "credentials", Namespace: namespace},
		}}}
	}
	secret := func(namespace string) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: namespace}, Data: map[string][]byte{"username": []byte("user"), "password": []byte("pass")}}
	}
	tests := map[string]struct {
		objects  []client.Object
		wantHost string
	}{
		"same name in another namespace": {objects: []client.Object{machine("tink", "own"), secret("tink"), machine("other", "foreign"), secret("other")}, wantHost: "own"},
		"only in another namespace":      {objects: []client.Object{machine("other", "foreign"), secret("other")}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			hw := templatedV1Alpha1()
			hw.Spec.BMCRef = &corev1.TypedLocalObjectReference{Kind: "Machine", Name: "m"}
			b := newFakeBackend(t, Backend{}, append([]client.Object{hw}, tt.objects...)...)
			got, err := b.FilterBMCMachine(context.Background(), data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace})
			if tt.wantHost == "" {
				if err == nil {
					t.Fatalf("FilterBMCMachine = %+v, want an error for a Machine outside the Hardware's namespace", got)
				}
				return
			}
			if err != nil || got.Host != tt.wantHost {
				t.Fatalf("FilterBMCMachine = %+v, %v; want host %q", got, err, tt.wantHost)
			}
		})
	}
}

func TestFilterBMCMachineWithoutBMCRef(t *testing.T) {
	hw := templatedV1Alpha1()
	b := newFakeBackend(t, Backend{}, hw)
	if _, err := b.FilterBMCMachine(context.Background(), data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace}); err == nil {
		t.Fatal("want an error for Hardware without a BMC reference")
	}
}
