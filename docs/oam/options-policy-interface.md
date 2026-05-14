# Options: Policy Interface Design

*Status: Decision required | Blocks: `design-policy-interface.md`, issue #38*

This document fully designs both options for the `oam.Policy` interface.
Read both options completely before deciding.

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

## Option B — Opaque Marker Interface

### Interface definition

`Policy` is a marker interface: it identifies an object as a policy but exposes no data.
All data access happens through crane-controlled types or local sub-interfaces.

```go
// launcher/pkg/oam/policy.go

// Policy marks an object as an OAM policy carrier.
// It carries no data methods; handlers access policy data through type assertions
// or sub-interfaces defined locally in their own package.
type Policy interface {
    // unexported marker — prevents accidental satisfaction outside this module
    oamPolicy()
}

// Enforceable is implemented by component configs that accept policy enforcement.
type Enforceable interface {
    ApplyPolicy(policy Policy) error
}
```

### NoopPolicy

```go
// NoopPolicy is used when no policy is configured.
// It satisfies Policy; all type assertions against it return false,
// so handlers that assert to access limits/defaults effectively skip enforcement.
type NoopPolicy struct{}

func (*NoopPolicy) oamPolicy() {} // satisfies Policy; no data methods
```

### How crane satisfies Policy

crane wraps `*api.EnvironmentPolicy` in an adapter struct. The adapter satisfies
`oam.Policy` and also exposes the data methods that crane's own handlers will assert to.

```go
// crane/internal/policy/oam_adapter.go (new file)
package policy

import (
    "github.com/go-kure/launcher/pkg/oam"
    "gitlab.com/autops/wharf/crane/pkg/api"
)

// OAMPolicyAdapter wraps *api.EnvironmentPolicy and satisfies oam.Policy.
type OAMPolicyAdapter struct {
    env *api.EnvironmentPolicy
}

// NewOAMPolicy returns an oam.Policy for the given environment policy.
// Returns oam.NoopPolicy when env is nil.
func NewOAMPolicy(env *api.EnvironmentPolicy) oam.Policy {
    if env == nil {
        return &oam.NoopPolicy{}
    }
    return &OAMPolicyAdapter{env: env}
}

func (a *OAMPolicyAdapter) oamPolicy() {} // satisfies oam.Policy

// Data accessors — used by crane handlers that type-assert to this type or to sub-interfaces
func (a *OAMPolicyAdapter) MaxReplicas() *int32          { return a.env.Enforced.MaxReplicas }
func (a *OAMPolicyAdapter) MaxCPU() string               { return a.env.Enforced.MaxCPU }
func (a *OAMPolicyAdapter) MaxMemory() string            { return a.env.Enforced.MaxMemory }
func (a *OAMPolicyAdapter) MaxStorageSize() string       { return a.env.Enforced.MaxStorageSize }
func (a *OAMPolicyAdapter) AllowedRegistries() []string  { return a.env.Enforced.AllowedRegistries }
func (a *OAMPolicyAdapter) DefaultReplicas() *int32      { return a.env.Defaults.Replicas }
// ... (same accessor set as Option A, but on the adapter, not on EnvironmentPolicy)
```

### How handler code changes — two sub-options

crane's handlers receive `oam.Policy` and must extract data. There are two ways to do this:

**Sub-option B1: Assert to the concrete adapter**

```go
// crane handler — asserts directly to crane's own adapter type
func (c *WebserviceConfig) ApplyPolicy(p oam.Policy) error {
    ep, ok := p.(*cranePolicy.OAMPolicyAdapter)
    if !ok {
        return nil // NoopPolicy or unknown policy type — skip enforcement
    }
    c.Replicas = policy.ApplyDefaultReplicas(c.Replicas, c.explicitReplicas, ep.DefaultReplicas())
    if err := policy.EnforceMaxReplicas(c.Replicas, ep.MaxReplicas()); err != nil {
        return err
    }
    return nil
}
```

Consequence: the handler file gains an import of crane's own `cranePolicy` package to
name the adapter type. The handler is still tightly coupled to crane's concrete types —
just through an extra indirection layer.

**Sub-option B2: Assert to local sub-interfaces**

Each handler defines a minimal sub-interface for the policy data it needs:

```go
// crane handler — asserts to a local sub-interface
func (c *WebserviceConfig) ApplyPolicy(p oam.Policy) error {
    type replicaLimiter interface {
        MaxReplicas() *int32
        DefaultReplicas() *int32
    }
    type registryLimiter interface {
        AllowedRegistries() []string
    }

    if rl, ok := p.(replicaLimiter); ok {
        c.Replicas = policy.ApplyDefaultReplicas(c.Replicas, c.explicitReplicas, rl.DefaultReplicas())
        if err := policy.EnforceMaxReplicas(c.Replicas, rl.MaxReplicas()); err != nil {
            return err
        }
    }
    if rl, ok := p.(registryLimiter); ok {
        if err := policy.EnforceAllowedRegistries(c.Image, rl.AllowedRegistries()); err != nil {
            return err
        }
    }
    return nil
}
```

No import of the adapter type. The handler works with any `oam.Policy` that happens to
implement `replicaLimiter` — including future launcher-native policies.

### NoopPolicy problem

With Option B, `NoopPolicy` has no data methods. Both B1 and B2 return early (no
enforcement, no defaults) when the policy is `NoopPolicy`:
- B1: `p.(*OAMPolicyAdapter)` assertion fails → `return nil`
- B2: `p.(replicaLimiter)` assertion fails → skip the block

This means NoopPolicy silently disables all enforcement and default application. This is
the intended behavior (no policy = no constraints), but it is implicit. Bugs where a nil
policy slips through as `(*NoopPolicy)(nil)` (nil pointer wrapped in interface) would also
silently skip all enforcement without error.

To make this explicit, the transformer must always pass `&oam.NoopPolicy{}` (non-nil
NoopPolicy) rather than a nil `oam.Policy` interface value, and handler code must not
check `p == nil`.

### Interface growth

If `EnvironmentPolicy` adds a new field, the adapter grows a new accessor method. The
`Policy` interface is never updated. Sub-option B2 handler files also grow a new line in
their local sub-interface if they need the new data.

There is no compile-time check that the adapter covers all data a handler might need —
only runtime behavior verifies coverage.

### Summary

- 1 interface method (marker only)
- crane gains one `OAMPolicyAdapter` struct with ~20 accessor methods
- Handler code: type assertions (B1: to adapter type; B2: to local sub-interfaces)
- No compiler verification — coverage gaps are silent
- Policy interface never grows; adapters grow instead
- A future launcher-native Policy (completely different semantics) can satisfy the marker without any constraint on its shape

---

## Summary Comparison

| Aspect | Option A: Typed Accessors | Option B: Opaque Marker |
|---|---|---|
| Interface methods | ~19 | 1 (marker) |
| Compiler verifies crane implements Policy | **Yes** — `var _ oam.Policy = ...` | No |
| Type assertions in handler code | **None** | Required (B1 or B2) |
| NoopPolicy works implicitly | **Yes** — zero returns permit everything | Silent skip (assertions fail) |
| Boilerplate | ~20 methods on `EnvironmentPolicy` | Adapter struct + ~20 methods |
| Handler imports | `oam` package only | `oam` + adapter type (B1) or just `oam` (B2) |
| Interface grows with EnvironmentPolicy | **Yes** — explicit gate | No — adapter grows silently |
| Future non-crane Policy implementations | Must implement all ~19 methods | Only satisfy the marker |
| Coverage gap detection | **Compile time** | Runtime (silent) |

### When Option B's flexibility matters

Option B's main advantage — that future Policy implementations only need the marker, not
19 methods — is relevant if launcher will define its own native Policy type (separate from
crane's EnvironmentPolicy) that has fundamentally different enforcement semantics. Because
policy is launcher-native from day one (see Framing above), a future launcher policy type
is a realistic scenario. However, if that future type has the same enforcement surface as
crane's `EnvironmentPolicy` (replicas, memory, allowed registries, security flags), it
would implement the same 19 methods anyway.

Option A's explicit interface growth (every new policy field requires a new interface
method) is an intentional gate, not a burden — it ensures new policy data is consciously
exposed to the public API surface rather than silently accumulating in an adapter.
