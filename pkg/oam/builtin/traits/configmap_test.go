package traits_test

import (
	"slices"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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
	dec := traits.NewConfigMapDecorator(inner, "my-config", "/etc/config")

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
	dec := traits.NewConfigMapDecorator(inner, "my-config", "/etc/config")

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

// TestConfigMapHandler_Apply_SizeLimit pins the trait to the API server's 1 MiB
// ConfigMap limit (corev1.MaxSecretSize), summed over the stringified data
// values the ConfigMap stores.
func TestConfigMapHandler_Apply_SizeLimit(t *testing.T) {
	limit := corev1.MaxSecretSize
	cases := []struct {
		name    string
		data    map[string]any
		wantErr string
	}{
		{
			name: "one value at the limit",
			data: map[string]any{"k": strings.Repeat("a", limit)},
		},
		{
			name:    "one value a byte over",
			data:    map[string]any{"k": strings.Repeat("a", limit+1)},
			wantErr: `configmap trait "my-config": data and binaryData hold 1048577 bytes, over the 1048576-byte limit`,
		},
		{
			name:    "two values summing a byte over",
			data:    map[string]any{"a": strings.Repeat("a", limit/2), "b": strings.Repeat("b", limit/2+1)},
			wantErr: "hold 1048577 bytes, over the 1048576-byte limit",
		},
		{
			// 12345 is stored as the five-byte string "12345".
			name:    "a stringified number counted as stored",
			data:    map[string]any{"n": 12345, "s": strings.Repeat("a", limit-4)},
			wantErr: "hold 1048577 bytes, over the 1048576-byte limit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &traits.ConfigMapHandler{}
			app := stack.NewApplication("myapp", "default", nil)
			bundle := &stack.Bundle{}
			trait := &oam.Trait{
				Type:       "configmap",
				Properties: map[string]any{"name": "my-config", "data": tc.data},
			}
			err := h.Apply(trait, app, bundle)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				if len(bundle.Applications) != 1 {
					t.Fatalf("expected 1 bundle app, got %d", len(bundle.Applications))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Apply error = %v, want one containing %q", err, tc.wantErr)
			}
			if len(bundle.Applications) != 0 {
				t.Fatalf("refused trait still added %d bundle app(s)", len(bundle.Applications))
			}
		})
	}
}

// TestConfigMapHandler_Apply_KeyValidity pins the trait to the keys the API
// server admits in a ConfigMap (IsConfigMapKey), checked before the size limit
// with the configmap component's message.
func TestConfigMapHandler_Apply_KeyValidity(t *testing.T) {
	cases := []struct {
		name    string
		data    map[string]any
		wantErr string
	}{
		{
			name: "valid keys",
			data: map[string]any{"config.yaml": "a: 1", "APP_ENV": "prod", "key-name": "v"},
		},
		{
			name:    "a slash",
			data:    map[string]any{"a/b": "v"},
			wantErr: `configmap trait "my-config": data: invalid key "a/b": `,
		},
		{
			name:    "254 characters",
			data:    map[string]any{strings.Repeat("a", 254): "v"},
			wantErr: `data: invalid key "` + strings.Repeat("a", 254) + `"`,
		},
		{
			name:    "dot-dot",
			data:    map[string]any{"..": "v"},
			wantErr: `data: invalid key ".."`,
		},
		{
			name:    "key checked before size",
			data:    map[string]any{"a/b": strings.Repeat("a", corev1.MaxSecretSize+1)},
			wantErr: `data: invalid key "a/b"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &traits.ConfigMapHandler{}
			app := stack.NewApplication("myapp", "default", nil)
			bundle := &stack.Bundle{}
			trait := &oam.Trait{
				Type:       "configmap",
				Properties: map[string]any{"name": "my-config", "data": tc.data},
			}
			err := h.Apply(trait, app, bundle)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				if len(bundle.Applications) != 1 {
					t.Fatalf("expected 1 bundle app, got %d", len(bundle.Applications))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Apply error = %v, want one containing %q", err, tc.wantErr)
			}
			if len(bundle.Applications) != 0 {
				t.Fatalf("refused trait still added %d bundle app(s)", len(bundle.Applications))
			}
		})
	}
}

// With several bad keys, the one reported is the first in sorted key order,
// whatever order the map iterates in. Five bad keys and fifty runs make an
// unsorted loop report "a/x" every time with a chance of about 5^-50.
func TestConfigMapHandler_Apply_KeysInSortedOrder(t *testing.T) {
	data := map[string]any{}
	for _, k := range []string{"e", "c", "a", "d", "b"} {
		data[k+"/x"] = "v"
	}
	h := &traits.ConfigMapHandler{}
	for range 50 {
		app := stack.NewApplication("myapp", "default", nil)
		trait := &oam.Trait{
			Type:       "configmap",
			Properties: map[string]any{"name": "my-config", "data": data},
		}
		err := h.Apply(trait, app, &stack.Bundle{})
		if want := `data: invalid key "a/x"`; err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Apply error = %v, want one containing %q", err, want)
		}
	}
}

func TestTransform_FluxNamespace_ReachesHelmRelease(t *testing.T) {
	// Build a transformer with helmrelease component handler and configmap trait handler.
	transformer := oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"helmrelease": &components.HelmReleaseHandler{},
		},
		nil,
	)
	transformer.RegisterBuiltinTrait("configmap", &traits.ConfigMapHandler{})

	// OAM app: helmrelease component + configmap traits WITHOUT mountPath.
	// Without mountPath the configmap trait adds a sibling ConfigMap app but does NOT
	// wrap the helmrelease config, so postProcessFluxNamespace calls SetFluxNamespace
	// directly on HelmReleaseConfig — exercising the pipeline wiring. The release
	// reads metrics-config through valuesFrom, from its own namespace, so that
	// ConfigMap follows it to the Flux namespace; other-config, which it does not
	// read, stays in the application namespace (go-kure/launcher#740).
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name: "metrics",
					Type: "helmrelease",
					Properties: map[string]any{
						"chart": map[string]any{
							"spec": map[string]any{
								"chart":     "kube-prometheus-stack",
								"sourceRef": map[string]any{"kind": "HelmRepository", "name": "prometheus-community"},
							},
						},
						"valuesFrom": []any{map[string]any{"kind": "ConfigMap", "name": "metrics-config"}},
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
						{
							Type: "configmap",
							Properties: map[string]any{
								"name": "other-config",
								"data": map[string]any{"key": "val"},
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

	// Find the helmrelease app, generate its resources, and assert namespaces.
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
			switch o := obj.(type) {
			case *helmv2.HelmRelease:
				found = true
				if ns := obj.GetNamespace(); ns != "custom-flux" {
					t.Errorf("%T.Namespace = %q, want %q", obj, ns, "custom-flux")
				}
				// The release still installs into the application namespace
				// (go-kure/launcher#610).
				if o.Spec.TargetNamespace != "default" {
					t.Errorf("HelmRelease targetNamespace = %q, want the application namespace %q", o.Spec.TargetNamespace, "default")
				}
			}
		}
	}
	if !found {
		t.Error("no HelmRelease found in cluster")
	}

	// The ConfigMap the release reads follows it; the other one stays.
	want := map[string]string{"metrics-config": "custom-flux", "other-config": "default"}
	for _, bundleApp := range allApps {
		ns, ok := want[bundleApp.Name]
		if !ok {
			continue
		}
		delete(want, bundleApp.Name)
		objs, genErr := bundleApp.Config.Generate(bundleApp)
		if genErr != nil {
			t.Fatalf("Generate %s: %v", bundleApp.Name, genErr)
		}
		for _, objPtr := range objs {
			if got := (*objPtr).GetNamespace(); got != ns {
				t.Errorf("%s: %T.Namespace = %q, want %q", bundleApp.Name, *objPtr, got, ns)
			}
		}
	}
	for name := range want {
		t.Errorf("no application %q found in cluster", name)
	}
}

// hcVetoConfig implements ApplicationConfig + autoHealthCheckEmitter and vetoes
// its auto health check (like a helmrelease with suspend: true).
type hcVetoConfig struct{ fluxNSCapture }

func (c *hcVetoConfig) EmitsAutoHealthCheck() bool { return false }

func TestConfigMapDecorator_EmitsAutoHealthCheck_Forwards(t *testing.T) {
	dec := traits.NewConfigMapDecorator(&hcVetoConfig{}, "c", "/etc/c")
	e, ok := any(dec).(interface{ EmitsAutoHealthCheck() bool })
	if !ok {
		t.Fatal("ConfigMapDecorator does not implement EmitsAutoHealthCheck")
	}
	if e.EmitsAutoHealthCheck() {
		t.Error("expected veto (false) forwarded from inner template-delivery config")
	}
}

// TestConfigMapDecorator_MountsIntoJobPodKinds pins the two run-to-completion
// kinds against the decorator's workload switch. Nothing in validation stops a
// `configmap` trait with a `mountPath` from being authored on a `job` or
// `cronjob` component — traitComponentRestrictions restricts `scaler` only — so
// a kind missing from that switch does not degrade, it fails generation outright
// with the unsupported-workload error. The two are asserted separately because
// their PodSpecs sit at different depths: a Job's at Spec.Template.Spec, a
// CronJob's one level further down.
func TestConfigMapDecorator_MountsIntoJobPodKinds(t *testing.T) {
	cases := []struct {
		name  string
		inner stack.ApplicationConfig
	}{
		{"job", &components.JobConfig{Image: "job:v1", RestartPolicy: corev1.RestartPolicyOnFailure}},
		{"cronjob", &components.CronjobConfig{Image: "job:v1", Schedule: "0 2 * * *"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := traits.NewConfigMapDecorator(tc.inner, "cfg", "/etc/cfg")
			objs, err := dec.Generate(newApp(tc.name, "default"))
			if err != nil {
				t.Fatalf("Generate() error = %v, want the configmap mounted", err)
			}
			var podSpec *corev1.PodSpec
			for _, objPtr := range objs {
				switch w := (*objPtr).(type) {
				case *batchv1.Job:
					podSpec = &w.Spec.Template.Spec
				case *batchv1.CronJob:
					podSpec = &w.Spec.JobTemplate.Spec.Template.Spec
				}
			}
			if podSpec == nil {
				t.Fatalf("no Job or CronJob among the %d generated objects", len(objs))
			}
			if !slices.ContainsFunc(podSpec.Volumes, func(v corev1.Volume) bool {
				return v.Name == "cfg" && v.ConfigMap != nil && v.ConfigMap.Name == "cfg"
			}) {
				t.Errorf("configmap volume not added: %+v", podSpec.Volumes)
			}
			if len(podSpec.Containers) == 0 {
				t.Fatal("generated pod spec has no containers to mount into")
			}
			if !slices.ContainsFunc(podSpec.Containers[0].VolumeMounts, func(m corev1.VolumeMount) bool {
				return m.Name == "cfg" && m.MountPath == "/etc/cfg"
			}) {
				t.Errorf("configmap volume mount not added: %+v", podSpec.Containers[0].VolumeMounts)
			}
		})
	}
}

func TestConfigMapDecorator_EmitsAutoHealthCheck_DefaultsTrue(t *testing.T) {
	dec := traits.NewConfigMapDecorator(&cmStub{name: "app", namespace: "default"}, "c", "/etc/c")
	e := any(dec).(interface{ EmitsAutoHealthCheck() bool })
	if !e.EmitsAutoHealthCheck() {
		t.Error("expected default true when inner does not implement autoHealthCheckEmitter")
	}
}
