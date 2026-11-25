package oam

import (
	"errors"
	"fmt"
)

// ErrMissingCapability is returned when a CapabilityAware trait handler is applied
// without a matching entry in TransformContext.Capabilities.
var ErrMissingCapability = errors.New("oam: capability key not found in ClusterProfile")

// ErrPlatformReserved is returned when an authored component/trait property sets a
// key the target handler's schema marks PropertySchema.PlatformReserved (D3) — such
// a value may only arrive via ClusterProfile capability rendering.
var ErrPlatformReserved = errors.New("oam: property is platform-reserved")

// ErrReservedMetadataKey is returned when an object an application generates
// carries a label or annotation key TransformContext.ReservedMetadataKeys
// reserves. It comes from generation, not from the transform: the objects of a
// rendered chart and of a trait exist only then. The error that answers to it is
// a *ReservedMetadataKeyError, which errors.As finds and which says the owner,
// the object, the key and the entry that reserves it.
var ErrReservedMetadataKey = errors.New("oam: metadata key is reserved")

// ErrComponentLabelValue is returned when what a component generates holds the
// component label's key (TransformContext.ComponentLabelKey) with a value that
// is not the component's, or could not carry the component's. Generation
// returns it for a value on an object and for a workload selector that requires
// another value; the transform returns it for a kind component's `labels`
// property that holds such a value, and for an entry a lowering rule emitted
// whose `app` label value is another component's. The error that answers to it
// is a *ComponentLabelError, which errors.As finds and which says which of the
// four it is, the component, the object, the path, the key and the value.
var ErrComponentLabelValue = errors.New("oam: label value is not the component's")

// CapabilityError marks a refusal that names a capability in its text, and holds the
// key the text names, so a caller that adds context to the refusal reads the key from
// Capability and never from the message (go-kure/launcher#1687). Find one with
// errors.As in the error Transform, TransformWithPolicy, EvaluateProfile or
// ValidateAuthoredPropertiesWithCapabilities returns, whatever the refusal is: a
// *PropertyError, a *PolicyRefusal under a *ViolationError, a *TransformError, a
// plain error or one that wraps ErrMissingCapability.
//
// Capability is a ClusterProfile capability key, "<type>" or "<type>.<scope>". In the
// three refusals by the environment policy's capability lists (forbidden, allowed,
// required) it is the trait type the list names, which their text also calls a
// capability. The refusal is Err, and Error returns its text unchanged, so a refusal
// reads as it did before the wrapper and errors.Is and errors.As still reach it.
type CapabilityError struct {
	Capability string
	Err        error
}

func (e *CapabilityError) Error() string { return e.Err.Error() }

func (e *CapabilityError) Unwrap() error { return e.Err }

// withCapability marks err as a refusal that names the capability key. A nil err stays nil.
func withCapability(key string, err error) error {
	if err == nil {
		return nil
	}
	return &CapabilityError{Capability: key, Err: err}
}

// TransformError represents a failure in the OAM-to-kure transformation pipeline.
type TransformError struct {
	Message string
	Cause   error
}

func (e *TransformError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s", e.Message, e.Cause)
	}
	return e.Message
}

func (e *TransformError) Unwrap() error { return e.Cause }
