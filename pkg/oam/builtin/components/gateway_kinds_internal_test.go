package components

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// gatewayAPIModulePath is the module whose CRDs the tests below read. It
// ships them for both channels of the API: the standard one, and the
// experimental one, which holds every field the Go types do.
const gatewayAPIModulePath = "sigs.k8s.io/gateway-api"

// gatewayAPIChannels are the two channels, the one the Go types match first.
var gatewayAPIChannels = []string{"experimental", "standard"}

// gatewayAPIKinds lists the kind components of the Gateway API's
// infrastructure objects, and of its routes that carry no HTTP
// (gateway_route_common.go), with the CRD of the object each emits and the
// spec type it decodes into. experimental names the properties only the
// experimental channel's CRD holds, and omitted the numbers, booleans and
// strings the type omits when zero. Their required lists are generated from
// the same CRDs (TestKindComponents_RequiredIsGenerated).
//
// The fields the CRD requires that the type omits when they are not authored,
// which no required list can name, are answered in the kind's row of
// apiSetKinds.
var gatewayAPIKinds = []struct {
	component    string
	crd          string
	typ          reflect.Type
	experimental []string
	omitted      []string
	// defaulted is the kind's defaulted-zero list (policyFreeKind.defaultedZeros).
	defaulted map[string]string
}{
	{component: "gatewayclass", crd: "gatewayclasses", typ: reflect.TypeFor[gatewayv1.GatewayClassSpec]()},
	{
		component: "gateway", crd: "gateways", typ: reflect.TypeFor[gatewayv1.GatewaySpec](),
		experimental: append([]string{"addresses[].routability", "defaultScope"}, gatewayListenerFiltersExperimental()...),
		omitted: slices.Concat(
			[]string{"addresses[].routability", "addresses[].value", "defaultScope"},
			gatewayListenerFiltersOmitted,
			[]string{"tls.frontend.default.validation.mode", "tls.frontend.perPort[].tls.validation.mode"},
		),
		defaulted: gatewayKind.defaultedZeros.fields,
	},
	{
		component: "listenerset", crd: "listenersets", typ: reflect.TypeFor[gatewayv1.ListenerSetSpec](),
		experimental: gatewayListenerFiltersExperimental(),
		omitted:      append(slices.Clone(gatewayListenerFiltersOmitted), "listeners[].name", "listeners[].port", "listeners[].protocol"),
	},
	{component: "referencegrant", crd: "referencegrants", typ: reflect.TypeFor[gatewayv1.ReferenceGrantSpec]()},
	{
		component: "backendtlspolicy", crd: "backendtlspolicies", typ: reflect.TypeFor[gatewayv1.BackendTLSPolicySpec](),
		omitted: []string{"validation.subjectAltNames[].hostname", "validation.subjectAltNames[].uri"},
	},
	{
		component: "tcproute", crd: "tcproutes", typ: reflect.TypeFor[gatewayv1.TCPRouteSpec](),
		experimental: gatewayRouteExperimental, omitted: gatewayRouteExperimental,
	},
	{
		component: "udproute", crd: "udproutes", typ: reflect.TypeFor[gatewayv1.UDPRouteSpec](),
		experimental: gatewayRouteExperimental, omitted: gatewayRouteExperimental,
	},
	{
		component: "tlsroute", crd: "tlsroutes", typ: reflect.TypeFor[gatewayv1.TLSRouteSpec](),
		experimental: gatewayRouteExperimental, omitted: gatewayRouteExperimental,
	},
}

// gatewayListenerFiltersExperimental names, in order, the properties of a
// listener's request filters, which only the experimental channel's CRD
// holds, on a Gateway and on a ListenerSet.
func gatewayListenerFiltersExperimental() []string {
	const filters = "listeners[].filters"
	out := []string{filters, filters + ".requests"}
	for _, field := range []string{
		"extensionRef", "extensionRef.group", "extensionRef.kind", "extensionRef.name",
		"externalAuth", "externalAuth.backendRef", "externalAuth.backendRef.group", "externalAuth.backendRef.kind",
		"externalAuth.backendRef.name", "externalAuth.backendRef.namespace", "externalAuth.backendRef.port",
		"externalAuth.forwardBody", "externalAuth.forwardBody.maxSize",
		"externalAuth.grpc", "externalAuth.grpc.allowedHeaders",
		"externalAuth.http", "externalAuth.http.allowedHeaders", "externalAuth.http.allowedResponseHeaders", "externalAuth.http.path",
		"externalAuth.protocol", "type",
	} {
		out = append(out, filters+".requests[]."+field)
	}
	return out
}

// gatewayListenerFiltersOmitted are, in order, the numbers and strings of a
// listener's request filter that the type omits when zero.
var gatewayListenerFiltersOmitted = []string{
	"listeners[].filters.requests[].externalAuth.forwardBody.maxSize",
	"listeners[].filters.requests[].externalAuth.http.path",
	"listeners[].filters.requests[].externalAuth.protocol",
}

// gatewayRouteExperimental names the property of a route that only the
// experimental channel's CRD holds. It is also the one string of a route the
// type omits when empty.
var gatewayRouteExperimental = []string{"useDefaultGateways"}

// gatewayAPICRDFile is the path of one CRD under the directory of the linked
// module, in one channel.
func gatewayAPICRDFile(channel, name string) string {
	return "config/crd/" + channel + "/gateway.networking.k8s.io_" + name + ".yaml"
}

// gatewayAPICRD reads one CRD of the linked module, in one channel, and
// returns it with the schema of its v1 version: the one the Go types of
// apis/v1 are, which must be served. A CRD of this API may serve older
// versions beside it, so the single-version readers (crdSpecProperties) do not
// fit.
func gatewayAPICRD(t *testing.T, channel, name string) (*apiextensionsv1.CustomResourceDefinition, apiextensionsv1.JSONSchemaProps) {
	t.Helper()
	file := filepath.Join(linkedModuleDir(t, gatewayAPIModulePath), filepath.FromSlash(gatewayAPICRDFile(channel, name)))
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	for _, version := range crd.Spec.Versions {
		if version.Name != "v1" {
			continue
		}
		if !version.Served || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			t.Fatalf("%s does not serve a schema-bearing v1 version; update this test", file)
		}
		return &crd, *version.Schema.OpenAPIV3Schema
	}
	t.Fatalf("%s has no v1 version; update this test", file)
	return nil, apiextensionsv1.JSONSchemaProps{}
}

// gatewayAPICRDSpec is the schema of the spec of the v1 version of one CRD of
// the linked module, in one channel.
func gatewayAPICRDSpec(t *testing.T, channel, name string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	_, root := gatewayAPICRD(t, channel, name)
	spec, ok := root.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property in v1", gatewayAPICRDFile(channel, name))
	}
	return spec
}

// TestGatewayKinds_WrittenEmptyAccepted holds, on the CRDs of the linked
// module, the fields of the Gateway API's own types that reach the API server
// empty with nobody having authored them, as
// TestCertManagerKinds_WrittenEmptyAccepted does for cert-manager. The kinds'
// required lists are generated from the experimental channel's CRDs
// (TestKindComponents_RequiredIsGenerated), and follow what was authored.
// Where the type writes a struct the author left out, its fields reach the API
// server as their zero values, and so does every field the type writes that
// the API does not require: none may be refused empty in either channel. The
// fields the API requires that the type omits when they are not authored are
// a second set, which TestKindComponents_OmittedRequiredAndWrittenDefaults
// derives and holds to the kind's answers; here each member is only held to a
// place a refusal can name.
//
// The Go types hold the fields of both channels, so the experimental CRD is
// the one every path is looked up in. The standard one must agree with it on
// what it requires, wherever it holds the property, and the properties it does
// not hold are the kind's stated experimental fields: a cluster on either
// channel is then refused the same omissions.
func TestGatewayKinds_WrittenEmptyAccepted(t *testing.T) {
	for _, kind := range gatewayAPIKinds {
		t.Run(kind.component, func(t *testing.T) {
			props, required := schemaProperties(gatewayAPICRDSpec(t, "experimental", kind.crd))
			standard, standardRequired := schemaProperties(gatewayAPICRDSpec(t, "standard", kind.crd))

			var experimental []string
			for path := range props {
				if _, ok := standard[path]; !ok {
					experimental = append(experimental, path)
					continue
				}
				if required[path] != standardRequired[path] {
					t.Errorf("%s: required = %v in the experimental channel and %v in the standard one", path, required[path], standardRequired[path])
				}
			}
			slices.Sort(experimental)
			if !slices.Equal(experimental, kind.experimental) {
				t.Errorf("only the experimental channel holds %v, want %v: the README names the kind's experimental fields", experimental, kind.experimental)
			}
			for path := range standard {
				if _, ok := props[path]; !ok {
					t.Errorf("%s is a property of the standard channel only; the experimental CRD is no superset, and this test reads it as one", path)
				}
			}

			empty, omitted := map[string]bool{}, map[string]bool{}
			fields := 0
			walkKindFields(kind.typ, func(f kindField) bool { return required[f.path] }, func(f kindField) {
				if !strings.HasPrefix(f.owner.PkgPath(), gatewayAPIModulePath+"/") {
					return
				}
				fields++
				if _, ok := props[f.path]; !ok {
					t.Errorf("%s (%s.%s) is no property of the CRD; the paths are keyed wrongly", f.path, f.owner, f.field.Name)
					return
				}
				if !f.writtenUnauthored() {
					if !required[f.path] {
						return
					}
					omitted[f.path] = true
					if strings.Contains(f.path, "{}") {
						t.Errorf("%s is required and omitted under a map value; say in the kind's row how its omission is answered", f.path)
					}
					if f.forced {
						t.Errorf("%s is required and omitted under a struct the type writes where the author wrote nothing: the API refuses the object of an author who left the struct out; the kind must fill the field or leave the struct out", f.path)
					}
					return
				}
				if required[f.path] && !f.forced {
					return
				}
				empty[f.path] = true
				for channel, schema := range map[string]map[string]apiextensionsv1.JSONSchemaProps{"experimental": props, "standard": standard} {
					held, ok := schema[f.path]
					if !ok {
						continue
					}
					if why := crdRefusesZero(held, f.field.Type); why != "" {
						t.Errorf("%s is written empty where the author wrote nothing, and the API refuses that (%s channel): %s; the kind must refuse the omission or fill the field", f.path, channel, why)
					}
				}
			})
			if fields == 0 {
				t.Fatalf("the walk found no field of %s", kind.typ)
			}
			t.Logf("walked %d fields of the Gateway API's types; required and omitted unauthored: %v; written empty unauthored: %v",
				fields, slices.Sorted(maps.Keys(omitted)), slices.Sorted(maps.Keys(empty)))
		})
	}
}

// gatewayAPIRefused returns the fields the row of apiSetKinds for the kind
// says the kind refuses the omission of.
func gatewayAPIRefused(t *testing.T, component string) []string {
	t.Helper()
	at := slices.IndexFunc(apiSetKinds, func(kind apiSetKind) bool { return kind.component == component })
	if at < 0 {
		t.Fatalf("apiSetKinds has no row for %s", component)
	}
	return apiSetKinds[at].refused
}

// TestGatewayKinds_RefusedOmissions holds every field a kind's validate
// refuses the omission of to both channels of the API: each one is required in
// the standard channel as in the experimental one that
// TestKindComponents_OmittedRequiredAndWrittenDefaults derives the set from,
// so the refusal is no wider than what a cluster on either channel refuses.
// The kind refuses an authored empty value as it refuses the omission, since
// the type omits both; of a list that is an empty list, so each refused list
// must be one the API wants at least one item of, in both channels. A path
// that is one of the kind's experimental fields is held to the experimental
// channel only: the standard one holds no such property, so no document of a
// cluster on it reaches the refusal.
func TestGatewayKinds_RefusedOmissions(t *testing.T) {
	refusals := 0
	for _, kind := range gatewayAPIKinds {
		for _, path := range gatewayAPIRefused(t, kind.component) {
			refusals++
			t.Run(kind.component+"/"+path, func(t *testing.T) {
				for _, channel := range gatewayAPIChannels {
					props, required := schemaProperties(gatewayAPICRDSpec(t, channel, kind.crd))
					held, ok := props[path]
					if !ok && channel == "standard" && slices.Contains(kind.experimental, path) {
						continue
					}
					if !ok {
						t.Fatalf("%s is no property of the %s channel's CRD; the kind refuses its omission on every cluster", path, channel)
					}
					if !required[path] {
						t.Errorf("%s is not required in the %s channel; the kind refuses its omission", path, channel)
					}
					if held.Type != "array" {
						continue
					}
					if min := held.MinItems; min == nil || *min < 1 {
						t.Errorf("%s may be empty in the %s channel; the kind refuses an empty one", path, channel)
					}
				}
			})
		}
	}
	// Vacuity guard: the rows hold the refusals this test reads.
	if refusals != 14 {
		t.Errorf("read %d refusals, want 14: the protocol of a listener request filter's external authorization on a Gateway and on a ListenerSet, a ListenerSet's listeners with a listener's name, port and protocol, a BackendTLSPolicy's targetRefs, a TCPRoute's, a UDPRoute's and a TLSRoute's rules with a rule's backendRefs, and a TLSRoute's hostnames", refusals)
	}
}

// TestGatewayKinds_DefaultedZeros is TestMonitoringKinds_DefaultedZeros for
// the policy-free kinds of the Gateway API, its infrastructure objects and
// the TCP, UDP and TLS routes, with the CRD of each
// channel as the source of the defaults: a number, a boolean or a string these
// kinds decode that is omitted when zero and that a CRD defaults to something
// else must be in the kind's defaulted-zero list
// (policyFreeKind.defaultedZeros) with that default, and the list must hold
// nothing else.
//
// The numbers, booleans and strings a type omits when zero are held to the
// kind's row, so the reflection walk is seen to reach them and a dependency
// bump that adds one fails here, naming it. At v1.7.0 there are nineteen: three
// are each route's useDefaultGateways, six the three of a listener request
// filter on a Gateway and on a ListenerSet, and two of the nineteen the CRDs
// default: the mode of a Gateway's frontend TLS validation.
func TestGatewayKinds_DefaultedZeros(t *testing.T) {
	for _, kind := range gatewayAPIKinds {
		t.Run(kind.component, func(t *testing.T) {
			omitted := omitemptyScalarPaths(kind.typ)
			if got := slices.Sorted(maps.Keys(omitted)); !slices.Equal(got, kind.omitted) {
				t.Fatalf("numbers, booleans and strings omitted when zero = %v, want %v", got, kind.omitted)
			}
			for _, channel := range gatewayAPIChannels {
				spec := gatewayAPICRDSpec(t, channel, kind.crd)
				// Vacuity guard: the CRD's defaults are read, at depth.
				if kind.component == "gateway" || kind.component == "listenerset" {
					const from = "listeners[].allowedRoutes.namespaces.from"
					if def := schemaScalarDefaults(spec, "string")[from]; def != `"Same"` {
						t.Fatalf("%s has the default %s in the %s channel, want \"Same\"; the CRD's defaults are not being read", from, def, channel)
					}
				}
				derived := map[string]string{}
				for path, def := range schemaScalarDefaults(spec, "integer", "number", "boolean", "string") {
					if omitted[path] && !crdDefaultIsZero(def) {
						derived[path] = def
					}
				}
				compareDefaultedZeros(t, kind.component+" ("+channel+" channel)", derived, kind.defaulted)
			}
		})
	}
	// The list refuses: an authored "" on a listed field does not reach the
	// object.
	_, err := gatewayKind.config(&oam.Component{Name: "gateway", Properties: map[string]any{
		"tls": map[string]any{"frontend": map[string]any{"default": map[string]any{"validation": map[string]any{"mode": ""}}}},
	}})
	const want = `tls.frontend.default.validation.mode: "" cannot be carried by the Gateway API types (the field is omitted when zero, so the API server would apply its default "AllowValidOnly")`
	if err == nil || err.Error() != want {
		t.Errorf("an authored empty string on a defaulted field: got %v, want %s", err, want)
	}
}
