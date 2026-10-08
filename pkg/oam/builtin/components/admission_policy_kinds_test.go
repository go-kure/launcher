package components_test

import (
	"maps"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The validatingadmissionpolicy, validatingadmissionpolicybinding,
// mutatingadmissionpolicy and mutatingadmissionpolicybinding kinds
// (go-kure/launcher#943) are rows of policyFreeKinds, which holds what the
// shared helper promises of each. This file holds their fixtures and what is
// theirs alone.

// admissionPolicyRule is a whole rule of a policy's match: the least the API
// takes, with the given fields set over it, and a field given nil left out.
func admissionPolicyRule(fields map[string]any) map[string]any {
	rule := map[string]any{"operations": []any{"CREATE", "UPDATE"}, "apiGroups": []any{"apps"}, "apiVersions": []any{"v1"}, "resources": []any{"deployments"}}
	for k, v := range fields {
		if v == nil {
			delete(rule, k)
			continue
		}
		rule[k] = v
	}
	return rule
}

// admissionPolicyMatch is a policy's matchConstraints of one whole rule.
func admissionPolicyMatch() map[string]any {
	return map[string]any{"resourceRules": []any{admissionPolicyRule(nil)}}
}

// admissionPolicyMatchFull is a match that sets every field of one.
func admissionPolicyMatchFull() map[string]any {
	return map[string]any{
		"namespaceSelector": map[string]any{"matchLabels": map[string]any{"policy.example.com/enforce": "true"}},
		"objectSelector": map[string]any{"matchExpressions": []any{
			map[string]any{"key": "tier", "operator": "In", "values": []any{"web", "api"}},
		}},
		"resourceRules":        []any{admissionPolicyRule(map[string]any{"resourceNames": []any{"web"}, "scope": "Namespaced"})},
		"excludeResourceRules": []any{admissionPolicyRule(map[string]any{"resources": []any{"deployments/scale"}})},
		"matchPolicy":          "Equivalent",
	}
}

// validatingAdmissionPolicy is the least a validatingadmissionpolicy takes,
// with the given fields set over it.
func validatingAdmissionPolicy(fields map[string]any) map[string]any {
	props := map[string]any{
		"matchConstraints": admissionPolicyMatch(),
		"validations":      []any{map[string]any{"expression": "object.spec.replicas <= 5"}},
	}
	maps.Copy(props, fields)
	return props
}

// validatingAdmissionPolicyFull sets every field of a ValidatingAdmissionPolicySpec.
func validatingAdmissionPolicyFull() map[string]any {
	return map[string]any{
		"paramKind":        map[string]any{"apiVersion": "policy.example.com/v1", "kind": "ReplicaLimit"},
		"matchConstraints": admissionPolicyMatchFull(),
		"validations": []any{map[string]any{
			"expression": "object.spec.replicas <= params.maxReplicas", "message": "too many replicas",
			"reason": "Invalid", "messageExpression": "'at most ' + string(params.maxReplicas)",
		}},
		"failurePolicy":    "Fail",
		"auditAnnotations": []any{map[string]any{"key": "replicas", "valueExpression": "string(object.spec.replicas)"}},
		"matchConditions":  []any{map[string]any{"name": "not-system", "expression": "!request.userInfo.username.startsWith('system:')"}},
		"variables":        []any{map[string]any{"name": "replicas", "expression": "object.spec.replicas"}},
	}
}

// admissionPolicyBinding is the least a binding of either kind takes, with
// the given fields set over it; the validating one adds its actions.
func admissionPolicyBinding(validating bool, fields map[string]any) map[string]any {
	props := map[string]any{"policyName": "replica-limit"}
	if validating {
		props["validationActions"] = []any{"Deny"}
	}
	maps.Copy(props, fields)
	return props
}

// admissionPolicyBindingFull sets every field of a binding of either kind:
// the validating one's parameter is named, the mutating one's selected.
func admissionPolicyBindingFull(validating bool) map[string]any {
	props := map[string]any{
		"policyName":     "replica-limit",
		"matchResources": admissionPolicyMatchFull(),
	}
	if validating {
		props["paramRef"] = map[string]any{"name": "limits", "namespace": "policy", "parameterNotFoundAction": "Deny"}
		props["validationActions"] = []any{"Deny", "Audit"}
	} else {
		props["paramRef"] = map[string]any{
			"selector":                map[string]any{"matchExpressions": []any{map[string]any{"key": "team", "operator": "Exists"}}},
			"parameterNotFoundAction": "Allow",
		}
	}
	return props
}

// mutatingAdmissionPolicy is the least a mutatingadmissionpolicy takes, with
// the given fields set over it.
func mutatingAdmissionPolicy(fields map[string]any) map[string]any {
	props := map[string]any{
		"matchConstraints": admissionPolicyMatch(),
		"mutations": []any{map[string]any{
			"patchType":          "ApplyConfiguration",
			"applyConfiguration": map[string]any{"expression": "Object{metadata: Object.metadata{labels: {'policy.example.com/seen': 'true'}}}"},
		}},
		"reinvocationPolicy": "Never",
	}
	maps.Copy(props, fields)
	return props
}

// mutatingAdmissionPolicyFull sets every field of a MutatingAdmissionPolicySpec,
// a mutation of each patch type among them.
func mutatingAdmissionPolicyFull() map[string]any {
	props := mutatingAdmissionPolicy(map[string]any{
		"paramKind":        map[string]any{"apiVersion": "v1", "kind": "ConfigMap"},
		"matchConstraints": admissionPolicyMatchFull(),
		"variables":        []any{map[string]any{"name": "team", "expression": "params.data.team"}},
		"failurePolicy":    "Ignore",
		"matchConditions":  []any{map[string]any{"name": "not-system", "expression": "!request.userInfo.username.startsWith('system:')"}},
		// IfNeeded and not the default of none: the API server has none.
		"reinvocationPolicy": "IfNeeded",
	})
	props["mutations"] = append(props["mutations"].([]any), map[string]any{
		"patchType": "JSONPatch",
		"jsonPatch": map[string]any{"expression": "[JSONPatch{op: 'add', path: '/metadata/annotations', value: {'team': variables.team}}]"},
	})
	return props
}

// admissionPolicyKinds returns the four kinds' rows of policyFreeKinds.
func admissionPolicyKinds(t *testing.T) map[string]policyFreeKind {
	t.Helper()
	out := map[string]policyFreeKind{}
	for _, kind := range policyFreeKinds {
		if strings.Contains(kind.component, "admissionpolicy") {
			out[kind.component] = kind
		}
	}
	if len(out) != 4 {
		t.Fatalf("policyFreeKinds holds %d admission policy kinds, want 4", len(out))
	}
	return out
}

// TestAdmissionPolicyKinds_AuthoredValuesArriveTyped: each kind's spec arrives
// in the typed object as authored, and what the API server defaults stays
// unset.
func TestAdmissionPolicyKinds_AuthoredValuesArriveTyped(t *testing.T) {
	kinds := admissionPolicyKinds(t)

	vap := kinds["validatingadmissionpolicy"].generate(t, "replica-limit", validatingAdmissionPolicyFull()).(*admissionregistrationv1.ValidatingAdmissionPolicy)
	if pk := vap.Spec.ParamKind; pk == nil || pk.APIVersion != "policy.example.com/v1" || pk.Kind != "ReplicaLimit" {
		t.Errorf("paramKind = %+v, want policy.example.com/v1 ReplicaLimit", pk)
	}
	if mc := vap.Spec.MatchConstraints; mc == nil || len(mc.ResourceRules) != 1 || mc.ResourceRules[0].Resources[0] != "deployments" ||
		len(mc.ExcludeResourceRules) != 1 || mc.MatchPolicy == nil || *mc.MatchPolicy != admissionregistrationv1.Equivalent {
		t.Errorf("matchConstraints = %+v, want the authored rules and matchPolicy Equivalent", mc)
	}
	if v := vap.Spec.Validations; len(v) != 1 || v[0].Reason == nil || *v[0].Reason != "Invalid" || v[0].Message != "too many replicas" {
		t.Errorf("validations = %+v, want the authored one", v)
	}
	if fp := vap.Spec.FailurePolicy; fp == nil || *fp != admissionregistrationv1.Fail {
		t.Errorf("failurePolicy = %v, want Fail", fp)
	}

	vapb := kinds["validatingadmissionpolicybinding"].generate(t, "replica-limit", admissionPolicyBindingFull(true)).(*admissionregistrationv1.ValidatingAdmissionPolicyBinding)
	if pr := vapb.Spec.ParamRef; pr == nil || pr.Name != "limits" || pr.Namespace != "policy" || pr.ParameterNotFoundAction == nil || *pr.ParameterNotFoundAction != admissionregistrationv1.DenyAction {
		t.Errorf("paramRef = %+v, want policy/limits, Deny", pr)
	}
	if va := vapb.Spec.ValidationActions; len(va) != 2 || va[0] != admissionregistrationv1.Deny || va[1] != admissionregistrationv1.Audit {
		t.Errorf("validationActions = %v, want [Deny Audit]", va)
	}

	mapol := kinds["mutatingadmissionpolicy"].generate(t, "labels", mutatingAdmissionPolicyFull()).(*admissionregistrationv1.MutatingAdmissionPolicy)
	if m := mapol.Spec.Mutations; len(m) != 2 || m[0].ApplyConfiguration == nil || m[1].PatchType != admissionregistrationv1.PatchTypeJSONPatch || m[1].JSONPatch == nil {
		t.Errorf("mutations = %+v, want an apply configuration, then a JSON patch", m)
	}
	if rp := mapol.Spec.ReinvocationPolicy; rp != admissionregistrationv1.IfNeededReinvocationPolicy {
		t.Errorf("reinvocationPolicy = %q, want IfNeeded", rp)
	}

	mapb := kinds["mutatingadmissionpolicybinding"].generate(t, "labels", admissionPolicyBindingFull(false)).(*admissionregistrationv1.MutatingAdmissionPolicyBinding)
	if pr := mapb.Spec.ParamRef; pr == nil || pr.Selector == nil || len(pr.Selector.MatchExpressions) != 1 || pr.Name != "" {
		t.Errorf("paramRef = %+v, want the selector alone", pr)
	}

	// What the API server defaults is left to it.
	plain := kinds["validatingadmissionpolicy"].generate(t, "replica-limit", validatingAdmissionPolicy(nil)).(*admissionregistrationv1.ValidatingAdmissionPolicy)
	if mc := plain.Spec.MatchConstraints; plain.Spec.FailurePolicy != nil || mc.MatchPolicy != nil || mc.NamespaceSelector != nil || mc.ObjectSelector != nil {
		t.Errorf("unauthored defaults = %+v, want them unset for the API server", plain.Spec)
	}
	binding := kinds["mutatingadmissionpolicybinding"].generate(t, "labels", admissionPolicyBinding(false, nil)).(*admissionregistrationv1.MutatingAdmissionPolicyBinding)
	if binding.Spec.ParamRef != nil || binding.Spec.MatchResources != nil {
		t.Errorf("an unauthored binding's paramRef and matchResources = %+v, want unset", binding.Spec)
	}
}

// TestAdmissionPolicyKinds_ValuesLeftToTheAPIServer: the form of a value the
// kinds do not read builds, as their README entries say; the API server
// judges it.
func TestAdmissionPolicyKinds_ValuesLeftToTheAPIServer(t *testing.T) {
	kinds := admissionPolicyKinds(t)
	rule := func(fields map[string]any) map[string]any {
		return map[string]any{"resourceRules": []any{admissionPolicyRule(fields)}}
	}
	broken := "object.("
	// Values the two policy kinds share.
	policyCases := func(policy func(map[string]any) map[string]any) map[string]map[string]any {
		return map[string]map[string]any{
			"an unknown failure policy":                 policy(map[string]any{"failurePolicy": "Retry"}),
			"an unknown match policy":                   policy(map[string]any{"matchConstraints": map[string]any{"resourceRules": []any{admissionPolicyRule(nil)}, "matchPolicy": "Loose"}}),
			"an unknown rule scope":                     policy(map[string]any{"matchConstraints": rule(map[string]any{"scope": "Everywhere"})}),
			"a wildcard beside a resource":              policy(map[string]any{"matchConstraints": rule(map[string]any{"resources": []any{"*", "deployments"}})}),
			"a parameter kind of no form":               policy(map[string]any{"paramKind": map[string]any{"apiVersion": "a/b/c", "kind": "Not A Kind"}}),
			"a variable name no CEL identifier":         policy(map[string]any{"variables": []any{map[string]any{"name": "not-an-identifier", "expression": "1"}}}),
			"a match condition that does not compile":   policy(map[string]any{"matchConditions": []any{map[string]any{"name": "broken", "expression": broken}}}),
			"two match conditions of one name":          policy(map[string]any{"matchConditions": []any{map[string]any{"name": "a", "expression": "true"}, map[string]any{"name": "a", "expression": "true"}}}),
			"an authored empty match condition name":    policy(map[string]any{"matchConditions": []any{map[string]any{"name": "", "expression": "true"}}}),
			"a variable expression that does not build": policy(map[string]any{"variables": []any{map[string]any{"name": "v", "expression": broken}}}),
		}
	}
	vap := policyCases(validatingAdmissionPolicy)
	maps.Copy(vap, map[string]map[string]any{
		"a validation that does not compile":  validatingAdmissionPolicy(map[string]any{"validations": []any{map[string]any{"expression": broken}}}),
		"an unknown validation reason":        validatingAdmissionPolicy(map[string]any{"validations": []any{map[string]any{"expression": "true", "reason": "Teapot"}}}),
		"a message with a line break":         validatingAdmissionPolicy(map[string]any{"validations": []any{map[string]any{"expression": "true", "message": "a\nb"}}}),
		"two audit annotations of one key":    validatingAdmissionPolicy(map[string]any{"auditAnnotations": []any{map[string]any{"key": "a", "valueExpression": "'x'"}, map[string]any{"key": "a", "valueExpression": "'y'"}}}),
		"an operation the policy cannot take": validatingAdmissionPolicy(map[string]any{"matchConstraints": rule(map[string]any{"operations": []any{"PATCH"}})}),
		"audit annotations and no validation": func() map[string]any {
			props := validatingAdmissionPolicy(map[string]any{"auditAnnotations": []any{map[string]any{"key": "seen", "valueExpression": "'yes'"}}})
			delete(props, "validations")
			return props
		}(),
		"an audit value that does not compile":    validatingAdmissionPolicy(map[string]any{"auditAnnotations": []any{map[string]any{"key": "a", "valueExpression": broken}}}),
		"an authored empty validation expression": validatingAdmissionPolicy(map[string]any{"validations": []any{map[string]any{"expression": ""}}}),
	})
	mapol := policyCases(mutatingAdmissionPolicy)
	maps.Copy(mapol, map[string]map[string]any{
		"an unknown reinvocation policy":    mutatingAdmissionPolicy(map[string]any{"reinvocationPolicy": "Sometimes"}),
		"a DELETE the policy cannot mutate": mutatingAdmissionPolicy(map[string]any{"matchConstraints": rule(map[string]any{"operations": []any{"DELETE"}})}),
		"an unknown patch type":             mutatingAdmissionPolicy(map[string]any{"mutations": []any{map[string]any{"patchType": "StrategicMerge"}}}),
		"an authored empty patch type":      mutatingAdmissionPolicy(map[string]any{"mutations": []any{map[string]any{"patchType": ""}}}),
		"the other patch beside its own": mutatingAdmissionPolicy(map[string]any{"mutations": []any{map[string]any{
			"patchType":          "JSONPatch",
			"jsonPatch":          map[string]any{"expression": "[]"},
			"applyConfiguration": map[string]any{"expression": "Object{}"},
		}}}),
		"a patch that does not compile": mutatingAdmissionPolicy(map[string]any{"mutations": []any{map[string]any{
			"patchType": "JSONPatch", "jsonPatch": map[string]any{"expression": broken},
		}}}),
	})
	// Values the two binding kinds share.
	bindingCases := func(validating bool) map[string]map[string]any {
		binding := func(fields map[string]any) map[string]any { return admissionPolicyBinding(validating, fields) }
		return map[string]map[string]any{
			"a policy name of no form":                binding(map[string]any{"policyName": "Not A Name"}),
			"an unknown parameter-not-found action":   binding(map[string]any{"paramRef": map[string]any{"name": "limits", "parameterNotFoundAction": "Ignore"}}),
			"a parameter name of no form":             binding(map[string]any{"paramRef": map[string]any{"name": "Not A Name", "parameterNotFoundAction": "Deny"}}),
			"a match with no rule":                    binding(map[string]any{"matchResources": map[string]any{"matchPolicy": "Exact"}}),
			"an unknown match policy in the binding":  binding(map[string]any{"matchResources": map[string]any{"matchPolicy": "Loose"}}),
			"a resource name that is no path segment": binding(map[string]any{"matchResources": rule(map[string]any{"resourceNames": []any{"a/b"}})}),
		}
	}
	vapb := bindingCases(true)
	maps.Copy(vapb, map[string]map[string]any{
		"an unknown validation action": admissionPolicyBinding(true, map[string]any{"validationActions": []any{"Block"}}),
		"Deny beside Warn":             admissionPolicyBinding(true, map[string]any{"validationActions": []any{"Deny", "Warn"}}),
		"one action twice":             admissionPolicyBinding(true, map[string]any{"validationActions": []any{"Audit", "Audit"}}),
	})
	mapb := bindingCases(false)
	maps.Copy(mapb, map[string]map[string]any{
		"a DELETE the binding's policy cannot mutate": admissionPolicyBinding(false, map[string]any{"matchResources": rule(map[string]any{"operations": []any{"DELETE"}})}),
	})

	for component, cases := range map[string]map[string]map[string]any{
		"validatingadmissionpolicy": vap, "validatingadmissionpolicybinding": vapb,
		"mutatingadmissionpolicy": mapol, "mutatingadmissionpolicybinding": mapb,
	} {
		kind := kinds[component]
		for what, props := range cases {
			if err := coreKindErr(kind.handler, kind.component, "fast", props); err != nil {
				t.Errorf("%s: %s: %v, want it built", component, what, err)
			}
		}
	}
}

// TestAdmissionPolicyKinds_ObjectKindPolicy: the object kind rules gate the
// four kinds as every emitted object; nothing else of the environment policy
// does.
func TestAdmissionPolicyKinds_ObjectKindPolicy(t *testing.T) {
	for component, kind := range admissionPolicyKinds(t) {
		t.Run(component, func(t *testing.T) {
			clusterWideObjectKindPolicy(t, kind.component, kind.handler, kind.minimal, kind.gvk.GroupKind(), kind.gvk.Kind+` "web"`)
		})
	}
}

// TestAdmissionPolicyKinds_StateTheirServedVersion: each kind's schema states
// the Kubernetes version from which the API server serves its object, the
// mutating pair's 1.36 and the validating pair's 1.30, in the description of
// the property every document of the kind authors.
func TestAdmissionPolicyKinds_StateTheirServedVersion(t *testing.T) {
	for component, want := range map[string]struct{ property, version string }{
		"validatingadmissionpolicy":        {"matchConstraints", "1.30"},
		"validatingadmissionpolicybinding": {"policyName", "1.30"},
		"mutatingadmissionpolicy":          {"matchConstraints", "1.36"},
		"mutatingadmissionpolicybinding":   {"policyName", "1.36"},
	} {
		kind := admissionPolicyKinds(t)[component]
		says := kind.handler.(oam.PropertySchemaProvider).PropertySchema()[want.property].Description
		if !strings.Contains(says, "admissionregistration.k8s.io/v1 from Kubernetes "+want.version+";") {
			t.Errorf("%s: %s says %q, want the version %s it is served from", component, want.property, says, want.version)
		}
	}
}
