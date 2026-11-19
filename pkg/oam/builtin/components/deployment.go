package components

import "github.com/go-kure/launcher/pkg/oam"

// DeploymentHandler handles the kind-named OAM `deployment` component.
//
// It is transitional (go-kure/launcher#1629): the reshaped parser moved to
// RoleDeploymentHandler, the type `webservice` and `worker` lower into, and
// this handler wraps it so that `deployment` reads the same reshaped form until
// the kind takes the upstream DeploymentSpec. Only what names the type is its
// own: the answer to CanHandle and the refusal hints, which read the
// `deployment` rows of the refusal tables.
type DeploymentHandler struct{ RoleDeploymentHandler }

// CanHandle returns true for the deployment component type, never for the role
// member type.
func (h *DeploymentHandler) CanHandle(componentType string) bool {
	return componentType == "deployment"
}

// NestedUnsupportedFieldHint is the deployment's refusedNestedKeyHint.
func (h *DeploymentHandler) NestedUnsupportedFieldHint(parents []string, key string) string {
	return refusedNestedKeyHint("deployment", parents, key)
}

// UnsupportedFieldHint is the deployment's refusedKeyHint.
func (h *DeploymentHandler) UnsupportedFieldHint(key string) string {
	return refusedKeyHint("deployment", key)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *DeploymentHandler) ContractMetadata() oam.ContractMetadata { return contract("deployment") }
