package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// readerStub is a moving Flux config reading the named ConfigMaps and Secrets.
type readerStub struct {
	siblingStub
	configMaps, secrets []string
}

func (r *readerStub) FluxNamespaceReads() (configMaps, secrets []string) {
	return r.configMaps, r.secrets
}

// inputStub is a trait sub-application config whose object is kind/name.
type inputStub struct {
	siblingStub
	kind, name string
}

func (i *inputStub) FluxNamespaceInput() (kind, name string) { return i.kind, i.name }

// TestMoveFluxNamespaceInputs: a sub-application moves only when its owner
// reads its object, matched on both kind and name; any other stays.
func TestMoveFluxNamespaceInputs(t *testing.T) {
	sub := func(name string, cfg stack.ApplicationConfig) *stack.Application {
		return stack.NewApplication(name, "app", cfg)
	}
	readCM := sub("read-cm", &inputStub{kind: "ConfigMap", name: "a"})
	readSecret := sub("read-secret", &inputStub{kind: "Secret", name: "s"})
	wrongKind := sub("wrong-kind", &inputStub{kind: "Secret", name: "a"})
	unread := sub("unread", &inputStub{kind: "ConfigMap", name: "b"})
	notInput := sub("not-input", &siblingStub{})
	owner := stack.NewApplication("c", "app", &readerStub{configMaps: []string{"a"}, secrets: []string{"s"}})

	nonReaderSub := sub("non-reader-sub", &inputStub{kind: "ConfigMap", name: "a"})
	nonReader := stack.NewApplication("d", "app", &siblingStub{})

	moveFluxNamespaceInputs([]traitSubApps{
		{owner: owner, subApps: []*stack.Application{readCM, readSecret, wrongKind, unread, notInput}},
		{owner: nonReader, subApps: []*stack.Application{nonReaderSub}},
	}, "flux")

	for _, tc := range []struct {
		app  *stack.Application
		want string
	}{
		{readCM, "flux"}, {readSecret, "flux"},
		{wrongKind, "app"}, {unread, "app"}, {notInput, "app"}, {nonReaderSub, "app"},
		{owner, "app"}, // the owner's own namespace is its config's business
	} {
		if tc.app.Namespace != tc.want {
			t.Errorf("%s namespace = %q, want %q", tc.app.Name, tc.app.Namespace, tc.want)
		}
	}
}

// secretInputTrait appends a sub-application producing Secret "x".
type secretInputTrait struct{}

func (secretInputTrait) CanHandle(string) bool { return true }
func (secretInputTrait) Apply(_ *Trait, app *stack.Application, b *stack.Bundle) error {
	b.Applications = append(b.Applications, stack.NewApplication(app.Name+"-in", app.Namespace, &inputStub{kind: "Secret", name: "x"}))
	return nil
}

// TestSiblingGroup_FluxNamespaceInputsFollowTheirMember: in a sibling group a
// trait sub-application follows the member whose trait created it, so it moves
// only when that member reads it, not when another member does.
func TestSiblingGroup_FluxNamespaceInputsFollowTheirMember(t *testing.T) {
	in := []Trait{{Type: "in", Properties: map[string]any{}}}
	for _, tc := range []struct {
		name     string
		onReader bool
		want     string
	}{
		{name: "trait on the reading member", onReader: true, want: "flux"},
		{name: "trait on another member", onReader: false, want: "ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &siblingStubHandler{typ: "a", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }}
			tr := NewTransformer(map[string]ComponentHandler{"a": reader, "b": stubHandler("b", 0)},
				map[string]TraitHandler{"in": secretInputTrait{}})
			tr.RegisterComponentLowering(emitRule{"pair", func(c *Component) []Component {
				a := Component{Name: c.Name, Type: "a", Properties: map[string]any{}}
				b := Component{Name: c.Name, Type: "b", Properties: map[string]any{}}
				if tc.onReader {
					a.Traits = in
				} else {
					b.Traits = in
				}
				return []Component{a, b}
			}})
			doc := siblingDoc(Component{Name: "web", Type: "pair"})
			doc.Metadata.Namespace = "ns"
			cluster, _, err := tr.TransformWithPolicy(doc, TransformContext{FluxNamespace: "flux"})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			var found bool
			for _, a := range cluster.Node.Bundle.Applications {
				if a.Name != "web-in" {
					continue
				}
				found = true
				if a.Namespace != tc.want {
					t.Errorf("web-in namespace = %q, want %q", a.Namespace, tc.want)
				}
			}
			if !found {
				t.Fatal("no web-in application in the bundle")
			}
		})
	}
}

// TestSiblingGroup_FluxNamespaceReads: a group reports every member's reads.
func TestSiblingGroup_FluxNamespaceReads(t *testing.T) {
	g := &siblingGroupConfig{members: []*stack.Application{
		stack.NewApplication("c", "ns", &readerStub{configMaps: []string{"a"}}),
		stack.NewApplication("c", "ns", &siblingStub{}),
		stack.NewApplication("c", "ns", &readerStub{secrets: []string{"s"}}),
	}}
	cms, secs := g.FluxNamespaceReads()
	if len(cms) != 1 || cms[0] != "a" || len(secs) != 1 || secs[0] != "s" {
		t.Errorf("FluxNamespaceReads = (%v, %v), want ([a], [s])", cms, secs)
	}
}
