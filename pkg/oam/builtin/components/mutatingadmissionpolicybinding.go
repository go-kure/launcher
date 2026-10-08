package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// MutatingAdmissionPolicyBindingHandler handles OAM
// mutatingadmissionpolicybinding components: the kind-named projection of a
// cluster-scoped admissionregistration.k8s.io/v1
// MutatingAdmissionPolicyBinding (go-kure/launcher#943).
//
// Its properties are exactly the top-level fields of
// admissionregistrationv1.MutatingAdmissionPolicyBindingSpec, under their
// json names, decoded strictly (decodeKindSpec). It emits the binding, named
// after the component unless `objectName` names it, with no namespace, and
// nothing else. The policy it names need not be in the same build, and is not
// resolved. See admission_policy_common.go for what gates it and what the
// build does not reach.
type MutatingAdmissionPolicyBindingHandler struct{}

// CanHandle returns true for the mutatingadmissionpolicybinding component
// type.
func (h *MutatingAdmissionPolicyBindingHandler) CanHandle(componentType string) bool {
	return componentType == "mutatingadmissionpolicybinding"
}

// PropertySchema declares every top-level
// admissionregistrationv1.MutatingAdmissionPolicyBindingSpec field by its
// json name.
func (h *MutatingAdmissionPolicyBindingHandler) PropertySchema() map[string]oam.PropertySchema {
	return admissionPolicyBindingSchema("MutatingAdmissionPolicyBinding", "MutatingAdmissionPolicy")
}

// mutatingAdmissionPolicyBindingKind is the mutatingadmissionpolicybinding
// kind: see policyFreeKind and admission_policy_common.go
// (validateAdmissionBinding, read from
// validateMutatingAdmissionPolicyBindingSpec, Kubernetes v1.37.1).
var mutatingAdmissionPolicyBindingKind = &policyFreeKind[admissionregistrationv1.MutatingAdmissionPolicyBindingSpec]{
	upstream: "admissionregistration.k8s.io/v1 MutatingAdmissionPolicyBindingSpec",
	required: admissionPolicyBindingRequired,
	validate: func(spec *admissionregistrationv1.MutatingAdmissionPolicyBindingSpec) error {
		return validateAdmissionBinding("MutatingAdmissionPolicy", spec.PolicyName, spec.ParamRef, spec.MatchResources, admissionMutatingPolicyOperations)
	},
	build: func(name, _ string, spec *admissionregistrationv1.MutatingAdmissionPolicyBindingSpec) client.Object {
		binding := kubernetes.CreateMutatingAdmissionPolicyBinding(name)
		spec.DeepCopyInto(&binding.Spec)
		return binding
	},
}

// ToApplicationConfig decodes an OAM mutatingadmissionpolicybinding component
// into its config. The build namespace is not used: a
// MutatingAdmissionPolicyBinding is cluster-scoped.
func (h *MutatingAdmissionPolicyBindingHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return mutatingAdmissionPolicyBindingKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MutatingAdmissionPolicyBindingHandler) ContractMetadata() oam.ContractMetadata {
	return contract("mutatingadmissionpolicybinding")
}

// ComponentObject declares the mutatingadmissionpolicybinding kind's
// MutatingAdmissionPolicyBinding, which is cluster-scoped.
func (h *MutatingAdmissionPolicyBindingHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: admissionregistrationv1.GroupName, Kind: "MutatingAdmissionPolicyBinding"}, oam.ObjectScopeCluster
}
