// Package backend provides tootles-specific backend logic for converting Hardware resources
// into EC2, NoCloud and Hack instance metadata formats.
//
// Hardware is read as v1alpha2. The EC2 and Hack formats are shaped after v1alpha1 Hardware
// (metadata.instance, facility, plan, operating system, ...), so they are built from the v1alpha1
// view of the Hardware (conversion.HardwareToV1Alpha1). For Hardware created through v1alpha1
// that view is exact: the conversion annotation keeps the v1alpha1 fields v1alpha2 has no
// place for. For Hardware created as v1alpha2 it is a best effort, see addInterfaceMetadata.
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/ec2"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/nocloud"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"sigs.k8s.io/yaml"
)

const tracerName = "github.com/tinkerbell/tinkerbell"

// HardwareFilterer is the interface required to filter Hardware objects.
type HardwareFilterer interface {
	// FilterHardware returns the one Hardware opts matches. ByIPAddress matches the
	// addresses of its network interfaces and ByInstanceID its spec.instance.lookupID.
	FilterHardware(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error)
}

// Backend provides tootles instance metadata by filtering Hardware via a HardwareFilterer.
type Backend struct {
	filterer HardwareFilterer
}

// New creates a new Backend wrapping the given HardwareFilterer.
func New(filterer HardwareFilterer) *Backend {
	return &Backend{filterer: filterer}
}

// GetHackInstance returns a HackInstance for the hardware associated with the given IP.
func (b *Backend) GetHackInstance(ctx context.Context, ip string) (data.HackInstance, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "tootles.backend.GetHackInstance")
	defer span.End()

	hw, err := b.filterer.FilterHardware(ctx, data.HardwareFilter{ByIPAddress: ip})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return data.HackInstance{}, err
	}
	old, err := toV1Alpha1(hw)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return data.HackInstance{}, err
	}

	span.SetStatus(codes.Ok, "")

	return toHackInstance(*old)
}

// GetEC2Instance returns an Ec2Instance for the hardware associated with the given IP.
func (b *Backend) GetEC2Instance(ctx context.Context, ip string) (data.Ec2Instance, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "tootles.backend.GetEC2Instance")
	defer span.End()

	hw, err := b.filterer.FilterHardware(ctx, data.HardwareFilter{ByIPAddress: ip})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return data.Ec2Instance{}, toInstanceNotFoundErr(err, ec2.ErrInstanceNotFound)
	}
	i, err := ec2Instance(hw, ip)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return data.Ec2Instance{}, err
	}

	span.SetStatus(codes.Ok, "")

	return i, nil
}

// GetEC2InstanceByInstanceID returns an Ec2Instance for the hardware with the given instance ID,
// its spec.instance.lookupID (the v1alpha1 metadata.instance.id).
func (b *Backend) GetEC2InstanceByInstanceID(ctx context.Context, instanceID string) (data.Ec2Instance, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "tootles.backend.GetEC2InstanceByInstanceID")
	defer span.End()

	hw, err := b.filterer.FilterHardware(ctx, data.HardwareFilter{ByInstanceID: instanceID})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return data.Ec2Instance{}, toInstanceNotFoundErr(err, ec2.ErrInstanceNotFound)
	}
	i, err := ec2Instance(hw, "")
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return data.Ec2Instance{}, err
	}

	span.SetStatus(codes.Ok, "")

	return i, nil
}

// GetNoCloudInstance returns the NoCloud data of the hardware associated with the given IP.
func (b *Backend) GetNoCloudInstance(ctx context.Context, ip string) (nocloud.Instance, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "tootles.backend.GetNoCloudInstance")
	defer span.End()

	hw, err := b.filterer.FilterHardware(ctx, data.HardwareFilter{ByIPAddress: ip})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nocloud.Instance{}, toInstanceNotFoundErr(err, nocloud.ErrInstanceNotFound)
	}
	i, err := noCloudInstance(hw, ip)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nocloud.Instance{}, err
	}

	span.SetStatus(codes.Ok, "")

	return i, nil
}

// GetNoCloudInstanceByLookupID returns the NoCloud data of the hardware with the given
// spec.instance.lookupID.
func (b *Backend) GetNoCloudInstanceByLookupID(ctx context.Context, lookupID string) (nocloud.Instance, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "tootles.backend.GetNoCloudInstanceByLookupID")
	defer span.End()

	hw, err := b.filterer.FilterHardware(ctx, data.HardwareFilter{ByInstanceID: lookupID})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nocloud.Instance{}, toInstanceNotFoundErr(err, nocloud.ErrInstanceNotFound)
	}
	i, err := noCloudInstance(hw, "")
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nocloud.Instance{}, err
	}

	span.SetStatus(codes.Ok, "")

	return i, nil
}

// toV1Alpha1 returns the v1alpha1 view of hw.
func toV1Alpha1(hw *v1alpha2.Hardware) (*v1alpha1.Hardware, error) {
	old := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, old); err != nil {
		return nil, fmt.Errorf("convert hardware %s/%s to v1alpha1: %w", hw.Namespace, hw.Name, err)
	}
	return old, nil
}

// createdThroughV1Alpha1 reports whether hw was created through v1alpha1, so that its v1alpha1
// view holds all of its v1alpha1 data.
func createdThroughV1Alpha1(hw *v1alpha2.Hardware) bool {
	_, ok := hw.Annotations[conversion.AnnotationV1Alpha1]
	return ok
}

// toHackInstance converts a Tinkerbell Hardware resource to a HackInstance by marshalling and
// unmarshalling. This works because the Hardware resource has historical roots that align with
// the HackInstance struct that is derived from the rootio action.
//
// The data it serves, metadata.instance.storage, only exists in v1alpha1: Hardware created as
// v1alpha2 gets an empty HackInstance.
func toHackInstance(hw v1alpha1.Hardware) (data.HackInstance, error) {
	marshalled, err := json.Marshal(hw.Spec)
	if err != nil {
		return data.HackInstance{}, err
	}

	var i data.HackInstance
	if err := json.Unmarshal(marshalled, &i); err != nil {
		return data.HackInstance{}, err
	}

	return i, nil
}

// ec2Instance returns the EC2 data of hw, built from its v1alpha1 view. ip is the address the
// request came from, empty when hw was looked up by instance ID.
func ec2Instance(hw *v1alpha2.Hardware, ip string) (data.Ec2Instance, error) {
	old, err := toV1Alpha1(hw)
	if err != nil {
		return data.Ec2Instance{}, err
	}
	i := toEC2Instance(*old)
	if !createdThroughV1Alpha1(hw) {
		addInterfaceMetadata(&i, hw, ip)
	}
	return i, nil
}

func toEC2Instance(hw v1alpha1.Hardware) data.Ec2Instance {
	var i data.Ec2Instance

	if hw.Spec.Metadata != nil && hw.Spec.Metadata.Instance != nil {
		i.Metadata.InstanceID = hw.Spec.Metadata.Instance.ID
		i.Metadata.Hostname = hw.Spec.Metadata.Instance.Hostname
		i.Metadata.LocalHostname = hw.Spec.Metadata.Instance.Hostname
		i.Metadata.Tags = hw.Spec.Metadata.Instance.Tags
		i.Metadata.PublicKeys = hw.Spec.Metadata.Instance.SSHKeys

		if hw.Spec.Metadata.Instance.OperatingSystem != nil {
			i.Metadata.OperatingSystem.Slug = hw.Spec.Metadata.Instance.OperatingSystem.Slug
			i.Metadata.OperatingSystem.Distro = hw.Spec.Metadata.Instance.OperatingSystem.Distro
			i.Metadata.OperatingSystem.Version = hw.Spec.Metadata.Instance.OperatingSystem.Version
			i.Metadata.OperatingSystem.ImageTag = hw.Spec.Metadata.Instance.OperatingSystem.ImageTag
		}

		// Iterate over all IPs and set the first one for IPv4 and IPv6 as the values in the
		// instance metadata.
		for _, ip := range hw.Spec.Metadata.Instance.Ips {
			// Public IPv4
			if ip.Family == 4 && ip.Public && i.Metadata.PublicIPv4 == "" {
				i.Metadata.PublicIPv4 = ip.Address
			}

			// Private IPv4
			if ip.Family == 4 && !ip.Public && i.Metadata.LocalIPv4 == "" {
				i.Metadata.LocalIPv4 = ip.Address
			}

			// Public IPv6
			if ip.Family == 6 && i.Metadata.PublicIPv6 == "" {
				i.Metadata.PublicIPv6 = ip.Address
			}
		}
	}

	if hw.Spec.Metadata != nil && hw.Spec.Metadata.Facility != nil {
		i.Metadata.Plan = hw.Spec.Metadata.Facility.PlanSlug
		i.Metadata.Facility = hw.Spec.Metadata.Facility.FacilityCode
	}

	if hw.Spec.UserData != nil {
		i.Userdata = *hw.Spec.UserData
	}

	return i
}

// addInterfaceMetadata adds to i the EC2 data that Hardware created as v1alpha2 holds per network
// interface rather than in an instance: the hostname and addresses of the interface with the
// address ip or, when no interface has it (a lookup by instance ID), of the only interface with
// IPAM. Its IPv4 address is local-ipv4 and its IPv6 address public-ipv6, as v1alpha1 serves
// them. The rest of i comes from the v1alpha1 view: instance-id is spec.instance.lookupID,
// public-keys spec.instance.sshKeys and user-data spec.instance.userdata.
//
// v1alpha2 has no equivalent of the other v1alpha1 instance metadata, so Hardware created as
// v1alpha2 serves these empty: public-ipv4 (v1alpha2 addresses are neither public nor private),
// plan and facility, tags, iqn, and operating-system.
func addInterfaceMetadata(i *data.Ec2Instance, hw *v1alpha2.Hardware, ip string) {
	ipam, matched := interfaceWithAddress(hw, ip)
	if ipam == nil {
		return
	}
	hostname := ""
	for _, addr := range []*v1alpha2.IP{matched, ipam.IPv4, ipam.IPv6} {
		if addr != nil && addr.Hostname != nil && *addr.Hostname != "" {
			hostname = *addr.Hostname
			break
		}
	}
	i.Metadata.Hostname, i.Metadata.LocalHostname = hostname, hostname
	if ipam.IPv4 != nil {
		i.Metadata.LocalIPv4 = ipam.IPv4.Address
	}
	if ipam.IPv6 != nil {
		i.Metadata.PublicIPv6 = ipam.IPv6.Address
	}
}

// interfaceWithAddress returns the IPAM of the network interface of hw with the address ip, and
// that address. When no interface has it, it returns the IPAM of the only interface with IPAM,
// if there is exactly one, and no matched address.
func interfaceWithAddress(hw *v1alpha2.Hardware, ip string) (*v1alpha2.IPAM, *v1alpha2.IP) {
	want, _ := netip.ParseAddr(ip)
	var withIPAM []*v1alpha2.IPAM
	for _, mac := range slices.Sorted(maps.Keys(hw.Spec.NetworkInterfaces)) {
		ipam := hw.Spec.NetworkInterfaces[mac].IPAM
		if ipam == nil {
			continue
		}
		withIPAM = append(withIPAM, ipam)
		if !want.IsValid() {
			continue
		}
		for _, addr := range []*v1alpha2.IP{ipam.IPv4, ipam.IPv6} {
			if addr == nil {
				continue
			}
			if got, err := netip.ParseAddr(addr.Address); err == nil && got.Unmap() == want.Unmap() {
				return ipam, addr
			}
		}
	}
	if len(withIPAM) == 1 {
		return withIPAM[0], nil
	}
	return nil, nil
}

// noCloudMetaData is the NoCloud meta-data served for Hardware without spec.instance.metadata.
type noCloudMetaData struct {
	InstanceID    string   `json:"instance-id,omitempty"`
	LocalHostname string   `json:"local-hostname,omitempty"`
	PublicKeys    []string `json:"public-keys,omitempty"`
}

// noCloudInstance returns the NoCloud data of hw: spec.instance metadata, userdata, vendordata
// and networkConfig. Without spec.instance.metadata, as for all Hardware created through
// v1alpha1, the meta-data is the instance-id, local-hostname and public-keys tootles serves
// as EC2 data. ip is the address the request came from, empty when hw was looked up by its
// lookup ID.
func noCloudInstance(hw *v1alpha2.Hardware, ip string) (nocloud.Instance, error) {
	var i nocloud.Instance
	if inst := hw.Spec.Instance; inst != nil {
		i.UserData = deref(inst.Userdata)
		i.VendorData = deref(inst.Vendordata)
		i.NetworkConfig = deref(inst.NetworkConfig)
		if inst.Metadata != nil {
			i.MetaData = *inst.Metadata
			return i, nil
		}
	}

	e, err := ec2Instance(hw, ip)
	if err != nil {
		return nocloud.Instance{}, err
	}
	md, err := yaml.Marshal(noCloudMetaData{
		InstanceID:    e.Metadata.InstanceID,
		LocalHostname: e.Metadata.LocalHostname,
		PublicKeys:    e.Metadata.PublicKeys,
	})
	if err != nil {
		return nocloud.Instance{}, fmt.Errorf("encode meta-data of hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}
	i.MetaData = string(md)
	return i, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// notFounder is implemented by errors that indicate a resource was not found.
type notFounder interface {
	NotFound() bool
}

// toInstanceNotFoundErr checks if err indicates a not-found condition and, if so,
// wraps it as notFound, the frontend's not found error, so the frontend handles it correctly.
func toInstanceNotFoundErr(err, notFound error) error {
	if isNotFound(err) {
		return fmt.Errorf("%w: %w", notFound, err)
	}
	return err
}

func isNotFound(err error) bool {
	var nf notFounder
	return err != nil && errors.As(err, &nf) && nf.NotFound()
}
