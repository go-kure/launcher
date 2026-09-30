package oam

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

// versionedContractRawRule is a testRawRule that declares a contract version, so its
// rule identity carries an "@<version>" suffix.
type versionedContractRawRule struct {
	testRawRule
	version string
}

func (r versionedContractRawRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: r.kind, Version: r.version}
}

// unmarshalableValue fails yaml encoding with an error (not a panic), as a value
// a raw rule writes into an unchecked trait can.
type unmarshalableValue struct{}

func (unmarshalableValue) MarshalYAML() (any, error) {
	return nil, errors.New("unmarshalableValue: refused")
}

// outputNames reads metadata.name from each output document.
func outputNames(t *testing.T, out []json.RawMessage) []string {
	t.Helper()
	names := make([]string, len(out))
	for i, raw := range out {
		var env documentEnvelope
		if err := yaml.Unmarshal(raw, &env); err != nil {
			t.Fatalf("output %d does not parse: %v", i, err)
		}
		names[i] = env.Metadata.Name
	}
	return names
}

// TestLowerRawsWithSteps_OneStepPerClaimedInputInInputOrder is go-kure/launcher#390:
// a successful raw lowering reports which rule produced each emitted document. One
// step per claimed input, in input order, naming the rule, the authored document and
// exactly the documents it emitted; a pass-through input has none.
func TestLowerRawsWithSteps_OneStepPerClaimedInputInInputOrder(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "GoodApp", emit: 2})
	tr.RegisterRawDocumentLowering(versionedContractRawRule{testRawRule: testRawRule{kind: "WebApplication"}, version: "v2"})

	in := []json.RawMessage{
		json.RawMessage(passThroughYAML),
		rawOfKind("GoodApp", "alpha"),
		rawWebApplication("beta"),
		// Same kind and name in two namespaces: steps differ only by position.
		rawWebApplicationNS("gamma", "team-a"),
		rawWebApplicationNS("gamma", "team-b"),
	}
	out, steps, err := tr.LowerRawsWithSteps(in, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRawsWithSteps: %v", err)
	}
	names := outputNames(t, out)
	if len(names) != 6 || names[0] != "untouched" {
		t.Fatalf("output names = %v, want the pass-through first and 5 lowered documents", names)
	}

	webRule := "rawdocument/" + SupportedAPIVersion + "/WebApplication@v2"
	want := []LoweringStep{
		{Rule: "rawdocument/" + SupportedAPIVersion + "/GoodApp", Position: PositionDocument, Round: 0, From: "alpha", To: names[1:3]},
		{Rule: webRule, Position: PositionDocument, Round: 0, From: "beta", To: names[3:4]},
		{Rule: webRule, Position: PositionDocument, Round: 0, From: "gamma", To: names[4:5]},
		{Rule: webRule, Position: PositionDocument, Round: 0, From: "gamma", To: names[5:6]},
	}
	if len(steps) != len(want) {
		t.Fatalf("got %d steps, want %d: %+v", len(steps), len(want), steps)
	}
	for i := range want {
		got := steps[i]
		if got.Rule != want[i].Rule || got.Position != want[i].Position || got.Round != want[i].Round ||
			got.From != want[i].From || !slices.Equal(got.To, want[i].To) {
			t.Errorf("step %d = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestLowerRawsWithSteps_LowerRawsReturnsTheSameDocuments pins LowerRaws as the
// step-dropping form of LowerRawsWithSteps: byte-identical output for the same input.
func TestLowerRawsWithSteps_LowerRawsReturnsTheSameDocuments(t *testing.T) {
	newTr := func() *Transformer {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", emit: 2})
		return tr
	}
	in := []json.RawMessage{rawWebApplication("one"), json.RawMessage(passThroughYAML), rawWebApplication("two")}

	plain, err := newTr().LowerRaws(in, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	withSteps, _, err := newTr().LowerRawsWithSteps(in, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRawsWithSteps: %v", err)
	}
	if len(plain) != len(withSteps) {
		t.Fatalf("LowerRaws returned %d documents, LowerRawsWithSteps %d", len(plain), len(withSteps))
	}
	for i := range plain {
		if !bytes.Equal(plain[i], withSteps[i]) {
			t.Errorf("document %d differs:\n--- LowerRaws ---\n%s\n--- LowerRawsWithSteps ---\n%s", i, plain[i], withSteps[i])
		}
	}
}

// TestLowerRawsWithSteps_NoStepsWithoutAClaimOrOnError: nothing claimed (no rules, or
// no matching input) returns the input and nil steps; a failure returns nil steps and
// leaves the failing document's chain on the LoweringError.
func TestLowerRawsWithSteps_NoStepsWithoutAClaimOrOnError(t *testing.T) {
	in := []json.RawMessage{json.RawMessage(passThroughYAML)}

	t.Run("no rules registered", func(t *testing.T) {
		out, steps, err := NewTransformer(nil, nil).LowerRawsWithSteps(in, TransformContext{})
		if err != nil || steps != nil || len(out) != 1 {
			t.Fatalf("got out=%d steps=%v err=%v, want the input back, nil steps, no error", len(out), steps, err)
		}
	})

	t.Run("no input claimed", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})
		out, steps, err := tr.LowerRawsWithSteps(in, TransformContext{})
		if err != nil || steps != nil || len(out) != 1 {
			t.Fatalf("got out=%d steps=%v err=%v, want the input back, nil steps, no error", len(out), steps, err)
		}
	})

	t.Run("error", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(testRawRule{kind: "GoodApp"})
		tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "BadApp"},
			apiVersion: SupportedAPIVersion, emitAPIVersion: "unrelated.example.com/v1"})
		out, steps, err := tr.LowerRawsWithSteps([]json.RawMessage{rawOfKind("GoodApp", "first"), rawOfKind("BadApp", "second")}, TransformContext{})
		if out != nil || steps != nil {
			t.Fatalf("got out=%v steps=%v on error, want both nil", out, steps)
		}
		var lerr *LoweringError
		if !stderrors.As(err, &lerr) {
			t.Fatalf("expected *LoweringError, got %T: %v", err, err)
		}
		if len(lerr.Chain) != 1 || lerr.Chain[0].From != "second" {
			t.Fatalf("error chain = %+v, want the failing document's one step", lerr.Chain)
		}
	})

	// A raw-emitted trait is not schema-checked here, so a value yaml cannot encode
	// passes the emission checks and fails at re-serialization. The error still
	// names the step that emitted it.
	t.Run("re-serialization error", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(testRawRule{kind: "GoodApp"})
		tr.RegisterRawDocumentLowering(rawRuleWithNestedTrait{kind: "WebApplication", nestedTraitType: "anything",
			nestedProps: map[string]any{"bad": unmarshalableValue{}}})
		out, steps, err := tr.LowerRawsWithSteps([]json.RawMessage{rawOfKind("GoodApp", "first"), rawWebApplication("second")}, TransformContext{})
		if out != nil || steps != nil {
			t.Fatalf("got out=%v steps=%v on error, want both nil", out, steps)
		}
		var lerr *LoweringError
		if !stderrors.As(err, &lerr) {
			t.Fatalf("expected *LoweringError, got %T: %v", err, err)
		}
		if len(lerr.Chain) != 1 || lerr.Chain[0].From != "second" || !slices.Equal(lerr.Chain[0].To, []string{"second-lowered"}) {
			t.Fatalf("error chain = %+v, want the step that emitted second-lowered", lerr.Chain)
		}
	})
}
