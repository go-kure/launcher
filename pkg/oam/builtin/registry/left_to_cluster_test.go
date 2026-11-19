package registry_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/probe"
)

// No shipped probe lists a default as left to the cluster any more (the raw
// cronjob, which did, fills nothing: go-kure/launcher#1629), so the checks of
// that list run on a stand-in handler. The stand-in is the statefulset handler
// with one more property, `mode`, whose schema declares the default "Allow". An
// unauthored `mode` writes nothing to the objects (the cluster would apply the
// default); an authored one is written as an annotation on the StatefulSet.

const standInType = "stand-in"

type standInHandler struct{ components.StatefulsetHandler }

func (*standInHandler) CanHandle(componentType string) bool { return componentType == standInType }

func (h *standInHandler) PropertySchema() map[string]oam.PropertySchema {
	schema := h.StatefulsetHandler.PropertySchema()
	schema["mode"] = oam.PropertySchema{Type: "string", Default: "Allow", Description: "a default the handler leaves to the cluster"}
	return schema
}

func (h *standInHandler) ToApplicationConfig(c *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	inner := *c
	inner.Type = "statefulset"
	inner.Properties = maps.Clone(c.Properties)
	mode := inner.Properties["mode"]
	delete(inner.Properties, "mode")
	cfg, err := h.StatefulsetHandler.ToApplicationConfig(&inner, namespace)
	if err != nil {
		return nil, err
	}
	if s, ok := mode.(string); ok {
		cfg.(*components.StatefulsetConfig).Metadata.Annotations = map[string]string{"example.com/mode": s}
	}
	return cfg, nil
}

func standInTransformer() *oam.Transformer {
	return oam.NewTransformer(map[string]oam.ComponentHandler{standInType: &standInHandler{}}, nil)
}

func standInProbe(props map[string]any) probe.Probe {
	// The raw statefulset takes the upstream spec: a selector and a pod template
	// with one container, and a serviceName.
	p := rawDeploymentProps("app", "ghcr.io/example/app:1.0", map[string]any{"serviceName": "app-headless"})
	maps.Copy(p, props)
	return probe.Probe{
		Name:       "stand-in",
		Components: []oam.Component{{Name: "app", Type: standInType, Properties: p}},
	}
}

func checkDeclared(t *testing.T, p probe.Probe) string {
	t.Helper()
	return strings.Join(probe.CheckDeclaredDefaults(standInTransformer(), []probe.Probe{p}), "\n")
}

// Fault A: a declared default the handler does not apply. Writing "Allow" changes
// the objects, so without a LeftToCluster row the check names the property; with
// the row it is clean.
func TestProbes_FaultA_ADeclaredDefaultTheHandlerDoesNotApply(t *testing.T) {
	p := standInProbe(nil)
	if got := checkDeclared(t, p); !strings.Contains(got, "mode") || !strings.Contains(got, "LeftToCluster") {
		t.Fatalf("want a finding naming mode and LeftToCluster, got %q", got)
	}
	p.LeftToCluster = map[string]string{"mode": "the handler omits it and the cluster applies Allow"}
	if got := checkDeclared(t, p); got != "" {
		t.Fatalf("a listed row must be clean, got %q", got)
	}
}

// Check A reads an explicit null as omitted, as the pipeline does: a `mode: null`
// is still a declared default the probe leaves out.
func TestProbes_FaultA_ANullReadsAsOmitted(t *testing.T) {
	p := standInProbe(map[string]any{"mode": nil})
	if got := checkDeclared(t, p); !strings.Contains(got, "mode") {
		t.Fatalf("want a finding naming mode, got %q", got)
	}
	p.LeftToCluster = map[string]string{"mode": "the cluster applies Allow"}
	if got := checkDeclared(t, p); got != "" {
		t.Fatalf("a listed row must be clean with a null, got %q", got)
	}
}
