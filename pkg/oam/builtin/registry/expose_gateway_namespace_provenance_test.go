package registry_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The expose rule folds the gateway namespace of its capability binding into
// the parent reference of the httproute trait it emits, so the report holds
// `parentRefs[0].namespace` under that trait, as rendered by the rule
// (go-kure/launcher#1580). Where the profile omits the namespace,
// EvaluateProfile writes "gateway-system" into the binding, and the binding
// does not say who wrote it: the entry is the same in both cases, so the report
// cannot say launcher defaulted the value. These tests pin that reading, so a
// change to it shows.

func exposeNamespaceProvenance(t *testing.T, rendering string) (entry oam.Source, manifests string) {
	t.Helper()
	profile, err := oam.ParseClusterProfile([]byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
        gatewayName: public
` + rendering))
	if err != nil {
		t.Fatalf("ParseClusterProfile: %v", err)
	}
	evaluated, err := newTransformer().EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("EvaluateProfile: %v", err)
	}
	comp := workload("app", "deployment", nil)
	comp.Traits = []oam.Trait{{Type: "expose", Properties: map[string]any{"hostnames": []any{"a.example.com"}, "servicePort": 8080}}}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	cluster, _, prov, err := newTransformer().TransformWithProvenance(app, oam.TransformContext{
		Namespace: "default", Policy: &defaultsPolicy{}, Capabilities: evaluated.Spec.Capabilities})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	got, ok := entriesOf(prov, owner{component: "app", trait: "httproute"})["parentRefs[0].namespace"]
	if !ok {
		t.Fatalf("the report holds no parentRefs[0].namespace under the httproute trait")
	}
	return got, string(manifestsOf(t, cluster))
}

func TestExposeGatewayNamespace_TheReportReadsRenderedWhetherOrNotTheProfileSetIt(t *testing.T) {
	want := oam.Source{Kind: oam.SourceRendered, Ref: "trait/expose@v1alpha1"}
	for _, tc := range []struct {
		name, rendering, namespace string
	}{
		{"the profile sets it", "        gatewayNamespace: infra\n", "infra"},
		{"the profile omits it and EvaluateProfile defaults it", "", "gateway-system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, manifests := exposeNamespaceProvenance(t, tc.rendering)
			if got != want {
				t.Errorf("parentRefs[0].namespace = %+v, want %+v: a change to this reading belongs in the contract page's list of what is not reported", got, want)
			}
			if !strings.Contains(manifests, "namespace: "+tc.namespace) {
				t.Errorf("the generated objects do not carry the namespace %q", tc.namespace)
			}
		})
	}
}
