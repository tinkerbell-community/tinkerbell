package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/ec2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockReader struct {
	hw  *v1alpha2.Hardware
	err error
	// opts is the filter of the last lookup.
	opts data.HardwareFilter
}

func (m *mockReader) FilterHardwareV1Alpha2(_ context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error) {
	m.opts = opts
	return m.hw, m.err
}

// notFoundError satisfies the notFounder interface and the apierrors.APIStatus interface.
type notFoundError struct {
	msg string
}

func (e notFoundError) Error() string  { return e.msg }
func (e notFoundError) NotFound() bool { return true }
func (e notFoundError) Status() metav1.Status {
	return metav1.Status{
		Reason: metav1.StatusReasonNotFound,
		Code:   http.StatusNotFound,
	}
}

func ptr[T any](v T) *T { return &v }

// createdAsV1Alpha1 returns hw as the API server stores a Hardware created through v1alpha1.
func createdAsV1Alpha1(tb testing.TB, hw *v1alpha1.Hardware) *v1alpha2.Hardware {
	tb.Helper()
	stored := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(hw, stored); err != nil {
		tb.Fatal(err)
	}
	return stored
}

// v1alpha1Fixtures are Hardware created through v1alpha1, each with data some endpoint serves.
func v1alpha1Fixtures() map[string]*v1alpha1.Hardware {
	userData := "#cloud-config\nhostname: my-host\n"
	return map[string]*v1alpha1.Hardware{
		"empty": {},
		"full metadata": {
			Spec: v1alpha1.HardwareSpec{
				UserData: &userData,
				Metadata: &v1alpha1.HardwareMetadata{
					Instance: &v1alpha1.MetadataInstance{
						ID:       "inst-123",
						Hostname: "my-host",
						Tags:     []string{"tag1", "tag2"},
						SSHKeys:  []string{"ssh-ed25519 AAAATESTKEY1 a@test", "ssh-rsa AAAATESTKEY2 b@example"},
						OperatingSystem: &v1alpha1.MetadataInstanceOperatingSystem{
							Slug:     "ubuntu",
							Distro:   "ubuntu",
							Version:  "20.04",
							ImageTag: "v1",
						},
						Ips: []*v1alpha1.MetadataInstanceIP{
							{Address: "1.2.3.4", Family: 4, Public: true},
							{Address: "10.0.0.1", Family: 4, Public: false},
							{Address: "2001:db8::1", Family: 6, Public: true},
						},
					},
					Facility: &v1alpha1.MetadataFacility{
						PlanSlug:     "c3.small.x86",
						FacilityCode: "sjc1",
					},
				},
			},
		},
		"facility only": {
			Spec: v1alpha1.HardwareSpec{
				Metadata: &v1alpha1.HardwareMetadata{
					Facility: &v1alpha1.MetadataFacility{PlanSlug: "plan-a", FacilityCode: "dc1"},
				},
			},
		},
		"userdata only": {
			Spec: v1alpha1.HardwareSpec{UserData: &userData},
		},
		// The interface has an address and a hostname, which v1alpha1 does not serve as EC2
		// metadata: it serves metadata.instance.
		"interface with hostname": {
			Spec: v1alpha1.HardwareSpec{
				Interfaces: []v1alpha1.Interface{{DHCP: &v1alpha1.DHCP{
					MAC:      "3c:ec:ef:4c:4f:54",
					Hostname: "dhcp-host",
					IP:       &v1alpha1.IP{Address: "10.0.0.1", Netmask: "255.255.255.0"},
				}}},
				Metadata: &v1alpha1.HardwareMetadata{Instance: &v1alpha1.MetadataInstance{ID: "inst-1", Hostname: "instance-host"}},
			},
		},
		"interface without instance": {
			Spec: v1alpha1.HardwareSpec{
				Interfaces: []v1alpha1.Interface{{DHCP: &v1alpha1.DHCP{
					MAC:      "3c:ec:ef:4c:4f:54",
					Hostname: "dhcp-host",
					IP:       &v1alpha1.IP{Address: "10.0.0.1", Netmask: "255.255.255.0"},
				}}},
			},
		},
		"storage": {
			Spec: v1alpha1.HardwareSpec{
				Metadata: &v1alpha1.HardwareMetadata{
					Instance: &v1alpha1.MetadataInstance{
						ID: "inst-456",
						Storage: &v1alpha1.MetadataInstanceStorage{
							Disks: []*v1alpha1.MetadataInstanceStorageDisk{{
								Device:     "/dev/sda",
								WipeTable:  true,
								Partitions: []*v1alpha1.MetadataInstanceStorageDiskPartition{{Label: "root", Number: 1, Size: 1000000}},
							}},
						},
					},
				},
			},
		},
	}
}

// TestV1Alpha1Parity serves each Hardware created through v1alpha1 the way tootles did, from the
// v1alpha1 Hardware, and the way it does now, from its v1alpha2 form: the responses must match.
func TestV1Alpha1Parity(t *testing.T) {
	for name, hw := range v1alpha1Fixtures() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			b := New(&mockReader{hw: createdAsV1Alpha1(t, hw)})

			wantEC2 := toEC2Instance(*hw)
			gotEC2, err := b.GetEC2Instance(ctx, "10.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(wantEC2, gotEC2); diff != "" {
				t.Errorf("EC2 by IP (-v1alpha1 +v1alpha2):\n%s", diff)
			}
			gotEC2, err = b.GetEC2InstanceByInstanceID(ctx, "inst-123")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(wantEC2, gotEC2); diff != "" {
				t.Errorf("EC2 by instance ID (-v1alpha1 +v1alpha2):\n%s", diff)
			}

			wantHack, err := toHackInstance(*hw)
			if err != nil {
				t.Fatal(err)
			}
			gotHack, err := b.GetHackInstance(ctx, "10.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(wantHack, gotHack); diff != "" {
				t.Errorf("hack (-v1alpha1 +v1alpha2):\n%s", diff)
			}
		})
	}
}

// v1alpha2Native is a Hardware created as v1alpha2 with two network interfaces.
func v1alpha2Native() *v1alpha2.Hardware {
	return &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "native", Namespace: "tink"},
		Spec: v1alpha2.HardwareSpec{
			Instance: &v1alpha2.Instance{
				LookupID:   "lookup-1",
				SSHKeys:    []string{"ssh-ed25519 AAAATESTKEY1 a@test"},
				Userdata:   ptr("#cloud-config\n"),
				Vendordata: ptr("#cloud-config\npackages: [vim]\n"),
			},
			NetworkInterfaces: v1alpha2.NetworkInterfaces{
				"3c:ec:ef:4c:4f:54": {IPAM: &v1alpha2.IPAM{
					IPv4: &v1alpha2.IP{Address: "10.0.0.1", Prefix: "24", Hostname: ptr("host4")},
					IPv6: &v1alpha2.IP{Address: "2001:db8::1", Prefix: "64", Hostname: ptr("host6")},
				}},
				"3c:ec:ef:4c:4f:55": {IPAM: &v1alpha2.IPAM{
					IPv4: &v1alpha2.IP{Address: "10.0.1.1", Prefix: "24", Hostname: ptr("other")},
				}},
			},
		},
	}
}

func TestEC2InstanceV1Alpha2(t *testing.T) {
	oneInterface := v1alpha2Native()
	delete(oneInterface.Spec.NetworkInterfaces, "3c:ec:ef:4c:4f:55")
	noHostname := v1alpha2Native()
	noHostname.Spec.NetworkInterfaces["3c:ec:ef:4c:4f:54"].IPAM.IPv4.Hostname = nil
	identity := data.Metadata{InstanceID: "lookup-1", PublicKeys: []string{"ssh-ed25519 AAAATESTKEY1 a@test"}}
	withInterface := func(hostname, ipv4, ipv6 string) data.Ec2Instance {
		m := identity
		m.Hostname, m.LocalHostname, m.LocalIPv4, m.PublicIPv6 = hostname, hostname, ipv4, ipv6
		return data.Ec2Instance{Userdata: "#cloud-config\n", Metadata: m}
	}
	tests := map[string]struct {
		hw         *v1alpha2.Hardware
		ip         string
		instanceID string
		want       data.Ec2Instance
	}{
		"IPv4 request": {
			hw: v1alpha2Native(), ip: "10.0.0.1",
			want: withInterface("host4", "10.0.0.1", "2001:db8::1"),
		},
		"IPv6 request written differently": {
			hw: v1alpha2Native(), ip: "2001:DB8:0::1",
			want: withInterface("host6", "10.0.0.1", "2001:db8::1"),
		},
		"other interface": {
			hw: v1alpha2Native(), ip: "10.0.1.1",
			want: withInterface("other", "10.0.1.1", ""),
		},
		"hostname of the other family": {
			hw: noHostname, ip: "10.0.0.1",
			want: withInterface("host6", "10.0.0.1", "2001:db8::1"),
		},
		"instance ID with several interfaces": {
			hw: v1alpha2Native(), instanceID: "lookup-1",
			want: data.Ec2Instance{Userdata: "#cloud-config\n", Metadata: identity},
		},
		"instance ID with one interface": {
			hw: oneInterface, instanceID: "lookup-1",
			want: withInterface("host4", "10.0.0.1", "2001:db8::1"),
		},
		"no instance": {
			hw: &v1alpha2.Hardware{Spec: v1alpha2.HardwareSpec{NetworkInterfaces: v1alpha2.NetworkInterfaces{
				"3c:ec:ef:4c:4f:54": {IPAM: &v1alpha2.IPAM{IPv4: &v1alpha2.IP{Address: "10.0.0.1"}}},
			}}},
			ip:   "10.0.0.1",
			want: data.Ec2Instance{Metadata: data.Metadata{LocalIPv4: "10.0.0.1"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			reader := &mockReader{hw: tt.hw}
			b := New(reader)
			var got data.Ec2Instance
			var err error
			if tt.instanceID != "" {
				got, err = b.GetEC2InstanceByInstanceID(context.Background(), tt.instanceID)
				if reader.opts != (data.HardwareFilter{ByInstanceID: tt.instanceID}) {
					t.Errorf("filter = %+v, want a lookup by instance ID", reader.opts)
				}
			} else {
				got, err = b.GetEC2Instance(context.Background(), tt.ip)
				if reader.opts != (data.HardwareFilter{ByIPAddress: tt.ip}) {
					t.Errorf("filter = %+v, want a lookup by IP", reader.opts)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestHackInstanceV1Alpha2(t *testing.T) {
	got, err := New(&mockReader{hw: v1alpha2Native()}).GetHackInstance(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(data.HackInstance{}, got); diff != "" {
		t.Fatalf("Hardware created as v1alpha2 has no storage metadata (-want +got):\n%s", diff)
	}
}

func TestErrors(t *testing.T) {
	broken := &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{conversion.AnnotationV1Alpha1: "{"}}}
	tests := map[string]struct {
		reader       *mockReader
		wantNotFound bool
	}{
		"not found":         {reader: &mockReader{err: notFoundError{msg: "hardware not found: 10.0.0.1"}}, wantNotFound: true},
		"wrapped not found": {reader: &mockReader{err: fmt.Errorf("wrap: %w", notFoundError{msg: "gone"})}, wantNotFound: true},
		"other error":       {reader: &mockReader{err: errors.New("connection refused")}},
		"conversion error":  {reader: &mockReader{hw: broken}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			b := New(tt.reader)
			check := func(endpoint string, err, notFound error) {
				t.Helper()
				switch {
				case err == nil:
					t.Errorf("%s: want an error", endpoint)
				case tt.wantNotFound && !errors.Is(err, notFound):
					t.Errorf("%s: err = %v, want it to wrap %v", endpoint, err, notFound)
				case !tt.wantNotFound && errors.Is(err, notFound):
					t.Errorf("%s: err = %v, must not be not found", endpoint, err)
				}
			}
			_, err := b.GetEC2Instance(ctx, "10.0.0.1")
			check("EC2 by IP", err, ec2.ErrInstanceNotFound)
			_, err = b.GetEC2InstanceByInstanceID(ctx, "inst-1")
			check("EC2 by instance ID", err, ec2.ErrInstanceNotFound)
			if _, err := b.GetHackInstance(ctx, "10.0.0.1"); err == nil {
				t.Error("hack: want an error")
			}
		})
	}
}

func TestToEC2Instance(t *testing.T) {
	userData := "cloud-init data"
	tests := map[string]struct {
		hw   v1alpha1.Hardware
		want data.Ec2Instance
	}{
		"nil metadata": {
			hw:   v1alpha1.Hardware{},
			want: data.Ec2Instance{},
		},
		"nil instance": {
			hw: v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Metadata: &v1alpha1.HardwareMetadata{},
				},
			},
			want: data.Ec2Instance{},
		},
		"facility only": {
			hw: v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Metadata: &v1alpha1.HardwareMetadata{
						Facility: &v1alpha1.MetadataFacility{
							PlanSlug:     "plan-a",
							FacilityCode: "dc1",
						},
					},
				},
			},
			want: data.Ec2Instance{
				Metadata: data.Metadata{
					Plan:     "plan-a",
					Facility: "dc1",
				},
			},
		},
		"userdata": {
			hw: v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					UserData: &userData,
				},
			},
			want: data.Ec2Instance{
				Userdata: "cloud-init data",
			},
		},
		"first matching IPs chosen": {
			hw: v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Metadata: &v1alpha1.HardwareMetadata{
						Instance: &v1alpha1.MetadataInstance{
							Ips: []*v1alpha1.MetadataInstanceIP{
								{Address: "pub4-first", Family: 4, Public: true},
								{Address: "pub4-second", Family: 4, Public: true},
								{Address: "priv4-first", Family: 4, Public: false},
								{Address: "priv4-second", Family: 4, Public: false},
								{Address: "pub6-first", Family: 6},
								{Address: "pub6-second", Family: 6},
							},
						},
					},
				},
			},
			want: data.Ec2Instance{
				Metadata: data.Metadata{
					PublicIPv4: "pub4-first",
					LocalIPv4:  "priv4-first",
					PublicIPv6: "pub6-first",
				},
			},
		},
		"instance with ssh keys": {
			hw: v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Metadata: &v1alpha1.HardwareMetadata{
						Instance: &v1alpha1.MetadataInstance{
							ID:       "inst-789",
							Hostname: "my-host",
							SSHKeys: []string{
								"ssh-ed25519 AAAATESTKEY1 a@test",
								"ssh-rsa AAAATESTKEY2 b@example",
							},
						},
					},
				},
			},
			want: data.Ec2Instance{
				Metadata: data.Metadata{
					InstanceID:    "inst-789",
					Hostname:      "my-host",
					LocalHostname: "my-host",
					PublicKeys: []string{
						"ssh-ed25519 AAAATESTKEY1 a@test",
						"ssh-rsa AAAATESTKEY2 b@example",
					},
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := toEC2Instance(tt.hw)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"nil error":     {err: nil, want: false},
		"generic error": {err: fmt.Errorf("oops"), want: false},
		"not found error": {
			err:  notFoundError{msg: "gone"},
			want: true,
		},
		"wrapped not found": {
			err:  fmt.Errorf("wrap: %w", notFoundError{msg: "gone"}),
			want: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isNotFound(tt.err); got != tt.want {
				t.Fatalf("isNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}
