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
// "" and pass its refusal. The lowering fills the parent of four of the
// eleven: the backup's and an external cluster's object store path, the base
// backup's source and the synchronous method. The first two it builds from
// other authored settings, so postgresql refuses them unauthored, under the
// property the author wrote; the last two it builds only from the string
// itself. Each case reads every path of cnpgClusterRequired in the lowered
// component.
func TestPostgresqlRule_UnauthoredRequiredStrings(t *testing.T) {
	const (
		backupPath   = "backup.barmanObjectStore.destinationPath"
		externalPath = "externalClusters[].barmanObjectStore.destinationPath"
	)
	external := func(stores ...map[string]any) []any {
		out := make([]any, 0, len(stores))
		for i, store := range stores {
			out = append(out, map[string]any{"name": "origin" + string(rune('a'+i)), "barmanObjectStore": store})
		}
		return out
	}
	for _, tc := range []struct {
		name    string
		props   map[string]any
		refused string
		carried map[string][]any
	}{
		{
			name:    "backup with a retention policy and no path",
			props:   map[string]any{"backup": map[string]any{"retentionPolicy": "30d"}},
			refused: "backup.destinationPath: required (",
		},
		{
			name:    "backup with a retention policy and a null path",
			props:   map[string]any{"backup": map[string]any{"retentionPolicy": "30d", "destinationPath": nil}},
			refused: "backup.destinationPath: required (",
		},
		{
			name:    "external cluster with an empty object store",
			props:   map[string]any{"externalClusters": external(map[string]any{})},
			refused: "externalClusters[0].barmanObjectStore.destinationPath: required (",
		},
		{
			name: "second external cluster with an object store and no path",
			props: map[string]any{"externalClusters": external(
				map[string]any{"destinationPath": "s3://bucket/a"},
				map[string]any{"endpointURL": "https://s3.example"},
			)},
			refused: "externalClusters[1].barmanObjectStore.destinationPath: required (",
		},
		{
			// The object store map is decoded into the upstream type, which
			// folds the key, so a null in another spelling is no path either.
			name:    "external cluster with a null path in another spelling",
			props:   map[string]any{"externalClusters": external(map[string]any{"DestinationPath": nil})},
			refused: "externalClusters[0].barmanObjectStore.destinationPath: required (",
		},
		{
			name:    "external cluster with its path in another spelling",
			props:   map[string]any{"externalClusters": external(map[string]any{"DestinationPath": "s3://bucket/a"})},
			carried: map[string][]any{externalPath: {"s3://bucket/a"}},
		},
		{
			name:    "backup without a retention policy builds no object store",
			props:   map[string]any{"backup": map[string]any{"endpointURL": "https://s3.example", "retentionPolicy": ""}},
			carried: map[string][]any{},
		},
		{
			name:    "base backup without a source builds no bootstrap",
			props:   map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{}}},
			carried: map[string][]any{},
		},
		{
			name:    "synchronous replication without a method builds none",
			props:   map[string]any{"replication": map[string]any{"synchronous": map[string]any{"number": 1}}},
			carried: map[string][]any{},
		},
		{
			name: "an authored empty path is a value",
			props: map[string]any{
				"backup":           map[string]any{"retentionPolicy": "30d", "destinationPath": ""},
				"externalClusters": external(map[string]any{"destinationPath": ""}),
			},
			carried: map[string][]any{backupPath: {""}, externalPath: {""}},
		},
		{
			name: "all four authored",
			props: map[string]any{
				"backup":           map[string]any{"retentionPolicy": "30d", "destinationPath": "s3://bucket/db"},
				"externalClusters": external(map[string]any{"destinationPath": "s3://bucket/a"}),
				"bootstrap":        map[string]any{"pg_basebackup": map[string]any{"source": "origina"}},
				"replication":      map[string]any{"synchronous": map[string]any{"method": "any", "number": 1}},
			},
			carried: map[string][]any{
				backupPath:                       {"s3://bucket/db"},
				externalPath:                     {"s3://bucket/a"},
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
