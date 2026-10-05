package oam

import (
	"github.com/go-kure/launcher/pkg/errors"
)

// defaultNamespace is the namespace a transform gives a document where neither
// its context nor the document names one.
const defaultNamespace = "default"

// loweredName is one object name a lowering rule resolved
// (LoweringContext.ResolveName). The namespace the object lands in is the
// document's, known only once lowering has settled, so the name is held here
// until the transform claims it (claimLowered).
type loweredName struct {
	group, kind, name string
	// namespace is the authored document's (Origin.Namespace), which is not the
	// namespace the object lands in: it holds apart the names of two documents
	// lowered with one allocator (LowerRaws). A document that authors none is
	// held as one of defaultNamespace, so it is not held apart from a document
	// that authors that namespace. It is empty for a cluster-scoped object,
	// which no namespace holds apart.
	namespace string
	// clusterScoped is NameSpec.ClusterScoped: the object is claimed with no
	// namespace.
	clusterScoped bool
	// fluxScoped is NameSpec.FluxScoped: the object is claimed in the Flux
	// namespace when the transform has one.
	fluxScoped bool
	claim      resolvedNameClaim
}

// sharedNameKey is what makes two unauthored ResolveSharedName calls ask for
// the same name: the authored document, the object's kind and the default.
type sharedNameKey struct {
	namespace, document, documentKind string
	group, kind, def                  string
}

// ResolveName returns the name of an object a lowering rule generates, in this
// order: the author's (spec.Property set), else the one the consumer's
// TransformContext.Naming hook returns, else the default "<base>-<suffix>",
// built, validated and shortened as Namer.Name builds it. A name that is not
// the default is validated for spec.Role and used as written or refused, never
// shortened. spec.Role must name an object and spec.Kind say which;
// spec.Default and spec.Namespace choose nothing here: the default is the one
// built here, and the namespace is the document's. A cluster-scoped object (a
// ClusterRole) has none: the rule says so with spec.ClusterScoped, and the name
// is then held against every other of its kind, whatever namespace its document
// has; a spec.Namespace beside it is refused, as it is for a trait. An object
// that lands in the Flux namespace when the transform has one (a Flux source,
// a HelmRelease's values ConfigMap) says so with spec.FluxScoped, and is then
// claimed there. Where base
// and suffix build no valid default, an authored name is still
// used; without one the call fails with the default's problem, and the hook is
// not asked.
//
// The hook is asked with the document's name as it stands when the rule runs
// (NameRequest.Application) and the enclosing component's, empty at document
// and policy position. Only a rule the engine runs inside a transform has a
// hook: under LowerRaws, and on a context built outside the engine, the hook is
// not consulted and the name is the author's or the default.
//
// The name is claimed as the object it names. Each call names one object the
// rule generates: a second call that resolves the same kind and name for a
// document of the same namespace is refused here with both named, whichever
// rule made it and also when it is the first one made again, so a rule
// resolves a name once and keeps it. The transform then holds every one of
// them against the names it resolves after lowering, by kind, namespace and
// name: a later one that names the same object is refused, also where its
// component, role and default are this call's. The name is not reserved as a
// component name. A rule that emits a
// component under it reserves it with Namer.Reserve, once per component name:
// two components of one name that one rule emits on purpose are a sibling
// group.
func (l LoweringContext) ResolveName(base, suffix string, spec NameSpec) (string, error) {
	if l.Namer == nil {
		return "", errors.New("lowering: ResolveName needs a LoweringContext with a Namer")
	}
	lowered, err := l.lowerName(base, suffix, spec, l.Component == nil)
	if err != nil {
		return "", err
	}
	if err := l.Namer.recordLowered(lowered); err != nil {
		return "", err
	}
	return lowered.name, nil
}

// ResolveMemberName names the one object of member, a kind component the rule
// is about to emit, where the rule lets the author and the Naming hook choose
// that name. The object is named after its component unless one of them says
// otherwise: the name is the author's (spec.Property set), else the hook's
// answer, else member's component name, used as written. A name that is not
// the default is validated for spec.Role and used as given or refused, never
// shortened. spec.Role must name an object and spec.Kind be the kind of
// member's type; spec.Default and spec.Namespace choose nothing here, and
// spec.ClusterScoped and spec.FluxScoped say where the object lands, as they
// do for ResolveName.
//
// The name is resolved, recorded for the transform to claim and set on member
// in this one call: a rule has no other way to give a member's object a name
// of its own, so no such name goes unclaimed. member keeps its component name,
// and with it its sibling group, its place in the layout, its labels and what
// OrderAfter orders: only the object's name changes, and what the rule writes
// as a reference to that object (a sourceRef) is the rule's to write from
// member.ObjectName afterwards. The hook is asked with the enclosing
// component, as ResolveName asks it, and the claim is held as ResolveName's
// is.
//
// member's type must be a kind component's (ComponentObjectProvider): the
// transform refuses a name set on any other. `objectName` stays refused on the
// member, as on every component a rule emitted.
//
// On a context with no Namer (a rule driven directly, outside the engine) an
// authored name is validated and set, the hook is not consulted and nothing is
// claimed, as for a trait built outside a transform (Trait.ResolveName): the
// default needs no allocator, and no transform follows to claim anything.
func (l LoweringContext) ResolveMemberName(member *Component, spec NameSpec) error {
	if member == nil || member.Name == "" {
		return errors.New("lowering: ResolveMemberName needs a member component with a name")
	}
	if class, _, known := classOfNameRole(spec.Role); known && class != nameClassObject {
		return errors.Errorf("lowering: role %q names no object; a lowering rule resolves object names only", spec.Role)
	}
	if err := clusterScopeProblem(spec); err != nil {
		return err
	}
	lowered, err := l.lowerDefault(member.Name, spec, false)
	if err != nil {
		return err
	}
	if l.Namer != nil {
		if err := l.Namer.recordLowered(lowered); err != nil {
			return err
		}
	}
	// The default leaves the member as the rule built it: the object is named
	// after its component, as before (Component.ObjectName).
	member.objectName = ""
	if lowered.name != member.Name {
		member.objectName = lowered.name
	}
	return nil
}

// ResolveSharedName is ResolveName for an object the elements of one document
// share: one its content identity determines wholly, as NameAllocator.EmitOrAdopt
// asks of it (a generated Flux source). identity is that content identity. The
// name is claimed through EmitOrAdopt, under its three constraints on the
// caller, and so is also reserved as a component name: the first claim returns
// adopted=false and the caller emits the element under the name; a later one
// for the same name and identity, from any element of the same authored
// document, returns adopted=true, and the caller only references it. The same
// name for another identity is EmitOrAdopt's collision error.
//
// Without an authored name (spec.Property empty) the object is the document's:
// the Naming hook is asked with no component, whatever position the rule runs
// at, and once. Every later call for the same kind and default in that document
// takes the first one's answer without asking again, so every consumer of one
// identity adopts the one object whatever the hook would have said the second
// time. An authored name is the element's own, validated and used as ResolveName
// uses one. It is not a second name for the unauthored object: an element that
// names the object and one that does not get two objects, and two elements that
// write the same name for the same identity share one. One name for one
// identity is one object whoever chose the name, so an authored name equal to
// the unauthored object's (its default, or the hook's answer) shares that
// object too, in either order.
//
// The name is recorded for the transform to claim only where the object is
// emitted (adopted=false): an adopter names no second object.
func (l LoweringContext) ResolveSharedName(base, suffix, identity string, spec NameSpec) (name string, adopted bool, err error) {
	if l.Namer == nil {
		return "", false, errors.New("lowering: ResolveSharedName needs a LoweringContext with a Namer")
	}
	authored := spec.Property != ""
	var key sharedNameKey
	var lowered loweredName
	known := false
	if !authored {
		def, err := generatedName(base, suffix)
		if err != nil {
			return "", false, err
		}
		key = sharedNameKey{
			namespace: l.Origin.Namespace, document: l.Origin.Document, documentKind: l.Origin.DocumentKind,
			group: spec.Kind.Group, kind: spec.Kind.Kind, def: def,
		}
		lowered, known = l.Namer.shared[key]
	}
	if !known {
		if lowered, err = l.lowerName(base, suffix, spec, !authored); err != nil {
			return "", false, err
		}
	}
	if adopted, err = l.Namer.EmitOrAdopt(lowered.name, identity, l.Origin); err != nil {
		return "", false, err
	}
	if !adopted {
		if err := l.Namer.recordLowered(lowered); err != nil {
			return "", false, err
		}
	}
	if !authored && !known {
		if l.Namer.shared == nil {
			l.Namer.shared = make(map[sharedNameKey]loweredName)
		}
		l.Namer.shared[key] = lowered
	}
	return lowered.name, adopted, nil
}

// lowerName resolves spec as ResolveName documents and returns the name with
// what the transform needs to claim it; the caller records it (recordLowered).
// documentOwned makes the document the name's owner, and the hook asked with no
// component, also where the context has an enclosing component.
func (l LoweringContext) lowerName(base, suffix string, spec NameSpec, documentOwned bool) (loweredName, error) {
	class, syntax, known := classOfNameRole(spec.Role)
	if known && class != nameClassObject {
		return loweredName{}, errors.Errorf("lowering: role %q names no object; a lowering rule resolves object names only", spec.Role)
	}
	// Before Namespace is set aside below: with ClusterScoped it is a caller
	// error here as it is for a trait.
	if err := clusterScopeProblem(spec); err != nil {
		return loweredName{}, err
	}
	def, err := generatedName(base, suffix)
	if err != nil {
		// No default can be built from this base and suffix (a character no
		// object name takes). An authored name is what the author has for that:
		// it names the object all the same. The hook is not asked, having no
		// default to be asked about.
		if spec.Property == "" || !known {
			return loweredName{}, err
		}
		if problem := overrideNameProblem(spec.Authored, syntax); problem != "" {
			return loweredName{}, errors.Errorf("%s %q cannot be the name for role %q: %s; write a valid name", spec.Property, spec.Authored, spec.Role, problem)
		}
		// Stands in for the default as what tells this name's owner from
		// another of its component and role; it is never the name.
		def = base + "-" + suffix
	}
	return l.lowerDefault(def, spec, documentOwned)
}

// lowerDefault resolves spec with def as its default and returns the name with
// what the transform needs to claim it: lowerName past the building of the
// default, and the whole of it for a name whose default is not built from a
// base and a suffix (ResolveMemberName). The caller has checked that spec's
// role names an object and that its scope is one (clusterScopeProblem).
func (l LoweringContext) lowerDefault(def string, spec NameSpec, documentOwned bool) (loweredName, error) {
	// Where the object lands is the transform's to say (claimLowered): the
	// resolver below is given neither.
	fluxScoped := spec.FluxScoped
	spec.Default, spec.Namespace, spec.FluxScoped = def, "", false

	application := l.application
	if application == "" {
		application = l.Origin.Document
		if l.Document != nil {
			application = l.Document.Metadata.Name
		}
	}
	// lowered: never the owner of a name resolved after lowering, whatever else
	// the two share.
	owner := nameOwner{role: spec.Role, def: def, lowered: true}
	switch {
	case documentOwned || l.Component == nil:
		owner.document = application
	case l.Origin.TraitType != "":
		// A trait rule: the authored trait it lowers tells it from another rule
		// of the same component.
		owner.component = l.Component.Name
		owner.trait, owner.slot, owner.authored = l.Origin.TraitType, l.Origin.Index, true
	default:
		owner.component = l.Component.Name
	}
	// No claim space: the name is recorded by the caller and claimed by the
	// transform. No hook without a Namer (ResolveMemberName outside the engine).
	resolver := &nameResolver{application: application}
	if l.Namer != nil {
		resolver.hook = l.Namer.hook
	}
	name, source, err := resolver.resolveFrom(owner, spec)
	if err != nil {
		return loweredName{}, err
	}
	lowered := loweredName{
		group: spec.Kind.Group, kind: spec.Kind.Kind, name: name, namespace: l.Origin.Namespace,
		clusterScoped: spec.ClusterScoped, fluxScoped: fluxScoped,
		claim: resolvedNameClaim{owner: owner, source: source, property: spec.Property},
	}
	switch {
	case lowered.clusterScoped:
		lowered.namespace = ""
	case lowered.namespace == "":
		lowered.namespace = defaultNamespace
	}
	return lowered, nil
}

// recordLowered holds a name a lowering rule resolved until the transform
// claims it. A second resolution of one kind and name for a document of the
// same namespace is an error naming both. Every lowered name of one transform
// lands in one namespace; LowerRaws lowers documents of several. A
// cluster-scoped name is one object whichever document it was resolved for.
//
// An equal owner is no exception, as it is none for Reserve: nothing a rule is
// given tells two rule calls on one authored element apart, so the same owner
// resolving the name again cannot be told from a second rule generating a
// second object of that name.
//
// A Flux-scoped name and one that is not are not compared here: with a Flux
// namespace they are two objects. Without one they are the same object, and
// claimLowered refuses the pair.
func (n *NameAllocator) recordLowered(lowered loweredName) error {
	for _, prior := range n.lowered {
		if prior.group != lowered.group || prior.kind != lowered.kind || prior.namespace != lowered.namespace || prior.name != lowered.name {
			continue
		}
		if prior.fluxScoped != lowered.fluxScoped {
			continue
		}
		// Printed without the namespace: the authored one is not where the object
		// lands.
		key := nameClaimKey{class: nameClassObject, objectIdentity: objectIdentity{group: lowered.group, kind: lowered.kind, name: lowered.name}}
		return nameCollision(key, prior.claim, lowered.claim)
	}
	n.lowered = append(n.lowered, lowered)
	return nil
}

// claimLowered claims every name the lowering rules resolved, as objects of
// namespace (a cluster-scoped one of none, a Flux-scoped one of fluxNamespace
// when the transform has one), in the order they were resolved. It
// runs once lowering has settled and before any other name is resolved, so a
// name resolved later that names the same object is refused with both named.
//
// Two lowered names that land on one object are refused here whoever resolved
// them. recordLowered left one pair to this point, a Flux-scoped name and one
// that is not, and an equal owner is no exception for it either: claimName
// alone would take the second for the first one claimed again.
func (n *NameAllocator) claimLowered(namespace, fluxNamespace string) error {
	claimed := make(map[nameClaimKey]resolvedNameClaim, len(n.lowered))
	for _, lowered := range n.lowered {
		key := nameClaimKey{class: nameClassObject, objectIdentity: objectIdentity{
			group: lowered.group, kind: lowered.kind, namespace: namespace, name: lowered.name,
		}}
		switch {
		case lowered.clusterScoped:
			key.namespace = ""
		case lowered.fluxScoped && fluxNamespace != "":
			key.namespace = fluxNamespace
		}
		if prior, ok := claimed[key]; ok {
			return nameCollision(key, prior, lowered.claim)
		}
		claimed[key] = lowered.claim
		if err := n.claimName(key, lowered.claim); err != nil {
			return err
		}
	}
	return nil
}
