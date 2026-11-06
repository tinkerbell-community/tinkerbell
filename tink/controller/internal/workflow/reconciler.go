package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/go-logr/logr"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// templateDataReferences is the key used to access the Hardware references in the template data.
	// This is lowercase as it is new and follows the all lowercase convention used when referencing
	// fields in the reference object.
	templateDataReferences = "references"
	// templateDataHardware is the key used to access the Hardware data in the template data.
	templateDataHardware = "hardware"
	// templateDataHardwareLegacy is the key used to access the Hardware data in the template data.
	// This is Title cased as it was the original convention used in the template data and is
	// used for backwards compatibility.
	//
	// Deprecated: use templateDataHardware instead. This key will be removed in a future release.
	templateDataHardwareLegacy = "Hardware"
	templateDataMetadataURL    = "metadata_url"
	// templateDataWorkflow and templateDataTask are the keys under which a Task's
	// templates see the Workflow running it and the Task itself.
	templateDataWorkflow = "workflow"
	templateDataTask     = "task"

	// reasonError is the condition Reason set when a workflow step fails.
	reasonError = "Error"
)

// backend renders Hardware and resolves references, as the kube backend does.
type backend interface {
	// RenderedHardware returns the v1alpha1 rendering of a Hardware, for
	// Workflows run in v1alpha1 compatibility mode.
	RenderedHardware(ctx context.Context, hw *v1alpha1.Hardware) (*v1alpha1.Hardware, error)
	RenderedHardwareV1Alpha2(ctx context.Context, hw *v1alpha2.Hardware) (*v1alpha2.Hardware, error)
	ResolveReferencesFor(ctx context.Context, src types.NamespacedName, refs map[string]v1alpha2.Reference) (map[string]any, error)
}

// Reconciler is a type for managing Workflows.
type Reconciler struct {
	client      ctrlclient.Client
	nowFunc     func() time.Time
	backoff     *backoff.ExponentialBackOff
	references  backend
	metadataURL string
	recorder    events.EventRecorder
	// compatOnly keeps Workflows created through v1alpha1 in v1alpha1 compatibility mode instead of
	// rewriting them onto Tasks. Tests of compatibility mode set it.
	compatOnly bool
}

type Option func(*Reconciler)

func WithMetadataURL(u string) Option {
	return func(r *Reconciler) {
		r.metadataURL = u
	}
}

// WithEventRecorder sets the recorder of the events the reconciler emits.
func WithEventRecorder(rec events.EventRecorder) Option {
	return func(r *Reconciler) {
		r.recorder = rec
	}
}

// TODO(jacobweinstock): add functional arguments to the signature.
// TODO(jacobweinstock): write functional argument for customizing the backoff.
func NewReconciler(client ctrlclient.Client, references backend, opts ...Option) *Reconciler {
	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 5 * time.Second // this should keep all NextBackOff's under 10 seconds
	r := &Reconciler{
		client:     client,
		nowFunc:    time.Now,
		backoff:    bo,
		references: references,
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *Reconciler) SetupWithManager(mgr manager.Manager, opts controller.Options) error {
	return ctrl.
		NewControllerManagedBy(mgr).
		WithOptions(opts).
		For(&v1alpha2.Workflow{}).
		Owns(&bmcv1alpha2.Job{}).
		Complete(r)
}

type state struct {
	client   ctrlclient.Client
	workflow *v1alpha2.Workflow
	backoff  *backoff.ExponentialBackOff
	// render returns the Workflow with its templates rendered for task j,
	// for boot options that are templated.
	render func(ctx context.Context, j int) (*v1alpha2.Workflow, error)
}

func (r *Reconciler) newState(wf *v1alpha2.Workflow) *state {
	return &state{
		client:   r.client,
		workflow: wf,
		backoff:  r.backoff,
		render: func(ctx context.Context, j int) (*v1alpha2.Workflow, error) {
			return r.renderedWorkflowFor(ctx, wf, j)
		},
	}
}

// +kubebuilder:rbac:groups=tinkerbell.org,resources=hardware;hardware/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=tinkerbell.org,resources=templates;templates/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=tinkerbell.org,resources=tasks,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=tinkerbell.org,resources=workflows;workflows/status,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=job;job/status,verbs=get;list;watch;delete;create

// Reconcile handles Workflow objects. This includes Task rendering, optional Hardware netboot
// toggling, and optional Hardware one-time netbooting, ISO booting and custom boot operations.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctx = journal.New(ctx)
	logger := ctrl.LoggerFrom(ctx)
	defer func() {
		logger.V(1).Info("Reconcile code flow journal", "journal", journal.Journal(ctx))
	}()
	logger.Info("Reconcile")
	journal.Log(ctx, "starting reconcile")

	stored := &v1alpha2.Workflow{}
	if err := r.client.Get(ctx, req.NamespacedName, stored); err != nil {
		if kerrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !stored.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}
	if stored.Status.BootOptions.Jobs == nil {
		stored.Status.BootOptions.Jobs = make(map[string]v1alpha2.JobStatus)
	}

	wflow := stored.DeepCopy()

	switch wflow.Status.Metadata.Workflow.State {
	case 0:
		journal.Log(ctx, "new workflow")
		// A Workflow created through v1alpha1 runs its Template's Tasks when the Template translates.
		if rewritten, err := r.rewriteToTasks(ctx, stored); err != nil || rewritten {
			return reconcile.Result{}, err
		}
		resp, err := r.processNewWorkflow(ctx, logger, wflow)
		// If the Workflow spec is disabled, just set the AgentID and return.
		// The Agent ID is used as an index in the Tink Server backend, so it needs to be set even when the Workflow is disabled.
		if wflow.Spec.Disabled != nil && *wflow.Spec.Disabled {
			journal.Log(ctx, "workflow disabled")
			wflow2 := stored.DeepCopy()
			wflow2.Status.Metadata.Task.AgentID = wflow.Status.Metadata.Task.AgentID
			return reconcile.Result{}, mergePatchStatus(ctx, r.client, stored, wflow2)
		}

		return resp, errors.Join(err, mergePatchStatus(ctx, r.client, stored, wflow))
	case v1alpha2.StatePreparing:
		journal.Log(ctx, "preparing workflow")
		s := r.newState(wflow)
		resp, err := s.prepareWorkflow(ctx)

		return resp, errors.Join(err, mergePatchStatus(ctx, r.client, stored, s.workflow))
	case v1alpha2.StateRunning:
		journal.Log(ctx, "process running workflow")
		return r.processRunningWorkflow(ctx, stored, wflow)
	case v1alpha2.StatePost:
		journal.Log(ctx, "post actions")
		s := r.newState(wflow)
		rc, err := s.postActions(ctx)

		return rc, errors.Join(err, mergePatchStatus(ctx, r.client, stored, wflow))
	case v1alpha2.StatePending, v1alpha2.StateTimeout, v1alpha2.StateFailed, v1alpha2.StateSuccess:
		journal.Log(ctx, "controller will not trigger another reconcile", "state", wflow.Status.Metadata.Workflow.State)

		return reconcile.Result{}, nil
	default:
		journal.Log(ctx, "controller will not trigger reconcile, unknown state", "state", wflow.Status.Metadata.Workflow.State)
	}

	return reconcile.Result{}, nil
}

// processRunningWorkflow enforces the Workflow's timeouts and keeps its current
// Agent on the task that runs next.
func (r *Reconciler) processRunningWorkflow(ctx context.Context, stored, wflow *v1alpha2.Workflow) (reconcile.Result, error) {
	now := r.nowFunc()
	// Check if the global timeout has been reached.
	if wflow.Status.GlobalExecutionStop != nil && now.After(wflow.Status.GlobalExecutionStop.Time) {
		journal.Log(ctx, "global timeout reached")
		wflow.Status.Metadata.Workflow.State = v1alpha2.StateTimeout
		wflow.Status.Metadata.Workflow.Message = fmt.Sprintf("workflow timed out after %ds", wflow.Status.GlobalTimeout)
		return reconcile.Result{}, mergePatchStatus(ctx, r.client, stored, wflow)
	}

	var result reconcile.Result
	if timedOut, after := taskTimeout(wflow, now); timedOut {
		journal.Log(ctx, "task timeout reached")
		return reconcile.Result{}, mergePatchStatus(ctx, r.client, stored, wflow)
	} else if after > 0 {
		result.RequeueAfter = after
	}

	// Update AgentID if transitioning between tasks
	if updateAgentIDIfNeeded(wflow) {
		journal.Log(ctx, "updated workflow AgentID for task transition", "newAgentID", wflow.Status.Metadata.Task.AgentID)
	}

	first := firstAction(wflow)
	if wflow.Status.GlobalExecutionStop == nil && first != nil && wflow.Status.Metadata.Action.ID != "" && first.Metadata.ID == wflow.Status.Metadata.Action.ID {
		if first.Metadata.StartTime == nil {
			return result, mergePatchStatus(ctx, r.client, stored, wflow)
		}
		// v1alpha2 has no timeout for a zero timeout; v1alpha1 Workflows keep theirs.
		if wflow.Status.GlobalTimeout > 0 || engine.Compat(wflow) {
			var skew time.Duration
			if now.After(first.Metadata.StartTime.Time) {
				skew = now.Sub(first.Metadata.StartTime.Time).Abs()
			}
			wflow.Status.GlobalExecutionStop = &metav1.Time{
				Time: now.Add(time.Duration(wflow.Status.GlobalTimeout) * time.Second).Add(skew),
			}
			journal.Log(ctx, "global execution times set")
			if until := time.Until(wflow.Status.GlobalExecutionStop.Time); result.RequeueAfter == 0 || until < result.RequeueAfter {
				result.RequeueAfter = until
			}
		}
	}

	return result, mergePatchStatus(ctx, r.client, stored, wflow)
}

// taskTimeout enforces the timeoutSeconds of the current task of a native
// Workflow. A task's time starts when the controller first sees one of its
// actions started, so it is measured with one clock. It reports whether the
// task timed out, or else how long until it does.
func taskTimeout(wf *v1alpha2.Workflow, now time.Time) (bool, time.Duration) {
	if engine.Compat(wf) {
		return false, 0
	}
	j := engine.TaskIndex(wf, wf.Status.Metadata.Task.ID)
	if j < 0 || j >= len(wf.Spec.Tasks) {
		return false, 0
	}
	timeout := pointerToValue(wf.Spec.Tasks[j].TimeoutSeconds)
	if timeout <= 0 {
		return false, 0
	}
	rt := &wf.Status.RenderedTasks[j]
	if rt.Metadata.StartTime == nil {
		for _, a := range rt.Actions {
			if a.Metadata.StartTime != nil {
				rt.Metadata.StartTime = &metav1.Time{Time: now}
				return false, time.Duration(timeout) * time.Second
			}
		}
		return false, 0
	}
	deadline := rt.Metadata.StartTime.Add(time.Duration(timeout) * time.Second)
	if !now.After(deadline) {
		return false, deadline.Sub(now)
	}
	rt.Metadata.State = v1alpha2.StateTimeout
	wf.Status.Metadata.Task.State = v1alpha2.StateTimeout
	wf.Status.Metadata.Workflow.State = v1alpha2.StateTimeout
	wf.Status.Metadata.Workflow.Message = fmt.Sprintf("task %s timed out after %ds", rt.Name, timeout)
	return true, 0
}

// mergePatchStatus merges an updated Workflow with an original Workflow and patches the Status object via the client (cc).
func mergePatchStatus(ctx context.Context, cc ctrlclient.Client, original, updated *v1alpha2.Workflow) error {
	// Patch any changes, regardless of errors
	if !equality.Semantic.DeepEqual(updated.Status, original.Status) {
		journal.Log(ctx, "patching status")
		patch := ctrlclient.MergeFromWithOptions(original, ctrlclient.MergeFromWithOptimisticLock{})
		if err := cc.Status().Patch(ctx, updated, patch); err != nil {
			return fmt.Errorf("error patching status of workflow: %s, error: %w", updated.Name, err)
		}
	}
	return nil
}

// processWorkflow renders the Workflow's tasks into its status.
func (r *Reconciler) processWorkflow(ctx context.Context, logger logr.Logger, stored *v1alpha2.Workflow) error {
	if engine.Compat(stored) {
		return r.renderCompat(ctx, logger, stored)
	}
	return r.renderNative(ctx, stored)
}

func (r *Reconciler) processNewWorkflow(ctx context.Context, logger logr.Logger, stored *v1alpha2.Workflow) (reconcile.Result, error) {
	if err := r.processWorkflow(ctx, logger, stored); err != nil {
		return reconcile.Result{}, err
	}

	first := engine.NextTask(stored, -1)
	if first < 0 && !engine.Compat(stored) {
		// Every task's actions were skipped: there is nothing to run.
		stored.Status.Metadata.Workflow.State = v1alpha2.StateSuccess
		return reconcile.Result{}, nil
	}

	// Run the boot options of the first task, if it has any.
	if engine.BootBefore(stored, max(first, 0)).Pre() {
		stored.Status.Metadata.Workflow.State = v1alpha2.StatePreparing
		return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue keeps rate-limited backoff; RequeueAfter would change behavior.
	}

	stored.Status.Metadata.Workflow.State = v1alpha2.StatePending

	return reconcile.Result{}, nil
}

// templateHardwareData defines the data exposed for a Hardware instance to a Template.
type templateHardwareData struct {
	Disks      []string
	Interfaces []v1alpha1.Interface
	UserData   string
	Metadata   v1alpha1.HardwareMetadata
	VendorData string
}

// toTemplateHardwareData converts a Hardware instance of templateHardwareData for use in template
// rendering.
func toTemplateHardwareData(hardware v1alpha1.Hardware) templateHardwareData {
	var contract templateHardwareData
	for _, disk := range hardware.Spec.Disks {
		contract.Disks = append(contract.Disks, disk.Device)
	}
	if len(hardware.Spec.Interfaces) > 0 {
		contract.Interfaces = hardware.Spec.Interfaces
	}
	if hardware.Spec.UserData != nil {
		contract.UserData = pointerToValue(hardware.Spec.UserData)
	}
	if hardware.Spec.Metadata != nil {
		contract.Metadata = *hardware.Spec.Metadata
	}
	if hardware.Spec.VendorData != nil {
		contract.VendorData = pointerToValue(hardware.Spec.VendorData)
	}
	return contract
}

func pointerToValue[V any](ptr *V) V {
	if ptr == nil {
		var zero V
		return zero
	}
	return *ptr
}

// firstAction returns the first Action of the Workflow: that of its first Task with Actions. A native
// Task's Actions can all be skipped.
func firstAction(w *v1alpha2.Workflow) *v1alpha2.ActionWithMetadata {
	for i := range w.Status.RenderedTasks {
		if len(w.Status.RenderedTasks[i].Actions) > 0 {
			return &w.Status.RenderedTasks[i].Actions[0]
		}
	}
	return nil
}

// updateAgentIDIfNeeded makes the next task's Agent the Workflow's current Agent once the current task is
// complete. The Tink server does this itself when it records the last Action of a task; this covers
// Workflows that were between tasks when they were moved from v1alpha1, whose current Agent is still
// the Agent of the finished task.
func updateAgentIDIfNeeded(wf *v1alpha2.Workflow) bool {
	// Find the current task index
	currentTaskIndex := engine.TaskIndex(wf, wf.Status.Metadata.Task.ID)

	// If we can't find the current task, nothing to do
	if currentTaskIndex == -1 {
		return false
	}

	// Step 1: Check for invalid index (>) or if we're in the last task (==)
	if currentTaskIndex >= len(wf.Status.RenderedTasks)-1 {
		return false
	}

	currentTask := wf.Status.RenderedTasks[currentTaskIndex]
	// Step 2: Check if the current task is complete
	// A task is complete when all its actions are in SUCCESS state
	for _, action := range currentTask.Actions {
		if action.Metadata.State != v1alpha2.StateSuccess {
			return false // Current task is not complete
		}
	}

	nextTask := wf.Status.RenderedTasks[currentTaskIndex+1]
	// Step 3: Check if the next task's first action is pending
	if len(nextTask.Actions) == 0 {
		return false // Next task has no actions
	}

	if nextTask.Actions[0].Metadata.State != v1alpha2.StatePending {
		return false // Next task's first action is not pending
	}

	// Step 4: Check if the current AgentID is not equal to the next task's agent ID
	if wf.Status.Metadata.Task.AgentID == nextTask.Metadata.AgentID {
		return false // AgentID is already correct
	}

	// All conditions met, update the current Agent to the next task's Agent
	wf.Status.Metadata.Task.AgentID = nextTask.Metadata.AgentID
	return true // Indicates that an update was made
}
