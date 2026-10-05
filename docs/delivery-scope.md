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
Paths are relative to the repository root. Kure paths refer to the kure commit `go.mod`
pins, `v0.2.0-beta.15.0.20261005101450-d3a45fad7a9a`: a commit of kure's `main` after
v0.2.0-beta.15, pinned while both libraries are being worked on. Everything here is
pre-release: output, names and the library contract may change, and live-cluster upgrade
effects are not a constraint. A section or
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
   `fluxcd-kustomization` kind).
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

### 1.3 What leaves launcher (go-kure/launcher#781 and go-kure/launcher#782 shipped)

Every row is shipped: "Now" is what the code does, and "Where it was" names the code
before that change, most of which is gone.

| Before | Where it was | Now |
|---|---|---|
| Automatic health checks on every leaf bundle, from a type table | `applyAutoHealthChecks` `pkg/oam/transform.go:1858`, table `componentHealthCheckGVK` `:1799`, `isFluxControlPlaneGVK` `:1842`, the `EmitsAutoHealthCheck` veto | **Shipped (go-kure/launcher#781):** removed. Launcher sets no health check and no other delivery field on a bundle (`buildCluster`, `pkg/oam/transform.go`); what a step waits for is the delivering consumer's. An author who needs explicit checks declares them through the consumer's own policy. |
| Reconciliation settings on bundles (interval, retry, timeout, prune, wait, force, suspend) | `applyReconciliationSettings` `transform.go:1909`; policy `pkg/oam/builtin/policies/reconciliation.go` | **Shipped (go-kure/launcher#781):** removed with the policy. A document with a `reconciliation` policy fails the transform with `no handler for policy type "reconciliation": it configures delivery, which launcher leaves to the consumer that delivers the application; …`, unless the consumer registers its own handler. |
| `health-checks` policy | `pkg/oam/builtin/policies/healthchecks.go` | **Shipped (go-kure/launcher#781):** removed, and refused the same way without a consumer's handler. |
| `fluxcd-patches`, `fluxcd-postbuild` traits | `pkg/oam/builtin/traits/patches.go`, `postbuild.go`, `pkg/oam/bundle_patches.go` | **Shipped (go-kure/launcher#781):** removed. Both types stay admitted trait types (`validTraitTypes`, `pkg/oam/validate.go`), so a consumer that delivers through Flux can register its own handler; without one the transform refuses the trait with the same "no handler" message. |
| `PolicyResult.HealthCheckOverrides`, `PolicyResult.ReconciliationSettings` | `pkg/oam/pipeline.go:20-21,59-67` | **Shipped (go-kure/launcher#781):** removed (breaking for a consumer that aliased them). `PolicyResult.Extensions` carries what a consumer's own policy handlers record; launcher neither reads nor changes it. |
| `GeneratedApplication.Patches` and the bundle-patch replay in the force warnings | `pkg/oam/in_document_collisions.go:24-27`, `force_warnings.go:121`, `force_attribution.go:62` | **Shipped (go-kure/launcher#781):** removed with `bundle_patches.go`. `GeneratedApplication.Forced` lost its source (the reconciliation policy's `force`): it is true only for a bundle whose `Force` the caller set before generating (`generateBundle`, `pkg/oam/in_document_collisions.go`). go-kure/launcher#782 added a second source: the application's `ForceReplace` delivery intent. |
| `kurel build --oci-repository`, `--oci-tag`: a bundle-level Flux delivery layer | `pkg/cmd/kurel/delivery.go`; `pkg/cmd/kurel/README.md:159-160` and its "Flux delivery output" section; design §11 "Launcher Layout" | **Shipped (go-kure/launcher#781):** removed in the same change. `kurel build` writes plain YAML only and both flags are unknown. An engine-neutral artifact option may follow when `kurel` work resumes. |
| `force-replace`, `prune-protection` write Flux annotations (`kustomize.toolkit.fluxcd.io/force`, `.../prune`) on every object of the application | The config decorators of `ForceReplaceHandler` (`pkg/oam/builtin/traits/forcereplace.go`) and `PruneProtectionHandler` (`pruneprotection.go`), with a post-augment hook for what a layout augmenter adds | **Shipped (go-kure/launcher#782):** both traits stay, and set the engine-neutral delivery intent on the kure `Application` instead (`stack.Application.Delivery`; kure's Flux workflow maps it to the annotation). Launcher's objects carry neither annotation, and `kurel build`'s output shows neither trait. A sibling group's application takes the intent of any member. The PV force warning (`Transformer.WarnForcedVolumes`) reads the intent through `GeneratedApplication.Forced`. |

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

### 2.3 `oci` lowers to kind components (go-kure/launcher#784, done)

- Kind component `fluxcd-kustomization` (`pkg/oam/builtin/components/fluxcd_kustomization.go`):
  one Flux Kustomization, with strict decode of the upstream `KustomizationSpec` (the
  pattern of `helmrelease`). The type name follows the naming decision in
  [go-kure/launcher#352](https://github.com/go-kure/launcher/issues/352). It is an
  authored Flux object, not a delivery mechanism (§1.2), and it closes the gap `oci` had
  against the spec (`patches`, `postBuild`, `force`, `dependsOn`, `timeout`,
  `serviceAccountName` and more). An unknown field is refused with its path from the
  property root.
- `oci` is an upper-level component: `OCIRule` (`oci.go`) lowers it into `ocirepository`
  plus `fluxcd-kustomization`. The `OCIHandler` kind and its `OCIConfig` are gone. A
  document with one `oci` component per artifact renders the same two objects as before,
  byte for byte.
- The explicit-registry rule for a non-empty allowlist is the `ocirepository` terminal's
  (`fluxsource.go`), so its refusals name that kind and its `url` field.

Decided in the ticket:

- **The component's own source stays with it.** A component alone on its artifact lowers
  to a same-name sibling group. Its `ocirepository` member is not a generated source (§2.2
  item 4): the rule orders nothing after it and it is a group member, so the pair is one
  unit, and a tier annotation, `placement` or `dependency` naming the component moves both
  objects. With nothing declared the pair orders nothing.
- **A shared source belongs to the application.** When two or more `oci` components of a
  document have the same source, it is emitted once as a generated source named
  `<document>-source-<digest>`, `helm`'s scheme, and no longer under the name of the
  component deployed first. The rule orders each Kustomization after it (§2.2 item 2), so
  it sits in the application bundle itself, ahead of the ordered groups. The identity is
  the url, the version and the effective interval: components whose intervals differ keep
  a source each, so no component's interval is replaced by another's. An `oci` and a `helm`
  component on one artifact do not share.
- **`SourceDeduplicatable` is removed**, with the engine pass that read it
  (`deduplicateSourceRefs`): no builtin implemented it any more, and a rule that shares a
  source emits it as a component (`LoweringContext.ResolveSharedName`, `Component.OrderAfter`).
- `targetNamespace` is never defaulted on `fluxcd-kustomization`, for the reason `oci`
  never defaulted it (§7).
- The name of an authored Kustomization against a delivery Kustomization a consumer
  generates is a kure check, not launcher's.

---

## 3. Naming and overrides (go-kure/launcher#785, go-kure/launcher#787, go-kure/launcher#788, go-kure/launcher#792, go-kure/launcher#793)

### 3.1 Current behaviour

- **Consumer knobs:** `ClusterID`, `Namespace`, `FluxNamespace`, `Domain`,
  `ComponentLabelKey`, and the `Naming` hook for the names of a closed set of roles
  (go-kure/launcher#787, §3.2), which include the `postgresql` Pooler and Databases: a
  lowering rule resolves such a name with `LoweringContext.ResolveName`, and
  `Transformer.ComponentEndpointsNamed` gives the pooler endpoint the same name. The roles
  also include a kind component's own object, and the source and the values ConfigMap and
  Secret the `helm` rule generates.
- **Author overrides.** Shipped with go-kure/launcher#787 (§3.2): the `scaler` HPA and PDB
  (`hpaName`, `pdbName`), the `rbac` objects (`name`), the `networkpolicy` trait's
  policy (`name`), the `postgresql` Pooler and Databases (`poolerName`,
  `databases[].objectName`), the object of every kind component (`objectName`), the source
  a `helm` or `oci` component generates (`source.name`) and the `helm` values ConfigMap and
  Secret (`valuesConfigMapName`, `valuesSecretName`), and the prefix of a `helmtemplate`
  component's hook-group child layouts (`hookGroupNamePrefix`). Still
  none for:
  bundle and ordered-group
  names and synthesized NetworkPolicies (`<c>-allow-ingress-traffic` and others). The Helm release name has one under both deliveries
  (`releaseName`, go-kure/launcher#785, §4.2).
- **Object name = component name** for every kind component unless `objectName` or the
  hook names the object otherwise (§3.2). Two kind components `app` are still refused as a
  duplicate component name (`validateComponent`, `pkg/oam/validate.go`); a Service named
  like its StatefulSet is two components of different names, one given the other's name as
  its `objectName`.
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
- **Shipped: a consumer naming hook** on `TransformContext` (`Naming`, a
  `func(NameRequest) (string, bool)`; `pkg/oam/naming.go`, `pkg/oam/README.md` "Name roles
  and the `Naming` hook"). A name is the author's property, else the hook's answer, else
  the default.
  - The roles are a closed set (`NameRoles`): the application's bundle, each ordered
    group's bundle, each sub-application a trait or a synthesized policy adds, each
    synthesized NetworkPolicy, the `scaler` HPA and PDB, the `rbac` objects and the
    `networkpolicy` trait's policy, the `postgresql` Pooler and Databases, the object
    of an authored kind component, the generated source and values ConfigMap and
    Secret of a `helm` component, and the prefix of a `helmtemplate` component's
    hook-group layouts. A trait
    handler resolves its names with `(*Trait).ResolveName`, a lowering rule with
    `LoweringContext.ResolveName` (`pkg/oam/naming_lowering.go`).
  - An override from the hook is held to the rule for an authored name: never shortened,
    a DNS-1123 subdomain (a DNS-1035 label for the Pooler), refused when invalid or too
    long. Only launcher's own defaults go through the shortening rule (§3.3).
  - A lowering rule's request carries the name of the document the rule is lowering,
    which a later document rule may still change. The hook is asked only inside
    `Transform`: `LowerRaws` and a rule driven directly keep the defaults.
  - `Transformer.ComponentEndpointsNamed` asks the hook the transform's request, so the
    `postgresql` pooler selector follows a hook-given name; `ComponentEndpoints` asks no
    hook. The hook must therefore be a pure function of its request.
  - A sub-application's name is no longer its object's: a hook that renames the
    sub-application of a `configmap`, `secret`, `ingress`, `httproute` or `volsync` trait
    leaves the object's name alone.
- **Shipped: the transform keeps the names of those roles apart.** Two that name one
  object, or one bundle, fail the transform, naming both and where each came from. The
  names a lowering rule resolves are held in the same space as the ones resolved after
  lowering, and two rules that resolve one kind and name for one component are refused
  when the second resolves it. Every other name is still compared only by
  `CheckInDocumentCollisions` over `GenerateApplications`: the objects of a component that
  is not a kind component, the lowering-rule names without a role, and the objects of a
  trait outside the roles.
- **Shipped: `postgresql` `poolerName` and `databases[].objectName`**
  (`PostgresqlRule`, `pkg/oam/builtin/components/postgresql_lowering.go`). The Pooler's
  endpoint selector follows the chosen name. `poolerName` without `pooler.enabled: true`
  is refused, and so is either name when it is already a component of the document.
- **Shipped: `objectName` on every kind component** (`pkg/oam/object_name.go`,
  `pkg/oam/README.md` "`objectName`: the object of a kind component"). It names the
  component's one object and nothing else: labels, selectors and trait-derived names keep
  the component name. The engine reads it and asks the hook under role `object`; a handler
  opts in by declaring its object's kind and scope (`ComponentObjectProvider`), and a test
  walks the registry so that a new type either declares it or is listed as taking none.
  - Only for a component no rule emitted: on a member a component or trait lowering rule
    emitted it is refused and the hook is not asked. The party that writes a reference
    names its target, so a rule that wants a member's name choosable resolves it itself.
    What a document rule or a raw document rule returns is authored input, so it applies
    there.
  - The references launcher writes to the object follow it (`scaleTargetRef`, a routing
    trait's own backend and the Service a route resolves to, the `rbac` subject on a
    `serviceaccount` component, the CloudNativePG endpoint selectors). A reference the
    author writes is written with the object name.
  - It allows a Service named like its StatefulSet as kind components (rule 4 permits
    different kinds to share a name).
- **Shipped: the generated source and values names** (`HelmRule` and `OCIRule`,
  `pkg/oam/builtin/components/helm.go` and `oci.go`; the components README, **helm** and
  **oci**).
  - `source.name` beside an inline source names the source the component generates. It
    was refused there before; alone it still references an existing source. The hook is
    asked under role `helm-source`, once for each source identity of a document and with
    no component, for the shared `<document>-source-<digest>` source of either rule
    (`LoweringContext.ResolveSharedName`). The source an `oci` component keeps to itself
    stays named after the component, with no role.
  - `valuesConfigMapName` and `valuesSecretName` name the `helm` values ConfigMap and
    Secret, under roles `values-configmap` and `values-secret`. A name from the author or
    the hook carries no content hash, so a values-only edit no longer changes the
    HelmRelease: Flux applies it at the release's next reconciliation, within its
    interval, unless the object is watched.
  - These objects land in the Flux namespace when one is set, and their names are claimed
    there (`NameSpec.FluxScoped`).
- **Hook-group names** (`pkg/oam/README.md` "Pipeline" and "Name roles and the `Naming`
  hook"). Role `hook-group` names the prefix of a `helmtemplate` component's hook-group
  layouts, `<prefix>-<NN>-<phase>`, by `hookGroupNamePrefix` on `helmtemplate` and on `helm`
  under `delivery: template`, else the hook, else `<application>-<component>`.
  - It is the one role whose answer is a prefix: the group count is known only after the
    render, which follows the name resolution. Two components resolving to one prefix are
    refused in the transform.
  - Each child carries the name of its Flux Kustomization
    (`ManifestLayout.KustomizationName`). The default is shortened to 63 characters by the
    one shortening rule, while the directory keeps its 253-character name, so the two differ
    for a long default. An authored or hook-given prefix is never shortened, and a child
    name over 63 characters built from it is refused.
  - **Breaking:** under per-layout placement a child's Kustomization was named
    `<bundle's Kustomization>-<child>` (`shop-shop-db-01-main`), and refused over 63
    characters; it is now `shop-db-01-main`.
- **Target, author:** an override for each remaining name of §3.1.
- **Target, consumer:** the hook reaches the remaining sites.
  - A name the `Namer` builds (`NameAllocator.Name` and
    `NameOrAdopt`, `pkg/oam/lowering.go`) reaches the hook only where its rule calls
    `LoweringContext.ResolveName` or `ResolveSharedName`.

### 3.3 Shipped (go-kure/launcher#792, go-kure/launcher#793): uniqueness and shortening

- **go-kure/launcher#793, one shortening rule:** `ShortenName(name, limit)` and
  `ShortenNameWithSuffix(name, suffix, limit)` (`pkg/oam/shorten_name.go`;
  `pkg/oam/README.md` "Names and overrides"). A name that fits is returned unchanged. A
  longer one keeps a prefix, a `-` and the first 10 hex characters of the sha256 of the
  whole name; a fixed suffix is kept whole, unless it leaves less room than the digest
  needs (a long routing `scope`): name and suffix are then shortened together. The caller
  passes the limit:
  - `ShortenLimitLabel` (63): the component label value, `ComponentLabelValue`, and the
    default name of a hook-group child's Flux Kustomization (go-kure/launcher#787, §3.2).
  - `ShortenLimitSubdomain` (253): every object name launcher generates by default, the
    hook-group child layouts and the ordered-group bundles.
  - `ShortenLimitHelmRelease` (53): the one exception to the rule. The result is what Flux
    computes for a HelmRelease, so a release launcher renders itself is named as Flux
    would name it. The default Helm release name passes it (go-kure/launcher#785, §4.2).

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
| `helm` or `helmrelease`, Flux delivery, no Flux namespace | `spec.releaseName` is always written: the authored `releaseName`, else `<component>`, shortened when over 53 characters (`HelmReleaseConfig.releaseName`, `helmrelease.go`; §4.2). | Inline `values`. On `helm` only, `valuesMode: configMap`: a ConfigMap `<component>-values-<hash>`, prepended to `valuesFrom` (`helmValuesConfigMap`, `pkg/oam/builtin/components/helm.go`); `helmrelease` refuses the key (`helmReleaseValuesModeHint`, `helmrelease.go`). On `helm` only, `secretValues`: a Secret `<component>-secret-values-<hash>`, in `valuesFrom` after the values ConfigMap and before the authored entries (§4.3). `valuesFrom` references to out-of-band ConfigMaps or Secrets work. |
| Same, Flux namespace set | The HelmRelease moves to the Flux namespace and launcher defaults `targetNamespace` to the app namespace (`HelmReleaseConfig.Generate`, `helmrelease.go`). The release name is the one of the row above: `spec.releaseName` is written, so Flux does not compute `<appns>-<component>`. | As above. The `helm` values ConfigMap and values Secret follow the HelmRelease into the Flux namespace. |
| `helm` with `delivery: template`, or `helmtemplate` | The same name as `.Release.Name`: the authored `releaseName`, which must be a valid Helm release name, else the default a HelmRelease gets (`templateReleaseName`, `helmtemplate_render.go`; §4.2). The application namespace as `.Release.Namespace` (`chartSource`, `helmtemplate_render.go`); the chart's templates decide the object names. | Inline values, rendered into the output; `secretValues` is merged over them for the render (§4.3). `valuesFrom` is refused: a build cannot read a cluster object. |

Explicit values are written into a Secret by `secretValues` under Flux delivery only
(§4.3).

### 4.2 Shipped (go-kure/launcher#785): release name

- **Before:** launcher wrote no `spec.releaseName` unless one was authored, so Flux named
  the release `<component>`, or `<targetNamespace>-<component>` where a target namespace
  was set, as a Flux namespace sets one by default. A template render used kure's fixed
  default `release` and refused `releaseName`.
- **Now:** the default release name is `<component>` under both deliveries, written
  explicitly (`defaultHelmReleaseName`,
  `pkg/oam/builtin/components/helmtemplate_render.go`):
  - `spec.releaseName` on every HelmRelease. The `helmrelease` kind sets it, so a directly
    authored `helmrelease` gets it too, as the kind already defaults `targetNamespace`
    (`HelmReleaseConfig.Generate`).
  - The template render's `.Release.Name` (`templateReleaseName`).
  - Shortened with the rule of go-kure/launcher#793 at `ShortenLimitHelmRelease`
    (53 characters), which reproduces Flux's own algorithm for that limit (§3.3). A
    default Helm would refuse is a build error that names `releaseName` as the remedy.
  - The author's `releaseName` overrides it under both deliveries: written as it is on a
    HelmRelease, and held to Helm's release name rule under template delivery. The
    consumer override comes from go-kure/launcher#787.
  - Output change: with a target namespace and no authored `releaseName`, the release
    name changes from `<targetNamespace>-<component>` to `<component>`, and Flux installs
    a new release instead of renaming the installed one. A template render no longer runs
    under `release`. The components README states how to keep an installed release.

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
- **A key named `global` below the top level of `secretValues` is refused,** under either
  delivery, naming the path and no value (`refuseNestedGlobal`; go-kure/launcher#794,
  item 9). Helm reads such a key as a dependency's globals and, where their shape conflicts
  with the chart's own, prints the dropped entry in a warning that cannot be intercepted:
  in the build's log under template delivery, in the Flux controller's under Flux
  delivery. Launcher does not read the chart, so a key named `global` that is not a
  dependency's is refused too and can be given through `values` only. A sensitive global
  goes under the top-level `global`, which reaches every dependency and is not printed.
- **The `secret` trait** is new with this ticket (`pkg/oam/builtin/traits/secret.go`). It
  builds through the one Secret path, `ParseSecretProperties` and `GenerateSecret`
  (`pkg/oam/builtin/components/secret.go`), which the `secret` kind of go-kure/launcher#790
  (§6.2) runs too.
- **Policy:** `oam.ExplicitSecretPolicy` (`AllowExplicitSecrets() bool`,
  `pkg/oam/policy.go`) is an interface a `Policy` may also implement. Where it answers
  false these are refused: the `secret` trait, the `secret` kind, `secretValues` on `helm` and on
  `helmtemplate`, and a core Secret a `passthrough` or `manifests` component carries
  (`enforceExplicitSecretObject`, `enforce.go`; told by group and kind, whatever it holds).
  The author then references a Secret created out of band. A policy that does not
  implement the interface allows them, as does no policy. Breaking only for a consumer
  whose policy answers false.
- **Not covered:** a Secret a chart renders under template delivery is emitted under such
  a policy. Its content comes from the chart and its values, and most charts render one.
- **Limits:** a Secret in the output is base64, not encrypted: the output is as sensitive
  as the document. The Secret's name carries 40 bits of a digest of the tree. No refusal of
  the property repeats a value, and the cause of a render that fails with `secretValues` is
  withheld. An error about an object the render produced is not scrubbed: it names that
  object and can quote any part of it the check refuses or locates the refusal by (a
  container's or a volume's name, an image reference, a label key), so a chart that
  renders a sensitive value into the object has it quoted there; the refusal of a
  `scopeOverrides` entry that contradicts a rendered CustomResourceDefinition (§7,
  item 11) is reported as it is, since it names a kind, an object and two scopes, never a
  value. Under template delivery, out-of-band secrets go through the chart's own
  `existingSecret`-style values.

---

## 5. Security on template delivery (go-kure/launcher#791)

### 5.1 Current behaviour

| Check | Flux delivery | Template delivery |
|---|---|---|
| Registry allowlist | Chart source host, on the generated or authored Flux source (`HelmRepositoryConfig.ApplyPolicy`, `pkg/oam/builtin/components/helmrepository.go`, and the other source kinds). Chart images: not checked (`HelmReleaseConfig.ApplyPolicy` is a no-op). | Chart source host, before any fetch, and every image of a rendered workload (`HelmTemplateConfig.ApplyPolicy`, `helmtemplate_policy.go`). |
| Image reference check (`ValidateImageRef`, `common.go`) | No | Yes, on every init and regular container of a rendered workload. |
| Pod security (privileged, host namespaces, hostPath, capabilities; the `Policy` flags, `pkg/oam/policy.go`, read by `enforcePodTemplatePolicy`, `cnpg_common.go`) | No | Yes, on every rendered workload (`enforceRenderedObjectPolicy`, `helmtemplate_policy.go`). A chart rendering a privileged pod is refused unless the policy allows it. |
| PersistentVolume (a `hostPath` or `local` source, `capacity.storage`) | No | Yes, since the `persistentvolume` kind (go-kure/launcher#790): a rendered PersistentVolume is held to what the kind holds its own to (`enforcePersistentVolumePolicy`, `enforce.go`). |
| Namespace | HelmRelease and source in the Flux namespace | **Shipped (go-kure/launcher#794, item 4):** a namespaced object the chart rendered without `metadata.namespace` is given the application namespace, where a Helm install would create it (`stampRenderedNamespaces`, `helmtemplate_render.go`). A namespace the chart wrote is kept, and it is not checked. A cluster-scoped object is left as rendered. So is an object whose scope is unknown (a kind kure does not register, with no CustomResourceDefinition for it among the rendered objects; a chart's `crds/` directory is not rendered): it stays without a namespace, unless `scopeOverrides`, on the `helmtemplate` component or on `helm` under `delivery: template`, states the kind's scope (§7, item 11). |

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
     index names and redirects are not checked (the last two: a decided limit,
     go-kure/launcher#794, item 6, §7).
     A field the vendored API type does not declare is no longer dropped: on a workload, a
     claim or a PersistentVolume it is refused; any other registered kind is emitted as
     rendered, as an unstructured object, with the field kept. One such field is refused on
     every kind: a top-level `items` array on a kind that declares none, since emitted as
     rendered the object would be a list. A key inside a type that unmarshals itself is the
     known limit: it is not reported and is still dropped (go-kure/launcher#794, item 7).
     A `v1` `List` and a typed list are replaced by their items, and each item is held to
     the check and to the undeclared-field rule as a document of its own is
     (go-kure/launcher#790, with the kure commit that reads such lists).

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

- **Shipped: reserved metadata keys.** A consumer names the label and annotation keys it
  keeps to itself in `TransformContext.ReservedMetadataKeys` (exact keys, or a prefix ending
  in `/`), and one rule refuses such a key on every object that reaches the output, whatever
  component type or trait carries it (`pkg/oam/README.md` "Reserved metadata keys";
  `reserved_metadata.go`).
  - It is one check in one place, the ownership wrapper of go-kure/launcher#788 (§3.4),
    so it reads what each config generated: `passthrough`, `manifests`, template delivery,
    the `annotations` of `ingress`, `httproute` and `expose`, and `inheritedMetadata` of a
    CloudNativePG Cluster. A test runs each of these carriers
    (`TestReservedMetadataKeys_EveryCarrier`).
  - Read: an object's own labels and annotations, the pod template's on the kinds that have
    one, and a Cluster's `spec.inheritedMetadata`. Exempt: the `app` label and the component
    label key, and the annotations the platform sets on an Ingress, which the `expose` rule
    now hands to the `ingress` trait in a platform-reserved `platformAnnotations` property.
  - Not covered: a chart Flux renders in the cluster, metadata an object hands on in a field
    of its own (`commonMetadata`, `volumeClaimTemplates`, a job template's own), and what a
    controller adds.
  - Breaking for a document: an `expose` trait's authored annotation that contradicts a
    value the trait writes is refused where the trait's value used to win silently.
  - It lands before the kind components take `labels` and `annotations`, so authored
    metadata on a kind never exists without it. That part is still open: until then a kind
    component's metadata stays not authorable, as the kinds below say.
- **Shipped: the kind inventory.** `pkg/oam/builtin/components/README.md` "Kind
  inventory" has one row per constructor the base library generates, with a status
  (`kind`, `component`, `trait`, `missing`, `held`, `not authorable`), the component or
  trait type and, for a kind held or judged not authorable, the reason. Two tests hold it
  to the code
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
    `flexVolume` source is not checked (a decided limit, go-kure/launcher#794, item 12,
    §7).
    Breaking for a chart, a `passthrough` component or a `manifests` source that holds
    such a PersistentVolume. The other three kinds have nothing to enforce.
- **Shipped: the pod kinds** `pod`, `replicaset`, `replicationcontroller` and
  `podtemplate` (`pod.go`, `replicaset.go`, `replicationcontroller.go`, `podtemplate.go`,
  `pod_template.go`), on the same recipe.
  - `pod` projects `PodSpec`, `replicaset` projects `ReplicaSetSpec`,
    `replicationcontroller` projects `ReplicationControllerSpec`. The pod spec, the one
    under a `template` included, refuses `ephemeralContainers`, `priority` and
    `overhead`, an untagged or `:latest` image and a probe timing written as `0`; a
    controller's template also refuses `activeDeadlineSeconds`.
  - All are held to environment policy by the check the rendered paths run on the same
    object (`enforcePodTemplatePolicy`, and the replica maximum on the two controllers),
    and fill no policy default.
  - A Pod carries the `app` label; a controller's pod template gains it beside the
    authored labels, and an authored `app` with another value is refused. These three
    are targets of `security-context`, a `configmap` mount and an `external-secret`
    injection.
  - A ReplicaSet's `selector` is required; a ReplicationController's is a plain label
    map and optional.
  - `podtemplate` projects a PodTemplate's one field, `template`. A PodTemplate is
    stored, not run: no `app` label, no ServiceAccount reported, and not a trait target
    (§7, item 14).
- **Shipped: six cluster-scoped kinds no environment policy applies to,**
  `storageclass`, `volumeattributesclass`, `priorityclass`, `runtimeclass`,
  `ingressclass` and `csidriver` (one file each, named after the type, in
  `pkg/oam/builtin/components`).
  - One shared helper builds them (`policyFreeKind`, `kind_policy_free.go`): the strict
    decode, a config with nothing to enforce, and a `Generate` that returns the
    base-library constructor's object with a copy of what was decoded. A kind built on it
    is a type, an optional required-field check and a constructor.
  - Each declares its object as cluster-scoped and takes `objectName` (§3.2): the config
    carries the resolved name, `Generate` names the object with it, and the name is
    claimed in no namespace.
  - `ingressclass` and `csidriver` project their spec type. The four classes have no
    spec type: the properties are the object's fields beside its identity, and `kind`,
    `apiVersion` and `metadata` are refused by name.
  - A top-level field the API server refuses an object without must be authored
    (`provisioner`, `handler`, `driverName`, an IngressClass's `controller`, and at least
    one of a VolumeAttributesClass's `parameters`). A PriorityClass `value` is not one:
    unauthored, it is emitted as `0`. Other value rules are left to the API server.
  - Metadata is not authorable, as on every kind component, so a default StorageClass or
    IngressClass (an annotation) cannot be written with these kinds.
  - A CSIDriver's name is not held to the 63 characters the API documents for it: the
    API server does not hold the object to that limit, and refuses a PersistentVolume
    that names a longer driver. The kind refuses what the API server refuses of the
    object and no more.
- **Shipped: the routing kinds** `ingress` and `httproute` (`ingress.go`,
  `httproute.go`), on the recipe of the core kinds above.
  - `ingress` projects `IngressSpec`, `httproute` projects `HTTPRouteSpec`. No field is
    required by the decode and none is filled; the API's value rules are left to the API
    server.
  - Each is an authored object, not the trait of the same type name. The NetworkPolicy
    synthesis reads a routing trait's traffic sources and target component, which a
    kind does not report, so no allow rule is synthesized for it. The trait's other
    rendering inputs do not reach a kind either: the platform's hostname constraint on
    an Ingress, the capability's Gateway as an HTTPRoute's parent.
  - No environment policy applies. The policy's capability lists gate trait types, so a
    policy that forbids the trait does not refuse the component; the rendered
    paths emit the same object under the same terms. A capability gate on component
    types is an open point of go-kure/launcher#790.
- **Shipped: the `networkpolicy` kind** (`networkpolicy.go`), on the same recipe and
  ungated under the same terms.
  - It projects `NetworkPolicySpec`. No field is required by the decode and none is
    filled.
  - It is an authored object, not the trait of the same type name, and reads as the
    API reads it. The trait always selects its component's pods; on the kind an
    unwritten `podSelector` selects every pod of the namespace. The trait lists a
    direction when its key is present; on the kind `policyTypes` is the author's, so
    `egress: []` alone isolates no egress.
  - A null rule, peer or port is refused by its path: decoded, it would be the empty
    one, which allows everything.
- **Shipped: the `cilium-networkpolicy` kind** (`cilium_networkpolicy.go`), ungated
  under the same terms.
  - A CiliumNetworkPolicy has no spec type: the kind projects its `spec` (one rule) and
    `specs` (a list of rules), each the whole Cilium rule.
  - It refuses a policy Cilium rejects: no rule at all, a rule with no
    `endpointSelector`, a rule with a `nodeSelector`, a rule with no entry in `ingress`,
    `ingressDeny`, `egress` or `egressDeny`. Cilium's CRD schema refuses some of these
    at admission; the rest the API server would store and the agent reject when it
    reads the object, unenforced. No selector is filled in.
  - An unknown key inside an endpoint selector is refused by its path, as in the trait:
    the selector unmarshals itself and would drop a misspelt key, leaving the selector
    that matches everything. The positions are found from the Cilium types.
- **Shipped: the `secret` kind** (`secret.go`), the `secret` trait's twin (§4.3).
  - It projects a v1 Secret through the parser and the generator the trait runs
    (`ParseSecretProperties`, `GenerateSecret`): `stringData`, `data`, `type`,
    `immutable`. Every entry is emitted under `data`, base64 and not encrypted, and no
    refusal repeats a value.
  - It is held to the environment policy, unlike the other kinds of this group: a policy
    that forbids explicit secrets (`oam.ExplicitSecretPolicy`) refuses it, as it refuses
    the trait.
  - The trait names its Secret under no role, so a `secret` component and a `secret`
    trait that name one Secret are refused among the generated objects with both named,
    as a `configmap` component and trait are. The Secret a `helm` component generates for
    `secretValues` is claimed under role `values-secret`, so a `secret` component under
    that name is refused as a name collision.
- **Shipped: `servicecidr`, `poddisruptionbudget` and `horizontalpodautoscaler`**
  (`servicecidr.go`, `poddisruptionbudget.go`, `horizontalpodautoscaler.go`), each the
  strict projection of its spec type, declaring its object and taking `objectName`.
  - `servicecidr` (cluster-scoped) and `poddisruptionbudget` (namespaced) are built on
    `policyFreeKind`: no environment policy applies. A ServiceCIDR needs at least one of
    `cidrs`; a PodDisruptionBudget has no required field. That `minAvailable` and
    `maxUnavailable` exclude each other is left to the API server.
  - `horizontalpodautoscaler` (namespaced) requires `scaleTargetRef` and `maxReplicas`
    (an authored `0` counts as unauthored; a negative one is left to the API server),
    and `maxReplicas` is held to `MaxReplicas`, as a rendered HorizontalPodAutoscaler
    is. No policy default is filled.
  - The selector of a budget and the target of an autoscaler are the author's: launcher
    points neither at a component and checks neither against the document. **The
    `scaler` trait's guard on a workload whose claim is not ReadWriteMany does not see a
    `horizontalpodautoscaler` component that targets that workload,** as it does not see
    an autoscaler a chart renders or a `passthrough` component holds.
  - The `scaler` trait's objects and these kinds are claimed as the same kinds, so one
    name given to both in one namespace is a name collision.
- **Held: `endpointslice`.** A slice belongs to a Service only through the
  `kubernetes.io/service-name` label, and a kind component's metadata is not authorable,
  so the kind could not do what it is authored for. Its inventory row is `held`, with
  that reason, until the kinds take authored labels (the point below).
- **Not offered: Endpoints.** Deprecated upstream in favour of EndpointSlice; its
  inventory row is `not authorable` with that note.
- **Decided, not yet shipped: object metadata on kind components.** No kind component
  lets its object's labels or annotations be authored yet. Three concrete cases need them:
  - the `kubernetes.io/service-name` label of an EndpointSlice, without which the slice
    belongs to no Service;
  - the default-class annotation of a StorageClass or an IngressClass
    (`storageclass.kubernetes.io/is-default-class`,
    `ingressclass.kubernetes.io/is-default-class`);
  - the Pod Security Admission labels of a Namespace
    (`pod-security.kubernetes.io/enforce` and its siblings).

  go-kure/launcher#790 decides it: every kind component is to take optional `labels`
  and `annotations`, on the object's own metadata only. That is a change of its own
  and is not in the tree yet.
- **Field gaps** in the hand-parsed kinds (upstream fields with no schema key):
  - `statefulset`: the raw `affinity` shape (it keeps the four-key shorthand);
    `tolerations` and `topologySpreadConstraints` are read;
  - `daemonset`: no scheduling field is left; the raw `affinity`, `tolerations`,
    `topologySpreadConstraints` and `sidecars` are read;
  - `job`, `cronjob`: the raw `affinity`, `tolerations` and `topologySpreadConstraints`
    are read;
  - `job`, `cronjob`: `sidecars` (a plain sidecar keeps the Job's pod from completing;
    it needs the restartable init container below);
  - all workloads: the container fields `restartPolicy` and `restartPolicyRules` (a
    restartable init container, which the package does not model) and the pod field
    `evictionResponders`. `imagePullPolicy`, `terminationMessagePath`,
    `terminationMessagePolicy`, `stdin`, `stdinOnce`, `tty` and `resizePolicy` are read
    on the main container, on an init container and on a sidecar (README "Container
    fields");
  - `service`: a literal `clusterIP`, `clusterIPs` and `externalIPs`, which are
    refused. Every other `ServiceSpec` field is read: `type: ExternalName` with
    `externalName`, the traffic policies, session affinity, the IP families, the
    load-balancer fields, and a port's `nodePort` and `appProtocol`. `type:
    ExternalName` has no capability gate, and a route to such a Service gets no
    synthesized inbound NetworkPolicy (README, the `service` entry);
  - `persistentvolumeclaim`: the long `resources` spelling of `size`. `selector`,
    `dataSourceRef`, `volumeName` and `volumeAttributesClassName` are read, by the kind
    and by the `pvc` trait; `volumeName` has no policy check (README, the
    `persistentvolumeclaim` entry). `dataSource` is refused: `dataSourceRef` supersedes
    it.

  `selector` is refused today with an explicit reason on `deployment`, `statefulset`,
  `daemonset` and `job`, and `template` on `deployment` and `job`
  (`deploymentSpecRejectedKeys`, `statefulSetSpecRejectedKeys`,
  `daemonSetSpecRejectedKeys`, `jobSpecRejectedKeys`, in `deployment_spec.go`,
  `statefulset_spec.go`, `daemonset_spec.go` and `job.go`); each kind's sub-task decides
  whether that refusal stays, with its reason documented. Each kind gets a sub-task in the
  ticket.
- **Missing kinds:** the inventory's `missing` rows (Pod, ServiceMonitor,
  Gateway among them), and its `trait` rows, the
  kinds reachable only as traits today (Certificate, ExternalSecret, Role and RoleBinding,
  ReplicationSource).
  The ticket adds them group by group. A kind kure lacks is added to kure first.

---

## 7. Asymmetries (go-kure/launcher#794)

Each item is fixed, or documented with its reason; none is open. The issue holds the full
list and the disposition of every item:

- **`targetNamespace` default (item 1): documented, no change.** `oci` sets no default;
  `helmrelease` defaults it to the application namespace under a Flux namespace
  (`HelmReleaseConfig.Generate`). The difference is deliberate: a Kustomization's
  `targetNamespace` overrides the namespace of every namespaced object in the artifact,
  where the HelmRelease default only says where the release installs. The `oci` property's
  description says so (`OCIRule.PropertySchema`, `pkg/oam/builtin/components/oci.go`).
  The `fluxcd-kustomization` kind sets none either, for the same reason
  (`FluxcdKustomizationHandler.PropertySchema`, `fluxcd_kustomization.go`).
- **Environment policy on unbuilt objects (items 2 and 10): shipped for `passthrough` and
  `manifests`.** Both are held to the check template delivery runs on a rendered object
  (`enforceRenderedObjectPolicy`, `helmtemplate_policy.go`): the image, pod security,
  resource, storage and replica rules, on every kind that check reads.
  - `passthrough` (`PassthroughConfig.ApplyPolicy`, `passthrough.go`): an object of a
    kind kure's scheme registers is decoded as that kind for the check alone
    (`policyObject`); what is emitted stays the authored object. One that cannot be read
    is refused: a registered kind that does not decode, and a workload kind, a claim or a
    PersistentVolume in an API version the scheme does not register. An object the decoder
    of its kind panics on is refused the same way, as is such a document in a `manifests`
    source or a rendered chart, named by its position, kind and name (a Cilium policy whose
    `icmps` field leaves its `type` out is the known case): a build error, not a crash.
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
  is left as rendered.
- **Helm's values warning quoting a sensitive value (item 9): shipped, as a refusal**
  (go-kure/launcher#786, §4.3). A key named `global` below the top level of `secretValues`
  is refused on `helm`, under either delivery, and on `helmtemplate`
  (`refuseNestedGlobal`, `pkg/oam/builtin/components/helm_secret_values.go`), so the one
  Helm warning that quoted a value of that tree cannot occur. Helm's logging is not
  captured or filtered. The two shapes that can still meet a values conflict, a top-level
  `global` and a dependency's ordinary key, are pinned by a test to print the plain side
  only. Not breaking: `secretValues` is new with go-kure/launcher#786.
- **Scope overrides on template delivery (item 11): shipped.** The `helmtemplate`
  component takes the `scopeOverrides` property `manifests` has, read by the same parser
  and resolved by one function both call (`resolveObjectScope`,
  `pkg/oam/builtin/components/manifests.go`); a test holds the two to the same answer. A
  kind stated `Namespaced` gets the application namespace on an object without one; a kind
  stated `Cluster` is left as rendered, also with a namespace the chart wrote, which
  `manifests` refuses. Refused: a malformed entry, and an entry that contradicts a
  CustomResourceDefinition the chart renders. The `helm` rule takes the property under
  `delivery: template` and forwards it to the `helmtemplate` as written, refusing a
  malformed entry under its own prefix; a test holds a chart delivered through `helm` and
  an authored `helmtemplate` to the same objects for the same list. Under `delivery: flux`
  the rule refuses the property by name, since Helm creates the objects in the cluster and
  nothing in the build could apply a stated scope. Not breaking: a `helm` document that
  set the property was refused before, as an unknown key under either delivery.
- **A pod-spec trait on a component without a workload (item 14): shipped.**
  `security-context`, a `configmap` mount and an `external-secret` injection read one
  list of workloads (`workloadPodSpec`, `pkg/oam/builtin/traits/workload_target.go`): a
  typed Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job,
  CronJob or Pod. On a component that generates none (a `service`, a `helmrelease`, a
  `podtemplate`, any `passthrough` object) all three are refused in one message form,
  naming the trait, the component and the properties that nothing would apply. The
  exception is `security-context` with `psaLevel` alone: it is accepted and writes
  nothing, since the level is a declaration a reader of the document can act on for pods
  the build never sees. A component that generates a workload next to other objects is
  not refused; the trait applies to the workloads. Breaking for a document that set one
  of the six pod-spec properties of `security-context` on such a component: it built,
  with the property applied to nothing, and is refused now. The two mount refusals
  existed already and changed wording only.
- **The scope of a `passthrough` object (item 13): shipped.** `passthrough` resolves
  whether its object is namespaced with the precedence `manifests` gives a
  `scopeOverrides` entry, with `clusterScoped` in the entry's place
  (`resolveClusterScoped`, `pkg/oam/builtin/components/passthrough.go`). For a kind
  whose scope the Kubernetes API governs, the scope table decides, so a PriorityClass
  gets no namespace without the property; a `clusterScoped` that contradicts the table
  is refused, where `manifests` ignores such an override (its list may name kinds the
  source does not hold; the property is a statement about the one object). For any
  other kind an authored `clusterScoped` decides, either way; not authored, the table
  decides for a kind it registers and an unknown kind stays namespaced, the default
  the component always had (`manifests` fails closed there). An inline namespace on an
  object that resolves cluster-scoped is refused. Breaking: an object of a
  cluster-scoped kind the table knows loses the stamped namespace; `clusterScoped:
  true` on a built-in namespaced kind and `false` on a built-in cluster-scoped kind
  stop building; an inline namespace on a cluster-scoped kind the table knows is
  refused without the property too.
- **The chart archive host on template delivery (item 6): documented as a limit, no
  change.** The registry allowlist is checked on the chart's source URL before anything
  is fetched (`HelmTemplateConfig.ApplyPolicy`,
  `pkg/oam/builtin/components/helmtemplate_policy.go`). The archive a Helm repository's
  index names, and any redirect, are followed by kure's chart renderer to whatever host
  they point at. Holding every request to the allowlist needs a hook in that renderer,
  which offers none; that is a change to kure, not to launcher.
- **Undeclared fields in a decoded document (item 7): shipped** (§5.2).
- **A bundle trait on a component that lowers into several groups (item 8): the refusal
  is kept, and the rule is documented.** A trait that configures how a bundle is
  delivered (`fluxcd-patches`, `fluxcd-postbuild`) is forwarded to one member per bundle.
  The groups are computed after lowering has settled, so a rule forwards such a trait
  only where it can tell them, and refuses a document that could separate its members:
  `postgresql` refuses one that authors such a trait and orders or places a generated
  member on its own (`postgresqlMembersShareBundle`,
  `pkg/oam/builtin/components/postgresql_lowering.go`). Applying a forwarded trait once
  per resulting bundle would need an engine feature and is not built. The rule for every
  lowering rule, with the message, is in `pkg/oam/builtin/components/README.md`, "Traits
  a lowering rule forwards".
- **Driver-defined volumes (item 12): documented as a limit, no change.** A `csi` or
  `flexVolume` volume names a driver and options only the driver interprets, so no field
  says whether a node path is exposed; both pass the hostPath gate, on a pod and on a
  PersistentVolume. The environment policy makes no statement about them: one would be a
  new part of the policy every consumer implements, and no use case asked for it.

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
| [go-kure/launcher#782](https://github.com/go-kure/launcher/issues/782) | Delivery intent instead of Flux annotations | §1.3 | Shipped | [go-kure/kure#974](https://github.com/go-kure/kure/issues/974) (delivery intent), go-kure/launcher#781 |
| [go-kure/launcher#783](https://github.com/go-kure/launcher/issues/783) | Explicit ordering only; one bundle shape | §2.2 | Shipped | go-kure/launcher#781; go-kure/launcher#787 for the name override (can follow) |
| [go-kure/launcher#784](https://github.com/go-kure/launcher/issues/784) | `oci` as an upper-level component; new `fluxcd-kustomization` kind | §2.3 | Shipped | — |
| [go-kure/launcher#785](https://github.com/go-kure/launcher/issues/785) | Release name default (rescopes [go-kure/launcher#776](https://github.com/go-kure/launcher/issues/776)) | §4.2 | Shipped | go-kure/launcher#793 |
| [go-kure/launcher#786](https://github.com/go-kure/launcher/issues/786) | Secret values | §4.3 | Shipped | — |
| [go-kure/launcher#787](https://github.com/go-kure/launcher/issues/787) | Name overrides | §3.2 | Partly: authored names used as written or refused; `scaler`, `rbac`, `networkpolicy` and `postgresql` overrides; `objectName` on kind components; the consumer `Naming` hook for the roles of §3.2; the hook-group names and their `hook-group` role | go-kure/launcher#783, go-kure/launcher#793 |
| [go-kure/launcher#788](https://github.com/go-kure/launcher/issues/788) | Component label and provenance | §3.4 | Shipped | — |
| [go-kure/launcher#789](https://github.com/go-kure/launcher/issues/789) | Contract metadata | §6.1 | Shipped | — |
| [go-kure/launcher#790](https://github.com/go-kure/launcher/issues/790) | Full spec and full set of kind components | §6.2 | Partly: the kind inventory; the `namespace`, `limitrange`, `resourcequota`, `persistentvolume`, `pod`, `replicaset`, `replicationcontroller`, `podtemplate`, `storageclass`, `volumeattributesclass`, `priorityclass`, `runtimeclass`, `ingressclass`, `csidriver`, `ingress`, `httproute`, `networkpolicy`, `cilium-networkpolicy`, `servicecidr`, `poddisruptionbudget`, `horizontalpodautoscaler` and `secret` kinds | [go-kure/kure#981](https://github.com/go-kure/kure/issues/981) (missing constructors), go-kure/launcher#787 |
| [go-kure/launcher#791](https://github.com/go-kure/launcher/issues/791) | Security on template delivery | §5.2 | Shipped | — |
| [go-kure/launcher#792](https://github.com/go-kure/launcher/issues/792) | Hook-group child names unique across applications | §3.3 | Shipped | go-kure/launcher#793, go-kure/launcher#787 |
| [go-kure/launcher#793](https://github.com/go-kure/launcher/issues/793) | One shortening rule | §3.3 | Shipped | — |
| [go-kure/launcher#794](https://github.com/go-kure/launcher/issues/794) | Asymmetries | §7 | Shipped | go-kure/launcher#783, go-kure/launcher#784, go-kure/launcher#788 |
| [go-kure/launcher#795](https://github.com/go-kure/launcher/issues/795) | `kurel build` ignores the global `-f/--output-file` (deferred) | §7 | Open | — |
