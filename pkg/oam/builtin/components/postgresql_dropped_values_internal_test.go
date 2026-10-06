package components

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPostgresqlRule_BlockNotBuilt: five blocks of a postgresql component are
// built only when one field of theirs is set (go-kure/launcher#790): the
// Cluster's backup (retentionPolicy or destinationPath), its bootstrap (the
// source of recovery or of pg_basebackup), its synchronous replication
// (method) and its monitoring (enabled), and the Pooler (enabled). Each case
// authors other values of such a block and not that field. The document
// builds, and the result holds none of what the author wrote: the Cluster has
// no such block, and no Pooler is emitted.
func TestPostgresqlRule_BlockNotBuilt(t *testing.T) {
	queries := []any{map[string]any{"name": "queries", "key": "custom.yaml"}}
	for _, tc := range []struct {
		name  string
		props map[string]any
		// absent is the path, in the lowered cnpg-cluster properties, of the
		// block the authored values belong in. Empty for the pooler, which is
		// a component of its own.
		absent []string
	}{
		{
			name:   "backup with an endpoint and no retention policy or path",
			props:  map[string]any{"backup": map[string]any{"endpointURL": "https://s3.example"}},
			absent: []string{"backup"},
		},
		{
			name:   "backup with a secret and no retention policy or path",
			props:  map[string]any{"backup": map[string]any{"secretName": "s3-credentials"}},
			absent: []string{"backup"},
		},
		{
			name:   "backup with an endpoint and an empty retention policy",
			props:  map[string]any{"backup": map[string]any{"endpointURL": "https://s3.example", "retentionPolicy": ""}},
			absent: []string{"backup"},
		},
		{
			name:   "backup with an empty path only",
			props:  map[string]any{"backup": map[string]any{"destinationPath": ""}},
			absent: []string{"backup"},
		},
		{
			name:   "base backup without a source",
			props:  map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{}}},
			absent: []string{"bootstrap"},
		},
		{
			name:   "base backup with an empty source",
			props:  map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{"source": ""}}},
			absent: []string{"bootstrap"},
		},
		{
			name:   "recovery without a source",
			props:  map[string]any{"bootstrap": map[string]any{"recovery": map[string]any{}}},
			absent: []string{"bootstrap"},
		},
		{
			name:   "recovery with an empty source",
			props:  map[string]any{"bootstrap": map[string]any{"recovery": map[string]any{"source": ""}}},
			absent: []string{"bootstrap"},
		},
		{
			name:   "synchronous replication with a number and no method",
			props:  map[string]any{"replication": map[string]any{"synchronous": map[string]any{"number": 2}}},
			absent: []string{"postgresql", "synchronous"},
		},
		{
			name:   "synchronous replication with a durability and no method",
			props:  map[string]any{"replication": map[string]any{"synchronous": map[string]any{"dataDurability": "required"}}},
			absent: []string{"postgresql", "synchronous"},
		},
		{
			name:   "monitoring with custom queries and no enabled",
			props:  map[string]any{"monitoring": map[string]any{"customQueries": queries}},
			absent: []string{"monitoring"},
		},
		{
			name:   "monitoring with custom queries, switched off",
			props:  map[string]any{"monitoring": map[string]any{"enabled": false, "customQueries": queries}},
			absent: []string{"monitoring"},
		},
		{
			name:  "pooler with instances and no enabled",
			props: map[string]any{"pooler": map[string]any{"instances": 2}},
		},
		{
			name:  "pooler with a type and no enabled",
			props: map[string]any{"pooler": map[string]any{"type": "ro"}},
		},
		{
			name:  "pooler with a pool mode and no enabled",
			props: map[string]any{"pooler": map[string]any{"poolMode": "transaction"}},
		},
		{
			name:  "pooler with parameters and no enabled",
			props: map[string]any{"pooler": map[string]any{"parameters": map[string]any{"max_client_conn": "200"}}},
		},
		{
			name:  "pooler with instances, switched off",
			props: map[string]any{"pooler": map[string]any{"enabled": false, "instances": 2}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			res, err := PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err != nil {
				t.Fatalf("lowering refused: %v", err)
			}
			var cluster *oam.Component
			for i := range res.Components {
				switch res.Components[i].Type {
				case "cnpg-cluster":
					cluster = &res.Components[i]
				case "cnpg-pooler":
					t.Errorf("the lowering emitted a Pooler: %v", res.Components[i].Properties)
				}
			}
			if cluster == nil {
				t.Fatal("the lowering emitted no cnpg-cluster component")
			}
			if len(tc.absent) == 0 {
				return
			}
			var node any = cluster.Properties
			for _, key := range tc.absent {
				m, _ := node.(map[string]any)
				child, present := m[key]
				if !present {
					return
				}
				node = child
			}
			t.Errorf("the lowered cnpg-cluster holds %v at %v, want nothing", node, tc.absent)
		})
	}
}
