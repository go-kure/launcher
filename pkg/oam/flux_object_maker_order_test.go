package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
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
}

func (makingTrait) CanHandle(string) bool { return true }
func (m makingTrait) Apply(_ *Trait, app *stack.Application, b *stack.Bundle) error {
	cfg := &producingInputStub{inputStub: inputStub{kind: m.kind, name: m.name}, produces: m.produces}
	b.Applications = append(b.Applications, stack.NewApplication(app.Name+"-in", app.Namespace, cfg))
	return nil
}

// fluxObjectInput is how fluxObjectTransform transforms: under fluxNS ("" for
// none), with groups waiting for their Flux objects when wait is set, with deps
// as the dependency policy.
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
// same-name sibling group of an "rq" and a "w". The "make" trait makes Secret
// "x", "make-cm" ConfigMap "c", "make-secret-c" Secret "c", "refer" refers to
// Secret "x" without making it; "decorate" decorates its application.
func fluxObjectTransform(t *testing.T, in fluxObjectInput, comps ...Component) (*stack.Cluster, error) {
	t.Helper()
	both := readerStub{configMaps: []string{"c"}, secrets: []string{"x"}}
	tr := NewTransformer(map[string]ComponentHandler{
		"rq": &siblingStubHandler{typ: "rq", build: func() stack.ApplicationConfig {
			return &requiringStub{readerStub: both, requiredConfigMaps: []string{"c"}, requiredSecrets: []string{"x"}}
		}},
		"rq-none": &siblingStubHandler{typ: "rq-none", build: func() stack.ApplicationConfig { return &requiringStub{readerStub: both} }},
		"rd":      &siblingStubHandler{typ: "rd", build: func() stack.ApplicationConfig { r := both; return &r }},
		"fx":      &siblingStubHandler{typ: "fx", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }},
		"fc":      &siblingStubHandler{typ: "fc", build: func() stack.ApplicationConfig { return &readerStub{configMaps: []string{"c"}} }},
		"w":       &siblingStubHandler{typ: "w", build: func() stack.ApplicationConfig { return &workloadStub{} }},
	}, map[string]TraitHandler{
		"make":          makingTrait{kind: "Secret", name: "x", produces: true},
		"make-cm":       makingTrait{kind: "ConfigMap", name: "c", produces: true},
		"make-secret-c": makingTrait{kind: "Secret", name: "c", produces: true},
		"refer":         makingTrait{kind: "Secret", name: "x"},
		"decorate":      decoratingTrait{},
	})
	tr.RegisterComponentLowering(emitRule{typ: "group-rq", fn: pair("rq", "w")})
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
