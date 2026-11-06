// Package conversion converts Tinkerbell objects between API versions.
//
// v1alpha2 is the hub: every conversion goes v1alpha1 -> v1alpha2 or
// v1alpha2 -> v1alpha1. The API server stores v1alpha2 and calls the
// conversion webhook, which uses these functions, whenever a client reads or
// writes v1alpha1.
//
// The versions do not map one to one, so each conversion records on the
// object it produces whatever that version cannot represent (see stash). The
// API server round-trips annotations, so the data comes back when the object
// is converted the other way, and an object that was not changed in between
// converts back exactly as it was.
package conversion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// AnnotationV1Alpha1 is set on v1alpha2 objects converted from v1alpha1. It holds
	// the v1alpha1 data the object was converted from, so that it converts back to
	// the same v1alpha1 object.
	AnnotationV1Alpha1 = "conversion.tinkerbell.org/v1alpha1"

	// AnnotationV1Alpha2 is set on v1alpha1 objects converted from v1alpha2. It holds
	// the v1alpha2 data the object was converted from.
	AnnotationV1Alpha2 = "conversion.tinkerbell.org/v1alpha2"
)

// stash is the data one version keeps in an annotation on the other version.
//
// Spec is the spec of the version that was converted from, minus large fields
// that convert without loss (they are copied back from the converted object).
// SpecHash is the hash of the spec the conversion produced: when the object is
// converted back and its spec still has that hash, it was not changed in the
// other version and Spec is returned as it was. Otherwise the conversion maps
// field by field and only restores from Spec what the other version cannot
// represent. Status holds status data the other version cannot represent; it
// is always restored, because only Tinkerbell writes status.
type stash[Spec, Status any] struct {
	Spec     Spec   `json:"spec"`
	SpecHash string `json:"specHash"`
	Status   Status `json:"status"`
}

// hashOf returns a stable hash of v's JSON encoding. encoding/json sorts map
// keys, so equal values hash equally.
func hashOf(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("hashing for conversion: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// putStash records s in obj's annotation key.
func putStash(obj metav1.Object, key string, s any) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encoding conversion data: %w", err)
	}
	ann := maps.Clone(obj.GetAnnotations())
	if ann == nil {
		ann = map[string]string{}
	}
	ann[key] = string(b)
	obj.SetAnnotations(ann)
	return nil
}

// takeStash decodes the annotation key of src into into and removes the
// annotation from dst. It reports whether the annotation was present.
func takeStash(src, dst metav1.Object, key string, into any) (bool, error) {
	dropAnnotation(dst, key)
	v, ok := src.GetAnnotations()[key]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal([]byte(v), into); err != nil {
		return false, fmt.Errorf("decoding conversion data in annotation %s: %w", key, err)
	}
	return true, nil
}

func dropAnnotation(obj metav1.Object, key string) {
	ann := obj.GetAnnotations()
	if _, ok := ann[key]; !ok {
		return
	}
	ann = maps.Clone(ann)
	delete(ann, key)
	if len(ann) == 0 {
		ann = nil
	}
	obj.SetAnnotations(ann)
}

// jsonCopy converts between two types with the same JSON encoding.
func jsonCopy(src, dst any) error {
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
