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
pins, `v0.2.0-beta.15.0.20261005182950-3afb93e06660`: a commit of kure's `main` after
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
  also include a kind component's own object, the source and the values ConfigMap and
  Secret the `helm` rule generates, and the `ingress` trait's Ingress and the `httproute`
  trait's HTTPRoute.
- **Author overrides.** Shipped with go-kure/launcher#787 (§3.2): the `scaler` HPA and PDB
  (`hpaName`, `pdbName`), the `rbac` objects (`name`), the `networkpolicy` trait's
  policy (`name`), the `postgresql` Pooler and Databases (`poolerName`,
  `databases[].objectName`) and its Cluster and ObjectStore (`clusterObjectName`,
  `objectStoreObjectName`), the object of every kind component (`objectName`), the source
  a `helm` or `oci` component generates (`source.name`) and the `helm` values ConfigMap and
  Secret (`valuesConfigMapName`, `valuesSecretName`), the HelmRelease of a `helm`
  component (`helmReleaseName`), the Kustomization of an `oci`
  component and the source it keeps to itself (`kustomizationName`, `source.objectName`),
  the Deployment, the Service and the ServiceAccount of a `webservice` or `worker`
  component (`deploymentObjectName`, `serviceObjectName`, `serviceAccountObjectName`),
  and the prefix of a `helmtemplate`
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
    synthesized NetworkPolicy, the `scaler` HPA and PDB, the `rbac` objects, the
    `networkpolicy` trait's policy, the `ingress` trait's Ingress and the `httproute`
    trait's HTTPRoute, the `postgresql` Pooler and Databases, the object
    of an authored kind component, the generated source, the values ConfigMap and
    Secret and the HelmRelease of a `helm` component, the Kustomization and the kept source of an `oci`
    component, the Deployment, the Service and the ServiceAccount of a `webservice` or
    `worker` component, and the prefix of a `helmtemplate` component's
    hook-group layouts. A trait
    handler resolves its names with `(*Trait).ResolveName`, a lowering rule with
    `LoweringContext.ResolveName`, or with `LoweringContext.ResolveMemberName` for the
    object of a kind component it emits under its component's name
    (`pkg/oam/naming_lowering.go`).
  - An override from the hook is held to the rule for an authored name: never shortened,
    a DNS-1123 subdomain (a DNS-1035 label for the Pooler, for the Service of a
    `webservice` and, of at most 50 characters, for the Cluster of a `postgresql`), refused when invalid or too long. Only launcher's own defaults go through the shortening rule (§3.3).
  - A lowering rule's request carries the name of the document the rule is lowering,
    which a later document rule may still change. The hook is asked only inside
    `Transform`: `LowerRaws` and a rule driven directly keep the defaults.
  - `Transformer.ComponentEndpointsNamed` asks the hook the transform's request, so the
    `postgresql` pooler and cluster selectors follow a hook-given name; `ComponentEndpoints` asks no
    hook. The hook must therefore be a pure function of its request.
  - A sub-application's name is no longer its object's: a hook that renames the
    sub-application of a `configmap`, `secret`, `ingress`, `httproute` or `volsync` trait
    leaves the object's name alone.
- **Shipped: the transform keeps the names of those roles apart.** Two that name one
  object, or one bundle, fail the transform, naming both and where each came from. A
  caller recognises that refusal through the transform's own prefix:
  `errors.Is(err, ErrNameCollision)`, and `errors.As` finds a `*NameCollisionError` with
  the object and the two members that named it (`pkg/oam/name_collision.go`). The
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
    (`LoweringContext.ResolveSharedName`).
  - `kustomizationName` and `source.objectName` name the Kustomization of an `oci`
    component and the source it keeps to itself, under roles `oci-kustomization` and
    `oci-source`, asked with the component; the default of both is the component name.
    The members keep the component's name, the Kustomization's `sourceRef` follows the
    source's name, and both names are claimed (`LoweringContext.ResolveMemberName`).
    `source.objectName` is refused beside `source.name`.
  - `helmReleaseName` names the HelmRelease of a `helm` component, under role
    `helm-release`, asked with the component; the default is the component name. It is
    the object's name, not Helm's: `spec.releaseName` (`releaseName`) and the values
    ConfigMap and Secret keep following the component name. Refused under
    `delivery: template`.
  - `valuesConfigMapName` and `valuesSecretName` name the `helm` values ConfigMap and
    Secret, under roles `values-configmap` and `values-secret`. A name from the author or
    the hook carries no content hash, so a values-only edit no longer changes the
    HelmRelease: Flux applies it at the release's next reconciliation, within its
    interval, unless the object is watched.
  - These objects land in the Flux namespace when one is set, and their names are claimed
    there (`NameSpec.FluxScoped`).
- **Shipped: the objects of a `webservice` and a `worker`** (`WebserviceRule` and
  `WorkerRule`, `pkg/oam/builtin/components/role_members.go`; the components README,
  **webservice / worker**). `deploymentObjectName`, `serviceObjectName` (`webservice` only)
  and `serviceAccountObjectName` name the Deployment, the Service and the ServiceAccount,
  under roles `workload-deployment`, `workload-service` and `workload-serviceaccount`,
  asked with the component; the default of each is the component name, so a document that
  sets none builds as before.
  - Each names its object alone and is claimed (`LoweringContext.ResolveMemberName`). The
    members keep the component's name, and with it the labels, the selectors and the names
    the traits derive.
  - The references launcher writes follow: the `scaler` trait's `scaleTargetRef` the
    Deployment; a routing trait's own backend and the Service a route resolves to the
    Service; the pods' `serviceAccountName` and the `rbac` subject the ServiceAccount.
  - **A renamed Service has another DNS name in the cluster, and launcher builds no such
    address: every address written with the component name is the author's to change.**
  - `serviceAccountObjectName` is refused beside `serviceAccountName`, which names an
    existing account: the component then generates none, and the hook is not asked.
  - A `pvc` volume's `claimObjectName` names the claim the volume generates, under role
    `workload-volume-claim`, asked once per such volume; the default stays
    `<component>-<volume>`, and the volume mounts the claim by the resolved name
    (`LoweringContext.ResolveName`). It is refused beside `claimName`, which references an
    existing claim: the volume then generates none, and the hook is not asked.
  - A sibling group accepts a member that runs no pods and names the account its one
    pod-running member runs as: the same answer, not a second one
    (`checkSiblingGroups`, `pkg/oam/sibling_group.go`).
- **Shipped: the Cluster and the ObjectStore of a `postgresql`** (`PostgresqlRule`,
  `pkg/oam/builtin/components/postgresql.go` and `postgresql_lowering.go`; the components
  README, **postgresql**). `clusterObjectName` and `objectStoreObjectName` name the two
  objects, under roles `postgresql-cluster` and `postgresql-objectstore`, asked with the
  component; the default of each is the component name, so a document that sets neither
  builds as before.
  - Each names its object alone and is claimed (`LoweringContext.ResolveMemberName`). The
    members keep the component's name. The Cluster's name is held to the Cluster's rule, a
    DNS-1035 label of at most 50 characters, whoever chose it.
  - The references launcher writes follow: the Pooler's and each Database's `cluster.name`
    and the `cnpg.io/cluster` endpoint selector (`ComponentEndpointsNamed` with the hook)
    the Cluster; the backup plugin's `barmanObjectName` the ObjectStore. The default names
    of the Pooler and the Databases keep deriving from the component name; a component
    name the Pooler's default cannot be built from is refused, naming `poolerName` as what
    settles it.
  - **CloudNativePG derives the Cluster's Services and Secrets from the Cluster's name, and
    the default backup path moves with it. Renaming an existing Cluster creates a new one:
    the old one is pruned with its data unless it is protected.**
  - `objectStoreObjectName` is refused without `objectStore`: the component then generates
    no store, and the hook is not asked.
- **Hook-group names** (`pkg/oam/README.md` "Pipeline" and "Name roles and the `Naming`
  hook"). Role `hook-group` names the prefix of a `helmtemplate` component's hook-group
  layouts, `<prefix>-<NN>-<phase>`, by `hookGroupNamePrefix` on `helmtemplate` and on `helm`
  under `delivery: template`, else the hook, else `<application>-<component>`.
  - It is the one role whose answer is a prefix: the group count is known only after the
    render, and the prefix is resolved before it. Two components resolving to one prefix are
    refused in the transform. The names built from two different prefixes are not held
    apart there: they exist only after the render, and two that meet are refused by kure
    when the walked tree is integrated.
  - Each child carries the name of its Flux Kustomization
    (`ManifestLayout.KustomizationName`). The default is shortened to 63 characters by the
    one shortening rule, while the directory keeps its 253-character name, so the two differ
    for a long default. An authored or hook-given prefix is never shortened, and a child
    name over 63 characters built from it is refused: by the transform, which has the chart
    rendered by its policy step, and by `AugmentLayout` for a config no transform checked.
    A caller recognises the refusal from either with
    `errors.Is(err, ErrHookGroupNameTooLong)`, and `errors.As` finds a
    `*HookGroupNameError` with the component, the role, the name, its length and the limit
    (`pkg/oam/hook_group_name.go`).
  - **Breaking:** under per-layout placement a child's Kustomization was named
    `<bundle's Kustomization>-<child>` (`shop-shop-db-01-main`), and refused over 63
    characters; it is now `shop-db-01-main`.
- **A chart's layout Kustomization** (`pkg/oam/README.md` "Pipeline" and "Name roles and the
  `Naming` hook"). Role `layout` names the Flux Kustomization kure generates under per-layout
  placement for the layout of a chart (`helmtemplate`, or `helm` under `delivery: template`),
  by `layoutKustomizationName`, else the hook, else kure's own `<bundle>-<component>`.
  - Over 63 characters the default is shortened to 63 by the one shortening rule,
    `-<component>` kept whole (for a component name over 52 characters the whole name is
    shortened instead, as `ShortenNameWithSuffix` does), and set on the layout; kure refused
    it before. Where it fits
    nothing is set and the output does not change. An authored or hook-given name is never
    shortened, and is refused in the transform unless it is a DNS-1123 subdomain of at most 63
    characters.
  - Charts only, and per-layout placement only. The length is measured with the bundle's
    name as launcher made it, not as a consumer renames it afterwards. Not covered: the
    layout of any other component under `ApplicationGrouping: GroupByName`, which keeps
    kure's default.
- **Shipped: the routing traits' objects** (`traits/ingress.go`, `traits/httproute.go`;
  the traits README, "Conventions"). The Ingress and the HTTPRoute are named by `name`,
  else the hook, else the default, under roles `ingress` and `httproute`, and so are those
  of the routing trait an `expose` trait lowers to. The hook's answer names the object
  alone: the trait's sub-application keeps `name`, else the default.
  - Launcher writes no reference to either object by name, and cert-manager's
    ingress-shim names its Certificate after the TLS `secretName`, not the Ingress, so a
    renamed object breaks no reference.
  - The `cilium-networkpolicy` trait's CiliumNetworkPolicy has no role, by design: its
    `name` is required, so the hook is never asked. The name is still claimed.
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
  `pkg/oam/README.md` "Component label and ownership"). The label is authoritative
  (go-kure/launcher#790): where the key is there with a value that is not the component's,
  generation is refused (`pkg/oam/component_label_check.go`). That holds for an object's own
  labels, a pod template's, the labels a CloudNativePG Cluster or Pooler and the Prometheus
  operator kinds hand on to their pods, and the value a workload's selector requires. The
  key must be one nothing else writes. With `app` as the key, the value the built-in kinds
  write for an entry a lowering rule named differently from its component is accepted too,
  and the transform refuses a document in which such an entry's value is another
  component's. Each refusal, and the one of a kind component's `labels` property that
  holds another value under the key, is a `*ComponentLabelError`, found with `errors.As`
  and answering to `ErrComponentLabelValue`: it says which of the four it is, the
  component, the object, the path, the key and the value.
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
    key is absent. A value the chart set under the key that is not the component's is
    refused under template delivery, overwritten when Flux installs.
  - A generated workload whose own selector rules the label out keeps its pod template as
    written, and its pods carry no component label.
  - `GeneratedApplication.Component` is the authored component for the pooler, a database,
    an object store and a component's synthesized NetworkPolicies.
  - A synthesized inbound or egress NetworkPolicy selects the authored component's value,
    the one stamped, also for an entry a lowering rule emitted under another name.
  - A shared generated source is owned by the application: it carries no component label
    and reports an empty component. So does the external-backend NetworkPolicy.
  - Not covered: pods an operator creates from a custom resource whose pod metadata the
    custom resource does not hold.

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
| Image reference check (`ValidateImageRef`, `common.go`) | No | Yes, on every init and regular container and every image volume of a rendered workload. |
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
     launcher-built one. The reference of an image volume is held to it as a container's
     image is (go-kure/launcher#790).
   - **Decided:** with no policy passed, `NoopPolicy` denies a chart that renders a
     privileged container, a host namespace or a hostPath volume, as it does an authored
     workload. The `Policy` flags `AllowPrivileged`, `AllowHostNetwork`, `AllowHostPID`,
     `AllowHostIPC` and `AllowHostPathVolumes` allow one.
   - **Limits:** a workload, claim or PersistentVolume that cannot be decoded typed (an API
     version kure's scheme does not register) is refused, inside a list as outside one; a
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
   - **List handling** (go-kure/launcher#790, with go-kure/kure#1014, where kure's parser
     reads a list in one place). A list of a kind the scheme does not register (a kind
     ending in `List` that states `items`) is replaced by its items as the other two are:
     an item of a registered kind is its Go type, held to the check and to the
     undeclared-field rule, and a list among the items is opened in turn. Launcher's two
     readers of a list, the undeclared-field rule and the hook check, read a list as the
     parser does and nothing else.

     Built before, refused now, for template delivery, `manifests` and `crd`:

     | Document | Before | Now |
     |---|---|---|
     | A list with a label or an annotation on its own metadata | Built, the metadata dropped (template delivery refused `helm.sh/hook` there, and still does) | The parser's error: the list `has metadata of its own that its items cannot keep` |
     | `Kind` or `apiversion` beside the exact key, on a document or a list item | Built, the object emitted. Refused already, with another text: a workload or a claim (the undeclared-field rule), and an item of a typed list whose other-case key states another value than the list holds (the parser) | The parser's error: the key `equals "kind" only after case folding` |
     | `Items` on a `v1` `List` or on a list of an unregistered kind | Built: no object (a `manifests` or `crd` source that held nothing else was refused already, `source resolved to no manifests`), or the list as one object | The same error |
     | A `null` item in a list of an unregistered kind | Built: an otherwise empty object emitted, of the list's kind without `List` | `the item is null, not an object` |
     | An item of a registered kind, in a list of an unregistered kind, that does not decode as its type | Built: the item emitted as written (a workload, a claim or a PersistentVolume there was refused already, as unreadable) | The item's decode error |
     | In a JSON document, an item of a registered kind in a list of an unregistered kind that states `apiVersion` or `kind` and then `null` for it, the stated value not the one its list gives it | Built: the item emitted as written (a workload, a claim or a PersistentVolume there was refused already, as unreadable) | Refused by launcher: the strict decode reads the item as another kind than the parser, so its fields `cannot be checked` |
     | An object of an unregistered kind that does not end in `List`, with a top-level `items` array | Built: the entries emitted in its place | Refused by launcher, with a policy or with none |

     The last two rows are launcher's own rules. The parser gives such an item its list's
     `apiVersion` where the last statement of it is `null`, and the Kubernetes decoder
     keeps the string the `null` follows: where the two differ, the undeclared-field rule
     would check another kind than the one emitted, so an item that came back as a Go type
     is refused. For the
     last row, kure's parser reads such a document as one object. Whatever applies the
     output tells a list by the `items` array and not by the kind, so the entries would be
     applied in the object's place, read by no check. It is the envelope `passthrough`
     refuses, and the one refused on a registered kind that declares no `items`; the three
     readers now hold one rule. Under template delivery the refusal is the component's
     unclassified `oam.ViolationError`, as every render that does not decode is.
     `passthrough` of a registered kind that states `Kind` beside `kind`, or an
     `apiversion` that names a version the kind is registered in, was read and checked,
     and is refused as unreadable.

     Not a refusal: a workload, a claim or a PersistentVolume in a list of an unregistered
     kind was refused as unreadable and is now checked, and builds when the policy allows
     it; a list inside such a list was refused and is now opened. The policy check's
     refusal of an object with a top-level `items` array stays for an object that reaches
     it another way; its text no longer places the object inside a list of an
     unregistered kind.

### 5.3 Shipped (go-kure/launcher#849): refusal classes

A consumer tells one refusal by the environment policy from another by its class, not by
its text:

- `oam.ViolationError` has a `Class` (`pkg/oam/pipeline.go`), filled from the first
  `oam.PolicyRefusal` in the cause chain (`NewViolationError`, `pkg/oam/policy_refusal.go`).
- The classes are a closed set of eleven: host namespace, privileged, host path, container
  capability, registry, resource maximum, storage maximum, replica maximum, explicit
  secret, trait capability and unreadable object (`RefusalClasses`). Every built-in refusal
  by the policy carries one, on every path: a component's `ApplyPolicy`, a trait
  sub-application's, the rendered-object check of template delivery (§5.2), and
  `passthrough` and `manifests` at the transform and at generation (§7).
- No message changed.
- What is not a refusal by the policy is unclassified, an explicit value and never a guess:
  a chart that does not render, a policy default or maximum that does not parse, and the
  library's own rules on an object.
- A consumer's own `Enforceable` gives a class through `oam.NewPolicyRefusal`.
- **Limit:** a redirect of a `manifests` `url` source to a host outside the allowed
  registries stays a fetch failure and no violation; its error holds the refusal with the
  registry class.
- **Breaking** for an unkeyed `oam.ViolationError` literal only.
- The table of classes is in `pkg/oam/README.md`, "Refusal classes".

---

## 6. Contract metadata and kind coverage (go-kure/launcher#789, go-kure/launcher#790)

### 6.1 Shipped (go-kure/launcher#789): contract metadata

- **Before:** no builtin implemented `ContractDescriber`, so `HandlerContracts()` returned
  empty maps. A missing co-registration was found only when a lowered component was
  dispatched, and the message named the type, not the authored component or the rule.
- **Now** (`pkg/oam/README.md` "Contract metadata"):
  - Every built-in handler and lowering rule implements `ContractDescriber`
    (`pkg/oam/handler.go`; one `contract.go` in each of the components, traits and
    policies packages, or the kind's own file for a kind component added since).
    `Family` is the type name, `Version` is `builtin.ContractVersion`
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
  `reserved_metadata.go`). The refusal is a `*ReservedMetadataKeyError`, found with
  `errors.As` and answering to `ErrReservedMetadataKey`: it says the owning component, the
  object, where on it the key is, the key and the entry that reserves it, and its text names
  no Go field.
  - It is one check in one place, the ownership wrapper of go-kure/launcher#788 (§3.4),
    so it reads what each config generated: `passthrough`, `manifests`, template delivery,
    the `annotations` of `ingress`, `httproute` and `expose`, and `inheritedMetadata` of a
    CloudNativePG Cluster. A test runs each of these carriers
    (`TestReservedMetadataKeys_EveryCarrier`).
  - Read: an object's own labels and annotations, the pod template's on the kinds that have
    one, a Cluster's `spec.inheritedMetadata`, a Pooler's pod template,
    `spec.podMetadata` of the Prometheus operator kinds, the `moverPodLabels` of a VolSync
    mover, the pod template of a cert-manager issuer's HTTP01 solvers, a Gateway's
    `spec.infrastructure`, `spec.commonMetadata` of a Flux Kustomization, HelmRelease or
    ArtifactGenerator and a CronJob's job template (all but the first three since
    go-kure/launcher#790, with the component label's check, which also writes the label on
    the job template); and metadata an operator copies onto objects it creates that
    are no pods (a solver's Ingress template and HTTPRoute labels, the Secret templates of a
    Certificate and the external-secrets kinds, a ClusterExternalSecret's
    `externalSecretMetadata`, the Service, ServiceAccount and VolumeSnapshot templates of a
    Cluster and a Pooler, a HelmRelease's `spec.chart.metadata`, a VolSync destination's
    `serviceAnnotations`), for the reserved keys alone (go-kure/launcher#790: nothing
    selects those objects by the component label). Exempt: the `app` label and the component
    label key, and the annotations the platform sets on an Ingress, which the `expose` rule
    now hands to the `ingress` trait in a platform-reserved `platformAnnotations` property.
  - Not covered: a chart Flux renders in the cluster, metadata an object hands on in a field
    of its own (`volumeClaimTemplates`; a test derives the fields that hand metadata on from
    the kinds' API types and holds or lists each: `TestLabelReach_EveryFieldIsHeldOrListed`),
    and what a controller adds.
  - Not read either: what a config that a consumer wraps around an application's config
    after the transform adds. On a layout a config augments, the check reads the objects the
    config's `AugmentLayout` added and leaves what was on the layout before, so a consumer may
    label a rendered chart's objects under a prefix it reserved.
  - Breaking for a document: an `expose` trait's authored annotation that contradicts a
    value the trait writes is refused where the trait's value used to win silently.
  - It landed before the kind components took `labels` and `annotations` (below), so
    authored metadata on a kind never existed without it.
- **Shipped: object metadata on kind components.** Every kind component takes two optional
  properties, `labels` and `annotations`, each a map of strings
  (`pkg/oam/builtin/components/README.md` "The object's labels and annotations";
  `object_metadata.go` in `pkg/oam`).
  - They go on the object's own metadata and nowhere else. The pod template of a workload
    kind is unchanged, and no selector reads an authored label.
  - Refused, naming component, property and key: a key or value the API server refuses,
    the `app` label with another value than the component's, the component label key with
    another value than the component's, a key the kind already sets with another value,
    and a key the consumer reserved (the rule above).
  - The engine reads the two properties, as it reads `objectName`: no handler declares
    them, and a handler finds them on the component (`Component.ObjectMetadata`). One
    test holds every registered kind to them
    (`TestObjectMetadata_EveryKindComponentTakesIt`), so a later kind cannot ship without.
  - A label that names another object is the author's literal and does not follow that
    object's `objectName` or the `Naming` hook. There is no typed reference.
  - It answers four cases: the Pod Security Admission labels of a Namespace, the
    default-class annotation of a StorageClass or an IngressClass, the labels a
    Prometheus selects a ServiceMonitor, a PodMonitor, a Probe or a PrometheusRule by,
    and the `kubernetes.io/service-name` label of an EndpointSlice (the
    `endpointslice` kind, below).
  - Breaking: nothing for a document that built before. A type that declared a property
    named `labels` or `annotations` of its own keeps it only if it declares no object.
- **Shipped: the kind inventory.** `pkg/oam/builtin/components/README.md` "Kind
  inventory" has one row per constructor the base library generates, with a status
  (`kind`, `component`, `trait`, `missing`, `held`, `not authorable`), the component or
  trait type and, for a kind held or judged not authorable, the reason. Two tests hold it
  to the code
  (`TestKindInventory_CoversEveryConstructor`, `TestKindInventory_MatchesCallSites`,
  `kind_inventory_internal_test.go`): a base-library bump that adds a kind fails until the
  table has its row.
- **Shipped: kinds of the Flux APIs beside the sources, the HelmRelease and the
  Kustomization,** `fluxcd-alert` (`fluxcd_alert.go`), `imagepolicy`
  (`imagepolicy.go`), `imageupdateautomation` (`imageupdateautomation.go`) and
  `artifactgenerator` (`artifactgenerator.go`), with
  what such kinds share in `kind_flux.go`: the strict projection of its spec type,
  declaring its object and taking `objectName`. Each is built on `policyFreeKind` with
  two additions. The Flux namespace: the object lands there when one is set, as the
  Flux kinds above do, and reports what it reads there by name (`FluxNamespaceReads`;
  an Alert, an ImagePolicy and an ArtifactGenerator read nothing, an
  ImageUpdateAutomation the Secret of its signing key). And its durations, held to the pattern their fields declare as on the
  Flux kinds above (the `interval` of an ImagePolicy and of an ImageUpdateAutomation).
  - **Nothing gates what such an object reaches outside its namespace:** a source's
    `namespace` on an Alert, the repository's on an ImagePolicy, the GitRepository's
    on an ImageUpdateAutomation, which commits and pushes through it, and a source's on
    an ArtifactGenerator, which copies that source's content into its artifacts, are
    written as authored, as the references and the accounts of a `helmrelease` and a
    `fluxcd-kustomization` are. No environment policy applies and no `Policy` method is
    added.
  - **In the Flux namespace such an object shares its namespace with every other
    application's Flux objects,** so a reference without a namespace and a selector over
    the object's namespace reach them with no `namespace` written: an
    ImageUpdateAutomation without a `policySelector` selects every ImagePolicy there and
    commits their selections through its own GitRepository. Nothing gates this either.
  - Required fields follow the rule of the Prometheus operator's kinds, and are derived
    as theirs are, from the `+required` markers of the linked modules' Go source: the
    API modules of the Flux controllers ship no CRD, so no list is held to the API
    server's validator.
  - **The APIs' expression rules are not checked,** for the same reason: a check is
    held to the API server's validator (go-kure/launcher#874), which answers from a
    CRD. An ImagePolicy with `interval` and no `digestReflectionPolicy: Always`, or the
    reverse, builds and is refused at apply; so does an ArtifactGenerator without a
    `pathPattern` whose artifact is not named as a Kubernetes object. The rules are listed from the markers of
    the linked source, and a test fails on one added or reworded.
  - No default is filled, and the API's other value rules are the API server's.
- **Shipped: five kinds of the Gateway API's `gateway.networking.k8s.io/v1`
  infrastructure objects,** `gatewayclass`, `gateway`, `listenerset`, `referencegrant`
  and `backendtlspolicy` (`gatewayclass.go`, `gateway.go`, `listenerset.go`,
  `referencegrant.go`, `backendtlspolicy.go`, with what they share in
  `gateway_common.go`), each the strict projection of its spec type, declaring its object
  and taking `objectName`. A GatewayClass is cluster-scoped, the other four namespaced.
  All five are built on `policyFreeKind`, unchanged: no environment policy applies, no
  default is filled and no `Policy` method is added.
  - **No capability is required and nothing gates them,** a GatewayClass and a
    ReferenceGrant (which lets another namespace refer into the build namespace)
    included: on a cluster without the Gateway API's CRDs the component builds and the
    object is refused at apply. The `gateway` kind is not the Gateway a capability names
    for the `httproute` trait.
  - Required fields follow the rule of the Prometheus operator's kinds: a field the API
    requires that the Go type writes whether or not it was authored must be authored. A
    test holds each list to the CRDs the linked module ships (4 paths for a gatewayclass,
    26 for a gateway, 6 for a listenerset, 7 for a referencegrant, 9 for a
    backendtlspolicy), read in the experimental channel's CRDs, which hold every field
    the Go types do, and held to agree with the standard channel's. A `gateway` and a
    `listenerset` also hold the `key` and `operator` of a match expression in a namespace
    selector (`allowedRoutes.namespaces.selector`, `allowedListeners.namespaces.selector`),
    presence only (go-kure/launcher#790); those are derived from the CRDs with the rest of
    the list, and the test of the label selectors shows, in both channels, what the API
    server answers for an expression without one.
  - **A field the API requires and the type omits when it is not authored is refused by
    the kind itself,** as `servicecidr` refuses a missing `cidrs`, absent or authored
    empty: a `listenerset` with no `listeners`, a `backendtlspolicy` with no
    `targetRefs`, and **a ListenerSet listener without its `name`, `port` or
    `protocol`,** which a Gateway's listener must author too. The same test derives
    that set from the CRDs, at every depth, and holds each member to the kind's refusal
    or to a stated reason the decoded value cannot show the omission; there are five,
    all refused.
  - `defaultScope` on a Gateway is an experimental-channel field, the only one of the
    five specs; a test holds that.
  - **No host these objects name is held to the allowed registries** (a listener's
    hostname, a requested address, the hostname and subject alternative names a
    BackendTLSPolicy validates): none is an artifact source. **No field holds a literal
    secret and none is checked:** a certificate is a reference, and the free maps a
    controller defines (`tls.options`, `options`) are written as authored.
  - The pods a controller starts for a Gateway are not sized by the object, so the
    policy's maxima have nothing to hold. Labels and annotations are the `labels` and
    `annotations` properties, as on every kind component.
- **Shipped: three kinds of cert-manager's `cert-manager.io/v1` API,** `issuer`,
  `clusterissuer` and `certificate` (`issuer.go`, `clusterissuer.go`, `certificate.go`,
  with what they share in `certmanager_common.go`), each the strict projection of its
  spec type, declaring its object and taking `objectName`. An Issuer and a Certificate
  are namespaced, a ClusterIssuer cluster-scoped. `certificate` is also a trait type;
  the two are separate lists, and the trait is unchanged.
  - **No capability is required and nothing gates them,** a ClusterIssuer included: on
    a cluster without cert-manager's CRDs the component builds and the object is refused
    at apply.
  - **The environment policy reaches two fields, so these are not built on
    `policyFreeKind` alone.** `policyHeldKind` (`kind_policy_held.go`) is that helper,
    unchanged, with an `ApplyPolicy` that asks one function of the kind. An issuer's ACME
    HTTP01 solver pod template sizes a pod cert-manager starts: its cpu and memory are
    held to the policy's maxima, and nothing else of it is (it names no image and no
    security context of a container). A Certificate's keystore password written into the
    object is refused under a policy that forbids explicit secrets. No default is filled
    and no `Policy` method is added.
  - Required fields follow the rule of the Prometheus operator's kinds: a field the API
    requires that the Go type writes whether or not it was authored must be authored. A
    test holds each list to the CRDs the linked module ships (96 paths for an issuer, 8
    for a certificate). An issuer has none at the top level; a required field under a
    parent the author left out is not asked for. Three of an issuer's paths are fields
    of a Kubernetes or Gateway API type an ACME HTTP01 solver embeds (the terms of a
    required node affinity, the name of a parent reference): the CRDs refuse each as the
    type writes it unauthored, which a second test shows with the CRDs' validator. An
    issuer's list also holds the `key` and `operator` of a match expression in the label
    selectors of an HTTP01 solver's pod affinity, presence only (go-kure/launcher#790);
    those are derived from the same CRDs with the rest of the list, and the test of the
    label selectors shows what the API server answers for an expression without one.
  - **cert-manager's validating webhook refuses more than the CRDs do, and launcher
    repeats none of it:** an issuer of no type or of two, a keystore with a password
    beside a reference that names a Secret or with neither, a certificate that names no
    subject. Such a component builds and is refused at apply. The one rule the CRDs
    write as an expression (a `venafi` issuer names exactly one platform) is not
    repeated either.
  - **No host these objects name is held to the allowed registries** (an ACME directory,
    a Vault server, a certificate platform, a DNS server, a CRL or OCSP endpoint): none
    is an artifact source. **No field of an issuer is checked for a literal secret:** a
    credential is a reference to a Secret, and the free JSON of a webhook solver's
    `config` is written as authored.
  - A duration is carried in Go's spelling (`2160h` as `2160h0m0s`). Labels and
    annotations are the `labels` and `annotations` properties, as on every kind
    component.
- **Shipped: the four kinds of Cilium's BGP control plane, `cilium.io/v2`,**
  `cilium-bgpadvertisement`, `cilium-bgpclusterconfig`, `cilium-bgpnodeconfigoverride`
  and `cilium-bgppeerconfig` (`cilium_bgpadvertisement.go`, `cilium_bgpclusterconfig.go`,
  `cilium_bgpnodeconfigoverride.go`, `cilium_bgppeerconfig.go`, with what they share in
  `cilium_bgp_common.go`), each the strict projection of its spec type, built on
  `policyFreeKind`, cluster-scoped, declaring its object and taking `objectName`. The
  type names carry a prefix: MetalLB has a BGPAdvertisement too.
  - **No capability is required and nothing gates them:** on a cluster without Cilium's
    CRDs the component builds and the object is refused at apply.
  - The required lists follow the rule of the Prometheus operator's kinds, read from the
    CRDs of the linked module: a
    test holds each list to the CRD's `required` entries the Go type would write
    unauthored.
  - The CRDs carry six expression rules. The five on an advertisement entry (`service`
    with type `Service` and only with it, `interface` with type `Interface` and only
    with it, no `selector` with type `PodCIDR`) are checked: each compares authored
    fields. The one on a peer configuration's `timers` (`keepAliveTimeSeconds` not
    larger than `holdTimeSeconds`) is checked too; with one authored, the other is
    the default the API server fills before it evaluates the rule, and the kind
    compares with that default (30 and 90, the linked CRD's, held to it by a test)
    and names it in the refusal. A test fails on a rule of the linked CRDs that is neither
    listed as checked nor as left, and holds each checked rule to the API server's own
    expression validator, run over the linked CRD after its defaults: the validator
    refuses what breaks the rule and accepts what is next to it, and the kind agrees on
    both.
  - **No host these objects name is held to the allowed registries** (a peer's
    address, a session's local address, a router ID): none is an artifact source. **No
    field is checked for a literal secret:** `authSecretRef` is the name of a Secret,
    and no field holds a password.
  - A peer configuration selects its advertisements by label: those are written under
    the advertisement's `labels`, as on every kind component. An override takes effect
    on the CiliumBGPNodeConfig of the same name, which `objectName` sets.
- **Shipped: five more kinds of Cilium's API, `cilium.io/v2`,** `cilium-cidrgroup`,
  `cilium-loadbalancerippool`, `cilium-egressgatewaypolicy`, `cilium-localredirectpolicy`
  and `cilium-nodeconfig` (`cilium_cidrgroup.go`, `cilium_loadbalancerippool.go`,
  `cilium_egressgatewaypolicy.go`, `cilium_localredirectpolicy.go`,
  `cilium_nodeconfig.go`), each the strict projection of its spec type, built on
  `policyFreeKind`, declaring its object and taking `objectName`. The first three are
  cluster-scoped; the redirect policy and the node configuration are namespaced.
  - **No capability is required and nothing gates them,** as for the BGP kinds.
  - The required lists are read from the CRDs of the linked module and held to them by
    a test, with the helper the BGP kinds use for a selector's match expressions.
  - A redirect frontend takes exactly one of `addressMatcher` and `serviceMatcher`: the
    CRD's schema says so, it is one comparison of authored fields, and the kind refuses
    neither and both.
  - The CRDs carry five expression rules and none is checked. That an `egressIP` is an
    IP address (twice) is one field's value rule. The three on a redirect policy refuse
    a change of the stored object, which a build does not have. A test fails on a rule
    of the linked CRDs that is not listed as left.
  - Three defaults of the CRDs sit on fields the Go type omits when empty (`disabled`,
    `skipRedirectFromBackend`, `egressGateways`). Each default is that empty value, so
    an authored `false` or `[]` is left out and filled back the same; a test holds the
    defaults to that.
  - **No address or CIDR these objects hold is held to the allowed registries:** none
    is an artifact source, and no field names a host. **No field is checked for a
    literal secret:** none refers to a Secret, and a node configuration's `defaults` is
    a free map of strings, written as authored and not checked.
  - A CIDR group is referred to by its name, which `objectName` sets, or selected by
    the labels written under its `labels`, as on every kind component.
- **Shipped: the `cilium-clusterwidenetworkpolicy` kind**
  (`cilium_clusterwidenetworkpolicy.go`), the cluster-scoped counterpart of the
  `cilium-networkpolicy` kind: `spec`, `specs` or both, each a Cilium rule, with that
  kind's decode and its refusal of an unknown key inside a selector. It declares its
  object and takes `objectName`. **No capability is required and nothing gates it.**
  - It refuses what the CRD's schema refuses of the two fields and is one comparison of
    authored fields: an object with no rule, a rule that authors both or neither of
    `endpointSelector` and `nodeSelector`, and a rule with no entry in any of its four
    lists. The choices deeper in a rule (how a CIDR entry names its addresses, a DNS
    entry's name or pattern, a port's layer 7 protocol) and every value rule are left to
    the API server. A test lists every choice and expression rule of the linked CRD as
    checked or left, and another holds the refusal of an object with no rule to the API
    server's own expression validator.
  - The required list of a rule is read from the CRD and held to it by a test: 59
    fields the Cilium type writes whether or not they were authored. Two optional
    fields are required as well, a listener's `priority` and the `kind` of its Envoy
    configuration, because the type writes an unauthored one as `0` and as the empty
    string, and the API refuses both.
  - **Hosts are not checked:** the names a rule holds (`toFQDNs`, DNS rules, an HTTP
    `host`, server names) and its CIDRs are not artifact sources and are not held to
    the allowed registries. **No literal secret is checked:** an HTTP header match's
    `value` is written as authored, and a Secret a rule refers to is not looked for.
- **Shipped: the `cilium-networkpolicy` kind** (`cilium_networkpolicy.go`), ungated
  under the terms of the routing kinds.
  - A CiliumNetworkPolicy has no spec type: the kind projects its `spec` (one rule) and
    `specs` (a list of rules), each the whole Cilium rule.
  - It refuses a policy Cilium rejects: no rule at all, a rule with no
    `endpointSelector`, a rule with a `nodeSelector`, a rule with no entry in `ingress`,
    `ingressDeny`, `egress` or `egressDeny`. Cilium's CRD schema refuses some of these
    at admission; the rest the API server would store and the agent reject when it
    reads the object, unenforced. No selector is filled in.
  - It refuses a required field that was not authored, by its path: the fields the
    CiliumNetworkPolicy CRD requires inside a rule that the Cilium type writes whether
    or not they were authored (a match expression's `key` and `operator`, an
    `authentication`'s `mode`, a TLS context's `secret`), and a listener's `priority`
    and the `kind` of its `envoyConfig`, which the type writes with a value the API
    refuses. Such a document was emitted before, with the empty value in place of the
    field. A test holds the list to the CRD of the linked module.
  - The `cilium-networkpolicy` trait refuses the same, under the three fields of a rule
    it publishes (`endpointSelector`, `ingress`, `egress`): it decodes into the same
    type, and a trait that left such a field out was emitted with the empty value too.
    Its list is the kind's, cut to those fields, and a test holds it to the same CRD.
    Both read the list from the internal `pkg/oam/internal/requiredfields`.
  - An unknown key inside an endpoint selector is refused by its path, as in the trait:
    the selector unmarshals itself and would drop a misspelt key, leaving the selector
    that matches everything. The positions are found from the Cilium types.
- **Shipped: the four kinds of the External Secrets Operator, `external-secrets.io/v1`,**
  `secretstore`, `clustersecretstore`, `externalsecret` and `clusterexternalsecret`
  (`secretstore.go`, `clustersecretstore.go`, `externalsecret.go`,
  `clusterexternalsecret.go`, with what they share in `externalsecrets_common.go`), each
  the strict projection of its spec type, declaring its object and taking `objectName`.
  The two cluster kinds are cluster-scoped. All four are built on `policyHeldKind`.
  - **No capability is required and nothing gates them:** on a cluster without the
    operator's CRDs the component builds and the object is refused at apply. The
    cluster's `external-secret` capability is the trait's; the kinds do not read it,
    and an `externalsecret` names its own store.
  - The required lists follow the rule of the Prometheus operator's kinds, read from the
    Go source of the linked
    module, which ships no CRD. **A store's list is generated** (251 paths over every
    provider at this pin, in `zz_generated_externalsecrets_required.go`), and a test
    fails where the file and the derivation differ. Four more fields of a store are
    ones the API would default and the type always writes, so the default never
    applies and the value must be written: **a Vault store writes its `version`.** The
    `key` and `operator` of a match expression must be authored too, presence only
    (go-kure/launcher#790), in a store condition's `namespaceSelector` and in a
    cluster external secret's `namespaceSelector` and `namespaceSelectors`: the same
    test derives them from the source of the linked Kubernetes type by the schema
    generators' rule.
  - An authored `false` or `0` the type would omit and the API would replace with
    another default is refused, on three fields of single providers.
  - Of the API's count and expression rules two are checked: a store configures exactly
    one provider, and a `data` entry's `sourceRef` names no generator (the object
    always carries its `storeRef`; `dataFrom` takes a generator). The others, and what
    the operator's validating webhook adds (an external secret with neither `data` nor
    `dataFrom`, among others), are left to the API server. A test fails on a rule of
    the linked source that is neither listed as checked nor as left.
  - **A credential written into a store is refused under an environment policy that
    forbids explicit secrets:** the `value` of seven credential fields that also take a
    reference to a Secret, and the data of the `fake` provider. An identifier in the
    same shape of field is not. **Not checked, under any policy:** a webhook's headers
    and body, a Vault provider's headers, the user part of a URL and the template of
    the Secret an external secret writes, where a reference and a literal look alike.
    **No host these objects name is held to the allowed registries** (a provider's
    server, URL or endpoint): none is an artifact source.
  - **An object an external secret has the operator write instead of a Secret
    (`target.manifest`) gets no more than the same policy gives that kind on
    `passthrough`.** Its content is rendered in the cluster and cannot be read at
    build, so a kind the rendered-object check reads (a workload, a claim, a
    PersistentVolume, a HorizontalPodAutoscaler, in any version) is refused, under
    every policy and under the default one; a core Secret is refused where
    `passthrough` refuses it; any other kind passes as it would there, and whether
    the operator writes it at all is the cluster's (the operator's generic-target
    setting and its RBAC). That is stricter than `passthrough` where the policy
    would pass the object it read (a claim under a policy with no storage maximum,
    an autoscaler under one with no replica maximum): here there is nothing to
    read. An `apiVersion` there that is no API version (a version, or a group and
    a version, each a name the API can have) is refused under every policy, since
    nothing can write such an object and, where the value does not split into the
    two, its kind cannot be read.
  - A trait's ExternalSecret and an `externalsecret` component's are one kind: given
    one name in one namespace they are refused as a collision.
- **Shipped: `role`, `rolebinding`, `clusterrole`, `clusterrolebinding`** (`role.go`,
  `rolebinding.go`, `clusterrole.go`, `clusterrolebinding.go`, with what they share in
  `rbac_common.go`), the projections of the four objects of the
  `rbac.authorization.k8s.io/v1` API on the shared helper `policyFreeKind`. The first
  two are in the build namespace, the last two cluster-scoped.
  - **They are ungated: no capability and no environment-policy check restricts what a
    role grants,** nor to whom a binding grants it.
  - None has a spec: the properties are the object's own fields (`rules`; `rules` and
    `aggregationRule`; `subjects` and `roleRef`), strictly decoded, and its `kind`,
    `apiVersion` and `metadata` are refused.
  - The required lists (a rule's `verbs`; a binding's `roleRef` with its `kind` and
    `name`, and a subject's `kind` and `name`; the `key` and `operator` of a match
    expression of an aggregation selector) are derived from the markers of the linked
    `k8s.io/api` module by the test that derives the `endpointslice` kind's, which also
    fails on a required field the type omits when unauthored: the four have none.
  - Beyond the markers, the kinds check presence rules read by hand from the API
    server's validation at Kubernetes v1.37.1, which no linked module holds and no
    test derives: a rule names `apiGroups` and `resources`, or in a ClusterRole
    `nonResourceURLs` instead, which are refused in a Role and beside resources; a
    ClusterRoleBinding's ServiceAccount subject names its `namespace`; an
    `aggregationRule` holds a selector. A match expression of such a selector is also
    held to the shared label-selector check: an operator that is none of the four, and
    `values` that do not go with the operator, are refused. The form of any other value
    that validation checks is left to the API server; whether a verb, a resource or an
    API group exists is checked by neither.
  - `roleRef.apiGroup` may be left out: the object carries it empty and the API server
    fills the RBAC group. `roleRef.name` and the names of subjects are the author's
    literals and follow no component's `objectName`.
  - An aggregated ClusterRole is emitted with `rules: null` when `rules` is not
    authored and with `[]` when authored so; the control plane fills the rules.
  - They stand beside the `rbac` trait, which grants to a workload's own
    ServiceAccount. A trait's object and a component's of one kind and name are
    refused as a name collision.
- **Shipped: six cluster-scoped kinds no environment policy applies to,**
  `storageclass`, `volumeattributesclass`, `priorityclass`, `runtimeclass`,
  `ingressclass` and `csidriver` (one file each, named after the type, in
  `pkg/oam/builtin/components`).
  - One shared helper builds them (`policyFreeKind`, `kind_policy_free.go`): the strict
    decode, a config with nothing to enforce, and a `Generate` that returns the
    base-library constructor's object with a copy of what was decoded. A kind built on it
    is a type, an optional required-field check, an optional list of required fields the
    type writes unauthored, and a constructor.
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
  - A default StorageClass or IngressClass writes its default-class annotation under
    `annotations`, as every kind component does.
  - A CSIDriver's name is not held to the 63 characters the API documents for it: the
    API server does not hold the object to that limit, and refuses a PersistentVolume
    that names a longer driver. The kind refuses what the API server refuses of the
    object and no more.
- **Shipped: `endpointslice`** (`endpointslice.go`), the projection of a
  `discovery.k8s.io/v1` EndpointSlice on the shared helper `policyFreeKind`, in the
  build namespace. It was held until the kinds took `labels` (above).
  - An EndpointSlice has no spec: the properties are the object's own fields
    (`addressType`, `endpoints`, `ports`), strictly decoded, and its `kind`,
    `apiVersion` and `metadata` are refused.
  - A slice belongs to a Service only through the `kubernetes.io/service-name` label,
    authored under `labels`. It is the author's literal and does not follow a
    Service's `objectName`.
  - The required list (`addressType`, an endpoint's `addresses`, the `name` of a zone
    or node hint) is the fields the API's source marks required and the type writes
    whether or not they were authored. A test derives it from the markers of the
    linked `k8s.io/api` module, and fails on a required field the type omits when
    unauthored: the kind has none. The check is of presence; the form of every value
    is left to the API server.
  - The type writes `endpoints`, `ports` and an endpoint's `conditions` when they
    are not authored, and the API does not require them: the object carries them
    empty (`null`, `null`, `{}`).
  - **Hosts are not checked:** the addresses of an endpoint, FQDNs included, are not
    artifact sources and are not held to the allowed registries.
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
- **Shipped: the routing kinds** `ingress` and `httproute` (`ingress.go`,
  `httproute.go`), on the recipe of the four core kinds.
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
- **Shipped: four core kinds,** `namespace`, `limitrange`, `resourcequota` and
  `persistentvolume` (`namespace.go`, `limitrange.go`, `resourcequota.go`,
  `persistentvolume.go` in `pkg/oam/builtin/components`).
  - Each has one schema key per json field of the object's spec type, and the property
    map is decoded strictly into that type (`decodeKindSpec`, `kind_decode.go`), as the
    CloudNativePG kinds are. A test holds each schema to the type by reflection
    (`TestCoreKindSchemas_CoverSpec`).
  - Each emits one object named after the component, with the authored spec. The handler
    writes no label: the object carries the component label of go-kure/launcher#788
    (§3.4) and what `labels` authors, the Pod Security Admission labels of a Namespace
    among them.
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
- **Shipped: five kinds of MetalLB's `metallb.io/v1beta1` API,**
  `metallb-ipaddresspool`, `metallb-l2advertisement`, `metallb-bgpadvertisement`,
  `metallb-bfdprofile` and `metallb-community` (`metallb_ipaddresspool.go`,
  `metallb_l2advertisement.go`, `metallb_bgpadvertisement.go`, `metallb_bfdprofile.go`,
  `metallb_community.go`, with what the kinds of this API share in
  `metallb_common.go`), each the strict projection of its spec type, built on
  `policyFreeKind`, declaring its object and taking `objectName`. The type names carry
  the `metallb-` prefix as the kinds of Cilium's API carry theirs: both APIs have a pool
  of addresses and an advertisement for BGP.
  - A pool says which Services, in which namespaces, MetalLB gives an address of which
    range: with no `serviceAllocation`, a Service of any namespace.
  - An L2 advertisement says which pools' addresses MetalLB announces on the local
    network, from which nodes and interfaces, for which Services. No field of it is
    required and each narrows it: one that authors nothing limits none of them.
  - A BGP advertisement says which pools' addresses MetalLB announces to which BGP
    peers, for which Services, rolled up into which prefix length and with which
    LOCAL_PREF and communities. No field of it is required either, and one that authors
    nothing announces every pool to every peer.
  - A BFD profile is the timers of the BFD session of the BGP peers that name it, and
    so how fast the loss of such a peer is noticed. No field of it is required. Every
    field is a pointer, so an authored 0 or false is written; the bounds the CRD sets
    on the numbers are the API server's.
  - A Community gives names to BGP community values, and a BGP advertisement that
    names one attaches its value to what it announces. No field of it is required, of
    the spec or of an alias; the form of a value and a name defined twice are not
    read.
  - They are namespaced and written in the build namespace. MetalLB reads its objects
    in one namespace and in no other: the one it is configured to watch (its
    `--namespace` flag or `METALLB_NAMESPACE`), by default the one it runs in;
    launcher does not know that namespace.
  - **No capability is required and nothing gates them.**
  - The required lists are read from the CRDs of the linked module and held to them by
    a test: a pool's `addresses`, and the key and the operator of a selector's match
    expression in the three kinds that hold selectors.
  - The defaults the CRDs declare are carried: a pool's `autoAssign` and a BGP
    advertisement's two aggregation lengths sit on pointers, and `avoidBuggyIPs`
    defaults to the `false` the Go type omits; a test holds the defaults to that. The
    CRDs of the L2 advertisement, of the BFD profile and of the Community have none.
  - **The one expression rule these CRDs declare is checked:** a BGP advertisement's
    `serviceSelectors` is refused beside an aggregation length other than 32 (IPv4) or
    128 (IPv6). A test holds the kind's answer to the API server's, after the defaults
    the API server fills, and fails on a rule that is added or reworded. The CRD's
    other value rules (the minimum of `aggregationLength`) are the API server's.
  - **MetalLB's validating webhook was not read, and nothing it refuses is repeated.**
  - **No address a pool holds is held to the allowed registries:** none is an artifact
    source. **No field is checked for a literal secret:** none holds one, and none
    refers to a Secret.
- **Shipped: the `networkpolicy` kind** (`networkpolicy.go`), on the recipe of the four
  core kinds and ungated under the terms of the routing kinds.
  - It projects `NetworkPolicySpec`. No top-level field is required and none is
    filled. A match expression of a selector is refused without its `key` or
    `operator`, with an unknown operator, or with `values` that do not go with the
    operator.
  - It is an authored object, not the trait of the same type name, and reads as the
    API reads it. The trait always selects its component's pods; on the kind an
    unwritten `podSelector` selects every pod of the namespace. The trait lists a
    direction when its key is present; on the kind `policyTypes` is the author's, so
    `egress: []` alone isolates no egress.
  - A null rule, peer or port is refused by its path: decoded, it would be the empty
    one, which allows everything.
- **Shipped: the pod kinds** `pod`, `replicaset`, `replicationcontroller` and
  `podtemplate` (`pod.go`, `replicaset.go`, `replicationcontroller.go`, `podtemplate.go`,
  `pod_template.go`), on the recipe of the four core kinds.
  - `pod` projects `PodSpec`, `replicaset` projects `ReplicaSetSpec`,
    `replicationcontroller` projects `ReplicationControllerSpec`. The pod spec, the one
    under a `template` included, refuses `ephemeralContainers`, `priority` and
    `overhead`, an untagged or `:latest` image and a probe timing written as `0`; a
    controller's template also refuses `activeDeadlineSeconds`.
  - All are held to environment policy by the check the rendered paths run on the same
    object (`enforcePodTemplatePolicy`, and the replica maximum on the two controllers),
    and fill no policy default.
  - That check holds an image volume's `reference` (`volumes[].image`) to
    `AllowedRegistries`, as it holds a container's image, on every path that produces a
    pod spec: these four kinds, the `cnpg-pooler` template, and a workload template
    delivery, `passthrough` or a `manifests` source carries. A test derives the image
    fields of a pod spec from the linked `k8s.io/api` type (`TestImageFields_HeldOrListed`).
    Breaking for a document, a chart or a source whose pod names an image volume from a
    registry outside the list. Not covered: a custom resource those three paths carry.
  - The `cnpg-cluster` kind holds the image of each `postgresql.extensions[]` entry to
    the same list, as it holds `imageName`; the same test derives the image fields of
    the Cluster and the Pooler spec. Breaking for a `cnpg-cluster` whose extension image
    names a registry outside the list.
  - Every one of those fields is held to the tag rule too (`ValidateImageRef`: a tag or a
    digest, no `:latest`), with or without a policy: an image volume's `reference` on each
    of those paths, `pgbouncer.image` and the template images of `cnpg-pooler`,
    `imageName` and the extension references of `cnpg-cluster`, and the image a
    `postgresql` component composes from `version`, refused under `version`. The same
    test requires both rules on each field it derives. A field that names no image is
    not checked, and a digest without a tag passes; CloudNativePG's own rules on
    `imageName` are left to the operator. Breaking for a document, a chart or a source
    that names an untagged or `:latest` image in one of those fields.
  - The `cnpg-cluster` and `cnpg-pooler` kinds refuse a string their CRD requires and
    bounds (a minimum length, an enumeration, a pattern) that the Go type writes as `""`
    when it is unauthored under an authored parent: eleven of a Cluster (among them
    `replica.source`, `postgresql.synchronous.method` and
    `backup.barmanObjectStore.destinationPath`; the list is in the components README) and
    the `key` of a Pooler's `pgbouncer.imageCatalogRef`. An authored empty one is written
    and left to the API server. A test derives, for the cert-manager and CloudNativePG
    kinds, every field the type writes unauthored as a zero value the CRD's own rule
    refuses (23) and holds each to an answer shown with the CRD's validator
    (`TestKindComponents_NullRequired`): these twelve, six a kind already refused, and
    five a kind writes a default for. Breaking for a document that leaves one of the
    twelve out; the API server refused its object. A `postgresql` component refuses
    the two of them its lowering wrote empty for the author: `backup.destinationPath`
    where `backup.retentionPolicy` is set, and the `barmanObjectStore.destinationPath`
    of an `externalClusters` entry that has a `barmanObjectStore`
    (`TestPostgresqlRule_UnauthoredRequiredStrings`). Breaking for a document that
    leaves either out.
  - A `postgresql` component refuses a block that would not be built, where it used to
    drop what the author wrote in it. Five blocks are built only when one field is set:
    `backup` (`destinationPath`), `bootstrap.recovery` and `bootstrap.pg_basebackup`
    (a non-empty `source`), `replication.synchronous` (`method`), `monitoring` and
    `pooler` (`enabled`). Other values of the block without that field are refused by the
    field's name (`replication.synchronous.method: required (any or first)`,
    `pooler.enabled: required where pooler.instances is set (…)`); under `backup` that
    adds `endpointURL`, `secretName` and an empty `retentionPolicy` to the refusal
    above, which a `retentionPolicy` that is not empty already got. An authored
    `enabled: false` is the block's own switch and keeps building, with the settings
    beside it kept in the document; an empty outer block (`bootstrap: {}`, `pooler: {}`)
    builds as before, an empty `bootstrap.pg_basebackup` or `replication.synchronous`
    does not
    (`TestPostgresqlRule_BlockNotBuilt`). Breaking for a document that authored such a
    block without its field: it built, without those values.
  - A `postgresql` component writes an authored `pooler.instances` as authored, a 0 or
    a negative count included, where it left a count of 0 or less out and the Pooler
    CRD's default of 1 applied. The CRD sets no minimum, so nothing is refused
    (`TestPostgresqlRule_PoolerInstances`). Behavior-changing for a document with such a
    count: with `instances: 0` it got one pod before and gets none now.
  - A Pod carries the `app` label; a controller's pod template gains it beside the
    authored labels, and an authored `app` with another value is refused. These three
    are targets of `security-context`, a `configmap` mount and an `external-secret`
    injection.
  - A ReplicaSet's `selector` is required; a ReplicationController's is a plain label
    map and optional.
  - `podtemplate` projects a PodTemplate's one field, `template`. A PodTemplate is
    stored, not run: no `app` label, no ServiceAccount reported, and not a trait target
    (§7, item 14).
- **Shipped: four kinds of the Prometheus operator's `monitoring.coreos.com/v1` API,**
  `servicemonitor`, `podmonitor`, `prometheus-probe` and `prometheusrule`
  (`servicemonitor.go`, `podmonitor.go`, `prometheus_probe.go`, `prometheusrule.go`,
  with what they share in `monitoring_common.go`), each the strict projection of its
  spec type, built on `policyFreeKind`, namespaced, declaring its object and taking
  `objectName`. The Probe's type name carries a prefix: a probe, in the components
  package, is a container's.
  - **No capability is required and nothing gates them:** on a cluster without the
    operator's CRDs the component builds and the object is refused at apply.
  - A field the API requires that the Go type writes whether or not it was authored must
    be authored, since the object would not show the omission: a monitor's `selector`
    (unauthored, the type writes `{}`, which selects everything), a ServiceMonitor's
    `endpoints`, the three required fields of an `oauth2`, a rule group's `name` and a
    rule's `expr`. It is the rule every kind follows, not a wider one: sent as authored,
    the document is one the API server refuses, and only the Go type's zero value hides
    that. `policyFreeKind` gained a list of such fields for this
    (`refuseUnauthoredRequired`), and a test holds each kind's list to the required
    markers of the linked module's source. Of the Kubernetes types these specs embed, the
    `key` and `operator` of a selector's match expression must be authored too, presence
    only (go-kure/launcher#790): the module ships no CRD, so the same test derives them
    from the source of the linked type by the schema generators' rule. Another required field
    of such a type (the `key` of a Secret key reference) is not checked.
  - A Probe needs `prober.url` here because the object always carries a prober: a limit
    of the Go type, which writes one whether or not it was authored, not a rule of the
    API, which does not require `prober` and refuses one without a `url`.
  - The selector, the prober and the targets are the author's. **No host these objects
    name is held to the allowed registries** (a prober, a proxy, an OAuth2 token
    endpoint, a static probe target): none is an artifact source. **No field is checked
    for a literal secret:** a credential is a reference to a Secret key, and free text
    that could hold one (`params`, `endpointParams`, a proxy URL) is written as authored.
  - The labels a Prometheus selects monitors and rules by are written under `labels`,
    as on every kind component.
- **Shipped: the two kinds of VolSync's `volsync.backube/v1alpha1` API,**
  `replicationsource` and `replicationdestination` (`replicationsource.go`,
  `replicationdestination.go`, with what they share in `volsync_common.go`), each the
  strict projection of its spec type, declaring its namespaced object and taking
  `objectName`. Both are built on `policyHeldKind`; no `Policy` method is added.
  - **No capability is required and nothing gates them:** on a cluster without VolSync's
    CRDs the component builds and the object is refused at apply. The kinds read nothing
    of the cluster profile.
  - **Three things an author writes are held to the environment policy:** every
    authored capacity that sizes a volume the operator provisions (`capacity`,
    `restic.cacheCapacity`, `syncthing.configCapacity`), to the storage maximum; the cpu
    and memory of a mover's `moverResources`, to the maxima; and a mover's
    `moverSecurityContext.windowsOptions.hostProcess`, refused unless privileged
    workloads are allowed. No default is filled. **Not held:** a capacity the author
    left out, the rest of a mover's pod security context (the user and groups it runs
    as, sysctls, SELinux and seccomp settings), for which the policy has no dimension,
    the service account it runs under (`moverServiceAccount`), which is carried as
    authored, a mover's affinity, the volumes mounted into it, the type of its Service,
    and the namespace annotation by which an administrator lets an `rsyncTLS`, `rclone`,
    `restic` or `syncthing` mover run privileged, which is no field of the object.
  - **The `rsync` mover is held to the policy's container capabilities.** For the
    rsync-over-SSH mover, authoring it is the choice: the linked operator version
    (VolSync v0.16.0) runs its container as root with seven added capabilities, and
    its builder drops the namespace's answer. An authored `rsync` mover is held to the policy's
    allowed and forbidden container capabilities for those seven, as a container that
    adds them is on a pod kind, and refused with the capability named; it builds where
    the policy sets neither list. A test reads the seven from the operator's source in
    the linked module. A cluster that runs another version of the operator may add
    others, which the kind does not know, and that the container runs as root is not
    held: the policy has no dimension for it.
  - Required fields follow the rule of the Prometheus operator's kinds. A test holds
    each list to the CRDs the linked module ships (75 paths for a source, 54 for a
    destination): of a volume mounted into a mover its `mountPath` and `volumeSource`,
    of a Syncthing peer its `address`, `ID` and `introducer`, and the `key` and
    `operator` of a match expression in the label selectors of a mover's pod affinity
    and anti-affinity, presence only (go-kure/launcher#790); the test of the label
    selectors shows what the API server answers for an expression without one. The
    terms of a required node affinity in a mover's affinity, which the Kubernetes type
    would write as `null`, are refused when left out.
  - **Two fields the linked Kubernetes type holds and the CRDs do not are refused when
    authored:** `defaultUser` and `items[].user` of a Secret mounted into a mover. The
    linked Kubernetes API is newer than the one VolSync's CRDs were generated from. A
    test derives the set from the CRDs, both ways, and shows with the API server's own
    create sequence that it names each as an unknown field and prunes it.
  - The CRDs default nothing under `spec` and declare no expression rule; a test holds
    each claim, so a dependency bump that adds either fails with the field or rule
    named.
  - **The operator's own rule is not repeated:** it refuses, when it reconciles, an
    object that configures no replication method or more than one.
  - **Hosts are not checked** (`rsync.address`, `rsyncTLS.address`, a Syncthing peer's
    `address`, the server of an NFS export mounted into a mover): none is an artifact
    source. **No literal secret is checked:** a credential is the name of a Secret, and
    the `parameters` of an `external` provider are written as authored.
  - The `volsync` trait still builds a ReplicationSource for a workload's claim, named
    `<sourcePVC>-backup`; its object and a `replicationsource` component's of the same
    name are refused as a collision.
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
- **Held: `ResourceSet` and `FluxInstance`** (fluxcd.controlplane.io/v1), each with its
  reason in its inventory row. A ResourceSet's `resourcesTemplate` is a Go template the
  operator renders on the cluster into the objects it reconciles: no build sees them,
  so the kind would be a way round every rule a policy holds a workload or a Secret to.
  A FluxInstance is the installation of Flux itself, under the one name the API accepts
  (`flux`), not an application's object.
- **Not offered: Endpoints.** Deprecated upstream in favour of EndpointSlice; its
  inventory row is `not authorable` with that note.
- **Field gaps** in the hand-parsed kinds (upstream fields with no schema key):
  - `statefulset`: no scheduling field is left; the raw `affinity`, `tolerations`
    and `topologySpreadConstraints` are read;
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
- **Missing kinds:** the inventory's `missing` rows (APIService, GRPCRoute among
  them). The inventory has no `trait` row left: no kind is reachable only as a trait.
  The ticket adds the missing kinds group by group. A kind kure lacks is added to kure
  first.

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
    source or a rendered chart, named by its kind and name (a Cilium policy whose
    `icmps` field leaves its `type` out is the known case): a build error, not a crash.
    Kure's parser reports that panic as the document's parse error (go-kure/kure#1009).
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
| [go-kure/launcher#787](https://github.com/go-kure/launcher/issues/787) | Name overrides | §3.2 | Partly: authored names used as written or refused; `scaler`, `rbac`, `networkpolicy` and `postgresql` overrides; `objectName` on kind components; the consumer `Naming` hook for the roles of §3.2; the hook-group names and their `hook-group` role; the Kustomization of a chart's own layout (`layout`); the HelmRelease of a `helm` component (`helm-release`) and the Kustomization and the kept source of an `oci` component (`oci-kustomization`, `oci-source`); the Deployment, the Service and the ServiceAccount of a `webservice` or `worker` component (`workload-deployment`, `workload-service`, `workload-serviceaccount`); the Cluster and the ObjectStore of a `postgresql` component (`postgresql-cluster`, `postgresql-objectstore`); the claim a `pvc` volume of a `webservice` or `worker` component generates (`workload-volume-claim`, `claimObjectName`); the Ingress and the HTTPRoute of the routing traits (`ingress`, `httproute`). Bundle, ordered-group and synthesized NetworkPolicy names are hook-only by design: the author names the bundle by `metadata.name`, a group is derived from the order, and a synthesized policy has no authored home. Open: a hook role for the names outside the roles of §3.2 | go-kure/launcher#783, go-kure/launcher#793 |
| [go-kure/launcher#788](https://github.com/go-kure/launcher/issues/788) | Component label and provenance | §3.4 | Shipped | — |
| [go-kure/launcher#789](https://github.com/go-kure/launcher/issues/789) | Contract metadata | §6.1 | Shipped | — |
| [go-kure/launcher#790](https://github.com/go-kure/launcher/issues/790) | Full spec and full set of kind components | §6.2 | Partly: the kind inventory; the kinds §6.2 lists as shipped; `labels` and `annotations` on every kind component | [go-kure/kure#981](https://github.com/go-kure/kure/issues/981) (missing constructors), go-kure/launcher#787 |
| [go-kure/launcher#791](https://github.com/go-kure/launcher/issues/791) | Security on template delivery | §5.2 | Shipped | — |
| [go-kure/launcher#792](https://github.com/go-kure/launcher/issues/792) | Hook-group child names unique across applications | §3.3 | Shipped | go-kure/launcher#793, go-kure/launcher#787 |
| [go-kure/launcher#793](https://github.com/go-kure/launcher/issues/793) | One shortening rule | §3.3 | Shipped | — |
| [go-kure/launcher#794](https://github.com/go-kure/launcher/issues/794) | Asymmetries | §7 | Shipped | go-kure/launcher#783, go-kure/launcher#784, go-kure/launcher#788 |
| [go-kure/launcher#795](https://github.com/go-kure/launcher/issues/795) | `kurel build` ignores the global `-f/--output-file` (deferred) | §7 | Open | — |
| [go-kure/launcher#849](https://github.com/go-kure/launcher/issues/849) | Policy refusals carry a class | §5.3 | Shipped | — |
