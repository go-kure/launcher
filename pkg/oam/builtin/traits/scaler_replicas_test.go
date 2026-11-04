package traits_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
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

// The omission is part of how the kind builds the Deployment, so the output does
// not depend on which of the scaler and topology-spread traits is declared
// first: both orders build byte-identical objects, and spec.replicas is unset.
//
// The topology-spread trait derives its constraints from the generated
// Deployment's replica count, which is unset under a scaler, so it applies none
// there, even where an environment policy default of 3 would have given the
// Deployment three replicas and main emitted the constraints. That loss is
// tracked in go-kure/launcher#1196; the assertion below pins it until that
// issue lands and must then change with it.
func TestScaler_OutputIsIdenticalInBothOrdersWithTopologySpread(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	three := int32(3)
	for _, kind := range []string{"deployment", "webservice", "worker"} {
		for policyName, policy := range map[string]oam.Policy{
			"no policy":        nil,
			"policy default 3": &replicasPolicy{defaultReplicas: &three},
		} {
			t.Run(kind+"/"+policyName, func(t *testing.T) {
				if policy != nil && kind != "deployment" {
					// The webservice and worker kinds write their own spread at a
					// count above one, which the topology-spread trait refuses on
					// any component, scaled or not.
					t.Skip("the kind's own spread default already applies at this count")
				}
				props := map[string]any{"image": "ghcr.io/org/app:v1"}
				render := func(trs ...oam.Trait) (string, *appsv1.Deployment) {
					cluster, err := transformCluster(t, kind, props, policy, trs...)
					if err != nil {
						t.Fatalf("transform: %v", err)
					}
					out, err := json.Marshal(clusterObjects(t, cluster))
					if err != nil {
						t.Fatalf("marshal: %v", err)
					}
					return string(out), oneDeployment(t, cluster)
				}
				scalerFirst, depA := render(scaledDeploymentTrait(), spread)
				spreadFirst, depB := render(spread, scaledDeploymentTrait())
				if scalerFirst != spreadFirst {
					t.Errorf("output depends on trait order:\nscaler first:          %s\ntopology-spread first: %s", scalerFirst, spreadFirst)
				}
				for _, dep := range []*appsv1.Deployment{depA, depB} {
					if dep.Spec.Replicas != nil {
						t.Errorf("spec.replicas = %d, want it unset", *dep.Spec.Replicas)
					}
					if n := len(dep.Spec.Template.Spec.TopologySpreadConstraints); n != 0 {
						t.Errorf("%d spread constraints under a scaler, want none until go-kure/launcher#1196", n)
					}
				}
			})
		}
	}
}

// generateErr runs Generate on every application in the cluster and returns the
// first error, for the checks that run at Generate rather than at Transform.
func generateErr(cluster *stack.Cluster) error {
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				if _, err := a.Generate(); err != nil {
					return err
				}
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(cluster.Node)
}

// Under a scaler the Deployment's own count is unused (the HPA owns it), so the
// non-RWX replica check must not refuse an environment-policy default of 3 that
// no Deployment will carry. Before this the workaround was to author
// `replicas: 1`, which the scaler now refuses (go-kure/launcher#1179). The webservice
// and worker rows reach the Deployment through a lowering rule that forwards the
// scaler, so they prove the signal survives the fixpoint.
func TestScaler_NonRWXClaim_UnusedPolicyDefaultCountIsNotChecked(t *testing.T) {
	three := int32(3)
	policy := &replicasPolicy{defaultReplicas: &three}
	for _, kind := range []string{"deployment", "webservice", "worker"} {
		t.Run(kind, func(t *testing.T) {
			one := scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 1})
			cluster, err := transformCluster(t, kind, claimProps(kind, "ReadWriteOnce"), policy, one)
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if err := generateErr(cluster); err != nil {
				t.Fatalf("a scaled single-pod Deployment with a non-RWX claim was refused: %v", err)
			}
			dep := oneDeployment(t, cluster)
			if dep.Spec.Replicas != nil {
				t.Errorf("spec.replicas = %d, want it unset", *dep.Spec.Replicas)
			}
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Errorf("strategy.type = %q, want Recreate: the claim's strategy guard stays", dep.Spec.Strategy.Type)
			}

			// Control: without the scaler the default count is the Deployment's
			// own, and the claim refuses it.
			cluster, err = transformCluster(t, kind, claimProps(kind, "ReadWriteOnce"), policy)
			if err != nil {
				t.Fatalf("transform without scaler: %v", err)
			}
			err = generateErr(cluster)
			if err == nil || !strings.Contains(err.Error(), "at most one replica") {
				t.Errorf("without a scaler: error = %v, want the non-RWX replica refusal", err)
			}

			// Control: the HPA-bound check still guards the pod count.
			two := scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 2})
			err = transformOne(t, kind, claimProps(kind, "ReadWriteOnce"), policy, two)
			if err == nil || !strings.Contains(err.Error(), nonRWXClaimName(kind)) {
				t.Errorf("maxReplicas 2 with a claim: error = %v, want the scaler refusal naming the claim", err)
			}
		})
	}
}
