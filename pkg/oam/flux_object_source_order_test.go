package oam

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestFluxObject_LateSourceComponent: with groups waiting for their Flux
// objects, a source a Flux object of a group cannot become Ready without
// (fluxRequiredSourceReader) that a component of a later group makes as its
// own object, in the namespace the reference resolves in, is refused: the
// object's group waits for it, it for the source, and the source's group for
// the object's (go-kure/launcher#1088). The reference resolves in the
// namespace it writes, else where the object lands: "ns" without a Flux
// namespace and under "ns", "flux" under "flux", where every Flux-scope source
// component lands too. It is checked whatever the object reports of the
// ConfigMaps and Secrets it requires, nothing or not saying. Everything the
// transform cannot prove cyclic is admitted.
func TestFluxObject_LateSourceComponent(t *testing.T) {
	all := []string{"", "flux", "ns"}
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
	after := func(name string) map[string][]string { return map[string][]string{name: {"early"}} }
	for _, tc := range []struct {
		name  string
		wait  bool
		deps  map[string][]string
		comps []Component
		// refused lists the Flux namespaces the transform refuses it under;
		// it admits it under the others.
		refused []string
		// text is the part of the refusal that names the waiter and the source.
		text string
	}{
		{"refused: a later source of an object requiring nothing else", true, after("x"), []Component{comp("early", "src-rq"), comp("x", "src")}, all, `src-rq "early" requires GitRepository "x" (sourceRef), the object of component "x" in group "01"`},
		{"refused: a later source of an object not saying what else it requires", true, after("x"), []Component{comp("early", "src-only"), comp("x", "src")}, all, `src-only "early" requires GitRepository "x" (sourceRef)`},
		{"refused: an explicit namespace where the source lands (ns)", true, after("x"), []Component{comp("early", "src-rq-ns"), comp("x", "src")}, []string{"", "ns"}, `requires GitRepository "x" (sourceRef), the object of component "x"`},
		{"refused: an explicit namespace where the source lands (flux)", true, after("x"), []Component{comp("early", "src-rq-flux"), comp("x", "src")}, []string{"flux"}, `requires GitRepository "x" (sourceRef), the object of component "x"`},
		{"refused: the source named by objectName", true, after("s"), []Component{comp("early", "src-rq"), named(comp("s", "src"), "x")}, all, `requires GitRepository "x" (sourceRef), the object of component "s"`},
		{"refused: a sibling group's Flux member as waiter", true, after("x"), []Component{comp("early", "group-src-rq"), comp("x", "src")}, all, `src-rq "early" requires GitRepository "x"`},
		{"refused: a decorated waiter", true, after("x"), []Component{comp("early", "src-rq", "decorate"), comp("x", "src")}, all, `src-rq "early" requires GitRepository "x"`},
		{"admitted: the default delivery", false, after("x"), []Component{comp("early", "src-rq"), comp("x", "src")}, nil, ""},
		{"admitted: the source in the same group", true, nil, []Component{comp("early", "src-rq"), comp("x", "src")}, nil, ""},
		{"admitted: the source in an earlier group", true, map[string][]string{"early": {"x"}}, []Component{comp("early", "src-rq"), comp("x", "src")}, nil, ""},
		{"admitted: the component name when objectName differs", true, after("x"), []Component{comp("early", "src-rq"), named(comp("x", "src"), "y")}, nil, ""},
		{"admitted: another name", true, after("y"), []Component{comp("early", "src-rq"), comp("y", "src")}, nil, ""},
		{"admitted: another kind of source", true, after("x"), []Component{comp("early", "src-rq"), comp("x", "src-oci")}, nil, ""},
		{"admitted: a Secret component of the same name", true, after("x"), []Component{comp("early", "src-rq"), comp("x", "sec")}, nil, ""},
		{"admitted: a derived Secret of the same name", true, after("x"), []Component{comp("early", "src-rq"), comp("x", "derived-own")}, nil, ""},
	} {
		for _, ns := range all {
			t.Run(tc.name+"/fluxNamespace="+ns, func(t *testing.T) {
				comps := make([]Component, len(tc.comps))
				for i, c := range tc.comps {
					c.Properties = maps.Clone(c.Properties)
					comps[i] = c
				}
				_, err := fluxObjectTransform(t, fluxObjectInput{fluxNS: ns, wait: tc.wait, deps: tc.deps}, comps...)
				if !slices.Contains(tc.refused, ns) {
					if err != nil {
						t.Fatalf("TransformWithPolicy: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("TransformWithPolicy admitted it, want the late source refused")
				}
				for _, want := range []string{tc.text, "never converges"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %v, want it to name %s", err, want)
					}
				}
			})
		}
	}
}

// TestFluxObject_LateSourceComponentMessage holds the refusal of a late source
// whole.
func TestFluxObject_LateSourceComponentMessage(t *testing.T) {
	const want = `src-rq "early" requires GitRepository "x" (sourceRef), the object of component "x" in group "01", later than "early"'s group "00"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the GitRepository, and the group of "x" for "early"'s, so the delivery never converges: place component "x" in group "00" or earlier`
	in := fluxObjectInput{fluxNS: "flux", wait: true, deps: map[string][]string{"x": {"early"}}}
	_, err := fluxObjectTransform(t, in, Component{Name: "x", Type: "src"}, Component{Name: "early", Type: "src-rq"})
	if err == nil || err.Error() != want {
		t.Errorf("error = %v\nwant     %s", err, want)
	}
}
