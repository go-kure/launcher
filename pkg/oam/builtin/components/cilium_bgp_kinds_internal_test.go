package components

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// ciliumBGPModulePath is the module whose CRDs the tests below read: the ones
// under its cilium.io client package, next to the API's types.
const ciliumBGPModulePath = "github.com/cilium/cilium"

// ciliumBGPVersion is the version of the API the kinds emit and whose schema
// the tests read.
const ciliumBGPVersion = "v2"

// ciliumBGPRule is one expression rule of a CRD a kind checks: properties that
// break it, the refusal, and the properties next to them that keep the rule,
// which differ in what the rule reads. Where the rule reads whether a field is
// set, the field authored as null is among them: an absent field to the API
// server, which drops the null before it evaluates the rule, and to the kind.
type ciliumBGPRule struct {
	breaks []map[string]any
	want   string
	keeps  []map[string]any
}

// ciliumBGPKinds lists the kind components of Cilium's BGP control plane with
// the CRD each emits an object of, the spec type it decodes into and its
// required list.
//
// refused names the fields the CRD requires that the list does not hold,
// because the type leaves each out when it is not authored: the kind's
// validate refuses the entry without one, which
// TestKindComponents_OmittedRequiredAndWrittenDefaults shows. checked names the CRD's
// expression rules the kind's validate holds, by the path of the value they
// are declared on and their text, each with a case that breaks it. A rule in
// neither checked nor left fails TestCiliumBGPKinds_ExpressionRules.
var ciliumBGPKinds = []struct {
	component string
	handler   oam.ComponentHandler
	crd       string
	typ       reflect.Type
	required  map[string]string
	refused   []string
	checked   map[string]ciliumBGPRule
	left      map[string]string
}{
	{
		component: "cilium-bgpadvertisement", handler: &CiliumBGPAdvertisementHandler{},
		crd: "ciliumbgpadvertisements.yaml", typ: reflect.TypeFor[ciliumv2.CiliumBGPAdvertisementSpec](),
		required: ciliumBGPAdvertisementKind.required,
		refused:  []string{"advertisements[].interface.name", "advertisements[].service.addresses"},
		checked: map[string]ciliumBGPRule{
			"spec.advertisements[]: self.advertisementType != 'Service' || has(self.service)": {
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "Service"},
					map[string]any{"advertisementType": "Service", "service": nil},
				),
				`advertisements[0].service: required with advertisementType "Service"`,
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "Service", "service": map[string]any{"addresses": []any{"ClusterIP"}}},
				),
			},
			"spec.advertisements[]: self.advertisementType == 'Service' || !has(self.service)": {
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "PodCIDR", "service": map[string]any{"addresses": []any{"ClusterIP"}}},
				),
				`advertisements[0].service: not allowed with advertisementType "PodCIDR", only with "Service"`,
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "PodCIDR"},
					map[string]any{"advertisementType": "PodCIDR", "service": nil},
				),
			},
			"spec.advertisements[]: self.advertisementType != 'Interface' || has(self.interface)": {
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "Interface"},
					map[string]any{"advertisementType": "Interface", "interface": nil},
				),
				`advertisements[0].interface: required with advertisementType "Interface"`,
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "Interface", "interface": map[string]any{"name": "lo"}},
				),
			},
			"spec.advertisements[]: self.advertisementType == 'Interface' || !has(self.interface)": {
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "CiliumPodIPPool", "interface": map[string]any{"name": "lo"}},
				),
				`advertisements[0].interface: not allowed with advertisementType "CiliumPodIPPool", only with "Interface"`,
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "CiliumPodIPPool"},
					map[string]any{"advertisementType": "CiliumPodIPPool", "interface": nil},
				),
			},
			"spec.advertisements[]: self.advertisementType != 'PodCIDR' || !has(self.selector)": {
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "PodCIDR", "selector": map[string]any{}},
				),
				`advertisements[0].selector: not allowed with advertisementType "PodCIDR"`,
				ciliumBGPAdvertisements(
					map[string]any{"advertisementType": "CiliumPodIPPool", "selector": map[string]any{}},
					map[string]any{"advertisementType": "PodCIDR"},
					map[string]any{"advertisementType": "PodCIDR", "selector": nil},
				),
			},
		},
	},
	{
		component: "cilium-bgpclusterconfig", handler: &CiliumBGPClusterConfigHandler{},
		crd: "ciliumbgpclusterconfigs.yaml", typ: reflect.TypeFor[ciliumv2.CiliumBGPClusterConfigSpec](),
		required: ciliumBGPClusterConfigKind.required,
	},
	{
		component: "cilium-bgpnodeconfigoverride", handler: &CiliumBGPNodeConfigOverrideHandler{},
		crd: "ciliumbgpnodeconfigoverrides.yaml", typ: reflect.TypeFor[ciliumv2.CiliumBGPNodeConfigOverrideSpec](),
		required: ciliumBGPNodeConfigOverrideKind.required,
	},
	{
		component: "cilium-bgppeerconfig", handler: &CiliumBGPPeerConfigHandler{},
		crd: "ciliumbgppeerconfigs.yaml", typ: reflect.TypeFor[ciliumv2.CiliumBGPPeerConfigSpec](),
		required: ciliumBGPPeerConfigKind.required,
		// Checked where both of its fields are authored. With one authored the
		// API server compares it with the default the installed CRD fills for
		// the other; TestCiliumBGPKinds_ExpressionRules holds that both have
		// one.
		checked: map[string]ciliumBGPRule{
			"spec.timers: self.keepAliveTimeSeconds <= self.holdTimeSeconds": {
				[]map[string]any{{"timers": map[string]any{"keepAliveTimeSeconds": 31, "holdTimeSeconds": 30}}},
				"timers.keepAliveTimeSeconds: 31 is larger than timers.holdTimeSeconds (30)",
				[]map[string]any{{"timers": map[string]any{"keepAliveTimeSeconds": 30, "holdTimeSeconds": 30}}},
			},
		},
	},
}

// ciliumBGPAdvertisements is, for each entry, the properties of a
// cilium-bgpadvertisement with that one entry.
func ciliumBGPAdvertisements(entries ...map[string]any) []map[string]any {
	var out []map[string]any
	for _, entry := range entries {
		out = append(out, map[string]any{"advertisements": []any{entry}})
	}
	return out
}

// ciliumBGPCRD reads one CRD of the linked module and returns it with the
// schema of the version the kinds emit, which must be served and stored.
func ciliumBGPCRD(t *testing.T, file string) (*apiextensionsv1.CustomResourceDefinition, apiextensionsv1.JSONSchemaProps) {
	t.Helper()
	path := filepath.Join(linkedModuleDir(t, ciliumBGPModulePath), "pkg", "k8s", "apis", "cilium.io", "client", "crds", ciliumBGPVersion, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	for _, version := range crd.Spec.Versions {
		if version.Name != ciliumBGPVersion {
			continue
		}
		if !version.Served || !version.Storage || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			t.Fatalf("%s: version %s is served %v, stored %v, and has a schema: %v; update this test", file, version.Name, version.Served, version.Storage, version.Schema != nil)
		}
		return &crd, *version.Schema.OpenAPIV3Schema
	}
	t.Fatalf("%s has no %s version", file, ciliumBGPVersion)
	return nil, apiextensionsv1.JSONSchemaProps{}
}

// walkCiliumBGPSchema visits every schema under s with its json path: a
// property under its name, a list element under [], a map value under {}.
func walkCiliumBGPSchema(s apiextensionsv1.JSONSchemaProps, path string, visit func(path string, s apiextensionsv1.JSONSchemaProps)) {
	visit(path, s)
	for name, prop := range s.Properties {
		child := name
		if path != "" {
			child = path + "." + name
		}
		walkCiliumBGPSchema(prop, child, visit)
	}
	if s.Items != nil && s.Items.Schema != nil {
		walkCiliumBGPSchema(*s.Items.Schema, path+"[]", visit)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		walkCiliumBGPSchema(*s.AdditionalProperties.Schema, path+"{}", visit)
	}
}

// ciliumBGPSpec is the schema of the CRD's spec.
func ciliumBGPSpec(t *testing.T, file string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	_, root := ciliumBGPCRD(t, file)
	spec, ok := root.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property", file)
	}
	return spec
}

// ciliumBGPTypeFields is every field the encoding of typ reaches, by json
// path, as walkMonitoringFields reads it.
func ciliumBGPTypeFields(typ reflect.Type) map[string]kindField {
	fields := map[string]kindField{}
	walkMonitoringFields(typ, nil, func(f kindField) { fields[f.path] = f })
	return fields
}

// TestCiliumBGPKinds_EmitTheServedVersion: each kind's CRD is cluster-scoped
// and names the kind the handler declares, in the group the handler declares,
// and the version the kinds emit is the one the CRD serves and stores.
func TestCiliumBGPKinds_EmitTheServedVersion(t *testing.T) {
	if ciliumv2.CustomResourceDefinitionVersion != ciliumBGPVersion {
		t.Fatalf("the API's types are %s, this test reads %s", ciliumv2.CustomResourceDefinitionVersion, ciliumBGPVersion)
	}
	for _, kind := range ciliumBGPKinds {
		t.Run(kind.component, func(t *testing.T) {
			crd, _ := ciliumBGPCRD(t, kind.crd)
			declared, scope := kind.handler.(oam.ComponentObjectProvider).ComponentObject()
			if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
				t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
			}
			if crd.Spec.Scope != apiextensionsv1.ClusterScoped || scope != oam.ObjectScopeCluster {
				t.Errorf("the CRD's scope is %s and the handler declares scope %d; want both cluster-scoped", crd.Spec.Scope, scope)
			}
		})
	}
}

// TestCiliumBGPKinds_RequiredMatchCRD derives, from the linked module's CRD
// and the spec type, the fields of each kind that the API requires and the
// type would write unauthored, and holds the kind's required list to them. The
// fields the API requires and the type leaves out unauthored are held to the
// row's refused list. A dependency bump that adds, drops or moves one fails
// here, naming it.
//
// The selector these specs embed is Cilium's own type and its fields are in
// the CRD, so the key and the operator of a match expression are derived too.
func TestCiliumBGPKinds_RequiredMatchCRD(t *testing.T) {
	for _, kind := range ciliumBGPKinds {
		t.Run(kind.component, func(t *testing.T) {
			fields := ciliumBGPTypeFields(kind.typ)
			var crdRequired []string
			walkCiliumBGPSchema(ciliumBGPSpec(t, kind.crd), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				for _, name := range s.Required {
					if path != "" {
						name = path + "." + name
					}
					crdRequired = append(crdRequired, name)
				}
			})
			if len(crdRequired) == 0 || len(fields) == 0 {
				t.Fatalf("the CRD requires %d fields and the type has %d; a walk is broken", len(crdRequired), len(fields))
			}
			var listed, omitted []string
			for _, path := range crdRequired {
				f, ok := fields[path]
				switch {
				case !ok:
					t.Errorf("the CRD requires %s, which is no field of %s", path, kind.typ)
				case strings.Contains(path, "{}"):
					t.Errorf("%s is required under a map value, which a required list cannot name", path)
				case f.forced:
					t.Errorf("%s is required under a struct the type writes unauthored; the kind's validate must refuse it, and this test hold that", path)
				case f.writtenUnauthored():
					listed = append(listed, path)
				default:
					omitted = append(omitted, path)
				}
			}
			slices.Sort(listed)
			slices.Sort(omitted)
			t.Logf("the CRD requires %d fields; written unauthored: %v; left out unauthored: %v", len(crdRequired), listed, omitted)
			if got := slices.Sorted(maps.Keys(kind.required)); !slices.Equal(got, listed) {
				t.Errorf("required list = %v\nthe CRD and the type give %v", got, listed)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
			if want := slices.Sorted(slices.Values(kind.refused)); !slices.Equal(omitted, want) {
				t.Errorf("required by the CRD and left out unauthored by the type = %v, the row says %v", omitted, want)
			}
		})
	}
}

// TestCiliumBGPKinds_DefaultsSitOnPointers: every default the CRDs declare
// under spec sits on a field that is a pointer in the Go type. Such a field is
// left out when it is not authored, so the API server fills its default, and
// an authored 0 or false is written and kept. A default on a field that is no
// pointer would need a list policyFreeKind does not carry: a defaulted-zero
// one where the field is omitted when zero, a written default where it is
// always encoded.
func TestCiliumBGPKinds_DefaultsSitOnPointers(t *testing.T) {
	found := map[string]string{}
	for _, kind := range ciliumBGPKinds {
		fields := ciliumBGPTypeFields(kind.typ)
		walkCiliumBGPSchema(ciliumBGPSpec(t, kind.crd), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
			if s.Default == nil {
				return
			}
			found[kind.component+": "+path] = strings.TrimSpace(string(s.Default.Raw))
			f, ok := fields[path]
			switch {
			case !ok:
				t.Errorf("%s: the CRD defaults %s, which is no field of %s", kind.component, path, kind.typ)
			case f.field.Type.Kind() != reflect.Pointer:
				t.Errorf("%s: %s has the CRD default %s and is no pointer in the Go type (%s)", kind.component, path, s.Default.Raw, f.field.Type)
			}
		})
	}
	// Vacuity guard: the defaults are read, a zero one and others.
	for at, want := range map[string]string{
		"cilium-bgpclusterconfig: bgpInstances[].peers[].peerASN": "0",
		"cilium-bgppeerconfig: timers.holdTimeSeconds":            "90",
		"cilium-bgppeerconfig: transport.peerPort":                "179",
	} {
		if found[at] != want {
			t.Errorf("%s has the default %q, want %s; the walk found %v", at, found[at], want, found)
		}
	}
}

// TestCiliumBGPKinds_ExpressionRules: every expression rule the CRDs declare,
// anywhere in the object, is one a kind checks or one it leaves to the API
// server with a reason, and a checked one is refused with a text that names
// both of its fields. A dependency bump that adds or rewords a rule fails
// here until it is classified.
func TestCiliumBGPKinds_ExpressionRules(t *testing.T) {
	for _, kind := range ciliumBGPKinds {
		t.Run(kind.component, func(t *testing.T) {
			_, root := ciliumBGPCRD(t, kind.crd)
			var declared []string
			walkCiliumBGPSchema(root, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				for _, rule := range s.XValidations {
					declared = append(declared, path+": "+rule.Rule)
				}
			})
			slices.Sort(declared)
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
			// A checked rule is the API server's: see checkedRules.show.
			shown := ciliumCheckedRules(t, kind.component, kind.handler, kind.crd)
			for rule, checked := range kind.checked {
				t.Run(rule, func(t *testing.T) {
					shown.show(t, crdRuleOf(t, rule), checked.want, checked.breaks, checked.keeps)
				})
			}
		})
	}

	// The timers rule compares two fields the CRD defaults, so the API server
	// evaluates it on a timers block that authors one of them. That case is
	// the one cilium-bgppeerconfig leaves to it: the API server refuses each of
	// these by the rule, against the default it fills, and the kind builds
	// them.
	const peerCRD = "ciliumbgppeerconfigs.yaml"
	spec := ciliumBGPSpec(t, peerCRD)
	timers := spec.Properties["timers"]
	for _, name := range []string{"keepAliveTimeSeconds", "holdTimeSeconds"} {
		if timers.Properties[name].Default == nil {
			t.Errorf("timers.%s has no CRD default: with one of the two authored the rule no longer compares it with a default, and the kind can check it", name)
		}
	}
	peer := ciliumCheckedRules(t, "cilium-bgppeerconfig", &CiliumBGPPeerConfigHandler{}, peerCRD)
	only, says := peer.create.only(t, crdRule{path: "spec.timers", rule: "self.keepAliveTimeSeconds <= self.holdTimeSeconds"})
	for name, authored := range map[string]map[string]any{
		"a keepalive above the default hold time":   {"keepAliveTimeSeconds": 91},
		"a hold time under the default keepalive":   {"holdTimeSeconds": 29},
		"a keepalive that is the default hold time": {"keepAliveTimeSeconds": 90},
		"a hold time that is the default keepalive": {"holdTimeSeconds": 30},
	} {
		props := map[string]any{"timers": authored}
		answer := only.create(t, peer.document(props))
		if strings.Contains(name, " that is ") {
			answer.accepted(t, name)
		} else {
			answer.refusedByRule(t, name, says)
		}
		if _, err := peer.build(t, props); err != nil {
			t.Errorf("%s: the kind refuses it (%v); a timers block with one field authored is left to the API server", name, err)
		}
	}
}

// ciliumCheckedRules prepares the CRD in file as the API server serves it, for
// a kind whose properties are the object's spec.
func ciliumCheckedRules(t *testing.T, component string, handler oam.ComponentHandler, file string) checkedRules {
	t.Helper()
	crd, _ := ciliumBGPCRD(t, file)
	return checkedRules{
		create: crdCreateOf(t, crd, ciliumBGPVersion), component: component, handler: handler,
		document: func(props map[string]any) map[string]any {
			return crdDocument(crd, ciliumBGPVersion, map[string]any{"spec": props})
		},
	}
}
