---
title: "Contributing"
weight: 35
---

Launcher turns an application document into plain Kubernetes manifests. You use it as the `kurel`
command line tool or as a Go library (`pkg/oam`). This section is for people who change it. Pick the
route that matches your task.

## Build, run and test

The [Development Guide](guide/) covers setup, the build, the test suite and the pre-commit checks.
It also explains how a change reaches `main`. [Releasing](releasing/) describes how a release is cut.
The CI jobs are in [GitHub Workflows](github-workflows/) and [Shared Workflows](shared-workflows/).

## Find your way around the code

Read the [architecture map](https://github.com/go-kure/launcher/blob/main/ARCHITECTURE.md) first. It
lists every package under `pkg/` and `cmd/` with what it owns, shows how data moves through
`kurel build`, and names the rules the code keeps and where each is enforced. It also says which five
files to read first.

For the details of one stage of the pipeline, use the [OAM reference](oam-reference/). For the public
API of one package, use the [API reference](../api-reference/).

## Extend launcher

| You want to add | Start here |
|---|---|
| A component type | [Adding a kind component](../api-reference/oam-components/adding-a-kind/) and [Extending](../api-reference/oam-components/extending/) |
| A trait | The [Extending section](https://github.com/go-kure/launcher/blob/main/pkg/oam/builtin/traits/README.md#extending) of the traits package |
| A policy | The [policies package](https://github.com/go-kure/launcher/blob/main/pkg/oam/builtin/policies/README.md): how a handler is registered and what it records |
| A probe row for the fill report | The [probe package](https://github.com/go-kure/launcher/blob/main/pkg/oam/builtin/probe/README.md): the checks name what a new fill needs |

A new built-in is added in its handler package and in the map in `pkg/oam/builtin/registry`. A new
terminal component type is also listed in `validComponentTypes` in `pkg/oam/validate.go`, or validation
rejects it. A test pins the set of built-ins, so adding one is a visible change. The
[Adding a kind component](../api-reference/oam-components/adding-a-kind/) page has the full checklist.

## Keep the documents in step

A change to a package updates that package's `README.md` and any page that describes it, in the same
pull request. A change that adds a package under `pkg/` or `cmd/` also adds a row to the map in
`ARCHITECTURE.md`; a test fails until it does.
