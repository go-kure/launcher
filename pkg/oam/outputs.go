package oam

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
)

// OutputProvider is an optional ComponentHandler or ComponentLoweringRule
// interface: it declares the objects a component generates whose names another
// part of the document needs before the document is built (go-kure/launcher#1259),
// each under a key. A consumer calls Transformer.ComponentOutputs to learn them,
// the way it calls ComponentEndpoints for endpoints.
//
// Outputs resolves each name through lctx.ResolveName (or a helper that does)
// with the base, suffix and NameSpec the provider resolves it with when it
// generates the object, so the answer is the name the object gets: the author's,
// else the consumer hook's, else the default. lctx carries the component and a
// Namer and no Document. The Namer is a fresh one: nothing the call resolves is
// claimed.
type OutputProvider interface {
	Outputs(component *Component, lctx LoweringContext) ([]OutputDecl, error)
}

// OutputDecl is one object a component declares. Its Key names the output in a
// document and is one identifier (validOutputKey). It is one of two shapes:
//
//   - allocated: Name is the name the provider resolved for the object, Role and
//     Default are what it resolved it under, and From and Derive are empty. The
//     transform holds Name against the name the lowering gives the object.
//   - derived: From names the Key of an earlier allocated output and Derive maps
//     that output's name to this one (a name the operator, not launcher, gives:
//     CloudNativePG's <cluster>-app Secret). Name is empty. The object does not
//     exist in the built application, so nothing is claimed for it.
type OutputDecl struct {
	Key     string
	Role    NameRole
	Kind    schema.GroupKind
	Default string
	Name    string
	From    string
	Derive  func(string) string
}

// Output is the answer to one declaration: the key, the kind of object and the
// name it gets.
type Output struct {
	Key  string
	Kind schema.GroupKind
	Name string
}

// validOutputKey reports whether key is one identifier: a letter or underscore,
// then letters, digits and underscores, ASCII only. A key is addressed as
// parts.<part>.<key> in a document, so it must read as one.
func validOutputKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// ComponentOutputs returns the outputs declared for comp.Type by its
// ComponentLoweringRule, or by its handler when no rule claims the type, or
// (nil, nil) if comp is nil, neither is registered, or the one registered is not
// an OutputProvider. A declaration that is malformed is an error (fail-fast).
//
// It consults no TransformContext.Naming hook: a name is the author's or the
// default. A consumer that sets Naming calls ComponentOutputsNamed.
func (t *Transformer) ComponentOutputs(comp *Component) ([]Output, error) {
	return t.componentOutputs(comp, "", nil)
}

// ComponentOutputsNamed is ComponentOutputs for a consumer that sets
// TransformContext.Naming: a name is the author's, else the one naming returns,
// else the default. naming is asked the NameRequest the transform asks for that
// name, and an answer that is no valid name for its role is refused here with the
// transform's message. application is the name of the document comp is authored
// in, as ComponentEndpointsNamed takes it. A nil naming makes this
// ComponentOutputs.
//
// Like the endpoint queries, it answers for one component: it does not see what
// only the document shows (a name that is already another component, or two
// objects that collide), and a query made under an application name other than
// the one the transform asks with is not held against a build.
func (t *Transformer) ComponentOutputsNamed(application string, comp *Component, naming func(NameRequest) (string, bool)) ([]Output, error) {
	return t.componentOutputs(comp, application, naming)
}

func (t *Transformer) componentOutputs(comp *Component, application string, naming func(NameRequest) (string, bool)) ([]Output, error) {
	provider, comp, ok, err := t.queryProvider(comp, application, naming, declaresOutputs)
	if err != nil || !ok {
		return nil, err
	}
	p, ok := provider.(OutputProvider)
	if !ok {
		return nil, nil
	}
	_, outs, err := outputsOf(p, comp, application, naming)
	return outs, err
}

// outputsOf asks p for comp's outputs on a fresh allocator: what a name is
// resolved to is asked here, nothing is claimed. It returns the declarations
// as the provider wrote them and the answers they resolve to.
func outputsOf(p OutputProvider, comp *Component, application string, naming func(NameRequest) (string, bool)) ([]OutputDecl, []Output, error) {
	namer := NewNameAllocator()
	namer.hook = naming
	decls, err := p.Outputs(comp, LoweringContext{Component: comp, Namer: namer, application: application})
	if err != nil {
		return nil, nil, err
	}
	outs, err := resolveOutputs(decls)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "component %q", comp.Name)
	}
	return decls, outs, nil
}

// checkRuleOutputs holds the outputs a lowering rule declared for component
// against the names the rule resolved when it lowered it (claims, the lowered
// names that call added to its allocator). Each allocated output must find
// exactly one claim of its component, role, kind and default, made by a
// lowering rule, and that claim must carry the declared name. Kind tells
// apart two names a rule resolves under one role and default (a ConfigMap and
// a Secret answered differently by the hook). A derived output claims nothing:
// it is the derivation of its parent, which was held.
func checkRuleOutputs(component string, decls []OutputDecl, outs []Output, claims []loweredName) error {
	for _, d := range decls {
		if d.From != "" || d.Derive != nil {
			continue
		}
		var matched []loweredName
		for _, c := range claims {
			o := c.claim.owner
			if o.lowered && o.component == component && o.role == d.Role && o.def == d.Default &&
				c.group == d.Kind.Group && c.kind == d.Kind.Kind {
				matched = append(matched, c)
			}
		}
		switch {
		case len(matched) == 0:
			return errors.Errorf("output %q of component %q declares the %s name %q, but the lowering resolved none for role %q and default %q",
				d.Key, component, d.Kind, d.Name, d.Role, d.Default)
		case len(matched) > 1:
			return errors.Errorf("output %q of component %q declares the %s name %q, but the lowering resolved %d names for role %q and default %q, so the output names no one of them",
				d.Key, component, d.Kind, d.Name, len(matched), d.Role, d.Default)
		case matched[0].name != d.Name:
			return errors.Errorf("output %q of component %q declares the %s name %q, but the lowering named the object %q",
				d.Key, component, d.Kind, d.Name, matched[0].name)
		}
	}
	return nil
}

// checkHandlerOutputs holds the outputs a handler declared for comp against
// the object name the transform resolved for comp (Component.ObjectName). A
// handler's object is the one the engine names after the component, so only
// the role object can be held.
func checkHandlerOutputs(comp *Component, decls []OutputDecl, outs []Output) error {
	for _, d := range decls {
		if d.From != "" || d.Derive != nil {
			continue
		}
		if d.Role != NameRoleObject {
			return errors.Errorf("output %q of component %q is declared under role %q: a handler declares the one object it names after the component, role %q",
				d.Key, comp.Name, d.Role, NameRoleObject)
		}
		if d.Name != comp.ObjectName() {
			return errors.Errorf("output %q of component %q declares the %s name %q, but the transform named the object %q",
				d.Key, comp.Name, d.Kind, d.Name, comp.ObjectName())
		}
	}
	return nil
}

// resolveOutputs checks decls and answers them: every key is one identifier and
// appears once, an allocated output has a name and a kind, a derived output
// names an earlier allocated output and carries its derivation.
func resolveOutputs(decls []OutputDecl) ([]Output, error) {
	outs := make([]Output, 0, len(decls))
	names := make(map[string]string, len(decls))
	for _, d := range decls {
		if !validOutputKey(d.Key) {
			return nil, errors.Errorf("output key %q is not one identifier (letters, digits and underscores, not starting with a digit)", d.Key)
		}
		if _, dup := names[d.Key]; dup {
			return nil, errors.Errorf("output key %q declared twice", d.Key)
		}
		if d.Kind.Empty() {
			return nil, errors.Errorf("output %q declares no kind", d.Key)
		}
		name := d.Name
		switch {
		case d.From != "" || d.Derive != nil:
			if name != "" {
				return nil, errors.Errorf("output %q is both derived and named; a derived output takes its name from %q", d.Key, d.From)
			}
			parent, ok := names[d.From]
			if !ok {
				return nil, errors.Errorf("output %q derives from %q, which is not an output declared before it", d.Key, d.From)
			}
			if d.Derive == nil {
				return nil, errors.Errorf("output %q derives from %q and has no derivation", d.Key, d.From)
			}
			name = d.Derive(parent)
		case strings.TrimSpace(name) == "":
			return nil, errors.Errorf("output %q has no name", d.Key)
		}
		names[d.Key] = name
		outs = append(outs, Output{Key: d.Key, Kind: d.Kind, Name: name})
	}
	return outs, nil
}

// declaresOutputs reports whether handler is one componentOutputs reads outputs
// from. A nil handler is not.
func declaresOutputs(handler ComponentHandler) bool {
	_, ok := handler.(OutputProvider)
	return ok
}
