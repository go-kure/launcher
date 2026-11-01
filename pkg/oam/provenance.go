package oam

import (
	"maps"
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
	eachLeaf("", final, func(path string, v any) {
		sv, inSchema := s.schema[path]
		if !inSchema || !reflect.DeepEqual(sv, v) {
			out[path] = detailHandlerDefault
			return
		}
		if pv, inProfile := s.profile[path]; inProfile && reflect.DeepEqual(pv, v) {
			out[path] = detailProfile
			return
		}
		out[path] = detailSchemaDefault
	})
	return out
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
	return Source{Kind: SourceCapability, Ref: r.ref, Detail: r.detail[path]}
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
		r.entries[p] = Source{Kind: SourceCapability, Ref: ref, Detail: detail[p]}
	})
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
		if was, ok := before[p]; ok && reflect.DeepEqual(was, now[p]) {
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

// snapshotLeaf deep-copies a leaf that holds a map, slice or array, keeping its Go
// type. Unlike copyRenderedValue it accepts what a profile may hold before its
// handler normalizes it (a null inside a typed collection), because the snapshot
// exists to be compared with that handler's result.
func snapshotLeaf(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return snapshotValue(rv, map[uintptr]bool{}).Interface()
	default:
		return v
	}
}

// snapshotValue copies maps, slices and arrays recursively, through interface
// values. Any other value, a struct or pointer included, is kept as it is. onPath
// holds the maps and slices being copied above rv, so a collection that contains
// itself is left shared instead of looping.
func snapshotValue(rv reflect.Value, onPath map[uintptr]bool) reflect.Value {
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		out := reflect.New(rv.Type()).Elem()
		out.Set(snapshotValue(rv.Elem(), onPath))
		return out
	case reflect.Map:
		if rv.IsNil() || onPath[rv.Pointer()] {
			return rv
		}
		onPath[rv.Pointer()] = true
		defer delete(onPath, rv.Pointer())
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
		if onPath[rv.Pointer()] {
			return rv
		}
		onPath[rv.Pointer()] = true
		defer delete(onPath, rv.Pointer())
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
func eachLeaf(path string, v any, fn func(path string, v any)) {
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
		for _, k := range slices.Sorted(maps.Keys(x)) {
			eachLeaf(joinPath(path, k), x[k], fn)
		}
	case []any:
		if len(x) == 0 {
			fn(path, v)
			return
		}
		for i, e := range x {
			eachLeaf(path+"["+strconv.Itoa(i)+"]", e, fn)
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
