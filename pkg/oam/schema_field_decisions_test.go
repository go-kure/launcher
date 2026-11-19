package oam

import (
	"reflect"
	"strings"
	"testing"
)

// flatSiteDecision is the recorded decision for one PropertySchema field at the
// two flat call sites (flatschema.go): whether a kurel parameter and a capability
// rendering property accept its key. sample is a YAML line that sets the key, for
// a key a site refuses.
type flatSiteDecision struct {
	kurel      bool
	capability bool
	sample     string
}

// flatSiteDecisions holds a decision for every field of PropertySchema, by its
// YAML key. A new field has none, and TestPropertySchemaFields_FlatSiteDecisions
// fails until one is recorded: a field is never accepted or refused at a flat
// site by accident (go-kure/launcher#1580).
var flatSiteDecisions = map[string]flatSiteDecision{
	"type":                       {kurel: true, capability: true},
	"description":                {kurel: true, capability: true},
	"required":                   {kurel: true, capability: true},
	"default":                    {kurel: true, capability: true},
	"types":                      {sample: `types: [string, integer]`},
	"enum":                       {sample: `enum: []`},
	"properties":                 {sample: `properties: {}`},
	"items":                      {sample: `items: null`},
	"additionalProperties":       {sample: `additionalProperties: false`},
	"additionalPropertiesSchema": {sample: `additionalPropertiesSchema: {type: string}`},
	"platformReserved":           {sample: `platformReserved: false`},
	"exclusive":                  {sample: `exclusive: []`},
	"gate":                       {sample: `gate: SomeGate`},
	"gatedPaths":                 {sample: `gatedPaths: {}`},
	"valueGatedPaths":            {sample: `valueGatedPaths: {}`},
	"valueGates":                 {sample: `valueGates: {}`},
	"boundedBy":                  {sample: `boundedBy: []`},
	"requiredWhen":               {sample: `requiredWhen: {key: mode}`},
}

// propertySchemaKeys returns the YAML key of every field of PropertySchema, and
// fails for a field whose JSON key differs: the two forms publish one vocabulary.
func propertySchemaKeys(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(PropertySchema{})
	keys := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		f := typ.Field(i)
		yamlKey, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		jsonKey, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if yamlKey == "" || yamlKey != jsonKey {
			t.Errorf("PropertySchema.%s: yaml key %q and json key %q, want one non-empty key for both", f.Name, yamlKey, jsonKey)
			continue
		}
		keys = append(keys, yamlKey)
	}
	return keys
}

func TestPropertySchemaFields_FlatSiteDecisions(t *testing.T) {
	fields := map[string]bool{}
	for _, key := range propertySchemaKeys(t) {
		fields[key] = true
		d, ok := flatSiteDecisions[key]
		if !ok {
			t.Errorf("PropertySchema key %q has no decision in flatSiteDecisions: say whether a kurel parameter and a capability rendering property accept it", key)
			continue
		}
		if _, allowed := kurelParamKeys[key]; allowed != d.kurel {
			t.Errorf("key %q: kurelParamKeys accepts it = %v, the recorded decision says %v", key, allowed, d.kurel)
		}
		if _, allowed := capabilityPropKeys[key]; allowed != d.capability {
			t.Errorf("key %q: capabilityPropKeys accepts it = %v, the recorded decision says %v", key, allowed, d.capability)
		}
		if (!d.kurel || !d.capability) && d.sample == "" {
			t.Errorf("key %q is refused at a flat site and has no sample line to prove it", key)
		}
	}
	for key := range flatSiteDecisions {
		if !fields[key] {
			t.Errorf("flatSiteDecisions has %q, which is no key of PropertySchema", key)
		}
	}

	// A refused key is refused in a document, not only left out of an allow-set.
	for key, d := range flatSiteDecisions {
		if d.sample == "" {
			continue
		}
		if !d.kurel {
			t.Run("kurel parameter/"+key, func(t *testing.T) {
				_, err := ParsePackage([]byte(packageWithParamField(d.sample)))
				if err == nil || !strings.Contains(err.Error(), `unsupported field "`+key+`"`) {
					t.Errorf("err = %v, want the parameter refused for the unsupported field %q", err, key)
				}
			})
		}
		if !d.capability {
			t.Run("capability rendering property/"+key, func(t *testing.T) {
				_, err := loadCapDef(t, "  rendering:\n    properties:\n      mode:\n        type: string\n        "+d.sample+"\n")
				if err == nil || !strings.Contains(err.Error(), `unsupported field "`+key+`"`) {
					t.Errorf("err = %v, want the property refused for the unsupported field %q", err, key)
				}
			})
		}
	}
}
