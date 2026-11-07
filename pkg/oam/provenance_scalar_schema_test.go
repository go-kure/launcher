package oam

import (
	"reflect"
	"testing"
)

// Named scalar types of every kind emission validation unnames.
type (
	provMode     string
	provSwitch   bool
	provRatio    float64
	provReplicas int32
	provCount    int64
	provBig      uint64
)

// scalarSchemaComponent declares one property per scalar schema, and an object that
// holds an integer and a number, so emission validation rewrites the Go type of what
// a rule gives it in each of the ways it can.
type scalarSchemaComponent struct {
	provComponentHandler
}

func (scalarSchemaComponent) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"mode":  {Type: PropertyTypeString},
		"on":    {Type: PropertyTypeBoolean},
		"ratio": {Type: PropertyTypeNumber},
		"count": {Type: PropertyTypeInteger},
		"pair": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"a": {Type: PropertyTypeInteger},
			"b": {Type: PropertyTypeNumber},
		}},
	}
}

// A rule that copies a value of a named scalar type into a target whose schema names
// the scalar gave back a value that emission validation then unnamed. The report still
// calls it copied, and calls it rendered only when the rule changed the value or its
// numeric kind.
func TestProvenance_NamedScalarStaysCopiedUnderItsSchema(t *testing.T) {
	const big = provBig(1<<63 + 1)
	cases := []struct {
		name     string
		key      string
		authored any
		emit     any // nil: the rule copies the authored value
		want     SourceKind
	}{
		{"named string under a string schema", "mode", provMode("rolling"), nil, SourceCopied},
		{"named bool under a boolean schema", "on", provSwitch(true), nil, SourceCopied},
		{"named float under a number schema", "ratio", provRatio(0.5), nil, SourceCopied},
		{"named int32 under a number schema", "ratio", provReplicas(3), nil, SourceCopied},
		{"named int64 under a number schema", "ratio", provCount(3), nil, SourceCopied},
		{"negative named int32 under a number schema", "ratio", provReplicas(-3), nil, SourceCopied},
		{"named unsigned above MaxInt under a number schema", "ratio", big, nil, SourceCopied},
		{"named int32 under an integer schema", "count", provReplicas(3), nil, SourceCopied},
		{"named int32 handed on as an int32", "ratio", provReplicas(3), int32(3), SourceCopied},

		{"named int32 changed from 3 to 4", "ratio", provReplicas(3), provReplicas(4), SourceRendered},
		{"named int32 changed to an int64", "ratio", provReplicas(3), int64(3), SourceRendered},
		{"named int32 changed to a float64", "ratio", provReplicas(3), float64(3), SourceRendered},
		{"named string changed", "mode", provMode("rolling"), provMode("recreate"), SourceRendered},
		{"named bool changed", "on", provSwitch(true), provSwitch(false), SourceRendered},
		{"named float changed", "ratio", provRatio(0.5), provRatio(0.25), SourceRendered},
		{"named unsigned above MaxInt changed", "ratio", big, provBig(1<<63 + 3), SourceRendered},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newProvCapture()
			tr := provTransformer(c)
			tr.RegisterComponent("scalar-target", scalarSchemaComponent{provComponentHandler{typ: "scalar-target", c: c}})
			tr.RegisterComponentLowering(emitRule{"scalar-rule", func(in *Component) []Component {
				out := tc.emit
				if out == nil {
					out = in.Properties[tc.key]
				}
				return []Component{{Name: in.Name, Type: "scalar-target", Properties: map[string]any{tc.key: out}}}
			}})
			app := provApp(Component{Name: "data", Type: "scalar-rule", Properties: map[string]any{tc.key: tc.authored}})
			_, _, prov, err := tr.TransformWithProvenance(app, TransformContext{})
			if err != nil {
				t.Fatalf("TransformWithProvenance: %v", err)
			}
			want := Source{Kind: tc.want, Ref: "component/scalar-rule"}
			if tc.want == SourceCopied {
				want.From = SourceAuthored
			}
			got := entriesFor(prov, "data", "", "")
			if !reflect.DeepEqual(got, map[string]Source{tc.key: want}) {
				t.Errorf("entries = %+v, want %q: %+v", got, tc.key, want)
			}
		})
	}
}

// A copied object whose integer leaf is given back as an int and whose number leaf is
// given back as an int32 reads as copied leaf by leaf; a changed leaf reads as
// rendered and leaves the other leaf copied.
func TestProvenance_ObjectOfAnIntegerAndANumberStaysCopied(t *testing.T) {
	cases := []struct {
		name  string
		emit  map[string]any // nil: the rule copies the authored object
		wantA SourceKind
		wantB SourceKind
	}{
		{"object copied", nil, SourceCopied, SourceCopied},
		{"number leaf changed", map[string]any{"a": provReplicas(3), "b": provReplicas(4)}, SourceCopied, SourceRendered},
		{"integer leaf changed", map[string]any{"a": provReplicas(4), "b": provReplicas(3)}, SourceRendered, SourceCopied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newProvCapture()
			tr := provTransformer(c)
			tr.RegisterComponent("scalar-target", scalarSchemaComponent{provComponentHandler{typ: "scalar-target", c: c}})
			tr.RegisterComponentLowering(emitRule{"scalar-rule", func(in *Component) []Component {
				var out any = in.Properties["pair"]
				if tc.emit != nil {
					out = tc.emit
				}
				return []Component{{Name: in.Name, Type: "scalar-target", Properties: map[string]any{"pair": out}}}
			}})
			app := provApp(Component{Name: "data", Type: "scalar-rule", Properties: map[string]any{
				"pair": map[string]any{"a": provReplicas(3), "b": provReplicas(3)},
			}})
			_, _, prov, err := tr.TransformWithProvenance(app, TransformContext{})
			if err != nil {
				t.Fatalf("TransformWithProvenance: %v", err)
			}
			got := entriesFor(prov, "data", "", "")
			for path, kind := range map[string]SourceKind{"pair.a": tc.wantA, "pair.b": tc.wantB} {
				if got[path].Kind != kind {
					t.Errorf("%s: kind = %q, want %q (entries %+v)", path, got[path].Kind, kind, got)
				}
			}
		})
	}
}

// A snapshot of a named scalar holds the unnamed value a later step gives back, in the
// kind validation keeps for it, and not another value or another numeric kind.
func TestPropIndex_HoldsAnUnnamedFormOfANamedScalar(t *testing.T) {
	cases := []struct {
		name string
		snap any
		now  any
		want bool
	}{
		{"named string", provMode("x"), "x", true},
		{"named bool", provSwitch(true), true, true},
		{"named float", provRatio(0.5), 0.5, true},
		{"named int32 as an int", provReplicas(3), 3, true},
		{"named int32 as an int32", provReplicas(3), int32(3), true},
		{"negative named int32 as an int32", provReplicas(-3), int32(-3), true},
		{"named int64 as an int", provCount(3), 3, true},
		{"named int64 as an int64", provCount(3), int64(3), true},
		{"named unsigned above MaxInt", provBig(1<<63 + 1), uint64(1<<63 + 1), true},
		{"named int32 as itself", provReplicas(3), provReplicas(3), true},

		{"named string, another string", provMode("x"), "y", false},
		{"named bool, the other bool", provSwitch(true), false, false},
		{"named float, another float", provRatio(0.5), 0.25, false},
		{"named int32, another number", provReplicas(3), int32(4), false},
		{"named int32 as an int64", provReplicas(3), int64(3), false},
		{"named int32 as a float64", provReplicas(3), float64(3), false},
		{"named int64 as an int32", provCount(3), int32(3), false},
		{"named int64 as a float64", provCount(3), float64(3), false},
		{"named unsigned above MaxInt, another one", provBig(1<<63 + 1), uint64(1<<63 + 3), false},
		{"plain int32 as an int", int32(3), 3, false},
		{"plain int as an int32", 3, int32(3), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := indexOf(map[string]any{"p": tc.snap}).holds("p", tc.now); got != tc.want {
				t.Errorf("holds(%#v, %#v) = %v, want %v", tc.snap, tc.now, got, tc.want)
			}
		})
	}
}

// The comparison runs on the parent a collection is recorded under too, so it holds
// per leaf: a list or an object with an integer leaf and a number leaf matches what
// validation wrote for each, and no other width.
func TestPropIndex_HoldsACollectionOfNamedScalarsLeafByLeaf(t *testing.T) {
	obj := indexOf(map[string]any{"pair": map[string]any{"a": provReplicas(3), "b": provReplicas(3)}})
	list := indexOf(map[string]any{"l": []any{provReplicas(1), provReplicas(1)}})
	cases := []struct {
		name string
		ix   propIndex
		path string
		now  any
		want bool
	}{
		{"object, integer and number leaf", obj, "pair", map[string]any{"a": 3, "b": int32(3)}, true},
		{"object, number leaf changed", obj, "pair", map[string]any{"a": 3, "b": int32(4)}, false},
		{"object, number leaf as an int64", obj, "pair", map[string]any{"a": 3, "b": int64(3)}, false},
		{"object, number leaf as a float64", obj, "pair", map[string]any{"a": 3, "b": float64(3)}, false},
		{"list, int and int32", list, "l", []any{1, int32(1)}, true},
		{"list, int and int64", list, "l", []any{1, int64(1)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ix.holds(tc.path, tc.now); got != tc.want {
				t.Errorf("holds(%q, %#v) = %v, want %v", tc.path, tc.now, got, tc.want)
			}
		})
	}
}

// The snapshot holds whatever emission validation writes for a named scalar under
// each schema. This ties the comparison to validation's own output, so a new rewrite
// the comparison lacks fails here and not in a report.
func TestPropIndex_HoldsWhatScalarValidationWrote(t *testing.T) {
	cases := []struct {
		name   string
		schema PropertySchema
		value  any
	}{
		{"string", PropertySchema{Type: PropertyTypeString}, provMode("x")},
		{"boolean", PropertySchema{Type: PropertyTypeBoolean}, provSwitch(true)},
		{"number from a float", PropertySchema{Type: PropertyTypeNumber}, provRatio(0.5)},
		{"number from an int32", PropertySchema{Type: PropertyTypeNumber}, provReplicas(3)},
		{"number from a negative int32", PropertySchema{Type: PropertyTypeNumber}, provReplicas(-3)},
		{"number from an int64", PropertySchema{Type: PropertyTypeNumber}, provCount(3)},
		{"number from an unsigned above MaxInt", PropertySchema{Type: PropertyTypeNumber}, provBig(1<<63 + 1)},
		{"integer from an int32", PropertySchema{Type: PropertyTypeInteger}, provReplicas(3)},
		{"integer from an int64", PropertySchema{Type: PropertyTypeInteger}, provCount(3)},
		{"object of an integer and a number", PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"a": {Type: PropertyTypeInteger},
			"b": {Type: PropertyTypeNumber},
		}}, map[string]any{"a": provReplicas(3), "b": provReplicas(3)}},
		{"array of numbers", PropertySchema{Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeNumber}},
			[]any{provReplicas(1), provCount(2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ix := indexOf(map[string]any{"p": tc.value}) // before validation, which may write into a collection
			got, err := validatePropertyValue(tc.schema, tc.value, "p")
			if err != nil {
				t.Fatalf("validatePropertyValue(%#v): %v", tc.value, err)
			}
			if !ix.holds("p", got) {
				t.Errorf("holds(p, %#v) = false, want true: validation wrote %T for %T", got, got, tc.value)
			}
		})
	}
}
