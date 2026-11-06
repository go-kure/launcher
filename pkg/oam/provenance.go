package oam

import (
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
)

// SourceKind names where a property value came from.
type SourceKind string

const (
	// SourceAuthored is a value in the properties the caller passed.
	SourceAuthored SourceKind = "authored"
	// SourceCapability is a value a ClusterProfile capability binding supplied;
	// Source.Ref is the binding's key.
	SourceCapability SourceKind = "capability"
	// SourceCopied is a value of an element a lowering rule emitted that the rule
	// received and passed on at the same path: the value its input held there.
	// Source.Ref is the rule's identity (Origin.Rule), Source.From the kind the value
	// had in the rule's input.
	SourceCopied SourceKind = "copied"
	// SourceRendered is a value of an element a lowering rule emitted that its input
	// did not hold at that path: the rule built it. Source.Ref is the rule's identity.
	SourceRendered SourceKind = "rendered"
	// SourcePolicy is a value the environment policy supplied because the properties
	// held none: a Default* accessor of Policy that a handler applied. Source.Ref is
	// the accessor's name (DefaultReplicas, DefaultCPURequest, ...); the policy has no
	// identity of its own to name. The path is the one the author would have written,
	// so it is not in the properties the handler received.
	SourcePolicy SourceKind = "policy"
	// SourceBuiltin is a value launcher's own code filled because nothing else held
	// one: a default a handler or a step after the policy chose, as opposed to one the
	// environment policy supplied (SourcePolicy). Source.Ref names the fill, for
	// example "postgresql.enablePDB". The path is the one the author would have
	// written, so it is not in the properties the handler received. It is not the
	// "handler-default" Detail, which says a trait handler filled a capability rendering.
	SourceBuiltin SourceKind = "builtin"
)

// Source is the origin of one property value.
type Source struct {
	Kind SourceKind `json:"kind" yaml:"kind"`
	// Ref names the lowering rule of a SourceCopied or SourceRendered value, the
	// capability key that supplied a SourceCapability value, and the Policy accessor
	// that supplied a SourcePolicy value, and the fill that produced a SourceBuiltin
	// value. For a
	// value a component's nested filler supplied it is the capability the filler
	// read, or the sorted keys joined by "," when it read several: the engine does
	// not see which read produced which value.
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
	// From says, for a SourceCopied value, which kind the value had in the rule's
	// input: SourceAuthored, SourceCapability or SourceRendered (a value an earlier
	// rule built). A value copied through several rules carries the kind it had when
	// it first entered them. Empty for every other kind, SourcePolicy and
	// SourceBuiltin included.
	From SourceKind `json:"from,omitempty" yaml:"from,omitempty"`
	// Detail says, for a SourceCapability value whose profile went through
	// EvaluateProfileWithProvenance, which step of the profile evaluation supplied
	// it: "profile" (the profile document), "schema-default" (a CapabilityDefinition
	// default) or "handler-default" (the trait handler's or lowering rule's
	// ValidateAndApplyDefaults). A SourceCopied value whose From is SourceCapability
	// keeps the Detail it had. For a SourcePolicy value it is "capped-to-limit" when
	// the value is the author's limit, which the policy's request default would have
	// exceeded, and empty otherwise. Empty means not determined, never "profile": it is
	// empty for a profile evaluated with EvaluateProfile, for a binding built by
	// hand, for a value a component's nested filler supplied, and for every kind
	// but SourceCapability, the SourceCopied values taken from one, and SourcePolicy;
	// a SourceBuiltin value has none.
	Detail string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// The Source.Detail values.
const (
	detailProfile        = "profile"
	detailSchemaDefault  = "schema-default"
	detailHandlerDefault = "handler-default"
	// DetailCappedToLimit is the Detail of a SourcePolicy value that is the author's
	// limit: the policy's request default was above it (a request above its limit is
	// refused by the API server), so the request was fixed at the limit.
	DetailCappedToLimit = "capped-to-limit"
)

// PropertySource is the source of one leaf property of a component or a trait.
type PropertySource struct {
	// Component is the component's name.
	Component string `json:"component" yaml:"component"`
	// Member is the component type of a member of a same-name sibling group, whose
	// members share a name; empty for every other component.
	Member string `json:"member,omitempty" yaml:"member,omitempty"`
	// Trait is the trait type, empty for a property of the component itself.
	Trait string `json:"trait,omitempty" yaml:"trait,omitempty"`
	// TraitIndex is the trait's index among its component's traits (among its
	// member's traits for a sibling group member); 0 when Trait is empty.
	TraitIndex int `json:"traitIndex,omitempty" yaml:"traitIndex,omitempty"`
	// Path addresses the leaf: dot-separated object keys, [i] for a list element,
	// and ["key"] (a Go-quoted string) for a key that is empty or holds ".", "[" or
	// "]". Example: volumeClaimTemplates[0].storageClass.
	Path   string `json:"path" yaml:"path"`
	Source Source `json:"source" yaml:"source"`
}

// Provenance reports, for one Application, which source supplied each leaf
// property of its components and traits, as TransformWithProvenance computed it.
//
// An entry exists for each leaf that holds a value: an empty object or an empty
// list is its own leaf, and a null is none. The one exception is a value the
// environment policy supplied (SourcePolicy) or launcher's own code filled
// (SourceBuiltin): its entry is at the path the author would have written, which
// the properties do not hold. The winner is reported,
// never a source it shadowed. Entries are sorted by (Component, Member, Trait,
// TraitIndex, Path).
type Provenance struct {
	// Application is metadata.name of the Application that was transformed.
	Application string           `json:"application" yaml:"application"`
	Entries     []PropertySource `json:"entries" yaml:"entries"`
}

// TransformWithProvenance is TransformWithPolicy plus a report of which source
// supplied each leaf property of the Application's components and traits. The
// cluster and the PolicyResult are exactly those TransformWithPolicy returns;
// TransformWithPolicy and Transform record nothing and pay nothing for the report.
//
// A default the environment policy supplied is in the report as SourcePolicy when
// the handler that applied it reports it (PolicyDefaultReporter), and a value a step
// after the policy filled is in it as SourceBuiltin when the config reports it
// (BuiltinDefaultReporter). Neither writes a value into a property map, so the entry
// addresses a path the properties do not hold. See the package README for what the
// report does not cover.
func (t *Transformer) TransformWithProvenance(app *Application, ctx TransformContext) (*stack.Cluster, *PolicyResult, *Provenance, error) {
	rec := newProvenanceRecorder()
	ctx.provenance = rec
	cluster, result, err := t.TransformWithPolicy(app, ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return cluster, result, rec.report(app.Metadata.Name), nil
}

// EvaluateProfileWithProvenance is EvaluateProfile plus, on each returned
// CapabilityBinding, the origin of every leaf of its rendering: the profile
// document, a CapabilityDefinition default or a handler default. TransformWithProvenance
// reads it and reports it as Source.Detail. The profile and the renderings it returns
// are exactly those EvaluateProfile returns; the origin is an unexported field that
// serialization ignores. A profile evaluated by EvaluateProfile, or a binding built by
// hand, yields an empty Detail.
func (t *Transformer) EvaluateProfileWithProvenance(profile *ClusterProfile) (*ClusterProfile, error) {
	return t.evaluateProfile(profile, true)
}

// defaultSteps holds snapshots of one rendering: as the profile gave it and as the
// schema-defaults step left it. A handler rewrites the rendering it is handed in
// place, so both are taken before the next step runs, never kept as references.
type defaultSteps struct {
	profile propIndex
	schema  propIndex
}

// binding returns the evaluated binding for final, with its leaf origins when stamp.
func (s defaultSteps) binding(final map[string]any, stamp bool) CapabilityBinding {
	b := CapabilityBinding{Rendering: final}
	if stamp {
		b.detail = s.detail(final)
	}
	return b
}

// detail names, for each leaf of final, the step that supplied it. A leaf the schema
// step left as it found it is the profile's, a leaf that step added or changed is the
// schema's; a leaf the handler step added or changed is the handler's. A handler can
// rewrite a leaf the profile gave (an empty issuerRef.kind), so presence alone does
// not decide.
func (s defaultSteps) detail(final map[string]any) map[string]string {
	out := map[string]string{}
	eachLeaf("", final, func(path string, v any) {
		switch {
		case !s.schema.holds(path, v):
			out[path] = detailHandlerDefault
		case s.profile.holds(path, v):
			out[path] = detailProfile
		default:
			out[path] = detailSchemaDefault
		}
	})
	return out
}

// propIndex is a snapshot of one property map for comparing it with what a later step
// returns, whatever Go shape that step gave the values. The comparison goes through one
// canonical form (canonicalValue): a typed map or slice is a map[string]any or []any
// there, on both sides. leaves holds the leaves of the map as eachLeaf names them, nodes
// every value under it at any depth, each leaf and each collection whole, typed or not.
// A later leaf is looked up in nodes, so it is found whether the step handed back the
// collection whole, in pieces or in another shape, with no special case per shape.
type propIndex struct {
	leaves map[string]any
	nodes  map[string]any
}

// indexOf snapshots props. Every collection in it is copied, because a handler may
// change one in place after the snapshot is taken.
func indexOf(props map[string]any) propIndex {
	ix := propIndex{leaves: leafValues(props), nodes: map[string]any{}}
	recordNodes("", canonicalValue(props), map[collectionID]bool{}, ix.nodes)
	return ix
}

// holds reports whether the snapshot has, at path, the value v that a later step left
// there. A value compares equal to itself in another shape, so v is canonicalized as
// the snapshot is; the raw v is tried too, for the one value canonical form keeps as it
// is (a collection met again inside itself).
func (ix propIndex) holds(path string, v any) bool {
	n, ok := ix.nodes[path]
	return ok && (sameLeafValue(n, canonicalValue(v)) || sameLeafValue(n, v))
}

// recordNodes records v, canonical, at path and every value under it. A null is no
// node, as it is no leaf. A collection met again inside itself is recorded where it is
// met, not entered a second time.
func recordNodes(path string, v any, onPath map[collectionID]bool, out map[string]any) {
	if isNullValue(v) {
		return
	}
	if path != "" {
		out[path] = v
	}
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return
		}
		id := idOf(reflect.ValueOf(x))
		if onPath[id] {
			return
		}
		onPath[id] = true
		defer delete(onPath, id)
		for k, e := range x {
			recordNodes(joinPath(path, k), e, onPath, out)
		}
	case []any:
		if len(x) == 0 {
			return
		}
		id := idOf(reflect.ValueOf(x))
		if onPath[id] {
			return
		}
		onPath[id] = true
		defer delete(onPath, id)
		for i, e := range x {
			recordNodes(path+"["+strconv.Itoa(i)+"]", e, onPath, out)
		}
	default:
	}
}

// canonicalValue returns a copy of v in canonical form: a map with string keys is a
// map[string]any, any other map a map[any]any, a slice or array a []any, all the way
// down and through interface values; a null stays a null. A pointer or a struct is deep
// copied with its types kept (copyTyped), so a change made in place afterwards does not
// reach the copy. Any other value is kept as it is. A collection that contains itself is
// not entered a second time: where it is met again it is kept as it is, so the walk ends.
func canonicalValue(v any) any {
	return canonicalWalk(reflect.ValueOf(v), map[collectionID]bool{})
}

// canonicalWalk is canonicalValue with onPath, the maps and slices being copied above rv.
func canonicalWalk(rv reflect.Value, onPath map[collectionID]bool) any {
	switch rv.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return canonicalWalk(rv.Elem(), onPath)
	case reflect.Map:
		stringKeys := rv.Type().Key().Kind() == reflect.String
		if rv.IsNil() {
			if stringKeys {
				return map[string]any(nil)
			}
			return map[any]any(nil)
		}
		if onPath[idOf(rv)] {
			return rv.Interface()
		}
		onPath[idOf(rv)] = true
		defer delete(onPath, idOf(rv))
		if !stringKeys {
			out := make(map[any]any, rv.Len())
			for it := rv.MapRange(); it.Next(); {
				out[it.Key().Interface()] = canonicalWalk(it.Value(), onPath)
			}
			return out
		}
		out := make(map[string]any, rv.Len())
		for it := rv.MapRange(); it.Next(); {
			out[it.Key().String()] = canonicalWalk(it.Value(), onPath)
		}
		return out
	case reflect.Slice:
		if rv.IsNil() {
			return []any(nil)
		}
		if rv.Len() > 0 {
			if onPath[idOf(rv)] {
				return rv.Interface()
			}
			onPath[idOf(rv)] = true
			defer delete(onPath, idOf(rv))
		}
		return canonicalElements(rv, onPath)
	case reflect.Array:
		return canonicalElements(rv, onPath)
	case reflect.Pointer, reflect.Struct:
		return copyTyped(rv, map[copyKey]reflect.Value{}).Interface()
	default:
		return rv.Interface()
	}
}

// copyKey names a pointer, map or slice already copied by copyTyped. A pointer to a
// struct and a pointer to its first field share an address, so the type is part of it.
type copyKey struct {
	typ  reflect.Type
	addr uintptr
	n    int
}

// copyTyped returns a deep copy of rv that keeps every type, so an in-place change to
// rv afterwards does not reach the copy. It follows pointers, interface values and the
// exported fields of a struct, and copies the slices, arrays and maps it meets (a map's
// keys are kept, as a pointer key is its identity). A pointer, map or slice met again,
// through a cycle or a second path, is the copy already made, so the walk ends and the
// copy has the shape of the original.
//
// An unexported field is kept as it is: reflect cannot set it. A func, a chan and an
// unsafe pointer are kept as they are too.
func copyTyped(rv reflect.Value, copies map[copyKey]reflect.Value) reflect.Value {
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		out := reflect.New(rv.Type()).Elem()
		out.Set(copyTyped(rv.Elem(), copies))
		return out
	case reflect.Pointer:
		if rv.IsNil() {
			return rv
		}
		key := copyKey{typ: rv.Type(), addr: rv.Pointer()}
		if c, ok := copies[key]; ok {
			return c
		}
		// New yields a plain *T; Convert gives a pointer of a defined type (type P *T)
		// back its own type.
		out := reflect.New(rv.Type().Elem()).Convert(rv.Type())
		copies[key] = out
		out.Elem().Set(copyTyped(rv.Elem(), copies))
		return out
	case reflect.Struct:
		out := reflect.New(rv.Type()).Elem()
		out.Set(rv)
		for i := range rv.NumField() {
			if f := out.Field(i); f.CanSet() {
				f.Set(copyTyped(rv.Field(i), copies))
			}
		}
		return out
	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		key := copyKey{typ: rv.Type(), addr: rv.Pointer(), n: rv.Len()}
		if c, ok := copies[key]; ok && rv.Len() > 0 {
			return c
		}
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		if rv.Len() > 0 {
			copies[key] = out
		}
		for i := range rv.Len() {
			out.Index(i).Set(copyTyped(rv.Index(i), copies))
		}
		return out
	case reflect.Array:
		out := reflect.New(rv.Type()).Elem()
		for i := range rv.Len() {
			out.Index(i).Set(copyTyped(rv.Index(i), copies))
		}
		return out
	case reflect.Map:
		if rv.IsNil() {
			return rv
		}
		key := copyKey{typ: rv.Type(), addr: rv.Pointer()}
		if c, ok := copies[key]; ok {
			return c
		}
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		copies[key] = out
		for it := rv.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), copyTyped(it.Value(), copies))
		}
		return out
	default:
		return rv
	}
}

// canonicalElements is the []any of the canonical elements of a slice or array.
func canonicalElements(rv reflect.Value, onPath map[collectionID]bool) []any {
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = canonicalWalk(rv.Index(i), onPath)
	}
	return out
}

// sameLeafValue reports whether two values are the same value. It is
// reflect.DeepEqual, except that a NaN equals a NaN: an unchanged NaN is the value its
// source gave, though NaN != NaN would make DeepEqual call a handler's work on it.
func sameLeafValue(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	return sameValue(reflect.ValueOf(a), reflect.ValueOf(b), map[sameVisit]bool{})
}

// sameVisit is a pair of collections already under comparison; meeting it again ends
// the walk, as reflect.DeepEqual does for a self-referential value.
type sameVisit struct {
	typ  reflect.Type
	x, y uintptr
	n    int
}

// sameValue is the deep comparison behind sameLeafValue. It is reached only after
// reflect.DeepEqual found the values different, so it looks for the one way the two
// can still be the same value: a NaN somewhere inside.
func sameValue(x, y reflect.Value, seen map[sameVisit]bool) bool {
	if !x.IsValid() || !y.IsValid() {
		return x.IsValid() == y.IsValid()
	}
	if x.Type() != y.Type() {
		return false
	}
	switch x.Kind() {
	case reflect.Float32, reflect.Float64:
		xf, yf := x.Float(), y.Float()
		return xf == yf || (math.IsNaN(xf) && math.IsNaN(yf))
	case reflect.Complex64, reflect.Complex128:
		xc, yc := x.Complex(), y.Complex()
		return (real(xc) == real(yc) || (math.IsNaN(real(xc)) && math.IsNaN(real(yc)))) &&
			(imag(xc) == imag(yc) || (math.IsNaN(imag(xc)) && math.IsNaN(imag(yc))))
	case reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return sameValue(x.Elem(), y.Elem(), seen)
	case reflect.Pointer:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		if x.Pointer() == y.Pointer() {
			return true
		}
		visit := sameVisit{typ: x.Type(), x: x.Pointer(), y: y.Pointer()}
		if seen[visit] {
			return true
		}
		seen[visit] = true
		return sameValue(x.Elem(), y.Elem(), seen)
	case reflect.Map:
		if x.IsNil() != y.IsNil() || x.Len() != y.Len() {
			return false
		}
		visit := sameVisit{typ: x.Type(), x: x.Pointer(), y: y.Pointer(), n: x.Len()}
		if seen[visit] {
			return true
		}
		seen[visit] = true
		return sameEntries(x, y, seen)
	case reflect.Slice:
		if x.IsNil() != y.IsNil() || x.Len() != y.Len() {
			return false
		}
		visit := sameVisit{typ: x.Type(), x: x.Pointer(), y: y.Pointer(), n: x.Len()}
		if seen[visit] {
			return true
		}
		seen[visit] = true
		return sameElements(x, y, seen)
	case reflect.Array:
		return sameElements(x, y, seen)
	case reflect.Struct:
		for i := range x.NumField() {
			if !sameValue(x.Field(i), y.Field(i), seen) {
				return false
			}
		}
		return true
	case reflect.Bool:
		return x.Bool() == y.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return x.Int() == y.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return x.Uint() == y.Uint()
	case reflect.String:
		return x.String() == y.String()
	case reflect.Func:
		// As DeepEqual: a func equals only another nil func.
		return x.IsNil() && y.IsNil()
	case reflect.Chan, reflect.UnsafePointer:
		return x.Pointer() == y.Pointer()
	case reflect.Invalid:
		return false
	}
	return false
}

// sameEntries compares two maps of equal length entry by entry. An entry whose key the
// other map cannot look up (a NaN key, which is not equal to itself) is matched with an
// entry of the other map that no lookup reaches either, key against key and value
// against value, in any order.
func sameEntries(x, y reflect.Value, seen map[sameVisit]bool) bool {
	type entry struct{ k, v reflect.Value }
	var xLoose, yLoose []entry
	for it := x.MapRange(); it.Next(); {
		yv := y.MapIndex(it.Key())
		if !yv.IsValid() {
			xLoose = append(xLoose, entry{it.Key(), it.Value()})
			continue
		}
		if !sameValue(it.Value(), yv, seen) {
			return false
		}
	}
	if len(xLoose) == 0 {
		return true
	}
	for it := y.MapRange(); it.Next(); {
		if !x.MapIndex(it.Key()).IsValid() {
			yLoose = append(yLoose, entry{it.Key(), it.Value()})
		}
	}
	if len(xLoose) != len(yLoose) {
		return false
	}
	used := make([]bool, len(yLoose))
	for _, e := range xLoose {
		match := false
		for i, f := range yLoose {
			if used[i] {
				continue
			}
			// A candidate that fails must leave no visited pair behind: a later candidate
			// would take that pair as equal.
			try := maps.Clone(seen)
			if sameValue(e.k, f.k, try) && sameValue(e.v, f.v, try) {
				maps.Copy(seen, try)
				used[i], match = true, true
				break
			}
		}
		if !match {
			return false
		}
	}
	return true
}

// sameElements compares two slices or arrays of equal length element by element.
func sameElements(x, y reflect.Value, seen map[sameVisit]bool) bool {
	for i := range x.Len() {
		if !sameValue(x.Index(i), y.Index(i), seen) {
			return false
		}
	}
	return true
}

// propOwner identifies the property map a leaf belongs to: a component's own
// properties (trait empty) or one trait's.
type propOwner struct {
	component string
	member    string
	trait     string
	index     int
}

// provenanceRecorder is the transform's report in progress. It exists only for
// TransformWithProvenance; every other path holds a nil one and records nothing.
type provenanceRecorder struct {
	owners map[propOwner]*ownerRecorder
}

func newProvenanceRecorder() *provenanceRecorder {
	return &provenanceRecorder{owners: map[propOwner]*ownerRecorder{}}
}

// forOwner returns the recorder for one property map, or nil on a nil receiver.
func (r *provenanceRecorder) forOwner(o propOwner) *ownerRecorder {
	if r == nil {
		return nil
	}
	rec, ok := r.owners[o]
	if !ok {
		rec = &ownerRecorder{entries: map[string]Source{}}
		r.owners[o] = rec
	}
	return rec
}

// report returns the sorted entries.
func (r *provenanceRecorder) report(application string) *Provenance {
	p := &Provenance{Application: application, Entries: []PropertySource{}}
	for o, rec := range r.owners {
		for path, src := range rec.entries {
			p.Entries = append(p.Entries, PropertySource{
				Component: o.component, Member: o.member, Trait: o.trait, TraitIndex: o.index,
				Path: path, Source: src,
			})
		}
	}
	slices.SortFunc(p.Entries, func(a, b PropertySource) int {
		if c := strings.Compare(a.Component, b.Component); c != 0 {
			return c
		}
		if c := strings.Compare(a.Member, b.Member); c != 0 {
			return c
		}
		if c := strings.Compare(a.Trait, b.Trait); c != 0 {
			return c
		}
		if a.TraitIndex != b.TraitIndex {
			return a.TraitIndex - b.TraitIndex
		}
		return strings.Compare(a.Path, b.Path)
	})
	return p
}

// ownerRecorder is the handle a merge or fill step records one property map
// through. Every method is a no-op on a nil receiver, so a step runs the same
// code with the report off; the one thing a step must still guard is building a
// path, which allocates.
type ownerRecorder struct {
	entries map[string]Source
	// ref is the capability key the rendering being merged came from, and detail
	// the origin of its leaves by path (nil when its profile was not evaluated with
	// provenance).
	ref    string
	detail map[string]string
}

// bind sets the capability key later capability entries name, and the origin of the
// bound rendering's leaves.
func (r *ownerRecorder) bind(ref string, detail map[string]string) {
	if r != nil {
		r.ref = ref
		r.detail = detail
	}
}

// capability is the source of the value at path that the bound rendering supplied.
func (r *ownerRecorder) capability(path string) Source {
	return Source{Kind: SourceCapability, Ref: r.ref, Detail: stampFor(r.detail, path)}
}

// authored records every leaf of props as authored.
func (r *ownerRecorder) authored(props map[string]any) {
	if r == nil {
		return
	}
	eachLeaf("", props, func(path string, _ any) {
		r.entries[path] = Source{Kind: SourceAuthored}
	})
}

// fromRule records every leaf of props, the output of the lowering rule, as copied
// from in or rendered by the rule (ruleInput.attribute). in is nil for an element
// whose input was not kept: every leaf is then rendered.
func (r *ownerRecorder) fromRule(in *ruleInput, props map[string]any, rule string) {
	if r == nil {
		return
	}
	for path, src := range in.attribute(props, rule) {
		r.entries[path] = src
	}
}

// ruleInput is what a lowering rule was handed, kept so the report can tell the
// leaves the rule copied from the leaves it rendered. The engine builds it only when
// a report is wanted (TransformContext.provenance is set), at the one place the rule
// is called, and stores it on the element the rule emitted; every other path holds
// nil and pays nothing.
type ruleInput struct {
	// index is a snapshot of the input's properties, taken before the rule ran.
	index propIndex
	// sources is the source of each leaf of those properties, by path.
	sources map[string]Source
}

// newRuleInput snapshots props, whose leaves have the given sources.
func newRuleInput(props map[string]any, sources map[string]Source) *ruleInput {
	return &ruleInput{index: indexOf(props), sources: sources}
}

// ruleInputFor snapshots props, the properties of an element about to be handed to
// a rule. origin and prior are the element's own: an element no rule emitted holds
// authored leaves; one an earlier rule emitted holds what that rule's attribution
// says (prior is nil when none was kept, and then every leaf is rendered).
func ruleInputFor(props map[string]any, origin *Origin, prior *ruleInput) *ruleInput {
	var sources map[string]Source
	if origin != nil && origin.Rule != "" {
		sources = prior.attribute(props, origin.Rule)
	} else {
		sources = map[string]Source{}
		eachLeaf("", props, func(path string, _ any) {
			sources[path] = Source{Kind: SourceAuthored}
		})
	}
	return newRuleInput(props, sources)
}

// attribute returns the source of each leaf of out, the properties a rule emitted
// under the identity rule. A leaf is copied when the input held the same value at the
// same path (the comparison a nested filler's output gets, nestedFill), and then it
// keeps what the input knew of it; any other leaf is rendered. A value the rule moved
// to another path is rendered. A nil receiver renders every leaf.
func (in *ruleInput) attribute(out map[string]any, rule string) map[string]Source {
	res := map[string]Source{}
	eachLeaf("", out, func(path string, v any) {
		res[path] = in.sourceOf(path, v, rule)
	})
	return res
}

// sourceOf is the source of one emitted leaf: v at path.
func (in *ruleInput) sourceOf(path string, v any, rule string) Source {
	rendered := Source{Kind: SourceRendered, Ref: rule}
	if in == nil || !in.index.holds(path, v) {
		return rendered
	}
	from, ok := nearestSource(in.sources, path)
	if !ok {
		return rendered
	}
	src := Source{Kind: SourceCopied, Ref: rule, From: from.Kind, Detail: from.Detail}
	if from.Kind == SourceCopied {
		src.From = from.From
	}
	return src
}

// nearestSource is the source recorded for the leaf at path, or for its nearest
// enclosing entry (an object a step recorded as one leaf). A value the rule gave back
// as one leaf that its input held as several (a []any handed back as a []string) has
// no entry of its own: the entries under it stand for it, when they all agree on kind,
// From and Detail. Entries that disagree name no single source, so none is returned.
func nearestSource(sources map[string]Source, path string) (Source, bool) {
	if s, ok := sources[path]; ok {
		return s, true
	}
	best, found := "", false
	for q := range sources {
		if q != "" && pathWithin(path, q) && (!found || len(q) > len(best)) {
			best, found = q, true
		}
	}
	if found {
		return sources[best], true
	}
	var agreed Source
	have := false
	for q, s := range sources {
		if path == "" || q == path || !pathWithin(q, path) {
			continue
		}
		if !have {
			agreed, have = s, true
			continue
		}
		if s.Kind != agreed.Kind || s.From != agreed.From || s.Detail != agreed.Detail {
			return Source{}, false
		}
	}
	return agreed, have
}

// seedCapability records every leaf of a rendering, before its merge with the
// authored properties decides which survive. The merge visits authored keys only,
// so a rendering key the author never mentions has no event of its own.
func (r *ownerRecorder) seedCapability(rendering map[string]any) {
	if r == nil {
		return
	}
	eachLeaf("", rendering, func(path string, _ any) {
		r.entries[path] = r.capability(path)
	})
}

// replaceCapability is replace for a value copied from the rendering of capability
// ref at the same path: each leaf carries its own origin from detail, because an
// object can hold leaves of different origins.
func (r *ownerRecorder) replaceCapability(path string, v any, ref string, detail map[string]string) {
	if r == nil {
		return
	}
	r.dropUnder(path)
	eachLeaf(path, v, func(p string, _ any) {
		r.entries[p] = Source{Kind: SourceCapability, Ref: ref, Detail: stampFor(detail, p)}
	})
}

// stampFor returns the origin recorded for the leaf at path. When the component's
// schema check expanded a typed collection the profile stamped as one leaf (a
// map[string]string at class becomes class.tier), the descendant has no stamp of its
// own and takes the one on its nearest stamped ancestor. No stamp stays empty: not
// determined.
func stampFor(detail map[string]string, path string) string {
	if d, ok := detail[path]; ok {
		return d
	}
	best := ""
	for q := range detail {
		if q != "" && len(q) > len(best) && pathWithin(path, q) {
			best = q
		}
	}
	return detail[best]
}

// policy records a default the config's ApplyPolicy applied at path: the value is
// the policy's, and the properties do not hold it. The site decided that the
// author left it out, so it replaces whatever is recorded at path. Two writers of
// one path on one owner are last-wins with the same source: the scaler and the
// pvc trait each append one sub-application, and a second trait of a type is
// another owner (its TraitIndex).
func (r *ownerRecorder) policy(path, accessor, detail string) {
	if r == nil {
		return
	}
	r.entries[path] = Source{Kind: SourcePolicy, Ref: accessor, Detail: detail}
}

// recordPolicyDefaults reads the defaults a config's ApplyPolicy just applied
// into rec. It reads only: the config and the transform's output are unchanged.
func recordPolicyDefaults(rec *ownerRecorder, config any) {
	if rec == nil {
		return
	}
	reporter, ok := config.(PolicyDefaultReporter)
	if !ok {
		return
	}
	for _, d := range reporter.PolicyDefaults() {
		rec.policy(d.Path, d.Accessor, d.Detail)
	}
}

// builtin records a value launcher's own code filled at path: the properties do not
// hold it, and ref names the fill. Like policy, it replaces whatever is recorded at
// path, and two writers of one path on one owner are last-wins.
func (r *ownerRecorder) builtin(path, ref string) {
	if r == nil {
		return
	}
	r.entries[path] = Source{Kind: SourceBuiltin, Ref: ref}
}

// recordBuiltinDefaults reads the values a config's post-policy steps filled into
// rec. It reads only: the config and the transform's output are unchanged.
func recordBuiltinDefaults(rec *ownerRecorder, config any) {
	if rec == nil {
		return
	}
	reporter, ok := config.(BuiltinDefaultReporter)
	if !ok {
		return
	}
	for _, d := range reporter.BuiltinDefaults() {
		rec.builtin(d.Path, d.Ref)
	}
}

// replace records the leaves of v, which now stand at path, as src. Whatever was
// recorded at or under path is gone: the merge replaced that value whole.
func (r *ownerRecorder) replace(path string, v any, src Source) {
	if r == nil {
		return
	}
	r.dropUnder(path)
	eachLeaf(path, v, func(p string, _ any) {
		r.entries[p] = src
	})
}

// recursed closes the recursive branch of a merge at path: res is the merged
// object, am the authored one. An empty object is its own leaf, so a seeded
// rendering leaf at path stands only while res is still empty; an empty res can
// only be the authored object (and the rendering's) over nothing.
func (r *ownerRecorder) recursed(path string, res map[string]any) {
	if r == nil {
		return
	}
	delete(r.entries, path)
	if len(res) == 0 {
		r.entries[path] = Source{Kind: SourceAuthored}
	}
}

// dropUnder deletes every entry at or under path.
func (r *ownerRecorder) dropUnder(path string) {
	for p := range r.entries {
		if pathWithin(p, path) {
			delete(r.entries, p)
		}
	}
}

// nestedFill attributes what a component's nested filler changed. before is the
// snapshot of the properties it was handed, after the properties it returned; reads is
// the set of capability keys it read during the call. A leaf that is new or holds a
// different value is a capability leaf, a leaf that is gone has no entry. A leaf that
// holds the value it had keeps its source, also when the filler handed the value back in
// another shape: a collection it returned in pieces or whole takes the source of the
// entry its value was recorded under (the collection's own, or all its elements' when
// they agree).
func (r *ownerRecorder) nestedFill(before propIndex, after map[string]any, reads map[string]struct{}) {
	if r == nil {
		return
	}
	now := map[string]any{}
	eachLeaf("", after, func(path string, v any) { now[path] = v })
	kept := map[string]Source{}
	for p, v := range now {
		if !before.holds(p, v) {
			continue
		}
		if own, ok := r.entries[p]; ok {
			kept[p] = own
		} else if src, ok := r.sourceOfValue(p); ok {
			kept[p] = src
		}
	}
	for p := range before.leaves {
		if _, ok := now[p]; !ok {
			delete(r.entries, p)
		}
	}
	src := Source{Kind: SourceCapability, Ref: strings.Join(slices.Sorted(maps.Keys(reads)), ",")}
	for _, p := range slices.Sorted(maps.Keys(now)) {
		if k, ok := kept[p]; ok {
			r.entries[p] = k
			continue
		}
		r.dropAncestors(p)
		r.entries[p] = src
	}
}

// sourceOfValue is the source recorded for the value at path when path has no entry of
// its own: that of the nearest enclosing entry (an object recorded as one leaf), else
// the one source all entries under path share. Entries that differ only in Detail share
// their kind and Ref, with Detail empty (not determined); any other difference leaves
// no one source.
func (r *ownerRecorder) sourceOfValue(path string) (Source, bool) {
	best, found := "", false
	for q := range r.entries {
		if q != path && pathWithin(path, q) && (!found || len(q) > len(best)) {
			best, found = q, true
		}
	}
	if found {
		return r.entries[best], true
	}
	var one Source
	have := false
	for q, s := range r.entries {
		if !pathWithin(q, path) {
			continue
		}
		switch {
		case !have:
			one, have = s, true
		case s.Kind != one.Kind || s.Ref != one.Ref || s.From != one.From:
			return Source{}, false
		case s.Detail != one.Detail:
			one.Detail = ""
		}
	}
	return one, have
}

// dropAncestors deletes the entries of the objects that contain path: a container
// leaf (an empty object or list) stops being one when a leaf appears inside it.
func (r *ownerRecorder) dropAncestors(path string) {
	for p := range r.entries {
		if p != path && pathWithin(path, p) {
			delete(r.entries, p)
		}
	}
}

// leafValues returns the leaves of props by path, as a snapshot in canonical form: a
// leaf that is a typed map or slice (map[string]string, []string) is copied, because a
// handler may change one in place after the snapshot is taken.
func leafValues(props map[string]any) map[string]any {
	out := map[string]any{}
	eachLeaf("", props, func(path string, v any) { out[path] = canonicalValue(v) })
	return out
}

// collectionID identifies a map or slice being walked: its address and, for a slice,
// its length. A slice and a shorter slice of the same backing array (s and s[:1]) are
// different values, so a sub-slice of an acyclic slice is not mistaken for a cycle,
// while a slice that holds a slice of itself (s[0] = s[:1]) is still caught when the
// walk meets the same (address, length) again.
type collectionID struct {
	addr uintptr
	n    int
}

// idOf returns the collectionID of rv, a non-nil map or slice.
func idOf(rv reflect.Value) collectionID {
	id := collectionID{addr: rv.Pointer()}
	if rv.Kind() == reflect.Slice {
		id.n = rv.Len()
	}
	return id
}

// eachLeaf calls fn for each leaf of v at path: a value that is not a
// map[string]any or a []any, and an empty one of those. A null is no leaf. Only
// map[string]any and []any are descended into, as mergeRenderedProperties does,
// so a typed map or slice is one leaf.
//
// A map or slice that contains itself, directly or through another, is one leaf at the
// path where the walk meets it again, never descended into twice. Only a binding built in
// Go can hold one; the walk must not run without bound on it.
func eachLeaf(path string, v any, fn func(path string, v any)) {
	eachLeafWalk(path, v, map[collectionID]bool{}, fn)
}

// eachLeafWalk is eachLeaf with onPath, the maps and slices being walked above v.
func eachLeafWalk(path string, v any, onPath map[collectionID]bool, fn func(path string, v any)) {
	if isNullValue(v) {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			if path != "" {
				fn(path, v)
			}
			return
		}
		id := idOf(reflect.ValueOf(x))
		if onPath[id] {
			fn(path, v)
			return
		}
		onPath[id] = true
		defer delete(onPath, id)
		for _, k := range slices.Sorted(maps.Keys(x)) {
			eachLeafWalk(joinPath(path, k), x[k], onPath, fn)
		}
	case []any:
		if len(x) == 0 {
			fn(path, v)
			return
		}
		id := idOf(reflect.ValueOf(x))
		if onPath[id] {
			fn(path, v)
			return
		}
		onPath[id] = true
		defer delete(onPath, id)
		for i, e := range x {
			eachLeafWalk(path+"["+strconv.Itoa(i)+"]", e, onPath, fn)
		}
	default:
		fn(path, v)
	}
}

// joinPath appends an object key to a path.
func joinPath(path, key string) string {
	if key == "" || strings.ContainsAny(key, ".[]") {
		return path + "[" + strconv.Quote(key) + "]"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// pathWithin reports whether p is path or lies under it.
func pathWithin(p, path string) bool {
	if path == "" || p == path {
		return true
	}
	return strings.HasPrefix(p, path) && (p[len(path)] == '.' || p[len(path)] == '[')
}
