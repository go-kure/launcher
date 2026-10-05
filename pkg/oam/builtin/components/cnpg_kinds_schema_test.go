package components_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// cnpgKindSchemas lists the cnpg-pooler, cnpg-database and cnpg-objectstore
// schemas with the upstream spec type each projects. Every field of each
// linked spec type is authorable, so none has an exclusion list.
var cnpgKindSchemas = []struct {
	component string
	typ       reflect.Type
	handler   interface {
		PropertySchema() map[string]oam.PropertySchema
	}
}{
	{"cnpg-pooler", reflect.TypeFor[cnpgv1.PoolerSpec](), &components.CnpgPoolerHandler{}},
	{"cnpg-database", reflect.TypeFor[cnpgv1.DatabaseSpec](), &components.CnpgDatabaseHandler{}},
	{"cnpg-objectstore", reflect.TypeFor[barmanv1.ObjectStoreSpec](), &components.CnpgObjectStoreHandler{}},
}

// specJSONFields is clusterSpecJSONFields for any spec type. It also walks an
// embedded struct that carries no json name (PersistentVolumeSpec's volume
// source), whose fields encoding/json promotes to the embedding object. An
// embedded struct that does carry a json name (PodTemplate's metadata) is one
// field under that name, as encoding/json reads it. Of two fields under one
// name the one embedded less deeply hides the other, as encoding/json resolves
// it (ProbeSpec's own authorization over the one its HTTP settings embed); two
// at one depth fail the test rather than being resolved here, and so does any
// other embedding.
func specJSONFields(t *testing.T, typ reflect.Type) map[string]reflect.Type {
	t.Helper()
	fields := make(map[string]reflect.Type, typ.NumField())
	collectSpecJSONFields(t, typ, 0, fields, map[string]int{})
	if len(fields) == 0 {
		t.Fatalf("found no json fields on %s; the reflection walk is broken", typ)
	}
	return fields
}

// collectSpecJSONFields adds the json fields of typ, embedded depth levels
// deep, to fields; depths holds the depth each name was found at.
func collectSpecJSONFields(t *testing.T, typ reflect.Type, depth int, fields map[string]reflect.Type, depths map[string]int) {
	t.Helper()
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			if f.Type.Kind() != reflect.Struct || !f.IsExported() {
				t.Fatalf("%s embeds %s other than as an exported struct; walk it before trusting this coverage test", typ, f.Name)
			}
			collectSpecJSONFields(t, f.Type, depth+1, fields, depths)
			continue
		}
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if at, dup := depths[name]; dup {
			switch {
			case at == depth:
				t.Fatalf("%s reaches two fields named %q at one depth; resolve the promotion before trusting this coverage test", typ, name)
			case at < depth:
				continue
			}
		}
		fields[name], depths[name] = f.Type, depth
	}
}

// TestCnpgKindSchemas_CoverSpec is TestCnpgClusterSchema_CoversClusterSpec for
// the Pooler, Database and ObjectStore kinds: the schema publishes exactly the
// linked spec type's json fields, each with the type its Go field decodes
// from. A dependency bump that adds, removes or retypes a top-level field
// fails here, naming it.
func TestCnpgKindSchemas_CoverSpec(t *testing.T) {
	for _, tt := range cnpgKindSchemas {
		t.Run(tt.component, func(t *testing.T) {
			fields := specJSONFields(t, tt.typ)
			schema := tt.handler.PropertySchema()
			for _, name := range slices.Sorted(maps.Keys(fields)) {
				prop, ok := schema[name]
				if !ok {
					t.Errorf("%s field %q is not published in the %s schema", tt.typ, name, tt.component)
					continue
				}
				if want := schemaTypeForGo(fields[name]); prop.Type != want {
					t.Errorf("schema key %q declares type %q, but %s's field is %s (want %q)", name, prop.Type, tt.typ, fields[name], want)
				}
				if prop.Type == oam.PropertyTypeObject && !prop.AdditionalProperties {
					t.Errorf("schema key %q is a closed object; structured fields are open objects checked by the strict decode", name)
				}
				if prop.Type == oam.PropertyTypeArray && (prop.Items == nil || prop.Items.Type != oam.PropertyTypeObject || !prop.Items.AdditionalProperties) {
					t.Errorf("schema key %q must be an array of open objects", name)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(schema)) {
				if _, ok := fields[key]; !ok {
					t.Errorf("schema key %q has no %s json field; the strict decode would refuse every value", key, tt.typ)
				}
			}
		})
	}
}

// TestCnpgKindSchemas_EveryFieldReachable is
// TestCnpgClusterSchema_EveryFieldReachable for the three kinds.
func TestCnpgKindSchemas_EveryFieldReachable(t *testing.T) {
	for _, tt := range cnpgKindSchemas {
		if got := builtin.UnreachableJSONFields(tt.typ); len(got) != 0 {
			t.Errorf("%s fields unreachable through the strict decode: %v", tt.typ, got)
		}
	}
}
