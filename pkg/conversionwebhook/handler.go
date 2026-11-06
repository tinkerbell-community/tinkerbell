// Package conversionwebhook serves the CRD conversion webhook the API server
// calls to convert Tinkerbell objects between v1alpha1 and v1alpha2.
package conversionwebhook

import (
	"context"
	"fmt"
	"net/http"

	apiconversion "github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

// Path is the HTTP path the webhook is served on.
const Path = "/convert"

// Scheme returns a scheme with every Tinkerbell API version registered.
func Scheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		v1alpha1.AddToScheme, bmcv1.AddToScheme, v1alpha2.AddToScheme, bmcv2.AddToScheme,
	} {
		if err := add(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// NewHandler returns the conversion webhook HTTP handler.
func NewHandler() (http.Handler, error) {
	scheme, err := Scheme()
	if err != nil {
		return nil, err
	}
	registry := conversion.NewRegistry()
	converters := []struct {
		gk  schema.GroupKind
		new func(*runtime.Scheme) (conversion.Converter, error)
	}{
		{
			schema.GroupKind{Group: v1alpha2.GroupVersion.Group, Kind: "Hardware"},
			conversion.NewHubSpokeConverter(&v1alpha2.Hardware{}, conversion.NewSpokeConverter(&v1alpha1.Hardware{},
				func(_ context.Context, src *v1alpha2.Hardware, dst *v1alpha1.Hardware) error {
					return apiconversion.HardwareToV1Alpha1(src, dst)
				},
				func(_ context.Context, src *v1alpha1.Hardware, dst *v1alpha2.Hardware) error {
					return apiconversion.HardwareToV1Alpha2(src, dst)
				})),
		},
		{
			schema.GroupKind{Group: v1alpha2.GroupVersion.Group, Kind: "Workflow"},
			conversion.NewHubSpokeConverter(&v1alpha2.Workflow{}, conversion.NewSpokeConverter(&v1alpha1.Workflow{},
				func(_ context.Context, src *v1alpha2.Workflow, dst *v1alpha1.Workflow) error {
					return apiconversion.WorkflowToV1Alpha1(src, dst)
				},
				func(_ context.Context, src *v1alpha1.Workflow, dst *v1alpha2.Workflow) error {
					return apiconversion.WorkflowToV1Alpha2(src, dst)
				})),
		},
		{
			schema.GroupKind{Group: bmcv2.GroupVersion.Group, Kind: "Job"},
			conversion.NewHubSpokeConverter(&bmcv2.Job{}, conversion.NewSpokeConverter(&bmcv1.Job{},
				func(_ context.Context, src *bmcv2.Job, dst *bmcv1.Job) error {
					return apiconversion.JobToV1Alpha1(src, dst)
				},
				func(_ context.Context, src *bmcv1.Job, dst *bmcv2.Job) error {
					return apiconversion.JobToV1Alpha2(src, dst)
				})),
		},
	}
	for _, c := range converters {
		conv, err := c.new(scheme)
		if err != nil {
			return nil, fmt.Errorf("creating %s converter: %w", c.gk, err)
		}
		if err := registry.RegisterConverter(c.gk, conv); err != nil {
			return nil, err
		}
	}
	return conversion.NewWebhookHandler(scheme, registry), nil
}
