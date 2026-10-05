package components

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what a kind component adds to policyFreeKind when a
// dimension of the environment policy does reach its object
// (go-kure/launcher#790): a field that sizes a pod another controller starts
// for it, or one that holds a secret in the clear. Such a kind is a
// policyHeldKind: everything a policyFreeKind is, and one function that holds
// the decoded value to the policy. The decode, its refusals and Generate are
// policyFreeKind's, unchanged: the wrapper embeds the shared helper and only
// refuses, so whatever the helper comes to carry for every kind (an object's
// authored labels and annotations, when it takes them) a policyHeldKind
// carries too, with no change here.

// policyHeldKind is a policyFreeKind whose decoded value the environment
// policy is asked about.
type policyHeldKind[T any] struct {
	policyFreeKind[T]
	// enforce holds decoded to the policy p, which is never nil. It refuses or
	// passes: it fills no default and must not change decoded, which Generate
	// copies into the object afterwards.
	enforce func(decoded *T, p oam.Policy) error
}

// config is policyFreeKind.config, returning a config whose ApplyPolicy asks
// the kind's enforce.
func (k *policyHeldKind[T]) config(component *oam.Component) (stack.ApplicationConfig, error) {
	cfg, err := k.policyFreeKind.config(component)
	if err != nil {
		return nil, err
	}
	free, ok := cfg.(*policyFreeKindConfig[T])
	if !ok {
		return nil, errors.Errorf("internal: a policy-free kind's config is a %T", cfg)
	}
	return &policyHeldKindConfig[T]{policyFreeKindConfig: free, enforce: k.enforce}, nil
}

// policyHeldKindConfig implements stack.ApplicationConfig for a
// policyHeldKind: a policyFreeKindConfig, of which it keeps Generate, with an
// ApplyPolicy that checks.
type policyHeldKindConfig[T any] struct {
	*policyFreeKindConfig[T]
	enforce func(decoded *T, p oam.Policy) error
}

// ApplyPolicy holds the decoded value to the policy. A nil policy checks
// nothing.
func (c *policyHeldKindConfig[T]) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return c.enforce(c.decoded, p)
}
