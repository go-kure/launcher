---
title: "Transform, extension and lowering"
weight: 5
---

The implementation, history and tests of the sections below are on the
[contributor page](../../contributing/oam-reference/transform-and-lowering.md).

## Transform & extension

`NewTransformer(...)` builds a transformer from maps of component/trait handlers, and
`RegisterPolicy(type, handler)` adds an application policy handler; the built-ins come from
`pkg/oam/builtin/registry` (see [Registering the builtins](#registering-the-builtins)).
Extend the system by implementing:

| Interface | Role |
|-----------|------|
| `ComponentHandler` | `CanHandle(type)` + `ToApplicationConfig(...)` — see [components](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components). |
| `TraitHandler` | `CanHandle(type)` + `Apply(...)` — see [traits](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits). `Apply` mutates the application it is given and may append sub-applications to the bundle. It must not replace, remove or rename a component's application there, its own or any other component's of the bundle, including one whose traits already ran: the transform fails, naming the trait and the component, because NetworkPolicy synthesis finds a component's application by its name and then its pointer, and a replaced, removed or renamed one would silently get no policy. Nor may it rename a sibling group member's application: the member would generate its objects under a name the group does not carry, so the transform fails, naming the trait, the group and the member's type. |
| `PolicyHandler` | `CanHandle(type)` + `Apply(policy, components, result)` — validates one `spec.policies` entry and records its effect (tier overrides, dependency edges), or its own data under `PolicyResult.Extensions`, on the shared `PolicyResult`; see [policies](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies). A policy type with no registered handler fails the transform. The transform returns an error from `Apply` as `policy "<name>": <error>`, so a handler says what is wrong and does not name the policy itself. |
| `CapabilityAware` | Mark a handler as requiring a `ClusterProfile` capability. |
| `ComponentCapabilityDefaults` | `CapabilityDefaults() (key string, properties []string)` — on a `ComponentHandler` whose properties take defaults from a `ClusterProfile` capability. Before `ToApplicationConfig`, the engine fills each listed property the component leaves unauthored (absent or `null`) from that binding's rendering; an authored value, `""` included, wins. It reads only the listed keys, never the rest of the rendering, and records the key in `ConsumedCapabilities` when the profile binds it. A component a lowering rule synthesized is skipped, as a sealed trait is. Each filled value is validated against the component's own `PropertySchema`, which must declare every listed key: the trait side's schema may differ. A handler that declares no schema relies on `EvaluateProfile`, which validates the binding only through the trait handler or trait lowering rule of the key's type. Its fill is refused unless that handler or rule validates the rendering: it implements `ValidateAndApplyDefaults`, or the type is not built in and has a `CapabilityDefinition`, whose schema `EvaluateProfile` applies. Implemented by `persistentvolumeclaim` (`pvc`, `storageClassName`). |
| `ComponentCapabilityFiller` | `FillCapabilityDefaults(props map[string]any, lctx LoweringContext) (map[string]any, error)` — on a `ComponentHandler` whose capability defaults land below the top level of its properties, where `ComponentCapabilityDefaults` cannot reach. The engine calls it right after `ComponentCapabilityDefaults`, on a component no lowering rule synthesized, and passes the result to `ToApplicationConfig`; an error fails the component. On a kind component `props` no longer holds `objectName`, which the engine took out. It must not mutate `props`, and reads a binding only through `lctx.Capability`, which records the key in `ConsumedCapabilities`; `lctx` carries nothing else. The handler validates the values it fills; the engine's schema check covers only `ComponentCapabilityDefaults` keys. Implemented by `statefulset` (`pvc`'s `storageClassName` into each `volumeClaimTemplates` entry that leaves `storageClass` unauthored). |
| `PropertySchemaProvider` | Declare a `PropertySchema` for the handler's user-facing properties (see [Property schemas](schemas-and-contract.md#property-schemas)). |
| `ContractDescriber` | Declare `ContractMetadata` — contract family, version, required capability keys, prerequisites, readiness, deprecation info (see [Contract metadata](schemas-and-contract.md#contract-metadata)). Every built-in handler and lowering rule implements it. |
| `LoweringTargetDeclarer` | `LoweringTargets() LoweringTargets` — on a lowering rule of any kind: the component, trait and policy types it lowers into. `Transformer.Seal` refuses a registry in which one of them is not registered (see [Contract metadata](schemas-and-contract.md#contract-metadata)). Every built-in lowering rule implements it. |
| `ComponentNamed` | Expose the owning OAM component (`ComponentName() string`) on a trait/component sub-app config, so consumers can attribute each emitted resource to its component without re-deriving it from sub-app names. The value is the raw component name; a consumer writing it into a label or selector passes it through `ComponentLabelValue` first. |
| `ApplicationNameSetter` | `SetApplicationName(name string)` — on a component config that builds a name out of the OAM application it belongs to, so the name differs when two differently named applications each have a component of the same name (the application's namespace is not part of it). The transform calls it once, right after `ToApplicationConfig` and before policy and traits, with the name of the document it transforms (the name the application's bundle carries). A config built directly, outside a transform, is never told one. Implemented by `helmtemplate`, whose hook-group child layouts are named `<application>-<component>-NN-<phase-slug>`. |
| `HookGroupNamePrefixSetter` | `AuthoredHookGroupNamePrefix() (prefix string, authored bool)` and `SetHookGroupNamePrefix(prefix string)` — on a component config whose `AugmentLayout` partitions it into hook-group layouts. The transform resolves the `hook-group` name role for it once, right after `ApplicationNameSetter`: the prefix the config reports as authored (the `hookGroupNamePrefix` property, `HookGroupNamePrefixProperty`), else the `Naming` hook's answer, else the default `<application>-<component>`, and it calls `SetHookGroupNamePrefix` only with an authored or hook-given prefix, so a config left alone keeps its default names. A config built directly, outside a transform, sets its own. Implemented by `helmtemplate` (`HelmTemplateConfig.HookGroupNamePrefix`). |
| `LayoutKustomizationNameSetter` | `AuthoredLayoutKustomizationName() (name string, authored bool)` and `SetLayoutKustomizationName(name string)` — on a component config kure's walker gives a layout of its own, which kure's Flux integration under per-layout placement gives a Kustomization. The transform resolves the `layout` name role for it once, where it builds the bundle holding the component, before any trait: the name the config reports as authored (the `layoutKustomizationName` property, `LayoutKustomizationNameProperty`), else the `Naming` hook's answer, else the default `<bundle>-<component>`. It calls `SetLayoutKustomizationName` with an authored or hook-given name only, never with the default, which is kure's own. A config built directly, outside a transform, sets its own. Implemented by `helmtemplate` (`HelmTemplateConfig.LayoutKustomizationName`, which `AugmentLayout` sets on the layout unless it carries one). |
| `SubApplicationDecorator` | `DecoratesSubApplications() bool` — on a `TraitHandler` whose `Apply` decorates an application's objects. When it returns `true`, the engine also calls `Apply` on every sub-application the component's traits appended to the bundle, as the last step of the transform, so trait order does not matter; a trait forwarded to several sibling-group members decorates the group's sub-applications once. `Apply` must not add, remove, replace, rename or reorder the bundle's applications there (the transform fails), nor rename a sibling group member's application, which the bundle does not hold: the transform fails, naming the trait, the sub-application it was decorating, the group and the member's type. Implemented by `prune-protection` and `force-replace`, whose `Apply` sets the application's delivery intent and wraps nothing. |
| `ServiceAccountNamer` | `ServiceAccountName() (name string, runsPods bool)` — the ServiceAccount a workload component's pods run as: the authored `serviceAccountName`, or `""` when none is authored (no pod kind generates an account; a `webservice`/`worker` hands its `deployment` member the name of the account it generates). `runsPods` reports whether the config runs pods at all; a trait decorator or sibling group that wraps no pod-running config reports `false`. Traits that bind identity to the workload (the `rbac` trait's binding subject) read this instead of assuming the component name, and `rbac` refuses a pod-running component with no name. Implemented by every built-in pod kind config. **Breaking library change**: the method gained the `runsPods` result. |
| `LayoutAugmentationCoverage` | `GenerateCoversAugmentLayout() bool` — for a config that also implements kure's `layout.LayoutAugmenter`, declare whether `Generate` alone already produces every resource `AugmentLayout` places into the layout. `kurel build` (which never walks a `layout.ManifestLayout`) uses this to fail closed: an augmenter that doesn't implement this interface, or that implements it and returns `false`, is rejected outright rather than silently dropping layout-level resources from the output. |
| `ConfigWrapper` | `WrappedApplicationConfig() stack.ApplicationConfig` — a config that wraps another one and says so. The ownership wrapper is one; `UnwrapConfig(cfg)` returns the config under every such wrapper. After `Transform` every application's config answers `ComponentNamed` through the ownership wrapper: the authored component, or `""` for an application the document as a whole owns ([Component label and ownership](labels-and-ownership.md#component-label-and-ownership)). |

`PolicyResult.Extensions` is a `map[string]any` a consumer's policy handler writes its own
result into, under a key it owns (a domain-qualified name, as a label key is). Launcher
neither reads nor changes it: `TransformWithPolicy` returns it as the handlers left it, and
no built-in handler writes one. A consumer that delivers through Flux
uses it to carry what its own `reconciliation` or `health-checks` handler read. Launcher has
no handler for those two policy types, nor for the `fluxcd-patches` and `fluxcd-postbuild`
traits: a document using one fails the transform with `no handler for policy type
"reconciliation" (policy "<name>"): it configures delivery, which launcher leaves to the
consumer that delivers the application; a consumer that delivers through Flux registers its
own handler` (`no handler for trait type "fluxcd-patches" (on component "<name>"): …` for
the two traits).

`PolicyResult.ConsumedCapabilities` is the sorted, deduped set of capability keys this
app's traits actually resolved against `ctx.Capabilities` during the transform — a real
`ClusterProfile` match, not every syntactically possible key a trait could name — plus
every bound key a `ComponentCapabilityDefaults` handler takes defaults from,
and every key a component, trait, document or policy lowering rule reads through
`LoweringContext.Capability`, as a `ComponentCapabilityFiller`
handler does. A read by a
`RawDocumentLoweringRule` under `LowerRaws` is not among them: `LowerRaws` returns no
`PolicyResult`, and the capabilities the rewritten document's traits resolve against are
recorded when `TransformWithPolicy` runs on it. Populated
only by `TransformWithPolicy` (nil on the plain `Transform` path, which discards
`PolicyResult` entirely); a downstream consumer that needs this signal must call
`TransformWithPolicy`. It replaces a downstream consumer's own interim, purely syntactic
candidate-key derivation with the authoritative one launcher's own resolution already
computes internally.

A lowering rule reads a capability only through `lctx.Capability(key)`, which returns
the binding and whether the profile has one, and records a key it finds. The
`LoweringContext.Capabilities` map field is gone. A downstream rule author migrates
`lctx.Capabilities[k]` to `binding, ok := lctx.Capability(k)`. A test driver that calls
a rule directly, and used to set the field, uses
`lctx.WithCapabilities(m)` instead; its reads are recorded nowhere.

**Property provenance.** `Transformer.TransformWithProvenance(app, ctx)` returns the same
`*stack.Cluster` and `*PolicyResult` as `TransformWithPolicy`, plus a `*Provenance`: for
each component and trait, the source of every leaf property value. A `PropertySource`
names the component, the `Member` (the component type of a member of a same-name sibling
group, empty otherwise), the trait type and its index (empty and 0 for the component's own
properties), the leaf `Path` and a `Source`. A `Source` has a `Kind`:

| Kind | Meaning |
|------|---------|
| `authored` | The value is in the properties the caller passed. |
| `capability` | A `ClusterProfile` binding supplied it. `Ref` is the binding's key: the scoped key or the bare type key that matched, `pvc` for a top-level fill, or, for a value a `ComponentCapabilityFiller` wrote below the top level, the capability the filler read (the sorted keys joined by `,` when it read several). |
| `copied` | A lowering rule emitted the element (a component whose origin names a rule, whether or not it counts as synthesized, or a sealed trait), and the value is one the rule's input held at the same path. `Ref` is the rule's identity, such as `component/<type>`. `From` is the kind the value had in that input: `authored`, `capability` (its `Detail` is kept) or `rendered` (an earlier rule's). A rule that hands a value on unchanged reports where it came from, not that the rule touched it. A collection the rule hands back in another Go type (a `[]any` as a `[]string`) is copied when every item had the same source, and rendered when they differ. An integer of a kind that emission validation turns into an `int` (`int8`, `int16`, every unsigned kind, a named integer type) is compared by value, not by Go type: an `int16(3)` the rule copied into a target whose schema declares an integer is still copied after that change, and a rule that changes the number reports rendered. A named string, boolean or float type, and a named unsigned integer above `math.MaxInt`, is compared as the predeclared type of its kind, which a string, boolean or number schema writes for it. A named `int32` or `int64` is the same value as the `int` an integer schema gives it and as the `int32` or `int64` a number schema keeps, in either width and at any depth in a list or object: a rule that hands a named `int32` on as an `int64`, or the reverse, reports copied under either schema. A plain `int`, `int32` or `int64` is left as it is, and so is an unnamed unsigned integer above `math.MaxInt`, which an integer schema refuses. A rule that changes the number, or its kind (an integer to a float), still reports rendered, and so does a change of width between plain integers, which keep their width. Provenance assumes a 64-bit `int`: on a 32-bit build a named `int64` outside the range of `int` is not folded and reports rendered. Launcher builds for amd64 and arm64 only. |
| `builtin` | launcher's own code filled the value after the policy decided, because nothing else held one (not a `Policy` accessor). `Ref` is the fill, such as `postgresql.enablePDB`; `From` and `Detail` are empty. |
| `rendered` | The same elements, and the value is any other: one the rule added, changed or moved to another path. `Ref` is the rule's identity. This is what `lowered` reported before, for the part of the output the rule made. A capability fill on a rule-emitted component no rule synthesized stays `capability`. |
| `policy` | The `Policy` supplied the value, as a default the author left out. `Ref` is the `Policy` accessor that supplied it, such as `DefaultCPURequest`; `From` is empty. `Detail` is `capped-to-limit` for a default request the engine lowered to the authored limit (the `DefaultCPURequest` and `DefaultMemoryRequest` accessors), empty otherwise. |

`lowered` is gone: a consumer that read `Source.Kind == "lowered"` reads `copied` or `rendered` now. A rule's input is what the engine handed it, as the next paragraph describes. The input is kept only while a report is wanted; `Transform` and `TransformWithPolicy` keep and compute nothing for it.

A component rule is handed the component's properties as they stand: a capability fill of a component happens after lowering, so a missing property is not populated from the profile yet. An unsealed trait rule is handed the trait's properties merged over its capability rendering, in this order of precedence: an authored value wins over the profile, which wins over a schema default, which wins over a handler default (the last three are the capability rendering). A sealed trait (one an earlier rule emitted) is handed its properties as they stand: no capability rendering is merged at this step, and its properties are final. A lowering rule then returns its own properties, and what it returns replaces that input: it can keep a value (`copied`), change or move one, or add one the input lacks (`rendered`). A policy default is reported as `policy`. It is applied to the typed config after the handler built it, so the properties do not hold it: the entry addresses the path the author would have written (`replicas`, `resources.requests.cpu`, `storage.size`, `size`, `minReplicas`), which is a path the properties do not contain. A config reports its defaults through the optional `PolicyDefaultReporter` interface, which the engine reads right after `ApplyPolicy`, for a component and for a trait. A policy default is reported only when it fills the value: an authored value, an `unlimited` limit and the replicas of a deployment a scaler trait owns have no `policy` entry. A policy default fills a value only when the value was not present in the properties the handler received. That holds for `replicas`, which counts a value left by the author, by a lowering rule or by a capability fill as explicit, so a rule-added `replicas` beats the policy default. A config type that applies a policy default and does not implement `PolicyDefaultReporter` leaves it unreported. A value launcher's own code fills after the policy decided is reported as `builtin`: the steps a lowering rule attached (`Component.AfterPolicy`) run after `ApplyPolicy`, and the config they fill reports through the optional `BuiltinDefaultReporter` interface (embed `BuiltinDefaultLog`), which the engine reads once after all those steps ran, for a component only. Today that is the postgresql Cluster: `enablePDB`, always, and `storage.size` when no storage spelling is set after the policy. A size the author wrote is `rendered` (the postgresql rule moves `storageSize` to `storage.size`) and a size the policy supplied is `policy`; neither has a `builtin` entry. Both logs replace by path, so a later report of a path wins.

A `capability` source also has a `Detail` when the profile went through
`Transformer.EvaluateProfileWithProvenance`, which returns what `EvaluateProfile` returns
plus, on each binding, the step that supplied each leaf of its rendering (an unexported
field, so serialization ignores it):

| Detail | Meaning |
|--------|---------|
| `profile` | The value is in the profile document. A binding no handler, rule or applicable `CapabilityDefinition` claims is all `profile`. |
| `schema-default` | A `CapabilityDefinition` default for a key the profile left out or set to null. |
| `handler-default` | The trait handler's or lowering rule's `ValidateAndApplyDefaults` added the leaf or changed its value (an empty `issuerRef.kind` rewritten to `ClusterIssuer` is the handler's, not the profile's). |

An empty `Detail` means not determined, never `profile`: it is empty for a profile
evaluated with `EvaluateProfile`, for a binding built by hand, for a leaf a
`ComponentCapabilityFiller` wrote (the engine cannot map the filler's destination to a
rendering path), and for the other kinds. A top-level fill of an object carries each
leaf's own `Detail`, since one object can hold leaves of different origins. A leaf a step
removes is not reported. A value a step hands back unchanged keeps its source and `Detail`
whatever Go shape the step gave it (a `map[string]string` returned as a `map[string]any`,
a list returned typed); only a value the step added or changed takes the step's.
One limit: values that a Go caller or handler builds in shapes YAML or JSON cannot express
(a NaN, a map with non-string keys, a func or chan field, a collection re-typed from the
one it was given) may report a less specific source, such as a missing `Detail` or a
filler's capability key in place of the original one. A pointer, and the exported fields of
a struct, are copied for the comparison, so a handler that changes what a pointer points
to in place is seen, and a copy of a pointer of a defined type (`type P *int`) keeps that
type, so a step that hands back a plain pointer to an equal value has changed the value.
An unexported field, a func and a chan are compared as they are, so a
change made in place to what an unexported field holds reads as having left it unchanged.

`Path` is dot-separated object keys, `[i]` for a list element, and `["key"]` (a quoted
string) for a key that is empty or holds `.`, `[` or `]`: `tls.enabled`,
`volumeClaimTemplates[0].storageClass`. An empty object or an empty list is its own leaf
and a null is none. Entries are sorted by component, member, trait, trait index and path,
so two calls on the same input report the same bytes.

The report follows the merge rule exactly (the rendering is the default, an authored value
wins, a null over a rendered key is skipped, two objects merge key by key, anything else
replaces whole), so it names the winner and never a source that was shadowed. A
`ComponentCapabilityFiller` is attributed by comparing the properties before and after the
call: a leaf it added or changed is `capability`. The filler is handed a call-local
consumed set for that, merged into `ConsumedCapabilities` afterwards, so the result of
`TransformWithPolicy` is unchanged.

`Transform` and `TransformWithPolicy` build no report, carry no recorder and read no
source of one. The configs they build still record the defaults they apply, as
`PolicyDefaultLog` and `BuiltinDefaultLog` do: one small entry per reported default,
made whether or not a report is asked for, and read only by `TransformWithProvenance`.
The output of the two is unchanged. The report covers the
values a policy supplies and the fills of a post-policy step only where the config
reports them (`policy` and `builtin` above; the typed config, not the property map,
holds them). It does not cover a policy default or a fill of a config that reports
nothing, defaults a handler applies while it builds its typed config, parameter
substitution, the names, labels and annotations the engine stamps on an object, or a
statement about the generated objects: entries key property paths.

### Registering the builtins

[`pkg/oam/builtin/registry`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/registry)
returns every built-in handler and lowering rule launcher implements, one map per
registration call, keyed by type. Registering all five, then declaring the type identities
with `DeclareTypes`, gives the transformer `kurel build` uses (`pkg/cmd/kurel/build.go`
registers the same set and declares the types last). `DeclareTypes` declares the versioned identities a document may author
for the registered types, such as `deployment.apps@v1` or `webservice@v1` (see
[Type identity](parsing-and-types.md#type-identity)), so it runs after the registration:

```go
t := oam.NewTransformer(registry.ComponentHandlers(), nil)
for _, r := range registry.ComponentLoweringRules() {
	t.RegisterBuiltinComponentLowering(r)
}
for name, h := range registry.TraitHandlers() {
	t.RegisterBuiltinTrait(name, h)
}
for name, h := range registry.PolicyHandlers() {
	t.RegisterPolicy(name, h)
}
for _, r := range registry.TraitLoweringRules() {
	t.RegisterBuiltinTraitLowering(r)
}
if err := registry.DeclareTypes(t); err != nil {
	return err
}
```

The `registry` package documentation holds the canonical copy of this example.

Each call returns a new map, so a consumer drops or replaces entries before it registers
them, and the change reaches no other caller; a type launcher adds later appears there
without a change on the consumer's side. `registry.DeclareTypes` is refused for a built-in type
the Transformer does not hold, so a consumer that dropped one does not call it: it passes
`DeclareComponentTypes`, `DeclareTraitTypes` and `DeclarePolicyTypes` the maps that
`ComponentTypeIDs()`, `TraitTypeIDs()` and `PolicyTypeIDs()` return, with the same entries
deleted. Built-in traits and trait lowering rules go
through `RegisterBuiltinTrait` and `RegisterBuiltinTraitLowering`, which exempt them from a
`CapabilityDefinition` of the same type. A consumer that replaces a lowered type (such as
`webservice`) with a handler of its own deletes it from `ComponentLoweringRules()` first:
`RegisterComponentLowering` panics on a type that is already a handler. The maps hold no
handler for the delivery policies and traits above; a consumer that delivers registers its
own.

## Lowering

Some documents, components, traits, and policies are authored in a higher-level
vocabulary that has no direct dispatchable handler — a type a platform wants expanded
into one or more terminal types before the transform's own component/trait dispatch
runs. Where a component lowering rule places each object it emits (a same-name
sibling member, a trait on one member, or a separately named kind component), and
how a trait builds the same object as its kind twin, is set out in
`docs/oam/design-lowering-engine.md`, section "Where a lowering rule places each
object".
The lowering engine (`lowering.go`, `lowering_raw.go`) is the shared fixpoint
that performs that expansion, reachable from two entry points:

| Entry point | Reachable rules | Use when |
|---|---|---|
| `(*Transformer).lower`, invoked from `Transform`/`TransformWithPolicy` | `DocumentLoweringRule`, `ComponentLoweringRule`, `TraitLoweringRule`, `PolicyLoweringRule` | The authored document's field set already fits `ApplicationSpec` (`Components`/`Policies`, nothing else), so `ParseWithExtraTypes` can decode it before the transform lowers it away. |
| `(*Transformer).LowerRaws` — standalone, called by the caller BEFORE its own parse fan-out | `RawDocumentLoweringRule` only, for round 0 only. Nothing it emits is sealed; every other rule runs later, in `Transform`. | The authored document is a whole higher-level kind carrying its OWN fields that don't fit `ApplicationSpec`, so it can never survive a strict `ParseWithExtraTypes` decode. `LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error)` rewrites only the inputs whose `(apiVersion, kind)` pair a registered `RawDocumentLoweringRule` claims — `SupportedAPIVersion` unless the rule implements the optional `RawDocumentAPIVersioner` hook (`RawDocumentAPIVersion() string` — a plain `APIVersion()` accessor does not opt a rule in), which a consumer that owns its own API group uses so its documents are claimed rather than silently passed through (everything else passes through byte-identical) — decoding each claimed input via that rule's own `DecodeDocument` and running its `LowerDocument` once. The emitted `Application` bytes then re-enter the caller's parse and `Transform` exactly as if authored: the in-transform rules run there, under that call's `MaxLoweringDepth` budget, and ClusterProfile capability rendering is merged there. Before claiming a document, `LowerRaws` validates its `metadata.name` (required, DNS-1123 subdomain) and `metadata.namespace` (DNS-1123 label, if set) itself — the in-transform path gets the same check from `ParseWithExtraTypes`, which a raw-entered document never goes through. |

**Raw-rule contract: a raw rule rewrites authored input; it does not lower.** Emit what a
person would author — for example an `expose` trait carrying only its `hostnames` — and
let `Transform` run the in-transform rules and merge capability rendering. A raw rule
that copies capability rendering (a `PlatformReserved` field such as `controllerType`)
into what it emits is writing platform-reserved values into authored input, and
`Transform` rejects them with `ErrPlatformReserved`. `ctx TransformContext` is still
passed to the raw rule, but `LowerRaws` no longer needs an evaluated `ClusterProfile`
itself, since no `CapabilityAware` in-transform rule runs inside it. Besides metadata,
`LowerRaws` checks duplicates and generated-name collisions across the call (pass-through
identities included), rule arity, component and policy schemas of each emitted document,
and that each emitted document's apiVersion is `SupportedAPIVersion` or the group its
rule claims. Emitted traits are not checked in `LowerRaws`. A component or policy is
checked on its own copy of its properties, so checking it cannot strip a reserved
`null` from a trait or another element that shares its map before `Transform` sees it.
`Transform` does not
shape-check trait properties either (it enforces only their platform-reserved keys): like
any authored document, each parsed output document must go through
`ValidateAuthoredProperties`, after parameter substitution, before `Transform`, which then
takes the document the validator returned (see its paragraph under [Property schemas](schemas-and-contract.md#property-schemas)). Without it, a raw rule's `enablePDB: "true"` on
a trait whose schema declares a boolean builds cleanly and is silently dropped.
`LowerRaws` does not schema-check emitted traits itself; a caller that
relied on that must now add the `ValidateAuthoredProperties` call.

The returned bytes cannot say which rule produced them: the element origin is unexported
and does not survive serialization. A caller that records provenance calls
`LowerRawsWithSteps(raws, ctx) ([]json.RawMessage, []LoweringStep, error)` instead, which
returns the same documents plus one `LoweringStep` per claimed input, in input order: `Rule`
is the rule identity (`rawdocument/<apiVersion>/<kind>`, suffixed `@<version>` when the rule
declares `ContractMetadata().Version`), `From` the authored `metadata.name` and `To` the
`metadata.name` of each document it emitted. `From` is a name, not a full identity, so two
claimed inputs of one kind and name in different namespaces are told apart only by their
position. A pass-through input has no step; on error the steps are nil and the
`LoweringError`'s `Chain` reports the failing document's steps. `LowerRaws` is
`LowerRawsWithSteps` with the steps dropped. The source of each property value after
`TransformWithPolicy` is a separate report, `TransformWithProvenance` (see "Property
provenance" above).

`NameAllocator`'s generated-name collision detection (D2) is scoped by `Origin.Namespace`,
not by name alone: two documents authored in different namespaces may generate the same
child name without colliding, since they lower to namespace-disjoint resources — the same
reason two identically-named Kubernetes objects in different namespaces coexist. Two raw
inputs sharing both a name and a namespace are rejected earlier, as a duplicate authored
document, before either reaches the allocator.

Every rule receives the run's one shared allocator as `LoweringContext.Namer`, which the
engine never leaves nil at any position (raw documents included), so a rule derives every
generated child name through `lctx.Namer.Name(base, suffix, origin)` with no nil-guard
fallback. A nil `Namer` is a contract violation by whoever built the `LoweringContext`.
A name that has a name role is resolved with `lctx.ResolveName(base, suffix, spec)` instead
(see [Name roles and the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook)), which reserves no component name: the rule
reserves the one it emits a component under with `lctx.Namer.Reserve`.
Code that drives a rule directly, outside the engine — a rule's own unit test in another
module, a pre-pass, a golden-file or fixture harness — builds the `Namer` with
`NewNameAllocator()` (the zero value is not usable) and shares that one allocator across
every call belonging to the same run, so collisions between those calls are detected.
`LowerRaws` shares one allocator across its raw inputs, but only for round-0 raw-rule
claims; in-transform rules run inside each document's own `Transform` with a fresh
allocator, so a name an in-transform rule generates is never compared across documents.
A caller that transforms several documents detects that collision with
`CheckCrossDocumentCollisions`: it takes each document's identity and generated objects
(a `GeneratedDocument` per document, including any objects a layout walk adds) and reports
every object — keyed by API group, kind, namespace and name — that more than one document
generates, once per object, naming every document that generates it. Being keyed on the objects rather than on allocator claims,
it also catches two same-named authored components in one namespace, and a cluster-scoped
object generated from documents in different namespaces. An object's namespace is read from
the object, so pass objects as generated; an object with no kind is an error. An object that
Kustomize's build drops (`config.kubernetes.io/local-config` present, not `"false"`) is not
compared.

Both checks return the refusal as a `*oam.GeneratedObjectCollisionError`, with the text
unchanged. `errors.Is(err, oam.ErrGeneratedObjectCollision)` is true, also behind a prefix a
caller adds, and `errors.As` finds the error and tells which objects collide and who
produces each, without parsing the text:

```go
var collision *oam.GeneratedObjectCollisionError
if errors.As(err, &collision) {
	// collision.Scope: oam.CollisionAcrossDocuments or oam.CollisionWithinDocument
	for _, c := range collision.Collisions {
		// c.Group, c.Kind, c.Namespace, c.Name: the object
		// c.Producers: every producer of it, a controller's claim first
	}
}
```

Nothing above compares the applications inside one document: a component and another
component's trait, two components, or two traits can each generate the same object, and
both pass `Transform`. That includes one component's own: two of
its traits, or a trait and an application the component generates itself (a `helm`
component's values ConfigMap), rendering one object. `Transform` alone refuses only those whose
names it resolves under a name role (see [Name roles and the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook): two `scaler`,
two `rbac` or two `networkpolicy` traits naming one object, on one component or on two), so a
library caller must run both steps below on every transformed document, as `kurel build`
does. After transforming a document, generate it
with `GenerateApplications(cluster)` and pass the result to `CheckInDocumentCollisions`. It
returns each application's objects with its producer, generating each application once, in
the order kure's own generation uses and with the bundle labels and annotations kure adds,
so its objects replace a `Bundle.Generate` of the same cluster rather than add a second
generation, which could differ from the first. The check reports every object, keyed as
above, that more than one application generates, naming each producer as a component (a
sibling group is one) or as a trait's sub-application and its component. Producers that
read alike are named once with their count, since the cluster cannot tell them apart: two
traits of one component whose sub-applications share a name read as `2 sub-applications
"dup" of component "web"`, a trait named after its own component and that component as
`2 applications "web" of component "web"`. The transform itself refuses
both of those shapes within one bundle (two applications of one name), so
the check meets them only in a cluster built some other way. A repeat within one application is not reported. `kurel build` runs both before it writes anything.

Both checks also hold an object a controller generates for a generated object as generated: for a HelmRelease with a chart template
(`spec.chart`), helm-controller generates a source of the template's kind named
`<namespace>-<name>` after the HelmRelease, takes over any object of that name and deletes
it with the HelmRelease, when the kind, name or namespace of the chart source it generates
changes, or when the release switches to `chartRef`. For kind `OCIRepository` it is the
OCIRepository in the HelmRelease's namespace; for kind `HelmChart`, the default, it is the
HelmChart in the namespace `spec.chart.spec.sourceRef.namespace` names, else in the
HelmRelease's (a HelmRelease `demo/web` whose sourceRef is in `flux-sources` claims the
HelmChart `flux-sources/demo-web`). A `spec.chartRef` claims nothing. So that identity
collides with an object of that kind any application or document generates — the
HelmRelease's own included, so within one application (a sibling group) too — and is
reported as generated by `helm-controller for HelmRelease … (chart.kind <kind>) of` its
producer, after the other collisions. The HelmRelease's namespace is the one it is
generated in, so under a Flux namespace the name, and a HelmChart's namespace when no
sourceRef namespace is set, follow the Flux namespace. The HelmChart claim is a refusal
that a document that built before the claim existed may now meet: a `helmchart` component
named `<namespace>-<name>` after a `helmrelease` beside it, in the same namespace.

`GeneratedChartSource(obj)` returns that source as a `ChartSource` (kind, namespace and name)
and whether obj makes one, a typed HelmRelease counting as one whether or not it states its
kind; the checks read their claim from it, and so does the `helmrelease`
component, which refuses a HelmRelease whose source helm-controller could not apply.
The name is always `<namespace>-<name>` after the HelmRelease's own
namespace (helm-controller API v1.7.1 `v2/helmrelease_types.go:1633-1649`); a sourceRef
namespace moves only the HelmChart.

A PersistentVolume or PersistentVolumeClaim that is force-applied, or covered by the
`ForceReplace` delivery intent, is warned about, not refused: when an
update changes an immutable field of a force-applied object, Flux deletes and recreates it
instead of failing the apply, which can lose a claim's data. Pass the same
`GenerateApplications` result, after `CheckInDocumentCollisions`, to
`Transformer.WarnForcedVolumes`. It emits one warning through the warning handler
(`SetWarningHandler`) per PersistentVolume and PersistentVolumeClaim that carries
`kustomize.toolkit.fluxcd.io/force: enabled` itself (an author's own, in a `manifests`
component for one) or whose application is `GeneratedApplication.Forced`. An application is
forced when it carries the `ForceReplace` delivery intent (the `force-replace` trait sets it) or when its bundle sets `Force` (launcher never does, so only a
bundle whose `Force` the caller set before generating). The warning
names the kind, `namespace/name`, the producer and every reason the object is forced — the
annotation, the intent, the bundle, in that order:
`PersistentVolumeClaim shop/data (component "db") is force-applied
(kustomize.toolkit.fluxcd.io/force: enabled; its application sets the force-replace
delivery intent): when an update changes an immutable field, Flux deletes and recreates it
instead of failing the apply, which can lose its data`.
An object whose only reason is the intent is forced by nothing in the generated objects, so
its warning is conditional on the workflow that delivers them:
`PersistentVolumeClaim shop/data (component "db") is covered by the force-replace delivery
intent of its application: where the delivery workflow maps that intent (kure's Flux layout
integration writes kustomize.toolkit.fluxcd.io/force: enabled), an update that changes an
immutable field deletes and recreates it instead of failing the apply, which can lose its
data`. `kurel build` output carries no mapping of the intent, so there the warning describes
what a consumer's delivery workflow would do, not what the output does.
An object counts as annotated as Flux's force selector matches it: the force key as a label
or an annotation, with `enabled` in any letter case. Objects are read as Flux applies them:
a list envelope still in the output stands for its members — Kustomize's build expands a
kind ending in `List` whose `items` is an array, recursively, then Flux expands any
remaining object whose `items` is an array, one level only — and a member is forced by its
own metadata, not the envelope's. An object generated more than once is warned once,
naming its first producer and every reason any copy is forced. Objects are read as
generated: what a consumer's delivery changes afterwards (patches, post-build substitution)
and anything the cluster changes on apply are not modelled. It
covers every generated claim alike — a `webservice`/`worker` `volumes` entry (a
synthesized `pvc` trait, so named as a sub-application of its component), the `pvc` trait, the `persistentvolumeclaim` component, a `manifests` component's objects — and changes no
output. A `volumes` entry with `claimName` and the `volsync` trait's `sourcePVC` generate no
claim: an existing claim is warned where it is generated. With no warning handler it
does nothing. An embedder that does not
call it gets no warning. Objects a layout augmenter adds outside `Generate` are not in the
inventory and are not checked (`kurel build` refuses an augmenter whose `Generate` does not
cover them; see `LayoutAugmentationCoverage`).

`Reserve`/`Name` fail on every repeat claim of a name, including one from the same content.
Rules whose outputs share one derived object (two components pointing at the same chart
source, say) use the emit-or-adopt pair instead:
- `EmitOrAdopt(name, identity, origin)` claims a name for an element whose content is fully
  determined by `identity`, a string the rule builds from every input that shapes that
  element.
- `NameOrAdopt(base, suffix, identity, origin)` is the same claim through `Name`'s
  `<base>-<suffix>` construction, shortening ([Names and overrides](names-and-overrides.md#names-and-overrides)) and
  DNS-1123 check.

The first claim returns `adopted=false`, and the rule emits the element. A later claim with
the same identity, from any element of the same authored document and in any round, returns
`adopted=true`, and the rule must not emit the element again. Same-round sibling components
cannot see each other's output, so this is how they share one object instead of colliding. A
different identity at the same name, the same identity claimed from another authored document
(each settled document is transformed on its own, so it would lack the adopted element), an
empty identity, and a name already held by `Reserve` stay hard errors, and `Reserve` still
refuses a name claimed this way. The cross-document refusal fires only where one allocator
spans documents (`LowerRaws`' raw rules, or a caller's own shared allocator); within
`Transform` every claim comes from one document. Errors name an identity only by a short
SHA-256 digest, never its text, so an identity may include sensitive inputs.

Three caller constraints the allocator does not check. The shared element must be of a terminal
type, one no lowering rule claims: claims outlive the round, so a lowerable element could be
replaced under another name while a later adopter still references the claimed one. Adoption
covers only the shared element: the adopting rule still emits its own output, because the
engine rejects an empty `LoweringResult` as a deletion, so a rule whose whole expansion is the
shared element cannot use this pair. And the document check sees only the authored document:
when a document rule fans one authored document out into several documents, their elements
share that origin, so adoption must not be relied on across them.

Four registration interfaces, one per position in the document tree, each with its own
registrar on `*Transformer` and a duplicate/dispatchable-collision guard (a type
claimed by a lowering rule must not also be a dispatchable handler type, and a
document kind must not be claimed by both document registrars):

| Interface | Registrar | Position | May emit |
|---|---|---|---|
| `DocumentLoweringRule` | `RegisterDocumentLowering` | whole document (`ApplicationSpec`-shaped only) | `Documents` |
| `RawDocumentLoweringRule` | `RegisterRawDocumentLowering` | whole document (any shape; own `DecodeDocument`) | `Documents` |
| `ComponentLoweringRule` | `RegisterComponentLowering` | `spec.components[]` | `Components`, `Policies` |
| `TraitLoweringRule` | `RegisterTraitLowering` | `spec.components[].traits[]` | `Traits`, `Components`, `Policies` |
| `PolicyLoweringRule` | `RegisterPolicyLowering` | `spec.policies[]` | `Policies` |

Every rule returns a `LoweringResult`; an entirely empty result is rejected — a
registered rule that emits nothing is indistinguishable from deleting the authored
element, which is not permitted. `Transformer.LowerableTypes()` reports every
kind/component-type/trait-type/policy-type claimed by rules registered on a
transformer (excluding raw-only rules, which are reachable only via `LowerRaws`), for
a caller to pass into `ParseWithExtraTypes` ahead of a transform that will lower them
(see [Parsing](parsing-and-types.md#parsing)).

**Same-name sibling groups.** A `ComponentLoweringRule` may emit several components
under one name, each of a distinct type that has a component handler (no lowering
rule claims it). They form one sibling group, which deploys as a single component.
Each member keeps its own config, policy defaults, traits and objects. The group has
one tier, one bundle, one `dependency` node and one layout directory. Its application
generates each member's
first object in emission order, then every member's remaining objects in the same
order, as a single component generating all of them orders them. `webservice`
lowers to a deployment, a service and a serviceaccount member, which give
Deployment, Service, ServiceAccount, then the claims of the deployment member's
synthesized `pvc` traits. It answers every config contract the transform reads
(Service port and port name, backend Service name, routing target, ServiceAccount,
single-pod claim) from the one member that has a value, gives the Flux namespace
to every member that takes one, and reports the group's name as its component
name (`ComponentNamed`). When a member's routing target selects another member's
pods (its non-empty `matchLabels` are a subset of that member's pod template labels,
with no `matchExpressions`) and every port of the routing member targets its own
port number, whatever its protocol, the traffic lands on the group's own pods, on
the ports it was routed to. The synthesized inbound policy then keeps the group's
component label as its pod selector, as one component deploying both would, while
its ports still go through the routing target: a routed Service port name becomes
its number and a non-TCP port is dropped. A member that remaps a port or names a
`targetPort` gets the Service `selector` policy, so it opens the port the pods
listen on. A member reports its pod template labels through an optional method on
its config, `PodTemplateLabels() map[string]string`; a member without it counts as
running no pods. Of the built-in pod kinds only `deployment` has it, the one pod
kind a lowering rule emits into a group. A rule that
emits another pod kind into a group must add the method to that kind's config, or
a routing member selecting its pods gets the Service `selector` policy.

Traits run per member, against that member's own config: the rule decides which
member carries each trait. A routing trait (ingress, Gateway API routes) belongs on
the member that owns the Service; a workload trait on the workload member. The
group applies its members' traits in authored order, not member by member: the
rule's own traits first, in member order, then the traits it forwarded, by the
slot each held among the authored traits (a forwarded trait a trait rule lowers
later keeps that slot). Trait sub-applications are therefore ordered as for one
component carrying the same traits. A rule whose members can land in different
groups owes more for a trait that configures a bundle rather than objects: one
copy per bundle, or a refusal where it cannot tell the groups
(`pkg/oam/builtin/components/README.md`, "Traits a lowering rule forwards").

The build refuses a group:
- whose members' tier annotations disagree (one of them carrying none included),
  unless a placement policy places the group (it is then in the placed tier);
- in which two members answer the same contract (a member that runs pods
  answers `ServiceAccountName` even with no name, since its pods run as the
  namespace's `default` account). One pair is a single answer: a member that runs
  no pods and names the account the group's one pod-running member runs as, which
  is the ServiceAccount beside the workload that runs as it (the renamed account of
  a `webservice` or `worker` component);
- that has a member needing layout-level resources;
- in which two members generate the same Kubernetes object (API group, kind,
  namespace and name), such as two Services both named after the group;
- in which traits on two members create the same sub-application (the same trait on
  both members derives one name, such as `web-rbac`, from the shared name). The names
  are compared after each sub-application's `ApplyPolicy` has run, so a policy that
  renames a sub-application onto another member's is refused, and the name it moved
  away from is free. Two sub-applications that both still carry
  the name the `Naming` hook gave them are compared by their defaults instead: the same
  trait on both members is refused whatever the hook answers, and two different
  sub-applications the hook gave one name are left to the bundle, which refuses two
  applications of one name as it does outside a group. A name is the hook's when the hook answered,
  also when its answer is the default. Several sub-applications of one name that one
  trait creates cannot be told apart: they are the hook's only when the hook named
  every one of them, and are then compared by all their defaults. A name the hook gave
  that meets, on another member, one it did not give (a trait's own, or one a policy
  renamed onto it) is refused by name.

An authored duplicate name is still refused. So is a name repeated by different
rule invocations, or by a trait or document rule, including a copy of a member.

Expansion runs to a **fixpoint**: every round, every current document's non-terminal
kind, components, traits, and policies are lowered once via their registered rule (if
any); the loop repeats until a round changes nothing, bounded by `MaxLoweringDepth`
(9) — a rule that keeps re-emitting its own (or another registered) type fails the
build with the full expansion chain rather than looping forever. A transformer with no
lowering rules registered anywhere returns the input `*Application` unchanged (the
same pointer — no copy, no allocation); registering rules only on the raw entry point
leaves that guarantee intact for the in-transform path.

Every emitted element carries an `Origin` — the AUTHORED location it descends from,
stamped once and copied verbatim onto every element expanded from it at any depth —
so a `LoweringError` always leads with the YAML the user actually wrote, then the
synthesized cause, then the expansion chain (`LoweringStep`s) that produced it.

`Origin.Rule` is the one field on `Origin` that is NOT copied verbatim: it identifies
the lowering rule that most recently produced the element — `"<position>/<type>"`
(e.g. `"trait/expose"`), suffixed with `"@<version>"` when the rule also implements
`ContractDescriber` (see [Contract metadata](schemas-and-contract.md#contract-metadata)) — and is re-derived at every hop, so
it always names the immediate producer rather than the first rule in a multi-hop
chain. `""` means the element was never itself the direct output of a lowering rule
(authored as-is, or carried through untouched).

A trait a rule builds is sealed: its properties are final, and no
`ClusterProfile` capability rendering is merged into it later. A trait a
`ComponentLoweringRule` or `DocumentLoweringRule` merely forwards stays an ordinary
authored trait (unsealed, still capability-processed). Forwarding covers returning
`comp.Traits` itself and returning unchanged copies of its elements inside a new
slice, for example to add one trait of its own next to the authored ones or, in a
document rule, to rebuild a component around the authored traits; a copy whose type
or properties map the rule replaced counts as built by the rule. A forwarded trait's
`Origin.Index` stays its authored slot even when the rule places its own trait ahead
of it, and a trait a document rule moves to another component keeps the component it
was authored on in its `Origin`. A `DocumentLoweringRule` is handed a copy of the document whose component and
trait slices are its own, so neither the rule nor the engine's stamping of what it
forwards writes through to the authored document; properties maps are shared, and a
rule still must not mutate them.

Sealing says nothing about whether the trait's content was checked. A component or
trait a rule emits is synthesized when its properties are the rule's own output from
checked input, so a `PlatformReserved` value the rule rendered from
`LoweringContext.Capability` is accepted rather than rejected as authored. That
holds only when the rule's input was checked before it ran: a
`ComponentLoweringRule` that declares a schema (`PropertySchemaProvider`) or whose
input component is itself synthesized, or a `TraitLoweringRule` that declares a
schema over an unsealed trait or whose input trait is itself synthesized. A trait
nested in an emitted component gets the same classification as that component. A
`DocumentLoweringRule`'s output is never synthesized: the rule
sees trait and policy properties and metadata too, which nothing checks before it
runs. Such a rule writes a reserved value it renders from `LoweringContext.Capability`
with `Component.RenderReserved(path, value)`, or `Trait.RenderReserved(path, value)`
for a trait it builds, which writes a deep copy of `value` at the dot-separated
object-key `path` in `Properties` (`"networkPolicy"`,
`"tls.secretName"`; array items cannot be addressed; an object along the path that
is missing or `null`, typed nil included, is created) and records another deep copy
of it as rendered, both keeping its Go types. Changing `value` afterwards changes
nothing in `Properties`, and one value rendered at two paths is two independent
copies, so emission validation normalizing it under one key leaves the other as
written. A reserved key is then accepted only while it
holds the recorded value at that same path: either that value itself, or what
emission validation makes of it under the key's own schema, compared Go type for Go
type and a floating-point number bit for bit. So the record survives validation's
normalization exactly where validation performs it (a `[]byte` or `[]int32` under a declared array becoming `[]any`, a named
integer under a declared integer becoming `int`), and nowhere else: below a key an
object leaves to `AdditionalProperties`, or under a schema with no `Type`, the
handler receives what the rule wrote, so a rendered `[]byte` there is not matched by
an authored list of the same integers. A value the rule copied into the key from a
trait, a policy, metadata or another component, or changed after recording it, is
refused like any authored one — including a number that only prints the same, such
as an authored `1.0000000000000001e+18` over a rendered `1000000000000000100`, or one
that only compares equal, such as an authored `-0.0` over a rendered `0.0`, which a
handler tells apart with `math.Signbit`. `RenderReserved`
refuses a value that is or holds a `null`, NaN or ±Inf, any Go type other than strings, booleans, numbers, slices, arrays and
string-keyed maps, or a collection that contains itself. The record follows the component
through copies and later lowering rounds; a rule that rebuilds a component from its
fields, or a document that is serialized and parsed again (what `LowerRaws` returns),
leaves it behind. A trait works the same way against its own type's schema; its
record covers the trait's own properties, checked before `applyTraits` merges a
capability rendering in, so that merge exempts nothing. Output of any other rule whose
input was not checked stays authored too (`RenderReserved` works there the same way)
and is checked like anything a user wrote, so a schema-less rule cannot pass an
authored reserved value through. Its components and traits — those it emits at trait position and those
nested in the components it emits — are checked for reserved keys as they are
emitted, before emission validation strips an explicit `null`, so a reserved key
written as `null` is refused here exactly as when authored directly.
Emission validation of any rule's output
normalizes the element's own copy of its properties, never the map the rule emitted,
so validating an element whose type does not reserve a key cannot strip that key's
`null` from another element sharing the map (including one the rule forwarded), which
may lower into one that does reserve it a round later. A trait such a rule only forwarded
keeps its classification, as a forwarded component does. A sealed trait that is not
synthesized is checked as it stands: as it is emitted, and again before a
`TraitLoweringRule` that declares a schema runs over it and when its handler applies
it. A schema-less rule that copies an authored reserved value into a trait it emits,
or writes one there any way other than `Trait.RenderReserved`, is therefore rejected;
a value it renders from capabilities with `Trait.RenderReserved` is accepted while the
key still holds it. A
sealed trait that passes that check is still not checked input for what the rule
emits: its schema covers the trait's own reserved keys, not those of the components
or traits the rule builds, so that rule's output stays authored. A
component a `DocumentLoweringRule` forwards — the same element of the
`doc.Spec.Components` it was handed, not a copy — keeps the classification it arrived with:
forwarding neither makes it synthesized nor resets it to authored. What a user wrote is checked
before any rule can rewrite it: before a `ComponentLoweringRule` claims the
component, and for every component of a document before its `DocumentLoweringRule`
runs, so rebuilding a component by value does not launder an authored reserved
value. Every component and every trait no rule synthesized is also checked at the
start of each lowering round, before any rule of that round runs, so a rule that
emits an element sharing a component's or trait's properties map cannot have
emission validation strip an authored `null` from it.

**Post-policy steps.** Lowering runs before the environment policy, so a rule
cannot write a value it derives from what the policy decided. It attaches a
`PostPolicyStep` to the component it emits instead, with
`Component.AfterPolicy(step)`. The transform runs the steps on the config the
component's handler built, in the order attached, right after
`Enforceable.ApplyPolicy` and before any trait of the component; an error fails the
transform, naming the component. A step that holds the config to the policy refuses as
`ApplyPolicy` does: an error that holds a `PolicyRefusal` fails the transform as the
component's `ViolationError` with the refusal's class, any other error as a
`TransformError`. The transform does not apply the policy again
after a step. A kind may hold its own config to the policy once more when it generates,
as `passthrough` does, but no kind has to: holding what the step writes to the policy is
the rule author's responsibility. A step is part of the component value: it survives
copies and later lowering rounds, including a trait rule rewriting the component's
traits, but not serialization, and a document cannot author one. A
`ComponentLoweringRule` that lowers a component carrying a step carries it over only
by copying that component; a component it builds anew has no step, and the engine
cannot tell one was dropped. The built-in user is the `postgresql` rule, which
attaches a step to the `cnpg-cluster` it emits to set the values postgresql derives
after the policy (`CnpgClusterConfig.ApplyPostgresqlDefaults`).

**Order between a rule's own components.** A rule that emits components of which one
must be applied before another declares it on the later one:
`Component.OrderAfter(names...)`. Each name is a component of the
document once lowering has settled, usually one the same rule emits or adopts
(`LoweringContext.ResolveSharedName`); an unknown name, or the component's own, fails the
transform, naming the component and the rule's order. Like a post-policy step it is
part of the component value, survives copies, is not serialized, and cannot be
authored: an author orders components with a `dependency` policy. It survives further
lowering too: when a component rule lowers a component that carries an order, the
engine gives that order to the components the rule emits for it, whatever the rule
built them from, so what the component becomes waits as it did. One kind of component
is left out: a generated source, as [Pipeline](pipeline.md#pipeline) defines it (the rule orders another of
those components after it and orders it after nothing; it is not a member of a
same-name sibling group). It does not wait with
the component: it is the application's, shared by every component that names the same
source, and stays among the application bundle's own applications. Any other source the
rule emits is a component like any other and takes the order. `OrderAfter` is one of
the three ordering declarations described under [Pipeline](pipeline.md#pipeline).
The built-in users are the `helm` rule, which orders the release after the source it
generates or adopts, and the `oci` rule, which does the same for each Kustomization of a
source several `oci` components share. A component alone on its
artifact orders nothing: its source is a member of its same-name sibling group.

**Waiting as another component.** A component a rule emits that must not wait for the
component it lowers, but must still wait for what that one waits for, declares it with
`Component.WaitsAs(name)`. It follows every component the
document's `dependency` rules make `name` follow, itself excepted, as if each rule named
it too. The rules are read once every lowering has run, so a rule another component's
lowering generates counts as an authored one does, though the rule lowering the
component could not see it. A name no rule makes wait adds nothing, and what `name`
waits for only by a `WaitsAs` of its own is not passed on.
It is an edge like a dependency rule's: it can close a cycle, which is refused naming
the `dependency policy`, and a generated source that declares one and would wait is
refused as a dependency rule making it wait is. Like `OrderAfter` it survives copies and
further lowering, a generated source aside, is not serialized, and cannot be authored.
Unlike `OrderAfter`, a wait a generated source inherits that way (one nothing else its
rule emits is ordered after) does not make it an ordinary component: it is still a
generated source, refused as above if the wait would make it wait.
The built-in user is the `postgresql` rule: the inline recovery store waits as the
component, and the Cluster is ordered after the store.

A source that several components share is always a component a rule emits once and the
others adopt. The engine has no other sharing mechanism: the `SourceDeduplicatable`
interface and the pass that let the first config of a shared source key emit it were
removed.

A trait-position rule that implements `CapabilityAware` is enforced by the engine
exactly as `applyTraits` enforces it for a dispatchable `TraitHandler`: missing the
required `ClusterProfile` capability fails with `ErrMissingCapability`. A rule that
also implements `PropertySchemaProvider` has an authored value for one of its
platform-reserved properties rejected before capability rendering is merged into the
trait it receives. Independently of that — every lowering rule, whether or not it
declares its own schema — has each element it emits (component, trait, or policy)
validated against the TARGET handler's declared schema before the emitted element is
accepted into the next round (see [Property schemas](schemas-and-contract.md#property-schemas)).

