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
`Description` on every node, so a consumer can publish and check the property surface.

## Policy catalog

| `type` | Records | Properties |
|--------|---------|------------|
| `dependency` | `PolicyResult.Dependencies` | `rules[]` (required): `component` (required), `dependsOn[]` (required) |
| `placement` | `PolicyResult.TierOverrides` | `component` (required), `tier` (required: `infra`, `services` or `apps`) |
| `reconciliation` | `PolicyResult.ReconciliationSettings` | `interval`, `retryInterval`, `timeout`, `prune`, `wait`, `force`, `suspend` — at least one |
| `health-checks` | `PolicyResult.HealthCheckOverrides` | `checks[]` (required, non-empty): `apiVersion`, `kind`, `name` (required), `namespace` |

### `dependency`

Orders components of the same application. Every component a rule names, on either
side, must exist in the application; a component may not depend on itself; and the graph
must be acyclic. Several `dependency` policies accumulate into one graph, and the cycle
check runs over all of it, so a cycle split across two policies is still rejected. A
cycle is reported as the walk that reached it, which starts from the alphabetically first
component with dependencies (`circular dependency detected: a -> b -> c -> a`), so the
same document always gives the same message.

When at least one rule is recorded, the transform builds one bundle per component and
wires each rule's `dependsOn` as bundle dependencies, on top of the automatic tier
ordering (each bundle depends on the bundles of the tier before it).

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
exist.

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
application. `interval`, `retryInterval` and `timeout` must parse as Go durations
(`5m`, `1h30m`); `prune`, `wait`, `force` and `suspend` are booleans. A boolean left out
leaves the bundle's own value unchanged rather than forcing `false`. At least one property
must be given, and at most one `reconciliation` policy is allowed per application.

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
`name` are required; `namespace` is optional and left empty when omitted. Several
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

Each handler checks the properties it reads when the transform dispatches the policy: a
required property that is missing, empty or of the wrong type, an empty `checks` list,
an invalid duration, an unknown tier or component, a self-dependency or a cycle is an
error naming the policy. Two things are not checked. A key the handler does not read is
ignored, not rejected. And a `reconciliation` property of the wrong type (`prune: "true"`)
is skipped as if absent — an error only when nothing usable remains. `kurel build`'s
authored-property check (`ValidateAuthoredProperties`) covers components and traits but
not policies, so the declared `PropertySchema` is not applied to an authored policy; it is
applied to a policy a lowering rule emits.

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
