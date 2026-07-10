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

// ParameterDecl declares a single package parameter: an ordered, named entry over
// the shared PropertySchema vocabulary. The ordered list (PackageSpec.Parameters)
// drives default resolution — a default may reference only earlier parameters — so
// this stays a list, not a map. The embedded PropertySchema is restricted to the
// flat vocabulary (type/required/default/description); the rich fields are rejected
// at decode time by UnmarshalYAML (flatschema.go). Accepted types are gated to
// string/integer/boolean by validatePackage (array/object are not yet substitutable).
type ParameterDecl struct {
	Name           string `yaml:"name"`
	PropertySchema `yaml:",inline"`
}
