package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ClusterRoleBindingHandler handles OAM clusterrolebinding components: the
// kind-named projection of a cluster-scoped rbac.authorization.k8s.io/v1
// ClusterRoleBinding (go-kure/launcher#790).
//
// A ClusterRoleBinding has no spec: its properties, `subjects` and `roleRef`,
// are the object's own fields, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the
// ClusterRoleBinding, named after the component unless `objectName` names it,
// with no namespace, and nothing else. The names in its roleRef and its
// subjects are the author's literals. The kind is ungated: see
// rbac_common.go.
type ClusterRoleBindingHandler struct{}

// CanHandle returns true for the clusterrolebinding component type.
func (h *ClusterRoleBindingHandler) CanHandle(componentType string) bool {
	return componentType == "clusterrolebinding"
}

// PropertySchema declares every authorable rbacv1.ClusterRoleBinding field by
// its json name.
func (h *ClusterRoleBindingHandler) PropertySchema() map[string]oam.PropertySchema {
	return rbacBindingSchema("ClusterRoleBinding", "a ClusterRole")
}

// clusterRoleBindingKind is the clusterrolebinding kind: see policyFreeKind
// and rbac_common.go.
var clusterRoleBindingKind = &policyFreeKind[rbacv1.ClusterRoleBinding]{
	upstream:    "rbac.authorization.k8s.io/v1 ClusterRoleBinding (a clusterrolebinding component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required:    rbacBindingRequired,
	validate: func(binding *rbacv1.ClusterRoleBinding) error {
		// Read from ValidateRoleBindingSubject,
		// pkg/apis/rbac/validation/validation.go, Kubernetes v1.37.1: a
		// ServiceAccount subject of a binding that is in no namespace needs
		// its own.
		for i, subject := range binding.Subjects {
			if subject.Kind == rbacv1.ServiceAccountKind && subject.Namespace == "" {
				return errors.Errorf("subjects[%d].namespace: required (the namespace of the ServiceAccount: a ClusterRoleBinding is in none)", i)
			}
		}
		return nil
	},
	build: func(name, _ string, authored *rbacv1.ClusterRoleBinding) client.Object {
		identity := kubernetes.CreateClusterRoleBinding(name)
		binding := authored.DeepCopy()
		binding.TypeMeta, binding.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return binding
	},
}

// ToApplicationConfig decodes an OAM clusterrolebinding component into its
// config. The build namespace is not used: a ClusterRoleBinding is
// cluster-scoped.
func (h *ClusterRoleBindingHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return clusterRoleBindingKind.config(component)
}
