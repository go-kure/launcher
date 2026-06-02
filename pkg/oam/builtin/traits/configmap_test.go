package traits_test

import (
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// fluxNSCapture is a stub ApplicationConfig that implements SetFluxNamespace.
type fluxNSCapture struct {
	lastNS string
}

func (f *fluxNSCapture) SetFluxNamespace(ns string) { f.lastNS = ns }
func (f *fluxNSCapture) Generate(_ *stack.Application) ([]*client.Object, error) {
	return nil, nil
}

func TestConfigMapDecorator_SetFluxNamespace_Forwards(t *testing.T) {
	inner := &fluxNSCapture{}
	dec := &traits.ConfigMapDecorator{
		Inner:         inner,
		ConfigMapName: "my-config",
		MountPath:     "/etc/config",
	}

	setter, ok := any(dec).(interface{ SetFluxNamespace(string) })
	if !ok {
		t.Fatal("ConfigMapDecorator does not implement SetFluxNamespace")
	}

	setter.SetFluxNamespace("custom-flux")

	if inner.lastNS != "custom-flux" {
		t.Errorf("inner.lastNS = %q, want %q", inner.lastNS, "custom-flux")
	}
}

func TestConfigMapDecorator_SetFluxNamespace_NoopWhenInnerLacksInterface(t *testing.T) {
	inner := &cmStub{name: "app", namespace: "default"} // defined in pruneprotection_test.go
	dec := &traits.ConfigMapDecorator{
		Inner:         inner,
		ConfigMapName: "my-config",
		MountPath:     "/etc/config",
	}

	setter, ok := any(dec).(interface{ SetFluxNamespace(string) })
	if !ok {
		t.Fatal("ConfigMapDecorator does not implement SetFluxNamespace")
	}

	// Must not panic when inner doesn't implement the interface.
	setter.SetFluxNamespace("custom-flux")
}

func TestConfigMapHandler_Apply_NoMountPath(t *testing.T) {
	h := &traits.ConfigMapHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "configmap",
		Properties: map[string]any{
			"name": "my-config",
			"data": map[string]any{"key": "val"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle app, got %d", len(bundle.Applications))
	}
}

func TestTransform_FluxNamespace_ReachesHelmRelease(t *testing.T) {
	// Build a transformer with helmchart component handler and configmap trait handler.
	transformer := oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"helmchart": &components.HelmchartHandler{},
		},
		nil,
	)
	transformer.RegisterBuiltinTrait("configmap", &traits.ConfigMapHandler{})

	// OAM app: helmchart component + configmap trait WITHOUT mountPath.
	// Without mountPath the configmap trait adds a sibling ConfigMap app but does NOT
	// wrap the helmchart config, so postProcessFluxNamespace calls SetFluxNamespace
	// directly on HelmchartConfig — exercising the pipeline wiring.
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name: "metrics",
					Type: "helmchart",
					Properties: map[string]any{
						"chart": "kube-prometheus-stack",
						"source": map[string]any{
							"url": "https://prometheus-community.github.io/helm-charts",
						},
					},
					Traits: []oam.Trait{
						{
							Type: "configmap",
							Properties: map[string]any{
								"name": "metrics-config",
								"data": map[string]any{"key": "val"},
								// no mountPath — configmap adds sibling, does not wrap
							},
						},
					},
				},
			},
		},
	}

	cluster, err := transformer.Transform(app, oam.TransformContext{
		FluxNamespace: "custom-flux",
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	// Collect all apps from all leaf bundles.
	var allApps []*stack.Application
	var collectApps func(node *stack.Node)
	collectApps = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil && !node.Bundle.IsUmbrella() {
			allApps = append(allApps, node.Bundle.Applications...)
		}
		for _, child := range node.Children {
			collectApps(child)
		}
	}
	collectApps(cluster.Node)

	// Find the helmchart app, generate its resources, and assert namespaces.
	var found bool
	for _, bundleApp := range allApps {
		if bundleApp.Name != "metrics" {
			continue
		}
		objs, genErr := bundleApp.Config.Generate(bundleApp)
		if genErr != nil {
			t.Fatalf("Generate: %v", genErr)
		}
		for _, objPtr := range objs {
			obj := *objPtr
			switch obj.(type) {
			case *helmv2.HelmRelease, *sourcev1.HelmRepository:
				found = true
				if ns := obj.GetNamespace(); ns != "custom-flux" {
					t.Errorf("%T.Namespace = %q, want %q", obj, ns, "custom-flux")
				}
			}
		}
	}
	if !found {
		t.Error("no HelmRelease or HelmRepository found in cluster")
	}
}

// hcVetoConfig implements ApplicationConfig + autoHealthCheckEmitter and vetoes
// its auto health check (like a helmchart with delivery=template).
type hcVetoConfig struct{ fluxNSCapture }

func (c *hcVetoConfig) EmitsAutoHealthCheck() bool { return false }

func TestConfigMapDecorator_EmitsAutoHealthCheck_Forwards(t *testing.T) {
	dec := &traits.ConfigMapDecorator{Inner: &hcVetoConfig{}, ConfigMapName: "c", MountPath: "/etc/c"}
	e, ok := any(dec).(interface{ EmitsAutoHealthCheck() bool })
	if !ok {
		t.Fatal("ConfigMapDecorator does not implement EmitsAutoHealthCheck")
	}
	if e.EmitsAutoHealthCheck() {
		t.Error("expected veto (false) forwarded from inner template-delivery config")
	}
}

func TestConfigMapDecorator_EmitsAutoHealthCheck_DefaultsTrue(t *testing.T) {
	dec := &traits.ConfigMapDecorator{Inner: &cmStub{name: "app", namespace: "default"}, ConfigMapName: "c", MountPath: "/etc/c"}
	e := any(dec).(interface{ EmitsAutoHealthCheck() bool })
	if !e.EmitsAutoHealthCheck() {
		t.Error("expected default true when inner does not implement autoHealthCheckEmitter")
	}
}
