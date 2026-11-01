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
	// SourceLowered is a value of an element a lowering rule emitted; Source.Ref is
	// the rule's identity (Origin.Rule). The report does not split what the rule
	// copied from authored input from what it rendered.
	SourceLowered SourceKind = "lowered"
)

// Source is the origin of one property value.
type Source struct {
	Kind SourceKind `json:"kind" yaml:"kind"`
	// Ref names the lowering rule of a SourceLowered value, and the capability key
	// that supplied a SourceCapability value. For a
	// value a component's nested filler supplied it is the capability the filler
	// read, or the sorted keys joined by "," when it read several: the engine does
	// not see which read produced which value.
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
	// Detail says, for a SourceCapability value whose profile went through
	// EvaluateProfileWithProvenance, which step of the profile evaluation supplied
	// it: "profile" (the profile document), "schema-default" (a CapabilityDefinition
	// default) or "handler-default" (the trait handler's or lowering rule's
	// ValidateAndApplyDefaults). Empty means not determined, never "profile": it is
	// empty for a profile evaluated with EvaluateProfile, for a binding built by
	// hand, for a value a component's nested filler supplied, and for every kind
	// but SourceCapability.
	Detail string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// The Source.Detail values.
const (
	detailProfile        = "profile"
	detailSchemaDefault  = "schema-default"
	detailHandlerDefault = "handler-default"
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
// list is its own leaf, and a null is none. The winner is reported, never a
// source it shadowed. Entries are sorted by (Component, Member, Trait,
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
// Values from a policy are not in the report: no policy writes a value into a
// property map. See the package README for what the report does not cover.
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

// defaultSteps holds leaf snapshots of one rendering: as the profile gave it and as
// the schema-defaults step left it. A handler rewrites the rendering it is handed in
// place, so both are taken before the next step runs, never kept as references.
type defaultSteps struct {
	profile map[string]any
	schema  map[string]any
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
	// A typed collection is one leaf of a snapshot, but a handler may hand it back as
	// map[string]any or []any, which eachLeaf walks into. The final leaves under it are
	// then compared with the elements of the typed value, so an element the handler left
	// alone keeps the origin of the step that supplied it.
	schemaIn, profileIn := collectionElements(s.schema), collectionElements(s.profile)
	eachLeaf("", final, func(path string, v any) {
		sv, inSchema := snapshotAt(s.schema, schemaIn, path)
		if !inSchema || !sameLeafValue(sv, v) {
			out[path] = detailHandlerDefault
			return
		}
		if pv, inProfile := snapshotAt(s.profile, profileIn, path); inProfile && sameLeafValue(pv, v) {
			out[path] = detailProfile
			return
		}
		out[path] = detailSchemaDefault
	})
	return out
}

// sameLeafValue reports whether two leaf values are the same value. It is
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
		for _, k := range x.MapKeys() {
			yv := y.MapIndex(k)
			if !yv.IsValid() || !sameValue(x.MapIndex(k), yv, seen) {
				return false
			}
		}
		return true
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
	default:
		// A func, channel or unsafe pointer: DeepEqual's verdict stands.
		return false
	}
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

// lowered records every leaf of props as the output of the lowering rule.
func (r *ownerRecorder) lowered(props map[string]any, rule string) {
	if r == nil {
		return
	}
	eachLeaf("", props, func(path string, _ any) {
		r.entries[path] = Source{Kind: SourceLowered, Ref: rule}
	})
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
// leaf set of the properties it was handed, after the one of the properties it
// returned; reads is the set of capability keys it read during the call. A leaf
// that is new or holds a different value is a capability leaf, a leaf that is gone
// has no entry.
func (r *ownerRecorder) nestedFill(before map[string]any, after map[string]any, reads map[string]struct{}) {
	if r == nil {
		return
	}
	now := leafValues(after)
	for p := range before {
		if _, ok := now[p]; !ok {
			delete(r.entries, p)
		}
	}
	src := Source{Kind: SourceCapability, Ref: strings.Join(slices.Sorted(maps.Keys(reads)), ",")}
	for _, p := range slices.Sorted(maps.Keys(now)) {
		if was, ok := before[p]; ok && sameLeafValue(was, now[p]) {
			continue
		}
		r.dropAncestors(p)
		r.entries[p] = src
	}
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

// leafValues returns the leaves of props by path, as a snapshot: a leaf that is a
// typed map or slice (map[string]string, []string) is copied, because a handler may
// change one in place after the snapshot is taken.
func leafValues(props map[string]any) map[string]any {
	out := map[string]any{}
	eachLeaf("", props, func(path string, v any) { out[path] = snapshotLeaf(v) })
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

// collectionElements returns the elements inside each typed map or slice leaf of snap, by
// the path eachLeaf would give them had the collection been a map[string]any or a
// []any: a string-keyed map adds ".key", a slice or array adds "[i]". Descent goes
// through interface values and stops at any other value, which is an element. A
// collection that contains itself is not entered again.
func collectionElements(snap map[string]any) map[string]any {
	out := map[string]any{}
	for _, path := range slices.Sorted(maps.Keys(snap)) {
		rv := reflect.ValueOf(snap[path])
		switch rv.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array:
			collectionElementsWalk(path, rv, map[collectionID]bool{}, out)
		default:
		}
	}
	return out
}

// collectionElementsWalk records the elements of rv, a typed collection at path, into out.
// The collection itself is not recorded: the snapshot already holds it as one leaf.
func collectionElementsWalk(path string, rv reflect.Value, onPath map[collectionID]bool, out map[string]any) {
	for rv.Kind() == reflect.Interface && !rv.IsNil() {
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String || rv.Len() == 0 || onPath[idOf(rv)] {
			return
		}
		onPath[idOf(rv)] = true
		defer delete(onPath, idOf(rv))
		for it := rv.MapRange(); it.Next(); {
			collectionElementsAt(joinPath(path, it.Key().String()), it.Value(), onPath, out)
		}
	case reflect.Slice:
		if rv.Len() == 0 || onPath[idOf(rv)] {
			return
		}
		onPath[idOf(rv)] = true
		defer delete(onPath, idOf(rv))
		for i := range rv.Len() {
			collectionElementsAt(path+"["+strconv.Itoa(i)+"]", rv.Index(i), onPath, out)
		}
	case reflect.Array:
		for i := range rv.Len() {
			collectionElementsAt(path+"["+strconv.Itoa(i)+"]", rv.Index(i), onPath, out)
		}
	default:
	}
}

// collectionElementsAt records one element of a typed collection: a null is no element,
// anything else is one. A nested non-empty collection is recorded as a whole and its
// own elements are entered too, because a handler may return the outer collection as a
// map[string]any that still holds the inner one typed (the final leaf is then the inner
// collection itself, not its elements).
func collectionElementsAt(path string, rv reflect.Value, onPath map[collectionID]bool, out map[string]any) {
	for rv.Kind() == reflect.Interface && !rv.IsNil() {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Interface {
		return
	}
	if rv.CanInterface() {
		out[path] = rv.Interface()
	}
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		if rv.Len() > 0 {
			collectionElementsWalk(path, rv, onPath, out)
		}
	default:
	}
}

// snapshotAt returns the value a snapshot holds at path: the leaf itself, else the
// element of a typed collection leaf that eachLeaf reaches at path.
func snapshotAt(snap, elements map[string]any, path string) (any, bool) {
	if v, ok := snap[path]; ok {
		return v, true
	}
	v, ok := elements[path]
	return v, ok
}

// snapshotLeaf deep-copies a leaf that holds a map, slice or array, keeping its Go
// type. Unlike copyRenderedValue it accepts what a profile may hold before its
// handler normalizes it (a null inside a typed collection), because the snapshot
// exists to be compared with that handler's result.
func snapshotLeaf(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return snapshotValue(rv, map[collectionID]bool{}).Interface()
	default:
		return v
	}
}

// snapshotValue copies maps, slices and arrays recursively, through interface
// values. Any other value, a struct or pointer included, is kept as it is. onPath
// holds the maps and slices being copied above rv, so a collection that contains
// itself is left shared instead of looping.
func snapshotValue(rv reflect.Value, onPath map[collectionID]bool) reflect.Value {
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		out := reflect.New(rv.Type()).Elem()
		out.Set(snapshotValue(rv.Elem(), onPath))
		return out
	case reflect.Map:
		if rv.IsNil() || onPath[idOf(rv)] {
			return rv
		}
		onPath[idOf(rv)] = true
		defer delete(onPath, idOf(rv))
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		for it := rv.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), snapshotValue(it.Value(), onPath))
		}
		return out
	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		if rv.Len() == 0 {
			return reflect.MakeSlice(rv.Type(), 0, 0)
		}
		if onPath[idOf(rv)] {
			return rv
		}
		onPath[idOf(rv)] = true
		defer delete(onPath, idOf(rv))
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := range rv.Len() {
			out.Index(i).Set(snapshotValue(rv.Index(i), onPath))
		}
		return out
	case reflect.Array:
		out := reflect.New(rv.Type()).Elem()
		for i := range rv.Len() {
			out.Index(i).Set(snapshotValue(rv.Index(i), onPath))
		}
		return out
	default:
		return rv
	}
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
