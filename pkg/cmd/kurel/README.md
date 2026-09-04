# kurel CLI Reference

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/cmd/kurel.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/kurel)

`kurel` is an OAM-native package manager for Kubernetes. Packages are described with
a launcher Application document (`app.yaml`) and an optional parameter schema
(`kurel.yaml`); build-time parameter substitution produces static, GitOps-ready
Kubernetes manifests.

This package defines the `kurel` command tree (`NewKurelCommand`) and entry point
(`Execute`). The completion and version subcommands are provided by
[`pkg/cmd/shared`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/shared).

## Command tree

```
kurel
├── build <app.yaml|package-dir>   Build Kubernetes manifests from an OAM Application
├── config                          Manage kurel configuration
│   ├── view                        View current configuration
│   └── init                        Initialize a configuration file (.kurel/config.yaml)
├── completion [bash|zsh|fish|powershell]   Generate a shell completion script
└── version                         Print version information
```

## `kurel build`

Builds static manifests from an Application (a path to `app.yaml`, or a directory
containing `app.yaml` and optionally `kurel.yaml`) plus a platform `ClusterProfile`.
Output goes to stdout by default, or to a directory with `--output`.

All built-in component and trait handlers are registered automatically (via
`builtinComponentHandlers()` / `builtinTraitHandlers()`, the shared registration
source), alongside the built-in trait-position lowering rules
(`builtinTraitLoweringRules()` — currently just `expose`, registered via
`RegisterBuiltinTraitLowering` rather than `RegisterBuiltinTrait`). Every registered handler
declares a `PropertySchema` for its user-facing properties, so a component/trait's
properties can be validated before dispatch. See
[Component Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
and [Trait Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
for the full catalogue; the `deployment` component — the kind-named projection
of `appsv1.Deployment`, alongside the role-named `webservice` and `worker` —
the `job` component — a run-to-completion workload sharing the whole
JobSpec-level surface with `cronjob`'s job template — and the
`security-context` trait were added in this release.

Because a lowering rule may claim types the parser would otherwise reject, `build`
constructs the transformer BEFORE parsing the Application: `newBuiltinTransformer()`
runs first, and its `LowerableTypes()` (the kinds/component-types/trait-types claimed
by its registered lowering rules — just `expose` today) is passed into
`oam.ParseWithExtraTypes` alongside the `--capability-def`-supplied custom trait
types. See the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s
Parsing and Lowering sections for the general mechanism.

### Platform key domain

kurel derives its platform label/annotation keys under the **`launcher.gokure.dev`**
domain: the tier-override annotation is `launcher.gokure.dev/tier`, and synthesized
NetworkPolicies select pods via `launcher.gokure.dev/component`. This is kurel's fixed
choice over the launcher library default (`gokure.dev`); other embedders set their own
domain through `TransformContext.Domain`. See the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the derivation
and precedence rules.

| Flag | Description |
|------|-------------|
| `--profile` (required) | Path to the `ClusterProfile` YAML. |
| `-o, --output` | Output directory (default: stdout). |
| `-n, --namespace` | Namespace override. |
| `--cluster-id` | Cluster identifier (default `local`). |
| `--values` | Path to a values YAML file (requires a `kurel.yaml` package). |
| `--set key=value` | Set a parameter value (repeatable; requires `kurel.yaml`). |
| `--capability-def` | Additional `CapabilityDefinition` file (repeatable). |
| `--strict-capabilities` | Error (instead of warn) on unvalidated custom capabilities. |

With `--output`, the written file is named `<app.Metadata.Name>.yaml` inside that
directory. `Metadata.Name` is safe to use unescaped here because parsing already
rejects any Application whose name fails `validation.IsDNS1123Subdomain` (see the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s Parsing
section) before `build` ever reaches the write step, so the filename can't carry a
`/` or `..` path-traversal segment.

`build` collects objects directly from the transform result
(`collectFromNode`/`collectFromBundle`) — it never constructs or walks a kure
`layout.ManifestLayout`. A component whose config implements the optional
`layout.LayoutAugmenter` interface fails the build outright, naming the
component, **unless** it also implements `oam.LayoutAugmentationCoverage` and
its `GenerateCoversAugmentLayout()` returns `true` — meaning its plain
`Generate` output is already a complete superset of whatever `AugmentLayout`
would otherwise add, so skipping the layout walk loses nothing. The `helmchart`
component with `valuesMode: configMap` (which needs a values `ConfigMap`
emitted alongside it) does not opt in and still fails the build; `delivery:
template` (which only repartitions `Generate`'s own flat output into hook-group
child layouts, adding no resources) does opt in and builds normally — see the
[Component Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
helmchart section for both. Any other `LayoutAugmenter` that doesn't implement
`oam.LayoutAugmentationCoverage` at all still fails closed, the same as before
this opt-out existed.

## Global flags

Available on all commands (defined in [`pkg/cmd/shared/options`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/shared/options)):

| Flag | Description |
|------|-------------|
| `-c, --config` | Config file (default `$HOME/.kurel.yaml`). |
| `-v, --verbose` | Verbose output. |
| `--debug` | Debug output (implies verbose). |
| `--strict` | Treat warnings as errors. |
| `-o, --output` | Output format: `yaml`\|`json`\|`table`\|`wide`\|`name`. |
| `-f, --output-file` | Write output to a file instead of stdout. |
| `--no-headers`, `--show-labels`, `--wide` | Table-output controls. |
| `--dry-run` | Print generated resources without writing files. |
| `-n, --namespace` | Target namespace. |

## Examples

```bash
# Render to stdout using a cluster profile
kurel build ./app.yaml --profile profiles/minimal.yaml

# Render a parameterized package to a directory with overrides
kurel build ./mypackage --profile profiles/prod.yaml -o out/ \
  --values values.yaml --set replicas=3
```
