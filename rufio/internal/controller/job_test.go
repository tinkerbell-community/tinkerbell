package controller_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	bmclib "github.com/bmc-toolbox/bmclib/v2"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/rufio/internal/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newJob(ops ...bmcv2.Operations) *bmcv2.Job {
	return &bmcv2.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: bmcv2.GroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-namespace",
			Name:      "job",
		},
		Spec: bmcv2.JobSpec{Operations: ops},
	}
}

// newConnection returns a v1alpha2 BMC connection using the createSecret credentials.
func newConnection(host string) *bmcv2.Connection {
	return &bmcv2.Connection{
		Host:           host,
		CredentialsRef: bmcv2.SimpleReference{Name: "test-bm-auth", Namespace: "test-namespace"},
	}
}

func powerOp(a bmcv2.PowerAction) bmcv2.Operations {
	return bmcv2.Operations{PowerAction: &a}
}

func bootPXEOp() bmcv2.Operations {
	return bmcv2.Operations{BootDevice: &bmcv2.BootDeviceConfig{Name: bmcv2.BootDevicePXE, Persistent: true, EFIBoot: true}}
}

// recordHosts wraps a ClientFunc to record the BMC hosts it connects to.
func recordHosts(f controller.ClientFunc, hosts *[]string) controller.ClientFunc {
	return func(ctx context.Context, log logr.Logger, host, username, password string, opts *controller.BMCOptions) (*bmclib.Client, error) {
		*hosts = append(*hosts, host)
		return f(ctx, log, host, username, password, opts)
	}
}

func jobRequest(job *bmcv2.Job) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: job.Namespace, Name: job.Name}}
}

func getJob(t *testing.T, c client.Client, job *bmcv2.Job) *bmcv2.Job {
	t.Helper()
	got := &bmcv2.Job{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(job), got); err != nil {
		t.Fatalf("get Job: %v", err)
	}
	return got
}

func condition(conds []bmcv2.Condition, ct bmcv2.ConditionType) *bmcv2.Condition {
	for i := range conds {
		if conds[i].Type == ct {
			return &conds[i]
		}
	}
	return nil
}

// conditionSummary lists conditions as "Type=Status", in order.
func conditionSummary(conds []bmcv2.Condition) []string {
	var out []string
	for _, c := range conds {
		out = append(out, fmt.Sprintf("%s=%s", c.Type, c.Status))
	}
	return out
}

func TestJobConnection(t *testing.T) {
	machine := createMachine() // test-namespace/test-bm at 0.0.0.0
	hwWithConnection := &v1alpha2.Hardware{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-namespace", Name: "hw-connection"},
		Spec:       v1alpha2.HardwareSpec{BMC: &v1alpha2.BMC{Connection: newConnection("10.0.0.2")}},
	}
	hwUntranslated := createHardwareForMachine(machine.Name) // bmcRef to test-bm, no connection yet
	hwUntranslated.Name = "hw-untranslated"
	hwWithoutBMC := &v1alpha2.Hardware{ObjectMeta: metav1.ObjectMeta{Namespace: "test-namespace", Name: "hw-without-bmc"}}
	machineAnnotation := map[string]string{conversion.AnnotationMachine: "test-namespace/test-bm"}

	tests := map[string]struct {
		connection  *bmcv2.Connection
		hardware    string
		annotations map[string]string
		wantHost    string
		wantFailed  bool
	}{
		"its own connection first": {
			connection:  newConnection("10.0.0.1"),
			annotations: machineAnnotation,
			wantHost:    "10.0.0.1",
		},
		"then the Hardware it refers to": {
			hardware:    "hw-connection",
			annotations: machineAnnotation,
			wantHost:    "10.0.0.2",
		},
		"a Hardware whose Machine is not translated yet": {
			hardware: "hw-untranslated",
			wantHost: "0.0.0.0",
		},
		"then the Machine of a Job created through v1alpha1": {
			annotations: machineAnnotation,
			wantHost:    "0.0.0.0",
		},
		"Hardware without a BMC is retried": {
			hardware: "hw-without-bmc",
		},
		"a missing Hardware is retried": {
			hardware: "no-such-hardware",
		},
		"a missing Machine is retried": {
			annotations: map[string]string{conversion.AnnotationMachine: "test-namespace/no-such-machine"},
		},
		"no BMC at all fails": {
			wantFailed: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			job := newJob(bootPXEOp())
			job.Annotations = tt.annotations
			job.Spec.Connection = tt.connection
			if tt.hardware != "" {
				job.Spec.HardwareRef = &bmcv2.SimpleReference{Namespace: "test-namespace", Name: tt.hardware}
			}
			cl := newClientBuilder().WithObjects(job, machine, createSecret(), hwWithConnection, hwUntranslated, hwWithoutBMC).Build()
			provider := &testProvider{BootdeviceOK: true}
			var hosts []string
			r := controller.NewJobReconciler(cl, cl, recordHosts(newTestClient(provider), &hosts))

			_, err := r.Reconcile(context.Background(), jobRequest(job))
			got := getJob(t, cl, job)
			switch {
			case tt.wantHost != "":
				if err != nil {
					t.Fatalf("Reconcile() error = %v", err)
				}
				if diff := cmp.Diff([]string{tt.wantHost}, hosts); diff != "" {
					t.Errorf("BMC hosts (-want +got):\n%s", diff)
				}
				if !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeCompleted, bmcv2.ConditionStatusTrue) {
					t.Errorf("conditions = %v, want Completed", conditionSummary(got.Status.Conditions))
				}
			case tt.wantFailed:
				if !errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("Reconcile() error = %v, want a terminal error", err)
				}
				if !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeFailed, bmcv2.ConditionStatusTrue) {
					t.Errorf("conditions = %v, want Failed", conditionSummary(got.Status.Conditions))
				}
				if len(hosts) != 0 {
					t.Errorf("connected to %v, want no BMC connection", hosts)
				}
			default:
				if err == nil || errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("Reconcile() error = %v, want an error that is retried", err)
				}
				if len(got.Status.Conditions) != 0 || len(hosts) != 0 {
					t.Errorf("conditions = %v, BMC hosts = %v, want neither", conditionSummary(got.Status.Conditions), hosts)
				}
			}
		})
	}
}

func TestJobRunsOperationsInOrder(t *testing.T) {
	url := "http://example.com/boot.efi"
	tlsNone := "None"
	job := newJob(
		bootPXEOp(),
		powerOp(bmcv2.PowerActionOn),
		bmcv2.Operations{VirtualMediaAction: &bmcv2.VirtualMediaAction{Kind: bmcv2.VirtualMediaCD, MediaURL: "http://example.com/image.iso"}},
		bmcv2.Operations{NetworkBootConfig: &bmcv2.NetworkBootConfig{HTTPBootURL: &url, HTTPBootTLSMode: &tlsNone}},
	)
	job.Spec.Connection = newConnection("10.0.0.1")
	cl := newClientBuilder().WithObjects(job, createSecret()).Build()
	provider := &testProvider{Powerstate: "on", PowerSetOK: true, BootdeviceOK: true, VirtualMediaOK: true, HTTPBootTLSModeOK: true, HTTPBootURIOK: true}
	r := controller.NewJobReconciler(cl, cl, newTestClient(provider))

	result, err := r.Reconcile(context.Background(), jobRequest(job))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.IsZero() {
		t.Errorf("Reconcile() result = %+v, want none", result)
	}

	calls, opens := provider.recorded()
	want := []string{
		"BootDeviceSet(pxe, persistent=true, efi=true)",
		"PowerSet(on)",
		"PowerStateGet",
		"SetVirtualMedia(CD, http://example.com/image.iso)",
		"SetHTTPBootTLSMode(None)",
		"SetHTTPBootURI(http://example.com/boot.efi)",
	}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Errorf("BMC calls (-want +got):\n%s", diff)
	}
	if opens != 1 {
		t.Errorf("opened %d BMC sessions, want 1", opens)
	}

	got := getJob(t, cl, job)
	if got.Status.CompletedOperations != 4 {
		t.Errorf("completedOperations = %d, want 4", got.Status.CompletedOperations)
	}
	if diff := cmp.Diff([]string{"Running=True", "Completed=True"}, conditionSummary(got.Status.Conditions)); diff != "" {
		t.Errorf("conditions (-want +got):\n%s", diff)
	}
	if got.Status.StartTime == nil || got.Status.CompletionTime == nil {
		t.Errorf("startTime = %v, completionTime = %v, want both set", got.Status.StartTime, got.Status.CompletionTime)
	}

	// v1alpha2 Jobs run their operations themselves.
	tasks := &bmc.TaskList{}
	if err := cl.List(context.Background(), tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks.Items) != 0 {
		t.Errorf("created %d bmc Tasks, want none", len(tasks.Items))
	}

	// A finished Job is left alone.
	if _, err := r.Reconcile(context.Background(), jobRequest(job)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if again, _ := provider.recorded(); len(again) != len(calls) {
		t.Errorf("a completed Job made BMC calls %v", again[len(calls):])
	}
}

func TestJobWaitsForPowerState(t *testing.T) {
	job := newJob(powerOp(bmcv2.PowerActionHardOff), bootPXEOp())
	job.Spec.Connection = newConnection("10.0.0.1")
	cl := newClientBuilder().WithObjects(job, createSecret()).Build()
	provider := &testProvider{Powerstate: "on", PowerSetOK: true, BootdeviceOK: true}
	r := controller.NewJobReconciler(cl, cl, newTestClient(provider))

	// Powered off, but the BMC still reports on.
	result, err := r.Reconcile(context.Background(), jobRequest(job))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != 3*time.Second {
		t.Errorf("Reconcile() result = %+v, want a requeue after 3s", result)
	}
	got := getJob(t, cl, job)
	running := condition(got.Status.Conditions, bmcv2.ConditionTypeRunning)
	if got.Status.CompletedOperations != 0 || running == nil || running.Status != bmcv2.ConditionStatusTrue || running.LastUpdateTime.IsZero() {
		t.Fatalf("status = %+v, want Running with the time the power action was performed and no completed operation", got.Status)
	}

	// Still on: the power action is not performed again.
	if _, err := r.Reconcile(context.Background(), jobRequest(job)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	// Off: the next operation runs.
	provider.setPowerState("off")
	result, err = r.Reconcile(context.Background(), jobRequest(job))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.IsZero() {
		t.Errorf("Reconcile() result = %+v, want none", result)
	}

	calls, _ := provider.recorded()
	want := []string{"PowerSet(off)", "PowerStateGet", "PowerStateGet", "PowerStateGet", "BootDeviceSet(pxe, persistent=true, efi=true)"}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Errorf("BMC calls (-want +got):\n%s", diff)
	}
	got = getJob(t, cl, job)
	if got.Status.CompletedOperations != 2 || !bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeCompleted, bmcv2.ConditionStatusTrue) {
		t.Errorf("status = %+v, want 2 completed operations and Completed", got.Status)
	}
}

func TestJobOperationTimeout(t *testing.T) {
	tests := map[string]struct {
		timeoutSeconds int64
		performedAgo   time.Duration
		wantFailed     bool
	}{
		"default timeout exceeded":    {performedAgo: 11 * time.Minute, wantFailed: true},
		"default timeout not reached": {performedAgo: 9 * time.Minute},
		"own timeout exceeded":        {timeoutSeconds: 30, performedAgo: time.Minute, wantFailed: true},
		"own timeout not reached":     {timeoutSeconds: 300, performedAgo: time.Minute},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			op := powerOp(bmcv2.PowerActionOn)
			op.TimeoutSeconds = tt.timeoutSeconds
			job := newJob(op)
			job.Spec.Connection = newConnection("10.0.0.1")
			started := metav1.NewTime(time.Now().Add(-tt.performedAgo))
			job.Status = bmcv2.JobStatus{
				StartTime:  &started,
				Conditions: []bmcv2.Condition{{Type: bmcv2.ConditionTypeRunning, Status: bmcv2.ConditionStatusTrue, LastUpdateTime: started}},
			}
			cl := newClientBuilder().WithObjects(job, createSecret()).Build()
			provider := &testProvider{Powerstate: "off", PowerSetOK: true}
			r := controller.NewJobReconciler(cl, cl, newTestClient(provider))

			result, err := r.Reconcile(context.Background(), jobRequest(job))
			got := getJob(t, cl, job)
			calls, _ := provider.recorded()
			if tt.wantFailed {
				if !errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("Reconcile() error = %v, want a terminal error", err)
				}
				failed := condition(got.Status.Conditions, bmcv2.ConditionTypeFailed)
				if failed == nil || failed.Status != bmcv2.ConditionStatusTrue || !strings.Contains(failed.Message, "timed out") {
					t.Errorf("Failed condition = %+v, want a timeout", failed)
				}
				if len(calls) != 0 {
					t.Errorf("BMC calls = %v, want none", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if result.RequeueAfter != 3*time.Second {
				t.Errorf("Reconcile() result = %+v, want a requeue after 3s", result)
			}
			// Checked, not performed again.
			if diff := cmp.Diff([]string{"PowerStateGet"}, calls); diff != "" {
				t.Errorf("BMC calls (-want +got):\n%s", diff)
			}
		})
	}
}

func TestJobFailures(t *testing.T) {
	performed := metav1.NewTime(time.Now().Add(-time.Minute))
	performedStatus := bmcv2.JobStatus{
		StartTime:  &performed,
		Conditions: []bmcv2.Condition{{Type: bmcv2.ConditionTypeRunning, Status: bmcv2.ConditionStatusTrue, LastUpdateTime: performed}},
	}

	tests := map[string]struct {
		op          bmcv2.Operations
		status      bmcv2.JobStatus
		provider    *testProvider
		noSecret    bool
		wantFailed  string
		wantRequeue bool
		wantRetry   bool
	}{
		"the operation fails": {
			op:         bootPXEOp(),
			provider:   &testProvider{ErrBootDeviceSet: errors.New("boom")},
			wantFailed: "operation 1 of 1 (bootDevice pxe) failed",
		},
		"the BMC cannot be reached to perform it": {
			op:         bootPXEOp(),
			provider:   &testProvider{ErrOpen: errors.New("unreachable")},
			wantFailed: "Failed to connect to BMC",
		},
		"the BMC cannot be reached to check a performed operation": {
			op:          powerOp(bmcv2.PowerActionOn),
			status:      performedStatus,
			provider:    &testProvider{ErrOpen: errors.New("unreachable")},
			wantRequeue: true,
		},
		"the power state cannot be read": {
			op:          powerOp(bmcv2.PowerActionOn),
			status:      performedStatus,
			provider:    &testProvider{ErrPowerStateGet: errors.New("busy")},
			wantRequeue: true,
		},
		"the credentials Secret is missing": {
			op:        bootPXEOp(),
			provider:  &testProvider{BootdeviceOK: true},
			noSecret:  true,
			wantRetry: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			job := newJob(tt.op)
			job.Spec.Connection = newConnection("10.0.0.1")
			job.Status = tt.status
			objs := []client.Object{job}
			if !tt.noSecret {
				objs = append(objs, createSecret())
			}
			cl := newClientBuilder().WithObjects(objs...).Build()
			r := controller.NewJobReconciler(cl, cl, newTestClient(tt.provider))

			result, err := r.Reconcile(context.Background(), jobRequest(job))
			got := getJob(t, cl, job)
			failed := condition(got.Status.Conditions, bmcv2.ConditionTypeFailed)
			switch {
			case tt.wantFailed != "":
				if !errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("Reconcile() error = %v, want a terminal error", err)
				}
				if failed == nil || failed.Status != bmcv2.ConditionStatusTrue || !strings.Contains(failed.Message, tt.wantFailed) {
					t.Errorf("Failed condition = %+v, want one saying %q", failed, tt.wantFailed)
				}
				// Failed Jobs are left alone.
				before, _ := tt.provider.recorded()
				if _, err := r.Reconcile(context.Background(), jobRequest(job)); err != nil {
					t.Errorf("Reconcile() of a failed Job error = %v", err)
				}
				if after, _ := tt.provider.recorded(); len(after) != len(before) {
					t.Errorf("a failed Job made BMC calls %v", after[len(before):])
				}
			case tt.wantRequeue:
				if err != nil || result.RequeueAfter != 3*time.Second {
					t.Errorf("Reconcile() = %+v, %v, want a requeue after 3s", result, err)
				}
				if failed != nil {
					t.Errorf("Failed condition = %+v, want none", failed)
				}
			case tt.wantRetry:
				if err == nil || errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("Reconcile() error = %v, want an error that is retried", err)
				}
				if calls, opens := tt.provider.recorded(); len(calls) != 0 || opens != 0 {
					t.Errorf("BMC calls = %v in %d sessions, want none", calls, opens)
				}
			}
		})
	}
}

// v1alpha1Job is a Job as a v1alpha1 client creates it, for the Machine of createMachine.
func v1alpha1Job(actions ...bmc.Action) *bmc.Job {
	return &bmc.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: bmc.GroupVersion.String(), Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-namespace", Name: "job", UID: "job-uid"},
		Spec: bmc.JobSpec{
			MachineRef: bmc.MachineRef{Name: "test-bm", Namespace: "test-namespace"},
			Tasks:      actions,
		},
	}
}

// toV1Alpha2 converts a v1alpha1 Job as the conversion webhook does.
func toV1Alpha2(t *testing.T, job *bmc.Job) *bmcv2.Job {
	t.Helper()
	out := &bmcv2.Job{}
	if err := conversion.JobToV1Alpha2(job, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// toV1Alpha1 converts a v1alpha2 Job as the conversion webhook does.
func toV1Alpha1(t *testing.T, job *bmcv2.Job) *bmc.Job {
	t.Helper()
	out := &bmc.Job{}
	if err := conversion.JobToV1Alpha1(job, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// v1alpha1Task is a Task the v1alpha1 Job controller created for job.
func v1alpha1Task(job *bmc.Job, i int, conditions ...bmc.TaskCondition) *bmc.Task {
	isController := true
	return &bmc.Task{
		TypeMeta: metav1.TypeMeta{APIVersion: bmc.GroupVersion.String(), Kind: "Task"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      bmc.FormatTaskName(*job, i),
			Namespace: job.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: bmc.GroupVersion.String(), Kind: "Job", Name: job.Name, UID: job.UID, Controller: &isController,
			}},
		},
		Spec:   bmc.TaskSpec{Task: job.Spec.Tasks[i], Connection: createMachine().Spec.Connection},
		Status: bmc.TaskStatus{Conditions: conditions},
	}
}

// TestJobTakesOverV1Alpha1Tasks covers Jobs that the v1alpha1 Job controller
// started before Rufio was upgraded: it ran one bmc Task per action.
func TestJobTakesOverV1Alpha1Tasks(t *testing.T) {
	completed := bmc.TaskCondition{Type: bmc.TaskCompleted, Status: bmc.ConditionTrue}
	failed := bmc.TaskCondition{Type: bmc.TaskFailed, Status: bmc.ConditionTrue}

	tests := map[string]struct {
		task0      []bmc.TaskCondition
		wantCalls  []string
		wantStatus []string
	}{
		"the first Task completed": {
			task0:      []bmc.TaskCondition{completed},
			wantCalls:  []string{"PowerSet(on)", "PowerStateGet"},
			wantStatus: []string{"Running=True", "Completed=True"},
		},
		"the first Task is still running": {
			wantStatus: []string{"Running=True"},
		},
		"the first Task failed": {
			task0:      []bmc.TaskCondition{failed},
			wantStatus: []string{"Running=True", "Failed=True"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			old := v1alpha1Job(getAction("BootPXE"), getAction("PowerOn"))
			now := metav1.Now()
			old.Status = bmc.JobStatus{StartTime: &now, Conditions: []bmc.JobCondition{{Type: bmc.JobRunning, Status: bmc.ConditionTrue}}}
			job := toV1Alpha2(t, old)
			cl := newClientBuilder().WithObjects(job, v1alpha1Task(old, 0, tt.task0...), createMachine(), createSecret()).Build()
			provider := &testProvider{Powerstate: "on", PowerSetOK: true, BootdeviceOK: true}
			r := controller.NewJobReconciler(cl, cl, newTestClient(provider))

			_, _ = r.Reconcile(context.Background(), jobRequest(job))

			calls, _ := provider.recorded()
			if diff := cmp.Diff(tt.wantCalls, calls); diff != "" {
				t.Errorf("BMC calls (-want +got):\n%s", diff)
			}
			got := getJob(t, cl, job)
			if diff := cmp.Diff(tt.wantStatus, conditionSummary(got.Status.Conditions)); diff != "" {
				t.Errorf("conditions (-want +got):\n%s", diff)
			}
		})
	}
}

// runV1Alpha1Path runs a v1alpha1 Job the way Rufio did before Jobs were v1alpha2:
// one bmc Task per action, with the Machine's connection, each run by the Task
// controller until it completes, stopping at the first that fails. It returns
// whether the Job failed.
func runV1Alpha1Path(t *testing.T, job *bmc.Job, provider *testProvider) bool {
	t.Helper()
	machine := createMachine()
	cl := newClientBuilder().WithObjects(machine, createSecret()).Build()
	r := controller.NewTaskReconciler(cl, newTestClient(provider))
	for i, action := range job.Spec.Tasks {
		task := &bmc.Task{
			ObjectMeta: metav1.ObjectMeta{Name: bmc.FormatTaskName(*job, i), Namespace: job.Namespace},
			Spec:       bmc.TaskSpec{Task: action, Connection: machine.Spec.Connection},
		}
		if err := cl.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		for range 10 {
			_, _ = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(task)})
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(task), task); err != nil {
				t.Fatal(err)
			}
			if task.HasCondition(bmc.TaskFailed, bmc.ConditionTrue) {
				return true
			}
			if task.HasCondition(bmc.TaskCompleted, bmc.ConditionTrue) {
				break
			}
		}
		if !task.HasCondition(bmc.TaskCompleted, bmc.ConditionTrue) {
			t.Fatalf("Task %d did not complete", i)
		}
	}
	return false
}

// runV1Alpha2Path runs the v1alpha2 conversion of a v1alpha1 Job with the Job
// controller until it is done, and returns it.
func runV1Alpha2Path(t *testing.T, old *bmc.Job, provider *testProvider) *bmcv2.Job {
	t.Helper()
	job := toV1Alpha2(t, old)
	cl := newClientBuilder().WithObjects(job, createMachine(), createSecret()).Build()
	r := controller.NewJobReconciler(cl, cl, newTestClient(provider))
	for range 10 {
		_, _ = r.Reconcile(context.Background(), jobRequest(job))
		got := getJob(t, cl, job)
		if bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeCompleted, bmcv2.ConditionStatusTrue) ||
			bmcv2.HasConditionStatus(got.Status.Conditions, bmcv2.ConditionTypeFailed, bmcv2.ConditionStatusTrue) {
			return got
		}
	}
	t.Fatal("Job did not finish")
	return nil
}

// TestJobV1Alpha1Parity checks that a Job created through v1alpha1 makes the same
// BMC calls, in the same order, as it did when Rufio ran it as one bmc Task per
// action, in no more BMC sessions, and that v1alpha1 clients (such as the tink
// controller, which waits for Completed or Failed) read the same outcome.
func TestJobV1Alpha1Parity(t *testing.T) {
	tests := map[string]struct {
		actions    []bmc.Action
		provider   func() *testProvider
		wantFailed bool
	}{
		"netboot": {
			actions: []bmc.Action{getAction("HardOff"), getAction("BootPXE"), getAction("PowerOn")},
			provider: func() *testProvider {
				return &testProvider{Powerstate: "on", FollowPowerSet: true, PowerSetOK: true, BootdeviceOK: true}
			},
		},
		"every kind of action": {
			actions: []bmc.Action{
				getAction("SoftOff"),
				{BootDevice: &bmc.BootDeviceConfig{Device: bmc.PXE, Persistent: true, EFIBoot: true}},
				getAction("VirtualMedia"),
				getAction("HTTPAndPXEBootEnabled"),
				getAction("HTTPBootTLSModeAndURL"),
				getAction("PowerOn"),
			},
			provider: func() *testProvider {
				return &testProvider{
					Powerstate: "on", FollowPowerSet: true, PowerSetOK: true, BootdeviceOK: true, VirtualMediaOK: true,
					NetworkBootEnabledOK: true, HTTPBootTLSModeOK: true, HTTPBootURIOK: true,
				}
			},
		},
		"a failing action": {
			actions: []bmc.Action{getAction("BootPXE"), getAction("PowerOn"), getAction("VirtualMedia")},
			provider: func() *testProvider {
				return &testProvider{Powerstate: "off", BootdeviceOK: true, ErrPowerStateSet: errors.New("boom"), VirtualMediaOK: true}
			},
			wantFailed: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			old := v1alpha1Job(tt.actions...)

			oldProvider := tt.provider()
			oldFailed := runV1Alpha1Path(t, old, oldProvider)
			if oldFailed != tt.wantFailed {
				t.Fatalf("v1alpha1 path failed = %v, want %v", oldFailed, tt.wantFailed)
			}
			newProvider := tt.provider()
			job := runV1Alpha2Path(t, old, newProvider)

			oldCalls, oldOpens := oldProvider.recorded()
			newCalls, newOpens := newProvider.recorded()
			if diff := cmp.Diff(oldCalls, newCalls); diff != "" {
				t.Errorf("BMC calls (-v1alpha1 path +v1alpha2 path):\n%s", diff)
			}
			if newOpens > oldOpens {
				t.Errorf("opened %d BMC sessions, the v1alpha1 path opened %d", newOpens, oldOpens)
			}

			// What a v1alpha1 client reads.
			v1 := toV1Alpha1(t, job)
			if diff := cmp.Diff(old.Spec, v1.Spec); diff != "" {
				t.Errorf("v1alpha1 spec changed (-want +got):\n%s", diff)
			}
			if v1.HasCondition(bmc.JobFailed, bmc.ConditionTrue) != tt.wantFailed || v1.HasCondition(bmc.JobCompleted, bmc.ConditionTrue) == tt.wantFailed {
				t.Errorf("v1alpha1 conditions = %+v, want failed = %v", v1.Status.Conditions, tt.wantFailed)
			}
			if !v1.HasCondition(bmc.JobRunning, bmc.ConditionTrue) {
				t.Errorf("v1alpha1 conditions = %+v, want Running kept, as the v1alpha1 Job controller did", v1.Status.Conditions)
			}
			if v1.Status.StartTime == nil || (v1.Status.CompletionTime == nil) != tt.wantFailed {
				t.Errorf("v1alpha1 startTime = %v, completionTime = %v", v1.Status.StartTime, v1.Status.CompletionTime)
			}
		})
	}
}
