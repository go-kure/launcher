package builtin

// ExternalSecretRendering defines the platform values for the external-secret capability.
// The secretStoreRef is provided by the cluster operator in the ClusterProfile.
type ExternalSecretRendering struct {
	SecretStoreRef  ExternalSecretStoreRef `yaml:"secretStoreRef" json:"secretStoreRef"`
	RefreshInterval string                 `yaml:"refreshInterval,omitempty" json:"refreshInterval,omitempty"`
}

// ExternalSecretStoreRef identifies the ExternalSecrets SecretStore or ClusterSecretStore.
type ExternalSecretStoreRef struct {
	Name string `yaml:"name" json:"name"`
	// Kind defaults to "ClusterSecretStore" if not specified.
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty"`
}
