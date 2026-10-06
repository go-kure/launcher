package components

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	metallbv1beta2 "go.universe.tf/metallb/api/v1beta2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// metallbModulePath is the module whose CRDs the tests below read, and
// metallbCRDDir where it keeps them, from the module's root.
const (
	metallbModulePath = "go.universe.tf/metallb"
	metallbCRDDir     = "config/crd/bases"
)

// metallbVersion is the version of the API five of the kinds emit and whose
// schema the tests read for them. metallbPeerVersion is the BGP peer's: its
// CRD serves v1beta1 too, deprecated, and stores this one.
const (
	metallbVersion     = "v1beta1"
	metallbPeerVersion = "v1beta2"
)

// metallbKinds lists the kind components of MetalLB's API
// (go-kure/launcher#790), each with the CRD it emits an object of, the version
// of it, the spec type it decodes into and its required list. Every one is
// namespaced.
//
// checked names the CRD's expression rules the kind's validate holds, by the
// path of the value they are declared on and their text, each with properties
// that break it and properties that keep it (ciliumBGPRule, which the rules of
// Cilium's BGP kinds are listed with). left names the rules the kind leaves to
// the API server, each with the reason. A rule a CRD declares that is in
// neither fails TestMetalLBKinds_ExpressionRules.
//
// defaulted is the kind's defaulted-zero list (policyFreeKind.defaultedZeros),
// which TestMetalLBKinds_NoDefaultIsLost holds to the CRD's defaults.
var metallbKinds = []struct {
	component string
	handler   oam.ComponentHandler
	crd       string
	version   string
	typ       reflect.Type
	required  map[string]string
	defaulted map[string]string
	checked   map[string]ciliumBGPRule
	left      map[string]string
}{
	{
		component: "metallb-ipaddresspool", handler: &MetalLBIPAddressPoolHandler{},
		crd: "metallb.io_ipaddresspools.yaml", version: metallbVersion,
		typ:      reflect.TypeFor[metallbv1beta1.IPAddressPoolSpec](),
		required: metallbIPAddressPoolKind.required,
	},
	{
		component: "metallb-l2advertisement", handler: &MetalLBL2AdvertisementHandler{},
		crd: "metallb.io_l2advertisements.yaml", version: metallbVersion,
		typ:      reflect.TypeFor[metallbv1beta1.L2AdvertisementSpec](),
		required: metallbL2AdvertisementKind.required,
	},
	{
		component: "metallb-bgpadvertisement", handler: &MetalLBBGPAdvertisementHandler{},
		crd: "metallb.io_bgpadvertisements.yaml", version: metallbVersion,
		typ:      reflect.TypeFor[metallbv1beta1.BGPAdvertisementSpec](),
		required: metallbBGPAdvertisementKind.required,
		checked: map[string]ciliumBGPRule{
			"spec: !has(self.serviceSelectors) || self.serviceSelectors.size() == 0 || ((!has(self.aggregationLength) || self.aggregationLength == 32) && (!has(self.aggregationLengthV6) || self.aggregationLengthV6 == 128))": {
				breaks: []map[string]any{
					{"serviceSelectors": []any{map[string]any{}}, "aggregationLength": 24},
					{"serviceSelectors": []any{map[string]any{"matchLabels": map[string]any{"exposure": "public"}}}, "aggregationLengthV6": 64},
					// One length at the value the rule allows does not excuse the other.
					{"serviceSelectors": []any{map[string]any{}}, "aggregationLength": 32, "aggregationLengthV6": 64},
					{"serviceSelectors": []any{map[string]any{}}, "aggregationLength": 24, "aggregationLengthV6": nil},
				},
				want: "serviceSelectors: not allowed with aggregationLength",
				keeps: []map[string]any{
					// Neither length authored: the API server fills the two the rule allows.
					{"serviceSelectors": []any{map[string]any{}}},
					{"serviceSelectors": []any{map[string]any{}}, "aggregationLength": nil, "aggregationLengthV6": nil},
					{"serviceSelectors": []any{map[string]any{}}, "aggregationLength": 32, "aggregationLengthV6": 128},
					{"serviceSelectors": []any{map[string]any{}}, "aggregationLength": 32},
					// No service selector: any length.
					{"aggregationLength": 24, "aggregationLengthV6": 64},
					{"aggregationLength": 24, "serviceSelectors": []any{}},
					{"aggregationLengthV6": 64, "serviceSelectors": nil},
				},
			},
		},
	},
	{
		// The CRD requires no field of a profile and declares no rule.
		component: "metallb-bfdprofile", handler: &MetalLBBFDProfileHandler{},
		crd: "metallb.io_bfdprofiles.yaml", version: metallbVersion,
		typ:      reflect.TypeFor[metallbv1beta1.BFDProfileSpec](),
		required: metallbBFDProfileKind.required,
	},
	{
		// The CRD requires no field of a Community or of an alias, and declares
		// no rule.
		component: "metallb-community", handler: &MetalLBCommunityHandler{},
		crd: "metallb.io_communities.yaml", version: metallbVersion,
		typ:      reflect.TypeFor[metallbv1beta1.CommunitySpec](),
		required: metallbCommunityKind.required,
	},
	{
		component: "metallb-bgppeer", handler: &MetalLBBGPPeerHandler{},
		crd: "metallb.io_bgppeers.yaml", version: metallbPeerVersion,
		typ:       reflect.TypeFor[metallbv1beta2.BGPPeerSpec](),
		required:  metallbBGPPeerKind.required,
		defaulted: metallbBGPPeerKind.defaultedZeros.fields,
		checked: map[string]ciliumBGPRule{
			"spec.connectTime: duration(self).getSeconds() >= 1 && duration(self).getSeconds() <= 65535": {
				breaks: []map[string]any{
					metallbPeer("connectTime", "0s"),
					// Under a second: its whole seconds are none.
					metallbPeer("connectTime", "999ms"),
					metallbPeer("connectTime", "65536s"),
					metallbPeer("connectTime", "24h"),
					metallbPeer("connectTime", "-10s"),
				},
				want: "is not between 1 and 65535 seconds",
				keeps: []map[string]any{
					metallbPeer("connectTime", "1s"),
					metallbPeer("connectTime", "65535s"),
					metallbPeer("connectTime", "18h12m15s"),
					// Not authored, by either spelling: the rule is not evaluated.
					metallbPeer("connectTime", nil),
					metallbPeer("holdTime", "0s"),
				},
			},
			"spec.connectTime: duration(self).getMilliseconds() % 1000 == 0": {
				breaks: []map[string]any{
					metallbPeer("connectTime", "1500ms"),
					metallbPeer("connectTime", "10.5s"),
					// In range by its whole seconds, and no whole number of them.
					metallbPeer("connectTime", "65535.5s"),
					metallbPeer("connectTime", "1.001s"),
				},
				want: "is not a whole number of seconds",
				keeps: []map[string]any{
					metallbPeer("connectTime", "10s"),
					metallbPeer("connectTime", "2000ms"),
					// A part under a millisecond is dropped before the rule reads
					// the value, by the API server and by the kind.
					metallbPeer("connectTime", "10.0005s"),
					metallbPeer("connectTime", nil),
				},
			},
		},
		left: map[string]string{
			"spec.enableGracefulRestart: self == oldSelf": "a transition rule: it compares the field with the stored object's, which a build does not have, and the API server does not evaluate it on a create",
		},
	},
}

// metallbPeer is the least a metallb-bgppeer may author, with one more field.
func metallbPeer(field string, value any) map[string]any {
	return map[string]any{"myASN": 64512, field: value}
}

// metallbCheckedRules prepares the CRD in file as the API server serves it in
// version, defaults included, for a kind whose properties are the object's
// spec.
func metallbCheckedRules(t *testing.T, component string, handler oam.ComponentHandler, file, version string) checkedRules {
	t.Helper()
	crd, _ := metallbCRD(t, file, version)
	return checkedRules{
		create: crdCreateOf(t, crd, version), component: component, handler: handler,
		document: func(props map[string]any) map[string]any {
			return crdDocument(crd, version, map[string]any{"spec": props})
		},
	}
}

// metallbCRD reads one CRD of the linked module and returns it with the schema
// of the version the kind emits, which must be served and stored.
func metallbCRD(t *testing.T, file, version string) (*apiextensionsv1.CustomResourceDefinition, apiextensionsv1.JSONSchemaProps) {
	t.Helper()
	path := filepath.Join(linkedModuleDir(t, metallbModulePath), filepath.FromSlash(metallbCRDDir), file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	for _, served := range crd.Spec.Versions {
		if served.Name != version {
			continue
		}
		if !served.Served || !served.Storage || served.Schema == nil || served.Schema.OpenAPIV3Schema == nil {
			t.Fatalf("%s: version %s is served %v, stored %v, and has a schema: %v; update this test", file, served.Name, served.Served, served.Storage, served.Schema != nil)
		}
		return &crd, *served.Schema.OpenAPIV3Schema
	}
	t.Fatalf("%s has no %s version", file, version)
	return nil, apiextensionsv1.JSONSchemaProps{}
}

// metallbSpec is the schema of the CRD's spec, in version.
func metallbSpec(t *testing.T, file, version string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	_, root := metallbCRD(t, file, version)
	spec, ok := root.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property", file)
	}
	return spec
}

// metallbRules is every expression rule the CRD declares in version, anywhere
// in the object, by the path of the value it is declared on and its text.
func metallbRules(t *testing.T, file, version string) []string {
	t.Helper()
	_, root := metallbCRD(t, file, version)
	var declared []string
	walkCiliumBGPSchema(root, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
		for _, rule := range s.XValidations {
			declared = append(declared, path+": "+rule.Rule)
		}
	})
	slices.Sort(declared)
	return declared
}

// TestMetalLBKinds_EmitTheServedVersion: each kind's CRD is namespaced and
// names the kind the handler declares, in the group the handler declares, and
// the version the kind emits is the one the CRD serves and stores (metallbCRD
// fails on another): the spec type it decodes into is that version's, of the
// module's package of that name, and the object it builds states it.
func TestMetalLBKinds_EmitTheServedVersion(t *testing.T) {
	for version, types := range map[string]string{
		metallbVersion: metallbv1beta1.GroupVersion.Version, metallbPeerVersion: metallbv1beta2.GroupVersion.Version,
	} {
		if types != version {
			t.Fatalf("the API's types are %s, this test reads %s", types, version)
		}
	}
	minimal := map[string]map[string]any{
		"metallb-ipaddresspool": {"addresses": []any{"192.0.2.0/24"}},
		"metallb-bgppeer":       {"myASN": 64512},
	}
	versions := map[string]int{}
	for _, kind := range metallbKinds {
		versions[kind.version]++
		t.Run(kind.component, func(t *testing.T) {
			crd, _ := metallbCRD(t, kind.crd, kind.version)
			if got, want := kind.typ.PkgPath(), metallbModulePath+"/api/"+kind.version; got != want {
				t.Errorf("the kind decodes into a type of %s, want one of %s, the version the CRD stores", got, want)
			}
			props := minimal[kind.component]
			if props == nil {
				props = map[string]any{}
			}
			object, err := checkedRules{component: kind.component, handler: kind.handler}.build(t, props)
			if err != nil {
				t.Fatalf("the least the kind may author does not build: %v", err)
			}
			if got, want := object["apiVersion"], crd.Spec.Group+"/"+kind.version; got != want {
				t.Errorf("the object's apiVersion is %v, want %s", got, want)
			}
			declared, scope := kind.handler.(oam.ComponentObjectProvider).ComponentObject()
			if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
				t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
			}
			if crd.Spec.Scope != apiextensionsv1.NamespaceScoped || scope != oam.ObjectScopeNamespaced {
				t.Errorf("the CRD's scope is %s and the handler declares scope %d; want both namespaced", crd.Spec.Scope, scope)
			}
		})
	}
	// Vacuity guard: both versions are read, the peer's for the peer alone.
	if versions[metallbVersion] != len(metallbKinds)-1 || versions[metallbPeerVersion] != 1 {
		t.Errorf("the kinds emit the versions %v, want %s for one of the %d and %s for the others", versions, metallbPeerVersion, len(metallbKinds), metallbVersion)
	}
}

// TestMetalLBKinds_RequiredMatchCRD derives, from the linked module's CRD and
// the spec type, the fields of each kind that the API requires and the type
// would write unauthored, and holds the kind's required list to them. A
// dependency bump that adds, drops or moves one fails here, naming it.
//
// None of these types leaves a required field out when it is not authored. One
// that came to do so fails here: the required list cannot hold it, so the
// kind's validate refuses it on the decoded value, with a test row, or it is
// listed in this test with the reason the decoded value cannot show the
// omission.
//
// The selectors these specs hold are Kubernetes' own label selector, whose
// fields are in the CRD, so the key and the operator of a match expression are
// derived too.
func TestMetalLBKinds_RequiredMatchCRD(t *testing.T) {
	var total int
	for _, kind := range metallbKinds {
		t.Run(kind.component, func(t *testing.T) {
			fields := ciliumBGPTypeFields(kind.typ)
			crdRequired := ciliumPlainRequired(metallbSpec(t, kind.crd, kind.version))
			if len(fields) == 0 {
				t.Fatalf("the type has no field; the walk is broken")
			}
			total += len(crdRequired)
			var listed []string
			for _, path := range crdRequired {
				f, ok := fields[path]
				switch {
				case !ok:
					t.Errorf("the CRD requires %s, which is no field of %s", path, kind.typ)
				case strings.Contains(path, "{}"):
					t.Errorf("%s is required under a map value, which a required list cannot name", path)
				case !f.writtenUnauthored():
					t.Errorf("the CRD requires %s, which the type leaves out when it is not authored; refuse it in the kind's validate with a test row, or list it in this test with the reason the decoded value cannot show the omission", path)
				default:
					listed = append(listed, path)
				}
				segments := strings.Split(path, ".")
				for i := 1; i < len(segments); i++ {
					parent := strings.Join(segments[:i], ".")
					parent = strings.TrimSuffix(strings.TrimSuffix(parent, "[]"), "{}")
					above, ok := fields[parent]
					if !ok {
						t.Errorf("%s: its parent %s is no field of %s", path, parent, kind.typ)
						continue
					}
					if above.field.Type.Kind() == reflect.Struct && !slices.Contains(crdRequired, parent) {
						t.Errorf("%s is required under %s, a struct the type writes unauthored that the CRD does not require; the kind's validate must refuse it, and this test hold that", path, parent)
					}
				}
			}
			slices.Sort(listed)
			t.Logf("the CRD requires %d fields, all written unauthored: %v", len(crdRequired), listed)
			if got := slices.Sorted(maps.Keys(kind.required)); !slices.Equal(got, listed) {
				t.Errorf("required list = %v\nthe CRD and the type give %v", got, listed)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
			// The published schema marks a property required exactly where the
			// required list names it at the top level.
			for name, property := range kind.handler.(oam.PropertySchemaProvider).PropertySchema() {
				_, listed := kind.required[name]
				if property.Required != listed {
					t.Errorf("property %s: published Required = %v, the required list says %v", name, property.Required, listed)
				}
			}
		})
	}
	// Vacuity guard: the walk reads required fields. A CRD of these may require
	// none, so the count is the family's.
	if total == 0 {
		t.Errorf("the CRDs require no field at all; the walk is broken")
	}
}

// TestMetalLBKinds_NoDefaultIsLost: every default the CRDs declare under spec
// sits on a pointer field, is the value the type leaves out, or is on the
// kind's defaulted-zero list. On a pointer an authored 0 or false is written:
// `autoAssign: false` reaches the pool. On a field that is omitted when empty,
// the API server fills the default into what the type left out, and where that
// default is the empty value itself an authored one comes back the same:
// `avoidBuggyIPs: false` is a pool that hands out every address, written or
// not. A default of another value on such a number or boolean would turn an
// authored 0 or false into it: the kind's list (policyFreeKind.defaultedZeros)
// must hold exactly those fields, each with its default, so that the value is
// refused. A peer's `peerPort` is the one, and the refusal is shown.
func TestMetalLBKinds_NoDefaultIsLost(t *testing.T) {
	found := map[string]string{}
	for _, kind := range metallbKinds {
		fields := ciliumBGPTypeFields(kind.typ)
		derived := map[string]string{}
		walkCiliumBGPSchema(metallbSpec(t, kind.crd, kind.version), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
			if s.Default == nil {
				return
			}
			raw := strings.TrimSpace(string(s.Default.Raw))
			found[kind.component+": "+path] = raw
			f, ok := fields[path]
			if !ok {
				t.Errorf("%s: the CRD defaults %s, which is no field of %s", kind.component, path, kind.typ)
				return
			}
			if f.field.Type.Kind() == reflect.Pointer {
				return
			}
			var empty bool
			switch f.field.Type.Kind() {
			case reflect.Slice:
				empty = raw == "[]"
			case reflect.String:
				empty = raw == `""`
			default:
				if f.omitemptyScalar() && !crdDefaultIsZero(raw) {
					derived[path] = raw
					return
				}
				empty = f.omitemptyScalar() && crdDefaultIsZero(raw)
			}
			if !empty || !slices.Contains(f.jsonOptions(), "omitempty") {
				t.Errorf("%s: %s has the CRD default %s on a field that is no pointer (%s, json options %v); an authored empty value there would not be carried", kind.component, path, raw, f.field.Type, f.jsonOptions())
			}
		})
		if !maps.Equal(derived, kind.defaulted) {
			t.Errorf("%s: omitted when zero and defaulted to another value by the CRD: %v\nthe kind's defaulted-zero list holds %v", kind.component, derived, kind.defaulted)
		}
	}
	// Vacuity guard: the defaults of these CRDs are read, the ones on a pointer,
	// the ones on a field the type leaves out and the one that is listed.
	want := map[string]string{
		"metallb-ipaddresspool: autoAssign":             "true",
		"metallb-ipaddresspool: avoidBuggyIPs":          "false",
		"metallb-bgpadvertisement: aggregationLength":   "32",
		"metallb-bgpadvertisement: aggregationLengthV6": "128",
		"metallb-bgppeer: disableMP":                    "false",
		"metallb-bgppeer: dualStackAddressFamily":       "false",
		"metallb-bgppeer: peerPort":                     "179",
	}
	if !maps.Equal(found, want) {
		t.Errorf("the CRDs default %v, want %v", found, want)
	}

	// The list refuses: an authored 0 on the listed field does not reach the
	// object, and an authored false on a boolean whose default is false builds.
	const refusal = "peerPort: 0 cannot be carried by the MetalLB API types (the field is omitted when zero, so the API server would apply its default 179)"
	if _, err := metallbBGPPeerKind.config(&oam.Component{Name: "upstream", Properties: metallbPeer("peerPort", 0)}); err == nil || err.Error() != refusal {
		t.Errorf("an authored 0 on the defaulted-zero field: got %v, want %s", err, refusal)
	}
	for field, value := range map[string]any{"peerPort": 179, "disableMP": false, "dualStackAddressFamily": false, "peerASN": 0} {
		if _, err := metallbBGPPeerKind.config(&oam.Component{Name: "upstream", Properties: metallbPeer(field, value)}); err != nil {
			t.Errorf("%s: %v is refused (%v), want it built", field, value, err)
		}
	}
}

// TestMetalLBKinds_ExpressionRules: every expression rule the CRDs declare,
// anywhere in the object, is one its kind checks or one it leaves to the API
// server with a reason, and a checked one is held to the API server's answer,
// after the defaults the API server fills (checkedRules.show). A dependency
// bump that adds or rewords a rule fails here until it is classified.
func TestMetalLBKinds_ExpressionRules(t *testing.T) {
	for _, kind := range metallbKinds {
		t.Run(kind.component, func(t *testing.T) {
			declared := metallbRules(t, kind.crd, kind.version)
			classified := slices.Sorted(maps.Keys(kind.checked))
			for rule, why := range kind.left {
				if _, both := kind.checked[rule]; both || strings.TrimSpace(why) == "" {
					t.Errorf("rule %q is left to the API server with the reason %q, and checked: %v", rule, why, both)
				}
				classified = append(classified, rule)
			}
			slices.Sort(classified)
			if !slices.Equal(declared, classified) {
				t.Errorf("the CRD declares the rules %q\nthe kind classifies   %q", declared, classified)
			}
			if len(kind.checked) == 0 {
				return
			}
			shown := metallbCheckedRules(t, kind.component, kind.handler, kind.crd, kind.version)
			for rule, checked := range kind.checked {
				t.Run(rule, func(t *testing.T) {
					shown.show(t, crdRuleOf(t, rule), checked.want, checked.breaks, checked.keeps)
				})
			}
		})
	}
	// Vacuity guard: the walk reads rules. The BGP advertisement's CRD declares
	// one, on its spec, the peer's three, two of them on one field, and the
	// others none.
	if declared := metallbRules(t, "metallb.io_bgpadvertisements.yaml", metallbVersion); len(declared) != 1 || !strings.HasPrefix(declared[0], "spec: ") {
		t.Errorf("the module's BGP advertisement declares the rules %q, want the one on its spec", declared)
	}
	if declared := metallbRules(t, "metallb.io_bgppeers.yaml", metallbPeerVersion); len(declared) != 3 {
		t.Errorf("the module's BGP peer declares the rules %q, want three", declared)
	}

	// The transition rule is the one the kind leaves: the API server does not
	// evaluate it on a create, so a peer that sets the field is accepted, and
	// the kind builds it.
	peer := metallbCheckedRules(t, "metallb-bgppeer", &MetalLBBGPPeerHandler{}, "metallb.io_bgppeers.yaml", metallbPeerVersion)
	for _, props := range []map[string]any{metallbPeer("enableGracefulRestart", true), metallbPeer("enableGracefulRestart", false)} {
		peer.create.create(t, peer.document(props)).accepted(t, "a created peer")
		object, err := peer.build(t, props)
		if err != nil {
			t.Errorf("the kind refuses %v: %v", props, err)
			continue
		}
		peer.create.create(t, object).accepted(t, "the object the kind emits")
	}
}
