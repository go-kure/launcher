package registry_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The expose rule moves the gateway namespace of its capability binding to the
// parent reference of the httproute trait it emits, so the report holds
// `parentRefs[0].namespace` under that trait, as rendered by the rule
// (go-kure/launcher#1580). The engine keeps the source of a rule's input only
// for a value the rule's output holds at the same path, and this value changes
// its path, so the entry reads the same whoever supplied the namespace: the
// profile, the schema default EvaluateProfile writes, or the route's own default
// when the binding never went through EvaluateProfile. The report cannot say
// launcher defaulted it. These tests pin that reading, so a change to it shows.

const exposeProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
        gatewayName: public
`

func exposeNamespaceReport(t *testing.T, caps map[string]oam.CapabilityBinding) (entry oam.Source, manifests string) {
	t.Helper()
	comp := workload("app", "deployment", nil)
	comp.Traits = []oam.Trait{{Type: "expose", Properties: map[string]any{"hostnames": []any{"a.example.com"}, "servicePort": 8080}}}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	cluster, _, prov, err := newTransformer().TransformWithProvenance(app, oam.TransformContext{
		Namespace: "default", Policy: &defaultsPolicy{}, Capabilities: caps})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	got, ok := entriesOf(prov, owner{component: "app", trait: "httproute"})["parentRefs[0].namespace"]
	if !ok {
		t.Fatalf("the report holds no parentRefs[0].namespace under the httproute trait")
	}
	return got, string(manifestsOf(t, cluster))
}

func evaluatedExposeBinding(t *testing.T, withProvenance bool, rendering string) map[string]oam.CapabilityBinding {
	t.Helper()
	profile, err := oam.ParseClusterProfile([]byte(exposeProfile + rendering))
	if err != nil {
		t.Fatalf("ParseClusterProfile: %v", err)
	}
	evaluate := newTransformer().EvaluateProfile
	if withProvenance {
		evaluate = newTransformer().EvaluateProfileWithProvenance
	}
	evaluated, err := evaluate(profile)
	if err != nil {
		t.Fatalf("evaluating the profile: %v", err)
	}
	return evaluated.Spec.Capabilities
}

func TestExposeGatewayNamespace_TheReportReadsRenderedWhoeverSuppliedIt(t *testing.T) {
	want := oam.Source{Kind: oam.SourceRendered, Ref: "trait/expose@v1alpha1"}
	cases := []struct {
		name      string
		caps      func(t *testing.T) map[string]oam.CapabilityBinding
		namespace string
	}{
		{"the profile sets it", func(t *testing.T) map[string]oam.CapabilityBinding {
			return evaluatedExposeBinding(t, false, "        gatewayNamespace: infra\n")
		}, "infra"},
		{"the profile omits it and EvaluateProfile writes the default", func(t *testing.T) map[string]oam.CapabilityBinding {
			return evaluatedExposeBinding(t, false, "")
		}, "gateway-system"},
		// The binding of this evaluation records the writer of each leaf
		// (Detail "handler-default"), and the entry still carries none.
		{"the profile omits it and EvaluateProfileWithProvenance writes the default", func(t *testing.T) map[string]oam.CapabilityBinding {
			return evaluatedExposeBinding(t, true, "")
		}, "gateway-system"},
		// No evaluation: the route's own default supplies the namespace.
		{"a binding built by hand omits it", func(t *testing.T) map[string]oam.CapabilityBinding {
			return map[string]oam.CapabilityBinding{"expose": {Rendering: map[string]any{"controllerType": "gateway", "gatewayName": "public"}}}
		}, "gateway-system"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, manifests := exposeNamespaceReport(t, tc.caps(t))
			if got != want {
				t.Errorf("parentRefs[0].namespace = %+v, want %+v: a change to this reading belongs in the contract page's list of what is not reported", got, want)
			}
			if !strings.Contains(manifests, "namespace: "+tc.namespace) {
				t.Errorf("the generated objects do not carry the namespace %q", tc.namespace)
			}
		})
	}
}
