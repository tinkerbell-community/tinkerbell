package kube

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestNewBackend(t *testing.T) {
	tests := map[string]struct {
		conf      *rest.Config
		opt       cluster.Option
		idxs      map[IndexType]Index
		shouldErr bool
	}{
		"no config": {
			shouldErr: false,
			opt: func(o *cluster.Options) {
				o.NewClient = func(*rest.Config, client.Options) (client.Client, error) {
					return newWorkingClient(t), nil
				}
				o.MapperProvider = func(*rest.Config, *http.Client) (meta.RESTMapper, error) {
					return newWorkingClient(t).RESTMapper(), nil
				}
				o.NewCache = func(*rest.Config, cache.Options) (cache.Cache, error) {
					return &informertest.FakeInformers{Scheme: newWorkingClient(t).Scheme()}, nil
				}
			},
		},
		"failed index field": {
			shouldErr: true,
			conf:      new(rest.Config),
			opt: func(o *cluster.Options) {
				cl := fake.NewClientBuilder().Build()
				o.NewClient = func(*rest.Config, client.Options) (client.Client, error) {
					return cl, nil
				}
				o.MapperProvider = func(*rest.Config, *http.Client) (meta.RESTMapper, error) {
					return cl.RESTMapper(), nil
				}
			},
			idxs: map[IndexType]Index{
				IndexType("one"):              {Obj: &v1alpha2.Hardware{}, Field: MACAddrIndexV1Alpha2, ExtractValue: MACAddrsV1Alpha2},
				IndexType("duplicate of one"): {Obj: &v1alpha2.Hardware{}, Field: MACAddrIndexV1Alpha2, ExtractValue: MACAddrsV1Alpha2},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b, err := NewBackend(Backend{ClientConfig: tt.conf, APIURL: "localhost", Indexes: tt.idxs}, tt.opt)
			if tt.shouldErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.shouldErr && err != nil {
				t.Fatal(err)
			}
			if !tt.shouldErr && b == nil {
				t.Fatal("expected backend")
			}
		})
	}
}

func newWorkingClient(t *testing.T) client.WithWatch {
	t.Helper()
	ct := fake.NewClientBuilder()

	rs := runtime.NewScheme()
	if err := scheme.AddToScheme(rs); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha2.AddToScheme(rs); err != nil {
		t.Fatal(err)
	}

	ct = ct.WithScheme(rs)
	ct = ct.WithRuntimeObjects(&v1alpha2.HardwareList{})
	ct = ct.WithIndex(&v1alpha2.Hardware{}, IPAddrIndexV1Alpha2, func(client.Object) []string {
		var list []string
		return list
	})

	return ct.Build()
}

// watchedKinds is a cache that records the kinds indexed in it or read through
// it. A real cache starts an informer, a watch of the API server, for each.
type watchedKinds struct {
	*informertest.FakeInformers
	mu    sync.Mutex
	kinds map[schema.GroupVersionKind]bool
}

func (w *watchedKinds) record(obj runtime.Object) {
	gvk, err := apiutil.GVKForObject(obj, w.Scheme)
	if err != nil {
		panic(err)
	}
	gvk.Kind = strings.TrimSuffix(gvk.Kind, "List")
	w.mu.Lock()
	defer w.mu.Unlock()
	w.kinds[gvk] = true
}

func (w *watchedKinds) watches(gvk schema.GroupVersionKind) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.kinds[gvk]
}

func (w *watchedKinds) IndexField(ctx context.Context, obj client.Object, field string, extract client.IndexerFunc) error {
	w.record(obj)
	return w.FakeInformers.IndexField(ctx, obj, field, extract)
}

func (w *watchedKinds) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	w.record(obj)
	return w.FakeInformers.GetInformer(ctx, obj, opts...)
}

func (w *watchedKinds) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	w.record(obj)
	return w.FakeInformers.Get(ctx, key, obj, opts...)
}

func (w *watchedKinds) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	w.record(list)
	return w.FakeInformers.List(ctx, list, opts...)
}

// TestBackendWatchesHardwareAndWorkflowsOnlyAsV1Alpha2 checks that neither the
// indexes nor the reads of the backend, rendering included, make it watch
// Hardware or Workflows as v1alpha1 next to v1alpha2: that would hold every
// one of them twice, as the API server serves them in both versions.
func TestBackendWatchesHardwareAndWorkflowsOnlyAsV1Alpha2(t *testing.T) {
	registry := metrics.Registry
	metrics.Registry = prometheus.NewRegistry()
	t.Cleanup(func() { metrics.Registry = registry })

	var watched *watchedKinds
	options := func(o *cluster.Options) {
		o.NewCache = func(_ *rest.Config, opts cache.Options) (cache.Cache, error) {
			watched = &watchedKinds{FakeInformers: &informertest.FakeInformers{Scheme: opts.Scheme}, kinds: map[schema.GroupVersionKind]bool{}}
			return watched, nil
		}
		o.MapperProvider = func(*rest.Config, *http.Client) (meta.RESTMapper, error) {
			return fake.NewClientBuilder().WithScheme(testScheme(t)).Build().RESTMapper(), nil
		}
	}
	// The default client reads through the cache; nothing here reaches the API server.
	b, err := NewBackend(Backend{ClientConfig: &rest.Config{Host: "http://127.0.0.1:1"}, Indexes: Indexes, Rendering: true}, options)
	if err != nil {
		t.Fatal(err)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.store.Start(stopped, 0); err != nil {
		t.Fatal(err)
	}

	// Every read of the backend that may go through the cache. Reads of the API
	// server (ReadWorkflow) and writes do not start informers.
	ctx := context.Background()
	filter := data.HardwareFilter{ByName: "m1", InNamespace: "tink"}
	_, _ = b.FilterHardwareV1Alpha2(ctx, filter)
	_, _ = b.RenderedReader().FilterHardwareV1Alpha2(ctx, filter)
	_, _ = b.ListHardwareV1Alpha2(ctx, filter)
	_, _ = b.ReadHardwareV1Alpha2(ctx, "m1", "tink")
	_, _ = b.ListWorkflowsV1Alpha2(ctx, data.WorkflowFilter{ByAgentID: "agent1", ByTaskAgentID: "agent1"})
	_, _ = b.FilterBMCMachineV1Alpha2(ctx, filter)
	_, _ = b.BMCConnectionV1Alpha2(ctx, createdAsV1Alpha1(t, hardwareWithBMCRef("bmc1")))
	if _, _, err := b.ReadAuthSecret(ctx, "credentials", "tink"); err == nil {
		t.Error("want an error for credentials the cache does not have")
	}
	_, _ = b.ReadTask(ctx, "task1", "tink")
	_, _ = b.ListPolicies(ctx, data.WorkflowFilter{})
	_, _ = b.ListWorkflowRuleSets(ctx, data.WorkflowFilter{})
	_, _ = b.RenderedHardwareV1Alpha2(ctx, templated("1"))
	_, _ = b.RenderedHardware(ctx, servedAsV1Alpha1(t, templated("1")))

	for _, kind := range []schema.GroupVersionKind{v1alpha1.GroupVersion.WithKind("Hardware"), v1alpha1.GroupVersion.WithKind("Workflow")} {
		if watched.watches(kind) {
			t.Errorf("the backend watches %s", kind)
		}
	}
	// It does record what the backend reads, so the check above is not vacuous.
	for _, kind := range []schema.GroupVersionKind{
		v1alpha2.GroupVersion.WithKind("Hardware"), v1alpha2.GroupVersion.WithKind("Workflow"), bmc.GroupVersion.WithKind("Machine"),
	} {
		if !watched.watches(kind) {
			t.Errorf("the backend does not watch %s", kind)
		}
	}
}
