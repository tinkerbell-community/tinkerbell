package kube

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/template/render"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// renderStore renders Hardware in the background, so that request paths such as
// DHCP and iPXE only look up results and never render or call the API server.
//
// It watches and renders v1alpha2 Hardware, and serves v1alpha1 readers the
// same renderings converted, so that there is one rendering per Hardware
// whichever version a component reads.
//
// Referenced objects are read live by the workers. Informers on their types
// hold metadata only and are used just to notice changes.
type renderStore struct {
	informers          informerGetter
	referenceInformers metadataInformerFactories
	namespaces         []string
	mapper             meta.RESTMapper
	get                func(context.Context, types.NamespacedName) (*v1alpha2.Hardware, error)
	resolve            func(context.Context, types.NamespacedName, map[string]v1alpha2.Reference) (map[string]any, error)
	queue              workqueue.TypedRateLimitingInterface[types.NamespacedName]
	log                logr.Logger
	renders            *prometheus.CounterVec
	duration           prometheus.Histogram
	notify             chan struct{}
	changes            map[types.NamespacedName]struct{}

	mu        sync.RWMutex
	entries   map[types.NamespacedName]*renderEntry
	decisions map[types.NamespacedName]renderDecision
	refs      map[types.NamespacedName][]refKey
	referrers map[refKey]map[types.NamespacedName]struct{}
	watchMu   sync.Mutex
	watched   map[schema.GroupResource]struct{}
}

type informerGetter interface {
	GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error)
}

type metadataInformerFactory interface {
	ForResource(schema.GroupVersionResource) informers.GenericInformer
	Start(<-chan struct{})
	Shutdown()
}

// metadataInformerFactories holds one factory per watched namespace, or a single
// all-namespaces factory, because a list of namespaces cannot be watched with one request.
type metadataInformerFactories []metadataInformerFactory

func (f metadataInformerFactories) Start(stop <-chan struct{}) {
	for _, factory := range f {
		factory.Start(stop)
	}
}

func (f metadataInformerFactories) Shutdown() {
	for _, factory := range f {
		factory.Shutdown()
	}
}

type metadataListClient struct {
	metadata.Interface
	listed func(schema.GroupResource)
}

func (c *metadataListClient) Resource(resource schema.GroupVersionResource) metadata.Getter {
	getter := c.Interface.Resource(resource)
	return &metadataListResource{ResourceInterface: getter, namespace: getter.Namespace, listed: func() { c.listed(resource.GroupResource()) }}
}

// Explicit LIST/WATCH keeps every completed snapshot on the reconciliation path.
func (*metadataListClient) IsWatchListSemanticsUnSupported() bool { return true }

type metadataListResource struct {
	metadata.ResourceInterface
	namespace func(string) metadata.ResourceInterface
	listed    func()
}

func (r *metadataListResource) Namespace(namespace string) metadata.ResourceInterface {
	scoped := *r
	scoped.ResourceInterface = r.namespace(namespace)
	return &scoped
}

func (r *metadataListResource) List(ctx context.Context, options metav1.ListOptions) (*metav1.PartialObjectMetadataList, error) {
	list, err := r.ResourceInterface.List(ctx, options)
	if err == nil {
		r.listed()
	}
	return list, err
}

// renderEntry is replaced whole, never modified, so readers need no lock on it.
type renderEntry struct {
	uid             types.UID          // of the Hardware owning this entry
	resourceVersion string             // of the Hardware last rendered
	err             error              // rendering that resourceVersion failed
	good            *v1alpha2.Hardware // rendering of resourceVersion, nil if it failed
	identity        bool               // good is the stored object: nothing needed rendering
	// asV1Alpha1 returns good as v1alpha1, for readers not yet moved to v1alpha2.
	// Optional: without it good is converted on each call.
	asV1Alpha1 func() (*v1alpha1.Hardware, error)
}

// newRenderEntry returns the entry for good, a rendering of hw. asV1Alpha1 is
// the v1alpha1 form of good when it is known, otherwise it is converted once,
// when a v1alpha1 reader first asks.
func newRenderEntry(hw, good *v1alpha2.Hardware, asV1Alpha1 *v1alpha1.Hardware) *renderEntry {
	e := &renderEntry{uid: hw.UID, resourceVersion: hw.ResourceVersion, good: good, identity: good == hw}
	if asV1Alpha1 != nil {
		e.asV1Alpha1 = func() (*v1alpha1.Hardware, error) { return asV1Alpha1, nil }
	} else {
		e.asV1Alpha1 = sync.OnceValues(func() (*v1alpha1.Hardware, error) { return hardwareToV1Alpha1(good) })
	}
	return e
}

// goodV1Alpha1 returns good as v1alpha1. Callers must not modify it.
func (e *renderEntry) goodV1Alpha1() (*v1alpha1.Hardware, error) {
	if e.asV1Alpha1 != nil {
		return e.asV1Alpha1()
	}
	return hardwareToV1Alpha1(e.good)
}

type renderDecision struct {
	uid             types.UID
	resourceVersion string
	needed          bool
}

type refKey struct {
	group, resource, namespace, name string
}

func newRenderStore(
	log logr.Logger,
	hardwareInformers informerGetter,
	mapper meta.RESTMapper,
	metadataClient metadata.Interface,
	namespaces []string,
	get func(context.Context, types.NamespacedName) (*v1alpha2.Hardware, error),
	resolve func(context.Context, types.NamespacedName, map[string]v1alpha2.Reference) (map[string]any, error),
	registry prometheus.Registerer,
) *renderStore {
	store := &renderStore{
		informers:  hardwareInformers,
		namespaces: slices.Clone(namespaces),
		mapper:     mapper,
		get:        get,
		resolve:    resolve,
		log:        log,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
			workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{Name: "hardware_render"},
		),
		entries:   map[types.NamespacedName]*renderEntry{},
		decisions: map[types.NamespacedName]renderDecision{},
		refs:      map[types.NamespacedName][]refKey{},
		referrers: map[refKey]map[types.NamespacedName]struct{}{},
		watched:   map[schema.GroupResource]struct{}{},
		notify:    make(chan struct{}, 1),
		changes:   map[types.NamespacedName]struct{}{},
	}
	listClient := &metadataListClient{Interface: metadataClient, listed: store.requeueResource}
	watched := store.namespaces
	if len(watched) == 0 {
		watched = []string{metav1.NamespaceAll}
	}
	for _, ns := range watched {
		store.referenceInformers = append(store.referenceInformers, metadatainformer.NewFilteredSharedInformerFactory(listClient, 0, ns, nil))
	}
	store.renders = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "tinkerbell_hardware_render_attempts_total", Help: "Hardware render attempts by outcome."}, []string{"result"})
	store.duration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "tinkerbell_hardware_render_duration_seconds", Help: "Hardware render attempt duration."})
	registry.MustRegister(store.renders, store.duration,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "tinkerbell_hardware_render_queue_depth", Help: "Hardware keys waiting for rendering."}, func() float64 { return float64(store.queue.Len()) }))
	return store
}

func (s *renderStore) Changes() <-chan struct{} { return s.notify }

func (s *renderStore) TakeChanges() []types.NamespacedName {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]types.NamespacedName, 0, len(s.changes))
	for key := range s.changes {
		keys = append(keys, key)
	}
	clear(s.changes)
	return keys
}

func (s *renderStore) publishLocked(key types.NamespacedName, entry *renderEntry) {
	if entry == nil {
		delete(s.entries, key)
	} else {
		s.entries[key] = entry
	}
	s.changes[key] = struct{}{}
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// needsRendering reports whether a v1alpha1 Hardware has templates to render.
func needsRendering(hw *v1alpha1.Hardware) bool {
	skip, err := hardwareRenderSkip(hw)
	if err != nil {
		return true
	}
	doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hw)
	if err != nil {
		return true
	}
	return render.HasTemplates(doc, render.WithSkip(skip))
}

func (s *renderStore) rememberDecision(hw *v1alpha2.Hardware) bool {
	decision := renderDecision{uid: hw.UID, resourceVersion: hw.ResourceVersion, needed: needsRenderingV1Alpha2(hw)}
	s.mu.Lock()
	s.decisions[client.ObjectKeyFromObject(hw)] = decision
	s.mu.Unlock()
	return decision.needed
}

// Start renders every Hardware and keeps renderings current until ctx is done.
func (s *renderStore) Start(ctx context.Context, workers int) error {
	defer close(s.notify)
	defer s.queue.ShutDown()
	inf, err := s.informers.GetInformer(ctx, &v1alpha2.Hardware{}, cache.BlockUntilSynced(false))
	if err != nil {
		return err
	}
	if _, err := inf.AddEventHandler(s.hardwareHandler()); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for s.processNext(ctx) {
			}
		})
	}
	<-ctx.Done()
	s.queue.ShutDown()
	wg.Wait()
	s.referenceInformers.Shutdown()

	return nil
}

// rendered returns the rendering of hw to serve and reports whether there is one.
// A failed rendering is never served. While a newer resourceVersion is still
// rendering, the previous rendering is served if it succeeded, so that status
// updates during provisioning do not briefly take the Hardware away.
func (s *renderStore) rendered(hw *v1alpha2.Hardware) (*v1alpha2.Hardware, bool) {
	e, stored := s.serve(hw, func() bool { return needsRenderingV1Alpha2(hw) })
	switch {
	case stored:
		return hw, true
	case e != nil:
		return e.good.DeepCopy(), true
	default:
		return nil, false
	}
}

// renderedV1Alpha1 is rendered for a v1alpha1 reader: hw is the v1alpha1 form of
// a Hardware the store renders as v1alpha2, and the rendering is served as
// v1alpha1. The API server serves both versions of a Hardware with the same UID
// and resourceVersion, so they identify the rendering.
func (s *renderStore) renderedV1Alpha1(hw *v1alpha1.Hardware) (*v1alpha1.Hardware, bool, error) {
	e, stored := s.serve(hw, func() bool { return needsRendering(hw) })
	switch {
	case stored:
		return hw, true, nil
	case e != nil:
		good, err := e.goodV1Alpha1()
		if err != nil {
			return nil, false, err
		}
		return good.DeepCopy(), true, nil
	default:
		return nil, false, nil
	}
}

// serve decides what to serve for obj, the version of a Hardware a reader holds:
// obj itself (stored), the rendering in e, or, when both are zero, nothing.
// needsRendering reports whether obj has templates; it is only called when no
// decision is recorded for obj's version.
func (s *renderStore) serve(obj metav1.Object, needsRendering func() bool) (e *renderEntry, stored bool) {
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	uid, resourceVersion := obj.GetUID(), obj.GetResourceVersion()
	s.mu.RLock()
	e = s.entries[key]
	decision, known := s.decisions[key]
	s.mu.RUnlock()
	if e != nil && e.uid != uid {
		e = nil
	}

	if e != nil && e.resourceVersion == resourceVersion && e.err == nil && e.identity {
		return nil, true
	}
	if e == nil || e.resourceVersion != resourceVersion {
		// Not rendered at this version yet; only the startup and edit windows get here.
		needed := decision.needed
		if !known || decision.uid != uid || decision.resourceVersion != resourceVersion {
			needed = needsRendering()
		}
		if !needed {
			return nil, true
		}
	}
	if e != nil && e.good != nil {
		return e, false
	}
	return nil, false
}

func (s *renderStore) processNext(ctx context.Context) bool {
	key, shutdown := s.queue.Get()
	if shutdown {
		return false
	}
	defer s.queue.Done(key)

	if s.render(ctx, key) {
		s.queue.Forget(key)
	} else {
		s.queue.AddRateLimited(key)
	}
	return true
}

// render reports whether rendering and reference watch registration succeeded.
func (s *renderStore) render(ctx context.Context, key types.NamespacedName) (succeeded bool) {
	started := time.Now()
	defer func() {
		result := "success"
		if !succeeded {
			result = "error"
		}
		s.renders.WithLabelValues(result).Inc()
		s.duration.Observe(time.Since(started).Seconds())
	}()
	hw, err := s.get(ctx, key)
	if apierrors.IsNotFound(err) {
		s.forget(key)
		return true
	}
	if err != nil {
		s.log.Error(err, "get hardware to render", "hardware", key)
		return false
	}
	if !s.rememberDecision(hw) {
		s.mu.Lock()
		s.setRefs(key, nil)
		s.publishLocked(key, newRenderEntry(hw, hw, nil))
		s.mu.Unlock()
		return true
	}
	watchErr := s.track(ctx, key, hw.Spec.References)

	// A denied or unreadable reference only fails the render if a template uses it.
	refs, refErr := s.resolve(ctx, key, hw.Spec.References)
	var e *renderEntry
	good, asV1Alpha1, err := renderHardwareV1Alpha2(hw, refs)
	if err != nil {
		e = &renderEntry{uid: hw.UID, resourceVersion: hw.ResourceVersion, err: errors.Join(refErr, err)}
	} else {
		e = newRenderEntry(hw, good, asV1Alpha1)
	}
	s.mu.Lock()
	s.publishLocked(key, e)
	s.mu.Unlock()
	if err != nil {
		s.log.Error(e.err, "render hardware", "hardware", key)
	}

	return err == nil && watchErr == nil
}

// forget drops everything held for a deleted Hardware.
func (s *renderStore) forget(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(key, nil)
	delete(s.decisions, key)
	s.setRefs(key, nil)
}

// track records what key references, so that a change to a referenced object
// re-renders it, and starts watching referenced types not yet watched.
func (s *renderStore) track(ctx context.Context, key types.NamespacedName, references map[string]v1alpha2.Reference) error {
	if len(s.namespaces) > 0 {
		references = maps.Clone(references)
		maps.DeleteFunc(references, func(_ string, r v1alpha2.Reference) bool { return !slices.Contains(s.namespaces, r.Namespace) })
	}
	refs := make([]refKey, 0, len(references))
	for _, r := range references {
		refs = append(refs, refKey{strings.ToLower(r.Group), strings.ToLower(r.Resource), r.Namespace, r.Name})
	}

	s.mu.Lock()
	s.setRefs(key, refs)
	s.mu.Unlock()

	var watchErr error
	for _, reference := range references {
		gvr := schema.GroupVersionResource{Group: strings.ToLower(reference.Group), Resource: strings.ToLower(reference.Resource), Version: reference.Version}
		if err := s.ensureWatch(ctx, gvr); err != nil {
			s.log.Error(err, "watch referenced objects; scheduling retry", "resource", gvr)
			watchErr = errors.Join(watchErr, err)
		}
	}
	return watchErr
}

func (s *renderStore) requeueResource(resource schema.GroupResource) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for reference, hardware := range s.referrers {
		if reference.group == resource.Group && reference.resource == resource.Resource {
			for key := range hardware {
				s.queue.Add(key)
			}
		}
	}
}

func (s *renderStore) ensureWatch(ctx context.Context, gvr schema.GroupVersionResource) error {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if _, ok := s.watched[gvr.GroupResource()]; ok {
		return nil
	}
	if err := s.watch(ctx, gvr); err != nil {
		return err
	}
	s.watched[gvr.GroupResource()] = struct{}{}
	return nil
}

// setRefs replaces key's references in the reverse index. s.mu must be held.
func (s *renderStore) setRefs(key types.NamespacedName, refs []refKey) {
	for _, r := range s.refs[key] {
		delete(s.referrers[r], key)
		if len(s.referrers[r]) == 0 {
			delete(s.referrers, r)
		}
	}
	if len(refs) == 0 {
		delete(s.refs, key)
		return
	}
	s.refs[key] = refs
	for _, r := range refs {
		if s.referrers[r] == nil {
			s.referrers[r] = map[types.NamespacedName]struct{}{}
		}
		s.referrers[r][key] = struct{}{}
	}
}

// watch starts a metadata-only informer on gvr that re-renders the Hardware
// referencing a changed object.
func (s *renderStore) watch(ctx context.Context, gvr schema.GroupVersionResource) error {
	if _, err := s.mapper.KindFor(gvr); err != nil {
		return err
	}
	handler := s.enqueueHandler(func(changed types.NamespacedName) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for hw := range s.referrers[refKey{gvr.Group, gvr.Resource, changed.Namespace, changed.Name}] {
			s.queue.Add(hw)
		}
	})
	// On failure, remove the handlers already added so that a retry adds each once.
	type added struct {
		informer     toolscache.SharedIndexInformer
		registration toolscache.ResourceEventHandlerRegistration
	}
	var registered []added
	for _, factory := range s.referenceInformers {
		inf := factory.ForResource(gvr).Informer()
		reg, err := inf.AddEventHandler(handler)
		if err != nil {
			for _, r := range registered {
				_ = r.informer.RemoveEventHandler(r.registration)
			}
			return err
		}
		registered = append(registered, added{inf, reg})
	}
	s.referenceInformers.Start(ctx.Done())
	return nil
}

// enqueueHandler calls enqueue with the key of every added, updated or deleted object.
func (s *renderStore) hardwareHandler() toolscache.ResourceEventHandler {
	handler := s.enqueueHandler(func(key types.NamespacedName) { s.queue.Add(key) })
	enqueue := handler.AddFunc
	handler.AddFunc = func(object any) {
		if hw, ok := object.(*v1alpha2.Hardware); ok {
			s.rememberDecision(hw)
		}
		enqueue(object)
	}
	handler.UpdateFunc = func(before, after any) {
		oldHardware, oldOK := before.(*v1alpha2.Hardware)
		newHardware, newOK := after.(*v1alpha2.Hardware)
		if !oldOK || !newOK || hardwareInputsChanged(oldHardware, newHardware) {
			handler.AddFunc(after)
		} else {
			s.rememberDecision(newHardware)
		}
	}
	return handler
}

func hardwareInputsChanged(before, after *v1alpha2.Hardware) bool {
	b, a := before.DeepCopy(), after.DeepCopy()
	for _, hw := range []*v1alpha2.Hardware{b, a} {
		hw.ResourceVersion = ""
		hw.ManagedFields = nil
	}
	// Not equality.Semantic: it ignores differences templates can see, such as a nil
	// vs empty slice.
	return !reflect.DeepEqual(b, a)
}

func (s *renderStore) enqueueHandler(enqueue func(types.NamespacedName)) toolscache.ResourceEventHandlerFuncs {
	handle := func(obj any) {
		if t, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		if o, ok := obj.(client.Object); ok {
			enqueue(client.ObjectKeyFromObject(o))
		}
	}
	return toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { handle(obj) },
		UpdateFunc: func(_, obj any) { handle(obj) },
		DeleteFunc: handle,
	}
}
