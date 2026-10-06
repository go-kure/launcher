package components

import (
	"reflect"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPostgresqlRule_BlockNotBuilt: five blocks of a postgresql component are
// built only when one field of theirs is set (go-kure/launcher#790): the
// Cluster's backup (destinationPath), its bootstrap (the source of recovery or
// of pg_basebackup), its synchronous replication (method) and its monitoring
// (enabled), and the Pooler (enabled). A document that authors other values of
// such a block and not that field used to build, with none of them in the
// result. It is refused where the author wrote the block, by the name of the
// field that is missing. One such document was refused already and did not
// build: a backup with a retention policy that is not empty and no path.
//
// Three shapes still build. An authored `enabled: false` is the block's own
// switch, so the settings beside it are kept in the document and build
// nothing. An outer block with nothing in it (backup, bootstrap, replication,
// monitoring or pooler as {}), or with an empty list or map only, authors no
// value to drop; an empty bootstrap.recovery, bootstrap.pg_basebackup or
// replication.synchronous is refused like one without its field. An authored
// empty backup path is a value: the backup is built from it.
func TestPostgresqlRule_BlockNotBuilt(t *testing.T) {
	const (
		pathRequired   = "backup.destinationPath: required (the object store path backups and WAL are written to)"
		methodRequired = "replication.synchronous.method: required (any or first)"
		copied         = "(the externalClusters entry the cluster is copied from)"
		recovered      = "(the externalClusters entry the cluster is recovered from)"
		poolerOnly     = " (the Pooler is emitted only when it is true)"
	)
	queries := []any{map[string]any{"name": "queries", "key": "custom.yaml"}}
	for _, tc := range []struct {
		name    string
		props   map[string]any
		refused string
		// cluster is what the lowered cnpg-cluster properties hold at
		// clusterPath, nil for nothing. pooler is the Pooler's properties, nil
		// when none is emitted.
		clusterPath []string
		cluster     any
		pooler      map[string]any
	}{
		{
			name:    "backup with an endpoint and no path",
			props:   map[string]any{"backup": map[string]any{"endpointURL": "https://s3.example"}},
			refused: pathRequired,
		},
		{
			name:    "backup with a secret and no path",
			props:   map[string]any{"backup": map[string]any{"secretName": "s3-credentials"}},
			refused: pathRequired,
		},
		{
			name:    "backup with an endpoint and an empty retention policy",
			props:   map[string]any{"backup": map[string]any{"endpointURL": "https://s3.example", "retentionPolicy": ""}},
			refused: pathRequired,
		},
		{
			name:    "backup with an empty retention policy only",
			props:   map[string]any{"backup": map[string]any{"retentionPolicy": ""}},
			refused: pathRequired,
		},
		{
			name:        "backup with an empty path only",
			props:       map[string]any{"backup": map[string]any{"destinationPath": ""}},
			clusterPath: []string{"backup"},
			cluster:     map[string]any{"barmanObjectStore": map[string]any{"destinationPath": ""}},
		},
		{
			name: "backup with a path, an endpoint and a secret",
			props: map[string]any{"backup": map[string]any{
				"destinationPath": "s3://bucket/db", "endpointURL": "https://s3.example", "secretName": "s3-credentials",
			}},
			clusterPath: []string{"backup", "barmanObjectStore", "endpointURL"},
			cluster:     "https://s3.example",
		},
		{
			name:        "empty backup",
			props:       map[string]any{"backup": map[string]any{}},
			clusterPath: []string{"backup"},
		},
		{
			name:    "base backup without a source",
			props:   map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{}}},
			refused: "bootstrap.pg_basebackup.source: required " + copied,
		},
		{
			name:    "base backup with a null source",
			props:   map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{"source": nil}}},
			refused: "bootstrap.pg_basebackup.source: required " + copied,
		},
		{
			name:    "base backup with an empty source",
			props:   map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{"source": ""}}},
			refused: "bootstrap.pg_basebackup.source: must not be empty " + copied,
		},
		{
			name:    "recovery without a source",
			props:   map[string]any{"bootstrap": map[string]any{"recovery": map[string]any{}}},
			refused: "bootstrap.recovery.source: required " + recovered,
		},
		{
			name:    "recovery with an empty source",
			props:   map[string]any{"bootstrap": map[string]any{"recovery": map[string]any{"source": ""}}},
			refused: "bootstrap.recovery.source: must not be empty " + recovered,
		},
		{
			name:        "recovery with a source",
			props:       map[string]any{"bootstrap": map[string]any{"recovery": map[string]any{"source": "origin"}}},
			clusterPath: []string{"bootstrap"},
			cluster:     map[string]any{"recovery": map[string]any{"source": "origin"}},
		},
		{
			name:        "empty bootstrap",
			props:       map[string]any{"bootstrap": map[string]any{}},
			clusterPath: []string{"bootstrap"},
		},
		{
			name:    "synchronous replication with a number and no method",
			props:   map[string]any{"replication": map[string]any{"synchronous": map[string]any{"number": 2}}},
			refused: methodRequired,
		},
		{
			name:    "synchronous replication with a durability and no method",
			props:   map[string]any{"replication": map[string]any{"synchronous": map[string]any{"dataDurability": "required"}}},
			refused: methodRequired,
		},
		{
			name:    "empty synchronous replication",
			props:   map[string]any{"replication": map[string]any{"synchronous": map[string]any{}}},
			refused: methodRequired,
		},
		{
			name: "synchronous replication with a method, a number and a durability",
			props: map[string]any{"replication": map[string]any{"synchronous": map[string]any{
				"method": "first", "number": 2, "dataDurability": "required",
			}}},
			clusterPath: []string{"postgresql", "synchronous"},
			// failoverQuorum has no omitempty in the upstream type.
			cluster: map[string]any{"method": "first", "number": int64(2), "dataDurability": "required", "failoverQuorum": false},
		},
		{
			name:        "empty replication",
			props:       map[string]any{"replication": map[string]any{}},
			clusterPath: []string{"postgresql", "synchronous"},
		},
		{
			name:    "monitoring with custom queries and no enabled",
			props:   map[string]any{"monitoring": map[string]any{"customQueries": queries}},
			refused: "monitoring.enabled: required where monitoring.customQueries is set (the Cluster's monitoring is built only when it is true)",
		},
		{
			name:        "monitoring with custom queries, switched off",
			props:       map[string]any{"monitoring": map[string]any{"enabled": false, "customQueries": queries}},
			clusterPath: []string{"monitoring"},
		},
		{
			name:        "monitoring with an empty list of custom queries and no enabled",
			props:       map[string]any{"monitoring": map[string]any{"customQueries": []any{}}},
			clusterPath: []string{"monitoring"},
		},
		{
			name:        "monitoring with custom queries, switched on",
			props:       map[string]any{"monitoring": map[string]any{"enabled": true, "customQueries": queries}},
			clusterPath: []string{"monitoring", "customQueriesConfigMap"},
			cluster:     []any{map[string]any{"name": "queries", "key": "custom.yaml"}},
		},
		{
			name:    "pooler with instances and no enabled",
			props:   map[string]any{"pooler": map[string]any{"instances": 2}},
			refused: "pooler.enabled: required where pooler.instances is set" + poolerOnly,
		},
		{
			name:    "pooler with a type and no enabled",
			props:   map[string]any{"pooler": map[string]any{"type": "ro"}},
			refused: "pooler.enabled: required where pooler.type is set" + poolerOnly,
		},
		{
			name:    "pooler with a pool mode and no enabled",
			props:   map[string]any{"pooler": map[string]any{"poolMode": "transaction"}},
			refused: "pooler.enabled: required where pooler.poolMode is set" + poolerOnly,
		},
		{
			name:    "pooler with parameters and no enabled",
			props:   map[string]any{"pooler": map[string]any{"parameters": map[string]any{"max_client_conn": "200"}}},
			refused: "pooler.enabled: required where pooler.parameters is set" + poolerOnly,
		},
		{
			name:    "pooler with instances and a null enabled",
			props:   map[string]any{"pooler": map[string]any{"enabled": nil, "instances": 2}},
			refused: "pooler.enabled: required where pooler.instances is set" + poolerOnly,
		},
		{
			name:  "pooler with instances, switched off",
			props: map[string]any{"pooler": map[string]any{"enabled": false, "instances": 2, "type": "ro"}},
		},
		{
			name:  "pooler with an empty map of parameters and no enabled",
			props: map[string]any{"pooler": map[string]any{"parameters": map[string]any{}}},
		},
		{
			name:  "empty pooler",
			props: map[string]any{"pooler": map[string]any{}},
		},
		{
			name:  "pooler with instances and a type, switched on",
			props: map[string]any{"pooler": map[string]any{"enabled": true, "instances": 2, "type": "ro"}},
			pooler: map[string]any{
				"cluster":   map[string]any{"name": "db"},
				"type":      "ro",
				"instances": int64(2),
				"pgbouncer": map[string]any{"poolMode": "session"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			res, err := PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if tc.refused != "" {
				if err == nil || err.Error() != tc.refused {
					t.Fatalf("got %v, want the refusal %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("lowering refused: %v", err)
			}
			var cluster *oam.Component
			var pooler map[string]any
			for i := range res.Components {
				switch res.Components[i].Type {
				case "cnpg-cluster":
					cluster = &res.Components[i]
				case "cnpg-pooler":
					pooler = res.Components[i].Properties
				}
			}
			if cluster == nil {
				t.Fatal("the lowering emitted no cnpg-cluster component")
			}
			if !reflect.DeepEqual(pooler, tc.pooler) {
				t.Errorf("the lowered Pooler is %v, want %v", pooler, tc.pooler)
			}
			if tc.clusterPath == nil {
				return
			}
			var held any = cluster.Properties
			for _, key := range tc.clusterPath {
				m, _ := held.(map[string]any)
				held = m[key]
			}
			if !reflect.DeepEqual(held, tc.cluster) {
				t.Errorf("the lowered cnpg-cluster holds %v at %v, want %v", held, tc.clusterPath, tc.cluster)
			}
		})
	}
}

// TestPostgresqlRule_PoolerInstances: what the emitted Pooler holds at
// instances for each authored pooler.instances. The Pooler CRD gives the field
// a default of 1 and no minimum, so a Pooler without it runs one pod. An
// authored 0 or negative count used to be left out of the Pooler, where the
// operator's default replaced it. It is written as authored: the upstream type
// carries a 0, and the CRD does not bound the count, so refusing a negative one
// is left to the cluster. The cnpg-pooler kind takes each.
func TestPostgresqlRule_PoolerInstances(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pooler map[string]any
		want   any
	}{
		{name: "not authored", pooler: map[string]any{"enabled": true}, want: int64(3)},
		{name: "two", pooler: map[string]any{"enabled": true, "instances": 2}, want: int64(2)},
		{name: "zero", pooler: map[string]any{"enabled": true, "instances": 0}, want: int64(0)},
		{name: "negative", pooler: map[string]any{"enabled": true, "instances": -1}, want: int64(-1)},
		{name: "null", pooler: map[string]any{"enabled": true, "instances": nil}, want: int64(3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"pooler": tc.pooler}}
			res, err := PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err != nil {
				t.Fatalf("lowering refused: %v", err)
			}
			var pooler *oam.Component
			for i := range res.Components {
				if res.Components[i].Type == "cnpg-pooler" {
					pooler = &res.Components[i]
				}
			}
			if pooler == nil {
				t.Fatal("the lowering emitted no cnpg-pooler component")
			}
			if got := pooler.Properties["instances"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the lowered Pooler holds %v (%T) at instances, want %v", got, got, tc.want)
			}
			if _, err := (&CnpgPoolerHandler{}).ToApplicationConfig(pooler, "default"); err != nil {
				t.Errorf("cnpg-pooler refuses the lowered component: %v", err)
			}
		})
	}
}
