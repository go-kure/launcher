package components_test

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

// An authored bootstrap.initdb is honoured on the generated Cluster: its
// database and owner reach spec.bootstrap.initdb. Before go-kure/launcher#1269
// the parse read no key under bootstrap but pg_basebackup, so the block was
// dropped and the Cluster kept the operator default (database and owner `app`).
func TestPostgresql_BootstrapInitdbIsHonoured(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"bootstrap": map[string]any{
			"initdb": map[string]any{"database": "orders", "owner": "orders_owner"},
		},
	})
	objs := generatePostgresql(t, pc)
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	b := cluster.Spec.Bootstrap
	if b == nil || b.InitDB == nil {
		t.Fatalf("spec.bootstrap.initdb is absent: the authored bootstrap.initdb was dropped (bootstrap = %+v)", b)
	}
	if b.InitDB.Database != "orders" || b.InitDB.Owner != "orders_owner" {
		t.Errorf("spec.bootstrap.initdb = {database %q, owner %q}, want {orders, orders_owner}", b.InitDB.Database, b.InitDB.Owner)
	}
	if b.PgBaseBackup != nil || b.Recovery != nil {
		t.Errorf("spec.bootstrap carries another method: %+v", b)
	}
}

// Without an initdb the Cluster keeps the operator's bootstrap: nothing is
// written for it.
func TestPostgresql_NoBootstrapWritesNone(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{})
	cluster := (*generatePostgresql(t, pc)[0]).(*cnpgv1.Cluster)
	if cluster.Spec.Bootstrap != nil {
		t.Errorf("spec.bootstrap = %+v, want none", cluster.Spec.Bootstrap)
	}
}

// Every key under bootstrap is read or refused naming it, never dropped, and so
// is every initdb key the component does not model, and an initdb that
// CloudNativePG's webhook would refuse (one of database and owner, another
// bootstrap method beside it) is refused here, at the author's path.
func TestPostgresql_BootstrapInitdbRefusals(t *testing.T) {
	ok := map[string]any{"database": "orders", "owner": "orders_owner"}
	with := func(key string, value any) map[string]any {
		m := map[string]any{"database": "orders", "owner": "orders_owner"}
		m[key] = value
		return m
	}
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"unknown bootstrap key", map[string]any{"bootstrap": map[string]any{"initdbb": ok}}, `bootstrap: unrecognized key "initdbb"`},
		{"initdb wrong type", map[string]any{"bootstrap": map[string]any{"initdb": "x"}}, "bootstrap.initdb"},
		{"initdb encoding", map[string]any{"bootstrap": map[string]any{"initdb": with("encoding", "UTF8")}}, `bootstrap.initdb: unrecognized key "encoding"`},
		{"initdb secret", map[string]any{"bootstrap": map[string]any{"initdb": with("secret", map[string]any{"name": "s"})}}, `bootstrap.initdb: unrecognized key "secret"`},
		{"initdb postInitSQL", map[string]any{"bootstrap": map[string]any{"initdb": with("postInitSQL", []any{"select 1"})}}, `bootstrap.initdb: unrecognized key "postInitSQL"`},
		{"initdb import", map[string]any{"bootstrap": map[string]any{"initdb": with("import", map[string]any{})}}, `bootstrap.initdb: unrecognized key "import"`},
		{"database without owner", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"database": "orders"}}}, "bootstrap.initdb.owner: required"},
		{"owner without database", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"owner": "o"}}}, "bootstrap.initdb.database: required"},
		{"empty initdb", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{}}}, "bootstrap.initdb.database: required"},
		{"empty database", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"database": "", "owner": "o"}}}, "bootstrap.initdb.database: must not be empty"},
		{"empty owner", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"database": "d", "owner": ""}}}, "bootstrap.initdb.owner: must not be empty"},
		{"database wrong type", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"database": 1, "owner": "o"}}}, "bootstrap.initdb.database: must be a string"},
		{"owner wrong type", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"database": "d", "owner": true}}}, "bootstrap.initdb.owner: must be a string"},
		{"with pg_basebackup", map[string]any{"bootstrap": map[string]any{"initdb": ok, "pg_basebackup": map[string]any{"source": "src"}}}, "bootstrap.initdb: not with bootstrap.pg_basebackup"},
		{"with recovery", map[string]any{"bootstrap": map[string]any{"initdb": ok}, "recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStoreName": "origin"}}}, "recovery: not with bootstrap.initdb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := postgresqlConfigFor(t, tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// A null initdb is absence, as for every other optional block.
func TestPostgresql_BootstrapInitdbNullIsAbsence(t *testing.T) {
	cfg, err := postgresqlConfigFor(t, map[string]any{"bootstrap": map[string]any{"initdb": nil, "pg_basebackup": map[string]any{"source": "src"}}})
	if err != nil {
		t.Fatalf("a null initdb was refused: %v", err)
	}
	if cfg.BootstrapInitdbDatabase != "" || cfg.BootstrapPgBasebackupSource != "src" {
		t.Errorf("config = %+v, want pg_basebackup only", cfg)
	}
}
