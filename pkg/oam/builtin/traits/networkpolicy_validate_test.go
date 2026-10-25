package traits_test

import (
	"errors"
	"testing"

	launchererrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// trafficSourceCases are networkPolicy values with the field path the first
// refusal names; wantField "" means the value is valid.
var trafficSourceCases = []struct {
	name          string
	networkPolicy any
	wantField     string
}{
	{"valid", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge"},
		map[string]any{"namespace": "mon", "podSelector": map[string]any{"matchLabels": map[string]any{"app": "prom"}}},
	}}, ""},
	{"empty list disables", map[string]any{"trafficSources": []any{}}, ""},
	{"not an object", "edge", "networkPolicy"},
	{"null", nil, "networkPolicy"},
	{"unsupported key", map[string]any{"trafficSources": []any{}, "egress": true}, "networkPolicy"},
	{"trafficSources missing", map[string]any{}, "networkPolicy.trafficSources"},
	{"trafficSources null", map[string]any{"trafficSources": nil}, "networkPolicy.trafficSources"},
	{"trafficSources typed nil", map[string]any{"trafficSources": []any(nil)}, "networkPolicy.trafficSources"},
	{"trafficSources not a list", map[string]any{"trafficSources": "edge"}, "networkPolicy.trafficSources"},
	{"entry not an object", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge"}, "mon",
	}}, "networkPolicy.trafficSources[1]"},
	{"entry unsupported key", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge", "port": 80},
	}}, "networkPolicy.trafficSources[0]"},
	{"namespace missing", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge"},
		map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"k": "v"}}},
	}}, "networkPolicy.trafficSources[1].namespace"},
	{"namespace empty", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": ""},
	}}, "networkPolicy.trafficSources[0].namespace"},
	{"first invalid entry wins", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge"},
		map[string]any{"namespace": 7},
		map[string]any{},
	}}, "networkPolicy.trafficSources[1].namespace"},
	{"podSelector not an object", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge", "podSelector": "app=web"},
	}}, "networkPolicy.trafficSources[0].podSelector"},
	{"podSelector without matchLabels", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge", "podSelector": map[string]any{}},
	}}, "networkPolicy.trafficSources[0].podSelector"},
	{"matchLabels null", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge", "podSelector": map[string]any{"matchLabels": nil}},
	}}, "networkPolicy.trafficSources[0].podSelector"},
	{"matchLabels value not a string", map[string]any{"trafficSources": []any{
		map[string]any{"namespace": "edge", "podSelector": map[string]any{"matchLabels": map[string]any{"tier": 1}}},
	}}, "networkPolicy.trafficSources[0].podSelector"},
}

func TestValidateTrafficSources(t *testing.T) {
	for _, tc := range trafficSourceCases {
		t.Run(tc.name, func(t *testing.T) {
			err := traits.ValidateTrafficSources(map[string]any{"networkPolicy": tc.networkPolicy})
			checkTrafficSourceRefusal(t, err, tc.wantField)
			if err == nil {
				return
			}
			var ve *launchererrors.ValidationError
			errors.As(err, &ve)
			if ve.Component != "" {
				t.Errorf("Component = %q, want empty: the caller attaches the component", ve.Component)
			}
		})
	}
}

func TestValidateTrafficSources_NoNetworkPolicy(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"nil props":   nil,
		"empty props": {},
		"other keys":  {"rules": []any{}},
	} {
		if err := traits.ValidateTrafficSources(props); err != nil {
			t.Errorf("%s: ValidateTrafficSources = %v, want nil", name, err)
		}
	}
}

// TestValidateTrafficSources_MatchesApply holds the exported check to the one
// each routing trait (ingress, httproute, and expose on either rendering)
// applies when it renders: the same values are refused, at the same field path.
func TestValidateTrafficSources_MatchesApply(t *testing.T) {
	routers := []struct {
		name  string
		apply func(networkPolicy any) error
	}{
		{"ingress", func(np any) error {
			trait := ingressTrafficSourcesTrait(map[string]any{"networkPolicy": np})
			return (&traits.IngressHandler{}).Apply(trait, newWebApp("my-app", "default"), newBundle())
		}},
		{"httproute", func(np any) error {
			trait := &oam.Trait{Type: "httproute", Properties: map[string]any{
				"parentRefs":    []any{map[string]any{"name": "gw"}},
				"rules":         []any{map[string]any{}},
				"networkPolicy": np,
			}}
			return (&traits.HTTPRouteHandler{}).Apply(trait, newWebApp("my-app", "default"), newBundle())
		}},
		{"expose ingress", func(np any) error {
			trait := &oam.Trait{Type: "expose", Properties: map[string]any{
				"controllerType": "ingress", "ingressClassName": "nginx",
				"hostnames": []any{"shop.example.com"}, "networkPolicy": np,
			}}
			return applyExpose(trait, newWebApp("my-app", "default"), newBundle())
		}},
		{"expose gateway", func(np any) error {
			trait := &oam.Trait{Type: "expose", Properties: map[string]any{
				"controllerType": "gateway", "gatewayName": "public",
				"hostnames": []any{"shop.example.com"}, "networkPolicy": np,
			}}
			return applyExpose(trait, newWebApp("my-app", "default"), newBundle())
		}},
	}
	for _, r := range routers {
		for _, tc := range trafficSourceCases {
			t.Run(r.name+"/"+tc.name, func(t *testing.T) {
				checkTrafficSourceRefusal(t, r.apply(tc.networkPolicy), tc.wantField)
			})
		}
	}
}

func checkTrafficSourceRefusal(t *testing.T, err error, wantField string) {
	t.Helper()
	if wantField == "" {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("err = nil, want a refusal at %q", wantField)
	}
	var ve *launchererrors.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %T %v, want *errors.ValidationError", err, err)
	}
	if ve.Field != wantField {
		t.Errorf("Field = %q, want %q (err: %v)", ve.Field, wantField, err)
	}
}
