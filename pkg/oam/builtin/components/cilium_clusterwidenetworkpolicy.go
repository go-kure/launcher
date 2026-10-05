package components

import (
	"fmt"

	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/internal/requiredfields"
)

// CiliumClusterwideNetworkPolicyHandler handles OAM
// cilium-clusterwidenetworkpolicy components: the kind-named projection of a
// cilium.io/v2 CiliumClusterwideNetworkPolicy (go-kure/launcher#790).
//
// The object holds what a CiliumNetworkPolicy holds, one rule under `spec`, a
// list of rules under `specs`, or both, each a Cilium api.Rule, and the
// component's properties are those two fields, decoded as the
// cilium-networkpolicy kind decodes them. It differs in two things: it is
// cluster-scoped, so the object carries no namespace and its rules reach every
// namespace; and a rule selects endpoints or nodes, by `endpointSelector` or
// `nodeSelector`, where a namespaced policy takes endpoints only.
//
// It refuses what the API server's schema refuses of these two fields and
// nothing wider (validate), and a field the API requires that the Go type
// writes whether or not it was authored (ciliumClusterwideNetworkPolicyRequired).
type CiliumClusterwideNetworkPolicyHandler struct{}

// CanHandle returns true for the cilium-clusterwidenetworkpolicy component type.
func (h *CiliumClusterwideNetworkPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-clusterwidenetworkpolicy"
}

// PropertySchema declares the two authorable
// ciliumv2.CiliumClusterwideNetworkPolicy fields by their json names: open
// objects whose content is checked by the strict decode, not by this schema.
func (h *CiliumClusterwideNetworkPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	const rule = "exactly one of `endpointSelector` ({} selects every endpoint of the cluster) and `nodeSelector` ({} selects every node), and at least one of `ingress`, `ingressDeny`, `egress` and `egressDeny` with a rule in it, plus the rest of the Cilium rule (`labels`, `enableDefaultDeny`, `description`, `log`)."
	return map[string]oam.PropertySchema{
		"spec": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumClusterwideNetworkPolicy spec: one rule. It takes " + rule + " At least one of spec and specs is required.",
		},
		"specs": {
			Type:        oam.PropertyTypeArray,
			Description: "CiliumClusterwideNetworkPolicy specs: a list of rules, applied beside spec when both are authored. At least one of spec and specs is required, with a rule in it: an empty specs holds none and is not emitted.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule. It takes " + rule,
			},
		},
	}
}

// ciliumClusterwideNetworkPolicyRequired is the required list of a
// cilium-clusterwidenetworkpolicy component: that of a Cilium rule
// (requiredfields.CiliumRule, which the cilium-networkpolicy kind and trait
// read too), under `spec` and under every entry of `specs`.
// TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD holds it to the CRD of
// the linked module and to the Cilium rule type.
var ciliumClusterwideNetworkPolicyRequired = requiredFields(requiredfields.CiliumRule("spec"), requiredfields.CiliumRule("specs[]"))

// ToApplicationConfig decodes an OAM cilium-clusterwidenetworkpolicy component
// into a CiliumClusterwideNetworkPolicyConfig, under the package's null
// contract and the strict decode every spec-projecting kind uses. An unknown
// key inside a selector is refused as well, as the cilium-networkpolicy kind
// refuses it. The object is cluster-scoped: the build namespace is not read.
func (h *CiliumClusterwideNetworkPolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	authored, props, err := decodeKindSpec[ciliumNetworkPolicyProperties](component.Properties, "cilium.io/v2 CiliumClusterwideNetworkPolicy (a cilium-clusterwidenetworkpolicy component authors its `spec` and `specs` only)")
	if err != nil {
		return nil, err
	}
	// The strict decode does not reach inside a Cilium type that unmarshals
	// itself: see CiliumNetworkPolicyHandler.ToApplicationConfig. Here a
	// selector that lost its only key selects every endpoint, or every node,
	// of the cluster.
	if path := builtin.UnknownCiliumKeyPath[ciliumNetworkPolicyProperties](component.Properties); path != "" {
		return nil, errors.Errorf("%s: unknown field (Cilium drops a key it does not know there instead of refusing it, and a selector that loses a key selects more than was written)", path)
	}
	if err := refuseUncarriedSpecValues(props, authored, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, ciliumClusterwideNetworkPolicyRequired); err != nil {
		return nil, err
	}
	cfg := &CiliumClusterwideNetworkPolicyConfig{
		Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(),
		Spec: authored.Spec, Specs: authored.Specs,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// CiliumClusterwideNetworkPolicyConfig implements stack.ApplicationConfig for
// cilium-clusterwidenetworkpolicy components. Spec and Specs are the decoded
// rules exactly as authored; a nil Spec and an empty Specs are unauthored.
type CiliumClusterwideNetworkPolicyConfig struct {
	Name string
	// ObjectName names the CiliumClusterwideNetworkPolicy
	// (oam.Component.ObjectName); empty when it is the component's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the
	// CiliumClusterwideNetworkPolicy (oam.Component.ObjectMetadata).
	Metadata oam.ObjectMetadata
	Spec     *ciliumapi.Rule
	Specs    ciliumapi.Rules
}

// validate refuses what the schema of the CiliumClusterwideNetworkPolicy CRD
// refuses of the object's `spec` and `specs`, each one comparison of authored
// fields:
//
//   - its rule `has(self.spec) || has(self.specs)`. An empty `specs` is not
//     emitted, so it is no `specs`;
//   - a rule's choice of exactly one of `endpointSelector` and `nodeSelector`;
//   - a rule's need of one of `ingress`, `ingressDeny`, `egress` and
//     `egressDeny`. An empty list is not emitted, so it is none.
//
// TestCiliumClusterwideNetworkPolicy_SchemaChoices holds the three to the CRD
// of the linked module. The choices deeper in a rule and the rule's value
// rules are left to the API server, and what Cilium's own reader refuses of an
// admitted object ((*Rule).Sanitize) to Cilium.
func (c *CiliumClusterwideNetworkPolicyConfig) validate() error {
	if c.Spec == nil && len(c.Specs) == 0 {
		return errors.New("at least one of 'spec' and 'specs' is required, with a rule in it (the API refuses a CiliumClusterwideNetworkPolicy that holds neither)")
	}
	if c.Spec != nil {
		if err := validateCiliumClusterwideRule("spec", c.Spec); err != nil {
			return err
		}
	}
	for i, rule := range c.Specs {
		if rule == nil {
			return errors.Errorf("specs[%d]: expected a rule, got null", i)
		}
		if err := validateCiliumClusterwideRule(fmt.Sprintf("specs[%d]", i), rule); err != nil {
			return err
		}
	}
	return nil
}

// validateCiliumClusterwideRule is validate for one rule, at path. A selector
// is authored when its LabelSelector is set, as in validateCiliumRule.
func validateCiliumClusterwideRule(path string, rule *ciliumapi.Rule) error {
	endpoints, nodes := rule.EndpointSelector.LabelSelector != nil, rule.NodeSelector.LabelSelector != nil
	switch {
	case endpoints && nodes:
		return errors.Errorf("%s: endpointSelector and nodeSelector are both set; the API takes exactly one (a rule selects endpoints or nodes)", path)
	case !endpoints && !nodes:
		return errors.Errorf("%s: one of endpointSelector and nodeSelector is required (no default selector is filled; endpointSelector: {} selects every endpoint of the cluster, nodeSelector: {} every node)", path)
	}
	return requireCiliumRuleEntry(path, rule)
}

// ApplyPolicy is a no-op: the environment policy holds no rule for a
// CiliumClusterwideNetworkPolicy, as it holds none for a CiliumNetworkPolicy.
func (c *CiliumClusterwideNetworkPolicyConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the CiliumClusterwideNetworkPolicy: kure's identity-only
// constructor and a deep copy of each rule. The parse-time refusals of
// validate are repeated, since the config is exported.
func (c *CiliumClusterwideNetworkPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	ccnp := kurecilium.CreateCiliumClusterwideNetworkPolicy(kindObjectName(c.ObjectName, app.Name))
	if c.Spec != nil {
		kurecilium.SetCiliumClusterwideNetworkPolicySpec(ccnp, c.Spec.DeepCopy())
	}
	for _, rule := range c.Specs {
		kurecilium.AddCiliumClusterwideNetworkPolicySpec(ccnp, rule.DeepCopy())
	}
	return kindObject(ccnp, c.Metadata)
}
