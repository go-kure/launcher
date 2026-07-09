package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestPassthroughConfig_ComponentName verifies the passthrough component config
// exposes its owning OAM component through the oam.ComponentNamed accessor.
func TestPassthroughConfig_ComponentName(t *testing.T) {
	h := &components.PassthroughHandler{}
	cfg, err := h.ToApplicationConfig(passthroughComponent(map[string]any{
		"object": map[string]any{
			"apiVersion": "sparkoperator.k8s.io/v1beta2",
			"kind":       "SparkApplication",
			"spec":       map[string]any{"mode": "cluster"},
		},
	}), "data")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	named, ok := cfg.(oam.ComponentNamed)
	if !ok {
		t.Fatalf("%T does not implement oam.ComponentNamed", cfg)
	}
	if got := named.ComponentName(); got != "my-res" {
		t.Errorf("ComponentName() = %q, want \"my-res\"", got)
	}
}
