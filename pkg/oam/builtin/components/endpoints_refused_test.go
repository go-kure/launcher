package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestCnpgClusterHandler_EndpointsForPropertiesTheBuildRefuses: what
// ToApplicationConfig refuses of a cnpg-cluster's properties, Endpoints refuses
// for the same component, in the same words. A component it accepts keeps its
// endpoint, whatever it authors.
func TestCnpgClusterHandler_EndpointsForPropertiesTheBuildRefuses(t *testing.T) {
	h := &components.CnpgClusterHandler{}
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			"a misspelt key below a declared one",
			map[string]any{"storage": map[string]any{"sise": "1Gi"}},
			`properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec: json: unknown field "sise"`,
		},
		{
			"a misspelt key authored as null",
			map[string]any{"storage": map[string]any{"sise": nil}},
			`properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec: json: unknown field "sise"`,
		},
		{
			"a null array element",
			map[string]any{"imagePullSecrets": []any{nil}},
			`imagePullSecrets[0]: `,
		},
		{
			"instances that is no integer",
			map[string]any{"instances": "three"},
			`instances: `,
		},
		{
			"no instance",
			map[string]any{"instances": 0},
			`instances: must be >= 1, got 0`,
		},
		{
			"a required string left out below its parent",
			map[string]any{"replica": map[string]any{"enabled": true}},
			`replica.source: required`,
		},
		{
			"an image without a tag",
			map[string]any{"imageName": "ghcr.io/example/postgres"},
			`imageName: image "ghcr.io/example/postgres" rejected: no tag or digest specified`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "cnpg-cluster", Properties: tc.props}
			_, buildErr := h.ToApplicationConfig(comp, "data")
			if buildErr == nil || !strings.Contains(buildErr.Error(), tc.want) {
				t.Fatalf("ToApplicationConfig err = %v\nwant one containing %q", buildErr, tc.want)
			}
			eps, err := h.Endpoints(comp)
			if err == nil || err.Error() != buildErr.Error() || eps != nil {
				t.Errorf("Endpoints = %+v, %v\nwant no endpoint and ToApplicationConfig's refusal: %v", eps, err, buildErr)
			}
		})
	}

	accepted := &oam.Component{Name: "db", Type: "cnpg-cluster", Properties: map[string]any{
		"instances": 3,
		"imageName": "ghcr.io/example/postgres:17.2",
		"storage":   map[string]any{"size": "10Gi"},
	}}
	if _, err := h.ToApplicationConfig(accepted, "data"); err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	eps, err := h.Endpoints(accepted)
	if err != nil || len(eps) != 1 || eps[0].PodSelector.MatchLabels["cnpg.io/cluster"] != "db" {
		t.Errorf("Endpoints = %+v, %v\nwant cnpg.io/cluster=db", eps, err)
	}
}

// TestPostgresqlRule_EndpointsForAPoolerNamedLikeItsComponent: the lowering
// refuses a Pooler that would carry the name of the postgresql component it is
// generated from. EndpointsNamed is handed that one component, so it sees the
// same thing and refuses it in the lowering's words, ahead of the Pooler's own
// rule against carrying its Cluster's name, as the lowering does. (A name the
// consumer hook gives the Pooler is shown where the hook is set, at the
// transformer's endpoint entry.)
//
// A Pooler named like another component of the document is refused by the
// lowering alike, and not by EndpointsNamed, which is not given the document.
func TestPostgresqlRule_EndpointsForAPoolerNamedLikeItsComponent(t *testing.T) {
	pooler := map[string]any{"enabled": true}
	lower := func(comp oam.Component) error {
		doc := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp, {Name: "api", Type: "webservice"}}}}
		_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
		return err
	}
	endpoints := func(comp oam.Component) ([]string, error) {
		eps, err := components.PostgresqlRule{}.EndpointsNamed(&comp, oam.LoweringContext{Component: &comp, Namer: oam.NewNameAllocator()})
		var selected []string
		for _, ep := range eps {
			for key, value := range ep.PodSelector.MatchLabels {
				selected = append(selected, key+"="+value)
			}
		}
		return selected, err
	}

	const own = `pooler: generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`
	for _, tc := range []struct {
		name  string
		props map[string]any
	}{
		{"the Cluster named apart", map[string]any{"pooler": pooler, "clusterObjectName": "pg", "poolerName": "db"}},
		{"the Cluster named like the component", map[string]any{"pooler": pooler, "poolerName": "db"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			lowerErr := lower(comp)
			if lowerErr == nil || lowerErr.Error() != own {
				t.Fatalf("LowerComponent err = %v\nwant %q", lowerErr, own)
			}
			selected, err := endpoints(comp)
			if err == nil || err.Error() != own || selected != nil {
				t.Errorf("EndpointsNamed = %v, %v\nwant no endpoint and the lowering's refusal %q", selected, err, own)
			}
		})
	}

	t.Run("another component's name is not seen", func(t *testing.T) {
		comp := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"pooler": pooler, "poolerName": "api"}}
		const other = `pooler: generates component "api", which is already the name of component "api" (type "webservice") in the document; rename one of them`
		if err := lower(comp); err == nil || err.Error() != other {
			t.Fatalf("LowerComponent err = %v\nwant %q", err, other)
		}
		selected, err := endpoints(comp)
		if want := []string{"cnpg.io/cluster=db", "cnpg.io/poolerName=api"}; err != nil || !slices.Equal(selected, want) {
			t.Errorf("EndpointsNamed = %v, %v\nwant %v", selected, err, want)
		}
	})
}
