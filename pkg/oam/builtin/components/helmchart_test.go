package components_test

import (
	"reflect"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestHelmchartHandler_IntervalInvalid_Rejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"interval": "5minutes",
			"source":   map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error for invalid interval format")
	}
}

func TestHelmchartHandler_CanHandle(t *testing.T) {
	h := &components.HelmchartHandler{}
	if !h.CanHandle("helmchart") {
		t.Error("expected true for helmchart")
	}
	if h.CanHandle("webservice") {
		t.Error("expected false for webservice")
	}
}

func TestHelmchartHandler_RequiresSource(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "metrics",
		Type:       "helmchart",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error when source is absent")
	}
}

func TestHelmchartHandler_BothURLAndName_Rejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"url":  "https://prometheus-community.github.io/helm-charts",
				"name": "existing-repo",
				"kind": "HelmRepository",
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error when both source.url and source.name are set")
	}
}

func TestHelmchartHandler_HelmRepository_Generate(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":   "kube-prometheus-stack",
			"version": "69.3.2",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(objects) != 2 {
		t.Fatalf("expected 2 objects (HelmRepository + HelmRelease), got %d", len(objects))
	}

	if _, ok := (*objects[0]).(*sourcev1.HelmRepository); !ok {
		t.Errorf("objects[0]: expected *sourcev1.HelmRepository, got %T", *objects[0])
	}
	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Errorf("objects[1]: expected *helmv2.HelmRelease, got %T", *objects[1])
	} else {
		if hr.Spec.Chart == nil {
			t.Fatal("HelmRelease.Spec.Chart is nil")
		}
		if hr.Spec.Chart.Spec.Chart != "kube-prometheus-stack" {
			t.Errorf("chart = %q, want %q", hr.Spec.Chart.Spec.Chart, "kube-prometheus-stack")
		}
		if hr.Spec.Chart.Spec.SourceRef.Kind != "HelmRepository" {
			t.Errorf("sourceRef.Kind = %q, want HelmRepository", hr.Spec.Chart.Spec.SourceRef.Kind)
		}
		if hr.Spec.Chart.Spec.SourceRef.Name != "metrics" {
			t.Errorf("sourceRef.Name = %q, want metrics", hr.Spec.Chart.Spec.SourceRef.Name)
		}
	}
}

func TestHelmchartHandler_OCIRepository_Generate(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "cert-manager",
		Type: "helmchart",
		Properties: map[string]any{
			"version": "v1.17.2",
			"source": map[string]any{
				"kind": "OCIRepository",
				"url":  "oci://ghcr.io/cert-manager/charts/cert-manager",
			},
		},
	}, "cert-manager")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("cert-manager", "cert-manager", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(objects) != 2 {
		t.Fatalf("expected 2 objects (OCIRepository + HelmRelease), got %d", len(objects))
	}

	if _, ok := (*objects[0]).(*sourcev1.OCIRepository); !ok {
		t.Errorf("objects[0]: expected *sourcev1.OCIRepository, got %T", *objects[0])
	}
	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Errorf("objects[1]: expected *helmv2.HelmRelease, got %T", *objects[1])
	} else {
		if hr.Spec.ChartRef == nil {
			t.Fatal("HelmRelease.Spec.ChartRef is nil")
		}
		if hr.Spec.ChartRef.Kind != "OCIRepository" {
			t.Errorf("chartRef.Kind = %q, want OCIRepository", hr.Spec.ChartRef.Kind)
		}
		if hr.Spec.ChartRef.Name != "cert-manager" {
			t.Errorf("chartRef.Name = %q, want cert-manager", hr.Spec.ChartRef.Name)
		}
	}
}

func TestHelmchartHandler_SourceRef_ExistingHelmRepo(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "prometheus",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":   "kube-prometheus-stack",
			"version": "69.3.2",
			"source": map[string]any{
				"kind":      "HelmRepository",
				"name":      "prometheus-community",
				"namespace": "flux-system",
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("prometheus", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(objects) != 1 {
		t.Fatalf("expected 1 object (HelmRelease only, no source CR), got %d", len(objects))
	}

	hr, ok := (*objects[0]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected *helmv2.HelmRelease, got %T", *objects[0])
	}
	if hr.Spec.Chart == nil {
		t.Fatal("HelmRelease.Spec.Chart is nil")
	}
	if hr.Spec.Chart.Spec.SourceRef.Kind != "HelmRepository" {
		t.Errorf("sourceRef.Kind = %q, want HelmRepository", hr.Spec.Chart.Spec.SourceRef.Kind)
	}
	if hr.Spec.Chart.Spec.SourceRef.Name != "prometheus-community" {
		t.Errorf("sourceRef.Name = %q, want prometheus-community", hr.Spec.Chart.Spec.SourceRef.Name)
	}
}

func TestHelmchartHandler_SourceRef_ExistingChartRef(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "cert-manager",
		Type: "helmchart",
		Properties: map[string]any{
			"source": map[string]any{
				"kind":      "HelmChart",
				"name":      "cert-manager-chart",
				"namespace": "flux-system",
			},
		},
	}, "cert-manager")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("cert-manager", "cert-manager", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(objects) != 1 {
		t.Fatalf("expected 1 object (HelmRelease only), got %d", len(objects))
	}

	hr, ok := (*objects[0]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected *helmv2.HelmRelease, got %T", *objects[0])
	}
	if hr.Spec.ChartRef == nil {
		t.Fatal("HelmRelease.Spec.ChartRef is nil")
	}
	if hr.Spec.ChartRef.Kind != "HelmChart" {
		t.Errorf("chartRef.Kind = %q, want HelmChart", hr.Spec.ChartRef.Kind)
	}
	if hr.Spec.ChartRef.Name != "cert-manager-chart" {
		t.Errorf("chartRef.Name = %q, want cert-manager-chart", hr.Spec.ChartRef.Name)
	}
}

func TestHelmchartHandler_KindURLMismatch_Rejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	cases := []struct {
		name string
		kind string
		url  string
	}{
		{
			name: "OCIRepository with https URL",
			kind: "OCIRepository",
			url:  "https://prometheus-community.github.io/helm-charts",
		},
		{
			name: "HelmRepository with oci URL",
			kind: "HelmRepository",
			url:  "oci://ghcr.io/cert-manager/charts/cert-manager",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{
				Name: "metrics",
				Type: "helmchart",
				Properties: map[string]any{
					"chart": "test-chart",
					"source": map[string]any{
						"kind": tc.kind,
						"url":  tc.url,
					},
				},
			}, "default")
			if err == nil {
				t.Fatal("expected error for kind/URL mismatch, got nil")
			}
		})
	}
}

func TestHelmchartHandler_SourceDedup(t *testing.T) {
	// Directly test dedup behavior on the config: set suppressSource and sharedSrcName,
	// verify Generate() emits no source CR and HelmRelease references the shared name.
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":   "kube-prometheus-stack",
			"version": "69.3.2",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	dedup, ok := cfg.(interface {
		SuppressSourceGeneration(string)
		GetSourceKey() string
		GetSourceRefName() string
	})
	if !ok {
		t.Fatal("HelmchartConfig does not implement SourceDeduplicatable")
	}

	dedup.SuppressSourceGeneration("prometheus-community")

	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate after suppression: %v", err)
	}

	if len(objects) != 1 {
		t.Fatalf("expected 1 object after dedup suppression, got %d", len(objects))
	}

	hr, ok := (*objects[0]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease, got %T", *objects[0])
	}
	if hr.Spec.Chart == nil {
		t.Fatal("HelmRelease.Spec.Chart is nil")
	}
	if hr.Spec.Chart.Spec.SourceRef.Name != "prometheus-community" {
		t.Errorf("sourceRef.Name = %q, want prometheus-community (shared source)", hr.Spec.Chart.Spec.SourceRef.Name)
	}
}

func TestHelmchartHandler_DeliveryTemplate_FormBRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"delivery": "template",
			"source": map[string]any{
				"name": "existing-repo",
				"kind": "HelmRepository",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support source.name (Form B)")
	}
}

func TestHelmchartHandler_DeliveryTemplate_OCIWithoutVersionRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "my-chart",
		Type: "helmchart",
		Properties: map[string]any{
			"delivery": "template",
			"source": map[string]any{
				"url": "oci://ghcr.io/example/charts/myapp",
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error: template delivery with OCIRepository requires version")
	}
}

func TestHelmchartHandler_DeliveryTemplate_ValuesFromRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"delivery": "template",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
			"valuesFrom": []any{
				map[string]any{"kind": "ConfigMap", "name": "my-values"},
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support valuesFrom")
	}
}

func TestHelmchartHandler_DeliveryTemplate_ReleaseNameRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":       "kube-prometheus-stack",
			"delivery":    "template",
			"releaseName": "my-release",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support releaseName")
	}
}

func TestHelmchartHandler_DeliveryTemplate_TargetNamespaceRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":           "kube-prometheus-stack",
			"delivery":        "template",
			"targetNamespace": "other-ns",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support targetNamespace")
	}
}

func TestHelmchartHandler_DeliveryTemplate_IntervalRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"delivery": "template",
			"interval": "5m",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support interval")
	}
}

func TestHelmchartHandler_DeliveryTemplate_DriftDetectionRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"delivery": "template",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
			"driftDetection": map[string]any{"mode": "enabled"},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support driftDetection")
	}
}

func TestHelmchartHandler_DeliveryTemplate_InstallCRDsRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"delivery": "template",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
			"install": map[string]any{"crds": "Create"},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support install.crds")
	}
}

func TestHelmchartHandler_DeliveryTemplate_ValuesModeConfigMapRejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":      "kube-prometheus-stack",
			"delivery":   "template",
			"valuesMode": "configMap",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error: template delivery does not support valuesMode: configMap")
	}
}

func TestHelmchartGetSourceKey_TemplateReturnsEmpty(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":    "kube-prometheus-stack",
			"delivery": "template",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	type sourcer interface{ GetSourceKey() string }
	s, ok := cfg.(sourcer)
	if !ok {
		t.Skip("config does not implement GetSourceKey")
	}
	if key := s.GetSourceKey(); key != "" {
		t.Errorf("GetSourceKey() = %q, want empty string for template delivery", key)
	}
}

func TestHelmchartHandler_DefaultInterval(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease at objects[1], got %T", *objects[1])
	}
	if hr.Spec.Interval.Duration.String() != "1h0m0s" {
		t.Errorf("interval = %q, want 1h0m0s (default)", hr.Spec.Interval.Duration.String())
	}
}

func TestHelmchartHandler_SourceKindRequired_FormB(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"name": "existing-source",
				// kind deliberately omitted
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error when source.kind is absent for Form B")
	}
}

func TestHelmchartHandler_DriftDetectionMode_Validated(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
			"driftDetection": map[string]any{
				"mode": "invalid-mode",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error for invalid driftDetection.mode")
	}
}

func TestHelmchartHandler_InstallCRDs_Validated(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
			"install": map[string]any{
				"crds": "Bad",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error for invalid install.crds")
	}
}

func TestHelmchartHandler_UpgradeCRDs_Validated(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
			"upgrade": map[string]any{
				"crds": "Bad",
			},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error for invalid upgrade.crds")
	}
}

func TestHelmchartHandler_InvalidHelmRepositoryURL_Rejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	for _, url := range []string{"ftp://repo.example/charts", "not-a-url", "ssh://repo.example"} {
		_, err := h.ToApplicationConfig(&oam.Component{
			Name: "metrics",
			Type: "helmchart",
			Properties: map[string]any{
				"chart": "kube-prometheus-stack",
				"source": map[string]any{
					"url": url,
				},
			},
		}, "monitoring")
		if err == nil {
			t.Errorf("expected error for HelmRepository URL %q, got nil", url)
		}
	}
}

func TestHelmchartConfig_SetFluxNamespace_AffectsFluxCRNamespace(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart": "kube-prometheus-stack",
			"source": map[string]any{
				"url": "https://prometheus-community.github.io/helm-charts",
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	setter, ok := cfg.(interface{ SetFluxNamespace(string) })
	if !ok {
		t.Fatal("HelmchartConfig does not implement SetFluxNamespace")
	}
	setter.SetFluxNamespace("custom-flux")

	app := stack.NewApplication("metrics", "monitoring", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, objPtr := range objs {
		obj := *objPtr
		switch obj.(type) {
		case *helmv2.HelmRelease, *sourcev1.HelmRepository:
			if ns := obj.GetNamespace(); ns != "custom-flux" {
				t.Errorf("%T.Namespace = %q, want %q", obj, ns, "custom-flux")
			}
		}
	}
}

func TestHelmchartHandler_ValuesFrom_Validated(t *testing.T) {
	h := &components.HelmchartHandler{}
	cases := []struct {
		name    string
		vfEntry map[string]any
	}{
		{
			name: "invalid kind",
			vfEntry: map[string]any{
				"kind": "BadKind",
				"name": "my-config",
			},
		},
		{
			name: "missing name",
			vfEntry: map[string]any{
				"kind": "ConfigMap",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{
				Name: "metrics",
				Type: "helmchart",
				Properties: map[string]any{
					"chart": "kube-prometheus-stack",
					"source": map[string]any{
						"url": "https://prometheus-community.github.io/helm-charts",
					},
					"valuesFrom": []any{tc.vfEntry},
				},
			}, "monitoring")
			if err == nil {
				t.Fatalf("expected error for valuesFrom case %q, got nil", tc.name)
			}
		})
	}
}

func TestHelmchartConfig_EmitsAutoHealthCheck(t *testing.T) {
	h := &components.HelmchartHandler{}
	mk := func(props map[string]any) stack.ApplicationConfig {
		cfg, err := h.ToApplicationConfig(&oam.Component{Name: "c", Type: "helmchart", Properties: props}, "demo")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		return cfg
	}
	emitter := func(cfg stack.ApplicationConfig) bool {
		e, ok := cfg.(interface{ EmitsAutoHealthCheck() bool })
		if !ok {
			t.Fatal("HelmchartConfig does not implement EmitsAutoHealthCheck")
		}
		return e.EmitsAutoHealthCheck()
	}

	native := mk(map[string]any{
		"chart":  "redis",
		"source": map[string]any{"url": "https://charts.example.com"},
	})
	if !emitter(native) {
		t.Error("native delivery must emit a HelmRelease auto health check")
	}

	template := mk(map[string]any{
		"chart":    "redis",
		"delivery": "template",
		"source":   map[string]any{"url": "https://charts.example.com"},
	})
	if emitter(template) {
		t.Error("template delivery emits no HelmRelease, so must veto the auto health check")
	}
}

func TestHelmchartHandler_ValuesModeInline_KeepsInlineValues(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":      "kube-prometheus-stack",
			"valuesMode": "inline",
			"values":     map[string]any{"replicaCount": 3},
			"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease at objects[1], got %T", *objects[1])
	}
	if hr.Spec.Values == nil {
		t.Fatal("Spec.Values is nil, want inline values set (unchanged pre-Task-1 behavior)")
	}
	if len(hr.Spec.ValuesFrom) != 0 {
		t.Errorf("Spec.ValuesFrom = %v, want empty under inline mode", hr.Spec.ValuesFrom)
	}
}

func TestHelmchartHandler_ValuesModeConfigMap_EmitsValuesFromRef(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":      "kube-prometheus-stack",
			"valuesMode": "configMap",
			"values":     map[string]any{"replicaCount": 3},
			"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
			"valuesFrom": []any{
				map[string]any{"kind": "ConfigMap", "name": "user-supplied"},
			},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease at objects[1], got %T", *objects[1])
	}

	if hr.Spec.Values != nil {
		t.Errorf("Spec.Values = %v, want nil under configMap mode", hr.Spec.Values)
	}
	if len(hr.Spec.ValuesFrom) != 2 {
		t.Fatalf("Spec.ValuesFrom has %d entries, want 2 (generated ref + user entry)", len(hr.Spec.ValuesFrom))
	}
	// D1 ordering: the generated values-ConfigMap ref comes first, so a
	// user-supplied valuesFrom entry (merged by Flux in list order, then
	// overridden further by nothing since Values is unset) wins on any
	// overlapping key.
	if hr.Spec.ValuesFrom[0].Kind != "ConfigMap" || hr.Spec.ValuesFrom[0].Name != "metrics-values" {
		t.Errorf("ValuesFrom[0] = %+v, want the generated values ConfigMap ref (kind ConfigMap, name metrics-values)", hr.Spec.ValuesFrom[0])
	}
	if hr.Spec.ValuesFrom[0].ValuesKey != "values.yaml" {
		t.Errorf("ValuesFrom[0].ValuesKey = %q, want values.yaml", hr.Spec.ValuesFrom[0].ValuesKey)
	}
	if hr.Spec.ValuesFrom[1].Name != "user-supplied" {
		t.Errorf("ValuesFrom[1].Name = %q, want user-supplied (user entry must land after the generated ref)", hr.Spec.ValuesFrom[1].Name)
	}
}

func TestHelmchartHandler_ValuesModeInvalid_Rejected(t *testing.T) {
	h := &components.HelmchartHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":      "kube-prometheus-stack",
			"valuesMode": "bogus",
			"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		},
	}, "monitoring")
	if err == nil {
		t.Fatal("expected error for unrecognized valuesMode")
	}
}

func TestHelmchartHandler_ValuesMode_HandlerDefault(t *testing.T) {
	h := &components.HelmchartHandler{ValuesMode: "configMap"}

	// No valuesMode property set: the handler's registration-time default
	// externalizes values into a ConfigMap.
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":  "kube-prometheus-stack",
			"values": map[string]any{"replicaCount": 3},
			"source": map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease at objects[1], got %T", *objects[1])
	}
	if hr.Spec.Values != nil {
		t.Error("Spec.Values must be nil when the handler default externalizes values")
	}
	if len(hr.Spec.ValuesFrom) != 1 {
		t.Fatalf("Spec.ValuesFrom has %d entries, want 1 (handler default configMap)", len(hr.Spec.ValuesFrom))
	}

	// A component-level valuesMode: inline overrides the handler default
	// back to inline.
	cfg2, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics2",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":      "kube-prometheus-stack",
			"valuesMode": "inline",
			"values":     map[string]any{"replicaCount": 3},
			"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app2 := stack.NewApplication("metrics2", "monitoring", cfg2)
	objects2, err := cfg2.Generate(app2)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hr2, ok := (*objects2[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease at objects2[1], got %T", *objects2[1])
	}
	if hr2.Spec.Values == nil {
		t.Error("Spec.Values must be set when the component overrides the handler default back to inline")
	}
	if len(hr2.Spec.ValuesFrom) != 0 {
		t.Errorf("Spec.ValuesFrom = %v, want empty when the component overrides to inline", hr2.Spec.ValuesFrom)
	}
}

func TestHelmchartConfig_ValuesModeConfigMap_AugmentsLayout(t *testing.T) {
	h := &components.HelmchartHandler{}
	mk := func(name string) stack.ApplicationConfig {
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name: name,
			Type: "helmchart",
			Properties: map[string]any{
				"chart":      "kube-prometheus-stack",
				"valuesMode": "configMap",
				"values":     map[string]any{"replicaCount": 3, "image": map[string]any{"tag": "v1.2.3"}},
				"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
			},
		}, "monitoring")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		return cfg
	}

	cfg := mk("metrics")
	aug, ok := cfg.(interface {
		AugmentLayout(*layout.ManifestLayout) error
	})
	if !ok {
		t.Fatal("configMap-mode config with non-empty Values does not implement LayoutAugmenter")
	}

	ml := &layout.ManifestLayout{}
	if err := aug.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Resources) != 1 {
		t.Fatalf("ml.Resources has %d entries, want exactly 1", len(ml.Resources))
	}
	cm, ok := ml.Resources[0].(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("ml.Resources[0] = %T, want *corev1.ConfigMap", ml.Resources[0])
	}
	// The emitted ConfigMap must carry TypeMeta: a zero-value TypeMeta's
	// apiVersion/kind fields are `omitempty` and json.Marshal drops them
	// entirely, producing a manifest kubectl apply/kustomize build reject.
	if cm.APIVersion != "v1" || cm.Kind != "ConfigMap" {
		t.Errorf("ConfigMap TypeMeta = {APIVersion: %q, Kind: %q}, want {APIVersion: \"v1\", Kind: \"ConfigMap\"}", cm.APIVersion, cm.Kind)
	}
	if len(ml.ExtraFiles) != 0 {
		t.Errorf("ml.ExtraFiles has %d entries, want 0", len(ml.ExtraFiles))
	}
	if len(ml.ConfigMapGenerators) != 0 {
		t.Errorf("ml.ConfigMapGenerators has %d entries, want 0", len(ml.ConfigMapGenerators))
	}

	// The ConfigMap's name must be exactly what the shared valuesConfigMapName
	// helper (unexported, so not directly callable from this external test
	// package) produces. Rather than hardcode that name, cross-check it
	// against buildHelmRelease's independent use of the same helper: both
	// call sites derive the name from c.Name, so if they ever diverge this
	// assertion catches it without needing to know the naming scheme itself.
	// TestValuesConfigMapName_TruncatesLongComponentName (internal test)
	// separately pins the helper's own behavior directly, including
	// truncation.
	app := stack.NewApplication("metrics", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hr, ok := (*objects[1]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("expected HelmRelease at objects[1], got %T", *objects[1])
	}
	if len(hr.Spec.ValuesFrom) == 0 {
		t.Fatal("HelmRelease has no ValuesFrom entries")
	}
	if cm.Name != hr.Spec.ValuesFrom[0].Name {
		t.Errorf("ConfigMap name %q != HelmRelease ValuesFrom[0].Name %q", cm.Name, hr.Spec.ValuesFrom[0].Name)
	}

	// Round-trip the expected values through the same yaml package so the
	// comparison isn't sensitive to yaml's own literal-to-Go-type decoding
	// choices (e.g. int vs int64).
	wantValues := map[string]any{"replicaCount": 3, "image": map[string]any{"tag": "v1.2.3"}}
	wantBytes, err := yaml.Marshal(wantValues)
	if err != nil {
		t.Fatalf("yaml.Marshal(wantValues): %v", err)
	}
	var wantRoundTrip map[string]any
	if err := yaml.Unmarshal(wantBytes, &wantRoundTrip); err != nil {
		t.Fatalf("yaml.Unmarshal(wantBytes): %v", err)
	}
	var gotValues map[string]any
	if err := yaml.Unmarshal([]byte(cm.Data["values.yaml"]), &gotValues); err != nil {
		t.Fatalf("yaml.Unmarshal(ConfigMap values.yaml): %v", err)
	}
	if !reflect.DeepEqual(gotValues, wantRoundTrip) {
		t.Errorf("ConfigMap values.yaml = %#v, want %#v", gotValues, wantRoundTrip)
	}

	// SetFluxNamespace must re-stamp the emitted ConfigMap's namespace too —
	// Flux's ValuesReference has no namespace field of its own and resolves
	// only within the referring HelmRelease's own namespace.
	cfg2 := mk("metrics-ns")
	setter, ok := cfg2.(interface{ SetFluxNamespace(string) })
	if !ok {
		t.Fatal("configMap-mode config does not implement SetFluxNamespace")
	}
	setter.SetFluxNamespace("custom-flux")
	aug2, ok := cfg2.(interface {
		AugmentLayout(*layout.ManifestLayout) error
	})
	if !ok {
		t.Fatal("configMap-mode config with non-empty Values does not implement LayoutAugmenter")
	}
	ml2 := &layout.ManifestLayout{}
	if err := aug2.AugmentLayout(ml2); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml2.Resources) != 1 {
		t.Fatalf("ml2.Resources has %d entries, want exactly 1", len(ml2.Resources))
	}
	cm2, ok := ml2.Resources[0].(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("ml2.Resources[0] = %T, want *corev1.ConfigMap", ml2.Resources[0])
	}
	if cm2.Namespace != "custom-flux" {
		t.Errorf("ConfigMap namespace = %q, want custom-flux", cm2.Namespace)
	}
}

func TestHelmchartConfig_InlineValues_IsNotLayoutAugmenter(t *testing.T) {
	h := &components.HelmchartHandler{}
	mk := func(props map[string]any) stack.ApplicationConfig {
		cfg, err := h.ToApplicationConfig(&oam.Component{Name: "metrics", Type: "helmchart", Properties: props}, "monitoring")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		return cfg
	}

	// Default (inline) mode.
	inlineDefault := mk(map[string]any{
		"chart":  "kube-prometheus-stack",
		"values": map[string]any{"replicaCount": 3},
		"source": map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
	})
	if _, ok := interface{}(inlineDefault).(interface {
		AugmentLayout(*layout.ManifestLayout) error
	}); ok {
		t.Error("default (inline) config must not satisfy LayoutAugmenter")
	}

	// configMap mode with zero Values: nothing to externalize, so still a
	// no-op augmenter and must not be wrapped either.
	configMapNoValues := mk(map[string]any{
		"chart":      "kube-prometheus-stack",
		"valuesMode": "configMap",
		"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
	})
	if _, ok := interface{}(configMapNoValues).(interface {
		AugmentLayout(*layout.ManifestLayout) error
	}); ok {
		t.Error("configMap-mode config with zero Values must not satisfy LayoutAugmenter")
	}
}

func TestHelmchartConfig_ValuesModeConfigMap_PreservesOptionalInterfaces(t *testing.T) {
	h := &components.HelmchartHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "metrics",
		Type: "helmchart",
		Properties: map[string]any{
			"chart":      "kube-prometheus-stack",
			"valuesMode": "configMap",
			"values":     map[string]any{"replicaCount": 2},
			"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	// Confirm this config is actually wrapped by augmentingHelmchartConfig —
	// otherwise this test would trivially pass by testing the unwrapped
	// *HelmchartConfig instead of the wrapper it's meant to guard.
	if _, ok := cfg.(interface {
		AugmentLayout(*layout.ManifestLayout) error
	}); !ok {
		t.Fatal("expected configMap-mode config with non-empty Values to be wrapped (satisfy LayoutAugmenter)")
	}

	if _, ok := cfg.(oam.SourceDeduplicatable); !ok {
		t.Error("wrapped config lost SourceDeduplicatable (GetSourceKey/GetSourceRefName/SuppressSourceGeneration)")
	}
	if _, ok := cfg.(interface{ SetFluxNamespace(string) }); !ok {
		t.Error("wrapped config lost fluxNamespaceSettable (SetFluxNamespace)")
	}
	if _, ok := cfg.(interface{ EmitsAutoHealthCheck() bool }); !ok {
		t.Error("wrapped config lost autoHealthCheckEmitter (EmitsAutoHealthCheck)")
	}
}
