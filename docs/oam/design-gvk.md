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
rendering schema for a custom (non-builtin) trait or component type.

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
deliberately carries **no separate in-document format counter** (no `schemaVersion` field):
Parser Strictness above makes an unrecognised key a build error, so introducing a required
counter would itself be a breaking change to every existing document — the opposite of what it
would exist to signal. The CHANGELOG's Document Format category (below) serves the
at-a-glance-scanning need instead, without touching the wire format. Revisit this if a machine
consumer ever needs to gate behavior on a format level rather than read a changelog.

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
