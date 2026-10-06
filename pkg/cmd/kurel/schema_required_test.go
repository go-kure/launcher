package kurel

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// schemaRequiredWitness is an accepted document of a component type that lacks
// a key the type's first accepted document (componentLabelFixtures) is refused
// without. It shows that the key has an alternative, so that the schema is
// right not to mark it Required.
type schemaRequiredWitness struct {
	// props returns the witness's properties, from the first document's.
	props func(t *testing.T, first map[string]any) map[string]any
	// policy is the environment policy the witness builds under, nil for none.
	policy oam.Policy
	// parseOnly, when set, is the reason the witness is shown on the handler's
	// parse (ToApplicationConfig) and is not built.
	parseOnly string
}

// schemaRequiredStoragePolicy is an environment policy with a storage default.
type schemaRequiredStoragePolicy struct{ *oam.NoopPolicy }

func (schemaRequiredStoragePolicy) DefaultStorageSize() string { return "1Gi" }

// without returns the first document's properties without key and with extra.
func without(key string, extra map[string]any) func(*testing.T, map[string]any) map[string]any {
	return func(_ *testing.T, first map[string]any) map[string]any {
		props := maps.Clone(first)
		delete(props, key)
		maps.Copy(props, extra)
		return props
	}
}

// servedAsURL returns the first document's `inline` manifest as a `url`
// served by a server local to the test.
func servedAsURL(t *testing.T, first map[string]any) map[string]any {
	t.Helper()
	inline, _ := first["inline"].(string)
	if inline == "" {
		t.Fatal("the first document holds no inline manifest to serve")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, inline)
	}))
	t.Cleanup(srv.Close)
	props := maps.Clone(first)
	delete(props, "inline")
	props["url"] = srv.URL + "/manifest.yaml"
	return props
}

// specAsSpecs moves a Cilium policy's one rule from `spec` to `specs`.
func specAsSpecs(_ *testing.T, first map[string]any) map[string]any {
	props := maps.Clone(first)
	delete(props, "spec")
	props["specs"] = []any{first["spec"]}
	return props
}

// schemaRequiredWitnesses holds, by component type and key, the accepted
// document without the key. A key the first document is refused without and
// that the schema does not mark Required has one here; no other key does.
var schemaRequiredWitnesses = map[string]map[string]schemaRequiredWitness{
	// One of `spec` and `specs` holds the rules.
	"cilium-networkpolicy":            {"spec": {props: specAsSpecs}},
	"cilium-clusterwidenetworkpolicy": {"spec": {props: specAsSpecs}},
	// One of `inline` and `url` is the manifest source.
	"crd":       {"inline": {props: servedAsURL}},
	"manifests": {"inline": {props: servedAsURL}},
	// One of `chart` and `chartRef` names the chart.
	"helmrelease": {"chart": {props: without("chart", map[string]any{
		"chartRef": map[string]any{"kind": "OCIRepository", "name": "example"}})}},
	// An OCI source names the chart in its URL.
	"helm": {"chart": {props: func(*testing.T, map[string]any) map[string]any {
		return map[string]any{"source": map[string]any{"url": "oci://registry.example.com/charts/app"}, "version": "0.1.0"}
	}}},
	"helmtemplate": {"chart": {
		props: func(*testing.T, map[string]any) map[string]any {
			return map[string]any{"source": map[string]any{"url": "oci://registry.example.com/charts/app"}, "version": "0.1.0"}
		},
		parseOnly: "helmtemplate renders its chart at build time, and a chart of an OCI source is pulled from a registry over TLS, which a test has no local one to serve",
	}},
	// A headless Service may list no port.
	"service": {"ports": {props: without("ports", map[string]any{"clusterIP": "None"})}},
	// An environment policy's storage default supplies the size.
	"persistentvolumeclaim": {"size": {props: without("size", nil),
		policy: schemaRequiredStoragePolicy{NoopPolicy: &oam.NoopPolicy{}}}},
}

// TestComponentSchemas_RequiredIsWhatTheTypeRefusesAbsent holds every
// registered component type's published schema to the type: a top-level
// property is Required in PropertySchema exactly when no accepted document of
// the type can leave it out (go-kure/launcher#790). A consumer publishes its
// schemas from PropertySchema, so a required key that is not marked lets its
// validator accept a component the transform refuses.
//
// Nothing here lists the required keys. A key the type refuses the absence of
// is in every accepted document of the type, so the one accepted document the
// registry already holds per type (componentLabelFixtures, which
// TestComponentLabelInvariant_FixturesCoverRegistry holds to the registry)
// shows them all: each of its top-level keys is left out in turn, and the
// document is transformed. The transform runs without
// ValidateAuthoredProperties, so a refusal is the type's own and not the
// schema's. A key the document is refused without is Required, or it has a
// witness: a second accepted document without it (schemaRequiredWitnesses).
//
// Limits. Top level only: a Required below the top level is the engine's to
// hold (checkNestedRequired). A key left out is absent, which is what an
// explicit null is read as. And a witness that cannot be built says why and
// is shown on the handler's parse instead.
func TestComponentSchemas_RequiredIsWhatTheTypeRefusesAbsent(t *testing.T) {
	type registered struct {
		handler oam.ComponentHandler
		schema  map[string]oam.PropertySchema
		hasOne  bool
	}
	types := map[string]registered{}
	for typ, h := range builtinComponentHandlers() {
		r := registered{handler: h}
		if p, ok := h.(oam.PropertySchemaProvider); ok {
			r.schema, r.hasOne = p.PropertySchema(), true
		}
		types[typ] = r
	}
	for typ, rule := range builtinComponentLoweringRules() {
		if _, both := types[typ]; both {
			t.Fatalf("%s is registered as a handler and as a lowering rule", typ)
		}
		r := registered{}
		if p, ok := rule.(oam.PropertySchemaProvider); ok {
			r.schema, r.hasOne = p.PropertySchema(), true
		}
		types[typ] = r
	}

	profile, err := oam.ParseClusterProfile([]byte(labelInvariantProfile))
	if err != nil {
		t.Fatalf("parsing the profile: %v", err)
	}
	build := func(t *testing.T, typ string, props map[string]any, policy oam.Policy) error {
		t.Helper()
		transformer := newBuiltinTransformer()
		evaluated, err := transformer.EvaluateProfile(profile)
		if err != nil {
			t.Fatalf("evaluating the profile: %v", err)
		}
		raw, err := yaml.Marshal(labelInvariantApp("fast", typ, props, "", nil))
		if err != nil {
			t.Fatalf("marshaling the application: %v", err)
		}
		app, err := oam.ParseWithExtraTypes(raw, nil, transformer.LowerableTypes())
		if err != nil {
			t.Fatalf("parsing the application: %v", err)
		}
		_, err = transformer.Transform(app, oam.TransformContext{Capabilities: evaluated.Spec.Capabilities, Domain: kurelDomain, Policy: policy})
		return err
	}

	ran, required, refused, witnessed := 0, 0, 0, 0
	for _, typ := range slices.Sorted(maps.Keys(types)) {
		t.Run(typ, func(t *testing.T) {
			ran++
			reg := types[typ]
			if !reg.hasOne {
				t.Fatalf("%s declares no PropertySchema, so nothing says which of its properties are required", typ)
			}
			fixture, ok := componentLabelFixtures[typ]
			if !ok {
				t.Fatalf("%s has no accepted document: add it to componentLabelFixtures", typ)
			}
			first := fixture.props
			if fixture.propsFor != nil {
				first = fixture.propsFor(t)
			}
			if err := build(t, typ, first, nil); err != nil {
				t.Fatalf("the accepted document does not build: %v", err)
			}

			witnesses := schemaRequiredWitnesses[typ]
			for _, key := range slices.Sorted(maps.Keys(reg.schema)) {
				if !reg.schema[key].Required {
					continue
				}
				required++
				if _, held := first[key]; !held {
					t.Errorf("%s is Required in the schema, and the accepted document builds without it", key)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(first)) {
				props := maps.Clone(first)
				delete(props, key)
				err := build(t, typ, props, nil)
				marked := reg.schema[key].Required
				witness, hasWitness := witnesses[key]
				switch {
				case err == nil && marked:
					t.Errorf("%s is Required in the schema, and the document builds without it", key)
				case err == nil && hasWitness:
					t.Errorf("%s has a witness, and the first document already builds without it: drop the witness", key)
				case err == nil:
					// Optional, and the document shows it.
				case marked && hasWitness:
					t.Errorf("%s is Required in the schema and has a witness that builds without it: one of the two is wrong", key)
				case marked:
					refused++
				case !hasWitness:
					t.Errorf("%s is not Required in the schema, and the type refuses the document without it (%v): mark it Required, or show an accepted document without it in schemaRequiredWitnesses", key, err)
				default:
					witnessed++
					alt := witness.props(t, first)
					if _, held := alt[key]; held {
						t.Fatalf("the witness for %s holds the key", key)
					}
					if witness.parseOnly != "" {
						if reg.handler == nil {
							t.Fatalf("the witness for %s is parse-only, and %s is no handler's type", key, typ)
						}
						if _, err := reg.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: typ, Properties: alt}, "default"); err != nil {
							t.Errorf("the witness for %s is refused by the parse: %v", key, err)
						}
						t.Logf("the witness for %s is shown on the parse only: %s", key, witness.parseOnly)
						continue
					}
					if err := build(t, typ, alt, witness.policy); err != nil {
						t.Errorf("the witness for %s does not build: %v", key, err)
					}
				}
			}
			for key := range witnesses {
				if _, held := first[key]; !held {
					t.Errorf("the witness for %s answers a key the first document does not hold", key)
				}
			}
		})
	}
	for typ := range schemaRequiredWitnesses {
		if _, ok := types[typ]; !ok {
			t.Errorf("schemaRequiredWitnesses names %s, which is no registered component type", typ)
		}
	}
	// Vacuity guards: the walk met required keys, showed each refused, and
	// showed every witness. They are for the whole registry, so a run that
	// -run narrows to some types does not reach them.
	if ran != len(types) {
		return
	}
	want := 0
	for _, byKey := range schemaRequiredWitnesses {
		want += len(byKey)
	}
	if required == 0 || refused != required || witnessed != want {
		t.Errorf("%d Required keys, %d shown refused when absent, %d of %d witnesses shown; want every Required key refused and every witness shown", required, refused, witnessed, want)
	}
}
