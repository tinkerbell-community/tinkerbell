// Package translate keeps the v1alpha2 translations of the kinds only v1alpha1
// has: the Tasks of each Template and the Policy of each WorkflowRuleSet.
package translate

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AnnotationTranslation is set on a Template or WorkflowRuleSet that has no
// v1alpha2 translation, to the reason why. It is removed once the object
// translates.
const AnnotationTranslation = "tinkerbell.org/v1alpha2-translation"

// reasonTranslationFailed is the reason of the Warning event emitted when an
// object stops translating, or first fails to.
const reasonTranslationFailed = "TranslationFailed"

// NotTranslatedError is a reason an object has no v1alpha2 translation. The
// reason is recorded on the object; other errors are retried.
type NotTranslatedError struct{ Reason string }

func (e NotTranslatedError) Error() string { return e.Reason }

func notTranslated(format string, args ...any) error {
	return NotTranslatedError{Reason: fmt.Sprintf(format, args...)}
}

// IsNotTranslated reports whether err says why an object has no translation,
// rather than that translating it failed.
func IsNotTranslated(err error) bool {
	var nt NotTranslatedError
	return errors.As(err, &nt) || conversion.IsNotConvertible(err)
}

// ConvertTemplate converts the data of tpl to v1alpha2 Tasks, with the
// template functions Templates are rendered with.
func ConvertTemplate(tpl *v1alpha1.Template) (*conversion.TemplateConversion, error) {
	if tpl.Spec.Data == nil {
		return nil, notTranslated("template has no data")
	}
	return conversion.ConvertTemplate(*tpl.Spec.Data, funcmap.New())
}

// EnsureTemplateTasks makes the Tasks of tpl match conv, its conversion: it
// creates or updates the Tasks conversion.TemplateTasks describes, owned by
// tpl so that they are deleted with it, and deletes the Tasks tpl owns that conv
// no longer has. With a nil conv it deletes all of them. A Task of the same name
// that tpl does not own is left alone and reported as a NotTranslatedError.
func EnsureTemplateTasks(ctx context.Context, c client.Client, tpl *v1alpha1.Template, conv *conversion.TemplateConversion) error {
	owner := metav1.OwnerReference{
		APIVersion: v1alpha1.GroupVersion.String(),
		Kind:       "Template",
		Name:       tpl.Name,
		UID:        tpl.UID,
		Controller: ptr(true),
	}
	wanted := map[string]bool{}
	if conv != nil {
		for _, task := range conversion.TemplateTasks(tpl, conv) {
			wanted[task.Name] = true
			task.OwnerReferences = []metav1.OwnerReference{owner}
			if err := ensureOwned(ctx, c, &task, &v1alpha2.Task{}, tpl.UID, func(existing, want *v1alpha2.Task) bool {
				if equality.Semantic.DeepEqual(existing.Spec, want.Spec) && hasAnnotations(existing, want.Annotations) {
					return false
				}
				existing.Spec = want.Spec
				existing.Annotations = mergeAnnotations(existing.Annotations, want.Annotations)
				return true
			}); err != nil {
				return err
			}
		}
	}

	tasks := &v1alpha2.TaskList{}
	if err := c.List(ctx, tasks, client.InNamespace(tpl.Namespace)); err != nil {
		return fmt.Errorf("listing tasks of template %s/%s: %w", tpl.Namespace, tpl.Name, err)
	}
	for i := range tasks.Items {
		task := &tasks.Items[i]
		if wanted[task.Name] || !controlledBy(task, tpl.UID) {
			continue
		}
		if err := c.Delete(ctx, task); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("deleting task %s/%s that template %s no longer has: %w", task.Namespace, task.Name, tpl.Name, err)
		}
	}
	return nil
}

// ensureOwned creates want, or updates the existing object of its name with
// update when that reports a change. existing is an empty object of want's type.
// An existing object that ownerUID does not control is not changed.
func ensureOwned[T client.Object](ctx context.Context, c client.Client, want, existing T, ownerUID types.UID, update func(existing, want T) bool) error {
	key := client.ObjectKeyFromObject(want)
	kind := fmt.Sprintf("%T", want)
	err := c.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, want); err != nil {
			return fmt.Errorf("creating %s %s: %w", kind, key, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting %s %s: %w", kind, key, err)
	}
	if !controlledBy(existing, ownerUID) {
		return notTranslated("%s %s already exists and is not a translation of this object", kind, key)
	}
	if !update(existing, want) {
		return nil
	}
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("updating %s %s: %w", kind, key, err)
	}
	return nil
}

// controlledBy reports whether obj's controller is the object with uid.
func controlledBy(obj metav1.Object, uid types.UID) bool {
	ref := metav1.GetControllerOf(obj)
	return ref != nil && ref.UID == uid
}

func hasAnnotations(obj metav1.Object, want map[string]string) bool {
	have := obj.GetAnnotations()
	for k, v := range want {
		if got, ok := have[k]; !ok || got != v {
			return false
		}
	}
	return true
}

func mergeAnnotations(have, want map[string]string) map[string]string {
	out := maps.Clone(have)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, want)
	return out
}

// setTranslationAnnotation records reason on obj, or removes the record when
// reason is empty. It reports whether the record changed.
func setTranslationAnnotation(ctx context.Context, c client.Client, obj client.Object, reason string) (bool, error) {
	current, has := obj.GetAnnotations()[AnnotationTranslation]
	if (reason == "" && !has) || (reason != "" && has && current == reason) {
		return false, nil
	}
	original, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return false, fmt.Errorf("copying %T", obj)
	}
	ann := maps.Clone(obj.GetAnnotations())
	if reason == "" {
		delete(ann, AnnotationTranslation)
	} else {
		if ann == nil {
			ann = map[string]string{}
		}
		ann[AnnotationTranslation] = reason
	}
	obj.SetAnnotations(ann)
	if err := c.Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return false, fmt.Errorf("recording the v1alpha2 translation of %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	return true, nil
}

func ptr[T any](v T) *T { return &v }
