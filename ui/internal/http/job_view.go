package webhttp

import (
	"fmt"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/ui/templates"
)

const (
	targetMachine  = "Machine"
	targetHardware = "Hardware"
	targetBMC      = "BMC"
)

// jobTarget returns what a Job runs against. A Job created through v1alpha1
// names its deprecated bmc Machine in an annotation; v1alpha2 Jobs name a
// Hardware or carry the BMC connection.
func jobTarget(job *bmcv1alpha2.Job) templates.JobTarget {
	if machine := job.Annotations[conversion.AnnotationMachine]; machine != "" {
		return templates.JobTarget{TargetKind: targetMachine, Target: machine, TargetPath: "/bmc/machines/" + machine}
	}
	if ref := job.Spec.HardwareRef; ref != nil && ref.Name != "" {
		hw := ref.Namespace + "/" + ref.Name
		return templates.JobTarget{TargetKind: targetHardware, Target: hw, TargetPath: "/hardware/" + hw}
	}
	if c := job.Spec.Connection; c != nil {
		return templates.JobTarget{TargetKind: targetBMC, Target: c.Host}
	}
	return templates.JobTarget{}
}

// describeOperation returns the kind of a Job operation and what it does.
func describeOperation(op bmcv1alpha2.Operations) (kind, details string) {
	switch {
	case op.PowerAction != nil:
		return "Power", string(*op.PowerAction)
	case op.BootDevice != nil:
		var flags []string
		if op.BootDevice.Persistent {
			flags = append(flags, "persistent")
		}
		if op.BootDevice.EFIBoot {
			flags = append(flags, "EFI")
		}
		details = string(op.BootDevice.Name)
		if len(flags) > 0 {
			details += " (" + strings.Join(flags, ", ") + ")"
		}
		return "Boot Device", details
	case op.VirtualMediaAction != nil:
		media := op.VirtualMediaAction.MediaURL
		if media == "" {
			media = "eject"
		}
		return "Virtual Media", string(op.VirtualMediaAction.Kind) + ": " + media
	case op.NetworkBootConfig != nil:
		n := op.NetworkBootConfig
		var parts []string
		if n.HTTPBootEnabled != nil {
			parts = append(parts, "HTTP boot "+onOff(*n.HTTPBootEnabled))
		}
		if n.PXEBootEnabled != nil {
			parts = append(parts, "PXE boot "+onOff(*n.PXEBootEnabled))
		}
		if n.HTTPBootURL != nil {
			parts = append(parts, "HTTP boot URL "+*n.HTTPBootURL)
		}
		if n.HTTPBootTLSMode != nil {
			parts = append(parts, "HTTP boot TLS "+*n.HTTPBootTLSMode)
		}
		return "Network Boot", strings.Join(parts, ", ")
	}
	return "Unknown", ""
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// operationsSummary summarizes a Job's operations, e.g. "power off, boot device pxe".
func operationsSummary(ops []bmcv1alpha2.Operations) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		kind, details := describeOperation(op)
		parts = append(parts, strings.TrimSpace(strings.ToLower(kind)+" "+details))
	}
	return strings.Join(parts, ", ")
}

func toBMCJobRow(job *bmcv1alpha2.Job) templates.BMCJob {
	return templates.BMCJob{
		Name:        job.Name,
		Namespace:   job.Namespace,
		JobTarget:   jobTarget(job),
		Operations:  operationsSummary(job.Spec.Operations),
		Status:      GetBMCJobStatus(job.Status.Conditions),
		CompletedAt: formatTime(job.Status.CompletionTime),
		CreatedAt:   job.GetCreationTimestamp().Format(timeFormat),
	}
}

// bmcJobDetail builds the Job detail page data, without the YAML views.
func bmcJobDetail(job *bmcv1alpha2.Job) templates.BMCJobDetail {
	d := templates.BMCJobDetail{
		Name:        job.Name,
		Namespace:   job.Namespace,
		JobTarget:   jobTarget(job),
		Status:      GetBMCJobStatus(job.Status.Conditions),
		StartedAt:   formatTime(job.Status.StartTime),
		CompletedAt: formatTime(job.Status.CompletionTime),
		Conditions:  bmcConditions(job.Status.Conditions),
		CreatedAt:   job.GetCreationTimestamp().Format(timeFormat),
		Labels:      job.Labels,
		Annotations: job.Annotations,
	}
	if n := len(job.Spec.Operations); n > 0 {
		d.Progress = fmt.Sprintf("%d of %d", job.Status.CompletedOperations, n)
	}
	for i, op := range job.Spec.Operations {
		kind, details := describeOperation(op)
		d.Operations = append(d.Operations, templates.BMCOperation{
			Kind:    kind,
			Details: details,
			Timeout: positiveSeconds(op.TimeoutSeconds),
			Done:    int32(i) < job.Status.CompletedOperations || d.Status == statusCompleted,
		})
	}
	return d
}

func positiveSeconds(v int64) string {
	if v <= 0 {
		return ""
	}
	return seconds(&v)
}
