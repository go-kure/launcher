package components

import (
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the four admission policy kinds share
// (go-kure/launcher#943): validatingadmissionpolicy and its binding, and
// mutatingadmissionpolicy and its binding. Each is a policyFreeKind over the
// object's spec type.
//
// No dimension of the environment policy reads a policy or a binding. The
// kinds are gated as every emitted object is, by the policy's object kind
// rules (oam.ObjectKindPolicy): a policy that forbids the group or the kind,
// or cluster-scoped objects, refuses them. What a policy does once applied is
// out of the build's reach, and the kinds' README entries say so: the API
// server evaluates it on the requests its bindings match, in any namespace; a
// mutating one changes objects after the build, so what the build checked
// need not hold of what is stored; its CEL expressions are compiled by the API
// server, not here; and neither the policy a binding names nor the parameter
// resource it passes is resolved at build.
//
// Each kind is served at admissionregistration.k8s.io/v1 from a Kubernetes
// version of its own (admissionPolicyServedFrom): a cluster below it refuses
// the object at apply. Launcher has no input for the target version, so the
// build does not check it; the kind's schema and README entry state it.
//
// Each kind's required list is derived from the markers of the linked
// k8s.io/api source (TestBuiltinMarkerKinds_RequiredMatchMarkers). The API
// server requires more than the markers say, after its defaults (failurePolicy
// Fail, and a match's matchPolicy Equivalent and two empty selectors). The
// presence rules of that validation are checked here by hand, read from
// pkg/apis/admissionregistration/validation/validation.go of
// k8s.io/kubernetes at Kubernetes v1.37.1. The form of every other value it
// checks is left to the API server: the values of failurePolicy, matchPolicy,
// reinvocationPolicy, a validation action, a parameter-not-found action, a
// patch type, a validation's reason and a rule's operations and scope; that a
// mutating policy matches only CREATE and UPDATE; names, keys and identifiers;
// a parameter kind's apiVersion and kind; how wildcards combine in a rule; the
// limits on the number of match conditions and audit annotations; and that a
// mutation names only the patch its type takes.

// admissionPolicyServedFrom is the Kubernetes version from which the API
// server serves each kind's object at admissionregistration.k8s.io/v1: the
// +k8s:prerelease-lifecycle-gen:introduced marker of the linked k8s.io/api
// source.
var admissionPolicyServedFrom = map[string]string{
	"ValidatingAdmissionPolicy":        "1.30",
	"ValidatingAdmissionPolicyBinding": "1.30",
	"MutatingAdmissionPolicy":          "1.36",
	"MutatingAdmissionPolicyBinding":   "1.36",
}

// admissionPolicyServed is the sentence of a kind's schema that states the
// Kubernetes version from which its object is served.
func admissionPolicyServed(kind string) string {
	return "The API server serves a " + kind + " at admissionregistration.k8s.io/v1 from Kubernetes " + admissionPolicyServedFrom[kind] + "; a cluster below that refuses the object at apply, and the build does not check it."
}

// admissionOperations names the operations a rule of a webhook or a
// validating admission policy takes, and admissionMutatingPolicyOperations
// those of a mutating admission policy, for the messages.
const (
	admissionOperations               = "CREATE, UPDATE, DELETE, CONNECT or *"
	admissionMutatingPolicyOperations = "CREATE, UPDATE or *"
)

// admissionPolicyRequired is the part of a policy's required list its two
// kinds share: the fields of a match condition and a variable the API marks
// required and the Go type writes whether or not they were authored, and the
// key and operator of a match expression of the two selectors of its match
// constraints.
var admissionPolicyRequired = requiredFields(map[string]string{
	"matchConditions[].name":       "the match condition's name",
	"matchConditions[].expression": "the match condition's CEL expression",
	"variables[].name":             "the variable's name, a CEL identifier",
	"variables[].expression":       "the variable's CEL expression",
}, labelSelectorRequired("matchConstraints.namespaceSelector", "matchConstraints.objectSelector"))

// admissionPolicyBindingRequired is the required list of both binding kinds:
// the key and operator of a match expression of each of their three
// selectors.
var admissionPolicyBindingRequired = labelSelectorRequired("paramRef.selector", "matchResources.namespaceSelector", "matchResources.objectSelector")

// validateAdmissionPolicyMatch refuses what the API server requires of a
// policy's matchConstraints beyond the type's markers (validateMatchResources
// and the policy's own spec validation, Kubernetes v1.37.1): the constraints
// are set, and hold at least one resource rule, which provides the policy its
// type information.
func validateAdmissionPolicyMatch(match *admissionregistrationv1.MatchResources, operations string) error {
	switch {
	case match == nil:
		return errors.New("matchConstraints: required (the resources the policy applies to, with at least one resourceRules entry)")
	case len(match.ResourceRules) == 0:
		return errors.New("matchConstraints.resourceRules: required (the rules of the resources the policy applies to, at least one)")
	}
	return validateAdmissionMatchResources("matchConstraints", match, operations)
}

// validateAdmissionMatchResources refuses a rule of the match at the path at
// that leaves out one of its four lists (validateRuleWithOperations), and a
// defective match expression of its two selectors (validateLabelSelector). A
// binding's match needs no rule: it narrows what its policy matches.
func validateAdmissionMatchResources(at string, match *admissionregistrationv1.MatchResources, operations string) error {
	if match == nil {
		return nil
	}
	for _, rules := range []struct {
		name  string
		rules []admissionregistrationv1.NamedRuleWithOperations
	}{{"resourceRules", match.ResourceRules}, {"excludeResourceRules", match.ExcludeResourceRules}} {
		for i, rule := range rules.rules {
			if err := validateRuleWithOperations(fmt.Sprintf("%s.%s[%d]", at, rules.name, i), rule.RuleWithOperations, operations); err != nil {
				return err
			}
		}
	}
	if err := validateLabelSelector(at+".namespaceSelector", match.NamespaceSelector); err != nil {
		return err
	}
	return validateLabelSelector(at+".objectSelector", match.ObjectSelector)
}

// validateAdmissionParamKind refuses a policy's paramKind without its
// apiVersion or its kind (validateParamKind, Kubernetes v1.37.1). Unset, the
// policy takes no parameter.
func validateAdmissionParamKind(kind *admissionregistrationv1.ParamKind) error {
	switch {
	case kind == nil:
		return nil
	case kind.APIVersion == "":
		return errors.New("paramKind.apiVersion: required (the API group and version of the parameter resource, e.g. example.com/v1)")
	case kind.Kind == "":
		return errors.New("paramKind.kind: required (the kind of the parameter resource)")
	}
	return nil
}

// validateAdmissionBinding refuses what the API server requires of a binding
// of either kind beyond the type's markers (validateParamRef and
// validateMatchResources, Kubernetes v1.37.1): it names a policy; a paramRef
// names exactly one of a name and a selector, and what happens when no
// parameter resource matches; and its match is held as
// validateAdmissionMatchResources holds it. policy names the kind of the
// policy a binding applies, for the message.
func validateAdmissionBinding(policy, policyName string, ref *admissionregistrationv1.ParamRef, match *admissionregistrationv1.MatchResources, operations string) error {
	if policyName == "" {
		return errors.Errorf("policyName: required (the name of the %s the binding applies)", policy)
	}
	if ref != nil {
		switch {
		case (ref.Name == "") == (ref.Selector == nil):
			return errors.New("paramRef: exactly one of name and selector is required (the parameter resource, or the parameter resources, the binding passes to the policy)")
		case ref.ParameterNotFoundAction == nil || *ref.ParameterNotFoundAction == "":
			return errors.New("paramRef.parameterNotFoundAction: required (what happens when no parameter resource matches: Allow or Deny)")
		}
		if err := validateLabelSelector("paramRef.selector", ref.Selector); err != nil {
			return err
		}
	}
	return validateAdmissionMatchResources("matchResources", match, operations)
}

// admissionPolicySchema is the properties a policy of either kind shares,
// under one name and one meaning. kind names the object ("ValidatingAdmissionPolicy").
func admissionPolicySchema(kind string) map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"paramKind": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: kind + " spec.paramKind: the kind of the resource a binding passes to the policy as its parameter: apiVersion (required) and kind (required). Unset, the policy takes no parameter. Not resolved at build.",
		},
		"matchConstraints": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + kind + " spec.matchConstraints: the resources the policy applies to, in any namespace: resourceRules (at least one), excludeResourceRules, namespaceSelector, objectSelector, matchPolicy. Each rule names at least one of operations, apiGroups, apiVersions and resources; a selector's match expression takes a key and an operator. Nothing restricts what a policy matches. " + admissionPolicyServed(kind),
		},
		"failurePolicy": {
			Type:        oam.PropertyTypeString,
			Description: kind + " spec.failurePolicy: what happens when the policy cannot be evaluated: Fail or Ignore. Unset, the API server's default of Fail.",
		},
		"matchConditions": {
			Type:        oam.PropertyTypeArray,
			Description: kind + " spec.matchConditions: the conditions a request must meet beyond matchConstraints for the policy to be evaluated. Unauthored, none.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One match condition: name (required) and expression (required), a CEL expression compiled by the API server, not at build.",
			},
		},
		"variables": {
			Type:        oam.PropertyTypeArray,
			Description: kind + " spec.variables: named CEL expressions the policy's other expressions may refer to. Unauthored, none.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One variable: name (required) and expression (required), a CEL expression compiled by the API server, not at build.",
			},
		},
	}
}

// admissionPolicyBindingSchema is the properties a binding of either kind
// shares. kind names the binding's object, policy the policy's.
func admissionPolicyBindingSchema(kind, policy string) map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"policyName": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + kind + " spec.policyName: the name of the " + policy + " the binding applies. Not resolved at build: the policy need not be in the same build. " + admissionPolicyServed(kind),
		},
		"paramRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: kind + " spec.paramRef: the parameter resource the binding passes to the policy, of the policy's paramKind: exactly one of name and selector, namespace, and parameterNotFoundAction (required). A selector's match expression takes a key and an operator. Not resolved at build. Unset, the policy is given no parameter.",
		},
		"matchResources": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: kind + " spec.matchResources: narrows the resources the policy applies to: resourceRules, excludeResourceRules, namespaceSelector, objectSelector, matchPolicy. Each rule names at least one of operations, apiGroups, apiVersions and resources; a selector's match expression takes a key and an operator. Unset, the binding applies the policy to everything its matchConstraints match.",
		},
	}
}
