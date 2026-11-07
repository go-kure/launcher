# The OAM model and its layers

This page explains the pieces of launcher's model and how one authored document becomes
Kubernetes objects. The reference pages hold the details; start here to learn which piece
is which.

## The documents

Every document launcher parses uses `apiVersion: launcher.gokure.dev/v1alpha1`. The one
exception is the raw-document lowering seam, where a consumer registers a rule for a kind of
its own under its own `apiVersion`; this page does not cover it (see the `pkg/oam` README).

| Kind | Who writes it | What it holds |
|---|---|---|
| `Application` | The application author | `components[]`, each with a `type` and `properties`, and `traits[]` that decorate them. |
| `ClusterProfile` | The platform operator | The choices a cluster makes: for each capability (such as `expose`), how it is rendered. |
| `CapabilityDefinition` | Whoever defines a capability | The schema of a capability's rendering: the keys a `ClusterProfile` binding for it may hold. |
| `Package` | A package author | The `kurel.yaml` descriptor: the declarations of the package's build-time parameters. It sits next to the `Application` file (`app.yaml`) in a package directory and holds no template. |

A **component** is a thing to run or provide, such as a `webservice`, a `worker` or a
`postgresql` database. A **trait** adds a platform capability to a component, such as `expose`
or `certificate`. A **policy** applies to the whole application, for example to order
components (`dependency`, `placement`). The application states what it needs; the
`ClusterProfile` states how this cluster provides it, so the same document builds on
different clusters.

## Not upstream OAM

The documents use launcher's own group, not `core.oam.dev/v1beta1`. The shape (components,
traits, properties) follows OAM, but launcher does not claim compatibility with KubeVela or
another OAM runtime, and a type name such as `webservice` or `expose` means launcher's
meaning here. The API Group & GVK page gives the reasoning.

## Two things called Application

launcher's `Application` is the authored document above. kure, the library launcher builds
objects with, has its own `Application`: one deployable unit inside its Cluster, Node and
Bundle hierarchy. One authored `Application` becomes a kure `Cluster` with one application
bundle, which has child bundles when a declared order splits the components into groups. Each
component becomes one or more kure applications that hold the generated objects. When a page
says "the application bundle", it means the kure side.

## From document to objects

The transform runs in four steps.

1. **Parse and evaluate.** launcher parses the `Application` and the `ClusterProfile`, then
   evaluates the profile's capability choices.
2. **Validate.** launcher checks the authored properties of each component and trait against
   the schema its handler or lowering rule declares, plus the properties the engine reads
   itself (`scope` on a trait; `objectName`, `labels` and `annotations` on a component whose
   type declares an object). It rejects a property that none of them lists. A type that
   declares no schema keeps its own properties unchecked; a trait's `scope` is still checked.
3. **Lower.** *Lowering rules* expand a high-level type into the lower-level types that have
   a handler. A `webservice` lowers into a `deployment` and a `service`; a `worker` into a
   `deployment`; `postgresql` into the CloudNativePG kinds; an `expose` trait into an
   `ingress` or an `httproute` trait, which one the profile's `expose` capability selects.
   Rules run until nothing is left to expand, and a lowered type may itself be lowered
   again. Four positions have rules: the document, a component, a trait and a policy.
4. **Generate.** Each terminal type has a *handler* that builds the Kubernetes object through
   kure. A caller then checks that no two applications generate an object of the same identity.

The result is an ordered kure `Cluster`; encoding it as YAML is the consumer's choice.

## One document, traced

This application has one `webservice` with an `expose` trait:

```yaml {check="build" profile="examples/cluster-profiles/minimal.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: myregistry/my-app:v1.2.3
      port: 8080
      replicas: 2
    traits:
    - type: expose
      properties:
        rules:
        - host: my-app.example.com
          paths:
          - path: /
            port: 8080
```

This profile says that on this cluster `expose` means an Ingress of class `traefik`:

```yaml {check="snippet"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: staging-minimal
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: traefik
```

| Step | What happens to `web` |
|---|---|
| Authored | Component type `webservice`, trait `expose` |
| Lowered | `webservice` becomes a `deployment` and a `service`, both named `web`; `expose` becomes an `ingress` trait, because the profile says `controllerType: ingress` |
| Generated | A `Deployment` and a `Service` named `web`, a `ServiceAccount` named `web`, and an `Ingress` named `web-ingress` |

A cluster that routes through a Gateway says so in its profile instead:

```yaml {check="snippet"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: staging-gateway
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
        gatewayName: public
```

A gateway profile needs `gatewayName` and refuses `ingressClassName`. It builds an `HTTPRoute`
where the other profile built the `Ingress`. The `rules[].host` and `paths` form above is
converted only on the ingress path: the gateway path reads the host from the top-level
`hostnames` list. So a document meant for both clusters writes `hostnames`, and the cluster's
choice alone then decides the object.

## What launcher leaves to others

launcher generates objects and stops there. It does not apply them, and it carries delivery
intent only as far as the model. The Delivery Scope page lists what the
library, the `kurel` command and a downstream consumer each own.

## Where to go next

- The library quickstart (Getting Started): the same pipeline from your own program, with the
  checks in order.
- The OAM Model reference (API Reference): types, parser and transformer.
- The Component Handlers reference: every built-in component type.
- The Lowering Engine page: how the rules run and how names stay unique.
