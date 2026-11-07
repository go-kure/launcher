package oam

import (
	"math"
	"reflect"
	"testing"
)

// namedReplicas is a named integer type, one emission validation turns into an int.
type namedReplicas int32

// intSchemaComponent is a component handler whose schema declares replicas as an
// integer, so emission validation normalizes the Go type of what a rule gives it.
type intSchemaComponent struct {
	provComponentHandler
}

func (intSchemaComponent) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"replicas": {Type: PropertyTypeInteger}}
}

// intSchemaTrait is the trait handler with the same kind of schema, for port.
type intSchemaTrait struct {
	provTraitHandler
}

func (intSchemaTrait) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"port": {Type: PropertyTypeInteger}}
}

// A rule that copies an integer of another Go type into a target with an integer
// schema gave back a value that emission validation then turned into an int. The
// report still calls it copied, from where the rule's input held it, and calls it
// rendered only when the rule changed the number.
func TestProvenance_IntegerKindNormalizedByEmissionStaysCopied(t *testing.T) {
	cases := []struct {
		name      string
		register  func(*Transformer)
		app       func() *Application
		component string
		trait     string
		want      map[string]Source
	}{{
		name: "component rule copies an int16",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(emitRule{"int16-copier", func(c *Component) []Component {
				return []Component{{Name: c.Name, Type: "counted", Properties: map[string]any{"replicas": c.Properties["replicas"]}}}
			}})
		},
		app: func() *Application {
			return provApp(Component{Name: "data", Type: "int16-copier", Properties: map[string]any{"replicas": int16(3)}})
		},
		component: "data",
		want: map[string]Source{
			"replicas": {Kind: SourceCopied, Ref: "component/int16-copier", From: SourceAuthored},
		},
	}, {
		name: "component rule copies a named int32",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(emitRule{"named-copier", func(c *Component) []Component {
				return []Component{{Name: c.Name, Type: "counted", Properties: map[string]any{"replicas": c.Properties["replicas"]}}}
			}})
		},
		app: func() *Application {
			return provApp(Component{Name: "data", Type: "named-copier", Properties: map[string]any{"replicas": namedReplicas(3)}})
		},
		component: "data",
		want: map[string]Source{
			"replicas": {Kind: SourceCopied, Ref: "component/named-copier", From: SourceAuthored},
		},
	}, {
		name: "component rule copies a uint8",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(emitRule{"uint8-copier", func(c *Component) []Component {
				return []Component{{Name: c.Name, Type: "counted", Properties: map[string]any{"replicas": c.Properties["replicas"]}}}
			}})
		},
		app: func() *Application {
			return provApp(Component{Name: "data", Type: "uint8-copier", Properties: map[string]any{"replicas": uint8(3)}})
		},
		component: "data",
		want: map[string]Source{
			"replicas": {Kind: SourceCopied, Ref: "component/uint8-copier", From: SourceAuthored},
		},
	}, {
		name: "component rule changes 3 to 4",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(emitRule{"int16-changer", func(c *Component) []Component {
				return []Component{{Name: c.Name, Type: "counted", Properties: map[string]any{"replicas": int16(4)}}}
			}})
		},
		app: func() *Application {
			return provApp(Component{Name: "data", Type: "int16-changer", Properties: map[string]any{"replicas": int16(3)}})
		},
		component: "data",
		want: map[string]Source{
			"replicas": {Kind: SourceRendered, Ref: "component/int16-changer"},
		},
	}, {
		name: "trait rule copies an int16",
		register: func(tr *Transformer) {
			tr.RegisterTraitLowering(fnTraitRule{"int16-src", func(t *Trait) []Trait {
				return []Trait{{Type: "sized", Properties: map[string]any{"port": t.Properties["port"]}}}
			}})
		},
		app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{"image": "a"},
				Traits: []Trait{{Type: "int16-src", Properties: map[string]any{"port": int16(80)}}}})
		},
		component: "web",
		trait:     "sized",
		want: map[string]Source{
			"port": {Kind: SourceCopied, Ref: "trait/int16-src", From: SourceAuthored},
		},
	}, {
		name: "trait rule changes 80 to 81",
		register: func(tr *Transformer) {
			tr.RegisterTraitLowering(fnTraitRule{"int16-bump", func(t *Trait) []Trait {
				return []Trait{{Type: "sized", Properties: map[string]any{"port": int16(81)}}}
			}})
		},
		app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{"image": "a"},
				Traits: []Trait{{Type: "int16-bump", Properties: map[string]any{"port": int16(80)}}}})
		},
		component: "web",
		trait:     "sized",
		want: map[string]Source{
			"port": {Kind: SourceRendered, Ref: "trait/int16-bump"},
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newProvCapture()
			tr := provTransformer(c)
			tr.RegisterComponent("counted", intSchemaComponent{provComponentHandler{typ: "counted", c: c}})
			tr.RegisterTrait("sized", intSchemaTrait{provTraitHandler{typ: "sized", c: c}})
			tc.register(tr)
			_, _, prov, err := tr.TransformWithProvenance(tc.app(), TransformContext{})
			if err != nil {
				t.Fatalf("TransformWithProvenance: %v", err)
			}
			got := entriesFor(prov, tc.component, "", tc.trait)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("entries = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Canonical form folds the integer kinds the way emission validation does, so a value
// and its normalized self are one value: int8, int16, every unsigned kind within
// math.MaxInt and a named int, int32 or int64 become int. An unnamed int, int32 or
// int64 stays as it is, as the decoder set keeps it, and so does an unsigned value
// above math.MaxInt, which emission validation refuses and canonical form cannot.
func TestCanonicalValue_FoldsIntegerKinds(t *testing.T) {
	above := uint64(math.MaxInt) + 1
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"int8", int8(3), 3},
		{"int16", int16(3), 3},
		{"uint8", uint8(3), 3},
		{"uint16", uint16(3), 3},
		{"uint32", uint32(3), 3},
		{"uint64 within range", uint64(math.MaxInt), math.MaxInt},
		{"uint", uint(3), 3},
		{"named int32", namedReplicas(3), 3},
		{"int", 3, 3},
		{"unnamed int32 stays", int32(3), int32(3)},
		{"unnamed int64 stays", int64(3), int64(3)},
		{"uint64 above MaxInt stays", above, above},
		{"string stays", "3", "3"},
		{"float stays", 3.0, 3.0},
		{"in a typed slice", []int16{1, 2}, []any{1, 2}},
		{"in a typed map", map[string]uint8{"a": 1}, map[string]any{"a": 1}},
		{"in a nested any", map[string]any{"a": []any{int8(1)}}, map[string]any{"a": []any{1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := canonicalValue(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("canonicalValue(%#v) = %#v (%T), want %#v (%T)", tc.in, got, got, tc.want, tc.want)
			}
		})
	}
}

// A snapshot of an int16 holds the int a later step gives back, and not another number.
func TestPropIndex_HoldsAnIntegerInAnotherKind(t *testing.T) {
	ix := indexOf(map[string]any{"replicas": int16(3), "ports": []uint16{80, 443}})
	for path, v := range map[string]any{"replicas": 3, "ports[0]": 80, "ports": []any{80, 443}} {
		if !ix.holds(path, v) {
			t.Errorf("holds(%q, %#v) = false, want true", path, v)
		}
	}
	for path, v := range map[string]any{"replicas": 4, "ports[1]": 80} {
		if ix.holds(path, v) {
			t.Errorf("holds(%q, %#v) = true, want false", path, v)
		}
	}
}
