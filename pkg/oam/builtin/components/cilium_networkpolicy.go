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

// CiliumNetworkPolicyHandler handles OAM cilium-networkpolicy components: the
// kind-named projection of a cilium.io/v2 CiliumNetworkPolicy
// (go-kure/launcher#790).
//
// A CiliumNetworkPolicy has no spec type of its own: it holds one rule under
// `spec`, a list of rules under `specs`, or both, each a Cilium api.Rule. The
// component's properties are those two fields, decoded strictly
// (decodeKindSpec). It emits the CiliumNetworkPolicy, named after the
// component in the build namespace, and nothing else.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
//
// It is an authored object, not the `cilium-networkpolicy` trait, which holds
// one rule's endpointSelector, ingress and egress: here the whole rule is
// authorable, its deny lists, labels and default-deny switches included.
type CiliumNetworkPolicyHandler struct{}

// CanHandle returns true for the cilium-networkpolicy component type.
func (h *CiliumNetworkPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-networkpolicy"
}

// ciliumNetworkPolicyProperties is what a cilium-networkpolicy component
// authors of a ciliumv2.CiliumNetworkPolicy: its `spec` and `specs` fields. The
// object's kind, apiVersion and metadata are launcher's to set and its status
// is Cilium's to write, so the strict decode refuses them as unknown keys.
// TestCoreKindSchemas_CoverSpec holds the two fields to the upstream ones.
type ciliumNetworkPolicyProperties struct {
	Spec  *ciliumapi.Rule `json:"spec,omitempty"`
	Specs ciliumapi.Rules `json:"specs,omitempty"`
}

// PropertySchema declares the two authorable ciliumv2.CiliumNetworkPolicy
// fields by their json names: open objects whose content is checked by the
// strict decode, not by this schema.
func (h *CiliumNetworkPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	const rule = "`endpointSelector` (required; {} selects every endpoint of the namespace) and at least one of `ingress`, `ingressDeny`, `egress` and `egressDeny` with a rule in it, plus the rest of the Cilium rule (`labels`, `enableDefaultDeny`, `description`, `log`). `nodeSelector` is refused: it belongs to a CiliumClusterwideNetworkPolicy."
	return map[string]oam.PropertySchema{
		"spec": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumNetworkPolicy spec: one rule. " + rule + " At least one of spec and specs is required.",
		},
		"specs": {
			Type:        oam.PropertyTypeArray,
			Description: "CiliumNetworkPolicy specs: a list of rules, applied beside spec when both are authored. At least one of spec and specs is required, with a rule in it: an empty specs holds none and is not emitted.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule: " + rule,
			},
		},
	}
}

// ciliumNetworkPolicyRequired is the required list of a cilium-networkpolicy
// component: that of a Cilium rule (requiredfields.CiliumRule), under `spec` and
// under every entry of `specs`. These are the fields the API requires inside a rule
// that the Go type writes whether or not they were authored, and the two it
// writes with a value the API refuses; without the list the object would carry
// the type's empty value where the document left the field out, and would not
// show the omission. TestCiliumNetworkPolicy_RequiredMatchCRD holds it to the
// CiliumNetworkPolicy CRD of the linked module.
var ciliumNetworkPolicyRequired = requiredFields(requiredfields.CiliumRule("spec"), requiredfields.CiliumRule("specs[]"))

// ToApplicationConfig decodes an OAM cilium-networkpolicy component into a
// CiliumNetworkPolicyConfig, under the package's null contract and the strict
// decode every spec-projecting kind uses. An unknown key inside a selector is
// refused as well, then a required field that was not authored
// (ciliumNetworkPolicyRequired), and a policy Cilium rejects: see validate.
func (h *CiliumNetworkPolicyHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	authored, props, err := decodeKindSpec[ciliumNetworkPolicyProperties](component.Properties, "cilium.io/v2 CiliumNetworkPolicy (a cilium-networkpolicy component authors its `spec` and `specs` only)")
	if err != nil {
		return nil, err
	}
	// The strict decode does not reach inside a Cilium type that unmarshals
	// itself, the endpoint selector first of all: a misspelt key there is dropped,
	// and a selector that lost its only key matches every endpoint. The properties
	// are read as authored, not with their nulls stripped, so a misspelt key with
	// a null value is refused too.
	if path := builtin.UnknownCiliumKeyPath[ciliumNetworkPolicyProperties](component.Properties); path != "" {
		return nil, errors.Errorf("%s: unknown field (Cilium drops a key it does not know there instead of refusing it, and a selector that loses a key selects more than was written)", path)
	}
	if err := refuseUncarriedSpecValues(props, authored, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, ciliumNetworkPolicyRequired); err != nil {
		return nil, err
	}
	cfg := &CiliumNetworkPolicyConfig{
		Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace,
		Spec: authored.Spec, Specs: authored.Specs,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// CiliumNetworkPolicyConfig implements stack.ApplicationConfig for
// cilium-networkpolicy components. Spec and Specs are the decoded rules
// exactly as authored; a nil Spec and an empty Specs are unauthored.
type CiliumNetworkPolicyConfig struct {
	Name string
	// ObjectName names the CiliumNetworkPolicy (oam.Component.ObjectName);
	// empty when it is the component's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the
	// CiliumNetworkPolicy (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      *ciliumapi.Rule
	Specs     ciliumapi.Rules
}

// validate refuses a policy Cilium rejects: one with no rule at all, and a rule
// with no endpointSelector, with a nodeSelector, or with no ingress,
// ingressDeny, egress or egressDeny entry. Cilium's CRD schema refuses some of
// these at admission (a rule with no selector, or without any of the four
// keys); the rest the API server stores and the agent rejects when it reads the
// object, so that nothing enforces it. These are the checks Cilium's own reader
// makes that need no agent configuration, and a dependency bump should re-read
// them there:
//
//   - github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2, cnp_types.go,
//     (*CiliumNetworkPolicy).Parse: ErrEmptyCNP when both spec and specs are
//     nil, and "rule cannot have NodeSelector" for a namespaced policy;
//   - github.com/cilium/cilium/pkg/policy/api, rule_validation.go,
//     (*Rule).Sanitize: "rule must have at least one of Ingress, IngressDeny,
//     Egress, EgressDeny" and "rule must have one of EndpointSelector or
//     NodeSelector".
//
// Sanitize itself is not called: it reads the agent's configuration and fills
// defaults into the rule. The rest of a rule's value rules are left to Cilium.
func (c *CiliumNetworkPolicyConfig) validate() error {
	if c.Spec == nil && len(c.Specs) == 0 {
		return errors.New("at least one of 'spec' and 'specs' is required, with a rule in it (Cilium rejects a CiliumNetworkPolicy that holds no rule)")
	}
	if c.Spec != nil {
		if err := validateCiliumRule("spec", c.Spec); err != nil {
			return err
		}
	}
	for i, rule := range c.Specs {
		if rule == nil {
			return errors.Errorf("specs[%d]: expected a rule, got null", i)
		}
		if err := validateCiliumRule(fmt.Sprintf("specs[%d]", i), rule); err != nil {
			return err
		}
	}
	return nil
}

// validateCiliumRule is validate for one rule, at path. A selector is authored
// when its LabelSelector is set, which is how Cilium tells one: {} decodes to an
// allocated selector that matches every endpoint, and a null or absent one
// leaves it nil.
func validateCiliumRule(path string, rule *ciliumapi.Rule) error {
	if rule.NodeSelector.LabelSelector != nil {
		return errors.Errorf("%s.nodeSelector: not allowed in a CiliumNetworkPolicy (Cilium rejects the policy; a node selector belongs to a CiliumClusterwideNetworkPolicy)", path)
	}
	if rule.EndpointSelector.LabelSelector == nil {
		return errors.Errorf("%s.endpointSelector: required (no default selector is filled; use {} to select every endpoint of the namespace)", path)
	}
	return requireCiliumRuleEntry(path, rule)
}

// requireCiliumRuleEntry refuses a rule, at path, with no entry in any of its
// four lists. The namespaced and the cluster-wide policy share it.
func requireCiliumRuleEntry(path string, rule *ciliumapi.Rule) error {
	if len(rule.Ingress) == 0 && len(rule.IngressDeny) == 0 && len(rule.Egress) == 0 && len(rule.EgressDeny) == 0 {
		return errors.Errorf("%s: at least one of 'ingress', 'ingressDeny', 'egress' and 'egressDeny' is required, with a rule in it (a null or empty list holds none, and Cilium rejects a rule without one)", path)
	}
	return nil
}

// ApplyPolicy is a no-op: the environment policy holds no rule for a
// CiliumNetworkPolicy. Its capability lists gate trait types, so a policy that
// forbids the `cilium-networkpolicy` trait does not refuse this component; a
// consumer that restricts network policy restricts the component types it
// registers.
func (c *CiliumNetworkPolicyConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the CiliumNetworkPolicy: kure's identity-only constructor and
// a deep copy of each rule. The parse-time refusals are repeated, since the
// config is exported.
func (c *CiliumNetworkPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	cnp := kurecilium.CreateCiliumNetworkPolicy(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	if c.Spec != nil {
		kurecilium.SetCiliumNetworkPolicySpec(cnp, c.Spec.DeepCopy())
	}
	for _, rule := range c.Specs {
		kurecilium.AddCiliumNetworkPolicySpec(cnp, rule.DeepCopy())
	}
	return kindObject(cnp, c.Metadata)
}
