package oam

import (
	"fmt"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// GeneratedApplication is one application's generated output, as a caller that
// generates a transformed document holds it: the stack.Application's name, the OAM
// component it belongs to, every object it generated, and whether they are
// force-applied.
type GeneratedApplication struct {
	Name string // the stack.Application's name
	// Component is the authored OAM component the application belongs to
	// (go-kure/launcher#788): the component itself, one a lowering rule emitted
	// for it under any name, a sub-application one of its traits added, a
	// NetworkPolicy synthesized for it. It is empty for an application the
	// document as a whole owns: a generated source the application bundle holds,
	// and an external backend Service's synthesized NetworkPolicy. For an
	// application the transform did not build (one a caller added to the
	// cluster) it is the config's ComponentNamed answer, else Name.
	Component string
	Objects   []*client.Object
	// Forced reports that every object of the application is force-applied,
	// annotated or not, for either of two reasons. The application carries the
	// ForceReplace delivery intent (stack.Application.Delivery), which the
	// force-replace trait sets (go-kure/launcher#782). Or its own bundle sets
	// Force, so its Flux Kustomization (spec.force) forces them: launcher never
	// sets a bundle's Force (go-kure/launcher#781), so that is a bundle whose
	// Force the caller set before generating.
	Forced bool
	// forceReplace and bundleForce keep Forced's two reasons apart for
	// WarnForcedVolumes; GenerateApplications sets them. Forced decides and they
	// only say why: they are not read when Forced is false, and a value a caller
	// built with Forced alone names neither and is read as its bundle's force.
	forceReplace, bundleForce bool
}

// String names the application as a collision error names its producer: a
// component's own application (or a sibling group, which deploys as one) and
// an application the document as a whole owns by its name, any other — a
// trait's sub-application, a lowered component named differently, a synthesized
// NetworkPolicy — by its name and component.
func (a GeneratedApplication) String() string {
	if a.componentOrName() == a.Name {
		return fmt.Sprintf("component %q", a.Name)
	}
	return fmt.Sprintf("sub-application %q of component %q", a.Name, a.Component)
}

func (a GeneratedApplication) componentOrName() string {
	if a.Component == "" {
		return a.Name
	}
	return a.Component
}

// GenerateApplications generates every application of a transformed document
// once and returns each one's objects with its producer, in generation order:
// each node's bundle, then the node's children; a bundle's own applications in
// order, then its children in order. That is the order a bundle is applied in:
// an ordered application's bundle holds its generated sources itself, ahead of
// the groups that consume them (go-kure/launcher#783). A bundle's labels and
// annotations are added to its own applications' objects where missing, exactly
// as stack.Bundle.Generate adds them, so the objects are the ones
// Bundle.Generate would return. An application's Generate error is returned
// unchanged.
//
// Generating a document once and checking that inventory is the point: a
// component may hand out objects it cached and a trait may change them in place,
// so a second generation of the same cluster can differ from the first.
func GenerateApplications(cluster *stack.Cluster) ([]GeneratedApplication, error) {
	if cluster == nil {
		return nil, nil
	}
	var out []GeneratedApplication
	if err := generateNode(cluster.Node, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func generateNode(node *stack.Node, out *[]GeneratedApplication) error {
	if node == nil {
		return nil
	}
	if err := generateBundle(node.Bundle, out); err != nil {
		return err
	}
	for _, child := range node.Children {
		if err := generateNode(child, out); err != nil {
			return err
		}
	}
	return nil
}

func generateBundle(bundle *stack.Bundle, out *[]GeneratedApplication) error {
	if bundle == nil {
		return nil
	}
	start := len(*out)
	bundleForce := bundle.Force != nil && *bundle.Force
	for _, app := range bundle.Applications {
		objs, err := app.Generate()
		if err != nil {
			return err
		}
		component := app.Name
		if owned, ok := app.Config.(componentOwner); ok {
			component = owned.owningComponent()
		} else if named, ok := app.Config.(ComponentNamed); ok && named.ComponentName() != "" {
			component = named.ComponentName()
		}
		// Copied at once, as Bundle.Generate appends each result at once: a config
		// that reuses its result slice must not change an earlier application's.
		objs = append([]*client.Object(nil), objs...)
		forceReplace := app.Delivery.ForceReplace
		*out = append(*out, GeneratedApplication{
			Name: app.Name, Component: component, Objects: objs,
			Forced: bundleForce || forceReplace, forceReplace: forceReplace, bundleForce: bundleForce,
		})
	}
	// Merged after every application of the bundle has generated, as
	// Bundle.Generate merges them: an application may change an object another
	// one generated earlier.
	for _, generated := range (*out)[start:] {
		for _, p := range generated.Objects {
			if p == nil || isNullValue(*p) {
				continue
			}
			if len(bundle.Labels) > 0 {
				(*p).SetLabels(withMissing((*p).GetLabels(), bundle.Labels))
			}
			if len(bundle.Annotations) > 0 {
				(*p).SetAnnotations(withMissing((*p).GetAnnotations(), bundle.Annotations))
			}
		}
	}
	for _, child := range bundle.Children {
		if err := generateBundle(child, out); err != nil {
			return err
		}
	}
	return nil
}

// withMissing returns m with every key of bundleValues it lacks, as
// stack.Bundle.Generate merges a bundle's labels and annotations: the object's own
// value wins.
func withMissing(m, bundleValues map[string]string) map[string]string {
	if m == nil {
		m = make(map[string]string, len(bundleValues))
	}
	for k, v := range bundleValues {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return m
}

// CheckInDocumentCollisions reports every generated object that more than one
// application of one document generates (go-kure/launcher#646): a component and
// another component's trait, two components, or two traits that render one
// Kubernetes object would each deploy it, and whichever applied last would win.
// Nothing earlier compares them: the NameAllocator guards only the names it
// allocates, and CheckCrossDocumentCollisions compares documents, not the
// applications inside one. A caller passes GenerateApplications' result, or its
// own per-application generation of one document.
//
// Objects are keyed as CheckCrossDocumentCollisions keys them: API group, kind,
// namespace as generated, and name. An object with no kind cannot be keyed and is
// an error; a nil entry, or a nil object inside one, is skipped. An object one
// application generates twice is not reported here. Each colliding object is
// reported once, naming every application that generates it, in input order.
func CheckInDocumentCollisions(apps []GeneratedApplication) error {
	generators := map[objectIdentity][]int{}
	var colliding []objectIdentity
	for i, app := range apps {
		for _, p := range app.Objects {
			if p == nil || isNullValue(*p) {
				continue
			}
			obj := *p
			gvk := obj.GetObjectKind().GroupVersionKind()
			if gvk.Kind == "" {
				return errors.Errorf("generated-object collision check: %s: object %q has no kind; set its apiVersion and kind so it can be compared",
					app, qualifiedName(obj.GetNamespace(), obj.GetName()))
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
	collisions := make([]string, 0, len(colliding))
	for _, id := range colliding {
		collisions = append(collisions, fmt.Sprintf("%s is generated by %s", id, applicationList(apps, generators[id])))
	}
	switch len(collisions) {
	case 0:
		return nil
	case 1:
		return errors.Errorf("generated-object collision: %s; rename one so that each object has one producer", collisions[0])
	default:
		return errors.Errorf("generated-object collision: %d generated objects are each produced by more than one application; rename one producer of each:\n  %s",
			len(collisions), strings.Join(collisions, "\n  "))
	}
}

// applicationList names the applications at idx: "both A and B" for two, "A, B
// and C" for more. Applications that read alike are named once, where the first
// of them stands, with their count: "2 sub-applications "dup" of component
// "web"" for two traits of one component that create sub-applications of one
// name, "2 applications "web" of component "web"" for a trait named after its
// own component. Which one is which cannot be told from the cluster
// (go-kure/launcher#757).
func applicationList(apps []GeneratedApplication, idx []int) string {
	var names []string
	var first []int
	count := map[string]int{}
	for _, i := range idx {
		name := apps[i].String()
		if count[name] == 0 {
			names = append(names, name)
			first = append(first, i)
		}
		count[name]++
	}
	for k, name := range names {
		if n := count[name]; n > 1 {
			names[k] = apps[first[k]].countedString(n)
		}
	}
	last := len(names) - 1
	switch {
	case last == 0:
		return names[0]
	case len(idx) == 2:
		return "both " + names[0] + " and " + names[1]
	}
	return strings.Join(names[:last], ", ") + " and " + names[last]
}

// countedString names n applications that each read as a.String().
func (a GeneratedApplication) countedString(n int) string {
	if a.componentOrName() == a.Name {
		return fmt.Sprintf("%d applications %q of component %q", n, a.Name, a.Name)
	}
	return fmt.Sprintf("%d sub-applications %q of component %q", n, a.Name, a.Component)
}
