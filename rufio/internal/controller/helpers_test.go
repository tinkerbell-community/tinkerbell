package controller_test

import (
	"context"
	"fmt"
	"sync"

	bmclib "github.com/bmc-toolbox/bmclib/v2"
	bmclibbmc "github.com/bmc-toolbox/bmclib/v2/bmc"
	"github.com/bmc-toolbox/bmclib/v2/providers"
	common "github.com/bmc-toolbox/common"
	"github.com/go-logr/logr"
	"github.com/jacobweinstock/registrar"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/rufio/internal/controller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// This source file is currently a bucket of stuff. If it grows too big, consider breaking it
// into more granular helper sources.

// newScheme returns a scheme with the types Rufio reads and writes.
func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := bmc.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := bmcv2.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := v1alpha2.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return scheme
}

// newClientBuilder creates a fake kube client builder loaded with Rufio's and Kubernetes'
// corev1 schemes. It uses a basic ObjectTracker to avoid managed fields issues with
// controller-runtime v0.22+.
func newClientBuilder() *fake.ClientBuilder {
	scheme := newScheme()

	ensureTypeMeta := func(obj client.Object) {
		if obj != nil {
			gvks, _, _ := scheme.ObjectKinds(obj)
			if len(gvks) > 0 {
				obj.GetObjectKind().SetGroupVersionKind(gvks[0])
			}
		}
	}

	// Use a basic ObjectTracker that does NOT do managed fields tracking.
	// controller-runtime v0.22+ uses FieldManagedObjectTracker by default which causes issues
	// with MergeFrom patches when TypeMeta is not properly set.
	// Note: WithObjectTracker is incompatible with WithStatusSubresource, so tests using
	// newClientBuilder() must NOT chain WithStatusSubresource. Tests that need WithStatusSubresource
	// (like those in tink/controller/internal/workflow) should create their own fake client builder.
	codecs := serializer.NewCodecFactory(scheme)
	tracker := k8stesting.NewObjectTracker(scheme, codecs.UniversalDecoder())

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjectTracker(tracker).
		WithIndex(&v1alpha2.Hardware{}, controller.HardwareBMCRefIndexKey, controller.HardwareBMCRefIndexFunc).
		WithIndex(&bmc.Task{}, ".metadata.controller", controller.TaskOwnerIndexFunc).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				err := c.Get(ctx, key, obj, opts...)
				ensureTypeMeta(obj)
				return err
			},
			SubResourceGet: func(ctx context.Context, c client.Client, subResource string, obj client.Object, subResourceObj client.Object, opts ...client.SubResourceGetOption) error {
				err := c.SubResource(subResource).Get(ctx, obj, subResourceObj, opts...)
				ensureTypeMeta(obj)
				ensureTypeMeta(subResourceObj)
				return err
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				ensureTypeMeta(obj)
				return c.Update(ctx, obj, opts...)
			},
			// SubResourcePatch and SubResourceUpdate use direct Update since we're using a basic
			// ObjectTracker without status subresource support. As a result, the patch parameter
			// provided to SubResourcePatch is intentionally ignored and a full update is performed
			// instead; tests relying on real patch semantics should not use this helper.
			SubResourcePatch: func(ctx context.Context, c client.Client, _ string, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
				ensureTypeMeta(obj)
				return c.Update(ctx, obj)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, _ string, obj client.Object, _ ...client.SubResourceUpdateOption) error {
				ensureTypeMeta(obj)
				return c.Update(ctx, obj)
			},
		})
}

type testProvider struct {
	PName                 string
	Proto                 string
	Powerstate            string
	PowerSetOK            bool
	BootdeviceOK          bool
	VirtualMediaOK        bool
	ErrOpen               error
	ErrClose              error
	ErrPowerStateGet      error
	ErrPowerStateSet      error
	ErrBootDeviceSet      error
	ErrVirtualMediaInsert error

	// InventoryDevice and ErrInventory control the Inventory() implementation
	// below, used to test BMC inventory collection without a live BMC or a
	// Redfish simulator. InventoryCalls counts how many times Inventory() was
	// called, used to assert on reconcile cadence (e.g. once per 24h, not once
	// per 3-minute power-poll reconcile).
	InventoryDevice *common.Device
	ErrInventory    error
	InventoryCalls  int

	// NetworkBootEnabledOK and ErrSetNetworkBootEnabled control the
	// SetNetworkBootEnabled implementation below, used to test HTTPBootEnabled/PXEBootEnabled
	// without a live BMC.
	NetworkBootEnabledOK     bool
	ErrSetNetworkBootEnabled error

	// HTTPBootURIOK, ErrHTTPBootURISet, and SetHTTPBootURICalls control the
	// SetHTTPBootURI implementation below, used to test HTTPBootURL without a
	// live BMC.
	HTTPBootURIOK       bool
	ErrHTTPBootURISet   error
	SetHTTPBootURICalls []string

	// HTTPBootTLSModeOK, ErrHTTPBootTLSModeSet, and SetHTTPBootTLSModeCalls control the
	// SetHTTPBootTLSMode implementation below, used to test HTTPBootTLSMode without a
	// live BMC.
	HTTPBootTLSModeOK       bool
	ErrHTTPBootTLSModeSet   error
	SetHTTPBootTLSModeCalls []string

	// NetworkBootCallOrder records, across a single runNetworkBootConfig call, the order
	// SetHTTPBootTLSMode and SetHTTPBootURI were invoked in - used to assert TLS mode is set
	// before the URL, since a BMC that still has the stricter TLS mode active can reject a
	// plain-HTTP URL.
	NetworkBootCallOrder []string

	// FollowPowerSet makes PowerSet change the power state PowerStateGet reports,
	// as a BMC does once the power action took effect.
	FollowPowerSet bool

	// mu guards the fields below and Powerstate, which tests running controllers
	// concurrently read and change.
	mu sync.Mutex
	// Calls records every BMC call made through this provider, with its
	// arguments, in order. Opens counts the BMC sessions opened.
	Calls []string
	Opens int
}

func (t *testProvider) recordf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Calls = append(t.Calls, fmt.Sprintf(format, args...))
}

// recorded returns a copy of the BMC calls made so far and the number of sessions opened.
func (t *testProvider) recorded() ([]string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.Calls...), t.Opens
}

// setPowerState changes the power state the provider reports.
func (t *testProvider) setPowerState(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Powerstate = s
}

func (t *testProvider) Name() string {
	if t.PName != "" {
		return t.PName
	}
	return "tester"
}

func (t *testProvider) Protocol() string {
	if t.Proto != "" {
		return t.Proto
	}
	return "redfish"
}

func (t *testProvider) Features() registrar.Features {
	return registrar.Features{
		providers.FeaturePowerState,
		providers.FeaturePowerSet,
		providers.FeatureBootDeviceSet,
		providers.FeatureVirtualMedia,
		providers.FeatureInventoryRead,
		providers.FeatureSetHTTPBootURI,
		providers.FeatureSetNetworkBootEnabled,
		providers.FeatureSetHTTPBootTLSMode,
	}
}

// Inventory implements bmc.InventoryGetter, so this fake provider can also serve
// bmclib.Client.Inventory() calls in tests — no live BMC or Redfish simulator
// needed.
func (t *testProvider) Inventory(_ context.Context) (*common.Device, error) {
	t.recordf("Inventory")
	t.mu.Lock()
	defer t.mu.Unlock()
	t.InventoryCalls++
	return t.InventoryDevice, t.ErrInventory
}

func (t *testProvider) Open(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Opens++
	return t.ErrOpen
}

func (t *testProvider) Close(_ context.Context) error {
	return t.ErrClose
}

func (t *testProvider) PowerStateGet(_ context.Context) (string, error) {
	t.recordf("PowerStateGet")
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Powerstate, t.ErrPowerStateGet
}

func (t *testProvider) PowerSet(_ context.Context, state string) (ok bool, err error) {
	t.recordf("PowerSet(%s)", state)
	if t.FollowPowerSet && t.ErrPowerStateSet == nil {
		switch state {
		case "on":
			t.setPowerState("on")
		case "off", "soft":
			t.setPowerState("off")
		}
	}
	return t.PowerSetOK, t.ErrPowerStateSet
}

func (t *testProvider) BootDeviceSet(_ context.Context, device string, persistent, efiBoot bool) (ok bool, err error) {
	t.recordf("BootDeviceSet(%s, persistent=%v, efi=%v)", device, persistent, efiBoot)
	return t.BootdeviceOK, t.ErrBootDeviceSet
}

func (t *testProvider) SetVirtualMedia(_ context.Context, kind string, mediaURL string) (ok bool, err error) {
	t.recordf("SetVirtualMedia(%s, %s)", kind, mediaURL)
	return t.VirtualMediaOK, t.ErrVirtualMediaInsert
}

func (t *testProvider) SetNetworkBootEnabled(_ context.Context, httpBoot, pxeBoot *bool) (ok bool, err error) {
	t.recordf("SetNetworkBootEnabled(%v, %v)", fmtBoolPtr(httpBoot), fmtBoolPtr(pxeBoot))
	return t.NetworkBootEnabledOK, t.ErrSetNetworkBootEnabled
}

func (t *testProvider) SetHTTPBootURI(_ context.Context, uri string) (ok bool, err error) {
	t.recordf("SetHTTPBootURI(%s)", uri)
	t.SetHTTPBootURICalls = append(t.SetHTTPBootURICalls, uri)
	t.NetworkBootCallOrder = append(t.NetworkBootCallOrder, "SetHTTPBootURI")
	return t.HTTPBootURIOK, t.ErrHTTPBootURISet
}

func (t *testProvider) SetHTTPBootTLSMode(_ context.Context, mode bmclibbmc.HTTPBootTLSMode) (ok bool, err error) {
	t.recordf("SetHTTPBootTLSMode(%s)", mode)
	t.SetHTTPBootTLSModeCalls = append(t.SetHTTPBootTLSModeCalls, string(mode))
	t.NetworkBootCallOrder = append(t.NetworkBootCallOrder, "SetHTTPBootTLSMode")
	return t.HTTPBootTLSModeOK, t.ErrHTTPBootTLSModeSet
}

func fmtBoolPtr(b *bool) string {
	if b == nil {
		return "nil"
	}
	return fmt.Sprint(*b)
}

// newMockBMCClientFactoryFunc returns a new BMCClientFactoryFunc.
func newTestClient(provider *testProvider) controller.ClientFunc {
	return func(ctx context.Context, log logr.Logger, hostIP, username, password string, opts *controller.BMCOptions) (*bmclib.Client, error) {
		o := opts.Translate(hostIP)
		reg := registrar.NewRegistry(registrar.WithLogger(log))
		reg.Register(provider.Name(), provider.Protocol(), provider.Features(), nil, provider)
		o = append(o, bmclib.WithLogger(log), bmclib.WithRegistry(reg))
		cl := bmclib.NewClient(hostIP, username, password, o...)
		return cl, cl.Open(ctx)
	}
}
