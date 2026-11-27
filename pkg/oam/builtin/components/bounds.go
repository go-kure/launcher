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
// pod fragments. sidecars and replicas say whether the type has `sidecars` and
// a `replicas` the policy holds.
func workloadBounds(sidecars, replicas bool) map[string][]oam.RefusalClass {
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
	return b
}

// roleBounds are the bounds of webservice and worker: those of the deployment
// they emit, and the storage a `pvc` volume asks for with `size`. Only these two
// types read that key, and their claim is held to the storage maximum. The
// schema declares no `size` on a volume, so the volume, an open node, lists the
// class.
func roleBounds() map[string][]oam.RefusalClass {
	b := workloadBounds(true, true)
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

// registryBounds are the bounds of a type that has the policy hold the properties
// at paths, each an address or an image reference the object is fetched from, to
// the allowed registries. The types that use it:
//   - the url of gitrepository, helmrepository, ocirepository and
//     resourcesetinputprovider, and `source.url` of oci and helm (whose generated
//     HelmRepository, OCIRepository or GitRepository, and under template delivery
//     helmtemplate, raise the refusal);
//   - bucket, and the inline Bucket source of helm (`source.`), on `endpoint` and,
//     for the gcp provider, on `provider`, whose host Flux does not read from the
//     endpoint;
//   - `image` of imagerepository;
//   - `chart` of helmrelease, an open node: only a chart template of kind
//     OCIRepository names a host, in its `spec.url`;
//   - the `url` of crd, a redirect to another host included. Its `inline` source
//     declares nothing: a CustomResourceDefinition passes the check on the
//     objects of a document, and any other object is refused as no CRD, a plain
//     error.
func registryBounds(paths ...string) map[string][]oam.RefusalClass {
	b := map[string][]oam.RefusalClass{}
	for _, path := range paths {
		b[path] = []oam.RefusalClass{oam.RefusalRegistry}
	}
	return b
}

// documentClasses are the classes of the refusals of the objects of a document a
// handler reads object by object. A core Secret is refused under a policy that
// forbids explicit secrets (enforceExplicitSecretObject), an object that cannot
// be read is refused (an unregistered workload version, an autoscaler whose
// maxReplicas is no integer), and every other object is held to the check a
// chart's rendered objects are held to (enforceRenderedObjectPolicy): the pod
// classes of podBounds on a pod, the storage a claim asks for and a
// PersistentVolume's capacity and hostPath source, and the replica maximum. The
// image reference level (ValidateImageRefAt) and the refusal of ephemeral
// containers are plain errors and no class. RefusalObjectKind is the transform's
// check on every application, which no property carries.
func documentClasses() []oam.RefusalClass {
	return slices.Concat(podTemplateBoundsOn("template", false)["template"],
		[]oam.RefusalClass{oam.RefusalReplicaMaximum, oam.RefusalExplicitSecret, oam.RefusalUnreadableObject})
}

// manifestsBounds are the bounds of the manifests type. The url is held to the
// allowed registries, on every redirect hop too. The objects of the source,
// written inline or fetched from the url, are held to the checks of
// documentClasses (enforceManifestPolicy), so both properties list those
// classes: the document is the value of inline and is what url names.
func manifestsBounds() map[string][]oam.RefusalClass {
	return map[string][]oam.RefusalClass{
		"inline": documentClasses(),
		"url":    documentClasses(),
	}
}

// persistentVolumeBounds are the bounds of persistentvolume
// (enforcePersistentVolumePolicy): a hostPath or a local source, each a path on
// the node, refused unless the policy allows hostPath volumes, and the storage a
// volume's capacity gives, held to the storage maximum. `capacity` is an open
// object and only its `storage` key is read, so the class sits on the node. A csi
// or flexVolume source is not checked, and declares nothing.
func persistentVolumeBounds() map[string][]oam.RefusalClass {
	hostPath := []oam.RefusalClass{oam.RefusalHostPath}
	return map[string][]oam.RefusalClass{
		"hostPath": hostPath,
		"local":    hostPath,
		"capacity": {oam.RefusalStorageMaximum},
	}
}

// passthroughBounds are the bounds of the passthrough type: its `object`, an
// open node, is the one object it emits, held to the checks of documentClasses
// (enforcePassthroughPolicy).
func passthroughBounds() map[string][]oam.RefusalClass {
	return map[string][]oam.RefusalClass{"object": documentClasses()}
}

// helmTemplateBounds are the bounds of helmtemplate that sit on a property: the
// host source.url is fetched from, and secretValues, refused under a policy that
// forbids explicit secrets (HelmTemplateConfig.ApplyPolicy). The classes of the
// objects the chart renders are not declared: the render comes from source and
// values together, which no property of the document holds alone.
func helmTemplateBounds() map[string][]oam.RefusalClass {
	b := registryBounds("source.url")
	b["secretValues"] = []oam.RefusalClass{oam.RefusalExplicitSecret}
	return b
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

// The bounds of the CloudNativePG kinds and of postgresql, which lowers onto
// them. Each is read from the kind's ApplyPolicy; the structured properties of a
// kind are open nodes, so a class sits on the node and is raised by a value below
// it.

// cnpgClusterBounds are the bounds of cnpg-cluster (CnpgClusterConfig.ApplyPolicy):
// the instance count, the cpu and memory requests and limits of `resources`, the
// storage request of every volume the Cluster asks for (`storage`, `walStorage`,
// each of `tablespaces` and the claim of `ephemeralVolumeSource`), the image
// (`imageName`, and the image of each of the `postgresql.extensions`), and the
// privilege of the two security contexts. A Cluster that names neither
// `imageName` nor `imageCatalogRef` is refused under an allowlist, since the
// operator then chooses the image: the class and the property of the authored form.
func cnpgClusterBounds() map[string][]oam.RefusalClass {
	storage := []oam.RefusalClass{oam.RefusalStorageMaximum}
	return map[string][]oam.RefusalClass{
		"instances":             {oam.RefusalReplicaMaximum},
		"resources":             {oam.RefusalResourceMaximum},
		"storage":               storage,
		"walStorage":            storage,
		"tablespaces":           storage,
		"ephemeralVolumeSource": storage,
		"imageName":             {oam.RefusalRegistry},
		"postgresql":            {oam.RefusalRegistry},
		"securityContext":       {oam.RefusalPrivileged, oam.RefusalContainerCapability},
		"podSecurityContext":    {oam.RefusalPrivileged},
	}
}

// cnpgPoolerBounds are the bounds of cnpg-pooler (CnpgPoolerConfig.ApplyPolicy):
// the image PgBouncer runs (a Pooler that names it by none of `pgbouncer.image`,
// `pgbouncer.imageCatalogRef` and an image of a template container named
// "pgbouncer" leaves it to the operator, and is refused under an allowlist),
// and every class the pod template
// is held to (enforcePodTemplatePolicy), those of a pod on the one `template`
// node. The instance count is deliberately not policed.
func cnpgPoolerBounds() map[string][]oam.RefusalClass {
	b := podTemplateBoundsOn("template", false)
	b["pgbouncer"] = []oam.RefusalClass{oam.RefusalRegistry}
	return b
}

// cnpgObjectStoreBounds are the bounds of cnpg-objectstore: the cpu and memory
// of the plugin sidecar, in `instanceSidecarConfiguration.resources`, an open
// node below an open node.
func cnpgObjectStoreBounds() map[string][]oam.RefusalClass {
	return map[string][]oam.RefusalClass{"instanceSidecarConfiguration": {oam.RefusalResourceMaximum}}
}

// cnpgImageCatalogBounds are the bounds of cnpg-imagecatalog and
// cnpg-clusterimagecatalog (enforceCnpgImageCatalogPolicy): the registry of the
// three fields that name an image, `images[].image`, the image volume of
// `images[].extensions[]` and `componentImages[].image`, declared on the two lists.
func cnpgImageCatalogBounds() map[string][]oam.RefusalClass {
	return registryBounds("images", "componentImages")
}

// postgresqlBounds are the bounds of postgresql, from the Cluster and the Pooler
// it lowers to: `replicas` is the Cluster's instance count, `imageName` its
// image (the default composed from `version` is refused as an authored one is),
// `resources` and `storageSize` its cpu and memory and its storage (the 1Gi
// fallback included), and the open `pooler` node holds the PgBouncer image, which
// a pooler that names none leaves to the operator.
func postgresqlBounds() map[string][]oam.RefusalClass {
	maximum := []oam.RefusalClass{oam.RefusalResourceMaximum}
	return map[string][]oam.RefusalClass{
		"replicas":                  {oam.RefusalReplicaMaximum},
		"imageName":                 {oam.RefusalRegistry},
		"pooler":                    {oam.RefusalRegistry},
		"resources.requests.cpu":    maximum,
		"resources.requests.memory": maximum,
		"resources.limits.cpu":      maximum,
		"resources.limits.memory":   maximum,
		"storageSize":               {oam.RefusalStorageMaximum},
	}
}
