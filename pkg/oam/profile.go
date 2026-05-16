package oam

// ClusterProfile encodes per-cluster capability bindings for the launcher runtime.
// It is a YAML document with apiVersion launcher.gokure.dev/v1alpha1, kind ClusterProfile.
//
// Fields present in crane's ClusterProfile that are intentionally absent here:
// spec.gitops (FluxCD delivery concern), spec.catalog, spec.componentCatalog,
// spec.componentVariants (Wharf-specific). See docs/oam/design-cluster-profile.md §2.
type ClusterProfile struct {
	APIVersion string                 `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                 `yaml:"kind"       json:"kind"`
	Metadata   ClusterProfileMetadata `yaml:"metadata"   json:"metadata"`
	Spec       ClusterProfileSpec     `yaml:"spec"       json:"spec"`
}

// ClusterProfileMetadata holds the identifying metadata for a ClusterProfile.
// Only name is defined; namespace, labels, and annotations are not part of the schema.
type ClusterProfileMetadata struct {
	Name string `yaml:"name" json:"name"`
}

// ClusterProfileSpec holds the capability bindings for a cluster.
type ClusterProfileSpec struct {
	Capabilities map[string]CapabilityBinding `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// CapabilityBinding holds the rendering values the platform injects into trait
// properties at build time. Renamed from crane's CapabilityDefinition; the
// parameters field is removed — capability schema is not cluster-operator input.
// See docs/oam/design-cluster-profile.md §2 and docs/oam/design-capability-schema.md §3.6.
type CapabilityBinding struct {
	Rendering map[string]any `yaml:"rendering,omitempty" json:"rendering,omitempty"`
}
