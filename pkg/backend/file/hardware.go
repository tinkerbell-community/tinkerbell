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

// FilterHardware returns the one Hardware in the file matching opts.
// Zero matches return a not-found error and several a multiple-found error.
//
// The file may hold Hardware of either version. An entry whose apiVersion is
// tinkerbell.org/v1alpha2 is read as is; any other entry is read as v1alpha1 and
// converted, the way the API server serves v1alpha1 Hardware as v1alpha2.
func (w *Watcher) FilterHardware(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error) {
	tracer := otel.Tracer(tracerName)
	_, span := tracer.Start(ctx, "backend.file.FilterHardware")
	defer span.End()

	w.dataMu.RLock()
	d := w.data
	w.dataMu.RUnlock()

	hwList, err := readHardware(d)
	if err != nil {
		w.Log.Error(err, "failed to read file data")
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	var matches []*v1alpha2.Hardware
	for i := range hwList {
		if matchHardware(&hwList[i], opts) {
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

// readHardware reads the Hardware in d as v1alpha2.
func readHardware(d []byte) ([]v1alpha2.Hardware, error) {
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

// matchHardware reports whether hw matches every selector opts sets
// (AND logic). ByInstanceID matches spec.instance.lookupID, the v1alpha2 form of
// the v1alpha1 metadata instance ID.
func matchHardware(hw *v1alpha2.Hardware, opts data.HardwareFilter) bool {
	if opts.InNamespace != "" && hw.Namespace != opts.InNamespace {
		return false
	}
	if opts.ByName != "" && hw.Name != opts.ByName {
		return false
	}
	if opts.ByAgentID != "" && hw.Spec.AgentID != opts.ByAgentID {
		return false
	}
	if opts.ByMACAddress != "" && !hardwareHasMAC(hw, opts.ByMACAddress) {
		return false
	}
	if opts.ByIPAddress != "" && !hardwareHasIP(hw, opts.ByIPAddress) {
		return false
	}
	if opts.ByInstanceID != "" && (hw.Spec.Instance == nil || hw.Spec.Instance.LookupID != opts.ByInstanceID) {
		return false
	}
	// At least one selector must be set for a match.
	return opts.ByName != "" || opts.ByAgentID != "" || opts.ByMACAddress != "" || opts.ByIPAddress != "" || opts.ByInstanceID != ""
}

func hardwareHasMAC(hw *v1alpha2.Hardware, mac string) bool {
	for m := range hw.Spec.NetworkInterfaces {
		if strings.EqualFold(string(m), mac) {
			return true
		}
	}
	return false
}

// hardwareHasIP reports whether an interface of hw has the IPv4 or
// IPv6 address ip. Addresses are compared parsed, so that different spellings
// of an IPv6 address match.
func hardwareHasIP(hw *v1alpha2.Hardware, ip string) bool {
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
