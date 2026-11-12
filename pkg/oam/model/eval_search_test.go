package model

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The class of a call that works for a long time and is charged after it ran: cel-go runs
// indexOf and lastIndexOf as two nested loops over the runes of the string and of the search
// text, so a string of 1,000,000 bytes and a search text of 500,000 bytes make about 250 billion
// comparisons. cel-go charges the call by the product of the two lengths, but it observes the
// cost only after the call returns, and the charge of the model reads two operands of about 500
// steps each. A row must be refused in a short time, and a run with no refusal fails it.
func TestSearchIsRefusedBeforeItRuns(t *testing.T) {
	s := stdScope()
	s.Inputs["image"] = strings.Repeat("x", maxValueSize)
	const nick = "(inputs.image.substring(0, 499999) + 'y')"
	// lastIndexOf from offset 0 compares at one position only, so its offset row starts at the end.
	for _, row := range [][2]string{{"indexOf", ""}, {"indexOf", ", 0"}, {"lastIndexOf", ""}, {"lastIndexOf", ", 999999"}} {
		fn, extra := row[0], row[1]
		{
			expr := "inputs.image." + fn + "(" + nick + extra + ")"
			t.Run(expr, func(t *testing.T) {
				c := mustCompile(t, oneValue(t, "${"+expr+"}"))
				type result struct {
					got any
					err error
				}
				done := make(chan result, 1)
				go func() {
					got, _, err := c.Eval("spec.values[0].expr", s)
					done <- result{got, err}
				}()
				select {
				case r := <-done:
					wantCostLimit(t, r.got, r.err)
				case <-time.After(5 * time.Second):
					t.Fatal("no refusal in 5 seconds: the search runs its 250 billion comparisons before anything charges it")
				}
			})
		}
	}
}

// The bound is cel-go's own: a search whose cost cel-go charges, one tenth of the product of the
// two lengths, passes the limit of 1,000,000, and one over it is refused. A search of 3,000 bytes
// for 3,000 costs 900,000 and runs. One of 3,500 for 3,000 costs 1,050,000 and is refused.
func TestSearchBoundIsTheCostLimit(t *testing.T) {
	const nick = "(inputs.image.substring(0, 2999) + 'y')"
	for _, tc := range []struct {
		name  string
		size  int
		fails bool
	}{
		{"under the limit", 3000, false},
		{"over the limit", 3500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := stdScope()
			s.Inputs["image"] = strings.Repeat("x", tc.size)
			c := mustCompile(t, oneValue(t, "${inputs.image.indexOf("+nick+")}"))
			got, _, err := c.Eval("spec.values[0].expr", s)
			if tc.fails {
				wantCostLimit(t, got, err)
				return
			}
			if err != nil || got != int64(-1) {
				t.Fatalf("gave %v, %v, want -1", got, err)
			}
		})
	}
}

// The node gives the value, the failure and the cost that cel-go gives. Each row runs on stock
// cel-go with the limit of the model, so a row that the node reads differently shows as a
// different value or cost.
func TestSearchGivesWhatCelGoGives(t *testing.T) {
	for _, expr := range []string{
		"'hello mellow'.indexOf('')",
		"'hello mellow'.indexOf('ello')",
		"'hello mellow'.indexOf('jello')",
		"'hello mellow'.indexOf('', 2)",
		"'hello mellow'.indexOf('ello', 2)",
		"'hello mellow'.indexOf('ello', 20)",
		"'hello mellow'.indexOf('ello', -1)",
		"'hello mellow'.lastIndexOf('')",
		"'hello mellow'.lastIndexOf('ello')",
		"'hello mellow'.lastIndexOf('jello')",
		"'hello mellow'.lastIndexOf('ello', 6)",
		"'hello mellow'.lastIndexOf('ello', 20)",
		"'hello mellow'.lastIndexOf('ello', -1)",
		"'hello mellow'.lastIndexOf('', 5)",
		"'hello mellow'.indexOf('', 50)",
		"'hello'.indexOf('hello world')",
		"'hello'.lastIndexOf('hello world')",
		"'héllo wörld'.indexOf('wörld')",
		"'héllo wörld'.lastIndexOf('ö', 3)",
		"'日本語日本語'.lastIndexOf('日本')",
		"''.indexOf('a')",
		"''.lastIndexOf('a')",
		"''.lastIndexOf('')",
	} {
		t.Run(expr, func(t *testing.T) {
			want, wantCost := stockResult(t, expr)
			got, cost, err := evalValueCost(t, expr)
			if want == "error" {
				if err == nil {
					t.Fatalf("gave %v, cel-go gives an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("failed with %v, cel-go gives %s", err, want)
			}
			if have := fmt.Sprintf("%T %v", got, got); have != want {
				t.Errorf("gave %s, cel-go gives %s", have, want)
			}
			if cost != wantCost {
				t.Errorf("cost %d, cel-go reports %d", cost, wantCost)
			}
		})
	}
}
