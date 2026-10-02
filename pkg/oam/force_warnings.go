package oam

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Flux kustomize-controller's force-apply annotation, as the force-replace trait
// sets it: an object carrying it is deleted and recreated when a server-side apply
// fails on an immutable field.
const (
	fluxForceAnnotation        = "kustomize.toolkit.fluxcd.io/force"
	fluxForceAnnotationEnabled = "enabled"
)

// forcedVolume is one force-applied PersistentVolume or PersistentVolumeClaim, with
// the first application that generates it and every reason it is forced.
// annotated is set by any copy Flux would apply with the force key enabled;
// generatedAnnotated by one that already carried it as generated, so a force key
// only a bundle's patches add is named as theirs.
type forcedVolume struct {
	kind, name                            string
	app                                   GeneratedApplication
	annotated, generatedAnnotated, forced bool
}

// forceScan collects the force-applied volumes of a document in generation order.
type forceScan struct {
	order []objectIdentity
	found map[objectIdentity]*forcedVolume
}

// add records one copy of a volume: app generates it, annotated if Flux applies it
// with the force key enabled, generatedAnnotated if it was generated so.
func (s *forceScan) add(id objectIdentity, app GeneratedApplication, annotated, generatedAnnotated bool) {
	v, seen := s.found[id]
	if !seen {
		v = &forcedVolume{kind: id.kind, name: qualifiedName(id.namespace, id.name), app: app}
		s.found[id] = v
		s.order = append(s.order, id)
	}
	v.annotated = v.annotated || annotated
	v.generatedAnnotated = v.generatedAnnotated || generatedAnnotated
	v.forced = v.forced || app.Forced
}

// volumeIdentity returns obj's identity if it is a core PersistentVolume or
// PersistentVolumeClaim.
func volumeIdentity(obj client.Object) (objectIdentity, bool) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Group != "" || (gvk.Kind != "PersistentVolume" && gvk.Kind != "PersistentVolumeClaim") {
		return objectIdentity{}, false
	}
	return objectIdentity{kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()}, true
}

// generatedObjects returns app's objects, nil entries and nil objects skipped.
func generatedObjects(app GeneratedApplication) []*client.Object {
	var out []*client.Object
	for _, p := range app.Objects {
		if p != nil && !isNullValue(*p) {
			out = append(out, p)
		}
	}
	return out
}

// addUnpatched records the volumes of apps as generated.
func (s *forceScan) addUnpatched(apps []GeneratedApplication) {
	for _, app := range apps {
		for _, p := range generatedObjects(app) {
			for _, obj := range appliedObjects(*p) {
				if id, ok := volumeIdentity(obj); ok {
					selected := forceSelected(obj)
					s.add(id, app, selected, selected)
				}
			}
		}
	}
}

// addPatched records the volumes of one leaf bundle's apps as Flux applies them
// after the bundle's patches. kustomize keeps its resources in input order and a
// patch never reorders them, so each built resource is traced to the generated
// resource it was built from: the next one with its identity, skipping those a
// patch deleted, or, when its identity was not generated at all, the next one in
// order (a patch renamed it; a rename after a deletion in one bundle is read as
// renaming the deleted one). Its volumes, a list envelope's members included, are
// named by that resource's application and read as patched; a force key is named
// as the patches' unless the volume was generated with it. A patch can add a
// volume, as a member of a list envelope it extends, so a bundle with no volume is
// built too.
func (s *forceScan) addPatched(apps []GeneratedApplication) error {
	type generatedResource struct {
		app GeneratedApplication
		obj client.Object
		id  objectIdentity
	}
	var objects []*client.Object
	var inputs []generatedResource
	inputIDs := map[objectIdentity]bool{}
	generated := map[objectIdentity]bool{}
	for _, app := range apps {
		for _, p := range generatedObjects(app) {
			objects = append(objects, p)
			for _, r := range kustomizeInlined(*p) {
				id := resourceIdentity(r)
				inputs = append(inputs, generatedResource{app: app, obj: r, id: id})
				inputIDs[id] = true
				for _, obj := range fluxExpanded(r) {
					if vid, ok := volumeIdentity(obj); ok {
						generated[vid] = generated[vid] || forceSelected(obj)
					}
				}
			}
		}
	}
	built, err := applyBundlePatches(objects, apps[0].Patches)
	if err != nil {
		return err
	}
	next := 0
	for _, out := range built {
		outID := resourceIdentity(out)
		producer, origin := apps[0], client.Object(nil)
		for next < len(inputs) {
			in := inputs[next]
			next++
			if in.id == outID || !inputIDs[outID] {
				producer, origin = in.app, in.obj
				break
			}
		}
		for _, obj := range fluxExpanded(out) {
			id, ok := volumeIdentity(obj)
			if !ok {
				continue
			}
			wasSelected, wasGenerated := generated[id]
			if !wasGenerated && origin != nil {
				if _, isVolume := volumeIdentity(origin); isVolume {
					wasSelected = forceSelected(origin)
				}
			}
			selected := forceSelected(obj)
			s.add(id, producer, selected, selected && wasSelected)
		}
	}
	return nil
}

// resourceIdentity keys obj as kustomize and Flux key a resource: API group, kind,
// namespace and name.
func resourceIdentity(obj client.Object) objectIdentity {
	gvk := obj.GetObjectKind().GroupVersionKind()
	return objectIdentity{group: gvk.Group, kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()}
}

// fluxExpanded is Flux's ReadObjects step for one built resource: a list envelope
// (items is an array, kind unchecked) stands for its members, one level only.
func fluxExpanded(obj client.Object) []client.Object {
	if u, ok := obj.(*unstructured.Unstructured); ok && u.IsList() {
		return listMembers(u)
	}
	return []client.Object{obj}
}

// bundleEnd returns the end of the run of apps, from start, that one leaf bundle
// generated: the applications GenerateApplications generated from one bundle, or
// a single application a caller built.
func bundleEnd(apps []GeneratedApplication, start int) int {
	end := start + 1
	if apps[start].bundle == nil {
		return end
	}
	for end < len(apps) && apps[end].bundle == apps[start].bundle {
		end++
	}
	return end
}

// WarnForcedVolumes emits one warning through the warning handler (SetWarningHandler)
// for every PersistentVolume and PersistentVolumeClaim in apps that Flux
// force-applies: one carrying kustomize.toolkit.fluxcd.io/force: enabled (the
// force-replace trait sets it), or one in an application whose bundle sets Force
// (GeneratedApplication.Forced, from a reconciliation policy's force: true). Flux
// then deletes and recreates the object when an update changes an immutable field,
// instead of failing the apply, and a claim's data can be lost with it.
//
// An object is annotated as Flux's force selector matches it: the force key in its
// labels or its annotations, with the value enabled in any letter case.
//
// Objects are read as Flux applies them (appliedObjects): a list envelope stands for
// its members, and a member is forced by its own metadata, not the envelope's. Each
// object, keyed by API group, kind, namespace and name, is warned once, naming the
// first application that generates it and every reason any copy of it is forced.
//
// A bundle's patches (GeneratedApplication.Patches, from the fluxcd-patches trait)
// are applied first, as Flux applies its Kustomization's spec.patches: the objects
// of a leaf bundle with patches are built with kustomize and the patched copies are
// read (applyBundlePatches), each named by the application that generated it. A
// patch can add the force key, remove or disable it, delete or rename the volume,
// or add one to a list envelope; a force key only patches add is named as theirs. A
// patch set that does not build is warned once, naming the bundle's first
// application and the build error, and that bundle's objects are read unpatched.
// postBuild substitution and anything the cluster changes on apply are not
// modelled.
//
// It warns and changes nothing: the force is the author's choice. A caller passes
// GenerateApplications' result, after CheckInDocumentCollisions; the volume warnings
// follow generation order, after any patch build warning. An application a caller
// built (not from GenerateApplications) is patched on its own. With no warning
// handler it does nothing. A nil entry, or a nil object inside one, is skipped.
func (t *Transformer) WarnForcedVolumes(apps []GeneratedApplication) {
	if t.warnHandler == nil {
		return
	}
	scan := forceScan{found: map[objectIdentity]*forcedVolume{}}
	for start := 0; start < len(apps); {
		end := bundleEnd(apps, start)
		bundle := apps[start:end]
		start = end
		if len(bundle[0].Patches) == 0 {
			scan.addUnpatched(bundle)
			continue
		}
		if err := scan.addPatched(bundle); err != nil {
			t.warnHandler(fmt.Sprintf("the patches of the bundle of %s could not be applied, so its "+
				"force-applied volumes are checked as generated, without them: %v", bundle[0], err))
			scan.addUnpatched(bundle)
		}
	}
	for _, id := range scan.order {
		if v := scan.found[id]; v.annotated || v.forced {
			t.warnHandler(v.warning())
		}
	}
}

// forceSelected reports whether Flux's force selector matches obj, as fluxcd/pkg/ssa
// AnyInMetadata matches it: the force key's label or annotation is enabled, compared
// without regard to case.
func forceSelected(obj client.Object) bool {
	return strings.EqualFold(obj.GetLabels()[fluxForceAnnotation], fluxForceAnnotationEnabled) ||
		strings.EqualFold(obj.GetAnnotations()[fluxForceAnnotation], fluxForceAnnotationEnabled)
}

// appliedObjects returns the objects Flux applies for obj, in two stages, as
// kustomize-controller builds and then reads its source. Kustomize's build
// (kustomize/api resource/factory.go, inlineAnyEmbeddedLists) replaces an object
// whose kind ends in "List" and whose items is an array with its members,
// recursively. Flux's ReadObjects (fluxcd/pkg/ssa) then replaces every remaining
// list envelope (apimachinery's IsList: items is an array, kind unchecked) with its
// members, one level only. A member that is not an object is dropped, as either
// stage fails such an apply.
func appliedObjects(obj client.Object) []client.Object {
	var out []client.Object
	for _, built := range kustomizeInlined(obj) {
		out = append(out, fluxExpanded(built)...)
	}
	return out
}

// kustomizeInlined is Kustomize's list inlining: an unstructured object whose kind
// ends in "List" and whose items is an array stands for its items, recursively; any
// other object is itself. (Kustomize also drops a List whose items is null; no
// volume is lost with it.)
func kustomizeInlined(obj client.Object) []client.Object {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || !strings.HasSuffix(u.GetKind(), "List") {
		return []client.Object{obj}
	}
	if _, isArray := u.Object["items"].([]any); !isArray {
		return []client.Object{obj}
	}
	var out []client.Object
	for _, member := range listMembers(u) {
		out = append(out, kustomizeInlined(member)...)
	}
	return out
}

// listMembers returns the object members of an unstructured list's items array.
func listMembers(u *unstructured.Unstructured) []client.Object {
	items, _ := u.Object["items"].([]any)
	var out []client.Object
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, &unstructured.Unstructured{Object: m})
		}
	}
	return out
}

func (v *forcedVolume) warning() string {
	var reasons []string
	switch {
	case v.generatedAnnotated:
		reasons = append(reasons, fluxForceAnnotation+": "+fluxForceAnnotationEnabled)
	case v.annotated:
		reasons = append(reasons, fluxForceAnnotation+": "+fluxForceAnnotationEnabled+", set by its bundle's patches")
	}
	if v.forced {
		reasons = append(reasons, "its bundle's reconciliation policy sets force: true")
	}
	return fmt.Sprintf("%s %s (%s) is force-applied (%s): when an update changes an immutable field, "+
		"Flux deletes and recreates it instead of failing the apply, which can lose its data",
		v.kind, v.name, v.app, strings.Join(reasons, "; "))
}
