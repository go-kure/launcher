package builtin

// NetworkPolicyRendering holds no rendering keys; ValidateAndApplyDefaults rejects
// any key the operator accidentally provides (design-capability-schema.md §2.4).
type NetworkPolicyRendering struct{}
