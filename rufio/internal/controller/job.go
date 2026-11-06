/*
Copyright 2022 Tinkerbell.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	bmclib "github.com/bmc-toolbox/bmclib/v2"
	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Index key for Job Owner Name.
const jobOwnerKey = ".metadata.controller"

// machineKind is the kind a Hardware's v1alpha1 bmcRef names for a bmc Machine.
const machineKind = "Machine"

// JobReconciler reconciles a Job object.
type JobReconciler struct {
	client client.Client
	// reader reads Jobs from the API server rather than the cache: whether the
	// current operation was already performed is recorded in the Job status, and
	// a stale read must never perform it twice.
	reader           client.Reader
	bmcClientFactory ClientFunc
	nowFunc          func() time.Time
}

// NewJobReconciler returns a new JobReconciler. reader is used to read the Jobs
// themselves; it should not be a cache.
func NewJobReconciler(c client.Client, reader client.Reader, bmcClientFactory ClientFunc) *JobReconciler {
	return &JobReconciler{
		client:           c,
		reader:           reader,
		bmcClientFactory: bmcClientFactory,
		nowFunc:          time.Now,
	}
}

//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=jobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=jobs/finalizers,verbs=update
//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=tasks,verbs=get;list;watch
//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=machines,verbs=get;list;watch
//+kubebuilder:rbac:groups=tinkerbell.org,resources=hardware,verbs=get;list;watch

// Reconcile runs a Job: it performs its operations against the BMC in order,
// one at a time.
//
// The status records the progress: completedOperations counts the operations
// that are done, and while the Job is Running, the Running condition's
// lastUpdateTime is when the next operation (index completedOperations) was
// performed. That operation is then only waited for (a power action until the
// BMC reports the power state it asked for), never performed again.
//
// Jobs created through v1alpha1 that the v1alpha1 Job controller started, by
// creating one bmc Task per action, are taken over: the Tasks that completed
// count as completed operations.
func (r *JobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx).WithName("controllers/Job").WithValues("job", req.NamespacedName)
	logger.Info("Reconciling Job")

	job := &bmc.Job{}
	if err := r.reader.Get(ctx, req.NamespacedName, job); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Job")
		return ctrl.Result{}, err
	}

	// Deletion is a noop.
	if !job.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Job is Completed or Failed is noop.
	if bmc.HasConditionStatus(job.Status.Conditions, bmc.ConditionTypeCompleted, bmc.ConditionStatusTrue) ||
		bmc.HasConditionStatus(job.Status.Conditions, bmc.ConditionTypeFailed, bmc.ConditionStatusTrue) {
		return ctrl.Result{}, nil
	}

	before := job.DeepCopy()
	result, err := r.run(ctx, logger, job)
	if !equality.Semantic.DeepEqual(before.Status, job.Status) {
		if patchErr := r.client.Status().Patch(ctx, job, client.MergeFrom(before)); patchErr != nil {
			// Not terminal, whatever err is: the status must still be written.
			if err != nil {
				logger.Error(err, "Job step failed and its status could not be recorded")
			}
			return ctrl.Result{}, fmt.Errorf("failed to patch Job %s/%s status: %w", job.Namespace, job.Name, patchErr)
		}
	}

	return result, err
}

// run advances job and updates its status in place.
func (r *JobReconciler) run(ctx context.Context, logger logr.Logger, job *bmc.Job) (ctrl.Result, error) {
	ops := job.Spec.Operations
	completed := int(job.Status.CompletedOperations)
	performedAt, performed := r.performedAt(job)

	if !performed {
		wait, err := r.takeOverV1Alpha1Tasks(ctx, job)
		if err != nil || wait {
			return ctrl.Result{}, err
		}
		completed = int(job.Status.CompletedOperations)
		if job.Status.StartTime == nil {
			now := metav1.NewTime(r.nowFunc())
			job.Status.StartTime = &now
		}
	}

	if completed >= len(ops) {
		r.complete(job)
		return ctrl.Result{}, nil
	}

	// An operation that was performed and is not done yet fails once it runs
	// longer than its timeout.
	if performed {
		if running := r.nowFunc().Sub(performedAt); running >= operationTimeout(ops[completed]) {
			err := fmt.Errorf("operation %d of %d (%s) timed out after %s", completed+1, len(ops), describeOperation(ops[completed]), running.Round(time.Second))
			r.setCondition(job, bmc.ConditionTypeFailed, err.Error())
			return ctrl.Result{}, reconcile.TerminalError(err)
		}
	}

	conn, err := r.connection(ctx, job)
	if err != nil {
		if errors.Is(err, errNoBMC) {
			r.setCondition(job, bmc.ConditionTypeFailed, err.Error())
			return ctrl.Result{}, reconcile.TerminalError(err)
		}
		// Requeue: the Hardware, Machine or Secret may not exist yet.
		return ctrl.Result{}, fmt.Errorf("get Job %s/%s BMC connection: %w", job.Namespace, job.Name, err)
	}
	username, password, opts, err := bmcCredentials(ctx, r.client, conn)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolving connection secret for Job %s/%s: %w", job.Namespace, job.Name, err)
	}
	logger = logger.WithValues("host", conn.Host)

	bmcClient, err := r.bmcClientFactory(ctx, logger, conn.Host, username, password, opts)
	if err != nil {
		if performed {
			// BMCs are routinely slow or unreachable for a few seconds right after a
			// power action. Only fail once the operation times out.
			logger.V(1).Info("BMC connection failed while checking a performed operation, will retry", "error", err.Error(), "requeueAfter", powerActionRequeueAfter)
			return ctrl.Result{RequeueAfter: powerActionRequeueAfter}, nil
		}
		logger.Error(err, "BMC connection failed")
		r.setCondition(job, bmc.ConditionTypeFailed, fmt.Sprintf("Failed to connect to BMC: %v", err))
		return ctrl.Result{}, reconcile.TerminalError(err)
	}
	defer closeBMC(ctx, logger, bmcClient)

	for completed < len(ops) {
		op := ops[completed]
		oplog := logger.WithValues("operation", completed+1, "action", describeOperation(op))
		if !performed {
			oplog.Info("performing operation")
			opCtx, cancel := context.WithTimeout(ctx, operationTimeout(op))
			err := runOperation(opCtx, oplog, op, bmcClient)
			cancel()
			if err != nil {
				md := bmcClient.GetMetadata()
				oplog.Info("failed to perform operation", "providersAttempted", md.ProvidersAttempted, "error", err.Error())
				err = fmt.Errorf("operation %d of %d (%s) failed: %w", completed+1, len(ops), describeOperation(op), err)
				r.setCondition(job, bmc.ConditionTypeFailed, err.Error())
				return ctrl.Result{}, reconcile.TerminalError(err)
			}
			r.setCondition(job, bmc.ConditionTypeRunning, fmt.Sprintf("operation %d of %d: %s", completed+1, len(ops), describeOperation(op)))
		}

		done, err := operationDone(ctx, oplog, op, bmcClient)
		if err != nil {
			oplog.Info("checking the operation failed, will retry", "error", err.Error(), "requeueAfter", powerActionRequeueAfter)
			return ctrl.Result{RequeueAfter: powerActionRequeueAfter}, nil
		}
		if !done {
			oplog.Info("operation not done yet", "requeueAfter", powerActionRequeueAfter)
			return ctrl.Result{RequeueAfter: powerActionRequeueAfter}, nil
		}
		completed++
		job.Status.CompletedOperations = int32(completed) //nolint:gosec // bounded by the number of operations.
		performed = false
	}

	r.complete(job)
	return ctrl.Result{}, nil
}

// performedAt returns when the operation the Job is at was performed, if it was.
// That is the Running condition's lastUpdateTime: it is only recorded, and
// changed, when an operation is performed. Jobs the v1alpha1 Job controller
// started have no such time.
func (r *JobReconciler) performedAt(job *bmc.Job) (time.Time, bool) {
	for _, c := range job.Status.Conditions {
		if c.Type == bmc.ConditionTypeRunning && c.Status == bmc.ConditionStatusTrue && !c.LastUpdateTime.IsZero() {
			return c.LastUpdateTime.Time, true
		}
	}
	return time.Time{}, false
}

// complete marks the Job completed. Like a v1alpha1 Job, it keeps its Running
// condition.
func (r *JobReconciler) complete(job *bmc.Job) {
	if !bmc.HasConditionStatus(job.Status.Conditions, bmc.ConditionTypeRunning, bmc.ConditionStatusTrue) {
		r.setCondition(job, bmc.ConditionTypeRunning, "")
	}
	r.setCondition(job, bmc.ConditionTypeCompleted, "")
	now := metav1.NewTime(r.nowFunc())
	job.Status.CompletionTime = &now
}

// setCondition sets the condition t of the Job to True. A Job's conditions
// never become False: a v1alpha1 Job's never did.
func (r *JobReconciler) setCondition(job *bmc.Job, t bmc.ConditionType, message string) {
	job.Status.Conditions = bmc.SetCondition(job.Status.Conditions, bmc.Condition{
		Type:               t,
		Status:             bmc.ConditionStatusTrue,
		Message:            message,
		LastUpdateTime:     metav1.NewTime(r.nowFunc()),
		ObservedGeneration: job.Generation,
	})
}

// takeOverV1Alpha1Tasks continues a Job that the v1alpha1 Job controller started:
// for each action, it created a bmc Task owned by the Job, one after the other.
// The completed Tasks count as completed operations (an action is one
// operation). It reports whether to wait for a Task that is still running; the
// Task controller finishes it. A failed Task fails the Job.
func (r *JobReconciler) takeOverV1Alpha1Tasks(ctx context.Context, job *bmc.Job) (bool, error) {
	tasks := &bmcv1.TaskList{}
	if err := r.client.List(ctx, tasks, client.MatchingFields{jobOwnerKey: job.Name}, client.InNamespace(job.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list owned Tasks for Job %s/%s: %w", job.Namespace, job.Name, err)
	}

	completed := 0
	for _, task := range tasks.Items {
		switch {
		case task.HasCondition(bmcv1.TaskFailed, bmcv1.ConditionTrue):
			err := fmt.Errorf("task %s/%s failed", task.Namespace, task.Name)
			r.setCondition(job, bmc.ConditionTypeFailed, err.Error())
			return false, reconcile.TerminalError(err)
		case task.HasCondition(bmcv1.TaskCompleted, bmcv1.ConditionTrue):
			completed++
		default:
			return true, nil
		}
	}
	if completed > int(job.Status.CompletedOperations) {
		job.Status.CompletedOperations = int32(min(completed, len(job.Spec.Operations)))
	}

	return false, nil
}

// errNoBMC is returned for Jobs that name no BMC at all.
var errNoBMC = errors.New("job has no connection, hardwareRef or machine")

// connection returns the BMC connection of the Job, in order: its own
// connection; the BMC connection of the Hardware it refers to; for Jobs created
// through v1alpha1, the connection of the bmc Machine they named.
func (r *JobReconciler) connection(ctx context.Context, job *bmc.Job) (*bmc.Connection, error) {
	if job.Spec.Connection != nil {
		return job.Spec.Connection, nil
	}

	if ref := job.Spec.HardwareRef; ref != nil && ref.Name != "" {
		hw := &v1alpha2.Hardware{}
		if err := r.client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hw); err != nil {
			return nil, fmt.Errorf("failed to get Hardware %s/%s: %w", ref.Namespace, ref.Name, err)
		}
		if hw.Spec.BMC != nil && hw.Spec.BMC.Connection != nil {
			return hw.Spec.BMC.Connection, nil
		}
		// Hardware created through v1alpha1 whose Machine is not translated yet.
		if ref := conversion.HardwareBMCRef(hw); ref != nil && ref.Kind == machineKind {
			return machineConnection(ctx, r.client, hw.Namespace, ref.Name)
		}
		return nil, fmt.Errorf("hardware %s/%s has no BMC connection", hw.Namespace, hw.Name)
	}

	if ns, name, ok := strings.Cut(job.Annotations[conversion.AnnotationMachine], "/"); ok {
		return machineConnection(ctx, r.client, ns, name)
	}

	return nil, errNoBMC
}

// machineConnection returns the connection of a bmc Machine in its v1alpha2 form.
func machineConnection(ctx context.Context, c client.Client, namespace, name string) (*bmc.Connection, error) {
	machine := &bmcv1.Machine{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, machine); err != nil {
		return nil, fmt.Errorf("failed to get Machine %s/%s: %w", namespace, name, err)
	}
	return conversion.MachineConnectionToV1Alpha2(machine.Spec.Connection)
}

// closeBMC closes a BMC session, logging the outcome.
func closeBMC(ctx context.Context, logger logr.Logger, bmcClient *bmclib.Client) {
	if err := bmcClient.Close(ctx); err != nil {
		md := bmcClient.GetMetadata()
		logger.Error(err, "BMC close connection failed", "providersAttempted", md.ProvidersAttempted)
		return
	}
	md := bmcClient.GetMetadata()
	logger.Info("BMC connection closed", "successfulCloseConns", md.SuccessfulCloseConns, "providersAttempted", md.ProvidersAttempted, "successfulProvider", md.SuccessfulProvider)
}

// SetupWithManager sets up the controller with the Manager.
//
// Only changes to a Job's spec start a reconcile: its status is written here and
// the next step of a running Job is requeued explicitly, so a status update does
// not cause another round of BMC calls. Tasks of Jobs the v1alpha1 Job
// controller started are watched so those Jobs continue when their Task ends.
func (r *JobReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ctrlcontroller.Options) error {
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&bmcv1.Task{},
		jobOwnerKey,
		TaskOwnerIndexFunc,
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(opts).
		For(&bmc.Job{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&bmcv1.Task{},
			handler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&bmc.Job{},
				handler.OnlyControllerOwner(),
			),
		).
		Complete(r)
}

// TaskOwnerIndexFunc is Indexer func which returns the owner name for obj.
func TaskOwnerIndexFunc(obj client.Object) []string {
	task, ok := obj.(*bmcv1.Task)
	if !ok {
		return nil
	}

	owner := metav1.GetControllerOf(task)
	if owner == nil {
		return nil
	}

	// Check if owner is Job
	if owner.Kind != "Job" || owner.APIVersion != bmcv1.GroupVersion.String() {
		return nil
	}

	return []string{owner.Name}
}
