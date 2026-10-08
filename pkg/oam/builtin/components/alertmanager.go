package components

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
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
		"podMetadata":     object("podMetadata: the labels and annotations the operator copies onto the Alertmanager pods. A key the consumer reserves is refused here as on a workload's pod template. Nothing is added: the pods carry the component label only if it is written here with the component's own value, and without it the NetworkPolicies generated for the component do not select them. The operator sets five labels and one annotation of its own; a value authored here replaces its app.kubernetes.io/version label, and not the other four labels or the annotation." + decoded + "EmbeddedObjectMetadata in its API reference."),
		"image":           text("image: the full image reference of the alertmanager container, with a tag other than latest or a digest. Held to the EnvironmentPolicy's allowed registries. An image an entry of containers named alertmanager names replaces it: that one runs and is held, and this one is not. Unset or empty, the image is the one an entry of containers named alertmanager names, and where none does the operator chooses the one that runs: refused under a policy with allowed registries, which cannot hold that choice, and built under one without. Where this or an entry of containers named alertmanager names the image, version is required: the operator chooses the container's flags by it."),
		"imagePullPolicy": text("imagePullPolicy: when the images of the alertmanager, config-reloader and init-config-reloader containers are pulled: Always, Never or IfNotPresent."),
		"version":         text("version: the Alertmanager version the operator configures for, such as v0.28.1: it chooses the alertmanager container's flags by it, and by its own default where it is unset. Required where image, or an entry of containers named alertmanager, names the image; name the version that image runs. A version the operator cannot parse, one under 0.15.0 and one of a major version above 0 are refused: the operator fails to build the pods for them."),
		"imagePullSecrets": objects("imagePullSecrets: the Secrets of the Alertmanager's namespace that hold the credentials the images are pulled with.",
			"One reference: name."),
		"secrets": texts("secrets: the Secrets of the Alertmanager's namespace mounted into the alertmanager container, each under /etc/alertmanager/secrets/<name>. An entry whose volume name, secret-<name> cut to 63 characters, ends in - is refused: the operator fails to build the pods.",
			"The name of a Secret."),
		"configMaps": texts("configMaps: the ConfigMaps of the Alertmanager's namespace mounted into the alertmanager container, each under /etc/alertmanager/configmaps/<name>. An entry whose volume name, configmap-<name> cut to 63 characters, ends in - is refused: the operator fails to build the pods.",
			"The name of a ConfigMap."),
		"configSecret": text("configSecret: the name of the Secret, in the Alertmanager's namespace, that holds the Alertmanager configuration under the key alertmanager.yaml. Unset, alertmanager-<name of the Alertmanager>. Where the Secret or the key is missing the operator provisions a configuration that drops every notification."),
		"logLevel":     text("logLevel: the log level of Alertmanager: debug, info, warn or error."),
		"logFormat":    text("logFormat: the log format of Alertmanager: logfmt or json."),
		"replicas":     number("replicas: the number of Alertmanager pods; two or more run in high-availability mode. A negative one is refused: the operator runs 0 for it. Held to the EnvironmentPolicy's replica maximum. Unset, the operator runs 1, which is held to that maximum; nothing is written, and no replica default of the policy is applied."),
		"retention":    text("retention: how long Alertmanager keeps its data, as whole hours, minutes, seconds and milliseconds in that order, such as 120h or 1h30m. Unset, the API fills 120h; an empty one is refused, since the API server would replace it, and so is one of 0 or less, which the operator ignores. The kind leaves to the API what the CRD's own schema refuses when the Alertmanager is applied, such as a retention of 1.5h or 1d, which shows at once; it refuses what the CRD admits but the operator or the API then refuses on the StatefulSet or the pods, which would fail late and out of sight."),
		"storage":      object("storage: where the Alertmanager pods keep their data: emptyDir, ephemeral or volumeClaimTemplate, in that order of precedence; the operator uses the first that is set. A claim template's name beside emptyDir or ephemeral is refused unless it is alertmanager-<name>-db: the operator mounts the data volume under it and creates it under that name. On the volumeClaimTemplate arm, a claim template's name must be a DNS-1123 label, which the operator names the data volume with, and not the name of a volume the operator adds. A claim of the arm in use, ephemeral or volumeClaimTemplate, must request storage above 0 (spec.resources.requests.storage), and an ephemeral one name its access modes; unset or empty access modes of volumeClaimTemplate are ReadWriteOnce. An emptyDir claims nothing. Unset, the storage is the operator's to decide: no storage default of the policy is applied. The storage the claim template of the arm in use requests is held to the EnvironmentPolicy's storage maximum; a claim template of an arm after it is not, nor the size limit of an emptyDir. A key the consumer reserves is refused in the labels and annotations of volumeClaimTemplate, which the operator copies onto its StatefulSet's volume claim template; those of the ephemeral arm's claim template are not read. No claim template takes a component label." + decoded + "StorageSpec in its API reference."),
		"volumes": objects("volumes: further volumes of the Alertmanager pods, beside the ones the operator generates. A volume named as one of those (config-volume, tls-assets, config-out, web-config, cluster-tls-config, the secrets, configMaps and templates volumes, the data volume) is refused; web-config and cluster-tls-config whatever version names. Two entries of secrets, or of configMaps, whose volumes the operator gives one name are refused as well. The TLS credentials' volumes, whose names the operator hashes, are left to the API. Held to the EnvironmentPolicy as a pod's volumes are: hostPath, the storage a generic ephemeral volume's claim requests, the registry of an image volume.",
			"One volume."+core+"Volume in the Kubernetes API reference."),
		"volumeMounts": objects("volumeMounts: further volume mounts of the alertmanager container. A mount path the operator mounts a volume at is refused: /alertmanager, /etc/alertmanager/config, config_out and certs, the web and cluster TLS configuration files whatever version names, /etc/alertmanager/templates where alertmanagerConfiguration.templates is set, and /etc/alertmanager/secrets/<name> and configmaps/<name> of each entry of secrets and configMaps. The mounts of the TLS credentials are left to the API.",
			"One volume mount."+core+"VolumeMount in the Kubernetes API reference."),
		"persistentVolumeClaimRetentionPolicy": object("persistentVolumeClaimRetentionPolicy: whether the claims of the StatefulSet are deleted when it is deleted (whenDeleted) or scaled down (whenScaled): Retain, the default, or Delete." + core + "StatefulSetPersistentVolumeClaimRetentionPolicy in the Kubernetes API reference."),
		"externalUrl":                          text("externalUrl: the URL under which the Alertmanager web service is reached from outside, which the links in its notifications are built from. The Prometheus operator passes it to Alertmanager unchanged, which exits at startup on one it cannot parse, and from v0.19.0 on one not of scheme http or https: both are refused, the second where version is unset or v0.19.0 or later, its prereleases included."),
		"routePrefix":                          text("routePrefix: the path prefix Alertmanager registers its HTTP handlers under."),
		"paused":                               flag("paused: true stops the operator from acting on the objects it manages for this Alertmanager, deletion excepted."),
		"nodeSelector":                         object("nodeSelector: the node labels a node must carry for the pods to be scheduled on it."),
		"schedulerName":                        text("schedulerName: the scheduler that places the pods. Unset, the default scheduler. Not empty."),
		"resources":                            object("resources: the resource requests and limits of the alertmanager container. Its cpu and memory are held to the EnvironmentPolicy's maxima, and a request may not exceed its limit. Without a memory request the operator requests 200Mi, whatever the limit, which is held to the memory maximum, and a memory limit under it is refused; nothing is written, and no resource default of the policy is applied. A containers entry named alertmanager is merged over this block key by key, and the checks hold the merged block, so a memory request it names replaces the 200Mi. A negative request or limit is refused, here and in a listed container." + core + "ResourceRequirements in the Kubernetes API reference."),
		"affinity":                             object("affinity: the scheduling constraints of the pods." + core + "Affinity in the Kubernetes API reference."),
		"tolerations": objects("tolerations: the taints the pods tolerate.",
			"One toleration."+core+"Toleration in the Kubernetes API reference."),
		"topologySpreadConstraints": objects("topologySpreadConstraints: how the pods are spread over topology domains.",
			"One constraint."+core+"TopologySpreadConstraint in the Kubernetes API reference."),
		"securityContext":     object("securityContext: the pod-level security attributes of the pods. Its windowsOptions.hostProcess is refused under an EnvironmentPolicy that does not allow privileged containers, and without hostNetwork: true, which the API requires of a HostProcess pod." + core + "PodSecurityContext in the Kubernetes API reference."),
		"dnsPolicy":           text("dnsPolicy: the DNS policy of the pods: ClusterFirstWithHostNet, ClusterFirst, Default or None. None is refused without dnsConfig.nameservers, which the API then requires."),
		"dnsConfig":           object("dnsConfig: the DNS configuration of the pods: nameservers, searches and options." + decoded + "PodDNSConfig in its API reference."),
		"enableServiceLinks":  flag("enableServiceLinks: whether the Services of the namespace are injected into the pods' environment variables."),
		"serviceName":         text("serviceName: the name of the governing Service of the StatefulSet, which must exist in the namespace and select the pods. Unset, the operator creates and manages a headless Service named alertmanager-operated. Not empty."),
		"serviceAccountName":  text("serviceAccountName: the ServiceAccount the pods run as. Launcher creates none for it and does not check that it exists."),
		"listenLocal":         flag("listenLocal: true makes the Alertmanager web server listen on loopback only, not on the pod's address; the gossip port is not affected."),
		"podManagementPolicy": text("podManagementPolicy: how the StatefulSet creates and deletes pods when it scales: Parallel, the operator's default, or OrderedReady. Changing it recreates the StatefulSet."),
		"updateStrategy":      object("updateStrategy: how the StatefulSet replaces its pods on a change: type (RollingUpdate, the default, or OnDelete) and rollingUpdate with maxUnavailable. The API refuses rollingUpdate with another type than RollingUpdate; launcher does not check that rule." + decoded + "StatefulSetUpdateStrategy in its API reference."),
		"containers": objects("containers: further containers of the pods, and patches of the ones the operator generates: an entry that shares its name with a container the operator generates (alertmanager, config-reloader) is merged into it. Each is held to the EnvironmentPolicy as a pod's containers are: the registry of an authored image, cpu and memory maxima, privilege and capabilities. A patch may name no image; any other entry must name one. A name may be listed once: the operator runs only the last entry of a name. A name of an init container, generated (init-config-reloader) or listed, is refused. A patch's port named as one the operator gives that container (the web port under portName, mesh-tcp, mesh-udp, reloader-web) at another number is refused: it is added beside it, unless the patch renames that port with one at its number. Under a policy with allowed registries, config-reloader must be patched with an image from one of them: unpatched, it runs the image of the operator's own configuration, which the allowlist cannot hold.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"initContainers": objects("initContainers: further init containers of the pods, and patches of the one the operator generates (init-config-reloader). Held to the EnvironmentPolicy as containers are, a name listed once as there and never a container's (alertmanager, config-reloader or a listed one), a patch's port as there (reloader-init), and, under a policy with allowed registries, init-config-reloader must be patched with an image from one of them, as config-reloader must.",
			"One container."+core+"Container in the Kubernetes API reference."),
		"priorityClassName": text("priorityClassName: the priority class of the pods."),
		"additionalPeers": texts("additionalPeers: further Alertmanager instances to form a high-availability cluster with, outside this object.",
			"The address of one peer."),
		"clusterAdvertiseAddress":             text("clusterAdvertiseAddress: the address advertised to the cluster's peers; needed where the pod's address is not a private one."),
		"clusterGossipInterval":               text("clusterGossipInterval: the interval between gossip attempts, as a Go duration. Not 0 or less, which the operator ignores."),
		"clusterLabel":                        text("clusterLabel: the identifier of the Alertmanager cluster; set only when the cluster includes instances outside this object."),
		"clusterPushpullInterval":             text("clusterPushpullInterval: the interval between push-pull attempts, as a Go duration. Not 0 or less, which the operator ignores."),
		"clusterPeerTimeout":                  text("clusterPeerTimeout: the timeout of cluster peering, as a Go duration. Not 0 or less, which the operator ignores."),
		"clusterPeerName":                     text("clusterPeerName: the name this instance advertises to its peers; may refer to environment variables of the alertmanager container, as $(POD_NAME). Unset, the pod's name. Requires Alertmanager v0.30.0 or later. Not empty."),
		"portName":                            text("portName: the name of the web port on the pods and the governing Service. Unset, the API fills web; an empty one is refused, since the API server would replace it. A name the API refuses for a port (not an IANA service name: at most 15 characters, lower-case letters, digits and -) is refused where the operator writes it; so are mesh-tcp and mesh-udp, the container ports the operator adds beside it, unless listenLocal is set, and tcp-mesh and udp-mesh, the ports of the governing Service, unless serviceName is set."),
		"forceEnableClusterMode":              flag("forceEnableClusterMode: true keeps the cluster mode on with a single replica, for a cluster that spans several Kubernetes clusters."),
		"alertmanagerConfigSelector":          object("alertmanagerConfigSelector: the AlertmanagerConfig objects merged into this Alertmanager's configuration, by their labels. A Kubernetes label selector: matchLabels and matchExpressions."),
		"alertmanagerConfigNamespaceSelector": object("alertmanagerConfigNamespaceSelector: the namespaces AlertmanagerConfig objects are read from, by their labels. Unset, the Alertmanager's own namespace only. A Kubernetes label selector: matchLabels and matchExpressions."),
		"alertmanagerConfigMatcherStrategy":   object("alertmanagerConfigMatcherStrategy: which alerts the routes and inhibition rules of an AlertmanagerConfig object process, as type: OnNamespace (the default: the alerts whose namespace label is the object's namespace), OnNamespaceExceptForAlertmanagerNamespace or None (all alerts); an empty type is refused, since the API server would replace it." + decoded + "AlertmanagerConfigMatcherStrategy in its API reference."),
		"minReadySeconds":                     number("minReadySeconds: how many seconds a new pod must be ready before it counts as available. At least 0."),
		"hostAliases": objects("hostAliases: further entries of the pods' hosts file.",
			"One entry: ip and hostnames, both required."),
		"hostNetwork":                  flag("hostNetwork: true runs the pods in the node's network namespace. Refused under an EnvironmentPolicy that does not allow the host network."),
		"web":                          object("web: the web server's settings: tlsConfig and httpConfig, getConcurrency and timeout. A tlsConfig the operator refuses is refused: one without a certificate or a key, or naming one twice." + decoded + "AlertmanagerWebSpec in its API reference."),
		"limits":                       object("limits: the limits Alertmanager is started with: maxSilences and maxPerSilenceBytes. Requires Alertmanager v0.28.0 or later." + decoded + "AlertmanagerLimitsSpec in its API reference."),
		"clusterTLS":                   object("clusterTLS: the mutual TLS configuration of the gossip protocol: server and client, both required. Requires Alertmanager v0.24.0 or later. A server or client the operator refuses is refused: a server without a certificate or a key, a client without a certificate, or either naming one twice." + decoded + "ClusterTLSConfig in its API reference."),
		"alertmanagerConfiguration":    object("alertmanagerConfiguration: the Alertmanager configuration, taken from the AlertmanagerConfig object `name` names in the same namespace, with global parameters and notification templates; it takes precedence over configSecret. A template whose key an earlier one names is refused: the operator skips it. Experimental upstream. Every credential in it is the key of a Secret." + decoded + "AlertmanagerConfiguration in its API reference."),
		"automountServiceAccountToken": flag("automountServiceAccountToken: whether a service account token is mounted into the pods."),
		"enableFeatures": texts("enableFeatures: the Alertmanager feature flags to enable. Requires Alertmanager v0.27.0 or later.",
			"The name of one feature flag."),
		"additionalArgs": objects("additionalArgs: further command-line arguments of the alertmanager container, passed as they are. An argument naming a flag the operator generates for the spec and version, or its negation with no-, is refused: the operator then fails to build the pods. Without a version, the flags of the operator's default version are held, which is every such flag. Beyond that name launcher does not read them: an argument can change what the fields above configure.",
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
// The fields on which an authored 0 or "" cannot be carried are the ones of a
// pod spec's containers and volumes (podSpecDefaultedZeros): the spec lists
// containers, init containers and volumes of the Kubernetes types, and copies
// hostNetwork to the pods, so a listed container port's hostPort is among them
// when it is true. Five strings of the operator's own types cannot be carried
// empty either (alertmanagerDefaultedZeroFields).
// TestMonitoringWorkloadKinds_DefaultedZeros derives the list
// (monitoringWorkloadDefaultedZeros). The refusal holds for an entry that
// patches a container the operator generates too: the zero is omitted there as
// anywhere, and the operator's value stays.
var alertmanagerKind = &policyHeldKind[monitoringv1.AlertmanagerSpec]{
	policyFreeKind: policyFreeKind[monitoringv1.AlertmanagerSpec]{
		upstream: "monitoring.coreos.com/v1 AlertmanagerSpec",
		required: alertmanagerRequired,
		defaultedZerosFor: func(spec *monitoringv1.AlertmanagerSpec) defaultedZeroFields {
			return monitoringWorkloadDefaultedZeros(&corev1.PodSpec{HostNetwork: spec.HostNetwork}, alertmanagerDefaultedZeroFields)
		},
		validate:     validateAlertmanager,
		validateName: validateAlertmanagerName,
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

// alertmanagerDefaultedZeroFields are the fields of the operator's own types
// in the alertmanager kind's defaulted-zero list
// (monitoringWorkloadDefaultedZeros): three to which the CRD gives another
// default, and two the operator copies to a pod field the API server defaults
// (alertmanager/statefulset.go at prometheus-operator v0.94.1): schedulerName
// to the pod's (:866), and imagePullPolicy to its alertmanager container's
// (:757) and to both config reloaders' (:813, :834).
var alertmanagerDefaultedZeroFields = map[string]string{
	"portName":                               `"web"`,
	"retention":                              `"120h"`,
	"alertmanagerConfigMatcherStrategy.type": `"OnNamespace"`,
	"schedulerName":                          `"default-scheduler"`,
	"imagePullPolicy":                        imagePullPolicyDefault,
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
// expression rule the API states on a type the spec reaches, as its
// expression and its message. None is checked here: the linked module ships
// no CRD, so there is no rule text for the validator harness to evaluate.
// TestMonitoringWorkloadKinds_RulesListed derives the list from the markers of
// the module's source.
var alertmanagerRulesLeft = map[string]string{
	"updateStrategy": "!(self.type != 'RollingUpdate' && has(self.rollingUpdate)) (rollingUpdate requires type to be RollingUpdate)",
}

// validateAlertmanager refuses, with or without an environment policy, the
// three deprecated fields that name the image in parts, a duration the
// operator discards, an externalUrl Alertmanager exits on at startup
// (alertmanagerURLSchemes), and what validateMonitoringWorkload refuses of the
// workload.
//
// baseImage, tag and sha are refused when not empty: the operator composes
// the image from them, and from version, in code that is not in the linked
// module, so the kind cannot say which image runs and has nothing to hold to
// the allowed registries or the tag rule. An authored empty string is the same
// object as none, and is left out as the type leaves it out.
//
// retention and the three durations of the cluster are refused where they
// parse as a Go duration of 0 or less: the operator empties such a value
// before it builds the StatefulSet and reports the field as ignored, so the
// pods run with the value an unset one gets, not the authored one
// (discardZeroDurations, pkg/alertmanager/statefulset.go:81-115 at
// prometheus-operator v0.94.1). A value that does not parse is left to the
// API's own pattern, as the operator leaves it.
func validateAlertmanager(spec *monitoringv1.AlertmanagerSpec) error {
	for _, field := range []fieldValue{{"baseImage", spec.BaseImage}, {"sha", spec.SHA}, {"tag", spec.Tag}} {
		if field.value != "" {
			return errors.Errorf("%s: not authorable: the Prometheus operator deprecates the field, and composes the image it yields outside what the object states; use image", field.path)
		}
	}
	for _, field := range []fieldValue{
		{"retention", string(spec.Retention)},
		{"clusterGossipInterval", string(spec.ClusterGossipInterval)},
		{"clusterPushpullInterval", string(spec.ClusterPushpullInterval)},
		{"clusterPeerTimeout", string(spec.ClusterPeerTimeout)},
	} {
		if d, err := time.ParseDuration(field.value); field.value != "" && err == nil && d <= 0 {
			return errors.Errorf("%s: %q is not a positive duration: the Prometheus operator ignores it and runs the pods as if the field were unset; name a positive one, or leave it unset", field.path, field.value)
		}
	}
	if spec.Replicas != nil && *spec.Replicas < 0 {
		return errors.Errorf("replicas: %d is below 0: the Prometheus operator runs 0 replicas for it; write 0", *spec.Replicas)
	}
	if err := refuseUnservableExternalURL("Alertmanager", spec.ExternalURL, alertmanagerURLSchemes(spec)); err != nil {
		return err
	}
	if err := validateAlertmanagerPortName(spec); err != nil {
		return err
	}
	if err := validateAlertmanagerStorage(spec.Storage); err != nil {
		return err
	}
	if err := refuseGeneratedAlertmanagerVolumes(spec); err != nil {
		return err
	}
	if err := refuseGeneratedAlertmanagerMounts(spec); err != nil {
		return err
	}
	if err := refuseGeneratedAlertmanagerArgs(spec); err != nil {
		return err
	}
	if err := refuseDuplicateAlertmanagerTemplateKeys(spec); err != nil {
		return err
	}
	if err := validateAlertmanagerTLS(spec); err != nil {
		return err
	}
	if err := validateMonitoringWorkload(alertmanagerWorkload(spec)); err != nil {
		return err
	}
	return validateAlertmanagerVersion(spec)
}

// alertmanagerURLSchemes returns the schemes of an externalUrl Alertmanager
// starts with: http and https from v0.19.0, which refuses any other and exits
// (cmd/alertmanager/main.go:386-389 and 621-622 at v0.28.1, app/url.go:46-47
// at v0.34.0), and its prerelease v0.19.0-rc.0 already does
// (cmd/alertmanager/main.go:542-543 there), so the bound is the least
// prerelease of v0.19.0; any before it, which only parses the URL
// (cmd/alertmanager/main.go:292-296 and 440-443 at v0.15.0). Unset, the
// version is the operator's default (v0.34.0 at v0.94.1), and where it does
// not parse validateAlertmanagerVersion refuses it, so both are held to the
// two schemes, as refuseGeneratedAlertmanagerArgs holds their flags.
func alertmanagerURLSchemes(spec *monitoringv1.AlertmanagerSpec) []string {
	if version, err := semver.ParseTolerant(spec.Version); spec.Version != "" && err == nil && version.LT(semver.MustParse("0.19.0-0")) {
		return nil
	}
	return []string{"http", "https"}
}

// validateAlertmanagerVersion refuses a version the operator cannot build the
// pods for, and an image named without one. The operator parses version, or
// its own default where it is unset, with semver.ParseTolerant, fails the
// reconcile where that fails, and refuses a version under 0.15.0 or of a
// major version above 0 (pkg/alertmanager/statefulset.go:284 and
// operator.go:902-909 at v0.94.1). It chooses the alertmanager container's
// flags by that version, and by its own default where it is unset. That default
// is the deployed operator's, which a build cannot know, so an image named without
// a version may be run with the flags of another.
func validateAlertmanagerVersion(spec *monitoringv1.AlertmanagerSpec) error {
	if spec.Version == "" {
		if (spec.Image != nil && *spec.Image != "") || patchedImage(spec.Containers, "alertmanager") != "" {
			return errors.New("version: required where image, or an entry of containers named alertmanager, names the image: the Prometheus operator chooses the flags of the alertmanager container by the version named here, and by the deployed operator's default where none is, which need not be the version the image runs; name the version of the image")
		}
		return nil
	}
	version, err := semver.ParseTolerant(spec.Version)
	if err != nil {
		return errors.Errorf("version: %q is not a version the Prometheus operator can parse (%v), and it fails to build the pods; name one such as v0.28.1", spec.Version, err)
	}
	if version.LT(semver.MustParse("0.15.0")) || version.Major > 0 {
		return errors.Errorf("version: %q is not supported by the Prometheus operator, which runs Alertmanager 0.15.0 and later of major version 0; name one such as v0.28.1", spec.Version)
	}
	return nil
}

// validateAlertmanagerPortName refuses a portName the API refuses where the
// operator writes it (pkg/alertmanager/statefulset.go at v0.94.1): unless
// listenLocal is set, as the name of the alertmanager container's web port,
// beside the ports mesh-tcp and mesh-udp (:483-502); unless serviceName names
// a Service of the author's, as the name and target port of the governing
// Service's web port, beside the ports tcp-mesh and udp-mesh (:223-250,
// operator.go:660). A container port's name and a target port name must be an
// IANA service name, and port names are unique within a container and within a
// Service. Empty, the CRD defaults it to web.
func validateAlertmanagerPortName(spec *monitoringv1.AlertmanagerSpec) error {
	name := spec.PortName
	containerPort, servicePort := !spec.ListenLocal, spec.ServiceName == nil
	if name == "" || (!containerPort && !servicePort) {
		return nil
	}
	if errs := validation.IsValidPortName(name); len(errs) > 0 {
		return errors.Errorf("portName: %q is not a valid port name: %s; the Prometheus operator names the web port with it, which the API then refuses", name, strings.Join(errs, "; "))
	}
	if containerPort && (name == "mesh-tcp" || name == "mesh-udp") {
		return errors.Errorf("portName: %q is the name of a port the Prometheus operator adds to the alertmanager container, and the API refuses a port name twice; name the web port otherwise", name)
	}
	if servicePort && (name == "tcp-mesh" || name == "udp-mesh") {
		return errors.Errorf("portName: %q is the name of a port of the governing Service the Prometheus operator creates where serviceName is unset, and the API refuses a port name twice; name the web port otherwise", name)
	}
	return nil
}

// validateAlertmanagerStorage refuses a storage the operator builds into a
// volume claim the API refuses. The operator uses the first arm set of
// emptyDir, ephemeral and volumeClaimTemplate (pkg/alertmanager/statefulset.go:
// 174-212 at v0.94.1):
//   - an ephemeral volume's claim template is used as written, and the API
//     refuses one without access modes or a storage request;
//   - for the claim template arm, which a storage with no arm set selects, it
//     defaults access modes to ReadWriteOnce where none are written (an empty
//     list is not serialized, so it is none) and copies the resources as
//     written, so a claim without a storage request is refused when the
//     StatefulSet controller creates it.
//
// The claim template's name, which the operator mounts the data volume under
// whatever arm is in use, is held by validateAlertmanagerName, which knows the
// name the operator gives the volume.
func validateAlertmanagerStorage(s *monitoringv1.StorageSpec) error {
	if s == nil || s.EmptyDir != nil {
		return nil
	}
	if s.Ephemeral != nil {
		t := s.Ephemeral.VolumeClaimTemplate
		if t == nil {
			return errors.New("storage.ephemeral.volumeClaimTemplate: required: the API refuses an ephemeral volume without one")
		}
		if len(t.Spec.AccessModes) == 0 {
			return errors.New("storage.ephemeral.volumeClaimTemplate.spec.accessModes: required: the API refuses an ephemeral volume's claim without access modes")
		}
		q, ok := t.Spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			return errors.New("storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage: required: the API refuses an ephemeral volume's claim without a storage request")
		}
		return positiveAlertmanagerStorage("storage.ephemeral.volumeClaimTemplate", q)
	}
	q, ok := s.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage]
	if !ok {
		return errors.New("storage.volumeClaimTemplate.spec.resources.requests.storage: required where neither storage.emptyDir nor storage.ephemeral is set: the Prometheus operator then claims the data volume from this template as written, and the API refuses a claim without a storage request")
	}
	return positiveAlertmanagerStorage("storage.volumeClaimTemplate", q)
}

// positiveAlertmanagerStorage refuses the storage request of a claim template
// in use that is not above 0: the operator passes it on as written, and the API
// refuses a claim whose storage request is not positive
// (ValidatePersistentVolumeClaimSpec, k8s.io/kubernetes
// pkg/apis/core/validation/validation.go).
func positiveAlertmanagerStorage(template string, q resource.Quantity) error {
	if q.Sign() <= 0 {
		return errors.Errorf("%s.spec.resources.requests.storage: %s is not above 0: the Prometheus operator claims the data volume with it as written, and the API refuses a claim whose storage request is not positive; request more", template, q.String())
	}
	return nil
}

// alertmanagerGeneratedVolumes are the volumes the Prometheus operator adds to
// an Alertmanager's pods under a fixed name: its configuration, its TLS
// assets, the configuration its reloader writes, the web configuration and the
// cluster TLS configuration (makeStatefulSetSpec,
// pkg/alertmanager/statefulset.go:510-529, :698-738; volumeName in
// pkg/webconfig/config.go and pkg/alertmanager/clustertlsconfig/config.go at
// prometheus-operator v0.94.1). The operator adds web-config only for
// Alertmanager 0.22.0 and later, and cluster-tls-config only for 0.24.0 and
// later; both are reserved whatever version names, as no field is held to
// version.
var alertmanagerGeneratedVolumes = []string{"config-volume", "tls-assets", "config-out", "web-config", "cluster-tls-config"}

// invalidDNS1123Characters is what the operator replaces in a name it derives
// a volume's from (pkg/k8s/resource_namer.go at v0.94.1).
var invalidDNS1123Characters = regexp.MustCompile("[^-a-z0-9]+")

// alertmanagerSourceVolume is the name the operator gives the volume of an
// entry of secrets or configMaps: the prefix and the entry, lower-cased, each
// run of other characters than a-z, 0-9 and - replaced by -, trimmed of - and
// cut to 63 characters (ResourceNamer.DNS1123Label, pkg/k8s/resource_namer.go
// at v0.94.1).
func alertmanagerSourceVolume(prefix, entry string) string {
	name := strings.Trim(invalidDNS1123Characters.ReplaceAllString(strings.ToLower(prefix+"-"+entry), "-"), "-")
	if len(name) > validation.DNS1123LabelMaxLength {
		name = name[:validation.DNS1123LabelMaxLength]
	}
	return name
}

// refuseGeneratedAlertmanagerVolumes refuses an entry of volumes named as a
// volume the operator adds to the pods: it appends volumes after its own
// (statefulset.go:215 at v0.94.1), and the API refuses a pod with two volumes
// of one name. The data volume, named after the Alertmanager, is held by
// validateAlertmanagerName. The volumes of the web and cluster TLS
// credentials are not: the operator names each after the credential's source
// with a hash appended (pkg/webconfig/tls_credentials.go,
// pkg/k8s/resource_namer.go at v0.94.1), which is not derived here, so an
// entry under one of those names is left to the API to refuse.
//
// Two entries of secrets, or of configMaps, whose volumes the operator gives
// one name are refused for the same reason: it adds a volume for each
// (:638-690), so the pods would have two of that name. An entry whose volume
// name is not a DNS-1123 label once cut to 63 characters (one cut after a -)
// is refused: the operator checks the name after the cut and fails the
// reconcile (ResourceNamer.DNS1123Label, :640-643). On the claim template
// arm, a claim template named as a volume the operator adds is refused: the
// StatefulSet controller replaces the pod's volume of that name with the
// claim, so the pods would not get the operator's volume.
func refuseGeneratedAlertmanagerVolumes(spec *monitoringv1.AlertmanagerSpec) error {
	generated := map[string]string{}
	for _, name := range alertmanagerGeneratedVolumes {
		generated[name] = "a volume the Prometheus operator adds to every Alertmanager's pods"
	}
	if c := spec.AlertmanagerConfiguration; c != nil && len(c.Templates) > 0 {
		generated["notification-templates"] = "the volume the Prometheus operator adds for alertmanagerConfiguration.templates"
	}
	for _, source := range []struct {
		field, prefix string
		names         []string
	}{{"secrets", "secret", spec.Secrets}, {"configMaps", "configmap", spec.ConfigMaps}} {
		for i, entry := range source.names {
			volume := alertmanagerSourceVolume(source.prefix, entry)
			if errs := validation.IsDNS1123Label(volume); len(errs) > 0 {
				return errors.Errorf("%s[%d] %q: the Prometheus operator names its volume %q, which is not a DNS-1123 label: %s; the operator then fails to build the pods; list a name whose first 63 characters, with the prefix, end in a letter or a digit", source.field, i, entry, volume, strings.Join(errs, "; "))
			}
			if what, ok := generated[volume]; ok {
				return errors.Errorf("%s[%d] %q: the Prometheus operator names its volume %q, which is %s, and the API refuses a pod with two volumes of one name; list each %s once, under names that differ in lower case and in their runs of a-z, 0-9 and -", source.field, i, entry, volume, what, source.prefix)
			}
			generated[volume] = fmt.Sprintf("the volume the Prometheus operator adds for %s[%d]", source.field, i)
		}
	}
	if s := spec.Storage; s != nil && s.EmptyDir == nil && s.Ephemeral == nil {
		if what, ok := generated[s.VolumeClaimTemplate.Name]; ok {
			return errors.Errorf("storage.volumeClaimTemplate.metadata.name: %q is %s, and the StatefulSet controller replaces the pod's volume of the claim template's name with the claim, so the pods would not get it; name the claim template otherwise", s.VolumeClaimTemplate.Name, what)
		}
	}
	for i, v := range spec.Volumes {
		if what, ok := generated[v.Name]; ok {
			return errors.Errorf("volumes[%d] %q: the name is %s; name the volume otherwise", i, v.Name, what)
		}
	}
	return nil
}

// alertmanagerGeneratedMounts are the paths the Prometheus operator mounts a
// volume at in the alertmanager container whatever the spec says: its
// configuration, the configuration its reloader writes, its TLS assets, the
// data volume, the web configuration file and the cluster TLS configuration
// file (makeStatefulSetSpec, pkg/alertmanager/statefulset.go:49-71, :531-556;
// GetMountParameters in pkg/webconfig/config.go and
// pkg/alertmanager/clustertlsconfig/config.go at prometheus-operator v0.94.1).
// The operator mounts the last two only for Alertmanager 0.22.0 and 0.24.0 on;
// both are reserved whatever version names, as the volumes are.
var alertmanagerGeneratedMounts = []string{
	"/etc/alertmanager/config",
	"/etc/alertmanager/config_out",
	"/etc/alertmanager/certs",
	"/alertmanager",
	"/etc/alertmanager/web_config/web-config.yaml",
	"/etc/alertmanager/cluster_tls_config/cluster-tls-config.yaml",
}

// refuseGeneratedAlertmanagerMounts refuses an entry of volumeMounts at a path
// the operator mounts a volume at in the alertmanager container: it appends
// volumeMounts to its own mounts (statefulset.go:692), and the API refuses a
// container with two mounts at one path. Beside the fixed paths, those are
// /etc/alertmanager/templates where alertmanagerConfiguration.templates is set,
// and /etc/alertmanager/secrets/<name> and /etc/alertmanager/configmaps/<name>
// for each entry of secrets and configMaps (:575-690). The mounts of the web
// and cluster TLS credentials, whose paths the operator derives from each
// credential's source, are left to the API, as their volumes are.
func refuseGeneratedAlertmanagerMounts(spec *monitoringv1.AlertmanagerSpec) error {
	generated := map[string]string{}
	for _, p := range alertmanagerGeneratedMounts {
		generated[p] = "a path the Prometheus operator mounts a volume at in every alertmanager container"
	}
	if c := spec.AlertmanagerConfiguration; c != nil && len(c.Templates) > 0 {
		generated["/etc/alertmanager/templates"] = "the path the Prometheus operator mounts alertmanagerConfiguration.templates at"
	}
	for i, s := range spec.Secrets {
		generated[path.Join("/etc/alertmanager/secrets", s)] = fmt.Sprintf("the path the Prometheus operator mounts secrets[%d] at", i)
	}
	for i, c := range spec.ConfigMaps {
		generated[path.Join("/etc/alertmanager/configmaps", c)] = fmt.Sprintf("the path the Prometheus operator mounts configMaps[%d] at", i)
	}
	for i, m := range spec.VolumeMounts {
		if what, ok := generated[m.MountPath]; ok {
			return errors.Errorf("volumeMounts[%d] %q: the mount path is %s, and the API refuses a container with two mounts at one path; mount the volume elsewhere", i, m.MountPath, what)
		}
	}
	return nil
}

// validateAlertmanagerName refuses an Alertmanager name the operator's objects
// cannot be named after, and a data volume the pods would not get. The
// operator names the StatefulSet alertmanager-<name>, whose pods take the
// hostname alertmanager-<name>-<ordinal>, and the data volume
// alertmanager-<name>-db (volumeName and prefixedName,
// pkg/alertmanager/statefulset.go:909-915 at v0.94.1), which the API refuses
// where it is not a DNS-1123 label: at most 63 characters, and no dot.
//
// The claim template's name, where it is set, is the name the operator mounts
// the data volume under, whatever arm is in use (:531-535). On the claim
// template arm it names the claim too; beside emptyDir or ephemeral the
// operator creates the volume as alertmanager-<name>-db (:174-198), so another
// name leaves the mount without a volume.
//
// An entry of volumes under the data volume's name is refused: beside emptyDir
// or ephemeral the API refuses a pod with two volumes of one name, and on the
// claim template arm the StatefulSet controller replaces the entry with the
// claim, so the volume authored there would not be the one the pods get.
func validateAlertmanagerName(name, componentName string, spec *monitoringv1.AlertmanagerSpec) error {
	refuse := func(rule string, args ...any) error {
		if name == componentName {
			return errors.Errorf("alertmanager %q: the component name is the Alertmanager's name, and "+rule, append([]any{name}, args...)...)
		}
		return errors.Errorf("%s: %q is not a valid name for this Alertmanager: "+rule, append([]any{objectNameField, name}, args...)...)
	}
	generated := "alertmanager-" + name + "-db"
	s := spec.Storage
	claimArm := s != nil && s.EmptyDir == nil && s.Ephemeral == nil
	volume := generated
	if claimArm && s.VolumeClaimTemplate.Name != "" {
		volume = s.VolumeClaimTemplate.Name
	}
	if volume == generated {
		if errs := validation.IsDNS1123Label(generated); len(errs) > 0 {
			return refuse("the Prometheus operator names the data volume %q, which must be a DNS-1123 label: %s", generated, strings.Join(errs, "; "))
		}
	} else if errs := validation.IsDNS1123Label(volume); len(errs) > 0 {
		return errors.Errorf("storage.volumeClaimTemplate.metadata.name: %q is not a DNS-1123 label: %s; the Prometheus operator names the data volume with it, which the API then refuses", volume, strings.Join(errs, "; "))
	}
	if s != nil && !claimArm && s.VolumeClaimTemplate.Name != "" && s.VolumeClaimTemplate.Name != generated {
		return errors.Errorf("storage.volumeClaimTemplate.metadata.name: %q beside storage.emptyDir or storage.ephemeral: the Prometheus operator mounts the data volume under this name, but creates it from the arm in use as %q, so the pods would mount a volume they do not have; leave it unset", s.VolumeClaimTemplate.Name, generated)
	}
	replicas := int32(alertmanagerOperatorReplicas)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}
	if replicas > 0 {
		hostname := "alertmanager-" + name + "-" + strconv.Itoa(int(replicas)-1)
		if errs := validation.IsDNS1123Label(hostname); len(errs) > 0 {
			return refuse("the pod of the last replica takes the hostname %q, which must be a DNS-1123 label: %s", hostname, strings.Join(errs, "; "))
		}
	}
	for i, v := range spec.Volumes {
		if v.Name != volume {
			continue
		}
		if claimArm {
			return errors.Errorf("volumes[%d] %q: the name is the data volume's claim template's, and the StatefulSet controller replaces a volume of that name with the claim, so the pods would not get this one; name the volume otherwise", i, v.Name)
		}
		return errors.Errorf("volumes[%d] %q: the name is the data volume's, which the Prometheus operator adds to the pods, and the API refuses a pod with two volumes of one name; name the volume otherwise", i, v.Name)
	}
	return nil
}

// What the Prometheus operator fills into an Alertmanager whose spec leaves
// the field unset, before it builds the StatefulSet (makeStatefulSet,
// pkg/alertmanager/statefulset.go:133-135 and :144-149 at prometheus-operator
// v0.94.1). Held to the environment policy, never written.
const (
	alertmanagerOperatorReplicas      = 1
	alertmanagerOperatorMemoryRequest = "200Mi"
)

// alertmanagerGenerated names the containers the operator generates for the
// pods, by the property that lists patches of them (makeStatefulSetSpec,
// pkg/alertmanager/statefulset.go:793-838 at prometheus-operator v0.94.1). Of
// them only alertmanager takes its image from the spec; the two reloaders run
// the image of the operator's own configuration unless a listed entry patches
// one.
var alertmanagerGenerated = map[string][]string{
	"containers":     {"alertmanager", "config-reloader"},
	"initContainers": {"init-config-reloader"},
}

// alertmanagerGeneratedPorts are the ports the operator gives the containers
// it generates: unless listenLocal is set, the alertmanager container's web
// port under portName (web where it is unset) at 9093 and the config-reloader
// container's reloader-web at 8080; whatever listenLocal says, the
// alertmanager container's mesh-tcp and mesh-udp at 9094, and the
// init-config-reloader container's reloader-init at 8081
// (pkg/alertmanager/statefulset.go:483-502 and :793-830, and CreateConfigReloader,
// pkg/operator/config_reloader.go:228-282 at prometheus-operator v0.94.1).
func alertmanagerGeneratedPorts(spec *monitoringv1.AlertmanagerSpec) map[string]map[string]int32 {
	ports := map[string]map[string]int32{
		"alertmanager":         {"mesh-tcp": 9094, "mesh-udp": 9094},
		"config-reloader":      {},
		"init-config-reloader": {"reloader-init": 8081},
	}
	if !spec.ListenLocal {
		web := spec.PortName
		if web == "" {
			web = "web"
		}
		ports["alertmanager"][web] = 9093
		ports["config-reloader"]["reloader-web"] = 8080
	}
	return ports
}

// refuseGeneratedAlertmanagerArgs refuses an entry of additionalArgs that
// names a flag the operator generates for the spec: it fails to build the pods
// where an additional argument's name, or that name with no- added or taken
// away, is the name of one (BuildArgs and ArgumentsIntersection,
// pkg/operator/argument.go:26-79 at prometheus-operator v0.94.1). The flags
// are those of makeStatefulSetSpec (pkg/alertmanager/statefulset.go:289-508,
// :700-748). A flag the operator generates only from some Alertmanager
// version on is reserved from that version on. Where version is unset the
// operator runs its default, DefaultAlertmanagerVersion (v0.34.0,
// pkg/operator/defaults.go:25), at or above every such version, as no flag is
// generated only up to a version; so every such flag is reserved, as it is
// where version does not parse, which validateAlertmanagerVersion refuses.
// dispatch.start-delay is not: the operator leaves it out where an
// additional argument names it.
func refuseGeneratedAlertmanagerArgs(spec *monitoringv1.AlertmanagerSpec) error {
	version, err := semver.ParseTolerant(spec.Version)
	from := func(v string) bool { return spec.Version == "" || err != nil || version.GTE(semver.MustParse(v)) }
	generated := map[string]bool{}
	for _, name := range []string{"config.file", "storage.path", "data.retention", "web.listen-address", "web.route-prefix", "cluster.reconnect-timeout"} {
		generated[name] = true
	}
	replicas := int32(alertmanagerOperatorReplicas)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}
	if replicas == 1 && !spec.ForceEnableClusterMode {
		generated["cluster.listen-address="] = true
	} else {
		generated["cluster.listen-address"] = true
	}
	web, limits := spec.Web, spec.Limits
	for name, set := range map[string]bool{
		"cluster.peer":                   replicas > 0 || len(spec.AdditionalPeers) > 0,
		"web.external-url":               spec.ExternalURL != "",
		"enable-feature":                 from("0.27.0") && len(spec.EnableFeatures) > 0,
		"web.get-concurrency":            from("0.17.0") && web != nil && web.GetConcurrency != nil,
		"web.timeout":                    from("0.17.0") && web != nil && web.Timeout != nil,
		"silences.max-silences":          from("0.28.0") && limits != nil && limits.MaxSilences != nil,
		"silences.max-per-silence-bytes": from("0.28.0") && limits != nil && !limits.MaxPerSilenceBytes.IsEmpty(),
		"log.level":                      spec.LogLevel != "" && spec.LogLevel != "info",
		"log.format":                     from("0.16.0") && spec.LogFormat != "" && spec.LogFormat != "logfmt",
		"cluster.advertise-address":      spec.ClusterAdvertiseAddress != "",
		"cluster.gossip-interval":        spec.ClusterGossipInterval != "",
		"cluster.pushpull-interval":      spec.ClusterPushpullInterval != "",
		"cluster.peer-timeout":           spec.ClusterPeerTimeout != "",
		"cluster.peer-name":              from("0.30.0"),
		"cluster.label":                  from("0.26.0"),
		"web.config.file":                from("0.22.0"),
		"cluster.tls-config":             from("0.24.0") && spec.ClusterTLS != nil,
	} {
		if set {
			generated[name] = true
		}
	}
	for i, arg := range spec.AdditionalArgs {
		negated, found := strings.CutPrefix(arg.Name, "no-")
		if !found {
			negated = "no-" + arg.Name
		}
		for _, name := range []string{arg.Name, negated} {
			if generated[name] {
				return errors.Errorf("additionalArgs[%d] %q: the Prometheus operator generates the flag %q for this spec, and fails to build the pods where an additional argument names it; set the field that configures it, or leave the argument out", i, arg.Name, name)
			}
		}
	}
	return nil
}

// refuseDuplicateAlertmanagerTemplateKeys refuses an entry of
// alertmanagerConfiguration.templates whose key an earlier entry names: the
// operator projects each key into one volume at the path of its name and
// skips a later entry of a key it has projected, configMap or secret
// (pkg/alertmanager/statefulset.go:575-620 at prometheus-operator v0.94.1), so
// that template would not be loaded.
func refuseDuplicateAlertmanagerTemplateKeys(spec *monitoringv1.AlertmanagerSpec) error {
	c := spec.AlertmanagerConfiguration
	if c == nil {
		return nil
	}
	first := map[string]string{}
	for i, t := range c.Templates {
		for _, source := range []struct {
			field string
			key   *string
		}{{"configMap", configMapKey(t.ConfigMap)}, {"secret", secretKey(t.Secret)}} {
			if source.key == nil {
				continue
			}
			where := fmt.Sprintf("alertmanagerConfiguration.templates[%d].%s.key", i, source.field)
			if earlier, seen := first[*source.key]; seen {
				return errors.Errorf("%s: %q is the key %s names already, and the Prometheus operator skips a template whose key it has already loaded; give each template a key of its own", where, *source.key, earlier)
			}
			first[*source.key] = where
		}
	}
	return nil
}

// validateAlertmanagerTLS refuses a web or cluster TLS configuration the
// operator refuses when it builds the pods, by the API module's own Validate:
// web.tlsConfig (webconfig.New, pkg/webconfig/config.go:50-55), and
// clusterTLS's server and client, the client with a certificate
// (clustertlsconfig.New, pkg/alertmanager/clustertlsconfig/config.go:78-90 at
// prometheus-operator v0.94.1). The reconcile builds both configuration
// Secrets whatever version names (pkg/alertmanager/operator.go:648-656), so
// both are held whatever version names.
func validateAlertmanagerTLS(spec *monitoringv1.AlertmanagerSpec) error {
	if spec.Web != nil {
		if err := spec.Web.TLSConfig.Validate(); err != nil {
			return errors.Errorf("web.tlsConfig: %v; the Prometheus operator refuses it and builds no pods", err)
		}
	}
	if c := spec.ClusterTLS; c != nil {
		if err := c.ServerTLS.Validate(); err != nil {
			return errors.Errorf("clusterTLS.server: %v; the Prometheus operator refuses it and builds no pods", err)
		}
		if err := c.ClientTLS.Validate(); err != nil {
			return errors.Errorf("clusterTLS.client: %v; the Prometheus operator refuses it and builds no pods", err)
		}
		if c.ClientTLS.Cert == (monitoringv1.SecretOrConfigMap{}) {
			return errors.New("clusterTLS.client.cert: required: the Prometheus operator refuses a client without a certificate and builds no pods")
		}
	}
	return nil
}

func configMapKey(s *corev1.ConfigMapKeySelector) *string {
	if s == nil {
		return nil
	}
	return &s.Key
}

func secretKey(s *corev1.SecretKeySelector) *string {
	if s == nil {
		return nil
	}
	return &s.Key
}

// alertmanagerWorkload maps an Alertmanager spec into the workload the two
// shared functions read. It only reads spec.
//
// Held through it: image, where no patch of the alertmanager container names
// one in its place; replicas, an unset one as the operator's 1;
// storage; resources, the alertmanager container's as the operator runs it:
// an unset memory request filled as its 200Mi, then the requests and limits
// of a listed alertmanager entry merged over it, key by key, so a request the
// entry names replaces the 200Mi, and the entry's block is not checked alone (makeStatefulSet and makeStatefulSetSpec,
// pkg/alertmanager/statefulset.go:144-149, :762 and :817 at
// prometheus-operator v0.94.1); the images of the three containers the operator
// generates, where the spec leaves them to it; and, as pod fields, containers,
// initContainers, volumes, securityContext and hostNetwork. The spec has no
// hostPID or hostIPC field, and no credential in the clear: every one is the
// key of a Secret. TestMonitoringWorkloadKinds_PodFieldsHeldOrListed and
// TestMonitoringWorkloadKinds_CredentialsHeldOrListed derive both claims from
// the type.
func alertmanagerWorkload(spec *monitoringv1.AlertmanagerSpec) monitoringWorkload {
	replicas := int64(alertmanagerOperatorReplicas)
	if spec.Replicas != nil {
		replicas = int64(*spec.Replicas)
	}
	resources := fieldResources{"resources", spec.Resources}
	var merged map[string]string
	if i := patchOf(spec.Containers, "alertmanager"); i >= 0 {
		patch := spec.Containers[i].Resources
		if len(patch.Requests) > 0 || len(patch.Limits) > 0 {
			resources = fieldResources{
				fmt.Sprintf("resources with containers[%d] %q merged over it", i, "alertmanager"),
				mergedResources(spec.Resources, patch),
			}
			merged = map[string]string{"containers": "alertmanager"}
		}
	}
	w := monitoringWorkload{
		pod: corev1.PodSpec{
			InitContainers:  spec.InitContainers,
			Containers:      spec.Containers,
			Volumes:         spec.Volumes,
			SecurityContext: spec.SecurityContext,
			HostNetwork:     spec.HostNetwork,
		},
		generated:      alertmanagerGenerated,
		generatedPorts: alertmanagerGeneratedPorts(spec),
		replicas:       &replicas,
		replicasPath:   "replicas",
		storage:        spec.Storage,
		resources:      []fieldResources{resources},
		mergedPatches:  merged,
		memoryRequests: map[string]string{resources.path: alertmanagerOperatorMemoryRequest},
	}
	if spec.DNSPolicy != nil {
		w.pod.DNSPolicy = corev1.DNSPolicy(*spec.DNSPolicy)
	}
	if spec.DNSConfig != nil {
		w.pod.DNSConfig = &corev1.PodDNSConfig{Nameservers: spec.DNSConfig.Nameservers}
	}
	// The image a patch names replaces image in the container the operator
	// builds from it, so image is then not run, and not held: the patch's own
	// is, as a listed container's image.
	switch {
	case patchedImage(spec.Containers, "alertmanager") != "":
	case spec.Image != nil && *spec.Image != "":
		w.images = []fieldValue{{"image", *spec.Image}}
	default:
		w.unsetImages = []string{"image"}
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
	return w
}

// ToApplicationConfig decodes an OAM alertmanager component into its config.
// The object takes the namespace of the application it is generated in.
func (h *AlertmanagerHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return alertmanagerKind.config(component)
}
