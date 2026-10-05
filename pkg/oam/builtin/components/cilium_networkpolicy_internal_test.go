package components

import (
	"maps"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/go-kure/launcher/pkg/oam"
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
// The derivation is ciliumRuleRequiredMatchesCRD's.
//
// The list names a rule's `nodeSelector` as the CRD does. The kind refuses a
// node selector in a namespaced policy whatever it holds (validateCiliumRule),
// after this list is read.
func TestCiliumNetworkPolicy_RequiredMatchCRD(t *testing.T) {
	ciliumRuleRequiredMatchesCRD(t, ciliumNetworkPolicyCRD, ciliumNetworkPolicyRequired)
}
