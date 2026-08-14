// Package oam provides the OAM data model, YAML parser, semantic validator, and
// lowering engine for launcher Application documents
// (apiVersion: launcher.gokure.dev/v1alpha1). The lowering engine (lowering.go,
// lowering_raw.go) rewrites higher-level authored kinds into base Application
// documents, at both an in-transform entry point and a raw-bytes entry point —
// see docs/oam/design-lowering-engine.md for the design rationale.
package oam
