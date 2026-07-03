package traits

import (
	"encoding/json"

	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// CiliumNetworkPolicyHandler handles OAM cilium-networkpolicy traits.
// Supports namespaced CiliumNetworkPolicy only; CiliumClusterWideNetworkPolicy is deferred.
type CiliumNetworkPolicyHandler struct{}

// CanHandle returns true for cilium-networkpolicy trait type.
func (h *CiliumNetworkPolicyHandler) CanHandle(traitType string) bool {
	return traitType == "cilium-networkpolicy"
}

// ValidateAndApplyDefaults rejects any rendering key for this no-rendering trait.
func (h *CiliumNetworkPolicyHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.CiliumNetworkPolicyRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "cilium-networkpolicy rendering")
	}
	return rendering, nil
}

// PropertySchema declares the cilium-networkpolicy trait's user-facing properties.
// endpointSelector/egress/ingress carry opaque Cilium api.Rule shapes, so they are
// kept open (AdditionalProperties on the objects / array items).
func (h *CiliumNetworkPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"name":             {Type: oam.PropertyTypeString, Required: true},
		"endpointSelector": {Type: oam.PropertyTypeObject, AdditionalProperties: true},
		"egress":           {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true}},
		"ingress":          {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true}},
	}
}

// Apply creates a CiliumNetworkPolicy resource appended to the bundle.
func (h *CiliumNetworkPolicyHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	cnpApp := stack.NewApplication(
		config.Name,
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, cnpApp)
	return nil
}

// app is reserved for future component-aware policy synthesis (defaulting
// endpointSelector from component labels, scoping rules to service ports).
func (h *CiliumNetworkPolicyHandler) parseProperties(props map[string]any, _ *stack.Application) (*CiliumNetworkPolicyConfig, error) {
	name, ok := props["name"].(string)
	if !ok || name == "" {
		return nil, errors.New("required property 'name' missing or not a string")
	}

	_, hasEgress := props["egress"]
	_, hasIngress := props["ingress"]
	if !hasEgress && !hasIngress {
		return nil, errors.New("at least one of 'egress' or 'ingress' must be specified")
	}

	return &CiliumNetworkPolicyConfig{
		Name:             name,
		EndpointSelector: props["endpointSelector"],
		Egress:           props["egress"],
		Ingress:          props["ingress"],
	}, nil
}

// CiliumNetworkPolicyConfig implements stack.ApplicationConfig for cilium-networkpolicy traits.
type CiliumNetworkPolicyConfig struct {
	Name             string
	EndpointSelector any
	Egress           any
	Ingress          any
}

// Generate creates a cilium.io/v2 CiliumNetworkPolicy via JSON round-trip.
func (c *CiliumNetworkPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	rule, err := c.toAPIRule()
	if err != nil {
		return nil, errors.Errorf("cilium-networkpolicy %q: %w", c.Name, err)
	}

	cnp := kurecilium.CiliumNetworkPolicy(&kurecilium.CiliumNetworkPolicyConfig{
		Name:      c.Name,
		Namespace: app.Namespace,
		Spec:      rule,
	})

	obj := client.Object(cnp)
	return []*client.Object{&obj}, nil
}

func (c *CiliumNetworkPolicyConfig) toAPIRule() (*ciliumapi.Rule, error) {
	raw := map[string]any{}
	if c.EndpointSelector != nil {
		raw["endpointSelector"] = c.EndpointSelector
	}
	if c.Egress != nil {
		raw["egress"] = c.Egress
	}
	if c.Ingress != nil {
		raw["ingress"] = c.Ingress
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.Wrap(err, "marshal spec")
	}

	var rule ciliumapi.Rule
	if err := json.Unmarshal(data, &rule); err != nil {
		return nil, errors.Wrap(err, "unmarshal into api.Rule")
	}

	return &rule, nil
}
