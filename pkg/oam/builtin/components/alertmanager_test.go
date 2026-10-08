package components_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The alertmanager kind (go-kure/launcher#790): the fixtures the shared tests
// of kind_policy_free_test.go build it from, and what the kind does beyond the
// shared helper: the three image fields it refuses, the environment policy on
// the pods the Prometheus operator runs for it, and the pods' metadata.

// alertmanagerUnfixtured names the fields of the spec the full fixture cannot
// set.
var alertmanagerUnfixtured = map[string]string{
	"baseImage":   "not in the schema; refused when not empty, and an empty one writes nothing (TestAlertmanager_DeprecatedImageFields)",
	"tag":         "not in the schema; refused when not empty, and an empty one writes nothing (TestAlertmanager_DeprecatedImageFields)",
	"sha":         "not in the schema; refused when not empty, and an empty one writes nothing (TestAlertmanager_DeprecatedImageFields)",
	"hostNetwork": "true is refused under the policy the fixture is built under, and the type omits false (TestAlertmanager_HostNetwork)",
}

// alertmanagerFull sets every other top-level field of the spec, inside
// ptStrictPolicy: images of registry.example with a tag, the two reloaders
// patched with one, three replicas, no more than 2 cpu, 1Gi of memory and 10Gi
// of storage, and nothing of the host.
func alertmanagerFull() map[string]any {
	webTLS := map[string]any{
		"keySecret": secretKey("alertmanager-tls", "tls.key"),
		"cert":      map[string]any{"secret": secretKey("alertmanager-tls", "tls.crt")},
	}
	return map[string]any{
		"podMetadata": map[string]any{
			"labels":      map[string]any{"team": "payments"},
			"annotations": map[string]any{"example.com/owner": "sre"},
		},
		"image":            "registry.example/prometheus/alertmanager:v0.28.1",
		"imagePullPolicy":  "IfNotPresent",
		"version":          "v0.28.1",
		"imagePullSecrets": []any{map[string]any{"name": "registry-credentials"}},
		"secrets":          []any{"alertmanager-tls"},
		"configMaps":       []any{"alertmanager-templates"},
		"configSecret":     "alertmanager-main",
		"logLevel":         "info",
		"logFormat":        "json",
		"replicas":         3,
		"retention":        "72h",
		"storage": map[string]any{"volumeClaimTemplate": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "monitoring"}},
			"spec": map[string]any{
				"accessModes": []any{"ReadWriteOnce"}, "storageClassName": "fast",
				"resources": map[string]any{"requests": map[string]any{"storage": "10Gi"}},
			},
		}},
		"volumes":                              []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{"sizeLimit": "1Gi"}}},
		"volumeMounts":                         []any{map[string]any{"name": "scratch", "mountPath": "/scratch"}},
		"persistentVolumeClaimRetentionPolicy": map[string]any{"whenDeleted": "Delete", "whenScaled": "Retain"},
		"externalUrl":                          "https://alerts.example.com",
		"routePrefix":                          "/alerts",
		"paused":                               true,
		"nodeSelector":                         map[string]any{"kubernetes.io/os": "linux"},
		"schedulerName":                        "default-scheduler",
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "100m", "memory": "128Mi"},
			"limits":   map[string]any{"cpu": 2, "memory": "1Gi"},
		},
		"affinity": map[string]any{"podAntiAffinity": map[string]any{
			"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
				"topologyKey":   "kubernetes.io/hostname",
				"labelSelector": map[string]any{"matchLabels": map[string]any{"alertmanager": "main"}},
			}},
		}},
		"tolerations": []any{map[string]any{"key": "dedicated", "operator": "Equal", "value": "monitoring", "effect": "NoSchedule"}},
		"topologySpreadConstraints": []any{map[string]any{
			"maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway",
			"labelSelector": map[string]any{"matchLabels": map[string]any{"alertmanager": "main"}},
		}},
		"securityContext":     map[string]any{"runAsNonRoot": true, "runAsUser": 1000, "fsGroup": 2000},
		"dnsPolicy":           "None",
		"dnsConfig":           map[string]any{"nameservers": []any{"192.0.2.53"}, "searches": []any{"example.com"}, "options": []any{map[string]any{"name": "ndots", "value": "2"}}},
		"enableServiceLinks":  false,
		"serviceName":         "alertmanager",
		"serviceAccountName":  "alertmanager",
		"listenLocal":         true,
		"podManagementPolicy": "OrderedReady",
		"updateStrategy":      map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": 1}},
		"containers": []any{
			// A patch of the container the operator generates: no image.
			map[string]any{"name": "alertmanager", "readinessProbe": map[string]any{"periodSeconds": 5}},
			map[string]any{
				"name": "proxy", "image": "registry.example/team/proxy:1.2.3",
				"resources":       map[string]any{"limits": map[string]any{"cpu": "500m", "memory": "64Mi"}},
				"securityContext": map[string]any{"capabilities": map[string]any{"drop": []any{"ALL"}}},
			},
			// The reloader's patch, with an image from the allowed registry.
			map[string]any{"name": "config-reloader", "image": amReloader},
		},
		"initContainers": []any{
			map[string]any{"name": "prepare", "image": "registry.example/team/prepare:1.0.0"},
			map[string]any{"name": "init-config-reloader", "image": amReloader},
		},
		"priorityClassName":                   "monitoring",
		"additionalPeers":                     []any{"alertmanager.other.example.com:9094"},
		"clusterAdvertiseAddress":             "192.0.2.10:9094",
		"clusterGossipInterval":               "200ms",
		"clusterLabel":                        "main",
		"clusterPushpullInterval":             "1m",
		"clusterPeerTimeout":                  "15s",
		"clusterPeerName":                     "$(POD_NAME)",
		"portName":                            "http-web",
		"forceEnableClusterMode":              true,
		"alertmanagerConfigSelector":          map[string]any{"matchLabels": map[string]any{"alertmanager": "main"}},
		"alertmanagerConfigNamespaceSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": "team", "operator": "In", "values": []any{"payments"}}}},
		"alertmanagerConfigMatcherStrategy":   map[string]any{"type": "None"},
		"minReadySeconds":                     0,
		"hostAliases":                         []any{map[string]any{"ip": "192.0.2.20", "hostnames": []any{"smtp.internal"}}},
		"web": map[string]any{
			"tlsConfig": webTLS, "httpConfig": map[string]any{"http2": true},
			"getConcurrency": 0, "timeout": 30,
		},
		"limits": map[string]any{"maxSilences": 100, "maxPerSilenceBytes": "1MB"},
		"clusterTLS": map[string]any{
			"server": webTLS,
			"client": map[string]any{"ca": map[string]any{"configMap": secretKey("alertmanager-ca", "ca.crt")}, "serverName": "alertmanager"},
		},
		"alertmanagerConfiguration": map[string]any{
			"name": "main",
			"global": map[string]any{
				"resolveTimeout": "5m",
				"smtp": map[string]any{
					"from": "alerts@example.com", "smartHost": map[string]any{"host": "smtp.example.com", "port": "587"},
					"authUsername": "alerts", "authPassword": secretKey("smtp", "password"),
				},
			},
			"templates": []any{map[string]any{"configMap": secretKey("alertmanager-templates", "main.tmpl")}},
		},
		"automountServiceAccountToken":  false,
		"enableFeatures":                []any{"classic-mode"},
		"additionalArgs":                []any{map[string]any{"name": "log.level", "value": "debug"}},
		"terminationGracePeriodSeconds": 0,
		"hostUsers":                     false,
	}
}

// alertmanagerReaches names references of the full fixture's object that the
// copy test must find (TestPolicyFreeKinds_GenerateCopies).
var alertmanagerReaches = []string{
	".Spec.PodMetadata", ".Spec.PodMetadata.Labels", ".Spec.PodMetadata.Annotations", ".Spec.Image", ".Spec.Replicas",
	".Spec.Storage", ".Spec.Storage.VolumeClaimTemplate.EmbeddedObjectMetadata.Labels",
	".Spec.Storage.VolumeClaimTemplate.Spec.Resources.Requests", ".Spec.Volumes", ".Spec.Volumes[0].VolumeSource.EmptyDir",
	".Spec.NodeSelector", ".Spec.Resources.Limits", ".Spec.Affinity", ".Spec.SecurityContext", ".Spec.SecurityContext.RunAsUser",
	".Spec.DNSConfig", ".Spec.DNSConfig.Options", ".Spec.UpdateStrategy", ".Spec.UpdateStrategy.RollingUpdate.MaxUnavailable",
	".Spec.Containers", ".Spec.Containers[0].ReadinessProbe", ".Spec.Containers[1].SecurityContext.Capabilities.Drop",
	".Spec.InitContainers", ".Spec.AlertmanagerConfigSelector", ".Spec.AlertmanagerConfigNamespaceSelector.MatchExpressions[0].Values",
	".Spec.MinReadySeconds", ".Spec.HostAliases[0].Hostnames", ".Spec.Web", ".Spec.Web.Timeout", ".Spec.Limits.MaxSilences",
	".Spec.ClusterTLS", ".Spec.AlertmanagerConfiguration", ".Spec.AlertmanagerConfiguration.Global.SMTPConfig.SmartHost",
	".Spec.AlertmanagerConfiguration.Templates", ".Spec.AdditionalArgs", ".Spec.TerminationGracePeriodSeconds", ".Spec.HostUsers",
}

// alertmanagerRefusals are the kind's refusal cases with or without an
// environment policy (TestPolicyFreeKinds_Refusals). The kind requires no
// top-level field.
func alertmanagerRefusals(notA string) []struct {
	name  string
	props map[string]any
	want  string
} {
	const deprecated = ": not authorable: the Prometheus operator deprecates the field, and composes the image it yields outside what the object states; use image"
	container := func(c map[string]any) map[string]any { return map[string]any{"containers": []any{c}} }
	smtp := func(smartHost map[string]any) map[string]any {
		return map[string]any{"alertmanagerConfiguration": map[string]any{"global": map[string]any{"smtp": map[string]any{"smartHost": smartHost}}}}
	}
	return []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"unknown key", map[string]any{"replicaCount": 3}, notA + "monitoring.coreos.com/v1 AlertmanagerSpec"},
		{"the object's spec", map[string]any{"spec": map[string]any{"replicas": 3}}, notA},
		{"replicas a string", map[string]any{"replicas": "three"}, notA},
		{"storage sub-key", map[string]any{"storage": map[string]any{"size": "10Gi"}}, notA},
		{"container sub-key", container(map[string]any{"name": "proxy", "registry": "registry.example"}), notA},
		{"bad quantity", map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "lots"}}}, notA},
		{"null container", map[string]any{"containers": []any{map[string]any{"name": "proxy"}, nil}}, "containers[1]"},
		{"two spellings", map[string]any{"replicas": 1, "Replicas": 2}, "sets the same field as"},

		{"baseImage", map[string]any{"baseImage": "registry.example/prometheus/alertmanager"}, "baseImage" + deprecated},
		{"tag", map[string]any{"image": "registry.example/prometheus/alertmanager:v0.28.1", "tag": "v0.28.1"}, "tag" + deprecated},
		{"sha", map[string]any{"sha": "0123456789abcdef"}, "sha" + deprecated},

		{"argument without a name", map[string]any{"additionalArgs": []any{map[string]any{"value": "debug"}}}, "additionalArgs[0].name: required"},
		{"cluster TLS without a server", map[string]any{"clusterTLS": map[string]any{"client": map[string]any{}}}, "clusterTLS.server: required"},
		{"cluster TLS without a client", map[string]any{"clusterTLS": map[string]any{"server": map[string]any{}}}, "clusterTLS.client: required"},
		{"DNS option without a name", map[string]any{"dnsConfig": map[string]any{"options": []any{map[string]any{"value": "2"}}}}, "dnsConfig.options[0].name: required"},
		{"host alias without an address", map[string]any{"hostAliases": []any{map[string]any{"hostnames": []any{"smtp.internal"}}}}, "hostAliases[0].ip: required"},
		{"a later host alias without host names", map[string]any{"hostAliases": []any{
			map[string]any{"ip": "192.0.2.20", "hostnames": []any{"smtp.internal"}}, map[string]any{"ip": "192.0.2.21"},
		}}, "hostAliases[1].hostnames: required"},
		{"update strategy without a type", map[string]any{"updateStrategy": map[string]any{"rollingUpdate": map[string]any{"maxUnavailable": 1}}}, "updateStrategy.type: required"},
		{"smart host without a host", smtp(map[string]any{"port": "587"}), "alertmanagerConfiguration.global.smtp.smartHost.host: required"},
		{"smart host without a port", smtp(map[string]any{"host": "smtp.example.com"}), "alertmanagerConfiguration.global.smtp.smartHost.port: required"},

		{"image without a tag", map[string]any{"image": "registry.example/prometheus/alertmanager"},
			`image: image "registry.example/prometheus/alertmanager" rejected: no tag or digest specified`},
		{"image tagged latest", map[string]any{"image": "registry.example/prometheus/alertmanager:latest"},
			`image: image "registry.example/prometheus/alertmanager:latest" rejected: :latest tag not allowed`},
		{"container image tagged latest", container(map[string]any{"name": "proxy", "image": "registry.example/team/proxy:latest"}),
			`containers[0] "proxy": image "registry.example/team/proxy:latest" rejected: :latest tag not allowed`},
		{"init container image without a tag", map[string]any{"initContainers": []any{map[string]any{"name": "prepare", "image": "registry.example/team/prepare"}}},
			`initContainers[0] "prepare": image "registry.example/team/prepare" rejected: no tag or digest specified`},
		{"image volume tagged latest", map[string]any{"volumes": []any{map[string]any{"name": "data", "image": map[string]any{"reference": "registry.example/team/data:latest"}}}},
			`"registry.example/team/data:latest" rejected: :latest tag not allowed`},
		{"request over its limit", map[string]any{"resources": map[string]any{
			"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"},
		}}, "resources: cpu: request 2 must not exceed limit 1"},
		// An unnamed entry merges into no generated container, so its block is
		// checked alone, as is any added container's.
		{"request over its limit in an unnamed container", container(map[string]any{"name": "", "image": "registry.example/team/proxy:v1", "resources": map[string]any{
			"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"},
		}}), `containers[0] "": resources: cpu: request 2 must not exceed limit 1`},
		{"request over its limit in an unnamed init container beside a merged patch", map[string]any{
			"containers":     []any{map[string]any{"name": "alertmanager", "resources": map[string]any{"limits": map[string]any{"cpu": "1"}}}},
			"initContainers": []any{map[string]any{"name": "", "image": "registry.example/team/prepare:v1", "resources": map[string]any{"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"}}}},
		}, `initContainers[0] "": resources: cpu: request 2 must not exceed limit 1`},
		// An image without its version: the operator chooses the flags for
		// its own default version (pkg/alertmanager/statefulset.go:272 and
		// :383-392 at v0.94.1), so a v0.28.1 image would be passed a flag of
		// v0.30.0.
		{"image without a version", map[string]any{"image": "quay.io/prometheus/alertmanager:v0.28.1"},
			"version: required where image, or an entry of containers named alertmanager, names the image"},
		{"image of a patch without a version", container(map[string]any{"name": "alertmanager", "image": "quay.io/prometheus/alertmanager:v0.28.1"}),
			"version: required where image, or an entry of containers named alertmanager, names the image"},
		// The operator mounts the data volume under the claim template's name
		// whatever arm is in use, and creates it under its own for emptyDir
		// and ephemeral (statefulset.go:174-198 and :531-535).
		{"a named claim template beside emptyDir", map[string]any{"storage": map[string]any{
			"emptyDir":            map[string]any{},
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": amClaim},
		}}, "storage.volumeClaimTemplate.metadata.name: refused beside storage.emptyDir or storage.ephemeral"},
		{"a named claim template beside ephemeral", map[string]any{"storage": map[string]any{
			"ephemeral":           map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{"accessModes": []any{"ReadWriteOnce"}}}},
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": amClaim},
		}}, "storage.volumeClaimTemplate.metadata.name: refused beside storage.emptyDir or storage.ephemeral"},
		// A volume named as one the operator adds to the pods
		// (statefulset.go:215, :510-529, :617-690, :698-738).
		{"a volume named as the configuration's", amVolume("config-volume"),
			`volumes[0] "config-volume": the name is a volume the Prometheus operator adds to every Alertmanager's pods`},
		{"a volume named as the web configuration's", amVolume("web-config"),
			`volumes[0] "web-config": the name is a volume the Prometheus operator adds`},
		{"a volume named as a TLS credential's", amVolume("web-config-tls-secret-key-web"),
			`volumes[0] "web-config-tls-secret-key-web": names starting "web-config-tls-" are the Prometheus operator's`},
		{"a volume named as a listed Secret's", withAmVolume(map[string]any{"secrets": []any{"Alert.TLS"}}, "secret-alert-tls"),
			`volumes[0] "secret-alert-tls": the name is the volume the Prometheus operator adds for secrets[0]`},
		{"a volume named as a listed ConfigMap's", withAmVolume(map[string]any{"configMaps": []any{"templates"}}, "configmap-templates"),
			`volumes[0] "configmap-templates": the name is the volume the Prometheus operator adds for configMaps[0]`},
		{"a volume named as the templates'", withAmVolume(map[string]any{"alertmanagerConfiguration": map[string]any{
			"name": "global", "templates": []any{map[string]any{"configMap": map[string]any{"name": "templates", "key": "slack.tmpl"}}},
		}}, "notification-templates"),
			`volumes[0] "notification-templates": the name is the volume the Prometheus operator adds for alertmanagerConfiguration.templates`},
		{"a volume named as the data volume", amVolume("alertmanager-fast-db"),
			`volumes[0] "alertmanager-fast-db": the name is the data volume's`},
		{"a volume named as the claim template's", withAmVolume(map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": amClaim},
		}}, "data"), `volumes[0] "data": the name is the data volume's`},
		// A version the operator fails the reconcile on (operator.go:902-909,
		// statefulset.go:284).
		{"a version that does not parse", map[string]any{"version": "banana"},
			`version: "banana" is not a version the Prometheus operator can parse`},
		{"a version under 0.15.0", map[string]any{"version": "v0.14.0"},
			`version: "v0.14.0" is not supported by the Prometheus operator`},
		{"a version of major version 1", map[string]any{"version": "v1.0.0"},
			`version: "v1.0.0" is not supported by the Prometheus operator`},
		// A web port name the API refuses on the container (statefulset.go:483-500).
		{"a port name over 15 characters", map[string]any{"portName": "alertmanager-web"},
			`portName: "alertmanager-web" is not a valid port name`},
		{"a port name of the mesh", map[string]any{"portName": "mesh-tcp"},
			`portName: "mesh-tcp" is the name of a port the Prometheus operator adds`},
		// The operator rewrites a negative count to 0 (statefulset.go:137-139).
		{"negative replicas", map[string]any{"replicas": -1}, "replicas: -1 is below 0"},
		// A claim the API refuses (statefulset.go:191-212).
		{"a claim template without a storage request", map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": map[string]any{"accessModes": []any{"ReadWriteOnce"}}},
		}}, "storage.volumeClaimTemplate.spec.resources.requests.storage: required where neither storage.emptyDir nor storage.ephemeral is set"},
		{"an empty storage", map[string]any{"storage": map[string]any{}},
			"storage.volumeClaimTemplate.spec.resources.requests.storage: required"},
		{"a claim template with access modes written empty", map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": map[string]any{"accessModes": []any{}, "resources": amClaim["resources"]}},
		}}, "storage.volumeClaimTemplate.spec.accessModes: written empty"},
		{"an ephemeral arm without a claim template", map[string]any{"storage": map[string]any{"ephemeral": map[string]any{}}},
			"storage.ephemeral.volumeClaimTemplate: required"},
		{"an ephemeral claim without access modes", map[string]any{"storage": map[string]any{"ephemeral": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": amClaim},
		}}}, "storage.ephemeral.volumeClaimTemplate.spec.accessModes: required"},
		{"an ephemeral claim without a storage request", map[string]any{"storage": map[string]any{"ephemeral": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": map[string]any{"accessModes": []any{"ReadWriteOnce"}}},
		}}}, "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage: required"},
		// The operator fills an unset memory request as 200Mi whatever the
		// limit (pkg/alertmanager/statefulset.go:144-149 at v0.94.1).
		{"memory limit under the operator's request", map[string]any{"resources": map[string]any{
			"limits": map[string]any{"memory": "100Mi"},
		}}, "resources: memory: the unset request the Prometheus operator fills as 200Mi must not exceed limit 100Mi; name a request no larger than the limit"},
		// The operator merges a patch of the alertmanager container over the
		// block it builds it with (statefulset.go:762 and :817), so the two are
		// held as one.
		{"memory limit of a patch under the operator's request", container(map[string]any{"name": "alertmanager", "resources": map[string]any{
			"limits": map[string]any{"memory": "64Mi"},
		}}), `resources with containers[0] "alertmanager" merged over it: memory: the unset request the Prometheus operator fills as 200Mi must not exceed limit 64Mi`},
		{"request of a patch over the spec's limit", map[string]any{
			"resources":  map[string]any{"limits": map[string]any{"memory": "100Mi"}},
			"containers": []any{map[string]any{"name": "alertmanager", "resources": map[string]any{"requests": map[string]any{"memory": "256Mi"}}}},
		}, `resources with containers[0] "alertmanager" merged over it: resources: memory: request 256Mi must not exceed limit 100Mi`},
		{"extended resource of a patch with no limit in either block", container(map[string]any{"name": "alertmanager", "resources": map[string]any{
			"requests": map[string]any{"example.com/device": "1"},
		}}), `resources with containers[0] "alertmanager" merged over it: resources: example.com/device: limit must be set when request is set`},
		// The operator keeps the last entry of a name (MergePatchContainers,
		// pkg/k8s/merge.go at v0.94.1), so an earlier one would be held and not
		// run.
		{"a reloader listed twice", map[string]any{"containers": []any{
			map[string]any{"name": "config-reloader", "image": "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"},
			map[string]any{"name": "config-reloader"},
		}}, `containers[1] "config-reloader": the name is listed already at containers[0], and the Prometheus operator keeps only the last entry of a name; list each container once`},
		{"the init reloader listed twice", map[string]any{"initContainers": []any{
			map[string]any{"name": "init-config-reloader", "image": "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"},
			map[string]any{"name": "init-config-reloader"},
		}}, `initContainers[1] "init-config-reloader": the name is listed already at initContainers[0]`},
		{"alertmanager listed twice", map[string]any{"containers": []any{
			map[string]any{"name": "alertmanager", "image": "registry.example/prometheus/alertmanager:v0.28.1"},
			map[string]any{"name": "alertmanager"},
		}}, `containers[1] "alertmanager": the name is listed already at containers[0]`},
		{"a sidecar listed twice", map[string]any{"containers": []any{
			map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3"},
			map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3"},
		}}, `containers[1] "proxy": the name is listed already at containers[0]`},
		{"container request over its limit", container(map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "resources": map[string]any{
			"requests": map[string]any{"memory": "2Gi"}, "limits": map[string]any{"memory": "1Gi"},
		}}), `containers[0] "proxy": resources: memory: request 2Gi must not exceed limit 1Gi`},
		// A listed container that patches none the operator generates is added
		// as written, and a pod cannot run one without an image.
		{"container without an image", container(map[string]any{"name": "proxy"}),
			`containers[0] "proxy": names no image, and the Prometheus operator generates no container of that name to merge it into; name an image, or the container it patches (alertmanager, config-reloader)`},
		{"init container without an image", map[string]any{"initContainers": []any{map[string]any{"name": "prepare"}}},
			`initContainers[0] "prepare": names no image, and the Prometheus operator generates no container of that name to merge it into; name an image, or the container it patches (init-config-reloader)`},
		{"init reloader's name in containers", container(map[string]any{"name": "init-config-reloader"}),
			`containers[0] "init-config-reloader": names no image`},
		{"the sidecar of a Prometheus", container(map[string]any{"name": "thanos-sidecar"}),
			`containers[0] "thanos-sidecar": names no image`},
		// A duration the operator discards (0 or less) is refused, naming the field.
		{"retention of 0", map[string]any{"retention": "0h"},
			`retention: "0h" is not a positive duration: the Prometheus operator ignores it and runs the pods as if the field were unset; name a positive one, or leave it unset`},
		{"gossip interval of 0", map[string]any{"clusterGossipInterval": "0s"}, `clusterGossipInterval: "0s" is not a positive duration`},
		{"push-pull interval of 0", map[string]any{"clusterPushpullInterval": "0"}, `clusterPushpullInterval: "0" is not a positive duration`},
		{"negative peer timeout", map[string]any{"clusterPeerTimeout": "-15s"}, `clusterPeerTimeout: "-15s" is not a positive duration`},
		// An authored 0 the type omits, on a container that patches one of the
		// operator's own as on any other.
		{"probe period of 0 on a patch", container(map[string]any{"name": "alertmanager", "readinessProbe": map[string]any{"periodSeconds": 0}}),
			"containers[0].readinessProbe.periodSeconds: 0 cannot be carried by the "},
		// An authored "" on a string of the operator's own types that the CRD defaults.
		{"empty portName", map[string]any{"portName": ""},
			`portName: "" cannot be carried by the Prometheus operator API types (the field is omitted when zero, so the API server would apply its default "web")`},
	}
}

// amImage is an image from the one registry ptStrictPolicy allows: under it an
// unset image is refused (TestAlertmanager_UnsetImage).
const amImage = "registry.example/prometheus/alertmanager:v0.28.1"

// amVersion is the version amImage runs.
const amVersion = "v0.28.1"

// amClaim is the spec of a claim template that claims storage, as the claim
// template arm in use must.
var amClaim = map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}

// amReloader is an image from that registry for the two config-reloader
// containers the operator generates: under ptStrictPolicy each is refused
// unless a listed entry patches it with an allowed image
// (TestAlertmanager_ReloaderImages).
const amReloader = "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"

// amReloaders returns props with both reloaders patched with amReloader, so
// that a document built under ptStrictPolicy is not refused for them: an
// entry of a reloader's name that names no image takes it, and one is
// appended where the list has none. It names the version of amImage where
// props names none, as an authored image needs one. props is not changed.
func amReloaders(props map[string]any) map[string]any {
	out := maps.Clone(props)
	if _, named := out["version"]; !named {
		out["version"] = amVersion
	}
	for list, name := range map[string]string{"containers": "config-reloader", "initContainers": "init-config-reloader"} {
		entries, _ := out[list].([]any)
		entries = slices.Clone(entries)
		patched := false
		for i, entry := range entries {
			if c, ok := entry.(map[string]any); ok && c["name"] == name {
				patched = true
				if _, named := c["image"]; !named {
					c = maps.Clone(c)
					c["image"] = amReloader
					entries[i] = c
				}
			}
		}
		if !patched {
			entries = append(entries, map[string]any{"name": name, "image": amReloader})
		}
		out[list] = entries
	}
	return out
}

// amVolume is an Alertmanager that lists one emptyDir volume, named name.
func amVolume(name string) map[string]any {
	return withAmVolume(map[string]any{}, name)
}

// withAmVolume returns props with one emptyDir volume, named name, listed.
// props is not changed.
func withAmVolume(props map[string]any, name string) map[string]any {
	out := maps.Clone(props)
	out["volumes"] = []any{map[string]any{"name": name, "emptyDir": map[string]any{}}}
	return out
}

// amPatch is a listed entry that patches the alertmanager container with the
// resource block resources, and names no image.
func amPatch(resources map[string]any) map[string]any {
	return map[string]any{"name": "alertmanager", "resources": resources}
}

// alertmanagerOf builds the Alertmanager of props under the given policies, in
// turn (generateCoreKindUnder).
func alertmanagerOf(t *testing.T, props map[string]any, policies ...oam.Policy) *monitoringv1.Alertmanager {
	t.Helper()
	obj := generateCoreKindUnder(t, &components.AlertmanagerHandler{}, "alertmanager", "main", props, policies...)
	am, ok := obj.(*monitoringv1.Alertmanager)
	if !ok {
		t.Fatalf("the kind built a %T, want an Alertmanager", obj)
	}
	return am
}

// TestAlertmanager_DeprecatedImageFields: baseImage, tag and sha are refused
// when not empty, whatever else is authored, without a policy, and are
// no property of the kind's schema. The check of the authored properties
// against that schema, which kurel build runs first, refuses each as an
// unsupported field, an empty one included; converted without that check, an
// empty one is the object an absent one is, and builds.
func TestAlertmanager_DeprecatedImageFields(t *testing.T) {
	h := &components.AlertmanagerHandler{}
	schema := h.PropertySchema()
	authoredErr := func(props map[string]any) error {
		app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{{Name: "main", Type: "alertmanager", Properties: props}}}}
		return oam.NewTransformer(map[string]oam.ComponentHandler{"alertmanager": h}, nil).ValidateAuthoredProperties(app)
	}
	if err := authoredErr(map[string]any{"image": amImage}); err != nil {
		t.Fatalf("the authored check refuses a document with an image alone: %v", err)
	}
	for _, field := range []string{"baseImage", "tag", "sha"} {
		t.Run(field, func(t *testing.T) {
			if _, published := schema[field]; published {
				t.Errorf("the schema publishes %q, which the kind refuses", field)
			}
			want := field + ": not authorable: the Prometheus operator deprecates the field"
			for name, props := range map[string]map[string]any{
				"alone":         {field: "v0.28.1"},
				"beside image":  {field: "v0.28.1", "image": "registry.example/prometheus/alertmanager:v0.28.1"},
				"another value": {field: "registry.example/prometheus/alertmanager"},
			} {
				if err := coreKindErr(h, "alertmanager", "main", props); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "use image") {
					t.Errorf("%s: err = %v, want one mentioning %q and \"use image\"", name, err, want)
				}
			}
			am := alertmanagerOf(t, map[string]any{field: ""})
			if am.Spec.BaseImage != "" || am.Spec.Tag != "" || am.Spec.SHA != "" {
				t.Errorf("an empty %s built baseImage %q, tag %q, sha %q; want none", field, am.Spec.BaseImage, am.Spec.Tag, am.Spec.SHA)
			}
			if err := authoredErr(map[string]any{field: ""}); err == nil || !strings.Contains(err.Error(), `unsupported field "`+field+`"`) {
				t.Errorf("an empty %s through the authored check: err = %v, want it refused as an unsupported field", field, err)
			}
		})
	}
}

// TestAlertmanager_ResourceRefusalNamesItsPathOnce: the checks on a resource
// block name it `resources` themselves, and the block of the alertmanager
// container is the property of that name, so its refusal carries the word once.
func TestAlertmanager_ResourceRefusalNamesItsPathOnce(t *testing.T) {
	for name, block := range map[string]map[string]any{
		"request over its limit":                {"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"}},
		"extended request without a limit":      {"requests": map[string]any{"example.com/device": "1"}},
		"memory limit under the filled request": {"limits": map[string]any{"memory": "100Mi"}},
	} {
		err := coreKindErr(&components.AlertmanagerHandler{}, "alertmanager", "main", map[string]any{"resources": block})
		if err == nil || strings.Count(err.Error(), "resources: ") != 1 {
			t.Errorf("%s: err = %v, want one that names resources once", name, err)
		}
	}
}

// TestAlertmanager_Unauthored: a component that authors nothing builds an
// Alertmanager whose spec holds no image, no replica count and no storage, so
// the operator's own defaults apply and no policy default is written; and the
// two fields the type always encodes, empty.
func TestAlertmanager_Unauthored(t *testing.T) {
	two := int32(2)
	defaulting := &stubPolicy{
		defaultReplicas: &two, defaultCPURequest: "100m", defaultMemoryRequest: "64Mi",
		defaultCPULimit: "1", defaultMemoryLimit: "128Mi", defaultStorageSize: "1Gi",
	}
	am := alertmanagerOf(t, map[string]any{}, defaulting, nil)
	if am.Spec.Image != nil || am.Spec.Replicas != nil || am.Spec.Storage != nil {
		t.Errorf("image = %v, replicas = %v, storage = %v; want none of them written", am.Spec.Image, am.Spec.Replicas, am.Spec.Storage)
	}
	if len(am.Spec.Resources.Requests) != 0 || len(am.Spec.Resources.Limits) != 0 {
		t.Errorf("resources = %+v, want no default of the policy filled", am.Spec.Resources)
	}
	spec, _ := policyFreeJSON(t, am)["spec"].(map[string]any)
	want := map[string]any{"resources": map[string]any{}, "alertmanagerConfigMatcherStrategy": map[string]any{}}
	if !maps.EqualFunc(spec, want, func(a, b any) bool {
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		return aok && bok && len(am) == 0 && len(bm) == 0
	}) {
		t.Errorf("spec = %v, want %v: the two fields the type encodes whether or not they were authored", spec, want)
	}
}

// TestAlertmanager_AuthoredStorageWritesAClaimSkeleton: an authored storage
// block that names no claim template still encodes one, empty: the type holds
// it by value. The operator reads an emptyDir before it.
func TestAlertmanager_AuthoredStorageWritesAClaimSkeleton(t *testing.T) {
	am := alertmanagerOf(t, amReloaders(map[string]any{"image": amImage, "storage": map[string]any{"emptyDir": map[string]any{"sizeLimit": "1Ti"}}}), ptStrictPolicy())
	if am.Spec.Storage == nil || am.Spec.Storage.EmptyDir == nil {
		t.Fatalf("storage = %+v, want the authored emptyDir", am.Spec.Storage)
	}
	storage, _ := policyFreeJSON(t, am)["spec"].(map[string]any)["storage"].(map[string]any)
	if _, ok := storage["volumeClaimTemplate"]; !ok {
		t.Errorf("storage = %v, want the claim template the type always encodes", storage)
	}
}

// TestAlertmanager_PolicyRefusals: under an environment policy, what the spec
// says of the pods is refused as a workload kind's own fields are, with the
// class of the refusal and the path of the property. Without a policy the same
// component builds.
func TestAlertmanager_PolicyRefusals(t *testing.T) {
	const image = amImage
	container := func(list string, c map[string]any) map[string]any { return map[string]any{list: []any{c}} }
	claim := func(size string) map[string]any {
		return map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": size}},
		}}}
	}
	cases := []struct {
		name  string
		props map[string]any
		class oam.RefusalClass
		want  string
	}{
		{"image outside the allowed registries", map[string]any{"image": "other.example/prometheus/alertmanager:v0.28.1"}, oam.RefusalRegistry, "image: "},
		{"replicas over the maximum", map[string]any{"replicas": 4}, oam.RefusalReplicaMaximum, "replicas 4 exceeds enforced maximum 3"},
		{"claim over the storage maximum", map[string]any{"storage": claim("1Ti")}, oam.RefusalStorageMaximum, "storage.volumeClaimTemplate.spec.resources.requests.storage"},
		{"ephemeral claim over the storage maximum", map[string]any{"storage": map[string]any{"ephemeral": claim("1Ti")}}, oam.RefusalStorageMaximum, "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage"},
		{"cpu over the maximum", map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "4"}}}, oam.RefusalResourceMaximum, "resources: "},
		{"memory request over the maximum", map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "2Gi"}}}, oam.RefusalResourceMaximum, "resources: "},
		{"host network", map[string]any{"hostNetwork": true}, oam.RefusalHostNamespace, "hostNetwork"},
		{"hostPath volume", map[string]any{"volumes": []any{map[string]any{"name": "host", "hostPath": map[string]any{"path": "/etc"}}}}, oam.RefusalHostPath, "hostPath"},
		{"volume claim over the storage maximum", map[string]any{"volumes": []any{map[string]any{"name": "big", "ephemeral": claim("1Ti")}}}, oam.RefusalStorageMaximum, "big"},
		{"image volume outside the allowed registries", map[string]any{"volumes": []any{map[string]any{"name": "data", "image": map[string]any{"reference": "other.example/team/data:1.0.0"}}}}, oam.RefusalRegistry, "other.example"},
		{"container image outside the allowed registries", container("containers", map[string]any{"name": "proxy", "image": "other.example/team/proxy:1.2.3"}), oam.RefusalRegistry, "proxy"},
		{"init container image outside the allowed registries", container("initContainers", map[string]any{"name": "prepare", "image": "other.example/team/prepare:1.0.0"}), oam.RefusalRegistry, "prepare"},
		{"container cpu over the maximum", container("containers", map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "resources": map[string]any{"limits": map[string]any{"cpu": "4"}}}), oam.RefusalResourceMaximum, "proxy"},
		// A patch of a container the operator generates is held like any other.
		{"patch over the memory maximum", container("containers", map[string]any{"name": "config-reloader", "resources": map[string]any{"limits": map[string]any{"memory": "2Gi"}}}), oam.RefusalResourceMaximum, "config-reloader"},
		{"privileged patch", container("containers", map[string]any{"name": "alertmanager", "securityContext": map[string]any{"privileged": true}}), oam.RefusalPrivileged, "alertmanager"},
		{"privileged init container", container("initContainers", map[string]any{"name": "init-config-reloader", "securityContext": map[string]any{"privileged": true}}), oam.RefusalPrivileged, "init-config-reloader"},
		{"forbidden capability", container("containers", map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "securityContext": map[string]any{"capabilities": map[string]any{"add": []any{"NET_ADMIN"}}}}), oam.RefusalContainerCapability, "NET_ADMIN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"image": image}
			maps.Copy(props, tc.props)
			props = amReloaders(props)
			_, err := pvTransform("alertmanager", &components.AlertmanagerHandler{}, props, ptStrictPolicy())
			rcWantClass(t, err, tc.class)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
			// Without a policy applied, the same component builds.
			alertmanagerOf(t, props)
		})
	}
}

// TestAlertmanager_HostNetwork: an authored true is carried under a policy that
// allows the host network and refused under one that does not
// (TestAlertmanager_PolicyRefusals); an authored false is the object an absent
// one is, since the type omits it.
func TestAlertmanager_HostNetwork(t *testing.T) {
	am := alertmanagerOf(t, amReloaders(map[string]any{"image": amImage, "hostNetwork": true}), hostNetworkOK{ptStrictPolicy()})
	if !am.Spec.HostNetwork {
		t.Error("hostNetwork = false, want the authored true under a policy that allows the host network")
	}
	off := alertmanagerOf(t, amReloaders(map[string]any{"image": amImage, "hostNetwork": false}), ptStrictPolicy())
	if spec, _ := policyFreeJSON(t, off)["spec"].(map[string]any); spec["hostNetwork"] != nil {
		t.Errorf("hostNetwork = %v, want it omitted: the API reads an absent one as false", spec["hostNetwork"])
	}
}

// TestAlertmanager_HostProcess: a pod-level securityContext that asks for a
// Windows HostProcess pod is refused under a policy that does not allow
// privileged containers, as on a workload kind.
func TestAlertmanager_HostProcess(t *testing.T) {
	props := amReloaders(map[string]any{
		"image":           amImage,
		"hostNetwork":     true,
		"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}},
	})
	h := &components.AlertmanagerHandler{}
	_, err := pvTransform("alertmanager", h, props, hostNetworkOK{ptStrictPolicy()})
	rcWantClass(t, err, oam.RefusalPrivileged)
	allowing := ptStrictPolicy()
	allowing.allowPrivileged = true
	if _, err := pvTransform("alertmanager", h, props, hostNetworkOK{allowing}); err != nil {
		t.Errorf("under a policy that allows privileged containers: %v, want it built", err)
	}
}

// TestAlertmanager_UnsetImage: a spec that names no image, a null one or an
// empty one leaves the image to the operator, which no registry allowlist
// reaches: it is refused with the registry class under a policy with allowed
// registries, and builds under one without and under none, where an unset or
// null image writes none. A listed container named for one the operator
// generates, as here, is merged into it and may name no image.
func TestAlertmanager_UnsetImage(t *testing.T) {
	patch := []any{amPatch(map[string]any{"requests": map[string]any{"memory": "64Mi"}, "limits": map[string]any{"memory": "64Mi"}})}
	h := &components.AlertmanagerHandler{}
	for name, props := range map[string]map[string]any{
		"unset": amReloaders(map[string]any{"version": "v0.28.1"}),
		"null":  amReloaders(map[string]any{"version": "v0.28.1", "image": nil}),
		"empty": amReloaders(map[string]any{"version": "v0.28.1", "image": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			if name != "empty" {
				if am := alertmanagerOf(t, props); am.Spec.Image != nil {
					t.Errorf("image = %q under no policy, want none written", *am.Spec.Image)
				}
			}
			_, err := pvTransform("alertmanager", h, props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), "image: unset") {
				t.Errorf("err = %v, want one naming the unset image", err)
			}
			open := ptStrictPolicy()
			open.allowedRegistries = nil
			if _, err := pvTransform("alertmanager", h, props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
		})
	}
	am := alertmanagerOf(t, amReloaders(map[string]any{
		"image":      amImage,
		"containers": patch,
	}), ptStrictPolicy())
	if am.Spec.Containers[0].Image != "" {
		t.Errorf("container image = %q, want none written", am.Spec.Containers[0].Image)
	}
}

// TestAlertmanager_PatchedImage: the operator merges an entry of containers
// named alertmanager into the container it generates, so an image named there
// is the one that runs. With image unset it answers for it: held to the
// allowed registries as any listed container's image is, and not refused as
// unset.
func TestAlertmanager_PatchedImage(t *testing.T) {
	h := &components.AlertmanagerHandler{}
	patched := func(image string) map[string]any {
		return amReloaders(map[string]any{"containers": []any{map[string]any{"name": "alertmanager", "image": image}}})
	}
	am := alertmanagerOf(t, patched(amImage), ptStrictPolicy())
	if am.Spec.Image != nil || am.Spec.Containers[0].Image != amImage {
		t.Errorf("image = %v, patch image = %q; want none and the authored one", am.Spec.Image, am.Spec.Containers[0].Image)
	}
	_, err := pvTransform("alertmanager", h, patched("other.example/prometheus/alertmanager:v0.28.1"), ptStrictPolicy())
	rcWantClass(t, err, oam.RefusalRegistry)
	if err != nil && !strings.Contains(err.Error(), "other.example") {
		t.Errorf("err = %v, want one naming the patch's image", err)
	}
	// The patch's image replaces image, which then never runs and is not
	// held, to the registries or to the tag rule.
	for _, image := range []string{"other.example/prometheus/alertmanager:v0.28.1", "registry.example/prometheus/alertmanager:latest"} {
		props := patched(amImage)
		props["image"] = image
		if _, err := pvTransform("alertmanager", h, props, ptStrictPolicy()); err != nil {
			t.Errorf("image %q replaced by a patch: %v, want it built", image, err)
		}
		alertmanagerOf(t, props)
	}
}

// TestAlertmanager_Name: the operator names the data volume
// alertmanager-<name>-db, unless a claim template's name names it, and each
// pod alertmanager-<name>-<ordinal>; the API refuses a pod whose volume name or
// hostname is not a DNS-1123 label. A name either breaks is refused, naming the
// component or the objectName it came from; the longest the defaults allow, 47
// characters, builds, and 48 where a claim template names the data volume.
func TestAlertmanager_Name(t *testing.T) {
	h := &components.AlertmanagerHandler{}
	long := strings.Repeat("a", 48)
	named := map[string]any{"storage": map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": amClaim}}}
	for name, tc := range map[string]struct {
		component string
		props     map[string]any
		want      string
	}{
		"a component name over 47 characters": {long, nil,
			`alertmanager "` + long + `": the component name is the Alertmanager's name, and the Prometheus operator names the data volume "alertmanager-` + long + `-db", which must be a DNS-1123 label`},
		"a dotted objectName": {"web", map[string]any{oam.ObjectNameProperty: "alerts.example"},
			`"alerts.example" is not a valid name for this Alertmanager: the Prometheus operator names the data volume "alertmanager-alerts.example-db"`},
		"a named claim template and a long name": {strings.Repeat("a", 50), named,
			`the pod of the last replica takes the hostname "alertmanager-` + strings.Repeat("a", 50) + `-0", which must be a DNS-1123 label`},
		"eleven replicas and 48 characters": {strings.Repeat("a", 48), map[string]any{"replicas": 11, "storage": named["storage"]},
			`the pod of the last replica takes the hostname "alertmanager-` + strings.Repeat("a", 48) + `-10"`},
	} {
		t.Run(name, func(t *testing.T) {
			err := alertmanagerNamed(h, tc.component, tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	for name, tc := range map[string]struct {
		component string
		props     map[string]any
	}{
		"47 characters":                      {strings.Repeat("a", 47), nil},
		"48 characters and a named template": {strings.Repeat("a", 48), named},
		"a dotted name of no replica's pod":  {"web", map[string]any{oam.ObjectNameProperty: "alerts.example", "replicas": 0, "storage": named["storage"]}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := alertmanagerNamed(h, tc.component, tc.props); err != nil {
				t.Fatalf("err = %v, want it built", err)
			}
		})
	}
}

// TestAlertmanager_OperatorRunsIt: the specs beside each refusal of what the
// operator or the API would refuse or rewrite, that both run as written, build.
func TestAlertmanager_OperatorRunsIt(t *testing.T) {
	h := &components.AlertmanagerHandler{}
	for name, props := range map[string]map[string]any{
		"the least version supported":           {"version": "v0.15.0"},
		"a version without its patch":           {"version": "0.28"},
		"zero replicas":                         {"replicas": 0},
		"a port name of 15 characters":          {"portName": "alertmanagerweb"},
		"emptyDir alone":                        {"storage": map[string]any{"emptyDir": map[string]any{}}},
		"a claim template without access modes": {"storage": map[string]any{"volumeClaimTemplate": map[string]any{"spec": amClaim}}},
		"an ephemeral claim beside a dormant template": {"storage": map[string]any{
			"ephemeral": map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{
				"accessModes": []any{"ReadWriteOnce"}, "resources": amClaim["resources"],
			}}},
			"volumeClaimTemplate": map[string]any{"spec": map[string]any{}},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := coreKindErr(h, "alertmanager", "fast", props); err != nil {
				t.Fatalf("err = %v, want it built", err)
			}
		})
	}
}

// alertmanagerNamed builds an alertmanager component of the given name through
// the transform, which resolves objectName, and returns its error.
func alertmanagerNamed(h oam.ComponentHandler, component string, props map[string]any) error {
	_, err := policyFreeTransform("alertmanager", h, nil, oam.Component{Name: component, Properties: props})
	return err
}

// TestAlertmanager_ReloaderImages: the operator generates config-reloader and
// init-config-reloader from the image of its own configuration, which no
// allowlist reaches. Under a policy with allowed registries each is refused
// with the registry class unless a listed entry of its name patches it with an
// image, which is then held to the registries; under a policy without allowed
// registries, and under none, neither needs a patch.
func TestAlertmanager_ReloaderImages(t *testing.T) {
	h := &components.AlertmanagerHandler{}
	open := ptStrictPolicy()
	open.allowedRegistries = nil
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"config-reloader unpatched": {
			map[string]any{"image": amImage, "initContainers": []any{map[string]any{"name": "init-config-reloader", "image": amReloader}}},
			"the image of the config-reloader container (containers): unset, so the Prometheus operator chooses the image the pods run, which the allowed registries [registry.example] cannot hold; name an image from one of them",
		},
		"init-config-reloader unpatched": {
			map[string]any{"image": amImage, "containers": []any{map[string]any{"name": "config-reloader", "image": amReloader}}},
			"the image of the init-config-reloader container (initContainers): unset",
		},
		"config-reloader patched without an image": {
			map[string]any{"image": amImage,
				"containers":     []any{map[string]any{"name": "config-reloader", "resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}}},
				"initContainers": []any{map[string]any{"name": "init-config-reloader", "image": amReloader}}},
			"the image of the config-reloader container (containers): unset",
		},
		"init-config-reloader patched in containers": {
			map[string]any{"image": amImage, "containers": []any{
				map[string]any{"name": "config-reloader", "image": amReloader},
				map[string]any{"name": "init-config-reloader", "image": amReloader},
			}},
			"the image of the init-config-reloader container (initContainers): unset",
		},
		"config-reloader patched outside the allowed registries": {
			amReloaders(map[string]any{"image": amImage, "containers": []any{map[string]any{"name": "config-reloader", "image": "other.example/prometheus-config-reloader:v0.94.1"}}}),
			`containers[0] "config-reloader"`,
		},
	} {
		// image needs the version it runs.
		tc.props = maps.Clone(tc.props)
		tc.props["version"] = amVersion
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("alertmanager", h, tc.props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
			if _, err := pvTransform("alertmanager", h, tc.props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
			alertmanagerOf(t, tc.props)
		})
	}
	if _, err := pvTransform("alertmanager", h, amReloaders(map[string]any{"image": amImage}), ptStrictPolicy()); err != nil {
		t.Errorf("both reloaders patched with an allowed image: %v, want it built", err)
	}
}

// TestAlertmanager_StoragePrecedence: the operator uses the first storage arm
// that is set, emptyDir, then ephemeral, then volumeClaimTemplate, so the
// storage maximum holds the claim of that arm and none after it.
func TestAlertmanager_StoragePrecedence(t *testing.T) {
	h := &components.AlertmanagerHandler{}
	claim := func(size string) map[string]any {
		return map[string]any{"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": size}},
		}}
	}
	for name, tc := range map[string]struct {
		storage map[string]any
		want    string // "" when the component builds
	}{
		"an emptyDir before an oversized claim template":        {map[string]any{"emptyDir": map[string]any{}, "volumeClaimTemplate": claim("1Ti")}, ""},
		"an emptyDir before an oversized ephemeral claim":       {map[string]any{"emptyDir": map[string]any{}, "ephemeral": map[string]any{"volumeClaimTemplate": claim("1Ti")}}, ""},
		"an ephemeral claim before an oversized claim template": {map[string]any{"ephemeral": map[string]any{"volumeClaimTemplate": claim("1Gi")}, "volumeClaimTemplate": claim("1Ti")}, ""},
		"an oversized ephemeral claim before a claim template":  {map[string]any{"ephemeral": map[string]any{"volumeClaimTemplate": claim("1Ti")}, "volumeClaimTemplate": claim("1Gi")}, "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage"},
		"an oversized claim template, the only arm":             {map[string]any{"volumeClaimTemplate": claim("1Ti")}, "storage.volumeClaimTemplate.spec.resources.requests.storage"},
		"a claim template within the maximum, the only arm":     {map[string]any{"volumeClaimTemplate": claim("10Gi")}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("alertmanager", h, amReloaders(map[string]any{"image": amImage, "storage": tc.storage}), ptStrictPolicy())
			if tc.want == "" {
				if err != nil {
					t.Errorf("err = %v, want it built", err)
				}
				return
			}
			rcWantClass(t, err, oam.RefusalStorageMaximum)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestAlertmanager_OperatorDefaultsHeld: where the spec leaves the replica
// count or the memory request unset, the Prometheus operator fills 1 and 200Mi
// (pkg/alertmanager/statefulset.go:133-135 and :144-149 at prometheus-operator
// v0.94.1), and those values are held to the policy's maxima; neither is
// written into the object.
func TestAlertmanager_OperatorDefaultsHeld(t *testing.T) {
	h := &components.AlertmanagerHandler{}
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
		// A request a patch of the alertmanager container names replaces the
		// one the operator fills, and is held with the spec's limit.
		"memory request of a patch under 128Mi": {map[string]any{"containers": []any{amPatch(map[string]any{"requests": map[string]any{"memory": "64Mi"}})}}, small, "", ""},
		"memory request of a patch under the spec's limit": {map[string]any{
			"resources":  map[string]any{"limits": map[string]any{"memory": "100Mi"}},
			"containers": []any{amPatch(map[string]any{"requests": map[string]any{"memory": "64Mi"}})},
		}, small, "", ""},
		"memory request of a patch over 128Mi": {map[string]any{"containers": []any{amPatch(map[string]any{"requests": map[string]any{"memory": "256Mi"}})}}, small, oam.RefusalResourceMaximum, `resources with containers[0] "alertmanager" merged over it: memory request "256Mi" exceeds enforced maximum "128Mi"`},
		// The block is checked as the pods run it: the filled request names
		// memory beside hugepages, and a patch's extended request meets the
		// limit of resources it is merged with.
		"hugepages alone, the filled request naming memory": {map[string]any{"resources": map[string]any{
			"requests": map[string]any{"hugepages-2Mi": "64Mi"}, "limits": map[string]any{"hugepages-2Mi": "64Mi"},
		}}, exact, "", ""},
		"an extended request of a patch, its limit in resources": {map[string]any{
			"resources":  map[string]any{"limits": map[string]any{"example.com/device": "1"}},
			"containers": []any{amPatch(map[string]any{"requests": map[string]any{"example.com/device": "1"}})},
		}, exact, "", ""},
		"a patch that names no memory request, under 128Mi": {map[string]any{"containers": []any{amPatch(map[string]any{"limits": map[string]any{"cpu": "1"}})}}, small, oam.RefusalResourceMaximum, `resources with containers[0] "alertmanager" merged over it, whose unset memory request the Prometheus operator fills as 200Mi`},
	} {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"image": amImage}
			maps.Copy(props, tc.props)
			props = amReloaders(props)
			_, err := pvTransform("alertmanager", h, props, tc.policy)
			if tc.want == "" {
				if err != nil {
					t.Errorf("err = %v, want it built", err)
				}
				alertmanagerOf(t, props)
				return
			}
			rcWantClass(t, err, tc.class)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	am := alertmanagerOf(t, amReloaders(map[string]any{"image": amImage}), exact)
	if am.Spec.Replicas != nil || len(am.Spec.Resources.Requests) != 0 {
		t.Errorf("replicas = %v, requests = %v; want neither written", am.Spec.Replicas, am.Spec.Resources.Requests)
	}
}

// alertmanagerThrough is the one Alertmanager the transform builds for
// component web from props, under no policy.
func alertmanagerThrough(t *testing.T, props map[string]any) (*monitoringv1.Alertmanager, error) {
	t.Helper()
	objs, err := policyFreeTransform("alertmanager", &components.AlertmanagerHandler{}, nil, oam.Component{Name: "web", Properties: props})
	if err != nil {
		return nil, err
	}
	var found []client.Object
	for _, obj := range objs {
		if _, ok := obj.(*monitoringv1.Alertmanager); ok {
			found = append(found, obj)
		}
	}
	if len(objs) != 1 || len(found) != 1 {
		t.Fatalf("built %d objects, %d of them an Alertmanager; want the one Alertmanager", len(objs), len(found))
	}
	return found[0].(*monitoringv1.Alertmanager), nil
}

// TestAlertmanager_PodMetadata: through the transform, the metadata the
// operator copies onto the pods is the author's and nothing else. The object
// itself takes the component label; podMetadata takes none, so the pods carry
// it only where the author writes it. The metadata of a claim template is left
// as authored too.
func TestAlertmanager_PodMetadata(t *testing.T) {
	key := oam.ComponentLabelKeyForDomain("")
	for name, tc := range map[string]struct {
		props map[string]any
		want  *monitoringv1.EmbeddedObjectMetadata
	}{
		"unauthored": {map[string]any{}, nil},
		"annotations alone": {
			map[string]any{"podMetadata": map[string]any{"annotations": map[string]any{"example.com/owner": "sre"}}},
			&monitoringv1.EmbeddedObjectMetadata{Annotations: map[string]string{"example.com/owner": "sre"}},
		},
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
			am, err := alertmanagerThrough(t, tc.props)
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if !reflect.DeepEqual(am.Spec.PodMetadata, tc.want) {
				t.Errorf("podMetadata = %+v, want it as authored: %+v", am.Spec.PodMetadata, tc.want)
			}
			if got := am.GetLabels(); !maps.Equal(got, map[string]string{key: "web"}) {
				t.Errorf("the object's labels = %v, want the component label alone", got)
			}
		})
	}

	claimed, err := alertmanagerThrough(t, map[string]any{"storage": map[string]any{"volumeClaimTemplate": map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"tier": "monitoring"}},
		"spec":     map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}},
	}}})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got, want := claimed.Spec.Storage.VolumeClaimTemplate.Labels, map[string]string{"tier": "monitoring"}; !maps.Equal(got, want) {
		t.Errorf("the claim template's labels = %v, want the authored %v and no component label, as on a statefulset", got, want)
	}
}
