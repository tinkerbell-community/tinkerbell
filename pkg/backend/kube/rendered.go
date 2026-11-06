package kube

import (
	"context"
	"fmt"

	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/metadata"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const renderWorkers = 4

// RenderedHardware returns the rendering of hw to serve. Without rendering, it
// returns hw. A Hardware with no rendering to serve is not found.
//
// Hardware is rendered as v1alpha2; hw gets that rendering converted to
// v1alpha1. Hardware created through v1alpha1 is rendered as v1alpha1, so its
// templates render as they did before v1alpha2 was stored.
func (b *Backend) RenderedHardware(_ context.Context, hw *v1alpha1.Hardware) (*v1alpha1.Hardware, error) {
	if b.store == nil {
		return hw, nil
	}
	rendered, ok, err := b.store.renderedV1Alpha1(hw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, hardwareNotRenderedError{hardwareNotFoundError{name: hw.Name, namespace: hw.Namespace}}
	}
	return rendered, nil
}

// RenderedHardwareV1Alpha2 is RenderedHardware for v1alpha2 Hardware.
func (b *Backend) RenderedHardwareV1Alpha2(_ context.Context, hw *v1alpha2.Hardware) (*v1alpha2.Hardware, error) {
	if b.store == nil {
		return hw, nil
	}
	rendered, ok := b.store.rendered(hw)
	if !ok {
		return nil, hardwareNotRenderedError{hardwareNotFoundError{name: hw.Name, namespace: hw.Namespace}}
	}
	return rendered, nil
}

// RenderedReader serves rendered Hardware. It only reads, so rendered Hardware
// cannot be written back over its templates.
type RenderedReader struct {
	backend *Backend
}

// RenderedReader returns a reader of rendered Hardware.
func (b *Backend) RenderedReader() *RenderedReader {
	return &RenderedReader{backend: b}
}

// FilterHardware is Backend.FilterHardware, returning the rendered Hardware.
func (r *RenderedReader) FilterHardware(ctx context.Context, opts data.HardwareFilter) (*v1alpha1.Hardware, error) {
	hw, err := r.backend.FilterHardware(ctx, opts)
	if err != nil {
		return nil, err
	}
	return r.backend.RenderedHardware(ctx, hw)
}

// FilterHardwareV1Alpha2 is Backend.FilterHardwareV1Alpha2, returning the
// rendered Hardware.
func (r *RenderedReader) FilterHardwareV1Alpha2(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error) {
	hw, err := r.backend.FilterHardwareV1Alpha2(ctx, opts)
	if err != nil {
		return nil, err
	}
	return r.backend.RenderedHardwareV1Alpha2(ctx, hw)
}

// hardwareNotRenderedError is not found to consumers, which cannot use a Hardware
// before its templates render, but says why.
type hardwareNotRenderedError struct {
	hardwareNotFoundError
}

func (h hardwareNotRenderedError) Error() string {
	return fmt.Sprintf("hardware %s/%s has templates that have not rendered successfully", h.namespace, h.name)
}

func (b *Backend) newRenderStore() (*renderStore, error) {
	metadataClient, err := metadata.NewForConfig(b.ClientConfig)
	if err != nil {
		return nil, fmt.Errorf("create Hardware reference metadata client: %w", err)
	}
	get := func(ctx context.Context, key types.NamespacedName) (*v1alpha2.Hardware, error) {
		hw := &v1alpha2.Hardware{}
		return hw, b.cluster.GetClient().Get(ctx, key, hw)
	}
	return newRenderStore(b.Logger.WithName("hardware-render"), b.cluster.GetCache(), b.cluster.GetRESTMapper(),
		metadataClient, b.Namespaces, get, b.ResolveReferencesFor, metrics.Registry), nil
}
