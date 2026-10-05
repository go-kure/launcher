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
)

// gatewayAPIModulePath is the module whose CRDs the tests below read. It
// ships them for both channels of the API: the standard one, and the
// experimental one, which holds every field the Go types do.
const gatewayAPIModulePath = "sigs.k8s.io/gateway-api"

// gatewayAPIChannels are the two channels, the one the Go types match first.
var gatewayAPIChannels = []string{"experimental", "standard"}

// gatewayAPIKinds lists the kind components of the Gateway API's
// infrastructure objects with the CRD of the object each emits, the spec type
// it decodes into and its required list. experimental names the properties
// only the experimental channel's CRD holds, and omitted the numbers and
// booleans the type omits when zero.
//
// refused and unshown answer, between them, for every field the CRD requires
// that the type omits when it is not authored, which no required list can
// name: a list is read against what was authored, and an authored empty value
// is omitted as an unauthored one is. refused holds, per field, the kind's
// validate on a spec that leaves out that field alone, and accepts the same
// validate on the spec that leaves out none. unshown holds the fields the kind
// does not refuse, each with the reason the decoded value cannot show the
// omission.
var gatewayAPIKinds = []struct {
	component    string
	crd          string
	typ          reflect.Type
	required     map[string]string
	experimental []string
	omitted      []string
	accepts      func() error
	refused      map[string]func() error
	unshown      map[string]string
}{
	{component: "gatewayclass", crd: "gatewayclasses", typ: reflect.TypeFor[gatewayv1.GatewayClassSpec](), required: gatewayClassKind.required},
	{
		component: "gateway", crd: "gateways", typ: reflect.TypeFor[gatewayv1.GatewaySpec](), required: gatewayKind.required,
		experimental: []string{"defaultScope"},
	},
	{
		component: "listenerset", crd: "listenersets", typ: reflect.TypeFor[gatewayv1.ListenerSetSpec](), required: listenerSetKind.required,
		omitted: []string{"listeners[].port"},
		accepts: func() error { return listenerSetKind.validate(listenerSetSpecWith(func(*gatewayv1.ListenerEntry) {})) },
		refused: map[string]func() error{
			"listeners": func() error { return listenerSetKind.validate(&gatewayv1.ListenerSetSpec{}) },
			"listeners[].name": func() error {
				return listenerSetKind.validate(listenerSetSpecWith(func(l *gatewayv1.ListenerEntry) { l.Name = "" }))
			},
			"listeners[].port": func() error {
				return listenerSetKind.validate(listenerSetSpecWith(func(l *gatewayv1.ListenerEntry) { l.Port = 0 }))
			},
			"listeners[].protocol": func() error {
				return listenerSetKind.validate(listenerSetSpecWith(func(l *gatewayv1.ListenerEntry) { l.Protocol = "" }))
			},
		},
	},
	{component: "referencegrant", crd: "referencegrants", typ: reflect.TypeFor[gatewayv1.ReferenceGrantSpec](), required: referenceGrantKind.required},
	{
		component: "backendtlspolicy", crd: "backendtlspolicies", typ: reflect.TypeFor[gatewayv1.BackendTLSPolicySpec](), required: backendTLSPolicyKind.required,
		accepts: func() error {
			return backendTLSPolicyKind.validate(&gatewayv1.BackendTLSPolicySpec{
				TargetRefs: []gatewayv1.LocalPolicyTargetReferenceWithSectionName{{}},
			})
		},
		refused: map[string]func() error{
			"targetRefs": func() error { return backendTLSPolicyKind.validate(&gatewayv1.BackendTLSPolicySpec{}) },
		},
	},
}

// listenerSetSpecWith returns a ListenerSet spec of one listener that holds
// every field the API requires of it and the type omits, after edit.
func listenerSetSpecWith(edit func(*gatewayv1.ListenerEntry)) *gatewayv1.ListenerSetSpec {
	listener := gatewayv1.ListenerEntry{Name: "web", Port: 80, Protocol: gatewayv1.HTTPProtocolType}
	edit(&listener)
	return &gatewayv1.ListenerSetSpec{Listeners: []gatewayv1.ListenerEntry{listener}}
}

// gatewayAPICRDSpec reads one CRD of the linked module, in one channel, and
// returns the schema of the spec of its v1 version: the one the Go types of
// apis/v1 are. A CRD of this API may serve older versions beside it, so the
// single-version readers (crdSpecProperties) do not fit.
func gatewayAPICRDSpec(t *testing.T, channel, name string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	file := filepath.Join(linkedModuleDir(t, gatewayAPIModulePath), "config", "crd", channel, "gateway.networking.k8s.io_"+name+".yaml")
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
		spec, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
		if !ok {
			t.Fatalf("%s has no spec property in v1", file)
		}
		return spec
	}
	t.Fatalf("%s has no v1 version; update this test", file)
	return apiextensionsv1.JSONSchemaProps{}
}

// TestGatewayKinds_RequiredMatchCRD is TestCertManagerKinds_RequiredMatchCRD
// for the kinds of the Gateway API's infrastructure objects: it derives, from
// the CRDs of the linked module, the fields of each kind that the API requires
// and the type would write unauthored, holds the kind's required list to them,
// and holds every other field the type writes empty unauthored to a schema
// that accepts it. It derives a second set as well, the fields the API
// requires that the type omits when they are not authored, and holds each to
// one of two answers in the kind's row: the kind's validate refuses the
// omission, which the test runs, or the row states why the decoded value
// cannot show it.
//
// The Go types hold the fields of both channels, so the experimental CRD is
// the one every path is looked up in. The standard one must agree with it on
// what it requires, wherever it holds the property, and the properties it does
// not hold are the kind's stated experimental fields: a cluster on either
// channel is then refused the same omissions.
//
// Only the fields of the Gateway API's own types are derived. A field of a
// Kubernetes type these specs embed (the expressions of a label selector) is
// not, and no kind refuses its omission.
func TestGatewayKinds_RequiredMatchCRD(t *testing.T) {
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

			listed, empty, omitted := map[string]bool{}, map[string]bool{}, map[string]bool{}
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
				if required[f.path] {
					if strings.Contains(f.path, "{}") {
						t.Errorf("%s is required under a map value, which a required list cannot name", f.path)
					}
					listed[f.path] = true
					if !f.forced {
						return
					}
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
			t.Logf("walked %d fields of the Gateway API's types; required and written unauthored: %v; required and omitted unauthored: %v; written empty unauthored: %v",
				fields, slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(omitted)), slices.Sorted(maps.Keys(empty)))
			if got, want := slices.Sorted(maps.Keys(kind.required)), slices.Sorted(maps.Keys(listed)); !slices.Equal(got, want) {
				t.Errorf("required list = %v\nthe CRD requires %v", got, want)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}

			// The second set: required by the CRD and omitted by the type
			// when unauthored. Each member is refused by the kind's validate
			// or stated, with its reason, as one the decoded value cannot
			// show.
			answered := slices.Sorted(maps.Keys(kind.refused))
			for path, why := range kind.unshown {
				if _, both := kind.refused[path]; both {
					t.Errorf("%s is both refused and stated as not shown", path)
					continue
				}
				if strings.TrimSpace(why) == "" {
					t.Errorf("%s is stated as not shown, with no reason", path)
				}
				answered = append(answered, path)
			}
			slices.Sort(answered)
			if want := slices.Sorted(maps.Keys(omitted)); !slices.Equal(answered, want) {
				t.Errorf("fields the kind answers for = %v\nthe CRD requires, and the type omits unauthored, %v", answered, want)
			}
			if kind.accepts != nil {
				if err := kind.accepts(); err != nil {
					t.Errorf("the spec that leaves out no such field is refused: %v; the refusals below would prove nothing", err)
				}
			} else if len(kind.refused) > 0 {
				t.Errorf("the kind's row has refusals and no accepted spec to hold them against")
			}
			for path, refuse := range kind.refused {
				// The message names the entry by its index, the first here.
				want := strings.ReplaceAll(path, "[]", "[0]") + ": required"
				if err := refuse(); err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Errorf("%s left out: err = %v, want one that starts with %q", path, err, want)
				}
			}
		})
	}
}

// TestGatewayKinds_RefusedOmissions holds every field a kind's validate
// refuses the omission of to both channels of the API: each one is required in
// the standard channel as in the experimental one that
// TestGatewayKinds_RequiredMatchCRD derives the set from, so the refusal is no
// wider than what a cluster on either channel refuses. The kind refuses an
// authored empty value as it refuses the omission, since the type omits both;
// of a list that is an empty list, so each refused list must be one the API
// wants at least one item of, in both channels.
func TestGatewayKinds_RefusedOmissions(t *testing.T) {
	refusals := 0
	for _, kind := range gatewayAPIKinds {
		for path := range kind.refused {
			refusals++
			t.Run(kind.component+"/"+path, func(t *testing.T) {
				for _, channel := range gatewayAPIChannels {
					props, required := schemaProperties(gatewayAPICRDSpec(t, channel, kind.crd))
					held, ok := props[path]
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
	if refusals != 5 {
		t.Errorf("read %d refusals, want 5: a ListenerSet's listeners with a listener's name, port and protocol, and a BackendTLSPolicy's targetRefs", refusals)
	}
}

// TestGatewayKinds_NoDefaultedZeros is TestCertManagerKinds_NoDefaultedZeros
// for the kinds of the Gateway API's infrastructure objects: no field these
// kinds decode may be a number or a boolean that is omitted when zero and that
// a CRD of either channel defaults to something else, since
// policyFreeKind.config carries no defaulted-zero list.
//
// The numbers and booleans a type omits when zero are held to the kind's row,
// so the reflection walk is seen to reach them and a dependency bump that adds
// one fails here, naming it. At v1.6.2 there is one, a ListenerSet's listener
// port, which the CRD does not default.
func TestGatewayKinds_NoDefaultedZeros(t *testing.T) {
	for _, kind := range gatewayAPIKinds {
		t.Run(kind.component, func(t *testing.T) {
			omitted := omitemptyScalarPaths(kind.typ)
			if got := slices.Sorted(maps.Keys(omitted)); !slices.Equal(got, kind.omitted) {
				t.Fatalf("numbers and booleans omitted when zero = %v, want %v", got, kind.omitted)
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
				for path, def := range schemaScalarDefaults(spec, "integer", "number", "boolean") {
					if omitted[path] && !crdDefaultIsZero(def) {
						t.Errorf("%s is omitted when zero and defaults to %s in the %s channel: an authored zero would be replaced; the kind needs a defaulted-zero list, which policyFreeKind does not carry", path, def, channel)
					}
				}
			}
		})
	}
}
