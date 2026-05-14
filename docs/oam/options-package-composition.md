# Options: Package Composition — Optional Sections, Multi-Instance, Split Files

*Status: Decision required | Blocks: `design-kurel-package.md` §4–5, issue #36*

This document compares two candidate mechanisms for package composition: how a package
author marks components or traits as optional, how the user enables or disables optional
sections at build time, and how multi-instance patterns work.

**Scope:** package-level composition decisions made before the Application is resolved.
Runtime conditionality (e.g. "include this component only if another trait is present") is a
Phase 2 concern and is tracked in issue #39. That axis is out of scope here.

**Parameter syntax** (placeholder vs overlay) is a separate design axis. See
`options-param-syntax.md`. This document assumes the parameter syntax decision has been
made but is written to be compatible with either option.

---

## What needs to be designed

1. **Optional traits** — a package may include a `certificate` trait or an `external-secret`
   trait that only applies when the user wants TLS or external secret injection. The user
   must be able to leave these out without forking the package.

2. **Optional components** — a package may include a worker component, a migration job, or a
   Redis sidecar component that is not always needed.

3. **Multi-instance** — a package may define a logical component pattern (e.g. a "worker")
   that can be instantiated multiple times with different names and values
   (worker-fast, worker-slow). Can the user add instances beyond what the package declares?

4. **Split files** — can a package split `app.yaml` into per-component files
   (e.g. `components/web.yaml`, `components/worker.yaml`) to keep large packages
   manageable?

---

## Option A — `optional:` list in kurel.yaml + boolean values

### How it works

`kurel.yaml` declares an `optional` list naming components and traits that are inactive by
default. The user enables them via boolean values (a `values.yaml` key or a `--set` flag).

The resolver reads the optional list, evaluates the enable flags, and strips
inactive sections from the Application before parameter resolution and ClusterProfile
rendering proceed.

### kurel.yaml

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
  - name: domain
    type: string
    required: true
  - name: tlsEnabled
    type: boolean
    required: false
    default: false
  - name: workerEnabled
    type: boolean
    required: false
    default: false
  - name: replicas
    type: integer
    required: false
    default: 1
  optional:
  - kind: trait
    component: web
    traitType: certificate
    enabledBy: tlsEnabled
  - kind: component
    name: worker
    enabledBy: workerEnabled
```

### app.yaml

`app.yaml` contains all components and traits, including the optional ones. The resolver
strips sections that are disabled before any further processing.

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
      replicas: ${replicas}
    traits:
    - type: expose
      properties:
        rules:
        - host: "${domain}"
    - type: certificate          # optional — stripped when tlsEnabled=false
      properties:
        secretName: "${domain}-tls"
        dnsNames:
        - "${domain}"
  - name: worker                 # optional — stripped when workerEnabled=false
    type: worker
    properties:
      image: "${image}"
      replicas: 1
```

### Supplying values

With Option A param syntax:

```sh
# Default — TLS and worker disabled
kurel build . --profile cluster.yaml --set image=myregistry/app:v1 --set domain=app.example.com

# With TLS enabled
kurel build . --profile cluster.yaml \
    --set image=myregistry/app:v1 \
    --set domain=app.example.com \
    --set tlsEnabled=true
```

With Option B param syntax (values.yaml overlay):

```yaml
# values.yaml
components:
- name: web
  properties:
    image: myregistry/app:v1
    replicas: 2
```

The optional list mechanism would need to be fed via a separate top-level key:

```yaml
# values.yaml
enable:
  web.certificate: true
components:
- name: web
  properties:
    image: myregistry/app:v1
```

This is an awkward fit for Option B, because the values file structure mirrors app.yaml
but the `enable` flags reference structural paths, not property paths.

### Multi-instance

Option A does not natively support multi-instance. The package declares a fixed set of
components. If a user needs two worker instances, they either:

- Use multiple packages (one per worker configuration)
- Wait for Phase 2 conditional composition (#39)
- Use `--set` flags to parameterize a single worker's properties

### Split files

Not addressed by Option A. `app.yaml` remains a single file. Large packages must fit in one
document. Split-file support is a separate concern.

### Implications

- Optional sections are declared in `kurel.yaml`, not inline in `app.yaml` — `app.yaml`
  reads as a complete superset and the optional declarations are the gate.
- The boolean parameter mechanism integrates cleanly with the Option A param syntax
  (parameters with `type: boolean` are first-class parameter types).
- Awkward with Option B param syntax: the `enable` flags don't fit the overlay structure.
- The resolver must run optional-section stripping before parameter resolution, so that
  stripped sections do not produce "missing required parameter" errors for their properties.
- Required parameters on optional components create a problem: the user must only supply
  required values for enabled components. This requires per-component required-parameter
  evaluation, which adds resolver complexity.

### Pros

- Clear separation: optional declarations are in `kurel.yaml` (public API); `app.yaml`
  contains the full superset of what a package can produce
- Enable flags are strongly typed parameters — schema-validated, discoverable
- Integrates with `--set` flags and the Option A parameter schema
- Optional traits and components are explicit in the package's public API

### Cons

- `app.yaml` must include all optional sections; browsing it shows disabled content mixed
  with enabled content
- Poor fit with Option B param syntax
- No multi-instance support
- No split-file support
- Resolver must handle per-section parameter validation (complex)

---

## Option B — Inline annotations in app.yaml

### How it works

Optional components and traits carry a launcher annotation directly in `app.yaml`. No
`optional:` list in `kurel.yaml`. The annotation specifies the condition under which the
section is included; the user controls it via values or `--set` flags.

The annotation uses a launcher-reserved key in the component or trait block.

### kurel.yaml

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
  - name: domain
    type: string
    required: true
  - name: tls
    type: boolean
    required: false
    default: false
  - name: worker
    type: boolean
    required: false
    default: false
  - name: replicas
    type: integer
    required: false
    default: 1
```

### app.yaml with inline annotations

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
      replicas: ${replicas}
    traits:
    - type: expose
      properties:
        rules:
        - host: "${domain}"
    - type: certificate
      launcher.gokure.dev/include-if: "${tls}"  # inline gate
      properties:
        secretName: "${domain}-tls"
        dnsNames:
        - "${domain}"
  - name: worker
    type: worker
    launcher.gokure.dev/include-if: "${worker}" # inline gate
    properties:
      image: "${image}"
      replicas: 1
```

`launcher.gokure.dev/include-if` is a launcher-reserved field. Its value is a parameter
reference that resolves to a boolean. The resolver evaluates these gates after parameter
substitution and strips sections where the gate evaluates to false before ClusterProfile
rendering.

### Implications

- `app.yaml` remains the single source of truth; the gate is co-located with the section
  it controls.
- The launcher API group prefix on `include-if` signals that this is not an Application
  property but a resolver directive — it must be explicitly excluded from strict parsing
  of the Application document body.
- The same `${...}` substitution machinery (Option A param syntax) is required to evaluate
  the gate expression. This option is incompatible with Option B param syntax as specified
  because `${...}` placeholders are not used there.

### Strict-parsing interaction

`launcher.gokure.dev/include-if` is a meta-directive on a component or trait, not a
property value. Under the strict parsing rule (see `design-gvk.md`), the Application parser
must either:

- Explicitly whitelist `launcher.gokure.dev/*` annotation keys on components and traits, or
- Process these directives before the Application document is parsed against the Application
  schema, removing them first

This adds a pre-processing step before the schema-validated parse.

### Multi-instance

Not addressed by this option either. The package still declares a fixed set of named
components.

### Split files

Could be combined with a split-file mechanism (each component file carries its own
`include-if`), but this is independent of the annotation mechanism.

### Pros

- Gate co-located with the section it controls — easier to read for package authors
- `kurel.yaml` stays clean; no parallel `optional:` list to keep in sync with `app.yaml`
- A future extension (`include-if` with richer expressions) follows naturally from the
  same mechanism

### Cons

- `app.yaml` contains non-Application syntax (resolver directives) — technically not a
  valid Application document even before parameter resolution
- Strict-parsing interaction adds a pre-processing requirement
- Only compatible with Option A param syntax (requires `${...}` evaluation)
- `kurel.yaml` no longer enumerates the enable/disable surface of the package — consumers
  must read `app.yaml` to discover what can be toggled
- The annotation key prefix is verbose; alternatives (`x-include-if`, `_if`) trade clarity
  for brevity

---

## Open Questions

These questions are not answered here and must be resolved before `design-kurel-package.md`
§4–5 can be written:

**Q1 — Where does optionality belong?**
Is optional section declaration:
- Package metadata (`kurel.yaml` — Option A)
- Inline in `app.yaml` (`include-if` annotation — Option B)
- Both (annotations in `app.yaml` are the mechanism; `kurel.yaml` enumerates them for
  discoverability)?
- Deferred entirely to Phase 2 conditional composition (issue #39)?

If optionality is deferred, Phase 1 packages have no optional sections. Users compose via
multiple packages or via a fixed superset (all sections always present).

**Q2 — Per-section required parameters**
If optional components or traits have required parameters, how are they validated? Options:
- Required-if: parameter is only required when the section is enabled
- Explicit group: parameters belong to a section; the group is validated or skipped
  together
- User responsibility: no framework validation; missing parameters fail at render time with
  a property-level error

**Q3 — Multi-instance scope**
Is multi-instance a Phase 1 concern? The use case (multiple worker instances from one
component pattern) exists in crane. Options:
- Not Phase 1: users instantiate the same package multiple times with different names
- Phase 1: package declares a component pattern; `values.yaml` provides a list of instances
- Phase 2 (issue #39): conditional composition handles this

**Q4 — Split files**
Should `app.yaml` be splittable into per-component files in Phase 1? This is a package
organization concern independent of optionality. It is not needed for correctness but
affects developer experience for large packages.

---

## Summary Comparison

| Aspect | Option A: kurel.yaml optional list | Option B: Inline annotation |
|---|---|---|
| Gate location | `kurel.yaml` — explicit public API | `app.yaml` co-located with section |
| `app.yaml` is valid Application at rest | Yes (all sections present, some inactive) | No (contains resolver directives) |
| `kurel.yaml` enumerates enable surface | **Yes** | No — must read `app.yaml` |
| Param syntax compatibility | **Both A and B** | Option A only |
| Strict-parsing interaction | None | Requires pre-processing step |
| Multi-instance support | None | None |
| Split-file support | None | None |
| Resolver complexity | Medium (per-section param validation) | Medium (pre-processing + gate eval) |
