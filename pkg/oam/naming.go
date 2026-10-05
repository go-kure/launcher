package oam

import (
	"fmt"
	"slices"
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
	// NameRolePooler is the CloudNativePG Pooler the postgresql component
	// generates. Default: "<component>-pooler". CloudNativePG names the pooler's
	// Service after it, so a name that is not the default must be a DNS-1035
	// label.
	NameRolePooler NameRole = "pooler"
	// NameRoleDatabase is one CloudNativePG Database object the postgresql
	// component generates. Default: "<component>-<database name>".
	NameRoleDatabase NameRole = "database"
)

// nameSyntax is the rule a name that is not the default is held to.
type nameSyntax int

const (
	// nameSyntaxSubdomain is a DNS-1123 subdomain of at most 253 characters.
	nameSyntaxSubdomain nameSyntax = iota
	// nameSyntaxLabel1035 is a DNS-1035 label of at most 63 characters.
	nameSyntaxLabel1035
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
	role   NameRole
	class  nameClass
	syntax nameSyntax
}{
	{NameRoleBundle, nameClassBundle, nameSyntaxSubdomain},
	{NameRoleGroup, nameClassBundle, nameSyntaxSubdomain},
	{NameRoleSubApplication, nameClassSubApplication, nameSyntaxSubdomain},
	{NameRoleNetpolSynth, nameClassObject, nameSyntaxSubdomain},
	{NameRoleHPA, nameClassObject, nameSyntaxSubdomain},
	{NameRolePDB, nameClassObject, nameSyntaxSubdomain},
	{NameRoleRBAC, nameClassObject, nameSyntaxSubdomain},
	{NameRoleNetworkPolicy, nameClassObject, nameSyntaxSubdomain},
	{NameRolePooler, nameClassObject, nameSyntaxLabel1035},
	{NameRoleDatabase, nameClassObject, nameSyntaxSubdomain},
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

func classOfNameRole(role NameRole) (nameClass, nameSyntax, bool) {
	for _, r := range nameRoles {
		if r.role == role {
			return r.class, r.syntax, true
		}
	}
	return 0, 0, false
}

// NameRequest is what a TransformContext.Naming hook is asked: one name
// launcher is about to use, and the name it uses when the hook declines.
type NameRequest struct {
	// Application is the name of the document being transformed, as it stands
	// when the name is made. For a name made after lowering has settled, and for
	// one a component, trait or policy lowering rule makes, that is the lowered
	// name where a document lowering rule renamed the document: such a rule runs
	// only on a document no document rule will change again. For a name a
	// document lowering rule itself makes it is the name of the document that
	// rule was given, which the rule, or a later one, may still change.
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
	// Default is launcher's own name, as launcher would use it: already shortened
	// to fit where launcher shortens a name (a group's bundle, a generated
	// object), and as long as it is where it does not (a trait's sub-application:
	// the component name plus a suffix). It is what tells apart several names of
	// one component and role: the two bundles of two groups, each object of the
	// rbac trait.
	Default string
}

// NameSpec is one name a handler asks the engine to resolve: what it names, the
// author's own name for it when the author wrote one, and the default.
type NameSpec struct {
	Role NameRole
	// Kind is the named object's group and kind; zero for a role that names no
	// object.
	Kind schema.GroupKind
	// Namespace is the namespace the object is generated in; empty for a name
	// that is no object, and for a cluster-scoped object, which says so with
	// ClusterScoped.
	Namespace string
	// ClusterScoped says the object has no namespace (a ClusterRole, a
	// Namespace): it is claimed with no namespace, and a spec that sets both
	// this and Namespace is refused. A lowering rule sets it for such an object
	// as a trait does, and has no other way to say so:
	// LoweringContext.ResolveName takes any other object to land in the
	// document's namespace. A trait's spec that sets neither is claimed with no
	// namespace too.
	ClusterScoped bool
	// Property names the property the author wrote Authored in ("hpaName"). It is
	// empty when the author wrote none, and Authored is then not read: a present
	// property holding the empty string is an authored name, and is refused.
	Property string
	Authored string
	// Default is launcher's name. It is used as it is: resolving a name never
	// shortens it, so a default that must fit a limit is shortened by its handler
	// before it asks.
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
// owners are the same name asked for again (a decorating trait the engine
// applies a second time), never two names.
type nameOwner struct {
	component string
	role      NameRole
	// trait is the trait type, empty for a name no trait resolves. slot is then
	// the trait's place among its component's traits: the authored index when
	// the trait was forwarded from an authored one (authored), else its position
	// where it was applied. member is the type of the sibling group member the
	// trait was applied on, empty outside a group.
	trait    string
	member   string
	slot     int
	authored bool
	// apply numbers the trait the engine applied (nameResolver.forTrait), zero for
	// a name no trait resolves. The slot does not tell two traits apart: a trait
	// rule's outputs keep their input's slot, and a trait forwarded to two
	// members of a sibling group is applied on each. nth counts, from one, the
	// applied traits of one component, member, slot and type: the outputs a
	// trait rule lowered one trait to.
	apply int
	nth   int
	// service is the external backend Service a NetworkPolicy was synthesized
	// for, which no component owns; empty for every other name.
	service string
	// document is the document a lowering rule resolved the name for, at
	// document or policy position; empty for every other name. LowerRaws lowers
	// several with one allocator, so the name tells two of them apart.
	document string
	// lowered is set for a name a lowering rule resolved
	// (LoweringContext.ResolveName), and for no other. It keeps such an owner
	// from being equal to the owner of a name resolved after lowering for the
	// same component, role and default: the rule's object and the later one are
	// two objects.
	lowered bool
	// def is the default: what tells apart two names of one component and role.
	def string
}

// How much describe says about a trait. A trait a rule gave a sibling group
// member is always described with the member, whose traits its slot counts.
const (
	// describeSlot describes a trait forwarded from an authored one by its
	// authored slot alone.
	describeSlot = iota
	// describeMember adds the member it was applied on: one authored trait
	// forwarded to two members.
	describeMember
	// describeOrigin says of a trait that was not forwarded from an authored one
	// that its slot counts the traits after lowering: a trait a rule added, at
	// the place an authored one holds in the document. It also says of a name a
	// lowering rule resolved that one did.
	describeOrigin
	// describeOutput adds which output of its lowering it is: one trait a trait
	// rule lowered to two of one type.
	describeOutput
)

// describe says who resolved the name and where it came from, in as much
// detail as asked (describeSlot, describeMember, describeOrigin,
// describeOutput).
func (o nameOwner) describe(source nameSource, property string, detail int) string {
	var who string
	switch {
	case o.service != "":
		who = fmt.Sprintf("external backend Service %q", o.service)
	case o.component == "" && o.document != "":
		who = fmt.Sprintf("document %q", o.document)
	case o.component == "":
		who = "the application"
	case o.trait == "":
		who = fmt.Sprintf("component %q", o.component)
	case o.member != "" && (detail >= describeMember || !o.authored):
		who = fmt.Sprintf("component %q member %q traits[%d] %q", o.component, o.member, o.slot, o.trait)
	default:
		who = fmt.Sprintf("component %q traits[%d] %q", o.component, o.slot, o.trait)
	}
	if o.trait != "" && !o.authored && detail >= describeOrigin {
		who += " after lowering"
	}
	if o.lowered && detail >= describeOrigin {
		who += " in a lowering rule"
	}
	if o.trait != "" && detail >= describeOutput {
		who += fmt.Sprintf(", output %d of its lowering", o.nth)
	}
	switch source {
	case nameFromAuthor:
		return fmt.Sprintf("%s (role %q, set by %s)", who, o.role, property)
	case nameFromHook:
		return fmt.Sprintf("%s (role %q, returned by the Naming hook in place of %q)", who, o.role, o.def)
	case nameFromDefault:
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
		return nameCollision(key, prior, claim)
	}
	if n.resolved == nil {
		n.resolved = make(map[nameClaimKey]resolvedNameClaim)
	}
	n.resolved[key] = claim
	return nil
}

// nameCollision is the error for two owners, prior and claim, that resolved the
// name key identifies.
func nameCollision(key nameClaimKey, prior, claim resolvedNameClaim) error {
	// The two are told apart in the fewest words that do.
	var first, second string
	for detail := describeSlot; detail <= describeOutput; detail++ {
		first, second = prior.owner.describe(prior.source, prior.property, detail), claim.owner.describe(claim.source, claim.property, detail)
		if first != second {
			break
		}
	}
	if first == second {
		// One trait that resolved one name for two of its objects.
		return errors.Errorf("name collision: %s is named twice by %s; give one of them another name",
			key, claim.owner.describe(claim.source, claim.property, describeSlot))
	}
	return errors.Errorf("name collision: %s is named by %s and by %s; give one of them another name", key, first, second)
}

// nameResolver resolves every name of one transform: the author's own, else the
// consumer hook's, else the default. It validates a name that is not the
// default, never shortens one, and claims the result.
type nameResolver struct {
	hook        func(NameRequest) (string, bool)
	application string
	claims      *NameAllocator
	// applied counts the traits forTrait was asked about, and outputs those of
	// them that share one component, member, slot and type.
	applied int
	outputs map[traitPlace]int
}

// traitPlace is where an applied trait stands, as a collision error describes
// it. Two traits of one place are the outputs a trait rule lowered one trait to.
type traitPlace struct {
	component, member, trait string
	slot                     int
	authored                 bool
}

// traitNaming is what the engine attaches to a trait before it applies it: the
// transform's resolver and where the trait stands.
type traitNaming struct {
	resolver  *nameResolver
	component string
	member    string
	slot      int
	authored  bool
	apply     int
	nth       int
	// subApps holds, by name, every sub-application name this trait resolved
	// since the engine last took them.
	subApps map[string][]subAppName
}

// subAppName is one sub-application name a trait resolved: where it came from
// and the default it stands for.
type subAppName struct {
	source nameSource
	def    string
}

// takeSubAppNames returns, by name, the sub-application names this trait
// resolved since the last call, and forgets them.
func (n *traitNaming) takeSubAppNames() map[string][]subAppName {
	if n == nil {
		return nil
	}
	names := n.subApps
	n.subApps = nil
	return names
}

// hookDefaults returns the defaults the Naming hook replaced with one name,
// given every resolution of that name by one trait and how many
// sub-applications of that name the trait created. It returns nil unless the
// hook named them all: a name is resolved to a string, so the engine cannot
// tell which of a trait's equal-named sub-applications is which, and says of
// each only what holds for all. One the trait did not resolve, or resolved from
// its default or an authored property, makes none of them the hook's.
func hookDefaults(resolved []subAppName, created int) []string {
	if created > len(resolved) {
		return nil
	}
	var defs []string
	for _, r := range resolved {
		if r.source != nameFromHook {
			return nil
		}
		if !slices.Contains(defs, r.def) {
			defs = append(defs, r.def)
		}
	}
	return defs
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
	owner.component, owner.member = t.naming.component, t.naming.member
	owner.slot, owner.authored = t.naming.slot, t.naming.authored
	owner.apply, owner.nth = t.naming.apply, t.naming.nth
	name, source, err := t.naming.resolver.resolveFrom(owner, spec)
	// Recorded with its source: a name the hook gave is the hook's even when it is
	// the default, since what the sibling-group check asks is who named it.
	if err == nil && spec.Role == NameRoleSubApplication {
		if t.naming.subApps == nil {
			t.naming.subApps = make(map[string][]subAppName)
		}
		t.naming.subApps[name] = append(t.naming.subApps[name], subAppName{source: source, def: spec.Default})
	}
	return name, err
}

func (r *nameResolver) resolve(owner nameOwner, spec NameSpec) (string, error) {
	name, _, err := r.resolveFrom(owner, spec)
	return name, err
}

// resolveFrom is resolve, also returning where the name came from.
func (r *nameResolver) resolveFrom(owner nameOwner, spec NameSpec) (string, nameSource, error) {
	class, syntax, known := classOfNameRole(spec.Role)
	if !known {
		return "", nameFromDefault, errors.Errorf("naming: %q is not a name role (the roles: %s)", spec.Role, joinNameRoles())
	}
	if isObject := class == nameClassObject; isObject != (spec.Kind.Kind != "") {
		if isObject {
			return "", nameFromDefault, errors.Errorf("naming: role %q names an object, and its NameSpec has no Kind", spec.Role)
		}
		return "", nameFromDefault, errors.Errorf("naming: role %q names no object, and its NameSpec has Kind %q", spec.Role, spec.Kind)
	}
	if err := clusterScopeProblem(spec); err != nil {
		return "", nameFromDefault, err
	}
	if spec.Default == "" {
		return "", nameFromDefault, errors.Errorf("naming: role %q has no default name", spec.Role)
	}

	name, source := spec.Default, nameFromDefault
	switch {
	case spec.Property != "":
		if problem := overrideNameProblem(spec.Authored, syntax); problem != "" {
			return "", nameFromDefault, errors.Errorf("%s %q cannot be the name for role %q: %s; write a valid name, or leave the property out for the default %q",
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
			if problem := overrideNameProblem(answer, syntax); problem != "" {
				return "", nameFromDefault, errors.Errorf("the Naming hook returned %q for role %q in place of %q: %s; return a valid name, or false to keep the default",
					answer, spec.Role, spec.Default, problem)
			}
			name, source = answer, nameFromHook
		}
	}

	if r == nil || r.claims == nil || class == nameClassSubApplication {
		return name, source, nil
	}
	key := nameClaimKey{class: class, objectIdentity: objectIdentity{name: name}}
	if class == nameClassObject {
		key.group, key.kind = spec.Kind.Group, spec.Kind.Kind
		if !spec.ClusterScoped {
			key.namespace = spec.Namespace
		}
	}
	if err := r.claims.claimName(key, resolvedNameClaim{owner: owner, source: source, property: spec.Property}); err != nil {
		return "", source, err
	}
	return name, source, nil
}

// clusterScopeProblem refuses a NameSpec that says its object has no namespace
// and names one all the same: a caller error, which read either way would claim
// the name where the other reading does not look for it.
func clusterScopeProblem(spec NameSpec) error {
	if spec.ClusterScoped && spec.Namespace != "" {
		return errors.Errorf("naming: the NameSpec for role %q is ClusterScoped and has Namespace %q; a cluster-scoped object has no namespace",
			spec.Role, spec.Namespace)
	}
	return nil
}

// overrideNameProblem returns why name cannot be an authored or hook-given
// name under syntax, or "". Most roles take the DNS-1123 subdomain, at most 253
// characters: an object's own rule for the kinds launcher names, and launcher's
// rule for a bundle, a group and a sub-application, whose defaults are built
// from an application or component name it already holds to it. A role whose
// object lends its name to a Service (the pooler) takes the DNS-1035 label.
func overrideNameProblem(name string, syntax nameSyntax) string {
	if name == "" {
		return "it is empty"
	}
	if syntax == nameSyntaxLabel1035 {
		if errs := validation.IsDNS1035Label(name); len(errs) > 0 {
			return "not a valid DNS-1035 label: " + strings.Join(errs, "; ")
		}
		return ""
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
