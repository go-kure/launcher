package model

import (
	"strings"

	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"

	"github.com/go-kure/launcher/pkg/errors"
)

// The class of this file: every cel-go path that walks or prints a value must be charged by
// the size of the value, not by the node that holds it. cel-go charges a call by the length of
// its operands, and a value that several places share is walked once for each place. The
// comparisons, flatten, join and the length of a string or bytes value are charged here. A
// reader who adds a cel-go version or a library looks for the next path of this class.

// maxCompareSteps is the number of values the comparisons and flattens of one site
// evaluation may visit. A list, a map, a list element and a map value each count one. It is
// the number of maxResultElements. Stock cel-go compares nested lists at every depth, even
// when they share one list, and its cost counts the outer lengths only, so n lists of n lists
// of n lists of n elements cost O(n) and took n^4 steps (go-kure/launcher#1451). The flatten
// of the pinned lists extension copies every element it reaches and charges the outer length
// only, in the same way.
const maxCompareSteps = 1000000

// compareBytesPerStep is the number of bytes of a string or bytes value that one step pays
// for. cel-go compares such a value by its content and counts it as one value.
const compareBytesPerStep = 1024

// compareBudgetName names the budget in the activation. It is no CEL identifier, so no
// expression can read or shadow it.
const compareBudgetName = "@compare-budget"

// budgetedCalls are the calls that the evaluation replaces, because cel-go compares, copies or
// prints nested values in them and charges the outer length only: `==`, `!=`, `in`, `distinct`,
// `flatten` and `join`. The join of a list that holds a non-string builds its error from the
// text of the whole tree. The first four are every caller of Equal in cel-go v0.31.0, read in common/types
// (Contains, behind `in`), ext (distinct) and interpreter (the == and != nodes). flatten is
// the one list function of the pinned extension that copies nested elements under the legacy
// cost tracker (ext/lists.go, trackListFlattenLegacy). A new cel-go version, or a new version
// of a pinned extension library, can add a call. TestComparisonCallSetIsReadFromCelGo fails
// on any other version, so that someone reads the calls again (go-kure/launcher#1413 moves
// cel-go).
var budgetedCalls = []string{operators.Equals, operators.NotEquals, operators.In, "distinct", "flatten", "join"}

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

// exhausted reports that a comparison or flatten of the evaluation needed more than the
// budget. It stays true: CEL drops an error when the other operand of || or && decides the
// result, so the evaluation reads this after the program ends and never relies on the error.
func (b *compareBudget) exhausted() bool { return b.left < 0 }

func compareStopError() error {
	return errors.Errorf("%w: the comparisons and flattens visit more than %d values", ErrCostLimit, maxCompareSteps)
}

func compareStop() ref.Val { return types.WrapErr(compareStopError()) }

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
	if n := scalarSize(l); n >= compareBytesPerStep {
		if b.left -= n / compareBytesPerStep; b.left < 0 {
			return nil, false
		}
	}
	return types.Equal(l, r), true
}

// scalarSize is the length in bytes of a string or bytes value, and 0 for any other.
func scalarSize(v ref.Val) int {
	switch x := v.(type) {
	case types.String:
		return len(x)
	case types.Bytes:
		return len(x)
	}
	return 0
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

// equalMaps reads every key, also after one differs. cel-go gives the keys in Go map order, so
// an early stop would make the spend, and with it a stop by the budget, depend on that order.
func (b *compareBudget) equalMaps(l, r traits.Mapper) (ref.Val, bool) {
	if l.Size() != r.Size() {
		return types.False, true
	}
	same := true
	for it := l.Iterator(); it.HasNext() == types.True; {
		k := it.Next()
		// both maps hash the key: a long key costs one step for each 1,024 bytes, twice
		if b.left -= 2 * (scalarSize(k) / compareBytesPerStep); b.left < 0 {
			return nil, false
		}
		lv, _ := l.Find(k)
		rv, found := r.Find(k)
		if !found {
			same = false
			continue
		}
		v, ok := b.equal(lv, rv)
		if !ok {
			return v, ok
		}
		same = same && v == types.True
	}
	return types.Bool(same), true
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
		return &budgetedEq{InterpretableCall: call, lhs: args[0], rhs: args[1], negate: call.Function() == operators.NotEquals}, nil
	case operators.In:
		return &budgetedIn{InterpretableCall: call, elem: args[0], list: args[1]}, nil
	case "distinct":
		return &budgetedDistinct{InterpretableCall: call, list: args[0]}, nil
	case "join":
		j := &budgetedJoin{InterpretableCall: call, list: args[0]}
		if len(args) == 2 {
			j.sep = args[1]
		}
		return j, nil
	case "flatten":
		f := &budgetedFlatten{InterpretableCall: call, list: args[0]}
		if len(args) == 2 {
			f.depth = args[1]
		}
		return f, nil
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

// A replacement node embeds the call it replaces, so that it keeps ID, Function, OverloadID
// and Args. cel-go's cost tracker reads those from an InterpretableCall and charges the call
// by its function, which keeps the reported cost the cel-go estimate. A node that is not an
// InterpretableV2 is wrapped by cel-go in an adapter that hides them, so each node has Exec.
type budgetedEq struct {
	interpreter.InterpretableCall
	lhs, rhs interpreter.Interpretable
	negate   bool
}

func (e *budgetedEq) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

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
	interpreter.InterpretableCall
	elem, list interpreter.Interpretable
}

func (e *budgetedIn) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

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
	interpreter.InterpretableCall
	list interpreter.Interpretable
}

func (e *budgetedDistinct) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

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

// budgetedJoin is the join of the strings extension. cel-go builds the error for an element
// that is no string from the text of the element, and the text of a list prints every shared
// child, so this node names the type of the element and never prints it.
type budgetedJoin struct {
	interpreter.InterpretableCall
	list interpreter.Interpretable
	sep  interpreter.Interpretable // nil for join()
}

func (e *budgetedJoin) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

func (e *budgetedJoin) Eval(a interpreter.Activation) ref.Val {
	c := e.list.Eval(a)
	if types.IsError(c) {
		return c
	}
	sep := types.String("")
	if e.sep != nil {
		s := e.sep.Eval(a)
		if types.IsError(s) {
			return s
		}
		str, ok := s.(types.String)
		if !ok {
			return types.ValOrErr(s, "no such overload: %v.join(%v)", c.Type(), s.Type())
		}
		sep = str
	}
	list, ok := c.(traits.Lister)
	if !ok {
		return types.ValOrErr(c, "no such overload: %v.join()", c.Type())
	}
	var sb strings.Builder
	n := list.Size().(types.Int)
	for i := types.IntZero; i < n; i++ {
		elem := list.Get(i)
		str, ok := elem.(types.String)
		if !ok {
			return types.WrapErr(errors.Errorf("join: invalid input of type %s", elem.Type().TypeName()))
		}
		// the buffer is checked before it grows, against the bytes that the next writes add: cel-go
		// would build all of it and charge it after. The first element has no separator before it.
		add := len(str)
		if i != 0 {
			add += len(sep)
		}
		if sb.Len()+add > maxValueSize {
			return chargeStop()
		}
		if i != 0 {
			sb.WriteString(string(sep))
		}
		sb.WriteString(string(str))
	}
	return types.String(sb.String())
}

type budgetedFlatten struct {
	interpreter.InterpretableCall
	list  interpreter.Interpretable
	depth interpreter.Interpretable // nil for flatten()
}

func (e *budgetedFlatten) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

func (e *budgetedFlatten) Eval(a interpreter.Activation) ref.Val {
	c := e.list.Eval(a)
	if types.IsError(c) {
		return c
	}
	depth := int64(1)
	if e.depth != nil {
		d := e.depth.Eval(a)
		if types.IsError(d) {
			return d
		}
		n, ok := d.(types.Int)
		if !ok {
			return types.ValOrErr(d, "no such overload: %v.flatten(%v)", c.Type(), d.Type())
		}
		depth = int64(n)
	}
	list, ok := c.(traits.Lister)
	if !ok {
		return types.ValOrErr(c, "no such overload: %v.flatten()", c.Type())
	}
	if depth < 0 {
		return types.WrapErr(errors.New("level must be non-negative"))
	}
	out, ok := budgetOf(a).flatten(list, depth, nil)
	if !ok {
		return compareStop()
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

// flatten gives the elements of l with the lists nested up to depth levels spliced in, as the
// lists extension does, and spends one step for each element it reaches.
func (b *compareBudget) flatten(l traits.Lister, depth int64, out []ref.Val) ([]ref.Val, bool) {
	for it := l.Iterator(); it.HasNext() == types.True; {
		v := it.Next()
		if b.left--; b.left < 0 {
			return nil, false
		}
		nested, isList := v.(traits.Lister)
		if !isList || depth == 0 {
			out = append(out, v)
			continue
		}
		var ok bool
		if out, ok = b.flatten(nested, depth-1, out); !ok {
			return nil, false
		}
	}
	return out, true
}
