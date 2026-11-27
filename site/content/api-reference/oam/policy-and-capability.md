---
title: "Policy defaults and capability system"
weight: 7
---

The implementation, history and tests of the sections below are on the
[contributor page](../../contributing/oam-reference/policy-and-capability.md).

## Policy defaults & enforcement

`Policy` is a typed accessor interface (no type assertions in handlers) that carries
per-environment **enforced limits** (`MaxReplicas`, `MaxCPU`, `MaxMemory`, `MaxStorageSize`,
`AllowedRegistries`), **defaults** (`DefaultReplicas`, the CPU/memory request/limit defaults,
and the workload-shape defaults `DefaultStorageSize`, `DefaultScalerMinReplicas`,
`DefaultScalerMaxReplicas`), security flags, and two distinct capability-constraint families:
`AllowedCapabilities`/`ForbiddenCapabilities`/`RequiredCapabilities` gate **OAM trait-type**
strings (e.g. "ingress", "autoscaling"). They judge the trait types an author wrote and the
traits a component-position lowering rule wrote onto a part it emitted, so an emitted trait is
refused when forbidden or outside the allowed list, and it counts toward `RequiredCapabilities`
because the policy judges the output. Launcher's own component rules are exempt: the traits they
write (`topology-spread`, `pvc`, `configmap`, `secret`) are launcher's opinions, so a policy sees
the same outcome for them as before. A rule registered with `RegisterBuiltinComponentLowering`
is exempt, and one registered with `RegisterComponentLowering` (an embedder's or a model's) is
judged. A trait-position rule's output is not judged: the authored type is, so a rule that
renames `expose` to `ingress` leaves the policy reading `expose`. `AllowedContainerCapabilities`/
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
component and the sub-application.

**Optional policy interfaces.** A check that not every consumer needs is asked through an
interface a `Policy` may also implement, so adding one breaks no implementation.
`ExplicitSecretPolicy` (`AllowExplicitSecrets() bool`) says whether a
document may carry secret values itself: the `secret` trait, the `secret` kind component, the `helm` component's
`secretValues` and the `helmtemplate` kind's, and a core Secret the `passthrough` or
`manifests` component carries. `ExplicitSecretsAllowed(policy)` is how
a handler asks. **A policy that does not implement it allows them**, as does no policy at all: this is
the permissive side, so a consumer that must keep secrets out of documents has to implement the
interface and answer `false`. A refusal is a `ViolationError` naming the component, of class
`RefusalExplicitSecret` (below).

`ImageReferencePolicy` (`ImageReferenceLevel() ImageReferenceLevel`) says how exact the
image references a build emits must be, one of four levels that are not nested:

| Level | Accepts | Refuses |
|---|---|---|
| `any` | every reference that parses, an untagged one and `:latest` included | one that does not parse |
| `tagged` | a tag or a digest | no tag, `:latest`, and `repo:latest@sha256:...` |
| `version` | a version tag such as `v1.2` or `1.2.3-alpine` | a digest alone, `:latest`, any other tag |
| `digest` | `name@sha256:...` (`latest@sha256:...` too) | any reference without a digest |

`ImageReferenceLevelOf(policy)` is how it is read. **A policy that does not implement it, or
answers an empty level, holds `tagged`**, as does no policy at all, which is the rule as it was
before the interface: no document changes until a consumer asks. A level that is none of the four
is refused, never read as `tagged`. The level reaches every image a built-in component names: a
container's, an init container's, a sidecar's, an image volume's reference, the images of the
CloudNativePG, monitoring and `postgresql` kinds, and the workloads a chart renders. An `Endpoints`
query checks the syntax of an image only, since an endpoint does not depend on which image runs;
the build holds the image to the level where it emits it.

A handler reads the level from `Component.ImageLevel()`, which the Transformer sets on its working
copy of the component before it calls `ToApplicationConfig`, and a lowering rule reads it from
`LoweringContext.ImageLevel()`. Both read `tagged` where nothing set them. A handler that builds a
fresh `Component` instead of copying the one it was given loses the level and reads `tagged`:
that fails closed under `any`, and a config that implements `oam.ImageReferenced`
(`ImageReferences() []oam.ImageReference`) is held to the stricter levels `version` and `digest`
by the Transformer's own reading of the config after the handler returns. A custom handler that
parses images calls `components.ValidateImageRefAt(image, level)`; `ValidateImageRef` stays the
`tagged` form.

`SourceReferencePolicy` (`SourceReferenceLevel() ImageReferenceLevel`) is the level of an `oci`
component's `version`, a tag or `sha256:<digest>` that no image rule reads. It defaults to `any`,
since the component checked nothing before: `tagged` requires a non-empty value that is not `latest`, as it does for an image, `version` a
version tag or a digest, `digest` a `sha256:` value (`oam.CheckSourceVersion`).

`ContentSourcePolicy` (`ContentSource() oam.ContentSource`) hands the build a caller's source of
bytes for the `url` of a `crd` or `manifests` component (go-kure/launcher#1669). It is a
dependency, not a constraint: it rides the policy because the policy is the one per-request input a
component receives. The source is asked `Lookup` once the url's scheme and host pass the policy, before any
request, with an `oam.ContentKey` of kind `url` that holds the url exactly as the document gave it. A
copy is served only if it is within the 5 MiB cap, it records at least one host, every host the caller
recorded for it (`Content.Hosts`) passes `AllowedRegistries()` (a fetch checks every redirect hop, so a hit
checks every recorded host; a fetch always records the first host, so a copy with none is a miss), and
its body parses as manifests; any other answer, an error included, is a miss, and the url is fetched.
After a fetch that succeeded, `Store` receives the body and every host the request went to, the first and
each redirect hop, in order. A `Store` error is ignored. A source's own errors never fail a build and
never change its output. A copy it serves is the caller's word for what the url holds: it can differ from the
url's current body, and a copy that parses as manifests but that the component then refuses (for a `crd`
component, a document that is not a CRD) fails the build as the fetched body would. Storing, expiry and which
copies to trust stay with the caller.
The same source serves the chart of a `helmtemplate` component (key kind `chart`), where a stored chart is
checked as a fetched one is: once, on the host of `source.url`, and no host of `Content.Hosts` is compared
with `AllowedRegistries()`; a copy that records none is a miss here too. `ContentKey` and `Content` carry
the fields kure's chart content source has, so one source can serve both.
A policy that does not implement it, and `NoopPolicy`, fetch every url and every chart.

`ObjectKindPolicy` says which kinds of object a build may emit and
whether it may emit cluster-scoped ones: `AllowedObjectKinds()` (empty allows every kind),
`ForbiddenObjectKinds()` (a kind on both lists is forbidden) and `AllowClusterScopedObjects()`.
A kind is a `schema.GroupKind`, matched in any version; a `Kind` of `*` matches every kind of
its group, the core group is `""`, and an entry with no `Kind` fails the transform. One check
holds every producer to it: the ownership wrapper the transform's last step puts on every
application reads each object generation emits (a kind component's, a trait's, what
`passthrough` and `manifests` carry, every object a chart renders at build time) as Flux
applies it, a list envelope (in any Go representation, read as it is written) by its members
and not as an object of its own (a kind ending in `List` through every List it holds, less one
whose items is null, as Kustomize drops it; any other envelope one level, as Flux expands it), each object a layout augmenter adds or edits among those its
Generate returned, and the ConfigMap of each configMapGenerator entry it adds under a new name.
The scope is kure's
`manifest.Scope`; a kind of unknown scope (neither built in nor registered with kure, such as a
custom resource whose CRD is installed apart from the build) counts as cluster-scoped whatever
namespace the object states. The refusal comes at generation (`GenerateApplications`, or kure's layout walk), a
`ViolationError` naming the owning component, of class `RefusalObjectKind`. **A policy that
does not implement it allows every kind and cluster-scoped objects**, as does `NoopPolicy`, and
the check does not run. Not covered: what a chart Flux installs renders on the cluster, objects
a controller creates, and what an RBAC object grants (the API server's escalate and bind
checks hold that to the identity that applies it).

Handlers apply values with the precedence **authored > policy default > handler default**,
then enforce the limits on the resulting effective value — for cpu/memory this explicitly
includes the `pkg/oam/builtin/components` intrinsic handler-default tier (100m CPU / 128Mi
memory request, applied by `buildResourceRequirements` at `Generate()` time, after
`ApplyPolicy` runs), not just the authored/policy-defaulted value `ApplyPolicy` sees; see
that package's README, "Policy defaults & enforcement ordering". An author can opt a
container out of its default cpu or memory limit with the string `unlimited` under
`limits.cpu`/`limits.memory`; that is tier-1 authored, so a policy default limit does not
apply over it, and it is refused with the `resource-maximum` class where the policy sets
`MaxCPU()`/`MaxMemory()` for that resource.

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
error text:

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
| `RefusalRegistry` | `registry` | A host outside `AllowedRegistries`, or one that cannot be held to that list: the registry of a container image or of an image volume's reference; an image an operator chooses because the document names none (`cnpg-cluster`, `cnpg-pooler`); the host a chart, a `manifests` `url` source or a Flux source is fetched from; an `oci://` url that does not name its registry; an Amazon S3 bucket endpoint, whose host Flux picks at runtime. |
| `RefusalResourceMaximum` | `resource-maximum` | A cpu or memory request or limit over `MaxCPU` or `MaxMemory`: on a container, on the pod, or on the pod template of an ACME HTTP01 solver of an `issuer` or `clusterissuer`. |
| `RefusalResourceDefaultLimit` | `resource-default-limit` | A cpu or memory request the document authors above the limit the policy defaults for that resource (`DefaultCPULimit`, `DefaultMemoryLimit`), on a container that authors no such limit: the main container, an init container or a sidecar of a workload kind. The author writes a limit or lowers the request. |
| `RefusalStorageMaximum` | `storage-maximum` | A storage request, or the capacity of a PersistentVolume, over `MaxStorageSize`. |
| `RefusalReplicaMaximum` | `replica-maximum` | A replica count, an autoscaler's `maxReplicas` or a database cluster's instance count over `MaxReplicas`. |
| `RefusalExplicitSecret` | `explicit-secret` | Secret material the document carries itself, under a policy that forbids explicit secrets (`ExplicitSecretPolicy`): a `secret` component or trait, a Secret that `passthrough` or a `manifests` source carries, `secretValues`, a `certificate`'s keystore password, a `fluxcd-provider` `address` that is the credential, and a credential or the `fake` provider's data written into a `secretstore` or `clustersecretstore`. |
| `RefusalTraitCapability` | `trait-capability` | A trait type the policy forbids or does not list as allowed, and one it requires that the application does not use. |
| `RefusalUnreadableObject` | `unreadable-object` | An object written elsewhere (rendered by a chart, carried by `passthrough` or by a `manifests` source) that the build cannot read, so that it cannot be held to the policy and is refused instead of passed. |
| `RefusalObjectKind` | `object-kind` | An object of a kind the policy forbids or does not list as allowed, or a cluster-scoped one (or one of unknown scope, whatever namespace it states) under a policy that does not allow cluster-scoped objects (`ObjectKindPolicy`), whatever component or trait emits it; and an object whose kind cannot be told. Refused at generation. |

The class is the same on every path a refusal comes from: a component's `ApplyPolicy`, a
post-policy step a lowering rule attached (the `1Gi` storage fallback of `postgresql` over
`MaxStorageSize`), a trait sub-application's, the check on the objects a chart renders under
template delivery,
the `passthrough` and `manifests` checks, at the transform and again at generation, and the
object kind check, at generation only. Of a
`manifests` `url` source the transform checks the host of the url; the objects it yields are
first known at generation, so a violation about one of them comes from `Generate`, not from
the transform. The text of no refusal changed with the class: it is the
`PolicyRefusal` at the end of the cause chain, whose `Error()` is the text the refusal had.

**Unclassified.** `RefusalUnclassified`, the empty string, is the class of a violation whose
cause is not a refusal by the policy. No class is guessed from text. It covers:

- an `ApplyPolicy` error of another kind: a chart that does not render, a policy default or
  maximum that does not parse, a policy whose default request is above its own default limit
  (a defect of the policy: the document authors neither value), a request above a limit the
  policy did not fill (only a config built in code can hold one: the parser refuses it in a
  document), and the library's own rules on an object, which hold with or without a policy
  (no ephemeral containers, an image with no tag or digest, an undeclared field on a
  workload or a claim a chart renders);
- a refusal a consumer's own `Enforceable` returns as a plain error;
- a `ViolationError` literal that leaves `Class` out.

Not violations, and so without a class: `ErrPlatformReserved` and `ErrReservedMetadataKey`,
which stay the sentinels they were, `ErrComponentLabelValue`, which is a sentinel of the same
kind, and a `manifests` `url` source that cannot be fetched. One
fetch failure is a refusal by the policy all the same, a redirect to a host outside the allowed
registries: its error holds a `PolicyRefusal` of class `RefusalRegistry`, which `errors.As`
reaches, under no `ViolationError`.

**A refusal in a `target.manifest`.** An `externalsecret` or a `clusterexternalsecret` may name the
object the operator would write, and that object is held to the policy as `passthrough` holds one.
A Secret there is refused with the class `RefusalExplicitSecret`, the same as a Secret a document
carries as an object. The refusal is a `*TargetManifestRefusal` around the `PolicyRefusal`: its
`At` is empty for an `externalsecret` and `externalSecretSpec.` for a `clusterexternalsecret`, and
its `Error()` is `At`, then `target.manifest: `, then the refusal's text, which is the text the
refusal had before the type existed. A consumer that gives other advice for this Secret than for
one the document carries finds it with `errors.As`, never by reading the message. The other refusal
of a `target.manifest`, an object of a kind the policy reads, keeps its class
`RefusalUnreadableObject` and is not this type.

**A consumer's own `Enforceable`** returns `NewPolicyRefusal(class, message)`, bare or wrapped
with `%w`, for its violation to carry the class: one of the constants above or a value of its
own, which the library passes on as given. The class of a violation is that of the first
`PolicyRefusal` in its cause chain. `NewViolationError(component, cause)` builds a violation
the same way, for a config that reports one itself at generation, as `passthrough` and
`manifests` do.

**Breaking** for an unkeyed `ViolationError` literal, which no longer compiles; a keyed
one is unaffected. And for a caller that matched `*TransformError` on a refusal by the
policy raised in a post-policy step: it is a `*ViolationError` now, with the same text.

### Declared bounds

A property schema can name the classes an authored value at that property can cause, in
`boundedBy` (see [Property schemas](schemas-and-contract.md#property-schemas)). These
built-in types declare them. Each entry is held by a test that shows the refusal; a property
with no entry is not shown to be free of a bound.

| Class | Declared on |
|---|---|
| `host-namespace` | `hostNetwork`, `hostPID`, `hostIPC`. |
| `privileged` | `securityContext.privileged` and `securityContext.windowsOptions.hostProcess` of a container; `podSecurityContext.windowsOptions.hostProcess`. |
| `host-path` | `volumes[].type`: the refusal is of a volume whose type is `hostPath`. |
| `container-capability` | `securityContext.capabilities.add` of a container. |
| `registry` | `image` of a container; `volumes[].image`, an open node; `url` of `gitrepository` and of `manifests`; `source.url` of `helm`. |
| `resource-maximum` | `resources.requests` and `resources.limits`, `cpu` and `memory`, of a container; the same four under `podResources`. |
| `resource-default-limit` | `resources.requests.cpu` and `resources.requests.memory` of a container. |
| `storage-maximum` | `volumes[].ephemeral`, an open node; `volumes[]` of `webservice` and `worker`, an open node, for the `size` of a `pvc` volume that generates its claim; `volumeClaimTemplates` of `statefulset`, an open node, for the storage request of each claim template; `size` of the `pvc` trait. |
| `replica-maximum` | `replicas` of `deployment`, `webservice`, `worker`, `statefulset`, `replicaset` and `replicationcontroller`; `maxReplicas` of the `scaler` trait. |
| `explicit-secret` | `inline` and `url` of `manifests`: a core Secret the source carries. |
| `unreadable-object` | `inline` and `url` of `manifests`: an object the policy check cannot read. |

The container properties are those of the main container, at the top of the component, and
the same ones under `initContainers[]` and `sidecars[]`. Where an entry names no type, it
holds for the workload types that keep the reshaped form: `webservice` and `worker`.

The kinds that take a pod as the API type leave most of it open, so their bounds sit on the
open nodes, with no path below them. `pod` declares `host-namespace` on `hostNetwork`,
`hostPID` and `hostIPC`; `privileged`, `container-capability`, `registry` and
`resource-maximum` on `containers` and on `initContainers`; `privileged` on
`securityContext`; `resource-maximum` on `resources`; and `host-path`, `registry` and
`storage-maximum` on `volumes`. `podtemplate`, `deployment`, `statefulset`, `daemonset`,
`job`, `replicaset` and `replicationcontroller` declare those seven classes on their one
`template` node, and `cronjob` on its `jobTemplate` node.

`gitrepository` declares `registry` on `url`, and `helm` on `source.url`: the source the
component generates (a HelmRepository, an OCIRepository or a GitRepository, or the
`helmtemplate` under template delivery) is held to the allowed registries. `manifests`
declares `registry` on `url`, a redirect to another host included. It declares
`explicit-secret` and `unreadable-object` on both `inline` and `url`, because the document is
the value of `inline` and is what `url` names: a core Secret is refused under a policy that
forbids explicit secrets, and so is an object the check cannot read (a workload in an API
version the build does not know, an autoscaler whose `maxReplicas` is not an integer).
The other classes the same check raises on a workload object of a `manifests` source (the
image, pod security, resource, storage and replica rules) are shared with `passthrough` and
`helmtemplate` and are declared on none of them yet.

`trait-capability` and `object-kind` are declared on no property: they are about a type as a
whole, and the second is the transform's check on every application, whatever component
emitted the object. `explicit-secret` and `unreadable-object` are declared only on
`manifests`. Other built-in types do raise them, some from one property (the keystore
`password` of a `certificate` component, for one) and some for the type as a whole (a
`secret` component or trait, whatever its properties hold). Those types, like every built-in
type not named above, declare no bound yet: that says nothing about whether the policy holds
them.

## Capability system

Capability-aware traits (e.g. `expose`, `certificate`, `external-secret`) declare
required platform inputs; the `ClusterProfile` provides them, and
`CapabilityDefinition` rendering schemas validate the bindings of custom capabilities
(`--strict-capabilities` turns warnings into errors).

`Transformer.CapabilityValidated(typeName)` reports whether `EvaluateProfile`
validates a capability rendering of that type. It returns true when a registered
trait handler or trait lowering rule implements `ValidateAndApplyDefaults`, or
when a non-built-in type has a `CapabilityDefinition` whose schema is applied.
With none of those validators, `EvaluateProfile` passes the binding through
unchecked.

**Pre-GA behavior change**: `EvaluateProfile` now also checks a binding whose type has
a `CapabilityDefinition` but no trait handler and no trait lowering rule. The schema
applies its defaults and refuses a rendering that breaks it. Before, such a binding
passed through unchecked. Built-in types stay exempt.

**The key of a refused capability.** A refusal that names a capability in its text is
wrapped in an `*oam.CapabilityError`, which `errors.As` finds in the error that
`Transform`, `TransformWithPolicy`, `EvaluateProfile` and
`ValidateAuthoredPropertiesWithCapabilities` return. Its `Capability` field is the key the text
names, so a caller that adds context reads the field and never the message. The key is a
`ClusterProfile` key, `<type>` or `<type>.<scope>`. Four refusals carry the trait type instead,
because they cannot see which binding matched: the three by the policy's
`ForbiddenCapabilities`, `AllowedCapabilities` and `RequiredCapabilities` lists (class
`RefusalTraitCapability`), and the `expose` rule's "ext-auth is not offered on this platform"
refusal, whose key is `expose` even when the binding that matched is `expose.<scope>`. The error's `Error()` is the text
the refusal had, and `errors.Is` and `errors.As` still reach what it wraps: a `*PropertyError`, a
`*PolicyRefusal` under its `*ViolationError`, a `*TransformError` and `ErrMissingCapability`.
**Breaking** only for a caller that compared the returned error with `==` or by type assertion.

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
the platform value. Where both hold an object, the merge recurses:
authoring `resources: {limits: {cpu: 2}}` over a rendered
`resources: {limits: {cpu: 1, memory: 1Gi}}` gives `{cpu: 2, memory: 1Gi}`, and an
authored `{}` keeps the rendered object. A list, a value of another kind, and a
rendered object built in Go with another type (`map[string]string`) are replaced
whole. An authored `null` is absence here too, at any depth, so a `null` over a key
the rendering supplies takes the platform value. A `null` under a key the rendering
lacks is left as authored. **Pre-GA output changes**: the authored `null` used to
replace the rendered value, so the handler saw the key unset;
an authored object used to replace the rendered object whole, dropping its sibling
keys. This holds for every unsealed trait with a rendering, on both the
dispatch path and an unsealed trait lowering rule's input.

Authored validation checks nested `Required` on the merged properties too
(`ValidateAuthoredPropertiesWithCapabilities`), so a partial
override that omits a required nested key the rendering supplies is accepted. The
profile-less `ValidateAuthoredProperties` still refuses it.

A component handler that implements `ComponentCapabilityDefaults` (see [Transform &
extension](transform-and-lowering.md#transform--extension)) gets the same precedence for the properties it lists, and only those.
Each value it fills is then validated against the component's own `PropertySchema`:
a value the trait handler's schema accepted can still be
refused here, with an error naming the component, the capability and the property
(`component "data": capability "pvc" defaults: properties.storageClassName: expected
string, got int`). A component with no schema relies on the trait side, and its
fill is refused when the component has no schema and no trait handler or trait
lowering rule for the capability's type validates the rendering, meaning it
implements `ValidateAndApplyDefaults`, or the type is not built in and has a
`CapabilityDefinition` applied. A type with no handler, no rule
and no applicable `CapabilityDefinition`, whose binding `EvaluateProfile` never checks,
is refused there too. The
error names the component, the capability and the rendering keys. **Pre-GA input
change**: a fill that used to reach a component unvalidated is now refused when
nothing validates it, and a value the component's schema does not accept is refused.
No built-in component is affected: `persistentvolumeclaim`, the only built-in
`ComponentCapabilityDefaults` implementation, declares a schema.

Both merges copy the rendering value they hand out, keeping its Go type: a `3` the
profile decoded as `int` reaches the handler as `int`, and an `int64` above 2^53 keeps
its exact value. The copy used to be a JSON round trip, which
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

This is a large internal builder surface; the tables on this page and in [Transform, extension and lowering](transform-and-lowering.md#transform--extension) cover the entry points.
See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the full
type reference, the design notes under the Concepts section, and `examples/` for
runnable applications.
