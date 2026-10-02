package traits_test

import (
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// A role kind's synthesized claim is a `pvc` trait sub-application named
// `<component>-<volume>` (go-kure/launcher#702). Another component can carry
// that same name. The claim must not take that component's Deployment health
// check: placed in the role component's bundle, a component that depends on
// the role component would wait on itself. Each component gets exactly one
// check, in the bundle that holds the component's own application.
func TestRoleClaims_SubApplicationDoesNotTakeAComponentsHealthCheck(t *testing.T) {
	for _, kind := range []string{"webservice", "worker"} {
		t.Run(kind, func(t *testing.T) {
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec: oam.ApplicationSpec{Components: []oam.Component{
					{Name: "app", Type: kind, Properties: map[string]any{
						"image": "ghcr.io/org/app:v1",
						"volumes": []any{map[string]any{
							"name": "data", "type": "pvc", "mountPath": "/data", "size": "1Gi",
						}},
					}},
					{Name: "app-data", Type: "deployment", Properties: map[string]any{
						"image": "ghcr.io/org/other:v1",
					}},
				}},
			}
			cluster, err := nonRWXScalerTransformer().Transform(app, oam.TransformContext{})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			var checks []string
			var walkBundle func(b *stack.Bundle)
			walkBundle = func(b *stack.Bundle) {
				if b == nil {
					return
				}
				for _, hc := range b.HealthChecks {
					checks = append(checks, hc.Kind+"/"+hc.Name)
					// The bundle must hold a Deployment application of that name;
					// the claim sub-application named app-data does not count.
					held := slices.ContainsFunc(b.Applications, func(a *stack.Application) bool {
						return a.Name == hc.Name && !isClaimApp(t, a)
					})
					if !held {
						t.Errorf("bundle %q checks %s/%s but holds no such workload", b.Name, hc.Kind, hc.Name)
					}
				}
				for _, child := range b.Children {
					walkBundle(child)
				}
			}
			var walk func(node *stack.Node)
			walk = func(node *stack.Node) {
				walkBundle(node.Bundle)
				for _, child := range node.Children {
					walk(child)
				}
			}
			walk(cluster.Node)
			slices.Sort(checks)
			if want := []string{"Deployment/app", "Deployment/app-data"}; !slices.Equal(checks, want) {
				t.Errorf("health checks = %v, want %v", checks, want)
			}
		})
	}
}

// isClaimApp reports whether the application generates only a
// PersistentVolumeClaim.
func isClaimApp(t *testing.T, a *stack.Application) bool {
	t.Helper()
	objs, err := a.Generate()
	if err != nil {
		t.Fatalf("Generate %q: %v", a.Name, err)
	}
	if len(objs) == 0 {
		return false
	}
	for _, o := range objs {
		if (*o).GetObjectKind().GroupVersionKind().Kind != "PersistentVolumeClaim" {
			return false
		}
	}
	return true
}
