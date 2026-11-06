package grpc

import (
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
)

// The server reads and writes v1alpha2. Most tests describe Workflows and Hardware as v1alpha1 clients
// created and read them: the mock backend serves them the way the API server does, converted to v1alpha2
// (toV2Workflow), and records what the server writes as v1alpha1 clients read it (toV1Workflow). The
// expected results are those of the v1alpha1 server, so these tests show the server behaves as it did for
// objects created through v1alpha1.

// testTemplate names the Template of v1alpha1 Workflow fixtures that name none: every Workflow created
// through v1alpha1 runs a Template.
const testTemplate = "test-template"

func toV2Workflow(wf *tinkerbell.Workflow) *v1alpha2.Workflow {
	if wf == nil {
		return nil
	}
	wf = wf.DeepCopy()
	if wf.Spec.TemplateRef == "" {
		wf.Spec.TemplateRef = testTemplate
	}
	out := &v1alpha2.Workflow{}
	if err := conversion.WorkflowToV1Alpha2(wf, out); err != nil {
		panic(err)
	}
	return out
}

func toV1Workflow(wf *v1alpha2.Workflow) *tinkerbell.Workflow {
	if wf == nil {
		return nil
	}
	out := &tinkerbell.Workflow{}
	if err := conversion.WorkflowToV1Alpha1(wf, out); err != nil {
		panic(err)
	}
	if out.Spec.TemplateRef == testTemplate {
		out.Spec.TemplateRef = ""
	}
	dropConversionAnnotations(out.Annotations)
	if len(out.Annotations) == 0 {
		out.Annotations = nil
	}
	return out
}

func toV2Hardware(hw *tinkerbell.Hardware) *v1alpha2.Hardware {
	if hw == nil {
		return nil
	}
	out := &v1alpha2.Hardware{}
	if err := conversion.HardwareToV1Alpha2(hw, out); err != nil {
		panic(err)
	}
	return out
}

func dropConversionAnnotations(ann map[string]string) {
	delete(ann, conversion.AnnotationV1Alpha1)
	delete(ann, conversion.AnnotationV1Alpha2)
}
