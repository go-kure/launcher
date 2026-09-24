package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// postgresqlStringMapSites lists the string→string maps the postgresql handler reads
// from authored properties (go-kure/launcher#466). Each of them used to go through
// stringMap, which dropped a non-string value without a word: an author writing
// `max_connections: 100` — the ordinary unquoted YAML form, which gopkg.in/yaml.v3
// decodes to an int — got a cluster with no max_connections at all, from a document
// that validated (the handler's schema leaves these maps open). One entry per site, so
// reverting any single conversion fails exactly its own subtest.
var postgresqlStringMapSites = []struct {
	name  string
	props func(v any) map[string]any
	key   string // the key whose value varies
	label string // the field the error must name
	get   func(*components.PostgresqlConfig) map[string]string
}{
	{
		name: "pooler.parameters",
		props: func(v any) map[string]any {
			return map[string]any{"pooler": map[string]any{
				"enabled":    true,
				"parameters": map[string]any{"default_pool_size": "25", "max_client_conn": v},
			}}
		},
		key:   "max_client_conn",
		label: `pooler.parameters["max_client_conn"]`,
		get:   func(c *components.PostgresqlConfig) map[string]string { return c.PoolerParameters },
	},
	{
		name: "externalClusters[].connectionParameters",
		props: func(v any) map[string]any {
			return map[string]any{"externalClusters": []any{
				map[string]any{"name": "first", "connectionParameters": map[string]any{"host": "a.example.com"}},
				map[string]any{"name": "second", "connectionParameters": map[string]any{"host": "b.example.com", "port": v}},
			}}
		},
		key:   "port",
		label: `externalClusters[1].connectionParameters["port"]`,
		get: func(c *components.PostgresqlConfig) map[string]string {
			if len(c.ExternalClusters) != 2 {
				return nil
			}
			return c.ExternalClusters[1].ConnectionParameters
		},
	},
	{
		name: "postgresql.parameters",
		props: func(v any) map[string]any {
			return map[string]any{"postgresql": map[string]any{
				"parameters": map[string]any{"shared_buffers": "256MB", "max_connections": v},
			}}
		},
		key:   "max_connections",
		label: `postgresql.parameters["max_connections"]`,
		get:   func(c *components.PostgresqlConfig) map[string]string { return c.PostgresqlParameters },
	},
	{
		name: "inheritedMetadata.labels",
		props: func(v any) map[string]any {
			return map[string]any{"inheritedMetadata": map[string]any{
				"labels": map[string]any{"team": "data", "tier": v},
			}}
		},
		key:   "tier",
		label: `inheritedMetadata.labels["tier"]`,
		get:   func(c *components.PostgresqlConfig) map[string]string { return c.InheritedLabels },
	},
	{
		name: "inheritedMetadata.annotations",
		props: func(v any) map[string]any {
			return map[string]any{"inheritedMetadata": map[string]any{
				"annotations": map[string]any{"owner": "data", "revision": v},
			}}
		},
		key:   "revision",
		label: `inheritedMetadata.annotations["revision"]`,
		get:   func(c *components.PostgresqlConfig) map[string]string { return c.InheritedAnnotations },
	},
}

// TestPostgresqlStringMaps_NonStringValueIsRejected: a non-string value in any of the
// maps above is refused by field and key instead of being dropped from the output.
// The values are the shapes an author actually produces — an unquoted integer
// (yaml.v3: int), a JSON number (float64) and an unquoted boolean.
func TestPostgresqlStringMaps_NonStringValueIsRejected(t *testing.T) {
	for _, site := range postgresqlStringMapSites {
		for _, v := range []any{100, float64(100), true} {
			t.Run(fmt.Sprintf("%s/%T", site.name, v), func(t *testing.T) {
				_, err := postgresqlConfigFor(t, site.props(v))
				if err == nil {
					t.Fatalf("%s: a %T value must be rejected; it was silently dropped from the output", site.label, v)
				}
				if !strings.Contains(err.Error(), site.label) {
					t.Errorf("error = %q, want it to name %s", err, site.label)
				}
			})
		}
	}
}

// TestPostgresqlStringMaps_StringValuesRoundTrip is the control for the test above: a
// reader that refused every multi-key map, or every map at all, would pass it. An
// all-string map must still reach the config with every pair intact.
func TestPostgresqlStringMaps_StringValuesRoundTrip(t *testing.T) {
	for _, site := range postgresqlStringMapSites {
		t.Run(site.name, func(t *testing.T) {
			cfg, err := postgresqlConfigFor(t, site.props("7"))
			if err != nil {
				t.Fatalf("an all-string map must parse, got: %v", err)
			}
			got := site.get(cfg)
			if len(got) != 2 {
				t.Fatalf("%s: got %#v, want both authored pairs", site.name, got)
			}
			if got[site.key] != "7" {
				t.Errorf("%s[%q] = %q, want %q", site.name, site.key, got[site.key], "7")
			}
		})
	}
}

// TestPostgresqlStringMaps_NullValueIsAbsent pins the package's null contract (README
// "The null contract") for these maps: a key authored as `max_connections:` with no
// value is absent, not a wrong-typed value. The lenient reader skipped a null along
// with every other non-string; the strict one must keep skipping it while refusing
// the rest, or a document that built correctly becomes an error.
func TestPostgresqlStringMaps_NullValueIsAbsent(t *testing.T) {
	for _, site := range postgresqlStringMapSites {
		t.Run(site.name, func(t *testing.T) {
			cfg, err := postgresqlConfigFor(t, site.props(nil))
			if err != nil {
				t.Fatalf("a null value must read as absent, got error: %v", err)
			}
			got := site.get(cfg)
			if _, present := got[site.key]; present {
				t.Errorf("%s[%q] is present (%q); a null value must leave the key out", site.name, site.key, got[site.key])
			}
			if len(got) != 1 {
				t.Errorf("%s: got %#v, want only the non-null pair", site.name, got)
			}
		})
	}
}
