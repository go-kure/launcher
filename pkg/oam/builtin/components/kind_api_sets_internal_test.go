package components

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
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
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// A kind component that decodes its properties into an upstream Go type emits
// what that type encodes, and the type and the API can disagree in two ways
// the strict decode does not see:
//
//   - (a) the API requires a field that the type leaves out when nothing was
//     decoded into it. The object then shows the omission and the API server
//     refuses it;
//   - (b) the API defaults a field that the type writes when nothing was
//     decoded into it. The object then carries the type's zero value, and the
//     API's default never applies.
//
// deriveAPISets derives both sets for a type from what the API says of its
// fields (apiSource), and apiSetKinds holds each kind to an answer for every
// member. A dependency bump that adds, drops or moves one fails
// TestKindComponents_OmittedRequiredAndWrittenDefaults, naming it.
//
// The set of required fields a type writes unauthored is a third one, held
// per family by the tests of the kinds' required lists.

// apiSource is what one API says of the fields of a kind's type.
type apiSource struct {
	// required says the API requires the field.
	required func(kindField) bool
	// def is the default the API gives the field, as it is written in the
	// source, a string unquoted.
	def func(kindField) (string, bool)
	// known says the source describes the field. A field the source does not
	// describe is neither required nor defaulted.
	known func(kindField) bool
}

// crdAPISource is the schema of the named version of the CRD in file, a path
// under the directory of the linked module: the properties under spec, their
// required lists and their defaults.
func crdAPISource(modulePath, file, version string) func(*testing.T) apiSource {
	return func(t *testing.T) apiSource {
		t.Helper()
		file := filepath.Join(linkedModuleDir(t, modulePath), filepath.FromSlash(file))
		data, err := os.ReadFile(file)
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
		spec, ok := crd.Spec.Versions[at].Schema.OpenAPIV3Schema.Properties["spec"]
		if !ok {
			t.Fatalf("%s has no spec", file)
		}
		props, required := map[string]apiextensionsv1.JSONSchemaProps{}, map[string]bool{}
		var walk func(schema apiextensionsv1.JSONSchemaProps, path string)
		walk = func(schema apiextensionsv1.JSONSchemaProps, path string) {
			for name, prop := range schema.Properties {
				child := name
				if path != "" {
					child = path + "." + name
				}
				props[child] = prop
				if slices.Contains(schema.Required, name) {
					required[child] = true
				}
				walk(prop, child)
			}
			if schema.Items != nil && schema.Items.Schema != nil {
				walk(*schema.Items.Schema, path+"[]")
			}
			if schema.AdditionalProperties != nil && schema.AdditionalProperties.Schema != nil {
				walk(*schema.AdditionalProperties.Schema, path+"{}")
			}
		}
		walk(spec, "")
		return apiSource{
			required: func(f kindField) bool { return required[f.path] },
			def: func(f kindField) (string, bool) {
				prop, ok := props[f.path]
				if !ok || prop.Default == nil {
					return "", false
				}
				return strings.Trim(strings.TrimSpace(string(prop.Default.Raw)), `"`), true
			},
			known: func(f kindField) bool { _, ok := props[f.path]; return ok },
		}
	}
}

// markerModules are the modules whose Go source markerAPISource reads.
var markerModules = append([]string{monitoringModulePath, "k8s.io/apimachinery", "k8s.io/api"}, fluxMarkerModules...)

// linkedFieldMarkers returns the lookup of a field's markers in the Go source
// of the linked modules named, and whether that source declares the field. A
// package is read once, when the first of its fields is asked for.
func linkedFieldMarkers(t *testing.T, modules []string) func(kindField) (fieldMarkers, bool) {
	t.Helper()
	dirs, packages := map[string]string{}, map[string]map[string]fieldMarkers{}
	return func(f kindField) (fieldMarkers, bool) {
		pkg := f.owner.PkgPath()
		markers, loaded := packages[pkg]
		if !loaded {
			for _, module := range modules {
				if pkg != module && !strings.HasPrefix(pkg, module+"/") {
					continue
				}
				dir, ok := dirs[module]
				if !ok {
					dir = linkedModuleDir(t, module)
					dirs[module] = dir
				}
				markers = packageFieldMarkers(t, filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(pkg, module))))
				break
			}
			packages[pkg] = markers
		}
		m, ok := markers[f.owner.Name()+"."+f.field.Name]
		return m, ok
	}
}

// markerAPISource is the markers of the linked modules' source, for an API
// whose module ships no CRD (the Prometheus operator's, the Flux controllers')
// and for the built-in types: a field is defaulted where it carries a default marker, and required
// by the rule the schema generators apply to the same source. A field marked
// required is required; one marked optional is not; one with neither marker
// is required unless its json tag omits it when empty. A field of a package
// outside markerModules is not described.
//
// Only a field marked required can be in set (a), since the rule's last
// clause reads a field that is left out when empty as optional. What that
// clause adds (the key and operator of a label selector requirement, the key
// of a key reference) is written when unauthored, so it belongs to the third
// set, which this table does not hold.
//
// For a built-in type the markers are all a linked module holds, and the rule
// says what the published schema marks required, not what the API server's
// own validation refuses. What that validation requires beyond the markers is
// held by hand, in the kinds' validate functions.
func markerAPISource(t *testing.T) apiSource {
	t.Helper()
	of := linkedFieldMarkers(t, markerModules)
	return apiSource{
		required: func(f kindField) bool {
			m, ok := of(f)
			opts := f.jsonOptions()
			return m.required || ok && !m.optional && !slices.Contains(opts, "omitempty") && !slices.Contains(opts, "omitzero")
		},
		def:   func(f kindField) (string, bool) { m, _ := of(f); return m.def, m.hasDefault },
		known: func(f kindField) bool { _, ok := of(f); return ok },
	}
}

// encodesNull says the type writes null for the field when nothing was decoded
// into it and it is not omitted: a pointer, a list, a map or an interface. The
// API server drops a null of a field that is not nullable before it applies
// the defaults, so the default of such a field still applies.
func (f kindField) encodesNull() bool {
	switch f.field.Type.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		return true
	default:
		return false
	}
}

// apiSets is the two sets of one type, by json path with [] for a list
// element and {} for a map value.
type apiSets struct {
	// fields counts the fields walked, known the ones the source describes.
	fields, known int
	// undescribed lists the fields the source does not describe.
	undescribed []string
	// omitted is set (a): required by the API, left out by the type when
	// unauthored.
	omitted []string
	// written is set (b): defaulted by the API and written by the type when
	// unauthored, as something other than null. The value is the default.
	written map[string]string
}

// deriveAPISets walks the fields the encoding of typ reaches and classes each
// by what src says of it. skip names top-level fields that are not walked.
func deriveAPISets(typ reflect.Type, src apiSource, skip ...string) apiSets {
	sets := apiSets{written: map[string]string{}}
	walkKindFields(typ, src.required, func(f kindField) {
		top, _, _ := strings.Cut(f.path, ".")
		if slices.Contains(skip, top) {
			return
		}
		sets.fields++
		if !src.known(f) {
			sets.undescribed = append(sets.undescribed, f.path)
			return
		}
		sets.known++
		written := f.writtenUnauthored()
		if src.required(f) && !written {
			sets.omitted = append(sets.omitted, f.path)
		}
		if def, ok := src.def(f); ok && written && !f.encodesNull() {
			sets.written[f.path] = def
		}
	})
	slices.Sort(sets.omitted)
	return sets
}

// apiSetKind is one kind component with its answer for every member of its
// two sets.
type apiSetKind struct {
	component string
	// typ is the type the walk starts at, source what the API says of it, and
	// skip the top-level fields a component does not author.
	typ    reflect.Type
	source func(*testing.T) apiSource
	skip   []string
	// build converts a component of the kind with the properties.
	build func(props map[string]any) error
	// documents are properties that build. Each field named in refused and
	// defaultRefused is held by exactly one, in the first element of each
	// list on its path.
	documents []func() map[string]any

	// Set (a). refused: the kind refuses the properties without the field,
	// under the field's path. listed: the kind does not, for the reason given.
	refused []string
	listed  map[string]string

	// Set (b). defaultRefused: the kind refuses the properties without the
	// field, in a sentence that names the API's default. filled: the kind
	// writes the API's default itself, which is the value here. harmless: the
	// value the type writes is one the API reads as its default, for the
	// reason given.
	defaultRefused []string
	filled         map[string]string
	harmless       map[string]string

	// absent lists the fields the type reaches that the source does not
	// describe, each of which the kind refuses when it is authored: the linked
	// Go type is newer than the API the source is of. The kind's own tests
	// show the refusals.
	absent []string
}

// podFieldsNotChecked is the answer of the kinds of the built-in pod types
// for the four pod-spec fields upstream marks required and leaves out when
// empty.
const podFieldsNotChecked = "marked required in the upstream source and not checked: no linked module shows that the API server refuses a Pod without it, and the field is behind a feature gate of the cluster"

// podSpecOmitted lists those fields, under the path of a pod spec.
func podSpecOmitted(path string) map[string]string {
	out := map[string]string{}
	for _, list := range []string{"containers", "initContainers", "ephemeralContainers"} {
		out[path+list+"[].restartPolicyRules[].action"] = podFieldsNotChecked
		out[path+list+"[].restartPolicyRules[].exitCodes.operator"] = podFieldsNotChecked
	}
	out[path+"volumes[].projected.sources[].podCertificate.signerName"] = podFieldsNotChecked
	out[path+"volumes[].projected.sources[].podCertificate.keyType"] = podFieldsNotChecked
	return out
}

func handlerBuild(h oam.ComponentHandler, typ string) func(map[string]any) error {
	return func(props map[string]any) error {
		_, err := h.ToApplicationConfig(&oam.Component{Name: "fast", Type: typ, Properties: props}, "data")
		return err
	}
}

const (
	certManagerCRDs = "deploy/crds/cert-manager.io_"
	ciliumCRDs      = "pkg/k8s/apis/cilium.io/client/crds/v2/"
	cnpgCRDs        = "config/crd/bases/postgresql.cnpg.io_"
	volsyncCRDs     = "config/crd/bases/volsync.backube_"
)

// objectIdentity is the top-level fields of a whole-object kind's type that a
// component does not author.
var objectIdentity = []string{"kind", "apiVersion", "metadata"}

// apiSetKinds lists the kind components of go-kure/launcher#790 that decode
// their properties into an upstream type. The httproute kind is not held
// here: the two fields its experimental-channel CRD requires of an external
// authorization filter are answered with the other Gateway API routes.
//
// The rows stand in the order of their component, as sort.Strings gives it
// (TestKindLists_InOrder, pkg/cmd/kurel). Each row's source says where its two
// sets are read: the markers of the source for the Prometheus operator's API
// and the built-in types, the CRDs their modules ship for cert-manager's API,
// Cilium's, CloudNativePG's and its Barman Cloud plugin's.
//
// Every registered component type has its row here, or its reason for having
// none in kindListExceptions (pkg/cmd/kurel): TestKindLists_Complete holds
// that, so a new kind is given one of the two.
//
// A new kind joins with one row, at its position: its component, the type its
// properties decode into (skip naming the identity fields of a whole-object
// type) and its source, crdAPISource where the linked module ships the CRD
// and markerAPISource where it does not.
// TestKindComponents_OmittedRequiredAndWrittenDefaults then names every
// member of the kind's two sets that has no answer. A refused member also
// needs build and a document that holds the field, on which the test shows
// the refusal.
var apiSetKinds = []apiSetKind{
	{component: "alertmanager", typ: reflect.TypeFor[monitoringv1.AlertmanagerSpec](), source: markerAPISource, listed: monitoringWorkloadPodOmitted()},
	{component: "artifactgenerator", typ: reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](), source: markerAPISource},
	// The Gateway API's infrastructure objects (backendtlspolicy, gateway,
	// gatewayclass, listenerset, referencegrant) are read from the experimental
	// channel's CRDs its module ships, which hold every field the Go types do.
	// TestGatewayKinds_RefusedOmissions holds each refusal to the standard
	// channel's as well.
	{
		component: "backendtlspolicy", typ: reflect.TypeFor[gatewayv1.BackendTLSPolicySpec](),
		source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "backendtlspolicies"), "v1"),
		build:  handlerBuild(&BackendTLSPolicyHandler{}, "backendtlspolicy"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{
				"targetRefs": []any{map[string]any{"group": "", "kind": "Service", "name": "payments"}},
				"validation": map[string]any{"hostname": "payments.internal.example.com", "wellKnownCACertificates": "System"},
			}
		}},
		refused: []string{"targetRefs"},
	},
	{component: "bucket", typ: reflect.TypeFor[sourcev1.BucketSpec](), source: markerAPISource},
	{
		component: "certificate", typ: reflect.TypeFor[certv1.CertificateSpec](),
		source: crdAPISource(certManagerModulePath, certManagerCRDs+"certificates.yaml", "v1"),
		build:  handlerBuild(&CertificateHandler{}, "certificate"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{
				"secretName": "web-tls", "issuerRef": map[string]any{"name": "ca"},
				"renewal": map[string]any{"windows": []any{map[string]any{"cron": "0 2 * * *", "windowDuration": "2h"}}},
			}
		}},
		refused: []string{"renewal.windows[].cron", "renewal.windows[].windowDuration"},
	},
	{
		component: "cilium-bgpadvertisement", typ: reflect.TypeFor[ciliumv2.CiliumBGPAdvertisementSpec](),
		source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumbgpadvertisements.yaml", "v2"),
		build:  handlerBuild(&CiliumBGPAdvertisementHandler{}, "cilium-bgpadvertisement"),
		// An entry holds an interface or a service, so each has a document.
		documents: []func() map[string]any{
			func() map[string]any {
				return map[string]any{"advertisements": []any{map[string]any{
					"advertisementType": "Interface", "interface": map[string]any{"name": "lo"},
				}}}
			},
			func() map[string]any {
				return map[string]any{"advertisements": []any{map[string]any{
					"advertisementType": "Service", "service": map[string]any{"addresses": []any{"LoadBalancerIP"}},
					"selector": map[string]any{"matchLabels": map[string]any{"bgp": "public"}},
				}}}
			},
		},
		refused: []string{"advertisements[].interface.name", "advertisements[].service.addresses"},
	},
	{component: "cilium-bgpclusterconfig", typ: reflect.TypeFor[ciliumv2.CiliumBGPClusterConfigSpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumbgpclusterconfigs.yaml", "v2")},
	{component: "cilium-bgpnodeconfigoverride", typ: reflect.TypeFor[ciliumv2.CiliumBGPNodeConfigOverrideSpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumbgpnodeconfigoverrides.yaml", "v2")},
	{component: "cilium-bgppeerconfig", typ: reflect.TypeFor[ciliumv2.CiliumBGPPeerConfigSpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumbgppeerconfigs.yaml", "v2")},
	{component: "cilium-cidrgroup", typ: reflect.TypeFor[ciliumv2.CiliumCIDRGroupSpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumcidrgroups.yaml", "v2")},
	{component: "cilium-clusterwidenetworkpolicy", typ: reflect.TypeFor[ciliumapi.Rule](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumclusterwidenetworkpolicies.yaml", "v2")},
	{component: "cilium-egressgatewaypolicy", typ: reflect.TypeFor[ciliumv2.CiliumEgressGatewayPolicySpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumegressgatewaypolicies.yaml", "v2")},
	{component: "cilium-loadbalancerippool", typ: reflect.TypeFor[ciliumv2.CiliumLoadBalancerIPPoolSpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumloadbalancerippools.yaml", "v2")},
	{component: "cilium-localredirectpolicy", typ: reflect.TypeFor[ciliumv2.CiliumLocalRedirectPolicySpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumlocalredirectpolicies.yaml", "v2")},
	{component: "cilium-networkpolicy", typ: reflect.TypeFor[ciliumapi.Rule](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumnetworkpolicies.yaml", "v2")},
	{component: "cilium-nodeconfig", typ: reflect.TypeFor[ciliumv2.CiliumNodeConfigSpec](), source: crdAPISource(ciliumBGPModulePath, ciliumCRDs+"ciliumnodeconfigs.yaml", "v2")},
	{component: "clusterissuer", typ: reflect.TypeFor[certv1.IssuerSpec](), source: crdAPISource(certManagerModulePath, certManagerCRDs+"clusterissuers.yaml", "v1")},
	{component: "clusterrole", typ: reflect.TypeFor[rbacv1.ClusterRole](), source: markerAPISource, skip: objectIdentity},
	{component: "clusterrolebinding", typ: reflect.TypeFor[rbacv1.ClusterRoleBinding](), source: markerAPISource, skip: objectIdentity},
	{component: "cnpg-backup", typ: reflect.TypeFor[cnpgv1.BackupSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"backups.yaml", "v1")},
	{
		component: "cnpg-cluster", typ: reflect.TypeFor[cnpgv1.ClusterSpec](),
		source: crdAPISource(cnpgModulePath, cnpgCRDs+"clusters.yaml", "v1"),
		build:  handlerBuild(&CnpgClusterHandler{}, "cnpg-cluster"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{"projectedVolumeTemplate": map[string]any{"sources": []any{
				map[string]any{"podCertificate": map[string]any{"signerName": "example.com/workload", "keyType": "ECDSAP384"}},
			}}}
		}},
		refused: []string{
			"projectedVolumeTemplate.sources[].podCertificate.keyType",
			"projectedVolumeTemplate.sources[].podCertificate.signerName",
		},
		harmless: map[string]string{
			"instances": "the kind always writes a count of at least one: the authored one, the policy's default, or 1, which is the CRD's own default",
			"backup.volumeSnapshot.onlineConfiguration": "the type writes {} under an authored volumeSnapshot; the object's default is waitForArchive true and immediateCheckpoint false, and in a {} the CRD fills waitForArchive with the field's own default true, while an absent immediateCheckpoint is false",
		},
	},
	{component: "cnpg-clusterimagecatalog", typ: reflect.TypeFor[cnpgv1.ImageCatalogSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"clusterimagecatalogs.yaml", "v1")},
	{
		component: "cnpg-database", typ: reflect.TypeFor[cnpgv1.DatabaseSpec](),
		source: crdAPISource(cnpgModulePath, cnpgCRDs+"databases.yaml", "v1"),
		// TestCnpgKindsAlwaysEncodedDefaults_MatchCRD holds the same list to
		// the same CRD, and the kind refuses an authored empty value.
		filled: cnpgDatabaseAlwaysEncodedDefaults,
	},
	{component: "cnpg-databaserole", typ: reflect.TypeFor[cnpgv1.DatabaseRoleSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"databaseroles.yaml", "v1")},
	{component: "cnpg-imagecatalog", typ: reflect.TypeFor[cnpgv1.ImageCatalogSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"imagecatalogs.yaml", "v1")},
	{component: "cnpg-objectstore", typ: reflect.TypeFor[barmanv1.ObjectStoreSpec](), source: crdAPISource(barmanCloudModulePath, "config/crd/bases/barmancloud.cnpg.io_objectstores.yaml", "v1")},
	{
		component: "cnpg-pooler", typ: reflect.TypeFor[cnpgv1.PoolerSpec](),
		source: crdAPISource(cnpgModulePath, cnpgCRDs+"poolers.yaml", "v1"),
		build:  handlerBuild(&CnpgPoolerHandler{}, "cnpg-pooler"),
		documents: []func() map[string]any{func() map[string]any {
			container := func() map[string]any {
				return map[string]any{"name": "pgbouncer", "restartPolicyRules": []any{map[string]any{
					"action": "Restart", "exitCodes": map[string]any{"operator": "In", "values": []any{42}},
				}}}
			}
			return map[string]any{
				"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{},
				"template": map[string]any{"spec": map[string]any{
					"containers": []any{container()}, "initContainers": []any{container()},
					"volumes": []any{map[string]any{"name": "identity", "projected": map[string]any{"sources": []any{
						map[string]any{"podCertificate": map[string]any{"signerName": "example.com/workload", "keyType": "ECDSAP384"}},
					}}}},
				}},
			}
		}},
		refused: []string{
			"template.spec.containers[].restartPolicyRules[].action",
			"template.spec.containers[].restartPolicyRules[].exitCodes.operator",
			"template.spec.initContainers[].restartPolicyRules[].action",
			"template.spec.initContainers[].restartPolicyRules[].exitCodes.operator",
			"template.spec.volumes[].projected.sources[].podCertificate.keyType",
			"template.spec.volumes[].projected.sources[].podCertificate.signerName",
		},
		listed: map[string]string{
			"template.spec.ephemeralContainers[].restartPolicyRules[].action":             "the kind refuses every ephemeral container of the template",
			"template.spec.ephemeralContainers[].restartPolicyRules[].exitCodes.operator": "the kind refuses every ephemeral container of the template",
		},
	},
	{component: "cnpg-publication", typ: reflect.TypeFor[cnpgv1.PublicationSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"publications.yaml", "v1")},
	{component: "cnpg-scheduledbackup", typ: reflect.TypeFor[cnpgv1.ScheduledBackupSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"scheduledbackups.yaml", "v1")},
	{component: "cnpg-subscription", typ: reflect.TypeFor[cnpgv1.SubscriptionSpec](), source: crdAPISource(cnpgModulePath, cnpgCRDs+"subscriptions.yaml", "v1")},
	{component: "csidriver", typ: reflect.TypeFor[storagev1.CSIDriverSpec](), source: markerAPISource},
	{component: "endpointslice", typ: reflect.TypeFor[discoveryv1.EndpointSlice](), source: markerAPISource, skip: objectIdentity},
	{component: "fluxcd-alert", typ: reflect.TypeFor[notificationv1beta3.AlertSpec](), source: markerAPISource},
	{component: "fluxcd-kustomization", typ: reflect.TypeFor[kustv1.KustomizationSpec](), source: markerAPISource},
	{component: "fluxcd-provider", typ: reflect.TypeFor[notificationv1beta3.ProviderSpec](), source: markerAPISource},
	{component: "fluxcd-receiver", typ: reflect.TypeFor[notificationv1.ReceiverSpec](), source: markerAPISource},
	{component: "gateway", typ: reflect.TypeFor[gatewayv1.GatewaySpec](), source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "gateways"), "v1")},
	{component: "gatewayclass", typ: reflect.TypeFor[gatewayv1.GatewayClassSpec](), source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "gatewayclasses"), "v1")},
	{component: "gitrepository", typ: reflect.TypeFor[sourcev1.GitRepositorySpec](), source: markerAPISource},
	{component: "grpcroute", typ: reflect.TypeFor[gatewayv1.GRPCRouteSpec](), source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "grpcroutes"), "v1")},
	{
		component: "helmchart", typ: reflect.TypeFor[sourcev1.HelmChartSpec](), source: markerAPISource,
		// The API defaults the provider of a verification and the type always
		// writes it: the kind writes the default where none was authored, and
		// refuses an authored empty one (TestFluxKinds_VerifyProviderDefault).
		filled: map[string]string{"verify.provider": fluxVerifyProviderDefault},
	},
	{
		component: "helmrelease", typ: reflect.TypeFor[helmv2.HelmReleaseSpec](), source: markerAPISource,
		build: handlerBuild(&HelmReleaseHandler{}, "helmrelease"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{"chart": map[string]any{"spec": map[string]any{
				"chart": "app", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts"},
			}}}
		}},
		refused: []string{"chart.spec.sourceRef.kind"},
		// The provider of a verification, as on helmchart.
		filled: map[string]string{"chart.spec.verify.provider": fluxVerifyProviderDefault},
	},
	{component: "helmrepository", typ: reflect.TypeFor[sourcev1.HelmRepositorySpec](), source: markerAPISource},
	{component: "horizontalpodautoscaler", typ: reflect.TypeFor[autoscalingv2.HorizontalPodAutoscalerSpec](), source: markerAPISource},
	{component: "imagepolicy", typ: reflect.TypeFor[imagev1.ImagePolicySpec](), source: markerAPISource},
	{
		component: "imagerepository", typ: reflect.TypeFor[imagev1.ImageRepositorySpec](), source: markerAPISource,
		build: handlerBuild(&ImageRepositoryHandler{}, "imagerepository"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{"image": "registry.example/shop/web", "interval": "10m"}
		}},
		// The API requires the image and the type leaves an empty one out.
		refused: []string{"image"},
	},
	{
		component: "imageupdateautomation", typ: reflect.TypeFor[autov1.ImageUpdateAutomationSpec](), source: markerAPISource,
		build: handlerBuild(&ImageUpdateAutomationHandler{}, "imageupdateautomation"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "fleet"}, "interval": "30m"}
		}},
		// The API defaults the kind of the source and the type writes it
		// empty, which is a value and not the default.
		defaultRefused: []string{"sourceRef.kind"},
	},
	{component: "ingress", typ: reflect.TypeFor[networkingv1.IngressSpec](), source: markerAPISource},
	{component: "ingressclass", typ: reflect.TypeFor[networkingv1.IngressClassSpec](), source: markerAPISource},
	{component: "issuer", typ: reflect.TypeFor[certv1.IssuerSpec](), source: crdAPISource(certManagerModulePath, certManagerCRDs+"issuers.yaml", "v1")},
	{component: "limitrange", typ: reflect.TypeFor[corev1.LimitRangeSpec](), source: markerAPISource},
	{
		component: "listenerset", typ: reflect.TypeFor[gatewayv1.ListenerSetSpec](),
		source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "listenersets"), "v1"),
		build:  handlerBuild(&ListenerSetHandler{}, "listenerset"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{
				"parentRef": map[string]any{"name": "public"},
				"listeners": []any{map[string]any{"name": "http", "port": 80, "protocol": "HTTP"}},
			}
		}},
		refused: []string{"listeners", "listeners[].name", "listeners[].port", "listeners[].protocol"},
	},
	// MetalLB's API, from the CRDs its module ships.
	{component: "metallb-bfdprofile", typ: reflect.TypeFor[metallbv1beta1.BFDProfileSpec](), source: crdAPISource(metallbModulePath, metallbCRDDir+"/metallb.io_bfdprofiles.yaml", metallbVersion)},
	{component: "metallb-bgpadvertisement", typ: reflect.TypeFor[metallbv1beta1.BGPAdvertisementSpec](), source: crdAPISource(metallbModulePath, metallbCRDDir+"/metallb.io_bgpadvertisements.yaml", metallbVersion)},
	{component: "metallb-bgppeer", typ: reflect.TypeFor[metallbv1beta2.BGPPeerSpec](), source: crdAPISource(metallbModulePath, metallbCRDDir+"/metallb.io_bgppeers.yaml", metallbPeerVersion)},
	{component: "metallb-community", typ: reflect.TypeFor[metallbv1beta1.CommunitySpec](), source: crdAPISource(metallbModulePath, metallbCRDDir+"/metallb.io_communities.yaml", metallbVersion)},
	{component: "metallb-ipaddresspool", typ: reflect.TypeFor[metallbv1beta1.IPAddressPoolSpec](), source: crdAPISource(metallbModulePath, metallbCRDDir+"/metallb.io_ipaddresspools.yaml", metallbVersion)},
	{component: "metallb-l2advertisement", typ: reflect.TypeFor[metallbv1beta1.L2AdvertisementSpec](), source: crdAPISource(metallbModulePath, metallbCRDDir+"/metallb.io_l2advertisements.yaml", metallbVersion)},
	{component: "namespace", typ: reflect.TypeFor[corev1.NamespaceSpec](), source: markerAPISource},
	{component: "networkpolicy", typ: reflect.TypeFor[networkingv1.NetworkPolicySpec](), source: markerAPISource},
	{
		component: "ocirepository", typ: reflect.TypeFor[sourcev1.OCIRepositorySpec](), source: markerAPISource,
		// The provider of a verification, as on helmchart.
		filled: map[string]string{"verify.provider": fluxVerifyProviderDefault},
	},
	{component: "persistentvolume", typ: reflect.TypeFor[corev1.PersistentVolumeSpec](), source: markerAPISource},
	{component: "pod", typ: reflect.TypeFor[corev1.PodSpec](), source: markerAPISource, listed: podSpecOmitted("")},
	{component: "poddisruptionbudget", typ: reflect.TypeFor[policyv1.PodDisruptionBudgetSpec](), source: markerAPISource},
	{component: "podmonitor", typ: reflect.TypeFor[monitoringv1.PodMonitorSpec](), source: markerAPISource},
	{component: "podtemplate", typ: reflect.TypeFor[corev1.PodTemplate](), source: markerAPISource, skip: objectIdentity, listed: podSpecOmitted("template.spec.")},
	{component: "priorityclass", typ: reflect.TypeFor[schedulingv1.PriorityClass](), source: markerAPISource, skip: objectIdentity},
	{
		component: "prometheus-probe", typ: reflect.TypeFor[monitoringv1.ProbeSpec](), source: markerAPISource,
		build: handlerBuild(&PrometheusProbeHandler{}, "prometheus-probe"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{
				"prober": map[string]any{"url": "blackbox-exporter.monitoring.svc:9115"},
				"params": []any{map[string]any{"name": "module", "values": []any{"http_2xx"}}},
			}
		}},
		refused: []string{"params[].name"},
	},
	{component: "prometheusrule", typ: reflect.TypeFor[monitoringv1.PrometheusRuleSpec](), source: markerAPISource},
	{component: "referencegrant", typ: reflect.TypeFor[gatewayv1.ReferenceGrantSpec](), source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "referencegrants"), "v1")},
	{component: "replicaset", typ: reflect.TypeFor[appsv1.ReplicaSetSpec](), source: markerAPISource, listed: podSpecOmitted("template.spec.")},
	{component: "replicationcontroller", typ: reflect.TypeFor[corev1.ReplicationControllerSpec](), source: markerAPISource, listed: podSpecOmitted("template.spec.")},
	// VolSync's API, from the CRDs its module ships.
	// TestVolsyncKinds_AbsentFromCRD shows the refusal of each absent field.
	{
		component: "replicationdestination", typ: reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec](),
		source: crdAPISource(volsyncModulePath, volsyncCRDs+"replicationdestinations.yaml", "v1alpha1"),
		absent: volsyncAbsentFields("rclone", "restic", "rsyncTLS"),
	},
	{
		component: "replicationsource", typ: reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec](),
		source: crdAPISource(volsyncModulePath, volsyncCRDs+"replicationsources.yaml", "v1alpha1"),
		absent: volsyncAbsentFields("rclone", "restic", "rsyncTLS", "syncthing"),
	},
	{component: "resourcequota", typ: reflect.TypeFor[corev1.ResourceQuotaSpec](), source: markerAPISource},
	{
		component: "resourcesetinputprovider", typ: reflect.TypeFor[fluxoperatorv1.ResourceSetInputProviderSpec](), source: markerAPISource,
		harmless: map[string]string{
			"schedule[].window": "the type writes an unauthored window as 0s, which is the API's own default",
		},
	},
	{component: "role", typ: reflect.TypeFor[rbacv1.Role](), source: markerAPISource, skip: objectIdentity},
	{component: "rolebinding", typ: reflect.TypeFor[rbacv1.RoleBinding](), source: markerAPISource, skip: objectIdentity},
	{component: "runtimeclass", typ: reflect.TypeFor[nodev1.RuntimeClass](), source: markerAPISource, skip: objectIdentity},
	{component: "servicecidr", typ: reflect.TypeFor[networkingv1.ServiceCIDRSpec](), source: markerAPISource},
	{component: "servicemonitor", typ: reflect.TypeFor[monitoringv1.ServiceMonitorSpec](), source: markerAPISource},
	{component: "storageclass", typ: reflect.TypeFor[storagev1.StorageClass](), source: markerAPISource, skip: objectIdentity},
	{
		component: "tcproute", typ: reflect.TypeFor[gatewayv1.TCPRouteSpec](),
		source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "tcproutes"), "v1"),
		build:  handlerBuild(&TCPRouteHandler{}, "tcproute"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "db", "port": 5432}}}}}
		}},
		refused: []string{"rules", "rules[].backendRefs"},
	},
	{
		component: "tlsroute", typ: reflect.TypeFor[gatewayv1.TLSRouteSpec](),
		source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "tlsroutes"), "v1"),
		build:  handlerBuild(&TLSRouteHandler{}, "tlsroute"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{
				"hostnames": []any{"db.example.com"},
				"rules":     []any{map[string]any{"backendRefs": []any{map[string]any{"name": "db", "port": 5432}}}},
			}
		}},
		refused: []string{"hostnames", "rules", "rules[].backendRefs"},
	},
	{
		component: "udproute", typ: reflect.TypeFor[gatewayv1.UDPRouteSpec](),
		source: crdAPISource(gatewayAPIModulePath, gatewayAPICRDFile("experimental", "udproutes"), "v1"),
		build:  handlerBuild(&UDPRouteHandler{}, "udproute"),
		documents: []func() map[string]any{func() map[string]any {
			return map[string]any{"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "dns", "port": 53}}}}}
		}},
		refused: []string{"rules", "rules[].backendRefs"},
	},
	{component: "volumeattributesclass", typ: reflect.TypeFor[storagev1.VolumeAttributesClass](), source: markerAPISource, skip: objectIdentity},
}

// editAt replaces the field of props at path, in the first element of each
// list on the way, by edit's result, or removes it where edit reports so. It
// reports whether props hold the field: where they do not, they are unchanged.
func editAt(props map[string]any, path string, edit func() (value any, keep bool)) bool {
	segments := strings.Split(path, ".")
	at := props
	for i, segment := range segments {
		name, list := strings.CutSuffix(segment, "[]")
		value, ok := at[name]
		if !ok {
			return false
		}
		if i == len(segments)-1 {
			if list {
				return false
			}
			if replacement, keep := edit(); keep {
				at[name] = replacement
			} else {
				delete(at, name)
			}
			return true
		}
		if list {
			elements, isList := value.([]any)
			if !isList || len(elements) == 0 {
				return false
			}
			value = elements[0]
		}
		if at, ok = value.(map[string]any); !ok {
			return false
		}
	}
	return false
}

// TestKindComponents_OmittedRequiredAndWrittenDefaults derives the two sets of
// every kind in apiSetKinds from its API source and holds the kind's answers
// to them: every member is answered once, nothing else is, and each refusal is
// shown on the kind's handler, for the field left out and for an authored
// null, beside properties with the field that build.
func TestKindComponents_OmittedRequiredAndWrittenDefaults(t *testing.T) {
	seen := map[string]bool{}
	refused, listed, filled, harmless := 0, 0, 0, 0
	for _, kind := range apiSetKinds {
		if seen[kind.component] {
			t.Errorf("%s is listed twice", kind.component)
		}
		seen[kind.component] = true
		refused += len(kind.refused) + len(kind.defaultRefused)
		listed, filled, harmless = listed+len(kind.listed), filled+len(kind.filled), harmless+len(kind.harmless)

		t.Run(kind.component, func(t *testing.T) {
			sets := deriveAPISets(kind.typ, kind.source(t), kind.skip...)
			t.Logf("%d fields, %d described by the source; required and left out: %v; defaulted and written: %v",
				sets.fields, sets.known, sets.omitted, slices.Sorted(maps.Keys(sets.written)))
			// Vacuity guards: the source describes the type it is read for.
			if sets.known == 0 {
				t.Fatalf("the source describes no field of %s", kind.typ)
			}
			// A CRD gives the metadata of an embedded object no properties:
			// its schema is the API server's own. Nothing else may be
			// missing from a source, or a member of either set could be,
			// but a field the kind lists as absent and refuses.
			for _, path := range sets.undescribed {
				if !strings.Contains(path, ".metadata.") && !slices.Contains(kind.absent, path) {
					t.Errorf("the source does not describe %s of %s", path, kind.typ)
				}
			}
			for _, path := range kind.absent {
				if !slices.Contains(sets.undescribed, path) {
					t.Errorf("%s is listed as absent from the source, which describes it or which the type does not reach", path)
				}
			}

			answers := slices.Concat(kind.refused, slices.Collect(maps.Keys(kind.listed)))
			slices.Sort(answers)
			if !slices.Equal(answers, sets.omitted) {
				t.Errorf("required by the API and left out by the type:\n  derived  %v\n  answered %v", sets.omitted, answers)
			}
			defaults := slices.Concat(kind.defaultRefused, slices.Collect(maps.Keys(kind.filled)), slices.Collect(maps.Keys(kind.harmless)))
			slices.Sort(defaults)
			if written := slices.Sorted(maps.Keys(sets.written)); !slices.Equal(defaults, written) {
				t.Errorf("defaulted by the API and written by the type:\n  derived  %v\n  answered %v", written, defaults)
			}
			for path, reason := range kind.listed {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is listed without a reason", path)
				}
			}
			for path, reason := range kind.harmless {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is harmless without a reason", path)
				}
			}
			for path, def := range kind.filled {
				if want, ok := sets.written[path]; ok && def != want {
					t.Errorf("%s: the kind writes %q, the API's default is %q", path, def, want)
				}
			}

			for i, document := range kind.documents {
				if err := kind.build(document()); err != nil {
					t.Fatalf("document %d does not build: %v", i, err)
				}
			}
			edits := map[string]func() (any, bool){
				"left out": func() (any, bool) { return nil, false },
				"null":     func() (any, bool) { return nil, true },
			}
			for _, path := range slices.Concat(kind.refused, kind.defaultRefused) {
				want, shown := strings.ReplaceAll(path, "[]", "[0]")+": required", 0
				for _, document := range kind.documents {
					for name, edit := range edits {
						props := document()
						if !editAt(props, path, edit) {
							continue
						}
						shown++
						if err := kind.build(props); err == nil || !strings.HasPrefix(err.Error(), want) {
							t.Errorf("%s %s: err = %v, want one starting %q", path, name, err, want)
						}
					}
				}
				if shown != len(edits) {
					t.Errorf("%s: shown on %d edited documents, want %d (one document that holds the field)", path, shown, len(edits))
				}
			}
		})
	}
	// Vacuity guard on the table: the kinds on main hold these answers.
	if refused == 0 || listed == 0 || filled == 0 || harmless == 0 {
		t.Errorf("the table holds %d refused, %d listed, %d filled and %d harmless answers; each kind of answer has a member on main", refused, listed, filled, harmless)
	}
}

// TestDeriveAPISets_ReadsBothSources: the derivation tells the four classes
// apart on fields whose answer is known, from a CRD and from markers.
func TestDeriveAPISets_ReadsBothSources(t *testing.T) {
	crd := crdAPISource(cnpgModulePath, cnpgCRDs+"databases.yaml", "v1")(t)
	markers := markerAPISource(t)
	field := func(typ reflect.Type, name, path string) kindField {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("%s has no field %s", typ, name)
		}
		return kindField{owner: typ, field: f, path: path}
	}
	ensure := field(reflect.TypeFor[cnpgv1.ExtensionSpec](), "Ensure", "extensions[].ensure")
	if def, ok := crd.def(ensure); !ok || def != "present" || !ensure.writtenUnauthored() || ensure.encodesNull() {
		t.Errorf("extensions[].ensure: default %q (%v), written %v, null %v; want present, written and not null", def, ok, ensure.writtenUnauthored(), ensure.encodesNull())
	}
	owner := field(reflect.TypeFor[cnpgv1.DatabaseSpec](), "Owner", "owner")
	if !crd.required(owner) || !owner.writtenUnauthored() {
		t.Errorf("owner: required %v, written %v; want a required field the type writes", crd.required(owner), owner.writtenUnauthored())
	}
	name := field(reflect.TypeFor[monitoringv1.ProbeParam](), "Name", "params[].name")
	if !markers.required(name) || name.writtenUnauthored() {
		t.Errorf("params[].name: required %v, written %v; want a required field the type leaves out", markers.required(name), name.writtenUnauthored())
	}
	signer := field(reflect.TypeFor[corev1.PodCertificateProjection](), "SignerName", "signerName")
	if !markers.required(signer) || signer.writtenUnauthored() {
		t.Errorf("podCertificate.signerName: required %v, written %v; want a required field the type leaves out", markers.required(signer), signer.writtenUnauthored())
	}
	// The generators' rule without a marker: required where the tag keeps the
	// field, optional where it is marked so or left out when empty.
	key := field(reflect.TypeFor[metav1.LabelSelectorRequirement](), "Key", "selector.matchExpressions[].key")
	if !markers.required(key) || !key.writtenUnauthored() {
		t.Errorf("matchExpressions[].key: required %v, written %v; want a field without a marker that the type writes read as required", markers.required(key), key.writtenUnauthored())
	}
	values := field(reflect.TypeFor[metav1.LabelSelectorRequirement](), "Values", "selector.matchExpressions[].values")
	if markers.required(values) {
		t.Error("matchExpressions[].values is read as required; it is marked optional")
	}
	path := field(reflect.TypeFor[monitoringv1.ProberSpec](), "Path", "prober.path")
	if def, ok := markers.def(path); !ok || def != "/probe" || path.writtenUnauthored() {
		t.Errorf("prober.path: default %q (%v), written %v; want /probe on a field the type leaves out", def, ok, path.writtenUnauthored())
	}
	if unknown := field(reflect.TypeFor[oam.Component](), "Name", "name"); markers.known(unknown) {
		t.Error("the markers describe a field of a package outside the modules read")
	}
}
