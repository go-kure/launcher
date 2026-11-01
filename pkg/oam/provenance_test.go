package oam

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/go-kure/kure/pkg/stack"
)

var updateProvenance = flag.Bool("update", false, "rewrite the provenance golden files")

// provCapture is a component and trait handler pair that keeps the final
// properties each one was handed, keyed for the completeness test.
type provCapture struct {
	components map[string]map[string]any
	traits     map[string]map[string]any
}

func newProvCapture() *provCapture {
	return &provCapture{components: map[string]map[string]any{}, traits: map[string]map[string]any{}}
}

type provComponentHandler struct {
	typ string
	c   *provCapture
}

func (h provComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h provComponentHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.c.components[c.Name+"/"+c.Type] = c.Properties
	return &stubAppConfig{}, nil
}

// provStoreHandler takes "class" from the "store" capability, as a top-level fill.
type provStoreHandler struct {
	provComponentHandler
}

func (provStoreHandler) CapabilityDefaults() (string, []string) { return "store", []string{"class"} }
func (provStoreHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"size": {Type: PropertyTypeString}, "class": {}}
}

type provTraitHandler struct {
	typ string
	c   *provCapture
}

func (h provTraitHandler) CanHandle(t string) bool { return t == h.typ }
func (h provTraitHandler) Apply(tr *Trait, app *stack.Application, _ *stack.Bundle) error {
	h.c.traits[app.Name+"/"+tr.Type] = tr.Properties
	return nil
}

func provTransformer(c *provCapture) *Transformer {
	return NewTransformer(
		map[string]ComponentHandler{
			"web":   provComponentHandler{typ: "web", c: c},
			"store": provStoreHandler{provComponentHandler{typ: "store", c: c}},
		},
		map[string]TraitHandler{
			"ingress": provTraitHandler{typ: "ingress", c: c},
			"expose":  provTraitHandler{typ: "expose", c: c},
		})
}

func provApp(components ...Component) *Application {
	app := makeApp("myapp", components...)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind
	return app
}

type provScenario struct {
	name string
	app  func() *Application
	caps map[string]CapabilityBinding
}

func rend(m map[string]any) CapabilityBinding { return CapabilityBinding{Rendering: m} }

func provScenarios() []provScenario {
	return []provScenario{
		{name: "01-authored-only", app: func() *Application {
			return provApp(Component{Name: "web", Type: "web",
				Properties: map[string]any{"image": "nginx", "env": map[string]any{"A": "1"}},
				Traits:     []Trait{{Type: "expose", Properties: map[string]any{"port": 80}}}})
		}},
		{name: "02-nested-override", app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{},
				Traits: []Trait{{Type: "ingress", Properties: map[string]any{"tls": map[string]any{"enabled": false}}}}})
		}, caps: map[string]CapabilityBinding{
			"ingress": rend(map[string]any{"tls": map[string]any{"enabled": true, "issuer": "letsencrypt"}}),
		}},
		{name: "03-null-over-rendered", app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{},
				Traits: []Trait{{Type: "ingress", Properties: map[string]any{"host": nil, "path": "/"}}}})
		}, caps: map[string]CapabilityBinding{
			"ingress": rend(map[string]any{"host": "example.com"}),
		}},
		{name: "04-list-replaces-list", app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{},
				Traits: []Trait{{Type: "expose", Properties: map[string]any{"ports": []any{8080}}}}})
		}, caps: map[string]CapabilityBinding{
			"expose": rend(map[string]any{"ports": []any{80, 443}}),
		}},
		{name: "05-scoped-and-bare-key", app: func() *Application {
			return provApp(
				Component{Name: "internal", Type: "web", Properties: map[string]any{},
					Traits: []Trait{{Type: "ingress", Properties: map[string]any{"scope": "internal"}}}},
				Component{Name: "external", Type: "web", Properties: map[string]any{},
					Traits: []Trait{{Type: "ingress", Properties: map[string]any{"scope": "external"}}}})
		}, caps: map[string]CapabilityBinding{
			"ingress.internal": rend(map[string]any{"class": "internal-nginx"}),
			"ingress":          rend(map[string]any{"class": "public-nginx"}),
		}},
		{name: "06-empty-rendering-and-no-binding", app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{},
				Traits: []Trait{
					{Type: "expose", Properties: map[string]any{"port": 80, "opts": map[string]any{"a": 1}}},
					{Type: "ingress", Properties: map[string]any{"host": "h"}},
				}})
		}, caps: map[string]CapabilityBinding{"expose": rend(map[string]any{})}},
		{name: "07-component-top-level-fill", app: func() *Application {
			return provApp(
				Component{Name: "filled", Type: "store", Properties: map[string]any{"size": "1Gi"}},
				Component{Name: "kept", Type: "store", Properties: map[string]any{"size": "2Gi", "class": "mine"}},
				Component{Name: "nulled", Type: "store", Properties: map[string]any{"class": nil}})
		}, caps: map[string]CapabilityBinding{
			"store": rend(map[string]any{"class": "platform", "extra": "never-copied"}),
		}},
		{name: "13-untouched-rendering-key-and-empty-object", app: func() *Application {
			return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{},
				Traits: []Trait{{Type: "ingress", Properties: map[string]any{
					"options": map[string]any{"enabled": true},
					"labels":  map[string]any{},
				}}}})
		}, caps: map[string]CapabilityBinding{
			"ingress": rend(map[string]any{
				"class":   "nginx",
				"options": map[string]any{},
				"labels":  map[string]any{},
				"empty":   []any{},
			}),
		}},
		{name: "13a-no-fill-hook", app: func() *Application {
			return provApp(
				Component{Name: "plain", Type: "web", Properties: map[string]any{"image": "nginx", "env": map[string]any{"A": "1"}}},
				Component{Name: "unmatched", Type: "store", Properties: map[string]any{"size": "1Gi"}})
		}, caps: map[string]CapabilityBinding{"other": rend(map[string]any{"x": 1})}},
	}
}

func runProvenance(t *testing.T, s provScenario) (*Provenance, *provCapture) {
	t.Helper()
	c := newProvCapture()
	_, _, prov, err := provTransformer(c).TransformWithProvenance(s.app(), TransformContext{Capabilities: s.caps})
	if err != nil {
		t.Fatalf("%s: TransformWithProvenance: %v", s.name, err)
	}
	return prov, c
}

func marshalProvenance(t *testing.T, p *Provenance) []byte {
	t.Helper()
	b, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestProvenance_Golden compares each scenario's report with its golden file.
func TestProvenance_Golden(t *testing.T) {
	seen := map[string]string{}
	for _, s := range provenanceGoldenScenarios() {
		t.Run(s.name, func(t *testing.T) {
			prov, _ := runProvenance(t, s)
			got := marshalProvenance(t, prov)
			path := filepath.Join("testdata", "provenance", s.name+".yaml")
			if *updateProvenance {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to write it): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("report differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
			}
			// Negative control: no two scenarios share a report.
			if other, dup := seen[string(got)]; dup {
				t.Errorf("scenario %s reports exactly what %s does", s.name, other)
			}
			seen[string(got)] = s.name
		})
	}
}

func provenanceGoldenScenarios() []provScenario { return provScenarios() }

func entriesFor(p *Provenance, component, member, trait string) map[string]Source {
	out := map[string]Source{}
	for _, e := range p.Entries {
		if e.Component == component && e.Member == member && e.Trait == trait {
			out[e.Path] = e.Source
		}
	}
	return out
}

// TestProvenance_SynthesizedComponentSkipsFill: a component a checked rule
// synthesized takes no capability fill, so the report holds none for it (test 11).
func TestProvenance_SynthesizedComponentSkipsFill(t *testing.T) {
	c := newProvCapture()
	tr := provTransformer(c)
	tr.RegisterComponentLowering(storeLoweringRule{props: map[string]any{"size": "1Gi"}})
	app := provApp(Component{Name: "data", Type: "store-rule", Properties: map[string]any{}})
	caps := map[string]CapabilityBinding{"store": rend(map[string]any{"class": "platform"})}
	_, _, prov, err := tr.TransformWithProvenance(app, TransformContext{Capabilities: caps})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	for _, e := range prov.Entries {
		if e.Source.Kind == SourceCapability {
			t.Errorf("synthesized component carries a capability entry: %+v", e)
		}
	}
	if _, filled := c.components["data/store"]["class"]; filled {
		t.Errorf("the synthesized component was filled: %v", c.components["data/store"])
	}
}

// TestProvenance_SiblingGroup: two members of one same-name group, each holding the
// authored trait the rule forwarded to it, report that trait at index 0 under their
// own Member (test 12). A trait the rule hard-codes is sealed and is not reported by
// this change.
func TestProvenance_SiblingGroup(t *testing.T) {
	c := newProvCapture()
	tr := provTransformer(c)
	tr.RegisterComponentLowering(emitRule{"pair", func(c *Component) []Component {
		return []Component{
			{Name: c.Name, Type: "web", Properties: map[string]any{"image": "a"}, Traits: c.Traits},
			{Name: c.Name, Type: "store", Properties: map[string]any{"size": "1Gi"}, Traits: c.Traits},
		}
	}})
	caps := map[string]CapabilityBinding{"expose": rend(map[string]any{"proto": "tcp"})}
	app := siblingDoc(Component{Name: "grp", Type: "pair",
		Traits: []Trait{{Type: "expose", Properties: map[string]any{"port": 80}}}})
	_, _, prov, err := tr.TransformWithProvenance(app, TransformContext{Capabilities: caps})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	checkComplete(t, prov, c)
	for _, member := range []string{"web", "store"} {
		got := entriesFor(prov, "grp", member, "expose")
		if len(got) != 2 || got["port"].Kind != SourceAuthored || got["proto"] != (Source{Kind: SourceCapability, Ref: "expose"}) {
			t.Errorf("member %s trait entries = %+v, want port authored and proto capability", member, got)
		}
		for _, e := range prov.Entries {
			if e.Trait == "expose" && e.Member == member && e.TraitIndex != 0 {
				t.Errorf("member %s: trait index %d, want 0", member, e.TraitIndex)
			}
		}
	}
	if got := entriesFor(prov, "grp", "", "expose"); len(got) != 0 {
		t.Errorf("group trait entries under an empty Member: %+v", got)
	}
}

// TestProvenance_OutputEquality: TransformWithProvenance returns the cluster and
// PolicyResult TransformWithPolicy does (test 14).
func TestProvenance_OutputEquality(t *testing.T) {
	for _, s := range provScenarios() {
		t.Run(s.name, func(t *testing.T) {
			wantCluster, wantResult, err := provTransformer(newProvCapture()).TransformWithPolicy(s.app(), TransformContext{Capabilities: s.caps})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			gotCluster, gotResult, _, err := provTransformer(newProvCapture()).TransformWithProvenance(s.app(), TransformContext{Capabilities: s.caps})
			if err != nil {
				t.Fatalf("TransformWithProvenance: %v", err)
			}
			if !reflect.DeepEqual(wantResult, gotResult) {
				t.Errorf("PolicyResult differs:\n got %+v\nwant %+v", gotResult, wantResult)
			}
			if !reflect.DeepEqual(wantCluster, gotCluster) {
				t.Errorf("cluster differs from TransformWithPolicy's")
			}
		})
	}
}

// transformWithPolicyAllocs is testing.AllocsPerRun of TransformWithPolicy on
// provAllocApp, measured on origin/main before this change (test 15).
const transformWithPolicyAllocs = 80

func provAllocApp() *Application {
	return provApp(Component{Name: "web", Type: "web", Properties: map[string]any{"image": "nginx"},
		Traits: []Trait{{Type: "ingress", Properties: map[string]any{"tls": map[string]any{"enabled": false}}}}})
}

// TestProvenance_ZeroCostOff: the plain path allocates what it did before the
// recorder existed (test 15). The handlers are ones that exist on origin/main, so
// this same test body measured the baseline there.
func TestProvenance_ZeroCostOff(t *testing.T) {
	caps := map[string]CapabilityBinding{"ingress": rend(map[string]any{"tls": map[string]any{"enabled": true, "issuer": "x"}})}
	tr := NewTransformer(
		map[string]ComponentHandler{"web": &pipelineComponentHandler{typ: "web"}},
		map[string]TraitHandler{"ingress": &stubTraitHandler{typ: "ingress"}})
	app := provAllocApp()
	ctx := TransformContext{Capabilities: caps}
	got := testing.AllocsPerRun(50, func() {
		if _, _, err := tr.TransformWithPolicy(app, ctx); err != nil {
			t.Fatal(err)
		}
	})
	if got != transformWithPolicyAllocs {
		t.Errorf("TransformWithPolicy allocates %v per run, the baseline on origin/main is %v", got, float64(transformWithPolicyAllocs))
	}
}

// TestProvenance_Deterministic: the same call twice reports the same bytes (test 16).
func TestProvenance_Deterministic(t *testing.T) {
	for _, s := range provScenarios() {
		t.Run(s.name, func(t *testing.T) {
			first, _ := runProvenance(t, s)
			want := marshalProvenance(t, first)
			for range 10 {
				again, _ := runProvenance(t, s)
				if got := marshalProvenance(t, again); string(got) != string(want) {
					t.Fatalf("reports differ between runs:\n%s\n---\n%s", want, got)
				}
			}
		})
	}
}

// TestProvenance_NoMutation: the Application and the profile are as they were (test 17).
func TestProvenance_NoMutation(t *testing.T) {
	for _, s := range provScenarios() {
		t.Run(s.name, func(t *testing.T) {
			app := s.app()
			snapshot := func() string {
				a, err := yaml.Marshal(app.Spec)
				if err != nil {
					t.Fatal(err)
				}
				b, err := yaml.Marshal(s.caps)
				if err != nil {
					t.Fatal(err)
				}
				return string(a) + "\n--\n" + string(b)
			}
			before := snapshot()
			if _, _, _, err := provTransformer(newProvCapture()).TransformWithProvenance(app, TransformContext{Capabilities: s.caps}); err != nil {
				t.Fatal(err)
			}
			if after := snapshot(); after != before {
				t.Errorf("input changed:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// TestProvenance_MergeRuleCoupling feeds the merge rule's cases through
// mergeRenderedProperties with a recorder and checks each leaf's kind against the
// origin of the merged value (test 18). A change to the merge that the recorder
// does not follow breaks this.
func TestProvenance_MergeRuleCoupling(t *testing.T) {
	cases := map[string]struct{ rendered, authored map[string]any }{
		"scalar override":        {map[string]any{"a": 1, "b": 2}, map[string]any{"a": 9}},
		"null over rendered":     {map[string]any{"a": 1}, map[string]any{"a": nil}},
		"null under absent key":  {map[string]any{"a": 1}, map[string]any{"b": nil}},
		"nested override":        {map[string]any{"o": map[string]any{"x": 1, "y": 2}}, map[string]any{"o": map[string]any{"x": 9}}},
		"list replaces list":     {map[string]any{"l": []any{1, 2, 3}}, map[string]any{"l": []any{9}}},
		"object over scalar":     {map[string]any{"o": 1}, map[string]any{"o": map[string]any{"x": 1}}},
		"scalar over object":     {map[string]any{"o": map[string]any{"x": 1}}, map[string]any{"o": 7}},
		"empty object expansion": {map[string]any{"o": map[string]any{}}, map[string]any{"o": map[string]any{"e": true}}},
		"empty over empty":       {map[string]any{"o": map[string]any{}}, map[string]any{"o": map[string]any{}}},
		"empty over full":        {map[string]any{"o": map[string]any{"x": 1}}, map[string]any{"o": map[string]any{}}},
		"dotted key":             {map[string]any{"a.b": 1, "c": map[string]any{"d[0]": 2}}, map[string]any{"c": map[string]any{"d[0]": 3}}},
		"untouched rendering":    {map[string]any{"a": 1, "z": map[string]any{"q": []any{1, map[string]any{"r": 2}}}}, map[string]any{"a": 2}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rendLeaves := leafValues(copyRendering(tc.rendered))
			authLeaves := leafValues(tc.authored)
			o := &ownerRecorder{entries: map[string]Source{}}
			o.bind("k")
			rendered := copyRendering(tc.rendered)
			o.seedCapability(rendered)
			merged := mergeRenderedProperties(rendered, tc.authored, o, "")
			mergedLeaves := leafValues(merged)
			if got, want := sortedKeys(o.entries), sortedKeys(mergedLeaves); !slices.Equal(got, want) {
				t.Fatalf("reported paths %v, merged leaf paths %v", got, want)
			}
			for p, src := range o.entries {
				switch src.Kind {
				case SourceAuthored:
					if v, ok := authLeaves[p]; !ok || !reflect.DeepEqual(v, mergedLeaves[p]) {
						t.Errorf("%s reported authored, but the merged value %v is not the authored one %v", p, mergedLeaves[p], authLeaves[p])
					}
				case SourceCapability:
					if v, ok := rendLeaves[p]; !ok || !reflect.DeepEqual(v, mergedLeaves[p]) {
						t.Errorf("%s reported capability, but the merged value %v is not the rendered one %v", p, mergedLeaves[p], rendLeaves[p])
					}
					if src.Ref != "k" {
						t.Errorf("%s: Ref = %q, want k", p, src.Ref)
					}
				default:
					t.Errorf("%s: kind %q", p, src.Kind)
				}
			}
		})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestProvenance_Completeness: for every component and trait, the reported paths
// are exactly the non-null leaf paths of the properties it ended with (test 19).
// It runs over every fixture app: the scenarios, the sibling group and the
// lowering-rule outputs. The fixtures keep a trait type unique within a same-name
// group, so a trait is told apart by its component name and type alone.
func TestProvenance_Completeness(t *testing.T) {
	for _, s := range provScenarios() {
		t.Run(s.name, func(t *testing.T) {
			prov, c := runProvenance(t, s)
			checkComplete(t, prov, c)
		})
	}
}

// checkComplete holds prov to the final properties c captured.
func checkComplete(t *testing.T, prov *Provenance, c *provCapture) {
	t.Helper()
	if len(c.components) == 0 {
		t.Fatal("no component reached its handler: the check would pass vacuously")
	}
	grouped := map[string]bool{}
	for _, e := range prov.Entries {
		if e.Member != "" {
			grouped[e.Component] = true
		}
	}
	for key, props := range c.components {
		name, typ := splitKey(key)
		member := ""
		if grouped[name] {
			member = typ
		}
		got := sortedKeys(entriesFor(prov, name, member, ""))
		want := sortedKeys(leafValues(props))
		if !slices.Equal(got, want) {
			t.Errorf("component %s: reported %v, final leaves %v", key, got, want)
		}
	}
	for key, props := range c.traits {
		name, typ := splitKey(key)
		got := map[string]bool{}
		for _, e := range prov.Entries {
			if e.Component == name && e.Trait == typ {
				got[e.Path] = true
			}
		}
		want := sortedKeys(leafValues(props))
		if !slices.Equal(sortedKeys(got), want) {
			t.Errorf("trait %s: reported %v, final leaves %v", key, sortedKeys(got), want)
		}
	}
}

func splitKey(k string) (string, string) {
	for i := range k {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// provDocRule claims the kind "ProvDoc" and builds a terminal document whose one
// component is a "store" with the property size.
type provDocRule struct{}

func (provDocRule) Kind() string { return "ProvDoc" }
func (provDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{
			{Name: doc.Spec.Components[0].Name, Type: "store", Properties: map[string]any{"size": "1Gi"}},
		}},
	}}}, nil
}

// TestProvenance_LoweredElements: what a lowering rule emits is reported as
// lowered, with the rule's identity as Ref; a capability fill on an emitted
// component no rule synthesized stays capability (test 9).
func TestProvenance_LoweredElements(t *testing.T) {
	caps := map[string]CapabilityBinding{
		"store":  rend(map[string]any{"class": "platform"}),
		"expose": rend(map[string]any{"proto": "tcp"}),
	}
	cases := []struct {
		name      string
		register  func(*Transformer)
		app       func() *Application
		component string
		trait     string
		want      map[string]Source
	}{{
		name: "schema-less component rule",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(emitRule{"plain-rule", func(c *Component) []Component {
				return []Component{{Name: c.Name, Type: "store", Properties: map[string]any{"size": "1Gi"}}}
			}})
		},
		app:       func() *Application { return provApp(Component{Name: "data", Type: "plain-rule"}) },
		component: "data",
		want: map[string]Source{
			"size":  {Kind: SourceLowered, Ref: "component/plain-rule"},
			"class": {Kind: SourceCapability, Ref: "store"},
		},
	}, {
		name:      "document rule",
		register:  func(tr *Transformer) { tr.RegisterDocumentLowering(provDocRule{}) },
		app:       func() *Application { return singleComponentApp("ProvDoc", "web", nil) },
		component: "web",
		want: map[string]Source{
			"size":  {Kind: SourceLowered, Ref: "document/ProvDoc"},
			"class": {Kind: SourceCapability, Ref: "store"},
		},
	}, {
		name: "synthesized component",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(storeLoweringRule{props: map[string]any{"size": "1Gi"}})
		},
		app:       func() *Application { return provApp(Component{Name: "data", Type: "store-rule"}) },
		component: "data",
		want:      map[string]Source{"size": {Kind: SourceLowered, Ref: "component/store-rule"}},
	}, {
		name: "trait the rule hard-codes",
		register: func(tr *Transformer) {
			tr.RegisterComponentLowering(emitRule{"traited", func(c *Component) []Component {
				return []Component{{Name: c.Name, Type: "web", Properties: map[string]any{"image": "a"},
					Traits: []Trait{{Type: "expose", Properties: map[string]any{"port": 80}}}}}
			}})
		},
		app:       func() *Application { return provApp(Component{Name: "web", Type: "traited"}) },
		component: "web",
		trait:     "expose",
		want:      map[string]Source{"port": {Kind: SourceLowered, Ref: "component/traited"}},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newProvCapture()
			tr := provTransformer(c)
			tc.register(tr)
			_, _, prov, err := tr.TransformWithProvenance(tc.app(), TransformContext{Capabilities: caps})
			if err != nil {
				t.Fatalf("TransformWithProvenance: %v", err)
			}
			checkComplete(t, prov, c)
			got := entriesFor(prov, tc.component, "", tc.trait)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("entries = %+v, want %+v", got, tc.want)
			}
		})
	}
}
