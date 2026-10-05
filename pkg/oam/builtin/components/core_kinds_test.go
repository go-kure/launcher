package components_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	fluxoperatorv1 "github.com/controlplaneio-fluxcd/flux-operator/api/v1"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	metallbv1beta2 "go.universe.tf/metallb/api/v1beta2"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of go-kure/launcher#790 that project a Kubernetes core
// object: each publishes the json fields of the object's spec type, decodes
// them strictly, and emits one object carrying identity and the authored spec.

// coreKindSchemas lists those components with the upstream spec type each
// projects. excluded names the json fields of the type the component refuses,
// each with its reason; every other field is authorable. The kinds of the
// APIs that are not built in are held here too: each projects the top-level
// fields of its spec type the same way. A kind with no spec type (the four
// classes of storage, scheduling and node, the four kinds of the RBAC API)
// names its object's type: the component projects the object, less its
// identity.
//
// The rows stand in the order of their component type, as sort.Strings gives
// it, and a new kind's row goes at its position: TestKindLists_InOrder
// (pkg/cmd/kurel) holds it.
//
// Every registered component type has its row here, or its reason for having
// none in kindListExceptions (pkg/cmd/kurel): TestKindLists_Complete holds
// that, so a new kind is given one of the two.
var coreKindSchemas = []struct {
	component string
	typ       reflect.Type
	handler   interface {
		PropertySchema() map[string]oam.PropertySchema
	}
	excluded map[string]string
}{
	{"artifactgenerator", reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](), &components.ArtifactGeneratorHandler{}, nil},
	{"backendtlspolicy", reflect.TypeFor[gatewayv1.BackendTLSPolicySpec](), &components.BackendTLSPolicyHandler{}, nil},
	{"certificate", reflect.TypeFor[certv1.CertificateSpec](), &components.CertificateHandler{}, nil},
	{"cilium-bgpadvertisement", reflect.TypeFor[ciliumv2.CiliumBGPAdvertisementSpec](), &components.CiliumBGPAdvertisementHandler{}, nil},
	{"cilium-bgpclusterconfig", reflect.TypeFor[ciliumv2.CiliumBGPClusterConfigSpec](), &components.CiliumBGPClusterConfigHandler{}, nil},
	{"cilium-bgpnodeconfigoverride", reflect.TypeFor[ciliumv2.CiliumBGPNodeConfigOverrideSpec](), &components.CiliumBGPNodeConfigOverrideHandler{}, nil},
	{"cilium-bgppeerconfig", reflect.TypeFor[ciliumv2.CiliumBGPPeerConfigSpec](), &components.CiliumBGPPeerConfigHandler{}, nil},
	{"cilium-cidrgroup", reflect.TypeFor[ciliumv2.CiliumCIDRGroupSpec](), &components.CiliumCIDRGroupHandler{}, nil},
	// A CiliumClusterwideNetworkPolicy has no spec type either: `spec` and
	// `specs`, as the CiliumNetworkPolicy.
	{"cilium-clusterwidenetworkpolicy", reflect.TypeFor[ciliumv2.CiliumClusterwideNetworkPolicy](), &components.CiliumClusterwideNetworkPolicyHandler{}, map[string]string{
		"kind":       "launcher emits a cilium.io/v2 CiliumClusterwideNetworkPolicy; the object's type is not authored",
		"apiVersion": "launcher emits a cilium.io/v2 CiliumClusterwideNetworkPolicy; the object's type is not authored",
		"metadata":   "launcher sets the object's name, as on every kind component",
		"status":     "the Cilium agent writes it",
	}},
	{"cilium-egressgatewaypolicy", reflect.TypeFor[ciliumv2.CiliumEgressGatewayPolicySpec](), &components.CiliumEgressGatewayPolicyHandler{}, nil},
	{"cilium-loadbalancerippool", reflect.TypeFor[ciliumv2.CiliumLoadBalancerIPPoolSpec](), &components.CiliumLoadBalancerIPPoolHandler{}, nil},
	{"cilium-localredirectpolicy", reflect.TypeFor[ciliumv2.CiliumLocalRedirectPolicySpec](), &components.CiliumLocalRedirectPolicyHandler{}, nil},
	// A CiliumNetworkPolicy has no spec type: it holds one rule under `spec` and
	// a list of them under `specs`, and the component projects those two fields.
	{"cilium-networkpolicy", reflect.TypeFor[ciliumv2.CiliumNetworkPolicy](), &components.CiliumNetworkPolicyHandler{}, map[string]string{
		"kind":       "launcher emits a cilium.io/v2 CiliumNetworkPolicy; the object's type is not authored",
		"apiVersion": "launcher emits a cilium.io/v2 CiliumNetworkPolicy; the object's type is not authored",
		"metadata":   "launcher sets the object's name and namespace, as on every kind component",
		"status":     "the Cilium agent writes it",
	}},
	{"cilium-nodeconfig", reflect.TypeFor[ciliumv2.CiliumNodeConfigSpec](), &components.CiliumNodeConfigHandler{}, nil},
	{"clusterexternalsecret", reflect.TypeFor[esv1.ClusterExternalSecretSpec](), &components.ClusterExternalSecretHandler{}, nil},
	// A ClusterIssuer and an Issuer share one spec type.
	{"clusterissuer", reflect.TypeFor[certv1.IssuerSpec](), &components.ClusterIssuerHandler{}, nil},
	{"clusterrole", reflect.TypeFor[rbacv1.ClusterRole](), &components.ClusterRoleHandler{}, objectIdentityExcluded("a rbac.authorization.k8s.io/v1 ClusterRole")},
	{"clusterrolebinding", reflect.TypeFor[rbacv1.ClusterRoleBinding](), &components.ClusterRoleBindingHandler{}, objectIdentityExcluded("a rbac.authorization.k8s.io/v1 ClusterRoleBinding")},
	// A ClusterSecretStore and a SecretStore hold one spec type.
	{"clustersecretstore", reflect.TypeFor[esv1.SecretStoreSpec](), &components.ClusterSecretStoreHandler{}, nil},
	{"cnpg-backup", reflect.TypeFor[cnpgv1.BackupSpec](), &components.CnpgBackupHandler{}, nil},
	// A ClusterImageCatalog and an ImageCatalog share one spec type.
	{"cnpg-clusterimagecatalog", reflect.TypeFor[cnpgv1.ImageCatalogSpec](), &components.CnpgClusterImageCatalogHandler{}, nil},
	{"cnpg-databaserole", reflect.TypeFor[cnpgv1.DatabaseRoleSpec](), &components.CnpgDatabaseRoleHandler{}, nil},
	{"cnpg-imagecatalog", reflect.TypeFor[cnpgv1.ImageCatalogSpec](), &components.CnpgImageCatalogHandler{}, nil},
	{"cnpg-publication", reflect.TypeFor[cnpgv1.PublicationSpec](), &components.CnpgPublicationHandler{}, nil},
	{"cnpg-scheduledbackup", reflect.TypeFor[cnpgv1.ScheduledBackupSpec](), &components.CnpgScheduledBackupHandler{}, nil},
	{"cnpg-subscription", reflect.TypeFor[cnpgv1.SubscriptionSpec](), &components.CnpgSubscriptionHandler{}, nil},
	{"csidriver", reflect.TypeFor[storagev1.CSIDriverSpec](), &components.CSIDriverHandler{}, nil},
	// An EndpointSlice has no spec type either: the component projects the
	// object, less its identity.
	{"endpointslice", reflect.TypeFor[discoveryv1.EndpointSlice](), &components.EndpointSliceHandler{}, namespacedObjectIdentityExcluded("a discovery.k8s.io/v1 EndpointSlice")},
	{"externalsecret", reflect.TypeFor[esv1.ExternalSecretSpec](), &components.ExternalSecretHandler{}, nil},
	{"fluxcd-alert", reflect.TypeFor[notificationv1beta3.AlertSpec](), &components.FluxcdAlertHandler{}, nil},
	{"fluxcd-provider", reflect.TypeFor[notificationv1beta3.ProviderSpec](), &components.FluxcdProviderHandler{}, nil},
	{"fluxcd-receiver", reflect.TypeFor[notificationv1.ReceiverSpec](), &components.FluxcdReceiverHandler{}, nil},
	{"gateway", reflect.TypeFor[gatewayv1.GatewaySpec](), &components.GatewayHandler{}, nil},
	{"gatewayclass", reflect.TypeFor[gatewayv1.GatewayClassSpec](), &components.GatewayClassHandler{}, nil},
	{"grpcroute", reflect.TypeFor[gatewayv1.GRPCRouteSpec](), &components.GRPCRouteHandler{}, nil},
	{"horizontalpodautoscaler", reflect.TypeFor[autoscalingv2.HorizontalPodAutoscalerSpec](), &components.HorizontalPodAutoscalerHandler{}, nil},
	{"httproute", reflect.TypeFor[gatewayv1.HTTPRouteSpec](), &components.HTTPRouteHandler{}, nil},
	{"imagepolicy", reflect.TypeFor[imagev1.ImagePolicySpec](), &components.ImagePolicyHandler{}, nil},
	{"imagerepository", reflect.TypeFor[imagev1.ImageRepositorySpec](), &components.ImageRepositoryHandler{}, nil},
	{"imageupdateautomation", reflect.TypeFor[autov1.ImageUpdateAutomationSpec](), &components.ImageUpdateAutomationHandler{}, nil},
	{"ingress", reflect.TypeFor[networkingv1.IngressSpec](), &components.IngressHandler{}, nil},
	{"ingressclass", reflect.TypeFor[networkingv1.IngressClassSpec](), &components.IngressClassHandler{}, nil},
	{"issuer", reflect.TypeFor[certv1.IssuerSpec](), &components.IssuerHandler{}, nil},
	{"limitrange", reflect.TypeFor[corev1.LimitRangeSpec](), &components.LimitRangeHandler{}, nil},
	{"listenerset", reflect.TypeFor[gatewayv1.ListenerSetSpec](), &components.ListenerSetHandler{}, nil},
	// And the kinds of MetalLB's API.
	{"metallb-bfdprofile", reflect.TypeFor[metallbv1beta1.BFDProfileSpec](), &components.MetalLBBFDProfileHandler{}, nil},
	{"metallb-bgpadvertisement", reflect.TypeFor[metallbv1beta1.BGPAdvertisementSpec](), &components.MetalLBBGPAdvertisementHandler{}, nil},
	{"metallb-bgppeer", reflect.TypeFor[metallbv1beta2.BGPPeerSpec](), &components.MetalLBBGPPeerHandler{}, nil},
	{"metallb-community", reflect.TypeFor[metallbv1beta1.CommunitySpec](), &components.MetalLBCommunityHandler{}, nil},
	{"metallb-ipaddresspool", reflect.TypeFor[metallbv1beta1.IPAddressPoolSpec](), &components.MetalLBIPAddressPoolHandler{}, nil},
	{"metallb-l2advertisement", reflect.TypeFor[metallbv1beta1.L2AdvertisementSpec](), &components.MetalLBL2AdvertisementHandler{}, nil},
	{"namespace", reflect.TypeFor[corev1.NamespaceSpec](), &components.NamespaceHandler{}, nil},
	{"networkpolicy", reflect.TypeFor[networkingv1.NetworkPolicySpec](), &components.NetworkPolicyHandler{}, nil},
	{"persistentvolume", reflect.TypeFor[corev1.PersistentVolumeSpec](), &components.PersistentVolumeHandler{}, nil},
	{"pod", reflect.TypeFor[corev1.PodSpec](), &components.PodHandler{}, map[string]string{
		"ephemeralContainers": "a pod cannot be created with ephemeral containers; they are added through its ephemeralcontainers subresource",
		"priority":            "the Priority admission controller derives it from priorityClassName and rejects a differing value",
		"overhead":            "the RuntimeClass admission controller derives it from the RuntimeClass and rejects a differing value",
	}},
	{"poddisruptionbudget", reflect.TypeFor[policyv1.PodDisruptionBudgetSpec](), &components.PodDisruptionBudgetHandler{}, nil},
	{"podmonitor", reflect.TypeFor[monitoringv1.PodMonitorSpec](), &components.PodMonitorHandler{}, nil},
	// A PodTemplate has no spec type: the component projects the object, less
	// its identity.
	{"podtemplate", reflect.TypeFor[corev1.PodTemplate](), &components.PodTemplateHandler{}, map[string]string{
		"kind":       "launcher emits a v1 PodTemplate; the object's type is not authored",
		"apiVersion": "launcher emits a v1 PodTemplate; the object's type is not authored",
		"metadata":   "launcher sets the object's name and namespace, as on every kind component; the pods' metadata is template.metadata",
	}},
	{"priorityclass", reflect.TypeFor[schedulingv1.PriorityClass](), &components.PriorityClassHandler{}, objectIdentityExcluded("a scheduling.k8s.io/v1 PriorityClass")},
	{"prometheus-probe", reflect.TypeFor[monitoringv1.ProbeSpec](), &components.PrometheusProbeHandler{}, nil},
	{"prometheusrule", reflect.TypeFor[monitoringv1.PrometheusRuleSpec](), &components.PrometheusRuleHandler{}, nil},
	{"referencegrant", reflect.TypeFor[gatewayv1.ReferenceGrantSpec](), &components.ReferenceGrantHandler{}, nil},
	{"replicaset", reflect.TypeFor[appsv1.ReplicaSetSpec](), &components.ReplicaSetHandler{}, nil},
	{"replicationcontroller", reflect.TypeFor[corev1.ReplicationControllerSpec](), &components.ReplicationControllerHandler{}, nil},
	{"replicationdestination", reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec](), &components.ReplicationDestinationHandler{}, nil},
	{"replicationsource", reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec](), &components.ReplicationSourceHandler{}, nil},
	{"resourcequota", reflect.TypeFor[corev1.ResourceQuotaSpec](), &components.ResourceQuotaHandler{}, nil},
	{"resourcesetinputprovider", reflect.TypeFor[fluxoperatorv1.ResourceSetInputProviderSpec](), &components.ResourceSetInputProviderHandler{}, nil},
	{"role", reflect.TypeFor[rbacv1.Role](), &components.RoleHandler{}, namespacedObjectIdentityExcluded("a rbac.authorization.k8s.io/v1 Role")},
	{"rolebinding", reflect.TypeFor[rbacv1.RoleBinding](), &components.RoleBindingHandler{}, namespacedObjectIdentityExcluded("a rbac.authorization.k8s.io/v1 RoleBinding")},
	{"runtimeclass", reflect.TypeFor[nodev1.RuntimeClass](), &components.RuntimeClassHandler{}, objectIdentityExcluded("a node.k8s.io/v1 RuntimeClass")},
	{"secretstore", reflect.TypeFor[esv1.SecretStoreSpec](), &components.SecretStoreHandler{}, nil},
	{"servicecidr", reflect.TypeFor[networkingv1.ServiceCIDRSpec](), &components.ServiceCIDRHandler{}, nil},
	{"servicemonitor", reflect.TypeFor[monitoringv1.ServiceMonitorSpec](), &components.ServiceMonitorHandler{}, nil},
	{"storageclass", reflect.TypeFor[storagev1.StorageClass](), &components.StorageClassHandler{}, objectIdentityExcluded("a storage.k8s.io/v1 StorageClass")},
	{"tcproute", reflect.TypeFor[gatewayv1.TCPRouteSpec](), &components.TCPRouteHandler{}, nil},
	{"tlsroute", reflect.TypeFor[gatewayv1.TLSRouteSpec](), &components.TLSRouteHandler{}, nil},
	{"udproute", reflect.TypeFor[gatewayv1.UDPRouteSpec](), &components.UDPRouteHandler{}, nil},
	{"volumeattributesclass", reflect.TypeFor[storagev1.VolumeAttributesClass](), &components.VolumeAttributesClassHandler{}, objectIdentityExcluded("a storage.k8s.io/v1 VolumeAttributesClass")},
}

// coreKindHiddenFields names, per component, the Go fields of its type that no
// property reaches because the type declares another field under the same json
// name less deeply, each with what stands in its place. Every other field of a
// kind's type must be reachable through the strict decode.
var coreKindHiddenFields = map[string]map[string]string{
	"prometheus-probe": {
		"HTTPConfig.HTTPConfigWithoutTLS.Authorization": "ProbeSpec declares authorization itself, of the same type; the one its HTTP settings embed is hidden by it",
	},
}

// objectIdentityExcluded is the excluded set of a kind that projects a whole
// cluster-scoped object, emitted as the given kind: its identity is launcher's.
func objectIdentityExcluded(emits string) map[string]string {
	return map[string]string{
		"kind":       "launcher emits " + emits + "; the object's type is not authored",
		"apiVersion": "launcher emits " + emits + "; the object's type is not authored",
		"metadata":   "launcher sets the object's name, as on every kind component",
	}
}

// namespacedObjectIdentityExcluded is objectIdentityExcluded for a kind that
// projects a whole namespaced object.
func namespacedObjectIdentityExcluded(emits string) map[string]string {
	excluded := objectIdentityExcluded(emits)
	excluded["metadata"] = "launcher sets the object's name and namespace, as on every kind component"
	return excluded
}

// checkCoreKindProperty holds one published property to the Go type it decodes
// into: the same PropertyType, a description, an open object for a structured
// value (the strict decode checks its content), and for a list an item schema
// held to the element type the same way.
func checkCoreKindProperty(t *testing.T, key string, prop oam.PropertySchema, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	// An int-or-string decodes from a number or a string, so its schema is the
	// union of the two and declares no single type.
	if typ == reflect.TypeFor[intstr.IntOrString]() {
		if want := []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString}; prop.Type != "" || !slices.Equal(prop.Types, want) {
			t.Errorf("schema key %q declares type %q and types %v, but the field is an int-or-string (want no type and types %v)", key, prop.Type, prop.Types, want)
		}
		if prop.Description == "" {
			t.Errorf("schema key %q has no description", key)
		}
		return
	}
	// A quantity decodes from a number or a string too, and a number need not
	// be whole.
	if typ == reflect.TypeFor[resource.Quantity]() {
		if want := []oam.PropertyType{oam.PropertyTypeNumber, oam.PropertyTypeString}; prop.Type != "" || !slices.Equal(prop.Types, want) {
			t.Errorf("schema key %q declares type %q and types %v, but the field is a quantity (want no type and types %v)", key, prop.Type, prop.Types, want)
		}
		if prop.Description == "" {
			t.Errorf("schema key %q has no description", key)
		}
		return
	}
	if len(prop.Types) != 0 {
		t.Errorf("schema key %q declares the union %v, but the field is %s, which decodes from one type", key, prop.Types, typ)
		return
	}
	// A duration is a struct that decodes from a string ("2160h") and from
	// nothing else, and so is a time ("2030-01-01T00:00:00Z").
	if typ == reflect.TypeFor[metav1.Duration]() || typ == reflect.TypeFor[metav1.Time]() {
		if prop.Type != oam.PropertyTypeString {
			t.Errorf("schema key %q declares type %q, but the field is a %s, which decodes from a string", key, prop.Type, typ.Name())
		}
		if prop.Description == "" {
			t.Errorf("schema key %q has no description", key)
		}
		return
	}
	if want := schemaTypeForGo(typ); prop.Type != want {
		t.Errorf("schema key %q declares type %q, but the field is %s (want %q)", key, prop.Type, typ, want)
		return
	}
	if prop.Description == "" {
		t.Errorf("schema key %q has no description", key)
	}
	switch prop.Type {
	case oam.PropertyTypeObject:
		if !prop.AdditionalProperties {
			t.Errorf("schema key %q is a closed object; structured fields are open objects checked by the strict decode", key)
		}
	case oam.PropertyTypeArray:
		if prop.Items == nil {
			t.Errorf("schema key %q declares no item schema", key)
			return
		}
		checkCoreKindProperty(t, key+"[]", *prop.Items, typ.Elem())
	case oam.PropertyTypeString, oam.PropertyTypeInteger, oam.PropertyTypeBoolean, oam.PropertyTypeNumber:
		// A scalar has no structure to hold beyond its type and description.
	}
}

// TestCoreKindSchemas_CoverSpec is TestCnpgKindSchemas_CoverSpec for the core
// kinds: the schema publishes exactly the spec type's json fields, less the
// excluded ones, each with the type its Go field decodes from. A dependency
// bump that adds, removes or retypes a top-level field fails here, naming it.
func TestCoreKindSchemas_CoverSpec(t *testing.T) {
	for _, tt := range coreKindSchemas {
		t.Run(tt.component, func(t *testing.T) {
			fields := specJSONFields(t, tt.typ)
			schema := tt.handler.PropertySchema()
			for _, name := range slices.Sorted(maps.Keys(fields)) {
				reason, excluded := tt.excluded[name]
				prop, published := schema[name]
				switch {
				case published && excluded:
					t.Errorf("%s field %q is both published in the %s schema and excluded (%q); pick one", tt.typ, name, tt.component, reason)
				case !published && !excluded:
					t.Errorf("%s field %q is neither published in the %s schema nor excluded with a reason", tt.typ, name, tt.component)
				case published:
					checkCoreKindProperty(t, name, prop, fields[name])
				}
			}
			for _, key := range slices.Sorted(maps.Keys(schema)) {
				if _, ok := fields[key]; !ok {
					t.Errorf("schema key %q has no %s json field; the strict decode would refuse every value", key, tt.typ)
				}
			}
			for _, name := range slices.Sorted(maps.Keys(tt.excluded)) {
				if _, ok := fields[name]; !ok {
					t.Errorf("excluded field %q is stale: %s has no such field", name, tt.typ)
				}
				if strings.TrimSpace(tt.excluded[name]) == "" {
					t.Errorf("excluded field %q has no reason", name)
				}
			}
		})
	}
}

// TestCoreKindSchemas_EveryFieldReachable is
// TestCnpgClusterSchema_EveryFieldReachable for the core kinds. A field hidden
// by another of the same json name is unreachable by the type's own design; it
// passes only when coreKindHiddenFields names it with what stands in its
// place.
func TestCoreKindSchemas_EveryFieldReachable(t *testing.T) {
	known := map[string]bool{}
	for _, tt := range coreKindSchemas {
		known[tt.component] = true
		hidden := coreKindHiddenFields[tt.component]
		got := builtin.UnreachableJSONFields(tt.typ)
		for _, field := range got {
			if strings.TrimSpace(hidden[field]) == "" {
				t.Errorf("%s field %s is unreachable through the strict decode", tt.typ, field)
			}
		}
		for field := range hidden {
			if !slices.Contains(got, field) {
				t.Errorf("%s: hidden field %s is stale: the strict decode reaches it, or the type has no such field", tt.component, field)
			}
		}
	}
	for component := range coreKindHiddenFields {
		if !known[component] {
			t.Errorf("hidden fields are listed for %q, which is no kind of coreKindSchemas", component)
		}
	}
}

// coreKindNamespace is the build namespace the core-kind tests convert and
// generate in.
const coreKindNamespace = "apps"

// coreKindErr returns the conversion error of one component.
func coreKindErr(h oam.ComponentHandler, typ, name string, props map[string]any) error {
	_, err := h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, coreKindNamespace)
	return err
}

// generateCoreKind converts one component, applies a restrictive policy and no
// policy, and generates. It checks what every core kind promises: exactly one
// object, named after the component, with no annotation of launcher's and no
// label, except the `app` label on a Pod. A kind that holds a pod template
// labels the template, not the object. The caller checks the namespace, which
// depends on the kind's scope.
func generateCoreKind(t *testing.T, h oam.ComponentHandler, typ, name string, props map[string]any) client.Object {
	t.Helper()
	one := int32(1)
	restrictive := &stubPolicy{
		maxReplicas: &one, maxCPU: "1m", maxMemory: "1Ki", maxStorageSize: "1Ki",
		allowedRegistries: []string{"registry.invalid"},
	}
	return generateCoreKindUnder(t, h, typ, name, props, restrictive, nil)
}

// generateCoreKindUnder is generateCoreKind under the given policies, for a
// kind the environment policy does constrain.
func generateCoreKindUnder(t *testing.T, h oam.ComponentHandler, typ, name string, props map[string]any, policies ...oam.Policy) client.Object {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	for _, p := range policies {
		if err := cfg.(policyApplier).ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy(%v): %v", p, err)
		}
	}
	objs, err := cfg.Generate(stack.NewApplication(name, coreKindNamespace, cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate: got %d objects, want 1", len(objs))
	}
	obj := *objs[0]
	if obj.GetName() != name {
		t.Errorf("name = %q, want the component name %q", obj.GetName(), name)
	}
	// The pod kind is the one core kind that labels its object: a Pod is what
	// traits and Services select, so it carries the `app` label.
	var wantLabels map[string]string
	if typ == "pod" {
		wantLabels = map[string]string{"app": oam.ComponentLabelValue(name)}
	}
	if !maps.Equal(obj.GetLabels(), wantLabels) || len(obj.GetAnnotations()) != 0 {
		t.Errorf("labels = %v, annotations = %v; want labels %v and no annotation", obj.GetLabels(), obj.GetAnnotations(), wantLabels)
	}
	return obj
}
