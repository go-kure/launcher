package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The non-RWX guard lives in one helper shared by webservice, worker and
// deployment, so correcting it to read a claim's whole access-mode set changes
// what the two older kinds build for a claim declaring
// `[ReadWriteOnce, ReadWriteMany]`. That is a real behaviour change on a
// pre-existing document, not only on the new kind, and it runs in the accepting
// direction: a build that failed now succeeds, and a strategy the author never
// wrote is no longer forced. These tests pin both halves for both kinds, so the
// change is observable rather than incidental — and so re-scoping the fix to
// `deployment` alone would fail here rather than pass quietly.

func rwxClaimProps(replicas int, modes ...string) map[string]any {
	authored := make([]any, len(modes))
	for i, m := range modes {
		authored[i] = m
	}
	return map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"replicas": replicas,
		"volumes": []any{
			map[string]any{
				"name":        "data",
				"type":        "pvc",
				"mountPath":   "/data",
				"size":        "1Gi",
				"accessModes": authored,
			},
		},
	}
}

// generateSharedKindDeployment is the webservice/worker counterpart of
// deployment_test.go's generateDeployment, which is bound to the new kind's own
// handler.
func generateSharedKindDeployment(t *testing.T, h sharedKindHandler, kind string, props map[string]any) *appsv1.Deployment {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			return dep
		}
	}
	t.Fatal("Deployment not found in output")
	return nil
}

// sharedKindHandler is the one method these tests need from a handler, named
// locally so the test does not depend on which wider interface the two kinds
// happen to share.
type sharedKindHandler interface {
	ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error)
}

func TestSharedNonRWXGuard_RWXCapableClaimIsNotConstrained(t *testing.T) {
	kinds := map[string]sharedKindHandler{
		"webservice": &components.WebserviceHandler{},
		"worker":     &components.WorkerHandler{},
	}
	for kind, h := range kinds {
		t.Run(kind+" keeps replicas above one", func(t *testing.T) {
			dep := generateSharedKindDeployment(t, h, kind, rwxClaimProps(2, "ReadWriteOnce", "ReadWriteMany"))
			if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
				t.Errorf("expected replicas=2, got %v", dep.Spec.Replicas)
			}
		})

		t.Run(kind+" leaves the strategy unset", func(t *testing.T) {
			// The pre-correction behaviour on a single-replica document was a
			// successful build carrying `strategy.type: Recreate`, so this is
			// the half that changes rendered output rather than turning an
			// error into an object.
			dep := generateSharedKindDeployment(t, h, kind, rwxClaimProps(1, "ReadWriteOnce", "ReadWriteMany"))
			if dep.Spec.Strategy.Type != "" {
				t.Errorf("expected no forced strategy, got %q", dep.Spec.Strategy.Type)
			}
		})

		t.Run(kind+" still constrains a ReadWriteOnce-only claim", func(t *testing.T) {
			// The control: without this the suite would pass just as well if
			// the guard stopped firing altogether.
			cfg, err := h.ToApplicationConfig(&oam.Component{
				Name: "app", Type: kind, Properties: rwxClaimProps(2, "ReadWriteOnce"),
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			if _, err := cfg.Generate(stack.NewApplication("app", "default", cfg)); err == nil {
				t.Error("expected a non-RWX claim to refuse replicas=2")
			}
		})

		t.Run(kind+" still forces Recreate on a ReadWriteOnce-only claim", func(t *testing.T) {
			dep := generateSharedKindDeployment(t, h, kind, rwxClaimProps(1, "ReadWriteOnce"))
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Errorf("expected Recreate, got %q", dep.Spec.Strategy.Type)
			}
		})
	}
}
