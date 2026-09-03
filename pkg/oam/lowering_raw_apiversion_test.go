package oam

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// yamlUnmarshalStrict decodes one lowered output document with KnownFields(true), so
// a stray field in what LowerRaws re-serialized fails the test instead of vanishing.
func yamlUnmarshalStrict(raw []byte, into *Application) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(into)
}

// consumerGroup stands in for the API group of a downstream consumer that owns its
// own OAM dialect and hands LowerRaws documents declared under it.
const consumerGroup = "consumer.example.com/v1alpha1"

// versionedRawRule is testRawRule plus the RawDocumentAPIVersioner hook: it claims
// its kind under apiVersion rather than SupportedAPIVersion, and stamps the same
// apiVersion on every document it emits — the shape a consumer-owned rule takes so
// its own parser accepts the lowered output.
type versionedRawRule struct {
	testRawRule
	apiVersion string
	// emitAPIVersion overrides the apiVersion stamped on emitted documents; ""
	// means apiVersion itself.
	emitAPIVersion string
}

func (r versionedRawRule) APIVersion() string { return r.apiVersion }

func (r versionedRawRule) LowerDocument(doc any, lctx LoweringContext) (LoweringResult, error) {
	result, err := r.testRawRule.LowerDocument(doc, lctx)
	if err != nil {
		return result, err
	}
	emit := r.emitAPIVersion
	if emit == "" {
		emit = r.apiVersion
	}
	for i := range result.Documents {
		result.Documents[i].APIVersion = emit
	}
	return result, nil
}

func rawWebApplicationIn(group, name string) json.RawMessage {
	return json.RawMessage("apiVersion: " + group + "\nkind: WebApplication\nmetadata:\n  name: " + name +
		"\nspec:\n  image: nginx:1.27\n")
}

// assertRegistrationPanics runs register and fails the test unless it panics with a
// message containing want.
func assertRegistrationPanics(t *testing.T, want string, register func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a panic containing %q, got none", want)
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, want) {
			t.Fatalf("expected a panic containing %q, got: %v", want, r)
		}
	}()
	register()
}

// TestLowerRaws_PerRuleAPIVersion_ClaimsOnlyItsOwnGroup is the regression for the
// silent pass-through a consumer with its own API group hit: with only the
// single-group gate, a document under that group matched a registered kind string
// yet was never claimed, and no signal said a rule existed for it. A rule that
// implements RawDocumentAPIVersioner now claims exactly its (apiVersion, kind) pair;
// the same kind under SupportedAPIVersion, which it did not claim, still passes
// through byte-identical and is never decoded.
func TestLowerRaws_PerRuleAPIVersion_ClaimsOnlyItsOwnGroup(t *testing.T) {
	tr := NewTransformer(nil, nil)
	var decodes int
	tr.RegisterRawDocumentLowering(versionedRawRule{
		testRawRule: testRawRule{kind: "WebApplication", decodes: &decodes},
		apiVersion:  consumerGroup,
	})

	theirs := rawWebApplication("shop") // SupportedAPIVersion, same kind, same name
	ours := rawWebApplicationIn(consumerGroup, "shop")
	out, err := tr.LowerRaws([]json.RawMessage{theirs, ours}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 output documents, got %d", len(out))
	}
	if string(out[0]) != string(theirs) {
		t.Errorf("SupportedAPIVersion document of an unclaimed pair was rewritten:\n--- want ---\n%s\n--- got ---\n%s", theirs, out[0])
	}
	if decodes != 1 {
		t.Errorf("expected DecodeDocument to run once (for the claimed group only), got %d call(s)", decodes)
	}
	var lowered Application
	if err := yamlUnmarshalStrict(out[1], &lowered); err != nil {
		t.Fatalf("lowered output does not decode as an Application: %v\n%s", err, out[1])
	}
	if lowered.Kind != terminalDocumentKind || lowered.APIVersion != consumerGroup {
		t.Errorf("expected a lowered %s under %s, got kind %q apiVersion %q", terminalDocumentKind, consumerGroup, lowered.Kind, lowered.APIVersion)
	}
	if lowered.Metadata.Name != "shop-1" {
		t.Errorf("expected the rule's generated name shop-1, got %q", lowered.Metadata.Name)
	}
}

// TestLowerRaws_RuleWithoutHookStillClaimsSupportedAPIVersionOnly pins the default:
// a rule that does not implement RawDocumentAPIVersioner claims SupportedAPIVersion
// and nothing else, so the consumer-group document passes through untouched.
func TestLowerRaws_RuleWithoutHookStillClaimsSupportedAPIVersionOnly(t *testing.T) {
	tr := NewTransformer(nil, nil)
	var decodes int
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", decodes: &decodes})

	foreign := rawWebApplicationIn(consumerGroup, "shop")
	out, err := tr.LowerRaws([]json.RawMessage{foreign, rawWebApplication("shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if string(out[0]) != string(foreign) {
		t.Errorf("consumer-group document was rewritten by a SupportedAPIVersion-only rule:\n--- want ---\n%s\n--- got ---\n%s", foreign, out[0])
	}
	if decodes != 1 {
		t.Errorf("expected exactly the SupportedAPIVersion document to be decoded, got %d call(s)", decodes)
	}
}

// TestLowerRaws_SameKindUnderTwoGroups_EachRuleClaimsItsOwn proves the registry is
// keyed on the pair: two rules for one kind string under different apiVersions
// coexist, and each document reaches the rule of its own group.
func TestLowerRaws_SameKindUnderTwoGroups_EachRuleClaimsItsOwn(t *testing.T) {
	tr := NewTransformer(nil, nil)
	var launcherDecodes, consumerDecodes int
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", decodes: &launcherDecodes, nameSuffixes: []string{"launcher"}})
	tr.RegisterRawDocumentLowering(versionedRawRule{
		testRawRule: testRawRule{kind: "WebApplication", decodes: &consumerDecodes, nameSuffixes: []string{"consumer"}},
		apiVersion:  consumerGroup,
	})

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("a"), rawWebApplicationIn(consumerGroup, "b")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if launcherDecodes != 1 || consumerDecodes != 1 {
		t.Fatalf("expected one decode per rule, got launcher=%d consumer=%d", launcherDecodes, consumerDecodes)
	}
	var first, second Application
	if err := yamlUnmarshalStrict(out[0], &first); err != nil {
		t.Fatalf("output 0: %v", err)
	}
	if err := yamlUnmarshalStrict(out[1], &second); err != nil {
		t.Fatalf("output 1: %v", err)
	}
	if first.Metadata.Name != "a-launcher" || first.APIVersion != SupportedAPIVersion {
		t.Errorf("slot 0: expected a-launcher under %s, got %q under %q", SupportedAPIVersion, first.Metadata.Name, first.APIVersion)
	}
	if second.Metadata.Name != "b-consumer" || second.APIVersion != consumerGroup {
		t.Errorf("slot 1: expected b-consumer under %s, got %q under %q", consumerGroup, second.Metadata.Name, second.APIVersion)
	}
}

// TestRegisterRawDocumentLowering_APIVersionGuards covers the registration guards the
// pair key adds and the ones it must keep.
func TestRegisterRawDocumentLowering_APIVersionGuards(t *testing.T) {
	t.Run("same pair twice panics", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "WebApplication"}, apiVersion: consumerGroup})
		assertRegistrationPanics(t, "under apiVersion "+consumerGroup, func() {
			tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "WebApplication"}, apiVersion: consumerGroup})
		})
	})
	t.Run("empty apiVersion panics", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		assertRegistrationPanics(t, "empty apiVersion", func() {
			tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "WebApplication"}})
		})
	})
	t.Run("hook returning SupportedAPIVersion collides with a hook-less rule", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})
		assertRegistrationPanics(t, "under apiVersion "+SupportedAPIVersion, func() {
			tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "WebApplication"}, apiVersion: SupportedAPIVersion})
		})
	})
	t.Run("cross-registrar guard stays kind-wide", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "OrderedApplication"}, apiVersion: consumerGroup})
		assertRegistrationPanics(t, "already registered via RegisterRawDocumentLowering", func() {
			tr.RegisterDocumentLowering(testDocRule{kind: "OrderedApplication"})
		})

		tr2 := NewTransformer(nil, nil)
		tr2.RegisterDocumentLowering(testDocRule{kind: "OrderedApplication"})
		assertRegistrationPanics(t, "already registered via RegisterDocumentLowering", func() {
			tr2.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "OrderedApplication"}, apiVersion: consumerGroup})
		})
	})
}

// TestLowerRaws_SettledDocumentMustBeInAClaimedGroup pins the settled-document
// apiVersion rule: a lowered document may carry SupportedAPIVersion or the group of
// any registered raw rule, and nothing else — a rule emitting into a group no rule
// claims is a rule bug, reported against the authored document, not a document the
// caller's parser is left to reject.
func TestLowerRaws_SettledDocumentMustBeInAClaimedGroup(t *testing.T) {
	t.Run("emitting SupportedAPIVersion from a consumer-group rule is accepted", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(versionedRawRule{
			testRawRule:    testRawRule{kind: "WebApplication"},
			apiVersion:     consumerGroup,
			emitAPIVersion: SupportedAPIVersion,
		})
		if _, err := tr.LowerRaws([]json.RawMessage{rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{}); err != nil {
			t.Fatalf("LowerRaws: %v", err)
		}
	})
	t.Run("emitting an unclaimed group is rejected", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(versionedRawRule{
			testRawRule:    testRawRule{kind: "WebApplication"},
			apiVersion:     consumerGroup,
			emitAPIVersion: "unrelated.example.com/v1",
		})
		_, err := tr.LowerRaws([]json.RawMessage{rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
		if err == nil {
			t.Fatal("expected the settled-document validation to reject an unclaimed apiVersion")
		}
		if !strings.Contains(err.Error(), `unsupported apiVersion "unrelated.example.com/v1"`) {
			t.Errorf("expected an unsupported-apiVersion error, got: %v", err)
		}
		var lerr *LoweringError
		if !stderrors.As(err, &lerr) || lerr.Origin.Document != "shop" {
			t.Errorf("expected a *LoweringError attributed to document shop, got: %v", err)
		}
	})
}

// TestLowerRaws_PassThroughInClaimedGroupIsPreReserved extends the pass-through
// identity reservation to a consumer group: an Application authored under a group a
// registered rule claims blocks a generated child of the same name, while the same
// Application under a group no rule claims is a foreign resource and does not.
func TestLowerRaws_PassThroughInClaimedGroupIsPreReserved(t *testing.T) {
	newTransformer := func() *Transformer {
		tr := NewTransformer(nil, nil)
		tr.RegisterRawDocumentLowering(versionedRawRule{
			testRawRule: testRawRule{kind: "WebApplication", nameBase: "shop", nameSuffixes: []string{"1"}},
			apiVersion:  consumerGroup,
		})
		return tr
	}
	passThrough := func(group string) json.RawMessage {
		return json.RawMessage(`{"apiVersion":"` + group + `","kind":"Application","metadata":{"name":"shop-1"},"spec":{"components":[{"name":"web","type":"webservice","properties":{"image":"nginx"}}]}}`)
	}

	_, err := newTransformer().LowerRaws([]json.RawMessage{passThrough(consumerGroup), rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
	if err == nil {
		t.Fatal("expected a generated-name collision against the claimed-group pass-through Application")
	}
	if !strings.Contains(err.Error(), "shop-1") {
		t.Errorf("expected the error to name the colliding identity %q, got: %v", "shop-1", err)
	}

	out, err := newTransformer().LowerRaws([]json.RawMessage{passThrough("unrelated.example.com/v1"), rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("expected an unclaimed-group Application not to reserve an identity, got: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 output documents, got %d", len(out))
	}
}
