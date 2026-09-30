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
	"strconv"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// cnpgModulePath is the module whose Cluster CRD and Go types the set is
// derived from; the test reads the copy the build links, never the network.
const cnpgModulePath = "github.com/cloudnative-pg/cloudnative-pg"

// TestCnpgClusterDefaultedZeroFields_MatchCRD derives the fields an authored
// zero would silently change from the linked CloudNativePG module and requires
// cnpgClusterDefaultedZeroFields to equal them: every non-pointer omitempty
// scalar of cnpgv1.ClusterSpec (by json path) whose Cluster CRD default is not
// zero or false, with that default. A CNPG bump that adds, removes or changes
// one fails here, naming it.
func TestCnpgClusterDefaultedZeroFields_MatchCRD(t *testing.T) {
	defaults := clusterCRDScalarDefaults(t, cnpgModuleDir(t))
	omitted := omitemptyScalarPaths(reflect.TypeFor[cnpgv1.ClusterSpec]())

	// Vacuity guards: a walker that stops early finds nothing to intersect and
	// would pass against an empty set. The v1.30.1 counts are 40 and 64.
	if len(defaults) < 20 {
		t.Fatalf("found %d scalar defaults under the Cluster CRD's spec, want >= 20; the CRD walk is broken", len(defaults))
	}
	if len(omitted) < 20 {
		t.Fatalf("found %d omitempty scalar fields under ClusterSpec, want >= 20; the reflection walk is broken", len(omitted))
	}

	want := map[string]string{}
	for path, def := range defaults {
		if omitted[path] && !crdDefaultIsZero(def) {
			want[path] = def
		}
	}
	for _, path := range slices.Sorted(maps.Keys(want)) {
		got, ok := cnpgClusterDefaultedZeroFields[path]
		switch {
		case !ok:
			t.Errorf("missing %s (CRD default %s): a non-pointer omitempty field with a non-zero default", path, want[path])
		case got != want[path]:
			t.Errorf("%s records default %s, the CRD says %s", path, got, want[path])
		}
	}
	for _, path := range slices.Sorted(maps.Keys(cnpgClusterDefaultedZeroFields)) {
		if _, ok := want[path]; !ok {
			t.Errorf("stale %s: not a non-pointer omitempty field with a non-zero CRD default in %s", path, cnpgModulePath)
		}
	}
}

// cnpgModuleDir returns the linked CloudNativePG module's directory. It asks
// the go command with the test's own environment (GOWORK, GOFLAGS), so it
// resolves the version the test binary was built against, and with GOPROXY=off,
// so it reads the module cache the build already filled and never downloads.
// Not finding it fails the test: skipping would pass without checking anything.
func cnpgModuleDir(t *testing.T) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", cnpgModulePath)
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m %s: %v: %s", cnpgModulePath, err, stderr.String())
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("go list -m %s reported no directory; the module is not in the module cache", cnpgModulePath)
	}
	return dir
}

// clusterCRDScalarDefaults returns the default of every integer, number and
// boolean property under the Cluster CRD's spec, keyed by json path with []
// for an array element, as its JSON literal (26, -1, true, false).
func clusterCRDScalarDefaults(t *testing.T, moduleDir string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(moduleDir, "config", "crd", "bases", "postgresql.cnpg.io_clusters.yaml"))
	if err != nil {
		t.Fatalf("read the Cluster CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the Cluster CRD: %v", err)
	}
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Name != "v1" || crd.Spec.Versions[0].Schema == nil {
		t.Fatalf("the Cluster CRD does not serve exactly one schema-bearing v1 version; update this test")
	}
	spec, ok := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatal("the Cluster CRD schema has no spec property")
	}
	out := map[string]string{}
	var walk func(s apiextensionsv1.JSONSchemaProps, path string)
	walk = func(s apiextensionsv1.JSONSchemaProps, path string) {
		switch s.Type {
		case "integer", "number", "boolean":
			if s.Default != nil {
				out[path] = strings.TrimSpace(string(s.Default.Raw))
			}
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

// crdDefaultIsZero reports whether a CRD default literal is the value omitempty
// drops: false or a numeric zero.
func crdDefaultIsZero(def string) bool {
	if def == "false" {
		return true
	}
	f, err := strconv.ParseFloat(def, 64)
	return err == nil && f == 0
}

// omitemptyScalarPaths returns the json path of every non-pointer bool,
// integer or float field tagged omitempty under typ, following what
// encoding/json encodes: exported fields by json name, embedded structs
// promoted, pointers and slices (as []) descended, maps and types with their
// own JSON or text encoding not descended.
func omitemptyScalarPaths(typ reflect.Type) map[string]bool {
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
			case reflect.Bool,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64:
				if slices.Contains(strings.Split(opts, ","), "omitempty") {
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
