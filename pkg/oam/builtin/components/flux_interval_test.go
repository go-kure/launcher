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
// a metav1.Duration (go-kure/launcher#601): helmrelease and the five Flux sources.
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
		"helmchart": func(d time.Duration) stack.ApplicationConfig {
			return &components.HelmChartConfig{Name: "src", Spec: sourcev1.HelmChartSpec{
				Chart: "podinfo", SourceRef: sourcev1.LocalHelmChartSourceReference{Kind: "HelmRepository", Name: "podinfo"},
				Interval: metav1.Duration{Duration: d},
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
