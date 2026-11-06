package kube

import (
	"context"
	"fmt"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateHardware creates hw.
func (b *Backend) CreateHardware(ctx context.Context, hw *v1alpha2.Hardware) error {
	if err := b.cluster.GetClient().Create(ctx, hw); err != nil {
		return fmt.Errorf("failed to create hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}

	return nil
}

// ReadHardware returns the Hardware name in namespace. Hardware outside
// b.Namespaces is reported as not found.
func (b *Backend) ReadHardware(ctx context.Context, name, namespace string) (*v1alpha2.Hardware, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.ReadHardware")
	defer span.End()

	if !b.Watches(namespace) {
		err := apierrors.NewNotFound(v1alpha2.GroupVersion.WithResource("hardware").GroupResource(), name)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	hw := &v1alpha2.Hardware{}
	if err := b.cluster.GetClient().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, hw); err != nil {
		return nil, fmt.Errorf("failed to get hardware %s/%s: %w", namespace, name, err)
	}

	return hw, nil
}

// FilterHardware returns the one Hardware matching opts. Zero matches
// return a not-found error and several a multiple-found error.
func (b *Backend) FilterHardware(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.FilterHardware")
	defer span.End()

	hwList := &v1alpha2.HardwareList{}
	if err := b.cluster.GetClient().List(ctx, hwList, hardwareListOptions(opts)...); err != nil {
		return nil, fmt.Errorf("failed to list hardware %s: %w", filterQueryDesc(opts), err)
	}

	nsDesc := ternary(opts.InNamespace == "", "all namespaces", opts.InNamespace)

	if len(hwList.Items) == 0 {
		err := hardwareNotFoundError{name: filterQueryDesc(opts), namespace: nsDesc}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if len(hwList.Items) > 1 {
		err := &foundMultipleHardwareError{id: filterQueryDesc(opts), namespace: nsDesc, count: len(hwList.Items)}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	return &hwList.Items[0], nil
}

// filterQueryDesc builds a human-readable description of a hardware filter for error messages.
func filterQueryDesc(opts data.HardwareFilter) string {
	nsDesc := opts.InNamespace
	if nsDesc == "" {
		nsDesc = "all namespaces"
	}
	desc := fmt.Sprintf("in %s", nsDesc)
	if opts.ByName != "" {
		desc = fmt.Sprintf("%s with name %q", desc, opts.ByName)
	}
	if opts.ByAgentID != "" {
		desc = fmt.Sprintf("%s with agentID %q", desc, opts.ByAgentID)
	}
	if opts.ByMACAddress != "" {
		desc = fmt.Sprintf("%s with MAC %q", desc, opts.ByMACAddress)
	}
	if opts.ByIPAddress != "" {
		desc = fmt.Sprintf("%s with IP %q", desc, opts.ByIPAddress)
	}
	if opts.ByInstanceID != "" {
		desc = fmt.Sprintf("%s with instanceID %q", desc, opts.ByInstanceID)
	}
	return desc
}

// ListHardware returns the Hardware matching opts.
func (b *Backend) ListHardware(ctx context.Context, opts data.HardwareFilter) ([]v1alpha2.Hardware, error) {
	list := &v1alpha2.HardwareList{}
	if err := b.cluster.GetClient().List(ctx, list, hardwareListOptions(opts)...); err != nil {
		return nil, err
	}

	return list.Items, nil
}

// hardwareListOptions returns the list options that select the Hardware
// opts matches with the indexes. ByInstanceID matches spec.instance.lookupID,
// the v1alpha2 form of the v1alpha1 metadata instance ID. MAC and IP addresses
// are normalized the way the indexes store them.
//
// The fields are matched with a single MatchingFields: each one replaces the
// field selector of the one before, so with several only the last would apply.
func hardwareListOptions(opts data.HardwareFilter) []client.ListOption {
	los := []client.ListOption{}
	if opts.InNamespace != "" {
		los = append(los, client.InNamespace(opts.InNamespace))
	}
	fields := client.MatchingFields{}
	if opts.ByAgentID != "" {
		fields[HardwareAgentIDIndex] = opts.ByAgentID
	}
	if opts.ByName != "" {
		fields[NameIndex] = opts.ByName
	}
	if opts.ByIPAddress != "" {
		fields[IPAddrIndex] = normalizeIP(opts.ByIPAddress)
	}
	if opts.ByMACAddress != "" {
		fields[MACAddrIndex] = normalizeMAC(opts.ByMACAddress)
	}
	if opts.ByInstanceID != "" {
		fields[LookupIDIndex] = opts.ByInstanceID
	}
	if len(fields) > 0 {
		los = append(los, fields)
	}

	return los
}

// UpdateHardware writes hw as opts asks, see Backend.update.
func (b *Backend) UpdateHardware(ctx context.Context, hw *v1alpha2.Hardware, opts data.UpdateOptions) error {
	return b.update(ctx, hw, "hardware", hw.Namespace+"/"+hw.Name, opts)
}
