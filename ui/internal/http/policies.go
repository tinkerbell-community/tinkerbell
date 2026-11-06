package webhttp

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	tinkv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/ui/templates"
	"sigs.k8s.io/yaml"
)

const (
	nameSingularPolicy = "Policy"
	namePluralPolicy   = "Policies"
	pathPolicies       = "/policies"
)

// HandlePolicyList handles the Policy list page route.
func HandlePolicyList(c *gin.Context, log logr.Logger) { servePolicies(c, log, true) }

// HandlePolicyData handles the Policy data endpoint (HTMX partial).
func HandlePolicyData(c *gin.Context, log logr.Logger) { servePolicies(c, log, false) }

func servePolicies(c *gin.Context, log logr.Logger, fullPage bool) {
	req, ok := newListRequest(c, log, fullPage)
	if !ok {
		return
	}
	var rows []templates.Policy
	list, err := req.client.ListPolicies(req.ctx, req.namespace)
	if err != nil {
		log.V(1).Info("Failed to list "+namePluralPolicy, "error", err)
		if HandleAuthError(c, err, log) {
			return
		}
	} else {
		for i := range list.Items {
			rows = append(rows, policyRow(&list.Items[i]))
		}
	}
	var data templates.PolicyPageData
	data.Policies, data.Pagination = paginate(rows, req.page, req.perPage, pathPolicies, "#policies-content")

	if !fullPage || IsHTMXRequest(c) {
		renderHTML(c, templates.PolicyTableContent(data), log)
		return
	}
	renderHTML(c, templates.PolicyPage(pageConfig(c, req.namespaces), data), log)
}

// HandlePolicyDetail handles the Policy detail page route.
func HandlePolicyDetail(c *gin.Context, log logr.Logger) {
	client, namespaces, ok := detailRequest(c, log)
	if !ok {
		return
	}
	namespace, name := c.Param("namespace"), c.Param(keyName)
	policy, err := client.GetPolicy(c.Request.Context(), namespace, name)
	if err != nil {
		log.V(1).Info("Failed to fetch "+nameSingularPolicy, "namespace", namespace, "name", name, "error", err)
		if !HandleAuthError(c, err, log) {
			renderNotFound(c, log, namespaces, nameSingularPolicy, namePluralPolicy, GetBaseURL(c)+pathPolicies, namespace, name)
		}
		return
	}

	d := policyDetail(policy, GetBaseURL(c))
	specYAML, _ := yaml.Marshal(&policy.Spec)
	yamlBytes, _ := yaml.Marshal(policy)
	d.SpecYAML, d.YAML = string(specYAML), string(yamlBytes)
	renderHTML(c, templates.PolicyDetailPage(pageConfig(c, namespaces), d), log)
}

func policyRow(p *tinkv1alpha2.Policy) templates.Policy {
	return templates.Policy{
		Name:            p.Name,
		Namespace:       p.Namespace,
		WorkflowRules:   len(p.Spec.Rules.WorkflowAutoCreation),
		ReferenceAccess: p.Spec.Rules.ReferenceAccess != nil,
		RuleSet:         ownerName(p, "WorkflowRuleSet"),
		CreatedAt:       p.GetCreationTimestamp().Format(timeFormat),
	}
}

func policyDetail(p *tinkv1alpha2.Policy, baseURL string) templates.PolicyDetail {
	d := templates.PolicyDetail{
		Name:        p.Name,
		Namespace:   p.Namespace,
		CreatedAt:   p.GetCreationTimestamp().Format(timeFormat),
		Labels:      p.Labels,
		Annotations: p.Annotations,
		Details: []templates.InfoRow{
			{Name: "Workflow Auto-Creation Rules", Value: strconv.Itoa(len(p.Spec.Rules.WorkflowAutoCreation))},
			{Name: "Reference Access", Value: yesNo(p.Spec.Rules.ReferenceAccess != nil)},
		},
	}
	if rs := ownerName(p, "WorkflowRuleSet"); rs != "" {
		d.Details = append(d.Details, templates.InfoRow{Name: "Translated From Rule Set", Value: rs, Link: baseURL + "/workflows/rulesets/" + p.Namespace + "/" + rs})
	}
	for _, r := range p.Spec.Rules.WorkflowAutoCreation {
		d.Rules = append(d.Rules, policyWorkflowRule(r, p.Namespace))
	}
	if ra := p.Spec.Rules.ReferenceAccess; ra != nil {
		if b, err := yaml.Marshal(ra); err == nil {
			d.ReferenceAccessYAML = string(b)
		}
	}
	return d
}

// policyWorkflowRule describes a workflowAutoCreation rule of a Policy in namespace.
func policyWorkflowRule(r tinkv1alpha2.WorkflowRule, namespace string) templates.PolicyWorkflowRule {
	var out templates.PolicyWorkflowRule
	for _, pattern := range r.Rules {
		b, err := json.MarshalIndent(pattern, "", "  ")
		if err != nil {
			continue
		}
		out.Patterns = append(out.Patterns, string(b))
	}
	cfg := r.Config
	out.Config = []templates.InfoRow{
		{Name: "Disabled", Value: yesNo(cfg.Disabled != nil && *cfg.Disabled)},
		{Name: "Add Attributes", Value: yesNo(cfg.AddAttributes)},
		{Name: "Timeout", Value: seconds(cfg.TimeoutSeconds)},
		{Name: "References", Value: strings.Join(slices.Sorted(maps.Keys(cfg.References)), ", ")},
	}
	if g := cfg.Globals; g != nil {
		out.Config = append(out.Config, templates.InfoRow{Name: "Template Map", Value: keyValues(g.TemplateMap)})
	}
	for _, t := range cfg.Tasks {
		ns := t.TaskRef.Namespace
		if ns == "" {
			ns = namespace
		}
		out.Tasks = append(out.Tasks, templates.TaskRef{Name: t.TaskRef.Name, Namespace: ns, Timeout: seconds(t.TimeoutSeconds)})
	}
	return out
}

// keyValues returns m as "key=value" pairs in key order.
func keyValues(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		pairs = append(pairs, k+"="+m[k])
	}
	return strings.Join(pairs, ", ")
}
