package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// TestMoveFluxNamespaceInputs: a sub-application moves only when one of its
// readers reads its object, matched on both kind and name; any other stays.
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

	// A group member's trait object moves when another of its readers reads it,
	// and one with no readers stays even when its owner reads it.
	groupSub := sub("group-sub", &inputStub{kind: "Secret", name: "s"})
	noReadersSub := sub("no-readers-sub", &inputStub{kind: "Secret", name: "s"})

	moveFluxNamespaceInputs([]traitSubApps{
		{owner: owner, readers: []*stack.Application{owner}, subApps: []*stack.Application{readCM, readSecret, wrongKind, unread, notInput}},
		{owner: nonReader, readers: []*stack.Application{nonReader}, subApps: []*stack.Application{nonReaderSub}},
		{owner: nonReader, readers: []*stack.Application{nonReader, owner}, subApps: []*stack.Application{groupSub}},
		{owner: owner, subApps: []*stack.Application{noReadersSub}},
	}, "flux")

	for _, tc := range []struct {
		app  *stack.Application
		want string
	}{
		{readCM, "flux"}, {readSecret, "flux"}, {groupSub, "flux"},
		{wrongKind, "app"}, {unread, "app"}, {notInput, "app"}, {nonReaderSub, "app"}, {noReadersSub, "app"},
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

// claimingSecretTrait is secretInputTrait claiming its Secret "x" where it
// lands, as the external-secret trait claims its Secret (NameSpec.FluxInput).
type claimingSecretTrait struct{}

func (claimingSecretTrait) CanHandle(string) bool { return true }
func (claimingSecretTrait) Apply(t *Trait, app *stack.Application, b *stack.Bundle) error {
	if err := t.ClaimGeneratedObjectName(schema.GroupKind{Kind: "Secret"}, app.Namespace, "x", "", "Secret", "x"); err != nil {
		return err
	}
	return secretInputTrait{}.Apply(t, app, b)
}

// workloadStub is a component config that takes no Flux namespace: a workload.
type workloadStub struct{}

func (*workloadStub) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }

// decoratedStub wraps a config as a trait decorator does (builtin/traits
// decoratorBase): it answers fluxNamespaceSettable and fluxNamespaceReader
// whatever its inner config is.
type decoratedStub struct{ inner stack.ApplicationConfig }

func (d *decoratedStub) Generate(a *stack.Application) ([]*client.Object, error) {
	return d.inner.Generate(a)
}

func (d *decoratedStub) SetFluxNamespace(ns string) {
	if s, ok := d.inner.(fluxNamespaceSettable); ok {
		s.SetFluxNamespace(ns)
	}
}

func (d *decoratedStub) FluxNamespaceReads() (configMaps, secrets []string) {
	if r, ok := d.inner.(fluxNamespaceReader); ok {
		return r.FluxNamespaceReads()
	}
	return nil, nil
}

// decoratingTrait decorates the application it runs on (decoratedStub).
type decoratingTrait struct{}

func (decoratingTrait) CanHandle(string) bool { return true }
func (decoratingTrait) Apply(_ *Trait, app *stack.Application, _ *stack.Bundle) error {
	app.Config = &decoratedStub{inner: app.Config}
	return nil
}

// fluxInputGroupTransform transforms component "web", a sibling group of a
// Flux member reading Secret "x" (type "a") and a second member of memberType
// carrying traits ("a" puts them on the reading member instead, beside a Flux
// member of type "b"), with others beside it, under the Flux namespace "flux".
func fluxInputGroupTransform(t *testing.T, memberType string, traits []Trait, others ...Component) (*stack.Cluster, error) {
	t.Helper()
	reader := &siblingStubHandler{typ: "a", build: func() stack.ApplicationConfig { return &readerStub{secrets: []string{"x"}} }}
	workload := &siblingStubHandler{typ: "w", build: func() stack.ApplicationConfig { return &workloadStub{} }}
	tr := NewTransformer(map[string]ComponentHandler{"a": reader, "b": stubHandler("b", 0), "w": workload},
		map[string]TraitHandler{"in": secretInputTrait{}, "claim": claimingSecretTrait{}, "decorate": decoratingTrait{}})
	tr.RegisterComponentLowering(emitRule{"pair", func(c *Component) []Component {
		a := Component{Name: c.Name, Type: "a", Properties: map[string]any{}}
		m := Component{Name: c.Name, Type: memberType, Properties: map[string]any{}, Traits: traits}
		if memberType == "a" {
			a.Traits = traits
			m = Component{Name: c.Name, Type: "b", Properties: map[string]any{}}
		}
		return []Component{a, m}
	}})
	doc := siblingDoc(append([]Component{{Name: "web", Type: "pair"}}, others...)...)
	doc.Metadata.Namespace = "ns"
	cluster, _, err := tr.TransformWithPolicy(doc, TransformContext{FluxNamespace: "flux"})
	return cluster, err
}

// TestSiblingGroup_FluxNamespaceInputsFollowTheFluxMembers: in a sibling group
// a trait sub-application moves when any of the group's Flux members reads it,
// whichever Flux member's trait created it, as they land in the one Flux
// namespace together (go-kure/launcher#1012). One a member that is no Flux
// object made stays with that member's workload, also when an earlier trait
// decorated the member, since a decorator takes a Flux namespace whatever it
// wraps. Moved or not, it keeps the component it came from.
func TestSiblingGroup_FluxNamespaceInputsFollowTheFluxMembers(t *testing.T) {
	in := Trait{Type: "in", Properties: map[string]any{}}
	decorate := Trait{Type: "decorate", Properties: map[string]any{}}
	for _, tc := range []struct {
		name       string
		memberType string
		traits     []Trait
		want       string
	}{
		{name: "trait on the reading member", memberType: "a", traits: []Trait{in}, want: "flux"},
		{name: "trait on another Flux member", memberType: "b", traits: []Trait{in}, want: "flux"},
		{name: "trait on a member that takes no Flux namespace", memberType: "w", traits: []Trait{in}, want: "ns"},
		{name: "trait on a decorated member that takes no Flux namespace", memberType: "w", traits: []Trait{decorate, in}, want: "ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster, err := fluxInputGroupTransform(t, tc.memberType, tc.traits)
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
				named, ok := a.Config.(interface{ ComponentName() string })
				if !ok || named.ComponentName() != "web" {
					t.Errorf("web-in config %T is not attributed to component web", a.Config)
				}
			}
			if !found {
				t.Fatal("no web-in application in the bundle")
			}
		})
	}
}

// TestSiblingGroup_FluxNamespaceInputClaims: a group member's trait Secret is
// claimed where it lands, as the move decides: in the Flux namespace for a
// Flux member's, where it collides with another component's Secret of that
// name there, and in the application namespace for a decorated workload
// member's, where it does not.
func TestSiblingGroup_FluxNamespaceInputClaims(t *testing.T) {
	claim := Trait{Type: "claim", Properties: map[string]any{}}
	decorate := Trait{Type: "decorate", Properties: map[string]any{}}
	// "other" reads its own trait's Secret "x", which lands in the Flux namespace.
	other := Component{Name: "other", Type: "a", Properties: map[string]any{}, Traits: []Trait{claim}}

	t.Run("on another Flux member: claimed in the Flux namespace", func(t *testing.T) {
		_, err := fluxInputGroupTransform(t, "b", []Trait{claim}, other)
		want := `Secret "flux/x"`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("TransformWithPolicy error = %v, want a collision naming %s", err, want)
		}
	})
	t.Run("on a decorated workload member: claimed in the application namespace", func(t *testing.T) {
		cluster, err := fluxInputGroupTransform(t, "w", []Trait{decorate, claim}, other)
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		var found bool
		for _, a := range cluster.Node.Bundle.Applications {
			if a.Name != "web-in" {
				continue
			}
			found = true
			if a.Namespace != "ns" {
				t.Errorf("web-in namespace = %q, want %q", a.Namespace, "ns")
			}
		}
		if !found {
			t.Fatal("no web-in application in the bundle")
		}
	})
}

// TestFluxObjectReads: true when any reader's Flux object reads the object,
// matched on kind and name; a reader that is nil or reads nothing is skipped.
func TestFluxObjectReads(t *testing.T) {
	reader := stack.NewApplication("r", "app", &readerStub{configMaps: []string{"a"}, secrets: []string{"s"}})
	nonReader := stack.NewApplication("n", "app", &workloadStub{})
	for _, tc := range []struct {
		name       string
		readers    []*stack.Application
		kind, read string
		want       bool
	}{
		{"no readers", nil, "Secret", "s", false},
		{"a nil reader", []*stack.Application{nil}, "Secret", "s", false},
		{"one that reads nothing", []*stack.Application{nonReader}, "Secret", "s", false},
		{"the second reads the Secret", []*stack.Application{nonReader, reader}, "Secret", "s", true},
		{"the second reads the ConfigMap", []*stack.Application{nil, reader}, "ConfigMap", "a", true},
		{"name read under the other kind", []*stack.Application{nonReader, reader}, "Secret", "a", false},
		{"unread name", []*stack.Application{reader}, "ConfigMap", "b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fluxObjectReads(tc.readers, tc.kind, tc.read); got != tc.want {
				t.Errorf("fluxObjectReads = %v, want %v", got, tc.want)
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
