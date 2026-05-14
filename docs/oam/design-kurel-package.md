# Design: Kurel Package Spec

*Status: Draft | Issue: [#36](https://github.com/go-kure/launcher/issues/36)*

| Version | Date | Summary |
|---|---|---|
| 1.1 | 2026-05-14 | Complete §6 (parameter syntax — Option A); fix GVK references; remove `backup` from Phase 1 trait table; fix §5 diagram label |
| 1.0 | 2026-04-19 | Initial draft — parameter syntax section omitted pending decision |

---

## 1. Purpose

A kurel package is a distributable, reusable OAM application pattern. It bundles:

- an OAM Application document (`app.yaml`) describing what workloads to run and what
  platform capabilities they need
- a package metadata file (`kurel.yaml`) with identity and parameter declarations
- optionally, example value files for common deployment scenarios

Packages are designed to be shared: a team defines a `webservice-with-ingress` package
once and any project instantiates it by supplying their image, domain, and values.

---

## 2. Package Directory Layout

```
my-app/
├── kurel.yaml        # package identity and parameter schema
├── app.yaml          # launcher Application (launcher.gokure.dev/v1alpha1)
└── examples/
    ├── production.yaml   # example values for a production deployment
    └── staging.yaml      # example values for a staging deployment
```

The OAM Application format replaces the prototype's `parameters.yaml + resources/ + patches/`
layout. No coexistence or backward-compatible bridging is required.

---

## 3. kurel.yaml

`kurel.yaml` declares the package identity and (when the parameter syntax is decided)
the parameter schema.

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: webservice        # package identifier
  version: "1.0.0"        # semver
  description: "A stateless web service with ingress, TLS, and optional autoscaling."
  # Future: home, keywords, maintainers (informational only)
spec:
  parameters:
  - name: image
    type: string
    required: true
    description: "Container image with tag, e.g. registry/app:v1.2.3"
  - name: domain
    type: string
    required: true
    description: "Primary hostname, e.g. app.example.com"
  - name: replicas
    type: integer
    required: false
    default: 1
```

---

## 4. app.yaml — OAM Application

`app.yaml` is a launcher Application document (`launcher.gokure.dev/v1alpha1`, kind
`Application`). It contains `${var}` parameter placeholders that the resolver substitutes
using the values supplied at build time. The component and trait types must match the
handler registry that the runtime is configured with. See `docs/oam/design-gvk.md` for
the GVK rationale and `docs/oam/options-param-syntax.md` for the parameter syntax spec.

### 4.1 Basic structure

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
spec:
  components:
  - name: web
    type: webservice        # must match a registered ComponentHandler
    properties:
      image: myregistry/myapp:latest
      port: 8080
      replicas: 1
    traits:
    - type: expose          # must match a registered TraitHandler
      properties:
        rules:
        - host: my-app.example.com
          paths:
          - path: /
            port: 8080
```

### 4.2 Supported component types (Phase 1)

Migrated from crane. Each type maps to a `ComponentHandler` implementation in
`pkg/oam/builtin/`:

| type | description |
|---|---|
| `webservice` | Long-running HTTP service: Deployment + Service |
| `worker` | Long-running background worker: Deployment (no Service) |
| `cronjob` | Scheduled task: CronJob |
| `postgresql` | PostgreSQL instance (CNPG) |
| `helmrelease` | FluxCD HelmRelease for third-party charts |
| `daemonset` | DaemonSet for node-level agents |
| `statefulset` | StatefulSet for ordered, persistent workloads |

### 4.3 Supported trait types (Phase 1)

Migrated from crane. Each type maps to a `TraitHandler` in `pkg/oam/builtin/`:

| type | requires capability | description |
|---|---|---|
| `expose` | yes — `controllerType` | Dispatches to `ingress` or `httproute` based on platform |
| `ingress` | no | Kubernetes Ingress |
| `httproute` | no | Gateway API HTTPRoute |
| `certificate` | yes — `issuerRef` | cert-manager Certificate |
| `external-secret` | yes — `secretStoreRef` | ExternalSecrets ExternalSecret |
| `configmap` | no | ConfigMap with optional volume mount |
| `scaler` | no | HPA + optional PDB |

Traits that remain in crane (not migrated to launcher): `backup`, `fluxcd-postbuild`,
`fluxcd-patches`, `prune-protection`, `rbac`. These depend on crane's delivery pipeline
and have no meaning in a static manifest build.

### 4.4 OAM policies

OAM Application policies are parsed and passed to the runtime unchanged. The runtime
does not interpret any policy type in Phase 1 (policy application via `Enforceable` is
wired in Phase 1 but uses `NoopPolicy` by default). Policy handling is activated in
Phase 1 via the `--policy` flag.

```yaml
spec:
  # ...
  policies:
  - name: resource-limits
    type: env-policy
    properties:
      # crane-style EnvironmentPolicy fields
      enforced:
        maxReplicas: 5
```

---

## 5. Two-Parameter-Set Model

Every kurel build receives exactly two parameter sets:

**Set 1 — Platform profile (`--profile cluster.yaml`)**

Describes how the platform implements each trait. This is an environment-level input,
supplied by the platform operator and shared across all applications on a cluster.
Represented as a `ClusterProfile` document. See `docs/oam/design-cluster-profile.md`.

**Set 2 — Application values**

Describes what this specific deployment needs: image, replica count, domain names, etc.
This is a per-deployment input, supplied by the application team at build time.

The two sets are merged at different stages:
- Platform profile rendering is merged into trait properties before handler invocation
  (capability resolution, see ClusterProfile design)
- Application values are merged into component and trait properties
  (`${var}` placeholder substitution — see §6)

### Separation of concerns

```
┌─────────────────────────────────────────────────────────────────┐
│  Application team provides:                                     │
│  - app.yaml (launcher Application — what to run, what capabilities)  │
│  - values  (image, replicas, domains — per deployment)          │
└─────────────────────┬───────────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────────┐
│  kurel build                                                    │
│  1. Resolve application values into OAM Application             │
│  2. Load ClusterProfile (platform profile)                         │
│  3. For each trait: merge capability rendering into properties     │
│  4. Dispatch to component and trait handlers                    │
│  5. Output: static Kubernetes manifests                         │
└─────────────────────┬───────────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────────┐
│  Platform operator provides:                                    │
│  - cluster.yaml (ClusterProfile — how traits are implemented)   │
└─────────────────────────────────────────────────────────────────┘
```

---

## 6. Parameter Syntax

Application values are expressed as `${name}` placeholders in `app.yaml`. The resolver
substitutes all placeholders using the values supplied at build time before the Application
is parsed or dispatched to handlers. For the full design rationale see
`docs/oam/options-param-syntax.md`.

### 6.1 Parameter declarations in kurel.yaml

Each parameter has a name, type, required flag, optional default, and optional description.

```yaml
spec:
  parameters:
  - name: image
    type: string
    required: true
    description: "Container image with tag, e.g. registry/app:v1.2.3"
  - name: replicas
    type: integer
    required: false
    default: 1
  - name: domain
    type: string
    required: true
  - name: tlsSecret
    type: string
    required: false
    default: "${name}-tls"   # may reference other parameters
  - name: env
    type: array
    required: false
    default: []
    description: "Extra environment variables as [{name, value}] objects"
```

Supported types: `string`, `integer`, `boolean`, `array`, `object`.

### 6.2 Placeholder syntax in app.yaml

```yaml
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: "${image}"          # scalar substitution — resolves to string
      replicas: ${replicas}      # scalar substitution — resolves to integer
      env: ${env}                # node substitution — resolves to YAML list
    traits:
    - type: expose
      properties:
        rules:
        - host: "${domain}"
    - type: certificate
      properties:
        secretName: "${tlsSecret}"
        dnsNames:
        - "${domain}"
```

### 6.3 Resolver behaviour

**Scalar substitution** — when `${name}` is the entire value of a YAML field, the resolver
replaces it with the typed value from the parameter declaration:
- `image: "${image}"` → `image: "myregistry/app:v1.2.3"` (string)
- `replicas: ${replicas}` → `replicas: 3` (integer, not string `"3"`)

**Node substitution** — when the parameter type is `array` or `object`, the resolver
replaces the placeholder with the full YAML node:
- `env: ${env}` → `env: [{name: LOG_LEVEL, value: info}, ...]`

**Inline string embedding** — when `${name}` is embedded inside a larger string value:
- `secretName: "${name}-tls"` → `secretName: "webservice-tls"` (always a string)

### 6.4 Supplying values at build time

```sh
# values.yaml — flat scalars or structured (for array/object parameters)
kurel build . --profile cluster.yaml --values values.yaml

# --set flags — scalars only
kurel build . --profile cluster.yaml \
    --set image=myregistry/app:v1.2.3 \
    --set replicas=3 \
    --set domain=app.example.com
```

```yaml
# values.yaml — with structured array parameter
image: myregistry/app:v1.2.3
replicas: 3
domain: app.example.com
env:
- name: LOG_LEVEL
  value: info
- name: DB_HOST
  value: postgres.svc
```

### 6.5 Validation

- Missing required parameter → build error naming the parameter before any resolution
- Optional parameter with no value → default value used
- `default` may itself contain `${name}` references to other parameters; these are
  resolved in declaration order

---

## 7. Build Invocation

```sh
kurel build <package-dir> \
    --profile cluster.yaml \
    [--values values.yaml | --set key=value]
```

Output is static Kubernetes manifests on stdout (YAML, multi-document). Pipe into
`kubectl apply`, a GitOps repo, or a CI artifact store.

The `--profile` flag is required. Without a profile, capability-aware traits will fail
with `ErrMissingCapability`.
