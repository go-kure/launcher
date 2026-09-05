package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// roleKinds are the two role-named kinds that project appsv1.Deployment
// alongside the kind-named `deployment` component. go-kure/launcher#341 gave
// them the same DeploymentSpec-level surface, so the properties below are
// asserted on both rather than on whichever one a test happened to pick.
var roleKinds = []struct {
	name    string
	handler oam.ComponentHandler
}{
	{"webservice", &components.WebserviceHandler{}},
	{"worker", &components.WorkerHandler{}},
}

// roleKindDeployment builds one role-kind config and returns the Deployment it
// generates, or the first error from either step. Both are returned rather
// than fataling, because half these cases are about the refusal.
func roleKindDeployment(t *testing.T, h oam.ComponentHandler, kind string, props map[string]any) (*appsv1.Deployment, error) {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		return nil, err
	}
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		return nil, err
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			return dep, nil
		}
	}
	t.Fatalf("%s generated no Deployment", kind)
	return nil, nil
}

// TestRoleKinds_DeploymentSpecPropertiesReachTheDeployment: every field the
// shared fragment publishes is projected onto the generated object. Authoring
// all five at once also pins that they do not overwrite one another.
func TestRoleKinds_DeploymentSpecPropertiesReachTheDeployment(t *testing.T) {
	for _, k := range roleKinds {
		t.Run(k.name, func(t *testing.T) {
			dep, err := roleKindDeployment(t, k.handler, k.name, map[string]any{
				"image":                   "ghcr.io/org/app:v1",
				"minReadySeconds":         10,
				"revisionHistoryLimit":    4,
				"paused":                  true,
				"progressDeadlineSeconds": 700,
				"strategy": map[string]any{
					"type": "RollingUpdate",
					"rollingUpdate": map[string]any{
						"maxUnavailable": "25%",
						"maxSurge":       2,
					},
				},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
				t.Errorf("Strategy.Type = %q, want RollingUpdate", dep.Spec.Strategy.Type)
			}
			if dep.Spec.Strategy.RollingUpdate == nil {
				t.Fatal("Strategy.RollingUpdate is nil, want the authored block")
			}
			if got := dep.Spec.Strategy.RollingUpdate.MaxUnavailable; got == nil || got.StrVal != "25%" {
				t.Errorf("maxUnavailable = %v, want \"25%%\"", got)
			}
			if got := dep.Spec.Strategy.RollingUpdate.MaxSurge; got == nil || got.IntValue() != 2 {
				t.Errorf("maxSurge = %v, want 2", got)
			}
			if dep.Spec.MinReadySeconds != 10 {
				t.Errorf("MinReadySeconds = %d, want 10", dep.Spec.MinReadySeconds)
			}
			if dep.Spec.RevisionHistoryLimit == nil || *dep.Spec.RevisionHistoryLimit != 4 {
				t.Errorf("RevisionHistoryLimit = %v, want 4", dep.Spec.RevisionHistoryLimit)
			}
			if !dep.Spec.Paused {
				t.Error("Paused = false, want true")
			}
			if dep.Spec.ProgressDeadlineSeconds == nil || *dep.Spec.ProgressDeadlineSeconds != 700 {
				t.Errorf("ProgressDeadlineSeconds = %v, want 700", dep.Spec.ProgressDeadlineSeconds)
			}
		})
	}
}

// TestRoleKinds_DeploymentSpecUnauthoredLeavesTheConstructorValue: nothing in
// the fragment writes when the document is silent, so adding it to these kinds
// cannot move an existing package's output.
func TestRoleKinds_DeploymentSpecUnauthoredLeavesTheConstructorValue(t *testing.T) {
	for _, k := range roleKinds {
		t.Run(k.name, func(t *testing.T) {
			dep, err := roleKindDeployment(t, k.handler, k.name, map[string]any{"image": "ghcr.io/org/app:v1"})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if dep.Spec.Strategy.Type != "" {
				t.Errorf("Strategy.Type = %q, want the constructor's empty value", dep.Spec.Strategy.Type)
			}
			if dep.Spec.MinReadySeconds != 0 {
				t.Errorf("MinReadySeconds = %d, want 0", dep.Spec.MinReadySeconds)
			}
			if dep.Spec.RevisionHistoryLimit != nil {
				t.Errorf("RevisionHistoryLimit = %v, want nil", dep.Spec.RevisionHistoryLimit)
			}
			if dep.Spec.Paused {
				t.Error("Paused = true, want false")
			}
			if dep.Spec.ProgressDeadlineSeconds != nil {
				t.Errorf("ProgressDeadlineSeconds = %v, want nil", dep.Spec.ProgressDeadlineSeconds)
			}
		})
	}
}

// TestRoleKinds_PausedVetoesAutoHealthCheck: the transform pipeline reaches
// these configs by type assertion, so the assertion is the load-bearing half —
// a method whose name or signature drifts stops being seen there without any
// call site failing to compile.
func TestRoleKinds_PausedVetoesAutoHealthCheck(t *testing.T) {
	for _, k := range roleKinds {
		for _, tc := range []struct {
			name  string
			props map[string]any
			want  bool
		}{
			{"paused true vetoes the check", map[string]any{"image": "nginx:1.27", "paused": true}, false},
			{"paused false keeps it", map[string]any{"image": "nginx:1.27", "paused": false}, true},
			{"paused unauthored keeps it", map[string]any{"image": "nginx:1.27"}, true},
		} {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: tc.props}, "default")
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				e, ok := cfg.(interface{ EmitsAutoHealthCheck() bool })
				if !ok {
					t.Fatalf("%s config does not satisfy the autoHealthCheckEmitter shape the transform asserts on", k.name)
				}
				if got := e.EmitsAutoHealthCheck(); got != tc.want {
					t.Errorf("EmitsAutoHealthCheck() = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// TestRoleKinds_NonRWXConstraint: these kinds now share the deployment kind's
// guard instead of carrying their own inline copy. The forced Recreate and the
// replica ceiling are the pre-existing behaviour; the refusal of a
// contradicting authored strategy is new, and can only be new, because
// `strategy` was not authorable on these kinds before.
func TestRoleKinds_NonRWXConstraint(t *testing.T) {
	for _, k := range roleKinds {
		t.Run(k.name+"/unauthored strategy is forced to Recreate", func(t *testing.T) {
			dep, err := roleKindDeployment(t, k.handler, k.name, nonRWXVolumeProps(nil))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Errorf("Strategy.Type = %q, want Recreate for a non-RWX PVC", dep.Spec.Strategy.Type)
			}
		})

		t.Run(k.name+"/an authored RollingUpdate is refused", func(t *testing.T) {
			_, err := roleKindDeployment(t, k.handler, k.name, nonRWXVolumeProps(map[string]any{
				"strategy": map[string]any{"type": "RollingUpdate"},
			}))
			if err == nil {
				t.Fatal("build error = nil, want a refusal for RollingUpdate with a non-RWX PVC")
			}
			if !strings.Contains(err.Error(), "strategy.type must be Recreate") {
				t.Errorf("build error = %v, want the non-RWX strategy refusal", err)
			}
		})

		t.Run(k.name+"/an authored Recreate is accepted", func(t *testing.T) {
			dep, err := roleKindDeployment(t, k.handler, k.name, nonRWXVolumeProps(map[string]any{
				"strategy": map[string]any{"type": "Recreate"},
			}))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Errorf("Strategy.Type = %q, want Recreate", dep.Spec.Strategy.Type)
			}
		})

		t.Run(k.name+"/more than one replica is refused", func(t *testing.T) {
			_, err := roleKindDeployment(t, k.handler, k.name, nonRWXVolumeProps(map[string]any{"replicas": 2}))
			if err == nil {
				t.Fatal("build error = nil, want the replica refusal for a non-RWX PVC")
			}
			if !strings.Contains(err.Error(), "at most one replica") {
				t.Errorf("build error = %v, want the non-RWX replica refusal", err)
			}
		})

		// Scale-to-zero holds the claim in no pod at all, so the count is
		// accepted — but the strategy half of the guard still runs, since a
		// later edit setting replicas back to 1 touches neither the volume nor
		// the strategy and would not re-examine the pair.
		t.Run(k.name+"/scale-to-zero keeps the strategy half of the guard", func(t *testing.T) {
			dep, err := roleKindDeployment(t, k.handler, k.name, nonRWXVolumeProps(map[string]any{"replicas": 0}))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Errorf("Strategy.Type = %q, want Recreate at replicas: 0", dep.Spec.Strategy.Type)
			}
			if _, err := roleKindDeployment(t, k.handler, k.name, nonRWXVolumeProps(map[string]any{
				"replicas": 0,
				"strategy": map[string]any{"type": "RollingUpdate"},
			})); err == nil {
				t.Fatal("build error = nil, want the RollingUpdate refusal to survive scale-to-zero")
			}
		})
	}
}

// TestRoleKinds_DeploymentSpecSchemaPublished: the fragment is composed into
// both handlers' PropertySchema, so an authored property is accepted by the
// document validator rather than refused as unknown.
func TestRoleKinds_DeploymentSpecSchemaPublished(t *testing.T) {
	want := []string{"strategy", "minReadySeconds", "revisionHistoryLimit", "paused", "progressDeadlineSeconds"}
	for _, k := range roleKinds {
		t.Run(k.name, func(t *testing.T) {
			schema := k.handler.(oam.PropertySchemaProvider).PropertySchema()
			for _, key := range want {
				s, ok := schema[key]
				if !ok {
					t.Errorf("PropertySchema() is missing %q", key)
					continue
				}
				if s.Description == "" {
					t.Errorf("PropertySchema()[%q] has an empty description", key)
				}
			}
		})
	}
}

// TestRoleKinds_BuilderManagedFieldsStillRefused: the fragment's refusals
// travel with it, so `selector` and `template` earn their explaining error on
// these kinds too rather than being accepted and ignored.
func TestRoleKinds_BuilderManagedFieldsStillRefused(t *testing.T) {
	for _, k := range roleKinds {
		for _, key := range []string{"selector", "template"} {
			t.Run(k.name+"/"+key, func(t *testing.T) {
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name:       "app",
					Type:       k.name,
					Properties: map[string]any{"image": "ghcr.io/org/app:v1", key: map[string]any{}},
				}, "default")
				if err == nil {
					t.Fatalf("ToApplicationConfig error = nil, want %q to be refused", key)
				}
				if !strings.HasPrefix(err.Error(), key+":") {
					t.Errorf("ToApplicationConfig error = %v, want it to start with %q", err, key+":")
				}
			})
		}
	}
}
