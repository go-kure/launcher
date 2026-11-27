# Launcher architecture

A map of the code as it is on `main`. Read this first, then follow the links. It describes what exists, not
what is planned. A change that touches the Map, the flows, the rules or the boundaries below updates this
file in the same pull request.

## What this is

Launcher turns an application written as an OAM-style document (`apiVersion: launcher.gokure.dev/v1alpha1`)
into plain Kubernetes manifests. It builds the objects with the [kure](https://github.com/go-kure/kure)
library. It ships as a Go library (`pkg/oam`) and as the `kurel` command line tool. Its output is one stream
of YAML; applying or delivering that YAML is not its job.

## Map

Every directory under `pkg/` and `cmd/` that holds Go source has one row. A test keeps this table complete
(see the rules below). "Must not" is given only where the code or its documentation states it.

| Path | What it owns | Must not | More |
|---|---|---|---|
| `cmd/kurel` | The `main` of the `kurel` binary. It calls `Execute()` and nothing else. | Hold logic. | [README](cmd/kurel/README.md) |
| `pkg/cmd/kurel` | The command tree (`build`, `config`, `completion`, `version`) and the wiring of a build. It is the one place that registers the built-in handlers. | | [README](pkg/cmd/kurel/README.md) |
| `pkg/cmd/shared` | Cobra and Viper building blocks for the CLI (`InitConfig`, completion, version). | Be used as a standalone API. | [README](pkg/cmd/shared/README.md) |
| `pkg/cmd/shared/options` | The global flags (`--config`, `--verbose`, `--debug`, `--strict`, `--output-file`, `--namespace`, ...). | Be used as a standalone API. | [README](pkg/cmd/shared/options/README.md) |
| `pkg/errors` | Structured error types and wrapping helpers. | | [README](pkg/errors/README.md) |
| `pkg/oam` | The core: data model, parser, validator, lowering engine, `Transformer`, `Policy`. | Import the built-in handlers; write to stdout or stderr; exit. | [README](pkg/oam/README.md) |
| `pkg/oam/model` | Component models defined in YAML and CEL: load, compile, expand. `kurel build` loads them from a package's `models/` directory and from `--model` files. | | [README](pkg/oam/model/README.md) |
| `pkg/oam/definition` | Component types declared by a ComponentDefinition document and a CRD file it pins by digest: load, check against the CRD's structural schema, register. `kurel build` loads them from `definitions/`. | Evaluate CEL rules, apply defaults, read a CRD from anywhere but a local file. | [README](pkg/oam/definition/README.md) |
| `pkg/oam/netpol` | Shared NetworkPolicy types. They live outside `pkg/oam` so that the engine and the routing traits can both import them without a cycle. | | [README](pkg/oam/netpol/README.md) |
| `pkg/oam/builtin` | Schema types for the built-in capabilities, and strict decoding of their properties. | | [README](pkg/oam/builtin/README.md) |
| `pkg/oam/builtin/components` | The handlers of the built-in terminal component types, each building kure objects, and the lowering rules for `webservice`, `worker`, `helm`, `postgresql` and `oci`, which rewrite those types into terminal ones (the registry lists the two sets apart). | | [README](pkg/oam/builtin/components/README.md) |
| `pkg/oam/builtin/traits` | One handler per built-in trait, plus one lowering rule (`expose`). | | [README](pkg/oam/builtin/traits/README.md) |
| `pkg/oam/builtin/policies` | Handlers for the `dependency` and `placement` policies. | Build resources. Carry delivery policies (those are for a consumer to register). | [README](pkg/oam/builtin/policies/README.md) |
| `pkg/oam/builtin/registry` | Every built-in handler and lowering rule as a map. `kurel` registers exactly these. | | [README](pkg/oam/builtin/registry/README.md) |
| `pkg/oam/builtin/probe` | Probe documents and checks that compare a run's fill report with what a document triggers. | | [README](pkg/oam/builtin/probe/README.md) |
| `pkg/oam/internal/fluxduration` | The check for Flux's duration form. | Be imported outside `pkg/oam`. | [README](pkg/oam/internal/fluxduration/README.md) |
| `pkg/oam/internal/requiredfields` | The check that refuses a required field a document left out. | Be imported outside `pkg/oam`. | [README](pkg/oam/internal/requiredfields/README.md) |
| `pkg/patch` | Standalone JSONPath patching of Kubernetes resources (TOML and YAML). | Be part of `kurel build`; no other package here imports it. | [README](pkg/patch/README.md) |

Outside the code: `docs/` (design and OAM notes), `examples/` (runnable applications and cluster profiles),
`site/` (the documentation site and `docs-map.yaml`), `scripts/` (CI and release helpers).

## How data moves

**1. `kurel build`, from files to YAML.** The code is `runBuild` in `pkg/cmd/kurel/build.go`.

1. It finds `app.yaml`. If a `kurel.yaml` sits next to it, `oam.ResolveParameters` reads `app.yaml` as YAML
   and replaces `${name}` in its values (never in a mapping key) with the values from `--values` and `--set`,
   before the document is decoded as an application.
2. `newBuiltinTransformer` makes an `oam.Transformer` and registers every built-in from
   `pkg/oam/builtin/registry`.
3. `oam.ParseWithExtraTypes` parses the application. `oam.ParseClusterProfile` and
   `Transformer.EvaluateProfile` read the cluster profile.
4. `Transformer.ValidateAuthoredPropertiesForTargetWithGates` checks each authored property against the
   schema its handler declares and returns a normalised copy.
5. `Transformer.Transform` rewrites higher-level kinds into base ones (the lowering engine), asks each
   component's handler for its kure objects, applies the policies and traits, and returns a kure
   `*stack.Cluster`.
6. `oam.GenerateApplications` asks each application for its objects, and `oam.CheckInDocumentCollisions` looks
   for two of them with one identity. `runBuild` encodes the objects with kure's `pkg/io` and writes the YAML
   to stdout, to `--output <dir>`, or to the global `-f` file.

**2. A library consumer.** It does what steps 2 to 6 do, with its own additions.

1. Call the `registry` functions. Each returns a fresh map, so the caller may add, replace or delete entries.
2. Build a `Transformer` as `newBuiltinTransformer` does, and register extra handlers
   (`RegisterComponent`, `RegisterTrait`, `RegisterPolicy`) or lowering rules.
3. Put the consumer's own `oam.Policy` in `TransformContext`.
4. Call `TransformWithPolicy` for the cluster and the `PolicyResult`, or `TransformWithProvenance` for the
   fill report too.
5. Call `GenerateApplications` and `CheckInDocumentCollisions` on the result. `Transform` does not compare
   objects across applications.

**3. Parameters and environments (inputs read before step 1 above).**

1. `kurel.yaml` declares the parameters of a package.
2. Values come from `--values`; each `--set key=value` overrides the same key.
3. `oam.ResolveParameters` substitutes them into the values of `app.yaml` before it is decoded.
4. `--environment NAME` reads `environments.yaml` and names the profile and values files. A path may not
   leave the directory of that file.

## Rules the code keeps

| Rule | Where it is enforced |
|---|---|
| Launcher imports kure, and kure does not import launcher. | Review only. Go allows module requirement cycles, so the module graph does not enforce it, and the kure side is in another repository. |
| Launcher never newly leads kure on a direct dependency both use. | `site/scripts/check-kure-dep-sync.sh` (`make check-kure-dep-sync`, CI `validate`). |
| The set of built-in handlers is pinned: adding, removing or changing one is a visible test change. | `TestBuiltinRegistries_TypesUnchanged` in `pkg/cmd/kurel/builtin_registries_test.go`. |
| Every built-in handler declares a property schema. A handler a consumer registers may omit one, and the properties only that handler owns are then not checked (the engine's own checks, such as a trait's `scope`, still run). | `TestNewBuiltinTransformer_HandlerSchemaParity` in `pkg/cmd/kurel/build_test.go`. The check itself is `ValidateAuthoredPropertiesForTargetWithGates`. |
| A lowering rule is pure: no clock, environment, file, network or randomness. | `forbidigo` in `.golangci.yml`, scoped to the built-in components and traits and the `lowering*.go` files; the runtime tests `TestLoweringPurity_*` in `pkg/cmd/kurel/lowering_purity_test.go`. |
| `pkg/oam/internal` is imported only from below `pkg/oam`, and `pkg/oam` never imports the built-in handlers (they import it). | The Go compiler (the `internal` rule and the import cycle rule). |
| Code and its documentation change in one pull request; every public package is in `site/docs-map.yaml`. | CI job `doc-gate`, and `check-doc-sync` in job `docs-build`. |
| Issue references name their repository; no downstream project is named. | CI job `issue-refs`, and the forbidden-terms check in job `docs-build`. |
| Test coverage stays at or above 80 percent. | CI job `coverage-check`, which enforces it only when the coverage artifact is available: a missing artifact is recorded as a warning and the job passes. |
| Every directory under `pkg/` and `cmd/` that holds Go source has a row in the Map above, and every row names a real directory. | `TestArchitectureMapListsEveryPackage` in `architecture_map_test.go`. |
| Errors come from `pkg/errors`, not `fmt.Errorf`. | Review only. `pkg/oam/parser.go` and `pkg/oam/builtin/traits/networkpolicy_auto.go` still call `fmt.Errorf`. |
| `pkg/oam` writes nothing to stdout or stderr and does not exit. Warnings go through `Transformer.SetWarningHandler`. | Review only. (The CLI packages print and `pkg/cmd/kurel/cmd.go` exits; `pkg/patch` prints.) |

## Boundaries

- **kure.** Launcher imports `pkg/stack`, `pkg/kubernetes` and its per-family sub-packages, `pkg/io`,
  `pkg/manifest`, `pkg/stack/layout` and `pkg/stack/helm`; `pkg/patch` also imports `pkg/logger`. The bridge type is `stack.ApplicationConfig`,
  which `ComponentHandler.ToApplicationConfig` returns (`pkg/oam/handler.go`). `Transform` returns a
  `*stack.Cluster` (`pkg/oam/transform.go`).
- **The command line.** `kurel build <app.yaml|directory>` and its flags (`pkg/cmd/kurel/build.go`); the
  global flags (`pkg/cmd/shared/options`); `config`, `completion` and `version`. The `build` command's own
  `--output` names a directory, and the global `-f`/`--output-file` names one file.
- **Documents.** All use `apiVersion: launcher.gokure.dev/v1alpha1`. `Application` (`app.yaml`,
  `pkg/oam/parser.go`), `Package` (`kurel.yaml`, `pkg/oam/package.go`), `ClusterProfile`
  (`pkg/oam/profile.go`), `EnvironmentSet` (`environments.yaml`, `pkg/cmd/kurel/environment.go`),
  `CapabilityDefinition` (`pkg/oam/capability.go`), `ComponentDefinition` (`pkg/oam/definition`,
  loaded by `kurel build` from `definitions/`), and `Model` (`pkg/oam/model`; `kurel build` loads
  it through `pkg/cmd/kurel/models.go`).
- **What a consumer extends.** `ComponentHandler` and `TraitHandler` (`pkg/oam/handler.go`),
  `PolicyHandler` (`pkg/oam/pipeline.go`), the lowering rule interfaces (`pkg/oam/lowering.go`), the `Policy`
  interface (`pkg/oam/policy.go`) and `PolicyResult.Extensions`.
- **Output.** One YAML stream. No OCI artifact, and launcher wraps the application in no delivery object of its
  own. A Flux object an author writes as a component is generated like any other component
  ([docs/delivery-scope.md](docs/delivery-scope.md)).
- **Releases.** Signed `kurel` binaries from the release workflow, never from a hand-pushed tag.

## Where to start reading

1. [`docs/oam/model-and-layers.md`](docs/oam/model-and-layers.md): documents to objects, with a traced example.
2. `runBuild` and `newBuiltinTransformer` in [`pkg/cmd/kurel/build.go`](pkg/cmd/kurel/build.go): the whole
   pipeline on one screen.
3. [`pkg/oam/handler.go`](pkg/oam/handler.go) and [`pkg/oam/pipeline.go`](pkg/oam/pipeline.go): the
   interfaces a handler implements. Then `Transform` in [`pkg/oam/transform.go`](pkg/oam/transform.go).
4. [`pkg/oam/builtin/registry/registry.go`](pkg/oam/builtin/registry/registry.go): how the built-ins are
   exported and registered.
5. [`docs/design.md`](docs/design.md), sections 4 and 5: the design record. It is partly out of date; the
   code and this file win where they differ.
