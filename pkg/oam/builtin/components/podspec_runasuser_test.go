package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// generateErr runs the full ToApplicationConfig -> Generate path and returns
// whichever error either half produced, so a test can assert on a rejection
// rather than fail on it the way generateKind does.
func generateErr(t *testing.T, h oam.ComponentHandler, kind string, props map[string]any) error {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		return err
	}
	_, err = cfg.Generate(stack.NewApplication("app", "default", cfg))
	return err
}

// TestEffectiveRunAsUser checks that the runAsUser-0/runAsNonRoot contradiction
// is judged on each container's effective settings, not on the pod-level object
// in isolation.
//
// A container-level runAsUser overrides the pod-level value, which the schema
// states explicitly, so pod-level {runAsUser: 0, runAsNonRoot: true} is only a
// defect for containers that do not override it. Deciding at parse time
// rejected the valid document as well; deciding in buildPodSpec also catches
// the mixed cases neither per-object parser can see.
func TestEffectiveRunAsUser(t *testing.T) {
	rootPod := map[string]any{"runAsUser": 0, "runAsNonRoot": true}

	t.Run("container override makes the pod-level pair valid", func(t *testing.T) {
		for _, k := range workloadKinds {
			t.Run(k.name, func(t *testing.T) {
				props := withProps(k.props, map[string]any{
					"podSecurityContext": rootPod,
					"securityContext":    map[string]any{"runAsUser": 1000},
				})
				ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
				main := ps.Containers[0]
				if main.SecurityContext == nil || main.SecurityContext.RunAsUser == nil || *main.SecurityContext.RunAsUser != 1000 {
					t.Fatalf("main container runAsUser = %v, want 1000", main.SecurityContext)
				}
				if ps.SecurityContext == nil || ps.SecurityContext.RunAsUser == nil || *ps.SecurityContext.RunAsUser != 0 {
					t.Errorf("pod-level runAsUser did not survive: %v", ps.SecurityContext)
				}
			})
		}
	})

	t.Run("no override is rejected and names the container", func(t *testing.T) {
		for _, k := range workloadKinds {
			t.Run(k.name, func(t *testing.T) {
				err := generateErr(t, k.handler, k.name, withProps(k.props, map[string]any{
					"podSecurityContext": rootPod,
				}))
				if err == nil {
					t.Fatal("expected an error when the main container inherits runAsUser 0 under runAsNonRoot")
				}
				if !strings.Contains(err.Error(), "containers[0]") ||
					!strings.Contains(err.Error(), "effective runAsUser must not be 0") {
					t.Errorf("error = %q, want it to name containers[0] and the effective runAsUser", err.Error())
				}
			})
		}
	})

	t.Run("container-level runAsUser 0 under a pod-level runAsNonRoot is rejected", func(t *testing.T) {
		err := generateErr(t, &components.WebserviceHandler{}, "webservice", map[string]any{
			"image":              "ghcr.io/org/app:v1",
			"port":               8080,
			"podSecurityContext": map[string]any{"runAsNonRoot": true},
			"securityContext":    map[string]any{"runAsUser": 0},
		})
		if err == nil {
			t.Fatal("expected an error for a container-level runAsUser 0 under a pod-level runAsNonRoot")
		}
		if !strings.Contains(err.Error(), "containers[0]") {
			t.Errorf("error = %q, want it to name containers[0]", err.Error())
		}
	})

	t.Run("init container inheriting the pod-level pair is rejected", func(t *testing.T) {
		err := generateErr(t, &components.WebserviceHandler{}, "webservice", map[string]any{
			"image":              "ghcr.io/org/app:v1",
			"port":               8080,
			"podSecurityContext": rootPod,
			"securityContext":    map[string]any{"runAsUser": 1000},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"},
			},
		})
		if err == nil {
			t.Fatal("expected an error when an init container inherits runAsUser 0 under runAsNonRoot")
		}
		if !strings.Contains(err.Error(), "initContainers[0]") {
			t.Errorf("error = %q, want it to name initContainers[0]", err.Error())
		}
	})
}
