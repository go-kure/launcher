package components

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PrometheusHandler handles OAM prometheus components: the kind-named
// projection of a monitoring.coreos.com/v1 Prometheus (go-kure/launcher#790).
//
// Its properties are the top-level fields of monitoringv1.PrometheusSpec,
// under their json names, decoded strictly (decodeKindSpec), less the three
// deprecated ones that name the image in parts. It emits the Prometheus, named
// after the component unless `objectName` names it, in the build namespace,
// and nothing else: the Prometheus operator runs the pods, from one
// StatefulSet per shard it builds. What the spec says of those pods, the
// Thanos sidecar's included, is held to the environment policy as a workload
// kind's own fields are (prometheusWorkload, monitoring_workload.go).
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type PrometheusHandler struct{}

// CanHandle returns true for the prometheus component type.
func (h *PrometheusHandler) CanHandle(componentType string) bool {
	return componentType == "prometheus"
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PrometheusHandler) ContractMetadata() oam.ContractMetadata {
	return contract("prometheus")
}

// ComponentObject declares the prometheus kind's Prometheus.
func (h *PrometheusHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.PrometheusesKind), oam.ObjectScopeNamespaced
}

// PropertySchema declares the top-level monitoringv1.PrometheusSpec fields by
// their json names, less baseImage, tag and sha, which the kind refuses
// (validatePrometheus). Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *PrometheusHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Prometheus spec."
	const decoded = " Decoded strictly into the Prometheus operator's API type: see "
	const core = " Decoded strictly into the Kubernetes API type: see "
	const duration = " A number and a unit: ms, s, m, h, d, w or y."
	const selector = " A Kubernetes label selector: matchLabels and matchExpressions."
	const unmanaged = " Where serviceMonitorSelector, podMonitorSelector, probeSelector and scrapeConfigSelector are all unset, the operator leaves the Prometheus configuration to its Secret, a behaviour deprecated upstream."
	const limit = " At least 0. Requires Prometheus v2.45.0 or later; applies to the scrape objects that set no limit of their own."
	const enforcedLimit = " At least 0. Overrides a larger or unset limit of a ServiceMonitor, PodMonitor or Probe object."
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
	const secretKey = " A Secret key selector: name and key, in the Prometheus's namespace. Launcher emits no Secret for it." //nolint:gosec // G101: a description, not a credential
	return map[string]oam.PropertySchema{
		"podMetadata":                     object("podMetadata: the labels and annotations the operator copies onto the Prometheus pods. A key the consumer reserves is refused here as on a workload's pod template. Nothing is added: the pods carry the component label only if it is written here with the component's own value, and without it the NetworkPolicies generated for the component do not select them. The operator sets seven labels and one annotation of its own: a value authored here replaces app.kubernetes.io/version and the annotation, but not the other six labels." + decoded + "EmbeddedObjectMetadata in its API reference."),
		"serviceMonitorSelector":          object("serviceMonitorSelector: the ServiceMonitor objects scraped, by their labels. Unset, none; an empty selector, all." + unmanaged + selector),
		"serviceMonitorNamespaceSelector": object("serviceMonitorNamespaceSelector: the namespaces ServiceMonitor objects are read from, by their labels. Unset, the Prometheus's own namespace only; an empty selector, all." + selector),
		"podMonitorSelector":              object("podMonitorSelector: the PodMonitor objects scraped, by their labels. Unset, none; an empty selector, all." + unmanaged + selector),
		"podMonitorNamespaceSelector":     object("podMonitorNamespaceSelector: the namespaces PodMonitor objects are read from, by their labels. Unset, the Prometheus's own namespace only; an empty selector, all." + selector),
		"probeSelector":                   object("probeSelector: the Probe objects scraped, by their labels. Unset, none; an empty selector, all." + unmanaged + selector),
		"probeNamespaceSelector":          object("probeNamespaceSelector: the namespaces Probe objects are read from, by their labels. Unset, the Prometheus's own namespace only; an empty selector, all." + selector),
		"scrapeConfigSelector":            object("scrapeConfigSelector: the ScrapeConfig objects scraped, by their labels. Unset, none; an empty selector, all. ScrapeConfig is an alpha API upstream." + unmanaged + selector),
		"scrapeConfigNamespaceSelector":   object("scrapeConfigNamespaceSelector: the namespaces ScrapeConfig objects are read from, by their labels. Unset, the Prometheus's own namespace only; an empty selector, all." + selector),
		"version":                         text("version: the Prometheus version the operator configures for. Unset, the latest the operator knows of."),
		"paused":                          flag("paused: true stops the operator from acting on the objects it manages for this Prometheus, deletion excepted."),
		"image":                           text("image: the full image reference of the prometheus container, with a tag other than latest or a digest. Held to the EnvironmentPolicy's allowed registries. Unset or empty, the image is the one an entry of containers named prometheus names, and where none does the operator chooses the one that runs: refused under a policy with allowed registries, which cannot hold that choice, and built under one without. version is still needed for the operator to know which Prometheus it configures."),
		"imagePullPolicy":                 text("imagePullPolicy: when the images of the prometheus, config-reloader, init-config-reloader and thanos-sidecar containers are pulled: Always, Never or IfNotPresent."),
		"imagePullSecrets": objects("imagePullSecrets: the Secrets of the Prometheus's namespace that hold the credentials the images are pulled with.",
			"One reference: name."),
		"replicas":                    number("replicas: the number of Prometheus pods of each shard. replicas times shards is held to the EnvironmentPolicy's replica maximum as the operator counts them: an unset or negative replicas as 1, an unset shards as 1. Nothing is written, and no replica default of the policy is applied."),
		"shards":                      number("shards: the number of shards the scraped targets are spread over, one StatefulSet each. Unset, the API fills 1. Below 1 is refused, since the operator runs 1 shard for it. Held with replicas: see replicas. Scaling the shards down or up moves no data."),
		"shardingStrategy":            object("shardingStrategy: how targets are spread over the shards: mode, Address (the default, by a hash of the target's address) or Topology (behind the operator's PrometheusTopologySharding feature gate), with topology." + decoded + "ShardingStrategy in its API reference."),
		"replicaExternalLabelName":    text("replicaExternalLabelName: the name of the external label that holds the replica's name. Unset, prometheus_replica; empty, no such label."),
		"prometheusExternalLabelName": text("prometheusExternalLabelName: the name of the external label that holds the Prometheus's name. Unset, prometheus; empty, no such label."),
		"logLevel":                    text("logLevel: the log level of Prometheus and its config-reloader: debug, info, warn or error."),
		"logFormat":                   text("logFormat: the log format of Prometheus and its config-reloader: logfmt or json."),
		"scrapeInterval":              text("scrapeInterval: the interval between two scrapes. Unset, the API fills 30s; an empty one is refused, since the API server would replace it." + duration),
		"scrapeTimeout":               text("scrapeTimeout: how long a scrape may take; not longer than scrapeInterval, or the operator refuses the object." + duration),
		"scrapeProtocols": texts("scrapeProtocols: the protocols offered in a scrape, most preferred first. Requires Prometheus v2.49.0 or later.",
			"One protocol: PrometheusProto, OpenMetricsText0.0.1, OpenMetricsText1.0.0, PrometheusText0.0.4 or PrometheusText1.0.0 (Prometheus v3.0.0 or later)."),
		"externalLabels":            object("externalLabels: the Prometheus external labels added to every series and alert sent to an external system (federation, remote storage, Alertmanager), a map of label names to values. They are not Kubernetes labels: the labels property sets the Prometheus object's own, and the reserved-key and component-label checks of that property do not apply here. The labels replicaExternalLabelName and prometheusExternalLabelName name take precedence."),
		"enableRemoteWriteReceiver": flag("enableRemoteWriteReceiver: true makes Prometheus accept series sent with the remote write protocol, a use upstream calls inefficient. Requires Prometheus v2.33.0 or later."),
		"enableOTLPReceiver":        flag("enableOTLPReceiver: true makes Prometheus accept metrics sent with the OTLP protocol; otlp turns it on too. Requires Prometheus v2.47.0 or later."),
		"remoteWriteReceiverMessageVersions": texts("remoteWriteReceiverMessageVersions: the protobuf message versions accepted from remote writers; at least one. Requires Prometheus v2.54.0 or later.",
			"One version: V1.0 or V2.0."),
		"enableFeatures": texts("enableFeatures: the Prometheus feature flags to enable, which upstream does not support.",
			"The name of one feature flag."),
		"externalUrl": text("externalUrl: the URL under which Prometheus is reached from outside, which the URLs it generates are built from."),
		"routePrefix": text("routePrefix: the path prefix Prometheus registers its HTTP handlers under."),
		"storage":     object("storage: where the Prometheus pods keep their data: emptyDir, ephemeral or volumeClaimTemplate, in that order of precedence. Unset, the storage is the operator's to decide: no storage default of the policy is applied. The storage a claim template requests is held to the EnvironmentPolicy's storage maximum; the size limit of an emptyDir is not. A claim template's labels and annotations are not read for reserved keys and take no component label, as a statefulset's are not." + decoded + "StorageSpec in its API reference."),
		"volumes": objects("volumes: further volumes of the Prometheus pods, beside the ones the operator generates. Two entries of one name are refused. The web TLS credentials' volumes, whose names the operator hashes, are left to the API. Held to the EnvironmentPolicy as a pod's volumes are: hostPath, the storage a generic ephemeral volume's claim requests, the registry of an image volume.",
			"One volume."+core+"Volume in the Kubernetes API reference."),
		"volumeMounts": objects("volumeMounts: further volume mounts of the prometheus container. The mounts of the web TLS credentials are left to the API.",
			"One volume mount."+core+"VolumeMount in the Kubernetes API reference."),
		"persistentVolumeClaimRetentionPolicy": object("persistentVolumeClaimRetentionPolicy: whether the claims of the StatefulSets are deleted when they are deleted (whenDeleted) or scaled down (whenScaled): Retain, the default, or Delete." + core + "StatefulSetPersistentVolumeClaimRetentionPolicy in the Kubernetes API reference."),
		"web":                                  object("web: the web server's settings: tlsConfig, httpConfig, pageTitle and maxConnections." + decoded + "PrometheusWebSpec in its API reference."),
		"resources":                            object("resources: the resource requests and limits of the prometheus container. Its cpu and memory are held to the EnvironmentPolicy's maxima, and a request may not exceed its limit. No resource default of the policy is applied." + core + "ResourceRequirements in the Kubernetes API reference."),
		"nodeSelector":                         object("nodeSelector: the node labels a node must carry for the pods to be scheduled on it."),
		"schedulerName":                        text("schedulerName: the scheduler that places the pods. Unset, the default scheduler. Not empty."),
		"serviceAccountName":                   text("serviceAccountName: the ServiceAccount the pods run as. Launcher creates none for it, and no Role, and does not check that it exists; Kubernetes service discovery needs one that may read the targets."),
		"automountServiceAccountToken":         flag("automountServiceAccountToken: whether a service account token is mounted into the pods. Unset, the operator mounts one; Kubernetes service discovery needs it."),
		"secrets": texts("secrets: the Secrets of the Prometheus's namespace mounted into the prometheus container, each under /etc/prometheus/secrets/<name>. An entry whose volume name, secret-<name> cut to 63 characters, ends in - is refused: the operator fails to build the pods.",
			"The name of a Secret."),
		"configMaps": texts("configMaps: the ConfigMaps of the Prometheus's namespace mounted into the prometheus container, each under /etc/prometheus/configmaps/<name>. An entry whose volume name, configmap-<name> cut to 63 characters, ends in - is refused: the operator fails to build the pods.",
			"The name of a ConfigMap."),
		"affinity": object("affinity: the scheduling constraints of the pods." + core + "Affinity in the Kubernetes API reference."),
		"tolerations": objects("tolerations: the taints the pods tolerate.",
			"One toleration."+core+"Toleration in the Kubernetes API reference."),
		"topologySpreadConstraints": objects("topologySpreadConstraints: how the pods are spread over topology domains.",
			"One constraint, and additionalLabelSelectors."+decoded+"TopologySpreadConstraint in its API reference."),
		"remoteWrite": objects("remoteWrite: the remote write endpoints Prometheus sends its series to. The deprecated bearerToken of an entry is refused under an EnvironmentPolicy that forbids explicit secrets; every other credential of an entry is the key of a Secret or the path of a file in the container. Launcher does not read a credential written into a header or a URL.",
			"One endpoint: url, required. An empty action of a writeRelabelConfigs rule is refused, since the API server would replace it with replace."+decoded+"RemoteWriteSpec in its API reference."),
		"otlp":                object("otlp: the settings of the OTLP receiver, which they turn on. Requires Prometheus v2.55.0 or later." + decoded + "OTLPConfig in its API reference."),
		"securityContext":     object("securityContext: the pod-level security attributes of the pods. Its windowsOptions.hostProcess is refused under an EnvironmentPolicy that does not allow privileged containers." + core + "PodSecurityContext in the Kubernetes API reference."),
		"dnsPolicy":           text("dnsPolicy: the DNS policy of the pods: ClusterFirstWithHostNet, ClusterFirst, Default or None. With hostNetwork and no dnsPolicy, ClusterFirstWithHostNet."),
		"dnsConfig":           object("dnsConfig: the DNS configuration of the pods: nameservers, searches and options." + decoded + "PodDNSConfig in its API reference."),
		"listenLocal":         flag("listenLocal: true makes Prometheus listen on loopback only, not on the pod's address."),
		"podManagementPolicy": text("podManagementPolicy: how the StatefulSets create and delete pods when they scale: Parallel, the operator's default, or OrderedReady. Changing it recreates the StatefulSets."),
		"updateStrategy":      object("updateStrategy: how the StatefulSets replace their pods on a change: type (RollingUpdate, the default, or OnDelete) and rollingUpdate with maxUnavailable. The API refuses rollingUpdate with another type than RollingUpdate; launcher does not check that rule." + decoded + "StatefulSetUpdateStrategy in its API reference."),
		"enableServiceLinks":  flag("enableServiceLinks: whether the Services of the namespace are injected into the pods' environment variables."),
		"containers": objects("containers: further containers of the pods, and patches of the ones the operator generates: an entry that shares its name with a container the operator generates (prometheus, config-reloader, and thanos-sidecar where thanos is set) is merged into it. Each is held to the EnvironmentPolicy as a pod's containers are: the registry of an authored image, cpu and memory maxima, privilege and capabilities. A patch may name no image; any other entry must name one. A name may be listed once: the operator runs only the last entry of a name. A name of an init container, generated (init-config-reloader) or listed, is refused. The operator merges a patch's ports into those it gives that container (prometheus: the web port under portName at 9090/TCP; config-reloader: reloader-web at 8080/TCP; thanos-sidecar: http at 10902 and grpc at 10901) by number: in the patch's order, a port is merged into the first port of its number, the operator's or one the patch added before it, and takes the name and protocol it names; a port of another number is added. Where the operator gives the container no port (prometheus under listenLocal, and config-reloader under listenLocal with the HTTP reloadStrategy), the patch's ports are taken as listed. A container whose ports, merged so or as an added entry lists them, name two ports alike is refused, since the API refuses it. Under a policy with allowed registries, config-reloader must be patched with an image from one of them: unpatched, it runs the image of the operator's own configuration, which the allowlist cannot hold.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"initContainers": objects("initContainers: further init containers of the pods, and patches of the one the operator generates: an entry named init-config-reloader is merged into it. Held to the EnvironmentPolicy as containers are, a name listed once as there and never a container's (prometheus, config-reloader, thanos-sidecar where thanos is set, or a listed one), and its ports as there (a patch's merged into reloader-init at 8081/TCP). A patch may name no image; any other entry must name one. Under a policy with allowed registries, init-config-reloader must be patched with an image from one of them, as config-reloader is.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"additionalScrapeConfigs":     object("additionalScrapeConfigs: the Secret key that holds further scrape configurations, appended to the ones the operator generates as they are." + secretKey),
		"apiserverConfig":             object("apiserverConfig: the Kubernetes API server Prometheus discovers targets from, and how it authenticates. Unset, the cluster Prometheus runs in, with the pod's service account. The deprecated bearerToken is refused under an EnvironmentPolicy that forbids explicit secrets; every other credential is the key of a Secret or the path of a file in the container." + decoded + "APIServerConfig in its API reference."),
		"priorityClassName":           text("priorityClassName: the priority class of the pods."),
		"portName":                    text("portName: the name of the web port on the pods and the governing Service. Unset, the API fills web; an empty one is refused, since the API server would replace it."),
		"arbitraryFSAccessThroughSMs": object("arbitraryFSAccessThroughSMs: deny, true to forbid ServiceMonitor, PodMonitor and Probe objects to name files of the prometheus container, such as its service account token." + decoded + "ArbitraryFSAccessThroughSMsConfig in its API reference."),
		"overrideHonorLabels":         flag("overrideHonorLabels: true renames the scraped labels that conflict with the target's to exported_<name>, for every ServiceMonitor, PodMonitor and ScrapeConfig object, whatever its honorLabels."),
		"overrideHonorTimestamps":     flag("overrideHonorTimestamps: true ignores the scraped timestamps for every ServiceMonitor and PodMonitor object, whatever its honorTimestamps."),
		"ignoreNamespaceSelectors":    flag("ignoreNamespaceSelectors: true ignores the namespaceSelector of every PodMonitor, ServiceMonitor and Probe object: each discovers targets in its own namespace only."),
		"enforcedNamespaceLabel":      text("enforcedNamespaceLabel: the label that every series, alert and rule selector of a selected object gets, with the namespace of that object as its value."),
		"enforcedSampleLimit":         number("enforcedSampleLimit: the most samples a scrape may yield." + enforcedLimit),
		"enforcedTargetLimit":         number("enforcedTargetLimit: the most targets that are scraped." + enforcedLimit),
		"enforcedLabelLimit":          number("enforcedLabelLimit: the most labels a sample may carry. Requires Prometheus v2.27.0 or later." + enforcedLimit),
		"enforcedLabelNameLengthLimit": number("enforcedLabelNameLengthLimit: the longest label name of a sample. Requires Prometheus v2.27.0 or later." +
			enforcedLimit),
		"enforcedLabelValueLengthLimit": number("enforcedLabelValueLengthLimit: the longest label value of a sample. Requires Prometheus v2.27.0 or later." +
			enforcedLimit),
		"enforcedKeepDroppedTargets": number("enforcedKeepDroppedTargets: the most targets dropped by relabeling that are kept in memory. Requires Prometheus v2.47.0 or later." +
			enforcedLimit),
		"enforcedBodySizeLimit":          text("enforcedBodySizeLimit: the largest uncompressed response body a scrape accepts, as a number and a unit (B, KB, MB, GB, TB, PB, EB, or KiB to EiB). Requires Prometheus v2.28.0 or later. Overrides a larger or unset limit of a scrape object."),
		"nameValidationScheme":           text("nameValidationScheme: how metric and label names are validated: UTF8 or Legacy. Requires Prometheus v2.55.0 or later."),
		"nameEscapingScheme":             text("nameEscapingScheme: how names outside the legacy character set are escaped in a scrape request: AllowUTF8, Underscores, Dots or Values. Requires Prometheus v3.4.0 or later."),
		"convertClassicHistogramsToNHCB": flag("convertClassicHistogramsToNHCB: true converts every scraped classic histogram into a native histogram with custom buckets. Requires Prometheus v3.4.0 or later."),
		"scrapeNativeHistograms":         flag("scrapeNativeHistograms: whether native histograms are scraped. Requires Prometheus v3.8.0 or later."),
		"scrapeClassicHistograms":        flag("scrapeClassicHistograms: whether a classic histogram that is also exposed as a native one is scraped too. Requires Prometheus v3.5.0 or later."),
		"minReadySeconds":                number("minReadySeconds: how many seconds a new pod must be ready before it counts as available. At least 0."),
		"hostAliases": objects("hostAliases: further entries of the pods' hosts file.",
			"One entry: ip and hostnames, both required."),
		"additionalArgs": objects("additionalArgs: further command-line arguments of the prometheus container, passed as they are; launcher does not read them. Upstream documents that an argument the operator already sets, or an invalid one, fails the reconciliation.",
			"One argument: name (required) and value."),
		"walCompression": flag("walCompression: whether the write-ahead log is compressed with Snappy; Prometheus v2.20.0 and later compress it unless told not to. Requires Prometheus v2.11.0 or later."),
		"excludedFromEnforcement": objects("excludedFromEnforcement: the objects whose series and rules do not get enforcedNamespaceLabel. An entry that leaves group out is written with monitoring.coreos.com, the one group the API admits; an authored empty group is refused.",
			"One reference: group, resource, namespace and name."+decoded+"ObjectReference in its API reference."),
		"hostNetwork": flag("hostNetwork: true runs the pods in the node's network namespace. Refused under an EnvironmentPolicy that does not allow the host network."),
		"podTargetLabels": texts("podTargetLabels: the pod labels appended to the podTargetLabels of every PodMonitor and ServiceMonitor object.",
			"The name of one label."),
		"tracingConfig":                 object("tracingConfig: how Prometheus sends traces: endpoint, required, with clientType, samplingFraction, insecure, headers, compression, timeout and tlsConfig. Experimental upstream. Launcher does not read a credential written into a header." + decoded + "TracingConfig in its API reference."),
		"bodySizeLimit":                 text("bodySizeLimit: the largest uncompressed response body a scrape accepts, as a number and a unit (B, KB, MB, GB, TB, PB, EB, or KiB to EiB). Requires Prometheus v2.45.0 or later; applies to the scrape objects that set no limit of their own."),
		"sampleLimit":                   number("sampleLimit: the most samples a scrape may yield." + limit),
		"targetLimit":                   number("targetLimit: the most targets that are scraped." + limit),
		"labelLimit":                    number("labelLimit: the most labels a sample may carry." + limit),
		"labelNameLengthLimit":          number("labelNameLengthLimit: the longest label name of a sample." + limit),
		"labelValueLengthLimit":         number("labelValueLengthLimit: the longest label value of a sample." + limit),
		"keepDroppedTargets":            number("keepDroppedTargets: the most targets dropped by relabeling that are kept in memory; 0, no limit. At least 0. Requires Prometheus v2.47.0 or later; applies to the scrape objects that set no limit of their own."),
		"reloadStrategy":                text("reloadStrategy: how the configuration is reloaded: HTTP, by the /-/reload endpoint, the default, or ProcessSignal, by a SIGHUP."),
		"maximumStartupDurationSeconds": number("maximumStartupDurationSeconds: how many seconds the startup probe of the prometheus container waits for the write-ahead log to be replayed. At least 60. Unset, 900."),
		"scrapeClasses": objects("scrapeClasses: the scrape classes ServiceMonitor, PodMonitor, Probe and ScrapeConfig objects may name. Experimental upstream.",
			"One scrape class: name, required. An empty action of a relabelings or metricRelabelings rule is refused, since the API server would replace it with replace."+decoded+"ScrapeClass in its API reference."),
		"serviceDiscoveryRole":          text("serviceDiscoveryRole: the role ServiceMonitor targets and Alertmanager endpoints are discovered with: Endpoints, the operator's default, or EndpointSlice."),
		"tsdb":                          object("tsdb: the settings of the time series database that are reloaded at runtime: outOfOrderTimeWindow, staleSeriesCompactionThreshold and chunkEncoding. Requires Prometheus v2.39.0 or later." + decoded + "TSDBSpec in its API reference."),
		"scrapeFailureLogFile":          text("scrapeFailureLogFile: the file scrape failures are logged to. A name alone is a file of an emptyDir the operator mounts at /var/log/prometheus; a full path needs a writable volume mounted there. Requires Prometheus v2.55.0 or later. Not empty."),
		"serviceName":                   text("serviceName: the name of the governing Service of the StatefulSets, which must exist in the namespace and select the pods. Unset, the operator creates and manages a headless Service named prometheus-operated. Not empty, and a DNS-1035 label (starting with a letter), the rule of every Service name here: the API refuses a Service of another name before Kubernetes 1.36 (by default), and the operator fails to reconcile where it finds no Service of the name."),
		"runtime":                       object("runtime: the settings of the Prometheus process: goGC." + decoded + "RuntimeConfig in its API reference."),
		"terminationGracePeriodSeconds": number("terminationGracePeriodSeconds: how many seconds the pods are given to stop; 0 kills them at once, which may corrupt data. Unset, 600. At least 0."),
		"hostUsers":                     flag("hostUsers: false runs the pods in a user namespace of their own, not the host's."),
		"retention":                     text("retention: how long Prometheus keeps its data. Unset, with retentionSize and retentionPercentage unset too, 24h." + duration),
		"retentionSize":                 text("retentionSize: the most bytes Prometheus keeps of its data, as a number and a unit (B, KB, MB, GB, TB, PB, EB, or KiB to EiB)."),
		"retentionPercentage": {
			Types:       []oam.PropertyType{oam.PropertyTypeNumber, oam.PropertyTypeString},
			Description: spec + "retentionPercentage: the most of the data volume's capacity Prometheus keeps of its data, from 0 to 100; 0 turns it off. A Kubernetes quantity, as a number or a string (80, \"80\"). Requires Prometheus v3.11.0 or later.",
		},
		"shardRetentionPolicy": object("shardRetentionPolicy: whether the pods of a shard removed by scaling down are deleted or kept (whenScaled: Delete or Retain), and for how long (retain). Behind a feature gate of the operator upstream." + decoded + "ShardRetentionPolicy in its API reference."),
		"disableCompaction":    flag("disableCompaction: true turns block compaction off. With thanos.objectStorageConfig or objectStorageConfigFile and older versions of Prometheus or the sidecar, the operator turns it off by itself."),
		"rules":                object("rules: the settings of the rule engine: alert, with forOutageTolerance, forGracePeriod and resendDelay." + decoded + "Rules in its API reference."),
		"prometheusRulesExcludedFromEnforce": objects("prometheusRulesExcludedFromEnforce: the PrometheusRule objects whose rules do not get enforcedNamespaceLabel. Deprecated upstream for excludedFromEnforcement.",
			"One reference: ruleNamespace and ruleName, both required."),
		"ruleSelector":                  object("ruleSelector: the PrometheusRule objects evaluated, by their labels. Unset, none; an empty selector, all." + selector),
		"ruleNamespaceSelector":         object("ruleNamespaceSelector: the namespaces PrometheusRule objects are read from, by their labels. Unset, the Prometheus's own namespace only; an empty selector, all." + selector),
		"query":                         object("query: the settings of the query engine: lookbackDelta, maxConcurrency, maxSamples and timeout." + decoded + "QuerySpec in its API reference."),
		"alerting":                      object("alerting: the Alertmanager endpoints alerts are sent to, under alertmanagers. Every credential of an endpoint is the key of a Secret or the path of a file in the container. An empty action of an endpoint's relabelings or alertRelabelings rule is refused, since the API server would replace it with replace." + decoded + "AlertingSpec in its API reference."),
		"additionalAlertRelabelConfigs": object("additionalAlertRelabelConfigs: the Secret key that holds further alert relabeling configurations, appended to the ones the operator generates as they are." + secretKey),
		"additionalAlertManagerConfigs": object("additionalAlertManagerConfigs: the Secret key that holds further Alertmanager configurations, appended to the ones the operator generates as they are." + secretKey),
		"remoteRead": objects("remoteRead: the remote read endpoints Prometheus reads series from. The deprecated bearerToken of an entry is refused under an EnvironmentPolicy that forbids explicit secrets; every other credential of an entry is the key of a Secret or the path of a file in the container. Launcher does not read a credential written into a header or a URL.",
			"One endpoint: url, required."+decoded+"RemoteReadSpec in its API reference."),
		"thanos":                 object("thanos: the Thanos sidecar the operator adds to the pods. Its image is held to the EnvironmentPolicy's allowed registries, with a tag other than latest or a digest, and its resources to the cpu and memory maxima, as the prometheus container's are; its deprecated baseImage, tag and sha are refused whenever set, the empty string included; a null one sets none. Unset or empty image, the sidecar's image is the one an entry of containers named thanos-sidecar names, and where none does the operator chooses it: refused under a policy with allowed registries, built under one without. Its objectStorageConfig and tracingConfig are keys of a Secret. An entry of its volumeMounts is refused at /prometheus where object storage is configured, and at /etc/thanos/config, where the operator mounts its own from Thanos v0.24.0, whatever version names; the operator copies only an entry's name and mountPath, so any other field of one is refused. An empty blockSize is refused, since the API server would replace it with 2h." + decoded + "ThanosSpec in its API reference."),
		"queryLogFile":           text("queryLogFile: the file PromQL queries are logged to. A name alone is a file of an emptyDir the operator mounts at /var/log/prometheus, which it does not mount beside a scrapeFailureLogFile with a directory: refused there unless a volume is mounted at /var/log/prometheus without readOnly. A full path needs a writable volume mounted there, or a standard stream such as /dev/stdout."),
		"allowOverlappingBlocks": flag("allowOverlappingBlocks: true turns vertical compaction on. Deprecated upstream: no effect from Prometheus v2.39.0, where it is on."),
		"exemplars":              object("exemplars: the exemplar storage: maxSize. Needs the exemplar-storage feature flag." + decoded + "Exemplars in its API reference."),
		"evaluationInterval":     text("evaluationInterval: the interval between two evaluations of the rules. Unset, the API fills 30s; an empty one is refused, since the API server would replace it." + duration),
		"ruleQueryOffset":        text("ruleQueryOffset: how far into the past the evaluation timestamp of a rule group is moved. Requires Prometheus v2.53.0 or later." + duration),
		"enableAdminAPI":         flag("enableAdminAPI: true turns on the admin API, whose endpoints delete data and shut Prometheus down. Launcher adds no authentication in front of it."),
	}
}

// prometheusKind is the prometheus kind: see policyHeldKind. The API requires
// no top-level field of the spec, and of what is authored below it the fields
// prometheusRequired lists; the type would write each one empty. The API's
// value rules are left to it, the expression rules the spec reaches included
// (prometheusRulesLeft): the linked module ships no CRD to hold either to.
//
// The fields on which an authored 0 or "" cannot be carried are the ones of a
// pod spec's containers and volumes (podSpecDefaultedZeros): the spec lists
// containers, init containers and volumes of the Kubernetes types, and copies
// hostNetwork to the pods, so a listed container port's hostPort is among them
// when it is true. Strings of the operator's own types cannot be carried empty
// either (prometheusDefaultedZeroFields).
// TestMonitoringWorkloadKinds_DefaultedZeros derives the list
// (monitoringWorkloadDefaultedZeros).
var prometheusKind = &policyHeldKind[monitoringv1.PrometheusSpec]{
	policyFreeKind: policyFreeKind[monitoringv1.PrometheusSpec]{
		upstream: "monitoring.coreos.com/v1 PrometheusSpec",
		required: prometheusRequired,
		defaultedZerosFor: func(spec *monitoringv1.PrometheusSpec) defaultedZeroFields {
			return monitoringWorkloadDefaultedZeros(&corev1.PodSpec{HostNetwork: spec.HostNetwork}, prometheusDefaultedZeroFields)
		},
		validate:     validatePrometheus,
		validateName: validatePrometheusName,
		build: func(name, namespace string, spec *monitoringv1.PrometheusSpec) client.Object {
			p := prometheus.CreatePrometheus(name, namespace)
			spec.DeepCopyInto(&p.Spec)
			withExcludedGroups(p.Spec.ExcludedFromEnforcement)
			return p
		},
	},
	enforce: func(spec *monitoringv1.PrometheusSpec, p oam.Policy) error {
		return enforceMonitoringWorkloadPolicy(prometheusWorkload(spec), p)
	},
}

// prometheusDefaultedZeroFields are the fields of the operator's own types in
// the prometheus kind's defaulted-zero list (monitoringWorkloadDefaultedZeros):
// four to which the CRD gives another default, the action of a relabeling rule
// in the five lists of rules the spec holds (monitoringDefaultedZeros), and two
// the operator copies to a pod field the API server defaults
// (pkg/prometheus/server/statefulset.go at prometheus-operator v0.94.1):
// schedulerName to the pod's (:399), and imagePullPolicy to its prometheus
// container's (:338), the Thanos sidecar's (:620) and both config reloaders'
// (pkg/prometheus/common.go:380, pkg/operator/config_reloader.go:360).
var prometheusDefaultedZeroFields = func() map[string]string {
	fields := monitoringDefaultedZeros([]string{
		"remoteWrite[].writeRelabelConfigs",
		"alerting.alertmanagers[].relabelings",
		"alerting.alertmanagers[].alertRelabelings",
		"scrapeClasses[].relabelings",
		"scrapeClasses[].metricRelabelings",
	}, map[string]string{
		"portName":           `"web"`,
		"scrapeInterval":     `"30s"`,
		"evaluationInterval": `"30s"`,
		"thanos.blockSize":   `"2h"`,
	}).fields
	fields["schedulerName"] = `"default-scheduler"`
	fields["imagePullPolicy"] = imagePullPolicyDefault
	return fields
}()

// prometheusRequired lists the fields the API requires below an authored
// parent that the type would write unauthored, the key and the operator of
// each label selector's match expression included: those of the pod spec's
// fields, of the storage's claim templates and of the ten selectors of the
// objects Prometheus reads, the ServiceMonitors, PodMonitors, Probes,
// ScrapeConfigs and PrometheusRules and their namespaces
// (labelSelectorRequired).
// TestMonitoringKinds_RequiredMatchMarkers derives it from the markers of the
// linked module's source.
var prometheusRequired = requiredFields(map[string]string{ //nolint:gosec // G101: the keys are field paths (clientSecret is a field's name) and the values their descriptions
	"additionalArgs[].name":                              "the name of the command-line argument",
	"alerting.alertmanagers":                             "the Alertmanager endpoints alerts are sent to",
	"alerting.alertmanagers[].name":                      "the name of the Alertmanager endpoint's Service",
	"alerting.alertmanagers[].port":                      "the port of the Alertmanager endpoint's Service, by name or number",
	"apiserverConfig.host":                               "the address of the Kubernetes API server",
	"dnsConfig.options[].name":                           "the name of the DNS resolver option",
	"excludedFromEnforcement[].namespace":                "the namespace of the excluded object",
	"excludedFromEnforcement[].resource":                 "the resource of the excluded object: prometheusrules, servicemonitors, podmonitors or probes",
	"hostAliases[].ip":                                   "the IP address of the hosts-file entry",
	"hostAliases[].hostnames":                            "the host names of the hosts-file entry",
	"prometheusRulesExcludedFromEnforce[].ruleNamespace": "the namespace of the excluded PrometheusRule",
	"prometheusRulesExcludedFromEnforce[].ruleName":      "the name of the excluded PrometheusRule",
	"remoteRead[].url":                                   "the URL of the remote read endpoint",
	"remoteRead[].oauth2.clientId":                       "the Secret or ConfigMap key that holds the OAuth2 client id",
	"remoteRead[].oauth2.clientSecret":                   "the Secret key that holds the OAuth2 client secret",
	"remoteRead[].oauth2.tokenUrl":                       "the URL tokens are fetched from",
	"remoteWrite[].url":                                  "the URL of the remote write endpoint",
	"remoteWrite[].azureAd.oauth.clientId":               "the client id of the Azure OAuth application",
	"remoteWrite[].azureAd.oauth.clientSecret":           "the Secret key that holds the Azure OAuth client secret",
	"remoteWrite[].azureAd.oauth.tenantId":               "the tenant id of the Azure OAuth application",
	"remoteWrite[].azureAd.workloadIdentity.clientId":    "the client id of the Azure workload identity",
	"remoteWrite[].azureAd.workloadIdentity.tenantId":    "the tenant id of the Azure workload identity",
	"remoteWrite[].oauth2.clientId":                      "the Secret or ConfigMap key that holds the OAuth2 client id",
	"remoteWrite[].oauth2.clientSecret":                  "the Secret key that holds the OAuth2 client secret",
	"remoteWrite[].oauth2.tokenUrl":                      "the URL tokens are fetched from",
	"scrapeClasses[].name":                               "the name of the scrape class",
	"shardRetentionPolicy.retain.retentionPeriod":        "how long the pods of a removed shard are kept",
	"thanos.additionalArgs[].name":                       "the name of the sidecar's command-line argument",
	"tracingConfig.endpoint":                             "the endpoint traces are sent to, as host:port",
	"updateStrategy.type":                                "RollingUpdate or OnDelete",
}, labelSelectorRequired(append(podSpecLabelSelectors(""),
	"storage.volumeClaimTemplate.spec.selector",
	"storage.ephemeral.volumeClaimTemplate.spec.selector",
	"serviceMonitorSelector",
	"serviceMonitorNamespaceSelector",
	"podMonitorSelector",
	"podMonitorNamespaceSelector",
	"probeSelector",
	"probeNamespaceSelector",
	"scrapeConfigSelector",
	"scrapeConfigNamespaceSelector",
	"ruleSelector",
	"ruleNamespaceSelector",
)...))

// prometheusRulesLeft lists, by the property that reaches it, each expression
// rule the API states on a type the spec reaches. None is checked here: the
// linked module ships no CRD, so there is no rule text for the validator
// harness to evaluate. TestMonitoringWorkloadKinds_RulesListed derives the
// list from the markers of the module's source.
var prometheusRulesLeft = map[string]string{
	"":                               "!has(self.shardingStrategy) || !has(self.shardingStrategy.mode) || self.shardingStrategy.mode != 'Topology' || !has(self.shardingStrategy.topology) || !has(self.shardingStrategy.topology.values) || self.shardingStrategy.topology.values.size() == 0 || (has(self.shards) ? self.shards : 1) >= self.shardingStrategy.topology.values.size() (shards must be greater than or equal to the number of topology values when sharding strategy mode is Topology)",
	"alerting.alertmanagers[].sigv4": "!has(self.externalId) || has(self.roleArn) (externalId can only be used when roleArn is specified)",
	"remoteWrite[].sigv4":            "!has(self.externalId) || has(self.roleArn) (externalId can only be used when roleArn is specified)",
	"shardingStrategy":               "!has(self.topology) || (has(self.mode) && self.mode == 'Topology') (topology can only be defined when mode is set to 'Topology')",
	"updateStrategy":                 "!(self.type != 'RollingUpdate' && has(self.rollingUpdate)) (rollingUpdate requires type to be RollingUpdate)",
}

// validatePrometheus refuses, with or without an environment policy, the
// deprecated fields that name an image in parts, the spec's and the Thanos
// sidecar's, and what validateMonitoringWorkload refuses of the workload.
//
// baseImage, tag and sha are refused wherever the object would carry them,
// as the alertmanager kind refuses them (validateAlertmanager): the operator
// composes the image from them, and from version, in code that is not in the
// linked module, so the kind cannot say which image runs. The spec's three
// are strings the type leaves out when empty: an empty one is the object an
// absent one is. The sidecar's three are pointers, written whenever set, so
// an authored one is refused even empty; a null one sets none and builds.
// thanos.version is not one of them: it names the version the operator
// configures for, as the spec's version does.
//
// An externalUrl Prometheus exits on at startup is refused too
// (refusePrometheusExternalURL).
func validatePrometheus(spec *monitoringv1.PrometheusSpec) error {
	type part struct {
		path, use string
		value     *string
	}
	refused := func(field part) error {
		return errors.Errorf("%s: not authorable: the Prometheus operator deprecates the field, and composes the image it yields outside what the object states; use %s", field.path, field.use)
	}
	for _, field := range []part{{"baseImage", "image", &spec.BaseImage}, {"sha", "image", &spec.SHA}, {"tag", "image", &spec.Tag}} {
		if *field.value != "" {
			return refused(field)
		}
	}
	if t := spec.Thanos; t != nil {
		for _, field := range []part{{"thanos.baseImage", "thanos.image", t.BaseImage}, {"thanos.sha", "thanos.image", t.SHA}, {"thanos.tag", "thanos.image", t.Tag}} {
			if field.value != nil {
				return refused(field)
			}
		}
	}
	// The operator runs 1 replica for a negative count (ReplicasNumberPtr,
	// pkg/prometheus/common.go:131-143 at prometheus-operator v0.94.1).
	if err := refuseNegativeReplicas(spec.Replicas, "the Prometheus operator runs 1 replica for it; write 1"); err != nil {
		return err
	}
	if s := spec.Shards; s != nil && *s < 1 {
		return errors.Errorf("shards: %d is below 1: the Prometheus operator runs 1 shard for it; write 1", *s)
	}
	if err := validatePrometheusPortName(spec); err != nil {
		return err
	}
	if err := validateOperatorStorage(spec.Storage); err != nil {
		return err
	}
	if err := refusePrometheusExternalURL(spec.ExternalURL); err != nil {
		return err
	}
	if err := validatePrometheusQueryLogFile(spec); err != nil {
		return err
	}
	if err := validateThanosSidecarMounts(spec.Thanos); err != nil {
		return err
	}
	return validateMonitoringWorkload(prometheusWorkload(spec))
}

// refusePrometheusExternalURL refuses a nonempty externalUrl Prometheus exits
// on at startup, though the operator and the API accept it: the operator
// passes it unchanged as --web.external-url (pkg/prometheus/promcfg.go:1346-1348
// at prometheus-operator v0.94.1), and Prometheus exits with status 2 where
// computeExternalURL fails (cmd/prometheus/main.go:720-723 at v3.14.0): on a
// value that begins or ends with a quote (startsOrEndsWithQuote, :1746-1749),
// and on one Go's net/url cannot parse (refuseUnservableExternalURL).
// Prometheus checks no scheme (computeExternalURL, :1762-1792), so none is
// held. No message names the value.
func refusePrometheusExternalURL(value string) error {
	if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") || strings.HasSuffix(value, `"`) || strings.HasSuffix(value, "'") {
		return errors.New("externalUrl: begins or ends with a quote: the Prometheus operator passes it to Prometheus, which then exits at startup; name the URL without quotes, or leave it unset")
	}
	return refuseUnservableExternalURL("Prometheus", value, nil)
}

// validatePrometheusQueryLogFile refuses a queryLogFile named without a
// directory where the operator adds no volume for it. The operator writes such
// a file under /var/log/prometheus (logFilePath, pkg/prometheus/common.go:
// 230-236, appendQueryLogFile, pkg/prometheus/promcfg.go:3218-3224 at
// prometheus-operator v0.94.1) and adds the log-file volume there for it only
// where scrapeFailureLogFile is unset (appendServerVolumes,
// pkg/prometheus/server/statefulset.go:510 and :537); where
// scrapeFailureLogFile is set, it adds the volume only for a
// scrapeFailureLogFile named without a directory (BuildCommonVolumes,
// common.go:330-345). Without that volume Prometheus writes the file to the
// prometheus container's root filesystem, which the operator makes read-only
// (server/statefulset.go:349). A writable mount of the author's at
// /var/log/prometheus, in volumeMounts or a listed patch of the prometheus
// container, gives the file a volume, and is not refused. A read-only one
// leaves Prometheus unable to open the file. The path is compared cleaned, so
// /var/log/prometheus/ is the same directory, and any read-only mount there
// refuses: where the patch names the path as volumeMounts does, the
// operator's strategic merge (MergePatchContainers, pkg/k8s/merge.go:55)
// keeps readOnly where either sets it, since a false is not serialized; where
// the two spell it differently, both mounts stay, and which the container
// runtime mounts last, over the other, is not derived here.
func validatePrometheusQueryLogFile(spec *monitoringv1.PrometheusSpec) error {
	file := spec.QueryLogFile
	if file == "" || filepath.Dir(file) != "." || usesLogFileVolume(spec) {
		return nil
	}
	mounts := spec.VolumeMounts
	for _, c := range spec.Containers {
		if c.Name == "prometheus" {
			mounts = append(slices.Clone(mounts), c.VolumeMounts...)
		}
	}
	mounted, readOnly := false, false
	for _, m := range mounts {
		if path.Clean(m.MountPath) == prometheusLogDirectory {
			mounted, readOnly = true, readOnly || m.ReadOnly
		}
	}
	if mounted && !readOnly {
		return nil
	}
	return errors.Errorf("queryLogFile: %q names no directory, so the Prometheus operator configures it under %s, but beside scrapeFailureLogFile %q, which names one, it mounts no volume there, and the prometheus container's root filesystem is read-only; name the file with scrapeFailureLogFile's directory, name scrapeFailureLogFile without one too, or mount a writable volume at %s", file, prometheusLogDirectory, *spec.ScrapeFailureLogFile, prometheusLogDirectory)
}

// thanosDroppedMountFields are the fields of a thanos.volumeMounts entry the
// operator drops, each by its JSON name, with whether vm sets it.
// TestThanosDroppedMountFields holds the list to every field of the type but
// name and mountPath.
func thanosDroppedMountFields(vm corev1.VolumeMount) []struct {
	name string
	set  bool
} {
	return []struct {
		name string
		set  bool
	}{
		{"readOnly", vm.ReadOnly},
		{"recursiveReadOnly", vm.RecursiveReadOnly != nil},
		{"subPath", vm.SubPath != ""},
		{"mountPropagation", vm.MountPropagation != nil},
		{"subPathExpr", vm.SubPathExpr != ""},
		{"bindMountOptions", len(vm.BindMountOptions) > 0},
	}
}

// prometheusLogDirectory is where the operator writes a log file named without
// a directory, and mounts the log-file volume (DefaultLogDirectory,
// pkg/prometheus/common.go:53 at prometheus-operator v0.94.1).
const prometheusLogDirectory = "/var/log/prometheus"

// validateThanosSidecarMounts refuses an entry of thanos.volumeMounts at a
// path the operator mounts a volume at in the thanos-sidecar container, as
// refuseGeneratedMounts refuses it (createThanosContainer,
// pkg/prometheus/server/statefulset.go:544 at prometheus-operator v0.94.1):
// /prometheus, the data volume, where objectStorageConfig or
// objectStorageConfigFile is set (:649 and :664-671), and /etc/thanos/config,
// the HTTP client configuration, for Thanos 0.24.0 and later (:730-738,
// thanosConfigDir in thanos_sidecar_config.go:29). The latter is reserved
// whatever thanos.version names, as web-config is (validatePrometheusName).
//
// It refuses too a field of an entry other than name and mountPath: the
// operator copies only those two into the sidecar's mount (:642-647), so any
// other it drops, and the sidecar mounts the volume without it.
func validateThanosSidecarMounts(t *monitoringv1.ThanosSpec) error {
	if t == nil {
		return nil
	}
	for i, vm := range t.VolumeMounts {
		for _, f := range thanosDroppedMountFields(vm) {
			if f.set {
				return errors.Errorf("thanos.volumeMounts[%d].%s: not carried: the Prometheus operator copies only name and mountPath of a sidecar mount and drops it, so the thanos-sidecar container mounts the volume without it; leave it unset", i, f.name)
			}
		}
	}
	generated := map[string]string{
		"/etc/thanos/config": "the path the Prometheus operator mounts the sidecar's HTTP client configuration at in the thanos-sidecar container for Thanos 0.24.0 and later",
	}
	if t.ObjectStorageConfig != nil || t.ObjectStorageConfigFile != nil {
		generated["/prometheus"] = "the path the Prometheus operator mounts the data volume at in the thanos-sidecar container where object storage is configured"
	}
	return refuseGeneratedMounts(operatorContainerMounts{field: "thanos.volumeMounts", generated: generated, mounts: t.VolumeMounts})
}

// validatePrometheusPortName refuses a portName the API refuses where the
// operator writes it: unless listenLocal is set, as the name of the prometheus
// container's one port (MakeContainerPorts, pkg/prometheus/common.go:459-469
// at prometheus-operator v0.94.1); unless serviceName names a Service of the
// author's, as the name and target port of the governing Service's web port,
// beside the port grpc it adds where thanos is set
// (pkg/prometheus/server/operator.go:1006-1030, BuildStatefulSetService in
// common.go:500-520), as validateOperatorPortName refuses them. The Thanos
// sidecar's ports, http and grpc, are on its own container, where a port name
// of the prometheus container's does not clash.
func validatePrometheusPortName(spec *monitoringv1.PrometheusSpec) error {
	containerPort, servicePort := !spec.ListenLocal, spec.ServiceName == nil
	reserved := map[string]string{}
	if servicePort && spec.Thanos != nil {
		reserved["grpc"] = "the port the Prometheus operator adds for the Thanos sidecar to the governing Service it creates where serviceName is unset"
	}
	return validateOperatorPortName(spec.PortName, containerPort || servicePort, reserved)
}

// usesLogFileVolume says the operator adds the log-file volume, mounted at
// /var/log/prometheus in the prometheus container: where scrapeFailureLogFile
// is a bare file name, or queryLogFile is one and scrapeFailureLogFile is
// unset (UsesDefaultFileVolume, BuildCommonVolumes, pkg/prometheus/common.go:
// 226-228 and :330-345; appendServerVolumes,
// pkg/prometheus/server/statefulset.go:508-541 at prometheus-operator v0.94.1).
func usesLogFileVolume(spec *monitoringv1.PrometheusSpec) bool {
	bare := func(file string) bool { return file != "" && filepath.Dir(file) == "." }
	if spec.ScrapeFailureLogFile != nil {
		return bare(*spec.ScrapeFailureLogFile)
	}
	return bare(spec.QueryLogFile)
}

// prometheusRuleFiles is how many rule ConfigMaps the operator mounts at
// least, whether or not it has rules for them, so that a change in their
// number does not roll the pods (AppendConfigMapNames, pkg/operator/rules.go:
// 343-361; createOrUpdateRuleConfigMaps, pkg/prometheus/server/rules.go:99-119,
// the 3 at :119, at prometheus-operator v0.94.1). It names them
// prometheus-<name>-rulefiles-<i>, and the volume of each after its ConfigMap
// (appendServerVolumes, pkg/prometheus/server/statefulset.go:514-526). It
// creates more only where the PrometheusRules it selects outgrow three
// ConfigMaps of half a MiB each (makeConfigMapsFromRules and
// MaxConfigMapDataSize, pkg/operator/rules.go:440-465 and :58-62), which the
// object does not say; an eleventh's volume, rulefiles-10, is one character
// longer than rulefiles-2 and is not checked.
const prometheusRuleFiles = 3

// validatePrometheusName refuses a Prometheus name the operator's objects
// cannot be named after, a data volume the pods would not get, and an entry of
// volumes or volumeMounts the operator's own clash with, as
// validateOperatorObjectName, refuseGeneratedVolumes and refuseGeneratedMounts
// refuse them; it holds the volumes and mounts here, as the rule ConfigMaps'
// are named after the Prometheus.
//
// The operator names the StatefulSet of shard 0 prometheus-<name> and of shard
// i prometheus-<name>-shard-<i>, whose pods take the hostname
// <StatefulSet>-<ordinal>, and the data volume prometheus-<name>-db
// (prometheusNameByShard, VolumeName and PrefixedName,
// pkg/prometheus/common.go:145-151 and :197-203 at prometheus-operator
// v0.94.1); beside emptyDir or ephemeral it creates the data volume under that
// name (pkg/prometheus/server/statefulset.go:105-128). The longest hostname is
// the last pod's of the last shard, the replica and shard counts as the
// operator reads them (prometheusPods). The volume of the third rule
// ConfigMap, prometheus-<name>-rulefiles-2, which the operator mounts
// whatever the rules, is longer than the hostname of a Prometheus of one shard,
// and must be a DNS-1123 label too.
//
// The volumes the operator adds under a fixed name are config, tls-assets and
// config-out (BuildCommonVolumes, common.go:242-261), web-config
// (BuildWebconfig, server/statefulset.go:192-205), log-file where
// usesLogFileVolume, thanos-prometheus-http-client-file where thanos is set
// (createThanosContainer, :729-746), and the rule ConfigMaps' volumes. The
// operator adds web-config only for Prometheus 2.24.0 and later, and the
// sidecar's only for Thanos 0.24.0 and later; both are reserved whatever the
// versions name, as on the alertmanager kind. Its mounts in the prometheus
// container are /prometheus, /etc/prometheus/config_out and
// /etc/prometheus/certs (common.go:263-283), /etc/prometheus/secrets/<name>
// and /etc/prometheus/configmaps/<name> for each entry of secrets and
// configMaps (:286-326), /var/log/prometheus where usesLogFileVolume,
// /etc/prometheus/web_config/web-config.yaml, and
// /etc/prometheus/rules/<ConfigMap> for each rule ConfigMap
// (server/statefulset.go:528-534); /etc/prometheus/config is the reloaders'
// only (CreateConfigReloaderVolumeMounts, common.go:471-480). A rule
// ConfigMap's ordinal is written in decimal without a leading zero
// (configMapNameAt, pkg/operator/rules.go:363-365), so only such a name is
// reserved. The volumes and mounts of the web TLS credentials are left to the
// API, as on the alertmanager kind: the operator names each volume after the
// credential's source with a hash appended (pkg/webconfig/tls_credentials.go,
// pkg/k8s/resource_namer.go), which is not derived here.
func validatePrometheusName(name, componentName string, spec *monitoringv1.PrometheusSpec) error {
	prefix := "prometheus-" + name
	n := operatorObjectName{
		kind: "Prometheus", label: "prometheus",
		name: name, componentName: componentName,
		dataVolume: prefix + "-db",
		storage:    spec.Storage,
		volumes:    spec.Volumes,
	}
	replicas := int32(1)
	if spec.Replicas != nil && *spec.Replicas >= 0 {
		replicas = *spec.Replicas
	}
	if replicas > 0 {
		statefulSet := prefix
		if spec.Shards != nil && *spec.Shards > 1 {
			statefulSet = fmt.Sprintf("%s-shard-%d", prefix, *spec.Shards-1)
		}
		n.derived = append(n.derived, derivedName{"the pod of the last replica of the last shard takes the hostname", fmt.Sprintf("%s-%d", statefulSet, replicas-1)})
	}
	ruleFiles := fmt.Sprintf("%s-rulefiles-%d", prefix, prometheusRuleFiles-1)
	n.derived = append(n.derived, derivedName{"the Prometheus operator names the volume of a rule ConfigMap it mounts", ruleFiles})
	if err := validateOperatorObjectName(n); err != nil {
		return err
	}
	ruleFile := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "-rulefiles-(0|[1-9][0-9]*)$")
	ruleMount := regexp.MustCompile("^/etc/prometheus/rules/" + regexp.QuoteMeta(prefix) + "-rulefiles-(0|[1-9][0-9]*)$")
	volumes := map[string]string{}
	for _, v := range []string{"config", "tls-assets", "config-out", "web-config"} {
		volumes[v] = "a volume the Prometheus operator adds to every Prometheus's pods"
	}
	mounts := map[string]string{}
	for _, p := range []string{"/prometheus", "/etc/prometheus/config_out", "/etc/prometheus/certs", "/etc/prometheus/web_config/web-config.yaml"} {
		mounts[p] = "a path the Prometheus operator mounts a volume at in every prometheus container"
	}
	if usesLogFileVolume(spec) {
		volumes["log-file"] = "the volume the Prometheus operator adds for a log file named without a directory in scrapeFailureLogFile or queryLogFile"
		mounts["/var/log/prometheus"] = "the path the Prometheus operator mounts the volume of a log file named without a directory in scrapeFailureLogFile or queryLogFile at"
	}
	if spec.Thanos != nil {
		volumes["thanos-prometheus-http-client-file"] = "the volume the Prometheus operator adds for the Thanos sidecar's configuration where thanos is set"
	}
	if err := refuseGeneratedVolumes(operatorPodVolumes{
		generated: volumes,
		patterns:  []reservedPattern{{ruleFile, "the volume of a rule ConfigMap the Prometheus operator mounts"}},
		secrets:   spec.Secrets, configMaps: spec.ConfigMaps,
		storage: spec.Storage,
		volumes: spec.Volumes,
	}); err != nil {
		return err
	}
	return refuseGeneratedMounts(operatorContainerMounts{
		field:      "volumeMounts",
		generated:  mounts,
		patterns:   []reservedPattern{{ruleMount, "the path the Prometheus operator mounts a rule ConfigMap at"}},
		root:       "/etc/prometheus",
		secrets:    spec.Secrets,
		configMaps: spec.ConfigMaps,
		mounts:     spec.VolumeMounts,
	})
}

// prometheusPods is the number of pods the operator runs for a Prometheus:
// one StatefulSet per shard, each with the replica count. The operator reads
// an unset or negative replica count as 1 (ReplicasNumberPtr), and an unset
// shard count, or one of 1 or less, as 1 (shardsNumber,
// pkg/prometheus/common.go:118-143 at prometheus-operator v0.94.1). The type's
// own ExpectedReplicas multiplies the two as written, so it does not count a
// negative one as the operator runs it.
func prometheusPods(spec *monitoringv1.PrometheusSpec) int64 {
	replicas := int64(1)
	if spec.Replicas != nil && *spec.Replicas >= 0 {
		replicas = int64(*spec.Replicas)
	}
	shards := int64(1)
	if spec.Shards != nil && *spec.Shards > 1 {
		shards = int64(*spec.Shards)
	}
	return replicas * shards
}

// prometheusGenerated names the containers the operator generates for the
// pods, by the property that lists patches of them (makeStatefulSetSpec,
// pkg/prometheus/server/statefulset.go:307-318 and :334-369 at
// prometheus-operator v0.94.1): prometheus and config-reloader, with
// init-config-reloader as an init container, on every Prometheus, and
// thanos-sidecar where thanos is set (createThanosContainer, :544-547). Of
// them prometheus takes its image from image and thanos-sidecar from
// thanos.image; the two reloaders run the image of the operator's own
// configuration unless a listed entry patches one (BuildConfigReloader,
// pkg/prometheus/common.go:362-417).
func prometheusGenerated(spec *monitoringv1.PrometheusSpec) map[string][]string {
	containers := []string{"prometheus", "config-reloader"}
	if spec.Thanos != nil {
		containers = append(containers, "thanos-sidecar")
	}
	return map[string][]string{
		"containers":     containers,
		"initContainers": {"init-config-reloader"},
	}
}

// prometheusGeneratedPorts are the ports the operator gives the containers it
// generates, in its order: the prometheus container's web port under portName
// (web where it is unset) at 9090/TCP unless listenLocal is set
// (MakeContainerPorts, pkg/prometheus/common.go:457-469 at
// prometheus-operator v0.94.1); the config-reloader container's reloader-web
// at 8080/TCP unless listenLocal is set under the HTTP reloadStrategy, the
// default (BuildConfigReloader, common.go:391-414: only that strategy hands
// listenLocal on); the init-config-reloader container's reloader-init at
// 8081/TCP, whatever either says (common.go:384-389, CreateConfigReloader,
// pkg/operator/config_reloader.go:226-282); and, where thanos is set, the
// thanos-sidecar container's http at 10902 and grpc at 10901, which name no
// protocol, so the API's TCP, whatever thanos.listenLocal says
// (pkg/prometheus/server/statefulset.go:629-638).
func prometheusGeneratedPorts(spec *monitoringv1.PrometheusSpec) map[string][]corev1.ContainerPort {
	port := func(name string, number int32, protocol corev1.Protocol) corev1.ContainerPort {
		return corev1.ContainerPort{Name: name, ContainerPort: number, Protocol: protocol}
	}
	ports := map[string][]corev1.ContainerPort{
		"init-config-reloader": {port("reloader-init", 8081, corev1.ProtocolTCP)},
	}
	if !spec.ListenLocal {
		web := spec.PortName
		if web == "" {
			web = "web"
		}
		ports["prometheus"] = []corev1.ContainerPort{port(web, 9090, corev1.ProtocolTCP)}
	}
	signal := spec.ReloadStrategy != nil && *spec.ReloadStrategy == monitoringv1.ProcessSignalReloadStrategyType
	if !spec.ListenLocal || signal {
		ports["config-reloader"] = []corev1.ContainerPort{port("reloader-web", 8080, corev1.ProtocolTCP)}
	}
	if spec.Thanos != nil {
		ports["thanos-sidecar"] = []corev1.ContainerPort{port("http", 10902, ""), port("grpc", 10901, "")}
	}
	return ports
}

// prometheusWorkload maps a Prometheus spec into the workload the two shared
// functions read. It only reads spec.
//
// Held through it: image and thanos.image, each where no patch of its
// container names an image in its place; the pods of all shards, replicas
// times shards as the operator counts them (prometheusPods), authored or not;
// storage; resources, the prometheus container's and the Thanos sidecar's as
// the operator runs them: the spec's block, which the operator copies as
// written and fills no request of (statefulset.go:346 and :639), with the
// requests and limits of a listed entry that patches the container merged
// over it, key by key, the entry's block then not checked alone (both are
// generated before the listed containers are merged into them,
// statefulset.go:334-366); the images of the containers the operator
// generates, where the spec leaves them to it; the deprecated bearerToken of
// each remoteWrite and remoteRead entry and of apiserverConfig, the
// credentials the spec holds in the clear; and, as pod fields, containers,
// initContainers, volumes, securityContext and hostNetwork. The spec has no
// hostPID or hostIPC field.
// TestMonitoringWorkloadKinds_PodFieldsHeldOrListed and
// TestMonitoringWorkloadKinds_CredentialsHeldOrListed derive both claims from
// the type.
func prometheusWorkload(spec *monitoringv1.PrometheusSpec) monitoringWorkload {
	pods := prometheusPods(spec)
	w := monitoringWorkload{
		pod: corev1.PodSpec{
			InitContainers:  spec.InitContainers,
			Containers:      spec.Containers,
			Volumes:         spec.Volumes,
			SecurityContext: spec.SecurityContext,
			HostNetwork:     spec.HostNetwork,
		},
		generated:      prometheusGenerated(spec),
		generatedPorts: prometheusGeneratedPorts(spec),
		serviceName:    spec.ServiceName,
		replicas:       &pods,
		replicasPath:   "replicas times shards",
		storage:        spec.Storage,
	}
	if spec.DNSPolicy != nil {
		w.pod.DNSPolicy = corev1.DNSPolicy(*spec.DNSPolicy)
	}
	if spec.DNSConfig != nil {
		w.pod.DNSConfig = &corev1.PodDNSConfig{Nameservers: spec.DNSConfig.Nameservers}
	}
	// The image a patch names replaces the spec's in the container the
	// operator builds from it, so the spec's is then not run, and not held: the
	// patch's own is, as a listed container's image.
	held := func(path string, image *string, resources corev1.ResourceRequirements, container string) {
		block := fieldResources{path, resources}
		if i := patchOf(spec.Containers, container); i >= 0 {
			patch := spec.Containers[i].Resources
			if len(patch.Requests) > 0 || len(patch.Limits) > 0 {
				block = fieldResources{
					fmt.Sprintf("%s with containers[%d] %q merged over it", path, i, container),
					mergedResources(resources, patch),
				}
				if w.mergedPatches == nil {
					w.mergedPatches = map[string][]string{}
				}
				w.mergedPatches["containers"] = append(w.mergedPatches["containers"], container)
			}
		}
		w.resources = append(w.resources, block)
		imagePath := strings.TrimSuffix(path, "resources") + "image"
		switch {
		case patchedImage(spec.Containers, container) != "":
		case image != nil && *image != "":
			w.images = append(w.images, fieldValue{imagePath, *image})
		default:
			w.unsetImages = append(w.unsetImages, imagePath)
		}
	}
	held("resources", spec.Image, spec.Resources, "prometheus")
	if t := spec.Thanos; t != nil {
		held("thanos.resources", t.Image, t.Resources, "thanos-sidecar")
	}
	for _, reloader := range []struct {
		list       string
		containers []corev1.Container
		name       string
	}{{"containers", spec.Containers, "config-reloader"}, {"initContainers", spec.InitContainers, "init-config-reloader"}} {
		if patchedImage(reloader.containers, reloader.name) == "" {
			w.unsetImages = append(w.unsetImages, fmt.Sprintf("the image of the %s container (%s)", reloader.name, reloader.list))
		}
	}
	for i, rw := range spec.RemoteWrite {
		if rw.BearerToken != "" {
			w.literals = append(w.literals, fmt.Sprintf("remoteWrite[%d].bearerToken", i))
		}
	}
	for i, rr := range spec.RemoteRead {
		if rr.BearerToken != "" {
			w.literals = append(w.literals, fmt.Sprintf("remoteRead[%d].bearerToken", i))
		}
	}
	if a := spec.APIServerConfig; a != nil && a.BearerToken != "" {
		w.literals = append(w.literals, "apiserverConfig.bearerToken")
	}
	return w
}

// ToApplicationConfig decodes an OAM prometheus component into its config.
// The object takes the namespace of the application it is generated in.
//
// The properties are serialized once, and that one tree is what the
// empty-group refusal and the spec's decode read: a value whose encoder answers differently on another call cannot pass the
// refusal in one form and reach the decode in another.
func (h *PrometheusHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	_, props, err := jsonProperties(component.Properties)
	if err != nil {
		return nil, err
	}
	if err := refuseEmptyExcludedGroups(props); err != nil {
		return nil, err
	}
	rest := *component
	rest.Properties = maps.Clone(props)
	return prometheusKind.config(&rest)
}
