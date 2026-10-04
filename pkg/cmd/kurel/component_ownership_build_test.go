package kurel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// The acceptance of go-kure/launcher#788 through the built-in pipeline:
// launcher labels what a component owns and reports the owner of every
// generated application, with no caller labelling anything.

// ownershipFixtureApps transforms the fixture's app.yaml with the built-in
// transformer, as kurel build does, and returns the transformer's parsed
// application and a function that transforms and generates it once more.
func ownershipFixtureApps(t *testing.T, fixture string) func() []oam.GeneratedApplication {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture, "app.yaml"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes(data, nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return func() []oam.GeneratedApplication {
		t.Helper()
		cluster, err := transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		apps, err := oam.GenerateApplications(cluster)
		if err != nil {
			t.Fatalf("GenerateApplications: %v", err)
		}
		return apps
	}
}

// objectsOfKind returns every generated object of kind with the application
// that generated it.
func objectsOfKind(apps []oam.GeneratedApplication, kind string) map[client.Object]oam.GeneratedApplication {
	out := map[client.Object]oam.GeneratedApplication{}
	for _, a := range apps {
		for _, p := range a.Objects {
			if p != nil && (*p).GetObjectKind().GroupVersionKind().Kind == kind {
				out[*p] = a
			}
		}
	}
	return out
}

// TestComponentOwnership_SynthesizedNetworkPolicySelectsItsPods: in a kurel
// build the NetworkPolicy synthesized for a component selects that component's
// pods as emitted. The selector names the component label, which the pod
// template carries because launcher put it there.
func TestComponentOwnership_SynthesizedNetworkPolicySelectsItsPods(t *testing.T) {
	dir := filepath.Join("testdata", "webservice-trait-mix")
	out := buildManifests(t, filepath.Join(dir, "app.yaml"), filepath.Join(dir, "cluster.yaml"))
	var policy, deployment map[string]any
	for _, raw := range strings.Split(out, "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		md, _ := doc["metadata"].(map[string]any)
		switch {
		case doc["kind"] == "NetworkPolicy" && md["name"] == "web-allow-ingress-traffic":
			policy = doc
		case doc["kind"] == "Deployment" && md["name"] == "web":
			deployment = doc
		}
	}
	if policy == nil || deployment == nil {
		t.Fatalf("the build emitted policy=%v deployment=%v, want both", policy != nil, deployment != nil)
	}

	raw := childMap(t, childMap(t, policy, "spec"), "podSelector")
	if got := childMap(t, raw, "matchLabels")[kurelComponentLabel]; got != "web" {
		t.Fatalf("policy spec.podSelector %s = %v, want %q", kurelComponentLabel, got, "web")
	}
	decoded, err := decodeLabelSelector(raw)
	if err != nil {
		t.Fatalf("decoding spec.podSelector: %v", err)
	}
	sel, err := metav1.LabelSelectorAsSelector(decoded)
	if err != nil {
		t.Fatalf("spec.podSelector: %v", err)
	}
	pod := labelSet(podTemplateLabels(deployment))
	if !sel.Matches(pod) {
		t.Errorf("policy selector %q does not match the Deployment's pod template labels %v", sel, pod)
	}
	// The match is the label's doing: the same pod without it is not selected.
	delete(pod, kurelComponentLabel)
	if sel.Matches(pod) {
		t.Errorf("policy selector %q matches the pod template without the component label", sel)
	}
}

// TestComponentOwnership_PostgresqlPooler: the Pooler a postgresql component
// lowers to is named differently from its component; it carries the
// component's label and its application reports the component.
func TestComponentOwnership_PostgresqlPooler(t *testing.T) {
	apps := ownershipFixtureApps(t, "postgresql-pooler")()
	poolers := objectsOfKind(apps, "Pooler")
	if len(poolers) != 1 {
		t.Fatalf("%d Poolers generated, want 1", len(poolers))
	}
	for obj, app := range poolers {
		if obj.GetName() != "postgres-pooler" {
			t.Fatalf("Pooler is named %q, want postgres-pooler", obj.GetName())
		}
		if app.Component != "postgres" {
			t.Errorf("the Pooler's application %q reports component %q, want %q", app.Name, app.Component, "postgres")
		}
		if got := obj.GetLabels()[kurelComponentLabel]; got != "postgres" {
			t.Errorf("Pooler %s = %q, want %q", kurelComponentLabel, got, "postgres")
		}
	}
}

// TestComponentOwnership_HelmReleasePostRendererOnce: a HelmRelease gets the
// component label post-renderer exactly once, however often its document is
// transformed and generated: the chart's pods are labelled by Flux, not by
// launcher, and a second entry would only repeat the first.
func TestComponentOwnership_HelmReleasePostRendererOnce(t *testing.T) {
	generate := ownershipFixtureApps(t, "helm-git-inline")
	for round := 1; round <= 2; round++ {
		releases := objectsOfKind(generate(), "HelmRelease")
		if len(releases) != 2 {
			t.Fatalf("transform %d: %d HelmReleases generated, want 2", round, len(releases))
		}
		for obj, app := range releases {
			hr, ok := obj.(*helmv2.HelmRelease)
			if !ok {
				t.Fatalf("transform %d: HelmRelease %q is a %T", round, obj.GetName(), obj)
			}
			if app.Component != hr.Name {
				t.Errorf("transform %d: HelmRelease %q reports component %q", round, hr.Name, app.Component)
			}
			if got := hr.Labels[kurelComponentLabel]; got != hr.Name {
				t.Errorf("transform %d: HelmRelease %q %s = %q", round, hr.Name, kurelComponentLabel, got)
			}
			if len(hr.Spec.PostRenderers) != 1 {
				t.Fatalf("transform %d: HelmRelease %q has %d post-renderers, want the component label's alone", round, hr.Name, len(hr.Spec.PostRenderers))
			}
			kustomize := hr.Spec.PostRenderers[0].Kustomize
			if kustomize == nil || len(kustomize.Patches) == 0 {
				t.Fatalf("transform %d: HelmRelease %q post-renderer carries no patch", round, hr.Name)
			}
			for _, p := range kustomize.Patches {
				if !strings.Contains(p.Patch, kurelComponentLabel+": "+hr.Name+"\n") {
					t.Errorf("transform %d: HelmRelease %q patch for %s does not set the component label:\n%s", round, hr.Name, p.Target.Kind, p.Patch)
				}
			}
		}
	}
}

// TestComponentOwnership_SharedGeneratedSource: the source two helm components
// share belongs to the application, not to the component that happened to emit
// it. It carries no component label and its application reports no component.
func TestComponentOwnership_SharedGeneratedSource(t *testing.T) {
	apps := ownershipFixtureApps(t, "helm-git-inline")()
	sources := objectsOfKind(apps, "GitRepository")
	if len(sources) != 1 {
		t.Fatalf("%d GitRepositories generated, want the one both components share", len(sources))
	}
	for obj, app := range sources {
		if app.Component != "" {
			t.Errorf("the shared source's application %q reports component %q, want none", app.Name, app.Component)
		}
		if got, has := obj.GetLabels()[kurelComponentLabel]; has {
			t.Errorf("the shared source %q carries %s=%q, want no component label", obj.GetName(), kurelComponentLabel, got)
		}
	}
}
