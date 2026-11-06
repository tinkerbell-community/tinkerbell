package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type jobName string

const (
	jobNameNetboot             jobName = "netboot"
	jobNameISOMount            jobName = "iso-mount"
	jobNameISOEject            jobName = "iso-eject"
	jobNameCustombootPreparing jobName = "customboot-preparing"
	jobNameCustombootPost      jobName = "customboot-post"
)

func (j jobName) String() string {
	return string(j)
}

// jobNameFor returns the name of the Job of kind j that the boot options of spec.tasks[index] of the
// Workflow named workflow create. The first task's Jobs have the names v1alpha1 used.
func jobNameFor(j jobName, workflow string, index int) jobName {
	if index == 0 {
		return jobName(fmt.Sprintf("%s-%s", j, workflow))
	}
	return jobName(fmt.Sprintf("%s-%s-%d", j, workflow, index))
}

// this function will update the Workflow status.
func (s *state) handleJob(ctx context.Context, hwRef v1alpha2.SimpleReference, ops []bmc.Operations, name jobName) (reconcile.Result, error) {
	// there are 3 phases. 1. Clean up existing 2. Create new 3. Track status
	// 1. clean up existing job if it wasn't already deleted
	if j, found := s.workflow.Status.BootOptions.Jobs[name.String()]; found && !j.ExistingJobDeleted {
		journal.Log(ctx, "deleting existing job", "name", name)
		result, err := s.deleteExisting(ctx, name)
		if err != nil {
			return result, err
		}

		return result, nil
	}

	// 2. create a new job
	if uid := s.workflow.Status.BootOptions.Jobs[name.String()].UID; uid == "" {
		if len(ops) == 0 {
			journal.Log(ctx, "no actions provided", "name", name)
			return reconcile.Result{}, nil
		}
		journal.Log(ctx, "no uid found for job", "name", name)
		result, err := s.createJob(ctx, hwRef, ops, name)
		if err != nil {
			s.workflow.Status.SetCondition(v1alpha2.WorkflowCondition{
				Type:    v1alpha2.BootJobSetupFailed,
				Status:  metav1.ConditionTrue,
				Reason:  reasonError,
				Message: fmt.Sprintf("error creating job: %v", err),
				Time:    &metav1.Time{Time: metav1.Now().UTC()},
			})
			return result, err
		}
		s.workflow.Status.SetCondition(v1alpha2.WorkflowCondition{
			Type:    v1alpha2.BootJobSetupComplete,
			Status:  metav1.ConditionTrue,
			Reason:  "Created",
			Message: "job created",
			Time:    &metav1.Time{Time: metav1.Now().UTC()},
		})
		return result, nil
	}

	// 3. track status
	if !s.workflow.Status.BootOptions.Jobs[name.String()].Complete {
		journal.Log(ctx, "tracking job", "name", name)
		// track status
		r, tState, err := s.trackRunningJob(ctx, name)
		if err != nil {
			s.workflow.Status.SetCondition(v1alpha2.WorkflowCondition{
				Type:    v1alpha2.BootJobFailed,
				Status:  metav1.ConditionTrue,
				Reason:  reasonError,
				Message: err.Error(),
				Time:    &metav1.Time{Time: metav1.Now().UTC()},
			})
			return r, err
		}
		if tState == trackedStateComplete {
			s.workflow.Status.SetCondition(v1alpha2.WorkflowCondition{
				Type:    v1alpha2.BootJobComplete,
				Status:  metav1.ConditionTrue,
				Reason:  "Complete",
				Message: "job completed",
				Time:    &metav1.Time{Time: metav1.Now().UTC()},
			})
		}
		return r, nil
	}

	return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
}

func (s *state) deleteExisting(ctx context.Context, name jobName) (reconcile.Result, error) {
	// delete the tasks.bmc.tinkerbell.org objects
	// Generally, deleting tasks is not needed. But because we have an embedded kube-apiserver option
	// this is precautionary. The Kubernetes garbage collector is not part of the
	// kube-apiserver, it is a separate controller (kube-controller-manager) that
	// watches for delete events, and is not embedded in Tinkerbell, at the moment.
	op := []client.DeleteAllOfOption{
		client.GracePeriodSeconds(0),
		client.PropagationPolicy(metav1.DeletePropagationBackground),
		client.MatchingLabels{"owner-name": name.String()},
		client.InNamespace(s.workflow.Namespace),
	}
	// No match: the bmc Task CRD was removed, with every Task.
	if err := s.client.DeleteAllOf(ctx, &bmcv1alpha1.Task{}, op...); client.IgnoreNotFound(err) != nil && !meta.IsNoMatchError(err) {
		journal.Log(ctx, "error deleting tasks", "name", name, "error", err)
		return reconcile.Result{}, fmt.Errorf("error deleting tasks.bmc.tinkerbell.org objects: %w", err)
	}

	opts := []client.DeleteOption{
		client.GracePeriodSeconds(0),
		client.PropagationPolicy(metav1.DeletePropagationBackground),
	}
	existingJob := &bmc.Job{ObjectMeta: metav1.ObjectMeta{Name: name.String(), Namespace: s.workflow.Namespace}}
	if err := s.client.Delete(ctx, existingJob, opts...); client.IgnoreNotFound(err) != nil {
		journal.Log(ctx, "error deleting job", "name", name, "error", err)
		return reconcile.Result{}, fmt.Errorf("error deleting job.bmc.tinkerbell.org object: %w", err)
	}

	journal.Log(ctx, "job deleted", "name", name)

	jStatus := s.workflow.Status.BootOptions.Jobs[name.String()]
	jStatus.ExistingJobDeleted = true
	// if we delete an existing job, we need to remove any uid that was set.
	jStatus.UID = ""
	jStatus.Complete = false
	s.workflow.Status.BootOptions.Jobs[name.String()] = jStatus

	return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
}

// This function will update the Workflow status.
func (s *state) createJob(ctx context.Context, hwRef v1alpha2.SimpleReference, ops []bmc.Operations, name jobName) (reconcile.Result, error) {
	// create a new job
	// The assumption is that the UID is not set. UID checking is not handled here.
	// 1. look up if there's an existing job with the same name, if so update the status with the UID and return
	// 2. if there's no existing job, create a new job, update the status with the UID, and return

	rj := &bmc.Job{}
	if err := s.client.Get(ctx, client.ObjectKey{Name: name.String(), Namespace: s.workflow.Namespace}, rj); err == nil {
		journal.Log(ctx, "job already exists", "name", name)
		if !rj.DeletionTimestamp.IsZero() {
			journal.Log(ctx, "job is being deleted", "name", name)
			return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
		}
		// TODO(jacobweinstock): job exists means that the job name and uid from the status are the same.
		// get the UID and update the status
		jStatus := s.workflow.Status.BootOptions.Jobs[name.String()]
		jStatus.UID = rj.GetUID()
		s.workflow.Status.BootOptions.Jobs[name.String()] = jStatus

		return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
	}

	// create a new job
	hw, err := hardwareFrom(ctx, s.client, s.workflow, hwRef)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error getting hardware: %w", err)
	}
	if !hasBMC(hw) {
		return reconcile.Result{}, fmt.Errorf("hardware %q does not have a BMC", hw.Name)
	}

	ownerRef := []metav1.OwnerReference{
		{
			APIVersion: v1alpha2.GroupVersion.String(),
			Kind:       "Workflow",
			Name:       s.workflow.Name,
			UID:        s.workflow.UID,
			Controller: valueToPointer(true),
		},
	}

	if err := create(ctx, s.client, name.String(), hw, s.workflow.Namespace, ops, ownerRef); err != nil {
		return reconcile.Result{}, fmt.Errorf("error creating job: %w", err)
	}
	journal.Log(ctx, "job created", "name", name)

	return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
}

type trackedState string

var (
	trackedStateComplete trackedState = "complete"
	trackedStateRunning  trackedState = "running"
	trackedStateError    trackedState = "error"
	trackedStateFailed   trackedState = "failed"
)

// This function will update the Workflow status.
func (s *state) trackRunningJob(ctx context.Context, name jobName) (reconcile.Result, trackedState, error) {
	// track status
	// get the job
	rj := &bmc.Job{}
	if err := s.client.Get(ctx, client.ObjectKey{Name: name.String(), Namespace: s.workflow.Namespace}, rj); err != nil {
		return reconcile.Result{}, trackedStateError, fmt.Errorf("error getting job: %w", err)
	}
	if bmc.HasConditionStatus(rj.Status.Conditions, bmc.ConditionTypeFailed, bmc.ConditionStatusTrue) {
		journal.Log(ctx, "job failed", "name", name)
		// job failed
		return reconcile.Result{}, trackedStateFailed, fmt.Errorf("job failed")
	}
	if bmc.HasConditionStatus(rj.Status.Conditions, bmc.ConditionTypeCompleted, bmc.ConditionStatusTrue) {
		journal.Log(ctx, "job completed", "name", name)
		// job completed
		jStatus := s.workflow.Status.BootOptions.Jobs[name.String()]
		jStatus.Complete = true
		s.workflow.Status.BootOptions.Jobs[name.String()] = jStatus

		return reconcile.Result{}, trackedStateComplete, nil
	}
	// still running
	journal.Log(ctx, "job still running", "name", name)
	time.Sleep(s.backoff.NextBackOff())
	return reconcile.Result{Requeue: true}, trackedStateRunning, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
}

// create creates the bmc Job name, running ops against the BMC of hw. Hardware created through v1alpha1
// names its BMC by a bmc Machine (bmcRef): such Jobs name the Machine, as v1alpha1 Jobs did, in the
// bmc.tinkerbell.org/machine annotation, so that Rufio, whichever version it reads, uses the same BMC as
// before. Other Hardware holds its BMC connection in spec.bmc, which the Job uses through hardwareRef.
func create(ctx context.Context, cc client.Client, name string, hw *v1alpha2.Hardware, ns string, ops []bmc.Operations, ownerRef []metav1.OwnerReference) error {
	job := &bmc.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				"tink-controller-auto-created": "true",
			},
			Labels: map[string]string{
				"tink-controller-auto-created": "true",
			},
			OwnerReferences: ownerRef,
		},
		Spec: bmc.JobSpec{
			Operations: ops,
		},
	}
	if machine := bmcMachine(hw); machine != "" {
		job.Annotations[conversion.AnnotationMachine] = ns + "/" + machine
	} else {
		job.Spec.HardwareRef = &bmc.SimpleReference{Name: hw.Name, Namespace: hw.Namespace}
	}

	journal.Log(ctx, "creating job", "name", name)
	if err := cc.Create(ctx, job); err != nil {
		return fmt.Errorf("error creating job.bmc.tinkerbell.org object for netbooting machine: %w", err)
	}

	return nil
}

// bmcMachine returns the bmc Machine that holds the BMC connection of Hardware created through v1alpha1
// (its bmcRef), or "".
func bmcMachine(hw *v1alpha2.Hardware) string {
	if ref := conversion.HardwareBMCRef(hw); ref != nil {
		return ref.Name
	}
	return ""
}

// hasBMC reports whether Jobs can run against the BMC of hw.
func hasBMC(hw *v1alpha2.Hardware) bool {
	return bmcMachine(hw) != "" || (hw.Spec.BMC != nil && hw.Spec.BMC.Connection != nil)
}
