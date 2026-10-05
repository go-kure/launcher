package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// RoleBindingHandler handles OAM rolebinding components: the kind-named
// projection of a rbac.authorization.k8s.io/v1 RoleBinding
// (go-kure/launcher#790).
//
// A RoleBinding has no spec: its properties, `subjects` and `roleRef`, are the
// object's own fields, decoded strictly; its kind, apiVersion and metadata are
// launcher's to set and are refused. It emits the RoleBinding, named after the
// component unless `objectName` names it, in the build namespace, and nothing
// else. The names in its roleRef and its subjects are the author's literals.
// The kind is ungated: see rbac_common.go.
type RoleBindingHandler struct{}

// CanHandle returns true for the rolebinding component type.
func (h *RoleBindingHandler) CanHandle(componentType string) bool {
	return componentType == "rolebinding"
}

// PropertySchema declares every authorable rbacv1.RoleBinding field by its
// json name.
func (h *RoleBindingHandler) PropertySchema() map[string]oam.PropertySchema {
	return rbacBindingSchema("RoleBinding", "a Role of the binding's namespace or a ClusterRole")
}

// roleBindingKind is the rolebinding kind: see policyFreeKind and
// rbac_common.go. The API server's validation requires no field of a
// RoleBinding beyond its markers (ValidateRoleBinding,
// pkg/apis/rbac/validation/validation.go, Kubernetes v1.37.1): a
// ServiceAccount subject without a namespace passes that validation in a
// namespaced binding.
var roleBindingKind = &policyFreeKind[rbacv1.RoleBinding]{
	upstream:    "rbac.authorization.k8s.io/v1 RoleBinding (a rolebinding component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required:    rbacBindingRequired,
	build: func(name, namespace string, authored *rbacv1.RoleBinding) client.Object {
		identity := kubernetes.CreateRoleBinding(name, namespace)
		binding := authored.DeepCopy()
		binding.TypeMeta, binding.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return binding
	},
}

// ToApplicationConfig decodes an OAM rolebinding component into its config.
// The object lands in the application's namespace at Generate.
func (h *RoleBindingHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return roleBindingKind.config(component)
}
