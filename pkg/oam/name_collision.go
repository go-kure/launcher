package oam

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
)

// ErrNameCollision is what every name collision answers to under errors.Is: the
// refusal of one object, bundle, hook-group name prefix or layout Kustomization
// name that two members of a
// transform named (go-kure/launcher#787). errors.As with a *NameCollisionError
// finds the same error and says what was named and by whom.
var ErrNameCollision = errors.New("oam: name collision")

// NameCollisionError is the refusal of a name two members of one transform
// resolved or claimed: two objects of one kind, namespace and name are one
// object, and two bundles of one name are one bundle. Its text is the one the
// transform has always returned, built from these fields.
//
// The transform returns it wrapped (`component "web": …`), so it is found with
// errors.As, or recognised with errors.Is(err, ErrNameCollision).
type NameCollisionError struct {
	// Kind is the group and kind of the object that was named. It is zero for a
	// name that is no object's: a bundle's (the members' Role is "bundle" or
	// "group"), a hook-group name prefix (their Role is "hook-group") or the
	// Kustomization of a component's own layout (their Role is "layout").
	Kind schema.GroupKind
	// Namespace is the object's namespace as the text prints it. It is empty for
	// a cluster-scoped object and for a name that is no object's. It is also
	// empty for two names of one lowering rule or of two that are refused while
	// lowering, before the namespace the object lands in is settled. One pair of
	// lowered names is refused only once it is settled, and carries it: a
	// NameSpec.FluxScoped name and one that is not, where both land in one
	// namespace (the transform has no Flux namespace, or its Flux namespace is
	// the one the document's objects land in).
	Namespace string
	// Name is the name the two members share.
	Name string
	// First and Second are the two members that named it, in the order they did.
	// They are equal in the "named twice by" form: one member that named the
	// object twice.
	First, Second NameCollisionMember
}

// NameCollisionMember is one of the two that named what a NameCollisionError
// refuses.
type NameCollisionMember struct {
	// Component is the component the name belongs to. It is empty for a name
	// the document as a whole owns (the bundle, a group, a source its components
	// share, the NetworkPolicy synthesized for an external backend Service),
	// which Description then says.
	Component string
	// Trait is the type of the trait that named it, empty when no trait did. Two
	// traits of one component and type are told apart in Description.
	Trait string
	// Role is the role the name was resolved under. It is empty for an object a
	// trait names itself, under no role (Trait.ClaimObjectName).
	Role NameRole
	// Property is the property the author wrote the name in, empty when the
	// author wrote none: the name is then the default, or the hook's answer
	// (FromHook).
	Property string
	// FromHook says the TransformContext.Naming hook returned the name.
	FromHook bool
	// Description is the member as the text names it, in the fewest words that
	// tell it from the other: `component "web" (role "hpa", its default)`.
	Description string
}

// Error returns the refusal's text.
func (e *NameCollisionError) Error() string {
	if e.First == e.Second {
		return fmt.Sprintf("name collision: %s is named twice by %s; give one of them another name",
			e.named(), e.First.Description)
	}
	return fmt.Sprintf("name collision: %s is named by %s and by %s; give one of them another name",
		e.named(), e.First.Description, e.Second.Description)
}

// Is makes the error answer to ErrNameCollision under errors.Is.
func (e *NameCollisionError) Is(target error) bool { return target == ErrNameCollision }

// named is what was named, as the text prints it: nameClaimKey.String, from the
// exported fields. What tells a bundle from a hook-group name prefix or a
// layout Kustomization is the role, since a role has one class
// (classOfNameRole).
func (e *NameCollisionError) named() string {
	key := nameClaimKey{class: nameClassObject, objectIdentity: objectIdentity{
		group: e.Kind.Group, kind: e.Kind.Kind, namespace: e.Namespace, name: e.Name,
	}}
	if e.Kind.Kind == "" {
		switch e.First.Role {
		case NameRoleHookGroup:
			key.class = nameClassHookGroupPrefix
		case NameRoleLayout:
			key.class = nameClassLayout
		default:
			key.class = nameClassBundle
		}
	}
	return key.String()
}

// collisionMember is claim as a NameCollisionError names it, described in as
// much detail as asked (nameOwner.describe).
func collisionMember(claim resolvedNameClaim, detail int) NameCollisionMember {
	return NameCollisionMember{
		Component:   claim.owner.component,
		Trait:       claim.owner.trait,
		Role:        claim.owner.role,
		Property:    claim.property,
		FromHook:    claim.source == nameFromHook,
		Description: claim.owner.describe(claim.source, claim.property, detail),
	}
}
