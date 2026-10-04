# Design: API Group and Document Ownership

*Status: Final | Prerequisite for: design-cluster-profile.md, design-kurel-package.md,
options-policy-interface.md*

| Version | Date | Summary |
|---|---|---|
| 1.0 | 2026-05-14 | Initial — records GVK decision, rationale, strictness rule, OAM reuse |
| 1.1 | 2026-08-23 | Adds the type-name reservation covenant and document-format lifecycle |
| 1.2 | 2026-09-23 | Adds the pre-release bug-fix exception to the document-format lifecycle |
| 1.3 | 2026-09-26 | Adds the two-axis type model (terminal/lowerable, kind-named/role-named) and the naming rule; OAM-reuse table covers lowering |
| 1.4 | 2026-10-02 | Lifecycle rules take effect at the first stable GA release; before it, no format stability promise (replaces the pre-release bug-fix exception) |

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
| `environments.yaml` next to `app.yaml`, or passed via `--environments` | `launcher.gokure.dev/v1alpha1` | `EnvironmentSet` |

These documents form one coherent API family. They are not split across groups or
versions because they belong to the same ownership and lifecycle domain:
`Application` is what to run, `Package` is how it is packaged, `ClusterProfile` is how
the target platform resolves capabilities for it, `CapabilityDefinition` declares the
rendering schema for a custom (non-builtin) trait type, and `EnvironmentSet` names the
profile+values pairs `kurel build --environment` selects between. `EnvironmentSet` is a
deployer input read only by the `kurel` CLI, not part of a package; its format is
described in the kurel CLI reference's
[Named environments](https://github.com/go-kure/launcher/blob/main/pkg/cmd/kurel/README.md#named-environments)
section.

### Example document headers

```yaml {check="snippet"}
# app.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
```

```yaml {check="snippet"}
# kurel.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: webservice
  version: "1.0.0"
```

```yaml {check="snippet"}
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
| Shadowed (same name, downstream superset) | `prune-protection` | Launcher builtin; the downstream implementation carries additive behaviour on top. Upstreaming that delta — and retiring this shadowed class — is tracked in go-kure/launcher#245. |
| Former builtin (downstream-used) | `helmchart` | The role-level composite of this name was removed by go-kure/launcher#350; since go-kure/launcher#351 the name is the kind-named component for the Flux `HelmChart` CR, and a downstream dialect using the name renames its type before adopting that release. |

**Enforcement is deliberately review discipline, not CI.** An extending dialect commonly lives
in a separate, non-public project that launcher's CI cannot see or gate against; a downstream
dialect statement (its own `extends` / reserved-types / shadowed-types declaration, where one
is published) is informational input to review, not an automated check. This is revisitable if
launcher gains outside contributors who cannot be expected to know the covenant by convention
alone.

---

## Two Axes of a Type, and the Naming Rule

Every component and trait type sits on two independent axes. The first is a tier boundary
the engine enforces; the second is a naming convention only.

**Axis A — terminal vs lowerable.** A *terminal* type is served by a dispatchable handler
(`RegisterComponent` / `RegisterTrait`) that generates or modifies the rendered configuration
itself, with no further OAM lowering: it emits or modifies Kubernetes objects.
A *lowerable* type is served by a
lowering rule (`RegisterComponentLowering` / `RegisterTraitLowering`) and emits other OAM
entries, never objects. Which entries depends on the rule's position: a component rule emits
components and policies, a trait rule traits, components and policies
(`loweringPositionRules` in `pkg/oam/lowering.go`). The lowering fixpoint expands it until
only terminal types remain (see `design-lowering-engine.md`). A type is never both:
registration keeps terminal and lowerable ownership of a name mutually exclusive, and each
registration path panics when the name is already claimed on the other side, because "a
lowerable type must not also be terminal" (`pkg/oam/transform.go`, `pkg/oam/lowering.go`).
The builtin example today is the `expose` trait, which lowers into a terminal `ingress` or
`httproute` trait.

**Axis B — kind-named vs role-named.** A *kind-named* type projects exactly one Kubernetes API
kind as that kind, adding no role-specific opinions, and is named after it: the `deployment`
component, the `ingress`, `httproute` and `networkpolicy` traits. Two things do not change
that. Supporting objects the kind needs (the `deployment` component's ServiceAccount, an
optional PVC) are one. Defaults and safety constraints shared by every type that projects the
same kind are the other: the guard that refuses more than one replica and forces a `Recreate`
strategy when a non-RWX PVC is attached applies to `deployment`, `webservice` and `worker`
alike. A *role-named* type is named for the job it does: it combines kinds as peers
(`webservice`: Deployment plus Service), chooses between them (`expose`: an `ingress` or an
`httproute`), or emits one primary kind shaped by launcher's opinions about that job
(`worker`: a Deployment with a default topology spread and an `affinity` shorthand, both of
which `deployment` deliberately leaves out; see `pkg/oam/builtin/components/README.md`). The
number of kinds emitted does not decide the axis; whether the type projects the API kind or a
role does. Nothing checks this axis; it binds naming choices, like the covenant above.

The axes are orthogonal. `deployment` and `webservice` are both terminal, one kind-named and one
role-named; `expose` is lowerable and role-named. The layering the Helm-family redesign uses
(go-kure/launcher#336) is a role-named lowerable upper tier (`helm`) over kind-named terminals
(`helmrelease`, `helmrepository`, `ocirepository`, …).

**Naming rule.** A new type name is chosen as follows:

1. A type that projects exactly one API kind, without role-specific opinions, takes that
   kind's name in lowercase:
   `Deployment` → `deployment`, `HTTPRoute` → `httproute`.
2. Any other type takes a role name.
3. A vendor prefix, `<vendor>-<kind>`, is added only when the bare kind name is already taken
   by something a reader would plausibly mean instead. Precedent: `cilium-networkpolicy`
   alongside core `networkpolicy`. Under this rule the Flux `Kustomization` CR becomes
   `fluxcd-kustomization`, because bare `kustomization` collides with `kustomization.yaml`.

A name this rule produces is still subject to the reservation covenant above. One former
builtin predated the rule: `helmchart` was a role-level composite (a HelmRelease plus its
source, or client-side rendered manifests), not a projection of the Flux `HelmChart` CR its
name suggests. It was removed in favour of the role-named `helm` (go-kure/launcher#350)
without a document-format version move, which the Document-Format Lifecycle below does not
require before the first stable GA release. go-kure/launcher#351 reuses the name for the
`HelmChart` CR (maintainer decision, 2026-10-02), so `helmchart` now follows rule 1. A document
written for the composite fails validation on a key the HelmChart spec does not declare;
when the refused key is one of the composite's own (`source`, `values`, `delivery`, …) the
error adds a pointer to `helm`.

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
`KnownFields(true)` (`ParseWithExtraTypes`, `pkg/oam/parser.go`). A misspelled `replicaz` at
the component level, or a stray `spec.traits`, fails there.

**Authored `properties` maps** are not covered by that decoder. `Component.Properties`,
`Trait.Properties` and `ApplicationPolicy.Properties` (`pkg/oam/types.go`)
are each `map[string]any`, so YAML strictness stops at the envelope and any key at all decodes
successfully. Those maps are instead checked against the handler's own declared
`PropertySchema` by `Transformer.ValidateAuthoredPropertiesWithCapabilities`, which the build
calls after parsing and evaluating the profile (`pkg/cmd/kurel/build.go`). For a policy, the handler is the `PolicyHandler`
registered for its type, or the `PolicyLoweringRule` claiming it; the built-in handlers
`kurel build` registers (`dependency` and `placement`, in
`pkg/oam/builtin/policies`) each declare one. A policy type with nothing registered for it is
not checked here and fails the transform with `no handler for policy type`. An undeclared key
is a build error naming the allowed fields; a declared key whose value has the wrong type is a build error too. An array- or
object-typed value that validation had to rebuild in order to check it — a typed Go `[]string`
or `map[string]string` normalised into `[]any`/`map[string]any` — is written back in place, so
the handler downstream sees the shape that was actually checked. Scalars a library caller or a
lowering rule supplies in a Go type the readers do not assert are rewritten too: a `string`,
`boolean` or `number` value of a named Go type (`type Mode string`) is written back as its
predeclared type, and an `integer`-typed value of kind `int8`, `int16`, any unsigned kind, or of
any named integer type (`type Replicas int32`) is written back as `int`. A value outside the
`int` range is a build error rather than a silently defaulted field. The
one YAML shape this reaches is an integer literal above the int64 maximum, which yaml.v3 decodes
to `uint64`: it is now rejected at build instead of being dropped by the reader. Compound `Enum`
members are compared element by element with the same exact numeric equality as scalar members, so a
member declared with `uint16(80)` still matches the normalised value. `int`, `int32`, `int64` and
an integral `float64` are left as they are when their type is exactly that one; a named integer
type becomes `int` rather than its underlying type because a reader such as `servicePort`'s accepts
`float64` and `int` only. A key an open object leaves undeclared is not rewritten, and a schema that
declares no `Type` at all is checked only against its `Enum`, if it has one. A quantity or an
int-or-string leaf declares a `Types` union instead (go-kure/launcher#383): the value is accepted
when any member's single `Type` accepts it, and is normalised by the first member, in declared order,
that does.

**Ordering is load-bearing.** The authored-properties check runs *after* `ResolveParameters`
(`pkg/cmd/kurel/build.go`). In package mode an authored value may be a `${...}` placeholder,
which is a bare string until substitution; type-checking before substitution would reject a
document whose integer- or boolean-typed property is supplied by a parameter.

### One carve-out

**Trait types declared by a `CapabilityDefinition`.** A definition supplied via
`--capability-def` declares that a trait type *exists*; it does not declare what properties that
type accepts. With no `PropertySchemaProvider` behind it, such a trait's handler-owned properties
are left unchecked rather than rejected wholesale. When the trait is served by a registered handler
or lowering rule that declares no schema, the engine-owned `scope` (below) is still type-checked:
that key is not the handler's to declare, so a handler that declares no schema cannot opt it out
of being a string. A trait type with no registered handler or lowering rule at all is not
checked here; it has nothing to apply it and fails later in the build.

### `scope` is declared by the engine, not by a handler

Not a carve-out — a property whose schema lives somewhere other than a handler. `scope` selects
which `ClusterProfile` capability binding a trait resolves against: `buildCapabilityKey`
(`pkg/oam/transform.go`) builds the key `"<traitType>.<scope>"` for **every** trait type,
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
ahead of the type switch, `pkg/oam/property_validate.go`), and resolves against the unscoped
binding exactly as omitting the key does.

### Required fields are checked at nested levels only

`Required` on a *top-level* authored property is deliberately not enforced at parse time. A
trait's top-level property map is merged with the ClusterProfile's capability rendering after
parsing (`applyTraits` → `resolveCapability`, `pkg/oam/transform.go`), so a
capability-aware trait may legitimately author a document in which the platform, not the
author, supplies a required property. Enforcing `Required` before that merge would reject it.
Nested `Required` — inside an object- or array-typed property — *is* enforced. Capability
rendering merges into nested objects too (go-kure/launcher#750), so on a trait a capability
binding matches it is enforced on the merged properties: by
`ValidateAuthoredPropertiesWithCapabilities`, and by `Transform` at both merge sites
(go-kure/launcher#765). A partial nested override that relies on the rendering for a required
sibling is therefore accepted. A required key inside an array element is checked as written,
because a rendering never merges into a list.

---

## Document-Format Lifecycle

`launcher.gokure.dev/v1alpha1` names a document *format* for `app.yaml`, `kurel.yaml`,
`cluster.yaml`, any `CapabilityDefinition` document, and an `EnvironmentSet` document. This section states what stays true
while that string is unchanged, and what must change it, from launcher's first stable GA
release on ("When these rules apply" below) — binding on launcher itself, and
relied on by any consumer that pins the version string, including a dialect that declares
itself as extending it.

**When these rules apply.** Launcher has not yet had a stable GA release; the
`v0.1.0-alpha.N` pre-release tags are not one. The stability promise, the additive test,
"Breaking changes move the version string" and the deprecation procedure below take effect at
launcher's first stable GA release (maintainer ruling 2026-10-02 on go-kure/launcher#666).
Until then `launcher.gokure.dev/v1alpha1` carries no stability promise: a fix, a feature or a
removal may change the output of an existing document, or newly reject one, with no
version-string move and no deprecation period. Such a change uses the `format` commit scope
(`fix(format)`, `feat(format)`) so the Document Format changelog heading lists it (see
"Changelog signal" below), its commit subject names the affected kinds or properties, and its
PR description discloses the output change.

**Stability promise (from the first stable GA release).** While a version string is current,
every document that was valid under it remains valid, and compiles to the same output. For
`launcher.gokure.dev/v1alpha1` the baseline is the set of documents the first stable GA release
accepts, and the output it compiles them to; a document only an earlier pre-release accepted
is not covered.

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
go-kure/launcher#408 closed the gap before the first stable GA release: a document that
authored a key no handler declares was accepted before and is a build error now. It never
compiled to the output its author intended — the key was dropped — and the change was taken
under `v1alpha1` while no stability promise applied, so the promise starts without that hole.

**Breaking changes move the version string (from the first stable GA release).** From that
release on, a breaking document-format change requires a new `apiVersion` (via graduation to
`v1beta1`/`v1`, or otherwise). Nothing that pins the then-current version string should
observe a breaking change without a version-string move to signal it. Before it, a breaking
change ships under `v1alpha1`, signalled by its `format` changelog entry and its PR description
("When these rules apply" above).

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
(That second direction is forward compatibility, which the additive test does not ask about at
all: it looks only at how documents that were already valid fare under a newer parser.) The
CHANGELOG's Document Format category (below) serves the at-a-glance-scanning need instead,
without touching the wire format. Revisit this if a machine consumer ever needs to gate behavior
on a format level rather than read a changelog.

**Deprecation procedure (from the first stable GA release).** A field slated for removal is
documented as deprecated and continues to be accepted for at least one minor release before
being dropped. Dropping it is a breaking change like any other, so — per "Breaking changes move
the version string" above — it is removed only alongside a version-string move. Before that
release deprecation is optional: an item may be removed under `v1alpha1` with no deprecation
period and no version-string move.

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
| Component type | Lowered by a registered `ComponentLoweringRule`, or dispatched to a registered `ComponentHandler` once lowering settles (see Two Axes of a Type) |
| Trait | Same shape (type, properties); attached to components |
| Trait type | Lowered by a registered `TraitLoweringRule`, or dispatched to a registered `TraitHandler` once lowering settles (see Two Axes of a Type) |
| Policy | Same shape (name, type, properties) in `spec.policies`; lowered by a registered `PolicyLoweringRule`, or dispatched to a registered `PolicyHandler` (built-ins: `dependency`, `placement`) |

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
