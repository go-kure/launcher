package kurel

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestBuiltinTopLevelRequiredKeysAreRefusedWhenAbsent is the walk go-kure/launcher#1463
// asks for. It enumerates, at run time and never by grep, every top-level Required
// key of every built-in schema: component handlers and rules, trait handlers and
// rules, and policies. For each key it authors a document that holds every other
// Required key and omits that one, and asks ValidateAuthoredProperties. The document
// must be refused, and the refusal must name the key.
//
// Nothing here is skipped. A key the generator cannot fill, or a document refused
// for another reason, fails the test with the key's path. The test also fails when
// the keys seen and the documents exercised differ, so a silent skip cannot pass.
func TestBuiltinTopLevelRequiredKeysAreRefusedWhenAbsent(t *testing.T) {
	tr := newBuiltinTransformer()
	carrier := requiredWalkCarrier(t)

	type entry struct {
		kind, name string
		schema     map[string]oam.PropertySchema
		build      func(props map[string]any) *oam.Application
	}
	var entries []entry
	add := func(kind, name string, h any, build func(props map[string]any) *oam.Application) {
		if p, ok := h.(oam.PropertySchemaProvider); ok {
			entries = append(entries, entry{kind, name, p.PropertySchema(), build})
		}
	}
	component := func(typ string) func(map[string]any) *oam.Application {
		return func(props map[string]any) *oam.Application {
			return &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
				{Name: "x", Type: typ, Properties: props},
			}}}
		}
	}
	trait := func(typ string) func(map[string]any) *oam.Application {
		return func(props map[string]any) *oam.Application {
			return &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
				{Name: "x", Type: carrier, Properties: map[string]any{}, Traits: []oam.Trait{{Type: typ, Properties: props}}},
			}}}
		}
	}
	policy := func(typ string) func(map[string]any) *oam.Application {
		return func(props map[string]any) *oam.Application {
			return &oam.Application{Spec: oam.ApplicationSpec{Policies: []oam.ApplicationPolicy{
				{Name: "x", Type: typ, Properties: props},
			}}}
		}
	}
	for name, h := range builtinComponentHandlers() {
		add("component", name, h, component(name))
	}
	for name, r := range builtinComponentLoweringRules() {
		add("component rule", name, r, component(name))
	}
	for name, h := range builtinTraitHandlers() {
		add("trait", name, h, trait(name))
	}
	for name, r := range builtinTraitLoweringRules() {
		add("trait rule", name, r, trait(name))
	}
	for name, h := range builtinPolicyHandlers() {
		add("policy", name, h, policy(name))
	}

	seen, exercised := 0, 0
	for _, e := range entries {
		required := requiredKeys(e.schema)
		for _, omitted := range required {
			seen++
			where := fmt.Sprintf("%s %s.%s", e.kind, e.name, omitted)
			props := map[string]any{}
			for _, k := range required {
				if k != omitted {
					props[k] = sampleValue(e.schema[k])
				}
			}
			_, err := tr.ValidateAuthoredProperties(e.build(props))
			if msg := requiredWalkVerdict(err, omitted); msg != "" {
				t.Errorf("%s: %s", where, msg)
			} else {
				exercised++
			}
		}
	}
	t.Logf("top-level Required keys seen %d, documents exercised %d", seen, exercised)
	if seen == 0 {
		t.Fatal("no top-level Required key found in any built-in schema: the walk reads nothing")
	}
	if seen != exercised {
		t.Errorf("keys seen %d, documents exercised %d: every key must be exercised", seen, exercised)
	}
}

// requiredWalkVerdict is the walk's check of one document that omits the key
// omitted: it returns "" when validation refused the document naming that key, and
// the failure text otherwise.
func requiredWalkVerdict(err error, omitted string) string {
	switch {
	case err == nil:
		return "authored without it, the document is accepted"
	case !strings.Contains(err.Error(), fmt.Sprintf("%q is required", omitted)):
		return fmt.Sprintf("refused, but not for the omitted key (generator gap or a wrong refusal): %v", err)
	}
	return ""
}

// TestRequiredWalkReportsAnUnrefusedKey is the control: the walk's own check
// (requiredWalkVerdict) must fail on an accepted document that omits a Required key,
// and on a refusal that does not name it. Without it a walk that reads nothing, or a
// check that always passes, would look green.
func TestRequiredWalkReportsAnUnrefusedKey(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"probe": requiredWalkProbe{}}, nil)
	app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
		{Name: "x", Type: "probe", Properties: map[string]any{}},
	}}}
	_, err := tr.ValidateAuthoredProperties(app)
	if err == nil {
		t.Fatal("the control handler's Required key was not refused: the validator accepts a document without it")
	}
	if msg := requiredWalkVerdict(err, "must"); msg != "" {
		t.Errorf("the walk's check fails a refusal that names the omitted key: %s", msg)
	}
	if msg := requiredWalkVerdict(nil, "must"); msg == "" {
		t.Error("the walk's check passes an accepted document that omits a Required key")
	}
	if msg := requiredWalkVerdict(errors.New("some other refusal"), "must"); msg == "" {
		t.Error("the walk's check passes a refusal that does not name the omitted key")
	}
}

type requiredWalkProbe struct{}

func (requiredWalkProbe) CanHandle(t string) bool { return t == "probe" }
func (requiredWalkProbe) ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}
func (requiredWalkProbe) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{"must": {Type: oam.PropertyTypeString, Required: true}}
}

// requiredWalkCarrier names a built-in component type with no top-level Required
// key, so a trait document under test is never refused for its component.
func requiredWalkCarrier(t *testing.T) string {
	t.Helper()
	for _, name := range sortedKeys(builtinComponentHandlers()) {
		if p, ok := any(builtinComponentHandlers()[name]).(oam.PropertySchemaProvider); ok && len(requiredKeys(p.PropertySchema())) == 0 {
			return name
		}
	}
	t.Fatal("no built-in component type is free of Required keys to carry a trait document")
	return ""
}

func requiredKeys(schema map[string]oam.PropertySchema) []string {
	var out []string
	for _, k := range sortedSchemaKeys(schema) {
		if schema[k].Required {
			out = append(out, k)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sampleValue builds a value that satisfies field's own shape, with every nested
// Required key filled. It checks presence rules only: a value the handler would
// refuse later does not matter to authored validation.
func sampleValue(field oam.PropertySchema) any {
	if len(field.Enum) > 0 {
		return field.Enum[0]
	}
	typ := field.Type
	if typ == "" && len(field.Types) > 0 {
		typ = field.Types[0]
	}
	switch typ {
	case oam.PropertyTypeString:
		return "x"
	case oam.PropertyTypeInteger:
		return 1
	case oam.PropertyTypeNumber:
		return 1.5
	case oam.PropertyTypeBoolean:
		return true
	case oam.PropertyTypeArray:
		return []any{}
	case oam.PropertyTypeObject:
		obj := map[string]any{}
		for _, k := range requiredKeys(field.Properties) {
			obj[k] = sampleValue(field.Properties[k])
		}
		return obj
	}
	return "x"
}
