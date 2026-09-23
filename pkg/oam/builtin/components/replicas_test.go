package components_test

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// replicaKinds is every component kind that reads `replicas`. They all share
// one checked reading of it (go-kure/launcher#393): before that, every kind
// but deployment fell back to the default when the value was not an integer,
// so `replicas: "3"` silently built one replica, and a negative value was
// carried through to an object the apiserver refuses.
var replicaKinds = []struct {
	name    string
	handler oam.ComponentHandler
	props   map[string]any
}{
	{"webservice", &components.WebserviceHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}},
	{"worker", &components.WorkerHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"deployment", &components.DeploymentHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"postgresql", &components.PostgresqlHandler{}, map[string]any{}},
}

func replicaProps(base map[string]any, replicas any, authored bool) map[string]any {
	props := make(map[string]any, len(base)+1)
	for k, v := range base {
		props[k] = v
	}
	if authored {
		props["replicas"] = replicas
	}
	return props
}

// generatedReplicas builds the kind, applies p when it is non-nil, and returns
// the replica count on the object it emits: Spec.Replicas on a
// Deployment/StatefulSet, Spec.Instances on a CNPG Cluster.
func generatedReplicas(t *testing.T, h oam.ComponentHandler, kind string, props map[string]any, p oam.Policy) int32 {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if p != nil {
		enforceable, ok := cfg.(oam.Enforceable)
		if !ok {
			t.Fatalf("%s config does not implement oam.Enforceable; the policy step would be silently skipped", kind)
		}
		if err := enforceable.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
	}
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, o := range objects {
		switch w := (*o).(type) {
		case *appsv1.Deployment:
			if w.Spec.Replicas == nil {
				t.Fatal("Deployment Spec.Replicas = nil, want a value")
			}
			return *w.Spec.Replicas
		case *appsv1.StatefulSet:
			if w.Spec.Replicas == nil {
				t.Fatal("StatefulSet Spec.Replicas = nil, want a value")
			}
			return *w.Spec.Replicas
		case *cnpgv1.Cluster:
			return int32(w.Spec.Instances)
		}
	}
	t.Fatalf("no replica-bearing object among %d generated", len(objects))
	return 0
}

// TestReplicas_RejectedOnEveryKind pins both rejections on every kind: a value
// that is not an integer names its type, a negative names the value. Removing
// either guard from the shared helper turns every row of that case red.
func TestReplicas_RejectedOnEveryKind(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"quoted string", "3", "replicas: must be an integer, got string"},
		{"boolean", true, "replicas: must be an integer, got bool"},
		{"fractional", 2.5, "replicas: must be an integer, got float64"},
		{"negative", -1, "replicas: must be >= 0, got -1"},
	}
	for _, k := range replicaKinds {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name:       "app",
					Type:       k.name,
					Properties: replicaProps(k.props, tc.value, true),
				}, "default")
				if err == nil {
					t.Fatalf("ToApplicationConfig(replicas: %#v) = nil error, want one containing %q", tc.value, tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
				}
			})
		}
	}
}

// TestReplicas_AcceptedOnEveryKind is the accepting side, including the
// unauthored default and an explicit null (read as absence, like every other
// components property), so a helper that rejected everything could not pass.
func TestReplicas_AcceptedOnEveryKind(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		authored bool
		want     int32
	}{
		{"unauthored defaults to 1", nil, false, 1},
		{"explicit null defaults to 1", nil, true, 1},
		{"authored int", 3, true, 3},
		{"authored whole float", float64(2), true, 2},
		{"zero", 0, true, 0},
	}
	for _, k := range replicaKinds {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				got := generatedReplicas(t, k.handler, k.name, replicaProps(k.props, tc.value, tc.authored), nil)
				if got != tc.want {
					t.Errorf("replicas = %d, want %d", got, tc.want)
				}
			})
		}
	}
}

// TestReplicas_PolicyDefaultRespectsPresenceOnEveryKind pins the authored flag
// the shared helper returns alongside the value. A policy default replaces
// only an unauthored count (absent or null). An authored count wins over it,
// including 0 and 1. 1 matters because it equals the intrinsic default, so a
// rule keyed on the value instead of on presence is visible only there.
func TestReplicas_PolicyDefaultRespectsPresenceOnEveryKind(t *testing.T) {
	policy := &stubPolicy{defaultReplicas: int32ptr(9)}
	cases := []struct {
		name     string
		value    any
		authored bool
		want     int32
	}{
		{"unauthored takes the policy default", nil, false, 9},
		{"explicit null takes the policy default", nil, true, 9},
		{"authored 1 wins over the policy default", 1, true, 1},
		{"authored 3 wins over the policy default", 3, true, 3},
		{"authored 0 wins over the policy default", 0, true, 0},
	}
	for _, k := range replicaKinds {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				got := generatedReplicas(t, k.handler, k.name, replicaProps(k.props, tc.value, tc.authored), policy)
				if got != tc.want {
					t.Errorf("replicas = %d, want %d", got, tc.want)
				}
			})
		}
	}
}
