package builtin

// VolSyncRendering carries platform-supplied class defaults for volsync backups.
// Both are overridable by inline OAM trait properties; injection is copyMethod-aware
// (see traits/volsync.go Generate). ValidateAndApplyDefaults strict-decodes into this
// struct, so any other rendering key the operator accidentally provides is rejected.
type VolSyncRendering struct {
	StorageClassName        string `yaml:"storageClassName,omitempty" json:"storageClassName,omitempty"`
	VolumeSnapshotClassName string `yaml:"volumeSnapshotClassName,omitempty" json:"volumeSnapshotClassName,omitempty"`
}
