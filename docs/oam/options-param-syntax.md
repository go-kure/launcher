# Options: Parameter Syntax for OAM Packages

*Status: Decision required | Blocks: `design-kurel-package.md` §6, issue #36*

This document fully designs both options for how application-specific values (image,
replicas, domain names, etc.) are expressed in a kurel package and supplied at build time.
Read both options completely before deciding.

**Scope of this decision:** how values flow from the user into OAM Application
`component.properties` and `trait.properties`. This does not cover platform profile
resolution (that is ClusterProfile rendering, already decided) or handler implementation.

---

## Option A — `${var}` Placeholders in app.yaml

### How it works

`kurel.yaml` declares a parameter schema: a list of parameters the package accepts,
their types, defaults, and whether they are required. `app.yaml` embeds `${name}`
placeholders in property values. Before the OAM Application is parsed, the runtime
resolves all placeholders by substituting parameter values supplied at build time.

The OAM runtime receives a fully-resolved Application document with no placeholders.

### kurel.yaml parameter declarations

```yaml
apiVersion: launcher.wharf.zone/v1alpha1
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
```

### app.yaml with placeholders

```yaml
apiVersion: core.oam.dev/v1beta1
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
        # issuerRef comes from ClusterProfile capability rendering
    - type: scaler
      properties:
        minReplicas: ${replicas}
        maxReplicas: 10
        cpuUtilization: 70
```

### Supplying values at build time

```sh
# Option 1: values.yaml (flat key=value map)
kurel build . --profile cluster.yaml --values values.yaml

# Option 2: --set flags
kurel build . --profile cluster.yaml \
    --set image=myregistry/app:v1.2.3 \
    --set replicas=3 \
    --set domain=app.example.com
```

```yaml
# values.yaml
image: myregistry/app:v1.2.3
replicas: 3
domain: app.example.com
# tlsSecret not set — uses default from kurel.yaml
```

### What the OAM runtime receives (after resolution)

```yaml
apiVersion: core.oam.dev/v1beta1
kind: Application
metadata:
  name: my-app
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: "myregistry/app:v1.2.3"  # substituted
      port: 8080
      replicas: 3                      # substituted, typed as integer
    traits:
    - type: expose
      properties:
        rules:
        - host: "app.example.com"      # substituted
          paths: [...]
        tls:
        - secretName: "my-app-tls"    # substituted (from default expression)
          hosts: ["app.example.com"]
    - type: certificate
      properties:
        secretName: "my-app-tls"
        dnsNames: ["app.example.com"]
```

### Type handling

String substitution produces strings. The resolver must type-coerce values to match the
declared parameter type:
- `type: integer` — `"${replicas}"` in the YAML value → resolved to YAML integer `3`
- `type: boolean` — `"${enabled}"` → resolved to YAML boolean `true` or `false`
- `type: string` — no coercion needed

This requires the resolver to understand YAML context (is `${replicas}` a scalar integer
field or embedded in a string?) and produce correct output. The existing prototype
resolver does string substitution only and would need extension for type-aware resolution.

### What cannot be parameterized

Placeholders only work in scalar positions. You cannot substitute a list, a map, or a
block. The following is not supported:

```yaml
# NOT SUPPORTED — cannot substitute an entire env list
env: "${envVars}"

# NOT SUPPORTED — cannot conditionally include a trait block
traits:
- ${maybeScalerTrait}
```

Workarounds: hard-code structural elements in app.yaml; use conditional composition
(Phase 2, issue #39) for structural variations.

### Implications

- `app.yaml` is a **template**, not a valid standalone OAM document. It cannot be
  processed by OAM tooling, validated by Kubernetes, or read by other tools without
  first running through the kurel resolver.
- The package maintainer defines the public API explicitly — consumers know exactly what
  parameters a package accepts, with types and defaults.
- Required parameters are enforced by the schema: build fails immediately if a required
  parameter is missing, with a clear error message.
- The resolver must be type-aware (see Type handling above). This is an implementation
  cost not present in Option B.

### Pros

- Explicit contract: `kurel.yaml` is the package's public API, fully machine-readable
- Enforced required parameters — clear error at build time, not runtime
- Values file is flat — no need to know app.yaml's internal structure
- `--set key=value` flags work naturally
- Follows the existing prototype resolver convention — partial reuse possible

### Cons

- `app.yaml` is not valid standalone OAM
- Type coercion adds resolver complexity (must produce integer/boolean YAML, not just strings)
- Cannot parameterize structural elements (lists, maps, conditional blocks)
- Placeholder escaping needed if property values legitimately contain `${...}`
- Package consumers cannot read `app.yaml` directly to understand what the app does without resolving

---

## Option B — values.yaml Overlay on Static app.yaml

### How it works

`kurel.yaml` is metadata-only — no parameter schema. `app.yaml` is a complete, valid OAM
Application with sensible defaults. A `values.yaml` file (user-supplied) deep-merges into
`app.yaml`'s component and trait properties at build time. The runtime receives the merged
document.

`app.yaml` is always a valid, parseable OAM Application — before and after the overlay.

### kurel.yaml (metadata only)

```yaml
apiVersion: launcher.wharf.zone/v1alpha1
kind: Package
metadata:
  name: webservice
  version: "1.0.0"
  description: "A stateless web service with ingress, TLS, and optional autoscaling."
spec:
  # No parameter declarations — app.yaml is the source of truth for defaults
```

### app.yaml with defaults

```yaml
apiVersion: core.oam.dev/v1beta1
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
      replicas: 1           # default; user overrides in values.yaml
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
    traits:
    - type: expose
      properties:
        rules:
        - host: "REQUIRED"  # sentinel
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
name and trait type (not by index):

```yaml
# values.yaml
components:
- name: web
  properties:
    image: "myregistry/app:v1.2.3"
    replicas: 3
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

There is no `--set` equivalent: to override a value you must write `values.yaml`.

### What the OAM runtime receives (after overlay)

```yaml
apiVersion: core.oam.dev/v1beta1
kind: Application
metadata:
  name: my-app
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: "myregistry/app:v1.2.3"   # from values.yaml
      port: 8080                         # from app.yaml (not overridden)
      replicas: 3                        # from values.yaml
      resources:                         # from app.yaml (not overridden)
        requests:
          cpu: "100m"
          memory: "128Mi"
    traits:
    - type: expose
      properties:
        rules:
        - host: "app.example.com"        # from values.yaml
          ...
    - type: certificate
      properties:
        secretName: "my-app-tls"         # from values.yaml
        dnsNames: ["app.example.com"]
    - type: scaler
      properties:
        minReplicas: 1                   # from app.yaml (not overridden)
        maxReplicas: 10
        cpuUtilization: 70
```

### Merge algorithm

1. Components are matched by `name` (not by index position in the list).
2. Within a component, `properties` is deep-merged: user keys override package keys;
   unmentioned package keys are preserved.
3. Traits are matched by `type`. If multiple traits of the same type exist, they are
   matched by index — this is a known limitation.
4. Within a trait, `properties` is deep-merged the same way.
5. After merge, any property value equal to the sentinel string `"REQUIRED"` causes the
   build to fail with a clear error listing the component, trait, and property path.

### What cannot be parameterized

- A user cannot **add** a new component or trait via `values.yaml` — only override
  properties of existing ones.
- A user cannot **remove** a component or trait — all components from `app.yaml` are
  always present.
- Structural variations (include scaler only in production) require a separate package
  variant, or conditional composition (Phase 2, issue #39).

```yaml
# NOT SUPPORTED — cannot add a component that is not in app.yaml
components:
- name: new-sidecar   # not in app.yaml — ignored or error
  type: worker
```

### Implications

- `app.yaml` is always a **valid standalone OAM Application**. It can be used with OAM
  tooling, validated, imported into editors, and read as documentation for what the
  package does with its defaults.
- There is no explicit parameter schema — consumers must read `app.yaml` to understand
  what values are overridable.
- "Required" enforcement relies on the sentinel convention (`"REQUIRED"`). This is
  ad-hoc: a user who sets `image: ""` passes validation but produces a broken manifest.
- Trait index ambiguity when multiple traits of the same type exist (rare but possible
  with e.g. two `configmap` traits) requires a deterministic resolution rule.

### Pros

- `app.yaml` is always valid, standalone OAM — readable by any OAM tooling, editors, validators
- Type-safe: YAML values in `values.yaml` retain their types natively (integers stay integers)
- No resolver complexity: overlay is a structural deep-merge, no type coercion
- `app.yaml` serves as living documentation — readers see the defaults without running a build
- Familiar mental model for Helm users

### Cons

- No explicit parameter schema — no machine-readable list of what a package accepts
- "Required" enforcement is ad-hoc (sentinel strings, not schema validation)
- No `--set key=value` flag — every override requires a `values.yaml` file
- Values file mirrors app.yaml's internal structure — users must know component/trait names
- Cannot remove or conditionally exclude components/traits
- Trait-by-index matching is fragile for packages with multiple traits of the same type

---

## Summary Comparison

| Aspect | Option A: Placeholders | Option B: Overlay |
|---|---|---|
| `app.yaml` is valid standalone OAM | **No** — template | **Yes** |
| Explicit parameter schema in `kurel.yaml` | **Yes** | No |
| Required parameters enforced | **Yes** — schema validation | Ad-hoc (sentinel strings) |
| Type safety | Requires type-aware resolver | **Native YAML** |
| `--set key=value` flags | **Yes** | No |
| Values file complexity | Flat map | Mirrors app.yaml structure |
| Structural parameterization | No | No |
| Resolver implementation cost | Higher (type coercion) | **Lower** (deep-merge) |
| Prototype compatibility | **Partial** (same resolver pattern) | No |
