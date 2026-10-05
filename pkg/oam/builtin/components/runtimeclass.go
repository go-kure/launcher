package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	nodev1 "k8s.io/api/node/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// RuntimeClassHandler handles OAM runtimeclass components: the kind-named
// projection of a node.k8s.io/v1 RuntimeClass (go-kure/launcher#790).
//
// A RuntimeClass has no spec: its properties are the object's own top-level
// fields, under their json names, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the RuntimeClass,
// named after the component unless `objectName` names it, and nothing else. A
// RuntimeClass is cluster-scoped: the object carries no namespace, whatever
// namespace the application is built for. TestCoreKindSchemas_CoverSpec keeps
// the published key set equal to the upstream json tags, less the object's own
// identity.
type RuntimeClassHandler struct{}

// CanHandle returns true for the runtimeclass component type.
func (h *RuntimeClassHandler) CanHandle(componentType string) bool {
	return componentType == "runtimeclass"
}

// PropertySchema declares every authorable nodev1.RuntimeClass field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *RuntimeClassHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"handler": {
			Type:        oam.PropertyTypeString,
			Required:    true,
			Description: "Required. RuntimeClass handler: the runtime configuration the node's CRI implementation uses for pods of this class, e.g. runc or kata. A lowercase DNS label, immutable once created.",
		},
		"overhead": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "RuntimeClass overhead: the resources running a pod of this class costs beside its containers (podFixed: a map of resource name to quantity). Admission adds it to each such pod. Not held to the environment policy's resource maxima.",
		},
		"scheduling": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "RuntimeClass scheduling: the nodes that support the class (nodeSelector, tolerations); admission merges both into each pod of the class. Decoded strictly into the Kubernetes API type: see Scheduling in the Kubernetes API reference.",
		},
	}
}

// runtimeClassKind is the runtimeclass kind: see policyFreeKind. The API
// requires handler, which the Go type would write as "" when unauthored. The
// API's other value rules are left to the API server.
var runtimeClassKind = &policyFreeKind[nodev1.RuntimeClass]{
	upstream:    "node.k8s.io/v1 RuntimeClass (a runtimeclass component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	validate: func(rc *nodev1.RuntimeClass) error {
		if rc.Handler == "" {
			return errors.New("handler: required (the runtime configuration the node's CRI implementation uses for pods of this class)")
		}
		return nil
	},
	build: func(name, _ string, authored *nodev1.RuntimeClass) client.Object {
		identity := kubernetes.CreateRuntimeClass(name)
		rc := authored.DeepCopy()
		rc.TypeMeta, rc.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return rc
	},
}

// ToApplicationConfig decodes an OAM runtimeclass component into its config.
// The build namespace is not used: a RuntimeClass is cluster-scoped.
func (h *RuntimeClassHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return runtimeClassKind.config(component)
}
