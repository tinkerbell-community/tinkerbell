package kube

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	"github.com/tinkerbell/tinkerbell/pkg/template/render"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const templateSkipAnnotation = "tinkerbell.org/render-skip"

// Skipped and protected paths. In a path, "[]" matches any slice index and a "*"
// segment any map key.
var skippedHardwarePaths = []string{
	"apiVersion",
	"kind",
	"metadata",
	"status",
	"spec.references",
	"spec.agentID",
	"spec.metadata.instance.id",
	"spec.interfaces[].dhcp.mac",
	"spec.interfaces[].dhcp.ip.address",
}

var protectedHardwarePaths = []string{
	"apiVersion",
	"kind",
	"metadata",
	"status",
	"spec.references",
	"spec.agentID",
	"spec.metadata.instance.id",
	"spec.interfaces[].dhcp.mac",
	"spec.interfaces[].dhcp.ip.address",
}

// hardwareRules are the paths a version of Hardware never renders (skipped) and
// that rendering must leave as they were (protected): the lookup keys and
// everything outside the spec.
type hardwareRules struct {
	skipped, protected []string
}

// renderHardware returns hw with the templates in its spec rendered against the
// Hardware itself (.hardware) and its resolved references (.references). hw is
// never modified; when it has nothing to render it is returned as is.
func renderHardware(hw *tinkerbell.Hardware, references map[string]any) (*tinkerbell.Hardware, error) {
	doc, err := renderObject(hw, hw.Annotations, references, hardwareRules{skippedHardwarePaths, protectedHardwarePaths})
	if err != nil {
		return nil, fmt.Errorf("render hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}
	if doc == nil {
		return hw, nil
	}
	out := &tinkerbell.Hardware{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(doc, out); err != nil {
		return nil, fmt.Errorf("convert rendered hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}
	return out, nil
}

// renderObject renders the templates in obj, a Hardware with the given
// annotations, and returns its rendered unstructured form, or nil when it has
// nothing to render. obj is never modified.
func renderObject(obj any, annotations map[string]string, references map[string]any, rules hardwareRules) (map[string]any, error) {
	skip, err := renderSkip(annotations, rules.skipped)
	if err != nil {
		return nil, err
	}

	doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("convert: %w", err)
	}
	if !render.HasTemplates(doc, render.WithSkip(skip)) {
		return nil, nil
	}
	// Not runtime.DeepCopyJSON(doc): it panics on the uint64 values ToUnstructured produces.
	original, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("convert: %w", err)
	}

	// doc is a map, so it is rendered in place.
	if _, err := render.Value(doc, map[string]any{"references": runtime.DeepCopyJSON(references)},
		render.WithSelfKey("hardware"),
		render.WithFuncs(funcmap.New()),
		render.WithSkip(skip),
	); err != nil {
		return nil, err
	}
	if err := validateProtected(original, doc, rules.protected); err != nil {
		return nil, err
	}
	return doc, nil
}

func validateProtectedHardware(before, after map[string]any) error {
	return validateProtected(before, after, protectedHardwarePaths)
}

func validateProtected(before, after map[string]any, protected []string) error {
	for _, path := range protected {
		if changed := changedProtectedPath(before, after, path); changed != "" {
			return fmt.Errorf("protected field %q changed", changed)
		}
	}
	return nil
}

// Wildcards in skipped and protected paths.
const (
	noWildcard    = 0
	sliceWildcard = '['
	mapWildcard   = '*'
)

// cutWildcard splits path around its first wildcard, "[]." or ".*.", and
// reports which it is.
func cutWildcard(path string) (prefix, rest string, wildcard byte) {
	slice := strings.Index(path, "[].")
	key := strings.Index(path, ".*.")
	switch {
	case key >= 0 && (slice < 0 || key < slice):
		return path[:key], path[key+len(".*."):], mapWildcard
	case slice >= 0:
		return path[:slice], path[slice+len("[]."):], sliceWildcard
	default:
		return path, "", noWildcard
	}
}

func changedProtectedPath(before, after map[string]any, path string) string {
	prefix, rest, wildcard := cutWildcard(path)
	switch wildcard {
	case sliceWildcard:
		return changedProtectedSlice(before, after, prefix, rest)
	case mapWildcard:
		return changedProtectedMap(before, after, prefix, rest)
	default:
		if protectedFieldChanged(before, after, strings.Split(path, ".")...) {
			return path
		}
		return ""
	}
}

func changedProtectedSlice(before, after map[string]any, arrayPath, elementPath string) string {
	fields := strings.Split(arrayPath, ".")
	oldValue, oldFound, oldErr := unstructured.NestedFieldNoCopy(before, fields...)
	newValue, newFound, newErr := unstructured.NestedFieldNoCopy(after, fields...)
	oldElements, oldSlice := oldValue.([]any)
	newElements, newSlice := newValue.([]any)
	if oldErr != nil || newErr != nil || (oldFound && !oldSlice) || (newFound && !newSlice) {
		return arrayPath
	}
	for index := range max(len(oldElements), len(newElements)) {
		var oldElement, newElement map[string]any
		if index < len(oldElements) {
			var ok bool
			oldElement, ok = oldElements[index].(map[string]any)
			if !ok {
				return fmt.Sprintf("%s[%d]", arrayPath, index)
			}
		}
		if index < len(newElements) {
			var ok bool
			newElement, ok = newElements[index].(map[string]any)
			if !ok {
				return fmt.Sprintf("%s[%d]", arrayPath, index)
			}
		}
		if changed := changedProtectedPath(oldElement, newElement, elementPath); changed != "" {
			return fmt.Sprintf("%s[%d].%s", arrayPath, index, changed)
		}
	}
	return ""
}

// changedProtectedMap checks valuePath in every value of the map at mapPath. The
// keys are protected too: adding or removing one is a change.
func changedProtectedMap(before, after map[string]any, mapPath, valuePath string) string {
	fields := strings.Split(mapPath, ".")
	oldValue, oldFound, oldErr := unstructured.NestedFieldNoCopy(before, fields...)
	newValue, newFound, newErr := unstructured.NestedFieldNoCopy(after, fields...)
	oldMap, oldIsMap := oldValue.(map[string]any)
	newMap, newIsMap := newValue.(map[string]any)
	if oldErr != nil || newErr != nil || (oldFound && !oldIsMap) || (newFound && !newIsMap) {
		return mapPath
	}
	keys := maps.Clone(oldMap)
	maps.Copy(keys, newMap)
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		keyPath := fmt.Sprintf("%s[%q]", mapPath, key)
		oldElement, inOld := oldMap[key]
		newElement, inNew := newMap[key]
		if inOld != inNew {
			return keyPath
		}
		oldFields, oldOK := oldElement.(map[string]any)
		newFields, newOK := newElement.(map[string]any)
		if !oldOK || !newOK {
			return keyPath
		}
		if changed := changedProtectedPath(oldFields, newFields, valuePath); changed != "" {
			return keyPath + "." + changed
		}
	}
	return ""
}

func protectedFieldChanged(before, after map[string]any, path ...string) bool {
	oldValue, oldFound, oldErr := unstructured.NestedFieldNoCopy(before, path...)
	newValue, newFound, newErr := unstructured.NestedFieldNoCopy(after, path...)
	return oldErr != nil || newErr != nil || oldFound != newFound || !reflect.DeepEqual(oldValue, newValue)
}

// hardwareRenderSkip returns which paths of hw are not rendered.
func hardwareRenderSkip(hw *tinkerbell.Hardware) (func(string) bool, error) {
	return renderSkip(hw.Annotations, skippedHardwarePaths)
}

// renderSkip returns which paths are not rendered: those in skipped and those
// the render-skip annotation lists. Annotation paths match exactly.
func renderSkip(annotations map[string]string, skipped []string) (func(string) bool, error) {
	skipDefault := func(path string) bool { return skipPath(path, skipped) }
	raw, ok := annotations[templateSkipAnnotation]
	if !ok {
		return skipDefault, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil, fmt.Errorf("annotation %s must be a JSON array of string paths: %w", templateSkipAnnotation, err)
	}
	if paths == nil {
		return nil, fmt.Errorf("annotation %s must be a JSON array of string paths, not null", templateSkipAnnotation)
	}
	annotated := make(map[string]bool, len(paths))
	for _, path := range paths {
		if !strings.HasPrefix(path, "spec.") || path == "spec." {
			return nil, fmt.Errorf("annotation %s path %q must be under spec", templateSkipAnnotation, path)
		}
		annotated[path] = true
	}
	return func(path string) bool { return skipDefault(path) || annotated[path] }, nil
}

func skipRender(path string) bool {
	return skipPath(path, skippedHardwarePaths)
}

// skipPath reports whether path is outside the spec or matches one of skipped.
func skipPath(path string, skipped []string) bool {
	if !strings.HasPrefix(path, "spec.") {
		return true
	}
	for _, s := range skipped {
		if matchesSkippedPath(path, s) {
			return true
		}
	}
	return false
}

// matchesSkippedPath reports whether path, as the render package writes it, is
// skipped or inside skipped.
func matchesSkippedPath(path, skipped string) bool {
	prefix, rest, wildcard := cutWildcard(skipped)
	switch wildcard {
	case sliceWildcard:
		remainder, ok := strings.CutPrefix(path, prefix+"[")
		if !ok {
			return false
		}
		index, remainder, ok := strings.Cut(remainder, "].")
		if !ok {
			return false
		}
		if _, err := strconv.ParseUint(index, 10, 64); err != nil {
			return false
		}
		return matchesSkippedPath(remainder, rest)
	case mapWildcard:
		remainder, ok := strings.CutPrefix(path, prefix)
		if !ok {
			return false
		}
		if remainder, ok = cutMapKey(remainder); !ok {
			return false
		}
		if remainder, ok = strings.CutPrefix(remainder, "."); !ok {
			return false
		}
		return matchesSkippedPath(remainder, rest)
	default:
		return path == skipped || strings.HasPrefix(path, skipped+".") || strings.HasPrefix(path, skipped+"[")
	}
}

// cutMapKey removes the map key path starts with, written the way the render
// package writes it: ".key", or `["key"]` for a key that is not an identifier,
// such as a MAC address.
func cutMapKey(path string) (string, bool) {
	switch {
	case strings.HasPrefix(path, "["):
		quoted, err := strconv.QuotedPrefix(path[1:])
		if err != nil {
			return "", false
		}
		return strings.CutPrefix(path[1+len(quoted):], "]")
	case strings.HasPrefix(path, ".") && len(path) > 1:
		switch end := strings.IndexAny(path[1:], ".["); end {
		case 0:
			return "", false
		case -1:
			return "", true
		default:
			return path[1+end:], true
		}
	default:
		return "", false
	}
}
