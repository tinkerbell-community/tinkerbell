package file

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"sigs.k8s.io/yaml"
)

const mixedVersions = `
- apiVersion: tinkerbell.org/v1alpha1
  kind: Hardware
  metadata:
    name: old
    namespace: default
  spec:
    interfaces:
    - dhcp:
        mac: "08:00:27:29:4e:67"
        lease_time: 3600
        ip:
          address: "192.168.2.153"
          netmask: "255.255.255.0"
      netboot:
        allowPXE: true
- apiVersion: tinkerbell.org/v1alpha2
  kind: Hardware
  metadata:
    name: new
    namespace: other
  spec:
    agentID: agent-new
    instance:
      lookupID: lookup-new
    networkInterfaces:
      "52:54:00:aa:88:2a":
        ipam:
          ipv4:
            address: "192.168.2.15"
            prefix: "24"
          ipv6:
            address: "2001:db8::15"
`

func TestFilterHardware(t *testing.T) {
	name, err := createFile([]byte(mixedVersions))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(name)
	w, err := NewWatcher(logr.Discard(), name)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		opts     data.HardwareFilter
		wantName string
	}{
		"v1alpha1 entry by MAC":                {opts: data.HardwareFilter{ByMACAddress: "08:00:27:29:4e:67"}, wantName: "old"},
		"v1alpha1 entry by upper case MAC":     {opts: data.HardwareFilter{ByMACAddress: "08:00:27:29:4E:67"}, wantName: "old"},
		"v1alpha1 entry by IP":                 {opts: data.HardwareFilter{ByIPAddress: "192.168.2.153"}, wantName: "old"},
		"v1alpha2 entry by MAC":                {opts: data.HardwareFilter{ByMACAddress: "52:54:00:aa:88:2a"}, wantName: "new"},
		"v1alpha2 entry by IPv4":               {opts: data.HardwareFilter{ByIPAddress: "192.168.2.15"}, wantName: "new"},
		"v1alpha2 entry by IPv6, other form":   {opts: data.HardwareFilter{ByIPAddress: "2001:db8:0::15"}, wantName: "new"},
		"v1alpha2 entry by agent ID":           {opts: data.HardwareFilter{ByAgentID: "agent-new"}, wantName: "new"},
		"v1alpha2 entry by lookup ID":          {opts: data.HardwareFilter{ByInstanceID: "lookup-new"}, wantName: "new"},
		"v1alpha2 entry by name and namespace": {opts: data.HardwareFilter{ByName: "new", InNamespace: "other"}, wantName: "new"},
		"name in another namespace":            {opts: data.HardwareFilter{ByName: "new", InNamespace: "default"}},
		"no selector":                          {opts: data.HardwareFilter{InNamespace: "default"}},
		"no match":                             {opts: data.HardwareFilter{ByMACAddress: "00:01:02:03:04:05"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			hw, err := w.FilterHardware(context.Background(), tt.opts)
			if tt.wantName == "" {
				var nf hardwareNotFoundError
				if !errors.As(err, &nf) {
					t.Fatalf("FilterHardware() error = %v, want NotFound error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if hw.Name != tt.wantName {
				t.Fatalf("got Hardware %q, want %q", hw.Name, tt.wantName)
			}
		})
	}
}

// TestFilterHardwareConverts checks that a v1alpha1 entry is served as
// its conversion, annotation included, so that readers can recover what only
// v1alpha1 holds.
func TestFilterHardwareConverts(t *testing.T) {
	w, err := NewWatcher(logr.Discard(), "testdata/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var entries []v1alpha1.Hardware
	if err := yaml.Unmarshal(w.data, &entries); err != nil {
		t.Fatal(err)
	}
	old := &entries[0]
	if old.Spec.Interfaces[0].DHCP.MAC != "08:00:27:29:4e:67" {
		t.Fatalf("first entry has MAC %s, want 08:00:27:29:4e:67", old.Spec.Interfaces[0].DHCP.MAC)
	}
	got, err := w.FilterHardware(context.Background(), data.HardwareFilter{ByMACAddress: "08:00:27:29:4e:67"})
	if err != nil {
		t.Fatal(err)
	}
	want := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(old, want); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	back := &v1alpha1.Hardware{}
	if err := conversion.HardwareToV1Alpha1(got, back); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(old.Spec, back.Spec); diff != "" {
		t.Fatalf("v1alpha1 view differs from the file: %s", diff)
	}
}

func TestFilterHardwareErrors(t *testing.T) {
	tests := map[string]struct {
		content string
		wantErr error
	}{
		"not yaml":          {content: "not a yaml file", wantErr: errFileFormat},
		"multiple matching": {content: "[{spec: {agentID: a}}, {spec: {agentID: a}}]"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f, err := createFile([]byte(tt.content))
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f)
			w, err := NewWatcher(logr.Discard(), f)
			if err != nil {
				t.Fatal(err)
			}
			_, err = w.FilterHardware(context.Background(), data.HardwareFilter{ByAgentID: "a"})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			var mf foundMultipleHardwareError
			if !errors.As(err, &mf) {
				t.Fatalf("error = %v, want multiple found", err)
			}
		})
	}
}
