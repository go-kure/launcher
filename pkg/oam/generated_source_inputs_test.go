package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// sourceConsumerRule lowers a component of type typ into the one
// "ocirepository" every such component of the document shares (the first
// invocation emits it, a later one adopts it, as the oci rule shares a source)
// and the consumer itself, of type as, ordered after it and carrying the
// component's traits. With group set the consumer is a same-name sibling group
// of a Flux member ("flux") and a workload member ("w"), both ordered after the
// source, the traits on the member of type as; fluxUnordered leaves the Flux
// member unordered, so only the workload member consumes the source, and
// workloads makes the group's members two workloads ("w", "w2"). It declares a
// schema, so its output is synthesized.
type sourceConsumerRule struct {
	typ, as       string
	group         bool
	fluxUnordered bool
	workloads     bool
}

func (r sourceConsumerRule) ComponentType() string { return r.typ }
func (sourceConsumerRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}
func (r sourceConsumerRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	name, adopted, err := lctx.Namer.NameOrAdopt(lctx.Origin.Document, "source", "src", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	var result LoweringResult
	if !adopted {
		result.Components = append(result.Components, Component{Name: name, Type: "ocirepository"})
	}
	types := []string{r.as}
	switch {
	case r.workloads:
		types = []string{"w", "w2"}
	case r.group:
		types = []string{"flux", "w"}
	}
	for _, typ := range types {
		c := Component{Name: comp.Name, Type: typ}
		if typ == r.as {
			c.Traits = comp.Traits
		}
		if typ != "flux" || !r.fluxUnordered {
			c.OrderAfter(name)
		}
		result.Components = append(result.Components, c)
	}
	return result, nil
}

// requiredReaderStub is a readerStub that reports the Secrets it requires
// (fluxSourceRequiredReader).
type requiredReaderStub struct {
	readerStub
	required []string
}

func (r *requiredReaderStub) FluxSourceRequiredSecrets() []string { return r.required }

// producingInputStub is an inputStub that says whether its trait makes it
// (fluxNamespaceProducer).
type producingInputStub struct {
	inputStub
	produces bool
}

func (p *producingInputStub) ProducesFluxNamespaceInput() bool { return p.produces }

// makingSecretTrait appends a sub-application whose Secret name it makes, or
// refers to (produces false).
type makingSecretTrait struct {
	name     string
	produces bool
}

func (makingSecretTrait) CanHandle(string) bool { return true }
func (m makingSecretTrait) Apply(_ *Trait, app *stack.Application, b *stack.Bundle) error {
	cfg := &producingInputStub{inputStub: inputStub{kind: "Secret", name: m.name}, produces: m.produces}
	b.Applications = append(b.Applications, stack.NewApplication(app.Name+"-in", app.Namespace, cfg))
	return nil
}

// sourceInput is how sourceInputTransform transforms: under fluxNS ("" for
// none), with groups waiting for their Flux objects when wait is set, with deps
// as the dependency policy. The generated source requires the Secrets required
// names, and reports none (no fluxSourceRequiredReader) when it is nil.
type sourceInput struct {
	fluxNS   string
	wait     bool
	deps     map[string][]string
	required []string
}

// sourceInputTransform transforms comps in namespace "ns" as in says. The
// generated source reads Secret "x"; "flux" is a Flux object reading nothing,
// "fx" one reading "x", "w" and "w2" workloads. A "claim" trait makes Secret
// "x" (claimed where it lands) and does not say whether it makes it; "make"
// makes Secret "x", "refer" refers to one it does not make, "make-y" makes
// Secret "y"; "decorate" decorates its application. "sec" is a Namespaced
// Secret kind.
func sourceInputTransform(t *testing.T, in sourceInput, comps ...Component) (*stack.Cluster, error) {
	t.Helper()
	source := func() stack.ApplicationConfig {
		if in.required == nil {
			return &readerStub{secrets: []string{"x"}}
		}
		return &requiredReaderStub{readerStub: readerStub{secrets: []string{"x"}}, required: in.required}
	}
	tr := NewTransformer(map[string]ComponentHandler{
		"ocirepository": &siblingStubHandler{typ: "ocirepository", build: source},
		"flux":          &siblingStubHandler{typ: "flux", build: func() stack.ApplicationConfig { return &readerStub{} }},
		"fx":            &siblingStubHandler{typ: "fx", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }},
		"w":             &siblingStubHandler{typ: "w", build: func() stack.ApplicationConfig { return &workloadStub{} }},
		"w2":            &siblingStubHandler{typ: "w2", build: func() stack.ApplicationConfig { return &workloadStub{} }},
		"sec": &objectStubHandler{siblingStubHandler: siblingStubHandler{typ: "sec", build: func() stack.ApplicationConfig { return &workloadStub{} }},
			kind: schema.GroupKind{Kind: "Secret"}, scope: ObjectScopeNamespaced},
	}, map[string]TraitHandler{
		"claim":    claimingSecretTrait{},
		"make":     makingSecretTrait{name: "x", produces: true},
		"refer":    makingSecretTrait{name: "x"},
		"make-y":   makingSecretTrait{name: "y", produces: true},
		"decorate": decoratingTrait{},
	})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "flux-consumer", as: "flux"})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "workload-consumer", as: "w"})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "group-flux-consumer", as: "flux", group: true})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "group-workload-consumer", as: "w", group: true})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "group-workload-only", as: "w", group: true, fluxUnordered: true})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "group-workloads", as: "w", workloads: true})
	tr.RegisterPolicy("dependency", &depsPolicyHandler{deps: in.deps})
	doc := siblingDoc(comps...)
	doc.Metadata.Namespace = "ns"
	doc.Spec.Policies = []ApplicationPolicy{{Name: "order", Type: "dependency"}}
	cluster, _, err := tr.TransformWithPolicy(doc, TransformContext{FluxNamespace: in.fluxNS, GroupsWaitForFluxObjects: in.wait})
	return cluster, err
}

// clusterApplication returns the application of name anywhere in cluster.
func clusterApplication(cluster *stack.Cluster, name string) *stack.Application {
	var found *stack.Application
	walkBundles(cluster.Node, func(b *stack.Bundle) {
		for _, a := range b.Applications {
			if a.Name == name {
				found = a
			}
		}
	})
	return found
}

// TestGeneratedSource_TraitInputsFollowItsReads: a Secret a generated source
// reads moves to the Flux namespace with it when a trait of a Flux object the
// rule ordered after the source makes it, and is claimed there: it collides
// with a same-named Secret another component makes in the Flux namespace, not
// with one in the application namespace. A workload's stays with it, also when
// an earlier trait decorated the workload, and is claimed in the application
// namespace, as is a sibling group's workload member's (go-kure/launcher#1016).
func TestGeneratedSource_TraitInputsFollowItsReads(t *testing.T) {
	claim := Trait{Type: "claim", Properties: map[string]any{}}
	decorate := Trait{Type: "decorate", Properties: map[string]any{}}
	// Each makes Secret "x": "fluxOther" in the Flux namespace (its Flux object
	// reads it), "nsOther" in the application namespace.
	fluxOther := Component{Name: "other", Type: "fx", Properties: map[string]any{}, Traits: []Trait{claim}}
	nsOther := Component{Name: "other", Type: "w", Properties: map[string]any{}, Traits: []Trait{claim}}
	for _, tc := range []struct {
		name   string
		typ    string
		traits []Trait
		want   string
	}{
		{"a Flux consumer", "flux-consumer", []Trait{claim}, "flux"},
		{"a workload consumer", "workload-consumer", []Trait{claim}, "ns"},
		{"a decorated workload consumer", "workload-consumer", []Trait{decorate, claim}, "ns"},
		{"a sibling group's Flux member", "group-flux-consumer", []Trait{claim}, "flux"},
		{"a sibling group's workload member", "group-workload-consumer", []Trait{claim}, "ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Component{Name: "c", Type: tc.typ, Traits: tc.traits}
			cluster, err := sourceInputTransform(t, sourceInput{fluxNS: "flux"}, c)
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			in := clusterApplication(cluster, "c-in")
			if in == nil {
				t.Fatal("no c-in application in the cluster")
			}
			if in.Namespace != tc.want {
				t.Errorf("c-in namespace = %q, want %q", in.Namespace, tc.want)
			}
			// The claim is held where the Secret lands.
			collides, admitted := fluxOther, nsOther
			if tc.want == "ns" {
				collides, admitted = nsOther, fluxOther
			}
			if _, err := sourceInputTransform(t, sourceInput{fluxNS: "flux"}, c, collides); err == nil || !strings.Contains(err.Error(), `Secret "`+tc.want+`/x"`) {
				t.Errorf("with %s: error = %v, want a collision naming Secret %s/x", collides.Type, err, tc.want)
			}
			if _, err := sourceInputTransform(t, sourceInput{fluxNS: "flux"}, c, admitted); err != nil {
				t.Errorf("with %s: TransformWithPolicy: %v", admitted.Type, err)
			}
		})
	}
}

// TestGeneratedSource_LateMaker: with groups waiting for their Flux objects
// (GroupsWaitForFluxObjects), a Secret the generated source requires that a
// trait makes in a later group than a Flux object consuming the source is
// refused: that consumer's group waits for it, it for the source, the source
// for the Secret, and the Secret's group for the consumer's
// (go-kure/launcher#1039). A sibling group's Flux member consumes the source by
// its own order only. Everything the transform cannot prove cyclic is admitted:
// the default delivery, the maker in the earliest group, a workload consumer
// (which does not wait on the source), a trait that refers to the Secret or
// does not say, a Secret the source does not require, a source that does not
// say. Each row runs under a Flux namespace and without one.
func TestGeneratedSource_LateMaker(t *testing.T) {
	trait := func(typ string) []Trait { return []Trait{{Type: typ, Properties: map[string]any{}}} }
	later := map[string][]string{"maker": {"early"}}
	x := []string{"x"}
	for _, tc := range []struct {
		name               string
		wait               bool
		deps               map[string][]string
		required           []string
		earlyTyp, makerTyp string
		makerTrait         string
		refused            bool
	}{
		{"refused: a later maker", true, later, x, "flux-consumer", "flux-consumer", "make", true},
		{"admitted: the default delivery", false, later, x, "flux-consumer", "flux-consumer", "make", false},
		{"admitted: the maker in the earliest group", true, map[string][]string{"early": {"maker"}}, x, "flux-consumer", "flux-consumer", "make", false},
		{"admitted: a trait referring to the Secret", true, later, x, "flux-consumer", "flux-consumer", "refer", false},
		{"admitted: a trait not saying", true, later, x, "flux-consumer", "flux-consumer", "claim", false},
		{"admitted: a later maker of a Secret the source does not require", true, later, []string{"z"}, "flux-consumer", "flux-consumer", "make", false},
		{"admitted: a later maker of a Secret the source does not read", true, later, x, "flux-consumer", "flux-consumer", "make-y", false},
		{"admitted: a source not saying what it requires", true, later, nil, "flux-consumer", "flux-consumer", "make", false},
		{"admitted: a workload consumer", true, later, x, "workload-consumer", "flux-consumer", "make", false},
		{"refused: a sibling group's Flux member consumer", true, later, x, "group-flux-consumer", "flux-consumer", "make", true},
		{"admitted: a sibling group whose Flux member does not consume", true, later, x, "group-workload-only", "flux-consumer", "make", false},
		{"admitted: a sibling group of workloads", true, later, x, "group-workloads", "flux-consumer", "make", false},
		{"refused: a sibling group's Flux member maker", true, later, x, "flux-consumer", "group-flux-consumer", "make", true},
		{"refused: sibling groups both", true, later, x, "group-flux-consumer", "group-flux-consumer", "make", true},
	} {
		for _, ns := range []string{"flux", ""} {
			t.Run(tc.name+"/fluxNamespace="+ns, func(t *testing.T) {
				early := Component{Name: "early", Type: tc.earlyTyp}
				maker := Component{Name: "maker", Type: tc.makerTyp, Traits: trait(tc.makerTrait)}
				in := sourceInput{fluxNS: ns, wait: tc.wait, deps: tc.deps, required: tc.required}
				_, err := sourceInputTransform(t, in, maker, early)
				if !tc.refused {
					if err != nil {
						t.Fatalf("TransformWithPolicy: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("TransformWithPolicy admitted it, want the late maker refused")
				}
				for _, want := range []string{`ocirepository "app-source" requires Secret "x"`, `component "maker"`, `component "early"`, "never converges"} {
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

// TestGeneratedSource_LateWorkloadMaker: a trait of a workload in a later
// group makes its Secret in the application namespace, so it is refused for a
// generated source that lands there too, without a Flux namespace, and
// admitted under one, where the source reads another Secret
// (go-kure/launcher#1079).
func TestGeneratedSource_LateWorkloadMaker(t *testing.T) {
	early := Component{Name: "early", Type: "flux-consumer"}
	maker := Component{Name: "maker", Type: "workload-consumer", Traits: []Trait{{Type: "make", Properties: map[string]any{}}}}
	deps := map[string][]string{"maker": {"early"}}
	if _, err := sourceInputTransform(t, sourceInput{fluxNS: "flux", wait: true, deps: deps, required: []string{"x"}}, maker, early); err != nil {
		t.Errorf("under a Flux namespace: TransformWithPolicy: %v", err)
	}
	_, err := sourceInputTransform(t, sourceInput{wait: true, deps: deps, required: []string{"x"}}, maker, early)
	if err == nil || !strings.Contains(err.Error(), `ocirepository "app-source" requires Secret "x", which a trait of component "maker"`) {
		t.Errorf("without a Flux namespace: error = %v, want the late workload maker refused", err)
	}
}

// TestGeneratedSource_LateMakerMessage holds the refusal's whole message.
func TestGeneratedSource_LateMakerMessage(t *testing.T) {
	early := Component{Name: "early", Type: "flux-consumer"}
	maker := Component{Name: "maker", Type: "flux-consumer", Traits: []Trait{{Type: "make", Properties: map[string]any{}}}}
	in := sourceInput{fluxNS: "flux", wait: true, deps: map[string][]string{"maker": {"early"}}, required: []string{"x"}}
	_, err := sourceInputTransform(t, in, maker, early)
	want := `ocirepository "app-source" requires Secret "x", which a trait of component "maker" makes in group "01", later than component "early" in group "00", a consumer of "app-source"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the source, the source for the Secret, and the group of "maker", which makes it, for "early"'s, so the delivery never converges: put the trait on a consumer in the earliest group, or provide the Secret apart from the document in the Flux namespace "flux"`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v\nwant     %s", err, want)
	}
}

// TestGeneratedSource_LateComponentMaker: a Secret component of a later group
// than a generated source's earliest consumer, named as the Secret the source
// requires, is refused where the source lands with it: without a Flux
// namespace, or under one equal to the application namespace "ns"; under
// "flux" the source reads another Secret (go-kure/launcher#1086).
func TestGeneratedSource_LateComponentMaker(t *testing.T) {
	early := Component{Name: "early", Type: "flux-consumer"}
	maker := Component{Name: "x", Type: "sec"}
	deps := map[string][]string{"x": {"early"}}
	if _, err := sourceInputTransform(t, sourceInput{fluxNS: "flux", wait: true, deps: deps, required: []string{"x"}}, maker, early); err != nil {
		t.Errorf("under a Flux namespace: TransformWithPolicy: %v", err)
	}
	if _, err := sourceInputTransform(t, sourceInput{deps: deps, required: []string{"x"}}, maker, early); err != nil {
		t.Errorf("under the default delivery: TransformWithPolicy: %v", err)
	}
	if _, err := sourceInputTransform(t, sourceInput{fluxNS: "ns", wait: true, deps: map[string][]string{"early": {"x"}}, required: []string{"x"}}, maker, early); err != nil {
		t.Errorf("with the maker earlier: TransformWithPolicy: %v", err)
	}
	for _, ns := range []string{"", "ns"} {
		_, err := sourceInputTransform(t, sourceInput{fluxNS: ns, wait: true, deps: deps, required: []string{"x"}}, maker, early)
		if err == nil || !strings.Contains(err.Error(), `ocirepository "app-source" requires Secret "x", which component "x" (sec) makes`) {
			t.Errorf("fluxNamespace=%q: error = %v, want the late component maker refused", ns, err)
		}
	}
}

// TestGeneratedSource_LateComponentMakerMessage holds the component maker's
// refusal of a generated source whole.
func TestGeneratedSource_LateComponentMakerMessage(t *testing.T) {
	early := Component{Name: "early", Type: "flux-consumer"}
	in := sourceInput{wait: true, deps: map[string][]string{"x": {"early"}}, required: []string{"x"}}
	_, err := sourceInputTransform(t, in, Component{Name: "x", Type: "sec"}, early)
	want := `ocirepository "app-source" requires Secret "x", which component "x" (sec) makes in group "01", later than component "early" in group "00", a consumer of "app-source"; with each group waiting for its Flux objects, "early"'s group waits for "early", "early" for the source, the source for the Secret, and the group of "x", which makes it, for "early"'s, so the delivery never converges: place component "x" in the group of "early" or earlier, or provide the Secret apart from the document`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v\nwant     %s", err, want)
	}
}

// TestFluxInputReaders: a Flux object's readers gain the generated sources it
// is ordered after, once each, after its own; a member with no readers gains
// none.
func TestFluxInputReaders(t *testing.T) {
	app := func(name string) *stack.Application { return stack.NewApplication(name, "ns", &readerStub{}) }
	own, flux2, work := app("own"), app("flux2"), stack.NewApplication("w", "ns", &workloadStub{})
	s1, s2 := app("s1"), app("s2")
	sources := map[*stack.Application][]*stack.Application{own: {s1, s2}, flux2: {s1}}
	group := componentEntry{members: []componentEntry{{app: own}, {app: flux2}, {app: work}}}
	for _, tc := range []struct {
		name        string
		e           componentEntry
		fluxMembers []*stack.Application
		member      *stack.Application
		want        []*stack.Application
	}{
		{"a component", componentEntry{}, nil, own, []*stack.Application{own, s1, s2}},
		{"a component ordered after none", componentEntry{}, nil, work, []*stack.Application{work}},
		{"a group's Flux member, a source both read once", group, []*stack.Application{own, flux2}, own, []*stack.Application{own, flux2, s1, s2}},
		{"a group's workload member", group, []*stack.Application{own, flux2}, work, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fluxInputReaders(tc.e, tc.fluxMembers, tc.member, sources)
			if !slices.Equal(got, tc.want) {
				t.Errorf("fluxInputReaders = %v, want %v", appNames(got), appNames(tc.want))
			}
		})
	}
	// The group's own list is not appended to in place, spare capacity or not.
	members := make([]*stack.Application, 2, 4)
	copy(members, []*stack.Application{own, flux2})
	_ = fluxInputReaders(group, members, own, sources)
	if spare := members[:4]; spare[2] != nil || spare[3] != nil {
		t.Errorf("fluxInputReaders appended to the group's Flux members in place: %v", appNames(spare))
	}
}

func appNames(apps []*stack.Application) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}
