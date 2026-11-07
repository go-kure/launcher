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
	declared string           // "" declares the component's own object name
	kind     schema.GroupKind // zero declares the kind of the handler's own object, a core ConfigMap
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
	kind := h.kind
	if kind.Empty() {
		kind = schema.GroupKind{Kind: "ConfigMap"}
	}
	return []OutputDecl{{Key: "object", Role: NameRoleObject, Kind: kind, Default: c.Name, Name: name}}, nil
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

// A handler's output names its one object, so its kind is the kind the handler
// declares for that object (ComponentObject), the group included. The right
// name under another kind, or under the same kind of another group, is refused
// by every entry point; the right kind passes.
func TestOutputCheck_HandlerOutputKindMustBeTheObjectsKind(t *testing.T) {
	run := func(h *objectOutputHandler) []error {
		tr := NewTransformer(map[string]ComponentHandler{"cfg": h}, nil)
		app := func() *Application { return terminalApp(Component{Name: "settings", Type: "cfg"}) }
		_, e1 := tr.Transform(app(), TransformContext{})
		_, _, e2 := tr.TransformWithPolicy(app(), TransformContext{})
		_, _, _, e3 := tr.TransformWithProvenance(app(), TransformContext{})
		return []error{e1, e2, e3}
	}
	for i, err := range run(&objectOutputHandler{typ: "cfg", kind: schema.GroupKind{Kind: "ConfigMap"}}) {
		if err != nil {
			t.Errorf("right kind, entry point %d: %v", i, err)
		}
	}
	for _, tc := range []struct {
		name string
		kind schema.GroupKind
		want []string
	}{
		{"another kind", schema.GroupKind{Kind: "Secret"}, []string{"Secret", "ConfigMap"}},
		{"same kind of another group", schema.GroupKind{Group: "example.com", Kind: "ConfigMap"}, []string{"ConfigMap.example.com", "ConfigMap"}},
	} {
		for i, err := range run(&objectOutputHandler{typ: "cfg", kind: tc.kind}) {
			if err == nil {
				t.Errorf("%s, entry point %d: no error, want the kind refused", tc.name, i)
				continue
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%s, entry point %d: error = %v, want it to name %q", tc.name, i, err, w)
				}
			}
			if !strings.Contains(err.Error(), "kind") {
				t.Errorf("%s, entry point %d: error = %v, want a kind refusal", tc.name, i, err)
			}
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

// mutatingOutputRule is a rule whose Outputs rewrites the component it is
// handed, in every layer a copy of the struct alone would share, and whose
// lowering records what it then sees. The probe must hand Outputs a copy no
// write reaches the build through.
type mutatingOutputRule struct {
	seen *[]string
}

func (r mutatingOutputRule) ComponentType() string { return "db" }

func (r mutatingOutputRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	c.Properties["k"] = "mutated"
	c.Properties["nested"].(map[string]any)["x"] = "mutated"
	c.Properties["list"].([]any)[0] = "mutated"
	c.Annotations["a"] = "mutated"
	return declareResolved(outputClusterKind)(c, lctx)
}

func (r mutatingOutputRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.seen = []string{
		c.Properties["k"].(string),
		c.Properties["nested"].(map[string]any)["x"].(string),
		c.Properties["list"].([]any)[0].(string),
		c.Annotations["a"],
	}
	return loweringOutputRule{kinds: []schema.GroupKind{outputClusterKind}}.LowerComponent(c, lctx)
}

// What a provider writes to the component it is asked about stays in the
// query: the lowering sees the component as the document has it.
func TestOutputCheck_ProviderMutationDoesNotReachTheBuild(t *testing.T) {
	var seen []string
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(mutatingOutputRule{seen: &seen})
	app := terminalApp(Component{
		Name: "pg", Type: "db",
		Properties: map[string]any{
			"k":      "orig",
			"nested": map[string]any{"x": "orig"},
			"list":   []any{"orig"},
		},
		Annotations: map[string]string{"a": "orig"},
	})
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	want := []string{"orig", "orig", "orig", "orig"}
	if len(seen) != 4 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] || seen[3] != want[3] {
		t.Errorf("the lowering saw %v, want %v: the provider's writes reached the build", seen, want)
	}
	if app.Spec.Components[0].Properties["k"] != "orig" {
		t.Errorf("the document's component changed: %v", app.Spec.Components[0].Properties)
	}
}

// retainingOutputRule keeps the slice it returned and rewrites it while it
// lowers, to the name the lowering gave: a declaration made wrong at query time
// would then read as right. The check holds the declarations as they were
// answered.
type retainingOutputRule struct {
	retained *[]OutputDecl
}

func (r retainingOutputRule) ComponentType() string { return "db" }

func (r retainingOutputRule) Outputs(c *Component, _ LoweringContext) ([]OutputDecl, error) {
	decls := []OutputDecl{{Key: "cluster", Role: NameRolePostgresqlCluster, Kind: outputClusterKind, Default: c.Name + "-cluster", Name: "wrong"}}
	*r.retained = decls
	return decls, nil
}

func (r retainingOutputRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	res, err := loweringOutputRule{kinds: []schema.GroupKind{outputClusterKind}}.LowerComponent(c, lctx)
	if err == nil && len(*r.retained) > 0 {
		(*r.retained)[0].Name = c.Name + "-cluster"
	}
	return res, err
}

func TestOutputCheck_RewrittenDeclarationStillFails(t *testing.T) {
	var retained []OutputDecl
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(retainingOutputRule{retained: &retained})
	_, err := tr.Transform(terminalApp(Component{Name: "pg", Type: "db"}), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `"wrong"`) {
		t.Fatalf("Transform = %v; want the mismatch between the declared %q and the lowered name", err, "wrong")
	}
}

// typedMutatingOutputRule rewrites values whose Go type is no map[string]any or
// []any: collections a caller that builds a Component in Go may hold. The probe
// copies every map and slice, whatever its type.
type typedMutatingOutputRule struct {
	seen *[]string
}

func (r typedMutatingOutputRule) ComponentType() string { return "db" }

func (r typedMutatingOutputRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	c.Properties["typed"].(map[string][]string)["k"][0] = "mutated"
	c.Properties["rows"].([]map[string]any)[0]["x"] = "mutated"
	return declareResolved(outputClusterKind)(c, lctx)
}

func (r typedMutatingOutputRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.seen = []string{
		c.Properties["typed"].(map[string][]string)["k"][0],
		c.Properties["rows"].([]map[string]any)[0]["x"].(string),
	}
	return loweringOutputRule{kinds: []schema.GroupKind{outputClusterKind}}.LowerComponent(c, lctx)
}

func TestOutputCheck_ProviderMutationOfTypedCollectionsDoesNotReachTheBuild(t *testing.T) {
	var seen []string
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(typedMutatingOutputRule{seen: &seen})
	app := terminalApp(Component{
		Name: "pg", Type: "db",
		Properties: map[string]any{
			"typed": map[string][]string{"k": {"orig"}},
			"rows":  []map[string]any{{"x": "orig"}},
		},
	})
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(seen) != 2 || seen[0] != "orig" || seen[1] != "orig" {
		t.Errorf("the lowering saw %v, want [orig orig]: the provider's writes reached the build", seen)
	}
}

// A consumer queries the outputs of a component of its own document before it
// builds: the query leaves that component as it was, for a rule as for a handler.
func TestOutputCheck_QueryBeforeBuildLeavesTheDocumentAlone(t *testing.T) {
	var seen []string
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(mutatingOutputRule{seen: &seen})
	app := terminalApp(Component{
		Name: "pg", Type: "db",
		Properties: map[string]any{
			"k":      "orig",
			"nested": map[string]any{"x": "orig"},
			"list":   []any{"orig"},
		},
		Annotations: map[string]string{"a": "orig"},
	})
	comp := &app.Spec.Components[0]
	if _, err := tr.ComponentOutputs(comp); err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	if _, err := tr.ComponentOutputsNamed("app", comp, nil); err != nil {
		t.Fatalf("ComponentOutputsNamed: %v", err)
	}
	got := []any{comp.Properties["k"], comp.Properties["nested"].(map[string]any)["x"], comp.Properties["list"].([]any)[0], comp.Annotations["a"]}
	for i, v := range got {
		if v != "orig" {
			t.Errorf("after the query the component holds %v (value %d): the provider's writes reached the document", got, i)
		}
	}
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	for i, v := range seen {
		if v != "orig" {
			t.Errorf("the lowering saw %v (value %d) after a query: the provider's writes reached the build", seen, i)
		}
	}
}

// strictOutputHandler is a kind-component handler whose Outputs reads its
// properties the way its own parser does: any key it does not know is refused.
// The labels and annotations of the object are not its properties.
type strictOutputHandler struct {
	objectOutputHandler
	fail bool // Outputs fails whatever it is given
}

func (h *strictOutputHandler) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	if h.fail {
		return nil, errors.New("boom")
	}
	for k := range c.Properties {
		return nil, errors.Errorf("unknown property %q", k)
	}
	return h.objectOutputHandler.Outputs(c, lctx)
}

// The query and the build hand a handler's Outputs the same properties: those
// the handler is given, without the object's authored labels and annotations.
func TestOutputCheck_HandlerSeesAuthoredMetadataAsTheBuildDoes(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"cfg": &strictOutputHandler{objectOutputHandler: objectOutputHandler{typ: "cfg"}}}, nil)
	props := func() map[string]any {
		return map[string]any{
			ObjectLabelsProperty:      map[string]any{"team": "a"},
			ObjectAnnotationsProperty: map[string]any{"note": "b"},
		}
	}
	comp := Component{Name: "settings", Type: "cfg", Properties: props()}
	if _, err := tr.ComponentOutputs(&comp); err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	app := func() *Application {
		return terminalApp(Component{Name: "settings", Type: "cfg", Properties: props()})
	}
	_, e1 := tr.Transform(app(), TransformContext{})
	_, _, e2 := tr.TransformWithPolicy(app(), TransformContext{})
	_, _, _, e3 := tr.TransformWithProvenance(app(), TransformContext{})
	for i, err := range []error{e1, e2, e3} {
		if err != nil {
			t.Errorf("entry point %d: %v: the query answers for the same component", i, err)
		}
	}
}

// Metadata is prepared before the output check runs, as in the query: a component
// that has a bad labels value and a failing provider reports the labels.
func TestOutputCheck_MetadataErrorComesBeforeAProviderFailure(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"cfg": &strictOutputHandler{objectOutputHandler: objectOutputHandler{typ: "cfg"}, fail: true}}, nil)
	app := terminalApp(Component{Name: "settings", Type: "cfg", Properties: map[string]any{ObjectLabelsProperty: "not-a-map"}})
	_, err := tr.Transform(app, TransformContext{})
	if err == nil || !strings.Contains(err.Error(), ObjectLabelsProperty) || strings.Contains(err.Error(), "boom") {
		t.Fatalf("Transform = %v; want the labels refusal, not the provider's failure", err)
	}
}

// A derivation that yields no name is a defect of the provider, refused where
// the declarations are answered: neither build check holds a derived output.
func TestResolveOutputs_RefusesAnEmptyDerivedName(t *testing.T) {
	for _, derived := range []string{"", "  "} {
		decls := []OutputDecl{
			{Key: "cluster", Role: NameRolePostgresqlCluster, Kind: outputClusterKind, Default: "pg", Name: "pg"},
			{Key: "secret", Kind: outputSecretKind, From: "cluster", Derive: func(string) string { return derived }},
		}
		_, err := resolveOutputs(decls)
		if err == nil || !strings.Contains(err.Error(), `output "secret"`) || !strings.Contains(err.Error(), "no name") {
			t.Errorf("a derivation yielding %q: err = %v, want one naming the output and that it has no name", derived, err)
		}
	}
}

// A declaration names the kind of the object, so a kind with no kind name is
// refused where the declarations are answered, as an empty one is: GroupKind
// reads {Group: "example.com"} as not empty, and neither build check holds a
// derived output.
func TestResolveOutputs_RefusesAKindWithNoKindName(t *testing.T) {
	cluster := OutputDecl{Key: "cluster", Role: NameRolePostgresqlCluster, Kind: outputClusterKind, Default: "pg", Name: "pg"}
	for _, kc := range []struct {
		name string
		kind schema.GroupKind
	}{
		{"empty", schema.GroupKind{}},
		{"group only", schema.GroupKind{Group: "example.com"}},
		{"blank kind", schema.GroupKind{Kind: "  "}},
		{"group and blank kind", schema.GroupKind{Group: "example.com", Kind: "\t"}},
	} {
		for _, shape := range []struct {
			name string
			decl OutputDecl
		}{
			{"derived", OutputDecl{Key: "secret", Kind: kc.kind, From: "cluster", Derive: func(s string) string { return s + "-app" }}},
			{"allocated", OutputDecl{Key: "secret", Role: NameRoleObject, Kind: kc.kind, Default: "pg", Name: "pg-app"}},
		} {
			t.Run(kc.name+"/"+shape.name, func(t *testing.T) {
				_, err := resolveOutputs([]OutputDecl{cluster, shape.decl})
				if err == nil || !strings.Contains(err.Error(), `output "secret"`) || !strings.Contains(err.Error(), "no kind") {
					t.Errorf("err = %v, want one naming the output and that it declares no kind", err)
				}
			})
		}
	}
}
