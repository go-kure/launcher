# Options: Parameter Syntax for OAM Packages

*Status: Decision required | Blocks: `design-kurel-package.md` §6, issue #36*

This document compares two options for how application-specific values (image, replicas,
domain names, etc.) are expressed in a kurel package and supplied at build time.

**Scope:** how values flow from the user into `component.properties` and
`trait.properties`. Platform profile resolution (ClusterProfile rendering) is already
decided and is out of scope here.

**Optionality and package composition** (optional traits, optional components,
multi-instance components) are a separate design axis and are not part of this decision.
See `options-package-composition.md`.

---

## Option A — `${var}` Placeholders in app.yaml

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

## Option B — values.yaml Overlay on Static app.yaml

### How it works

`kurel.yaml` is metadata-only — no parameter schema. `app.yaml` is a complete, valid
Application document with sensible defaults (or sentinel values for required fields).
A `values.yaml` file (user-supplied) deep-merges into `app.yaml`'s component and trait
properties at build time. The runtime receives the merged document.

`app.yaml` is always valid and parseable as a launcher Application — before and after
the overlay.

### kurel.yaml (metadata only)

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: webservice
  version: "1.0.0"
  description: "A stateless web service with ingress, TLS, and optional autoscaling."
spec:
  # No parameter declarations
```

### app.yaml with defaults

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
      image: "REQUIRED"     # sentinel — build fails if still present after overlay
      port: 8080
      replicas: 1           # default
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
    traits:
    - type: expose
      properties:
        rules:
        - host: "REQUIRED"
          paths:
          - path: /
            port: 8080
        tls:
        - secretName: "REQUIRED"
          hosts:
          - "REQUIRED"
    - type: certificate
      properties:
        secretName: "REQUIRED"
        dnsNames:
        - "REQUIRED"
    - type: scaler
      properties:
        minReplicas: 1
        maxReplicas: 10
        cpuUtilization: 70
```

### values.yaml

The overlay mirrors the component/trait structure of `app.yaml`. Merge is by component
name and trait type (not index):

```yaml
# values.yaml
components:
- name: web
  properties:
    image: "myregistry/app:v1.2.3"
    replicas: 3
    env:
    - name: LOG_LEVEL       # full list supplied directly — no type coercion needed
      value: info
    - name: DB_HOST
      value: postgres.svc
  traits:
  - type: expose
    properties:
      rules:
      - host: "app.example.com"
        paths:
        - path: /
          port: 8080
      tls:
      - secretName: "my-app-tls"
        hosts:
        - "app.example.com"
  - type: certificate
    properties:
      secretName: "my-app-tls"
      dnsNames:
      - "app.example.com"
```

### Supplying values at build time

```sh
# values.yaml is the only input mechanism — no --set flags
kurel build . --profile cluster.yaml --values values.yaml
```

### Merge algorithm

1. Components matched by `name`.
2. Within a component, `properties` deep-merged: user keys override package keys;
   unmentioned keys preserved.
3. Traits matched by `type`. If multiple traits of the same type exist, matched by
   index — this is a known limitation.
4. Within a trait, `properties` deep-merged the same way.
5. After merge, any property value equal to the sentinel string `"REQUIRED"` causes a
   build error listing the component, trait, and property path.

### What cannot be added via values.yaml

- A new component not present in `app.yaml`
- A new trait not present in `app.yaml`
- Conditional inclusion/exclusion of components or traits (separate design axis)

### Implications

- `app.yaml` is always a valid launcher Application document. Readable as-is; serves as
  documentation for the package's defaults and structure.
- No explicit parameter schema: consumers must read `app.yaml` to discover what can be
  overridden.
- Required field enforcement is ad-hoc: sentinel strings work for string fields, but a
  user who supplies `image: ""` passes the check and produces a broken manifest.
- Trait index ambiguity: packages with multiple traits of the same type (e.g. two
  `configmap` traits) require a deterministic resolution rule.

### Pros

- `app.yaml` is always a valid, readable Application document
- Type-safe: YAML values in `values.yaml` retain types natively
- No resolver complexity: deep-merge, no type coercion
- `app.yaml` serves as living documentation with defaults visible

### Cons

- No machine-readable parameter schema — no way to enumerate what a package accepts
  without reading app.yaml
- Required enforcement is ad-hoc (sentinel strings)
- No `--set` flags
- Values file must mirror app.yaml structure — users need to know component/trait names
- Trait-by-index matching is fragile

---

## Summary Comparison

| Aspect | Option A: Placeholders | Option B: Overlay |
|---|---|---|
| Explicit parameter schema | **Yes — in `kurel.yaml`** | No — read `app.yaml` |
| Required parameters enforced | **Yes — schema validation** | Ad-hoc (sentinel strings) |
| Parameter discovery | Single list in `kurel.yaml` | Must read `app.yaml` |
| Type safety | Requires type-aware resolver | **Native YAML** |
| Lists and maps as parameters | Yes (node substitution) | **Yes (native)** |
| `--set` flags | **Yes (scalars)** | No |
| Values file structure | By parameter name (flat/structured) | Mirrors app.yaml internals |
| Resolver complexity | Higher (type coercion + node subst.) | **Lower (deep-merge)** |
| `app.yaml` readable at rest | No (contains `${…}`) | **Yes** |
| Prototype resolver reuse | Partial | No |

Note on `app.yaml` readability: in Option A, `app.yaml` at rest contains placeholder
syntax and is not a valid Application. In Option B it is valid and readable without
tooling. This affects developer experience when browsing package source, but is not a
runtime or correctness concern — both options produce identical resolved output.
