package oam

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// deprecatedComponentHandler is a pipelineComponentHandler whose contract is
// deprecated.
type deprecatedComponentHandler struct {
	pipelineComponentHandler
	message string
}

func (h *deprecatedComponentHandler) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: h.typ, Version: "v1", Deprecated: true, DeprecationMessage: h.message}
}

// deprecatedTraitHandler is a stubTraitHandler whose contract is deprecated, with no
// message.
type deprecatedTraitHandler struct{ stubTraitHandler }

func (h *deprecatedTraitHandler) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: h.typ, Version: "v1", Deprecated: true}
}

// deprecatedComponentRule is a stubComponentLoweringRule (it lowers to webservice)
// whose contract is deprecated.
type deprecatedComponentRule struct{ stubComponentLoweringRule }

func (r deprecatedComponentRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: r.typ, Version: "v1", Deprecated: true, DeprecationMessage: "use webservice"}
}

// currentContractHandler declares a contract that is not deprecated.
type currentContractHandler struct{ pipelineComponentHandler }

func (h *currentContractHandler) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: h.typ, Version: "v1"}
}

func deprecationTransformer() *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice": &deprecatedComponentHandler{pipelineComponentHandler{typ: "webservice"}, "use web2"},
		"comp":       &currentContractHandler{pipelineComponentHandler{typ: "comp"}},
		"plain":      &pipelineComponentHandler{typ: "plain"},
	}, nil)
	tr.RegisterBuiltinTrait("old", &deprecatedTraitHandler{stubTraitHandler{typ: "old"}})
	tr.RegisterBuiltinTrait("new", &stubTraitHandler{typ: "new"})
	tr.RegisterComponentLowering(deprecatedComponentRule{stubComponentLoweringRule{typ: "legacy"}})
	tr.RegisterComponentLowering(stubComponentLoweringRule{typ: "web"})
	return tr
}

func deprecationApp(components ...Component) *Application {
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "shop"},
		Spec:       ApplicationSpec{Components: components},
	}
}

func deprecationComponent(name, typ string, traits ...string) Component {
	c := Component{Name: name, Type: typ, Properties: map[string]any{}}
	for _, tr := range traits {
		c.Traits = append(c.Traits, Trait{Type: tr, Properties: map[string]any{}})
	}
	return c
}

// TestTransform_DeprecatedTypesWarnOncePerAuthoredUse pins the warning through both
// exported build entry points: one warning per authored component or trait whose
// handler or lowering rule declares Deprecated, in document order, with the
// DeprecationMessage appended when it has one. A type without ContractMetadata, or
// with a current contract, does not warn.
func TestTransform_DeprecatedTypesWarnOncePerAuthoredUse(t *testing.T) {
	app := deprecationApp(
		deprecationComponent("a", "webservice", "old", "new"),
		deprecationComponent("b", "comp", "old"),
		deprecationComponent("c", "plain"),
		deprecationComponent("d", "legacy"),
	)
	want := []string{
		`component "a": type webservice is deprecated: use web2`,
		`component "a": trait type old is deprecated`,
		`component "b": trait type old is deprecated`,
		`component "d": type legacy is deprecated: use webservice`,
	}
	entryPoints := map[string]func(*Transformer) error{
		"Transform": func(tr *Transformer) error {
			_, err := tr.Transform(app, TransformContext{Namespace: "default"})
			return err
		},
		"TransformWithPolicy": func(tr *Transformer) error {
			_, _, err := tr.TransformWithPolicy(app, TransformContext{Namespace: "default"})
			return err
		},
	}
	for name, run := range entryPoints {
		t.Run(name, func(t *testing.T) {
			tr := deprecationTransformer()
			var got []string
			tr.SetWarningHandler(func(msg string) { got = append(got, msg) })
			if err := run(tr); err != nil {
				t.Fatalf("a deprecated type must build: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("warnings:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// TestTransform_SynthesizedDeprecatedTypeDoesNotWarn proves the check reads the
// authored document: the "web" rule lowers to a webservice component, whose handler
// is deprecated, but nobody wrote that component, so nothing warns.
func TestTransform_SynthesizedDeprecatedTypeDoesNotWarn(t *testing.T) {
	tr := deprecationTransformer()
	var got []string
	tr.SetWarningHandler(func(msg string) { got = append(got, msg) })
	if _, err := tr.Transform(deprecationApp(deprecationComponent("w", "web")), TransformContext{Namespace: "default"}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a synthesized component warned: %q", got)
	}
}

// TestTransform_DeprecationWarningDoesNotChangeOutput proves a deprecation is a
// warning only: the cluster is the same with and without a warning handler.
func TestTransform_DeprecationWarningDoesNotChangeOutput(t *testing.T) {
	app := deprecationApp(
		deprecationComponent("a", "webservice", "old"),
		deprecationComponent("d", "legacy"),
	)
	ctx := TransformContext{Namespace: "default"}

	silent, err := deprecationTransformer().Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform without a warning handler: %v", err)
	}
	tr := deprecationTransformer()
	warnings := 0
	tr.SetWarningHandler(func(string) { warnings++ })
	warned, err := tr.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform with a warning handler: %v", err)
	}
	if warnings == 0 {
		t.Fatal("expected deprecation warnings; the comparison below would prove nothing")
	}
	if !reflect.DeepEqual(silent, warned) {
		t.Error("the cluster differs with and without a warning handler")
	}
}

// TestLowerRaws_DeprecatedTypeWarnsOnceInTransform covers the raw entry point:
// LowerRaws only rewrites authored input, so it warns nothing itself; the document it
// emits re-enters Transform as authored and warns there, once.
func TestLowerRaws_DeprecatedTypeWarnsOnceInTransform(t *testing.T) {
	tr := deprecationTransformer()
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})
	var got []string
	tr.SetWarningHandler(func(msg string) { got = append(got, msg) })

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("LowerRaws warned; the warning belongs to Transform: %q", got)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output document, got %d", len(out))
	}
	app := parseLoweredOutput(t, tr, out[0])
	if _, err := tr.Transform(app, TransformContext{Namespace: "default"}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(got) != len(app.Spec.Components) || len(got) == 0 {
		t.Fatalf("expected one warning per emitted webservice component, got %q", got)
	}
	for _, msg := range got {
		if !strings.HasSuffix(msg, `: type webservice is deprecated: use web2`) {
			t.Errorf("unexpected warning %q", msg)
		}
	}
}
