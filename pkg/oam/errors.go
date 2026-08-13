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
