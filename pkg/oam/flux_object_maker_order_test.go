package oam

import (
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// requiringStub is a readerStub that reports what of its reads its object
// cannot become Ready without (fluxRequiredReader).
type requiringStub struct {
	readerStub
	requiredConfigMaps, requiredSecrets []string
}

func (r *requiringStub) FluxRequiredReads() (configMaps, secrets []string) {
	return r.requiredConfigMaps, r.requiredSecrets
}

// makingTrait appends a sub-application whose object kind/name it makes, or
// refers to (produces false).
type makingTrait struct {
	kind, name string
	produces   bool
	// object makes the sub-application's object the one it names
	// (objectInputStub).
	object bool
}

func (makingTrait) CanHandle(string) bool { return true }
func (m makingTrait) Apply(_ *Trait, app *stack.Application, b *stack.Bundle) error {
	var cfg stack.ApplicationConfig = &producingInputStub{inputStub: inputStub{kind: m.kind, name: m.name}, produces: m.produces}
	if m.object {
		cfg = &objectInputStub{producingInputStub: producingInputStub{inputStub: inputStub{kind: m.kind, name: m.name}, produces: true}}
	}
	b.Applications = append(b.Applications, stack.NewApplication(app.Name+"-in", app.Namespace, cfg))
	return nil
}

// objectInputStub is a producingInputStub whose object is the ConfigMap or
// Secret it names (fluxNamespaceInputObject), as the secret trait's is.
type objectInputStub struct {
	producingInputStub
}

func (*objectInputStub) IsFluxNamespaceInputObject() bool { return true }

// objectStubHandler is a siblingStubHandler whose type declares the one object
// its components generate (ComponentObjectProvider).
type objectStubHandler struct {
	siblingStubHandler
	kind  schema.GroupKind
	scope ObjectScope
}

func (h *objectStubHandler) ComponentObject() (schema.GroupKind, ObjectScope) { return h.kind, h.scope }

// madeStub is a config whose object has a controller make one more object
// (componentObjectMaker): Secret name, or, with name empty, the Secret named
// after the object; none when ok is false.
type madeStub struct {
	siblingStub
	name string
	ok   bool
}

func (m *madeStub) MadeObject(objectName string) (kind, name string, ok bool) {
	if m.name == "" {
		return "Secret", objectName, m.ok
	}
	return "Secret", m.name, m.ok
}

// sourcingStub is a readerStub whose object cannot become Ready without the
// GitRepository "x" in namespace (fluxRequiredSourceReader), named by its
// sourceRef, and says nothing of what else it requires.
type sourcingStub struct {
	readerStub
	namespace string
}

func (s *sourcingStub) FluxRequiredSource() (field, kind, name, namespace string) {
	return "sourceRef", "GitRepository", "x", s.namespace
}

// requiringSourcingStub is a requiringStub, requiring nothing, that names the
// source sourcingStub does.
type requiringSourcingStub struct {
	requiringStub
	namespace string
}

func (s *requiringSourcingStub) FluxRequiredSource() (field, kind, name, namespace string) {
	return "sourceRef", "GitRepository", "x", s.namespace
}

// fluxObjectInput is how fluxObjectTransform transforms: under fluxNS ("" for
// none, "ns" for the application namespace), with groups waiting for their
// Flux objects when wait is set, with deps as the dependency policy.
type fluxObjectInput struct {
	fluxNS string
	wait   bool
	deps   map[string][]string
}

// fluxObjectTransform transforms comps in namespace "ns" as in says. "rq" is a
// Flux object reading and requiring ConfigMap "c" and Secret "x"; "rq-none" one
// reading both and requiring neither, as a suspended one; "rd" one reading both
// and not saying what it requires; "fx" a Flux object reading Secret "x", "fc"
// one reading ConfigMap "c"; "w" a workload. "group-rq" lowers into a
// same-name sibling group of an "rq" and a "w", "group-sec" into one of a
// "sec" and a "w". The "make" trait makes Secret
// "x", "make-cm" ConfigMap "c", "make-secret-c" Secret "c", "refer" refers to
// Secret "x" without making it, "make-object" makes Secret "x" as its own
// object (fluxNamespaceInputObject); "decorate" decorates its application.
// Component makers: "sec" a Namespaced Secret kind, "cm" a Namespaced ConfigMap
// kind, "cluster-sec" a cluster-scoped Secret kind, "derived" a Namespaced kind
// whose controller makes Secret "x", "derived-own" one making the Secret named
// after its object, "derived-none" one making none. Sources: "src" a Flux-scope
// GitRepository kind, "src-oci" an OCIRepository one; "src-rq" a Flux object
// requiring the GitRepository "x" (sourcingStub) and nothing else, "src-rq-ns"
// the same in namespace "ns", "src-rq-flux" in namespace "flux", "src-only"
// one naming the source and not saying what else it requires; "group-src-rq"
// lowers into a same-name sibling group of a "src-rq" and a "w".
func fluxObjectTransform(t *testing.T, in fluxObjectInput, comps ...Component) (*stack.Cluster, error) {
	t.Helper()
	both := readerStub{configMaps: []string{"c"}, secrets: []string{"x"}}
	kindHandler := func(typ, kind string, scope ObjectScope, build func() stack.ApplicationConfig) *objectStubHandler {
		if build == nil {
			build = func() stack.ApplicationConfig { return &workloadStub{} }
		}
		return &objectStubHandler{siblingStubHandler: siblingStubHandler{typ: typ, build: build}, kind: schema.GroupKind{Kind: kind}, scope: scope}
	}
	made := func(name string, ok bool) func() stack.ApplicationConfig {
		return func() stack.ApplicationConfig { return &madeStub{name: name, ok: ok} }
	}
	sourceHandler := func(typ, kind string) *objectStubHandler {
		h := kindHandler(typ, kind, ObjectScopeFlux, nil)
		h.kind.Group = fluxSourceGroup
		return h
	}
	sourcing := func(typ, namespace string) *siblingStubHandler {
		return &siblingStubHandler{typ: typ, build: func() stack.ApplicationConfig {
			return &requiringSourcingStub{requiringStub: requiringStub{readerStub: both}, namespace: namespace}
		}}
	}
	tr := NewTransformer(map[string]ComponentHandler{
		"sec":          kindHandler("sec", "Secret", ObjectScopeNamespaced, nil),
		"cm":           kindHandler("cm", "ConfigMap", ObjectScopeNamespaced, nil),
		"cluster-sec":  kindHandler("cluster-sec", "Secret", ObjectScopeCluster, nil),
		"derived":      kindHandler("derived", "Made", ObjectScopeNamespaced, made("x", true)),
		"derived-own":  kindHandler("derived-own", "Made", ObjectScopeNamespaced, made("", true)),
		"derived-none": kindHandler("derived-none", "Made", ObjectScopeNamespaced, made("x", false)),
		"rq": &siblingStubHandler{typ: "rq", build: func() stack.ApplicationConfig {
			return &requiringStub{readerStub: both, requiredConfigMaps: []string{"c"}, requiredSecrets: []string{"x"}}
		}},
		"rq-none":     &siblingStubHandler{typ: "rq-none", build: func() stack.ApplicationConfig { return &requiringStub{readerStub: both} }},
		"rd":          &siblingStubHandler{typ: "rd", build: func() stack.ApplicationConfig { r := both; return &r }},
		"fx":          &siblingStubHandler{typ: "fx", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }},
		"fc":          &siblingStubHandler{typ: "fc", build: func() stack.ApplicationConfig { return &readerStub{configMaps: []string{"c"}} }},
		"w":           &siblingStubHandler{typ: "w", build: func() stack.ApplicationConfig { return &workloadStub{} }},
		"src":         sourceHandler("src", "GitRepository"),
		"src-oci":     sourceHandler("src-oci", "OCIRepository"),
		"src-rq":      sourcing("src-rq", ""),
		"src-rq-ns":   sourcing("src-rq-ns", "ns"),
		"src-rq-flux": sourcing("src-rq-flux", "flux"),
		"src-only":    &siblingStubHandler{typ: "src-only", build: func() stack.ApplicationConfig { return &sourcingStub{readerStub: both} }},
	}, map[string]TraitHandler{
		"make":          makingTrait{kind: "Secret", name: "x", produces: true},
		"make-cm":       makingTrait{kind: "ConfigMap", name: "c", produces: true},
		"make-secret-c": makingTrait{kind: "Secret", name: "c", produces: true},
		"refer":         makingTrait{kind: "Secret", name: "x"},
		"make-object":   makingTrait{kind: "Secret", name: "x", object: true},
		"decorate":      decoratingTrait{},
	})
	tr.RegisterComponentLowering(emitRule{typ: "group-rq", fn: pair("rq", "w")})
	tr.RegisterComponentLowering(emitRule{typ: "group-sec", fn: pair("sec", "w")})
	tr.RegisterComponentLowering(emitRule{typ: "group-src-rq", fn: pair("src-rq", "w")})
	tr.RegisterPolicy("dependency", &depsPolicyHandler{deps: in.deps})
	doc := siblingDoc(comps...)
	doc.Metadata.Namespace = "ns"
	doc.Spec.Policies = []ApplicationPolicy{{Name: "order", Type: "dependency"}}
	cluster, _, err := tr.TransformWithPolicy(doc, TransformContext{FluxNamespace: in.fluxNS, GroupsWaitForFluxObjects: in.wait})
	return cluster, err
}

// TestFluxObject_LateMaker: with groups waiting for their Flux objects
// (GroupsWaitForFluxObjects), a ConfigMap or Secret a Flux object of a group
// requires (fluxRequiredReader) that a trait makes in a later group, in the
// namespace the object lands in, is refused: the object's group waits for it,
// it for the object, and the object's group for the earlier one
// (go-kure/launcher#1079). The maker may be any component, a workload too: its
// trait object lands in the application namespace, the Flux object's only
// without a Flux namespace. The object's own config is asked, before a trait
// decorates it. Everything the transform cannot prove cyclic is admitted: the
// default delivery, the maker in the same or an earlier group, a trait that
// refers to the object, an object the Flux object does not require (a
// suspended one) or of another kind, a Flux object that does not say.
func TestFluxObject_LateMaker(t *testing.T) {
	trait := func(types ...string) []Trait {
		var out []Trait
		for _, typ := range types {
			out = append(out, Trait{Type: typ, Properties: map[string]any{}})
		}
		return out
	}
	later := map[string][]string{"maker": {"early"}}
	type want int
	const (
		admitted want = iota
		refused
		refusedWithoutFluxNamespace
	)
	for _, tc := range []struct {
		name               string
		wait               bool
		deps               map[string][]string
		earlyTyp, makerTyp string
		earlyTraits        []string
		makerTrait         string
		kind               string
		want               want
	}{
		{"refused: a later maker of a required Secret", true, later, "rq", "fx", nil, "make", "Secret", refused},
		{"refused: a later maker of a required ConfigMap", true, later, "rq", "fc", nil, "make-cm", "ConfigMap", refused},
		{"refused: a decorated Flux object", true, later, "rq", "fx", []string{"decorate"}, "make", "Secret", refused},
		{"refused: a sibling group's Flux member", true, later, "group-rq", "fx", nil, "make", "Secret", refused},
		{"a later workload maker: refused only in the application namespace", true, later, "rq", "w", nil, "make", "Secret", refusedWithoutFluxNamespace},
		{"admitted: the default delivery", false, later, "rq", "fx", nil, "make", "", admitted},
		{"admitted: the maker in an earlier group", true, map[string][]string{"early": {"maker"}}, "rq", "fx", nil, "make", "", admitted},
		{"admitted: the maker in the same group", true, nil, "rq", "fx", nil, "make", "", admitted},
		{"admitted: a trait referring to the Secret", true, later, "rq", "fx", nil, "refer", "", admitted},
		{"admitted: a Secret named as the required ConfigMap", true, later, "rq", "fx", nil, "make-secret-c", "", admitted},
		{"admitted: a Flux object requiring none", true, later, "rq-none", "fx", nil, "make", "", admitted},
		{"admitted: a Flux object not saying", true, later, "rd", "fx", nil, "make", "", admitted},
	} {
		for _, ns := range []string{"flux", ""} {
			t.Run(tc.name+"/fluxNamespace="+ns, func(t *testing.T) {
				early := Component{Name: "early", Type: tc.earlyTyp, Traits: trait(tc.earlyTraits...)}
				maker := Component{Name: "maker", Type: tc.makerTyp, Traits: trait(tc.makerTrait)}
				in := fluxObjectInput{fluxNS: ns, wait: tc.wait, deps: tc.deps}
				_, err := fluxObjectTransform(t, in, maker, early)
				if tc.want == admitted || (tc.want == refusedWithoutFluxNamespace && ns != "") {
					if err != nil {
						t.Fatalf("TransformWithPolicy: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("TransformWithPolicy admitted it, want the late maker refused")
				}
				name := `"x"`
				if tc.kind == "ConfigMap" {
					name = `"c"`
				}
				for _, want := range []string{`rq "early" requires ` + tc.kind + " " + name, `component "maker"`, "never converges", "provide the " + tc.kind + " apart from the document"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %v, want it to name %s", err, want)
					}
				}
				if inFlux := strings.Contains(err.Error(), `in the Flux namespace "flux"`); inFlux != (ns != "") {
					t.Errorf("error = %v, want the Flux namespace named only under one", err)
				}
			})
		}
	}
}

// TestFluxObject_LateMakerMessage holds the refusal's whole message, for a
// Flux object of its own and for a sibling group's Flux member, which the
// group names.
func TestFluxObject_LateMakerMessage(t *testing.T) {
	maker := Component{Name: "maker", Type: "fc", Traits: []Trait{{Type: "make-cm", Properties: map[string]any{}}}}
	in := fluxObjectInput{fluxNS: "flux", wait: true, deps: map[string][]string{"maker": {"early"}}}
	for _, tc := range []struct {
		typ  string
		want string
	}{
		{"rq", `rq "early" requires ConfigMap "c", which a trait of component "maker" makes in group "01", later than "early"'s group "00"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the ConfigMap, and the group of "maker", which makes it, for "early"'s, so the delivery never converges: put the trait on a component in group "00" or earlier, or provide the ConfigMap apart from the document in the Flux namespace "flux"`},
		{"group-rq", `rq "early" requires ConfigMap "c", which a trait of component "maker" makes in group "01", later than "early"'s group "00"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the ConfigMap, and the group of "maker", which makes it, for "early"'s, so the delivery never converges: put the trait on a component in group "00" or earlier, or provide the ConfigMap apart from the document in the Flux namespace "flux"`},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			_, err := fluxObjectTransform(t, in, maker, Component{Name: "early", Type: tc.typ})
			if err == nil || err.Error() != tc.want {
				t.Errorf("error = %v\nwant     %s", err, tc.want)
			}
		})
	}
}

// TestFluxObject_LateComponentMaker: with groups waiting for their Flux
// objects, a ConfigMap or Secret a Flux object of a group requires that a
// component of a later group makes, as its own object or through its object's
// controller (componentObjectMaker), in the namespace the Flux object lands in,
// is refused, as a trait's is (go-kure/launcher#1086). A Namespaced
// component's object lands in the application namespace "ns": it is refused
// without a Flux namespace and under one equal to "ns", and admitted under
// "flux", where the Flux object reads another Secret. A derived maker is
// admitted when the document holds the object itself (a component's own
// object, or a trait's that is its own: fluxNamespaceInputObject) in the
// waiter's group or an earlier one; an earlier derived maker does not count.
// Everything the transform cannot prove cyclic is admitted.
func TestFluxObject_LateComponentMaker(t *testing.T) {
	type want int
	const (
		admitted want = iota
		refusedOutsideFlux
	)
	comp := func(name, typ string, traits ...string) Component {
		c := Component{Name: name, Type: typ, Properties: map[string]any{}}
		for _, tr := range traits {
			c.Traits = append(c.Traits, Trait{Type: tr, Properties: map[string]any{}})
		}
		return c
	}
	named := func(c Component, objectName string) Component {
		c.Properties[ObjectNameProperty] = objectName
		return c
	}
	after := func(names ...string) map[string][]string {
		deps := map[string][]string{}
		for _, n := range names {
			deps[n] = []string{"early"}
		}
		return deps
	}
	for _, tc := range []struct {
		name  string
		wait  bool
		deps  map[string][]string
		comps []Component
		want  want
		// text is the part of the refusal that names the maker.
		text string
	}{
		{"refused: a later Secret component", true, after("x"), []Component{comp("early", "rq"), comp("x", "sec")}, refusedOutsideFlux, `requires Secret "x", which component "x" (sec) makes in group "01"`},
		{"refused: a later ConfigMap component", true, after("c"), []Component{comp("early", "rq"), comp("c", "cm")}, refusedOutsideFlux, `requires ConfigMap "c", which component "c" (cm) makes in group "01"`},
		{"refused: a later derived maker", true, after("maker"), []Component{comp("early", "rq"), comp("maker", "derived")}, refusedOutsideFlux, `requires Secret "x", which component "maker" (derived), whose Made makes it, makes in group "01"`},
		{"refused: a later derived maker of the Secret named after its object", true, after("x"), []Component{comp("early", "rq"), comp("x", "derived-own")}, refusedOutsideFlux, `which component "x" (derived-own), whose Made makes it, makes`},
		{"refused: the object named by objectName", true, after("maker"), []Component{comp("early", "rq"), named(comp("maker", "sec"), "x")}, refusedOutsideFlux, `which component "maker" (sec) makes`},
		{"refused: a sibling group's member as maker", true, after("x"), []Component{comp("early", "rq"), comp("x", "group-sec")}, refusedOutsideFlux, `which component "x" (sec) makes`},
		{"refused: a sibling group's Flux member as waiter", true, after("x"), []Component{comp("early", "group-rq"), comp("x", "sec")}, refusedOutsideFlux, `rq "early" requires Secret "x", which component "x" (sec) makes`},
		{"refused: a decorated maker", true, after("x"), []Component{comp("early", "rq"), comp("x", "sec", "decorate")}, refusedOutsideFlux, `which component "x" (sec) makes`},
		{"refused: a decorated derived maker", true, after("maker"), []Component{comp("early", "rq"), comp("maker", "derived", "decorate")}, refusedOutsideFlux, `which component "maker" (derived), whose Made makes it, makes`},
		{"refused: a decorated waiter", true, after("x"), []Component{comp("early", "rq", "decorate"), comp("x", "sec")}, refusedOutsideFlux, `which component "x" (sec) makes`},
		{"refused: the trait maker wins over the component", true, after("x"), []Component{comp("early", "rq"), comp("x", "sec", "make")}, refusedOutsideFlux, `which a trait of component "x" makes`},
		{"admitted: the default delivery", false, after("x"), []Component{comp("early", "rq"), comp("x", "sec")}, admitted, ""},
		{"admitted: the maker in an earlier group", true, map[string][]string{"early": {"x"}}, []Component{comp("early", "rq"), comp("x", "sec")}, admitted, ""},
		{"admitted: the maker in the same group", true, nil, []Component{comp("early", "rq"), comp("x", "sec")}, admitted, ""},
		{"admitted: a derived maker that makes none", true, after("maker"), []Component{comp("early", "rq"), comp("maker", "derived-none")}, admitted, ""},
		{"admitted: a cluster-scoped kind", true, after("x"), []Component{comp("early", "rq"), comp("x", "cluster-sec")}, admitted, ""},
		{"admitted: another name", true, after("y"), []Component{comp("early", "rq"), comp("y", "sec")}, admitted, ""},
		{"admitted: the component name when objectName differs", true, after("x"), []Component{comp("early", "rq"), named(comp("x", "sec"), "y")}, admitted, ""},
		{"admitted: a Secret named as the required ConfigMap", true, after("c"), []Component{comp("early", "rq"), comp("c", "sec")}, admitted, ""},
		{"admitted: a Flux object requiring none", true, after("x"), []Component{comp("early", "rq-none"), comp("x", "sec")}, admitted, ""},
		{"admitted: a Flux object not saying", true, after("x"), []Component{comp("early", "rd"), comp("x", "sec")}, admitted, ""},
		// Concrete availability: derived makers only.
		{"admitted: a derived maker after a Secret component of the waiter's group", true, after("maker"), []Component{comp("early", "rq"), comp("x", "sec"), comp("maker", "derived")}, admitted, ""},
		{"admitted: a derived maker after a Secret component of an earlier group", true, map[string][]string{"early": {"x"}, "maker": {"early"}}, []Component{comp("early", "rq"), comp("x", "sec"), comp("maker", "derived")}, admitted, ""},
		{"admitted: a derived maker after a trait's own Secret", true, after("maker"), []Component{comp("early", "rq", "make-object"), comp("maker", "derived")}, admitted, ""},
		{"refused: a derived maker after a trait's derived Secret (R3-F1)", true, after("maker"), []Component{comp("early", "rq", "make"), comp("maker", "derived")}, refusedOutsideFlux, `which component "maker" (derived), whose Made makes it, makes`},
		{"refused: a derived maker before a later Secret component", true, after("maker", "x"), []Component{comp("early", "rq"), comp("maker", "derived"), comp("x", "sec")}, refusedOutsideFlux, `which component "maker" (derived), whose Made makes it, makes`},
		{"refused: a Secret component after a derived maker (R2-F1)", true, after("x"), []Component{comp("early", "rq"), comp("e", "derived"), comp("x", "sec")}, refusedOutsideFlux, `which component "x" (sec) makes`},
		{"refused: a Secret trait after a derived maker (R2-F1)", true, after("m"), []Component{comp("early", "rq"), comp("e", "derived"), comp("m", "w", "make-object")}, refusedOutsideFlux, `which a trait of component "m" makes`},
	} {
		for _, ns := range []string{"", "flux", "ns"} {
			t.Run(tc.name+"/fluxNamespace="+ns, func(t *testing.T) {
				comps := make([]Component, len(tc.comps))
				for i, c := range tc.comps {
					c.Properties = maps.Clone(c.Properties)
					comps[i] = c
				}
				_, err := fluxObjectTransform(t, fluxObjectInput{fluxNS: ns, wait: tc.wait, deps: tc.deps}, comps...)
				if tc.want == admitted || ns == "flux" {
					if err != nil {
						t.Fatalf("TransformWithPolicy: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("TransformWithPolicy admitted it, want the late maker refused")
				}
				for _, want := range []string{tc.text, "never converges"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %v, want it to name %s", err, want)
					}
				}
				if inFlux := strings.Contains(err.Error(), "in the Flux namespace"); inFlux != (ns != "") {
					t.Errorf("error = %v, want the Flux namespace named only under one", err)
				}
			})
		}
	}
}

// TestFluxObject_LateComponentMakerMessage holds a component maker's refusal
// whole, for its own object and for one its object's controller makes.
func TestFluxObject_LateComponentMakerMessage(t *testing.T) {
	for _, tc := range []struct {
		name, typ, ns, want string
	}{
		{"own object", "cm", "", `rq "early" requires ConfigMap "c", which component "c" (cm) makes in group "01", later than "early"'s group "00"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the ConfigMap, and the group of "c", which makes it, for "early"'s, so the delivery never converges: place component "c" in group "00" or earlier, or provide the ConfigMap apart from the document`},
		{"derived object", "derived", "ns", `rq "early" requires Secret "x", which component "c" (derived), whose Made makes it, makes in group "01", later than "early"'s group "00"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the Secret, and the group of "c", which makes it, for "early"'s, so the delivery never converges: place component "c" in group "00" or earlier, or provide the Secret apart from the document in the Flux namespace "ns"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fluxObjectInput{fluxNS: tc.ns, wait: true, deps: map[string][]string{"c": {"early"}}}
			_, err := fluxObjectTransform(t, in, Component{Name: "c", Type: tc.typ}, Component{Name: "early", Type: "rq"})
			if err == nil || err.Error() != tc.want {
				t.Errorf("error = %v\nwant     %s", err, tc.want)
			}
		})
	}
}
