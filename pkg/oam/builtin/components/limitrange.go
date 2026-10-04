package components

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// LimitRangeHandler handles OAM limitrange components: the kind-named
// projection of a v1 LimitRange (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of corev1.LimitRangeSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// LimitRange, named after the component in the build namespace, and nothing
// else. TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type LimitRangeHandler struct{}

// CanHandle returns true for the limitrange component type.
func (h *LimitRangeHandler) CanHandle(componentType string) bool {
	return componentType == "limitrange"
}

// PropertySchema declares every top-level corev1.LimitRangeSpec field by its
// json name. The items are open objects whose content is checked by the strict
// decode, not by this schema.
func (h *LimitRangeHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"limits": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "Required. LimitRange spec.limits: the limits enforced in the namespace, at most one per type; [] enforces none. Decoded strictly into the Kubernetes API type: see LimitRangeItem in the Kubernetes API reference for its fields.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One limit: type (required: Pod, Container, PersistentVolumeClaim or a qualified name) and any of max, min, default, defaultRequest and maxLimitRequestRatio, each a map of resource name to quantity.",
			},
		},
	}
}

// ToApplicationConfig decodes an OAM limitrange component into a
// LimitRangeConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. limits and each limit's type must be
// authored: the Go type cannot omit either, so an unauthored one would be
// written as null or "".
func (h *LimitRangeHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[corev1.LimitRangeSpec](component.Properties, "v1 LimitRangeSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	cfg := &LimitRangeConfig{Name: component.Name, Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LimitRangeConfig implements stack.ApplicationConfig for limitrange
// components. Spec is the decoded LimitRangeSpec exactly as authored.
type LimitRangeConfig struct {
	Name      string
	Namespace string
	Spec      corev1.LimitRangeSpec
}

// validate refuses what the strict decode cannot see and the Go type cannot
// leave out: an unset limits, which it encodes as null where the API type
// declares a required list (an authored empty list is kept: a LimitRange that
// enforces nothing), and an unset type on a limit, which it encodes as "". It
// also refuses two limits of one type, as the API does. The value rules inside
// a limit (which type names exist, min at most max, what a Pod limit may not
// set) are left to the API server.
func (c *LimitRangeConfig) validate() error {
	if c.Spec.Limits == nil {
		return errors.New("limits: required (the limits the LimitRange enforces; [] enforces none)")
	}
	seen := make(map[corev1.LimitType]int, len(c.Spec.Limits))
	for i, limit := range c.Spec.Limits {
		path := fmt.Sprintf("limits[%d].type", i)
		if limit.Type == "" {
			return errors.Errorf("%s: required (Pod, Container, PersistentVolumeClaim or a qualified name)", path)
		}
		if first, dup := seen[limit.Type]; dup {
			return errors.Errorf("%s: %q is also the type of limits[%d]; a LimitRange holds one limit per type", path, limit.Type, first)
		}
		seen[limit.Type] = i
	}
	return nil
}

// ApplyPolicy is a no-op: a LimitRange runs no pod and requests nothing. It
// constrains the pods and claims of its namespace, and the environment policy
// is applied to each of those where it is authored.
func (c *LimitRangeConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the LimitRange: kure's identity-only constructor plus a deep
// copy of the spec. The parse-time refusals are repeated, since the config is
// exported.
func (c *LimitRangeConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	lr := kubernetes.CreateLimitRange(app.Name, app.Namespace)
	c.Spec.DeepCopyInto(&lr.Spec)
	obj := client.Object(lr)
	return []*client.Object{&obj}, nil
}
