# Design: operator custom-resource components

Status: accepted (go-kure/launcher#281). First instance: the `cnpg-cluster`
component, a projection of the CloudNativePG `postgresql.cnpg.io/v1` `Cluster`.
go-kure/launcher#573 adds `cnpg-pooler` (`Pooler`), `cnpg-database`
(`Database`) and `cnpg-objectstore` (the Barman Cloud plugin's
`barmancloud.cnpg.io/v1` `ObjectStore`).

## Problem

An operator's custom resource (a CNPG `Cluster`, and later its `Pooler`,
`Database` or `ObjectStore`) has a large, versioned spec that the operator
already validates and defaults. Launcher had one way to author such a resource:
a role-named component such as `postgresql`, which publishes a small curated
property set, fills in launcher's own choices (an image tag from `version`, a
`1Gi` storage fallback, `enablePDB` derived from the replica count,
`primaryUpdateStrategy: unsupervised`) and emits the result. Every CR field that
component does not name is out of reach, and each new one is a code change.

The alternative, `passthrough`, reaches every field but gives up everything a
component provides for a custom resource: no schema, no environment policy, no
endpoints for network policy synthesis. Since go-kure/launcher#794 `passthrough`
holds the object it emits to the environment policy, but only on the kinds that
check reads (workloads, claims, autoscalers and PersistentVolumes). A custom
resource is none of them, so it passes whatever it holds, and the pods its operator
creates from it are not checked.

## Decision

Split the two concerns into two layers:

1. An **operator-CR kind component** projects exactly one custom-resource kind
   at full fidelity, with no launcher opinions. It owns the CR's schema,
   environment policy and endpoints.
2. A **semantic component** (such as `postgresql`) becomes a lowering rule onto
   kind components: it computes its opinions and writes them as properties of
   the kind components it expands into.

### One kind component per CR kind

Each component emits one object of one kind. `cnpg-cluster` emits one `Cluster`
and nothing else; a pooler, a database or an object store is a separate kind
component (`cnpg-pooler`, `cnpg-database`, `cnpg-objectstore`), wired together
by the author or by a semantic component's lowering rule: the `Pooler` and the
`Database` name their `Cluster` in `cluster.name`, and a `Cluster` names its
`ObjectStore` in its plugin configuration. One kind per component keeps a component's schema
equal to one upstream type, so the coverage test below has a single target.

### No launcher opinions on the kind

The kind component writes what the author wrote and nothing else. An unset field
is left unset so the operator's own default applies. The one exception is a
field the upstream Go type cannot leave unset: `ClusterSpec.instances` is an
`int` without `omitempty`, so an unauthored value would serialize as `0`. The
kind writes the CRD's documented default (`1`) there instead, which is the value
the API server would have applied. `DatabaseSpec` has the same shape one level
down: the `ensure` of each schema, extension, fdw and server has no
`omitempty` and a CRD default of `present`, so an unauthored one would reach the
API server as `""`, which the CRD's enum refuses. `cnpg-database` writes
`present` there, and refuses an authored `ensure: ""`, which the Go type
cannot tell from an unauthored one. A `Pooler` template is the third case: its `spec` is not a
pointer and `containers` has no `omitempty`, so a template that lists no
containers would carry `containers: null`, which the API server prunes before
checking the CRD's required list. `cnpg-pooler` writes `containers: []`, which
the operator reads as it reads an omitted spec, adding its `pgbouncer`
container. A second derived test pins the `Database` `ensure` cases the same
way as the omitted-zero list below: the CRD's scalar defaults crossed with the
Go type's non-pointer fields without `omitempty`. `cnpg-cluster`'s
`instances` default is pinned by its own test. The `containers`
case is a list, outside that derivation, and is pinned by its own
serialization test.

A launcher opinion that depends on the post-policy object — `enablePDB` on only
when there is more than one instance, for example — is not computed by the kind.
It is carried by a **trait applied after policy**, which reads the generated
object and sets the field. go-kure/launcher#540 set the precedent: its
`topology-spread` trait applies launcher's default spread opinion from a
Deployment's post-policy replica count, so a document or a lowering rule can ask
for that opinion explicitly while the `deployment` kind itself carries none. The
trait is attached by the semantic component's rule, not by the kind, so an
author of the kind component gets exactly what they wrote.

### Schema recipe

The component's `PropertySchema` mirrors the upstream spec type:

- **Declare every top-level field.** Each json field of the upstream spec type is
  a schema key with its type and a description. Scalars are typed (`string`,
  `integer`, `boolean`); structured fields are `object` or `array` (of objects)
  with `additionalProperties: true` and a description pointing at the upstream
  type. The schema does not restate the deep structure: that would be a second,
  hand-maintained copy of a type the operator versions.
- **Decode strictly into the upstream type.** `ToApplicationConfig` decodes the
  whole property map into the upstream Go struct with unknown fields disallowed
  (`builtin.DecodeStrictJSON`). A misspelt key at any depth, or a value of the
  wrong type, is an error naming it, not a field silently dropped from the
  emitted object. The strictness the schema does not carry below the top level
  comes from here.
- **Pin the schema to the type with a coverage test.** A reflection test walks
  the upstream type's json fields and requires each to be either published in the
  schema, with the matching type, or listed in an exclusion map with a reason.
  It also fails on a schema key with no field, and on an exclusion naming a field
  that no longer exists. A dependency bump that adds, removes or retypes a field
  therefore fails the suite, naming the field, instead of shipping a schema that
  has drifted from what the decoder accepts. `cnpg-cluster`'s exclusion map is
  empty.

Explicit nulls follow the package-wide null contract: a null is absence at every
depth, and a null array element is an error naming its path. They are removed
before the strict decode, so a null never reaches the operator as a zero value.
The contract stops at declared keys, so the unstripped tree is decoded strictly
as well, and a key the operator type does not declare is refused as an unknown
field even when its value is null.
The contract defines a null as a value that serializes to JSON null, so the
handler applies it to the serialization itself: the property map is marshalled
with `encoding/json` and decoded back into plain maps, arrays and scalars before
nulls are removed. A lowering rule emits the same JSON-shaped values as for
every component (string-keyed maps, slices, scalars); that is the supported
contract. The engine checks them against the schema before the handler runs
and refuses a typed API struct where the schema checks an object or array item,
but it does not descend into an open object's undeclared keys, so the contract
is not enforced at every depth. The serialization read additionally makes a
direct caller of the handler read exactly as it encodes — concrete collection
types, nils behind pointers, raw JSON, custom encoders — with no separate walk
to keep in step with the encoder. A value that does not serialize is refused.

The typed decode can also lose an authored value in the other direction. Many
upstream fields are non-pointer and `omitempty`, and some carry a non-zero CRD
default (`managed.roles[].connectionLimit` defaults to `-1`, `postgresUID` to
`26`): an authored `0` or `false` decodes into the field, is omitted when the
object is encoded, and the API server applies the default. The kind refuses
such a value by path rather than silently change it. The decoded spec is
encoded as the emitted object will be, and an authored numeric zero or `false`
with nothing at the same path in that encoding is an error when the path is
in the kind's list of fields whose CRD default is not zero. On any other
field the absent value means the same zero, so an explicit default such as
`managed.roles[].login: false` or `minSyncReplicas: 0` is accepted with its
meaning unchanged. A coverage test
pins the list: it derives the set from the linked operator module itself —
the CRD's scalar defaults that are not zero, crossed with the non-pointer
`omitempty` fields of the Go spec type — and requires the list to equal it,
default values included, so a dependency bump that adds, removes or changes
such a field fails CI, naming it. An authored empty string is not refused
(`storage.size: ""` is a supported value), and two spellings of one field in
the same object (`size` and `Size`) are refused, since the decoder would keep
only one.

### Policy lives on the kind

Environment policy (`oam.Policy`) is enforced by the kind component's
`ApplyPolicy`, because the kind is what emits the object the policy constrains.
A semantic component that lowers onto the kind inherits the enforcement rather
than duplicating it. For `cnpg-cluster` this is the policy `postgresql` applies
today — the instance count default and maximum, the cpu and memory request and
limit defaults and maxima, and the storage size default and maximum — plus the
checks that follow from fields the curated component never exposed: the storage
maximum on every claim the Cluster creates (`storage`, `walStorage`, each
tablespace and the ephemeral volume template), the privileged, capability and
host-process refusals on the two security contexts, and the registry allowlist
the workload kinds apply to their image, on an authored `imageName` and on the
image of each `postgresql.extensions[]` entry, with a Cluster that leaves its
image to the operator refused under a non-empty allowlist.

`cnpg-pooler` polices what the `Pooler` runs: its pod template gets the gates
the workload kinds apply to their pod (host namespaces, hostPath volumes,
privilege, host-process, capabilities, the registry allowlist on each authored
container image and on an image volume's reference, and the cpu and memory
maxima), plus the storage maximum on a
generic ephemeral volume's claim, as `cnpg-cluster` caps its ephemeral volume
template, and an authored `pgbouncer.image` gets the registry allowlist; under a
non-empty allowlist a Pooler that leaves the PgBouncer image to the operator is
refused (see *What this does not cover*). A
template declaring `ephemeralContainers`, `activeDeadlineSeconds`, `priority`
or `overhead` is refused at parse, as the workload kinds refuse them: the
operator copies the template into a Deployment, whose pod template cannot
carry the first two and whose pods get the last two from the default Priority
and RuntimeClass admission controllers, which refuse an authored value that
differs from theirs. The instance count is not
policed, neither by a replica default nor by a maximum: `postgresql` applies no
policy to its pooler today, so a maximum on the kind would refuse, once
`postgresql` lowers onto it, a document that builds today, and break the
identical-output promise below. `cnpg-objectstore` caps the cpu and memory of
the plugin sidecar it adds to every instance pod
(`instanceSidecarConfiguration.resources`). A `Database` runs nothing of its
own, so `cnpg-database` applies no policy. As `cnpg-cluster` does for its
`resources`, both kinds run admission's request/limit and hugepages checks at
generation on every resource block they emit for a pod or container, so a
request above its limit is a build error rather than a refused pod.

A policy default never overrides an authored value, including an authored value
equal to what the default would be. The kind therefore records which fields were
authored before it fills any default.

### Endpoints live on the kind

The kind component implements `oam.EndpointProvider`, declaring the pods a
consumer connects to. `cnpg-cluster` declares the primary endpoint
(`cnpg.io/cluster: <Cluster object name>` on port `5432`), the same one `postgresql`
declares, so a synthesized ingress allow does not change when a component moves
from one to the other. `cnpg-pooler` declares its PgBouncer pods
(`cnpg.io/poolerName: <Pooler object name>` on port `5432`). The object's name is
the component name unless `objectName` or the naming hook names it. `postgresql` names its
`Pooler` `<component-name>-pooler` by default, so a `cnpg-pooler` of that name declares the
identical endpoint, which a test pins byte for byte. `cnpg-database` and
`cnpg-objectstore` run no pods and declare no endpoint.

### Semantic components as lowering rules

A semantic component keeps its curated authoring surface and its opinions, and
lowers onto kind components through the lowering engine (see the Lowering
Engine design). go-kure/launcher#281 does this for `postgresql`: its rule
expands one `postgresql` component into a `cnpg-cluster` (and, where enabled, a
`cnpg-objectstore` of the same name, a `cnpg-pooler` and one `cnpg-database` per
database), writing the image and update strategy as kind properties. The two
values that depend on the policy, which runs after lowering, come from a
post-policy step the rule attaches to the Cluster (go-kure/launcher#729):
`enablePDB` from the post-policy instance count, and the `1Gi` storage fallback,
held to the policy maximum. The generated objects are unchanged; with both an
object store and a pooler, the ObjectStore now precedes the Pooler.

## Consequences

- An author who needs a CR field the semantic component does not publish uses
  the kind component directly, and keeps policy and endpoints.
- A new upstream field is authorable as soon as the dependency is bumped and the
  coverage test is satisfied; no hand-written property parser is involved.
- The kind's schema documents the top level only. Authors read the operator's
  API reference for the deep structure; the strict decode is what catches a
  mistake there.
- Opinions live in one place each: in a semantic component's rule, or in a trait
  that rule attaches. A kind component never grows an opinion to serve one
  semantic component.

## What this does not cover

- The CRDs' validation rules (CEL) are left to the API server, except the
  `ObjectStore`'s ban on `configuration.serverName`, which `cnpg-objectstore`
  refuses because the shared Barman type invites it. `postgresql` carries its
  `objectStore.serverName` as the `serverName` parameter of the Cluster's
  barman-cloud plugin entry, never on the `ObjectStore` it emits
  (go-kure/launcher#643).
- Of the shared resource parser's per-entry rules, `cnpg-pooler` and
  `cnpg-objectstore` run only the request/limit and hugepages checks, as
  `cnpg-cluster` does; resource-name validity, non-negative quantities, whole
  extended resources and hugepage divisibility are left to the API server.
- `cnpg-cluster` applies the registry allowlist to `imageName`, and a lowered
  `postgresql` always writes it (derived from `version` or authored), so since
  go-kure/launcher#281 a `postgresql` image from a registry outside the list is
  refused. When `imageName` is unset the operator takes the image from the
  catalog `imageCatalogRef` names, which this kind does not check, or,
  without one, chooses an image of its own. No allowlist can hold that one, so
  under a non-empty allowlist a Cluster naming neither is refused with the
  registry class (go-kure/launcher#790). `cnpg-pooler` does the same for the PgBouncer
  image: under a non-empty allowlist it is refused unless the template's
  `pgbouncer` container, `pgbouncer.image` or `pgbouncer.imageCatalogRef`
  names it. `postgresql` writes its `pooler.image` to `pgbouncer.image`, so an
  enabled pooler without it is refused there. The operator's own image, which
  it runs as a bootstrap init container in both kinds' pods, is not covered:
  no field names it.
  The image of a `postgresql.extensions[]` entry is held to the same list
  (go-kure/launcher#790); `postgresql` writes no such entry, so only an
  authored `cnpg-cluster` can meet that refusal. An entry without a reference
  takes its image from the catalog `imageCatalogRef` names and is not checked
  by this kind. A catalog's images are held where the catalog is authored: by
  `cnpg-imagecatalog` and `cnpg-clusterimagecatalog` for a catalog they build,
  and by nothing in this library for any other catalog.
- The images these kinds name are held to the tag rule the workload kinds
  apply to a container's image (`ValidateImageRef`: a tag or a digest, no
  `:latest`), with or without a policy (go-kure/launcher#790): `imageName` and
  each extension reference on `cnpg-cluster`; `pgbouncer.image`, the template's
  container images and its image volumes on `cnpg-pooler`; each image,
  component image and extension reference on `cnpg-imagecatalog` and
  `cnpg-clusterimagecatalog`. A field that names no image is not checked.
  `postgresql` checks the image it composes from `version` itself and names
  `version` in the refusal. A digest without a tag passes; CloudNativePG's
  webhook refuses one on `imageName`, and a tag that is no PostgreSQL version,
  and that rule stays the operator's.
- `postgresql` builds its kind properties from the same typed structs, so the
  omitted-zero refusal does not see a `0` or `false` its own parse already
  dropped; its parse decides those, as before.
- An authored empty string on an `omitempty` field with a CRD default (for
  example `primaryUpdateStrategy: ""`) is still omitted, and the operator
  applies its default; the refusal covers numbers and booleans only.
