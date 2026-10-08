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

// The prometheus kind (go-kure/launcher#790): the fixtures the shared tests of
// kind_policy_free_test.go build it from, and what the kind does beyond the
// shared helper: the six image fields it refuses, the environment policy on
// the pods the Prometheus operator runs for it, the Thanos sidecar's included,
// the credentials its spec holds in the clear, the external labels under their
// own name, and the pods' metadata.

// prometheusUnfixtured names the fields of the spec the full fixture cannot
// set.
var prometheusUnfixtured = map[string]string{
	"baseImage":   "refused when not empty; an empty one writes nothing (TestPrometheus_DeprecatedImageFields)",
	"tag":         "refused when not empty; an empty one writes nothing (TestPrometheus_DeprecatedImageFields)",
	"sha":         "refused when not empty; an empty one writes nothing (TestPrometheus_DeprecatedImageFields)",
	"hostNetwork": "true is refused under the policy the fixture is built under, and the type omits false (TestPrometheus_HostNetwork)",
}

// prometheusFull sets every other top-level field of the spec, inside
// ptStrictPolicy: images of registry.example with a tag, one replica of each
// of two shards, no more than 2 cpu, 1Gi of memory and 10Gi of storage,
// nothing of the host, and no credential in the object.
func prometheusFull() map[string]any {
	webTLS := map[string]any{
		"keySecret": secretKey("prometheus-tls", "tls.key"),
		"cert":      map[string]any{"secret": secretKey("prometheus-tls", "tls.crt")},
	}
	selector := map[string]any{"matchLabels": map[string]any{"team": "payments"}}
	everywhere := map[string]any{}
	return map[string]any{
		"podMetadata": map[string]any{
			"labels":      map[string]any{"team": "payments"},
			"annotations": map[string]any{"example.com/owner": "sre"},
		},
		"serviceMonitorSelector":             selector,
		"serviceMonitorNamespaceSelector":    everywhere,
		"podMonitorSelector":                 selector,
		"podMonitorNamespaceSelector":        map[string]any{"matchExpressions": []any{map[string]any{"key": "team", "operator": "In", "values": []any{"payments"}}}},
		"probeSelector":                      selector,
		"probeNamespaceSelector":             everywhere,
		"scrapeConfigSelector":               selector,
		"scrapeConfigNamespaceSelector":      everywhere,
		"version":                            "v3.5.0",
		"paused":                             true,
		"image":                              "registry.example/prometheus/prometheus:v3.5.0",
		"imagePullPolicy":                    "IfNotPresent",
		"imagePullSecrets":                   []any{map[string]any{"name": "registry-credentials"}},
		"replicas":                           1,
		"shards":                             2,
		"shardingStrategy":                   map[string]any{"mode": "Address"},
		"replicaExternalLabelName":           "replica",
		"prometheusExternalLabelName":        "",
		"logLevel":                           "info",
		"logFormat":                          "json",
		"scrapeInterval":                     "15s",
		"scrapeTimeout":                      "10s",
		"scrapeProtocols":                    []any{"PrometheusProto", "OpenMetricsText1.0.0"},
		"externalLabels":                     map[string]any{"cluster": "eu-1"},
		"enableRemoteWriteReceiver":          true,
		"enableOTLPReceiver":                 true,
		"remoteWriteReceiverMessageVersions": []any{"V1.0", "V2.0"},
		"enableFeatures":                     []any{"exemplar-storage"},
		"externalUrl":                        "https://prometheus.example.com",
		"routePrefix":                        "/",
		"storage": map[string]any{"volumeClaimTemplate": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "monitoring"}},
			"spec": map[string]any{
				"accessModes": []any{"ReadWriteOnce"}, "storageClassName": "fast",
				"resources": map[string]any{"requests": map[string]any{"storage": "10Gi"}},
			},
		}},
		"volumes":                              []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{"sizeLimit": "1Gi"}}},
		"volumeMounts":                         []any{map[string]any{"name": "scratch", "mountPath": "/scratch"}},
		"persistentVolumeClaimRetentionPolicy": map[string]any{"whenDeleted": "Retain", "whenScaled": "Delete"},
		"web":                                  map[string]any{"tlsConfig": webTLS, "pageTitle": "Payments", "maxConnections": 512},
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "100m", "memory": "128Mi"},
			"limits":   map[string]any{"cpu": 2, "memory": "1Gi"},
		},
		"nodeSelector":                 map[string]any{"kubernetes.io/os": "linux"},
		"schedulerName":                "default-scheduler",
		"serviceAccountName":           "prometheus",
		"automountServiceAccountToken": true,
		"secrets":                      []any{"etcd-client"},
		"configMaps":                   []any{"scrape-targets"},
		"affinity": map[string]any{"podAntiAffinity": map[string]any{
			"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
				"topologyKey":   "kubernetes.io/hostname",
				"labelSelector": map[string]any{"matchLabels": map[string]any{"prometheus": "main"}},
			}},
		}},
		"tolerations": []any{map[string]any{"key": "dedicated", "operator": "Equal", "value": "monitoring", "effect": "NoSchedule"}},
		"topologySpreadConstraints": []any{map[string]any{
			"maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway",
			"labelSelector":            map[string]any{"matchLabels": map[string]any{"prometheus": "main"}},
			"additionalLabelSelectors": "OnShard",
		}},
		"remoteWrite": []any{map[string]any{
			"url":       "https://metrics.example.com/api/v1/write",
			"basicAuth": map[string]any{"username": secretKey("remote-write", "username"), "password": secretKey("remote-write", "password")},
			"headers":   map[string]any{"X-Scope-OrgID": "payments"},
		}},
		"otlp":                map[string]any{"promoteResourceAttributes": []any{"service.instance.id"}},
		"securityContext":     map[string]any{"runAsNonRoot": true, "runAsUser": 1000, "fsGroup": 2000},
		"dnsPolicy":           "None",
		"dnsConfig":           map[string]any{"nameservers": []any{"192.0.2.53"}, "searches": []any{"example.com"}, "options": []any{map[string]any{"name": "ndots", "value": "2"}}},
		"listenLocal":         true,
		"podManagementPolicy": "OrderedReady",
		"updateStrategy":      map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": 1}},
		"enableServiceLinks":  false,
		"containers": []any{
			// A patch of the container the operator generates: no image.
			map[string]any{"name": "prometheus", "readinessProbe": map[string]any{"periodSeconds": 5}},
			map[string]any{
				"name": "proxy", "image": "registry.example/team/proxy:1.2.3",
				"resources":       map[string]any{"limits": map[string]any{"cpu": "500m", "memory": "64Mi"}},
				"securityContext": map[string]any{"capabilities": map[string]any{"drop": []any{"ALL"}}},
			},
			// The reloaders, patched with an image of the allowed registry:
			// unpatched, the operator would choose theirs.
			map[string]any{"name": "config-reloader", "image": amReloader},
		},
		"initContainers": []any{
			map[string]any{"name": "prepare", "image": "registry.example/team/prepare:1.0.0"},
			map[string]any{"name": "init-config-reloader", "image": amReloader},
		},
		"additionalScrapeConfigs":            secretKey("prometheus-scrape", "scrape.yml"),
		"apiserverConfig":                    map[string]any{"host": "https://kubernetes.default.svc", "authorization": map[string]any{"credentials": secretKey("apiserver", "token")}},
		"priorityClassName":                  "monitoring",
		"portName":                           "http-web",
		"arbitraryFSAccessThroughSMs":        map[string]any{"deny": true},
		"overrideHonorLabels":                true,
		"overrideHonorTimestamps":            true,
		"ignoreNamespaceSelectors":           true,
		"enforcedNamespaceLabel":             "namespace",
		"enforcedSampleLimit":                100000,
		"enforcedTargetLimit":                1000,
		"enforcedLabelLimit":                 64,
		"enforcedLabelNameLengthLimit":       128,
		"enforcedLabelValueLengthLimit":      1024,
		"enforcedKeepDroppedTargets":         100,
		"enforcedBodySizeLimit":              "10MB",
		"nameValidationScheme":               "UTF8",
		"nameEscapingScheme":                 "Underscores",
		"convertClassicHistogramsToNHCB":     true,
		"scrapeNativeHistograms":             true,
		"scrapeClassicHistograms":            false,
		"minReadySeconds":                    0,
		"hostAliases":                        []any{map[string]any{"ip": "192.0.2.20", "hostnames": []any{"metrics.internal"}}},
		"additionalArgs":                     []any{map[string]any{"name": "storage.tsdb.no-lockfile"}},
		"walCompression":                     true,
		"excludedFromEnforcement":            []any{map[string]any{"group": "monitoring.coreos.com", "resource": "servicemonitors", "namespace": "monitoring", "name": "global"}},
		"podTargetLabels":                    []any{"team"},
		"tracingConfig":                      map[string]any{"endpoint": "tempo.monitoring.svc:4317", "clientType": "grpc", "samplingFraction": "0.1"},
		"bodySizeLimit":                      "5MB",
		"sampleLimit":                        50000,
		"targetLimit":                        500,
		"labelLimit":                         32,
		"labelNameLengthLimit":               64,
		"labelValueLengthLimit":              512,
		"keepDroppedTargets":                 50,
		"reloadStrategy":                     "HTTP",
		"maximumStartupDurationSeconds":      600,
		"scrapeClasses":                      []any{map[string]any{"name": "default", "default": true}},
		"serviceDiscoveryRole":               "EndpointSlice",
		"tsdb":                               map[string]any{"outOfOrderTimeWindow": "10m"},
		"scrapeFailureLogFile":               "scrape-failures.log",
		"serviceName":                        "prometheus",
		"runtime":                            map[string]any{"goGC": 75},
		"terminationGracePeriodSeconds":      0,
		"hostUsers":                          false,
		"retention":                          "15d",
		"retentionSize":                      "8GB",
		"retentionPercentage":                80,
		"shardRetentionPolicy":               map[string]any{"whenScaled": "Retain", "retain": map[string]any{"retentionPeriod": "3d"}},
		"disableCompaction":                  true,
		"rules":                              map[string]any{"alert": map[string]any{"forOutageTolerance": "1h", "forGracePeriod": "10m", "resendDelay": "1m"}},
		"prometheusRulesExcludedFromEnforce": []any{map[string]any{"ruleNamespace": "monitoring", "ruleName": "global"}},
		"ruleSelector":                       map[string]any{"matchLabels": map[string]any{"role": "alert-rules"}},
		"ruleNamespaceSelector":              everywhere,
		"query":                              map[string]any{"lookbackDelta": "5m", "maxConcurrency": 20, "timeout": "2m"},
		"alerting":                           map[string]any{"alertmanagers": []any{map[string]any{"namespace": "monitoring", "name": "alertmanager-operated", "port": "web"}}},
		"additionalAlertRelabelConfigs":      secretKey("prometheus-alert-relabel", "relabel.yml"),
		"additionalAlertManagerConfigs":      secretKey("prometheus-alertmanagers", "alertmanagers.yml"),
		"remoteRead": []any{map[string]any{
			"url":       "https://metrics.example.com/api/v1/read",
			"basicAuth": map[string]any{"username": secretKey("remote-read", "username"), "password": secretKey("remote-read", "password")},
			"headers":   map[string]any{"X-Scope-OrgID": "payments"},
		}},
		"thanos": map[string]any{
			"image":               "registry.example/thanos/thanos:v0.39.2",
			"version":             "v0.39.2",
			"resources":           map[string]any{"limits": map[string]any{"cpu": "500m", "memory": "256Mi"}},
			"objectStorageConfig": secretKey("thanos-objstore", "objstore.yml"),
			"blockSize":           "2h",
		},
		"queryLogFile":           "/dev/stdout",
		"allowOverlappingBlocks": true,
		"exemplars":              map[string]any{"maxSize": 100000},
		"evaluationInterval":     "30s",
		"ruleQueryOffset":        "30s",
		"enableAdminAPI":         true,
	}
}

// prometheusReaches names references of the full fixture's object that the
// copy test must find (TestPolicyFreeKinds_GenerateCopies).
var prometheusReaches = []string{
	".Spec.CommonPrometheusFields.PodMetadata.Labels", ".Spec.CommonPrometheusFields.ServiceMonitorSelector.MatchLabels",
	".Spec.CommonPrometheusFields.PodMonitorNamespaceSelector.MatchExpressions[0].Values",
	".Spec.CommonPrometheusFields.Image", ".Spec.CommonPrometheusFields.Replicas", ".Spec.CommonPrometheusFields.Shards",
	".Spec.CommonPrometheusFields.ShardingStrategy", ".Spec.CommonPrometheusFields.ReplicaExternalLabelName",
	".Spec.CommonPrometheusFields.ScrapeProtocols", ".Spec.CommonPrometheusFields.ExternalLabels",
	".Spec.CommonPrometheusFields.EnableOTLPReceiver", ".Spec.CommonPrometheusFields.Storage.VolumeClaimTemplate.Spec.Resources.Requests",
	".Spec.CommonPrometheusFields.Volumes[0].VolumeSource.EmptyDir", ".Spec.CommonPrometheusFields.Web",
	".Spec.CommonPrometheusFields.Resources.Limits", ".Spec.CommonPrometheusFields.Secrets",
	".Spec.CommonPrometheusFields.TopologySpreadConstraints[0].AdditionalLabelSelectors",
	".Spec.CommonPrometheusFields.RemoteWrite[0].Headers", ".Spec.CommonPrometheusFields.OTLP",
	".Spec.CommonPrometheusFields.Containers[0].ReadinessProbe", ".Spec.CommonPrometheusFields.APIServerConfig",
	".Spec.CommonPrometheusFields.EnforcedSampleLimit", ".Spec.CommonPrometheusFields.ExcludedFromEnforcement",
	".Spec.CommonPrometheusFields.TracingConfig.SamplingFraction", ".Spec.CommonPrometheusFields.ScrapeClasses",
	".Spec.CommonPrometheusFields.TSDB", ".Spec.CommonPrometheusFields.Runtime", ".Spec.CommonPrometheusFields.HostUsers",
	".Spec.RetentionPercentage", ".Spec.ShardRetentionPolicy.Retain", ".Spec.PrometheusRulesExcludedFromEnforce",
	".Spec.RuleSelector", ".Spec.Query", ".Spec.Alerting.Alertmanagers", ".Spec.RemoteRead[0].Headers",
	".Spec.Thanos", ".Spec.Thanos.Image", ".Spec.Thanos.Resources.Limits", ".Spec.Exemplars", ".Spec.RuleQueryOffset",
}

// prometheusRefusals are the kind's refusal cases with or without an
// environment policy (TestPolicyFreeKinds_Refusals). The kind requires no
// top-level field.
func prometheusRefusals(notA string) []struct {
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
		{"unknown key", map[string]any{"replicaCount": 3}, notA + "monitoring.coreos.com/v1 PrometheusSpec"},
		{"the object's spec", map[string]any{"spec": map[string]any{"replicas": 3}}, notA},
		{"shards a string", map[string]any{"shards": "two"}, notA},
		{"thanos sub-key", map[string]any{"thanos": map[string]any{"sidecar": true}}, notA},
		{"container sub-key", container(map[string]any{"name": "proxy", "registry": "registry.example"}), notA},
		{"null container", map[string]any{"containers": []any{map[string]any{"name": "proxy"}, nil}}, "containers[1]"},
		{"two spellings", map[string]any{"shards": 1, "Shards": 2}, "sets the same field as"},

		{"remote write without a URL", map[string]any{"remoteWrite": []any{map[string]any{"headers": map[string]any{"X-Scope-OrgID": "payments"}}}}, "remoteWrite[0].url: required"},
		{"remote read without a URL", map[string]any{"remoteRead": []any{map[string]any{"readRecent": true}}}, "remoteRead[0].url: required"},
		{"an Alertmanager endpoint without a name", map[string]any{"alerting": map[string]any{"alertmanagers": []any{map[string]any{"port": "web"}}}}, "alerting.alertmanagers[0].name: required"},
		{"a scrape class without a name", map[string]any{"scrapeClasses": []any{map[string]any{"default": true}}}, "scrapeClasses[0].name: required"},

		{"image without a tag", map[string]any{"image": "registry.example/prometheus/prometheus"},
			`image: image "registry.example/prometheus/prometheus" rejected: no tag or digest specified`},
		{"image tagged latest", map[string]any{"image": "registry.example/prometheus/prometheus:latest"},
			`image: image "registry.example/prometheus/prometheus:latest" rejected: :latest tag not allowed`},
		{"sidecar image tagged latest", map[string]any{"thanos": map[string]any{"image": "registry.example/thanos/thanos:latest"}},
			`thanos.image: image "registry.example/thanos/thanos:latest" rejected: :latest tag not allowed`},
		{"container image tagged latest", container(map[string]any{"name": "proxy", "image": "registry.example/team/proxy:latest"}),
			`containers[0] "proxy": image "registry.example/team/proxy:latest" rejected: :latest tag not allowed`},
		{"request over its limit", map[string]any{"resources": map[string]any{
			"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"},
		}}, "resources: cpu: request 2 must not exceed limit 1"},
		{"sidecar request over its limit", map[string]any{"thanos": map[string]any{"resources": map[string]any{
			"requests": map[string]any{"memory": "2Gi"}, "limits": map[string]any{"memory": "1Gi"},
		}}}, "thanos: resources: memory: request 2Gi must not exceed limit 1Gi"},
		// An authored 0 the type omits, on a container that patches one of the
		// operator's own as on any other.
		{"probe period of 0 on a patch", container(map[string]any{"name": "prometheus", "readinessProbe": map[string]any{"periodSeconds": 0}}),
			"containers[0].readinessProbe.periodSeconds: 0 cannot be carried by the "},
		// An authored "" on a string of the operator's own types that the CRD
		// defaults, at the top and on the Thanos sidecar.
		{"empty scrapeInterval", map[string]any{"scrapeInterval": ""},
			`scrapeInterval: "" cannot be carried by the Prometheus operator API types (the field is omitted when zero, so the API server would apply its default "30s")`},
		{"empty thanos blockSize", map[string]any{"thanos": map[string]any{"blockSize": ""}},
			`thanos.blockSize: "" cannot be carried by the Prometheus operator API types (the field is omitted when zero, so the API server would apply its default "2h")`},
		// The API requires the names of a pod's init containers and containers
		// to be unique together.
		{"a container named as the generated init container", container(map[string]any{"name": "init-config-reloader", "image": "registry.example/team/proxy:1.2.3"}),
			`containers[0] "init-config-reloader": the name is also that of the init container the Prometheus operator generates, and the API refuses a pod whose init containers and containers share a name`},
		{"an init container named as a generated container", map[string]any{"initContainers": []any{map[string]any{"name": "config-reloader", "image": "registry.example/team/proxy:1.2.3"}}},
			`initContainers[0] "config-reloader": the name is that of a container the Prometheus operator generates, and the API refuses a pod whose init containers and containers share a name`},
		{"a name listed in both lists", map[string]any{
			"initContainers": []any{map[string]any{"name": "setup", "image": "registry.example/team/proxy:1.2.3"}},
			"containers":     []any{map[string]any{"name": "setup", "image": "registry.example/team/proxy:1.2.3"}},
		}, `containers[0] "setup": the name is also that of initContainers[0]`},
		// The API's pod rules across fields; the operator copies both fields
		// into the pod template.
		{"dnsPolicy None without a nameserver", map[string]any{"dnsPolicy": "None"},
			"dnsPolicy: None requires dnsConfig.nameservers with at least one entry; the API refuses the pods the Prometheus operator builds without one"},
		{"a pod-level HostProcess without the host network", map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}},
			"securityContext.windowsOptions.hostProcess: hostNetwork must be true when hostProcess is true"},
		// A patch's ports are merged by number (MergePatchContainers, a
		// strategic merge), so a port of a generated name at another number
		// is added beside the generated one.
		{"a patched port of the web port's name at another number", container(map[string]any{"name": "prometheus", "ports": []any{map[string]any{"name": "web", "containerPort": 8080}}}),
			`containers[0] "prometheus": ports[0] "web" at 8080/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 9090/TCP, and the API refuses a container with two ports of one name`},
		{"a patched port of the authored web port name", map[string]any{"portName": "http-web", "containers": []any{map[string]any{"name": "prometheus", "ports": []any{map[string]any{"name": "http-web", "containerPort": 8080}}}}},
			`containers[0] "prometheus": ports[0] "http-web" at 8080/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 9090/TCP`},
		{"a patched port of the reloader's name", container(map[string]any{"name": "config-reloader", "ports": []any{map[string]any{"name": "reloader-web", "containerPort": 9000}}}),
			`containers[0] "config-reloader": ports[0] "reloader-web" at 9000/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 8080/TCP`},
		// Under the ProcessSignal reloadStrategy the operator does not hand
		// listenLocal to config-reloader, which keeps its port.
		{"a patched port of the reloader's name under the signal strategy", map[string]any{"listenLocal": true, "reloadStrategy": "ProcessSignal", "containers": []any{map[string]any{"name": "config-reloader", "ports": []any{map[string]any{"name": "reloader-web", "containerPort": 9000}}}}},
			`containers[0] "config-reloader": ports[0] "reloader-web" at 9000/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 8080/TCP`},
		{"a patched port of the init reloader's name", map[string]any{"initContainers": []any{map[string]any{"name": "init-config-reloader", "ports": []any{map[string]any{"name": "reloader-init", "containerPort": 9000}}}}},
			`initContainers[0] "init-config-reloader": ports[0] "reloader-init" at 9000/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 8081/TCP`},
		// The sidecar's ports name no protocol; the API defaults it to TCP.
		{"a patched port of the sidecar's grpc name", map[string]any{"thanos": map[string]any{}, "containers": []any{map[string]any{"name": "thanos-sidecar", "ports": []any{map[string]any{"name": "grpc", "containerPort": 9000}}}}},
			`containers[0] "thanos-sidecar": ports[0] "grpc" at 9000/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 10901/TCP`},
		{"a patch that renames the sidecar's http port to grpc", map[string]any{"thanos": map[string]any{}, "containers": []any{map[string]any{"name": "thanos-sidecar", "ports": []any{map[string]any{"name": "grpc", "containerPort": 10902}}}}},
			`containers[0] "thanos-sidecar": ports[0] "grpc" at 10902/TCP: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, the Prometheus operator's port at 10901/TCP`},
		// Under listenLocal the operator gives prometheus no port, and the
		// merge takes the patch's ports whole.
		{"a patch of a portless container with two ports of one name", map[string]any{"listenLocal": true, "containers": []any{map[string]any{"name": "prometheus", "ports": []any{
			map[string]any{"name": "metrics", "containerPort": 9000, "protocol": "TCP"},
			map[string]any{"name": "metrics", "containerPort": 9000, "protocol": "UDP"},
		}}}}, `containers[0] "prometheus": ports[1] "metrics": the name is that of ports[0] already, and the API refuses a container with two ports of one name`},
		{"a sidecar with two ports of one name", container(map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "ports": []any{
			map[string]any{"name": "http", "containerPort": 8080},
			map[string]any{"name": "http", "containerPort": 8081},
		}}), `containers[0] "proxy": ports[1] "http": the name is that of ports[0] already, and the API refuses a container with two ports of one name`},
		{"two volumes of one name", map[string]any{"volumes": []any{
			map[string]any{"name": "scratch", "emptyDir": map[string]any{}},
			map[string]any{"name": "scratch", "emptyDir": map[string]any{}},
		}}, `volumes[1] "scratch": the name is listed already at volumes[0], and the API refuses a pod with two volumes of one name`},
		// The operator gets the governing Service by this name and fails the
		// reconcile where there is none (pkg/prometheus/server/operator.go:1010
		// at v0.94.1); the name is held to the package's Service-name rule.
		{"a serviceName that is not a DNS-1035 label", map[string]any{"serviceName": "Bad_Name"},
			`serviceName: "Bad_Name" is not a valid Service name, which must be a DNS-1035 label`},
		{"a serviceName with a leading digit", map[string]any{"serviceName": "1prom"},
			`serviceName: "1prom" is not a valid Service name, which must be a DNS-1035 label`},
		// Prometheus exits at startup on an externalUrl that begins or ends
		// with a quote, or that net/url cannot parse (computeExternalURL,
		// cmd/prometheus/main.go:1762-1792 at v3.14.0).
		{"an externalUrl that begins with a quote", map[string]any{"externalUrl": `"https://prometheus.example.com`},
			"externalUrl: begins or ends with a quote: the Prometheus operator passes it to Prometheus, which then exits at startup; name the URL without quotes, or leave it unset"},
		{"an externalUrl that ends with a quote", map[string]any{"externalUrl": "https://prometheus.example.com'"},
			"externalUrl: begins or ends with a quote"},
		{"an externalUrl that does not parse", map[string]any{"externalUrl": "http://[::1"},
			"externalUrl: not a URL Go's net/url can parse: the Prometheus operator passes it to Prometheus, which then exits at startup; name a valid URL, or leave it unset"},
		// The operator rewrites a negative count to 1 (ReplicasNumberPtr,
		// pkg/prometheus/common.go:131-143).
		{"negative replicas", map[string]any{"replicas": -1}, "replicas: -1 is below 0: the Prometheus operator runs 1 replica for it; write 1"},
		// A web port name the API refuses on the container or the governing
		// Service (common.go:459-469, server/operator.go:1006-1030).
		{"a port name over 15 characters", map[string]any{"portName": "prometheus-webui"},
			`portName: "prometheus-webui" is not a valid port name`},
		{"the sidecar's port name on the governing Service", map[string]any{"portName": "grpc", "thanos": map[string]any{}},
			`portName: "grpc" is the name of the port the Prometheus operator adds for the Thanos sidecar to the governing Service`},
		// A claim the API refuses (server/statefulset.go:105-138).
		{"an empty storage", map[string]any{"storage": map[string]any{}},
			"storage.volumeClaimTemplate.spec.resources.requests.storage: required"},
		{"an ephemeral claim without access modes", map[string]any{"storage": map[string]any{"ephemeral": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": amClaim},
		}}}, "storage.ephemeral.volumeClaimTemplate.spec.accessModes: required"},
		{"a claim template requesting 0", map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "0"}}}},
		}}, "storage.volumeClaimTemplate.spec.resources.requests.storage: 0 is not above 0"},
		// The operator mounts the data volume under the claim template's name
		// whatever arm is in use (VolumeClaimName, common.go:352-360).
		{"a named claim template beside emptyDir", map[string]any{"storage": map[string]any{
			"emptyDir":            map[string]any{},
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": amClaim},
		}}, `storage.volumeClaimTemplate.metadata.name: "data" beside storage.emptyDir or storage.ephemeral`},
		{"a claim template name that is not a DNS-1123 label", map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data.disk"}, "spec": amClaim},
		}}, `storage.volumeClaimTemplate.metadata.name: "data.disk" is not a DNS-1123 label`},
		{"a claim template named as the configuration's volume", map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "config"}, "spec": amClaim},
		}}, `storage.volumeClaimTemplate.metadata.name: "config" is a volume the Prometheus operator adds to every Prometheus's pods`},
		{"a claim template named as a rule ConfigMap's volume", map[string]any{"storage": map[string]any{
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "prometheus-fast-rulefiles-0"}, "spec": amClaim},
		}}, `storage.volumeClaimTemplate.metadata.name: "prometheus-fast-rulefiles-0" is the volume of a rule ConfigMap the Prometheus operator mounts`},
		// A volume named as one the operator adds to the pods (common.go:240-350,
		// server/statefulset.go:142, :192-204, :510-544, :730-745).
		{"a volume named as the configuration's", amVolume("config"),
			`volumes[0] "config": the name is a volume the Prometheus operator adds to every Prometheus's pods`},
		{"a volume named as the web configuration's", amVolume("web-config"),
			`volumes[0] "web-config": the name is a volume the Prometheus operator adds`},
		{"a volume named as the data volume", amVolume("prometheus-fast-db"),
			`volumes[0] "prometheus-fast-db": the name is the data volume's, which the Prometheus operator adds to the pods`},
		{"a volume named as a rule ConfigMap's", amVolume("prometheus-fast-rulefiles-7"),
			`volumes[0] "prometheus-fast-rulefiles-7": the name is the volume of a rule ConfigMap the Prometheus operator mounts`},
		{"a volume named as the log file's", withAmVolume(map[string]any{"queryLogFile": "query.log"}, "log-file"),
			`volumes[0] "log-file": the name is the volume the Prometheus operator adds for a log file named without a directory`},
		{"a volume named as the sidecar's configuration's", withAmVolume(map[string]any{"thanos": map[string]any{}}, "thanos-prometheus-http-client-file"),
			`volumes[0] "thanos-prometheus-http-client-file": the name is the volume the Prometheus operator adds for the Thanos sidecar's configuration`},
		{"a volume named as a listed Secret's", withAmVolume(map[string]any{"secrets": []any{"Remote.TLS"}}, "secret-remote-tls"),
			`volumes[0] "secret-remote-tls": the name is the volume the Prometheus operator adds for secrets[0]`},
		{"two Secrets the operator gives one volume name", map[string]any{"secrets": []any{"remote.tls", "remote-tls"}},
			`secrets[1] "remote-tls": the Prometheus operator names its volume "secret-remote-tls", which is the volume the Prometheus operator adds for secrets[0]`},
		// The API refuses two mounts at one path in the prometheus container
		// (common.go:264-340, server/statefulset.go:192-204, :527-533).
		{"a mount at the data volume's path", map[string]any{"volumeMounts": []any{map[string]any{"name": "prometheus-fast-db", "mountPath": "/prometheus"}}},
			`volumeMounts[0] "/prometheus": the mount path is a path the Prometheus operator mounts a volume at in every prometheus container`},
		{"a mount at the web configuration file", map[string]any{"volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/etc/prometheus/web_config/web-config.yaml"}}},
			`volumeMounts[0] "/etc/prometheus/web_config/web-config.yaml": the mount path is a path the Prometheus operator mounts a volume at`},
		{"a mount at a listed ConfigMap's path", map[string]any{"configMaps": []any{"targets"}, "volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/etc/prometheus/configmaps/targets"}}},
			`volumeMounts[0] "/etc/prometheus/configmaps/targets": the mount path is the path the Prometheus operator mounts configMaps[0] at`},
		{"a mount at a rule ConfigMap's path", map[string]any{"volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/etc/prometheus/rules/prometheus-fast-rulefiles-1"}}},
			`volumeMounts[0] "/etc/prometheus/rules/prometheus-fast-rulefiles-1": the mount path is the path the Prometheus operator mounts a rule ConfigMap at`},
		{"a mount at the log file's directory", map[string]any{"scrapeFailureLogFile": "scrape.log", "volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/var/log/prometheus"}}},
			`volumeMounts[0] "/var/log/prometheus": the mount path is the path the Prometheus operator mounts the volume of a log file`},
		// The CRD's quantity pattern admits a sign; the API refuses the
		// container the operator builds with it.
		{"a negative cpu request", map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "-1"}}},
			"resources: cpu: request -1 is below 0"},
		{"a negative sidecar memory limit", map[string]any{"thanos": map[string]any{"resources": map[string]any{"limits": map[string]any{"memory": "-1Gi"}}}},
			"thanos: resources: memory: limit -1Gi is below 0"},
	}
}

// TestPrometheus_ExternalURL: Prometheus checks no scheme of externalUrl, so
// one of any scheme, or none, builds; a refused one is not quoted, as it is
// authored text that can carry a credential, and url.Parse's error, which
// repeats it, is not passed on.
func TestPrometheus_ExternalURL(t *testing.T) {
	for _, value := range []string{"ftp://prometheus.example.com", "prometheus.example.com/prom", "HTTPS://prometheus.example.com"} {
		prometheusOf(t, map[string]any{"externalUrl": value})
	}
	h := &components.PrometheusHandler{}
	for name, value := range map[string]string{
		"a quoted URL":           `'http://bot:s3cret@prometheus.example.com'`,
		"a URL that won't parse": "http://bot:s3cret@[::1",
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(h, "prometheus", "main", map[string]any{"externalUrl": value})
			if err == nil {
				t.Fatal("err = nil, want the externalUrl refused")
			}
			for _, part := range []string{"s3cret", "bot", "[::1"} {
				if strings.Contains(err.Error(), part) {
					t.Errorf("err = %v, names %q of the value", err, part)
				}
			}
		})
	}
}

// TestPrometheus_PatchedPorts: the patch ports the operator's merge leaves
// named apart build. The operator gives no port to prometheus under
// listenLocal, nor to config-reloader under listenLocal with the HTTP
// reloadStrategy, so a patch's port of the generated name builds there; and
// thanos.listenLocal moves the sidecar's bind address only, not its ports.
func TestPrometheus_PatchedPorts(t *testing.T) {
	patch := func(name string, ports ...map[string]any) []any {
		list := make([]any, len(ports))
		for i, p := range ports {
			list[i] = p
		}
		return []any{map[string]any{"name": name, "ports": list}}
	}
	for name, props := range map[string]map[string]any{
		"the web port's name and number":        {"containers": patch("prometheus", map[string]any{"name": "web", "containerPort": 9090})},
		"the web port's name listening locally": {"listenLocal": true, "containers": patch("prometheus", map[string]any{"name": "web", "containerPort": 8080})},
		"the reloader's name listening locally": {"listenLocal": true, "containers": patch("config-reloader", map[string]any{"name": "reloader-web", "containerPort": 9000})},
		// The merge by number renames the generated port at 10902 to
		// metrics, so http at 9000 is the only port of that name.
		"a patch that renames a sidecar port and reuses its name": {"thanos": map[string]any{}, "containers": patch("thanos-sidecar",
			map[string]any{"name": "metrics", "containerPort": 10902},
			map[string]any{"name": "http", "containerPort": 9000},
		)},
		"a port without a name at the sidecar's grpc number": {"thanos": map[string]any{"listenLocal": true}, "containers": patch("thanos-sidecar", map[string]any{"containerPort": 10901})},
		"a sidecar port of a generated port's name":          {"containers": []any{map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "ports": []any{map[string]any{"name": "web", "containerPort": 8080}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			prometheusOf(t, props)
		})
	}
}

// pmImage is a Prometheus image of the registry ptStrictPolicy allows.
const pmImage = "registry.example/prometheus/prometheus:v3.5.0"

// thanosImage is a Thanos image of the registry ptStrictPolicy allows.
const thanosImage = "registry.example/thanos/thanos:v0.39.2"

// pmHeld returns props with image pmImage where props names none, and both
// reloaders patched (amReloaders), so that a document built under
// ptStrictPolicy is not refused for an image left to the operator. props is
// not changed.
func pmHeld(props map[string]any) map[string]any {
	out := amReloaders(props)
	if _, named := out["image"]; !named {
		out["image"] = pmImage
	}
	return out
}

// prometheusOf builds the Prometheus of props under the given policies, in
// turn (generateCoreKindUnder).
func prometheusOf(t *testing.T, props map[string]any, policies ...oam.Policy) *monitoringv1.Prometheus {
	t.Helper()
	obj := generateCoreKindUnder(t, &components.PrometheusHandler{}, "prometheus", "main", props, policies...)
	p, ok := obj.(*monitoringv1.Prometheus)
	if !ok {
		t.Fatalf("the kind built a %T, want a Prometheus", obj)
	}
	return p
}

// prometheusThrough is the one Prometheus the transform builds for component
// web from props, under no policy.
func prometheusThrough(t *testing.T, props map[string]any) (*monitoringv1.Prometheus, error) {
	t.Helper()
	objs, err := policyFreeTransform("prometheus", &components.PrometheusHandler{}, nil, oam.Component{Name: "web", Properties: props})
	if err != nil {
		return nil, err
	}
	var found []client.Object
	for _, obj := range objs {
		if _, ok := obj.(*monitoringv1.Prometheus); ok {
			found = append(found, obj)
		}
	}
	if len(objs) != 1 || len(found) != 1 {
		t.Fatalf("built %d objects, %d of them a Prometheus; want the one Prometheus", len(objs), len(found))
	}
	return found[0].(*monitoringv1.Prometheus), nil
}

// TestPrometheus_DeprecatedImageFields: baseImage, tag and sha, of the spec
// and of the Thanos sidecar, are refused wherever the object would carry them,
// whatever else is authored, without a policy, each naming the field that replaces it; the
// spec's three are no property of the kind's schema. An empty one of the
// spec is the object an absent one is, and builds. The sidecar's are pointers
// the type writes whenever set, so an empty one is refused too, under any
// spelling the decode folds onto the field; a null one sets none and builds.
// thanos.version is not one of them.
func TestPrometheus_DeprecatedImageFields(t *testing.T) {
	h := &components.PrometheusHandler{}
	schema := h.PropertySchema()
	for _, field := range []string{"baseImage", "tag", "sha"} {
		t.Run(field, func(t *testing.T) {
			if _, published := schema[field]; published {
				t.Errorf("the schema publishes %q, which the kind refuses", field)
			}
			for name, tc := range map[string]struct {
				props map[string]any
				path  string
				use   string
			}{
				"alone":                             {map[string]any{field: "v3.5.0"}, field, "use image"},
				"beside image":                      {map[string]any{field: "v3.5.0", "image": "registry.example/prometheus/prometheus:v3.5.0"}, field, "use image"},
				"the sidecar's":                     {map[string]any{"thanos": map[string]any{field: "v0.39.2"}}, "thanos." + field, "use thanos.image"},
				"beside thanos.image":               {map[string]any{"thanos": map[string]any{field: "v0.39.2", "image": "registry.example/thanos/thanos:v0.39.2"}}, "thanos." + field, "use thanos.image"},
				"another sidecar text":              {map[string]any{"thanos": map[string]any{field: "registry.example/thanos/thanos"}}, "thanos." + field, "use thanos.image"},
				"the sidecar's, empty":              {map[string]any{"thanos": map[string]any{field: ""}}, "thanos." + field, "use thanos.image"},
				"the sidecar's, empty, in capitals": {map[string]any{"thanos": map[string]any{strings.ToUpper(field): ""}}, "thanos." + field, "use thanos.image"},
			} {
				want := tc.path + ": not authorable: the Prometheus operator deprecates the field"
				if err := coreKindErr(h, "prometheus", "main", tc.props); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), tc.use) {
					t.Errorf("%s: err = %v, want one mentioning %q and %q", name, err, want, tc.use)
				}
			}
			p := prometheusOf(t, map[string]any{field: ""})
			if p.Spec.BaseImage != "" || p.Spec.Tag != "" || p.Spec.SHA != "" {
				t.Errorf("an empty %s built baseImage %q, tag %q, sha %q; want none", field, p.Spec.BaseImage, p.Spec.Tag, p.Spec.SHA)
			}
			p = prometheusOf(t, map[string]any{"thanos": map[string]any{field: nil}})
			if th := p.Spec.Thanos; th == nil || th.BaseImage != nil || th.Tag != nil || th.SHA != nil {
				t.Errorf("a null thanos.%s built thanos %+v; want a sidecar with none of the three set", field, th)
			}
		})
	}
	p := prometheusOf(t, pmHeld(map[string]any{"thanos": map[string]any{"version": "v0.39.2", "image": "registry.example/thanos/thanos:v0.39.2"}}), ptStrictPolicy())
	if p.Spec.Thanos == nil || p.Spec.Thanos.Version == nil || *p.Spec.Thanos.Version != "v0.39.2" {
		t.Errorf("thanos = %+v, want the authored version", p.Spec.Thanos)
	}
}

// TestPrometheus_Unauthored: a component that authors nothing builds a
// Prometheus whose spec holds no image, no replica or shard count and no
// storage, so the operator's own defaults apply and no policy default is
// written; and the fields the type always encodes, empty.
func TestPrometheus_Unauthored(t *testing.T) {
	two := int32(2)
	defaulting := &stubPolicy{
		defaultReplicas: &two, defaultCPURequest: "100m", defaultMemoryRequest: "64Mi",
		defaultCPULimit: "1", defaultMemoryLimit: "128Mi", defaultStorageSize: "1Gi",
	}
	p := prometheusOf(t, map[string]any{}, defaulting, nil)
	if p.Spec.Image != nil || p.Spec.Replicas != nil || p.Spec.Shards != nil || p.Spec.Storage != nil {
		t.Errorf("image = %v, replicas = %v, shards = %v, storage = %v; want none of them written", p.Spec.Image, p.Spec.Replicas, p.Spec.Shards, p.Spec.Storage)
	}
	if len(p.Spec.Resources.Requests) != 0 || len(p.Spec.Resources.Limits) != 0 {
		t.Errorf("resources = %+v, want no default of the policy filled", p.Spec.Resources)
	}
	spec, _ := policyFreeJSON(t, p)["spec"].(map[string]any)
	want := map[string]any{
		"arbitraryFSAccessThroughSMs": map[string]any{},
		"resources":                   map[string]any{},
		"rules":                       map[string]any{"alert": map[string]any{}},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Errorf("spec = %v, want %v: the fields the type encodes whether or not they were authored, empty", spec, want)
	}
}

// TestPrometheus_ReplicasTimesShards: the pods the policy's replica maximum
// holds are those of all shards, replicas times shards, as the operator counts
// them (ReplicasNumberPtr and shardsNumber, pkg/prometheus/common.go:118-143
// at prometheus-operator v0.94.1): an unset or negative replica count as 1,
// an unset shard count or one of 1 or less as 1. Where neither is authored,
// one pod is held.
func TestPrometheus_ReplicasTimesShards(t *testing.T) {
	h := &components.PrometheusHandler{}
	for name, tc := range map[string]struct {
		props   map[string]any
		refused bool
	}{
		"neither":                        {map[string]any{}, false},
		"replicas within":                {map[string]any{"replicas": 3}, false},
		"replicas over":                  {map[string]any{"replicas": 4}, true},
		"shards within":                  {map[string]any{"shards": 3}, false},
		"shards over":                    {map[string]any{"shards": 4}, true},
		"both within":                    {map[string]any{"replicas": 1, "shards": 3}, false},
		"both over, each within":         {map[string]any{"replicas": 2, "shards": 2}, true},
		"a replica count of 0":           {map[string]any{"replicas": 0, "shards": 4}, false},
		"a null shard count":             {map[string]any{"replicas": 3, "shards": nil}, false},
		"a null count beside over":       {map[string]any{"replicas": nil, "shards": 4}, true},
		"a shard count of 0 beside over": {map[string]any{"replicas": 4, "shards": 0}, true},
		"a negative shard count":         {map[string]any{"replicas": 3, "shards": -2}, false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("prometheus", h, pmHeld(tc.props), ptStrictPolicy())
			if !tc.refused {
				if err != nil {
					t.Errorf("err = %v, want it built", err)
				}
				return
			}
			rcWantClass(t, err, oam.RefusalReplicaMaximum)
			if err != nil && !strings.Contains(err.Error(), "replicas times shards 4 exceeds enforced maximum 3") {
				t.Errorf("err = %v, want one naming replicas times shards", err)
			}
		})
	}
}

// TestPrometheus_Name: the operator names the data volume, the rule
// ConfigMaps' volumes and the pods' hostnames after the Prometheus, each a
// DNS-1123 label; the third rule ConfigMap's volume, mounted whatever the
// rules, binds a name to 40 characters.
func TestPrometheus_Name(t *testing.T) {
	h := &components.PrometheusHandler{}
	for name, tc := range map[string]struct {
		component string
		props     map[string]any
		want      string
	}{
		"a component name over 40 characters": {strings.Repeat("a", 41), nil,
			`prometheus "` + strings.Repeat("a", 41) + `": the component name is the Prometheus's name, and the Prometheus operator names the volume of a rule ConfigMap it mounts "prometheus-` + strings.Repeat("a", 41) + `-rulefiles-2", which must be a DNS-1123 label`},
		"a dotted objectName": {"web", map[string]any{oam.ObjectNameProperty: "metrics.example"},
			`"metrics.example" is not a valid name for this Prometheus`},
		"the hostname of the last shard's last pod": {strings.Repeat("a", 40), map[string]any{"shards": 1000, "replicas": 11},
			`the pod of the last replica of the last shard takes the hostname "prometheus-` + strings.Repeat("a", 40) + `-shard-999-10", which must be a DNS-1123 label`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := policyFreeTransform("prometheus", h, nil, oam.Component{Name: tc.component, Properties: tc.props})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	for name, tc := range map[string]struct {
		component string
		props     map[string]any
	}{
		"40 characters":                    {strings.Repeat("a", 40), nil},
		"40 characters, 1000 shards of 10": {strings.Repeat("a", 40), map[string]any{"shards": 1000, "replicas": 10}},
		"40 characters, no replica's pod":  {strings.Repeat("a", 40), map[string]any{"shards": 1000, "replicas": 0}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := policyFreeTransform("prometheus", h, nil, oam.Component{Name: tc.component, Properties: tc.props}); err != nil {
				t.Fatalf("err = %v, want it built", err)
			}
		})
	}
}

// TestPrometheus_OperatorRunsIt: the specs beside each refusal of what the
// operator or the API would refuse or rewrite, that both run as written, build.
func TestPrometheus_OperatorRunsIt(t *testing.T) {
	h := &components.PrometheusHandler{}
	extra := func(name string) []any { return []any{map[string]any{"name": name, "emptyDir": map[string]any{}}} }
	for name, props := range map[string]map[string]any{
		"zero replicas":                                     {"replicas": 0},
		"a port name of 15 characters":                      {"portName": "prometheuswebui"},
		"the sidecar's port name without a sidecar":         {"portName": "grpc"},
		"the sidecar's port name with a Service of its own": {"portName": "grpc", "thanos": map[string]any{}, "serviceName": "metrics"},
		"an invalid port name written nowhere":              {"portName": "prometheus-webui", "listenLocal": true, "serviceName": "metrics"},
		"emptyDir alone":                                    {"storage": map[string]any{"emptyDir": map[string]any{}}},
		"a claim template of its own name":                  {"storage": map[string]any{"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "data"}, "spec": amClaim}}},
		"the operator's name for the data volume beside emptyDir": {"storage": map[string]any{
			"emptyDir":            map[string]any{},
			"volumeClaimTemplate": map[string]any{"metadata": map[string]any{"name": "prometheus-fast-db"}},
		}},
		"the log file's volume name for a log file in a directory": {"queryLogFile": "/var/log/query.log", "volumes": extra("log-file")},
		"the sidecar's volume name without a sidecar":              {"volumes": extra("thanos-prometheus-http-client-file")},
		"another Prometheus's rule ConfigMap volume name":          {"volumes": extra("prometheus-other-rulefiles-0")},
		"two Secrets whose volumes differ":                         {"secrets": []any{"remote", "remote-tls"}},
		"a mount beside the operator's": {"secrets": []any{"remote"}, "volumes": extra("extra"),
			"volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/etc/prometheus/secrets/extra"}}},
		"the log file's directory without a log file volume": {"volumes": extra("extra"),
			"volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/var/log/prometheus"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := coreKindErr(h, "prometheus", "fast", props); err != nil {
				t.Fatalf("err = %v, want it built", err)
			}
		})
	}
}

// TestPrometheus_PolicyRefusals: under an environment policy, what the spec
// says of the pods is refused as a workload kind's own fields are, the Thanos
// sidecar's image and resources too, with the class of the refusal and the
// path of the property; and so is each credential the spec holds in the clear.
// Without a policy the same component builds.
func TestPrometheus_PolicyRefusals(t *testing.T) {
	const image = "registry.example/prometheus/prometheus:v3.5.0"
	container := func(list string, c map[string]any) map[string]any { return map[string]any{list: []any{c}} }
	claim := func(size string) map[string]any {
		return map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": size}},
		}}}
	}
	endpoints := func(list string, entries ...map[string]any) map[string]any {
		all := make([]any, 0, len(entries))
		for _, e := range entries {
			all = append(all, e)
		}
		return map[string]any{list: all}
	}
	cases := []struct {
		name  string
		props map[string]any
		class oam.RefusalClass
		want  string
	}{
		{"image outside the allowed registries", map[string]any{"image": "other.example/prometheus/prometheus:v3.5.0"}, oam.RefusalRegistry, "image: "},
		{"sidecar image outside the allowed registries", map[string]any{"thanos": map[string]any{"image": "other.example/thanos/thanos:v0.39.2"}}, oam.RefusalRegistry, "thanos.image: "},
		{"replicas over the maximum", map[string]any{"replicas": 4}, oam.RefusalReplicaMaximum, "replicas times shards 4 exceeds enforced maximum 3"},
		{"claim over the storage maximum", map[string]any{"storage": claim("1Ti")}, oam.RefusalStorageMaximum, "storage.volumeClaimTemplate.spec.resources.requests.storage"},
		{"ephemeral claim over the storage maximum", map[string]any{"storage": map[string]any{"ephemeral": claim("1Ti")}}, oam.RefusalStorageMaximum, "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage"},
		{"cpu over the maximum", map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "4"}}}, oam.RefusalResourceMaximum, "resources: "},
		{"sidecar memory over the maximum", map[string]any{"thanos": map[string]any{"image": thanosImage, "resources": map[string]any{"limits": map[string]any{"memory": "2Gi"}}}}, oam.RefusalResourceMaximum, "thanos.resources: "},
		{"host network", map[string]any{"hostNetwork": true}, oam.RefusalHostNamespace, "hostNetwork"},
		{"hostPath volume", map[string]any{"volumes": []any{map[string]any{"name": "host", "hostPath": map[string]any{"path": "/etc"}}}}, oam.RefusalHostPath, "hostPath"},
		{"image volume outside the allowed registries", map[string]any{"volumes": []any{map[string]any{"name": "data", "image": map[string]any{"reference": "other.example/team/data:1.0.0"}}}}, oam.RefusalRegistry, "other.example"},
		{"container image outside the allowed registries", container("containers", map[string]any{"name": "proxy", "image": "other.example/team/proxy:1.2.3"}), oam.RefusalRegistry, "proxy"},
		{"init container image outside the allowed registries", container("initContainers", map[string]any{"name": "prepare", "image": "other.example/team/prepare:1.0.0"}), oam.RefusalRegistry, "prepare"},
		// A patch of a container the operator generates is held like any other.
		{"patch over the memory maximum", container("containers", map[string]any{"name": "config-reloader", "resources": map[string]any{"limits": map[string]any{"memory": "2Gi"}}}), oam.RefusalResourceMaximum, "config-reloader"},
		// thanos-sidecar is generated where thanos is set, and only there.
		{"privileged sidecar patch", map[string]any{
			"thanos":     map[string]any{"image": thanosImage},
			"containers": []any{map[string]any{"name": "thanos-sidecar", "securityContext": map[string]any{"privileged": true}}},
		}, oam.RefusalPrivileged, "thanos-sidecar"},
		{"privileged init container", container("initContainers", map[string]any{"name": "init-config-reloader", "securityContext": map[string]any{"privileged": true}}), oam.RefusalPrivileged, "init-config-reloader"},
		{"forbidden capability", container("containers", map[string]any{"name": "proxy", "image": "registry.example/team/proxy:1.2.3", "securityContext": map[string]any{"capabilities": map[string]any{"add": []any{"NET_ADMIN"}}}}), oam.RefusalContainerCapability, "NET_ADMIN"},
		// The deprecated bearer tokens, the credentials in the clear, by the
		// index of their entry.
		{"a remote write bearer token", endpoints("remoteWrite",
			map[string]any{"url": "https://a.example.com/api/v1/write"},
			map[string]any{"url": "https://b.example.com/api/v1/write", "bearerToken": "s3cr3t"},
		), oam.RefusalExplicitSecret, "remoteWrite[1].bearerToken: holds a credential in the object, and the environment policy forbids explicit secrets"},
		{"a remote read bearer token", endpoints("remoteRead",
			map[string]any{"url": "https://a.example.com/api/v1/read"},
			map[string]any{"url": "https://b.example.com/api/v1/read", "bearerToken": "s3cr3t"},
		), oam.RefusalExplicitSecret, "remoteRead[1].bearerToken: holds a credential in the object"},
		{"an API server bearer token", map[string]any{"apiserverConfig": map[string]any{"host": "https://kubernetes.default.svc", "bearerToken": "s3cr3t"}},
			oam.RefusalExplicitSecret, "apiserverConfig.bearerToken: holds a credential in the object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"image": image}
			maps.Copy(props, tc.props)
			props = pmHeld(props)
			_, err := pvTransform("prometheus", &components.PrometheusHandler{}, props, esPolicy{stubPolicy: ptStrictPolicy()})
			rcWantClass(t, err, tc.class)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
			// Without a policy applied, the same component builds.
			prometheusOf(t, props)
		})
	}
}

// TestPrometheus_HostNetwork: an authored true is carried under a policy that
// allows the host network and refused under one that does not
// (TestPrometheus_PolicyRefusals); an authored false is the object an absent
// one is, since the type omits it.
func TestPrometheus_HostNetwork(t *testing.T) {
	p := prometheusOf(t, pmHeld(map[string]any{"hostNetwork": true}), hostNetworkOK{ptStrictPolicy()})
	if !p.Spec.HostNetwork {
		t.Error("hostNetwork = false, want the authored true under a policy that allows the host network")
	}
	off := prometheusOf(t, pmHeld(map[string]any{"hostNetwork": false}), ptStrictPolicy())
	if spec, _ := policyFreeJSON(t, off)["spec"].(map[string]any); spec["hostNetwork"] != nil {
		t.Errorf("hostNetwork = %v, want it omitted: the API reads an absent one as false", spec["hostNetwork"])
	}
}

// TestPrometheus_HostProcess: a pod-level securityContext that asks for a
// Windows HostProcess pod is refused under a policy that does not allow
// privileged containers, as on a workload kind. The API requires the host
// network of such a pod, so the props set it.
func TestPrometheus_HostProcess(t *testing.T) {
	props := pmHeld(map[string]any{
		"hostNetwork":     true,
		"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}},
	})
	h := &components.PrometheusHandler{}
	_, err := pvTransform("prometheus", h, props, hostNetworkOK{ptStrictPolicy()})
	rcWantClass(t, err, oam.RefusalPrivileged)
	allowing := ptStrictPolicy()
	allowing.allowPrivileged = true
	if _, err := pvTransform("prometheus", h, props, hostNetworkOK{allowing}); err != nil {
		t.Errorf("under a policy that allows privileged containers: %v, want it built", err)
	}
}

// TestPrometheus_CredentialsStatedNotHeld: a credential under a name that does
// not say so is not read, under a policy that forbids explicit secrets: a
// header value and the user information of a URL build, of a remote write and
// a remote read entry and of the tracing configuration. Every other credential
// of the spec is the key of a Secret or the path of a file in the container.
func TestPrometheus_CredentialsStatedNotHeld(t *testing.T) {
	p := prometheusOf(t, pmHeld(map[string]any{
		"remoteWrite": []any{map[string]any{
			"url":     "https://user:s3cr3t@metrics.example.com/api/v1/write",
			"headers": map[string]any{"X-Api-Key": "s3cr3t"},
		}},
		"remoteRead": []any{map[string]any{
			"url":     "https://user:s3cr3t@metrics.example.com/api/v1/read",
			"headers": map[string]any{"X-Api-Key": "s3cr3t"},
		}},
		"tracingConfig": map[string]any{"endpoint": "tempo.monitoring.svc:4317", "headers": map[string]any{"X-Api-Key": "s3cr3t"}},
	}), esPolicy{stubPolicy: ptStrictPolicy()})
	if got := p.Spec.RemoteWrite[0].Headers["X-Api-Key"]; got != "s3cr3t" {
		t.Errorf("the remote write header = %q, want it carried as authored", got)
	}
	if got := p.Spec.RemoteRead[0].Headers["X-Api-Key"]; got != "s3cr3t" {
		t.Errorf("the remote read header = %q, want it carried as authored", got)
	}
}

// TestPrometheus_ExternalLabels: the spec's external labels are published
// under their own name, and land in the spec as authored; the object's own
// labels are the engine's `labels`, beside the component label. The engine's
// checks of object labels do not reach the external labels.
func TestPrometheus_ExternalLabels(t *testing.T) {
	key := oam.ComponentLabelKeyForDomain("")
	p, err := prometheusThrough(t, map[string]any{
		"labels":         map[string]any{"team": "payments"},
		"externalLabels": map[string]any{"cluster": "eu-1", key: "other"},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got, want := p.GetLabels(), map[string]string{"team": "payments", key: "web"}; !maps.Equal(got, want) {
		t.Errorf("the object's labels = %v, want the authored labels and the component label: %v", got, want)
	}
	if got, want := p.Spec.ExternalLabels, map[string]string{"cluster": "eu-1", key: "other"}; !maps.Equal(got, want) {
		t.Errorf("the external labels = %v, want the authored externalLabels: %v", got, want)
	}
}

// TestPrometheus_ExcludedGroupFilled: an entry of excludedFromEnforcement that leaves its group out, or writes it null,
// carries the one group the API allows; an authored group is kept, and an
// authored empty one refused by the entry's index.
func TestPrometheus_ExcludedGroupFilled(t *testing.T) {
	p := prometheusOf(t, map[string]any{"excludedFromEnforcement": []any{
		map[string]any{"resource": "servicemonitors", "namespace": "monitoring"},
		map[string]any{"group": nil, "resource": "podmonitors", "namespace": "monitoring"},
		map[string]any{"group": "example.com", "resource": "probes", "namespace": "monitoring"},
	}})
	want := []string{"monitoring.coreos.com", "monitoring.coreos.com", "example.com"}
	got := make([]string, 0, len(p.Spec.ExcludedFromEnforcement))
	for _, ref := range p.Spec.ExcludedFromEnforcement {
		got = append(got, ref.Group)
	}
	if !slices.Equal(got, want) {
		t.Errorf("excludedFromEnforcement groups = %q, want %q", got, want)
	}
	err := coreKindErr(&components.PrometheusHandler{}, "prometheus", "main", map[string]any{"excludedFromEnforcement": []any{
		map[string]any{"resource": "servicemonitors", "namespace": "monitoring"},
		map[string]any{"group": "", "resource": "podmonitors", "namespace": "monitoring"},
	}})
	if wantErr := "excludedFromEnforcement[1].group: empty: the API admits only monitoring.coreos.com"; err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Errorf("an authored empty group: err = %v, want %q", err, wantErr)
	}
}

// TestPrometheus_UnsetImage: a spec that names no image, a null one or an
// empty one leaves the image to the operator, which no registry allowlist
// reaches: it is refused with the registry class under a policy with allowed
// registries, and builds under one without and under none, where an unset or
// null image writes none. A listed container named for one the operator
// generates, as here, is merged into it and may name no image.
func TestPrometheus_UnsetImage(t *testing.T) {
	patch := []any{map[string]any{"name": "prometheus", "resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}}}
	h := &components.PrometheusHandler{}
	for name, props := range map[string]map[string]any{
		"unset": amReloaders(map[string]any{"version": "v3.5.0"}),
		"null":  amReloaders(map[string]any{"version": "v3.5.0", "image": nil}),
		"empty": amReloaders(map[string]any{"version": "v3.5.0", "image": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			if name != "empty" {
				if p := prometheusOf(t, props); p.Spec.Image != nil {
					t.Errorf("image = %q under no policy, want none written", *p.Spec.Image)
				}
			}
			_, err := pvTransform("prometheus", h, props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), "image: unset") {
				t.Errorf("err = %v, want one naming the unset image", err)
			}
			open := ptStrictPolicy()
			open.allowedRegistries = nil
			if _, err := pvTransform("prometheus", h, props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
		})
	}
	p := prometheusOf(t, pmHeld(map[string]any{"containers": patch}), ptStrictPolicy())
	if p.Spec.Containers[0].Image != "" {
		t.Errorf("container image = %q, want none written", p.Spec.Containers[0].Image)
	}
}

// TestPrometheus_PatchedImage: the operator merges an entry of containers
// named prometheus into the container it generates (makeStatefulSetSpec,
// pkg/prometheus/server/statefulset.go:366 at prometheus-operator v0.94.1), so
// an image named there is the one that runs. With image unset it answers for
// it: held to the allowed registries as any listed container's image is, and
// not refused as unset.
func TestPrometheus_PatchedImage(t *testing.T) {
	h := &components.PrometheusHandler{}
	patched := func(image string) map[string]any {
		return amReloaders(map[string]any{"containers": []any{map[string]any{"name": "prometheus", "image": image}}})
	}
	p := prometheusOf(t, patched(pmImage), ptStrictPolicy())
	if p.Spec.Image != nil || p.Spec.Containers[0].Image != pmImage {
		t.Errorf("image = %v, patch image = %q; want none and the authored one", p.Spec.Image, p.Spec.Containers[0].Image)
	}
	_, err := pvTransform("prometheus", h, patched("other.example/prometheus/prometheus:v3.5.0"), ptStrictPolicy())
	rcWantClass(t, err, oam.RefusalRegistry)
	if err != nil && !strings.Contains(err.Error(), "other.example") {
		t.Errorf("err = %v, want one naming the patch's image", err)
	}
	// The patch's image replaces image, which then never runs and is not
	// held, to the registries or to the tag rule.
	for _, image := range []string{"other.example/prometheus/prometheus:v3.5.0", "registry.example/prometheus/prometheus:latest"} {
		props := patched(pmImage)
		props["image"] = image
		if _, err := pvTransform("prometheus", h, props, ptStrictPolicy()); err != nil {
			t.Errorf("image %q replaced by a patch: %v, want it built", image, err)
		}
		prometheusOf(t, props)
	}
}

// TestPrometheus_PatchResourcesMerged: the operator generates the prometheus
// container from resources and the thanos-sidecar container from
// thanos.resources, then merges a listed entry of either name into it
// (makeStatefulSetSpec, pkg/prometheus/server/statefulset.go:334-366 at
// prometheus-operator v0.94.1). Each block is held as the pods run it: the
// entry's requests and limits merged over the spec's, key by key, named by
// both, and the entry's block is not checked alone.
func TestPrometheus_PatchResourcesMerged(t *testing.T) {
	h := &components.PrometheusHandler{}
	small := ptStrictPolicy()
	small.maxMemory = "1Mi"
	thanos := map[string]any{"version": "v0.39.2", "image": thanosImage}
	patch := func(name string, resources map[string]any) []any {
		return []any{map[string]any{"name": name, "resources": resources}}
	}
	for name, tc := range map[string]struct {
		props  map[string]any
		policy *stubPolicy
		class  oam.RefusalClass
		want   string // "" when the component builds
	}{
		"memory request of a prometheus patch over 1Mi": {map[string]any{
			"containers": patch("prometheus", map[string]any{"requests": map[string]any{"memory": "2Mi"}}),
		}, small, oam.RefusalResourceMaximum, `resources with containers[0] "prometheus" merged over it: memory request "2Mi" exceeds enforced maximum "1Mi"`},
		"memory request of a thanos-sidecar patch over 1Mi": {map[string]any{
			"thanos":     thanos,
			"containers": patch("thanos-sidecar", map[string]any{"requests": map[string]any{"memory": "2Mi"}}),
		}, small, oam.RefusalResourceMaximum, `thanos.resources with containers[0] "thanos-sidecar" merged over it: memory request "2Mi" exceeds enforced maximum "1Mi"`},
		// A request of a patch replaces the spec's of the same key.
		"a prometheus patch's request replacing one over 1Mi": {map[string]any{
			"resources":  map[string]any{"requests": map[string]any{"memory": "2Mi"}},
			"containers": patch("prometheus", map[string]any{"requests": map[string]any{"memory": "1Mi"}}),
		}, small, "", ""},
		// A patch's extended request meets the limit of the block it is merged
		// with; alone, it would be refused for naming no limit.
		"an extended request of a prometheus patch, its limit in resources": {map[string]any{
			"resources":  map[string]any{"limits": map[string]any{"example.com/device": "1"}},
			"containers": patch("prometheus", map[string]any{"requests": map[string]any{"example.com/device": "1"}}),
		}, ptStrictPolicy(), "", ""},
		"an extended request of a thanos-sidecar patch, its limit in thanos.resources": {map[string]any{
			"thanos": map[string]any{"version": "v0.39.2", "image": thanosImage,
				"resources": map[string]any{"limits": map[string]any{"example.com/device": "1"}}},
			"containers": patch("thanos-sidecar", map[string]any{"requests": map[string]any{"example.com/device": "1"}}),
		}, ptStrictPolicy(), "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			props := pmHeld(tc.props)
			_, err := pvTransform("prometheus", h, props, tc.policy)
			if tc.want == "" {
				if err != nil {
					t.Errorf("err = %v, want it built", err)
				}
				prometheusOf(t, props)
				return
			}
			rcWantClass(t, err, tc.class)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestPrometheus_ReloaderImages: the operator generates config-reloader and
// init-config-reloader on every Prometheus (pkg/prometheus/server/
// statefulset.go:307-318 and :356-363 at prometheus-operator v0.94.1) from
// the image of its own configuration, which no allowlist reaches. Under a
// policy with allowed registries each is refused with the registry class
// unless a listed entry of its name patches it with an image, which is then
// held to the registries; under a policy without allowed registries, and
// under none, neither needs a patch.
func TestPrometheus_ReloaderImages(t *testing.T) {
	h := &components.PrometheusHandler{}
	open := ptStrictPolicy()
	open.allowedRegistries = nil
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"config-reloader unpatched": {
			map[string]any{"image": pmImage, "initContainers": []any{map[string]any{"name": "init-config-reloader", "image": amReloader}}},
			"the image of the config-reloader container (containers): unset, so the Prometheus operator chooses the image the pods run, which the allowed registries [registry.example] cannot hold; name an image from one of them",
		},
		"init-config-reloader unpatched": {
			map[string]any{"image": pmImage, "containers": []any{map[string]any{"name": "config-reloader", "image": amReloader}}},
			"the image of the init-config-reloader container (initContainers): unset",
		},
		"config-reloader patched without an image": {
			map[string]any{"image": pmImage,
				"containers":     []any{map[string]any{"name": "config-reloader", "resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}}},
				"initContainers": []any{map[string]any{"name": "init-config-reloader", "image": amReloader}}},
			"the image of the config-reloader container (containers): unset",
		},
		"config-reloader patched outside the allowed registries": {
			amReloaders(map[string]any{"image": pmImage, "containers": []any{map[string]any{"name": "config-reloader", "image": "other.example/prometheus-config-reloader:v0.94.1"}}}),
			`containers[0] "config-reloader"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("prometheus", h, tc.props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
			if _, err := pvTransform("prometheus", h, tc.props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
			prometheusOf(t, tc.props)
		})
	}
	if _, err := pvTransform("prometheus", h, pmHeld(map[string]any{}), ptStrictPolicy()); err != nil {
		t.Errorf("both reloaders patched with an allowed image: %v, want it built", err)
	}
}

// TestPrometheus_SidecarImage: the operator generates thanos-sidecar where
// thanos is set, and only there (createThanosContainer,
// pkg/prometheus/server/statefulset.go:544-547 at prometheus-operator
// v0.94.1). A sidecar that names no image, a null one or an empty one leaves
// the image to the operator: refused with the registry class under a policy
// with allowed registries unless an entry of containers named thanos-sidecar
// patches it with an image, which is then held to the registries, and built
// under one without. Without thanos, no sidecar is generated, so an entry of
// that name is a container of its own and must name an image.
func TestPrometheus_SidecarImage(t *testing.T) {
	h := &components.PrometheusHandler{}
	open := ptStrictPolicy()
	open.allowedRegistries = nil
	for name, thanos := range map[string]map[string]any{
		"unset": {"version": "v0.39.2"},
		"null":  {"version": "v0.39.2", "image": nil},
		"empty": {"version": "v0.39.2", "image": ""},
	} {
		t.Run(name, func(t *testing.T) {
			props := pmHeld(map[string]any{"thanos": thanos})
			_, err := pvTransform("prometheus", h, props, ptStrictPolicy())
			rcWantClass(t, err, oam.RefusalRegistry)
			if err != nil && !strings.Contains(err.Error(), "thanos.image: unset") {
				t.Errorf("err = %v, want one naming the unset sidecar image", err)
			}
			if _, err := pvTransform("prometheus", h, props, open); err != nil {
				t.Errorf("under a policy without allowed registries: %v, want it built", err)
			}
			prometheusOf(t, props)
		})
	}
	patched := func(image string) map[string]any {
		return pmHeld(map[string]any{
			"thanos":     map[string]any{"version": "v0.39.2"},
			"containers": []any{map[string]any{"name": "thanos-sidecar", "image": image}},
		})
	}
	p := prometheusOf(t, patched(thanosImage), ptStrictPolicy())
	if p.Spec.Thanos.Image != nil || p.Spec.Containers[0].Image != thanosImage {
		t.Errorf("thanos.image = %v, patch image = %q; want none and the authored one", p.Spec.Thanos.Image, p.Spec.Containers[0].Image)
	}
	_, err := pvTransform("prometheus", h, patched("other.example/thanos/thanos:v0.39.2"), ptStrictPolicy())
	rcWantClass(t, err, oam.RefusalRegistry)
	if err != nil && !strings.Contains(err.Error(), "other.example") {
		t.Errorf("err = %v, want one naming the patch's image", err)
	}
	// The patch's image replaces thanos.image, which then never runs and is
	// not held, to the registries or to the tag rule.
	for _, image := range []string{"other.example/thanos/thanos:v0.39.2", "registry.example/thanos/thanos:latest"} {
		props := patched(thanosImage)
		props["thanos"] = map[string]any{"version": "v0.39.2", "image": image}
		if _, err := pvTransform("prometheus", h, props, ptStrictPolicy()); err != nil {
			t.Errorf("thanos.image %q replaced by a patch: %v, want it built", image, err)
		}
		prometheusOf(t, props)
	}
	err = coreKindErr(h, "prometheus", "main", map[string]any{"containers": []any{map[string]any{"name": "thanos-sidecar", "resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}}}})
	if want := `containers[0] "thanos-sidecar": names no image, and the Prometheus operator generates no container of that name to merge it into`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a thanos-sidecar entry without thanos: err = %v, want %q", err, want)
	}
}

// TestPrometheus_OperatorDefaultsHeld: where the spec leaves the replica
// count unset, the operator runs one pod a shard (ReplicasNumberPtr,
// pkg/prometheus/common.go:131-143 at prometheus-operator v0.94.1), which the
// policy's maximum holds; nothing is written into the object. The operator
// fills no memory request of its own (makeStatefulSet,
// pkg/prometheus/server/statefulset.go:56-153), so an unset one holds none.
func TestPrometheus_OperatorDefaultsHeld(t *testing.T) {
	h := &components.PrometheusHandler{}
	zero := ptStrictPolicy()
	zero.maxReplicas = int32ptr(0)
	small := ptStrictPolicy()
	small.maxMemory = "1Mi"
	for name, tc := range map[string]struct {
		props  map[string]any
		policy *stubPolicy
		want   string // "" when the component builds
	}{
		"replicas unset under a maximum of 0":      {map[string]any{}, zero, "replicas times shards 1 exceeds enforced maximum 0"},
		"replicas 0 under a maximum of 0":          {map[string]any{"replicas": 0}, zero, ""},
		"memory request unset under a 1Mi maximum": {map[string]any{}, small, ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("prometheus", h, pmHeld(tc.props), tc.policy)
			if tc.want == "" {
				if err != nil {
					t.Errorf("err = %v, want it built", err)
				}
				return
			}
			rcWantClass(t, err, oam.RefusalReplicaMaximum)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	p := prometheusOf(t, pmHeld(map[string]any{}), ptStrictPolicy())
	if p.Spec.Replicas != nil || p.Spec.Shards != nil || len(p.Spec.Resources.Requests) != 0 {
		t.Errorf("replicas = %v, shards = %v, requests = %v; want none written", p.Spec.Replicas, p.Spec.Shards, p.Spec.Resources.Requests)
	}
}

// TestPrometheus_PodMetadata: through the transform, the metadata the
// operator copies onto the pods is the author's and nothing else. The object
// itself takes the component label; podMetadata takes none, so the pods carry
// it only where the author writes it.
func TestPrometheus_PodMetadata(t *testing.T) {
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
			p, err := prometheusThrough(t, tc.props)
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if tc.want == nil && p.Spec.PodMetadata != nil || tc.want != nil && (p.Spec.PodMetadata == nil || !maps.Equal(p.Spec.PodMetadata.Labels, tc.want.Labels)) {
				t.Errorf("podMetadata = %+v, want it as authored: %+v", p.Spec.PodMetadata, tc.want)
			}
			if got := p.GetLabels(); !maps.Equal(got, map[string]string{key: "web"}) {
				t.Errorf("the object's labels = %v, want the component label alone", got)
			}
		})
	}
}
