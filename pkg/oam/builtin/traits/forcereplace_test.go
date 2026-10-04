package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The wire values are written out literally rather than read from a constant:
// they are what kustomize-controller matches (its apply ForceSelector), so a
// test that shared the implementation's constant would stay green if the
// constant drifted.
const (
	forceKey     = "kustomize.toolkit.fluxcd.io/force"
	forceEnabled = "enabled"
)

func isForceEnabled(o client.Object) bool {
	return o.GetAnnotations()[forceKey] == forceEnabled
}

func applyForceReplace(t *testing.T, app *stack.Application) {
	t.Helper()
	if err := (&traits.ForceReplaceHandler{}).Apply(&oam.Trait{Type: "force-replace"}, app, &stack.Bundle{}); err != nil {
		t.Fatalf("force-replace Apply: %v", err)
	}
}

func TestForceReplaceHandler_CanHandle(t *testing.T) {
	h := &traits.ForceReplaceHandler{}
	cases := []struct {
		typ  string
		want bool
	}{
		{"force-replace", true},
		{"prune-protection", false},
		{"force", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := h.CanHandle(tc.typ); got != tc.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

// TestForceReplaceHandler_PropertySchema_Empty pins that the trait accepts no
// properties: an empty, non-nil schema is what makes ValidateAuthoredProperties
// reject any key an author writes on it.
func TestForceReplaceHandler_PropertySchema_Empty(t *testing.T) {
	s := (&traits.ForceReplaceHandler{}).PropertySchema()
	if s == nil {
		t.Fatal("PropertySchema() = nil, want an empty map")
	}
	if len(s) != 0 {
		t.Errorf("PropertySchema() declares %d properties, want none: %v", len(s), s)
	}
}

func TestForceReplaceHandler_Apply_AnnotatesResources(t *testing.T) {
	app := stack.NewApplication("migrate", "ns", &cmStub{name: "migrate", namespace: "ns"})
	applyForceReplace(t, app)

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resources) == 0 {
		t.Fatal("Generate returned no resources")
	}
	for _, r := range resources {
		if !isForceEnabled(*r) {
			t.Errorf("resource %q: annotation %q = %q, want %q",
				(*r).GetName(), forceKey, (*r).GetAnnotations()[forceKey], forceEnabled)
		}
	}
}

// TestForceReplaceHandler_Apply_OnlyTargetApp pins both the opt-in default
// (a component without the trait carries no force annotation) and the narrow
// scope (the trait on one component does not leak onto another).
func TestForceReplaceHandler_Apply_OnlyTargetApp(t *testing.T) {
	forced := stack.NewApplication("forced", "ns", &cmStub{name: "forced", namespace: "ns"})
	plain := stack.NewApplication("plain", "ns", &cmStub{name: "plain", namespace: "ns"})
	applyForceReplace(t, forced)

	forcedResources, err := forced.Generate()
	if err != nil {
		t.Fatalf("forced.Generate: %v", err)
	}
	plainResources, err := plain.Generate()
	if err != nil {
		t.Fatalf("plain.Generate: %v", err)
	}
	for _, r := range forcedResources {
		if !isForceEnabled(*r) {
			t.Errorf("forced resource %q: missing %s=%s", (*r).GetName(), forceKey, forceEnabled)
		}
	}
	for _, r := range plainResources {
		if v, ok := (*r).GetAnnotations()[forceKey]; ok {
			t.Errorf("resource %q without the trait carries %s=%q", (*r).GetName(), forceKey, v)
		}
	}
}

// TestForceReplaceHandler_Apply_RealJob is the case go-kure/launcher#406 was
// filed for: the job component clears the generated Job's annotations
// wholesale (createJob's job.Annotations = nil), so the force annotation only
// reaches the emitted Job if the decorator sets it after the inner Generate
// returns. Every object the component emits (Job and ServiceAccount) carries it.
func TestForceReplaceHandler_Apply_RealJob(t *testing.T) {
	cfg, err := (&components.JobHandler{}).ToApplicationConfig(&oam.Component{
		Name:       "migrate",
		Type:       "job",
		Properties: map[string]any{"image": "ghcr.io/example/migrate:v1.0.0"},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("migrate", "default", cfg)
	applyForceReplace(t, app)

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sawJob := false
	for _, r := range resources {
		if _, ok := (*r).(*batchv1.Job); ok {
			sawJob = true
		}
		if !isForceEnabled(*r) {
			t.Errorf("%T %q: missing %s=%s", *r, (*r).GetName(), forceKey, forceEnabled)
		}
	}
	if !sawJob {
		t.Fatal("job component emitted no batch/v1 Job; the test no longer exercises the case it pins")
	}
}

// TestForceReplace_PreservesOtherAnnotations stacks force-replace with
// prune-protection in both orders: each decorator adds its own key to the
// existing map rather than replacing it.
func TestForceReplace_PreservesOtherAnnotations(t *testing.T) {
	for _, order := range []string{"ForceOutermost", "ForceInnermost"} {
		t.Run(order, func(t *testing.T) {
			app := stack.NewApplication("stub", "ns", &cmStub{name: "stub", namespace: "ns"})
			if order == "ForceOutermost" {
				applyPruneProtection(t, app)
				applyForceReplace(t, app)
			} else {
				applyForceReplace(t, app)
				applyPruneProtection(t, app)
			}
			resources, err := app.Generate()
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, r := range resources {
				if !isForceEnabled(*r) {
					t.Errorf("%q: missing %s", (*r).GetName(), forceKey)
				}
				if !isPruneDisabled(*r) {
					t.Errorf("%q: missing %s", (*r).GetName(), stack.AnnotationFluxPruneKey)
				}
			}
		})
	}
}

// TestForceReplace_AnnotatesAugmentLayoutResources mirrors prune-protection's
// go-kure/launcher#324 regression test: resources an inner LayoutAugmenter
// adds in AugmentLayout, to ml.Resources or to a child layout, are generated by
// the component too and must carry the annotation, whatever the trait order.
func TestForceReplace_AnnotatesAugmentLayoutResources(t *testing.T) {
	assertAllForced := func(t *testing.T, ml *layout.ManifestLayout) {
		t.Helper()
		seen := map[string]bool{}
		for _, o := range layoutObjects(ml) {
			seen[o.GetName()] = true
			if !isForceEnabled(o) {
				t.Errorf("%q: missing %s=%s", o.GetName(), forceKey, forceEnabled)
			}
		}
		for _, n := range []string{"generated", "augmented", "augmented-child"} {
			if !seen[n] {
				t.Errorf("layout does not contain %q; the assertion above would be vacuous for it", n)
			}
		}
	}

	t.Run("Alone", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		applyForceReplace(t, app)
		assertAllForced(t, augmentLikeWalker(t, app))
	})
	t.Run("ForceOutermost", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		applySecurityContext(t, app)
		applyForceReplace(t, app)
		assertAllForced(t, augmentLikeWalker(t, app))
	})
	t.Run("ForceInnermost", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		applyForceReplace(t, app)
		applySecurityContext(t, app)
		assertAllForced(t, augmentLikeWalker(t, app))
	})
}

// TestNonForceDecorator_DoesNotAnnotateAugmentLayoutResources pins that the
// post-AugmentLayout annotation is force-replace's own, not a side effect of
// the generic augmentingDecorator forward (prune-protection's hook included).
func TestNonForceDecorator_DoesNotAnnotateAugmentLayoutResources(t *testing.T) {
	app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
	applyPruneProtection(t, app)
	for _, o := range layoutObjects(augmentLikeWalker(t, app)) {
		if v, ok := o.GetAnnotations()[forceKey]; ok {
			t.Errorf("%q: carries %s=%q without force-replace", o.GetName(), forceKey, v)
		}
	}
}
