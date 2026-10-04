package oam

import (
	"fmt"
	"slices"
	"strings"
)

// Launcher orders nothing by itself (go-kure/launcher#783). Components are
// applied in an order only where something declares one:
//
//   - a placement, from the placement policy or the tier annotation: every
//     component of a tier follows every component of the populated tier before it;
//   - a dependency policy rule: a component follows the components it names;
//   - a lowering rule ordering the components it emits (Component.OrderAfter).
//
// orderComponents turns those into groups; the transform makes each group a
// child bundle of the application's one bundle.

// OrderAfter declares that c is applied after the components named: the
// transform puts c in a later group than each of them, as a dependency policy
// rule would.
//
// It is for a lowering rule's output, to order the components one invocation
// emits (the helm rule: the release after the source it generated). A document
// cannot author it; an author orders components with the dependency policy. A
// name must be a component of the document after lowering, and not c itself:
// anything else fails the transform.
//
// The order is part of the component value, as a step attached with
// AfterPolicy is: it survives copies of the component, but not serialization,
// so a RawDocumentLoweringRule cannot use it. It survives further lowering
// too: when a ComponentLoweringRule lowers a component carrying one, the
// engine gives the order to the components that rule emits for it, whatever
// the rule built them from, so what the component became waits as it did
// (inheritOrder).
//
// Declaring never changes another copy of c: each call gives c a new slice.
func (c *Component) OrderAfter(names ...string) {
	if c == nil || len(names) == 0 {
		return
	}
	c.orderAfter = append(slices.Clip(c.orderAfter), names...)
}

// inheritOrder orders the components a rule emitted for one component after
// each of names, the components that one was ordered after, so what it became
// waits as it did (lowerDocumentBody).
//
// A Flux source the rule ordered another of the emitted components after is
// left as it is. Such a source is the application's, not the component's: every
// component that names the same source adopts it, and it is applied with the
// application bundle, before every group (orderComponents). Made to wait on
// what one of its consumers waits on, it could wait on another of its
// consumers, which waits on it.
func inheritOrder(emitted []Component, names []string) {
	if len(names) == 0 {
		return
	}
	prerequisite := map[string]bool{}
	for i := range emitted {
		for _, name := range emitted[i].orderAfter {
			prerequisite[name] = true
		}
	}
	for i := range emitted {
		c := &emitted[i]
		if generatedSourceTypes[c.Type] && prerequisite[c.Name] {
			continue
		}
		for _, name := range names {
			if !slices.Contains(c.orderAfter, name) {
				c.OrderAfter(name)
			}
		}
	}
}

// generatedSourceTypes are the Flux source component types a lowering rule emits
// on its consumers' behalf (the helm rule's helmrepository, ocirepository,
// gitrepository and bucket).
var generatedSourceTypes = map[string]bool{
	"helmrepository": true,
	"ocirepository":  true,
	"gitrepository":  true,
	"bucket":         true,
}

// componentOrder is the order a document's components are applied in.
type componentOrder struct {
	// sources are the generated sources (orderComponents), in document order.
	// They are in no group: the application bundle itself holds them.
	sources []componentEntry
	// groups hold every other component, each group in document order. A
	// component is in the first group that follows every component it is
	// ordered after.
	groups [][]componentEntry
	// suffixes names each group: the tier when the group is exactly the
	// components placed in one tier, else the group's two-digit position.
	suffixes []string
}

// ordered reports whether anything is applied before anything else. When
// nothing is, the application is one flat bundle.
func (o *componentOrder) ordered() bool {
	return len(o.sources) > 0 || len(o.groups) > 1
}

// groupName is the name of the application's i-th group bundle,
// "<application>-<tier>" or "<application>-<NN>", shortened by the one rule
// for generated names (ShortenNameWithSuffix) to a DNS-1123 subdomain.
func (o *componentOrder) groupName(application string, i int) string {
	return ShortenNameWithSuffix(application, "-"+o.suffixes[i], ShortenLimitSubdomain)
}

// sequence returns every entry in the order it is applied: the generated
// sources, then each group. With nothing ordered that is document order.
func (o *componentOrder) sequence() []componentEntry {
	out := slices.Clone(o.sources)
	for _, group := range o.groups {
		out = append(out, group...)
	}
	return out
}

// orderEdge is one declared order: a component is applied after the entry at
// index after, for the reason origin gives.
type orderEdge struct {
	after  int
	origin string
}

const (
	orderOriginDependency = "dependency policy"
	orderOriginRule       = "lowering rule"
)

// ruleOrder returns the names the entry's lowering rule ordered it after: its
// component's, or every member's for a collapsed sibling group.
func (e componentEntry) ruleOrder() []string {
	if len(e.members) == 0 {
		return e.component.orderAfter
	}
	var names []string
	for _, m := range e.members {
		names = append(names, m.component.orderAfter...)
	}
	return names
}

// orderComponents groups entries by what orders them: entries' tiers (the tier
// annotation or a placement, "" for neither), deps (the dependency policy:
// component name to the names it follows) and what each component's lowering
// rule declared (Component.OrderAfter).
//
// A generated source is a Flux source (generatedSourceTypes) a lowering rule
// emitted, ordered a component after, and ordered after nothing itself. It is
// kept out of the groups, for the application bundle to hold: a bundle's own
// applications are applied before its children, so every consumer follows it
// wherever the consumer is placed. For the same reason nothing can place it in
// a tier or make it wait, and an order after it needs no group.
//
// A dependency on a name no component has is ignored, as it always was: the
// dependency policy refuses one itself. A rule's order after such a name, or
// after the component itself, is refused.
//
// A cycle is refused, naming each component of it and what ordered it. A
// dependency rule against the placement order is such a cycle.
func orderComponents(entries []componentEntry, deps map[string][]string) (*componentOrder, error) {
	pos := make(map[string]int, len(entries))
	for i, e := range entries {
		pos[e.component.Name] = i
	}

	consumed := make([]bool, len(entries)) // a rule ordered a component after it
	for _, e := range entries {
		for _, name := range e.ruleOrder() {
			j, ok := pos[name]
			if !ok {
				return nil, &TransformError{Message: fmt.Sprintf(
					"component %q is ordered after %q by its lowering rule, but the document has no component %q",
					e.component.Name, name, name)}
			}
			if name == e.component.Name {
				return nil, &TransformError{Message: fmt.Sprintf(
					"component %q is ordered after itself by its lowering rule", e.component.Name)}
			}
			consumed[j] = true
		}
	}

	order := &componentOrder{}
	source := make([]bool, len(entries))
	for i, e := range entries {
		c := e.component
		if len(e.members) > 0 || !c.synthesized || !generatedSourceTypes[c.Type] || !consumed[i] || len(c.orderAfter) > 0 {
			continue
		}
		if e.tier != "" {
			return nil, &TransformError{Message: fmt.Sprintf(
				"%s %q cannot be placed in tier %s: a source a lowering rule generates is applied with the application bundle itself, before every group",
				c.Type, c.Name, e.tier)}
		}
		if on := deps[c.Name]; len(on) > 0 {
			return nil, &TransformError{Message: fmt.Sprintf(
				"%s %q cannot wait on %s: a source a lowering rule generates is applied with the application bundle itself, before every group",
				c.Type, c.Name, strings.Join(on, ", "))}
		}
		source[i] = true
		order.sources = append(order.sources, e)
	}

	// Edges, in the order a cycle names them: the dependency policy's, the
	// lowering rules', then placement's.
	edges := make([][]orderEdge, len(entries))
	add := func(i, after int, origin string) {
		if source[after] || slices.ContainsFunc(edges[i], func(e orderEdge) bool { return e.after == after }) {
			return
		}
		edges[i] = append(edges[i], orderEdge{after: after, origin: origin})
	}
	for i, e := range entries {
		if source[i] {
			continue
		}
		for _, name := range deps[e.component.Name] {
			if j, ok := pos[name]; ok {
				add(i, j, orderOriginDependency)
			}
		}
		for _, name := range e.ruleOrder() {
			add(i, pos[name], orderOriginRule)
		}
	}
	placed := make(map[Tier]int, len(TierOrder))
	var previous []int
	var previousTier Tier
	for _, tier := range TierOrder {
		var current []int
		for i, e := range entries {
			if !source[i] && e.tier == tier {
				current = append(current, i)
			}
		}
		if len(current) == 0 {
			continue
		}
		placed[tier] = len(current)
		for _, i := range current {
			for _, j := range previous {
				add(i, j, fmt.Sprintf("placement: tier %s is after tier %s", tier, previousTier))
			}
		}
		previous, previousTier = current, tier
	}

	levels, err := orderLevels(entries, edges)
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		if source[i] {
			continue
		}
		for len(order.groups) <= levels[i] {
			order.groups = append(order.groups, nil)
		}
		order.groups[levels[i]] = append(order.groups[levels[i]], e)
	}
	for i, group := range order.groups {
		tier := group[0].tier
		wholeTier := placed[tier] == len(group) && !slices.ContainsFunc(group, func(e componentEntry) bool { return e.tier != tier })
		if wholeTier {
			order.suffixes = append(order.suffixes, string(tier))
		} else {
			order.suffixes = append(order.suffixes, fmt.Sprintf("%02d", i))
		}
	}
	return order, nil
}

// orderLevels returns each entry's level: 0 for an entry ordered after nothing,
// else one more than the highest level it is ordered after. Entries are visited
// in document order and each entry's edges in the order they were added, so a
// cycle is reported the same way on every run.
func orderLevels(entries []componentEntry, edges [][]orderEdge) ([]int, error) {
	const (
		unvisited = iota
		visiting
		visited
	)
	levels := make([]int, len(entries))
	state := make([]int, len(entries))
	// path is the chain being walked: path[k] is ordered after path[k+1] for
	// the reason via[k] gives.
	var path []int
	var via []string

	var visit func(i int) error
	visit = func(i int) error {
		state[i] = visiting
		path = append(path, i)
		for _, edge := range edges[i] {
			via = append(via, edge.origin)
			switch state[edge.after] {
			case visiting:
				start := slices.Index(path, edge.after)
				steps := make([]string, 0, len(path)-start)
				for k := start; k < len(path); k++ {
					next := edge.after
					if k+1 < len(path) {
						next = path[k+1]
					}
					steps = append(steps, fmt.Sprintf("%q is after %q (%s)",
						entries[path[k]].component.Name, entries[next].component.Name, via[k]))
				}
				return &TransformError{Message: "components cannot be ordered: " + strings.Join(steps, ", ")}
			case unvisited:
				if err := visit(edge.after); err != nil {
					return err
				}
			}
			via = via[:len(via)-1]
			levels[i] = max(levels[i], levels[edge.after]+1)
		}
		path = path[:len(path)-1]
		state[i] = visited
		return nil
	}
	for i := range entries {
		if state[i] == unvisited {
			if err := visit(i); err != nil {
				return nil, err
			}
		}
	}
	return levels, nil
}
