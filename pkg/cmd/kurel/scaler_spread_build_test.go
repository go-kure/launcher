package kurel

import (
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// scalerSpreadWant is the spread constraints for a pod count, reduced to the
// topology key and the unsatisfiable action: no constraint for 1 pod or fewer,
// the hostname key for 2 pods, and the zone key too for 3 or more.
func scalerSpreadWant(pods int32) []string {
	switch {
	case pods <= 1:
		return nil
	case pods == 2:
		return []string{"kubernetes.io/hostname/" + string(corev1.DoNotSchedule)}
	default:
		return []string{
			"kubernetes.io/hostname/" + string(corev1.DoNotSchedule),
			"topology.kubernetes.io/zone/" + string(corev1.ScheduleAnyway),
		}
	}
}

func scalerSpreadGot(cs []corev1.TopologySpreadConstraint) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.TopologyKey+"/"+string(c.WhenUnsatisfiable))
	}
	return out
}

// scalerSpreadDeployments returns every Deployment that apps generate.
func scalerSpreadDeployments(apps []oam.GeneratedApplication) []*appsv1.Deployment {
	var out []*appsv1.Deployment
	for _, a := range apps {
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			if dep, ok := (*p).(*appsv1.Deployment); ok {
				out = append(out, dep)
			}
		}
	}
	return out
}

// TestScalerSpread_BuiltinTransformerSpreadsByTheScalersMinimum runs the
// invariant through the transformer that kurel build uses: an authored document
// is parsed, validated, transformed and generated with the built-in handlers
// that the registry lists. A Deployment under a scaler has no spec.replicas and
// spreads by the scaler's minReplicas. A transformer that is built by hand in a
// test does not show a lost registry entry for the scaler, so this test is the
// one that does. A webservice and a worker synthesize their own topology-spread
// trait, so only the deployment kind authors one, in either order.
func TestScalerSpread_BuiltinTransformerSpreadsByTheScalersMinimum(t *testing.T) {
	const spread = "        - type: topology-spread\n          properties: {}\n"
	for _, kind := range []string{"deployment", "webservice", "worker"} {
		for _, minReplicas := range []int32{1, 2, 3} {
			for _, spreadFirst := range []bool{false, true} {
				if kind != "deployment" && spreadFirst {
					continue
				}
				name := fmt.Sprintf("%s/min%d/spreadFirst=%v", kind, minReplicas, spreadFirst)
				t.Run(name, func(t *testing.T) {
					scaler := fmt.Sprintf("        - type: scaler\n          properties:\n            minReplicas: %d\n            maxReplicas: 5\n", minReplicas)
					authored := scaler
					switch {
					case kind != "deployment":
					case spreadFirst:
						authored = spread + scaler
					default:
						authored = scaler + spread
					}
					props := "        image: ghcr.io/example/app:v1.0.0\n"
					if kind == "webservice" {
						props += "        port: 8080\n"
					}
					doc := helmNamesApp("    - name: app\n      type: " + kind + "\n      properties:\n" + props + "      traits:\n" + authored)
					_, apps := namingTransform(t, doc, oam.TransformContext{})
					deps := scalerSpreadDeployments(apps)
					if len(deps) != 1 {
						t.Fatalf("generated %d Deployments, want 1", len(deps))
					}
					dep := deps[0]
					if dep.Spec.Replicas != nil {
						t.Fatalf("spec.replicas = %d under a scaler, want it unset", *dep.Spec.Replicas)
					}
					got := scalerSpreadGot(dep.Spec.Template.Spec.TopologySpreadConstraints)
					if want := scalerSpreadWant(minReplicas); fmt.Sprint(got) != fmt.Sprint(want) {
						t.Errorf("spread constraints = %v, want %v (scaler minReplicas %d)", got, want, minReplicas)
					}
				})
			}
		}
	}
}
