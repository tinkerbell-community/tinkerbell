package conversion

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"gopkg.in/yaml.v3"
)

// TemplateTask is one v1alpha1 Template task converted to a v1alpha2 Task spec.
type TemplateTask struct {
	// Name is the template task's name.
	Name string
	// WorkerKey is the hardwareMap key the task's worker comes from ("device_1"
	// for worker "{{.device_1}}"), or empty when the worker is a literal.
	WorkerKey string
	// Worker is the literal worker (Agent ID) when WorkerKey is empty.
	Worker string
	// Spec is the v1alpha2 Task spec.
	Spec v1alpha2.TaskSpec
}

// TemplateConversion is a v1alpha1 Template converted to v1alpha2 Tasks.
type TemplateConversion struct {
	Tasks []TemplateTask
	// GlobalTimeout is the template's global_timeout, in seconds.
	GlobalTimeout int64
}

// NotConvertibleError explains why a Template cannot become v1alpha2 Tasks.
type NotConvertibleError struct{ Reason string }

func (e NotConvertibleError) Error() string { return "template cannot be converted: " + e.Reason }

func notConvertible(format string, args ...any) error {
	return NotConvertibleError{Reason: fmt.Sprintf(format, args...)}
}

const (
	identHardwareV1 = "Hardware"
	identHardware   = "hardware"
	identSpec       = "spec"
	identInstance   = "instance"
)

// v1alpha1 template YAML, as the v1alpha1 Workflow engine parses it.
type tplWorkflow struct {
	Version       string    `yaml:"version"`
	Name          string    `yaml:"name"`
	ID            string    `yaml:"id"`
	GlobalTimeout int64     `yaml:"global_timeout"`
	Tasks         []tplTask `yaml:"tasks"`
}

type tplTask struct {
	Name        string            `yaml:"name"`
	Worker      string            `yaml:"worker"`
	Actions     []tplAction       `yaml:"actions"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
}

type tplAction struct {
	Name        string            `yaml:"name"`
	Image       string            `yaml:"image"`
	Timeout     int64             `yaml:"timeout"`
	Command     []string          `yaml:"command,omitempty"`
	OnTimeout   []string          `yaml:"on-timeout,omitempty"`
	OnFailure   []string          `yaml:"on-failure,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Pid         string            `yaml:"pid,omitempty"`
	Namespaces  struct {
		Network string `yaml:"network,omitempty"`
		PID     string `yaml:"pid,omitempty"`
	} `yaml:"namespaces,omitempty"`
}

// structureFuncs produce YAML structure, which v1alpha2 cannot express: it
// renders string fields one by one, not the whole document.
var structureFuncs = []string{"toYaml", "toYAML", "indent", "nindent", "fromYaml", "fromYAML"}

var (
	placeholderRE = regexp.MustCompile(`__tinkerbell_tpl_(\d+)__`)
	soleWorkerRE  = regexp.MustCompile(`^__tinkerbell_tpl_(\d+)__$`)
)

// builtins are the text/template builtin functions; parse.Parse does not add them.
var builtins = map[string]any{
	"and": true, "call": true, "html": true, "index": true, "slice": true, "js": true, "len": true, "not": true,
	"or": true, "print": true, "printf": true, "println": true, "urlquery": true,
	"eq": true, "ge": true, "gt": true, "le": true, "lt": true, "ne": true,
}

// ConvertTemplate converts the data of a v1alpha1 Template to v1alpha2 Tasks.
//
// A Template's data is Go-templated YAML that the v1alpha1 engine renders as a
// whole before parsing; v1alpha2 renders each string field of a Task. So only
// Templates whose structure does not depend on templating convert: every
// template action must be a single expression inside a string value, with no
// control structures and no functions that produce YAML, and every variable
// must have a v1alpha2 equivalent. funcs are the template functions available
// to Templates. Anything else returns a NotConvertibleError.
func ConvertTemplate(data string, funcs template.FuncMap) (*TemplateConversion, error) {
	trees, err := parse.Parse("template", data, "{{", "}}", funcMapAny(funcs), builtins)
	if err != nil {
		return nil, notConvertible("invalid template: %v", err)
	}
	root := trees["template"]
	if len(trees) > 1 {
		return nil, notConvertible("it defines templates")
	}
	if root == nil || root.Root == nil {
		return nil, notConvertible("empty template")
	}

	// First pass: the structure, with every action replaced by a placeholder.
	var skeleton strings.Builder
	var actions []*parse.ActionNode
	for _, n := range root.Root.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			skeleton.Write(n.Text)
		case *parse.ActionNode:
			fmt.Fprintf(&skeleton, "__tinkerbell_tpl_%d__", len(actions))
			actions = append(actions, n)
		default:
			return nil, notConvertible("it uses %q, whose output can change the YAML structure", firstWord(n.String()))
		}
	}
	var wf tplWorkflow
	dec := yaml.NewDecoder(strings.NewReader(skeleton.String()))
	dec.KnownFields(true)
	if err := dec.Decode(&wf); err != nil {
		return nil, notConvertible("its YAML depends on template output: %v", err)
	}
	if len(wf.Tasks) == 0 {
		return nil, notConvertible("it has no tasks")
	}

	// Second pass: rewrite each action for v1alpha2.
	workerTask := map[string]int{}
	workerKeys := make([]string, len(wf.Tasks))
	for i, t := range wf.Tasks {
		if placeholderRE.MatchString(t.Worker) {
			m := soleWorkerRE.FindStringSubmatch(strings.TrimSpace(t.Worker))
			if m == nil {
				return nil, notConvertible("task %q worker is not a single variable", t.Name)
			}
			key, ok := topLevelField(actions[atoi(m[1])])
			if !ok {
				return nil, notConvertible("task %q worker is not a single variable", t.Name)
			}
			workerKeys[i] = key
			if _, dup := workerTask[key]; !dup {
				workerTask[key] = i
			}
		}
	}
	rewritten := make([]string, len(actions))
	for i, a := range actions {
		s, err := rewriteAction(a, workerTask)
		if err != nil {
			return nil, err
		}
		rewritten[i] = s
	}
	expand := func(s string) string {
		return placeholderRE.ReplaceAllStringFunc(s, func(p string) string {
			return rewritten[atoi(placeholderRE.FindStringSubmatch(p)[1])]
		})
	}
	noPlaceholder := func(what, s string) error {
		if placeholderRE.MatchString(s) {
			return notConvertible("%s %q is templated", what, s)
		}
		return nil
	}

	out := &TemplateConversion{GlobalTimeout: wf.GlobalTimeout}
	for i, t := range wf.Tasks {
		if err := noPlaceholder("task name", t.Name); err != nil {
			return nil, err
		}
		tt := TemplateTask{Name: t.Name, WorkerKey: workerKeys[i]}
		if tt.WorkerKey == "" {
			tt.Worker = t.Worker
		}
		env, err := expandMap(t.Environment, expand)
		if err != nil {
			return nil, err
		}
		tt.Spec = v1alpha2.TaskSpec{Env: env}
		for _, v := range t.Volumes {
			tt.Spec.Volumes = append(tt.Spec.Volumes, v1alpha2.Volume(expand(v)))
		}
		for _, a := range t.Actions {
			if len(a.OnTimeout) > 0 || len(a.OnFailure) > 0 {
				return nil, notConvertible("action %q uses on-timeout/on-failure, which v1alpha2 does not have", a.Name)
			}
			if err := noPlaceholder("action name", a.Name); err != nil {
				return nil, err
			}
			act := v1alpha2.Action{Name: a.Name, Image: expand(a.Image)}
			if len(a.Command) > 0 {
				act.Command = expand(a.Command[0])
				for _, c := range a.Command[1:] {
					act.Args = append(act.Args, expand(c))
				}
			}
			if a.Timeout > 0 {
				act.TimeoutSeconds = ptr(a.Timeout)
			}
			if act.Env, err = expandMap(a.Environment, expand); err != nil {
				return nil, err
			}
			for _, v := range a.Volumes {
				act.Volumes = append(act.Volumes, v1alpha2.Volume(expand(v)))
			}
			act.Namespaces = v1alpha2.Namespaces{Network: expand(a.Namespaces.Network), PID: expand(a.Namespaces.PID)}
			if act.Namespaces.PID == "" {
				act.Namespaces.PID = expand(a.Pid)
			}
			tt.Spec.Actions = append(tt.Spec.Actions, act)
		}
		out.Tasks = append(out.Tasks, tt)
	}
	return out, nil
}

func expandMap(in map[string]string, expand func(string) string) (map[string]string, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if placeholderRE.MatchString(k) {
			return nil, notConvertible("environment variable name %q is templated", k)
		}
		out[k] = expand(v)
	}
	return out, nil
}

// rewriteAction returns the v1alpha2 form of a template action.
func rewriteAction(a *parse.ActionNode, workerTask map[string]int) (string, error) {
	if len(a.Pipe.Decl) > 0 {
		return "", notConvertible("action %s declares variables", a)
	}
	for _, cmd := range a.Pipe.Cmds {
		if len(cmd.Args) == 0 {
			continue
		}
		if id, ok := cmd.Args[0].(*parse.IdentifierNode); ok && slices.Contains(structureFuncs, id.Ident) {
			return "", notConvertible("action %s uses %s, which produces YAML structure", a, id.Ident)
		}
		// {{ index .Hardware.Disks N }}: v1alpha2 storage devices are objects.
		if id, ok := cmd.Args[0].(*parse.IdentifierNode); ok && id.Ident == "index" && len(cmd.Args) == 3 {
			if f, ok := cmd.Args[1].(*parse.FieldNode); ok && slices.Equal(f.Ident, []string{identHardwareV1, "Disks"}) {
				devices := &parse.FieldNode{NodeType: parse.NodeField, Ident: []string{identHardware, identSpec, "storageDevices"}}
				inner := &parse.CommandNode{NodeType: parse.NodeCommand, Args: []parse.Node{cmd.Args[0], devices, cmd.Args[2]}}
				pipe := &parse.PipeNode{NodeType: parse.NodePipe, Cmds: []*parse.CommandNode{inner}}
				cmd.Args = []parse.Node{&parse.ChainNode{NodeType: parse.NodeChain, Node: pipe, Field: []string{"name"}}}
				continue
			}
		}
		for i, arg := range cmd.Args {
			repl, err := rewriteNode(arg, workerTask)
			if err != nil {
				return "", err
			}
			cmd.Args[i] = repl
		}
	}
	return a.String(), nil
}

func rewriteNode(n parse.Node, workerTask map[string]int) (parse.Node, error) {
	switch n := n.(type) {
	case *parse.FieldNode:
		return rewriteField(n.Ident, workerTask)
	case *parse.VariableNode:
		if len(n.Ident) > 1 && n.Ident[0] == "$" {
			return rewriteField(n.Ident[1:], workerTask)
		}
		if len(n.Ident) == 1 && n.Ident[0] == "$" {
			return nil, notConvertible("it uses the whole template data ($)")
		}
		return nil, notConvertible("it uses variable %s", n)
	case *parse.DotNode:
		return nil, notConvertible("it uses the whole template data (.)")
	case *parse.PipeNode:
		for _, cmd := range n.Cmds {
			for i, arg := range cmd.Args {
				repl, err := rewriteNode(arg, workerTask)
				if err != nil {
					return nil, err
				}
				cmd.Args[i] = repl
			}
		}
		return n, nil
	case *parse.ChainNode:
		inner, err := rewriteNode(n.Node, workerTask)
		if err != nil {
			return nil, err
		}
		n.Node = inner
		return n, nil
	}
	return n, nil
}

// rewriteField maps a v1alpha1 template data path to its v1alpha2 Task data path.
func rewriteField(ident []string, workerTask map[string]int) (parse.Node, error) {
	field := func(path ...string) parse.Node {
		return &parse.FieldNode{NodeType: parse.NodeField, Ident: path}
	}
	path := "." + strings.Join(ident, ".")
	switch ident[0] {
	case "metadata_url":
		return field(ident...), nil
	case "references":
		return nil, notConvertible("it uses %s: in v1alpha2 a Task cannot read its Hardware's references", path)
	case identHardwareV1:
		switch {
		case slices.Equal(ident, []string{identHardwareV1, "UserData"}):
			return field(identHardware, identSpec, identInstance, "userdata"), nil
		case slices.Equal(ident, []string{identHardwareV1, "VendorData"}):
			return field(identHardware, identSpec, identInstance, "vendordata"), nil
		case slices.Equal(ident, []string{identHardwareV1, "Metadata", "Instance", "ID"}):
			return field(identHardware, identSpec, identInstance, "lookupID"), nil
		case slices.Equal(ident, []string{identHardwareV1, "Metadata", "Instance", "SSHKeys"}):
			return field(identHardware, identSpec, identInstance, "sshKeys"), nil
		}
		return nil, notConvertible("it uses %s, which has no v1alpha2 equivalent", path)
	case identHardware:
		rest := ident[1:]
		switch {
		case len(rest) >= 1 && rest[0] == "metadata":
			return field(ident...), nil
		case slices.Equal(rest, []string{identSpec, "agentID"}):
			return field(ident...), nil
		case slices.Equal(rest, []string{identSpec, "userData"}):
			return field(identHardware, identSpec, identInstance, "userdata"), nil
		case slices.Equal(rest, []string{identSpec, "vendorData"}):
			return field(identHardware, identSpec, identInstance, "vendordata"), nil
		case slices.Equal(rest, []string{identSpec, "metadata", identInstance, "id"}):
			return field(identHardware, identSpec, identInstance, "lookupID"), nil
		case slices.Equal(rest, []string{identSpec, "metadata", identInstance, "ssh_keys"}):
			return field(identHardware, identSpec, identInstance, "sshKeys"), nil
		}
		return nil, notConvertible("it uses %s, which has no v1alpha2 equivalent", path)
	}
	if len(ident) > 1 {
		return nil, notConvertible("it uses %s; hardwareMap values are strings", path)
	}
	// A hardwareMap key. If it is a task's worker, it is that task's Agent.
	if i, ok := workerTask[ident[0]]; ok {
		agents := &parse.FieldNode{NodeType: parse.NodeField, Ident: []string{"workflow", identSpec, "tasks"}}
		idx := &parse.CommandNode{NodeType: parse.NodeCommand, Args: []parse.Node{
			&parse.IdentifierNode{NodeType: parse.NodeIdentifier, Ident: "index"}, agents,
			&parse.NumberNode{NodeType: parse.NodeNumber, IsInt: true, Int64: int64(i), Text: strconv.Itoa(i)},
		}}
		return &parse.ChainNode{NodeType: parse.NodeChain, Node: &parse.PipeNode{NodeType: parse.NodePipe, Cmds: []*parse.CommandNode{idx}}, Field: []string{"agentID"}}, nil
	}
	return field("workflow", identSpec, "globals", "templateMap", ident[0]), nil
}

// topLevelField returns K when the action is exactly {{ .K }}.
func topLevelField(a *parse.ActionNode) (string, bool) {
	if len(a.Pipe.Decl) > 0 || len(a.Pipe.Cmds) != 1 || len(a.Pipe.Cmds[0].Args) != 1 {
		return "", false
	}
	f, ok := a.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
	if !ok || len(f.Ident) != 1 {
		return "", false
	}
	return f.Ident[0], true
}

func funcMapAny(f template.FuncMap) map[string]any {
	out := make(map[string]any, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}

func firstWord(s string) string {
	s = strings.TrimPrefix(s, "{{")
	if f := strings.Fields(s); len(f) > 0 {
		return strings.TrimSuffix(f[0], "}}")
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// IsNotConvertible reports whether err is a NotConvertibleError.
func IsNotConvertible(err error) bool {
	var nc NotConvertibleError
	return errors.As(err, &nc)
}
