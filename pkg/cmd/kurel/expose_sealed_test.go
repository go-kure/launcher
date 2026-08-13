package kurel

import (
	"os"
	"testing"

	kio "github.com/go-kure/kure/pkg/io"

	"github.com/go-kure/launcher/pkg/oam"
)

// buildFixtureYAML runs the same pipeline runBuild uses (parse -> EvaluateProfile ->
// Transform -> collect -> encode) against a fixture's app.yaml and the given profile
// bytes, and returns the encoded manifest YAML. Unlike the spike's version, this
// calls transformer.Transform (not TransformAll, which does not exist on this
// branch): ExposeRule is a trait-position lowering rule, so it can only ever settle
// the single authored Application into itself — no document-position rule is
// registered anywhere in newBuiltinTransformer, so a 1->N fan-out never occurs here.
func buildFixtureYAML(t *testing.T, appPath string, profileData []byte) []byte {
	t.Helper()
	appData, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("reading %q: %v", appPath, err)
	}
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes(appData, nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing %q: %v", appPath, err)
	}
	profile, err := oam.ParseClusterProfile(profileData)
	if err != nil {
		t.Fatalf("parsing profile: %v", err)
	}
	evaluatedProfile, err := transformer.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("evaluating profile: %v", err)
	}
	ctx := oam.TransformContext{Capabilities: evaluatedProfile.Spec.Capabilities, Domain: kurelDomain}
	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	objects, err := collectFromNode(cluster.Node)
	if err != nil {
		t.Fatalf("collecting manifests: %v", err)
	}
	got, err := kio.EncodeObjectsToYAML(objects)
	if err != nil {
		t.Fatalf("encoding YAML: %v", err)
	}
	return got
}

// TestExposeRule_SealedGuard_ExtraIngressCapabilityIgnored is the C6 sealed-guard
// regression test: ExposeRule emits an already capability-resolved, sealed "ingress"
// trait (lowering.go), so applyTraits must skip its capability merge entirely
// (transform.go's `if !trait.sealed` guard) — even when the profile ALSO defines an
// "ingress" capability whose rendering carries a value the emitted trait does not
// already have. If the sealed guard were missing, that second merge would leak the
// "ingress" capability's rendering into the final Ingress annotations; with the guard,
// output is byte-identical to the expose-only profile.
func TestExposeRule_SealedGuard_ExtraIngressCapabilityIgnored(t *testing.T) {
	const appPath = "testdata/webservice-expose-ingress/app.yaml"

	exposeOnly := []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
`)
	exposeAndIngress := []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
    ingress:
      rendering:
        annotations:
          leaked-if-sealed-guard-is-broken: "yes"
`)

	want := buildFixtureYAML(t, appPath, exposeOnly)
	got := buildFixtureYAML(t, appPath, exposeAndIngress)
	if string(got) != string(want) {
		t.Errorf("adding an \"ingress\" capability changed output (sealed guard not honored):\nexpose-only:\n%s\nexpose+ingress:\n%s", want, got)
	}
}
