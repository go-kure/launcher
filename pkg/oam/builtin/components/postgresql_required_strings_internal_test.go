package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPostgresqlRule_UnauthoredRequiredStrings: the cnpg-cluster component a
// postgresql component lowers to is built from a typed spec, so a string of
// cnpgClusterRequired the author left out would reach the kind as an authored
// "" and pass its refusal. The lowering fills the parent of two of the eleven,
// the base backup's source and the synchronous method, and builds each only
// from the string itself: postgresql refuses their block without it
// (TestPostgresqlRule_BlockNotBuilt). `backup` lowers to no in-tree
// spec.backup, only the Cluster's plugin entry, and `recovery` to an
// externalClusters entry naming the plugin, with no in-tree barmanObjectStore:
// their object stores are ObjectStores (go-kure/launcher#1001). Each case reads
// every path of cnpgClusterRequired in the lowered component.
func TestPostgresqlRule_UnauthoredRequiredStrings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		props   map[string]any
		refused string
		carried map[string][]any
	}{
		{
			name:    "base backup without a source",
			props:   map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{}}},
			refused: "bootstrap.pg_basebackup.source: required (",
		},
		{
			name:    "synchronous replication without a method",
			props:   map[string]any{"replication": map[string]any{"synchronous": map[string]any{"number": 1}}},
			refused: "replication.synchronous.method: required (",
		},
		{
			name:    "no block at all",
			props:   map[string]any{},
			carried: map[string][]any{},
		},
		{
			name: "recovery from an inline store",
			props: map[string]any{"recovery": map[string]any{"source": map[string]any{
				"serverName":  "origin",
				"objectStore": map[string]any{"destinationPath": "s3://bucket/a", "s3Credentials": map[string]any{"inheritFromIAMRole": true}},
			}}},
			carried: map[string][]any{},
		},
		{
			name: "both authored",
			props: map[string]any{
				"externalClusters": []any{map[string]any{"name": "origina", "connectionParameters": map[string]any{"host": "h"}}},
				"bootstrap":        map[string]any{"pg_basebackup": map[string]any{"source": "origina"}},
				"replication":      map[string]any{"synchronous": map[string]any{"method": "any", "number": 1}},
			},
			carried: map[string][]any{
				"bootstrap.pg_basebackup.source": {"origina"},
				"postgresql.synchronous.method":  {"any"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			res, err := PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if tc.refused != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.refused) {
					t.Fatalf("got %v, want a refusal starting %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("lowering refused: %v", err)
			}
			var cluster *oam.Component
			for i := range res.Components {
				if res.Components[i].Type == "cnpg-cluster" {
					cluster = &res.Components[i]
				}
			}
			if cluster == nil {
				t.Fatal("the lowering emitted no cnpg-cluster component")
			}
			carried := map[string][]any{}
			for path := range cnpgClusterRequired {
				if values := requiredStringsAt(cluster.Properties, strings.Split(path, ".")); len(values) > 0 {
					carried[path] = values
				}
			}
			if !reflect.DeepEqual(carried, tc.carried) {
				t.Errorf("the lowered cnpg-cluster carries %v at the required strings, want %v", carried, tc.carried)
			}
			if _, err := (&CnpgClusterHandler{}).ToApplicationConfig(cluster, "default"); err != nil {
				t.Errorf("cnpg-cluster refuses the lowered component: %v", err)
			}
		})
	}
}

// requiredStringsAt returns every value node holds at a path written as the
// keys of cnpgClusterRequired are, a "[]" suffix standing for each entry of a
// list.
func requiredStringsAt(node any, segments []string) []any {
	if len(segments) == 0 {
		return []any{node}
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	key, list := strings.CutSuffix(segments[0], "[]")
	child, present := m[key]
	if !present || child == nil {
		return nil
	}
	if !list {
		return requiredStringsAt(child, segments[1:])
	}
	items, _ := child.([]any)
	var out []any
	for _, item := range items {
		out = append(out, requiredStringsAt(item, segments[1:])...)
	}
	return out
}
