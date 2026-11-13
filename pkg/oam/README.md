# OAM Model, Parser & Transformer

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` is launcher's core: the OAM data model, YAML parser, semantic
validator, and the transform pipeline that turns an `Application` + `ClusterProfile`
into Kubernetes manifests. Every document this package parses uses
`apiVersion: launcher.gokure.dev/v1alpha1` (`SupportedAPIVersion`); the one exception is
the raw-document lowering seam, where a `RawDocumentLoweringRule` may claim a kind under a
consumer-owned group via `RawDocumentAPIVersioner` (see [Lowering](/site/content/api-reference/oam/transform-and-lowering.md#lowering)).

## Document kinds

| Kind | Type | Purpose |
|------|------|---------|
| `Application` | `Application` | The app: `components[]` (each with `type` + `properties`) and `traits[]`. |
| `Package` | `Package` | A package's `kurel.yaml` descriptor: the declarations of its build-time parameters (`ParameterDecl`). The application is the separate `app.yaml`. |
| `ClusterProfile` | `ClusterProfile` | Platform choices (trait implementations, capabilities) supplied at build time, and the cluster's Kubernetes release and per-gate feature-gate overrides (`spec.kubernetesVersion`, `spec.featureGates`; a library caller passes the overrides to `ValidateAuthoredPropertiesForTargetWithGates` and `TransformContext.FeatureGates`, see [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md)). |
| `CapabilityDefinition` | `CapabilityDefinition` | Declares the schema of a capability's rendering: the keys a `ClusterProfile` binding for it may hold. |

## Parts

The rest of this page is split into parts. Each part holds the full text of its sections.

- [Pipeline](/site/content/api-reference/oam/pipeline.md): How a document goes from YAML to manifests.
- [Component label and ownership](/site/content/api-reference/oam/labels-and-ownership.md): The label that marks what a component owns, and the metadata keys launcher reserves.
- [Names and overrides](/site/content/api-reference/oam/names-and-overrides.md): How object names, labels and annotations are chosen and overridden.
- [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md): The parser, the component type allowlist and how a type name resolves.
- [Transform, extension and lowering](/site/content/api-reference/oam/transform-and-lowering.md): The transform step, how to register builtins and the lowering rules.
- [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md): Property schemas, what an explicit null means and the contract metadata.
- [Policy defaults and capability system](/site/content/api-reference/oam/policy-and-capability.md): Policy defaults, refusal classes and how a capability renders.

The headings below stay so that a link to an old section still lands here. Each one links to the part that holds the text. Some also keep the opening lines of the section.

## Pipeline

See [Pipeline](/site/content/api-reference/oam/pipeline.md#pipeline).

## Component label and ownership

Every application of a transformed document belongs to one authored component, or to the
document as a whole (go-kure/launcher#788). The transform records that as its last step, and
two things follow from it: each application reports its owner, and each object it generates
carries the owner's component label.

Full text: [Component label and ownership](/site/content/api-reference/oam/labels-and-ownership.md#component-label-and-ownership).

## Reserved metadata keys

A consumer that keeps label and annotation keys to itself names them in
`TransformContext.ReservedMetadataKeys` (go-kure/launcher#790). One rule holds every
application the transform returns to the list, whatever wrote the key: a kind component, a
`passthrough` or `manifests` object, a chart rendered at build time, a trait's `annotations`,
a `postgresql` or `cnpg-cluster` component's `inheritedMetadata`.

Full text: [Component label and ownership](/site/content/api-reference/oam/labels-and-ownership.md#reserved-metadata-keys).

## Names and overrides

See [Names and overrides](/site/content/api-reference/oam/names-and-overrides.md#names-and-overrides).

### Name roles and the `Naming` hook

See [Names and overrides](/site/content/api-reference/oam/names-and-overrides.md#name-roles-and-the-naming-hook).

### Outputs: the names a component's objects get

A consumer that wants to refer to an object a component generates (a Secret an operator
reads, a Service a route points at) needs the object's name before the document is built.
A component type answers with the optional `OutputProvider` interface, which the
`ComponentLoweringRule` claiming the type implements, or the handler when no rule claims it,
as for `EndpointProvider`. `Transformer.ComponentOutputs(comp)` returns one `Output` per
declaration, in the provider's order: a `Key`, the object's `Kind` (a `schema.GroupKind`) and
its `Name`. A key is one identifier (a letter or underscore, then letters, digits and
underscores), so a document can address it as `parts.<part>.<key>`. A type that provides no
outputs answers `(nil, nil)`.

Full text: [Names and overrides](/site/content/api-reference/oam/names-and-overrides.md#outputs-the-names-a-components-objects-get).

### `objectName`: the object of a kind component

See [Names and overrides](/site/content/api-reference/oam/names-and-overrides.md#objectname-the-object-of-a-kind-component).

### `labels` and `annotations`: the metadata of a kind component's object

See [Names and overrides](/site/content/api-reference/oam/names-and-overrides.md#labels-and-annotations-the-metadata-of-a-kind-components-object).

## Parsing

See [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md#parsing).

### Component type allowlist

See [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md#component-type-allowlist).

## Type identity

See [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md#type-identity).

### Grammar

See [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md#grammar).

### Resolution

See [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md#resolution).

### Declaring and resolving

See [Parsing and type identity](/site/content/api-reference/oam/parsing-and-types.md#declaring-and-resolving).

## Transform & extension

See [Transform, extension and lowering](/site/content/api-reference/oam/transform-and-lowering.md#transform--extension).

### Registering the builtins

See [Transform, extension and lowering](/site/content/api-reference/oam/transform-and-lowering.md#registering-the-builtins).

## Lowering

Some documents, components, traits, and policies are authored in a higher-level
vocabulary that has no direct dispatchable handler — a type a platform wants expanded
into one or more terminal types before the transform's own component/trait dispatch
runs. Where a component lowering rule places each object it emits (a same-name
sibling member, a trait on one member, or a separately named kind component), and
how a trait builds the same object as its kind twin, is set out in
`docs/oam/design-lowering-engine.md`, section "Where a lowering rule places each
object".

After each lowering round, the engine resolves the types that a rule emitted. A type
written as a declared identity becomes the registered name. A plain name that a handler or
a rule is registered under stays as written, so a rule that emits plain names changes
nothing. Where emission validation runs and the registered name declares a schema, the
properties of an emitted component, trait or policy are checked against that schema, also
when the rule wrote the type as an identity. The same holds for the platform-reserved check
and for the members of a same-name sibling group. A rule reads the Naming hook of the run
through `LoweringContext.NamingHook`.

A consumer sees what a configuration-defined model emits through `TransformContext.EmitHook`.
The model rule calls it through `LoweringContext.EmitModelComponent`, once for each component it
emits, in part order and only after the whole render succeeded. The call carries a `ModelEmit`: the
model key, the part id, the type as the model wrote it, the name the build gave and a copy of the
properties. A hook error fails the transform and names the model and the part. A Go lowering rule
and an authored component never reach the hook, and a nil hook asks nothing. The model rule keeps the
limits of the whole build (parts, cost, emitted properties bytes and time) in `LoweringContext.RunState`,
so that they belong to one transform.
`Transformer.CheckComponentTypes` is a dry run of `DeclareComponentTypes`. It checks that
the declaration is valid and declares nothing. It does not need the handler or rule to be
registered yet, and `DeclareComponentTypes` does. A component model registers its rule
only after this check passes (see [Component Models](/pkg/oam/model/README.md)).

Full text: [Transform, extension and lowering](/site/content/api-reference/oam/transform-and-lowering.md#lowering).

## Property schemas

Handlers may implement `PropertySchemaProvider` (`PropertySchema() map[string]PropertySchema`)
to declare a constrained schema for their user-facing properties. `PropertySchema` is launcher's
single schema vocabulary — the same type also backs `kurel.yaml` parameters (`ParameterDecl`) and
`CapabilityDefinition` rendering properties. It has `Type` (string/integer/boolean/number/array/object),
`Types`, `Description`, `Required`, `Default`, `Enum`, nested `Properties`, `Items`, `AdditionalProperties`
(default false; escape-hatch fields set it true), and `Exclusive`. The rich fields (`Types`, `Enum`, `Properties`, `Items`,
`AdditionalProperties`, `Exclusive`) are meaningful only for handler properties: the two flat call sites (kurel
parameters, capability rendering) reject them at decode time, so unifying the type does not widen
their accepted behavior.

Full text: [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md#property-schemas).

### What an explicit `null` means on the emitted path

See [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md#what-an-explicit-null-means-on-the-emitted-path).

## Contract metadata

See [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md#contract-metadata).

### Lowering targets and `Seal`

See [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md#lowering-targets-and-seal).

### The "no handler" error

See [Property schemas and contract metadata](/site/content/api-reference/oam/schemas-and-contract.md#the-no-handler-error).

## Policy defaults & enforcement

See [Policy defaults and capability system](/site/content/api-reference/oam/policy-and-capability.md#policy-defaults--enforcement).

### Refusal classes

See [Policy defaults and capability system](/site/content/api-reference/oam/policy-and-capability.md#refusal-classes).

## Capability system

See [Policy defaults and capability system](/site/content/api-reference/oam/policy-and-capability.md#capability-system).

The `AllowedCapabilities`, `ForbiddenCapabilities` and `RequiredCapabilities` lists of a `Policy`
judge the trait types an author wrote. They also judge the traits that a component rule writes onto
the parts it emits. Launcher's own component rules are exempt, because their traits are launcher's
opinions. A library consumer registers those rules with `RegisterBuiltinComponentLowering`. Any other
rule goes through `RegisterComponentLowering`, and its emitted traits count.

### Capability rendering: types and nulls

See [Policy defaults and capability system](/site/content/api-reference/oam/policy-and-capability.md#capability-rendering-types-and-nulls).

### Rendering merge: authored values and nulls

See [Policy defaults and capability system](/site/content/api-reference/oam/policy-and-capability.md#rendering-merge-authored-values-and-nulls).
