package traits_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The four sites below are the typed-nil holes go-kure/launcher#468 enumerated in
// the two parsers that sit beside networkpolicy.go. Each is its own test, not a
// row in a shared table: they are independent statements, and one mutant across a
// symmetric set would read as full discrimination when it is one of four.
//
// A TYPED nil (map[string]any(nil), []any(nil)) is a non-nil interface holding a
// nil value — what an uninitialized Go map or slice in a lowering rule produces —
// so `== nil` is false and a comma-ok assertion on it succeeds. An UNTYPED nil is
// what a decoded `key:` with no value produces. Every case pins both shapes to the
// same answer.

// cnpSelector is a valid endpointSelector, so a test aimed at the rule keys is
// not answered by the endpointSelector check instead.
func cnpSelector() map[string]any {
	return map[string]any{"matchLabels": map[string]any{"app": "api"}}
}

// cnpRule is one non-empty Cilium rule, valid for either direction.
func cnpRule() []any {
	return []any{map[string]any{"toEndpoints": []any{map[string]any{"matchLabels": map[string]any{"app": "db"}}}}}
}

// applyCNP runs the cilium-networkpolicy handler over props.
func applyCNP(props map[string]any) error {
	h := &traits.CiliumNetworkPolicyHandler{}
	return h.Apply(&oam.Trait{Type: "cilium-networkpolicy", Properties: props},
		stack.NewApplication("myapp", "production", nil), &stack.Bundle{})
}

// Site 1 — cilium-networkpolicy's joint egress/ingress requirement. The rendered
// api.Rule carries Egress/Ingress as `omitempty` lists, so a null and an empty
// list both render no rule key at all, and the CiliumNetworkPolicy CRD (anyOf
// ingress/ingressDeny/egress/egressDeny) and Rule.Sanitize both reject a spec
// without one. A key counts toward the requirement only when it holds at least one
// rule; anything else is refused here, by name, rather than at apply time.
func TestCiliumNetworkPolicy_RuleKeyWithoutRulesDoesNotSatisfyJointRequirement(t *testing.T) {
	cases := map[string]map[string]any{
		"typed nil egress":              {"egress": []any(nil)},
		"typed nil ingress":             {"ingress": []any(nil)},
		"untyped nil egress":            {"egress": nil},
		"untyped nil ingress":           {"ingress": nil},
		"both typed nil":                {"egress": []any(nil), "ingress": []any(nil)},
		"empty egress":                  {"egress": []any{}},
		"empty ingress":                 {"ingress": []any{}},
		"both empty":                    {"egress": []any{}, "ingress": []any{}},
		"null egress beside empty":      {"egress": []any(nil), "ingress": []any{}},
		"untyped nil beside empty":      {"egress": nil, "ingress": []any{}},
		"empty typed slice egress":      {"egress": []map[string]any{}},
		"typed nil typed-slice ingress": {"ingress": []map[string]any(nil)},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"name": "cnp", "endpointSelector": cnpSelector()}
			for k, v := range extra {
				props[k] = v
			}
			err := applyCNP(props)
			if err == nil || !strings.Contains(err.Error(), "at least one of 'egress' or 'ingress'") {
				t.Fatalf("Apply error = %v, want the joint-requirement error", err)
			}
		})
	}

	// A null or empty key beside a direction that carries a rule is still just
	// absent: the policy is the other direction's rules and the document is accepted.
	for name, extra := range map[string]map[string]any{
		"null egress beside ingress rule":  {"egress": []any(nil), "ingress": cnpRule()},
		"empty ingress beside egress rule": {"ingress": []any{}, "egress": cnpRule()},
		"egress rule as typed slice":       {"egress": []map[string]any{{"toEndpoints": []any{}}}},
	} {
		t.Run("accepted/"+name, func(t *testing.T) {
			props := map[string]any{"name": "cnp", "endpointSelector": cnpSelector()}
			for k, v := range extra {
				props[k] = v
			}
			if err := applyCNP(props); err != nil {
				t.Fatalf("Apply: %v", err)
			}
		})
	}
}

// renderCNPSpec generates cfg and returns the rendered object's spec as JSON.
func renderCNPSpec(t *testing.T, cfg *traits.CiliumNetworkPolicyConfig) string {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication("myapp", "production", nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	data, err := json.Marshal(*objs[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var obj struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return string(obj.Spec)
}

// Site 2 — endpointSelector. The trait synthesizes no default selector (the
// component-label default is future work, see parseProperties), so an omitted key
// renders a CiliumNetworkPolicy with neither endpointSelector nor nodeSelector,
// which the CRD (oneOf endpointSelector/nodeSelector) and Rule.Sanitize reject.
// Before go-kure/launcher#468 a TYPED nil instead passed toAPIRule's `!= nil`
// guard, was marshalled as `"endpointSelector": null`, and Cilium's
// EndpointSelector.UnmarshalJSON turned that into an allocated empty selector — a
// wildcard over every endpoint. A null is absence, absence cannot render an
// applicable policy, so every null shape and the omitted key are refused by name.
func TestCiliumNetworkPolicy_EndpointSelectorNullOrAbsentIsRejected(t *testing.T) {
	for name, sel := range map[string]any{
		"typed nil map":       map[string]any(nil),
		"typed nil other map": map[string]string(nil),
		"untyped nil":         nil,
	} {
		t.Run(name, func(t *testing.T) {
			err := applyCNP(map[string]any{"name": "cnp", "endpointSelector": sel, "egress": cnpRule()})
			if err == nil || !strings.Contains(err.Error(), "'endpointSelector'") {
				t.Fatalf("Apply error = %v, want an error naming 'endpointSelector'", err)
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		err := applyCNP(map[string]any{"name": "cnp", "egress": cnpRule()})
		if err == nil || !strings.Contains(err.Error(), "'endpointSelector'") {
			t.Fatalf("Apply error = %v, want an error naming 'endpointSelector'", err)
		}
	})

	// An authored empty selector is a value, not a null: it is Cilium's explicit
	// select-all, renders as `endpointSelector: {}` and is accepted.
	t.Run("accepted/empty selector", func(t *testing.T) {
		if err := applyCNP(map[string]any{"name": "cnp", "endpointSelector": map[string]any{}, "egress": cnpRule()}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	})
}

// Below the parser, toAPIRule still reads a typed nil exactly as an untyped one, so
// a CiliumNetworkPolicyConfig built directly in Go cannot turn an uninitialized map
// into the select-all selector described above.
func TestCiliumNetworkPolicyConfig_TypedNilEndpointSelectorRendersAsUntypedNil(t *testing.T) {
	untyped := renderCNPSpec(t, &traits.CiliumNetworkPolicyConfig{Name: "cnp", Egress: cnpRule()})
	got := renderCNPSpec(t, &traits.CiliumNetworkPolicyConfig{
		Name: "cnp", EndpointSelector: map[string]any(nil), Egress: cnpRule(),
	})
	if got != untyped {
		t.Errorf("typed-nil endpointSelector rendered differently from an untyped nil:\n got %s\nwant %s", got, untyped)
	}
}

// Site 3 — networkPolicy.trafficSources is required once networkPolicy is present,
// and an authored `[]` is the deliberate opt-out. A typed nil used to satisfy the
// `[]any` assertion, reach `len() == 0` and take that opt-out path with no
// diagnostic; a null is not `[]`, and for a required field it is an error.
func TestIngressHandler_TrafficSources_NullIsRejected(t *testing.T) {
	for name, sources := range map[string]any{
		"typed nil list": []any(nil),
		"untyped nil":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			trait := ingressTrafficSourcesTrait(map[string]any{
				"networkPolicy": map[string]any{"trafficSources": sources},
			})
			h := &traits.IngressHandler{}
			err := h.Apply(trait, newWebApp("my-app", "default"), newBundle())
			if err == nil {
				t.Fatal("Apply accepted a null trafficSources; want an error, not the [] opt-out")
			}
			if !strings.Contains(err.Error(), "networkPolicy.trafficSources") {
				t.Errorf("error %q does not name networkPolicy.trafficSources", err)
			}
		})
	}
}

// Site 4 — matchLabels is required in a matchLabels-only selector. The presence
// check passes for a key that is present-but-null; a typed nil then satisfied the
// map assertion with a nil map and produced an empty selector, which matches every
// pod. A null required field is an error.
func TestIngressHandler_TrafficSources_NullMatchLabelsIsRejected(t *testing.T) {
	for name, ml := range map[string]any{
		"typed nil map": map[string]any(nil),
		"untyped nil":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			trait := ingressTrafficSourcesTrait(map[string]any{
				"networkPolicy": map[string]any{"trafficSources": []any{
					map[string]any{
						"namespace":   "ingress-nginx",
						"podSelector": map[string]any{"matchLabels": ml},
					},
				}},
			})
			h := &traits.IngressHandler{}
			err := h.Apply(trait, newWebApp("my-app", "default"), newBundle())
			if err == nil {
				t.Fatal("Apply accepted a null matchLabels; want an error, not a select-all selector")
			}
			if !strings.Contains(err.Error(), "podSelector.matchLabels") {
				t.Errorf("error %q does not name podSelector.matchLabels", err)
			}
		})
	}
}
