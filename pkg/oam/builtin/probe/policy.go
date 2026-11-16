package probe

import "github.com/go-kure/launcher/pkg/oam"

// probePolicy sets every Default* accessor of oam.Policy to a value no built-in
// fill uses, so a default the policy supplies is told apart from one the library
// supplies. The values are arbitrary: nothing may read them as the platform's.
type probePolicy struct {
	oam.NoopPolicy
}

func ptr(v int32) *int32 { return &v }

func (*probePolicy) DefaultReplicas() *int32          { return ptr(2) }
func (*probePolicy) DefaultCPURequest() string        { return "37m" }
func (*probePolicy) DefaultMemoryRequest() string     { return "73Mi" }
func (*probePolicy) DefaultCPULimit() string          { return "211m" }
func (*probePolicy) DefaultMemoryLimit() string       { return "331Mi" }
func (*probePolicy) DefaultStorageSize() string       { return "7Gi" }
func (*probePolicy) DefaultScalerMinReplicas() *int32 { return ptr(2) }
func (*probePolicy) DefaultScalerMaxReplicas() *int32 { return ptr(4) }

// ProbePolicy returns the policy the probes run under when the policy is on:
// every Default* accessor of oam.Policy set.
func ProbePolicy() oam.Policy { return &probePolicy{} }

// NoPolicy returns the policy the probes run under when the policy is switched
// off: it supplies no default and enforces nothing.
func NoPolicy() oam.Policy { return &oam.NoopPolicy{} }
