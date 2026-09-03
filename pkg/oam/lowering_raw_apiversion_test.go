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

func (r versionedRawRule) RawDocumentAPIVersion() string { return r.apiVersion }

// accessorRawRule carries a plain APIVersion() string accessor — the shape an
// embedded document type or a Kubernetes-style envelope gives a rule for reasons
// unrelated to the hook — and must NOT be treated as opting in.
type accessorRawRule struct {
	testRawRule
}

func (accessorRawRule) APIVersion() string { return "some.other.group/v1" }

// TestLowerRaws_PlainAPIVersionAccessorIsNotTheHook pins the hook's spelling: a
// rule whose only apiVersion-shaped method is APIVersion() keeps claiming
// SupportedAPIVersion (its documents are still decoded), and its accessor's value
// claims nothing. Structural satisfaction of a generic method name must not re-key
// an existing rule (PR review, go-kure/launcher#379).
func TestLowerRaws_PlainAPIVersionAccessorIsNotTheHook(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(accessorRawRule{testRawRule{kind: "Widget"}})
	if got := rawRuleAPIVersion(accessorRawRule{testRawRule{kind: "Widget"}}); got != SupportedAPIVersion {
		t.Fatalf("rawRuleAPIVersion = %q, want SupportedAPIVersion: a plain APIVersion() accessor opted the rule in", got)
	}
	var (
		_ RawDocumentLoweringRule          = accessorRawRule{}
		_ interface{ APIVersion() string } = accessorRawRule{}
	)
	if _, ok := any(accessorRawRule{}).(RawDocumentAPIVersioner); ok {
		t.Fatal("accessorRawRule satisfies RawDocumentAPIVersioner through APIVersion(); the hook method must be uniquely named")
	}
}

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
// apiVersion rule: a lowered document may carry SupportedAPIVersion or the one group
// its own raw rule was matched under, and nothing else — a rule emitting into any
// other group is a rule bug, reported against the authored document, not a document
// the caller's parser is left to reject. (The other-rule's-group case is
// TestLowerRaws_RuleMayNotSettleUnderAnotherRulesGroup.)
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
// registered rule claims blocks a generated child of the same name — at the claimed
// version and at any other version of that group, since the pass's identity model
// is version-blind — while the same Application under a group no rule claims is a
// foreign resource and does not.
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

	for _, group := range []string{consumerGroup, "consumer.example.com/v1beta1"} {
		_, err := newTransformer().LowerRaws([]json.RawMessage{passThrough(group), rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
		if err == nil {
			t.Fatalf("expected a generated-name collision against the pass-through Application at %s (a claimed group)", group)
		}
		if !strings.Contains(err.Error(), "shop-1") {
			t.Errorf("expected the error for %s to name the colliding identity %q, got: %v", group, "shop-1", err)
		}
	}

	out, err := newTransformer().LowerRaws([]json.RawMessage{passThrough("unrelated.example.com/v1"), rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("expected an unclaimed-group Application not to reserve an identity, got: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 output documents, got %d", len(out))
	}
}

// groupEmittingDocRule is an in-transform DocumentLoweringRule that stamps a chosen
// apiVersion on the one Application it emits — the shape an unrelated rule would
// need to leak a raw-claimed group into the in-transform path.
type groupEmittingDocRule struct {
	kind       string
	apiVersion string
}

func (r groupEmittingDocRule) Kind() string { return r.kind }

func (r groupEmittingDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: r.apiVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name + "-lowered"},
		Spec:       ApplicationSpec{Components: doc.Spec.Components},
	}}}, nil
}

// TestLower_InTransformRuleCannotSettleUnderRawClaimedGroup pins the raw-only scope of
// the hook: registering a raw rule for a consumer group on a Transformer must not
// widen what the in-transform path accepts. A DocumentLoweringRule dispatched from
// lower() that emits that same group is rejected exactly as it was before any raw
// rule existed, because the allowed group travels with each lowering seed and the
// in-transform seed carries none.
func TestLower_InTransformRuleCannotSettleUnderRawClaimedGroup(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(versionedRawRule{
		testRawRule: testRawRule{kind: "WebApplication"},
		apiVersion:  consumerGroup,
	})
	tr.RegisterDocumentLowering(groupEmittingDocRule{kind: "Intent", apiVersion: consumerGroup})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Intent",
		Metadata:   Metadata{Name: "shop"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.27"}}},
		},
	}
	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected the in-transform path to reject a document settled under a group only a raw rule claims")
	}
	if !strings.Contains(err.Error(), `unsupported apiVersion "`+consumerGroup+`"`) {
		t.Errorf("expected an unsupported-apiVersion error, got: %v", err)
	}
	if strings.Contains(err.Error(), "RawDocumentLoweringRule") {
		t.Errorf("the in-transform error must not offer a raw-claimed group as acceptable, got: %v", err)
	}
}

// TestLowerRaws_RuleMayNotSettleUnderAnotherRulesGroup pins the per-seed scope on the
// raw side: a rule claiming group A may settle under A or SupportedAPIVersion, never
// under group B just because another rule on the same Transformer claims B. The
// failure's chain records the raw step under its full "<apiVersion>/<kind>"
// identity, so the two same-kind rules stay distinguishable in provenance.
func TestLowerRaws_RuleMayNotSettleUnderAnotherRulesGroup(t *testing.T) {
	const otherGroup = "other.example.com/v1alpha1"
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(versionedRawRule{
		testRawRule:    testRawRule{kind: "WebApplication"},
		apiVersion:     consumerGroup,
		emitAPIVersion: otherGroup,
	})
	tr.RegisterRawDocumentLowering(versionedRawRule{
		testRawRule: testRawRule{kind: "WebApplication"},
		apiVersion:  otherGroup,
	})

	_, err := tr.LowerRaws([]json.RawMessage{rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
	if err == nil {
		t.Fatal("expected a rule to be refused settling under another rule's group")
	}
	if !strings.Contains(err.Error(), `unsupported apiVersion "`+otherGroup+`"`) || !strings.Contains(err.Error(), `"`+consumerGroup+`"`) {
		t.Errorf("expected the error to name the emitted group and the claiming rule's own group, got: %v", err)
	}
	var lerr *LoweringError
	if !stderrors.As(err, &lerr) {
		t.Fatalf("expected a *LoweringError, got %T: %v", err, err)
	}
	if len(lerr.Chain) != 1 || lerr.Chain[0].Rule != "rawdocument/"+consumerGroup+"/WebApplication" {
		t.Errorf("expected the chain to record the raw step under its (apiVersion, kind) identity, got: %+v", lerr.Chain)
	}
}

// driftingRawRule answers RawDocumentAPIVersion() with whatever its pointer currently holds,
// standing in for a stateful rule that reports one group at registration and
// another later.
type driftingRawRule struct {
	testRawRule
	group *string
}

func (r *driftingRawRule) RawDocumentAPIVersion() string { return *r.group }

func (r *driftingRawRule) LowerDocument(doc any, lctx LoweringContext) (LoweringResult, error) {
	result, err := r.testRawRule.LowerDocument(doc, lctx)
	if err != nil {
		return result, err
	}
	for i := range result.Documents {
		result.Documents[i].APIVersion = *r.group
	}
	return result, nil
}

// TestLowerRaws_SeedGroupIsTheMatchedRegistryKeyNotTheHookReEvaluated pins that
// the group a seed may settle under is the registry pair that dispatched it, never
// a fresh call to RawDocumentAPIVersion(): a rule registered under group A whose hook later
// says B still dispatches for A (the registry is fixed at registration) and is
// refused when it then emits B.
func TestLowerRaws_SeedGroupIsTheMatchedRegistryKeyNotTheHookReEvaluated(t *testing.T) {
	const otherGroup = "other.example.com/v1alpha1"
	group := consumerGroup
	rule := &driftingRawRule{testRawRule: testRawRule{kind: "WebApplication"}, group: &group}
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(rule)
	group = otherGroup // the hook now answers a group the rule never registered under

	_, err := tr.LowerRaws([]json.RawMessage{rawWebApplicationIn(consumerGroup, "shop")}, TransformContext{})
	if err == nil {
		t.Fatal("expected a rule whose hook drifted after registration to be refused settling under the drifted group")
	}
	if !strings.Contains(err.Error(), `unsupported apiVersion "`+otherGroup+`"`) || !strings.Contains(err.Error(), `"`+consumerGroup+`"`) {
		t.Errorf("expected the error to name the drifted group and the registered group, got: %v", err)
	}
}

// TestLowerRaws_SameIdentityInTwoGroupsIsADuplicate pins the identity model as
// group-blind on purpose (see rawDocKey): one LowerRaws call yields one output slice,
// in which (namespace, kind, name) names one resource regardless of the group each
// input was authored under, so the same triple under two claimed groups is a
// duplicate rather than two independent dispatches.
func TestLowerRaws_SameIdentityInTwoGroupsIsADuplicate(t *testing.T) {
	const otherGroup = "other.example.com/v1alpha1"
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "WebApplication"}, apiVersion: consumerGroup})
	tr.RegisterRawDocumentLowering(versionedRawRule{testRawRule: testRawRule{kind: "WebApplication"}, apiVersion: otherGroup})

	_, err := tr.LowerRaws([]json.RawMessage{rawWebApplicationIn(consumerGroup, "shop"), rawWebApplicationIn(otherGroup, "shop")}, TransformContext{})
	if err == nil {
		t.Fatal("expected the same (namespace, kind, name) under two claimed groups to be rejected as a duplicate")
	}
	if !strings.Contains(err.Error(), "duplicate authored document") {
		t.Errorf("expected a duplicate-document error, got: %v", err)
	}
}
