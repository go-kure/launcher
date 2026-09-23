package kurel

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests in this file pin go-kure/launcher#325: an authored property declared as
// a string but written with another YAML type (`delivery: 123`, `valuesMode: true`)
// must fail the build, naming the property, rather than be coerced to "" by the
// handler's comma-ok read and then defaulted as though it were absent.
//
// The handlers themselves keep their comma-ok reads (`cfg.Delivery, _ =
// props["delivery"].(string)` in helmchart.go). The type check lives in one place,
// Transformer.ValidateAuthoredProperties, which `kurel build` runs on every authored
// component and trait before any handler sees it (build.go). That is option (b) of
// the issue — type-checking once, generically, against each handler's declared
// PropertySchema — and it is why these tests go through the build and the
// registered schemas rather than through a handler.

// TestBuildCommand_HelmchartNonStringProperty_Rejected is the issue's own case, end
// to end: every string-typed helmchart property written with a non-string value
// fails `kurel build` with the property's path and the type it actually got.
func TestBuildCommand_HelmchartNonStringProperty_Rejected(t *testing.T) {
	cases := []struct {
		key     string
		yamlVal string // as the author writes it, unquoted
		gotType string // the Go type yaml.v3 decodes it to
	}{
		{"chart", "123", "int"},
		{"version", "1.2", "float64"},
		{"delivery", "123", "int"},
		{"interval", "10", "int"},
		{"releaseName", "true", "bool"},
		{"targetNamespace", "7", "int"},
		{"valuesMode", "true", "bool"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			// chart and source are always present so the only defect in the
			// document is the one under test; a case overriding chart replaces it.
			props := map[string]string{"chart": "podinfo", tc.key: tc.yamlVal}
			var b strings.Builder
			for _, k := range slices.Sorted(maps.Keys(props)) {
				fmt.Fprintf(&b, "        %s: %s\n", k, props[k])
			}
			appYAML := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: podinfo
      type: helmchart
      properties:
` + b.String() + `        source:
          url: https://stefanprodan.github.io/podinfo
`
			dir := t.TempDir()
			appPath := writeTempFile(t, dir, "app.yaml", appYAML)
			profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

			cmd := NewKurelCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("%s: %s built successfully; want a type error\noutput:\n%s", tc.key, tc.yamlVal, out.String())
			}
			want := fmt.Sprintf(`component "podinfo" (type "helmchart"): properties.%s: expected string, got %s`, tc.key, tc.gotType)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not name the malformed property\n got: %v\nwant substring: %s", err, want)
			}
		})
	}
}

// TestBuiltinStringProperties_RejectNonStringOnAuthoredPath is the repo-wide sweep
// the issue asks for, done against the schemas rather than the handlers: every
// top-level property that any built-in component handler, trait handler or trait
// lowering rule declares as a string is rejected by ValidateAuthoredProperties when
// authored as an integer. It iterates the same registration maps newBuiltinTransformer
// registers from, so a handler added later is covered without editing this test.
//
// Each case is paired with a control on the same key holding a real string, which
// must not produce the type error — otherwise a check that rejected every value
// would pass the sweep by construction.
func TestBuiltinStringProperties_RejectNonStringOnAuthoredPath(t *testing.T) {
	transformer := newBuiltinTransformer()
	checked := 0

	check := func(t *testing.T, where string, build func(props map[string]any) *oam.Application, schema map[string]oam.PropertySchema) {
		t.Helper()
		for _, key := range sortedSchemaKeys(schema) {
			if schema[key].Type != oam.PropertyTypeString {
				continue
			}
			checked++
			want := fmt.Sprintf("properties.%s: expected string, got int", key)

			err := transformer.ValidateAuthoredProperties(build(map[string]any{key: 123}))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: authored %s: 123 was not rejected as a non-string\n got: %v\nwant substring: %s", where, key, err, want)
			}

			err = transformer.ValidateAuthoredProperties(build(map[string]any{key: "x"}))
			if err != nil && strings.Contains(err.Error(), "properties."+key+": expected string") {
				t.Errorf("%s: control: authored %s: \"x\" was rejected as a non-string: %v", where, key, err)
			}
		}
	}

	for name, h := range builtinComponentHandlers() {
		p, ok := h.(oam.PropertySchemaProvider)
		if !ok {
			continue // TestNewBuiltinTransformer_HandlerSchemaParity flags this.
		}
		check(t, "component "+name, func(props map[string]any) *oam.Application {
			return authoredApp(oam.Component{Name: "c", Type: name, Properties: props})
		}, p.PropertySchema())
	}

	// Traits need a host component; webservice with only its image is valid on its
	// own, so any error comes from the trait under test.
	traitSchemas := map[string]map[string]oam.PropertySchema{}
	for name, h := range builtinTraitHandlers() {
		if p, ok := h.(oam.PropertySchemaProvider); ok {
			traitSchemas[name] = p.PropertySchema()
		}
	}
	for name, r := range builtinTraitLoweringRules() {
		if p, ok := r.(oam.PropertySchemaProvider); ok {
			traitSchemas[name] = p.PropertySchema()
		}
	}
	for name, schema := range traitSchemas {
		check(t, "trait "+name, func(props map[string]any) *oam.Application {
			return authoredApp(oam.Component{
				Name:       "c",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx"},
				Traits:     []oam.Trait{{Type: name, Properties: props}},
			})
		}, schema)
	}

	// Guard against the sweep iterating nothing: helmchart alone declares seven
	// top-level string properties.
	if checked < 7 {
		t.Fatalf("swept only %d string-typed properties; the registration maps or schemas are not being read", checked)
	}
	t.Logf("swept %d top-level string-typed properties", checked)
}

func authoredApp(comp oam.Component) *oam.Application {
	return &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}}
}
