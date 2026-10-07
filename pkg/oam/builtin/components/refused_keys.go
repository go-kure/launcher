package components

import (
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// This file holds what the hand-parsed component types share for the upstream
// fields they do not read (go-kure/launcher#790). Such a type parses its
// properties key by key, so a field of the Kubernetes type it projects that it
// does not read under its name is a key of one of its refusal maps, with the
// text that says why, or that names the shape the field is read in. Two paths
// then give that text:
//
//   - the type's parser, which a caller that drives Transform without the
//     document check reaches with whatever was authored (refusedProperty, or
//     the loop of the parser that owns the map);
//   - the document check (Transformer.ValidateAuthoredProperties), which
//     refuses a key the type does not declare before the parser runs, and
//     appends the type's UnsupportedFieldHint to that refusal (refusedKeyHint).
//
// A key that is no field of the upstream type is not listed: the document
// check refuses it as it refuses any undeclared key, and a caller that skips
// the check has it dropped, as pkg/oam's README states. The one exception is
// jobCronOnlyRejectedKeys, the CronJobSpec fields `job` refuses by name.
//
// The hint is a type's answer for its top-level keys. A field below the top
// level, inside a property the type declares, is refused by the parser of that
// property (resourcesRejectedKeys, affinityShorthandRejectedKeys below,
// initContainerRejectedKeys, volumeClaimTemplateRejectedKeys); the document
// check appends the type's NestedUnsupportedFieldHint to its refusal of such a
// key (refusedNestedKeyHint).

// mainContainerRejectedKeys are the corev1.Container fields a workload type's
// main container is not authored with. That container is authored as the type's
// own top-level keys, so parseMainContainerFields refuses these there; on an
// `initContainers` or `sidecars` entry several of the names are the entry's own
// keys.
//
// One is read on no main container. The others are read in another shape, and
// the reason names it, so the upstream name is not dropped in silence either.
var mainContainerRejectedKeys = map[string]string{
	// validateContainerRestartPolicy (pkg/apis/core/validation) refuses rules
	// on a container that sets no restartPolicy of its own. An init entry
	// reads both (parseInitContainerRestart); a main container reads neither.
	"restartPolicyRules": "restartPolicyRules: not authorable — upstream accepts a container's restart rules only together with the container's own restartPolicy, which this component does not read",
	// buildMainContainer names the container after the component.
	"name":           "name: not authorable — the main container is named after the component",
	"livenessProbe":  "livenessProbe: authored as probes.liveness",
	"readinessProbe": "readinessProbe: authored as probes.readiness",
	"startupProbe":   "startupProbe: authored as probes.startup",
	"volumeMounts":   "volumeMounts: authored on the volume — a volumes entry's mountPath mounts it into the main container",
	"volumeDevices":  "volumeDevices: authored on the volume — a volumes entry's devicePath attaches a volumeMode: Block claim to the main container",
}

// appsPodTemplateRejectedKeys are the corev1.PodSpec fields the types whose
// object is an apps/v1 workload do not read: apps/v1 validation leaves an
// author nothing to choose on a Deployment's, a StatefulSet's or a DaemonSet's
// pod template (ValidatePodTemplateSpecForReplicaSet, ValidateStatefulSetSpec
// and ValidateDaemonSetSpec in pkg/apis/apps/validation). `restartPolicy` is
// the container field of that name too, which no type reads for its main
// container; on `job` and `cronjob` the key is the pod's, and read.
// parsePodSpec refuses them on a pod that is no Job's.
var appsPodTemplateRejectedKeys = map[string]string{
	"restartPolicy":         "restartPolicy: not authorable — apps/v1 validation accepts only Always as the restart policy of a Deployment's, a StatefulSet's or a DaemonSet's pod template, which is the API's default; a container's own restartPolicy is not read",
	"activeDeadlineSeconds": "activeDeadlineSeconds: " + podSpecJobOnlyReason,
}

// resourcesRejectedKeys are the corev1.ResourceRequirements fields a
// `resources` property is not read with (parseResources). Reading `claims`
// would need the pod's `resourceClaims` beside it, to hold each entry to a
// claim the pod declares, and parseResources is handed the one object.
var resourcesRejectedKeys = map[string]string{
	"claims": "resources.claims: not read by this component — an entry names one of the pod's resourceClaims, and a container's resources are read without them; upstream puts the field behind the DynamicResourceAllocation feature gate",
}

// affinityShorthandReason ends the refusal of a corev1.Affinity field on the
// types whose `affinity` is the shorthand.
const affinityShorthandReason = "on this component type affinity is a shorthand of four keys (enablePodAntiAffinity, topologyKey, podAntiAffinityType, nodeSelector), not a Kubernetes Affinity; an affinity in the Kubernetes shape is authored on a deployment, statefulset, daemonset, job or cronjob component"

// affinityShorthandRejectedKeys are the corev1.Affinity fields: all of them,
// since the `affinity` of `webservice` and `worker` is the
// shorthand parseAffinity reads (schemaAffinity) and holds no field of the
// upstream type under its name. Each reason says what the shorthand has for
// the field.
var affinityShorthandRejectedKeys = map[string]string{
	"nodeAffinity":    "affinity.nodeAffinity: not read — the shorthand's nodeSelector is a required node affinity on the labels it lists; " + affinityShorthandReason,
	"podAffinity":     "affinity.podAffinity: not read — the shorthand has no pod affinity; " + affinityShorthandReason,
	"podAntiAffinity": "affinity.podAntiAffinity: not read — enablePodAntiAffinity, topologyKey and podAntiAffinityType are the shorthand's anti-affinity to the component's own pods; " + affinityShorthandReason,
}

// affinityShorthandKeys are the keys of the shorthand (schemaAffinity).
var affinityShorthandKeys = []string{"enablePodAntiAffinity", "topologyKey", "podAntiAffinityType", "nodeSelector"}

// refusedKeys lists, by component type, the refusal maps of the type: every
// field of the upstream type it projects that an author may not write, mapped
// to the reason. TestHandParsedKinds_CoverEveryUpstreamField holds each type to
// it field by field, and holds the two paths above to the same text.
var refusedKeys = map[string][]map[string]string{
	"deployment":            {deploymentSpecRejectedKeys, appsPodTemplateRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"webservice":            {deploymentSpecRejectedKeys, appsPodTemplateRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"worker":                {deploymentSpecRejectedKeys, appsPodTemplateRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"statefulset":           {statefulSetSpecRejectedKeys, appsPodTemplateRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"daemonset":             {daemonSetSpecRejectedKeys, appsPodTemplateRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"job":                   {jobSpecRejectedKeys, jobCronOnlyRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"cronjob":               {cronJobSpecRejectedKeys, jobSpecRejectedKeys, podSpecRejectedKeys, mainContainerRejectedKeys},
	"service":               {serviceRejectedKeys},
	"persistentvolumeclaim": {claimRejectedKeys},
	"serviceaccount":        {serviceAccountRejectedKeys},
}

// refusedKeyHint returns the reason componentType refuses the property key
// with, and "" when it refuses none under that name. It is the
// UnsupportedFieldHint of every type refusedKeys lists.
func refusedKeyHint(componentType, key string) string {
	for _, refusals := range refusedKeys[componentType] {
		if reason, ok := refusals[key]; ok {
			return reason
		}
	}
	return ""
}

// nestedRefusal is the refusal map of the parser of an object below a type's
// top level, and the text that parser refuses a key of the map with, given
// the key and its reason.
type nestedRefusal struct {
	refused map[string]string
	text    func(key, reason string) string
}

// reasonOnly is the text of a parser that refuses with the reason alone, which
// names the key's path itself (resourcesRejectedKeys,
// affinityShorthandRejectedKeys).
func reasonOnly(_, reason string) string { return reason }

// containerNestedRefusals are the objects below the top level that every
// workload type of refusedKeys declares and whose parser refuses keys by name,
// keyed by the declared keys down to the object, joined with "." (array items
// unnamed): the main container's `resources`, an init container entry, and the
// `resources` of an init container or a sidecar entry. A type with no
// `sidecars` property never reaches that row.
var containerNestedRefusals = map[string]nestedRefusal{
	"resources": {resourcesRejectedKeys, reasonOnly},
	"initContainers": {initContainerRejectedKeys, func(key, reason string) string {
		return key + ": not supported on an init container — " + reason
	}},
	"initContainers.resources": {resourcesRejectedKeys, reasonOnly},
	"sidecars.resources":       {resourcesRejectedKeys, reasonOnly},
}

// nestedRefusedKeys lists, by component type, the objects below its top level
// whose parser refuses keys by name (see containerNestedRefusals): the
// `affinity` shorthand of webservice and worker, and a statefulset's
// `volumeClaimTemplates` entry, besides the container ones.
var nestedRefusedKeys = map[string]map[string]nestedRefusal{
	"deployment": containerNestedRefusals,
	"webservice": withNestedRefusals(containerNestedRefusals, "affinity", nestedRefusal{affinityShorthandRejectedKeys, reasonOnly}),
	"worker":     withNestedRefusals(containerNestedRefusals, "affinity", nestedRefusal{affinityShorthandRejectedKeys, reasonOnly}),
	"statefulset": withNestedRefusals(containerNestedRefusals, "volumeClaimTemplates", nestedRefusal{volumeClaimTemplateRejectedKeys, func(key, reason string) string {
		return key + ": not authorable — " + reason
	}}),
	"daemonset": containerNestedRefusals,
	"job":       containerNestedRefusals,
	"cronjob":   containerNestedRefusals,
}

// withNestedRefusals returns a copy of refusals with r added under path.
func withNestedRefusals(refusals map[string]nestedRefusal, path string, r nestedRefusal) map[string]nestedRefusal {
	out := maps.Clone(refusals)
	out[path] = r
	return out
}

// refusedNestedKeyHint returns the reason componentType refuses key with
// inside the object parents lead to, and "" when the parser of that object
// refuses none under that name. It is the NestedUnsupportedFieldHint of every
// type nestedRefusedKeys lists.
func refusedNestedKeyHint(componentType string, parents []string, key string) string {
	r, ok := nestedRefusedKeys[componentType][strings.Join(parents, ".")]
	if !ok {
		return ""
	}
	reason, ok := r.refused[key]
	if !ok {
		return ""
	}
	return r.text(key, reason)
}

// The methods below give the document check the reason a type refuses key
// with: one per type refusedKeys lists (the serviceaccount's is beside its
// map), and for a key below the top level one per type nestedRefusedKeys
// lists. A lowering rule answers with the maps of the kind it lowers to, whose
// parsers its own parser runs.

// NestedUnsupportedFieldHint is the deployment's refusedNestedKeyHint.
func (h *DeploymentHandler) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("deployment", parents, key)
}

// NestedUnsupportedFieldHint is the webservice's refusedNestedKeyHint.
func (WebserviceRule) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("webservice", parents, key)
}

// NestedUnsupportedFieldHint is the worker's refusedNestedKeyHint.
func (WorkerRule) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("worker", parents, key)
}

// NestedUnsupportedFieldHint is the statefulset's refusedNestedKeyHint.
func (h *StatefulsetHandler) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("statefulset", parents, key)
}

// NestedUnsupportedFieldHint is the daemonset's refusedNestedKeyHint.
func (h *DaemonsetHandler) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("daemonset", parents, key)
}

// NestedUnsupportedFieldHint is the job's refusedNestedKeyHint.
func (h *JobHandler) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("job", parents, key)
}

// NestedUnsupportedFieldHint is the cronjob's refusedNestedKeyHint.
func (h *CronjobHandler) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("cronjob", parents, key)
}

// UnsupportedFieldHint is the deployment's refusedKeyHint.
func (h *DeploymentHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("deployment", key)
}

// UnsupportedFieldHint is the webservice's refusedKeyHint.
func (WebserviceRule) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("webservice", key)
}

// UnsupportedFieldHint is the worker's refusedKeyHint.
func (WorkerRule) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("worker", key)
}

// UnsupportedFieldHint is the statefulset's refusedKeyHint.
func (h *StatefulsetHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("statefulset", key)
}

// UnsupportedFieldHint is the daemonset's refusedKeyHint.
func (h *DaemonsetHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("daemonset", key)
}

// UnsupportedFieldHint is the job's refusedKeyHint.
func (h *JobHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("job", key)
}

// UnsupportedFieldHint is the cronjob's refusedKeyHint.
func (h *CronjobHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("cronjob", key)
}

// UnsupportedFieldHint is the service's refusedKeyHint.
func (h *ServiceHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("service", key)
}

// UnsupportedFieldHint is the persistentvolumeclaim's refusedKeyHint. The `pvc`
// trait gives the same answer: it reads a claim through this type's parser
// (ParseClaimProperties).
func (h *PersistentVolumeClaimHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("persistentvolumeclaim", key)
}

// refusedProperty returns the refusal of the first property of props, in key
// order, that one of refusals refuses, and nil when there is none. A key is
// refused whatever its value, null included: it is not the type's to read.
func refusedProperty(props map[string]any, refusals ...map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		for _, refused := range refusals {
			if reason, ok := refused[key]; ok {
				return errors.New(reason)
			}
		}
	}
	return nil
}
