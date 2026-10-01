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
source), alongside the built-in lowering rules: the trait-position ones
(`builtinTraitLoweringRules()` — currently just `expose`, registered via
`RegisterBuiltinTraitLowering` rather than `RegisterBuiltinTrait`) and the
component-position ones (`builtinComponentLoweringRules()` — `worker` and
`helm`, registered via `RegisterComponentLowering`). `worker` lowers to a
`deployment` component plus, unless `topologySpread: false`, a synthesized
`topology-spread` trait. `helm` lowers to a `helmrelease` (plus a generated
`helmrepository` or `ocirepository` for an inline URL, shared within the document)
or, under `delivery: template`, to a `helmtemplate`. The built-in application policy
handlers are registered too (`builtinPolicyHandlers()` — `dependency`, `placement`,
`reconciliation` and `health-checks`, registered via `RegisterPolicy`). Every registered handler and rule
declares a `PropertySchema` for its user-facing properties, so every authored
component's, trait's and policy's properties are validated against it before dispatch
(a misspelt policy key such as `prunee` fails the build). A policy type with no built-in handler —
including `app-dependency`, which orders one application after others and has nothing to
order against in a single-application build — fails with `no handler for policy type`.
Policies shape the bundle tree and its Flux settings, which `kurel build`'s manifest
output does not include; see
[Policy Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies).
See
[Component Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
and [Trait Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
for the full catalogue; the `deployment` component — the kind-named projection
of `appsv1.Deployment`, alongside the role-named `webservice` and `worker` —
the `service` component — the kind-named projection of `corev1.Service`,
emitting only a Service in front of pods another component owns —
the `job` component — a run-to-completion workload sharing the whole
JobSpec-level surface with `cronjob`'s job template — and the
`security-context` trait were added in this release. The `topology-spread`
trait (launcher's default spread constraints on any typed Deployment a
component generates, from its post-policy replica count) is registered the
same way. The `force-replace` trait (opt-in Flux force-apply, e.g. so a `job`
can be updated in place) is registered in both `builtinTraitHandlers()` and
`pkg/oam`'s trait allowlist, and `force_replace_build_test.go` builds a `job`
with and without it. The kind-named Flux source components — `helmrepository`,
`ocirepository`, `gitrepository` and `bucket`, each emitting exactly one source
CR (go-kure/launcher#347) — are registered in both `builtinComponentHandlers()`
and `pkg/oam`'s component allowlist; the `flux-sources` fixture under
`testdata/` builds all four. The `cnpg-cluster` component — the full-fidelity,
opinion-free projection of a CloudNativePG `Cluster` — is registered in
`builtinComponentHandlers()` and `pkg/oam`'s component allowlist; the
`cnpg-cluster-minimal` and `cnpg-cluster-full` fixtures build it. Its siblings
`cnpg-pooler`, `cnpg-database` and `cnpg-objectstore` (one CloudNativePG
`Pooler`, `Database` or Barman Cloud `ObjectStore` each) are registered the same
way; the `cnpg-<kind>-minimal` and `cnpg-<kind>-full` fixtures build each.

Because a lowering rule may claim types the parser would otherwise reject, `build`
constructs the transformer BEFORE parsing the Application: `newBuiltinTransformer()`
runs first, and its `LowerableTypes()` (the kinds/component-types/trait-types claimed
by its registered lowering rules — `worker`, `helm` and `expose` today) is passed into
`oam.ParseWithExtraTypes` alongside the `--capability-def`-supplied custom trait
types. See the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s
Parsing and Lowering sections for the general mechanism.

Immediately after parsing, `build` calls `ValidateAuthoredProperties`, so a component
or trait property no handler declares is a build error naming the field and the
allowed set, rather than being silently dropped (go-kure/launcher#408):

```text
Error: validating application file "app.yaml": component "web": trait "expose": properties: unsupported field "tls" (allowed: allowedGroups, allowedHostnameWildcard, annotations, authResponseHeaders, authSigninURL, authURL, certManagerClusterIssuer, controllerType, forceSslRedirect, gatewayName, gatewayNamespace, hostnames, ingressClassName, name, networkPolicy, rules, scope, secretName, serviceName, servicePort, sslRedirect)
```

The same check type-checks every declared property, so a value of the wrong YAML
type fails the build instead of being coerced. Many handlers read string
properties with a lenient type assertion that turns a non-string into `""`, which the
handler then treats as absent — so on the handler alone, `delivery: 123` on a
`helmchart` component builds with the `native` default. Through `build` it is
rejected (go-kure/launcher#325):

```text
Error: validating application file "app.yaml": component "podinfo" (type "helmchart"): properties.delivery: expected string, got int
```

In package mode this runs *after* parameter resolution, because an authored `${...}`
placeholder is a bare string until it is substituted — checking a typed property
before that point would reject a document that is correct. See the OAM model's
Property schemas section for what the check does and does not cover.

### Platform key domain

kurel derives its platform label/annotation keys under the **`launcher.gokure.dev`**
domain: the tier-override annotation is `launcher.gokure.dev/tier`, and synthesized
NetworkPolicies select pods via `launcher.gokure.dev/component`, valued at the
component's label value (the name itself at 63 characters or fewer, a projection beyond —
`ComponentLabelValue` in the OAM model). This is kurel's fixed
choice over the launcher library default (`gokure.dev`); other embedders set their own
domain through `TransformContext.Domain`. See the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the derivation
and precedence rules.

| Flag | Description |
|------|-------------|
| `--profile` | Path to the `ClusterProfile` YAML. Required unless `--environment` is set; the two are mutually exclusive. |
| `--environment` | Named environment whose profile and values stand in for `--profile`/`--values` (see [Named environments](#named-environments)). Mutually exclusive with `--profile` and `--values`. |
| `--environments` | `EnvironmentSet` file declaring the `--environment` names (default: `environments.yaml` next to `app.yaml`). Requires `--environment`. |
| `-o, --output` | Output directory (default: stdout). |
| `-n, --namespace` | Namespace override; must be a DNS-1123 label (at most 63 characters, no dots), like `metadata.namespace`. |
| `--cluster-id` | Cluster identifier (default `local`). |
| `--values` | Path to a values YAML file (requires a `kurel.yaml` package). The only flag that can supply an `array` or `object` parameter. |
| `--set key=value` | Set a parameter value (repeatable; requires `kurel.yaml`). Scalars only: an `array` or `object` parameter set this way is refused; use `--values` or the parameter's default. |
| `--capability-def` | Additional `CapabilityDefinition` file (repeatable). |
| `--strict-capabilities` | Error (instead of warn) on unvalidated custom capabilities. |
| `--oci-repository` | `oci://registry/prefix` URL: also write the Flux delivery output (below). Requires `--output`; the URL is checked as described there. |
| `--oci-tag` | Tag the generated `OCIRepository` objects pull (`spec.ref.tag`); must be a valid OCI tag. Unset leaves `spec.ref` out, so Flux pulls `latest`. Requires `--oci-repository`. |

With `--output`, the written file is named `<app.Metadata.Name>.yaml` inside that
directory. `Metadata.Name` is safe to use unescaped here because parsing already
rejects any Application whose name fails `validation.IsDNS1123Subdomain` (see the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s Parsing
section) before `build` ever reaches the write step, so the filename can't carry a
`/` or `..` path-traversal segment.

### Named environments

An environments file binds a name to a `ClusterProfile` and, optionally, a values
file, so `kurel build --environment staging` replaces a script that pairs
`--profile` with `--values` by hand (go-kure/launcher#291):

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: EnvironmentSet
metadata:
  name: my-app-environments   # optional
spec:
  environments:
  - name: staging
    profile: profiles/staging.yaml
    values: values/staging.yaml
  - name: prod
    profile: profiles/prod.yaml
    values: values/prod.yaml
```

`--environment <name>` behaves exactly as if the bound `--profile` and `--values` had
been passed: the output is byte-identical to that invocation. `profile` and `values`
are relative to the environments file's own directory, not the working directory, and
may not leave it: an absolute path, a `..` element, or a symlink whose target lies
outside that directory is an error, because the file's content (and, by default, its
location inside the package) is not the operator's own command line. Pass `--profile`/`--values` directly for a file elsewhere. Each name
must be a unique DNS-1123 label; `profile` is required, `values` is optional (an
Application without a `kurel.yaml` can still be bound to a profile, and a binding
with `values` on such an Application fails the same way `--values` does). The file is
strict-decoded and holds exactly one YAML document, so an unknown field or a trailing
`---` document is an error. An explicitly empty `--environment=` or `--environments=`
(an unset variable in a wrapper script) is also an error, never read as absent.

`--profile` or `--values` together with `--environment` is an error rather than an
override, so a build never silently mixes an environment's half with an explicit
flag. `--set` stays available and overrides the environment's values key by key, as
it overrides `--values`.

The file is a deployer input, deliberately separate from `kurel.yaml`: the package
descriptor is the package author's public API, while the profile belongs to whoever
operates the target cluster. Bindings carry no policy, constraint or limit model —
they name a profile+values pair and nothing more.

`build` collects the manifests it outputs directly from the transform result
(`collectFromNode`/`collectFromBundle`) — that collection never constructs or walks a kure
`layout.ManifestLayout`. (Only the Flux delivery output below walks one, via
`layout.WalkCluster`.) A component whose config implements the optional
`layout.LayoutAugmenter` interface fails the build outright, naming the
component, **unless** it also implements `oam.LayoutAugmentationCoverage` and
its `GenerateCoversAugmentLayout()` returns `true` — meaning its plain
`Generate` output is already a complete superset of whatever `AugmentLayout`
would otherwise add, so skipping the layout walk loses nothing. The `helmchart`
component with `valuesMode: configMap` (which needs a values `ConfigMap`
emitted alongside it) does not opt in and still fails the build; `delivery:
template` (which only repartitions `Generate`'s own flat output into hook-group
child layouts, adding no resources) does opt in and builds normally, and so does
every kind-named `helmtemplate` component, which is that same path authored
directly — see the
[Component Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
helmchart and helmtemplate sections. Any other `LayoutAugmenter` that doesn't implement
`oam.LayoutAugmentationCoverage` at all still fails closed, the same as before
this opt-out existed. The kind-named `helmrelease` component is not a
`LayoutAugmenter`: its `valuesMode: configMap` values `ConfigMap` is part of
its plain `Generate` output, so `build` emits it alongside the HelmRelease.

### Flux delivery output

With `--oci-repository`, `build -o <dir>` also writes the delivery layer of the
design's Launcher Layout (`docs/design.md` §11): one OCI artifact directory, one
`OCIRepository` and one Flux `Kustomization` per bundle.

- `<dir>/<bundle>/` — one flat directory per bundle, named by the bundle
  (reconciliation unit) name: `<app>` for a single-tier application; `<app>` (the tier
  umbrella) plus one `<app>-<tier>` per populated tier for a multi-tier one; one
  `<app>-<component>` per component, with no umbrella, for an application with a
  `dependency` policy, whatever its tiers. A tier umbrella's children are siblings of it,
  never nested inside it. Each holds `manifests.yaml`
  (exactly that bundle's objects, encoded as the stdout build encodes them) and a
  `kustomization.yaml` listing it. The tier umbrella renders no objects: its directory
  holds only a `kustomization.yaml` with `resources: []`.
- `<dir>/<app>.flux.yaml` — the `OCIRepository` and `Kustomization` of every bundle,
  generated by kure's Flux resource generator, so names, `dependsOn`, health checks and
  interval/prune/wait come from the bundle as kure renders them. Each `OCIRepository`
  pulls `<oci-repository>/<bundle>` (at `--oci-tag` when set); each `Kustomization`
  references that source and builds the artifact root, `spec.path: ./`. The tier
  umbrella's `Kustomization` is Ready only when every child `Kustomization` is: it carries
  one health check per child and no `wait` (Flux ignores health checks when `wait` is enabled).
- `<dir>/<app>.yaml` — still written, unchanged: `-o` keeps its contract.

Every object of the stdout build is in exactly one artifact, and the artifacts together
hold exactly the stdout build's objects. The layout-augmenter check above runs first, so
a component it refuses fails the build before anything is written. Output is
byte-identical across runs. A build that renders no objects still warns `no resources
generated` and writes no `<app>.yaml` (one an earlier build wrote stays), but with `--oci-repository` it writes the delivery
output: every bundle's artifact directory holds only the empty `kustomization.yaml`, and a
`manifests.yaml` an earlier build left there is removed.

A build is refused, before anything is written, when an artifact carries an object with
the API group, kind, namespace and name of one of the generated `OCIRepository` or
`Kustomization` objects — for example an `oci` component named like its bundle in an
application whose namespace is `flux-system`: reconciling that artifact would overwrite
its own source or `Kustomization`. An object inside a list counts too when reconciliation
applies it: kustomize expands a `*List` kind's `items` at any depth, then kustomize-controller
expands one more `items` array of any kind and applies its members as they are, while a
list either one expands is not applied itself. Rename the component
or the application. The same holds for one object (API group, kind, namespace and name,
where a namespace on a kind kustomize knows to be cluster-scoped is ignored, as the API
server ignores it) in two artifacts, such as `configmap` traits of one name on components in different
tiers, whose `Kustomization`s would fight over it, or twice in one artifact. An artifact
kustomize would refuse to build is refused too: one that carries two resources with one
kustomize resource id (API group, version, kind, name and namespace, where no namespace
reads as `default` and a namespace on a kind kustomize knows to be cluster-scoped is
ignored), even resources
kustomize-controller would not apply, such as two identical kustomize config
`Kustomization`s. A build is also refused when `<app>.flux.yaml` (an
application name over 245 characters) would exceed the 255-byte file name limit, when a
reconciliation unit name (its artifact directory and its `OCIRepository` and
`Kustomization` name, such as `<app>-services`) is not a DNS-1123 subdomain of at most 253
characters, and when a generated `Kustomization` or `OCIRepository` duration
is written outside Flux's duration pattern (units `ms`, `s`, `m`, `h`): durations are
written in normalized form, so a `reconciliation` policy value that normalizes to
microseconds or nanoseconds, such as `0.5ms` (written `500µs`), is refused. Use at
least `1ms`. A zero duration is written `0s` and is accepted.

The flags are checked before the build reads anything, and each bundle's url before
anything is written:

- `--oci-repository` must be `oci://<registry>[/<path>]` (a trailing `/` is ignored). The
  registry is a `host[:port]` that names itself explicitly — `localhost`, or containing
  `.` or `:` — because Flux resolves any other first segment against Docker Hub. The host
  is required: a DNS name, an IPv4 address or a bracketed IPv6 address, and a port is a
  number from 1 to 65535, so `oci://:5000/apps`, `oci://registry.example.com:/apps` or
  `oci://registry.example.com:70000/apps` is refused. The path
  is `/`-separated OCI distribution-spec components (lowercase letters and digits, joined
  by `.`, `_`, `__` or `-`), so a query, fragment, whitespace, empty segment or uppercase
  letter is refused. Each bundle's `<oci-repository>/<bundle>` must also stay within 255
  characters of path.
- `--oci-tag` must match the OCI tag grammar `[A-Za-z0-9_][A-Za-z0-9._-]{0,127}`.

Limits:

- The registry is set with the flag only; there is no `ClusterProfile` field for it.
- The Flux objects' namespace is always `flux-system`; there is no flag for it.
- `kurel` does not publish artifacts: push each `<dir>/<bundle>/` to
  `<oci-repository>/<bundle>` yourself (for example with `flux push artifact`). OCI
  publishing is Phase 5 of the design roadmap.
- `--output` is not cleared first: a directory of a bundle a later build no longer
  generates stays behind, so build into a fresh directory. (An empty artifact's stale
  `manifests.yaml` is removed.)
- An artifact holds the same flat object list the stdout build emits: a `helmtemplate`
  component's (or `helmchart` `delivery: template`'s) Helm hook groups are not split
  into ordered sub-directories.
- A namespaced object the build renders without `metadata.namespace` (for example from a
  `helmtemplate` chart, or `helmchart` with `delivery: template`) is written as is. The
  generated `Kustomization` sets no `targetNamespace`, so kustomize-controller refuses it
  ("namespace not specified"). Rendering the chart with the release namespace is tracked
  in [go-kure/launcher#602](https://github.com/go-kure/launcher/issues/602).

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

# Render the prod environment declared in ./mypackage/environments.yaml
kurel build ./mypackage --environment prod -o out/

# Also write per-bundle OCI artifact directories and their Flux objects
kurel build ./app.yaml --profile profiles/prod.yaml -o out/ \
  --oci-repository oci://registry.example.com/apps --oci-tag v1.2.0
```
