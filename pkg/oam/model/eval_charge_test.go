package model

import (
	"cmp"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
)

// go-kure/launcher#1461: cel-go charges some calls by node, not by size. A value that many
// places share, or a call that makes a big output from small operands, ran below the cost
// limit. The evaluation charges every call result and every identifier read by size, and stops
// the evaluation at once.

const (
	// long100k is a string of 100,000 bytes, built below the cost limit.
	long100k = "lists.range(10000).map(i, 'xxxxxxxxxx').join('')"
	// chain10 is the sum of ten values of 100,000 bytes: 1,000,000 bytes, exactly the cap.
	chain10 = "(a + a + a + a + a + a + a + a + a + a)"
)

// allocMB runs fn and gives the megabytes it allocated. The tests of this file do not run in parallel.
func allocMB(fn func()) uint64 {
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	fn()
	runtime.ReadMemStats(&m1)
	return (m1.TotalAlloc - m0.TotalAlloc) >> 20
}

func wantCostLimit(t *testing.T, got any, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("the evaluation gave %v, want a refusal", got)
	}
	if !errors.Is(err, ErrCostLimit) || !strings.Contains(err.Error(), "spec.values[0].expr") {
		t.Errorf("error %q must wrap ErrCostLimit and name the site", err)
	}
}

// The members of the class that cel-go lets through. Each input is accepted by stock cel-go.
func TestChargeRefusesWhatCelGoChargesByNode(t *testing.T) {
	member := []struct{ name, expr string }{
		// S: a long string read many times. cel-go charges size() and != by node.
		{"S size() of a long string in a loop", fmt.Sprintf("cel.bind(s, %s, lists.range(15000).all(i, s.size() == 100000))", long100k)},
		{"S != of a long string in a loop", fmt.Sprintf("cel.bind(s, %s, lists.range(15000).all(i, '' != s))", long100k)},
		{"S int() of a long string in a loop", fmt.Sprintf("cel.bind(s, %s, lists.range(15000).all(i, int(s) == 0))", strings.ReplaceAll(long100k, "xxxxxxxxxx", "0000000000"))},
		// O: sort and sortBy compare long equal values, and cel-go charges them by element count.
		{"O sort of long equal strings in a loop", sortRow("xs.sort().size() == 2")},
		{"O sortBy of long equal strings in a loop", sortRow("xs.sortBy(x, x).size() == 2")},
		// K: a long map key is hashed at every map of a shared nested list.
		{"K long key of a shared map", fmt.Sprintf("cel.bind(k, %s, cel.bind(m, {k: 1}, cel.bind(a, lists.range(300).map(i, m), cel.bind(b, lists.range(300).map(i, a), b == b))))", strings.ReplaceAll(long100k, "xxxxxxxxxx", "kkkkkkkkkk"))},
		// the cap: one value of more than 1,000,000 bytes, built from views below the cost limit.
		{"a string of 1,000,001 bytes", fmt.Sprintf("cel.bind(a, %s, (%s + 'x').size() > 0)", long100k, chain10)},
		{"bytes of 1,000,001 bytes", fmt.Sprintf("cel.bind(a, bytes(%s), (%s + b'x').size() > 0)", long100k, chain10)},
	}
	for _, tc := range member {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			got, err := evalValue(t, tc.expr)
			wantCostLimit(t, got, err)
		})
	}
}

// sortRow builds a list of two equal strings of 100,000 bytes that share no memory and runs
// body on it 7,000 times. Stock cel-go passes this at a cost of about 720,000: it charges a sort
// by the element count. The evaluation charges the bytes of the sorted list, 195 steps a sort.
func sortRow(body string) string {
	return fmt.Sprintf("cel.bind(xs, lists.range(2).map(j, %s), lists.range(7000).all(i, %s))", long100k, body)
}

// The memory of a join and of a copy of a long view is allocated before cel-go looks at it.
func TestChargeStopsBeforeTheAllocation(t *testing.T) {
	rows := []struct{ name, expr string }{
		{"J join of a list of long strings", fmt.Sprintf("cel.bind(big, %s, (lists.range(1000).map(i, big).join() == '') || true)", long100k)},
		{"R reverse of a view over the cap", "cel.bind(a, lists.range(100000), (a + a + a + a + a + a + a + a + a + a + a).reverse().size() > 0)"},
		{"R slice of a view over the cap", "cel.bind(a, lists.range(100000), (a + a + a + a + a + a + a + a + a + a + a).slice(0, 10).size() > 0)"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			var got any
			var err error
			mb := allocMB(func() { got, err = evalValue(t, tc.expr) })
			wantCostLimit(t, got, err)
			if mb > 50 {
				t.Errorf("the refusal came after %d MB of allocation, want less than 50", mb)
			}
		})
	}
}

// The cap sits at 1,000,000 bytes: the value at the cap is allowed and one more byte is refused.
func TestChargeCapIsOneMillion(t *testing.T) {
	allowed := []struct{ name, expr string }{
		{"a string of 1,000,000 bytes", fmt.Sprintf("cel.bind(a, %s, %s.size())", long100k, chain10)},
		{"a list of 1,000,000 elements", "cel.bind(a, lists.range(500000), (a + a).size())"},
		{"bytes of 1,000,000 bytes", fmt.Sprintf("cel.bind(a, bytes(%s), %s.size())", long100k, chain10)},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			got, err := evalValue(t, tc.expr)
			if err != nil || got != int64(1000000) {
				t.Errorf("got %v, %v, want 1000000", got, err)
			}
		})
	}
}

// The same cap for a list, built from two views of 500,000 elements.
func TestChargeListCapIsOneMillion(t *testing.T) {
	got, err := evalValue(t, "cel.bind(a, lists.range(500000), (a + a + [0]).size() > 0)")
	wantCostLimit(t, got, err)
}

// A refusal reaches the caller as an error of Eval, and never as a panic of the process. cel-go
// recovers the cancellation that the evaluation raises (cel/program.go, the deferred recover of
// (*prog).Eval), and an all() loop must end at once instead of reading an error value 100,000 times.
// Each iteration reads a value of 100,000 bytes, 97 steps, so the budget of 1,000,000 steps is gone
// after about 10,300 iterations, at a cost of about 392,000. A loop that ran on would pass the cost
// limit of 1,000,000 of cel-go, or end with the budget exhausted, and Compiled.Eval would refuse
// either from the budget, so the bound on the cost is what tells the stop from the end.
func TestChargeStopIsAnErrorFromEval(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the evaluation panicked: %v", r)
		}
	}()
	c := mustCompile(t, oneValue(t, "${cel.bind(s, "+long100k+", lists.range(100000).all(i, s.size() == 100000))}"))
	_, cost, err := c.Eval("spec.values[0].expr", stdScope())
	wantCostLimit(t, nil, err)
	t.Logf("cost at the stop: %d", cost)
	if cost > 600000 {
		t.Errorf("the evaluation went on to a cost of %d after the stop, want under 600000", cost)
	}
}

// What a model may do stays allowed, and the lazy operators do not read what they skip.
func TestChargeAllowsAndKeepsLaziness(t *testing.T) {
	over := fmt.Sprintf("cel.bind(a, %s, (%s + 'x').size() > 0)", long100k, chain10)
	allowed := []struct {
		name, expr string
		want       any
	}{
		{"one size() of a long string", fmt.Sprintf("cel.bind(s, %s, s.size())", long100k), int64(100000)},
		{"one sort of short strings", "lists.range(100).map(i, string(i)).sort().size()", int64(100)},
		{"a list of 900000 elements read once", "lists.range(900000).size()", int64(900000)},
		{"&& does not read its skipped side", "false && " + over, false},
		{"|| does not read its skipped side", "true || " + over, true},
		{"?: does not read its skipped branch", "true ? true : (" + over + ")", true},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalValue(t, tc.expr)
			if err != nil || got != tc.want {
				t.Errorf("got %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

// Every function of the pinned environment has a class in testdata/celenv1-charges.txt, so a
// new cel-go version fails two lists and not one. A function that makes a big output from small
// operands names the check that bounds it.
func TestChargeClassCoversEveryFunction(t *testing.T) {
	golden, err := os.ReadFile(goldenFunctions)
	if err != nil {
		t.Fatalf("read %s: %v", goldenFunctions, err)
	}
	classes, err := os.ReadFile("testdata/celenv1-charges.txt")
	if err != nil {
		t.Fatalf("read the charge classes: %v", err)
	}
	have := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(classes)), "\n") {
		name, class, ok := strings.Cut(l, ": ")
		if !ok {
			t.Fatalf("line %q is not `function: class`", l)
		}
		have[name] = class
	}
	var want []string
	for _, l := range strings.Split(strings.TrimSpace(string(golden)), "\n") {
		name, _, _ := strings.Cut(l, ": ")
		want = append(want, name)
		class, ok := have[name]
		if !ok {
			t.Errorf("function %q has no class in the charge list", name)
			continue
		}
		if kind, _, _ := strings.Cut(class, " "); !slices.Contains([]string{"scalar-out", "bounded", "amplifying"}, kind) {
			t.Errorf("function %q has the class %q, want scalar-out, bounded or amplifying", name, class)
		}
		if strings.HasPrefix(class, "amplifying") && !strings.Contains(class, "(") {
			t.Errorf("amplifying function %q must name its check in brackets", name)
		}
	}
	for name := range have {
		if !slices.Contains(want, name) {
			t.Errorf("the charge list names %q, which the environment lacks", name)
		}
	}
}

const (
	// long2k is a string of 2,000 bytes.
	long2k = "lists.range(200).map(i, 'xxxxxxxxxx').join('')"
	// key100k is a string of 100,000 bytes that is not equal to long100k.
	key100k = "lists.range(10000).map(i, 'kkkkkkkkkk').join('')"
)

// replace builds its result before cel-go charges it. s.replace('x', t) with a s of 100,000 bytes
// and a t of 2,000 bytes builds 200,000,000 bytes from two small operands, so the evaluation checks
// the size of the result first (finding 1 of the review of go-kure/launcher#1461).
func TestChargeReplaceStopsBeforeTheAllocation(t *testing.T) {
	rows := []struct{ name, expr string }{
		{"replace all", fmt.Sprintf("cel.bind(s, %s, cel.bind(t, %s, s.replace('x', t).size() > 0))", long100k, long2k)},
		{"replace with a count", fmt.Sprintf("cel.bind(s, %s, cel.bind(t, %s, s.replace('x', t, 100000).size() > 0))", long100k, long2k)},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			var got any
			var err error
			mb := allocMB(func() { got, err = evalValue(t, tc.expr) })
			wantCostLimit(t, got, err)
			if mb > 50 {
				t.Errorf("the refusal came after %d MB of allocation, want less than 50", mb)
			}
		})
	}
}

// A result of replace that cel-go lets through stays allowed. cel-go charges the result of replace by
// its length, so a result near the cap of 1,000,000 stops at the cost limit of cel-go first.
func TestChargeReplaceCap(t *testing.T) {
	allowed := []struct {
		name, expr string
		want       any
	}{
		{"a result of 500,000 bytes", fmt.Sprintf("cel.bind(s, %s, s.replace('x', 'yyyyy').size())", long100k), int64(500000)},
		{"a result smaller than the string", fmt.Sprintf("cel.bind(s, %s, s.replace('xx', '').size())", long100k), int64(0)},
		{"a count that keeps the result under the cap", fmt.Sprintf("cel.bind(s, %s, s.replace('x', 'yyyyyyyyyyyyyyyyyyyy', 1000).size())", long100k), int64(100000 + 1000*19)},
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

// A map or list key that is an identifier is not read through Exec: cel-go calls the Qualify of the
// key attribute, which resolves the value by itself. A key of 100,000 bytes is hashed at every lookup,
// so the evaluation charges the key where cel-go reads it (finding 2 of the review of
// go-kure/launcher#1461).
func TestChargeStopsALongKeyOfAnIndex(t *testing.T) {
	rows := []struct{ name, expr string }{
		{"m[k] in a loop", fmt.Sprintf("cel.bind(k, %s, cel.bind(m, {k: 1}, lists.range(15000).all(i, m[k] == 1)))", key100k)},
		{"a conditional key in a loop", fmt.Sprintf("cel.bind(k, %s, cel.bind(m, {k: 1}, lists.range(15000).all(i, m[i >= 0 ? k : k] == 1)))", key100k)},
		// the key is picked from the list that a call returns: the call wrapper charges the list and never the string (finding 2 of the second review)
		{"a key picked from a list", fmt.Sprintf("cel.bind(k, %s, cel.bind(xs, [k], cel.bind(m, {k: 1}, lists.range(15000).all(i, m[(xs + [])[0]] == 1))))", key100k)},
		{"a conditional key with a constant branch", fmt.Sprintf("cel.bind(k, %s, cel.bind(m, {k: 1}, lists.range(15000).all(i, m[i >= 0 ? k : ''] == 1)))", key100k)},
	}
	for _, tc := range rows {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			got, err := evalValue(t, tc.expr)
			wantCostLimit(t, got, err)
		})
	}
	t.Run("allowed/one lookup", func(t *testing.T) {
		got, err := evalValue(t, fmt.Sprintf("cel.bind(k, %s, cel.bind(m, {k: 1}, m[k]))", key100k))
		if err != nil || got != int64(1) {
			t.Errorf("got %v, %v, want 1", got, err)
		}
	})
}

// join writes n-1 separators. The check of its buffer counts the bytes that the next write adds
// and no more, so one element and a separator of 1,000,000 bytes is a result of one byte (finding 3 of the review of go-kure/launcher#1461).
func TestChargeJoinChecksWhatItWrites(t *testing.T) {
	allowed := []struct {
		name, expr string
		want       any
	}{
		{"one element and a separator of 1,000,000 bytes", fmt.Sprintf("cel.bind(a, %s, ['x'].join(%s).size())", long100k, chain10), int64(1)},
		{"two elements and one separator", fmt.Sprintf("cel.bind(a, %s, cel.bind(h, a + a, [h, h].join('-').size()))", long100k), int64(400001)},
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

// The replacements give what cel-go gives, with the cost cel-go reports: the evaluation builds the
// result of replace itself, and it reads a key through the Qualify of the wrapper.
func TestChargeWrappersGiveWhatCelGoGives(t *testing.T) {
	corpus := []string{
		"'hello'.replace('l', 'L')", "'hello'.replace('l', 'L', 1)", "'hello'.replace('l', 'L', 0)", "'hello'.replace('l', 'L', -1)",
		"'hello'.replace('', '-')", "'hello'.replace('', '-', 2)", "'aaa'.replace('a', '', 2)", "'héllo'.replace('', '.')", "''.replace('', 'x')", "'abc'.replace('d', 'x')",
		"'a.b.c'.replace('.', '').size()",
		"{'a': 1}['a']", "[1, 2, 3][1]", "{'a': 1}['a' + '']", "[10, 20][1 + 0]", "cel.bind(k, 'a', {'a': 1}[k])", "cel.bind(k, 'a', {'a': 1}[true ? k : 'b'])",
		"[0, 1, 2].map(i, [10, 20, 30][i])", "cel.bind(m, {'a': {'b': 2}}, m['a']['b'])", "cel.bind(k, 'b', {'a': {'b': 2}}['a'][k])",
		// sort and sortBy are evaluated by budgetedSort and must give what cel-go gives
		"[3, 1, 2].sort()", "['b', 'a', 'c'].sort()", "[1.5, 0.5, 1.0].sort()", "[b'b', b'a'].sort()", "[true, false].sort()", "[7].sort()", "lists.range(0).sort()",
		"[2, 1].sortBy(x, -x)", "['b', 'a'].sortBy(x, x)", "[{'n': 2}, {'n': 1}].sortBy(m, m.n).map(m, m.n)", "['x', 'y', 'z'].sortBy(s, size(s) == 1 ? 3 - s.indexOf(s) : 0)",
		// a key that holds a call or an index is resolved once: a second resolution reports more cost than cel-go
		"cel.bind(xs, ['a'], {'a': 1}[xs[0 + 0]])", "cel.bind(xs, ['a'], {'a': 1}[(xs + [])[0]])", "cel.bind(k, 'a', cel.bind(c, 1, {'a': 1}[c + 0 > 0 ? k : k]))",
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

// A map comparison reads the keys of cel-go's map in Go map order. The budget made the outcome
// depend on that order: a comparison that a cheap key decides gave false when it came first and
// ErrCostLimit when a deep key came first. The walk now reads every key, so the spend and the
// outcome do not depend on the order.
func TestMapComparisonDoesNotDependOnKeyOrder(t *testing.T) {
	for name, expr := range map[string]string{
		"a cheap key that differs":   nested4(60, "{'cheap': 0, 'deep': d} == {'cheap': 1, 'deep': d}"),
		"a key that the other lacks": nested4(60, "{'cheap': 0, 'deep': d} == {'other': 0, 'deep': d}"),
	} {
		t.Run(name, func(t *testing.T) {
			c := mustCompile(t, oneValue(t, "${"+expr+"}"))
			for i := 0; i < 60; i++ {
				got, _, err := c.Eval("spec.values[0].expr", stdScope())
				if !errors.Is(err, ErrCostLimit) {
					t.Fatalf("run %d gave %v, %v: every run must wrap ErrCostLimit", i, got, err)
				}
			}
		})
	}
}

// A string nested in an input is exported as it is, so it is held to the cap of one value, as a
// string that is read by name is.
func TestNestedInputStringIsCapped(t *testing.T) {
	over := strings.Repeat("x", maxValueSize+1)
	at := strings.Repeat("x", maxValueSize)
	for _, tc := range []struct {
		name   string
		input  any
		expr   string
		refuse bool
	}{
		{"list element over the cap", []any{over}, "inputs.tags", true},
		{"map value over the cap", map[string]any{"k": over}, "inputs.tags", true},
		{"deep element over the cap", []any{[]any{map[string]any{"k": over}}}, "inputs.tags", true},
		{"element read by index", []any{over}, "inputs.tags[0]", true},
		{"list element at the cap", []any{at}, "inputs.tags", false},
		{"map key over the cap", map[string]any{over: 1}, "inputs.tags", true},
		{"map key at the cap", map[string]any{at: 1}, "inputs.tags", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := stdScope()
			s.Inputs["tags"] = tc.input
			_, _, err := mustCompile(t, oneValue(t, "${"+tc.expr+"}")).Eval("spec.values[0].expr", s)
			if tc.refuse != errors.Is(err, ErrCostLimit) || (!tc.refuse && err != nil) {
				t.Errorf("gave %v, refuse = %v", err, tc.refuse)
			}
		})
	}
}

// A map comparison spends a step for every key it visits, also a key that the other map lacks.
// Two inner maps of 10,000 keys with no key in common held a step for the maps and none for the
// keys, so 1,000 comparisons read 10,000,000 entries for the price of 2,000 steps.
func TestMapComparisonSpendsForAMissingKey(t *testing.T) {
	a, b := map[string]any{}, map[string]any{}
	for i := 0; i < 10000; i++ {
		a[fmt.Sprintf("a%d", i)] = int64(i)
		b[fmt.Sprintf("b%d", i)] = int64(i)
	}
	s := stdScope()
	s.Inputs["labels"], s.Inputs["config"] = a, b
	c := mustCompile(t, oneValue(t, "${lists.range(1000).all(i, {'x': inputs.labels} != {'x': inputs.config})}"))
	got, _, err := c.Eval("spec.values[0].expr", s)
	if !errors.Is(err, ErrCostLimit) {
		t.Fatalf("gave %v, %v: the comparisons read 10,000,000 entries and must wrap ErrCostLimit", got, err)
	}
}

// sort and sortBy compare the keys by content and can fail after they compared: a list that
// holds two long equal strings and an int is sorted until the int is met, and the error goes
// away in `|| true`. The charge spends the bytes of the keys before the sort runs, so a sort
// that fails is paid for as a sort that succeeds is.
func TestChargeFailedSortSpendsForItsCompares(t *testing.T) {
	s := stdScope()
	s.Inputs["image"] = strings.Repeat("x", maxValueSize)
	s.Inputs["nick"] = strings.Repeat("x", maxValueSize)
	for _, body := range []string{"xs.sort() == []", "xs.sortBy(x, x) == []"} {
		t.Run(body, func(t *testing.T) {
			expr := "cel.bind(xs, [inputs.image, inputs.nick, 0], lists.range(15000).all(i, (" + body + ") || true))"
			got, _, err := mustCompile(t, oneValue(t, "${"+expr+"}")).Eval("spec.values[0].expr", s)
			if !errors.Is(err, ErrCostLimit) {
				t.Fatalf("gave %v, %v: 15,000 failed sorts compare 30 GB and must wrap ErrCostLimit", got, err)
			}
		})
	}
}

// A sort that fails gives the failure that cel-go gives, so `|| true` and `.orValue` read the same.
func TestChargeSortFailsLikeCelGo(t *testing.T) {
	for _, expr := range []string{"[1, 'b'].sort()", "[1, 'b'].sortBy(x, x)", "[1, 'b', 2].sort()"} {
		t.Run(expr, func(t *testing.T) {
			if want, _ := stockResult(t, expr); want != "error" {
				t.Fatalf("test bug: cel-go gives %q for %s, want an error", want, expr)
			}
			if got, _, err := evalValueCost(t, expr); err == nil {
				t.Errorf("gave %v, cel-go gives an error", got)
			}
		})
	}
}

// The class of a call that works and then fails: the failure is dropped by `|| true`, and a charge
// of the result sees nothing. Each row runs one budgeted node on strings of 1,000,000 bytes
// 2,000 times, which is 2,000,000 steps if the node pays for the bytes it touches. A node that
// fails before it touches anything (touches false) must let the whole loop finish.
//
// A row must fail when its own guard is removed, so two rows carry more than the stop:
//   - replace stops at the first call, and the result cap would stop an unguarded replace too, after
//     it built 16 MB. maxMB bounds what the guarded node may allocate.
//   - sortBy reads x twice per iteration in the keys that its macro builds, 1,954 steps, and the
//     charge of the keys adds the same. 400 iterations spend 782,000 steps for the reads alone, under
//     the budget, and 1,564,000 with the charge, over it.
func TestChargeFailAfterWorkSweep(t *testing.T) {
	s := stdScope()
	s.Inputs["image"] = strings.Repeat("x", maxValueSize)
	s.Inputs["nick"] = strings.Repeat("x", maxValueSize)
	rows := []struct {
		node, body string
		touches    bool
		iters      int    // 2,000 when 0
		maxMB      uint64 // an allocation bound for a row that must stop before it builds, 0 for none
	}{
		{"==", "ys == zs", true, 0, 0},
		{"in", "ys in [zs]", true, 0, 0},
		{"distinct", "xs.distinct() == []", true, 0, 0},
		{"flatten", "xs.flatten(-1) == []", false, 0, 0},
		{"replace", "inputs.image.replace('x', 'yyyyyyyyyyyyyyyy') == ''", true, 0, 8},
		{"join", "ys.join() == ''", false, 0, 0},
		{"sort", "xs.sort() == []", true, 0, 0},
		{"sortBy", "xs.sortBy(x, x) == []", true, 400, 0},
	}
	for _, tc := range rows {
		t.Run(tc.node, func(t *testing.T) {
			// the lists are bound once: reading inputs.image in the loop would be charged, and stop it
			iters := cmp.Or(tc.iters, 2000)
			expr := fmt.Sprintf("cel.bind(xs, [inputs.image, inputs.nick, 0], cel.bind(ys, [inputs.image, 0], cel.bind(zs, [inputs.nick, 1], "+
				"lists.range(%d).all(i, (%s) || true))))", iters, tc.body)
			c := mustCompile(t, oneValue(t, "${"+expr+"}"))
			var got any
			var err error
			mb := allocMB(func() { got, _, err = c.Eval("spec.values[0].expr", s) })
			if tc.touches {
				if !errors.Is(err, ErrCostLimit) {
					t.Fatalf("gave %v, %v: the failed calls touch more than the budget pays for and must wrap ErrCostLimit", got, err)
				}
				if tc.maxMB != 0 && mb > tc.maxMB {
					t.Fatalf("allocated %d MB, want at most %d: the node must stop before it builds", mb, tc.maxMB)
				}
				return
			}
			// the loop finishes, and the failing calls built nothing: 2,000 copies of the string are 2 GB
			if err != nil || got != true || mb > 64 {
				t.Fatalf("gave %v, %v after allocating %d MB: a call that fails before it touches anything must run to the end and copy nothing", got, err, mb)
			}
		})
	}
}

// A join that meets a non-string gives the failure that cel-go gives, after any number of strings.
func TestChargeJoinFailsLikeCelGo(t *testing.T) {
	for _, expr := range []string{"['a', 1].join()", "['a', 'b', 2.5].join(',')", "[1, 'a'].join()"} {
		t.Run(expr, func(t *testing.T) {
			if want, _ := stockResult(t, expr); want != "error" {
				t.Fatalf("test bug: cel-go gives %q for %s, want an error", want, expr)
			}
			_, _, err := evalValueCost(t, expr)
			if err == nil || !strings.Contains(err.Error(), "join: invalid input of type") {
				t.Errorf("error %v, want the type error of join", err)
			}
		})
	}
}
