package registry_test

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// These tests keep issue numbers out of the descriptions of the builtin
// properties (go-kure/launcher#1623). A consumer publishes each description
// verbatim, for example as JSON Schema text in a page or an editor tooltip, and
// an issue number tells its reader nothing about what the property means. The
// reference belongs in the Go comment beside the property, where a maintainer
// reads it.

// issueNumber matches a # followed by two or more digits, the number of an issue or
// pull request, and so the number of a qualified reference as well.
var issueNumber = regexp.MustCompile(`#[0-9]{2,}`)

// walkDescriptions calls visit for every non-empty description under s: its own,
// and those of its Properties, its Items and its AdditionalPropertiesSchema,
// each at its path. These are the links a consumer follows to publish a schema.
func walkDescriptions(s oam.PropertySchema, path string, visit func(path, description string)) {
	if s.Description != "" {
		visit(path, s.Description)
	}
	for _, key := range slices.Sorted(maps.Keys(s.Properties)) {
		walkDescriptions(s.Properties[key], path+"."+key, visit)
	}
	if s.Items != nil {
		walkDescriptions(*s.Items, path+"[]", visit)
	}
	if s.AdditionalPropertiesSchema != nil {
		walkDescriptions(*s.AdditionalPropertiesSchema, path+".*", visit)
	}
}

// builtinSchemas returns the property schema of every builtin component, trait
// and policy type, by "<kind> <type>", as the transformer publishes them
// (Transformer.HandlerSchemas): the handlers, and the lowering rules that claim a
// type of their own, with the properties the engine adds to a kind component.
func builtinSchemas() map[string]map[string]oam.PropertySchema {
	published := newTransformer().HandlerSchemas()
	schemas := map[string]map[string]oam.PropertySchema{}
	for kind, set := range map[string]map[string]map[string]oam.PropertySchema{
		"component": published.Components,
		"trait":     published.Traits,
		"policy":    published.Policies,
	} {
		for name, schema := range set {
			schemas[kind+" "+name] = schema
		}
	}
	return schemas
}

// No description of a builtin property names an issue. A failure names the
// handler and the path of the property, and the match.
func TestBuiltinDescriptionsCarryNoIssueNumber(t *testing.T) {
	schemas := builtinSchemas()
	for _, handler := range slices.Sorted(maps.Keys(schemas)) {
		for _, key := range slices.Sorted(maps.Keys(schemas[handler])) {
			walkDescriptions(schemas[handler][key], key, func(path, description string) {
				if m := issueNumber.FindString(description); m != "" {
					t.Errorf("%s %s: the description names an issue (%q); move the reference to the Go comment of the property", handler, path, m)
				}
			})
		}
	}
}

// The walk reaches every description. Its count equals the count of string-valued
// "description" keys in the schema marshalled to JSON, so a link the walk does not
// follow fails here.
func TestBuiltinDescriptionWalk_ReachesEveryDescription(t *testing.T) {
	schemas := builtinSchemas()
	if len(schemas) == 0 {
		t.Fatal("no builtin type publishes a schema")
	}
	total := 0
	for _, handler := range slices.Sorted(maps.Keys(schemas)) {
		walked, counted := 0, 0
		for _, key := range slices.Sorted(maps.Keys(schemas[handler])) {
			walkDescriptions(schemas[handler][key], key, func(string, string) { walked++ })
			raw, err := json.Marshal(schemas[handler][key])
			if err != nil {
				t.Fatalf("%s %s: %v", handler, key, err)
			}
			counted += strings.Count(string(raw), `"description":"`)
		}
		if walked != counted {
			t.Errorf("%s: the walk reads %d descriptions, the schema holds %d", handler, walked, counted)
		}
		total += walked
	}
	if total == 0 {
		t.Error("the builtin schemas hold no description at all")
	}
}

// A type that only a lowering rule claims is read too: its schema is published
// like a handler's, so a description there reaches a consumer the same way.
func TestBuiltinSchemas_IncludeLoweringRuleTypes(t *testing.T) {
	schemas := builtinSchemas()
	rules := 0
	for name, rule := range registry.ComponentLoweringRules() {
		if _, ok := rule.(oam.PropertySchemaProvider); ok {
			rules++
			if _, read := schemas["component "+name]; !read {
				t.Errorf("component lowering rule %q publishes a schema the guard does not read", name)
			}
		}
	}
	for name, rule := range registry.TraitLoweringRules() {
		if _, ok := rule.(oam.PropertySchemaProvider); ok {
			rules++
			if _, read := schemas["trait "+name]; !read {
				t.Errorf("trait lowering rule %q publishes a schema the guard does not read", name)
			}
		}
	}
	if rules == 0 {
		t.Error("no lowering rule publishes a schema: the case reads nothing")
	}
}

// The walk follows a description on the node itself, in a nested property, in
// Items and in AdditionalPropertiesSchema.
func TestWalkDescriptions_ReachesEveryLink(t *testing.T) {
	schema := oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "root",
		Properties: map[string]oam.PropertySchema{
			"nested": {Type: oam.PropertyTypeString, Description: "nested"},
			"list": {
				Type: oam.PropertyTypeArray, Description: "list",
				Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "item"},
			},
			"map": {
				Type: oam.PropertyTypeObject, Description: "map",
				AdditionalPropertiesSchema: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "value"},
			},
		},
	}
	var got []string
	walkDescriptions(schema, "p", func(path, _ string) { got = append(got, path) })
	want := []string{"p", "p.list", "p.list[]", "p.map", "p.map.*", "p.nested"}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

// The pattern matches a bare and a qualified reference, and no ordinary prose.
func TestIssueNumberPattern(t *testing.T) {
	for _, text := range []string{
		"see #12",                    // allow-ref: a fixture string
		"go-kure/launcher#1050",      // a qualified reference
		"decided in (go-kure/x#999)", // inside a parenthesis
	} {
		if !issueNumber.MatchString(text) {
			t.Errorf("%q is not matched", text)
		}
	}
	for _, text := range []string{
		"step #1",                        // allow-ref: a single digit is not a reference
		"issue 12",                       // no sigil
		"a # followed by text",           // no digits
		"/etc/alertmanager/config_out#a", // no digits after the sigil
	} {
		if issueNumber.MatchString(text) {
			t.Errorf("%q is matched", text)
		}
	}
}
