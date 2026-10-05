package components

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ClusterRoleHandler handles OAM clusterrole components: the kind-named
// projection of a cluster-scoped rbac.authorization.k8s.io/v1 ClusterRole
// (go-kure/launcher#790).
//
// A ClusterRole has no spec: its properties, `rules` and `aggregationRule`,
// are the object's own fields, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the ClusterRole,
// named after the component unless `objectName` names it, with no namespace,
// and nothing else. The kind is ungated: see rbac_common.go.
type ClusterRoleHandler struct{}

// CanHandle returns true for the clusterrole component type.
func (h *ClusterRoleHandler) CanHandle(componentType string) bool {
	return componentType == "clusterrole"
}

// PropertySchema declares every authorable rbacv1.ClusterRole field by its
// json name.
func (h *ClusterRoleHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"rules": rbacRulesSchema("ClusterRole"),
		"aggregationRule": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "ClusterRole aggregationRule: clusterRoleSelectors, the label selectors, at least one, of the ClusterRoles whose rules the control plane writes into this one. A match expression's key and operator are required, the operator is one of In, NotIn, Exists and DoesNotExist, and In and NotIn take at least one value where Exists and DoesNotExist take none. With it, leave rules unauthored: the control plane fills them.",
		},
	}
}

// clusterRoleKind is the clusterrole kind: see policyFreeKind and
// rbac_common.go. A match expression of an aggregation selector is a type of
// k8s.io/apimachinery, whose key and operator the API requires and the Go type
// writes whether or not they were authored (labelSelectorRequired).
var clusterRoleKind = &policyFreeKind[rbacv1.ClusterRole]{
	upstream:    "rbac.authorization.k8s.io/v1 ClusterRole (a clusterrole component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required:    requiredFields(rbacRuleRequired, labelSelectorRequired("aggregationRule.clusterRoleSelectors[]")),
	validate: func(role *rbacv1.ClusterRole) error {
		if err := validatePolicyRules(role.Rules, false); err != nil {
			return err
		}
		rule := role.AggregationRule
		if rule == nil {
			return nil
		}
		// Read from ValidateClusterRole, pkg/apis/rbac/validation/validation.go,
		// Kubernetes v1.37.1: an aggregation rule needs at least one selector,
		// and each is validated as a label selector (validateLabelSelector).
		if len(rule.ClusterRoleSelectors) == 0 {
			return errors.New("aggregationRule.clusterRoleSelectors: required (the label selectors of the ClusterRoles whose rules are aggregated, at least one)")
		}
		for i := range rule.ClusterRoleSelectors {
			if err := validateLabelSelector(fmt.Sprintf("aggregationRule.clusterRoleSelectors[%d]", i), &rule.ClusterRoleSelectors[i]); err != nil {
				return err
			}
		}
		return nil
	},
	build: func(name, _ string, authored *rbacv1.ClusterRole) client.Object {
		identity := kubernetes.CreateClusterRole(name)
		role := authored.DeepCopy()
		role.TypeMeta, role.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return role
	},
}

// ToApplicationConfig decodes an OAM clusterrole component into its config.
// The build namespace is not used: a ClusterRole is cluster-scoped.
func (h *ClusterRoleHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return clusterRoleKind.config(component)
}
