package oam

import (
	"errors"
	"testing"
)

func TestNewPolicyResult_Defaults(t *testing.T) {
	r := NewPolicyResult()
	if r.TierOverrides == nil {
		t.Error("TierOverrides should be initialised, got nil")
	}
	if r.Dependencies == nil {
		t.Error("Dependencies should be initialised, got nil")
	}
	if r.AppDependsOn != nil {
		t.Errorf("AppDependsOn should be nil, got %v", r.AppDependsOn)
	}
	if r.HealthCheckOverrides != nil {
		t.Errorf("HealthCheckOverrides should be nil, got %v", r.HealthCheckOverrides)
	}
	if r.ReconciliationSettings != nil {
		t.Errorf("ReconciliationSettings should be nil, got %v", r.ReconciliationSettings)
	}
}

func TestPolicyResult_HasDependencies(t *testing.T) {
	r := NewPolicyResult()
	if r.HasDependencies() {
		t.Error("HasDependencies() = true on empty result, want false")
	}
	r.Dependencies["web"] = []string{"db"}
	if !r.HasDependencies() {
		t.Error("HasDependencies() = false after adding entry, want true")
	}
}

func TestViolationError_Format(t *testing.T) {
	cause := errors.New("max replicas exceeded")
	e := &ViolationError{Component: "web", Cause: cause}

	want := `component "web": max replicas exceeded`
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if e.Unwrap() == nil {
		t.Error("Unwrap() = nil, want non-nil cause")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is(e, cause) = false, want true")
	}
}
