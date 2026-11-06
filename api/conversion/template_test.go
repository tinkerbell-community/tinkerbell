package conversion

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/google/go-cmp/cmp"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/template/funcmap"
)

const captTemplate = `version: "0.1"
name: talos
global_timeout: 6000
tasks:
  - name: "talos"
    worker: "{{.device_1}}"
    volumes:
      - /dev:/dev
    environment:
      MIRROR: "{{ .mirror }}"
    actions:
      - name: "stream-image"
        image: ghcr.io/sidero-community/actions/talos2disk:latest
        timeout: 600
        environment:
          DEST_DISK: {{ index .Hardware.Disks 0 }}
          USER_DATA: "{{ .hardware.spec.userData }}"
          METADATA: "{{ .metadata_url }}/{{ .device_1 }}"
          ID: '{{ .Hardware.Metadata.Instance.ID | quote }}'
      - name: "reboot"
        image: ghcr.io/jacobweinstock/waitdaemon:latest
        timeout: 90
        pid: host
        command: ["sh", "-c", "echo b > /proc/sysrq-trigger"]
`

func TestConvertTemplate(t *testing.T) {
	got, err := ConvertTemplate(captTemplate, funcmap.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.GlobalTimeout != 6000 || len(got.Tasks) != 1 {
		t.Fatalf("conversion: %+v", got)
	}
	task := got.Tasks[0]
	if task.Name != "talos" || task.WorkerKey != "device_1" {
		t.Errorf("worker binding: %+v", task)
	}
	want := v1alpha2.TaskSpec{
		Env:     map[string]string{"MIRROR": "{{.workflow.spec.globals.templateMap.mirror}}"},
		Volumes: []v1alpha2.Volume{"/dev:/dev"},
		Actions: []v1alpha2.Action{
			{
				Name: "stream-image", Image: "ghcr.io/sidero-community/actions/talos2disk:latest", TimeoutSeconds: ptr[int64](600),
				Env: map[string]string{
					"DEST_DISK": "{{(index .hardware.spec.storageDevices 0).name}}",
					"USER_DATA": "{{.hardware.spec.instance.userdata}}",
					"METADATA":  "{{.metadata_url}}/{{(index .workflow.spec.tasks 0).agentID}}",
					"ID":        `{{(dig "spec" "instance" "lookupID" "" .hardware) | quote}}`,
				},
			},
			{
				Name: "reboot", Image: "ghcr.io/jacobweinstock/waitdaemon:latest", TimeoutSeconds: ptr[int64](90),
				Command: "sh", Args: []string{"-c", "echo b > /proc/sysrq-trigger"}, Namespaces: v1alpha2.Namespaces{PID: "host"},
			},
		},
	}
	if d := cmp.Diff(want, task.Spec, cmpOpts...); d != "" {
		t.Errorf("task spec (-want +got):\n%s", d)
	}

	// The rewritten expressions render against v1alpha2 data.
	data := map[string]any{
		"metadata_url": "http://tootles",
		"hardware": map[string]any{"spec": map[string]any{
			"storageDevices": []any{map[string]any{"name": "/dev/nvme0n1"}},
			"instance":       map[string]any{"userdata": "cfg", "lookupID": "abc"},
		}},
		"workflow": map[string]any{"spec": map[string]any{"tasks": []any{map[string]any{"agentID": "52:54:00:00:00:01"}}}},
	}
	for k, want := range map[string]string{"DEST_DISK": "/dev/nvme0n1", "METADATA": "http://tootles/52:54:00:00:00:01", "ID": `"abc"`} {
		tpl := template.Must(template.New(k).Funcs(funcmap.New()).Parse(task.Spec.Actions[0].Env[k]))
		var b bytes.Buffer
		if err := tpl.Execute(&b, data); err != nil {
			t.Fatal(err)
		}
		if b.String() != want {
			t.Errorf("%s rendered %q, want %q", k, b.String(), want)
		}
	}
}

func TestConvertTemplateNotConvertible(t *testing.T) {
	base := "version: \"0.1\"\nname: x\ntasks:\n  - name: t\n    worker: \"{{.device_1}}\"\n    actions:\n"
	tests := map[string]string{
		"range":           base + "{{- range .disks }}\n      - name: a\n        image: i\n{{- end }}\n",
		"if in a value":   base + "      - name: a\n        image: \"{{ if .x }}a{{ end }}\"\n",
		"toYaml":          base + "      - name: a\n        image: i\n        environment:\n          CFG: {{ .references.c | toYaml | nindent 12 }}\n",
		"references":      base + "      - name: a\n        image: \"{{ .references.c.data.x }}\"\n",
		"templated int":   base + "      - name: a\n        image: i\n        timeout: {{ .t }}\n",
		"interfaces":      base + "      - name: a\n        image: \"{{ (index .Hardware.Interfaces 0).DHCP.MAC }}\"\n",
		"templated key":   base + "      - name: a\n        image: i\n        environment:\n          \"{{ .k }}\": v\n",
		"computed worker": "version: \"0.1\"\nname: x\ntasks:\n  - name: t\n    worker: \"{{ .a }}{{ .b }}\"\n    actions:\n      - name: a\n        image: i\n",
	}
	for name, tpl := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ConvertTemplate(tpl, funcmap.New())
			if !IsNotConvertible(err) {
				t.Fatalf("want a NotConvertibleError, got %v", err)
			}
			t.Log(err)
		})
	}
}

// The legacy .Hardware contract renders unset fields as empty, as v1alpha1 did.
func TestConvertTemplateLegacyUnsetFields(t *testing.T) {
	tpl := `version: "0.1"
name: x
tasks:
  - name: t
    worker: "{{.device_1}}"
    actions:
      - name: a
        image: i
        environment:
          U: "{{ .Hardware.UserData }}"
          K: '{{ join "," .Hardware.Metadata.Instance.SSHKeys }}'
`
	c, err := ConvertTemplate(tpl, funcmap.New())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []map[string]any{
		{"hardware": map[string]any{"spec": map[string]any{}}},
		{"hardware": map[string]any{"spec": map[string]any{"instance": map[string]any{}}}},
	} {
		for k, expr := range c.Tasks[0].Spec.Actions[0].Env {
			tm := template.Must(template.New(k).Funcs(funcmap.New()).Option("missingkey=error").Parse(expr))
			var b bytes.Buffer
			if err := tm.Execute(&b, data); err != nil {
				t.Fatalf("%s (%s): %v", k, expr, err)
			}
			if b.String() != "" {
				t.Errorf("%s rendered %q, want empty", k, b.String())
			}
		}
	}
}
