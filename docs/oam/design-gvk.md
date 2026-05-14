# Design: API Group and Document Ownership

*Status: Draft | Prerequisite for: design-cluster-profile.md, design-kurel-package.md,
design-policy-interface.md*

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

These three documents form one coherent API family. They are not split across groups or
versions because they belong to the same ownership and lifecycle domain:
`Application` is what to run, `Package` is how it is packaged, `ClusterProfile` is how
the target platform resolves capabilities for it.

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

Launcher's component and trait types (`webservice`, `expose`, `certificate`) are
launcher-specific. No other OAM runtime understands them. The shared shape
(components/traits/properties) is a design choice, not an API contract.

**Not `wharf.zone`**

`wharf.zone` is the Wharf platform zone. Launcher is a go-kure project — an
open-source product that is not Wharf-internal. Using `wharf.zone` would make the
application model look platform-specific when it is intended to be launcher-native and
publicly usable beyond Wharf deployments.

**Not `launcher.wharf.zone`**

Same reasoning as `wharf.zone`. Embedding the Wharf zone in the group name ties the API
to the Wharf platform identity rather than to launcher as a standalone product.

**`launcher.gokure.dev`**

Reflects the actual ownership (go-kure project), keeps launcher's API separate from both
the Wharf platform APIs and the upstream OAM namespace, and is honest about what these
documents are: launcher's native input format.

---

## Parser Strictness

Launcher rejects unknown fields in all launcher-native documents. An `app.yaml`,
`kurel.yaml`, or `cluster.yaml` with unrecognised keys is a build error.

Rationale: unknown fields are most often typos or stale config carried over from a
different tool (e.g. a `cluster.yaml` migrated from crane that still contains `gitops:` or
`componentCatalog:`). Strict parsing surfaces these problems at build time rather than
silently ignoring them and producing incorrect output.

Operators migrating crane `ClusterProfile` documents to launcher `cluster.yaml` must
remove crane-specific fields before use. The fields to remove are documented in
`design-cluster-profile.md §8`.

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
