package oam

import (
	"fmt"

	"github.com/go-kure/kure/pkg/stack"
)

// PolicyHandler dispatches a single OAM policy document during pipeline execution.
type PolicyHandler interface {
	CanHandle(policyType string) bool
	Apply(policy *ApplicationPolicy, components []string, result *PolicyResult) error
}

// PolicyResult accumulates the effects of all OAM policy handlers.
type PolicyResult struct {
	TierOverrides          map[string]Tier
	Dependencies           map[string][]string
	AppDependsOn           []string
	HealthCheckOverrides   []stack.HealthCheck
	ReconciliationSettings *ReconciliationSettings
}

// NewPolicyResult creates an empty PolicyResult with initialised maps.
func NewPolicyResult() *PolicyResult {
	return &PolicyResult{
		TierOverrides: make(map[string]Tier),
		Dependencies:  make(map[string][]string),
	}
}

// HasDependencies reports whether any dependency relationships were recorded.
func (r *PolicyResult) HasDependencies() bool {
	return len(r.Dependencies) > 0
}

// Tier classifies a component's deployment ordering.
type Tier string

const (
	TierInfra    Tier = "infra"
	TierServices Tier = "services"
	TierApps     Tier = "apps"
)

// TierOrder defines the deployment order from earliest to latest.
var TierOrder = []Tier{TierInfra, TierServices, TierApps}

// ReconciliationSettings holds Flux reconciliation overrides from a reconciliation policy.
type ReconciliationSettings struct {
	Interval      string
	RetryInterval string
	Timeout       string
	Prune         *bool
	Wait          *bool
	Force         *bool
	Suspend       *bool
}

// ViolationError is returned when an Enforceable config rejects the current Policy.
type ViolationError struct {
	Component string
	Cause     error
}

func (e *ViolationError) Error() string {
	return fmt.Sprintf("component %q: %s", e.Component, e.Cause)
}

func (e *ViolationError) Unwrap() error { return e.Cause }
