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

// ValidatingAdmissionPolicyBindingHandler handles OAM
// validatingadmissionpolicybinding components: the kind-named projection of a
// cluster-scoped admissionregistration.k8s.io/v1
// ValidatingAdmissionPolicyBinding (go-kure/launcher#943).
//
// Its properties are exactly the top-level fields of
// admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec, under their
// json names, decoded strictly (decodeKindSpec). It emits the binding, named
// after the component unless `objectName` names it, with no namespace, and
// nothing else. The policy it names need not be in the same build, and is not
// resolved. See admission_policy_common.go for what gates it and what the
// build does not reach.
type ValidatingAdmissionPolicyBindingHandler struct{}

// CanHandle returns true for the validatingadmissionpolicybinding component
// type.
func (h *ValidatingAdmissionPolicyBindingHandler) CanHandle(componentType string) bool {
	return componentType == "validatingadmissionpolicybinding"
}

// PropertySchema declares every top-level
// admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec field by its
// json name.
func (h *ValidatingAdmissionPolicyBindingHandler) PropertySchema() map[string]oam.PropertySchema {
	props := admissionPolicyBindingSchema("ValidatingAdmissionPolicyBinding", "ValidatingAdmissionPolicy")
	maps.Copy(props, map[string]oam.PropertySchema{
		"validationActions": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. ValidatingAdmissionPolicyBinding spec.validationActions: what a failed validation of the policy does, at least one: Deny, Warn or Audit, not Deny beside Warn.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One validation action: Deny, Warn or Audit."},
		},
	})
	return props
}

// validatingAdmissionPolicyBindingKind is the
// validatingadmissionpolicybinding kind: see policyFreeKind and
// admission_policy_common.go.
//
// Read from validateValidatingAdmissionPolicyBindingSpec, Kubernetes v1.37.1:
// beyond what validateAdmissionBinding holds, a binding names at least one
// validation action. Their values, and that Deny and Warn do not stand
// together, are left to the API server.
var validatingAdmissionPolicyBindingKind = &policyFreeKind[admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec]{
	upstream: "admissionregistration.k8s.io/v1 ValidatingAdmissionPolicyBindingSpec",
	required: admissionPolicyBindingRequired,
	validate: func(spec *admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec) error {
		if err := validateAdmissionBinding("ValidatingAdmissionPolicy", spec.PolicyName, spec.ParamRef, spec.MatchResources, admissionOperations); err != nil {
			return err
		}
		if len(spec.ValidationActions) == 0 {
			return errors.New("validationActions: required (what a failed validation of the policy does, at least one: Deny, Warn or Audit)")
		}
		return nil
	},
	build: func(name, _ string, spec *admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec) client.Object {
		binding := kubernetes.CreateValidatingAdmissionPolicyBinding(name)
		spec.DeepCopyInto(&binding.Spec)
		return binding
	},
}

// ToApplicationConfig decodes an OAM validatingadmissionpolicybinding
// component into its config. The build namespace is not used: a
// ValidatingAdmissionPolicyBinding is cluster-scoped.
func (h *ValidatingAdmissionPolicyBindingHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return validatingAdmissionPolicyBindingKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ValidatingAdmissionPolicyBindingHandler) ContractMetadata() oam.ContractMetadata {
	return contract("validatingadmissionpolicybinding")
}

// ComponentObject declares the validatingadmissionpolicybinding kind's
// ValidatingAdmissionPolicyBinding, which is cluster-scoped.
func (h *ValidatingAdmissionPolicyBindingHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: admissionregistrationv1.GroupName, Kind: "ValidatingAdmissionPolicyBinding"}, oam.ObjectScopeCluster
}
