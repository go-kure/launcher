package components_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestPostgresqlRule_MembersHoldTheRequiredProperties: every component the
// postgresql rule emits carries each property its kind's schema marks
// Required, also where the author wrote nothing under the member
// (go-kure/launcher#790). The engine holds a top-level Required on an emitted
// component, so a member emitted without one would be refused in the schema's
// words where its kind refused it before. Of the kinds whose schema gained a
// Required property with that issue, postgresql's rule emits three
// (cnpg-pooler, cnpg-database, cnpg-objectstore) and no rule emits the others.
func TestPostgresqlRule_MembersHoldTheRequiredProperties(t *testing.T) {
	schemas := map[string]map[string]oam.PropertySchema{
		"cnpg-cluster":     (&components.CnpgClusterHandler{}).PropertySchema(),
		"cnpg-objectstore": (&components.CnpgObjectStoreHandler{}).PropertySchema(),
		"cnpg-pooler":      (&components.CnpgPoolerHandler{}).PropertySchema(),
		"cnpg-database":    (&components.CnpgDatabaseHandler{}).PropertySchema(),
	}
	cases := map[string]map[string]any{
		"nothing authored":                  {},
		"a pooler with nothing under it":    {"pooler": map[string]any{"enabled": true}},
		"a database of a name and an owner": {"databases": []any{map[string]any{"name": "orders", "owner": "app"}}},
		"an object store of a path":         {"objectStore": map[string]any{"destinationPath": "s3://bucket/db/"}},
		"all three": {
			"pooler":      map[string]any{"enabled": true},
			"objectStore": map[string]any{"destinationPath": "s3://bucket/db/"},
			"databases":   []any{map[string]any{"name": "orders", "owner": "app"}, map[string]any{"name": "billing", "owner": "app"}},
		},
	}
	held := map[string]int{}
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		t.Run(name, func(t *testing.T) {
			res := lowerPostgresql(t, &oam.Component{Name: "db", Type: "postgresql", Properties: cases[name]}, nil)
			for _, member := range res.Components {
				schema, ok := schemas[member.Type]
				if !ok {
					t.Fatalf("the rule emits a %s, whose schema this test does not read", member.Type)
				}
				for _, key := range slices.Sorted(maps.Keys(schema)) {
					if !schema[key].Required {
						continue
					}
					if _, written := member.Properties[key]; !written {
						t.Errorf("%s %q is emitted without %s, which its schema marks Required", member.Type, member.Name, key)
					}
					held[member.Type]++
				}
			}
		})
	}
	// Vacuity guard: each of the three kinds was emitted and has a Required
	// property that was looked for.
	for _, typ := range []string{"cnpg-objectstore", "cnpg-pooler", "cnpg-database"} {
		if held[typ] == 0 {
			t.Errorf("no Required property of an emitted %s was looked for", typ)
		}
	}
}
