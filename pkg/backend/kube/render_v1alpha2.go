package kube

import (
	"fmt"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/template/render"
	"k8s.io/apimachinery/pkg/runtime"
)

// The v1alpha2 forms of the v1alpha1 skipped and protected paths. The "*"
// wildcard on spec.networkInterfaces also protects its keys, the MAC addresses.
var skippedHardwarePathsV1Alpha2 = []string{
	"apiVersion",
	"kind",
	"metadata",
	"status",
	"spec.references",
	"spec.agentID",
	"spec.instance.lookupID",
	"spec.networkInterfaces.*.ipam.ipv4.address",
	"spec.networkInterfaces.*.ipam.ipv6.address",
}

var protectedHardwarePathsV1Alpha2 = []string{
	"apiVersion",
	"kind",
	"metadata",
	"status",
	"spec.references",
	"spec.agentID",
	"spec.instance.lookupID",
	"spec.networkInterfaces.*.ipam.ipv4.address",
	"spec.networkInterfaces.*.ipam.ipv6.address",
}

// rendersAsV1Alpha1 reports whether hw was created through v1alpha1: the
// conversion left its v1alpha1 form in an annotation. Its templates were written
// against the v1alpha1 shape (.hardware.spec.interfaces, .hardware.spec.userData,
// ...), so it is rendered as v1alpha1.
func rendersAsV1Alpha1(hw *v1alpha2.Hardware) bool {
	_, ok := hw.Annotations[conversion.AnnotationV1Alpha1]
	return ok
}

// renderHardwareV1Alpha2 returns hw with the templates in its spec rendered
// against the Hardware itself (.hardware) and its resolved references
// (.references). hw is never modified; when it has nothing to render it is
// returned as is.
//
// Hardware created through v1alpha1 is rendered as v1alpha1, by
// renderHardwareAsV1Alpha1, and that v1alpha1 rendering is returned too. For other
// Hardware it is nil.
func renderHardwareV1Alpha2(hw *v1alpha2.Hardware, references map[string]any) (*v1alpha2.Hardware, *v1alpha1.Hardware, error) {
	if rendersAsV1Alpha1(hw) {
		return renderHardwareAsV1Alpha1(hw, references)
	}
	doc, err := renderObject(hw, hw.Annotations, references, hardwareRules{skippedHardwarePathsV1Alpha2, protectedHardwarePathsV1Alpha2})
	if err != nil {
		return nil, nil, fmt.Errorf("render hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}
	if doc == nil {
		return hw, nil, nil
	}
	out := &v1alpha2.Hardware{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(doc, out); err != nil {
		return nil, nil, fmt.Errorf("convert rendered hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}
	return out, nil, nil
}

// renderHardwareAsV1Alpha1 renders the v1alpha1 form of hw with the v1alpha1
// rules, so that its templates render exactly as they did before v1alpha2 was
// stored, and returns the rendering in both versions. Templates in fields only
// v1alpha2 has are not rendered: v1alpha1 templates cannot have been written for
// them.
func renderHardwareAsV1Alpha1(hw *v1alpha2.Hardware, references map[string]any) (*v1alpha2.Hardware, *v1alpha1.Hardware, error) {
	old := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, old); err != nil {
		return nil, nil, fmt.Errorf("convert hardware %s/%s to v1alpha1: %w", hw.Namespace, hw.Name, err)
	}
	rendered, err := renderHardware(old, references)
	if err != nil {
		return nil, nil, err
	}
	if rendered == old {
		return hw, old, nil
	}
	out := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(rendered, out); err != nil {
		return nil, nil, fmt.Errorf("convert rendered hardware %s/%s to v1alpha2: %w", hw.Namespace, hw.Name, err)
	}
	return out, rendered, nil
}

// needsRenderingV1Alpha2 reports whether hw has templates to render, with the
// rules renderHardwareV1Alpha2 applies to it. Invalid configuration needs
// rendering, so that rendering reports it.
func needsRenderingV1Alpha2(hw *v1alpha2.Hardware) bool {
	if rendersAsV1Alpha1(hw) {
		old := &v1alpha1.Hardware{}
		if err := conversion.HardwareToV1Alpha1(hw, old); err != nil {
			return true
		}
		return needsRendering(old)
	}
	skip, err := renderSkip(hw.Annotations, skippedHardwarePathsV1Alpha2)
	if err != nil {
		return true
	}
	doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hw)
	if err != nil {
		return true
	}
	return render.HasTemplates(doc, render.WithSkip(skip))
}

// hardwareToV1Alpha1 converts a rendered v1alpha2 Hardware for v1alpha1 readers.
func hardwareToV1Alpha1(hw *v1alpha2.Hardware) (*v1alpha1.Hardware, error) {
	out := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, out); err != nil {
		return nil, fmt.Errorf("convert rendered hardware %s/%s to v1alpha1: %w", hw.Namespace, hw.Name, err)
	}
	return out, nil
}
