package traits

import (
	"reflect"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-kure/kure/pkg/stack"
)

// policyTypes follows key PRESENCE in the authored document, not rule count
// (go-kure/launcher#467). In networking.k8s.io/v1 a set policyTypes is
// authoritative, so a direction missing from it is not isolated at all.
//
// The four shapes a key can take, per direction:
//
//	key absent        -> not listed, no rules
//	key null          -> not listed, no rules (null is absence)
//	key []            -> LISTED, no rules: deny all for that direction
//	key with rules    -> listed, the rules
//
// The single-key `ingress: []` case hides the defect: with no policyTypes at
// all the API server defaults to Ingress. It surfaces only when the sibling key
// is non-empty, so the mixed rows below are the ones that discriminate.

func npRender(t *testing.T, props map[string]any) *networkingv1.NetworkPolicy {
	t.Helper()
	app := &stack.Application{Name: "web", Namespace: "default"}
	config, err := (&NetworkPolicyHandler{}).parseProperties(props, app)
	if err != nil {
		t.Fatalf("parseProperties: %v", err)
	}
	objs, err := config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate returned %d objects, want 1", len(objs))
	}
	np, ok := (*objs[0]).(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("Generate returned %T, want *NetworkPolicy", *objs[0])
	}
	return np
}

func TestGenerate_PolicyTypesFollowKeyPresence(t *testing.T) {
	ingressRule := []any{map[string]any{"from": []any{map[string]any{"podSelector": map[string]any{}}}}}
	egressRule := []any{map[string]any{"to": []any{map[string]any{"ipBlock": map[string]any{"cidr": "10.0.0.0/8"}}}}}

	ingress := networkingv1.PolicyTypeIngress
	egress := networkingv1.PolicyTypeEgress

	for _, tc := range []struct {
		name        string
		props       map[string]any
		wantTypes   []networkingv1.PolicyType
		wantIngress int
		wantEgress  int
	}{
		{"empty ingress beside egress rules", map[string]any{"ingress": []any{}, "egress": egressRule},
			[]networkingv1.PolicyType{ingress, egress}, 0, 1},
		{"empty egress beside ingress rules", map[string]any{"ingress": ingressRule, "egress": []any{}},
			[]networkingv1.PolicyType{ingress, egress}, 1, 0},
		{"both empty", map[string]any{"ingress": []any{}, "egress": []any{}},
			[]networkingv1.PolicyType{ingress, egress}, 0, 0},
		{"empty ingress alone", map[string]any{"ingress": []any{}},
			[]networkingv1.PolicyType{ingress}, 0, 0},
		{"empty egress alone", map[string]any{"egress": []any{}},
			[]networkingv1.PolicyType{egress}, 0, 0},
		// Controls: the rows that were already right and must stay so.
		{"ingress rules, egress absent", map[string]any{"ingress": ingressRule},
			[]networkingv1.PolicyType{ingress}, 1, 0},
		{"ingress rules, egress null", map[string]any{"ingress": ingressRule, "egress": nil},
			[]networkingv1.PolicyType{ingress}, 1, 0},
		{"ingress rules, egress typed null", map[string]any{"ingress": ingressRule, "egress": []any(nil)},
			[]networkingv1.PolicyType{ingress}, 1, 0},
		{"egress rules, ingress null", map[string]any{"ingress": nil, "egress": egressRule},
			[]networkingv1.PolicyType{egress}, 0, 1},
		{"egress rules, ingress typed null", map[string]any{"ingress": []any(nil), "egress": egressRule},
			[]networkingv1.PolicyType{egress}, 0, 1},
		{"both with rules", map[string]any{"ingress": ingressRule, "egress": egressRule},
			[]networkingv1.PolicyType{ingress, egress}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			np := npRender(t, tc.props)
			if !reflect.DeepEqual(np.Spec.PolicyTypes, tc.wantTypes) {
				t.Errorf("policyTypes = %v, want %v", np.Spec.PolicyTypes, tc.wantTypes)
			}
			if got := len(np.Spec.Ingress); got != tc.wantIngress {
				t.Errorf("ingress rules = %d, want %d", got, tc.wantIngress)
			}
			if got := len(np.Spec.Egress); got != tc.wantEgress {
				t.Errorf("egress rules = %d, want %d", got, tc.wantEgress)
			}
		})
	}
}
