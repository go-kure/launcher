package oam

// Application represents an OAM Application resource.
type Application struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   Metadata        `yaml:"metadata"`
	Spec       ApplicationSpec `yaml:"spec"`
}

// Metadata contains standard Kubernetes-style metadata fields.
type Metadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// ApplicationSpec defines the components and policies of an OAM application.
type ApplicationSpec struct {
	Components []Component         `yaml:"components"`
	Policies   []ApplicationPolicy `yaml:"policies,omitempty"`
}

// Component represents a single component within an OAM application.
type Component struct {
	Name        string            `yaml:"name"`
	Type        string            `yaml:"type"`
	Properties  map[string]any    `yaml:"properties"`
	Traits      []Trait           `yaml:"traits,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// Trait represents an operational behavior attached to a component.
type Trait struct {
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties"`
}

// ApplicationPolicy defines an application-level policy entry passed through to the runtime unchanged.
type ApplicationPolicy struct {
	Name       string         `yaml:"name"`
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties,omitempty"`
}
