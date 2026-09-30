package traits

import "github.com/go-kure/launcher/pkg/oam"

// componentLabels returns the `app` label set a trait's generated objects and
// selectors carry, as a fresh map on every call (see the Conventions section of
// this package's README: no two objects share one label map).
//
// The value is oam.ComponentLabelValue(componentName), never the raw component
// name: a name may be up to 253 characters and a label value at most 63
// (go-kure/launcher#572). It is the same function the component handlers use
// for the pods' own `app` label, so a trait's selector (the PDB, the
// NetworkPolicy podSelector) always matches the pods it targets.
func componentLabels(componentName string) map[string]string {
	return map[string]string{"app": oam.ComponentLabelValue(componentName)}
}
