package oam

import "slices"

// ScaleTarget is the workload a scaler's HorizontalPodAutoscaler scales: the
// apiVersion and kind of its scaleTargetRef.
type ScaleTarget struct {
	APIVersion string
	Kind       string
}

// DefaultScaleTarget is the target of a scaler with no component type to read: a
// handler's Apply called outside a transform, and a config built directly. It is
// what the scaler targeted before it admitted a StatefulSet.
var DefaultScaleTarget = ScaleTarget{APIVersion: "apps/v1", Kind: "Deployment"}

// scalerTargets maps a settled component type to the workload its scaler scales
// (go-kure/launcher#1268). It is the one table behind both questions about a
// scaler: which component types admit it (traitComponentRestrictions["scaler"] is
// derived from its keys) and what its HorizontalPodAutoscaler targets, so a type
// cannot be admitted without a target or the reverse. The types are the settled
// ones: webservice and worker are lowering rules that settle to a Deployment, but
// the type the engine stamps on a trait is the one the component settled to, and
// those two stay in the table because they are admitted as authored.
var scalerTargets = map[string]ScaleTarget{
	"webservice":  DefaultScaleTarget,
	"worker":      DefaultScaleTarget,
	"deployment":  DefaultScaleTarget,
	"statefulset": {APIVersion: "apps/v1", Kind: "StatefulSet"},
}

// ScalerTargetFor returns the workload a scaler on a component of the given
// settled type scales, and whether the type has a row in the table.
func ScalerTargetFor(componentType string) (ScaleTarget, bool) {
	t, ok := scalerTargets[componentType]
	return t, ok
}

// ScalerComponentTypes returns the component types that admit a scaler, sorted.
func ScalerComponentTypes() []string {
	types := make([]string, 0, len(scalerTargets))
	for t := range scalerTargets {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

// scalerAdmittedTypes is ScalerComponentTypes as the set
// traitComponentRestrictions holds.
func scalerAdmittedTypes() map[string]bool {
	admitted := make(map[string]bool, len(scalerTargets))
	for t := range scalerTargets {
		admitted[t] = true
	}
	return admitted
}
