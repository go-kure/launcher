# OAM Model, Parser & Transformer

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` is launcher's core: the OAM data model, YAML parser, semantic
validator, and the transform pipeline that turns an `Application` + `ClusterProfile`
into Kubernetes manifests. All documents use `apiVersion: launcher.gokure.dev/v1alpha1`.

## Document kinds

| Kind | Type | Purpose |
|------|------|---------|
| `Application` | `Application` | The app: `components[]` (each with `type` + `properties`) and `traits[]`. |
| `Package` | `Package` | A parameterized, distributable unit: `app.yaml` + a `kurel.yaml` parameter schema (`ParameterDecl`). |
| `ClusterProfile` | `ClusterProfile` | Platform choices (trait implementations, capabilities) supplied at build time. |
| `CapabilityDefinition` | `CapabilityDefinition` | Declares a capability's rendering/property schema for validation. |

## Pipeline

```
parse → resolve parameters → transform (component + trait handlers) → manifests
```

1. **Parse** an Application/Package/ClusterProfile from YAML.
2. **Resolve parameters** (`ResolveParameters`) — apply `kurel.yaml` declarations,
   values files, and `--set` overrides via `${var}` substitution.
3. **Transform** (`Transformer`) — dispatch each component to its
   `ComponentHandler` and each trait to its `TraitHandler`, merging the
   `ClusterProfile`'s capability choices.

## Parsing

| Function | Purpose |
|----------|---------|
| `Parse` / `ParseMulti` / `MustParse` | Parse one / many Application documents. |
| `ParsePackage` | Parse a `Package` (app + parameter schema). |
| `ParseClusterProfile` | Parse a `ClusterProfile`. |
| `LoadCapabilityDefinitions` | Load `CapabilityDefinition`s for capability validation. |
| `ParseWithExtraTraitTypes` | Parse allowing additional (custom) trait types. |

## Transform & extension

`NewTransformer(...)` builds a transformer from maps of component/trait handlers;
`pkg/cmd/kurel` registers the built-ins. Extend the system by implementing:

| Interface | Role |
|-----------|------|
| `ComponentHandler` | `CanHandle(type)` + `ToApplicationConfig(...)` — see [components](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components). |
| `TraitHandler` | `CanHandle(type)` + `Apply(...)` — see [traits](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits). |
| `PolicyHandler` | Enforce/validate policies (`Enforceable`, `PolicyResult`). |
| `CapabilityAware` | Mark a handler as requiring a `ClusterProfile` capability. |
| `SourceDeduplicatable` | Collapse duplicate sources (e.g. shared OCI/Helm repos). |

## Capability system

Capability-aware traits (e.g. `expose`, `certificate`, `external-secret`) declare
required platform inputs; the `ClusterProfile` provides them, and
`CapabilityDefinition` rendering/property schemas validate custom capabilities
(`--strict-capabilities` turns warnings into errors).

This is a large internal builder surface; the tables above cover the entry points.
See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the full
type reference, the design notes under the Concepts section, and `examples/` for
runnable applications.
