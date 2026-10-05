package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// RoleHandler handles OAM role components: the kind-named projection of a
// rbac.authorization.k8s.io/v1 Role (go-kure/launcher#790).
//
// A Role has no spec: its one property, `rules`, is the object's own field,
// decoded strictly; its kind, apiVersion and metadata are launcher's to set
// and are refused. It emits the Role, named after the component unless
// `objectName` names it, in the build namespace, and nothing else: no binding
// and no ServiceAccount. The kind is ungated: see rbac_common.go.
type RoleHandler struct{}

// CanHandle returns true for the role component type.
func (h *RoleHandler) CanHandle(componentType string) bool {
	return componentType == "role"
}

// PropertySchema declares the one authorable rbacv1.Role field by its json
// name.
func (h *RoleHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{"rules": rbacRulesSchema("Role")}
}

// roleKind is the role kind: see policyFreeKind and rbac_common.go.
var roleKind = &policyFreeKind[rbacv1.Role]{
	upstream:    "rbac.authorization.k8s.io/v1 Role (a role component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required:    rbacRuleRequired,
	validate: func(role *rbacv1.Role) error {
		return validatePolicyRules(role.Rules, true)
	},
	build: func(name, namespace string, authored *rbacv1.Role) client.Object {
		identity := kubernetes.CreateRole(name, namespace)
		role := authored.DeepCopy()
		role.TypeMeta, role.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return role
	},
}

// ToApplicationConfig decodes an OAM role component into its config. The
// object lands in the application's namespace at Generate.
func (h *RoleHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return roleKind.config(component)
}
