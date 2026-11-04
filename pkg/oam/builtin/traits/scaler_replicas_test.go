package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The HorizontalPodAutoscaler owns the replica count of the Deployment it
// scales, so a scaler builds that Deployment without spec.replicas
// (go-kure/launcher#1179). A component that authors `replicas` beside a scaler
// is refused at validation (pkg/oam/scaler_replicas_validate_test.go); these
// tests cover what is left: the count the kind or the environment policy would
// have written.

func scaledDeploymentTrait() oam.Trait {
	return scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 5})
}

// clusterObjects runs Generate on every application in the cluster.
func clusterObjects(t *testing.T, cluster *stack.Cluster) []client.Object {
	t.Helper()
	var out []client.Object
	var walk func(node *stack.Node)
	walk = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, o := range objs {
					out = append(out, *o)
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cluster.Node)
	return out
}

func generatedDeployments(t *testing.T, cluster *stack.Cluster) []*appsv1.Deployment {
	t.Helper()
	var deps []*appsv1.Deployment
	for _, o := range clusterObjects(t, cluster) {
		if dep, ok := o.(*appsv1.Deployment); ok {
			deps = append(deps, dep)
		}
	}
	return deps
}

// oneDeployment returns the only Deployment the cluster generates.
func oneDeployment(t *testing.T, cluster *stack.Cluster) *appsv1.Deployment {
	t.Helper()
	deps := generatedDeployments(t, cluster)
	if len(deps) != 1 {
		t.Fatalf("generated %d Deployments, want 1", len(deps))
	}
	return deps[0]
}

func TestScaler_OmitsSpecReplicasOnTheScaledDeployment(t *testing.T) {
	props := map[string]any{"image": "ghcr.io/org/app:v1"}
	for _, kind := range []string{"webservice", "worker", "deployment"} {
		t.Run(kind, func(t *testing.T) {
			cluster, err := transformCluster(t, kind, props, nil, scaledDeploymentTrait())
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := oneDeployment(t, cluster).Spec.Replicas; got != nil {
				t.Errorf("spec.replicas = %d under a scaler, want it unset", *got)
			}
			if hpas := generatedHPAs(t, cluster); len(hpas) != 1 {
				t.Errorf("generated %d HPAs, want 1", len(hpas))
			}

			// Control: the same component without a scaler keeps the kind's default.
			cluster, err = transformCluster(t, kind, props, nil)
			if err != nil {
				t.Fatalf("transform without scaler: %v", err)
			}
			got := oneDeployment(t, cluster).Spec.Replicas
			if got == nil || *got != 1 {
				t.Errorf("spec.replicas = %v without a scaler, want 1", got)
			}
		})
	}
}

func TestScaler_OmitsAnEnvironmentPolicyDefaultCount(t *testing.T) {
	three := int32(3)
	policy := &replicasPolicy{defaultReplicas: &three}
	props := map[string]any{"image": "ghcr.io/org/app:v1"}

	cluster, err := transformCluster(t, "webservice", props, policy, scaledDeploymentTrait())
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := oneDeployment(t, cluster).Spec.Replicas; got != nil {
		t.Errorf("spec.replicas = %d under a scaler with a policy default, want it unset", *got)
	}

	// Control: the policy default still reaches a Deployment no scaler scales.
	cluster, err = transformCluster(t, "webservice", props, policy)
	if err != nil {
		t.Fatalf("transform without scaler: %v", err)
	}
	if got := oneDeployment(t, cluster).Spec.Replicas; got == nil || *got != 3 {
		t.Errorf("spec.replicas = %v without a scaler, want the policy default 3", got)
	}
}

// The scaler keeps the rest of the component's output: a webservice still
// generates its Service and ServiceAccount.
func TestScaler_OmittingReplicasKeepsTheComponentsOtherObjects(t *testing.T) {
	cluster, err := transformCluster(t, "webservice",
		map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}, nil, scaledDeploymentTrait())
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	kinds := map[string]int{}
	for _, o := range clusterObjects(t, cluster) {
		switch o.(type) {
		case *appsv1.Deployment:
			kinds["Deployment"]++
		case *corev1.Service:
			kinds["Service"]++
		case *corev1.ServiceAccount:
			kinds["ServiceAccount"]++
		}
	}
	for _, kind := range []string{"Deployment", "Service", "ServiceAccount"} {
		if kinds[kind] != 1 {
			t.Errorf("generated %d %s objects, want 1 (all: %v)", kinds[kind], kind, kinds)
		}
	}
}

// topology-spread reads the generated Deployment's replica count, so the strip
// must not make the output depend on which of the two traits is declared first:
// both orders build, and both leave spec.replicas unset.
func TestScaler_ReplicasAreUnsetInBothOrdersWithTopologySpread(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	orders := map[string][]oam.Trait{
		"scaler first":          {scaledDeploymentTrait(), spread},
		"topology-spread first": {spread, scaledDeploymentTrait()},
	}
	three := int32(3)
	for name, trs := range orders {
		for _, policy := range []oam.Policy{nil, &replicasPolicy{defaultReplicas: &three}} {
			t.Run(name, func(t *testing.T) {
				cluster, err := transformCluster(t, "deployment",
					map[string]any{"image": "ghcr.io/org/app:v1"}, policy, trs...)
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if got := oneDeployment(t, cluster).Spec.Replicas; got != nil {
					t.Errorf("spec.replicas = %d, want it unset", *got)
				}
			})
		}
	}
}

// twoDeploymentConfig generates two Deployments, the one the scaler targets
// (named like the application) and another.
type twoDeploymentConfig struct{}

func (twoDeploymentConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	one := int32(4)
	mk := func(name string) *client.Object {
		var o client.Object = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: app.Namespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &one},
		}
		return &o
	}
	return []*client.Object{mk(app.Name), mk(app.Name + "-other")}, nil
}

// Only the Deployment the HPA targets loses its count; a handler applied
// outside a transform (no trait naming) targets the application's name.
func TestScaler_OnlyTheTargetedDeploymentLosesItsCount(t *testing.T) {
	app := stack.NewApplication("api", "default", twoDeploymentConfig{})
	bundle := &stack.Bundle{}
	trait := scaledDeploymentTrait()
	if err := (&traits.ScalerHandler{}).Apply(&trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := map[string]*int32{}
	for _, o := range objs {
		dep := (*o).(*appsv1.Deployment)
		got[dep.Name] = dep.Spec.Replicas
	}
	if got["api"] != nil {
		t.Errorf("the targeted Deployment kept spec.replicas = %d", *got["api"])
	}
	if got["api-other"] == nil || *got["api-other"] != 4 {
		t.Errorf("the other Deployment's spec.replicas = %v, want 4 untouched", got["api-other"])
	}
}
