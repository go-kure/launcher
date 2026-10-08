package registry_test

import (
	"fmt"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// TestRegistries_FreshMapEachCall fails where a caller's change to a returned
// map reaches the map the next call returns.
func TestRegistries_FreshMapEachCall(t *testing.T) {
	checkFresh(t, "ComponentHandlers", registry.ComponentHandlers)
	checkFresh(t, "ComponentLoweringRules", registry.ComponentLoweringRules)
	checkFresh(t, "TraitHandlers", registry.TraitHandlers)
	checkFresh(t, "TraitLoweringRules", registry.TraitLoweringRules)
	checkFresh(t, "PolicyHandlers", registry.PolicyHandlers)
}

// checkFresh deletes one entry of the map call returns and adds another, then
// fails where the next call's map shows either change.
func checkFresh[V any](t *testing.T, name string, call func() map[string]V) {
	t.Helper()
	first := call()
	if len(first) == 0 {
		t.Fatalf("%s returns no entry", name)
	}
	var deleted string
	for key := range first {
		deleted = key
		break
	}
	delete(first, deleted)
	var zero V
	first["added-by-a-caller"] = zero

	second := call()
	if _, ok := second[deleted]; !ok {
		t.Errorf("%s: after a caller deleted %q, the next call has no entry for it", name, deleted)
	}
	if _, ok := second["added-by-a-caller"]; ok {
		t.Errorf("%s: an entry a caller added appears in the next call", name)
	}
}

// TestRegistries_LoweringRulesClaimTheirKey fails where a lowering rule is
// keyed under a type other than the one it claims: a consumer that deletes or
// replaces a key would otherwise leave the rule registered under its own.
func TestRegistries_LoweringRulesClaimTheirKey(t *testing.T) {
	for key, rule := range registry.ComponentLoweringRules() {
		if got := rule.ComponentType(); got != key {
			t.Errorf("ComponentLoweringRules()[%q] claims %q", key, got)
		}
	}
	for key, rule := range registry.TraitLoweringRules() {
		if got := rule.TraitType(); got != key {
			t.Errorf("TraitLoweringRules()[%q] claims %q", key, got)
		}
	}
}

// TestRegistries_RegisterTogether registers every entry the way the package
// comment shows; a type that is both a handler and a lowering rule of one
// position panics there.
func TestRegistries_RegisterTogether(t *testing.T) {
	newTransformer()
}

func newTransformer() *oam.Transformer {
	t := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		t.RegisterComponentLowering(r)
	}
	for name, h := range registry.TraitHandlers() {
		t.RegisterBuiltinTrait(name, h)
	}
	for name, h := range registry.PolicyHandlers() {
		t.RegisterPolicy(name, h)
	}
	for _, r := range registry.TraitLoweringRules() {
		t.RegisterBuiltinTraitLowering(r)
	}
	return t
}

// A consumer that brings its own placement policy drops launcher's and
// registers the rest.
func ExamplePolicyHandlers() {
	policies := registry.PolicyHandlers()
	delete(policies, "placement")
	t := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for name, h := range policies {
		t.RegisterPolicy(name, h)
	}
	fmt.Println(len(policies), len(registry.PolicyHandlers()))
	// Output: 1 2
}
