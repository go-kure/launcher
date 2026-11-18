package model

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// inputFieldDecision is the recorded decision for one oam.PropertySchema field on
// a model input: whether Load accepts its key there. body is the lines of an
// input, at the top level of the schema, that set the key.
type inputFieldDecision struct {
	accepted bool
	body     []string
}

// inputFieldDecisions holds a decision for every field of oam.PropertySchema, by
// its YAML key. A model input is decoded into that type, so a field added to it
// is writable on an input from that moment: TestLoad_InputSchemaFieldDecisions
// fails until a decision is recorded for it (go-kure/launcher#1580).
//
// boundedBy is refused: a bound is published where a test holds it, and nothing
// holds one a model document states. Which facts a model may declare for its
// inputs is an open point of go-kure/launcher#1580; every other key is accepted
// as it was before that issue.
var inputFieldDecisions = map[string]inputFieldDecision{
	"type":                       {accepted: true, body: []string{"type: string"}},
	"types":                      {accepted: true, body: []string{"types: [string, integer]"}},
	"description":                {accepted: true, body: []string{"type: string", "description: the image"}},
	"required":                   {accepted: true, body: []string{"type: string", "required: true"}},
	"default":                    {accepted: true, body: []string{"type: string", "default: a"}},
	"enum":                       {accepted: true, body: []string{"type: string", "enum: [a, b]"}},
	"properties":                 {accepted: true, body: []string{"type: object", "properties: {a: {type: string}}"}},
	"items":                      {accepted: true, body: []string{"type: array", "items: {type: string}"}},
	"additionalProperties":       {accepted: true, body: []string{"type: object", "additionalProperties: true"}},
	"additionalPropertiesSchema": {accepted: true, body: []string{"type: object", "additionalPropertiesSchema: {type: string}"}},
	"platformReserved":           {accepted: true, body: []string{"type: string", "platformReserved: true"}},
	"exclusive":                  {accepted: true, body: []string{"type: object", "properties: {a: {type: string}, b: {type: string}}", "exclusive: [{keys: [a, b]}]"}},
	"gate":                       {accepted: true, body: []string{"type: string", "gate: SomeGate"}},
	"gatedPaths":                 {accepted: true, body: []string{"type: object", "additionalProperties: true", "gatedPaths: {a: SomeGate}"}},
	"valueGatedPaths":            {accepted: true, body: []string{"type: object", "additionalProperties: true", "valueGatedPaths: {a: {v: SomeGate}}"}},
	"valueGates":                 {accepted: true, body: []string{"type: string", "enum: [a]", "valueGates: {a: SomeGate}"}},
	"boundedBy":                  {body: []string{"type: string", "boundedBy: [registry]"}},
}

// inputBlock is a spec.inputs block with one input, image, whose schema is body
// under the given nesting: "" for the input itself, "properties" for a property
// of it, "items" for its items.
func inputBlock(nesting string, body []string) string {
	indent, head := "      ", "  inputs:\n    image:\n"
	switch nesting {
	case "properties":
		indent, head = "          ", head+"      type: object\n      properties:\n        part:\n"
	case "items":
		indent, head = "        ", head+"      type: array\n      items:\n"
	}
	return head + indent + strings.Join(body, "\n"+indent) + "\n"
}

func TestLoad_InputSchemaFieldDecisions(t *testing.T) {
	typ := reflect.TypeOf(oam.PropertySchema{})
	fields := map[string]bool{}
	for i := range typ.NumField() {
		key, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		fields[key] = true
		if _, ok := inputFieldDecisions[key]; !ok {
			t.Errorf("oam.PropertySchema key %q has no decision in inputFieldDecisions: say whether a model input accepts it", key)
		}
	}
	paths := map[string]string{"": "spec.inputs.image", "properties": "spec.inputs.image.properties.part", "items": "spec.inputs.image.items"}
	for key, d := range inputFieldDecisions {
		if !fields[key] {
			t.Errorf("inputFieldDecisions has %q, which is no key of oam.PropertySchema", key)
			continue
		}
		for nesting, path := range paths {
			name := key + "/input"
			if nesting != "" {
				name = key + "/" + nesting
			}
			t.Run(name, func(t *testing.T) {
				m, err := loadNoPanic(withInputs(t, inputBlock(nesting, d.body)))
				switch {
				case d.accepted && err != nil:
					t.Errorf("an input that sets %q was refused, and the recorded decision accepts it: %v", key, err)
				case !d.accepted && err == nil:
					t.Errorf("an input that sets %q was accepted, and the recorded decision refuses it: %+v", key, m.Spec.Inputs)
				case !d.accepted && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), path+": ") || !strings.Contains(err.Error(), key)):
					t.Errorf("error %q must wrap ErrInvalid, name %s and say %s", err, path, key)
				}
			})
		}
	}

	// The refusal is by the key, not by what the list holds: an empty list is
	// refused too. A null is the key left out, as everywhere in the format.
	if _, err := loadNoPanic(withInputs(t, inputBlock("", []string{"type: string", "boundedBy: []"}))); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "spec.inputs.image: boundedBy") {
		t.Errorf("an empty boundedBy: error %v, want it to wrap ErrInvalid and name spec.inputs.image and boundedBy", err)
	}
	if _, err := loadNoPanic(withInputs(t, inputBlock("", []string{"type: string", "boundedBy:"}))); err != nil {
		t.Errorf("a null boundedBy is the key left out: %v", err)
	}
}
