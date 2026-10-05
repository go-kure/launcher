package components

import (
	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// PrometheusRuleHandler handles OAM prometheusrule components: the kind-named
// projection of a monitoring.coreos.com/v1 PrometheusRule
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// monitoringv1.PrometheusRuleSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the PrometheusRule, named after the component
// unless `objectName` names it, in the build namespace, and nothing else. An
// expression is the author's text: launcher does not parse it.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type PrometheusRuleHandler struct{}

// CanHandle returns true for the prometheusrule component type.
func (h *PrometheusRuleHandler) CanHandle(componentType string) bool {
	return componentType == "prometheusrule"
}

// PropertySchema declares every top-level monitoringv1.PrometheusRuleSpec
// field by its json name. A group is an open object whose content is checked
// by the strict decode, not by this schema.
func (h *PrometheusRuleHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"groups": {
			Type:        oam.PropertyTypeArray,
			Description: "PrometheusRule spec.groups: the rule groups of the rule file, each named once.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule group: name (required), interval, query_offset, limit, labels, partial_response_strategy and rules. A rule is a recording rule (record) or an alerting rule (alert, for, keep_firing_for, annotations), with labels and the required PromQL expression expr. Decoded strictly into the Prometheus operator's API type: see RuleGroup and Rule in its API reference.",
			},
		},
	}
}

// prometheusRuleKind is the prometheusrule kind: see policyFreeKind. The API
// requires no field of the spec, a `name` of each group and an `expr` of each
// rule; the type would write an unauthored name empty and an unauthored
// expression as 0, which is an expression. The API's value rules, the one that
// a rule is a recording or an alerting rule included, are left to it, and
// whether an expression is valid PromQL is not checked here.
var prometheusRuleKind = &policyFreeKind[monitoringv1.PrometheusRuleSpec]{
	upstream: "monitoring.coreos.com/v1 PrometheusRuleSpec",
	required: map[string]string{
		"groups[].name":         "the name of the rule group",
		"groups[].rules[].expr": "the PromQL expression the rule evaluates; no default expression is filled",
	},
	build: func(name, namespace string, spec *monitoringv1.PrometheusRuleSpec) client.Object {
		rule := prometheus.CreatePrometheusRule(name, namespace)
		spec.DeepCopyInto(&rule.Spec)
		return rule
	},
}

// ToApplicationConfig decodes an OAM prometheusrule component into its config.
// The object takes the namespace of the application it is generated in.
func (h *PrometheusRuleHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return prometheusRuleKind.config(component)
}
