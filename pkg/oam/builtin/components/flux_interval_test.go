package components_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// namedInterval is a named string type, as Go code building properties may use.
type namedInterval string

// kindNamedIntervalCase is one kind-named component whose interval decodes into
// a metav1.Duration (go-kure/launcher#601): helmrelease and the four Flux sources.
type kindNamedIntervalCase struct {
	typ     string
	handler oam.ComponentHandler
	props   func(t *testing.T) map[string]any // the smallest valid properties
}

func kindNamedIntervalCases() []kindNamedIntervalCase {
	cases := []kindNamedIntervalCase{{
		typ:     "helmrelease",
		handler: &components.HelmReleaseHandler{},
		props:   func(*testing.T) map[string]any { return map[string]any{"chart": hrChart()} },
	}}
	for _, k := range fluxSourceKinds() {
		cases = append(cases, kindNamedIntervalCase{
			typ:     k.typ,
			handler: k.handler,
			props:   func(t *testing.T) map[string]any { return fluxSrcProps(t, k.minimal) },
		})
	}
	return cases
}

// TestKindNamedFluxComponents_IntervalFluxDuration: the authored interval must be
// a duration the Flux CRDs accept, in the form it is emitted. Each refused value
// used to build and fail only at apply time, or, for one truncated to zero, to
// build with the 60m default in place of the value authored.
func TestKindNamedFluxComponents_IntervalFluxDuration(t *testing.T) {
	refused := []struct {
		key      string
		interval any
		wantSub  string
	}{
		{"interval", "-5m", "must be a Flux duration"},
		{"interval", "500us", "must be a Flux duration"},
		{"interval", "0.5ms", `emitted as "500µs"`},
		// Positive, but truncated to zero by time.ParseDuration.
		{"interval", "0.0000000001ms", `emitted as "0s"`},
		// The strict decode matches keys case-insensitively, so the check must too.
		// A truncating value, because only the authored-text check can see it.
		{"Interval", "0.0000000001ms", `emitted as "0s"`},
		// Go code may build properties with a named string type; the decode
		// accepts it, so the authored-text check must read it too.
		{"interval", namedInterval("0.0000000001ms"), `emitted as "0s"`},
	}
	accepted := []string{"0s", "1ms", "1.5s", "10m", "1h30m"}
	for _, c := range kindNamedIntervalCases() {
		for _, tc := range refused {
			t.Run(fmt.Sprintf("%s/refused %s=%v (%T)", c.typ, tc.key, tc.interval, tc.interval), func(t *testing.T) {
				props := c.props(t)
				props[tc.key] = tc.interval
				_, err := c.handler.ToApplicationConfig(&oam.Component{Name: "src", Type: c.typ, Properties: props}, "demo")
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), c.typ+": interval") || !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("error %q does not name %q and %q", err, c.typ+": interval", tc.wantSub)
				}
			})
		}
		for _, interval := range accepted {
			t.Run(c.typ+"/accepted "+interval, func(t *testing.T) {
				props := c.props(t)
				props["interval"] = interval
				cfg, err := c.handler.ToApplicationConfig(&oam.Component{Name: "src", Type: c.typ, Properties: props}, "demo")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if _, err := cfg.Generate(stack.NewApplication("src", "demo", cfg)); err != nil {
					t.Fatalf("Generate: %v", err)
				}
			})
		}
	}
}

// TestKindNamedFluxConfigs_GenerateChecksInterval: a config built directly, not
// parsed, is checked at Generate in the form the interval is emitted,
// Duration.String(): a negative or sub-millisecond duration falls outside Flux's
// pattern.
func TestKindNamedFluxConfigs_GenerateChecksInterval(t *testing.T) {
	configs := map[string]func(d time.Duration) stack.ApplicationConfig{
		"helmrelease": func(d time.Duration) stack.ApplicationConfig {
			return &components.HelmReleaseConfig{Name: "src", Spec: helmv2.HelmReleaseSpec{
				Chart: &helmv2.HelmChartTemplate{Spec: helmv2.HelmChartTemplateSpec{
					Chart:     "podinfo",
					SourceRef: helmv2.CrossNamespaceObjectReference{Kind: "HelmRepository", Name: "podinfo"},
				}},
				Interval: metav1.Duration{Duration: d},
			}}
		},
		"helmrepository": func(d time.Duration) stack.ApplicationConfig {
			return &components.HelmRepositoryConfig{Name: "src", Spec: sourcev1.HelmRepositorySpec{
				URL: "https://charts.example.com", Interval: metav1.Duration{Duration: d},
			}}
		},
		"ocirepository": func(d time.Duration) stack.ApplicationConfig {
			return &components.OCIRepositoryConfig{Name: "src", Spec: sourcev1.OCIRepositorySpec{
				URL: "oci://ghcr.io/org/manifests", Interval: metav1.Duration{Duration: d},
			}}
		},
		"gitrepository": func(d time.Duration) stack.ApplicationConfig {
			return &components.GitRepositoryConfig{Name: "src", Spec: sourcev1.GitRepositorySpec{
				URL: "https://github.com/org/repo", Interval: metav1.Duration{Duration: d},
			}}
		},
		"bucket": func(d time.Duration) stack.ApplicationConfig {
			return &components.BucketConfig{Name: "src", Spec: sourcev1.BucketSpec{
				BucketName: "b", Endpoint: "s3.amazonaws.com", Interval: metav1.Duration{Duration: d},
			}}
		},
	}
	for typ, build := range configs {
		for _, d := range []time.Duration{-5 * time.Minute, 500 * time.Microsecond} {
			t.Run(typ+"/refused "+d.String(), func(t *testing.T) {
				_, err := build(d).Generate(nil)
				if err == nil || !strings.Contains(err.Error(), typ+": interval") {
					t.Fatalf("expected an interval error naming %q, got %v", typ, err)
				}
			})
		}
		for _, d := range []time.Duration{0, 10 * time.Minute} {
			t.Run(typ+"/accepted "+d.String(), func(t *testing.T) {
				if _, err := build(d).Generate(nil); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			})
		}
	}
}

// TestHelmchartHandler_WrongTypeRefused: a present optional property of the
// wrong type is an error naming the field, not a silent fallback to the unset
// default (go-kure/launcher#601). `version: 7` used to build with no version,
// and `interval: 7` with the 60m default.
func TestHelmchartHandler_WrongTypeRefused(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"chart":  "kube-prometheus-stack",
			"source": map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
		}
	}
	cases := []struct {
		key     string
		value   any
		wantSub string
	}{
		{"chart", 7, "helmchart: chart: must be a string"},
		{"version", 7, "helmchart: version: must be a string"},
		{"delivery", 7, "helmchart: delivery: must be a string"},
		{"valuesMode", 7, "helmchart: valuesMode: must be a string"},
		{"interval", 7, "helmchart: interval: must be a string"},
		{"releaseName", 7, "helmchart: releaseName: must be a string"},
		{"targetNamespace", 7, "helmchart: targetNamespace: must be a string"},
		{"driftDetection", "enabled", "helmchart: driftDetection: must be an object"},
		{"install", "Skip", "helmchart: install: must be an object"},
		{"upgrade", "Skip", "helmchart: upgrade: must be an object"},
		{"values", "a: 1", "helmchart: values: must be an object"},
		{"valuesFrom", "cm", "helmchart: valuesFrom: must be an array"},
		{"driftDetection", map[string]any{"mode": 7}, "helmchart: driftDetection.mode: must be a string"},
		{"install", map[string]any{"crds": 7}, "helmchart: install.crds: must be a string"},
		{"upgrade", map[string]any{"crds": 7}, "helmchart: upgrade.crds: must be a string"},
		{"valuesFrom", []any{map[string]any{"kind": 7, "name": "cm"}}, "valuesFrom[0].kind: must be a string"},
		{"valuesFrom", []any{map[string]any{"kind": "ConfigMap", "name": "cm", "valuesKey": 7}}, "valuesFrom[0].valuesKey: must be a string"},
		{"valuesFrom", []any{map[string]any{"kind": "ConfigMap", "name": "cm", "targetPath": 7}}, "valuesFrom[0].targetPath: must be a string"},
	}
	h := &components.HelmchartHandler{}
	for _, tc := range cases {
		t.Run(tc.wantSub, func(t *testing.T) {
			props := base()
			props[tc.key] = tc.value
			_, err := h.ToApplicationConfig(&oam.Component{Name: "metrics", Type: "helmchart", Properties: props}, "monitoring")
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

// TestHelmchartHandler_NullOptionalIsAbsent pins the unchanged half: an explicit
// null on an optional property still reads as absence.
func TestHelmchartHandler_NullOptionalIsAbsent(t *testing.T) {
	h := &components.HelmchartHandler{}
	for _, key := range []string{
		"version", "delivery", "interval", "releaseName", "targetNamespace",
		"driftDetection", "install", "upgrade", "values", "valuesFrom",
	} {
		t.Run(key, func(t *testing.T) {
			props := map[string]any{
				"chart":  "kube-prometheus-stack",
				"source": map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
				key:      nil,
			}
			if _, err := h.ToApplicationConfig(&oam.Component{Name: "metrics", Type: "helmchart", Properties: props}, "monitoring"); err != nil {
				t.Fatalf("null %s: unexpected error: %v", key, err)
			}
		})
	}
}
