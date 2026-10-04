package builtin

// ContractVersion is the contract version every built-in handler and lowering
// rule declares (oam.ContractMetadata.Version). The scheme is one family per
// type, named after the type, and one version for all of them: the built-ins
// are released together, so a family has no version of its own yet.
//
// A lowering rule's version is part of its identity: Origin.Rule and
// LoweringStep.Rule read "component/webservice@v1alpha1".
const ContractVersion = "v1alpha1"
