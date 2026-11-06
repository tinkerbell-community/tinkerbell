package webhttp

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const (
	nameSingularTinkTask = "Task"
	namePluralTinkTask   = "Tasks"
	pathTasks            = "/tasks"
)

// HandleTaskList handles the Task list page route.
func HandleTaskList(c *gin.Context, log logr.Logger) { serveTasks(c, log, true) }

// HandleTaskData handles the Task data endpoint (HTMX partial).
func HandleTaskData(c *gin.Context, log logr.Logger) { serveTasks(c, log, false) }

func serveTasks(c *gin.Context, log logr.Logger, fullPage bool) {
	req, ok := newListRequest(c, log, fullPage)
	if !ok {
		return
	}
	var rows []templates.Task
	list, err := req.client.ListTasks(req.ctx, req.namespace)
	if err != nil {
		log.V(1).Info("Failed to list "+namePluralTinkTask, "error", err)
		if HandleAuthError(c, err, log) {
			return
		}
	} else {
		for i := range list.Items {
			rows = append(rows, taskRow(&list.Items[i]))
		}
	}
	var data templates.TaskPageData
	data.Tasks, data.Pagination = paginate(rows, req.page, req.perPage, pathTasks, "#tasks-content")

	if !fullPage || IsHTMXRequest(c) {
		renderHTML(c, templates.TaskTableContent(data), log)
		return
	}
	renderHTML(c, templates.TaskPage(pageConfig(c, req.namespaces), data), log)
}

// HandleTaskDetail handles the Task detail page route.
func HandleTaskDetail(c *gin.Context, log logr.Logger) {
	client, namespaces, ok := detailRequest(c, log)
	if !ok {
		return
	}
	namespace, name := c.Param("namespace"), c.Param(keyName)
	task, err := client.GetTask(c.Request.Context(), namespace, name)
	if err != nil {
		log.V(1).Info("Failed to fetch "+nameSingularTinkTask, "namespace", namespace, "name", name, "error", err)
		if !HandleAuthError(c, err, log) {
			renderNotFound(c, log, namespaces, nameSingularTinkTask, namePluralTinkTask, GetBaseURL(c)+pathTasks, namespace, name)
		}
		return
	}

	d := taskDetail(task, GetBaseURL(c))
	specYAML, _ := yaml.Marshal(&task.Spec)
	yamlBytes, _ := yaml.Marshal(task)
	d.SpecYAML, d.YAML = string(specYAML), string(yamlBytes)
	renderHTML(c, templates.TaskDetailPage(pageConfig(c, namespaces), d), log)
}

// taskTemplate returns the deprecated v1alpha1 Template a Task was translated
// from: translated Tasks name it in an annotation and are owned by it.
func taskTemplate(t *tinkv1alpha2.Task) string {
	if name := t.Annotations[conversion.AnnotationTemplate]; name != "" {
		return name
	}
	return ownerName(t, "Template")
}

// ownerName returns the name of obj's tinkerbell.org owner of kind.
func ownerName(obj metav1.Object, kind string) string {
	for _, o := range obj.GetOwnerReferences() {
		if o.Kind == kind && strings.HasPrefix(o.APIVersion, groupTinkerbell+"/") {
			return o.Name
		}
	}
	return ""
}

func taskRow(t *tinkv1alpha2.Task) templates.Task {
	return templates.Task{
		Name:      t.Name,
		Namespace: t.Namespace,
		Actions:   len(t.Spec.Actions),
		Template:  taskTemplate(t),
		CreatedAt: t.GetCreationTimestamp().Format(timeFormat),
	}
}

func taskDetail(t *tinkv1alpha2.Task, baseURL string) templates.TaskDetail {
	d := templates.TaskDetail{
		Name:        t.Name,
		Namespace:   t.Namespace,
		CreatedAt:   t.GetCreationTimestamp().Format(timeFormat),
		Labels:      t.Labels,
		Annotations: t.Annotations,
		Details: []templates.InfoRow{
			{Name: "Actions", Value: strconv.Itoa(len(t.Spec.Actions))},
			{Name: "Volumes", Value: joinStrings(t.Spec.Volumes)},
		},
		Env:        mapRows(t.Spec.Env),
		Vars:       mapRows(t.Spec.Vars),
		References: referenceRows(t.Spec.References),
	}
	if tpl := taskTemplate(t); tpl != "" {
		d.Details = append(d.Details,
			templates.InfoRow{Name: "Translated From Template", Value: tpl, Link: baseURL + "/templates/" + t.Namespace + "/" + tpl},
			templates.InfoRow{Name: "Template Task", Value: t.Annotations[conversion.AnnotationTemplateTask]},
		)
	}
	for _, a := range t.Spec.Actions {
		d.Actions = append(d.Actions, taskAction(a))
	}
	return d
}

func taskAction(a tinkv1alpha2.Action) templates.TaskAction {
	out := templates.TaskAction{
		Name:    a.Name,
		Image:   a.Image,
		Command: strings.TrimSpace(a.Command + " " + strings.Join(a.Args, " ")),
		Timeout: seconds(a.TimeoutSeconds),
	}
	if a.If != "" {
		out.Details = append(out.Details, "if "+a.If)
	}
	if a.Retries > 0 {
		out.Details = append(out.Details, "retries: "+strconv.Itoa(a.Retries))
	}
	if a.Background {
		out.Details = append(out.Details, "runs in the background")
	}
	if a.Namespaces.PID != "" {
		out.Details = append(out.Details, "pid namespace: "+a.Namespaces.PID)
	}
	if a.Namespaces.Network != "" {
		out.Details = append(out.Details, "network namespace: "+a.Namespaces.Network)
	}
	for _, k := range slices.Sorted(maps.Keys(a.Env)) {
		out.Details = append(out.Details, k+"="+a.Env[k])
	}
	for _, v := range a.Volumes {
		out.Details = append(out.Details, "volume "+string(v))
	}
	return out
}

// mapRows returns one row per key of m, in key order.
func mapRows(m map[string]string) []templates.InfoRow {
	rows := make([]templates.InfoRow, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		rows = append(rows, templates.InfoRow{Name: k, Value: m[k]})
	}
	return rows
}
