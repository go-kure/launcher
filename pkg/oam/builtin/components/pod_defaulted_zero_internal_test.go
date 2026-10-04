package components

import (
	"encoding"
	"encoding/json"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// docDefault finds the default a k8s.io/api field comment states for a number
// or a boolean: "Defaults to 1 second.", "Default to 10 seconds.", "Default is
// false.", "Default false.".
var docDefault = regexp.MustCompile(`(?i)\bdefaults?(?: to| is)? (-?[0-9]+|true|false)\b`)

// TestPodSpecDefaultedZeros_MatchFieldDocs derives the fields of a pod spec on
// which an authored 0 or false would be silently replaced, and requires
// podSpecDefaultedZeros to equal them. Such a field is a non-pointer omitempty
// number or boolean under corev1.PodSpec (by json path) whose field comment, as
// the linked k8s.io/api publishes it through SwaggerDoc, states a default that
// is not zero or false. A dependency bump that adds one, drops one or changes
// its default fails here, naming it.
//
// The core types have no CRD to read defaults from, so the field comment is
// the source, as it is for the list itself. A default the comment does not
// state in one of docDefault's forms is not found: the test holds the list to
// the documented defaults, not to the API server's defaulting code.
//
// ephemeralContainers is not walked: the kind components refuse the field
// whole (validateAuthoredPodSpec).
func TestPodSpecDefaultedZeros_MatchFieldDocs(t *testing.T) {
	docs := omitemptyScalarDocs(t, reflect.TypeFor[corev1.PodSpec](), "ephemeralContainers")

	// Vacuity guards: a walk that stops early, or a SwaggerDoc that returns
	// nothing, finds no default and would pass against an empty list. At
	// k8s.io/api v0.37.1 the walk finds 65 fields, 51 of them with a stated
	// default, 24 of those not zero.
	if len(docs) < 40 {
		t.Fatalf("found %d omitempty numbers and booleans under PodSpec, want >= 40; the reflection walk is broken", len(docs))
	}
	stated := 0
	want := map[string]string{}
	for path, doc := range docs {
		m := docDefault.FindStringSubmatch(doc)
		if m == nil {
			continue
		}
		stated++
		if def := strings.ToLower(m[1]); !crdDefaultIsZero(def) {
			want[path] = def
		}
	}
	if stated < 15 {
		t.Fatalf("found %d stated defaults among those fields, want >= 15; the field comments are not being read", stated)
	}
	t.Logf("walked %d fields, %d with a stated default, %d of them not zero", len(docs), stated, len(want))

	got := podSpecDefaultedZeros("").fields
	for _, path := range slices.Sorted(maps.Keys(want)) {
		def, ok := got[path]
		switch {
		case !ok:
			t.Errorf("missing %s (documented default %s): a non-pointer omitempty field with a non-zero default", path, want[path])
		case def != want[path]:
			t.Errorf("%s records default %s, the field comment says %s", path, def, want[path])
		}
	}
	for _, path := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[path]; !ok {
			t.Errorf("stale %s: not a non-pointer omitempty field with a documented non-zero default", path)
		}
	}
}

// TestPodSpecDefaultedZeros_Prefix: the list of a pod spec that sits under a
// path in the properties is the same list under that path.
func TestPodSpecDefaultedZeros_Prefix(t *testing.T) {
	bare := podSpecDefaultedZeros("").fields
	nested := podSpecDefaultedZeros("template.spec.").fields
	if len(nested) != len(bare) {
		t.Fatalf("prefixed list has %d fields, the bare one %d", len(nested), len(bare))
	}
	for path, def := range bare {
		if nested["template.spec."+path] != def {
			t.Errorf("template.spec.%s = %q, want %q", path, nested["template.spec."+path], def)
		}
	}
}

// omitemptyScalarDocs is omitemptyScalarPaths with each field's comment: the
// json path of every non-pointer bool, integer or float field tagged omitempty
// under typ, mapped to the description its struct's SwaggerDoc gives it. The
// top-level fields named in skip are not walked. A struct that holds such a
// field and publishes no description for it fails the test, since its default
// could not be read.
func omitemptyScalarDocs(t *testing.T, typ reflect.Type, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
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
		var doc map[string]string
		if m := reflect.New(typ).Elem().MethodByName("SwaggerDoc"); m.IsValid() {
			doc, _ = m.Call(nil)[0].Interface().(map[string]string)
		}
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
			if path == "" && slices.Contains(skip, name) {
				continue
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
				if !slices.Contains(strings.Split(opts, ","), "omitempty") {
					continue
				}
				if doc[name] == "" {
					t.Errorf("%s (%s.%s) has no field description to read a default from", child, typ, f.Name)
					continue
				}
				out[child] = doc[name]
			default:
				walk(f.Type, child, seen)
			}
		}
	}
	walk(typ, "", map[reflect.Type]bool{})
	return out
}
