package oam

import (
	"slices"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Policy provides environment-level constraints and defaults for OAM component
// and trait handlers. Handlers call its methods; they must not type-assert. A
// constraint added after the interface was fixed is an optional interface
// beside it (ExplicitSecretPolicy, ObjectKindPolicy), asked through pkg/oam, so
// an existing Policy implementation keeps compiling.
//
// The 23 typed accessor methods correspond to every piece of data that handlers
// currently access via the downstream runtime's *api.EnvironmentPolicy. See the finalized design
// in docs/oam/options-policy-interface.md (Option A).
//
// When no policy is supplied the runtime passes NoopPolicy, so handlers always
// receive a non-nil Policy value and nil checks are not needed.
type Policy interface {
	// Enforced limits — nil or empty string means no limit is set.
	MaxReplicas() *int32
	MaxCPU() string
	MaxMemory() string
	MaxStorageSize() string
	AllowedRegistries() []string

	// Defaults — nil or empty string means leave the OAM value as-is.
	DefaultReplicas() *int32
	DefaultCPURequest() string
	DefaultMemoryRequest() string
	DefaultCPULimit() string
	DefaultMemoryLimit() string

	// Workload-shape defaults — nil or empty string means leave the OAM value as-is.
	DefaultStorageSize() string
	DefaultScalerMinReplicas() *int32
	DefaultScalerMaxReplicas() *int32

	// Security flags — false is the zero value (default-deny).
	AllowHostNetwork() bool
	AllowPrivileged() bool
	AllowHostPID() bool
	AllowHostIPC() bool
	AllowHostPathVolumes() bool

	// Capability constraints — nil means unconstrained. They judge the trait types
	// an author wrote and those a component-position lowering rule wrote onto a part
	// it emitted, so an emitted trait also counts toward RequiredCapabilities.
	// Launcher's own component rules (RegisterBuiltinComponentLowering) are exempt:
	// their traits are launcher's opinions. A trait-position rule's output is not
	// judged: its authored type is.
	AllowedCapabilities() []string
	ForbiddenCapabilities() []string
	RequiredCapabilities() []string

	// Container capability constraints — Linux capabilities on a container's
	// securityContext.capabilities.add, NOT the OAM trait-type strings the three
	// methods above gate. Default-allow, mirroring AllowedRegistries: nil or empty
	// Allowed means no restriction (any capability may be added); nil or empty
	// Forbidden means no forbids. Forbidden wins when both are set and overlap.
	AllowedContainerCapabilities() []string
	ForbiddenContainerCapabilities() []string
}

// ExplicitSecretPolicy is an optional interface of a Policy: whether a document
// may carry secret material itself, which then lands in the build output as a
// Secret (base64-encoded, not encrypted), in a chart rendered at build time, or
// in plain text in the object a component writes it into. The built-in sources
// of such material include the secret trait, the secretValues of a helm or
// helmtemplate component (go-kure/launcher#786), a core Secret the passthrough
// or manifests component carries (go-kure/launcher#794), and an `address` of a
// fluxcd-provider that is the Provider's credential (go-kure/launcher#973). Each
// is refused as RefusalExplicitSecret.
//
// A Policy that does not implement it allows explicit secrets, and so does
// NoopPolicy: the interface exists so that adding the constraint changes
// nothing for a consumer that has not asked for it. A consumer that forbids
// them returns false, and its authors reference a Secret created out of band
// instead.
type ExplicitSecretPolicy interface {
	AllowExplicitSecrets() bool
}

// ExplicitSecretsAllowed reports whether policy allows a document to carry
// secret material itself: false only for a policy that implements
// ExplicitSecretPolicy and forbids it.
func ExplicitSecretsAllowed(policy Policy) bool {
	p, ok := policy.(ExplicitSecretPolicy)
	return !ok || p.AllowExplicitSecrets()
}

// ObjectKindPolicy is an optional interface of a Policy: which kinds of object a
// build may emit, and whether it may emit cluster-scoped objects
// (go-kure/launcher#922). It gates what the build emits, whatever emitted it: a
// kind component, a trait, the passthrough and manifests components, a chart
// rendered at build time. The capability lists above keep their meaning, trait
// types only.
//
// A kind is matched by its API group and kind, in any version. An entry whose
// Kind is "*" matches every kind of its group; the core group is "". An entry
// with no Kind is refused when the transform starts.
//
// A Policy that does not implement it allows every kind and cluster-scoped
// objects, and so does NoopPolicy: the build is exactly what it was before the
// interface existed. Not covered: what a chart Flux installs renders on the
// cluster (only its HelmRelease is emitted), objects a controller creates, and
// what an RBAC object grants, which the API server holds to the identity that
// applies it (its escalate and bind checks).
type ObjectKindPolicy interface {
	// AllowedObjectKinds lists the kinds the build may emit. Nil or empty means
	// every kind.
	AllowedObjectKinds() []schema.GroupKind
	// ForbiddenObjectKinds lists the kinds the build may not emit. A kind on
	// both lists is forbidden.
	ForbiddenObjectKinds() []schema.GroupKind
	// AllowClusterScopedObjects says whether the build may emit an object of a
	// cluster-scoped kind. A kind whose scope the build does not know (neither
	// built in nor registered with kure, such as a custom resource whose CRD is
	// installed apart from the build) counts as cluster-scoped, whatever
	// namespace the object states.
	AllowClusterScopedObjects() bool
}

// Enforceable is implemented by component and trait ApplicationConfig types that
// accept per-environment policy enforcement. The runtime calls ApplyPolicy after
// each handler produces a config; configs that do not implement Enforceable are
// left unchanged. The ApplyPolicy of a trait's sub-application must not replace,
// remove or rename a component's application in the bundle, nor rename a sibling
// group member's: the transform fails (go-kure/launcher#752).
type Enforceable interface {
	ApplyPolicy(policy Policy) error
}

// PolicyDefault is one value that a config's ApplyPolicy took from a Default*
// accessor of the Policy because the author left it out.
type PolicyDefault struct {
	// Path is the property path the author would have written for the value, for
	// example "replicas" or "resources.requests.cpu". The properties the handler
	// received do not hold it.
	Path string
	// Accessor is the name of the Policy method that supplied the value, for
	// example "DefaultReplicas".
	Accessor string
	// Detail is the Source.Detail of the report entry: empty, or DetailCappedToLimit.
	Detail string
}

// PolicyDefaultReporter is an optional interface of an Enforceable config: the
// defaults its ApplyPolicy applied. The runtime reads it right after ApplyPolicy
// and TransformWithProvenance reports each as SourcePolicy under the owner of the
// properties the config was built from. A config that does not implement it
// reports nothing, and the transform's output never depends on it.
//
// A value is reported only where ApplyPolicy applied the default: a value the
// author wrote, a limit the author opted out of with `unlimited`, and a count a
// scaler owns are not.
type PolicyDefaultReporter interface {
	PolicyDefaults() []PolicyDefault
}

// PolicyDefaultLog is the list a config embeds to implement PolicyDefaultReporter.
// The zero value is empty and ready to use.
type PolicyDefaultLog struct {
	defaults []PolicyDefault
}

// ReportPolicyDefault records that a default was applied at path. A path reported
// again replaces its earlier report, so ApplyPolicy may run more than once.
func (l *PolicyDefaultLog) ReportPolicyDefault(path, accessor, detail string) {
	d := PolicyDefault{Path: path, Accessor: accessor, Detail: detail}
	for i := range l.defaults {
		if l.defaults[i].Path == path {
			l.defaults[i] = d
			return
		}
	}
	l.defaults = append(l.defaults, d)
}

// PolicyDefaults returns the defaults reported so far, in the order reported.
func (l *PolicyDefaultLog) PolicyDefaults() []PolicyDefault {
	return slices.Clone(l.defaults)
}

// BuiltinDefault is one value that launcher's own code filled after the policy
// decided, because nothing else held one: not a Default* accessor of the Policy.
type BuiltinDefault struct {
	// Path is the property path the author would have written for the value, for
	// example "enablePDB". The properties the handler received do not hold it.
	Path string
	// Ref names the fill, for example "postgresql.enablePDB".
	Ref string
}

// BuiltinDefaultReporter is an optional interface of a config that a step after the
// policy (Component.AfterPolicy) fills values on. The runtime reads it right after
// those steps, and TransformWithProvenance reports each as SourceBuiltin under the
// owner of the properties the config was built from. A config that does not
// implement it reports nothing, and the transform's output never depends on it.
type BuiltinDefaultReporter interface {
	BuiltinDefaults() []BuiltinDefault
}

// BuiltinDefaultLog is the list a config embeds to implement BuiltinDefaultReporter.
// The zero value is empty and ready to use.
type BuiltinDefaultLog struct {
	defaults []BuiltinDefault
}

// ReportBuiltinDefault records that launcher filled path. A path reported again
// replaces its earlier report, so a step may run more than once.
func (l *BuiltinDefaultLog) ReportBuiltinDefault(path, ref string) {
	d := BuiltinDefault{Path: path, Ref: ref}
	for i := range l.defaults {
		if l.defaults[i].Path == path {
			l.defaults[i] = d
			return
		}
	}
	l.defaults = append(l.defaults, d)
}

// BuiltinDefaults returns the fills reported so far, in the order reported.
func (l *BuiltinDefaultLog) BuiltinDefaults() []BuiltinDefault {
	return slices.Clone(l.defaults)
}

// NoopPolicy satisfies Policy with zero values:
// no enforced limits, no defaults applied, security-sensitive boolean flags denied by default.
// Security flags are plain bool; false means denied — this is intentional default-deny
// behaviour when no policy document is provided, not a "permit everything" stance.
// The two container-capability accessors are the exception: they are default-allow like
// AllowedRegistries — nil leaves capabilities unconstrained absent an explicit Forbidden list.
type NoopPolicy struct{}

// compile-time interface check
var _ Policy = (*NoopPolicy)(nil)

func (*NoopPolicy) MaxReplicas() *int32                      { return nil }
func (*NoopPolicy) MaxCPU() string                           { return "" }
func (*NoopPolicy) MaxMemory() string                        { return "" }
func (*NoopPolicy) MaxStorageSize() string                   { return "" }
func (*NoopPolicy) AllowedRegistries() []string              { return nil }
func (*NoopPolicy) DefaultReplicas() *int32                  { return nil }
func (*NoopPolicy) DefaultCPURequest() string                { return "" }
func (*NoopPolicy) DefaultMemoryRequest() string             { return "" }
func (*NoopPolicy) DefaultCPULimit() string                  { return "" }
func (*NoopPolicy) DefaultMemoryLimit() string               { return "" }
func (*NoopPolicy) DefaultStorageSize() string               { return "" }
func (*NoopPolicy) DefaultScalerMinReplicas() *int32         { return nil }
func (*NoopPolicy) DefaultScalerMaxReplicas() *int32         { return nil }
func (*NoopPolicy) AllowHostNetwork() bool                   { return false }
func (*NoopPolicy) AllowPrivileged() bool                    { return false }
func (*NoopPolicy) AllowHostPID() bool                       { return false }
func (*NoopPolicy) AllowHostIPC() bool                       { return false }
func (*NoopPolicy) AllowHostPathVolumes() bool               { return false }
func (*NoopPolicy) AllowedCapabilities() []string            { return nil }
func (*NoopPolicy) ForbiddenCapabilities() []string          { return nil }
func (*NoopPolicy) RequiredCapabilities() []string           { return nil }
func (*NoopPolicy) AllowedContainerCapabilities() []string   { return nil }
func (*NoopPolicy) ForbiddenContainerCapabilities() []string { return nil }
