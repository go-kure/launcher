package traits_test

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// Authored `topologySpreadConstraints` on webservice and worker (go-kure/launcher#1473,
// ruled in go-kure/launcher#1252): while `topologySpread` is true the
// synthesized topology-spread trait refuses them with its own text; with
// `topologySpread: false` they build and nothing is synthesized.

func authoredSpread() []any {
	return []any{map[string]any{
		"maxSkew": 2, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway",
	}}
}

func roleHandlers() map[string]oam.ComponentHandler {
	return map[string]oam.ComponentHandler{"webservice": webserviceViaRule{}, "worker": workerViaRule{}}
}

func TestRolePlacement_AuthoredConstraintsWithSpreadOnRefused(t *testing.T) {
	// The trait counts what the Deployment carries, so the refusal text
	// quotes the authored count.
	const refusal = `topology-spread: Deployment "app" already carries 1 topologySpreadConstraints (authored on the component, or from its own topologySpread default); remove them, or set topologySpread: false, or drop the trait`
	for typ, h := range roleHandlers() {
		t.Run(typ, func(t *testing.T) {
			comp := &oam.Component{Name: "app", Type: typ, Properties: map[string]any{
				"image": "ghcr.io/org/app:v1", "replicas": 3, "topologySpreadConstraints": authoredSpread(),
			}}
			_, err := generateWithTopologySpreadless(t, h, comp)
			if err == nil || !strings.Contains(err.Error(), refusal) {
				t.Errorf("err = %v, want the trait refusal %q", err, refusal)
			}
		})
	}
}

func TestRolePlacement_AuthoredConstraintsWithSpreadOffBuild(t *testing.T) {
	want := []corev1.TopologySpreadConstraint{{
		MaxSkew: 2, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: corev1.ScheduleAnyway,
	}}
	for typ, h := range roleHandlers() {
		t.Run(typ, func(t *testing.T) {
			comp := &oam.Component{Name: "app", Type: typ, Properties: map[string]any{
				"image": "ghcr.io/org/app:v1", "replicas": 3, "topologySpread": false, "topologySpreadConstraints": authoredSpread(),
			}}
			got, err := generateWithTopologySpreadless(t, h, comp)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("constraints = %v, want only the authored %v", got, want)
			}
		})
	}
}
