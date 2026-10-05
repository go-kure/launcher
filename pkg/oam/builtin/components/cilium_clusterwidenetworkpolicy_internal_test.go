package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/requiredfields"
)

// These tests hold the cilium-clusterwidenetworkpolicy kind to the CRD the
// linked Cilium module ships, with the readers the BGP kinds brought
// (cilium_bgp_kinds_internal_test.go): what the kind requires, what it checks
// of the schema and what it leaves to the API server are each derived from that
// file, so a dependency bump that changes one fails here.

const ciliumClusterwidePolicyCRD = "ciliumclusterwidenetworkpolicies.yaml"

// ciliumPolicyRuleSchemas returns, from the CRD of a Cilium network policy in
// file, the schema of a rule at each of the two positions that hold one, keyed
// by the path a kind's required list uses.
func ciliumPolicyRuleSchemas(t *testing.T, file string) map[string]apiextensionsv1.JSONSchemaProps {
	t.Helper()
	_, root := ciliumBGPCRD(t, file)
	spec, ok := root.Properties["spec"]
	if !ok {
		t.Fatal("the CRD has no spec property")
	}
	specs, ok := root.Properties["specs"]
	if !ok || specs.Items == nil || specs.Items.Schema == nil {
		t.Fatal("the CRD has no specs property that is a list")
	}
	return map[string]apiextensionsv1.JSONSchemaProps{"spec": spec, "specs[]": *specs.Items.Schema}
}

// TestCiliumClusterwideNetworkPolicy_EmitsTheServedVersion: the CRD names the
// kind the handler declares, in the group it declares, cluster-scoped, and the
// object holds `spec` and `specs` beside what launcher and Cilium write.
func TestCiliumClusterwideNetworkPolicy_EmitsTheServedVersion(t *testing.T) {
	crd, root := ciliumBGPCRD(t, ciliumClusterwidePolicyCRD)
	declared, scope := (&CiliumClusterwideNetworkPolicyHandler{}).ComponentObject()
	if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
		t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
	}
	if crd.Spec.Scope != apiextensionsv1.ClusterScoped || scope != oam.ObjectScopeCluster {
		t.Errorf("the CRD's scope is %s and the handler declares scope %d; want both cluster-scoped", crd.Spec.Scope, scope)
	}
	got := slices.Sorted(maps.Keys(root.Properties))
	if want := []string{"apiVersion", "kind", "metadata", "spec", "specs", "status"}; !slices.Equal(got, want) {
		t.Errorf("the object's fields are %v, want %v: the kind authors spec and specs", got, want)
	}
}

// TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD derives, from the CRD and
// the Cilium rule type, the fields of a rule that the API requires and the
// type would write unauthored, and holds requiredfields.CiliumRule to them, under
// `spec` and under an entry of `specs`.
//
// The list reads what was authored, so a required field under a struct the
// type writes unauthored needs that struct required too: then it is authored
// wherever its fields are looked for. A struct the type leaves out when it is
// not authored (a selector, tagged omitzero) needs nothing.
//
// A field the CRD does not require is listed as well where the type writes it
// unauthored and the CRD refuses the value it then holds: the object could not
// be emitted without it. The fields the type writes unauthored with a value the
// CRD admits are named here, so that a new one is read before it is emitted.
func TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD(t *testing.T) {
	ciliumRuleRequiredMatchesCRD(t, ciliumClusterwidePolicyCRD, ciliumClusterwideNetworkPolicyRequired)
}

// ciliumRuleRequiredMatchesCRD holds one kind to that derivation: the CRD in
// file, and the required list the kind refuses by.
func ciliumRuleRequiredMatchesCRD(t *testing.T, file string, kindRequired map[string]string) {
	t.Helper()
	for at, schema := range ciliumPolicyRuleSchemas(t, file) {
		t.Run(at, func(t *testing.T) {
			var listed []string
			for _, path := range ciliumRuleRequiredFromCRD(t, schema) {
				listed = append(listed, at+"."+path)
			}
			required := requiredfields.CiliumRule(at)
			if got := slices.Sorted(maps.Keys(required)); !slices.Equal(got, listed) {
				t.Errorf("requiredfields.CiliumRule(%q) = %v\nthe CRD and the type give %v", at, got, listed)
			}
			for path, says := range required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
				if _, ok := kindRequired[path]; !ok {
					t.Errorf("the kind's required list lacks %s", path)
				}
			}
		})
	}
	if got, want := len(kindRequired), len(requiredfields.CiliumRule("spec"))+len(requiredfields.CiliumRule("specs[]")); got != want {
		t.Errorf("the kind's required list holds %d fields, want the %d of a rule at its two positions", got, want)
	}
}

// ciliumRuleRequiredFromCRD is the derivation: from the schema of one rule of
// a CRD and from the Cilium rule type, the fields a required list must name,
// by their paths from the rule, sorted. It fails the test where the CRD and
// the type disagree in a way a list cannot express.
func ciliumRuleRequiredFromCRD(t *testing.T, schema apiextensionsv1.JSONSchemaProps) []string {
	t.Helper()
	fields := ciliumBGPTypeFields(reflect.TypeFor[ciliumapi.Rule]())
	// Optional, written unauthored, and the written value refused by the CRD.
	wantRefusedZero := []string{
		"egress[].toPorts[].listener.envoyConfig.kind",
		"egress[].toPorts[].listener.priority",
		"ingress[].toPorts[].listener.envoyConfig.kind",
		"ingress[].toPorts[].listener.priority",
	}
	// Optional, written unauthored, and admitted: a label without a source
	// carries `source: ""`, which Cilium reads as any source.
	wantAdmittedZero := []string{"labels[].source"}
	crdRequired := ciliumPlainRequired(schema)
	if len(crdRequired) == 0 || len(fields) == 0 {
		t.Fatalf("the CRD requires %d fields and the type has %d; a walk is broken", len(crdRequired), len(fields))
	}
	schemas := map[string]apiextensionsv1.JSONSchemaProps{}
	walkCiliumBGPSchema(schema, "", func(path string, s apiextensionsv1.JSONSchemaProps) { schemas[path] = s })

	var refusedZero, admittedZero []string
	for path, f := range fields {
		if !f.writtenUnauthored() || slices.Contains(crdRequired, path) {
			continue
		}
		s, ok := schemas[path]
		if !ok {
			t.Errorf("%s is written unauthored and the CRD has no schema for it", path)
			continue
		}
		refused, known := ciliumZeroRefused(f.field.Type.Kind(), s)
		switch {
		case !known:
			t.Errorf("%s (%s) is written unauthored; this test cannot tell whether the CRD admits its empty value", path, f.field.Type)
		case refused:
			refusedZero = append(refusedZero, path)
		default:
			admittedZero = append(admittedZero, path)
		}
	}
	slices.Sort(refusedZero)
	slices.Sort(admittedZero)
	if !slices.Equal(refusedZero, wantRefusedZero) {
		t.Errorf("optional fields written unauthored with a value the CRD refuses: %v, want %v", refusedZero, wantRefusedZero)
	}
	if !slices.Equal(admittedZero, wantAdmittedZero) {
		t.Errorf("optional fields written unauthored with a value the CRD admits: %v, want %v; say what the object then carries", admittedZero, wantAdmittedZero)
	}

	var listed []string
	for _, path := range slices.Concat(crdRequired, refusedZero) {
		f, ok := fields[path]
		switch {
		case !ok:
			t.Errorf("the CRD requires %s, which is no field of the rule type", path)
		case strings.Contains(path, "{}"):
			t.Errorf("%s is required under a map value, which a required list cannot name", path)
		case !f.writtenUnauthored():
			t.Errorf("the CRD requires %s, which the type leaves out when it is not authored; the kind does not list it, and its documentation must say so", path)
		default:
			listed = append(listed, path)
		}
		segments := strings.Split(path, ".")
		for i := 1; i < len(segments); i++ {
			parent := strings.TrimSuffix(strings.Join(segments[:i], "."), "[]")
			above, ok := fields[parent]
			if !ok {
				t.Errorf("%s: its parent %s is no field of the rule type", path, parent)
				continue
			}
			if above.field.Type.Kind() == reflect.Struct && above.writtenUnauthored() && !slices.Contains(crdRequired, parent) {
				t.Errorf("%s is required under %s, a struct the type writes unauthored that the CRD does not require", path, parent)
			}
		}
	}
	slices.Sort(listed)
	t.Logf("the CRD requires %d fields in a rule, all written unauthored; %d more are written with a value it refuses", len(crdRequired), len(refusedZero))
	return listed
}

// ciliumZeroRefused says whether the schema of a field refuses the value the Go
// type writes when the field is not authored: 0 for a number, the empty string
// for a string. known is false where this function cannot tell: another Go
// kind, or a rule it does not read (a pattern, a format, an expression).
func ciliumZeroRefused(kind reflect.Kind, s apiextensionsv1.JSONSchemaProps) (refused, known bool) {
	if s.Pattern != "" || s.Format != "" || len(s.XValidations) > 0 || len(s.AnyOf)+len(s.OneOf)+len(s.AllOf) > 0 || s.Not != nil {
		return false, false
	}
	switch kind {
	case reflect.String:
		if s.MinLength != nil && *s.MinLength > 0 {
			return true, true
		}
		for _, value := range s.Enum {
			if string(value.Raw) == `""` {
				return false, true
			}
		}
		return len(s.Enum) > 0, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if len(s.Enum) > 0 {
			return false, false
		}
		low := s.Minimum != nil && (*s.Minimum > 0 || *s.Minimum == 0 && s.ExclusiveMinimum)
		high := s.Maximum != nil && (*s.Maximum < 0 || *s.Maximum == 0 && s.ExclusiveMaximum)
		return low || high, true
	default:
		return false, false
	}
}

// ciliumSchemaChoice writes one anyOf or oneOf of a schema as
// "oneOf(a|b)": each branch by the field it requires, or by its type where it
// requires none.
func ciliumSchemaChoice(kind string, branches []apiextensionsv1.JSONSchemaProps) string {
	var names []string
	for _, branch := range branches {
		if len(branch.Required) > 0 {
			names = append(names, strings.Join(branch.Required, "+"))
		} else {
			names = append(names, branch.Type)
		}
	}
	return kind + "(" + strings.Join(names, "|") + ")"
}

// TestCiliumClusterwideNetworkPolicy_SchemaChoices classifies every rule of the
// CRD's schema that is more than a field's own type and value: the object's
// one expression rule and every anyOf and oneOf under a rule. Each is either
// checked by the kind's validate or listed here as left to the API server,
// with the reason, and one that is added, moved or reworded fails.
//
// Checked are the three that sit on the object and on a rule itself, each one
// comparison of authored fields. Left are the choices inside a CIDR entry, a
// DNS entry and a port's rules, nine positions deep in a rule: the
// cilium-networkpolicy kind leaves them too, and the API server refuses them at
// apply. The four others are no choice between fields.
func TestCiliumClusterwideNetworkPolicy_SchemaChoices(t *testing.T) {
	const (
		peer   = "which of three ways a CIDR entry names its addresses; left to the API server"
		name   = "which of a name and a pattern a DNS entry holds; left to the API server"
		layer7 = "which layer 7 protocol a port's rules are for; left to the API server"
		number = "a type union, a number or a name, not a choice between fields"
	)
	checked := map[string]string{
		": anyOf(ingress|ingressDeny|egress|egressDeny)": "requireCiliumRuleEntry",
		": oneOf(endpointSelector|nodeSelector)":         "validateCiliumClusterwideRule",
	}
	left := map[string]string{
		"ingress[].fromCIDRSet[]: oneOf(cidr|cidrGroupRef|cidrGroupSelector)":     peer,
		"ingressDeny[].fromCIDRSet[]: oneOf(cidr|cidrGroupRef|cidrGroupSelector)": peer,
		"egress[].toCIDRSet[]: oneOf(cidr|cidrGroupRef|cidrGroupSelector)":        peer,
		"egressDeny[].toCIDRSet[]: oneOf(cidr|cidrGroupRef|cidrGroupSelector)":    peer,
		"egress[].toFQDNs[]: oneOf(matchName|matchPattern)":                       name,
		"egress[].toPorts[].rules.dns[]: oneOf(matchName|matchPattern)":           name,
		"ingress[].toPorts[].rules.dns[]: oneOf(matchName|matchPattern)":          name,
		"egress[].toPorts[].rules: oneOf(http|dns)":                               layer7,
		"ingress[].toPorts[].rules: oneOf(http|dns)":                              layer7,
		"ingress[].icmps[].fields[].type: anyOf(integer|string)":                  number,
		"ingressDeny[].icmps[].fields[].type: anyOf(integer|string)":              number,
		"egress[].icmps[].fields[].type: anyOf(integer|string)":                   number,
		"egressDeny[].icmps[].fields[].type: anyOf(integer|string)":               number,
	}
	want := slices.Sorted(maps.Keys(checked))
	want = append(want, slices.Sorted(maps.Keys(left))...)
	slices.Sort(want)

	_, root := ciliumBGPCRD(t, ciliumClusterwidePolicyCRD)
	var objectRules []string
	for _, rule := range root.XValidations {
		objectRules = append(objectRules, rule.Rule)
	}
	// Checked by (*CiliumClusterwideNetworkPolicyConfig).validate.
	if want := []string{"has(self.spec) || has(self.specs)"}; !slices.Equal(objectRules, want) {
		t.Errorf("the object's expression rules are %v, want %v", objectRules, want)
	}

	for at, schema := range ciliumPolicyRuleSchemas(t, ciliumClusterwidePolicyCRD) {
		t.Run(at, func(t *testing.T) {
			var found []string
			walkCiliumBGPSchema(schema, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				for _, rule := range s.XValidations {
					t.Errorf("%s declares the expression rule %q; classify it", path, rule.Rule)
				}
				if len(s.AllOf) > 0 || s.Not != nil {
					t.Errorf("%s declares an allOf or a not; classify it", path)
				}
				if len(s.AnyOf) > 0 {
					found = append(found, path+": "+ciliumSchemaChoice("anyOf", s.AnyOf))
				}
				if len(s.OneOf) > 0 {
					found = append(found, path+": "+ciliumSchemaChoice("oneOf", s.OneOf))
				}
			})
			slices.Sort(found)
			if !slices.Equal(found, want) {
				t.Errorf("the rule's schema choices are\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
			}
		})
	}
	for choice, why := range left {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is left without a reason", choice)
		}
	}
}

// TestCiliumClusterwideNetworkPolicy_OneDefault: the one default the CRD
// declares inside a rule is an ICMP field's `family`, IPv4, on a string the
// type leaves out when empty. An authored empty family is therefore left out
// and filled as IPv4, which is also how Cilium reads an empty one; every other
// field is written as authored or not at all, and no default is filled here.
func TestCiliumClusterwideNetworkPolicy_OneDefault(t *testing.T) {
	want := []string{
		"egress[].icmps[].fields[].family = \"IPv4\"",
		"egressDeny[].icmps[].fields[].family = \"IPv4\"",
		"ingress[].icmps[].fields[].family = \"IPv4\"",
		"ingressDeny[].icmps[].fields[].family = \"IPv4\"",
	}
	for at, schema := range ciliumPolicyRuleSchemas(t, ciliumClusterwidePolicyCRD) {
		t.Run(at, func(t *testing.T) {
			var found []string
			walkCiliumBGPSchema(schema, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				if s.Default != nil {
					found = append(found, path+" = "+string(s.Default.Raw))
				}
			})
			slices.Sort(found)
			slices.Sort(want)
			if !slices.Equal(found, want) {
				t.Errorf("the rule's defaults are %v, want %v", found, want)
			}
		})
	}
	family, ok := reflect.TypeFor[ciliumapi.ICMPField]().FieldByName("Family")
	if !ok || family.Type.Kind() != reflect.String || family.Tag.Get("json") != "family,omitempty" {
		t.Errorf("ICMPField.Family is %v (found %v); the default above was read against a string left out when empty", family, ok)
	}
	if ciliumapi.IPv4Family != "IPv4" {
		t.Errorf("Cilium's IPv4 family is %q, the CRD's default is IPv4", ciliumapi.IPv4Family)
	}
}
