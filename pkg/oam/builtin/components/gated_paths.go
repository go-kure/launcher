package components

import (
	"maps"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The raw-PodSpec kinds leave their containers and pod template open (AdditionalProperties
// with no Properties), so the gate check cannot find a Gate declared below them. They
// name the gated paths on the open node instead (oam.PropertySchema.GatedPaths,
// go-kure/launcher#1245), and the paths whose gate holds for some values only in
// oam.PropertySchema.ValueGatedPaths (go-kure/launcher#1330). The lists below are the one
// place those paths are written: the pod kind and every kind that takes a pod template
// build theirs from them, so the two cannot diverge. Each gate names a row of the
// lifecycle table in pkg/oam/featuregates.go, and a test holds every path to a field of
// the Kubernetes type.

// containerGatedPaths are the gated paths below one container, regular or init: a raw
// PodSpec takes the same fields on both (the init-only limit on restart rules belongs to
// the workload kinds' shorthand, not to a PodSpec).
var containerGatedPaths = map[string]string{
	"env[].valueFrom.fileKeyRef": "EnvFiles",
	"restartPolicyRules":         "ContainerRestartRules",
}

// regularContainerGatedPaths are the gated paths below a regular container only. With
// ContainerRestartRules off, Kubernetes refuses every value of a regular container's
// restartPolicy, Always included, and with it on accepts all three. An init container
// takes Always at every release, so its restartPolicy is a value gate
// (initContainerValueGatedPaths) and not a path here (go-kure/launcher#1330).
var regularContainerGatedPaths = map[string]string{
	"restartPolicy": "ContainerRestartRules",
}

// initContainerRestartValueGates are the values of an init container's restartPolicy that
// need ContainerRestartRules. Always is accepted at every release, so it is not named.
// The workload kinds' shorthand (schemaInitContainerRestart) and the raw-PodSpec kinds
// (initContainerValueGatedPaths) both read this one table.
var initContainerRestartValueGates = map[string]string{
	string(corev1.ContainerRestartPolicyNever):     "ContainerRestartRules",
	string(corev1.ContainerRestartPolicyOnFailure): "ContainerRestartRules",
}

// initContainerValueGatedPaths are the value-gated paths below an init container.
var initContainerValueGatedPaths = map[string]map[string]string{
	"restartPolicy": initContainerRestartValueGates,
}

// podSpecGatedPaths are the gated paths of a pod spec's own fields. The pod kind
// declares these two at its top level, with a Gate, so only the pod template needs them.
var podSpecGatedPaths = map[string]string{
	"hostnameOverride": "HostnameOverride",
	"resources":        "PodLevelResources",
}

// regularContainerPaths returns the gated paths of a regular container: the ones every
// container takes and the ones only a regular container takes.
func regularContainerPaths() map[string]string {
	paths := maps.Clone(containerGatedPaths)
	maps.Copy(paths, regularContainerGatedPaths)
	return paths
}

// cloneValueGatedPaths returns a copy of paths that shares no map with it.
func cloneValueGatedPaths(paths map[string]map[string]string) map[string]map[string]string {
	if paths == nil {
		return nil
	}
	out := make(map[string]map[string]string, len(paths))
	for path, gates := range paths {
		out[path] = maps.Clone(gates)
	}
	return out
}

// withItemGatedPaths returns the list schema s with paths and valuePaths on its items.
func withItemGatedPaths(s oam.PropertySchema, paths map[string]string, valuePaths map[string]map[string]string) oam.PropertySchema {
	item := *s.Items
	item.GatedPaths = maps.Clone(paths)
	item.ValueGatedPaths = cloneValueGatedPaths(valuePaths)
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
	for path, gate := range regularContainerPaths() {
		paths["spec.containers[]."+path] = gate
	}
	for path, gate := range containerGatedPaths {
		paths["spec.initContainers[]."+path] = gate
	}
	return paths
}

// podTemplateValueGatedPaths are the value-gated paths below a `template` property: those
// of its init containers, under spec.
func podTemplateValueGatedPaths() map[string]map[string]string {
	paths := map[string]map[string]string{}
	for path, gates := range initContainerValueGatedPaths {
		paths["spec.initContainers[]."+path] = maps.Clone(gates)
	}
	return paths
}
