package components

import (
	"bytes"
	"encoding"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// barmanCloudModulePath is the module whose ObjectStore CRD and Go types
// cnpg-objectstore's list is derived from.
const barmanCloudModulePath = "github.com/cloudnative-pg/plugin-barman-cloud"

// TestCnpgKindsDefaultedZeroFields_MatchCRD is
// TestCnpgClusterDefaultedZeroFields_MatchCRD for the Pooler, Database and
// ObjectStore kinds: each kind's list must equal every non-pointer omitempty
// scalar of its spec type (by json path) whose CRD default is not zero or
// false, with that default, read from the linked module. A dependency bump
// that adds, removes or changes one fails here, naming it.
func TestCnpgKindsDefaultedZeroFields_MatchCRD(t *testing.T) {
	cnpgDir := linkedModuleDir(t, cnpgModulePath)
	barmanDir := linkedModuleDir(t, barmanCloudModulePath)
	for _, tt := range []struct {
		kind string
		crd  string
		typ  reflect.Type
		list map[string]string
		// minimum counts, the vacuity guards: a walk that stops early finds
		// nothing to intersect and would pass against an empty list. The
		// Database row has none of its own (DatabaseSpec carries only pointer
		// scalars and its CRD no scalar default, both 0 at v1.30.1); the
		// Pooler and ObjectStore rows prove the same two walks reach depth.
		minDefaults, minOmitted int
	}{
		{"Pooler", filepath.Join(cnpgDir, "config", "crd", "bases", "postgresql.cnpg.io_poolers.yaml"),
			reflect.TypeFor[cnpgv1.PoolerSpec](), cnpgPoolerDefaultedZeroFields, 5, 50},
		{"Database", filepath.Join(cnpgDir, "config", "crd", "bases", "postgresql.cnpg.io_databases.yaml"),
			reflect.TypeFor[cnpgv1.DatabaseSpec](), cnpgDatabaseDefaultedZeroFields, 0, 0},
		{"ObjectStore", filepath.Join(barmanDir, "config", "crd", "bases", "barmancloud.cnpg.io_objectstores.yaml"),
			reflect.TypeFor[barmanv1.ObjectStoreSpec](), cnpgObjectStoreDefaultedZeroFields, 1, 5},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			defaults := crdSpecScalarDefaults(t, tt.crd, "integer", "number", "boolean")
			omitted := omitemptyScalarPaths(tt.typ)
			if len(defaults) < tt.minDefaults {
				t.Fatalf("found %d scalar defaults under the %s CRD's spec, want >= %d; the CRD walk is broken", len(defaults), tt.kind, tt.minDefaults)
			}
			if len(omitted) < tt.minOmitted {
				t.Fatalf("found %d omitempty scalar fields under %s, want >= %d; the reflection walk is broken", len(omitted), tt.typ, tt.minOmitted)
			}
			want := map[string]string{}
			for path, def := range defaults {
				if omitted[path] && !crdDefaultIsZero(def) {
					want[path] = def
				}
			}
			for _, path := range slices.Sorted(maps.Keys(want)) {
				got, ok := tt.list[path]
				switch {
				case !ok:
					t.Errorf("missing %s (CRD default %s): a non-pointer omitempty field with a non-zero default", path, want[path])
				case got != want[path]:
					t.Errorf("%s records default %s, the CRD says %s", path, got, want[path])
				}
			}
			for _, path := range slices.Sorted(maps.Keys(tt.list)) {
				if _, ok := want[path]; !ok {
					t.Errorf("stale %s: not a non-pointer omitempty field with a non-zero CRD default", path)
				}
			}
		})
	}
}

// linkedModuleDir is cnpgModuleDir for any module: the directory of the
// version the test binary links, read from the module cache with GOPROXY=off.
func linkedModuleDir(t *testing.T, modulePath string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", modulePath)
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m %s: %v: %s", modulePath, err, stderr.String())
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("go list -m %s reported no directory; the module is not in the module cache", modulePath)
	}
	return dir
}

// crdSpecScalarDefaults is clusterCRDScalarDefaults for any single-version
// CRD file: the default of every property under spec whose schema type is one
// of types, keyed by json path with [] for an array element. A string default
// keeps its JSON quotes.
func crdSpecScalarDefaults(t *testing.T, file string, types ...string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Name != "v1" || crd.Spec.Versions[0].Schema == nil {
		t.Fatalf("%s does not serve exactly one schema-bearing v1 version; update this test", file)
	}
	spec, ok := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property", file)
	}
	return schemaScalarDefaults(spec, types...)
}

// schemaScalarDefaults returns the default of every property under spec, the
// schema of a CRD's spec, whose schema type is one of types, keyed as
// crdSpecScalarDefaults says.
func schemaScalarDefaults(spec apiextensionsv1.JSONSchemaProps, types ...string) map[string]string {
	out := map[string]string{}
	var walk func(s apiextensionsv1.JSONSchemaProps, path string)
	walk = func(s apiextensionsv1.JSONSchemaProps, path string) {
		if s.Default != nil && slices.Contains(types, s.Type) {
			out[path] = strings.TrimSpace(string(s.Default.Raw))
		}
		for name, prop := range s.Properties {
			child := name
			if path != "" {
				child = path + "." + name
			}
			walk(prop, child)
		}
		if s.Items != nil && s.Items.Schema != nil {
			walk(*s.Items.Schema, path+"[]")
		}
	}
	walk(spec, "")
	return out
}

// TestCnpgKindsAlwaysEncodedDefaults_MatchCRD is the converse of the
// defaulted-zero check: a non-pointer scalar the spec type encodes even when
// empty (no omitempty) reaches the API server as its zero value and is never
// defaulted, so where the CRD gives it a default the kind must write that
// default itself. Each kind's list must equal that set, with the CRD default,
// read from the linked module. Only the Database has such fields; the
// Cluster's one (instances) is cnpg-cluster's schema default.
func TestCnpgKindsAlwaysEncodedDefaults_MatchCRD(t *testing.T) {
	cnpgDir := linkedModuleDir(t, cnpgModulePath)
	barmanDir := linkedModuleDir(t, barmanCloudModulePath)
	for _, tt := range []struct {
		kind string
		crd  string
		typ  reflect.Type
		list map[string]string
	}{
		{"Pooler", filepath.Join(cnpgDir, "config", "crd", "bases", "postgresql.cnpg.io_poolers.yaml"),
			reflect.TypeFor[cnpgv1.PoolerSpec](), map[string]string{}},
		{"Database", filepath.Join(cnpgDir, "config", "crd", "bases", "postgresql.cnpg.io_databases.yaml"),
			reflect.TypeFor[cnpgv1.DatabaseSpec](), cnpgDatabaseAlwaysEncodedDefaults},
		{"ObjectStore", filepath.Join(barmanDir, "config", "crd", "bases", "barmancloud.cnpg.io_objectstores.yaml"),
			reflect.TypeFor[barmanv1.ObjectStoreSpec](), map[string]string{}},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			defaults := crdSpecScalarDefaults(t, tt.crd, "integer", "number", "boolean", "string")
			encoded := alwaysEncodedScalarPaths(tt.typ)
			if len(encoded) == 0 {
				t.Fatalf("found no always-encoded scalar field under %s; the reflection walk is broken", tt.typ)
			}
			want := map[string]string{}
			for path, def := range defaults {
				if encoded[path] {
					want[path] = strings.Trim(def, `"`)
				}
			}
			if !maps.Equal(tt.list, want) {
				t.Errorf("list = %v, want %v (always-encoded fields with a CRD default)", tt.list, want)
			}
		})
	}
}

// alwaysEncodedScalarPaths is omitemptyScalarPaths' converse: the json path
// of every non-pointer bool, integer, float or string field encoded without
// omitempty under typ, walked the same way.
func alwaysEncodedScalarPaths(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	marshaler := reflect.TypeFor[json.Marshaler]()
	textMarshaler := reflect.TypeFor[encoding.TextMarshaler]()
	var walk func(typ reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(typ reflect.Type, path string, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			if typ.Kind() != reflect.Pointer {
				path += "[]"
			}
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		ptr := reflect.PointerTo(typ)
		if ptr.Implements(marshaler) || ptr.Implements(textMarshaler) {
			return
		}
		seen[typ] = true
		defer delete(seen, typ)
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" && opts == "" {
				continue
			}
			if f.Anonymous && name == "" {
				walk(f.Type, path, seen)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			switch f.Type.Kind() {
			case reflect.Bool, reflect.String,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64:
				if !slices.Contains(strings.Split(opts, ","), "omitempty") {
					out[child] = true
				}
			default:
				walk(f.Type, child, seen)
			}
		}
	}
	walk(typ, "", map[reflect.Type]bool{})
	return out
}
