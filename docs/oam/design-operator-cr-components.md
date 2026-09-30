# Design: operator custom-resource components

Status: accepted (go-kure/launcher#281). First instance: the `cnpg-cluster`
component, a projection of the CloudNativePG `postgresql.cnpg.io/v1` `Cluster`.

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
component provides: no schema, no environment policy, no endpoints for network
policy synthesis, no health check.

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
component (go-kure/launcher#573), wired together by the author or by a semantic
component's lowering rule. One kind per component keeps a component's schema
equal to one upstream type, so the coverage test below has a single target.

### No launcher opinions on the kind

The kind component writes what the author wrote and nothing else. An unset field
is left unset so the operator's own default applies. The one exception is a
field the upstream Go type cannot leave unset: `ClusterSpec.instances` is an
`int` without `omitempty`, so an unauthored value would serialize as `0`. The
kind writes the CRD's documented default (`1`) there instead, which is the value
the API server would have applied.

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

### Policy lives on the kind

Environment policy (`oam.Policy`) is enforced by the kind component's
`ApplyPolicy`, because the kind is what emits the object the policy constrains.
A semantic component that lowers onto the kind inherits the enforcement rather
than duplicating it. For `cnpg-cluster` this is the policy `postgresql` applies
today — the instance count default and maximum, the cpu and memory request and
limit defaults and maxima, and the storage size default and maximum — plus the
checks that follow from fields the curated component never exposed: the storage
maximum on every claim the Cluster creates (`storage`, `walStorage`, each
tablespace and the ephemeral volume template) and the privileged, capability and
host-process refusals on the two security contexts.

A policy default never overrides an authored value, including an authored value
equal to what the default would be. The kind therefore records which fields were
authored before it fills any default.

### Endpoints live on the kind

The kind component implements `oam.EndpointProvider`, declaring the pods a
consumer connects to. `cnpg-cluster` declares the primary endpoint
(`cnpg.io/cluster: <component-name>` on port `5432`), the same one `postgresql`
declares, so a synthesized ingress allow does not change when a component moves
from one to the other.

### Semantic components as lowering rules

A semantic component keeps its curated authoring surface and its opinions, and
lowers onto kind components through the lowering engine (see the Lowering
Engine design). The follow-up change under
go-kure/launcher#281 does this for `postgresql`: its rule expands one
`postgresql` component into a `cnpg-cluster` (and, where enabled, the kinds from
go-kure/launcher#573), writing the image, storage fallback and update strategy as
kind properties and attaching the post-policy `enablePDB` trait. The generated
output is intended to stay identical.

## Consequences

- An author who needs a CR field the semantic component does not publish uses
  the kind component directly, and keeps policy, endpoints and health checks.
- A new upstream field is authorable as soon as the dependency is bumped and the
  coverage test is satisfied; no hand-written property parser is involved.
- The kind's schema documents the top level only. Authors read the operator's
  API reference for the deep structure; the strict decode is what catches a
  mistake there.
- Opinions live in one place each: in a semantic component's rule, or in a trait
  that rule attaches. A kind component never grows an opinion to serve one
  semantic component.

## What this does not cover

- Lowering `postgresql` onto `cnpg-cluster`, and the `enablePDB` trait: the
  follow-up change under go-kure/launcher#281.
- `Pooler`, `Database` and `ObjectStore` kind components: go-kure/launcher#573.
- The registry allowlist is not applied to `imageName` or `imageCatalogRef`,
  matching `postgresql`, which does not apply it to its image either.
