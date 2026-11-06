package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/inventory"
	"github.com/tinkerbell/tinkerbell/pkg/constant"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"github.com/tinkerbell/tinkerbell/pkg/proto"
	"github.com/tinkerbell/tinkerbell/tink/internal/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	errInvalidWorkflowID = "invalid workflow id"
	errInvalidTaskName   = "invalid task name"
	errInvalidActionName = "invalid action name"

	// maxAnnotationSize is the maximum allowed size for agent attributes annotations.
	maxAnnotationSize = 64 * 1024 // 64KB
)

var (
	ErrBackendRead  = errors.New("error reading from backend")
	ErrBackendWrite = errors.New("error writing to backend")
)

type Backend interface {
	WorkflowReader
	WorkflowUpdater
	WorkflowLister
	HardwareReader
	HardwareFilterer
	HardwareUpdater
	HardwareInBandAttributesApplier
}

type WorkflowCreator interface {
	CreateWorkflowV1Alpha2(ctx context.Context, wf *v1alpha2.Workflow) error
}

type WorkflowReader interface {
	ReadWorkflowV1Alpha2(ctx context.Context, name, namespace string) (*v1alpha2.Workflow, error)
}

type WorkflowLister interface {
	ListWorkflowsV1Alpha2(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha2.Workflow, error)
}

type WorkflowUpdater interface {
	UpdateWorkflowV1Alpha2(ctx context.Context, wf *v1alpha2.Workflow, opts data.UpdateOptions) error
}

// WorkflowRuleSetLister lists the deprecated v1alpha1 WorkflowRuleSets, which only exist in v1alpha1.
type WorkflowRuleSetLister interface {
	ListWorkflowRuleSets(ctx context.Context, opts data.WorkflowFilter) ([]v1alpha1.WorkflowRuleSet, error)
}

type HardwareReader interface {
	ReadHardwareV1Alpha2(ctx context.Context, name, namespace string) (*v1alpha2.Hardware, error)
}

type HardwareFilterer interface {
	FilterHardwareV1Alpha2(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error)
}

type HardwareUpdater interface {
	UpdateHardwareV1Alpha2(ctx context.Context, hw *v1alpha2.Hardware, opts data.UpdateOptions) error
}

type HardwareInBandAttributesApplier interface {
	ApplyHardwareInBandAttributesV1Alpha2(ctx context.Context, name, namespace string, attrs *inventory.Attributes) error
}

type HardwareCreator interface {
	CreateHardwareV1Alpha2(ctx context.Context, hw *v1alpha2.Hardware) error
}

// Handler is a server that implements a workflow API.
//
// Each request is a single attempt; the returned gRPC status code tells the client whether
// and when to retry. See docs/technical/designs/AGENT_SERVER_RETRY.md.
type Handler struct {
	Logger           logr.Logger
	Backend          Backend
	NowFunc          func() time.Time
	AutoCapabilities AutoCapabilities

	proto.UnimplementedWorkflowServiceServer
}

type Option func(*Handler)

func NewHandler(opts ...Option) *Handler {
	h := &Handler{
		NowFunc: time.Now,
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

func (h *Handler) GetAction(ctx context.Context, req *proto.ActionRequest) (*proto.ActionResponse, error) {
	select {
	case <-ctx.Done():
		return nil, status.Error(codes.Unavailable, "server shutting down")
	default:
	}

	ctx = journal.New(ctx)
	log := h.Logger.WithValues("agent", req.GetAgentId())
	defer func() {
		log.V(1).Info("GetAction code flow journal", "journal", journal.Journal(ctx))
	}()
	if req.GetAgentId() == "" {
		journal.Log(ctx, "invalid Agent ID")
		return nil, status.Errorf(codes.InvalidArgument, "invalid Agent ID")
	}

	attrs := convert(req.GetAgentAttributes())

	// hwRef is used in auto discovery and enrollment to avoid multiple lookups of the Hardware object.
	var hwRef *v1alpha2.Hardware
	var hwErr error
	// handle auto discovery
	if h.AutoCapabilities.Discovery.Enabled {
		journal.Log(ctx, "auto discovery triggered")
		// Check if there is an existing Hardware Object.
		// If not, Discovery creates one.
		hw, err := h.Discover(ctx, req.GetAgentId(), attrs)
		if err != nil {
			journal.Log(ctx, "error auto discovering Hardware", "error", err)
			log.Error(err, "error auto discovering Hardware")
			hw = hwRef
			hwErr = err
			// We don't return the error here as we don't want to disrupt any Workflows from running.
		}
		hwRef = hw
	}

	wfs, err := h.Backend.ListWorkflowsV1Alpha2(ctx, data.WorkflowFilter{ByAgentID: req.GetAgentId()})
	if err != nil {
		// TODO: This is where we handle auto capabilities
		journal.Log(ctx, "error getting Workflows", "error", err)
		return nil, errors.Join(ErrBackendRead, status.Errorf(codes.Internal, "error getting workflows: %v", err))
	}
	if len(wfs) == 0 {
		if h.AutoCapabilities.Enrollment.Enabled {
			// A Workflow that will run on this Agent but has not reached it yet is not a reason to enroll it.
			if err := h.workflowAwaitsAgent(ctx, req.GetAgentId()); err != nil {
				return nil, err
			}
			journal.Log(ctx, "auto enrollment triggered")
			// If auto discovery is disabled, we do a Hardware object lookup.
			// If auto discovery is enabled, we rely on the lookup and/or creation of a Hardware object from the Discover method.
			// This means that only one Hardware lookup call is every made to the backend.
			if !h.AutoCapabilities.Discovery.Enabled {
				hwRef, hwErr = h.hardware(ctx, req.GetAgentId())
			}
			// Enrolling as if there were no Hardware would bypass its enrollmentEnabled and namespace.
			if err := hardwareLookupError(ctx, log, req.GetAgentId(), hwErr); err != nil {
				return nil, err
			}
			return nil, h.enroll(ctx, req.GetAgentId(), attrs, hwRef)
		}
		journal.Log(ctx, "no Workflow found")
		return nil, status.Error(codes.NotFound, "no Workflows found")
	}

	journal.Log(ctx, "found Workflows", "workflows", len(wfs))
	var wf v1alpha2.Workflow
	// Scan every Workflow for this agent instead of returning on the first
	// ineligible one. Terminal Workflows keep their current Agent and so remain
	// in the agent-ID index indefinitely; returning early would let one of them
	// permanently shadow a later pending/running Workflow assigned to the same
	// agent, so its Action would never be served.
	var found, preparing, notRunning bool
	for _, w := range wfs {
		if len(w.Status.RenderedTasks) == 0 {
			continue
		}
		// Don't serve Actions when in a Preparing state.
		// This is to prevent the Agent from starting Actions before Workflow boot options are performed.
		if w.Status.Metadata.Workflow.State == v1alpha2.StatePreparing {
			journal.Log(ctx, "Workflow is in preparing state")
			preparing = true
			continue
		}
		if w.Status.Metadata.Workflow.State != v1alpha2.StatePending && w.Status.Metadata.Workflow.State != v1alpha2.StateRunning {
			journal.Log(ctx, "Workflow not in pending or running state")
			notRunning = true
			continue
		}
		wf = w
		found = true
		journal.Log(ctx, "found Workflow", "workflow", wf.Name)
		break
	}
	if !found {
		switch {
		case preparing:
			return nil, status.Error(codes.FailedPrecondition, "Workflow is in preparing state")
		case notRunning:
			return nil, status.Error(codes.FailedPrecondition, "Workflow not in pending or running state")
		default:
			journal.Log(ctx, "no Tasks found in Workflow")
			return nil, status.Error(codes.NotFound, "no Tasks found in Workflow")
		}
	}

	tasks := wf.Status.RenderedTasks
	ti := -1
	if first := firstTaskWithActions(tasks); isFirstAction(tasks[first]) {
		ti = first
		journal.Log(ctx, "first Task, first Action")
		hwRef = h.resolveAndAnnotateHardware(ctx, log, hwRef, engine.HardwareRef(&wf, ti), attrs)
	} else {
		for i := range tasks {
			// check if all actions have been run successfully in this task.
			// if so continue to the next task.
			if isTaskSuccessful(tasks[i]) {
				continue
			}
			ti = i
			journal.Log(ctx, "found Task", "taskID", tasks[i].Metadata.ID)
			break
		}
		if ti < 0 {
			journal.Log(ctx, "no Tasks found")
			return nil, status.Error(codes.NotFound, "no Tasks found")
		}
	}
	task := &tasks[ti]

	if len(task.Actions) == 0 {
		journal.Log(ctx, "no Actions found")
		return nil, status.Error(codes.NotFound, "no Actions found")
	}

	action, err := resolveAction(ctx, task, &wf.Status.Metadata)
	if err != nil {
		return nil, err
	}
	// This check goes after the action is found, so that multi task Workflows can be handled.
	if task.Metadata.AgentID != req.GetAgentId() {
		journal.Log(ctx, "Task not assigned to Agent")
		return nil, status.Error(codes.NotFound, "Task not assigned to Agent")
	}

	// Applied independently of the legacy annotation above, and on whichever Task's first Action is
	// currently being served (not just the Workflow's overall first Task): the Agent handling the
	// Workflow's first Task is often an administrative Agent (e.g. for Netbox/network setup), not the
	// Agent running on the target Hardware itself. Deferred until after the Task-assignment check above
	// so an unassigned/spoofed Agent can't get its attributes written to Hardware status.
	if isFirstAction(*task) {
		h.resolveAndApplyInBandAttributes(ctx, log, hwRef, engine.HardwareRef(&wf, ti), req.GetAgentId(), attrs)
	}

	// update the current state
	// populate the current state and then send the action to the client.
	wf.Status.Metadata.Task = v1alpha2.Metadata{
		AgentID:  req.GetAgentId(),
		ID:       task.Metadata.ID,
		Name:     task.Name,
		Hardware: task.Metadata.Hardware,
		State:    task.Metadata.State,
	}
	wf.Status.Metadata.Action = v1alpha2.Metadata{
		ID:    action.Metadata.ID,
		Name:  action.Name,
		State: action.Metadata.State,
	}

	if err := h.Backend.UpdateWorkflowV1Alpha2(ctx, &wf, data.UpdateOptions{StatusOnly: true}); err != nil {
		if apierrors.IsConflict(err) {
			return nil, status.Errorf(codes.Aborted, "error writing current state: %v", err)
		}
		return nil, errors.Join(ErrBackendWrite, status.Errorf(codes.Internal, "error writing current state: %v", err))
	}

	ar := &proto.ActionResponse{
		WorkflowId: toPtr(wf.Namespace + "/" + wf.Name),
		TaskId:     toPtr(string(task.Metadata.ID)),
		AgentId:    toPtr(req.GetAgentId()),
		ActionId:   toPtr(string(action.Metadata.ID)),
		Name:       toPtr(action.Name),
		Image:      toPtr(action.Image),
		Timeout:    toPtr(pointerValue(action.TimeoutSeconds)),
		Command:    actionCommand(action.Action),
		Volumes:    volumes(task.Volumes, action.Volumes),
		Environment: func() []string {
			// add task environment variables to the action environment variables.
			joined := map[string]string{}
			maps.Copy(joined, task.Env)
			maps.Copy(joined, action.Env)
			resp := []string{}
			for k, v := range joined {
				resp = append(resp, fmt.Sprintf("%s=%s", k, v))
			}
			sort.Strings(resp)
			return resp
		}(),
		// The v1alpha1 top-level pid is namespaces.pid in v1alpha2.
		Pid: toPtr(action.Namespaces.PID),
	}
	if action.Namespaces != (v1alpha2.Namespaces{}) {
		// Pass the namespace values through to the agent as-is. The container
		// runtime interprets them (e.g. network "host" shares the host's
		// network namespace); the server does not impose any semantics.
		ar.Namespaces = &proto.Namespaces{
			Network: toPtr(action.Namespaces.Network),
			Pid:     toPtr(action.Namespaces.PID),
		}
	}

	log.Info("sending action", "action", ar, "actionID", action.Metadata.ID)
	journal.Log(ctx, "sending Action", "action", ar)
	return ar, nil
}

// workflowAwaitsAgent returns a NotFound status when a Workflow that has not finished has a task assigned to
// agentID that it is not running yet: it has not been rendered, or another task is current. Enrolling such
// an Agent would give it a second Workflow.
func (h *Handler) workflowAwaitsAgent(ctx context.Context, agentID string) error {
	wfs, err := h.Backend.ListWorkflowsV1Alpha2(ctx, data.WorkflowFilter{ByTaskAgentID: agentID})
	if err != nil {
		journal.Log(ctx, "error getting Workflows by task Agent", "error", err)
		return errors.Join(ErrBackendRead, status.Errorf(codes.Internal, "error getting workflows: %v", err))
	}
	for _, w := range wfs {
		if st := w.Status.Metadata.Workflow.State; st == v1alpha2.StateSuccess || st == v1alpha2.StateFailed || st == v1alpha2.StateTimeout {
			continue
		}
		journal.Log(ctx, "Workflow has a later Task for the Agent", "workflow", w.Name, "namespace", w.Namespace)
		return status.Errorf(codes.NotFound, "Workflow %s/%s has a Task for Agent %s that it is not running yet", w.Namespace, w.Name, agentID)
	}
	return nil
}

// actionCommand returns the command the Agent runs for an action, as a v1alpha1 action command: the
// executable followed by its arguments.
func actionCommand(a v1alpha2.Action) []string {
	if a.Command != "" {
		return append([]string{a.Command}, a.Args...)
	}
	if len(a.Args) > 0 {
		return append([]string(nil), a.Args...)
	}
	return nil
}

func volumes(lists ...[]v1alpha2.Volume) []string {
	var out []string
	for _, l := range lists {
		for _, v := range l {
			out = append(out, string(v))
		}
	}
	return out
}

func pointerValue[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// firstTaskWithActions returns the index of the first rendered task with actions, or 0.
func firstTaskWithActions(tasks []v1alpha2.TaskWithMetadata) int {
	for i := range tasks {
		if len(tasks[i].Actions) > 0 {
			return i
		}
	}
	return 0
}

// resolveAction determines which action to serve for the given task based on the workflow's current state:
// the current Action in m.Action, unset before the first Action is served.
func resolveAction(ctx context.Context, task *v1alpha2.TaskWithMetadata, m *v1alpha2.WorkflowMetadata) (*v1alpha2.ActionWithMetadata, error) {
	current := m.Action
	switch {
	case isFirstAction(*task):
		journal.Log(ctx, "first Action")
		return &task.Actions[0], nil

	case current.ID != "" && (current.State == v1alpha2.StateRunning || current.State == v1alpha2.StatePending):
		// Re-serve the current action. This handles two restart scenarios:
		// 1. Server restarts after serving an action but before the agent reports RUNNING
		//    (the current Action persisted as PENDING by GetAction).
		// 2. Server restarts while the agent is executing (the current Action is RUNNING).
		for idx := range task.Actions {
			if task.Actions[idx].Metadata.ID == current.ID {
				journal.Log(ctx, "re-serving in-progress Action", "actionID", task.Actions[idx].Metadata.ID, "state", current.State)
				return &task.Actions[idx], nil
			}
		}
		journal.Log(ctx, "in-progress Action not found in task")
		return nil, status.Error(codes.NotFound, "in-progress Action not found in task")

	default:
		// Get the next Action after the one defined in the current state.
		if current.ID == "" {
			journal.Log(ctx, "no current state available")
			return nil, status.Error(codes.FailedPrecondition, "no current state available")
		}
		if current.State != v1alpha2.StateSuccess {
			journal.Log(ctx, "current Action not in success state")
			return nil, status.Error(codes.FailedPrecondition, "current Action not in success state")
		}
		for idx, act := range task.Actions {
			if act.Metadata.ID == current.ID {
				if idx == len(task.Actions)-1 {
					journal.Log(ctx, "last Action in task")
					return nil, status.Error(codes.NotFound, "last Action in task")
				}
				journal.Log(ctx, "found Action", "actionID", task.Actions[idx+1].Metadata.ID)
				return &task.Actions[idx+1], nil
			}
		}
		journal.Log(ctx, "no Action found")
		return nil, status.Error(codes.NotFound, "no Action found")
	}
}

// isFirstAction checks if the Task is at the first Action.
func isFirstAction(t v1alpha2.TaskWithMetadata) bool {
	if len(t.Actions) == 0 {
		return false
	}
	if t.Actions[0].Metadata.State == v1alpha2.StatePending {
		return true
	}
	return false
}

func isTaskSuccessful(t v1alpha2.TaskWithMetadata) bool {
	return engine.TaskDone(&t)
}

// isAgentTerminalWorkflowState reports whether a workflow has finished its agent-driven execution, or
// is between tasks, running boot options: no Action is running, so reports can only be stale.
func isAgentTerminalWorkflowState(state v1alpha2.State) bool {
	switch state {
	case v1alpha2.StatePreparing,
		v1alpha2.StatePost,
		v1alpha2.StateSuccess,
		v1alpha2.StateFailed,
		v1alpha2.StateTimeout:
		return true
	default:
		return false
	}
}

func (h *Handler) ReportActionStatus(ctx context.Context, req *proto.ActionStatusRequest) (*proto.ActionStatusResponse, error) {
	// 1. Validate the request
	namespace, name, ok := strings.Cut(req.GetWorkflowId(), "/")
	if !ok || namespace == "" || name == "" {
		return nil, status.Errorf(codes.InvalidArgument, errInvalidWorkflowID)
	}
	if req.GetTaskId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, errInvalidTaskName)
	}
	if req.GetActionId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, errInvalidActionName)
	}
	// 2. Get the workflow
	wf, err := h.Backend.ReadWorkflowV1Alpha2(ctx, name, namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "workflow %s not found", req.GetWorkflowId())
		}
		return nil, errors.Join(ErrBackendRead, status.Errorf(codes.Internal, "error getting workflow: %v", err))
	}
	// 3. Find the Action in the workflow from the request
	for ti := range wf.Status.RenderedTasks {
		task := &wf.Status.RenderedTasks[ti]
		for ai := range task.Actions {
			action := &task.Actions[ai]
			if string(action.Metadata.ID) != req.GetActionId() || task.Metadata.AgentID != req.GetAgentId() {
				continue
			}
			if isAgentTerminalWorkflowState(wf.Status.Metadata.Workflow.State) {
				switch req.GetActionState() {
				case proto.ActionStatusRequest_SUCCESS, proto.ActionStatusRequest_FAILED, proto.ActionStatusRequest_TIMEOUT:
					return &proto.ActionStatusResponse{}, nil
				default:
					return nil, status.Errorf(codes.FailedPrecondition, "Workflow is in %s state", conversion.StateToV1Alpha1(wf.Status.Metadata.Workflow.State))
				}
			}
			actionState := conversion.StateToV1Alpha2(v1alpha1.WorkflowState(req.GetActionState().String()))
			if actionState == 0 {
				return nil, status.Errorf(codes.InvalidArgument, "invalid action state %s", req.GetActionState())
			}

			action.Metadata.State = actionState
			action.Metadata.StartTime = &metav1.Time{Time: req.GetExecutionStart().AsTime()}
			action.Metadata.EndTime = &metav1.Time{Time: req.GetExecutionStop().AsTime()}
			action.Metadata.ExecutionDuration = req.GetExecutionDuration()
			action.Metadata.Message = req.GetMessage().GetMessage()

			// 4. Write the updated workflow
			advanced := false
			switch {
			case actionState != v1alpha2.StateSuccess:
				wf.Status.Metadata.Workflow.State = actionState
				task.Metadata.State = actionState
				if actionState == v1alpha2.StateFailed || actionState == v1alpha2.StateTimeout {
					msg := fmt.Sprintf("action %q of task %q: %s", action.Name, task.Name, strings.ToLower(actionState.String()))
					if m := req.GetMessage().GetMessage(); m != "" {
						msg += ": " + m
					}
					task.Metadata.Message = msg
					wf.Status.Metadata.Workflow.Message = msg
				}
			case ai == len(task.Actions)-1:
				// The last action of the task. Boot options run, and a Workflow finishes, in Post.
				next := engine.NextTask(wf, ti)
				if next < 0 || engine.BootAfter(wf, ti).Post() || engine.BootBefore(wf, next).Pre() {
					wf.Status.Metadata.Workflow.State = v1alpha2.StatePost
					task.Metadata.State = v1alpha2.StatePost
				} else {
					// The next task runs straight away, on its own Agent.
					engine.Advance(wf, ti)
					advanced = true
				}
			default:
				task.Metadata.State = v1alpha2.StateRunning
			}

			// update the status current state
			if !advanced {
				wf.Status.Metadata.Task = v1alpha2.Metadata{
					AgentID:  req.GetAgentId(),
					ID:       task.Metadata.ID,
					Name:     task.Name,
					Hardware: task.Metadata.Hardware,
					State:    task.Metadata.State,
				}
			}
			wf.Status.Metadata.Action = v1alpha2.Metadata{
				ID:      action.Metadata.ID,
				Name:    req.GetActionName(),
				State:   actionState,
				Message: req.GetMessage().GetMessage(),
			}
			if err := h.Backend.UpdateWorkflowV1Alpha2(ctx, wf, data.UpdateOptions{StatusOnly: true}); err != nil {
				code := codes.Internal
				if apierrors.IsConflict(err) {
					code = codes.Aborted
				}
				return nil, status.Errorf(code, "error writing report status: %v", err)
			}
			return &proto.ActionStatusResponse{}, nil
		}
	}

	return nil, status.Error(codes.NotFound, "action not found")
}

// resolveAndAnnotateHardware resolves the Hardware object for a Workflow and persists agent attributes
// as a legacy annotation. This is only called on the very first action of the Workflow's first Task to
// avoid unnecessary backend reads; see updateHardwareWithAttributes for why the annotation, once set, is
// never refreshed by later Workflow runs. The resolved Hardware is returned so callers that need it again
// in the same request (e.g. for status.attributes.inBand) don't have to re-read it from the backend.
func (h *Handler) resolveAndAnnotateHardware(ctx context.Context, log logr.Logger, hwRef *v1alpha2.Hardware, ref *v1alpha2.SimpleReference, attrs *data.AgentAttributes) *v1alpha2.Hardware {
	if attrs == nil {
		return hwRef
	}
	hwRef = h.resolveHardware(ctx, hwRef, ref)
	if hwRef == nil {
		return nil
	}
	if err := h.updateHardwareWithAttributes(ctx, log, hwRef, attrs); err != nil {
		journal.Log(ctx, "error updating Hardware with attributes", "error", err)
		log.Error(err, "error updating Hardware with attributes")
	}
	return hwRef
}

// updateHardwareWithAttributes updates the Hardware with the given attributes annotation if it doesn't already have it.
// It uses a merge-patch (rather than a full update) to avoid conflicts with other controllers that may be
// modifying the same Hardware object concurrently (e.g. the workflow controller toggling netboot).
func (h *Handler) updateHardwareWithAttributes(ctx context.Context, log logr.Logger, hw *v1alpha2.Hardware, attrs *data.AgentAttributes) error {
	if hw == nil || attrs == nil || hw.Annotations[constant.AttributesAnnotation] != "" {
		return nil
	}

	// Take a snapshot before mutation so the backend can compute a minimal merge-patch.
	original := hw.DeepCopy()

	if hw.Annotations == nil {
		hw.Annotations = make(map[string]string)
	}

	a, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("error marshaling attributes for annotation: %w", err)
	}
	if len(a) > maxAnnotationSize {
		return fmt.Errorf("agent attributes annotation exceeds %dKB limit (%d bytes)", maxAnnotationSize/1024, len(a))
	}

	hw.Annotations[constant.AttributesAnnotation] = string(a)
	if err := h.Backend.UpdateHardwareV1Alpha2(ctx, hw, data.UpdateOptions{PatchFrom: original}); err != nil {
		return fmt.Errorf("error updating Hardware with attributes annotation: %w", err)
	}

	journal.Log(ctx, "updated Hardware with attributes annotation", "hardware", hw.Name)
	log.Info("updated Hardware with attributes annotation", "hardware", hw.Name)

	return nil
}

// resolveAndApplyInBandAttributes resolves the Hardware object for a Workflow and applies the calling
// Agent's attributes to status.attributes.inBand, but only when the calling Agent is the Hardware's own
// Agent (hw.Spec.AgentID) - other Agents used by the Workflow (e.g. an administrative Agent handling
// network or Netbox tasks) don't run on the target Hardware and would otherwise overwrite its in-band
// attributes with unrelated data. Unlike the legacy annotation, this is applied every time a matching
// Agent reports in, regardless of any existing value, so Hardware changes or a Tink Agent update are
// reflected on the next report rather than being permanently masked by the first one ever recorded.
func (h *Handler) resolveAndApplyInBandAttributes(ctx context.Context, log logr.Logger, hwRef *v1alpha2.Hardware, ref *v1alpha2.SimpleReference, agentID string, attrs *data.AgentAttributes) {
	if attrs == nil {
		return
	}
	hwRef = h.resolveHardware(ctx, hwRef, ref)
	if hwRef == nil || hwRef.Spec.AgentID != agentID {
		return
	}
	// inBandAttributesFromAgent(attrs) only returns nil when attrs is nil, already
	// ruled out by the guard above, so inBand is always non-nil here.
	inBand := inBandAttributesFromAgent(attrs)
	// Convert first, normalize second. This removes empty '{}' components from showing up in the Kubernetes API, which would otherwise be serialized as present but empty rather than absent.
	inBand.PruneEmpty()
	inBand.CollectionMethod = "agent"
	inBand.LastUpdated = &metav1.Time{Time: h.NowFunc()}
	if err := h.Backend.ApplyHardwareInBandAttributesV1Alpha2(ctx, hwRef.Name, hwRef.Namespace, inBand); err != nil {
		journal.Log(ctx, "error applying Hardware status.attributes.inBand", "error", err)
		log.Error(err, "error applying Hardware status.attributes.inBand")
	}
}

// resolveHardware returns hwRef unchanged if already resolved, otherwise reads the Hardware ref names from
// the backend. It returns nil when neither is set.
func (h *Handler) resolveHardware(ctx context.Context, hwRef *v1alpha2.Hardware, ref *v1alpha2.SimpleReference) *v1alpha2.Hardware {
	if hwRef != nil {
		return hwRef
	}
	if ref == nil {
		return nil
	}
	hw, err := h.Backend.ReadHardwareV1Alpha2(ctx, ref.Name, ref.Namespace)
	if err != nil {
		return nil
	}
	return hw
}

func toPtr[T any](v T) *T {
	return &v
}
