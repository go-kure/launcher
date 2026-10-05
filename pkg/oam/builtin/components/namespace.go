package components

import (
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// NamespaceHandler handles OAM namespace components: the kind-named projection
// of a v1 Namespace (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of corev1.NamespaceSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Namespace, named after the component, and nothing else. A Namespace is
// cluster-scoped: the object carries no namespace, whatever namespace the
// application is built for. TestCoreKindSchemas_CoverSpec keeps the published
// key set equal to the upstream json tags.
type NamespaceHandler struct{}

// CanHandle returns true for the namespace component type.
func (h *NamespaceHandler) CanHandle(componentType string) bool {
	return componentType == "namespace"
}

// PropertySchema declares every top-level corev1.NamespaceSpec field by its
// json name.
func (h *NamespaceHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"finalizers": {
			Type:        oam.PropertyTypeArray,
			Description: "Namespace spec.finalizers: the finalizers that must be removed before the namespace is deleted. The API server adds its own (kubernetes) when the field is unset.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A finalizer name."},
		},
	}
}

// ToApplicationConfig decodes an OAM namespace component into a
// NamespaceConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. The component name is the Namespace's name,
// so it must be one the API accepts for a Namespace.
func (h *NamespaceHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[corev1.NamespaceSpec](component.Properties, "v1 NamespaceSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	cfg := &NamespaceConfig{Name: component.Name, ObjectName: componentObjectName(component), Spec: *spec}
	if err := validateNamespaceObjectName(component.ObjectName(), cfg.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// NamespaceConfig implements stack.ApplicationConfig for namespace components.
// Spec is the decoded NamespaceSpec exactly as authored.
type NamespaceConfig struct {
	Name string
	// ObjectName names the Namespace (oam.Component.ObjectName). Empty for the
	// application's name.
	ObjectName string
	Spec       corev1.NamespaceSpec
}

// validateNamespaceObjectName is validateNamespaceName for the name the
// Namespace takes: the component's, or the one that names it apart from the
// component, the author's `objectName` or the Naming hook's answer
// (objectNameField), which is then the one held to the rule.
func validateNamespaceObjectName(name, componentName string) error {
	if name == componentName {
		return validateNamespaceName(name)
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return errors.Errorf("%s: %q is not a valid Namespace name, which must be a DNS-1123 label of at most %d characters: %s",
			objectNameField, name, validation.DNS1123LabelMaxLength, strings.Join(errs, "; "))
	}
	return nil
}

// validateNamespaceName refuses a name the API refuses for a Namespace: a
// component name is a DNS-1123 subdomain of up to 253 characters, a Namespace
// name a DNS-1123 label of at most 63.
func validateNamespaceName(name string) error {
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return errors.Errorf("namespace %q: the component name is the Namespace's name, which must be a DNS-1123 label of at most %d characters: %s",
			name, validation.DNS1123LabelMaxLength, strings.Join(errs, "; "))
	}
	return nil
}

// ApplyPolicy is a no-op: a Namespace runs no pod and holds no image, storage
// or replica count, so no environment policy dimension applies to it.
func (c *NamespaceConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the Namespace: kure's identity-only constructor plus a deep
// copy of the spec. The name check is repeated, since the config is exported.
func (c *NamespaceConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	name := kindObjectName(c.ObjectName, app.Name)
	if err := validateNamespaceObjectName(name, app.Name); err != nil {
		return nil, err
	}
	ns := kubernetes.CreateNamespace(name)
	c.Spec.DeepCopyInto(&ns.Spec)
	obj := client.Object(ns)
	return []*client.Object{&obj}, nil
}
