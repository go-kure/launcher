package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumNodeConfigHandler handles OAM cilium-nodeconfig components: the
// kind-named projection of a cilium.io/v2 CiliumNodeConfig
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumNodeConfigSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the CiliumNodeConfig, named after the component
// unless `objectName` names it, in the build namespace, and nothing else.
// `nodeSelector` is the author's, and so is every key of `defaults`: launcher
// reads neither. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type CiliumNodeConfigHandler struct{}

// CanHandle returns true for the cilium-nodeconfig component type.
func (h *CiliumNodeConfigHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-nodeconfig"
}

// PropertySchema declares every top-level ciliumv2.CiliumNodeConfigSpec field
// by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *CiliumNodeConfigHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"defaults": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. CiliumNodeConfig spec.defaults: Cilium configuration keys with their values, all strings, read by the agent of a selected node as it reads its configuration ConfigMap. Launcher checks neither a key nor a value.",
		},
		"nodeSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. CiliumNodeConfig spec.nodeSelector: the label query over the nodes the configuration applies to (matchLabels, matchExpressions). Empty, every node.",
		},
	}
}

// ciliumNodeConfigKind is the cilium-nodeconfig kind: see policyFreeKind. The
// CRD has no expression rule. What a key of `defaults` means to Cilium, and
// whether it is one Cilium knows, is not read.
var ciliumNodeConfigKind = &policyFreeKind[ciliumv2.CiliumNodeConfigSpec]{
	upstream: "cilium.io/v2 CiliumNodeConfigSpec",
	required: requiredFields(map[string]string{
		"defaults":     "the Cilium configuration keys the selected nodes take, with their values",
		"nodeSelector": "the label query over the nodes the configuration applies to; an empty one selects every node",
	}, labelSelectorRequired("nodeSelector")),
	build: func(name, namespace string, spec *ciliumv2.CiliumNodeConfigSpec) client.Object {
		config := kurecilium.CreateCiliumNodeConfig(name, namespace)
		spec.DeepCopyInto(&config.Spec)
		return config
	},
}

// ToApplicationConfig decodes an OAM cilium-nodeconfig component into its
// config. The object lands in the application's namespace at Generate.
func (h *CiliumNodeConfigHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumNodeConfigKind.config(component)
}
