package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// A cnpg-cluster and a cnpg-pooler endpoint selector follows `objectName` and falls back to the
// component name without it. The `objectName` description must not say otherwise
// (TestObjectNameDescription_NamesTheEndpointSelector).
func TestCnpgEndpoints_SelectorFollowsObjectName(t *testing.T) {
	cases := []struct {
		name      string
		typ       string
		handler   oam.ComponentHandler
		props     map[string]any
		wantLabel string
	}{
		{"cluster default", "cnpg-cluster", &components.CnpgClusterHandler{}, map[string]any{}, "cnpg.io/cluster"},
		{"cluster renamed", "cnpg-cluster", &components.CnpgClusterHandler{}, map[string]any{"objectName": "db-main"}, "cnpg.io/cluster"},
		{"pooler default", "cnpg-pooler", &components.CnpgPoolerHandler{}, minimalPooler(), "cnpg.io/poolerName"},
		{"pooler renamed", "cnpg-pooler", &components.CnpgPoolerHandler{}, withObjectName(minimalPooler(), "bouncer-main"), "cnpg.io/poolerName"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := oam.NewTransformer(nil, nil)
			tr.RegisterComponent(c.typ, c.handler)
			// Keep the Pooler's resolved name different from its cluster name ("db"): equal names are refused.
			comp := &oam.Component{Name: "orders", Type: c.typ, Properties: c.props}
			want := "orders"
			if v, ok := c.props["objectName"].(string); ok {
				want = v
			}
			eps, err := tr.ComponentEndpoints(comp)
			if err != nil || len(eps) != 1 {
				t.Fatalf("endpoints = %+v, err = %v; want one", eps, err)
			}
			if got := eps[0].PodSelector.MatchLabels[c.wantLabel]; got != want {
				t.Errorf("selector %s = %q, want %q", c.wantLabel, got, want)
			}
		})
	}
}

// The `objectName` description is published on every kind component. It names the one exception
// to "selectors keep the component name": the endpoint selector of the two cnpg kinds above.
// This checks that the text names that exception and both kinds; that it is true is
// TestCnpgEndpoints_SelectorFollowsObjectName.
func TestObjectNameDescription_NamesTheEndpointSelector(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("cnpg-cluster", &components.CnpgClusterHandler{})
	desc := tr.HandlerSchemas().Components["cnpg-cluster"]["objectName"].Description
	for _, want := range []string{"endpoint selector", "cnpg-cluster", "cnpg-pooler"} {
		if !strings.Contains(desc, want) {
			t.Errorf("objectName description = %q, want it to contain %q", desc, want)
		}
	}
}

func withObjectName(props map[string]any, name string) map[string]any {
	props["objectName"] = name
	return props
}
