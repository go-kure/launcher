package registry_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// postgresqlKeyVariants holds the variants of every postgresql schema key. A key
// that creates a member (backup, pooler, databases, recovery) has a smaller
// value of the same key in its base, so both documents emit the same members.
func postgresqlKeyVariants() map[string][]keyVariant {
	store := func(path string) map[string]any {
		return map[string]any{"destinationPath": path, "s3Credentials": map[string]any{"inheritFromIAMRole": true}}
	}
	schedule := map[string]any{"name": "daily", "schedule": "0 0 3 * * *"}
	backup := map[string]any{"objectStore": store("s3://bucket/a"), "schedules": []any{schedule}}
	pooler := map[string]any{"enabled": true}
	databases := []any{map[string]any{"name": "app", "owner": "app"}}
	const image = "ghcr.io/org/postgresql:17"

	clusterAt := func(path string) reportedAt { return place("cnpg-cluster", path) }
	storeAt := func(path string) reportedAt { return place("cnpg-objectstore", path) }
	poolerAt := func(path string) reportedAt { return place("cnpg-pooler", path) }
	databaseAt := func(path string) reportedAt { return place("cnpg-database", path) }
	scheduledAt := func(path string) reportedAt { return place("cnpg-scheduledbackup", path) }

	// A key whose value lands at one path of the Cluster.
	onCluster := func(path string, base map[string]any, value any) []keyVariant {
		return []keyVariant{{name: "cluster", base: base, value: value, moves: []reportedAt{clusterAt(path)}}}
	}
	none := map[string]any{}

	return map[string][]keyVariant{
		"provider":   {{name: "written nowhere", base: none, value: "cnpg"}},
		"poolerName": {{name: "names the Pooler", base: map[string]any{"pooler": pooler}, value: "gateway"}},
		"clusterObjectName": {{
			name:  "referring members",
			base:  map[string]any{"pooler": pooler, "databases": databases, "backup": backup},
			value: "main",
			moves: []reportedAt{poolerAt("cluster"), databaseAt("cluster"), scheduledAt("cluster")},
		}},
		"version": {
			{name: "default image", base: none, value: "17", moves: []reportedAt{clusterAt("imageName")}},
			{name: "imageName set", base: map[string]any{"imageName": image}, value: "17"},
		},
		"imageName": onCluster("imageName", none, image),
		// The base authors a size too: with none the Cluster holds an empty storage
		// block, which any size replaces.
		"storageSize":       onCluster("storage.size", map[string]any{"storageSize": "1Gi"}, "2Gi"),
		"replicas":          onCluster("instances", none, 3),
		"resources":         onCluster("resources", none, map[string]any{"requests": map[string]any{"cpu": "1"}, "limits": map[string]any{"cpu": "2"}}),
		"inheritedMetadata": onCluster("inheritedMetadata", none, map[string]any{"labels": map[string]any{"team": "a"}}),
		"monitoring":        onCluster("monitoring", none, map[string]any{"enabled": true}),
		"affinity":          onCluster("affinity", none, map[string]any{"topologyKey": "topology.kubernetes.io/zone"}),
		"postgresql":        onCluster("postgresql.parameters", none, map[string]any{"parameters": map[string]any{"max_connections": "200"}}),
		"replication":       onCluster("postgresql.synchronous", none, map[string]any{"synchronous": map[string]any{"method": "any"}}),
		"managedRoles":      onCluster("managed.roles", none, []any{map[string]any{"name": "reader"}}),
		"externalClusters":  onCluster("externalClusters", none, []any{map[string]any{"name": "source"}}),
		"bootstrap": {
			{name: "initdb", base: none, value: map[string]any{"initdb": map[string]any{"database": "shop", "owner": "shop"}},
				moves: []reportedAt{clusterAt("bootstrap")}},
			{name: "pg_basebackup", base: map[string]any{"externalClusters": []any{map[string]any{"name": "source"}}},
				value: map[string]any{"pg_basebackup": map[string]any{"source": "source"}}, moves: []reportedAt{clusterAt("bootstrap")}},
		},
		"recovery": {
			{
				name: "inline source",
				base: map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "a", "objectStore": store("s3://bucket/a")}}},
				value: map[string]any{
					"source":   map[string]any{"serverName": "b", "objectStore": store("s3://bucket/b")},
					"target":   map[string]any{"time": "2026-01-01T00:00:00Z"},
					"database": "shop",
					"owner":    "shop",
					"secret":   map[string]any{"name": "credentials"},
				},
				moves: []reportedAt{clusterAt("bootstrap.recovery"), clusterAt("externalClusters"), storeAt("configuration")},
			},
			{
				name:  "named source",
				base:  map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "a", "objectStoreName": "one"}}},
				value: map[string]any{"source": map[string]any{"serverName": "a", "objectStoreName": "two"}},
				moves: []reportedAt{clusterAt("externalClusters")},
				idle:  []reportedAt{clusterAt("bootstrap.recovery")},
			},
		},
		"backup": {{
			name: "every setting",
			base: map[string]any{"backup": backup},
			value: map[string]any{
				"objectStore":           store("s3://bucket/b"),
				"objectStoreObjectName": "archive",
				"retentionPolicy":       "30d",
				"serverName":            "folder",
				"schedules": []any{map[string]any{
					"name": "daily", "schedule": "0 0 4 * * *", "immediate": false, "suspend": true,
					"target": "primary", "backupOwnerReference": "none",
				}},
			},
			moves: []reportedAt{
				clusterAt("plugins"), storeAt("configuration"), storeAt("retentionPolicy"),
				scheduledAt("schedule"), scheduledAt("immediate"), scheduledAt("suspend"),
				scheduledAt("target"), scheduledAt("backupOwnerReference"),
			},
		}},
		"pooler": {{
			name: "every setting",
			base: map[string]any{"pooler": pooler},
			value: map[string]any{
				"enabled": true, "instances": 2, "type": "ro", "poolMode": "transaction",
				"parameters": map[string]any{"max_client_conn": "10"}, "image": "ghcr.io/org/pgbouncer:1.2",
			},
			moves: []reportedAt{poolerAt("instances"), poolerAt("type"), poolerAt("pgbouncer")},
		}},
		"databases": {{
			name: "every setting",
			base: map[string]any{"databases": databases},
			value: []any{map[string]any{
				"name": "shop", "owner": "shop", "ensure": "absent", "databaseReclaimPolicy": "delete",
				"extensions": []any{map[string]any{"name": "vector"}},
			}},
			moves: []reportedAt{
				databaseAt("name"), databaseAt("owner"), databaseAt("ensure"),
				databaseAt("databaseReclaimPolicy"), databaseAt("extensions"),
			},
		}},
	}
}

// The one condition of the postgresql map is on version's place, and no place is
// Unchanged: the rule builds every value.
func TestLoweringKeys_PostgresqlConditionsAndUnchanged(t *testing.T) {
	keys := keyedRules()["postgresql"].LoweringKeys()
	for key, places := range keys {
		for _, p := range places {
			var want []oam.LoweredWhen
			if key == "version" {
				want = []oam.LoweredWhen{{Key: "imageName", Values: []string{""}}}
			}
			if !reflect.DeepEqual(p.When, want) {
				t.Errorf("%s: place %s %q is conditional on %v, want %v", key, p.Member, p.Path, p.When, want)
			}
			if p.Unchanged {
				t.Errorf("%s: place %s %q is declared unchanged", key, p.Member, p.Path)
			}
		}
	}
}

// The Cluster and the backup ObjectStore carry the component's name; the Pooler,
// each database and schedule and the inline recovery store are components of
// their own.
func TestLoweringKeys_PostgresqlMemberNames(t *testing.T) {
	store := map[string]any{"destinationPath": "s3://bucket/a", "s3Credentials": map[string]any{"inheritFromIAMRole": true}}
	source := map[string]any{"destinationPath": "s3://bucket/b", "s3Credentials": map[string]any{"inheritFromIAMRole": true}}
	comp := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{
		"pooler":    map[string]any{"enabled": true},
		"databases": []any{map[string]any{"name": "shop", "owner": "shop"}},
		"backup":    map[string]any{"objectStore": store, "schedules": []any{map[string]any{"name": "daily", "schedule": "0 0 3 * * *"}}},
		"recovery":  map[string]any{"source": map[string]any{"serverName": "old", "objectStore": source}},
	}}
	res, err := registry.ComponentLoweringRules()["postgresql"].LowerComponent(&comp, lowerContext())
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	var got []string
	for _, c := range res.Components {
		got = append(got, c.Type+" "+c.Name)
	}
	slices.Sort(got)
	want := []string{
		"cnpg-cluster db", "cnpg-database db-shop", "cnpg-objectstore db", "cnpg-objectstore db-recovery-source",
		"cnpg-pooler db-pooler", "cnpg-scheduledbackup db-daily",
	}
	if !slices.Equal(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
}

// A postgresql component with no backup lowers to one component under its name,
// and the report names no member there; with backup the Cluster and the
// ObjectStore share the name, and the report names cnpg-cluster.
func TestLoweringKeys_PostgresqlClusterMemberInTheReport(t *testing.T) {
	backup := map[string]any{"objectStore": map[string]any{
		"destinationPath": "s3://bucket/a", "s3Credentials": map[string]any{"inheritFromIAMRole": true},
	}}
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  []string
	}{
		{"no backup", map[string]any{}, []string{""}},
		{"backup", map[string]any{"backup": backup}, []string{"cnpg-cluster"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, oam.Component{Name: "db", Type: "postgresql", Properties: tc.props})
			var members []string
			for _, e := range prov.Entries {
				if e.Component == "db" && e.Path == "storage.size" && !slices.Contains(members, e.Member) {
					members = append(members, e.Member)
				}
			}
			if !slices.Equal(members, tc.want) {
				t.Errorf("members of the entry at storage.size = %q, want %q", members, tc.want)
			}
		})
	}
}
