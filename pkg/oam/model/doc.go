// Package model defines the document format of a configuration-defined component
// model (go-kure/launcher#1261): a YAML document that declares inputs, computes
// values with CEL, and lists the parts (components and traits) a component of the
// model type expands to.
//
// Load decodes one document and checks its shape. It never compiles or evaluates an
// expression. A model is identified by its metadata name and version under the type
// identity grammar of pkg/oam, and Model.Key gives the canonical registry key.
package model
