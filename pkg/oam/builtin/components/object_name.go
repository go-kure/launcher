package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/validate/content"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The kind components: each is one object named after its component, and so
// takes `objectName` and the name role "object" (oam.ComponentObjectProvider,
// go-kure/launcher#787). A handler declares here the kind and scope of that
// object; the engine resolves the name and the handler's config carries it
// (kindObjectName).
//
// A type that is not here generates no single object named after the
// component: `manifests`, `passthrough`, `crd` and `helmtemplate` emit what
// their source holds, under the names it gives.

// componentObjectName returns what a kind component's config carries as its
// ObjectName: the component's object name where the engine resolved one that
// is not the component's own, else "". A config whose object is named after
// its component thus carries none, and is generated as it was before the
// property existed: under the name of the Application it is generated for.
func componentObjectName(component *oam.Component) string {
	if name := component.ObjectName(); name != component.Name {
		return name
	}
	return ""
}

// kindObjectName returns the name a kind component's object takes: the one its
// config carries (componentObjectName, set in ToApplicationConfig), else
// fallback, the name a config with none is generated under.
func kindObjectName(objectName, fallback string) string {
	if objectName != "" {
		return objectName
	}
	return fallback
}

// objectNameField names, in a refusal of a kind's own name rule, where an
// object name that is not the component's came from: the author's `objectName`
// or the Naming hook's answer. A handler is handed the resolved name only, and
// its Generate a config, so it names both.
const objectNameField = oam.ObjectNameProperty + ` (or the Naming hook's answer for role "` + string(oam.NameRoleObject) + `")`

// cronJobNameMaxLength is the longest name the API server creates a CronJob
// under: 52 characters. ValidateCronJobCreate (k8s.io/kubernetes,
// pkg/apis/batch/validation) refuses a longer one, since the controller names
// each Job it creates after the CronJob plus an 11-character suffix, and that
// Job's name is held to 63.
const cronJobNameMaxLength = validation.DNS1035LabelMaxLength - 11

// jobNameMaxLength is the longest name the API server creates a Job under when
// it generates the Job's selector, as it does for every job component
// (`manualSelector` is refused): 63 characters. generateSelector
// (k8s.io/kubernetes, pkg/registry/batch/job) writes the Job's name as the
// value of the pod template's `job-name` and `batch.kubernetes.io/job-name`
// labels, and ValidateJobCreate (pkg/apis/batch/validation) refuses a template
// whose label value is longer than that.
const jobNameMaxLength = content.LabelValueMaxLength

// validateKindNameLength refuses a name longer than the API server accepts for
// the object of a kind component (typ, generating kind): a component name is
// held to 253 characters, an `objectName` to the same. name is the one the
// object takes, and the refusal says where it came from: the component name,
// or, when the object is named apart from it, `objectName` or the Naming hook
// (objectNameField).
func validateKindNameLength(typ, kind string, limit int, name, componentName string) error {
	if len(name) <= limit {
		return nil
	}
	if name == componentName {
		return errors.Errorf("%s %q: the component name is the %s's name, which must be at most %d characters: it has %d",
			typ, name, kind, limit, len(name))
	}
	return errors.Errorf("%s: %q is not a valid %s name, which must be at most %d characters: it has %d",
		objectNameField, name, kind, limit, len(name))
}

// validateCronJobName holds the name a cronjob component's CronJob takes to
// cronJobNameMaxLength.
func validateCronJobName(name, componentName string) error {
	return validateKindNameLength("cronjob", "CronJob", cronJobNameMaxLength, name, componentName)
}

// validateJobName holds the name a job component's Job takes to
// jobNameMaxLength.
func validateJobName(name, componentName string) error {
	return validateKindNameLength("job", "Job", jobNameMaxLength, name, componentName)
}

func coreKind(kind string) schema.GroupKind { return schema.GroupKind{Kind: kind} }

func appsKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: appsv1.GroupName, Kind: kind}
}

func batchKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: batchv1.GroupName, Kind: kind}
}

func cnpgKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: cnpgv1.SchemeGroupVersion.Group, Kind: kind}
}

func fluxSourceKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: sourcev1.GroupVersion.Group, Kind: kind}
}

// ComponentObject declares the deployment kind's Deployment.
func (h *DeploymentHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return appsKind("Deployment"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the daemonset kind's DaemonSet.
func (h *DaemonsetHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return appsKind("DaemonSet"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the statefulset kind's StatefulSet.
func (h *StatefulsetHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return appsKind("StatefulSet"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the replicaset kind's ReplicaSet.
func (h *ReplicaSetHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return appsKind("ReplicaSet"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cronjob kind's CronJob.
func (h *CronjobHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return batchKind("CronJob"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the job kind's Job.
func (h *JobHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return batchKind("Job"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the pod kind's Pod.
func (h *PodHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("Pod"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the replicationcontroller kind's
// ReplicationController.
func (h *ReplicationControllerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("ReplicationController"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the podtemplate kind's PodTemplate.
func (h *PodTemplateHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("PodTemplate"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the service kind's Service.
func (h *ServiceHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("Service"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the configmap kind's ConfigMap.
func (h *ConfigMapHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("ConfigMap"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the persistentvolumeclaim kind's
// PersistentVolumeClaim.
func (h *PersistentVolumeClaimHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("PersistentVolumeClaim"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the persistentvolume kind's PersistentVolume, which
// is cluster-scoped.
func (h *PersistentVolumeHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("PersistentVolume"), oam.ObjectScopeCluster
}

// ComponentObject declares the resourcequota kind's ResourceQuota.
func (h *ResourceQuotaHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("ResourceQuota"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the limitrange kind's LimitRange.
func (h *LimitRangeHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("LimitRange"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the namespace kind's Namespace, which is
// cluster-scoped.
func (h *NamespaceHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("Namespace"), oam.ObjectScopeCluster
}

// ComponentObject declares the serviceaccount kind's ServiceAccount.
func (h *ServiceAccountHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("ServiceAccount"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cnpg-cluster kind's Cluster.
func (h *CnpgClusterHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind(cnpgv1.ClusterKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cnpg-pooler kind's Pooler.
func (h *CnpgPoolerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgPoolerKind, oam.ObjectScopeNamespaced
}

// ComponentObject declares the cnpg-database kind's Database.
func (h *CnpgDatabaseHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgDatabaseKind, oam.ObjectScopeNamespaced
}

// ComponentObject declares the cnpg-objectstore kind's ObjectStore.
func (h *CnpgObjectStoreHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: barmanv1.GroupVersion.Group, Kind: "ObjectStore"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the bucket kind's Bucket, which lands in the Flux
// namespace when one is set.
func (h *BucketHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return fluxSourceKind(sourcev1.BucketKind), oam.ObjectScopeFlux
}

// ComponentObject declares the helmchart kind's HelmChart, which lands in the
// Flux namespace when one is set.
func (h *HelmChartHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return fluxSourceKind(sourcev1.HelmChartKind), oam.ObjectScopeFlux
}

// ComponentObject declares the ocirepository kind's OCIRepository, which lands
// in the Flux namespace when one is set.
func (h *OCIRepositoryHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return fluxSourceKind(sourcev1.OCIRepositoryKind), oam.ObjectScopeFlux
}

// ComponentObject declares the helmrepository kind's HelmRepository, which
// lands in the Flux namespace when one is set.
func (h *HelmRepositoryHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return fluxSourceKind(sourcev1.HelmRepositoryKind), oam.ObjectScopeFlux
}

// ComponentObject declares the gitrepository kind's GitRepository, which lands
// in the Flux namespace when one is set.
func (h *GitRepositoryHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return fluxSourceKind(sourcev1.GitRepositoryKind), oam.ObjectScopeFlux
}

// ComponentObject declares the helmrelease kind's HelmRelease, which lands in
// the Flux namespace when one is set.
func (h *HelmReleaseHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: helmv2.GroupVersion.Group, Kind: helmv2.HelmReleaseKind}, oam.ObjectScopeFlux
}

// ComponentObject declares the fluxcd-kustomization kind's Kustomization, which
// lands in the Flux namespace when one is set.
func (h *FluxcdKustomizationHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: kustv1.GroupVersion.Group, Kind: kustv1.KustomizationKind}, oam.ObjectScopeFlux
}
