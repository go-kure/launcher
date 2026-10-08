package components

import (
	"fmt"
	"maps"
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

// ThanosRulerHandler handles OAM thanosruler components: the kind-named
// projection of a monitoring.coreos.com/v1 ThanosRuler
// (go-kure/launcher#790).
//
// Its properties are the top-level fields of monitoringv1.ThanosRulerSpec,
// under their json names, decoded strictly (decodeKindSpec), but for one: the
// spec's `labels`, the external labels of the ruler's series and alerts, is
// the `externalLabels` property, since the engine reads a kind component's
// `labels` as the object's own (thanosRulerExternalLabels). It emits the
// ThanosRuler, named after the component unless `objectName` names it, in the
// build namespace, and nothing else: the Prometheus operator runs the pods,
// from a StatefulSet it builds. What the spec says of those pods is held to
// the environment policy as a workload kind's own fields are
// (thanosRulerWorkload, monitoring_workload.go).
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags, the one renamed field mapped.
type ThanosRulerHandler struct{}

// CanHandle returns true for the thanosruler component type.
func (h *ThanosRulerHandler) CanHandle(componentType string) bool {
	return componentType == "thanosruler"
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ThanosRulerHandler) ContractMetadata() oam.ContractMetadata {
	return contract("thanosruler")
}

// ComponentObject declares the thanosruler kind's ThanosRuler.
func (h *ThanosRulerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return monitoringKind(monitoringv1.ThanosRulerKind), oam.ObjectScopeNamespaced
}

// PropertySchema declares the top-level monitoringv1.ThanosRulerSpec fields by
// their json names. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *ThanosRulerHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ThanosRuler spec."
	const decoded = " Decoded strictly into the Prometheus operator's API type: see "
	const core = " Decoded strictly into the Kubernetes API type: see "
	const duration = " A number and a unit: ms, s, m, h, d, w or y."
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
	const secretKey = " A Secret key selector: name and key, in the ThanosRuler's namespace. Launcher emits no Secret for it." //nolint:gosec // G101: a description, not a credential
	return map[string]oam.PropertySchema{
		"version":          text("version: the Thanos version the operator configures for."),
		"podMetadata":      object("podMetadata: the labels and annotations the operator copies onto the ThanosRuler pods. A key the consumer reserves is refused here as on a workload's pod template. Nothing is added: the pods carry the component label only if it is written here with the component's own value, and without it the NetworkPolicies generated for the component do not select them. The operator sets four labels and one annotation of its own, which a value authored here does not replace." + decoded + "EmbeddedObjectMetadata in its API reference."),
		"image":            text("image: the full image reference of the thanos-ruler container, with a tag other than latest or a digest. Held to the EnvironmentPolicy's allowed registries. An image an entry of containers named thanos-ruler names replaces it: that one runs and is held, and this one is not. Unset or empty, the image is the one an entry of containers named thanos-ruler names, and where none does the operator chooses the one that runs: refused under a policy with allowed registries, which cannot hold that choice, and built under one without."),
		"imagePullPolicy":  text("imagePullPolicy: when the image of the thanos-ruler container is pulled: Always, Never or IfNotPresent."),
		"imagePullSecrets": objects("imagePullSecrets: the Secrets of the ThanosRuler's namespace that hold the credentials the images are pulled with.", "One reference: name."),
		"paused":           flag("paused: true stops the operator from acting on the objects it manages for this ThanosRuler, deletion excepted."),
		"replicas":         number("replicas: the number of Thanos Ruler pods. A negative one is refused: the operator copies it into the StatefulSet, which the API refuses. Held to the EnvironmentPolicy's replica maximum. Unset, the StatefulSet runs 1, which is held to that maximum; nothing is written, and no replica default of the policy is applied."),
		"nodeSelector":     object("nodeSelector: the node labels a node must carry for the pods to be scheduled on it."),
		"schedulerName":    text("schedulerName: the scheduler that places the pods. Unset, the default scheduler. Not empty."),
		"resources":        object("resources: the resource requests and limits of the thanos-ruler container. Its cpu and memory are held to the EnvironmentPolicy's maxima, and a request may not exceed its limit. Without a memory request the operator requests 200Mi, whatever the limit, which is held to the memory maximum, and a memory limit under it is refused; nothing is written, and no resource default of the policy is applied. A containers entry named thanos-ruler is merged over this block key by key, and the checks hold the merged block, so a memory request it names replaces the 200Mi." + core + "ResourceRequirements in the Kubernetes API reference."),
		"affinity":         object("affinity: the scheduling constraints of the pods." + core + "Affinity in the Kubernetes API reference."),
		"tolerations": objects("tolerations: the taints the pods tolerate.",
			"One toleration."+core+"Toleration in the Kubernetes API reference."),
		"topologySpreadConstraints": objects("topologySpreadConstraints: how the pods are spread over topology domains.",
			"One constraint."+core+"TopologySpreadConstraint in the Kubernetes API reference."),
		"securityContext":    object("securityContext: the pod-level security attributes of the pods. Its windowsOptions.hostProcess cannot be true: the API requires hostNetwork: true of a HostProcess pod, and a ThanosRuler has no hostNetwork." + core + "PodSecurityContext in the Kubernetes API reference."),
		"dnsPolicy":          text("dnsPolicy: the DNS policy of the pods: ClusterFirstWithHostNet, ClusterFirst, Default or None. None is refused without dnsConfig.nameservers, which the API then requires."),
		"dnsConfig":          object("dnsConfig: the DNS configuration of the pods: nameservers, searches and options." + decoded + "PodDNSConfig in its API reference."),
		"enableServiceLinks": flag("enableServiceLinks: whether the Services of the namespace are injected into the pods' environment variables."),
		"priorityClassName":  text("priorityClassName: the priority class of the pods."),
		"serviceName":        text("serviceName: the name of the governing Service of the StatefulSet, which must exist in the namespace and select the pods. Unset, the operator creates and manages a headless Service named thanos-ruler-operated. Not empty, and a DNS-1035 label (starting with a letter), the rule of every Service name here: the API refuses a Service of another name before Kubernetes 1.36 (by default), and the operator fails to reconcile where it finds no Service of the name."),
		"serviceAccountName": text("serviceAccountName: the ServiceAccount the pods run as. Launcher creates none for it and does not check that it exists."),
		"storage":            object("storage: where the Thanos Ruler pods keep their data: emptyDir, ephemeral or volumeClaimTemplate, in that order of precedence; the operator uses the first that is set. A claim template's name beside emptyDir or ephemeral is refused unless it is thanos-ruler-<name>-data: the operator mounts the data volume under it and creates it under that name. On the volumeClaimTemplate arm, a claim template's name must be a DNS-1123 label, which the operator names the data volume with, and not the name of a volume the operator adds. A claim of the arm in use, ephemeral or volumeClaimTemplate, must request storage above 0 (spec.resources.requests.storage), and an ephemeral one name its access modes; unset or empty access modes of volumeClaimTemplate are ReadWriteOnce. An emptyDir claims nothing. Unset, the storage is the operator's to decide: no storage default of the policy is applied. The storage the claim template of the arm in use requests is held to the EnvironmentPolicy's storage maximum; a claim template of an arm after it is not, nor the size limit of an emptyDir. A claim template's labels and annotations are not read for reserved keys and take no component label, as a statefulset's are not." + decoded + "StorageSpec in its API reference."),
		"volumes": objects("volumes: further volumes of the Thanos Ruler pods, beside the ones the operator generates. A volume named as one of those is refused: tls-assets, web-config whatever version names, remote-write-config, the volume of each Secret key the spec sets (query-config, alertmanager-config, and objstorage-config, tracing-config and alertrelabel-config where their file field is unset), the rule ConfigMaps' thanos-ruler-<name>-rulefiles-<n>, and the data volume. So are two entries of one name. The web TLS credentials' volumes, whose names the operator hashes, are left to the API. Held to the EnvironmentPolicy as a pod's volumes are: hostPath, the storage a generic ephemeral volume's claim requests, the registry of an image volume.",
			"One volume."+core+"Volume in the Kubernetes API reference."),
		"volumeMounts": objects("volumeMounts: further volume mounts of the thanos-ruler container. A mount path the operator mounts a volume at is refused: /thanos/data, /etc/thanos/certs, the web configuration file /etc/thanos/web_config/web-config.yaml whatever version names, /etc/thanos/config/<volume> of each Secret key's volume the operator adds (as listed under volumes), and /etc/thanos/rules/thanos-ruler-<name>-rulefiles-<n>. The mounts of the web TLS credentials are left to the API.",
			"One volume mount."+core+"VolumeMount in the Kubernetes API reference."),
		"objectStorageConfig":     object("objectStorageConfig: the Secret key that holds the object storage configuration the ruler uploads its blocks to. objectStorageConfigFile takes precedence." + secretKey),
		"objectStorageConfigFile": text("objectStorageConfigFile: the path, in the thanos-ruler container, of the object storage configuration file. Takes precedence over objectStorageConfig."),
		"listenLocal":             flag("listenLocal: true makes the Thanos Ruler listen on loopback only, not on the pod's address."),
		"podManagementPolicy":     text("podManagementPolicy: how the StatefulSet creates and deletes pods when it scales: Parallel, the operator's default, or OrderedReady. Changing it recreates the StatefulSet."),
		"updateStrategy":          object("updateStrategy: how the StatefulSet replaces its pods on a change: type (RollingUpdate, the default, or OnDelete) and rollingUpdate with maxUnavailable. The API refuses rollingUpdate with another type than RollingUpdate; launcher does not check that rule." + decoded + "StatefulSetUpdateStrategy in its API reference."),
		"queryEndpoints": texts("queryEndpoints: the Thanos Query endpoints the rules are evaluated against. queryConfig takes precedence. Launcher does not read a credential written into an address.",
			"The address of one endpoint."),
		"queryConfig": object("queryConfig: the Secret key that holds the configuration of the Thanos Query endpoints. Takes precedence over queryEndpoints." + secretKey),
		"alertmanagersUrl": texts("alertmanagersUrl: the Alertmanager endpoints alerts are sent to. alertmanagersConfig takes precedence. Launcher does not read a credential written into a URL.",
			"The URL of one endpoint."),
		"alertmanagersConfig":    object("alertmanagersConfig: the Secret key that holds the configuration of the Alertmanager endpoints. Takes precedence over alertmanagersUrl." + secretKey),
		"ruleSelector":           object("ruleSelector: the PrometheusRule objects evaluated, by their labels. Unset, none; an empty selector, all. A Kubernetes label selector: matchLabels and matchExpressions."),
		"ruleNamespaceSelector":  object("ruleNamespaceSelector: the namespaces PrometheusRule objects are read from, by their labels. Unset, the ThanosRuler's own namespace only. A Kubernetes label selector: matchLabels and matchExpressions."),
		"enforcedNamespaceLabel": text("enforcedNamespaceLabel: the label that every alert and series a selected rule yields gets, with the namespace of the rule's object as its value."),
		"excludedFromEnforcement": objects("excludedFromEnforcement: the objects whose rules do not get enforcedNamespaceLabel.",
			"One reference: group, resource, namespace and name."+decoded+"ObjectReference in its API reference."),
		"prometheusRulesExcludedFromEnforce": objects("prometheusRulesExcludedFromEnforce: the PrometheusRule objects whose rules do not get enforcedNamespaceLabel. Deprecated upstream for excludedFromEnforcement.",
			"One reference: ruleNamespace and ruleName, both required."),
		"logLevel":            text("logLevel: the log level of Thanos Ruler: debug, info, warn or error."),
		"logFormat":           text("logFormat: the log format of Thanos Ruler: logfmt or json."),
		"portName":            text("portName: the name of the web port on the pods and the governing Service. Unset, the API fills web; an empty one is refused, since the API server would replace it. A name the API refuses for a port (not an IANA service name: at most 15 characters, lower-case letters, digits and -) is refused where the operator writes it, and so is grpc, the port the operator adds beside it on the thanos-ruler container unless listenLocal is set, and on the governing Service unless serviceName is set."),
		"evaluationInterval":  text("evaluationInterval: the interval between two evaluations of the rules. Unset, the API fills 15s; an empty one is refused, since the API server would replace it." + duration),
		"resendDelay":         text("resendDelay: the least time before an alert is sent to Alertmanager again." + duration),
		"ruleOutageTolerance": text("ruleOutageTolerance: the longest outage of the query endpoints after which the for state of an alert is still restored. Requires Thanos v0.30.0 or later." + duration),
		"ruleQueryOffset":     text("ruleQueryOffset: the default query offset of a rule group. Requires Thanos v0.38.0 or later." + duration),
		"ruleConcurrentEval":  number("ruleConcurrentEval: how many rules may be evaluated at once. At least 1. Requires Thanos v0.37.0 or later."),
		"ruleGracePeriod":     text("ruleGracePeriod: the least time between an alert and the restored for state, for alerts with a longer for. Requires Thanos v0.30.0 or later." + duration),
		"retention":           text("retention: how long Thanos Ruler keeps its data. Unset, the API fills 24h; an empty one is refused, since the API server would replace it. No effect with remoteWrite, where the ruler keeps none." + duration),
		"containers": objects("containers: further containers of the pods, and patches of the ones the operator generates: an entry that shares its name with a container the operator generates (thanos-ruler, config-reloader) is merged into it. Each is held to the EnvironmentPolicy as a pod's containers are: the registry of an authored image, cpu and memory maxima, privilege and capabilities. A patch may name no image; any other entry must name one. A name may be listed once: the operator runs only the last entry of a name. A name of a listed init container is refused. Under a policy with allowed registries, config-reloader must be patched with an image from one of them: unpatched, it runs the image of the operator's own configuration, which the allowlist cannot hold.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"initContainers": objects("initContainers: further init containers of the pods. The operator generates none and adds each entry as written, so each must name an image. Held to the EnvironmentPolicy as containers are, a name listed once as there and never a container's (thanos-ruler, config-reloader or a listed one).",
			"One container."+core+"Container in the Kubernetes API reference."),
		"tracingConfig":     object("tracingConfig: the Secret key that holds the tracing configuration. Experimental upstream. tracingConfigFile takes precedence." + secretKey),
		"tracingConfigFile": text("tracingConfigFile: the path, in the thanos-ruler container, of the tracing configuration file. Experimental upstream. Takes precedence over tracingConfig."),
		thanosRulerExternalLabelsProperty: {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "labels, published as externalLabels: the Prometheus external labels added to every series and alert the ruler yields, a map of label names to values. They are not Kubernetes labels: the labels property sets the ThanosRuler object's own, and the reserved-key and component-label checks of that property do not apply here.",
		},
		"alertDropLabels": texts("alertDropLabels: the label names dropped from the alerts; thanos_ruler_replica is always dropped.",
			"The name of one label."),
		"externalPrefix":         text("externalPrefix: the path prefix under which Thanos Ruler is reached from outside, which the URLs it generates are built from."),
		"routePrefix":            text("routePrefix: the path prefix Thanos Ruler registers its HTTP handlers under."),
		"grpcServerTlsConfig":    object("grpcServerTlsConfig: the TLS configuration of the gRPC server Thanos Query reads recorded series from. The operator reads only minVersion, caFile, certFile, keyFile, cipherSuites and curves." + decoded + "GRPCServerTLSConfig in its API reference."),
		"alertQueryUrl":          text("alertQueryUrl: the URL set in the Source field of every alert."),
		"minReadySeconds":        number("minReadySeconds: how many seconds a new pod must be ready before it counts as available. At least 0."),
		"alertRelabelConfigs":    object("alertRelabelConfigs: the Secret key that holds the alert relabeling configuration. alertRelabelConfigFile takes precedence." + secretKey),
		"alertRelabelConfigFile": text("alertRelabelConfigFile: the path, in the thanos-ruler container, of the alert relabeling configuration file. Takes precedence over alertRelabelConfigs."),
		"hostAliases": objects("hostAliases: further entries of the pods' hosts file.",
			"One entry: ip and hostnames, both required."),
		"additionalArgs": objects("additionalArgs: further command-line arguments of the thanos-ruler container, passed as they are; launcher does not read them. Upstream documents that an argument the operator already sets, or an invalid one, fails the reconciliation.",
			"One argument: name (required) and value."),
		"web": object("web: the web server's settings: tlsConfig and httpConfig." + decoded + "ThanosRulerWebSpec in its API reference."),
		"remoteWrite": objects("remoteWrite: the remote write endpoints the ruler sends its series to; with one, the ruler keeps no data of its own. Requires Thanos v0.24.0 or later. The deprecated bearerToken of an entry is refused under an EnvironmentPolicy that forbids explicit secrets; every other credential of an entry is the key of a Secret or the path of a file in the container. Launcher does not read a credential written into a header or a URL.",
			"One endpoint: url, required. An empty action of a writeRelabelConfigs rule is refused, since the API server would replace it with replace."+decoded+"RemoteWriteSpec in its API reference."),
		"terminationGracePeriodSeconds": number("terminationGracePeriodSeconds: how many seconds the pods are given to stop. Unset, the operator's default of 120. At least 0."),
		"enableFeatures": texts("enableFeatures: the Thanos Ruler feature flags to enable. Requires Thanos v0.39.0 or later.",
			"The name of one feature flag."),
		"hostUsers": flag("hostUsers: false runs the pods in a user namespace of their own, not the host's."),
	}
}

// thanosRulerKind is the thanosruler kind: see policyHeldKind. The API
// requires no top-level field of the spec, and of what is authored below it
// the fields thanosRulerRequired lists; the type would write each one empty.
// The API's value rules are left to it, the expression rules the spec reaches
// included (thanosRulerRulesLeft): the linked module ships no CRD to hold
// either to.
//
// The fields on which an authored 0 or "" cannot be carried are the ones of a
// pod spec's containers and volumes (podSpecDefaultedZeros): the spec lists
// containers, init containers and volumes of the Kubernetes types, and has no
// hostNetwork, so a listed container port's hostPort is not among them. Six
// strings of the operator's own types cannot be carried empty either
// (thanosRulerDefaultedZeroFields). TestMonitoringWorkloadKinds_DefaultedZeros
// derives the list (monitoringWorkloadDefaultedZeros).
var thanosRulerKind = &policyHeldKind[monitoringv1.ThanosRulerSpec]{
	policyFreeKind: policyFreeKind[monitoringv1.ThanosRulerSpec]{
		upstream:       "monitoring.coreos.com/v1 ThanosRulerSpec",
		required:       thanosRulerRequired,
		defaultedZeros: monitoringWorkloadDefaultedZeros(nil, thanosRulerDefaultedZeroFields),
		validate:       validateThanosRuler,
		validateName:   validateThanosRulerName,
		build: func(name, namespace string, spec *monitoringv1.ThanosRulerSpec) client.Object {
			ruler := prometheus.CreateThanosRuler(name, namespace)
			spec.DeepCopyInto(&ruler.Spec)
			withExcludedGroups(ruler.Spec.ExcludedFromEnforcement)
			return ruler
		},
	},
	enforce: func(spec *monitoringv1.ThanosRulerSpec, p oam.Policy) error {
		return enforceMonitoringWorkloadPolicy(thanosRulerWorkload(spec), p)
	},
}

// thanosRulerDefaultedZeroFields are the fields of the operator's own types in
// the thanosruler kind's defaulted-zero list (monitoringWorkloadDefaultedZeros):
// four to which the CRD gives another default, and two the operator copies to a
// pod field the API server defaults (thanos/statefulset.go at
// prometheus-operator v0.94.1): schedulerName to the pod's (:521), and
// imagePullPolicy to its thanos-ruler container's (:477). The config reloader
// is given no pull policy of the spec (:402-418).
var thanosRulerDefaultedZeroFields = map[string]string{
	"portName":           `"web"`,
	"evaluationInterval": `"15s"`,
	"retention":          `"24h"`,
	"remoteWrite[].writeRelabelConfigs[].action": `"replace"`,
	"schedulerName":   `"default-scheduler"`,
	"imagePullPolicy": imagePullPolicyDefault,
}

// thanosRulerRequired lists the fields the API requires below an authored
// parent that the type would write unauthored, the key and the operator of
// each label selector's match expression included: those of the pod spec's
// fields, of the storage's claim templates and of the two selectors of
// PrometheusRule objects (labelSelectorRequired).
// TestMonitoringKinds_RequiredMatchMarkers derives it from the markers of the
// linked module's source.
var thanosRulerRequired = requiredFields(map[string]string{ //nolint:gosec // G101: the keys are field paths (clientSecret is a field's name) and the values their descriptions
	"additionalArgs[].name":                              "the name of the command-line argument",
	"dnsConfig.options[].name":                           "the name of the DNS resolver option",
	"excludedFromEnforcement[].namespace":                "the namespace of the excluded object",
	"excludedFromEnforcement[].resource":                 "the resource of the excluded object: prometheusrules, servicemonitors, podmonitors or probes",
	"hostAliases[].ip":                                   "the IP address of the hosts-file entry",
	"hostAliases[].hostnames":                            "the host names of the hosts-file entry",
	"prometheusRulesExcludedFromEnforce[].ruleNamespace": "the namespace of the excluded PrometheusRule",
	"prometheusRulesExcludedFromEnforce[].ruleName":      "the name of the excluded PrometheusRule",
	"remoteWrite[].url":                                  "the URL of the remote write endpoint",
	"remoteWrite[].azureAd.oauth.clientId":               "the client id of the Azure OAuth application",
	"remoteWrite[].azureAd.oauth.clientSecret":           "the Secret key that holds the Azure OAuth client secret",
	"remoteWrite[].azureAd.oauth.tenantId":               "the tenant id of the Azure OAuth application",
	"remoteWrite[].azureAd.workloadIdentity.clientId":    "the client id of the Azure workload identity",
	"remoteWrite[].azureAd.workloadIdentity.tenantId":    "the tenant id of the Azure workload identity",
	"remoteWrite[].oauth2.clientId":                      "the Secret or ConfigMap key that holds the OAuth2 client id",
	"remoteWrite[].oauth2.clientSecret":                  "the Secret key that holds the OAuth2 client secret",
	"remoteWrite[].oauth2.tokenUrl":                      "the URL tokens are fetched from",
	"updateStrategy.type":                                "RollingUpdate or OnDelete",
}, labelSelectorRequired(append(podSpecLabelSelectors(""),
	"storage.volumeClaimTemplate.spec.selector",
	"storage.ephemeral.volumeClaimTemplate.spec.selector",
	"ruleSelector",
	"ruleNamespaceSelector",
)...))

// thanosRulerExcludedGroup is the group the API gives an entry of
// excludedFromEnforcement that names none, and the one value it allows.
const thanosRulerExcludedGroup = "monitoring.coreos.com"

// withExcludedGroups writes thanosRulerExcludedGroup into each entry of refs
// that names no group. The type writes the field whether or not it was
// authored, so an entry without one would carry an empty group, which the API
// refuses instead of defaulting. An authored empty group never reaches here:
// refuseEmptyExcludedGroups refuses it, so only an omitted (or null) group is
// filled. TestKindComponents_OmittedRequiredAndWrittenDefaults derives the
// field from the type's markers, and TestThanosRulerExcludedGroup_MatchesMarkers
// holds the value to the module's default and enum.
func withExcludedGroups(refs []monitoringv1.ObjectReference) {
	for i := range refs {
		if refs[i].Group == "" {
			refs[i].Group = thanosRulerExcludedGroup
		}
	}
}

// refuseEmptyExcludedGroups refuses an excludedFromEnforcement entry that
// authors its group as the empty string, by the entry's index: the API admits
// only thanosRulerExcludedGroup there, and an invalid authored value is
// refused, not repaired. props is the authored properties' JSON tree
// (jsonProperties, before the null strip), the one tree the decode then reads,
// so a direct caller's typed collections and pointers are seen as the decode
// sees them; the decoded value does not tell an empty group from an omitted
// one. Keys match as the strict decode matches them, case-insensitively; a null
// is an absent value (the null contract), and a value of another shape is left
// to the decode.
func refuseEmptyExcludedGroups(props map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		if !strings.EqualFold(key, "excludedFromEnforcement") {
			continue
		}
		refs, _ := props[key].([]any)
		for i, raw := range refs {
			entry, _ := raw.(map[string]any)
			for _, field := range slices.Sorted(maps.Keys(entry)) {
				if strings.EqualFold(field, "group") && entry[field] == "" {
					return errors.Errorf("%s[%d].%s: empty: the API admits only %s; leave the field out to have it written",
						key, i, field, thanosRulerExcludedGroup)
				}
			}
		}
	}
	return nil
}

// thanosRulerRulesLeft lists, by the property that reaches it, each
// expression rule the API states on a type the spec reaches. None is checked
// here: the linked module ships no CRD, so there is no rule text for the
// validator harness to evaluate. TestMonitoringWorkloadKinds_RulesListed
// derives the list from the markers of the module's source.
var thanosRulerRulesLeft = map[string]string{
	"remoteWrite[].sigv4": "!has(self.externalId) || has(self.roleArn) (externalId can only be used when roleArn is specified)",
	"updateStrategy":      "!(self.type != 'RollingUpdate' && has(self.rollingUpdate)) (rollingUpdate requires type to be RollingUpdate)",
}

// What a ThanosRuler whose spec leaves the field unset runs with. The operator
// fills the memory request before it builds the StatefulSet (makeStatefulSet,
// pkg/thanos/statefulset.go:62-67 at prometheus-operator v0.94.1). It copies
// the replica count into the StatefulSet as it is (makeStatefulSetSpec,
// :507), so an unset one is the StatefulSet's, which the API server defaults
// to 1. Held to the environment policy, never written.
const (
	thanosRulerReplicas              = 1
	thanosRulerOperatorMemoryRequest = "200Mi"
)

// thanosRulerGenerated names the containers the operator generates for the
// pods, by the property that lists patches of them (makeStatefulSetSpec,
// pkg/thanos/statefulset.go:400-419 and :473-494 at prometheus-operator
// v0.94.1). config-reloader is generated wherever the ruler has a rule
// ConfigMap, and the operator makes at least one, with no rule selected
// (makeConfigMapsFromRules, pkg/operator/rules.go:450-469). Of the two only
// thanos-ruler takes its image from the spec; config-reloader runs the image
// of the operator's own configuration unless a listed entry patches it. The
// operator generates no init container and copies initContainers as written
// (statefulset.go:526), so that list has no key.
var thanosRulerGenerated = map[string][]string{
	"containers": {"thanos-ruler", "config-reloader"},
}

// validateThanosRuler refuses what the operator or the API refuses of the
// spec alone, then holds the pods as a workload's (validateMonitoringWorkload).
// What depends on the ThanosRuler's name as well is validateThanosRulerName's.
//
// The operator copies the replica count into the StatefulSet as it is
// (makeStatefulSetSpec, pkg/thanos/statefulset.go:507 at prometheus-operator
// v0.94.1), and the API refuses a StatefulSet of fewer than 0
// (ValidateStatefulSetSpec, k8s.io/kubernetes pkg/apis/apps/validation). It
// uses the first storage arm set, and an emptyDir where storage is unset
// (makeStatefulSet, :92-133).
func validateThanosRuler(spec *monitoringv1.ThanosRulerSpec) error {
	if err := refuseNegativeReplicas(spec.Replicas, "the Prometheus operator copies it into the StatefulSet, which the API refuses with fewer than 0; write 0 or more"); err != nil {
		return err
	}
	if err := validateThanosRulerPortName(spec); err != nil {
		return err
	}
	if err := validateOperatorStorage(spec.Storage); err != nil {
		return err
	}
	return validateMonitoringWorkload(thanosRulerWorkload(spec))
}

// validateThanosRulerPortName refuses a portName the API refuses where the
// operator writes it (validateOperatorPortName): unless listenLocal is set, as
// the name of the thanos-ruler container's web port, beside the port grpc
// (makeStatefulSetSpec, pkg/thanos/statefulset.go:216-232 at
// prometheus-operator v0.94.1); unless serviceName names a Service of the
// author's, as the name and target port of the governing Service's web port,
// beside the port grpc (makeStatefulSetService, :545-583; operator.go:567-574).
//
// The operator writes portName nowhere else: it gives the ruler no probe
// (:216-232 and :545-583 are its only reads). So the container's web port is
// judged as the pods run it, after a thanos-ruler patch's ports are merged
// into the operator's by number (:494, runPorts): a patch that names port
// 10902 otherwise leaves portName out of the container, and one that renames
// the grpc port frees that name. Where the patch renames neither, a portName
// that is not a port name, or is the name another port of the container has,
// is refused; a clash with a port the patch adds is the shared port check's
// (refuseDuplicatePortNames). The Service's half is held whatever the patch
// says, as the Service's ports are the operator's alone.
func validateThanosRulerPortName(spec *monitoringv1.ThanosRulerSpec) error {
	if !spec.ListenLocal && spec.PortName != "" {
		var listed []corev1.ContainerPort
		if i := patchOf(spec.Containers, "thanos-ruler"); i >= 0 {
			listed = spec.Containers[i].Ports
		}
		ports := runPorts(true, thanosRulerPorts(spec)["thanos-ruler"], listed)
		web := slices.IndexFunc(ports, func(p runPort) bool { return p.port.ContainerPort == thanosRulerWebPort })
		if web >= 0 && !strings.HasPrefix(ports[web].from, "ports[") {
			reserved := map[string]string{}
			for i, p := range ports {
				if i != web && !strings.HasPrefix(p.from, "ports[") {
					reserved[p.port.Name] = "a port the Prometheus operator adds to the thanos-ruler container"
				}
			}
			if err := validateOperatorPortName(spec.PortName, true, reserved); err != nil {
				return err
			}
		}
	}
	return validateOperatorPortName(spec.PortName, spec.ServiceName == nil, map[string]string{
		"grpc": "a port of the governing Service the Prometheus operator creates where serviceName is unset",
	})
}

// thanosRulerPorts are the ports the operator gives the containers it
// generates (makeStatefulSetSpec, pkg/thanos/statefulset.go:216-232, and
// CreateConfigReloader, pkg/operator/config_reloader.go:226-282 at
// prometheus-operator v0.94.1): grpc on the thanos-ruler container, beside the
// web port, which, as the config reloader's reloader-web, it adds only unless
// listenLocal is set. Empty, the CRD defaults portName to web.
func thanosRulerPorts(spec *monitoringv1.ThanosRulerSpec) map[string][]corev1.ContainerPort {
	port := func(name string, number int32) corev1.ContainerPort {
		return corev1.ContainerPort{Name: name, ContainerPort: number, Protocol: corev1.ProtocolTCP}
	}
	ports := map[string][]corev1.ContainerPort{"thanos-ruler": {port("grpc", 10901)}}
	if !spec.ListenLocal {
		web := spec.PortName
		if web == "" {
			web = "web"
		}
		ports["thanos-ruler"] = append(ports["thanos-ruler"], port(web, thanosRulerWebPort))
		ports["config-reloader"] = []corev1.ContainerPort{port("reloader-web", 8080)}
	}
	return ports
}

// thanosRulerWebPort is the number of the thanos-ruler container's web port,
// the one portName names (:223-232).
const thanosRulerWebPort = 10902

// thanosRulerSecretKeyVolumes are the volumes the operator adds to the pods
// for a Secret key of the spec, each mounted in the thanos-ruler container
// under /etc/thanos/config/<volume> (mountSecretKey,
// pkg/thanos/statefulset.go:601-626 at prometheus-operator v0.94.1), by
// whether it adds it: remote-write-config always (:243-253), and the others
// where the field is set and the file field that takes precedence over it is
// not (:258-295).
func thanosRulerSecretKeyVolumes(spec *monitoringv1.ThanosRulerSpec) map[string]bool {
	return map[string]bool{
		"remote-write-config": true,
		"query-config":        spec.QueryConfig != nil,
		"alertmanager-config": spec.AlertManagersConfig != nil,
		"objstorage-config":   spec.ObjectStorageConfigFile == nil && spec.ObjectStorageConfig != nil,
		"tracing-config":      spec.TracingConfigFile == "" && spec.TracingConfig != nil,
		"alertrelabel-config": spec.AlertRelabelConfigFile == nil && spec.AlertRelabelConfigs != nil,
	}
}

// thanosRulerRuleFiles is the family of names of the rule ConfigMaps the
// operator generates for the ThanosRuler of the name, and of their volumes:
// thanos-ruler-<name>-rulefiles-<i>, numbered from 0, as many as the selected
// rules need (createOrUpdateRuleConfigMaps, pkg/thanos/rules.go:86-104;
// configMapNameAt, pkg/operator/rules.go:363-365 at prometheus-operator
// v0.94.1). It always makes the first, with no rule selected.
func thanosRulerRuleFiles(name string) string {
	return "thanos-ruler-" + name + "-rulefiles-"
}

// validateThanosRulerName refuses a ThanosRuler name the operator's objects
// cannot be named after, a data volume the pods would not get
// (validateOperatorObjectName), and a listed volume or volume mount the
// operator's own collide with (refuseGeneratedVolumes, refuseGeneratedMounts),
// which the name partly decides. The operator names the data volume
// thanos-ruler-<name>-data (volumeName and prefixedName,
// pkg/thanos/statefulset.go:585-591 at prometheus-operator v0.94.1), and the
// first rule ConfigMap and its volume thanos-ruler-<name>-rulefiles-0
// (thanosRulerRuleFiles), the longer: the API refuses a volume name that is
// not a DNS-1123 label, so a name is at most 38 characters. The StatefulSet,
// thanos-ruler-<name>, gives each pod the hostname
// thanos-ruler-<name>-<ordinal>, which at any replica count the API admits is
// no longer than that volume's name, so it is not checked apart.
func validateThanosRulerName(name, componentName string, spec *monitoringv1.ThanosRulerSpec) error {
	ruleFiles := thanosRulerRuleFiles(name)
	if err := validateOperatorObjectName(operatorObjectName{
		kind:          "ThanosRuler",
		label:         "thanosruler",
		name:          name,
		componentName: componentName,
		dataVolume:    "thanos-ruler-" + name + "-data",
		derived:       []derivedName{{"the Prometheus operator names the volume of the first rule ConfigMap", ruleFiles + "0"}},
		storage:       spec.Storage,
		volumes:       spec.Volumes,
	}); err != nil {
		return err
	}
	if err := refuseGeneratedThanosRulerVolumes(ruleFiles, spec); err != nil {
		return err
	}
	return refuseGeneratedThanosRulerMounts(ruleFiles, spec)
}

// thanosRulerRuleFileNumber matches the numbers the operator gives its rule
// ConfigMaps (%d, from 0).
const thanosRulerRuleFileNumber = "(0|[1-9][0-9]*)$"

// refuseGeneratedThanosRulerVolumes refuses an entry of volumes, or a claim
// template, named as a volume the operator adds to the pods
// (refuseGeneratedVolumes): it appends volumes after its own
// (pkg/thanos/statefulset.go:135 at prometheus-operator v0.94.1). Those are
// the Secret keys' (thanosRulerSecretKeyVolumes), tls-assets (:297), the rule
// ConfigMaps' (:452-469) and web-config, which it adds only for Thanos 0.21.0
// and later (:377-398; volumeName in pkg/webconfig/config.go) and which is
// reserved whatever version names, as no field is held to version. The data
// volume is held by validateOperatorObjectName. The volumes of the web TLS
// credentials, which the operator names after each credential's source, are
// left to the API, as an alertmanager's are.
func refuseGeneratedThanosRulerVolumes(ruleFiles string, spec *monitoringv1.ThanosRulerSpec) error {
	generated := map[string]string{}
	for _, name := range []string{"tls-assets", "web-config"} {
		generated[name] = "a volume the Prometheus operator adds to every ThanosRuler's pods"
	}
	for name, added := range thanosRulerSecretKeyVolumes(spec) {
		if added {
			generated[name] = "a volume the Prometheus operator adds to the pods for a Secret key of the spec"
		}
	}
	return refuseGeneratedVolumes(operatorPodVolumes{
		generated: generated,
		patterns: []reservedPattern{{
			regexp.MustCompile("^" + regexp.QuoteMeta(ruleFiles) + thanosRulerRuleFileNumber),
			"a volume the Prometheus operator adds for a rule ConfigMap it generates, of which it makes as many as the selected rules need",
		}},
		storage: spec.Storage,
		volumes: spec.Volumes,
	})
}

// refuseGeneratedThanosRulerMounts refuses an entry of volumeMounts at a path
// the operator mounts a volume at in the thanos-ruler container
// (refuseGeneratedMounts): it appends volumeMounts to its own mounts
// (pkg/thanos/statefulset.go:471 at prometheus-operator v0.94.1). Those are
// /etc/thanos/config/<volume> for each Secret key's volume
// (thanosRulerSecretKeyVolumes), the TLS assets at /etc/thanos/certs
// (:297-302), the web configuration file (:394; pkg/webconfig/config.go),
// the data volume at /thanos/data (:441-450) and each rule ConfigMap's
// volume at /etc/thanos/rules/<volume> (:452-469).
func refuseGeneratedThanosRulerMounts(ruleFiles string, spec *monitoringv1.ThanosRulerSpec) error {
	generated := map[string]string{}
	for _, p := range []string{"/etc/thanos/certs", "/etc/thanos/web_config/web-config.yaml", "/thanos/data"} {
		generated[p] = "a path the Prometheus operator mounts a volume at in every thanos-ruler container"
	}
	for name, added := range thanosRulerSecretKeyVolumes(spec) {
		if added {
			generated["/etc/thanos/config/"+name] = "a path the Prometheus operator mounts a Secret key of the spec at in the thanos-ruler container"
		}
	}
	return refuseGeneratedMounts(operatorContainerMounts{
		field:     "volumeMounts",
		generated: generated,
		patterns: []reservedPattern{{
			regexp.MustCompile("^" + regexp.QuoteMeta("/etc/thanos/rules/"+ruleFiles) + thanosRulerRuleFileNumber),
			"the path the Prometheus operator mounts the volume of a rule ConfigMap it generates at",
		}},
		mounts: spec.VolumeMounts,
	})
}

// thanosRulerWorkload maps a ThanosRuler spec into the workload the two
// shared functions read. It only reads spec.
//
// Held through it: image, where no patch of the thanos-ruler container names
// one in its place; replicas, or where unset the StatefulSet's 1;
// storage; resources, the thanos-ruler container's as the operator runs it:
// an unset memory request filled as its 200Mi, then the requests and limits
// of a listed thanos-ruler entry merged over it, key by key, so a request the
// entry names replaces the 200Mi, and the entry's block is not checked alone
// (makeStatefulSet and makeStatefulSetSpec, pkg/thanos/statefulset.go:62-67,
// :481 and :494 at prometheus-operator v0.94.1); the images of the two containers the
// operator generates, where the spec leaves them to it; the deprecated
// bearerToken of each remoteWrite entry, the one credential the spec holds in
// the clear; and, as pod fields, containers, initContainers, volumes and
// securityContext. The spec has no hostNetwork, hostPID or hostIPC field.
// TestMonitoringWorkloadKinds_PodFieldsHeldOrListed and
// TestMonitoringWorkloadKinds_CredentialsHeldOrListed derive both claims from
// the type.
func thanosRulerWorkload(spec *monitoringv1.ThanosRulerSpec) monitoringWorkload {
	replicas := int64(thanosRulerReplicas)
	if spec.Replicas != nil {
		replicas = int64(*spec.Replicas)
	}
	resources := fieldResources{"resources", spec.Resources}
	var merged map[string]string
	if i := patchOf(spec.Containers, "thanos-ruler"); i >= 0 {
		patch := spec.Containers[i].Resources
		if len(patch.Requests) > 0 || len(patch.Limits) > 0 {
			resources = fieldResources{
				fmt.Sprintf("resources with containers[%d] %q merged over it", i, "thanos-ruler"),
				mergedResources(spec.Resources, patch),
			}
			merged = map[string]string{"containers": "thanos-ruler"}
		}
	}
	w := monitoringWorkload{
		pod: corev1.PodSpec{
			InitContainers:  spec.InitContainers,
			Containers:      spec.Containers,
			Volumes:         spec.Volumes,
			SecurityContext: spec.SecurityContext,
		},
		generated:      thanosRulerGenerated,
		generatedPorts: thanosRulerPorts(spec),
		serviceName:    spec.ServiceName,
		replicas:       &replicas,
		replicasPath:   "replicas",
		storage:        spec.Storage,
		resources:      []fieldResources{resources},
		mergedPatches:  merged,
		memoryRequests: map[string]string{resources.path: thanosRulerOperatorMemoryRequest},
	}
	// The operator copies both into the pod template (:539-540).
	if spec.DNSPolicy != nil {
		w.pod.DNSPolicy = corev1.DNSPolicy(*spec.DNSPolicy)
	}
	if spec.DNSConfig != nil {
		w.pod.DNSConfig = &corev1.PodDNSConfig{Nameservers: spec.DNSConfig.Nameservers}
	}
	// The image a patch names replaces image in the container the operator
	// builds from it (:494), so image is then not run, and not held: the
	// patch's own is, as a listed container's image.
	switch {
	case patchedImage(spec.Containers, "thanos-ruler") != "":
	case spec.Image != "":
		w.images = []fieldValue{{"image", spec.Image}}
	default:
		w.unsetImages = []string{"image"}
	}
	if patchedImage(spec.Containers, "config-reloader") == "" {
		w.unsetImages = append(w.unsetImages, "the image of the config-reloader container (containers)")
	}
	for i, rw := range spec.RemoteWrite {
		if rw.BearerToken != "" {
			w.literals = append(w.literals, fmt.Sprintf("remoteWrite[%d].bearerToken", i))
		}
	}
	return w
}

// thanosRulerExternalLabelsProperty is the property that sets the spec's
// `labels`: see thanosRulerExternalLabels.
const thanosRulerExternalLabelsProperty = "externalLabels"

// thanosRulerExternalLabels is what the externalLabels property decodes into,
// under the same strict decode and null contract as the spec (decodeKindSpec),
// so that an error names the property as authored.
//
// The one renamed field of the kind: the engine reads a kind component's
// `labels` property as the labels of the object's metadata
// (oam.ObjectLabelsProperty) before the handler sees it, so the spec's own
// `labels`, the Prometheus external labels, cannot be authored under its json
// name. The rename is the kind's alone; the engine's `labels` keeps its
// meaning. TestCoreKindSchemas_CoverSpec records the mapping.
type thanosRulerExternalLabels struct {
	ExternalLabels map[string]string `json:"externalLabels,omitempty"`
}

// ToApplicationConfig decodes an OAM thanosruler component into its config.
// The object takes the namespace of the application it is generated in.
//
// externalLabels is taken out of the properties and decoded on its own, and
// the rest is the kind's config; the decoded labels are then the spec's
// `labels`. A property that names the spec's `labels` in another spelling
// (`Labels`) is refused: the strict decode matches field names
// case-insensitively, so it would reach that field unpublished.
func (h *ThanosRulerHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	for _, key := range slices.Sorted(maps.Keys(component.Properties)) {
		if strings.EqualFold(key, oam.ObjectLabelsProperty) {
			return nil, errors.Errorf("%s: not a thanosruler property: the ThanosRuler's external labels are the %s property, and the object's own labels the %s property, which the engine reads",
				key, thanosRulerExternalLabelsProperty, oam.ObjectLabelsProperty)
		}
	}
	// One serialization for every read below: the empty-group refusal, the
	// externalLabels decode and the spec's decode read the same JSON, so a value
	// whose encoder answers differently on another call cannot pass the refusal
	// in one form and reach the decode in another.
	_, props, err := jsonProperties(component.Properties)
	if err != nil {
		return nil, err
	}
	if err := refuseEmptyExcludedGroups(props); err != nil {
		return nil, err
	}
	raw, authored := props[thanosRulerExternalLabelsProperty]
	rest := *component
	rest.Properties = maps.Clone(props)
	delete(rest.Properties, thanosRulerExternalLabelsProperty)
	cfg, err := thanosRulerKind.config(&rest)
	if err != nil || !authored {
		return cfg, err
	}
	labels, _, err := decodeKindSpec[thanosRulerExternalLabels](map[string]any{thanosRulerExternalLabelsProperty: raw}, thanosRulerKind.upstream)
	if err != nil {
		return nil, err
	}
	held, ok := cfg.(*policyHeldKindConfig[monitoringv1.ThanosRulerSpec])
	if !ok {
		return nil, errors.Errorf("internal: a thanosruler config is a %T", cfg)
	}
	held.decoded.Labels = labels.ExternalLabels
	return held, nil
}
