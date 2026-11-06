package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// valueToPointer converts a value of any type to a pointer of that type.
func valueToPointer[T any](v T) *T {
	return &v
}

func defaultBackoff() func() time.Duration {
	backoffPolicy := backoff.NewExponentialBackOff()
	backoffPolicy.MaxInterval = time.Second
	return backoffPolicy.NextBackOff
}

type backoffConfig struct {
	maxRetries int
	duration   func() time.Duration
}

type backoffOpts func(*backoffConfig)

type netbootJSONPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// jsonPointerEscaper escapes a JSON Pointer reference token (RFC 6901).
var jsonPointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// setNetboot allows or disallows netbooting on every network interface of the Hardware key names: it sets
// netboot.disabled to !allow, as v1alpha1 toggled allowPXE on every interface. The Hardware is patched in
// the cluster with a test of its resourceVersion, and read again on a conflict. h, when non-nil, is the
// Hardware as last read.
func setNetboot(ctx context.Context, cc client.Client, key client.ObjectKey, h *v1alpha2.Hardware, allow bool, opts ...backoffOpts) error {
	bc := &backoffConfig{
		maxRetries: 4,
		duration:   defaultBackoff(),
	}
	for _, opt := range opts {
		opt(bc)
	}
	for attempt := 1; attempt <= bc.maxRetries; attempt++ {
		if h == nil || h.ResourceVersion == "" {
			h = &v1alpha2.Hardware{}
			if err := cc.Get(ctx, key, h); err != nil {
				return fmt.Errorf("hardware not found: name=%v; namespace=%v, error: %w", key.Name, key.Namespace, err)
			}
		}

		operations := []netbootJSONPatchOperation{{
			Op:    "test",
			Path:  "/metadata/resourceVersion",
			Value: h.ResourceVersion,
		}}
		// Sorted, so that the patch does not depend on map order.
		for _, mac := range slices.Sorted(maps.Keys(h.Spec.NetworkInterfaces)) {
			path := "/spec/networkInterfaces/" + jsonPointerEscaper.Replace(string(mac)) + "/netboot"
			if h.Spec.NetworkInterfaces[mac].Netboot == nil {
				operations = append(operations, netbootJSONPatchOperation{
					Op:    "add",
					Path:  path,
					Value: map[string]any{"disabled": !allow},
				})
				continue
			}
			operations = append(operations, netbootJSONPatchOperation{
				Op:    "add",
				Path:  path + "/disabled",
				Value: !allow,
			})
		}
		patchData, err := json.Marshal(operations)
		if err != nil {
			return fmt.Errorf("failed to marshal netboot patch for Hardware %s/%s: %w", h.Namespace, h.Name, err)
		}
		if err := cc.Patch(ctx, h, client.RawPatch(types.JSONPatchType, patchData)); err != nil {
			if isNetbootConflict(err) {
				if attempt >= bc.maxRetries {
					return fmt.Errorf("error updating netboot after %d retries: %w", attempt, err)
				}
				h = nil // reset h to nil to retry fetching the hardware
				// This is a conflict error, which means the hardware object was updated by another process
				// We will retry fetching the hardware object and updating it again.
				time.Sleep(bc.duration())
				continue
			}
			return fmt.Errorf("error updating netboot: %w", err)
		}
		return nil
	}

	return nil
}

// hardwareFrom retrieves the in cluster Hardware ref names, in the Workflow's namespace.
func hardwareFrom(ctx context.Context, cc client.Client, wf *v1alpha2.Workflow, ref v1alpha2.SimpleReference) (*v1alpha2.Hardware, error) {
	if ref.Namespace != "" && ref.Namespace != wf.Namespace {
		return nil, fmt.Errorf("hardware %s/%s is not in the workflow's namespace", ref.Namespace, ref.Name)
	}
	h := &v1alpha2.Hardware{}
	if err := cc.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: wf.Namespace}, h); err != nil {
		return nil, fmt.Errorf("hardware not found: name=%v; namespace=%v, error: %w", ref.Name, wf.Namespace, err)
	}

	return h, nil
}

// toggleHardware allows (or disallows) netbooting on the network interfaces of the Hardware ref names.
// It is idempotent and uses the Workflow.Status.BootOptions.AllowNetboot fields for idempotent checks.
// This function will update the Workflow status.
func (s *state) toggleHardware(ctx context.Context, ref v1alpha2.SimpleReference, allow bool) error {
	// 1. check if we've already set the netboot field to the desired value
	// 2. if not, set the netboot field to the desired value
	// 3. return a WorkflowCondition with the result of the operation

	hw, err := hardwareFrom(ctx, s.client, s.workflow, ref)
	if err != nil {
		s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
			Type:    v1alpha2.ToggleNetbootTrue,
			Status:  metav1.ConditionFalse,
			Reason:  reasonError,
			Message: fmt.Sprintf("error getting hardware: %v", err),
			Time:    &metav1.Time{Time: metav1.Now().UTC()},
		})

		return err
	}

	if allow {
		if s.workflow.Status.BootOptions.AllowNetboot.ToggledTrue {
			return nil
		}
		if err := setNetboot(ctx, s.client, client.ObjectKeyFromObject(hw), hw, allow); err != nil {
			s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
				Type:    v1alpha2.ToggleNetbootTrue,
				Status:  metav1.ConditionFalse,
				Reason:  reasonError,
				Message: fmt.Sprintf("error setting allowPXE to %v: %v", allow, err),
				Time:    &metav1.Time{Time: metav1.Now().UTC()},
			})
			return err
		}
		s.workflow.Status.BootOptions.AllowNetboot.ToggledTrue = true
		s.workflow.Status.SetCondition(v1alpha2.WorkflowCondition{
			Type:    v1alpha2.ToggleNetbootTrue,
			Status:  metav1.ConditionTrue,
			Reason:  "Complete",
			Message: fmt.Sprintf("set allowPXE to %v", allow),
			Time:    &metav1.Time{Time: metav1.Now().UTC()},
		})
		return nil
	}

	if s.workflow.Status.BootOptions.AllowNetboot.ToggledFalse {
		return nil
	}
	if err := setNetboot(ctx, s.client, client.ObjectKeyFromObject(hw), hw, allow); err != nil {
		s.workflow.Status.SetConditionIfDifferent(v1alpha2.WorkflowCondition{
			Type:    v1alpha2.ToggleNetbootFalse,
			Status:  metav1.ConditionFalse,
			Reason:  reasonError,
			Message: fmt.Sprintf("error setting allowPXE to %v: %v", allow, err),
			Time:    &metav1.Time{Time: metav1.Now().UTC()},
		})
		return err
	}
	s.workflow.Status.BootOptions.AllowNetboot.ToggledFalse = true
	s.workflow.Status.SetCondition(v1alpha2.WorkflowCondition{
		Type:    v1alpha2.ToggleNetbootFalse,
		Status:  metav1.ConditionTrue,
		Reason:  "Complete",
		Message: fmt.Sprintf("set allowPXE to %v", allow),
		Time:    &metav1.Time{Time: metav1.Now().UTC()},
	})
	return nil
}

func isNetbootConflict(err error) bool {
	if apierrors.IsConflict(err) {
		return true
	}
	if !apierrors.IsInvalid(err) && !apierrors.IsBadRequest(err) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "test failed") && strings.Contains(message, "resourceversion")
}
