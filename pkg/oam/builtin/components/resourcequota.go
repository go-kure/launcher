package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ResourceQuotaHandler handles OAM resourcequota components: the kind-named
// projection of a v1 ResourceQuota (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of corev1.ResourceQuotaSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// ResourceQuota, named after the component in the build namespace, and nothing
// else. The API requires no field of the spec, so a component with no
// properties is a quota that limits nothing.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ResourceQuotaHandler struct{}

// CanHandle returns true for the resourcequota component type.
func (h *ResourceQuotaHandler) CanHandle(componentType string) bool {
	return componentType == "resourcequota"
}

// PropertySchema declares every top-level corev1.ResourceQuotaSpec field by
// its json name. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *ResourceQuotaHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"hard": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "ResourceQuota spec.hard: the hard limit per resource name, each a quantity (requests.cpu: \"4\", persistentvolumeclaims: 10).",
		},
		"scopes": {
			Type:        oam.PropertyTypeArray,
			Description: "ResourceQuota spec.scopes: the quota tracks only the objects that match every scope listed. Unset matches all objects.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A quota scope, e.g. Terminating, NotTerminating, BestEffort, NotBestEffort or PriorityClass."},
		},
		"scopeSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "ResourceQuota spec.scopeSelector: scopes selected by expression (matchExpressions of scopeName, operator, values), combined with scopes. Decoded strictly into the Kubernetes API type: see ScopeSelector in the Kubernetes API reference.",
		},
	}
}

// ToApplicationConfig decodes an OAM resourcequota component into a
// ResourceQuotaConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses.
func (h *ResourceQuotaHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[corev1.ResourceQuotaSpec](component.Properties, "v1 ResourceQuotaSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	return &ResourceQuotaConfig{Name: component.Name, Namespace: namespace, Spec: *spec}, nil
}

// ResourceQuotaConfig implements stack.ApplicationConfig for resourcequota
// components. Spec is the decoded ResourceQuotaSpec exactly as authored.
type ResourceQuotaConfig struct {
	Name      string
	Namespace string
	Spec      corev1.ResourceQuotaSpec
}

// ApplyPolicy is a no-op: a ResourceQuota runs no pod and requests nothing. It
// bounds what its namespace may hold in total, and the environment policy is
// applied to each pod and claim where it is authored.
func (c *ResourceQuotaConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the ResourceQuota: kure's identity-only constructor plus a
// deep copy of the spec.
func (c *ResourceQuotaConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	rq := kubernetes.CreateResourceQuota(app.Name, app.Namespace)
	c.Spec.DeepCopyInto(&rq.Spec)
	obj := client.Object(rq)
	return []*client.Object{&obj}, nil
}
