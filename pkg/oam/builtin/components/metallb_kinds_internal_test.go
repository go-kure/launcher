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

// metallbVersion is the version of the API the kinds emit and whose schema the
// tests read.
const metallbVersion = "v1beta1"

// metallbKinds lists the kind components of MetalLB's API
// (go-kure/launcher#790), each with the CRD it emits an object of, the spec
// type it decodes into and its required list. Every one is namespaced.
//
// checked names the CRD's expression rules the kind's validate holds, by the
// path of the value they are declared on and their text, each with properties
// that break it and properties that keep it (ciliumBGPRule, which the rules of
// Cilium's BGP kinds are listed with). A rule a CRD declares that is not
// checked fails TestMetalLBKinds_ExpressionRules.
var metallbKinds = []struct {
	component string
	handler   oam.ComponentHandler
	crd       string
	typ       reflect.Type
	required  map[string]string
	checked   map[string]ciliumBGPRule
}{
	{
		component: "metallb-ipaddresspool", handler: &MetalLBIPAddressPoolHandler{},
		crd: "metallb.io_ipaddresspools.yaml", typ: reflect.TypeFor[metallbv1beta1.IPAddressPoolSpec](),
		required: metallbIPAddressPoolKind.required,
	},
	{
		component: "metallb-l2advertisement", handler: &MetalLBL2AdvertisementHandler{},
		crd: "metallb.io_l2advertisements.yaml", typ: reflect.TypeFor[metallbv1beta1.L2AdvertisementSpec](),
		required: metallbL2AdvertisementKind.required,
	},
	{
		component: "metallb-bgpadvertisement", handler: &MetalLBBGPAdvertisementHandler{},
		crd: "metallb.io_bgpadvertisements.yaml", typ: reflect.TypeFor[metallbv1beta1.BGPAdvertisementSpec](),
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
		crd: "metallb.io_bfdprofiles.yaml", typ: reflect.TypeFor[metallbv1beta1.BFDProfileSpec](),
		required: metallbBFDProfileKind.required,
	},
	{
		// The CRD requires no field of a Community or of an alias, and declares
		// no rule.
		component: "metallb-community", handler: &MetalLBCommunityHandler{},
		crd: "metallb.io_communities.yaml", typ: reflect.TypeFor[metallbv1beta1.CommunitySpec](),
		required: metallbCommunityKind.required,
	},
}

// metallbCheckedRules prepares the CRD in file as the API server serves it,
// defaults included, for a kind whose properties are the object's spec.
func metallbCheckedRules(t *testing.T, component string, handler oam.ComponentHandler, file string) checkedRules {
	t.Helper()
	crd, _ := metallbCRD(t, file)
	return checkedRules{
		create: crdCreateOf(t, crd, metallbVersion), component: component, handler: handler,
		document: func(props map[string]any) map[string]any {
			return crdDocument(crd, metallbVersion, map[string]any{"spec": props})
		},
	}
}

// metallbCRD reads one CRD of the linked module and returns it with the schema
// of the version the kinds emit, which must be served and stored.
func metallbCRD(t *testing.T, file string) (*apiextensionsv1.CustomResourceDefinition, apiextensionsv1.JSONSchemaProps) {
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
	for _, version := range crd.Spec.Versions {
		if version.Name != metallbVersion {
			continue
		}
		if !version.Served || !version.Storage || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			t.Fatalf("%s: version %s is served %v, stored %v, and has a schema: %v; update this test", file, version.Name, version.Served, version.Storage, version.Schema != nil)
		}
		return &crd, *version.Schema.OpenAPIV3Schema
	}
	t.Fatalf("%s has no %s version", file, metallbVersion)
	return nil, apiextensionsv1.JSONSchemaProps{}
}

// metallbSpec is the schema of the CRD's spec.
func metallbSpec(t *testing.T, file string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	_, root := metallbCRD(t, file)
	spec, ok := root.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property", file)
	}
	return spec
}

// metallbRules is every expression rule the CRD declares, anywhere in the
// object, by the path of the value it is declared on and its text.
func metallbRules(t *testing.T, file string) []string {
	t.Helper()
	_, root := metallbCRD(t, file)
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
// the version the kinds emit is the one the CRD serves and stores (metallbCRD
// fails on another).
func TestMetalLBKinds_EmitTheServedVersion(t *testing.T) {
	if metallbv1beta1.GroupVersion.Version != metallbVersion {
		t.Fatalf("the API's types are %s, this test reads %s", metallbv1beta1.GroupVersion.Version, metallbVersion)
	}
	for _, kind := range metallbKinds {
		t.Run(kind.component, func(t *testing.T) {
			crd, _ := metallbCRD(t, kind.crd)
			declared, scope := kind.handler.(oam.ComponentObjectProvider).ComponentObject()
			if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
				t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
			}
			if crd.Spec.Scope != apiextensionsv1.NamespaceScoped || scope != oam.ObjectScopeNamespaced {
				t.Errorf("the CRD's scope is %s and the handler declares scope %d; want both namespaced", crd.Spec.Scope, scope)
			}
		})
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
			crdRequired := ciliumPlainRequired(metallbSpec(t, kind.crd))
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
		})
	}
	// Vacuity guard: the walk reads required fields. A CRD of these may require
	// none, so the count is the family's.
	if total == 0 {
		t.Errorf("the CRDs require no field at all; the walk is broken")
	}
}

// TestMetalLBKinds_NoDefaultIsLost: every default the CRDs declare under spec
// sits on a pointer field, or is the value the type leaves out. On a pointer
// an authored 0 or false is written: `autoAssign: false` reaches the pool. On
// a field that is omitted when empty, the API server fills the default into
// what the type left out, and where that default is the empty value itself an
// authored one comes back the same: `avoidBuggyIPs: false` is a pool that
// hands out every address, written or not. A default of another value on such
// a field would turn an authored false into it. These kinds leave
// policyFreeKind's defaulted-zero list empty, so such a default fails here.
func TestMetalLBKinds_NoDefaultIsLost(t *testing.T) {
	found := map[string]string{}
	for _, kind := range metallbKinds {
		fields := ciliumBGPTypeFields(kind.typ)
		walkCiliumBGPSchema(metallbSpec(t, kind.crd), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
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
				empty = f.omitemptyScalar() && crdDefaultIsZero(raw)
			}
			if !empty || !slices.Contains(f.jsonOptions(), "omitempty") {
				t.Errorf("%s: %s has the CRD default %s on a field that is no pointer (%s, json options %v); an authored empty value there would not be carried", kind.component, path, raw, f.field.Type, f.jsonOptions())
			}
		})
	}
	// Vacuity guard: the defaults of these CRDs are read, the ones on a pointer
	// and the one on a field the type leaves out.
	want := map[string]string{
		"metallb-ipaddresspool: autoAssign":             "true",
		"metallb-ipaddresspool: avoidBuggyIPs":          "false",
		"metallb-bgpadvertisement: aggregationLength":   "32",
		"metallb-bgpadvertisement: aggregationLengthV6": "128",
	}
	if !maps.Equal(found, want) {
		t.Errorf("the CRDs default %v, want %v", found, want)
	}
}

// TestMetalLBKinds_ExpressionRules: every expression rule the CRDs declare,
// anywhere in the object, is one its kind checks, and a checked one is held to
// the API server's answer, after the defaults the API server fills
// (checkedRules.show). A dependency bump that adds or rewords a rule fails
// here until it is classified.
func TestMetalLBKinds_ExpressionRules(t *testing.T) {
	for _, kind := range metallbKinds {
		t.Run(kind.component, func(t *testing.T) {
			declared := metallbRules(t, kind.crd)
			if classified := slices.Sorted(maps.Keys(kind.checked)); !slices.Equal(declared, classified) {
				t.Errorf("the CRD declares the rules %q\nthe kind checks       %q", declared, classified)
			}
			if len(kind.checked) == 0 {
				return
			}
			shown := metallbCheckedRules(t, kind.component, kind.handler, kind.crd)
			for rule, checked := range kind.checked {
				t.Run(rule, func(t *testing.T) {
					shown.show(t, crdRuleOf(t, rule), checked.want, checked.breaks, checked.keeps)
				})
			}
		})
	}
	// Vacuity guard: the walk reads rules. The BGP advertisement's CRD declares
	// one, on its spec, and the others none.
	if declared := metallbRules(t, "metallb.io_bgpadvertisements.yaml"); len(declared) != 1 || !strings.HasPrefix(declared[0], "spec: ") {
		t.Errorf("the module's BGP advertisement declares the rules %q, want the one on its spec", declared)
	}
}
