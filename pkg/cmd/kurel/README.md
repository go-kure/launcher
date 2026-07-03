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
source). Every registered handler declares a `PropertySchema` for its user-facing
properties, so a component/trait's properties can be validated before dispatch. See
[Component Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
and [Trait Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
for the full catalogue; the `security-context` trait was added in this release.

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
