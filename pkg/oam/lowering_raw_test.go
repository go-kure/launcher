package oam

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

// --- fixtures --------------------------------------------------------------

// testRawDoc is a whole-noun higher-level kind: its spec carries authored fields
// (image, hostname, database.version) that have no ApplicationSpec home at all, so a
// document of this shape cannot survive Parse/ParseWithExtraTypes and is reachable
// only from LowerRaws.
type testRawDoc struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   Metadata    `yaml:"metadata"`
	Spec       testRawSpec `yaml:"spec"`
}

type testRawSpec struct {
	Image    string          `yaml:"image"`
	Hostname string          `yaml:"hostname,omitempty"`
	Database testRawDatabase `yaml:"database,omitempty"`
}

type testRawDatabase struct {
	Version string `yaml:"version,omitempty"`
}

// testRawRule implements RawDocumentLoweringRule and nothing else — it cannot also
// implement DocumentLoweringRule, since the two LowerDocument signatures differ.
type testRawRule struct {
	kind string
	// emit is how many documents LowerDocument emits; 0 means 1.
	emit int
	// nameBase overrides the base handed to NameAllocator.Name for every emitted
	// document; "" means the authored document's own name. Two rules sharing a
	// nameBase generate identical child names for different authored documents,
	// which is what the cross-document collision test needs.
	nameBase string
	// nameSuffixes overrides the per-document suffixes; nil means "1", "2", ...
	nameSuffixes []string
	// childKind is the kind stamped on every emitted document; "" means the
	// terminal kind. A non-terminal value no rule claims settles unlowered and is
	// caught by the post-fixpoint validation pass.
	childKind string
	// compType is the type of the single component each emitted document carries;
	// "" means "webservice".
	compType string
	// decodeErr / lowerErr make the corresponding method fail.
	decodeErr bool
	lowerErr  bool
	// decodes counts DecodeDocument calls when non-nil.
	decodes *int
}

func (r testRawRule) Kind() string { return r.kind }

func (r testRawRule) DecodeDocument(raw []byte) (any, error) {
	if r.decodes != nil {
		*r.decodes++
	}
	if r.decodeErr {
		return nil, errors.New("testRawRule: decode refused")
	}
	var doc testRawDoc
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r testRawRule) LowerDocument(doc any, lctx LoweringContext) (LoweringResult, error) {
	src, ok := doc.(*testRawDoc)
	if !ok {
		return LoweringResult{}, errors.Errorf("testRawRule: unexpected decode target %T", doc)
	}
	if r.lowerErr {
		return LoweringResult{}, errors.New("testRawRule: lowering refused")
	}

	n := r.emit
	if n == 0 {
		n = 1
	}
	base := r.nameBase
	if base == "" {
		base = src.Metadata.Name
	}
	kind := r.childKind
	if kind == "" {
		kind = terminalDocumentKind
	}
	compType := r.compType
	if compType == "" {
		compType = "webservice"
	}

	docs := make([]Application, 0, n)
	for i := range n {
		suffix := strconv.Itoa(i + 1)
		if i < len(r.nameSuffixes) {
			suffix = r.nameSuffixes[i]
		}
		name, err := lctx.Namer.Name(base, suffix, lctx.Origin)
		if err != nil {
			return LoweringResult{}, err
		}
		// Every authored field of the higher-level kind lands in the emitted
		// component's properties — the whole point of the raw entry point.
		props := map[string]any{"image": src.Spec.Image}
		if src.Spec.Hostname != "" {
			props["hostname"] = src.Spec.Hostname
		}
		if src.Spec.Database.Version != "" {
			props["databaseVersion"] = src.Spec.Database.Version
		}
		docs = append(docs, Application{
			APIVersion: SupportedAPIVersion,
			Kind:       kind,
			Metadata:   Metadata{Name: name},
			Spec: ApplicationSpec{
				Components: []Component{{Name: "web", Type: compType, Properties: props}},
			},
		})
	}
	return LoweringResult{Documents: docs}, nil
}

// boomComponentRule fails at component position, in a round strictly after the raw
// documents were decoded and first lowered.
type boomComponentRule struct{}

func (boomComponentRule) ComponentType() string { return "boom" }

func (boomComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, errors.New("boom")
}

// passThroughYAML carries comments and unusual indentation, so a re-serialization
// round-trip would visibly change it.
const passThroughYAML = `# authored by hand — keep me exactly as written
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
    name: untouched
spec:
    components:
        - name: web    # trailing comment
          type: webservice
          properties:
              image: nginx
`

func rawWebApplication(name string) json.RawMessage {
	return json.RawMessage("apiVersion: " + SupportedAPIVersion + "\nkind: WebApplication\nmetadata:\n  name: " + name +
		"\nspec:\n  image: nginx:1.27\n  hostname: " + name + ".example.com\n  database:\n    version: \"16\"\n")
}

func rawOfKind(kind, name string) json.RawMessage {
	return json.RawMessage("apiVersion: " + SupportedAPIVersion + "\nkind: " + kind + "\nmetadata:\n  name: " + name +
		"\nspec:\n  image: nginx\n")
}

// --- tests -----------------------------------------------------------------

// TestLowerRaws_PassThroughIsByteIdentical proves an input whose kind no raw rule
// claims is returned exactly as authored: never probed beyond the envelope, never
// decoded, never re-serialized. The splice-path variant of the same guarantee (a
// pass-through input sitting between two lowered ones) is
// TestLowerRaws_SlotSplicePreservesOrder.
func TestLowerRaws_PassThroughIsByteIdentical(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})

	in := []json.RawMessage{json.RawMessage(passThroughYAML)}
	out, err := tr.LowerRaws(in, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output document, got %d", len(out))
	}
	if string(out[0]) != string(in[0]) {
		t.Fatalf("pass-through document was rewritten:\n--- want ---\n%s\n--- got ---\n%s", in[0], out[0])
	}
}

// TestLowerRaws_DifferentAPIVersion_PassesThrough is the round-3 Codex regression:
// an unrelated resource that happens to share a registered kind string ("WebApplication")
// but carries a foreign apiVersion must be preserved byte-for-byte, not claimed by a
// decoder that was never meant for it. Dispatch must key on (apiVersion, kind), not
// kind alone.
func TestLowerRaws_DifferentAPIVersion_PassesThrough(t *testing.T) {
	tr := NewTransformer(nil, nil)
	var decodes int
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", decodes: &decodes})

	foreign := json.RawMessage("apiVersion: unrelated.example.com/v1\nkind: WebApplication\nmetadata:\n  name: not-ours\nspec:\n  whatever: true\n")
	out, err := tr.LowerRaws([]json.RawMessage{foreign}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output document, got %d", len(out))
	}
	if string(out[0]) != string(foreign) {
		t.Fatalf("foreign-apiVersion document was rewritten:\n--- want ---\n%s\n--- got ---\n%s", foreign, out[0])
	}
	if decodes != 0 {
		t.Fatalf("expected DecodeDocument to never be called for a foreign apiVersion, got %d call(s)", decodes)
	}
}

// TestLowerRaws_DecodeTargetFieldsSurvive proves the gap the in-transform path
// structurally cannot cross: authored fields with no ApplicationSpec home reach the
// rule's own decode target and land in the emitted components' properties.
func TestLowerRaws_DecodeTargetFieldsSurvive(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output document, got %d", len(out))
	}

	var got Application
	if err := yaml.Unmarshal(out[0], &got); err != nil {
		t.Fatalf("output is not a decodable Application: %v\n%s", err, out[0])
	}
	if got.Kind != terminalDocumentKind {
		t.Errorf("output kind = %q, want %q", got.Kind, terminalDocumentKind)
	}
	if len(got.Spec.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(got.Spec.Components))
	}
	props := got.Spec.Components[0].Properties
	for key, want := range map[string]string{
		"image":           "nginx:1.27",
		"hostname":        "shop.example.com",
		"databaseVersion": "16",
	} {
		if props[key] != want {
			t.Errorf("component property %q = %v, want %q", key, props[key], want)
		}
	}
}

// TestLowerRaws_CrossDocumentNameCollision proves D2 collision detection spans the
// whole call: two DIFFERENTLY named raw inputs whose rules independently generate the
// same child name fail, naming both origins. (Two inputs sharing one name+kind never
// reach the NameAllocator — they fail earlier, see
// TestLowerRaws_DuplicateOrigin_RejectsBeforeLowering.)
func TestLowerRaws_CrossDocumentNameCollision(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", nameBase: "shared", nameSuffixes: []string{"db"}})

	_, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("x"), rawWebApplication("y")}, TransformContext{})
	if err == nil {
		t.Fatal("expected a generated-name collision error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "shared-db") {
		t.Errorf("expected the error to name the colliding generated name %q, got: %v", "shared-db", msg)
	}
	if !strings.Contains(msg, `document "x"`) || !strings.Contains(msg, `document "y"`) {
		t.Fatalf("expected the error to name both colliding origins (x and y), got: %v", msg)
	}
}

// TestLowerRaws_DuplicateOrigin_RejectsBeforeLowering proves the duplicate-origin
// check short-circuits: two raw inputs sharing one authored name+kind are rejected
// before either rule's DecodeDocument runs, so the shared NameAllocator never has to
// distinguish two different documents that happen to share an Origin.
func TestLowerRaws_DuplicateOrigin_RejectsBeforeLowering(t *testing.T) {
	decodes := 0
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", decodes: &decodes})

	_, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("x"), rawWebApplication("x")}, TransformContext{})
	if err == nil {
		t.Fatal("expected a duplicate-authored-document error")
	}
	if !strings.Contains(err.Error(), "duplicate authored document") {
		t.Fatalf("expected a duplicate-authored-document error, got: %v", err)
	}
	if decodes != 0 {
		t.Fatalf("expected DecodeDocument never to run, got %d call(s)", decodes)
	}
}

// TestLowerRaws_SlotSplicePreservesOrder proves output is spliced back on slot, not on
// Origin and not on position within the settled set: two DIFFERENT lowered inputs
// interleaved with pass-throughs, one emitting 2 documents and one emitting 1.
func TestLowerRaws_SlotSplicePreservesOrder(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "MultiApp", emit: 2})
	tr.RegisterRawDocumentLowering(testRawRule{kind: "SingleApp", emit: 1})

	p1 := json.RawMessage("# p1\napiVersion: " + SupportedAPIVersion + "\nkind: Application\nmetadata:\n  name: p1\nspec:\n  components:\n    - name: web\n      type: webservice\n      properties:\n        image: nginx\n")
	p2 := json.RawMessage("# p2\napiVersion: " + SupportedAPIVersion + "\nkind: Application\nmetadata:\n  name: p2\nspec:\n  components:\n    - name: web\n      type: webservice\n      properties:\n        image: nginx\n")
	p3 := json.RawMessage("# p3\napiVersion: " + SupportedAPIVersion + "\nkind: Application\nmetadata:\n  name: p3\nspec:\n  components:\n    - name: web\n      type: webservice\n      properties:\n        image: nginx\n")
	in := []json.RawMessage{p1, rawOfKind("MultiApp", "a"), p2, rawOfKind("SingleApp", "b"), p3}

	out, err := tr.LowerRaws(in, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 6 {
		t.Fatalf("expected 6 output documents (3 pass-through + 2 from a + 1 from b), got %d", len(out))
	}

	for _, tc := range []struct {
		index int
		want  json.RawMessage
		label string
	}{
		{0, p1, "P1"},
		{3, p2, "P2"},
		{5, p3, "P3"},
	} {
		if string(out[tc.index]) != string(tc.want) {
			t.Errorf("output[%d] (%s) is not byte-identical to its input:\n--- want ---\n%s\n--- got ---\n%s",
				tc.index, tc.label, tc.want, out[tc.index])
		}
	}

	for _, tc := range []struct {
		index int
		want  string
	}{
		{1, "a-1"},
		{2, "a-2"},
		{4, "b-1"},
	} {
		var got Application
		if err := yaml.Unmarshal(out[tc.index], &got); err != nil {
			t.Fatalf("output[%d] is not a decodable Application: %v", tc.index, err)
		}
		if got.Metadata.Name != tc.want {
			t.Errorf("output[%d] name = %q, want %q — a document landed in the wrong slot", tc.index, got.Metadata.Name, tc.want)
		}
	}
}

// TestLowerRaws_MidLoopErrorNamesTheRightDocument proves per-document attribution for
// the first failure mode: an error raised mid-fixpoint by a rule, in a round after
// both raw inputs decoded and lowered successfully.
func TestLowerRaws_MidLoopErrorNamesTheRightDocument(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "GoodApp"})
	tr.RegisterRawDocumentLowering(testRawRule{kind: "BoomApp", compType: "boom"})
	tr.RegisterComponentLowering(boomComponentRule{})

	_, err := tr.LowerRaws([]json.RawMessage{rawOfKind("GoodApp", "first"), rawOfKind("BoomApp", "second")}, TransformContext{})
	if err == nil {
		t.Fatal("expected the second document's descendant to fail")
	}
	var loweringErr *LoweringError
	if !stderrors.As(err, &loweringErr) {
		t.Fatalf("expected *LoweringError, got %T: %v", err, err)
	}
	if loweringErr.Origin.Document != "second" {
		t.Fatalf("error attributed to document %q, want %q", loweringErr.Origin.Document, "second")
	}
}

// TestLowerRaws_SettledValidationNamesTheRightDocument proves per-document
// attribution for the second failure mode: the post-settle validation pass. This is
// the failure a batched validation pass could not attribute at all.
func TestLowerRaws_SettledValidationNamesTheRightDocument(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "GoodApp"})
	tr.RegisterRawDocumentLowering(testRawRule{kind: "LeftoverApp", childKind: "StillHigher"})

	_, err := tr.LowerRaws([]json.RawMessage{rawOfKind("GoodApp", "first"), rawOfKind("LeftoverApp", "second")}, TransformContext{})
	if err == nil {
		t.Fatal("expected the second document's settled output to fail validation")
	}
	var loweringErr *LoweringError
	if !stderrors.As(err, &loweringErr) {
		t.Fatalf("expected *LoweringError, got %T: %v", err, err)
	}
	if loweringErr.Origin.Document != "second" {
		t.Fatalf("error attributed to document %q, want %q", loweringErr.Origin.Document, "second")
	}
}

// TestLowerRaws_DepthBudgetMatchesInTransform is the regression test for round-0
// accounting: a raw document's decode+first-lower IS round 0 of the shared run, so
// the raw path burns exactly the same round budget as the in-transform path. A
// decode+lower pre-step outside the shared loop would give the raw path one round
// more.
func TestLowerRaws_DepthBudgetMatchesInTransform(t *testing.T) {
	rawTr := NewTransformer(nil, nil)
	rawTr.RegisterRawDocumentLowering(testRawRule{kind: "LoopApp", compType: "loopy"})
	rawTr.RegisterComponentLowering(loopyRule{})

	_, rawErr := rawTr.LowerRaws([]json.RawMessage{rawOfKind("LoopApp", "loopy-raw")}, TransformContext{})
	if rawErr == nil {
		t.Fatal("expected the raw path to hit the depth limit")
	}

	inTr := NewTransformer(nil, nil)
	inTr.RegisterComponentLowering(loopyRule{})
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "loopy-in-transform"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "loopy", Properties: map[string]any{}}},
		},
	}
	_, inErr := inTr.lower(app, TransformContext{})
	if inErr == nil {
		t.Fatal("expected the in-transform path to hit the depth limit")
	}

	var rawLowering, inLowering *LoweringError
	if !stderrors.As(rawErr, &rawLowering) {
		t.Fatalf("raw path: expected *LoweringError, got %T: %v", rawErr, rawErr)
	}
	if !stderrors.As(inErr, &inLowering) {
		t.Fatalf("in-transform path: expected *LoweringError, got %T: %v", inErr, inErr)
	}
	if !stderrors.Is(rawLowering.Cause, ErrLoweringDepthExceeded) {
		t.Fatalf("raw path: expected ErrLoweringDepthExceeded, got: %v", rawLowering.Cause)
	}
	if !stderrors.Is(inLowering.Cause, ErrLoweringDepthExceeded) {
		t.Fatalf("in-transform path: expected ErrLoweringDepthExceeded, got: %v", inLowering.Cause)
	}
	if len(rawLowering.Chain) != len(inLowering.Chain) {
		t.Fatalf("round budgets differ: raw path recorded %d rounds, in-transform path %d",
			len(rawLowering.Chain), len(inLowering.Chain))
	}
	if len(rawLowering.Chain) != MaxLoweringDepth {
		t.Fatalf("expected both paths to record %d rounds, got %d", MaxLoweringDepth, len(rawLowering.Chain))
	}
}

// TestLowerRaws_NoRawRulesRegistered_ReturnsInputUnchanged proves the raw-path
// analogue of the pointer-identity guarantee: with only in-transform rules
// registered, LowerRaws touches nothing.
func TestLowerRaws_NoRawRulesRegistered_ReturnsInputUnchanged(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(testDocRule{kind: "OrderedApplication"})

	in := []json.RawMessage{json.RawMessage(passThroughYAML), rawWebApplication("shop")}
	out, err := tr.LowerRaws(in, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("expected %d output documents, got %d", len(in), len(out))
	}
	for i := range in {
		if string(out[i]) != string(in[i]) {
			t.Errorf("input %d was rewritten:\n--- want ---\n%s\n--- got ---\n%s", i, in[i], out[i])
		}
	}
}
