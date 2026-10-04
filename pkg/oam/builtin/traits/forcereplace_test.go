package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

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

// TestForceReplaceHandler_Apply_SetsDeliveryIntent pins what the trait is
// since go-kure/launcher#782: the ForceReplace delivery intent on the
// application, and nothing else. The config is the component's own, not a
// wrapper around it, and the objects it generates carry no Flux annotation.
func TestForceReplaceHandler_Apply_SetsDeliveryIntent(t *testing.T) {
	cfg := &cmStub{name: "migrate", namespace: "ns"}
	app := stack.NewApplication("migrate", "ns", cfg)
	bundle := newBundle()

	h := &traits.ForceReplaceHandler{}
	if !h.DecoratesSubApplications() {
		t.Error("DecoratesSubApplications() = false: the engine would not cover the component's sub-applications")
	}
	if err := h.Apply(&oam.Trait{Type: "force-replace"}, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got, want := app.Delivery, (stack.DeliveryIntent{ForceReplace: true}); got != want {
		t.Errorf("Delivery = %+v, want %+v", got, want)
	}
	if got, ok := app.Config.(*cmStub); !ok || got != cfg {
		t.Errorf("Config = %T, want the component's own config, unwrapped", app.Config)
	}
	if len(bundle.Applications) != 0 {
		t.Errorf("Apply added %d applications to the bundle, want none", len(bundle.Applications))
	}
	if bundle.Force != nil {
		t.Errorf("Apply set the bundle's Force to %v; the trait forces its own component, not the bundle", *bundle.Force)
	}
	assertNoFluxObjectKeys(t, generatedObjects(t, app)...)
}

// TestForceReplaceHandler_Apply_OnlyTargetApp pins both the opt-in default
// (an application without the trait has no intent) and the narrow scope (the
// trait on one application does not reach another).
func TestForceReplaceHandler_Apply_OnlyTargetApp(t *testing.T) {
	forced := stack.NewApplication("forced", "ns", &cmStub{name: "forced", namespace: "ns"})
	plain := stack.NewApplication("plain", "ns", &cmStub{name: "plain", namespace: "ns"})
	applyForceReplace(t, forced)

	if !forced.Delivery.ForceReplace {
		t.Error("the application with the trait has no ForceReplace intent")
	}
	if !plain.Delivery.IsZero() {
		t.Errorf("the application without the trait has the intent %+v, want none", plain.Delivery)
	}
}

// TestForceReplaceHandler_Apply_RealJob is the case go-kure/launcher#406 was
// filed for: the job component clears the generated Job's annotations
// wholesale (createJob's job.Annotations = nil). The intent is stated on the
// application, where the component's Generate cannot drop it, and the Job is
// generated as the component wrote it.
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

	if !app.Delivery.ForceReplace {
		t.Error("the job's application has no ForceReplace intent")
	}
	objs := generatedObjects(t, app)
	sawJob := false
	for _, o := range objs {
		if _, ok := o.(*batchv1.Job); ok {
			sawJob = true
		}
	}
	if !sawJob {
		t.Fatal("job component emitted no batch/v1 Job; the test no longer exercises the case it pins")
	}
	assertNoFluxObjectKeys(t, objs...)
}

// TestDeliveryIntentTraits_Stack pins that force-replace and prune-protection
// each set their own field and keep the other's, in either order.
func TestDeliveryIntentTraits_Stack(t *testing.T) {
	want := stack.DeliveryIntent{PruneProtection: true, ForceReplace: true}
	for _, order := range []string{"PruneFirst", "ForceFirst"} {
		t.Run(order, func(t *testing.T) {
			app := stack.NewApplication("stub", "ns", &cmStub{name: "stub", namespace: "ns"})
			if order == "PruneFirst" {
				applyPruneProtection(t, app)
				applyForceReplace(t, app)
			} else {
				applyForceReplace(t, app)
				applyPruneProtection(t, app)
			}
			if app.Delivery != want {
				t.Errorf("Delivery = %+v, want %+v", app.Delivery, want)
			}
		})
	}
}

// TestDeliveryIntentTraits_Idempotent pins that applying a trait twice, as the
// engine does when the same trait reaches an application through two paths,
// leaves the same intent.
func TestDeliveryIntentTraits_Idempotent(t *testing.T) {
	app := stack.NewApplication("stub", "ns", &cmStub{name: "stub", namespace: "ns"})
	applyForceReplace(t, app)
	applyForceReplace(t, app)
	applyPruneProtection(t, app)
	applyPruneProtection(t, app)
	if want := (stack.DeliveryIntent{PruneProtection: true, ForceReplace: true}); app.Delivery != want {
		t.Errorf("Delivery = %+v, want %+v", app.Delivery, want)
	}
}
