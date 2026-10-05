package builtin_test

import (
	"encoding"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	ciliumlabels "github.com/cilium/cilium/pkg/labels"
	ciliumapi "github.com/cilium/cilium/pkg/policy/api"

	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// decodesItself reports whether encoding/json hands a value of type t to the
// type's own decoding, where it checks no key.
func decodesItself(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	return pt.Implements(reflect.TypeFor[json.Unmarshaler]()) || pt.Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

// jsonStep is one step of a JSON path: a key, or the first element of a list.
type jsonStep struct {
	key  string
	elem bool
}

func jsonPath(steps []jsonStep) string {
	var b strings.Builder
	for i, s := range steps {
		switch {
		case s.elem:
			b.WriteString("[0]")
		case i == 0:
			b.WriteString(s.key)
		default:
			b.WriteString("." + s.key)
		}
	}
	return b.String()
}

// jsonDocument returns the document that holds leaf at steps and nothing else.
func jsonDocument(t *testing.T, steps []jsonStep, leaf any) map[string]any {
	t.Helper()
	value := leaf
	for _, s := range slices.Backward(steps) {
		if s.elem {
			value = []any{value}
		} else {
			value = map[string]any{s.key: value}
		}
	}
	doc, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("path %s does not start at a key", jsonPath(steps))
	}
	return doc
}

// jsonKeyOf returns the key encoding/json reads field f from, whether f is an
// embedded struct whose fields are read as the parent's own, and whether f is
// read at all.
func jsonKeyOf(f reflect.StructField) (key string, promoted, read bool) {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if f.Tag.Get("json") == "-" {
		return "", false, false
	}
	ft := f.Type
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
		return "", true, true
	}
	if !f.IsExported() {
		return "", false, false
	}
	if name == "" {
		name = f.Name
	}
	return name, false, true
}

// declaredJSONKeys returns the keys struct type t declares, the fields of an
// embedded struct counted as its own.
func declaredJSONKeys(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var keys []string
	if t.Kind() != reflect.Struct {
		return keys
	}
	for f := range t.Fields() {
		key, promoted, read := jsonKeyOf(f)
		switch {
		case !read:
		case promoted:
			keys = append(keys, declaredJSONKeys(f.Type)...)
		default:
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// walkJSONTypes calls visit for every type a JSON document of type t can hold a
// value of, with the path to its first position under each field. visit returns
// the type to go on with below that position (the type itself, or the shape of
// one that decodes itself) and false to stop there. A type already on the path
// is not entered again.
func walkJSONTypes(t reflect.Type, steps []jsonStep, onPath map[reflect.Type]bool, visit func(reflect.Type, []jsonStep) (reflect.Type, bool)) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if onPath[t] {
		return
	}
	next, descend := visit(t, steps)
	if !descend {
		return
	}
	onPath[t] = true
	defer delete(onPath, t)
	t = next
	switch t.Kind() {
	case reflect.Struct:
		for f := range t.Fields() {
			key, promoted, read := jsonKeyOf(f)
			switch {
			case !read:
			case promoted:
				walkJSONTypes(f.Type, steps, onPath, func(ft reflect.Type, at []jsonStep) (reflect.Type, bool) {
					if len(at) == len(steps) {
						// The embedded struct itself holds no position of its own.
						return ft, true
					}
					return visit(ft, at)
				})
			default:
				walkJSONTypes(f.Type, append(slices.Clone(steps), jsonStep{key: key}), onPath, visit)
			}
		}
	case reflect.Slice, reflect.Array:
		walkJSONTypes(t.Elem(), append(slices.Clone(steps), jsonStep{elem: true}), onPath, visit)
	case reflect.Map:
		walkJSONTypes(t.Elem(), append(slices.Clone(steps), jsonStep{key: "k"}), onPath, visit)
	default:
		// A scalar holds no further position.
	}
}

// ciliumUnchecked lists the types under a Cilium rule that decode themselves
// and that UnknownCiliumKeyPath does not look into, each with the reason. An
// entry is a decision; TestCiliumSelfDecodedShapes_CoverRule fails on a type
// that is neither here nor among the shapes.
var ciliumUnchecked = map[string]string{
	"intstr.IntOrString": "a number or a string: its own decoding refuses an object, so it holds no key",
}

// TestCiliumSelfDecodedShapes_CoverRule: every type a Cilium rule can hold that
// decodes itself has a shape, so UnknownCiliumKeyPath looks into it, or is
// listed as unchecked with a reason. A Cilium bump that adds such a type, or
// that drops one of these, fails here.
func TestCiliumSelfDecodedShapes_CoverRule(t *testing.T) {
	shapes := builtin.CiliumSelfDecodedShapes()
	reached := map[reflect.Type]string{}
	unchecked := map[string]string{}
	walkJSONTypes(reflect.TypeFor[ciliumapi.Rule](), nil, map[reflect.Type]bool{}, func(rt reflect.Type, steps []jsonStep) (reflect.Type, bool) {
		if !decodesItself(rt) {
			return rt, true
		}
		if shape, ok := shapes[rt]; ok {
			if _, seen := reached[rt]; !seen {
				reached[rt] = jsonPath(steps)
			}
			return shape, true
		}
		name := rt.String()
		if _, seen := unchecked[name]; !seen {
			unchecked[name] = jsonPath(steps)
		}
		return rt, false
	})
	for name, path := range unchecked {
		if _, ok := ciliumUnchecked[name]; !ok {
			t.Errorf("%s (first at %s) decodes itself: an unknown key inside it is dropped. Give it a shape, or list it in ciliumUnchecked with the reason", name, path)
		}
	}
	for name := range ciliumUnchecked {
		if _, ok := unchecked[name]; !ok {
			t.Errorf("%s is listed as unchecked and a rule no longer holds one, or it no longer decodes itself: drop the entry", name)
		}
	}
	for self := range shapes {
		if _, ok := reached[self]; !ok {
			t.Errorf("%s has a shape and a rule no longer holds one, or it no longer decodes itself: drop the shape", self)
		}
	}
}

// TestCiliumSelfDecodedShapes_MatchTheTypes holds each shape to the type it
// stands for. The shape declares the keys the type declares, so a field added
// to the type is not refused as unknown; and every key of the shape survives
// the type's own decoding, so the shape does not pass a key the type drops.
func TestCiliumSelfDecodedShapes_MatchTheTypes(t *testing.T) {
	// One document per type that sets every key the type keeps.
	examples := map[reflect.Type]string{
		reflect.TypeFor[ciliumapi.EndpointSelector](): `{"matchLabels":{"role":"db"},"matchExpressions":[{"key":"tier","operator":"In","values":["a"]}]}`,
		reflect.TypeFor[ciliumapi.ICMPField]():        `{"family":"IPv6","type":128}`,
		reflect.TypeFor[ciliumlabels.Label]():         `{"key":"role","value":"db","source":"k8s"}`,
	}
	normal := func(t *testing.T, v any) any {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}
	for self, shape := range builtin.CiliumSelfDecodedShapes() {
		t.Run(self.String(), func(t *testing.T) {
			if decodesItself(shape) {
				t.Fatalf("the shape %s decodes itself: its keys cannot be checked either", shape)
			}
			if got, want := declaredJSONKeys(shape), declaredJSONKeys(self); !slices.Equal(got, want) {
				t.Errorf("the shape declares %v, the type declares %v", got, want)
			}
			example, ok := examples[self]
			if !ok {
				t.Fatalf("no example document for %s", self)
			}
			var authored map[string]any
			if err := json.Unmarshal([]byte(example), &authored); err != nil {
				t.Fatalf("example: %v", err)
			}
			keys := make([]string, 0, len(authored))
			for k := range authored {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if want := declaredJSONKeys(shape); !slices.Equal(keys, want) {
				t.Fatalf("the example sets %v, the shape declares %v: the example must set every key", keys, want)
			}
			viaType, viaShape := reflect.New(self), reflect.New(shape)
			if err := json.Unmarshal([]byte(example), viaType.Interface()); err != nil {
				t.Fatalf("decode into the type: %v", err)
			}
			if err := json.Unmarshal([]byte(example), viaShape.Interface()); err != nil {
				t.Fatalf("decode into the shape: %v", err)
			}
			if got := normal(t, viaType.Interface()); !reflect.DeepEqual(got, normal(t, authored)) {
				t.Errorf("the type's own decoding kept %v of %v: the shape passes a key the type drops", got, authored)
			}
			if got := normal(t, viaShape.Interface()); !reflect.DeepEqual(got, normal(t, authored)) {
				t.Errorf("the shape kept %v of %v", got, authored)
			}
		})
	}
}

// TestUnknownCiliumKeyPath_ValueTypedICMPField: a caller's own type may hold an
// ICMP field by value. Cilium's decoder panics on the null the walk probes the
// key with; the key is declared all the same, and its value is walked.
func TestUnknownCiliumKeyPath_ValueTypedICMPField(t *testing.T) {
	type holder struct {
		ICMP ciliumapi.ICMPField `json:"icmp"`
	}
	valid := map[string]any{"icmp": map[string]any{"family": "IPv4", "type": 8}}
	if _, _, err := builtin.DecodeStrictJSON[holder](valid); err != nil {
		t.Fatalf("DecodeStrictJSON refused the valid document: %v", err)
	}
	if got := builtin.UnknownCiliumKeyPath[holder](valid); got != "" {
		t.Errorf("UnknownCiliumKeyPath = %q for a valid field, want nothing", got)
	}
	typo := map[string]any{"icmp": map[string]any{"family": "IPv4", "type": 8, "zzUnknown": true}}
	if got := builtin.UnknownCiliumKeyPath[holder](typo); got != "icmp.zzUnknown" {
		t.Errorf("UnknownCiliumKeyPath = %q, want icmp.zzUnknown", got)
	}
}

// TestUnknownCiliumKeyPath_EveryPosition: an unknown key is reported at every
// position of a rule that holds a type with a shape. The positions come from
// the Cilium types, not from a list, so a field a Cilium bump adds is covered
// here without an edit, and a position the walk does not reach fails.
func TestUnknownCiliumKeyPath_EveryPosition(t *testing.T) {
	shapes := builtin.CiliumSelfDecodedShapes()
	var positions []string
	walkJSONTypes(reflect.TypeFor[ciliumapi.Rule](), nil, map[reflect.Type]bool{}, func(rt reflect.Type, steps []jsonStep) (reflect.Type, bool) {
		shape, ok := shapes[rt]
		if !ok {
			return rt, !decodesItself(rt)
		}
		path := jsonPath(steps)
		positions = append(positions, path)
		t.Run(path, func(t *testing.T) {
			doc := jsonDocument(t, steps, map[string]any{"zzUnknown": true})
			if got, want := builtin.UnknownCiliumKeyPath[ciliumapi.Rule](doc), path+".zzUnknown"; got != want {
				t.Errorf("UnknownCiliumKeyPath = %q, want %q", got, want)
			}
			if got := builtin.UnknownCiliumKeyPath[ciliumapi.Rule](jsonDocument(t, steps, map[string]any{})); got != "" {
				t.Errorf("UnknownCiliumKeyPath = %q for an empty value, want nothing", got)
			}
		})
		return shape, true
	})
	// The walk above is only as good as its reach: these must be among them.
	for _, want := range []string{
		"endpointSelector",
		"nodeSelector",
		"ingress[0].fromEndpoints[0]",
		"ingress[0].fromNodes[0]",
		"ingress[0].fromCIDRSet[0].cidrGroupSelector",
		"ingress[0].icmps[0].fields[0]",
		"ingressDeny[0].fromEndpoints[0]",
		"egress[0].toEndpoints[0]",
		"egress[0].toNodes[0]",
		"egress[0].toCIDRSet[0].cidrGroupSelector",
		"egress[0].icmps[0].fields[0]",
		"egressDeny[0].toEndpoints[0]",
		"labels[0]",
	} {
		if !slices.Contains(positions, want) {
			t.Errorf("no position %s among %v", want, positions)
		}
	}
}
