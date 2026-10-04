# Launcher delivery scope: ordering, naming, Helm values and template security

*Date: 2026-10-04 | Status: Draft design, tickets filed*

This document records the target scope of the launcher library and `kurel` after a
cross-library scope review of kure, launcher and a downstream cluster engine that consumes
both. For each area it states launcher's current behaviour, with `file:line` references,
and the target behaviour of the ticket that changes it.

It supersedes §11 "Launcher Layout" of the [design document](design.md), which
go-kure/launcher#781 deleted with the `kurel` layer it described.

**Tickets.** Each target names the go-kure/launcher issue that implements it; §8 lists them
all. Each issue links back to this document.

**Basis.** "Current" means `main` at v0.2.0-beta.1. Paths are relative to the repository
root. Kure paths refer to kure v0.2.0-beta.15. Everything here is pre-release: output,
names and the library contract may change, and live-cluster upgrade effects are not a
constraint.

---

## 1. Scope: launcher is delivery-agnostic

### 1.1 Layer roles

| Layer | Role |
|---|---|
| kure | Generates Kubernetes objects from typed APIs: every base object with its full spec, layouts in directories, Flux object generation. |
| launcher library | Translates the components of **one application** into kure's model (`stack.Cluster`, `Node`, `Bundle`, `Application`). Writes no files. |
| `kurel` | The command-line tool on the library. It writes the YAML of one application. |
| A downstream consumer | Calls the launcher library and owns all delivery glue: Flux Kustomizations within and between applications, health checks, reconciliation settings, delivery annotations. It may extend launcher with its own components, traits and policies. |

### 1.2 Rules this document applies

1. **Delivery-agnostic.** The library and `kurel` add nothing specific to a delivery engine
   on top of an application: no Flux Kustomizations, health checks, reconciliation settings
   or Flux annotations. Delivery-relevant intent (ordering, prune protection, force replace)
   is carried in kure's model, not in the YAML. launcher still emits Flux objects when an
   author writes them as components (`helmrelease`, the source kinds, `helmchart`, and the
   new `fluxcd-kustomization` kind).
2. **Nothing implicit.** Ordering inside an application exists only where the author
   declares it (`placement`, `dependency`) or a component declares it about its own parts
   (`helm`: source before release). Readiness defaults to waiting for every applied
   object; explicit health checks exist only when the author asks for them.
3. **Every generated name can be overridden**, with a sane default. An author override (a
   property on one component) and a consumer override (a parameter of the library call)
   are different things; having one does not satisfy the other.
4. **Same kind, same namespace: no shared name.** The library refuses that clearly, naming
   both sources. Objects of different kinds may share a name, which is the normal way to
   show they belong together.
5. **A layer never repairs the layer below.** A gap is fixed in the library that owns it.
   launcher builds no low-level builder kure should supply.
6. **Switching delivery mode renames nothing it need not.** The Helm release name defaults
   to `<component>` under both deliveries.
7. **A security check applies on every path** that can produce the object, including
   template delivery.
8. **Siblings are consistent.** Handlers offer the same optional capabilities wherever they
   make sense. A mechanism launcher offers to consumers (such as contract metadata) is used
   by its own builtins. An asymmetry is either documented with its reason or a gap.

### 1.3 What leaves launcher (go-kure/launcher#781, go-kure/launcher#782)

| Today | Where | Target |
|---|---|---|
| Automatic health checks on every leaf bundle, from a type table | `applyAutoHealthChecks` `pkg/oam/transform.go:1858`, table `componentHealthCheckGVK` `:1799`, `isFluxControlPlaneGVK` `:1842`, the `EmitsAutoHealthCheck` veto | **go-kure/launcher#781:** removed. Default for every step is the delivery engine's wait-for-all (`wait: true`, no health checks). An author who needs explicit checks declares them through the consumer's own policy. |
| Reconciliation settings on bundles (interval, retry, timeout, prune, wait, force, suspend) | `applyReconciliationSettings` `transform.go:1909`; policy `pkg/oam/builtin/policies/reconciliation.go` | **go-kure/launcher#781:** removed with the policy. |
| `health-checks` policy | `pkg/oam/builtin/policies/healthchecks.go` | **go-kure/launcher#781:** removed. |
| `fluxcd-patches`, `fluxcd-postbuild` traits | `pkg/oam/builtin/traits/patches.go`, `postbuild.go`, `pkg/oam/bundle_patches.go` | **go-kure/launcher#781:** removed. |
| `PolicyResult.HealthCheckOverrides`, `PolicyResult.ReconciliationSettings` | `pkg/oam/pipeline.go:20-21,59-67` | **go-kure/launcher#781:** removed (breaking for a consumer that aliased them). |
| `GeneratedApplication.Patches` and the bundle-patch replay in the force warnings | `pkg/oam/in_document_collisions.go:24-27`, `force_warnings.go:121`, `force_attribution.go:62` | **go-kure/launcher#781:** removed with `bundle_patches.go`. `GeneratedApplication.Forced` loses its source (the reconciliation policy's `force`, `transform.go:1929`) and is re-sourced by go-kure/launcher#782. |
| `kurel build --oci-repository`, `--oci-tag`: a bundle-level Flux delivery layer | `pkg/cmd/kurel/delivery.go`; `pkg/cmd/kurel/README.md:159-160` and its "Flux delivery output" section; design §11 "Launcher Layout" | **go-kure/launcher#781:** removed in the same change. An engine-neutral artifact option may follow when `kurel` work resumes. |
| `force-replace`, `prune-protection` write Flux annotations (`kustomize.toolkit.fluxcd.io/force`, `.../prune`) on every object of the application | `pkg/oam/builtin/traits/forcereplace.go:17`, `pruneprotection.go:14,112` | **go-kure/launcher#782:** both traits stay, and set an engine-neutral delivery-intent field on the kure `Application` instead (kure adds the field; its Flux workflow maps it to the annotation). The PV force warning (`Transformer.WarnForcedVolumes`) reads the intent. |

What stays: `placement`, `dependency`, the tier annotation (author-declared ordering
intent), `postProcessFluxNamespace` (`transform.go:1822`), which places authored Flux
objects in the Flux namespace, and the Flux kinds as authorable components.

---

## 2. Ordering model (go-kure/launcher#783, go-kure/launcher#784)

### 2.1 Current behaviour

- **Structure choice** (`transform.go:688-694`): any `dependency` rule gives one bundle per
  component; otherwise one tier gives a flat bundle; otherwise a hierarchy of tier bundles.
- **Automatic tiers** (`pkg/oam/classify.go:47-79,88-141`): the `<domain>/tier` annotation
  first; then a rule-generated Flux source goes to `infra`; then a type table (`postgresql`,
  `cnpg-*` → `services`; `daemonset` → `infra`; the rest → `apps`). An authored Flux source
  lands in `apps`, a generated one in `infra`.
- **Names** (`transform.go:888,903-905,909-912,936`): tier bundles `<app>-<tier>` chained by
  `dependsOn` under an umbrella `<app>`; per-component bundles `<app>-<component>`, so a
  generated source becomes `<app>-<app>-source-<digest>`.
- **Per-component shape** (`buildDependencyAwareCluster`, `transform.go:926`): one bundle
  per component, children of an unnamed root node.

### 2.2 Target (go-kure/launcher#783)

1. **Remove the automatic category table** and the generated-source rule. Keep, in
   `classify.go`: `DefaultDomain` (`:13`), `TierAnnotationKey` (`:38`),
   `ComponentLabelKeyForDomain` (`:42`) and the annotation read inside
   `ClassifyComponentWithDomain`. Ordering comes only from:
   - the `placement` policy;
   - the tier annotation, as an author declaration;
   - the `dependency` policy;
   - a lowering rule's declaration about its own parts (below).
2. **A lowering result can declare order between its own lowered parts.** `helm` uses it:
   source before release.
3. **One shape.** One application bundle. It is flat when nothing is ordered. Otherwise it
   has ordered child groups, computed as topological levels of the declared order. This
   replaces the per-component shape.
4. **Generated sources** sit in the application bundle itself, ahead of its ordered groups,
   so a shared source adopted by a later component is still applied first. The application
   owns them (see §3.4 on labels). launcher expresses this through the shape. Applying the
   bundle's own applications before its child groups is the delivery engine's job.

   A bundle with its own applications plus child groups is new for launcher. Three walks
   skip the own applications of a bundle that has children today, and must visit them:
   - `walkLeafBundle` (`transform.go:1951-1962`): without it `postProcessFluxNamespace`
     does not reach the generated sources. A generated source is placed in the Flux
     namespace when one is set;
   - `generateBundle` (`pkg/oam/in_document_collisions.go:93-100`): without it
     `GenerateApplications` drops them from `kurel` output, the collision check and the
     volume warnings;
   - `rejectLayoutAugmentersInBundle` (`pkg/cmd/kurel/build.go:459-466`).
5. **Decide in the ticket:** the placement vocabulary (keep `infra`/`services`/`apps`, or
   named groups) and the default child group names (§3).

### 2.3 Target (go-kure/launcher#784): `oci` lowers to kind components

- New kind component `fluxcd-kustomization`: one Flux Kustomization, with strict decode of
  the upstream `KustomizationSpec` (the pattern of `helmrelease`,
  `pkg/oam/builtin/components/helmrelease.go:167-168`). The type name follows the naming
  decision in [go-kure/launcher#352](https://github.com/go-kure/launcher/issues/352).
- `oci` becomes an upper-level component that lowers into `ocirepository` plus
  `fluxcd-kustomization`. The `OCIHandler` kind goes.
- `oci` today misses most of the Kustomization spec (`patches`, `postBuild`, `force`,
  `dependsOn`, `timeout`, `serviceAccountName` and more). The new kind closes that gap.
- Keep the explicit-registry rule for a non-empty allowlist (`oci.go:258-279`) on the
  `ocirepository` path.
- `SourceDeduplicatable` (`pkg/oam/handler.go`) has one implementer today, `OCIConfig`
  (`oci.go:287`). After go-kure/launcher#784 it has none: remove it, or document why it stays.
- **Decide in the ticket:** the name of a source shared by two `oci` components. Today the
  first component's name is kept (`deduplicateSourceRefs`, `transform.go:1412-1429`), while
  `helm` names a generated source `<document>-source-<digest>`.
- The name of an authored Kustomization against a delivery Kustomization a consumer
  generates is a kure check, not launcher's.

---

## 3. Naming and overrides (go-kure/launcher#785, go-kure/launcher#787, go-kure/launcher#788, go-kure/launcher#792, go-kure/launcher#793)

### 3.1 Current behaviour

- **Consumer knobs:** `ClusterID`, `Namespace`, `FluxNamespace`, `Domain`,
  `ComponentLabelKey`. No hook for any object, application, bundle or source name.
  `LoweringContext.Namer` is a concrete `*NameAllocator` built inside the engine
  (`pkg/oam/lowering.go:276-284`).
- **No author override** exists for: the `postgresql` pooler name (`<cluster>-pooler`),
  generated Helm source names (`<document>-source-<digest>`), the values ConfigMap name
  (`helm.go:518-520`),
  bundle names, `scaler` HPA/PDB (`<c>-hpa`, `<c>-pdb`), `rbac` object names (`<c>`), the
  `networkpolicy` trait object (`<c>-allow`), synthesized NetworkPolicies
  (`<c>-allow-ingress-traffic` and others), Helm hook-group child layouts, and the template
  release name.
- **Object name = component name** for every kind component. A Service named like its
  StatefulSet is only reachable through `passthrough` or `manifests`: two kind components
  `app` are refused as a duplicate component name (`pkg/oam/validate.go:247`).
- **Shortening is per site:** `ComponentLabelValue` (52 + 10 hex), the values ConfigMap and
  hook-group children (253, 8 hex), and the Namer, which never shortens and fails on an
  over-length name (`lowering.go:495-501`). CNPG and Service names are validated, never
  shortened.
- **Collisions:** `CheckInDocumentCollisions` refuses a (group, kind, namespace, name)
  generated by two different generated applications of one document. A repeat inside a
  single generated application is not detected (`in_document_collisions.go:182-184`).
  Hook-group child names are not unique across two applications with a same-named
  component (documented in `helmtemplate_render.go:264-268`).

### 3.2 Target (go-kure/launcher#787): name overrides

- **Author:** an override property for every generated name that has none (the list above),
  plus an object name separate from the component name. The latter allows a Service named
  like its StatefulSet as kind components (rule 4 permits different kinds to share a name).
- **Consumer:** an optional naming hook on `TransformContext`, keyed by owning component and
  role, falling back to the default.
  - The `Namer` (`lowering.go:383-501`) consults it for lowering-rule names.
  - Most names above are **not** built by the Namer: trait objects (`scaler`, `rbac`,
    `networkpolicy`), synthesized NetworkPolicies, the values ConfigMap, hook-group
    children and bundle names. The hook must reach each of these sites. The ticket lists
    the roles.
  - An override, from an author property or from the hook, is never shortened. It is
    validated for its target and refused when invalid or too long, and collision-checked
    as a default name is. Only launcher's own defaults go through the shortening helper
    (§3.3).

### 3.3 Target (go-kure/launcher#792, go-kure/launcher#793): uniqueness and shortening

- **go-kure/launcher#792:** hook-group child layout names include the application, so they are unique across
  applications (`hookGroupChildName`, `helmtemplate_render.go:711`, called at `:281`).
- **go-kure/launcher#793:** one shortening helper, prefix plus hash, with a documented limit passed by the
  caller (63, 253, or 53 for a Helm release name). Every name launcher generates by default
  uses it; an override never does (§3.2). The Namer shortens instead of failing. One
  documented exception: at limit 53 for a Helm release
  name, the helper reproduces Flux's shortening algorithm, so a template-rendered release
  is named as Flux would name it.

### 3.4 Target (go-kure/launcher#788): component label and provenance

- **Today:** launcher never stamps `<domain>/component`. It is only a NetworkPolicy
  selector key (`ComponentLabelKey`, else the domain's key, `transform.go:711-714`), so a
  synthesized NetworkPolicy can select a label present nowhere in the output.
  Chart-rendered pods carry chart labels only.
  `GeneratedApplication.Component` maps trait sub-applications and sibling groups to their
  component; the pooler, database, generated sources and synthesized NetworkPolicies
  report themselves (`in_document_collisions.go:17-19`).
- **Target:** launcher stamps `<ComponentLabelKey>: ComponentLabelValue(c)` on every object
  and pod template a component owns.
  - Chart output under Flux delivery: through a post-renderer on the HelmRelease. Flux
    post-renderers carry kustomize patches, so pod-template labels need one patch per
    workload kind.
  - Chart output under template delivery: labels added to the rendered objects.
  - `GeneratedApplication.Component` is filled for the pooler, database and NetworkPolicies.
  - A shared generated source is owned by the application: it carries no component label.

---

## 4. Helm and values (go-kure/launcher#785, go-kure/launcher#786)

### 4.1 Current behaviour

| Path | Release name | Values |
|---|---|---|
| `helm` or `helmrelease`, Flux delivery, no Flux namespace | No `spec.releaseName`. Flux defaults it to the HelmRelease name, `<component>`. | Inline `values`. On `helm` only, `valuesMode: configMap`: a ConfigMap `<component>-values-<hash>`, prepended to `valuesFrom` (`pkg/oam/builtin/components/helm.go:418-430`); `helmrelease` refuses the key (`helmrelease.go:147-149`). `valuesFrom` references to out-of-band ConfigMaps or Secrets work. |
| Same, Flux namespace set | The HelmRelease moves to the Flux namespace and launcher defaults `targetNamespace` to the app namespace (`helmrelease.go:358-359`). Flux then computes `<appns>-<component>`. | As above. The `helm` values ConfigMap follows the HelmRelease into the Flux namespace. |
| `helm` with `delivery: template`, or `helmtemplate` | kure's fixed default `release` as `.Release.Name`; the chart's templates decide the object names. `releaseName` is refused (`helm.go:42`). | Inline values, rendered into the output. `valuesFrom` is refused: a build cannot read a cluster object. |

No path writes explicit values into a Secret.

### 4.2 Target (go-kure/launcher#785): release name

- Default release name `<component>` under both deliveries, written explicitly:
  `spec.releaseName` on every HelmRelease, and the template render's release name, as in
  [go-kure/launcher#778](https://github.com/go-kure/launcher/pull/778).
- The `helmrelease` kind sets the default, so a directly authored `helmrelease` gets it
  too, as the kind already defaults `targetNamespace` (`helmrelease.go:358-359`).
- Shortened with the shortening helper of go-kure/launcher#793 at 53 characters, which
  reproduces Flux's own algorithm for that limit (§3.3).
- The author's `releaseName` overrides it under both deliveries. The consumer override
  comes from go-kure/launcher#787.

### 4.3 Target (go-kure/launcher#786): Secret values

- A property for marked sensitive values (proposed name `secretValues`), on the `helm`
  component only. The `helmrelease` kind decodes its properties strictly as a
  `HelmReleaseSpec` and takes no `valuesMode` (§4.1), so it gets no `secretValues` either:
  an author using the kind writes the Secret as its own component and references it in
  `valuesFrom`.
- **Flux delivery:** a Secret `<component>-secret-values-<hash>` in the HelmRelease's
  namespace (it follows the Flux namespace as the values ConfigMap does), referenced in
  `valuesFrom` ahead of the authored entries, mirroring the ConfigMap mode.
- **Template delivery:** merged into the render values.
- launcher has no Secret kind or secret trait today. The ConfigMap mode emits its object
  through a synthesized `configmap` trait. go-kure/launcher#786 needs an equivalent Secret path; it can reuse
  the Secret kind of go-kure/launcher#790.
- **Decide in the ticket:** the `valuesFrom` order when both `valuesMode: configMap` and
  `secretValues` are set, and whether a key present in both `values` and `secretValues` is
  refused.
- **Documented limit:** a Secret in the output is base64, not encrypted. A consumer that
  forbids explicit secrets by policy requires a reference to an out-of-band Secret instead.
  Under template delivery, out-of-band secrets go through the chart's own
  `existingSecret`-style values.

---

## 5. Security on template delivery (go-kure/launcher#791)

### 5.1 Current behaviour

| Check | Flux delivery | Template delivery |
|---|---|---|
| Registry allowlist | Chart source host, on the generated or authored Flux source (`helmrepository.go:110-115`). Chart images: not checked (`HelmReleaseConfig.ApplyPolicy` is a no-op). | **None.** The chart URL is fetched at build time unchecked. `HelmTemplateConfig.ApplyPolicy` is a no-op (`helmtemplate.go:177-179`). |
| Image reference check (`ValidateImageRef`, `common.go:37-60`) | No | No |
| Pod security (privileged, host namespaces, hostPath, capabilities; `pkg/oam/policy.go:32-37`, `enforce.go:115-260`) | No | No. A chart rendering a privileged pod is not refused. |
| Namespace | HelmRelease and source in the Flux namespace | Whatever `metadata.namespace` the chart writes; nothing stamps or checks it. |

`kurel` sets no `Policy`, so `NoopPolicy` applies and the registry allowlist is empty on
every path (`pkg/cmd/kurel/build.go:190-195`, `transform.go:551-552`). Only a library
consumer that passes a `Policy` gets it.

### 5.2 Target (go-kure/launcher#791)

1. **Decode** chart output with kure's parser (`ParseYAMLWithOptions` with
   `AllowUnstructured`) instead of `decodeKubeManifests` (`helmtemplate_render.go:542-573`),
   so registered kinds are typed.
2. **Chart URL allowlist:** `HelmTemplateConfig.ApplyPolicy` checks the chart URL host
   against `AllowedRegistries`, as the source kinds do.
3. **Rendered workloads** go through the existing image and pod-security enforcement.
   - `ApplyPolicy` runs before `Generate` (`transform.go:796`), while the render is lazy.
     The check must trigger the render, or move into `Generate`.
   - Only the image, securityContext and volume helpers in `enforce.go` take Kubernetes
     types. The host-namespace, host-process and resource helpers take launcher's own
     config types, so they need a variant over `corev1.PodSpec`.
   - **Decide in the ticket:** whether `ValidateImageRef` (tag or digest required, no
     `:latest`) applies to chart images. Recommendation: yes, so a rendered workload is
     held to the same image rule as a launcher-built one.

---

## 6. Contract metadata and kind coverage (go-kure/launcher#789, go-kure/launcher#790)

### 6.1 Target (go-kure/launcher#789): contract metadata

- **Today:** no builtin implements `ContractDescriber` (`pkg/oam/handler.go:100-131`), so
  `HandlerContracts()` returns empty maps. A missing co-registration is found only when a
  lowered component is dispatched, and the message (`transform.go:751`, `:1227`) names the
  type, not the authored component or the rule.
- **Target:**
  - every builtin implements `ContractDescriber`;
  - `HandlerContractSet` has only `Components` and `Traits` today
    (`transform.go:296-299`); policies need a third map;
  - lowering rules declare their target kinds (new optional `LoweringTargets()`);
  - a rule whose targets are not registered is refused, naming the rule and the kind.
- `kurel` registers component lowering rules before trait handlers
  (`pkg/cmd/kurel/build.go:385-420`), and `webservice` emits the `topology-spread` trait. A
  check at registration time would refuse that valid order. The check runs once the
  registry is complete: at the first `Transform`, or at an explicit seal.

### 6.2 Target (go-kure/launcher#790): full spec and the full set of kinds

- **Field gaps** in the hand-parsed kinds (upstream fields with no schema key):
  - `statefulset`: `tolerations`, `topologySpreadConstraints`;
  - `daemonset`: `affinity`, `topologySpreadConstraints`;
  - `job`, `cronjob`: `affinity`, `tolerations`, `topologySpreadConstraints`;
  - all workloads: `imagePullPolicy` and further container and pod fields;
  - `service`: `ExternalName` (refused today, `service.go:48-55,75-79`), traffic policies,
    load-balancer fields;
  - `persistentvolumeclaim`: `dataSource`, `dataSourceRef`, `selector`, `volumeName`.

  `selector` is refused today with an explicit reason on `deployment`, `statefulset`,
  `daemonset` and `job`, and `template` on `deployment` and `job`
  (`deployment_spec.go:52-53`, `statefulset_spec.go:59`, `daemonset_spec.go:48`,
  `job.go:44-46`); each kind's sub-task decides whether that refusal stays, with its
  reason documented. Each kind gets a sub-task in the ticket.
- **Missing kinds:** Secret, Namespace, ServiceMonitor, PersistentVolume, Pod, LimitRange,
  ResourceQuota, and the kinds reachable only as traits today (Ingress, HTTPRoute,
  Certificate, ExternalSecret, HPA, PDB, NetworkPolicy, CiliumNetworkPolicy, Role and
  RoleBinding, ReplicationSource). The inventory is derived from kure's generated
  `Create<Kind>` set and the supported CRD kinds. A kind judged not authorable is listed
  with its reason. A kind kure lacks is added to kure first.

---

## 7. Asymmetries (go-kure/launcher#794)

Fix or document each:

- `oci` sets no `targetNamespace` default; `helmrelease` does under a Flux namespace
  (`oci.go:44`).
- No `ApplyPolicy` on `service`, `serviceaccount`, `configmap`, `passthrough`. (`crd` has
  one through `manifestConfig`, `crd.go:34`, `manifestsource.go:435-442`.)
- Pod-template labels (`PodTemplateLabels`) only on `deployment`, not on `statefulset`,
  `daemonset`, `cronjob`, `job`.
- Template output keeps whatever namespace the chart writes; launcher neither stamps nor
  checks it (§5.1).

`kurel`'s global `-f/--output-file`, which `build` ignores, is filed separately as
go-kure/launcher#795 and deferred with the rest of the `kurel` CLI work.

---

## 8. Ticket index

| Issue | Ticket | Section | Needs |
|---|---|---|---|
| [go-kure/launcher#781](https://github.com/go-kure/launcher/issues/781) | Remove Flux delivery fields from library output, and `kurel`'s delivery mode | §1.3 | — |
| [go-kure/launcher#782](https://github.com/go-kure/launcher/issues/782) | Delivery intent instead of Flux annotations | §1.3 | [go-kure/kure#974](https://github.com/go-kure/kure/issues/974) (delivery intent), go-kure/launcher#781 |
| [go-kure/launcher#783](https://github.com/go-kure/launcher/issues/783) | Explicit ordering only; one bundle shape | §2.2 | go-kure/launcher#781; go-kure/launcher#787 for the name override (can follow) |
| [go-kure/launcher#784](https://github.com/go-kure/launcher/issues/784) | `oci` as an upper-level component; new `fluxcd-kustomization` kind | §2.3 | — |
| [go-kure/launcher#785](https://github.com/go-kure/launcher/issues/785) | Release name default (rescopes [go-kure/launcher#776](https://github.com/go-kure/launcher/issues/776)) | §4.2 | go-kure/launcher#793 |
| [go-kure/launcher#786](https://github.com/go-kure/launcher/issues/786) | Secret values | §4.3 | go-kure/launcher#790 (Secret kind) |
| [go-kure/launcher#787](https://github.com/go-kure/launcher/issues/787) | Name overrides | §3.2 | go-kure/launcher#783, go-kure/launcher#793 |
| [go-kure/launcher#788](https://github.com/go-kure/launcher/issues/788) | Component label and provenance | §3.4 | — |
| [go-kure/launcher#789](https://github.com/go-kure/launcher/issues/789) | Contract metadata | §6.1 | — |
| [go-kure/launcher#790](https://github.com/go-kure/launcher/issues/790) | Full spec and full set of kind components | §6.2 | [go-kure/kure#981](https://github.com/go-kure/kure/issues/981) (missing constructors), go-kure/launcher#787 |
| [go-kure/launcher#791](https://github.com/go-kure/launcher/issues/791) | Security on template delivery | §5.2 | — |
| [go-kure/launcher#792](https://github.com/go-kure/launcher/issues/792) | Hook-group child names unique across applications | §3.3 | go-kure/launcher#793, go-kure/launcher#787 |
| [go-kure/launcher#793](https://github.com/go-kure/launcher/issues/793) | One shortening rule | §3.3 | — |
| [go-kure/launcher#794](https://github.com/go-kure/launcher/issues/794) | Asymmetries | §7 | go-kure/launcher#783, go-kure/launcher#784, go-kure/launcher#788 |
| [go-kure/launcher#795](https://github.com/go-kure/launcher/issues/795) | `kurel build` ignores the global `-f/--output-file` (deferred) | §7 | — |
