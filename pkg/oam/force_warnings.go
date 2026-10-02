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
type forcedVolume struct {
	kind, name        string
	app               GeneratedApplication
	annotated, forced bool
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
// It warns and changes nothing: the force is the author's choice. A caller passes
// GenerateApplications' result, after CheckInDocumentCollisions; the warnings follow
// generation order. With no warning handler it does nothing. A nil entry, or a nil
// object inside one, is skipped.
func (t *Transformer) WarnForcedVolumes(apps []GeneratedApplication) {
	if t.warnHandler == nil {
		return
	}
	var order []objectIdentity
	found := map[objectIdentity]*forcedVolume{}
	for _, app := range apps {
		for _, p := range app.Objects {
			if p == nil || isNullValue(*p) {
				continue
			}
			for _, obj := range appliedObjects(*p) {
				gvk := obj.GetObjectKind().GroupVersionKind()
				if gvk.Group != "" || (gvk.Kind != "PersistentVolume" && gvk.Kind != "PersistentVolumeClaim") {
					continue
				}
				id := objectIdentity{kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()}
				v, seen := found[id]
				if !seen {
					v = &forcedVolume{kind: gvk.Kind, name: qualifiedName(obj.GetNamespace(), obj.GetName()), app: app}
					found[id] = v
					order = append(order, id)
				}
				v.annotated = v.annotated || forceSelected(obj)
				v.forced = v.forced || app.Forced
			}
		}
	}
	for _, id := range order {
		if v := found[id]; v.annotated || v.forced {
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
		u, ok := built.(*unstructured.Unstructured)
		if !ok || !u.IsList() {
			out = append(out, built)
			continue
		}
		out = append(out, listMembers(u)...)
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
	if v.annotated {
		reasons = append(reasons, fluxForceAnnotation+": "+fluxForceAnnotationEnabled)
	}
	if v.forced {
		reasons = append(reasons, "its bundle's reconciliation policy sets force: true")
	}
	return fmt.Sprintf("%s %s (%s) is force-applied (%s): when an update changes an immutable field, "+
		"Flux deletes and recreates it instead of failing the apply, which can lose its data",
		v.kind, v.name, v.app, strings.Join(reasons, "; "))
}
