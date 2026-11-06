package workflow

import (
	"github.com/oklog/ulid/v2"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/proto"
	"k8s.io/apimachinery/pkg/types"
)

// YAMLToStatus returns the status of a Workflow that runs the parsed v1alpha1
// Template wf: its tasks and actions, as the v1alpha1 engine rendered them,
// in their v1alpha2 form.
func YAMLToStatus(wf *Workflow) *v1alpha2.WorkflowStatus {
	if wf == nil {
		return nil
	}
	tasks := []v1alpha2.TaskWithMetadata{}
	agentID := ""
	for _, task := range wf.Tasks {
		actions := []v1alpha2.ActionWithMetadata{}
		for _, action := range task.Actions {
			a := v1alpha1.Action{
				ID:          ulid.Make().String(),
				Name:        action.Name,
				Image:       action.Image,
				Timeout:     action.Timeout,
				Command:     action.Command,
				Volumes:     action.Volumes,
				State:       v1alpha1.WorkflowState(proto.ActionStatusRequest_PENDING.String()),
				Environment: action.Environment,
				Pid:         action.Pid, //nolint:staticcheck // SA1019: deprecated field still propagated for backward compatibility.
			}
			if action.Namespaces.Network != "" || action.Namespaces.PID != "" {
				a.Namespaces = &v1alpha1.ActionNamespaces{
					Network: action.Namespaces.Network,
					PID:     action.Namespaces.PID,
				}
			}
			actions = append(actions, conversion.ActionToV1Alpha2(a))
		}
		var volumes []v1alpha2.Volume
		for _, v := range task.Volumes {
			volumes = append(volumes, v1alpha2.Volume(v))
		}
		tasks = append(tasks, v1alpha2.TaskWithMetadata{
			Name:    task.Name,
			Env:     task.Environment,
			Volumes: volumes,
			Actions: actions,
			Metadata: v1alpha2.Metadata{
				ID:      types.UID(ulid.Make().String()),
				Name:    task.Name,
				AgentID: task.WorkerAddr,
				State:   v1alpha2.StatePending,
			},
		})
		// only use the first Task's agentID. At the moment only support single Task Workflows.
		if agentID == "" {
			agentID = task.WorkerAddr
		}
	}
	status := &v1alpha2.WorkflowStatus{
		GlobalTimeout: int64(wf.GlobalTimeout),
		RenderedTasks: tasks,
	}
	status.Metadata.Task.AgentID = agentID
	return status
}
