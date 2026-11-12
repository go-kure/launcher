package model

import (
	"strings"

	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/interpreter"

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
// text of the whole tree. The first four are every caller of Equal in cel-go v0.33.2 that a model reaches, read in
// common/types (Contains, behind `in`), ext (distinct) and interpreter (the == and != nodes).
// flatten is the one list function of the pinned extension that copies nested elements under the
// legacy cost tracker (ext/lists.go, trackListFlattenLegacy). A new cel-go version, or a new
// version of a pinned extension library, can add a call. TestComparisonCallSetIsReadFromCelGo
// fails on any other version, so that someone reads the calls again when cel-go moves.
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
			// the key is a visit like any other, and a missing key costs a step too
			if b.left--; b.left < 0 {
				return nil, false
			}
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
	switch call.Function() {
	case operators.Equals, operators.NotEquals:
		return &budgetedEq{InterpretableCall: call, negate: call.Function() == operators.NotEquals}, nil
	case operators.In:
		return &budgetedIn{call}, nil
	case "distinct":
		return &budgetedDistinct{call}, nil
	case "join":
		return &budgetedJoin{call}, nil
	case "flatten":
		return &budgetedFlatten{call}, nil
	}
	return i, nil
}

// execCall is the Exec of a replacement node. It evaluates the operands of call in order, with
// Exec on the frame so that each reports its own cost, and returns the first error as cel-go
// does. It runs body on the values and reports the call to the cost tracker of the frame.
//
// cel-go v0.33 has no observer that reads a node's function and arguments after it ran: each
// node reports its own cost from inside its own Exec (interpreter/interpretable.go, evalUnary,
// evalBinary and evalVarArgs), so a node that does not run cel-go's Exec reports nothing, and
// the evaluation reads a cost of 0. An operand that fails returns before the report, as in
// cel-go. A refusal by panic does too, because the evaluation ends there.
func execCall(f *interpreter.ExecutionFrame, call interpreter.InterpretableCall, body func(vals []ref.Val) ref.Val) ref.Val {
	args := call.Args()
	vals := make([]ref.Val, len(args))
	for i, arg := range args {
		if vals[i] = arg.Exec(f); types.IsError(vals[i]) {
			return vals[i]
		}
	}
	res := body(vals)
	reportCost(f, call, vals, res)
	return res
}

// reportCost gives the cost tracker of the frame the call that the node replaced, with the method
// of cel-go's node for that number of operands (a call of three or more is a vararg call).
func reportCost(f *interpreter.ExecutionFrame, call interpreter.InterpretableCall, vals []ref.Val, res ref.Val) {
	costs := f.CostTracker()
	if costs == nil {
		return
	}
	switch len(vals) {
	case 0:
		costs.EvalZeroArity(f, call.ID(), call, res)
	case 1:
		costs.EvalUnary(f, call.ID(), call, vals[0], res)
	case 2:
		costs.EvalBinary(f, call.ID(), call, vals[0], vals[1], res)
	default:
		costs.EvalVarArgs(f, call.ID(), call, vals, res)
	}
}

// A replacement node embeds the call it replaces, so that it keeps ID, Function, OverloadID
// and Args, which the cost tracker reads to charge the call by its function. Each node has Exec,
// which runs the node and reports its cost, and an Eval that runs Exec on a frame.
type budgetedEq struct {
	interpreter.InterpretableCall
	negate bool
}

func (e *budgetedEq) Eval(a interpreter.Activation) ref.Val {
	return interpreter.EvalActivation(a, e.Exec)
}

// Exec reports the cost of an operand that fails as cel-go's == and != nodes do (a legacy
// behaviour of cel-go, evalEq and evalNe), which no other call does.
func (e *budgetedEq) Exec(f *interpreter.ExecutionFrame) ref.Val {
	args := e.Args()
	l := args[0].Exec(f)
	if types.IsError(l) {
		reportCost(f, e, []ref.Val{l, types.UnknownType}, l)
		return l
	}
	r := args[1].Exec(f)
	if types.IsError(r) {
		reportCost(f, e, []ref.Val{l, r}, r)
		return r
	}
	var res ref.Val
	v, ok := budgetOf(f).equal(l, r)
	switch {
	case !ok:
		res = compareStop() // an error value, so the call still reports its cost
	case e.negate:
		res = types.Bool(v != types.True)
	default:
		res = v
	}
	reportCost(f, e, []ref.Val{l, r}, res)
	return res
}

type budgetedIn struct{ interpreter.InterpretableCall }

func (e *budgetedIn) Eval(a interpreter.Activation) ref.Val {
	return interpreter.EvalActivation(a, e.Exec)
}

func (e *budgetedIn) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return execCall(f, e, func(vals []ref.Val) ref.Val { return e.run(f, vals) })
}

func (e *budgetedIn) run(f *interpreter.ExecutionFrame, vals []ref.Val) ref.Val {
	x, c := vals[0], vals[1]
	list, ok := c.(traits.Lister)
	if !ok {
		if c.Type().HasTrait(traits.ContainerType) {
			return c.(traits.Container).Contains(x)
		}
		return types.ValOrErr(c, "no such overload")
	}
	b := budgetOf(f)
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

type budgetedDistinct struct{ interpreter.InterpretableCall }

func (e *budgetedDistinct) Eval(a interpreter.Activation) ref.Val {
	return interpreter.EvalActivation(a, e.Exec)
}

func (e *budgetedDistinct) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return execCall(f, e, func(vals []ref.Val) ref.Val { return e.run(f, vals) })
}

func (e *budgetedDistinct) run(f *interpreter.ExecutionFrame, vals []ref.Val) ref.Val {
	c := vals[0]
	list, ok := c.(traits.Lister)
	if !ok {
		return types.ValOrErr(c, "no such overload")
	}
	b := budgetOf(f)
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
type budgetedJoin struct{ interpreter.InterpretableCall }

func (e *budgetedJoin) Eval(a interpreter.Activation) ref.Val {
	return interpreter.EvalActivation(a, e.Exec)
}

func (e *budgetedJoin) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return execCall(f, e, func(vals []ref.Val) ref.Val { return e.run(vals) })
}

// run joins vals[0] with the separator vals[1], which join() does not have.
func (e *budgetedJoin) run(vals []ref.Val) ref.Val {
	c := vals[0]
	sep := types.String("")
	if len(vals) == 2 {
		s := vals[1]
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
	n := list.Size().(types.Int)
	// every element is checked before the first byte is written: a join that fails after it copied
	// a long prefix would be charged for none of it, and `|| true` hides the failure
	for i := types.IntZero; i < n; i++ {
		elem := list.Get(i)
		if _, ok := elem.(types.String); !ok {
			return types.WrapErr(errors.Errorf("join: invalid input of type %s", elem.Type().TypeName()))
		}
	}
	var sb strings.Builder
	for i := types.IntZero; i < n; i++ {
		str := list.Get(i).(types.String)
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

type budgetedFlatten struct{ interpreter.InterpretableCall }

func (e *budgetedFlatten) Eval(a interpreter.Activation) ref.Val {
	return interpreter.EvalActivation(a, e.Exec)
}

func (e *budgetedFlatten) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return execCall(f, e, func(vals []ref.Val) ref.Val { return e.run(f, vals) })
}

// run flattens vals[0] to the depth vals[1], which flatten() does not have.
func (e *budgetedFlatten) run(f *interpreter.ExecutionFrame, vals []ref.Val) ref.Val {
	c := vals[0]
	depth := int64(1)
	if len(vals) == 2 {
		d := vals[1]
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
	out, ok := budgetOf(f).flatten(list, depth, nil)
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
