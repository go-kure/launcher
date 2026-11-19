package oam

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/yaml"
)

// RequiredWhen (go-kure/launcher#1625): PropertySchema.RequiredWhen makes one
// property mandatory while a sibling key is set, or set to a value.

// volumeLikeObject is an object "o" with a "type" string, a "csi" string required
// when type is "csi", a "devicePath" string and a "volumeMode" string required when
// devicePath is set, and a boolean "on" with "extra" required when on is true.
func volumeLikeObject() map[string]PropertySchema {
	return map[string]PropertySchema{"o": {
		Type: PropertyTypeObject,
		Properties: map[string]PropertySchema{
			"type":       {Type: PropertyTypeString, Enum: []any{"csi", "emptyDir"}},
			"csi":        {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "type", Equals: "csi"}},
			"devicePath": {Type: PropertyTypeString},
			"volumeMode": {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "devicePath"}},
			"on":         {Type: PropertyTypeBoolean},
			"extra":      {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "on", Equals: true}},
		},
	}}
}

func TestRequiredWhen_SchemaErrors(t *testing.T) {
	object := func(name string, rule *RequiredWhen, required bool) map[string]PropertySchema {
		return map[string]PropertySchema{"o": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"type": {Type: PropertyTypeString, Enum: []any{"csi", "emptyDir"}},
				"flag": {Type: PropertyTypeBoolean},
				"n":    {Type: PropertyTypeInteger},
				name:   {Type: PropertyTypeString, Required: required, RequiredWhen: rule},
			},
		}}
	}
	tests := []struct {
		name   string
		schema map[string]PropertySchema
		want   string
	}{
		{
			name:   "an undeclared key",
			schema: object("csi", &RequiredWhen{Key: "zz"}, false),
			want:   `properties.o: schema declares requiredWhen on "csi" naming key "zz", which is not a declared property`,
		},
		{
			name:   "the property itself",
			schema: object("csi", &RequiredWhen{Key: "csi"}, false),
			want:   `properties.o: schema declares requiredWhen on "csi" naming the property itself`,
		},
		{
			name:   "a Required property",
			schema: object("csi", &RequiredWhen{Key: "type", Equals: "csi"}, true),
			want:   `properties.o: schema declares "csi" Required and requiredWhen; a property is required always or conditionally, not both`,
		},
		{
			name:   "an equals that is neither a string nor a boolean",
			schema: object("csi", &RequiredWhen{Key: "n", Equals: 3}, false),
			want:   `properties.o: schema declares requiredWhen on "csi" with an equals of type int; equals is a string or a boolean`,
		},
		{
			name:   "an equals whose type is not the key's",
			schema: object("csi", &RequiredWhen{Key: "flag", Equals: "yes"}, false),
			want:   `properties.o: schema declares requiredWhen on "csi" with a string equals, but key "flag" is declared boolean`,
		},
		{
			name:   "an equals outside the key's enum",
			schema: object("csi", &RequiredWhen{Key: "type", Equals: "zip"}, false),
			want:   `properties.o: schema declares requiredWhen on "csi" with equals "zip", which is not one of key "type"'s enum values`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{"o": map[string]any{"type": "emptyDir"}}
			wantError(t, validateProperties(tc.schema, p, "properties"), tc.want)
			wantError(t, checkNestedRequired(tc.schema, nil, p, "properties", ""), tc.want)
			// A malformed rule is the schema's error, reported before the document's.
			q := map[string]any{"o": map[string]any{"type": "csi", "devicePath": "/x"}}
			wantError(t, validateProperties(tc.schema, q, "properties"), tc.want)
		})
	}

	t.Run("a rule on a top-level property", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"mode": {Type: PropertyTypeString},
			"path": {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "nope"}},
		}
		want := `properties: schema declares requiredWhen on "path" naming key "nope", which is not a declared property`
		wantError(t, validateProperties(schema, map[string]any{"mode": "x"}, "properties"), want)
		wantError(t, validateAuthoredTopLevel(schema, nil, map[string]any{"mode": "x"}, "properties"), want)
	})
}

// TestRequiredWhen_Validate: the rule against a document, at a nested object.
func TestRequiredWhen_Validate(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]any
		want string // "" accepts
	}{
		{name: "the condition does not hold", obj: map[string]any{"type": "emptyDir"}},
		{name: "the key is absent", obj: map[string]any{}},
		{name: "equals holds and the property is set", obj: map[string]any{"type": "csi", "csi": "driver"}},
		{
			name: "equals holds and the property is absent",
			obj:  map[string]any{"type": "csi"},
			want: `properties.o: "csi" is required when "type" is "csi"`,
		},
		{
			name: "equals holds and the property is a null",
			obj:  map[string]any{"type": "csi", "csi": nil},
			want: `properties.o: "csi" is required when "type" is "csi"`,
		},
		{name: "equals holds and the property is an empty string", obj: map[string]any{"type": "csi", "csi": ""}},
		{name: "the key is a null: the condition does not hold", obj: map[string]any{"type": nil}},
		{name: "a set key with no equals, the property set", obj: map[string]any{"devicePath": "/dev/x", "volumeMode": "Block"}},
		{
			name: "a set key with no equals, the property absent",
			obj:  map[string]any{"devicePath": "/dev/x"},
			want: `properties.o: "volumeMode" is required when "devicePath" is set`,
		},
		{
			name: "a set key with no equals, the property null",
			obj:  map[string]any{"devicePath": "/dev/x", "volumeMode": nil},
			want: `properties.o: "volumeMode" is required when "devicePath" is set`,
		},
		{name: "a null key with no equals: the condition does not hold", obj: map[string]any{"devicePath": nil}},
		{name: "an empty string key counts as set", obj: map[string]any{"devicePath": ""}, want: `properties.o: "volumeMode" is required when "devicePath" is set`},
		{
			name: "a boolean equals holds",
			obj:  map[string]any{"on": true},
			want: `properties.o: "extra" is required when "on" is true`,
		},
		{name: "a boolean equals does not hold", obj: map[string]any{"on": false}},
	}
	schema := volumeLikeObject()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{"o": tc.obj}
			err := validateProperties(schema, p, "properties")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			wantError(t, err, tc.want)
			var pe *PropertyError
			if !errors.As(err, &pe) || pe.Kind != PropertyErrorRequired {
				t.Fatalf("error kind = %v, want PropertyErrorRequired", err)
			}
		})
	}

	t.Run("the check follows the same set test after a merge", func(t *testing.T) {
		for _, tc := range tests {
			p := map[string]any{"o": tc.obj}
			err := checkNestedRequired(schema, nil, p, "properties", "")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("%s: unexpected error: %v", tc.name, err)
				}
				continue
			}
			wantError(t, err, tc.want)
		}
	})

	t.Run("the refusal names the capability after a merge", func(t *testing.T) {
		p := map[string]any{"o": map[string]any{"type": "csi"}}
		err := checkNestedRequired(schema, nil, p, "properties", "store")
		wantError(t, err, `properties.o: "csi" is required when "type" is "csi"; neither the trait nor capability "store"'s rendering sets it`)
	})
}

// TestRequiredWhen_ArrayItems: the rule is read per item.
func TestRequiredWhen_ArrayItems(t *testing.T) {
	item := volumeLikeObject()["o"]
	schema := map[string]PropertySchema{"vols": {
		Type:  PropertyTypeArray,
		Items: &item,
	}}
	p := map[string]any{"vols": []any{
		map[string]any{"type": "emptyDir"},
		map[string]any{"type": "csi"},
	}}
	want := `properties.vols[1]: "csi" is required when "type" is "csi"`
	wantError(t, validateProperties(schema, p, "properties"), want)
	wantError(t, checkNestedRequired(schema, nil, p, "properties", ""), want)

	ok := map[string]any{"vols": []any{map[string]any{"type": "csi", "csi": "d"}, map[string]any{}}}
	if err := validateProperties(schema, ok, "properties"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRequiredWhen_TopLevel: a rule on a top-level property of a handler.
func TestRequiredWhen_TopLevel(t *testing.T) {
	schema := map[string]PropertySchema{
		"kind": {Type: PropertyTypeString},
		"name": {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "kind", Equals: "named"}},
	}
	wantError(t, validateProperties(schema, map[string]any{"kind": "named"}, "properties"),
		`properties: "name" is required when "kind" is "named"`)
	if err := validateProperties(schema, map[string]any{"kind": "other"}, "properties"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// An authored document's top level enforces it as it enforces Required, and the
	// relaxed schema of a capability-bound trait does not: the rendering may supply it.
	want := `properties: "name" is required when "kind" is "named"`
	wantError(t, validateAuthoredTopLevel(schema, nil, map[string]any{"kind": "named"}, "properties"), want)
	if err := validateAuthoredTopLevel(relaxObjectRequired(schema), nil, map[string]any{"kind": "named"}, "properties"); err != nil {
		t.Fatalf("a relaxed schema must not enforce requiredWhen: %v", err)
	}
	wantError(t, checkNestedRequired(schema, nil, map[string]any{"kind": "named"}, "properties", ""), want)
}

// TestRequiredWhen_Relaxation: relaxation clears the rule on a copy and never
// mutates the handler's own schema.
func TestRequiredWhen_Relaxation(t *testing.T) {
	shared := volumeLikeObject()
	relaxed := relaxObjectRequired(shared)
	if rw := relaxed["o"].Properties["csi"].RequiredWhen; rw != nil {
		t.Errorf("relaxObjectRequired left RequiredWhen %+v on the copy", rw)
	}
	if shared["o"].Properties["csi"].RequiredWhen == nil {
		t.Error("relaxObjectRequired cleared RequiredWhen in the handler's own schema map")
	}
}

// TestRequiredWhen_Serialisation: the field round-trips through JSON and YAML under
// the name requiredWhen.
func TestRequiredWhen_Serialisation(t *testing.T) {
	in := PropertySchema{Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "type", Equals: "csi"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"type":"string","requiredWhen":{"key":"type","equals":"csi"}}`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
	var out PropertySchema
	if err := yaml.Unmarshal([]byte("type: string\nrequiredWhen:\n  key: devicePath\n"), &out); err != nil {
		t.Fatal(err)
	}
	if out.RequiredWhen == nil || out.RequiredWhen.Key != "devicePath" || out.RequiredWhen.Equals != nil {
		t.Errorf("yaml decode = %+v", out.RequiredWhen)
	}
}

type whenMode string

// TestRequiredWhen_EqualsAgainstTheKey: Equals is held to the key's declared type
// or union members, and to its Enum as a value is, by kind.
func TestRequiredWhen_EqualsAgainstTheKey(t *testing.T) {
	object := func(key PropertySchema, equals any) map[string]PropertySchema {
		return map[string]PropertySchema{"o": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"a": key,
				"b": {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "a", Equals: equals}},
			},
		}}
	}
	valid := map[string]any{"o": map[string]any{"a": "x", "b": "y"}}
	t.Run("an enum member of a named string type equals its string", func(t *testing.T) {
		schema := object(PropertySchema{Type: PropertyTypeString, Enum: []any{whenMode("x"), whenMode("y")}}, "x")
		if err := validateProperties(schema, valid, "properties"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("a union that lists the equals' type", func(t *testing.T) {
		schema := object(PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeInteger}}, "x")
		if err := validateProperties(schema, valid, "properties"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("a union that does not list the equals' type", func(t *testing.T) {
		schema := object(PropertySchema{Types: []PropertyType{PropertyTypeInteger, PropertyTypeBoolean}}, "x")
		wantError(t, validateProperties(schema, valid, "properties"),
			`properties.o: schema declares requiredWhen on "b" with a string equals, but key "a" is declared integer/boolean`)
	})
}

// whenDefaultsComponent takes "class" from a capability as a default.
type whenDefaultsComponent struct{ schema map[string]PropertySchema }

func (whenDefaultsComponent) CanHandle(t string) bool { return t == "wd" }
func (whenDefaultsComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}
func (h whenDefaultsComponent) PropertySchema() map[string]PropertySchema { return h.schema }
func (whenDefaultsComponent) CapabilityDefaults() (string, []string) {
	return "store", []string{"class"}
}

// TestRequiredWhen_ComponentCapabilityDefaults: a key a capability fills is not
// required of the author, as for Required, but a malformed rule on it is still the
// schema's error.
func TestRequiredWhen_ComponentCapabilityDefaults(t *testing.T) {
	schema := func(key string) map[string]PropertySchema {
		return map[string]PropertySchema{
			"mode":  {Type: PropertyTypeString},
			"class": {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: key}},
		}
	}
	h := func(key string) ComponentHandler { return whenDefaultsComponent{schema: schema(key)} }
	if err := validateAuthoredComponentAgainst(h("mode"), map[string]any{"mode": "x"}, "properties"); err != nil {
		t.Fatalf("a defaulted key is not required of the author: %v", err)
	}
	wantError(t, validateAuthoredComponentAgainst(h("missing"), map[string]any{"mode": "x"}, "properties"),
		`properties: schema declares requiredWhen on "class" naming key "missing", which is not a declared property`)
}

// TestCheckSchemaNodeNotProperty: a rule on a node with no sibling is refused, in the
// walk a validator makes and by the exported node check.
func TestCheckSchemaNodeNotProperty(t *testing.T) {
	rule := &RequiredWhen{Key: "a"}
	want := `schema declares requiredWhen on a node that is not a property of an object; a rule names a sibling key`
	wantError(t, CheckSchemaNodeNotProperty(PropertySchema{Type: PropertyTypeString, RequiredWhen: rule}, "x"), "x: "+want)
	if err := CheckSchemaNodeNotProperty(PropertySchema{Type: PropertyTypeString}, "x"); err != nil {
		t.Fatalf("a node with no rule: %v", err)
	}
	items := map[string]PropertySchema{"v": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString, RequiredWhen: rule}}}
	wantError(t, checkObjectGroups(items, nil, "properties"), "properties.v[]: "+want)
	open := map[string]PropertySchema{"v": {Type: PropertyTypeObject, AdditionalPropertiesSchema: &PropertySchema{Type: PropertyTypeString, RequiredWhen: rule}}}
	wantError(t, checkObjectGroups(open, nil, "properties"), "properties.v.*: "+want)
}

// TestCheckSchemaNodeGroups_RequiredWhen: the exported node check a model loader
// calls reports a malformed rule on the node's properties, and under array items.
func TestCheckSchemaNodeGroups_RequiredWhen(t *testing.T) {
	good := volumeLikeObject()["o"]
	if err := CheckSchemaNodeGroups(good, "properties.o"); err != nil {
		t.Fatalf("a well-formed node: %v", err)
	}
	bad := PropertySchema{
		Type: PropertyTypeObject,
		Properties: map[string]PropertySchema{
			"a": {Type: PropertyTypeString},
			"b": {Type: PropertyTypeString, RequiredWhen: &RequiredWhen{Key: "zz"}},
		},
	}
	wantError(t, CheckSchemaNodeGroups(bad, "properties.o"),
		`properties.o: schema declares requiredWhen on "b" naming key "zz", which is not a declared property`)

	// The walk a validator makes reaches a rule under an array's items.
	arr := map[string]PropertySchema{"v": {Type: PropertyTypeArray, Items: &bad}}
	wantError(t, checkObjectGroups(arr, nil, "properties"),
		`properties.v[]: schema declares requiredWhen on "b" naming key "zz", which is not a declared property`)
}
