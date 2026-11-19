package components

import (
	"maps"
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// The bounds the workload types declare (oam.PropertySchema.BoundedBy,
// go-kure/launcher#1580): which classes of refusal by the environment policy an
// authored value can cause, by property.
//
// No schema fragment carries one. A type states its own in its PropertySchema,
// through oam.DeclareBounds and with the arguments that say which of these
// properties it has, so a type that shares a fragment and is held to no test
// publishes none. The registry package's TestBoundedBy tests hold every entry
// to a refusal, and fail for one with no row.

// containerBounds are the bounds of one container's fields: the main
// container's with prefix "", an init container's or a sidecar's with the path
// of its list's items.
func containerBounds(prefix string) map[string][]oam.RefusalClass {
	maximum := []oam.RefusalClass{oam.RefusalResourceMaximum}
	// A request alone is also held to the limit the policy defaults.
	request := []oam.RefusalClass{oam.RefusalResourceMaximum, oam.RefusalResourceDefaultLimit}
	return map[string][]oam.RefusalClass{
		prefix + "image":                                      {oam.RefusalRegistry},
		prefix + "resources.requests.cpu":                     request,
		prefix + "resources.requests.memory":                  request,
		prefix + "resources.limits.cpu":                       maximum,
		prefix + "resources.limits.memory":                    maximum,
		prefix + "securityContext.privileged":                 {oam.RefusalPrivileged},
		prefix + "securityContext.windowsOptions.hostProcess": {oam.RefusalPrivileged},
		prefix + "securityContext.capabilities.add":           {oam.RefusalContainerCapability},
	}
}

// workloadBounds are the bounds of a type built from the shared container and
// pod fragments. sidecars, replicas and claims say whether the type has
// `sidecars`, a `replicas` the policy holds, and `volumeClaimTemplates`.
func workloadBounds(sidecars, replicas, claims bool) map[string][]oam.RefusalClass {
	maximum := []oam.RefusalClass{oam.RefusalResourceMaximum}
	hostNamespace := []oam.RefusalClass{oam.RefusalHostNamespace}
	b := map[string][]oam.RefusalClass{
		"hostNetwork": hostNamespace,
		"hostPID":     hostNamespace,
		"hostIPC":     hostNamespace,
		"podSecurityContext.windowsOptions.hostProcess": {oam.RefusalPrivileged},
		"podResources.requests.cpu":                     maximum,
		"podResources.requests.memory":                  maximum,
		"podResources.limits.cpu":                       maximum,
		"podResources.limits.memory":                    maximum,
		// A hostPath volume is refused by its type. The two nested sources are
		// open: the storage a claim template asks for, and the image reference.
		"volumes[].type":      {oam.RefusalHostPath},
		"volumes[].ephemeral": {oam.RefusalStorageMaximum},
		"volumes[].image":     {oam.RefusalRegistry},
	}
	maps.Copy(b, containerBounds(""))
	maps.Copy(b, containerBounds("initContainers[]."))
	if sidecars {
		maps.Copy(b, containerBounds("sidecars[]."))
	}
	if replicas {
		b["replicas"] = []oam.RefusalClass{oam.RefusalReplicaMaximum}
	}
	if claims {
		b["volumeClaimTemplates[].size"] = []oam.RefusalClass{oam.RefusalStorageMaximum}
		b["volumeClaimTemplates[].resources.requests.storage"] = []oam.RefusalClass{oam.RefusalStorageMaximum}
	}
	return b
}

// roleBounds are the bounds of webservice and worker: those of the deployment
// they emit, and the storage a `pvc` volume asks for with `size`. Only these two
// types read that key, and their claim is held to the storage maximum. The
// schema declares no `size` on a volume, so the volume, an open node, lists the
// class.
func roleBounds() map[string][]oam.RefusalClass {
	b := workloadBounds(true, true, false)
	b["volumes[]"] = []oam.RefusalClass{oam.RefusalStorageMaximum}
	return b
}

// podBounds are the bounds of a pod written as the API type (the pod kind).
// Its containers, volumes, security context and resources are open nodes, so
// each lists the classes of every value below it.
func podBounds() map[string][]oam.RefusalClass {
	containers := []oam.RefusalClass{oam.RefusalPrivileged, oam.RefusalContainerCapability, oam.RefusalRegistry, oam.RefusalResourceMaximum}
	hostNamespace := []oam.RefusalClass{oam.RefusalHostNamespace}
	return map[string][]oam.RefusalClass{
		"containers":      containers,
		"initContainers":  containers,
		"hostNetwork":     hostNamespace,
		"hostPID":         hostNamespace,
		"hostIPC":         hostNamespace,
		"securityContext": {oam.RefusalPrivileged},
		"resources":       {oam.RefusalResourceMaximum},
		"volumes":         {oam.RefusalHostPath, oam.RefusalRegistry, oam.RefusalStorageMaximum},
	}
}

// podTemplateBounds are the bounds of a type that holds its pod below one open
// `template` node: every class of podBounds, on that node. replicas says
// whether the type has a `replicas` the policy holds.
func podTemplateBounds(replicas bool) map[string][]oam.RefusalClass {
	return podTemplateBoundsOn("template", replicas)
}

// podTemplateBoundsOn is podTemplateBounds for a type whose open node is key, as
// `jobTemplate` is for a CronJob, whose pod sits below it.
func podTemplateBoundsOn(key string, replicas bool) map[string][]oam.RefusalClass {
	b := map[string][]oam.RefusalClass{key: slices.Concat(slices.Collect(maps.Values(podBounds()))...)}
	if replicas {
		b["replicas"] = []oam.RefusalClass{oam.RefusalReplicaMaximum}
	}
	return b
}
