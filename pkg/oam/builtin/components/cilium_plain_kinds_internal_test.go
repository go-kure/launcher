package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// ciliumPlainKinds lists five kind components of Cilium's API outside its BGP
// control plane (go-kure/launcher#790), each with the CRD it emits an object
// of, the spec type it decodes into and its required list. The CRDs are the
// linked module's, read as the BGP kinds' are (ciliumBGPCRD).
//
// left names the CRD's expression rules the kind leaves to the API server, by
// the path of the value they are declared on and their text, each with the
// reason. None of these kinds checks one: a rule that is not in left fails
// TestCiliumPlainKinds_ExpressionRules.
var ciliumPlainKinds = []struct {
	component  string
	handler    oam.ComponentHandler
	crd        string
	typ        reflect.Type
	namespaced bool
	required   map[string]string
	left       map[string]string
}{
	{
		component: "cilium-cidrgroup", handler: &CiliumCIDRGroupHandler{},
		crd: "ciliumcidrgroups.yaml", typ: reflect.TypeFor[ciliumv2.CiliumCIDRGroupSpec](),
		required: ciliumCIDRGroupKind.required,
	},
	{
		component: "cilium-loadbalancerippool", handler: &CiliumLoadBalancerIPPoolHandler{},
		crd: "ciliumloadbalancerippools.yaml", typ: reflect.TypeFor[ciliumv2.CiliumLoadBalancerIPPoolSpec](),
		required: ciliumLoadBalancerIPPoolKind.required,
	},
	{
		component: "cilium-egressgatewaypolicy", handler: &CiliumEgressGatewayPolicyHandler{},
		crd: "ciliumegressgatewaypolicies.yaml", typ: reflect.TypeFor[ciliumv2.CiliumEgressGatewayPolicySpec](),
		required: ciliumEgressGatewayPolicyKind.required,
		left: map[string]string{
			"spec.egressGateway.egressIP: self == '' || isIP(self)":    "the form of one field's value, not a comparison of authored fields",
			"spec.egressGateways[].egressIP: self == '' || isIP(self)": "the form of one field's value, not a comparison of authored fields",
		},
	},
	{
		component: "cilium-localredirectpolicy", handler: &CiliumLocalRedirectPolicyHandler{},
		crd: "ciliumlocalredirectpolicies.yaml", typ: reflect.TypeFor[ciliumv2.CiliumLocalRedirectPolicySpec](),
		namespaced: true,
		required:   ciliumLocalRedirectPolicyKind.required,
		left: map[string]string{
			"spec.redirectFrontend: self == oldSelf":        ciliumPlainTransitionRule,
			"spec.redirectBackend: self == oldSelf":         ciliumPlainTransitionRule,
			"spec.skipRedirectFromBackend: self == oldSelf": ciliumPlainTransitionRule,
		},
	},
	{
		component: "cilium-nodeconfig", handler: &CiliumNodeConfigHandler{},
		crd: "ciliumnodeconfigs.yaml", typ: reflect.TypeFor[ciliumv2.CiliumNodeConfigSpec](),
		namespaced: true,
		required:   ciliumNodeConfigKind.required,
	},
}

// ciliumPlainTransitionRule is why a rule that reads oldSelf is left to the
// API server: it compares the object with the stored one, and a build has
// none.
const ciliumPlainTransitionRule = "a transition rule: it compares a field with its value on the stored object, which a build does not have"

// ciliumPlainRequired is every field the CRD's spec requires, by json path.
func ciliumPlainRequired(spec apiextensionsv1.JSONSchemaProps) []string {
	var required []string
	walkCiliumBGPSchema(spec, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
		for _, name := range s.Required {
			if path != "" {
				name = path + "." + name
			}
			required = append(required, name)
		}
	})
	return required
}

// TestCiliumPlainKinds_EmitTheServedVersion: each kind's CRD names the kind
// the handler declares, in the group the handler declares and with the scope
// it declares, and the version the kinds emit is the one the CRD serves and
// stores (ciliumBGPCRD fails on another).
func TestCiliumPlainKinds_EmitTheServedVersion(t *testing.T) {
	if ciliumv2.CustomResourceDefinitionVersion != ciliumBGPVersion {
		t.Fatalf("the API's types are %s, this test reads %s", ciliumv2.CustomResourceDefinitionVersion, ciliumBGPVersion)
	}
	var namespaced int
	for _, kind := range ciliumPlainKinds {
		t.Run(kind.component, func(t *testing.T) {
			crd, _ := ciliumBGPCRD(t, kind.crd)
			declared, scope := kind.handler.(oam.ComponentObjectProvider).ComponentObject()
			if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
				t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
			}
			wantCRD, wantScope := apiextensionsv1.ClusterScoped, oam.ObjectScopeCluster
			if kind.namespaced {
				wantCRD, wantScope = apiextensionsv1.NamespaceScoped, oam.ObjectScopeNamespaced
				namespaced++
			}
			if crd.Spec.Scope != wantCRD || scope != wantScope {
				t.Errorf("the CRD's scope is %s and the handler declares scope %d; want %s and %d", crd.Spec.Scope, scope, wantCRD, wantScope)
			}
		})
	}
	// Vacuity guard: both scopes are read.
	if namespaced == 0 || namespaced == len(ciliumPlainKinds) {
		t.Errorf("%d of %d kinds are namespaced; want some of each scope", namespaced, len(ciliumPlainKinds))
	}
}

// TestCiliumPlainKinds_RequiredMatchCRD derives, from the linked module's CRD
// and the spec type, the fields of each kind that the API requires and the
// type would write unauthored, and holds the kind's required list to them. A
// dependency bump that adds, drops or moves one fails here, naming it.
//
// None of these types leaves a required field out when it is not authored. One
// that came to do so fails here: the required list cannot hold it, so the
// kind's validate refuses it on the decoded value, with a test row, or it is
// listed in this test with the reason the decoded value cannot show the
// omission.
//
// A required field may sit under a struct that is no pointer (a redirect
// backend's selector and ports). The list reads what was authored, so such a
// struct must be required itself: then it is authored wherever its fields are
// looked for.
func TestCiliumPlainKinds_RequiredMatchCRD(t *testing.T) {
	for _, kind := range ciliumPlainKinds {
		t.Run(kind.component, func(t *testing.T) {
			fields := ciliumBGPTypeFields(kind.typ)
			crdRequired := ciliumPlainRequired(ciliumBGPSpec(t, kind.crd))
			if len(crdRequired) == 0 || len(fields) == 0 {
				t.Fatalf("the CRD requires %d fields and the type has %d; a walk is broken", len(crdRequired), len(fields))
			}
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
}

// TestCiliumPlainKinds_NoDefaultIsLost: every default the CRDs declare under
// spec sits on a pointer field, or is the value the type leaves out. On a
// pointer an authored 0 or false is written. On a field that is omitted when
// empty, the API server fills the default into what the type left out, and
// where that default is the empty value itself an authored one comes back the
// same: `disabled: false` is a pool that allocates, written or not. A default
// of another value on such a field would turn an authored false into it, and
// none of these kinds sets a defaulted-zero list (policyFreeKind.defaultedZeros)
// to refuse that.
func TestCiliumPlainKinds_NoDefaultIsLost(t *testing.T) {
	found := map[string]string{}
	for _, kind := range ciliumPlainKinds {
		fields := ciliumBGPTypeFields(kind.typ)
		walkCiliumBGPSchema(ciliumBGPSpec(t, kind.crd), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
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
	// Vacuity guard: the three defaults of these CRDs are read.
	want := map[string]string{
		"cilium-loadbalancerippool: disabled":                 "false",
		"cilium-egressgatewaypolicy: egressGateways":          "[]",
		"cilium-localredirectpolicy: skipRedirectFromBackend": "false",
	}
	if !maps.Equal(found, want) {
		t.Errorf("the CRDs default %v, want %v", found, want)
	}
}

// TestCiliumPlainKinds_ExpressionRules: every expression rule the CRDs
// declare, anywhere in the object, is one the kind leaves to the API server
// with a reason. A dependency bump that adds or rewords a rule fails here until
// it is classified, and a rule left as a transition rule must read the stored
// object.
func TestCiliumPlainKinds_ExpressionRules(t *testing.T) {
	var total int
	for _, kind := range ciliumPlainKinds {
		t.Run(kind.component, func(t *testing.T) {
			_, root := ciliumBGPCRD(t, kind.crd)
			var declared []string
			walkCiliumBGPSchema(root, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				for _, rule := range s.XValidations {
					declared = append(declared, path+": "+rule.Rule)
				}
			})
			slices.Sort(declared)
			total += len(declared)
			if left := slices.Sorted(maps.Keys(kind.left)); !slices.Equal(declared, left) {
				t.Errorf("the CRD declares the rules %q\nthe kind leaves        %q", declared, left)
			}
			for rule, why := range kind.left {
				if strings.TrimSpace(why) == "" {
					t.Errorf("rule %q is left to the API server with no reason", rule)
				}
				if why == ciliumPlainTransitionRule && !strings.Contains(rule, "oldSelf") {
					t.Errorf("rule %q is left as a transition rule and does not read oldSelf", rule)
				}
			}
		})
	}
	// Vacuity guard: the walk reads rules.
	if total != 5 {
		t.Errorf("the five CRDs declare %d expression rules, want 5", total)
	}
}

// TestCiliumPlainKinds_RedirectFrontendTakesOneMatcher holds the refusal of
// validateCiliumLocalRedirectPolicy to the CRD: the schema of
// `redirectFrontend` is a one-of over its two matchers, each required in its
// branch, and no other schema under the five specs combines schemas (oneOf,
// anyOf, allOf, not), so no refusal of that shape goes unread.
func TestCiliumPlainKinds_RedirectFrontendTakesOneMatcher(t *testing.T) {
	var combined []string
	for _, kind := range ciliumPlainKinds {
		walkCiliumBGPSchema(ciliumBGPSpec(t, kind.crd), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
			if len(s.OneOf)+len(s.AnyOf)+len(s.AllOf) > 0 || s.Not != nil {
				combined = append(combined, kind.component+": "+path)
			}
		})
	}
	if want := []string{"cilium-localredirectpolicy: redirectFrontend"}; !slices.Equal(combined, want) {
		t.Fatalf("schemas that combine schemas: %v, want %v", combined, want)
	}

	frontend := ciliumBGPSpec(t, "ciliumlocalredirectpolicies.yaml").Properties["redirectFrontend"]
	var branches []string
	for _, branch := range frontend.OneOf {
		branches = append(branches, strings.Join(branch.Required, "+"))
	}
	slices.Sort(branches)
	if want := []string{"addressMatcher", "serviceMatcher"}; len(frontend.AnyOf)+len(frontend.AllOf) != 0 || frontend.Not != nil || !slices.Equal(branches, want) {
		t.Fatalf("redirectFrontend's one-of requires %v, want one branch for each of %v and nothing else", branches, want)
	}

	handler := &CiliumLocalRedirectPolicyHandler{}
	port := map[string]any{"port": "80", "protocol": "TCP"}
	address := map[string]any{"ip": "169.254.169.254", "toPorts": []any{port}}
	service := map[string]any{"serviceName": "kube-dns", "namespace": "kube-system"}
	for name, tc := range map[string]struct {
		frontend map[string]any
		want     string
	}{
		"neither":      {map[string]any{}, "redirectFrontend: one of addressMatcher and serviceMatcher is required"},
		"both":         {map[string]any{"addressMatcher": address, "serviceMatcher": service}, "redirectFrontend: addressMatcher and serviceMatcher are both set; the API takes exactly one"},
		"the address":  {map[string]any{"addressMatcher": address}, ""},
		"the service":  {map[string]any{"serviceMatcher": service}, ""},
		"a null other": {map[string]any{"addressMatcher": address, "serviceMatcher": nil}, ""},
	} {
		_, err := handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: "cilium-localredirectpolicy", Properties: map[string]any{
			"redirectFrontend": tc.frontend,
			"redirectBackend":  map[string]any{"localEndpointSelector": map[string]any{}, "toPorts": []any{port}},
		}}, "apps")
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tc.want)
		}
	}
}
