package oam

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// NameRole says what a name launcher generates is the name of
// (go-kure/launcher#787). The roles are a closed set: NameRoles lists them, the
// README's role table documents each, and resolving a name under any other role
// is an error.
type NameRole string

const (
	// NameRoleBundle is the application's bundle. Default: the application's name.
	NameRoleBundle NameRole = "bundle"
	// NameRoleGroup is one ordered group's bundle, a child of the application's.
	// Default: "<application>-<group suffix>".
	NameRoleGroup NameRole = "group"
	// NameRoleSubApplication is an application launcher adds beside a component's
	// own: a trait's, a synthesized NetworkPolicy's. It is no object, and two
	// sub-applications may share a name.
	NameRoleSubApplication NameRole = "sub-application"
	// NameRoleNetpolSynth is a NetworkPolicy the synthesis generates.
	NameRoleNetpolSynth NameRole = "netpol-synth"
	// NameRoleHPA is the scaler trait's HorizontalPodAutoscaler.
	NameRoleHPA NameRole = "hpa"
	// NameRolePDB is the scaler trait's PodDisruptionBudget.
	NameRolePDB NameRole = "pdb"
	// NameRoleRBAC is each object the rbac trait generates: the Role and
	// RoleBinding, or the ClusterRole and ClusterRoleBinding.
	NameRoleRBAC NameRole = "rbac"
	// NameRoleNetworkPolicy is the networkpolicy trait's NetworkPolicy.
	NameRoleNetworkPolicy NameRole = "networkpolicy"
)

// nameClass is how a role's names are held apart.
type nameClass int

const (
	// nameClassObject is a Kubernetes object: claimed by kind, namespace and name.
	nameClassObject nameClass = iota + 1
	// nameClassBundle is a bundle: claimed by name across the document, whatever
	// the role, since a group's bundle and the application's are told apart by
	// name alone.
	nameClassBundle
	// nameClassSubApplication is a sub-application: not claimed. Two of one name
	// are accepted when their objects differ (a configmap trait and a pvc trait
	// both named "dup").
	nameClassSubApplication
)

// nameRoles is the closed set, in the order NameRoles returns it.
var nameRoles = []struct {
	role  NameRole
	class nameClass
}{
	{NameRoleBundle, nameClassBundle},
	{NameRoleGroup, nameClassBundle},
	{NameRoleSubApplication, nameClassSubApplication},
	{NameRoleNetpolSynth, nameClassObject},
	{NameRoleHPA, nameClassObject},
	{NameRolePDB, nameClassObject},
	{NameRoleRBAC, nameClassObject},
	{NameRoleNetworkPolicy, nameClassObject},
}

// NameRoles returns every role a name is resolved under, in a fixed order. A
// TransformContext.Naming hook sees each of them.
func NameRoles() []NameRole {
	out := make([]NameRole, len(nameRoles))
	for i, r := range nameRoles {
		out[i] = r.role
	}
	return out
}

func classOfNameRole(role NameRole) (nameClass, bool) {
	for _, r := range nameRoles {
		if r.role == role {
			return r.class, true
		}
	}
	return 0, false
}

// NameRequest is what a TransformContext.Naming hook is asked: one name
// launcher is about to use, and the name it uses when the hook declines.
type NameRequest struct {
	// Application is the name of the document being transformed.
	Application string
	// Component is the component the name belongs to. It is empty for a name the
	// document as a whole owns: the bundle, a group, and the NetworkPolicy
	// synthesized for an external backend Service.
	Component string
	// Role is what the name names.
	Role NameRole
	// Kind is the named object's kind as a collision error prints it, "Kind" or
	// "Kind.group" ("HorizontalPodAutoscaler.autoscaling"). It is empty for a
	// name that is no object (a bundle, a group, a sub-application).
	Kind string
	// Default is launcher's own name, already shortened to fit. It is what tells
	// apart several names of one component and role: the two bundles of two
	// groups, each object of the rbac trait.
	Default string
}

// NameSpec is one name a handler asks the engine to resolve: what it names, the
// author's own name for it when the author wrote one, and the default.
type NameSpec struct {
	Role NameRole
	// Kind is the named object's group and kind; zero for a role that names no
	// object.
	Kind schema.GroupKind
	// Namespace is the namespace the object is generated in; empty for a
	// cluster-scoped object and for a name that is no object.
	Namespace string
	// Property names the property the author wrote Authored in ("hpaName"). It is
	// empty when the author wrote none, and Authored is then not read: a present
	// property holding the empty string is an authored name, and is refused.
	Property string
	Authored string
	// Default is launcher's name, already shortened to fit. It is used as it is.
	Default string
}

// nameSource is where a resolved name came from, for a collision error.
type nameSource int

const (
	nameFromDefault nameSource = iota
	nameFromAuthor
	nameFromHook
)

// nameOwner is the one thing a name was resolved for. Two resolutions with equal
// owners are the same name asked for again (a trait the engine applies a second
// time), never two names.
type nameOwner struct {
	component string
	role      NameRole
	// trait is the trait type, empty for a name no trait resolves. slot is then
	// the trait's place among its component's traits: the authored index when
	// the trait was forwarded from an authored one (authored), else its position
	// where it was applied.
	trait    string
	slot     int
	authored bool
	// def is the default: what tells apart two names of one component and role.
	def string
}

func (o nameOwner) describe(source nameSource, property string) string {
	var who string
	switch {
	case o.component == "":
		who = "the application"
	case o.trait == "":
		who = fmt.Sprintf("component %q", o.component)
	default:
		who = fmt.Sprintf("component %q traits[%d] %q", o.component, o.slot, o.trait)
	}
	switch source {
	case nameFromAuthor:
		return fmt.Sprintf("%s (role %q, set by %s)", who, o.role, property)
	case nameFromHook:
		return fmt.Sprintf("%s (role %q, returned by the Naming hook in place of %q)", who, o.role, o.def)
	}
	return fmt.Sprintf("%s (role %q, its default)", who, o.role)
}

// nameClaimKey is what makes two resolved names the same name: for an object
// its group, kind, namespace and name, as CheckInDocumentCollisions keys one;
// for a bundle its name alone.
type nameClaimKey struct {
	class nameClass
	objectIdentity
}

func (k nameClaimKey) String() string {
	if k.class == nameClassBundle {
		return fmt.Sprintf("bundle %q", k.name)
	}
	return k.objectIdentity.String()
}

type resolvedNameClaim struct {
	owner    nameOwner
	source   nameSource
	property string
}

// claimName records that owner resolved the name key identifies. A second owner
// resolving it is an error naming both: two objects of one kind, namespace and
// name are one object, and two bundles of one name are one bundle. The same
// owner resolving it again claims nothing new.
//
// It knows only the names resolved through it. An object named without the
// resolver is not in it; CheckInDocumentCollisions compares every generated
// object, whoever named it.
func (n *NameAllocator) claimName(key nameClaimKey, claim resolvedNameClaim) error {
	if prior, ok := n.resolved[key]; ok {
		if prior.owner == claim.owner {
			return nil
		}
		return errors.Errorf("name collision: %s is named by %s and by %s; give one of them another name",
			key, prior.owner.describe(prior.source, prior.property), claim.owner.describe(claim.source, claim.property))
	}
	if n.resolved == nil {
		n.resolved = make(map[nameClaimKey]resolvedNameClaim)
	}
	n.resolved[key] = claim
	return nil
}

// nameResolver resolves every name of one transform: the author's own, else the
// consumer hook's, else the default. It validates a name that is not the
// default, never shortens one, and claims the result.
type nameResolver struct {
	hook        func(NameRequest) (string, bool)
	application string
	claims      *NameAllocator
}

// traitNaming is what the engine attaches to a trait before it applies it: the
// transform's resolver and where the trait stands.
type traitNaming struct {
	resolver  *nameResolver
	component string
	slot      int
	authored  bool
}

// ResolveName returns the name to use for spec, in this order: the author's
// (spec.Property set), else the one the consumer's TransformContext.Naming hook
// returns, else spec.Default. A name that is not the default must be a DNS-1123
// subdomain and is used as written or refused, never shortened. The hook is
// asked once per call and only when the author wrote no name. The result is
// claimed: when another trait, or the engine, resolved the same object name (or
// bundle name), the error names both. A sub-application name is not claimed.
//
// Only a trait the engine applies has a hook and a claim space. On a trait
// built outside a transform (a handler's Apply called directly) ResolveName
// validates an authored name and returns it or the default: the hook is not
// consulted and nothing is claimed.
func (t *Trait) ResolveName(spec NameSpec) (string, error) {
	owner := nameOwner{role: spec.Role, trait: t.Type, def: spec.Default}
	if t.naming == nil {
		return (*nameResolver)(nil).resolve(owner, spec)
	}
	owner.component, owner.slot, owner.authored = t.naming.component, t.naming.slot, t.naming.authored
	return t.naming.resolver.resolve(owner, spec)
}

func (r *nameResolver) resolve(owner nameOwner, spec NameSpec) (string, error) {
	class, known := classOfNameRole(spec.Role)
	if !known {
		return "", errors.Errorf("naming: %q is not a name role (the roles: %s)", spec.Role, joinNameRoles())
	}
	if isObject := class == nameClassObject; isObject != (spec.Kind.Kind != "") {
		if isObject {
			return "", errors.Errorf("naming: role %q names an object, and its NameSpec has no Kind", spec.Role)
		}
		return "", errors.Errorf("naming: role %q names no object, and its NameSpec has Kind %q", spec.Role, spec.Kind)
	}
	if spec.Default == "" {
		return "", errors.Errorf("naming: role %q has no default name", spec.Role)
	}

	name, source := spec.Default, nameFromDefault
	switch {
	case spec.Property != "":
		if problem := overrideNameProblem(spec.Authored); problem != "" {
			return "", errors.Errorf("%s %q cannot be the name for role %q: %s; write a valid name, or leave the property out for the default %q",
				spec.Property, spec.Authored, spec.Role, problem, spec.Default)
		}
		name, source = spec.Authored, nameFromAuthor
	case r != nil && r.hook != nil:
		answer, ok := r.hook(NameRequest{
			Application: r.application,
			Component:   owner.component,
			Role:        spec.Role,
			Kind:        spec.Kind.String(),
			Default:     spec.Default,
		})
		if ok {
			if problem := overrideNameProblem(answer); problem != "" {
				return "", errors.Errorf("the Naming hook returned %q for role %q in place of %q: %s; return a valid name, or false to keep the default",
					answer, spec.Role, spec.Default, problem)
			}
			name, source = answer, nameFromHook
		}
	}

	if r == nil || r.claims == nil || class == nameClassSubApplication {
		return name, nil
	}
	key := nameClaimKey{class: class, objectIdentity: objectIdentity{name: name}}
	if class == nameClassObject {
		key.group, key.kind, key.namespace = spec.Kind.Group, spec.Kind.Kind, spec.Namespace
	}
	if err := r.claims.claimName(key, resolvedNameClaim{owner: owner, source: source, property: spec.Property}); err != nil {
		return "", err
	}
	return name, nil
}

// overrideNameProblem returns why name cannot be an authored or hook-given
// name, or "". Every role takes one rule, the DNS-1123 subdomain, at most 253
// characters: an object's own rule for the kinds launcher names, and launcher's
// rule for a bundle, a group and a sub-application, whose defaults are built
// from an application or component name it already holds to it.
func overrideNameProblem(name string) string {
	if name == "" {
		return "it is empty"
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "not a valid DNS-1123 subdomain: " + strings.Join(errs, "; ")
	}
	return ""
}

func joinNameRoles() string {
	roles := NameRoles()
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = string(r)
	}
	return strings.Join(names, ", ")
}
