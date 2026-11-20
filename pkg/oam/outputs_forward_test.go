package oam

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// forwardRule lowers a component to one component of type cfg, named <name>-inner, and
// declares one forwarded output of it (go-kure/launcher#1643): the output of a rule that is a
// record the emitted component already publishes, held against what the emitted component
// answers once the rule has lowered.
type forwardRule struct {
	decl func(c *Component) OutputDecl
}

func (forwardRule) ComponentType() string { return "wrap" }

func (forwardRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: c.Name + "-inner", Type: "cfg", Properties: map[string]any{}}}}, nil
}

func (r forwardRule) Outputs(c *Component, _ LoweringContext) ([]OutputDecl, error) {
	return []OutputDecl{r.decl(c)}, nil
}

var configMapKind = schema.GroupKind{Kind: "ConfigMap"}

// hookedObjectRule is a lowering rule that names the object it declares for the role object
// through ResolveName, so the Naming hook is asked for it in its own lowering and in its query.
type hookedObjectRule struct{}

func (hookedObjectRule) ComponentType() string { return "hooked-db" }

func (hookedObjectRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	if _, err := lctx.ResolveName(c.Name, "s", NameSpec{Role: NameRoleObject, Kind: configMapKind}); err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{Name: c.Name + "-cfg", Type: "cfg", Properties: map[string]any{}}}}, nil
}

func (hookedObjectRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	name, err := lctx.ResolveName(c.Name, "s", NameSpec{Role: NameRoleObject, Kind: configMapKind})
	if err != nil {
		return nil, err
	}
	return []OutputDecl{{Key: "s", Role: NameRoleObject, Kind: configMapKind, Default: c.Name + "-s", Name: name}}, nil
}

// forwardsHookedRule emits one component of the rule above and forwards its output.
type forwardsHookedRule struct{ forwarded string }

func (forwardsHookedRule) ComponentType() string { return "wrap-hooked" }

func (forwardsHookedRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: c.Name + "-in", Type: "hooked-db", Properties: map[string]any{}}}}, nil
}

func (r forwardsHookedRule) Outputs(c *Component, _ LoweringContext) ([]OutputDecl, error) {
	return []OutputDecl{{Key: "s", Kind: configMapKind, Name: r.forwarded, ForwardOf: c.Name + "-in", ForwardKey: "s"}}, nil
}

// TestForwardedOutput_ARuleAmongTheEmittedKeepsTheHookForTheRoleObject: the build names the object
// of an emitted kind component without the hook, but an emitted rule that resolves the role object
// gets the hook whole, so a forward of its output is held to the name the hook gave.
func TestForwardedOutput_ARuleAmongTheEmittedKeepsTheHookForTheRoleObject(t *testing.T) {
	hook := func(r NameRequest) (string, bool) {
		if r.Role != NameRoleObject {
			return "", false
		}
		return "h-" + r.Default, true
	}
	for name, tc := range map[string]struct {
		forwarded string
		ok        bool
	}{
		"the name the hook gave":  {"h-pg-in-s", true},
		"the name without a hook": {"pg-in-s", false},
	} {
		t.Run(name, func(t *testing.T) {
			tr := NewTransformer(map[string]ComponentHandler{"cfg": &objectOutputHandler{typ: "cfg"}}, nil)
			tr.RegisterComponentLowering(hookedObjectRule{})
			tr.RegisterComponentLowering(forwardsHookedRule{forwarded: tc.forwarded})
			_, err := tr.Transform(terminalApp(Component{Name: "pg", Type: "wrap-hooked"}), TransformContext{Naming: hook})
			if tc.ok && err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("Transform accepted a forward of the unhooked name")
			}
		})
	}
}

// forwardOf is the forwarded declaration that agrees with the lowering: the emitted component
// is pg-inner and it publishes its object under "object", a ConfigMap of that name.
func forwardOf(c *Component) OutputDecl {
	return OutputDecl{Key: "settings", Kind: configMapKind, Name: c.Name + "-inner", ForwardOf: c.Name + "-inner", ForwardKey: "object"}
}

func forwardTransformer(decl func(c *Component) OutputDecl) *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{"cfg": &objectOutputHandler{typ: "cfg"}}, nil)
	tr.RegisterComponentLowering(forwardRule{decl: decl})
	return tr
}

func TestForwardedOutput_AgreeingDeclarationPassesAndAnswers(t *testing.T) {
	tr := forwardTransformer(forwardOf)
	app := func() *Application { return terminalApp(Component{Name: "pg", Type: "wrap"}) }
	if _, err := tr.Transform(app(), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if _, _, err := tr.TransformWithPolicy(app(), TransformContext{}); err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if _, _, _, err := tr.TransformWithProvenance(app(), TransformContext{}); err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	outs, err := tr.ComponentOutputs(&Component{Name: "pg", Type: "wrap"})
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	if len(outs) != 1 || outs[0] != (Output{Key: "settings", Kind: configMapKind, Name: "pg-inner"}) {
		t.Fatalf("ComponentOutputs = %v, want one ConfigMap named pg-inner under settings", outs)
	}
}

// A forwarded declaration that the emitted component does not bear out refuses the build, in
// every entry point, naming the output and what was wrong.
func TestForwardedOutput_DisagreeingDeclarationFailsTheBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		decl  func(c *Component) OutputDecl
		wants []string
	}{
		"no such emitted component": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.ForwardOf = "nope"; return d },
			[]string{`output "settings"`, `component "pg"`, `"nope"`},
		},
		"the emitted component publishes no such key": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.ForwardKey = "missing"; return d },
			[]string{`output "settings"`, `"pg-inner"`, `"missing"`},
		},
		"another name": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.Name = "other"; return d },
			[]string{`output "settings"`, `"other"`, `"pg-inner"`},
		},
		"another kind": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.Kind = schema.GroupKind{Kind: "Secret"}; return d },
			[]string{`output "settings"`, "Secret", "ConfigMap"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr := forwardTransformer(tc.decl)
			app := func() *Application { return terminalApp(Component{Name: "pg", Type: "wrap"}) }
			_, e1 := tr.Transform(app(), TransformContext{})
			_, _, e2 := tr.TransformWithPolicy(app(), TransformContext{})
			_, _, _, e3 := tr.TransformWithProvenance(app(), TransformContext{})
			for i, err := range []error{e1, e2, e3} {
				if err == nil {
					t.Errorf("entry point %d: the build accepted %s", i, name)
					continue
				}
				for _, want := range tc.wants {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("entry point %d: error %q lacks %s", i, err, want)
					}
				}
			}
		})
	}
}

// The shape of a forwarded declaration is checked where it is answered: it names the component
// and the key it forwards, a name and a kind, and nothing of an allocated or derived output.
func TestForwardedOutput_ShapeIsRefusedAtTheQuery(t *testing.T) {
	for name, tc := range map[string]struct {
		decl func(c *Component) OutputDecl
		want string
	}{
		"no forwarded key": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.ForwardKey = ""; return d }, "forward"},
		"no forwarded component": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.ForwardOf = ""; return d }, "forward"},
		"no name": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.Name = ""; return d }, "no name"},
		"a role": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.Role = NameRoleObject; return d }, "role"},
		"a default": {
			func(c *Component) OutputDecl { d := forwardOf(c); d.Default = "x"; return d }, "default"},
		"a derivation": {
			func(c *Component) OutputDecl {
				d := forwardOf(c)
				d.From, d.Derive = "settings", func(s string) string { return s }
				return d
			}, "derived"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := forwardTransformer(tc.decl)
			_, err := tr.ComponentOutputs(&Component{Name: "pg", Type: "wrap"})
			if err == nil {
				t.Fatalf("ComponentOutputs accepted a forwarded declaration with %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

// A handler declares the one object it names after the component: it forwards nothing.
func TestForwardedOutput_AHandlerMayNotForward(t *testing.T) {
	h := &forwardingHandler{objectOutputHandler: objectOutputHandler{typ: "cfg"}}
	tr := NewTransformer(map[string]ComponentHandler{"cfg": h}, nil)
	_, err := tr.Transform(terminalApp(Component{Name: "settings", Type: "cfg"}), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `"settings"`) || !strings.Contains(err.Error(), "forward") {
		t.Fatalf("Transform = %v, want a refusal naming the component and that a handler forwards nothing", err)
	}
}

type forwardingHandler struct{ objectOutputHandler }

func (h *forwardingHandler) Outputs(c *Component, _ LoweringContext) ([]OutputDecl, error) {
	return []OutputDecl{{Key: "settings", Kind: configMapKind, Name: c.Name, ForwardOf: "x", ForwardKey: "object"}}, nil
}
