# Design: Policy Interface

*Status: **Final — Option A selected** | Issue #38*

| Version | Date | Summary |
|---|---|---|
| 1.1 | 2026-05-14 | Record decision (Option A); add framing section; rename options A/B; remove Option B |
| 1.0 | 2026-04-19 | Initial draft — compared typed accessor (Option A) and opaque marker (Option B) |

**Decision:** `oam.Policy` is a typed accessor interface with ~19 methods. Reason:
compile-time verification, no type assertions in handler code, and explicit `NoopPolicy`
behaviour (zero returns permit everything) are more important than the flexibility of a
marker interface for future policy types.

**Scope:** The `Policy` and `Enforceable` interface definitions in `pkg/oam`, how
`*api.EnvironmentPolicy` in crane satisfies `Policy` after migration, and how handler
code uses the interface. This does not cover `TransformContext`, handler registration,
or the pipeline execution loop.

---

## Framing

### Policy vs ClusterProfile

These are separate concerns with different owners:

- **ClusterProfile** — describes how the platform implements each trait (which ingress
  controller, which certificate issuer). Written once per cluster by a platform operator.
  Covered in `design-cluster-profile.md`.
- **Policy** — describes enforcement constraints and defaults applied to application
  components (max replicas, allowed registries, memory limits). Written per environment by
  a platform or security operator. Covered here.

The two inputs are orthogonal. A cluster profile says "ingress means Gateway API here";
a policy says "no component may request more than 2 replicas in staging". ClusterProfile
values flow into trait rendering; Policy values flow into component configuration
enforcement.

### Policy is launcher-native from day one

`kurel build` will accept a `--policy` flag pointing to a policy document. This means the
`oam.Policy` interface is a first-class launcher abstraction from Phase 1, not solely a
crane compatibility seam.

When no policy is supplied, launcher passes `NoopPolicy` — a concrete type that satisfies
`oam.Policy` and permits everything (all limits absent, all defaults absent, all security
flags false). Handlers always receive a non-nil `Policy` value; nil checks in handler code
are not needed or intended.

### crane compatibility

crane's `*api.EnvironmentPolicy` is the existing concrete policy type. After migration,
crane wires it into launcher by satisfying the `oam.Policy` interface. The question is how.

The interface must be rich enough to serve both crane's `EnvironmentPolicy` and future
launcher-native policy document types that may have different enforcement semantics.

---

## Background

### Current state in crane

```go
// crane/internal/policy/policy.go
type Enforceable interface {
    ApplyPolicy(policy *api.EnvironmentPolicy) error
}
```

Component config types (e.g. `WebserviceConfig`, `WorkerConfig`) implement this interface.
The transformer calls `ApplyPolicy` after parsing each component, passing the environment
policy from the request.

```go
// crane/pkg/api/types.go (abbreviated)
type EnvironmentPolicy struct {
    Enforced     EnforcedLimits         // MaxReplicas, MaxCPU, MaxMemory, MaxStorageSize, AllowedRegistries
    Defaults     DefaultValues          // Replicas, CPURequest, MemoryRequest, CPULimit, MemoryLimit
    Security     SecurityPolicy         // AllowHostNetwork, AllowPrivileged, AllowHostPID, AllowHostIPC, AllowHostPathVolumes
    Placement    *PlacementRules        // optional
    Capabilities *CapabilityConstraints // Allowed, Forbidden, Required
}
```

### Goal after migration

```go
// launcher/pkg/oam/policy.go
type Enforceable interface {
    ApplyPolicy(policy Policy) error
}
```

Where crane's `*api.EnvironmentPolicy` satisfies `Policy` — so that migrated handlers
compile without the import path change breaking anything beyond the type signature.

### Compatibility scope

The compatibility requirement is **behavioral**, not **zero code change**: migrated OAM
fixtures must produce identical manifest output. Handler code will be updated as part of
the migration (Phase 4 in the roadmap). The question is what shape the interface takes.

---

## Option A — Typed Accessor Interface

### Interface definition

`Policy` exposes typed getter methods corresponding to every piece of data that handlers
currently access via `*api.EnvironmentPolicy`.

```go
// launcher/pkg/oam/policy.go

// Policy provides environment-level constraints and defaults for OAM handlers.
// Handlers call its methods to apply limits and defaults; they must not type-assert.
type Policy interface {
    // Enforced limits — nil / empty string means no limit
    MaxReplicas() *int32
    MaxCPU() string
    MaxMemory() string
    MaxStorageSize() string
    AllowedRegistries() []string

    // Defaults — nil / empty string means "no default; leave OAM value as-is"
    DefaultReplicas() *int32
    DefaultCPURequest() string
    DefaultMemoryRequest() string
    DefaultCPULimit() string
    DefaultMemoryLimit() string

    // Security flags — false is the zero value (default-deny)
    AllowHostNetwork() bool
    AllowPrivileged() bool
    AllowHostPID() bool
    AllowHostIPC() bool
    AllowHostPathVolumes() bool

    // Capability constraints — nil means unconstrained
    AllowedCapabilities() []string
    ForbiddenCapabilities() []string
    RequiredCapabilities() []string
}

// Enforceable is implemented by component configs that accept policy enforcement.
type Enforceable interface {
    ApplyPolicy(policy Policy) error
}
```

### NoopPolicy

```go
// NoopPolicy is used when no policy is configured.
// All methods return nil / empty / false (permit everything, apply no defaults).
type NoopPolicy struct{}

func (*NoopPolicy) MaxReplicas() *int32          { return nil }
func (*NoopPolicy) MaxCPU() string               { return "" }
func (*NoopPolicy) MaxMemory() string            { return "" }
func (*NoopPolicy) MaxStorageSize() string       { return "" }
func (*NoopPolicy) AllowedRegistries() []string  { return nil }
func (*NoopPolicy) DefaultReplicas() *int32      { return nil }
func (*NoopPolicy) DefaultCPURequest() string    { return "" }
func (*NoopPolicy) DefaultMemoryRequest() string { return "" }
func (*NoopPolicy) DefaultCPULimit() string      { return "" }
func (*NoopPolicy) DefaultMemoryLimit() string   { return "" }
func (*NoopPolicy) AllowHostNetwork() bool       { return false }
func (*NoopPolicy) AllowPrivileged() bool        { return false }
func (*NoopPolicy) AllowHostPID() bool           { return false }
func (*NoopPolicy) AllowHostIPC() bool           { return false }
func (*NoopPolicy) AllowHostPathVolumes() bool   { return false }
func (*NoopPolicy) AllowedCapabilities() []string   { return nil }
func (*NoopPolicy) ForbiddenCapabilities() []string { return nil }
func (*NoopPolicy) RequiredCapabilities() []string  { return nil }
```

### How crane satisfies Policy

crane adds accessor methods to `*api.EnvironmentPolicy`. No adapter or wrapper struct is
needed — the existing type grows a method set:

```go
// crane/pkg/api/policy_impl.go (new file)
package api

import "github.com/go-kure/launcher/pkg/oam"

// Verify at compile time that *EnvironmentPolicy satisfies oam.Policy.
var _ oam.Policy = (*EnvironmentPolicy)(nil)

func (p *EnvironmentPolicy) MaxReplicas() *int32          { return p.Enforced.MaxReplicas }
func (p *EnvironmentPolicy) MaxCPU() string               { return p.Enforced.MaxCPU }
func (p *EnvironmentPolicy) MaxMemory() string            { return p.Enforced.MaxMemory }
func (p *EnvironmentPolicy) MaxStorageSize() string       { return p.Enforced.MaxStorageSize }
func (p *EnvironmentPolicy) AllowedRegistries() []string  { return p.Enforced.AllowedRegistries }
func (p *EnvironmentPolicy) DefaultReplicas() *int32      { return p.Defaults.Replicas }
func (p *EnvironmentPolicy) DefaultCPURequest() string    { return p.Defaults.CPURequest }
func (p *EnvironmentPolicy) DefaultMemoryRequest() string { return p.Defaults.MemoryRequest }
func (p *EnvironmentPolicy) DefaultCPULimit() string      { return p.Defaults.CPULimit }
func (p *EnvironmentPolicy) DefaultMemoryLimit() string   { return p.Defaults.MemoryLimit }
func (p *EnvironmentPolicy) AllowHostNetwork() bool       { return p.Security.AllowHostNetwork }
func (p *EnvironmentPolicy) AllowPrivileged() bool        { return p.Security.AllowPrivileged }
func (p *EnvironmentPolicy) AllowHostPID() bool           { return p.Security.AllowHostPID }
func (p *EnvironmentPolicy) AllowHostIPC() bool           { return p.Security.AllowHostIPC }
func (p *EnvironmentPolicy) AllowHostPathVolumes() bool   { return p.Security.AllowHostPathVolumes }

func (p *EnvironmentPolicy) AllowedCapabilities() []string {
    if p.Capabilities == nil { return nil }
    return p.Capabilities.Allowed
}
func (p *EnvironmentPolicy) ForbiddenCapabilities() []string {
    if p.Capabilities == nil { return nil }
    return p.Capabilities.Forbidden
}
func (p *EnvironmentPolicy) RequiredCapabilities() []string {
    if p.Capabilities == nil { return nil }
    return p.Capabilities.Required
}
```

### How handler code changes

The change is mechanical: the parameter type changes from `*api.EnvironmentPolicy` to
`oam.Policy`, and field accesses become method calls:

```go
// Before (crane):
func (c *WebserviceConfig) ApplyPolicy(p *api.EnvironmentPolicy) error {
    if p == nil { return nil }
    c.Replicas = policy.ApplyDefaultReplicas(c.Replicas, c.explicitReplicas, p.Defaults.Replicas)
    if err := policy.EnforceMaxReplicas(c.Replicas, p.Enforced.MaxReplicas); err != nil {
        return err
    }
    if err := policy.EnforceAllowedRegistries(c.Image, p.Enforced.AllowedRegistries); err != nil {
        return err
    }
    return nil
}

// After (launcher-migrated):
func (c *WebserviceConfig) ApplyPolicy(p oam.Policy) error {
    // nil check not needed — caller always passes at least NoopPolicy
    c.Replicas = policy.ApplyDefaultReplicas(c.Replicas, c.explicitReplicas, p.DefaultReplicas())
    if err := policy.EnforceMaxReplicas(c.Replicas, p.MaxReplicas()); err != nil {
        return err
    }
    if err := policy.EnforceAllowedRegistries(c.Image, p.AllowedRegistries()); err != nil {
        return err
    }
    return nil
}
```

No type assertions. No imports of crane's `api` package in handler code.

### Interface growth

If `EnvironmentPolicy` gains a new field (e.g. `MaxPodCount`), `Policy` must be explicitly
extended with a new method, and `NoopPolicy` must implement it. This is an intentional
gate — it ensures new policy fields are consciously exposed to the public interface.

The `var _ oam.Policy = (*EnvironmentPolicy)(nil)` compile check in crane's file catches
any omission immediately.

### Summary

- 19 methods (as defined above)
- crane gains ~20 accessor methods on `EnvironmentPolicy` (pure boilerplate, no logic)
- Handler code: method calls, no type assertions, no adapter imports
- Compiler verifies the contract at crane build time
- Interface must grow manually as `EnvironmentPolicy` grows

---

## Why Not an Opaque Marker Interface

The rejected alternative defined `Policy` as a single marker method (`oamPolicy()`),
with all data access via type assertions in handler code. It was rejected because:

- Handler code requires type assertions (to the adapter type or to local sub-interfaces) — no compiler verification of coverage
- `NoopPolicy` has no data methods; enforcement is silently skipped by failed assertions rather than by explicit zero-value returns — the "no policy = no constraints" behaviour is implicit, not self-documenting
- A nil pointer wrapped in the interface (`(*NoopPolicy)(nil)`) silently skips all enforcement without error
- The `Policy` interface never grows, so the adapter accumulates data silently as `EnvironmentPolicy` evolves — no compile-time gate

The explicit interface growth of the typed accessor approach (every new policy field
requires a new method and a `NoopPolicy` stub) is an intentional gate, not a burden —
it ensures new policy data is consciously exposed to the public API surface.
