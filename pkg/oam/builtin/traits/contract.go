package traits

import (
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// This file holds the contract metadata of every built-in trait handler and
// lowering rule (oam.ContractDescriber), and the types each rule lowers into
// (oam.LoweringTargetDeclarer). The scheme is builtin.ContractVersion's: the
// family is the type name, and a type whose CapabilityRequired is true lists
// its own capability key.

// contract is the metadata of the built-in trait type typ. capabilityKeys are
// the ClusterProfile capability keys the type cannot do without.
func contract(typ string, capabilityKeys ...string) oam.ContractMetadata {
	return oam.ContractMetadata{Family: typ, Version: builtin.ContractVersion, RequiredCapabilityKeys: capabilityKeys}
}

// ContractMetadata implements oam.ContractDescriber.
func (h *IngressHandler) ContractMetadata() oam.ContractMetadata { return contract("ingress") }

// ContractMetadata implements oam.ContractDescriber.
func (h *HTTPRouteHandler) ContractMetadata() oam.ContractMetadata { return contract("httproute") }

// ContractMetadata implements oam.ContractDescriber. The "certificate"
// capability is required (CapabilityRequired).
func (h *CertificateHandler) ContractMetadata() oam.ContractMetadata {
	return contract("certificate", "certificate")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ScalerHandler) ContractMetadata() oam.ContractMetadata { return contract("scaler") }

// ContractMetadata implements oam.ContractDescriber.
func (h *PVCHandler) ContractMetadata() oam.ContractMetadata { return contract("pvc") }

// ContractMetadata implements oam.ContractDescriber. The "external-secret"
// capability is read when present and not required (CapabilityRequired).
func (h *ExternalSecretHandler) ContractMetadata() oam.ContractMetadata {
	return contract("external-secret")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ConfigMapHandler) ContractMetadata() oam.ContractMetadata { return contract("configmap") }

// ContractMetadata implements oam.ContractDescriber.
func (h *NetworkPolicyHandler) ContractMetadata() oam.ContractMetadata {
	return contract("networkpolicy")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CiliumNetworkPolicyHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cilium-networkpolicy")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *VolSyncHandler) ContractMetadata() oam.ContractMetadata { return contract("volsync") }

// ContractMetadata implements oam.ContractDescriber.
func (h *RBACHandler) ContractMetadata() oam.ContractMetadata { return contract("rbac") }

// ContractMetadata implements oam.ContractDescriber.
func (h *PruneProtectionHandler) ContractMetadata() oam.ContractMetadata {
	return contract("prune-protection")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ForceReplaceHandler) ContractMetadata() oam.ContractMetadata {
	return contract("force-replace")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *SecurityContextHandler) ContractMetadata() oam.ContractMetadata {
	return contract("security-context")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *TopologySpreadHandler) ContractMetadata() oam.ContractMetadata {
	return contract("topology-spread")
}

// ContractMetadata implements oam.ContractDescriber. The "expose" capability is
// required (CapabilityRequired): it chooses between the two targets.
func (ExposeRule) ContractMetadata() oam.ContractMetadata { return contract("expose", "expose") }

// LoweringTargets implements oam.LoweringTargetDeclarer: the trait of the
// exposure the "expose" capability selects.
func (ExposeRule) LoweringTargets() oam.LoweringTargets {
	return oam.LoweringTargets{TraitTypes: []string{"ingress", "httproute"}}
}
