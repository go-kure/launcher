package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
)

// loweringOutputRule is a ComponentLoweringRule that resolves the names it
// lowers with and declares outputs, so the transform can hold one against the
// other. kinds are the kinds it resolves under one role and default, in order.
type loweringOutputRule struct {
	typeName string
	kinds    []schema.GroupKind
	declared func(c *Component, lctx LoweringContext) ([]OutputDecl, error)
}

func (r loweringOutputRule) ComponentType() string { return r.typeName }

func (r loweringOutputRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	for _, kind := range r.kinds {
		if _, err := lctx.ResolveName(c.Name, "cluster", NameSpec{Role: NameRolePostgresqlCluster, Kind: kind}); err != nil {
			return LoweringResult{}, err
		}
	}
	return LoweringResult{Components: []Component{{Name: c.Name + "-out", Type: "kind-a", Properties: map[string]any{}}}}, nil
}

func (r loweringOutputRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	return r.declared(c, lctx)
}

// declareResolved declares one output per kind, each named the way the lowering
// names it.
func declareResolved(kinds ...schema.GroupKind) func(*Component, LoweringContext) ([]OutputDecl, error) {
	return func(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
		var decls []OutputDecl
		for _, kind := range kinds {
			name, err := lctx.ResolveName(c.Name, "cluster", NameSpec{Role: NameRolePostgresqlCluster, Kind: kind})
			if err != nil {
				return nil, err
			}
			decls = append(decls, OutputDecl{Key: strings.ToLower(kind.Kind), Role: NameRolePostgresqlCluster, Kind: kind, Default: c.Name + "-cluster", Name: name})
		}
		return decls, nil
	}
}

func outputsTransformer(rule loweringOutputRule) *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(rule)
	return tr
}

// transformEverywhere runs app through the three entry points and returns what
// each said, in the order Transform, TransformWithPolicy, TransformWithProvenance.
func transformEverywhere(tr *Transformer, ctx TransformContext) []error {
	app := func() *Application { return terminalApp(Component{Name: "pg", Type: "db"}) }
	_, e1 := tr.Transform(app(), ctx)
	_, _, e2 := tr.TransformWithPolicy(app(), ctx)
	_, _, _, e3 := tr.TransformWithProvenance(app(), ctx)
	return []error{e1, e2, e3}
}

func TestOutputCheck_AgreeingRulePasses(t *testing.T) {
	tr := outputsTransformer(loweringOutputRule{typeName: "db", kinds: []schema.GroupKind{outputClusterKind}, declared: declareResolved(outputClusterKind)})
	for i, err := range transformEverywhere(tr, TransformContext{}) {
		if err != nil {
			t.Errorf("entry point %d: %v", i, err)
		}
	}
}

// A rule that declares one name and resolves another fails the build with both
// names, whichever entry point runs it.
func TestOutputCheck_DisagreeingRuleFailsEveryEntryPoint(t *testing.T) {
	wrong := func(c *Component, _ LoweringContext) ([]OutputDecl, error) {
		return []OutputDecl{{Key: "cluster", Role: NameRolePostgresqlCluster, Kind: outputClusterKind, Default: c.Name + "-cluster", Name: "wrong"}}, nil
	}
	tr := outputsTransformer(loweringOutputRule{typeName: "db", kinds: []schema.GroupKind{outputClusterKind}, declared: wrong})
	for i, err := range transformEverywhere(tr, TransformContext{}) {
		if err == nil {
			t.Errorf("entry point %d: want the check to fail", i)
			continue
		}
		for _, want := range []string{`output "cluster"`, `component "pg"`, `"wrong"`, `"pg-cluster"`, "Cluster.postgresql.cnpg.io"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("entry point %d: error %q lacks %s", i, err, want)
			}
		}
	}
}

// A declared output the lowering resolved no name for fails too.
func TestOutputCheck_DeclaredButNeverResolvedFails(t *testing.T) {
	tr := outputsTransformer(loweringOutputRule{typeName: "db", declared: declareResolved(outputClusterKind)})
	for i, err := range transformEverywhere(tr, TransformContext{}) {
		if err == nil || !strings.Contains(err.Error(), `output "cluster"`) || !strings.Contains(err.Error(), "none") {
			t.Errorf("entry point %d: error = %v, want one naming the output and that no name was resolved", i, err)
		}
	}
}

// A rule that resolves a Cluster and a Secret under one component, role and
// default gets one answer per kind from the hook, and each declaration is held
// against the claim of its own kind.
func TestOutputCheck_TwoKindsUnderOneRolePass(t *testing.T) {
	hook := func(req NameRequest) (string, bool) {
		if req.Kind == "Secret" {
			return "secret-name", true
		}
		return "cluster-name", true
	}
	tr := outputsTransformer(loweringOutputRule{
		typeName: "db",
		kinds:    []schema.GroupKind{outputClusterKind, outputSecretKind},
		declared: declareResolved(outputClusterKind, outputSecretKind),
	})
	for i, err := range transformEverywhere(tr, TransformContext{Naming: hook}) {
		if err != nil {
			t.Errorf("entry point %d: %v", i, err)
		}
	}
}

// A rule that resolves two names of one kind, role and default is refused by
// the allocator before the check; a declaration the check cannot tell from a
// second claim is not let through as a match.
func TestCheckRuleOutputs_ExactlyOneClaimMatches(t *testing.T) {
	claim := func(name string) loweredName {
		return loweredName{
			group: outputClusterKind.Group, kind: outputClusterKind.Kind, name: name,
			claim: resolvedNameClaim{owner: nameOwner{component: "pg", role: NameRolePostgresqlCluster, def: "pg-cluster", lowered: true}},
		}
	}
	decls := []OutputDecl{{Key: "cluster", Role: NameRolePostgresqlCluster, Kind: outputClusterKind, Default: "pg-cluster", Name: "a"}}
	outs := []Output{{Key: "cluster", Kind: outputClusterKind, Name: "a"}}

	if err := checkRuleOutputs("pg", decls, outs, []loweredName{claim("a")}); err != nil {
		t.Errorf("one matching claim: %v", err)
	}
	if err := checkRuleOutputs("pg", decls, outs, []loweredName{claim("a"), claim("b")}); err == nil || !strings.Contains(err.Error(), "2") {
		t.Errorf("two matching claims: err = %v, want a refusal counting them", err)
	}
	other := claim("a")
	other.claim.owner.component = "other"
	if err := checkRuleOutputs("pg", decls, outs, []loweredName{other}); err == nil {
		t.Errorf("a claim of another component matched")
	}
	tagged := claim("a")
	tagged.claim.owner.lowered = false
	if err := checkRuleOutputs("pg", decls, outs, []loweredName{tagged}); err == nil {
		t.Errorf("a claim that is not a lowering rule's matched")
	}
	// A derived output names no claim of its own.
	derived := append(decls, OutputDecl{Key: "app_secret", Kind: outputSecretKind, From: "cluster", Derive: func(s string) string { return s + "-app" }})
	derivedOuts := append(outs, Output{Key: "app_secret", Kind: outputSecretKind, Name: "a-app"})
	if err := checkRuleOutputs("pg", derived, derivedOuts, []loweredName{claim("a")}); err != nil {
		t.Errorf("derived output: %v", err)
	}
}

// objectOutputHandler is a kind-component handler that declares its one object
// as an output, under a name the test chooses.
type objectOutputHandler struct {
	typ      string
	declared string // "" declares the component's own object name
}

func (h *objectOutputHandler) CanHandle(t string) bool { return t == h.typ }
func (h *objectOutputHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}
func (h *objectOutputHandler) ComponentObject() (schema.GroupKind, ObjectScope) {
	return schema.GroupKind{Kind: "ConfigMap"}, ObjectScopeNamespaced
}
func (h *objectOutputHandler) Outputs(c *Component, _ LoweringContext) ([]OutputDecl, error) {
	name := h.declared
	if name == "" {
		name = c.ObjectName()
	}
	return []OutputDecl{{Key: "object", Role: NameRoleObject, Kind: schema.GroupKind{Kind: "ConfigMap"}, Default: c.Name, Name: name}}, nil
}

func TestOutputCheck_HandlerKindComponent(t *testing.T) {
	build := func(h *objectOutputHandler, props map[string]any) []error {
		tr := NewTransformer(map[string]ComponentHandler{"cfg": h}, nil)
		app := func() *Application {
			return terminalApp(Component{Name: "settings", Type: "cfg", Properties: props})
		}
		_, e1 := tr.Transform(app(), TransformContext{})
		_, _, e2 := tr.TransformWithPolicy(app(), TransformContext{})
		_, _, _, e3 := tr.TransformWithProvenance(app(), TransformContext{})
		return []error{e1, e2, e3}
	}
	for i, err := range build(&objectOutputHandler{typ: "cfg"}, nil) {
		if err != nil {
			t.Errorf("default name, entry point %d: %v", i, err)
		}
	}
	for i, err := range build(&objectOutputHandler{typ: "cfg"}, map[string]any{ObjectNameProperty: "mine"}) {
		if err != nil {
			t.Errorf("authored name, entry point %d: %v", i, err)
		}
	}
	for i, err := range build(&objectOutputHandler{typ: "cfg", declared: "wrong"}, nil) {
		if err == nil || !strings.Contains(err.Error(), `"wrong"`) || !strings.Contains(err.Error(), `"settings"`) {
			t.Errorf("mismatch, entry point %d: error = %v, want both names", i, err)
		}
	}
}

// The check wraps a provider's own failure with the component it came from.
func TestOutputCheck_ProviderErrorFailsTheBuild(t *testing.T) {
	boom := func(*Component, LoweringContext) ([]OutputDecl, error) { return nil, errors.New("boom") }
	tr := outputsTransformer(loweringOutputRule{typeName: "db", kinds: []schema.GroupKind{outputClusterKind}, declared: boom})
	for i, err := range transformEverywhere(tr, TransformContext{}) {
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("entry point %d: error = %v, want the provider's", i, err)
		}
	}
}
