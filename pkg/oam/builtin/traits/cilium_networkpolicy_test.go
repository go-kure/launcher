package traits_test

import (
	"strings"
	"testing"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestCiliumNetworkPolicyHandler_Apply_PropagatesNamespace verifies that the
// emitted CiliumNetworkPolicy sub-app inherits its namespace from the component
// application — confirming the app parameter is correctly threaded through Apply.
func TestCiliumNetworkPolicyHandler_Apply_PropagatesNamespace(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	app := stack.NewApplication("myapp", "production", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "cilium-networkpolicy",
		Properties: map[string]any{
			"name":             "allow-egress",
			"endpointSelector": map[string]any{"matchLabels": map[string]any{"app": "api"}},
			"egress":           []any{map[string]any{"toEndpoints": []any{map[string]any{}}}},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	cnpApp := bundle.Applications[0]
	if cnpApp.Namespace != "production" {
		t.Errorf("cnpApp.Namespace = %q, want %q", cnpApp.Namespace, "production")
	}
}

// egressWithL7Rules builds an egress rule whose toPorts carries the given L7 rule
// shape, which is where Cilium's removed api.L7Rules fields would be supplied.
func egressWithL7Rules(rules map[string]any) []any {
	return []any{
		map[string]any{
			"toEndpoints": []any{
				map[string]any{"matchLabels": map[string]any{"app": "backend"}},
			},
			"toPorts": []any{
				map[string]any{
					"ports": []any{
						map[string]any{"port": "9092", "protocol": "TCP"},
					},
					"rules": rules,
				},
			},
		},
	}
}

// TestCiliumNetworkPolicyConfig_Generate_RejectsUnsupportedL7Rules is the
// regression guard for the silent policy-widening bug: Cilium 1.20 removed the
// kafka, l7proto and l7 fields from api.L7Rules, and a lenient json.Unmarshal
// dropped them without error — quietly turning an L7-restricted policy into an
// L4-only one. Generate must refuse instead.
func TestCiliumNetworkPolicyConfig_Generate_RejectsUnsupportedL7Rules(t *testing.T) {
	tests := []struct {
		name    string
		rules   map[string]any
		wantErr string
	}{
		{
			name:    "kafka",
			rules:   map[string]any{"kafka": []any{map[string]any{"role": "produce", "topic": "events"}}},
			wantErr: "kafka",
		},
		{
			name:    "l7proto",
			rules:   map[string]any{"l7proto": "cassandra"},
			wantErr: "l7proto",
		},
		{
			name:    "generic l7",
			rules:   map[string]any{"l7": []any{map[string]any{"action": "select"}}},
			wantErr: "l7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &traits.CiliumNetworkPolicyConfig{
				Name:   "restrict-" + tt.name,
				Egress: egressWithL7Rules(tt.rules),
			}
			objs, err := cfg.Generate(stack.NewApplication("myapp", "production", nil))
			if err == nil {
				t.Fatalf("Generate: expected an error for unsupported %q rule, got nil and %d object(s) — "+
					"the rule was silently dropped and the rendered policy is more permissive than authored",
					tt.name, len(objs))
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Generate error = %q, want it to name the rejected field %q", err, tt.wantErr)
			}
		})
	}
}

// TestCiliumNetworkPolicyConfig_Generate_AcceptsSupportedRules confirms strict
// decoding did not become over-strict: rule shapes the linked Cilium API still
// supports must render normally.
func TestCiliumNetworkPolicyConfig_Generate_AcceptsSupportedRules(t *testing.T) {
	cfg := &traits.CiliumNetworkPolicyConfig{
		Name:             "allow-http",
		EndpointSelector: map[string]any{"matchLabels": map[string]any{"app": "frontend"}},
		Egress: egressWithL7Rules(map[string]any{
			"http": []any{map[string]any{"method": "GET", "path": "/healthz"}},
		}),
	}
	objs, err := cfg.Generate(stack.NewApplication("myapp", "production", nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}

	// The decoded rule must carry what was authored, not merely decode: a rule
	// that decoded and was then dropped would render an empty spec.
	cnp, ok := (*objs[0]).(*ciliumv2.CiliumNetworkPolicy)
	if !ok || cnp.Spec == nil {
		t.Fatalf("object = %T with spec %v, want a CiliumNetworkPolicy with a spec", *objs[0], cnp)
	}
	spec := cnp.Spec
	if spec.EndpointSelector.LabelSelector == nil || !hasLabel(spec.EndpointSelector.LabelSelector.MatchLabels, "app", "frontend") {
		t.Errorf("endpointSelector = %+v, want app=frontend", spec.EndpointSelector.LabelSelector)
	}
	if len(spec.Ingress) != 0 {
		t.Errorf("ingress = %+v, want none", spec.Ingress)
	}
	if len(spec.Egress) != 1 {
		t.Fatalf("egress rules = %d, want 1", len(spec.Egress))
	}
	eg := spec.Egress[0]
	if len(eg.ToEndpoints) != 1 || eg.ToEndpoints[0].LabelSelector == nil ||
		!hasLabel(eg.ToEndpoints[0].LabelSelector.MatchLabels, "app", "backend") {
		t.Errorf("toEndpoints = %+v, want one selector app=backend", eg.ToEndpoints)
	}
	if len(eg.ToPorts) != 1 || len(eg.ToPorts[0].Ports) != 1 {
		t.Fatalf("toPorts = %+v, want one port rule with one port", eg.ToPorts)
	}
	if p := eg.ToPorts[0].Ports[0]; p.Port != "9092" || p.Protocol != "TCP" {
		t.Errorf("port = %s/%s, want 9092/TCP", p.Port, p.Protocol)
	}
	l7 := eg.ToPorts[0].Rules
	if l7 == nil || len(l7.HTTP) != 1 || l7.HTTP[0].Method != "GET" || l7.HTTP[0].Path != "/healthz" {
		t.Errorf("L7 rules = %+v, want one HTTP rule GET /healthz", l7)
	}
}

// hasLabel reports whether a Cilium selector's matchLabels carries key=value.
// Cilium stores an authored key with a source prefix ("any:app"), so the match
// is on the key's suffix.
func hasLabel(matchLabels map[string]string, key, value string) bool {
	for k, v := range matchLabels {
		if (k == key || strings.HasSuffix(k, ":"+key) || strings.HasSuffix(k, "."+key)) && v == value {
			return true
		}
	}
	return false
}

// TestCiliumNetworkPolicyConfig_Generate_RefusesUnknownSelectorKey: an unknown
// key inside a value Cilium decodes itself is refused by its path. The strict
// decode cannot refuse it (encoding/json does not carry DisallowUnknownFields
// into a type with its own UnmarshalJSON), and a selector whose only key was
// misspelt would build as the empty one, which matches every endpoint.
func TestCiliumNetworkPolicyConfig_Generate_RefusesUnknownSelectorKey(t *testing.T) {
	selector := map[string]any{"matchLabels": map[string]any{"app": "frontend"}}
	typo := map[string]any{"matchLabel": map[string]any{"app": "backend"}}
	cases := []struct {
		name     string
		selector map[string]any
		ingress  any
		egress   any
		want     string
	}{
		{name: "endpointSelector", selector: map[string]any{"matchLabel": map[string]any{"app": "frontend"}}, egress: []any{map[string]any{"toEndpoints": []any{map[string]any{}}}}, want: "endpointSelector.matchLabel"},
		{name: "endpointSelector beside a valid key", selector: map[string]any{"matchLabels": map[string]any{"app": "frontend"}, "bogusKey": "x"}, egress: []any{map[string]any{"toEndpoints": []any{map[string]any{}}}}, want: "endpointSelector.bogusKey"},
		{name: "null-valued key", selector: map[string]any{"matchLabel": nil}, egress: []any{map[string]any{"toEndpoints": []any{map[string]any{}}}}, want: "endpointSelector.matchLabel"},
		{name: "fromEndpoints", selector: selector, ingress: []any{map[string]any{"fromEndpoints": []any{selector, typo}}}, want: "ingress[0].fromEndpoints[1].matchLabel"},
		{name: "toEndpoints", selector: selector, egress: []any{map[string]any{"toEndpoints": []any{typo}}}, want: "egress[0].toEndpoints[0].matchLabel"},
		{name: "fromNodes", selector: selector, ingress: []any{map[string]any{"fromNodes": []any{typo}}}, want: "ingress[0].fromNodes[0].matchLabel"},
		{name: "toNodes", selector: selector, egress: []any{map[string]any{"toNodes": []any{typo}}}, want: "egress[0].toNodes[0].matchLabel"},
		{name: "cidrGroupSelector", selector: selector, egress: []any{map[string]any{"toCIDRSet": []any{map[string]any{"cidrGroupSelector": typo}}}}, want: "egress[0].toCIDRSet[0].cidrGroupSelector.matchLabel"},
		{name: "matchExpressions element", selector: selector, egress: []any{map[string]any{"toEndpoints": []any{map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "Exists", "value": "x"}}}}}}, want: "egress[0].toEndpoints[0].matchExpressions[0].value"},
		{name: "icmps field", selector: selector, egress: []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{map[string]any{"family": "IPv4", "type": 8, "code": 0}}}}}}, want: "egress[0].icmps[0].fields[0].code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &traits.CiliumNetworkPolicyConfig{Name: "p", EndpointSelector: tc.selector, Ingress: tc.ingress, Egress: tc.egress}
			_, err := cfg.Generate(stack.NewApplication("myapp", "production", nil))
			if err == nil {
				t.Fatalf("Generate succeeded, want unknown field %q refused", tc.want)
			}
			if want := `unknown field "` + tc.want + `"`; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to contain %s", err, want)
			}
		})
	}
}

// TestCiliumNetworkPolicyConfig_Generate_ICMPFieldWithoutType: Cilium's own
// decoding of an ICMP field dereferences a nil pointer when `type` is absent or
// null. The build must refuse the document, not crash on it.
func TestCiliumNetworkPolicyConfig_Generate_ICMPFieldWithoutType(t *testing.T) {
	for name, field := range map[string]map[string]any{
		"absent": {"family": "IPv4"},
		"null":   {"family": "IPv4", "type": nil},
		"empty":  {},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &traits.CiliumNetworkPolicyConfig{
				Name:             "p",
				EndpointSelector: map[string]any{},
				Egress:           []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{field}}}}},
			}
			_, err := cfg.Generate(stack.NewApplication("myapp", "production", nil))
			if err == nil {
				t.Fatal("Generate succeeded, want the ICMP field without a type refused")
			}
			for _, want := range []string{`cilium-networkpolicy "p"`, "api.Rule", "panicked"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestCiliumNetworkPolicyConfig_Generate_KeepsValidSelectors is the control for
// the refusal above: every key a selector and an ICMP field declare builds.
func TestCiliumNetworkPolicyConfig_Generate_KeepsValidSelectors(t *testing.T) {
	selector := map[string]any{
		"matchLabels":      map[string]any{"app": "frontend"},
		"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"web"}}},
	}
	cfg := &traits.CiliumNetworkPolicyConfig{
		Name:             "p",
		EndpointSelector: selector,
		Ingress:          []any{map[string]any{"fromEndpoints": []any{selector, map[string]any{}}}},
		Egress: []any{
			map[string]any{"toEndpoints": []any{selector}},
			map[string]any{"toCIDRSet": []any{map[string]any{"cidrGroupSelector": selector}}},
			map[string]any{"icmps": []any{map[string]any{"fields": []any{map[string]any{"family": "IPv4", "type": 8}, map[string]any{"type": "EchoRequest"}}}}},
		},
	}
	objs, err := cfg.Generate(stack.NewApplication("myapp", "production", nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cnp, ok := (*objs[0]).(*ciliumv2.CiliumNetworkPolicy)
	if !ok || cnp.Spec == nil {
		t.Fatalf("object = %T, want a CiliumNetworkPolicy with a spec", *objs[0])
	}
	if got := cnp.Spec.EndpointSelector.LabelSelector; got == nil || len(got.MatchExpressions) != 1 || !hasLabel(got.MatchLabels, "app", "frontend") {
		t.Errorf("endpointSelector = %+v, want the authored labels and expression", got)
	}
	if len(cnp.Spec.Egress) != 3 || len(cnp.Spec.Egress[2].ICMPs) != 1 || len(cnp.Spec.Egress[2].ICMPs[0].Fields) != 2 {
		t.Errorf("egress = %+v, want three rules, the last with two ICMP fields", cnp.Spec.Egress)
	}
}
