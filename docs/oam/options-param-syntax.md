# Design: Parameter Syntax for Kurel Packages

*Status: **Final — Option A selected** | Issue #36*

| Version | Date | Summary |
|---|---|---|
| 1.1 | 2026-05-14 | Record decision (Option A); remove Option B; add resolver behaviour section; correct env list claim |
| 1.0 | 2026-04-19 | Initial draft — compared Option A (placeholders) and Option B (overlay) |

**Decision:** Application values are expressed as `${var}` placeholders in `app.yaml`,
with a typed parameter schema declared in `kurel.yaml`. Reason: launcher is a package
manager; an explicit, machine-readable package API (schema, required fields, types,
`--set` support) is more important than `app.yaml` being valid at rest.

**Scope:** how values flow from the user into `component.properties` and
`trait.properties`. Platform profile resolution (ClusterProfile rendering) is already
decided and is out of scope here.

**Optionality and package composition** (optional traits, optional components,
multi-instance components) are deferred to Phase 2 (issue #39).
See `options-package-composition.md`.

---

## Parameter Syntax — `${var}` Placeholders

### How it works

`kurel.yaml` declares a parameter schema: a list of parameters the package accepts,
with types, defaults, and required flags. `app.yaml` embeds `${name}` placeholders in
property values. Before the Application is parsed, the runtime resolves all placeholders
by substituting the parameter values supplied at build time.

The runtime receives a fully-resolved Application document with no placeholders.

### kurel.yaml parameter declarations

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: webservice
  version: "1.0.0"
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
    description: "Primary hostname, e.g. app.example.com"
  - name: tlsSecret
    type: string
    required: false
    default: "${name}-tls"   # can reference other parameters
  - name: env
    type: array
    required: false
    default: []
    description: "Extra environment variables as a list of {name, value} objects"
```

### app.yaml with placeholders

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: "${image}"
      port: 8080
      replicas: ${replicas}
      env: ${env}            # node substitution — resolved to a YAML list
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
    traits:
    - type: expose
      properties:
        rules:
        - host: "${domain}"
          paths:
          - path: /
            port: 8080
        tls:
        - secretName: "${tlsSecret}"
          hosts:
          - "${domain}"
    - type: certificate
      properties:
        secretName: "${tlsSecret}"
        dnsNames:
        - "${domain}"
    - type: scaler
      properties:
        minReplicas: ${replicas}
        maxReplicas: 10
        cpuUtilization: 70
```

### Supplying values at build time

Values can be supplied as a file or via `--set` flags. The values file can be flat or
structured — the parameter names in `kurel.yaml` are the keys:

```sh
# Option 1: values.yaml
kurel build . --profile cluster.yaml --values values.yaml

# Option 2: --set flags (scalars only)
kurel build . --profile cluster.yaml \
    --set image=myregistry/app:v1.2.3 \
    --set replicas=3 \
    --set domain=app.example.com
```

```yaml
# values.yaml — flat scalars
image: myregistry/app:v1.2.3
replicas: 3
domain: app.example.com
# tlsSecret not set — uses default from kurel.yaml

# values.yaml — with structured parameter (array type)
image: myregistry/app:v1.2.3
replicas: 3
domain: app.example.com
env:
- name: LOG_LEVEL
  value: info
- name: DB_HOST
  value: postgres.svc
```

### Resolver behaviour: scalars vs nodes

**Scalar substitution** — when the placeholder is the entire YAML value for a scalar
field, the resolver substitutes the typed value directly:
- `image: "${image}"` → `image: "myregistry/app:v1.2.3"` (string)
- `replicas: ${replicas}` → `replicas: 3` (integer, not string)
- The declared parameter `type` drives coercion; `type: integer` produces a YAML integer

**Node substitution** — when the parameter type is `array` or `object`, the resolver
replaces the placeholder with the full YAML node from values.yaml:
- `env: ${env}` → `env: [{name: LOG_LEVEL, value: info}, ...]` (list)
- Requires a YAML-node-level resolver (not plain string replacement)
- The existing prototype resolver does string substitution only; node substitution would
  be an extension

**Inline string embedding** — when the placeholder is embedded in a larger string:
- `name: "prefix-${name}-suffix"` → `name: "prefix-webservice-suffix"`
- Produces a string; not type-coerced

### What cannot be parameterized

- Conditional inclusion of entire traits or components (separate design axis — see
  `options-package-composition.md`)
- Template-level structural variation (e.g. "repeat this trait N times")

### Implications

- `app.yaml` is a template: it contains unresolved `${…}` and cannot be used directly
  as a valid launcher Application without running through the resolver first.
- `kurel.yaml` is the explicit public API of the package: consumers see all parameter
  names, types, defaults, and descriptions.
- Required parameters are schema-enforced: build fails immediately with a clear error if
  any required parameter is missing.
- Node substitution for `array`/`object` parameters adds resolver implementation cost.

### Pros

- Explicit contract: `kurel.yaml` parameter list is the package's public API
- Enforced required parameters — schema validation, not sentinel strings
- Values file is flat/structured by parameter name — no need to know app.yaml internals
- `--set` flags work for scalar parameters
- Type declarations make parameter intent clear

### Cons

- `app.yaml` is not a valid launcher Application document at rest (template)
- Type-aware resolver required (beyond simple string substitution)
- Node substitution adds implementation complexity
- Placeholder escaping needed if property values legitimately contain `${…}`

---

## Why Not values.yaml Overlay

The rejected alternative had `app.yaml` as a static document with sentinel strings
(`"REQUIRED"`) for mandatory fields, and a `values.yaml` that deep-merges into the
component/trait structure at build time. It was rejected because:

- No machine-readable parameter schema — consumers must read `app.yaml` to discover what can be overridden
- Required field enforcement is ad-hoc: sentinel strings work for string fields, but `image: ""` passes the check and produces a broken manifest
- No `--set` flags — scalars cannot be supplied on the command line
- Values file must mirror `app.yaml` structure — users need to know component and trait names
- Trait-by-index matching is fragile for packages with multiple traits of the same type
