package traits

import (
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/errors"
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

// applyDefaultResource returns dflt when current is unset (empty string).
func applyDefaultResource(current, dflt string) string {
	if current != "" {
		return current
	}
	return dflt
}

// enforceMaxReplicas errors when current exceeds a set maximum (nil = no limit).
func enforceMaxReplicas(current int32, max *int32) error {
	if max == nil {
		return nil
	}
	if current > *max {
		return errors.Errorf("replicas %d exceeds enforced maximum %d", current, *max)
	}
	return nil
}

// enforceMaxStorageSize errors when current exceeds a set maximum ("" = no limit).
func enforceMaxStorageSize(current, max string) error {
	if max == "" || current == "" {
		return nil
	}
	currentQty, err := resource.ParseQuantity(current)
	if err != nil {
		return errors.Wrapf(err, "invalid storageSize value %q", current)
	}
	maxQty, err := resource.ParseQuantity(max)
	if err != nil {
		return errors.Wrapf(err, "invalid enforced max storageSize value %q", max)
	}
	if currentQty.Cmp(maxQty) > 0 {
		return errors.Errorf("storageSize %q exceeds enforced maximum %q", current, max)
	}
	return nil
}
