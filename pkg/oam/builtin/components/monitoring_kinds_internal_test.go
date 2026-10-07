package components

import (
	"encoding"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// monitoringModulePath is the module whose Go source the tests below read: the
// Prometheus operator's API types publish no field descriptions and the module
// ships no CRD, so the markers its CRDs are generated from are the source.
const monitoringModulePath = "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"

// monitoringKinds lists the kind components of that API with the spec type
// each decodes into and its required list. validated names the required
// fields the list cannot hold, because a parent of theirs is written whether
// or not it was authored; the kind's validate refuses those.
var monitoringKinds = []struct {
	component string
	typ       reflect.Type
	required  map[string]string
	validated []string
}{
	{"servicemonitor", reflect.TypeFor[monitoringv1.ServiceMonitorSpec](), serviceMonitorKind.required, nil},
	{"podmonitor", reflect.TypeFor[monitoringv1.PodMonitorSpec](), podMonitorKind.required, nil},
	{"prometheus-probe", reflect.TypeFor[monitoringv1.ProbeSpec](), prometheusProbeKind.required, []string{"prober.url"}},
	{"prometheusrule", reflect.TypeFor[monitoringv1.PrometheusRuleSpec](), prometheusRuleKind.required, nil},
}

// fieldMarkers is what the comment of one struct field says of it to the CRD
// generator.
type fieldMarkers struct {
	required, optional bool
	// def is the field's default, unquoted; hasDefault says there is one.
	def        string
	hasDefault bool
}

// fieldDefaultMarker is a default marker, as the CRD generator spells it
// (+kubebuilder:default) and as the Kubernetes API types do (+default).
var fieldDefaultMarker = regexp.MustCompile(`^\+(?:kubebuilder:)?default:?=(.*)$`)

// monitoringFieldMarkers reads the markers of every struct field the linked
// module's v1 package declares, keyed by "<type>.<Go field name>", an embedded
// field under its type's name.
func monitoringFieldMarkers(t *testing.T) map[string]fieldMarkers {
	t.Helper()
	return packageFieldMarkers(t, filepath.Join(linkedModuleDir(t, monitoringModulePath), "v1"))
}

// packageFieldMarkers reads the markers of every struct field the Go package
// in dir declares, keyed by "<type>.<Go field name>", an embedded field under
// its type's name.
func packageFieldMarkers(t *testing.T, dir string) map[string]fieldMarkers {
	t.Helper()
	out := map[string]fieldMarkers{}
	sourceStructs(t, dir, func(typ *ast.TypeSpec, _ *ast.CommentGroup, st *ast.StructType) {
		for _, field := range st.Fields.List {
			markers := markersOf(field.Doc)
			for _, fieldName := range sourceFieldNames(field) {
				out[typ.Name.Name+"."+fieldName] = markers
			}
		}
	})
	return out
}

// sourceFieldNames returns the Go names one field declaration declares: its
// identifiers, or the name of its type where it is embedded.
func sourceFieldNames(field *ast.Field) []string {
	names := make([]string, 0, len(field.Names))
	for _, ident := range field.Names {
		names = append(names, ident.Name)
	}
	if len(names) == 0 {
		names = append(names, embeddedTypeName(field.Type))
	}
	return names
}

// sourceStructs parses the Go files of the package in dir, its tests left out,
// and visits every struct type they declare, with the comment of its
// declaration.
func sourceStructs(t *testing.T, dir string, visit func(typ *ast.TypeSpec, doc *ast.CommentGroup, st *ast.StructType)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the package in %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				st, ok := typ.Type.(*ast.StructType)
				if !ok {
					continue
				}
				doc := typ.Doc
				if doc == nil {
					doc = gen.Doc
				}
				visit(typ, doc, st)
			}
		}
	}
}

// markersOf reads the markers of one field comment.
func markersOf(doc *ast.CommentGroup) fieldMarkers {
	var m fieldMarkers
	if doc == nil {
		return m
	}
	for _, comment := range doc.List {
		line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		switch {
		case line == "+required", line == "+kubebuilder:validation:Required", line == "+k8s:required":
			m.required = true
		case line == "+optional", line == "+kubebuilder:validation:Optional", line == "+k8s:optional":
			m.optional = true
		default:
			if def := fieldDefaultMarker.FindStringSubmatch(line); def != nil {
				m.def, m.hasDefault = strings.Trim(strings.TrimSpace(def[1]), `"`), true
			}
		}
	}
	return m
}

// embeddedTypeName is the Go field name of an embedded field: its type's name.
func embeddedTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return embeddedTypeName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// kindField is one field the encoding of a kind's spec type reaches.
type kindField struct {
	// owner is the struct that declares the field.
	owner reflect.Type
	field reflect.StructField
	// path is the field's json path, with [] for a list element and {} for a
	// map value.
	path string
	// forced says a struct above the field is written whether or not it was
	// authored and is not itself required, so the field is written under a
	// parent the author may have left out.
	forced bool
}

// markers returns the field's markers, and whether its source was read: only
// the monitoring package's is.
func (f kindField) markers(all map[string]fieldMarkers) (fieldMarkers, bool) {
	if f.owner.PkgPath() != reflect.TypeFor[monitoringv1.ServiceMonitorSpec]().PkgPath() {
		return fieldMarkers{}, false
	}
	m, ok := all[f.owner.Name()+"."+f.field.Name]
	return m, ok
}

// jsonOptions returns the options of the field's json tag.
func (f kindField) jsonOptions() []string {
	_, opts, _ := strings.Cut(f.field.Tag.Get("json"), ",")
	return strings.Split(opts, ",")
}

// writtenUnauthored says the type encodes the field when nothing was decoded
// into it: a struct that is no pointer, whatever omitempty says, or any field
// without omitempty.
func (f kindField) writtenUnauthored() bool {
	opts := f.jsonOptions()
	if slices.Contains(opts, "omitzero") {
		return false
	}
	return f.field.Type.Kind() == reflect.Struct || !slices.Contains(opts, "omitempty")
}

// omitemptyScalar says the field is a number or a boolean that is no pointer
// and is omitted when zero.
func (f kindField) omitemptyScalar() bool {
	switch f.field.Type.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return slices.Contains(f.jsonOptions(), "omitempty")
	default:
		return false
	}
}

// walkMonitoringFields is walkKindFields for a type of the Prometheus
// operator's API: a field is required where its source marks it so.
func walkMonitoringFields(typ reflect.Type, all map[string]fieldMarkers, visit func(kindField)) {
	walkKindFields(typ, func(f kindField) bool {
		m, _ := f.markers(all)
		return m.required
	}, visit)
}

// walkKindFields visits every field the encoding of typ reaches, as
// encoding/json reads it: exported fields by json name, embedded structs
// promoted, pointers, lists and maps descended, types with their own JSON or
// text encoding not. A type is walked once per branch. required says whether
// the API requires a field, which decides what is forced below it.
func walkKindFields(typ reflect.Type, required func(kindField) bool, visit func(kindField)) {
	marshaler := reflect.TypeFor[json.Marshaler]()
	textMarshaler := reflect.TypeFor[encoding.TextMarshaler]()
	var walk func(typ reflect.Type, path string, forced bool, branch []reflect.Type)
	walk = func(typ reflect.Type, path string, forced bool, branch []reflect.Type) {
		ptr := reflect.PointerTo(typ)
		if typ.Kind() != reflect.Struct || slices.Contains(branch, typ) || ptr.Implements(marshaler) || ptr.Implements(textMarshaler) {
			return
		}
		branch = append(branch, typ)
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				embedded := f.Type
				if embedded.Kind() == reflect.Pointer {
					embedded = embedded.Elem()
				}
				walk(embedded, path, forced, branch)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			field := kindField{owner: typ, field: f, path: name, forced: forced}
			if path != "" {
				field.path = path + "." + name
			}
			visit(field)

			// Below a struct that is no pointer the field's own presence
			// decides nothing: it is written either way. Below a pointer, a
			// list or a map, a value exists only where one was authored.
			child, childPath, childForced := f.Type, field.path, false
			if child.Kind() == reflect.Struct {
				childForced = forced || !required(field)
			}
		descend:
			for {
				switch child.Kind() {
				case reflect.Pointer:
					child = child.Elem()
				case reflect.Slice, reflect.Array:
					child, childPath = child.Elem(), childPath+"[]"
				case reflect.Map:
					child, childPath = child.Elem(), childPath+"{}"
				default:
					break descend
				}
			}
			walk(child, childPath, childForced, branch)
		}
	}
	walk(typ, "", false, nil)
}

// TestMonitoringKinds_NoDefaultedZeros is TestPolicyFreeKinds_NoDefaultedZeros
// for the kinds of the Prometheus operator's API, whose types publish no field
// description to read a default from: no field these kinds decode may be a
// number or a boolean that is omitted when zero and that the CRD defaults to
// something else, since none of these kinds sets a defaulted-zero list
// (policyFreeKind.defaultedZeros).
// The default is the field's kubebuilder marker, read from the linked module's
// source. A field of that shape on a type whose source is not read fails too.
//
// The API's two defaults at v0.94.1 are strings (a prober's path, a relabeling
// rule's action). An authored empty string there is omitted and defaulted, as
// on every kind: only an authored 0 or false is held to be carried.
func TestMonitoringKinds_NoDefaultedZeros(t *testing.T) {
	all := monitoringFieldMarkers(t)
	// Vacuity guards: the markers are read, and in both spellings of the
	// default marker.
	if m := all["ProberSpec.Path"]; m.def != "/probe" {
		t.Fatalf("ProberSpec.Path has the default %q (read: %v), want /probe; the source is not being read", m.def, m.hasDefault)
	}
	if m := all["RelabelConfig.Action"]; m.def != "replace" {
		t.Fatalf("RelabelConfig.Action has the default %q (read: %v), want replace; the source is not being read", m.def, m.hasDefault)
	}
	walked := map[string]bool{}
	for _, kind := range monitoringKinds {
		walkMonitoringFields(kind.typ, all, func(f kindField) {
			if !f.omitemptyScalar() {
				return
			}
			at := kind.typ.Name() + ": " + f.path
			walked[at] = true
			m, read := f.markers(all)
			switch {
			case !read:
				t.Errorf("%s (%s.%s) is omitted when zero, and its default cannot be read: the source of its type is not", at, f.owner, f.field.Name)
			case m.hasDefault && !crdDefaultIsZero(m.def):
				t.Errorf("%s is omitted when zero and defaults to %s: an authored zero would be replaced; the kind needs the field in its defaulted-zero list (policyFreeKind.defaultedZeros), which it does not set", at, m.def)
			}
		})
	}
	for _, at := range []string{
		"ServiceMonitorSpec: endpoints[].honorLabels",
		"ServiceMonitorSpec: namespaceSelector.any",
		"PodMonitorSpec: podMetricsEndpoints[].relabelings[].modulus",
		"ProbeSpec: targets.ingress.namespaceSelector.any",
	} {
		if !walked[at] {
			t.Errorf("the walk did not reach %s; it found %v", at, slices.Sorted(maps.Keys(walked)))
		}
	}
}

// TestMonitoringKinds_RequiredMatchMarkers derives, from the linked module's
// source, the fields of each kind that the API requires and the type would
// write unauthored, and holds the kind's required list to them. Such a field
// is marked +required and is encoded when nothing was decoded into it. One
// under a parent that is itself written unauthored and not required cannot be
// in the list, which follows what was authored; the kind's validate refuses
// it, and the row names it. A dependency bump that adds, drops or moves one
// fails here, naming it.
//
// Only the monitoring package's source is read. A field of a Kubernetes type
// these specs embed (the key of a Secret key selector, the key and operator of
// a label selector requirement) is not derived, and no kind refuses its
// omission.
func TestMonitoringKinds_RequiredMatchMarkers(t *testing.T) {
	all := monitoringFieldMarkers(t)
	for _, kind := range monitoringKinds {
		t.Run(kind.component, func(t *testing.T) {
			listed, validated := map[string]bool{}, map[string]bool{}
			fields := 0
			walkMonitoringFields(kind.typ, all, func(f kindField) {
				m, read := f.markers(all)
				if f.owner.PkgPath() != kind.typ.PkgPath() {
					return
				}
				fields++
				if !read {
					t.Errorf("%s (%s.%s) has no field in the module's source; the markers are keyed wrongly", f.path, f.owner, f.field.Name)
					return
				}
				if !f.writtenUnauthored() {
					return
				}
				if !m.required && !m.optional {
					t.Errorf("%s (%s.%s) is written unauthored and is marked neither required nor optional; classify it", f.path, f.owner, f.field.Name)
				}
				if !m.required {
					return
				}
				switch {
				case strings.Contains(f.path, "{}"):
					t.Errorf("%s is required under a map value, which a required list cannot name", f.path)
				case f.forced:
					validated[f.path] = true
				default:
					listed[f.path] = true
				}
			})
			if fields == 0 {
				t.Fatalf("the walk found no field of %s", kind.typ)
			}
			t.Logf("walked %d fields of the monitoring package; required and written unauthored: %v; of those under a parent written unauthored: %v",
				fields, slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(validated)))
			if got, want := slices.Sorted(maps.Keys(kind.required)), slices.Sorted(maps.Keys(listed)); !slices.Equal(got, want) {
				t.Errorf("required list = %v\nthe source marks %v", got, want)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
			if got, want := slices.Sorted(slices.Values(kind.validated)), slices.Sorted(maps.Keys(validated)); !slices.Equal(got, want) {
				t.Errorf("fields left to validate = %v\nthe source marks %v", got, want)
			}
		})
	}
}

// TestRefuseUnauthoredRequired: a listed field is refused where its parent is
// authored and it is not, under the path of the value that lacks it; a parent
// the author left out holds nothing to refuse, and a key matches as the decode
// matches it.
func TestRefuseUnauthoredRequired(t *testing.T) {
	required := map[string]string{
		"selector":                    "the query",
		"groups[].name":               "the group's name",
		"groups[].rules[].expr":       "the expression",
		"endpoints[].oauth2.clientId": "the client",
	}
	group := func(fields map[string]any) map[string]any {
		return map[string]any{"selector": map[string]any{}, "groups": []any{fields}}
	}
	for name, tc := range map[string]struct {
		authored map[string]any
		want     string
	}{
		"everything authored": {map[string]any{
			"selector":  map[string]any{},
			"groups":    []any{map[string]any{"name": "a", "rules": []any{map[string]any{"expr": 0}}}},
			"endpoints": []any{map[string]any{"oauth2": map[string]any{"clientId": map[string]any{}}}},
		}, ""},
		"no list, no element":    {map[string]any{"selector": map[string]any{}}, ""},
		"an empty list":          {map[string]any{"selector": map[string]any{}, "groups": []any{}, "endpoints": []any{}}, ""},
		"an omittable parent":    {map[string]any{"selector": map[string]any{}, "endpoints": []any{map[string]any{"port": "web"}}}, ""},
		"an authored empty":      {group(map[string]any{"name": ""}), ""},
		"another spelling":       {map[string]any{"Selector": map[string]any{}}, ""},
		"a list that is no list": {map[string]any{"selector": map[string]any{}, "groups": "none"}, ""},
		"top level":              {map[string]any{"groups": []any{}}, "selector: required (the query)"},
		"nothing authored":       {nil, "selector: required (the query)"},
		"in an element":          {group(map[string]any{"interval": "1m"}), "groups[0].name: required (the group's name)"},
		"in a later element": {map[string]any{"selector": map[string]any{}, "groups": []any{
			map[string]any{"name": "a"}, map[string]any{"name": "b"}, map[string]any{},
		}}, "groups[2].name: required (the group's name)"},
		"two lists deep": {group(map[string]any{"name": "a", "rules": []any{
			map[string]any{"expr": "up"}, map[string]any{"alert": "Down"},
		}}), "groups[0].rules[1].expr: required (the expression)"},
		"under an authored parent": {map[string]any{"selector": map[string]any{}, "endpoints": []any{
			map[string]any{"oauth2": map[string]any{"tokenUrl": "https://example.com"}},
		}}, "endpoints[0].oauth2.clientId: required (the client)"},
		"under a parent spelled otherwise": {map[string]any{"selector": map[string]any{}, "Endpoints": []any{
			map[string]any{"OAuth2": map[string]any{}},
		}}, "Endpoints[0].OAuth2.clientId: required (the client)"},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseUnauthoredRequired(tc.authored, required)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want none", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := refuseUnauthoredRequired(map[string]any{}, nil); err != nil {
		t.Errorf("no required list: err = %v, want none", err)
	}
}
