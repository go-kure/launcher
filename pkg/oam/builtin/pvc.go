package builtin

// PVCRendering carries the platform-supplied StorageClass default for pvc traits.
// The class is optional and overridable by an inline OAM trait property, so pvc does
// not implement CapabilityRequired. ValidateAndApplyDefaults strict-decodes into this
// struct, so any other rendering key the operator accidentally provides is rejected.
type PVCRendering struct {
	StorageClassName string `yaml:"storageClassName,omitempty" json:"storageClassName,omitempty"`
}
