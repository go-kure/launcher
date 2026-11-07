package components

import (
	"maps"

	"github.com/go-kure/launcher/pkg/oam"
)

// The raw-PodSpec kinds leave their containers and pod template open (AdditionalProperties
// with no Properties), so the gate check cannot find a Gate declared below them. They
// name the gated paths on the open node instead (oam.PropertySchema.GatedPaths,
// go-kure/launcher#1245). The lists below are the one place those paths are written:
// the pod kind and every kind that takes a pod template build theirs from them, so the
// two cannot diverge. Each gate names a row of the lifecycle table in
// pkg/oam/featuregates.go, and a test holds every path to a field of the Kubernetes type.

// containerGatedPaths are the gated paths below one container, regular or init: a raw
// PodSpec takes the same fields on both (the init-only limit on restart rules belongs to
// the workload kinds' shorthand, not to a PodSpec).
var containerGatedPaths = map[string]string{
	"env[].valueFrom.fileKeyRef": "EnvFiles",
	"restartPolicyRules":         "ContainerRestartRules",
}

// podSpecGatedPaths are the gated paths of a pod spec's own fields. The pod kind
// declares these two at its top level, with a Gate, so only the pod template needs them.
var podSpecGatedPaths = map[string]string{
	"hostnameOverride": "HostnameOverride",
	"resources":        "PodLevelResources",
}

// withItemGatedPaths returns the list schema s with paths on its items.
func withItemGatedPaths(s oam.PropertySchema, paths map[string]string) oam.PropertySchema {
	item := *s.Items
	item.GatedPaths = maps.Clone(paths)
	s.Items = &item
	return s
}

// podTemplateGatedPaths are the gated paths below a `template` property, a
// corev1.PodTemplateSpec: the pod spec's fields under spec, and those of its containers.
func podTemplateGatedPaths() map[string]string {
	paths := map[string]string{}
	for path, gate := range podSpecGatedPaths {
		paths["spec."+path] = gate
	}
	for path, gate := range containerGatedPaths {
		paths["spec.containers[]."+path] = gate
		paths["spec.initContainers[]."+path] = gate
	}
	return paths
}
