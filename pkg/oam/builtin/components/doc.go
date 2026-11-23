// Package components implements oam.ComponentHandler for the built-in component
// types. Each handler parses a typed configuration from a component's
// properties and produces the matching Kubernetes resources, mostly through
// kure's builders; the passthrough, manifests and crd handlers emit objects the
// document supplies.
package components
