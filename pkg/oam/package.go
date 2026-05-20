package oam

// PackageMetadata holds Package identity fields. It is intentionally separate from
// Metadata — it includes version and description which are not valid Kubernetes
// metadata fields and would be rejected by strict YAML decode on Application documents.
type PackageMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Package represents a kurel.yaml package descriptor.
type Package struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   PackageMetadata `yaml:"metadata"`
	Spec       PackageSpec     `yaml:"spec"`
}

// PackageSpec holds the parameter declarations for the package.
type PackageSpec struct {
	Parameters []ParameterDecl `yaml:"parameters,omitempty"`
}

// ParameterDecl declares a single package parameter with its type, default, and constraints.
type ParameterDecl struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"` // string, integer, boolean, array, object
	Required    bool   `yaml:"required,omitempty"`
	Default     any    `yaml:"default,omitempty"`
	Description string `yaml:"description,omitempty"`
}
