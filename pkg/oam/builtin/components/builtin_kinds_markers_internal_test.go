package components

import (
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

// builtinMarkerSources maps each Go package whose types the kinds below reach
// to the linked module and the directory in it that holds the package's
// source. The API types of Kubernetes publish their field comments, but not
// whether a field is required: that is the +required and +optional markers of
// the source, which the generated OpenAPI is built from.
var builtinMarkerSources = map[string][2]string{
	"k8s.io/api/discovery/v1":              {"k8s.io/api", "discovery/v1"},
	"k8s.io/api/rbac/v1":                   {"k8s.io/api", "rbac/v1"},
	"k8s.io/api/core/v1":                   {"k8s.io/api", "core/v1"},
	"k8s.io/apimachinery/pkg/apis/meta/v1": {"k8s.io/apimachinery", "pkg/apis/meta/v1"},
}

// builtinMarkerKinds lists the whole-object kind components of the Kubernetes
// API whose required lists are derived from those markers, with the type each
// decodes into. emittedEmpty names the fields the type writes when they are
// not authored and the API does not require: the object carries them empty,
// and the kind's README entry says so.
var builtinMarkerKinds = []struct {
	component    string
	typ          reflect.Type
	required     map[string]string
	emittedEmpty []string
}{
	{"endpointslice", reflect.TypeFor[discoveryv1.EndpointSlice](), endpointSliceKind.required, []string{"endpoints", "endpoints[].conditions", "ports"}},
	{"role", reflect.TypeFor[rbacv1.Role](), roleKind.required, []string{"rules"}},
	{"rolebinding", reflect.TypeFor[rbacv1.RoleBinding](), roleBindingKind.required, []string{"roleRef.apiGroup"}},
	{"clusterrole", reflect.TypeFor[rbacv1.ClusterRole](), clusterRoleKind.required, []string{"rules"}},
	{"clusterrolebinding", reflect.TypeFor[rbacv1.ClusterRoleBinding](), clusterRoleBindingKind.required, []string{"roleRef.apiGroup"}},
}

// builtinFieldMarkers reads the markers of every package in
// builtinMarkerSources, keyed by "<package path>.<type>.<Go field name>".
func builtinFieldMarkers(t *testing.T) map[string]fieldMarkers {
	t.Helper()
	out := map[string]fieldMarkers{}
	dirs := map[string]string{}
	for _, pkg := range slices.Sorted(maps.Keys(builtinMarkerSources)) {
		source := builtinMarkerSources[pkg]
		if _, ok := dirs[source[0]]; !ok {
			dirs[source[0]] = linkedModuleDir(t, source[0])
		}
		for field, markers := range packageFieldMarkers(t, filepath.Join(dirs[source[0]], source[1])) {
			out[pkg+"."+field] = markers
		}
	}
	return out
}

// fieldClasses is how the fields a kind's type reaches divide, by what the API
// requires and what the type encodes when nothing was decoded into a field.
type fieldClasses struct {
	// fields counts the fields classified.
	fields int
	// listed are required and written unauthored: the decoded value does not
	// show the omission, so the kind's required list holds them.
	listed []string
	// forced are listed fields under a parent that is itself written unauthored
	// and not required: a required list cannot hold them, since it follows what
	// was authored.
	forced []string
	// underMap are listed fields under a map value, which a required list
	// cannot name.
	underMap []string
	// omitted are required and left out of the encoding when unauthored: the
	// second set, which the decoded value does show.
	omitted []string
	// emittedEmpty are written unauthored and not required.
	emittedEmpty []string
	// unread are fields whose source was not read.
	unread []string
}

// apiRequires says the API requires a field of those markers and json options:
// it is marked +required, or it is not marked +optional and is not omitted
// when empty (the rule the OpenAPI generator of Kubernetes applies).
func apiRequires(m fieldMarkers, f kindField) bool {
	return m.required || (!m.optional && !slices.Contains(f.jsonOptions(), "omitempty"))
}

// classifyKindFields classifies every field the encoding of typ reaches, less
// those under one of skip's top-level keys. markers returns a field's markers
// and whether its source was read.
func classifyKindFields(typ reflect.Type, skip []string, markers func(kindField) (fieldMarkers, bool)) fieldClasses {
	var out fieldClasses
	walkKindFields(typ, func(f kindField) bool {
		m, _ := markers(f)
		return apiRequires(m, f)
	}, func(f kindField) {
		top, _, _ := strings.Cut(f.path, ".")
		if slices.Contains(skip, top) {
			return
		}
		out.fields++
		m, read := markers(f)
		if !read {
			out.unread = append(out.unread, f.path+" ("+f.owner.String()+"."+f.field.Name+")")
			return
		}
		required, written := apiRequires(m, f), f.writtenUnauthored()
		switch {
		case required && !written:
			out.omitted = append(out.omitted, f.path)
		case required && strings.Contains(f.path, "{}"):
			out.underMap = append(out.underMap, f.path)
		case required && f.forced:
			out.forced = append(out.forced, f.path)
		case required:
			out.listed = append(out.listed, f.path)
		case written:
			out.emittedEmpty = append(out.emittedEmpty, f.path)
		}
	})
	for _, list := range []*[]string{&out.listed, &out.forced, &out.underMap, &out.omitted, &out.emittedEmpty, &out.unread} {
		slices.Sort(*list)
	}
	return out
}

// TestBuiltinMarkerKinds_RequiredMatchMarkers derives two sets from the source
// of the linked Kubernetes API modules, for each kind of builtinMarkerKinds,
// and holds the kind to both.
//
// The first is the fields the API requires and the type writes whether or not
// they were authored. The decoded value does not show their omission, so the
// kind's required list holds exactly them (refuseUnauthoredRequired).
//
// The second is the fields the API requires and the type omits when
// unauthored. The kind's validate would have to refuse each on the decoded
// value; these kinds have none, and a member fails here, naming it.
//
// A field written unauthored that the API does not require is held to the
// row's emittedEmpty, which the kind's README entry states. The object's kind,
// apiVersion and metadata are not authorable and are not classified. A
// dependency bump that adds, drops or moves a field of any class fails here,
// as does a type that reaches a package whose source is not read.
//
// The markers are what the source declares. What the API server's validation
// requires beyond them is in no linked module, and is not derived here.
func TestBuiltinMarkerKinds_RequiredMatchMarkers(t *testing.T) {
	all := builtinFieldMarkers(t)
	// Vacuity guards: the markers are read, in both forms.
	if m := all["k8s.io/api/discovery/v1.EndpointSlice.AddressType"]; !m.required || m.optional {
		t.Fatalf("EndpointSlice.AddressType is read as %+v, want it marked required; the source is not being read", m)
	}
	if m := all["k8s.io/api/discovery/v1.Endpoint.Hostname"]; m.required || !m.optional {
		t.Fatalf("Endpoint.Hostname is read as %+v, want it marked optional; the source is not being read", m)
	}
	for _, kind := range builtinMarkerKinds {
		t.Run(kind.component, func(t *testing.T) {
			classes := classifyKindFields(kind.typ, objectIdentityKeys, func(f kindField) (fieldMarkers, bool) {
				m, ok := all[f.owner.PkgPath()+"."+f.owner.Name()+"."+f.field.Name]
				return m, ok
			})
			if classes.fields == 0 {
				t.Fatalf("the walk found no field of %s", kind.typ)
			}
			t.Logf("classified %d fields; required and written unauthored: %v; written unauthored and not required: %v",
				classes.fields, classes.listed, classes.emittedEmpty)
			for _, field := range classes.unread {
				t.Errorf("%s has no field in the source read; add its package to builtinMarkerSources", field)
			}
			if got := slices.Sorted(maps.Keys(kind.required)); !slices.Equal(got, classes.listed) {
				t.Errorf("required list = %v\nthe source marks %v", got, classes.listed)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
			for _, path := range classes.omitted {
				t.Errorf("%s is required and omitted when unauthored: the kind's validate must refuse it on the decoded value, and this test must then list it", path)
			}
			for _, path := range classes.forced {
				t.Errorf("%s is required under a parent that is written unauthored and not required: a required list cannot hold it, the kind's validate must", path)
			}
			for _, path := range classes.underMap {
				t.Errorf("%s is required under a map value, which a required list cannot name", path)
			}
			if got := slices.Sorted(slices.Values(kind.emittedEmpty)); !slices.Equal(got, classes.emittedEmpty) {
				t.Errorf("fields emitted empty = %v\nthe source gives %v", got, classes.emittedEmpty)
			}
		})
	}
}

// TestClassifyKindFields holds the classification itself on a type that has a
// member of every class, the second set included: no kind has one, so only
// this shows that the test above would name it.
func TestClassifyKindFields(t *testing.T) {
	type leaf struct {
		Name string `json:"name"`
	}
	type object struct {
		Kind     string          `json:"kind"`
		Listed   string          `json:"listed"`
		Marked   []string        `json:"marked"`
		Omitted  string          `json:"omitted,omitempty"`
		Optional string          `json:"optional,omitempty"`
		Empty    []string        `json:"empty"`
		Pointer  *leaf           `json:"pointer,omitempty"`
		Items    []leaf          `json:"items,omitempty"`
		Parent   leaf            `json:"parent,omitempty"`
		Needed   leaf            `json:"needed"`
		ByKey    map[string]leaf `json:"byKey,omitempty"`
		Foreign  reflect.Method  `json:"foreign,omitempty"`
		Unread   string          `json:"unread"`
	}
	markers := map[string]fieldMarkers{
		"object.Kind":     {required: true},
		"object.Listed":   {},
		"object.Marked":   {required: true},
		"object.Omitted":  {required: true},
		"object.Optional": {optional: true},
		"object.Empty":    {optional: true},
		"object.Pointer":  {optional: true},
		"object.Items":    {optional: true},
		"object.Parent":   {optional: true},
		"object.Needed":   {required: true},
		"object.ByKey":    {optional: true},
		"object.Foreign":  {optional: true},
		"leaf.Name":       {},
	}
	got := classifyKindFields(reflect.TypeFor[object](), []string{"kind", "foreign"}, func(f kindField) (fieldMarkers, bool) {
		m, ok := markers[f.owner.Name()+"."+f.field.Name]
		return m, ok
	})
	want := fieldClasses{
		// Every field but kind and those of foreign, which skip leaves out.
		fields:       16,
		listed:       []string{"items[].name", "listed", "marked", "needed", "needed.name", "pointer.name"},
		forced:       []string{"parent.name"},
		underMap:     []string{"byKey{}.name"},
		omitted:      []string{"omitted"},
		emittedEmpty: []string{"empty", "parent"},
		unread:       []string{"unread (components.object.Unread)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("classes = %+v\nwant      %+v", got, want)
	}
}
