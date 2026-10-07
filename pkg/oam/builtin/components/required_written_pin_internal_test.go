package components

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	fluxoperatorv1 "github.com/controlplaneio-fluxcd/flux-operator/api/v1"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	metallbv1beta2 "go.universe.tf/metallb/api/v1beta2"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The third set of kind_api_sets_internal_test.go: the fields the API requires
// and the Go type writes when nothing was decoded into them ("", 0, {}, [],
// null). A kind refuses the omission of such a field only where its required
// list or its own validation names it; any other one reaches the API server
// written empty, and what the API server or the operator then says of it is
// not the kind's answer. This test does not hold those fields to be refused
// (the mechanism that would is tracked by go-kure/launcher#883). It pins
// which ones are not: the set is
// checked in, one line per field, and a field that joins it (a new gap) or
// leaves it (a new refusal) fails the test until the file is regenerated
// with UPDATE_REQUIRED_WRITTEN_PIN=1.
//
// Each member is measured, not read off a list: for every field the source
// says is required and that the type writes unauthored, the kind builds a
// control (its base properties, the field authored, and every required field
// around it filled with a sample), then the same properties without the field.
// A control the kind refuses at an ancestor that requiredWrittenUnauthorable
// lists means the field cannot be authored on the kind at all, and it is not
// a member. Any other refusal of the control, a refusal of the omission that
// names neither the field nor one under it (unless requiredWrittenDecodeRefused
// lists it), and a member the object does not show fail the test: the
// measurement did not answer for that field.

// requiredWrittenPinFile is the checked-in set, and requiredWrittenPinUpdate
// the switch under which TestKindComponents_RequiredWrittenNotRefused writes
// it.
const (
	requiredWrittenPinFile   = "testdata/required-written-not-refused.txt"
	requiredWrittenPinUpdate = "UPDATE_REQUIRED_WRITTEN_PIN"
)

// requiredWrittenValidDefaults are the members whose written value is one the
// API takes as authored, not an empty one: `instances` 1 is the CRD's own
// default, and `enabled` false, which the CRD requires and gives no default,
// is the valid zero value of the Go type. Of the Flux kinds, `interval` is the
// kind's own default, a verification's `provider` the API's (which the kind
// writes, TestFluxKinds_VerifyProviderDefault), and a Kustomization's `prune`
// false the valid zero value of the Go type. They are left out of the set, by
// kind and path with the value written; one whose omission no longer builds
// with that value written fails the test.
var requiredWrittenValidDefaults = map[string]string{
	"cnpg-cluster instances": "1",
	"cnpg-cluster postgresql.syncReplicaElectionConstraint.enabled": "false",
	"bucket interval":                        `"1h0m0s"`,
	"fluxcd-kustomization interval":          `"1h0m0s"`,
	"fluxcd-kustomization prune":             "false",
	"gitrepository interval":                 `"1h0m0s"`,
	"helmchart interval":                     `"1h0m0s"`,
	"helmchart verify.provider":              `"cosign"`,
	"helmrelease interval":                   `"1h0m0s"`,
	"helmrelease chart.spec.verify.provider": `"cosign"`,
	"ocirepository interval":                 `"1h0m0s"`,
	"ocirepository verify.provider":          `"cosign"`,
}

// requiredWrittenUnauthorable are the paths a kind refuses whatever is
// authored there, by kind and path, each with the reason; a required field
// under one cannot be authored, so it is not measured. A control refused at
// any other ancestor fails the test, and so does an entry the measurement no
// longer meets.
var requiredWrittenUnauthorable = map[string]string{
	"cilium-networkpolicy spec.nodeSelector":                                 "a node selector belongs to a CiliumClusterwideNetworkPolicy",
	"cilium-networkpolicy specs[].nodeSelector":                              "a node selector belongs to a CiliumClusterwideNetworkPolicy",
	"cnpg-pooler template.spec.ephemeralContainers":                          "ephemeral containers cannot be declared on a pod template",
	"externalsecret data[].sourceRef.generatorRef":                           "the object always carries a storeRef there, and the API takes exactly one",
	"clusterexternalsecret externalSecretSpec.data[].sourceRef.generatorRef": "the object always carries a storeRef there, and the API takes exactly one",
}

// requiredWrittenDecodeRefused are the fields whose omission the kind refuses
// in its decode, by kind and path, each with the reason: a type of the API
// decodes itself, and the error names no field. Every other refusal of an
// omission must name the field or one under it; a listed omission that builds,
// or is refused another way, fails the test.
var requiredWrittenDecodeRefused = ciliumPolicyDecodeRefused()

// ciliumPolicyDecodeRefused lists the two policy kinds' decode refusals, under
// spec and under each entry of specs.
func ciliumPolicyDecodeRefused() map[string]string {
	refused := map[string]string{}
	for _, component := range []string{"cilium-networkpolicy", "cilium-clusterwidenetworkpolicy"} {
		for _, prefix := range []string{"spec.", "specs[]."} {
			refused[component+" "+prefix+"labels[].key"] = "Cilium's label type decodes itself and refuses a label without a key"
			for _, rule := range []string{"ingress", "ingressDeny", "egress", "egressDeny"} {
				refused[component+" "+prefix+rule+"[].icmps[].fields[].type"] = "Cilium's ICMP field type decodes itself and fails on an omitted type, which the kind's decode refuses"
			}
		}
	}
	return refused
}

// pinSchema is what the measurement reads of a kind's API: the properties by
// json path, with [] for a list element and {} for a map value, and the
// required ones.
type pinSchema struct {
	props    map[string]apiextensionsv1.JSONSchemaProps
	required map[string]bool
}

// pinKind is one kind component the set is measured on.
type pinKind struct {
	component string
	handler   oam.ComponentHandler
	typ       reflect.Type
	schema    func(*testing.T) pinSchema
	// prefixes are the top-level keys of a component whose properties map onto
	// the CRD's root rather than its spec, each with its separator; nil for
	// the others.
	prefixes []string
	// base is properties that build once the required fields are filled.
	base map[string]any
}

// pinCRDSchema reads the named version of the CRD in file, a path under the
// directory of the linked module, rooted at spec, or at the CRD's root
// restricted to prefixes.
func pinCRDSchema(modulePath, file, version string, prefixes ...string) func(*testing.T) pinSchema {
	return func(t *testing.T) pinSchema {
		t.Helper()
		data, err := os.ReadFile(linkedModuleDir(t, modulePath) + "/" + file)
		if err != nil {
			t.Fatalf("read the CRD: %v", err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("decode the CRD %s: %v", file, err)
		}
		at := slices.IndexFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool { return v.Name == version })
		if at < 0 || crd.Spec.Versions[at].Schema == nil || crd.Spec.Versions[at].Schema.OpenAPIV3Schema == nil {
			t.Fatalf("%s has no schema for the version %s", file, version)
		}
		root := *crd.Spec.Versions[at].Schema.OpenAPIV3Schema
		if prefixes == nil {
			props, required := schemaProperties(root.Properties["spec"])
			return pinSchema{props, required}
		}
		props, required := schemaProperties(root)
		under := func(path string) bool {
			return slices.ContainsFunc(prefixes, func(p string) bool {
				root := strings.TrimSuffix(p, ".")
				return strings.HasPrefix(path, p) || path == root || path == strings.TrimSuffix(root, "[]")
			})
		}
		for path := range props {
			if !under(path) {
				delete(props, path)
				delete(required, path)
			}
		}
		// The roots themselves are the component's own keys, which it may leave
		// out in favour of one another.
		for _, p := range prefixes {
			delete(required, strings.TrimSuffix(strings.TrimSuffix(p, "."), "[]"))
		}
		return pinSchema{props, required}
	}
}

// pinMarkerSchema is the schema of a kind whose module ships no CRD: the
// properties from the Go type, the required ones from the markers of its
// source (markerAPISource, and for the external-secrets API its own reader).
func pinMarkerSchema(typ reflect.Type, externalSecrets bool) func(*testing.T) pinSchema {
	return func(t *testing.T) pinSchema {
		t.Helper()
		src := markerAPISource(t)
		if externalSecrets {
			es := readExternalSecretsSource(t)
			k8s := src
			src = apiSource{
				required: func(f kindField) bool {
					if strings.HasPrefix(f.owner.PkgPath(), externalSecretsModulePath+"/") {
						return es.required(f)
					}
					return k8s.required(f) && k8s.known(f)
				},
				known: func(f kindField) bool {
					if strings.HasPrefix(f.owner.PkgPath(), externalSecretsModulePath+"/") {
						_, ok := es.markers(f)
						return ok
					}
					return k8s.known(f)
				},
			}
		}
		schema := pinSchema{props: map[string]apiextensionsv1.JSONSchemaProps{}, required: map[string]bool{}}
		walkKindFields(typ, src.required, func(f kindField) {
			schema.props[f.path] = goTypeProps(f.field.Type)
			if src.known(f) && src.required(f) {
				schema.required[f.path] = true
			}
		})
		return schema
	}
}

// goTypeProps is the schema a CRD generator gives a Go type, as far as a
// sample of it needs: its type, the item type of a list, the format of a
// duration and the shape of a quantity or an int-or-string.
func goTypeProps(typ reflect.Type) apiextensionsv1.JSONSchemaProps {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ {
	case reflect.TypeFor[intstr.IntOrString]():
		return apiextensionsv1.JSONSchemaProps{XIntOrString: true}
	case reflect.TypeFor[resource.Quantity]():
		return apiextensionsv1.JSONSchemaProps{Type: "string", Pattern: "^[0-9]"}
	case reflect.TypeFor[metav1.Duration]():
		return apiextensionsv1.JSONSchemaProps{Type: "string", Format: "duration"}
	case reflect.TypeFor[metav1.Time]():
		return apiextensionsv1.JSONSchemaProps{Type: "string", Format: "date-time"}
	}
	switch typ.Kind() {
	case reflect.String:
		return apiextensionsv1.JSONSchemaProps{Type: "string"}
	case reflect.Bool:
		return apiextensionsv1.JSONSchemaProps{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return apiextensionsv1.JSONSchemaProps{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return apiextensionsv1.JSONSchemaProps{Type: "number"}
	case reflect.Slice, reflect.Array:
		item := goTypeProps(typ.Elem())
		return apiextensionsv1.JSONSchemaProps{Type: "array", Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &item}}
	default:
		return apiextensionsv1.JSONSchemaProps{Type: "object"}
	}
}

// requiredWrittenKinds are the kind components the set is measured on: every
// kind whose API is a CRD a linked module ships, and the kinds of the APIs
// whose markers a reader of this package reads (the Prometheus operator's, the
// Flux controllers' and the External Secrets Operator's).
// TestRequiredWrittenKinds_CoverEveryCRDKind holds it to the kind inventory.
var requiredWrittenKinds = []pinKind{
	{component: "issuer", handler: &IssuerHandler{}, typ: reflect.TypeFor[certv1.IssuerSpec](),
		schema: pinCRDSchema(certManagerModulePath, certManagerCRDs+"issuers.yaml", "v1"),
		base:   map[string]any{"selfSigned": map[string]any{}}},
	{component: "clusterissuer", handler: &ClusterIssuerHandler{}, typ: reflect.TypeFor[certv1.IssuerSpec](),
		schema: pinCRDSchema(certManagerModulePath, certManagerCRDs+"clusterissuers.yaml", "v1"),
		base:   map[string]any{"selfSigned": map[string]any{}}},
	{component: "certificate", handler: &CertificateHandler{}, typ: reflect.TypeFor[certv1.CertificateSpec](),
		schema: pinCRDSchema(certManagerModulePath, certManagerCRDs+"certificates.yaml", "v1"),
		base:   map[string]any{"secretName": "web-tls", "issuerRef": map[string]any{"name": "ca"}, "dnsNames": []any{"a.example.com"}}},
	{component: "replicationsource", handler: &ReplicationSourceHandler{}, typ: reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec](),
		schema: pinCRDSchema(volsyncModulePath, volsyncCRDs+"replicationsources.yaml", "v1alpha1"),
		base:   map[string]any{"sourcePVC": "data", "trigger": map[string]any{"manual": "now"}, "rsync": map[string]any{"copyMethod": "Direct", "address": "a.example.com"}}},
	{component: "replicationdestination", handler: &ReplicationDestinationHandler{}, typ: reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec](),
		schema: pinCRDSchema(volsyncModulePath, volsyncCRDs+"replicationdestinations.yaml", "v1alpha1"),
		base:   map[string]any{"trigger": map[string]any{"manual": "now"}, "rsync": map[string]any{"copyMethod": "Direct", "destinationPVC": "data"}}},
	{component: "cilium-bgpadvertisement", handler: &CiliumBGPAdvertisementHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumBGPAdvertisementSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumbgpadvertisements.yaml", "v2"),
		// A selector is allowed with CiliumPodIPPool, not with PodCIDR.
		base: map[string]any{"advertisements": []any{map[string]any{"advertisementType": "CiliumPodIPPool"}}}},
	{component: "cilium-bgpclusterconfig", handler: &CiliumBGPClusterConfigHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumBGPClusterConfigSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumbgpclusterconfigs.yaml", "v2")},
	{component: "cilium-bgpnodeconfigoverride", handler: &CiliumBGPNodeConfigOverrideHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumBGPNodeConfigOverrideSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumbgpnodeconfigoverrides.yaml", "v2")},
	{component: "cilium-bgppeerconfig", handler: &CiliumBGPPeerConfigHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumBGPPeerConfigSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumbgppeerconfigs.yaml", "v2")},
	{component: "cilium-networkpolicy", handler: &CiliumNetworkPolicyHandler{}, typ: reflect.TypeFor[ciliumapi.Rule](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumnetworkpolicies.yaml", "v2", "spec.", "specs[]."), prefixes: []string{"spec.", "specs[]."},
		base: map[string]any{"spec": ciliumPinRule(), "specs": []any{ciliumPinRule()}}},
	{component: "cilium-clusterwidenetworkpolicy", handler: &CiliumClusterwideNetworkPolicyHandler{}, typ: reflect.TypeFor[ciliumapi.Rule](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumclusterwidenetworkpolicies.yaml", "v2", "spec.", "specs[]."), prefixes: []string{"spec.", "specs[]."},
		base: map[string]any{"spec": ciliumPinRule(), "specs": []any{ciliumPinRule()}}},
	{component: "cilium-cidrgroup", handler: &CiliumCIDRGroupHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumCIDRGroupSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumcidrgroups.yaml", "v2"),
		base:   map[string]any{"externalCIDRs": []any{"10.0.0.0/8"}}},
	{component: "cilium-loadbalancerippool", handler: &CiliumLoadBalancerIPPoolHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumLoadBalancerIPPoolSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumloadbalancerippools.yaml", "v2"),
		base:   map[string]any{"blocks": []any{map[string]any{"cidr": "10.0.0.0/24"}}}},
	{component: "cilium-egressgatewaypolicy", handler: &CiliumEgressGatewayPolicyHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumEgressGatewayPolicySpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumegressgatewaypolicies.yaml", "v2"),
		base: map[string]any{
			"destinationCIDRs": []any{"0.0.0.0/0"}, "selectors": []any{map[string]any{"podSelector": map[string]any{}}},
			"egressGateway": map[string]any{"nodeSelector": map[string]any{}, "egressIP": "10.0.0.1"},
		}},
	{component: "cilium-localredirectpolicy", handler: &CiliumLocalRedirectPolicyHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumLocalRedirectPolicySpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumlocalredirectpolicies.yaml", "v2"),
		base: map[string]any{
			"redirectFrontend": map[string]any{"addressMatcher": map[string]any{"ip": "169.254.169.254", "toPorts": []any{map[string]any{"port": "80", "protocol": "TCP"}}}},
			"redirectBackend":  map[string]any{"localEndpointSelector": map[string]any{}, "toPorts": []any{map[string]any{"port": "8080", "protocol": "TCP"}}},
		}},
	{component: "cilium-nodeconfig", handler: &CiliumNodeConfigHandler{}, typ: reflect.TypeFor[ciliumv2.CiliumNodeConfigSpec](),
		schema: pinCRDSchema(ciliumBGPModulePath, ciliumCRDs+"ciliumnodeconfigs.yaml", "v2"),
		base:   map[string]any{"defaults": map[string]any{"a": "b"}, "nodeSelector": map[string]any{}}},
	{component: "cnpg-pooler", handler: &CnpgPoolerHandler{}, typ: reflect.TypeFor[cnpgv1.PoolerSpec](),
		schema: pinCRDSchema(cnpgModulePath, cnpgCRDs+"poolers.yaml", "v1"),
		base:   map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}}},
	{component: "cnpg-database", handler: &CnpgDatabaseHandler{}, typ: reflect.TypeFor[cnpgv1.DatabaseSpec](),
		schema: pinCRDSchema(cnpgModulePath, cnpgCRDs+"databases.yaml", "v1"),
		base:   map[string]any{"cluster": map[string]any{"name": "db"}, "name": "app", "owner": "app"}},
	{component: "cnpg-objectstore", handler: &CnpgObjectStoreHandler{}, typ: reflect.TypeFor[barmanv1.ObjectStoreSpec](),
		schema: pinCRDSchema(barmanCloudModulePath, "config/crd/bases/barmancloud.cnpg.io_objectstores.yaml", "v1"),
		base:   map[string]any{"configuration": map[string]any{"destinationPath": "s3://bucket/path", "s3Credentials": map[string]any{"inheritFromIAMRole": true}}}},
	{component: "cnpg-cluster", handler: &CnpgClusterHandler{}, typ: reflect.TypeFor[cnpgv1.ClusterSpec](),
		schema: pinCRDSchema(cnpgModulePath, cnpgCRDs+"clusters.yaml", "v1"),
		base:   map[string]any{"instances": 1, "storage": map[string]any{"size": "1Gi"}}},
	{component: "gatewayclass", handler: &GatewayClassHandler{}, typ: reflect.TypeFor[gatewayv1.GatewayClassSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "gatewayclasses"), "v1"),
		base:   map[string]any{"controllerName": "example.com/gateway"}},
	{component: "gateway", handler: &GatewayHandler{}, typ: reflect.TypeFor[gatewayv1.GatewaySpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "gateways"), "v1"),
		base:   map[string]any{"gatewayClassName": "public", "listeners": []any{map[string]any{"name": "http", "port": 80, "protocol": "HTTP"}}}},
	{component: "listenerset", handler: &ListenerSetHandler{}, typ: reflect.TypeFor[gatewayv1.ListenerSetSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "listenersets"), "v1"),
		base:   map[string]any{"parentRef": map[string]any{"name": "public"}, "listeners": []any{map[string]any{"name": "http", "port": 80, "protocol": "HTTP"}}}},
	{component: "referencegrant", handler: &ReferenceGrantHandler{}, typ: reflect.TypeFor[gatewayv1.ReferenceGrantSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "referencegrants"), "v1"),
		base: map[string]any{
			"from": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": "web"}},
			"to":   []any{map[string]any{"group": "", "kind": "Service"}},
		}},
	{component: "backendtlspolicy", handler: &BackendTLSPolicyHandler{}, typ: reflect.TypeFor[gatewayv1.BackendTLSPolicySpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "backendtlspolicies"), "v1"),
		base: map[string]any{
			"targetRefs": []any{map[string]any{"group": "", "kind": "Service", "name": "payments"}},
			"validation": map[string]any{"hostname": "payments.example.com", "wellKnownCACertificates": "System"},
		}},
	{component: "httproute", handler: &HTTPRouteHandler{}, typ: reflect.TypeFor[gatewayv1.HTTPRouteSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "httproutes"), "v1"),
		base:   map[string]any{"parentRefs": []any{map[string]any{"name": "public"}}}},
	{component: "tcproute", handler: &TCPRouteHandler{}, typ: reflect.TypeFor[gatewayv1.TCPRouteSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "tcproutes"), "v1"),
		base: map[string]any{
			"parentRefs": []any{map[string]any{"name": "public"}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "db", "port": 5432}}}},
		}},
	{component: "udproute", handler: &UDPRouteHandler{}, typ: reflect.TypeFor[gatewayv1.UDPRouteSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "udproutes"), "v1"),
		base: map[string]any{
			"parentRefs": []any{map[string]any{"name": "public"}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "dns", "port": 53}}}},
		}},
	{component: "tlsroute", handler: &TLSRouteHandler{}, typ: reflect.TypeFor[gatewayv1.TLSRouteSpec](),
		schema: pinCRDSchema(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "tlsroutes"), "v1"),
		base: map[string]any{
			"parentRefs": []any{map[string]any{"name": "public"}},
			"hostnames":  []any{"db.example.com"},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "db", "port": 5432}}}},
		}},
	// MetalLB's BFD profile and Community are not listed: their CRDs require
	// no field, so there is nothing to measure.
	{component: "metallb-ipaddresspool", handler: &MetalLBIPAddressPoolHandler{}, typ: reflect.TypeFor[metallbv1beta1.IPAddressPoolSpec](),
		schema: pinCRDSchema(metallbModulePath, metallbCRDDir+"/metallb.io_ipaddresspools.yaml", metallbVersion),
		base:   map[string]any{"addresses": []any{"192.168.10.0/24"}}},
	{component: "metallb-l2advertisement", handler: &MetalLBL2AdvertisementHandler{}, typ: reflect.TypeFor[metallbv1beta1.L2AdvertisementSpec](),
		schema: pinCRDSchema(metallbModulePath, metallbCRDDir+"/metallb.io_l2advertisements.yaml", metallbVersion),
		base:   map[string]any{}},
	{component: "metallb-bgpadvertisement", handler: &MetalLBBGPAdvertisementHandler{}, typ: reflect.TypeFor[metallbv1beta1.BGPAdvertisementSpec](),
		schema: pinCRDSchema(metallbModulePath, metallbCRDDir+"/metallb.io_bgpadvertisements.yaml", metallbVersion),
		base:   map[string]any{}},
	{component: "metallb-bgppeer", handler: &MetalLBBGPPeerHandler{}, typ: reflect.TypeFor[metallbv1beta2.BGPPeerSpec](),
		schema: pinCRDSchema(metallbModulePath, metallbCRDDir+"/metallb.io_bgppeers.yaml", metallbPeerVersion),
		base:   map[string]any{"myASN": 64500}},
	{component: "servicemonitor", handler: &ServiceMonitorHandler{}, typ: reflect.TypeFor[monitoringv1.ServiceMonitorSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[monitoringv1.ServiceMonitorSpec](), false),
		base:   map[string]any{"selector": map[string]any{}, "endpoints": []any{map[string]any{"port": "http"}}}},
	{component: "podmonitor", handler: &PodMonitorHandler{}, typ: reflect.TypeFor[monitoringv1.PodMonitorSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[monitoringv1.PodMonitorSpec](), false),
		base:   map[string]any{"selector": map[string]any{}, "podMetricsEndpoints": []any{map[string]any{"port": "http"}}}},
	{component: "prometheus-probe", handler: &PrometheusProbeHandler{}, typ: reflect.TypeFor[monitoringv1.ProbeSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[monitoringv1.ProbeSpec](), false),
		base: map[string]any{
			"prober":  map[string]any{"url": "blackbox-exporter.monitoring.svc:9115"},
			"targets": map[string]any{"staticConfig": map[string]any{"static": []any{"https://example.com"}}},
		}},
	{component: "prometheusrule", handler: &PrometheusRuleHandler{}, typ: reflect.TypeFor[monitoringv1.PrometheusRuleSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[monitoringv1.PrometheusRuleSpec](), false),
		base:   map[string]any{"groups": []any{map[string]any{"name": "g", "rules": []any{map[string]any{"alert": "Down", "expr": "up == 0"}}}}}},
	{component: "artifactgenerator", handler: &ArtifactGeneratorHandler{}, typ: reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](), false)},
	{component: "bucket", handler: &BucketHandler{}, typ: reflect.TypeFor[sourcev1.BucketSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[sourcev1.BucketSpec](), false)},
	{component: "fluxcd-alert", handler: &FluxcdAlertHandler{}, typ: reflect.TypeFor[notificationv1beta3.AlertSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[notificationv1beta3.AlertSpec](), false)},
	{component: "fluxcd-provider", handler: &FluxcdProviderHandler{}, typ: reflect.TypeFor[notificationv1beta3.ProviderSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[notificationv1beta3.ProviderSpec](), false)},
	{component: "fluxcd-receiver", handler: &FluxcdReceiverHandler{}, typ: reflect.TypeFor[notificationv1.ReceiverSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[notificationv1.ReceiverSpec](), false)},
	{component: "fluxcd-kustomization", handler: &FluxcdKustomizationHandler{}, typ: reflect.TypeFor[kustv1.KustomizationSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[kustv1.KustomizationSpec](), false),
		base:   map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "fleet"}}},
	{component: "gitrepository", handler: &GitRepositoryHandler{}, typ: reflect.TypeFor[sourcev1.GitRepositorySpec](),
		schema: pinMarkerSchema(reflect.TypeFor[sourcev1.GitRepositorySpec](), false),
		base:   map[string]any{"url": "https://example.com/fleet.git"}},
	{component: "helmchart", handler: &HelmChartHandler{}, typ: reflect.TypeFor[sourcev1.HelmChartSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[sourcev1.HelmChartSpec](), false)},
	{component: "helmrelease", handler: &HelmReleaseHandler{}, typ: reflect.TypeFor[helmv2.HelmReleaseSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[helmv2.HelmReleaseSpec](), false),
		// A marker schema holds no enum, so a list element whose kind is one is
		// authored with a valid kind.
		base: map[string]any{
			"chartRef":   map[string]any{"kind": "OCIRepository", "name": "app"},
			"valuesFrom": []any{map[string]any{"kind": "ConfigMap", "name": "values"}},
		}},
	{component: "helmrepository", handler: &HelmRepositoryHandler{}, typ: reflect.TypeFor[sourcev1.HelmRepositorySpec](),
		schema: pinMarkerSchema(reflect.TypeFor[sourcev1.HelmRepositorySpec](), false),
		base:   map[string]any{"url": "https://charts.example.com"}},
	{component: "imagepolicy", handler: &ImagePolicyHandler{}, typ: reflect.TypeFor[imagev1.ImagePolicySpec](),
		schema: pinMarkerSchema(reflect.TypeFor[imagev1.ImagePolicySpec](), false)},
	{component: "imagerepository", handler: &ImageRepositoryHandler{}, typ: reflect.TypeFor[imagev1.ImageRepositorySpec](),
		schema: pinMarkerSchema(reflect.TypeFor[imagev1.ImageRepositorySpec](), false)},
	{component: "imageupdateautomation", handler: &ImageUpdateAutomationHandler{}, typ: reflect.TypeFor[autov1.ImageUpdateAutomationSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[autov1.ImageUpdateAutomationSpec](), false)},
	{component: "ocirepository", handler: &OCIRepositoryHandler{}, typ: reflect.TypeFor[sourcev1.OCIRepositorySpec](),
		schema: pinMarkerSchema(reflect.TypeFor[sourcev1.OCIRepositorySpec](), false),
		base:   map[string]any{"url": "oci://registry.example.com/app"}},
	{component: "resourcesetinputprovider", handler: &ResourceSetInputProviderHandler{}, typ: reflect.TypeFor[fluxoperatorv1.ResourceSetInputProviderSpec](),
		schema: pinCRDSchema("github.com/controlplaneio-fluxcd/flux-operator", fluxOperatorCRDs[resourceSetInputProviderType], fluxoperatorv1.GroupVersion.Version)},
	{component: "secretstore", handler: &SecretStoreHandler{}, typ: reflect.TypeFor[esv1.SecretStoreSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[esv1.SecretStoreSpec](), true),
		base:   map[string]any{"provider": externalSecretsPinFake()}},
	{component: "clustersecretstore", handler: &ClusterSecretStoreHandler{}, typ: reflect.TypeFor[esv1.SecretStoreSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[esv1.SecretStoreSpec](), true),
		base:   map[string]any{"provider": externalSecretsPinFake()}},
	{component: "externalsecret", handler: &ExternalSecretHandler{}, typ: reflect.TypeFor[esv1.ExternalSecretSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[esv1.ExternalSecretSpec](), true),
		base:   map[string]any{"secretStoreRef": map[string]any{"name": "vault"}, "data": []any{externalSecretsPinData()}}},
	{component: "clusterexternalsecret", handler: &ClusterExternalSecretHandler{}, typ: reflect.TypeFor[esv1.ClusterExternalSecretSpec](),
		schema: pinMarkerSchema(reflect.TypeFor[esv1.ClusterExternalSecretSpec](), true),
		base: map[string]any{
			"externalSecretSpec": map[string]any{"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"}, "data": []any{externalSecretsPinData()}},
			"namespaces":         []any{"web"},
		}},
}

// requiredWrittenUnmeasured are the kind components whose API the API server
// does not serve itself and that requiredWrittenKinds leaves out, each with the
// reason. TestRequiredWrittenKinds_CoverEveryCRDKind holds the two lists to
// the kind inventory.
var requiredWrittenUnmeasured = map[string]string{
	"metallb-bfdprofile": "its CRD requires no field",
	"metallb-community":  "its CRD requires no field",
}

// TestRequiredWrittenKinds_CoverEveryCRDKind holds requiredWrittenKinds to the
// kind components it is meant to cover. Every `kind` row of README.md's kind
// inventory whose API group client-go's scheme does not register, so a CRD
// serves it, is measured or listed in requiredWrittenUnmeasured with its
// reason: a new kind component of such an API fails here until it is one or
// the other. A component on both lists fails, and so does one on either list
// that is no such row.
func TestRequiredWrittenKinds_CoverEveryCRDKind(t *testing.T) {
	crdKinds := map[string]int{}
	for _, row := range readKindInventory(t) {
		if row.status != inventoryKind {
			continue
		}
		fields := strings.Fields(row.kind)
		if len(fields) == 0 {
			t.Fatalf("README.md:%d: a kind row with an empty Kind cell", row.line)
		}
		// "<group>/<version>", or "<version>" for the core group.
		group, _, found := strings.Cut(fields[0], "/")
		if !found {
			group = ""
		}
		if clientgoscheme.Scheme.IsGroupRegistered(group) {
			continue
		}
		crdKinds[strings.Trim(row.typ, "`")] = row.line
	}
	// Vacuity guard: a scheme that registered every group, or rows read
	// without their Kind cell, would leave nothing to check.
	if len(crdKinds) < len(requiredWrittenKinds) {
		t.Fatalf("found %d kind components of a CRD-backed API, fewer than the %d requiredWrittenKinds measures; the walk is broken", len(crdKinds), len(requiredWrittenKinds))
	}

	measured := map[string]bool{}
	for _, k := range requiredWrittenKinds {
		measured[k.component] = true
		if _, ok := crdKinds[k.component]; !ok {
			t.Errorf("requiredWrittenKinds measures %s, which is no kind row of a CRD-backed API in README.md's kind inventory", k.component)
		}
	}
	for _, component := range slices.Sorted(maps.Keys(crdKinds)) {
		reason, excused := requiredWrittenUnmeasured[component]
		switch {
		case measured[component] && excused:
			t.Errorf("%s is measured and also listed in requiredWrittenUnmeasured: drop it from one", component)
		case !measured[component] && !excused:
			t.Errorf("README.md:%d: %s is a kind component of a CRD-backed API that requiredWrittenKinds does not measure: add it there, or to requiredWrittenUnmeasured with the reason", crdKinds[component], component)
		case excused && strings.TrimSpace(reason) == "":
			t.Errorf("%s is listed in requiredWrittenUnmeasured without a reason", component)
		}
	}
	for _, component := range slices.Sorted(maps.Keys(requiredWrittenUnmeasured)) {
		if _, ok := crdKinds[component]; !ok {
			t.Errorf("requiredWrittenUnmeasured lists %s, which is no kind row of a CRD-backed API in README.md's kind inventory", component)
		}
	}
}

func ciliumPinRule() map[string]any {
	return map[string]any{"endpointSelector": map[string]any{}, "ingress": []any{map[string]any{"fromEndpoints": []any{map[string]any{}}}}}
}

func externalSecretsPinFake() map[string]any {
	return map[string]any{"fake": map[string]any{"data": []any{map[string]any{"key": "k", "value": "v"}}}}
}

func externalSecretsPinData() map[string]any {
	return map[string]any{"secretKey": "k", "remoteRef": map[string]any{"key": "path"}}
}

// pinFamily names the family of a member by the type that declares it: AFF,
// the terms of an affinity; SEC, a pod's security profiles and sysctls; POD,
// the rest of a pod's and a container's core types; SEL, a label selector's
// match expression; KIND, the kind's own API.
func pinFamily(f kindField) string {
	if f.owner == reflect.TypeFor[metav1.LabelSelectorRequirement]() {
		return "SEL"
	}
	if f.owner.PkgPath() != reflect.TypeFor[corev1.PodSpec]().PkgPath() {
		return "KIND"
	}
	switch f.owner.Name() {
	case "NodeSelectorRequirement", "PodAffinityTerm", "PreferredSchedulingTerm", "WeightedPodAffinityTerm":
		return "AFF"
	case "SeccompProfile", "AppArmorProfile", "Sysctl":
		return "SEC"
	default:
		return "POD"
	}
}

// pinMember is one line of the set.
type pinMember struct{ component, path, family, written string }

func (m pinMember) String() string {
	return m.component + "\t" + m.path + "\t" + m.family + "\t" + m.written
}

// pinMeasure is what the measurement found on one kind: the members, and for
// the guards the fields whose omission the kind refuses, the valid defaults
// the omission wrote and the unauthorable ancestors the controls met, each by
// kind and path.
type pinMeasure struct {
	members                                         []pinMember
	refused, decodeRefused, defaulted, unauthorable []string
}

// measureRequiredWritten measures the set on one kind.
func measureRequiredWritten(t *testing.T, k pinKind) (m pinMeasure) {
	t.Helper()
	schema := k.schema(t)
	prefixes := k.prefixes
	if prefixes == nil {
		prefixes = []string{""}
	}
	base := pinCopy(k.base)
	pinFill(base, schema, "")
	if _, err := pinBuild(k, base); err != nil {
		t.Fatalf("the base properties do not build: %v; properties %s", err, pinJSON(base))
	}
	measured := 0
	// A promoted field can share its JSON name with a shallower one (the
	// Probe's own authorization and the one in its embedded HTTPConfig); the
	// walk reaches the shallower first, and the path is measured once.
	seen := map[string]bool{}
	for _, prefix := range prefixes {
		walkKindFields(k.typ, func(f kindField) bool { return schema.required[prefix+f.path] }, func(f kindField) {
			path := prefix + f.path
			if _, known := schema.props[path]; !known || !schema.required[path] || !f.writtenUnauthored() || seen[path] {
				return
			}
			seen[path] = true
			measured++
			with, err := pinControl(k, base, path, schema)
			if err != nil {
				if ancestor, ok := pinRefusedAncestor(path, err); ok {
					if _, listed := requiredWrittenUnauthorable[k.component+" "+ancestor]; listed {
						m.unauthorable = append(m.unauthorable, k.component+" "+ancestor)
						return
					}
					t.Errorf("%s: the control is refused at %s, which requiredWrittenUnauthorable does not list: %v", path, ancestor, err)
					return
				}
				t.Errorf("%s: no control builds with the field authored: %v", path, err)
				return
			}
			without := pinCopy(with)
			parent, name, _ := pinParent(without, path, false)
			delete(parent, name)
			obj, err := pinBuild(k, without)
			if err != nil {
				if !pinRefusalNames(path, err) {
					if _, listed := requiredWrittenDecodeRefused[k.component+" "+path]; listed && strings.Contains(err.Error(), "do not decode into") {
						m.decodeRefused = append(m.decodeRefused, k.component+" "+path)
						return
					}
					t.Errorf("%s: the omission is refused for another reason: %v", path, err)
					return
				}
				m.refused = append(m.refused, k.component+" "+path)
				return
			}
			objPath := "spec." + path
			if k.prefixes != nil {
				objPath = path
			}
			parent, name, ok := pinParent(obj, objPath, false)
			value, held := parent[name]
			if !ok || !held {
				t.Errorf("%s: the omission builds, and the object does not show the field", path)
				return
			}
			written := pinJSON(value)
			if requiredWrittenValidDefaults[k.component+" "+path] == written {
				m.defaulted = append(m.defaulted, k.component+" "+path)
				return
			}
			m.members = append(m.members, pinMember{k.component, path, pinFamily(f), written})
		})
	}
	if measured == 0 {
		t.Fatalf("the walk measured no field of %s", k.typ)
	}
	return m
}

// pinRefusedAncestor returns the ancestor of path that err is the kind's
// refusal of, without a trailing [] where the refusal names the list.
func pinRefusedAncestor(path string, err error) (string, bool) {
	said := regexp.MustCompile(`\[[0-9]+\]`).ReplaceAllString(err.Error(), "[]")
	segments := strings.Split(path, ".")
	for i := len(segments) - 1; i > 0; i-- {
		ancestor := strings.Join(segments[:i], ".")
		if strings.HasPrefix(said, ancestor+":") {
			return ancestor, true
		}
		if list := strings.TrimSuffix(ancestor, "[]"); strings.HasPrefix(said, list+":") {
			return list, true
		}
	}
	return "", false
}

// pinRefusalNames reports whether err is the kind's refusal of the field at
// path: the error names its full path or a field under it (omitting a struct
// is refused through the field of it the kind requires), at the start or after
// a space, with an index for a list element and any key for a map entry, and
// followed by what a refusal writes after it (": required", " is required", a
// field under it). A leaf name alone would also match another field's error
// (name in namespace, key in secretKey) or the text of a decode failure.
func pinRefusalNames(path string, err error) bool {
	pattern := regexp.QuoteMeta(path)
	pattern = strings.ReplaceAll(pattern, `\[\]`, `\[[0-9]+\]`)
	pattern = strings.ReplaceAll(pattern, `\{\}`, `(\[[^\]]+\]|\.[^.:\s]+)`)
	return regexp.MustCompile(`(^|\s)` + pattern + `(:|\.|\[| is )`).MatchString(err.Error())
}

func pinLeaf(path string) string {
	segments := strings.Split(path, ".")
	return strings.TrimSuffix(strings.TrimSuffix(segments[len(segments)-1], "[]"), "{}")
}

// TestPinRefusalNames holds the match of a refusal to the field's full path: a
// field under it counts, another field that shares its leaf or its prefix
// does not.
func TestPinRefusalNames(t *testing.T) {
	for _, c := range []struct {
		path, err string
		want      bool
	}{
		{"secretRef.name", "secretRef.name: required (the Secret's name)", true},
		{"valuesFrom[].kind", "helmrelease: valuesFrom[1].kind is required: one of Secret, ConfigMap", true},
		{"cluster", "cluster.name: required (the Cluster's name)", true},
		{"tablespaceStorage{}.name", "tablespaceStorage[data].name: required (the name)", true},
		{"tablespaceStorage{}.name", "tablespaceStorage.data.name: required (the name)", true},
		{"secretRef.name", "secretRef.namespace: required (the namespace)", false},
		{"cluster", "clusterName: required (the name)", false},
		{"key", "secretKey: required (the key)", false},
		{"key", "invalid Label: '{}' does not contain label key", false},
		{"fields[].type", "a type under it decodes itself and does not handle what was written", false},
	} {
		if got := pinRefusalNames(c.path, errors.New(c.err)); got != c.want {
			t.Errorf("pinRefusalNames(%q, %q) = %v, want %v", c.path, c.err, got, c.want)
		}
	}
}

// pinRequiredError is a kind's refusal of a missing field, which the control
// answers by filling it.
var pinRequiredError = regexp.MustCompile(`^([A-Za-z0-9_.\[\]]+): required`)

// pinControl returns properties that build with path authored: base, path
// set to a sample, and every required field around it filled. It tries base
// as it is, then, at each ancestor of path, with every other field of base
// removed (for one of several fields the API takes exactly one of), then with
// one of them removed at a time; on each it fills a required field the kind
// names as missing, up to six times.
func pinControl(k pinKind, base map[string]any, path string, schema pinSchema) (map[string]any, error) {
	segments := strings.Split(path, ".")
	variants := []map[string]any{pinCopy(base)}
	for depth := range len(segments) - 1 {
		at := strings.Join(segments[:depth+1], ".")
		doc := pinCopy(base)
		holder, keep, ok := pinParent(doc, at, false)
		if !ok {
			continue
		}
		var siblings []string
		for key := range holder {
			if key != keep {
				siblings = append(siblings, key)
			}
		}
		slices.Sort(siblings)
		for _, key := range siblings {
			delete(holder, key)
		}
		variants = append(variants, doc)
		for _, drop := range siblings {
			one := pinCopy(base)
			h, _, _ := pinParent(one, at, false)
			delete(h, drop)
			variants = append(variants, one)
		}
	}
	var last error
	for _, doc := range variants {
		at, name, ok := pinParent(doc, path, true)
		if !ok {
			return nil, errors.Errorf("%s: cannot be placed in the properties", path)
		}
		if _, held := at[name]; !held {
			at[name] = pinSample(path, schema.props[path])
		}
		pinFill(doc, schema, "")
		for range 6 {
			_, err := pinBuild(k, doc)
			if err == nil {
				return doc, nil
			}
			last = err
			m := pinRequiredError.FindStringSubmatch(err.Error())
			if m == nil {
				break
			}
			named := regexp.MustCompile(`\[[0-9]+\]`).ReplaceAllString(m[1], "[]")
			if named == path {
				break
			}
			p, n, ok := pinParent(doc, named, true)
			if !ok {
				break
			}
			if _, held := p[n]; held {
				break
			}
			p[n] = pinSample(named, schema.props[named])
		}
	}
	return nil, last
}

// pinCandidates are the strings a sample tries against a property's pattern.
var pinCandidates = []string{
	"x", "a", "1", "ab", "a.example.com", "https://example.com", "s3://bucket/path", "10.0.0.0/8", "10.0.0.1",
	"1h", "5m", "a-b", "A", "Ab", "x/y", "example.com/x", "oci://registry.example.com/r", "ssh://a@example.com/r", "1Gi", "v1",
}

// pinSample is a value of the property at path that a kind takes: the first
// non-empty value of an enum, a string the pattern matches, a number in
// bounds, one sampled item of a list, an empty object. A string the schema
// leaves unformatted and the type parses is sampled by its name.
func pinSample(path string, p apiextensionsv1.JSONSchemaProps) any {
	if len(p.Enum) > 0 {
		for _, raw := range p.Enum {
			var v any
			if err := json.Unmarshal(raw.Raw, &v); err == nil && v != "" {
				return v
			}
		}
	}
	if p.XIntOrString {
		return 1
	}
	switch p.Type {
	case "string":
		switch {
		case p.Format == "duration" || pinLeaf(path) == "duration":
			return "1h"
		case p.Format == "date-time":
			return "2026-01-01T00:00:00Z"
		case pinLeaf(path) == "schedule":
			return "0 0 * * *"
		}
		n := int64(1)
		if p.MinLength != nil && *p.MinLength > 1 {
			n = *p.MinLength
		}
		if p.Pattern != "" {
			if re, err := regexp.Compile(p.Pattern); err == nil {
				for _, c := range pinCandidates {
					if int64(len(c)) >= n && re.MatchString(c) {
						return c
					}
				}
			}
		}
		return strings.Repeat("x", int(n))
	case "integer", "number":
		v := 1.0
		if p.Minimum != nil && *p.Minimum > v {
			v = *p.Minimum
		}
		if p.Maximum != nil && *p.Maximum < v {
			v = *p.Maximum
		}
		return v
	case "boolean":
		return true
	case "array":
		if p.Items != nil && p.Items.Schema != nil {
			return []any{pinSample(path+"[]", *p.Items.Schema)}
		}
		return []any{}
	case "object":
		return map[string]any{}
	}
	if len(p.AnyOf) > 0 {
		return "1"
	}
	return map[string]any{}
}

func pinCopy(m map[string]any) map[string]any {
	out := map[string]any{}
	if m == nil {
		return out
	}
	raw, _ := json.Marshal(m)
	_ = json.Unmarshal(raw, &out)
	return out
}

func pinJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// pinParent walks doc along the segments of path but its last, the first
// element of a list and the first value of a map, creating what is missing
// where create says so, and returns the map that holds the last segment and
// its name.
func pinParent(doc map[string]any, path string, create bool) (map[string]any, string, bool) {
	segments := strings.Split(path, ".")
	at := doc
	for _, segment := range segments[:len(segments)-1] {
		// The segment's name, then what it holds, outermost first: [] a list
		// element, {} a map value.
		name, steps := segment, ""
		for {
			if rest, ok := strings.CutSuffix(name, "[]"); ok {
				name, steps = rest, "[]"+steps
				continue
			}
			if rest, ok := strings.CutSuffix(name, "{}"); ok {
				name, steps = rest, "{}"+steps
				continue
			}
			break
		}
		// An empty container of the shape a step takes: a list for [], an
		// object for {} and for the end of the segment.
		empty := func(i int) any {
			if i < len(steps)/2 && steps[2*i:2*i+2] == "[]" {
				return []any{}
			}
			return map[string]any{}
		}
		holder := at
		value, held := holder[name]
		if !held {
			if !create {
				return nil, "", false
			}
			value = empty(0)
			holder[name] = value
		}
		set := func(v any) { holder[name] = v }
		for i := range len(steps) / 2 {
			switch steps[2*i : 2*i+2] {
			case "[]":
				items, ok := value.([]any)
				if !ok {
					return nil, "", false
				}
				if len(items) == 0 {
					if !create {
						return nil, "", false
					}
					items = []any{empty(i + 1)}
					set(items)
				}
				value = items[0]
				set = func(v any) { items[0] = v }
			default:
				values, ok := value.(map[string]any)
				if !ok {
					return nil, "", false
				}
				if len(values) == 0 {
					if !create {
						return nil, "", false
					}
					values["k"] = empty(i + 1)
				}
				key := slices.Sorted(maps.Keys(values))[0]
				value = values[key]
				set = func(v any) { values[key] = v }
			}
		}
		object, isObject := value.(map[string]any)
		if !isObject {
			return nil, "", false
		}
		at = object
	}
	return at, pinLeaf(segments[len(segments)-1]), true
}

// pinFill sets a sample at every required path whose parent doc holds and
// that doc does not, until nothing changes.
func pinFill(doc map[string]any, schema pinSchema, skip string) {
	paths := make([]string, 0, len(schema.required))
	for path := range schema.required {
		if path != skip {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	for range 20 {
		changed := false
		for _, path := range paths {
			parent, name, ok := pinParent(doc, path, false)
			if !ok {
				continue
			}
			if _, held := parent[name]; held {
				continue
			}
			parent[name] = pinSample(path, schema.props[path])
			changed = true
		}
		if !changed {
			return
		}
	}
}

// pinBuild builds a component of the kind with the properties and returns
// the kind's object, as the JSON it is written as.
func pinBuild(k pinKind, props map[string]any) (map[string]any, error) {
	cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "web", Type: k.component, Properties: props}, "default")
	if err != nil {
		return nil, err
	}
	objs, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err != nil {
		return nil, errors.Wrap(err, "generate")
	}
	for _, obj := range objs {
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(*obj)
		if err != nil {
			return nil, err
		}
		if _, ok := u["spec"]; ok || len(objs) == 1 {
			return u, nil
		}
	}
	return nil, errors.Errorf("the build has no object of the kind")
}

// renderRequiredWritten returns the checked-in file for the members.
func renderRequiredWritten(members []pinMember) []byte {
	var b bytes.Buffer
	b.WriteString("# Generated by TestKindComponents_RequiredWrittenNotRefused with " + requiredWrittenPinUpdate + "=1. DO NOT EDIT.\n")
	b.WriteString("# The fields a kind's API requires, that its Go type writes when unauthored, and whose omission the kind does not refuse.\n")
	b.WriteString("# kind\tpath\tfamily\twritten\n")
	for _, m := range members {
		b.WriteString(m.String() + "\n")
	}
	return b.Bytes()
}

// TestKindComponents_RequiredWrittenNotRefused measures the set on every kind
// of requiredWrittenKinds and holds it to the checked-in file, line by line
// and byte for byte. After a change that moves a member, in a kind or in a
// dependency, run it once with UPDATE_REQUIRED_WRITTEN_PIN=1: it writes the
// file and compares nothing, so it is refused where CI is set.
func TestKindComponents_RequiredWrittenNotRefused(t *testing.T) {
	update := os.Getenv(requiredWrittenPinUpdate) == "1"
	if update && os.Getenv("CI") != "" {
		t.Fatalf("%s=1 rewrites %s and compares nothing; it is refused where CI is set", requiredWrittenPinUpdate, requiredWrittenPinFile)
	}
	var all []pinMember
	refused, decodeRefused, defaulted, unauthorable := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, k := range requiredWrittenKinds {
		t.Run(k.component, func(t *testing.T) {
			m := measureRequiredWritten(t, k)
			all = append(all, m.members...)
			for _, key := range m.refused {
				refused[key] = true
			}
			for _, key := range m.decodeRefused {
				decodeRefused[key] = true
			}
			for _, key := range m.defaulted {
				defaulted[key] = true
			}
			for _, key := range m.unauthorable {
				unauthorable[key] = true
			}
		})
	}
	if t.Failed() {
		return
	}
	// Vacuity guards: the measurement finds a member of each family that has
	// one (AFF, SEC, POD twice, KIND), a refusal of an omission (two of the KIND
	// family), each valid default it
	// leaves out, and each unauthorable path it skips, each in its own class.
	have := map[string]bool{}
	for _, m := range all {
		have[m.component+" "+m.path] = true
	}
	for _, guard := range []string{
		"issuer acme.solvers[].http01.ingress.podTemplate.spec.affinity.podAffinity.requiredDuringSchedulingIgnoredDuringExecution[].topologyKey",
		"replicationsource rsyncTLS.moverSecurityContext.seccompProfile.type",
		"cnpg-pooler template.spec.containers[].name",
		"cnpg-cluster imageCatalogRef.name",
		"httproute parentRefs[].name",
	} {
		if !have[guard] {
			t.Errorf("the measurement does not find %s, a member", guard)
		}
	}
	for _, guard := range []struct{ component, path string }{
		{"certificate", "secretName"},
		{"gatewayclass", "controllerName"},
	} {
		if !refused[guard.component+" "+guard.path] {
			t.Errorf("the measurement does not find the %s's refusal of %s without it", guard.component, guard.path)
		}
	}
	// The omission of each valid default builds and writes its value; one
	// that is refused or writes another fails here.
	for _, key := range slices.Sorted(maps.Keys(requiredWrittenValidDefaults)) {
		if !defaulted[key] {
			t.Errorf("%s: the omission no longer builds with %s written; drop it from requiredWrittenValidDefaults", key, requiredWrittenValidDefaults[key])
		}
	}
	for _, key := range slices.Sorted(maps.Keys(requiredWrittenUnauthorable)) {
		if !unauthorable[key] {
			t.Errorf("%s: no control is refused there any more; drop it from requiredWrittenUnauthorable", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(requiredWrittenDecodeRefused)) {
		if !decodeRefused[key] {
			t.Errorf("%s: the omission is no longer refused in the decode; drop it from requiredWrittenDecodeRefused", key)
		}
	}

	want := renderRequiredWritten(all)
	if update {
		if err := os.WriteFile(requiredWrittenPinFile, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", requiredWrittenPinFile, err)
		}
		t.Logf("wrote %s: %d members", requiredWrittenPinFile, len(all))
		return
	}
	got, err := os.ReadFile(requiredWrittenPinFile)
	if err != nil {
		t.Fatalf("read %s: %v", requiredWrittenPinFile, err)
	}
	pinned := map[string]bool{}
	for _, line := range strings.Split(string(got), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			pinned[line] = true
		}
	}
	for _, m := range all {
		if !pinned[m.String()] {
			t.Errorf("new member, a required field the kind writes unauthored and does not refuse: %s", m)
		}
		delete(pinned, m.String())
	}
	for _, line := range slices.Sorted(func(yield func(string) bool) {
		for line := range pinned {
			if !yield(line) {
				return
			}
		}
	}) {
		t.Errorf("no longer a member (now refused, written otherwise, or gone): %s", line)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the measurement writes (%d members); regenerate with %s=1", requiredWrittenPinFile, len(all), requiredWrittenPinUpdate)
	}
	t.Logf("%d members", len(all))
}
