package components

import (
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ValidatingAdmissionPolicyHandler handles OAM validatingadmissionpolicy
// components: the kind-named projection of a cluster-scoped
// admissionregistration.k8s.io/v1 ValidatingAdmissionPolicy
// (go-kure/launcher#943).
//
// Its properties are exactly the top-level fields of
// admissionregistrationv1.ValidatingAdmissionPolicySpec, under their json
// names, decoded strictly (decodeKindSpec). It emits the policy, named after
// the component unless `objectName` names it, with no namespace, and nothing
// else. A policy does nothing until a validatingadmissionpolicybinding names
// it. See admission_policy_common.go for what gates it and what the build
// does not reach.
type ValidatingAdmissionPolicyHandler struct{}

// CanHandle returns true for the validatingadmissionpolicy component type.
func (h *ValidatingAdmissionPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "validatingadmissionpolicy"
}

// PropertySchema declares every top-level
// admissionregistrationv1.ValidatingAdmissionPolicySpec field by its json
// name.
func (h *ValidatingAdmissionPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	props := admissionPolicySchema("ValidatingAdmissionPolicy")
	maps.Copy(props, map[string]oam.PropertySchema{
		"validations": {
			Type:        oam.PropertyTypeArray,
			Description: "ValidatingAdmissionPolicy spec.validations: the CEL expressions a matched request must pass. At least one of validations and auditAnnotations is required.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One validation: expression (required), message, messageExpression, reason. The expressions are compiled by the API server, not at build.",
			},
		},
		"auditAnnotations": {
			Type:        oam.PropertyTypeArray,
			Description: "ValidatingAdmissionPolicy spec.auditAnnotations: the audit annotations the policy adds to the audit event of a matched request. At least one of validations and auditAnnotations is required.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One audit annotation: key (required) and valueExpression (required), a CEL expression compiled by the API server, not at build.",
			},
		},
	})
	return props
}

// validatingAdmissionPolicyKind is the validatingadmissionpolicy kind: see
// policyFreeKind and admission_policy_common.go. The Go type always encodes a
// validation's expression and an audit annotation's key and valueExpression,
// which the API requires, so they are on the required list.
//
// Read from validateValidatingAdmissionPolicySpec, Kubernetes v1.37.1: beyond
// its match constraints and parameter kind (admission_policy_common.go), a
// policy holds at least one validation or audit annotation.
var validatingAdmissionPolicyKind = &policyFreeKind[admissionregistrationv1.ValidatingAdmissionPolicySpec]{
	upstream: "admissionregistration.k8s.io/v1 ValidatingAdmissionPolicySpec",
	required: requiredFields(admissionPolicyRequired, map[string]string{
		"validations[].expression":           "the validation's CEL expression",
		"auditAnnotations[].key":             "the audit annotation's key",
		"auditAnnotations[].valueExpression": "the audit annotation's CEL value expression",
	}),
	validate: func(spec *admissionregistrationv1.ValidatingAdmissionPolicySpec) error {
		if err := validateAdmissionPolicyMatch(spec.MatchConstraints, admissionOperations); err != nil {
			return err
		}
		if len(spec.Validations) == 0 && len(spec.AuditAnnotations) == 0 {
			return errors.New("validations: at least one of validations and auditAnnotations is required (what the policy checks of a matched request, or records of it)")
		}
		return validateAdmissionParamKind(spec.ParamKind)
	},
	build: func(name, _ string, spec *admissionregistrationv1.ValidatingAdmissionPolicySpec) client.Object {
		policy := kubernetes.CreateValidatingAdmissionPolicy(name)
		spec.DeepCopyInto(&policy.Spec)
		return policy
	},
}

// ToApplicationConfig decodes an OAM validatingadmissionpolicy component into
// its config. The build namespace is not used: a ValidatingAdmissionPolicy is
// cluster-scoped.
func (h *ValidatingAdmissionPolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return validatingAdmissionPolicyKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ValidatingAdmissionPolicyHandler) ContractMetadata() oam.ContractMetadata {
	return contract("validatingadmissionpolicy")
}

// ComponentObject declares the validatingadmissionpolicy kind's
// ValidatingAdmissionPolicy, which is cluster-scoped.
func (h *ValidatingAdmissionPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: admissionregistrationv1.GroupName, Kind: "ValidatingAdmissionPolicy"}, oam.ObjectScopeCluster
}
