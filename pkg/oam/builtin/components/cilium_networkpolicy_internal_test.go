package components

import (
	"maps"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/requiredfields"
)

// The cilium-networkpolicy kind is held to the CRD of the CiliumNetworkPolicy
// the linked Cilium module ships, its own and not that of the cluster-wide
// policy: the two declare the same rule today, and each kind fails here on its
// own file if that changes.

const ciliumNetworkPolicyCRD = "ciliumnetworkpolicies.yaml"

// TestCiliumNetworkPolicy_EmitsTheServedVersion: the CRD names the kind the
// handler declares, in the group it declares, namespaced, and the object holds
// `spec` and `specs` beside what launcher and Cilium write.
func TestCiliumNetworkPolicy_EmitsTheServedVersion(t *testing.T) {
	crd, root := ciliumBGPCRD(t, ciliumNetworkPolicyCRD)
	declared, scope := (&CiliumNetworkPolicyHandler{}).ComponentObject()
	if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
		t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
	}
	if crd.Spec.Scope != apiextensionsv1.NamespaceScoped || scope != oam.ObjectScopeNamespaced {
		t.Errorf("the CRD's scope is %s and the handler declares scope %d; want both namespaced", crd.Spec.Scope, scope)
	}
	got := slices.Sorted(maps.Keys(root.Properties))
	if want := []string{"apiVersion", "kind", "metadata", "spec", "specs", "status"}; !slices.Equal(got, want) {
		t.Errorf("the object's fields are %v, want %v: the kind authors spec and specs", got, want)
	}
}

// TestCiliumNetworkPolicy_RequiredMatchCRD holds the kind's required list to
// the fields its CRD requires inside a rule that the Cilium rule type writes
// whether or not they were authored, and to the optional ones the type writes
// with a value the CRD refuses, under `spec` and under an entry of `specs`.
// The derivation is ciliumRuleRequiredFromCRD's.
//
// The list names a rule's `nodeSelector` as the CRD does. The kind refuses a
// node selector in a namespaced policy whatever it holds (validateCiliumRule),
// after this list is read.
func TestCiliumNetworkPolicy_RequiredMatchCRD(t *testing.T) {
	ciliumRuleRequiredMatchesCRD(t, ciliumNetworkPolicyCRD, ciliumNetworkPolicyRequired)
}

// TestCiliumPolicyKinds_TwoSpellingsAreRefused: field names match
// case-insensitively in the decode, so a rule can hold one field in two
// spellings, of which the object carries one. The two policy kinds refuse such
// a rule before the required list is read (refuseUncarriedSpecValues), so the
// list never reads another spelling than the one the object is built from. The
// trait has no such refusal, and its list is held on every spelling instead
// (requiredfields.Refuse).
func TestCiliumPolicyKinds_TwoSpellingsAreRefused(t *testing.T) {
	peers := func(fields map[string]any) []any {
		return []any{map[string]any{"matchExpressions": []any{fields}}}
	}
	complete := map[string]any{"key": "role", "operator": "Exists"}
	incomplete := map[string]any{"key": "role"}
	for kind, handler := range map[string]oam.ComponentHandler{
		"cilium-networkpolicy":            &CiliumNetworkPolicyHandler{},
		"cilium-clusterwidenetworkpolicy": &CiliumClusterwideNetworkPolicyHandler{},
	} {
		for name, entry := range map[string]map[string]any{
			"the kept spelling incomplete":  {"FROMENDPOINTS": peers(complete), "FromEndpoints": peers(incomplete)},
			"the other spelling incomplete": {"FROMENDPOINTS": peers(incomplete), "FromEndpoints": peers(complete)},
			"both complete":                 {"FROMENDPOINTS": peers(complete), "FromEndpoints": peers(complete)},
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				component := &oam.Component{Name: "edge", Type: kind, Properties: map[string]any{
					"spec": map[string]any{"endpointSelector": map[string]any{}, "ingress": []any{entry}},
				}}
				_, err := handler.ToApplicationConfig(component, "production")
				if want := "sets the same field as"; err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one mentioning %q", err, want)
				}
			})
		}
	}
}

// TestCiliumNetworkPolicyTrait_RequiredMatchCRD holds the required list of the
// `cilium-networkpolicy` trait to the same CRD by the same derivation, cut to
// what the trait publishes: the rule it authors is the object's `spec`, and of
// a rule it publishes the fields requiredfields.CiliumTraitFields names. Every
// field the derivation gives under one of them must be in the trait's list,
// and the list holds nothing else. The test is here, beside the kind's,
// because the CRD readers are this package's; the trait's own tests hold its
// published properties to the same field names.
func TestCiliumNetworkPolicyTrait_RequiredMatchCRD(t *testing.T) {
	rule := ciliumPolicyRuleSchemas(t, ciliumNetworkPolicyCRD)["spec"]
	published := requiredfields.CiliumTraitFields()
	for _, field := range published {
		if _, ok := rule.Properties[field]; !ok {
			t.Errorf("the trait publishes %s, which is no field of a rule in the CRD", field)
		}
	}
	var want []string
	for _, path := range ciliumRuleRequiredFromCRD(t, rule) {
		field, _, _ := strings.Cut(path, ".")
		if slices.Contains(published, strings.TrimSuffix(field, "[]")) {
			want = append(want, path)
		}
	}
	if len(want) == 0 {
		t.Fatal("the derivation gives no required field under what the trait publishes; a walk is broken")
	}
	required := requiredfields.CiliumTraitRule()
	if got := slices.Sorted(maps.Keys(required)); !slices.Equal(got, want) {
		t.Errorf("requiredfields.CiliumTraitRule() = %v\nthe CRD and the type give %v", got, want)
	}
	for path, says := range required {
		if strings.TrimSpace(says) == "" {
			t.Errorf("required field %s says nothing of itself", path)
		}
	}
	t.Logf("the trait publishes %v; %d required fields lie under them", published, len(want))
}
