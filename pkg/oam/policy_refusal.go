package oam

import "github.com/go-kure/launcher/pkg/errors"

// RefusalClass names what a refusal by the environment policy is about, so
// that a consumer can tell one kind of refusal from another without matching
// error text (go-kure/launcher#849). A consumer reads it from
// ViolationError.Class.
//
// The classes the library itself gives form a closed set, the constants below
// and RefusalClasses. A consumer's own Enforceable may give one of them, or a
// value of its own, through NewPolicyRefusal: the library passes a class on as
// it was given.
type RefusalClass string

const (
	// RefusalUnclassified is the value of a violation whose cause carries no
	// class: an ApplyPolicy error that is not a refusal by the policy (a chart
	// that does not render, an invalid policy default or maximum, the library's
	// own rules on an object), and a refusal a consumer's Enforceable returns
	// as a plain error. It is the zero value. No class is guessed from the
	// text.
	RefusalUnclassified RefusalClass = ""

	// RefusalHostNamespace: a pod asks for the node's network, PID or IPC
	// namespace and the policy does not allow it.
	RefusalHostNamespace RefusalClass = "host-namespace"
	// RefusalPrivileged: a privileged container, or a Windows HostProcess
	// container or pod, under a policy that does not allow privileged
	// workloads.
	RefusalPrivileged RefusalClass = "privileged"
	// RefusalHostPath: a hostPath volume of a pod, or a hostPath or local
	// source of a PersistentVolume, under a policy that does not allow hostPath
	// volumes.
	RefusalHostPath RefusalClass = "host-path"
	// RefusalContainerCapability: a Linux capability a container adds that the
	// policy forbids or does not list as allowed.
	RefusalContainerCapability RefusalClass = "container-capability"
	// RefusalRegistry: a host outside the policy's allowed registries, or one
	// that cannot be held to them: the registry of a container image, and the
	// host a chart, a manifest source or a Flux source is fetched from.
	RefusalRegistry RefusalClass = "registry"
	// RefusalResourceMaximum: a cpu or memory request or limit over the
	// policy's maximum.
	RefusalResourceMaximum RefusalClass = "resource-maximum"
	// RefusalResourceDefaultLimit: a cpu or memory request the document authors
	// above the limit the policy defaults for that resource, on a container
	// that authors no such limit. The author writes a limit or lowers the
	// request.
	RefusalResourceDefaultLimit RefusalClass = "resource-default-limit"
	// RefusalStorageMaximum: a storage request, or the capacity of a
	// PersistentVolume, over the policy's maximum.
	RefusalStorageMaximum RefusalClass = "storage-maximum"
	// RefusalReplicaMaximum: a replica count, an autoscaler's maximum or a
	// database cluster's instance count over the policy's maximum.
	RefusalReplicaMaximum RefusalClass = "replica-maximum"
	// RefusalExplicitSecret: secret material the document carries itself, under
	// a policy that forbids explicit secrets (ExplicitSecretPolicy).
	RefusalExplicitSecret RefusalClass = "explicit-secret"
	// RefusalTraitCapability: a trait type the policy forbids or does not list
	// as allowed, or one it requires that the application does not use.
	RefusalTraitCapability RefusalClass = "trait-capability"
	// RefusalUnreadableObject: an object written elsewhere (rendered by a
	// chart, carried by passthrough or by a manifests source) that the build
	// cannot read, so that it cannot be held to the policy and is refused
	// rather than passed.
	RefusalUnreadableObject RefusalClass = "unreadable-object"
	// RefusalObjectKind: an object of a kind the policy forbids or does not list
	// as allowed, or a cluster-scoped one under a policy that does not allow
	// them (ObjectKindPolicy).
	RefusalObjectKind RefusalClass = "object-kind"
)

// RefusalClasses returns the classes the library gives its own refusals: the
// closed set, without RefusalUnclassified. The slice is the caller's.
func RefusalClasses() []RefusalClass {
	return []RefusalClass{
		RefusalHostNamespace,
		RefusalPrivileged,
		RefusalHostPath,
		RefusalContainerCapability,
		RefusalRegistry,
		RefusalResourceMaximum,
		RefusalResourceDefaultLimit,
		RefusalStorageMaximum,
		RefusalReplicaMaximum,
		RefusalExplicitSecret,
		RefusalTraitCapability,
		RefusalUnreadableObject,
		RefusalObjectKind,
	}
}

// PolicyRefusal is a refusal by the environment policy: its class and its
// text. Every built-in refusal by the policy is one. It sits in the cause
// chain of the ViolationError the transform or a generation returns: read the
// class from ViolationError.Class, or reach the refusal with errors.As. One
// refusal comes without a violation: a manifests url source redirected to a
// host outside the allowed registries fails as a fetch, and that error holds
// the refusal, which errors.As reaches.
type PolicyRefusal struct {
	// Class is what the refusal is about.
	Class RefusalClass
	// Message is the refusal's text. Error returns it unchanged.
	Message string
}

func (e *PolicyRefusal) Error() string { return e.Message }

// NewPolicyRefusal returns a refusal by the environment policy of the given
// class, whose text is message as given. An Enforceable returns it, bare or
// wrapped with %w, for the violation to carry the class; one that returns any
// other error gets RefusalUnclassified.
func NewPolicyRefusal(class RefusalClass, message string) error {
	return &PolicyRefusal{Class: class, Message: message}
}

// TargetManifestRefusal is a refusal by the environment policy found in the
// target.manifest of an ExternalSecret or a ClusterExternalSecret: the object
// the operator would write. It wraps the PolicyRefusal, so the class a
// ViolationError reports is unchanged (a Secret there is RefusalExplicitSecret,
// as it is where a document carries one as an object) and errors.As still
// reaches the refusal. What it adds is where the refusal was found: a consumer
// that gives its own users other advice for a Secret the operator would write
// than for one the document carries finds it with errors.As, and never by
// reading the message.
//
// Its text is the refusal's, preceded by the path: At, then "target.manifest: ".
type TargetManifestRefusal struct {
	// At is where the spec starts in the component's properties: empty for an
	// externalsecret, and "externalSecretSpec." for a clusterexternalsecret. It
	// ends in a dot because it is a prefix of the path in the text.
	At string
	// Cause is the refusal, a PolicyRefusal or an error that wraps one.
	Cause error
}

func (e *TargetManifestRefusal) Error() string { return e.At + "target.manifest: " + e.Cause.Error() }

func (e *TargetManifestRefusal) Unwrap() error { return e.Cause }

// NewViolationError returns the violation of a component whose Enforceable
// refused with cause. Its Class is that of the first PolicyRefusal in the
// cause chain, and RefusalUnclassified when the chain holds none. The
// transform builds its violations with it, and so do the built-in components
// that hold an object to the policy again at generation.
func NewViolationError(component string, cause error) *ViolationError {
	v := &ViolationError{Component: component, Cause: cause}
	var refusal *PolicyRefusal
	if errors.As(cause, &refusal) {
		v.Class = refusal.Class
	}
	return v
}
