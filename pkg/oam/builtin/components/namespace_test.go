package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestNamespaceHandler_CanHandle(t *testing.T) {
	h := &components.NamespaceHandler{}
	if !h.CanHandle("namespace") {
		t.Error("CanHandle(namespace) = false")
	}
	if h.CanHandle("resourcequota") {
		t.Error("CanHandle(resourcequota) = true")
	}
}

// TestNamespaceHandler_EmitsIdentityOnly: with no properties the Namespace
// carries its name and nothing else. It is cluster-scoped, so it has no
// namespace, whatever namespace the application is built for.
func TestNamespaceHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null finalizers":  {"finalizers": nil},
	} {
		t.Run(name, func(t *testing.T) {
			ns := generateCoreKind(t, &components.NamespaceHandler{}, "namespace", "team-a", props).(*corev1.Namespace)
			if ns.APIVersion != "v1" || ns.Kind != "Namespace" {
				t.Errorf("GVK = %s %s, want v1 Namespace", ns.APIVersion, ns.Kind)
			}
			if ns.Namespace != "" {
				t.Errorf("namespace = %q, want none on a cluster-scoped object", ns.Namespace)
			}
			if !reflect.DeepEqual(ns.Spec, corev1.NamespaceSpec{}) {
				t.Errorf("spec = %+v, want empty", ns.Spec)
			}
		})
	}
}

func TestNamespaceHandler_Finalizers(t *testing.T) {
	ns := generateCoreKind(t, &components.NamespaceHandler{}, "namespace", "team-a",
		map[string]any{"finalizers": []any{"kubernetes", "example.com/cleanup"}}).(*corev1.Namespace)
	want := []corev1.FinalizerName{corev1.FinalizerKubernetes, "example.com/cleanup"}
	if !slices.Equal(ns.Spec.Finalizers, want) {
		t.Errorf("finalizers = %v, want %v in authored order", ns.Spec.Finalizers, want)
	}
}

// TestNamespaceHandler_Refusals: the properties are the NamespaceSpec fields
// and nothing else, so a metadata key is refused like any unknown one.
func TestNamespaceHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a v1 NamespaceSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"metadata key":      {map[string]any{"labels": map[string]any{"team": "a"}}, notASpec},
		"wrong type":        {map[string]any{"finalizers": "kubernetes"}, notASpec},
		"null list element": {map[string]any{"finalizers": []any{"kubernetes", nil}}, "finalizers[1]"},
		"two spellings":     {map[string]any{"finalizers": []any{"a"}, "Finalizers": []any{"b"}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.NamespaceHandler{}, "namespace", "team-a", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestNamespaceHandler_NameIsALabel: the component name is the Namespace's
// name, which the API holds to a DNS-1123 label, narrower than a component
// name. The refusal names the component.
func TestNamespaceHandler_NameIsALabel(t *testing.T) {
	h := &components.NamespaceHandler{}
	longest := strings.Repeat("a", 63)
	if err := coreKindErr(h, "namespace", longest, nil); err != nil {
		t.Errorf("a 63-character label: err = %v, want it accepted", err)
	}
	for _, name := range []string{"team.a", longest + "a", "-team"} {
		err := coreKindErr(h, "namespace", name, nil)
		if err == nil || !strings.Contains(err.Error(), "must be a DNS-1123 label of at most 63 characters") ||
			!strings.Contains(err.Error(), name) {
			t.Errorf("name %q: err = %v, want the label refusal naming it", name, err)
		}
	}
}

// TestNamespaceConfig_GenerateRepeatsNameCheck: the config is exported, so
// Generate cannot rely on ToApplicationConfig having run.
func TestNamespaceConfig_GenerateRepeatsNameCheck(t *testing.T) {
	cfg := &components.NamespaceConfig{Name: "team.a"}
	_, err := cfg.Generate(stack.NewApplication("team.a", coreKindNamespace, cfg))
	if err == nil || !strings.Contains(err.Error(), "must be a DNS-1123 label") {
		t.Errorf("err = %v, want the label refusal", err)
	}
}
