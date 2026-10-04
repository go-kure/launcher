package oam

import (
	"fmt"
	"slices"
	"strings"
)

// LoweringTargets names the types a lowering rule lowers into, by the position
// each is emitted at: the components it emits, the traits it emits or attaches
// to a component it emits, and the policies it emits. A type the rule only
// forwards from its input is not a target: the author wrote it. Document kinds
// are not listed either: a kind no rule claims is refused where the document is
// read.
type LoweringTargets struct {
	ComponentTypes []string
	TraitTypes     []string
	PolicyTypes    []string
}

// LoweringTargetDeclarer is an optional interface for a lowering rule of any
// kind (document, raw document, component, trait, policy). A rule that
// implements it declares every type it can emit, over all of its inputs, and
// Transformer.Seal refuses the registry while one of them is claimed by neither
// a handler nor a lowering rule at that position.
//
// A rule that does not implement it is not checked: a type it emits with no
// handler is found only when a document uses the rule, as a "no handler" error
// of the transform. The engine does not check that a rule emits only what it
// declares.
type LoweringTargetDeclarer interface {
	LoweringTargets() LoweringTargets
}

// Seal checks that the registry is complete: every type a registered lowering
// rule declares it lowers into (LoweringTargetDeclarer) is registered at its
// position, as a handler or as another lowering rule. It returns a
// TransformError naming each rule and the type it is missing, or nil.
//
// The check cannot run when a rule is registered: a rule and the handlers of
// its targets are registered in any order. A caller that has finished
// registering calls Seal to learn of a gap before it has a document; Transform
// and TransformWithPolicy call it themselves, first, so a gap is refused
// whether or not the document uses the rule. LowerRaws does not: it dispatches
// no handler.
//
// Seal stores nothing and locks nothing. It reads the registry as it stands, so
// it may be called any number of times, and a type registered after a refusal
// is accepted by the next call.
func (t *Transformer) Seal() error {
	var missing []string
	check := func(label, typeName string, rule any) {
		d, ok := rule.(LoweringTargetDeclarer)
		if !ok {
			return
		}
		id := loweringRuleIdentity(label, typeName, rule)
		targets := d.LoweringTargets()
		for _, typ := range targets.ComponentTypes {
			_, isRule := t.componentLoweringRules[typ]
			if _, isHandler := t.componentHandlers[typ]; !isHandler && !isRule {
				missing = append(missing, missingTarget(id, PositionComponent, typ))
			}
		}
		for _, typ := range targets.TraitTypes {
			_, isRule := t.traitLoweringRules[typ]
			if _, isHandler := t.traitHandlers[typ]; !isHandler && !isRule {
				missing = append(missing, missingTarget(id, PositionTrait, typ))
			}
		}
		for _, typ := range targets.PolicyTypes {
			_, isRule := t.policyLoweringRules[typ]
			if _, isHandler := t.policyHandlers[typ]; !isHandler && !isRule {
				missing = append(missing, missingTarget(id, PositionPolicy, typ))
			}
		}
	}
	for kind, r := range t.docLoweringRules {
		check(string(PositionDocument), kind, r)
	}
	for key, r := range t.rawDocLoweringRules {
		check("rawdocument", key.apiVersion+"/"+key.kind, r)
	}
	for typ, r := range t.componentLoweringRules {
		check(string(PositionComponent), typ, r)
	}
	for typ, r := range t.traitLoweringRules {
		check(string(PositionTrait), typ, r)
	}
	for typ, r := range t.policyLoweringRules {
		check(string(PositionPolicy), typ, r)
	}
	if len(missing) == 0 {
		return nil
	}
	// The registries are maps: sorted, so the message does not depend on their
	// iteration order, and compacted, so a type a rule lists twice is named once.
	slices.Sort(missing)
	missing = slices.Compact(missing)
	return &TransformError{Message: "registry incomplete: " + strings.Join(missing, "; ")}
}

// missingTarget is one entry of Seal's error: the rule, by the identity
// Origin.Rule and LoweringStep.Rule carry, and the type it lowers into.
func missingTarget(rule string, position Position, typ string) string {
	return fmt.Sprintf("lowering rule %s lowers into %s type %q, which is not registered", rule, position, typ)
}

// emittedBy is the clause a "no handler" error adds for an element a lowering
// rule emitted: the rule, and the authored element it lowered. It is empty for
// an element as authored (no origin, or one no rule produced), so the message
// of a document that uses no rule stays short.
//
// The authored element is rendered at component level even for a trait: the
// TraitType of a trait a component rule attached is the emitted type, not an
// authored one, and the component is what the author looks for.
func emittedBy(origin *Origin) string {
	if origin == nil || origin.Rule == "" {
		return ""
	}
	authored := *origin
	authored.TraitType = ""
	return fmt.Sprintf(", emitted by lowering rule %s for %s", origin.Rule, authored)
}
