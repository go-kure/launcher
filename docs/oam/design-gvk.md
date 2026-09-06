# Design: API Group and Document Ownership

*Status: Final | Prerequisite for: design-cluster-profile.md, design-kurel-package.md,
options-policy-interface.md*

| Version | Date | Summary |
|---|---|---|
| 1.0 | 2026-05-14 | Initial — records GVK decision, rationale, strictness rule, OAM reuse |
| 1.1 | 2026-08-23 | Adds the type-name reservation covenant and document-format lifecycle |

---

## Design Statement

Launcher defines its own native application model under `launcher.gokure.dev/v1alpha1`.
The model is inspired by OAM concepts — applications, components, traits, and
capability-driven rendering — but launcher does not claim native API compatibility with
`core.oam.dev/v1beta1`. Standard OAM import/export compatibility may be supported later
through a translation layer.

---

## Launcher-Native Documents

All launcher-native input files share a single API group and version:

| File | apiVersion | kind |
|---|---|---|
| `app.yaml` | `launcher.gokure.dev/v1alpha1` | `Application` |
| `kurel.yaml` | `launcher.gokure.dev/v1alpha1` | `Package` |
| `cluster.yaml` | `launcher.gokure.dev/v1alpha1` | `ClusterProfile` |
| any `*.yaml` under a package's `definitions/` directory, or passed via `--capability-def` | `launcher.gokure.dev/v1alpha1` | `CapabilityDefinition` |

These documents form one coherent API family. They are not split across groups or
versions because they belong to the same ownership and lifecycle domain:
`Application` is what to run, `Package` is how it is packaged, `ClusterProfile` is how
the target platform resolves capabilities for it, and `CapabilityDefinition` declares the
rendering schema for a custom (non-builtin) trait type.

### Example document headers

```yaml
# app.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
```

```yaml
# kurel.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: webservice
  version: "1.0.0"
```

```yaml
# cluster.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: prod-eu-west
```

---

## Why `launcher.gokure.dev`

**Not `core.oam.dev/v1beta1`**

Using the upstream OAM GVK would signal:
- runtime compatibility with KubeVela and other OAM implementations that does not exist
- API ownership that launcher does not hold
- stronger semantic alignment to upstream OAM than launcher intends

Launcher's component and trait types (`webservice`, `expose`, `certificate`) carry
launcher-specific schemas and rendering semantics. Another OAM runtime may define a type of
the same name — KubeVela ships its own `webservice`/`expose` — but that name match does not
imply compatibility: the two are governed independently, and nothing beyond the string is
shared. The shared shape (components/traits/properties) is a design choice, not an API
contract.

**Not a platform-specific zone**

Launcher is a go-kure project — an open-source product that is not tied to any single
downstream platform. Borrowing a specific platform's DNS zone or label namespace for the
API group would make the application model look platform-specific when it is intended to be
launcher-native and publicly usable by any consumer. Embedding a downstream platform's
identity in the group name would tie the API to that platform rather than to launcher as a
standalone product.

**`launcher.gokure.dev`**

Reflects the actual ownership (go-kure project), keeps launcher's API separate from both any
downstream platform's APIs and the upstream OAM namespace, and is honest about what these
documents are: launcher's native input format.

---

## Type-Name Reservation Covenant

Component, trait, and policy type names (`webservice`, `expose`, `certificate`, …) are treated
as one flat namespace by convention — this covenant is what makes that true, not launcher's
code. Today, components and traits are each checked against their own allowlist in
`pkg/oam/validate.go` (`validComponentTypes` / `validTraitTypes`) with no cross-check
preventing the same name from meaning different things in each, and policy type names carry no
allowlist at all (`validateApplicationPolicy`). The covenant below is the naming discipline that
stands in for that missing enforcement, applied uniformly across all three categories.

That discipline is shared not only within launcher, but with any downstream dialect that
extends launcher's model — a document format built as a deliberate superset of
`launcher.gokure.dev/v1alpha1`, adding its own component/trait/policy types alongside
launcher's own. An extending dialect widens the namespace through its own custom-type channel,
not through launcher's registry.

Without a rule, launcher could later ship a builtin under a name an extending dialect already
uses with different semantics — a permanent squatting collision neither side can resolve after
the fact. The covenant below prevents that.

1. **Reservation.** A type name in active use by a dialect that extends launcher's model is
   reserved.
2. **Upstream-or-rename.** Launcher may take a reserved name **only** by upstreaming that
   feature into launcher with the same semantics, so the name becomes a launcher builtin
   meaning what it already meant downstream. For a genuinely different feature, launcher picks
   a different name.
3. **Shadowed types stay compatible supersets.** Where a name is defined on both sides, the
   downstream implementation must remain a compatible superset of launcher's — accepting
   everything launcher accepts, with the same meaning — or be renamed on the downstream side.
4. **Family reservation.** Some dialects spell a successor implementation as
   `<family>.v<N>` (e.g. `webservice.v2`), with a bare name equivalent to `.v1` and the dot
   reserved exclusively for that generation marker. A reservation under this covenant covers
   the **whole family**: reserving `backup` also reserves `backup.v2`, `backup.v3`, and so on.
   An upstreamed name follows the same upstream-or-rename discipline at each generation it
   reaches. Note that launcher does not itself enforce a type-name grammar today — type names
   are allowlist-checked, not pattern-checked (`pkg/oam/validate.go`) — so this convention
   binds naming *choices*, not the parser.

**Current inventory**

| Class | Names | Status |
|---|---|---|
| Reserved (downstream-only) | `backup` | No launcher component or trait of this name exists; launcher must not claim it for an unrelated feature. (Distinct from the existing `backup` *property* of the `postgresql` component — a property name, not a type name; no collision.) |
| Shadowed (same name, downstream superset) | `helmchart`, `prune-protection` | Launcher builtins; the downstream implementation carries additive behaviour on top. Upstreaming these deltas — and retiring this shadowed class — is tracked in go-kure/launcher#245. |

**Enforcement is deliberately review discipline, not CI.** An extending dialect commonly lives
in a separate, non-public project that launcher's CI cannot see or gate against; a downstream
dialect statement (its own `extends` / reserved-types / shadowed-types declaration, where one
is published) is informational input to review, not an automated check. This is revisitable if
launcher gains outside contributors who cannot be expected to know the covenant by convention
alone.

---

## Parser Strictness

Launcher rejects unknown fields in all launcher-native documents. An `app.yaml`,
`kurel.yaml`, or `cluster.yaml` with unrecognised keys is a build error.

Rationale: unknown fields are most often typos or stale config carried over from a
different tool (e.g. a `cluster.yaml` derived from a downstream runtime's profile that still
contains delivery-wiring or catalog fields). Strict parsing surfaces these problems at build
time rather than silently ignoring them and producing incorrect output.

Operators deriving a launcher `cluster.yaml` from a downstream runtime's `ClusterProfile`
must remove the downstream-specific fields before use. See `design-cluster-profile.md §7`.

### Two levels, two mechanisms

Strictness is enforced at two distinct levels, because an `app.yaml` is not a single strictly
typed struct all the way down.

**The document envelope** — `apiVersion`, `kind`, `metadata`, `spec`, and every field of a
component, trait or policy entry other than `properties` — is decoded into Go structs with
`KnownFields(true)` (`ParseWithExtraTypes`, `pkg/oam/parser.go:99`). A misspelled `replicaz` at
the component level, or a stray `spec.traits`, fails there.

**Authored `properties` maps** are not covered by that decoder. `Component.Properties`
(`pkg/oam/types.go:44`), `Trait.Properties` (`:63`) and `ApplicationPolicy.Properties` (`:87`)
are each `map[string]any`, so YAML strictness stops at the envelope and any key at all decodes
successfully. Those maps are instead checked against the handler's own declared
`PropertySchema` by `Transformer.ValidateAuthoredProperties`, which the build calls immediately
after parsing (`pkg/cmd/kurel/build.go:149`). An undeclared key is a build error naming the
allowed fields; a declared key whose value has the wrong type is a build error too. An array- or
object-typed value that validation had to rebuild in order to check it — a typed Go `[]string`
or `map[string]string` normalised into `[]any`/`map[string]any` — is written back in place, so
the handler downstream sees the shape that was actually checked. Scalars are never rewritten,
and a schema that declares no `Type` at all (a quantity, an int-or-string) is checked only
against its `Enum`, if it has one.

**Ordering is load-bearing.** The authored-properties check runs *after* `ResolveParameters`
(`pkg/cmd/kurel/build.go:114`). In package mode an authored value may be a `${...}` placeholder,
which is a bare string until substitution; type-checking before substitution would reject a
document whose integer- or boolean-typed property is supplied by a parameter.

### Two deliberate carve-outs

Neither is an oversight; both are places where launcher has no schema to check against, and
inventing one would reject documents that are correct today.

**Application policies.** `ApplicationPolicy` is "passed through to the runtime unchanged"
(`pkg/oam/types.go:83`), and no production code registers a `PolicyHandler` —
`Transformer.RegisterPolicy` (`pkg/oam/transform.go:295`) has no non-test caller. A policy's
properties therefore have no declared shape, and are not checked.

**Trait types declared by a `CapabilityDefinition`.** A definition supplied via
`--capability-def` declares that a trait type *exists*; it does not declare what properties that
type accepts. With no `PropertySchemaProvider` behind it, such a trait's properties are left
unchecked rather than rejected wholesale.

### `scope` is declared by the engine, not by a handler

Not a carve-out — a property whose schema lives somewhere other than a handler. `scope` selects
which `ClusterProfile` capability binding a trait resolves against: `buildCapabilityKey`
(`pkg/oam/transform.go:985-991`) builds the key `"<traitType>.<scope>"` for **every** trait type,
falling back to the unscoped `"<traitType>"` when no scoped binding is declared. It is therefore
legal on any trait, including the many whose handlers declare no such property — a `pvc` trait
selecting a fast storage class is the worked example, and it built correctly long before the
authored path was checked at all.

`expose`, `ingress` and `httproute` also declare `scope` in their own schemas, but for an
unrelated purpose (disambiguating sub-application names); that coincidence is why this was
invisible until authored properties were checked. The authored check merges
`engineTraitProperties` (`pkg/oam/property_validate_authored.go`) into the trait's schema, with
the handler's own declaration winning when both describe the key, and without mutating what
`PropertySchema()` returned — so `HandlerSchemas` continues to advertise only what each handler
actually declares. The engine type-asserts `scope` to `string`, so the check declares it a string
and rejects any other non-null type rather than letting it be silently ignored. An explicit
`scope: null` stays accepted, like any absent optional property (`isNullValue` short-circuits
ahead of the type switch, `pkg/oam/property_validate.go:108`), and resolves against the unscoped
binding exactly as omitting the key does.

### Required fields are checked at nested levels only

`Required` on a *top-level* authored property is deliberately not enforced at parse time. A
trait's top-level property map is merged with the ClusterProfile's capability rendering after
parsing (`applyTraits` → `resolveCapability`, `pkg/oam/transform.go:845` and `:951`), so a
capability-aware trait may legitimately author a document in which the platform, not the
author, supplies a required property. Enforcing `Required` before that merge would reject it.
Nested `Required` — inside an object- or array-typed property — *is* enforced, because
capability rendering merges only at the top level.

---

## Document-Format Lifecycle

`launcher.gokure.dev/v1alpha1` names a document *format* for `app.yaml`, `kurel.yaml`,
`cluster.yaml`, and any `CapabilityDefinition` document. This section states what stays true
while that string is unchanged, and what must change it — binding on launcher itself, and
relied on by any consumer that pins the version string, including a dialect that declares
itself as extending it.

**Stability promise.** While `launcher.gokure.dev/v1alpha1` is current, every document that
was valid under it remains valid, and compiles to the same output.

**The additive test.** A change is additive **if and only if** every previously valid document
remains valid *and* compiles to the same output. Additive changes ship freely under the same
version string — no coordination required, no deprecation cycle. Everything else — a removed
or retyped field, a changed default, a changed rendering target — is a breaking change.
Changing a default is a semantic change; there is no "just tweaking a default."

The additive test only means anything once authored `properties` are actually checked, and
until go-kure/launcher#408 they were not — an undeclared key decoded into a `map[string]any`
and was silently dropped. While that held, *every* property ever added to an existing component
or trait kind was formally breaking, because a document could already have been authoring that
key to no effect and would start compiling to different output the moment a handler claimed it.
Closing the gap is itself a one-off exception to the test: a document that authored a key no
handler declares was accepted before and is a build error now. It never compiled to the output
its author intended — the key was dropped — so the exception is taken deliberately here, under
`v1alpha1`, rather than carried forward as a permanent hole in the promise.

**Breaking changes move the version string.** A breaking document-format change requires a new
`apiVersion` (via graduation to `v1beta1`/`v1`, or otherwise). Nothing that pins
`launcher.gokure.dev/v1alpha1` should ever observe a breaking change without a version-string
move to signal it.

**Evolution style.** Launcher's primary audience hand-writes `app.yaml`/`kurel.yaml`/
`cluster.yaml` directly in git, rather than generating them from a higher-level API. That
places launcher's document format at the kustomize/Compose end of the versioning-precedent
spectrum, not the Kubernetes-API-graduation end: a long-lived version string, additive-only
evolution under it, deprecation warnings ahead of any removal, and — eventually — a rewrite
tool (a prospective `kurel fix`, not yet built) as the migration path, rather than a version
bump for every evolution step.

**Maturity suffix ≠ contract counter.** The `v1alpha1` suffix states the format's maturity; it
is not a contract-revision counter that increments on every additive change. Launcher
deliberately carries **no separate in-document format counter** (no `schemaVersion` field): a
counter would sit in the document envelope, where Parser Strictness above makes an unrecognised
key a build error. A **required** counter therefore fails every existing document that omits
it, which is a breaking change by the additive test above — the opposite of what it would
exist to signal. An **optional** counter does pass that test, but buys nothing: a document
carrying it is rejected by every parser released before the key existed, so no consumer could
rely on reading it without the coordinated rollout a version-string move already provides.
(That second direction is forward compatibility, not the additive test, which asks only that
previously valid documents stay valid.) The CHANGELOG's Document Format category (below)
serves the at-a-glance-scanning need instead, without touching the wire format. Revisit this
if a machine consumer ever needs to gate behavior on a format level rather than read a
changelog.

**Deprecation procedure.** A field slated for removal is documented as deprecated and continues
to be accepted for at least one minor release before being dropped. Dropping it is a breaking
change like any other, so — per "Breaking changes move the version string" above — it is
removed only alongside a version-string move; there is no pre-`v1beta1` exception.

**Changelog signal.** A commit that changes the shape or meaning of a
`launcher.gokure.dev/v1alpha1` document uses the conventional-commit scope `format`
(`feat(format):`, `fix(format):`, `docs(format):`) and is grouped under a **Document Format**
changelog heading, distinct from ordinary feature/fix entries — scannable by anyone re-pinning
their launcher dependency without reading every line.

**Compatibility-layer slot.** The reserved import/export layer described below under "Future:
OAM Compatibility" (tracked in go-kure/launcher#247) is the natural home for translating a *different*
document format (`core.oam.dev/v1beta1`) into launcher's native one; it does not change this
lifecycle discipline for `launcher.gokure.dev/v1alpha1` itself.

---

## OAM Conceptual Reuse

Launcher's native model borrows the following OAM concepts:

| OAM concept | Launcher usage |
|---|---|
| Application | Top-level kind; same structure (components, policies) |
| Component | Same shape (name, type, properties, traits) |
| Component type | Dispatches to a registered `ComponentHandler` |
| Trait | Same shape (type, properties); attached to components |
| Trait type | Dispatches to a registered `TraitHandler` |
| Policy | Present in Application spec; used for enforcement (Phase 1+) |

Concepts not adopted in Phase 0:
- OAM `WorkloadDefinition` / `ComponentDefinition` / `TraitDefinition` — launcher
  handlers are Go code, not declarative definition files (future work)
- OAM workflow semantics
- OAM revision/rollout model

---

## Future: OAM Compatibility

Support for reading `core.oam.dev/v1beta1` Applications as a launcher input format may
be added later via an import/export layer. This would allow OAM/KubeVela documents to be
used with `kurel build` without being launcher's native format. No timeline is set for
this; it is not a Phase 0 concern.
