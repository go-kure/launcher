# Launcher — Design Document

*Date: 2026-04-19 | Updated: 2026-05-14 | Status: Phase 0 design in progress (PR #58)*

---

## 1. Vision

**Launcher** is an OAM-inspired package manager for Kubernetes — a semantically richer alternative to Helm.

Where Helm templates Kubernetes manifests from Go template files and a flat `values.yaml`, launcher models deployments using OAM concepts (Applications, Components, Traits) as its package format, under launcher's own API group. The result is a tool where application structure is explicit and typed, platform implementation choices are separated from application choices, and output is always static, GitOps-ready Kubernetes manifests.

Launcher defines its own native application model under `launcher.gokure.dev/v1alpha1`. OAM is the conceptual inspiration, not the API contract — launcher does not claim native API compatibility with `core.oam.dev/v1beta1`.

Launcher uses the [kure](https://github.com/go-kure/kure) library for Kubernetes resource generation.

---

## 2. Background — Origin in kure

Launcher originated as `pkg/launcher` inside the kure library — an early prototype of a declarative, file-based Kubernetes manifest generation pipeline. That prototype demonstrated the core idea (parametric package definitions, patch-based composition) but was not yet OAM-aware and did not have a clear separation between platform and application concerns.

The decision to extract launcher into its own repository reflects two things:
1. Launcher is an *application* (a CLI tool with its own user, design space, and release cadence), not a *library component*. It does not belong inside kure.
2. The design direction is changing: the next iteration is built around OAM rather than the file-based patch pipeline of the prototype.

The following code from kure moved to this repository as the starting point:
- `pkg/launcher/` — the prototype pipeline (loader, resolver, patch processor, validator, builder)
- `pkg/patch/` — the patch engine (TOML/YAML parsing, JSONPath application, strategic merge, conflict detection)
- `cmd/kurel/` and `pkg/cmd/kurel/` — the CLI entry point and commands

---

## 3. Core Concept: The Kurel Package

A **kurel package** is a bundle of OAM specs that can be instantiated with parameters. It represents a reusable, shareable application pattern — think "a web application with ingress, TLS, and external secret injection" as a single distributable unit.

### 3.1 Package Contents

```
my-webservice/
├── kurel.yaml          # Package metadata and parameter schema
└── app.yaml            # Application template
```

`kurel.yaml` is the package's public API — it declares the parameters (name, type, default, required) and the package version. `app.yaml` is a launcher Application document. The package author defines what can be varied; consumers fill in the values.

The Application document uses `apiVersion: launcher.gokure.dev/v1alpha1`, kind `Application`. ClusterProfile documents (`cluster.yaml`) and Package documents (`kurel.yaml`) use the same API group and version. See `docs/oam/design-gvk.md` for the full GVK rationale.

### 3.2 Two Parameter Sets

Every kurel package accepts **two distinct parameter sets** at instantiation time:

**Platform profile (set 1)**

Describes *how* the platform implements each trait. These values are environment-specific, not application-specific. A team managing a cluster defines one platform profile; all applications deployed to that cluster share it.

Examples:
- Which ingress controller is in use (Nginx, Traefik, Gateway API)
- Which certificate authority backs the cert manager (Let's Encrypt, internal CA)
- Which secret store implementation is active (Vault, AWS Secrets Manager, Azure Key Vault via External Secrets)
- Which GitOps engine is in use (FluxCD, ArgoCD)

Platform profiles express the trait implementation choices made by the platform operator. The OAM spec in the package stays generic (it says "needs ingress"); the platform profile says "ingress on this cluster means a Gateway API HTTPRoute".

**Application values (set 2)**

Describes *what* this specific application instance needs. These are provided per deployment of the package.

Examples:
- Container image and tag
- Replica count
- Resource requests and limits
- Feature flags
- External secret names
- Domain names

### 3.3 Separation of Concerns

The split between platform profile and application values maps directly onto OAM's design intent:
- OAM Components define *what workloads exist* — application developer concern
- OAM Traits define *what platform capabilities to attach* — platform operator concern
- The trait *implementation* (how it works) is a platform profile concern, invisible to the application developer

When deploying multiple packages to a cluster, the platform profile is configured once per environment. Each application provides its own values. Platform changes (e.g. switching ingress controllers) update one profile; no individual application spec changes.

---

## 4. Architecture

```
┌─────────────────────────────────────────────┐
│                kurel CLI                    │
│           (launcher/cmd/kurel)              │
└──────────────────────┬──────────────────────┘
                       │
         ┌─────────────▼──────────────┐
         │     launcher runtime       │
         │  (launcher/pkg/launcher)   │
         │                            │
         │  load → resolve → patch    │
         │       → validate → build   │
         └─────────────┬──────────────┘
                       │
         ┌─────────────▼──────────────┐
         │       patch engine         │
         │   (launcher/pkg/patch)     │
         │  TOML/YAML/JSONPath/SMP    │
         └─────────────┬──────────────┘
                       │
         ┌─────────────▼──────────────┐
         │       kure library         │
         │  (github.com/go-kure/kure) │
         │  K8s builders + GitOps     │
         └────────────────────────────┘
```

Launcher generates **static Kubernetes manifests**. It does not deploy them. Consumers feed the output into a GitOps pipeline (FluxCD, ArgoCD) or apply it directly with `kubectl`.

---

## 5. Relationship to kure

Launcher is a consumer of kure, not a component of it.

| Concern | Lives in |
|---|---|
| K8s resource construction | kure (`pkg/kubernetes`) |
| GitOps engine (FluxCD, ArgoCD) | kure (`pkg/stack/fluxcd`, `pkg/stack/argocd`) |
| Kubernetes resource builders for CRD operators | kure (`pkg/kubernetes/certmanager`, etc.) |
| OAM package format and runtime | launcher |
| Parameter resolution and patch application | launcher (`pkg/patch`) |
| Two-set parameter model (platform + app) | launcher |
| CLI tool | launcher |

kure remains a standalone library with no dependency on launcher. Launcher imports kure. The dependency is one-directional.

### What stays in kure

`pkg/stack/generators/kurelpackage/` — a kure *generator* that produces kurel package structure as output from a kure Application. This is a kure concern (generating artifacts from the kure domain model), not a launcher concern.

---

## 6. Comparison with Helm

| Aspect | Helm | Launcher / Kurel |
|---|---|---|
| Package format | Go templates + values.yaml | OAM Application spec + typed parameters |
| Platform vs app config | Single values.yaml (no separation) | Two explicit parameter sets |
| Semantics | Arbitrary YAML generation | OAM components/traits (typed intent) |
| Platform customization | Via values + conditional templates | Via platform profile (trait implementation resolution) |
| Output | Manifest apply to cluster | Static manifests → GitOps delivery |
| Cluster runtime component | Tiller (Helm 2) / none (Helm 3) | None — compile-time only |
| Composability | Helm subcharts | OAM composition + patches |

---

## 7. Comparison with KubeVela

[KubeVela](https://kubevela.io/) is the reference OAM runtime. The key architectural difference:

| Aspect | KubeVela | Launcher / Kurel |
|---|---|---|
| Runtime model | Live reconciler (CRD controller in cluster) | Compiler (offline, static output) |
| Cluster dependency | Requires KubeVela CRDs installed | No cluster-side component |
| Audit trail | Live CRD state | Git history of generated manifests |
| GitOps | Via VelaUX addon or GitOps integration | Native — output is GitOps-ready |

Launcher targets teams committed to a GitOps-first workflow who want OAM semantics without a cluster-side controller.

---

## 8. Current Status and Roadmap

**Phase 0 (current): Extraction, design, and housekeeping**
- Move prototype code from kure — *done*
- Establish module structure and CI — *done*
- Design the launcher-native application model — *in progress (PR #58)*
  - GVK: `launcher.gokure.dev/v1alpha1` for Application, Package, ClusterProfile
  - ClusterProfile format (`design-cluster-profile.md`)
  - Parameter syntax options (`options-param-syntax.md`)
  - Package composition options (`options-package-composition.md`)
  - Policy interface options (`options-policy-interface.md`)
  - Package spec (`design-kurel-package.md`) — on hold until param syntax decided

**Phase 1: OAM-native package format**
- Implement the launcher Application, Package, and ClusterProfile types
- Implement parameter resolution (`${var}` placeholders or values overlay — decision pending)
- Implement ClusterProfile rendering (capability key resolution, merge semantics)
- Implement policy enforcement (`--policy` flag, `NoopPolicy`, crane compatibility)
- CLI: `kurel build` with `--profile`, `--values`, `--policy`, `--set` flags

**Phase 2: Conditional composition (issue #39)**
- Optional component and trait inclusion
- Policy-based conditionality
- Multi-instance component patterns

**Phase 3: Package distribution**
- OCI-based package publishing and pulling (similar to Helm OCI repositories)
- Package versioning

---

## 9. Pending Decisions (PR #58 — blocks final design)

The following decisions must be made and recorded before `design-kurel-package.md` and
`design-policy-interface.md` can be completed and PR #58 merged. Options documents for
each are in `docs/oam/`.

| Decision | Options doc | Blocks |
|---|---|---|
| Parameter syntax — `${var}` placeholders vs values overlay | `options-param-syntax.md` | `design-kurel-package.md` §6 |
| Package composition — how optional sections are declared and enabled | `options-package-composition.md` | `design-kurel-package.md` §4–5 |
| Policy interface — typed accessors vs opaque marker | `options-policy-interface.md` | `design-policy-interface.md` |

`design-cluster-profile.md` and `design-gvk.md` are complete and have no open decisions.
Issue #37 (ClusterProfile) can be closed once PR #58 is approved.

---

## 10. Open Questions

1. **Conditional inclusion syntax** — How the package author marks optional components and
   traits, and how the user enables/disables them, is a Phase 0 design question. Two
   options are compared in `docs/oam/options-package-composition.md`. Deeper conditionality
   (Phase 2) is tracked in issue #39.

2. **Parameter syntax** — Two options compared in `docs/oam/options-param-syntax.md`:
   - Option A: `${var}` placeholders in `app.yaml`, resolved before parse
   - Option B: `values.yaml` overlay on a static `app.yaml`
   Decision pending before `design-kurel-package.md` §6 can be completed.

3. **Policy interface** — Two options compared in `docs/oam/options-policy-interface.md`:
   - Option A: typed accessor interface (~19 methods); compiler-verified; no type assertions
   - Option B: opaque marker interface; flexible; requires type assertions in handlers
   Decision pending before `design-policy-interface.md` can be written.

4. **Platform profile format** — Resolved. `ClusterProfile` is a `cluster.yaml` file under
   `launcher.gokure.dev/v1alpha1`; it carries `rendering` values per capability type. See
   `docs/oam/design-cluster-profile.md`.

5. **Backwards compatibility with prototype** — The existing `pkg/launcher` pipeline
   (file-based, patch-centric) is superseded by the new OAM-native design. The prototype
   code remains in the repository as reference but is not the Phase 1 implementation target.
