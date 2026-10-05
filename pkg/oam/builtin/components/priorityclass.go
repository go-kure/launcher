package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// PriorityClassHandler handles OAM priorityclass components: the kind-named
// projection of a scheduling.k8s.io/v1 PriorityClass (go-kure/launcher#790).
//
// A PriorityClass has no spec: its properties are the object's own top-level
// fields, under their json names, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the PriorityClass,
// named after the component, and nothing else. A PriorityClass is
// cluster-scoped: the object carries no namespace, whatever namespace the
// application is built for. TestCoreKindSchemas_CoverSpec keeps the published
// key set equal to the upstream json tags, less the object's own identity.
type PriorityClassHandler struct{}

// CanHandle returns true for the priorityclass component type.
func (h *PriorityClassHandler) CanHandle(componentType string) bool {
	return componentType == "priorityclass"
}

// PropertySchema declares every authorable schedulingv1.PriorityClass field by
// its json name.
func (h *PriorityClassHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"value": {
			Type:        oam.PropertyTypeInteger,
			Description: "PriorityClass value: the priority a pod receives when it names this class; a higher value is scheduled first and preempts lower ones. Unauthored, the object carries value: 0. Immutable once created.",
		},
		"globalDefault": {
			Type:        oam.PropertyTypeBoolean,
			Description: "PriorityClass globalDefault: whether pods that name no priority class receive this one. At most one class of a cluster should set it.",
		},
		"description": {
			Type:        oam.PropertyTypeString,
			Description: "PriorityClass description: free text saying when the class should be used.",
		},
		"preemptionPolicy": {
			Type:        oam.PropertyTypeString,
			Description: "PriorityClass preemptionPolicy: whether a pod of this class may preempt lower-priority pods: PreemptLowerPriority or Never. The API server defaults it to PreemptLowerPriority.",
		},
	}
}

// priorityClassKind is the priorityclass kind: see policyFreeKind. The API
// requires no field. The Go type always encodes value, so a component that
// authors none emits value: 0, which is what the API reads an absent value as.
// The API's value rules are left to the API server.
var priorityClassKind = &policyFreeKind[schedulingv1.PriorityClass]{
	upstream:    "scheduling.k8s.io/v1 PriorityClass (a priorityclass component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	build: func(name, _ string, authored *schedulingv1.PriorityClass) client.Object {
		identity := kubernetes.CreatePriorityClass(name)
		pc := authored.DeepCopy()
		pc.TypeMeta, pc.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return pc
	},
}

// ToApplicationConfig decodes an OAM priorityclass component into its config.
// The build namespace is not used: a PriorityClass is cluster-scoped.
func (h *PriorityClassHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return priorityClassKind.config(component)
}
