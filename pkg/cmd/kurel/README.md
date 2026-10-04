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
component-position ones (`builtinComponentLoweringRules()` — `worker`,
`webservice`, `postgresql` and `helm`, registered via `RegisterComponentLowering`).
`worker` lowers to a `deployment` component plus, unless `topologySpread: false`, a
synthesized `topology-spread` trait. `webservice` lowers to a same-name `deployment`
and `service` pair, deployed as one component, with the same `topology-spread`
treatment on the `deployment`. Unless `serviceAccountName` is authored, both also
emit a same-name `serviceaccount` member for the component's ServiceAccount, and
turn each claim a `pvc` volume describes into a synthesized `pvc` trait on the
`deployment` member (go-kure/launcher#702), so `builtinComponentHandlers()` must
register `serviceaccount` and the `pvc` trait handler must be registered. `postgresql` lowers to a `cnpg-cluster`
(plus `cnpg-objectstore`, `cnpg-pooler` and one `cnpg-database` per database
when authored), with a post-policy step on the `cnpg-cluster` for the values
postgresql derives after the policy; nothing besides the rule needs registering
for it. `helm` lowers to a `helmrelease` (plus a generated
`helmrepository`, `ocirepository`, `gitrepository` or `bucket` for an inline
source, shared within the document)
or, under `delivery: template`, to a `helmtemplate`. The built-in application policy
handlers are registered too (`builtinPolicyHandlers()` — `dependency` and `placement`,
registered via `RegisterPolicy`). Every registered handler and rule
declares a `PropertySchema` for its user-facing properties, so every authored
component's, trait's and policy's properties are validated against it before dispatch
(a misspelt policy key such as `teir` fails the build). A policy type with no built-in handler —
including `app-dependency`, which orders one application after others and has nothing to
order against in a single-application build — fails with `no handler for policy type`.
The `reconciliation` and `health-checks` policies and the `fluxcd-patches` and
`fluxcd-postbuild` traits configure how Flux delivers an application, which launcher leaves
to the consumer that delivers it (go-kure/launcher#781): `build` has no handler for them, and
the `no handler for policy type` or `no handler for trait type` error says so.
Policies shape the bundle tree, which `kurel build`'s manifest
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
`ocirepository`, `gitrepository` and `bucket` (go-kure/launcher#347), and
`helmchart` (go-kure/launcher#351), each emitting exactly one source CR — are
registered in both `builtinComponentHandlers()` and `pkg/oam`'s component
allowlist; the `flux-sources` fixture under `testdata/` builds all five. The `cnpg-cluster` component — the full-fidelity,
opinion-free projection of a CloudNativePG `Cluster` — is registered in
`builtinComponentHandlers()` and `pkg/oam`'s component allowlist; the
`cnpg-cluster-minimal` and `cnpg-cluster-full` fixtures build it. Its siblings
`cnpg-pooler`, `cnpg-database` and `cnpg-objectstore` (one CloudNativePG
`Pooler`, `Database` or Barman Cloud `ObjectStore` each) are registered the same
way; the `cnpg-<kind>-minimal` and `cnpg-<kind>-full` fixtures build each. The
kind components `serviceaccount`, `persistentvolumeclaim` and `configmap`
(go-kure/launcher#702) are registered the same way. Fixtures with the same names
build each one, and the `pvc-volume-claimname` fixture mounts a
`persistentvolumeclaim` through a `pvc` volume's `claimName`. The kind
components `namespace`, `limitrange`, `resourcequota`, `persistentvolume` and
`pod` (go-kure/launcher#790) are registered the same way; the
`<type>-component` fixtures build each. The `pod` kind emits the authored spec
alone and is not one of the five pod kinds named below. The
`webservice-pvc-volumes` and `worker-pvc-volumes` fixtures pin the claims both
role components generate, byte-identical to before the `serviceaccount` member
and the synthesized `pvc` traits. The five pod kinds generate no ServiceAccount
and no claim: their fixtures emit the workload alone, with
`automountServiceAccountToken: false` on the pod when no account is authored.

Because a lowering rule may claim types the parser would otherwise reject, `build`
constructs the transformer BEFORE parsing the Application: `newBuiltinTransformer()`
runs first, and its `LowerableTypes()` (the kinds/component-types/trait-types claimed
by its registered lowering rules — `worker`, `webservice`, `postgresql`, `helm` and
`expose` today) is passed into
`oam.ParseWithExtraTypes` alongside the `--capability-def`-supplied custom trait
types. See the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s
Parsing and Lowering sections for the general mechanism.

Once the application is parsed and the profile evaluated, `build` calls
`ValidateAuthoredPropertiesWithCapabilities` with the evaluated bindings, so a
component or trait property no handler declares is a build error naming the field
and the allowed set, rather than being silently dropped (go-kure/launcher#408). The
bindings are what let a trait leave a nested required key to the capability
rendering (go-kure/launcher#765):

```text
Error: validating application file "app.yaml": component "web": trait "expose": properties: unsupported field "tls" (allowed: allowedGroups, allowedHostnameWildcard, annotations, authResponseHeaders, authSigninURL, authURL, certManagerClusterIssuer, controllerType, forceSslRedirect, gatewayName, gatewayNamespace, hostnames, ingressClassName, name, networkPolicy, rules, scope, secretName, serviceName, servicePort, sslRedirect)
```

The same check type-checks every declared property, so a value of the wrong YAML
type fails the build instead of being coerced. A handler that reads a string
property with a lenient type assertion turns a non-string into `""` and then treats
it as absent, so on such a handler alone a wrongly typed value silently becomes the
default. Through `build` it is rejected (go-kure/launcher#325):

```text
Error: validating application file "app.yaml": component "podinfo" (type "helmrelease"): properties.interval: expected string, got int
```

In package mode this runs *after* parameter resolution, because an authored `${...}`
placeholder is a bare string until it is substituted — checking a typed property
before that point would reject a document that is correct. See the OAM model's
Property schemas section for what the check does and does not cover.

### Platform key domain

kurel derives its platform label/annotation keys under the **`launcher.gokure.dev`**
domain: the tier annotation is `launcher.gokure.dev/tier`, and synthesized
NetworkPolicies select pods via `launcher.gokure.dev/component`, valued at the
component's label value (the name itself at 63 characters or fewer, a projection beyond —
`ComponentLabelValue` in the OAM model). Every object a component owns and each of its
pod templates carries that label in the build output, and a `HelmRelease` carries a
post-renderer that sets it on the chart's pod templates, so a synthesized policy selects
its component's pods as emitted (go-kure/launcher#788); a generated source shared by the
application carries none. This is kurel's fixed
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

`build` generates the manifests it outputs directly from the transform result
(`oam.GenerateApplications`), each application once, and refuses the document when two
of its applications generate the same object (API group, kind, namespace and name): a
component and another component's trait, two components, or two traits, which would each
deploy that object, the last applied winning. The error names the object and both
producers, and nothing is written (go-kure/launcher#646). That generation never
constructs or walks a kure `layout.ManifestLayout`. A component whose config implements the optional
`layout.LayoutAugmenter` interface fails the build outright, naming the
component, **unless** it also implements `oam.LayoutAugmentationCoverage` and
its `GenerateCoversAugmentLayout()` returns `true` — meaning its plain
`Generate` output is already a complete superset of whatever `AugmentLayout`
would otherwise add, so skipping the layout walk loses nothing. The `helmtemplate` component (which
only repartitions `Generate`'s own flat output into hook-group child layouts,
adding no resources) opts in and builds normally, and so does a `helm` component
under `delivery: template`, which lowers to it — see the
[Component Handlers](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
helmtemplate section. Any other `LayoutAugmenter` that doesn't implement
`oam.LayoutAugmentationCoverage` at all still fails closed, the same as before
this opt-out existed. A `helm` component under `valuesMode: configMap` is not a
`LayoutAugmenter` either: its values `ConfigMap` comes from a `configmap` trait
on the `helmrelease` it lowers to, ordinary trait output, so `build` emits it
after the HelmRelease.

### Warnings

A build that renders no objects warns `no resources generated` on stderr and writes no
`<app>.yaml` (one an earlier build wrote stays). A build also warns, on stderr and with
unchanged output, once per PersistentVolume or PersistentVolumeClaim that carries the
`force-replace` trait's annotation, because Flux then deletes and recreates it on an
immutable-field change (go-kure/launcher#720; `force_warning_test.go`).

### No delivery output

`build` writes the application's objects and nothing that delivers them. The
`--oci-repository` and `--oci-tag` flags are gone, with the per-bundle artifact directories
and the Flux `OCIRepository` and `Kustomization` objects they wrote
(go-kure/launcher#781): how an application is delivered belongs to the consumer that
delivers it, see `docs/delivery-scope.md`.

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
```
