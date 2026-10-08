package oam

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack/layout"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// Object kind policy (go-kure/launcher#922). A consumer that keeps some kinds of
// object out of a build, or every cluster-scoped one, says so through
// ObjectKindPolicy. One check holds every application to it, and it sits in the
// ownership wrapper (ownedConfig) beside the reserved metadata keys: the
// transform's last step wraps every application's config in one, so the check
// reads each object the build emits, whatever emitted it, and each object a list
// envelope stands for when Flux applies it (appliedObjects). It reads what
// Generate returns, and what a layout augmenter adds (a chart rendered at build
// time); not what a chart Flux installs renders on the cluster.

// objectKindWildcard is the Kind of an entry that matches every kind of its
// group.
const objectKindWildcard = "*"

// objectKindRules is ObjectKindPolicy as the check reads it. A nil one holds the
// build to nothing: the policy does not implement the interface.
type objectKindRules struct {
	allowed   objectKindSet
	forbidden objectKindSet
	// allowCluster is AllowClusterScopedObjects.
	allowCluster bool
}

// objectKindSet is a list of kinds as the check matches it.
type objectKindSet struct {
	kinds map[schema.GroupKind]struct{}
	// groups holds the groups of the wildcard entries.
	groups map[string]struct{}
}

// objectKindRulesOf returns policy's ObjectKindPolicy as the check reads it, nil
// when policy does not implement it. An entry with no Kind is refused: it would
// match nothing, and a forbidden one that matches nothing lets through what it
// was written to keep out.
func objectKindRulesOf(policy Policy) (*objectKindRules, error) {
	p, ok := policy.(ObjectKindPolicy)
	if !ok || isNullValue(p) {
		return nil, nil
	}
	allowed, err := parseObjectKinds("AllowedObjectKinds", p.AllowedObjectKinds())
	if err != nil {
		return nil, err
	}
	forbidden, err := parseObjectKinds("ForbiddenObjectKinds", p.ForbiddenObjectKinds())
	if err != nil {
		return nil, err
	}
	return &objectKindRules{allowed: allowed, forbidden: forbidden, allowCluster: p.AllowClusterScopedObjects()}, nil
}

func parseObjectKinds(method string, entries []schema.GroupKind) (objectKindSet, error) {
	set := objectKindSet{kinds: map[schema.GroupKind]struct{}{}, groups: map[string]struct{}{}}
	for i, gk := range entries {
		switch gk.Kind {
		case "":
			return set, errors.Errorf("invalid ObjectKindPolicy.%s()[%d] %q: no kind; %q matches every kind of the group", method, i, gk.String(), objectKindWildcard)
		case objectKindWildcard:
			set.groups[gk.Group] = struct{}{}
		default:
			set.kinds[gk] = struct{}{}
		}
	}
	return set, nil
}

func (s objectKindSet) empty() bool { return len(s.kinds) == 0 && len(s.groups) == 0 }

func (s objectKindSet) matches(gk schema.GroupKind) bool {
	if _, ok := s.kinds[gk]; ok {
		return true
	}
	_, ok := s.groups[gk.Group]
	return ok
}

// appliedSelfAndMembers returns obj, and every object it stands for when Flux
// applies it (appliedObjects) that is not obj itself.
func appliedSelfAndMembers(obj client.Object) []client.Object {
	out := []client.Object{obj}
	if u, ok := obj.(*unstructured.Unstructured); ok {
		for _, applied := range appliedObjects(u) {
			if applied != obj {
				out = append(out, applied)
			}
		}
	}
	return out
}

// checkObjectKinds holds every object obj stands for when Flux applies it
// (appliedObjects) to the policy: obj itself, or a list envelope's members and
// not the envelope, which Flux never applies (kindAppliedObjects). Nil rules
// check nothing.
func (o *ownedConfig) checkObjectKinds(obj client.Object) error {
	if o.kinds == nil {
		return nil
	}
	for _, applied := range kindAppliedObjects(obj) {
		if err := o.checkObjectKind(applied); err != nil {
			return err
		}
	}
	return nil
}

// kindAppliedObjects is appliedObjects of obj as it is written (asWritten), so a
// list of any Go representation stands for its members, with the List envelopes
// Kustomize drops for a null items removed first (pruneNullLists): a List whose
// kind ends in "List" is inlined at every depth of Lists, and any other envelope
// is expanded one level, as Flux does. The pruning works on the written copy
// only; the objects the other checks read are left as they are.
func kindAppliedObjects(obj client.Object) []client.Object {
	written := asWritten(obj)
	if u, ok := written.(*unstructured.Unstructured); ok && written != obj && pruneNullLists(u.Object) {
		return nil
	}
	return appliedObjects(written)
}

// pruneNullLists reports whether m is a List envelope Kustomize drops, one whose
// kind ends in "List" and whose items is null. Otherwise it removes every such
// envelope from the items of m, when Kustomize inlines m, and so on down the
// Lists it inlines.
func pruneNullLists(m map[string]any) bool {
	kind, _ := m["kind"].(string)
	if !strings.HasSuffix(kind, "List") {
		return false
	}
	items, present := m["items"]
	if present && items == nil {
		return true
	}
	members, isArray := items.([]any)
	if !isArray {
		return false
	}
	kept := make([]any, 0, len(members))
	for _, member := range members {
		if mm, ok := member.(map[string]any); ok && pruneNullLists(mm) {
			continue
		}
		kept = append(kept, member)
	}
	m["items"] = kept
	return false
}

// asWritten returns obj as the manifest kure writes for it when it has items:
// its whole JSON encoding, the form Kustomize and Flux expand. appliedObjects
// reads only unstructured items arrays of []any whose members are maps, which a
// typed list, a typed Go slice or a typed member is not until encoded, at any
// depth. Any other object, or one that does not encode, which the writer fails
// on too, is returned as is.
func asWritten(obj client.Object) client.Object {
	if isNullValue(obj) {
		return obj
	}
	if u, ok := obj.(*unstructured.Unstructured); ok {
		if _, has := u.Object["items"]; !has {
			return obj
		}
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return obj
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return obj
	}
	if _, has := m["items"]; !has {
		return obj
	}
	return &unstructured.Unstructured{Object: m}
}

// layoutGenerators is the configMapGenerator entries on a layout and on its
// child layouts, each told apart by the layout that holds it and its name.
type layoutGenerators map[layoutGenerator]struct{}

type layoutGenerator struct {
	layout *layout.ManifestLayout
	name   string
}

// collect adds every configMapGenerator entry on l and on its child layouts to g.
func (g layoutGenerators) collect(l *layout.ManifestLayout) {
	if l == nil {
		return
	}
	for _, gen := range l.ConfigMapGenerators {
		g[layoutGenerator{l, gen.Name}] = struct{}{}
	}
	for _, c := range l.Children {
		g.collect(c)
	}
}

// checkAddedGenerators holds to the policy the ConfigMap of every
// configMapGenerator entry on l and on its child layouts that is not in before:
// one the wrapped augmenter added under a name new to its layout, which
// Kustomize turns into a ConfigMap at build time and no Generate returns. An
// entry under a name its layout already held is the caller's and is not read;
// one the augmenter adds beside it under that name is a duplicate, which kure
// refuses when it writes the layout.
func (o *ownedConfig) checkAddedGenerators(l *layout.ManifestLayout, before layoutGenerators) error {
	if l == nil || o.kinds == nil {
		return nil
	}
	for _, gen := range l.ConfigMapGenerators {
		if _, was := before[layoutGenerator{l, gen.Name}]; was {
			continue
		}
		cm := &unstructured.Unstructured{}
		cm.SetAPIVersion("v1")
		cm.SetKind("ConfigMap")
		cm.SetName(gen.Name)
		cm.SetNamespace(l.Namespace)
		if err := o.checkObjectKind(cm); err != nil {
			return err
		}
	}
	for _, c := range l.Children {
		if err := o.checkAddedGenerators(c, before); err != nil {
			return err
		}
	}
	return nil
}

// recordEmitted adds obj to emitted, the objects one Generate returned, when the
// policy has object kind rules: AugmentLayout reads them again (recheckEmitted).
func (o *ownedConfig) recordEmitted(emitted layoutResources, obj client.Object) {
	if id, ok := resourceIdentity(obj); o.kinds != nil && ok {
		emitted[id] = struct{}{}
	}
}

// setEmitted makes emitted the objects the latest Generate returned, in place of
// the earlier ones, so a config generated again and again holds one generation's
// objects, not every one's. A Generate that runs between another and the
// AugmentLayout that follows it therefore leaves only its own objects to be read
// again: kure's walker generates an application and then augments its layout.
func (o *ownedConfig) setEmitted(emitted layoutResources) {
	if o.kinds == nil {
		return
	}
	o.emittedMu.Lock()
	defer o.emittedMu.Unlock()
	o.emitted = emitted
}

// recheckEmitted holds again to the policy every resource on l and on its child
// layouts that was there before the wrapped augmenter ran (before) and that this
// wrapper's Generate returned: an augmenter may edit in place an object its
// Generate returned, a list envelope's items included. What the augmenter added
// was checked by stampAdded; an object a consumer put on the layout is not read.
func (o *ownedConfig) recheckEmitted(l *layout.ManifestLayout, before layoutResources) error {
	if l == nil || o.kinds == nil {
		return nil
	}
	for _, r := range l.Resources {
		id, ok := resourceIdentity(r)
		if !ok {
			continue
		}
		if _, was := before[id]; !was {
			continue
		}
		o.emittedMu.Lock()
		_, mine := o.emitted[id]
		o.emittedMu.Unlock()
		if !mine {
			continue
		}
		if err := o.checkObjectKinds(r); err != nil {
			return err
		}
	}
	for _, c := range l.Children {
		if err := o.recheckEmitted(c, before); err != nil {
			return err
		}
	}
	return nil
}

// checkObjectKind refuses obj when the policy keeps its kind out of the build,
// or its scope: a ViolationError of the application's owner, whose cause is a
// PolicyRefusal of class RefusalObjectKind naming the object.
//
// The kind is the one the object states, else its Go type's
// (objectGroupVersionKind). An object whose kind cannot be told is refused: the
// check cannot hold it to the policy. The scope is kure's (manifest.Scope); a
// kind whose scope kure does not know is taken as cluster-scoped, whatever
// namespace the object states, so the check fails closed: the namespace is the
// author's to write, and the API server ignores it on a cluster-scoped kind.
//
// The scope a CustomResourceDefinition in the build gives its kind is not read:
// it could not change the outcome. A CRD is itself cluster-scoped, so a policy
// that does not allow cluster-scoped objects refuses the build that emits it,
// and a policy that does allow them reads no scope. A kind whose CRD is
// installed apart from the build is of unknown scope here, and refused.
func (o *ownedConfig) checkObjectKind(obj client.Object) error {
	gvk, ok := objectGroupVersionKind(obj)
	if !ok {
		return o.objectKindViolation(fmt.Sprintf("%T %q: its kind cannot be told, so it cannot be held to the object kind policy", obj, obj.GetName()))
	}
	gk := gvk.GroupKind()
	where := fmt.Sprintf("%s %q (%s)", gvk.Kind, obj.GetName(), groupKindLabel(gk))
	if o.kinds.forbidden.matches(gk) {
		return o.objectKindViolation(where + ": the object kind policy forbids the kind")
	}
	if !o.kinds.allowed.empty() && !o.kinds.allowed.matches(gk) {
		return o.objectKindViolation(where + ": the kind is not among those the object kind policy allows")
	}
	if o.kinds.allowCluster {
		return nil
	}
	probe := &unstructured.Unstructured{}
	probe.SetGroupVersionKind(gvk)
	switch manifest.Scope(probe, nil) {
	case manifest.ScopeCluster:
		return o.objectKindViolation(where + ": the kind is cluster-scoped, and the object kind policy does not allow cluster-scoped objects")
	case manifest.ScopeUnknown:
		return o.objectKindViolation(where + ": the build does not know the kind's scope (it is neither a built-in kind nor one kure registers), so it is taken as cluster-scoped, which the object kind policy does not allow; a namespace the object states does not make it namespaced")
	case manifest.ScopeNamespaced:
	}
	return nil
}

// objectKindViolation is the refusal of an object of the wrapper's application:
// its owning component's, or, for an application the document as a whole owns,
// the one the application came from (entry).
func (o *ownedConfig) objectKindViolation(message string) error {
	owner := o.component
	if owner == "" {
		owner = o.entry
	}
	return NewViolationError(owner, NewPolicyRefusal(RefusalObjectKind, message))
}

// objectGroupVersionKind returns the kind obj states, else the one its Go type
// is registered under (kure's scheme), when that is one group and kind.
func objectGroupVersionKind(obj client.Object) (schema.GroupVersionKind, bool) {
	if gvk := obj.GetObjectKind().GroupVersionKind(); gvk.Kind != "" {
		return gvk, true
	}
	if group, kind := statedOrTypedKind(obj); kind != "" {
		return schema.GroupVersionKind{Group: group, Kind: kind}, true
	}
	// A scheme that fails to register leaves the kind untold: the object is
	// refused, not passed.
	if err := kubernetes.RegisterSchemes(); err != nil {
		return schema.GroupVersionKind{}, false
	}
	gvks, _, err := kubernetes.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		return schema.GroupVersionKind{}, false
	}
	gk := gvks[0].GroupKind()
	if slices.ContainsFunc(gvks[1:], func(g schema.GroupVersionKind) bool { return g.GroupKind() != gk }) {
		return schema.GroupVersionKind{}, false
	}
	return gvks[0], true
}

// groupKindLabel writes gk as "group/Kind", the core group as "core".
func groupKindLabel(gk schema.GroupKind) string {
	group := gk.Group
	if group == "" {
		group = "core"
	}
	return strings.Join([]string{group, gk.Kind}, "/")
}
