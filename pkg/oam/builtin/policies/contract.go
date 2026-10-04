package policies

import (
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// This file holds the contract metadata of every built-in policy handler
// (oam.ContractDescriber). The scheme is builtin.ContractVersion's: the family
// is the type name. No policy requires a ClusterProfile capability.

// contract is the metadata of the built-in policy type typ.
func contract(typ string) oam.ContractMetadata {
	return oam.ContractMetadata{Family: typ, Version: builtin.ContractVersion}
}

// ContractMetadata implements oam.ContractDescriber.
func (h *DependencyHandler) ContractMetadata() oam.ContractMetadata { return contract("dependency") }

// ContractMetadata implements oam.ContractDescriber.
func (h *PlacementHandler) ContractMetadata() oam.ContractMetadata { return contract("placement") }
