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
)

// Source is the origin of one property value.
type Source struct {
	Kind SourceKind `json:"kind" yaml:"kind"`
	// Ref names the capability key that supplied a SourceCapability value. For a
	// value a component's nested filler supplied it is the capability the filler
	// read, or the sorted keys joined by "," when it read several: the engine does
	// not see which read produced which value.
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

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
	// ref is the capability key the rendering being merged came from.
	ref string
}

// bind sets the capability key later capability entries name.
func (r *ownerRecorder) bind(ref string) {
	if r != nil {
		r.ref = ref
	}
}

// capability is the source of a value the bound rendering supplied.
func (r *ownerRecorder) capability() Source {
	return Source{Kind: SourceCapability, Ref: r.ref}
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

// seedCapability records every leaf of a rendering, before its merge with the
// authored properties decides which survive. The merge visits authored keys only,
// so a rendering key the author never mentions has no event of its own.
func (r *ownerRecorder) seedCapability(rendering map[string]any) {
	if r == nil {
		return
	}
	eachLeaf("", rendering, func(path string, _ any) {
		r.entries[path] = r.capability()
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

// leafValues returns the leaves of props by path.
func leafValues(props map[string]any) map[string]any {
	out := map[string]any{}
	eachLeaf("", props, func(path string, v any) { out[path] = v })
	return out
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
