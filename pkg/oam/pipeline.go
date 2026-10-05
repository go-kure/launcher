package oam

import "fmt"

// PolicyHandler dispatches a single OAM policy document during pipeline execution.
//
// The transform returns an error from Apply as `policy "<name>": <error>`, so a
// handler says what is wrong and does not name the policy itself.
type PolicyHandler interface {
	CanHandle(policyType string) bool
	Apply(policy *ApplicationPolicy, components []string, result *PolicyResult) error
}

// PolicyResult accumulates the effects of all OAM policy handlers.
type PolicyResult struct {
	TierOverrides map[string]Tier
	Dependencies  map[string][]string
	AppDependsOn  []string
	// Extensions carries what a consumer's own policy handlers record: launcher
	// neither reads nor changes it, and TransformWithPolicy returns it as the
	// handlers left it. A handler writes under a key it owns (a domain-qualified
	// name, as a label key is) a value of any type; the consumer reads that key
	// back and asserts its own type. This is where a consumer that delivers the
	// application keeps what its delivery policies say (go-kure/launcher#781).
	Extensions map[string]any
	// ConsumedCapabilities is the sorted, deduped capability keys this app's traits
	// actually resolved against ctx.Capabilities, that its components' handlers took
	// defaults from (ComponentCapabilityDefaults, go-kure/launcher#742), and that its
	// lowering rules read through LoweringContext.Capability (go-kure/launcher#686) — the authoritative
	// replacement for a downstream consumer's own interim candidate-key derivation
	// (go-kure/launcher#290). A read by a RawDocumentLoweringRule, under LowerRaws, is
	// not among them. Nil when nothing consumed anything (AppDependsOn's
	// nil-until-populated convention).
	ConsumedCapabilities []string
}

// NewPolicyResult creates an empty PolicyResult with initialised maps.
func NewPolicyResult() *PolicyResult {
	return &PolicyResult{
		TierOverrides: make(map[string]Tier),
		Dependencies:  make(map[string][]string),
		Extensions:    make(map[string]any),
	}
}

// HasDependencies reports whether any dependency relationships were recorded.
func (r *PolicyResult) HasDependencies() bool {
	return len(r.Dependencies) > 0
}

// Tier is a deployment tier an author places a component in (a placement
// policy, or the tier annotation). Tiers deploy in TierOrder; a component
// nothing places has the empty Tier and is ordered after no tier.
type Tier string

const (
	TierInfra    Tier = "infra"
	TierServices Tier = "services"
	TierApps     Tier = "apps"
)

// TierOrder defines the deployment order from earliest to latest.
var TierOrder = []Tier{TierInfra, TierServices, TierApps}

// ViolationError is returned when an Enforceable config rejects the current Policy.
// Whatever ApplyPolicy returns is wrapped in one, so it is wider than a refusal
// by the policy: Class tells the two apart.
type ViolationError struct {
	Component string
	Cause     error
	// Class is what the refusal is about: the class of the first PolicyRefusal
	// in the cause chain, and RefusalUnclassified when the cause is not a
	// refusal by the policy or carries no class. NewViolationError fills it; a
	// literal that leaves it out is unclassified.
	Class RefusalClass
}

func (e *ViolationError) Error() string {
	return fmt.Sprintf("component %q: %s", e.Component, e.Cause)
}

func (e *ViolationError) Unwrap() error { return e.Cause }
