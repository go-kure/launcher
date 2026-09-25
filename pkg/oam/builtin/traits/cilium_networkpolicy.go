package traits

import (
	"bytes"
	"encoding/json"
	"reflect"

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
		"name":             {Type: oam.PropertyTypeString, Required: true, Description: "Name of the generated CiliumNetworkPolicy resource."},
		"endpointSelector": {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true, Description: "Cilium endpoint selector matching the pods this policy applies to. Required: no default is synthesized; {} selects every endpoint."},
		"egress":           {Type: oam.PropertyTypeArray, Description: "Cilium egress rules controlling outbound traffic.", Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single Cilium egress rule (opaque api.Rule shape)."}},
		"ingress":          {Type: oam.PropertyTypeArray, Description: "Cilium ingress rules controlling inbound traffic.", Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single Cilium ingress rule (opaque api.Rule shape)."}},
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

// app supplies the owning component name (componentName); deeper component-aware
// policy synthesis (defaulting endpointSelector from component labels, scoping rules
// to service ports) remains future work.
func (h *CiliumNetworkPolicyHandler) parseProperties(props map[string]any, app *stack.Application) (*CiliumNetworkPolicyConfig, error) {
	name, ok := props["name"].(string)
	if !ok || name == "" {
		return nil, errors.New("required property 'name' missing or not a string")
	}

	// The joint requirement counts rules, not keys. api.Rule carries Egress and
	// Ingress as `omitempty` lists, so a null and an empty list both render no rule
	// key at all, and a CiliumNetworkPolicy without one is rejected by the CRD
	// (anyOf ingress/ingressDeny/egress/egressDeny) and by Rule.Sanitize. A
	// presence-only check let `egress:` with no value, or `egress: []`, satisfy the
	// requirement and render a policy the cluster refuses (go-kure/launcher#468).
	// A non-list value is left to count: the strict decode in toAPIRule rejects it
	// with the type error, which is the more useful message.
	if !hasCiliumRules(props["egress"]) && !hasCiliumRules(props["ingress"]) {
		return nil, errors.New("at least one of 'egress' or 'ingress' must be specified with at least one rule " +
			"(a null or empty list renders no rule, and Cilium rejects a policy without one)")
	}

	// endpointSelector is required: this trait synthesizes no default and exposes no
	// nodeSelector, so an omitted selector renders a policy with neither, which the
	// CRD (oneOf endpointSelector/nodeSelector) and Rule.Sanitize reject. A null is
	// absence and is refused the same way, whatever its Go shape — a TYPED nil used
	// to be emitted as `endpointSelector: null`, which Cilium decodes to a wildcard
	// over every endpoint. An authored `{}` is a value (Cilium's explicit
	// select-all) and passes.
	if sel, ok := props["endpointSelector"]; !ok || oam.IsNullValue(sel) {
		return nil, errors.New("required property 'endpointSelector' missing or null " +
			"(no default selector is synthesized; use {} to select every endpoint)")
	}

	return &CiliumNetworkPolicyConfig{
		Name:             name,
		componentName:    app.Name,
		EndpointSelector: props["endpointSelector"],
		Egress:           props["egress"],
		Ingress:          props["ingress"],
	}, nil
}

// hasCiliumRules reports whether v, an egress or ingress property value, would
// render at least one rule: a null (typed or untyped) or an empty list renders
// none. A non-list, non-null value reports true so the strict decode in toAPIRule
// reports its type error rather than this check misnaming it as missing.
func hasCiliumRules(v any) bool {
	if oam.IsNullValue(v) {
		return false
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Slice, reflect.Array:
		return rv.Len() > 0
	default:
		return true
	}
}

// CiliumNetworkPolicyConfig implements stack.ApplicationConfig for cilium-networkpolicy traits.
type CiliumNetworkPolicyConfig struct {
	Name             string
	componentName    string
	EndpointSelector any
	Egress           any
	Ingress          any
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *CiliumNetworkPolicyConfig) ComponentName() string { return c.componentName }

// Generate creates a cilium.io/v2 CiliumNetworkPolicy via JSON round-trip.
func (c *CiliumNetworkPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	rule, err := c.toAPIRule()
	if err != nil {
		return nil, errors.Errorf("cilium-networkpolicy %q: %w", c.Name, err)
	}

	cnp := kurecilium.CreateCiliumNetworkPolicy(c.Name, app.Namespace)
	kurecilium.SetCiliumNetworkPolicySpec(cnp, rule)

	obj := client.Object(cnp)
	return []*client.Object{&obj}, nil
}

func (c *CiliumNetworkPolicyConfig) toAPIRule() (*ciliumapi.Rule, error) {
	// A null is absence and the key is omitted. For a document this is
	// unreachable for endpointSelector and for an all-null rule set —
	// parseProperties refuses those — but a CiliumNetworkPolicyConfig built
	// directly in Go reaches here unchecked, and a null must still not turn into a
	// wildcard. The check is oam.IsNullValue, not `!= nil`: a TYPED nil (map[string]any(nil)) is a
	// non-nil interface, so it passed `!= nil`, marshalled as
	// `"endpointSelector": null`, and Cilium's EndpointSelector.UnmarshalJSON turns
	// that into an allocated empty selector — a wildcard over every endpoint, where
	// the untyped nil omitted the key (go-kure/launcher#468).
	raw := map[string]any{}
	if !oam.IsNullValue(c.EndpointSelector) {
		raw["endpointSelector"] = c.EndpointSelector
	}
	if !oam.IsNullValue(c.Egress) {
		raw["egress"] = c.Egress
	}
	if !oam.IsNullValue(c.Ingress) {
		raw["ingress"] = c.Ingress
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.Wrap(err, "marshal spec")
	}

	// Decode strictly: a property the linked Cilium API cannot represent must fail
	// loudly rather than be dropped. Lenient decoding silently widened policies when
	// Cilium removed api.L7Rules fields — 1.20 dropped kafka, l7proto and l7, which
	// would have turned an L7-restricted policy into an L4-only one with no error.
	//
	// Limitation: encoding/json does not propagate DisallowUnknownFields into types
	// with a custom UnmarshalJSON. In this API that is EndpointSelector and ICMPField,
	// so unknown keys nested inside endpointSelector or icmps are still dropped
	// silently. The toPorts.rules.* shapes that motivated this are covered.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rule ciliumapi.Rule
	if err := dec.Decode(&rule); err != nil {
		return nil, errors.Wrap(err, "unmarshal into api.Rule (a rejected field is not supported by the linked Cilium API version)")
	}

	return &rule, nil
}
