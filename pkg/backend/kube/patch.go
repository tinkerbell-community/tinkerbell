package kube

import (
	"context"
	"fmt"

	"github.com/tinkerbell/tinkerbell/pkg/data"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validPatchTypes is the set of Kubernetes patch types accepted by patchFromOpts.
var validPatchTypes = map[types.PatchType]bool{
	types.JSONPatchType:           true,
	types.MergePatchType:          true,
	types.StrategicMergePatchType: true,
	types.ApplyPatchType:          true,
}

// update writes obj, a kind named name in errors, as opts asks: a patch when it
// sets one, otherwise an update; of the status subresource alone with
// StatusOnly, so that the Tink Server writing status does not conflict with the
// controller writing the spec.
func (b *Backend) update(ctx context.Context, obj client.Object, kind, name string, opts data.UpdateOptions) error {
	cc := b.cluster.GetClient()

	p, err := patchFromOpts(opts)
	if err != nil {
		return fmt.Errorf("invalid patch options for %s %s: %w", kind, name, err)
	}
	switch {
	case p != nil && opts.StatusOnly:
		if err := cc.Status().Patch(ctx, obj, p); err != nil {
			return fmt.Errorf("failed to patch %s status %s: %w", kind, name, err)
		}
	case p != nil:
		if err := cc.Patch(ctx, obj, p); err != nil {
			return fmt.Errorf("failed to patch %s %s: %w", kind, name, err)
		}
	case opts.StatusOnly:
		if err := cc.Status().Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to update %s status %s: %w", kind, name, err)
		}
	default:
		if err := cc.Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to update %s %s: %w", kind, name, err)
		}
	}

	return nil
}

// patchFromOpts builds a client.Patch from the given UpdateOptions.
// It returns (nil, nil) when no patch options are set, signaling the caller to fall through to a full Update.
func patchFromOpts(opts data.UpdateOptions) (client.Patch, error) {
	switch {
	case opts.PatchFrom != nil:
		obj, ok := opts.PatchFrom.(client.Object)
		if !ok {
			return nil, fmt.Errorf("PatchFrom must be a client.Object, got %T", opts.PatchFrom)
		}
		return client.MergeFrom(obj), nil
	case opts.RawPatch != nil:
		if opts.RawPatchType == "" {
			return nil, fmt.Errorf("RawPatchType must be set when RawPatch is provided")
		}
		pt := types.PatchType(opts.RawPatchType)
		if !validPatchTypes[pt] {
			return nil, fmt.Errorf("unsupported RawPatchType %q", opts.RawPatchType)
		}
		return client.RawPatch(pt, opts.RawPatch), nil
	default:
		return nil, nil
	}
}
