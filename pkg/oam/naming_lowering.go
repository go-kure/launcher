package oam

import (
	"github.com/go-kure/launcher/pkg/errors"
)

// loweredName is one object name a lowering rule resolved
// (LoweringContext.ResolveName). The namespace the object lands in is the
// document's, known only once lowering has settled, so the name is held here
// until the transform claims it (claimLowered).
type loweredName struct {
	group, kind, name string
	claim             resolvedNameClaim
}

// ResolveName returns the name of an object a lowering rule generates, in this
// order: the author's (spec.Property set), else the one the consumer's
// TransformContext.Naming hook returns, else the default "<base>-<suffix>",
// built, validated and shortened as Namer.Name builds it. A name that is not
// the default is validated for spec.Role and used as written or refused, never
// shortened. spec.Role must name an object and spec.Kind say which;
// spec.Default and spec.Namespace are not read: the default is the one built
// here, and the namespace is the document's. Where base and suffix build no
// valid default, an authored name is still used; without one the call fails
// with the default's problem, and the hook is not asked.
//
// The hook is asked with the document's name as it stands when the rule runs
// (NameRequest.Application) and the enclosing component's, empty at document
// and policy position. Only a rule the engine runs inside a transform has a
// hook: under LowerRaws, and on a context built outside the engine, the hook is
// not consulted and the name is the author's or the default.
//
// The name is claimed as the object it names: two names a rule resolves for one
// kind and name are refused here, and the transform holds every one of them
// against the names it resolves after lowering, by kind, namespace and name. It
// is not reserved as a component name. A rule that emits a component under it
// reserves it with Namer.Reserve, once per component name: two components of
// one name that one rule emits on purpose are a sibling group.
func (l LoweringContext) ResolveName(base, suffix string, spec NameSpec) (string, error) {
	if l.Namer == nil {
		return "", errors.New("lowering: ResolveName needs a LoweringContext with a Namer")
	}
	class, syntax, known := classOfNameRole(spec.Role)
	if known && class != nameClassObject {
		return "", errors.Errorf("lowering: role %q names no object; a lowering rule resolves object names only", spec.Role)
	}
	def, err := generatedName(base, suffix)
	if err != nil {
		// No default can be built from this base and suffix (a character no
		// object name takes). An authored name is what the author has for that:
		// it names the object all the same. The hook is not asked, having no
		// default to be asked about.
		if spec.Property == "" || !known {
			return "", err
		}
		if problem := overrideNameProblem(spec.Authored, syntax); problem != "" {
			return "", errors.Errorf("%s %q cannot be the name for role %q: %s; write a valid name", spec.Property, spec.Authored, spec.Role, problem)
		}
		// Stands in for the default as what tells this name's owner from
		// another of its component and role; it is never the name.
		def = base + "-" + suffix
	}
	spec.Default, spec.Namespace = def, ""

	owner := nameOwner{role: spec.Role, def: def}
	if l.Component != nil {
		owner.component = l.Component.Name
	}
	application := l.application
	if application == "" {
		application = l.Origin.Document
		if l.Document != nil {
			application = l.Document.Metadata.Name
		}
	}
	// No claim space: the name is recorded below and claimed by the transform.
	resolver := &nameResolver{hook: l.Namer.hook, application: application}
	name, source, err := resolver.resolveFrom(owner, spec)
	if err != nil {
		return "", err
	}
	lowered := loweredName{
		group: spec.Kind.Group, kind: spec.Kind.Kind, name: name,
		claim: resolvedNameClaim{owner: owner, source: source, property: spec.Property},
	}
	if err := l.Namer.recordLowered(lowered); err != nil {
		return "", err
	}
	return name, nil
}

// recordLowered holds a name a lowering rule resolved until the transform
// claims it. Two owners resolving one kind and name is an error naming both:
// every lowered name of one transform lands in one namespace.
func (n *NameAllocator) recordLowered(lowered loweredName) error {
	for _, prior := range n.lowered {
		if prior.group != lowered.group || prior.kind != lowered.kind || prior.name != lowered.name {
			continue
		}
		if prior.claim.owner == lowered.claim.owner {
			return nil
		}
		key := nameClaimKey{class: nameClassObject, objectIdentity: objectIdentity{group: lowered.group, kind: lowered.kind, name: lowered.name}}
		return nameCollision(key, prior.claim, lowered.claim)
	}
	n.lowered = append(n.lowered, lowered)
	return nil
}

// claimLowered claims every name the lowering rules resolved, as objects of
// namespace, in the order they were resolved. It runs once lowering has
// settled and before any other name is resolved, so a name resolved later that
// names the same object is refused with both named.
func (n *NameAllocator) claimLowered(namespace string) error {
	for _, lowered := range n.lowered {
		key := nameClaimKey{class: nameClassObject, objectIdentity: objectIdentity{
			group: lowered.group, kind: lowered.kind, namespace: namespace, name: lowered.name,
		}}
		if err := n.claimName(key, lowered.claim); err != nil {
			return err
		}
	}
	return nil
}
