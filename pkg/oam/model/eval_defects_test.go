package model

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The rows of this file pin the six defects of the first review of the evaluation step. Each
// row names the defect it shows. Row 6 is a pin and not a red row: the code already refuses at
// Eval, and the repair is the README sentence that says so.

// propsModel returns a model whose first part holds props as its properties.
func propsModel(t *testing.T, props map[string]any) *Model {
	t.Helper()
	m := evalBase(t)
	m.Spec.Parts[0].Properties = props
	return m
}

const propsBase = "spec.parts[0].properties."

// Defect 1: the conversion of a result to Go data has no budget. The review input shares one list
// across the elements of another, so cost stays small and the copy grows with the product of the
// two sizes (10000 by 10000 is 100,000,000 elements). The rows here use sizes that cross the
// limit of 1,000,000 elements at a cost of a few thousand, so a base without the limit allocates
// tens of MiB and no more.
func TestResultSizeIsBounded(t *testing.T) {
	share := func(outer, inner int) string {
		return "${cel.bind(xs, lists.range(" + strconv.Itoa(inner) + "), lists.range(" + strconv.Itoa(outer) + ").map(i, xs))}"
	}
	t.Run("a result under the limit converts", func(t *testing.T) {
		c := mustCompile(t, propModel(t, share(500, 1000)))
		got, _ := mustEval(t, c, propSite, stdScope())
		outer, ok := got.([]any)
		if !ok || len(outer) != 500 {
			t.Fatalf("gave %T of length %d, want a list of 500", got, len(outer))
		}
	})
	t.Run("a shared list that converts past the limit fails and wraps ErrCostLimit", func(t *testing.T) {
		c := mustCompile(t, propModel(t, share(1100, 1100)))
		got, _, err := c.Eval(propSite, stdScope())
		if err == nil {
			t.Fatalf("a result of 1,210,000 elements converted: %T", got)
		}
		if !errors.Is(err, ErrCostLimit) {
			t.Errorf("error %q must wrap ErrCostLimit", err)
		}
		if !strings.Contains(err.Error(), "1000000") || !strings.Contains(err.Error(), propSite) {
			t.Errorf("error %q must name the limit 1000000 and the site %q", err, propSite)
		}
	})
	t.Run("a map result counts too", func(t *testing.T) {
		// The lists hold 1000 + 1000*999 = 1,000,000 elements, which the limit allows. Only the
		// 1000 map entries push the result over it, so a copy that skips the entries passes.
		expr := "${cel.bind(xs, lists.range(999), lists.range(1000).map(i, {'k': xs}))}"
		c := mustCompile(t, propModel(t, expr))
		_, _, err := c.Eval(propSite, stdScope())
		if err == nil || !errors.Is(err, ErrCostLimit) {
			t.Errorf("a result of 1,000,000 list elements and 1,000 map entries gave error %v, want one that wraps ErrCostLimit", err)
		}
	})
	t.Run("a result of exactly the limit converts", func(t *testing.T) {
		c := mustCompile(t, propModel(t, share(1000, 999)))
		got, _ := mustEval(t, c, propSite, stdScope())
		if outer, ok := got.([]any); !ok || len(outer) != 1000 {
			t.Fatalf("gave %T of length %d, want a list of 1000", got, len(outer))
		}
	})
	t.Run("a shared bytes value counts one unit for each byte", func(t *testing.T) {
		// One value of 10,000 bytes shared by 150 places is 1,500,000 bytes once copied.
		bytesIn := func(outer int) string {
			return "${cel.bind(b, bytes(lists.range(1000).map(i, 'xxxxxxxxxx').join('')), lists.range(" + strconv.Itoa(outer) + ").map(i, b))}"
		}
		c := mustCompile(t, propModel(t, bytesIn(150)))
		got, _, err := c.Eval(propSite, stdScope())
		if err == nil || !errors.Is(err, ErrCostLimit) {
			t.Errorf("a result of 1,500,000 bytes gave %T and error %v, want an error that wraps ErrCostLimit", got, err)
		}
		c = mustCompile(t, propModel(t, bytesIn(50)))
		got, _ = mustEval(t, c, propSite, stdScope())
		if outer, ok := got.([]any); !ok || len(outer) != 50 {
			t.Fatalf("a result of 500,000 bytes gave %T of length %d, want a list of 50", got, len(outer))
		}
	})
}

// Defect 2: a bytes result shares its backing array with the cached literal. A write to the
// result changes what the next evaluation of the site returns, for every Compiled that shares
// the cache.
func TestBytesResultIsACopy(t *testing.T) {
	cache := NewCache()
	first, err := cache.Compile(propModel(t, "${b'abc'}"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, _ := mustEval(t, first, propSite, stdScope())
	b, ok := got.([]byte)
	if !ok {
		t.Fatalf("gave %T, want []byte", got)
	}
	b[0] = 'z'
	again, _ := mustEval(t, first, propSite, stdScope())
	if !reflect.DeepEqual(again, []byte("abc")) {
		t.Errorf("after a write to the first result the same site gave %q, want \"abc\"", again)
	}
	second, err := cache.Compile(propModel(t, "${b'abc'}"))
	if err != nil {
		t.Fatalf("Compile again: %v", err)
	}
	other, _ := mustEval(t, second, propSite, stdScope())
	if !reflect.DeepEqual(other, []byte("abc")) {
		t.Errorf("a second Compiled on the same cache gave %q after the write, want \"abc\"", other)
	}
}

// Defect 3: two properties can give one evaluation path, and the second silently replaces the
// first. A key that holds a dot, a key that holds a bracket and an integer key beside a string
// key of the same text all do. The model is refused at compile, naming the path.
func TestPathCollisionIsRefused(t *testing.T) {
	rows := []struct {
		name  string
		props map[string]any
		path  string
	}{
		{"a dotted key and a nested map", map[string]any{
			"a":   map[string]any{"b": "${1}"},
			"a.b": "${2}",
		}, propsBase + "a.b"},
		{"an indexed key and a list", map[string]any{
			"a":    []any{"${1}"},
			"a[0]": "${2}",
		}, propsBase + "a[0]"},
		{"an integer key and a string key of the same text", map[string]any{
			"p": map[any]any{1: "${1}", "1": "${2}"},
		}, propsBase + "p.1"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			wantCompileRefusal(t, propsModel(t, tc.props), tc.path, "same path")
		})
	}
	t.Run("control: distinct paths compile and each answers", func(t *testing.T) {
		c := mustCompile(t, propsModel(t, map[string]any{
			"a":   map[string]any{"b": "${1}"},
			"a-b": "${2}",
		}))
		if got, _ := mustEval(t, c, propsBase+"a.b", stdScope()); got != int64(1) {
			t.Errorf("a.b gave %#v, want int64(1)", got)
		}
		if got, _ := mustEval(t, c, propsBase+"a-b", stdScope()); got != int64(2) {
			t.Errorf("a-b gave %#v, want int64(2)", got)
		}
	})
}

// Defect 4: the cache key joins model identity, path and text with a NUL byte, and YAML can write
// a NUL in a key and in a value. These two sites make one key, so the second site reuses the
// program of the first and gives a string where its own text gives an integer.
func TestCacheKeyDoesNotCollide(t *testing.T) {
	m := propsModel(t, map[string]any{
		"a":      "x\x00${1}",
		"a\x00x": "${1}",
	})
	c := mustCompile(t, m)
	if got, _ := mustEval(t, c, propsBase+"a", stdScope()); got != "x\x001" {
		t.Errorf("the text site gave %#v, want \"x\\x001\"", got)
	}
	if got, _ := mustEval(t, c, propsBase+"a\x00x", stdScope()); got != int64(1) {
		t.Errorf("the single-hole site gave %#v, want int64(1)", got)
	}
}

// Defect 5: Load accepts a free-form map whose keys are not all strings, and yaml.v3 gives it as
// map[any]any. The walker handled map[string]any only, so a template below such a map was never
// compiled, and a template key in it was never refused. The text of a key is the path segment.
func TestWalkerReadsMapsWithNonStringKeys(t *testing.T) {
	t.Run("an undeclared input below the map is refused", func(t *testing.T) {
		m := propsModel(t, map[string]any{
			"p": map[any]any{1: "literal", "q": "${inputs.nope}"},
		})
		wantCompileRefusal(t, m, propsBase+"p.q", "undeclared reference")
	})
	t.Run("a template in a key is refused", func(t *testing.T) {
		m := propsModel(t, map[string]any{
			"p": map[any]any{"a${inputs.image}": "literal"},
		})
		wantCompileRefusal(t, m, propsBase+"p.a${inputs.image}", "in a map key")
	})
	t.Run("an integer key gives the path segment of its text", func(t *testing.T) {
		c := mustCompile(t, propsModel(t, map[string]any{
			"p": map[any]any{1: "${40 + 2}"},
		}))
		if got, _ := mustEval(t, c, propsBase+"p.1", stdScope()); got != int64(42) {
			t.Errorf("p.1 gave %#v, want int64(42)", got)
		}
	})
	t.Run("a list of such maps is read too", func(t *testing.T) {
		m := propsModel(t, map[string]any{
			"p": []any{map[any]any{true: "${inputs.nope}"}},
		})
		wantCompileRefusal(t, m, propsBase+"p[0].true", "undeclared reference")
	})
}

// Defect 6, a pin and not a red row: a map literal with a key that is not a string has the type
// map(dyn, ...), which the checker cannot refuse. It compiles and fails at Eval with the same
// words the checker would use. The README says so.
func TestDynKeyedMapFailsAtEval(t *testing.T) {
	for _, expr := range []string{
		"${{1: 'bad', 'ok': 'good'}}",
		"${[0, {1: 'bad'}]}",
	} {
		t.Run(expr, func(t *testing.T) {
			c := mustCompile(t, propModel(t, expr))
			_, _, err := c.Eval(propSite, stdScope())
			if err == nil {
				t.Fatal("a map with an integer key converted")
			}
			if !strings.Contains(err.Error(), "map keys must be strings") || !strings.Contains(err.Error(), propSite) {
				t.Errorf("error %q must name the site %q and say that map keys must be strings", err, propSite)
			}
		})
	}
}
