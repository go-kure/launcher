# Launcher delivery scope: ordering, naming, Helm values and template security

*Date: 2026-10-04 | Status: Design, tickets filed; §8 says which have shipped*

This document records the target scope of the launcher library and `kurel` after a
cross-library scope review of kure, launcher and a downstream cluster engine that consumes
both. For each area it states launcher's current behaviour, naming the symbol and the file
that hold it, and the target behaviour of the ticket that changes it. A reference to code
that a shipped ticket removed keeps the `file:line` it had before that change.

It supersedes §11 "Launcher Layout" of the [design document](design.md), which
go-kure/launcher#781 deleted with the `kurel` layer it described.

**Tickets.** Each target names the go-kure/launcher issue that implements it; §8 lists them
all. Each issue links back to this document.

**Basis.** "Current" means `main` after v0.2.0-beta.1, with the tickets §8 marks shipped.
Paths are relative to the repository root. Kure paths refer to kure v0.2.0-beta.15, the
version `go.mod` pins. Everything here is pre-release: output, names and the library
contract may change, and live-cluster upgrade effects are not a constraint. A section or
row marked **Shipped** states what the code does since its ticket merged, in place of the
target it replaced; one marked **Target** is not in the code.

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

### 1.3 What leaves launcher (go-kure/launcher#781 shipped, go-kure/launcher#782 open)

The rows of go-kure/launcher#781 are shipped: "Now" is what the code does, and "Where it
was" names the code before that change, most of which is gone. The last row is still a
target.

| Before | Where it was | Now |
|---|---|---|
| Automatic health checks on every leaf bundle, from a type table | `applyAutoHealthChecks` `pkg/oam/transform.go:1858`, table `componentHealthCheckGVK` `:1799`, `isFluxControlPlaneGVK` `:1842`, the `EmitsAutoHealthCheck` veto | **Shipped (go-kure/launcher#781):** removed. Launcher sets no health check and no other delivery field on a bundle (`buildCluster`, `pkg/oam/transform.go`); what a step waits for is the delivering consumer's. An author who needs explicit checks declares them through the consumer's own policy. |
| Reconciliation settings on bundles (interval, retry, timeout, prune, wait, force, suspend) | `applyReconciliationSettings` `transform.go:1909`; policy `pkg/oam/builtin/policies/reconciliation.go` | **Shipped (go-kure/launcher#781):** removed with the policy. A document with a `reconciliation` policy fails the transform with `no handler for policy type "reconciliation": it configures delivery, which launcher leaves to the consumer that delivers the application; …`, unless the consumer registers its own handler. |
| `health-checks` policy | `pkg/oam/builtin/policies/healthchecks.go` | **Shipped (go-kure/launcher#781):** removed, and refused the same way without a consumer's handler. |
| `fluxcd-patches`, `fluxcd-postbuild` traits | `pkg/oam/builtin/traits/patches.go`, `postbuild.go`, `pkg/oam/bundle_patches.go` | **Shipped (go-kure/launcher#781):** removed. Both types stay admitted trait types (`validTraitTypes`, `pkg/oam/validate.go`), so a consumer that delivers through Flux can register its own handler; without one the transform refuses the trait with the same "no handler" message. |
| `PolicyResult.HealthCheckOverrides`, `PolicyResult.ReconciliationSettings` | `pkg/oam/pipeline.go:20-21,59-67` | **Shipped (go-kure/launcher#781):** removed (breaking for a consumer that aliased them). `PolicyResult.Extensions` carries what a consumer's own policy handlers record; launcher neither reads nor changes it. |
| `GeneratedApplication.Patches` and the bundle-patch replay in the force warnings | `pkg/oam/in_document_collisions.go:24-27`, `force_warnings.go:121`, `force_attribution.go:62` | **Shipped (go-kure/launcher#781):** removed with `bundle_patches.go`. `GeneratedApplication.Forced` lost its source (the reconciliation policy's `force`): it is true only for a bundle whose `Force` the caller set before generating (`generateBundle`, `pkg/oam/in_document_collisions.go`), until go-kure/launcher#782 re-sources it. |
| `kurel build --oci-repository`, `--oci-tag`: a bundle-level Flux delivery layer | `pkg/cmd/kurel/delivery.go`; `pkg/cmd/kurel/README.md:159-160` and its "Flux delivery output" section; design §11 "Launcher Layout" | **Shipped (go-kure/launcher#781):** removed in the same change. `kurel build` writes plain YAML only and both flags are unknown. An engine-neutral artifact option may follow when `kurel` work resumes. |
| `force-replace`, `prune-protection` write Flux annotations (`kustomize.toolkit.fluxcd.io/force`, `.../prune`) on every object of the application | Still there: `ForceReplaceHandler` (`pkg/oam/builtin/traits/forcereplace.go`), `PruneProtectionHandler` (`pruneprotection.go`) | **Target (go-kure/launcher#782, open):** both traits stay, and set an engine-neutral delivery-intent field on the kure `Application` instead (kure adds the field; its Flux workflow maps it to the annotation). The PV force warning (`Transformer.WarnForcedVolumes`) reads the intent. |

What stays: `placement`, `dependency`, the tier annotation (author-declared ordering
intent), `postProcessFluxNamespace` (`pkg/oam/transform.go`), which places authored Flux
objects in the Flux namespace, and the Flux kinds as authorable components.

---

## 2. Ordering model (go-kure/launcher#783, go-kure/launcher#784)

### 2.1 Before go-kure/launcher#783

The references name the code before that change.

- **Structure choice** (`transform.go:688-694`): any `dependency` rule gave one bundle per
  component; otherwise one tier gave a flat bundle; otherwise a hierarchy of tier bundles.
- **Automatic tiers** (`pkg/oam/classify.go:47-79,88-141`): the `<domain>/tier` annotation
  first; then a rule-generated Flux source went to `infra`; then a type table (`postgresql`,
  `cnpg-*` → `services`; `daemonset` → `infra`; the rest → `apps`). An authored Flux source
  landed in `apps`, a generated one in `infra`.
- **Names** (`transform.go:888,903-905,909-912,936`): tier bundles `<app>-<tier>` chained by
  `dependsOn` under an umbrella `<app>`; per-component bundles `<app>-<component>`, so a
  generated source became `<app>-<app>-source-<digest>`.
- **Per-component shape** (`buildDependencyAwareCluster`, `transform.go:926`): one bundle
  per component, children of an unnamed root node.

### 2.2 Shipped (go-kure/launcher#783): explicit ordering, one bundle shape

What the code does now (`pkg/oam/ordering.go`, `buildCluster` in `pkg/oam/transform.go`,
`pkg/oam/README.md` "Pipeline"):

1. **No automatic category.** The type table and the generated-source rule are gone.
   `classify.go` keeps `DefaultDomain`, `TierAnnotationKey`, `ComponentLabelKeyForDomain`
   and the annotation read in `ClassifyComponentWithDomain`, which returns no tier for a
   component without the annotation. A component nothing places is in no tier. Order comes
   only from:
   - the `placement` policy;
   - the tier annotation, as an author declaration;
   - the `dependency` policy;
   - a lowering rule's declaration about its own parts (below).
2. **A lowering rule orders the components it emits** with `Component.OrderAfter`. `helm`
   uses it: the release is after the source the rule generates.
3. **One shape.** One application bundle, named after the Application. It is flat when
   nothing is ordered. Otherwise it has ordered child groups, the topological levels of the
   declared order, each depending on the group before it. Declarations that cannot all hold
   (a dependency against the tier order, a cycle) fail the transform, naming each step and
   where it was declared. The per-component shape is gone.
4. **Generated sources** are the application bundle's own applications, ahead of its
   ordered groups, so a shared source adopted by a later component is still applied first.
   A generated source is a Flux source a lowering rule emitted, ordered a component after
   and ordered after nothing, that is not a member of a same-name sibling group. The
   application owns them (see §3.4 on labels). A document cannot place one in a tier or make
   it wait on a component: the transform refuses both. Applying the bundle's own
   applications before its child groups is the delivery engine's job.

   The walks over a bundle that has its own applications and child groups:
   - `postProcessFluxNamespace` walks every bundle (`walkBundles`), so a generated source
     is placed in the Flux namespace when one is set;
   - `generateBundle` (`pkg/oam/in_document_collisions.go`) generates a bundle's own
     applications, then its children, so `GenerateApplications` returns them for `kurel`
     output, the collision check and the volume warnings;
   - `rejectLayoutAugmentersInBundle` (`pkg/cmd/kurel/build.go`) follows the same
     traversal;
   - `walkLeafBundles` still skips them, on purpose: the NetworkPolicy synthesis that uses
     it reads workloads, and an ordered application's own bundle holds only sources.
5. **Decided in the ticket:** the placement vocabulary stays `infra`/`services`/`apps`. A
   group that is exactly the components of one tier is named `<application>-<tier>`, any
   other `<application>-<NN>`, its two-digit position counted from `00`. The name is
   shortened to 253 characters, the limit of a name that names no delivery engine (§3).

### 2.3 Target (go-kure/launcher#784): `oci` lowers to kind components

- New kind component `fluxcd-kustomization`: one Flux Kustomization, with strict decode of
  the upstream `KustomizationSpec` (the pattern of `helmrelease`:
  `HelmReleaseHandler.ToApplicationConfig`,
  `pkg/oam/builtin/components/helmrelease.go`). The type name follows the naming
  decision in [go-kure/launcher#352](https://github.com/go-kure/launcher/issues/352).
- `oci` becomes an upper-level component that lowers into `ocirepository` plus
  `fluxcd-kustomization`. The `OCIHandler` kind goes.
- `oci` today misses most of the Kustomization spec (`patches`, `postBuild`, `force`,
  `dependsOn`, `timeout`, `serviceAccountName` and more). The new kind closes that gap.
- Keep the explicit-registry rule for a non-empty allowlist (`OCIConfig.ApplyPolicy`,
  `oci.go`, through `ociNamesRegistry`) on the `ocirepository` path.
- `SourceDeduplicatable` (`pkg/oam/handler.go`) has one implementer today, `OCIConfig`
  (`OCIConfig.GetSourceKey`, `oci.go`). After go-kure/launcher#784 it has none: remove it,
  or document why it stays.
- **Decide in the ticket:** the name of a source shared by two `oci` components. Today the
  first component's name is kept (`deduplicateSourceRefs`, `pkg/oam/transform.go`), while
  `helm` names a generated source `<document>-source-<digest>`.
- The name of an authored Kustomization against a delivery Kustomization a consumer
  generates is a kure check, not launcher's.

---

## 3. Naming and overrides (go-kure/launcher#785, go-kure/launcher#787, go-kure/launcher#788, go-kure/launcher#792, go-kure/launcher#793)

### 3.1 Current behaviour

- **Consumer knobs:** `ClusterID`, `Namespace`, `FluxNamespace`, `Domain`,
  `ComponentLabelKey`. No hook for any object, application, bundle or source name.
  `LoweringContext.Namer` is a concrete `*NameAllocator` the engine builds itself
  (`NewNameAllocator`, `pkg/oam/lowering.go`).
- **Author overrides.** Shipped with go-kure/launcher#787 (§3.2): the `scaler` HPA and PDB
  (`hpaName`, `pdbName`), the `rbac` objects (`name`) and the `networkpolicy` trait's
  policy (`name`). Still none for: the `postgresql` pooler name (`<cluster>-pooler`),
  generated Helm source names (`<document>-source-<digest>`), the values ConfigMap name
  (`helmValuesConfigMapName`, `pkg/oam/builtin/components/helm.go`), bundle and ordered-group
  names, synthesized NetworkPolicies (`<c>-allow-ingress-traffic` and others), Helm
  hook-group child layouts, and the template release name.
- **Object name = component name** for every kind component. A Service named like its
  StatefulSet is only reachable through `passthrough` or `manifests`: two kind components
  `app` are refused as a duplicate component name (`validateComponent`,
  `pkg/oam/validate.go`).
- **Shortening** is one rule for every name launcher generates (§3.3). CNPG and Service
  names, which are the component's name, are validated, never shortened.
- **Collisions:** `CheckInDocumentCollisions` (`pkg/oam/in_document_collisions.go`) refuses
  a (group, kind, namespace, name) generated by two different generated applications of
  one document. An object one generated application emits twice is not reported there.

### 3.2 Partly shipped (go-kure/launcher#787, open): name overrides

- **Shipped: an authored name is used as written or refused.** It is never shortened and
  never changed. One that cannot name its object fails the transform with the property in
  the error (`checkAuthoredObjectName`, `pkg/oam/builtin/traits/authored_name.go`): the
  DNS-1123 subdomain rule, and an authored empty string is refused too. A `scope`, which
  is one part of a generated name, is checked for its characters only
  (`checkAuthoredNamePart`). The rule covers the names the traits already took (`name` on
  `ingress`, `httproute`, `configmap` and `cilium-networkpolicy`, the Secret names of
  `certificate`, `expose` and `external-secret`, the `volsync` names) and the new ones.
- **Shipped: four new author overrides** (the traits README, "Conventions"):
  - `scaler` `hpaName` and `pdbName` (`ScalerHandler`, `traits/scaler.go`). `pdbName`
    without `enablePDB: true` is refused: it would name no object.
  - `rbac` `name` (`RBACHandler`, `traits/rbac.go`) names the Role, RoleBinding,
    ClusterRole and ClusterRoleBinding and both `roleRef.name`. The binding's subject
    stays the component's ServiceAccount, and the `app` label stays the component's. It is
    held to the subdomain rule, which is stricter than the cluster's own rule for the RBAC
    kinds: a name with a colon, which a cluster accepts, is refused.
  - `networkpolicy` `name` (`NetworkPolicyHandler`, `traits/networkpolicy.go`).
- **Shipped limit:** an authored name is not checked against other objects at the
  transform. Two objects of one kind, namespace and name are reported by
  `CheckInDocumentCollisions` over `GenerateApplications`, as for a default name.
- **Target, author:** an override for each remaining name of §3.1, plus an object name
  separate from the component name. The latter allows a Service named like its StatefulSet
  as kind components (rule 4 permits different kinds to share a name).
- **Target, consumer:** an optional naming hook on `TransformContext`, keyed by owning
  component and role, falling back to the default.
  - The `Namer` (`NameAllocator.Name` and `NameOrAdopt`, `pkg/oam/lowering.go`) consults
    it for lowering-rule names.
  - Most generated names are **not** built by the Namer: trait objects (`scaler`, `rbac`,
    `networkpolicy`), synthesized NetworkPolicies, the values ConfigMap, hook-group
    children and bundle names. The hook must reach each of these sites. The ticket lists
    the roles.
  - An override from the hook is held to the shipped rule for an authored name: never
    shortened, validated for its target, refused when invalid or too long. Only
    launcher's own defaults go through the shortening rule (§3.3).

### 3.3 Shipped (go-kure/launcher#792, go-kure/launcher#793): uniqueness and shortening

- **go-kure/launcher#793, one shortening rule:** `ShortenName(name, limit)` and
  `ShortenNameWithSuffix(name, suffix, limit)` (`pkg/oam/shorten_name.go`;
  `pkg/oam/README.md` "Names and overrides"). A name that fits is returned unchanged. A
  longer one keeps a prefix, a `-` and the first 10 hex characters of the sha256 of the
  whole name; a fixed suffix is kept whole, unless it leaves less room than the digest
  needs (a long routing `scope`): name and suffix are then shortened together. The caller
  passes the limit:
  - `ShortenLimitLabel` (63): the component label value, `ComponentLabelValue`.
  - `ShortenLimitSubdomain` (253): every object name launcher generates by default, the
    hook-group child layouts and the ordered-group bundles.
  - `ShortenLimitHelmRelease` (53): the one exception to the rule. The result is what Flux
    computes for a HelmRelease, so a release launcher renders itself is named as Flux
    would name it. Nothing passes this limit yet: go-kure/launcher#785 is its first caller
    (§4.2).

  The Namer shortens instead of refusing an over-length `<base>-<suffix>` (`generatedName`,
  `pkg/oam/lowering.go`). The characters are checked on the name as built, before it is
  shortened, and the shortened name is reserved, so it takes part in collision detection.
  An authored name never goes through the rule (§3.2).
- **go-kure/launcher#792, hook-group child names:** a child layout is named
  `<application>-<component>-<NN>-<phase>` (`hookGroupChildName`,
  `pkg/oam/builtin/components/helmtemplate_render.go`), so two applications with a
  same-named component no longer produce the same child names. The transform tells the
  component its application through `ApplicationNameSetter` (`pkg/oam/handler.go`;
  `HelmTemplateConfig.SetApplicationName`). A config built directly, with no application,
  keeps `<component>-<NN>-<phase>`.
  - The join is a plain `-`: application `a-b` with component `c`, and application `a`
    with component `b-c`, compose the same prefix.
  - The name carries no namespace, as a bundle name carries none: two applications with
    one name in two namespaces produce the same child names.

### 3.4 Shipped (go-kure/launcher#788): component label and provenance

- **Before:** launcher never stamped `<domain>/component`. It was only a NetworkPolicy
  selector key (`ComponentLabelKey`, else the domain's key), so a synthesized NetworkPolicy
  could select a label present nowhere in the output. Chart-rendered pods carried chart
  labels only. `GeneratedApplication.Component` mapped trait sub-applications and sibling
  groups to their component; the pooler, database, generated sources and synthesized
  NetworkPolicies reported themselves.
- **Now:** the transform's last step records the owner of every application, and launcher
  stamps `<ComponentLabelKey>: ComponentLabelValue(c)` on every object and pod template a
  component owns, where the key is absent (`pkg/oam/component_ownership.go`,
  `pkg/oam/README.md` "Component label and ownership").
  - Chart output under Flux delivery: through one post-renderer on the HelmRelease, after
    the authored ones, with a strategic-merge patch per kind with a pod template
    (Deployment, StatefulSet, DaemonSet, Job, CronJob, ReplicaSet, ReplicationController,
    PodTemplate) and one for a bare Pod, each for the kind in its own API group only. It
    replaces a value the chart set and touches no
    selector: with a `ComponentLabelKey` the chart's own selectors use, that parts a
    selector from the chart's pods where it does not accept the component's value. The key
    to use is one no chart sets, such as the default. Whether a chart's hook and test Pods
    pass through a post-renderer is not verified.
  - Chart output under template delivery: labels added to the rendered objects, where the
    key is absent.
  - A generated workload whose own selector rules the label out keeps its pod template as
    written, and its pods carry no component label.
  - `GeneratedApplication.Component` is the authored component for the pooler, a database,
    an object store and a component's synthesized NetworkPolicies.
  - A synthesized inbound or egress NetworkPolicy selects the authored component's value,
    the one stamped, also for an entry a lowering rule emitted under another name.
  - A shared generated source is owned by the application: it carries no component label
    and reports an empty component. So does the external-backend NetworkPolicy.
  - Not covered: pods an operator creates from a custom resource, and an owner label a
    consumer needs to be authoritative (it enforces that in its own pass).

---

## 4. Helm and values (go-kure/launcher#785, go-kure/launcher#786)

### 4.1 Current behaviour

| Path | Release name | Values |
|---|---|---|
| `helm` or `helmrelease`, Flux delivery, no Flux namespace | No `spec.releaseName`. Flux defaults it to the HelmRelease name, `<component>`. | Inline `values`. On `helm` only, `valuesMode: configMap`: a ConfigMap `<component>-values-<hash>`, prepended to `valuesFrom` (`helmValuesConfigMap`, `pkg/oam/builtin/components/helm.go`); `helmrelease` refuses the key (`helmReleaseValuesModeHint`, `helmrelease.go`). On `helm` only, `secretValues`: a Secret `<component>-secret-values-<hash>`, in `valuesFrom` after the values ConfigMap and before the authored entries (§4.3). `valuesFrom` references to out-of-band ConfigMaps or Secrets work. |
| Same, Flux namespace set | The HelmRelease moves to the Flux namespace and launcher defaults `targetNamespace` to the app namespace (`HelmReleaseConfig.Generate`, `helmrelease.go`). Flux then computes `<appns>-<component>`. | As above. The `helm` values ConfigMap and values Secret follow the HelmRelease into the Flux namespace. |
| `helm` with `delivery: template`, or `helmtemplate` | kure's fixed default `release` as `.Release.Name`, and the application namespace as `.Release.Namespace` (`chartSource`, `helmtemplate_render.go`); the chart's templates decide the object names. `releaseName` is refused (`helmFluxOnlyKeys`, `helm.go`; the strict decode of `helmTemplateProperties`, `helmtemplate.go`). | Inline values, rendered into the output; `secretValues` is merged over them for the render (§4.3). `valuesFrom` is refused: a build cannot read a cluster object. |

Explicit values are written into a Secret by `secretValues` under Flux delivery only
(§4.3).

### 4.2 Target (go-kure/launcher#785): release name

- Default release name `<component>` under both deliveries, written explicitly:
  `spec.releaseName` on every HelmRelease, and the template render's release name, as in
  [go-kure/launcher#778](https://github.com/go-kure/launcher/pull/778).
- The `helmrelease` kind sets the default, so a directly authored `helmrelease` gets it
  too, as the kind already defaults `targetNamespace` (`HelmReleaseConfig.Generate`).
- Shortened with the shipped rule of go-kure/launcher#793 at `ShortenLimitHelmRelease`
  (53 characters), which reproduces Flux's own algorithm for that limit (§3.3). This
  ticket is the limit's first caller.
- The author's `releaseName` overrides it under both deliveries. The consumer override
  comes from go-kure/launcher#787.

### 4.3 Shipped (go-kure/launcher#786): Secret values

- `secretValues` is a second values tree, for the values that must not sit in the
  HelmRelease or in a ConfigMap, on the `helm` component and on the `helmtemplate` kind.
  The `helmrelease` kind decodes its properties strictly as a `HelmReleaseSpec` (§4.1) and
  has no such property: an author using the kind names a Secret in `valuesFrom`.
- **Flux delivery:** the `helm` rule appends a `secret` trait to the `helmrelease`
  (`helmSecretValuesTrait`, `pkg/oam/builtin/components/helm_secret_values.go`). Its
  Secret holds the tree under the key `values.json`, serialized as the values ConfigMap's
  is, and is named `<component>-secret-values-<hash>`: ten hex digits of the sha256 of
  those bytes, shortened by the rule of §3.3. It is in the HelmRelease's namespace and
  follows it into the Flux namespace, as the values ConfigMap does. The `valuesFrom` order
  is: values ConfigMap, values Secret, authored entries. No value of the tree is written
  to the HelmRelease or to the values ConfigMap.
- **Template delivery:** the tree is merged over `values` for the render
  (`mergeSecretValues`). Nothing is emitted for it: a value is in the output only where
  the chart renders it.
- **A path set in both `values` and `secretValues` is refused,** under either delivery and
  either values mode (`refuseSharedValuePath`). Flux applies inline `spec.values` after
  every `valuesFrom` entry, so without the refusal the winner would depend on the values
  mode.
- **The `secret` trait** is new with this ticket (`pkg/oam/builtin/traits/secret.go`). It
  builds through the one Secret path, `ParseSecretProperties` and `GenerateSecret`
  (`pkg/oam/builtin/components/secret.go`), which a `secret` kind of go-kure/launcher#790
  (§6.2) can use.
- **Policy:** `oam.ExplicitSecretPolicy` (`AllowExplicitSecrets() bool`,
  `pkg/oam/policy.go`) is an interface a `Policy` may also implement. Where it answers
  false these are refused: the `secret` trait, `secretValues` on `helm` and on
  `helmtemplate`, and a core Secret a `passthrough` or `manifests` component carries
  (`enforceExplicitSecretObject`, `enforce.go`; told by group and kind, whatever it holds).
  The author then references a Secret created out of band. A policy that does not
  implement the interface allows them, as does no policy. Breaking only for a consumer
  whose policy answers false.
- **Not covered:** a Secret a chart renders under template delivery is emitted under such
  a policy. Its content comes from the chart and its values, and most charts render one.
- **Limits:** a Secret in the output is base64, not encrypted: the output is as sensitive
  as the document. The Secret's name carries 40 bits of a digest of the tree. No refusal
  repeats a value, and the cause of a render that fails with `secretValues` is withheld.
  One Helm warning can still print a value to the build's log (a subchart
  `global` conflict; go-kure/launcher#794, item 9). Under template delivery, out-of-band
  secrets go through the chart's own `existingSecret`-style values.

---

## 5. Security on template delivery (go-kure/launcher#791)

### 5.1 Current behaviour

| Check | Flux delivery | Template delivery |
|---|---|---|
| Registry allowlist | Chart source host, on the generated or authored Flux source (`HelmRepositoryConfig.ApplyPolicy`, `pkg/oam/builtin/components/helmrepository.go`, and the other source kinds). Chart images: not checked (`HelmReleaseConfig.ApplyPolicy` is a no-op). | Chart source host, before any fetch, and every image of a rendered workload (`HelmTemplateConfig.ApplyPolicy`, `helmtemplate_policy.go`). |
| Image reference check (`ValidateImageRef`, `common.go`) | No | Yes, on every init and regular container of a rendered workload. |
| Pod security (privileged, host namespaces, hostPath, capabilities; the `Policy` flags, `pkg/oam/policy.go`, read by `enforcePodTemplatePolicy`, `cnpg_common.go`) | No | Yes, on every rendered workload (`enforceRenderedObjectPolicy`, `helmtemplate_policy.go`). A chart rendering a privileged pod is refused unless the policy allows it. |
| PersistentVolume (a `hostPath` or `local` source, `capacity.storage`) | No | Yes, since the `persistentvolume` kind (go-kure/launcher#790): a rendered PersistentVolume is held to what the kind holds its own to (`enforcePersistentVolumePolicy`, `enforce.go`). |
| Namespace | HelmRelease and source in the Flux namespace | **Shipped (go-kure/launcher#794, item 4):** a namespaced object the chart rendered without `metadata.namespace` is given the application namespace, where a Helm install would create it (`stampRenderedNamespaces`, `helmtemplate_render.go`). A namespace the chart wrote is kept, and it is not checked. A cluster-scoped object is left as rendered. So is an object whose scope is unknown (a kind kure does not register, with no CustomResourceDefinition for it among the rendered objects; a chart's `crds/` directory is not rendered): it stays without a namespace. |

`kurel` sets no `Policy` (`runBuild`, `pkg/cmd/kurel/build.go`), so `NoopPolicy` applies
(`Transformer.TransformWithPolicy`, `pkg/oam/transform.go`): the registry allowlist is
empty on every path, and the five security flags are denied. Only a library consumer that
passes a `Policy` sets either.

A chart delivered as a `HelmRelease` is rendered on the cluster, so the Flux column cannot
be closed at build time.

### 5.2 Implemented (go-kure/launcher#791)

1. **Decode:** chart output is decoded with kure's parser (`ParseYAMLWithOptions` with
   `AllowUnstructured`), so registered kinds are typed (`decodeChartManifests`,
   `helmtemplate_render.go`). The one exception is an object kept as rendered over a field
   its type does not declare (Limits, below): it is unstructured, whatever its kind.
   `Generate` returns a fresh copy on each call, so a
   workload-decorating trait acts on a chart's typed workload without changing the cached
   render.
2. **Chart URL allowlist:** `HelmTemplateConfig.ApplyPolicy` checks the chart URL host
   against `AllowedRegistries`, as the source kinds do, and fails before any request.
3. **Rendered workloads** go through the existing image and pod-security enforcement.
   - `ApplyPolicy` runs before `Generate`, so it triggers the render — the one `Generate`
     and `AugmentLayout` then return. The chart is therefore fetched during the transform.
   - The pod spec of each Pod, PodTemplate, ReplicationController, Deployment, StatefulSet,
     DaemonSet, ReplicaSet, Job and CronJob is checked by `enforcePodTemplatePolicy`, the
     variant over `corev1.PodSpec` the operator-CR components already use. The storage request
     of a PersistentVolumeClaim and of a StatefulSet's claim templates is held to
     `MaxStorageSize`, as on the authored kinds. The replica count of a Deployment,
     StatefulSet, ReplicaSet or ReplicationController and the `maxReplicas` of a
     HorizontalPodAutoscaler are held to `MaxReplicas`, as on the authored kinds and the
     `scaler` trait. A PersistentVolume's `hostPath` or `local` source needs
     `AllowHostPathVolumes`, and its `capacity.storage` is held to `MaxStorageSize`, as on
     the `persistentvolume` kind (go-kure/launcher#790).
   - **Decided:** `ValidateImageRef` (tag or digest required, no `:latest`) applies to
     chart images, so a rendered workload is held to the same image rule as a
     launcher-built one.
   - **Decided:** with no policy passed, `NoopPolicy` denies a chart that renders a
     privileged container, a host namespace or a hostPath volume, as it does an authored
     workload. The `Policy` flags `AllowPrivileged`, `AllowHostNetwork`, `AllowHostPID`,
     `AllowHostIPC` and `AllowHostPathVolumes` allow one.
   - **Limits:** a workload, claim or PersistentVolume that cannot be decoded typed (an API
     version kure's scheme does not register), and a list nested in an unregistered list
     (any object with a top-level `items` array there), are refused; a
     custom resource's pods, the archive host a Helm repository
     index names and redirects are not checked (the last two: go-kure/launcher#794, item 6).
     A field the vendored API type does not declare is no longer dropped: on a workload, a
     claim or a PersistentVolume it is refused; any other registered kind is emitted as
     rendered, as an unstructured object, with the field kept. One such field is refused on
     every kind: a top-level `items` array on a kind that declares none, since emitted as
     rendered the object would be a list. A key inside a type that unmarshals itself is the
     known limit: it is not reported and is still dropped (go-kure/launcher#794, item 7).

---

## 6. Contract metadata and kind coverage (go-kure/launcher#789, go-kure/launcher#790)

### 6.1 Shipped (go-kure/launcher#789): contract metadata

- **Before:** no builtin implemented `ContractDescriber`, so `HandlerContracts()` returned
  empty maps. A missing co-registration was found only when a lowered component was
  dispatched, and the message named the type, not the authored component or the rule.
- **Now** (`pkg/oam/README.md` "Contract metadata"):
  - Every built-in handler and lowering rule implements `ContractDescriber`
    (`pkg/oam/handler.go`; one `contract.go` in each of the components, traits and
    policies packages). `Family` is the type name, `Version` is `builtin.ContractVersion`
    (`v1alpha1`, `pkg/oam/builtin/contract.go`).
  - `HandlerContractSet` (`pkg/oam/transform.go`) has a third map, `Policies`. Breaking
    for a consumer that wrote an unkeyed literal of it.
  - A built-in rule's identity carries the version: `component/webservice@v1alpha1` on
    `Origin.Rule`, on `LoweringStep.Rule` and in a `LoweringError` chain, where it read
    `component/webservice`.
  - A lowering rule declares the types it lowers into (`LoweringTargetDeclarer`,
    `LoweringTargets`, `pkg/oam/lowering_targets.go`), and every built-in rule does.
  - `Transformer.Seal` refuses a registry where a declared type is registered neither as
    a handler nor as a rule, naming the rule and the type: `registry incomplete: lowering
    rule component/webservice@v1alpha1 lowers into component type "service", which is not
    registered`. Breaking for a consumer that registers a built-in rule without the types
    it lowers into.
  - The "no handler" error names where the element is, and for an element a rule emitted
    the rule and the authored component it lowered (`emittedBy`, `traitLocation`,
    `pkg/oam/lowering_targets.go`).
- **When the check runs.** A rule and the handlers of its targets are registered in any
  order, so the check cannot run at registration. `Transform` and `TransformWithPolicy`
  call `Seal` first, whether or not the document uses the rule; `LowerRaws` does not. A
  caller that has finished registering may call `Seal` itself. It stores and locks
  nothing.
- **Limits:** the engine enforces no field of the metadata but a rule's `Version` (in the
  identity) and `Deprecated` (one warning per authored element). A rule that declares no
  targets is not checked, and the engine does not check that a rule emits only what it
  declares.

### 6.2 Partly shipped (go-kure/launcher#790, open): full spec and the full set of kinds

- **Shipped: the kind inventory.** `pkg/oam/builtin/components/README.md` "Kind
  inventory" has one row per constructor the base library generates, with a status
  (`kind`, `component`, `trait`, `missing`, `not authorable`), the component or trait type
  and, for a kind judged not authorable, the reason. Two tests hold it to the code
  (`TestKindInventory_CoversEveryConstructor`, `TestKindInventory_MatchesCallSites`,
  `kind_inventory_internal_test.go`): a base-library bump that adds a kind fails until the
  table has its row.
- **Shipped: four core kinds,** `namespace`, `limitrange`, `resourcequota` and
  `persistentvolume` (`namespace.go`, `limitrange.go`, `resourcequota.go`,
  `persistentvolume.go` in `pkg/oam/builtin/components`).
  - Each has one schema key per json field of the object's spec type, and the property
    map is decoded strictly into that type (`decodeKindSpec`, `kind_decode.go`), as the
    CloudNativePG kinds are. A test holds each schema to the type by reflection
    (`TestCoreKindSchemas_CoverSpec`).
  - Each emits one object named after the component, with the authored spec. Its
    metadata is not authorable, so a Namespace that needs the Pod Security Admission
    labels cannot be written with `namespace`. The only label is the component label of
    go-kure/launcher#788 (§3.4).
  - `namespace` and `persistentvolume` are cluster-scoped. A `namespace` component's name
    must be a DNS-1123 label.
  - `persistentvolume` is held to environment policy on every path that produces one
    (the kind, template delivery, `passthrough`, `manifests`): a `hostPath` or `local`
    source needs `AllowHostPathVolumes`, and `capacity.storage` is held to
    `MaxStorageSize` (`enforcePersistentVolumePolicy`, `enforce.go`). A `csi` or
    `flexVolume` source is not checked (go-kure/launcher#794, item 12, not decided).
    Breaking for a chart, a `passthrough` component or a `manifests` source that holds
    such a PersistentVolume. The other three kinds have nothing to enforce.
- **Field gaps** in the hand-parsed kinds (upstream fields with no schema key):
  - `statefulset`: `tolerations`, `topologySpreadConstraints`;
  - `daemonset`: `affinity`, `topologySpreadConstraints`;
  - `job`, `cronjob`: `affinity`, `tolerations`, `topologySpreadConstraints`;
  - all workloads: `imagePullPolicy` and further container and pod fields;
  - `service`: `ExternalName` (refused today: not in `serviceTypes`,
    `pkg/oam/builtin/components/service.go`), traffic policies, load-balancer fields;
  - `persistentvolumeclaim`: `dataSource`, `dataSourceRef`, `selector`, `volumeName`.

  `selector` is refused today with an explicit reason on `deployment`, `statefulset`,
  `daemonset` and `job`, and `template` on `deployment` and `job`
  (`deploymentSpecRejectedKeys`, `statefulSetSpecRejectedKeys`,
  `daemonSetSpecRejectedKeys`, `jobSpecRejectedKeys`, in `deployment_spec.go`,
  `statefulset_spec.go`, `daemonset_spec.go` and `job.go`); each kind's sub-task decides
  whether that refusal stays, with its reason documented. Each kind gets a sub-task in the
  ticket.
- **Missing kinds:** the inventory's `missing` rows (Secret, Pod, ServiceMonitor,
  StorageClass, Gateway among them), and its `trait` rows, the
  kinds reachable only as traits today (Ingress, HTTPRoute, Certificate, ExternalSecret,
  HPA, PDB, NetworkPolicy, CiliumNetworkPolicy, Role and RoleBinding, ReplicationSource).
  The ticket adds them group by group. A kind kure lacks is added to kure first.

---

## 7. Asymmetries (go-kure/launcher#794)

Each item is to be fixed, or documented with its reason. The issue holds the full list
and the disposition of every item. The four this document started from:

- **`targetNamespace` default (item 1): documented, no change.** `oci` sets no default;
  `helmrelease` defaults it to the application namespace under a Flux namespace
  (`HelmReleaseConfig.Generate`). The difference is deliberate: a Kustomization's
  `targetNamespace` overrides the namespace of every namespaced object in the artifact,
  where the HelmRelease default only says where the release installs. The `oci` property's
  description says so (`OCIHandler.PropertySchema`, `pkg/oam/builtin/components/oci.go`).
  The `fluxcd-kustomization` kind of go-kure/launcher#784 follows `oci`.
- **Environment policy on unbuilt objects (items 2 and 10): shipped for `passthrough` and
  `manifests`.** Both are held to the check template delivery runs on a rendered object
  (`enforceRenderedObjectPolicy`, `helmtemplate_policy.go`): the image, pod security,
  resource, storage and replica rules, on every kind that check reads.
  - `passthrough` (`PassthroughConfig.ApplyPolicy`, `passthrough.go`): an object of a
    kind kure's scheme registers is decoded as that kind for the check alone
    (`policyObject`); what is emitted stays the authored object. One that cannot be read
    is refused: a registered kind that does not decode, and a workload kind, a claim or a
    PersistentVolume in an API version the scheme does not register.
  - `manifests` and `crd` (`manifestConfig.ApplyPolicy`, `enforceManifestPolicy`,
    `manifestsource.go`): the objects of an `inline` source are checked at the policy
    step, those of a `url` source at generation, where they are first known. The `url`
    host is still checked first.
  - Both check again what `Generate` emits and report a refusal there as the component's
    `oam.ViolationError`.
  - A PersistentVolume is among the kinds the check reads since the `persistentvolume`
    kind (§6.2), on both.
  - A core Secret is refused on both under a policy that forbids explicit secrets
    (go-kure/launcher#786, §4.3).
  - Not covered: an object of any other kind the check does not read passes, a custom
    resource included, so the pods its controller creates are not checked. With no policy
    passed to the handler, nothing is checked.
  - Breaking for a document that relied on either bypass.
  - **Documented, no change:** `service`, `serviceaccount` and `configmap` have no
    `ApplyPolicy`, deliberately. None of their properties maps to an environment-policy
    method, so there is nothing to enforce; the policy makes no statement about a
    Service's `type` (`pkg/oam/builtin/components/README.md`, the `service` and
    `serviceaccount` entries).
- **Pod-template labels (item 3): documented, no change.** `PodTemplateLabels` is
  implemented by `DeploymentConfig` only (`deployment.go`), not by `statefulset`,
  `daemonset`, `cronjob` or `job`. Its one reader is the sibling-group check
  (`podTemplateLabeler`, `pkg/oam/sibling_group.go`), and `deployment` is the only pod kind
  a lowering rule emits into a group. A rule that emits another pod kind into a group must
  add the method to that kind's config (`pkg/oam/README.md` "Same-name sibling groups").
  The component label of go-kure/launcher#788 (§3.4) is stamped on the pod template of
  every workload kind without it, and is not among the labels it returns.
- **Namespace on template output (item 4): shipped** (§5.1). An object of unknown scope
  is left as rendered; a `scopeOverrides` property on `helm` and `helmtemplate`, as
  `manifests` has, is the follow-up (item 11, not decided).

Item 5 was decided as "document" and needed no text: tiers are declared, never
derived, since go-kure/launcher#783 (§2.2).

`kurel`'s global `-f/--output-file`, which `build` ignores, is filed separately as
go-kure/launcher#795 and deferred with the rest of the `kurel` CLI work.

---

## 8. Ticket index

State is what the code holds: **shipped** (the ticket's whole target), **partly** (the
section says which part), or **open** (nothing of it).

| Issue | Ticket | Section | State | Needs |
|---|---|---|---|---|
| [go-kure/launcher#781](https://github.com/go-kure/launcher/issues/781) | Remove Flux delivery fields from library output, and `kurel`'s delivery mode | §1.3 | Shipped | — |
| [go-kure/launcher#782](https://github.com/go-kure/launcher/issues/782) | Delivery intent instead of Flux annotations | §1.3 | Open | [go-kure/kure#974](https://github.com/go-kure/kure/issues/974) (delivery intent), go-kure/launcher#781 |
| [go-kure/launcher#783](https://github.com/go-kure/launcher/issues/783) | Explicit ordering only; one bundle shape | §2.2 | Shipped | go-kure/launcher#781; go-kure/launcher#787 for the name override (can follow) |
| [go-kure/launcher#784](https://github.com/go-kure/launcher/issues/784) | `oci` as an upper-level component; new `fluxcd-kustomization` kind | §2.3 | Open | — |
| [go-kure/launcher#785](https://github.com/go-kure/launcher/issues/785) | Release name default (rescopes [go-kure/launcher#776](https://github.com/go-kure/launcher/issues/776)) | §4.2 | Open | go-kure/launcher#793 |
| [go-kure/launcher#786](https://github.com/go-kure/launcher/issues/786) | Secret values | §4.3 | Shipped | — |
| [go-kure/launcher#787](https://github.com/go-kure/launcher/issues/787) | Name overrides | §3.2 | Partly: authored names used as written or refused; `scaler`, `rbac` and `networkpolicy` overrides | go-kure/launcher#783, go-kure/launcher#793 |
| [go-kure/launcher#788](https://github.com/go-kure/launcher/issues/788) | Component label and provenance | §3.4 | Shipped | — |
| [go-kure/launcher#789](https://github.com/go-kure/launcher/issues/789) | Contract metadata | §6.1 | Shipped | — |
| [go-kure/launcher#790](https://github.com/go-kure/launcher/issues/790) | Full spec and full set of kind components | §6.2 | Partly: the kind inventory; the `namespace`, `limitrange`, `resourcequota` and `persistentvolume` kinds | [go-kure/kure#981](https://github.com/go-kure/kure/issues/981) (missing constructors), go-kure/launcher#787 |
| [go-kure/launcher#791](https://github.com/go-kure/launcher/issues/791) | Security on template delivery | §5.2 | Shipped | — |
| [go-kure/launcher#792](https://github.com/go-kure/launcher/issues/792) | Hook-group child names unique across applications | §3.3 | Shipped | go-kure/launcher#793, go-kure/launcher#787 |
| [go-kure/launcher#793](https://github.com/go-kure/launcher/issues/793) | One shortening rule | §3.3 | Shipped | — |
| [go-kure/launcher#794](https://github.com/go-kure/launcher/issues/794) | Asymmetries | §7 | Partly: `passthrough` and `manifests` policy, template namespace, undeclared fields (item 7); items 1, 2, 3 and 5 documented | go-kure/launcher#783, go-kure/launcher#784, go-kure/launcher#788 |
| [go-kure/launcher#795](https://github.com/go-kure/launcher/issues/795) | `kurel build` ignores the global `-f/--output-file` (deferred) | §7 | Open | — |
