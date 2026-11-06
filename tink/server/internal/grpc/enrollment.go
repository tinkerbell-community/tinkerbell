package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/constant"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/pkg/journal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"quamina.net/go/quamina"
)

const (
	workflowPrefix = "enrollment-"
)

// match is the best match so far: a WorkflowRuleSet, or a workflowAutoCreation rule of a Policy.
type match struct {
	wrs        tinkerbell.WorkflowRuleSet
	policy     *v1alpha2.Policy
	rule       v1alpha2.WorkflowRule
	numMatches int
}

// enroll creates a Workflow for an agentID by matching the attr against the workflowAutoCreation rules of
// Policies and against WorkflowRuleSets. A WorkflowRuleSet translated to a Policy is matched only through
// that Policy, so it never creates a Workflow twice. The rule matching the most patterns wins.
// Even on success it returns a NotFound status, because the new Workflow has no Action
// until the Tink Controller reconciles it.
// auto enrollment through WorkflowRuleSets does not support Templates with multiple Agents defined.
func (h *Handler) enroll(ctx context.Context, agentID string, attr *data.AgentAttributes, hardware *v1alpha2.Hardware) error {
	log := h.Logger.WithValues("agentID", agentID)
	// If auto enrollment is not enabled, then we do not create a Workflow for the AgentID.
	if hardware != nil && !hardware.Spec.Auto.EnrollmentEnabled {
		journal.Log(ctx, "auto enrollment is disabled for this hardware", "hardware", hardware.Name)
		return status.Errorf(codes.FailedPrecondition, "auto enrollment is disabled for this hardware")
	}

	var policies []v1alpha2.Policy
	if h.AutoCapabilities.Enrollment.PolicyLister != nil {
		var err error
		if policies, err = h.AutoCapabilities.Enrollment.ListPolicies(ctx, data.WorkflowFilter{}); err != nil {
			journal.Log(ctx, "error getting policies", "error", err)
			return errors.Join(ErrBackendRead, status.Errorf(codes.Internal, "error getting policies: %v", err))
		}
	}
	// Get all WorkflowRuleSets and check if there is a match to the AgentID or the Attributes (if Attributes are provided by request)
	// using github.com/timbray/quamina
	// If there is a match, create a Workflow for the AgentID.
	wrs, err := h.AutoCapabilities.Enrollment.ListWorkflowRuleSets(ctx, data.WorkflowFilter{})
	if err != nil {
		journal.Log(ctx, "error getting workflow rules", "error", err)
		return errors.Join(ErrBackendRead, status.Errorf(codes.Internal, "error getting workflow rules: %v", err))
	}
	name, err := makeValidName(agentID, workflowPrefix)
	if err != nil {
		journal.Log(ctx, "error making agentID a valid Kubernetes name", "error", err)
		return status.Errorf(codes.Internal, "error making agentID a valid Kubernetes name: %v", err)
	}
	log = log.WithValues("workflowName", name)

	event, err := matchEvent(attr)
	if err != nil {
		journal.Log(ctx, "error marshalling attributes to json", "error", err)
		return status.Errorf(codes.Internal, "error marshalling attributes to json: %v", err)
	}

	// Rule sets the Tink controller translated to a Policy, which enrolls in their place.
	translated := map[types.UID]bool{}
	for _, p := range policies {
		if ref := metav1.GetControllerOf(&p); ref != nil && ref.Kind == "WorkflowRuleSet" {
			translated[ref.UID] = true
		}
	}

	final := &match{}
	for _, wr := range wrs {
		if translated[wr.UID] {
			journal.Log(ctx, "skipping WorkflowRuleSet translated to a Policy", "workflowRuleSet", wr.Name, "namespace", wr.Namespace)
			continue
		}
		// The Workflow's owner reference to wr is only valid in wr's namespace.
		if ns := wr.Spec.Workflow.Namespace; ns != "" && ns != wr.Namespace { //nolint:staticcheck // read only to reject a mismatch until the field is removed
			journal.Log(ctx, "skipping WorkflowRuleSet whose spec.workflow.namespace is not its own namespace",
				"workflowRuleSet", wr.Name, "namespace", wr.Namespace, "workflowNamespace", ns)
			log.Info("skipping WorkflowRuleSet whose spec.workflow.namespace is not its own namespace",
				"workflowRuleSet", wr.Name, "namespace", wr.Namespace, "workflowNamespace", ns)
			continue
		}
		// A Workflow can only reference Hardware in its own namespace, and it is created in wr's.
		if hardware != nil && wr.Namespace != hardware.Namespace {
			journal.Log(ctx, "skipping WorkflowRuleSet outside the Hardware's namespace",
				"workflowRuleSet", wr.Name, "namespace", wr.Namespace, "hardwareNamespace", hardware.Namespace)
			log.Info("skipping WorkflowRuleSet outside the Hardware's namespace",
				"workflowRuleSet", wr.Name, "namespace", wr.Namespace, "hardwareNamespace", hardware.Namespace)
			continue
		}
		n, err := countMatches(wr.Spec.Rules, event)
		if err != nil {
			journal.Log(ctx, "error matching pattern", "workflowRuleSet", wr.Name, "namespace", wr.Namespace, "error", err)
			log.Error(err, "error matching pattern", "workflowRuleSet", wr.Name, "namespace", wr.Namespace)
			continue
		}
		if n > final.numMatches {
			*final = match{wrs: wr, numMatches: n}
		}
	}
	for i := range policies {
		p := &policies[i]
		for _, rule := range p.Spec.Rules.WorkflowAutoCreation {
			if !policyRuleApplies(ctx, log, p, rule, hardware) {
				continue
			}
			patterns, err := rulePatterns(rule.Rules)
			if err != nil {
				journal.Log(ctx, "error encoding pattern", "policy", p.Name, "namespace", p.Namespace, "error", err)
				log.Error(err, "error encoding pattern", "policy", p.Name, "namespace", p.Namespace)
				continue
			}
			n, err := countMatches(patterns, event)
			if err != nil {
				journal.Log(ctx, "error matching pattern", "policy", p.Name, "namespace", p.Namespace, "error", err)
				log.Error(err, "error matching pattern", "policy", p.Name, "namespace", p.Namespace)
				continue
			}
			if n > final.numMatches {
				*final = match{policy: p, rule: rule, numMatches: n}
			}
		}
	}
	if final.numMatches == 0 {
		journal.Log(ctx, "no WorkflowRuleSet or Policy matched")
		return status.Errorf(codes.NotFound, "no Workflow Rule Sets or Policies found or matched for Agent %s", agentID)
	}

	var wf *v1alpha2.Workflow
	var from string
	if final.policy != nil {
		wf = policyWorkflow(name, agentID, final.policy, final.rule, hardware)
		from = "Policy " + final.policy.Name
	} else {
		if wf, err = ruleSetWorkflow(name, agentID, final.wrs, hardware); err != nil {
			journal.Log(ctx, "error converting enrollment workflow", "error", err)
			return status.Errorf(codes.Internal, "error converting enrollment workflow: %v", err)
		}
		from = "WorkflowRuleSet " + final.wrs.Name
	}
	addAttributes := final.rule.Config.AddAttributes
	if final.policy == nil {
		addAttributes = final.wrs.Spec.Workflow.AddAttributes
	}
	if addAttributes {
		// Add attributes to the Workflow as an annotation.
		if a, err := json.Marshal(attr); err == nil {
			ann := maps.Clone(wf.Annotations)
			if ann == nil {
				ann = make(map[string]string)
			}
			ann[constant.AttributesAnnotation] = string(a)
			wf.Annotations = ann
		} else {
			journal.Log(ctx, "error marshalling attributes to json", "error", err)
			log.Error(err, "error marshalling attributes to json")
		}
	}

	// TODO: without Hardware, query for a Hardware object with some corresponding value from the attributes.
	if err := h.AutoCapabilities.Enrollment.CreateWorkflow(ctx, wf); err != nil {
		if apierrors.IsAlreadyExists(err) {
			journal.Log(ctx, "workflow already exists", "workflow", name, "namespace", wf.Namespace)
			// The Workflow exists but isn't cached yet. That resolves itself, so it isn't an error.
			return status.Error(codes.FailedPrecondition, "existing workflow found")
		}
		journal.Log(ctx, "error creating enrollment workflow", "error", err)
		return errors.Join(ErrBackendWrite, status.Errorf(codes.Internal, "error creating enrollment workflow: %v", err))
	}
	journal.Log(ctx, "created enrollment Workflow", "namespace", wf.Namespace, "from", from)
	log.Info("created enrollment Workflow", "namespace", wf.Namespace, "from", from)

	return status.Error(codes.NotFound, "enrollment Workflow created, no Action available yet")
}

// ruleSetWorkflow returns the Workflow a WorkflowRuleSet creates: the v1alpha1 Workflow it created before,
// stored exactly as the conversion webhook would store it. It runs the Template in v1alpha1 compatibility
// mode, or is rewritten onto the Template's Tasks by the Tink controller.
func ruleSetWorkflow(name, agentID string, wrs tinkerbell.WorkflowRuleSet, hardware *v1alpha2.Hardware) (*v1alpha2.Workflow, error) {
	awf := &tinkerbell.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: wrs.Namespace,
			Labels: map[string]string{
				"tinkerbell.org/auto-enrollment": "true",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: tinkerbell.GroupVersion.String(),
					Kind:       "WorkflowRuleSet",
					Name:       wrs.Name,
					UID:        wrs.UID,
				},
			},
		},
		Spec: tinkerbell.WorkflowSpec{
			TemplateRef: wrs.Spec.Workflow.Template.Ref,
			Disabled:    wrs.Spec.Workflow.Disabled,
			HardwareMap: map[string]string{wrs.Spec.Workflow.Template.AgentValue: agentID},
		},
	}
	if hardware != nil {
		awf.Spec.HardwareRef = hardware.Name
	}
	maps.Copy(awf.Spec.HardwareMap, wrs.Spec.Workflow.Template.KVs)

	wf := &v1alpha2.Workflow{}
	if err := conversion.WorkflowToV1Alpha2(awf, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// policyWorkflow returns the Workflow a workflowAutoCreation rule of a Policy creates, in the Policy's
// namespace: its tasks run on the enrolling Agent, against the Agent's Hardware if it has one.
func policyWorkflow(name, agentID string, p *v1alpha2.Policy, rule v1alpha2.WorkflowRule, hardware *v1alpha2.Hardware) *v1alpha2.Workflow {
	cfg := rule.Config.DeepCopy()
	wf := &v1alpha2.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: p.Namespace,
			Labels: map[string]string{
				"tinkerbell.org/auto-enrollment": "true",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha2.GroupVersion.String(),
					Kind:       "Policy",
					Name:       p.Name,
					UID:        p.UID,
				},
			},
		},
		Spec: v1alpha2.WorkflowSpec{
			Disabled:       cfg.Disabled,
			Globals:        cfg.Globals,
			References:     cfg.References,
			TimeoutSeconds: cfg.TimeoutSeconds,
		},
	}
	for _, t := range cfg.Tasks {
		wt := v1alpha2.WorkflowTask{AgentID: agentID, Extra: t.Extra, TimeoutSeconds: t.TimeoutSeconds, TaskRef: t.TaskRef}
		if hardware != nil {
			wt.Hardware = &v1alpha2.WorkflowHardware{HardwareRef: &v1alpha2.SimpleReference{Name: hardware.Name, Namespace: hardware.Namespace}}
		}
		wf.Spec.Tasks = append(wf.Spec.Tasks, wt)
	}
	return wf
}

// policyRuleApplies reports whether a workflowAutoCreation rule of p can enroll the Agent.
func policyRuleApplies(ctx context.Context, log logr.Logger, p *v1alpha2.Policy, rule v1alpha2.WorkflowRule, hardware *v1alpha2.Hardware) bool {
	// Workflows are created in the Policy's namespace, where its owner reference is valid.
	if ns := rule.Config.Namespace; ns != "" && ns != p.Namespace { //nolint:staticcheck // read only to reject a mismatch until the field is removed
		journal.Log(ctx, "skipping Policy rule whose config.namespace is not the Policy's namespace", "policy", p.Name, "namespace", p.Namespace, "workflowNamespace", ns)
		log.Info("skipping Policy rule whose config.namespace is not the Policy's namespace", "policy", p.Name, "namespace", p.Namespace, "workflowNamespace", ns)
		return false
	}
	// A Workflow can only reference Hardware in its own namespace.
	if hardware != nil && p.Namespace != hardware.Namespace {
		journal.Log(ctx, "skipping Policy outside the Hardware's namespace", "policy", p.Name, "namespace", p.Namespace, "hardwareNamespace", hardware.Namespace)
		log.Info("skipping Policy outside the Hardware's namespace", "policy", p.Name, "namespace", p.Namespace, "hardwareNamespace", hardware.Namespace)
		return false
	}
	if len(rule.Config.Tasks) == 0 {
		journal.Log(ctx, "skipping Policy rule without tasks", "policy", p.Name, "namespace", p.Namespace)
		return false
	}
	return true
}

// rulePatterns returns the Quamina patterns of a Policy rule.
func rulePatterns(rules []v1alpha2.AgentAttributes) ([]string, error) {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	return out, nil
}

// matchEvent returns the Agent attributes as the event patterns are matched against.
func matchEvent(attr *data.AgentAttributes) ([]byte, error) {
	if attr == nil {
		return nil, nil
	}
	b, err := json.Marshal(attr)
	if err != nil {
		return nil, fmt.Errorf("error marshalling attributes to json: %w", err)
	}
	return b, nil
}

// countMatches returns how many of patterns match event.
func countMatches(patterns []string, event []byte) (int, error) {
	q, _ := quamina.New() // errors are ignored because they can only happen when passing in options.
	for idx, r := range patterns {
		if err := q.AddPattern(fmt.Sprintf("pattern-%v", idx), r); err != nil {
			// TODO: pattern checking should be done before this. Maybe when a CRD is created.
			return 0, fmt.Errorf("error adding Workflow matching pattern: %v err: %w", fmt.Sprintf("pattern-%v", idx), err)
		}
	}
	matches, err := q.MatchesForEvent(event)
	if err != nil {
		return 0, fmt.Errorf("error matching pattern: %w", err)
	}
	return len(matches), nil
}
