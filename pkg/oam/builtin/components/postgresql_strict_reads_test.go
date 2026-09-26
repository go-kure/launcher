package components_test

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestPostgresql_WrongTypeIsRejected covers go-kure/launcher#512: the handler read
// its optional properties with bare comma-ok assertions, so a wrongly typed value was
// treated as absent and the cluster was built with a default or with nothing. Every
// read is now refused by path. The nested cases are the ones an authored document
// reaches too: the schema keeps these sub-objects open and types none of their
// fields. The top-level cases are reachable only from a direct handler call.
func TestPostgresql_WrongTypeIsRejected(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"provider", map[string]any{"provider": 1}, "provider"},
		{"version", map[string]any{"version": 16}, "version"},
		{"storageSize", map[string]any{"storageSize": 10}, "storageSize"},
		{"imageName", map[string]any{"imageName": true}, "imageName"},

		{"backup", map[string]any{"backup": "daily"}, "backup"},
		{"backup.retentionPolicy", map[string]any{"backup": map[string]any{"retentionPolicy": 3}}, "backup.retentionPolicy"},
		{"backup.destinationPath", map[string]any{"backup": map[string]any{"destinationPath": 1}}, "backup.destinationPath"},
		{"backup.endpointURL", map[string]any{"backup": map[string]any{"endpointURL": 1}}, "backup.endpointURL"},
		{"backup.secretName", map[string]any{"backup": map[string]any{"secretName": 1}}, "backup.secretName"},

		{"monitoring", map[string]any{"monitoring": true}, "monitoring"},
		{"monitoring.enabled", map[string]any{"monitoring": map[string]any{"enabled": "true"}}, "monitoring.enabled"},
		{"monitoring.customQueries", map[string]any{"monitoring": map[string]any{"customQueries": "q"}}, "monitoring.customQueries"},
		{"monitoring.customQueries[0]", map[string]any{"monitoring": map[string]any{"customQueries": []any{"q"}}}, "monitoring.customQueries[0]"},
		{"monitoring.customQueries[0].name", map[string]any{"monitoring": map[string]any{"customQueries": []any{map[string]any{"name": 1, "key": "k"}}}}, "monitoring.customQueries[0].name"},

		{"pooler", map[string]any{"pooler": true}, "pooler"},
		{"pooler.enabled", map[string]any{"pooler": map[string]any{"enabled": "yes"}}, "pooler.enabled"},
		{"pooler.type", map[string]any{"pooler": map[string]any{"type": 1}}, "pooler.type"},
		{"pooler.poolMode", map[string]any{"pooler": map[string]any{"poolMode": 1}}, "pooler.poolMode"},
		{"pooler.parameters", map[string]any{"pooler": map[string]any{"parameters": "x"}}, "pooler.parameters"},

		{"bootstrap", map[string]any{"bootstrap": "x"}, "bootstrap"},
		{"bootstrap.recovery", map[string]any{"bootstrap": map[string]any{"recovery": "x"}}, "bootstrap.recovery"},
		{"bootstrap.recovery.source", map[string]any{"bootstrap": map[string]any{"recovery": map[string]any{"source": 1}}}, "bootstrap.recovery.source"},
		{"bootstrap.pg_basebackup", map[string]any{"bootstrap": map[string]any{"pg_basebackup": "x"}}, "bootstrap.pg_basebackup"},
		{"bootstrap.pg_basebackup.source", map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{"source": 1}}}, "bootstrap.pg_basebackup.source"},

		{"externalClusters", map[string]any{"externalClusters": map[string]any{}}, "externalClusters"},
		{"externalClusters[0]", map[string]any{"externalClusters": []any{"x"}}, "externalClusters[0]"},
		{"externalClusters[0].name", map[string]any{"externalClusters": []any{map[string]any{"name": 1}}}, "externalClusters[0].name"},
		{"externalClusters[0] nameless", map[string]any{"externalClusters": []any{map[string]any{"connectionParameters": map[string]any{"host": "h"}}}}, "externalClusters[0]"},
		{"externalClusters[0].barmanObjectStore", map[string]any{"externalClusters": []any{map[string]any{"name": "e", "barmanObjectStore": "x"}}}, "externalClusters[0].barmanObjectStore"},
		{"externalClusters[0].connectionParameters", map[string]any{"externalClusters": []any{map[string]any{"name": "e", "connectionParameters": "x"}}}, "externalClusters[0].connectionParameters"},

		{"replication", map[string]any{"replication": "x"}, "replication"},
		{"replication.synchronous", map[string]any{"replication": map[string]any{"synchronous": "x"}}, "replication.synchronous"},
		{"replication.synchronous.method", map[string]any{"replication": map[string]any{"synchronous": map[string]any{"method": 1}}}, "replication.synchronous.method"},
		{"replication.synchronous.dataDurability", map[string]any{"replication": map[string]any{"synchronous": map[string]any{"dataDurability": 1}}}, "replication.synchronous.dataDurability"},

		{"postgresql", map[string]any{"postgresql": "x"}, "postgresql"},
		{"postgresql.parameters", map[string]any{"postgresql": map[string]any{"parameters": "x"}}, "postgresql.parameters"},

		{"inheritedMetadata", map[string]any{"inheritedMetadata": "x"}, "inheritedMetadata"},
		{"inheritedMetadata.labels", map[string]any{"inheritedMetadata": map[string]any{"labels": "x"}}, "inheritedMetadata.labels"},
		{"inheritedMetadata.annotations", map[string]any{"inheritedMetadata": map[string]any{"annotations": "x"}}, "inheritedMetadata.annotations"},

		{"managedRoles", map[string]any{"managedRoles": "x"}, "managedRoles"},
		{"managedRoles[0].name", map[string]any{"managedRoles": []any{map[string]any{"name": 1}}}, "managedRoles[0].name"},
		{"managedRoles[0].ensure", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "ensure": 1}}}, "managedRoles[0].ensure"},
		{"managedRoles[0].login", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "login": "true"}}}, "managedRoles[0].login"},
		{"managedRoles[0].superuser", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "superuser": "true"}}}, "managedRoles[0].superuser"},
		{"managedRoles[0].createdb", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "createdb": "true"}}}, "managedRoles[0].createdb"},
		{"managedRoles[0].createrole", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "createrole": "true"}}}, "managedRoles[0].createrole"},
		{"managedRoles[0].replication", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "replication": "true"}}}, "managedRoles[0].replication"},
		{"managedRoles[0].inherit", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "inherit": "false"}}}, "managedRoles[0].inherit"},
		{"managedRoles[0].passwordSecret", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "passwordSecret": 1}}}, "managedRoles[0].passwordSecret"},
		{"managedRoles[0].comment", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "comment": 1}}}, "managedRoles[0].comment"},
		{"managedRoles[0].inRoles", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "inRoles": "admin"}}}, "managedRoles[0].inRoles"},
		{"managedRoles[0].inRoles[1]", map[string]any{"managedRoles": []any{map[string]any{"name": "r", "inRoles": []any{"a", 2}}}}, "managedRoles[0].inRoles[1]"},

		{"objectStore", map[string]any{"objectStore": "x"}, "objectStore"},
		{"objectStore.destinationPath", map[string]any{"objectStore": map[string]any{"destinationPath": 1}}, "objectStore.destinationPath"},
		{"objectStore.endpointURL", map[string]any{"objectStore": map[string]any{"destinationPath": "s3://b", "endpointURL": 1}}, "objectStore.endpointURL"},
		{"objectStore.secretName", map[string]any{"objectStore": map[string]any{"destinationPath": "s3://b", "secretName": 1}}, "objectStore.secretName"},
		{"objectStore.retentionPolicy", map[string]any{"objectStore": map[string]any{"destinationPath": "s3://b", "retentionPolicy": 1}}, "objectStore.retentionPolicy"},
		{"objectStore.serverName", map[string]any{"objectStore": map[string]any{"destinationPath": "s3://b", "serverName": 1}}, "objectStore.serverName"},

		{"databases", map[string]any{"databases": "x"}, "databases"},
		{"databases[0].name", map[string]any{"databases": []any{map[string]any{"name": 1, "owner": "o"}}}, "databases[0].name"},
		{"databases[0].owner", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": 1}}}, "databases[0].owner"},
		{"databases[0].ensure", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": "o", "ensure": 1}}}, "databases[0].ensure"},
		{"databases[0].databaseReclaimPolicy", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": "o", "databaseReclaimPolicy": 1}}}, "databases[0].databaseReclaimPolicy"},
		{"databases[0].extensions", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": "o", "extensions": "x"}}}, "databases[0].extensions"},
		{"databases[0].extensions[0]", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": "o", "extensions": []any{"pgvector"}}}}, "databases[0].extensions[0]"},
		{"databases[0].extensions[0].name", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": "o", "extensions": []any{map[string]any{"name": 1}}}}}, "databases[0].extensions[0].name"},
		{"databases[0].extensions[0].ensure", map[string]any{"databases": []any{map[string]any{"name": "d", "owner": "o", "extensions": []any{map[string]any{"name": "e", "ensure": 1}}}}}, "databases[0].extensions[0].ensure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := postgresqlConfigFor(t, tc.props)
			if err == nil {
				t.Fatalf("props %v: accepted; the wrongly typed value was dropped", tc.props)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

// TestPostgresql_NullIsAbsence: an explicit null, including the typed nil a Go
// lowering rule leaves for an unset optional, reads as absent, as it does for every
// other kind. objectStore in particular used to read a typed nil map as present and
// then demand destinationPath.
func TestPostgresql_NullIsAbsence(t *testing.T) {
	cfg, err := postgresqlConfigFor(t, map[string]any{
		"version":     nil,
		"objectStore": map[string]any(nil),
		"pooler":      map[string]any{"enabled": nil},
		"databases":   []any(nil),
		"backup":      map[string]any{"retentionPolicy": nil},
	})
	if err != nil {
		t.Fatalf("nulls were refused: %v", err)
	}
	if cfg.Version != "16" || cfg.ObjectStore != nil || cfg.PoolerEnabled || cfg.Databases != nil || cfg.BackupRetentionPolicy != "" {
		t.Errorf("nulls did not read as absent: %+v", cfg)
	}
}

// TestPostgresql_TypedNullMatchesOmitted: a typed nil in a scalar slot reads
// exactly as an omitted key. storageSize used to count as authored because the
// presence check was `props[key] != nil`, which a typed nil passes, so the 1Gi
// fallback beat a policy default; pooler.instances and replication.synchronous.number
// refused a typed nil as an invalid number.
func TestPostgresql_TypedNullMatchesOmitted(t *testing.T) {
	policy := &stubPolicy{defaultStorageSize: "20Gi"}
	build := func(t *testing.T, props map[string]any) *components.PostgresqlConfig {
		t.Helper()
		pc := newPostgresqlApp(t, props)
		if err := stack.ApplicationConfig(pc).(oam.Enforceable).ApplyPolicy(policy); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		return pc
	}

	t.Run("storageSize", func(t *testing.T) {
		got := build(t, map[string]any{"storageSize": (*string)(nil)}).StorageSize
		want := build(t, map[string]any{}).StorageSize
		if got != want {
			t.Errorf("typed-nil storageSize = %q, omitted = %q", got, want)
		}
	})
	t.Run("pooler.instances", func(t *testing.T) {
		got := build(t, map[string]any{"pooler": map[string]any{"enabled": true, "instances": (*int)(nil)}}).PoolerInstances
		want := build(t, map[string]any{"pooler": map[string]any{"enabled": true}}).PoolerInstances
		if got != want {
			t.Errorf("typed-nil pooler.instances = %d, omitted = %d", got, want)
		}
	})
	t.Run("replication.synchronous.number", func(t *testing.T) {
		sync := func(extra map[string]any) map[string]any {
			s := map[string]any{"method": "any"}
			for k, v := range extra {
				s[k] = v
			}
			return map[string]any{"replication": map[string]any{"synchronous": s}}
		}
		got := build(t, sync(map[string]any{"number": (*int)(nil)})).SynchronousNumber
		want := build(t, sync(nil)).SynchronousNumber
		if got != want {
			t.Errorf("typed-nil synchronous.number = %d, omitted = %d", got, want)
		}
	})
}

// TestPostgresql_ManagedRoleFlagsAreIndependent: each boolean role flag lands on
// its own field, in the parsed config and on the generated Cluster. Setting one
// flag must set exactly that one, so a swapped destination (login granting
// superuser) fails here.
func TestPostgresql_ManagedRoleFlagsAreIndependent(t *testing.T) {
	flags := []string{"login", "superuser", "createdb", "createrole", "replication"}
	for _, flag := range flags {
		t.Run(flag, func(t *testing.T) {
			pc := newPostgresqlApp(t, map[string]any{"managedRoles": []any{
				map[string]any{"name": "app_user", flag: true, "inherit": false},
			}})
			if len(pc.ManagedRoles) != 1 {
				t.Fatalf("parsed %d roles, want 1", len(pc.ManagedRoles))
			}
			r := pc.ManagedRoles[0]
			parsed := map[string]bool{
				"login": r.Login, "superuser": r.Superuser, "createdb": r.CreateDB,
				"createrole": r.CreateRole, "replication": r.Replication,
			}
			cluster := (*generatePostgresql(t, pc)[0]).(*cnpgv1.Cluster)
			if cluster.Spec.Managed == nil || len(cluster.Spec.Managed.Roles) != 1 {
				t.Fatalf("generated managed roles = %v, want 1", cluster.Spec.Managed)
			}
			rc := cluster.Spec.Managed.Roles[0]
			generated := map[string]bool{
				"login": rc.Login, "superuser": rc.Superuser, "createdb": rc.CreateDB,
				"createrole": rc.CreateRole, "replication": rc.Replication,
			}
			for _, f := range flags {
				if parsed[f] != (f == flag) {
					t.Errorf("config %s = %v with only %s authored", f, parsed[f], flag)
				}
				if generated[f] != (f == flag) {
					t.Errorf("Cluster role %s = %v with only %s authored", f, generated[f], flag)
				}
			}
			if r.Inherit == nil || *r.Inherit || rc.Inherit == nil || *rc.Inherit {
				t.Errorf("inherit: false not carried: config %v, Cluster %v", r.Inherit, rc.Inherit)
			}
		})
	}
}

// TestPostgresql_EmptyEnumStillRefused: the strict reads keep an explicit "" as a
// value, so an enum authored empty still reaches its switch and is refused, as it
// was before.
func TestPostgresql_EmptyEnumStillRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"provider":       {"provider": ""},
		"pooler.type":    {"pooler": map[string]any{"type": ""}},
		"managedRoles":   {"managedRoles": []any{map[string]any{"name": "r", "ensure": ""}}},
		"replication":    {"replication": map[string]any{"synchronous": map[string]any{"method": ""}}},
		"databases":      {"databases": []any{map[string]any{"name": "d", "owner": "o", "databaseReclaimPolicy": ""}}},
		"pooler.mode":    {"pooler": map[string]any{"poolMode": ""}},
		"dataDurability": {"replication": map[string]any{"synchronous": map[string]any{"dataDurability": ""}}},
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := postgresqlConfigFor(t, props); err == nil {
				t.Errorf("props %v: an empty enum value was accepted", props)
			}
		})
	}
}

// TestPostgresql_EndpointsRefusesWrongTypedPooler: Endpoints reads pooler.enabled on
// its own, ahead of the config build, and used to report a wrongly typed value as
// "no pooler" — a NetworkPolicy endpoint missing for a pooler the build then refused
// or, for a typed-nil pooler, never emitted.
func TestPostgresql_EndpointsRefusesWrongTypedPooler(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.Endpoints(&oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{
		"pooler": map[string]any{"enabled": "true"},
	}})
	if err == nil || !strings.Contains(err.Error(), "pooler.enabled") {
		t.Errorf("Endpoints error = %v, want one naming pooler.enabled", err)
	}

	eps, err := h.Endpoints(&oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{
		"pooler": map[string]any{"enabled": true},
	}})
	if err != nil || len(eps) != 2 {
		t.Errorf("enabled pooler: Endpoints = (%d endpoints, %v), want (2, nil)", len(eps), err)
	}
}
