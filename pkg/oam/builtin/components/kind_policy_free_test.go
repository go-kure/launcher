package components_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of go-kure/launcher#790 to which no dimension of the
// environment policy applies, built on one shared helper (policyFreeKind): the
// cluster-scoped classes, the CSIDriver, the ServiceCIDR, the
// PodDisruptionBudget, the four kinds of the Prometheus operator's API and the
// four of Cilium's BGP control plane. The three kinds of cert-manager's API are
// held here too: the policy reaches one part of each (held), and everything
// else of them is the helper's.

// policyFreeKind is one of them. typ is the type the properties decode into:
// the object itself for a kind with no spec type (wholeObject), its spec type
// otherwise. namespaced says the object lands in the build namespace; the
// others are cluster-scoped. held says the environment policy reaches the
// kind: its fixtures then stay inside ptStrictPolicy, and what the policy
// refuses of it has its own tests. minimal is the least a component may
// author, full a value of every top-level field.
type policyFreeKind struct {
	component   string
	handler     oam.ComponentHandler
	gvk         schema.GroupVersionKind
	typ         reflect.Type
	wholeObject bool
	namespaced  bool
	held        bool
	minimal     map[string]any
	full        map[string]any
}

// generate builds the kind's object from props, named name. A kind the policy
// does not reach is built under a policy that allows next to nothing and under
// none (generateCoreKind); a held one under ptStrictPolicy with explicit
// secrets forbidden, and under none.
func (k policyFreeKind) generate(t *testing.T, name string, props map[string]any) client.Object {
	t.Helper()
	if k.held {
		return generateCoreKindUnder(t, k.handler, k.component, name, props, esPolicy{stubPolicy: ptStrictPolicy()}, nil)
	}
	return generateCoreKind(t, k.handler, k.component, name, props)
}

// namespace is the namespace the kind's object carries when it is built for
// the given one: that namespace, or none on a cluster-scoped object.
func (k policyFreeKind) namespace(build string) string {
	if k.namespaced {
		return build
	}
	return ""
}

// scope is the scope the kind's handler declares for its object.
func (k policyFreeKind) scope() oam.ObjectScope {
	if k.namespaced {
		return oam.ObjectScopeNamespaced
	}
	return oam.ObjectScopeCluster
}

// policyFreeKinds lists them.
var policyFreeKinds = []policyFreeKind{
	{
		component: "storageclass", handler: &components.StorageClassHandler{},
		gvk: storagev1.SchemeGroupVersion.WithKind("StorageClass"),
		typ: reflect.TypeFor[storagev1.StorageClass](), wholeObject: true,
		minimal: map[string]any{"provisioner": "ebs.csi.aws.com"},
		full: map[string]any{
			"provisioner":          "ebs.csi.aws.com",
			"parameters":           map[string]any{"type": "gp3", "encrypted": "true"},
			"reclaimPolicy":        "Retain",
			"mountOptions":         []any{"noatime", "discard"},
			"allowVolumeExpansion": false,
			"volumeBindingMode":    "WaitForFirstConsumer",
			"allowedTopologies": []any{map[string]any{"matchLabelExpressions": []any{
				map[string]any{"key": "topology.kubernetes.io/zone", "values": []any{"eu-west-1a", "eu-west-1b"}},
			}}},
		},
	},
	{
		component: "volumeattributesclass", handler: &components.VolumeAttributesClassHandler{},
		gvk: storagev1.SchemeGroupVersion.WithKind("VolumeAttributesClass"),
		typ: reflect.TypeFor[storagev1.VolumeAttributesClass](), wholeObject: true,
		minimal: map[string]any{"driverName": "ebs.csi.aws.com", "parameters": map[string]any{"iops": "3000"}},
		full: map[string]any{
			"driverName": "ebs.csi.aws.com",
			"parameters": map[string]any{"iops": "6000", "throughput": "250"},
		},
	},
	{
		component: "priorityclass", handler: &components.PriorityClassHandler{},
		gvk: schedulingv1.SchemeGroupVersion.WithKind("PriorityClass"),
		typ: reflect.TypeFor[schedulingv1.PriorityClass](), wholeObject: true,
		full: map[string]any{
			"value":            1000000,
			"globalDefault":    true,
			"description":      "Workloads that must not be preempted.",
			"preemptionPolicy": "Never",
		},
	},
	{
		component: "runtimeclass", handler: &components.RuntimeClassHandler{},
		gvk: nodev1.SchemeGroupVersion.WithKind("RuntimeClass"),
		typ: reflect.TypeFor[nodev1.RuntimeClass](), wholeObject: true,
		minimal: map[string]any{"handler": "runc"},
		full: map[string]any{
			"handler":  "kata",
			"overhead": map[string]any{"podFixed": map[string]any{"cpu": "250m", "memory": 134217728}},
			"scheduling": map[string]any{
				"nodeSelector": map[string]any{"runtime": "kata"},
				"tolerations": []any{
					map[string]any{"key": "runtime", "operator": "Equal", "value": "kata", "effect": "NoSchedule"},
				},
			},
		},
	},
	{
		component: "ingressclass", handler: &components.IngressClassHandler{},
		gvk:     networkingv1.SchemeGroupVersion.WithKind("IngressClass"),
		typ:     reflect.TypeFor[networkingv1.IngressClassSpec](),
		minimal: map[string]any{"controller": "k8s.io/ingress-nginx"},
		full: map[string]any{
			"controller": "k8s.io/ingress-nginx",
			"parameters": map[string]any{
				"apiGroup": "k8s.example.com", "kind": "IngressParameters", "name": "external",
				"scope": "Namespace", "namespace": "ingress",
			},
		},
	},
	{
		component: "csidriver", handler: &components.CSIDriverHandler{},
		gvk: storagev1.SchemeGroupVersion.WithKind("CSIDriver"),
		typ: reflect.TypeFor[storagev1.CSIDriverSpec](),
		full: map[string]any{
			"attachRequired":                     false,
			"podInfoOnMount":                     true,
			"volumeLifecycleModes":               []any{"Persistent", "Ephemeral"},
			"storageCapacity":                    true,
			"fsGroupPolicy":                      "File",
			"tokenRequests":                      []any{map[string]any{"audience": "vault", "expirationSeconds": 3600}, map[string]any{"audience": ""}},
			"requiresRepublish":                  true,
			"seLinuxMount":                       false,
			"nodeAllocatableUpdatePeriodSeconds": 60,
			"serviceAccountTokenInSecrets":       true,
			"preventPodSchedulingIfMissing":      false,
		},
	},
	{
		component: "servicecidr", handler: &components.ServiceCIDRHandler{},
		gvk:     networkingv1.SchemeGroupVersion.WithKind("ServiceCIDR"),
		typ:     reflect.TypeFor[networkingv1.ServiceCIDRSpec](),
		minimal: map[string]any{"cidrs": []any{"10.96.0.0/16"}},
		full:    map[string]any{"cidrs": []any{"10.96.0.0/16", "fd00:10:96::/112"}},
	},
	{
		component: "poddisruptionbudget", handler: &components.PodDisruptionBudgetHandler{},
		gvk: policyv1.SchemeGroupVersion.WithKind("PodDisruptionBudget"),
		typ: reflect.TypeFor[policyv1.PodDisruptionBudgetSpec](), namespaced: true,
		minimal: map[string]any{},
		// The API allows minAvailable or maxUnavailable, not both; the kind
		// leaves that to the API server, and the fixture sets every field.
		full: map[string]any{
			"minAvailable":   0,
			"maxUnavailable": "25%",
			"selector": map[string]any{
				"matchLabels":      map[string]any{"app": "web"},
				"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"frontend", "edge"}}},
			},
			"unhealthyPodEvictionPolicy": "AlwaysAllow",
		},
	},
	{
		component: "servicemonitor", handler: &components.ServiceMonitorHandler{},
		gvk: monitoringv1.SchemeGroupVersion.WithKind("ServiceMonitor"),
		typ: reflect.TypeFor[monitoringv1.ServiceMonitorSpec](), namespaced: true,
		// The API requires both; an empty list and an empty selector are
		// authored values.
		minimal: map[string]any{"endpoints": []any{}, "selector": map[string]any{}},
		// The operator lets an endpoint authenticate one way; the kind leaves
		// that to it. The two endpoints take one way each, oauth2 and
		// authorization; basicAuth and bearerTokenSecret are authored on the
		// rows of the other kinds.
		full: monitoringScrapeFull(map[string]any{
			"jobLabel":        "app.kubernetes.io/name",
			"targetLabels":    []any{"team"},
			"podTargetLabels": []any{"version"},
			"endpoints": []any{
				map[string]any{
					"port": "metrics", "path": "/metrics", "scheme": "https", "interval": "30s", "scrapeTimeout": "10s",
					"honorLabels": false, "honorTimestamps": false,
					"params":            map[string]any{"format": []any{"prometheus"}},
					"relabelings":       []any{map[string]any{"sourceLabels": []any{"__meta_kubernetes_pod_node_name"}, "targetLabel": "node"}},
					"metricRelabelings": []any{map[string]any{"sourceLabels": []any{"__name__"}, "regex": "go_.*", "action": "drop"}},
					"oauth2":            monitoringOAuth2(),
					"tlsConfig": map[string]any{
						"serverName": "metrics.example.com", "insecureSkipVerify": false,
						"ca": map[string]any{"secret": map[string]any{"name": "metrics-ca", "key": "ca.crt"}},
					},
					"proxyUrl": "http://proxy.example.com:3128",
				},
				map[string]any{
					"targetPort":    9090,
					"authorization": map[string]any{"credentials": map[string]any{"name": "scrape", "key": "token"}},
				},
			},
			"selector": map[string]any{
				"matchLabels":      map[string]any{"app": "web"},
				"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"frontend", "edge"}}},
			},
			"selectorMechanism":    "RoleSelector",
			"namespaceSelector":    map[string]any{"matchNames": []any{"payments", "billing"}},
			"attachMetadata":       map[string]any{"node": false},
			"bodySizeLimit":        "512MB",
			"serviceDiscoveryRole": "EndpointSlice",
		}),
	},
	{
		component: "podmonitor", handler: &components.PodMonitorHandler{},
		gvk: monitoringv1.SchemeGroupVersion.WithKind("PodMonitor"),
		typ: reflect.TypeFor[monitoringv1.PodMonitorSpec](), namespaced: true,
		minimal: map[string]any{"selector": map[string]any{}},
		full: monitoringScrapeFull(map[string]any{
			"jobLabel":        "app.kubernetes.io/name",
			"podTargetLabels": []any{"version"},
			"podMetricsEndpoints": []any{
				map[string]any{
					"port": "metrics", "path": "/metrics", "scheme": "http", "interval": "30s",
					"honorLabels": true, "filterRunning": false,
					"relabelings": []any{map[string]any{"action": "labelmap", "regex": "__meta_kubernetes_pod_label_(.+)"}},
					"basicAuth": map[string]any{
						"username": map[string]any{"name": "scrape", "key": "username"},
						"password": map[string]any{"name": "scrape", "key": "password"},
					},
				},
				map[string]any{"portNumber": 9090, "oauth2": monitoringOAuth2(), "enableHttp2": false},
			},
			"selector": map[string]any{
				"matchLabels":      map[string]any{"app": "web"},
				"matchExpressions": []any{map[string]any{"key": "tier", "operator": "Exists"}},
			},
			"selectorMechanism": "RelabelConfig",
			"namespaceSelector": map[string]any{"any": true},
			"attachMetadata":    map[string]any{"node": true},
			"bodySizeLimit":     "16MiB",
		}),
	},
	{
		component: "prometheus-probe", handler: &components.PrometheusProbeHandler{},
		gvk: monitoringv1.SchemeGroupVersion.WithKind("Probe"),
		typ: reflect.TypeFor[monitoringv1.ProbeSpec](), namespaced: true,
		// The type always writes a prober, and the API refuses one with no url.
		minimal: probeProperties(),
		// The operator lets a Probe authenticate one way and reads one kind of
		// target; the kind leaves both to it, and the fixture sets every field.
		full: monitoringScrapeFull(map[string]any{
			"jobName": "blackbox",
			"prober": map[string]any{
				"url": "blackbox-exporter.monitoring.svc:9115", "scheme": "http", "path": "/probe",
				"proxyUrl": "http://proxy.example.com:3128", "noProxy": "cluster.local", "proxyFromEnvironment": false,
			},
			"module": "http_2xx",
			"targets": map[string]any{
				"staticConfig": map[string]any{
					"static":            []any{"https://example.com", "https://example.org"},
					"labels":            map[string]any{"environment": "production"},
					"relabelingConfigs": []any{map[string]any{"sourceLabels": []any{"__address__"}, "targetLabel": "target"}},
				},
				"ingress": map[string]any{
					"selector":          map[string]any{"matchLabels": map[string]any{"probe": "true"}},
					"namespaceSelector": map[string]any{"any": true},
				},
			},
			"interval":          "60s",
			"scrapeTimeout":     "30s",
			"metricRelabelings": []any{map[string]any{"action": "labeldrop", "regex": "pod"}},
			"authorization":     map[string]any{"type": "Bearer", "credentials": map[string]any{"name": "probe", "key": "token"}},
			"params":            []any{map[string]any{"name": "debug", "values": []any{"true"}}},
			"basicAuth": map[string]any{
				"username": map[string]any{"name": "probe", "key": "username"},
				"password": map[string]any{"name": "probe", "key": "password"},
			},
			"oauth2":            monitoringOAuth2(),
			"bearerTokenSecret": map[string]any{"name": "probe", "key": "token"},
			"followRedirects":   false,
			"enableHttp2":       false,
			"tlsConfig":         map[string]any{"insecureSkipVerify": true, "minVersion": "TLS12"},
		}),
	},
	{
		component: "prometheusrule", handler: &components.PrometheusRuleHandler{},
		gvk: monitoringv1.SchemeGroupVersion.WithKind("PrometheusRule"),
		typ: reflect.TypeFor[monitoringv1.PrometheusRuleSpec](), namespaced: true,
		minimal: map[string]any{},
		full: map[string]any{"groups": []any{
			map[string]any{
				"name": "availability", "interval": "1m", "query_offset": "30s", "limit": 0,
				"labels": map[string]any{"team": "payments"}, "partial_response_strategy": "warn",
				"rules": []any{
					map[string]any{"record": "job:up:sum", "expr": "sum by (job) (up)", "labels": map[string]any{"tier": "frontend"}},
					map[string]any{
						"alert": "TargetDown", "expr": "job:up:sum == 0", "for": "5m", "keep_firing_for": "10m",
						"labels":      map[string]any{"severity": "page"},
						"annotations": map[string]any{"summary": "No target of {{ $labels.job }} is up."},
					},
					// An expression may be a bare number.
					map[string]any{"record": "zero", "expr": 0},
				},
			},
			map[string]any{"name": "empty"},
		}},
	},
	{
		component: "issuer", handler: &components.IssuerHandler{},
		gvk: certv1.SchemeGroupVersion.WithKind("Issuer"),
		typ: reflect.TypeFor[certv1.IssuerSpec](), namespaced: true, held: true,
		// The API's schema requires no issuer type; cert-manager's webhook wants
		// one, and the kind leaves that to it.
		minimal: map[string]any{},
		full:    issuerFull(),
	},
	{
		component: "clusterissuer", handler: &components.ClusterIssuerHandler{},
		gvk: certv1.SchemeGroupVersion.WithKind("ClusterIssuer"),
		typ: reflect.TypeFor[certv1.IssuerSpec](), held: true,
		minimal: map[string]any{},
		full:    issuerFull(),
	},
	{
		component: "certificate", handler: &components.CertificateHandler{},
		gvk: certv1.SchemeGroupVersion.WithKind("Certificate"),
		typ: reflect.TypeFor[certv1.CertificateSpec](), namespaced: true, held: true,
		minimal: certificateMinimal(),
		full:    certificateFull(),
	},
	{
		component: "cilium-bgpadvertisement", handler: &components.CiliumBGPAdvertisementHandler{},
		gvk:     ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.BGPAKindDefinition),
		typ:     reflect.TypeFor[ciliumv2.CiliumBGPAdvertisementSpec](),
		minimal: map[string]any{"advertisements": []any{}},
		// One entry of each type, each with what the API requires of its type
		// and nothing it refuses with it.
		full: map[string]any{"advertisements": []any{
			map[string]any{"advertisementType": "PodCIDR", "attributes": map[string]any{
				"communities": map[string]any{
					"standard": []any{"65000:100"}, "wellKnown": []any{"no-export"}, "large": []any{"65000:100:50"},
				},
				"localPreference": 200,
			}},
			map[string]any{
				"advertisementType": "CiliumPodIPPool",
				"selector":          map[string]any{"matchLabels": map[string]any{"pool": "blue"}},
			},
			map[string]any{
				"advertisementType": "Service",
				"service": map[string]any{
					"addresses": []any{"LoadBalancerIP", "ClusterIP"}, "aggregationLengthIPv4": 0, "aggregationLengthIPv6": 64,
				},
				"selector": map[string]any{"matchExpressions": []any{
					map[string]any{"key": "bgp", "operator": "In", "values": []any{"blue", "green"}},
				}},
			},
			map[string]any{"advertisementType": "Interface", "interface": map[string]any{"name": "lo"}},
		}},
	},
	{
		component: "cilium-bgpclusterconfig", handler: &components.CiliumBGPClusterConfigHandler{},
		gvk:     ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.BGPCCKindDefinition),
		typ:     reflect.TypeFor[ciliumv2.CiliumBGPClusterConfigSpec](),
		minimal: bgpInstances(map[string]any{"name": "instance-65000"}),
		full: map[string]any{
			"nodeSelector": map[string]any{
				"matchLabels":      map[string]any{"bgp": "enabled"},
				"matchExpressions": []any{map[string]any{"key": "rack", "operator": "In", "values": []any{"a", "b"}}},
			},
			"bgpInstances": []any{
				map[string]any{
					"name": "instance-65000", "localASN": 65000, "localPort": 179,
					"peers": []any{
						// peerASN 0 accepts any ASN the peer opens with.
						map[string]any{"name": "tor-1", "peerAddress": "192.0.2.1", "peerASN": 0, "peerConfigRef": map[string]any{"name": "tor"}},
						map[string]any{"name": "gateway", "peerASN": 65001, "autoDiscovery": map[string]any{
							"mode": "DefaultGateway", "defaultGateway": map[string]any{"addressFamily": "ipv4"},
						}},
					},
				},
				map[string]any{"name": "instance-65010"},
			},
		},
	},
	{
		component: "cilium-bgpnodeconfigoverride", handler: &components.CiliumBGPNodeConfigOverrideHandler{},
		gvk:     ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.BGPNCOKindDefinition),
		typ:     reflect.TypeFor[ciliumv2.CiliumBGPNodeConfigOverrideSpec](),
		minimal: bgpInstances(map[string]any{"name": "instance-65000"}),
		full: bgpInstances(
			map[string]any{
				"name": "instance-65000", "routerID": "192.0.2.10", "localPort": 1790, "localASN": 65000,
				"peers": []any{
					map[string]any{"name": "tor-1", "localAddress": "192.0.2.10", "localPort": 1791},
					map[string]any{"name": "gateway"},
				},
			},
			map[string]any{"name": "instance-65010"},
		),
	},
	{
		component: "cilium-bgppeerconfig", handler: &components.CiliumBGPPeerConfigHandler{},
		gvk:     ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.BGPPCKindDefinition),
		typ:     reflect.TypeFor[ciliumv2.CiliumBGPPeerConfigSpec](),
		minimal: map[string]any{},
		full: map[string]any{
			"transport":     map[string]any{"peerPort": 1790, "sourceInterface": "lo"},
			"timers":        map[string]any{"connectRetryTimeSeconds": 30, "holdTimeSeconds": 9, "keepAliveTimeSeconds": 3},
			"authSecretRef": "bgp-auth",
			// The API requires `enabled` and fills no default: false is authored.
			"gracefulRestart": map[string]any{"enabled": false, "restartTimeSeconds": 60},
			"ebgpMultihop":    4,
			"families": []any{
				map[string]any{"afi": "ipv4", "safi": "unicast", "advertisements": map[string]any{
					"matchLabels": map[string]any{"launcher.gokure.dev/component": "pod-cidrs"},
				}},
				map[string]any{"afi": "ipv6", "safi": "unicast", "advertisements": map[string]any{
					"matchExpressions": []any{map[string]any{"key": "advertise", "operator": "In", "values": []any{"bgp"}}},
				}},
				map[string]any{"afi": "ipv4", "safi": "multicast"},
			},
		},
	},
}

// bgpInstances is the properties of a cilium-bgpclusterconfig or a
// cilium-bgpnodeconfigoverride with the instances.
func bgpInstances(instances ...any) map[string]any {
	return map[string]any{"bgpInstances": append([]any{}, instances...)}
}

// bgpAdvertisements is the properties of a cilium-bgpadvertisement with the
// entries.
func bgpAdvertisements(entries ...any) map[string]any {
	return map[string]any{"advertisements": append([]any{}, entries...)}
}

// bgpPeers is the properties of a cilium-bgpclusterconfig or a
// cilium-bgpnodeconfigoverride with one instance and its peers.
func bgpPeers(peers ...any) map[string]any {
	return bgpInstances(map[string]any{"name": "instance-65000", "peers": append([]any{}, peers...)})
}

// monitoringOAuth2 is an OAuth2 block of the Prometheus operator's API with
// the three fields it requires and one it does not.
func monitoringOAuth2() map[string]any {
	return map[string]any{
		"clientId":     map[string]any{"configMap": map[string]any{"name": "oauth", "key": "client-id"}},
		"clientSecret": map[string]any{"name": "oauth", "key": "client-secret"},
		"tokenUrl":     "https://auth.example.com/token",
		"scopes":       []any{"metrics"},
	}
}

// monitoringOAuth2Without is monitoringOAuth2 less one field.
func monitoringOAuth2Without(field string) map[string]any {
	block := monitoringOAuth2()
	delete(block, field)
	return block
}

// monitorWith is the properties of a ServiceMonitor or a PodMonitor with the
// selector the API requires and the endpoints, under the name the kind gives
// their list. With no endpoint the list is authored empty.
func monitorWith(list string, endpoints ...any) map[string]any {
	return map[string]any{"selector": map[string]any{}, list: append([]any{}, endpoints...)}
}

// probeProperties is the least a prometheus-probe may author.
func probeProperties() map[string]any {
	return map[string]any{"prober": map[string]any{"url": "blackbox-exporter.monitoring.svc:9115"}}
}

// ruleGroups is the properties of a prometheusrule with the groups.
func ruleGroups(groups ...any) map[string]any {
	return map[string]any{"groups": append([]any{}, groups...)}
}

// withProperty is props with one more property.
func withProperty(props map[string]any, name string, value any) map[string]any {
	props[name] = value
	return props
}

// monitoringScrapeFull is own with a value of every property the three scrape
// kinds of the Prometheus operator's API share. The limits take an authored 0,
// which the type keeps apart from an unset one.
func monitoringScrapeFull(own map[string]any) map[string]any {
	full := map[string]any{
		"sampleLimit":                    0,
		"targetLimit":                    100,
		"scrapeProtocols":                []any{"OpenMetricsText1.0.0", "PrometheusText0.0.4"},
		"fallbackScrapeProtocol":         "PrometheusText0.0.4",
		"labelLimit":                     64,
		"labelNameLengthLimit":           128,
		"labelValueLengthLimit":          1024,
		"scrapeNativeHistograms":         false,
		"scrapeClassicHistograms":        true,
		"nativeHistogramBucketLimit":     160,
		"nativeHistogramMinBucketFactor": 1.1,
		"convertClassicHistogramsToNHCB": false,
		"keepDroppedTargets":             0,
		"scrapeClass":                    "tenant",
	}
	maps.Copy(full, own)
	return full
}

// policyFreeJSON is obj as the JSON tree it encodes to, numbers kept exact.
func policyFreeJSON(t *testing.T, obj any) map[string]any {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal %T: %v", obj, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var tree map[string]any
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return tree
}

// TestPolicyFreeKinds_CanHandle: each handler takes its own type and none of
// the others.
func TestPolicyFreeKinds_CanHandle(t *testing.T) {
	for _, kind := range policyFreeKinds {
		for _, other := range policyFreeKinds {
			if got, want := kind.handler.CanHandle(other.component), kind.component == other.component; got != want {
				t.Errorf("%T: CanHandle(%q) = %v, want %v", kind.handler, other.component, got, want)
			}
		}
	}
}

// TestPolicyFreeKinds_FullCoversEveryField: the full fixture of each kind sets
// every authorable top-level field of the type it decodes into, so the tests
// that build from it see each field. A dependency bump that adds a field fails
// here until the fixture has it.
func TestPolicyFreeKinds_FullCoversEveryField(t *testing.T) {
	for _, kind := range policyFreeKinds {
		t.Run(kind.component, func(t *testing.T) {
			fields := specJSONFields(t, kind.typ)
			if kind.wholeObject {
				for _, identity := range []string{"kind", "apiVersion", "metadata"} {
					if _, ok := fields[identity]; !ok {
						t.Errorf("%s has no %q json field; the kind is not an object type", kind.typ, identity)
					}
					delete(fields, identity)
				}
				// A status is not the author's to write either, and
				// refuseObjectIdentityKeys does not know it.
				if _, ok := fields["status"]; ok {
					t.Errorf("%s has a status field, which a whole-object kind would let an author write", kind.typ)
				}
			}
			for name := range fields {
				if _, ok := kind.full[name]; !ok {
					t.Errorf("the full fixture sets no %q, a field of %s", name, kind.typ)
				}
			}
			for name := range kind.full {
				if _, ok := fields[name]; !ok {
					t.Errorf("the full fixture sets %q, which is no field of %s", name, kind.typ)
				}
			}
		})
	}
}

// TestPolicyFreeKinds_EmitIdentityAndTheAuthoredFields: the object is the
// kind's, named after the component and in the build namespace or, for a
// cluster-scoped kind, in none, and beside that identity it holds exactly what
// was authored: nothing with the least a component may author, every field
// with the full fixture. A null field is an unauthored one. The comparison is
// on the encoded object, so a field the build dropped or added shows,
// whichever it is.
func TestPolicyFreeKinds_EmitIdentityAndTheAuthoredFields(t *testing.T) {
	for _, kind := range policyFreeKinds {
		withNulls := map[string]any{}
		for name := range kind.full {
			withNulls[name] = nil
		}
		for name, value := range kind.minimal {
			withNulls[name] = value
		}
		for name, props := range map[string]map[string]any{
			"minimal": kind.minimal, "minimal, the rest null": withNulls, "full": kind.full,
		} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				// What was authored is read before the handler sees the
				// properties, so a handler that changed its input could not
				// change what its object is compared with.
				authored, data := authoredProperties(t, props)
				before, err := json.Marshal(props)
				if err != nil {
					t.Fatalf("marshal the properties: %v", err)
				}

				obj := kind.generate(t, "fast", props)
				if got := obj.GetObjectKind().GroupVersionKind(); got != kind.gvk {
					t.Errorf("GVK = %s, want %s", got, kind.gvk)
				}
				if want := kind.namespace(coreKindNamespace); obj.GetNamespace() != want {
					t.Errorf("namespace = %q, want %q: the build namespace, or none on a cluster-scoped object", obj.GetNamespace(), want)
				}
				// The whole input, null entries included.
				if after, err := json.Marshal(props); err != nil || !bytes.Equal(after, before) {
					t.Errorf("the handler changed its input: %s (err %v), was %s", after, err, before)
				}

				// What the object must encode to: its identity and the authored
				// fields, as the upstream type encodes those fields.
				decoded := reflect.New(kind.typ).Interface()
				if err := json.Unmarshal(data, decoded); err != nil {
					t.Fatalf("decode the authored properties into %s: %v", kind.typ, err)
				}
				want := map[string]any{}
				if kind.wholeObject {
					want = policyFreeJSON(t, decoded)
				} else {
					want["spec"] = policyFreeJSON(t, decoded)
				}
				apiVersion, kindName := kind.gvk.ToAPIVersionAndKind()
				want["apiVersion"], want["kind"] = apiVersion, kindName
				metadata := map[string]any{"name": "fast"}
				if kind.namespaced {
					metadata["namespace"] = coreKindNamespace
				}
				want["metadata"] = metadata

				got := policyFreeJSON(t, obj)
				// A type with a status always encodes one. It is the system's
				// to write, so the object's must be unset.
				if status := reflect.ValueOf(obj).Elem().FieldByName("Status"); status.IsValid() {
					if !status.IsZero() {
						t.Errorf("status = %+v, want none: no component authors a status", status.Interface())
					}
					delete(got, "status")
				}
				// Older apimachinery encodes an unset creation time as null.
				if meta, ok := got["metadata"].(map[string]any); ok && meta["creationTimestamp"] == nil {
					delete(meta, "creationTimestamp")
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("object = %v\nwant     %v", got, want)
				}
				// Every authored field is in the encoded object under its own name.
				holder := got
				if !kind.wholeObject {
					holder, _ = got["spec"].(map[string]any)
				}
				for name := range authored {
					if _, ok := holder[name]; !ok {
						t.Errorf("authored %q is not in the object", name)
					}
				}
			})
		}
	}
}

// authoredProperties returns the names of the properties that are not null,
// and those properties as JSON. encoding/json writes map keys sorted, so two
// encodings of the same content are equal.
func authoredProperties(t *testing.T, props map[string]any) (map[string]struct{}, []byte) {
	t.Helper()
	authored := map[string]any{}
	names := map[string]struct{}{}
	for name, value := range props {
		if value != nil {
			authored[name] = value
			names[name] = struct{}{}
		}
	}
	data, err := json.Marshal(authored)
	if err != nil {
		t.Fatalf("marshal the authored properties: %v", err)
	}
	return names, data
}

// TestPolicyFreeKinds_GenerateCopies: each Generate returns an object of its
// own, sharing no map, slice or pointer with the next, so what the transform
// writes on one (the component label, for one) does not reach another build
// of the same config. reaches names, per case, references the authored fields
// hold: the walk must find each when an object is compared with itself, or it
// would pass without having looked there.
func TestPolicyFreeKinds_GenerateCopies(t *testing.T) {
	// An Issuer and a ClusterIssuer hold one spec type.
	issuerReaches := []string{
		".Spec.IssuerConfig.ACME", ".Spec.IssuerConfig.ACME.CABundle", ".Spec.IssuerConfig.ACME.ExternalAccountBinding",
		".Spec.IssuerConfig.ACME.Solvers", ".Spec.IssuerConfig.ACME.Solvers[0].Selector.MatchLabels",
		".Spec.IssuerConfig.ACME.Solvers[0].HTTP01.Ingress.PodTemplate",
		".Spec.IssuerConfig.ACME.Solvers[0].HTTP01.Ingress.PodTemplate.ACMEChallengeSolverHTTP01IngressPodObjectMeta.Labels",
		".Spec.IssuerConfig.ACME.Solvers[0].HTTP01.Ingress.PodTemplate.Spec.Resources.Limits",
		".Spec.IssuerConfig.ACME.Solvers[0].HTTP01.Ingress.PodTemplate.Spec.SecurityContext.RunAsUser",
		".Spec.IssuerConfig.ACME.Solvers[1].HTTP01.GatewayHTTPRoute.ParentRefs",
		".Spec.IssuerConfig.ACME.Solvers[2].WaitInsteadOfSelfCheck",
		".Spec.IssuerConfig.ACME.Solvers[3].DNS01.Webhook.Config.Raw",
		".Spec.IssuerConfig.CA.CRLDistributionPoints", ".Spec.IssuerConfig.Vault.CABundleSecretRef",
		".Spec.IssuerConfig.Vault.Auth.Kubernetes.ServiceAccountRef.TokenAudiences",
		".Spec.IssuerConfig.SelfSigned", ".Spec.IssuerConfig.Venafi.TPP.CABundleSecretRef",
	}
	reaches := map[string][]string{
		"storageclass":          {".Parameters", ".ReclaimPolicy", ".MountOptions", ".AllowedTopologies"},
		"volumeattributesclass": {".Parameters"},
		"priorityclass":         {".PreemptionPolicy"},
		"runtimeclass":          {".Overhead", ".Overhead.PodFixed", ".Scheduling.NodeSelector", ".Scheduling.Tolerations"},
		"ingressclass":          {".Spec.Parameters", ".Spec.Parameters.APIGroup"},
		"csidriver":             {".Spec.AttachRequired", ".Spec.VolumeLifecycleModes", ".Spec.TokenRequests"},
		"servicecidr":           {".Spec.CIDRs"},
		"poddisruptionbudget": {
			".Spec.MinAvailable", ".Spec.MaxUnavailable", ".Spec.Selector", ".Spec.Selector.MatchLabels",
			".Spec.Selector.MatchExpressions", ".Spec.Selector.MatchExpressions[0].Values", ".Spec.UnhealthyPodEvictionPolicy",
		},
		"servicemonitor": {
			".Spec.Endpoints", ".Spec.Endpoints[0].Params", ".Spec.Endpoints[0].RelabelConfigs",
			".Spec.Endpoints[0].HTTPConfigWithProxyAndTLSFiles.HTTPConfigWithTLSFiles.HTTPConfigWithoutTLS.OAuth2",
			".Spec.Endpoints[1].TargetPort", ".Spec.Selector.MatchLabels", ".Spec.NamespaceSelector.MatchNames",
			".Spec.SampleLimit", ".Spec.ScrapeProtocols", ".Spec.NativeHistogramConfig.NativeHistogramMinBucketFactor",
			".Spec.AttachMetadata",
		},
		"podmonitor": {
			".Spec.PodMetricsEndpoints", ".Spec.PodMetricsEndpoints[0].Port", ".Spec.PodMetricsEndpoints[0].RelabelConfigs",
			".Spec.PodMetricsEndpoints[0].HTTPConfigWithProxy.HTTPConfig.HTTPConfigWithoutTLS.BasicAuth",
			".Spec.Selector.MatchLabels", ".Spec.Selector.MatchExpressions", ".Spec.KeepDroppedTargets", ".Spec.BodySizeLimit",
		},
		"prometheus-probe": {
			".Spec.ProberSpec.Scheme", ".Spec.ProberSpec.ProxyConfig.ProxyURL", ".Spec.Targets.StaticConfig",
			".Spec.Targets.StaticConfig.Targets", ".Spec.Targets.StaticConfig.Labels", ".Spec.Targets.Ingress",
			".Spec.Targets.Ingress.Selector.MatchLabels", ".Spec.MetricRelabelConfigs", ".Spec.Authorization",
			".Spec.Authorization.Credentials", ".Spec.Params", ".Spec.Params[0].Values",
			".Spec.HTTPConfig.HTTPConfigWithoutTLS.OAuth2", ".Spec.HTTPConfig.TLSConfig",
		},
		"prometheusrule": {
			".Spec.Groups", ".Spec.Groups[0].Labels", ".Spec.Groups[0].Interval", ".Spec.Groups[0].Limit",
			".Spec.Groups[0].Rules", ".Spec.Groups[0].Rules[1].For", ".Spec.Groups[0].Rules[1].Annotations",
		},
		"issuer":        issuerReaches,
		"clusterissuer": issuerReaches,
		"certificate": {
			".Spec.Subject", ".Spec.Subject.Organizations", ".Spec.Duration", ".Spec.RenewBeforePercentage",
			".Spec.Renewal", ".Spec.Renewal.Windows", ".Spec.Renewal.Windows[0].WindowDuration", ".Spec.DNSNames",
			".Spec.OtherNames", ".Spec.SecretTemplate.Labels", ".Spec.Keystores", ".Spec.Keystores.JKS",
			".Spec.Keystores.JKS.Alias", ".Spec.Keystores.PKCS12", ".Spec.Usages", ".Spec.PrivateKey",
			".Spec.EncodeUsagesInRequest", ".Spec.RevisionHistoryLimit", ".Spec.AdditionalOutputFormats",
			".Spec.NameConstraints", ".Spec.NameConstraints.Permitted.DNSDomains",
		},
		"cilium-bgpadvertisement": {
			".Spec.Advertisements", ".Spec.Advertisements[0].Attributes", ".Spec.Advertisements[0].Attributes.Communities",
			".Spec.Advertisements[0].Attributes.Communities.Standard", ".Spec.Advertisements[0].Attributes.LocalPreference",
			".Spec.Advertisements[1].Selector", ".Spec.Advertisements[1].Selector.MatchLabels",
			".Spec.Advertisements[2].Service", ".Spec.Advertisements[2].Service.Addresses",
			".Spec.Advertisements[2].Service.AggregationLengthIPv4",
			".Spec.Advertisements[2].Selector.MatchExpressions[0].Values", ".Spec.Advertisements[3].Interface",
		},
		"cilium-bgpclusterconfig": {
			".Spec.NodeSelector", ".Spec.NodeSelector.MatchLabels", ".Spec.NodeSelector.MatchExpressions[0].Values",
			".Spec.BGPInstances", ".Spec.BGPInstances[0].LocalASN", ".Spec.BGPInstances[0].LocalPort",
			".Spec.BGPInstances[0].Peers", ".Spec.BGPInstances[0].Peers[0].PeerAddress",
			".Spec.BGPInstances[0].Peers[0].PeerASN", ".Spec.BGPInstances[0].Peers[0].PeerConfigRef",
			".Spec.BGPInstances[0].Peers[1].AutoDiscovery", ".Spec.BGPInstances[0].Peers[1].AutoDiscovery.DefaultGateway",
		},
		"cilium-bgpnodeconfigoverride": {
			".Spec.BGPInstances", ".Spec.BGPInstances[0].RouterID", ".Spec.BGPInstances[0].LocalPort",
			".Spec.BGPInstances[0].LocalASN", ".Spec.BGPInstances[0].Peers",
			".Spec.BGPInstances[0].Peers[0].LocalAddress", ".Spec.BGPInstances[0].Peers[0].LocalPort",
		},
		"cilium-bgppeerconfig": {
			".Spec.Transport", ".Spec.Transport.PeerPort", ".Spec.Transport.SourceInterface", ".Spec.Timers",
			".Spec.Timers.HoldTimeSeconds", ".Spec.AuthSecretRef", ".Spec.GracefulRestart",
			".Spec.GracefulRestart.RestartTimeSeconds", ".Spec.EBGPMultihop", ".Spec.Families",
			".Spec.Families[0].Advertisements", ".Spec.Families[0].Advertisements.MatchLabels",
			".Spec.Families[1].Advertisements.MatchExpressions[0].Values",
		},
	}
	type copyCase struct {
		name      string
		component string
		handler   oam.ComponentHandler
		props     map[string]any
		reaches   []string
	}
	var cases []copyCase
	for _, kind := range policyFreeKinds {
		if len(reaches[kind.component]) == 0 {
			t.Fatalf("%s names no reference the walk must reach", kind.component)
		}
		cases = append(cases, copyCase{kind.component, kind.component, kind.handler, kind.full, reaches[kind.component]})
	}
	// A quantity too large for its integer form keeps its number behind an
	// unexported pointer, which only the type's own DeepCopy copies.
	cases = append(cases, copyCase{
		name: "runtimeclass/decimal-backed quantity", component: "runtimeclass", handler: &components.RuntimeClassHandler{},
		props: map[string]any{
			"handler":  "kata",
			"overhead": map[string]any{"podFixed": map[string]any{"memory": "100000000000Gi"}},
		},
		reaches: []string{".Overhead.PodFixed[memory].d.Dec"},
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := tc.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: tc.component, Properties: tc.props}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			var objs [2]reflect.Value
			for i := range objs {
				generated, err := cfg.Generate(stack.NewApplication("fast", coreKindNamespace, cfg))
				if err != nil || len(generated) != 1 {
					t.Fatalf("Generate: %d objects, err %v", len(generated), err)
				}
				objs[i] = reflect.ValueOf(*generated[0])
			}
			if !reflect.DeepEqual(objs[0].Interface(), objs[1].Interface()) {
				t.Fatalf("two builds differ: %+v and %+v", objs[0].Interface(), objs[1].Interface())
			}
			shared := sharedReferences(objs[0], objs[1], "")
			if len(shared) != 0 {
				t.Errorf("two builds share %v", shared)
			}
			// Vacuity guard: compared with itself, an object shares every
			// reference it holds, so the walk must report the named ones.
			self := sharedReferences(objs[0], objs[0], "")
			for _, path := range tc.reaches {
				if !slices.Contains(self, path) {
					t.Errorf("the walk did not reach %s; it found %v", path, self)
				}
			}
		})
	}
}

// sharedReferences returns the paths at which a and b, two values of one
// type, hold the same non-nil pointer, map or slice backing array. It reads
// unexported fields too: a copy that left one shared is not a copy.
func sharedReferences(a, b reflect.Value, path string) []string {
	var shared []string
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return nil
		}
		if a.Kind() == reflect.Pointer && a.Pointer() == b.Pointer() {
			shared = append(shared, path)
		}
		return append(shared, sharedReferences(a.Elem(), b.Elem(), path)...)
	case reflect.Struct:
		for i := range a.NumField() {
			shared = append(shared, sharedReferences(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name)...)
		}
	case reflect.Map:
		if a.Len() == 0 || b.Len() == 0 {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			shared = append(shared, path)
		}
		for _, key := range a.MapKeys() {
			if other := b.MapIndex(key); other.IsValid() {
				shared = append(shared, sharedReferences(a.MapIndex(key), other, fmt.Sprintf("%s[%v]", path, key))...)
			}
		}
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			shared = append(shared, path)
		}
		for i := range min(a.Len(), b.Len()) {
			shared = append(shared, sharedReferences(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))...)
		}
	case reflect.Array:
		for i := range a.Len() {
			shared = append(shared, sharedReferences(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))...)
		}
	default:
		// A scalar is copied by value and holds no reference. An API type
		// declares no channel, function or unsafe pointer.
	}
	return shared
}

// TestPolicyFreeKinds_ObjectIdentityIsNotAuthorable: no kind lets an author
// write the object's kind, apiVersion or metadata, under any spelling and
// whatever the value. A whole-object kind refuses each by name; a kind that
// projects a spec type refuses it as a key the spec does not have.
func TestPolicyFreeKinds_ObjectIdentityIsNotAuthorable(t *testing.T) {
	for _, kind := range policyFreeKinds {
		want := ": not authorable: launcher sets the object's kind, apiVersion and metadata"
		if !kind.wholeObject {
			want = "properties do not decode into a "
		}
		for _, tc := range []struct {
			key   string
			value any
		}{
			{"kind", "Pod"}, {"apiVersion", "v1"}, {"metadata", map[string]any{"labels": map[string]any{"a": "b"}}},
			{"metadata", nil}, {"Kind", "Pod"}, {"APIVersion", "v1"}, {"Metadata", map[string]any{"name": "other"}},
		} {
			t.Run(fmt.Sprintf("%s/%s=%v", kind.component, tc.key, tc.value), func(t *testing.T) {
				props := map[string]any{tc.key: tc.value}
				for name, value := range kind.minimal {
					props[name] = value
				}
				err := coreKindErr(kind.handler, kind.component, "fast", props)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one mentioning %q", err, want)
				}
				if kind.wholeObject && !strings.HasPrefix(err.Error(), tc.key+":") {
					t.Errorf("err = %v, want it to name the key %q as written", err, tc.key)
				}
			})
		}
	}
}

// TestPolicyFreeKinds_Refusals: the properties are the fields of the type and
// nothing else, at any depth, and a field the API requires must be authored:
// a top-level one, and on the Prometheus operator's and cert-manager's kinds
// a nested one the type would write unauthored.
func TestPolicyFreeKinds_Refusals(t *testing.T) {
	const notA = "properties do not decode into a "
	type refusal struct {
		name  string
		props map[string]any
		want  string
	}
	// An Issuer and a ClusterIssuer hold one spec type. None of its fields is
	// required at the top level: of an issuer type that is authored the API
	// requires some, and of what is authored below those.
	vault := func(auth map[string]any) map[string]any {
		return map[string]any{"vault": map[string]any{"server": "https://vault.example.com", "path": "pki/sign/web", "auth": auth}}
	}
	issuerCases := []refusal{
		{"acme without a server", map[string]any{"acme": map[string]any{"privateKeySecretRef": map[string]any{"name": "acme-account"}}}, "acme.server: required"},
		{"acme without an account key", map[string]any{"acme": map[string]any{"server": "https://acme.example.com/directory"}}, "acme.privateKeySecretRef: required"},
		{"account key without a name", map[string]any{"acme": acmeWith("privateKeySecretRef", map[string]any{"key": "tls.key"})}, "acme.privateKeySecretRef.name: required"},
		{"account binding without a key ID", map[string]any{"acme": acmeWith("externalAccountBinding", map[string]any{"keySecretRef": secretKey("acme-eab", "hmac")})}, "acme.externalAccountBinding.keyID: required"},
		{"an empty ca", map[string]any{"ca": map[string]any{}}, "ca.secretName: required"},
		{"vault without auth", map[string]any{"vault": map[string]any{"server": "https://vault.example.com", "path": "pki/sign/web"}}, "vault.auth: required"},
		{"vault without a path", map[string]any{"vault": map[string]any{"server": "https://vault.example.com", "auth": map[string]any{}}}, "vault.path: required"},
		{"optional reference without a name", vault(map[string]any{"tokenSecretRef": map[string]any{"key": "token"}}), "vault.auth.tokenSecretRef.name: required"},
		{"app role without its secret", vault(map[string]any{"appRole": map[string]any{"path": "approle", "roleId": "issuer"}}), "vault.auth.appRole.secretRef: required"},
		{"service account reference without a name", vault(map[string]any{"kubernetes": map[string]any{"role": "issuer", "serviceAccountRef": map[string]any{"audiences": []any{"vault"}}}}), "vault.auth.kubernetes.serviceAccountRef.name: required"},
		{"venafi without a zone", map[string]any{"venafi": map[string]any{"tpp": map[string]any{"url": "https://tpp.example.com/vedsdk", "credentialsRef": map[string]any{"name": "tpp"}}}}, "venafi.zone: required"},
		{"a later solver's provider", acmeIssuer(
			map[string]any{"http01": map[string]any{"ingress": map[string]any{}}},
			map[string]any{"dns01": map[string]any{"digitalocean": map[string]any{}}},
		), "acme.solvers[1].dns01.digitalocean.tokenSecretRef: required"},
		{"webhook solver without a name", acmeIssuer(map[string]any{"dns01": map[string]any{"webhook": map[string]any{"groupName": "acme.example.com"}}}), "acme.solvers[0].dns01.webhook.solverName: required"},
		{"unknown key", map[string]any{"selfSigned": map[string]any{}, "letsEncrypt": map[string]any{}}, notA + "cert-manager.io/v1 IssuerSpec"},
		{"the object's spec", map[string]any{"spec": map[string]any{"selfSigned": map[string]any{}}}, notA},
		{"issuer type sub-key", map[string]any{"selfSigned": map[string]any{"crlDistributionPoint": "http://crl.example.com"}}, notA},
		{"issuer type a string", map[string]any{"selfSigned": "true"}, notA},
		{"bundle not base64", map[string]any{"acme": acmeWith("caBundle", "-----BEGIN CERTIFICATE-----")}, notA},
		{"bad quantity", acmeIssuer(http01Solver("ingress", map[string]any{"limits": map[string]any{"cpu": "lots"}})), notA},
		{"solver sub-key", acmeIssuer(map[string]any{"http01": map[string]any{"ingress": map[string]any{"image": "registry.example/solver:1"}}}), notA},
		{"null solver", map[string]any{"acme": acmeWith("solvers", []any{nil})}, "acme.solvers[0]"},
		{"two spellings", map[string]any{"selfSigned": map[string]any{}, "SelfSigned": map[string]any{}}, "sets the same field as"},
	}
	cases := map[string][]refusal{
		"storageclass": {
			{"no properties", nil, "provisioner: required"},
			{"null provisioner", map[string]any{"provisioner": nil}, "provisioner: required"},
			{"empty provisioner", map[string]any{"provisioner": ""}, "provisioner: required"},
			{"unknown key", map[string]any{"provisioner": "p", "spec": map[string]any{}}, notA + "storage.k8s.io/v1 StorageClass"},
			{"parameter not a string", map[string]any{"provisioner": "p", "parameters": map[string]any{"iops": 3000}}, notA},
			{"topology sub-key", map[string]any{"provisioner": "p", "allowedTopologies": []any{map[string]any{"matchLabels": map[string]any{}}}}, notA},
			{"expansion a string", map[string]any{"provisioner": "p", "allowVolumeExpansion": "yes"}, notA},
			{"null mount option", map[string]any{"provisioner": "p", "mountOptions": []any{"ro", nil}}, "mountOptions[1]"},
			{"two spellings", map[string]any{"provisioner": "p", "Provisioner": "q"}, "sets the same field as"},
		},
		"volumeattributesclass": {
			{"no properties", nil, "driverName: required"},
			{"empty driverName", map[string]any{"driverName": "", "parameters": map[string]any{"iops": "1"}}, "driverName: required"},
			{"no parameters", map[string]any{"driverName": "d"}, "parameters: required"},
			{"null parameters", map[string]any{"driverName": "d", "parameters": nil}, "parameters: required"},
			{"empty parameters", map[string]any{"driverName": "d", "parameters": map[string]any{}}, "parameters: required"},
			{"unknown key", map[string]any{"driverName": "d", "driver": "d"}, notA + "storage.k8s.io/v1 VolumeAttributesClass"},
			{"parameter not a string", map[string]any{"driverName": "d", "parameters": map[string]any{"iops": 3000}}, notA},
			{"two spellings", map[string]any{"driverName": "d", "drivername": "e"}, "sets the same field as"},
		},
		"priorityclass": {
			{"unknown key", map[string]any{"value": 1, "priority": 1}, notA + "scheduling.k8s.io/v1 PriorityClass"},
			{"value a string", map[string]any{"value": "high"}, notA},
			{"value not an integer", map[string]any{"value": 1.5}, notA},
			{"value over int32", map[string]any{"value": 4294967296}, notA},
			{"globalDefault a string", map[string]any{"value": 1, "globalDefault": "true"}, notA},
			{"two spellings", map[string]any{"value": 1, "Value": 2}, "sets the same field as"},
		},
		"runtimeclass": {
			{"no properties", nil, "handler: required"},
			{"empty handler", map[string]any{"handler": ""}, "handler: required"},
			{"unknown key", map[string]any{"handler": "runc", "runtimeHandler": "runc"}, notA + "node.k8s.io/v1 RuntimeClass"},
			{"overhead sub-key", map[string]any{"handler": "runc", "overhead": map[string]any{"fixed": map[string]any{}}}, notA},
			{"bad quantity", map[string]any{"handler": "runc", "overhead": map[string]any{"podFixed": map[string]any{"cpu": "lots"}}}, notA},
			{"null toleration", map[string]any{"handler": "runc", "scheduling": map[string]any{"tolerations": []any{nil}}}, "scheduling.tolerations[0]"},
			{"two spellings", map[string]any{"handler": "runc", "Handler": "kata"}, "sets the same field as"},
		},
		"ingressclass": {
			{"no properties", nil, "controller: required"},
			{"null controller", map[string]any{"controller": nil}, "controller: required"},
			{"empty controller", map[string]any{"controller": ""}, "controller: required"},
			{"parameters alone", map[string]any{"parameters": map[string]any{"kind": "K", "name": "n"}}, "controller: required"},
			{"unknown key", map[string]any{"controllerName": "k8s.io/ingress-nginx"}, notA + "networking.k8s.io/v1 IngressClassSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"controller": "c"}}, notA},
			{"parameters sub-key", map[string]any{"controller": "c", "parameters": map[string]any{"kind": "K", "name": "n", "group": "g"}}, notA},
			{"parameters a string", map[string]any{"controller": "c", "parameters": "external"}, notA},
			{"two spellings", map[string]any{"controller": "a", "Controller": "b"}, "sets the same field as"},
		},
		"csidriver": {
			{"unknown key", map[string]any{"driverName": "ebs.csi.aws.com"}, notA + "storage.k8s.io/v1 CSIDriverSpec"},
			{"attachRequired a string", map[string]any{"attachRequired": "false"}, notA},
			{"token request sub-key", map[string]any{"tokenRequests": []any{map[string]any{"audiences": []any{"a"}}}}, notA},
			{"modes a string", map[string]any{"volumeLifecycleModes": "Persistent"}, notA},
			{"null mode", map[string]any{"volumeLifecycleModes": []any{"Persistent", nil}}, "volumeLifecycleModes[1]"},
			{"two spellings", map[string]any{"fsGroupPolicy": "File", "FSGroupPolicy": "None"}, "sets the same field as"},
		},
		"servicecidr": {
			{"no properties", nil, "cidrs: required"},
			{"null cidrs", map[string]any{"cidrs": nil}, "cidrs: required"},
			{"empty cidrs", map[string]any{"cidrs": []any{}}, "cidrs: required"},
			{"unknown key", map[string]any{"cidrs": []any{"10.96.0.0/16"}, "cidr": "10.96.0.0/16"}, notA + "networking.k8s.io/v1 ServiceCIDRSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"cidrs": []any{"10.96.0.0/16"}}}, notA},
			{"cidrs a string", map[string]any{"cidrs": "10.96.0.0/16"}, notA},
			{"a block not a string", map[string]any{"cidrs": []any{10}}, notA},
			{"null block", map[string]any{"cidrs": []any{"10.96.0.0/16", nil}}, "cidrs[1]"},
			{"two spellings", map[string]any{"cidrs": []any{"10.96.0.0/16"}, "CIDRs": []any{"10.97.0.0/16"}}, "sets the same field as"},
		},
		"poddisruptionbudget": {
			{"unknown key", map[string]any{"minAvailable": 1, "minReady": 1}, notA + "policy/v1 PodDisruptionBudgetSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"minAvailable": 1}}, notA},
			{"minAvailable a fraction", map[string]any{"minAvailable": 1.5}, notA},
			{"minAvailable a boolean", map[string]any{"minAvailable": true}, notA},
			{"maxUnavailable a map", map[string]any{"maxUnavailable": map[string]any{"percent": 25}}, notA},
			{"selector sub-key", map[string]any{"selector": map[string]any{"labels": map[string]any{"app": "web"}}}, notA},
			{"selector a string", map[string]any{"selector": "app=web"}, notA},
			{"null expression", map[string]any{"selector": map[string]any{"matchExpressions": []any{nil}}}, "selector.matchExpressions[0]"},
			{"two spellings", map[string]any{"minAvailable": 1, "MinAvailable": 2}, "sets the same field as"},
		},
		"servicemonitor": {
			{"no properties", nil, "endpoints: required"},
			{"no endpoints", map[string]any{"selector": map[string]any{}}, "endpoints: required"},
			{"null endpoints", map[string]any{"selector": map[string]any{}, "endpoints": nil}, "endpoints: required"},
			{"no selector", map[string]any{"endpoints": []any{}}, "selector: required"},
			{"null selector", map[string]any{"endpoints": []any{}, "selector": nil}, "selector: required"},
			{"oauth2 without a client", monitorWith("endpoints", map[string]any{"oauth2": monitoringOAuth2Without("clientId")}), "endpoints[0].oauth2.clientId: required"},
			{"oauth2 without a secret", monitorWith("endpoints", map[string]any{"oauth2": monitoringOAuth2Without("clientSecret")}), "endpoints[0].oauth2.clientSecret: required"},
			{"oauth2 without a token URL", monitorWith("endpoints", map[string]any{"port": "web"}, map[string]any{"oauth2": monitoringOAuth2Without("tokenUrl")}), "endpoints[1].oauth2.tokenUrl: required"},
			{"an empty oauth2", monitorWith("endpoints", map[string]any{"oauth2": map[string]any{}}), "endpoints[0].oauth2.clientId: required"},
			{"unknown key", withProperty(monitorWith("endpoints"), "podMetricsEndpoints", []any{}), notA + "monitoring.coreos.com/v1 ServiceMonitorSpec"},
			{"the object's spec", map[string]any{"spec": monitorWith("endpoints")}, notA},
			{"endpoint sub-key", monitorWith("endpoints", map[string]any{"portName": "web"}), notA},
			{"selector a string", map[string]any{"endpoints": []any{}, "selector": "app=web"}, notA},
			{"limit a string", withProperty(monitorWith("endpoints"), "sampleLimit", "many"), notA},
			{"bad quantity", withProperty(monitorWith("endpoints"), "nativeHistogramMinBucketFactor", "lots"), notA},
			{"null endpoint", map[string]any{"selector": map[string]any{}, "endpoints": []any{nil}}, "endpoints[0]"},
			{"two spellings", withProperty(monitorWith("endpoints"), "Selector", map[string]any{"matchLabels": map[string]any{"app": "web"}}), "sets the same field as"},
		},
		"podmonitor": {
			{"no properties", nil, "selector: required"},
			{"null selector", map[string]any{"selector": nil}, "selector: required"},
			{"endpoints alone", map[string]any{"podMetricsEndpoints": []any{map[string]any{"port": "web"}}}, "selector: required"},
			{"oauth2 without a client", monitorWith("podMetricsEndpoints", map[string]any{"oauth2": monitoringOAuth2Without("clientId")}), "podMetricsEndpoints[0].oauth2.clientId: required"},
			{"oauth2 without a secret", monitorWith("podMetricsEndpoints", map[string]any{"oauth2": monitoringOAuth2Without("clientSecret")}), "podMetricsEndpoints[0].oauth2.clientSecret: required"},
			{"oauth2 without a token URL", monitorWith("podMetricsEndpoints", map[string]any{"oauth2": monitoringOAuth2Without("tokenUrl")}), "podMetricsEndpoints[0].oauth2.tokenUrl: required"},
			{"unknown key", withProperty(monitorWith("podMetricsEndpoints"), "targetLabels", []any{"team"}), notA + "monitoring.coreos.com/v1 PodMonitorSpec"},
			{"the object's spec", map[string]any{"spec": monitorWith("podMetricsEndpoints")}, notA},
			// A ServiceMonitor's endpoint reads a token from a file; a pod's does not.
			{"endpoint sub-key", monitorWith("podMetricsEndpoints", map[string]any{"bearerTokenFile": "/var/run/token"}), notA},
			{"port number a string", monitorWith("podMetricsEndpoints", map[string]any{"portNumber": "9090"}), notA},
			{"null endpoint", map[string]any{"selector": map[string]any{}, "podMetricsEndpoints": []any{map[string]any{"port": "web"}, nil}}, "podMetricsEndpoints[1]"},
			{"two spellings", withProperty(monitorWith("podMetricsEndpoints"), "Selector", map[string]any{}), "sets the same field as"},
		},
		"prometheus-probe": {
			{"no properties", nil, "prober.url: required"},
			{"null prober", map[string]any{"prober": nil}, "prober.url: required"},
			{"prober without a url", map[string]any{"prober": map[string]any{"scheme": "https"}}, "prober.url: required"},
			{"empty url", map[string]any{"prober": map[string]any{"url": ""}}, "prober.url: required"},
			{"targets alone", map[string]any{"targets": map[string]any{"staticConfig": map[string]any{"static": []any{"https://example.com"}}}}, "prober.url: required"},
			{"oauth2 without a client", withProperty(probeProperties(), "oauth2", monitoringOAuth2Without("clientId")), "oauth2.clientId: required"},
			{"oauth2 without a secret", withProperty(probeProperties(), "oauth2", monitoringOAuth2Without("clientSecret")), "oauth2.clientSecret: required"},
			{"oauth2 without a token URL", withProperty(probeProperties(), "oauth2", monitoringOAuth2Without("tokenUrl")), "oauth2.tokenUrl: required"},
			{"unknown key", withProperty(probeProperties(), "selector", map[string]any{}), notA + "monitoring.coreos.com/v1 ProbeSpec"},
			{"the object's spec", map[string]any{"spec": probeProperties()}, notA},
			{"prober sub-key", map[string]any{"prober": map[string]any{"url": "blackbox:9115", "address": "blackbox:9115"}}, notA},
			// A ServiceMonitor's endpoint takes its parameters as a map; a Probe takes a list.
			{"params a map", withProperty(probeProperties(), "params", map[string]any{"module": []any{"http_2xx"}}), notA},
			{"interval a number", withProperty(probeProperties(), "interval", 30), notA},
			{"null target", withProperty(probeProperties(), "targets", map[string]any{"staticConfig": map[string]any{"static": []any{"https://example.com", nil}}}), "targets.staticConfig.static[1]"},
			{"two spellings", withProperty(probeProperties(), "Prober", map[string]any{"url": "other:9115"}), "sets the same field as"},
		},
		"prometheusrule": {
			{"group without a name", ruleGroups(map[string]any{"interval": "1m"}), "groups[0].name: required"},
			{"a later group without a name", ruleGroups(map[string]any{"name": "a"}, map[string]any{"rules": []any{}}), "groups[1].name: required"},
			{"rule without an expression", ruleGroups(map[string]any{"name": "a", "rules": []any{map[string]any{"alert": "Down"}}}), "groups[0].rules[0].expr: required"},
			{"null expression", ruleGroups(map[string]any{"name": "a", "rules": []any{
				map[string]any{"record": "r", "expr": "up"}, map[string]any{"alert": "Down", "expr": nil},
			}}), "groups[0].rules[1].expr: required"},
			{"unknown key", map[string]any{"rules": []any{}}, notA + "monitoring.coreos.com/v1 PrometheusRuleSpec"},
			{"the object's spec", map[string]any{"spec": ruleGroups(map[string]any{"name": "a"})}, notA},
			{"group sub-key", ruleGroups(map[string]any{"name": "a", "queryOffset": "30s"}), notA},
			{"rule sub-key", ruleGroups(map[string]any{"name": "a", "rules": []any{map[string]any{"expr": "up", "severity": "page"}}}), notA},
			{"groups a map", map[string]any{"groups": map[string]any{"name": "a"}}, notA},
			{"expression a boolean", ruleGroups(map[string]any{"name": "a", "rules": []any{map[string]any{"expr": true}}}), notA},
			{"null group", map[string]any{"groups": []any{map[string]any{"name": "a"}, nil}}, "groups[1]"},
			{"two spellings", map[string]any{"groups": []any{}, "Groups": []any{}}, "sets the same field as"},
		},
		"issuer":        issuerCases,
		"clusterissuer": issuerCases,
		"certificate": {
			{"no properties", nil, "issuerRef: required"},
			{"no issuer", map[string]any{"secretName": "web-tls"}, "issuerRef: required"},
			{"null issuer", map[string]any{"secretName": "web-tls", "issuerRef": nil}, "issuerRef: required"},
			{"issuer without a name", map[string]any{"secretName": "web-tls", "issuerRef": map[string]any{"kind": "ClusterIssuer"}}, "issuerRef.name: required"},
			{"no secret name", map[string]any{"issuerRef": map[string]any{"name": "ca"}}, "secretName: required"},
			{"output format without a type", certificateWith("additionalOutputFormats", []any{map[string]any{"type": "DER"}, map[string]any{}}), "additionalOutputFormats[1].type: required"},
			{"keystore without create", certificateWith("keystores", map[string]any{"jks": map[string]any{"passwordSecretRef": map[string]any{"name": "keystore"}}}), "keystores.jks.create: required"},
			{"keystore reference without a name", certificateWith("keystores", map[string]any{"pkcs12": map[string]any{"create": true, "passwordSecretRef": map[string]any{"key": "password"}}}), "keystores.pkcs12.passwordSecretRef.name: required"},
			{"unknown key", certificateWith("issuer", "ca"), notA + "cert-manager.io/v1 CertificateSpec"},
			{"the object's spec", map[string]any{"spec": certificateMinimal()}, notA},
			{"private key sub-key", certificateWith("privateKey", map[string]any{"bits": 2048}), notA},
			{"dnsNames a string", certificateWith("dnsNames", "shop.example.com"), notA},
			{"duration a number", certificateWith("duration", 90), notA},
			{"duration not one", certificateWith("duration", "ninety days"), notA},
			{"revision limit a string", certificateWith("revisionHistoryLimit", "3"), notA},
			{"null dns name", certificateWith("dnsNames", []any{"shop.example.com", nil}), "dnsNames[1]"},
			{"two spellings", certificateWith("SecretName", "other-tls"), "sets the same field as"},
		},
		"cilium-bgpadvertisement": {
			{"no properties", nil, "advertisements: required"},
			{"null advertisements", map[string]any{"advertisements": nil}, "advertisements: required"},
			{"entry without a type", bgpAdvertisements(map[string]any{"attributes": map[string]any{"localPreference": 100}}), "advertisements[0].advertisementType: required"},
			{"a later entry without a type", bgpAdvertisements(map[string]any{"advertisementType": "PodCIDR"}, map[string]any{}), "advertisements[1].advertisementType: required"},
			{"expression without a key", bgpAdvertisements(map[string]any{"advertisementType": "CiliumPodIPPool", "selector": map[string]any{
				"matchExpressions": []any{map[string]any{"operator": "Exists"}},
			}}), "advertisements[0].selector.matchExpressions[0].key: required"},
			{"expression without an operator", bgpAdvertisements(map[string]any{"advertisementType": "CiliumPodIPPool", "selector": map[string]any{
				"matchExpressions": []any{map[string]any{"key": "pool", "operator": "Exists"}, map[string]any{"key": "pool"}},
			}}), "advertisements[0].selector.matchExpressions[1].operator: required"},
			// The CRD's five expression rules.
			{"Service without a service", bgpAdvertisements(map[string]any{"advertisementType": "Service"}), `advertisements[0].service: required with advertisementType "Service"`},
			{"Service with a null service", bgpAdvertisements(map[string]any{"advertisementType": "Service", "service": nil}), `advertisements[0].service: required with advertisementType "Service"`},
			{"service on a pool entry", bgpAdvertisements(map[string]any{"advertisementType": "PodCIDR"}, map[string]any{
				"advertisementType": "CiliumPodIPPool", "service": map[string]any{"addresses": []any{"ClusterIP"}},
			}), `advertisements[1].service: not allowed with advertisementType "CiliumPodIPPool", only with "Service"`},
			{"Interface without an interface", bgpAdvertisements(map[string]any{"advertisementType": "Interface"}), `advertisements[0].interface: required with advertisementType "Interface"`},
			{"interface on a Service entry", bgpAdvertisements(map[string]any{
				"advertisementType": "Service", "service": map[string]any{"addresses": []any{"ClusterIP"}}, "interface": map[string]any{"name": "lo"},
			}), `advertisements[0].interface: not allowed with advertisementType "Service", only with "Interface"`},
			{"selector on a PodCIDR entry", bgpAdvertisements(map[string]any{
				"advertisementType": "PodCIDR", "selector": map[string]any{"matchLabels": map[string]any{"pool": "blue"}},
			}), `advertisements[0].selector: not allowed with advertisementType "PodCIDR"`},
			{"an empty selector on a PodCIDR entry", bgpAdvertisements(map[string]any{"advertisementType": "PodCIDR", "selector": map[string]any{}}), `advertisements[0].selector: not allowed with advertisementType "PodCIDR"`},
			{"unknown key", withProperty(bgpAdvertisements(), "advertisement", []any{}), notA + "cilium.io/v2 CiliumBGPAdvertisementSpec"},
			{"the object's spec", map[string]any{"spec": bgpAdvertisements()}, notA},
			{"entry sub-key", bgpAdvertisements(map[string]any{"advertisementType": "PodCIDR", "type": "PodCIDR"}), notA},
			{"advertisements a map", map[string]any{"advertisements": map[string]any{"advertisementType": "PodCIDR"}}, notA},
			{"aggregation a string", bgpAdvertisements(map[string]any{"advertisementType": "Service", "service": map[string]any{"aggregationLengthIPv4": "24"}}), notA},
			{"null entry", map[string]any{"advertisements": []any{map[string]any{"advertisementType": "PodCIDR"}, nil}}, "advertisements[1]"},
			{"two spellings", withProperty(bgpAdvertisements(), "Advertisements", []any{}), "sets the same field as"},
		},
		"cilium-bgpclusterconfig": {
			{"no properties", nil, "bgpInstances: required"},
			{"null bgpInstances", map[string]any{"bgpInstances": nil}, "bgpInstances: required"},
			{"a selector alone", map[string]any{"nodeSelector": map[string]any{}}, "bgpInstances: required"},
			{"instance without a name", bgpInstances(map[string]any{"localASN": 65000}), "bgpInstances[0].name: required"},
			{"a later instance without a name", bgpInstances(map[string]any{"name": "a"}, map[string]any{"peers": []any{}}), "bgpInstances[1].name: required"},
			{"peer without a name", bgpPeers(map[string]any{"peerAddress": "192.0.2.1"}), "bgpInstances[0].peers[0].name: required"},
			{"discovery without a mode", bgpPeers(map[string]any{"name": "gateway", "autoDiscovery": map[string]any{
				"defaultGateway": map[string]any{"addressFamily": "ipv4"},
			}}), "bgpInstances[0].peers[0].autoDiscovery.mode: required"},
			{"default gateway without a family", bgpPeers(map[string]any{"name": "tor-1"}, map[string]any{"name": "gateway", "autoDiscovery": map[string]any{
				"mode": "DefaultGateway", "defaultGateway": map[string]any{},
			}}), "bgpInstances[0].peers[1].autoDiscovery.defaultGateway.addressFamily: required"},
			{"peer config reference without a name", bgpPeers(map[string]any{"name": "tor-1", "peerConfigRef": map[string]any{}}), "bgpInstances[0].peers[0].peerConfigRef.name: required"},
			{"node expression without a key", withProperty(bgpPeers(), "nodeSelector", map[string]any{
				"matchExpressions": []any{map[string]any{"operator": "Exists"}},
			}), "nodeSelector.matchExpressions[0].key: required"},
			{"node expression without an operator", withProperty(bgpPeers(), "nodeSelector", map[string]any{
				"matchExpressions": []any{map[string]any{"key": "rack"}},
			}), "nodeSelector.matchExpressions[0].operator: required"},
			{"unknown key", withProperty(bgpPeers(), "instances", []any{}), notA + "cilium.io/v2 CiliumBGPClusterConfigSpec"},
			{"the object's spec", map[string]any{"spec": bgpPeers()}, notA},
			{"instance sub-key", bgpInstances(map[string]any{"name": "a", "asn": 65000}), notA},
			{"peer sub-key", bgpPeers(map[string]any{"name": "tor-1", "address": "192.0.2.1"}), notA},
			{"localASN a string", bgpInstances(map[string]any{"name": "a", "localASN": "65000"}), notA},
			{"peer config reference a string", bgpPeers(map[string]any{"name": "tor-1", "peerConfigRef": "tor"}), notA},
			{"null instance", map[string]any{"bgpInstances": []any{map[string]any{"name": "a"}, nil}}, "bgpInstances[1]"},
			{"two spellings", withProperty(bgpPeers(), "BGPInstances", []any{}), "sets the same field as"},
		},
		"cilium-bgpnodeconfigoverride": {
			{"no properties", nil, "bgpInstances: required"},
			{"null bgpInstances", map[string]any{"bgpInstances": nil}, "bgpInstances: required"},
			{"instance without a name", bgpInstances(map[string]any{"routerID": "192.0.2.10"}), "bgpInstances[0].name: required"},
			{"peer without a name", bgpPeers(map[string]any{"name": "tor-1"}, map[string]any{"localAddress": "192.0.2.10"}), "bgpInstances[0].peers[1].name: required"},
			// A cluster config selects its nodes; an override has no selector.
			{"unknown key", withProperty(bgpPeers(), "nodeSelector", map[string]any{}), notA + "cilium.io/v2 CiliumBGPNodeConfigOverrideSpec"},
			{"the object's spec", map[string]any{"spec": bgpPeers()}, notA},
			{"instance sub-key", bgpInstances(map[string]any{"name": "a", "router": "192.0.2.10"}), notA},
			// A cluster config's peer has the peer's address; an override's has the local one.
			{"peer sub-key", bgpPeers(map[string]any{"name": "tor-1", "peerAddress": "192.0.2.1"}), notA},
			{"localPort a string", bgpInstances(map[string]any{"name": "a", "localPort": "179"}), notA},
			{"null peer", bgpPeers(map[string]any{"name": "tor-1"}, nil), "bgpInstances[0].peers[1]"},
			{"two spellings", withProperty(bgpPeers(), "BGPInstances", []any{}), "sets the same field as"},
		},
		"cilium-bgppeerconfig": {
			{"family without an afi", map[string]any{"families": []any{map[string]any{"safi": "unicast"}}}, "families[0].afi: required"},
			{"a later family without a safi", map[string]any{"families": []any{
				map[string]any{"afi": "ipv4", "safi": "unicast"}, map[string]any{"afi": "ipv6"},
			}}, "families[1].safi: required"},
			{"family expression without a key", map[string]any{"families": []any{map[string]any{"afi": "ipv4", "safi": "unicast", "advertisements": map[string]any{
				"matchExpressions": []any{map[string]any{"operator": "Exists"}},
			}}}}, "families[0].advertisements.matchExpressions[0].key: required"},
			{"family expression without an operator", map[string]any{"families": []any{map[string]any{"afi": "ipv4", "safi": "unicast", "advertisements": map[string]any{
				"matchExpressions": []any{map[string]any{"key": "advertise"}},
			}}}}, "families[0].advertisements.matchExpressions[0].operator: required"},
			{"graceful restart without enabled", map[string]any{"gracefulRestart": map[string]any{"restartTimeSeconds": 60}}, "gracefulRestart.enabled: required"},
			{"an empty graceful restart", map[string]any{"gracefulRestart": map[string]any{}}, "gracefulRestart.enabled: required"},
			{"null enabled", map[string]any{"gracefulRestart": map[string]any{"enabled": nil}}, "gracefulRestart.enabled: required"},
			// The CRD's expression rule, with both of its fields authored.
			{"keepalive over hold", map[string]any{"timers": map[string]any{"keepAliveTimeSeconds": 90, "holdTimeSeconds": 30}}, "timers.keepAliveTimeSeconds: 90 is larger than timers.holdTimeSeconds (30)"},
			{"unknown key", map[string]any{"peerPort": 179}, notA + "cilium.io/v2 CiliumBGPPeerConfigSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"ebgpMultihop": 2}}, notA},
			{"timers sub-key", map[string]any{"timers": map[string]any{"holdTime": 90}}, notA},
			{"ebgpMultihop a string", map[string]any{"ebgpMultihop": "2"}, notA},
			// The reference is a Secret's name, not an object.
			{"authSecretRef a map", map[string]any{"authSecretRef": map[string]any{"name": "bgp-auth"}}, notA},
			{"null family", map[string]any{"families": []any{map[string]any{"afi": "ipv4", "safi": "unicast"}, nil}}, "families[1]"},
			{"two spellings", map[string]any{"ebgpMultihop": 2, "EBGPMultihop": 3}, "sets the same field as"},
		},
	}
	for _, kind := range policyFreeKinds {
		if len(cases[kind.component]) == 0 {
			t.Errorf("%s has no refusal cases", kind.component)
		}
		for _, tc := range cases[kind.component] {
			t.Run(kind.component+"/"+tc.name, func(t *testing.T) {
				err := coreKindErr(kind.handler, kind.component, "fast", tc.props)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
				}
			})
		}
	}
}

// TestPolicyFreeKinds_AuthoredValuesArriveTyped reads a few authored values
// back from the typed object: an authored zero and false are kept, a quantity
// written as a number takes its canonical form, and lists keep their order.
func TestPolicyFreeKinds_AuthoredValuesArriveTyped(t *testing.T) {
	full := map[string]map[string]any{}
	kinds := map[string]policyFreeKind{}
	for _, kind := range policyFreeKinds {
		full[kind.component], kinds[kind.component] = kind.full, kind
	}
	build := func(component string, props map[string]any) any {
		return kinds[component].generate(t, "fast", props)
	}

	sc := build("storageclass", full["storageclass"]).(*storagev1.StorageClass)
	if sc.AllowVolumeExpansion == nil || *sc.AllowVolumeExpansion {
		t.Errorf("allowVolumeExpansion = %v, want the authored false", sc.AllowVolumeExpansion)
	}
	if !slices.Equal(sc.MountOptions, []string{"noatime", "discard"}) {
		t.Errorf("mountOptions = %v, want them in authored order", sc.MountOptions)
	}
	if sc.ReclaimPolicy == nil || *sc.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		t.Errorf("reclaimPolicy = %v, want Retain", sc.ReclaimPolicy)
	}

	pc := build("priorityclass", map[string]any{"value": 0, "globalDefault": false}).(*schedulingv1.PriorityClass)
	if pc.Value != 0 || pc.GlobalDefault {
		t.Errorf("value = %d, globalDefault = %v; want the authored 0 and false", pc.Value, pc.GlobalDefault)
	}
	if negative := build("priorityclass", map[string]any{"value": -10}).(*schedulingv1.PriorityClass); negative.Value != -10 {
		t.Errorf("value = %d, want the authored -10", negative.Value)
	}
	// The API requires no value. The type always encodes one, so a class that
	// authors none carries value: 0, which the API reads an absent one as.
	unvalued := policyFreeJSON(t, build("priorityclass", map[string]any{"description": "x"}))
	if got, ok := unvalued["value"]; !ok || fmt.Sprint(got) != "0" {
		t.Errorf("value = %v (present: %v), want 0 on a class that authors none", got, ok)
	}

	rc := build("runtimeclass", full["runtimeclass"]).(*nodev1.RuntimeClass)
	if rc.Overhead == nil || !rc.Overhead.PodFixed.Memory().Equal(resource.MustParse("128Mi")) || rc.Overhead.PodFixed.Memory().String() != "134217728" {
		t.Errorf("overhead = %+v, want podFixed.memory 134217728", rc.Overhead)
	}

	ic := build("ingressclass", full["ingressclass"]).(*networkingv1.IngressClass)
	if p := ic.Spec.Parameters; p == nil || p.Kind != "IngressParameters" || p.Scope == nil || *p.Scope != "Namespace" || p.Namespace == nil || *p.Namespace != "ingress" {
		t.Errorf("parameters = %+v, want the authored reference", p)
	}

	driver := build("csidriver", full["csidriver"]).(*storagev1.CSIDriver)
	if driver.Spec.AttachRequired == nil || *driver.Spec.AttachRequired {
		t.Errorf("attachRequired = %v, want the authored false", driver.Spec.AttachRequired)
	}
	wantTokens := []storagev1.TokenRequest{{Audience: "vault", ExpirationSeconds: new(int64(3600))}, {Audience: ""}}
	if !reflect.DeepEqual(driver.Spec.TokenRequests, wantTokens) {
		t.Errorf("tokenRequests = %+v, want %+v", driver.Spec.TokenRequests, wantTokens)
	}

	cidr := build("servicecidr", full["servicecidr"]).(*networkingv1.ServiceCIDR)
	if !slices.Equal(cidr.Spec.CIDRs, []string{"10.96.0.0/16", "fd00:10:96::/112"}) {
		t.Errorf("cidrs = %v, want them in authored order", cidr.Spec.CIDRs)
	}

	// A count stays a number and a percentage a string; an authored 0 is kept.
	pdb := build("poddisruptionbudget", full["poddisruptionbudget"]).(*policyv1.PodDisruptionBudget)
	if got := pdb.Spec.MinAvailable; got == nil || *got != intstr.FromInt32(0) {
		t.Errorf("minAvailable = %v, want the authored count 0", got)
	}
	if got := pdb.Spec.MaxUnavailable; got == nil || *got != intstr.FromString("25%") {
		t.Errorf("maxUnavailable = %v, want the authored 25%%", got)
	}
	if got := pdb.Spec.UnhealthyPodEvictionPolicy; got == nil || *got != policyv1.AlwaysAllow {
		t.Errorf("unhealthyPodEvictionPolicy = %v, want AlwaysAllow", got)
	}
	// An authored empty selector selects every pod of the namespace, an
	// unauthored one none: the two stay apart.
	every := build("poddisruptionbudget", map[string]any{"selector": map[string]any{}}).(*policyv1.PodDisruptionBudget)
	if every.Spec.Selector == nil {
		t.Error("selector = nil, want the authored empty selector")
	}
	if none := build("poddisruptionbudget", map[string]any{}).(*policyv1.PodDisruptionBudget); none.Spec.Selector != nil {
		t.Errorf("selector = %+v, want none on a budget that authors none", none.Spec.Selector)
	}

	// The limits are pointers: an authored 0 (no limit) stays apart from an
	// unset one. A bucket factor written as a number takes its canonical form.
	sm := build("servicemonitor", full["servicemonitor"]).(*monitoringv1.ServiceMonitor)
	if got := sm.Spec.SampleLimit; got == nil || *got != 0 {
		t.Errorf("sampleLimit = %v, want the authored 0", got)
	}
	if got := sm.Spec.KeepDroppedTargets; got == nil || *got != 0 {
		t.Errorf("keepDroppedTargets = %v, want the authored 0", got)
	}
	if got := sm.Spec.ScrapeNativeHistograms; got == nil || *got {
		t.Errorf("scrapeNativeHistograms = %v, want the authored false", got)
	}
	if got := sm.Spec.NativeHistogramMinBucketFactor; got == nil || got.String() != "1100m" {
		t.Errorf("nativeHistogramMinBucketFactor = %v, want 1100m", got)
	}
	if len(sm.Spec.Endpoints) != 2 || sm.Spec.Endpoints[0].Port != "metrics" || sm.Spec.Endpoints[1].TargetPort == nil || *sm.Spec.Endpoints[1].TargetPort != intstr.FromInt32(9090) {
		t.Errorf("endpoints = %+v, want the two authored ones in order, the second by target port 9090", sm.Spec.Endpoints)
	}
	if first := sm.Spec.Endpoints[0]; first.HonorTimestamps == nil || *first.HonorTimestamps || first.HonorLabels {
		t.Errorf("honorTimestamps = %v, honorLabels = %v; want the authored false of each", first.HonorTimestamps, first.HonorLabels)
	}
	if got := sm.Spec.NamespaceSelector.MatchNames; !slices.Equal(got, []string{"payments", "billing"}) {
		t.Errorf("namespaceSelector.matchNames = %v, want them in authored order", got)
	}
	// The API requires the list and the selector; authored empty they are in
	// the object as written. No namespace selector was authored, and the type
	// writes an empty one, which selects the object's own namespace.
	least := policyFreeJSON(t, build("servicemonitor", monitorWith("endpoints")))["spec"].(map[string]any)
	if got, want := fmt.Sprint(least), "map[endpoints:[] namespaceSelector:map[] selector:map[]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}

	pm := build("podmonitor", full["podmonitor"]).(*monitoringv1.PodMonitor)
	if got := pm.Spec.PodMetricsEndpoints; len(got) != 2 || got[0].Port == nil || *got[0].Port != "metrics" || got[1].PortNumber == nil || *got[1].PortNumber != 9090 {
		t.Errorf("podMetricsEndpoints = %+v, want the two authored ones in order", got)
	}
	if got := pm.Spec.PodMetricsEndpoints[0].FilterRunning; got == nil || *got {
		t.Errorf("filterRunning = %v, want the authored false", got)
	}
	if !pm.Spec.NamespaceSelector.Any {
		t.Error("namespaceSelector.any = false, want the authored true")
	}
	// The API does not require a pod monitor's endpoints. The type always
	// writes the list, so a monitor that authors none carries a null one.
	bare := policyFreeJSON(t, build("podmonitor", map[string]any{"selector": map[string]any{}}))["spec"].(map[string]any)
	if got, ok := bare["podMetricsEndpoints"]; !ok || got != nil {
		t.Errorf("podMetricsEndpoints = %v (present: %v), want null on a monitor that authors none", got, ok)
	}

	probe := build("prometheus-probe", full["prometheus-probe"]).(*monitoringv1.Probe)
	if probe.Spec.ProberSpec.URL != "blackbox-exporter.monitoring.svc:9115" || probe.Spec.ProberSpec.Path != "/probe" {
		t.Errorf("prober = %+v, want the authored url and path", probe.Spec.ProberSpec)
	}
	if static := probe.Spec.Targets.StaticConfig; static == nil || !slices.Equal(static.Targets, []string{"https://example.com", "https://example.org"}) {
		t.Errorf("targets.staticConfig = %+v, want the two authored targets in order", static)
	}
	if got := probe.Spec.FollowRedirects; got == nil || *got {
		t.Errorf("followRedirects = %v, want the authored false", got)
	}
	// The probe's own authorization is the one the object holds; the one its
	// HTTP settings embed is not reachable and stays unset.
	if got := probe.Spec.Authorization; got == nil || got.Type != "Bearer" || got.Credentials == nil || got.Credentials.Key != "token" {
		t.Errorf("authorization = %+v, want the authored one", got)
	}
	// A probe always holds a prober and targets; with only the url authored
	// the targets are empty, which the operator reads as no target.
	lone := policyFreeJSON(t, build("prometheus-probe", probeProperties()))["spec"].(map[string]any)
	if got, want := fmt.Sprint(lone), "map[prober:map[url:blackbox-exporter.monitoring.svc:9115] targets:map[]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}

	rule := build("prometheusrule", full["prometheusrule"]).(*monitoringv1.PrometheusRule)
	if len(rule.Spec.Groups) != 2 || rule.Spec.Groups[0].Name != "availability" || rule.Spec.Groups[1].Name != "empty" {
		t.Fatalf("groups = %+v, want the two authored ones in order", rule.Spec.Groups)
	}
	group := rule.Spec.Groups[0]
	if got := group.Limit; got == nil || *got != 0 {
		t.Errorf("limit = %v, want the authored 0", got)
	}
	if len(group.Rules) != 3 || group.Rules[0].Record != "job:up:sum" || group.Rules[1].Alert != "TargetDown" {
		t.Fatalf("rules = %+v, want the three authored ones in order", group.Rules)
	}
	// An expression is a string or a number, and stays what was authored.
	if got := group.Rules[1].Expr; got != intstr.FromString("job:up:sum == 0") {
		t.Errorf("expr = %v, want the authored string", got)
	}
	if got := group.Rules[2].Expr; got != intstr.FromInt32(0) {
		t.Errorf("expr = %v, want the authored number 0", got)
	}
	// No group is required: a rule object may hold none.
	if none := build("prometheusrule", map[string]any{}).(*monitoringv1.PrometheusRule); none.Spec.Groups != nil {
		t.Errorf("groups = %+v, want none on a rule that authors none", none.Spec.Groups)
	}

	advert := build("cilium-bgpadvertisement", full["cilium-bgpadvertisement"]).(*ciliumv2.CiliumBGPAdvertisement)
	var types []ciliumv2.BGPAdvertisementType
	for _, entry := range advert.Spec.Advertisements {
		types = append(types, entry.AdvertisementType)
	}
	if want := []ciliumv2.BGPAdvertisementType{
		ciliumv2.BGPPodCIDRAdvert, ciliumv2.BGPCiliumPodIPPoolAdvert, ciliumv2.BGPServiceAdvert, ciliumv2.BGPInterfaceAdvert,
	}; !slices.Equal(types, want) {
		t.Fatalf("advertisement types = %v, want %v in authored order", types, want)
	}
	service := advert.Spec.Advertisements[2].Service
	if service == nil || service.AggregationLengthIPv4 == nil || *service.AggregationLengthIPv4 != 0 {
		t.Errorf("service = %+v, want the authored aggregationLengthIPv4 0", service)
	}
	if want := []ciliumv2.BGPServiceAddressType{ciliumv2.BGPLoadBalancerIPAddr, ciliumv2.BGPClusterIPAddr}; service == nil || !slices.Equal(service.Addresses, want) {
		t.Errorf("service = %+v, want the addresses %v in authored order", service, want)
	}
	// The API requires a Service entry's addresses and an Interface entry's
	// name. The type omits each when it is not authored, so the kind refuses
	// neither: the object shows the block without it, and the API server
	// refuses that.
	blocks := policyFreeJSON(t, build("cilium-bgpadvertisement", bgpAdvertisements(
		map[string]any{"advertisementType": "Service", "service": map[string]any{}},
		map[string]any{"advertisementType": "Interface", "interface": map[string]any{}},
	)))["spec"].(map[string]any)
	if got, want := fmt.Sprint(blocks), "map[advertisements:[map[advertisementType:Service service:map[]] map[advertisementType:Interface interface:map[]]]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}

	cluster := build("cilium-bgpclusterconfig", full["cilium-bgpclusterconfig"]).(*ciliumv2.CiliumBGPClusterConfig)
	if len(cluster.Spec.BGPInstances) != 2 || len(cluster.Spec.BGPInstances[0].Peers) != 2 {
		t.Fatalf("bgpInstances = %+v, want the two authored ones, the first with its two peers", cluster.Spec.BGPInstances)
	}
	// An authored peerASN 0 accepts any ASN and is kept; so is an unauthored
	// one, which the API fills with that 0.
	peers := cluster.Spec.BGPInstances[0].Peers
	if got := peers[0].PeerASN; got == nil || *got != 0 {
		t.Errorf("peerASN = %v, want the authored 0", got)
	}
	if unset := build("cilium-bgpclusterconfig", bgpPeers(map[string]any{"name": "tor-1"})).(*ciliumv2.CiliumBGPClusterConfig); unset.Spec.BGPInstances[0].Peers[0].PeerASN != nil {
		t.Errorf("peerASN = %v, want none on a peer that authors none", unset.Spec.BGPInstances[0].Peers[0].PeerASN)
	}
	if got := peers[1].AutoDiscovery; got == nil || got.Mode != ciliumv2.BGPDefaultGatewayMode || got.DefaultGateway == nil || got.DefaultGateway.AddressFamily != "ipv4" {
		t.Errorf("autoDiscovery = %+v, want the authored default gateway discovery over ipv4", got)
	}
	// An authored empty node selector is every node, as an unauthored one is;
	// the object says which was written.
	if every := build("cilium-bgpclusterconfig", withProperty(bgpPeers(), "nodeSelector", map[string]any{})).(*ciliumv2.CiliumBGPClusterConfig); every.Spec.NodeSelector == nil {
		t.Error("nodeSelector = nil, want the authored empty selector")
	}

	override := build("cilium-bgpnodeconfigoverride", full["cilium-bgpnodeconfigoverride"]).(*ciliumv2.CiliumBGPNodeConfigOverride)
	instance := override.Spec.BGPInstances[0]
	if instance.RouterID == nil || *instance.RouterID != "192.0.2.10" || len(instance.Peers) != 2 || instance.Peers[0].Name != "tor-1" || instance.Peers[1].Name != "gateway" {
		t.Errorf("instance = %+v, want the authored router ID and the two peers in order", instance)
	}

	peer := build("cilium-bgppeerconfig", full["cilium-bgppeerconfig"]).(*ciliumv2.CiliumBGPPeerConfig)
	if got := peer.Spec.GracefulRestart; got == nil || got.Enabled || got.RestartTimeSeconds == nil || *got.RestartTimeSeconds != 60 {
		t.Errorf("gracefulRestart = %+v, want the authored enabled false and 60 seconds", got)
	}
	if got := peer.Spec.AuthSecretRef; got == nil || *got != "bgp-auth" {
		t.Errorf("authSecretRef = %v, want the authored Secret name", got)
	}
	// A peer config that authors nothing carries nothing: every default is the
	// API's to fill.
	if none := build("cilium-bgppeerconfig", map[string]any{}).(*ciliumv2.CiliumBGPPeerConfig); !reflect.DeepEqual(none.Spec, ciliumv2.CiliumBGPPeerConfigSpec{}) {
		t.Errorf("spec = %+v, want none of it set on a peer config that authors nothing", none.Spec)
	}
	// The timers rule is checked with both fields authored, and equal times
	// pass it. With one authored the other is the default the installed CRD
	// fills, and the comparison is the API server's: 100 alone is over the
	// default hold time of the linked CRD and is not refused here.
	for name, timers := range map[string]map[string]any{
		"equal":           {"keepAliveTimeSeconds": 30, "holdTimeSeconds": 30},
		"keepalive alone": {"keepAliveTimeSeconds": 100},
		"hold alone":      {"holdTimeSeconds": 3},
	} {
		if err := coreKindErr(kinds["cilium-bgppeerconfig"].handler, "cilium-bgppeerconfig", "fast", map[string]any{"timers": timers}); err != nil {
			t.Errorf("timers %s: %v, want it accepted", name, err)
		}
	}
}

// TestPolicyFreeKinds_ThroughTheTransform: under the strictest policy the
// tests have, under the transform's default one and under none passed, each
// kind builds its one object, in the build namespace or cluster-scoped, and
// the transform sets the component label on it and nothing else.
func TestPolicyFreeKinds_ThroughTheTransform(t *testing.T) {
	for _, kind := range policyFreeKinds {
		for name, policy := range map[string]oam.Policy{"strict policy": ptStrictPolicy(), "no policy passed": nil} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				objs, err := pvTransform(kind.component, kind.handler, kind.full, policy)
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 {
					t.Fatalf("generated %d objects, want one", len(objs))
				}
				obj := objs[0]
				if got := obj.GetObjectKind().GroupVersionKind(); got != kind.gvk {
					t.Errorf("GVK = %s, want %s", got, kind.gvk)
				}
				if want := kind.namespace("demo"); obj.GetName() != "web" || obj.GetNamespace() != want {
					t.Errorf("identity = %q/%q, want %q/web", obj.GetNamespace(), obj.GetName(), want)
				}
				wantLabels := map[string]string{oam.ComponentLabelKeyForDomain(""): "web"}
				if !reflect.DeepEqual(obj.GetLabels(), wantLabels) || len(obj.GetAnnotations()) != 0 {
					t.Errorf("labels = %v, annotations = %v; want labels %v and no annotation", obj.GetLabels(), obj.GetAnnotations(), wantLabels)
				}
			})
		}
	}
}

// TestPolicyFreeKinds_DeclareTheirObject: each handler declares the object it
// emits, by group and kind, with its scope, so the type takes `objectName`
// and the engine claims the name in the object's namespace, or in none for a
// cluster-scoped one.
func TestPolicyFreeKinds_DeclareTheirObject(t *testing.T) {
	for _, kind := range policyFreeKinds {
		t.Run(kind.component, func(t *testing.T) {
			provider, declares := kind.handler.(oam.ComponentObjectProvider)
			if !declares {
				t.Fatalf("%T declares no object (oam.ComponentObjectProvider)", kind.handler)
			}
			got, scope := provider.ComponentObject()
			if got != kind.gvk.GroupKind() || scope != kind.scope() {
				t.Errorf("ComponentObject() = %s, scope %d; want %s, scope %d",
					got, scope, kind.gvk.GroupKind(), kind.scope())
			}
		})
	}
}

// policyFreeTransform transforms a document of the given components, all of
// one kind, with that kind's handler alone registered, and returns every
// generated object. naming is the transform's Naming hook, nil for none.
func policyFreeTransform(typ string, h oam.ComponentHandler, naming func(oam.NameRequest) (string, bool), components ...oam.Component) ([]client.Object, error) {
	for i := range components {
		components[i].Type = typ
	}
	app := &oam.Application{Metadata: oam.Metadata{Name: "shop"}, Spec: oam.ApplicationSpec{Components: components}}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{typ: h}, nil)
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Naming: naming})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// TestPolicyFreeKinds_ObjectName: through the transform, the author's
// `objectName` names the object, and without one the Naming hook's answer for
// role "object" does; the author's wins over the hook's. It names the object
// and nothing else: the object keeps its namespace (the build's, or none on a
// cluster-scoped one), its component label keeps the component's name, and but
// for its name it is the object the same properties build under the component
// name. The hook is asked for the declared kind.
func TestPolicyFreeKinds_ObjectName(t *testing.T) {
	const component, authored = "web", "renamed.example.com"
	for _, kind := range policyFreeKinds {
		wantKind := kind.gvk.GroupKind().String()
		var asked []oam.NameRequest
		hook := func(req oam.NameRequest) (string, bool) {
			if req.Role != oam.NameRoleObject {
				return "", false
			}
			asked = append(asked, req)
			return "hooked-" + req.Default, true
		}
		build := func(t *testing.T, objectName string, naming func(oam.NameRequest) (string, bool)) client.Object {
			t.Helper()
			props := map[string]any{}
			maps.Copy(props, kind.full)
			if objectName != "" {
				props[oam.ObjectNameProperty] = objectName
			}
			objs, err := policyFreeTransform(kind.component, kind.handler, naming, oam.Component{Name: component, Properties: props})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(objs) != 1 {
				t.Fatalf("generated %d objects, want one", len(objs))
			}
			return objs[0]
		}
		for name, tc := range map[string]struct {
			objectName string
			naming     func(oam.NameRequest) (string, bool)
			want       string
		}{
			"objectName":               {authored, nil, authored},
			"the Naming hook":          {"", hook, "hooked-" + component},
			"objectName over the hook": {authored, hook, authored},
		} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				plain := build(t, "", nil)
				if plain.GetName() != component {
					t.Fatalf("without objectName the object is named %q, want the component's %q", plain.GetName(), component)
				}
				asked = nil
				obj := build(t, tc.objectName, tc.naming)
				if want := kind.namespace("demo"); obj.GetName() != tc.want || obj.GetNamespace() != want {
					t.Errorf("identity = %q/%q, want %q/%q", obj.GetNamespace(), obj.GetName(), want, tc.want)
				}
				wantLabels := map[string]string{oam.ComponentLabelKeyForDomain(""): component}
				if !reflect.DeepEqual(obj.GetLabels(), wantLabels) {
					t.Errorf("labels = %v, want %v: the component label keeps the component's name", obj.GetLabels(), wantLabels)
				}
				obj.SetName(plain.GetName())
				if got, want := policyFreeJSON(t, obj), policyFreeJSON(t, plain); !reflect.DeepEqual(got, want) {
					t.Errorf("but for its name the object differs from the one built under the component name:\n got %v\nwant %v", got, want)
				}
				switch {
				case tc.naming == nil || tc.objectName != "":
					if len(asked) != 0 {
						t.Errorf("the hook was asked %+v, want it not asked for an authored name", asked)
					}
				case len(asked) == 0:
					t.Error("the hook was not asked for the object's name")
				default:
					for _, req := range asked {
						if req.Kind != wantKind || req.Component != component || req.Default != component {
							t.Errorf("the hook was asked %+v, want kind %q, component and default %q", req, wantKind, component)
						}
					}
				}
			})
		}
	}
}

// TestPolicyFreeKinds_ObjectNameIsClaimedInItsScope: two components of one
// kind given one object name are refused, and the refusal names the object by
// its namespace and name, or by its name alone for a cluster-scoped object.
func TestPolicyFreeKinds_ObjectNameIsClaimedInItsScope(t *testing.T) {
	for _, kind := range policyFreeKinds {
		t.Run(kind.component, func(t *testing.T) {
			named := func(name string) oam.Component {
				props := map[string]any{oam.ObjectNameProperty: "shared"}
				maps.Copy(props, kind.minimal)
				return oam.Component{Name: name, Properties: props}
			}
			_, err := policyFreeTransform(kind.component, kind.handler, nil, named("a"), named("b"))
			claimed := "shared"
			if kind.namespaced {
				claimed = "demo/shared"
			}
			want := fmt.Sprintf("name collision: %s %q is named by component %q", kind.gvk.GroupKind(), claimed, "a")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %q", err, want)
			}
		})
	}
}
