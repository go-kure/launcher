package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// go-kure/launcher#570: these optional top-level properties were read with a
// bare lookup and then type-checked, so an explicit null passed the published
// schema and authored validation (which leaves a top-level null in place) and
// was then refused as a wrong type. Each now reads a null, typed or untyped, as
// omission: the component builds exactly as one leaving the key out. Each case
// is two-sided, as in common_null_presence_internal_test.go — a wrongly typed
// value must still be refused, so deleting the type check would turn it red.
//
// With each read reverted to its bare lookup, every null subtest here fails
// but one: scopeOverrides as a typed nil list. A `[]any(nil)` already passed
// the old `.([]any)` assertion as an empty list, which builds the same config,
// so that subtest does not discriminate; the other four null shapes do.
func TestOptionalProperty_NullIsOmission(t *testing.T) {
	type handler interface {
		ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
	}
	const manifestYAML = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"
	const sourceURL = "https://example.com/manifests.yaml"
	cronjob := map[string]any{"image": "ghcr.io/org/job:v1.0.0", "schedule": "0 2 * * *"}
	// typeErr is the key's own type refusal. A looser "any error" check would
	// pass with the type check deleted: a map completionMode still meets the
	// enum refusal, a map timeZone the empty-string one, a map inline the
	// exactly-one-source one, and a map url the url-scheme one.
	cases := []struct {
		kind    string
		h       handler
		base    map[string]any
		key     string
		typeErr string
	}{
		{"cronjob", &CronjobHandler{}, cronjob, "successfulJobsHistoryLimit", "successfulJobsHistoryLimit: must be an integer, got map"},
		{"cronjob", &CronjobHandler{}, cronjob, "failedJobsHistoryLimit", "failedJobsHistoryLimit: must be an integer, got map"},
		{"cronjob", &CronjobHandler{}, cronjob, "timeZone", "timeZone: must be a string, got map"},
		{"cronjob", &CronjobHandler{}, cronjob, "completionMode", "completionMode: must be a string, got map"},
		{"job", &JobHandler{}, map[string]any{"image": "ghcr.io/org/job:v1.0.0"}, "completionMode", "completionMode: must be a string, got map"},
		{"passthrough", &PassthroughHandler{}, map[string]any{
			"object": map[string]any{"apiVersion": "example.com/v1", "kind": "Widget", "metadata": map[string]any{"name": "w"}},
		}, "clusterScoped", "'clusterScoped' must be a bool"},
		{"manifests", &ManifestsHandler{}, map[string]any{"inline": manifestYAML}, "scopeOverrides", "scopeOverrides must be a list"},
		{"manifests", &ManifestsHandler{}, map[string]any{"inline": manifestYAML}, "url", `property "url" must be a string`},
		{"manifests", &ManifestsHandler{}, map[string]any{"url": sourceURL}, "inline", `property "inline" must be a YAML string`},
		{"crd", &CRDHandler{}, map[string]any{"inline": crdYAML}, "url", `property "url" must be a string`},
		{"crd", &CRDHandler{}, map[string]any{"url": sourceURL}, "inline", `property "inline" must be a YAML string`},
	}
	with := func(base map[string]any, key string, v any) map[string]any {
		props := make(map[string]any, len(base)+1)
		for k, e := range base {
			props[k] = e
		}
		props[key] = v
		return props
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.key, func(t *testing.T) {
			build := func(t *testing.T, props map[string]any) stack.ApplicationConfig {
				t.Helper()
				cfg, err := tc.h.ToApplicationConfig(&oam.Component{Name: "c", Type: tc.kind, Properties: props}, "default")
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				// A manifests config holds its namespace-stamping hook as a
				// closure, which reflect.DeepEqual never finds equal. It is
				// built from the parsed scope overrides alone, which the
				// scopeOverrides case below compares directly.
				if mc, ok := cfg.(*manifestConfig); ok {
					c := *mc
					c.process = nil
					return &c
				}
				return cfg
			}
			if tc.key == "scopeOverrides" {
				for _, nv := range nullValues() {
					overrides, srcProps, err := parseScopeOverrides(with(tc.base, tc.key, nv.val))
					if err != nil || len(overrides) != 0 || !reflect.DeepEqual(srcProps, tc.base) {
						t.Errorf("%s: parseScopeOverrides(null) = %v, %v, %v; want no overrides and the other properties",
							nv.name, overrides, srcProps, err)
					}
				}
			}
			absent := build(t, tc.base)
			for _, nv := range nullValues() {
				t.Run(nv.name, func(t *testing.T) {
					if got := build(t, with(tc.base, tc.key, nv.val)); !reflect.DeepEqual(got, absent) {
						t.Errorf("%s: null built a different config than omitting the key:\n null:   %+v\n absent: %+v",
							tc.key, got, absent)
					}
				})
			}
			t.Run("wrong type still errors", func(t *testing.T) {
				wrong := with(tc.base, tc.key, map[string]any{"x": "y"})
				_, err := tc.h.ToApplicationConfig(&oam.Component{Name: "c", Type: tc.kind, Properties: wrong}, "default")
				if err == nil || !strings.Contains(err.Error(), tc.typeErr) {
					t.Errorf("%s: a map value must still be refused as a wrong type (%q), got %v", tc.key, tc.typeErr, err)
				}
			})
		})
	}
}

// parseResourceList and parseLabelMap now read a null entry as absence, as
// stringMapStrict already did, instead of refusing it. Property validation
// strips a null only under a declared key (`cpu`, `memory`), so a validated
// document reaches these with one too — `resources.requests: {example.com/gpu:
// null}` or `nodeSelector: {disk: null}` — not only a caller skipping validation.
func TestResourceAndLabelMaps_NullEntryIsOmission(t *testing.T) {
	for _, nv := range nullValues() {
		t.Run("resources/"+nv.name, func(t *testing.T) {
			got, err := parseResourceList(map[string]any{"cpu": nv.val, "memory": "1Gi"})
			if err != nil {
				t.Fatalf("parseResourceList: %v", err)
			}
			want, err := parseResourceList(map[string]any{"memory": "1Gi"})
			if err != nil {
				t.Fatalf("parseResourceList: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("a null cpu = %v, want %v", got, want)
			}
		})
		t.Run("labels/"+nv.name, func(t *testing.T) {
			got, err := parseLabelMap(map[string]any{"disk": nv.val, "zone": "a"}, "nodeSelector")
			if err != nil {
				t.Fatalf("parseLabelMap: %v", err)
			}
			if want := map[string]string{"zone": "a"}; !reflect.DeepEqual(got, want) {
				t.Errorf("a null disk = %v, want %v", got, want)
			}
		})
	}
	t.Run("wrong type still errors", func(t *testing.T) {
		if _, err := parseResourceList(map[string]any{"cpu": true}); err == nil {
			t.Error("parseResourceList: a bool quantity must still be refused")
		}
		if _, err := parseLabelMap(map[string]any{"disk": 1}, "nodeSelector"); err == nil {
			t.Error("parseLabelMap: a non-string value must still be refused")
		}
	})
	// The null skip must not swallow the key check: an invalid name was
	// refused before this change whatever its value, and still is.
	for _, nv := range nullValues() {
		t.Run("invalid key with null still errors/"+nv.name, func(t *testing.T) {
			for name, want := range map[string]string{
				"bad name!":            "invalid resource name",          // IsQualifiedName
				"gpu":                  "must be a standard container",   // validateContainerResourceName
				"example.com/requests": "",                               // a valid name: the null is skipped
				"hugepages-bogus":      "invalid hugepage resource name", // hugePageSize
				"hugepages-0":          "invalid hugepage resource name",
			} {
				_, err := parseResourceList(map[string]any{name: nv.val})
				if want == "" {
					if err != nil {
						t.Errorf("parseResourceList(%q: null): %v, want the entry skipped", name, err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("parseResourceList(%q: null): got %v, want %q", name, err, want)
				}
			}
			if _, err := parseLabelMap(map[string]any{"in valid": nv.val}, "nodeSelector"); err == nil || !strings.Contains(err.Error(), "invalid label key") {
				t.Errorf("parseLabelMap: got %v, want the invalid label key refusal", err)
			}
		})
	}
}

// A service selector whose every entry is null parses to an empty map, which
// would otherwise replace the default selector and emit a selector-less
// Service. It must meet the refusal `selector: {}` already gets.
func TestServiceSelector_AllNullIsTheEmptySelectorRefusal(t *testing.T) {
	const want = "selector: must name at least one label"
	build := func(sel any) error {
		_, err := (&ServiceHandler{}).ToApplicationConfig(&oam.Component{Name: "web", Type: "service", Properties: map[string]any{
			"ports":    []any{map[string]any{"port": 8080}},
			"selector": sel,
		}}, "default")
		return err
	}
	if err := build(map[string]any{}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("selector: {} = %v, want %q", err, want)
	}
	for _, nv := range nullValues() {
		t.Run(nv.name, func(t *testing.T) {
			if err := build(map[string]any{"app": nv.val}); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("selector: {app: null} = %v, want %q", err, want)
			}
		})
	}
}
