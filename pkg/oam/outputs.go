package oam

import (
	"maps"
	"reflect"
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

// OutputDecl is one object a component declares. Its Key names the output in a
// document and is one identifier (validOutputKey). It is one of two shapes:
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
	// The provider is asked about a copy: queryProvider hands back the caller's
	// own component for a rule, and a write to it must not reach the document.
	probe := probeCopy(*comp)
	_, outs, err := outputsOf(p, &probe, application, naming)
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
// slice, array, pointer and exported struct field in them, whatever its Go
// type (copyProbeMap). A value whose type has a DeepCopy method is copied with
// that method. A channel, a function, a zero-size value, and an unexported
// field of a type without DeepCopy are shared.
func probeCopy(comp Component) Component {
	// One walk for the component and its traits: a pointee that two of them
	// reference stays one pointee in the copy.
	seen := map[propertyCopyKey]reflect.Value{}
	comp.Properties = copyProbeMap(comp.Properties, seen)
	comp.Annotations = maps.Clone(comp.Annotations)
	if comp.Traits != nil {
		traits := make([]Trait, len(comp.Traits))
		for i, t := range comp.Traits {
			t.Properties = copyProbeMap(t.Properties, seen)
			traits[i] = t
		}
		comp.Traits = traits
	}
	return comp
}

// copyProbeMap is copyPropertyMap for the probe: the same walk, and it also
// follows pointers and the exported fields of structs (copyProbeValue).
func copyProbeMap(m map[string]any, seen map[propertyCopyKey]reflect.Value) map[string]any {
	if m == nil {
		return nil
	}
	return copyProbeValue(reflect.ValueOf(m), seen).Interface().(map[string]any)
}

// copyProbeValue copies v for the probe. A pointee seen before in this walk
// keeps one copy, so two references to one pointee stay one pointee and a
// pointer cycle ends. A map key is copied like a value, because a pointer in a
// key reaches the caller's value too. A pointer keeps its own Go type, a
// defined one (type P *string) included. A struct is copied whole first, then each exported field
// is replaced by its own copy: reflection cannot set an unexported field, so
// that field is shared unless the type's DeepCopy copies it.
func copyProbeValue(v reflect.Value, seen map[propertyCopyKey]reflect.Value) reflect.Value {
	if out, ok := probeDeepCopy(v, seen); ok {
		return out
	}
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		key := propertyCopyKey{ptr: v.Pointer(), typ: v.Type(), kind: reflect.Map}
		if out, ok := seen[key]; ok {
			return out
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		seen[key] = out
		for iter := v.MapRange(); iter.Next(); {
			out.SetMapIndex(copyProbeValue(iter.Key(), seen), copyProbeValue(iter.Value(), seen))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		key := propertyCopyKey{ptr: v.Pointer(), len: v.Len(), typ: v.Type(), kind: reflect.Slice}
		if out, ok := seen[key]; ok {
			return out
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		seen[key] = out
		for i := range v.Len() {
			out.Index(i).Set(copyProbeValue(v.Index(i), seen))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			out.Index(i).Set(copyProbeValue(v.Index(i), seen))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(copyProbeValue(v.Elem(), seen))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		// A pointee of size zero holds no state, so a write through it reaches
		// nothing. It stays shared: the runtime gives every new zero-size value
		// one address, so copies of pointers to distinct zero-size values would
		// become one pointer, and map keys made of them would collide.
		if v.Type().Elem().Size() == 0 {
			return v
		}
		// The key names the pointee and not the pointer type, so a defined
		// pointer type (type P *T) and *T that reference one pointee get one
		// copy. The copy is held as *T and converted to the type of each
		// reference. The kind keeps the key apart from a slice that starts at
		// the same address (type S []S, with &s[0] and s[0] = s[:0]).
		key := propertyCopyKey{ptr: v.Pointer(), typ: v.Type().Elem(), kind: reflect.Pointer}
		if out, ok := seen[key]; ok {
			return out.Convert(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		seen[key] = out
		out.Elem().Set(copyProbeValue(v.Elem(), seen))
		return out.Convert(v.Type())
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				out.Field(i).Set(copyProbeValue(v.Field(i), seen))
			}
		}
		return out
	default:
		return v
	}
}

// probeDeepCopy copies v with its type's own DeepCopy method, when it has one
// that takes nothing and returns the type of v itself: the value-receiver form
// (func (T) DeepCopy() T, as resource.Quantity has) and the pointer-receiver
// form (func (*T) DeepCopy() *T, as the generated Kubernetes types have). It
// never calls the method on a nil pointer or on a value reflection marks
// unexported. The method runs in the probe: it must be pure. A pointer it
// returns is recorded in seen, so two references to one pointee get one copy.
func probeDeepCopy(v reflect.Value, seen map[propertyCopyKey]reflect.Value) (reflect.Value, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return reflect.Value{}, false
	}
	if k := v.Kind(); k != reflect.Pointer && k != reflect.Struct {
		return reflect.Value{}, false
	}
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return reflect.Value{}, false
	}
	m := v.MethodByName("DeepCopy")
	if !m.IsValid() {
		return reflect.Value{}, false
	}
	if t := m.Type(); t.NumIn() != 0 || t.NumOut() != 1 || t.Out(0) != v.Type() {
		return reflect.Value{}, false
	}
	if v.Kind() != reflect.Pointer {
		return m.Call(nil)[0], true
	}
	key := propertyCopyKey{ptr: v.Pointer(), typ: v.Type().Elem(), kind: reflect.Pointer}
	if out, ok := seen[key]; ok {
		return out.Convert(v.Type()), true
	}
	out := m.Call(nil)[0]
	seen[key] = out
	return out, true
}

// checkRuleOutputs holds the outputs a lowering rule declared for component
// against the names the rule resolved when it lowered it (claims, the lowered
// names that call added to its allocator). Each allocated output must find
// exactly one claim of its component, role, kind and default, made by a
// lowering rule, and that claim must carry the declared name. Kind tells
// apart two names a rule resolves under one role and default (a ConfigMap and
// a Secret answered differently by the hook). A derived output claims nothing:
// its derivation is applied again to the name held for its parent and must give
// the name it was answered (checkDerivedOutputs).
func checkRuleOutputs(component string, decls []OutputDecl, outs []Output, claims []loweredName) error {
	held := make(map[string]string, len(decls))
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
		held[d.Key] = matched[0].name
	}
	return checkDerivedOutputs(component, decls, outs, held)
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

// resolveOutputs checks decls and answers them: every key is one identifier and
// appears once, an allocated output has a name, a kind, a name role that names an
// object and a default, a derived output names an earlier allocated output and
// carries its derivation.
func resolveOutputs(decls []OutputDecl) ([]Output, error) {
	outs := make([]Output, 0, len(decls))
	names := make(map[string]string, len(decls))
	allocated := make(map[string]bool, len(decls))
	for _, d := range decls {
		if !validOutputKey(d.Key) {
			return nil, errors.Errorf("output key %q is not one identifier (letters, digits and underscores, not starting with a digit)", d.Key)
		}
		if _, dup := names[d.Key]; dup {
			return nil, errors.Errorf("output key %q declared twice", d.Key)
		}
		if strings.TrimSpace(d.Kind.Kind) == "" {
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
		allocated[d.Key] = d.From == "" && d.Derive == nil
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
