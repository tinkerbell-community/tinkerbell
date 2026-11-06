package webhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	bmcv1alpha1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// legacyBMCJobRow is the web UI's v1alpha1 Job list row as it was before the
// UI read v1alpha2: the reference for Jobs created through v1alpha1.
func legacyBMCJobRow(job bmcv1alpha1.Job) (machineRef, status, completedAt string) {
	if job.Status.CompletionTime != nil {
		completedAt = job.Status.CompletionTime.Format("2006-01-02 15:04:05")
	}
	return job.Spec.MachineRef.Namespace + "/" + job.Spec.MachineRef.Name, legacyBMCJobStatus(job.Status.Conditions), completedAt
}

func legacyBMCJobStatus(conditions []bmcv1alpha1.JobCondition) string {
	status := statusPending
	for _, condition := range conditions {
		if condition.Status != bmcv1alpha1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case bmcv1alpha1.JobCompleted:
			return statusCompleted
		case bmcv1alpha1.JobFailed:
			return statusFailed
		case bmcv1alpha1.JobRunning:
			status = statusRunning
		}
	}
	return status
}

func TestBMCJobRowV1Alpha1Parity(t *testing.T) {
	done := metav1.Now()
	power := bmcv1alpha1.PowerHardOff
	for name, conditions := range map[string][]bmcv1alpha1.JobCondition{
		"pending":   nil,
		"running":   {{Type: bmcv1alpha1.JobRunning, Status: bmcv1alpha1.ConditionTrue}},
		"completed": {{Type: bmcv1alpha1.JobRunning, Status: bmcv1alpha1.ConditionFalse}, {Type: bmcv1alpha1.JobCompleted, Status: bmcv1alpha1.ConditionTrue}},
		"failed":    {{Type: bmcv1alpha1.JobFailed, Status: bmcv1alpha1.ConditionTrue, Message: "unreachable"}},
	} {
		t.Run(name, func(t *testing.T) {
			v1 := newTestBMCJobV1Alpha1("job", "tink", "bmc-1", conditions)
			v1.Spec.Tasks = []bmcv1alpha1.Action{{PowerAction: &power}}
			if name == "completed" {
				v1.Status.CompletionTime = &done
			}
			machine, status, completedAt := legacyBMCJobRow(*v1)
			row := toBMCJobRow(bmcJobV1Alpha2(v1))
			if row.TargetKind != targetMachine || row.Target != machine || row.TargetPath != "/bmc/machines/"+machine {
				t.Errorf("target = %+v, want Machine %s", row.JobTarget, machine)
			}
			if row.Status != status || row.CompletedAt != completedAt {
				t.Errorf("status = %q, completed = %q; want %q, %q", row.Status, row.CompletedAt, status, completedAt)
			}
			if row.Operations != "power off" {
				t.Errorf("Operations = %q, want the v1alpha1 task as an operation", row.Operations)
			}
		})
	}
}

func newHardwareJob() *bmcv1alpha2.Job {
	on := bmcv1alpha2.PowerActionOn
	off := bmcv1alpha2.PowerActionHardOff
	httpBoot := true
	url := "http://boot.example/ipxe.efi"
	return &bmcv1alpha2.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "provision", Namespace: "default", CreationTimestamp: metav1.Now()},
		Spec: bmcv1alpha2.JobSpec{
			HardwareRef: &bmcv1alpha2.SimpleReference{Name: "hw-v2", Namespace: "default"},
			Operations: []bmcv1alpha2.Operations{
				{PowerAction: &off, TimeoutSeconds: 60},
				{BootDevice: &bmcv1alpha2.BootDeviceConfig{Name: bmcv1alpha2.BootDevicePXE, EFIBoot: true}},
				{NetworkBootConfig: &bmcv1alpha2.NetworkBootConfig{HTTPBootEnabled: &httpBoot, HTTPBootURL: &url}},
				{VirtualMediaAction: &bmcv1alpha2.VirtualMediaAction{Kind: bmcv1alpha2.VirtualMediaCD}},
				{PowerAction: &on},
			},
		},
		Status: bmcv1alpha2.JobStatus{
			CompletedOperations: 2,
			Conditions:          []bmcv1alpha2.Condition{{Type: bmcv1alpha2.ConditionTypeRunning, Status: bmcv1alpha2.ConditionStatusTrue}},
		},
	}
}

func TestBMCJobV1Alpha2(t *testing.T) {
	job := newHardwareJob()
	row := toBMCJobRow(job)
	if row.TargetKind != targetHardware || row.TargetPath != "/hardware/default/hw-v2" || row.Status != statusRunning {
		t.Errorf("row = %+v", row)
	}
	if want := "power off, boot device pxe (EFI), network boot HTTP boot on, HTTP boot URL http://boot.example/ipxe.efi, virtual media CD: eject, power on"; row.Operations != want {
		t.Errorf("Operations = %q\nwant %q", row.Operations, want)
	}

	d := bmcJobDetail(job)
	if d.Progress != "2 of 5" || len(d.Operations) != 5 {
		t.Fatalf("progress = %q, operations = %+v", d.Progress, d.Operations)
	}
	if op := d.Operations[0]; op.Kind != "Power" || op.Details != "off" || op.Timeout != "60s" || !op.Done {
		t.Errorf("first operation = %+v", op)
	}
	if d.Operations[2].Done {
		t.Error("the third operation has not completed")
	}

	host := &bmcv1alpha2.Job{Spec: bmcv1alpha2.JobSpec{Connection: &bmcv1alpha2.Connection{Host: "10.0.0.5"}}}
	if target := jobTarget(host); target.TargetKind != targetBMC || target.Target != "10.0.0.5" || target.TargetPath != "" {
		t.Errorf("target of a Job with a connection = %+v", target)
	}
}

func TestHandleBMCJobDetailV1Alpha2(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newHardwareJob())
	c, w := setupTestContext("/bmc/jobs/default/provision", kubeClient)
	c.Params = gin.Params{{Key: "namespace", Value: "default"}, {Key: "name", Value: "provision"}}

	HandleBMCJobDetail(c, testLog)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{`href="/hardware/default/hw-v2"`, "Network Boot", "http://boot.example/ipxe.efi", "2 of 5", "Running"} {
		if !strings.Contains(body, want) {
			t.Errorf("response should contain %q", want)
		}
	}
}

func TestHandleBMCJobList_MachineTarget(t *testing.T) {
	kubeClient := newFakeKubeClient(newTestNamespace("default"), newTestBMCJob("job-1", "default", "bmc-1", nil))
	c, w := setupTestContext("/bmc/jobs", kubeClient)

	HandleBMCJobList(c, testLog)

	body := w.Body.String()
	if !strings.Contains(body, `href="/bmc/machines/default/bmc-1"`) || !strings.Contains(body, "deprecated") {
		t.Error("a Job created through v1alpha1 should link its deprecated Machine")
	}
}
