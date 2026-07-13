package oam

// Policy provides environment-level constraints and defaults for OAM component
// and trait handlers. Handlers call its methods; they must not type-assert.
//
// The 21 typed accessor methods correspond to every piece of data that handlers
// currently access via the downstream runtime's *api.EnvironmentPolicy. See the finalized design
// in docs/oam/options-policy-interface.md (Option A).
//
// When no policy is supplied the runtime passes NoopPolicy, so handlers always
// receive a non-nil Policy value and nil checks are not needed.
type Policy interface {
	// Enforced limits — nil or empty string means no limit is set.
	MaxReplicas() *int32
	MaxCPU() string
	MaxMemory() string
	MaxStorageSize() string
	AllowedRegistries() []string

	// Defaults — nil or empty string means leave the OAM value as-is.
	DefaultReplicas() *int32
	DefaultCPURequest() string
	DefaultMemoryRequest() string
	DefaultCPULimit() string
	DefaultMemoryLimit() string

	// Workload-shape defaults — nil or empty string means leave the OAM value as-is.
	DefaultStorageSize() string
	DefaultScalerMinReplicas() *int32
	DefaultScalerMaxReplicas() *int32

	// Security flags — false is the zero value (default-deny).
	AllowHostNetwork() bool
	AllowPrivileged() bool
	AllowHostPID() bool
	AllowHostIPC() bool
	AllowHostPathVolumes() bool

	// Capability constraints — nil means unconstrained.
	AllowedCapabilities() []string
	ForbiddenCapabilities() []string
	RequiredCapabilities() []string
}

// Enforceable is implemented by component and trait ApplicationConfig types that
// accept per-environment policy enforcement. The runtime calls ApplyPolicy after
// each handler produces a config; configs that do not implement Enforceable are
// left unchanged.
type Enforceable interface {
	ApplyPolicy(policy Policy) error
}

// NoopPolicy satisfies Policy with zero values:
// no enforced limits, no defaults applied, security-sensitive features denied by default.
// Security flags are plain bool; false means denied — this is intentional default-deny
// behaviour when no policy document is provided, not a "permit everything" stance.
type NoopPolicy struct{}

// compile-time interface check
var _ Policy = (*NoopPolicy)(nil)

func (*NoopPolicy) MaxReplicas() *int32              { return nil }
func (*NoopPolicy) MaxCPU() string                   { return "" }
func (*NoopPolicy) MaxMemory() string                { return "" }
func (*NoopPolicy) MaxStorageSize() string           { return "" }
func (*NoopPolicy) AllowedRegistries() []string      { return nil }
func (*NoopPolicy) DefaultReplicas() *int32          { return nil }
func (*NoopPolicy) DefaultCPURequest() string        { return "" }
func (*NoopPolicy) DefaultMemoryRequest() string     { return "" }
func (*NoopPolicy) DefaultCPULimit() string          { return "" }
func (*NoopPolicy) DefaultMemoryLimit() string       { return "" }
func (*NoopPolicy) DefaultStorageSize() string       { return "" }
func (*NoopPolicy) DefaultScalerMinReplicas() *int32 { return nil }
func (*NoopPolicy) DefaultScalerMaxReplicas() *int32 { return nil }
func (*NoopPolicy) AllowHostNetwork() bool           { return false }
func (*NoopPolicy) AllowPrivileged() bool            { return false }
func (*NoopPolicy) AllowHostPID() bool               { return false }
func (*NoopPolicy) AllowHostIPC() bool               { return false }
func (*NoopPolicy) AllowHostPathVolumes() bool       { return false }
func (*NoopPolicy) AllowedCapabilities() []string    { return nil }
func (*NoopPolicy) ForbiddenCapabilities() []string  { return nil }
func (*NoopPolicy) RequiredCapabilities() []string   { return nil }
