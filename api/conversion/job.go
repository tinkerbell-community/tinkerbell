package conversion

import (
	"reflect"
	"strings"

	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
)

// AnnotationMachine names, as "namespace/name", the deprecated
// bmc.tinkerbell.org/v1alpha1 Machine a Job converted from v1alpha1 runs
// against. v1alpha2 Jobs carry the BMC connection or a Hardware reference
// instead, so Rufio uses this to find the BMC of such Jobs.
const AnnotationMachine = "bmc.tinkerbell.org/machine"

// JobToV1Alpha2 converts a v1alpha1 Job to v1alpha2.
func JobToV1Alpha2(src *bmcv1.Job, dst *bmcv2.Job) error {
	dst.ObjectMeta = *src.ObjectMeta.DeepCopy()
	dst.SetGroupVersionKind(bmcv2.GroupVersion.WithKind("Job"))

	var hub stash[bmcv2.JobSpec, struct{}]
	found, err := takeStash(src, dst, AnnotationV1Alpha2, &hub)
	if err != nil {
		return err
	}
	dropAnnotation(dst, AnnotationV1Alpha1)
	dropAnnotation(dst, AnnotationMachine)

	mapped := bmcv2.JobSpec{Operations: actionsToOperations(src.Spec.Tasks)}
	dst.Spec = mapped
	if found {
		h, err := hashOf(src.Spec)
		if err != nil {
			return err
		}
		if h == hub.SpecHash {
			dst.Spec = hub.Spec
		} else {
			dst.Spec.HardwareRef = hub.Spec.HardwareRef
			dst.Spec.Connection = hub.Spec.Connection
		}
	}
	if ref := src.Spec.MachineRef; ref.Name != "" && dst.Spec.HardwareRef == nil && dst.Spec.Connection == nil {
		ann := dst.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann[AnnotationMachine] = ref.Namespace + "/" + ref.Name
		dst.SetAnnotations(ann)
	}

	dst.Status = bmcv2.JobStatus{StartTime: src.Status.StartTime, CompletionTime: src.Status.CompletionTime}
	for _, c := range src.Status.Conditions {
		dst.Status.Conditions = append(dst.Status.Conditions, bmcv2.Condition{
			Type: bmcv2.ConditionType(c.Type), Status: bmcv2.ConditionStatus(c.Status), Message: c.Message,
		})
	}

	h, err := hashOf(normalizeJobV2(dst.Spec))
	if err != nil {
		return err
	}
	return putStash(dst, AnnotationV1Alpha1, stash[bmcv1.JobSpec, struct{}]{Spec: src.Spec, SpecHash: h})
}

// JobToV1Alpha1 converts a v1alpha2 Job to v1alpha1.
func JobToV1Alpha1(src *bmcv2.Job, dst *bmcv1.Job) error {
	dst.ObjectMeta = *src.ObjectMeta.DeepCopy()
	dst.SetGroupVersionKind(bmcv1.GroupVersion.WithKind("Job"))

	var spoke stash[bmcv1.JobSpec, struct{}]
	found, err := takeStash(src, dst, AnnotationV1Alpha1, &spoke)
	if err != nil {
		return err
	}
	dropAnnotation(dst, AnnotationV1Alpha2)
	dropAnnotation(dst, AnnotationMachine)

	mapped := bmcv1.JobSpec{Tasks: operationsToActions(src.Spec.Operations)}
	if ns, name, ok := strings.Cut(src.Annotations[AnnotationMachine], "/"); ok {
		mapped.MachineRef = bmcv1.MachineRef{Namespace: ns, Name: name}
	} else if src.Spec.HardwareRef != nil {
		// Closest v1alpha1 equivalent: Hardware and its Machine usually share a name.
		mapped.MachineRef = bmcv1.MachineRef{Namespace: src.Spec.HardwareRef.Namespace, Name: src.Spec.HardwareRef.Name}
	}
	dst.Spec = mapped
	if found {
		h, err := hashOf(normalizeJobV2(src.Spec))
		if err != nil {
			return err
		}
		if h == spoke.SpecHash {
			dst.Spec = spoke.Spec
		} else if mapped.MachineRef.Name == "" {
			dst.Spec.MachineRef = spoke.Spec.MachineRef
		}
	}

	dst.Status = bmcv1.JobStatus{StartTime: src.Status.StartTime, CompletionTime: src.Status.CompletionTime}
	for _, c := range src.Status.Conditions {
		dst.Status.Conditions = append(dst.Status.Conditions, bmcv1.JobCondition{
			Type: bmcv1.JobConditionType(c.Type), Status: bmcv1.ConditionStatus(c.Status), Message: c.Message,
		})
	}

	h, err := hashOf(dst.Spec)
	if err != nil {
		return err
	}
	return putStash(dst, AnnotationV1Alpha2, stash[bmcv2.JobSpec, struct{}]{Spec: src.Spec, SpecHash: h})
}

func normalizeJobV2(s bmcv2.JobSpec) bmcv2.JobSpec {
	s = *s.DeepCopy()
	normalizeConnection(s.Connection)
	return s
}

// actionsToOperations converts v1alpha1 BMC actions to v1alpha2 operations. A
// v1alpha2 operation does one thing, so an action with several fields becomes
// several operations, in the order Rufio applies them.
func actionsToOperations(actions []bmcv1.Action) []bmcv2.Operations {
	var out []bmcv2.Operations
	for _, a := range actions {
		if a.PowerAction != nil {
			out = append(out, bmcv2.Operations{PowerAction: ptr(bmcv2.PowerAction(*a.PowerAction))})
		}
		if o := a.OneTimeBootDeviceAction; o != nil && len(o.Devices) > 0 { //nolint:staticcheck // converting the deprecated field
			out = append(out, bmcv2.Operations{BootDevice: &bmcv2.BootDeviceConfig{Name: bmcv2.BootDevice(o.Devices[0]), EFIBoot: o.EFIBoot}})
		}
		if b := a.BootDevice; b != nil {
			out = append(out, bmcv2.Operations{BootDevice: &bmcv2.BootDeviceConfig{Name: bmcv2.BootDevice(b.Device), Persistent: b.Persistent, EFIBoot: b.EFIBoot}})
		}
		if v := a.VirtualMediaAction; v != nil {
			out = append(out, bmcv2.Operations{VirtualMediaAction: &bmcv2.VirtualMediaAction{MediaURL: v.MediaURL, Kind: bmcv2.VirtualMediaKind(v.Kind)}})
		}
		if n := a.NetworkBootConfig; n != nil {
			out = append(out, bmcv2.Operations{NetworkBootConfig: &bmcv2.NetworkBootConfig{
				HTTPBootEnabled: copyPtr(n.HTTPBootEnabled), PXEBootEnabled: copyPtr(n.PXEBootEnabled),
				HTTPBootURL: copyPtr(n.HTTPBootURL), HTTPBootTLSMode: copyPtr(n.HTTPBootTLSMode),
			}})
		}
	}
	return out
}

// operationsToActions converts v1alpha2 operations to v1alpha1 actions, one each.
func operationsToActions(ops []bmcv2.Operations) []bmcv1.Action {
	var out []bmcv1.Action
	for _, o := range ops {
		a := bmcv1.Action{}
		if o.PowerAction != nil {
			a.PowerAction = ptr(bmcv1.PowerAction(*o.PowerAction))
		}
		if b := o.BootDevice; b != nil {
			a.BootDevice = &bmcv1.BootDeviceConfig{Device: bmcv1.BootDevice(b.Name), Persistent: b.Persistent, EFIBoot: b.EFIBoot}
		}
		if v := o.VirtualMediaAction; v != nil {
			a.VirtualMediaAction = &bmcv1.VirtualMediaAction{MediaURL: v.MediaURL, Kind: bmcv1.VirtualMediaKind(v.Kind)}
		}
		if n := o.NetworkBootConfig; n != nil {
			a.NetworkBootConfig = &bmcv1.NetworkBootConfig{
				HTTPBootEnabled: copyPtr(n.HTTPBootEnabled), PXEBootEnabled: copyPtr(n.PXEBootEnabled),
				HTTPBootURL: copyPtr(n.HTTPBootURL), HTTPBootTLSMode: copyPtr(n.HTTPBootTLSMode),
			}
		}
		if !reflect.DeepEqual(a, bmcv1.Action{}) {
			out = append(out, a)
		}
	}
	return out
}
