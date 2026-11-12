package model

import (
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
// (*prog).Eval), and an all() loop must end at once instead of reading an error value 15,000 times.
func TestChargeStopIsAnErrorFromEval(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the evaluation panicked: %v", r)
		}
	}()
	c := mustCompile(t, oneValue(t, "${cel.bind(s, "+long100k+", lists.range(15000).all(i, s.size() == 100000))}"))
	_, cost, err := c.Eval("spec.values[0].expr", stdScope())
	wantCostLimit(t, nil, err)
	if cost > 2000000 {
		t.Errorf("the evaluation went on to a cost of %d after the stop", cost)
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
