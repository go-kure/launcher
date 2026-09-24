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

// Site 1 — cilium-networkpolicy's joint egress/ingress requirement. Both keys are
// optional individually, so a null is absence, and absence cannot satisfy a
// requirement that at least one of them is present.
func TestCiliumNetworkPolicy_NullRuleKeyDoesNotSatisfyJointRequirement(t *testing.T) {
	cases := map[string]map[string]any{
		"typed nil egress":    {"egress": []any(nil)},
		"typed nil ingress":   {"ingress": []any(nil)},
		"untyped nil egress":  {"egress": nil},
		"untyped nil ingress": {"ingress": nil},
		"both typed nil":      {"egress": []any(nil), "ingress": []any(nil)},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"name": "cnp"}
			for k, v := range extra {
				props[k] = v
			}
			h := &traits.CiliumNetworkPolicyHandler{}
			err := h.Apply(&oam.Trait{Type: "cilium-networkpolicy", Properties: props},
				stack.NewApplication("myapp", "production", nil), &stack.Bundle{})
			if err == nil || !strings.Contains(err.Error(), "at least one of 'egress' or 'ingress'") {
				t.Fatalf("Apply error = %v, want the joint-requirement error", err)
			}
		})
	}

	// A null beside a real rule key is still just absent: the policy is the other
	// direction's rules and the document is accepted.
	props := map[string]any{"name": "cnp", "egress": []any(nil), "ingress": []any{}}
	h := &traits.CiliumNetworkPolicyHandler{}
	if err := h.Apply(&oam.Trait{Type: "cilium-networkpolicy", Properties: props},
		stack.NewApplication("myapp", "production", nil), &stack.Bundle{}); err != nil {
		t.Fatalf("Apply with a null egress beside ingress: %v", err)
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

// Site 2 — a typed-nil endpointSelector passed the `!= nil` guard, was marshalled
// as `"endpointSelector": null`, and Cilium's EndpointSelector.UnmarshalJSON turns
// that into an allocated empty selector: a wildcard over every endpoint. The key is
// optional, so a null must render exactly as the key being absent.
func TestCiliumNetworkPolicyConfig_TypedNilEndpointSelectorRendersAsAbsent(t *testing.T) {
	absent := renderCNPSpec(t, &traits.CiliumNetworkPolicyConfig{Name: "cnp", Egress: []any{}})
	for name, sel := range map[string]any{
		"typed nil map": map[string]any(nil),
		"untyped nil":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			got := renderCNPSpec(t, &traits.CiliumNetworkPolicyConfig{
				Name: "cnp", EndpointSelector: sel, Egress: []any{},
			})
			if got != absent {
				t.Errorf("null endpointSelector rendered differently from an absent one:\n got %s\nwant %s", got, absent)
			}
		})
	}

	// The same holds end to end, from trait properties through Apply.
	h := &traits.CiliumNetworkPolicyHandler{}
	bundle := &stack.Bundle{}
	if err := h.Apply(&oam.Trait{Type: "cilium-networkpolicy", Properties: map[string]any{
		"name": "cnp", "endpointSelector": map[string]any(nil), "egress": []any{},
	}}, stack.NewApplication("myapp", "production", nil), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, ok := bundle.Applications[0].Config.(*traits.CiliumNetworkPolicyConfig)
	if !ok {
		t.Fatalf("expected *traits.CiliumNetworkPolicyConfig, got %T", bundle.Applications[0].Config)
	}
	if got := renderCNPSpec(t, cfg); got != absent {
		t.Errorf("Apply with a typed-nil endpointSelector rendered:\n got %s\nwant %s", got, absent)
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
