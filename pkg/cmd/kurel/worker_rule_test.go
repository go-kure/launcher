package kurel

import (
	stderrors "errors"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// workerApp is an authored Application holding one worker component "w" with the
// given properties and traits, in namespace "ns".
func workerApp(props map[string]any, traits ...oam.Trait) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "ns"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "w", Type: "worker", Properties: props, Traits: traits,
		}}},
	}
}

// errOriginSeen stops the transform once originProbeRule has recorded what it
// was handed; the tests below only need the lowering round, not the build.
var errOriginSeen = stderrors.New("origin probe: seen")

// originProbeRule is a trait lowering rule that records the component it is
// attached to, as the engine hands it over (LoweringContext.Component).
type originProbeRule struct{ seen *oam.Component }

func (originProbeRule) TraitType() string { return "origin-probe" }

func (r originProbeRule) LowerTrait(_ *oam.Trait, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	*r.seen = *lctx.Component
	return oam.LoweringResult{}, errOriginSeen
}

// TestWorkerRule_OriginRule asserts the provenance the engine stamps on the
// deployment component the worker rule emits: Origin.Rule names the rule,
// "component/worker", and every other Origin field still names the AUTHORED
// worker — the move to a lowering rule changes nothing else about where the
// component came from. The probe trait is authored on the worker and forwarded
// by the rule, so on the next round it sees the emitted component.
func TestWorkerRule_OriginRule(t *testing.T) {
	var seen oam.Component
	tr := newBuiltinTransformer()
	tr.RegisterTraitLowering(originProbeRule{seen: &seen})

	_, err := tr.Transform(workerApp(map[string]any{"image": "nginx:1"}, oam.Trait{Type: "origin-probe"}),
		oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	if !stderrors.Is(err, errOriginSeen) {
		t.Fatalf("expected the probe to run, got: %v", err)
	}
	if seen.Name != "w" || seen.Type != "deployment" {
		t.Fatalf("probe saw component %q (type %q), want w (type deployment)", seen.Name, seen.Type)
	}
	got, ok := seen.Origin()
	if !ok {
		t.Fatal("the emitted component carries no Origin")
	}
	want := oam.Origin{
		Document: "app", DocumentKind: "Application", Namespace: "ns",
		Component: "w", ComponentType: "worker", Index: 0,
		Rule: "component/worker",
	}
	if got != want {
		t.Errorf("Origin = %+v\nwant   %+v", got, want)
	}
}

// TestWorkerRule_ParseErrorPrefix pins the one deliberate difference in how a
// worker is refused. The cause text is the former handler's, unchanged; the
// prefix is now the lowering engine's, naming the component's type and
// document. The former handler's error read:
//
//	component "w": image "UPPER CASE" rejected: could not parse reference: UPPER CASE
func TestWorkerRule_ParseErrorPrefix(t *testing.T) {
	_, err := newBuiltinTransformer().Transform(workerApp(map[string]any{"image": "UPPER CASE"}),
		oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	const want = `lowering document "app" in document "app" (kind "Application"): ` +
		`component "w" (type "worker") in document "app" (kind "Application"): ` +
		`image "UPPER CASE" rejected: could not parse reference: UPPER CASE`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant  %s", err, want)
	}
	var le *oam.LoweringError
	if !stderrors.As(err, &le) {
		t.Errorf("err is %T, want it to carry *oam.LoweringError", err)
	}
}

// TestWorkerRule_ForwardedTraitKeepsCapabilityEnforcement: the worker rule
// forwards authored traits next to the topology-spread trait it synthesizes, so
// the forwarded ones must stay authored traits — still subject to the
// required-capability check — rather than being sealed as the rule's own
// output. expose on a worker, with no expose capability in the profile, must
// fail with ErrMissingCapability exactly as it did before worker was a rule.
func TestWorkerRule_ForwardedTraitKeepsCapabilityEnforcement(t *testing.T) {
	app := workerApp(map[string]any{"image": "nginx:1"},
		oam.Trait{Type: "expose", Properties: map[string]any{"hostnames": []any{"w.example.com"}}})
	_, err := newBuiltinTransformer().Transform(app, oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	if !stderrors.Is(err, oam.ErrMissingCapability) {
		t.Errorf("expected ErrMissingCapability for expose on a worker with no capability, got: %v", err)
	}
}
