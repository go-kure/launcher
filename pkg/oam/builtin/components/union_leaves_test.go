package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// unionLeafRule is a ComponentLoweringRule that emits one component of target
// carrying exactly props, so the emitted path validates the same property map an
// author would otherwise write directly.
type unionLeafRule struct {
	target string
	props  map[string]any
}

func (unionLeafRule) ComponentType() string { return "union-leaf-app" }

func (r unionLeafRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: r.target, Properties: r.props}}}, nil
}

func unionLeafTransformer() *oam.Transformer {
	return oam.NewTransformer(map[string]oam.ComponentHandler{
		"deployment":  &components.DeploymentHandler{},
		"daemonset":   &components.DaemonsetHandler{},
		"statefulset": &components.StatefulsetHandler{},
		"webservice":  &components.WebserviceHandler{},
	}, nil)
}

func unionLeafApp(compType string, props map[string]any) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "app", Type: compType, Properties: props,
		}}},
	}
}

// unionLeafAuthored runs what kurel build runs on an authored document —
// ValidateAuthoredProperties, then Transform — and returns the generated objects.
func unionLeafAuthored(compType string, props map[string]any) ([]client.Object, error) {
	tr := unionLeafTransformer()
	app := unionLeafApp(compType, props)
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		return nil, err
	}
	return unionLeafGenerate(tr, app)
}

// unionLeafEmitted has a lowering rule emit the same component, so the property
// map reaches the handler through emission validation instead.
func unionLeafEmitted(compType string, props map[string]any) ([]client.Object, error) {
	tr := unionLeafTransformer()
	tr.RegisterComponentLowering(unionLeafRule{target: compType, props: props})
	return unionLeafGenerate(tr, unionLeafApp("union-leaf-app", map[string]any{}))
}

func unionLeafGenerate(tr *oam.Transformer, app *oam.Application) ([]client.Object, error) {
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		return nil, err
	}
	var out []client.Object
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					return err
				}
				for _, o := range objs {
					out = append(out, *o)
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
	return out, walk(cluster.Node)
}

func firstOf[T client.Object](objs []client.Object) T {
	var zero T
	for _, o := range objs {
		if v, ok := o.(T); ok {
			return v
		}
	}
	return zero
}

// unionLeaf is one schema leaf published as a Types union (go-kure/launcher#383).
type unionLeaf struct {
	name     string
	compType string
	// props builds a fresh property map carrying v at the leaf; validation writes
	// normalized values back, so no map is shared between runs.
	props func(v any) map[string]any
	// strForm is the leaf's string spelling: a percentage, or a quantity.
	strForm string
	// rendered reads the leaf back off the generated objects.
	rendered func(objs []client.Object) string
}

func deploymentRollingUpdate(knobs map[string]any) map[string]any {
	return map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": knobs},
	}
}

func daemonSetRollingUpdate(knobs map[string]any) map[string]any {
	return map[string]any{
		"image":          "ghcr.io/org/agent:v1",
		"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": knobs},
	}
}

var unionLeaves = []unionLeaf{
	{
		name: "deployment strategy.rollingUpdate.maxUnavailable", compType: "deployment", strForm: "25%",
		props: func(v any) map[string]any { return deploymentRollingUpdate(map[string]any{"maxUnavailable": v}) },
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.Deployment](objs).Spec.Strategy.RollingUpdate.MaxUnavailable.String()
		},
	},
	{
		name: "deployment strategy.rollingUpdate.maxSurge", compType: "deployment", strForm: "25%",
		props: func(v any) map[string]any { return deploymentRollingUpdate(map[string]any{"maxSurge": v}) },
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.Deployment](objs).Spec.Strategy.RollingUpdate.MaxSurge.String()
		},
	},
	{
		name: "daemonset updateStrategy.rollingUpdate.maxUnavailable", compType: "daemonset", strForm: "25%",
		props: func(v any) map[string]any { return daemonSetRollingUpdate(map[string]any{"maxUnavailable": v}) },
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.DaemonSet](objs).Spec.UpdateStrategy.RollingUpdate.MaxUnavailable.String()
		},
	},
	{
		// A non-zero maxSurge requires maxUnavailable: 0 alongside it.
		name: "daemonset updateStrategy.rollingUpdate.maxSurge", compType: "daemonset", strForm: "25%",
		props: func(v any) map[string]any {
			return daemonSetRollingUpdate(map[string]any{"maxSurge": v, "maxUnavailable": 0})
		},
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.DaemonSet](objs).Spec.UpdateStrategy.RollingUpdate.MaxSurge.String()
		},
	},
	{
		name: "statefulset updateStrategy.rollingUpdate.maxUnavailable", compType: "statefulset", strForm: "25%",
		props: func(v any) map[string]any {
			return map[string]any{
				"image":          "ghcr.io/org/db:v1",
				"updateStrategy": map[string]any{"rollingUpdate": map[string]any{"maxUnavailable": v}},
			}
		},
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.StatefulSet](objs).Spec.UpdateStrategy.RollingUpdate.MaxUnavailable.String()
		},
	},
	{
		name: "resources.requests.cpu", compType: "webservice", strForm: "500m",
		props: func(v any) map[string]any {
			return map[string]any{
				"image": "ghcr.io/org/app:v1", "port": 8080,
				"resources": map[string]any{"requests": map[string]any{"cpu": v}},
			}
		},
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.Deployment](objs).Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().String()
		},
	},
	{
		name: "resources.requests.memory", compType: "webservice", strForm: "512Mi",
		props: func(v any) map[string]any {
			return map[string]any{
				"image": "ghcr.io/org/app:v1", "port": 8080,
				"resources": map[string]any{"requests": map[string]any{"memory": v}},
			}
		},
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.Deployment](objs).Spec.Template.Spec.Containers[0].Resources.Requests.Memory().String()
		},
	},
	{
		name: "volumeClaimTemplates[].resources.requests.storage", compType: "statefulset", strForm: "10Gi",
		props: func(v any) map[string]any {
			return map[string]any{
				"image": "ghcr.io/org/db:v1",
				"volumeClaimTemplates": []any{map[string]any{
					"name": "data", "mountPath": "/var/lib/data",
					"resources": map[string]any{"requests": map[string]any{"storage": v}},
				}},
			}
		},
		rendered: func(objs []client.Object) string {
			return firstOf[*appsv1.StatefulSet](objs).Spec.VolumeClaimTemplates[0].Spec.Resources.Requests.Storage().String()
		},
	},
}

// TestUnionLeaves_IntegerAndStringPassBothPaths is the acceptance check of
// go-kure/launcher#383 for every component leaf published as a Types union: the
// integer 2 and the leaf's string form both pass the authored path and the emitted
// path, and both render into the generated object unchanged.
func TestUnionLeaves_IntegerAndStringPassBothPaths(t *testing.T) {
	paths := []struct {
		name string
		run  func(string, map[string]any) ([]client.Object, error)
	}{
		{"authored", unionLeafAuthored},
		{"emitted", unionLeafEmitted},
	}
	for _, leaf := range unionLeaves {
		for _, p := range paths {
			for _, tc := range []struct {
				v    any
				want string
			}{{2, "2"}, {leaf.strForm, leaf.strForm}} {
				t.Run(leaf.name+"/"+p.name+"/"+tc.want, func(t *testing.T) {
					objs, err := p.run(leaf.compType, leaf.props(tc.v))
					if err != nil {
						t.Fatalf("%T(%v) rejected: %v", tc.v, tc.v, err)
					}
					if got := leaf.rendered(objs); got != tc.want {
						t.Errorf("rendered %q, want %q", got, tc.want)
					}
				})
			}
		}
	}
}

// TestUnionLeaves_NonMemberRejectedAtSchemaLayer: a value matching neither member
// is refused by property validation on both paths, before any handler parser sees
// it. While these leaves declared no type at all, a boolean passed validation and
// only the parser stood between it and the handler.
func TestUnionLeaves_NonMemberRejectedAtSchemaLayer(t *testing.T) {
	for _, leaf := range unionLeaves {
		for name, run := range map[string]func(string, map[string]any) ([]client.Object, error){
			"authored": unionLeafAuthored,
			"emitted":  unionLeafEmitted,
		} {
			t.Run(leaf.name+"/"+name, func(t *testing.T) {
				_, err := run(leaf.compType, leaf.props(true))
				if err == nil {
					t.Fatal("a boolean was accepted")
				}
				if !strings.Contains(err.Error(), "expected one of") {
					t.Errorf("error = %v, want the schema layer's union type error", err)
				}
			})
		}
	}
}
