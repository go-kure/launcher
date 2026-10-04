package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// containerLimit is one authored limit of type Container.
func containerLimit() map[string]any {
	return map[string]any{
		"type":                 "Container",
		"default":              map[string]any{"cpu": "500m", "memory": "256Mi"},
		"defaultRequest":       map[string]any{"cpu": "100m"},
		"max":                  map[string]any{"cpu": 2},
		"min":                  map[string]any{"cpu": "50m"},
		"maxLimitRequestRatio": map[string]any{"cpu": "4"},
	}
}

func TestLimitRangeHandler_CanHandle(t *testing.T) {
	h := &components.LimitRangeHandler{}
	if !h.CanHandle("limitrange") {
		t.Error("CanHandle(limitrange) = false")
	}
	if h.CanHandle("resourcequota") {
		t.Error("CanHandle(resourcequota) = true")
	}
}

// TestLimitRangeHandler_EmitsAuthoredSpec: the LimitRange lands in the build
// namespace with the limits as authored, a quantity written as a number
// included.
func TestLimitRangeHandler_EmitsAuthoredSpec(t *testing.T) {
	lr := generateCoreKind(t, &components.LimitRangeHandler{}, "limitrange", "defaults", map[string]any{
		"limits": []any{containerLimit(), map[string]any{"type": "PersistentVolumeClaim", "max": map[string]any{"storage": "10Gi"}}},
	}).(*corev1.LimitRange)
	if lr.APIVersion != "v1" || lr.Kind != "LimitRange" {
		t.Errorf("GVK = %s %s, want v1 LimitRange", lr.APIVersion, lr.Kind)
	}
	if lr.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", lr.Namespace, coreKindNamespace)
	}
	if len(lr.Spec.Limits) != 2 {
		t.Fatalf("limits = %+v, want the two authored", lr.Spec.Limits)
	}
	c, claim := lr.Spec.Limits[0], lr.Spec.Limits[1]
	if c.Type != corev1.LimitTypeContainer || claim.Type != corev1.LimitTypePersistentVolumeClaim {
		t.Errorf("types = %q, %q; want Container, PersistentVolumeClaim in authored order", c.Type, claim.Type)
	}
	for name, got := range map[string]corev1.ResourceList{
		"default 500m": c.Default, "defaultRequest 100m": c.DefaultRequest, "max 2": c.Max,
		"min 50m": c.Min, "maxLimitRequestRatio 4": c.MaxLimitRequestRatio,
	} {
		want := strings.Fields(name)[1]
		if q := got[corev1.ResourceCPU]; q.String() != want {
			t.Errorf("%s: cpu = %s", name, q.String())
		}
	}
	if q := c.Default[corev1.ResourceMemory]; q.String() != "256Mi" {
		t.Errorf("default memory = %s, want 256Mi", q.String())
	}
	if q := claim.Max[corev1.ResourceStorage]; q.String() != "10Gi" {
		t.Errorf("claim max storage = %s, want 10Gi", q.String())
	}
}

// TestLimitRangeHandler_EmptyLimitsIsKept: an authored empty list is a
// LimitRange that enforces nothing, and stays a list rather than null.
func TestLimitRangeHandler_EmptyLimitsIsKept(t *testing.T) {
	lr := generateCoreKind(t, &components.LimitRangeHandler{}, "limitrange", "defaults",
		map[string]any{"limits": []any{}}).(*corev1.LimitRange)
	if lr.Spec.Limits == nil || len(lr.Spec.Limits) != 0 {
		t.Errorf("limits = %#v, want an empty, non-nil list", lr.Spec.Limits)
	}
}

func TestLimitRangeHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a v1 LimitRangeSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":   {nil, "limits: required"},
		"null limits":     {map[string]any{"limits": nil}, "limits: required"},
		"untyped limit":   {map[string]any{"limits": []any{containerLimit(), map[string]any{"max": map[string]any{"cpu": "1"}}}}, "limits[1].type: required"},
		"empty type":      {map[string]any{"limits": []any{map[string]any{"type": ""}}}, "limits[0].type: required"},
		"repeated type":   {map[string]any{"limits": []any{containerLimit(), map[string]any{"type": "Pod"}, containerLimit()}}, `limits[2].type: "Container" is also the type of limits[0]`},
		"unknown key":     {map[string]any{"limits": []any{containerLimit()}, "labels": map[string]any{}}, notASpec},
		"unknown sub-key": {map[string]any{"limits": []any{map[string]any{"type": "Pod", "maximum": map[string]any{}}}}, notASpec},
		"bad quantity":    {map[string]any{"limits": []any{map[string]any{"type": "Pod", "max": map[string]any{"cpu": "lots"}}}}, notASpec},
		"limits a map":    {map[string]any{"limits": containerLimit()}, notASpec},
		"null limit":      {map[string]any{"limits": []any{nil}}, "limits[0]"},
		"two spellings":   {map[string]any{"limits": []any{map[string]any{"type": "Pod", "Type": "Container"}}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.LimitRangeHandler{}, "limitrange", "defaults", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestLimitRangeConfig_GenerateRepeatsRefusals: the config is exported, so
// Generate cannot rely on ToApplicationConfig having run.
func TestLimitRangeConfig_GenerateRepeatsRefusals(t *testing.T) {
	cfg := &components.LimitRangeConfig{Name: "defaults", Namespace: coreKindNamespace}
	_, err := cfg.Generate(stack.NewApplication("defaults", coreKindNamespace, cfg))
	if err == nil || !strings.Contains(err.Error(), "limits: required") {
		t.Errorf("err = %v, want the limits refusal", err)
	}
}
