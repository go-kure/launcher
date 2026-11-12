package oam

import (
	"math"

	"github.com/go-kure/kure/pkg/stack"
)

// The cases of go-kure/launcher#1466: one document per authored property refusal
// site, each with the message the refusal says today (the golden file) and the
// structured location a caller reads off it. property_error_golden_test.go holds the
// messages to the golden file; property_error_test.go holds the typed half.

// perrCase is one refusal. kind, pos, comp, trait, pol and field are the expected
// PropertyError fields as plain strings, so this file does not name the type.
type perrCase struct {
	name  string
	app   *Application
	caps  map[string]CapabilityBinding
	kind  string // a PropertyErrorKind value
	pos   string // "component", "trait" or "policy"
	comp  string
	trait string
	pol   string
	field string
}

// perrSchema is the schema every fixture handler declares, plus its top-level groups.
type perrSchema struct {
	props  map[string]PropertySchema
	groups []ExclusiveGroup
}

func (s perrSchema) PropertySchema() map[string]PropertySchema { return s.props }
func (s perrSchema) ExclusiveProperties() []ExclusiveGroup     { return s.groups }

type perrComp struct {
	typ string
	perrSchema
}

func (h perrComp) CanHandle(t string) bool { return t == h.typ }
func (h perrComp) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}

type perrTrait struct {
	typ string
	perrSchema
}

func (h perrTrait) CanHandle(t string) bool                               { return t == h.typ }
func (h perrTrait) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }

// perrBareTrait declares no schema, so only the engine's own keys are checked.
type perrBareTrait struct{ typ string }

func (h perrBareTrait) CanHandle(t string) bool                               { return t == h.typ }
func (h perrBareTrait) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }

type perrPolicy struct {
	typ string
	perrSchema
}

func (h perrPolicy) CanHandle(t string) bool { return t == h.typ }
func (h perrPolicy) Apply(*ApplicationPolicy, []string, *PolicyResult) error {
	return nil
}

// perrFullSchema reaches every kind of refusal. Two keys carry a dot and a colon with a
// space, the characters the location text uses as separators.
func perrFullSchema() perrSchema {
	str := PropertySchema{Type: PropertyTypeString}
	return perrSchema{
		props: map[string]PropertySchema{
			"name":     {Type: PropertyTypeString, Required: true},
			"mode":     {Type: PropertyTypeString, Enum: []any{"a", "b"}},
			"count":    {Type: PropertyTypeInteger},
			"ratio":    {Type: PropertyTypeNumber},
			"on":       {Type: PropertyTypeBoolean},
			"list":     {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString}},
			"u":        {Types: []PropertyType{PropertyTypeString, PropertyTypeInteger}},
			"a.b: c":   {Type: PropertyTypeInteger},
			"ex1":      str,
			"ex2":      str,
			"rq1":      str,
			"rq2":      str,
			"objs":     {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{"x": {Type: PropertyTypeString, Required: true}, "y": str}}},
			"obj":      {Type: PropertyTypeObject, Properties: map[string]PropertySchema{"inner": str, "i.n: k": {Type: PropertyTypeInteger}, "deep": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{"k": {Type: PropertyTypeString, Required: true}}}}},
			"grp":      {Type: PropertyTypeObject, Properties: map[string]PropertySchema{"g1": str, "g2": str, "h1": str, "h2": str}, Exclusive: []ExclusiveGroup{{Keys: []string{"g1", "g2"}}, {Keys: []string{"h1", "h2"}, Required: true}}},
			"scopeish": str,
		},
		groups: []ExclusiveGroup{{Keys: []string{"ex1", "ex2"}}, {Keys: []string{"rq1", "rq2"}, Required: true}},
	}
}

// perrHintComp is the full-schema component with a hint for a top-level key and one
// for a key below "obj".
type perrHintComp struct{ perrComp }

func (perrHintComp) UnsupportedFieldHint(key string) string { return "hint for " + key }
func (perrHintComp) NestedUnsupportedFieldHint(parents []string, key string) string {
	return "nested hint for " + key
}

// perrBadComp is a component whose handler declares a malformed schema.
func perrBadComp(typ string, props map[string]PropertySchema, groups ...ExclusiveGroup) perrComp {
	return perrComp{typ: typ, perrSchema: perrSchema{props: props, groups: groups}}
}

func newPerrTransformer() *Transformer {
	str := PropertySchema{Type: PropertyTypeString}
	ab := map[string]PropertySchema{"a": str, "b": str, "c": str}
	ar := map[string]PropertySchema{"a": {Type: PropertyTypeString, Required: true}, "b": str}
	one := func(s PropertySchema) map[string]PropertySchema { return map[string]PropertySchema{"t": s} }
	tr := NewTransformer(
		map[string]ComponentHandler{
			"perr":                 perrComp{typ: "perr", perrSchema: perrFullSchema()},
			"perr-hint":            perrHintComp{perrComp{typ: "perr-hint", perrSchema: perrFullSchema()}},
			"perr-grp-small":       perrBadComp("perr-grp-small", ab, ExclusiveGroup{Keys: []string{"a"}}),
			"perr-grp-undeclared":  perrBadComp("perr-grp-undeclared", ab, ExclusiveGroup{Keys: []string{"a", "zz"}}),
			"perr-grp-twice":       perrBadComp("perr-grp-twice", ab, ExclusiveGroup{Keys: []string{"a", "b"}}, ExclusiveGroup{Keys: []string{"b", "c"}}),
			"perr-grp-required":    perrBadComp("perr-grp-required", ar, ExclusiveGroup{Keys: []string{"a", "b"}}),
			"perr-grp-nested":      perrBadComp("perr-grp-nested", map[string]PropertySchema{"o": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{"p": str}, Exclusive: []ExclusiveGroup{{Keys: []string{"p"}}}}}),
			"perr-grp-nonobject":   perrBadComp("perr-grp-nonobject", map[string]PropertySchema{"s": {Type: PropertyTypeString, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}}}}),
			"perr-both":            perrBadComp("perr-both", one(PropertySchema{Type: PropertyTypeString, Types: []PropertyType{PropertyTypeString, PropertyTypeInteger}})),
			"perr-blob":            perrBadComp("perr-blob", one(PropertySchema{Type: "blob"})),
			"perr-enum-null":       perrBadComp("perr-enum-null", one(PropertySchema{Type: PropertyTypeString, Enum: []any{nil}})),
			"perr-union-one":       perrBadComp("perr-union-one", one(PropertySchema{Types: []PropertyType{PropertyTypeString}})),
			"perr-union-array":     perrBadComp("perr-union-array", one(PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeArray}})),
			"perr-union-duplicate": perrBadComp("perr-union-duplicate", one(PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeString}})),
			"perr-empty-key": perrBadComp("perr-empty-key", map[string]PropertySchema{
				"o": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{"ok": str}},
				"r": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{"": {Type: PropertyTypeString, Required: true}}},
			}),
		},
		map[string]TraitHandler{
			"perr-trait":      perrTrait{typ: "perr-trait", perrSchema: perrFullSchema()},
			"perr-trait-bare": perrBareTrait{typ: "perr-trait-bare"},
		},
	)
	tr.RegisterPolicy("perr-pol", perrPolicy{typ: "perr-pol", perrSchema: perrFullSchema()})
	return tr
}

// perrApp is one component "web" of type "perr", valid unless props says otherwise,
// carrying traits.
func perrApp(props map[string]any, traits ...Trait) *Application {
	return perrNamedApp("web", "perr", props, traits...)
}

func perrNamedApp(name, typ string, props map[string]any, traits ...Trait) *Application {
	return &Application{Spec: ApplicationSpec{Components: []Component{{Name: name, Type: typ, Properties: props, Traits: traits}}}}
}

func perrPolicyApp(name string, props map[string]any) *Application {
	return &Application{Spec: ApplicationSpec{Policies: []ApplicationPolicy{{Name: name, Type: "perr-pol", Properties: props}}}}
}

// perrWithTrait is a valid component carrying one trait of the given type.
func perrWithTrait(typ string, props map[string]any) *Application {
	return perrApp(map[string]any{"name": "n"}, Trait{Type: typ, Properties: props})
}

// perrOK is a valid property map for the full schema.
func perrOK() map[string]any { return map[string]any{"name": "n", "rq1": "x"} }

// with returns perrOK plus the extra keys.
func with(extra map[string]any) map[string]any {
	out := perrOK()
	for k, v := range extra {
		out[k] = v
	}
	return out
}

var (
	perrCapRequired = map[string]CapabilityBinding{"perr-trait": {Rendering: map[string]any{"count": 1}}}
	perrCapSetsEx2  = map[string]CapabilityBinding{"perr-trait": {Rendering: map[string]any{"ex2": "y"}}}
)

func perrCases() []perrCase {
	big := uint64(math.MaxInt64) + 1
	comp := func(name, kind, field string, props map[string]any) perrCase {
		return perrCase{name: name, app: perrApp(props), kind: kind, pos: "component", comp: "web", field: field}
	}
	trait := func(name, kind, field, typ string, props map[string]any, caps map[string]CapabilityBinding) perrCase {
		return perrCase{name: name, app: perrWithTrait(typ, props), caps: caps, kind: kind, pos: "trait", comp: "web", trait: typ, field: field}
	}
	pol := func(name, kind, field string, props map[string]any) perrCase {
		return perrCase{name: name, app: perrPolicyApp("order", props), kind: kind, pos: "policy", pol: "order", field: field}
	}
	schema := func(name, typ string, props map[string]any, field string) perrCase {
		return perrCase{name: name, app: perrNamedApp("web", typ, props), kind: "schema", pos: "component", comp: "web", field: field}
	}
	return []perrCase{
		// unsupported-field: PV:130 nested, PVA:632 top level.
		comp("component unsupported top", "unsupported-field", "nope", with(map[string]any{"nope": 1})),
		comp("component unsupported nested", "unsupported-field", "obj.bad", with(map[string]any{"obj": map[string]any{"bad": 1}})),
		comp("component unsupported in array element", "unsupported-field", "objs[1].zz", with(map[string]any{"objs": []any{map[string]any{"x": "1"}, map[string]any{"x": "1", "zz": 2}}})),
		trait("trait unsupported top", "unsupported-field", "nope", "perr-trait", with(map[string]any{"nope": 1}), nil),
		pol("policy unsupported top", "unsupported-field", "nope", with(map[string]any{"nope": 1})),

		{name: "component unsupported top with a hint", app: perrNamedApp("web", "perr-hint", with(map[string]any{"nope": 1})),
			kind: "unsupported-field", pos: "component", comp: "web", field: "nope"},
		{name: "component unsupported nested with a hint", app: perrNamedApp("web", "perr-hint", with(map[string]any{"obj": map[string]any{"bad": 1}})),
			kind: "unsupported-field", pos: "component", comp: "web", field: "obj.bad"},

		// required: PV:118 nested, :239 plain, :241 with a capability.
		comp("component required top", "required", "name", map[string]any{"rq1": "x"}),
		comp("component required nested", "required", "obj.deep.k", with(map[string]any{"obj": map[string]any{"deep": map[string]any{}}})),
		comp("component required in array element", "required", "objs[0].x", with(map[string]any{"objs": []any{map[string]any{"y": "1"}}})),
		trait("trait required top", "required", "name", "perr-trait", map[string]any{"rq1": "x"}, nil),
		trait("trait required top with capability", "required", "name", "perr-trait", map[string]any{"rq1": "x"}, perrCapRequired),
		trait("trait required nested with capability", "required", "obj.deep.k", "perr-trait", with(map[string]any{"obj": map[string]any{"deep": map[string]any{}}}), perrCapRequired),
		pol("policy required top", "required", "name", map[string]any{"rq1": "x"}),

		// exclusive: PV:322 and :328 plain, :324 and :330 with a capability.
		comp("component exclusive nested mutual", "exclusive", "grp", with(map[string]any{"grp": map[string]any{"g1": "a", "g2": "b", "h1": "c"}})),
		comp("component exclusive nested exactly one", "exclusive", "grp", with(map[string]any{"grp": map[string]any{"g1": "a"}})),
		comp("component exclusive top mutual", "exclusive", "", with(map[string]any{"ex1": "a", "ex2": "b"})),
		trait("trait exclusive top mutual with capability", "exclusive", "", "perr-trait", with(map[string]any{"ex1": "a"}), perrCapSetsEx2),
		trait("trait exclusive nested exactly one with capability", "exclusive", "grp", "perr-trait", with(map[string]any{"grp": map[string]any{"g1": "a"}}), perrCapRequired),

		// type: PV:516 string, :521 boolean, :526 integer, :535 number, :541 array,
		// :577 object, :706 union.
		comp("component type string", "type", "name", with(map[string]any{"name": 5})),
		comp("component type boolean", "type", "on", with(map[string]any{"on": "x"})),
		comp("component type integer", "type", "count", with(map[string]any{"count": "x"})),
		comp("component type number", "type", "ratio", with(map[string]any{"ratio": "x"})),
		comp("component type array", "type", "list", with(map[string]any{"list": "x"})),
		comp("component type object", "type", "obj", with(map[string]any{"obj": "x"})),
		comp("component type union", "type", "u", with(map[string]any{"u": true})),
		comp("component type array element", "type", "list[0]", with(map[string]any{"list": []any{1}})),
		trait("trait type engine key on a schema handler", "type", "scope", "perr-trait", with(map[string]any{"scope": 3}), nil),
		trait("trait type engine key on a handler with no schema", "type", "scope", "perr-trait-bare", map[string]any{"scope": 3}, nil),
		pol("policy type string", "type", "name", with(map[string]any{"name": 5})),

		// enum PV:650, range PV:934, null element PV:562. PV:926 needs a named integer
		// outside int, which no 64-bit platform has, so no case reaches it.
		comp("component enum", "enum", "mode", with(map[string]any{"mode": "c"})),
		comp("component range", "range", "count", with(map[string]any{"count": big})),
		comp("component null array element", "null-element", "list[0]", with(map[string]any{"list": []any{nil}})),

		// schema: PV:343, :349, :351, :353, :391, :496, :594, :644, :678, :683, :686.
		schema("schema group with one key", "perr-grp-small", map[string]any{}, ""),
		schema("schema group key undeclared", "perr-grp-undeclared", map[string]any{}, ""),
		schema("schema group key twice", "perr-grp-twice", map[string]any{}, ""),
		schema("schema group key required", "perr-grp-required", map[string]any{}, ""),
		schema("schema nested group with one key", "perr-grp-nested", map[string]any{}, "o"),
		schema("schema group on a non-object node", "perr-grp-nonobject", map[string]any{}, "s"),
		schema("schema both type and types", "perr-both", map[string]any{"t": "x"}, "t"),
		schema("schema unsupported type", "perr-blob", map[string]any{"t": "x"}, "t"),
		schema("schema enum member null", "perr-enum-null", map[string]any{"t": "x"}, "t"),
		schema("schema union of one member", "perr-union-one", map[string]any{"t": "x"}, "t"),
		schema("schema union member not scalar", "perr-union-array", map[string]any{"t": "x"}, "t"),
		schema("schema union member twice", "perr-union-duplicate", map[string]any{"t": "x"}, "t"),

		// A name or a key holding the location's own separators.
		{name: "component name and key with separators", app: perrNamedApp("we.b: x", "perr", with(map[string]any{"a.b: c": "s"})),
			kind: "type", pos: "component", comp: "we.b: x", field: "a.b: c"},
		{name: "component name and nested key with separators", app: perrNamedApp("we.b: x", "perr", with(map[string]any{"obj": map[string]any{"i.n: k": "s"}})),
			kind: "type", pos: "component", comp: "we.b: x", field: "obj.i.n: k"},
		{name: "trait on a component with separators and key with separators", app: perrNamedApp("we.b: x", "perr", map[string]any{"name": "n"}, Trait{Type: "perr-trait", Properties: with(map[string]any{"a.b: c": "s"})}),
			kind: "type", pos: "trait", comp: "we.b: x", trait: "perr-trait", field: "a.b: c"},
		{name: "policy name and key with separators", app: perrPolicyApp("po.l: x", with(map[string]any{"obj": map[string]any{"i.n: k": "s"}})),
			kind: "type", pos: "policy", pol: "po.l: x", field: "obj.i.n: k"},
		// An empty key is a key: its dot stays in the field.
		{name: "unsupported empty key below an object", app: perrNamedApp("web", "perr-empty-key", map[string]any{"o": map[string]any{"": 1}}),
			kind: "unsupported-field", pos: "component", comp: "web", field: "o."},
		{name: "required empty key below an object", app: perrNamedApp("web", "perr-empty-key", map[string]any{"r": map[string]any{}}),
			kind: "required", pos: "component", comp: "web", field: "r."},
	}
}
