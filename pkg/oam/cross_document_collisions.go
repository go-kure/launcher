package oam

import (
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// GeneratedDocument is one authored document's generated output, as a caller that
// transforms several documents holds it: the document's identity and every object
// generated from its Transform result, including any a layout walk adds.
type GeneratedDocument struct {
	Namespace string // authored metadata.namespace
	Name      string // authored metadata.name
	Kind      string // authored kind, e.g. "WebApplication"
	Objects   []*client.Object
}

func (d GeneratedDocument) String() string {
	return fmt.Sprintf("%s %q", d.Kind, qualifiedName(d.Namespace, d.Name))
}

func qualifiedName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

// objectIdentity is what makes two generated objects the same Kubernetes object:
// API group, kind, namespace and name. The version is excluded, since one object
// served under two versions is still one object.
type objectIdentity struct {
	group, kind, namespace, name string
}

func (o objectIdentity) String() string {
	kind := o.kind
	if o.group != "" {
		kind += "." + o.group
	}
	return fmt.Sprintf("%s %q", kind, qualifiedName(o.namespace, o.name))
}

// localConfigAnnotation is the annotation kustomize's build reads to leave an
// object out of its output (kustomize/api konfig IgnoredByKustomizeAnnotation).
const localConfigAnnotation = "config.kubernetes.io/local-config"

// droppedByKustomize reports whether kustomize's build leaves obj out of its
// output: the local-config annotation is present and is not the string "false"
// (kustomize/api resource/factory.go, DropLocalNodes). An empty value drops the
// object too. A dropped object is never applied, so it generates nothing.
func droppedByKustomize(obj client.Object) bool {
	v, ok := obj.GetAnnotations()[localConfigAnnotation]
	return ok && v != "false"
}

// keyedObjects returns the objects a comparison of generated objects keys for obj:
// the ones Flux applies (appliedObjects), a list envelope standing for its members,
// less the ones kustomize's build drops (droppedByKustomize). Kustomize inlines a
// "...List" envelope first and judges each member by its own annotation, so an
// envelope's own annotation is not read; an envelope that stays one object for
// kustomize (a kind that does not end in "List", with items) is dropped with all of
// its members when it carries the annotation. An object with no kind is returned
// as it is, items or not, so that it is refused as any object with no kind and not
// read past. Only obj itself is looked at: what it stands for is appliedObjects'
// to say. There a List's member with no kind that holds items of its own is one of
// the remaining envelopes: it is read by them, one level, and never keyed. Any other
// member with no kind is returned and refused.
func keyedObjects(obj client.Object) []client.Object {
	if obj.GetObjectKind().GroupVersionKind().Kind == "" {
		return []client.Object{obj}
	}
	var out []client.Object
	for _, built := range kustomizeInlined(obj) {
		if droppedByKustomize(built) {
			continue
		}
		out = append(out, fluxExpanded(built)...)
	}
	return out
}

// CheckCrossDocumentCollisions reports every generated object that more than one
// authored document produces (D2). Transform runs each document with its own
// NameAllocator, so a name an in-transform rule generates is never compared across
// documents: two same-namespace documents whose rules both emit a shared element
// through EmitOrAdopt each transform cleanly. A caller that transforms several
// documents passes every document's generated objects here to catch that
// collision, and any other two documents generating the same object: two
// same-named authored components in one namespace, or one cluster-scoped object
// generated from documents in different namespaces.
//
// Objects are keyed by API group, kind, namespace and name, as generated: an
// object's namespace is read from the object, never defaulted from its document,
// so a namespaced object must carry its namespace and a cluster-scoped one none.
// They are read as Flux applies them (keyedObjects): a list envelope stands for
// its members, so a member another document also generates collides, and the
// envelope itself is no object.
// A generated object with no kind cannot be keyed and is an error, whatever it
// holds; so is a member with none, unless it is a List's member that holds items
// of its own and is read by them, one level (keyedObjects). A nil entry, or a nil
// object inside one, is skipped. An object repeated within one document is not
// reported here: CheckInDocumentCollisions compares one document's applications.
// Each colliding object is reported once, naming every document that generates
// it, in input order. Listing the same document twice is an error. An object
// kustomize's build drops (droppedByKustomize) is not compared.
//
// The refusal is a *GeneratedObjectCollisionError (errors.As), which answers to
// ErrGeneratedObjectCollision (errors.Is); its text is the one it has always had.
//
// An object a controller generates for a generated object is held as generated
// too (controllerClaims), as CheckInDocumentCollisions holds it: a claim
// collides with any generated object of its identity, its own document's
// included, and with another claim of it; those collisions follow the others,
// and an object both claimed and generated twice is reported once, among them.
func CheckCrossDocumentCollisions(docs []GeneratedDocument) error {
	type docKey struct{ namespace, kind, name string }
	seenDocs := make(map[docKey]bool, len(docs))
	// generators lists, per object, the indexes of the documents generating it,
	// each once and in input order; colliding holds the objects with more than
	// one, in the order they first collided.
	generators := map[objectIdentity][]int{}
	var colliding []objectIdentity
	var claims []claimAt
	for i, doc := range docs {
		dk := docKey{doc.Namespace, doc.Kind, doc.Name}
		if seenDocs[dk] {
			return errors.Errorf("generated-object collision check: %s is listed more than once", doc)
		}
		seenDocs[dk] = true
		for _, p := range doc.Objects {
			if p == nil || isNullValue(*p) {
				continue
			}
			for _, obj := range keyedObjects(*p) {
				gvk := obj.GetObjectKind().GroupVersionKind()
				if gvk.Kind == "" {
					return errors.Errorf("generated-object collision check: %s: object %q has no kind; set its apiVersion and kind so it can be compared",
						doc, qualifiedName(obj.GetNamespace(), obj.GetName()))
				}
				made, err := controllerClaims(obj)
				if err != nil {
					return errors.Errorf("generated-object collision check: %s: object %q: %w", doc, qualifiedName(obj.GetNamespace(), obj.GetName()), err)
				}
				for _, c := range made {
					claims = append(claims, claimAt{c, i})
				}
				id := objectIdentity{group: gvk.Group, kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()}
				gen := generators[id]
				if len(gen) > 0 && gen[len(gen)-1] == i {
					continue
				}
				generators[id] = append(gen, i)
				if len(gen) == 1 {
					colliding = append(colliding, id)
				}
			}
		}
	}
	collisions := make([]ObjectCollision, 0, len(colliding))
	claimed := claimedIDs(claims)
	for _, id := range colliding {
		if claimed[id] {
			continue
		}
		producers := make([]string, len(generators[id]))
		for k, i := range generators[id] {
			producers[k] = docs[i].String()
		}
		collisions = append(collisions, objectCollision(id, producers, documentList(docs, generators[id])))
	}
	collisions = append(collisions, claimCollisions(claims, generators, func(i int) string { return docs[i].String() })...)
	if len(collisions) == 0 {
		return nil
	}
	return &GeneratedObjectCollisionError{Scope: CollisionAcrossDocuments, Collisions: collisions}
}

// documentList names the documents at idx: "both A and B" for two, "A, B and C"
// for more.
func documentList(docs []GeneratedDocument, idx []int) string {
	names := make([]string, len(idx))
	for k, i := range idx {
		names[k] = docs[i].String()
	}
	last := len(names) - 1
	if last == 1 {
		return "both " + names[0] + " and " + names[1]
	}
	return strings.Join(names[:last], ", ") + " and " + names[last]
}
