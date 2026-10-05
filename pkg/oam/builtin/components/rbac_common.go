package components

import (
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of the rbac.authorization.k8s.io/v1
// API share (go-kure/launcher#790): role, rolebinding, clusterrole and
// clusterrolebinding. None has a spec type, so each is a policyFreeKind over
// the whole object.
//
// The four kinds are ungated: no capability and no environment-policy check
// restricts what a role grants or to whom a binding grants it. An oam.Policy
// has no method that speaks to a rule, a subject or a role reference.
//
// Each kind's required list is derived from the markers of the linked
// k8s.io/api source (TestBuiltinMarkerKinds_RequiredMatchMarkers). The API
// server requires more than the markers say, in validation that is in no
// linked module. The presence rules of that validation are checked here by
// hand, each under a comment that names where it was read:
// pkg/apis/rbac/validation/validation.go of k8s.io/kubernetes, at Kubernetes
// v1.37.1. They are rules of presence only, with one exception: a match
// expression of a clusterrole's aggregation selector is held to what the API
// server refuses of an expression on every object (validateLabelSelector).
// The form of any other value that validation checks (a subject's kind, a role
// reference's kind or group, a name) is left to the API server, as is the rule
// that a binding's roleRef does not change once the binding exists. Whether a
// verb, a resource or an API group exists is checked by neither: that
// validation counts a rule's verbs, API groups and resources and does not read
// them.

// rbacRuleRequired is the required list of a role's or a clusterrole's rules:
// the one field of a rule the API marks required and the Go type writes
// whether or not it was authored.
var rbacRuleRequired = map[string]string{
	"rules[].verbs": "the verbs the rule grants, at least one",
}

// rbacBindingRequired is the required list of a rolebinding or a
// clusterrolebinding. A roleRef's apiGroup is not on it: the type writes an
// unauthored one as the empty string, and the API server's defaulting fills
// the RBAC group before it validates (SetDefaults_RoleBinding and
// SetDefaults_ClusterRoleBinding, pkg/apis/rbac/v1/defaults.go, Kubernetes
// v1.37.1), so a binding without it passes that validation.
var rbacBindingRequired = map[string]string{
	"roleRef":         "the role the binding grants: its kind and its name",
	"roleRef.kind":    "the kind of the role the binding grants",
	"roleRef.name":    "the name of the role the binding grants",
	"subjects[].kind": "the kind of the subject: User, Group or ServiceAccount",
	"subjects[].name": "the name of the subject",
}

// validatePolicyRules refuses a rule that lacks what the API server requires
// of it beyond the type's markers, by the rule's path. namespaced says the
// rules are a Role's.
//
// Read from ValidatePolicyRule, pkg/apis/rbac/validation/validation.go,
// Kubernetes v1.37.1: a rule that holds nonResourceURLs is refused in a
// namespaced role and together with apiGroups, resources or resourceNames;
// any other rule needs at least one of apiGroups and at least one of
// resources. That function also requires verbs, which the required list
// holds.
func validatePolicyRules(rules []rbacv1.PolicyRule, namespaced bool) error {
	for i, rule := range rules {
		if len(rule.NonResourceURLs) > 0 {
			switch {
			case namespaced:
				return errors.Errorf("rules[%d].nonResourceURLs: not allowed in a Role: a non-resource URL is not namespaced; grant it in a clusterrole", i)
			case len(rule.APIGroups) > 0 || len(rule.Resources) > 0 || len(rule.ResourceNames) > 0:
				return errors.Errorf("rules[%d].nonResourceURLs: not allowed beside apiGroups, resources or resourceNames: a rule grants resources or non-resource URLs, not both", i)
			}
			continue
		}
		if len(rule.APIGroups) == 0 {
			return errors.Errorf(`rules[%d].apiGroups: required (the API groups of the resources the rule grants, at least one; "" is the core group)`, i)
		}
		if len(rule.Resources) == 0 {
			return errors.Errorf("rules[%d].resources: required (the resources the rule grants, at least one)", i)
		}
	}
	return nil
}

// rbacRulesSchema is the `rules` property of a role or a clusterrole, of the
// given kind.
func rbacRulesSchema(kind string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: kind + " rules: the rules the role grants. Nothing restricts what a rule grants. Unauthored, the object carries rules: null, a role that grants nothing.",
		Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "One rule: verbs (required), apiGroups, resources, resourceNames, nonResourceURLs. A rule grants resources (apiGroups and resources, each required) or non-resource URLs. Decoded strictly into the Kubernetes API type: see PolicyRule in the Kubernetes API reference.",
		},
	}
}

// rbacBindingSchema is the properties of a rolebinding or a
// clusterrolebinding, of the given kind. roles names what its roleRef may
// refer to.
func rbacBindingSchema(kind, roles string) map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"subjects": {
			Type:        oam.PropertyTypeArray,
			Description: kind + " subjects: who the role is granted to. Nothing restricts the subjects. Unauthored, the binding grants the role to no one.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One subject: kind (required: User, Group or ServiceAccount), name (required), apiGroup, namespace. The name is written as authored and follows no component's objectName. Decoded strictly into the Kubernetes API type: see Subject in the Kubernetes API reference.",
			},
		},
		"roleRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + kind + " roleRef: the role the binding grants, " + roles + ": kind (required), name (required), apiGroup. The name is written as authored and follows no component's objectName. An unauthored apiGroup is written as the empty string, and the API server fills rbac.authorization.k8s.io. Immutable once created.",
		},
	}
}
