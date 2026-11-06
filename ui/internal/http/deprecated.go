package webhttp

import (
	"cmp"
	"context"
	"slices"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// annotationTranslation is set on a Template that cannot be translated to
// v1alpha2 Tasks, to the reason (see docs/technical/v1alpha2/MIGRATION.md).
const annotationTranslation = "tinkerbell.org/v1alpha2-translation"

// The deprecated kinds that only exist in v1alpha1 are translated to their
// v1alpha2 replacements. These find the replacements, for the deprecated
// kinds' detail pages; a failure only leaves them out.

// templateTasks returns the Tasks the Template namespace/name was translated to.
func templateTasks(ctx context.Context, client *KubeClient, log logr.Logger, namespace, name string) []templates.TaskRef {
	list, err := client.ListTasks(ctx, namespace)
	if err != nil {
		log.V(1).Info("Failed to list the Tasks of a Template", "namespace", namespace, "template", name, "error", err)
		return nil
	}
	var refs []templates.TaskRef
	for i := range list.Items {
		task := &list.Items[i]
		if taskTemplate(task) == name {
			refs = append(refs, templates.TaskRef{Name: task.Name, Namespace: task.Namespace})
		}
	}
	slices.SortFunc(refs, func(a, b templates.TaskRef) int { return cmp.Compare(a.Name, b.Name) })
	return refs
}

// ruleSetPolicy returns the name of the Policy the WorkflowRuleSet
// namespace/name was translated to, a Policy of the same name, if it exists.
func ruleSetPolicy(ctx context.Context, client *KubeClient, log logr.Logger, namespace, name string) string {
	if _, err := client.GetPolicy(ctx, namespace, name); err != nil {
		if !apierrors.IsNotFound(err) {
			log.V(1).Info("Failed to get the Policy of a WorkflowRuleSet", "namespace", namespace, "ruleSet", name, "error", err)
		}
		return ""
	}
	return name
}

// machineHardware returns the Hardware, as "namespace/name", whose v1alpha1
// bmcRef names the bmc Machine namespace/name.
func machineHardware(ctx context.Context, client *KubeClient, log logr.Logger, namespace, name string) []string {
	list, err := client.ListHardware(ctx, namespace)
	if err != nil {
		log.V(1).Info("Failed to list the Hardware of a bmc Machine", "namespace", namespace, "machine", name, "error", err)
		return nil
	}
	var out []string
	for i := range list.Items {
		hw := &list.Items[i]
		if ref := conversion.HardwareBMCRef(hw); ref != nil && ref.Name == name && (ref.Kind == "" || ref.Kind == nameSingularMachine) {
			out = append(out, hw.Namespace+"/"+hw.Name)
		}
	}
	slices.Sort(out)
	return out
}
