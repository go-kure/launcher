package oam

import (
	"maps"
	"slices"
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

// ObjectOutputKey is the key under which the engine publishes the one object of
// a kind component (a handler that implements ComponentObjectProvider and
// declares no outputs of its own): the object's kind and the name the build gives
// it, which is the author's `objectName`, else the consumer hook's, else the
// component name (go-kure/launcher#1643). A handler that declares its own
// outputs publishes what it declares.
const ObjectOutputKey = "object"

// objectOutputProvider is the OutputProvider the engine gives a kind component
// whose handler declares no outputs: the one object, under ObjectOutputKey. It
// parses nothing, so a component whose properties the handler refuses still
// answers; the build refuses those properties.
type objectOutputProvider struct{ object ComponentObjectProvider }

func (p objectOutputProvider) Outputs(comp *Component, _ LoweringContext) ([]OutputDecl, error) {
	kind, _ := p.object.ComponentObject()
	return []OutputDecl{{Key: ObjectOutputKey, Role: NameRoleObject, Kind: kind, Default: comp.Name, Name: comp.ObjectName()}}, nil
}

// handlerOutputProvider returns what answers for the outputs of handler: the
// handler itself when it declares its own, else the engine's default for a kind
// component. A handler that is neither declares none.
func handlerOutputProvider(handler ComponentHandler) (OutputProvider, bool) {
	if p, ok := handler.(OutputProvider); ok {
		return p, true
	}
	if p, ok := handler.(ComponentObjectProvider); ok {
		return objectOutputProvider{object: p}, true
	}
	return nil, false
}

// OutputDecl is one object a component declares. Its Key names the output in a
// document and is one identifier (validOutputKey). It is one of three shapes:
//
//   - allocated: Name is the name the provider resolved for the object, Role and
//     Default are what it resolved it under (a name role that names an object,
//     and a default that is not empty), and From and Derive are empty. The
//     transform holds Name against the name the lowering gives the object.
//   - derived: From names the Key of an earlier allocated output and Derive maps
//     that output's name to this one (a name the operator, not launcher, gives:
//     CloudNativePG's <cluster>-app Secret). Name is empty. The object does not
//     exist in the built application, so nothing is claimed for it. Derive must
//     give one name for one parent name: the build applies it again to the name
//     it holds for the parent and refuses a different answer.
//   - forwarded: ForwardOf and ForwardKey name an output that a component the rule emits
//     publishes, and Name and Kind are what the provider answered for it. The build holds
//     the declaration against that component's own answer (checkForwardedOutputs).
type OutputDecl struct {
	Key     string
	Role    NameRole
	Kind    schema.GroupKind
	Default string
	Name    string
	From    string
	Derive  func(string) string

	// ForwardOf and ForwardKey make a declaration forwarded: the output is one record
	// that a component the rule emits already publishes, ForwardOf names that component
	// and ForwardKey its output key. Name and Kind are what the provider answered for it.
	ForwardOf  string
	ForwardKey string
}

// Output is the answer to one declaration: the key, the kind of object and the
// name it gets.
type Output struct {
	Key  string
	Kind schema.GroupKind
	Name string
}

// reservedOutputKeys are the keys no output may take: the fields of a record
// (go-kure/launcher#1643). A document reads a part as one map, the fields of the
// part's own record (ObjectOutputKey) beside one record per other key, so a key
// named like a field would be read as that field.
var reservedOutputKeys = map[string]bool{"name": true, "kind": true}

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
// an OutputProvider. A handler that is no OutputProvider but declares its one
// object (ComponentObjectProvider) publishes it under ObjectOutputKey. A
// declaration that is malformed is an error (fail-fast).
//
// It consults no TransformContext.Naming hook: a name is the author's or the
// default. A consumer that sets Naming calls ComponentOutputsNamed.
func (t *Transformer) ComponentOutputs(comp *Component) ([]Output, error) {
	return t.componentOutputs(comp, "", nil, nil, false, "")
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
	return t.componentOutputs(comp, application, naming, nil, false, "")
}

// ComponentOutputsWithin is ComponentOutputsNamed for a rule that asks about a component it
// emits while it renders them, under the application of lctx. The query shares the run of
// lctx (LoweringContext.RunState), so what a rule keeps for the whole build, such as the
// limits of a model render, counts what the query spends too, and a chain of rules that each
// ask about the next cannot spend more than one build may. It claims nothing, as the other
// queries do.
func (t *Transformer) ComponentOutputsWithin(lctx LoweringContext, comp *Component, naming func(NameRequest) (string, bool)) ([]Output, error) {
	return t.componentOutputs(comp, lctx.Application(), naming, lctx.Namer, true, lctx.imageLevel)
}

// componentOutputs answers for comp. parent, when it is not nil, is the allocator of the run the
// query belongs to: the allocator of the query shares its run state. emitted says comp is one a
// rule emits (queryProvider). level is the image reference level the provider holds the image to
// (go-kure/launcher#1644); empty is the default level.
func (t *Transformer) componentOutputs(comp *Component, application string, naming func(NameRequest) (string, bool), parent *NameAllocator, emitted bool, level ImageReferenceLevel) ([]Output, error) {
	provider, comp, ok, err := t.queryProvider(comp, application, naming, emitted, declaresOutputs)
	if err != nil || !ok {
		return nil, err
	}
	p, ok := provider.(OutputProvider)
	if !ok {
		// A type registered as a rule is built by that rule, so the default for a
		// kind component is never its answer; a type registered as a handler is
		// built by it. The registration decides, whatever interfaces the value also
		// implements. queryProvider hands back the component under its registered
		// type.
		if _, isRule := t.componentLoweringRules[comp.Type]; !isRule {
			if handler, isHandler := provider.(ComponentHandler); isHandler {
				p, ok = handlerOutputProvider(handler)
			}
		}
	}
	if !ok {
		return nil, nil
	}
	// The provider is asked about a copy: queryProvider hands back the caller's
	// own component for a rule, and a write to it must not reach the document.
	probe := probeCopy(*comp)
	_, outs, err := outputsOf(p, &probe, application, naming, parent, level)
	return outs, err
}

// outputsOf asks p for comp's outputs on a fresh allocator: what a name is
// resolved to is asked here, nothing is claimed. It returns the declarations
// as the provider wrote them and the answers they resolve to. The allocator
// shares the run state of parent, when there is one. level is the image reference
// level the context carries to p (empty is the default level).
func outputsOf(p OutputProvider, comp *Component, application string, naming func(NameRequest) (string, bool), parent *NameAllocator, level ImageReferenceLevel) ([]OutputDecl, []Output, error) {
	namer := NewNameAllocator()
	namer.hook = naming
	if parent != nil {
		if parent.runState == nil {
			parent.runState = map[any]any{}
		}
		namer.runState = parent.runState
	}
	decls, err := p.Outputs(comp, LoweringContext{Component: comp, Namer: namer, application: application, imageLevel: level})
	if err != nil {
		return nil, nil, err
	}
	// The provider may keep the slice it returned; the check holds the
	// declarations as they were answered.
	decls = slices.Clone(decls)
	outs, err := resolveOutputs(decls)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "component %q", comp.Name)
	}
	return decls, outs, nil
}

// probeCopy returns a copy of comp a provider can be asked about without a
// write reaching the component the build uses: the properties of the component
// and of its traits, and its annotations, are copied down through every map,
// slice and array in them, whatever its Go type (copyPropertyMap). Any other
// value is shared.
func probeCopy(comp Component) Component {
	comp.Properties = copyPropertyMap(comp.Properties)
	comp.Annotations = maps.Clone(comp.Annotations)
	if comp.Traits != nil {
		traits := make([]Trait, len(comp.Traits))
		for i, t := range comp.Traits {
			t.Properties = copyPropertyMap(t.Properties)
			traits[i] = t
		}
		comp.Traits = traits
	}
	return comp
}

// checkRuleOutputs holds the outputs a lowering rule declared for component
// against the names the rule resolved when it lowered it (claims, the lowered
// names that call added to its allocator). Each allocated output must find
// exactly one claim of its component, role, kind and default, made by a
// lowering rule, and that claim must carry the declared name. Kind tells
// apart two names a rule resolves under one role and default (a ConfigMap and
// a Secret answered differently by the hook). A derived output claims nothing:
// its derivation is applied again to the name held for its parent and must give
// the name it was answered (checkDerivedOutputs). A forwarded output claims
// nothing either: the component it names must be among the ones the rule
// emitted, and outputsOf (the query of the transformer, for one component) must
// answer the key it names with the same name and kind (checkForwardedOutputs).
func checkRuleOutputs(component string, decls []OutputDecl, outs []Output, claims []loweredName, emitted []Component, outputsOf func(*Component) ([]Output, error)) error {
	held := make(map[string]string, len(decls))
	for _, d := range decls {
		if d.From != "" || d.Derive != nil || isForwarded(d) {
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
		held[d.Key] = matched[0].name
	}
	if err := checkForwardedOutputs(component, decls, emitted, outputsOf); err != nil {
		return err
	}
	return checkDerivedOutputs(component, decls, outs, held)
}

// isForwarded reports whether d is a forwarded declaration.
func isForwarded(d OutputDecl) bool { return d.ForwardOf != "" || d.ForwardKey != "" }

// checkForwardedOutputs holds each forwarded output to the component it names: that
// component is one the rule emitted, it publishes the key the output names, and its
// answer carries the name and kind the output was answered. The rule's own claims
// cannot tell, since the emitted component names its object itself, so this asks it.
func checkForwardedOutputs(component string, decls []OutputDecl, emitted []Component, outputsOf func(*Component) ([]Output, error)) error {
	for _, d := range decls {
		if !isForwarded(d) {
			continue
		}
		// Components of distinct terminal types may share a name (a ConfigMap and a Secret
		// both named x), so the name alone does not pick one: the output is held to the one
		// whose answer it matches. Where none does, the refusal reported is that of the one
		// of the declared kind, so the order of emission does not change the diagnosis, and
		// else the first one's.
		var targets []*Component
		for i := range emitted {
			if emitted[i].Name == d.ForwardOf {
				targets = append(targets, &emitted[i])
			}
		}
		if len(targets) == 0 || outputsOf == nil {
			return errors.Errorf("output %q of component %q forwards the output %q of the component %q, which the lowering did not emit",
				d.Key, component, d.ForwardKey, d.ForwardOf)
		}
		var refusal error
		var ofKind bool
		for _, target := range targets {
			probe := probeCopy(*target)
			got, err := outputsOf(&probe)
			if err != nil {
				return errors.Wrapf(err, "output %q of component %q forwards an output of the component %q", d.Key, component, d.ForwardOf)
			}
			sameKind, refused := forwardRefusal(component, d, got)
			if refused == nil {
				refusal = nil
				break
			}
			if refusal == nil || (sameKind && !ofKind) {
				refusal, ofKind = refused, sameKind
			}
		}
		if refusal != nil {
			return refusal
		}
	}
	return nil
}

// forwardRefusal holds the forwarded declaration d to the outputs got of the component it names,
// and returns why they do not carry it, or nil where they do. sameKind tells whether the output
// the declaration names is of the declared kind, so that a refusal about the name is told from
// one about another object of the same name.
func forwardRefusal(component string, d OutputDecl, got []Output) (sameKind bool, err error) {
	var found *Output
	for i := range got {
		if got[i].Key == d.ForwardKey {
			found = &got[i]
			break
		}
	}
	switch {
	case found == nil:
		keys := make([]string, 0, len(got))
		for _, o := range got {
			keys = append(keys, o.Key)
		}
		return false, errors.Errorf("output %q of component %q forwards the output %q of the component %q, which publishes none of that key (it publishes: %s)",
			d.Key, component, d.ForwardKey, d.ForwardOf, strings.Join(keys, ", "))
	case found.Name != d.Name || found.Kind != d.Kind:
		return found.Kind == d.Kind, errors.Errorf("output %q of component %q declares the %s name %q, but the output %q of the component %q is the %s name %q",
			d.Key, component, d.Kind, d.Name, d.ForwardKey, d.ForwardOf, found.Kind, found.Name)
	}
	return true, nil
}

// checkHandlerOutputs holds the outputs a handler declared for comp against
// the object the transform resolved for comp: its name (Component.ObjectName)
// and its kind, the GroupKind the handler declares for that object
// (ComponentObject), group included. A handler's object is the one the engine
// names after the component, so only the role object can be held. A derived
// output is held to the derivation of that name (checkDerivedOutputs).
func checkHandlerOutputs(comp *Component, handler ComponentHandler, decls []OutputDecl, outs []Output) error {
	var objectKind schema.GroupKind
	if p, ok := handler.(ComponentObjectProvider); ok {
		objectKind, _ = p.ComponentObject()
	}
	held := make(map[string]string, len(decls))
	for _, d := range decls {
		if isForwarded(d) {
			return errors.Errorf("output %q of component %q is forwarded: a handler declares the one object it names after the component and forwards no output of another component",
				d.Key, comp.Name)
		}
		if d.From != "" || d.Derive != nil {
			continue
		}
		if d.Role != NameRoleObject {
			return errors.Errorf("output %q of component %q is declared under role %q: a handler declares the one object it names after the component, role %q",
				d.Key, comp.Name, d.Role, NameRoleObject)
		}
		if d.Kind != objectKind {
			return errors.Errorf("output %q of component %q declares the object as kind %q, but the handler's object is kind %q",
				d.Key, comp.Name, d.Kind, objectKind)
		}
		if d.Name != comp.ObjectName() {
			return errors.Errorf("output %q of component %q declares the %s name %q, but the transform named the object %q",
				d.Key, comp.Name, d.Kind, d.Name, comp.ObjectName())
		}
		held[d.Key] = comp.ObjectName()
	}
	return checkDerivedOutputs(comp.Name, decls, outs, held)
}

// checkAllocation holds an allocated declaration to what the allocator holds a
// name to, so a declaration the lowering could never resolve is refused where it
// is answered: a Role that is a name role naming an object, and a Default to
// fall back to. The build check matches the claim of the lowering on both.
func checkAllocation(d OutputDecl) error {
	class, _, known := classOfNameRole(d.Role)
	switch {
	case !known:
		return errors.Errorf("output %q is declared under role %q, which is not a name role (the roles: %s)", d.Key, d.Role, joinNameRoles())
	case class != nameClassObject:
		return errors.Errorf("output %q is declared under role %q, which names no object", d.Key, d.Role)
	case strings.TrimSpace(d.Default) == "":
		return errors.Errorf("output %q has no default name for role %q", d.Key, d.Role)
	}
	return nil
}

// checkDerivedOutputs holds the derived outputs to the name the build holds for
// their parent: each derivation is applied again to that held name and must give
// the name the output was answered. An answer is made once, from the name the
// provider declared for the parent, so a derivation that is not a pure function
// of its parent's name (a counter, a clock) would otherwise name an object the
// build never checks. held maps the key of each allocated output to the name the
// build holds for it; outs are the answers to decls, in order.
func checkDerivedOutputs(component string, decls []OutputDecl, outs []Output, held map[string]string) error {
	if len(outs) != len(decls) {
		return errors.Errorf("component %q declares %d outputs and has %d answers", component, len(decls), len(outs))
	}
	for i, d := range decls {
		if d.From == "" && d.Derive == nil {
			continue
		}
		parent, ok := held[d.From]
		if !ok || d.Derive == nil {
			return errors.Errorf("output %q of component %q derives from %q, which the build holds no name for, or has no derivation", d.Key, component, d.From)
		}
		if again := d.Derive(parent); again != outs[i].Name {
			return errors.Errorf("output %q (kind %s) of component %q is answered %q, but its derivation from %q (%q) now gives %q: a derivation must give one name for one parent name",
				d.Key, d.Kind, component, outs[i].Name, d.From, parent, again)
		}
	}
	return nil
}

// checkForwarded holds the shape of a forwarded declaration: it names the component
// and the key it forwards, carries the name and kind the provider answered for it,
// and is neither allocated (no role, no default) nor derived. The check after the
// lowering holds the answer against what the component publishes (checkRuleOutputs).
func checkForwarded(d OutputDecl) error {
	switch {
	case d.ForwardOf == "" || d.ForwardKey == "":
		return errors.Errorf("output %q forwards an output of an emitted component and must name both the component and its key", d.Key)
	case d.From != "" || d.Derive != nil:
		return errors.Errorf("output %q is both forwarded and derived", d.Key)
	case d.Role != "" || d.Default != "":
		return errors.Errorf("output %q is forwarded, so it declares no role and no default: the emitted component names the object", d.Key)
	case strings.TrimSpace(d.Name) == "":
		return errors.Errorf("output %q has no name", d.Key)
	}
	return nil
}

// resolveOutputs checks decls and answers them: every key is one identifier and
// appears once, an allocated output has a name, a kind, a name role that names an
// object and a default, a derived output names an earlier allocated output and
// carries its derivation, and a forwarded output names the component and the key
// it forwards.
func resolveOutputs(decls []OutputDecl) ([]Output, error) {
	outs := make([]Output, 0, len(decls))
	names := make(map[string]string, len(decls))
	allocated := make(map[string]bool, len(decls))
	for _, d := range decls {
		if !validOutputKey(d.Key) {
			return nil, errors.Errorf("output key %q is not one identifier (letters, digits and underscores, not starting with a digit)", d.Key)
		}
		if reservedOutputKeys[d.Key] {
			return nil, errors.Errorf("output key %q is reserved: a document reads a part as one map, the fields of its own record beside one record per key, and %q is a field", d.Key, d.Key)
		}
		if _, dup := names[d.Key]; dup {
			return nil, errors.Errorf("output key %q declared twice", d.Key)
		}
		if strings.TrimSpace(d.Kind.Kind) == "" {
			return nil, errors.Errorf("output %q declares no kind", d.Key)
		}
		name := d.Name
		switch {
		case d.ForwardOf != "" || d.ForwardKey != "":
			if err := checkForwarded(d); err != nil {
				return nil, err
			}
		case d.From != "" || d.Derive != nil:
			if name != "" {
				return nil, errors.Errorf("output %q is both derived and named; a derived output takes its name from %q", d.Key, d.From)
			}
			parent, ok := names[d.From]
			if !ok {
				return nil, errors.Errorf("output %q derives from %q, which is not an output declared before it", d.Key, d.From)
			}
			if !allocated[d.From] {
				return nil, errors.Errorf("output %q derives from %q, which is itself derived: a derived output names an allocated one", d.Key, d.From)
			}
			if d.Derive == nil {
				return nil, errors.Errorf("output %q derives from %q and has no derivation", d.Key, d.From)
			}
			name = d.Derive(parent)
			if strings.TrimSpace(name) == "" {
				return nil, errors.Errorf("output %q derives from %q and the derivation gives it no name", d.Key, d.From)
			}
		case strings.TrimSpace(name) == "":
			return nil, errors.Errorf("output %q has no name", d.Key)
		default:
			if err := checkAllocation(d); err != nil {
				return nil, err
			}
		}
		allocated[d.Key] = d.From == "" && d.Derive == nil && d.ForwardOf == "" && d.ForwardKey == ""
		names[d.Key] = name
		outs = append(outs, Output{Key: d.Key, Kind: d.Kind, Name: name})
	}
	return outs, nil
}

// declaresOutputs reports whether handler is one componentOutputs reads outputs
// from. A nil handler is not.
func declaresOutputs(handler ComponentHandler) bool {
	_, ok := handlerOutputProvider(handler)
	return ok
}
