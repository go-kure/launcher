package components_test

import (
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

// durationFieldCase is one Flux duration field other than a top-level interval
// (go-kure/launcher#606) on a kind-named component.
type durationFieldCase struct {
	typ     string
	handler oam.ComponentHandler
	props   func(t *testing.T) map[string]any // the smallest valid properties
	path    []string
	// hours is whether the field's pattern takes the h unit. The source
	// timeouts do not.
	hours bool
}

func (c durationFieldCase) name() string { return strings.Join(c.path, ".") }

func durationFieldCases() []durationFieldCase {
	hr := func(*testing.T) map[string]any { return map[string]any{"chart": hrChart()} }
	var cases []durationFieldCase
	for _, path := range [][]string{
		{"timeout"},
		{"chart", "spec", "interval"},
		{"install", "timeout"},
		{"install", "strategy", "retryInterval"},
		{"upgrade", "timeout"},
		{"upgrade", "strategy", "retryInterval"},
		{"test", "timeout"},
		{"rollback", "timeout"},
		{"uninstall", "timeout"},
	} {
		cases = append(cases, durationFieldCase{typ: "helmrelease", handler: &components.HelmReleaseHandler{}, props: hr, path: path, hours: true})
	}
	for _, k := range fluxSourceKinds() {
		if _, ok := k.handler.PropertySchema()["timeout"]; !ok {
			continue // a HelmChart has no timeout
		}
		cases = append(cases, durationFieldCase{
			typ:     k.typ,
			handler: k.handler,
			props:   func(t *testing.T) map[string]any { return fluxSrcProps(t, k.minimal) },
			path:    []string{"timeout"},
		})
	}
	return cases
}

// setPath sets props at path, creating or extending the maps along it. A
// strategy object also gets the name the CRD requires.
func setPath(props map[string]any, path []string, value any) {
	m := props
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		if k == "strategy" {
			next["name"] = "RetryOnFailure"
		}
		m = next
	}
	m[path[len(path)-1]] = value
}

// TestKindNamedFluxComponents_DurationFields: every Flux duration field must be
// authored as a duration its CRD pattern accepts, in the form it is emitted.
// Each refused value used to build and fail only at apply time.
func TestKindNamedFluxComponents_DurationFields(t *testing.T) {
	type refusal struct{ value, wantSub string }
	common := []refusal{
		{"-5m", "must be a Flux duration"},
		{"500us", "must be a Flux duration"},
		{"0.5ms", `emitted as "500µs"`},
		// Positive, but truncated to zero by time.ParseDuration: only the
		// authored-text check can see it.
		{"0.0000000001ms", `emitted as "0s"`},
	}
	// A source timeout takes no h as authored. An hour or more authored in
	// minutes or seconds is accepted: it is emitted in minutes
	// (TestFluxSourceComponents_LongTimeoutEmittedInMinutes).
	noHours := []refusal{
		{"1h", "must be a Flux duration (unsigned; units ms, s, m;"},
		{"1h30m", "must be a Flux duration (unsigned; units ms, s, m;"},
	}
	for _, c := range durationFieldCases() {
		refused := common
		accepted := []string{"0s", "1ms", "10m"}
		if c.hours {
			accepted = append(accepted, "1h30m")
		} else {
			refused = append(append([]refusal{}, common...), noHours...)
			accepted = append(accepted, "59m59s", "60m", "90m", "3600s")
		}
		for _, tc := range refused {
			t.Run(c.typ+"/"+c.name()+"/refused "+tc.value, func(t *testing.T) {
				props := c.props(t)
				setPath(props, c.path, tc.value)
				_, err := c.handler.ToApplicationConfig(&oam.Component{Name: "src", Type: c.typ, Properties: props}, "demo")
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if want := c.typ + ": " + c.name() + " "; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("error %q does not name %q and %q", err, want, tc.wantSub)
				}
			})
		}
		for _, value := range accepted {
			t.Run(c.typ+"/"+c.name()+"/accepted "+value, func(t *testing.T) {
				props := c.props(t)
				setPath(props, c.path, value)
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

// TestKindNamedFluxComponents_NestedDurationKeysMatchCaseInsensitively: the
// strict decode matches keys case-insensitively at every level, so the
// authored-text check must too. A truncating value, because only that check
// can see it.
func TestKindNamedFluxComponents_NestedDurationKeysMatchCaseInsensitively(t *testing.T) {
	props := map[string]any{"chart": hrChart()}
	setPath(props, []string{"Install", "Strategy", "RETRYINTERVAL"}, "0.0000000001ms")
	_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(&oam.Component{Name: "src", Type: "helmrelease", Properties: props}, "demo")
	if err == nil || !strings.Contains(err.Error(), `helmrelease: install.strategy.retryInterval "0.0000000001ms"`) {
		t.Fatalf("expected the truncating retryInterval to be refused, got %v", err)
	}
}

// TestKindNamedFluxConfigs_GenerateChecksDurationFields: a config built
// directly, not parsed, is checked at Generate in the form each duration is
// emitted: Duration.String(), and for a source timeout of an hour or more, in
// minutes.
func TestKindNamedFluxConfigs_GenerateChecksDurationFields(t *testing.T) {
	type field struct {
		typ   string
		name  string
		hours bool
		build func(d *metav1.Duration) stack.ApplicationConfig
	}
	hr := func(set func(*helmv2.HelmReleaseSpec, *metav1.Duration)) func(*metav1.Duration) stack.ApplicationConfig {
		return func(d *metav1.Duration) stack.ApplicationConfig {
			spec := helmv2.HelmReleaseSpec{Chart: &helmv2.HelmChartTemplate{Spec: helmv2.HelmChartTemplateSpec{
				Chart:     "podinfo",
				SourceRef: helmv2.CrossNamespaceObjectReference{Kind: "HelmRepository", Name: "podinfo"},
			}}}
			set(&spec, d)
			return &components.HelmReleaseConfig{Name: "src", Spec: spec}
		}
	}
	fields := []field{
		{"helmrelease", "timeout", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Timeout = d })},
		{"helmrelease", "chart.spec.interval", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Chart.Spec.Interval = d })},
		{"helmrelease", "install.timeout", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Install = &helmv2.Install{Timeout: d} })},
		{"helmrelease", "install.strategy.retryInterval", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) {
			s.Install = &helmv2.Install{Strategy: &helmv2.InstallStrategy{Name: "RetryOnFailure", RetryInterval: d}}
		})},
		{"helmrelease", "upgrade.timeout", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Upgrade = &helmv2.Upgrade{Timeout: d} })},
		{"helmrelease", "upgrade.strategy.retryInterval", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) {
			s.Upgrade = &helmv2.Upgrade{Strategy: &helmv2.UpgradeStrategy{Name: "RetryOnFailure", RetryInterval: d}}
		})},
		{"helmrelease", "test.timeout", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Test = &helmv2.Test{Timeout: d} })},
		{"helmrelease", "rollback.timeout", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Rollback = &helmv2.Rollback{Timeout: d} })},
		{"helmrelease", "uninstall.timeout", true, hr(func(s *helmv2.HelmReleaseSpec, d *metav1.Duration) { s.Uninstall = &helmv2.Uninstall{Timeout: d} })},
		{"helmrepository", "timeout", false, func(d *metav1.Duration) stack.ApplicationConfig {
			return &components.HelmRepositoryConfig{Name: "src", Spec: sourcev1.HelmRepositorySpec{URL: "https://charts.example.com", Timeout: d}}
		}},
		{"ocirepository", "timeout", false, func(d *metav1.Duration) stack.ApplicationConfig {
			return &components.OCIRepositoryConfig{Name: "src", Spec: sourcev1.OCIRepositorySpec{URL: "oci://ghcr.io/org/manifests", Timeout: d}}
		}},
		{"gitrepository", "timeout", false, func(d *metav1.Duration) stack.ApplicationConfig {
			return &components.GitRepositoryConfig{Name: "src", Spec: sourcev1.GitRepositorySpec{URL: "https://github.com/org/repo", Timeout: d}}
		}},
		{"bucket", "timeout", false, func(d *metav1.Duration) stack.ApplicationConfig {
			return &components.BucketConfig{Name: "src", Spec: sourcev1.BucketSpec{BucketName: "b", Endpoint: "s3.amazonaws.com", Timeout: d}}
		}},
	}
	for _, f := range fields {
		refused := []time.Duration{-5 * time.Minute, 500 * time.Microsecond}
		accepted := []time.Duration{0, 10 * time.Minute, time.Hour, 2 * time.Hour}
		for _, d := range refused {
			t.Run(f.typ+"/"+f.name+"/refused "+d.String(), func(t *testing.T) {
				_, err := f.build(&metav1.Duration{Duration: d}).Generate(nil)
				if want := f.typ + ": " + f.name + " "; err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("expected an error naming %q, got %v", want, err)
				}
			})
		}
		for _, d := range accepted {
			t.Run(f.typ+"/"+f.name+"/accepted "+d.String(), func(t *testing.T) {
				if _, err := f.build(&metav1.Duration{Duration: d}).Generate(nil); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			})
		}
		t.Run(f.typ+"/"+f.name+"/unset", func(t *testing.T) {
			if _, err := f.build(nil).Generate(nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
