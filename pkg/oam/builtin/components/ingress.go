package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// IngressHandler handles OAM ingress components: the kind-named projection of
// a networking.k8s.io/v1 Ingress (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of networkingv1.IngressSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Ingress, named after the component in the build namespace, and nothing else.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
//
// It is an authored object, not the `ingress` trait: a backend is a Service
// reference as written, not a component, so the NetworkPolicy synthesis does
// not read it and allows no traffic for it, and the trait's platform inputs
// (traffic sources, the hostname constraint) do not reach it.
type IngressHandler struct{}

// CanHandle returns true for the ingress component type.
func (h *IngressHandler) CanHandle(componentType string) bool {
	return componentType == "ingress"
}

// PropertySchema declares every top-level networkingv1.IngressSpec field by
// its json name. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *IngressHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"ingressClassName": {
			Type:        oam.PropertyTypeString,
			Description: "Ingress spec.ingressClassName: the IngressClass whose controller serves this Ingress. Unset leaves the choice to the cluster's default class.",
		},
		"defaultBackend": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Ingress spec.defaultBackend: the backend for a request no rule matches, a `service` (name and port) or a `resource` reference. Decoded strictly into the Kubernetes API type: see IngressBackend in the Kubernetes API reference.",
		},
		"tls": {
			Type:        oam.PropertyTypeArray,
			Description: "Ingress spec.tls: the TLS configuration, each entry the hosts a certificate covers and the Secret that holds it.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One TLS entry: `hosts` and `secretName`. Decoded strictly into IngressTLS.",
			},
		},
		"rules": {
			Type:        oam.PropertyTypeArray,
			Description: "Ingress spec.rules: the host rules. A request that matches none goes to defaultBackend.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule: an optional `host` and its `http.paths`, each with `path`, `pathType` and `backend`. Decoded strictly into IngressRule.",
			},
		},
	}
}

// ToApplicationConfig decodes an OAM ingress component into an IngressConfig,
// under the package's null contract and the strict decode every
// spec-projecting kind uses.
func (h *IngressHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[networkingv1.IngressSpec](component.Properties, "networking.k8s.io/v1 IngressSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	return &IngressConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}, nil
}

// IngressConfig implements stack.ApplicationConfig for ingress components.
// Spec is the decoded IngressSpec exactly as authored.
//
// It reports no traffic source and no backend target, so the NetworkPolicy
// synthesis allows nothing for it: those are the `ingress` trait's, which
// receives them from capability rendering.
type IngressConfig struct {
	Name string
	// ObjectName names the Ingress (oam.Component.ObjectName); empty when it is
	// the component's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Ingress
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      networkingv1.IngressSpec
}

// ApplyPolicy is a no-op: the environment policy holds no rule for an Ingress.
// Its capability lists gate trait types, so a policy that forbids the `ingress`
// trait does not refuse this component; a consumer that restricts routing
// restricts the component types it registers.
func (c *IngressConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the Ingress: kure's identity-only constructor plus a deep
// copy of the spec.
func (c *IngressConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	ing := kubernetes.CreateIngress(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&ing.Spec)
	return kindObject(ing, c.Metadata)
}
