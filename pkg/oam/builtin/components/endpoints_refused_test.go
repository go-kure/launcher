package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestCnpgClusterHandler_EndpointsForPropertiesTheBuildRefuses: what
// ToApplicationConfig refuses of a cnpg-cluster's properties, and what
// Endpoints answers for the same component. Endpoints reads the object name
// only, so it answers for every one of them.
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
			if err != nil || len(eps) != 1 {
				t.Errorf("Endpoints = %+v, %v\nwant today's answer: one endpoint and no refusal", eps, err)
			}
		})
	}
}

// TestPostgresqlRule_EndpointsForAPoolerNamedLikeItsComponent: the lowering
// refuses a Pooler that would carry the name of the postgresql component it is
// generated from. EndpointsNamed is handed that one component, so it can see
// the same thing, and answers both endpoints where the Cluster is named apart,
// or refuses in the words of another rule where it is not. (A name the consumer
// hook gives the Pooler is shown where the hook is set, at the transformer's
// endpoint entry.)
func TestPostgresqlRule_EndpointsForAPoolerNamedLikeItsComponent(t *testing.T) {
	const lowering = `pooler: generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`
	pooler := map[string]any{"enabled": true}
	for _, tc := range []struct {
		name  string
		props map[string]any
		// today is the entry's refusal today, "" where it answers two endpoints.
		today string
	}{
		{
			name:  "the Cluster named apart",
			props: map[string]any{"pooler": pooler, "clusterObjectName": "pg", "poolerName": "db"},
		},
		{
			name:  "the Cluster named like the component",
			props: map[string]any{"pooler": pooler, "poolerName": "db"},
			today: `cluster.name "db": a pooler cannot have the same name as its cluster`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			doc := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
			if err == nil || !strings.Contains(err.Error(), lowering) {
				t.Fatalf("LowerComponent err = %v\nwant one containing %q", err, lowering)
			}

			eps, err := components.PostgresqlRule{}.EndpointsNamed(&comp, oam.LoweringContext{Component: &comp, Namer: oam.NewNameAllocator()})
			if tc.today == "" {
				if err != nil || len(eps) != 2 {
					t.Errorf("EndpointsNamed = %+v, %v\nwant today's answer: two endpoints and no refusal", eps, err)
				}
				return
			}
			if err == nil || err.Error() != tc.today {
				t.Errorf("EndpointsNamed err = %v\nwant today's refusal %q", err, tc.today)
			}
		})
	}
}
