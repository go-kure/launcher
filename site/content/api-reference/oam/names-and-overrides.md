---
title: "Names and overrides"
weight: 3
---

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
| 63 | `ShortenLimitLabel` | The component label value, `ComponentLabelValue`. Launcher shortens no Flux Kustomization name: the default name of a `helmtemplate` hook-group child's and of a chart's own layout's is kure's, which kure shortens by its own rule ([Pipeline](pipeline.md#pipeline)). |
| 253 | `ShortenLimitSubdomain` | Object names: `NameAllocator.Name` and `NameOrAdopt`, and the default of `LoweringContext.ResolveName` and `ResolveSharedName` (the `postgresql` pooler and databases, the `helm` values ConfigMap and values Secret, a generated Flux source), the layout name, and so the directory, of a `helmtemplate` hook-group child (`<application>-<component>-<NN>-<phase>`; the `-<NN>-<phase>` suffix is kept whole; its Kustomization's default name is kure's), the claim a role component's `pvc` volume generates (`{comp}-{volume}`, each half hyphen-escaped), the synthesized NetworkPolicies (`{comp}-allow-ingress-traffic`, `{comp}-allow-egress-traffic`, `{comp}-allow-endpoint-ingress`), the `scaler` HPA and PDB, the `networkpolicy` trait's policy, the `ingress` Ingress and `httproute` HTTPRoute (`{comp}-ingress`, `{comp}-httproute`, each with an optional `-{scope}`), the managed TLS Secret default (`{comp}-tls`), the `volsync` ReplicationSource (`{sourcePVC}-backup`) and its default repository Secret name, and the bundle of an ordered group (`<application>-<tier>`, `<application>-<NN>`; the suffix is kept whole). |
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
| `bundle` | The application's bundle. | The Application's `metadata.name`. | none, by design: the author names the bundle with `metadata.name` | always |
| `group` | The bundle of one ordered group, a child of the application's. | `<application>-<tier>` or `<application>-<NN>`, shortened to 253. | none, by design: a group is derived from the order, and has no authored home | always |
| `sub-application` | An application launcher adds beside a component's own: each one a trait creates, and the one holding each synthesized NetworkPolicy. It is no object. | What the trait or the synthesis names it (`<component>-scaler`, `<component>-rbac`, the policy's default name, …). | none | always |
| `netpol-synth` | A synthesized NetworkPolicy. | `{owner}-allow-ingress-traffic`, `{comp}-allow-egress-traffic`, `{comp}-allow-endpoint-ingress`. | none, by design: a synthesized policy has no authored home | always |
| `hpa` | The `scaler` trait's HorizontalPodAutoscaler. | `<component>-hpa` | `hpaName` | unless `hpaName` is set |
| `pdb` | The PodDisruptionBudget of the `scaler` trait (with `enablePDB`) or of the `disruption` trait. | `<component>-pdb` | `pdbName` | unless `pdbName` is set |
| `tls-secret` | The Secret of the TLS entry the `expose` trait manages on its Ingress (the `ingress` trait's platform-reserved `managedTLS`). cert-manager writes it; the Ingress's `tls[].secretName` is launcher's one reference to it and carries the resolved name. Not an `ingress` trait's own authored `tls[]` entries, used as written. | `<component>-tls`, shortened to 253 with `-tls` kept whole. | `secretName`, on `expose` | unless the expose trait's `secretName` is set |
| `external-secret` | The Secret the `external-secret` trait's ExternalSecret produces. The ExternalSecret's `target.name`, and the `envFrom` and secret volume the trait adds to the workload, carry the resolved name; a reference the author wrote elsewhere does not follow it. | The trait's `secretName`, the ExternalSecret's own name. | `targetSecretName` | unless `targetSecretName` is set; not under `creationPolicy: Merge` or `None`, where the name refers to a Secret that exists apart and is used as written |
| `rbac` | Each object of the `rbac` trait, asked once per object: the Role and the RoleBinding, and with `clusterWide` the ClusterRole and the ClusterRoleBinding. | The component's name. | `name` (one for all of them) | unless `name` is set |
| `networkpolicy` | The `networkpolicy` trait's NetworkPolicy. | `<component>-allow` | `name` | unless `name` is set |
| `ingress` | The `ingress` trait's Ingress. It names the object alone: the trait's sub-application keeps `name`, else the default. Launcher writes no reference to the Ingress by name. | `<component>-ingress`, `<component>-ingress-<scope>` with a `scope`. | `name` | unless `name` is set |
| `httproute` | The `httproute` trait's HTTPRoute. It names the object alone: the trait's sub-application keeps `name`, else the default. Launcher writes no reference to the HTTPRoute by name. | `<component>-httproute`, `<component>-httproute-<scope>` with a `scope`. | `name` | unless `name` is set |
| `volsync-replicationsource` | The `volsync` trait's ReplicationSource. It names the object alone: the trait's sub-application keeps `name`, else the default. Launcher writes no reference to the ReplicationSource by name. | `<sourcePVC>-backup`, shortened to 253 with `-backup` kept whole. | `name` | unless `name` is set |
| `pooler` | The Pooler a `postgresql` component generates. | `<component>-pooler` | `poolerName` | unless `poolerName` is set |
| `database` | Each Database a `postgresql` component generates, asked once per `databases` entry. | `<component>-<database name>` | `databases[].objectName` | unless that entry's `objectName` is set |
| `object` | The one object of an authored kind component (`deployment`, `service`, `cnpg-cluster`, `helmrelease`, …). Not asked for a member a component or trait lowering rule emitted. The endpoint selector of `cnpg-cluster` and `cnpg-pooler`, which matches the pods the object owns, is built from this name; every other generated label and selector keeps the component name. | The component's name. | `objectName` | unless `objectName` is set |
| `helm-source` | The Flux source (HelmRepository, OCIRepository, GitRepository, Bucket) a `helm` component generates for an inline `source`, and the OCIRepository `oci` components of one artifact share. Not the source an `oci` component keeps to itself, which is the component's own (`oci-source`). | `<application>-source-<digest>`, the digest of the source's content. | `source.name`, beside an inline source | once per source, with no component; not for a source a component names with `source.name` |
| `values-configmap` | The ConfigMap a `helm` component generates under `valuesMode: configMap`. | `<component>-values-<hash>`, the hash of the stored values. | `valuesConfigMapName` | unless `valuesConfigMapName` is set |
| `values-secret` | The Secret a `helm` component generates for `secretValues`. | `<component>-secret-values-<hash>`, the hash of the stored values. | `valuesSecretName` | unless `valuesSecretName` is set |
| `helm-release` | The HelmRelease a `helm` component generates under `delivery: flux`. It names the object alone: the Helm release name (`releaseName`, `spec.releaseName`) and the default names of the values ConfigMap and Secret keep following the component name. | The component's name. | `helmReleaseName` | unless `helmReleaseName` is set; not under `delivery: template` |
| `oci-kustomization` | The Flux Kustomization an `oci` component generates, whether the component keeps its source or shares one. | The component's name. | `kustomizationName` | unless `kustomizationName` is set |
| `oci-source` | The OCIRepository an `oci` component keeps to itself: the one no other `oci` component of the document shares and no `source.name` names. | The component's name. | `source.objectName` | unless `source.objectName` is set; not for a shared source or one `source.name` names (`helm-source`) |
| `workload-deployment` | The Deployment a `webservice` or `worker` component generates. It names the object alone: the pod labels, the selectors and every name derived from the component keep the component name. | The component's name. | `deploymentObjectName` | unless `deploymentObjectName` is set |
| `workload-service` | The Service a `webservice` component generates; a DNS-1123 label (one starting with a digit is refused when `spec.kubernetesVersion` is below 1.36, RelaxedServiceNameValidation; `docs/delivery-scope.md` §3.5). Its name is its DNS name in the cluster, and launcher writes no such address (below). | The component's name. | `serviceObjectName` | unless `serviceObjectName` is set |
| `workload-serviceaccount` | The ServiceAccount a `webservice` or `worker` component generates for its pods. | The component's name. | `serviceAccountObjectName` | unless `serviceAccountObjectName` is set; not when `serviceAccountName` names an existing account, since the component then generates none |
| `workload-volume-claim` | The PersistentVolumeClaim a `pvc` volume of a `webservice` or `worker` component generates, asked for each such volume. The volume mounts the claim by this name. | `<component>-<volume>`, each half hyphen-escaped, shortened to 253 with `-<volume>` kept whole. | `volumes[].claimObjectName` | unless that volume's `claimObjectName` is set; not for a volume whose `claimName` references an existing claim, since it then generates none |
| `postgresql-cluster` | The Cluster a `postgresql` component generates; a DNS-1035 label of at most 50 characters. The Pooler's, each Database's and each ScheduledBackup's reference to the Cluster and the endpoint selector follow it; the default names of the Pooler, the Databases and the ScheduledBackups keep the component name. The operator derives the Cluster's Services and Secrets from this name (below). | The component's name. | `clusterObjectName` | unless `clusterObjectName` is set |
| `postgresql-objectstore` | The ObjectStore a `postgresql` component generates for `backup`. The Cluster's backup plugin names the store by it (`barmanObjectName`). | The component's name. | `backup.objectStoreObjectName` | unless `backup.objectStoreObjectName` is set; not without `backup`, since the component then generates none |
| `postgresql-scheduledbackup` | Each ScheduledBackup a `postgresql` component generates, asked once per `backup.schedules` entry, with the component. It names the Cluster by the name the Cluster took. Every name is held to 63 characters, the hook's and the default included: the operator copies it into a label of each Backup. | `<component>-<schedule name>` | none: the schedule's `name` is the suffix | once per `backup.schedules` entry |
| `postgresql-recovery-objectstore` | The ObjectStore a `postgresql` component generates for an inline `recovery.source.objectStore`, the store the new Cluster is recovered from, asked with the component. The Cluster's recovery source names the store by it (`barmanObjectName`). | `<component>-recovery-source` | none | unless `recovery.source.objectStoreName` names an existing store, since the component then generates none; not without `recovery` |
| `hook-group` | The prefix of the names of a `helmtemplate` component's hook-group layouts, each `<prefix>-<NN>-<phase>`: the directory of a group and, for a prefix the author or the hook set, its Flux Kustomization; with the default the Kustomization's name is kure's, `<unit name>-<layout name>` ([Pipeline](pipeline.md#pipeline)). It is no object, and the one role whose answer is a prefix and not a name: how many groups a chart has is known only once it is rendered, and the prefix is resolved before that. | `<application>-<component>` | `hookGroupNamePrefix`, on `helmtemplate` and on `helm` under `delivery: template` | once per `helmtemplate` component, unless `hookGroupNamePrefix` is set |
| `layout` | The Flux Kustomization kure generates under `FluxIntegratedPerLayout` placement for a chart's own layout: a `helmtemplate` component's, the only layout of its own a component has under the default `ApplicationGrouping`. It is no object launcher writes and names neither the layout nor its directory; under any other placement the layout has no Kustomization of its own, and kure's Flux integration refuses a name set on it (go-kure/kure#1032). | `<bundle>-<component>`, the bundle as launcher named it (the `bundle` or `group` role's answer): kure's own default, which launcher never sets and kure shortens past 63 characters by its own rule. | `layoutKustomizationName`, on `helmtemplate` and on `helm` under `delivery: template` | once per `helmtemplate` component, unless `layoutKustomizationName` is set |
| `generated` | An object a lowering rule or a trait handler generates that no other role names: the role for a rule or a handler outside launcher's own. No builtin rule or handler asks it. | The one the rule or the handler builds. | the rule's or the handler's own, if it has one | once per object, unless the author named it |

The last column says when the hook is asked for a name, not how many times. `once per X` names
the scope of the decision: the source, schedule entry, component or object whose name the hook
answers. The `hook-group` answer is the exception in kind. It is a prefix that a component's
layouts share, not a name of its own. A type that declares outputs is asked again by the output
check (see "Outputs: the names a component's objects get") for each name its provider
resolves, and the lowering resolves the same name a second time. So the hook can run more than
once for one object. A provider that reads an already resolved `Component.ObjectName()` adds
no call, as the `configmap` and `secret` components do. Other queries ask again too:
`ComponentEndpointsNamed` asks for a name again (below). The hook must be a pure function of
its request, as below, so every call gives the same answer.

The `bundle`, `group` and `netpol-synth` names are the hook's to change, and only the hook's.
The `bundle` and `group` defaults carry no namespace: two applications of one name, or whose
order gives the same group name, get the same bundle names wherever they are deployed. Keeping
them apart across applications is the consumer's job, through the `Naming` hook.

The `cilium-networkpolicy` trait's CiliumNetworkPolicy has no role: it is author-named, and
the hook is not asked, by design. Its `name` is required, and an authored name is never put to
the hook. The trait claims the name, so a second owner of it is refused as under any role. The
`configmap`, `secret` and `pvc` traits' objects have no role for the same reason, and are
claimed the same way.

The `volsync` trait's repository Secret has no role either: launcher generates no such Secret,
it only names it in the ReplicationSource (`spec.restic.repository`). The author sets that
reference with `repository`; its default is `<component>-volsync-secret`, and the Secret is the
consumer's to create under it.

The `generated` role is for a lowering rule or a trait handler outside launcher's own whose
object no other role names. Such a rule resolves the name with `LoweringContext.ResolveName`,
such a handler with `(*Trait).ResolveName`, both under `NameRoleGenerated` with the object's
kind; the name is then put to the hook and claimed as under any role, where without a role it
would be the handler's alone and compared only by `CheckInDocumentCollisions`. The request
carries the object's `Kind` and its component, and no field of its own: a hook tells two such
objects of one component and kind apart by their `Default`.

The `hook-group` prefix is resolved in the transform, where two components of one document
that resolve to the same prefix are refused: their groups would share names. The names are
built after the render. With the default prefix the layout name is shortened to 253
characters with its `-<NN>-<phase>` suffix whole, and the Kustomization's name is kure's,
`<unit name>-<layout name>` shortened past 63 by kure's rule ([Pipeline](pipeline.md#pipeline)), so the
two differ. A prefix the author or the hook set is used as written in both, and is never
shortened: a prefix that is no DNS-1123 subdomain is refused in the
transform, and so is a child name over 63 characters built from it, in an error that carries
the component, the role and the full name. On a `helm` component under `delivery: flux` the
property is refused: a HelmRelease installs the chart, no hook-group layout exists, and the
prefix would name nothing.

**The over-long name fails the transform** (go-kure/launcher#787). It used to fail only when
the layout was built, in `AugmentLayout`. The names exist once the chart is rendered, and the
transform has it rendered: it holds every config to the environment policy, the default
`NoopPolicy` when `TransformContext.Policy` is nil, and a `helmtemplate` config renders its
chart for that step. Right after it the transform asks the config for the refusal
(`HookGroupNameChecker`, an optional interface of a config that takes a prefix) and returns
it behind its own prefix, `component "db": …`. So every transform refuses the name, whether
or not it was given a policy, and a consumer that only calls `Generate`, which writes no
hook-group layout, is refused as well. `AugmentLayout` still refuses the same name with the
same error. That is the one a consumer meets outside a transform: on a `HelmTemplateConfig`
it built directly, and on a config whose `HookGroupNamePrefix` it set after the transform.
Neither place refuses a prefix that names nothing: a chart with at most one hook group has no
hook-group layout, and its prefix is not measured, whatever its length.

A caller tells the refusal from every other error without reading its text, from either
place: `errors.Is(err, oam.ErrHookGroupNameTooLong)` is true, and `errors.As` finds the
`*oam.HookGroupNameError`:

```go
var tooLong *oam.HookGroupNameError
if errors.As(err, &tooLong) {
	// tooLong.Component, tooLong.Role ("hook-group")
	// tooLong.Name, tooLong.Length, tooLong.Limit (63), tooLong.Prefix
}
```

`Name` is the longest of the component's hook-group names, so a prefix short enough for it
fits every group; `Length` counts it as the text does; `Prefix` is what the author or the
hook gave; `ComponentType` is `helmtemplate`, also for a `helm` component under
`delivery: template`, which is lowered to one. The text is unchanged. The other refusal of
a hook-group name, one that is no DNS-1123 subdomain, is not this error: a transform checks
the prefix before any name is built, so only a prefix no transform resolved reaches it.

**Limit: the transform holds the prefixes apart, not the names built from them.** Those
exist only after the render, and two different prefixes can still give one Kustomization
name. Two shapes:

- A written prefix equals kure's name for a default child, less its suffix. In bundle `shop`,
  kure names the post-install group of component `db` of application `shop`
  `shop-shop-db-02-post-install`, and a second component whose prefix is `shop-shop-db` names
  its own post-install group so.
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

The hook sees every role. It is asked at least once for each name the transform resolves,
and again by the output check of a type that declares outputs, for each name its provider
resolves (see Outputs, below; a provider that reads an already resolved `Component.ObjectName()`
causes no further call), and not at all for a name the author set. `NameRequest` carries the Application's name, the component
(empty for the bundle, a group, an external backend's policy and a generated source the
document's components share), the role, the object's kind
as `Kind` or `Kind.group` (empty for a role that names no object) and the default as launcher
would use it: already shortened where launcher shortens a name (a group's bundle, the `hpa`,
`pdb`, `networkpolicy` and `netpol-synth` objects), and as long as it is where it does not: a
trait's sub-application default (`<component>-scaler` is 260 characters for a 253-character
component name) and the `hook-group` prefix, which is shortened only inside the names built
from it. The default is what tells apart several names of one component and role. Returning
`false` keeps the default. Answers are not cached, and a name can be asked for more than once (as said above, and
again by `ComponentEndpointsNamed`, below): the hook must be a pure
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
`helm-release`, `oci-kustomization`, `oci-source`, `workload-deployment`, `workload-service`,
`workload-serviceaccount`, `workload-volume-claim`, `postgresql-cluster`, `postgresql-objectstore`,
`postgresql-scheduledbackup`, `postgresql-recovery-objectstore`) carries the name of the document the rule is lowering. A component, trait or policy rule runs only once the document's kind is final, so
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

`GeneratedName(base, suffix)` returns that default: `<base>-<suffix>`, shortened to a
DNS-1123 subdomain by the one rule for generated names (`ShortenNameWithSuffix`), or the
error `ResolveName` gives where it cannot be built. It is the name an object takes where
no name is authored and no hook is set (`HasNamingHook` false), so a rule can tell the name
another component's lowering gives such an object without lowering it: the `postgresql`
rule matches another component's inline recovery store by it (go-kure/launcher#1044).

`LoweringContext.ResolveMemberName(member, spec)` names the one object of a kind component
the rule is about to emit under the component's name, where the rule lets the author and the
hook choose that name (the HelmRelease of a `helm` component, the Kustomization and the kept
OCIRepository of an `oci` component, the Deployment, the Service and the ServiceAccount of a
`webservice` or `worker` component).
The order is the same, the default being the member's component name, used as written. The
name is resolved, recorded for the transform to claim and set on the member in the one call,
and a rule has no other way to give a member's object a name of its own, so no such name
goes unclaimed. The member keeps its component name, and with it its sibling group, its
place in the layout, its labels and what `OrderAfter` orders. A reference the rule writes to
that object (a `sourceRef`) it writes from `member.ObjectName()` after the call. `spec.Kind`
and the scope are the ones the member's handler declares (`ComponentObject`); a name set on a
member whose type declares no object is refused by the transform. On a context with no
`Namer` an authored name is validated and set, the hook is not asked and nothing is claimed.

`LoweringContext.HasNamingHook()` reports whether a name resolved without an authored name
may be the hook's answer. A rule that reads another component of `Document` and needs the
name that component's object takes knows it only where the author wrote it, or where no hook
is set: the hook is asked by the rule that generates the object, never on another rule's
behalf, and the same name may be asked again (see above; `postgresql`'s archive check,
go-kure/launcher#1001).

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
`poolerName` or the default; its cluster endpoint selects pods by the Cluster's name, and
there it is the authored `clusterObjectName` or the component name. A consumer that sets
`Naming` calls
`ComponentEndpointsNamed(application, comp, naming)` instead, which asks `naming` the same
`NameRequest` the transform asks for that name, so one pure hook gives the selector the name
the Pooler or the Cluster gets; an answer that is no valid name for its role is refused there with the
transform's message. The names come from the rule's own lowering of the component, so the
hook is asked every request the transform asks for it: the ObjectStore's and each Database's
as well.
`application` is the document's name as the transform puts it in that request: its
`metadata.name`, or the name it has after lowering where a document rule renames it.

An endpoint answered by either entry is not the build's verdict on the document. Each is
given one component, and holds to one rule: **an endpoint entry answers only for a component
its type's own parse accepts, and refuses another in the parse's words.** The parse is what
the transform runs on that component alone: for a kind component (`service`, `cnpg-cluster`,
`cnpg-pooler`), the reading of its `objectName`, `labels` and `annotations` (the component
label excepted: the entries are given no key to hold it to) and then the kind's decode and
validation; for `webservice`, the rule's parse; for `postgresql`, the
rule's lowering, which names the Cluster, parses the component and names each member it
emits. A type with no endpoint entry answers no endpoint and no refusal.

Four things an endpoint entry does not see, and does not repeat:

- the schema check of authored properties (`ValidateAuthoredProperties`), which neither entry
  runs: an undeclared top-level property, or a required one left out, has no endpoint only
  where the type's own parse refuses it too;
- what the transform refuses of the component after its type's own parse, which an entry
  that does not run that step does not see. The two rule entries differ here. `webservice`'s
  runs the rule's parse and not its lowering, so a name or an evaluated field the lowering
  refuses (a `deploymentObjectName` that is no name, a node selector key that is no label
  key) still has an endpoint. `postgresql`'s runs its own lowering, so what the lowering
  refuses has none; what a kind it lowers into refuses of a member (a database named
  `postgres`, which the Database kind refuses) still has one;
- what is refused once the policy is applied, or at generation: neither entry is given the
  policy;
- what only the document shows: a name that is already another component of the document, a
  name another component generates in the same run, and two objects of the document that
  collide. A member `postgresql` emits is held against the name of its own component, which
  the entry is given, and against no other.

Each of these is refused where the document is built: by the schema check, the transform,
generation or the in-document collision check. Nothing is built from such an answer, and a
consumer that needs the document's verdict runs the transform, with the checks before and
after it.

A direct call of a kind's `Endpoints` runs the kind's parse on the component as it is given,
as a direct call of its `ToApplicationConfig` does, and gives the answer that gives. For a
component that still holds `objectName`, `labels` or `annotations`, which the transform
reads and takes out first: `cnpg-cluster` and `cnpg-pooler`, whose decode knows no such
field, refuse it in both in the same words; `service`, whose parse reads only the
properties it knows, accepts it in both.

A name that is not the default (the author's or the hook's) must be a DNS-1123 subdomain of
at most 253 characters, and is used as given or refused, never shortened. The `pooler` and
`workload-service` roles are narrower: a DNS-1123 label, which a Service's name must be, and
the Pooler's Service carries the Pooler's name. A leading digit is accepted here, and a build
whose `spec.kubernetesVersion` is below 1.36 (RelaxedServiceNameValidation off by default) refuses it in a generated Service
at generation (`docs/delivery-scope.md` §3.5). The refusal of a name written in `metadata.name`
names the kind and the field, as `Service metadata.name "1api"`, and prints the name once. Narrower
still is `postgresql-cluster`, whose Services carry the Cluster's name with a suffix: a
DNS-1035 label of at most 50 characters (CloudNativePG's own rule), a rule its default, the
component name, is held to as well. `postgresql-scheduledbackup` keeps the subdomain's syntax
but at most 63 characters, its default included: the operator copies the name into a label
of each Backup. For
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
whose shortened default policy names meet are refused too. Each synthesized policy is held in a
sub-application whose default is the policy's default name, resolved apart from the policy's: a
hook that names two such policies apart and not their sub-applications leaves two applications
of one name in the bundle, which is refused (go-kure/launcher#787).

A caller tells this refusal from every other error of the transform without reading its
text. The transform returns it behind a prefix of its own (`component "api": …`), and through
that prefix `errors.Is(err, oam.ErrNameCollision)` is true and `errors.As` finds the
`*oam.NameCollisionError`:

```go
var collision *oam.NameCollisionError
if errors.As(err, &collision) {
	// collision.Kind (group and kind), collision.Namespace, collision.Name
	// collision.First, collision.Second: the two members that named it
}
```

Each member (`NameCollisionMember`) holds its `Component`, the `Trait` type when a trait named
it, the `Role`, the `Property` the author wrote the name in, `FromHook` when the `Naming` hook
returned it, and `Description`, the member as the text names it. A caller with a `Naming` hook
reads from the two whether the name is the author's to change or its own hook's. What the
fields leave empty:

- `Kind` is zero for a name that is no object's: a bundle's, a hook-group name prefix, or the
  Kustomization of a chart's own layout (the members' `Role` says which);
- `Namespace` is empty for a cluster-scoped object and for a name that is no object's, and
  also for two names lowering rules resolved for one object that are refused while lowering,
  before the namespace the object lands in is settled: the text prints none either. One pair
  of lowered names is refused only once it is settled, and carries it: a `FluxScoped` name
  and one that is not, where both land in one namespace (the transform has no Flux
  namespace, or its Flux namespace is the one the document's objects land in);
- `Component` is empty for a name the document as a whole owns (the bundle, a group, a
  shared source, the policy synthesized for an external backend Service), which
  `Description` then says;
- `Role` is empty for an object a trait names itself (`ClaimObjectName`, below);
- `First` and `Second` are equal when one member named the object twice.

Only this refusal answers to it. The generated-object collision below and a lowering rule's
generated name the allocator refuses (`generated name "…" collides`) are other errors.

The names a lowering rule resolved are in it with the ones resolved after lowering. They are
held until the document's namespace is known and claimed first, so a `pooler` and another
resolved name of one kind, namespace and name are refused with both named, and two of one
rule (two `databases` entries given one `objectName`) at once:

```
name collision: Database.postgresql.cnpg.io "db-orders" is named by component "db" (role "database", its default) and by component "db" (role "database", set by databases[1].objectName); give one of them another name
```

This knows only the names resolved this way: the roles above. An object of a component that
is not a kind component, one a lowering rule names without a role, and the object of a trait that is not in the table (an
authored `configmap` trait's ConfigMap, an authored `secret` trait's Secret) are not in it, so
`CheckInDocumentCollisions` (below) is still what compares every generated object: a
`configmap` component given the `objectName` of a `configmap` trait's ConfigMap is refused
there, and so is a `secret` component that names the Secret of a `secret` trait:

```
generated-object collision: Secret "shop/shared" is generated by both component "shared" and sub-application "shared" of component "web"; rename one so that each object has one producer
```

The Secret a `helm` component generates for `secretValues` is in the table (role
`values-secret`), so a `secret` component under that name is a name collision instead. So are
the Deployment, the Service and the ServiceAccount of a `webservice` or `worker` component
(roles `workload-deployment`, `workload-service`, `workload-serviceaccount`): a `service`
component under the name of a `webservice` component's Service is a name collision. So are
the Cluster and the ObjectStore of a `postgresql` component (roles `postgresql-cluster`,
`postgresql-objectstore`): a `cnpg-cluster` component under the name of a `postgresql`
component's Cluster is a name collision.

A sub-application's name is resolved and validated, and kept apart within its bundle once the
bundle's traits have run: two applications of one name in one bundle are refused, a component's
own application and a trait's sub-application as well as two sub-applications. A `configmap`
trait and a `pvc` trait both named `dup` each add a sub-application `dup`, one holding a
ConfigMap and one a PersistentVolumeClaim, and the transform refuses them, naming both and
where each name came from:

```
bundle "shop": name collision: application "dup" is named by component "web" traits[0] "configmap" (role "sub-application", its default) and by component "web" traits[1] "pvc" (role "sub-application", its default); give one of them another name
```

The names are read after every trait of the bundle and every sub-application's `ApplyPolicy`
has run, so a policy that renames a sub-application onto another application's name is
refused (`renamed from "…" by its ApplyPolicy`), as is a sub-application a trait appends
without resolving its name (`set by the trait without resolving it`). Every bundle of the
transform is read again once nothing adds or renames an application any more, so the
sub-application of a synthesized NetworkPolicy, and a bundle a trait adds as a child, are
held to the same rule. Any other rename (by a later trait, or by another sub-application's
policy) is reported as such, from the last name its own trait or policy gave it (`renamed
from "…" after its trait named it`, or `renamed away and back to the name its trait or
policy gave it` when it is back under that name), and an application
whose namer the transform does not record (one in a bundle a trait added as a child) as
`named where the transform records no namer`. Where a trait resolved one name in more than
one way (by the author and by default, or for fewer of its sub-applications than it added under that
name), which way named which sub-application is not known, and the error says so rather
than guess; a hook that gave one name for two of a trait's defaults is reported without the
default (`returned by the Naming hook for one of the trait's defaults`). The refusal is a
`*NameCollisionError` with no `Kind`; a component's own application is the member with no
`Role` and no `Trait`. **This is a behaviour change** (go-kure/launcher#787): before it, two
sub-applications of one name were accepted. Two of one name in two bundles (two tiers, two
groups) are two applications and stay accepted. A Naming hook that answers for role
`sub-application` gives each a name of its own. A sub-application's
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
`(*Trait).ClaimObjectName(kind, namespace, name, property)`, or, for an object the trait
generates, `(*Trait).ClaimGeneratedObjectName` (below): the `cilium-networkpolicy` trait's
CiliumNetworkPolicy, whose required `name` leaves the hook nothing to be asked. The `ingress`
trait's Ingress and the `httproute` trait's HTTPRoute used to be claimed the same way; they are
resolved under their roles now (`ingress`, `httproute`). The name is the handler's own and is
not changed, and the `Naming` hook is not asked about it: there is no role for the object. The
claim only holds the name against every other one of the transform, so that a second owner of
the same kind, namespace and name is refused with both named, whichever comes first:

```
name collision: CiliumNetworkPolicy.cilium.io "default/allow-dns" is named by component "web" traits[0] "cilium-networkpolicy" (its own object, set by name) and by component "api" traits[0] "cilium-networkpolicy" (its own object, set by name); give one of them another name
```

`property` is the property the author wrote the name in, empty for the default. On a trait
built outside a transform nothing is claimed.

More objects are named by a required property and claimed the same way, with no role: the Secret
the `certificate` trait has cert-manager write (`secretName`), whose referrers (a secret volume,
an `ingress` trait's `tls[]` entry) are authored apart, the `external-secret` trait's
ExternalSecret (`secretName`), the `configmap` trait's ConfigMap, the `secret` trait's Secret and
the `pvc` trait's PersistentVolumeClaim (each `name`). A required name leaves no default for a
hook to replace. The Secret an ExternalSecret produces is the `external-secret` role's, and is
claimed under kind `Secret` too, so a `certificate` or `secret` trait's Secret of the same name
is refused.

All of these but the PersistentVolumeClaim move with the component's Flux object to the Flux namespace
(`TransformContext.FluxNamespace`) when that object reads the ConfigMap or Secret by name (in a
sibling group, any of the group's Flux members when the trait is on one of them), and
are claimed in the namespace they land in, decided by the same test that moves them: a Secret a HelmRepository
reads from its own trait no longer collides with a same-named Secret another component makes in
the application namespace, while two that land in the Flux namespace still collide. A Secret the
hook renames is no longer the one the Flux object reads: it stays, and is claimed, in the
application namespace. The trait says so with `NameSpec.FluxInput` on the role it resolves, and
with `(*Trait).ClaimFluxInputName(kind, namespace, name, property, readKind, readName)` for an
object claimed under no role, `readKind` and `readName` naming the ConfigMap or Secret whose read
decides where it lands. Both are trait-only: a lowering rule's Flux object is `FluxScoped`, and
a `FluxInput` NameSpec outside a trait is refused.

An object the trait's own sub-application generates is claimed with
`(*Trait).ClaimGeneratedObjectName(kind, namespace, name, property, readKind, readName)`: as
`ClaimFluxInputName` when `readKind` is set, as `ClaimObjectName` when it is empty. The
`configmap` and `secret` traits claim their object so, reading its own name, as the
`external-secret` trait claims its ExternalSecret; the `pvc` trait's claim never moves, and
neither does the `cilium-networkpolicy` trait's CiliumNetworkPolicy. The Secret a `certificate`
trait has cert-manager write is not generated by the trait and is claimed with
`ClaimFluxInputName`.

The two differ in one case. A trait a lowering rule emitted (a sealed trait) adds no claim of
a name that a lowering rule of its own component resolved and claimed
(`LoweringContext.ResolveName`), when it claims the name with `ClaimGeneratedObjectName`: the
object it generates is that rule's object (the `helm` rule's values ConfigMap and Secret, a role
component's volume claims), and a second claim would make one object two owners. Two generated
objects of one name are still refused by the generated-object check
(`CheckInDocumentCollisions`). Every other claim of a sealed trait is made as an authored
trait's: a name a component's role resolved, or another component's rule did, and any name
claimed with `ClaimObjectName` or `ClaimFluxInputName`. So a `certificate` trait a rule emits
with the `secretName` of the `helm` rule's values Secret is refused: cert-manager would write
over that Secret, and no generated-object check sees the Secret it writes.

### Outputs: the names a component's objects get

A consumer that wants to refer to an object a component generates (a Secret an operator
reads, a Service a route points at) needs the object's name before the document is built.
A component type answers with the optional `OutputProvider` interface, which the
`ComponentLoweringRule` claiming the type implements, or the handler when no rule claims it,
as for `EndpointProvider`. `Transformer.ComponentOutputs(comp)` returns one `Output` per
declaration, in the provider's order: a `Key`, the object's `Kind` (a `schema.GroupKind`) and
its `Name`. A key is one identifier (a letter or underscore, then letters, digits and
underscores), so a document can address it as `parts.<part>.<key>`. A type that provides no
outputs answers `(nil, nil)`.

The name is the name the build gives: the author's property, else the `Naming` hook's answer,
else the default. The provider resolves each name through `LoweringContext.ResolveName` or
`ResolveMemberName`, with the base, suffix and `NameSpec` its lowering uses, on a fresh
allocator, so the query claims nothing. As the endpoint queries do, `ComponentOutputs`
consults no hook, and a consumer that sets `Naming` calls
`ComponentOutputsNamed(application, comp, naming)`, which asks `naming` the same
`NameRequest` the transform asks.

A declaration is one of two shapes. An **allocated** output carries the `Role` and `Default`
the name was resolved under, and `Name`. The role must be a name role that names an object
and the default must not be empty, as the allocator requires; a declaration that fails either
is refused where it is answered, by a query as by the build. A **derived** output carries
`From`, the key of an allocated output, and `Derive`, which maps that name to this one. It
names an object the operator gives, not launcher: the Secret CloudNativePG generates for a
`postgresql` component's application user is `<cluster>-app`. It is not in the built
application, so nothing is claimed for it. The build applies `Derive` again to the name it
holds for the parent and fails if the result is not the name the output was answered, so
`Derive` must give one name for one parent name.

**The build holds every declaration.** The transform asks each provider for its
outputs as it lowers the component (asking the `Naming` hook again for each name the provider
resolves), and a difference fails the build, naming the component,
the output key, the kind and both names. For a lowering rule the declaration must find exactly
one name its lowering resolved for the same component, role, kind and default, and that name
must equal the declared one; for a handler, which names one object, the declared name must be
`Component.ObjectName` and the declared kind the GroupKind the handler declares for that object
(`ComponentObject`), group included. All three entry points (`Transform`, `TransformWithPolicy`,
`TransformWithProvenance`) run the check. A provider whose declaration is malformed or
whose query errs fails the build with its error: a malformed declaration is a defect of the
provider, not a quiet empty list.

A provider is asked about a copy of the component, in a query and in the build. The copy covers
every map, slice, array and interface that it reaches in the properties and in each trait's
properties. A write to one of these stays in the answer and never reaches the document or the
lowering. A pointer is shared, with everything behind it. A struct is copied by value, so a scalar
field of a struct is not shared, but a pointer, a map or a slice inside a struct is. A document
parsed from YAML or JSON holds no pointer, but it can hold a struct: an unquoted timestamp becomes
a `time.Time`, and the copy shares the pointer inside it. A component that a Go program builds can
also hold a pointer, and a provider that writes through a pointer, or through a map or a slice
inside a struct, changes the caller's value. A provider must not do this. The copy that authored
validation makes of the document (`ValidateAuthoredPropertiesWithCapabilities` and the
feature-gate check) follows the same rule: it shares a pointer, and what a struct holds by
reference, with the caller's `Application`, and a caller that writes through one in the returned
copy changes its own document. Validation's own code only reads through a pointer or a struct and
never writes through either, for a value under an open object. A test pins this
(`TestValidateAuthoredProperties_NeverWritesThroughAPointerOrStruct`). An error message formats
the value it refuses, or the object that holds it, with `%v`, so a `String`, `Error` or `Format`
method that the value supplies runs, and such a method can write. For a handler of a kind
component the query and the build give it
the same component: its object's name resolved and its `labels` and `annotations` taken off
its properties. The build prepares the metadata before it holds the outputs, so a component
with both a refused `labels` or `annotations` value and a failing provider reports the
metadata refusal.

What a query answers is not the build's verdict on the document. It is given one component
and answers only for one its type's own parse accepts, refusing another in the parse's
words. It does not see what only the document shows (a name that is already another
component, two objects that collide), the schema check of authored properties, or the policy.
A query made under an `application` other than the one the transform asks with is not held
against a build: the hook is asked a different `NameRequest`, and a hook that answers by
application can answer differently. `application` is the document's `metadata.name`, or the
name it has after lowering where a document rule renames it.

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
it runs on a component name (a Service's is a DNS-1123 label, a CronJob's at most 52
characters, a Job's at most 63).

It names the object alone. The component keeps its name everywhere else: the `app` label, the
pod template's labels, the selectors, the component label, the main container, and every name
a trait derives (`<component>-hpa`, a `configmap` trait's default). The one exception is the
endpoint selector of a `cnpg-cluster` or `cnpg-pooler` component: it matches the pods the
object owns, so it is built from the object name.

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
kind (the components README's "Kind inventory" gives the scope of each), and in the Flux
namespace for a Flux kind when the transform has one, so it is held against every other
resolved name:

```
name collision: Pooler.postgresql.cnpg.io "default/db-pooler" is named by component "db" (role "pooler", its default) and by component "pgb" (role "object", set by properties.objectName); give one of them another name
```

`objectName` and the `object` request apply only to a component no rule emitted. On a member
a component or trait lowering rule emitted the property is refused and the hook is not asked:
a rule that wants a member's name choosable resolves it itself at lowering time, under its
own role: `LoweringContext.ResolveName` for an object it names apart (`pooler`, `database`,
`postgresql-scheduledbackup`, `postgresql-recovery-objectstore`),
`LoweringContext.ResolveMemberName` for the object of a member it emits under the
component's name (`helm-release`, `oci-kustomization`, `oci-source`, `workload-deployment`,
`workload-service`, `workload-serviceaccount`, `postgresql-cluster`,
`postgresql-objectstore`). What a document rule or a raw document rule returns is
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

The same holds for the three objects a `webservice` or `worker` component generates, each
named on its own (`deploymentObjectName`, `serviceObjectName`, `serviceAccountObjectName`, or
the hook's answer for `workload-deployment`, `workload-service`, `workload-serviceaccount`):

- the `scaler` trait's `scaleTargetRef` names the Deployment by the name it took, whatever
  the Service and the ServiceAccount of the component are named;
- a routing trait's own backend is the Service by the name it took, and a route that names
  that Service resolves to the component for the synthesized NetworkPolicy, which keeps the
  component's name and selects the component's pods;
- the pods' `serviceAccountName` and the `rbac` trait's subject name the ServiceAccount by
  the name it took.

The labels, the selectors, the main container and every name a trait derives keep the
component name; `objectName` has the same rule, with the endpoint selector of a `cnpg-cluster` or
`cnpg-pooler` component as its one exception (above).

**A Service's name is its DNS name in the cluster, so a renamed Service is reached at another
address. Launcher builds no such address: every address written with the component name (an
`env` value, a URL in another component's properties, a backend another component's route
names) is the author's to change.**

A `postgresql` component's Cluster and ObjectStore are named on their own too
(`clusterObjectName`, `backup.objectStoreObjectName`, or the hook's answer for `postgresql-cluster`,
`postgresql-objectstore`), and what launcher writes to them follows:

- the Pooler's, each Database's and each ScheduledBackup's `cluster.name` name the Cluster
  by the name it took;
- the pod selector of the component's cluster endpoint (`cnpg.io/cluster`) is the Cluster's
  name, which `ComponentEndpoints` reads with the authored name and
  `ComponentEndpointsNamed` with the hook's as well;
- the Cluster's backup plugin names the ObjectStore by the name it took
  (`barmanObjectName`).

The default names of the Pooler, the Databases and the ScheduledBackups
(`<component>-pooler`, `<component>-<database name>`, `<component>-<schedule name>`) keep
deriving from the component name, whatever the Cluster is named. A component name the Pooler's default cannot be built from is refused with
`poolerName` named as what settles it. The two properties end in `ObjectName`, as
`objectName` does on a kind component; `poolerName`, the older property, keeps its name.

**CloudNativePG derives the Cluster's Services (`<cluster>-rw`, `<cluster>-ro`,
`<cluster>-r`) and Secrets (`<cluster>-app` and the others) from the Cluster's name, and the
default backup path moves with it: the server name a backup is stored under is the Cluster's
name unless it is set. Renaming an existing Cluster creates a new one: the old one is pruned
with its data unless it is protected. Every address or Secret reference written with the old
name is the author's to change.**

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
on every generated object ([Component label and ownership](labels-and-ownership.md#component-label-and-ownership), [Reserved metadata keys](labels-and-ownership.md#reserved-metadata-keys)).

The engine refuses, as a `TransformError` naming the component, the property and the key:

- a value that is no map and an entry that is no string; a null value or entry is an absent
  one, as at every property;
- a label key or value, or an annotation key, the API server refuses (the apimachinery
  rules), and annotations whose keys and values hold more than 262144 bytes together;
- the `app` label with another value than `ComponentLabelValue` of the component's name,
  which the kinds that set `app` select by;
- the component label key (`ComponentLabelKey`, or the key derived from `Domain`) with
  another value than the one launcher gives the component. The ownership wrapper refuses
  that value on every generated object ("Component label and ownership"); here the refusal
  names the property that holds it.

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

The two properties never reach a pod template. The pod template's labels and annotations are
a handler's own property, `podMetadata`, on `deployment`, `statefulset`, `daemonset`, `job` and
`cronjob` (go-kure/launcher#1178) and on `webservice` and `worker`, which hand it to their
deployment member (go-kure/launcher#1216); the engine neither reads nor removes it. What it writes is
checked as any pod template is: `ReservedMetadataKeys` and the component label.

