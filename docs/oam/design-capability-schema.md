# Design: Capability Rendering Schema

*Status: Final | Issue: [#60](https://github.com/go-kure/launcher/issues/60)*

| Version | Date | Summary |
|---|---|---|
| 1.1 | 2026-07-10 | §3.2: rendering vocabulary is the shared `PropertySchema` type (flat subset; rich fields rejected at decode). adr#33 |
| 1.0 | 2026-05-16 | Initial — records built-in struct pattern and custom CapabilityDefinition plan |

---

## 1. Scope

This document answers two questions deferred from `design-cluster-profile.md §2`:

1. **Built-in handlers** — where does the schema of accepted rendering keys live, and how
   is it validated?
2. **Custom capabilities** — how does a downstream consumer declare the rendering schema
   for a non-built-in handler?

**Out of scope here:**
- App-facing property schema for custom traits or components (application developer concern,
  different ownership boundary from rendering schema)
- Plugin-style external handler dispatch (Phase 4+)
- Editor integration for `cluster.yaml` (future; enabled by the JSON Schema output described
  in §2)

---

## 2. Built-in Handler Rendering Schema

### 2.1 Typed Go structs as the authoritative schema

Each built-in handler defines a typed Go struct in `pkg/oam/builtin/` that represents the
rendering keys it accepts. The struct is the single source of truth — it is both the schema
declaration and the parse target. There is no separate schema file to maintain.

Structs carry dual `yaml:` and `json:` tags so the same type serves both the YAML decoder
(`gopkg.in/yaml.v3`) and the JSON Schema generator (`github.com/invopop/jsonschema`).

```go
// pkg/oam/builtin/expose.go

// ExposeRendering defines the platform values for the expose capability.
// All fields are valid rendering keys; unknown fields are a build error.
type ExposeRendering struct {
    // ControllerType selects the ingress implementation.
    // Required. Must be "ingress" or "gateway".
    ControllerType string `yaml:"controllerType" json:"controllerType"`

    // IngressClassName is the Kubernetes IngressClass name.
    // Required when ControllerType is "ingress".
    IngressClassName string `yaml:"ingressClassName,omitempty" json:"ingressClassName,omitempty"`

    // GatewayName is the name of the Gateway API Gateway resource.
    // Required when ControllerType is "gateway".
    GatewayName string `yaml:"gatewayName,omitempty" json:"gatewayName,omitempty"`

    // GatewayNamespace is the namespace of the Gateway resource.
    // Optional when ControllerType is "gateway"; defaults to the application namespace.
    GatewayNamespace string `yaml:"gatewayNamespace,omitempty" json:"gatewayNamespace,omitempty"`
}
```

### 2.2 The `ValidateAndApplyDefaults` interface

All built-in trait handlers that accept rendering keys implement:

```go
// ValidateAndApplyDefaults is implemented by handlers whose rendering keys have a
// defined schema. The ClusterProfile loader calls it for each capability at
// profile evaluation time.
//
// The method must:
//   - Reject unknown keys (via yaml.v3 KnownFields strict decode)
//   - Apply conditional defaults in Go code for missing optional keys
//   - Return a semantic validation error for invalid value combinations
//
// The returned map replaces the original rendering map; the caller must use it.
type ValidateAndApplyDefaults interface {
    ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error)
}
```

Validation and defaulting happen in a single pass at **ClusterProfile evaluation time** —
before any Application is processed. This gives immediate feedback when `cluster.yaml` is
loaded, not when a specific trait is dispatched.

A shared `decodeStrict[T]` helper handles the strict YAML decode for all handlers:

```go
// decodeStrict decodes src into T using yaml.v3 KnownFields mode.
// Unknown keys in src produce an error.
func decodeStrict[T any](src map[string]any) (*T, error) {
    data, err := yaml.Marshal(src)
    if err != nil {
        return nil, fmt.Errorf("internal: marshal rendering: %w", err)
    }
    dec := yaml.NewDecoder(bytes.NewReader(data))
    dec.KnownFields(true)
    var out T
    if err := dec.Decode(&out); err != nil {
        return nil, err
    }
    return &out, nil
}
```

Example implementation for the expose handler:

```go
func (h *ExposeHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
    r, err := decodeStrict[ExposeRendering](rendering)
    if err != nil {
        return nil, errors.Wrap(err, "expose rendering")
    }
    if r.ControllerType == "" {
        return nil, errors.New("expose rendering: controllerType is required")
    }
    if r.ControllerType != "ingress" && r.ControllerType != "gateway" {
        return nil, errors.Errorf("expose rendering: controllerType %q must be \"ingress\" or \"gateway\"", r.ControllerType)
    }
    // Conditional defaults: only meaningful in the relevant branch
    if r.ControllerType == "gateway" && r.GatewayNamespace == "" {
        rendering["gatewayNamespace"] = "gateway-system"
    }
    return rendering, nil
}
```

**Note on conditional constraints:** Mutual exclusivity and conditional required fields
(e.g. "gatewayName is required when controllerType is gateway") are expressed as Go code
inside `ValidateAndApplyDefaults`. The struct tags themselves cannot express these
constraints. The generated JSON Schema (§2.3) is authoritative for simple required/optional
constraints and approximate for conditional ones.

**Note on defaults:** Defaults are conditional Go logic, not declarative struct annotations.
A default is only applied when the relevant condition is true. Applying a gateway default
when the controller type is ingress would be wrong; the handler's method handles this
naturally with an `if` statement.

### 2.3 JSON Schema generation

Each handler can expose a machine-readable schema derived from its rendering struct via
`github.com/invopop/jsonschema`:

```go
func (h *ExposeHandler) RenderingSchema() *jsonschema.Schema {
    return jsonschema.Reflect(&ExposeRendering{})
}
```

This schema is approximate for conditional constraints (see §2.2). It is suitable for
tooling (editor autocomplete, `kurel schema expose`) but not authoritative for validation —
validation is the handler's `ValidateAndApplyDefaults` method.

The dual `yaml:`/`json:` tag requirement exists because `invopop/jsonschema` reads `json:`
tags for field names.

### 2.4 Handlers with no rendering keys

A built-in handler that accepts no rendering keys (e.g. `configmap`, `networkpolicy`) must
still implement `ValidateAndApplyDefaults` with an empty struct:

```go
type ConfigmapRendering struct{} // no fields

func (h *ConfigmapHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
    if _, err := decodeStrict[ConfigmapRendering](rendering); err != nil {
        return nil, errors.Wrap(err, "configmap rendering")
    }
    return rendering, nil
}
```

`KnownFields(true)` on an empty struct rejects any key the operator accidentally provides.
This is a build error, not a silent pass-through — consistent with the strict-by-default
principle in `design-gvk.md`.

### 2.5 Startup assertion

Every handler that implements `CapabilityAware` (i.e. where `CapabilityRequired()` can
return `true`) must also implement `ValidateAndApplyDefaults`. This invariant is enforced by
a registry-level startup assertion: if a `CapabilityAware` handler is registered without
`ValidateAndApplyDefaults`, the binary panics at startup. This catches omissions during
development, not at runtime or in production.

### 2.6 Universal scope

The typed struct pattern and `ValidateAndApplyDefaults` interface apply uniformly across
all handler input types — not only capability rendering. The same approach governs:

- **Capability rendering** (ClusterProfile → handler, platform operator concern)
- **Trait properties** (Application → handler, application developer concern)
- **Component properties** (Application → handler, application developer concern)

All handler inputs are decoded via `decodeStrict[T]` into their respective typed structs.
The principle — typed struct as authoritative schema, strict decode, validate-and-default
in one pass — applies uniformly across all handler input types. The specific interface
methods for component and trait property validation (as distinct from capability rendering)
are follow-on design work, not specified here.

---

## 3. Custom Capability Schema

### 3.1 Schema ≠ implementation

A custom capability is a trait type implemented by a handler registered by a downstream
consumer — not one of the built-in handlers in `pkg/oam/builtin/`. This document addresses
the *schema* of custom capability rendering (what rendering keys the operator provides in
`cluster.yaml`). The *implementation* (the `TraitHandler` Go code that produces Kubernetes
manifests) is a separate concern.

Downstream consumers register custom handlers TODAY via library embedding — launcher is
designed to be consumed as a library, and consumers register additional `TraitHandler`
implementations at startup. What is deferred to Phase 4+ is plugin-style or
launcher-native external dispatch (external binaries, gRPC plugins). See
`dot-github/docs/design/kure-launcher-architecture.md` for the extension model.

A `CapabilityDefinition` document (§3.2) validates the rendering map for a registered
custom handler. Without a registered handler, the build still fails with `ErrUnknownTrait`
at dispatch — the `CapabilityDefinition` does not replace the handler.

### 3.2 CapabilityDefinition document kind (Phase 2/3)

The `CapabilityDefinition` document kind will be added in Phase 2/3. It declares the
rendering schema for a custom capability in the same vocabulary used by `kurel.yaml`
parameters (`type`, `required`, `default`, `description`).

> **Schema vocabulary (adr#33).** Internally that vocabulary is the shared `PropertySchema`
> type, restricted here to its flat subset: the rich fields (`enum`, nested `properties`,
> `items`, `additionalProperties`) are rejected at decode time, so capability rendering does
> not silently gain nested or enum semantics. The accepted wire format below is unchanged.

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: redis-sidecar    # IS the trait type — no separate spec.traitType field
spec:
  description: "Injects a Redis sidecar container alongside the main workload."
  rendering:
    properties:
      image:
        type: string
        required: true
        description: "Redis image, e.g. redis:7"
      maxMemory:
        type: string
        required: false
        default: "256Mi"
        description: "Memory limit for the Redis container"
```

**`metadata.name` is the trait type.** There is no `spec.traitType` field — following the
same convention as `Package` and `ClusterProfile`, where `metadata.name` is the primary
semantic identifier.

**Scope:** `CapabilityDefinition` covers rendering schema only — the platform-facing
`spec.capabilities.<type>.rendering` values. App-facing property schema for custom traits
(what the application developer writes in `app.yaml`) is a separate concern with a
different owner; it is not in scope for this document kind.

### 3.3 Package layout and discovery (Phase 2/3)

A package author ships `CapabilityDefinition` files in a `definitions/` directory alongside
`kurel.yaml` and `app.yaml`:

```
my-redis-app/
├── kurel.yaml
├── app.yaml
└── definitions/
    └── redis-sidecar.yaml    # CapabilityDefinition
```

`kurel build` discovers `CapabilityDefinition` files via:

1. **Auto-discovery** — any `*.yaml` file in `<package-dir>/definitions/` with
   `kind: CapabilityDefinition` is loaded automatically.
2. **Explicit flag** — `--capability-def path/to/def.yaml`; repeatable.

Built-in handlers do NOT use `CapabilityDefinition` files — their schema lives in typed Go
structs (§2). The `CapabilityDefinition` document format applies exclusively to custom
(non-built-in) handlers.

### 3.4 Validation timing and behavior

**When a `CapabilityDefinition` is found** for a given trait type: validate that capability
binding unconditionally at ClusterProfile evaluation time — before any Application
processing begins. This is consistent with the strict-by-default principle in `design-gvk.md`.

**When no `CapabilityDefinition` is found** for a custom capability that is actually used
in the current build (the trait appears in an Application being built AND a handler is
registered for it): emit a warning that rendering is passing through unvalidated.
`--strict-capabilities` upgrades this warning to a build error.

The warning is scoped to capabilities **actually dispatched** in the current build. Unused
capability entries sitting in a shared `cluster.yaml` do not trigger warnings — a
multi-cluster platform profile with entries for capabilities not used by the package being
built remains quiet.

**`ErrUnknownTrait` is a separate error** from schema warnings. If no handler is registered
for a trait type used in an Application, the build fails at dispatch regardless of whether
a `CapabilityDefinition` exists. Schema validation is only meaningful when a handler is
registered.

### 3.5 Conflict resolution

If two packages in the same build ship `CapabilityDefinition` for the same trait type:

- **Identical schemas** (same properties, types, required flags): de-duplicated silently.
- **Differing schemas**: build error naming both source files. There is no merge or
  last-writer-wins behavior.

### 3.6 Naming: CapabilityBinding vs CapabilityDefinition

The per-slot entry in `ClusterProfile.spec.capabilities` is named `CapabilityBinding` in
`pkg/oam` — not `CapabilityDefinition`. The binding is the operator's configuration
attachment (what rendering values to inject); the definition is the schema document (what
keys are accepted). These are distinct concepts with distinct Go types.

The rename from `CapabilityDefinition` → `CapabilityBinding` for the per-slot struct is
tracked in [#45](https://github.com/go-kure/launcher/issues/45) and happens there, not in
this PR.

---

## 4. What Is Deferred

| Concern | Deferred to |
|---|---|
| `CapabilityDefinition` document kind implementation | Phase 3 follow-up implementation issue ([#66](https://github.com/go-kure/launcher/issues/66)) |
| `CapabilityBinding` rename in `pkg/oam` | #45 (Phase 1) |
| App-facing property schema for custom traits | Future (schema-provider interface or separate document kinds) |
| Plugin-style external handler dispatch | Phase 4+ |
| Editor integration for `cluster.yaml` (schema publishing) | Phase 3+ (enabled by `RenderingSchema()` output) |
