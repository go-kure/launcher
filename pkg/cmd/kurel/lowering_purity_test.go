package kurel

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// The lowering contract (D5, docs/oam/design-lowering-engine.md) says a rule's
// output is a function of four inputs only: the element, the enclosing
// document, the ClusterProfile capabilities and the rule's own code. These
// tests run each built-in rule repeatedly on identical inputs, and the whole
// build twice, and require identical output. They catch state a rule keeps
// between calls, the clock, randomness and map-order leaks into a slice. They
// also compare what the rule is handed (the element, the document, the
// enclosing component and the capability bindings, not the name allocator)
// before and after each call, which catches a rule that writes to its inputs
// and returns the same result anyway. They do not catch a read of something
// that never changes during a test run (an
// environment variable, a file): `forbidigo` in .golangci.yml covers the
// named symbols for that.
//
// Results are compared leaf by leaf (flattenValue), unexported fields included.
// A func value, such as a post-policy step a rule attaches (Component.AfterPolicy),
// is compared by presence only: two funcs cannot be compared, so a rule that
// attached a different step each run would pass.

// purityRuns is how many times a rule runs on the same input. A rule whose
// output differs between runs with probability p per run is missed with
// probability (1-p)^(runs-1); the map-order double below fills a slice from a
// 64-key map, where p is close to 1.
const purityRuns = 8

// purityInput is one value a rule is handed by pointer or by reference, named
// for the report.
type purityInput struct {
	name  string
	value any
}

// loweringRun is one prepared lowering: the inputs the rule is handed and the
// call that hands them over.
type loweringRun struct {
	inputs []purityInput
	run    func() (oam.LoweringResult, error)
}

// loweringCall prepares one lowering with inputs built afresh, so one run
// cannot change what the next run is given. The inputs it lists are compared
// before and after run: the engine's own copy of a component is shallow (its
// Properties are shared with the document), so a rule that writes to what it
// is handed changes the document under every later rule while returning the
// same result each time.
type loweringCall func(t *testing.T) loweringRun

// plainCall wraps a lowering that has no inputs to guard.
func plainCall(f func(t *testing.T) (oam.LoweringResult, error)) loweringCall {
	return func(t *testing.T) loweringRun {
		return loweringRun{run: func() (oam.LoweringResult, error) { return f(t) }}
	}
}

// checkLoweringPurity runs call purityRuns times, running between (another
// rule, when given) after each run, and reports the first run whose result
// differs from the first. Each result is flattened as soon as it is returned: a
// rule may hand back storage it reuses, which a later call would change under a
// result kept for comparison.
func checkLoweringPurity(t *testing.T, r invariantReporter, label string, call loweringCall, between func()) {
	t.Helper()
	snapshot := func(run int) ([]string, bool) {
		lr := call(t)
		before, incomplete := flattenInputs(lr.inputs)
		if incomplete != "" {
			r.Errorf("%s: run %d: inputs cannot be compared completely: %s", label, run, incomplete)
			return nil, false
		}
		res, err := lr.run()
		if err != nil {
			t.Fatalf("%s: run %d: lowering failed: %v", label, run, err)
		}
		after, _ := flattenInputs(lr.inputs)
		if diff := firstDifference(before, after); diff != "" {
			r.Errorf("%s: run %d: the rule changed its input: %s", label, run, diff)
			return nil, false
		}
		leaves, incomplete := flattenValue(res)
		if incomplete != "" {
			r.Errorf("%s: run %d: result cannot be compared completely: %s", label, run, incomplete)
		}
		return leaves, incomplete == ""
	}
	first, ok := snapshot(1)
	if !ok {
		return
	}
	for i := 2; i <= purityRuns; i++ {
		if between != nil {
			between()
		}
		got, ok := snapshot(i)
		if !ok {
			return
		}
		if diff := firstDifference(first, got); diff != "" {
			r.Errorf("%s: run %d differs from run 1: %s", label, i, diff)
			return
		}
	}
}

// flattenInputs lists the leaves of every input, each under its name, and says
// why the listing is incomplete ("" when it is not).
//
// Two inputs are equal when they list the same leaves (flattenValue): maps by
// key and value whatever their insertion order, slices by length and element in
// order, pointers by what they point at. A slice's spare capacity, the identity
// of a pointer and the identity of a func are not compared, so a rule that
// appends beyond a slice's length within its capacity, swaps a pointer for a
// copy that is equal, or replaces one func with another passes. State outside
// the listed inputs is not seen either. The name allocator is not an input
// here: allocating names is the one change a rule is meant to make.
func flattenInputs(inputs []purityInput) (leaves []string, incomplete string) {
	for _, in := range inputs {
		got, why := flattenValue(in.value)
		if incomplete == "" && why != "" {
			incomplete = in.name + ": " + why
		}
		for _, l := range got {
			leaves = append(leaves, in.name+strings.TrimPrefix(l, "$"))
		}
	}
	return leaves, incomplete
}

// maxFlattenDepth bounds the traversal. A result nested deeper is reported as
// incomplete, never as equal.
const maxFlattenDepth = 1000

// flattenValue lists every leaf of v as "path = type value", following pointers
// and reading unexported fields, which a serialisation would drop (sealed,
// synthesized, order and origin). The concrete type is part of each leaf, so a
// []byte and a []int holding the same numbers differ. Maps are listed in key
// order. A func value is listed as present or absent: two funcs cannot be
// compared, so a post-policy step a rule attaches is checked for its count, not
// its identity.
//
// A map key is named by its value (keyName), read through the kind accessors
// (Int, String, ...) and never through Interface(), so neither a String method
// nor an unexported field changes what is compared. A result the traversal
// cannot compare completely is never treated as equal: incomplete says why (the
// first reason found), or is "" when the listing is complete. That is the case
// past maxFlattenDepth and for a map key with no stable name.
func flattenValue(v any) (leaves []string, incomplete string) {
	f := &flattener{seen: map[flattenKey]bool{}}
	f.walk("$", reflect.ValueOf(v), 0)
	return f.out, f.incomplete
}

type flattenKey struct {
	ptr uintptr
	typ reflect.Type
	len int // two slices that share a base pointer but not a length are different values
}

func keyOf(v reflect.Value) flattenKey {
	k := flattenKey{ptr: v.Pointer(), typ: v.Type()}
	if v.Kind() == reflect.Slice {
		k.len = v.Len()
	}
	return k
}

type flattener struct {
	out        []string
	seen       map[flattenKey]bool // pointers, maps and slices on the current path
	incomplete string              // why the listing cannot be compared completely; "" when it can
}

// markIncomplete records the first reason the listing is incomplete.
func (f *flattener) markIncomplete(format string, args ...any) {
	if f.incomplete == "" {
		f.incomplete = fmt.Sprintf(format, args...)
	}
}

func (f *flattener) walk(path string, v reflect.Value, depth int) {
	leaf := func(format string, args ...any) {
		typ := "nil"
		if v.IsValid() {
			typ = typeID(v.Type())
		}
		f.out = append(f.out, path+" = "+typ+" "+fmt.Sprintf(format, args...))
	}
	if depth > maxFlattenDepth {
		f.markIncomplete("nested deeper than %d levels at %s", maxFlattenDepth, path)
		return
	}
	switch v.Kind() {
	case reflect.Invalid:
		leaf("nil")
	case reflect.Bool:
		leaf("%t", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		leaf("%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		leaf("%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		leaf("%v", v.Float())
	case reflect.String:
		leaf("%q", v.String())
	case reflect.Func:
		if v.IsNil() {
			leaf("nil")
		} else {
			leaf("<func>")
		}
	case reflect.Interface:
		if v.IsNil() {
			leaf("nil")
			return
		}
		f.walk(path, v.Elem(), depth+1)
	case reflect.Pointer:
		if v.IsNil() {
			leaf("nil")
			return
		}
		if f.enter(v) {
			defer delete(f.seen, keyOf(v))
			f.out = append(f.out, path+" = "+typeID(v.Type()))
			f.walk(path, v.Elem(), depth+1)
		} else {
			leaf("<cycle>")
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				leaf("nil")
				return
			}
			if v.Len() > 0 {
				if !f.enter(v) {
					leaf("<cycle>")
					return
				}
				defer delete(f.seen, keyOf(v))
			}
		}
		leaf("len %d", v.Len())
		for i := 0; i < v.Len(); i++ {
			f.walk(fmt.Sprintf("%s[%d]", path, i), v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() {
			leaf("nil")
			return
		}
		if !f.enter(v) {
			leaf("<cycle>")
			return
		}
		defer delete(f.seen, keyOf(v))
		// A key is named with its concrete type, so 1 and "1" in a map[any]T
		// neither collide nor tie in the sort.
		type entry struct {
			name string
			key  reflect.Value
		}
		entries := make([]entry, 0, v.Len())
		for _, k := range v.MapKeys() {
			name, why := keyName(k)
			if why != "" {
				f.markIncomplete("map key at %s: %s", path, why)
			}
			entries = append(entries, entry{path + "[" + name + "]", k})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		leaf("len %d", v.Len())
		for _, e := range entries {
			f.walk(e.name, v.MapIndex(e.key), depth+1)
		}
	case reflect.Struct:
		f.out = append(f.out, path+" = "+typeID(v.Type()))
		for i := 0; i < v.NumField(); i++ {
			f.walk(path+"."+v.Type().Field(i).Name, v.Field(i), depth+1)
		}
	default:
		leaf("<%s>", v.Kind())
	}
}

// keyName names a map key as "type(value)", looking through an interface key.
// The value is read through the kind accessors, never through Interface() or
// fmt, so a String method on the key type cannot make two keys print alike and
// an unexported field is read like an exported one. why is "" when the name
// identifies the key, and says so when it cannot: a pointer or channel is
// identified by an address that differs between runs, and a NaN key cannot be
// looked up again.
func keyName(k reflect.Value) (name, why string) {
	if k.Kind() == reflect.Interface {
		if k.IsNil() {
			return "nil", ""
		}
		k = k.Elem()
	}
	enc, why := keyValue(k)
	return typeID(k.Type()) + "(" + enc + ")", why
}

// keyValue encodes the value of a key of any kind keyName accepts. A struct or
// array key is encoded field by field; a field of interface type carries its
// concrete type.
func keyValue(k reflect.Value) (enc, why string) {
	switch k.Kind() {
	case reflect.Bool:
		return fmt.Sprintf("%t", k.Bool()), ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", k.Int()), ""
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fmt.Sprintf("%d", k.Uint()), ""
	case reflect.Float32, reflect.Float64:
		f := k.Float()
		if math.IsNaN(f) {
			return "", "a NaN key cannot be looked up again"
		}
		if f == 0 {
			f = 0 // -0 and +0 are one map key, but print differently
		}
		return fmt.Sprintf("%v", f), ""
	case reflect.Complex64, reflect.Complex128:
		c := k.Complex()
		re, im := real(c), imag(c)
		if math.IsNaN(re) || math.IsNaN(im) {
			return "", "a NaN key cannot be looked up again"
		}
		if re == 0 {
			re = 0
		}
		if im == 0 {
			im = 0
		}
		return fmt.Sprintf("(%v,%v)", re, im), ""
	case reflect.String:
		return fmt.Sprintf("%q", k.String()), ""
	case reflect.Interface:
		return keyName(k)
	case reflect.Struct:
		parts := make([]string, 0, k.NumField())
		for i := 0; i < k.NumField(); i++ {
			part, partWhy := keyValue(k.Field(i))
			if why == "" {
				why = partWhy
			}
			parts = append(parts, k.Type().Field(i).Name+"="+part)
		}
		return "{" + strings.Join(parts, ",") + "}", why
	case reflect.Array:
		parts := make([]string, 0, k.Len())
		for i := 0; i < k.Len(); i++ {
			part, partWhy := keyValue(k.Index(i))
			if why == "" {
				why = partWhy
			}
			parts = append(parts, part)
		}
		return "[" + strings.Join(parts, ",") + "]", why
	default:
		return "", fmt.Sprintf("a key of kind %s has no stable name", k.Kind())
	}
}

// typeIDs gives every reflect.Type seen so far a number. A reflect.Type is
// unique per type within a process, so the number identifies the type itself.
// reflect.Type.String() does not: it qualifies a name with the package name,
// not the package path, so two types of the same name in packages that share a
// name (core/v1 and meta/v1 both have ConditionStatus) print identically, and
// so do unnamed types built from them. The numbers depend on the order types
// are first seen, so an id is only ever compared with another id from the same
// test process, never written to a fixture.
var (
	typeIDsMu sync.Mutex
	typeIDs   = map[reflect.Type]int{}
)

// typeID names t with its readable form and its identity number: distinct types
// get distinct ids within one process.
func typeID(t reflect.Type) string {
	typeIDsMu.Lock()
	defer typeIDsMu.Unlock()
	n, ok := typeIDs[t]
	if !ok {
		n = len(typeIDs)
		typeIDs[t] = n
	}
	return fmt.Sprintf("%s#%d", t.String(), n)
}

// enter marks v (a pointer, map or non-empty slice) as on the current path and
// reports false when it already is, which is a cycle.
func (f *flattener) enter(v reflect.Value) bool {
	k := keyOf(v)
	if f.seen[k] {
		return false
	}
	f.seen[k] = true
	return true
}

// firstDifference names the first leaf where a and b differ, or "" when equal.
func firstDifference(a, b []string) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("%q then %q", a[i], b[i])
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("%d leaves then %d", len(a), len(b))
	}
	return ""
}

// parsePurityInput parses a one-component document the way `kurel build` does,
// so the rule sees the same shapes the engine hands it.
func parsePurityInput(t *testing.T, name, typ string, props map[string]any, trait string, traitProps map[string]any) *oam.Application {
	t.Helper()
	data, err := yaml.Marshal(labelInvariantApp(name, typ, props, trait, traitProps))
	if err != nil {
		t.Fatalf("marshaling %s: %v", typ, err)
	}
	doc, err := oam.ParseWithExtraTypes(data, nil, newBuiltinTransformer().LowerableTypes())
	if err != nil {
		t.Fatalf("parsing %s fixture: %v", typ, err)
	}
	return doc
}

// purityContext is the context the engine builds for a rule at this position,
// with a fresh NameAllocator.
func purityContext(doc *oam.Application, comp *oam.Component, trait string, index int) oam.LoweringContext {
	origin := oam.Origin{
		Document:      doc.Metadata.Name,
		DocumentKind:  doc.Kind,
		Namespace:     doc.Metadata.Namespace,
		Component:     comp.Name,
		ComponentType: comp.Type,
		TraitType:     trait,
		Index:         index,
	}
	return oam.LoweringContext{Document: doc, Component: comp, Origin: origin, Namer: oam.NewNameAllocator()}
}

// componentPurityCall builds a call for the component rule typ on its fixture.
func componentPurityCall(typ string, rule oam.ComponentLoweringRule) loweringCall {
	return func(t *testing.T) loweringRun {
		t.Helper()
		name, props := componentLabelFixtures[typ].directly(t, "purity")
		doc := parsePurityInput(t, name, typ, props, "", nil)
		// The engine hands a rule a copy of the document's component
		// (lowering.go: comp := doc.Spec.Components[i]): the two share their maps
		// and slices but not their fields, so a rule that replaces a field of its
		// element does not replace the document's.
		// It also gets its own trait slice (forwardableTraits, lowering.go:1389),
		// whose elements share their Properties maps with the document's.
		element := doc.Spec.Components[0]
		element.Traits = slices.Clone(element.Traits)
		comp := &element
		caps := profileCapabilities(t)
		lctx := purityContext(doc, comp, "", 0).WithCapabilities(caps)
		return loweringRun{
			inputs: []purityInput{{"element", comp}, {"document", doc}, {"capabilities", caps}},
			run:    func() (oam.LoweringResult, error) { return rule.LowerComponent(comp, lctx) },
		}
	}
}

// profileCapabilities evaluates labelInvariantProfile the way `kurel build`
// does (ParseClusterProfile, then EvaluateProfile) and returns the bindings the
// engine hands a rule through its LoweringContext.
func profileCapabilities(t *testing.T) map[string]oam.CapabilityBinding {
	t.Helper()
	profile, err := oam.ParseClusterProfile([]byte(labelInvariantProfile))
	if err != nil {
		t.Fatalf("parsing the test profile: %v", err)
	}
	evaluated, err := newBuiltinTransformer().EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("evaluating the test profile: %v", err)
	}
	return evaluated.Spec.Capabilities
}

// mergedTraitProps returns the trait properties the engine hands a rule: the
// capability's rendering as defaults under the authored values (resolveCapability
// in pkg/oam/transform.go, which the engine runs before LowerTrait and which is
// not exported). The rendering comes from the evaluated profile. The engine
// deep-copies it (copyRendering) before the merge, so the trait's maps are not
// the capability's, and the copy here is deep too; a trait with no rendering
// keeps its authored map, as the engine returns it unchanged. The merge is the
// top-level overlay only, which equals the engine's recursive merge as long as
// the authored properties share no key with the rendering; that is checked, so
// a fixture that grows an overlapping key fails instead of drifting from the
// engine.
func mergedTraitProps(t *testing.T, caps map[string]oam.CapabilityBinding, typ string, authored map[string]any) map[string]any {
	t.Helper()
	if len(caps[typ].Rendering) == 0 {
		return authored
	}
	merged := deepCopyProps(caps[typ].Rendering)
	for k, v := range authored {
		if _, overlap := merged[k]; overlap {
			t.Fatalf("trait %s fixture authors %q, which its capability rendering also sets: merge them as the engine does before comparing", typ, k)
		}
		merged[k] = v
	}
	return merged
}

// deepCopyProps copies the maps and slices of a rendering, which holds only
// those and scalars.
func deepCopyProps(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopyProps(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCopyValue(x[i])
		}
		return out
	default:
		return v
	}
}

// traitPurityCall builds a call for the trait rule typ on its fixture. The
// engine lowers a host component first and forwards the trait to a member the
// host's rule emitted (webservice forwards expose to its service); it then hands
// the trait rule that member as the enclosing component, the authored trait
// merged with the capability rendering as the element, and the authored
// component's identity as the Origin. The call reproduces that: it lowers the
// host with its own rule when it has one, hands the rule the document as that
// round leaves it (the host replaced by its members), and falls back to the host
// itself for a host that is a terminal handler's type. Origin.Rule, which the
// engine stamps on emitted elements, is left empty: no built-in rule reads it.
func traitPurityCall(typ string, rule oam.TraitLoweringRule) loweringCall {
	return func(t *testing.T) loweringRun {
		t.Helper()
		fx := traitLabelFixtures[typ]
		host, hostProps := webserviceHost()
		if fx.host != "" {
			host, hostProps = fx.host, fx.hostProps
		}
		caps := profileCapabilities(t)
		doc := parsePurityInput(t, "purity", host, hostProps, typ, fx.props)
		authored := &doc.Spec.Components[0]
		lctx := purityContext(doc, authored, typ, 0).WithCapabilities(caps)

		member := authored
		hostDoc := doc
		if hostRule, ok := builtinComponentLoweringRules()[host]; ok {
			lowered, err := hostRule.LowerComponent(authored, purityContext(doc, authored, "", 0).WithCapabilities(caps))
			if err != nil {
				t.Fatalf("lowering the %s host of trait %s: %v", host, typ, err)
			}
			// The engine's next round holds the host's members in the document in
			// place of the host.
			next := *doc
			next.Spec.Components = lowered.Components
			hostDoc = &next
			member = nil
			for i := range lowered.Components {
				if slices.ContainsFunc(lowered.Components[i].Traits, func(tr oam.Trait) bool { return tr.Type == typ }) {
					member = &lowered.Components[i]
					break
				}
			}
			if member == nil {
				t.Fatalf("the %s host forwards no %s trait to any member it emits", host, typ)
			}
		}
		lctx.Document = hostDoc
		enclosing := *member // a copy, as the engine hands it (see componentPurityCall)
		member = &enclosing
		lctx.Component = member
		idx := slices.IndexFunc(member.Traits, func(tr oam.Trait) bool { return tr.Type == typ })
		resolved := member.Traits[idx]
		resolved.Properties = mergedTraitProps(t, caps, typ, resolved.Properties)
		return loweringRun{
			inputs: []purityInput{
				{"element", &resolved}, {"document", hostDoc}, {"enclosing component", member}, {"capabilities", caps},
			},
			run: func() (oam.LoweringResult, error) { return rule.LowerTrait(&resolved, lctx) },
		}
	}
}

// builtinPurityCalls returns one call per built-in lowering rule, keyed
// "component/<type>" or "trait/<type>".
func builtinPurityCalls() map[string]loweringCall {
	calls := map[string]loweringCall{}
	for typ, rule := range builtinComponentLoweringRules() {
		calls["component/"+typ] = componentPurityCall(typ, rule)
	}
	for typ, rule := range builtinTraitLoweringRules() {
		calls["trait/"+typ] = traitPurityCall(typ, rule)
	}
	return calls
}

// TestLoweringPurity_RulesAreDeterministic runs every built-in lowering rule
// purityRuns times on identical inputs and requires identical results, with a
// different rule run in between so state shared across rules shows.
func TestLoweringPurity_RulesAreDeterministic(t *testing.T) {
	calls := builtinPurityCalls()
	keys := slices.Sorted(maps.Keys(calls))
	if len(keys) == 0 {
		t.Fatal("no built-in lowering rules found")
	}
	for i, key := range keys {
		t.Run(key, func(t *testing.T) {
			other := calls[keys[(i+1)%len(keys)]]
			between := func() {
				if _, err := other(t).run(); err != nil {
					t.Fatalf("interleaved rule %s failed: %v", keys[(i+1)%len(keys)], err)
				}
			}
			checkLoweringPurity(t, t, key, calls[key], between)
		})
	}
}

// TestLoweringPurity_CoversEveryRule fails when a component, trait, document or
// policy rule is registered on the built-in transformer that the checks above do
// not run, and when a registered component or trait rule has no fixture to run
// it on. A raw-document rule cannot be seen from here: LowerableTypes leaves
// those out by design and no accessor lists them, so none is registered by
// newBuiltinTransformer today (build.go) and a future one has to join this check
// by hand.
func TestLoweringPurity_CoversEveryRule(t *testing.T) {
	lt := newBuiltinTransformer().LowerableTypes()
	if len(lt.Kinds) != 0 || len(lt.PolicyTypes) != 0 {
		t.Errorf("document kinds %v or policy types %v are lowered by a built-in rule: add them to the purity check", lt.Kinds, lt.PolicyTypes)
	}
	want := []string{}
	for _, typ := range lt.ComponentTypes {
		want = append(want, "component/"+typ)
		if _, ok := componentLabelFixtures[typ]; !ok {
			t.Errorf("component rule %q has no entry in componentLabelFixtures: add one so the purity check has an input for it", typ)
		}
	}
	for _, typ := range lt.TraitTypes {
		want = append(want, "trait/"+typ)
		if _, ok := traitLabelFixtures[typ]; !ok {
			t.Errorf("trait rule %q has no entry in traitLabelFixtures: add one so the purity check has an input for it", typ)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(builtinPurityCalls())); !slices.Equal(got, want) {
		t.Errorf("the purity check runs %v, the transformer lowers %v", got, want)
	}
}

// Rule doubles that break the contract in the ways the check exists for.
// The check must report each, or it proves nothing about the real rules.

var purityCounter int

type counterRule struct{}

func (counterRule) ComponentType() string { return "counter" }
func (counterRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	purityCounter++
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment",
		Properties: map[string]any{"n": purityCounter}}}}, nil
}

type clockRule struct{}

func (clockRule) ComponentType() string { return "clock" }
func (clockRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment",
		Properties: map[string]any{"at": time.Now().UnixNano()}}}}, nil
}

type mapOrderRule struct{}

func (mapOrderRule) ComponentType() string { return "maporder" }
func (mapOrderRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	// 64 keys: a small map iterates in a rotation of one of few orders, so a
	// short one would pass a few percent of the time.
	set := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		set[fmt.Sprintf("k%02d", i)] = struct{}{}
	}
	var keys []any
	for k := range set {
		keys = append(keys, k)
	}
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment",
		Properties: map[string]any{"keys": keys}}}}, nil
}

// sharedMapRule changes its output on every call but returns the same
// Properties map each time, so a comparison that keeps the first result and
// reads it after the next call sees the latest value on both sides.
type sharedMapRule struct{ props map[string]any }

func (sharedMapRule) ComponentType() string { return "sharedmap" }
func (r sharedMapRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	purityCounter++
	r.props["n"] = purityCounter
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment", Properties: r.props}}}, nil
}

// typeFlipRule alternates one property between a []byte and a []int holding the
// same numbers.
type typeFlipRule struct{ calls *int }

func (typeFlipRule) ComponentType() string { return "typeflip" }
func (r typeFlipRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	*r.calls++
	var v any = []byte{65}
	if *r.calls%2 == 0 {
		v = []int{65}
	}
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment",
		Properties: map[string]any{"v": v}}}}, nil
}

type structA struct{ X int }
type structB struct{ X int }

// shapeFlipRule alternates one property between values a comparison that looks
// only at leaf numbers would treat as equal: two struct types with the same
// fields, an int and a string map key, and a slice that contains itself.
type shapeFlipRule struct {
	name  string
	calls *int
	flip  func(n int) any
}

func (r shapeFlipRule) ComponentType() string { return r.name }
func (r shapeFlipRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	*r.calls++
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment",
		Properties: map[string]any{"v": r.flip(*r.calls)}}}}, nil
}

// deepRule changes a value beneath more nesting than the comparison follows.
type deepRule struct{}

func (deepRule) ComponentType() string { return "deep" }
func (deepRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	purityCounter++
	var v any = purityCounter
	for i := 0; i < maxFlattenDepth+10; i++ {
		v = map[string]any{"x": v}
	}
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment",
		Properties: map[string]any{"v": v}}}}, nil
}

func TestLoweringPurity_CheckReportsImpureRules(t *testing.T) {
	for _, rule := range []oam.ComponentLoweringRule{
		counterRule{}, clockRule{}, mapOrderRule{}, sharedMapRule{props: map[string]any{}},
		typeFlipRule{calls: new(int)}, deepRule{},
		shapeFlipRule{name: "structflip", calls: new(int), flip: func(n int) any {
			if n%2 == 0 {
				return structA{1}
			}
			return structB{1}
		}},
		// Two types with the same package name, type name and underlying value.
		shapeFlipRule{name: "pkgnameflip", calls: new(int), flip: func(n int) any {
			if n%2 == 0 {
				return corev1.ConditionTrue
			}
			return metav1.ConditionTrue
		}},
		// An empty slice has no element to tell the two apart, only its type.
		shapeFlipRule{name: "anonstructflip", calls: new(int), flip: func(n int) any {
			if n%2 == 0 {
				return []struct{ S corev1.ConditionStatus }{}
			}
			return []struct{ S metav1.ConditionStatus }{}
		}},
		shapeFlipRule{name: "mapkeyflip", calls: new(int), flip: func(n int) any {
			if n%2 == 0 {
				return map[any]int{1: 7}
			}
			return map[any]int{"1": 7}
		}},
		shapeFlipRule{name: "selfslice", calls: new(int), flip: func(n int) any {
			s := make([]any, 2)
			s[0], s[1] = s, n
			return s[:1]
		}},
	} {
		t.Run(rule.ComponentType(), func(t *testing.T) {
			call := func(t *testing.T) (oam.LoweringResult, error) {
				comp := &oam.Component{Name: "purity", Type: rule.ComponentType()}
				return rule.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			}
			rec := &recordingReporter{}
			checkLoweringPurity(t, rec, rule.ComponentType(), plainCall(call), nil)
			if len(rec.errs) == 0 {
				t.Fatalf("an impure rule (%s) passed the purity check", rule.ComponentType())
			}
		})
	}
}

// constKey prints the same for every value, as a type with a lossy String
// method would.
type constKey string

func (constKey) String() string { return "k" }

// maskedKey hides its only field behind String.
type maskedKey struct{ a int }

func (maskedKey) String() string { return "k" }

// hiddenKey has an unexported field and no String method.
type hiddenKey struct {
	a int
	b string
}

// TestLoweringPurity_MapKeys checks that a map key is named by its value, never
// through a String method, and that a key which has no stable name makes the
// comparison incomplete instead of equal. Each case alternates the key a rule
// returns between two values (flip is given the call number).
func TestLoweringPurity_MapKeys(t *testing.T) {
	negZero := math.Copysign(0, -1)
	for _, tc := range []struct {
		name    string
		flip    func(n int) any
		wantErr string // "" = the rule is pure and must pass
	}{
		{"String method hides the key", func(n int) any {
			if n%2 == 0 {
				return map[constKey]int{"a": 1}
			}
			return map[constKey]int{"b": 1}
		}, "differs from run 1"},
		{"String method on a struct key", func(n int) any {
			return map[maskedKey]int{{a: n % 2}: 1}
		}, "differs from run 1"},
		{"unexported field of a struct key", func(n int) any {
			return map[hiddenKey]int{{a: 1, b: string(rune('a' + n%2))}: 1}
		}, "differs from run 1"},
		{"array key", func(n int) any {
			return map[[2]int]int{{1, n % 2}: 1}
		}, "differs from run 1"},
		{"pointer key", func(int) any {
			return map[*int]int{new(int): 1}
		}, "cannot be compared completely"},
		{"NaN key", func(int) any {
			return map[float64]int{math.NaN(): 1}
		}, "cannot be compared completely"},
		{"pointer inside an interface key", func(int) any {
			return map[any]int{new(int): 1}
		}, "cannot be compared completely"},
		{"pointer inside a struct key", func(int) any {
			return map[struct{ P *int }]int{{new(int)}: 1}
		}, "cannot be compared completely"},
		// Controls: none of these may be reported.
		{"same String method, same key", func(int) any {
			return map[constKey]int{"a": 1}
		}, ""},
		{"-0 and +0 are one key", func(n int) any {
			if n%2 == 0 {
				return map[float64]int{negZero: 1}
			}
			return map[float64]int{0: 1}
		}, ""},
		{"struct key with unexported fields", func(int) any {
			return map[hiddenKey]int{{a: 1, b: "x"}: 1, {a: 2, b: "y"}: 2}
		}, ""},
		{"nil interface key", func(int) any {
			return map[any]int{nil: 1, 1: 2}
		}, ""},
		{"array key, same value", func(int) any {
			return map[[2]int]int{{1, 2}: 1}
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			call := func(t *testing.T) (oam.LoweringResult, error) {
				calls++
				return oam.LoweringResult{Components: []oam.Component{{Name: "purity", Type: "deployment",
					Properties: map[string]any{"v": tc.flip(calls)}}}}, nil
			}
			rec := &recordingReporter{}
			checkLoweringPurity(t, rec, "keys", plainCall(call), nil)
			switch {
			case tc.wantErr == "" && len(rec.errs) != 0:
				t.Fatalf("a pure rule was reported: %v", rec.errs)
			case tc.wantErr != "" && (len(rec.errs) == 0 || !strings.Contains(rec.errs[0], tc.wantErr)):
				t.Fatalf("want a report containing %q, got %v", tc.wantErr, rec.errs)
			}
		})
	}
}

// changeRule applies change to what it is handed and returns the same result
// every time, so only a comparison of its inputs can tell it from a pure rule.
type changeRule struct {
	change func(comp *oam.Component, trait *oam.Trait, lctx oam.LoweringContext)
}

func (changeRule) ComponentType() string { return "change" }
func (changeRule) TraitType() string     { return "expose" }
func (r changeRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	r.change(comp, nil, lctx)
	return oam.LoweringResult{Components: []oam.Component{{Name: comp.Name, Type: "deployment"}}}, nil
}
func (r changeRule) LowerTrait(trait *oam.Trait, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	r.change(nil, trait, lctx)
	return oam.LoweringResult{Components: []oam.Component{{Name: "purity", Type: "deployment"}}}, nil
}

// changeComponentCall hands a changeRule a two-component document, one trait and
// one capability, all built afresh.
func changeComponentCall(rule changeRule) loweringCall {
	return func(t *testing.T) loweringRun {
		doc := &oam.Application{}
		doc.Metadata.Name = "purity"
		doc.Spec.Components = []oam.Component{
			{Name: "purity", Type: "change",
				Properties: map[string]any{"x": 1, "nested": map[string]any{"y": 1}},
				Traits:     []oam.Trait{{Type: "t", Properties: map[string]any{"a": 1}}}},
			{Name: "other", Type: "change", Properties: map[string]any{"z": 1}},
		}
		// The engine hands a rule a copy of the document's component
		// (lowering.go: comp := doc.Spec.Components[i]): the two share their maps
		// and slices but not their fields, so a rule that replaces a field of its
		// element does not replace the document's.
		// It also gets its own trait slice (forwardableTraits, lowering.go:1389),
		// whose elements share their Properties maps with the document's.
		element := doc.Spec.Components[0]
		element.Traits = slices.Clone(element.Traits)
		comp := &element
		caps := map[string]oam.CapabilityBinding{"cap": {Rendering: map[string]any{"r": 1}}}
		lctx := purityContext(doc, comp, "", 0).WithCapabilities(caps)
		return loweringRun{
			inputs: []purityInput{{"element", comp}, {"document", doc}, {"capabilities", caps}},
			run:    func() (oam.LoweringResult, error) { return rule.LowerComponent(comp, lctx) },
		}
	}
}

// TestLoweringPurity_ReportsRulesThatChangeTheirInputs: a rule that writes to an
// input is impure even when its result never changes, and the result comparison
// cannot see it. Each case that must be reported has a control that reads the
// same input without writing to it.
func TestLoweringPurity_ReportsRulesThatChangeTheirInputs(t *testing.T) {
	type change = func(comp *oam.Component, trait *oam.Trait, lctx oam.LoweringContext)
	for _, tc := range []struct {
		name   string
		change change
		report bool
	}{
		{"element property", func(comp *oam.Component, _ *oam.Trait, _ oam.LoweringContext) {
			comp.Properties["x"] = 2
		}, true},
		{"nested element property", func(comp *oam.Component, _ *oam.Trait, _ oam.LoweringContext) {
			comp.Properties["nested"].(map[string]any)["y"] = 2
		}, true},
		{"element trait", func(comp *oam.Component, _ *oam.Trait, _ oam.LoweringContext) {
			comp.Traits[0].Properties["a"] = 2
		}, true},
		{"another component of the document", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			lctx.Document.Spec.Components[1].Properties["z"] = 2
		}, true},
		{"component list of the document", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			lctx.Document.Spec.Components = append(lctx.Document.Spec.Components, oam.Component{Name: "extra"})
		}, true},
		{"capability rendering", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			binding, _ := lctx.Capability("cap")
			binding.Rendering["r"] = 2
		}, true},
		// The element's trait slice is a copy too: replacing a trait's Properties
		// leaves the document's map in place.
		{"document trait map behind a replaced trait field", func(comp *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			comp.Traits[0].Properties = maps.Clone(comp.Traits[0].Properties)
			lctx.Document.Spec.Components[0].Traits[0].Properties["scratch"] = true
			delete(comp.Traits[0].Properties, "scratch")
		}, true},
		// The element is a copy of the document's component: replacing its
		// Properties leaves the document's map in place, and a write to that map
		// is a change to the document even after the copy's key is deleted.
		{"document map behind a replaced element field", func(comp *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			comp.Properties = maps.Clone(comp.Properties)
			lctx.Document.Spec.Components[0].Properties["scratch"] = true
			delete(comp.Properties, "scratch")
		}, true},
		// Controls.
		{"reads only", func(comp *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			_ = comp.Properties["x"]
			_ = lctx.Document.Spec.Components[1].Name
			_, _ = lctx.Capability("cap")
		}, false},
		{"writes to a copy", func(comp *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			props := maps.Clone(comp.Properties)
			props["x"] = 2
			binding, _ := lctx.Capability("cap")
			rendering := maps.Clone(binding.Rendering)
			rendering["r"] = 2
		}, false},
		{"allocates names", func(comp *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			_, _ = lctx.Namer.Name(comp.Name, "svc", lctx.Origin)
		}, false},
	} {
		t.Run("component/"+tc.name, func(t *testing.T) {
			rec := &recordingReporter{}
			checkLoweringPurity(t, rec, tc.name, changeComponentCall(changeRule{tc.change}), nil)
			assertInputsReport(t, rec, tc.report)
		})
	}

	// The trait position hands the rule a different set of inputs: the trait
	// merged with its capability rendering, the host's member as the enclosing
	// component, and the document as the host left it.
	for _, tc := range []struct {
		name   string
		change change
		report bool
	}{
		{"trait property", func(_ *oam.Component, trait *oam.Trait, _ oam.LoweringContext) {
			trait.Properties["rules"].([]any)[0].(map[string]any)["host"] = "changed.example.com"
		}, true},
		{"enclosing component", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			lctx.Component.Properties = map[string]any{"changed": true}
		}, true},
		{"document", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			lctx.Document.Spec.Components[0].Name = "changed"
		}, true},
		{"document map behind a replaced enclosing field", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			for i := range lctx.Document.Spec.Components {
				c := &lctx.Document.Spec.Components[i]
				if c.Name == lctx.Component.Name && c.Type == lctx.Component.Type && c.Properties != nil {
					lctx.Component.Properties = maps.Clone(c.Properties)
					c.Properties["scratch"] = true
					delete(lctx.Component.Properties, "scratch")
				}
			}
		}, true},
		// The trait's maps are a deep copy of the rendering: a write to the
		// capability's nested map stays there when the trait's copy is cleaned up.
		{"capability nested rendering behind a trait copy", func(_ *oam.Component, trait *oam.Trait, lctx oam.LoweringContext) {
			binding, _ := lctx.Capability("expose")
			for k, v := range binding.Rendering {
				nested, ok := v.(map[string]any)
				theirs, mine := trait.Properties[k].(map[string]any)
				if ok && mine {
					nested["scratch"] = true
					delete(theirs, "scratch")
					return
				}
			}
			t.Fatal("the expose rendering has no nested map the trait also carries")
		}, true},
		{"capability rendering", func(_ *oam.Component, _ *oam.Trait, lctx oam.LoweringContext) {
			binding, ok := lctx.Capability("expose")
			if !ok || binding.Rendering == nil {
				t.Fatal("the test profile binds no expose rendering to change")
			}
			binding.Rendering["changed"] = true
		}, true},
		{"reads only", func(_ *oam.Component, trait *oam.Trait, lctx oam.LoweringContext) {
			_ = trait.Properties["rules"]
			_ = lctx.Component.Name
			_, _ = lctx.Capability("expose")
		}, false},
	} {
		t.Run("trait/"+tc.name, func(t *testing.T) {
			rec := &recordingReporter{}
			checkLoweringPurity(t, rec, tc.name, traitPurityCall("expose", changeRule{tc.change}), nil)
			assertInputsReport(t, rec, tc.report)
		})
	}
}

func assertInputsReport(t *testing.T, rec *recordingReporter, report bool) {
	t.Helper()
	switch {
	case report && (len(rec.errs) == 0 || !strings.Contains(rec.errs[0], "changed its input")):
		t.Fatalf("a rule that changes its input was not reported as such: %v", rec.errs)
	case !report && len(rec.errs) != 0:
		t.Fatalf("a rule that leaves its inputs alone was reported: %v", rec.errs)
	}
}

// TestLoweringPurity_PureRulePasses is the control for the test above: the
// check must not report a rule that keeps no state.
func TestLoweringPurity_PureRulePasses(t *testing.T) {
	call := func(t *testing.T) (oam.LoweringResult, error) {
		return oam.LoweringResult{Components: []oam.Component{{Name: "purity", Type: "deployment",
			Properties: map[string]any{"keys": []any{"a", "b"}}}}}, nil
	}
	rec := &recordingReporter{}
	checkLoweringPurity(t, rec, "pure", plainCall(call), nil)
	if len(rec.errs) != 0 {
		t.Fatalf("a pure rule was reported: %v", rec.errs)
	}
}

// buildBytes runs `kurel build` in process and returns what it prints.
func buildBytes(t *testing.T, args ...string) string {
	t.Helper()
	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"build"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kurel build %v failed: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// TestLoweringPurity_BuildIsRepeatable builds every testdata scenario twice in
// one process and requires byte-identical output: the fixpoint loop, the
// shared NameAllocator and the ordering across rules all sit on this path.
func TestLoweringPurity_BuildIsRepeatable(t *testing.T) {
	scenarios, err := filepath.Glob("testdata/*/app.yaml")
	if err != nil || len(scenarios) == 0 {
		t.Fatalf("no testdata scenarios found (err %v)", err)
	}
	for _, appPath := range scenarios {
		dir := filepath.Dir(appPath)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			args := []string{appPath, "--profile", filepath.Join(dir, "cluster.yaml")}
			if vp := filepath.Join(dir, "values.yaml"); fileExists(vp) {
				args = append(args, "--values", vp)
			}
			first := buildBytes(t, args...)
			second := buildBytes(t, args...)
			if first != second {
				t.Errorf("two builds of %s differ\nfirst:\n%s\nsecond:\n%s", dir, first, second)
			}
		})
	}
}

// TestLoweringPurity_FixtureBuildsAreRepeatable does the same for every
// component and trait fixture the label invariant already renders.
func TestLoweringPurity_FixtureBuildsAreRepeatable(t *testing.T) {
	dir := t.TempDir()
	profile := writeTempFile(t, dir, "cluster.yaml", labelInvariantProfile)
	build := func(t *testing.T, app map[string]any, file string) string {
		data, err := yaml.Marshal(app)
		if err != nil {
			t.Fatalf("marshaling %s: %v", file, err)
		}
		return buildBytes(t, writeTempFile(t, dir, file, string(data)), "--profile", profile)
	}
	check := func(t *testing.T, app map[string]any) {
		t.Helper()
		first, second := build(t, app, "app.yaml"), build(t, app, "app.yaml")
		if first != second {
			t.Errorf("two builds differ\nfirst:\n%s\nsecond:\n%s", first, second)
		}
		if strings.TrimSpace(first) == "" {
			t.Error("build printed nothing")
		}
	}
	for _, typ := range slices.Sorted(maps.Keys(componentLabelFixtures)) {
		t.Run("component/"+typ, func(t *testing.T) {
			name, props := componentLabelFixtures[typ].directly(t, "purity")
			check(t, labelInvariantApp(name, typ, props, "", nil))
		})
	}
	for _, typ := range slices.Sorted(maps.Keys(traitLabelFixtures)) {
		t.Run("trait/"+typ, func(t *testing.T) {
			fx := traitLabelFixtures[typ]
			host, hostProps := webserviceHost()
			if fx.host != "" {
				host, hostProps = fx.host, fx.hostProps
			}
			check(t, labelInvariantApp("purity", host, hostProps, typ, fx.props))
		})
	}
}
