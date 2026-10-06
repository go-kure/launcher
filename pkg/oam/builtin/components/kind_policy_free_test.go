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

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of go-kure/launcher#790 to which no dimension of the
// environment policy applies, built on one shared helper (policyFreeKind): the
// cluster-scoped classes, the CSIDriver, the ServiceCIDR, the
// PodDisruptionBudget, the four kinds of the Prometheus operator's API, the
// four of Cilium's BGP control plane, five more of Cilium's API (a CIDR
// group, a load balancer IP pool, an egress gateway policy, a local redirect
// policy and a node configuration), the five kinds of the Gateway API's
// infrastructure objects, and the EndpointSlice, the first of them that is a
// whole object in a namespace. The three kinds of cert-manager's API and the
// four of the External Secrets Operator's are held here too: the policy
// reaches one part of each (held), and everything else of them is the helper's.
// So are the kinds of the Flux APIs beside the sources, the HelmRelease and the
// Kustomization (flux): what they add to the helper, the Flux namespace, has its
// own tests (kind_flux_test.go).

// policyFreeKind is one of them. typ is the type the properties decode into:
// the object itself for a kind with no spec type (wholeObject), its spec type
// otherwise. namespaced says the object lands in the build namespace; the
// others are cluster-scoped. flux says a namespaced object moves to the Flux
// namespace when one is set, which none is here. held says the environment
// policy reaches the
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
	flux        bool
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
	if k.flux {
		return oam.ObjectScopeFlux
	}
	if k.namespaced {
		return oam.ObjectScopeNamespaced
	}
	return oam.ObjectScopeCluster
}

// policyFreeKinds lists them, in the order of their component type: a new
// kind's row goes at its position (TestKindLists_InOrder, pkg/cmd/kurel).
var policyFreeKinds = []policyFreeKind{
	{
		component: "artifactgenerator", handler: &components.ArtifactGeneratorHandler{},
		gvk: swv1beta1.GroupVersion.WithKind(swv1beta1.ArtifactGeneratorKind),
		typ: reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](), namespaced: true, flux: true,
		minimal: artifactGeneratorMinimal(),
		full:    artifactGeneratorFull(),
	},
	{
		component: "backendtlspolicy", handler: &components.BackendTLSPolicyHandler{},
		gvk: gatewayGVK("BackendTLSPolicy"),
		typ: reflect.TypeFor[gatewayv1.BackendTLSPolicySpec](), namespaced: true,
		minimal: backendTLSPolicyMinimal(),
		full:    backendTLSPolicyFull(),
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
	{
		component: "cilium-cidrgroup", handler: &components.CiliumCIDRGroupHandler{},
		gvk: ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.CCGKindDefinition),
		typ: reflect.TypeFor[ciliumv2.CiliumCIDRGroupSpec](),
		// The API requires the list; an empty one is an authored value.
		minimal: map[string]any{"externalCIDRs": []any{}},
		full:    map[string]any{"externalCIDRs": []any{"192.0.2.0/24", "2001:db8::/32"}},
	},
	{
		component: "cilium-egressgatewaypolicy", handler: &components.CiliumEgressGatewayPolicyHandler{},
		gvk:     ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.CEGPKindDefinition),
		typ:     reflect.TypeFor[ciliumv2.CiliumEgressGatewayPolicySpec](),
		minimal: egressGatewayPolicy(),
		full: map[string]any{
			"selectors": []any{
				map[string]any{
					"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "payments"}},
					"podSelector":       map[string]any{"matchLabels": map[string]any{"app": "web"}},
				},
				map[string]any{"nodeSelector": map[string]any{"matchExpressions": []any{
					map[string]any{"key": "rack", "operator": "In", "values": []any{"a", "b"}},
				}}},
			},
			"destinationCIDRs": []any{"192.0.2.0/24", "2001:db8::/32"},
			"excludedCIDRs":    []any{"192.0.2.128/25"},
			"egressGateway": map[string]any{
				"nodeSelector": map[string]any{"matchLabels": map[string]any{"egress": "true"}}, "interface": "eth1",
			},
			"egressGateways": []any{
				map[string]any{"nodeSelector": map[string]any{"matchLabels": map[string]any{"egress": "a"}}, "egressIP": "198.51.100.7"},
				map[string]any{"nodeSelector": map[string]any{}},
			},
		},
	},
	{
		component: "cilium-loadbalancerippool", handler: &components.CiliumLoadBalancerIPPoolHandler{},
		gvk:     ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.PoolKindDefinition),
		typ:     reflect.TypeFor[ciliumv2.CiliumLoadBalancerIPPoolSpec](),
		minimal: map[string]any{},
		full: map[string]any{
			"serviceSelector": map[string]any{
				"matchLabels":      map[string]any{"pool": "blue"},
				"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"frontend", "edge"}}},
			},
			"allowFirstLastIPs": "No",
			"blocks": []any{
				map[string]any{"cidr": "192.0.2.0/24"},
				map[string]any{"start": "198.51.100.10", "stop": "198.51.100.20"},
			},
			// The type omits a false, which is also the API's default.
			"disabled": true,
		},
	},
	{
		component: "cilium-localredirectpolicy", handler: &components.CiliumLocalRedirectPolicyHandler{},
		gvk: ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.CLRPKindDefinition),
		typ: reflect.TypeFor[ciliumv2.CiliumLocalRedirectPolicySpec](), namespaced: true,
		minimal: redirectPolicy(redirectAddress(), redirectBackend()),
		full: map[string]any{
			"redirectFrontend": map[string]any{"addressMatcher": map[string]any{
				"ip": "169.254.169.254",
				"toPorts": []any{
					map[string]any{"port": "80", "protocol": "TCP", "name": "http"},
					map[string]any{"port": "443", "protocol": "TCP", "name": "https"},
				},
			}},
			"redirectBackend": map[string]any{
				"localEndpointSelector": map[string]any{
					"matchLabels":      map[string]any{"app": "metadata-proxy"},
					"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"node", "edge"}}},
				},
				"toPorts": []any{
					map[string]any{"port": "8080", "protocol": "TCP", "name": "http"},
					map[string]any{"port": "8443", "protocol": "TCP", "name": "https"},
				},
			},
			// The type omits a false, which is also the API's default.
			"skipRedirectFromBackend": true,
			"description":             "Metadata requests go to the proxy of the node.",
		},
	},
	{
		component: "cilium-nodeconfig", handler: &components.CiliumNodeConfigHandler{},
		gvk: ciliumv2.SchemeGroupVersion.WithKind(ciliumv2.CNCKindDefinition),
		typ: reflect.TypeFor[ciliumv2.CiliumNodeConfigSpec](), namespaced: true,
		// The API requires both; an empty map and an empty selector are
		// authored values.
		minimal: map[string]any{"defaults": map[string]any{}, "nodeSelector": map[string]any{}},
		full: map[string]any{
			"defaults": map[string]any{"bpf-map-dynamic-size-ratio": "0.005", "enable-hubble": "false"},
			"nodeSelector": map[string]any{
				"matchLabels":      map[string]any{"node-role": "edge"},
				"matchExpressions": []any{map[string]any{"key": "rack", "operator": "In", "values": []any{"a", "b"}}},
			},
		},
	},
	{
		component: "clusterexternalsecret", handler: &components.ClusterExternalSecretHandler{},
		gvk:     esv1.SchemeGroupVersion.WithKind(esv1.ClusterExtSecretKind),
		typ:     reflect.TypeFor[esv1.ClusterExternalSecretSpec](),
		held:    true,
		minimal: clusterExternalSecretOf(map[string]any{}),
		full:    clusterExternalSecretFull(),
	},
	{
		component: "clusterissuer", handler: &components.ClusterIssuerHandler{},
		gvk: certv1.SchemeGroupVersion.WithKind("ClusterIssuer"),
		typ: reflect.TypeFor[certv1.IssuerSpec](), held: true,
		minimal: map[string]any{},
		full:    issuerFull(),
	},
	// The four kinds of the RBAC API are whole objects too. The API requires
	// nothing of a role, and of a binding the role it grants.
	{
		component: "clusterrole", handler: &components.ClusterRoleHandler{},
		gvk: rbacv1.SchemeGroupVersion.WithKind("ClusterRole"),
		typ: reflect.TypeFor[rbacv1.ClusterRole](), wholeObject: true,
		minimal: map[string]any{},
		full: map[string]any{
			"rules": []any{
				map[string]any{"apiGroups": []any{""}, "resources": []any{"nodes"}, "resourceNames": []any{"node-a"}, "verbs": []any{"get"}},
				map[string]any{"nonResourceURLs": []any{"/healthz", "/metrics"}, "verbs": []any{"get"}},
			},
			"aggregationRule": map[string]any{"clusterRoleSelectors": []any{map[string]any{
				"matchLabels":      map[string]any{"rbac.example.com/aggregate-to-monitoring": "true"},
				"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"a", "b"}}},
			}}},
		},
	},
	{
		component: "clusterrolebinding", handler: &components.ClusterRoleBindingHandler{},
		gvk: rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"),
		typ: reflect.TypeFor[rbacv1.ClusterRoleBinding](), wholeObject: true,
		minimal: map[string]any{"roleRef": map[string]any{"kind": "ClusterRole", "name": "view"}},
		full:    bindingProperties(map[string]any{"apiGroup": rbacv1.GroupName, "kind": "ClusterRole", "name": "view"}),
	},
	{
		component: "clustersecretstore", handler: &components.ClusterSecretStoreHandler{},
		gvk: esv1.SchemeGroupVersion.WithKind(esv1.ClusterSecretStoreKind),
		typ: reflect.TypeFor[esv1.SecretStoreSpec](), held: true,
		minimal: secretStoreMinimal(),
		full:    secretStoreFull(),
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
		component: "endpointslice", handler: &components.EndpointSliceHandler{},
		gvk: discoveryv1.SchemeGroupVersion.WithKind("EndpointSlice"),
		typ: reflect.TypeFor[discoveryv1.EndpointSlice](), wholeObject: true, namespaced: true,
		// The API requires the address type alone: a slice may hold no endpoint
		// and no port.
		minimal: map[string]any{"addressType": "IPv4"},
		full: map[string]any{
			"addressType": "IPv4",
			"endpoints": []any{
				map[string]any{
					"addresses":  []any{"192.0.2.10"},
					"conditions": map[string]any{"ready": false, "serving": true, "terminating": false},
					"hostname":   "web-0",
					"targetRef":  map[string]any{"kind": "Pod", "namespace": "apps", "name": "web-0"},
					"nodeName":   "node-a", "zone": "eu-west-1a",
					"hints": map[string]any{
						"forZones": []any{map[string]any{"name": "eu-west-1a"}},
						"forNodes": []any{map[string]any{"name": "node-a"}},
					},
					"deprecatedTopology": map[string]any{"topology.kubernetes.io/region": "eu-west-1"},
				},
				map[string]any{"addresses": []any{"192.0.2.11", "192.0.2.12"}},
			},
			"ports": []any{
				map[string]any{"name": "http", "protocol": "TCP", "port": 8080, "appProtocol": "kubernetes.io/h2c"},
				// A slice not derived from a Service may leave a port's number out.
				map[string]any{"name": "metrics"},
			},
		},
	},
	{
		component: "externalsecret", handler: &components.ExternalSecretHandler{},
		gvk: esv1.SchemeGroupVersion.WithKind(esv1.ExtSecretKind),
		typ: reflect.TypeFor[esv1.ExternalSecretSpec](), namespaced: true, held: true,
		// The API requires no field of an ExternalSecret's spec.
		minimal: map[string]any{},
		full:    externalSecretFull(),
	},
	{
		component: "fluxcd-alert", handler: &components.FluxcdAlertHandler{},
		gvk: notificationv1beta3.GroupVersion.WithKind(notificationv1beta3.AlertKind),
		typ: reflect.TypeFor[notificationv1beta3.AlertSpec](), namespaced: true, flux: true,
		minimal: fluxAlertMinimal(),
		full:    fluxAlertFull(),
	},
	{
		component: "gateway", handler: &components.GatewayHandler{},
		gvk: gatewayGVK("Gateway"),
		typ: reflect.TypeFor[gatewayv1.GatewaySpec](), namespaced: true,
		minimal: gatewayMinimal(),
		full:    gatewayFull(),
	},
	{
		component: "gatewayclass", handler: &components.GatewayClassHandler{},
		gvk:     gatewayGVK("GatewayClass"),
		typ:     reflect.TypeFor[gatewayv1.GatewayClassSpec](),
		minimal: map[string]any{"controllerName": "example.net/gateway-controller"},
		full:    gatewayClassFull(),
	},
	{
		component: "imagepolicy", handler: &components.ImagePolicyHandler{},
		gvk: imagev1.GroupVersion.WithKind(imagev1.ImagePolicyKind),
		typ: reflect.TypeFor[imagev1.ImagePolicySpec](), namespaced: true, flux: true,
		minimal: imagePolicyMinimal(),
		full:    imagePolicyFull(),
	},
	{
		component: "imageupdateautomation", handler: &components.ImageUpdateAutomationHandler{},
		gvk: autov1.GroupVersion.WithKind(autov1.ImageUpdateAutomationKind),
		typ: reflect.TypeFor[autov1.ImageUpdateAutomationSpec](), namespaced: true, flux: true,
		minimal: imageUpdateAutomationMinimal(),
		full:    imageUpdateAutomationFull(),
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
		component: "issuer", handler: &components.IssuerHandler{},
		gvk: certv1.SchemeGroupVersion.WithKind("Issuer"),
		typ: reflect.TypeFor[certv1.IssuerSpec](), namespaced: true, held: true,
		// The API's schema requires no issuer type; cert-manager's webhook wants
		// one, and the kind leaves that to it.
		minimal: map[string]any{},
		full:    issuerFull(),
	},
	{
		component: "listenerset", handler: &components.ListenerSetHandler{},
		gvk: gatewayGVK("ListenerSet"),
		typ: reflect.TypeFor[gatewayv1.ListenerSetSpec](), namespaced: true,
		minimal: listenerSetWith(gatewayListener()),
		full:    listenerSetFull(),
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
		component: "referencegrant", handler: &components.ReferenceGrantHandler{},
		gvk: gatewayGVK("ReferenceGrant"),
		typ: reflect.TypeFor[gatewayv1.ReferenceGrantSpec](), namespaced: true,
		minimal: referenceGrantMinimal(),
		full:    referenceGrantFull(),
	},
	{
		component: "replicationdestination", handler: &components.ReplicationDestinationHandler{},
		gvk: volsyncv1alpha1.GroupVersion.WithKind("ReplicationDestination"),
		typ: reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec](), namespaced: true, held: true,
		minimal: map[string]any{},
		full:    replicationDestinationFull(),
	},
	{
		component: "replicationsource", handler: &components.ReplicationSourceHandler{},
		gvk: volsyncv1alpha1.GroupVersion.WithKind("ReplicationSource"),
		typ: reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec](), namespaced: true, held: true,
		// The API's schema requires no field at the top level; the operator
		// wants one mover when it reconciles, and the kind leaves that to it.
		minimal: map[string]any{},
		full:    replicationSourceFull(),
	},
	{
		component: "role", handler: &components.RoleHandler{},
		gvk: rbacv1.SchemeGroupVersion.WithKind("Role"),
		typ: reflect.TypeFor[rbacv1.Role](), wholeObject: true, namespaced: true,
		minimal: map[string]any{},
		full: map[string]any{"rules": []any{
			policyRule("", "pods", "get", "list"),
			map[string]any{"apiGroups": []any{"apps"}, "resources": []any{"deployments"}, "resourceNames": []any{"web"}, "verbs": []any{"*"}},
		}},
	},
	{
		component: "rolebinding", handler: &components.RoleBindingHandler{},
		gvk: rbacv1.SchemeGroupVersion.WithKind("RoleBinding"),
		typ: reflect.TypeFor[rbacv1.RoleBinding](), wholeObject: true, namespaced: true,
		minimal: map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "reader"}},
		full:    bindingProperties(map[string]any{"apiGroup": rbacv1.GroupName, "kind": "ClusterRole", "name": "view"}),
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
		component: "secretstore", handler: &components.SecretStoreHandler{},
		gvk: esv1.SchemeGroupVersion.WithKind(esv1.SecretStoreKind),
		typ: reflect.TypeFor[esv1.SecretStoreSpec](), namespaced: true, held: true,
		minimal: secretStoreMinimal(),
		full:    secretStoreFull(),
	},
	{
		component: "servicecidr", handler: &components.ServiceCIDRHandler{},
		gvk:     networkingv1.SchemeGroupVersion.WithKind("ServiceCIDR"),
		typ:     reflect.TypeFor[networkingv1.ServiceCIDRSpec](),
		minimal: map[string]any{"cidrs": []any{"10.96.0.0/16"}},
		full:    map[string]any{"cidrs": []any{"10.96.0.0/16", "fd00:10:96::/112"}},
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
}

// policyRule is a rule that grants verbs on one resource of one API group.
func policyRule(apiGroup, resource string, verbs ...any) map[string]any {
	return map[string]any{"apiGroups": []any{apiGroup}, "resources": []any{resource}, "verbs": verbs}
}

// bindingProperties is a binding of roleRef to a subject of each kind.
func bindingProperties(roleRef map[string]any) map[string]any {
	return map[string]any{
		"subjects": []any{
			map[string]any{"kind": "ServiceAccount", "name": "web", "namespace": "apps"},
			map[string]any{"kind": "User", "name": "jane", "apiGroup": rbacv1.GroupName},
			map[string]any{"kind": "Group", "name": "ops", "apiGroup": rbacv1.GroupName},
		},
		"roleRef": roleRef,
	}
}

// bindingTo is a binding of the role named view, of the given kind, to
// subjects.
func bindingTo(kind string, subjects ...any) map[string]any {
	return map[string]any{"subjects": subjects, "roleRef": map[string]any{"kind": kind, "name": "view"}}
}

// endpointSlice is an IPv4 endpointslice of the given endpoints.
func endpointSlice(endpoints ...any) map[string]any {
	return map[string]any{"addressType": "IPv4", "endpoints": endpoints}
}

// egressGatewayPolicy is the least a cilium-egressgatewaypolicy may author:
// the three fields the API requires, the lists empty, and the gateway's node
// selector, empty too.
func egressGatewayPolicy() map[string]any {
	return map[string]any{
		"selectors": []any{}, "destinationCIDRs": []any{},
		"egressGateway": map[string]any{"nodeSelector": map[string]any{}},
	}
}

// redirectPolicy is the properties of a cilium-localredirectpolicy with the
// frontend and the backend.
func redirectPolicy(frontend, backend any) map[string]any {
	return map[string]any{"redirectFrontend": frontend, "redirectBackend": backend}
}

// redirectAddress is a redirect frontend that matches an address and a port.
func redirectAddress() map[string]any {
	return map[string]any{"addressMatcher": map[string]any{
		"ip": "169.254.169.254", "toPorts": []any{map[string]any{"port": "80", "protocol": "TCP"}},
	}}
}

// redirectService is a redirect frontend that matches a Service, with the
// ports where any are given.
func redirectService(ports ...any) map[string]any {
	service := map[string]any{"serviceName": "kube-dns", "namespace": "kube-system"}
	if len(ports) > 0 {
		service["toPorts"] = ports
	}
	return map[string]any{"serviceMatcher": service}
}

// redirectBackend is a redirect backend: every pod of the node, and the ports
// given or, with none, one.
func redirectBackend(ports ...any) map[string]any {
	if len(ports) == 0 {
		ports = []any{map[string]any{"port": "8080", "protocol": "TCP"}}
	}
	return map[string]any{"localEndpointSelector": map[string]any{}, "toPorts": ports}
}

// ciliumExpression is a label selector with one match expression.
func ciliumExpression(expression map[string]any) map[string]any {
	return map[string]any{"matchExpressions": []any{expression}}
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
	// A SecretStore and a ClusterSecretStore hold one spec type, and an
	// ExternalSecret's spec is the one a ClusterExternalSecret holds under its
	// own.
	storeReaches := []string{
		".Spec.Provider", ".Spec.Provider.Vault", ".Spec.Provider.Vault.Path", ".Spec.Provider.Vault.Auth",
		".Spec.Provider.Vault.Auth.Kubernetes.ServiceAccountRef", ".Spec.Provider.Vault.Auth.Kubernetes.ServiceAccountRef.Audiences",
		".Spec.Provider.Vault.CAProvider", ".Spec.Provider.Vault.Headers", ".Spec.RetrySettings", ".Spec.RetrySettings.MaxRetries",
		".Spec.RefreshInterval", ".Spec.Conditions", ".Spec.Conditions[0].NamespaceSelector.MatchLabels", ".Spec.Conditions[0].Namespaces",
	}
	externalSecretReaches := func(at string) []string {
		var out []string
		for _, path := range []string{
			".Target.Template", ".Target.Template.Metadata.Labels", ".Target.Template.Data", ".Target.Template.TemplateFrom",
			".Target.Template.TemplateFrom[0].ConfigMap.Items", ".RefreshInterval", ".SyncWindows", ".SyncWindows.Windows",
			".Data", ".Data[1].SourceRef", ".DataFrom", ".DataFrom[0].Extract", ".DataFrom[1].Find.Name", ".DataFrom[1].Find.Tags",
			".DataFrom[1].Rewrite", ".DataFrom[1].Rewrite[0].Regexp", ".DataFrom[2].SourceRef.GeneratorRef",
		} {
			out = append(out, at+path)
		}
		return out
	}
	// In the order of the component types, as policyFreeKinds.
	reaches := map[string][]string{
		"artifactgenerator": {
			".Spec.CommonMetadata", ".Spec.CommonMetadata.Labels", ".Spec.CommonMetadata.Annotations",
			".Spec.Sources", ".Spec.OutputArtifacts", ".Spec.OutputArtifacts[0].Copy",
			".Spec.OutputArtifacts[0].Copy[0].Exclude",
		},
		"backendtlspolicy": {
			".Spec.TargetRefs", ".Spec.TargetRefs[1].SectionName", ".Spec.Validation.CACertificateRefs",
			".Spec.Validation.SubjectAltNames", ".Spec.Options",
		},
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
		"cilium-cidrgroup": {".Spec.ExternalCIDRs"},
		"cilium-egressgatewaypolicy": {
			".Spec.Selectors", ".Spec.Selectors[0].NamespaceSelector", ".Spec.Selectors[0].PodSelector.MatchLabels",
			".Spec.Selectors[1].NodeSelector.MatchExpressions[0].Values", ".Spec.DestinationCIDRs", ".Spec.ExcludedCIDRs",
			".Spec.EgressGateway", ".Spec.EgressGateway.NodeSelector", ".Spec.EgressGateway.NodeSelector.MatchLabels",
			".Spec.EgressGateways", ".Spec.EgressGateways[0].NodeSelector", ".Spec.EgressGateways[1].NodeSelector",
		},
		"cilium-loadbalancerippool": {
			".Spec.ServiceSelector", ".Spec.ServiceSelector.MatchLabels", ".Spec.ServiceSelector.MatchExpressions",
			".Spec.ServiceSelector.MatchExpressions[0].Values", ".Spec.Blocks",
		},
		"cilium-localredirectpolicy": {
			".Spec.RedirectFrontend.AddressMatcher", ".Spec.RedirectFrontend.AddressMatcher.ToPorts",
			".Spec.RedirectBackend.LocalEndpointSelector.MatchLabels",
			".Spec.RedirectBackend.LocalEndpointSelector.MatchExpressions[0].Values", ".Spec.RedirectBackend.ToPorts",
		},
		"cilium-nodeconfig": {
			".Spec.Defaults", ".Spec.NodeSelector", ".Spec.NodeSelector.MatchLabels", ".Spec.NodeSelector.MatchExpressions",
			".Spec.NodeSelector.MatchExpressions[0].Values",
		},
		"clusterexternalsecret": append(externalSecretReaches(".Spec.ExternalSecretSpec"),
			".Spec.ExternalSecretMetadata.Labels", ".Spec.NamespaceSelector", ".Spec.NamespaceSelectors",
			".Spec.NamespaceSelectors[1].MatchExpressions[0].Values", ".Spec.Namespaces", ".Spec.RefreshInterval",
		),
		"clusterissuer": issuerReaches,
		"clusterrole": {
			".Rules", ".Rules[0].Verbs", ".Rules[0].APIGroups", ".Rules[0].Resources", ".Rules[0].ResourceNames",
			".Rules[1].NonResourceURLs", ".AggregationRule", ".AggregationRule.ClusterRoleSelectors",
			".AggregationRule.ClusterRoleSelectors[0].MatchLabels", ".AggregationRule.ClusterRoleSelectors[0].MatchExpressions",
			".AggregationRule.ClusterRoleSelectors[0].MatchExpressions[0].Values",
		},
		"clusterrolebinding": {".Subjects"},
		"clustersecretstore": storeReaches,
		"csidriver":          {".Spec.AttachRequired", ".Spec.VolumeLifecycleModes", ".Spec.TokenRequests"},
		"endpointslice": {
			".Endpoints", ".Endpoints[0].Addresses", ".Endpoints[0].Conditions.Ready", ".Endpoints[0].Conditions.Terminating",
			".Endpoints[0].Hostname", ".Endpoints[0].TargetRef", ".Endpoints[0].DeprecatedTopology", ".Endpoints[0].NodeName",
			".Endpoints[0].Zone", ".Endpoints[0].Hints", ".Endpoints[0].Hints.ForZones", ".Endpoints[0].Hints.ForNodes",
			".Endpoints[1].Addresses", ".Ports", ".Ports[0].Name", ".Ports[0].Protocol", ".Ports[0].Port", ".Ports[0].AppProtocol",
		},
		"externalsecret": externalSecretReaches(".Spec"),
		"fluxcd-alert": {
			".Spec.EventSources", ".Spec.EventSources[1].MatchLabels", ".Spec.InclusionList", ".Spec.ExclusionList",
			".Spec.EventMetadata",
		},
		"gateway": {
			".Spec.Listeners", ".Spec.Listeners[1].Hostname", ".Spec.Listeners[1].TLS", ".Spec.Listeners[1].TLS.Mode",
			".Spec.Listeners[1].TLS.CertificateRefs", ".Spec.Listeners[1].TLS.CertificateRefs[1].Namespace",
			".Spec.Listeners[1].TLS.Options", ".Spec.Listeners[1].AllowedRoutes",
			".Spec.Listeners[1].AllowedRoutes.Namespaces.Selector.MatchLabels", ".Spec.Listeners[1].AllowedRoutes.Kinds",
			".Spec.Addresses", ".Spec.Addresses[0].Type", ".Spec.Infrastructure", ".Spec.Infrastructure.Labels",
			".Spec.Infrastructure.Annotations", ".Spec.Infrastructure.ParametersRef",
			".Spec.AllowedListeners.Namespaces.Selector.MatchLabels", ".Spec.TLS", ".Spec.TLS.Backend.ClientCertificateRef",
			".Spec.TLS.Frontend.Default.Validation", ".Spec.TLS.Frontend.Default.Validation.CACertificateRefs",
			".Spec.TLS.Frontend.PerPort", ".Spec.TLS.Frontend.PerPort[0].TLS.Validation.CACertificateRefs[0].Namespace",
		},
		"gatewayclass": {".Spec.ParametersRef", ".Spec.ParametersRef.Namespace", ".Spec.Description"},
		"imagepolicy": {
			".Spec.Policy.SemVer", ".Spec.Policy.Alphabetical", ".Spec.Policy.Numerical", ".Spec.FilterTags",
			".Spec.Interval",
		},
		"imageupdateautomation": {
			".Spec.GitSpec", ".Spec.GitSpec.Checkout", ".Spec.GitSpec.Commit.SigningKey",
			".Spec.GitSpec.Commit.MessageTemplateValues", ".Spec.GitSpec.Push", ".Spec.GitSpec.Push.Options",
			".Spec.PolicySelector", ".Spec.PolicySelector.MatchLabels", ".Spec.PolicySelector.MatchExpressions",
			".Spec.Update",
		},
		"ingressclass": {".Spec.Parameters", ".Spec.Parameters.APIGroup"},
		"issuer":       issuerReaches,
		"listenerset": {
			".Spec.ParentRef.Group", ".Spec.ParentRef.Kind", ".Spec.ParentRef.Namespace", ".Spec.Listeners",
			".Spec.Listeners[1].Hostname", ".Spec.Listeners[1].TLS.CertificateRefs", ".Spec.Listeners[1].TLS.Options",
			".Spec.Listeners[1].AllowedRoutes.Namespaces.Selector", ".Spec.Listeners[1].AllowedRoutes.Kinds[1].Group",
		},
		"poddisruptionbudget": {
			".Spec.MinAvailable", ".Spec.MaxUnavailable", ".Spec.Selector", ".Spec.Selector.MatchLabels",
			".Spec.Selector.MatchExpressions", ".Spec.Selector.MatchExpressions[0].Values", ".Spec.UnhealthyPodEvictionPolicy",
		},
		"podmonitor": {
			".Spec.PodMetricsEndpoints", ".Spec.PodMetricsEndpoints[0].Port", ".Spec.PodMetricsEndpoints[0].RelabelConfigs",
			".Spec.PodMetricsEndpoints[0].HTTPConfigWithProxy.HTTPConfig.HTTPConfigWithoutTLS.BasicAuth",
			".Spec.Selector.MatchLabels", ".Spec.Selector.MatchExpressions", ".Spec.KeepDroppedTargets", ".Spec.BodySizeLimit",
		},
		"priorityclass": {".PreemptionPolicy"},
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
		"referencegrant": {".Spec.From", ".Spec.To", ".Spec.To[1].Name"},
		"replicationdestination": {
			".Spec.Trigger", ".Spec.Trigger.Schedule", ".Spec.Rsync", ".Spec.Rsync.ReplicationDestinationVolumeOptions.Capacity",
			".Spec.Rsync.ReplicationDestinationVolumeOptions.DestinationPVC", ".Spec.Rsync.ServiceAnnotations", ".Spec.Rsync.MoverResources",
			".Spec.RsyncTLS", ".Spec.RsyncTLS.VolumeMode", ".Spec.RsyncTLS.MoverConfig.MoverSecurityContext",
			".Spec.Rclone", ".Spec.Rclone.MoverConfig.MoverAffinity", ".Spec.Rclone.MoverConfig.MoverVolumes",
			".Spec.Restic", ".Spec.Restic.CacheCapacity", ".Spec.Restic.Previous", ".Spec.Restic.MoverConfig.MoverPodLabels",
			".Spec.External", ".Spec.External.Parameters",
		},
		"replicationsource": {
			".Spec.Trigger", ".Spec.Trigger.Schedule", ".Spec.Rsync", ".Spec.Rsync.ReplicationSourceVolumeOptions.Capacity",
			".Spec.Rsync.ReplicationSourceVolumeOptions.AccessModes", ".Spec.Rsync.MoverPodLabels", ".Spec.Rsync.MoverResources",
			".Spec.RsyncTLS", ".Spec.RsyncTLS.MoverConfig.MoverSecurityContext", ".Spec.RsyncTLS.MoverConfig.MoverSecurityContext.RunAsUser",
			".Spec.Rclone", ".Spec.Rclone.MoverConfig.MoverAffinity", ".Spec.Rclone.MoverConfig.MoverVolumes",
			".Spec.Rclone.MoverConfig.MoverVolumes[0].VolumeSource.Secret", ".Spec.Restic", ".Spec.Restic.Retain",
			".Spec.Restic.Retain.Hourly", ".Spec.Restic.CacheCapacity", ".Spec.Restic.MoverConfig.MoverResources.Limits",
			".Spec.Syncthing", ".Spec.Syncthing.Peers", ".Spec.Syncthing.ConfigCapacity", ".Spec.External", ".Spec.External.Parameters",
		},
		"role": {
			".Rules", ".Rules[0].Verbs", ".Rules[0].APIGroups", ".Rules[0].Resources", ".Rules[1].ResourceNames",
		},
		"rolebinding":  {".Subjects"},
		"runtimeclass": {".Overhead", ".Overhead.PodFixed", ".Scheduling.NodeSelector", ".Scheduling.Tolerations"},
		"secretstore":  storeReaches,
		"servicecidr":  {".Spec.CIDRs"},
		"servicemonitor": {
			".Spec.Endpoints", ".Spec.Endpoints[0].Params", ".Spec.Endpoints[0].RelabelConfigs",
			".Spec.Endpoints[0].HTTPConfigWithProxyAndTLSFiles.HTTPConfigWithTLSFiles.HTTPConfigWithoutTLS.OAuth2",
			".Spec.Endpoints[1].TargetPort", ".Spec.Selector.MatchLabels", ".Spec.NamespaceSelector.MatchNames",
			".Spec.SampleLimit", ".Spec.ScrapeProtocols", ".Spec.NativeHistogramConfig.NativeHistogramMinBucketFactor",
			".Spec.AttachMetadata",
		},
		"storageclass":          {".Parameters", ".ReclaimPolicy", ".MountOptions", ".AllowedTopologies"},
		"volumeattributesclass": {".Parameters"},
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
	// A ReplicationSource and a ReplicationDestination share their movers but
	// for Syncthing. None of their fields is required at the top level: of a
	// mover that is authored the API requires some. foreign is a field the
	// other kind's movers hold and this one's do not.
	volsyncCases := func(upstream, foreign string) []refusal {
		volume := func(fields map[string]any) []any { return []any{fields} }
		secret := map[string]any{"secret": map[string]any{"secretName": "creds"}}
		return []refusal{
			{"volume without a mount path", moverWith("rclone", "moverVolumes", volume(map[string]any{"volumeSource": secret})), "rclone.moverVolumes[0].mountPath: required"},
			{"volume with a null mount path", moverWith("rclone", "moverVolumes", volume(map[string]any{"mountPath": nil, "volumeSource": secret})), "rclone.moverVolumes[0].mountPath: required"},
			{"volume without a source", moverWith("restic", "moverVolumes", volume(map[string]any{"mountPath": "creds"})), "restic.moverVolumes[0].volumeSource: required"},
			{"a later volume without a source", moverWith("rsyncTLS", "moverVolumes", []any{
				map[string]any{"mountPath": "creds", "volumeSource": secret}, map[string]any{"mountPath": "more"},
			}), "rsyncTLS.moverVolumes[1].volumeSource: required"},
			{"required node affinity without its terms", moverWith("restic", "moverAffinity", map[string]any{
				"nodeAffinity": map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{}},
			}), "restic.moverAffinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms: required"},
			{"a field of a mounted Secret the CRD does not hold", moverWith("rclone", "moverVolumes", volume(map[string]any{
				"mountPath": "creds", "volumeSource": map[string]any{"secret": map[string]any{"secretName": "creds", "defaultUser": 1000}},
			})), "rclone.moverVolumes[0].volumeSource.secret.defaultUser: no field of the volsync.backube/v1alpha1 API"},
			{"unknown key", map[string]any{"mover": "restic"}, notA + upstream},
			{"the other kind's field", moverWith("restic", foreign, "data"), notA + upstream},
			{"the object's spec", map[string]any{"spec": map[string]any{"paused": true}}, notA},
			{"mover sub-key", moverWith("restic", "repo", "restic-repo"), notA},
			// rsync over SSH shares no MoverConfig with the other movers.
			{"rsync with mover volumes", moverWith("rsync", "moverVolumes", []any{}), notA},
			{"rsync with a security context", moverWith("rsync", "moverSecurityContext", map[string]any{}), notA},
			{"a host path among the mover volumes", moverWith("restic", "moverVolumes", volume(map[string]any{
				"mountPath": "host", "volumeSource": map[string]any{"hostPath": map[string]any{"path": "/var/lib"}},
			})), notA},
			{"capacity not a quantity", moverWith("restic", "capacity", "plenty"), notA},
			{"paused a string", map[string]any{"paused": "yes"}, notA},
			{"trigger a string", map[string]any{"trigger": "0 * * * *"}, notA},
			{"null volume", moverWith("rclone", "moverVolumes", []any{map[string]any{"mountPath": "creds", "volumeSource": secret}, nil}), "rclone.moverVolumes[1]"},
		}
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
	// In the order of the component types, as policyFreeKinds.
	cases := map[string][]refusal{
		"artifactgenerator": {
			{"no properties", nil, ": required"},
			{"no sources", map[string]any{"artifacts": []any{artifactGeneratorArtifact()}}, "sources: required"},
			{"source without an alias", artifactGeneratorWith(map[string]any{"kind": "GitRepository", "name": "app"}), "sources[0].alias: required"},
			{"source without a kind", artifactGeneratorWith(map[string]any{"alias": "app", "name": "app"}), "sources[0].kind: required"},
			{"second source without a name", artifactGeneratorWith(artifactGeneratorSource(), map[string]any{"alias": "base", "kind": "OCIRepository"}), "sources[1].name: required"},
			{"no artifacts", map[string]any{"sources": []any{artifactGeneratorSource()}}, "artifacts: required"},
			{"artifact without a name", withProperty(artifactGeneratorMinimal(), "artifacts", []any{map[string]any{"copy": []any{artifactGeneratorCopy()}}}), "artifacts[0].name: required"},
			{"artifact without a copy", withProperty(artifactGeneratorMinimal(), "artifacts", []any{map[string]any{"name": "app"}}), "artifacts[0].copy: required"},
			{"copy without a from", withProperty(artifactGeneratorMinimal(), "artifacts", []any{map[string]any{"name": "app", "copy": []any{map[string]any{"to": "@artifact/"}}}}), "artifacts[0].copy[0].from: required"},
			{"copy without a to", withProperty(artifactGeneratorMinimal(), "artifacts", []any{map[string]any{"name": "app", "copy": []any{artifactGeneratorCopy(), map[string]any{"from": "@app/**"}}}}), "artifacts[0].copy[1].to: required"},
			{"unknown key", withProperty(artifactGeneratorMinimal(), "outputArtifacts", []any{}), notA + "source.extensions.fluxcd.io/v1beta1 ArtifactGeneratorSpec"},
			{"the object's spec", map[string]any{"spec": artifactGeneratorMinimal()}, notA},
			{"source sub-key", artifactGeneratorWith(map[string]any{"alias": "app", "kind": "GitRepository", "name": "app", "apiVersion": "source.toolkit.fluxcd.io/v1"}), notA},
			{"copy sub-key", withProperty(artifactGeneratorMinimal(), "artifacts", []any{map[string]any{"name": "app", "copy": []any{map[string]any{"from": "@app/**", "to": "@artifact/", "mode": "0644"}}}}), notA},
			{"sources a map", withProperty(artifactGeneratorMinimal(), "sources", map[string]any{"app": artifactGeneratorSource()}), notA},
			{"null source", artifactGeneratorWith(artifactGeneratorSource(), nil), "sources[1]"},
			{"two spellings", withProperty(artifactGeneratorMinimal(), "Sources", []any{artifactGeneratorSource()}), "sets the same field as"},
		},
		"backendtlspolicy": {
			{"no properties", nil, ": required"},
			{"no validation", map[string]any{"targetRefs": []any{backendTLSTarget()}}, "validation: required"},
			{"validation without a hostname", backendTLSPolicyWith(map[string]any{"wellKnownCACertificates": "System"}), "validation.hostname: required"},
			{"no targets", map[string]any{"validation": backendTLSValidation("wellKnownCACertificates", "System")}, "targetRefs: required"},
			{"empty targets", map[string]any{"targetRefs": []any{}, "validation": backendTLSValidation("wellKnownCACertificates", "System")}, "targetRefs: required"},
			{"target without a name", map[string]any{"targetRefs": []any{backendTLSTarget(), map[string]any{"group": "", "kind": "Service"}}, "validation": backendTLSValidation("wellKnownCACertificates", "System")}, "targetRefs[1].name: required"},
			{"CA reference without a kind", backendTLSPolicyWith(backendTLSValidation("caCertificateRefs", []any{map[string]any{"group": "", "name": "internal-ca"}})), "validation.caCertificateRefs[0].kind: required"},
			{"subject alternative name without a type", backendTLSPolicyWith(backendTLSValidation("subjectAltNames", []any{map[string]any{"hostname": "payments.example.com"}})), "validation.subjectAltNames[0].type: required"},
			{"unknown key", withProperty(backendTLSPolicyMinimal(), "targetRef", backendTLSTarget()), notA + "gateway.networking.k8s.io/v1 BackendTLSPolicySpec"},
			{"the object's spec", map[string]any{"spec": backendTLSPolicyMinimal()}, notA},
			{"validation sub-key", backendTLSPolicyWith(backendTLSValidation("caCertificate", "-----BEGIN CERTIFICATE-----")), notA},
			{"option not a string", withProperty(backendTLSPolicyMinimal(), "options", map[string]any{"example.com/min-version": 1.3}), notA},
			{"null target", map[string]any{"targetRefs": []any{backendTLSTarget(), nil}, "validation": backendTLSValidation("wellKnownCACertificates", "System")}, "targetRefs[1]"},
			{"two spellings", withProperty(backendTLSPolicyMinimal(), "Validation", map[string]any{"hostname": "other.example.com"}), "sets the same field as"},
		},
		"certificate": {
			{"no properties", nil, "issuerRef: required"},
			{"no issuer", map[string]any{"secretName": "web-tls"}, "issuerRef: required"},
			{"null issuer", map[string]any{"secretName": "web-tls", "issuerRef": nil}, "issuerRef: required"},
			{"issuer without a name", map[string]any{"secretName": "web-tls", "issuerRef": map[string]any{"kind": "ClusterIssuer"}}, "issuerRef.name: required"},
			{"no secret name", map[string]any{"issuerRef": map[string]any{"name": "ca"}}, "secretName: required"},
			{"output format without a type", certificateWith("additionalOutputFormats", []any{map[string]any{"type": "DER"}, map[string]any{}}), "additionalOutputFormats[1].type: required"},
			{"keystore without create", certificateWith("keystores", map[string]any{"jks": map[string]any{"passwordSecretRef": map[string]any{"name": "keystore"}}}), "keystores.jks.create: required"},
			{"keystore reference without a name", certificateWith("keystores", map[string]any{"pkcs12": map[string]any{"create": true, "passwordSecretRef": map[string]any{"key": "password"}}}), "keystores.pkcs12.passwordSecretRef.name: required"},
			// The type leaves either field of a renewal window out when it is empty.
			{"renewal window without a cron expression", certificateWith("renewal", renewalWindows(map[string]any{"windowDuration": "2h"})), "renewal.windows[0].cron: required"},
			{"renewal window with an empty cron expression", certificateWith("renewal", renewalWindows(map[string]any{"cron": "", "windowDuration": "2h"})), "renewal.windows[0].cron: required"},
			{"renewal window without a duration", certificateWith("renewal", renewalWindows(map[string]any{"cron": "0 2 * * *"})), "renewal.windows[0].windowDuration: required"},
			{"renewal window with a null duration", certificateWith("renewal", renewalWindows(map[string]any{"cron": "0 2 * * *", "windowDuration": nil})), "renewal.windows[0].windowDuration: required"},
			{"a later renewal window, empty", certificateWith("renewal", renewalWindows(map[string]any{"cron": "0 2 * * *", "windowDuration": "2h"}, map[string]any{})), "renewal.windows[1].windowDuration: required"},
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
			// The type leaves an interface's name and a service's addresses
			// out when they are empty, an empty list included.
			{"interface without a name", bgpAdvertisements(map[string]any{"advertisementType": "Interface", "interface": map[string]any{}}), "advertisements[0].interface.name: required"},
			{"interface with an empty name", bgpAdvertisements(map[string]any{"advertisementType": "Interface", "interface": map[string]any{"name": ""}}), "advertisements[0].interface.name: required"},
			{"service without addresses", bgpAdvertisements(map[string]any{"advertisementType": "Service", "service": map[string]any{"aggregationLengthIPv4": 24}}), "advertisements[0].service.addresses: required"},
			{"service with no address", bgpAdvertisements(map[string]any{"advertisementType": "Service", "service": map[string]any{"addresses": []any{}}}), "advertisements[0].service.addresses: required"},
			{"service with null addresses", bgpAdvertisements(map[string]any{"advertisementType": "Service", "service": map[string]any{"addresses": nil}}), "advertisements[0].service.addresses: required"},
			{"a later service without addresses", bgpAdvertisements(map[string]any{"advertisementType": "PodCIDR"}, map[string]any{"advertisementType": "Service", "service": map[string]any{}}), "advertisements[1].service.addresses: required"},
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
			// The CRD's expression rule, with both of its fields authored, and
			// with one authored against the default the API fills for the other.
			{"keepalive over hold", map[string]any{"timers": map[string]any{"keepAliveTimeSeconds": 90, "holdTimeSeconds": 30}}, "timers.keepAliveTimeSeconds: 90 is larger than timers.holdTimeSeconds (30)"},
			{"keepalive over the default hold", map[string]any{"timers": map[string]any{"keepAliveTimeSeconds": 100}}, "timers.keepAliveTimeSeconds: 100 is larger than the hold time the API fills where timers.holdTimeSeconds is not set (90)"},
			{"hold under the default keepalive", map[string]any{"timers": map[string]any{"holdTimeSeconds": 3}}, "timers.holdTimeSeconds: 3 is smaller than the keepalive time the API fills where timers.keepAliveTimeSeconds is not set (30)"},
			{"unknown key", map[string]any{"peerPort": 179}, notA + "cilium.io/v2 CiliumBGPPeerConfigSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"ebgpMultihop": 2}}, notA},
			{"timers sub-key", map[string]any{"timers": map[string]any{"holdTime": 90}}, notA},
			{"ebgpMultihop a string", map[string]any{"ebgpMultihop": "2"}, notA},
			// The reference is a Secret's name, not an object.
			{"authSecretRef a map", map[string]any{"authSecretRef": map[string]any{"name": "bgp-auth"}}, notA},
			{"null family", map[string]any{"families": []any{map[string]any{"afi": "ipv4", "safi": "unicast"}, nil}}, "families[1]"},
			{"two spellings", map[string]any{"ebgpMultihop": 2, "EBGPMultihop": 3}, "sets the same field as"},
		},
		"cilium-cidrgroup": {
			{"no properties", nil, "externalCIDRs: required"},
			{"null externalCIDRs", map[string]any{"externalCIDRs": nil}, "externalCIDRs: required"},
			{"unknown key", map[string]any{"externalCIDRs": []any{}, "cidrs": []any{}}, notA + "cilium.io/v2 CiliumCIDRGroupSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"externalCIDRs": []any{}}}, notA},
			{"externalCIDRs a string", map[string]any{"externalCIDRs": "192.0.2.0/24"}, notA},
			{"a CIDR a number", map[string]any{"externalCIDRs": []any{10}}, notA},
			{"null CIDR", map[string]any{"externalCIDRs": []any{"192.0.2.0/24", nil}}, "externalCIDRs[1]"},
			{"two spellings", map[string]any{"externalCIDRs": []any{}, "ExternalCIDRs": []any{}}, "sets the same field as"},
		},
		"cilium-egressgatewaypolicy": {
			{"no properties", nil, "destinationCIDRs: required"},
			{"no gateway", map[string]any{"selectors": []any{}, "destinationCIDRs": []any{}}, "egressGateway: required"},
			{"null gateway", withProperty(egressGatewayPolicy(), "egressGateway", nil), "egressGateway: required"},
			{"null selectors", withProperty(egressGatewayPolicy(), "selectors", nil), "selectors: required"},
			// The API requires the gateway also where a list names the gateways.
			{"gateways without the gateway", map[string]any{
				"selectors": []any{}, "destinationCIDRs": []any{}, "egressGateways": []any{map[string]any{"nodeSelector": map[string]any{}}},
			}, "egressGateway: required"},
			{"gateway without a node selector", withProperty(egressGatewayPolicy(), "egressGateway", map[string]any{"interface": "eth1"}), "egressGateway.nodeSelector: required"},
			{"gateway with a null node selector", withProperty(egressGatewayPolicy(), "egressGateway", map[string]any{"nodeSelector": nil}), "egressGateway.nodeSelector: required"},
			{"a later listed gateway without a node selector", withProperty(egressGatewayPolicy(), "egressGateways", []any{
				map[string]any{"nodeSelector": map[string]any{}}, map[string]any{"egressIP": "198.51.100.7"},
			}), "egressGateways[1].nodeSelector: required"},
			{"gateway expression without a key", withProperty(egressGatewayPolicy(), "egressGateway", map[string]any{
				"nodeSelector": ciliumExpression(map[string]any{"operator": "Exists"}),
			}), "egressGateway.nodeSelector.matchExpressions[0].key: required"},
			{"listed gateway expression without an operator", withProperty(egressGatewayPolicy(), "egressGateways", []any{map[string]any{
				"nodeSelector": ciliumExpression(map[string]any{"key": "rack"}),
			}}), "egressGateways[0].nodeSelector.matchExpressions[0].operator: required"},
			{"namespace expression without an operator", withProperty(egressGatewayPolicy(), "selectors", []any{map[string]any{
				"namespaceSelector": ciliumExpression(map[string]any{"key": "team"}),
			}}), "selectors[0].namespaceSelector.matchExpressions[0].operator: required"},
			{"pod expression without a key", withProperty(egressGatewayPolicy(), "selectors", []any{map[string]any{
				"podSelector": ciliumExpression(map[string]any{"operator": "Exists"}),
			}}), "selectors[0].podSelector.matchExpressions[0].key: required"},
			{"node expression in a later rule without a key", withProperty(egressGatewayPolicy(), "selectors", []any{map[string]any{}, map[string]any{
				"nodeSelector": ciliumExpression(map[string]any{"operator": "Exists"}),
			}}), "selectors[1].nodeSelector.matchExpressions[0].key: required"},
			{"unknown key", withProperty(egressGatewayPolicy(), "gateway", map[string]any{}), notA + "cilium.io/v2 CiliumEgressGatewayPolicySpec"},
			{"the object's spec", map[string]any{"spec": egressGatewayPolicy()}, notA},
			{"gateway sub-key", withProperty(egressGatewayPolicy(), "egressGateway", map[string]any{"nodeSelector": map[string]any{}, "ip": "198.51.100.7"}), notA},
			{"rule sub-key", withProperty(egressGatewayPolicy(), "selectors", []any{map[string]any{"selector": map[string]any{}}}), notA},
			{"destinationCIDRs a string", withProperty(egressGatewayPolicy(), "destinationCIDRs", "192.0.2.0/24"), notA},
			{"egressGateways a map", withProperty(egressGatewayPolicy(), "egressGateways", map[string]any{"nodeSelector": map[string]any{}}), notA},
			{"null rule", withProperty(egressGatewayPolicy(), "selectors", []any{map[string]any{}, nil}), "selectors[1]"},
			{"two spellings", withProperty(egressGatewayPolicy(), "Selectors", []any{}), "sets the same field as"},
		},
		"cilium-loadbalancerippool": {
			{"expression without a key", map[string]any{"serviceSelector": ciliumExpression(map[string]any{"operator": "Exists"})}, "serviceSelector.matchExpressions[0].key: required"},
			{"a later expression without an operator", map[string]any{"serviceSelector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "pool", "operator": "Exists"}, map[string]any{"key": "pool"},
			}}}, "serviceSelector.matchExpressions[1].operator: required"},
			{"unknown key", map[string]any{"cidrs": []any{}}, notA + "cilium.io/v2 CiliumLoadBalancerIPPoolSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"disabled": true}}, notA},
			// The pool's status is the operator's, and no field of the spec.
			{"the object's status", map[string]any{"status": map[string]any{"conditions": []any{}}}, notA},
			{"block sub-key", map[string]any{"blocks": []any{map[string]any{"range": "192.0.2.0/24"}}}, notA},
			{"blocks a map", map[string]any{"blocks": map[string]any{"cidr": "192.0.2.0/24"}}, notA},
			{"disabled a string", map[string]any{"disabled": "true"}, notA},
			{"null block", map[string]any{"blocks": []any{map[string]any{"cidr": "192.0.2.0/24"}, nil}}, "blocks[1]"},
			{"two spellings", map[string]any{"disabled": true, "Disabled": false}, "sets the same field as"},
		},
		"cilium-localredirectpolicy": {
			{"no properties", nil, "redirectBackend: required"},
			{"no frontend", map[string]any{"redirectBackend": redirectBackend()}, "redirectFrontend: required"},
			{"null frontend", redirectPolicy(nil, redirectBackend()), "redirectFrontend: required"},
			{"null backend", redirectPolicy(redirectAddress(), nil), "redirectBackend: required"},
			{"backend without a selector", redirectPolicy(redirectAddress(), map[string]any{
				"toPorts": []any{map[string]any{"port": "8080", "protocol": "TCP"}},
			}), "redirectBackend.localEndpointSelector: required"},
			{"backend without ports", redirectPolicy(redirectAddress(), map[string]any{"localEndpointSelector": map[string]any{}}), "redirectBackend.toPorts: required"},
			{"backend port without a port", redirectPolicy(redirectAddress(), redirectBackend(map[string]any{"protocol": "TCP"})), "redirectBackend.toPorts[0].port: required"},
			{"a later backend port without a protocol", redirectPolicy(redirectAddress(), redirectBackend(
				map[string]any{"port": "8080", "protocol": "TCP", "name": "http"}, map[string]any{"port": "8443", "name": "https"},
			)), "redirectBackend.toPorts[1].protocol: required"},
			{"backend expression without a key", redirectPolicy(redirectAddress(), map[string]any{
				"localEndpointSelector": ciliumExpression(map[string]any{"operator": "Exists"}),
				"toPorts":               []any{map[string]any{"port": "8080", "protocol": "TCP"}},
			}), "redirectBackend.localEndpointSelector.matchExpressions[0].key: required"},
			{"backend expression without an operator", redirectPolicy(redirectAddress(), map[string]any{
				"localEndpointSelector": ciliumExpression(map[string]any{"key": "app"}),
				"toPorts":               []any{map[string]any{"port": "8080", "protocol": "TCP"}},
			}), "redirectBackend.localEndpointSelector.matchExpressions[0].operator: required"},
			{"address without an ip", redirectPolicy(map[string]any{"addressMatcher": map[string]any{
				"toPorts": []any{map[string]any{"port": "80", "protocol": "TCP"}},
			}}, redirectBackend()), "redirectFrontend.addressMatcher.ip: required"},
			{"address without ports", redirectPolicy(map[string]any{"addressMatcher": map[string]any{"ip": "169.254.169.254"}}, redirectBackend()), "redirectFrontend.addressMatcher.toPorts: required"},
			{"address port without a protocol", redirectPolicy(map[string]any{"addressMatcher": map[string]any{
				"ip": "169.254.169.254", "toPorts": []any{map[string]any{"port": "80"}},
			}}, redirectBackend()), "redirectFrontend.addressMatcher.toPorts[0].protocol: required"},
			{"service without a name", redirectPolicy(map[string]any{"serviceMatcher": map[string]any{"namespace": "kube-system"}}, redirectBackend()), "redirectFrontend.serviceMatcher.serviceName: required"},
			{"service without a namespace", redirectPolicy(map[string]any{"serviceMatcher": map[string]any{"serviceName": "kube-dns"}}, redirectBackend()), "redirectFrontend.serviceMatcher.namespace: required"},
			{"service port without a port", redirectPolicy(redirectService(map[string]any{"protocol": "UDP"}), redirectBackend()), "redirectFrontend.serviceMatcher.toPorts[0].port: required"},
			// The one-of the CRD's schema declares on the frontend.
			{"frontend with no matcher", redirectPolicy(map[string]any{}, redirectBackend()), "redirectFrontend: one of addressMatcher and serviceMatcher is required"},
			{"frontend with a null matcher", redirectPolicy(map[string]any{"addressMatcher": nil}, redirectBackend()), "redirectFrontend: one of addressMatcher and serviceMatcher is required"},
			{"frontend with both matchers", redirectPolicy(map[string]any{
				"addressMatcher": redirectAddress()["addressMatcher"], "serviceMatcher": redirectService()["serviceMatcher"],
			}, redirectBackend()), "redirectFrontend: addressMatcher and serviceMatcher are both set; the API takes exactly one"},
			{"unknown key", withProperty(redirectPolicy(redirectAddress(), redirectBackend()), "frontend", map[string]any{}), notA + "cilium.io/v2 CiliumLocalRedirectPolicySpec"},
			{"the object's spec", map[string]any{"spec": redirectPolicy(redirectAddress(), redirectBackend())}, notA},
			// The policy's status is the agent's, and no field of the spec.
			{"the object's status", withProperty(redirectPolicy(redirectAddress(), redirectBackend()), "status", map[string]any{"ok": true}), notA},
			{"matcher sub-key", redirectPolicy(map[string]any{"addressMatcher": map[string]any{
				"ip": "169.254.169.254", "port": "80", "toPorts": []any{},
			}}, redirectBackend()), notA},
			// A port is a string in this API.
			{"port a number", redirectPolicy(redirectAddress(), redirectBackend(map[string]any{"port": 8080, "protocol": "TCP"})), notA},
			{"skipRedirectFromBackend a string", withProperty(redirectPolicy(redirectAddress(), redirectBackend()), "skipRedirectFromBackend", "true"), notA},
			{"null port", redirectPolicy(redirectAddress(), redirectBackend(map[string]any{"port": "8080", "protocol": "TCP"}, nil)), "redirectBackend.toPorts[1]"},
			{"two spellings", withProperty(withProperty(redirectPolicy(redirectAddress(), redirectBackend()), "description", "a"), "Description", "b"), "sets the same field as"},
		},
		"cilium-nodeconfig": {
			{"no properties", nil, "defaults: required"},
			{"null defaults", map[string]any{"defaults": nil, "nodeSelector": map[string]any{}}, "defaults: required"},
			{"no selector", map[string]any{"defaults": map[string]any{}}, "nodeSelector: required"},
			{"null selector", map[string]any{"defaults": map[string]any{}, "nodeSelector": nil}, "nodeSelector: required"},
			{"expression without a key", map[string]any{"defaults": map[string]any{}, "nodeSelector": ciliumExpression(map[string]any{"operator": "Exists"})}, "nodeSelector.matchExpressions[0].key: required"},
			{"expression without an operator", map[string]any{"defaults": map[string]any{}, "nodeSelector": ciliumExpression(map[string]any{"key": "rack"})}, "nodeSelector.matchExpressions[0].operator: required"},
			{"unknown key", map[string]any{"defaults": map[string]any{}, "nodeSelector": map[string]any{}, "selector": map[string]any{}}, notA + "cilium.io/v2 CiliumNodeConfigSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"defaults": map[string]any{}, "nodeSelector": map[string]any{}}}, notA},
			// Every value of the configuration is a string.
			{"a default a number", map[string]any{"defaults": map[string]any{"mtu": 1450}, "nodeSelector": map[string]any{}}, notA},
			{"defaults a list", map[string]any{"defaults": []any{"enable-hubble=false"}, "nodeSelector": map[string]any{}}, notA},
			{"selector sub-key", map[string]any{"defaults": map[string]any{}, "nodeSelector": map[string]any{"matchNames": []any{"node-a"}}}, notA},
			{"two spellings", map[string]any{"defaults": map[string]any{}, "Defaults": map[string]any{}, "nodeSelector": map[string]any{}}, "sets the same field as"},
		},
		"clusterissuer": issuerCases,
		"clusterrole": {
			{"rule without verbs", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"nodes"}}}}, "rules[0].verbs: required"},
			{"non-resource rule without verbs", map[string]any{"rules": []any{map[string]any{"nonResourceURLs": []any{"/healthz"}}}}, "rules[0].verbs: required"},
			{"rule without apiGroups", map[string]any{"rules": []any{map[string]any{"resources": []any{"nodes"}, "verbs": []any{"get"}}}}, "rules[0].apiGroups: required"},
			{"rule without resources", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "verbs": []any{"get"}}}}, "rules[0].resources: required"},
			{"non-resource URLs beside resources", map[string]any{"rules": []any{map[string]any{"nonResourceURLs": []any{"/healthz"}, "resources": []any{"nodes"}, "verbs": []any{"get"}}}}, "rules[0].nonResourceURLs: not allowed beside"},
			{"non-resource URLs beside an API group", map[string]any{"rules": []any{map[string]any{"nonResourceURLs": []any{"/healthz"}, "apiGroups": []any{""}, "verbs": []any{"get"}}}}, "rules[0].nonResourceURLs: not allowed beside"},
			{"non-resource URLs beside resource names", map[string]any{"rules": []any{map[string]any{"nonResourceURLs": []any{"/healthz"}, "resourceNames": []any{"a"}, "verbs": []any{"get"}}}}, "rules[0].nonResourceURLs: not allowed beside"},
			{"aggregation rule without selectors", map[string]any{"aggregationRule": map[string]any{}}, "aggregationRule.clusterRoleSelectors: required"},
			{"aggregation rule with no selector", map[string]any{"aggregationRule": map[string]any{"clusterRoleSelectors": []any{}}}, "aggregationRule.clusterRoleSelectors: required"},
			{"match expression without key", map[string]any{"aggregationRule": map[string]any{"clusterRoleSelectors": []any{map[string]any{"matchExpressions": []any{map[string]any{"operator": "Exists"}}}}}}, "aggregationRule.clusterRoleSelectors[0].matchExpressions[0].key: required"},
			{"match expression without operator", map[string]any{"aggregationRule": map[string]any{"clusterRoleSelectors": []any{map[string]any{}, map[string]any{"matchExpressions": []any{map[string]any{"key": "tier"}}}}}}, "aggregationRule.clusterRoleSelectors[1].matchExpressions[0].operator: required"},
			{"unknown key", map[string]any{"subjects": []any{}}, notA + "rbac.authorization.k8s.io/v1 ClusterRole"},
			{"aggregation rule sub-key", map[string]any{"aggregationRule": map[string]any{"selectors": []any{}}}, notA},
			{"selector a string", map[string]any{"aggregationRule": map[string]any{"clusterRoleSelectors": []any{"tier=a"}}}, notA},
			{"two spellings", map[string]any{"rules": []any{}, "Rules": []any{}}, "sets the same field as"},
		},
		"clusterrolebinding": {
			{"no properties", nil, "roleRef: required"},
			{"roleRef without kind", map[string]any{"roleRef": map[string]any{"name": "view"}}, "roleRef.kind: required"},
			{"roleRef without name", map[string]any{"roleRef": map[string]any{"kind": "ClusterRole"}}, "roleRef.name: required"},
			{"subject without kind", bindingTo("ClusterRole", map[string]any{"name": "web"}), "subjects[0].kind: required"},
			{"subject without name", bindingTo("ClusterRole", map[string]any{"kind": "Group"}), "subjects[0].name: required"},
			// What the API server requires of a subject beyond the markers.
			{"ServiceAccount without namespace", bindingTo("ClusterRole", map[string]any{"kind": "User", "name": "jane"}, map[string]any{"kind": "ServiceAccount", "name": "web"}), "subjects[1].namespace: required"},
			{"ServiceAccount with an empty namespace", bindingTo("ClusterRole", map[string]any{"kind": "ServiceAccount", "name": "web", "namespace": ""}), "subjects[0].namespace: required"},
			{"unknown key", map[string]any{"roleRef": map[string]any{"kind": "ClusterRole", "name": "view"}, "rules": []any{}}, notA + "rbac.authorization.k8s.io/v1 ClusterRoleBinding"},
			{"subject sub-key", bindingTo("ClusterRole", map[string]any{"kind": "User", "name": "jane", "uid": "1"}), notA},
			{"two spellings", map[string]any{"roleRef": map[string]any{"kind": "ClusterRole", "name": "a"}, "RoleRef": map[string]any{"kind": "ClusterRole", "name": "b"}}, "sets the same field as"},
		},
		"csidriver": {
			{"unknown key", map[string]any{"driverName": "ebs.csi.aws.com"}, notA + "storage.k8s.io/v1 CSIDriverSpec"},
			{"attachRequired a string", map[string]any{"attachRequired": "false"}, notA},
			{"token request sub-key", map[string]any{"tokenRequests": []any{map[string]any{"audiences": []any{"a"}}}}, notA},
			{"modes a string", map[string]any{"volumeLifecycleModes": "Persistent"}, notA},
			{"null mode", map[string]any{"volumeLifecycleModes": []any{"Persistent", nil}}, "volumeLifecycleModes[1]"},
			{"two spellings", map[string]any{"fsGroupPolicy": "File", "FSGroupPolicy": "None"}, "sets the same field as"},
		},
		"endpointslice": {
			{"no properties", nil, "addressType: required"},
			{"null addressType", map[string]any{"addressType": nil, "endpoints": []any{}}, "addressType: required"},
			{"endpoint without addresses", endpointSlice(map[string]any{"hostname": "web-0"}), "endpoints[0].addresses: required"},
			{"a later endpoint's null addresses", endpointSlice(map[string]any{"addresses": []any{"192.0.2.10"}}, map[string]any{"addresses": nil}), "endpoints[1].addresses: required"},
			{"zone hint without a name", endpointSlice(map[string]any{"addresses": []any{"192.0.2.10"}, "hints": map[string]any{"forZones": []any{map[string]any{}}}}), "endpoints[0].hints.forZones[0].name: required"},
			{"node hint without a name", endpointSlice(map[string]any{"addresses": []any{"192.0.2.10"}, "hints": map[string]any{"forNodes": []any{map[string]any{"name": "node-a"}, map[string]any{}}}}), "endpoints[0].hints.forNodes[1].name: required"},
			{"unknown key", map[string]any{"addressType": "IPv4", "spec": map[string]any{}}, notA + "discovery.k8s.io/v1 EndpointSlice"},
			// An endpoint's readiness sits under its conditions.
			{"endpoint sub-key", endpointSlice(map[string]any{"addresses": []any{"192.0.2.10"}, "ready": true}), notA},
			{"addresses a string", endpointSlice(map[string]any{"addresses": "192.0.2.10"}), notA},
			{"port a name", map[string]any{"addressType": "IPv4", "ports": []any{map[string]any{"port": "http"}}}, notA},
			{"null endpoint", endpointSlice(map[string]any{"addresses": []any{"192.0.2.10"}}, nil), "endpoints[1]"},
			{"two spellings", map[string]any{"addressType": "IPv4", "AddressType": "IPv6"}, "sets the same field as"},
		},
		"fluxcd-alert": {
			{"no properties", nil, ": required"},
			{"no provider", map[string]any{"eventSources": []any{fluxAlertSource()}}, "providerRef: required"},
			{"provider without a name", fluxAlertWith(map[string]any{}, fluxAlertSource()), "providerRef.name: required"},
			{"no sources", map[string]any{"providerRef": map[string]any{"name": "slack"}}, "eventSources: required"},
			{"null sources", map[string]any{"providerRef": map[string]any{"name": "slack"}, "eventSources": nil}, "eventSources: required"},
			{"source without a kind", fluxAlertWith(map[string]any{"name": "slack"}, map[string]any{"name": "web"}), "eventSources[0].kind: required"},
			{"a later source without a name", fluxAlertWith(map[string]any{"name": "slack"}, fluxAlertSource(), map[string]any{"kind": "HelmRelease"}), "eventSources[1].name: required"},
			{"unknown key", withProperty(fluxAlertMinimal(), "provider", "slack"), notA + "notification.toolkit.fluxcd.io/v1beta3 AlertSpec"},
			{"the object's spec", map[string]any{"spec": fluxAlertMinimal()}, notA},
			{"source sub-key", fluxAlertWith(map[string]any{"name": "slack"}, withProperty(fluxAlertSource(), "labels", map[string]any{"team": "shop"})), notA},
			{"provider sub-key", fluxAlertWith(map[string]any{"name": "slack", "namespace": "ops"}, fluxAlertSource()), notA},
			{"suspend a string", withProperty(fluxAlertMinimal(), "suspend", "yes"), notA},
			{"null source", fluxAlertWith(map[string]any{"name": "slack"}, fluxAlertSource(), nil), "eventSources[1]"},
			{"two spellings", withProperty(fluxAlertMinimal(), "ProviderRef", map[string]any{"name": "other"}), "sets the same field as"},
		},
		"gateway": {
			{"no properties", nil, ": required"},
			{"no class", map[string]any{"listeners": []any{gatewayListener()}}, "gatewayClassName: required"},
			{"no listeners", map[string]any{"gatewayClassName": "public"}, "listeners: required"},
			{"null listeners", map[string]any{"gatewayClassName": "public", "listeners": nil}, "listeners: required"},
			{"listener without a name", gatewayWith(map[string]any{"port": 80, "protocol": "HTTP"}), "listeners[0].name: required"},
			{"a later listener without a port", gatewayWith(gatewayListener(), map[string]any{"name": "https", "protocol": "HTTPS"}), "listeners[1].port: required"},
			{"listener without a protocol", gatewayWith(map[string]any{"name": "http", "port": 80}), "listeners[0].protocol: required"},
			{"certificate reference without a name", gatewayWith(gatewayListenerWith("tls", map[string]any{"certificateRefs": []any{map[string]any{"kind": "Secret"}}})), "listeners[0].tls.certificateRefs[0].name: required"},
			{"allowed route kind without a kind", gatewayWith(gatewayListenerWith("allowedRoutes", map[string]any{"kinds": []any{map[string]any{"kind": "HTTPRoute"}, map[string]any{"group": "gateway.networking.k8s.io"}}})), "listeners[0].allowedRoutes.kinds[1].kind: required"},
			{"parameters without a group", withProperty(gatewayMinimal(), "infrastructure", map[string]any{"parametersRef": map[string]any{"kind": "ConfigMap", "name": "config"}}), "infrastructure.parametersRef.group: required"},
			{"client certificate without a name", gatewayTLS(map[string]any{"backend": map[string]any{"clientCertificateRef": map[string]any{"kind": "Secret"}}}), "tls.backend.clientCertificateRef.name: required"},
			{"frontend without a default", gatewayTLS(map[string]any{"frontend": map[string]any{"perPort": []any{}}}), "tls.frontend.default: required"},
			{"validation without CA certificates", gatewayTLS(map[string]any{"frontend": map[string]any{"default": map[string]any{"validation": map[string]any{"mode": "AllowValidOnly"}}}}), "tls.frontend.default.validation.caCertificateRefs: required"},
			{"CA reference without a kind", gatewayTLS(map[string]any{"frontend": map[string]any{"default": map[string]any{"validation": map[string]any{"caCertificateRefs": []any{map[string]any{"group": "", "name": "client-ca"}}}}}}), "tls.frontend.default.validation.caCertificateRefs[0].kind: required"},
			{"port configuration without a port", gatewayTLS(map[string]any{"frontend": map[string]any{"default": map[string]any{}, "perPort": []any{map[string]any{"tls": map[string]any{}}}}}), "tls.frontend.perPort[0].port: required"},
			{"port configuration without its tls", gatewayTLS(map[string]any{"frontend": map[string]any{"default": map[string]any{}, "perPort": []any{map[string]any{"port": 8443}}}}), "tls.frontend.perPort[0].tls: required"},
			{"a port's CA reference without a name", gatewayTLS(map[string]any{"frontend": map[string]any{"default": map[string]any{}, "perPort": []any{map[string]any{"port": 8443, "tls": map[string]any{"validation": map[string]any{"caCertificateRefs": []any{map[string]any{"group": "", "kind": "ConfigMap"}}}}}}}}), "tls.frontend.perPort[0].tls.validation.caCertificateRefs[0].name: required"},
			{"unknown key", withProperty(gatewayMinimal(), "class", "public"), notA + "gateway.networking.k8s.io/v1 GatewaySpec"},
			{"the object's spec", map[string]any{"spec": gatewayMinimal()}, notA},
			{"listener sub-key", gatewayWith(gatewayListenerWith("targetPort", 8080)), notA},
			{"port a string", gatewayWith(gatewayListenerWith("port", "http")), notA},
			{"listeners a map", map[string]any{"gatewayClassName": "public", "listeners": gatewayListener()}, notA},
			{"null listener", gatewayWith(gatewayListener(), nil), "listeners[1]"},
			{"two spellings", withProperty(gatewayMinimal(), "GatewayClassName", "other"), "sets the same field as"},
		},
		"gatewayclass": {
			{"no properties", nil, "controllerName: required"},
			{"null controller", map[string]any{"controllerName": nil}, "controllerName: required"},
			{"parameters without a group", map[string]any{"controllerName": "example.net/c", "parametersRef": map[string]any{"kind": "ConfigMap", "name": "config"}}, "parametersRef.group: required"},
			{"parameters without a kind", map[string]any{"controllerName": "example.net/c", "parametersRef": map[string]any{"group": "", "name": "config"}}, "parametersRef.kind: required"},
			{"parameters without a name", map[string]any{"controllerName": "example.net/c", "parametersRef": map[string]any{"group": "", "kind": "ConfigMap"}}, "parametersRef.name: required"},
			{"unknown key", map[string]any{"controllerName": "example.net/c", "controller": "c"}, notA + "gateway.networking.k8s.io/v1 GatewayClassSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"controllerName": "example.net/c"}}, notA},
			{"parameters sub-key", map[string]any{"controllerName": "example.net/c", "parametersRef": map[string]any{"group": "", "kind": "ConfigMap", "name": "config", "key": "k"}}, notA},
			{"description a number", map[string]any{"controllerName": "example.net/c", "description": 1}, notA},
			{"two spellings", map[string]any{"controllerName": "example.net/c", "ControllerName": "example.net/d"}, "sets the same field as"},
		},
		"imagepolicy": {
			{"no properties", nil, ": required"},
			{"no repository", map[string]any{"policy": imagePolicySemver()}, "imageRepositoryRef: required"},
			{"repository without a name", withProperty(imagePolicyMinimal(), "imageRepositoryRef", map[string]any{"namespace": "registry"}), "imageRepositoryRef.name: required"},
			{"no policy", map[string]any{"imageRepositoryRef": map[string]any{"name": "web"}}, "policy: required"},
			{"semver without a range", withProperty(imagePolicyMinimal(), "policy", map[string]any{"semver": map[string]any{}}), "policy.semver.range: required"},
			{"unknown key", withProperty(imagePolicyMinimal(), "imageRepository", "web"), notA + "image.toolkit.fluxcd.io/v1 ImagePolicySpec"},
			{"the object's spec", map[string]any{"spec": imagePolicyMinimal()}, notA},
			{"policy sub-key", withProperty(imagePolicyMinimal(), "policy", map[string]any{"latest": map[string]any{}}), notA},
			{"repository sub-key", withProperty(imagePolicyMinimal(), "imageRepositoryRef", map[string]any{"name": "web", "kind": "ImageRepository"}), notA},
			{"suspend a string", withProperty(imagePolicyMinimal(), "suspend", "yes"), notA},
			{"interval not a duration", withProperty(imagePolicyMinimal(), "interval", "soon"), notA},
			{"interval in a unit the API refuses", withProperty(imagePolicyMinimal(), "interval", "500us"), `imagepolicy: interval "500us" is invalid: must be a Flux duration`},
			{"interval signed", withProperty(imagePolicyMinimal(), "interval", "-5m"), `imagepolicy: interval "-5m" is invalid: must be a Flux duration`},
			{"interval emitted below a millisecond", withProperty(imagePolicyMinimal(), "Interval", "0.5ms"), `imagepolicy: interval "0.5ms" is invalid: it would be emitted as "500µs"`},
			{"two spellings", withProperty(imagePolicyMinimal(), "Policy", imagePolicySemver()), "sets the same field as"},
		},
		"imageupdateautomation": {
			{"no properties", nil, ": required"},
			{"no source", map[string]any{"interval": "30m"}, "sourceRef: required"},
			{"source without a kind", withProperty(imageUpdateAutomationMinimal(), "sourceRef", map[string]any{"name": "fleet"}), "sourceRef.kind: required"},
			{"source without a name", withProperty(imageUpdateAutomationMinimal(), "sourceRef", map[string]any{"kind": "GitRepository"}), "sourceRef.name: required"},
			{"no interval", map[string]any{"sourceRef": imageUpdateAutomationSource()}, "interval: required"},
			{"git without a commit", withProperty(imageUpdateAutomationMinimal(), "git", map[string]any{}), "git.commit: required"},
			{"commit without an author", withProperty(imageUpdateAutomationMinimal(), "git", map[string]any{"commit": map[string]any{}}), "git.commit.author: required"},
			{"author without an email", withProperty(imageUpdateAutomationMinimal(), "git", imageUpdateAutomationGit(map[string]any{"author": map[string]any{"name": "fluxbot"}})), "git.commit.author.email: required"},
			{"checkout without a ref", withProperty(imageUpdateAutomationMinimal(), "git", map[string]any{"checkout": map[string]any{}, "commit": imageUpdateAutomationCommit()}), "git.checkout.ref: required"},
			{"signing key without a Secret", withProperty(imageUpdateAutomationMinimal(), "git", imageUpdateAutomationGit(map[string]any{"author": imageUpdateAutomationAuthor(), "signingKey": map[string]any{"type": "ssh"}})), "git.commit.signingKey.secretRef: required"},
			{"signing key's Secret without a name", withProperty(imageUpdateAutomationMinimal(), "git", imageUpdateAutomationGit(map[string]any{"author": imageUpdateAutomationAuthor(), "signingKey": map[string]any{"secretRef": map[string]any{}}})), "git.commit.signingKey.secretRef.name: required"},
			{"unknown key", withProperty(imageUpdateAutomationMinimal(), "gitRepositoryRef", map[string]any{"name": "fleet"}), notA + "image.toolkit.fluxcd.io/v1 ImageUpdateAutomationSpec"},
			{"the object's spec", map[string]any{"spec": imageUpdateAutomationMinimal()}, notA},
			{"source sub-key", withProperty(imageUpdateAutomationMinimal(), "sourceRef", map[string]any{"kind": "GitRepository", "name": "fleet", "branch": "main"}), notA},
			{"push sub-key", withProperty(imageUpdateAutomationMinimal(), "git", map[string]any{"commit": imageUpdateAutomationCommit(), "push": map[string]any{"force": true}}), notA},
			{"suspend a string", withProperty(imageUpdateAutomationMinimal(), "suspend", "yes"), notA},
			{"interval not a duration", withProperty(imageUpdateAutomationMinimal(), "interval", "soon"), notA},
			{"interval in a unit the API refuses", withProperty(imageUpdateAutomationMinimal(), "interval", "500us"), `imageupdateautomation: interval "500us" is invalid: must be a Flux duration`},
			{"interval signed", withProperty(imageUpdateAutomationMinimal(), "interval", "-5m"), `imageupdateautomation: interval "-5m" is invalid: must be a Flux duration`},
			{"interval emitted below a millisecond", withProperty(imageUpdateAutomationMinimal(), "interval", "0.5ms"), `imageupdateautomation: interval "0.5ms" is invalid: it would be emitted as "500µs"`},
			{"two spellings", withProperty(imageUpdateAutomationMinimal(), "SourceRef", imageUpdateAutomationSource()), "sets the same field as"},
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
		"issuer": issuerCases,
		"listenerset": {
			{"no properties", nil, ": required"},
			{"no parent", map[string]any{"listeners": []any{gatewayListener()}}, "parentRef: required"},
			{"null parent", map[string]any{"parentRef": nil, "listeners": []any{gatewayListener()}}, "parentRef: required"},
			{"parent without a name", map[string]any{"parentRef": map[string]any{"kind": "Gateway"}, "listeners": []any{gatewayListener()}}, "parentRef.name: required"},
			{"no listeners", map[string]any{"parentRef": map[string]any{"name": "public"}}, "listeners: required"},
			{"null listeners", map[string]any{"parentRef": map[string]any{"name": "public"}, "listeners": nil}, "listeners: required"},
			{"empty listeners", listenerSetWith(), "listeners: required"},
			// The type omits a listener's name, port and protocol where they
			// are not authored and where they are authored empty, so the kind
			// refuses both: the API server would refuse the object either way.
			{"empty listener", listenerSetWith(map[string]any{}), "listeners[0].name: required"},
			{"listener without a name", listenerSetWith(map[string]any{"port": 80, "protocol": "HTTP"}), "listeners[0].name: required"},
			{"listener with an empty name", listenerSetWith(gatewayListenerWith("name", "")), "listeners[0].name: required"},
			{"listener with a null name", listenerSetWith(gatewayListenerWith("name", nil)), "listeners[0].name: required"},
			{"a later listener without a port", listenerSetWith(gatewayListener(), map[string]any{"name": "https", "protocol": "HTTPS"}), "listeners[1].port: required"},
			{"listener with a port of 0", listenerSetWith(gatewayListenerWith("port", 0)), "listeners[0].port: required"},
			{"listener without a protocol", listenerSetWith(map[string]any{"name": "http", "port": 80}), "listeners[0].protocol: required"},
			{"a later listener with an empty protocol", listenerSetWith(gatewayListener(), gatewayListener(), map[string]any{"name": "tls", "port": 8443, "protocol": ""}), "listeners[2].protocol: required"},
			{"certificate reference without a name", listenerSetWith(gatewayListenerWith("tls", map[string]any{"certificateRefs": []any{map[string]any{"name": "a"}, map[string]any{"namespace": "certs"}}})), "listeners[0].tls.certificateRefs[1].name: required"},
			{"allowed route kind without a kind", listenerSetWith(gatewayListenerWith("allowedRoutes", map[string]any{"kinds": []any{map[string]any{}}})), "listeners[0].allowedRoutes.kinds[0].kind: required"},
			{"unknown key", withProperty(listenerSetWith(gatewayListener()), "gateway", "public"), notA + "gateway.networking.k8s.io/v1 ListenerSetSpec"},
			{"the object's spec", map[string]any{"spec": listenerSetWith(gatewayListener())}, notA},
			{"parent sub-key", map[string]any{"parentRef": map[string]any{"name": "public", "sectionName": "http"}, "listeners": []any{gatewayListener()}}, notA},
			{"port a string", listenerSetWith(gatewayListenerWith("port", "http")), notA},
			{"null listener", listenerSetWith(gatewayListener(), nil), "listeners[1]"},
			{"two spellings", withProperty(listenerSetWith(gatewayListener()), "ParentRef", map[string]any{"name": "other"}), "sets the same field as"},
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
		"priorityclass": {
			{"unknown key", map[string]any{"value": 1, "priority": 1}, notA + "scheduling.k8s.io/v1 PriorityClass"},
			{"value a string", map[string]any{"value": "high"}, notA},
			{"value not an integer", map[string]any{"value": 1.5}, notA},
			{"value over int32", map[string]any{"value": 4294967296}, notA},
			{"globalDefault a string", map[string]any{"value": 1, "globalDefault": "true"}, notA},
			{"two spellings", map[string]any{"value": 1, "Value": 2}, "sets the same field as"},
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
			// The type leaves a parameter's name out when it is empty.
			{"parameter without a name", withProperty(probeProperties(), "params", []any{map[string]any{"values": []any{"http_2xx"}}}), "params[0].name: required"},
			{"parameter with an empty name", withProperty(probeProperties(), "params", []any{map[string]any{"name": ""}}), "params[0].name: required"},
			{"parameter with a null name", withProperty(probeProperties(), "params", []any{map[string]any{"name": nil}}), "params[0].name: required"},
			{"a later parameter without a name", withProperty(probeProperties(), "params", []any{map[string]any{"name": "module"}, map[string]any{}}), "params[1].name: required"},
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
		"referencegrant": {
			{"no properties", nil, ": required"},
			{"no sources", map[string]any{"to": []any{map[string]any{"group": "", "kind": "Service"}}}, "from: required"},
			{"no targets", map[string]any{"from": []any{referenceGrantFrom()}}, "to: required"},
			{"null targets", map[string]any{"from": []any{referenceGrantFrom()}, "to": nil}, "to: required"},
			{"source without a group", referenceGrantWith(map[string]any{"kind": "HTTPRoute", "namespace": "shop"}, map[string]any{"group": "", "kind": "Service"}), "from[0].group: required"},
			{"source without a kind", referenceGrantWith(map[string]any{"group": "gateway.networking.k8s.io", "namespace": "shop"}, map[string]any{"group": "", "kind": "Service"}), "from[0].kind: required"},
			{"source without a namespace", referenceGrantWith(map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute"}, map[string]any{"group": "", "kind": "Service"}), "from[0].namespace: required"},
			{"target without a group", referenceGrantWith(referenceGrantFrom(), map[string]any{"kind": "Service"}), "to[0].group: required"},
			{"a later target without a kind", referenceGrantWith(referenceGrantFrom(), map[string]any{"group": "", "kind": "Service"}, map[string]any{"group": "", "name": "tls"}), "to[1].kind: required"},
			{"unknown key", withProperty(referenceGrantMinimal(), "namespace", "shop"), notA + "gateway.networking.k8s.io/v1 ReferenceGrantSpec"},
			{"the object's spec", map[string]any{"spec": referenceGrantMinimal()}, notA},
			{"target sub-key", referenceGrantWith(referenceGrantFrom(), map[string]any{"group": "", "kind": "Service", "namespace": "shop"}), notA},
			{"sources a map", map[string]any{"from": referenceGrantFrom(), "to": []any{map[string]any{"group": "", "kind": "Service"}}}, notA},
			{"null target", referenceGrantWith(referenceGrantFrom(), map[string]any{"group": "", "kind": "Service"}, nil), "to[1]"},
			{"two spellings", withProperty(referenceGrantMinimal(), "From", []any{referenceGrantFrom()}), "sets the same field as"},
		},
		"replicationdestination": append(volsyncCases("volsync.backube/v1alpha1 ReplicationDestinationSpec", "sourcePVC"),
			// A destination has no Syncthing mover and names no source claim.
			refusal{"syncthing", map[string]any{"syncthing": map[string]any{}}, notA},
			refusal{"a source claim", map[string]any{"sourcePVC": "data"}, notA},
			refusal{"two spellings", map[string]any{"paused": true, "Paused": false}, "sets the same field as"},
		),
		"replicationsource": append(volsyncCases("volsync.backube/v1alpha1 ReplicationSourceSpec", "destinationPVC"),
			refusal{"peer without an address", syncthingPeers(withoutProperty(syncthingPeer(), "address")), "syncthing.peers[0].address: required"},
			refusal{"peer without an ID", syncthingPeers(withoutProperty(syncthingPeer(), "ID")), "syncthing.peers[0].ID: required"},
			refusal{"peer without introducer", syncthingPeers(withoutProperty(syncthingPeer(), "introducer")), "syncthing.peers[0].introducer: required"},
			refusal{"peer with a null introducer", syncthingPeers(withProperty(syncthingPeer(), "introducer", nil)), "syncthing.peers[0].introducer: required"},
			refusal{"a later peer, empty", syncthingPeers(syncthingPeer(), map[string]any{}), "syncthing.peers[1]."},
			refusal{"syncthing volume without a source", moverWith("syncthing", "moverVolumes", []any{map[string]any{"mountPath": "creds"}}), "syncthing.moverVolumes[0].volumeSource: required"},
			refusal{"syncthing has no capacity", moverWith("syncthing", "capacity", "1Gi"), notA},
			refusal{"null peer", syncthingPeers(syncthingPeer(), nil), "syncthing.peers[1]"},
			refusal{"two spellings", map[string]any{"sourcePVC": "data", "SourcePVC": "other"}, "sets the same field as"},
		),
		"role": {
			{"rule without verbs", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}}}}, "rules[0].verbs: required"},
			{"a later rule's null verbs", map[string]any{"rules": []any{policyRule("", "pods", "get"), map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": nil}}}, "rules[1].verbs: required"},
			// What the API server requires of a rule beyond the markers.
			{"rule without apiGroups", map[string]any{"rules": []any{map[string]any{"resources": []any{"pods"}, "verbs": []any{"get"}}}}, "rules[0].apiGroups: required"},
			{"rule with no API group", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{}, "resources": []any{"pods"}, "verbs": []any{"get"}}}}, "rules[0].apiGroups: required"},
			{"rule without resources", map[string]any{"rules": []any{policyRule("", "pods", "get"), map[string]any{"apiGroups": []any{""}, "verbs": []any{"get"}}}}, "rules[1].resources: required"},
			{"non-resource URLs", map[string]any{"rules": []any{map[string]any{"nonResourceURLs": []any{"/healthz"}, "verbs": []any{"get"}}}}, "rules[0].nonResourceURLs: not allowed in a Role"},
			{"unknown key", map[string]any{"rule": []any{}}, notA + "rbac.authorization.k8s.io/v1 Role"},
			{"a clusterrole's field", map[string]any{"aggregationRule": map[string]any{}}, notA},
			{"rule sub-key", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}, "namespaces": []any{"apps"}}}}, notA},
			{"verbs a string", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": "get"}}}, notA},
			{"null rule", map[string]any{"rules": []any{policyRule("", "pods", "get"), nil}}, "rules[1]"},
			{"two spellings", map[string]any{"rules": []any{}, "Rules": []any{}}, "sets the same field as"},
		},
		"rolebinding": {
			{"no properties", nil, "roleRef: required"},
			{"null roleRef", map[string]any{"roleRef": nil}, "roleRef: required"},
			{"roleRef without kind", map[string]any{"roleRef": map[string]any{"name": "reader"}}, "roleRef.kind: required"},
			{"roleRef without name", map[string]any{"roleRef": map[string]any{"kind": "Role"}}, "roleRef.name: required"},
			{"subject without kind", bindingTo("Role", map[string]any{"name": "web"}), "subjects[0].kind: required"},
			{"a later subject without name", bindingTo("Role", map[string]any{"kind": "User", "name": "jane"}, map[string]any{"kind": "Group"}), "subjects[1].name: required"},
			{"unknown key", map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "reader"}, "rules": []any{}}, notA + "rbac.authorization.k8s.io/v1 RoleBinding"},
			{"roleRef sub-key", map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "reader", "namespace": "apps"}}, notA},
			{"subject sub-key", bindingTo("Role", map[string]any{"kind": "User", "name": "jane", "uid": "1"}), notA},
			{"roleRef a string", map[string]any{"roleRef": "reader"}, notA},
			{"null subject", bindingTo("Role", map[string]any{"kind": "User", "name": "jane"}, nil), "subjects[1]"},
			{"two spellings", map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "a"}, "RoleRef": map[string]any{"kind": "Role", "name": "b"}}, "sets the same field as"},
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
	}
	// The External Secrets Operator's kinds keep their cases beside their
	// fixtures.
	for _, kind := range secretStores {
		for _, tc := range secretStoreRefusals(notA) {
			cases[kind.component] = append(cases[kind.component], refusal(tc))
		}
	}
	for _, kind := range externalSecrets {
		for _, tc := range externalSecretRefusals(notA, kind.upstream, kind.at) {
			cases[kind.component] = append(cases[kind.component], refusal(tc))
		}
	}
	cases["clusterexternalsecret"] = append(cases["clusterexternalsecret"],
		refusal{"no properties", nil, "externalSecretSpec: required"},
		refusal{"null spec", map[string]any{"externalSecretSpec": nil}, "externalSecretSpec: required"},
		refusal{"a name alone", map[string]any{"externalSecretName": "web-credentials"}, "externalSecretSpec: required"},
		refusal{"refresh time a number", withProperty(clusterExternalSecretOf(map[string]any{}), "refreshTime", 60), notA},
		refusal{"null namespace", withProperty(clusterExternalSecretOf(map[string]any{}), "namespaces", []any{"shop", nil}), "namespaces[1]"},
	)
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
	if got := advert.Spec.Advertisements[3].Interface; got == nil || got.Name != "lo" {
		t.Errorf("interface = %+v, want the authored name lo", got)
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
	// The timers rule is checked on the two fields as authored or as the CRD
	// defaults them, and equal times pass it: a keepalive alone may be the
	// default hold time, and a hold time alone the default keepalive.
	for name, timers := range map[string]map[string]any{
		"equal":           {"keepAliveTimeSeconds": 30, "holdTimeSeconds": 30},
		"keepalive alone": {"keepAliveTimeSeconds": 90},
		"hold alone":      {"holdTimeSeconds": 30},
		"neither":         {"connectRetryTimeSeconds": 5},
	} {
		if err := coreKindErr(kinds["cilium-bgppeerconfig"].handler, "cilium-bgppeerconfig", "fast", map[string]any{"timers": timers}); err != nil {
			t.Errorf("timers %s: %v, want it accepted", name, err)
		}
	}

	// spec is the encoded spec of the object a component builds.
	spec := func(component string, props map[string]any) string {
		return fmt.Sprint(policyFreeJSON(t, build(component, props))["spec"])
	}

	cidrs := build("cilium-cidrgroup", full["cilium-cidrgroup"]).(*ciliumv2.CiliumCIDRGroup)
	if got := cidrs.Spec.ExternalCIDRs; len(got) != 2 || got[0] != "192.0.2.0/24" || got[1] != "2001:db8::/32" {
		t.Errorf("externalCIDRs = %v, want the two authored ones in order", got)
	}
	// An authored empty list is written as one, not as a null.
	if got, want := spec("cilium-cidrgroup", map[string]any{"externalCIDRs": []any{}}), "map[externalCIDRs:[]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}

	pool := build("cilium-loadbalancerippool", full["cilium-loadbalancerippool"]).(*ciliumv2.CiliumLoadBalancerIPPool)
	if blocks := pool.Spec.Blocks; pool.Spec.AllowFirstLastIPs != ciliumv2.AllowFirstLastIPNo || !pool.Spec.Disabled ||
		len(blocks) != 2 || blocks[0].Cidr != "192.0.2.0/24" || blocks[1].Start != "198.51.100.10" || blocks[1].Stop != "198.51.100.20" {
		t.Errorf("spec = %+v, want the authored No, disabled and the two blocks in order", pool.Spec)
	}
	// A pool that authors nothing carries nothing, and an authored
	// `disabled: false`, the API's default, is left out as an unauthored one is.
	for name, props := range map[string]map[string]any{"nothing": {}, "disabled false": {"disabled": false}} {
		if got, want := spec("cilium-loadbalancerippool", props), "map[]"; got != want {
			t.Errorf("a pool that authors %s: spec = %s, want %s", name, got, want)
		}
	}
	// An authored empty selector is every Service, as an unauthored one is;
	// the object says which was written.
	if every := build("cilium-loadbalancerippool", map[string]any{"serviceSelector": map[string]any{}}).(*ciliumv2.CiliumLoadBalancerIPPool); every.Spec.ServiceSelector == nil {
		t.Error("serviceSelector = nil, want the authored empty selector")
	}

	egress := build("cilium-egressgatewaypolicy", full["cilium-egressgatewaypolicy"]).(*ciliumv2.CiliumEgressGatewayPolicy)
	if got := egress.Spec.Selectors; len(got) != 2 || got[0].PodSelector == nil || got[0].NamespaceSelector == nil || got[0].NodeSelector != nil || got[1].NodeSelector == nil {
		t.Errorf("selectors = %+v, want the two authored rules in order, each with the selectors it authors", got)
	}
	if got := egress.Spec.EgressGateway; got == nil || got.NodeSelector == nil || got.Interface != "eth1" || got.EgressIP != "" {
		t.Errorf("egressGateway = %+v, want the authored node selector and interface", got)
	}
	if got := egress.Spec.EgressGateways; len(got) != 2 || got[0].EgressIP != "198.51.100.7" || got[1].NodeSelector == nil {
		t.Errorf("egressGateways = %+v, want the two authored ones in order, the second with its empty node selector", got)
	}
	// The least the kind takes is written as authored: the empty lists as
	// lists and the empty node selector as one. The gateway list the API
	// defaults to an empty one is left out.
	if got, want := spec("cilium-egressgatewaypolicy", egressGatewayPolicy()), "map[destinationCIDRs:[] egressGateway:map[nodeSelector:map[]] selectors:[]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}
	// The form of an address is the API server's to refuse: the CRD's rule on
	// egressIP is not checked here.
	if err := coreKindErr(kinds["cilium-egressgatewaypolicy"].handler, "cilium-egressgatewaypolicy", "fast", withProperty(egressGatewayPolicy(), "egressGateway", map[string]any{
		"nodeSelector": map[string]any{}, "egressIP": "not-an-address",
	})); err != nil {
		t.Errorf("an egressIP that is no address: %v, want it accepted", err)
	}

	redirect := build("cilium-localredirectpolicy", full["cilium-localredirectpolicy"]).(*ciliumv2.CiliumLocalRedirectPolicy)
	address := redirect.Spec.RedirectFrontend.AddressMatcher
	if address == nil || address.IP != "169.254.169.254" || len(address.ToPorts) != 2 || address.ToPorts[0].Port != "80" || address.ToPorts[1].Name != "https" {
		t.Errorf("addressMatcher = %+v, want the authored address and its two ports in order", address)
	}
	if backend := redirect.Spec.RedirectBackend; len(backend.ToPorts) != 2 || backend.ToPorts[0].Protocol != "TCP" || backend.ToPorts[1].Port != "8443" || !redirect.Spec.SkipRedirectFromBackend {
		t.Errorf("spec = %+v, want the two authored backend ports in order and skipRedirectFromBackend", redirect.Spec)
	}
	// A frontend may match a Service instead, with no port: every port of the
	// Service. The backend's empty selector is written as one, and an authored
	// `skipRedirectFromBackend: false`, the API's default, is left out.
	byService := withProperty(redirectPolicy(redirectService(), redirectBackend()), "skipRedirectFromBackend", false)
	if got, want := spec("cilium-localredirectpolicy", byService), "map[redirectBackend:map[localEndpointSelector:map[] toPorts:[map[port:8080 protocol:TCP]]] redirectFrontend:map[serviceMatcher:map[namespace:kube-system serviceName:kube-dns]]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}
	// The Service's namespace is the author's: Cilium holds it to the policy's
	// own, launcher does not.
	if err := coreKindErr(kinds["cilium-localredirectpolicy"].handler, "cilium-localredirectpolicy", "fast", redirectPolicy(redirectService(), redirectBackend())); err != nil {
		t.Errorf("a Service in another namespace than the build's: %v, want it accepted", err)
	}

	node := build("cilium-nodeconfig", full["cilium-nodeconfig"]).(*ciliumv2.CiliumNodeConfig)
	if want := map[string]string{"bpf-map-dynamic-size-ratio": "0.005", "enable-hubble": "false"}; !maps.Equal(node.Spec.Defaults, want) {
		t.Errorf("defaults = %v, want the authored %v", node.Spec.Defaults, want)
	}
	// The least the kind takes is written as authored: an empty map of
	// defaults and an empty selector, which is every node.
	if got, want := spec("cilium-nodeconfig", map[string]any{"defaults": map[string]any{}, "nodeSelector": map[string]any{}}), "map[defaults:map[] nodeSelector:map[]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}
	// A key of the configuration is not read: one Cilium does not know builds.
	if err := coreKindErr(kinds["cilium-nodeconfig"].handler, "cilium-nodeconfig", "fast", map[string]any{
		"defaults": map[string]any{"no-such-option": "true"}, "nodeSelector": map[string]any{},
	}); err != nil {
		t.Errorf("a configuration key Cilium does not know: %v, want it accepted", err)
	}

	slice := build("endpointslice", full["endpointslice"]).(*discoveryv1.EndpointSlice)
	if slice.AddressType != discoveryv1.AddressTypeIPv4 {
		t.Errorf("addressType = %q, want IPv4", slice.AddressType)
	}
	// An authored false is kept where the API reads an unset condition as true.
	if c := slice.Endpoints[0].Conditions; c.Ready == nil || *c.Ready || c.Serving == nil || !*c.Serving || c.Terminating == nil || *c.Terminating {
		t.Errorf("conditions = %+v, want the authored ready false, serving true and terminating false", c)
	}
	if !slices.Equal(slice.Endpoints[1].Addresses, []string{"192.0.2.11", "192.0.2.12"}) {
		t.Errorf("addresses = %v, want them in authored order", slice.Endpoints[1].Addresses)
	}
	if p := slice.Ports[0]; p.Port == nil || *p.Port != 8080 || p.Protocol == nil || *p.Protocol != corev1.ProtocolTCP {
		t.Errorf("port = %+v, want the authored 8080 over TCP", p)
	}
	// The API requires neither list. The type always encodes both, so a slice
	// that authors none carries endpoints: null and ports: null, and an
	// endpoint that authors no condition carries conditions: {}.
	bareSlice := policyFreeJSON(t, build("endpointslice", map[string]any{"addressType": "FQDN"}))
	for _, list := range []string{"endpoints", "ports"} {
		if got, ok := bareSlice[list]; !ok || got != nil {
			t.Errorf("%s = %v (present: %v), want null on a slice that authors none", list, got, ok)
		}
	}
	// An authored empty list is written as one.
	emptied := policyFreeJSON(t, build("endpointslice", map[string]any{"addressType": "IPv4", "endpoints": []any{}, "ports": []any{}}))
	if got, want := fmt.Sprint(emptied["endpoints"], emptied["ports"]), "[] []"; got != want {
		t.Errorf("endpoints and ports = %s, want %s: the authored empty lists", got, want)
	}
	unconditioned := policyFreeJSON(t, build("endpointslice", endpointSlice(map[string]any{"addresses": []any{"192.0.2.10"}})))
	if got, want := fmt.Sprint(unconditioned["endpoints"]), "[map[addresses:[192.0.2.10] conditions:map[]]]"; got != want {
		t.Errorf("endpoints = %s, want %s", got, want)
	}
	// The form of a value is the API server's to judge: an address that is not
	// of the slice's type builds, and so does a required field authored empty.
	if err := coreKindErr(kinds["endpointslice"].handler, "endpointslice", "fast", map[string]any{
		"addressType": "IPv6", "endpoints": []any{map[string]any{"addresses": []any{"db.example.com"}}},
	}); err != nil {
		t.Errorf("an address that is not of the slice's type: %v, want it accepted", err)
	}
	if err := coreKindErr(kinds["endpointslice"].handler, "endpointslice", "fast", map[string]any{
		"addressType": "", "endpoints": []any{map[string]any{"addresses": []any{}}},
	}); err != nil {
		t.Errorf("an empty address type and an empty address list: %v, want them accepted", err)
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
