package dhcp

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/ccoveille/go-safecast/v2"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
)

// v1alpha1LeaseTime is the lease time Smee served a v1alpha1 interface whose
// lease_time does not fit in 32 bits.
const v1alpha1LeaseTime uint32 = 604800

// v1alpha1Compat is what Smee served Hardware created through v1alpha1 from,
// that its v1alpha2 form does not hold. The conversion keeps the v1alpha1 data
// in the conversion annotation, and the v1alpha1 view of the Hardware restores
// it: that view is what Smee read before it read v1alpha2.
type v1alpha1Compat struct {
	// facility is metadata.facility.facility_code, the facility kernel argument.
	// v1alpha2 has no facility.
	facility string
	// leaseTime is the interface's lease_time as Smee read it. An unset lease
	// time was served as 0 over DHCPv4, and as the default lifetime over DHCPv6;
	// v1alpha2 holds the CRD default, 86400, instead.
	leaseTime *uint32
	// osie is the interface's netboot.osie, as long as it still converts to the
	// Hardware's instance.osie. The conversion turns a base URL and file names
	// into URLs, filling in Smee's default file names and dropping a base URL
	// that has no path; Smee instead used its configured file names and the path
	// of its default OSIE URL. It is nil once instance.osie is changed.
	osie *OSIE
}

// v1alpha1CompatFor returns the v1alpha1 data of hw, and of its interface with
// MAC address mac, when hw was created through v1alpha1. Hardware created
// through v1alpha2 has none.
func v1alpha1CompatFor(hw *v1alpha2.Hardware, mac v1alpha2.MAC) (v1alpha1Compat, error) {
	if _, ok := hw.Annotations[conversion.AnnotationV1Alpha1]; !ok {
		return v1alpha1Compat{}, nil
	}
	old := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(hw, old); err != nil {
		return v1alpha1Compat{}, fmt.Errorf("reading the v1alpha1 form of hardware %s/%s: %w", hw.Namespace, hw.Name, err)
	}

	c := v1alpha1Compat{}
	if m := old.Spec.Metadata; m != nil && m.Facility != nil {
		c.facility = m.Facility.FacilityCode
	}
	i := slices.IndexFunc(old.Spec.Interfaces, func(iface v1alpha1.Interface) bool {
		return iface.DHCP != nil && strings.EqualFold(iface.DHCP.MAC, string(mac))
	})
	if i < 0 {
		return c, nil
	}
	iface := old.Spec.Interfaces[i]

	lease := v1alpha1LeaseTime
	if v, err := safecast.Convert[uint32](iface.DHCP.LeaseTime); err == nil {
		lease = v
	}
	c.leaseTime = &lease

	if osieConvertsTo(iface, hw.Spec.Instance) {
		o := OSIE{}
		if src := iface.Netboot.OSIE; src != nil {
			if b, err := url.Parse(src.BaseURL); err == nil {
				o.BaseURL = b
			}
			o.Kernel = src.Kernel
			o.Initrd = src.Initrd
			o.KernelParams = src.KernelParams
		}
		c.osie = &o
	}

	return c, nil
}

// osieConvertsTo reports whether the OSIE settings of the v1alpha1 interface
// iface convert to the OSIE settings of inst, the v1alpha2 instance. It asks the
// conversion itself, so that the two cannot disagree.
func osieConvertsTo(iface v1alpha1.Interface, inst *v1alpha2.Instance) bool {
	if iface.Netboot == nil {
		return false
	}
	probe := &v1alpha1.Hardware{Spec: v1alpha1.HardwareSpec{Interfaces: []v1alpha1.Interface{*iface.DeepCopy()}}}
	converted := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(probe, converted); err != nil {
		return false
	}
	return sameOSIE(osieOf(converted.Spec.Instance), osieOf(inst))
}

func osieOf(inst *v1alpha2.Instance) v1alpha2.OSIE {
	if inst == nil || inst.OSIE == nil {
		return v1alpha2.OSIE{}
	}
	return *inst.OSIE
}

// sameOSIE compares the settings that locate and start the OSIE.
func sameOSIE(a, b v1alpha2.OSIE) bool {
	return a.KernelURL == b.KernelURL && a.InitrdURL == b.InitrdURL && slices.Equal(a.KernelParams, b.KernelParams)
}
