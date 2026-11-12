package model

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/cel-go/cel"

	"github.com/go-kure/launcher/pkg/errors"
)

// go-kure/launcher#1451: cel-go compares two lists element by element at every depth, and
// the cost limit charges the outer lengths only, so a shared nested list made a comparison
// of N^4 steps cost O(N). The evaluation bounds the values a site compares.

// nested4 builds a list of depth 4 whose levels share one list, so it holds n^4 values when
// it is read as a tree. final is the expression that reads d.
func nested4(n int, final string) string {
	return fmt.Sprintf("cel.bind(a, lists.range(%d), cel.bind(b, lists.range(%d).map(i, a), cel.bind(c, lists.range(%d).map(i, b), cel.bind(d, lists.range(%d).map(i, c), %s))))", n, n, n, n, final)
}

func evalValue(t *testing.T, expr string) (any, error) {
	t.Helper()
	got, _, err := evalValueCost(t, expr)
	return got, err
}

func evalValueCost(t *testing.T, expr string) (any, int64, error) {
	t.Helper()
	c := mustCompile(t, oneValue(t, "${"+expr+"}"))
	return c.Eval("spec.values[0].expr", stdScope())
}

func TestComparisonOfSharedNestedValuesIsBounded(t *testing.T) {
	refused := []struct{ name, expr string }{
		{"==", nested4(60, "d == d")},
		{"!=", nested4(60, "d != d")},
		{"in a list", nested4(60, "d in [d]")},
		{"distinct", nested4(60, "[d, d].distinct().size() == 1")},
		{"exists", nested4(60, "[d].exists(x, x == d)")},
		{"all", nested4(60, "[d].all(x, x == d)")},
		{"maps", nested4(60, "{'k': d} == {'k': d}")},
		{"many comparisons share one budget", nested4(20, "lists.range(100).map(i, d == d).size()")},
		{"one value over the limit", "cel.bind(a, lists.range(999), cel.bind(b, lists.range(1000).map(i, a), b == b))"},
		// CEL drops an error when the other operand of || or && decides the result. The stop must survive that.
		{"a stop that || would swallow", "cel.bind(a, lists.range(999), cel.bind(b, lists.range(1000).map(i, a), (b == b) || true))"},
		{"a stop that && would swallow", "cel.bind(a, lists.range(999), cel.bind(b, lists.range(1000).map(i, a), (b == b) && false))"},
		// flatten copies every element it reaches and cel-go charges the outer length only (lists extension version 3).
		{"flatten of shared lists", "cel.bind(a, lists.range(1100), lists.range(1100).map(i, a).flatten().size())"},
		{"flatten to a depth of shared lists", nested4(40, "[d].flatten(4).size()")},
		// a bytes or string value is compared by its content, and cel-go counts it as one value.
		{"equal bytes shared through nested lists", longBuffers("bytes(lists.range(10000).map(i, 'xxxxxxxxxx').join(''))", "a + b''")},
		{"equal strings shared through nested lists", longBuffers("lists.range(10000).map(i, 'xxxxxxxxxx').join('')", "a.upperAscii().lowerAscii()")},
		// the call is charged as in cel-go, and cel-go charges distinct by n^2.
		{"distinct is charged by cel-go", "lists.range(1000).distinct().size()"},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			got, err := evalValue(t, tc.expr)
			if err == nil {
				t.Fatalf("the comparison gave %v, want a refusal", got)
			}
			if !errors.Is(err, ErrCostLimit) || !strings.Contains(err.Error(), "spec.values[0].expr") {
				t.Errorf("error %q must wrap ErrCostLimit and name the site", err)
			}
		})
	}
	allowed := []struct {
		name, expr string
		want       any
	}{
		{"a flat comparison of 100000 values", "lists.range(100000) == lists.range(100000)", true},
		{"a nested comparison of 837,930 values", nested4(30, "d == d"), true},
		{"distinct of 837,930 values", nested4(30, "[d, d].distinct().size() == 1"), true},
		{"one value just under the limit", "cel.bind(a, lists.range(999), cel.bind(b, lists.range(999).map(i, a), b == b))", true},
		{"different values stop early", nested4(60, "d == [d[0]]"), false},
		{"two long equal strings compared once", "cel.bind(a, lists.range(10000).map(i, 'xxxxxxxxxx').join(''), a == a.upperAscii().lowerAscii())", true},
		{"two long equal byte values compared once", "cel.bind(a, bytes(lists.range(10000).map(i, 'xxxxxxxxxx').join('')), a == a + b'')", true},
		{"flatten of a few lists", "[[1, 2], [3]].flatten().size()", int64(3)},
		{"flatten of shared lists under the limit", nested4(30, "[d[0]].flatten(2).size()"), int64(900)},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			got, err := evalValue(t, tc.expr)
			if err != nil || got != tc.want {
				t.Errorf("got %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

// TestIssueExpressionAtN1000IsRefusedFast runs the expression of go-kure/launcher#1451 at
// N=1000, which holds 10^12 values when read as a tree, and requires the refusal itself. The
// evaluation runs in a goroutine only so that a comparison with no bound fails this test with
// its own message after a generous guard, and does not hang the run until the test binary
// times out. The guard is no time assertion: a refusal that comes late still passes.
func TestIssueExpressionAtN1000IsRefusedFast(t *testing.T) {
	const hangGuard = 30 * time.Second
	c := mustCompile(t, oneValue(t, "${"+nested4(1000, "d == d")+"}"))
	type result struct {
		got any
		err error
	}
	done := make(chan result, 1)
	go func() {
		got, _, err := c.Eval("spec.values[0].expr", stdScope())
		done <- result{got, err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("the comparison gave %v, want a refusal", r.got)
		}
		if !errors.Is(r.err, ErrCostLimit) || !strings.Contains(r.err.Error(), "spec.values[0].expr") {
			t.Errorf("error %q must wrap ErrCostLimit and name the site", r.err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("the comparison of 10^12 values hung for %v: it has no bound", hangGuard)
	}
}

// stockResult is what cel-go gives with no change to its comparison.
func stockResult(t *testing.T, expr string) (string, int64) {
	t.Helper()
	env, err := newEnv()
	if err != nil {
		t.Fatal(err)
	}
	ast, iss := env.Compile("(" + expr + ")")
	if iss.Err() != nil {
		return "compile error: " + iss.Err().Error(), 0
	}
	prog, err := env.Program(ast, cel.CostLimit(maxExpressionCost))
	if err != nil {
		t.Fatal(err)
	}
	out, det, err := prog.Eval(map[string]any{})
	if err != nil {
		return "error", 0
	}
	g, err := toGo(out)
	if err != nil {
		return "error", 0
	}
	return fmt.Sprintf("%T %v", g, g), int64(*det.ActualCost())
}

func TestComparisonGivesWhatCelGoGives(t *testing.T) {
	corpus := []string{
		"[1, 1.0][0] == [1, 1.0][1]", "[1u, 1][0] == [1u, 1][1]", "[1, 'x'] == [1.0, 'x']", "[1, 2] == [1, 2]", "[1, 2] == [2, 1]", "[1] != [1, 2]", "[] == []",
		"{'a': 1, 'b': 'x'} == {'a': 1.0, 'b': 'x'}", "{'a': 1} == {'b': 1}", "{'a': [1, {'b': 2}]} == {'a': [1, {'b': 2}]}",
		"[double('NaN')] == [double('NaN')]", "[[1], {'a': 1}][0] == [[1], {'a': 1}][1]", "[1u, 1] == [1, 1u]", "'x' == 'x'", "null == null", "[null] == [null]",
		"[[1], [2]] != [[1], [3]]", "1 in [1, 2]", "1.0 in [1, 'x']", "[1] in [[1], [2]]", "[3] in [[1], [2]]", "'a' in {'a': 1}",
		"2 in {1: 'a', 2: 'b'}", "[1, 1, 2, 1.0, 'a'].distinct()", "[[1], [1], [2]].distinct()", "[].distinct()",
		"[{'a': 1, 'b': 'x'}, {'a': 1.0, 'b': 'x'}].distinct().size()", "b'ab' == b'ab'", "[b'a'] == [b'a']", "duration('1s') == duration('1000ms')",
		"[1, 'x'] != [1, 'y']", "{1: 'a', 'k': 'b'} == {1u: 'a', 'k': 'b'}", "[1, 2, 3].exists(x, x == 2)", "[[1], [2]].all(x, x != [3])",
		"[1, 2].distinct() == [1, 2]", "[[1, [2]]] in [[[1, [2]]]]", "{'a': {'b': [1]}} != {'a': {'b': [2]}}",
		"[[1, 2], [3]].flatten()", "[[1, [2]], [3]].flatten(2)", "[[1], [2]].flatten(0)", "[[1, [2, [3]]]].flatten(1)", "[[1, [2, [3]]]].flatten(2)",
		"[[1, 2], [3]].flatten().size()", "1 == 1", "[1, 2, 3].distinct().size()",
		"['a', 'b'].join()", "['a', 'b'].join('-')", "[].join()", "['a'].join(',')", "['a', 'b', 'c'].join(', ').size()", "'abc' == 'abc'", "'abc' != 'abd'",
	}
	for _, expr := range corpus {
		t.Run(expr, func(t *testing.T) {
			want, wantCost := stockResult(t, expr)
			if strings.HasPrefix(want, "compile error") || want == "error" {
				t.Fatalf("test bug: cel-go gives %q for the corpus expression", want)
			}
			got, cost, err := evalValueCost(t, expr)
			if err != nil {
				t.Fatalf("Eval: %v, cel-go gives %s", err, want)
			}
			if have := fmt.Sprintf("%T %v", got, got); have != want {
				t.Errorf("gave %s, cel-go gives %s", have, want)
			}
			if cost != wantCost {
				t.Errorf("reported cost %d, cel-go reports %d", cost, wantCost)
			}
		})
	}
}

// The evaluation replaces five calls that compare or copy values, and join. They were read from cel-go v0.31.0: the callers of
// Equal in common/types (Contains, behind `in`), in ext (distinct) and in interpreter (the
// == and != nodes). A new cel-go version, or a new version of a pinned extension library, can
// add a caller, and a caller that is not replaced is the gap of go-kure/launcher#1451 again.
func TestComparisonCallSetIsReadFromCelGo(t *testing.T) {
	want := []string{"@in", "_!=_", "_==_", "distinct", "flatten", "join"}
	got := slices.Clone(budgetedCalls)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the budgeted calls are %v, want %v", got, want)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build info: the cel-go version cannot be read")
	}
	var version string
	for _, d := range bi.Deps {
		if d.Path == "github.com/google/cel-go" {
			version = d.Version
		}
	}
	if version != "v0.31.0" {
		t.Fatalf("cel-go is %q. The budgeted calls were read from v0.31.0. Search common/types, ext and interpreter for every caller of Equal in the new version, extend budgetedCalls, then change this version (go-kure/launcher#1413 moves cel-go)", version)
	}
}

func TestFlattenKeepsItsError(t *testing.T) {
	_, err := evalValue(t, "[[1]].flatten(-1)")
	if err == nil || !strings.Contains(err.Error(), "level must be non-negative") {
		t.Errorf("flatten(-1) gave %v, want cel-go's error", err)
	}
}

// longBuffers builds two equal buffers of 100000 bytes that do not share memory, and the
// comparison of 300 lists of 300 lists that share them: 90300 values, and 9 billion bytes
// to scan when every one of them is compared by content.
func longBuffers(a, b string) string {
	return fmt.Sprintf("cel.bind(a, %s, cel.bind(b, %s, cel.bind(xs, lists.range(300).map(i, a), cel.bind(ys, lists.range(300).map(i, b), cel.bind(xx, lists.range(300).map(i, xs), cel.bind(yy, lists.range(300).map(i, ys), xx == yy))))))", a, b)
}

// cel-go builds the error of join from the text of the value it refuses, and the text of a
// list prints every shared child, so the error of one expression grew to 7 GB at n = 200.
func TestJoinErrorNamesNoValue(t *testing.T) {
	for _, expr := range []string{"['', 1].join()", "['', 1].join(',')", nested4(60, "['', d].join()"), nested4(60, "['', d].join(',')")} {
		_, err := evalValue(t, expr)
		if err == nil || !strings.Contains(err.Error(), "join: invalid input") {
			t.Fatalf("%s gave %v, want cel-go's error text join: invalid input", expr, err)
		}
		if len(err.Error()) > 300 {
			t.Errorf("the error of %.40s is %d bytes long, want a text that names no value", expr, len(err.Error()))
		}
	}
}
