package kube

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func bmcCredentials(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tink"},
		Data:       map[string][]byte{"username": []byte(name + "-user"), "password": []byte(name + "-pass")},
	}
}

func bmcMachineV1Alpha1(name string, opts *bmc.ProviderOptions) *bmc.Machine {
	return &bmc.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tink"},
		Spec: bmc.MachineSpec{Connection: bmc.Connection{
			Host:            "10.0.0.10",
			Port:            443,
			AuthSecretRef:   corev1.SecretReference{Name: "machine-creds", Namespace: "tink"},
			ProviderOptions: opts,
		}},
	}
}

func machineIn(namespace string, m *bmc.Machine) *bmc.Machine {
	m.Namespace = namespace
	return m
}

// hardwareWithBMCRef is a v1alpha1 Hardware whose BMC is the bmc Machine named machine.
func hardwareWithBMCRef(machine string, sshKeys ...string) *v1alpha1.Hardware {
	hw := &v1alpha1.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "sol1", Namespace: "tink"},
		Spec: v1alpha1.HardwareSpec{
			Interfaces: []v1alpha1.Interface{{DHCP: &v1alpha1.DHCP{MAC: "3c:ec:ef:4c:4f:54"}}},
		},
	}
	if machine != "" {
		hw.Spec.BMCRef = &corev1.TypedLocalObjectReference{APIGroup: ptr("bmc.tinkerbell.org"), Kind: "Machine", Name: machine}
	}
	if len(sshKeys) > 0 {
		hw.Spec.Metadata = &v1alpha1.HardwareMetadata{Instance: &v1alpha1.MetadataInstance{SSHKeys: sshKeys}}
	}
	return hw
}

// TestFilterBMCMachineV1Alpha2Machine reads the BMC of Hardware created
// through v1alpha1 as SecondStar read it from v1alpha1: from the bmc Machine its
// v1alpha1 bmcRef names, in the Hardware's namespace, with the IPMI port and
// cipher suite of the Machine's IPMI provider options.
func TestFilterBMCMachineV1Alpha2Machine(t *testing.T) {
	key1, key2 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAa1 a@example.org", "ssh-rsa AAAAB3 b@example.org"
	machineBMC := func(port int, cipherSuite string, sshKeys ...string) *data.BMCMachine {
		return &data.BMCMachine{
			Host: "10.0.0.10", Port: port, CipherSuite: cipherSuite, User: "machine-creds-user", Pass: "machine-creds-pass",
			SSHPublicKeys: sshKeys,
		}
	}
	tests := map[string]struct {
		hw      *v1alpha1.Hardware
		objs    []client.Object
		want    *data.BMCMachine
		wantErr bool
	}{
		"IPMI options": {
			hw:   hardwareWithBMCRef("bmc1", key1),
			objs: []client.Object{bmcMachineV1Alpha1("bmc1", &bmc.ProviderOptions{IPMI: &bmc.IPMITOOLOptions{Port: 6230, CipherSuite: "3"}})},
			want: machineBMC(6230, "3", key1),
		},
		"IPMI defaults": {
			hw:   hardwareWithBMCRef("bmc1", key1, key2),
			objs: []client.Object{bmcMachineV1Alpha1("bmc1", &bmc.ProviderOptions{IPMI: &bmc.IPMITOOLOptions{}})},
			want: machineBMC(623, "17", key1, key2),
		},
		"no provider options": {
			hw:   hardwareWithBMCRef("bmc1"),
			objs: []client.Object{bmcMachineV1Alpha1("bmc1", nil)},
			want: machineBMC(0, ""),
		},
		"other providers' options": {
			hw: hardwareWithBMCRef("bmc1", key1),
			objs: []client.Object{bmcMachineV1Alpha1("bmc1", &bmc.ProviderOptions{
				Redfish:        &bmc.RedfishOptions{Port: 8443},
				PreferredOrder: []bmc.ProviderName{"gofish"},
			})},
			want: machineBMC(0, "", key1),
		},
		"no bmcRef": {
			hw:      hardwareWithBMCRef(""),
			objs:    []client.Object{bmcMachineV1Alpha1("bmc1", nil)},
			wantErr: true,
		},
		"Machine not found": {
			hw:      hardwareWithBMCRef("bmc1"),
			objs:    []client.Object{bmcMachineV1Alpha1("bmc2", nil)},
			wantErr: true,
		},
		"Machine in another namespace": {
			hw:      hardwareWithBMCRef("bmc1"),
			objs:    []client.Object{machineIn("other", bmcMachineV1Alpha1("bmc1", nil))},
			wantErr: true,
		},
		"same Machine name in another namespace": {
			hw: hardwareWithBMCRef("bmc1"),
			objs: []client.Object{
				bmcMachineV1Alpha1("bmc1", nil),
				machineIn("other", bmcMachineV1Alpha1("bmc1", &bmc.ProviderOptions{IPMI: &bmc.IPMITOOLOptions{Port: 1}})),
			},
			want: machineBMC(0, ""),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			objs := append([]client.Object{createdAsV1Alpha1(t, tt.hw), bmcCredentials("machine-creds")}, tt.objs...)
			b := newFakeBackend(t, Backend{}, objs...)

			got, err := b.FilterBMCMachineV1Alpha2(context.Background(), data.HardwareFilter{ByName: tt.hw.Name})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

func hardwareWithBMCConnection(conn *bmcv1alpha2.Connection, sshKeys ...string) *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "sol2", Namespace: "tink"},
		Spec: v1alpha2.HardwareSpec{
			BMC:      &v1alpha2.BMC{Connection: conn},
			Instance: &v1alpha2.Instance{SSHKeys: sshKeys},
		},
	}
}

func TestFilterBMCMachineV1Alpha2(t *testing.T) {
	credentials := bmcv1alpha2.SimpleReference{Name: "hw-creds", Namespace: "tink"}
	createdThroughV1Alpha1 := func(conn *bmcv1alpha2.Connection) *v1alpha2.Hardware {
		hw := createdAsV1Alpha1(t, hardwareWithBMCRef("bmc1", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAa1 a@example.org"))
		hw.Spec.BMC = &v1alpha2.BMC{Connection: conn}
		return hw
	}
	tests := map[string]struct {
		hw      *v1alpha2.Hardware
		want    *data.BMCMachine
		wantErr bool
	}{
		"connection with IPMI options": {
			hw: hardwareWithBMCConnection(&bmcv1alpha2.Connection{
				Host:            "10.0.0.20",
				Port:            443,
				CredentialsRef:  credentials,
				ProviderOptions: &bmcv1alpha2.ProviderOptions{IPMI: &bmcv1alpha2.IPMITOOLOptions{Port: 6230, CipherSuite: "3"}},
			}, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAa1 a@example.org"),
			want: &data.BMCMachine{
				Host: "10.0.0.20", Port: 6230, CipherSuite: "3", User: "hw-creds-user", Pass: "hw-creds-pass",
				SSHPublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAa1 a@example.org"},
			},
		},
		"connection with IPMI defaults": {
			hw: hardwareWithBMCConnection(&bmcv1alpha2.Connection{
				Host:            "10.0.0.20",
				CredentialsRef:  credentials,
				ProviderOptions: &bmcv1alpha2.ProviderOptions{IPMI: &bmcv1alpha2.IPMITOOLOptions{}},
			}),
			want: &data.BMCMachine{Host: "10.0.0.20", Port: 623, CipherSuite: "17", User: "hw-creds-user", Pass: "hw-creds-pass"},
		},
		// SecondStar defaults the port when the IPMI options do not set one.
		"connection without IPMI options": {
			hw:   hardwareWithBMCConnection(&bmcv1alpha2.Connection{Host: "10.0.0.20", Port: 623, CredentialsRef: credentials}),
			want: &data.BMCMachine{Host: "10.0.0.20", User: "hw-creds-user", Pass: "hw-creds-pass"},
		},
		"connection wins over bmcRef": {
			hw: createdThroughV1Alpha1(&bmcv1alpha2.Connection{Host: "10.0.0.20", CredentialsRef: credentials}),
			want: &data.BMCMachine{
				Host: "10.0.0.20", User: "hw-creds-user", Pass: "hw-creds-pass",
				SSHPublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAa1 a@example.org"},
			},
		},
		"bmcRef without connection": {
			hw: createdThroughV1Alpha1(nil),
			want: &data.BMCMachine{
				Host: "10.0.0.10", Port: 6230, CipherSuite: "17", User: "machine-creds-user", Pass: "machine-creds-pass",
				SSHPublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAa1 a@example.org"},
			},
		},
		"no connection and no bmcRef": {
			hw:      &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "sol3", Namespace: "tink"}},
			wantErr: true,
		},
		"credentials not found": {
			hw: hardwareWithBMCConnection(&bmcv1alpha2.Connection{
				Host:           "10.0.0.20",
				CredentialsRef: bmcv1alpha2.SimpleReference{Name: "missing", Namespace: "tink"},
			}),
			wantErr: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b := newFakeBackend(t, Backend{},
				tt.hw,
				bmcCredentials("hw-creds"),
				bmcCredentials("machine-creds"),
				bmcMachineV1Alpha1("bmc1", &bmc.ProviderOptions{IPMI: &bmc.IPMITOOLOptions{Port: 6230}}),
			)
			got, err := b.FilterBMCMachineV1Alpha2(context.Background(), data.HardwareFilter{ByName: tt.hw.Name})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

// TestFilterBMCMachineV1Alpha2Rendered reads the BMC of rendered Hardware: a
// templated bmcRef of Hardware created through v1alpha1 names the Machine its
// rendering names, and the SSH keys are the rendered ones.
func TestFilterBMCMachineV1Alpha2Rendered(t *testing.T) {
	ctx := context.Background()
	hw := templatedV1Alpha1()
	hw.Spec.UserData = nil
	hw.Spec.BMCRef = &corev1.TypedLocalObjectReference{Kind: "Machine", Name: "{{ .references.net.data.machine }}"}
	hw.Spec.Metadata = &v1alpha1.HardwareMetadata{Instance: &v1alpha1.MetadataInstance{SSHKeys: []string{"{{ .references.net.data.key }}"}}}
	stored := createdAsV1Alpha1(t, hw)
	machine := &bmc.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "tink"}, Spec: bmc.MachineSpec{Connection: bmc.Connection{
		Host: "host", AuthSecretRef: corev1.SecretReference{Name: "credentials", Namespace: "tink"},
	}}}
	b := newFakeBackend(t, Backend{}, stored, machine, bmcCredentials("credentials"))
	hws := &fakeHardware{}
	b.store = newTestStore(hws, &fakeResolver{refs: map[string]any{"net": map[string]any{"data": map[string]any{"machine": "m", "key": "k"}}}}, &fakeInformers{})
	defer b.store.queue.ShutDown()
	hws.set(stored)
	b.store.render(ctx, client.ObjectKeyFromObject(hw))

	got, err := b.FilterBMCMachineV1Alpha2(ctx, data.HardwareFilter{ByName: hw.Name, InNamespace: hw.Namespace})
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "host" || len(got.SSHPublicKeys) != 1 || got.SSHPublicKeys[0] != "k" {
		t.Fatalf("FilterBMCMachineV1Alpha2 = %+v, want the rendered Machine name and SSH key", got)
	}
}

// TestFilterBMCMachineV1Alpha2Lookup keeps the SSH user to Hardware lookup:
// the user names the Hardware, in any namespace, and must name exactly one.
func TestFilterBMCMachineV1Alpha2Lookup(t *testing.T) {
	conn := &bmcv1alpha2.Connection{Host: "10.0.0.20", CredentialsRef: bmcv1alpha2.SimpleReference{Name: "hw-creds", Namespace: "tink"}}
	other := hardwareWithBMCConnection(conn)
	other.Namespace = "other"
	b := newFakeBackend(t, Backend{}, hardwareWithBMCConnection(conn), other, bmcCredentials("hw-creds"),
		&v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "single", Namespace: "other"}, Spec: v1alpha2.HardwareSpec{BMC: &v1alpha2.BMC{Connection: conn}}})

	if got, err := b.FilterBMCMachineV1Alpha2(context.Background(), data.HardwareFilter{ByName: "single"}); err != nil || got.Host != conn.Host {
		t.Fatalf("got %v, %v; want the BMC of the Hardware named single", got, err)
	}
	if _, err := b.FilterBMCMachineV1Alpha2(context.Background(), data.HardwareFilter{ByName: "sol2"}); err == nil {
		t.Fatal("a name shared by Hardware in two namespaces must not resolve")
	}
	if _, err := b.FilterBMCMachineV1Alpha2(context.Background(), data.HardwareFilter{ByName: "missing"}); err == nil {
		t.Fatal("an unknown name must not resolve")
	}
}
