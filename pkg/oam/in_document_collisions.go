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
// component it belongs to, every object it generated, whether its bundle
// force-applies them, and the patches its bundle's Kustomization applies to them.
type GeneratedApplication struct {
	Name      string // the stack.Application's name
	Component string // the OAM component it belongs to (ComponentNamed), else Name
	Objects   []*client.Object
	// Forced reports that the application's leaf bundle sets Force, so its Flux
	// Kustomization (spec.force) force-applies every object, annotated or not.
	Forced bool
	// Patches are the leaf bundle's patches (stack.Bundle.Patches, which the
	// fluxcd-patches trait sets): its Flux Kustomization's spec.patches, applied
	// to every object of the bundle before Flux applies them.
	Patches []stack.Patch

	// bundle is the leaf bundle GenerateApplications generated it from, so the
	// applications of one bundle are patched together; nil for an application a
	// caller built, which is patched on its own.
	bundle *stack.Bundle
}

// String names the application as a collision error names its producer: a
// component's own application (or a sibling group, which deploys as one) by its
// component, any other — a trait's sub-application — by its name and component.
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
// each node's bundle, then the node's children; an umbrella bundle's children in
// order, and a leaf bundle's applications in order. A leaf bundle's labels and
// annotations are added to its applications' objects where missing, exactly as
// stack.Bundle.Generate adds them, so the objects are the ones Bundle.Generate
// would return. An application's Generate error is returned unchanged.
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
	if bundle.IsUmbrella() {
		for _, child := range bundle.Children {
			if err := generateBundle(child, out); err != nil {
				return err
			}
		}
		return nil
	}
	start := len(*out)
	forced := bundle.Force != nil && *bundle.Force
	for _, app := range bundle.Applications {
		objs, err := app.Generate()
		if err != nil {
			return err
		}
		component := app.Name
		if named, ok := app.Config.(ComponentNamed); ok && named.ComponentName() != "" {
			component = named.ComponentName()
		}
		// Copied at once, as Bundle.Generate appends each result at once: a config
		// that reuses its result slice must not change an earlier application's.
		objs = append([]*client.Object(nil), objs...)
		*out = append(*out, GeneratedApplication{Name: app.Name, Component: component, Objects: objs, Forced: forced,
			Patches: bundle.Patches, bundle: bundle})
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
