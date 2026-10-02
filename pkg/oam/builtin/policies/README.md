# OAM Built-in Policy Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/policies.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies)

Package `policies` implements `oam.PolicyHandler` for the built-in application policy
types. A policy is an entry in an Application's `spec.policies`; it builds no resources of
its own. Its handler validates the entry and records its effect on the shared
`oam.PolicyResult`, and the transform applies that result to the cluster tree it builds:
tier overrides and dependency edges decide how components are grouped into bundles, and
health checks and reconciliation settings are set on every leaf bundle.

Handlers are registered with the transformer in `pkg/cmd/kurel` via
`RegisterPolicy(type, handler)`, from `builtinPolicyHandlers()`. A policy type with no
registered handler fails the transform with `no handler for policy type "<type>"`. Every
handler also implements `oam.PropertySchemaProvider` (`PropertySchema()`), with a
`Description` on every node, so a consumer can publish and check the property surface;
`Transformer.HandlerSchemas()` returns each registered policy's schema under `Policies`.

## Policy catalog

| `type` | Records | Properties |
|--------|---------|------------|
| `dependency` | `PolicyResult.Dependencies` | `rules[]` (required, non-empty): `component` (required), `dependsOn[]` (required, non-empty) |
| `placement` | `PolicyResult.TierOverrides` | `component` (required), `tier` (required: `infra`, `services` or `apps`) |
| `reconciliation` | `PolicyResult.ReconciliationSettings` | `interval`, `retryInterval`, `timeout`, `prune`, `wait`, `force`, `suspend` — at least one |
| `health-checks` | `PolicyResult.HealthCheckOverrides` | `checks[]` (required, non-empty): `apiVersion`, `kind`, `name` (required), `namespace` |

### `dependency`

Orders components of the same application. Every component a rule names, on either
side, must exist in the application; a component may not depend on itself; and the graph
must be acyclic. Several `dependency` policies accumulate into one graph, and the cycle
check runs over all of it, so a cycle split across two policies is still rejected. A
rejected policy records none of its edges, so the graph holds only accepted policies. A
cycle is reported as the walk that reached it, which starts from the alphabetically first
component with dependencies (`circular dependency detected: a -> b -> c -> a`), so the
same document always gives the same message.

When at least one rule is recorded, the transform builds one bundle per component and
wires each rule's `dependsOn` as bundle dependencies, on top of automatic tier edges
(each bundle depends on the bundles of the tier before it). Without a `dependency`
policy, an application spanning several tiers gets one bundle per tier instead, each
depending on the bundle of the populated tier before it.

When several `oci` components share one Flux source (the same OCI artifact),
the transform emits it once, in the bundle of the sharing
component deployed first: the one in the earliest tier, after the components it depends
on, with document order breaking ties. That component depends on no other component
sharing the source, so it never waits on a bundle that needs a source it has not yet
created.

```yaml
policies:
  - name: deploy-order
    type: dependency
    properties:
      rules:
        - component: web
          dependsOn: [api]
        - component: api
          dependsOn: [db]
```

### `placement`

Overrides the tier a component is classified into (by its type, or by the `<domain>/tier`
annotation). The tier must be one of `infra`, `services`, `apps`, and the component must
exist. A second `placement` policy for the same component is an error if it names a
different tier, rather than silently overriding the first; repeating the same tier is
accepted.

Placement changes both which tier bundle holds the component and when it is deployed:
tiers deploy in order, `infra`, then `services`, then `apps`. Without a `dependency`
policy the component joins its new tier's bundle, which depends on the bundle of the
populated tier before it. With a `dependency` policy the component's own bundle depends
on the bundles of the tier before its new one.

```yaml
policies:
  - name: cache-in-infra
    type: placement
    properties:
      component: redis-cache
      tier: infra
```

### `reconciliation`

Sets Flux Kustomization reconciliation parameters on every leaf bundle of the
application. `interval`, `retryInterval` and `timeout` must be durations Flux's
Kustomization CRD accepts (`5m`, `1h30m`): unsigned, in `ms`, `s`, `m` or `h`, so a value
such as `-5m` or `500ns` that Go would parse is still an error. The check is the one the
`oci` component uses for its `interval` (the internal
`pkg/oam/internal/fluxduration`), applied to the emitted form as well as the authored
one: the generated Kustomization carries each value as a `metav1.Duration`, which
serializes as Go's `Duration.String()`, so a value below Flux's millisecond resolution is
an error too — `0.5ms` would be emitted as `500µs`, and a positive value below a
nanosecond as `0s` (use `0s` or at least `1ms`); `prune`, `wait`, `force` and `suspend` are booleans. A boolean left out
leaves the bundle's own value unchanged rather than forcing `false`. A property given
with the wrong type (`interval: 5`, `prune: "true"`) is an error rather than ignored;
`null` reads as absent. At least one property
must be given, and at most one `reconciliation` policy is allowed per application.
`force: true` force-applies every object of the bundle, its PersistentVolumeClaims and
PersistentVolumes included: one whose immutable field changes is deleted and recreated, which
can lose its data. It stays allowed, and `Transformer.WarnForcedVolumes` (which `kurel build`
runs) warns once per such claim or volume, as it does for the `force-replace` trait's
annotation (go-kure/launcher#720; see the `pkg/oam` README).

```yaml
policies:
  - name: flux
    type: reconciliation
    properties:
      interval: 5m
      retryInterval: 1m
      prune: true
```

### `health-checks`

Appends explicit Flux health-check entries to every leaf bundle, after the ones the
transform generates for the workloads it knows how to check. `apiVersion`, `kind` and
`name` are required; `namespace` is optional and left empty when omitted or null, but a present
`namespace` that is not a string is an error rather than read as omitted. Several
`health-checks` policies accumulate in document order.

```yaml
policies:
  - name: extra-checks
    type: health-checks
    properties:
      checks:
        - apiVersion: batch/v1
          kind: Job
          name: db-migrate
          namespace: default
```

**Interaction with `wait`.** Flux's kustomize-controller ignores `spec.healthChecks` on a
Kustomization whose `spec.wait` is `true`. A document that combines a `reconciliation`
policy setting `wait: true` with a `health-checks` policy therefore builds, but the listed
checks are never evaluated — nor are the generated ones. Use one or the other.

## Validation

Validation happens in two places, and both name the policy.

`kurel build` first checks every authored policy's properties against its handler's
`PropertySchema` (`Transformer.ValidateAuthoredProperties`, after components and traits):
a key the schema does not declare (`prunee: true`), a value of the wrong type
(`prune: "true"`) or a `tier` outside its allowed values is a build error. A required
property that is left out is not reported here; the handler reports it.

Each handler then checks what it reads when the transform dispatches the policy: a
required property that is missing or empty, an empty `rules`, `dependsOn` or `checks`
list, an invalid duration,
an unknown tier or component, a self-dependency or a cycle is an error. A caller that drives
`Transform` without calling `ValidateAuthoredProperties` first gets only this second
check, in which a key the handler does not read is ignored. A wrongly typed value
the handler does read is still an error there.

## What `kurel build` shows

`kurel build` prints the objects each component generates, not the Flux Kustomizations
that would carry the bundle settings. A document with these policies builds and is
validated, but the bundle grouping, dependencies, reconciliation settings and health
checks they produce live on the `stack.Cluster` the transform returns, and appear only
where a caller renders that tree into Flux resources.

## `app-dependency` is not built in

A policy that orders one application after other applications has no meaning inside a
single-application build, which is what launcher performs: there is nothing to order the
application against, and `PolicyResult.AppDependsOn` is not read by the transform.
Registering a handler for it would accept the policy and drop it silently, so `kurel build`
keeps rejecting it with `no handler for policy type "app-dependency"`. A caller that
orchestrates several applications registers its own handler for that type and reads
`AppDependsOn` itself.

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies)
for the full exported surface.
