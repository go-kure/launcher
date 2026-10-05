# OAM Model, Parser & Transformer

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` is launcher's core: the OAM data model, YAML parser, semantic
validator, and the transform pipeline that turns an `Application` + `ClusterProfile`
into Kubernetes manifests. Every document this package parses uses
`apiVersion: launcher.gokure.dev/v1alpha1` (`SupportedAPIVersion`); the one exception is
the raw-document lowering seam, where a `RawDocumentLoweringRule` may claim a kind under a
consumer-owned group via `RawDocumentAPIVersioner` (see "Lowering entry points" below).

## Document kinds

| Kind | Type | Purpose |
|------|------|---------|
| `Application` | `Application` | The app: `components[]` (each with `type` + `properties`) and `traits[]`. |
| `Package` | `Package` | A parameterized, distributable unit: `app.yaml` + a `kurel.yaml` parameter schema (`ParameterDecl`). |
| `ClusterProfile` | `ClusterProfile` | Platform choices (trait implementations, capabilities) supplied at build time. |
| `CapabilityDefinition` | `CapabilityDefinition` | Declares a capability's rendering/property schema for validation. |

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

The transform orders components only where the document declares an order
(go-kure/launcher#783). No component type comes before another by itself. Three
declarations order components:

- **A tier.** A `placement` policy, or the `<domain>/tier` annotation it replaces, puts a
  component in `infra`, `services` or `apps`. Every component of a populated tier comes
  after every component of the populated tier before it. A component nothing places is in
  no tier and takes no part in that order, so one populated tier alone orders nothing.
- **A `dependency` policy rule.**
- **A lowering rule's order between the components it emits** (`Component.OrderAfter`).
  The `helm` rule orders its release after the source it generates, and the `oci` rule
  each Kustomization after the source several `oci` components share.

An application is always one bundle, named after the Application. With nothing declared
it is flat: the one bundle holds every component. Otherwise the components are split into
ordered groups, the topological levels of the declared order: the first group holds every
component that comes after no other, each further group the components whose predecessors
are all in earlier groups. Each group is a child bundle of the application bundle and
depends on the group before it. A group that is exactly the components of one tier is
named `<application>-<tier>`; any other is `<application>-<NN>`, its two-digit position
counted from `00`. A group name is held to 253 characters, the limit of a name that names no
delivery engine ([Names and overrides](#names-and-overrides)). A stricter limit of the object
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
kure's layout walker does. Since go-kure/kure#979 the root node's directory renders no
bundle: the application bundle has a directory of its own inside it, named after the bundle,
which the root's `kustomization.yaml` does not list. `Bundle.DirName` on the bundle launcher
returned gives that directory another name (go-kure/kure#972). **Breaking output change** for such a
consumer: with `layout.DefaultLayoutRules()`, everything launcher returns is written one
directory down. Below, `<bundle>` is the application bundle's name: the Application's name,
unless the `Naming` hook renames the bundle. The root node is unnamed whether or not the
components are ordered, so `<bundle>/` is in the same place for both:

| Application | Before | Now |
|-------------|--------|-----|
| Flat: every component | `cluster/` | `cluster/<bundle>/` |
| Ordered: the generated sources | `<bundle>/` | `cluster/<bundle>/` |
| Ordered: a group | `<bundle>/<group>/` | `cluster/<bundle>/<group>/` |

**Breaking output change** for a layout-walking consumer of an ordered application
(go-kure/launcher#783): its root node was named `<bundle>` until then, which put a directory
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
(`cluster` or the `ClusterName` directory above), the one kure's bootstrap points Flux at
since go-kure/kure#979.

With the default `BundleGrouping: GroupFlat`, `ManifestLayout.OriginUnit()` on the root
node's layout returns the bundle directory's layout; it is nil on every other layout, and on
every layout under `BundleGrouping: GroupByName`. `WalkCluster` returns the root node's
layout.

Launcher adds no limit of a delivery engine to the names it returns. A document name over
63 characters builds, and kure's Flux workflow then refuses its bundle unless the consumer
names it: `Bundle.KustomizationName` on the bundle launcher returned, or the `Naming` hook
for the `bundle` and `group` roles
([Name roles and the `Naming` hook](#name-roles-and-the-naming-hook)).

Under `FluxIntegratedPerLayout` placement kure also makes a Kustomization for each
application layout and each hook-group layout. Its default name is
`<unit name>-<layout name>`, the unit name being the bundle's Kustomization name, and kure
refuses a name over 63 characters and shortens nothing. Launcher leaves that default on a
component layout, `<bundle>-<component>`, and never overwrites a `KustomizationName` a caller
set there. On each `helmtemplate` hook-group child launcher sets
`ManifestLayout.KustomizationName` itself (go-kure/launcher#787): the child's own name,
`<application>-<component>-NN-<phase>`, shortened to 63 characters by the one shortening
rule with the `-NN-<phase>` suffix kept whole
([Names and overrides](#names-and-overrides)). The bundle's name no longer leads it, so the
application name is not in it twice. The child's layout name, its directory, stays held to
253 characters, so past 63 the Kustomization's name and the directory's differ. The next
group's `spec.dependsOn` follows the Kustomization's name: the child's `DependsOn` lists the
sibling's layout name, and kure writes that sibling's Kustomization name. The prefix
`<application>-<component>` is the `hook-group` name role
([Name roles and the `Naming` hook](#name-roles-and-the-naming-hook)): an author or the hook
sets another, which is never shortened. A consumer that walks the tree itself can still set
the field on a walked child before the integration.

**Breaking output change** (go-kure/launcher#787): under `FluxIntegratedPerLayout` placement
the Kustomization of a hook-group child loses the leading `<unit name>-`
(`shop-shop-db-01-main` becomes `shop-db-01-main`), and one over 63 characters, which kure
refused, is now shortened and builds. Directory names do not change.

Launcher sets no Flux delivery field on any bundle it returns: `Interval`, `RetryInterval`,
`Timeout`, `Prune`, `Wait`, `Force`, `Suspend`, `HealthChecks`, `Patches` and `PostBuild`
stay unset (go-kure/launcher#781). How an application is delivered (which Flux
Kustomization applies it, how readiness is judged, how often it reconciles) belongs to the
consumer that delivers it; see `docs/delivery-scope.md`.

A consumer that sets one on a bundle launcher returned gets what kure's Flux workflow makes
of it. Under `FluxIntegratedPerLayout` placement five settings of the bundle reach further
than its own Kustomization since go-kure/kure#1016: `Wait`, `Timeout`, `RetryInterval`,
`Labels` and `Annotations` are also on the Kustomization of each component layout and of each
`helmtemplate` hook-group child (`shop-db` and `shop-db-NN-<phase>`, beside `shop`). The
other fields stay on the bundle's Kustomization; `spec.interval` and `spec.prune` of a
per-layout Kustomization are the generator's. With `Wait`, a hook group's Kustomization is
Ready once the objects it applied are, not once it has applied them, so the next group, whose
`spec.dependsOn` names it, starts after that. A hook group depends on the group before it and
never on the layout above, so the tree integrates with an inherited wait, and the names and
the `dependsOn` chain do not change. A walked layout can set its own value before the
integration (`ManifestLayout.Wait`, `Timeout`, `RetryInterval`, `Labels`, `Annotations`): one
`Timeout` inherited by the bundle's, the component's and the hook groups' Kustomizations gives
the innermost as long as the outermost, and a shorter one on the layouts below is the
consumer's to set. kure refuses three things it wrote out before: a bundle label or annotation
the Kubernetes API does not accept, where a per-layout Kustomization inherits it; a layout a
consumer added that depends on the application layout above it, once the bundle sets `Wait`
(set `Wait` to a pointer to `false` on the application's layout); and, under any placement, a
duration the Flux API does not take, a negative one or one above zero and under a millisecond,
in a bundle's `Interval`, `Timeout` or `RetryInterval` (in a layout's `Timeout` or
`RetryInterval` only where the layout gets a Kustomization of its own). kure's
`pkg/stack/fluxcd` README has the rules under "Per-layout settings" and "Durations".

**Breaking output change** for a consumer (go-kure/kure#1016, with the kure commit `go.mod`
pins). Under `FluxIntegratedPerLayout` placement, a bundle that sets one of the five gives
them to the component's and the hook groups' Kustomizations, and a tree kure refuses for the
first or the second reason above no longer renders. Under any placement, a tree with a
duration kure refuses no longer renders. What launcher returns does not change, and a tree
that sets none of the five and no such duration renders as before.

kure reads the resources a layout holds as kustomize builds them, a List standing for its
items, by one rule in its pre-write checks and in its Flux integration (go-kure/kure#1017).
Launcher's own decode paths put no List into a layout (kure's parser replaces a list document
by its items, and `passthrough` refuses a list); a Go caller's own `ApplicationConfig` that
returns one has it read by that rule.

An application can carry a delivery intent, `stack.Application.Delivery`: what it asks of the
engine that delivers its objects, stated without naming the engine. The `prune-protection` and
`force-replace` traits set it (go-kure/launcher#782), on the component's application and on
each of its trait sub-applications; a sibling group's one application takes each intent that
any of its members has. Launcher writes no Flux annotation for either trait. kure's Flux
workflow turns the intent into `kustomize.toolkit.fluxcd.io/prune: disabled` and
`kustomize.toolkit.fluxcd.io/force: enabled` on everything the application's layout holds,
where it integrates a layout (`CreateLayoutWithResources`, `IntegrateWithLayout`); the
[traits README](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits) says
what that covers.

**Breaking library changes** (go-kure/launcher#782):

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
than after every component of the bundle (go-kure/launcher#712). A trait handler that moves
an application or removes a sub-application keeps the order it left (go-kure/launcher#718); one
that replaces, removes or renames a component's application fails the transform (go-kure/launcher#734,
see `TraitHandler` below). A trait whose handler
implements `SubApplicationDecorator` (the built-in `prune-protection` and `force-replace`, which
set a delivery intent) is also applied to those sub-applications, whatever order the traits were authored in: the last step of
the transform, after the Phase-4 synthesis below, applies it to each of them. The NetworkPolicies
that synthesis adds are no component's sub-applications and stay undecorated.

Under `TransformContext.FluxNamespace`, every config that takes it (`SetFluxNamespace`: the
`helmrelease`, `fluxcd-kustomization` and Flux source kinds) moves its Flux objects there, and a trait
sub-application of that component follows only when the Flux object reads it by name from its own
namespace (go-kure/launcher#740). The config reports what it reads (`FluxNamespaceReads`: a
HelmRelease's `valuesFrom`, `kubeConfig` and chart-template `verify` Secret, a source's
`secretRef`, `certSecretRef`, `proxySecretRef` and the Secrets under `verify` and `sts`); a
sub-application config names the ConfigMap or Secret it produces (`FluxNamespaceInput`: the
`configmap` trait's ConfigMap, the `secret` trait's Secret, the Secret an `external-secret` trait's ExternalSecret or a
`certificate` trait's Certificate writes). Both kind and name must match. Every other trait object —
a ConfigMap or Secret the Flux object does not name, a Certificate whose Secret it does not name, a
claim, a NetworkPolicy, a route — stays in the application namespace with the
workloads, where a HelmRelease installs them (`targetNamespace`). A ConfigMap or Secret the Flux
object names moves even when the chart's pods read it as well: the Flux object cannot reconcile
without it, and a reference through the chart's values is invisible here. Every built-in config
that takes the Flux namespace reports its reads, possibly none; decorators and sibling
groups forward them.

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
  a `Naming` hook that gives one of the two policies another name avoids it (see "Name roles and
  the `Naming` hook").
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
  the `ComponentLoweringRule` claiming its type, via the optional `EndpointProvider` interface and read through `Transformer.ComponentEndpoints` (or `ComponentEndpointsNamed`, for a consumer that sets a `Naming` hook; see "Name roles and the `Naming` hook") — the
  producer half a downstream platform uses to learn the real selector (no hardcoding) and build
  its dependency graph. One policy is emitted **per distinct endpoint**: a single-endpoint
  component keeps the bare `{comp}-allow-endpoint-ingress` name, while a multi-endpoint component
  (e.g. a CloudNativePG cluster plus its pooler) suffixes each policy with a short content hash of
  the endpoint, so the names are distinct and stay stable across unrelated endpoint additions. The
  suffix names the emitted **NetworkPolicy resource** itself (not just the internal layout entry),
  so a multi-endpoint component's resource ids are unique and `kustomize build` accepts them.

The inbound/egress families select the component's own pods (the ingress recipients / the egress
source pods) via a **derived `<domain>/component`** label by default — the domain comes
from `TransformContext.Domain` (empty ⇒ the library default `gokure.dev`; the kurel CLI
uses `launcher.gokure.dev`). The full key is overridable per transform through
`TransformContext.ComponentLabelKey` (precedence: `ComponentLabelKey` > `<Domain>/component`
> `gokure.dev/component`). Launcher puts that label on every object a component owns and on
its pod templates itself (go-kure/launcher#788, [Component label and
ownership](#component-label-and-ownership)), so the selector matches the component's pods
with no caller labelling anything. A caller that sets `ComponentLabelKey` to a key its
objects already carry (e.g. `"app"`) keeps the values they carry: the label is added only
where the key is absent.

The selector **value** is `ComponentLabelValue(name)` of the authored component, not the raw
component name, and the label launcher adds has the same value. A component name is a DNS-1123
subdomain (up to 253 characters), a label value at most 63: `ComponentLabelValue` returns a
name of 63 characters or fewer unchanged and projects a longer one onto a readable prefix of at
most 52 characters (its first 52, with trailing `-`/`.` trimmed) plus `-` and the first 10 hex
characters of its sha256. The projection is deterministic, so every component-identity label and
selector that uses it gets the same value for one component. The `app` label and the `app`
selectors the built-in components and traits generate to identify a component use the same
function (go-kure/launcher#572); authored
labels and selectors (a `service` component's `selector`, a `networkpolicy` peer) are emitted as
written, and a type that emits authored objects (`passthrough`, for one) adds no `app` label.
Selectors under an operator's own label and naming contract — the `postgresql` endpoints'
`cnpg.io/cluster` and `cnpg.io/poolerName` — carry the operator's values and are not
component-identity labels.
It is a projection rather than a refusal because the label is an identifier, not the object's
name: several component types (`manifests`, `oci`,
`crd`, `passthrough`) and their traits accept a name over 63 characters. It is the 63-character
case of the one shortening rule, [Names and overrides](#names-and-overrides).
Because a projected value is itself a valid component name, validation rejects an Application
in which two components share a `ComponentLabelValue` — a component named exactly like another
component's projection, or two long names whose prefix and digest both coincide — since their
`app` labels and every selector built from them would match both components' pods.

One exception on the inbound side: a component whose config reports a routing target — the
`service` kind, whose Service fronts pods another component owns — gets its
`{comp}-allow-ingress-traffic` policy on the Service's `selector` pods instead of the component
label, with each routed Service port translated to its `targetPort`. A routed port that is not
one of the Service's TCP ports is dropped (the rules are TCP), and a route left with no port
synthesizes no policy. A `service` of type `ExternalName` selects no pods: it reports a routing
target without labels and no port for any routed port, so a route to it synthesizes no policy
either, alone or as a member of a sibling group (go-kure/launcher#790). That label-less
selector is a marker for "owns its Service name and selects no pods" and is never written into
an object, where it would select every pod of the namespace; a config that reported it together
with a port fails the build. A sibling group whose `service` member fronts its own sibling's pods on
unmapped ports keeps the component label, with the ports still translated and filtered the same
way (see Same-name sibling groups below).

A synthesized `NetworkPolicy` is constructed with **no labels and no annotations** —
only `metadata.name` and `metadata.namespace`, plus the spec. The one label it then gets
is the component label of the component it was synthesized for, like every object that
component owns (go-kure/launcher#788); the external-backend policy
(`{service}-allow-ingress-traffic`) belongs to no component and stays unlabelled. A
consumer attributes a policy through its application: `GeneratedApplication.Component`,
or `ComponentName()` on the application's config, names the component, and is empty for
the external-backend policy ([Component label and ownership](#component-label-and-ownership)).
Since go-kure/launcher#361 the absence of other labels is a
property of the object as constructed rather than a scrub: kure's release-1 builder
contract (`go-kure/kure` ≥ `v0.2.0-beta.11`) makes `CreateNetworkPolicy` return TypeMeta
and identity only, so `netpol_synthesis.go`'s `np.Labels = nil` / `np.Annotations = nil`
lines no longer strip a constructor-stamped `app:` label and annotation — they are kept
as a guard, not as a fix-up. Everything else on the policy (`spec.podSelector`,
`spec.policyTypes`, the rules) is written by the synthesizer as a direct field
assignment; nothing upstream of it supplies a default.

No synthesized policy shares a selector with its inputs or with another policy. Every
peer's `podSelector` and every policy's `spec.podSelector` is a `DeepCopy` of the traffic
source, egress peer, backend or endpoint selector it came from. The inputs are retained
trait configuration, reused by every rule and every policy built from them, so a label a
consumer stamps onto one generated policy would otherwise reach all of them
(go-kure/launcher#396).

## Component label and ownership

Every application of a transformed document belongs to one authored component, or to the
document as a whole (go-kure/launcher#788). The transform records that as its last step, and
two things follow from it: each application reports its owner, and each object it generates
carries the owner's component label.

| Application | Owner |
|-------------|-------|
| A component's own application, or a same-name sibling group | The component. |
| A component a lowering rule emitted for it, under any name (the `postgresql` pooler `<comp>-pooler`, a database, an object store) | The authored component the rule lowered. |
| A sub-application a trait added | The trait's component. |
| A synthesized `NetworkPolicy` (`{comp}-allow-ingress-traffic`, `{comp}-allow-egress-traffic`, `{comp}-allow-endpoint-ingress`) | The authored component of the entry it was synthesized for. |
| A generated source the application bundle holds (go-kure/launcher#783), which every consumer shares | None: the document as a whole. |
| The external-backend policy (`{service}-allow-ingress-traffic`) | None. |

**Provenance.** `GeneratedApplication.Component` is the owner, and empty for an application
the document as a whole owns. After `Transform`, every `stack.Application.Config` is
launcher's ownership wrapper: `ComponentName()` (`ComponentNamed`) answers the same value on
it. The wrapper forwards every optional contract launcher reads on a config after the
transform (`Validate`, the Flux namespace, Service port and routing answers,
`ServiceAccountNamer`, the single-pod claim, pod template labels, identity target ports, the
layout contracts). A test fails when the code asserts or switches on a new type directly on
an application's config (`app.Config.(T)`, `switch app.Config.(type)`) and the wrapper's
list does not account for it. It reads syntax only: a config first copied to a variable or
passed to a function, and asserted there, is outside what it sees. A caller that needs the
concrete config, or a contract of its own, reads `UnwrapConfig(app.Config)`; the wrapper is a
`ConfigWrapper`
(`WrappedApplicationConfig()`). An application a caller adds to the cluster itself has no
wrapper and reports its `ComponentNamed` answer, else its name, as before.

**The label.** `<key>: ComponentLabelValue(<owner>)`, with the key the synthesized
NetworkPolicies select (`TransformContext.ComponentLabelKey`, else `<Domain>/component`),
goes on:

- every object the application generates, in `metadata.labels`;
- the pod template of a `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `ReplicaSet`,
  `ReplicationController` or `PodTemplate`, and the job template's pod template of a
  `CronJob`, typed or unstructured;
- the same places on every object the config adds to a layout it augments
  (`layout.LayoutAugmenter`): the objects that were not on the layout, or on a layout below
  it, before the config's `AugmentLayout` ran. What was on the layout already is left as it
  is: the layout walker puts there what the application generated, which carries the label;
- nothing an application without an owner generates.

It is added **only where the key is absent**. An authored object or pod template that already
carries the key (a `manifests` or `passthrough` document, an authored pod label) keeps its
value, as a bundle's labels never replace an object's own. Launcher therefore does not make
the label authoritative: a consumer that needs every object of a component to carry exactly
the component's value enforces that in its own pass over the `GenerateApplications` result,
by overwriting the key or by refusing a document whose value differs.

The label is written into a label map of the object's, or the pod template's, own. A config
that uses one map for an object's labels, its selector and its pod template keeps that map
as it is, so a selector never gains the key.

A workload whose own selector rules the label out keeps its pod template as written: a
selector that matches the template and would stop matching it with the label, by a
`DoesNotExist` on the key or a `NotIn` holding the component's value. The cluster refuses a
workload whose selector does not match its template, so launcher does not add the label
there. Such a workload then carries no component label on its pods, and a synthesized policy
does not select them. The workload object itself still carries the label. A
`ReplicationController`'s selector is a plain label map, which a further label on its pods
never fails, so its pod template always gets the label.

An unstructured object's labels are read as written, on the object and on a workload's pod
template. Null `metadata` or `labels` are absent ones, as is a null pod `template`, and a null
label value is the empty string the cluster reads it as. A nil map, which a config built in
Go can hold in such a place, reads as a null. A label that is not a string is
refused, with the object named, whether or not the key is there already: generation fails
rather than drop a label an author wrote.

An unstructured list envelope stands for its members when Flux applies it (as the force
warning reads one: Kustomize's build inlines a `List`, Flux's reader expands what is left, one
level). Each such member is labelled as an object handed out on its own is, a workload on its
pod template and a `HelmRelease` with its post-renderer, beside the envelope itself.

**Chart output.** A chart Flux installs is rendered in the cluster, where launcher cannot
label it. Its `HelmRelease` gets one kustomize post-renderer, after any authored ones, with
a strategic-merge patch per kind with a pod template (`Deployment`, `StatefulSet`,
`DaemonSet`, `Job`, `CronJob`, `ReplicaSet`, `ReplicationController`, `PodTemplate`) that
sets the label on the pod template, and one for a bare `Pod` that sets it on the Pod's own
labels. Each patch targets its kind in that kind's own API group: a custom resource of
another group whose kind has the same name is left alone. Flux applies it to whatever the
chart rendered, so here the component's value
**replaces** one the chart set. The entry is added once, however often the document is
transformed or generated.

The post-renderer reaches what Helm hands it. Whether that includes a chart's hook and test
Pods depends on the Helm version the helm-controller runs, and launcher has not verified
it: such a Pod may carry no component label.

The patch sets the pod template's label and touches no selector, and launcher does not look
into a chart. With a `ComponentLabelKey` that a Flux-installed chart's own selectors use
(`app`, `app.kubernetes.io/name`), the post-renderer replaces the value the chart set under
it. Where a chart's selector does not accept the component's value, that parts the selector
from the chart's pods and the cluster refuses the workload. Use a key no chart sets, such as
the default.

A chart rendered at build time
(`helm` under `delivery: template`, `helmtemplate`) yields objects launcher generates, which
are labelled like any other: where the key is absent.

**What a synthesized policy selects.** A synthesized inbound or egress policy selects the
value its entry's objects carry: the authored component's, also for an entry a lowering rule
emitted under a name of its own. The policy keeps the entry's name. A rule that emits several
pod-running entries from one component therefore gets policies that each select the pods of
all of them, as a sibling group's does. No built-in rule emits a pod-running entry under
another name: `webservice` and `worker` emit theirs under the component's own name, as a
same-name sibling group, and the `postgresql` pooler and databases run no pods launcher
generates. The case is reached only through a consumer's own lowering rule.

**What the label does not reach.**

- Pods an operator creates from a custom resource (a CloudNativePG `Cluster`'s instance pods,
  a `Pooler`'s pods): the custom resource carries the label, its pods do not. This is not a
  goal. The endpoint-ingress policy selects those pods by the operator's own labels for that
  reason.
- Pods of a kind the post-renderer has no patch for in a Flux-installed chart.

**Breaking library changes** (go-kure/launcher#788):

- Output: every owned object and pod template gains the component label, a `HelmRelease`
  gains the post-renderer, and a component's synthesized NetworkPolicies gain the label.
- A `Job` that a cluster already holds from before this change, and that nothing recreates,
  cannot take the label on its pod template: the cluster keeps a Job's pod template
  immutable, so the apply, or the Helm upgrade, fails on it with the API's immutable-field
  error. This holds for a Job a chart renders, under Flux delivery (the post-renderer) and
  under template delivery, and for one launcher generates (the `job` component, a
  `manifests` Job). Once: delete the Job so that it is recreated with the label, or apply
  with force where the deployer supports it. A `CronJob` is not affected: its job template
  may change, and the Jobs it creates afterwards carry the label. The rule is read from
  Kubernetes 1.37's Job update validation (`validatePodTemplateUpdate` in
  `pkg/apis/batch/validation`), not run against a cluster here. Its exception is a suspended
  Job with no active pods, whose template labels may change: in 1.37 with its default
  features, one that never started or that carries the `JobSuspended` condition. Which
  suspended Jobs qualify depends on the cluster's version and feature gates.
- `GeneratedApplication.Component` changes from the application's name to empty for a
  generated source the application bundle holds and for the external-backend policy, and
  from the application's own name to the authored component for a lowered component named
  differently and for a synthesized NetworkPolicy. A collision error names those
  applications accordingly (`sub-application "db-pooler" of component "db"`).
- `stack.Application.Config` is the ownership wrapper after `Transform`: a type assertion on
  a concrete config type goes through `UnwrapConfig`.

## Reserved metadata keys

A consumer that keeps label and annotation keys to itself names them in
`TransformContext.ReservedMetadataKeys` (go-kure/launcher#790). One rule holds every
application the transform returns to the list, whatever wrote the key: a kind component, a
`passthrough` or `manifests` object, a chart rendered at build time, a trait's `annotations`,
a `postgresql` or `cnpg-cluster` component's `inheritedMetadata`.

**The list.** Each entry is one of:

- a key: a Kubernetes qualified name (`example.org/tenant`, `team`), which reserves that key;
- a prefix followed by `/`: a DNS-1123 subdomain (`platform.example/`), which reserves every
  key under that prefix. It does not reserve the prefix's own name as a key, or a key under
  another domain that ends alike (`sub.platform.example/zone`).

`Transform` refuses any other entry, naming it by index
(`invalid TransformContext.ReservedMetadataKeys[1] "not a key": ...`). A repeated entry, or a
key a prefix entry already covers, is accepted. An empty list reserves nothing, and the
transform and its output are then as without the field.

**Where it is refused.** At generation, not in `Transform`: the check sits in the ownership
wrapper ([Component label and ownership](#component-label-and-ownership)), so it reads each
object as its config generated it, in `GenerateApplications` (or `Generate` on an
application), and each object the config adds to a layout it augments
(`layout.LayoutAugmenter`): one that was not on the layout, or on a layout below it, before
the config's `AugmentLayout` ran. The error wraps `ErrReservedMetadataKey` and names the
component, the object, the key and the entry that reserves it:

```text
component "web": Ingress "web-ingress": annotation "platform.example/zone" may not be set: the prefix "platform.example/" is reserved (TransformContext.ReservedMetadataKeys): oam: metadata key is reserved
```

An application the document as a whole owns (a generated source several components share)
is checked too, and the refusal names `the document` in place of a component.

**What is read.** On every object, and on each member of an unstructured list envelope as
Flux applies it (a `List`, or an envelope with `items`):

- the object's own `metadata.labels` and `metadata.annotations`;
- the pod template's labels and annotations, on a `Deployment`, `StatefulSet`, `DaemonSet`,
  `Job`, `ReplicaSet`, `ReplicationController` or `PodTemplate`, and the job template's pod
  template on a `CronJob`, each in its own API group: they become the metadata of the pods;
- `spec.inheritedMetadata` of a `postgresql.cnpg.io` `Cluster`, which the operator copies
  onto every object it creates for the cluster.

A key is read whatever its value: a value the API server would refuse, or a null, does not
hide it. Metadata that cannot be read (a `labels` that is a list) fails generation with the
object named, and is not read as holding no key.

**What launcher writes itself is exempt.**

- The `app` label and the component label key
  (`TransformContext.ComponentLabelKey`, else `<Domain>/component`), as label keys, wherever
  the check reads labels. The built-in components and traits write the first on what they
  generate, and a consumer may well configure the second under a prefix it reserves. Both
  stay checked as annotation keys, and another key under the same prefix stays reserved.
- The component label the wrapper stamps, and a bundle's labels and annotations, are added
  after the check and never read.
- The annotations the platform sets on an Ingress: the ones the `expose` rule writes from a
  capability value or one of its typed properties (`cert-manager.io/cluster-issuer`,
  `nginx.ingress.kubernetes.io/ssl-redirect`, `force-ssl-redirect`, `auth-url`,
  `auth-signin`, `auth-response-headers`), and the ones a rendering of the `ingress`
  capability supplies (a map of strings, a `map[string]string` as well as a
  `map[string]any`). They reach the `ingress` trait in its platform-reserved
  `platformAnnotations` property, apart from the authored `annotations`, and pass as that key
  **and value**, on the Ingress's own annotations only. An authored annotation of such a key
  must hold the same value (it then says what the platform says, and passes); another value
  is refused by the trait, naming the annotation and where the platform's value comes from.
  Any other annotation under a reserved prefix is an authored one and is refused, so a
  consumer can reserve `nginx.ingress.kubernetes.io/` and keep `expose` working.

**What the check does not cover.**

- A chart Flux installs (`helmrelease`, `helm` under `flux` delivery) is rendered in the
  cluster, where launcher reads nothing. The `HelmRelease` object itself is checked.
- Metadata an object hands on to others in a field of its own: `spec.commonMetadata` of a
  Flux `Kustomization` or `HelmRelease`, a StatefulSet's `volumeClaimTemplates`, a CronJob's
  `jobTemplate` metadata (its pod template is read).
- What a controller or an admission webhook adds in the cluster.
- An application a caller adds to the cluster itself after `Transform`: it has no ownership
  wrapper.
- What a config that a caller wraps around an application's config after `Transform` adds: a
  key on an object the application generated, or an object of its own. The check reads below
  the ownership wrapper, never above it, so a consumer can label the objects under a prefix
  it reserved, on a component that augments its layout as on any other.
- On a layout the config augments, what was there before its `AugmentLayout` ran. The layout
  walker puts there what the application generated, checked at generation. Two cases follow:
  an object already on the layout that the config's `AugmentLayout` edits in place is not read
  again (no built-in component edits one), and a caller that calls `AugmentLayout` itself on
  a layout holding objects that never came from `Generate` gets them unchecked.

**Library changes** (go-kure/launcher#790): a new exported field,
`TransformContext.ReservedMetadataKeys`, the sentinel `ErrReservedMetadataKey`, and the
method `PlatformAnnotations()` on the `ingress` trait's config (`traits.IngressConfig`).
The check reads that method through an unexported contract of this package, which a config
of another package can only meet with an exported method. A consumer's own config that
writes annotations from the platform's input, apart from authored ones, may implement the
same method. The check asks the application's config and every config under it that a
`ConfigWrapper` says it holds, down to what `UnwrapConfig` returns (at most 32 layers, and a
nil one ends it), and passes a pair any of them states. So a trait that wraps a
sub-application's config (none of the built-in ones does: `prune-protection` and
`force-replace` set a delivery intent and leave the config) keeps `expose` working under a
reserved prefix by being a `ConfigWrapper`, with no method to hand on. Under a wrapper that
does not say it wraps, the pairs are checked as authored.
**Breaking for documents:** an `expose` trait whose authored annotation
contradicts a value the trait writes is now refused where the trait's value used to win
silently (`pkg/oam/builtin/traits/README.md`, "Capability-aware traits").

## Names and overrides

Launcher shortens a name only when it generated that name itself, and always by one rule,
`ShortenName(name, limit)` (go-kure/launcher#793). A name of at most `limit` characters is
returned unchanged, so no document whose names fit changes output. A longer one becomes its
first `limit-11` characters with trailing `-`/`.` trimmed, a `-`, and the first 10 hex
characters (`ShortenNameDigestLength`) of the sha256 of the whole name. The result is
deterministic. Two different names over the limit share one only when their trimmed prefixes and
their 40-bit digests both coincide; a name that fits is returned as written, so it can also equal
the shortened form of a longer one. `ShortenNameWithSuffix(name, suffix, limit)` is the same rule for a
name that ends in a fixed suffix (`-hpa`, `-values-<digest>`): the name is cut, the suffix kept
whole. A suffix that leaves fewer than 10 characters of the limit (only one with an authored part
of unbounded length, such as a routing trait's `scope`) cannot be kept whole beside a full digest:
the whole `name+suffix` is then shortened by the rule.

| Limit | Constant | Generated names |
|-------|----------|-----------------|
| 63 | `ShortenLimitLabel` | The component label value, `ComponentLabelValue`, and the default name of the Flux Kustomization of a `helmtemplate` hook-group child (`ManifestLayout.KustomizationName`: `<application>-<component>-<NN>-<phase>`; the `-<NN>-<phase>` suffix is kept whole). |
| 253 | `ShortenLimitSubdomain` | Object names: `NameAllocator.Name` and `NameOrAdopt`, and the default of `LoweringContext.ResolveName` and `ResolveSharedName` (the `postgresql` pooler and databases, the `helm` values ConfigMap and values Secret, a generated Flux source), the layout name, and so the directory, of a `helmtemplate` hook-group child (`<application>-<component>-<NN>-<phase>`; the `-<NN>-<phase>` suffix is kept whole; its Kustomization's default name is the 63 row's), the claim a role component's `pvc` volume generates (`{comp}-{volume}`, each half hyphen-escaped), the synthesized NetworkPolicies (`{comp}-allow-ingress-traffic`, `{comp}-allow-egress-traffic`, `{comp}-allow-endpoint-ingress`), the `scaler` HPA and PDB, the `networkpolicy` trait's policy, the `ingress` Ingress and `httproute` HTTPRoute (`{comp}-ingress`, `{comp}-httproute`, each with an optional `-{scope}`), the managed TLS Secret default (`{comp}-tls`), the `volsync` ReplicationSource (`{sourcePVC}-backup`) and its default repository Secret name, and the bundle of an ordered group (`<application>-<tier>`, `<application>-<NN>`; the suffix is kept whole). |
| 53 | `ShortenLimitHelmRelease` | A Helm release name. The one exception to the rule: the result is what Flux helm-controller computes for a HelmRelease (the first 40 characters as cut, a `-`, 12 hex characters), so a release launcher renders itself is named as Flux would name it. |

The allocator used to refuse a `<base>-<suffix>` over 253 characters; it now shortens `base`,
keeps `-<suffix>`, and reserves the shortened name, so that name takes part in collision
detection like any other. The characters are checked on `<base>-<suffix>` as built, before it is
shortened, so an invalid character in the part the digest replaces is still refused: only the
length may be over (`SubdomainSyntaxErrors` is `IsDNS1123Subdomain` without the length rule, for
a site that shortens and validates). The full DNS-1123 check then runs on the shortened name,
which is the name emitted. A role component's PVC claim name is checked the same way.

A name an author writes, or an override of a generated name, is used as written or refused. It
is never shortened and never changed, and one that cannot be the name of its object fails the
transform with the property in the error, so the cluster never has to refuse it
(go-kure/launcher#787). The built-in traits' authored names and the rule each is checked by are
listed in the trait handlers' README (`pkg/oam/builtin/traits/README.md`). The application
bundle carries the Application's name as written.

### Name roles and the `Naming` hook

A consumer changes a name launcher generates through `TransformContext.Naming`, a
`func(NameRequest) (string, bool)` (go-kure/launcher#787). Every name of the roles below is
resolved in one order: the author's own property when the author wrote it, else the hook's
answer, else the default. The roles are a closed set, `NameRoles()`.

| Role | What it names | Default | Author property | Hook asked |
|------|---------------|---------|-----------------|------------|
| `bundle` | The application's bundle. | The Application's `metadata.name`. | none | always |
| `group` | The bundle of one ordered group, a child of the application's. | `<application>-<tier>` or `<application>-<NN>`, shortened to 253. | none | always |
| `sub-application` | An application launcher adds beside a component's own: each one a trait creates, and the one holding each synthesized NetworkPolicy. It is no object. | What the trait or the synthesis names it (`<component>-scaler`, `<component>-rbac`, the policy's default name, …). | none | always |
| `netpol-synth` | A synthesized NetworkPolicy. | `{owner}-allow-ingress-traffic`, `{comp}-allow-egress-traffic`, `{comp}-allow-endpoint-ingress`. | none | always |
| `hpa` | The `scaler` trait's HorizontalPodAutoscaler. | `<component>-hpa` | `hpaName` | unless `hpaName` is set |
| `pdb` | The `scaler` trait's PodDisruptionBudget. | `<component>-pdb` | `pdbName` | unless `pdbName` is set |
| `rbac` | Each object of the `rbac` trait, asked once per object: the Role and the RoleBinding, and with `clusterWide` the ClusterRole and the ClusterRoleBinding. | The component's name. | `name` (one for all of them) | unless `name` is set |
| `networkpolicy` | The `networkpolicy` trait's NetworkPolicy. | `<component>-allow` | `name` | unless `name` is set |
| `pooler` | The Pooler a `postgresql` component generates. | `<component>-pooler` | `poolerName` | unless `poolerName` is set |
| `database` | Each Database a `postgresql` component generates, asked once per `databases` entry. | `<component>-<database name>` | `databases[].objectName` | unless that entry's `objectName` is set |
| `object` | The one object of an authored kind component (`deployment`, `service`, `cnpg-cluster`, `helmrelease`, …). Not asked for a member a component or trait lowering rule emitted. | The component's name. | `objectName` | unless `objectName` is set |
| `helm-source` | The Flux source (HelmRepository, OCIRepository, GitRepository, Bucket) a `helm` component generates for an inline `source`, and the OCIRepository `oci` components of one artifact share. Not the source an `oci` component keeps to itself, which is the component's own (`oci-source`). | `<application>-source-<digest>`, the digest of the source's content. | `source.name`, beside an inline source | once per source, with no component; not for a source a component names with `source.name` |
| `values-configmap` | The ConfigMap a `helm` component generates under `valuesMode: configMap`. | `<component>-values-<hash>`, the hash of the stored values. | `valuesConfigMapName` | unless `valuesConfigMapName` is set |
| `values-secret` | The Secret a `helm` component generates for `secretValues`. | `<component>-secret-values-<hash>`, the hash of the stored values. | `valuesSecretName` | unless `valuesSecretName` is set |
| `helm-release` | The HelmRelease a `helm` component generates under `delivery: flux`. It names the object alone: the Helm release name (`releaseName`, `spec.releaseName`) and the default names of the values ConfigMap and Secret keep following the component name. | The component's name. | `helmReleaseName` | unless `helmReleaseName` is set; not under `delivery: template` |
| `oci-kustomization` | The Flux Kustomization an `oci` component generates, whether the component keeps its source or shares one. | The component's name. | `kustomizationName` | unless `kustomizationName` is set |
| `oci-source` | The OCIRepository an `oci` component keeps to itself: the one no other `oci` component of the document shares and no `source.name` names. | The component's name. | `source.objectName` | unless `source.objectName` is set; not for a shared source or one `source.name` names (`helm-source`) |
| `hook-group` | The prefix of the names of a `helmtemplate` component's hook-group layouts, each `<prefix>-<NN>-<phase>`: the directory of a group and its Flux Kustomization. It is no object, and the one role whose answer is a prefix and not a name: how many groups a chart has is known only once it is rendered, and the prefix is resolved before that. | `<application>-<component>` | `hookGroupNamePrefix`, on `helmtemplate` and on `helm` under `delivery: template` | once per `helmtemplate` component, unless `hookGroupNamePrefix` is set |

The `hook-group` prefix is resolved in the transform, where two components of one document
that resolve to the same prefix are refused: their groups would share names. The names are
built after the render. With the default prefix the layout name is shortened to 253
characters and the Kustomization's name to 63, each with its `-<NN>-<phase>` suffix whole, so
the two differ for a long default. A prefix the author or the hook set is used as written in
both, and is never shortened: a prefix that is no DNS-1123 subdomain is refused in the
transform, and a child name over 63 characters built from it is refused by `AugmentLayout`,
in an error that carries the component, the role and the full name. A consumer that calls
only `Generate` writes no hook-group layout and never sees that refusal. On a `helm`
component under `delivery: flux` the property is refused: a HelmRelease installs the chart,
no hook-group layout exists, and the prefix would name nothing.

**Limit: the transform holds the prefixes apart, not the names built from them.** Those
exist only after the render, and two different prefixes can still give one Kustomization
name. Two shapes:

- A shortened default equals a written prefix. `shop-<52 characters>` beside
  `-02-post-install` becomes `shop-<31 characters>-<digest>-02-post-install`, and a second
  component whose prefix is `shop-<31 characters>-<digest>` names its own post-install group
  so.
- A prefix ends as another chart's phase begins. A phase is whatever the chart's
  `helm.sh/hook` annotation says, so prefix `shop-a` with a group `-01-x-00-main` and prefix
  `shop-a-01-x` with a group `-00-main` both give `shop-a-01-x-00-main`.

The transform accepts both documents. Nothing wrong is written: kure refuses a Flux
Kustomization name used twice when the walked tree is integrated, where every name of the
walked tree is known, and names both layouts with their paths
(`Flux Kustomization name "…" is used twice, by layout "…" (spec.path "…") and by layout "…" (spec.path "…")`).
The way out is another prefix for one of the components: `hookGroupNamePrefix`, or the
`Naming` hook's answer for the `hook-group` role. A consumer that walks the tree itself can
also set `KustomizationName` on a walked child before the integration.

The hook sees every role. It is asked once for each name the transform resolves, and not at
all for a name the author set. `NameRequest` carries the Application's name, the component
(empty for the bundle, a group, an external backend's policy and a generated source the
document's components share), the role, the object's kind
as `Kind` or `Kind.group` (empty for a role that names no object) and the default as launcher
would use it: already shortened where launcher shortens a name (a group's bundle, the `hpa`,
`pdb`, `networkpolicy` and `netpol-synth` objects), and as long as it is where it does not: a
trait's sub-application default (`<component>-scaler` is 260 characters for a 253-character
component name) and the `hook-group` prefix, which is shortened only inside the names built
from it. The default is what tells apart several names of one component and role. Returning
`false` keeps the default. Answers are not cached, and one name is asked for more than once
(in the transform, and again by `ComponentEndpointsNamed`, below): the hook must be a pure
function of its request, the same answer for the same `NameRequest` whenever it is asked. A
component's own application, whose name is the component's, is not a role, and the hook is
not asked for it. The application of a trait a rule appends is a trait's like any other: the
hook is asked under `sub-application` for the one holding a `helm` component's values
ConfigMap and for the one holding its values Secret, each with the object's name as the
default.

`NameRequest.Application` is the document's name at the moment the name is made, which a later
document rule may still change. Most names are made after lowering, and carry the lowered
name where a `DocumentLoweringRule` renamed the document, as their defaults use it. A name a
lowering rule makes (`pooler`, `database`, `helm-source`, `values-configmap`, `values-secret`,
`helm-release`, `oci-kustomization`, `oci-source`)
carries the name of the document the rule is lowering. A component, trait or policy rule runs only once the document's kind is final, so
for those that is the lowered name too; a document rule that resolves a name of its own is
asked with the name of the document it was given, which it or a later document rule may then
change.

A lowering rule resolves such a name with `LoweringContext.ResolveName(base, suffix, spec)`:
the same order, the default being `<base>-<suffix>` as `Namer.Name` builds it. The hook is
there only inside `Transform`. `LowerRaws`, and a rule driven directly on a `LoweringContext`
built outside the engine, keep the defaults: the name is the author's or the default, and the
hook is not asked. A consumer whose raw rule needs a consumer-chosen name writes it into the
document it emits. Where `<base>-<suffix>` is no valid name (a suffix with a character no
object name takes), an authored name is still used; without one the name is refused, and the
hook, which has no default to be asked about, is not asked.

`LoweringContext.ResolveMemberName(member, spec)` names the one object of a kind component
the rule is about to emit under the component's name, where the rule lets the author and the
hook choose that name (the HelmRelease of a `helm` component, the Kustomization and the kept
OCIRepository of an `oci` component).
The order is the same, the default being the member's component name, used as written. The
name is resolved, recorded for the transform to claim and set on the member in the one call,
and a rule has no other way to give a member's object a name of its own, so no such name
goes unclaimed. The member keeps its component name, and with it its sibling group, its
place in the layout, its labels and what `OrderAfter` orders. A reference the rule writes to
that object (a `sourceRef`) it writes from `member.ObjectName()` after the call. `spec.Kind`
and the scope are the ones the member's handler declares (`ComponentObject`); a name set on a
member whose type declares no object is refused by the transform. On a context with no
`Namer` an authored name is validated and set, the hook is not asked and nothing is claimed.

`LoweringContext.ResolveSharedName(base, suffix, identity, spec)` is `ResolveName` for an
object the components of one document share: one its content identity determines wholly, as
`NameAllocator.EmitOrAdopt` asks of it (a generated Flux source). The first claim of a name
returns `adopted=false` and the rule emits the component under it; a later claim of the same
name and identity, from any element of the same authored document, returns `adopted=true`
and the rule only references it. Without an authored name the object is the document's: the
hook is asked once, with no component, and every later consumer of the same kind and default
takes that answer without the hook being asked again. An authored name is the component's
own and not a second name for the document's object: a component that names the object and
one that does not get two objects, two components that write one name for one identity share
it, and one name for two identities is `EmitOrAdopt`'s collision error. One name for one
identity is one object whoever chose the name: an authored name equal to the document's
object's name (its default, or the hook's answer) shares that object. The name is also
reserved as a component name, since the shared object is a component of the lowered
document.

A rule resolves a name once and keeps it. Each call names one object, so a second call that
resolves the same kind and name is refused with both named, whichever rule made it: two trait
rules of one component, or the first rule asked again. A name resolved after lowering for the
same object is refused as well, also where its component, role and default are the rule's own
(a rule naming the NetworkPolicy the transform synthesizes for its component): the rule's
object and the later one are two. Under `LowerRaws`, which lowers several
documents with one allocator, two documents may resolve one kind and name only where their
`metadata.namespace` differs. A document without one counts as a document of `default`, the
namespace `Transform` gives it when its context names none. The comparison is by the authored
namespace: `LowerRaws` does not see a `TransformContext.Namespace` a later `Transform` lands a
document in. A caller that lands two documents in one namespace that way compares the objects
they generate with `CheckCrossDocumentCollisions`, which reads each object's own namespace.

A rule takes its object to land in the document's namespace: `NameSpec.Namespace` chooses
none for it. A cluster-scoped object (a ClusterRole) has no namespace, and the rule says so
with `NameSpec.ClusterScoped`. Its name is then one object whatever the document's namespace
is: it is refused against a trait's cluster-scoped object of the same kind and name (the
`rbac` trait's ClusterRole with `clusterWide`), and under `LowerRaws` against the same kind
and name resolved for a document of another namespace. A rule and a trait handler say it the
same way, and a `NameSpec` that sets both `ClusterScoped` and `Namespace` is refused for
either. For a rule the field is the only way: without it the document's namespace applies. A
trait handler's spec that sets neither is still claimed with no namespace.

An object that follows its HelmRelease or Kustomization to the Flux namespace
(`TransformContext.FluxNamespace`) says so with `NameSpec.FluxScoped`: a generated Flux
source, and the values ConfigMap and values Secret a HelmRelease reads through `valuesFrom`.
Its name is then claimed in the Flux namespace where the transform has one, so an object of
the same kind and name in the application namespace is another object and is not refused,
and one in the Flux namespace is the same object and is. Without a Flux namespace it is
claimed in the document's, like any other. Only a lowering rule sets the field: a trait
handler names its namespace itself, and a `NameSpec` that sets it beside `ClusterScoped` is
refused.

`Transformer.ComponentEndpoints` consults no hook either: the pooler endpoint of a
`postgresql` component selects pods by the Pooler's name, and there it is the authored
`poolerName` or the default. A consumer that sets `Naming` calls
`ComponentEndpointsNamed(application, comp, naming)` instead, which asks `naming` the same
`NameRequest` the transform asks for that name, so one pure hook gives the selector the name
the Pooler gets; an answer that is no valid name for its role is refused there with the
transform's message. It is given one component, so what the transform refuses for a reason
only the document shows (a name that is already a component of the document, or that another
object of it has) is not seen there.
`application` is the document's name as the transform puts it in that request: its
`metadata.name`, or the name it has after lowering where a document rule renames it.

A name that is not the default (the author's or the hook's) must be a DNS-1123 subdomain of
at most 253 characters, and is used as given or refused, never shortened. The `pooler` role is
narrower: a DNS-1035 label, which the Pooler's name must be since its Service carries it. For
an object that is the rule of its kind. For `bundle`, `group` and `sub-application` it is
launcher's own rule, on these grounds: a bundle's and a group's name is written as the name
of a Flux Kustomization, all three become a directory segment in a written tree, and their
defaults are built from an Application or component name launcher already holds to that
rule. The cost: a hook cannot return a name with an upper-case letter or an underscore for
them. A default is used as it is, 253 characters at most where launcher shortens it.

The transform keeps the names it resolved apart. Two that name one object (group, kind,
namespace and name) or one bundle (the application's and the groups' share one space) fail
the transform, naming both: who resolved each, and whether it is the default, an authored
property or the hook's answer.

```
name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa" is named by component "web" traits[0] "scaler" (role "hpa", its default) and by component "web" traits[1] "scaler" (role "hpa", its default); give one of them another name
```

Every trait the transform applies is its own owner, whatever its place: two traits a trait
rule lowered one authored trait to collide like two authored ones. The error tells the two
apart in the fewest words that do. A trait a rule gave a sibling group member is named with
the member (`component "web" member "deployment" traits[0] "scaler"`), and so is one authored
trait forwarded to two members; a trait a rule added, where its place among the lowered
traits is the place an authored one holds in the document, is named `… traits[0] "scaler"
after lowering`; two traits a trait rule lowered one trait to are named
`…, output 1 of its lowering` and `…, output 2 of its lowering`. One trait that resolves one
name for two of its objects is refused as naming it twice. A synthesized policy is named
by its component, or by its Service (`external backend Service "db"`): two external Services
whose shortened default policy names meet are refused too.

The names a lowering rule resolved are in it with the ones resolved after lowering. They are
held until the document's namespace is known and claimed first, so a `pooler` and another
resolved name of one kind, namespace and name are refused with both named, and two of one
rule (two `databases` entries given one `objectName`) at once:

```
name collision: Database.postgresql.cnpg.io "db-orders" is named by component "db" (role "database", its default) and by component "db" (role "database", set by databases[1].objectName); give one of them another name
```

This knows only the names resolved this way: the roles above. An object of a component that
is not a kind component, one a lowering rule names without a role (the Deployment and the
Service a `webservice` component is lowered to), and the object of a trait that is not in the table (an
authored `configmap` trait's ConfigMap, an authored `secret` trait's Secret) are not in it, so
`CheckInDocumentCollisions` (below) is still what compares every generated object: a
`configmap` component given the `objectName` of a `configmap` trait's ConfigMap is refused
there, and so is a `secret` component that names the Secret of a `secret` trait:

```
generated-object collision: Secret "shop/shared" is generated by both component "shared" and sub-application "shared" of component "web"; rename one so that each object has one producer
```

The Secret a `helm` component generates for `secretValues` is in the table (role
`values-secret`), so a `secret` component under that name is a name collision instead. The remaining lowering-rule names join it in later changes of go-kure/launcher#787.

A sub-application's name is resolved and validated but not kept apart: it is not unique. A
`configmap` trait and a `pvc` trait both named `dup` each add a sub-application `dup`, one
holding a ConfigMap and one a PersistentVolumeClaim, and that is accepted. A sub-application's
name is also not its object's: a hook that renames the sub-application leaves the object's name
alone. A consumer that read a sub-application's `Name` to learn the name of its ConfigMap,
Secret, Ingress, HTTPRoute or ReplicationSource must read the generated object instead.

A trait handler resolves a name with `(*Trait).ResolveName(NameSpec)`, on the trait its
`Apply` received. Only a trait the engine applies has a hook and a claim space: on a trait
built outside a transform (a handler's `Apply` called directly) `ResolveName` validates an
authored name and returns it or the default, the hook is not consulted and nothing is kept
apart. The handler names the namespace its object is generated in (`NameSpec.Namespace`), or
sets `NameSpec.ClusterScoped` for an object that has none.

A trait that names its object itself, under no role, claims the name with
`(*Trait).ClaimObjectName(kind, namespace, name, property)`: the `ingress` trait's Ingress, the
`httproute` trait's HTTPRoute and the `cilium-networkpolicy` trait's CiliumNetworkPolicy. The
name is the handler's own (its `name` property, else its default) and is not changed, and the
`Naming` hook is not asked about it: there is no role for these objects. The claim only holds
the name against every other one of the transform, so that a second owner of the same kind,
namespace and name is refused with both named, whichever comes first:

```
name collision: Ingress.networking.k8s.io "default/api-ingress" is named by component "web" traits[0] "ingress" (its own object, set by name) and by component "api" traits[0] "ingress" (its own object, its default name); give one of them another name
```

`property` is the property the author wrote the name in, empty for the default. On a trait
built outside a transform nothing is claimed.

### `objectName`: the object of a kind component

A kind component (`deployment`, `service`, `configmap`, `cnpg-cluster`, `helmrelease`, …) is
one object, named after the component. `objectName` gives that object another name:

```yaml
- name: api
  type: deployment
  properties:
    objectName: shop-api
    image: ghcr.io/example/api:v1.0.0
```

The name is resolved in the order of every role: the author's `objectName`, else the `Naming`
hook's answer for role `object`, else the component name. A name that is not the default is a
DNS-1123 subdomain used as given or refused, and the kind's own name rule then runs on it as
it runs on a component name (a Service's is a DNS-1035 label, a CronJob's at most 52
characters, a Job's at most 63).

It names the object alone. The component keeps its name everywhere else: the `app` label, the
pod template's labels, the selectors, the component label, the main container, and every name
a trait derives (`<component>-hpa`, a `configmap` trait's default).

The engine reads the property, not the handler. A handler opts its type in by declaring its
object's kind and scope (`ComponentObjectProvider.ComponentObject`); the engine adds
`objectName` to that type's schema, resolves and claims the name, removes the property, and
hands the handler the result as `Component.ObjectName()`. A type that declares no object
(`helmtemplate`, `manifests`, `crd`, `passthrough`) refuses the property; a consumer's own
type that declares no object and a property of that name keeps its property, which the
engine does not read. A kind handler
driven directly, outside a transform, never names its object with it: its own
`PropertySchema` does not declare the property, and its `ToApplicationConfig` either passes
over it, the object keeping the component name (`configmap`, `service`, `deployment`), or,
where the handler decodes its properties strictly, refuses it as a field it does not know
(`namespace`, `helmrelease`, `cnpg-cluster`).
The object is claimed as its kind in the document's namespace, in none for a cluster-scoped
kind (`namespace`, `persistentvolume`, `storageclass`, `volumeattributesclass`,
`priorityclass`, `runtimeclass`, `ingressclass`, `csidriver`, `servicecidr`,
`cilium-bgpadvertisement`, `cilium-bgpclusterconfig`, `cilium-bgpnodeconfigoverride`,
`cilium-bgppeerconfig`, `cilium-cidrgroup`, `cilium-loadbalancerippool`,
`cilium-egressgatewaypolicy`, `cilium-clusterwidenetworkpolicy`), and in the Flux namespace for a Flux kind when the
transform has one, so it is held against every other resolved name:

```
name collision: Pooler.postgresql.cnpg.io "default/db-pooler" is named by component "db" (role "pooler", its default) and by component "pgb" (role "object", set by properties.objectName); give one of them another name
```

`objectName` and the `object` request apply only to a component no rule emitted. On a member
a component or trait lowering rule emitted the property is refused and the hook is not asked:
a rule that wants a member's name choosable resolves it itself at lowering time, under its
own role: `LoweringContext.ResolveName` for an object it names apart (`pooler`, `database`),
`LoweringContext.ResolveMemberName` for the object of a member it emits under the
component's name (`helm-release`, `oci-kustomization`, `oci-source`). What a document rule or a raw document rule returns is
authored input, the components it built as much as the ones it forwarded: the property and
the request apply there, so a document rule that wants to fix a kind component's object name
writes `objectName` itself.

The party that writes a reference names its target. What launcher writes to a renamed
component's object carries the object name:

- the `scaler` trait's `scaleTargetRef` (the HPA, the PDB and the PDB's selector keep the
  component name);
- a routing trait's own backend, the Service of the `service` component it is on;
- the Service a route is resolved against for the synthesized NetworkPolicy: a route that
  names the renamed Service resolves to the component, and one that names the component's
  name names a Service the document does not own, an external one;
- the `rbac` trait's subject on a `serviceaccount` component;
- the `sourceRef` of an `oci` component's Kustomization, where the component keeps its
  source: it names the OCIRepository by the name that object takes (`source.objectName`, or
  the hook's answer for `oci-source`). Nothing launcher writes names the Kustomization of an
  `oci` component or the HelmRelease of a `helm` component, so `kustomizationName` and
  `helmReleaseName` move no reference;
- the pod selector of a `cnpg-cluster` or `cnpg-pooler` endpoint (`cnpg.io/cluster`,
  `cnpg.io/poolerName`), which `ComponentEndpoints` reads with the authored name and
  `ComponentEndpointsNamed` with the hook's as well. A `cnpg-pooler` is refused its cluster's
  name by its object name.

What the author writes stays as written, so a reference to a renamed component is written with
its object name: a HelmRelease's `chartRef.name` or `sourceRef.name` naming a renamed
`helmrepository`, a `cnpg-pooler`'s `cluster.name`, a volume's claim name, a `statefulset`'s
`serviceName`.

```yaml
- name: charts
  type: helmrepository
  properties:
    objectName: shop-charts
    url: https://charts.example.com
- name: app
  type: helmrelease
  properties:
    chart:
      spec:
        chart: app
        sourceRef:
          kind: HelmRepository
          name: shop-charts   # the object name, not "charts"
```

Two consequences follow from the object's own kind. A `statefulset`'s pods and volume claims
are named by its controller after the StatefulSet, so they follow `objectName`. A
`helmrelease`'s release name does not: launcher writes `spec.releaseName` from the component
name unless it is authored, so renaming the HelmRelease object leaves the release, and the
names its chart derives from it, where they were.

### `labels` and `annotations`: the metadata of a kind component's object

A kind component takes two more properties the engine reads, `labels` and `annotations`, each
a map of strings (go-kure/launcher#790). They go on the metadata of the component's one object
and nowhere else:

```yaml
- name: fast
  type: storageclass
  properties:
    provisioner: csi.example.com
    labels:
      tier: fast
    annotations:
      storageclass.kubernetes.io/is-default-class: "true"
```

The pod template of a workload kind is not labelled or annotated by them, and no selector
launcher writes reads an authored label. The component label and the reserved keys apply as
on every generated object ("Component label and ownership", "Reserved metadata keys").

The engine refuses, as a `TransformError` naming the component, the property and the key:

- a value that is no map and an entry that is no string; a null value or entry is an absent
  one, as at every property;
- a label key or value, or an annotation key, the API server refuses (the apimachinery
  rules), and annotations whose keys and values hold more than 262144 bytes together;
- the `app` label with another value than `ComponentLabelValue` of the component's name,
  which the kinds that set `app` select by;
- the component label key (`ComponentLabelKey`, or the key derived from `Domain`) with
  another value than the one launcher gives the component. The ownership wrapper keeps a
  component label an object already carries, so another value would take the object out of
  the selectors generated for its component.

A key in `ReservedMetadataKeys` is refused where every reserved key is, when the object is
generated. A label that names another object is the author's literal: it does not follow that
object's `objectName` or the `Naming` hook, and there is no typed reference.

As with `objectName`, the engine reads the properties and the handler does not. For a type
whose handler declares its object (`ComponentObjectProvider`) the engine adds both to the
type's schema, checks them, removes them from the properties before any of the handler's code
runs, and hands the handler the result as `Component.ObjectMetadata()`. They are read on a
member a lowering rule emitted too, where `objectName` is refused: a member's labels are its
rule's to write, and nothing is claimed for a label. **A handler that declares its object
must put them on it**: nothing else carries them there once the properties are removed. A
built-in handler's config holds the value and its `Generate` calls
`ObjectMetadata.ApplyTo(obj)`, which adds the authored keys beside the ones the config set, in
maps of the object's own, and refuses a key the config set to another value. A type that
declares no object refuses the two properties if its schema does not declare them, and keeps
them, unread by the engine, if it declares them or declares no schema.

## Parsing

| Function | Purpose |
|----------|---------|
| `Parse` / `ParseMulti` / `MustParse` | Parse one / many Application documents. |
| `ParsePackage` | Parse a `Package` (app + parameter schema). |
| `ParseClusterProfile` | Parse a `ClusterProfile`. |
| `LoadCapabilityDefinitions` | Load `CapabilityDefinition`s for capability validation. |
| `ParseWithExtraTraitTypes` | Parse allowing additional (custom) trait types. |
| `ParseWithExtraTypes` | Parse allowing custom trait types **and** a `LowerableTypes` set — the document kinds, component types and trait types claimed by a transformer's registered lowering rules. |

Standalone parsing validates each trait's `type` against this package's own allowlist
of built-in trait types (the `security-context`, `topology-spread` and `force-replace`
traits are included, matching `SecurityContextHandler`, `TopologySpreadHandler` and
`ForceReplaceHandler`);
`ParseWithExtraTraitTypes` widens that allowlist with caller-supplied custom types.
`ParseWithExtraTypes` widens it further with `Transformer.LowerableTypes()`, so a
document authored in types that only a lowering rule understands parses ahead of the
transform that will lower them away. The widening is additive and per position: an
empty `LowerableTypes` is exactly the strict behaviour, a name claimed for one position
never admits it at another, and decoding stays strict (`KnownFields(true)`), so a kind
carrying its own authored fields still belongs to the raw lowering entry point.

**Every namespace is a DNS-1123 label** (at most 63 characters, no dots), the rule the
apiserver applies. Parsing refuses any other `metadata.namespace`, `LowerRaws` refuses it
on a raw input, and `Transform` refuses a `TransformContext.Namespace` (the kurel
`--namespace` override) or `TransformContext.FluxNamespace` that breaks it, before
lowering. The message names the value, e.g.
`namespace "team.prod" is not a valid DNS-1123 label (…)`.

**Component `type` is validated against this package's own allowlist, not against
the caller's handler registry.** The two are independent lists that happen to agree:
parsing rejects an unknown component type before any handler is consulted, so
registering a `ComponentHandler` in `pkg/cmd/kurel` does *not* by itself make its type
name authorable — the name must also be added here. A type registered on one side only
is registered-but-unusable (every document naming it fails to parse) or
parseable-but-undispatchable, and in both cases a handler-level test suite stays green.
The kind components `namespace`, `limitrange`, `resourcequota`, `persistentvolume`,
`pod`, `replicaset`, `replicationcontroller`, `podtemplate`, `storageclass`,
`volumeattributesclass`, `priorityclass`, `runtimeclass`, `ingressclass`, `csidriver`,
`ingress`, `httproute`, `networkpolicy`, `cilium-networkpolicy`, `servicecidr`,
`poddisruptionbudget`, `horizontalpodautoscaler`, `servicemonitor`, `podmonitor`,
`prometheus-probe`, `prometheusrule`, `issuer`, `clusterissuer`, `certificate`,
`cilium-bgpadvertisement`, `cilium-bgpclusterconfig`, `cilium-bgpnodeconfigoverride`,
`cilium-bgppeerconfig`, `cilium-cidrgroup`, `cilium-loadbalancerippool`,
`cilium-egressgatewaypolicy`, `cilium-localredirectpolicy`, `cilium-nodeconfig`,
`cilium-clusterwidenetworkpolicy`, `gatewayclass`, `gateway`, `listenerset`,
`referencegrant` and `backendtlspolicy` (go-kure/launcher#790) are on this list.
`ingress`, `httproute`, `networkpolicy`, `cilium-networkpolicy` and `certificate` are
also trait types: the two lists are separate, and a component of such a type is the
authored object, not the trait.
`pkg/cmd/kurel`'s `TestBuiltinComponentHandlers_AcceptedByParser` is the guard: it
parses a minimal document for every registered built-in type through
`ParseWithExtraTypes`, the same entry point `kurel build` uses. One other per-type
registry has the same shape and the same failure mode: `traitComponentRestrictions`
(which traits a component type accepts; today only `scaler` is restricted, to `webservice`,
`worker` and `deployment`, the kinds that report a non-RWX claim to it).

**Tiers are declared, never derived from a type** (go-kure/launcher#783).
`ClassifyComponentWithDomain` returns the tier the component's `<domain>/tier` annotation
names, or the empty tier when it carries none; a `placement` policy replaces it. A
component in no tier is ordered after no tier (see "Pipeline"). A component a lowering
rule emits is read the same way, with one exception: a Flux source the rule orders a
component after is applied with the application bundle, so a tier on it, from an
annotation or a `placement` policy, is refused.

## Transform & extension

`NewTransformer(...)` builds a transformer from maps of component/trait handlers, and
`RegisterPolicy(type, handler)` adds an application policy handler; `pkg/cmd/kurel` registers
the built-ins. Extend the system by implementing:

| Interface | Role |
|-----------|------|
| `ComponentHandler` | `CanHandle(type)` + `ToApplicationConfig(...)` — see [components](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components). |
| `TraitHandler` | `CanHandle(type)` + `Apply(...)` — see [traits](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits). `Apply` mutates the application it is given and may append sub-applications to the bundle. It must not replace, remove or rename a component's application there, its own or any other component's of the bundle, including one whose traits already ran: the transform fails, naming the trait and the component, because NetworkPolicy synthesis finds a component's application by its name and then its pointer, and a replaced, removed or renamed one would silently get no policy (go-kure/launcher#734). Nor may it rename a sibling group member's application: the member would generate its objects under a name the group does not carry, so the transform fails, naming the trait, the group and the member's type (go-kure/launcher#752). |
| `PolicyHandler` | `CanHandle(type)` + `Apply(policy, components, result)` — validates one `spec.policies` entry and records its effect (tier overrides, dependency edges), or its own data under `PolicyResult.Extensions`, on the shared `PolicyResult`; see [policies](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies). A policy type with no registered handler fails the transform. The transform returns an error from `Apply` as `policy "<name>": <error>`, so a handler says what is wrong and does not name the policy itself. |
| `CapabilityAware` | Mark a handler as requiring a `ClusterProfile` capability. |
| `ComponentCapabilityDefaults` | `CapabilityDefaults() (key string, properties []string)` — on a `ComponentHandler` whose properties take defaults from a `ClusterProfile` capability. Before `ToApplicationConfig`, the engine fills each listed property the component leaves unauthored (absent or `null`) from that binding's rendering; an authored value, `""` included, wins. It reads only the listed keys, never the rest of the rendering, and records the key in `ConsumedCapabilities` when the profile binds it. A component a lowering rule synthesized is skipped, as a sealed trait is. Each filled value is validated against the component's own `PropertySchema`, which must declare every listed key (go-kure/launcher#751): the trait side's schema may differ. A handler that declares no schema relies on `EvaluateProfile`, which validates the binding only through the trait handler or trait lowering rule of the key's type. Its fill is refused unless that handler or rule validates the rendering: it implements `ValidateAndApplyDefaults`, or the type is not built in and has a `CapabilityDefinition`, whose schema `EvaluateProfile` applies (go-kure/launcher#772). Implemented by `persistentvolumeclaim` (`pvc`, `storageClassName`; go-kure/launcher#742). |
| `ComponentCapabilityFiller` | `FillCapabilityDefaults(props map[string]any, lctx LoweringContext) (map[string]any, error)` — on a `ComponentHandler` whose capability defaults land below the top level of its properties, where `ComponentCapabilityDefaults` cannot reach. The engine calls it right after `ComponentCapabilityDefaults`, on a component no lowering rule synthesized, and passes the result to `ToApplicationConfig`; an error fails the component. On a kind component `props` no longer holds `objectName`, which the engine took out. It must not mutate `props`, and reads a binding only through `lctx.Capability`, which records the key in `ConsumedCapabilities`; `lctx` carries nothing else. The handler validates the values it fills; the engine's schema check covers only `ComponentCapabilityDefaults` keys. Implemented by `statefulset` (`pvc`'s `storageClassName` into each `volumeClaimTemplates` entry that leaves `storageClass` unauthored; go-kure/launcher#761). |
| `PropertySchemaProvider` | Declare a `PropertySchema` for the handler's user-facing properties (see below). |
| `ContractDescriber` | Declare `ContractMetadata` — contract family, version, required capability keys, deprecation info (see below). Every built-in handler and lowering rule implements it. |
| `LoweringTargetDeclarer` | `LoweringTargets() LoweringTargets` — on a lowering rule of any kind: the component, trait and policy types it lowers into. `Transformer.Seal` refuses a registry in which one of them is not registered (see Contract metadata). Every built-in lowering rule implements it. |
| `ComponentNamed` | Expose the owning OAM component (`ComponentName() string`) on a trait/component sub-app config, so consumers can attribute each emitted resource to its component without re-deriving it from sub-app names. The value is the raw component name; a consumer writing it into a label or selector passes it through `ComponentLabelValue` first. |
| `ApplicationNameSetter` | `SetApplicationName(name string)` — on a component config that builds a name out of the OAM application it belongs to, so the name differs when two differently named applications each have a component of the same name (the application's namespace is not part of it). The transform calls it once, right after `ToApplicationConfig` and before policy and traits, with the name of the document it transforms (the name the application's bundle carries). A config built directly, outside a transform, is never told one. Implemented by `helmtemplate`, whose hook-group child layouts are named `<application>-<component>-NN-<phase-slug>` (go-kure/launcher#792). |
| `HookGroupNamePrefixSetter` | `AuthoredHookGroupNamePrefix() (prefix string, authored bool)` and `SetHookGroupNamePrefix(prefix string)` — on a component config whose `AugmentLayout` partitions it into hook-group layouts. The transform resolves the `hook-group` name role for it once, right after `ApplicationNameSetter`: the prefix the config reports as authored (the `hookGroupNamePrefix` property, `HookGroupNamePrefixProperty`), else the `Naming` hook's answer, else the default `<application>-<component>`, and it calls `SetHookGroupNamePrefix` only with an authored or hook-given prefix, so a config left alone keeps its default names. A config built directly, outside a transform, sets its own. Implemented by `helmtemplate` (`HelmTemplateConfig.HookGroupNamePrefix`; go-kure/launcher#787). |
| `SubApplicationDecorator` | `DecoratesSubApplications() bool` — on a `TraitHandler` whose `Apply` decorates an application's objects. When it returns `true`, the engine also calls `Apply` on every sub-application the component's traits appended to the bundle, as the last step of the transform, so trait order does not matter; a trait forwarded to several sibling-group members decorates the group's sub-applications once. `Apply` must not add, remove, replace, rename or reorder the bundle's applications there (the transform fails), nor rename a sibling group member's application, which the bundle does not hold: the transform fails, naming the trait, the sub-application it was decorating, the group and the member's type (go-kure/launcher#763). Implemented by `prune-protection` and `force-replace`, whose `Apply` sets the application's delivery intent and wraps nothing (go-kure/launcher#782). |
| `ServiceAccountNamer` | `ServiceAccountName() (name string, runsPods bool)` — the ServiceAccount a workload component's pods run as: the authored `serviceAccountName`, or `""` when none is authored (no pod kind generates an account, go-kure/launcher#702; a `webservice`/`worker` hands its `deployment` member the name of the account it generates). `runsPods` reports whether the config runs pods at all; a trait decorator or sibling group that wraps no pod-running config reports `false`. Traits that bind identity to the workload (the `rbac` trait's binding subject) read this instead of assuming the component name, and `rbac` refuses a pod-running component with no name. Implemented by every built-in pod kind config. **Breaking library change**: the method gained the `runsPods` result. |
| `LayoutAugmentationCoverage` | `GenerateCoversAugmentLayout() bool` — for a config that also implements kure's `layout.LayoutAugmenter`, declare whether `Generate` alone already produces every resource `AugmentLayout` places into the layout. `kurel build` (which never walks a `layout.ManifestLayout`) uses this to fail closed: an augmenter that doesn't implement this interface, or that implements it and returns `false`, is rejected outright rather than silently dropping layout-level resources from the output. |
| `ConfigWrapper` | `WrappedApplicationConfig() stack.ApplicationConfig` — a config that wraps another one and says so. The ownership wrapper is one; `UnwrapConfig(cfg)` returns the config under every such wrapper. After `Transform` every application's config answers `ComponentNamed` through the ownership wrapper: the authored component, or `""` for an application the document as a whole owns ([Component label and ownership](#component-label-and-ownership)). |

`PolicyResult.Extensions` is a `map[string]any` a consumer's policy handler writes its own
result into, under a key it owns (a domain-qualified name, as a label key is). Launcher
neither reads nor changes it: `TransformWithPolicy` returns it as the handlers left it, and
no built-in handler writes one (go-kure/launcher#781). A consumer that delivers through Flux
uses it to carry what its own `reconciliation` or `health-checks` handler read. Launcher has
no handler for those two policy types, nor for the `fluxcd-patches` and `fluxcd-postbuild`
traits: a document using one fails the transform with `no handler for policy type
"reconciliation" (policy "<name>"): it configures delivery, which launcher leaves to the
consumer that delivers the application; a consumer that delivers through Flux registers its
own handler` (`no handler for trait type "fluxcd-patches" (on component "<name>"): …` for
the two traits).

`PolicyResult.ConsumedCapabilities` is the sorted, deduped set of capability keys this
app's traits actually resolved against `ctx.Capabilities` during the transform — a real
`ClusterProfile` match, not every syntactically possible key a trait could name — plus
every bound key a `ComponentCapabilityDefaults` handler takes defaults from
(go-kure/launcher#742), and every key a component, trait, document or policy lowering rule reads through
`LoweringContext.Capability` (go-kure/launcher#686), as a `ComponentCapabilityFiller`
handler does (go-kure/launcher#761). A read by a
`RawDocumentLoweringRule` under `LowerRaws` is not among them: `LowerRaws` returns no
`PolicyResult`, and the capabilities the rewritten document's traits resolve against are
recorded when `TransformWithPolicy` runs on it. Populated
only by `TransformWithPolicy` (nil on the plain `Transform` path, which discards
`PolicyResult` entirely); a downstream consumer that needs this signal must call
`TransformWithPolicy`. It replaces a downstream consumer's own interim, purely syntactic
candidate-key derivation with the authoritative one launcher's own resolution already
computes internally.

A lowering rule reads a capability only through `lctx.Capability(key)`, which returns
the binding and whether the profile has one, and records a key it finds. The
`LoweringContext.Capabilities` map field is gone. A downstream rule author migrates
`lctx.Capabilities[k]` to `binding, ok := lctx.Capability(k)`. A test driver that calls
a rule directly, and used to set the field, uses
`lctx.WithCapabilities(m)` instead; its reads are recorded nowhere.

## Lowering

Some documents, components, traits, and policies are authored in a higher-level
vocabulary that has no direct dispatchable handler — a type a platform wants expanded
into one or more terminal types before the transform's own component/trait dispatch
runs. Where a component lowering rule places each object it emits (a same-name
sibling member, a trait on one member, or a separately named kind component), and
how a trait builds the same object as its kind twin, is set out in
`docs/oam/design-lowering-engine.md`, section "Where a lowering rule places each
object".
The lowering engine (`lowering.go`, `lowering_raw.go`) is the shared fixpoint
that performs that expansion, reachable from two entry points:

| Entry point | Reachable rules | Use when |
|---|---|---|
| `(*Transformer).lower`, invoked from `Transform`/`TransformWithPolicy` | `DocumentLoweringRule`, `ComponentLoweringRule`, `TraitLoweringRule`, `PolicyLoweringRule` | The authored document's field set already fits `ApplicationSpec` (`Components`/`Policies`, nothing else), so `ParseWithExtraTypes` can decode it before the transform lowers it away. |
| `(*Transformer).LowerRaws` — standalone, called by the caller BEFORE its own parse fan-out | `RawDocumentLoweringRule` only, for round 0 only. Nothing it emits is sealed; every other rule runs later, in `Transform`. | The authored document is a whole higher-level kind carrying its OWN fields that don't fit `ApplicationSpec`, so it can never survive a strict `ParseWithExtraTypes` decode. `LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error)` rewrites only the inputs whose `(apiVersion, kind)` pair a registered `RawDocumentLoweringRule` claims — `SupportedAPIVersion` unless the rule implements the optional `RawDocumentAPIVersioner` hook (`RawDocumentAPIVersion() string` — a plain `APIVersion()` accessor does not opt a rule in), which a consumer that owns its own API group uses so its documents are claimed rather than silently passed through (everything else passes through byte-identical) — decoding each claimed input via that rule's own `DecodeDocument` and running its `LowerDocument` once. The emitted `Application` bytes then re-enter the caller's parse and `Transform` exactly as if authored: the in-transform rules run there, under that call's `MaxLoweringDepth` budget, and ClusterProfile capability rendering is merged there. Before claiming a document, `LowerRaws` validates its `metadata.name` (required, DNS-1123 subdomain) and `metadata.namespace` (DNS-1123 label, if set) itself — the in-transform path gets the same check from `ParseWithExtraTypes`, which a raw-entered document never goes through. |

**Raw-rule contract: a raw rule rewrites authored input; it does not lower.** Emit what a
person would author — for example an `expose` trait carrying only its `hostnames` — and
let `Transform` run the in-transform rules and merge capability rendering. A raw rule
that copies capability rendering (a `PlatformReserved` field such as `controllerType`)
into what it emits is writing platform-reserved values into authored input, and
`Transform` rejects them with `ErrPlatformReserved`. `ctx TransformContext` is still
passed to the raw rule, but `LowerRaws` no longer needs an evaluated `ClusterProfile`
itself, since no `CapabilityAware` in-transform rule runs inside it. Besides metadata,
`LowerRaws` checks duplicates and generated-name collisions across the call (pass-through
identities included), rule arity, component and policy schemas of each emitted document,
and that each emitted document's apiVersion is `SupportedAPIVersion` or the group its
rule claims. Emitted traits are not checked in `LowerRaws`. A component or policy is
checked on its own copy of its properties, so checking it cannot strip a reserved
`null` from a trait or another element that shares its map before `Transform` sees it
(go-kure/launcher#626). `Transform` does not
shape-check trait properties either (it enforces only their platform-reserved keys): like
any authored document, each parsed output document must go through
`ValidateAuthoredProperties`, after parameter substitution, before `Transform` (see its
paragraph under Property schemas below). Without it, a raw rule's `enablePDB: "true"` on
a trait whose schema declares a boolean builds cleanly and is silently dropped. Before
go-kure/launcher#357 `LowerRaws` schema-checked emitted traits itself; a caller that
relied on that must now add the `ValidateAuthoredProperties` call.

The returned bytes cannot say which rule produced them: the element origin is unexported
and does not survive serialization. A caller that records provenance calls
`LowerRawsWithSteps(raws, ctx) ([]json.RawMessage, []LoweringStep, error)` instead, which
returns the same documents plus one `LoweringStep` per claimed input, in input order: `Rule`
is the rule identity (`rawdocument/<apiVersion>/<kind>`, suffixed `@<version>` when the rule
declares `ContractMetadata().Version`), `From` the authored `metadata.name` and `To` the
`metadata.name` of each document it emitted. `From` is a name, not a full identity, so two
claimed inputs of one kind and name in different namespaces are told apart only by their
position. A pass-through input has no step; on error the steps are nil and the
`LoweringError`'s `Chain` reports the failing document's steps. `LowerRaws` is
`LowerRawsWithSteps` with the steps dropped.

`NameAllocator`'s generated-name collision detection (D2) is scoped by `Origin.Namespace`,
not by name alone: two documents authored in different namespaces may generate the same
child name without colliding, since they lower to namespace-disjoint resources — the same
reason two identically-named Kubernetes objects in different namespaces coexist. Two raw
inputs sharing both a name and a namespace are rejected earlier, as a duplicate authored
document, before either reaches the allocator.

Every rule receives the run's one shared allocator as `LoweringContext.Namer`, which the
engine never leaves nil at any position (raw documents included), so a rule derives every
generated child name through `lctx.Namer.Name(base, suffix, origin)` with no nil-guard
fallback. A nil `Namer` is a contract violation by whoever built the `LoweringContext`.
A name that has a name role is resolved with `lctx.ResolveName(base, suffix, spec)` instead
(see "Name roles and the `Naming` hook"), which reserves no component name: the rule
reserves the one it emits a component under with `lctx.Namer.Reserve`.
Code that drives a rule directly, outside the engine — a rule's own unit test in another
module, a pre-pass, a golden-file or fixture harness — builds the `Namer` with
`NewNameAllocator()` (the zero value is not usable) and shares that one allocator across
every call belonging to the same run, so collisions between those calls are detected.
`LowerRaws` shares one allocator across its raw inputs, but only for round-0 raw-rule
claims; in-transform rules run inside each document's own `Transform` with a fresh
allocator, so a name an in-transform rule generates is never compared across documents.
A caller that transforms several documents detects that collision with
`CheckCrossDocumentCollisions`: it takes each document's identity and generated objects
(a `GeneratedDocument` per document, including any objects a layout walk adds) and reports
every object — keyed by API group, kind, namespace and name — that more than one document
generates, once per object, naming every document that generates it. Being keyed on the objects rather than on allocator claims,
it also catches two same-named authored components in one namespace, and a cluster-scoped
object generated from documents in different namespaces. An object's namespace is read from
the object, so pass objects as generated; an object with no kind is an error.

Nothing above compares the applications inside one document: a component and another
component's trait, two components, or two traits can each generate the same object, and
both pass `Transform` (go-kure/launcher#646). That includes one component's own: two of
its traits, or a trait and an application the component generates itself (a `helm`
component's values ConfigMap), rendering one object (go-kure/launcher#757). `Transform` alone refuses only those whose
names it resolves under a name role (see "Name roles and the `Naming` hook": two `scaler`,
two `rbac` or two `networkpolicy` traits naming one object, on one component or on two), so a
library caller must run both steps below on every transformed document, as `kurel build`
does. After transforming a document, generate it
with `GenerateApplications(cluster)` and pass the result to `CheckInDocumentCollisions`. It
returns each application's objects with its producer, generating each application once, in
the order kure's own generation uses and with the bundle labels and annotations kure adds,
so its objects replace a `Bundle.Generate` of the same cluster rather than add a second
generation, which could differ from the first. The check reports every object, keyed as
above, that more than one application generates, naming each producer as a component (a
sibling group is one) or as a trait's sub-application and its component. Producers that
read alike are named once with their count, since the cluster cannot tell them apart: two
traits of one component whose sub-applications share a name read as `2 sub-applications
"dup" of component "web"`, a trait named after its own component and that component as
`2 applications "web" of component "web"` (go-kure/launcher#757). A repeat within one application is not reported. `kurel build` runs both before it writes anything.

A PersistentVolume or PersistentVolumeClaim that is force-applied, or covered by the
`ForceReplace` delivery intent, is warned about, not refused (go-kure/launcher#720): when an
update changes an immutable field of a force-applied object, Flux deletes and recreates it
instead of failing the apply, which can lose a claim's data. Pass the same
`GenerateApplications` result, after `CheckInDocumentCollisions`, to
`Transformer.WarnForcedVolumes`. It emits one warning through the warning handler
(`SetWarningHandler`) per PersistentVolume and PersistentVolumeClaim that carries
`kustomize.toolkit.fluxcd.io/force: enabled` itself (an author's own, in a `manifests`
component for one) or whose application is `GeneratedApplication.Forced`. An application is
forced when it carries the `ForceReplace` delivery intent (the `force-replace` trait sets
it, go-kure/launcher#782) or when its bundle sets `Force` (launcher never does, so only a
bundle whose `Force` the caller set before generating, go-kure/launcher#781). The warning
names the kind, `namespace/name`, the producer and every reason the object is forced — the
annotation, the intent, the bundle, in that order:
`PersistentVolumeClaim shop/data (component "db") is force-applied
(kustomize.toolkit.fluxcd.io/force: enabled; its application sets the force-replace
delivery intent): when an update changes an immutable field, Flux deletes and recreates it
instead of failing the apply, which can lose its data`.
An object whose only reason is the intent is forced by nothing in the generated objects, so
its warning is conditional on the workflow that delivers them:
`PersistentVolumeClaim shop/data (component "db") is covered by the force-replace delivery
intent of its application: where the delivery workflow maps that intent (kure's Flux layout
integration writes kustomize.toolkit.fluxcd.io/force: enabled), an update that changes an
immutable field deletes and recreates it instead of failing the apply, which can lose its
data`. `kurel build` output carries no mapping of the intent, so there the warning describes
what a consumer's delivery workflow would do, not what the output does.
An object counts as annotated as Flux's force selector matches it: the force key as a label
or an annotation, with `enabled` in any letter case. Objects are read as Flux applies them:
a list envelope still in the output stands for its members — Kustomize's build expands a
kind ending in `List` whose `items` is an array, recursively, then Flux expands any
remaining object whose `items` is an array, one level only — and a member is forced by its
own metadata, not the envelope's. An object generated more than once is warned once,
naming its first producer and every reason any copy is forced. Objects are read as
generated: what a consumer's delivery changes afterwards (patches, post-build substitution)
and anything the cluster changes on apply are not modelled. It
covers every generated claim alike — a `webservice`/`worker` `volumes` entry (a
synthesized `pvc` trait, so named as a sub-application of its component,
go-kure/launcher#702), the `pvc` trait, the `persistentvolumeclaim` component, a `manifests` component's objects — and changes no
output. A `volumes` entry with `claimName` and the `volsync` trait's `sourcePVC` generate no
claim: an existing claim is warned where it is generated. With no warning handler it
does nothing. An embedder that does not
call it gets no warning. Objects a layout augmenter adds outside `Generate` are not in the
inventory and are not checked (`kurel build` refuses an augmenter whose `Generate` does not
cover them; see `LayoutAugmentationCoverage`).

`Reserve`/`Name` fail on every repeat claim of a name, including one from the same content.
Rules whose outputs share one derived object (two components pointing at the same chart
source, say) use the emit-or-adopt pair instead:
- `EmitOrAdopt(name, identity, origin)` claims a name for an element whose content is fully
  determined by `identity`, a string the rule builds from every input that shapes that
  element.
- `NameOrAdopt(base, suffix, identity, origin)` is the same claim through `Name`'s
  `<base>-<suffix>` construction, shortening ([Names and overrides](#names-and-overrides)) and
  DNS-1123 check.

The first claim returns `adopted=false`, and the rule emits the element. A later claim with
the same identity, from any element of the same authored document and in any round, returns
`adopted=true`, and the rule must not emit the element again. Same-round sibling components
cannot see each other's output, so this is how they share one object instead of colliding. A
different identity at the same name, the same identity claimed from another authored document
(each settled document is transformed on its own, so it would lack the adopted element), an
empty identity, and a name already held by `Reserve` stay hard errors, and `Reserve` still
refuses a name claimed this way. The cross-document refusal fires only where one allocator
spans documents (`LowerRaws`' raw rules, or a caller's own shared allocator); within
`Transform` every claim comes from one document. Errors name an identity only by a short
SHA-256 digest, never its text, so an identity may include sensitive inputs.

Three caller constraints the allocator does not check. The shared element must be of a terminal
type, one no lowering rule claims: claims outlive the round, so a lowerable element could be
replaced under another name while a later adopter still references the claimed one. Adoption
covers only the shared element: the adopting rule still emits its own output, because the
engine rejects an empty `LoweringResult` as a deletion, so a rule whose whole expansion is the
shared element cannot use this pair. And the document check sees only the authored document:
when a document rule fans one authored document out into several documents, their elements
share that origin, so adoption must not be relied on across them.

Four registration interfaces, one per position in the document tree, each with its own
registrar on `*Transformer` and a duplicate/dispatchable-collision guard (a type
claimed by a lowering rule must not also be a dispatchable handler type, and a
document kind must not be claimed by both document registrars):

| Interface | Registrar | Position | May emit |
|---|---|---|---|
| `DocumentLoweringRule` | `RegisterDocumentLowering` | whole document (`ApplicationSpec`-shaped only) | `Documents` |
| `RawDocumentLoweringRule` | `RegisterRawDocumentLowering` | whole document (any shape; own `DecodeDocument`) | `Documents` |
| `ComponentLoweringRule` | `RegisterComponentLowering` | `spec.components[]` | `Components`, `Policies` |
| `TraitLoweringRule` | `RegisterTraitLowering` | `spec.components[].traits[]` | `Traits`, `Components`, `Policies` |
| `PolicyLoweringRule` | `RegisterPolicyLowering` | `spec.policies[]` | `Policies` |

Every rule returns a `LoweringResult`; an entirely empty result is rejected — a
registered rule that emits nothing is indistinguishable from deleting the authored
element, which is not permitted. `Transformer.LowerableTypes()` reports every
kind/component-type/trait-type/policy-type claimed by rules registered on a
transformer (excluding raw-only rules, which are reachable only via `LowerRaws`), for
a caller to pass into `ParseWithExtraTypes` ahead of a transform that will lower them
(see Parsing above).

**Same-name sibling groups.** A `ComponentLoweringRule` may emit several components
under one name, each of a distinct type that has a component handler (no lowering
rule claims it). They form one sibling group, which deploys as a single component.
Each member keeps its own config, policy defaults, traits and objects. The group has
one tier, one bundle, one `dependency` node and one layout directory. Its application
generates each member's
first object in emission order, then every member's remaining objects in the same
order, as a single component generating all of them orders them. `webservice`
lowers to a deployment, a service and a serviceaccount member, which give
Deployment, Service, ServiceAccount, then the claims of the deployment member's
synthesized `pvc` traits (go-kure/launcher#702). It answers every config contract the transform reads
(Service port and port name, backend Service name, routing target, ServiceAccount,
single-pod claim) from the one member that has a value, gives the Flux namespace
to every member that takes one, and reports the group's name as its component
name (`ComponentNamed`). When a member's routing target selects another member's
pods (its non-empty `matchLabels` are a subset of that member's pod template labels,
with no `matchExpressions`) and every port of the routing member targets its own
port number, whatever its protocol, the traffic lands on the group's own pods, on
the ports it was routed to. The synthesized inbound policy then keeps the group's
component label as its pod selector, as one component deploying both would, while
its ports still go through the routing target: a routed Service port name becomes
its number and a non-TCP port is dropped. A member that remaps a port or names a
`targetPort` gets the Service `selector` policy, so it opens the port the pods
listen on. A member reports its pod template labels through an optional method on
its config, `PodTemplateLabels() map[string]string`; a member without it counts as
running no pods. Of the built-in pod kinds only `deployment` has it, the one pod
kind a lowering rule emits into a group (go-kure/launcher#794, item 3). A rule that
emits another pod kind into a group must add the method to that kind's config, or
a routing member selecting its pods gets the Service `selector` policy.

Traits run per member, against that member's own config: the rule decides which
member carries each trait. A routing trait (ingress, Gateway API routes) belongs on
the member that owns the Service; a workload trait on the workload member. The
group applies its members' traits in authored order, not member by member: the
rule's own traits first, in member order, then the traits it forwarded, by the
slot each held among the authored traits (a forwarded trait a trait rule lowers
later keeps that slot). Trait sub-applications are therefore ordered as for one
component carrying the same traits. A rule whose members can land in different
groups owes more for a trait that configures a bundle rather than objects: one
copy per bundle, or a refusal where it cannot tell the groups
(`pkg/oam/builtin/components/README.md`, "Traits a lowering rule forwards").

The build refuses a group:
- whose members' tier annotations disagree (one of them carrying none included),
  unless a placement policy places the group (it is then in the placed tier);
- in which two members answer the same contract (a member that runs pods
  answers `ServiceAccountName` even with no name, since its pods run as the
  namespace's `default` account);
- that has a member needing layout-level resources;
- in which two members generate the same Kubernetes object (API group, kind,
  namespace and name), such as two Services both named after the group;
- in which traits on two members create the same sub-application (the same trait on
  both members derives one name, such as `web-rbac`, from the shared name). The names
  are compared after each sub-application's `ApplyPolicy` has run, so a policy that
  renames a sub-application onto another member's is refused, and the name it moved
  away from is free (go-kure/launcher#755). Two sub-applications that both still carry
  the name the `Naming` hook gave them are compared by their defaults instead: the hook
  may give two different sub-applications one name, and the same trait on both members
  is refused whatever the hook answers. A name is the hook's when the hook answered,
  also when its answer is the default. Several sub-applications of one name that one
  trait creates cannot be told apart: they are the hook's only when the hook named
  every one of them, and are then compared by all their defaults. A name the hook gave
  that meets, on another member, one it did not give (a trait's own, or one a policy
  renamed onto it) is refused by name (go-kure/launcher#787).

An authored duplicate name is still refused. So is a name repeated by different
rule invocations, or by a trait or document rule, including a copy of a member.

Expansion runs to a **fixpoint**: every round, every current document's non-terminal
kind, components, traits, and policies are lowered once via their registered rule (if
any); the loop repeats until a round changes nothing, bounded by `MaxLoweringDepth`
(9) — a rule that keeps re-emitting its own (or another registered) type fails the
build with the full expansion chain rather than looping forever. A transformer with no
lowering rules registered anywhere returns the input `*Application` unchanged (the
same pointer — no copy, no allocation); registering rules only on the raw entry point
leaves that guarantee intact for the in-transform path.

Every emitted element carries an `Origin` — the AUTHORED location it descends from,
stamped once and copied verbatim onto every element expanded from it at any depth —
so a `LoweringError` always leads with the YAML the user actually wrote, then the
synthesized cause, then the expansion chain (`LoweringStep`s) that produced it.

`Origin.Rule` is the one field on `Origin` that is NOT copied verbatim: it identifies
the lowering rule that most recently produced the element — `"<position>/<type>"`
(e.g. `"trait/expose"`), suffixed with `"@<version>"` when the rule also implements
`ContractDescriber` (see Contract metadata below) — and is re-derived at every hop, so
it always names the immediate producer rather than the first rule in a multi-hop
chain. `""` means the element was never itself the direct output of a lowering rule
(authored as-is, or carried through untouched).

A trait a rule builds is sealed: its properties are final, and no
`ClusterProfile` capability rendering is merged into it later. A trait a
`ComponentLoweringRule` or `DocumentLoweringRule` merely forwards stays an ordinary
authored trait (unsealed, still capability-processed). Forwarding covers returning
`comp.Traits` itself and returning unchanged copies of its elements inside a new
slice, for example to add one trait of its own next to the authored ones or, in a
document rule, to rebuild a component around the authored traits; a copy whose type
or properties map the rule replaced counts as built by the rule. A forwarded trait's
`Origin.Index` stays its authored slot even when the rule places its own trait ahead
of it, and a trait a document rule moves to another component keeps the component it
was authored on in its `Origin`. A `DocumentLoweringRule` is handed a copy of the document whose component and
trait slices are its own, so neither the rule nor the engine's stamping of what it
forwards writes through to the authored document; properties maps are shared, and a
rule still must not mutate them.

Sealing says nothing about whether the trait's content was checked. A component or
trait a rule emits is synthesized when its properties are the rule's own output from
checked input, so a `PlatformReserved` value the rule rendered from
`LoweringContext.Capability` is accepted rather than rejected as authored. That
holds only when the rule's input was checked before it ran: a
`ComponentLoweringRule` that declares a schema (`PropertySchemaProvider`) or whose
input component is itself synthesized, or a `TraitLoweringRule` that declares a
schema over an unsealed trait or whose input trait is itself synthesized. A trait
nested in an emitted component gets the same classification as that component. A
`DocumentLoweringRule`'s output is never synthesized: the rule
sees trait and policy properties and metadata too, which nothing checks before it
runs. Such a rule writes a reserved value it renders from `LoweringContext.Capability`
with `Component.RenderReserved(path, value)`, or `Trait.RenderReserved(path, value)`
for a trait it builds, which writes a deep copy of `value` at the dot-separated
object-key `path` in `Properties` (`"networkPolicy"`,
`"tls.secretName"`; array items cannot be addressed; an object along the path that
is missing or `null`, typed nil included, is created) and records another deep copy
of it as rendered, both keeping its Go types. Changing `value` afterwards changes
nothing in `Properties`, and one value rendered at two paths is two independent
copies, so emission validation normalizing it under one key leaves the other as
written. A reserved key is then accepted only while it
holds the recorded value at that same path: either that value itself, or what
emission validation makes of it under the key's own schema, compared Go type for Go
type and a floating-point number bit for bit. So the record survives validation's
normalization exactly where validation performs it (a `[]byte` or `[]int32` under a declared array becoming `[]any`, a named
integer under a declared integer becoming `int`), and nowhere else: below a key an
object leaves to `AdditionalProperties`, or under a schema with no `Type`, the
handler receives what the rule wrote, so a rendered `[]byte` there is not matched by
an authored list of the same integers. A value the rule copied into the key from a
trait, a policy, metadata or another component, or changed after recording it, is
refused like any authored one — including a number that only prints the same, such
as an authored `1.0000000000000001e+18` over a rendered `1000000000000000100`, or one
that only compares equal, such as an authored `-0.0` over a rendered `0.0`, which a
handler tells apart with `math.Signbit` (go-kure/launcher#612). `RenderReserved`
refuses a value that is or holds a `null`, NaN or ±Inf, any Go type other than strings, booleans, numbers, slices, arrays and
string-keyed maps, or a collection that contains itself. The record follows the component
through copies and later lowering rounds; a rule that rebuilds a component from its
fields, or a document that is serialized and parsed again (what `LowerRaws` returns),
leaves it behind. A trait works the same way against its own type's schema; its
record covers the trait's own properties, checked before `applyTraits` merges a
capability rendering in, so that merge exempts nothing. Output of any other rule whose
input was not checked stays authored too (`RenderReserved` works there the same way)
and is checked like anything a user wrote, so a schema-less rule cannot pass an
authored reserved value through. Its components and traits — those it emits at trait position and those
nested in the components it emits — are checked for reserved keys as they are
emitted, before emission validation strips an explicit `null`, so a reserved key
written as `null` is refused here exactly as when authored directly
(go-kure/launcher#609, go-kure/launcher#626). Emission validation of any rule's output
normalizes the element's own copy of its properties, never the map the rule emitted,
so validating an element whose type does not reserve a key cannot strip that key's
`null` from another element sharing the map (including one the rule forwarded), which
may lower into one that does reserve it a round later. A trait such a rule only forwarded
keeps its classification, as a forwarded component does. A sealed trait that is not
synthesized is checked as it stands: as it is emitted, and again before a
`TraitLoweringRule` that declares a schema runs over it and when its handler applies
it. A schema-less rule that copies an authored reserved value into a trait it emits,
or writes one there any way other than `Trait.RenderReserved`, is therefore rejected;
a value it renders from capabilities with `Trait.RenderReserved` is accepted while the
key still holds it. A
sealed trait that passes that check is still not checked input for what the rule
emits: its schema covers the trait's own reserved keys, not those of the components
or traits the rule builds, so that rule's output stays authored. A
component a `DocumentLoweringRule` forwards — the same element of the
`doc.Spec.Components` it was handed, not a copy — keeps the classification it arrived with:
forwarding neither makes it synthesized nor resets it to authored. What a user wrote is checked
before any rule can rewrite it: before a `ComponentLoweringRule` claims the
component, and for every component of a document before its `DocumentLoweringRule`
runs, so rebuilding a component by value does not launder an authored reserved
value. Every component and every trait no rule synthesized is also checked at the
start of each lowering round, before any rule of that round runs, so a rule that
emits an element sharing a component's or trait's properties map cannot have
emission validation strip an authored `null` from it.

**Post-policy steps.** Lowering runs before the environment policy, so a rule
cannot write a value it derives from what the policy decided. It attaches a
`PostPolicyStep` to the component it emits instead, with
`Component.AfterPolicy(step)`. The transform runs the steps on the config the
component's handler built, in the order attached, right after
`Enforceable.ApplyPolicy` and before any trait of the component; an error fails the
transform, naming the component. A step that holds the config to the policy refuses as
`ApplyPolicy` does: an error that holds a `PolicyRefusal` fails the transform as the
component's `ViolationError` with the refusal's class, any other error as a
`TransformError` (go-kure/launcher#849). The transform does not apply the policy again
after a step. A kind may hold its own config to the policy once more when it generates,
as `passthrough` does, but no kind has to: holding what the step writes to the policy is
the rule author's responsibility. A step is part of the component value: it survives
copies and later lowering rounds, including a trait rule rewriting the component's
traits, but not serialization, and a document cannot author one. A
`ComponentLoweringRule` that lowers a component carrying a step carries it over only
by copying that component; a component it builds anew has no step, and the engine
cannot tell one was dropped. The built-in user is the `postgresql` rule, which
attaches a step to the `cnpg-cluster` it emits to set the values postgresql derives
after the policy (`CnpgClusterConfig.ApplyPostgresqlDefaults`; go-kure/launcher#281,
go-kure/launcher#729).

**Order between a rule's own components.** A rule that emits components of which one
must be applied before another declares it on the later one:
`Component.OrderAfter(names...)` (go-kure/launcher#783). Each name is a component of the
document once lowering has settled, usually one the same rule emits or adopts
(`LoweringContext.ResolveSharedName`); an unknown name, or the component's own, fails the
transform, naming the component and the rule's order. Like a post-policy step it is
part of the component value, survives copies, is not serialized, and cannot be
authored: an author orders components with a `dependency` policy. It survives further
lowering too: when a component rule lowers a component that carries an order, the
engine gives that order to the components the rule emits for it, whatever the rule
built them from, so what the component becomes waits as it did. One kind of component
is left out: a generated source, as "Pipeline" defines it (the rule orders another of
those components after it and orders it after nothing; it is not a member of a
same-name sibling group). It does not wait with
the component: it is the application's, shared by every component that names the same
source, and stays among the application bundle's own applications. Any other source the
rule emits is a component like any other and takes the order. `OrderAfter` is one of
the three ordering declarations described under "Pipeline".
The built-in users are the `helm` rule, which orders the release after the source it
generates or adopts, and the `oci` rule, which does the same for each Kustomization of a
source several `oci` components share (go-kure/launcher#784). A component alone on its
artifact orders nothing: its source is a member of its same-name sibling group.

A source that several components share is always a component a rule emits once and the
others adopt. The engine has no other sharing mechanism: the `SourceDeduplicatable`
interface and the pass that let the first config of a shared source key emit it were
removed with go-kure/launcher#784.

A trait-position rule that implements `CapabilityAware` is enforced by the engine
exactly as `applyTraits` enforces it for a dispatchable `TraitHandler`: missing the
required `ClusterProfile` capability fails with `ErrMissingCapability`. A rule that
also implements `PropertySchemaProvider` has an authored value for one of its
platform-reserved properties rejected before capability rendering is merged into the
trait it receives. Independently of that — every lowering rule, whether or not it
declares its own schema — has each element it emits (component, trait, or policy)
validated against the TARGET handler's declared schema before the emitted element is
accepted into the next round (see Property schemas below).

## Property schemas

Handlers may implement `PropertySchemaProvider` (`PropertySchema() map[string]PropertySchema`)
to declare a constrained schema for their user-facing properties. `PropertySchema` is launcher's
single schema vocabulary — the same type also backs `kurel.yaml` parameters (`ParameterDecl`) and
`CapabilityDefinition` rendering properties. It has `Type` (string/integer/boolean/number/array/object),
`Types`, `Description`, `Required`, `Default`, `Enum`, nested `Properties`, `Items`, and `AdditionalProperties`
(default false; escape-hatch fields set it true). The rich fields (`Types`, `Enum`, `Properties`, `Items`,
`AdditionalProperties`) are meaningful only for handler properties: the two flat call sites (kurel
parameters, capability rendering) reject them at decode time, so unifying the type does not widen
their accepted behavior.

A kurel parameter's `Type` is `string`, `integer`, `boolean`, `array` or `object`
(go-kure/launcher#421). `ResolveParameters` replaces a whole-value `${name}` for an `array` or
`object` parameter with the value's YAML list or map. With no `items` or `properties` to declare,
the parameter checks only that shape (its default too, and a string default is refused); the
substituted value is checked by `ValidateAuthoredProperties` against the consuming handler's schema
like any authored property. Such a parameter cannot be embedded in a larger string, in the template
or in another parameter's string default.

`Types` is the union idiom (go-kure/launcher#383): a leaf that accepts more than one scalar type
lists them, and a value is accepted when any member's single `Type` accepts it — whatever order the
members are listed in — normalised exactly as the first member, in declared order, that accepts it
would (below). Members are two or more distinct scalar types (string, integer,
number, boolean); `Type` and `Types` are mutually exclusive. A schema that sets both, or a malformed
union, is a schema error reported as soon as a value reaches the leaf. Every Kubernetes
`intstr.IntOrString` leaf — the rolling-update `maxUnavailable`/`maxSurge` knobs, the networkpolicy
`port`, the `service` kind's `ports[].targetPort` — declares `integer`/`string`, and every `resource.Quantity` leaf (`cpu`, `memory`, a claim's
`storage`) declares `string`/`number`, because their parsers take a fractional number too. `Type`
stays empty on a union leaf, so a schema consumer that does not read `Types` sees an untyped leaf and
keeps accepting every member. A completeness test (`pkg/cmd/kurel`) enforces that every built-in
schema node declares exactly one of `Type` and `Types`. `Transformer.HandlerSchemas()` returns a `HandlerSchemaSet{ Components, Traits, Policies }`
of every registered handler and lowering rule that declares one, so the downstream runtime's validator can check a
component's, trait's or policy's properties before the handler is invoked. Built-in examples: the `configmap` trait and the
`passthrough` component.

`Description` is optional (`json:"description,omitempty"`) but every built-in property populates it —
including nested object fields and array item schemas at every depth — so the downstream runtime can surface prose in
its generated Handler API Reference. A completeness test (`pkg/cmd/kurel`) enforces that no built-in
schema node is left without a description.

A schema field may also be marked `PlatformReserved`: its value may arrive only via
`ClusterProfile` capability rendering, never authored inline. `enforcePlatformReserved`
(`property_validate.go`) rejects an authored value for such a field — including an
explicit `null` — before capability rendering is merged in, wrapping
`ErrPlatformReserved`; it walks declared nested object fields and the items of a
declared array of objects too (named `properties.items[0]`), so a reservation on an
inner field is enforced wherever it is declared, not only at the top level
(go-kure/launcher#635).
`createApplications` and `applyTraits` (`transform.go`) run this check on the authored
path, and the lowering engine runs it on a `TraitLoweringRule`'s input trait before
capability rendering is resolved into it (see Lowering above). A value a lowering rule
recorded with `Component.RenderReserved` or `Trait.RenderReserved` is exempt while the
key still holds it (see Lowering above).

Separately, `validateProperties` (`property_validate.go`) checks an EMITTED
component/trait/policy's properties against its TARGET handler's declared schema —
enforcing `Required`, `Type`/`Types`, `Enum`, and nested `Properties`/`Items`/
`AdditionalProperties` — immediately after a lowering rule returns it, so a rule
cannot silently produce properties its own target handler would reject.

Both this check and the authored one below write a normalised value back where the
handler's reader would not accept what was supplied: a typed Go collection becomes
`[]any`/`map[string]any`; a `string`, `boolean` or `number` value of a named Go type
(`type Mode string`) becomes its predeclared type; and an `integer` value of kind
`int8`, `int16` or any unsigned kind, or of any named integer type
(`type Replicas int32`), becomes `int` — a value outside the `int` range is rejected
instead. Values already of type `int`, `int32`, `int64` or `float64` are left as they
are, because some readers accept `int` but not `int64`; a named integer type becomes
`int` rather than its underlying type for the same reason. A `Types` union leaf is
rewritten by whichever member the value matched. A property whose schema declares
neither `Type` nor `Types`, or a key an open object leaves undeclared, is not rewritten.

`ValidateAuthoredProperties` (`property_validate_authored.go`) is that check's
authored-path counterpart, and closes go-kure/launcher#408. Parsing is strict
(`KnownFields(true)`) only down to the envelope: `Component.Properties`,
`Trait.Properties` and `ApplicationPolicy.Properties` are each `map[string]any`, so
before this every handler read the keys it knew and silently dropped the rest — a
misspelled or stale property built successfully and produced nothing. The check walks
each authored component and its traits in document order, looks up the same schema
`validateProperties` would (terminal handler first, then a `ComponentLoweringRule` /
`TraitLoweringRule` claiming the type), rejects any key the schema does not declare,
and checks the shape of every key it does. `kurel build` calls it immediately after
parsing — and, in package mode, necessarily *after* `ResolveParameters`, because a
`${...}` placeholder is a bare string until substituted.

Before any of that, it runs `Transform`'s own reservation check
(`enforcePlatformReserved`) over the document, with the same schemas and the same
error text `Transform` would report for it. It has to come first: validation drops an
explicit `null` under a nested declared object, so a reserved key authored as
`config: {locked: null}` would otherwise be gone before `Transform` could refuse it
(go-kure/launcher#635).

The shape check is also the only place an authored scalar's *type* is enforced for
every property (go-kure/launcher#325). Many built-in handlers read string properties
with a comma-ok assertion (`cfg.Delivery, _ = props["delivery"].(string)`), so on
the handler alone a `delivery: 123` becomes `""` and is defaulted as though it were
absent. Type-checking once here, against the schema each handler already declares,
is what rejects it — not a per-field check in each handler. A caller that drives
`Transform` directly must therefore call `ValidateAuthoredPropertiesWithCapabilities`
first (after any parameter substitution, with the bindings `EvaluateProfile`
returned) to get the same guarantee `kurel build` gives. `ValidateAuthoredProperties`
is the same check for a caller with no profile.

Authored policies are checked the same way, after every component, in document order:
against the `PolicyHandler` registered for the type, else the `PolicyLoweringRule`
claiming it. The built-in policy handlers each declare a `PropertySchema`, so a
misspelt `placement` key such as `teir` is a build error rather than a
setting the handler never reads.

Three positions are exempt, each because there is no schema to check against: a
component or trait type no handler and no lowering rule claims (rejected separately
by the type allowlists and by `validateSettled`); a custom trait type from a
`CapabilityDefinition`, which declares that the type *exists* but not what properties
it accepts; and a policy type nothing is registered for, which the transform rejects
with `no handler for policy type`. Top-level `Required` is also deliberately not enforced
here — `ClusterProfile` capability rendering merges into a trait's top-level property
map after this runs, so a required property the platform supplies is legitimately
absent from what the author wrote.

Nested `Required`, inside an object the author did write, is enforced
(go-kure/launcher#765):

- `ValidateAuthoredProperties` knows no profile, so it checks nested `Required` as
  written, on every trait.
- `ValidateAuthoredPropertiesWithCapabilities(app, capabilities)` does the same for a
  trait no binding matches. For a trait a binding matches, it checks the trait's
  properties as the rendering merges into them, recursively
  (go-kure/launcher#750), so a required key the rendering supplies is not refused:
  a partial override such as `issuerRef: {kind: Issuer}` over a rendered
  `issuerRef: {name: ca}` is accepted. A missing key is then reported as `"name" is
  required; neither the trait nor capability "certificate"'s rendering sets it`. A
  required key inside an array element is always checked as written, because a
  rendering never merges into a list.
- `Transform` makes the same check itself on every trait's merged properties, at both
  merge sites (trait dispatch and the lowering fixpoint), whether or not a binding
  matched. This is new for a caller that runs `Transform` without the validator: a
  nested required key missing from both the trait and the rendering used to reach the
  handler and is now refused. `kurel build` already ran the validator, so its output
  is unchanged.

Required is enforced only inside an object that is present after the merge; an
object property left out entirely is not refused, whatever its own `Required` flag
says, as at the top level.

One property is legal on **every** trait regardless of what its handler declares:
`scope`. It is read by the transform engine rather than by a handler —
`buildCapabilityKey` builds the `"<traitType>.<scope>"` capability key for every trait
type, so a `pvc` or `certificate` trait can select a scoped `ClusterProfile` binding
even though neither handler declares such a property. `expose`, `ingress` and
`httproute` do declare it, but for an unrelated reason (sub-application naming), and
their own declaration wins over the engine default. Engine-owned keys are checked even
on a trait whose handler or lowering rule declares no `PropertySchema` at all (for
example a custom handler registered through `RegisterTrait`): a non-string `scope`
there is rejected, while the handler's own keys stay unchecked because nothing
declares them. A handler that refuses keys it does not read (for example
`topology-spread`, which takes none) asks `IsEngineTraitProperty` and lets these
through. It cannot tell an authored `scope` from one a capability rendering merged
in, and a rendered one selects nothing (the binding was already chosen), so such a
handler also refuses rendering keys in `ValidateAndApplyDefaults`, at
`EvaluateProfile`. Add to `engineTraitProperties` if
another engine-read property is ever introduced; the merge never mutates the
handler's returned schema, so `HandlerSchemas` still advertises only what each handler
actually declares.

`validateProperties`'s null check (`isNullValue`) treats a typed-nil pointer,
slice, or map — not just a bare `nil` interface — as `null`: a Go type assertion
alone can't tell an uninitialized slice/map apart from a validly-typed empty
collection, so a lowering rule that emits an unset (rather than empty) collection
field is still caught.

`IsNullValue` is the exported form of that check, and with it the contract itself:
**a null value is absent, not present-and-empty.** The predicate is nil-ness, not
serialization, and that distinction is load-bearing rather than pedantic: whether
the two shapes serialize differently depends on the ENCODER. `encoding/json` (and
`sigs.k8s.io/yaml`, which routes through it) writes `null` for a nil map or slice
and `{}`/`[]` for an allocated empty one, but `gopkg.in/yaml.v3` — the YAML library
this package parses with — renders both as `{}`/`[]`, because its encoder
dispatches on `reflect.Kind`. Keying the contract on nil-ness rather than on
rendered output is what makes it hold under either. The predicate parts company
with every encoder only on values no document round-trips (a nil channel is
reported null though `encoding/json` cannot marshal it at all).

It exists for parsers outside this package — handler and trait property readers,
and out-of-tree lowering rules — that must classify a null the same way the
validator does. Use it rather than `value == nil`: a typed nil is a non-nil
interface holding a nil value, so `== nil` is false and a `.(map[string]any)`
assertion on it succeeds with `ok=true` and a nil map, making the key read as an
authored empty collection. Where empty and absent mean different things that
difference is a behaviour change, not a cosmetic one. The standing example is a
`metav1.LabelSelector` in a `NetworkPolicy` peer: an empty one matches everything,
while a nil one applies no constraint on that axis — which is *not* the same as
matching nothing, since what the peer then selects depends on the sibling fields it
still has (`k8s.io/api` `networking/v1/types.go:199-222`).

`IntegerValue` is the matching reader for integers. It returns a property value as
an `int64` when it is a whole number of any Go integer kind, named or not, or a
finite integral float. It refuses a fraction, NaN/±Inf, a non-number, an unsigned
value above `math.MaxInt64`, and a float outside −2^63 ≤ value < 2^63 (−2^63 itself
fits `int64` and reads; 2^63 does not). The builtin handlers
read every integer property through it, and each then checks the result against its
own target (a port is 1–65535, a replica count fits `int32`), refusing a value that
does not fit rather than converting it. So a plain `int32` or `int64` from a lowering
rule or a Go caller reads the same as the `int` the YAML decoder produces, and a
value that would only fit after wrapping, like 2^32+80 for a port, is an error
instead of port 80. An out-of-tree handler should read integers through it for the
same reason. `IntegerInRange(value, lo, hi)` adds the target check and names the true
refusal reason, so an overflow like `1e20` never reads as a type error; a capability
`integer` property is checked with it too.

### What an explicit `null` means on the emitted path

The contract, stated once because "null" has several readers here and aligning
them one at a time is what made this take six rounds:

> A value that serializes to JSON null is absent, at every depth, on every path;
> a null is never a member of any `Items` type, so a null array element is a type
> error; reservation is about the KEY being written.

Applied in two places, because a key and an array element are not the same kind
of thing:

- **A declared, optional object key holding a null is DELETED** before its value
  is checked. `Required` already classifies a null as absent, so materialising it
  as a present key contradicted the classification the file had already made — and
  a handler parser decides presence with a bare two-value map lookup, so it saw a
  key the validator had decided was not there.
- **A null ARRAY ELEMENT is rejected**, by an explicit guard rather than by the
  type check. An element cannot be absent — it is present by being in the list —
  so there is nothing to normalise it to, and dropping it would renumber its
  siblings under a schema that may constrain length and order. The guard is
  necessary rather than decorative: a *typed* nil (`map[string]any(nil)`,
  `[]any(nil)`, reachable from a rule written in Go) satisfies the plain type
  assertion the object/array coercers try first, and iterating the resulting empty
  collection rejects nothing, so the type check alone accepted it. An `Items`
  schema with no declared type checks nothing at all — and so does *no* `Items`
  schema, which is why the guard runs before the `Items` walk rather than inside
  it: declaring no element schema says nothing about the members, but it does not
  license the one member no `Items` type could ever have matched.
- **An `Enum` member holding a null where the value's null would be stripped or
  rejected is refused.** Members are compared against a value that has already been
  normalised, while the declared members are not, so such a member could never
  match anything that reaches the comparison. Refusing it names the schema defect
  instead of leaving an `Enum` that silently never matches.

  The member is walked alongside its schema, because normalisation does not reach
  everywhere: a key an object leaves to `AdditionalProperties: true`, anything inside
  an array element with no `Items` schema, and anything below a schema with no
  declared `Type` pass through as written. A value can hold a null there, so a member
  holding the same null matches it and is accepted — `{opaque: null}` against
  `Enum: [{opaque: null}]` on an open object. A member's null compares equal only to
  a null value, whatever either side's Go type, and never to an empty collection. The
  rule is one-sided: where only the *value* holds a typed nil under such a key and the
  member holds an empty `[]`/`{}`, the two still match, as they always have — an
  undeclared key is outside every normalisation rule, so this does not start
  distinguishing a typed nil from an empty collection there. A member that cannot match
  for another reason — the wrong shape for the schema's type, or a key a closed
  object refuses — is still refused for any null it holds, as before.

  Refused per *member*, not per schema type. Refusing every `Enum` declared on an
  array or object type is simpler to state and was the first shape of this rule,
  but it also refuses the null-free compound enums that match perfectly well —
  and `PropertySchema` is exported, so a handler outside this repo would have seen
  a schema this validator used to accept start failing for a reason that does not
  apply to it. No built-in schema declares an `Enum` on a non-scalar type today,
  but nothing asserts that as a rule and this check does not depend on it. What is
  asserted is the rule above: `TestBuiltinHandlerSchemaEnumMembersHoldNoNull`
  (`pkg/cmd/kurel`) walks every schema that ships for a member holding a null,
  because the runtime arm only fires once a document validates against the property
  carrying the `Enum`.

  That walk is exported as `CheckEnumMembersHoldNoNull(schema PropertySchema) error`,
  so a handler outside this package can assert the same of its own schemas. It
  reports every `Enum` member — on the schema and on every schema nested under its
  `Properties` (in sorted key order) or `Items` — that holds a null anywhere, named
  by schema path and member index, and returns `nil` when none does. It is
  deliberately **stricter** than the runtime arm: it does not read the schema around
  a member, so it also reports the matchable nulls described above (under an
  `AdditionalProperties` key, inside an array element with no `Items`, below a schema
  with no `Type`). A schema it passes is never refused by the runtime's null rule; one
  it fails may still validate. A member nesting past the validator's depth bound
  counts as holding a null, as it does at runtime.

Two things this deliberately does not do:

- **It makes no exception for `PlatformReserved` keys.** Reservation
  (`enforcePlatformReserved`) is a rule about what a user *wrote*. On the authored
  surface the two rules never meet — it runs upstream of any emission validation, and
  `ValidateAuthoredProperties` runs it before its own strip —
  so reservation keeps treating an explicit null as *present* while the strip
  treats one as absent. Exempting reserved keys here would not have preserved the
  authored rule; it would only have handed a reserved null to the type check,
  producing a loud rejection with the wrong reason. The **component** and **trait**
  surfaces keep the same separation: an authored component or trait is checked
  before any rule can rewrite it. One a rule emitted from checked input — whose
  properties are the ones the strip touches — is exempt from reservation; the output
  of a rule whose input was not checked stays authored, and its components and
  traits are checked as they are emitted, before the strip (see the rule-output
  contract above).
- **A key the schema does not declare is untouched**, including inside an object
  that sets `AdditionalProperties`. Nothing describes such a value, so nothing
  here can normalise it, and a null inside an opaque object still reaches the
  handler parser. This is the same horizon as validation itself.

Scope, because this contract is not settled repository-wide: `ValidateAuthoredProperties`
(above) *does* reach `validatePropertyValue` — it calls it directly for every
authored key, with no strip of its own first. That is why
`validatePropertyValue` carries its own top-level null check
(`if isNullValue(value) { return value, nil }`) rather than relying entirely on
`validateObjectProperties`'s strip: on the emitted path the strip runs first and
the check is usually moot, but on the authored path it is what makes an
explicit null under an optional field read as absence instead of a type error.
What the contract does *not* reach is any path that skips both validators
entirely — a component a lowering rule constructs directly in Go, or a
built-in parser's own field read (`pkg/oam/builtin/components`) — which is why
those parsers keep their explicit-null guards regardless. Aligning that
parser-level layer with this contract is a separate, lower-level concern,
tracked as `go-kure/launcher#394`, and is not closed by this.

Two surfaces used to be named here as sitting outside the contract; both are now
fixed, so the note is historical rather than a live caveat:

- **Capability rendering** (`checkCapabilityValueType`, `capability.go`) is still
  outside the contract by the scope sentence — it is a parallel implementation,
  not a call-through to `validatePropertyValue` — but it now agrees with the
  contract's own behaviour: a present null reads as absence, matching the handler
  surface, and the type switch has a `default` arm. See "Capability rendering:
  types and nulls" below for the current, authoritative description. Fixed as
  `go-kure/launcher#431`.
- **The networkpolicy peer parser** used to read a *typed* nil `namespaceSelector`
  differently from an untyped one. Fixed as `go-kure/launcher#430` — both parsers
  now route their presence check through `oam.IsNullValue`'s reflect-based
  classification, so a typed nil reads as omission on every path that reaches it,
  the same as an untyped one.

## Contract metadata

Handlers and lowering rules may implement `ContractDescriber` (`ContractMetadata()
ContractMetadata`) to declare registration metadata about the contract they
implement: `Family`, `Version`, `RequiredCapabilityKeys` (the `ClusterProfile`
capability keys an entity of this contract needs), `Deprecated`, and
`DeprecationMessage`. It is primarily a discovery/documentation surface — the engine
does not enforce any of these fields (`CapabilityAware.CapabilityRequired` is what the
engine actually enforces). Two fields are read. A lowering rule's `Version` composes
the `"@<version>"` suffix on `Origin.Rule` (see below). `Deprecated` turns every
authored component or trait of that type into one warning through the Transformer's
warning handler (`SetWarningHandler`), in document order: `component "<name>": type
<t> is deprecated[: <DeprecationMessage>]`, or `trait type <t>` for a trait. Both
`Transform` and `TransformWithPolicy` warn; `LowerRaws` does not, because its output
re-enters `Transform` as authored and warns there. The check reads the authored
document before lowering, so a component a rule synthesizes never warns. A
deprecation is never an error and never changes the output. Nothing else here is
read or enforced. Consumers otherwise: schema publication, artifact
provenance in a downstream consumer, and deprecation tooling. Metadata rides the
existing registration mechanism; there is no separate contract registry.

`Transformer.HandlerContracts()` returns a `HandlerContractSet{ Components, Traits,
Policies }` of every registered component, trait and policy handler, and every
component, trait and policy lowering rule, that implements `ContractDescriber` — the
same six registries `HandlerSchemas()` covers (componentHandlers, traitHandlers,
policyHandlers and the three lowering-rule registries), for the identical reason: a
type reachable only through a lowering rule must still publish its metadata.
**Breaking library change** (go-kure/launcher#789): `HandlerContractSet` gained the
`Policies` field, so an unkeyed composite literal of it no longer compiles.

A lowering rule that implements `ContractDescriber` also has its `Version` folded
into the lowering-rule identity recorded on `Origin.Rule` (see Lowering above), e.g.
`"trait/expose@v1alpha1"`.

Every built-in handler and lowering rule implements `ContractDescriber`
(go-kure/launcher#789). The scheme: `Family` is the type name the built-in is
registered under (`webservice`, `expose`, `placement`), `Version` is
`builtin.ContractVersion` (`v1alpha1`, one value for all of them), and
`RequiredCapabilityKeys` is the type name for a built-in whose
`CapabilityAware.CapabilityRequired` returns `true` (`certificate`, `expose`) and
empty otherwise. No built-in is deprecated. A built-in rule's identity therefore
reads `component/webservice@v1alpha1`, on `Origin.Rule`, on `LoweringStep.Rule` and
in a `LoweringError` chain, where it read `component/webservice`.

### Lowering targets and `Seal`

A lowering rule emits elements other handlers render, so a registry that holds the
rule without them fails only when a document reaches the gap. A rule that
implements `LoweringTargetDeclarer` declares, in a `LoweringTargets{ ComponentTypes,
TraitTypes, PolicyTypes }`, every type it can emit over all of its inputs: the
components it emits, the traits it emits or attaches to a component it emits, and
the policies it emits. A type it only forwards from its input is not a target, and
document kinds are not listed.

`Transformer.Seal()` checks every registered rule that declares targets, of any
kind (document, raw document, component, trait, policy): each declared type must be
registered at its position, as a handler or as another lowering rule. The check
cannot run at registration, because a rule and the handlers of its targets are
registered in any order. `Transform` and `TransformWithPolicy` call `Seal` first, so
an incomplete registry is refused whether or not the document uses the rule;
`LowerRaws` does not, because it dispatches no handler. A caller that has finished
registering may call `Seal` itself to learn of a gap before it has a document. The
error names each rule and the type it is missing, sorted:

```text
registry incomplete: lowering rule component/webservice@v1alpha1 lowers into component type "service", which is not registered
```

`Seal` stores nothing and locks nothing: it reads the registry as it stands, may be
called any number of times, and accepts on the next call a type registered after a
refusal. A rule that does not implement `LoweringTargetDeclarer` is not checked, and
the engine does not check that a rule emits only what it declares.
**Breaking library change** (go-kure/launcher#789): a consumer that registers a
built-in lowering rule without the types it lowers into now fails at `Seal` or at
the first transform, where it failed only for a document that reached the missing
type. The built-in rules declare:

| Rule | Components | Traits | Policies |
|------|------------|--------|----------|
| `webservice` | `deployment`, `service`, `serviceaccount` | `topology-spread`, `pvc` | |
| `worker` | `deployment`, `serviceaccount` | `topology-spread`, `pvc` | |
| `helm` | `helmrelease`, `helmtemplate`, `helmrepository`, `ocirepository`, `gitrepository`, `bucket` | `configmap`, `secret` | |
| `postgresql` | `cnpg-cluster`, `cnpg-objectstore`, `cnpg-pooler`, `cnpg-database` | | `dependency`, `placement` |
| `expose` (trait) | | `ingress`, `httproute` | |

### The "no handler" error

A type with no registered handler fails the transform, and the message names where
the element is: `no handler for component type "x" (component "web")`, `no handler
for trait type "x" (on component "web")`, `no handler for policy type "x" (policy
"first")`. For an element a lowering rule emitted, it also names the rule and the
authored component it lowered:

```text
no handler for component type "service" (component "web", emitted by lowering rule component/role for component "web" (type "role") in document "app" (kind "Application"))
```

A trait the author wrote, forwarded by a rule onto a component the rule emitted under
another name, names that component and, through it, the rule and the authored
component: `no handler for trait type "x" (on component "web-rendered", itself emitted
by lowering rule component/role for component "web" (type "role") in document "app"
(kind "Application"))`. One a document rule moved to another component names the
component it was written on: `(on component "worker", authored on component "web" (type
"webservice") in document "app" (kind "Moving"))`. On the component the author wrote it
on, by name, the clause is left out.

The emitted form is reached only for a type the package knows and the registry holds no
handler for, and only for a rule that does not declare the type as a target (`Seal`
refuses a declared one first). A type the package does not know is refused earlier,
when the lowered document is validated.

## Policy defaults & enforcement

`Policy` is a typed accessor interface (no type assertions in handlers) that carries
per-environment **enforced limits** (`MaxReplicas`, `MaxCPU`, `MaxMemory`, `MaxStorageSize`,
`AllowedRegistries`), **defaults** (`DefaultReplicas`, the CPU/memory request/limit defaults,
and the workload-shape defaults `DefaultStorageSize`, `DefaultScalerMinReplicas`,
`DefaultScalerMaxReplicas`), security flags, and two distinct capability-constraint families:
`AllowedCapabilities`/`ForbiddenCapabilities`/`RequiredCapabilities` gate **OAM trait-type**
strings (e.g. "ingress", "autoscaling"), while `AllowedContainerCapabilities`/
`ForbiddenContainerCapabilities` gate Linux capabilities on a container's
`securityContext.capabilities.add` (e.g. "NET_ADMIN"). Both families share the same nil/empty
constraint-list convention under `NoopPolicy` — no `Allowed`/`Forbidden`/`Required` entries means
unconstrained — but only the boolean security flags (`AllowPrivileged`, `AllowHostPathVolumes`,
etc.) are default-deny; a container capability appearing in both an explicit `Allowed` list and
the `Forbidden` list is rejected, since forbidden always wins, and a nil/empty
`Allowed`/`Forbidden` list means no restriction/no forbids respectively. Handlers that implement
`Enforceable` receive it via `ApplyPolicy`; `NoopPolicy` supplies zero values when no policy is
set (so `ApplyPolicy` is always called with a non-nil value at runtime). A trait's
sub-application runs its `ApplyPolicy` after the trait's `Apply` and is held to the same rule
as the `TraitHandler`: it must not replace, remove or rename a component's application in the
bundle, nor rename a sibling group member's. The transform fails, naming the trait, its
component and the sub-application (go-kure/launcher#752).

**Optional policy interfaces.** A check that not every consumer needs is asked through an
interface a `Policy` may also implement, so adding one breaks no implementation.
`ExplicitSecretPolicy` (`AllowExplicitSecrets() bool`, go-kure/launcher#786) says whether a
document may carry secret values itself: the `secret` trait, the `secret` kind component
(go-kure/launcher#790), the `helm` component's
`secretValues` and the `helmtemplate` kind's, and a core Secret the `passthrough` or
`manifests` component carries (go-kure/launcher#794). `ExplicitSecretsAllowed(policy)` is how
a handler asks. **A policy that does not implement it allows them**, as does no policy at all: this is
the permissive side, so a consumer that must keep secrets out of documents has to implement the
interface and answer `false`. A refusal is a `ViolationError` naming the component, of class
`RefusalExplicitSecret` (below).

Handlers apply values with the precedence **authored > policy default > handler default**,
then enforce the limits on the resulting effective value — for cpu/memory this explicitly
includes the `pkg/oam/builtin/components` intrinsic handler-default tier (100m CPU / 128Mi
memory request, applied by `buildResourceRequirements` at `Generate()` time, after
`ApplyPolicy` runs), not just the authored/policy-defaulted value `ApplyPolicy` sees; see
that package's README, "Policy defaults & enforcement ordering".

For example the `scaler` trait fills
`minReplicas`/`maxReplicas` from the scaler defaults when omitted (erroring if neither the trait
nor a policy default supplies them), and the `pvc`/`postgresql` handlers default the storage
size from `DefaultStorageSize`. See the Policy Interface design note under the Concepts
section for the full accessor list and rationale.

### Refusal classes

Whatever an `ApplyPolicy` returns reaches the caller as a `*ViolationError` naming the
component, and so does a trait-type constraint of the policy (`ForbiddenCapabilities`,
`AllowedCapabilities`, `RequiredCapabilities`). Its `Class` says what the refusal is about, so
a consumer that reports a security refusal differently from a resource limit does not match
error text (go-kure/launcher#849):

```go
var v *oam.ViolationError
if errors.As(err, &v) {
	switch v.Class {
	case oam.RefusalHostNamespace, oam.RefusalPrivileged, oam.RefusalHostPath:
		// a security refusal of v.Component
	case oam.RefusalUnclassified:
		// not a refusal by the policy, or one that carries no class
	}
}
```

The classes the library gives are a closed set, the constants below and `RefusalClasses()`.
A `RefusalClass` is a string, and the value is what a consumer may log or store.

| Constant | Value | A refusal of |
|---|---|---|
| `RefusalHostNamespace` | `host-namespace` | `hostNetwork`, `hostPID` or `hostIPC` on a pod, under a policy that does not allow that namespace. |
| `RefusalPrivileged` | `privileged` | A privileged container, and a Windows HostProcess container or pod, under a policy that does not allow privileged workloads. |
| `RefusalHostPath` | `host-path` | A pod's `hostPath` volume, and a PersistentVolume's `hostPath` or `local` source, under a policy that does not allow hostPath volumes. |
| `RefusalContainerCapability` | `container-capability` | A Linux capability a container adds that the policy forbids or does not list as allowed. |
| `RefusalRegistry` | `registry` | A host outside `AllowedRegistries`, or one that cannot be held to that list: the registry of a container image or of an image volume's reference; the host a chart, a `manifests` `url` source or a Flux source is fetched from; an `oci://` url that does not name its registry; an Amazon S3 bucket endpoint, whose host Flux picks at runtime. |
| `RefusalResourceMaximum` | `resource-maximum` | A cpu or memory request or limit over `MaxCPU` or `MaxMemory`: on a container, on the pod, or on the pod template of an ACME HTTP01 solver of an `issuer` or `clusterissuer`. |
| `RefusalStorageMaximum` | `storage-maximum` | A storage request, or the capacity of a PersistentVolume, over `MaxStorageSize`. |
| `RefusalReplicaMaximum` | `replica-maximum` | A replica count, an autoscaler's `maxReplicas` or a database cluster's instance count over `MaxReplicas`. |
| `RefusalExplicitSecret` | `explicit-secret` | Secret material the document carries itself, under a policy that forbids explicit secrets (`ExplicitSecretPolicy`): a `secret` component or trait, a Secret that `passthrough` or a `manifests` source carries, `secretValues`, and a `certificate`'s keystore password. |
| `RefusalTraitCapability` | `trait-capability` | A trait type the policy forbids or does not list as allowed, and one it requires that the application does not use. |
| `RefusalUnreadableObject` | `unreadable-object` | An object written elsewhere (rendered by a chart, carried by `passthrough` or by a `manifests` source) that the build cannot read, so that it cannot be held to the policy and is refused instead of passed. |

The class is the same on every path a refusal comes from: a component's `ApplyPolicy`, a
post-policy step a lowering rule attached (the `1Gi` storage fallback of `postgresql` over
`MaxStorageSize`), a trait sub-application's, the check on the objects a chart renders under
template delivery,
and the `passthrough` and `manifests` checks, at the transform and again at generation. Of a
`manifests` `url` source the transform checks the host of the url; the objects it yields are
first known at generation, so a violation about one of them comes from `Generate`, not from
the transform. The text of no refusal changed with the class: it is the
`PolicyRefusal` at the end of the cause chain, whose `Error()` is the text the refusal had.

**Unclassified.** `RefusalUnclassified`, the empty string, is the class of a violation whose
cause is not a refusal by the policy. No class is guessed from text. It covers:

- an `ApplyPolicy` error of another kind: a chart that does not render, a policy default or
  maximum that does not parse, and the library's own rules on an object, which hold with or
  without a policy (no ephemeral containers, an image with no tag or digest, an undeclared
  field on a workload or a claim a chart renders);
- a refusal a consumer's own `Enforceable` returns as a plain error;
- a `ViolationError` literal that leaves `Class` out.

Not violations, and so without a class: `ErrPlatformReserved` and `ErrReservedMetadataKey`,
which stay the sentinels they were, and a `manifests` `url` source that cannot be fetched. One
fetch failure is a refusal by the policy all the same, a redirect to a host outside the allowed
registries: its error holds a `PolicyRefusal` of class `RefusalRegistry`, which `errors.As`
reaches, under no `ViolationError`.

**A consumer's own `Enforceable`** returns `NewPolicyRefusal(class, message)`, bare or wrapped
with `%w`, for its violation to carry the class: one of the constants above or a value of its
own, which the library passes on as given. The class of a violation is that of the first
`PolicyRefusal` in its cause chain. `NewViolationError(component, cause)` builds a violation
the same way, for a config that reports one itself at generation, as `passthrough` and
`manifests` do.

**Breaking** for an unkeyed `ViolationError` literal, which no longer compiles; a keyed
one is unaffected. And for a caller that matched `*TransformError` on a refusal by the
policy raised in a post-policy step: it is a `*ViolationError` now, with the same text.

## Capability system

Capability-aware traits (e.g. `expose`, `certificate`, `external-secret`) declare
required platform inputs; the `ClusterProfile` provides them, and
`CapabilityDefinition` rendering/property schemas validate custom capabilities
(`--strict-capabilities` turns warnings into errors).

### Capability rendering: types and nulls

A `CapabilityDefinition` rendering property uses the **flat** vocabulary —
`string`, `integer`, `boolean`, or **no type at all**, which accepts any value.
`LoadCapabilityDefinitions` rejects anything else at load time, and the value check
rejects it too, so a definition built in Go and installed with
`Transformer.SetCapabilityDefs` — which bypasses the loader — cannot silently accept
every value for a property by declaring a type outside that set.

An explicit `null` on this surface is **absence**, matching the contract the handler
surface follows, rather than failing as a type error; the null key does not survive
into the validated rendering. What happens next depends on `required` and `default`,
checked in that order — `required` always wins, even over a declared default:

- **required** — reported as required-and-missing. A declared default does not
  rescue it; requiredness is checked first.
- **optional, with a non-null default** — the declared default is substituted.
- **optional, with no default** (or `default:` with no value, which likewise
  declares **no default**, not a null one) — the key stays absent.

This holds for a typed nil as well as an authored one, which matters precisely
because `SetCapabilityDefs` takes Go-built definitions.

A **declared default is type-checked against its own declared type**, on the same
footing as a value the document supplies. Without that, a property was validated
when the *document* supplied the value and unvalidated when the *schema* did — so a
Go-built definition declaring `integer` with a `"three"` default injected that string
into the rendering, past the check that exists to keep it out. Files were never
exposed to this (`LoadCapabilityDefinitions` checks defaults at load); `SetCapabilityDefs`
was, being the same bypass the type check above guards. A property declaring **no
type** still accepts any default, unchanged.

### Rendering merge: authored values and nulls

A trait's matched capability rendering is merged under its properties: an authored
value replaces the rendered one at that key, and keys the trait does not author keep
the platform value. Where both hold an object, the merge recurses
(go-kure/launcher#750): authoring `resources: {limits: {cpu: 2}}` over a rendered
`resources: {limits: {cpu: 1, memory: 1Gi}}` gives `{cpu: 2, memory: 1Gi}`, and an
authored `{}` keeps the rendered object. A list, a value of another kind, and a
rendered object built in Go with another type (`map[string]string`) are replaced
whole. An authored `null` is absence here too, at any depth, so a `null` over a key
the rendering supplies takes the platform value. A `null` under a key the rendering
lacks is left as authored. **Pre-GA output changes**: the authored `null` used to
replace the rendered value, so the handler saw the key unset (go-kure/launcher#742);
an authored object used to replace the rendered object whole, dropping its sibling
keys (go-kure/launcher#750). This holds for every trait with a rendering, on both the
dispatch path and a trait lowering rule's input.

Authored validation checks nested `Required` on the merged properties too
(`ValidateAuthoredPropertiesWithCapabilities`, go-kure/launcher#765), so a partial
override that omits a required nested key the rendering supplies is accepted. The
profile-less `ValidateAuthoredProperties` still refuses it.

A component handler that implements `ComponentCapabilityDefaults` (see Transform &
extension) gets the same precedence for the properties it lists, and only those.
Each value it fills is then validated against the component's own `PropertySchema`
(go-kure/launcher#751): a value the trait handler's schema accepted can still be
refused here, with an error naming the component, the capability and the property
(`component "data": capability "pvc" defaults: properties.storageClassName: expected
string, got int`). A component with no schema relies on the trait side, and its
fill is refused when the component has no schema and no trait handler or trait
lowering rule for the capability's type validates the rendering, meaning it
implements `ValidateAndApplyDefaults`, or the type is not built in and has a
`CapabilityDefinition` applied (go-kure/launcher#772). A type with neither a handler
nor a rule, whose binding `EvaluateProfile` never checks, is refused there too. The
error names the component, the capability and the rendering keys. **Pre-GA input
change**: a fill that used to reach a component unvalidated is now refused when
nothing validates it, and a value the component's schema does not accept is refused.
No built-in component is affected: `persistentvolumeclaim`, the only built-in
`ComponentCapabilityDefaults` implementation, declares a schema.

Both merges copy the rendering value they hand out, keeping its Go type: a `3` the
profile decoded as `int` reaches the handler as `int`, and an `int64` above 2^53 keeps
its exact value (go-kure/launcher#756). The copy used to be a JSON round trip, which
turned every number into `float64`. A `ComponentCapabilityDefaults` fill is then
normalized by the component's schema check like any validated property: a
`map[string]string` under an object property reaches the handler as
`map[string]any`, and a `uint32` or named integer under an integer property as
`int`. The profile's own value is left as it was. A rendering value therefore obeys
the same rule as a value a lowering rule writes with `RenderReserved`: a string, boolean, finite
number, list or string-keyed object, nested to any depth. `TransformWithPolicy`
checks every binding in `TransformContext.Capabilities` before building anything,
whether or not a document uses it, and refuses the transform with an error naming
the capability key and the top-level rendering key
(`capability "pvc" rendering key "limits": …`). A `null` top-level value is not
refused: it reads as absent. **Pre-GA input change**: these renderings built before
and are now refused:

- a `null` below the top level (`limits: {cpu: null}`), which the round trip kept;
- `NaN` or `±Inf` (YAML `.nan`, `.inf`, `-.inf`), on which the round trip failed and
  the trait merge then handed out the profile's own value;
- in a Go-built `TransformContext.Capabilities`, any other Go type (a struct, a
  pointer, a channel, a function, a map whose keys are not strings): the round trip
  converted a struct or pointer to its JSON form, and failed on a channel or
  function, which the trait merge then handed out uncopied.

This is a large internal builder surface; the tables above cover the entry points.
See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the full
type reference, the design notes under the Concepts section, and `examples/` for
runnable applications.
