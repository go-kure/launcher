package model

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// evalBase is celBase with the inputs the evaluation tests need: a number, a boolean, a list of
// integers and a list of numbers.
func evalBase(t *testing.T) *Model {
	t.Helper()
	m := celBase(t)
	m.Spec.Inputs["ratio"] = oam.PropertySchema{Type: oam.PropertyTypeNumber}
	m.Spec.Inputs["flag"] = oam.PropertySchema{Type: oam.PropertyTypeBoolean}
	m.Spec.Inputs["sizes"] = oam.PropertySchema{Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeInteger}}
	m.Spec.Inputs["weights"] = oam.PropertySchema{Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeNumber}}
	if err := validate(m); err != nil {
		t.Fatalf("test bug: the base model does not load: %v", err)
	}
	return m
}

// propModel returns a model whose first part holds the template in the property p.
func propModel(t *testing.T, tmpl string) *Model {
	t.Helper()
	m := evalBase(t)
	m.Spec.Parts[0].Properties = map[string]any{"p": tmpl}
	return m
}

const propSite = "spec.parts[0].properties.p"

// stdScope is a scope that fills every input of evalBase except the optional ones.
func stdScope() Scope {
	return Scope{
		Inputs: map[string]any{
			"image":    "nginx",
			"replicas": int64(3),
			"tags":     []any{"a", "b"},
			"labels":   map[string]any{"k": "v"},
		},
		Self: Self{Name: "web", Application: "shop"},
	}
}

func mustCompile(t *testing.T, m *Model) *Compiled {
	t.Helper()
	c, err := Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if c == nil {
		t.Fatal("Compile returned no model and no error")
	}
	return c
}

func mustEval(t *testing.T, c *Compiled, site string, sc Scope) (any, int64) {
	t.Helper()
	got, cost, err := c.Eval(site, sc)
	if err != nil {
		t.Fatalf("Eval(%s): %v", site, err)
	}
	return got, cost
}

// wantCompileRefusal compiles m and requires a failure that wraps ErrInvalid, names the path and
// names the cause in the diagnostic text (never in the echoed source).
func wantCompileRefusal(t *testing.T, m *Model, path, cause string) {
	t.Helper()
	c, err := Compile(m)
	if err == nil {
		t.Fatalf("Compile accepted the model: %+v", c)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("a compile failure must wrap ErrInvalid: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}
	if !strings.Contains(diagnosticText(err), cause) {
		t.Errorf("error %q does not name the cause %q", err, cause)
	}
}

// The walker: what a template gives. A single hole keeps the CEL type, text around holes makes a
// string, `$${` writes a literal `${`.
func TestTemplateWalker(t *testing.T) {
	rows := []struct {
		name string
		tmpl string
		want any
	}{
		{"a single hole keeps an int", "${inputs.replicas}", int64(3)},
		{"a single hole keeps a list", "${inputs.tags}", []any{"a", "b"}},
		{"a single hole keeps a string", "${inputs.image}", "nginx"},
		{"text around a hole makes a string", "r-${inputs.image}-x", "r-nginx-x"},
		{"two holes make a string", "${inputs.image}:${inputs.replicas}", "nginx:3"},
		{"a plain string stays", "plain", "plain"},
		{"an escape writes a literal open", "$${inputs.image}", "${inputs.image}"},
		{"an escape then a hole", "$${a} ${inputs.image}", "${a} nginx"},
		{"an empty escaped pair", "$${}", "${}"},
		{"a lone dollar stays", "cost $5", "cost $5"},
		{"a double dollar without an open stays", "a$$b", "a$$b"},
		{"a close brace in a string literal", "${'}'}", "}"},
		{"a close brace in a string literal in text", "a${'}'}b", "a}b"},
		{"a map literal nests", "x${size({'k': 1})}y", "x1y"},
		{"a double quoted brace", `a${"}"}b`, "a}b"},
		{"a hole gives an int in text", "n=${inputs.replicas + 1}", "n=4"},
		{"self in text", "${self.application}-${self.name}", "shop-web"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			c := mustCompile(t, propModel(t, tc.tmpl))
			got, _ := mustEval(t, c, propSite, stdScope())
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("template %q gave %#v, want %#v", tc.tmpl, got, tc.want)
			}
		})
	}
}

// Refusals of the walker. Each names the site path and the cause.
func TestTemplateRefusals(t *testing.T) {
	rows := []struct {
		name  string
		tmpl  string
		cause string
	}{
		{"an unclosed hole", "a${inputs.image", "not closed"},
		{"an unclosed hole after text", "${inputs.image} and ${inputs.tags", "not closed"},
		{"an empty hole", "a${}b", "is empty"},
		{"a list in text", "x${inputs.tags}", "not allowed in text"},
		{"a map in text", "x${inputs.labels}", "not allowed in text"},
		{"a dyn value in text", "x${inputs.config.items}", "not allowed in text"},
		{"an unknown input", "x${inputs.nope}", "undeclared reference"},
		{"a wrong type", "x${inputs.image + 1}", "no matching overload"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			wantCompileRefusal(t, propModel(t, tc.tmpl), propSite, tc.cause)
		})
	}
	t.Run("the control compiles", func(t *testing.T) {
		mustCompile(t, propModel(t, "x${inputs.image}"))
	})
}

// Every site the walker reads has a path, and a refusal names it. The control is the same model
// with a good template in every site.
func TestSitePaths(t *testing.T) {
	good := "ok-${inputs.image}"
	bad := "bad-${inputs.nope}"
	build := func(t *testing.T, tmpl string, set func(p *Part, tmpl string)) *Model {
		m := evalBase(t)
		set(&m.Spec.Parts[0], tmpl)
		return m
	}
	rows := []struct {
		name string
		path string
		set  func(p *Part, tmpl string)
	}{
		{"name", "spec.parts[0].name", func(p *Part, s string) { p.Name = s }},
		{"property", "spec.parts[0].properties.q", func(p *Part, s string) { p.Properties = map[string]any{"q": s} }},
		{"nested property", "spec.parts[0].properties.q.r", func(p *Part, s string) {
			p.Properties = map[string]any{"q": map[string]any{"r": s}}
		}},
		{"list property", "spec.parts[0].properties.q[1]", func(p *Part, s string) {
			p.Properties = map[string]any{"q": []any{"fixed", s}}
		}},
		{"trait property", "spec.parts[0].traits[0].properties.q", func(p *Part, s string) {
			p.Traits = []PartTrait{{Type: "expose.launcher.gokure.dev@v1", Properties: map[string]any{"q": s}}}
		}},
		{"annotation", "spec.parts[0].annotations.q", func(p *Part, s string) { p.Annotations = map[string]string{"q": s} }},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			c := mustCompile(t, build(t, good, tc.set))
			got, _ := mustEval(t, c, tc.path, stdScope())
			if got != "ok-nginx" {
				t.Errorf("site %s gave %#v, want ok-nginx", tc.path, got)
			}
			wantCompileRefusal(t, build(t, bad, tc.set), tc.path, "undeclared reference")
		})
	}
	t.Run("an unknown site is an error", func(t *testing.T) {
		c := mustCompile(t, build(t, good, rows[0].set))
		if _, _, err := c.Eval("spec.parts[0].properties.nope", stdScope()); err == nil {
			t.Error("Eval of a site that does not exist returned no error")
		}
	})
}

// A map key is never a template (ruling CL-2633, no silent fallback). A "${" in a key of the
// properties, the trait properties or the annotations is refused at compile with the path of the
// key named. An escape "$${" in a key is refused too: a key is not text that is read for holes,
// so neither form may pass as a literal. The control is the same model with a plain key.
func TestTemplateInMapKeyIsRefused(t *testing.T) {
	keyed := func(key string) []struct {
		name, path string
		model      *Model
	} {
		mk := func(set func(p *Part)) *Model {
			m := evalBase(t)
			set(&m.Spec.Parts[0])
			return m
		}
		return []struct {
			name, path string
			model      *Model
		}{
			{"a property key", "spec.parts[0].properties." + key, mk(func(p *Part) { p.Properties = map[string]any{key: "v"} })},
			{"a nested property key", "spec.parts[0].properties.q." + key, mk(func(p *Part) {
				p.Properties = map[string]any{"q": map[string]any{key: "v"}}
			})},
			{"a key in a list of maps", "spec.parts[0].properties.q[0]." + key, mk(func(p *Part) {
				p.Properties = map[string]any{"q": []any{map[string]any{key: "v"}}}
			})},
			{"a trait property key", "spec.parts[0].traits[0].properties." + key, mk(func(p *Part) {
				p.Traits = []PartTrait{{Type: "expose.launcher.gokure.dev@v1", Properties: map[string]any{key: "v"}}}
			})},
			{"an annotation key", "spec.parts[0].annotations." + key, mk(func(p *Part) { p.Annotations = map[string]string{key: "v"} })},
		}
	}
	for _, key := range []string{"a${inputs.image}", "$${literal}"} {
		for _, tc := range keyed(key) {
			t.Run(tc.name+" "+key, func(t *testing.T) {
				wantCompileRefusal(t, tc.model, tc.path, "in a map key")
			})
		}
	}
	for _, tc := range keyed("plain") {
		t.Run("control "+tc.name+" compiles", func(t *testing.T) {
			mustCompile(t, tc.model)
		})
	}
}

// Document order across the template sites: values, then per part when, forEach.in, name,
// properties (keys sorted), traits, annotations. A name comes after forEach.in because it can read
// the loop variable. The first failure wins.
func TestCompileFirstFailureOrder(t *testing.T) {
	bad := "${inputs.nope}"
	t.Run("a bad name comes before a bad property", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Parts[0].Name = bad
		m.Spec.Parts[0].Properties = map[string]any{"a": bad}
		wantCompileRefusal(t, m, "spec.parts[0].name", "undeclared reference")
	})
	t.Run("a bad when comes before a bad property", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Parts[0].When = "${inputs.nope}"
		m.Spec.Parts[0].Properties = map[string]any{"a": bad}
		wantCompileRefusal(t, m, "spec.parts[0].when", "undeclared reference")
	})
	t.Run("property keys are read in sorted order", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Parts[0].Properties = map[string]any{"b": bad, "a": bad}
		wantCompileRefusal(t, m, "spec.parts[0].properties.a", "undeclared reference")
	})
	t.Run("a bad property comes before a bad trait", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Parts[0].Properties = map[string]any{"a": bad}
		m.Spec.Parts[0].Traits = []PartTrait{{Type: "expose.launcher.gokure.dev@v1", Properties: map[string]any{"a": bad}}}
		wantCompileRefusal(t, m, "spec.parts[0].properties.a", "undeclared reference")
	})
	t.Run("a bad trait comes before a bad annotation", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Parts[0].Traits = []PartTrait{{Type: "expose.launcher.gokure.dev@v1", Properties: map[string]any{"a": bad}}}
		m.Spec.Parts[0].Annotations = map[string]string{"a": bad}
		wantCompileRefusal(t, m, "spec.parts[0].traits[0].properties.a", "undeclared reference")
	})
	t.Run("a bad value comes before a bad part", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Values = []Value{{Name: "v", Expr: bad}}
		m.Spec.Parts[0].Name = bad
		wantCompileRefusal(t, m, "spec.values[0].expr", "undeclared reference")
	})
}

// Null in a template. A single hole keeps the null. Text around the hole has no text form for it,
// so the evaluation fails with a named error and never writes "null" or an empty string.
func TestNullInTemplates(t *testing.T) {
	// The value stays null in the emitted properties and no key is dropped (ruling CL-2633). The
	// assembly of the properties map is step 3. This test pins its half: the site is known, and
	// Eval gives a null value with no error, so the key is not skipped as an unknown site and not
	// refused as a missing value.
	t.Run("a single hole keeps null", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "${inputs.nick}"))
		sc := stdScope()
		sc.Inputs["nick"] = nil
		got, _, err := c.Eval(propSite, sc)
		if err != nil {
			t.Fatalf("a single hole of a null input failed: %v", err)
		}
		if got != nil {
			t.Errorf("a single hole of a null input gave %#v, want nil", got)
		}
	})
	t.Run("null in mixed text is refused", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "hi ${inputs.nick}"))
		sc := stdScope()
		sc.Inputs["nick"] = nil
		_, _, err := c.Eval(propSite, sc)
		if err == nil {
			t.Fatal("null in mixed text gave a value")
		}
		if !strings.Contains(err.Error(), propSite) || !strings.Contains(err.Error(), "null") {
			t.Errorf("error %q must name the site %q and the word null", err, propSite)
		}
	})
	t.Run("an absent key is null", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "hi ${inputs.nick}"))
		_, _, err := c.Eval(propSite, stdScope())
		if err == nil || !strings.Contains(err.Error(), "null") {
			t.Errorf("an absent optional input in text gave %v, want a null error", err)
		}
	})
	t.Run("the control: a given value is written", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "hi ${inputs.nick}"))
		sc := stdScope()
		sc.Inputs["nick"] = "bob"
		got, _ := mustEval(t, c, propSite, sc)
		if got != "hi bob" {
			t.Errorf("gave %#v, want \"hi bob\"", got)
		}
	})
}

// The loop variable is typed from the element type of forEach.in, and name, properties, traits and
// annotations of that part see it. `when` and `forEach.in` do not.
func TestLoopVariable(t *testing.T) {
	forEachModel := func(t *testing.T, in, prop string) *Model {
		m := evalBase(t)
		m.Spec.Parts[0].ForEach = &ForEach{As: "n", In: in}
		m.Spec.Parts[0].Properties = map[string]any{"p": prop}
		return m
	}
	t.Run("an int element", func(t *testing.T) {
		c := mustCompile(t, forEachModel(t, "${inputs.sizes}", "${n + 1}"))
		sc := stdScope()
		sc.Loop = int64(4)
		got, _ := mustEval(t, c, propSite, sc)
		if got != int64(5) {
			t.Errorf("gave %#v, want int64(5)", got)
		}
	})
	t.Run("a double element", func(t *testing.T) {
		c := mustCompile(t, forEachModel(t, "${inputs.weights}", "${n + 1.0}"))
		sc := stdScope()
		sc.Loop = 1.5
		got, _ := mustEval(t, c, propSite, sc)
		if got != 2.5 {
			t.Errorf("gave %#v, want 2.5", got)
		}
	})
	t.Run("a double element refuses an int sum", func(t *testing.T) {
		wantCompileRefusal(t, forEachModel(t, "${inputs.weights}", "${n + 1}"), propSite, "no matching overload")
	})
	t.Run("the loop variable in the name and an annotation", func(t *testing.T) {
		m := forEachModel(t, "${inputs.tags}", "x")
		m.Spec.Parts[0].Name = "web-${n}"
		m.Spec.Parts[0].Annotations = map[string]string{"k": "${n}"}
		c := mustCompile(t, m)
		sc := stdScope()
		sc.Loop = "a"
		if got, _ := mustEval(t, c, "spec.parts[0].name", sc); got != "web-a" {
			t.Errorf("name gave %#v, want web-a", got)
		}
		if got, _ := mustEval(t, c, "spec.parts[0].annotations.k", sc); got != "a" {
			t.Errorf("annotation gave %#v, want a", got)
		}
	})
	t.Run("a part without forEach does not see the name", func(t *testing.T) {
		wantCompileRefusal(t, propModel(t, "${n}"), propSite, "undeclared reference")
	})
	t.Run("when does not see the loop variable", func(t *testing.T) {
		m := forEachModel(t, "${inputs.sizes}", "x")
		m.Spec.Parts[0].When = "${n > 1}"
		wantCompileRefusal(t, m, "spec.parts[0].when", "undeclared reference")
	})
	t.Run("forEach.in does not see the loop variable", func(t *testing.T) {
		wantCompileRefusal(t, forEachModel(t, "${[n]}", "x"), "spec.parts[0].forEach.in", "undeclared reference")
	})
}

// The program cache key holds the part path. Two parts hold one text, one over an int list and
// one over a double list, and both build and run. A key from the text alone gives the second part
// the program of the first.
func TestProgramCachePartPath(t *testing.T) {
	m := evalBase(t)
	m.Spec.Parts = []Part{
		{ID: "ints", Type: "deployment.apps@v1", ForEach: &ForEach{As: "n", In: "${inputs.sizes}"}, Properties: map[string]any{"p": "${string(n)}"}},
		{ID: "dbls", Type: "deployment.apps@v1", ForEach: &ForEach{As: "n", In: "${inputs.weights}"}, Properties: map[string]any{"p": "${string(n)}"}},
	}
	if err := validate(m); err != nil {
		t.Fatalf("test bug: %v", err)
	}
	c := mustCompile(t, m)
	sc := stdScope()
	sc.Loop = int64(7)
	if got, _ := mustEval(t, c, "spec.parts[0].properties.p", sc); got != "7" {
		t.Errorf("int part gave %#v, want \"7\"", got)
	}
	sc.Loop = 1.5
	if got, _ := mustEval(t, c, "spec.parts[1].properties.p", sc); got != "1.5" {
		t.Errorf("double part gave %#v, want \"1.5\"", got)
	}
}

// The cache key. The same text at the same path in two models of different identity is two
// entries, and a second compile of one model adds none. The programs differ in type, so a key
// without the model identity would run one on the other's data.
func TestProgramCacheKey(t *testing.T) {
	mk := func(t *testing.T, name string, in oam.PropertySchema) *Model {
		m := celBase(t)
		m.Metadata.Name = name
		m.Spec.Inputs = map[string]oam.PropertySchema{"v": in}
		m.Spec.Values = []Value{{Name: "w", Expr: "${inputs.v + inputs.v}"}}
		m.Spec.Parts[0].Properties = map[string]any{"p": "${inputs.v + inputs.v}"}
		if err := validate(m); err != nil {
			t.Fatalf("test bug: %v", err)
		}
		return m
	}
	a := mk(t, "alpha.acme.example", oam.PropertySchema{Type: oam.PropertyTypeString, Required: true})
	b := mk(t, "beta.acme.example", oam.PropertySchema{Type: oam.PropertyTypeInteger, Required: true})

	cache := NewCache()
	ca, err := cache.Compile(a)
	if err != nil {
		t.Fatalf("Compile(a): %v", err)
	}
	if got := cache.Len(); got != 2 {
		t.Fatalf("after one model with two sites the cache holds %d programs, want 2 (same text at two paths)", got)
	}
	cb, err := cache.Compile(b)
	if err != nil {
		t.Fatalf("Compile(b): %v", err)
	}
	if got := cache.Len(); got != 4 {
		t.Errorf("after the second model the cache holds %d programs, want 4 (same path and text, another identity)", got)
	}
	if _, err := cache.Compile(a); err != nil {
		t.Fatalf("Compile(a) again: %v", err)
	}
	if got := cache.Len(); got != 4 {
		t.Errorf("a second compile of one model changed the cache to %d programs, want 4", got)
	}
	if got, _ := mustEval(t, ca, "spec.values[0].expr", Scope{Inputs: map[string]any{"v": "x"}}); got != "xx" {
		t.Errorf("string model gave %#v, want \"xx\"", got)
	}
	if got, _ := mustEval(t, cb, "spec.values[0].expr", Scope{Inputs: map[string]any{"v": int64(7)}}); got != int64(14) {
		t.Errorf("int model gave %#v, want int64(14)", got)
	}
}

// Two goroutines share one compiled model and one cache.
func TestConcurrentEval(t *testing.T) {
	m := propModel(t, "${inputs.image}:${inputs.replicas}")
	cache := NewCache()
	c, err := cache.Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				got, _, err := c.Eval(propSite, stdScope())
				if err != nil || got != "nginx:3" {
					errs <- "eval gave " + stringOf(got) + " and " + stringOf(err)
					return
				}
				if _, err := cache.Compile(m); err != nil {
					errs <- "compile: " + err.Error()
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func stringOf(v any) string {
	if v == nil {
		return "<nil>"
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	if s, ok := v.(string); ok {
		return s
	}
	return "<other>"
}

// What a single hole returns, as Go values: int stays int64, a double is a float64, a list is
// ordered, a map keeps its keys, null is nil. A map with a key that is not a string has no place
// in a document and is refused when the model compiles.
func TestResultConversion(t *testing.T) {
	rows := []struct {
		name string
		expr string
		want any
	}{
		{"an int", "${40 + 2}", int64(42)},
		{"a double", "${1.5}", 1.5},
		{"a bool", "${true}", true},
		{"a string", "${'s'}", "s"},
		{"an ordered list", "${[3, 1, 2]}", []any{int64(3), int64(1), int64(2)}},
		{"a map", "${{'b': 1, 'a': 2}}", map[string]any{"a": int64(2), "b": int64(1)}},
		{"a nested value", "${[{'k': [1.5, 'x']}]}", []any{map[string]any{"k": []any{1.5, "x"}}}},
		{"a null", "${null}", nil},
		{"an empty list", "${[]}", []any{}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			c := mustCompile(t, propModel(t, tc.expr))
			got, _ := mustEval(t, c, propSite, stdScope())
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s gave %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
	t.Run("a map key that is not a string is refused", func(t *testing.T) {
		wantCompileRefusal(t, propModel(t, "${{1: 'a'}}"), propSite, "map keys must be strings")
	})
	// The same rule holds at the strict sites, because Eval returns Go data for them too: a value goes
	// back in Scope.Values and a forEach.in list gives the loop variable.
	t.Run("a value with a map key that is not a string is refused", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Values = []Value{{Name: "m", Expr: "${{1: 'a'}}"}}
		wantCompileRefusal(t, m, "spec.values[0].expr", "map keys must be strings")
	})
	t.Run("a forEach.in list of maps with a key that is not a string is refused", func(t *testing.T) {
		m := evalBase(t)
		m.Spec.Parts[0].ForEach = &ForEach{As: "n", In: "${[{1: 'a'}]}"}
		wantCompileRefusal(t, m, "spec.parts[0].forEach.in", "map keys must be strings")
	})
	// JSON cannot encode NaN or an infinity, so such a double is refused at the site and not later in
	// the writer. The error wraps ErrNonFinite and names the site. The same holds inside a list and
	// for an input value.
	nonFinite := []struct{ name, expr string }{
		{"not a number", "${0.0 / 0.0}"},
		{"positive infinity", "${1.0 / 0.0}"},
		{"negative infinity", "${-1.0 / 0.0}"},
		{"infinity inside a list", "${[1.5, 1.0 / 0.0]}"},
		{"not a number inside a map", "${{'k': 0.0 / 0.0}}"},
	}
	for _, tc := range nonFinite {
		t.Run("refused "+tc.name, func(t *testing.T) {
			wantNonFiniteRefusal(t, mustCompile(t, propModel(t, tc.expr)), stdScope())
		})
	}
	for _, tc := range []struct {
		name string
		in   float64
	}{{"input NaN", math.NaN()}, {"input +Inf", math.Inf(1)}, {"input -Inf", math.Inf(-1)}} {
		t.Run("refused "+tc.name, func(t *testing.T) {
			sc := stdScope()
			sc.Inputs["ratio"] = tc.in
			wantNonFiniteRefusal(t, mustCompile(t, propModel(t, "${inputs.ratio}")), sc)
		})
	}
	t.Run("a finite double is not refused", func(t *testing.T) {
		sc := stdScope()
		sc.Inputs["ratio"] = 1e300
		got, _ := mustEval(t, mustCompile(t, propModel(t, "${inputs.ratio}")), propSite, sc)
		if got != 1e300 {
			t.Errorf("gave %#v, want float64(1e300)", got)
		}
	})
}

// wantNonFiniteRefusal evaluates the property site and requires a failure that wraps ErrNonFinite
// and names the site. The failure is a refusal of the result and not a cost or other failure.
func wantNonFiniteRefusal(t *testing.T, c *Compiled, sc Scope) {
	t.Helper()
	got, _, err := c.Eval(propSite, sc)
	if err == nil {
		t.Fatalf("Eval returned %#v with no error", got)
	}
	if !errors.Is(err, ErrNonFinite) {
		t.Errorf("error %q must wrap ErrNonFinite", err)
	}
	if errors.Is(err, ErrCostLimit) {
		t.Errorf("error %q must not be a cost failure", err)
	}
	if !strings.Contains(err.Error(), propSite) {
		t.Errorf("error %q must name the site %q", err, propSite)
	}
}

// A parse error for the optimizer macro. cel.@block is the form the cel-go optimizer writes. No
// model may use it.
func TestBlockMacroIsRefused(t *testing.T) {
	wantCompileRefusal(t, propModel(t, "${cel.@block([1], @index0)}"), propSite, "Syntax error")
}

// One expression has a cost limit of 1,000,000. The hostile case is a nested comprehension of
// about 9,000,000 steps. The control is a cheap expression that must run and report a cost.
func TestCostLimit(t *testing.T) {
	t.Run("a cheap expression reports its cost", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "${lists.range(10).map(a, a).size()}"))
		got, cost := mustEval(t, c, propSite, stdScope())
		if got != int64(10) {
			t.Errorf("gave %#v, want int64(10)", got)
		}
		if cost <= 0 || cost > 1000000 {
			t.Errorf("cost %d, want between 1 and 1000000", cost)
		}
	})
	t.Run("an expensive expression fails and names the limit", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "${lists.range(3000).map(a, lists.range(3000).map(b, a + b)).size()}"))
		_, _, err := c.Eval(propSite, stdScope())
		if err == nil {
			t.Fatal("an expression of millions of steps ran to the end")
		}
		if !errors.Is(err, ErrCostLimit) {
			t.Errorf("error %q must wrap ErrCostLimit", err)
		}
		if !strings.Contains(err.Error(), "1000000") || !strings.Contains(err.Error(), propSite) {
			t.Errorf("error %q must name the limit 1000000 and the site %q", err, propSite)
		}
	})
}

// TestReplaceGrowth decides whether cost alone bounds the size of a result. The call result
// limit is 1,000,000 bytes. Each hostile row builds a result of several megabytes with replace or
// join, or a list of more than 1,000,000 elements with lists.range. A row passes when the
// evaluation stops with the stop that row names, or the result is within the limit. replace and
// join must stop with ErrCostLimit. lists.range(1000001) must stop with the exact refusal of
// cel-go, which has its own cap of 1,000,000 elements: any other error fails the row, and so does a
// cel-go that lifts the cap. A row that fails shows that nothing bounds it, and then the size check
// moves to its own overloads in another pull request. format is not tested here: cost does not
// bound its result, and its row comes with those overloads. The control rows show that the
// evaluation works at all, so a pass of a hostile row on a stub is not read as evidence.
func TestReplaceGrowth(t *testing.T) {
	const ten = "'xxxxxxxxxx'"
	base := "lists.range(1000).map(i, " + ten + ").join('')" // 10,000 bytes
	grow := ".replace('x', 'xxxxxxxxxx')"                    // times 10
	t.Run("control a growth within the limit", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "${size("+base+grow+")}"))
		got, _ := mustEval(t, c, propSite, stdScope())
		if got != int64(100000) {
			t.Errorf("gave %#v, want int64(100000)", got)
		}
	})
	t.Run("control a result is a string", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "${"+base+"}"))
		got, _ := mustEval(t, c, propSite, stdScope())
		if s, ok := got.(string); !ok || len(s) != 10000 {
			t.Errorf("gave %T of %d bytes, want a string of 10000", got, len(stringOf(got)))
		}
	})
	// Controls of the forms the hostile rows use, at a size far below the limit: a hostile row that
	// fails to evaluate for a mistake in its form would otherwise pass as "bounded".
	controls := []struct {
		name, expr string
		want       any
	}{
		{"control join with a bound string", "cel.bind(b, 'ab', lists.range(3).map(i, b).join('-'))", "ab-ab-ab"},
		{"control format with a bound string", "cel.bind(b, 'ab', '%s%s'.format([b, b]))", "abab"},
		{"control lists.range size", "lists.range(1000).size()", int64(1000)},
	}
	for _, tc := range controls {
		t.Run(tc.name, func(t *testing.T) {
			c := mustCompile(t, propModel(t, "${"+tc.expr+"}"))
			got, _ := mustEval(t, c, propSite, stdScope())
			if got != tc.want {
				t.Errorf("gave %#v, want %#v", got, tc.want)
			}
		})
	}
	costStop := func(err error) bool { return errors.Is(err, ErrCostLimit) }
	// The text is the refusal of cel-go v0.31.0 for a count of 1,000,001.
	rangeCap := func(err error) bool {
		return strings.Contains(err.Error(), "lists.range: size 1000001 exceeds maximum allowed (1000000)")
	}
	hostile := []struct {
		name, expr string
		stops      func(error) bool
		mustStop   bool // the README states the stop, so a result without it fails the row
	}{
		{"hostile replace three times", base + grow + grow + grow, costStop, false},
		// The bound string is built once, so the cost of this row is the join call and not the build of
		// its operand.
		{"hostile join", "cel.bind(b, " + base + ", lists.range(1000).map(i, b).join(''))", costStop, false},
		// The smallest count that crosses a limit of 1,000,000 elements. If nothing bounds it, the row
		// allocates one list of 1,000,001 values: about 24 bytes each, so about 24 MiB, and up to
		// twice that while the list grows.
		{"hostile lists.range", "lists.range(1000001).size()", rangeCap, false},
		// The largest count below that cap. The README states that the cost limit stops it.
		{"hostile lists.range at the cap", "lists.range(1000000).size()", costStop, true},
	}
	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Compile(propModel(t, "${"+tc.expr+"}"))
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			got, cost, err := c.Eval(propSite, stdScope())
			t.Logf("outcome of %s: result %#v, cost %d, error %v", tc.name, got, cost, err)
			if err != nil {
				// A bounded row stops with the stop it names. A failure with another cause shows a
				// mistake in the row and proves nothing about the bound.
				if !tc.stops(err) {
					t.Errorf("the row failed for a cause other than its stop: %v", err)
				}
				return
			}
			if tc.mustStop {
				t.Fatalf("the row ran to the end with %#v at cost %d, and the README states that the cost limit stops it", got, cost)
			}
			switch v := got.(type) {
			case string:
				if len(v) > 1000000 {
					t.Errorf("a result of %d bytes ran to the end: cost does not bound it", len(v))
				}
			case int64:
				if v > 1000000 {
					t.Errorf("a list of %d elements was built and measured: cost does not bound it", v)
				}
			default:
				t.Errorf("the row gave %T, want a string or an int", got)
			}
		})
	}
}

// TestTextForms pins the text that a double or a bool takes inside mixed text. The form is the
// one CEL gives for string(x), read on cel-go v0.31.0 by a probe (1 for 1.0, 1e+08 for
// 100000000.0). A whole double has no ".0". The README states this form. A single hole keeps the
// double and is not text. NaN and the infinities have no text: they are refused. A change of cel-go that moves a row here moves the text
// of every model, so it fails this test first.
func TestTextForms(t *testing.T) {
	doubles := []struct {
		name string
		in   float64
		want string
	}{
		{"a whole double has no fraction", 1.0, "v=1"},
		{"a short fraction", 0.1, "v=0.1"},
		{"negative zero", math.Copysign(0, -1), "v=-0"},
		{"1e21", 1e21, "v=1e+21"},
		{"1e20", 1e20, "v=1e+20"},
		{"a small exponent has two digits", 1e-7, "v=1e-07"},
		{"1e8", 100000000.0, "v=1e+08"},
		{"a long mantissa", 123456789012345678.0, "v=1.2345678901234568e+17"},
		{"the shortest text that reads back", 0.30000000000000004, "v=0.30000000000000004"},
	}
	c := mustCompile(t, propModel(t, "v=${inputs.ratio}"))
	// CEL would print NaN, +Inf and -Inf here. A document cannot hold them as numbers, so a model
	// never gets that text from a double: the evaluation is refused, as for a single hole.
	for _, tc := range []struct {
		name string
		in   float64
	}{{"not a number", math.NaN()}, {"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)}} {
		t.Run("refused "+tc.name, func(t *testing.T) {
			sc := stdScope()
			sc.Inputs["ratio"] = tc.in
			wantNonFiniteRefusal(t, c, sc)
		})
	}
	for _, tc := range doubles {
		t.Run("double "+tc.name, func(t *testing.T) {
			sc := stdScope()
			sc.Inputs["ratio"] = tc.in
			got, _ := mustEval(t, c, propSite, sc)
			if got != tc.want {
				t.Errorf("a double %v in text gave %#v, want %q", tc.in, got, tc.want)
			}
		})
	}
	cb := mustCompile(t, propModel(t, "v=${inputs.flag}"))
	for _, tc := range []struct {
		in   bool
		want string
	}{{true, "v=true"}, {false, "v=false"}} {
		t.Run("bool "+tc.want, func(t *testing.T) {
			sc := stdScope()
			sc.Inputs["flag"] = tc.in
			got, _ := mustEval(t, cb, propSite, sc)
			if got != tc.want {
				t.Errorf("a bool %v in text gave %#v, want %q", tc.in, got, tc.want)
			}
		})
	}
	t.Run("a single hole keeps the double", func(t *testing.T) {
		cs := mustCompile(t, propModel(t, "${inputs.ratio}"))
		sc := stdScope()
		sc.Inputs["ratio"] = 1.0
		got, _ := mustEval(t, cs, propSite, sc)
		if got != 1.0 {
			t.Errorf("a single hole gave %#v, want float64(1)", got)
		}
	})
	// The boundary: a double is refused, but string(x) is the author's own conversion and gives text.
	t.Run("an explicit string() of an infinity is text and is not refused", func(t *testing.T) {
		cx := mustCompile(t, propModel(t, "v=${string(1.0 / 0.0)}"))
		got, _ := mustEval(t, cx, propSite, stdScope())
		if got != "v=+Inf" {
			t.Errorf("gave %#v, want \"v=+Inf\"", got)
		}
	})
	t.Run("the same input gives the same text twice", func(t *testing.T) {
		sc := stdScope()
		sc.Inputs["ratio"] = 0.1
		first, _ := mustEval(t, c, propSite, sc)
		second, _ := mustEval(t, c, propSite, sc)
		if first != second {
			t.Errorf("two evaluations gave %#v and %#v", first, second)
		}
	})
}

// TestNonStringPlus: "+" does not join text and a number or a bool. The checker refuses it, and
// the model must say string(x). The control shows the way that works.
func TestNonStringPlus(t *testing.T) {
	rows := []struct{ name, expr string }{
		{"a double", "${'a' + 1.0}"},
		{"a bool", "${'a' + true}"},
		{"an int input", "${'a' + inputs.replicas}"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			wantCompileRefusal(t, propModel(t, tc.expr), propSite, "no matching overload")
		})
	}
	t.Run("the control: string() joins", func(t *testing.T) {
		c := mustCompile(t, propModel(t, "${'a' + string(1.0)}"))
		got, _ := mustEval(t, c, propSite, stdScope())
		if got != "a1" {
			t.Errorf("gave %#v, want \"a1\"", got)
		}
	})
}
