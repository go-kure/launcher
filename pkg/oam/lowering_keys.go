package oam

import (
	"slices"
	"strings"
)

// LoweredKey is one place the value of a property of a lowered component or trait
// type lands in what its lowering rule emits. The provenance report
// (TransformWithProvenance) has no entry for the lowered type itself: it speaks
// at these places.
type LoweredKey struct {
	// Member is the component type of the emitted member the value lands on. It
	// is PropertySource.Member inside a same-name sibling group. Outside one the
	// report names the component with no member: the place is the same, read
	// under the plain owner.
	//
	// On a place of a trait lowering rule Member is empty: the emitted trait sits
	// on whichever member of the component holds it, so the place is matched by
	// component, trait type and path, whatever member the report names.
	Member string `json:"member" yaml:"member"`
	// Trait is the type of the trait the value lands in: a trait a component rule
	// attaches to that member, or the trait a trait rule emits. It is empty when
	// the value lands in a member's own properties.
	Trait string `json:"trait,omitempty" yaml:"trait,omitempty"`
	// Path is the path the value lands at, in the member's properties or the
	// trait's, in the spelling PropertySource.Path uses. Empty for a trait the
	// key switches or shapes as a whole.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Unchanged says the rule hands an authored value on as written, so a path
	// below the key is the same path below Path, and the report calls an
	// authored value there copied. When false the rule reads the key itself and
	// may build what lands there, in another shape: the report calls a value the
	// rule built rendered, whether the author wrote the key or left it to the
	// rule's default.
	Unchanged bool `json:"unchanged,omitempty" yaml:"unchanged,omitempty"`
	// When limits the place to components whose properties meet every one of
	// the conditions: all must hold. Empty means every component.
	When []LoweredWhen `json:"when,omitempty" yaml:"when,omitempty"`
}

// LoweredWhen is one condition of a LoweredKey: the property Key of the lowered
// component or trait holds one of Values. An empty string in Values also matches
// when Key is left out or written as null.
type LoweredWhen struct {
	Key    string   `json:"key" yaml:"key"`
	Values []string `json:"values" yaml:"values"`
}

// LiveFor reports whether the place applies to the properties props: every
// condition of When holds. For a component's place props are the component's
// authored properties. For a trait rule's place they are the trait's properties
// after the capability merge, since a condition may read a value the platform's
// rendering supplies (the expose rule's controllerType). A condition's key
// matches a property in any letter case, and a property that holds anything but
// a string or null matches no value.
func (k LoweredKey) LiveFor(props map[string]any) bool {
	for _, w := range k.When {
		value := ""
		for name, v := range props {
			if !strings.EqualFold(name, w.Key) || IsNullValue(v) {
				continue
			}
			s, isString := v.(string)
			if !isString {
				return false
			}
			value = s
		}
		if !slices.Contains(w.Values, value) {
			return false
		}
	}
	return true
}

// cloneLoweredKeys copies places with their conditions, so that a caller's
// write reaches no rule.
func cloneLoweredKeys(places []LoweredKey) []LoweredKey {
	out := slices.Clone(places)
	for i := range out {
		out[i].When = slices.Clone(out[i].When)
		for j := range out[i].When {
			out[i].When[j].Values = slices.Clone(out[i].When[j].Values)
		}
	}
	return out
}

// LoweringKeyDeclarer is an optional interface for a component or trait lowering
// rule that implements PropertySchemaProvider. It declares, for every key of the
// rule's schema, the places its value lands (LoweredKey); a key that reaches no
// property of what the rule emits, one that only names an object for example,
// maps to none.
//
// The declaration is structure, not a report: it says where to read, never
// which source supplied the value there. An entry at a place is there whether the
// author wrote the key or the rule, the policy or a built-in default filled it.
// At a place the rule builds, the report gives an authored key and the rule's
// default the same source, so whether the author wrote the key is read from the
// document.
type LoweringKeyDeclarer interface {
	LoweringKeys() map[string][]LoweredKey
}

// ComponentLoweringKeys returns the key map of every registered component
// lowering rule that implements LoweringKeyDeclarer, by component type. A rule
// that does not implement it is omitted: its type publishes no map. The result
// is always non-nil and the caller's own: writing to it reaches no rule.
func (t *Transformer) ComponentLoweringKeys() map[string]map[string][]LoweredKey {
	return loweringKeysOf(t.componentLoweringRules)
}

// TraitLoweringKeys is ComponentLoweringKeys for the registered trait lowering
// rules, by the trait type each claims.
func (t *Transformer) TraitLoweringKeys() map[string]map[string][]LoweredKey {
	return loweringKeysOf(t.traitLoweringRules)
}

// loweringKeysOf returns a copy of the key map of every rule that implements
// LoweringKeyDeclarer, by the type it is registered under.
func loweringKeysOf[R any](rules map[string]R) map[string]map[string][]LoweredKey {
	out := make(map[string]map[string][]LoweredKey)
	for name, r := range rules {
		d, ok := any(r).(LoweringKeyDeclarer)
		if !ok {
			continue
		}
		declared := d.LoweringKeys()
		keys := make(map[string][]LoweredKey, len(declared))
		for key, places := range declared {
			keys[key] = cloneLoweredKeys(places)
		}
		out[name] = keys
	}
	return out
}
