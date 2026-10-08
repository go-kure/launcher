package components_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The thanosruler kind (go-kure/launcher#790): the fixtures the shared tests
// of kind_policy_free_test.go build it from, and what the kind does beyond the
// shared helper: its one renamed field, the environment policy on the pods the
// Prometheus operator runs for it, the one credential its spec holds in the
// clear, and the pods' metadata.

// thanosRulerFull sets every top-level field of the spec, inside
// ptStrictPolicy: images of registry.example with a tag, two replicas, no more
// than 2 cpu, 1Gi of memory and 10Gi of storage, nothing of the host, and no
// credential in the object. Its external labels are the externalLabels
// property.
func thanosRulerFull() map[string]any {
	webTLS := map[string]any{
		"keySecret": secretKey("thanos-ruler-tls", "tls.key"),
		"cert":      map[string]any{"secret": secretKey("thanos-ruler-tls", "tls.crt")},
	}
	return map[string]any{
		"version": "v0.39.2",
		"podMetadata": map[string]any{
			"labels":      map[string]any{"team": "payments"},
			"annotations": map[string]any{"example.com/owner": "sre"},
		},
		"image":            "registry.example/thanos/thanos:v0.39.2",
		"imagePullPolicy":  "IfNotPresent",
		"imagePullSecrets": []any{map[string]any{"name": "registry-credentials"}},
		"paused":           true,
		"replicas":         2,
		"nodeSelector":     map[string]any{"kubernetes.io/os": "linux"},
		"schedulerName":    "default-scheduler",
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "100m", "memory": "128Mi"},
			"limits":   map[string]any{"cpu": 2, "memory": "1Gi"},
		},
		"affinity": map[string]any{"podAntiAffinity": map[string]any{
			"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
				"topologyKey":   "kubernetes.io/hostname",
				"labelSelector": map[string]any{"matchLabels": map[string]any{"thanos-ruler": "main"}},
			}},
		}},
		"tolerations": []any{map[string]any{"key": "dedicated", "operator": "Equal", "value": "monitoring", "effect": "NoSchedule"}},
		"topologySpreadConstraints": []any{map[string]any{
			"maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway",
			"labelSelector": map[string]any{"matchLabels": map[string]any{"thanos-ruler": "main"}},
		}},
		"securityContext":    map[string]any{"runAsNonRoot": true, "runAsUser": 1000, "fsGroup": 2000},
		"dnsPolicy":          "None",
		"dnsConfig":          map[string]any{"nameservers": []any{"192.0.2.53"}, "searches": []any{"example.com"}, "options": []any{map[string]any{"name": "ndots", "value": "2"}}},
		"enableServiceLinks": false,
		"priorityClassName":  "monitoring",
		"serviceName":        "thanos-ruler",
		"serviceAccountName": "thanos-ruler",
		"storage": map[string]any{"volumeClaimTemplate": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "monitoring"}},
			"spec": map[string]any{
				"accessModes": []any{"ReadWriteOnce"}, "storageClassName": "fast",
				"resources": map[string]any{"requests": map[string]any{"storage": "10Gi"}},
			},
		}},
		"volumes":                            []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{"sizeLimit": "1Gi"}}},
		"volumeMounts":                       []any{map[string]any{"name": "scratch", "mountPath": "/scratch"}},
		"objectStorageConfig":                secretKey("thanos-objstore", "objstore.yml"),
		"objectStorageConfigFile":            "/etc/thanos/objstore.yml",
		"listenLocal":                        true,
		"podManagementPolicy":                "OrderedReady",
		"updateStrategy":                     map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": 1}},
		"queryEndpoints":                     []any{"dnssrv+_http._tcp.thanos-query.monitoring.svc"},
		"queryConfig":                        secretKey("thanos-query", "query.yml"),
		"alertmanagersUrl":                   []any{"dnssrv+_http._tcp.alertmanager-operated.monitoring.svc"},
		"alertmanagersConfig":                secretKey("thanos-alertmanagers", "alertmanagers.yml"),
		"ruleSelector":                       map[string]any{"matchLabels": map[string]any{"role": "alert-rules"}},
		"ruleNamespaceSelector":              map[string]any{"matchExpressions": []any{map[string]any{"key": "team", "operator": "In", "values": []any{"payments"}}}},
		"enforcedNamespaceLabel":             "namespace",
		"excludedFromEnforcement":            []any{map[string]any{"group": "monitoring.coreos.com", "resource": "prometheusrules", "namespace": "monitoring", "name": "global"}},
		"prometheusRulesExcludedFromEnforce": []any{map[string]any{"ruleNamespace": "monitoring", "ruleName": "global"}},
		"logLevel":                           "info",
		"logFormat":                          "json",
		"portName":                           "http-web",
		"evaluationInterval":                 "30s",
		"resendDelay":                        "1m",
		"ruleOutageTolerance":                "1h",
		"ruleQueryOffset":                    "30s",
		"ruleConcurrentEval":                 2,
		"ruleGracePeriod":                    "10m",
		"retention":                          "48h",
		"containers": []any{
			// A patch of the container the operator generates: no image.
			map[string]any{"name": "thanos-ruler", "readinessProbe": map[string]any{"periodSeconds": 5}},
			map[string]any{
				"name": "proxy", "image": "registry.example/team/proxy:1.2.3",
				"resources":       map[string]any{"limits": map[string]any{"cpu": "500m", "memory": "64Mi"}},
				"securityContext": map[string]any{"capabilities": map[string]any{"drop": []any{"ALL"}}},
			},
			// The reloader the operator generates, patched with an allowed image.
			map[string]any{"name": "config-reloader", "image": trReloader},
		},
		"initContainers":    []any{map[string]any{"name": "prepare", "image": "registry.example/team/prepare:1.0.0"}},
		"tracingConfig":     secretKey("thanos-tracing", "tracing.yml"),
		"tracingConfigFile": "/etc/thanos/tracing.yml",
		"externalLabels":    map[string]any{"cluster": "eu-1"},
		"alertDropLabels":   []any{"replica"},
		"externalPrefix":    "https://rules.example.com",
		"routePrefix":       "/rules",
		"grpcServerTlsConfig": map[string]any{
			"caFile": "/etc/thanos/tls/ca.crt", "certFile": "/etc/thanos/tls/tls.crt", "keyFile": "/etc/thanos/tls/tls.key",
		},
		"alertQueryUrl":          "https://query.example.com",
		"minReadySeconds":        0,
		"alertRelabelConfigs":    secretKey("thanos-relabel", "relabel.yml"),
		"alertRelabelConfigFile": "/etc/thanos/relabel.yml",
		"hostAliases":            []any{map[string]any{"ip": "192.0.2.20", "hostnames": []any{"query.internal"}}},
		"additionalArgs":         []any{map[string]any{"name": "tsdb.wal-compression"}},
		"web":                    map[string]any{"tlsConfig": webTLS, "httpConfig": map[string]any{"http2": true}},
		"remoteWrite": []any{map[string]any{
			"url":       "https://metrics.example.com/api/v1/write",
			"basicAuth": map[string]any{"username": secretKey("remote-write", "username"), "password": secretKey("remote-write", "password")},
			"headers":   map[string]any{"X-Scope-OrgID": "payments"},
		}},
		"terminationGracePeriodSeconds": 0,
		"enableFeatures":                []any{"promql-experimental-functions"},
		"hostUsers":                     false,
	}
}

// thanosRulerReaches names references of the full fixture's object that the
// copy test must find (TestPolicyFreeKinds_GenerateCopies).
var thanosRulerReaches = []string{
	".Spec.Version", ".Spec.PodMetadata", ".Spec.PodMetadata.Labels", ".Spec.Replicas",
	".Spec.Storage", ".Spec.Storage.VolumeClaimTemplate.EmbeddedObjectMetadata.Labels",
	".Spec.Storage.VolumeClaimTemplate.Spec.Resources.Requests", ".Spec.Volumes", ".Spec.Volumes[0].VolumeSource.EmptyDir",
	".Spec.NodeSelector", ".Spec.Resources.Limits", ".Spec.Affinity", ".Spec.SecurityContext", ".Spec.DNSConfig",
	".Spec.ServiceName", ".Spec.ObjectStorageConfig", ".Spec.ObjectStorageConfigFile", ".Spec.UpdateStrategy",
	".Spec.QueryEndpoints", ".Spec.QueryConfig", ".Spec.AlertManagersURL", ".Spec.RuleSelector",
	".Spec.RuleNamespaceSelector.MatchExpressions[0].Values", ".Spec.ExcludedFromEnforcement",
	".Spec.PrometheusRulesExcludedFromEnforce", ".Spec.ResendDelay", ".Spec.RuleConcurrentEval",
	".Spec.Containers", ".Spec.Containers[0].ReadinessProbe", ".Spec.InitContainers", ".Spec.TracingConfig",
	".Spec.Labels", ".Spec.AlertDropLabels", ".Spec.GRPCServerTLSConfig", ".Spec.MinReadySeconds",
	".Spec.AlertRelabelConfigs", ".Spec.HostAliases[0].Hostnames", ".Spec.AdditionalArgs", ".Spec.Web",
	".Spec.RemoteWrite", ".Spec.RemoteWrite[0].Headers", ".Spec.RemoteWrite[0].BasicAuth",
	".Spec.TerminationGracePeriodSeconds", ".Spec.EnableFeatures", ".Spec.HostUsers",
}

// thanosRulerRefusals are the kind's refusal cases with or without an
// environment policy (TestPolicyFreeKinds_Refusals). The kind requires no
// top-level field.
func thanosRulerRefusals(notA string) []struct {
	name  string
	props map[string]any
	want  string
} {
	container := func(c map[string]any) map[string]any { return map[string]any{"containers": []any{c}} }
	return []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"unknown key", map[string]any{"replicaCount": 3}, notA + "monitoring.coreos.com/v1 ThanosRulerSpec"},
		{"the object's spec", map[string]any{"spec": map[string]any{"replicas": 3}}, notA},
		{"replicas a string", map[string]any{"replicas": "three"}, notA},
		{"storage sub-key", map[string]any{"storage": map[string]any{"size": "10Gi"}}, notA},
		{"container sub-key", container(map[string]any{"name": "proxy", "registry": "registry.example"}), notA},
		{"null container", map[string]any{"containers": []any{map[string]any{"name": "proxy"}, nil}}, "containers[1]"},
		{"two spellings", map[string]any{"replicas": 1, "Replicas": 2}, "sets the same field as"},

		// The renamed field: under its own name in another spelling, and
		// wrongly typed under the published one.
		{"the spec's labels in another spelling", map[string]any{"Labels": map[string]any{"cluster": "eu-1"}}, "Labels: not a thanosruler property: the ThanosRuler's external labels are the externalLabels property"},
		{"external labels a list", map[string]any{"externalLabels": []any{"cluster"}}, "externalLabels"},
		{"an external label a number", map[string]any{"externalLabels": map[string]any{"replica": 1}}, "externalLabels"},
		{"external labels in another spelling", map[string]any{"ExternalLabels": map[string]any{"cluster": "eu-1"}}, notA + "monitoring.coreos.com/v1 ThanosRulerSpec"},

		{"argument without a name", map[string]any{"additionalArgs": []any{map[string]any{"value": "debug"}}}, "additionalArgs[0].name: required"},
		{"remote write without a URL", map[string]any{"remoteWrite": []any{map[string]any{"headers": map[string]any{"X-Scope-OrgID": "payments"}}}}, "remoteWrite[0].url: required"},

		{"image without a tag", map[string]any{"image": "registry.example/thanos/thanos"},
			`image: image "registry.example/thanos/thanos" rejected: no tag or digest specified`},
		{"image tagged latest", map[string]any{"image": "registry.example/thanos/thanos:latest"},
			`image: image "registry.example/thanos/thanos:latest" rejected: :latest tag not allowed`},
		{"container image tagged latest", container(map[string]any{"name": "proxy", "image": "registry.example/team/proxy:latest"}),
			`containers[0] "proxy": image "registry.example/team/proxy:latest" rejected: :latest tag not allowed`},
		{"request over its limit", map[string]any{"resources": map[string]any{
			"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"},
		}}, "resources: cpu: request 2 must not exceed limit 1"},
		// The operator requests 200Mi where no memory request is named, whatever
		// the limit.
		{"memory limit under the operator's request", map[string]any{"resources": map[string]any{
			"limits": map[string]any{"memory": "100Mi"},
		}}, "resources: memory: the unset request the Prometheus operator fills as 200Mi must not exceed limit 100Mi; name a request no larger than the limit"},
		// The operator merges a patch of the thanos-ruler container over the
		// block it builds it with (pkg/thanos/statefulset.go:481 and :494), so
		// the two are held as one.
		{"memory limit of a patch under the operator's request", container(map[string]any{"name": "thanos-ruler", "resources": map[string]any{
			"limits": map[string]any{"memory": "64Mi"},
		}}), `resources with containers[0] "thanos-ruler" merged over it: memory: the unset request the Prometheus operator fills as 200Mi must not exceed limit 64Mi`},
		{"request of a patch over the spec's limit", map[string]any{
			"resources":  map[string]any{"limits": map[string]any{"memory": "100Mi"}},
			"containers": []any{map[string]any{"name": "thanos-ruler", "resources": map[string]any{"requests": map[string]any{"memory": "256Mi"}}}},
		}, `resources with containers[0] "thanos-ruler" merged over it: resources: memory: request 256Mi must not exceed limit 100Mi`},
		{"a negative memory limit of a patch", container(map[string]any{"name": "thanos-ruler", "resources": map[string]any{"limits": map[string]any{"memory": "-1Gi"}}}),
			`resources with containers[0] "thanos-ruler" merged over it: resources: memory: limit -1Gi is below 0`},
		// The operator copies both into the pod template (statefulset.go:539-540).
		{"dnsPolicy None without nameservers", map[string]any{"dnsPolicy": "None"},
			"dnsPolicy: None requires dnsConfig.nameservers with at least one entry"},
		// A listed entry that patches no container of the operator's own is
		// added as written, and must name an image; the operator generates no
		// init container at all.
		{"container without an image", container(map[string]any{"name": "proxy"}),
			`containers[0] "proxy": names no image, and the Prometheus operator generates no container of that name to merge it into; name an image, or the container it patches (thanos-ruler, config-reloader)`},
		{"init container without an image", map[string]any{"initContainers": []any{map[string]any{"name": "prepare"}}},
			`initContainers[0] "prepare": names no image, and the Prometheus operator merges no entry of initContainers into a container of its own; name an image`},
		// The API requires the names of a pod's init containers and
		// containers to be unique together; an init container is added as
		// written, never merged into the reloader.
		{"an init container named as a generated container", map[string]any{"initContainers": []any{map[string]any{"name": "config-reloader", "image": "registry.example/team/proxy:1.2.3"}}},
			`initContainers[0] "config-reloader": the name is that of a container the Prometheus operator generates, and the API refuses a pod whose init containers and containers share a name`},
		{"a container named as a listed init container", map[string]any{
			"initContainers": []any{map[string]any{"name": "prepare", "image": "registry.example/team/prepare:1.0.0"}},
			"containers":     []any{map[string]any{"name": "prepare", "image": "registry.example/team/proxy:1.2.3"}},
		}, `containers[0] "prepare": the name is also that of initContainers[0], and the API refuses a pod whose init containers and containers share a name`},
		// A ThanosRuler has no hostNetwork, which the API requires of a
		// HostProcess pod.
		{"a pod-level HostProcess", map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}},
			"securityContext.windowsOptions.hostProcess: hostNetwork must be true when hostProcess is true"},
		// An authored 0 the type omits, on a container that patches one of the
		// operator's own as on any other.
		{"probe period of 0 on a patch", container(map[string]any{"name": "thanos-ruler", "readinessProbe": map[string]any{"periodSeconds": 0}}),
			"containers[0].readinessProbe.periodSeconds: 0 cannot be carried by the "},
		// An authored "" on a string of the operator's own types that the CRD defaults.
		{"empty evaluationInterval", map[string]any{"evaluationInterval": ""},
			`evaluationInterval: "" cannot be carried by the Prometheus operator API types (the field is omitted when zero, so the API server would apply its default "15s")`},

		// A web port name the API refuses where the operator writes it
		// (thanos/statefulset.go:216-232 and :545-583 at v0.94.1).
		{"a port name over 15 characters", map[string]any{"portName": "thanos-ruler-web"},
			`portName: "thanos-ruler-web" is not a valid port name`},
		{"a port name of the gRPC port", map[string]any{"portName": "grpc"},
			`portName: "grpc" is the name of a port the Prometheus operator adds to the thanos-ruler container, and the API refuses a port name twice`},
		{"a port name of the governing Service's gRPC port", map[string]any{"portName": "grpc", "listenLocal": true},
			`portName: "grpc" is the name of a port of the governing Service the Prometheus operator creates where serviceName is unset`},
		// The operator merges a patch's ports into its own by number: the
		// thanos-ruler container's grpc port, and the reloader's reloader-web.
		{"a patched port of the gRPC port's name at another number", container(map[string]any{"name": "thanos-ruler", "ports": []any{
			map[string]any{"name": "grpc", "containerPort": 9000},
		}}), `containers[0] "thanos-ruler": ports[0] "grpc" at 9000/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 10901/TCP`},
		{"a patched port of the reloader's name at another number", container(map[string]any{"name": "config-reloader", "ports": []any{
			map[string]any{"name": "reloader-web", "containerPort": 9000},
		}}), `containers[0] "config-reloader": ports[0] "reloader-web" at 9000/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 8080/TCP`},
		{"a serviceName that is not a DNS-1035 label", map[string]any{"serviceName": "1rules"}, "serviceName:"},
		// The operator copies the count into the StatefulSet (statefulset.go:507).
		{"negative replicas", map[string]any{"replicas": -1},
			"replicas: -1 is below 0: the Prometheus operator copies it into the StatefulSet, which the API refuses with fewer than 0; write 0 or more"},
		// A claim the API refuses (statefulset.go:92-133).
		{"a claim template without a storage request", trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{"accessModes": []any{"ReadWriteOnce"}}}}),
			"storage.volumeClaimTemplate.spec.resources.requests.storage: required where neither storage.emptyDir nor storage.ephemeral is set"},
		{"an ephemeral claim without access modes", trStorage(map[string]any{"ephemeral": map[string]any{"volumeClaimTemplate": map[string]any{"spec": trClaim}}}),
			"storage.ephemeral.volumeClaimTemplate.spec.accessModes: required"},
		{"a claim template requesting 0", trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "0"}}}}}),
			"storage.volumeClaimTemplate.spec.resources.requests.storage: 0 is not above 0"},
		// The operator mounts the data volume under the claim template's name
		// whatever arm is in use, and creates it under its own beside emptyDir
		// and ephemeral (statefulset.go:92-133 and :441-450).
		{"a named claim template beside emptyDir", trStorage(map[string]any{
			"emptyDir":            map[string]any{},
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": trClaim},
		}), `storage.volumeClaimTemplate.metadata.name: "data" beside storage.emptyDir or storage.ephemeral: the Prometheus operator mounts the data volume under this name, but creates it from the arm in use as "thanos-ruler-fast-data"`},
		{"a claim template name that is not a DNS-1123 label", trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data.disk"}, "spec": trClaim}}),
			`storage.volumeClaimTemplate.metadata.name: "data.disk" is not a DNS-1123 label`},
		{"a claim template named as the TLS assets' volume", trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "tls-assets"}, "spec": trClaim}}),
			`storage.volumeClaimTemplate.metadata.name: "tls-assets" is a volume the Prometheus operator adds to every ThanosRuler's pods, and the StatefulSet controller replaces`},
		{"a claim template named as a rule ConfigMap's volume", trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "thanos-ruler-fast-rulefiles-0"}, "spec": trClaim}}),
			`storage.volumeClaimTemplate.metadata.name: "thanos-ruler-fast-rulefiles-0" is a volume the Prometheus operator adds for a rule ConfigMap it generates`},
		// A volume named as one the operator adds to the pods, which it lists
		// before the authored ones (statefulset.go:135, :243-297, :377-398 and
		// :452-469).
		{"a volume named as the TLS assets'", trVolume(nil, "tls-assets"),
			`volumes[0] "tls-assets": the name is a volume the Prometheus operator adds to every ThanosRuler's pods; name the volume otherwise`},
		{"a volume named as the web configuration's", trVolume(nil, "web-config"),
			`volumes[0] "web-config": the name is a volume the Prometheus operator adds to every ThanosRuler's pods`},
		{"a volume named as the remote write configuration's", trVolume(nil, "remote-write-config"),
			`volumes[0] "remote-write-config": the name is a volume the Prometheus operator adds to the pods for a Secret key of the spec`},
		{"a volume named as the query configuration's", trVolume(map[string]any{"queryConfig": secretKey("thanos-query", "query.yml")}, "query-config"),
			`volumes[0] "query-config": the name is a volume the Prometheus operator adds to the pods for a Secret key of the spec`},
		{"a volume named as the tracing configuration's", trVolume(map[string]any{"tracingConfig": secretKey("thanos-tracing", "tracing.yml")}, "tracing-config"),
			`volumes[0] "tracing-config": the name is a volume the Prometheus operator adds to the pods for a Secret key of the spec`},
		{"a volume named as the Alertmanager configuration's", trVolume(map[string]any{"alertmanagersConfig": secretKey("thanos-alertmanagers", "alertmanagers.yml")}, "alertmanager-config"),
			`volumes[0] "alertmanager-config": the name is a volume the Prometheus operator adds to the pods for a Secret key of the spec`},
		{"a volume named as the alert relabeling configuration's", trVolume(map[string]any{"alertRelabelConfigs": secretKey("thanos-alert-relabel", "relabel.yml")}, "alertrelabel-config"),
			`volumes[0] "alertrelabel-config": the name is a volume the Prometheus operator adds to the pods for a Secret key of the spec`},
		{"a volume named as the first rule ConfigMap's", trVolume(nil, "thanos-ruler-fast-rulefiles-0"),
			`volumes[0] "thanos-ruler-fast-rulefiles-0": the name is a volume the Prometheus operator adds for a rule ConfigMap it generates, of which it makes as many as the selected rules need`},
		{"a volume named as a later rule ConfigMap's", trVolume(nil, "thanos-ruler-fast-rulefiles-12"),
			`volumes[0] "thanos-ruler-fast-rulefiles-12": the name is a volume the Prometheus operator adds for a rule ConfigMap it generates`},
		{"a volume named as the data volume", trVolume(nil, "thanos-ruler-fast-data"),
			`volumes[0] "thanos-ruler-fast-data": the name is the data volume's, which the Prometheus operator adds to the pods, and the API refuses a pod with two volumes of one name`},
		{"a volume named as the claim template's", trVolume(trStorage(map[string]any{
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": trClaim},
		}), "data"), `volumes[0] "data": the name is the data volume's claim template's, and the StatefulSet controller replaces a volume of that name with the claim`},
		// The operator appends volumeMounts to its own (statefulset.go:471),
		// and the API refuses two mounts at one path.
		{"a mount at the data volume's path", trMount(nil, "/thanos/data"),
			`volumeMounts[0] "/thanos/data": the mount path is a path the Prometheus operator mounts a volume at in every thanos-ruler container, and the API refuses a container with two mounts at one path`},
		{"a mount at the TLS assets' path", trMount(nil, "/etc/thanos/certs"),
			`volumeMounts[0] "/etc/thanos/certs": the mount path is a path the Prometheus operator mounts a volume at in every thanos-ruler container`},
		{"a mount at the web configuration file", trMount(nil, "/etc/thanos/web_config/web-config.yaml"),
			`volumeMounts[0] "/etc/thanos/web_config/web-config.yaml": the mount path is a path the Prometheus operator mounts a volume at in every thanos-ruler container`},
		{"a mount at the remote write configuration's path", trMount(nil, "/etc/thanos/config/remote-write-config"),
			`volumeMounts[0] "/etc/thanos/config/remote-write-config": the mount path is a path the Prometheus operator mounts a Secret key of the spec at in the thanos-ruler container`},
		{"a mount at the object storage configuration's path", trMount(map[string]any{"objectStorageConfig": secretKey("thanos-objstore", "objstore.yml")}, "/etc/thanos/config/objstorage-config"),
			`volumeMounts[0] "/etc/thanos/config/objstorage-config": the mount path is a path the Prometheus operator mounts a Secret key of the spec at`},
		{"a mount at the Alertmanager configuration's path", trMount(map[string]any{"alertmanagersConfig": secretKey("thanos-alertmanagers", "alertmanagers.yml")}, "/etc/thanos/config/alertmanager-config"),
			`volumeMounts[0] "/etc/thanos/config/alertmanager-config": the mount path is a path the Prometheus operator mounts a Secret key of the spec at`},
		{"a mount at the alert relabeling configuration's path", trMount(map[string]any{"alertRelabelConfigs": secretKey("thanos-alert-relabel", "relabel.yml")}, "/etc/thanos/config/alertrelabel-config"),
			`volumeMounts[0] "/etc/thanos/config/alertrelabel-config": the mount path is a path the Prometheus operator mounts a Secret key of the spec at`},
		{"a mount at a rule ConfigMap's path", trMount(nil, "/etc/thanos/rules/thanos-ruler-fast-rulefiles-0"),
			`volumeMounts[0] "/etc/thanos/rules/thanos-ruler-fast-rulefiles-0": the mount path is the path the Prometheus operator mounts the volume of a rule ConfigMap it generates at`},
	}
}

// trClaim is a claim template's spec that requests storage.
var trClaim = map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}

// trStorage is the properties of a thanosruler whose storage is s.
func trStorage(s map[string]any) map[string]any {
	return map[string]any{"storage": s}
}

// trVolume returns props with one more entry of volumes, an emptyDir of the
// name. props is not changed.
func trVolume(props map[string]any, name string) map[string]any {
	out := maps.Clone(props)
	if out == nil {
		out = map[string]any{}
	}
	out["volumes"] = []any{map[string]any{"name": name, "emptyDir": map[string]any{}}}
	return out
}

// trMount returns props with one entry of volumeMounts, of a volume named
// extra at the path. props is not changed.
func trMount(props map[string]any, mountPath string) map[string]any {
	out := maps.Clone(props)
	if out == nil {
		out = map[string]any{}
	}
	out["volumeMounts"] = []any{map[string]any{"name": "extra", "mountPath": mountPath}}
	return out
}

// trImage is a thanos-ruler image from the one registry ptStrictPolicy allows:
// under it an unset image is refused (TestThanosRuler_UnsetImage).
const trImage = "registry.example/thanos/thanos:v0.39.2"

// trReloader is an image from that registry for the config-reloader container
// the operator generates: under ptStrictPolicy it is refused unless a listed
// entry patches it with an allowed image (TestThanosRuler_ReloaderImage).
const trReloader = "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"

// trReloaders returns props with the reloader patched with trReloader, so that
// a document built under ptStrictPolicy is not refused for it: an entry of the
// reloader's name that names no image takes it, and one is appended where
// containers has none. props is not changed.
func trReloaders(props map[string]any) map[string]any {
	out := maps.Clone(props)
	entries, _ := out["containers"].([]any)
	entries = slices.Clone(entries)
	patched := false
	for i, entry := range entries {
		if c, ok := entry.(map[string]any); ok && c["name"] == "config-reloader" {
			patched = true
			if _, named := c["image"]; !named {
				c = maps.Clone(c)
				c["image"] = trReloader
				entries[i] = c
			}
		}
	}
	if !patched {
		entries = append(entries, map[string]any{"name": "config-reloader", "image": trReloader})
	}
	out["containers"] = entries
	return out
}

// thanosRulerOf builds the ThanosRuler of props under the given policies, in
// turn (generateCoreKindUnder).
func thanosRulerOf(t *testing.T, props map[string]any, policies ...oam.Policy) *monitoringv1.ThanosRuler {
	t.Helper()
	obj := generateCoreKindUnder(t, &components.ThanosRulerHandler{}, "thanosruler", "main", props, policies...)
	tr, ok := obj.(*monitoringv1.ThanosRuler)
	if !ok {
		t.Fatalf("the kind built a %T, want a ThanosRuler", obj)
	}
	return tr
}

// thanosRulerThrough is the one ThanosRuler the transform builds for
// component web from props, under no policy.
func thanosRulerThrough(t *testing.T, props map[string]any) (*monitoringv1.ThanosRuler, error) {
	t.Helper()
	objs, err := policyFreeTransform("thanosruler", &components.ThanosRulerHandler{}, nil, oam.Component{Name: "web", Properties: props})
	if err != nil {
		return nil, err
	}
	var found []client.Object
	for _, obj := range objs {
		if _, ok := obj.(*monitoringv1.ThanosRuler); ok {
			found = append(found, obj)
		}
	}
	if len(objs) != 1 || len(found) != 1 {
		t.Fatalf("built %d objects, %d of them a ThanosRuler; want the one ThanosRuler", len(objs), len(found))
	}
	return found[0].(*monitoringv1.ThanosRuler), nil
}

// TestThanosRuler_LabelsAndExternalLabels: through the transform, `labels` and
// `externalLabels` authored together land in two places: `labels` on the
// ThanosRuler's own metadata, beside the component label, and externalLabels
// in the spec's labels, the Prometheus external labels, as authored. The
// engine's checks of object labels do not reach the external labels: a key it
// refuses on the object is an external label like any other.
func TestThanosRuler_LabelsAndExternalLabels(t *testing.T) {
	key := oam.ComponentLabelKeyForDomain("")
	tr, err := thanosRulerThrough(t, map[string]any{
		"labels":         map[string]any{"team": "payments"},
		"externalLabels": map[string]any{"cluster": "eu-1", "team": "rules", key: "other"},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got, want := tr.GetLabels(), map[string]string{"team": "payments", key: "web"}; !maps.Equal(got, want) {
		t.Errorf("the object's labels = %v, want the authored labels and the component label: %v", got, want)
	}
	if got, want := tr.Spec.Labels, map[string]string{"cluster": "eu-1", "team": "rules", key: "other"}; !maps.Equal(got, want) {
		t.Errorf("the external labels = %v, want the authored externalLabels: %v", got, want)
	}

	// Each alone stays in its own place.
	alone, err := thanosRulerThrough(t, map[string]any{"externalLabels": map[string]any{"cluster": "eu-1"}})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got, want := alone.GetLabels(), map[string]string{key: "web"}; !maps.Equal(got, want) {
		t.Errorf("externalLabels alone: the object's labels = %v, want the component label alone", got)
	}
	unlabelled, err := thanosRulerThrough(t, map[string]any{"labels": map[string]any{"team": "payments"}})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if unlabelled.Spec.Labels != nil {
		t.Errorf("labels alone: the external labels = %v, want none", unlabelled.Spec.Labels)
	}

	// The object's labels keep the engine's checks.
	if _, err := thanosRulerThrough(t, map[string]any{"labels": map[string]any{key: "other"}}); err == nil {
		t.Error("an object label that claims another component built; want the engine's refusal")
	}
}

// TestThanosRuler_ExternalLabelsDecodeStrictly: externalLabels is decoded as
// the spec is, under the null contract, and its refusal names it.
func TestThanosRuler_ExternalLabelsDecodeStrictly(t *testing.T) {
	h := &components.ThanosRulerHandler{}
	for name, value := range map[string]any{
		"a list":   []any{"cluster"},
		"a string": "cluster=eu-1",
		"a number": map[string]any{"replica": 1},
	} {
		err := coreKindErr(h, "thanosruler", "main", map[string]any{"externalLabels": value})
		if err == nil || !strings.Contains(err.Error(), "externalLabels") || strings.Contains(err.Error(), "ThanosRulerSpec.labels") {
			t.Errorf("%s: err = %v, want a refusal that names externalLabels and not the spec field", name, err)
		}
	}
	if tr := thanosRulerOf(t, map[string]any{"externalLabels": nil}); tr.Spec.Labels != nil {
		t.Errorf("a null externalLabels built %v, want none", tr.Spec.Labels)
	}
	if tr := thanosRulerOf(t, map[string]any{"externalLabels": map[string]any{"cluster": "eu-1", "replica": nil}}); !maps.Equal(tr.Spec.Labels, map[string]string{"cluster": "eu-1"}) {
		t.Errorf("a null external label built %v, want it absent", tr.Spec.Labels)
	}
}

// TestThanosRuler_ExcludedGroupFilled: an entry of excludedFromEnforcement that
// leaves its group out, or writes it null, carries the API's default, the one
// group it allows; the type would write an empty group, which the API refuses.
// An authored group is kept as authored, another one than the default too (the
// API, not the kind, refuses it). An authored empty group is refused by the
// entry's index, in either spelling of the key, and not repaired.
func TestThanosRuler_ExcludedGroupFilled(t *testing.T) {
	tr := thanosRulerOf(t, map[string]any{"excludedFromEnforcement": []any{
		map[string]any{"resource": "prometheusrules", "namespace": "monitoring"},
		map[string]any{"group": nil, "resource": "servicemonitors", "namespace": "monitoring"},
		map[string]any{"group": "monitoring.coreos.com", "resource": "probes", "namespace": "monitoring"},
		map[string]any{"group": "example.com", "resource": "podmonitors", "namespace": "monitoring"},
	}})
	want := []string{"monitoring.coreos.com", "monitoring.coreos.com", "monitoring.coreos.com", "example.com"}
	got := make([]string, 0, len(tr.Spec.ExcludedFromEnforcement))
	for _, ref := range tr.Spec.ExcludedFromEnforcement {
		got = append(got, ref.Group)
	}
	if !slices.Equal(got, want) {
		t.Errorf("excludedFromEnforcement groups = %q, want %q", got, want)
	}
	h := &components.ThanosRulerHandler{}
	for key, wantErr := range map[string]string{
		"group": "excludedFromEnforcement[1].group: empty: the API admits only monitoring.coreos.com",
		"Group": "excludedFromEnforcement[1].Group: empty: the API admits only monitoring.coreos.com",
	} {
		err := coreKindErr(h, "thanosruler", "main", map[string]any{"excludedFromEnforcement": []any{
			map[string]any{"resource": "prometheusrules", "namespace": "monitoring"},
			map[string]any{key: "", "resource": "servicemonitors", "namespace": "monitoring"},
		}})
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s authored empty: err = %v, want %q", key, err, wantErr)
		}
	}
	// A direct caller's typed collections and pointers are read as the decode
	// reads them, so an empty group in any of them is refused too, not filled.
	empty := ""
	for name, refs := range map[string]any{
		"typed list": []map[string]any{
			{"resource": "prometheusrules", "namespace": "monitoring"},
			{"group": "", "resource": "servicemonitors", "namespace": "monitoring"},
		},
		"typed entry": []any{
			map[string]any{"resource": "prometheusrules", "namespace": "monitoring"},
			map[string]string{"group": "", "resource": "servicemonitors", "namespace": "monitoring"},
		},
		"pointer group": []any{
			map[string]any{"resource": "prometheusrules", "namespace": "monitoring"},
			map[string]any{"group": &empty, "resource": "servicemonitors", "namespace": "monitoring"},
		},
	} {
		err := coreKindErr(h, "thanosruler", "main", map[string]any{"excludedFromEnforcement": refs})
		wantErr := "excludedFromEnforcement[1].group: empty: the API admits only monitoring.coreos.com"
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, wantErr)
		}
	}
}

// changingGroup encodes as null the first time and as "" after.
type changingGroup struct{ calls int }

func (v *changingGroup) MarshalJSON() ([]byte, error) {
	v.calls++
	if v.calls == 1 {
		return []byte("null"), nil
	}
	return []byte(`""`), nil
}

// TestThanosRuler_ExcludedGroupSerializedOnce: the empty-group refusal and the
// decode read one serialization of the properties, so a value whose encoder
// answers differently on another call is read once, as the decode reads it:
// here null, an absent group, which the build fills; the "" it would encode
// next is never read.
func TestThanosRuler_ExcludedGroupSerializedOnce(t *testing.T) {
	group := &changingGroup{}
	err := coreKindErr(&components.ThanosRulerHandler{}, "thanosruler", "main", map[string]any{"excludedFromEnforcement": []any{
		map[string]any{"resource": "prometheusrules", "namespace": "monitoring"},
		map[string]any{"group": group, "resource": "servicemonitors", "namespace": "monitoring"},
	}})
	if err != nil {
		t.Fatalf("err = %v, want none: the one serialization reads the group as null, an absent group", err)
	}
	if group.calls != 1 {
		t.Errorf("the group was serialized %d times, want 1", group.calls)
	}
}

// TestThanosRuler_Unauthored: a component that authors nothing builds a
// ThanosRuler whose spec holds no image, no replica count and no storage, so
// the operator's own defaults apply and no policy default is written; and the
// one field the type always encodes, empty.
func TestThanosRuler_Unauthored(t *testing.T) {
	two := int32(2)
	defaulting := &stubPolicy{
		defaultReplicas: &two, defaultCPURequest: "100m", defaultMemoryRequest: "64Mi",
		defaultCPULimit: "1", defaultMemoryLimit: "128Mi", defaultStorageSize: "1Gi",
	}
	tr := thanosRulerOf(t, map[string]any{}, defaulting, nil)
	if tr.Spec.Image != "" || tr.Spec.Replicas != nil || tr.Spec.Storage != nil {
		t.Errorf("image = %q, replicas = %v, storage = %v; want none of them written", tr.Spec.Image, tr.Spec.Replicas, tr.Spec.Storage)
	}
	if len(tr.Spec.Resources.Requests) != 0 || len(tr.Spec.Resources.Limits) != 0 {
		t.Errorf("resources = %+v, want no default of the policy filled", tr.Spec.Resources)
	}
	spec, _ := policyFreeJSON(t, tr)["spec"].(map[string]any)
	if len(spec) != 1 || spec["resources"] == nil || len(spec["resources"].(map[string]any)) != 0 {
		t.Errorf("spec = %v, want the one field the type encodes whether or not it was authored: resources, empty", spec)
	}
}

// TestThanosRuler_PolicyRefusals: under an environment policy, what the spec
// says of the pods is refused as a workload kind's own fields are, with the
// class of the refusal and the path of the property, and so is the one
// credential the spec holds in the clear. Without a policy the same component
// builds.
func TestThanosRuler_PolicyRefusals(t *testing.T) {
	const image = trImage
	container := func(list string, c map[string]any) map[string]any { return map[string]any{list: []any{c}} }
	claim := func(size string) map[string]any {
		return map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": size}},
		}}}
	}
	remoteWrite := func(entries ...map[string]any) map[string]any {
		list := make([]any, 0, len(entries))
		for _, e := range entries {
			list = append(list, e)
		}
		return map[string]any{"remoteWrite": list}
	}
	cases := []struct {
		name  string
		props map[string]any
		class oam.RefusalClass
		want  string
	}{
		{"image outside the allowed registries", map[string]any{"image": "other.example/thanos/thanos:v0.39.2"}, oam.RefusalRegistry, "image: "},
		{"replicas over the maximum", map[string]any{"replicas": 4}, oam.RefusalReplicaMaximum, "replicas 4 exceeds enforced maximum 3"},
		{"claim over the storage maximum", map[string]any{"storage": claim("1Ti")}, oam.RefusalStorageMaximum, "storage.volumeClaimTemplate.spec.resources.requests.storage"},
		{"ephemeral claim over the storage maximum", map[string]any{"storage": map[string]any{"ephemeral": claim("1Ti")}}, oam.RefusalStorageMaximum, "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage"},
		{"cpu over the maximum", map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "4"}}}, oam.RefusalResourceMaximum, "resources: "},
		{"hostPath volume", map[string]any{"volumes": []any{map[string]any{"name": "host", "hostPath": map[string]any{"path": "/etc"}}}}, oam.RefusalHostPath, "hostPath"},
		{"image volume outside the allowed registries", map[string]any{"volumes": []any{map[string]any{"name": "data", "image": map[string]any{"reference": "other.example/team/data:1.0.0"}}}}, oam.RefusalRegistry, "other.example"},
		{"container image outside the allowed registries", container("containers", map[string]any{"name": "proxy", "image": "other.example/team/proxy:1.2.3"}), oam.RefusalRegistry, "proxy"},
		{"init container image outside the allowed registries", container("initContainers", map[string]any{"name": "prepare", "image": "other.example/team/prepare:1.0.0"}), oam.RefusalRegistry, "prepare"},
		// A patch of a container the operator generates is held like any other.
		{"patch over the memory maximum", container("containers", map[string]any{"name": "config-reloader", "resources": map[string]any{"limits": map[string]any{"memory": "2Gi"}}}), oam.RefusalResourceMaximum, "config-reloader"},
		{"privileged patch", container("containers", map[string]any{"name": "thanos-ruler", "securityContext": map[string]any{"privileged": true}}), oam.RefusalPrivileged, "thanos-ruler"},
		{"forbidden capability", container("containers", map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "securityContext": map[string]any{"capabilities": map[string]any{"add": []any{"NET_ADMIN"}}}}), oam.RefusalContainerCapability, "NET_ADMIN"},
		// The deprecated bearer token, the one credential in the clear, by the
		// index of its entry.
		{"a bearer token", remoteWrite(
			map[string]any{"url": "https://a.example.com/api/v1/write"},
			map[string]any{"url": "https://b.example.com/api/v1/write", "bearerToken": "s3cr3t"},
		), oam.RefusalExplicitSecret, "remoteWrite[1].bearerToken: holds a credential in the object, and the environment policy forbids explicit secrets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"image": image}
			maps.Copy(props, tc.props)
			props = trReloaders(props)
			_, err := pvTransform("thanosruler", &components.ThanosRulerHandler{}, props, esPolicy{stubPolicy: ptStrictPolicy()})
			rcWantClass(t, err, tc.class)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
			// Without a policy applied, the same component builds.
			thanosRulerOf(t, props)
		})
	}
}

// TestThanosRuler_CredentialsStatedNotHeld: a credential under a name that
// does not say so is not read, under a policy that forbids explicit secrets:
// a header value and the user information of a URL build. Every other
// credential of the spec is the key of a Secret or the path of a file in the
// container.
func TestThanosRuler_CredentialsStatedNotHeld(t *testing.T) {
	tr := thanosRulerOf(t, trReloaders(map[string]any{
		"image": trImage,
		"remoteWrite": []any{map[string]any{
			"url":     "https://user:s3cr3t@metrics.example.com/api/v1/write",
			"headers": map[string]any{"X-Api-Key": "s3cr3t"},
		}},
		"queryEndpoints":   []any{"https://user:s3cr3t@query.example.com"},
		"alertmanagersUrl": []any{"https://user:s3cr3t@alertmanager.example.com"},
	}), esPolicy{stubPolicy: ptStrictPolicy()})
	if got := tr.Spec.RemoteWrite[0].Headers["X-Api-Key"]; got != "s3cr3t" {
		t.Errorf("the header = %q, want it carried as authored", got)
	}
}

// TestThanosRuler_EmptyBearerTokenHoldsNone: an authored empty bearerToken is
// the absent field, as an authored "" is everywhere in this package. The field
// is omitted when empty, so the object carries no token and holds no
// credential: the component builds under a policy that forbids explicit
// secrets, and the emitted entry has no bearerToken. A non-empty one is
// refused (TestThanosRuler_PolicyRefusals).
func TestThanosRuler_EmptyBearerTokenHoldsNone(t *testing.T) {
	tr := thanosRulerOf(t, trReloaders(map[string]any{
		"image":       trImage,
		"remoteWrite": []any{map[string]any{"url": "https://metrics.example.com/api/v1/write", "bearerToken": ""}},
	}), esPolicy{stubPolicy: ptStrictPolicy()})
	spec, _ := policyFreeJSON(t, tr)["spec"].(map[string]any)
	entries, _ := spec["remoteWrite"].([]any)
	if len(entries) != 1 {
		t.Fatalf("spec.remoteWrite = %v, want the one authored entry", spec["remoteWrite"])
	}
	entry, _ := entries[0].(map[string]any)
	if _, ok := entry["bearerToken"]; ok || entry["url"] != "https://metrics.example.com/api/v1/write" {
		t.Errorf("spec.remoteWrite[0] = %v, want its url and no bearerToken", entry)
	}
}

// TestThanosRuler_UnsetImage: a spec that names no image, a null one or an
// empty one leaves the image to the operator, which no registry allowlist
// reaches: it is refused with the registry class under a policy with allowed
// registries, and builds under one without and under none, where no image is
// written. A listed container named for one the operator generates, as here,
// is merged into it and may name no image.
func TestThanosRuler_UnsetImage(t *testing.T) {
	patch := []any{map[string]any{"name": "thanos-ruler", "resources": map[string]any{"limits": map[string]any{"memory": "256Mi"}}}}
	h := &components.ThanosRulerHandler{}
	for name, props := range map[string]map[string]any{
		"unset": trReloaders(map[string]any{"version": "v0.39.2"}),
		"null":  trReloaders(map[string]any{"version": "v0.39.2", "image": nil}),
		"empty": trReloaders(map[string]any{"version": "v0.39.2", "image": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			if tr := thanosRulerOf(t, props); tr.Spec.Image != "" {
				t.Errorf("image = %q under no policy, want none written", tr.Spec.Image)
			}
			_, err := pvTransform("thanosruler", h, props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), "image: unset") {
				t.Errorf("err = %v, want one naming the unset image", err)
			}
			open := ptStrictPolicy()
			open.allowedRegistries = nil
			if _, err := pvTransform("thanosruler", h, props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
		})
	}
	tr := thanosRulerOf(t, trReloaders(map[string]any{"image": trImage, "containers": patch}), ptStrictPolicy())
	if tr.Spec.Containers[0].Image != "" {
		t.Errorf("container image = %q, want none written", tr.Spec.Containers[0].Image)
	}
}

// TestThanosRuler_PatchedImage: the operator merges an entry of containers
// named thanos-ruler into the container it generates (makeStatefulSetSpec,
// pkg/thanos/statefulset.go:494 at prometheus-operator v0.94.1), so an image
// named there is the one that runs. With image unset it answers for it: held
// to the allowed registries as any listed container's image is, and not
// refused as unset.
func TestThanosRuler_PatchedImage(t *testing.T) {
	h := &components.ThanosRulerHandler{}
	patched := func(image string) map[string]any {
		return trReloaders(map[string]any{"containers": []any{map[string]any{"name": "thanos-ruler", "image": image}}})
	}
	tr := thanosRulerOf(t, patched(trImage), ptStrictPolicy())
	if tr.Spec.Image != "" || tr.Spec.Containers[0].Image != trImage {
		t.Errorf("image = %q, patch image = %q; want none and the authored one", tr.Spec.Image, tr.Spec.Containers[0].Image)
	}
	_, err := pvTransform("thanosruler", h, patched("other.example/thanos/thanos:v0.39.2"), ptStrictPolicy())
	rcWantClass(t, err, oam.RefusalRegistry)
	if err != nil && !strings.Contains(err.Error(), "other.example") {
		t.Errorf("err = %v, want one naming the patch's image", err)
	}
	// The patch's image replaces image, which then never runs and is not
	// held, to the registries or to the tag rule.
	for _, image := range []string{"other.example/thanos/thanos:v0.39.2", "registry.example/thanos/thanos:latest"} {
		props := patched(trImage)
		props["image"] = image
		if _, err := pvTransform("thanosruler", h, props, ptStrictPolicy()); err != nil {
			t.Errorf("image %q replaced by a patch: %v, want it built", image, err)
		}
		thanosRulerOf(t, props)
	}
}

// trResourcesPatch is an entry of containers that patches the thanos-ruler
// container's resources.
func trResourcesPatch(resources map[string]any) map[string]any {
	return map[string]any{"name": "thanos-ruler", "resources": resources}
}

// TestThanosRuler_ReloaderImage: the operator generates config-reloader from
// the image of its own configuration, which no allowlist reaches, wherever the
// ruler has a rule ConfigMap, and it makes at least one. Under a policy with
// allowed registries it is refused with the registry class unless an entry of
// containers of its name patches it with an image, which is then held to the
// registries (an entry of initContainers of that name is refused: the API
// requires init container and container names to be unique together). Under a
// policy without allowed registries, and under none, no patch is needed.
func TestThanosRuler_ReloaderImage(t *testing.T) {
	h := &components.ThanosRulerHandler{}
	open := ptStrictPolicy()
	open.allowedRegistries = nil
	const unset = "the image of the config-reloader container (containers): unset, so the Prometheus operator chooses the image the pods run, which the allowed registries [registry.example] cannot hold; name an image from one of them"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unpatched": {map[string]any{"image": trImage}, unset},
		"patched without an image": {
			map[string]any{"image": trImage, "containers": []any{map[string]any{"name": "config-reloader", "resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}}}},
			unset,
		},
		"patched outside the allowed registries": {
			map[string]any{"image": trImage, "containers": []any{map[string]any{"name": "config-reloader", "image": "other.example/prometheus-config-reloader:v0.94.1"}}},
			`containers[0] "config-reloader"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("thanosruler", h, tc.props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
			if _, err := pvTransform("thanosruler", h, tc.props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
			thanosRulerOf(t, tc.props)
		})
	}
	if _, err := pvTransform("thanosruler", h, trReloaders(map[string]any{"image": trImage}), ptStrictPolicy()); err != nil {
		t.Errorf("the reloader patched with an allowed image: %v, want it built", err)
	}
}

// TestThanosRuler_OperatorDefaultsHeld: where the spec leaves the memory
// request unset, the Prometheus operator fills 200Mi (makeStatefulSet,
// pkg/thanos/statefulset.go:62-67 at prometheus-operator v0.94.1); where it
// leaves the replica count unset, the operator copies none into the
// StatefulSet (:507), whose API default is 1. Both values are held to the
// policy's maxima, and neither is written into the object.
func TestThanosRuler_OperatorDefaultsHeld(t *testing.T) {
	h := &components.ThanosRulerHandler{}
	zero := ptStrictPolicy()
	zero.maxReplicas = int32ptr(0)
	small := ptStrictPolicy()
	small.maxMemory = "128Mi"
	exact := ptStrictPolicy()
	exact.maxMemory = "200Mi"
	for name, tc := range map[string]struct {
		props  map[string]any
		policy *stubPolicy
		class  oam.RefusalClass
		want   string // "" when the component builds
	}{
		"replicas unset under a maximum of 0":    {map[string]any{}, zero, oam.RefusalReplicaMaximum, "replicas 1 exceeds enforced maximum 0"},
		"replicas 0 under a maximum of 0":        {map[string]any{"replicas": 0}, zero, "", ""},
		"memory request unset under 128Mi":       {map[string]any{}, small, oam.RefusalResourceMaximum, `resources, whose unset memory request the Prometheus operator fills as 200Mi: memory request "200Mi" exceeds enforced maximum "128Mi"`},
		"memory request unset, a limit of 200Mi": {map[string]any{"resources": map[string]any{"limits": map[string]any{"memory": "200Mi"}}}, exact, "", ""},
		"memory request unset under 200Mi":       {map[string]any{}, exact, "", ""},
		"memory request authored under 128Mi":    {map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "64Mi"}}}, small, "", ""},
		"memory request authored over 128Mi":     {map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "256Mi"}}}, small, oam.RefusalResourceMaximum, `resources: memory request "256Mi" exceeds enforced maximum "128Mi"`},
		// A request a patch of the thanos-ruler container names replaces the
		// one the operator fills, and is held with the spec's limit.
		"memory request of a patch under 128Mi": {map[string]any{"containers": []any{trResourcesPatch(map[string]any{"requests": map[string]any{"memory": "64Mi"}})}}, small, "", ""},
		"memory request of a patch over 128Mi":  {map[string]any{"containers": []any{trResourcesPatch(map[string]any{"requests": map[string]any{"memory": "256Mi"}})}}, small, oam.RefusalResourceMaximum, `resources with containers[0] "thanos-ruler" merged over it: memory request "256Mi" exceeds enforced maximum "128Mi"`},
		"a patch that names no memory request, under 128Mi": {map[string]any{"containers": []any{trResourcesPatch(map[string]any{"limits": map[string]any{"cpu": "1"}})}}, small, oam.RefusalResourceMaximum,
			`resources with containers[0] "thanos-ruler" merged over it, whose unset memory request the Prometheus operator fills as 200Mi`},
	} {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"image": trImage}
			maps.Copy(props, tc.props)
			props = trReloaders(props)
			_, err := pvTransform("thanosruler", h, props, tc.policy)
			if tc.want == "" {
				if err != nil {
					t.Errorf("err = %v, want it built", err)
				}
				return
			}
			rcWantClass(t, err, tc.class)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	tr := thanosRulerOf(t, trReloaders(map[string]any{"image": trImage}), exact)
	if tr.Spec.Replicas != nil || len(tr.Spec.Resources.Requests) != 0 {
		t.Errorf("replicas = %v, requests = %v; want neither written", tr.Spec.Replicas, tr.Spec.Resources.Requests)
	}
}

// TestThanosRuler_PodMetadata: through the transform, the metadata the
// operator copies onto the pods is the author's and nothing else. The object
// itself takes the component label; podMetadata takes none, so the pods carry
// it only where the author writes it.
func TestThanosRuler_PodMetadata(t *testing.T) {
	key := oam.ComponentLabelKeyForDomain("")
	for name, tc := range map[string]struct {
		props map[string]any
		want  *monitoringv1.EmbeddedObjectMetadata
	}{
		"unauthored": {map[string]any{}, nil},
		"labels": {
			map[string]any{"podMetadata": map[string]any{"labels": map[string]any{"team": "payments"}}},
			&monitoringv1.EmbeddedObjectMetadata{Labels: map[string]string{"team": "payments"}},
		},
		"the component label with the component's own value": {
			map[string]any{"podMetadata": map[string]any{"labels": map[string]any{key: "web"}}},
			&monitoringv1.EmbeddedObjectMetadata{Labels: map[string]string{key: "web"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr, err := thanosRulerThrough(t, tc.props)
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if tc.want == nil && tr.Spec.PodMetadata != nil || tc.want != nil && (tr.Spec.PodMetadata == nil || !maps.Equal(tr.Spec.PodMetadata.Labels, tc.want.Labels)) {
				t.Errorf("podMetadata = %+v, want it as authored: %+v", tr.Spec.PodMetadata, tc.want)
			}
			if got := tr.GetLabels(); !maps.Equal(got, map[string]string{key: "web"}) {
				t.Errorf("the object's labels = %v, want the component label alone", got)
			}
		})
	}
}

// TestThanosRuler_Name: the operator names the data volume
// thanos-ruler-<name>-data, unless a claim template's name names it, and the
// first rule ConfigMap and its volume thanos-ruler-<name>-rulefiles-0; the API
// refuses a volume name that is not a DNS-1123 label. A name either breaks is
// refused, naming the component or the objectName it came from; the longest
// the rule ConfigMap's volume allows, 38 characters, builds.
func TestThanosRuler_Name(t *testing.T) {
	h := &components.ThanosRulerHandler{}
	named := trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": trClaim}})
	for name, tc := range map[string]struct {
		component string
		props     map[string]any
		want      string
	}{
		"a component name over 38 characters": {strings.Repeat("a", 39), nil,
			`thanosruler "` + strings.Repeat("a", 39) + `": the component name is the ThanosRuler's name, and the Prometheus operator names the volume of the first rule ConfigMap "thanos-ruler-` + strings.Repeat("a", 39) + `-rulefiles-0", which must be a DNS-1123 label`},
		"a component name over 45 characters": {strings.Repeat("a", 46), nil,
			`the Prometheus operator names the data volume "thanos-ruler-` + strings.Repeat("a", 46) + `-data", which must be a DNS-1123 label`},
		"a dotted objectName": {"web", map[string]any{oam.ObjectNameProperty: "rules.example"},
			`"rules.example" is not a valid name for this ThanosRuler: the Prometheus operator names the data volume "thanos-ruler-rules.example-data"`},
		"a dotted objectName and a named claim template": {"web", map[string]any{oam.ObjectNameProperty: "rules.example", "storage": named["storage"]},
			`the Prometheus operator names the volume of the first rule ConfigMap "thanos-ruler-rules.example-rulefiles-0"`},
		"a rule ConfigMap's volume of the objectName": {"web", trVolume(map[string]any{oam.ObjectNameProperty: "rules"}, "thanos-ruler-rules-rulefiles-0"),
			`volumes[0] "thanos-ruler-rules-rulefiles-0": the name is a volume the Prometheus operator adds for a rule ConfigMap it generates`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := policyFreeTransform("thanosruler", h, nil, oam.Component{Name: tc.component, Properties: tc.props})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	for name, tc := range map[string]struct {
		component string
		props     map[string]any
	}{
		"38 characters":                      {strings.Repeat("a", 38), nil},
		"38 characters and a named template": {strings.Repeat("a", 38), named},
		// The component's name is not the object's.
		"a rule ConfigMap's volume of the component name": {"web", trVolume(map[string]any{oam.ObjectNameProperty: "rules"}, "thanos-ruler-web-rulefiles-0")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := policyFreeTransform("thanosruler", h, nil, oam.Component{Name: tc.component, Properties: tc.props}); err != nil {
				t.Fatalf("err = %v, want it built", err)
			}
		})
	}
}

// TestThanosRuler_OperatorRunsIt: the specs beside each refusal of what the
// operator or the API would refuse, that both run as written, build.
func TestThanosRuler_OperatorRunsIt(t *testing.T) {
	h := &components.ThanosRulerHandler{}
	for name, props := range map[string]map[string]any{
		"zero replicas":                                    {"replicas": 0},
		"a port name of 15 characters":                     {"portName": "thanosrulerwebx"},
		"the gRPC port name nowhere":                       {"portName": "grpc", "listenLocal": true, "serviceName": "rules"},
		"an invalid port name nowhere":                     {"portName": "thanos-ruler-web", "listenLocal": true, "serviceName": "rules"},
		"a patched port of the web port's name and number": {"containers": []any{map[string]any{"name": "thanos-ruler", "ports": []any{map[string]any{"name": "web", "containerPort": 10902}}}}},
		// A request a patch of the thanos-ruler container names replaces the
		// 200Mi the operator fills, and is held with the spec's limit.
		"a patch's memory request under the spec's limit": {
			"resources":  map[string]any{"limits": map[string]any{"memory": "100Mi"}},
			"containers": []any{map[string]any{"name": "thanos-ruler", "resources": map[string]any{"requests": map[string]any{"memory": "64Mi"}}}},
		},
		// Listening locally, the operator gives the reloader no port.
		"a patched reloader port on a pod listening locally": {"listenLocal": true, "containers": []any{map[string]any{"name": "config-reloader", "ports": []any{map[string]any{"name": "reloader-web", "containerPort": 9000}}}}},
		"emptyDir alone":                   trStorage(map[string]any{"emptyDir": map[string]any{}}),
		"a claim template of its own name": trStorage(map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": trClaim}}),
		"the operator's name for the data volume beside emptyDir": trStorage(map[string]any{
			"emptyDir":            map[string]any{},
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "thanos-ruler-fast-data"}},
		}),
		// The operator adds a Secret key's volume only where the field is set,
		// and not where its file field takes precedence.
		"the query configuration's volume name without queryConfig":                    trVolume(nil, "query-config"),
		"the Alertmanager configuration's volume name without alertmanagersConfig":     trVolume(nil, "alertmanager-config"),
		"the alert relabeling configuration's volume name without alertRelabelConfigs": trVolume(nil, "alertrelabel-config"),
		"the alert relabeling configuration's volume name beside its file": trVolume(map[string]any{
			"alertRelabelConfigs": secretKey("thanos-alert-relabel", "relabel.yml"), "alertRelabelConfigFile": "/etc/thanos/relabel.yml",
		}, "alertrelabel-config"),
		"the object storage configuration's volume name beside its file": trVolume(map[string]any{
			"objectStorageConfig": secretKey("thanos-objstore", "objstore.yml"), "objectStorageConfigFile": "/etc/thanos/objstore.yml",
		}, "objstorage-config"),
		"the object storage configuration's path beside its file": trMount(map[string]any{
			"objectStorageConfig": secretKey("thanos-objstore", "objstore.yml"), "objectStorageConfigFile": "/etc/thanos/objstore.yml",
		}, "/etc/thanos/config/objstorage-config"),
		// The operator numbers its rule ConfigMaps with %d, from 0.
		"a rule ConfigMap volume name of a leading 0":  trVolume(nil, "thanos-ruler-fast-rulefiles-01"),
		"a rule ConfigMap volume name of no number":    trVolume(nil, "thanos-ruler-fast-rulefiles-x"),
		"a rule ConfigMap volume of another ruler":     trVolume(nil, "thanos-ruler-other-rulefiles-0"),
		"a mount beside the operator's configuration":  trMount(nil, "/etc/thanos/config/extra"),
		"a mount below the rule ConfigMaps' directory": trMount(nil, "/etc/thanos/rules/extra"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := coreKindErr(h, "thanosruler", "fast", props); err != nil {
				t.Fatalf("err = %v, want it built", err)
			}
		})
	}
}
