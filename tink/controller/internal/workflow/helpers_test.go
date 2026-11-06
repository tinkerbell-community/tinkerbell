package workflow

import (
	"context"
	"testing"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// The engine reads and writes v1alpha2. Most tests describe Workflows, Hardware and Jobs as v1alpha1
// clients created and read them: they are stored the way the API server stores them, as v1alpha2
// converted by the conversion webhook (v2), and read back the way v1alpha1 clients read them (v1).
// The expected results are those of the v1alpha1 engine, so these tests show the engine behaves as it
// did for objects created through v1alpha1.

var runtimescheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(runtimescheme)
	_ = v1alpha1.AddToScheme(runtimescheme)
	_ = v1alpha2.AddToScheme(runtimescheme)
	_ = bmcv1alpha1.AddToScheme(runtimescheme)
	_ = bmcv1alpha2.AddToScheme(runtimescheme)
}

func GetFakeClientBuilder() *fake.ClientBuilder {
	ensureTypeMeta := func(obj client.Object) {
		if obj != nil {
			gvks, _, _ := runtimescheme.ObjectKinds(obj)
			if len(gvks) > 0 {
				obj.GetObjectKind().SetGroupVersionKind(gvks[0])
			}
		}
	}

	return fake.NewClientBuilder().WithScheme(
		runtimescheme,
	).WithRuntimeObjects(
		&v1alpha2.Hardware{}, &v1alpha1.Template{}, &v1alpha2.Workflow{},
	).WithStatusSubresource(&v1alpha2.Hardware{}, &v1alpha1.Template{}, &v1alpha2.Workflow{}, &v1alpha1.WorkflowRuleSet{}, &bmcv1alpha2.Job{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				err := client.Get(ctx, key, obj, opts...)
				ensureTypeMeta(obj)
				return err
			},
			SubResourceGet: func(ctx context.Context, client client.Client, subResource string, obj client.Object, subResourceObj client.Object, opts ...client.SubResourceGetOption) error {
				err := client.SubResource(subResource).Get(ctx, obj, subResourceObj, opts...)
				ensureTypeMeta(obj)
				ensureTypeMeta(subResourceObj)
				return err
			},
		})
}

// v2 returns v1alpha1 Hardware, Workflows and bmc Jobs as the API server stores them: converted to
// v1alpha2. Other objects are returned as they are.
func v2(t *testing.T, obj client.Object) client.Object {
	t.Helper()
	switch o := obj.(type) {
	case *v1alpha1.Hardware:
		out := &v1alpha2.Hardware{}
		if err := conversion.HardwareToV1Alpha2(o, out); err != nil {
			t.Fatal(err)
		}
		return out
	case *v1alpha1.Workflow:
		return v2Workflow(t, o)
	case *bmcv1alpha1.Job:
		out := &bmcv1alpha2.Job{}
		if err := conversion.JobToV1Alpha2(o, out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return obj
}

func v2Workflow(t *testing.T, wf *v1alpha1.Workflow) *v1alpha2.Workflow {
	t.Helper()
	out := &v1alpha2.Workflow{}
	if err := conversion.WorkflowToV1Alpha2(wf, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// v1Workflow returns wf as a v1alpha1 client reads it.
func v1Workflow(t *testing.T, wf *v1alpha2.Workflow) *v1alpha1.Workflow {
	t.Helper()
	out := &v1alpha1.Workflow{}
	if err := conversion.WorkflowToV1Alpha1(wf, out); err != nil {
		t.Fatal(err)
	}
	dropConversionAnnotations(out)
	return out
}

// v1Hardware returns hw as a v1alpha1 client reads it.
func v1Hardware(t *testing.T, hw *v1alpha2.Hardware) *v1alpha1.Hardware {
	t.Helper()
	out := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, out); err != nil {
		t.Fatal(err)
	}
	dropConversionAnnotations(out)
	return out
}

// dropConversionAnnotations removes the data a conversion records for the way back, which a test
// comparing objects is not interested in.
func dropConversionAnnotations(obj client.Object) {
	ann := obj.GetAnnotations()
	delete(ann, conversion.AnnotationV1Alpha1)
	delete(ann, conversion.AnnotationV1Alpha2)
	if len(ann) == 0 {
		ann = nil
	}
	obj.SetAnnotations(ann)
}

// readV1Workflow reads the Workflow key as a v1alpha1 client does.
func readV1Workflow(t *testing.T, c client.Client, key types.NamespacedName) (*v1alpha1.Workflow, error) {
	t.Helper()
	wf := &v1alpha2.Workflow{}
	if err := c.Get(context.Background(), key, wf); err != nil {
		return nil, err
	}
	return v1Workflow(t, wf), nil
}

// readV1Hardware reads the Hardware key as a v1alpha1 client does.
func readV1Hardware(t *testing.T, c client.Client, key types.NamespacedName) *v1alpha1.Hardware {
	t.Helper()
	hw := &v1alpha2.Hardware{}
	if err := c.Get(context.Background(), key, hw); err != nil {
		t.Fatalf("getting hardware %s: %v", key, err)
	}
	return v1Hardware(t, hw)
}

type fakeReferences struct {
	refs      map[string]any
	err       error
	gotHW     *string
	rendered  *v1alpha1.Hardware
	renderErr error
	// rendered2 and render2Err are what RenderedHardware returns, when set.
	rendered2  *v1alpha2.Hardware
	render2Err error
	// refsFor are the references ResolveReferencesFor returns per source name, when set.
	refsFor map[string]map[string]any
}

func (f fakeReferences) RenderedHardwareV1Alpha1(_ context.Context, hw *v1alpha1.Hardware) (*v1alpha1.Hardware, error) {
	if f.rendered != nil || f.renderErr != nil {
		return f.rendered, f.renderErr
	}
	return hw, nil
}

func (f fakeReferences) RenderedHardware(_ context.Context, hw *v1alpha2.Hardware) (*v1alpha2.Hardware, error) {
	if f.rendered2 != nil || f.render2Err != nil {
		return f.rendered2, f.render2Err
	}
	return hw, nil
}

func (f fakeReferences) ResolveReferencesFor(_ context.Context, src types.NamespacedName, _ map[string]v1alpha2.Reference) (map[string]any, error) {
	if f.gotHW != nil {
		*f.gotHW = src.Name
	}
	if r, ok := f.refsFor[src.Name]; ok {
		return r, f.err
	}
	return f.refs, f.err
}
