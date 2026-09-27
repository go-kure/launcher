package builtin

// TopologySpreadRendering holds no rendering keys; ValidateAndApplyDefaults
// rejects any key a ClusterProfile capability supplies
// (design-capability-schema.md §2.4). That includes the engine-owned `scope`:
// a rendering is merged after the engine chose the capability binding, so a
// rendered `scope` could select nothing.
type TopologySpreadRendering struct{}
