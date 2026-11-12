---
title: "Pipeline"
weight: 1
---

The implementation and history of the section below are on the
[contributor page](../../contributing/oam-reference/pipeline.md).

## Pipeline

```
parse → resolve parameters → transform (component + trait handlers) → manifests
```

1. **Parse** an Application/Package/ClusterProfile from YAML.
2. **Resolve parameters** (`ResolveParameters`) — apply `kurel.yaml` declarations,
   values files, and `--set` overrides via `${var}` substitution.
3. **Transform** (`Transformer`) — dispatch each component to its
   `ComponentHandler` and each trait to its `TraitHandler`, merging the
   `ClusterProfile`'s capability choices.

The transform orders components only where the document declares an order.
No component type comes before another by itself. Three
declarations order components:

- **A tier.** A `placement` policy, or the `<domain>/tier` annotation it replaces, puts a
  component in `infra`, `services` or `apps`. Every component of a populated tier comes
  after every component of the populated tier before it. A component nothing places is in
  no tier and takes no part in that order, so one populated tier alone orders nothing.
- **A `dependency` policy rule.**
- **A lowering rule's order between the components it emits** (`Component.OrderAfter`).
  The `helm` rule orders its release after the source it generates, and the `oci` rule
  each Kustomization after the source several `oci` components share. A rule can also
  make a component it emits wait as another (`Component.WaitsAs`): it then follows what
  the `dependency` rules make that one follow.

An application is always one bundle, named after the Application. With nothing declared
it is flat: the one bundle holds every component. Otherwise the components are split into
ordered groups, the topological levels of the declared order: the first group holds every
component that comes after no other, each further group the components whose predecessors
are all in earlier groups. Each group is a child bundle of the application bundle and
depends on the group before it. A group that is exactly the components of one tier is
named `<application>-<tier>`; any other is `<application>-<NN>`, its two-digit position
counted from `00`. A group name is held to 253 characters, the limit of a name that names no
delivery engine ([Names and overrides](names-and-overrides.md#names-and-overrides)). A stricter limit of the object
an engine makes from a bundle is that engine's workflow to check where it builds the object:
a Flux Kustomization's name is also written as a label value, at most 63 characters. A
consumer that names its Kustomizations itself is not bound by the bundle's name.

Declarations that cannot all hold (a dependency against the tier order, a cycle) fail the
transform, naming each step and where it was declared:

```text
components cannot be ordered: "web" is after "db" (placement: tier apps is after tier infra), "db" is after "web" (dependency policy)
```

A *generated source* is in no group: a Flux source (`helmrepository`, `ocirepository`,
`gitrepository`, `bucket`) that a lowering rule generates, orders a component after, and
does not itself order after anything. It is one of the
application bundle's own applications, beside the child groups, so a source several
components share belongs to the application and exists once. It waits on nothing, not even
on what the component it was generated for waits on. A document cannot place it in a tier
or make it wait on a component: the transform refuses both. Every other source is a
component like any other, in a group: one the author wrote, one a rule emits without
ordering anything after it, one the rule itself orders after a component, and one emitted
as a member of a same-name sibling group.

**Contract on the consumer: a bundle's own applications are applied before its child
bundles.** Launcher expresses "sources before groups" only through that shape and cannot
enforce it. kure's layout walker writes a bundle's applications, then its children. A
caller that walks the tree itself must visit the own applications of a bundle that has
children; `GenerateApplications` does.

**Where a layout-walking consumer finds the application.** Launcher writes no directory;
kure's layout walker does. The root node's directory renders no
bundle: the application bundle has a directory of its own inside it, named after the bundle,
which the root's `kustomization.yaml` does not list. `Bundle.DirName` on the bundle launcher
returned gives that directory another name. **Breaking output change** for such a
consumer: with `layout.DefaultLayoutRules()`, everything launcher returns is written one
directory down. Below, `<bundle>` is the application bundle's name: the Application's name,
unless the `Naming` hook renames the bundle. The root node is unnamed whether or not the
components are ordered, so `<bundle>/` is in the same place for both:

| Application | Before | Now |
|-------------|--------|-----|
| Flat: every component | `cluster/` | `cluster/<bundle>/` |
| Ordered: the generated sources | `<bundle>/` | `cluster/<bundle>/` |
| Ordered: a group | `<bundle>/<group>/` | `cluster/<bundle>/<group>/` |

**Breaking output change** for a layout-walking consumer of an ordered application:
its root node was named `<bundle>` until then, which put a directory
of the root node's own above the bundle's. The tree was `<bundle>/<bundle>/`
(`<cluster>/<bundle>/<bundle>/` with a `ClusterName`; a `ClusterName` whose last segment is
the bundle's name was the node's directory itself, with no directory added and no
Kustomization of the node's own). Under `FluxIntegratedPerLayout` with any other cluster
directory, the node had a Flux Kustomization of its own (named `...-node`) between the top of
the tree and the application's. kure's Flux integration refused the application when every
Source that Kustomization could take was one the integration generates, from a `SourceRef`
with a URL: it would have delivered its own Source. The top of an ordered
application's tree is now the flat application's: the same top directory, the application's
Flux Kustomization where the flat application's is, and no node Kustomization. What ordering
adds is inside `<bundle>/` (the groups) and in the Flux objects kure generates for them: the
application's Kustomization checks the health of each group's.

With `LayoutRules.ClusterName`, `<cluster>` takes the place of `cluster`
(`<cluster>/<bundle>/`, and `<cluster>/<bundle>/<group>/` for a group). A component with a
directory of its own, a `helmtemplate` component or any component under
`ApplicationGrouping: GroupByName`, is a directory inside the one above
(`cluster/<bundle>/<component>/`). The `spec.path` of the Flux Kustomization, or the
`source.path` of the ArgoCD Application, that kure generates for the bundle moves with it.
`layout.TopDirectory(rootNode, rules)` returns the directory at the top of that tree
(`cluster` or the `ClusterName` directory above), the one kure's bootstrap points Flux at.

With the default `BundleGrouping: GroupFlat`, `ManifestLayout.OriginUnit()` on the root
node's layout returns the bundle directory's layout; it is nil on every other layout, and on
every layout under `BundleGrouping: GroupByName`. `WalkCluster` returns the root node's
layout.

Launcher adds no limit of a delivery engine to the names it returns. A document name over
63 characters builds, and kure's Flux workflow then refuses its bundle unless the consumer
names it: `Bundle.KustomizationName` on the bundle launcher returned, or the `Naming` hook
for the `bundle` and `group` roles
([Name roles and the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook)).

Under `FluxIntegratedPerLayout` placement kure also makes a Kustomization for each
application layout and each hook-group layout. Its default name is
`<unit name>-<layout name>`, the unit name being the bundle's Kustomization name, and kure
shortens a default over 63 characters by its own rule, with the first 8 hexadecimal characters of the SHA-256 of the default. A
layout name of at most 54 characters stays whole, after as much of the unit name as fits:
`<unit prefix>-<hash>-<layout name>`, or `<hash>-<layout name>` where none fits. A longer one
keeps its first 54 characters, `<layout prefix>-<hash>`, so the phase at the end of a long
hook-group name shows only in its directory. Either prefix loses the hyphens and dots that end
it, and one left empty is dropped with its `-`. A name set on the layout kure uses as
written. A component has a layout of
its own when it is a chart, a `helmtemplate` component or a `helm` component under
`delivery: template`, and kure names that layout's Kustomization `<bundle>-<component>`.
Launcher leaves that default to kure, whatever its length. That name is the `layout` name role
([Name roles and the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook)): an author
(`layoutKustomizationName`) or the hook sets another, used as written, never shortened, and
refused in the transform, naming the component and the role, unless it is a DNS-1123
subdomain of at most 63 characters. Launcher never overwrites a `KustomizationName` a caller
set on the layout. A name set on the layout needs per-layout placement: under any other the
layout has no Kustomization of its own, and kure's Flux integration refuses the name, naming
the layout. Charts of one document are held apart, but the name a chart's
layout gets is not held against a bundle's or a hook-group child's; kure refuses a
Kustomization name used twice when it integrates the walked tree.

On a `helmtemplate` hook-group child launcher sets no `ManifestLayout.KustomizationName`
unless a prefix names it: kure names the child's Kustomization `<unit name>-<child>`, the
child being `<application>-<component>-NN-<phase>`, and shortens it past 63 characters by its
own rule. The child's layout name, its directory, is held to 253 characters
([Names and overrides](names-and-overrides.md#names-and-overrides)). The next group's `spec.dependsOn` follows the
Kustomization's name: the child's `DependsOn` lists the sibling's layout name, and kure writes
that sibling's Kustomization name. The prefix `<application>-<component>` is the `hook-group`
name role ([Name roles and the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook)): an author or
the hook sets another, which names directory and Kustomization alike and is never shortened.
A consumer that walks the tree itself can still set the field on a walked child before the
integration. Like the child's `DependsOn`, the field needs `FluxIntegratedPerLayout`: under
any other placement a child gets no Kustomization of its own, and kure's Flux integration
refuses both, naming the child.

**Breaking output change**: under `FluxIntegratedPerLayout` placement
the Kustomization of a default hook-group child regains the leading `<unit name>-`
(`shop-db-01-main` becomes `shop-shop-db-01-main`), and a
Kustomization name launcher shortened, for a chart's layout or a hook-group child, becomes
the one kure shortens by its own rule. Directory names do not change.

Launcher sets no Flux delivery field on any bundle it returns: `Interval`, `RetryInterval`,
`Timeout`, `Prune`, `Wait`, `Force`, `Suspend`, `HealthChecks`, `Patches` and `PostBuild`
stay unset. How an application is delivered (which Flux
Kustomization applies it, how readiness is judged, how often it reconciles) belongs to the
consumer that delivers it; see `docs/delivery-scope.md`.

A consumer that sets one on a bundle launcher returned gets what kure's Flux workflow makes
of it. Under `FluxIntegratedPerLayout` placement eleven settings of a bundle reach further
than its own Kustomization: `Wait`, `Timeout`, `RetryInterval`, `Labels` and `Annotations`,
and `Interval`, `Prune`, `Force`, `Suspend`, `PostBuild` and
`Patches`. The ten besides `Patches` are also on the Kustomization
of the layout of each component the bundle holds and of each `helmtemplate` hook-group child
of such a component (`shop-db` and `shop-shop-db-NN-<phase>`, beside `shop`); a patch reaches
those Kustomizations only as the placements below say, some of them or none. The bundle
they come from is the one that holds the component, and kure hands none of them down from a
bundle to its child bundles. In a flat application that is the application's bundle. In an
ordered one the components are in the group bundles below it ([Pipeline](#pipeline)), so a
consumer sets them on each group bundle: on `shop-infra` the ten reach `shop-infra`,
`shop-infra-db` and the `shop-infra-shop-db-NN-<phase>` hook groups, and no other group. Set
on the
application's bundle alone they reach its own Kustomization, and no group's and no grouped
component's. `HealthChecks`
stays on the bundle's Kustomization alone. Where neither the bundle nor the layout sets
`Interval` or `Prune`, a per-layout Kustomization keeps the generator's `spec.interval` and
`spec.prune`; a bundle with `Prune` on turns garbage collection on for the component's and
the hook groups' Kustomizations. `PostBuild` is copied whole, and where the substitution of
the build above would change a per-layout Kustomization's postBuild or patches, kure writes
`kustomize.toolkit.fluxcd.io/substitute: disabled` on it.

Where a bundle has per-layout Kustomizations, its `Patches` are placed by object. A *plain*
entry is a strategic-merge patch that parses as resources, carries none of kustomize's
`internal.config.kubernetes.io/` build annotations and cannot remove its object (no top-level
`$patch: delete`, no `config.kubernetes.io/local-config` at a value but `"false"`); any other
entry, a JSON6902 patch among them, is not plain, with a `Target` or without.

- An entry with a `Target` is on the bundle's Kustomization and on every per-layout one.
- A plain untargeted entry with no entry before it in `Patches` that is not plain is only on
  the Kustomizations whose build holds every object it names, so it leaves the bundle's own
  Kustomization where that one does not build the object.
- A plain untargeted entry with an entry before it that is not plain stays on the bundle's
  own Kustomization, is written besides on each per-layout one whose build holds every object
  it names, and is never refused.
- An untargeted entry that is not plain stays on the bundle's own Kustomization alone.

A bundle without per-layout Kustomizations keeps every patch on its own, as before. With
`Wait`, a hook group's Kustomization is
Ready once the objects it applied are, not once it has applied them, so the next group, whose
`spec.dependsOn` names it, starts after that. A hook group depends on the group before it and
never on the layout above, so the tree integrates with an inherited wait, and the names and
the `dependsOn` chain do not change. A walked layout can set its own value before the
integration (`ManifestLayout.Wait`, `Timeout`, `RetryInterval`, `Labels`, `Annotations`,
`Interval`, `Prune`, `Force`, `Suspend`; postBuild and patches have no layout field): one
`Timeout` inherited by the bundle's, the component's and the hook groups' Kustomizations gives
the innermost as long as the outermost, and a shorter one on the layouts below is the
consumer's to set. kure refuses six things it wrote out before: a bundle label or annotation
the Kubernetes API does not accept, where a per-layout Kustomization inherits it; a layout a
consumer added that depends on the application layout above it, once the bundle sets `Wait`
(set `Wait` to a pointer to `false` on the application's layout); a plain untargeted
strategic-merge patch, placed by object as above, when no Kustomization of the bundle builds
an object it names, and one whose documents name objects no one Kustomization builds together
(give the patch a `Target`, one patch per object); a per-layout Kustomization whose postBuild
or patches the build above would substitute when that build also substitutes its labels,
annotations or path, which the opt-out would stop (remove the `${...}` expressions from one
of the two); and, under any placement, a duration the Flux API
does not take, a negative one or one above zero and under a millisecond, in a bundle's
`Interval`, `Timeout` or `RetryInterval` (in a layout's only where the layout gets a
Kustomization of its own). kure's `pkg/stack/fluxcd` README has the rules under "Per-layout
settings", "Patches of a bundle with per-layout Kustomizations" and "Durations".

Under any placement, a bundle with child bundles (in an ordered application, the
application's bundle above its group bundles) that sets `Wait` no longer carries a
`spec.healthChecks` entry per child on its Kustomization: Flux ignores
health checks under `wait`, so nothing changes on a cluster, but the generated object does.
kure's layout writers (`WriteManifest`, `ManifestLayout.WriteToDisk` and `WriteToTar`) refuse
a layout that holds an object written without a `kind`, or without an `apiVersion`,
where they wrote it before. Each item of a List is judged, not the List
itself, and an object with kustomize's `config.kubernetes.io/local-config` annotation at any
value but `"false"` needs a `kind` but no `apiVersion`.

**Breaking output change** for a consumer (with the kure commit `go.mod` pins). Under
`FluxIntegratedPerLayout` placement, a bundle that sets one of the ten besides `Patches` gives
it to the Kustomizations of the components it holds and of their hook groups; its patches go
there by the four placements above: a targeted one to every one of them, a plain untargeted
one to each that builds every object it names (with no entry before it that is not plain, it
moves off the bundle's own Kustomization where that one does not build the patched object),
and an untargeted one that is not plain to none; and a tree kure refuses
for one of the first five reasons above no longer renders. Under any placement, a tree with a
duration kure refuses no longer renders, a bundle with child bundles that sets `Wait` loses
its child health checks, and a layout holding an object the writers' type check above refuses
is no longer written. What launcher
returns does not change, and a tree that sets none of the eleven, no `Wait` on a bundle with
child bundles and no such duration, and holds no object the writers now refuse, renders and
is written as before.

kure reads the resources a layout holds as kustomize builds them, a List standing for its
items, by one rule in its pre-write checks and in its Flux integration.
Launcher's own decode paths put no List into a layout (kure's parser replaces a list document
by its items, and `passthrough` refuses a list); a Go caller's own `ApplicationConfig` that
returns one has it read by that rule. Launcher reads such a List by one rule of its own,
wherever it reads generated objects: as Flux applies it. A kind ending in `List` whose `items`
is an array stands for its items, recursively, as Kustomize's build expands it; any remaining
object whose `items` is an array stands for its items, one level, as Flux does. That one rule
decides what `CheckInDocumentCollisions`, `CheckCrossDocumentCollisions` and a sibling group's
comparison of its members key, as it decides what the forced-volume warning, the component
label and the reserved metadata keys read. So an item another application, document or member
also generates is a collision, reported by the item's own kind and name; and the envelope is
no object, so two Lists with different items, or none, do not collide. The three comparisons
also leave out what Kustomize's build drops: an object whose
`config.kubernetes.io/local-config` annotation is present with any value but `"false"` (an
empty value, a boolean `true`, a number or a null drops it too, as authored in an unstructured
object; a boolean `false` keeps it) is never applied, so it generates nothing and collides with nothing,
and the claim it would make for a controller goes with it. That is
judged on each item once a `List` is inlined, so a `List`'s own annotation is not read; an
envelope whose kind does not end in `List` is one object for Kustomize and is dropped with all
its items. The other readers of generated objects, such as the forced-volume warning, still
read it. An object with no kind
cannot be keyed. A generated object with none is refused by the three comparisons before it
is read, as before, whatever it holds. An item with none is refused too, with the one
exception the rule itself makes: an item of a `List` that has no kind and holds an `items`
array of its own is one of the remaining objects, so it is read by its items, one level, as
Flux reads it and as the three scans do, and the objects inside it are still compared. Past
that one level, and inside an envelope whose kind does not end in `List`, an item with no
kind is refused, `items` or not. Kustomize's build itself fails on a List item with no kind
(`missing kind in object`).

**Breaking output change** for a Go caller whose own `ApplicationConfig` or handler returns a
List: the three comparisons keyed the List as the one object it is. An item generated twice
passed, and two unnamed Lists were refused as one object whatever they held.

An application can carry a delivery intent, `stack.Application.Delivery`: what it asks of the
engine that delivers its objects, stated without naming the engine. The `prune-protection` and
`force-replace` traits set it, on the component's application and on
each of its trait sub-applications; a sibling group's one application takes each intent that
any of its members has. Launcher writes no Flux annotation for either trait. kure's Flux
workflow turns the intent into `kustomize.toolkit.fluxcd.io/prune: disabled` and
`kustomize.toolkit.fluxcd.io/force: enabled` on everything the application's layout holds,
where it integrates a layout (`CreateLayoutWithResources`, `IntegrateWithLayout`); the
[traits README](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits) says
what that covers.

**Breaking library changes**:

- Output: the objects of a component with `prune-protection` or `force-replace` no longer
  carry `kustomize.toolkit.fluxcd.io/prune` or `kustomize.toolkit.fluxcd.io/force`. A consumer
  that delivers through kure's Flux layout integration (`CreateLayoutWithResources`,
  `IntegrateWithLayout`) gets the same annotations from it. One that applies the objects
  another way reads `Application.Delivery`, or loses the effect.
- kure refuses a set intent where it cannot map it, and so a document with either trait:
  its Flux `GenerateFromCluster`, which returns none of the application's objects, fails
  naming the application, and so does a workflow with no mapping. A caller of
  `GenerateFromCluster` moves to `CreateLayoutWithResources`.
- The two traits no longer show in `kurel build`'s output, which has no place for the intent.
  A consumer that applies that output through a Kustomization of its own loses the effect;
  to keep it, it builds through the library and reads `Application.Delivery`.
- Under prune protection each content change of a generator-built ConfigMap leaves the old
  hash-named ConfigMap behind. That is kure's behaviour, not launcher's: its Flux workflow
  writes the prune annotation on the `configMapGenerator`s of the application's layouts, and
  kustomize names a generated ConfigMap by its content.
- A sibling group's application takes the intent of any member, so the trait on one member
  covers every object of the group.
- `GeneratedApplication.Forced` is also true for an application with the `ForceReplace`
  intent, not only for one whose bundle sets `Force`. A volume under the intent alone is
  warned about conditionally (`is covered by the force-replace delivery intent of its
  application: where the delivery workflow maps that intent ...`), where the warning named
  the annotation: the generated objects carry no force for it.
- The two traits no longer wrap `stack.Application.Config`, and the trait decorators' internal
  post-augment hook is gone.

Within a bundle, each component's application is followed by the sub-applications its traits
created, in creation order, so a trait's objects are emitted with their own component's rather
than after every component of the bundle. A trait handler that moves
an application or removes a sub-application keeps the order it left; one
that replaces, removes or renames a component's application fails the transform (see `TraitHandler` in [Transform, extension and lowering](transform-and-lowering.md#transform--extension)). A trait whose handler
implements `SubApplicationDecorator` (the built-in `prune-protection` and `force-replace`, which
set a delivery intent) is also applied to those sub-applications, whatever order the traits were authored in: the last step of
the transform, after the Phase-4 synthesis below, applies it to each of them. The NetworkPolicies
that synthesis adds are no component's sub-applications and stay undecorated.

Under `TransformContext.FluxNamespace`, every config that takes it (`SetFluxNamespace`: the
`helmrelease`, `fluxcd-kustomization`, Flux source, `fluxcd-alert`, `fluxcd-provider`, `fluxcd-receiver`, `imagepolicy`,
`imagerepository`, `imageupdateautomation`, `artifactgenerator` and `resourcesetinputprovider` kinds) moves its Flux objects there, and a trait
sub-application of that component follows only when the Flux object reads it by name from its own
namespace. In a sibling group the Flux object is any of the group's Flux
members, the members whose config takes the Flux namespace before the component's first trait runs:
they land in the Flux namespace together, so a Flux member's trait sub-application follows when any
of them reads it, whichever member carries the trait (an `oci` component's source reads the Secret
of a trait on its Kustomization). A member that is no Flux object keeps its
trait sub-applications with its workloads, whatever a Flux member reads. A Flux object a lowering
rule orders after a generated source (`Component.OrderAfter`) reads through that source as well: a
trait sub-application of the component follows when the source reads it (the `oci` rule's shared
source reads the Secret one consumer's trait makes). A workload ordered after
a source gains nothing from it. A trait that makes, in a later group, a Secret a generated source
or a ConfigMap or Secret a Flux object requires is admitted under the default delivery, which
converges, and refused under `TransformContext.GroupsWaitForFluxObjects` (below). The config reports what it reads (`FluxNamespaceReads`: a
HelmRelease's `valuesFrom`, `kubeConfig` and chart-template `verify` Secret, a source's
`secretRef`, `certSecretRef`, `proxySecretRef` and the Secrets under `verify` and `sts`); a
sub-application config names the ConfigMap or Secret it produces (`FluxNamespaceInput`: the
`configmap` trait's ConfigMap, the `secret` trait's Secret, the Secret an `external-secret` trait's ExternalSecret or a
`certificate` trait's Certificate writes). Both kind and name must match. Every other trait object —
a ConfigMap or Secret the Flux object does not name, a Certificate whose Secret it does not name, a
claim, a NetworkPolicy, a route — stays in the application namespace with the
workloads, where a HelmRelease installs them (`targetNamespace`) and keeps its Helm release
storage (`storageNamespace`, defaulted to the same namespace). A ConfigMap or Secret the Flux
object names moves even when the chart's pods read it as well: the Flux object cannot reconcile
without it, and a reference through the chart's values is invisible here. Every built-in config
that takes the Flux namespace reports its reads, possibly none; decorators and sibling
groups forward them.

`TransformContext.GroupsWaitForFluxObjects` is the consumer's statement that its delivery makes a
group Ready only once the group's Flux objects are Ready: kure's `UmbrellasDoNotWait`, for example,
or `wait: true` on the group bundles. Each group depends on the one before it, so a ConfigMap or
Secret a group's Flux object requires, made by a trait or a component in a later group, then never
converges: the Flux object's group waits for it, it for the ConfigMap or Secret, and the maker's
group for the Flux object's. Under the field the transform refuses it, naming the Flux object, the
ConfigMap or Secret, the maker and the groups: put the trait on a component in the Flux object's
group or an earlier one, or place the component there, or provide the object apart from the
document. Unset, launcher assumes the default delivery, under which a group is Ready once applied
and the document converges, and it refuses nothing. Two kinds of Flux object wait:
- a Flux object of a group, a sibling group's Flux member included (named by the group), in its
  own group;
- a generated source, in the group of its earliest Flux consumer, a Flux object its own lowering
  rule ordered after the source (in a sibling group the member's own order, not another member's);
  a workload does not wait on the source, and a source no Flux object consumes waits in no group.

The refusal needs every link proven:
- the Flux object reports the ConfigMap or Secret as one it cannot become Ready without. A Flux
  object of a group reports it as `FluxRequiredReads`, a subset of its `FluxNamespaceReads`:
  a `helmrelease`'s `valuesFrom` not marked `optional`, its `kubeConfig`, and what its chart
  template's generated object requires where it lands in the release's namespace; a
  `fluxcd-kustomization`'s `decryption.secretRef` and `kubeConfig`, never its
  `postBuild.substituteFrom`, which kustomize-controller skips for a rendered resource that
  disables substitution; every Secret a `gitrepository`, `bucket` or `helmchart` reads; a
  `helmrepository`'s `secretRef` and `certSecretRef`, none for `type: oci`, a static object that
  never sets Ready; what an `ocirepository` requires as a source; a `fluxcd-receiver`'s,
  `imagerepository`'s and `imageupdateautomation`'s Secrets. A `suspend: true` object requires
  nothing, and a `resourcesetinputprovider`, `fluxcd-alert`, `fluxcd-provider`, `imagepolicy` and
  `artifactgenerator` require nothing. A generated source reports only Secrets, as
  `FluxSourceRequiredSecrets`: an `ocirepository`'s `secretRef`, `certSecretRef` and
  `proxySecretRef`, its `verify.secretRef`, and its `verify.trustedRootSecretRef` only under the
  `cosign` provider, the default; a `helmrepository`'s `secretRef` and `certSecretRef`, of type
  `oci` too, since an OCI chart pulled through it reads them;
- the maker makes the ConfigMap or Secret. A trait object says so (`ProducesFluxNamespaceInput`:
  true for the `secret`, `certificate` and `configmap` traits, and for `external-secret` except
  under `target.creationPolicy: Merge` or `None`, where the Secret exists apart from it). A
  component makes its own object when its type declares one (`ComponentObjectProvider`) of
  Namespaced or Flux scope, named by `Component.ObjectName`: a `secret` or `configmap` component.
  It also makes the object its object's controller makes by name, which its config reports as
  `MadeObject`: a `certificate`'s `secretName`, an `externalsecret`'s target (`target.name`,
  else the object's own name) except under `creationPolicy: Merge` or `None` or with a
  `target.manifest`;
- the ConfigMap or Secret lands in the Flux object's namespace: the Flux namespace, or without
  one the application namespace, where a workload's trait objects land too, so a trait on any
  component counts. A Namespaced component's object, and what its controller makes, land in the
  application namespace, so a component maker counts without a Flux namespace or under one equal
  to the application namespace;
- for an object a controller makes (`MadeObject`), the document does not itself hold that
  ConfigMap or Secret in the Flux object's group or an earlier one: a component's own object, or a
  `secret` or `configmap` trait's (`IsFluxNamespaceInputObject`). The controller then writes into
  an object already in place, as an `externalsecret` under `CreateOrMerge` does. An earlier
  ExternalSecret or Certificate does not count: what it writes is not the document's.

The traits are looked at first, then the components, each in group and document order, and the
first maker found is refused. A config that does not report what it requires is admitted, as is a
Secret-naming trait that is no `FluxNamespaceInput` (the managed TLS Secret of `ingress`/`expose`, a
`volsync` repository), and a cluster-scoped component's object.

The same delivery refuses the source a Flux object of a group cannot become Ready without
(`FluxRequiredSource`) when a component of a later group makes it as its own object: a
`helmrelease`'s `chartRef` (an `ocirepository` or a `helmchart`), or the `chart.spec.sourceRef` of
its chart template of kind `HelmChart`, none for kind `OCIRepository`; a `fluxcd-kustomization`'s,
`helmchart`'s or `imageupdateautomation`'s `sourceRef`. A `helm` component naming an existing
source (`source.name`) lowers to such a HelmRelease and leaves the source's order to the author.
Kind, name and namespace must match: the reference's `namespace`, else the one the Flux object
lands in, where a source component lands too. A `suspend: true` object requires none; otherwise
the source counts whatever the object reports as `FluxRequiredReads`. The refusal names the
reference's field; place the source's component in the Flux object's group or an earlier one.

A Phase-4 post-build stage then synthesizes per-component `NetworkPolicy` resources,
each a **separate** additive resource (the authored `networkpolicy` /
`cilium-networkpolicy` traits are unaffected):

- **Inbound** (`{comp}-allow-ingress-traffic`) — routing-derived, from routing traits'
  platform-reserved `networkPolicy.trafficSources` capability rendering. When a routing trait's
  `backendRef` names a **separate** backend Service (not the exposing component's own),
  the allow is **retargeted onto that backend component's pods** + the backendRef port — resolved
  by matching the backend Service name to a sibling OAM component **cluster-wide**, and the
  retargeted policy is emitted in the **backend component's own leaf bundle**. Resolution spans
  bundles: components of one Application share a namespace but, once anything orders them, are
  split across leaf bundles (one per ordered group), so a router in one bundle
  correctly retargets onto a backend in another. Two components resolving to the **same** Service
  name is ambiguous and **fails the transform**. A backendRef
  that resolves to no component (a **bare external Service**) is left authored **unless**
  it carries an explicit authored `backendSelector` (matchLabels only, on the routing trait's
  `paths[].backend` / `backendRefs[]` — the selector is not inferable from a Service name): that
  emits a separate `{service}-allow-ingress-traffic` policy in the router's namespace selecting the
  backend's pods on the backendRef ports. **Same-namespace only** (the backend is referenced by bare
  name; cross-namespace `ReferenceGrant` is out of scope). External backends are deduplicated
  **cluster-wide** (routers in different leaf bundles naming the same Service emit one merged
  policy). Two routers giving one external Service different selectors, or an external policy name
  colliding with a component's emitted inbound policy, **fails the transform** rather than emitting
  conflicting or duplicate allows. The name collision is refused once both names are resolved, so
  a `Naming` hook that gives one of the two policies another name avoids it (see [Name roles and
  the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook)).
- **Egress** (`{comp}-allow-egress-traffic`) — from `TransformContext.EgressPeers`, a
  downstream-supplied, non-authorable synthesis input (graph-derived dependency peers; never
  set from OAM YAML or capability rendering). K8s `NetworkPolicy` only. Empty when a
  caller supplies no peers (e.g. the kurel CLI), so synthesis is then a no-op. **Fail-fast**
  (aligned with the endpoint-ingress family): a peer that carries ports but a nil, empty, or
  expression-bearing pod selector is a producer bug and fails the transform with an error — it
  would otherwise emit a namespace-wide egress allow. A peer with **no ports** is the documented
  escape hatch and is silently skipped (the destination stays authored).
- **Endpoint ingress** (`{comp}-allow-endpoint-ingress`) — the **target side** of a
  connection, from `TransformContext.IngressPeers` (a platform-supplied, non-authorable
  graph-derived input). Each `netpol.IngressPeer` names an `Endpoint` (pod selector + ports)
  and the sources allowed to reach it; launcher emits an Ingress `NetworkPolicy` selecting the
  **endpoint's own selector** — deliberately **not** the component-label key — so it protects
  operator-created pods (e.g. a CloudNativePG cluster's `cnpg.io/cluster` instance pods) that
  carry no component-provenance label. Fail-closed: each source must carry a namespace + a
  non-empty matchLabels pod selector (namespace-wide sources are dropped), and a policy with no
  valid rule is not emitted. A component's endpoints are declared by its handler, or by
  the `ComponentLoweringRule` claiming its type, via the optional `EndpointProvider` interface and read through `Transformer.ComponentEndpoints` (or `ComponentEndpointsNamed`, for a consumer that sets a `Naming` hook; see [Name roles and the `Naming` hook](names-and-overrides.md#name-roles-and-the-naming-hook)) — the
  producer half a downstream platform uses to learn the real selector (no hardcoding) and build
  its dependency graph. One policy is emitted **per distinct endpoint**: a single-endpoint
  component keeps the bare `{comp}-allow-endpoint-ingress` name, while a multi-endpoint component
  (e.g. a CloudNativePG cluster plus its pooler) suffixes each policy with a short content hash of
  the endpoint, so the names are distinct and stay stable across unrelated endpoint additions. The
  suffix names the emitted **NetworkPolicy resource** itself (not just the internal layout entry),
  so a multi-endpoint component's resource ids are unique and `kustomize build` accepts them.
  **Endpoint names and protocols.** An `Endpoint` carries an optional `Name`, empty or a valid IANA
  service name (the rule a container port name obeys: at most 15 characters, `[a-z0-9-]`, at least
  one letter, no leading, trailing or doubled hyphen), and a `Protocol` (empty meaning TCP, `TCP`,
  `UDP` or `SCTP`) that applies to every port of the endpoint. A non-empty `Name` is unique within
  one component's endpoint list; unnamed endpoints may repeat, and a consumer tells them apart by
  port and protocol. `ComponentEndpoints` refuses a bad name, a bad protocol and a repeated name.
  Synthesis stays TCP-only: an `IngressPeer` whose endpoint is UDP or SCTP is skipped, never
  emitted as TCP, and neither field enters the grouping key, so no policy name depends on them.
  Where endpoints come from: `deployment`, `statefulset` and `daemonset` report one per
  main-container port (the port's name, its number, its protocol); `helm`, `helmrelease` and
  `helmtemplate` report what an `endpoints` property declares, since a chart's pods are not
  visible to launcher; the other providers are unchanged.

The inbound/egress families select the component's own pods (the ingress recipients / the egress
source pods) via a **derived `<domain>/component`** label by default — the domain comes
from `TransformContext.Domain` (empty ⇒ the library default `gokure.dev`; the kurel CLI
uses `launcher.gokure.dev`). The full key is overridable per transform through
`TransformContext.ComponentLabelKey` (precedence: `ComponentLabelKey` > `<Domain>/component`
> `gokure.dev/component`). Launcher puts that label on every object a component owns and on
its pod templates itself ([Component label and
ownership](labels-and-ownership.md#component-label-and-ownership)), so the selector matches the component's pods
with no caller labelling anything. The label is authoritative: a value an object already
holds under the key is the component's own, or generation fails, so `ComponentLabelKey` must
be a key nothing else writes.

The selector **value** is `ComponentLabelValue(name)` of the authored component, not the raw
component name, and the label launcher adds has the same value. A component name is a DNS-1123
subdomain (up to 253 characters), a label value at most 63: `ComponentLabelValue` returns a
name of 63 characters or fewer unchanged and projects a longer one onto a readable prefix of at
most 52 characters (its first 52, with trailing `-`/`.` trimmed) plus `-` and the first 10 hex
characters of its sha256. The projection is deterministic, so every component-identity label and
selector that uses it gets the same value for one component. The `app` label and the `app`
selectors the built-in components and traits generate to identify a component use the same
function; authored
labels and selectors (a `service` component's `selector`, a `networkpolicy` peer) are emitted as
written, and a type that emits authored objects (`passthrough`, for one) adds no `app` label.
Selectors under an operator's own label and naming contract — the `postgresql` endpoints'
`cnpg.io/cluster` and `cnpg.io/poolerName` — carry the operator's values and are not
component-identity labels.
It is a projection rather than a refusal because the label is an identifier, not the object's
name: several component types (`manifests`, `oci`,
`crd`, `passthrough`) and their traits accept a name over 63 characters. It is the 63-character
case of the one shortening rule, [Names and overrides](names-and-overrides.md#names-and-overrides).
Because a projected value is itself a valid component name, validation rejects an Application
in which two components share a `ComponentLabelValue` — a component named exactly like another
component's projection, or two long names whose prefix and digest both coincide — since their
`app` labels and every selector built from them would match both components' pods.

One exception on the inbound side: a component whose config reports a routing target — the
`service` kind, whose Service fronts pods another component owns — gets its
`{comp}-allow-ingress-traffic` policy on the Service's `selector` pods instead of the component
label, with each routed Service port translated to its `targetPort`. A routed port that is not
one of the Service's TCP ports is dropped (the rules are TCP, so a route to a UDP or SCTP port
is dropped the same way), and a route left with no port synthesizes no policy. A `service` of type `ExternalName` selects no pods: it reports a routing
target without labels and no port for any routed port, so a route to it synthesizes no policy
either, alone or as a member of a sibling group. That label-less
selector is a marker for "owns its Service name and selects no pods" and is never written into
an object, where it would select every pod of the namespace; a config that reported it together
with a port fails the build. A sibling group whose `service` member fronts its own sibling's pods on
unmapped ports keeps the component label, with the ports still translated and filtered the same
way (see Same-name sibling groups in [Transform, extension and lowering](transform-and-lowering.md)).

A routing trait's ports for the Service of the component it runs on belong to whichever
component owns that Service name, not to the routing component. When another component owns it
(a `service` with `objectName` set to the routing component's name, say), the allow lands on
that owner's pods through the routing target above, and the routing component gets none; a
Service no component owns keeps the allow on the routing component.

A synthesized `NetworkPolicy` is constructed with **no labels and no annotations** —
only `metadata.name` and `metadata.namespace`, plus the spec. The one label it then gets
is the component label of the component it was synthesized for, like every object that
component owns; the external-backend policy
(`{service}-allow-ingress-traffic`) belongs to no component and stays unlabelled. A
consumer attributes a policy through its application: `GeneratedApplication.Component`,
or `ComponentName()` on the application's config, names the component, and is empty for
the external-backend policy ([Component label and ownership](labels-and-ownership.md#component-label-and-ownership)).

No synthesized policy shares a selector with its inputs or with another policy. Every
peer's `podSelector` and every policy's `spec.podSelector` is a `DeepCopy` of the traffic
source, egress peer, backend or endpoint selector it came from. The inputs are retained
trait configuration, reused by every rule and every policy built from them, so a label a
consumer stamps onto one generated policy would otherwise reach all of them.

**Where a route lands: `Transformer.RoutedBackends`.** `RoutedBackends(app, ctx)` reports, for
every route of every routing trait (`ingress`, `httproute`) of the document, the Service it
names, the component that owns that Service, the pods the Service selects and, for each routed
port, whether the Service has it. A downstream consumer calls it to check its own view of the
document against what the routing traits resolve, whether or not any traffic source is
configured. It is read-only and works on the whole document, not on one component.

- **One row per routing sub-application and Service** (`RoutedBackend`): `Component` carries the
  trait, `Application` is the sub-application's name, `Service` the Service the route names, and
  `Owner` the component that owns it (empty when no component owns it). A route to the
  component's own Service names the routing component, unless another component declares a
  Service of that name: then that component is the owner and its Service answers the ports. A route to the component's own Service gets a row
  only when it routes a port to it; a route that names only other components' Services gets no
  such row. For a route to the component's own Service, `Service` is the Service name the
  routing trait resolves for the application it ran on: that application's backend Service name,
  else its name. When the component owns a Service of another name (a sibling group's, named
  apart by a consumer's naming hook), that route is to a Service the component's routing target
  does not describe: the row reads `owner` pods and its ports `unchecked`. The paths of one
  route to an owned Service are one row, with each port once. A
  Service no component owns gets one row per backend selector the route authored for it, because
  those select different pods. Rows are ordered by `Component`, `Application` and `Service`; those three are
  not a unique key, because equally named sub-applications can sit in different bundles of one
  component.
- **Pods** (`RoutedPods.Kind`): `selector` (the owner is a routing target, such as a `service`,
  a `webservice` or a sibling group forwarding to one; `Selector` is its Service's selector),
  `owner` (a component owns the Service but is not a routing target, such as a `deployment` or
  a chart release; the route lands on that component's own pods), `none` (an `ExternalName`
  Service, which selects no pods) and `unowned` (no component owns the Service; `Selector` is
  the `backendSelector` the route authored, if any).
- **Ports** (`RoutedPort.Status`): `matched` (a TCP port of the Service matches the routed port,
  by number or by name; `TargetPorts` are the ports its pods listen on), `unmatched` (the owner
  was asked and no TCP port matches; the route is reported, not dropped) and `unchecked` (launcher
  has no list of Service ports to ask: the `owner` and `unowned` kinds). `unchecked` is neither
  a match nor a mismatch, and is never guessed. `RoutedPort.Port` is a Service port, a number or a
  Service port name, and is never a container port.

Limits. The method runs the transform up to the point where the synthesis would start and
stops there: an error raised before that point is returned, and what runs after it (the
NetworkPolicy, egress and endpoint-ingress synthesis, naming, decoration, the cluster-wide name
checks, component ownership) is not seen, so the result is not the build's verdict on the
document. Matching is TCP-only, like the synthesis: a route to a UDP or SCTP port reads
`unmatched`. A routing sub-application a trait put straight into a child bundle has no recorded
owning application, so a route to its own Service takes its Service name from the component's
application, which for a sibling group is the group. The result does not say which trait type
a route came from. A consumer that has already transformed the document pays for a second
transform; recording the result in `PolicyResult` is a deliberate later step.

