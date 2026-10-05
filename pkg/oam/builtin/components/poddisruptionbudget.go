package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	policyv1 "k8s.io/api/policy/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// PodDisruptionBudgetHandler handles OAM poddisruptionbudget components: the
// kind-named projection of a policy/v1 PodDisruptionBudget
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// policyv1.PodDisruptionBudgetSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the PodDisruptionBudget, named after the
// component unless `objectName` names it, in the build namespace, and nothing
// else. `selector` is the author's: launcher points it at no component, and an
// unset one selects no pod. The `scaler` trait's `enablePDB` is the budget
// launcher derives for a workload; this kind is the authored object.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type PodDisruptionBudgetHandler struct{}

// CanHandle returns true for the poddisruptionbudget component type.
func (h *PodDisruptionBudgetHandler) CanHandle(componentType string) bool {
	return componentType == "poddisruptionbudget"
}

// PropertySchema declares every top-level policyv1.PodDisruptionBudgetSpec
// field by its json name. The selector is an open object whose content is
// checked by the strict decode, not by this schema.
func (h *PodDisruptionBudgetHandler) PropertySchema() map[string]oam.PropertySchema {
	intOrPercent := []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString}
	return map[string]oam.PropertySchema{
		"minAvailable": {
			Types:       intOrPercent,
			Description: "PodDisruptionBudget spec.minAvailable: an eviction is allowed while at least this many of the selected pods stay available, a count or a percentage (\"50%\"). The API allows it or maxUnavailable, not both.",
		},
		"selector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PodDisruptionBudget spec.selector: the label query over the pods whose evictions the budget manages (matchLabels, matchExpressions). Unset selects no pod; {} selects every pod of the namespace. Decoded strictly into the Kubernetes API type: see LabelSelector in the Kubernetes API reference.",
		},
		"maxUnavailable": {
			Types:       intOrPercent,
			Description: "PodDisruptionBudget spec.maxUnavailable: an eviction is allowed while at most this many of the selected pods are unavailable, a count or a percentage (\"25%\"); 0 allows no voluntary eviction. The API allows it or minAvailable, not both.",
		},
		"unhealthyPodEvictionPolicy": {
			Type:        oam.PropertyTypeString,
			Description: "PodDisruptionBudget spec.unhealthyPodEvictionPolicy: when a running pod that is not yet healthy may be evicted: IfHealthyBudget or AlwaysAllow. The API server reads an unset one as IfHealthyBudget.",
		},
	}
}

// podDisruptionBudgetKind is the poddisruptionbudget kind: see policyFreeKind.
// The API requires no field of the spec itself. Of a match expression of the
// selector it requires the key and the operator, which the Go type writes
// whether or not they were authored (labelSelectorRequired), and it refuses
// what validateLabelSelector does (ValidatePodDisruptionBudgetSpec in
// pkg/apis/policy/validation, Kubernetes v1.37.1, validates the selector as a
// label selector). Its other value rules, the one that minAvailable and
// maxUnavailable exclude each other included, are left to the API server.
var podDisruptionBudgetKind = &policyFreeKind[policyv1.PodDisruptionBudgetSpec]{
	upstream: "policy/v1 PodDisruptionBudgetSpec",
	required: labelSelectorRequired("selector"),
	validate: func(spec *policyv1.PodDisruptionBudgetSpec) error {
		return validateLabelSelector("selector", spec.Selector)
	},
	build: func(name, namespace string, spec *policyv1.PodDisruptionBudgetSpec) client.Object {
		pdb := kubernetes.CreatePodDisruptionBudget(name, namespace)
		spec.DeepCopyInto(&pdb.Spec)
		return pdb
	},
}

// ToApplicationConfig decodes an OAM poddisruptionbudget component into its
// config. The object takes the namespace of the application it is generated
// in.
func (h *PodDisruptionBudgetHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return podDisruptionBudgetKind.config(component)
}
