package components_test

import (
	"reflect"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// minimalDatabase is the smallest cnpg-database the Database CRD admits.
func minimalDatabase() map[string]any {
	return map[string]any{"cluster": map[string]any{"name": "db"}, "name": "app", "owner": "app"}
}

func cnpgDatabaseErr(t *testing.T, props map[string]any) error {
	t.Helper()
	_, err := (&components.CnpgDatabaseHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db-app", Type: "cnpg-database", Properties: props}, "data")
	return err
}

func newCnpgDatabase(t *testing.T, props map[string]any) *components.CnpgDatabaseConfig {
	t.Helper()
	cfg, err := (&components.CnpgDatabaseHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db-app", Type: "cnpg-database", Properties: props}, "data")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg.(*components.CnpgDatabaseConfig)
}

func generateCnpgDatabase(t *testing.T, c *components.CnpgDatabaseConfig) *cnpgv1.Database {
	t.Helper()
	objs, err := c.Generate(stack.NewApplication("db-app", "data", c))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate: got %d objects, want 1", len(objs))
	}
	database, ok := (*objs[0]).(*cnpgv1.Database)
	if !ok {
		t.Fatalf("Generate: got %T, want *cnpgv1.Database", *objs[0])
	}
	return database
}

func TestCnpgDatabaseHandler_CanHandle(t *testing.T) {
	h := &components.CnpgDatabaseHandler{}
	if !h.CanHandle("cnpg-database") {
		t.Error("CanHandle(cnpg-database) = false")
	}
	if h.CanHandle("cnpg-cluster") {
		t.Error("CanHandle(cnpg-cluster) = true")
	}
}

// TestCnpgDatabaseHandler_MinimalWritesNoOpinions: the smallest admitted
// component yields a Database carrying only identity and the three required
// fields — no ensure or reclaim policy of launcher's own.
func TestCnpgDatabaseHandler_MinimalWritesNoOpinions(t *testing.T) {
	c := newCnpgDatabase(t, minimalDatabase())
	if err := c.ApplyPolicy(&stubPolicy{maxCPU: "1", allowedRegistries: []string{"registry.invalid"}}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	database := generateCnpgDatabase(t, c)
	if database.Name != "db-app" || database.Namespace != "data" {
		t.Errorf("identity = %s/%s, want data/db-app", database.Namespace, database.Name)
	}
	if database.APIVersion != "postgresql.cnpg.io/v1" || database.Kind != "Database" {
		t.Errorf("GVK = %s %s", database.APIVersion, database.Kind)
	}
	want := cnpgv1.DatabaseSpec{ClusterRef: corev1.LocalObjectReference{Name: "db"}, Name: "app", Owner: "app"}
	if !reflect.DeepEqual(database.Spec, want) {
		t.Errorf("spec = %+v, want only cluster, name and owner", database.Spec)
	}
}

func TestCnpgDatabaseHandler_DecodesDeepBlocks(t *testing.T) {
	p := minimalDatabase()
	p["ensure"] = "absent"
	p["databaseReclaimPolicy"] = "delete"
	p["connectionLimit"] = 0
	p["allowConnections"] = false
	p["extensions"] = []any{map[string]any{"name": "pgvector", "ensure": "present"}}
	p["schemas"] = []any{map[string]any{"name": "app", "owner": "app"}}
	s := newCnpgDatabase(t, p).Spec
	if s.Ensure != cnpgv1.EnsureAbsent || s.ReclaimPolicy != cnpgv1.DatabaseReclaimDelete {
		t.Errorf("ensure/reclaim = %q/%q", s.Ensure, s.ReclaimPolicy)
	}
	if s.ConnectionLimit == nil || *s.ConnectionLimit != 0 || s.AllowConnections == nil || *s.AllowConnections {
		t.Errorf("connectionLimit/allowConnections = %v/%v, want the authored 0 and false", s.ConnectionLimit, s.AllowConnections)
	}
	if len(s.Extensions) != 1 || s.Extensions[0].Name != "pgvector" || len(s.Schemas) != 1 || s.Schemas[0].Owner != "app" {
		t.Errorf("extensions/schemas = %+v/%+v", s.Extensions, s.Schemas)
	}
}

// TestCnpgDatabaseHandler_Refusals: the fields the Database CRD requires must
// be authored and non-empty, the reserved names are refused, and the strict
// decode refuses what the type does not declare.
func TestCnpgDatabaseHandler_Refusals(t *testing.T) {
	with := func(k string, v any) map[string]any {
		p := minimalDatabase()
		p[k] = v
		return p
	}
	without := func(k string) map[string]any {
		p := minimalDatabase()
		delete(p, k)
		return p
	}
	for _, tt := range []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"no cluster", without("cluster"), "cluster.name: required (the name of the CloudNativePG Cluster this object belongs to)"},
		{"cluster name CloudNativePG refuses", with("cluster", map[string]any{"name": strings.Repeat("a", 51)}),
			`cluster.name "` + strings.Repeat("a", 51) + `": must be a DNS-1035 label of at most 50 characters, as a CloudNativePG Cluster name is`},
		{"no name", without("name"), "name: required (the name of the database inside PostgreSQL)"},
		{"null name", with("name", nil), "name: required (the name of the database inside PostgreSQL)"},
		{"reserved name postgres", with("name", "postgres"), `name "postgres": reserved by PostgreSQL, the Database CRD refuses it`},
		{"reserved name template0", with("name", "template0"), `name "template0": reserved by PostgreSQL, the Database CRD refuses it`},
		{"reserved name template1", with("name", "template1"), `name "template1": reserved by PostgreSQL, the Database CRD refuses it`},
		{"no owner", without("owner"), "owner: required (the role that owns the database)"},
		{"empty reclaim policy", with("databaseReclaimPolicy", ""),
			`databaseReclaimPolicy: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "retain")`},
		{"empty fdw option ensure", with("fdws", []any{map[string]any{"name": "f", "options": []any{map[string]any{"name": "o", "value": "v", "ensure": ""}}}}),
			`fdws[0].options[0].ensure: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "present")`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := cnpgDatabaseErr(t, tt.props); err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
	for _, tt := range []struct {
		name    string
		props   map[string]any
		wantSub string
	}{
		{"unknown top-level key", with("database", "app"), `unknown field "database"`},
		{"misspelt key in an array item", with("extensions", []any{map[string]any{"name": "x", "extVersion": "1"}}), `unknown field "extVersion"`},
		{"wrong scalar type", with("connectionLimit", "10"), "cannot unmarshal string"},
		{"two spellings of one field", with("Owner", "other"), "sets the same field as"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := cnpgDatabaseErr(t, tt.props); err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

// TestCnpgDatabaseConfig_Generate_WritesAlwaysEncodedDefaults: an unauthored
// ensure on a schema, extension, fdw or server is always encoded by the Go
// type, so Generate writes the CRD default (present) the API server would
// otherwise never apply; an authored value is kept, and the config itself is
// not changed.
func TestCnpgDatabaseConfig_Generate_WritesAlwaysEncodedDefaults(t *testing.T) {
	p := minimalDatabase()
	p["schemas"] = []any{map[string]any{"name": "s"}}
	p["extensions"] = []any{map[string]any{"name": "e"}, map[string]any{"name": "gone", "ensure": "absent"}}
	p["fdws"] = []any{map[string]any{"name": "f"}}
	p["servers"] = []any{map[string]any{"name": "v", "fdw": "f"}}
	c := newCnpgDatabase(t, p)
	s := generateCnpgDatabase(t, c).Spec
	for path, got := range map[string]cnpgv1.EnsureOption{
		"schemas[0]": s.Schemas[0].Ensure, "extensions[0]": s.Extensions[0].Ensure,
		"fdws[0]": s.FDWs[0].Ensure, "servers[0]": s.Servers[0].Ensure,
	} {
		if got != cnpgv1.EnsurePresent {
			t.Errorf("%s.ensure = %q, want the CRD default present", path, got)
		}
	}
	if s.Extensions[1].Ensure != cnpgv1.EnsureAbsent {
		t.Errorf("extensions[1].ensure = %q, want the authored absent", s.Extensions[1].Ensure)
	}
	if c.Spec.Schemas[0].Ensure != "" {
		t.Errorf("config schemas[0].ensure = %q; Generate must not change the config", c.Spec.Schemas[0].Ensure)
	}
}

// TestCnpgDatabaseHandler_RefusesEmptyEnsure: an authored ensure: "" on a
// schema, extension, fdw or server is refused by its path, whatever the key's
// case, rather than read as unset and written as present; the CRD's enum would
// refuse it as written. An explicit null is absence and takes the default.
func TestCnpgDatabaseHandler_RefusesEmptyEnsure(t *testing.T) {
	item := func(list, ensureKey string, ensure any) []any {
		ok, x := map[string]any{"name": "ok"}, map[string]any{"name": "x", ensureKey: ensure}
		if list == "servers" {
			ok["fdw"], x["fdw"] = "f", "f"
		}
		return []any{ok, x}
	}
	for _, tt := range []struct {
		list, key string
	}{
		{"schemas", "ensure"},
		{"extensions", "ensure"},
		{"fdws", "ensure"},
		{"servers", "ensure"},
		{"extensions", "Ensure"},
	} {
		t.Run(tt.list+"."+tt.key, func(t *testing.T) {
			p := minimalDatabase()
			p[tt.list] = item(tt.list, tt.key, "")
			want := tt.list + `[1].` + tt.key + `: "" is refused by the Database CRD, whose enum is present or absent; omit the field for the operator's default, present`
			if err := cnpgDatabaseErr(t, p); err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
	t.Run("null is absence", func(t *testing.T) {
		p := minimalDatabase()
		p["schemas"] = item("schemas", "ensure", nil)
		if got := generateCnpgDatabase(t, newCnpgDatabase(t, p)).Spec.Schemas[1].Ensure; got != cnpgv1.EnsurePresent {
			t.Errorf("schemas[1].ensure = %q, want the CRD default present", got)
		}
	})
}

// TestCnpgDatabaseConfig_GenerateRevalidates pins the emission-boundary repeat
// of the parse-time refusals, for a config built directly in Go.
func TestCnpgDatabaseConfig_GenerateRevalidates(t *testing.T) {
	c := &components.CnpgDatabaseConfig{Name: "db-app", Namespace: "data", Spec: cnpgv1.DatabaseSpec{
		ClusterRef: corev1.LocalObjectReference{Name: "db"}, Name: "template1", Owner: "app",
	}}
	_, err := c.Generate(stack.NewApplication("db-app", "data", c))
	if want := `name "template1": reserved by PostgreSQL, the Database CRD refuses it`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// TestCnpgDatabaseConfig_Generate_DoesNotAlias: the generated Database shares
// no slice or pointer with the config.
func TestCnpgDatabaseConfig_Generate_DoesNotAlias(t *testing.T) {
	p := minimalDatabase()
	p["isTemplate"] = true
	p["extensions"] = []any{map[string]any{"name": "pgvector"}}
	c := newCnpgDatabase(t, p)
	first := generateCnpgDatabase(t, c)
	*first.Spec.IsTemplate = false
	first.Spec.Extensions[0].Name = "edited"
	second := generateCnpgDatabase(t, c)
	if !*second.Spec.IsTemplate || second.Spec.Extensions[0].Name != "pgvector" {
		t.Errorf("second render changed after editing the first: %+v", second.Spec)
	}
}
