package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// generateDaemonSet runs the whole path — ToApplicationConfig, Generate — and
// returns the DaemonSet from the output.
func generateDaemonSet(t *testing.T, props map[string]any) *appsv1.DaemonSet {
	t.Helper()
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name:       "node-agent",
		Type:       "daemonset",
		Properties: props,
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("node-agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if ds, ok := (*obj).(*appsv1.DaemonSet); ok {
			return ds
		}
	}
	t.Fatal("expected DaemonSet in output")
	return nil
}

// TestDaemonsetHandler_GeneratedSpecFields asserts the DaemonSetSpec-level
// fields that actually land on the generated object. The parser tests stop at
// the config, so without this the single apply call carrying the projection
// into the output could be deleted with the suite still green.
func TestDaemonsetHandler_GeneratedSpecFields(t *testing.T) {
	ds := generateDaemonSet(t, map[string]any{
		"image":                "ghcr.io/org/agent:v1",
		"minReadySeconds":      30,
		"revisionHistoryLimit": 3,
		"updateStrategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"maxUnavailable": "25%"},
		},
	})

	if ds.Spec.MinReadySeconds != 30 {
		t.Errorf("MinReadySeconds = %d, want 30", ds.Spec.MinReadySeconds)
	}
	if ds.Spec.RevisionHistoryLimit == nil || *ds.Spec.RevisionHistoryLimit != 3 {
		t.Errorf("RevisionHistoryLimit = %v, want 3", ds.Spec.RevisionHistoryLimit)
	}
	if ds.Spec.UpdateStrategy.Type != appsv1.RollingUpdateDaemonSetStrategyType {
		t.Errorf("UpdateStrategy.Type = %q, want RollingUpdate", ds.Spec.UpdateStrategy.Type)
	}
	if ds.Spec.UpdateStrategy.RollingUpdate == nil {
		t.Fatal("UpdateStrategy.RollingUpdate is nil")
	}
	mu := ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable
	if mu == nil || mu.String() != "25%" {
		t.Errorf("MaxUnavailable = %v, want 25%%", mu)
	}
	if ds.Spec.UpdateStrategy.RollingUpdate.MaxSurge != nil {
		t.Errorf("MaxSurge = %v, want nil (unauthored)", ds.Spec.UpdateStrategy.RollingUpdate.MaxSurge)
	}
	// The selector stays builder-managed, and the generated one must still
	// match the template labels.
	if ds.Spec.Selector == nil || ds.Spec.Selector.MatchLabels["app"] != "node-agent" {
		t.Errorf("Selector = %v, want the builder's app=node-agent", ds.Spec.Selector)
	}
}

// TestDaemonsetHandler_GeneratedSpecFields_Unauthored: a document authoring none
// of the new keys produces the same output as before, so every field stays at
// whatever the constructor set.
func TestDaemonsetHandler_GeneratedSpecFields_Unauthored(t *testing.T) {
	ds := generateDaemonSet(t, map[string]any{"image": "ghcr.io/org/agent:v1"})

	if ds.Spec.MinReadySeconds != 0 {
		t.Errorf("MinReadySeconds = %d, want 0", ds.Spec.MinReadySeconds)
	}
	if ds.Spec.RevisionHistoryLimit != nil {
		t.Errorf("RevisionHistoryLimit = %v, want nil", ds.Spec.RevisionHistoryLimit)
	}
	if ds.Spec.UpdateStrategy.Type != "" || ds.Spec.UpdateStrategy.RollingUpdate != nil {
		t.Errorf("UpdateStrategy = %+v, want the constructor's zero value", ds.Spec.UpdateStrategy)
	}
}

// TestDaemonsetHandler_SelectorRejected: the selector is builder-managed, must
// equal the generated template labels and is immutable once created, so
// authoring it is refused rather than silently dropped.
func TestDaemonsetHandler_SelectorRejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "node-agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image":    "ghcr.io/org/agent:v1",
			"selector": map[string]any{"matchLabels": map[string]any{"app": "other"}},
		},
	}, "default")
	if err == nil {
		t.Fatal("ToApplicationConfig succeeded, want the selector to be rejected")
	}
	if !strings.Contains(err.Error(), "selector: not authorable") {
		t.Errorf("error = %q, want it to name the selector as not authorable", err.Error())
	}
}

// TestDaemonsetHandler_SchemaPublishesSpecFields: the handler's own schema must
// carry the new keys, not just the parser. A field parsed but unpublished is
// invisible to every schema consumer and to the generated API reference.
func TestDaemonsetHandler_SchemaPublishesSpecFields(t *testing.T) {
	s := (&components.DaemonsetHandler{}).PropertySchema()
	for _, key := range []string{"updateStrategy", "minReadySeconds", "revisionHistoryLimit"} {
		if _, ok := s[key]; !ok {
			t.Errorf("PropertySchema is missing %q", key)
		}
	}
}
