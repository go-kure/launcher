package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPostgresqlRule_UnauthoredRequiredStrings: the cnpg-cluster component a
// postgresql component lowers to is built from a typed spec, so a field of
// cnpgClusterRequired the author left out would reach the kind as an authored
// "" and pass its refusal. Of the strings the kind kept by hand before its
// list was generated, the lowering fills the parent of two, the base backup's
// source and the synchronous method, and builds each only from the string
// itself: postgresql refuses their block without it
// (TestPostgresqlRule_BlockNotBuilt). `backup` lowers to no in-tree
// spec.backup, only the Cluster's plugin entry, and `recovery` to an
// externalClusters entry naming the plugin, with no in-tree barmanObjectStore:
// their object stores are ObjectStores (go-kure/launcher#1001). The other
// generated fields the lowering writes, it writes with a value of its own: an
// external cluster's name and its plugin's, the number of synchronous
// standbys. Each case reads every path of cnpgClusterRequired in the lowered
// component, and no value there is the empty string.
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
			carried: map[string][]any{
				"externalClusters[].name":        {"db-recovery-source"},
				"externalClusters[].plugin.name": {"barman-cloud.cloudnative-pg.io"},
			},
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
				"externalClusters[].name":        {"origina"},
				"postgresql.synchronous.method":  {"any"},
				"postgresql.synchronous.number":  {int64(1)},
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
					if slices.Contains(values, any("")) {
						t.Errorf("the lowered cnpg-cluster carries an empty string at %s, which passes its refusal", path)
					}
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
// list and a "{}" suffix for each value of a map.
func requiredStringsAt(node any, segments []string) []any {
	if len(segments) == 0 {
		return []any{node}
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	key, list := strings.CutSuffix(segments[0], "[]")
	key, values := strings.CutSuffix(key, "{}")
	child, present := m[key]
	if !present || child == nil {
		return nil
	}
	var out []any
	switch {
	case list:
		items, _ := child.([]any)
		for _, item := range items {
			out = append(out, requiredStringsAt(item, segments[1:])...)
		}
	case values:
		entries, _ := child.(map[string]any)
		for _, k := range slices.Sorted(maps.Keys(entries)) {
			out = append(out, requiredStringsAt(entries[k], segments[1:])...)
		}
	default:
		return requiredStringsAt(child, segments[1:])
	}
	return out
}
