package model

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"cel.dev/cel-go/common/types"
)

// go-kure/launcher#1706: sort, reverse and format copy what their operands hold. cel-go charges a
// sort or a reverse of unknown type one step, a failed call has no result to charge, and reading a
// list costs nothing, so each of the three ran gigabytes of allocation below both budgets.

// The three cases. Each wants the refusal of the budget and an allocation that shows the stop came
// early. The megabytes are runtime.MemStats.TotalAlloc: what the evaluation allocated in total, not
// what it held at once (1,000 of the failed formats below allocated 2,886 MB with a largest sampled
// heap of 41 MB). Before the charges, on the same 200 calls, each evaluation gave true: format
// allocated 577 MB, sort 150 MB and reverse 98 MB.
//
// CI runs the rows under the race detector, which raises some figures (the reverse row gave 49 MB
// without it and 82 MB with it, where the unstopped run gave 98 MB), so the megabyte bounds are
// asserted only without it (raceEnabled). The refusal is asserted in both.
func TestBudgetStopsTheThreeCalls(t *testing.T) {
	// A format that reaches the budget on its own allocates about 2.9 GB in total, at any string
	// length. So 9,000 reads of a bytes value of 100,000 bytes spend 873,000 of the 1,000,000 steps
	// first. What is left pays for about 130 formats. The value is bytes and not a string because the
	// race detector makes the size of a string copy it, which allocated 3.4 GB over these reads.
	spend := fmt.Sprintf("cel.bind(b, bytes(%s), lists.range(9000).all(i, b.size() == 100000))", long100k)
	rows := []struct {
		name, expr string
		maxMB      uint64
	}{
		{"a format that fails after it copied", spend + " && cel.bind(args, [inputs.image, 'x'], lists.range(200).all(i, '%s%d'.format(args) == '' || true))", 450},
		{"a sort of a list with an unknown element type", "cel.bind(xs, [0, 'bad'] + lists.range(10000), lists.range(200).all(i, xs.sort().size() == 10002 || true))", 100},
		// the receiver is a concatenation, which cel-go reverses element by element
		{"a reverse of a receiver of unknown type", "cel.bind(xs, {'xs': lists.range(5000) + lists.range(5000), 'other': 0}.xs, lists.range(200).all(i, xs.reverse().size() == 10000))", 70},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			s := stdScope()
			// below the cap of one value by more than the clauses of the format add
			s.Inputs["image"] = strings.Repeat("x", maxValueSize-10)
			c := mustCompile(t, oneValue(t, "${"+tc.expr+"}"))
			var got any
			var err error
			mb := allocMB(func() { got, _, err = c.Eval("spec.values[0].expr", s) })
			t.Logf("allocated %d MB in total", mb)
			wantCostLimit(t, got, err)
			if !raceEnabled && mb > tc.maxMB {
				t.Errorf("the refusal came after %d MB of allocation, want at most %d", mb, tc.maxMB)
			}
		})
	}
}

// A sort of n keys pays n times the bits of n. 62,500 keys have 16 bits and pay the whole budget of
// 1,000,000 steps, so one sort of a list whose type is not fully known holds at most 62,500
// elements, where nothing else in the evaluation spends. cel-go charges a typed sort by the square
// of its length and refuses it far below that. sortBy pays the same, but its macro costs cel-go
// units for each element, and cel-go's cost limit refuses a sortBy of this length first.
func TestBudgetSortLimit(t *testing.T) {
	const expr = "cel.bind(x, {'v': lists.range(%d), 'o': 0}.v, x.sort().size())"
	t.Run("62,500 elements", func(t *testing.T) {
		got, err := evalValue(t, fmt.Sprintf(expr, 62500))
		if err != nil || got != int64(62500) {
			t.Errorf("got %v, %v, want 62500", got, err)
		}
	})
	t.Run("62,501 elements", func(t *testing.T) {
		got, err := evalValue(t, fmt.Sprintf(expr, 62501))
		wantCostLimit(t, got, err)
	})
}

// The class, held over the pinned environment: every function that takes a list has a row here,
// which runs it 200 times on a list of 10,000 elements whose type the compiler does not fully know,
// or on operands that make it fail. A row is either stopped by a budget or allocates little. A
// new function that takes a list fails this test until it has a row. == and != take a type
// parameter, so the scan does not find them and their rows are here by hand.
func TestBudgetCoversEveryCallThatTakesAList(t *testing.T) {
	const (
		unknown = "{'v': lists.range(5000) + lists.range(5000), 'o': 0}.v"
		mixed   = "[0, 'bad'] + lists.range(10000)"
	)
	type row struct {
		list, call string
		stopped    bool
	}
	rows := map[string][]row{
		"reverse":               {{unknown, "x.reverse()", true}},
		"sort":                  {{unknown, "x.sort()", true}, {mixed, "x.sort()", true}},
		"@sortByAssociatedKeys": {{mixed, "x.sortBy(e, e)", true}},
		"slice":                 {{unknown, "x.slice(0, 10000)", true}},
		"flatten":               {{unknown, "x.flatten()", true}},
		"distinct":              {{unknown, "x.distinct()", true}},
		"format":                {{unknown, "'%s'.format([x])", true}},
		"@in":                   {{unknown, "-1 in x", true}},
		"_==_":                  {{unknown, "x == x", true}},
		"_!=_":                  {{unknown, "x != x", true}},
		// join checks every element before it writes, and the other three copy no element
		"join": {{unknown, "x.join()", false}},
		"size": {{unknown, "x.size()", false}},
		"_+_":  {{unknown, "x + x", false}},
		"_[_]": {{unknown, "x[0]", false}},
	}

	env, err := newEnv()
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range env.Functions() {
		// cel.@block is an internal construct of cel-go's optimizer that gives its body and runs
		// no call of its own, and a disabled declaration cannot be called
		if name == "cel.@block" || fn.IsDeclarationDisabled() {
			continue
		}
		for _, o := range fn.OverloadDecls() {
			for _, arg := range o.ArgTypes() {
				if arg.Kind() == types.ListKind && rows[name] == nil {
					t.Errorf("function %q takes a list (overload %s) and has no row", name, o.ID())
				}
			}
		}
	}

	for _, name := range slices.Sorted(maps.Keys(rows)) {
		for i, tc := range rows[name] {
			t.Run(fmt.Sprintf("%s/%d", name, i), func(t *testing.T) {
				expr := fmt.Sprintf("cel.bind(x, %s, lists.range(200).all(i, [%s].size() > 0 || true))", tc.list, tc.call)
				c := mustCompile(t, oneValue(t, "${"+expr+"}"))
				var got any
				var err error
				mb := allocMB(func() { got, _, err = c.Eval("spec.values[0].expr", stdScope()) })
				if tc.stopped {
					wantCostLimit(t, got, err)
				} else if err != nil || got != true {
					t.Errorf("gave %v, %v, want true", got, err)
				}
				// the format of a list was refused before go-kure/launcher#1706 too, at 113 MB
				if !raceEnabled && mb > 150 {
					t.Errorf("%s allocated %d MB in total, want at most 150", tc.call, mb)
				}
			})
		}
	}
}
