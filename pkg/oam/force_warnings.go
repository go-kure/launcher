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
// Objects are read as Flux applies them: a list envelope (an unstructured object
// whose items is an array) stands for its members, recursively, and a member is
// forced by its own annotation, not the envelope's. Each object, keyed by API
// group, kind, namespace and name, is warned once, naming the first application
// that generates it and every reason any copy of it is forced.
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
				annotated := obj.GetAnnotations()[fluxForceAnnotation] == fluxForceAnnotationEnabled
				if !annotated && !app.Forced {
					continue
				}
				id := objectIdentity{kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()}
				v, seen := found[id]
				if !seen {
					v = &forcedVolume{kind: gvk.Kind, name: qualifiedName(obj.GetNamespace(), obj.GetName()), app: app}
					found[id] = v
					order = append(order, id)
				}
				v.annotated = v.annotated || annotated
				v.forced = v.forced || app.Forced
			}
		}
	}
	for _, id := range order {
		t.warnHandler(found[id].warning())
	}
}

// appliedObjects returns obj as Flux applies it. A list envelope — an unstructured
// object whose items is an array, apimachinery's IsList — expands to its members,
// recursively, as Kustomize's build and Flux's ReadObjects expand it; a member that
// is not an object is dropped (Flux fails such an apply). Anything else is itself.
func appliedObjects(obj client.Object) []client.Object {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || !u.IsList() {
		return []client.Object{obj}
	}
	var out []client.Object
	for _, item := range u.Object["items"].([]any) {
		if m, ok := item.(map[string]any); ok {
			out = append(out, appliedObjects(&unstructured.Unstructured{Object: m})...)
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
