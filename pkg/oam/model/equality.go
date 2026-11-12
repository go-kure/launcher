package model

import (
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"

	"github.com/go-kure/launcher/pkg/errors"
)

// maxCompareSteps is the number of values the comparisons of one site evaluation may visit.
// A list, a map, a list element and a map value each count one. It is the number of
// maxResultElements. Stock cel-go compares nested lists at every depth, even when they share
// one list, and its cost counts the outer lengths only, so n lists of n lists of n lists of
// n elements cost O(n) and took n^4 steps (go-kure/launcher#1451).
const maxCompareSteps = 1000000

// compareBudgetName names the budget in the activation. It is no CEL identifier, so no
// expression can read or shadow it.
const compareBudgetName = "@compare-budget"

// budgetedCalls are the calls that compare lists or maps and that the evaluation replaces:
// `==`, `!=`, `in` and `distinct`. They are every caller of Equal in cel-go v0.31.0, read in
// common/types (Contains, behind `in`), ext (distinct) and interpreter (the == and != nodes).
// A new cel-go version, or a new version of a pinned extension library, can add a caller.
// TestComparisonCallSetIsReadFromCelGo fails on any other version, so that someone reads the
// callers again (go-kure/launcher#1413 moves cel-go).
var budgetedCalls = []string{operators.Equals, operators.NotEquals, operators.In, "distinct"}

// compareBudget is the steps left to the comparisons of one site evaluation.
type compareBudget struct{ left int }

func newCompareBudget() *compareBudget { return &compareBudget{left: maxCompareSteps} }

// budgetOf is the budget of the evaluation that runs a. Eval always sets one.
func budgetOf(a interpreter.Activation) *compareBudget {
	if v, ok := a.ResolveName(compareBudgetName); ok {
		if b, ok := v.(*compareBudget); ok {
			return b
		}
	}
	return newCompareBudget()
}

func compareStop() ref.Val {
	return types.WrapErr(errors.Errorf("%w: the comparisons visit more than %d values", ErrCostLimit, maxCompareSteps))
}

// equal compares l and r as cel-go does and spends one step for each value it visits. It
// gives false and ok when the budget is gone. A list and a map are compared here, so that
// every level spends; any other value is compared by cel-go.
func (b *compareBudget) equal(l, r ref.Val) (v ref.Val, ok bool) {
	if b.left--; b.left < 0 {
		return nil, false
	}
	if la, lok := l.(traits.Lister); lok {
		if ra, rok := r.(traits.Lister); rok {
			return b.equalLists(la, ra)
		}
	}
	if lm, lok := l.(traits.Mapper); lok {
		if rm, rok := r.(traits.Mapper); rok {
			return b.equalMaps(lm, rm)
		}
	}
	return types.Equal(l, r), true
}

func (b *compareBudget) equalLists(l, r traits.Lister) (ref.Val, bool) {
	if l.Size() != r.Size() {
		return types.False, true
	}
	n := l.Size().(types.Int)
	for i := types.IntZero; i < n; i++ {
		v, ok := b.equal(l.Get(i), r.Get(i))
		if !ok || v != types.True {
			return v, ok
		}
	}
	return types.True, true
}

func (b *compareBudget) equalMaps(l, r traits.Mapper) (ref.Val, bool) {
	if l.Size() != r.Size() {
		return types.False, true
	}
	for it := l.Iterator(); it.HasNext() == types.True; {
		k := it.Next()
		lv, _ := l.Find(k)
		rv, found := r.Find(k)
		if !found {
			return types.False, true
		}
		v, ok := b.equal(lv, rv)
		if !ok || v != types.True {
			return v, ok
		}
	}
	return types.True, true
}

// compareDecorator replaces the budgeted calls in a program.
func compareDecorator(i interpreter.Interpretable) (interpreter.Interpretable, error) {
	call, ok := i.(interpreter.InterpretableCall)
	if !ok {
		return i, nil
	}
	args := call.Args()
	switch call.Function() {
	case operators.Equals, operators.NotEquals:
		return &budgetedEq{id: i.ID(), lhs: args[0], rhs: args[1], negate: call.Function() == operators.NotEquals}, nil
	case operators.In:
		return &budgetedIn{id: i.ID(), elem: args[0], list: args[1]}, nil
	case "distinct":
		return &budgetedDistinct{id: i.ID(), list: args[0]}, nil
	}
	return i, nil
}

// operands evaluates two operands as cel-go does: the first error is the result.
func operands(a interpreter.Activation, x, y interpreter.Interpretable) (l, r, failed ref.Val) {
	l = x.Eval(a)
	if types.IsError(l) {
		return nil, nil, l
	}
	r = y.Eval(a)
	if types.IsError(r) {
		return nil, nil, r
	}
	return l, r, nil
}

type budgetedEq struct {
	id       int64
	lhs, rhs interpreter.Interpretable
	negate   bool
}

func (e *budgetedEq) ID() int64 { return e.id }

func (e *budgetedEq) Eval(a interpreter.Activation) ref.Val {
	l, r, failed := operands(a, e.lhs, e.rhs)
	if failed != nil {
		return failed
	}
	v, ok := budgetOf(a).equal(l, r)
	if !ok {
		return compareStop()
	}
	if e.negate {
		return types.Bool(v != types.True)
	}
	return v
}

type budgetedIn struct {
	id         int64
	elem, list interpreter.Interpretable
}

func (e *budgetedIn) ID() int64 { return e.id }

func (e *budgetedIn) Eval(a interpreter.Activation) ref.Val {
	x, c, failed := operands(a, e.elem, e.list)
	if failed != nil {
		return failed
	}
	list, ok := c.(traits.Lister)
	if !ok {
		if c.Type().HasTrait(traits.ContainerType) {
			return c.(traits.Container).Contains(x)
		}
		return types.ValOrErr(c, "no such overload")
	}
	b := budgetOf(a)
	n := list.Size().(types.Int)
	for i := types.IntZero; i < n; i++ {
		v, ok := b.equal(x, list.Get(i))
		if !ok {
			return compareStop()
		}
		if v == types.True {
			return types.True
		}
	}
	return types.False
}

type budgetedDistinct struct {
	id   int64
	list interpreter.Interpretable
}

func (e *budgetedDistinct) ID() int64 { return e.id }

func (e *budgetedDistinct) Eval(a interpreter.Activation) ref.Val {
	c := e.list.Eval(a)
	if types.IsError(c) {
		return c
	}
	list, ok := c.(traits.Lister)
	if !ok {
		return types.ValOrErr(c, "no such overload")
	}
	b := budgetOf(a)
	n := list.Size().(types.Int)
	var unique []ref.Val
	for i := types.IntZero; i < n; i++ {
		val := list.Get(i)
		seen := false
		for _, u := range unique {
			v, ok := b.equal(val, u)
			if !ok {
				return compareStop()
			}
			if v == types.True {
				seen = true
				break
			}
		}
		if !seen {
			unique = append(unique, val)
		}
	}
	return types.DefaultTypeAdapter.NativeToValue(unique)
}
