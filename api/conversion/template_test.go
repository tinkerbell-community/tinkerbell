package conversion

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/template"

	"github.com/google/go-cmp/cmp"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
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
	got, err := ConvertTemplate(captTemplate, testFuncs())
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
		tpl := template.Must(template.New(k).Funcs(testFuncs()).Parse(task.Spec.Actions[0].Env[k]))
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
			_, err := ConvertTemplate(tpl, testFuncs())
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
	c, err := ConvertTemplate(tpl, testFuncs())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []map[string]any{
		{"hardware": map[string]any{"spec": map[string]any{}}},
		{"hardware": map[string]any{"spec": map[string]any{"instance": map[string]any{}}}},
	} {
		for k, expr := range c.Tasks[0].Spec.Actions[0].Env {
			tm := template.Must(template.New(k).Funcs(testFuncs()).Option("missingkey=error").Parse(expr))
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

// The Cluster API Provider Tinkerbell Quick Start template: formatPartition
// takes (index .Hardware.Disks 0) as a nested pipeline.
const captQuickStartTemplate = `version: "0.1"
name: my-provisioning-template
global_timeout: 6000
tasks:
  - name: "my-provisioning-template"
    worker: "{{.device_1}}"
    volumes:
      - /dev:/dev
    actions:
      - name: "stream image"
        image: quay.io/tinkerbell/actions/oci2disk
        timeout: 1200
        environment:
          # Replace 'amd64' with the target node architecture (amd64 or arm64)
          IMG_URL: ghcr.io/tinkerbell/cluster-api-provider-tinkerbell/ubuntu:2404-v1.35.2-amd64.gz
          DEST_DISK: {{ index .Hardware.Disks 0 }}
          COMPRESSED: true
      - name: "add tink cloud-init config"
        image: quay.io/tinkerbell/actions/writefile
        timeout: 90
        environment:
          DEST_DISK: {{ formatPartition ( index .Hardware.Disks 0 ) 3 }}
          FS_TYPE: ext4
          DEST_PATH: /etc/cloud/cloud.cfg.d/10_tinkerbell.cfg
          UID: 0
          GID: 0
          MODE: 0600
          DIRMODE: 0700
          CONTENTS: |
            datasource:
              Ec2:
                metadata_urls: ["http://<TINKERBELL_VIP>:7080"]
                strict_id: false
            system_info:
              default_user:
                name: tink
                groups: [wheel, adm]
                sudo: ["ALL=(ALL) NOPASSWD:ALL"]
                shell: /bin/bash
            manage_etc_hosts: localhost
            warnings:
              dsid_missing_source: off
      - name: "add tink cloud-init ds-config"
        image: quay.io/tinkerbell/actions/writefile
        timeout: 90
        environment:
          DEST_DISK: {{ formatPartition ( index .Hardware.Disks 0 ) 3 }}
          FS_TYPE: ext4
          DEST_PATH: /etc/cloud/ds-identify.cfg
          UID: 0
          GID: 0
          MODE: 0600
          DIRMODE: 0700
          CONTENTS: |
            datasource: Ec2
      - name: "kexec image"
        image: ghcr.io/jacobweinstock/waitdaemon:latest
        timeout: 90
        pid: host
        environment:
          BLOCK_DEVICE: {{ formatPartition ( index .Hardware.Disks 0 ) 1 }}
          FS_TYPE: vfat
          IMAGE: quay.io/tinkerbell/actions/kexec
          WAIT_SECONDS: 5
        volumes:
          - /var/run/docker.sock:/var/run/docker.sock
`

func TestConvertTemplateNestedDiskIndex(t *testing.T) {
	c, err := ConvertTemplate(captQuickStartTemplate, testFuncs())
	if err != nil {
		t.Fatal(err)
	}
	acts := c.Tasks[0].Spec.Actions
	if got, want := acts[1].Env["DEST_DISK"], "{{formatPartition ((index .hardware.spec.storageDevices 0).name) 3}}"; got != want {
		t.Errorf("DEST_DISK = %q, want %q", got, want)
	}
	funcs := testFuncs()
	funcs["formatPartition"] = func(dev string, n int) string { return fmt.Sprintf("%s%d", dev, n) }
	data := map[string]any{"hardware": map[string]any{"spec": map[string]any{
		"storageDevices": []any{map[string]any{"name": "/dev/sda"}},
	}}}
	for i, want := range map[int]string{0: "/dev/sda", 1: "/dev/sda3", 3: "/dev/sda1"} {
		key := "DEST_DISK"
		if i == 3 {
			key = "BLOCK_DEVICE"
		}
		tm := template.Must(template.New(key).Funcs(funcs).Option("missingkey=error").Parse(acts[i].Env[key]))
		var b bytes.Buffer
		if err := tm.Execute(&b, data); err != nil {
			t.Fatal(err)
		}
		if b.String() != want {
			t.Errorf("action %d %s rendered %q, want %q", i, key, b.String(), want)
		}
	}
	// A structure function in a nested pipeline is still refused.
	if _, err := ConvertTemplate(strings.Replace(captQuickStartTemplate, "( index .Hardware.Disks 0 ) 3", "(toYaml .Hardware.Disks) 3", 1), testFuncs()); !IsNotConvertible(err) {
		t.Errorf("toYaml in a nested pipeline: want NotConvertibleError, got %v", err)
	}
}
