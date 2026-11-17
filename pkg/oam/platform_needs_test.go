package oam

import (
	"reflect"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- PolicyResult.PlatformNeeds (go-kure/launcher#1611) ---

// needyComponentHandler is a component handler that declares platform needs.
type needyComponentHandler struct {
	typ   string
	needs []PlatformNeed
}

func (h *needyComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *needyComponentHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}
func (h *needyComponentHandler) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: h.typ, Version: "v1alpha1", PlatformNeeds: h.needs}
}

// conditionalNeedHandler declares a static need and, through ComponentPlatformNeeds,
// a need that depends on the component's properties.
type conditionalNeedHandler struct {
	needyComponentHandler
}

func (h *conditionalNeedHandler) PlatformNeedsFor(c *Component) []PlatformNeed {
	if c.Properties["wants"] == "extra" {
		return []PlatformNeed{{Capability: "static.example.io"}, {Capability: "extra.example.io"}}
	}
	return []PlatformNeed{{Capability: "static.example.io"}}
}

// needyRule lowers its type into the component type "emitted-b" and declares a
// need of its own, which the report must not carry: it reports what is emitted.
type needyRule struct{}

func (needyRule) ComponentType() string { return "rule-a" }
func (needyRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "emitted-b"}}}, nil
}
func (needyRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: "rule-a", Version: "v1alpha1", PlatformNeeds: []PlatformNeed{{Capability: "rule.example.io"}}}
}

func platformNeedsOf(t *testing.T, tr *Transformer, app *Application) []string {
	t.Helper()
	app.APIVersion = SupportedAPIVersion
	_, result, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result.PlatformNeeds
}

// TestPlatformNeeds_ReportedForEmittedComponent: the need a component's handler
// declares is in the result.
func TestPlatformNeeds_ReportedForEmittedComponent(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{
		"needy": &needyComponentHandler{typ: "needy", needs: []PlatformNeed{{Capability: "x.example.io"}}},
	}, nil)
	got := platformNeedsOf(t, tr, singleComponentApp(terminalDocumentKind, "needy", nil))
	if want := []string{"x.example.io"}; !slices.Equal(got, want) {
		t.Errorf("PlatformNeeds = %v, want %v", got, want)
	}
}

// TestPlatformNeeds_ReportedThroughALoweringRule: a rule's own declaration is not
// reported, the declaration of the component it emitted is.
func TestPlatformNeeds_ReportedThroughALoweringRule(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{
		"emitted-b": &needyComponentHandler{typ: "emitted-b", needs: []PlatformNeed{{Capability: "b.example.io"}}},
	}, nil)
	tr.RegisterComponentLowering(needyRule{})
	got := platformNeedsOf(t, tr, singleComponentApp(terminalDocumentKind, "rule-a", nil))
	if want := []string{"b.example.io"}; !slices.Equal(got, want) {
		t.Errorf("PlatformNeeds = %v, want %v (the rule's own need rule.example.io must not appear)", got, want)
	}
	if contracts := tr.HandlerContracts().Components["rule-a"].PlatformNeeds; len(contracts) != 1 || contracts[0].Capability != "rule.example.io" {
		t.Errorf("HandlerContracts() publishes %+v for the rule, want its own declaration", contracts)
	}
}

// TestPlatformNeeds_AHandlerMayAnswerPerComponent: a handler that implements
// ComponentPlatformNeeds is asked for each component and its answer replaces the
// static list: one component of the type reports the extra need, another does not,
// and the static list the handler publishes stays what HandlerContracts shows.
func TestPlatformNeeds_AHandlerMayAnswerPerComponent(t *testing.T) {
	h := &conditionalNeedHandler{needyComponentHandler{typ: "cond", needs: []PlatformNeed{{Capability: "published.example.io"}}}}
	tr := NewTransformer(map[string]ComponentHandler{"cond": h}, nil)

	plain := singleComponentApp(terminalDocumentKind, "cond", nil)
	if got, want := platformNeedsOf(t, tr, plain), []string{"static.example.io"}; !slices.Equal(got, want) {
		t.Errorf("without the property: PlatformNeeds = %v, want %v (the published list is not read)", got, want)
	}

	extra := singleComponentApp(terminalDocumentKind, "cond", map[string]any{"wants": "extra"})
	if got, want := platformNeedsOf(t, tr, extra), []string{"extra.example.io", "static.example.io"}; !slices.Equal(got, want) {
		t.Errorf("with the property: PlatformNeeds = %v, want %v", got, want)
	}

	if got := tr.HandlerContracts().Components["cond"].PlatformNeeds; len(got) != 1 || got[0].Capability != "published.example.io" {
		t.Errorf("HandlerContracts() PlatformNeeds = %+v, want the published declaration unchanged", got)
	}
}

// TestHandlerContracts_PlatformNeeds: HandlerContracts publishes a handler's
// declared PlatformNeeds unchanged, When included, and a handler that declares
// none as nil.
func TestHandlerContracts_PlatformNeeds(t *testing.T) {
	declared := []PlatformNeed{{Capability: "x.example.io"}, {Capability: "y.example.io", When: "backup set"}}
	tr := NewTransformer(map[string]ComponentHandler{
		"needy": &needyComponentHandler{typ: "needy", needs: declared},
		"bare":  &needyComponentHandler{typ: "bare"},
	}, nil)
	set := tr.HandlerContracts().Components
	if !slices.Equal(set["needy"].PlatformNeeds, declared) {
		t.Errorf("needy PlatformNeeds = %+v, want %+v", set["needy"].PlatformNeeds, declared)
	}
	if got := set["bare"].PlatformNeeds; got != nil {
		t.Errorf("bare PlatformNeeds = %#v, want nil", got)
	}
}

// TestPlatformNeeds_SortedDedupedAndNilWhenNone: two components declaring the same
// need give one entry, several come sorted, and no declaration gives nil, not an
// empty list.
func TestPlatformNeeds_SortedDedupedAndNilWhenNone(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{
		"b":     &needyComponentHandler{typ: "b", needs: []PlatformNeed{{Capability: "z.example.io"}, {Capability: "a.example.io", When: "sometimes"}}},
		"c":     &needyComponentHandler{typ: "c", needs: []PlatformNeed{{Capability: "z.example.io"}}},
		"plain": &pipelineComponentHandler{typ: "plain"},
		"empty": &needyComponentHandler{typ: "empty"},
	}, nil)

	app := makeApp("myapp", makeComponent("one", "b"), makeComponent("two", "c"), makeComponent("three", "plain"))
	if got, want := platformNeedsOf(t, tr, app), []string{"a.example.io", "z.example.io"}; !slices.Equal(got, want) {
		t.Errorf("PlatformNeeds = %v, want %v", got, want)
	}

	none := makeApp("myapp", makeComponent("one", "plain"), makeComponent("two", "empty"))
	if got := platformNeedsOf(t, tr, none); got != nil {
		t.Errorf("PlatformNeeds = %#v, want nil when nothing declares a need", got)
	}
}

// TestPlatformNeeds_NotEnforced: a declared need with an empty ClusterProfile
// builds, and the output is the one an application with no declaration gets.
func TestPlatformNeeds_NotEnforced(t *testing.T) {
	needy := NewTransformer(map[string]ComponentHandler{
		"x": &needyComponentHandler{typ: "x", needs: []PlatformNeed{{Capability: "x.example.io"}}},
	}, nil)
	plain := NewTransformer(map[string]ComponentHandler{
		"x": &pipelineComponentHandler{typ: "x"},
	}, nil)
	ctx := TransformContext{Capabilities: map[string]CapabilityBinding{}}

	withNeed, _, err := needy.TransformWithPolicy(singleComponentApp(terminalDocumentKind, "x", nil), ctx)
	if err != nil {
		t.Fatalf("a declared need must not fail the build: %v", err)
	}
	without, _, err := plain.TransformWithPolicy(singleComponentApp(terminalDocumentKind, "x", nil), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(withNeed, without) {
		t.Errorf("a declared need changed the output: %+v vs %+v", withNeed, without)
	}
}
