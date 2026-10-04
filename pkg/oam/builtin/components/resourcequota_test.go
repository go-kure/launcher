package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestResourceQuotaHandler_CanHandle(t *testing.T) {
	h := &components.ResourceQuotaHandler{}
	if !h.CanHandle("resourcequota") {
		t.Error("CanHandle(resourcequota) = false")
	}
	if h.CanHandle("limitrange") {
		t.Error("CanHandle(limitrange) = true")
	}
}

// TestResourceQuotaHandler_EmitsIdentityOnly: the API requires no field of the
// spec, so a component with no properties is a quota that limits nothing.
func TestResourceQuotaHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null fields":      {"hard": nil, "scopes": nil, "scopeSelector": nil},
	} {
		t.Run(name, func(t *testing.T) {
			rq := generateCoreKind(t, &components.ResourceQuotaHandler{}, "resourcequota", "compute", props).(*corev1.ResourceQuota)
			if rq.APIVersion != "v1" || rq.Kind != "ResourceQuota" {
				t.Errorf("GVK = %s %s, want v1 ResourceQuota", rq.APIVersion, rq.Kind)
			}
			if rq.Namespace != coreKindNamespace {
				t.Errorf("namespace = %q, want the build namespace %q", rq.Namespace, coreKindNamespace)
			}
			if !reflect.DeepEqual(rq.Spec, corev1.ResourceQuotaSpec{}) {
				t.Errorf("spec = %+v, want empty", rq.Spec)
			}
		})
	}
}

// TestResourceQuotaHandler_EmitsAuthoredSpec: hard limits, a quantity written
// as a number included, the scopes in authored order and the scope selector.
func TestResourceQuotaHandler_EmitsAuthoredSpec(t *testing.T) {
	rq := generateCoreKind(t, &components.ResourceQuotaHandler{}, "resourcequota", "compute", map[string]any{
		"hard":   map[string]any{"requests.cpu": "4", "limits.memory": "8Gi", "persistentvolumeclaims": 10},
		"scopes": []any{"NotTerminating", "NotBestEffort"},
		"scopeSelector": map[string]any{"matchExpressions": []any{
			map[string]any{"scopeName": "PriorityClass", "operator": "In", "values": []any{"high"}},
		}},
	}).(*corev1.ResourceQuota)
	for name, want := range map[corev1.ResourceName]string{
		corev1.ResourceRequestsCPU: "4", corev1.ResourceLimitsMemory: "8Gi", corev1.ResourcePersistentVolumeClaims: "10",
	} {
		if q := rq.Spec.Hard[name]; q.String() != want {
			t.Errorf("hard[%s] = %s, want %s", name, q.String(), want)
		}
	}
	wantScopes := []corev1.ResourceQuotaScope{corev1.ResourceQuotaScopeNotTerminating, corev1.ResourceQuotaScopeNotBestEffort}
	if !slices.Equal(rq.Spec.Scopes, wantScopes) {
		t.Errorf("scopes = %v, want %v in authored order", rq.Spec.Scopes, wantScopes)
	}
	wantSelector := &corev1.ScopeSelector{MatchExpressions: []corev1.ScopedResourceSelectorRequirement{{
		ScopeName: corev1.ResourceQuotaScopePriorityClass, Operator: corev1.ScopeSelectorOpIn, Values: []string{"high"},
	}}}
	if !reflect.DeepEqual(rq.Spec.ScopeSelector, wantSelector) {
		t.Errorf("scopeSelector = %+v, want %+v", rq.Spec.ScopeSelector, wantSelector)
	}
}

func TestResourceQuotaHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a v1 ResourceQuotaSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unknown key":      {map[string]any{"limits": map[string]any{"cpu": "4"}}, notASpec},
		"bad quantity":     {map[string]any{"hard": map[string]any{"requests.cpu": "lots"}}, notASpec},
		"hard a list":      {map[string]any{"hard": []any{"requests.cpu"}}, notASpec},
		"scopes a string":  {map[string]any{"scopes": "BestEffort"}, notASpec},
		"selector sub-key": {map[string]any{"scopeSelector": map[string]any{"matchExpression": []any{}}}, notASpec},
		"null scope":       {map[string]any{"scopes": []any{"BestEffort", nil}}, "scopes[1]"},
		"two spellings":    {map[string]any{"hard": map[string]any{"pods": "1"}, "Hard": map[string]any{"pods": "2"}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.ResourceQuotaHandler{}, "resourcequota", "compute", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
