package paritytest

import (
	"fmt"
	"sync"
	"testing"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/crd"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"
)

// V1Alpha2 returns hw as the API server serves it to a v1alpha2 reader: converted
// by the conversion webhook, annotation included, and defaulted with the
// v1alpha2 Hardware CRD schema. A nil hw is nil.
func V1Alpha2(tb testing.TB, hw *v1alpha1.Hardware) *v1alpha2.Hardware {
	tb.Helper()
	out, err := ToV1Alpha2(hw)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// ToV1Alpha2 is V1Alpha2 for test doubles that have no testing.TB at hand.
func ToV1Alpha2(hw *v1alpha1.Hardware) (*v1alpha2.Hardware, error) {
	if hw == nil {
		return nil, nil
	}
	out := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(hw, out); err != nil {
		return nil, fmt.Errorf("converting Hardware %s/%s to v1alpha2: %w", hw.Namespace, hw.Name, err)
	}
	if err := Default(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Default applies the v1alpha2 Hardware CRD defaults to hw, as the API server
// does when it reads Hardware.
func Default(hw *v1alpha2.Hardware) error {
	s, err := hardwareSchema()
	if err != nil {
		return err
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hw)
	if err != nil {
		return err
	}
	defaulting.Default(obj, s)
	return runtime.DefaultUnstructuredConverter.FromUnstructured(obj, hw)
}

var hardwareSchema = sync.OnceValues(func() (*schema.Structural, error) {
	var c apiextensionsv1.CustomResourceDefinition
	if err := sigsyaml.Unmarshal(crd.TinkerbellDefaults["hardware.tinkerbell.org"], &c); err != nil {
		return nil, fmt.Errorf("decoding the Hardware CRD: %w", err)
	}
	for _, v := range c.Spec.Versions {
		if v.Name != v1alpha2.GroupVersion.Version || v.Schema == nil {
			continue
		}
		internal := &apiextensions.JSONSchemaProps{}
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v.Schema.OpenAPIV3Schema, internal, nil); err != nil {
			return nil, fmt.Errorf("converting the Hardware CRD schema: %w", err)
		}
		return schema.NewStructural(internal)
	}
	return nil, fmt.Errorf("the Hardware CRD has no %s schema", v1alpha2.GroupVersion.Version)
})
