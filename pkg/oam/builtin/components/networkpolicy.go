package components

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// NetworkPolicyHandler handles OAM networkpolicy components: the kind-named
// projection of a networking.k8s.io/v1 NetworkPolicy (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// networkingv1.NetworkPolicySpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the NetworkPolicy, named after the component in
// the build namespace, and nothing else.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
//
// It is an authored object, not the `networkpolicy` trait, which always
// selects its component's pods and lists a direction when its key is present.
// Here nothing is scoped to a component and no policyTypes are derived, so the
// spec reads as the API reads it: an unauthored podSelector selects every pod
// of the namespace, and an empty egress list alone isolates no egress.
type NetworkPolicyHandler struct{}

// CanHandle returns true for the networkpolicy component type.
func (h *NetworkPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "networkpolicy"
}

// PropertySchema declares every top-level networkingv1.NetworkPolicySpec field
// by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *NetworkPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"podSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "NetworkPolicy spec.podSelector: the pods the policy applies to (matchLabels, matchExpressions). Unset or {} selects every pod of the namespace: no selector is defaulted to a component's pods, unlike the networkpolicy trait.",
		},
		"ingress": {
			Type:        oam.PropertyTypeArray,
			Description: "NetworkPolicy spec.ingress: the inbound rules. A rule with no `from` allows every source and one with no `ports` every port, so {} allows all inbound traffic.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One ingress rule: `from` (peers) and `ports`. Decoded strictly into NetworkPolicyIngressRule.",
			},
		},
		"egress": {
			Type:        oam.PropertyTypeArray,
			Description: "NetworkPolicy spec.egress: the outbound rules. A rule with no `to` allows every destination and one with no `ports` every port, so {} allows all outbound traffic.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One egress rule: `to` (peers) and `ports`. Decoded strictly into NetworkPolicyEgressRule.",
			},
		},
		"policyTypes": {
			Type:        oam.PropertyTypeArray,
			Description: "NetworkPolicy spec.policyTypes: the directions the policy isolates. Unset, the API server derives it: Ingress always, Egress when an egress rule is authored. So an empty egress list alone isolates no egress, unlike on the networkpolicy trait: denying all egress takes policyTypes [Egress]. An empty list is refused: it is omitted, so the derived default would apply.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A policy type: Ingress or Egress."},
		},
	}
}

// networkPolicyLabelSelectors lists the label selectors of a
// networkingv1.NetworkPolicySpec, as required-list paths: the policy's own and
// the pod and namespace selectors of every peer.
var networkPolicyLabelSelectors = []string{
	"podSelector",
	"ingress[].from[].podSelector",
	"ingress[].from[].namespaceSelector",
	"egress[].to[].podSelector",
	"egress[].to[].namespaceSelector",
}

// networkPolicyDefaultedZeros lists the lists of NetworkPolicySpec that the
// type omits when empty and that the API server then defaults, so an authored
// [] there is refused (refuseUncarriedSpecValues): policyTypes, which the API
// server derives from the rules.
// TestKubernetesDefaulters_ListDefaultsMatchVendoredSource holds the default
// to the API server's defaulting code, at the linked k8s.io/api's release, and
// TestKindComponents_DefaultedEmptyLists holds the list to the lists the field
// comments (SwaggerDoc) say are defaulted.
var networkPolicyDefaultedZeros = defaultedZeroFields{api: "Kubernetes", defaulter: "API server", fields: map[string]string{
	"policyTypes": `["Ingress"], with "Egress" when an egress rule is authored`,
}}

// ToApplicationConfig decodes an OAM networkpolicy component into a
// NetworkPolicyConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. A null rule or peer is refused by path
// there: decoded, it would be an empty one, which allows everything. A match
// expression of a selector is held to labelSelectorRequired and to
// NetworkPolicyConfig.validate.
func (h *NetworkPolicyHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[networkingv1.NetworkPolicySpec](component.Properties, "networking.k8s.io/v1 NetworkPolicySpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, networkPolicyDefaultedZeros); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, labelSelectorRequired(networkPolicyLabelSelectors...)); err != nil {
		return nil, err
	}
	cfg := &NetworkPolicyConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// NetworkPolicyConfig implements stack.ApplicationConfig for networkpolicy
// components. Spec is the decoded NetworkPolicySpec exactly as authored.
type NetworkPolicyConfig struct {
	Name string
	// ObjectName names the NetworkPolicy (oam.Component.ObjectName); empty when
	// it is the component's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the NetworkPolicy
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      networkingv1.NetworkPolicySpec
}

// validate refuses a spec the NetworkPolicy cannot be emitted from: a match
// expression, in any selector networkPolicyLabelSelectors lists, that the API
// server refuses on every object (validateLabelSelector;
// ValidateNetworkPolicySpec and ValidateNetworkPolicyPeer in
// pkg/apis/networking/validation, Kubernetes v1.37.1, validate each as a label
// selector). The API's other value rules are left to the API server.
func (c *NetworkPolicyConfig) validate() error {
	if err := validateLabelSelector("podSelector", &c.Spec.PodSelector); err != nil {
		return err
	}
	for i := range c.Spec.Ingress {
		if err := validateNetworkPolicyPeerSelectors(fmt.Sprintf("ingress[%d].from", i), c.Spec.Ingress[i].From); err != nil {
			return err
		}
	}
	for i := range c.Spec.Egress {
		if err := validateNetworkPolicyPeerSelectors(fmt.Sprintf("egress[%d].to", i), c.Spec.Egress[i].To); err != nil {
			return err
		}
	}
	return nil
}

// validateNetworkPolicyPeerSelectors holds the pod and namespace selectors of
// the peers at the path at to validateLabelSelector.
func validateNetworkPolicyPeerSelectors(at string, peers []networkingv1.NetworkPolicyPeer) error {
	for i := range peers {
		if err := validateLabelSelector(fmt.Sprintf("%s[%d].podSelector", at, i), peers[i].PodSelector); err != nil {
			return err
		}
		if err := validateLabelSelector(fmt.Sprintf("%s[%d].namespaceSelector", at, i), peers[i].NamespaceSelector); err != nil {
			return err
		}
	}
	return nil
}

// ApplyPolicy is a no-op: the environment policy holds no rule for a
// NetworkPolicy. Its capability lists gate trait types, so a policy that
// forbids the `networkpolicy` trait does not refuse this component; a consumer
// that restricts network policy restricts the component types it registers.
func (c *NetworkPolicyConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the NetworkPolicy: kure's identity-only constructor plus a
// deep copy of the spec. The parse-time refusals the typed spec can show are
// repeated, since the config is exported.
func (c *NetworkPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	np := kubernetes.CreateNetworkPolicy(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&np.Spec)
	return kindObject(np, c.Metadata)
}
