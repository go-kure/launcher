package components_test

import (
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"

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
