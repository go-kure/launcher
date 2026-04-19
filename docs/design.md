# Launcher — Design Document

*Date: 2026-04-19 | Status: Early design — work in progress*

---

## 1. Vision

**Launcher** is an OAM-native package manager for Kubernetes — a semantically richer alternative to Helm.

Where Helm templates Kubernetes manifests from Go template files and a flat `values.yaml`, launcher models deployments using the [Open Application Model (OAM)](https://oam.dev/) as its package format. The result is a tool where application structure is explicit and typed, platform implementation choices are separated from application choices, and output is always static, GitOps-ready Kubernetes manifests.

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
├── app.yaml            # OAM Application template (parameterized)
└── patches/            # Optional composition patches
```

`app.yaml` is a standard OAM Application document with parameter placeholders. The package author defines what can be varied; consumers fill in the values.

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

**Phase 0 (current): Extraction and housekeeping**
- Move prototype code from kure
- Establish module structure and CI
- Document what the prototype does and does not do

**Phase 1: OAM-native package format**
- Define kurel package spec (kurel.yaml schema)
- Define OAM Application template parameterization
- Define platform profile contract
- Implement parameter resolution for both sets

**Phase 2: Conditional composition**
- OAM policy-based conditional inclusion (include component X only if trait Y is present)
- Patch composition on top of OAM Application base

**Phase 3: Package distribution**
- OCI-based package publishing and pulling (similar to Helm OCI repositories)
- Package versioning

---

## 9. Open Questions

1. **Conditional inclusion syntax** — OAM does not natively support conditional sections. Proposed: use OAM `PolicyDefinition` with kurel-specific policy types to express conditionality. Needs explicit design.

2. **Platform profile format** — How do platform operators express trait implementations? Options: YAML file, OAM WorkloadDefinition overrides, capability map. Needs design.

3. **Trait resolution contract** — How does the launcher runtime map "this component requests IngressTrait" to the concrete K8s objects to generate, given a platform profile? This is the core runtime design question.

4. **Backwards compatibility with prototype** — The existing `pkg/launcher` pipeline (file-based, patch-centric) is the starting point. How much of the prototype survives into the new design, and how much is replaced?
