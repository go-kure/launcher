package components_test

import (
	"fmt"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

// TestRBACKinds_AuthoredValuesArriveTyped: the role, rolebinding, clusterrole
// and clusterrolebinding kinds write what was authored into the typed object,
// and what they write when a field is not authored is what their README
// entries say. The rows of policyFreeKinds hold the rest of each kind.
func TestRBACKinds_AuthoredValuesArriveTyped(t *testing.T) {
	kinds := map[string]policyFreeKind{}
	for _, kind := range policyFreeKinds {
		kinds[kind.component] = kind
	}
	build := func(component string, props map[string]any) any {
		return kinds[component].generate(t, "fast", props)
	}
	accepted := func(component, what string, props map[string]any) {
		t.Helper()
		if err := coreKindErr(kinds[component].handler, component, "fast", props); err != nil {
			t.Errorf("%s: %s: %v, want it accepted", component, what, err)
		}
	}

	role := build("role", kinds["role"].full).(*rbacv1.Role)
	if r := role.Rules[0]; !slices.Equal(r.APIGroups, []string{""}) || !slices.Equal(r.Resources, []string{"pods"}) || !slices.Equal(r.Verbs, []string{"get", "list"}) {
		t.Errorf("rules[0] = %+v, want the authored core group, pods, and get and list in order", r)
	}
	if r := role.Rules[1]; !slices.Equal(r.ResourceNames, []string{"web"}) || !slices.Equal(r.Verbs, []string{"*"}) {
		t.Errorf("rules[1] = %+v, want the authored resource name and the * verb", r)
	}
	// The API requires no rule. The type always encodes the list, so a role that
	// authors none carries rules: null, and an authored empty list is written as
	// one.
	for _, component := range []string{"role", "clusterrole"} {
		bare := policyFreeJSON(t, build(component, nil))
		if got, ok := bare["rules"]; !ok || got != nil {
			t.Errorf("%s: rules = %v (present: %v), want null on a role that authors none", component, got, ok)
		}
		emptied := policyFreeJSON(t, build(component, map[string]any{"rules": []any{}}))
		if got, want := fmt.Sprint(emptied["rules"]), "[]"; got != want {
			t.Errorf("%s: rules = %s, want %s: the authored empty list", component, got, want)
		}
	}

	cluster := build("clusterrole", kinds["clusterrole"].full).(*rbacv1.ClusterRole)
	if r := cluster.Rules[1]; !slices.Equal(r.NonResourceURLs, []string{"/healthz", "/metrics"}) || len(r.APIGroups)+len(r.Resources) != 0 {
		t.Errorf("rules[1] = %+v, want the two authored non-resource URLs and no resource", r)
	}
	selectors := cluster.AggregationRule.ClusterRoleSelectors
	if len(selectors) != 1 || selectors[0].MatchLabels["rbac.example.com/aggregate-to-monitoring"] != "true" ||
		len(selectors[0].MatchExpressions) != 1 || !slices.Equal(selectors[0].MatchExpressions[0].Values, []string{"a", "b"}) {
		t.Errorf("clusterRoleSelectors = %+v, want the authored labels and expression", selectors)
	}
	// An aggregated ClusterRole authors its aggregation rule and no rule of its
	// own: it builds, with rules: null, or with the empty list where one was
	// authored.
	aggregation := map[string]any{"clusterRoleSelectors": []any{map[string]any{"matchLabels": map[string]any{"aggregate": "true"}}}}
	aggregated := policyFreeJSON(t, build("clusterrole", map[string]any{"aggregationRule": aggregation}))
	if got, ok := aggregated["rules"]; !ok || got != nil {
		t.Errorf("rules = %v (present: %v), want null on an aggregated ClusterRole that authors none", got, ok)
	}
	if got, want := fmt.Sprint(aggregated["aggregationRule"]), "map[clusterRoleSelectors:[map[matchLabels:map[aggregate:true]]]]"; got != want {
		t.Errorf("aggregationRule = %s, want %s", got, want)
	}
	aggregated = policyFreeJSON(t, build("clusterrole", map[string]any{"aggregationRule": aggregation, "rules": []any{}}))
	if got, want := fmt.Sprint(aggregated["rules"]), "[]"; got != want {
		t.Errorf("rules = %s, want %s: the authored empty list of an aggregated ClusterRole", got, want)
	}
	// An empty selector is one, and selects every ClusterRole: the API
	// server's validation requires a selector, not a term in it.
	accepted("clusterrole", "an aggregation rule with an empty selector", map[string]any{"aggregationRule": map[string]any{"clusterRoleSelectors": []any{map[string]any{}}}})
	// A rule of non-resource URLs needs no API group and no resource.
	accepted("clusterrole", "a rule of non-resource URLs alone", map[string]any{"rules": []any{map[string]any{"nonResourceURLs": []any{"/healthz"}, "verbs": []any{"get"}}}})
	// The required list holds presence only: an authored empty list of verbs
	// builds, and the API server refuses it.
	for _, component := range []string{"role", "clusterrole"} {
		accepted(component, "an authored empty list of verbs", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{}}}})
		// Whether a verb, a resource or an API group exists is checked nowhere.
		accepted(component, "a verb and a resource the API does not know", map[string]any{"rules": []any{policyRule("no.such.group", "nothings", "frobnicate")}})
	}

	for _, component := range []string{"rolebinding", "clusterrolebinding"} {
		obj := build(component, kinds[component].full)
		var subjects []rbacv1.Subject
		var ref rbacv1.RoleRef
		switch binding := obj.(type) {
		case *rbacv1.RoleBinding:
			subjects, ref = binding.Subjects, binding.RoleRef
		case *rbacv1.ClusterRoleBinding:
			subjects, ref = binding.Subjects, binding.RoleRef
		default:
			t.Fatalf("%s built a %T", component, obj)
		}
		if want := (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "view"}); ref != want {
			t.Errorf("%s: roleRef = %+v, want the authored %+v", component, ref, want)
		}
		if len(subjects) != 3 || subjects[0] != (rbacv1.Subject{Kind: "ServiceAccount", Name: "web", Namespace: "apps"}) ||
			subjects[1] != (rbacv1.Subject{Kind: "User", Name: "jane", APIGroup: rbacv1.GroupName}) || subjects[2].Name != "ops" {
			t.Errorf("%s: subjects = %+v, want the three authored subjects in order", component, subjects)
		}
		// The API server fills an unauthored roleRef.apiGroup with the RBAC group
		// before it validates, so a binding without one builds. The type always
		// encodes the field: the object carries it empty.
		minimal := policyFreeJSON(t, build(component, kinds[component].minimal))
		if got, ok := minimal["roleRef"].(map[string]any)["apiGroup"]; !ok || got != "" {
			t.Errorf("%s: roleRef.apiGroup = %v (present: %v), want it written empty when not authored", component, got, ok)
		}
		// The API requires no subject, and the type omits an unauthored list.
		if _, ok := minimal["subjects"]; ok {
			t.Errorf("%s: subjects = %v, want the key left out of a binding that authors none", component, minimal["subjects"])
		}
		// The form of a value is the API server's to judge: a role of another API
		// group, and a subject of a kind RBAC does not know.
		kind := minimal["roleRef"].(map[string]any)["kind"]
		accepted(component, "a roleRef of another API group", map[string]any{"roleRef": map[string]any{"apiGroup": "example.com", "kind": kind, "name": "view"}})
		accepted(component, "a subject of an unknown kind", bindingTo("ClusterRole", map[string]any{"kind": "Robot", "name": "r2"}))
		// A User or a Group has no namespace to name.
		accepted(component, "a User and a Group without namespace", bindingTo("ClusterRole", map[string]any{"kind": "User", "name": "jane"}, map[string]any{"kind": "Group", "name": "ops"}))
	}
	// A RoleBinding's ServiceAccount subject without namespace is one of the
	// binding's own namespace to the API server; a ClusterRoleBinding has none,
	// and its kind refuses the subject (TestPolicyFreeKinds_Refusals).
	accepted("rolebinding", "a ServiceAccount subject without namespace", bindingTo("Role", map[string]any{"kind": "ServiceAccount", "name": "web"}))
}
