package components

import (
	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// AlertmanagerHandler handles OAM alertmanager components: the kind-named
// projection of a monitoring.coreos.com/v1 Alertmanager
// (go-kure/launcher#790).
//
// Its properties are the top-level fields of monitoringv1.AlertmanagerSpec,
// under their json names, decoded strictly (decodeKindSpec), less the three
// deprecated ones that name the image in parts. It emits the Alertmanager,
// named after the component unless `objectName` names it, in the build
// namespace, and nothing else: the Prometheus operator runs the pods, from a
// StatefulSet it builds. What the spec says of those pods is held to the
// environment policy as a workload kind's own fields are (alertmanagerWorkload,
// monitoring_workload.go).
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type AlertmanagerHandler struct{}

// CanHandle returns true for the alertmanager component type.
func (h *AlertmanagerHandler) CanHandle(componentType string) bool {
	return componentType == "alertmanager"
}

// ContractMetadata implements oam.ContractDescriber.
func (h *AlertmanagerHandler) ContractMetadata() oam.ContractMetadata {
	return contract("alertmanager")
}

// ComponentObject declares the alertmanager kind's Alertmanager.
func (h *AlertmanagerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.AlertmanagersKind), oam.ObjectScopeNamespaced
}

// PropertySchema declares the top-level monitoringv1.AlertmanagerSpec fields
// by their json names, less baseImage, tag and sha, which the kind refuses
// (validateAlertmanager). Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *AlertmanagerHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Alertmanager spec."
	const decoded = " Decoded strictly into the Prometheus operator's API type: see "
	const core = " Decoded strictly into the Kubernetes API type: see "
	text := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: spec + description}
	}
	flag := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: spec + description}
	}
	number := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: spec + description}
	}
	object := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: spec + description}
	}
	texts := func(description, item string) oam.PropertySchema {
		return oam.PropertySchema{
			Type: oam.PropertyTypeArray, Description: spec + description,
			Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: item},
		}
	}
	objects := func(description, item string) oam.PropertySchema {
		return oam.PropertySchema{
			Type: oam.PropertyTypeArray, Description: spec + description,
			Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: item},
		}
	}
	return map[string]oam.PropertySchema{
		"podMetadata":     object("podMetadata: the labels and annotations the operator copies onto the Alertmanager pods. A key the consumer reserves is refused here as on a workload's pod template. Nothing is added: the pods carry the component label only if it is written here with the component's own value, and without it the NetworkPolicies generated for the component do not select them. The operator sets five labels and one annotation of its own, which a value authored here does not replace." + decoded + "EmbeddedObjectMetadata in its API reference."),
		"image":           text("image: the full image reference of the alertmanager container, with a tag other than latest or a digest. Held to the EnvironmentPolicy's allowed registries. Unset, the object names no image: which image then runs is the operator's to decide, and no policy is asked about it. version is still needed for the operator to know which Alertmanager it configures."),
		"imagePullPolicy": text("imagePullPolicy: when the images of the alertmanager, config-reloader and init-config-reloader containers are pulled: Always, Never or IfNotPresent."),
		"version":         text("version: the Alertmanager version the operator configures for, such as v0.28.1."),
		"imagePullSecrets": objects("imagePullSecrets: the Secrets of the Alertmanager's namespace that hold the credentials the images are pulled with.",
			"One reference: name."),
		"secrets": texts("secrets: the Secrets of the Alertmanager's namespace mounted into the alertmanager container, each under /etc/alertmanager/secrets/<name>.",
			"The name of a Secret."),
		"configMaps": texts("configMaps: the ConfigMaps of the Alertmanager's namespace mounted into the alertmanager container, each under /etc/alertmanager/configmaps/<name>.",
			"The name of a ConfigMap."),
		"configSecret": text("configSecret: the name of the Secret, in the Alertmanager's namespace, that holds the Alertmanager configuration under the key alertmanager.yaml. Unset, alertmanager-<name of the Alertmanager>. Where the Secret or the key is missing the operator provisions a configuration that drops every notification."),
		"logLevel":     text("logLevel: the log level of Alertmanager: debug, info, warn or error."),
		"logFormat":    text("logFormat: the log format of Alertmanager: logfmt or json."),
		"replicas":     number("replicas: the number of Alertmanager pods; two or more run in high-availability mode. Held to the EnvironmentPolicy's replica maximum. Unset, the number is the operator's to decide: no replica default of the policy is applied."),
		"retention":    text("retention: how long Alertmanager keeps its data, as a number and a unit (ms, s, m or h). Unset, the API fills 120h; an empty one is refused, since the API server would replace it."),
		"storage":      object("storage: where the Alertmanager pods keep their data: emptyDir, ephemeral or volumeClaimTemplate, in that order of precedence. Unset, the storage is the operator's to decide: no storage default of the policy is applied. The storage a claim template requests is held to the EnvironmentPolicy's storage maximum; the size limit of an emptyDir is not. A claim template's labels and annotations are not read for reserved keys and take no component label, as a statefulset's are not." + decoded + "StorageSpec in its API reference."),
		"volumes": objects("volumes: further volumes of the Alertmanager pods, beside the ones the operator generates. Held to the EnvironmentPolicy as a pod's volumes are: hostPath, the storage a generic ephemeral volume's claim requests, the registry of an image volume.",
			"One volume."+core+"Volume in the Kubernetes API reference."),
		"volumeMounts": objects("volumeMounts: further volume mounts of the alertmanager container.",
			"One volume mount."+core+"VolumeMount in the Kubernetes API reference."),
		"persistentVolumeClaimRetentionPolicy": object("persistentVolumeClaimRetentionPolicy: whether the claims of the StatefulSet are deleted when it is deleted (whenDeleted) or scaled down (whenScaled): Retain, the default, or Delete." + core + "StatefulSetPersistentVolumeClaimRetentionPolicy in the Kubernetes API reference."),
		"externalUrl":                          text("externalUrl: the URL under which the Alertmanager web service is reached from outside, which the links in its notifications are built from."),
		"routePrefix":                          text("routePrefix: the path prefix Alertmanager registers its HTTP handlers under."),
		"paused":                               flag("paused: true stops the operator from acting on the objects it manages for this Alertmanager, deletion excepted."),
		"nodeSelector":                         object("nodeSelector: the node labels a node must carry for the pods to be scheduled on it."),
		"schedulerName":                        text("schedulerName: the scheduler that places the pods. Unset, the default scheduler. Not empty."),
		"resources":                            object("resources: the resource requests and limits of the alertmanager container. Its cpu and memory are held to the EnvironmentPolicy's maxima, and a request may not exceed its limit. No resource default of the policy is applied." + core + "ResourceRequirements in the Kubernetes API reference."),
		"affinity":                             object("affinity: the scheduling constraints of the pods." + core + "Affinity in the Kubernetes API reference."),
		"tolerations": objects("tolerations: the taints the pods tolerate.",
			"One toleration."+core+"Toleration in the Kubernetes API reference."),
		"topologySpreadConstraints": objects("topologySpreadConstraints: how the pods are spread over topology domains.",
			"One constraint."+core+"TopologySpreadConstraint in the Kubernetes API reference."),
		"securityContext":     object("securityContext: the pod-level security attributes of the pods. Its windowsOptions.hostProcess is refused under an EnvironmentPolicy that does not allow privileged containers." + core + "PodSecurityContext in the Kubernetes API reference."),
		"dnsPolicy":           text("dnsPolicy: the DNS policy of the pods: ClusterFirstWithHostNet, ClusterFirst, Default or None."),
		"dnsConfig":           object("dnsConfig: the DNS configuration of the pods: nameservers, searches and options." + decoded + "PodDNSConfig in its API reference."),
		"enableServiceLinks":  flag("enableServiceLinks: whether the Services of the namespace are injected into the pods' environment variables."),
		"serviceName":         text("serviceName: the name of the governing Service of the StatefulSet, which must exist in the namespace and select the pods. Unset, the operator creates and manages a headless Service named alertmanager-operated. Not empty."),
		"serviceAccountName":  text("serviceAccountName: the ServiceAccount the pods run as. Launcher creates none for it and does not check that it exists."),
		"listenLocal":         flag("listenLocal: true makes the Alertmanager web server listen on loopback only, not on the pod's address; the gossip port is not affected."),
		"podManagementPolicy": text("podManagementPolicy: how the StatefulSet creates and deletes pods when it scales: Parallel, the operator's default, or OrderedReady. Changing it recreates the StatefulSet."),
		"updateStrategy":      object("updateStrategy: how the StatefulSet replaces its pods on a change: type (RollingUpdate, the default, or OnDelete) and rollingUpdate with maxUnavailable. The API refuses rollingUpdate with another type than RollingUpdate; launcher does not check that rule." + decoded + "StatefulSetUpdateStrategy in its API reference."),
		"containers": objects("containers: further containers of the pods, and patches of the ones the operator generates: an entry named alertmanager or config-reloader is merged into that container. Each is held to the EnvironmentPolicy as a pod's containers are: the registry of an authored image, cpu and memory maxima, privilege and capabilities. An entry without an image is not checked for one.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"initContainers": objects("initContainers: further init containers of the pods, and patches of the one the operator generates (init-config-reloader). Held to the EnvironmentPolicy as containers are.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"priorityClassName": text("priorityClassName: the priority class of the pods."),
		"additionalPeers": texts("additionalPeers: further Alertmanager instances to form a high-availability cluster with, outside this object.",
			"The address of one peer."),
		"clusterAdvertiseAddress":             text("clusterAdvertiseAddress: the address advertised to the cluster's peers; needed where the pod's address is not a private one."),
		"clusterGossipInterval":               text("clusterGossipInterval: the interval between gossip attempts, as a Go duration."),
		"clusterLabel":                        text("clusterLabel: the identifier of the Alertmanager cluster; set only when the cluster includes instances outside this object."),
		"clusterPushpullInterval":             text("clusterPushpullInterval: the interval between push-pull attempts, as a Go duration."),
		"clusterPeerTimeout":                  text("clusterPeerTimeout: the timeout of cluster peering, as a Go duration."),
		"clusterPeerName":                     text("clusterPeerName: the name this instance advertises to its peers; may refer to environment variables of the alertmanager container, as $(POD_NAME). Unset, the pod's name. Requires Alertmanager v0.30.0 or later. Not empty."),
		"portName":                            text("portName: the name of the web port on the pods and the governing Service. Unset, the API fills web; an empty one is refused, since the API server would replace it."),
		"forceEnableClusterMode":              flag("forceEnableClusterMode: true keeps the cluster mode on with a single replica, for a cluster that spans several Kubernetes clusters."),
		"alertmanagerConfigSelector":          object("alertmanagerConfigSelector: the AlertmanagerConfig objects merged into this Alertmanager's configuration, by their labels. A Kubernetes label selector: matchLabels and matchExpressions."),
		"alertmanagerConfigNamespaceSelector": object("alertmanagerConfigNamespaceSelector: the namespaces AlertmanagerConfig objects are read from, by their labels. Unset, the Alertmanager's own namespace only. A Kubernetes label selector: matchLabels and matchExpressions."),
		"alertmanagerConfigMatcherStrategy":   object("alertmanagerConfigMatcherStrategy: which alerts the routes and inhibition rules of an AlertmanagerConfig object process, as type: OnNamespace (the default: the alerts whose namespace label is the object's namespace), OnNamespaceExceptForAlertmanagerNamespace or None (all alerts); an empty type is refused, since the API server would replace it." + decoded + "AlertmanagerConfigMatcherStrategy in its API reference."),
		"minReadySeconds":                     number("minReadySeconds: how many seconds a new pod must be ready before it counts as available. At least 0."),
		"hostAliases": objects("hostAliases: further entries of the pods' hosts file.",
			"One entry: ip and hostnames, both required."),
		"hostNetwork":                  flag("hostNetwork: true runs the pods in the node's network namespace. Refused under an EnvironmentPolicy that does not allow the host network."),
		"web":                          object("web: the web server's settings: tlsConfig and httpConfig, getConcurrency and timeout." + decoded + "AlertmanagerWebSpec in its API reference."),
		"limits":                       object("limits: the limits Alertmanager is started with: maxSilences and maxPerSilenceBytes. Requires Alertmanager v0.28.0 or later." + decoded + "AlertmanagerLimitsSpec in its API reference."),
		"clusterTLS":                   object("clusterTLS: the mutual TLS configuration of the gossip protocol: server and client, both required. Requires Alertmanager v0.24.0 or later." + decoded + "ClusterTLSConfig in its API reference."),
		"alertmanagerConfiguration":    object("alertmanagerConfiguration: the Alertmanager configuration, taken from the AlertmanagerConfig object `name` names in the same namespace, with global parameters and notification templates; it takes precedence over configSecret. Experimental upstream. Every credential in it is the key of a Secret." + decoded + "AlertmanagerConfiguration in its API reference."),
		"automountServiceAccountToken": flag("automountServiceAccountToken: whether a service account token is mounted into the pods."),
		"enableFeatures": texts("enableFeatures: the Alertmanager feature flags to enable. Requires Alertmanager v0.27.0 or later.",
			"The name of one feature flag."),
		"additionalArgs": objects("additionalArgs: further command-line arguments of the alertmanager container, passed as they are. Launcher does not read them: an argument can change what the fields above configure.",
			"One argument: name (required) and value."),
		"terminationGracePeriodSeconds": number("terminationGracePeriodSeconds: how many seconds the pods are given to stop. Unset, the operator's default of 120. At least 0."),
		"hostUsers":                     flag("hostUsers: false runs the pods in a user namespace of their own, not the host's."),
	}
}

// alertmanagerKind is the alertmanager kind: see policyHeldKind. The API
// requires no top-level field of the spec, and of what is authored below it
// the fields alertmanagerRequired lists; the type would write each one empty.
// The API's value rules are left to it, the expression rule on updateStrategy
// included (alertmanagerRulesLeft): the linked module ships no CRD to hold
// either to.
//
// The fields on which an authored 0 cannot be carried are the ones of a pod
// spec's containers (podSpecDefaultedZeros): the spec lists containers and
// init containers of the Kubernetes type. Three strings of the operator's own
// types cannot be carried empty either: portName, retention and the type of
// alertmanagerConfigMatcherStrategy. TestMonitoringWorkloadKinds_DefaultedZeros
// derives the list (monitoringWorkloadDefaultedZeros). The refusal holds for an
// entry that patches a container the operator generates too: the zero is
// omitted there as anywhere, and the operator's value stays.
var alertmanagerKind = &policyHeldKind[monitoringv1.AlertmanagerSpec]{
	policyFreeKind: policyFreeKind[monitoringv1.AlertmanagerSpec]{
		upstream: "monitoring.coreos.com/v1 AlertmanagerSpec",
		required: alertmanagerRequired,
		defaultedZeros: monitoringWorkloadDefaultedZeros(map[string]string{
			"portName":                               `"web"`,
			"retention":                              `"120h"`,
			"alertmanagerConfigMatcherStrategy.type": `"OnNamespace"`,
		}),
		validate: validateAlertmanager,
		build: func(name, namespace string, spec *monitoringv1.AlertmanagerSpec) client.Object {
			alertmanager := prometheus.CreateAlertmanager(name, namespace)
			spec.DeepCopyInto(&alertmanager.Spec)
			return alertmanager
		},
	},
	enforce: func(spec *monitoringv1.AlertmanagerSpec, p oam.Policy) error {
		return enforceMonitoringWorkloadPolicy(alertmanagerWorkload(spec), p)
	},
}

// alertmanagerRequired lists the fields the API requires below an authored
// parent that the type would write unauthored, the key and the operator of
// each label selector's match expression included: those of the pod spec's
// fields, of the storage's claim templates and of the two selectors of
// AlertmanagerConfig objects (labelSelectorRequired).
// TestMonitoringKinds_RequiredMatchMarkers derives it from the markers of the
// linked module's source.
var alertmanagerRequired = requiredFields(map[string]string{ //nolint:gosec // G101: the keys are field paths (clientSecret is a field's name) and the values their descriptions
	"additionalArgs[].name":    "the name of the command-line argument",
	"clusterTLS.server":        "the TLS configuration of the gossip protocol's server side",
	"clusterTLS.client":        "the TLS configuration of the gossip protocol's client side",
	"dnsConfig.options[].name": "the name of the DNS resolver option",
	"hostAliases[].ip":         "the IP address of the hosts-file entry",
	"hostAliases[].hostnames":  "the host names of the hosts-file entry",
	"updateStrategy.type":      "RollingUpdate or OnDelete",
	"alertmanagerConfiguration.global.smtp.smartHost.host":            "the host of the SMTP server",
	"alertmanagerConfiguration.global.smtp.smartHost.port":            "the port of the SMTP server",
	"alertmanagerConfiguration.global.httpConfig.oauth2.clientId":     "the Secret or ConfigMap key that holds the OAuth2 client id",
	"alertmanagerConfiguration.global.httpConfig.oauth2.clientSecret": "the Secret key that holds the OAuth2 client secret",
	"alertmanagerConfiguration.global.httpConfig.oauth2.tokenUrl":     "the URL tokens are fetched from",
}, labelSelectorRequired(append(podSpecLabelSelectors(""),
	"storage.volumeClaimTemplate.spec.selector",
	"storage.ephemeral.volumeClaimTemplate.spec.selector",
	"alertmanagerConfigSelector",
	"alertmanagerConfigNamespaceSelector",
)...))

// alertmanagerRulesLeft lists, by the property that reaches it, each
// expression rule the API states on a type the spec reaches. None is checked
// here: the linked module ships no CRD, so there is no rule text for the
// validator harness to evaluate. TestMonitoringWorkloadKinds_RulesListed
// derives the list from the markers of the module's source.
var alertmanagerRulesLeft = map[string]string{
	"updateStrategy": "rollingUpdate requires type to be RollingUpdate",
}

// validateAlertmanager refuses, with or without an environment policy, the
// three deprecated fields that name the image in parts, and what
// validateMonitoringWorkload refuses of the workload.
//
// baseImage, tag and sha are refused when not empty: the operator composes
// the image from them, and from version, in code that is not in the linked
// module, so the kind cannot say which image runs and has nothing to hold to
// the allowed registries or the tag rule. An authored empty string is the same
// object as none, and is left out as the type leaves it out.
func validateAlertmanager(spec *monitoringv1.AlertmanagerSpec) error {
	for _, field := range []fieldValue{{"baseImage", spec.BaseImage}, {"sha", spec.SHA}, {"tag", spec.Tag}} {
		if field.value != "" {
			return errors.Errorf("%s: not authorable: the Prometheus operator deprecates the field, and composes the image it yields outside what the object states; use image", field.path)
		}
	}
	return validateMonitoringWorkload(alertmanagerWorkload(spec))
}

// alertmanagerWorkload maps an Alertmanager spec into the workload the two
// shared functions read. It only reads spec.
//
// Held through it: image; replicas; storage; resources, the alertmanager
// container's; and, as pod fields, containers, initContainers, volumes,
// securityContext and hostNetwork. The spec has no hostPID or hostIPC field,
// and no credential in the clear: every one is the key of a Secret.
// TestMonitoringWorkloadKinds_PodFieldsHeldOrListed and
// TestMonitoringWorkloadKinds_CredentialsHeldOrListed derive both claims from
// the type.
func alertmanagerWorkload(spec *monitoringv1.AlertmanagerSpec) monitoringWorkload {
	w := monitoringWorkload{
		pod: corev1.PodSpec{
			InitContainers:  spec.InitContainers,
			Containers:      spec.Containers,
			Volumes:         spec.Volumes,
			SecurityContext: spec.SecurityContext,
			HostNetwork:     spec.HostNetwork,
		},
		replicasPath: "replicas",
		storage:      spec.Storage,
		resources:    []fieldResources{{"resources", spec.Resources}},
	}
	if spec.Image != nil && *spec.Image != "" {
		w.images = []fieldValue{{"image", *spec.Image}}
	}
	if spec.Replicas != nil {
		replicas := int64(*spec.Replicas)
		w.replicas = &replicas
	}
	return w
}

// ToApplicationConfig decodes an OAM alertmanager component into its config.
// The object takes the namespace of the application it is generated in.
func (h *AlertmanagerHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return alertmanagerKind.config(component)
}
