package components

import (
	"strconv"
	"strings"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/validate/content"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The kind components: each is one object named after its component, and so
// takes `objectName` and the name role "object" (oam.ComponentObjectProvider,
// go-kure/launcher#787). A handler declares here the kind and scope of that
// object; the engine resolves the name and the handler's config carries it
// (kindObjectName). With the object it takes `labels` and `annotations` for
// that object's own metadata (go-kure/launcher#790): the engine reads and
// checks them, and the handler's config carries them to the object
// (kindObject).
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

// kindObject returns what a kind component's Generate returns: obj, its one
// object, with the labels and annotations authored for it on its own metadata
// (go-kure/launcher#790). meta is what the config carries from
// oam.Component.ObjectMetadata, set in ToApplicationConfig; the zero value
// leaves obj as it is. Nothing but the object's own metadata takes them: a pod
// template obj holds keeps the labels its kind gave it.
func kindObject(obj client.Object, meta oam.ObjectMetadata) ([]*client.Object, error) {
	if err := meta.ApplyTo(obj); err != nil {
		return nil, err
	}
	return []*client.Object{&obj}, nil
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

// validateJobNameAllowsCompletions refuses the name of an Indexed job whose
// pods could not be named after it. The job controller gives the pod of each
// index the hostname "<job>-<index>", the last being completions-1, and
// validateNameAllowsCompletions (k8s.io/kubernetes, pkg/apis/batch/validation,
// called from ValidateJobCreate) refuses a Job for which that last hostname is
// not a DNS-1123 label: at most 63 characters, and no dot. name is the one the
// Job takes, and the refusal says where it came from, as validateKindNameLength
// does.
//
// The conversion refuses an Indexed job without completions, but a JobConfig
// built by hand can leave it unset. Where parallelism is unset too the API
// server reads both as 1 (SetDefaults_Job, pkg/apis/batch/v1) before it
// validates, so the rule is held on "<job>-0" there. Completions unset beside a
// set parallelism is refused by the API server for the missing completions, not
// for the name, and is not this rule's.
func validateJobNameAllowsCompletions(name, componentName string, spec JobSpecConfig) error {
	if spec.CompletionMode == nil || *spec.CompletionMode != batchv1.IndexedCompletion {
		return nil
	}
	var completions int32
	var counted string
	switch {
	case spec.Completions != nil:
		completions = *spec.Completions
		counted = "completions " + strconv.Itoa(int(completions))
	case spec.Parallelism == nil:
		completions = 1
		counted = "completions and parallelism unset, which the API server reads as completions 1,"
	}
	if completions <= 0 {
		return nil
	}
	hostname := name + "-" + strconv.Itoa(int(completions)-1)
	errs := validation.IsDNS1123Label(hostname)
	if len(errs) == 0 {
		return nil
	}
	const rule = "with completionMode Indexed and %s the pod of the last index takes the hostname %q, which must be a DNS-1123 label: %s"
	if name == componentName {
		return errors.Errorf("job %q: the component name is the Job's name, and "+rule,
			name, counted, hostname, strings.Join(errs, "; "))
	}
	return errors.Errorf("%s: %q is not a valid name for this Job: "+rule,
		objectNameField, name, counted, hostname, strings.Join(errs, "; "))
}

func coreKind(kind string) schema.GroupKind { return schema.GroupKind{Kind: kind} }

func appsKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: appsv1.GroupName, Kind: kind}
}

func batchKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: batchv1.GroupName, Kind: kind}
}

func storageKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: storagev1.GroupName, Kind: kind}
}

func networkingKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: networkingv1.GroupName, Kind: kind}
}

func cnpgKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: cnpgv1.SchemeGroupVersion.Group, Kind: kind}
}

func monitoringKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: monitoringv1.SchemeGroupVersion.Group, Kind: kind}
}

func certManagerKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: certv1.SchemeGroupVersion.Group, Kind: kind}
}

func volsyncKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: volsyncv1alpha1.GroupVersion.Group, Kind: kind}
}

func gatewayAPIKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: gatewayv1.GroupName, Kind: kind}
}

func externalSecretsKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: esv1.Group, Kind: kind}
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

// ComponentObject declares the ingress kind's Ingress. The `ingress` trait
// claims its own Ingress under the same kind, so the two are held apart.
func (h *IngressHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return networkingKind("Ingress"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the httproute kind's HTTPRoute. The `httproute`
// trait claims its own HTTPRoute under the same kind, so the two are held
// apart.
func (h *HTTPRouteHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: gatewayv1.GroupName, Kind: "HTTPRoute"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the networkpolicy kind's NetworkPolicy. The
// `networkpolicy` trait resolves its policy's name under role "networkpolicy",
// and the synthesis its policies' under "netpol-synth", both as this kind, so
// all three are held apart.
func (h *NetworkPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return networkingKind("NetworkPolicy"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cilium-networkpolicy kind's
// CiliumNetworkPolicy. The `cilium-networkpolicy` trait claims its own policy
// under the same kind, so the two are held apart.
func (h *CiliumNetworkPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: ciliumv2.CustomResourceDefinitionGroup, Kind: ciliumv2.CNPKindDefinition}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the configmap kind's ConfigMap.
func (h *ConfigMapHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("ConfigMap"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the secret kind's Secret. The `secret` trait names
// its own Secret under no role and claims nothing, so a trait's Secret of the
// same name is refused among the generated objects
// (oam.CheckInDocumentCollisions); the Secret a `helm` component generates for
// secretValues is claimed under role "values-secret" as this kind, so a secret
// component under that name is refused as a name collision.
func (h *SecretHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return coreKind("Secret"), oam.ObjectScopeNamespaced
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

// ComponentObject declares the storageclass kind's StorageClass, which is
// cluster-scoped.
func (h *StorageClassHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return storageKind("StorageClass"), oam.ObjectScopeCluster
}

// ComponentObject declares the volumeattributesclass kind's
// VolumeAttributesClass, which is cluster-scoped.
func (h *VolumeAttributesClassHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return storageKind("VolumeAttributesClass"), oam.ObjectScopeCluster
}

// ComponentObject declares the csidriver kind's CSIDriver, which is
// cluster-scoped.
func (h *CSIDriverHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return storageKind("CSIDriver"), oam.ObjectScopeCluster
}

// ComponentObject declares the priorityclass kind's PriorityClass, which is
// cluster-scoped.
func (h *PriorityClassHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: schedulingv1.GroupName, Kind: "PriorityClass"}, oam.ObjectScopeCluster
}

// ComponentObject declares the runtimeclass kind's RuntimeClass, which is
// cluster-scoped.
func (h *RuntimeClassHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: nodev1.GroupName, Kind: "RuntimeClass"}, oam.ObjectScopeCluster
}

// ComponentObject declares the ingressclass kind's IngressClass, which is
// cluster-scoped.
func (h *IngressClassHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: networkingv1.GroupName, Kind: "IngressClass"}, oam.ObjectScopeCluster
}

// ComponentObject declares the horizontalpodautoscaler kind's
// HorizontalPodAutoscaler.
func (h *HorizontalPodAutoscalerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: autoscalingv2.GroupName, Kind: "HorizontalPodAutoscaler"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the poddisruptionbudget kind's PodDisruptionBudget.
func (h *PodDisruptionBudgetHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: policyv1.GroupName, Kind: "PodDisruptionBudget"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the servicecidr kind's ServiceCIDR, which is
// cluster-scoped.
func (h *ServiceCIDRHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: networkingv1.GroupName, Kind: "ServiceCIDR"}, oam.ObjectScopeCluster
}

// ComponentObject declares the servicemonitor kind's ServiceMonitor.
func (h *ServiceMonitorHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.ServiceMonitorsKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the podmonitor kind's PodMonitor.
func (h *PodMonitorHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.PodMonitorsKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the prometheus-probe kind's Probe.
func (h *PrometheusProbeHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.ProbesKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the prometheusrule kind's PrometheusRule.
func (h *PrometheusRuleHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.PrometheusRuleKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the issuer kind's Issuer.
func (h *IssuerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return certManagerKind(certv1.IssuerKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the clusterissuer kind's ClusterIssuer, which is
// cluster-scoped.
func (h *ClusterIssuerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return certManagerKind(certv1.ClusterIssuerKind), oam.ObjectScopeCluster
}

// ComponentObject declares the certificate kind's Certificate.
func (h *CertificateHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return certManagerKind(certv1.CertificateKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the replicationsource kind's ReplicationSource.
func (h *ReplicationSourceHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return volsyncKind("ReplicationSource"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the replicationdestination kind's
// ReplicationDestination.
func (h *ReplicationDestinationHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return volsyncKind("ReplicationDestination"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cilium-bgpadvertisement kind's
// CiliumBGPAdvertisement, which is cluster-scoped.
func (h *CiliumBGPAdvertisementHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.BGPAKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-bgpclusterconfig kind's
// CiliumBGPClusterConfig, which is cluster-scoped.
func (h *CiliumBGPClusterConfigHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.BGPCCKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-bgpnodeconfigoverride kind's
// CiliumBGPNodeConfigOverride, which is cluster-scoped.
func (h *CiliumBGPNodeConfigOverrideHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.BGPNCOKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-bgppeerconfig kind's
// CiliumBGPPeerConfig, which is cluster-scoped.
func (h *CiliumBGPPeerConfigHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.BGPPCKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-cidrgroup kind's CiliumCIDRGroup, which
// is cluster-scoped.
func (h *CiliumCIDRGroupHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.CCGKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-loadbalancerippool kind's
// CiliumLoadBalancerIPPool, which is cluster-scoped.
func (h *CiliumLoadBalancerIPPoolHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.PoolKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-egressgatewaypolicy kind's
// CiliumEgressGatewayPolicy, which is cluster-scoped.
func (h *CiliumEgressGatewayPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.CEGPKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the cilium-localredirectpolicy kind's
// CiliumLocalRedirectPolicy.
func (h *CiliumLocalRedirectPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.CLRPKindDefinition), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cilium-nodeconfig kind's CiliumNodeConfig.
func (h *CiliumNodeConfigHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.CNCKindDefinition), oam.ObjectScopeNamespaced
}

// ComponentObject declares the cilium-clusterwidenetworkpolicy kind's
// CiliumClusterwideNetworkPolicy, which is cluster-scoped. It is another kind
// than the CiliumNetworkPolicy the cilium-networkpolicy kind and trait claim.
func (h *CiliumClusterwideNetworkPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return ciliumKind(ciliumv2.CCNPKindDefinition), oam.ObjectScopeCluster
}

// ComponentObject declares the gatewayclass kind's GatewayClass, which is
// cluster-scoped.
func (h *GatewayClassHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return gatewayAPIKind("GatewayClass"), oam.ObjectScopeCluster
}

// ComponentObject declares the gateway kind's Gateway.
func (h *GatewayHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return gatewayAPIKind("Gateway"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the listenerset kind's ListenerSet.
func (h *ListenerSetHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return gatewayAPIKind("ListenerSet"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the referencegrant kind's ReferenceGrant.
func (h *ReferenceGrantHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return gatewayAPIKind("ReferenceGrant"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the backendtlspolicy kind's BackendTLSPolicy.
func (h *BackendTLSPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return gatewayAPIKind("BackendTLSPolicy"), oam.ObjectScopeNamespaced
}

// ComponentObject declares the endpointslice kind's EndpointSlice.
func (h *EndpointSliceHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: discoveryv1.GroupName, Kind: "EndpointSlice"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the role kind's Role. The rbac trait claims a Role
// too: one of the same name and namespace in a document collides with this.
func (h *RoleHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: rbacv1.GroupName, Kind: "Role"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the rolebinding kind's RoleBinding.
func (h *RoleBindingHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: rbacv1.GroupName, Kind: "RoleBinding"}, oam.ObjectScopeNamespaced
}

// ComponentObject declares the clusterrole kind's ClusterRole, which is
// cluster-scoped.
func (h *ClusterRoleHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: rbacv1.GroupName, Kind: "ClusterRole"}, oam.ObjectScopeCluster
}

// ComponentObject declares the clusterrolebinding kind's ClusterRoleBinding,
// which is cluster-scoped.
func (h *ClusterRoleBindingHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: rbacv1.GroupName, Kind: "ClusterRoleBinding"}, oam.ObjectScopeCluster
}

// ComponentObject declares the secretstore kind's SecretStore.
func (h *SecretStoreHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return externalSecretsKind(esv1.SecretStoreKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the clustersecretstore kind's ClusterSecretStore,
// which is cluster-scoped.
func (h *ClusterSecretStoreHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return externalSecretsKind(esv1.ClusterSecretStoreKind), oam.ObjectScopeCluster
}

// ComponentObject declares the externalsecret kind's ExternalSecret.
func (h *ExternalSecretHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return externalSecretsKind(esv1.ExtSecretKind), oam.ObjectScopeNamespaced
}

// ComponentObject declares the clusterexternalsecret kind's
// ClusterExternalSecret, which is cluster-scoped.
func (h *ClusterExternalSecretHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return externalSecretsKind(esv1.ClusterExtSecretKind), oam.ObjectScopeCluster
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
