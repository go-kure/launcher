package model

import (
	"github.com/go-kure/launcher/pkg/oam"
)

// Kind is the document kind of a configuration-defined component model.
const Kind = "Model"

// CELEnvV1 is the only CEL environment a model may pin in release one. The value
// names a fixed function set: the CEL standard library plus the strings (version 5),
// lists (version 3) and bindings (version 1) extensions of cel-go. A golden file pins
// the function list, so a model written today means the same later.
const CELEnvV1 = "1"

// Model is one configuration-defined component model: a YAML document that
// declares inputs, computes values with CEL, and lists the parts (components and
// traits) it expands to. A decoded Model is plain data. Loading checks its shape
// and never evaluates an expression.
type Model struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

// Metadata names the model. Name is `<name>.<domain>` and Version is
// `v<major>[.<minor>[.<patch>]]`, both under the type identity grammar of
// pkg/oam (oam.ValidateTypeIdentity).
type Metadata struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Spec is the body of a model.
type Spec struct {
	// CELEnv pins the CEL environment. It must be CELEnvV1.
	CELEnv string `yaml:"celEnv"`
	// Inputs maps an input name to the schema of the component property it
	// declares. The rule publishes it as its own property schema.
	Inputs map[string]oam.PropertySchema `yaml:"inputs,omitempty"`
	// Values are computed in order. A value reads inputs and earlier values only.
	Values []Value `yaml:"values,omitempty"`
	// Parts are the components and traits the model expands to.
	Parts []Part `yaml:"parts"`
}

// Value is one named, computed value.
type Value struct {
	Name string `yaml:"name"`
	// Expr is a `${ }` expression text. Loading does not compile it.
	Expr string `yaml:"expr"`
}

// Part is one emitted component. A part may repeat (ForEach), may be skipped
// (When), and carries the traits and annotations of the component it emits.
type Part struct {
	// ID names the part inside the model. It is a CEL identifier, because other
	// parts read `parts.<id>`.
	ID string `yaml:"id"`
	// Name is an optional expression for the component name.
	Name string `yaml:"name,omitempty"`
	// Type is the full identity of the emitted component type: `name@version`.
	Type string `yaml:"type"`
	// When is an optional expression. The part is emitted only when it is true.
	When string `yaml:"when,omitempty"`
	// ForEach repeats the part once per element of a list.
	ForEach *ForEach `yaml:"forEach,omitempty"`
	// Properties are the component properties. Strings may hold `${ }` expressions.
	Properties map[string]any `yaml:"properties,omitempty"`
	Traits     []PartTrait    `yaml:"traits,omitempty"`
	// Annotations are the component annotations.
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// ForEach repeats a part. In is an expression that yields a list. As names the
// loop variable inside the part.
type ForEach struct {
	As string `yaml:"as"`
	In string `yaml:"in"`
}

// PartTrait is one trait on an emitted component. Type is the full identity of
// the trait type: `name@version`.
type PartTrait struct {
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties,omitempty"`
}

// Identity returns the identity of the model as written in its metadata.
func (m *Model) Identity() oam.TypeID {
	return oam.TypeID{Name: m.Metadata.Name, Version: m.Metadata.Version}
}

// Key returns the registry key of the model: its identity with the version in the
// canonical form, so `v2`, `v2.0` and `v2.0.0` are one model. The type registry
// returns a version as it was registered (go-kure/launcher#1289), so any
// comparison or storage goes through this form.
func (m *Model) Key() string {
	return m.Identity().Canonical().String()
}
