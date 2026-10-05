package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumEgressGatewayPolicyHandler handles OAM cilium-egressgatewaypolicy
// components: the kind-named projection of a cilium.io/v2
// CiliumEgressGatewayPolicy (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumEgressGatewayPolicySpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the CiliumEgressGatewayPolicy, named
// after the component unless `objectName` names it, and nothing else. The
// object is cluster-scoped: it carries no namespace, whatever namespace the
// application is built for. Every selector in it is the author's.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CiliumEgressGatewayPolicyHandler struct{}

// CanHandle returns true for the cilium-egressgatewaypolicy component type.
func (h *CiliumEgressGatewayPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-egressgatewaypolicy"
}

// PropertySchema declares every top-level
// ciliumv2.CiliumEgressGatewayPolicySpec field by its json name. Structured
// fields are open objects whose content is checked by the strict decode, not
// by this schema.
func (h *CiliumEgressGatewayPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	const gateway = "nodeSelector (required: the label query over the nodes, of which the first by name is the gateway), interface (the interface whose first IPv4 address the traffic leaves with) and egressIP (the address the traffic leaves with). With neither of the last two, the first IPv4 address of the interface with the default route."
	return map[string]oam.PropertySchema{
		"selectors": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. CiliumEgressGatewayPolicy spec.selectors: the source pods whose egress traffic the policy applies to.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule: namespaceSelector (present and empty, every namespace), podSelector (present and empty, every pod) and nodeSelector (pods by their node), each a label query (matchLabels, matchExpressions). Decoded strictly into Cilium's API type: see EgressRule in its API reference.",
			},
		},
		"destinationCIDRs": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. CiliumEgressGatewayPolicy spec.destinationCIDRs: the destinations the policy applies to. A destination address in any one of them is selected.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An IP block in CIDR notation, e.g. 192.0.2.0/24."},
		},
		"excludedCIDRs": {
			Type:        oam.PropertyTypeArray,
			Description: "CiliumEgressGatewayPolicy spec.excludedCIDRs: destinations left out of the redirection and the address translation. A block outside destinationCIDRs has no effect.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An IP block in CIDR notation."},
		},
		"egressGateway": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. CiliumEgressGatewayPolicy spec.egressGateway: the gateway node the selected traffic leaves the cluster through: " + gateway + " Not read where egressGateways has an entry.",
		},
		"egressGateways": {
			Type:        oam.PropertyTypeArray,
			Description: "CiliumEgressGatewayPolicy spec.egressGateways: gateway nodes, up to 64. With one entry or more, egressGateway is not read. Unset, the API fills an empty list.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One gateway: " + gateway + " Decoded strictly into Cilium's API type: see EgressGateway in its API reference.",
			},
		},
	}
}

// ciliumEgressGatewayPolicyKind is the cilium-egressgatewaypolicy kind: see
// policyFreeKind. The CRD's one expression rule, declared on the egressIP of
// each gateway, holds a single field to the form of an IP address and is left
// to the API server with the API's other value rules.
var ciliumEgressGatewayPolicyKind = &policyFreeKind[ciliumv2.CiliumEgressGatewayPolicySpec]{
	upstream: "cilium.io/v2 CiliumEgressGatewayPolicySpec",
	required: requiredFields(
		map[string]string{
			"selectors":                     "the source pods the policy applies to",
			"destinationCIDRs":              "the destination CIDRs the policy applies to",
			"egressGateway":                 "the gateway node; the API requires it also where egressGateways names the gateways",
			"egressGateway.nodeSelector":    "the label query over the nodes the gateway is chosen from",
			"egressGateways[].nodeSelector": "the label query over the nodes the gateway is chosen from",
		},
		labelSelectorRequired("egressGateway.nodeSelector"),
		labelSelectorRequired("egressGateways[].nodeSelector"),
		labelSelectorRequired("selectors[].namespaceSelector"),
		labelSelectorRequired("selectors[].podSelector"),
		labelSelectorRequired("selectors[].nodeSelector"),
	),
	build: func(name, _ string, spec *ciliumv2.CiliumEgressGatewayPolicySpec) client.Object {
		policy := kurecilium.CreateCiliumEgressGatewayPolicy(name)
		spec.DeepCopyInto(&policy.Spec)
		return policy
	},
}

// ToApplicationConfig decodes an OAM cilium-egressgatewaypolicy component
// into its config. The build namespace is not used: the object is
// cluster-scoped.
func (h *CiliumEgressGatewayPolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumEgressGatewayPolicyKind.config(component)
}
