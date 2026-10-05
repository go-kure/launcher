package traits

import (
	"fmt"

	"github.com/go-kure/launcher/pkg/oam"
)

// applyDefaultReplicas returns dflt when the value was not user-authored and a
// policy default is set; otherwise it leaves current unchanged. Mirrors the
// component-side helper of the same name (kept trait-local to avoid coupling
// the traits and components packages).
func applyDefaultReplicas(current int32, explicit bool, dflt *int32) int32 {
	if explicit || dflt == nil {
		return current
	}
	return *dflt
}

// enforceMaxReplicas errors when current exceeds a set maximum (nil = no limit).
func enforceMaxReplicas(current int32, max *int32) error {
	if max == nil {
		return nil
	}
	if current > *max {
		return oam.NewPolicyRefusal(oam.RefusalReplicaMaximum, fmt.Sprintf("replicas %d exceeds enforced maximum %d", current, *max))
	}
	return nil
}
