package model

import (
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"

	"github.com/go-kure/launcher/pkg/errors"
)

// The class of this file is the one of equality.go: a cel-go path must be charged by the size
// of the value it walks or builds, not by the node that holds it (go-kure/launcher#1461).
// cel-go charges a call by the length of its operands and observes the cost after the call
// returns, so a value that many places share, or a call that builds a big output from small
// operands, ran below the cost limit.
//
// chargeDecorator closes the class with one rule. Every call result and every identifier read
// is charged against the budget of the comparisons: a string or bytes value costs one step for
// each 1,024 bytes, and no string, bytes value, list or map may hold more than maxValueSize
// bytes or elements. A list or map costs nothing more, because cel-go charges a call by the
// element count of its operands. The one exception is the keys of sort and sortBy, which cost
// the bytes of the string and bytes elements they hold: a sort compares them by content and
// cel-go counts each as one element. The charge comes before the sort runs, so a sort that
// fails after it compared is paid for too. The calls that build a big output from small operands
// check it before they build it: join (equality.go), replace and lists.range, which cel-go caps
// itself. indexOf and lastIndexOf check the work of their two nested loops before they run.
// testdata/celenv1-charges.txt names a class for every function of the pinned environment.
//
// `&&`, `||`, the conditional and a comprehension are no calls, so no node of them is wrapped and
// they stay lazy: a wrapped result is charged only when its node ran.

// maxValueSize is the largest string or bytes value in bytes, and the largest list or map in
// elements, that one value may hold. It is the cost limit of one hole.
const maxValueSize = maxExpressionCost

// chargeStopMessage is the text of a stop by the charge. siteError reads it.
const chargeStopMessage = "a value holds more than 1000000 bytes or elements, or the reads of values spent the budget"

// chargeStop ends the evaluation at once, as cel-go does for its own cost limit
// (interpreter/runtimecost.go: an EvalCancelledError raised from an observer). cel-go recovers
// the panic in (*prog).Eval (cel/program.go) and returns it as the error of Eval, so no panic
// leaves the evaluation. An error value would not stop it: all() keeps reading after an error.
func chargeStop() ref.Val {
	panic(interpreter.EvalCancelledError{Cause: interpreter.CostLimitExceeded, Message: chargeStopMessage})
}

// charge spends the size of v on the budget and stops the evaluation when v is too large or
// the budget is gone. scan adds the bytes of the string and bytes elements of a list.
func (b *compareBudget) charge(v ref.Val, scan bool) ref.Val {
	n := 0
	switch x := v.(type) {
	case types.String:
		n = len(x)
	case types.Bytes:
		n = len(x)
	case traits.Lister:
		sz, ok := x.Size().(types.Int)
		if !ok {
			return v
		}
		if sz > maxValueSize {
			return chargeStop()
		}
		if scan {
			for i := types.IntZero; i < sz; i++ {
				n += scalarSize(x.Get(i))
			}
		}
	case traits.Mapper:
		if sz, ok := x.Size().(types.Int); ok && sz > maxValueSize {
			return chargeStop()
		}
		return v
	default:
		return v
	}
	if n > maxValueSize && !scan {
		return chargeStop()
	}
	if b.left -= n / compareBytesPerStep; b.left < 0 {
		return chargeStop()
	}
	return v
}

// chargeDecorator wraps every call and every identifier read, so that the result is charged.
func chargeDecorator(i interpreter.Interpretable) (interpreter.Interpretable, error) {
	switch x := i.(type) {
	case interpreter.InterpretableCall:
		var call interpreter.InterpretableCall = x
		switch x.Function() {
		case "replace":
			call = &budgetedReplace{InterpretableCall: x}
		case "sort", "@sortByAssociatedKeys":
			call = &budgetedSort{InterpretableCall: x}
		case "indexOf", "lastIndexOf":
			call = &budgetedSearch{InterpretableCall: x}
		}
		return &chargedCall{InterpretableCall: call}, nil
	case interpreter.InterpretableAttribute:
		// The planner decorates a relative attribute twice, once when it makes the node and once
		// when it returns it. cel-go's own observer skips a node that it already watches, and a
		// wrapper around that node would hide it, so the observer would watch the node twice and
		// report one more step of cost. The node already holds the wrapper made at the first pass.
		// The type is matched by name because it is unexported. The guard is three rows of
		// TestComparisonGivesWhatCelGoGives, `[1, 1.0][0] == [1, 1.0][1]`, `[1u, 1][0] == [1u, 1][1]`
		// and `[[1], {'a': 1}][0] == [[1], {'a': 1}][1]`: without the skip they report a cost of 27,
		// 27 and 107 against 25, 25 and 105. A cel-go version that renames the type fails those rows.
		if reflect.TypeOf(i).String() == "*interpreter.evalWatchAttr" {
			return i, nil
		}
		return &chargedAttr{InterpretableAttribute: x, fac: interpreter.NewAttributeFactory(nil, x.Adapter(), nil)}, nil
	}
	return i, nil
}

// A wrapper embeds the node it wraps, so that the cost tracker of cel-go still reads the
// function, the arguments and the attribute from it and reports the cost cel-go reports.
type chargedCall struct {
	interpreter.InterpretableCall
}

func (c *chargedCall) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return budgetOf(f).charge(c.InterpretableCall.Exec(f), false)
}

func (c *chargedCall) Eval(a interpreter.Activation) ref.Val { return c.Exec(interpreter.AsFrame(a)) }

type chargedAttr struct {
	interpreter.InterpretableAttribute
	fac interpreter.AttributeFactory // makes the qualifier of a key; the type of the object is not read
}

func (c *chargedAttr) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return budgetOf(f).charge(c.InterpretableAttribute.Exec(f), false)
}

// cel-go reads the key of an index, as in m[k], through the Qualify of the key attribute, and never
// through its Exec: attrQualify (interpreter/attributes.go) resolves the attribute, makes a qualifier
// of the value and applies it. The key is hashed at every lookup, so it is charged here, as Exec
// charges a read. This is the same three steps as attrQualify with one charge between them, so
// the key is resolved once and the cost cel-go reports does not change. Every attribute kind
// qualifies this way: an identifier, a conditional of attributes such as m[c ? k : k] (the planner
// builds it from the bare attributes of its branches, so no wrapper of a branch ever runs) and
// a relative attribute such as m[(xs + [])[0]] (the call wrapper charges the list, never the
// string that is picked from it).
func (c *chargedAttr) Qualify(vars interpreter.Activation, obj any) (any, error) {
	qual, err := c.keyQualifier(vars)
	if err != nil {
		return nil, err
	}
	return qual.Qualify(vars, obj)
}

func (c *chargedAttr) QualifyIfPresent(vars interpreter.Activation, obj any, presenceOnly bool) (any, bool, error) {
	qual, err := c.keyQualifier(vars)
	if err != nil {
		return nil, false, err
	}
	return qual.QualifyIfPresent(vars, obj, presenceOnly)
}

func (c *chargedAttr) keyQualifier(vars interpreter.Activation) (interpreter.Qualifier, error) {
	key, err := c.InterpretableAttribute.Resolve(vars)
	if err != nil {
		return nil, err
	}
	budgetOf(vars).charge(c.Adapter().NativeToValue(key), false)
	return c.fac.NewQualifier(nil, c.ID(), key, c.IsOptional())
}

func (c *chargedAttr) Eval(a interpreter.Activation) ref.Val { return c.Exec(interpreter.AsFrame(a)) }

// budgetedReplace is replace with the size of its result checked before it is built. cel-go calls
// strings.Replace and charges the result after it exists, so two operands of 100,000 and 2,000
// bytes built 200,000,000 bytes. The result has the length of the string plus, for each replaced
// occurrence, the length of the new text less the length of the old. The node evaluates its own
// operands, as budgetedJoin does, and gives the result that cel-go gives.
type budgetedReplace struct {
	interpreter.InterpretableCall
}

func (e *budgetedReplace) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

func (e *budgetedReplace) Eval(a interpreter.Activation) ref.Val {
	args := e.Args()
	vals := make([]ref.Val, len(args))
	for i, arg := range args {
		if vals[i] = arg.Eval(a); types.IsError(vals[i]) {
			return vals[i]
		}
	}
	str, ok1 := vals[0].(types.String)
	old, ok2 := vals[1].(types.String)
	repl, ok3 := vals[2].(types.String)
	n := types.Int(-1)
	ok4 := true
	if len(vals) == 4 {
		n, ok4 = vals[3].(types.Int)
	}
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return types.NewErr("no such overload: %v.replace", vals[0].Type())
	}
	count := strings.Count(string(str), string(old))
	if n >= 0 && types.Int(count) > n {
		count = int(n)
	}
	if len(str)+count*(len(repl)-len(old)) > maxValueSize {
		return chargeStop()
	}
	return types.String(strings.Replace(string(str), string(old), string(repl), int(n)))
}

// budgetedSearch is indexOf and lastIndexOf with the work of the search checked before it runs.
// cel-go runs two nested loops over the runes of the string and of the search text and charges the
// product of the two lengths at a tenth of a step each, but it observes that cost only after the
// call returns, so a string of 1,000,000 bytes and a search text of 500,000 ran 250 billion
// comparisons before it stopped. The node evaluates its own operands, as budgetedReplace does,
// refuses the search that cel-go would refuse once it had run, and gives the result that cel-go
// gives (ext/strings.go, indexOfOffset and lastIndexOfOffset). It covers these two functions only:
// the general mechanism belongs to the redesign of the charge in go-kure/launcher#1461.
type budgetedSearch struct {
	interpreter.InterpretableCall
}

func (e *budgetedSearch) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

func (e *budgetedSearch) Eval(a interpreter.Activation) ref.Val {
	args := e.Args()
	vals := make([]ref.Val, len(args))
	for i, arg := range args {
		if vals[i] = arg.Eval(a); types.IsError(vals[i]) {
			return vals[i]
		}
	}
	str, ok1 := vals[0].(types.String)
	sub, ok2 := vals[1].(types.String)
	last := e.Function() == "lastIndexOf"
	off := types.Int(0)
	ok3 := true
	if len(vals) == 3 {
		off, ok3 = vals[2].(types.Int)
	} else if last {
		off = types.Int(utf8.RuneCountInString(string(str)) - 1)
	}
	if !ok1 || !ok2 || !ok3 {
		return types.NewErr("no such overload: %v.%s", vals[0].Type(), e.Function())
	}
	runes, subrunes := utf8.RuneCountInString(string(str)), utf8.RuneCountInString(string(sub))
	if float64(runes)*float64(subrunes)*common.StringTraversalCostFactor > maxExpressionCost {
		panic(interpreter.EvalCancelledError{Cause: interpreter.CostLimitExceeded, Message: "a search over more characters than the cost limit allows"})
	}
	if last && len(vals) == 2 {
		// without an offset, cel-go answers the empty text and a text longer than the string first
		if len(sub) == 0 {
			return types.Int(runes)
		}
		if len(str) < len(sub) {
			return types.IntNegOne
		}
	}
	return searchRunes([]rune(string(str)), []rune(string(sub)), int(off), last)
}

// searchRunes is the result of indexOf, or of lastIndexOf when last is set, from the offset off,
// and the errors that cel-go gives.
func searchRunes(runes, subrunes []rune, off int, last bool) ref.Val {
	if off < 0 {
		return types.NewErr("index out of range: %d", off)
	}
	if len(subrunes) == 0 {
		// the empty text matches at the offset, clamped to the end of the string
		if off > len(runes) {
			return types.Int(len(runes))
		}
		return types.Int(off)
	}
	if off >= len(runes) {
		return types.IntNegOne
	}
	matches := func(i int) bool {
		for j, r := range subrunes {
			if runes[i+j] != r {
				return false
			}
		}
		return true
	}
	if last {
		for i := min(off, len(runes)-len(subrunes)); i >= 0; i-- {
			if matches(i) {
				return types.Int(i)
			}
		}
		return types.IntNegOne
	}
	for i := off; i < len(runes)-(len(subrunes)-1); i++ {
		if matches(i) {
			return types.Int(i)
		}
	}
	return types.IntNegOne
}

// budgetedSort is sort and sortBy with the keys charged before they are compared. cel-go
// compares the keys by content until it finds one that cannot be compared and then fails, and
// the charge of a result never sees a sort that failed, so `xs.sort() == [] || true` on a list
// of two long strings and an int compared both strings for no step. The node evaluates its own
// operands, as budgetedJoin does, charges the bytes of the keys, and sorts as cel-go does
// (ext/lists.go, sortListByAssociatedKeys). For sort the keys are the list itself, and for
// sortBy they are the second operand.
type budgetedSort struct {
	interpreter.InterpretableCall
}

func (e *budgetedSort) Exec(f *interpreter.ExecutionFrame) ref.Val { return e.Eval(f) }

func (e *budgetedSort) Eval(a interpreter.Activation) ref.Val {
	args := e.Args()
	vals := make([]ref.Val, len(args))
	for i, arg := range args {
		if vals[i] = arg.Eval(a); types.IsError(vals[i]) {
			return vals[i]
		}
	}
	list, ok := vals[0].(traits.Lister)
	keys := list
	if len(vals) == 2 {
		keys, ok = vals[1].(traits.Lister)
		if _, first := vals[0].(traits.Lister); !first {
			ok = false
		}
	}
	if !ok {
		return types.NewErr("no such overload: %v.%s", vals[0].Type(), e.Function())
	}
	budgetOf(a).charge(keys, true)
	return sortByKeys(list, keys)
}

// sortByKeys gives the list in the order of its keys and the errors that cel-go gives.
func sortByKeys(list, keys traits.Lister) ref.Val {
	listLength := list.Size().(types.Int)
	keysLength := keys.Size().(types.Int)
	if listLength != keysLength {
		return types.NewErr("@sortByAssociatedKeys() expected a list of the same size as the associated keys list, but got %d and %d elements respectively", listLength, keysLength)
	}
	if listLength == 0 {
		return list
	}
	elem := keys.Get(types.IntZero)
	if _, ok := elem.(traits.Comparer); !ok {
		return types.NewErr("list elements must be comparable")
	}
	sortedIndices := make([]ref.Val, 0, listLength)
	for i := types.IntZero; i < listLength; i++ {
		sortedIndices = append(sortedIndices, i)
	}
	var err error
	sort.Slice(sortedIndices, func(i, j int) bool {
		iKey := keys.Get(sortedIndices[i])
		jKey := keys.Get(sortedIndices[j])
		if iKey.Type() != elem.Type() || jKey.Type() != elem.Type() {
			err = errors.New("list elements must have the same type")
			return false
		}
		return iKey.(traits.Comparer).Compare(jKey) == types.IntNegOne
	})
	if err != nil {
		return types.WrapErr(err)
	}
	sorted := make([]ref.Val, 0, listLength)
	for _, sortedIdx := range sortedIndices {
		sorted = append(sorted, list.Get(sortedIdx))
	}
	return types.DefaultTypeAdapter.NativeToValue(sorted)
}
