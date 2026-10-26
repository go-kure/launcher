package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// sourceConsumerRule lowers a component of type typ into the one
// "ocirepository" every such component of the document shares (the first
// invocation emits it, a later one adopts it, as the oci rule shares a source)
// and the consumer itself, of type as, ordered after it and carrying the
// component's traits. With group set the consumer is a same-name sibling group
// of a Flux member ("flux") and a workload member ("w"), both ordered after the
// source, the traits on the member of type as. It declares a schema, so its
// output is synthesized.
type sourceConsumerRule struct {
	typ, as string
	group   bool
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
	if r.group {
		types = []string{"flux", "w"}
	}
	for _, typ := range types {
		c := Component{Name: comp.Name, Type: typ}
		if typ == r.as {
			c.Traits = comp.Traits
		}
		c.OrderAfter(name)
		result.Components = append(result.Components, c)
	}
	return result, nil
}

// sourceInputTransform transforms comps under the Flux namespace "flux" with deps as the dependency policy. The generated
// source reads Secret "x"; "flux" is a Flux object reading nothing, "fx" one
// reading "x", "w" a workload. A "claim" trait makes Secret "x" (claimed where
// it lands), "decorate" decorates its application.
func sourceInputTransform(t *testing.T, deps map[string][]string, comps ...Component) (*stack.Cluster, error) {
	t.Helper()
	tr := NewTransformer(map[string]ComponentHandler{
		"ocirepository": &siblingStubHandler{typ: "ocirepository", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }},
		"flux":          &siblingStubHandler{typ: "flux", build: func() stack.ApplicationConfig { return &readerStub{} }},
		"fx":            &siblingStubHandler{typ: "fx", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }},
		"w":             &siblingStubHandler{typ: "w", build: func() stack.ApplicationConfig { return &workloadStub{} }},
	}, map[string]TraitHandler{"claim": claimingSecretTrait{}, "decorate": decoratingTrait{}})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "flux-consumer", as: "flux"})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "workload-consumer", as: "w"})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "group-flux-consumer", as: "flux", group: true})
	tr.RegisterComponentLowering(sourceConsumerRule{typ: "group-workload-consumer", as: "w", group: true})
	tr.RegisterPolicy("dependency", &depsPolicyHandler{deps: deps})
	doc := siblingDoc(comps...)
	doc.Metadata.Namespace = "ns"
	doc.Spec.Policies = []ApplicationPolicy{{Name: "order", Type: "dependency"}}
	cluster, _, err := tr.TransformWithPolicy(doc, TransformContext{FluxNamespace: "flux"})
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
			cluster, err := sourceInputTransform(t, nil, c)
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
			if _, err := sourceInputTransform(t, nil, c, collides); err == nil || !strings.Contains(err.Error(), `Secret "`+tc.want+`/x"`) {
				t.Errorf("with %s: error = %v, want a collision naming Secret %s/x", collides.Type, err, tc.want)
			}
			if _, err := sourceInputTransform(t, nil, c, admitted); err != nil {
				t.Errorf("with %s: TransformWithPolicy: %v", admitted.Type, err)
			}
		})
	}
}

// TestGeneratedSource_LateMaker: the Flux consumer whose trait makes the
// generated source's Secret is admitted in a later group than another consumer
// of the source, Flux object or workload, and the Secret moves
// (go-kure/launcher#1039 tracks the delivery that then waits on itself).
func TestGeneratedSource_LateMaker(t *testing.T) {
	claim := Trait{Type: "claim", Properties: map[string]any{}}
	maker := Component{Name: "maker", Type: "flux-consumer", Traits: []Trait{claim}}
	for _, typ := range []string{"flux-consumer", "workload-consumer"} {
		t.Run("after a "+typ, func(t *testing.T) {
			early := Component{Name: "early", Type: typ}
			cluster, err := sourceInputTransform(t, map[string][]string{"maker": {"early"}}, maker, early)
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if in := clusterApplication(cluster, "maker-in"); in == nil || in.Namespace != "flux" {
				t.Errorf("maker-in = %+v, want it in the Flux namespace", in)
			}
		})
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
