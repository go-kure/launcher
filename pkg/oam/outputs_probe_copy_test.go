package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The tests below pin go-kure/launcher#1349: an output provider is asked about a copy
// of the component (probeCopy), and a component built in Go can hold a pointer or a
// struct value in its properties. A write the provider makes through one must not
// reach the lowering, the handler, or the caller's own document.

// probeWriteRule is a lowering rule whose Outputs writes through the component it is
// handed (write) and whose lowering records what it then reads (read).
type probeWriteRule struct {
	write func(c *Component)
	read  func(c *Component) string
	seen  *string
}

func (r probeWriteRule) ComponentType() string { return "db" }

func (r probeWriteRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	r.write(c)
	return declareResolved(outputClusterKind)(c, lctx)
}

func (r probeWriteRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.seen = r.read(c)
	return loweringOutputRule{kinds: []schema.GroupKind{outputClusterKind}}.LowerComponent(c, lctx)
}

func probeWriteTransformer(rule probeWriteRule) *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}},
		map[string]TraitHandler{"rec": &recordingTraitHandler{typ: "rec"}})
	tr.RegisterComponentLowering(rule)
	return tr
}

// boxedMap is a struct value that holds a map.
type boxedMap struct{ M map[string]string }

// namedThing is a plain struct a pointer property points to.
type namedThing struct{ Name string }

// sealedBox keeps its value behind an unexported pointer, and copies it in DeepCopy
// (pointer receiver, the form of the generated Kubernetes deepcopy).
type sealedBox struct{ v *string }

func (b *sealedBox) DeepCopy() *sealedBox {
	if b == nil {
		return nil
	}
	s := *b.v
	return &sealedBox{v: &s}
}
func (b *sealedBox) Set(s string) { *b.v = s }
func (b *sealedBox) Get() string  { return *b.v }

// opaqueBox keeps its value behind an unexported pointer and has no DeepCopy.
type opaqueBox struct{ v *string }

// probeNode can point to itself.
type probeNode struct {
	Name string
	Next *probeNode
}

func TestOutputCheck_ProviderWriteThroughPointerDoesNotReachTheBuild(t *testing.T) {
	type row struct {
		name   string
		comp   func() (Component, func() string) // the component, and a read of the caller's own value
		write  func(c *Component)
		read   func(c *Component) string
		wanted string
	}
	rows := []row{
		{name: "P1 pointer to a string",
			comp: func() (Component, func() string) {
				s := "orig"
				return Component{Properties: map[string]any{"p": &s}}, func() string { return s }
			},
			write: func(c *Component) { *c.Properties["p"].(*string) = "mutated" },
			read:  func(c *Component) string { return *c.Properties["p"].(*string) }, wanted: "orig"},
		{name: "P2 struct value holding a map",
			comp: func() (Component, func() string) {
				b := boxedMap{M: map[string]string{"x": "orig"}}
				return Component{Properties: map[string]any{"p": b}}, func() string { return b.M["x"] }
			},
			write: func(c *Component) { c.Properties["p"].(boxedMap).M["x"] = "mutated" },
			read:  func(c *Component) string { return c.Properties["p"].(boxedMap).M["x"] }, wanted: "orig"},
		{name: "P3 pointer to a struct",
			comp: func() (Component, func() string) {
				n := &namedThing{Name: "orig"}
				return Component{Properties: map[string]any{"p": n}}, func() string { return n.Name }
			},
			write: func(c *Component) { c.Properties["p"].(*namedThing).Name = "mutated" },
			read:  func(c *Component) string { return c.Properties["p"].(*namedThing).Name }, wanted: "orig"},
		{name: "P4 pointer to a string in a trait's properties",
			comp: func() (Component, func() string) {
				s := "orig"
				return Component{Traits: []Trait{{Type: "rec", Properties: map[string]any{"p": &s}}}}, func() string { return s }
			},
			write: func(c *Component) { *c.Traits[0].Properties["p"].(*string) = "mutated" },
			read:  func(c *Component) string { return *c.Traits[0].Properties["p"].(*string) }, wanted: "orig"},
		{name: "P7 pointer to a Quantity with its decimal set",
			comp: func() (Component, func() string) {
				q := resource.MustParse("1.5")
				q.ToDec()
				return Component{Properties: map[string]any{"p": &q}}, func() string { return q.String() }
			},
			write: func(c *Component) { c.Properties["p"].(*resource.Quantity).Add(resource.MustParse("1")) },
			read:  func(c *Component) string { return c.Properties["p"].(*resource.Quantity).String() }, wanted: "1500m"},
		{name: "P8 pointer to a type with a pointer-receiver DeepCopy",
			comp: func() (Component, func() string) {
				s := "orig"
				b := &sealedBox{v: &s}
				return Component{Properties: map[string]any{"p": b}}, func() string { return b.Get() }
			},
			write: func(c *Component) { c.Properties["p"].(*sealedBox).Set("mutated") },
			read:  func(c *Component) string { return c.Properties["p"].(*sealedBox).Get() }, wanted: "orig"},
		{name: "P9 pointer to a corev1 Container",
			comp: func() (Component, func() string) {
				ct := &corev1.Container{Name: "app", Env: []corev1.EnvVar{{Name: "A", Value: "orig"}}}
				return Component{Properties: map[string]any{"p": ct}}, func() string { return ct.Env[0].Value }
			},
			write: func(c *Component) { c.Properties["p"].(*corev1.Container).Env[0].Value = "mutated" },
			read:  func(c *Component) string { return c.Properties["p"].(*corev1.Container).Env[0].Value }, wanted: "orig"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			var seen string
			tr := probeWriteTransformer(probeWriteRule{write: r.write, read: r.read, seen: &seen})
			comp, caller := r.comp()
			comp.Name, comp.Type = "pg", "db"
			if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if seen != r.wanted {
				t.Errorf("the lowering read %q, want %q: the provider's write reached the build", seen, r.wanted)
			}
			if got := caller(); got != r.wanted {
				t.Errorf("the caller's value is %q, want %q: the provider's write reached the caller's document", got, r.wanted)
			}
		})
	}
}

// P5: a query asks a provider about the caller's own component; a write through a
// pointer leaves it as it was.
func TestOutputCheck_QueryWriteThroughPointerLeavesTheDocumentAlone(t *testing.T) {
	var seen string
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) { *c.Properties["p"].(*string) = "mutated" },
		read:  func(c *Component) string { return *c.Properties["p"].(*string) },
		seen:  &seen,
	})
	s := "orig"
	app := terminalApp(Component{Name: "pg", Type: "db", Properties: map[string]any{"p": &s}})
	comp := &app.Spec.Components[0]
	if _, err := tr.ComponentOutputs(comp); err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	if s != "orig" {
		t.Errorf("after ComponentOutputs the caller's value is %q, want %q", s, "orig")
	}
	if _, err := tr.ComponentOutputsNamed("app", comp, nil); err != nil {
		t.Fatalf("ComponentOutputsNamed: %v", err)
	}
	if s != "orig" {
		t.Errorf("after ComponentOutputsNamed the caller's value is %q, want %q", s, "orig")
	}
}

// writingObjectHandler is objectOutputHandler whose Outputs writes through the
// pointer property "p" and whose ToApplicationConfig records what it then reads.
type writingObjectHandler struct {
	objectOutputHandler
	seen *string
}

func (h *writingObjectHandler) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	*c.Properties["p"].(*string) = "mutated"
	return h.objectOutputHandler.Outputs(c, lctx)
}

func (h *writingObjectHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	*h.seen = *c.Properties["p"].(*string)
	return &stubAppConfig{}, nil
}

// P6: the handler path.
func TestOutputCheck_HandlerWriteThroughPointerDoesNotReachTheBuild(t *testing.T) {
	var seen string
	h := &writingObjectHandler{objectOutputHandler: objectOutputHandler{typ: "cfg"}, seen: &seen}
	tr := NewTransformer(map[string]ComponentHandler{"cfg": h}, nil)
	s := "orig"
	if _, err := tr.Transform(terminalApp(Component{Name: "settings", Type: "cfg", Properties: map[string]any{"p": &s}}), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if seen != "orig" {
		t.Errorf("ToApplicationConfig read %q, want %q: the provider's write reached the build", seen, "orig")
	}
	if s != "orig" {
		t.Errorf("the caller's value is %q, want %q", s, "orig")
	}
}

// Controls. They pass before and after the fix, so they are not evidence for it.

// C2: a pointer cycle in the properties. The probe copy ends and the build passes.
func TestOutputCheck_ProbeCopyEndsOnAPointerCycle(t *testing.T) {
	var seen string
	tr := probeWriteTransformer(probeWriteRule{
		write: func(*Component) {},
		read:  func(c *Component) string { return c.Properties["p"].(*probeNode).Next.Name },
		seen:  &seen,
	})
	n := &probeNode{Name: "orig"}
	n.Next = n
	if _, err := tr.Transform(terminalApp(Component{Name: "pg", Type: "db", Properties: map[string]any{"p": n}}), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if seen != "orig" {
		t.Errorf("the lowering read %q, want %q", seen, "orig")
	}
}

// C3: two keys that hold one pointer still hold one pointer in the probe.
func TestOutputCheck_ProbeCopyKeepsAliasedPointersAliased(t *testing.T) {
	var seen, through string
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			*c.Properties["a"].(*string) = "mutated"
			through = *c.Properties["b"].(*string)
		},
		read: func(*Component) string { return "" },
		seen: &seen,
	})
	s := "orig"
	if _, err := tr.Transform(terminalApp(Component{Name: "pg", Type: "db", Properties: map[string]any{"a": &s, "b": &s}}), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if through != "mutated" {
		t.Errorf("in the probe, key b read %q after a write through key a, want %q: one pointer became two", through, "mutated")
	}
}

// probePtr is a defined pointer type: a property holds it behind an interface.
type probePtr *string

// C5: a pointer used as a map key is copied too, so a write through a key never reaches
// the caller's value.
func TestOutputCheck_ProbeCopyCopiesPointerMapKeys(t *testing.T) {
	var seen string
	var wrote int
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			for k := range c.Properties["m"].(map[*string]int) {
				*k = "mutated"
				wrote++
			}
		},
		read: func(c *Component) string {
			for k := range c.Properties["m"].(map[*string]int) {
				return *k
			}
			return ""
		},
		seen: &seen,
	})
	s := "orig"
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"m": map[*string]int{&s: 1}}}
	if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if wrote != 1 {
		t.Errorf("the provider wrote through %d map keys in the probe, want 1: the probe lost the entry, so nothing proves the isolation", wrote)
	}
	if seen != "orig" {
		t.Errorf("the lowering read %q through a map key, want %q: the provider's write reached the build", seen, "orig")
	}
	if s != "orig" {
		t.Errorf("the caller's value is %q, want %q: a write through a map key reached the caller", s, "orig")
	}
}

// C9: pointers to distinct zero-size values stay distinct map keys in the probe. A
// zero-size value holds no state, so the copy keeps it shared; copying it would give
// every key the runtime's one zero-size address and drop entries.
func TestOutputCheck_ProbeCopyKeepsDistinctZeroSizeKeys(t *testing.T) {
	var seen string
	var inProbe int
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) { inProbe = len(c.Properties["m"].(map[*struct{}]int)) },
		read:  func(*Component) string { return "" },
		seen:  &seen,
	})
	var owners [2]struct {
		Key struct{}
		Pad byte
	}
	m := map[*struct{}]int{&owners[0].Key: 1, &owners[1].Key: 2}
	if len(m) != 2 {
		t.Fatalf("the fixture holds %d keys, want 2", len(m))
	}
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"m": m}}
	if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if inProbe != 2 {
		t.Errorf("the probe's map holds %d zero-size keys, want 2: the copy dropped entries", inProbe)
	}
}

// C10: a defined pointer type and an ordinary pointer to one value are one pointer in the
// probe, each keeping its own Go type.
func TestOutputCheck_ProbeCopyKeepsADefinedAndAnOrdinaryPointerAliased(t *testing.T) {
	var seen, through string
	var typedOK bool
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			p, ok := c.Properties["a"].(probePtr)
			typedOK = ok
			if !ok {
				return
			}
			*p = "mutated"
			through = *c.Properties["b"].(*string)
		},
		read: func(*Component) string { return "" },
		seen: &seen,
	})
	s := "orig"
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"a": probePtr(&s), "b": &s}}
	if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !typedOK {
		t.Fatalf("in the probe, key a is no longer a probePtr")
	}
	if through != "mutated" {
		t.Errorf("in the probe, the ordinary pointer read %q after a write through the defined one, want %q: one pointee became two", through, "mutated")
	}
	if s != "orig" {
		t.Errorf("the caller's value is %q, want %q", s, "orig")
	}
}

// probeRecSlice is a slice type whose elements are slices of itself, so a pointer to
// its first element and the slice that element holds can share one address.
type probeRecSlice []probeRecSlice

// C11: a pointer to the first element of a slice, and the empty slice that element
// holds, have the same address and the same element type. The probe keeps them as two
// copies and does not panic. A panic fails the test on its own text, so it is a red
// test and not an invalid run.
func TestOutputCheck_ProbeCopyKeepsAPointerAndASliceAtOneAddressApart(t *testing.T) {
	var seen string
	var reached bool
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			p, ok := c.Properties["p"].(*probeRecSlice)
			reached = ok && p != nil && len(*p) == 0
		},
		read: func(*Component) string { return "" },
		seen: &seen,
	})
	s := make(probeRecSlice, 1)
	s[0] = s[:0]
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"p": &s[0]}}
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
			t.Errorf("Transform: %v", err)
		}
	}()
	if panicked != nil {
		t.Fatalf("the probe copy panicked on a pointer and a slice at one address: %v", panicked)
	}
	if !reached {
		t.Errorf("the provider did not see the pointer, or it did not point at an empty slice")
	}
}

// C6: two keys that hold one pointer to a type with a DeepCopy method still hold one
// pointer in the probe.
func TestOutputCheck_ProbeCopyKeepsDeepCopyAliasesAliased(t *testing.T) {
	var seen, through string
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			c.Properties["a"].(*sealedBox).Set("mutated")
			through = c.Properties["b"].(*sealedBox).Get()
		},
		read: func(*Component) string { return "" },
		seen: &seen,
	})
	s := "orig"
	b := &sealedBox{v: &s}
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"a": b, "b": b}}
	if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if through != "mutated" {
		t.Errorf("in the probe, key b read %q after a write through key a, want %q: one DeepCopy pointer became two", through, "mutated")
	}
	if s != "orig" {
		t.Errorf("the caller's value is %q, want %q", s, "orig")
	}
}

// C7: a property that holds a defined pointer type keeps that type in the probe, so the
// provider's own type assertion still holds.
func TestOutputCheck_ProbeCopyKeepsADefinedPointerType(t *testing.T) {
	var seen string
	var assertedOK bool
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			p, ok := c.Properties["p"].(probePtr)
			assertedOK = ok
			if ok {
				*p = "mutated"
			}
		},
		read: func(c *Component) string { return *(c.Properties["p"].(probePtr)) },
		seen: &seen,
	})
	s := "orig"
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"p": probePtr(&s)}}
	if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !assertedOK {
		t.Errorf("in the probe, the property is no longer a probePtr: the copy changed its Go type")
	}
	if seen != "orig" || s != "orig" {
		t.Errorf("the lowering read %q and the caller's value is %q, want both %q", seen, s, "orig")
	}
}

// C8: a pointer that a component property and a trait property both hold is one pointer
// in the probe.
func TestOutputCheck_ProbeCopyKeepsAliasesAcrossTheComponentAndItsTraits(t *testing.T) {
	var seen, through string
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) {
			*c.Properties["a"].(*string) = "mutated"
			through = *c.Traits[0].Properties["b"].(*string)
		},
		read: func(*Component) string { return "" },
		seen: &seen,
	})
	s := "orig"
	comp := Component{Name: "pg", Type: "db", Properties: map[string]any{"a": &s},
		Traits: []Trait{{Type: "rec", Properties: map[string]any{"b": &s}}}}
	if _, err := tr.Transform(terminalApp(comp), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if through != "mutated" {
		t.Errorf("in the probe, the trait's pointer read %q after a write through the component's, want %q: one pointer became two", through, "mutated")
	}
	if s != "orig" {
		t.Errorf("the caller's value is %q, want %q", s, "orig")
	}
}

// C4: an unexported pointer field of a type with no DeepCopy stays shared: the stated
// limit of the probe copy.
func TestOutputCheck_ProbeCopySharesUnexportedFieldsWithoutDeepCopy(t *testing.T) {
	var seen string
	tr := probeWriteTransformer(probeWriteRule{
		write: func(c *Component) { *c.Properties["p"].(*opaqueBox).v = "mutated" },
		read:  func(c *Component) string { return *c.Properties["p"].(*opaqueBox).v },
		seen:  &seen,
	})
	s := "orig"
	if _, err := tr.Transform(terminalApp(Component{Name: "pg", Type: "db", Properties: map[string]any{"p": &opaqueBox{v: &s}}}), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if seen != "mutated" {
		t.Errorf("the lowering read %q, want %q: the stated limit changed", seen, "mutated")
	}
}
