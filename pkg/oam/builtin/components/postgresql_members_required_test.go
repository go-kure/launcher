package components_test

import (
	"maps"
	"reflect"
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
	ran := 0
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		t.Run(name, func(t *testing.T) {
			ran++
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
					// The engine refuses a null, typed or not, as it refuses an
					// absent key (pkg/oam/property_validate.go).
					if v, written := member.Properties[key]; !written || isNull(v) {
						t.Errorf("%s %q is emitted without %s, which its schema marks Required", member.Type, member.Name, key)
					}
					held[member.Type]++
				}
			}
		})
	}
	// Vacuity guard: each of the three kinds was emitted and has a Required
	// property that was looked for. It reads every case, so it holds only
	// when -run selected them all.
	if ran < len(cases) {
		return
	}
	for _, typ := range []string{"cnpg-objectstore", "cnpg-pooler", "cnpg-database"} {
		if held[typ] == 0 {
			t.Errorf("no Required property of an emitted %s was looked for", typ)
		}
	}
}

// isNull reports a value the engine reads as null: nil, or a nil map, slice,
// pointer, channel or function held in an any. It mirrors pkg/oam's unexported
// isNullValue.
func isNull(v any) bool {
	if v == nil {
		return true
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}
