# OAM Model & Parser

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` provides the OAM data model, YAML parser, and semantic validator for
launcher Application documents (`apiVersion: launcher.gokure.dev/v1alpha1`), plus the
transform pipeline that turns an `Application` + `ClusterProfile` into Kubernetes
manifests.

Core surface: the data model (`Application`, `Component`, `Trait`,
`ApplicationPolicy`, `Package`, `ClusterProfile`, `CapabilityDefinition`), the
parsers (`Parse`, `ParseMulti`, `ParsePackage`, `ParseClusterProfile`,
`LoadCapabilityDefinitions`), the transformer (`NewTransformer`,
`ResolveParameters`), and the extension interfaces (`ComponentHandler`,
`TraitHandler`, `PolicyHandler`, `CapabilityAware`).

This is launcher's largest package and an **internal builder surface** consumed by
the `kurel` CLI rather than a stable external SDK. A full per-symbol API reference is
intentionally deferred; see the design docs under `docs/oam/` and
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the complete
exported API. Built-in handler implementations live in
[`builtin/components`](builtin/components) and [`builtin/traits`](builtin/traits).
