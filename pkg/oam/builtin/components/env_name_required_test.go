package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// An env entry without a name used to be skipped by parseEnv's own guard (a
// bare `continue`), so the variable vanished with no error. At the top level the
// published schema's `Required: true` on env[].name caught it first, but an env
// list nested inside an initContainers/sidecars entry is not reached by that
// check, and there the skip was the only thing standing — the container was
// emitted with the variable silently missing (go-kure/launcher#447).
//
// Each case runs the authored pipeline an author's document takes — schema
// validation first, then handler conversion — and asserts the outcome, not the
// layer that produces it: the top-level case is refused by the schema, the
// nested cases by the parser, and either is correct.

// buildWebservice runs a single webservice component through
// ValidateAuthoredProperties and then ToApplicationConfig, returning the first
// error either reports, or the converted config.
func buildWebservice(t *testing.T, props map[string]any) (*components.WebserviceConfig, error) {
	t.Helper()
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"webservice": &components.WebserviceHandler{},
	}, nil)
	comp := oam.Component{Name: "app", Type: "webservice", Properties: props}
	app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}}
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		return nil, err
	}
	cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(&comp, "default")
	if err != nil {
		return nil, err
	}
	ws, ok := cfg.(*components.WebserviceConfig)
	if !ok {
		t.Fatalf("ToApplicationConfig returned %T, want *components.WebserviceConfig", cfg)
	}
	return ws, nil
}

func nestedEnvProps(block string, env map[string]any) map[string]any {
	return map[string]any{
		"image": "ghcr.io/org/app:v1",
		block: []any{map[string]any{
			"name":  "helper",
			"image": "ghcr.io/org/helper:v1",
			"env":   []any{env},
		}},
	}
}

func TestEnvName_NestedMissingNameRejected(t *testing.T) {
	cases := map[string]map[string]any{
		"absent":     {"value": "x"},
		"empty":      {"name": "", "value": "x"},
		"null":       {"name": nil, "value": "x"},
		"wrong type": {"name": 7, "value": "x"},
	}
	for _, block := range []string{"initContainers", "sidecars"} {
		for label, env := range cases {
			t.Run(block+"/"+label, func(t *testing.T) {
				_, err := buildWebservice(t, nestedEnvProps(block, env))
				if err == nil {
					t.Fatalf("%s[0].env[0] with name %s built cleanly; the variable would be dropped", block, label)
				}
				if !strings.Contains(err.Error(), "env[0]") || !strings.Contains(err.Error(), "name") {
					t.Errorf("error does not name env[0].name: %v", err)
				}
			})
		}
	}
}

func TestEnvName_NestedNamedStillBuilds(t *testing.T) {
	cfg, err := buildWebservice(t, nestedEnvProps("initContainers", map[string]any{"name": "MODE", "value": "x"}))
	if err != nil {
		t.Fatalf("named nested env: %v", err)
	}
	if len(cfg.InitContainers) != 1 {
		t.Fatalf("InitContainers = %+v, want one entry", cfg.InitContainers)
	}
	env := cfg.InitContainers[0].Env
	if len(env) != 1 || env[0].Name != "MODE" || env[0].Value != "x" {
		t.Errorf("initContainers[0].Env = %+v, want [{MODE x}]", env)
	}
}

func TestEnvName_TopLevelMissingNameStillRejected(t *testing.T) {
	_, err := buildWebservice(t, map[string]any{
		"image": "ghcr.io/org/app:v1",
		"env":   []any{map[string]any{"value": "x"}},
	})
	if err == nil {
		t.Fatal("top-level env[0] without a name built cleanly")
	}
	if !strings.Contains(err.Error(), "env[0]") || !strings.Contains(err.Error(), "name") {
		t.Errorf("error does not name env[0].name: %v", err)
	}
}

// The schema checks that a top-level name is present and a string, not that it
// is non-empty, so `name: ""` passes validation and only the parser can refuse
// it. Before go-kure/launcher#447 it was skipped like the nested case.
func TestEnvName_TopLevelEmptyNameRejected(t *testing.T) {
	_, err := buildWebservice(t, map[string]any{
		"image": "ghcr.io/org/app:v1",
		"env":   []any{map[string]any{"name": "", "value": "x"}},
	})
	if err == nil {
		t.Fatal(`top-level env[0] with name "" built cleanly; the variable would be dropped`)
	}
	if !strings.Contains(err.Error(), "env[0]: name is required") {
		t.Errorf("error = %v, want it to name env[0]", err)
	}
}

// The schema layer is bypassed entirely by a caller that hands properties
// straight to a handler (a lowering rule's output, a Go consumer), so the
// parser must refuse the top-level case on its own as well.
func TestEnvName_TopLevelMissingNameRejectedByHandler(t *testing.T) {
	comp := &oam.Component{Name: "app", Type: "webservice", Properties: map[string]any{
		"image": "ghcr.io/org/app:v1",
		"env":   []any{map[string]any{"name": "KEEP", "value": "a"}, map[string]any{"value": "x"}},
	}}
	_, err := (&components.WebserviceHandler{}).ToApplicationConfig(comp, "default")
	if err == nil {
		t.Fatal("env[1] without a name converted cleanly")
	}
	if !strings.Contains(err.Error(), "env[1]: name is required") {
		t.Errorf("error = %v, want it to name env[1]", err)
	}
}
