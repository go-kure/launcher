package components

import (
	"fmt"
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// MutatingAdmissionPolicyHandler handles OAM mutatingadmissionpolicy
// components: the kind-named projection of a cluster-scoped
// admissionregistration.k8s.io/v1 MutatingAdmissionPolicy
// (go-kure/launcher#943).
//
// Its properties are exactly the top-level fields of
// admissionregistrationv1.MutatingAdmissionPolicySpec, under their json
// names, decoded strictly (decodeKindSpec). It emits the policy, named after
// the component unless `objectName` names it, with no namespace, and nothing
// else. A policy does nothing until a mutatingadmissionpolicybinding names
// it. See admission_policy_common.go for what gates it and what the build
// does not reach.
type MutatingAdmissionPolicyHandler struct{}

// CanHandle returns true for the mutatingadmissionpolicy component type.
func (h *MutatingAdmissionPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "mutatingadmissionpolicy"
}

// PropertySchema declares every top-level
// admissionregistrationv1.MutatingAdmissionPolicySpec field by its json name.
func (h *MutatingAdmissionPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	props := admissionPolicySchema("MutatingAdmissionPolicy")
	maps.Copy(props, map[string]oam.PropertySchema{
		"mutations": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. MutatingAdmissionPolicy spec.mutations: the changes the policy makes to a matched object, at least one, applied in order. They change objects after the build, so what the build checked need not hold of what is stored.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One mutation: patchType (required: ApplyConfiguration or JSONPatch) and the patch of that type, applyConfiguration or jsonPatch, with its expression (required), a CEL expression compiled by the API server, not at build.",
			},
		},
		"reinvocationPolicy": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. MutatingAdmissionPolicy spec.reinvocationPolicy: whether the policy runs again when a later admission plugin changes the object: Never or IfNeeded.",
		},
	})
	return props
}

// mutatingAdmissionPolicyKind is the mutatingadmissionpolicy kind: see
// policyFreeKind and admission_policy_common.go. The Go type always encodes a
// mutation's patchType, which the API requires, so it is on the required
// list.
//
// Read from validateMutatingAdmissionPolicySpec and validateMutation,
// Kubernetes v1.37.1: beyond its match constraints and parameter kind
// (admission_policy_common.go), a policy holds at least one mutation and a
// reinvocation policy, which the API server does not default; and a mutation
// holds the patch its type names, with an expression.
var mutatingAdmissionPolicyKind = &policyFreeKind[admissionregistrationv1.MutatingAdmissionPolicySpec]{
	upstream: "admissionregistration.k8s.io/v1 MutatingAdmissionPolicySpec",
	required: requiredFields(admissionPolicyRequired, map[string]string{
		"mutations[].patchType": "the mutation's patch type: ApplyConfiguration or JSONPatch",
	}),
	validate: func(spec *admissionregistrationv1.MutatingAdmissionPolicySpec) error {
		if err := validateAdmissionPolicyMatch(spec.MatchConstraints, admissionMutatingPolicyOperations); err != nil {
			return err
		}
		if len(spec.Mutations) == 0 {
			return errors.New("mutations: required (the changes the policy makes to a matched object, at least one)")
		}
		for i, mutation := range spec.Mutations {
			if err := validateAdmissionMutation(fmt.Sprintf("mutations[%d]", i), mutation); err != nil {
				return err
			}
		}
		if spec.ReinvocationPolicy == "" {
			return errors.New("reinvocationPolicy: required (whether the policy runs again when a later admission plugin changes the object: Never or IfNeeded)")
		}
		return validateAdmissionParamKind(spec.ParamKind)
	},
	build: func(name, _ string, spec *admissionregistrationv1.MutatingAdmissionPolicySpec) client.Object {
		policy := kubernetes.CreateMutatingAdmissionPolicy(name)
		spec.DeepCopyInto(&policy.Spec)
		return policy
	},
}

// validateAdmissionMutation refuses a mutation at the path at without the
// patch its type names, or with that patch's expression empty. A patch type
// that is neither is left to the API server, and so is the other patch beside
// it.
func validateAdmissionMutation(at string, mutation admissionregistrationv1.Mutation) error {
	switch mutation.PatchType {
	case admissionregistrationv1.PatchTypeApplyConfiguration:
		switch {
		case mutation.ApplyConfiguration == nil:
			return errors.Errorf("%s.applyConfiguration: required with the patchType ApplyConfiguration (the apply configuration the mutation merges into the object)", at)
		case mutation.ApplyConfiguration.Expression == "":
			return errors.Errorf("%s.applyConfiguration.expression: required (the CEL expression that yields the apply configuration)", at)
		}
	case admissionregistrationv1.PatchTypeJSONPatch:
		switch {
		case mutation.JSONPatch == nil:
			return errors.Errorf("%s.jsonPatch: required with the patchType JSONPatch (the JSON patch the mutation applies to the object)", at)
		case mutation.JSONPatch.Expression == "":
			return errors.Errorf("%s.jsonPatch.expression: required (the CEL expression that yields the JSON patch operations)", at)
		}
	}
	return nil
}

// ToApplicationConfig decodes an OAM mutatingadmissionpolicy component into
// its config. The build namespace is not used: a MutatingAdmissionPolicy is
// cluster-scoped.
func (h *MutatingAdmissionPolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return mutatingAdmissionPolicyKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MutatingAdmissionPolicyHandler) ContractMetadata() oam.ContractMetadata {
	return contract("mutatingadmissionpolicy")
}

// ComponentObject declares the mutatingadmissionpolicy kind's
// MutatingAdmissionPolicy, which is cluster-scoped.
func (h *MutatingAdmissionPolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: admissionregistrationv1.GroupName, Kind: "MutatingAdmissionPolicy"}, oam.ObjectScopeCluster
}
