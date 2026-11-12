# OAM Model, Parser & Transformer

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` is launcher's core: the OAM data model, YAML parser, semantic
validator, and the transform pipeline that turns an `Application` + `ClusterProfile`
into Kubernetes manifests. Every document this package parses uses
`apiVersion: launcher.gokure.dev/v1alpha1` (`SupportedAPIVersion`); the one exception is
the raw-document lowering seam, where a `RawDocumentLoweringRule` may claim a kind under a
consumer-owned group via `RawDocumentAPIVersioner` (see "Lowering entry points" below).

## Document kinds

| Kind | Type | Purpose |
|------|------|---------|
| `Application` | `Application` | The app: `components[]` (each with `type` + `properties`) and `traits[]`. |
| `Package` | `Package` | A package's `kurel.yaml` descriptor: the declarations of its build-time parameters (`ParameterDecl`). The application is the separate `app.yaml`. |
| `ClusterProfile` | `ClusterProfile` | Platform choices (trait implementations, capabilities) supplied at build time. |
| `CapabilityDefinition` | `CapabilityDefinition` | Declares the schema of a capability's rendering: the keys a `ClusterProfile` binding for it may hold. |

## Parts

The rest of this page is split into parts. Each part holds the full text of its sections.

- [Pipeline](https://www.gokure.dev/launcher/api-reference/oam/pipeline/): How a document goes from YAML to manifests.
- [Component label and ownership](https://www.gokure.dev/launcher/api-reference/oam/labels-and-ownership/): The label that marks what a component owns, and the metadata keys launcher reserves.
- [Names and overrides](https://www.gokure.dev/launcher/api-reference/oam/names-and-overrides/): How object names, labels and annotations are chosen and overridden.
- [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/): The parser, the component type allowlist and how a type name resolves.
- [Transform, extension and lowering](https://www.gokure.dev/launcher/api-reference/oam/transform-and-lowering/): The transform step, how to register builtins and the lowering rules.
- [Property schemas and contract metadata](https://www.gokure.dev/launcher/api-reference/oam/schemas-and-contract/): Property schemas, what an explicit null means and the contract metadata.
- [Policy defaults and capability system](https://www.gokure.dev/launcher/api-reference/oam/policy-and-capability/): Policy defaults, refusal classes and how a capability renders.

The headings below stay so that a link to an old section still lands here. Each one links to the part that holds the text.

## Pipeline

See [Pipeline](https://www.gokure.dev/launcher/api-reference/oam/pipeline/#pipeline).

## Component label and ownership

See [Component label and ownership](https://www.gokure.dev/launcher/api-reference/oam/labels-and-ownership/#component-label-and-ownership).

## Reserved metadata keys

See [Component label and ownership](https://www.gokure.dev/launcher/api-reference/oam/labels-and-ownership/#reserved-metadata-keys).

## Names and overrides

See [Names and overrides](https://www.gokure.dev/launcher/api-reference/oam/names-and-overrides/#names-and-overrides).

### Name roles and the `Naming` hook

See [Names and overrides](https://www.gokure.dev/launcher/api-reference/oam/names-and-overrides/#name-roles-and-the-naming-hook).

### Outputs: the names a component's objects get

See [Names and overrides](https://www.gokure.dev/launcher/api-reference/oam/names-and-overrides/#outputs-the-names-a-components-objects-get).

### `objectName`: the object of a kind component

See [Names and overrides](https://www.gokure.dev/launcher/api-reference/oam/names-and-overrides/#objectname-the-object-of-a-kind-component).

### `labels` and `annotations`: the metadata of a kind component's object

See [Names and overrides](https://www.gokure.dev/launcher/api-reference/oam/names-and-overrides/#labels-and-annotations-the-metadata-of-a-kind-components-object).

## Parsing

See [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/#parsing).

### Component type allowlist

See [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/#component-type-allowlist).

## Type identity

See [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/#type-identity).

### Grammar

See [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/#grammar).

### Resolution

See [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/#resolution).

### Declaring and resolving

See [Parsing and type identity](https://www.gokure.dev/launcher/api-reference/oam/parsing-and-types/#declaring-and-resolving).

## Transform & extension

See [Transform, extension and lowering](https://www.gokure.dev/launcher/api-reference/oam/transform-and-lowering/#transform--extension).

### Registering the builtins

See [Transform, extension and lowering](https://www.gokure.dev/launcher/api-reference/oam/transform-and-lowering/#registering-the-builtins).

## Lowering

See [Transform, extension and lowering](https://www.gokure.dev/launcher/api-reference/oam/transform-and-lowering/#lowering).

## Property schemas

See [Property schemas and contract metadata](https://www.gokure.dev/launcher/api-reference/oam/schemas-and-contract/#property-schemas).

### What an explicit `null` means on the emitted path

See [Property schemas and contract metadata](https://www.gokure.dev/launcher/api-reference/oam/schemas-and-contract/#what-an-explicit-null-means-on-the-emitted-path).

## Contract metadata

See [Property schemas and contract metadata](https://www.gokure.dev/launcher/api-reference/oam/schemas-and-contract/#contract-metadata).

### Lowering targets and `Seal`

See [Property schemas and contract metadata](https://www.gokure.dev/launcher/api-reference/oam/schemas-and-contract/#lowering-targets-and-seal).

### The "no handler" error

See [Property schemas and contract metadata](https://www.gokure.dev/launcher/api-reference/oam/schemas-and-contract/#the-no-handler-error).

## Policy defaults & enforcement

See [Policy defaults and capability system](https://www.gokure.dev/launcher/api-reference/oam/policy-and-capability/#policy-defaults--enforcement).

### Refusal classes

See [Policy defaults and capability system](https://www.gokure.dev/launcher/api-reference/oam/policy-and-capability/#refusal-classes).

## Capability system

See [Policy defaults and capability system](https://www.gokure.dev/launcher/api-reference/oam/policy-and-capability/#capability-system).

### Capability rendering: types and nulls

See [Policy defaults and capability system](https://www.gokure.dev/launcher/api-reference/oam/policy-and-capability/#capability-rendering-types-and-nulls).

### Rendering merge: authored values and nulls

See [Policy defaults and capability system](https://www.gokure.dev/launcher/api-reference/oam/policy-and-capability/#rendering-merge-authored-values-and-nulls).
