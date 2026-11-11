---
title: "launcher"
---

**launcher** is a Go library that turns an OAM-style application document into the Kubernetes
objects it describes, built on [kure](https://github.com/go-kure/kure). **kurel** is the
supported command-line tool on that library. launcher stops at the generated objects: a
downstream consumer decides how they are delivered and reconciled.

## Two ways in

| You want to | Start with |
|---|---|
| Render an application document to YAML from a terminal | [Install](getting-started/install/) `kurel`, then the [Quickstart](getting-started/quickstart/) |
| Generate objects from your own Go program | [Use launcher as a Go library](getting-started/install/#use-launcher-as-a-go-library), then the [OAM Model reference](api-reference/oam/) |

## What goes in, what comes out

An application document names typed components. A `ClusterProfile` supplies the choices a
cluster makes (which ingress controller, which issuer). `kurel build` resolves the two
together:

```yaml {check="build" profile="examples/cluster-profiles/minimal.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
        port: 80
```

This one `webservice` component builds a `Deployment`, a `Service` and a `ServiceAccount`,
each named `web`, as plain Kubernetes YAML.

Tested version: CI builds this example with `kurel` from the same source revision as this
site. The latest published prerelease is `v0.2.0-beta.5`.

## Status words

Each page that states a status uses one of these four words and no other:

- **development**: the work is on `main` and no published release carries it.
- **published prerelease**: a published prerelease tag carries it, such as `v0.2.0-beta.5`.
  The API can still change before the first stable release.
- **proposed**: a design that no shipped code implements yet.
- **current supported behavior**: a stable release carries it. No release is stable yet, so no
  page uses this word today.

## What launcher does not do

launcher has no install, upgrade or registry-resolution command, and `kurel build` does not
apply anything to a cluster. The [Delivery Scope](concepts/delivery-scope/) page lists what
the library, the CLI and a downstream consumer each own, and what is not implemented.

## Where to read next

- [Getting Started](getting-started/): install, first build, runnable examples.
- [Concepts](concepts/): the design, the OAM data model and the decisions behind them.
- [API Reference](api-reference/): the public Go packages and the `kurel` command tree.
