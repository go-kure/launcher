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
