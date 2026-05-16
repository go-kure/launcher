package oam

import "testing"

// Compile-time check: NoopPolicy must satisfy Policy.
var _ Policy = (*NoopPolicy)(nil)

func TestNoopPolicy_ZeroValues(t *testing.T) {
	p := &NoopPolicy{}

	if got := p.MaxReplicas(); got != nil {
		t.Errorf("MaxReplicas() = %v, want nil", got)
	}
	if got := p.MaxCPU(); got != "" {
		t.Errorf("MaxCPU() = %q, want empty", got)
	}
	if got := p.MaxMemory(); got != "" {
		t.Errorf("MaxMemory() = %q, want empty", got)
	}
	if got := p.MaxStorageSize(); got != "" {
		t.Errorf("MaxStorageSize() = %q, want empty", got)
	}
	if got := p.AllowedRegistries(); got != nil {
		t.Errorf("AllowedRegistries() = %v, want nil", got)
	}
	if got := p.DefaultReplicas(); got != nil {
		t.Errorf("DefaultReplicas() = %v, want nil", got)
	}
	if got := p.DefaultCPURequest(); got != "" {
		t.Errorf("DefaultCPURequest() = %q, want empty", got)
	}
	if got := p.DefaultMemoryRequest(); got != "" {
		t.Errorf("DefaultMemoryRequest() = %q, want empty", got)
	}
	if got := p.DefaultCPULimit(); got != "" {
		t.Errorf("DefaultCPULimit() = %q, want empty", got)
	}
	if got := p.DefaultMemoryLimit(); got != "" {
		t.Errorf("DefaultMemoryLimit() = %q, want empty", got)
	}
	if got := p.AllowHostNetwork(); got {
		t.Error("AllowHostNetwork() = true, want false (default-deny)")
	}
	if got := p.AllowPrivileged(); got {
		t.Error("AllowPrivileged() = true, want false (default-deny)")
	}
	if got := p.AllowHostPID(); got {
		t.Error("AllowHostPID() = true, want false (default-deny)")
	}
	if got := p.AllowHostIPC(); got {
		t.Error("AllowHostIPC() = true, want false (default-deny)")
	}
	if got := p.AllowHostPathVolumes(); got {
		t.Error("AllowHostPathVolumes() = true, want false (default-deny)")
	}
	if got := p.AllowedCapabilities(); got != nil {
		t.Errorf("AllowedCapabilities() = %v, want nil", got)
	}
	if got := p.ForbiddenCapabilities(); got != nil {
		t.Errorf("ForbiddenCapabilities() = %v, want nil", got)
	}
	if got := p.RequiredCapabilities(); got != nil {
		t.Errorf("RequiredCapabilities() = %v, want nil", got)
	}
}
