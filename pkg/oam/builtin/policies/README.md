# OAM Built-in Policy Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/policies.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies)

Package `policies` implements `oam.PolicyHandler` for the built-in application policy
types. A policy is an entry in an Application's `spec.policies`; it builds no resources of
its own. Its handler validates the entry and records its effect on the shared
`oam.PolicyResult`, and the transform applies that result to the cluster tree it builds:
tier overrides and dependency edges decide how components are grouped into bundles and
which bundle depends on which.

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

## Delivery policies are not built in

`reconciliation` and `health-checks` configured how Flux delivers an application: the
reconciliation parameters of its Kustomizations and extra health-check entries. Launcher
sets no Flux delivery field on the bundles it returns (go-kure/launcher#781; see
`docs/delivery-scope.md`), so it has no handler for either type and `kurel build` fails a
document using one:

```text
no handler for policy type "reconciliation": it configures delivery, which launcher leaves to the consumer that delivers the application; a consumer that delivers through Flux registers its own handler
```

A consumer that delivers through Flux registers its own handler for the type
(`RegisterPolicy`). The handler records what it read under a key it owns in
`PolicyResult.Extensions`, a `map[string]any` launcher neither reads nor changes, and the
consumer reads it back from the result `TransformWithPolicy` returns and applies it to the
delivery objects it generates.

## Validation

Validation happens in two places, and both name the policy.

`kurel build` first checks every authored policy's properties against its handler's
`PropertySchema` (`Transformer.ValidateAuthoredProperties`, after components and traits):
a key the schema does not declare (`teir: infra`), a value of the wrong type
(`component: 5`) or a `tier` outside its allowed values is a build error. A required
property that is left out is not reported here; the handler reports it.

Each handler then checks what it reads when the transform dispatches the policy: a
required property that is missing or empty, an empty `rules` or `dependsOn` list,
an unknown tier or component, a self-dependency or a cycle is an error. A caller that drives
`Transform` without calling `ValidateAuthoredProperties` first gets only this second
check, in which a key the handler does not read is ignored. A wrongly typed value
the handler does read is still an error there.

## What `kurel build` shows

`kurel build` prints the objects each component generates, not the bundles that group
them. A document with these policies builds and is validated, but the bundle grouping and
dependencies they produce live on the `stack.Cluster` the transform returns, and appear
only where a caller delivers that tree.

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
