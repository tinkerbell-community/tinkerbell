package file

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"sigs.k8s.io/yaml"
)

// FilterHardwareV1Alpha2 is FilterHardware for v1alpha2 Hardware.
//
// The file may hold Hardware of either version. An entry whose apiVersion is
// tinkerbell.org/v1alpha2 is read as is; any other entry is read as v1alpha1 and
// converted, the way the API server serves v1alpha1 Hardware as v1alpha2.
func (w *Watcher) FilterHardwareV1Alpha2(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error) {
	tracer := otel.Tracer(tracerName)
	_, span := tracer.Start(ctx, "backend.file.FilterHardwareV1Alpha2")
	defer span.End()

	w.dataMu.RLock()
	d := w.data
	w.dataMu.RUnlock()

	hwList, err := hardwareV1Alpha2(d)
	if err != nil {
		w.Log.Error(err, "failed to read file data")
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	var matches []*v1alpha2.Hardware
	for i := range hwList {
		if matchHardwareV1Alpha2(&hwList[i], opts) {
			matches = append(matches, &hwList[i])
		}
	}

	switch len(matches) {
	case 0:
		err := hardwareNotFoundError{}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	case 1:
		span.SetStatus(codes.Ok, "")
		return matches[0], nil
	default:
		err := foundMultipleHardwareError{count: len(matches)}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
}

// hardwareV1Alpha2 reads the Hardware in d as v1alpha2.
func hardwareV1Alpha2(d []byte) ([]v1alpha2.Hardware, error) {
	var docs []json.RawMessage
	if err := yaml.Unmarshal(d, &docs); err != nil {
		return nil, fmt.Errorf("%w: %v", errFileFormat, err)
	}

	out := make([]v1alpha2.Hardware, 0, len(docs))
	for i, doc := range docs {
		var tm struct {
			APIVersion string `json:"apiVersion"`
		}
		if err := json.Unmarshal(doc, &tm); err != nil {
			return nil, fmt.Errorf("%w: entry %d: %v", errFileFormat, i, err)
		}
		hw := v1alpha2.Hardware{}
		if tm.APIVersion == v1alpha2.GroupVersion.String() {
			if err := json.Unmarshal(doc, &hw); err != nil {
				return nil, fmt.Errorf("%w: entry %d: %v", errFileFormat, i, err)
			}
			out = append(out, hw)
			continue
		}
		old := v1alpha1.Hardware{}
		if err := json.Unmarshal(doc, &old); err != nil {
			return nil, fmt.Errorf("%w: entry %d: %v", errFileFormat, i, err)
		}
		if err := conversion.HardwareToV1Alpha2(&old, &hw); err != nil {
			return nil, fmt.Errorf("convert entry %d (%s/%s) to v1alpha2: %w", i, old.Namespace, old.Name, err)
		}
		out = append(out, hw)
	}

	return out, nil
}

// matchHardwareV1Alpha2 is matchHardware for v1alpha2 Hardware. ByInstanceID
// matches spec.instance.lookupID, its v1alpha2 form.
func matchHardwareV1Alpha2(hw *v1alpha2.Hardware, opts data.HardwareFilter) bool {
	if opts.InNamespace != "" && hw.Namespace != opts.InNamespace {
		return false
	}
	if opts.ByName != "" && hw.Name != opts.ByName {
		return false
	}
	if opts.ByAgentID != "" && hw.Spec.AgentID != opts.ByAgentID {
		return false
	}
	if opts.ByMACAddress != "" && !hardwareV1Alpha2HasMAC(hw, opts.ByMACAddress) {
		return false
	}
	if opts.ByIPAddress != "" && !hardwareV1Alpha2HasIP(hw, opts.ByIPAddress) {
		return false
	}
	if opts.ByInstanceID != "" && (hw.Spec.Instance == nil || hw.Spec.Instance.LookupID != opts.ByInstanceID) {
		return false
	}
	// At least one selector must be set for a match.
	return opts.ByName != "" || opts.ByAgentID != "" || opts.ByMACAddress != "" || opts.ByIPAddress != "" || opts.ByInstanceID != ""
}

func hardwareV1Alpha2HasMAC(hw *v1alpha2.Hardware, mac string) bool {
	for m := range hw.Spec.NetworkInterfaces {
		if strings.EqualFold(string(m), mac) {
			return true
		}
	}
	return false
}

// hardwareV1Alpha2HasIP reports whether an interface of hw has the IPv4 or
// IPv6 address ip. Addresses are compared parsed, so that different spellings
// of an IPv6 address match.
func hardwareV1Alpha2HasIP(hw *v1alpha2.Hardware, ip string) bool {
	want, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, ni := range hw.Spec.NetworkInterfaces {
		if ni.IPAM == nil {
			continue
		}
		for _, c := range []*v1alpha2.IP{ni.IPAM.IPv4, ni.IPAM.IPv6} {
			if c == nil {
				continue
			}
			if got, err := netip.ParseAddr(c.Address); err == nil && got.Unmap() == want.Unmap() {
				return true
			}
		}
	}
	return false
}
