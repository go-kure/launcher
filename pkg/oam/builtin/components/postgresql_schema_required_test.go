package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The postgresql schema says what the parser refuses (go-kure/launcher#1711): a
// property the parser refuses when it is absent is Required in the published
// schema, and a set of keys the parser refuses together or all absent is an
// exclusive group. A consumer that builds an input form reads these; the
// descriptions already said so.

// postgresqlRequiredUnflagged are the properties whose description starts
// "Required" and that the schema deliberately does not mark Required, each with
// its reason. The document check reports a missing Required key before an
// undeclared one, so marking these would replace the hint that names a key's
// replacement (backup.destinationPath: removed; use
// backup.objectStore.destinationPath) or an unsupported credential's reason
// with a plain missing-key refusal (the comment above objectStore in
// schemaPostgresqlBackup). The parser still refuses each when it is absent.
var postgresqlRequiredUnflagged = map[string]string{
	"backup.objectStore":                          "moved-key hints of backup come before a missing objectStore",
	"backup.objectStore.destinationPath":          "an object store of Azure or Google credentials is refused with its reason first",
	"backup.objectStore.s3Credentials":            "an object store of Azure or Google credentials is refused with its reason first",
	"recovery.source.objectStore.destinationPath": "the same object store schema as backup.objectStore",
	"recovery.source.objectStore.s3Credentials":   "the same object store schema as backup.objectStore",
}

// walkSchema calls visit for every node of the schema, with its path: a key is
// joined by ".", an array's items by "[]", the values of an open object by "{}".
func walkSchema(path string, node oam.PropertySchema, visit func(path string, node oam.PropertySchema)) {
	visit(path, node)
	for key, child := range node.Properties {
		walkSchema(joinSchemaPath(path, key), child, visit)
	}
	if node.Items != nil {
		walkSchema(path+"[]", *node.Items, visit)
	}
	if node.AdditionalPropertiesSchema != nil {
		walkSchema(path+"{}", *node.AdditionalPropertiesSchema, visit)
	}
}

func joinSchemaPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func TestPostgresqlSchema_RequiredDescriptionsAreDeclared(t *testing.T) {
	visited, described := 0, 0
	unflagged := map[string]bool{}
	for key, node := range (components.PostgresqlRule{}).PropertySchema() {
		walkSchema(key, node, func(path string, n oam.PropertySchema) {
			visited++
			if !strings.HasPrefix(n.Description, "Required") {
				return
			}
			described++
			if n.Required || n.RequiredWhen != nil {
				return
			}
			unflagged[path] = true
			if _, ok := postgresqlRequiredUnflagged[path]; !ok {
				t.Errorf("%s: the description starts %q but the schema does not mark it Required; mark it, or name it in postgresqlRequiredUnflagged with the reason", path, "Required")
			}
		})
	}
	// An empty walk would pass every check above.
	if visited < 50 || described < 10 {
		t.Fatalf("the walk read %d nodes and %d described as Required; the schema has more", visited, described)
	}
	for path, reason := range postgresqlRequiredUnflagged {
		if !unflagged[path] {
			t.Errorf("%s is exempt (%s) but is no longer an unflagged \"Required\" description; drop the exemption", path, reason)
		}
	}
}

// postgresqlDocumentCheck is what kurel build runs on an authored document first.
func postgresqlDocumentCheck(props map[string]any) error {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{}, nil)
	tr.RegisterComponentLowering(components.PostgresqlRule{})
	_, err := tr.ValidateAuthoredProperties(&oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{{Name: "db", Type: "postgresql", Properties: props}}},
	})
	return err
}

// The groups the schema publishes are the ones the parser enforces, and a key is
// set for both when it is present and not null (authoredValue, isNullValue).
func TestPostgresqlSchema_RequiredAndExclusiveGroups(t *testing.T) {
	store := map[string]any{"destinationPath": "s3://bucket/pg", "s3Credentials": map[string]any{"inheritFromIAMRole": true}}
	source := func(extra map[string]any) map[string]any {
		s := map[string]any{"serverName": "origin"}
		for k, v := range extra {
			s[k] = v
		}
		return map[string]any{"recovery": map[string]any{"source": s}}
	}
	target := func(extra map[string]any) map[string]any {
		return map[string]any{"recovery": map[string]any{
			"source": map[string]any{"serverName": "origin", "objectStoreName": "origin"},
			"target": extra,
		}}
	}
	bootstrap := func(b map[string]any) map[string]any { return map[string]any{"bootstrap": b} }
	initdb := map[string]any{"database": "app", "owner": "app"}
	basebackup := map[string]any{"source": "origin"}

	for _, c := range []struct {
		name  string
		props map[string]any
		want  string // the document check's refusal; "" when it accepts
	}{
		{"recovery without a source", map[string]any{"recovery": map[string]any{}}, `"source" is required`},
		{"a source with neither store", source(nil), `exactly one of "objectStoreName" and "objectStore" is required`},
		{"a source with both stores", source(map[string]any{"objectStoreName": "origin", "objectStore": store}), `"objectStoreName" and "objectStore" are mutually exclusive`},
		{"a source with a store by name", source(map[string]any{"objectStoreName": "origin"}), ""},
		{"a source with an inline store", source(map[string]any{"objectStore": store}), ""},
		{"a null store beside the named one", source(map[string]any{"objectStoreName": "origin", "objectStore": nil}), ""},

		{"two targets", target(map[string]any{"time": "2026-10-20T03:00:00Z", "lsn": "0/3000060"}), `"time" and "lsn" are mutually exclusive`},
		{"immediate false is a target", target(map[string]any{"lsn": "0/3000060", "immediate": false}), `"lsn" and "immediate" are mutually exclusive`},
		{"xid with name", target(map[string]any{"xid": "100", "name": "p", "backupID": "b"}), `"xid" and "name" are mutually exclusive`},
		{"one target", target(map[string]any{"time": "2026-10-20T03:00:00Z"}), ""},
		{"a target and a backupID", target(map[string]any{"lsn": "0/3000060", "backupID": "b"}), ""},
		{"a null target beside a set one", target(map[string]any{"time": nil, "lsn": "0/3000060"}), ""},

		{"initdb with pg_basebackup", bootstrap(map[string]any{"initdb": initdb, "pg_basebackup": basebackup}), `"initdb" and "pg_basebackup" are mutually exclusive`},
		{"initdb alone", bootstrap(map[string]any{"initdb": initdb}), ""},
		{"pg_basebackup alone", bootstrap(map[string]any{"pg_basebackup": basebackup}), ""},
		{"a null pg_basebackup beside initdb", bootstrap(map[string]any{"initdb": initdb, "pg_basebackup": nil}), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			docErr := postgresqlDocumentCheck(c.props)
			if c.want == "" {
				if docErr != nil {
					t.Fatalf("the document check refused an accepted document: %v", docErr)
				}
			} else {
				wantErrContaining(t, docErr, c.want)
			}
			// The parser refuses exactly what the schema now refuses.
			_, parseErr := postgresqlConfigFor(t, c.props)
			if (docErr == nil) != (parseErr == nil) {
				t.Errorf("the document check says %v and the parser says %v; the schema and the parser disagree", docErr, parseErr)
			}
		})
	}
}

// The groups are declared on the schema itself, so a consumer reads them.
func TestPostgresqlSchema_DeclaresTheGroups(t *testing.T) {
	schema := (components.PostgresqlRule{}).PropertySchema()
	recovery := schema["recovery"].Properties
	for _, c := range []struct {
		name     string
		node     oam.PropertySchema
		keys     []string
		required bool
	}{
		{"recovery.source", recovery["source"], []string{"objectStoreName", "objectStore"}, true},
		{"recovery.target", recovery["target"], []string{"time", "lsn", "xid", "name", "immediate"}, false},
		{"bootstrap", schema["bootstrap"], []string{"initdb", "pg_basebackup"}, false},
	} {
		found := false
		for _, g := range c.node.Exclusive {
			if slices.Equal(g.Keys, c.keys) {
				found = true
				if g.Required != c.required {
					t.Errorf("%s: group %v has Required %v, want %v", c.name, g.Keys, g.Required, c.required)
				}
			}
		}
		if !found {
			t.Errorf("%s declares no exclusive group of %v", c.name, c.keys)
		}
	}
	if !recovery["source"].Required {
		t.Error("recovery.source is not Required, and the parser refuses a recovery without it")
	}
}
