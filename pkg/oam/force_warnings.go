package oam

import (
	"fmt"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Flux kustomize-controller's force-apply annotation, as an author writes it on
// an object: an object carrying it is deleted and recreated when a server-side
// apply fails on an immutable field. Launcher writes it nowhere; kure's Flux
// workflow sets it for an application's ForceReplace delivery intent
// (go-kure/launcher#782).
const (
	fluxForceAnnotation        = stack.AnnotationFluxForceKey
	fluxForceAnnotationEnabled = stack.AnnotationFluxForceEnabled
)

// forcedVolume is one PersistentVolume or PersistentVolumeClaim that is
// force-applied or under the ForceReplace delivery intent, with the first
// application that generates it and every reason it is forced.
// annotated is set by any copy generated with the force key enabled, forceReplace
// by any copy in an application with the ForceReplace delivery intent, and
// bundleForce by any copy in an application whose bundle sets Force.
type forcedVolume struct {
	kind, name, producer                 string
	annotated, forceReplace, bundleForce bool
}

// forceScan collects the volumes of a document that are warned about, in
// generation order.
type forceScan struct {
	order []objectIdentity
	found map[objectIdentity]*forcedVolume
}

// add records one copy of a volume app generates, annotated if the copy carries
// the force key enabled. Whether the application forces it is
// GeneratedApplication.Forced alone; why is what GenerateApplications recorded
// beside it: the ForceReplace intent, its bundle's Force. A Forced application
// that names neither was built by a caller and is read as its bundle's force,
// the one meaning Forced had before the intent existed.
func (s *forceScan) add(id objectIdentity, app GeneratedApplication, annotated bool) {
	v, seen := s.found[id]
	if !seen {
		v = &forcedVolume{kind: id.kind, name: qualifiedName(id.namespace, id.name), producer: app.String()}
		s.found[id] = v
		s.order = append(s.order, id)
	}
	v.annotated = v.annotated || annotated
	if app.Forced {
		v.forceReplace = v.forceReplace || app.forceReplace
		v.bundleForce = v.bundleForce || app.bundleForce || !app.forceReplace
	}
}

// forced reports whether the volume is warned about: something force-applies it,
// or its application sets the intent.
func (v *forcedVolume) forced() bool {
	return v.annotated || v.forceReplace || v.bundleForce
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

// addGenerated records the volumes of apps as generated.
func (s *forceScan) addGenerated(apps []GeneratedApplication) {
	for _, app := range apps {
		for _, p := range generatedObjects(app) {
			for _, obj := range appliedObjects(*p) {
				if id, ok := volumeIdentity(obj); ok {
					s.add(id, app, forceSelected(obj))
				}
			}
		}
	}
}

// fluxExpanded is Flux's ReadObjects step for one built resource: a list envelope
// (items is an array, kind unchecked) stands for its members, one level only.
func fluxExpanded(obj client.Object) []client.Object {
	if u, ok := obj.(*unstructured.Unstructured); ok && u.IsList() {
		return listMembers(u)
	}
	return []client.Object{obj}
}

// WarnForcedVolumes emits one warning through the warning handler (SetWarningHandler)
// for every PersistentVolume and PersistentVolumeClaim in apps that is
// force-applied or to be (GeneratedApplication.Forced, or the object's own
// metadata): one in an application with the ForceReplace delivery intent (the
// force-replace trait sets it on everything its component owns), one in an
// application whose bundle sets Force, or one an author wrote
// kustomize.toolkit.fluxcd.io/force: enabled on. A force-applied object is deleted
// and recreated when an update changes an immutable field, instead of the apply
// failing, and a claim's data can be lost with it.
//
// The intent is read from the application, not from the objects: launcher writes
// no force annotation, the workflow that delivers the application does
// (go-kure/launcher#782). The objects alone therefore do not carry the intent's
// effect, and a volume whose only reason is the intent is warned about
// conditionally: the warning says what happens where the delivery workflow maps
// the intent, which kure's Flux layout integration does. An object an author
// annotated is read as Flux's force selector matches it: the force key in its
// labels or its annotations, with the value enabled in any letter case.
//
// Objects are read as Flux applies them (appliedObjects): a list envelope stands for
// its members, and a member is forced by its own metadata, not the envelope's. Each
// object, keyed by API group, kind, namespace and name, is warned once, naming the
// first application that generates it and every reason any copy of it is forced.
//
// Objects are read as generated. What the delivering consumer does to them
// afterwards (patches of its own, post-build substitution) and anything the
// cluster changes on apply are not modelled: launcher sets none of those
// (go-kure/launcher#781).
//
// It warns and changes nothing: the force is the author's choice. A caller passes
// GenerateApplications' result, after CheckInDocumentCollisions; the warnings
// follow generation order. With no warning handler it does nothing. A nil entry,
// or a nil object inside one, is skipped.
func (t *Transformer) WarnForcedVolumes(apps []GeneratedApplication) {
	if t.warnHandler == nil {
		return
	}
	scan := forceScan{found: map[objectIdentity]*forcedVolume{}}
	scan.addGenerated(apps)
	for _, id := range scan.order {
		if v := scan.found[id]; v.forced() {
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

// warning is the volume's warning text. A volume the generated objects or its
// bundle force is stated as force-applied, with every reason. One whose only
// reason is the delivery intent is stated conditionally: nothing in the objects
// forces it, the workflow that maps the intent does.
func (v *forcedVolume) warning() string {
	if v.forceReplace && !v.annotated && !v.bundleForce {
		return fmt.Sprintf("%s %s (%s) is covered by the force-replace delivery intent of its application: "+
			"where the delivery workflow maps that intent (kure's Flux layout integration writes %s: %s), "+
			"an update that changes an immutable field deletes and recreates it instead of failing the apply, "+
			"which can lose its data",
			v.kind, v.name, v.producer, fluxForceAnnotation, fluxForceAnnotationEnabled)
	}
	var reasons []string
	if v.annotated {
		reasons = append(reasons, fluxForceAnnotation+": "+fluxForceAnnotationEnabled)
	}
	if v.forceReplace {
		reasons = append(reasons, "its application sets the force-replace delivery intent")
	}
	if v.bundleForce {
		reasons = append(reasons, "its bundle sets force: true")
	}
	return fmt.Sprintf("%s %s (%s) is force-applied (%s): when an update changes an immutable field, "+
		"Flux deletes and recreates it instead of failing the apply, which can lose its data",
		v.kind, v.name, v.producer, strings.Join(reasons, "; "))
}
