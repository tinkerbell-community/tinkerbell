package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestTemplateActions(t *testing.T) {
	tests := map[string]struct {
		actions     []bmc.Action
		hardware    *v1alpha1.Hardware
		wantActions []bmc.Action
		wantErr     bool
	}{
		"nil hardware returns actions unchanged": {
			actions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerOn)},
			},
			hardware: nil,
			wantActions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerOn)},
			},
		},
		"no template syntax returns actions unchanged": {
			actions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerOn)},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				},
			},
			wantActions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerOn)},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
		},
		"template first MAC address": {
			actions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerHardOff)},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: `http://172.17.1.1:7171/iso/{{ (index .Hardware.Interfaces 0).DHCP.MAC | replace ":" "-" }}/hook.iso`,
					Kind:     bmc.VirtualMediaCD,
				}},
				{PowerAction: valueToPointer(bmc.PowerOn)},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				},
			},
			wantActions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerHardOff)},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://172.17.1.1:7171/iso/52-54-00-12-34-01/hook.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
				{PowerAction: valueToPointer(bmc.PowerOn)},
			},
		},
		"template second MAC address": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: `http://example.com/iso/{{ (index .Hardware.Interfaces 1).DHCP.MAC | replace ":" "-" }}/image.iso`,
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:02"}},
					},
				},
			},
			wantActions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/iso/52-54-00-12-34-02/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
		},
		"empty mediaURL with template syntax stays empty": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				},
			},
			wantActions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
		},
		"hardware with no interfaces": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{},
				},
			},
			wantActions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
		},
		"hardware with interface but no DHCP": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{Netboot: &v1alpha1.Netboot{AllowPXE: valueToPointer(true)}},
					},
				},
			},
			wantActions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
		},
		"invalid template syntax returns error": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/{{ .Invalid",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				},
			},
			wantErr: true,
		},
		"index out of range returns error": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/{{ (index .Hardware.Interfaces 5).DHCP.MAC }}/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				},
			},
			wantErr: true,
		},
		"access MAC with replace returns dash format": {
			actions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: `http://example.com/mac/{{ (index .Hardware.Interfaces 0).DHCP.MAC | replace ":" "-" }}/image.iso`,
					Kind:     bmc.VirtualMediaCD,
				}},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				},
			},
			wantActions: []bmc.Action{
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://example.com/mac/52-54-00-12-34-01/image.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
			},
		},
		"multiple actions with templates": {
			actions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerHardOff)},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "",
					Kind:     bmc.VirtualMediaCD,
				}},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: `http://172.17.1.1:7171/iso/{{ (index .Hardware.Interfaces 0).DHCP.MAC | replace ":" "-" }}/hook.iso`,
					Kind:     bmc.VirtualMediaCD,
				}},
				{BootDevice: &bmc.BootDeviceConfig{
					Device:  bmc.CDROM,
					EFIBoot: true,
				}},
				{PowerAction: valueToPointer(bmc.PowerOn)},
			},
			hardware: &v1alpha1.Hardware{
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "aa:bb:cc:dd:ee:ff"}},
					},
				},
			},
			wantActions: []bmc.Action{
				{PowerAction: valueToPointer(bmc.PowerHardOff)},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "",
					Kind:     bmc.VirtualMediaCD,
				}},
				{VirtualMediaAction: &bmc.VirtualMediaAction{
					MediaURL: "http://172.17.1.1:7171/iso/aa-bb-cc-dd-ee-ff/hook.iso",
					Kind:     bmc.VirtualMediaCD,
				}},
				{BootDevice: &bmc.BootDeviceConfig{
					Device:  bmc.CDROM,
					EFIBoot: true,
				}},
				{PowerAction: valueToPointer(bmc.PowerOn)},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := templateActions(operationsOf(t, tc.actions), tc.hardware)
			if (err != nil) != tc.wantErr {
				t.Errorf("templateActions() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(operationsOf(t, tc.wantActions), got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("templateActions() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Interfaces have a MAC address: v1alpha2 keys interfaces by MAC, so an interface without one has no
// netboot setting to toggle (v1alpha1 kept it, though DHCP and netboot never served it).
func TestPrepareWorkflow(t *testing.T) {
	tests := map[string]struct {
		wantResult   reconcile.Result
		wantError    bool
		hardware     *v1alpha1.Hardware
		wantHardware *v1alpha1.Hardware
		workflow     *v1alpha1.Workflow
		wantWorkflow *v1alpha1.Workflow
		job          *bmc.Job
	}{
		"nothing to do": {
			wantResult:   reconcile.Result{},
			hardware:     &v1alpha1.Hardware{},
			wantHardware: &v1alpha1.Hardware{},
			workflow:     &v1alpha1.Workflow{},
			wantWorkflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					State: v1alpha1.WorkflowStatePending,
				},
			},
		},
		"toggle allowPXE": {
			wantResult: reconcile.Result{},
			hardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{
							DHCP: &v1alpha1.DHCP{MAC: "3c:ec:ef:4c:4f:54"},
							Netboot: &v1alpha1.Netboot{
								AllowPXE: valueToPointer(false),
							},
						},
					},
				},
			},
			wantHardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{
							DHCP: &v1alpha1.DHCP{MAC: "3c:ec:ef:4c:4f:54"},
							Netboot: &v1alpha1.Netboot{
								AllowPXE: valueToPointer(true),
							},
						},
					},
				},
			},
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workflow",
					Namespace: "default",
				},
				Spec: v1alpha1.WorkflowSpec{
					HardwareRef: "test-hardware",
					BootOptions: v1alpha1.BootOptions{
						ToggleAllowNetboot: true,
					},
				},
			},
			wantWorkflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					State: v1alpha1.WorkflowStatePending,
					BootOptions: v1alpha1.BootOptionsStatus{
						AllowNetboot: v1alpha1.AllowNetbootStatus{
							ToggledTrue: true,
						},
					},
					Conditions: []v1alpha1.WorkflowCondition{
						{
							Type:    v1alpha1.ToggleAllowNetbootTrue,
							Status:  metav1.ConditionTrue,
							Reason:  reasonComplete,
							Message: "set allowPXE to true",
						},
					},
				},
			},
		},
		"boot mode netboot": {
			wantResult: reconcile.Result{Requeue: true},
			hardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			wantHardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workflow",
					Namespace: "default",
				},
				Spec: v1alpha1.WorkflowSpec{
					HardwareRef: "test-hardware",
					BootOptions: v1alpha1.BootOptions{
						BootMode: v1alpha1.BootModeNetboot,
					},
				},
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							fmt.Sprintf("%s-test-workflow", jobNameNetboot): {ExistingJobDeleted: true},
						},
					},
				},
			},
			job: &bmc.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-test-workflow", jobNameCustombootPost),
					Namespace: "default",
				},
				Spec:   bmc.JobSpec{},
				Status: bmc.JobStatus{},
			},
			wantWorkflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							fmt.Sprintf("%s-test-workflow", jobNameNetboot): {ExistingJobDeleted: true},
						},
					},
					Conditions: []v1alpha1.WorkflowCondition{
						{
							Type:    "BootJobSetupComplete",
							Status:  "True",
							Reason:  reasonCreated,
							Message: messageJobCreated,
						},
					},
				},
			},
		},
		"boot mode iso": {
			wantResult: reconcile.Result{Requeue: true},
			hardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			wantHardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workflow",
					Namespace: "default",
				},
				Spec: v1alpha1.WorkflowSpec{
					HardwareRef: "test-hardware",
					BootOptions: v1alpha1.BootOptions{
						BootMode: v1alpha1.BootModeISO,
						ISOURL:   "http://example.com",
					},
				},
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							fmt.Sprintf("%s-test-workflow", jobNameISOMount): {ExistingJobDeleted: true},
						},
					},
				},
			},
			job: &bmc.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-test-workflow", jobNameCustombootPost),
					Namespace: "default",
				},
				Spec:   bmc.JobSpec{},
				Status: bmc.JobStatus{},
			},
			wantWorkflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							fmt.Sprintf("%s-test-workflow", jobNameISOMount): {ExistingJobDeleted: true},
						},
					},
					Conditions: []v1alpha1.WorkflowCondition{
						{
							Type:    "BootJobSetupComplete",
							Status:  "True",
							Reason:  reasonCreated,
							Message: messageJobCreated,
						},
					},
				},
			},
		},
		"boot mode customboot": {
			wantResult: reconcile.Result{Requeue: true},
			hardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			wantHardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workflow",
					Namespace: "default",
				},
				Spec: v1alpha1.WorkflowSpec{
					HardwareRef: "test-hardware",
					BootOptions: v1alpha1.BootOptions{
						BootMode: v1alpha1.BootModeCustomboot,
						CustombootConfig: v1alpha1.CustombootConfig{
							PreparingActions: []bmc.Action{
								{
									PowerAction: valueToPointer(bmc.PowerOn),
								},
							},
							PostActions: []bmc.Action{
								{
									PowerAction: valueToPointer(bmc.PowerHardOff),
								},
							},
						},
					},
				},
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							fmt.Sprintf("%s-test-workflow", jobNameCustombootPreparing): {ExistingJobDeleted: true},
						},
					},
				},
			},
			job: &bmc.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-test-workflow", jobNameCustombootPost),
					Namespace: "default",
				},
				Spec:   bmc.JobSpec{},
				Status: bmc.JobStatus{},
			},
			wantWorkflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							fmt.Sprintf("%s-test-workflow", jobNameCustombootPreparing): {ExistingJobDeleted: true},
						},
					},
					Conditions: []v1alpha1.WorkflowCondition{
						{
							Type:    "BootJobSetupComplete",
							Status:  "True",
							Reason:  reasonCreated,
							Message: messageJobCreated,
						},
					},
				},
			},
		},
		"boot mode customboot no preparing actions": {
			wantResult: reconcile.Result{Requeue: false},
			hardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			wantHardware: &v1alpha1.Hardware{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hardware",
					Namespace: "default",
				},
				Spec: v1alpha1.HardwareSpec{
					BMCRef: &v1.TypedLocalObjectReference{
						Name: "test-bmc",
						Kind: "machine.bmc.tinkerbell.org",
					},
				},
			},
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workflow",
					Namespace: "default",
				},
				Spec: v1alpha1.WorkflowSpec{
					HardwareRef: "test-hardware",
					BootOptions: v1alpha1.BootOptions{
						BootMode: v1alpha1.BootModeCustomboot,
						CustombootConfig: v1alpha1.CustombootConfig{
							PostActions: []bmc.Action{
								{
									PowerAction: valueToPointer(bmc.PowerHardOff),
								},
							},
						},
					},
				},
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{},
					},
				},
			},
			wantWorkflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{},
					},
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			wf, c := bootStepFixture(t, tc.workflow, tc.hardware, tc.job)
			s := &state{
				workflow: wf,
				client:   c,
				render:   func(context.Context, int) (*v1alpha2.Workflow, error) { return wf, nil },
			}
			ctx := context.Background()
			ctx = journal.New(ctx)
			result, err := s.prepareWorkflow(ctx)
			if (err != nil) != tc.wantError {
				t.Errorf("expected error: %v, got: %v", tc.wantError, err)
			}
			if diff := cmp.Diff(result, tc.wantResult); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
				t.Logf("journal: %v", journal.Journal(ctx))
			}

			// get the Hardware object in cluster
			if tc.hardware.Name != "" {
				gotHardware := readV1Hardware(t, s.client, types.NamespacedName{Name: tc.hardware.Name, Namespace: tc.hardware.Namespace})
				if diff := cmp.Diff(gotHardware.Spec, tc.wantHardware.Spec); diff != "" {
					t.Errorf("unexpected hardware (-want +got):\n%s", diff)
					for _, entry := range journal.Journal(ctx) {
						t.Logf("journal: %+v", entry)
					}
				}
			}

			if diff := cmp.Diff(v1Workflow(t, s.workflow).Status, tc.wantWorkflow.Status, cmpopts.IgnoreFields(v1alpha1.WorkflowCondition{}, "Time")); diff != "" {
				t.Errorf("unexpected workflow status (-want +got):\n%s", diff)
				for _, entry := range journal.Journal(ctx) {
					t.Logf("journal: %+v", entry)
				}
			}
		})
	}
}

// bootStepFixture stores v1alpha1 boot step fixtures as the API server stores them and returns the
// Workflow as the engine reads it. A Workflow without a Template is given one, so that it is one created
// through v1alpha1, which runs in v1alpha1 compatibility mode.
func bootStepFixture(t *testing.T, workflow *v1alpha1.Workflow, hardware *v1alpha1.Hardware, job *bmc.Job) (*v1alpha2.Workflow, client.Client) {
	t.Helper()
	workflow = workflow.DeepCopy()
	if workflow.Spec.TemplateRef == "" {
		workflow.Spec.TemplateRef = "test-template"
	}
	wf := v2Workflow(t, workflow)
	builder := fake.NewClientBuilder().WithScheme(runtimescheme).WithStatusSubresource(&v1alpha2.Hardware{}, &v1alpha2.Workflow{}, &bmcv1alpha2.Job{})
	if hardware != nil && hardware.Name != "" {
		builder = builder.WithObjects(v2(t, hardware))
	}
	if workflow.Name != "" {
		builder = builder.WithObjects(wf.DeepCopy())
	}
	if job != nil {
		builder = builder.WithObjects(v2(t, job))
	}
	return wf, builder.Build()
}

func TestTemplateStringYamlFuncs(t *testing.T) {
	tests := map[string]struct {
		tmplStr string
		data    templateData
		want    string
		wantErr bool
	}{
		"toYaml in templateString": {
			tmplStr: `{{ .Hardware.Interfaces | toYaml }}`,
			data: func() templateData {
				d := templateData{}
				d.Hardware.HardwareSpec = v1alpha1.HardwareSpec{
					Interfaces: []v1alpha1.Interface{
						{DHCP: &v1alpha1.DHCP{MAC: "52:54:00:12:34:01"}},
					},
				}
				return d
			}(),
		},
		"fromYaml in templateString": {
			tmplStr: `{{ $m := fromYaml "hostname: worker-1" }}{{ $m.hostname }}`,
			data:    templateData{},
			want:    "worker-1",
		},
		"fromYaml empty string errors": {
			tmplStr: `{{ fromYaml "" }}`,
			data:    templateData{},
			wantErr: true,
		},
		"env func is disabled": {
			tmplStr: `{{ env "PATH" }}`,
			data:    templateData{},
			wantErr: true,
		},
		"expandenv func is disabled": {
			tmplStr: `{{ expandenv "$PATH" }}`,
			data:    templateData{},
			wantErr: true,
		},
		"missing map key errors": {
			tmplStr: `{{ .Hardware.References.missing }}`,
			data:    templateData{},
			wantErr: true,
		},
		"oversized output errors": {
			tmplStr: `{{ repeat 300000 "a" }}`,
			data:    templateData{},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := templateString(tt.tmplStr, tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("templateString() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("templateString() = %q, want %q", got, tt.want)
			}
			// For toYaml, just verify non-empty output containing MAC
			if tt.want == "" && len(got) == 0 {
				t.Error("templateString() returned empty output, expected YAML content")
			}
		})
	}
}
