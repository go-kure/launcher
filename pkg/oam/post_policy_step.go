package oam

import (
	"slices"

	"github.com/go-kure/kure/pkg/stack"
)

// PostPolicyStep is a step a lowering rule attaches to a component it emits
// (Component.AfterPolicy). The transform runs it on the config the component's
// handler built, after the environment policy (Enforceable.ApplyPolicy) and
// before any trait of the component. It is for a value the rule decides from
// what the policy decided: lowering runs before the policy, so the rule cannot
// write such a value itself.
//
// A step may change config in place. An error fails the transform, naming the
// component.
type PostPolicyStep func(config stack.ApplicationConfig) error

// AfterPolicy attaches step to c: the transform runs it after the environment
// policy has been applied to c's config and before c's traits, after any step
// attached before it.
//
// It is for a lowering rule's output. A document cannot author a step, so a
// component that carries one was emitted by a rule. The step is part of the
// component value: it survives copies of the component and later lowering
// rounds, including a trait rule rewriting the component's traits, but not
// serialization, so a RawDocumentLoweringRule cannot use it. A
// ComponentLoweringRule that lowers a component carrying a step must carry the
// step over itself, by copying the component it was handed; a component it
// builds anew has no step, and the engine cannot tell one was dropped.
//
// Attaching never changes another copy of c: each call gives c a new slice.
// A nil step is ignored.
func (c *Component) AfterPolicy(step PostPolicyStep) {
	if c == nil || step == nil {
		return
	}
	c.afterPolicy = append(slices.Clip(c.afterPolicy), step)
}
