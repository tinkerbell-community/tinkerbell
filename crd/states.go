package crd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/avast/retry-go/v4"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var workflowsV1Alpha2 = schema.GroupVersionResource{Group: GroupTinkerbell, Version: "v1alpha2", Resource: "workflows"}

// NameWorkflowStates rewrites the states that Tinkerbell v0.27 stored in v1alpha2 Workflow
// status as numbers with their names, as later releases store them. A Workflow whose status is
// written again gets the names anyway; one that changes no more, such as a finished one, would
// otherwise show its state as a number. It changes only those states, so validation ratcheting
// accepts the write, and a second run changes nothing.
func (t Tinkerbell) NameWorkflowStates(ctx context.Context) error {
	if _, ok := t.CRDs["workflows."+GroupTinkerbell]; !ok || t.Dynamic == nil {
		return nil
	}
	res := t.Dynamic.Resource(workflowsV1Alpha2)
	named := 0
	opts := metav1.ListOptions{Limit: 250}
	for {
		list, err := res.List(ctx, opts)
		if err != nil {
			return fmt.Errorf("listing workflows: %w", err)
		}
		for i := range list.Items {
			item := &list.Items[i]
			if !nameStates(item.Object["status"]) {
				continue
			}
			if err := t.updateNamedStatus(ctx, item); err != nil {
				return fmt.Errorf("naming the states of workflow %s/%s: %w", item.GetNamespace(), item.GetName(), err)
			}
			named++
		}
		if list.GetContinue() == "" {
			break
		}
		opts.Continue = list.GetContinue()
	}
	if named > 0 && t.Logger.GetSink() != nil {
		t.Logger.Info("named the workflow states stored as numbers", "workflows", named)
	}
	return nil
}

// updateNamedStatus writes the status of item, whose states are named, re-reading it and naming
// its states again when it changed meanwhile.
func (t Tinkerbell) updateNamedStatus(ctx context.Context, item *unstructured.Unstructured) error {
	ri := t.Dynamic.Resource(workflowsV1Alpha2).Namespace(item.GetNamespace())
	return retry.Do(func() error {
		_, err := ri.UpdateStatus(ctx, item, metav1.UpdateOptions{FieldManager: "tinkerbell-storage-migration"})
		if err == nil || apierrors.IsNotFound(err) {
			return nil
		}
		if apierrors.IsConflict(err) {
			fresh, gerr := ri.Get(ctx, item.GetName(), metav1.GetOptions{})
			if gerr != nil {
				if apierrors.IsNotFound(gerr) {
					return nil
				}
				return gerr
			}
			if !nameStates(fresh.Object["status"]) {
				*item = *fresh
				return nil // named meanwhile, by a status write of the controller
			}
			*item = *fresh
		}
		return err
	}, retry.Attempts(5), retry.Context(ctx), retry.LastErrorOnly(true))
}

// nameStates replaces, anywhere in v, each "state" that is a number with that state's name. It
// reports whether it replaced any.
func nameStates(v any) bool {
	changed := false
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "state" {
				if name, ok := stateName(x); ok {
					v[k] = name
					changed = true
					continue
				}
			}
			if nameStates(x) {
				changed = true
			}
		}
	case []any:
		for _, x := range v {
			if nameStates(x) {
				changed = true
			}
		}
	}
	return changed
}

// stateName returns the name of the state that the number v stands for.
func stateName(v any) (string, bool) {
	var n int64
	switch v := v.(type) {
	case int64:
		n = v
	case int:
		n = int64(v)
	case float64:
		if v != math.Trunc(v) {
			return "", false
		}
		n = int64(v)
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return "", false
		}
		n = i
	default:
		return "", false
	}
	if n < int64(v1alpha2.StatePreparing) || n > int64(v1alpha2.StateTimeout) {
		return "", false
	}
	return v1alpha2.State(n).String(), true
}
