package kurel

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"os"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// webserviceApp is an authored Application holding one webservice component
// "web" with the given properties and traits, in namespace "ns".
func webserviceApp(props map[string]any, traits ...oam.Trait) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "ns"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "webservice", Properties: props, Traits: traits,
		}}},
	}
}

// TestWebserviceRule_OriginRule asserts the provenance the engine stamps on the
// deployment member the webservice rule emits: Origin.Rule names the rule,
// "component/webservice", and every other Origin field still names the
// AUTHORED webservice. The probe trait is a type the rule does not know, so it
// is forwarded to the deployment member — the default for an extension's trait.
func TestWebserviceRule_OriginRule(t *testing.T) {
	var seen oam.Component
	tr := newBuiltinTransformer()
	tr.RegisterTraitLowering(originProbeRule{seen: &seen})

	_, err := tr.Transform(webserviceApp(map[string]any{"image": "nginx:1"}, oam.Trait{Type: "origin-probe"}),
		oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	if !stderrors.Is(err, errOriginSeen) {
		t.Fatalf("expected the probe to run, got: %v", err)
	}
	if seen.Name != "web" || seen.Type != "deployment" {
		t.Fatalf("probe saw component %q (type %q), want web (type deployment)", seen.Name, seen.Type)
	}
	got, ok := seen.Origin()
	if !ok {
		t.Fatal("the emitted component carries no Origin")
	}
	want := oam.Origin{
		Document: "app", DocumentKind: "Application", Namespace: "ns",
		Component: "web", ComponentType: "webservice", Index: 0,
		Rule: "component/webservice",
	}
	if got != want {
		t.Errorf("Origin = %+v\nwant   %+v", got, want)
	}
}

// TestWebserviceRule_ParseErrorPrefix pins the one deliberate difference in how
// a webservice is refused. The cause text is the former handler's, unchanged;
// the prefix is now the lowering engine's, naming the component's type and
// document. The former handler's error read:
//
//	component "web": image "UPPER CASE" rejected: could not parse reference: UPPER CASE
func TestWebserviceRule_ParseErrorPrefix(t *testing.T) {
	_, err := newBuiltinTransformer().Transform(webserviceApp(map[string]any{"image": "UPPER CASE"}),
		oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	const want = `lowering document "app" in document "app" (kind "Application"): ` +
		`component "web" (type "webservice") in document "app" (kind "Application"): ` +
		`image "UPPER CASE" rejected: could not parse reference: UPPER CASE`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant  %s", err, want)
	}
	var le *oam.LoweringError
	if !stderrors.As(err, &le) {
		t.Errorf("err is %T, want it to carry *oam.LoweringError", err)
	}
}

// TestWebserviceRule_ForwardedTraitKeepsCapabilityEnforcement: the rule forwards
// expose to its service member, and the forwarded trait stays an authored one,
// still subject to the required-capability check, as it was on the handler.
func TestWebserviceRule_ForwardedTraitKeepsCapabilityEnforcement(t *testing.T) {
	app := webserviceApp(map[string]any{"image": "nginx:1"},
		oam.Trait{Type: "expose", Properties: map[string]any{"hostnames": []any{"web.example.com"}}})
	_, err := newBuiltinTransformer().Transform(app, oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	if !stderrors.Is(err, oam.ErrMissingCapability) {
		t.Errorf("expected ErrMissingCapability for expose on a webservice with no capability, got: %v", err)
	}
}

// TestNewBuiltinTransformer_PublishesWebserviceSchemaUnchanged is
// TestNewBuiltinTransformer_PublishesWorkerSchemaUnchanged for "webservice",
// against the schema the former WebserviceHandler published
// (pkg/oam/builtin/components/testdata/webservice-property-schema.json).
func TestNewBuiltinTransformer_PublishesWebserviceSchemaUnchanged(t *testing.T) {
	want, err := os.ReadFile("../../oam/builtin/components/testdata/webservice-property-schema.json")
	if err != nil {
		t.Fatalf("reading the captured schema: %v", err)
	}
	published, ok := newBuiltinTransformer().HandlerSchemas().Components["webservice"]
	if !ok {
		t.Fatal("HandlerSchemas() publishes no schema for webservice")
	}
	got, err := json.MarshalIndent(published, "", "  ")
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Error("the schema published for webservice differs from the one the former handler published")
	}
}

// TestBuild_WebserviceBundleTraitsApplyOnce: the bundle traits act on the
// group's one Kustomization. The rule forwards them to the deployment member
// only, so the bundle carries the authored patch and substitute once, as the
// handler's did, rather than a second copy refused as a duplicate.
func TestBuild_WebserviceBundleTraitsApplyOnce(t *testing.T) {
	app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
      traits:
        - type: fluxcd-patches
          properties:
            patches:
              - patch: |
                  - op: add
                    path: /metadata/labels/patched
                    value: "yes"
                target:
                  kind: Deployment
        - type: fluxcd-postbuild
          properties:
            substitute:
              REGION: eu-west-1
`
	ks := deliveryKustomizations(t, app)
	if len(ks) != 1 {
		t.Fatalf("Kustomizations = %v, want the one bundle", keysOf(ks))
	}
	for name, spec := range ks {
		if patches, _ := spec["patches"].([]any); len(patches) != 1 {
			t.Errorf("%s patches = %v, want the one authored patch", name, spec["patches"])
		}
		pb, _ := spec["postBuild"].(map[string]any)
		if sub, _ := pb["substitute"].(map[string]any); sub["REGION"] != "eu-west-1" {
			t.Errorf("%s postBuild = %v, want the authored substitute", name, spec["postBuild"])
		}
	}
}
