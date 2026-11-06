package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestHandleJob(t *testing.T) {
	tests := map[string]struct {
		workflow     *v1alpha1.Workflow
		wantWorkflow *v1alpha1.WorkflowStatus
		hardware     *v1alpha1.Hardware
		actions      []bmc.Action
		name         jobName
		wantError    bool
		wantResult   reconcile.Result
		job          *bmc.Job
	}{
		"existing job deleted, new job already created and completed": {
			workflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							jobNameNetboot.String(): {
								ExistingJobDeleted: true,
								UID:                types.UID("1234"),
								Complete:           true,
							},
						},
						AllowNetboot: v1alpha1.AllowNetbootStatus{},
					},
				},
			},
			wantWorkflow: &v1alpha1.WorkflowStatus{
				BootOptions: v1alpha1.BootOptionsStatus{
					Jobs: map[string]v1alpha1.JobStatus{
						jobNameNetboot.String(): {
							ExistingJobDeleted: true,
							UID:                types.UID("1234"),
							Complete:           true,
						},
					},
					AllowNetboot: v1alpha1.AllowNetbootStatus{},
				},
			},
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
			name:       jobNameNetboot,
			wantResult: reconcile.Result{Requeue: true},
		},
		"existing job not deleted": {
			workflow: &v1alpha1.Workflow{
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							jobNameNetboot.String(): {},
						},
						AllowNetboot: v1alpha1.AllowNetbootStatus{},
					},
				},
			},
			wantWorkflow: &v1alpha1.WorkflowStatus{
				BootOptions: v1alpha1.BootOptionsStatus{
					Jobs: map[string]v1alpha1.JobStatus{
						jobNameNetboot.String(): {
							ExistingJobDeleted: true,
						},
					},
					AllowNetboot: v1alpha1.AllowNetbootStatus{},
				},
			},
			name:       jobNameNetboot,
			hardware:   new(v1alpha1.Hardware),
			wantResult: reconcile.Result{Requeue: true},
		},

		"create new job": {
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
				},
				Spec: v1alpha1.WorkflowSpec{
					HardwareRef: "test-hardware",
				},
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							jobNameNetboot.String(): {
								ExistingJobDeleted: true,
							},
						},
						AllowNetboot: v1alpha1.AllowNetbootStatus{},
					},
				},
			},
			wantWorkflow: &v1alpha1.WorkflowStatus{
				Conditions: []v1alpha1.WorkflowCondition{
					{
						Type:    v1alpha1.BootJobSetupComplete,
						Status:  metav1.ConditionTrue,
						Reason:  reasonCreated,
						Message: messageJobCreated,
					},
				},
				BootOptions: v1alpha1.BootOptionsStatus{
					Jobs: map[string]v1alpha1.JobStatus{
						jobNameNetboot.String(): {
							ExistingJobDeleted: true,
						},
					},
					AllowNetboot: v1alpha1.AllowNetbootStatus{},
				},
			},
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
			actions: []bmc.Action{
				{PowerAction: toPtr(bmc.PowerStatus)},
			},
			name:       jobNameNetboot,
			wantResult: reconcile.Result{Requeue: true},
		},
		"existing job running, track status": {
			workflow: &v1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
				},
				Status: v1alpha1.WorkflowStatus{
					BootOptions: v1alpha1.BootOptionsStatus{
						Jobs: map[string]v1alpha1.JobStatus{
							jobNameNetboot.String(): {
								ExistingJobDeleted: true,
								UID:                types.UID("1234"),
							},
						},
						AllowNetboot: v1alpha1.AllowNetbootStatus{},
					},
				},
			},
			wantWorkflow: &v1alpha1.WorkflowStatus{
				Conditions: []v1alpha1.WorkflowCondition{
					{
						Type:    v1alpha1.BootJobComplete,
						Status:  metav1.ConditionTrue,
						Reason:  reasonComplete,
						Message: "job completed",
					},
				},
				BootOptions: v1alpha1.BootOptionsStatus{
					Jobs: map[string]v1alpha1.JobStatus{
						jobNameNetboot.String(): {
							ExistingJobDeleted: true,
							UID:                types.UID("1234"),
							Complete:           true,
						},
					},
					AllowNetboot: v1alpha1.AllowNetbootStatus{},
				},
			},
			hardware:   new(v1alpha1.Hardware),
			actions:    []bmc.Action{},
			name:       jobNameNetboot,
			wantResult: reconcile.Result{},
			job: &bmc.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobNameNetboot.String(),
					Namespace: "default",
					UID:       types.UID("1234"),
				},
				Status: bmc.JobStatus{
					Conditions: []bmc.JobCondition{
						{
							Type:   bmc.JobCompleted,
							Status: bmc.ConditionTrue,
						},
					},
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			wf := v2Workflow(t, tc.workflow)
			builder := fake.NewClientBuilder().WithScheme(runtimescheme)
			if tc.hardware.Name != "" {
				builder = builder.WithObjects(v2(t, tc.hardware))
			}
			if tc.job != nil {
				builder = builder.WithObjects(v2(t, tc.job))
			}
			s := &state{
				workflow: wf,
				client:   builder.Build(),
			}
			ctx := context.Background()
			r, err := s.handleJob(ctx, v1alpha2.SimpleReference{Name: tc.workflow.Spec.HardwareRef}, operationsOf(t, tc.actions), tc.name)
			if (err != nil) != tc.wantError {
				t.Errorf("expected error: %v, got: %v", tc.wantError, err)
			}
			if diff := cmp.Diff(tc.wantResult, r); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(*tc.wantWorkflow, v1Workflow(t, s.workflow).Status, cmpopts.IgnoreFields(v1alpha1.WorkflowCondition{}, "Time")); diff != "" {
				t.Errorf("unexpected workflow status (-want +got):\n%s", diff)
			}
		})
	}
}

// operationsOf returns v1alpha1 BMC actions as the v1alpha2 operations they convert to.
func operationsOf(t *testing.T, actions []bmc.Action) []bmcv1alpha2.Operations {
	t.Helper()
	job := &bmcv1alpha2.Job{}
	if err := conversion.JobToV1Alpha2(&bmc.Job{Spec: bmc.JobSpec{Tasks: actions}}, job); err != nil {
		t.Fatal(err)
	}
	return job.Spec.Operations
}

// TestCreateJobBMC verifies a Job runs against the BMC v1alpha1 used for Hardware created through v1alpha1,
// its bmc Machine, and against spec.bmc for other Hardware.
func TestCreateJobBMC(t *testing.T) {
	ops := operationsOf(t, []bmc.Action{
		{PowerAction: toPtr(bmc.PowerHardOff)},
		{OneTimeBootDeviceAction: &bmc.OneTimeBootDeviceAction{Devices: []bmc.BootDevice{bmc.PXE}, EFIBoot: true}},
		{PowerAction: toPtr(bmc.PowerOn)},
	})
	machineHardware := v2(t, &v1alpha1.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "machine-hw", Namespace: "default"},
		Spec:       v1alpha1.HardwareSpec{BMCRef: &v1.TypedLocalObjectReference{Name: "test-bmc", Kind: "machine.bmc.tinkerbell.org"}},
	})
	nativeHardware := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Name: "native-hw", Namespace: "default"},
		Spec:       v1alpha2.HardwareSpec{BMC: &v1alpha2.BMC{Connection: &bmcv1alpha2.Connection{Host: "10.0.0.1"}}},
	}
	noBMC := &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Name: "no-bmc", Namespace: "default"}}

	tests := map[string]struct {
		hardware string
		wantErr  bool
		// want is the Job as a v1alpha1 client reads it.
		wantV1 bmc.JobSpec
		// wantHardwareRef is the v1alpha2 Job's hardwareRef.
		wantHardwareRef *bmcv1alpha2.SimpleReference
	}{
		"v1alpha1 Hardware uses its Machine": {
			hardware: "machine-hw",
			wantV1: bmc.JobSpec{
				MachineRef: bmc.MachineRef{Name: "test-bmc", Namespace: "default"},
				Tasks: []bmc.Action{
					{PowerAction: toPtr(bmc.PowerHardOff)},
					{BootDevice: &bmc.BootDeviceConfig{Device: bmc.PXE, EFIBoot: true}},
					{PowerAction: toPtr(bmc.PowerOn)},
				},
			},
		},
		"v1alpha2 Hardware uses spec.bmc": {
			hardware:        "native-hw",
			wantHardwareRef: &bmcv1alpha2.SimpleReference{Name: "native-hw", Namespace: "default"},
		},
		"Hardware without a BMC": {
			hardware: "no-bmc",
			wantErr:  true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			wf := &v1alpha2.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "default", UID: "wf-uid"}, Status: v1alpha2.WorkflowStatus{BootOptions: v1alpha2.BootOptionsStatus{Jobs: map[string]v1alpha2.JobStatus{}}}}
			s := &state{workflow: wf, client: GetFakeClientBuilder().WithObjects(machineHardware, nativeHardware, noBMC).Build()}
			_, err := s.createJob(context.Background(), v1alpha2.SimpleReference{Name: tt.hardware}, ops, "netboot-wf")
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "does not have a BMC") {
					t.Fatalf("err = %v, want no BMC", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			job := &bmcv1alpha2.Job{}
			if err := s.client.Get(context.Background(), types.NamespacedName{Name: "netboot-wf", Namespace: "default"}, job); err != nil {
				t.Fatal(err)
			}
			if ref := metav1.GetControllerOf(job); ref == nil || ref.UID != wf.UID || ref.Kind != "Workflow" || ref.APIVersion != v1alpha2.GroupVersion.String() {
				t.Errorf("Job controller = %+v, want the Workflow", ref)
			}
			if diff := cmp.Diff(tt.wantHardwareRef, job.Spec.HardwareRef); diff != "" {
				t.Errorf("hardwareRef (-want +got):\n%s", diff)
			}
			if tt.wantV1.MachineRef.Name != "" {
				v1Job := &bmc.Job{}
				if err := conversion.JobToV1Alpha1(job, v1Job); err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(tt.wantV1, v1Job.Spec); diff != "" {
					t.Errorf("Job as v1alpha1 (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func toPtr[T any](v T) *T {
	return &v
}
