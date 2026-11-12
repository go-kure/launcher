package model

import (
	"reflect"
	"strings"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"
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
// element count of its operands. The one exception is the result of sort and sortBy, which
// costs the bytes of the string and bytes elements it holds: a sort compares them by content
// and cel-go counts each as one element. The calls that build a big output from small operands
// check it before they build it: join (equality.go) and lists.range, which cel-go caps itself.
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

// scanCalls are the calls whose result costs the bytes of its elements.
var scanCalls = map[string]bool{"sort": true, "@sortByAssociatedKeys": true}

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
		if x.Function() == "replace" {
			call = &budgetedReplace{InterpretableCall: x}
		}
		return &chargedCall{InterpretableCall: call, scan: scanCalls[x.Function()]}, nil
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
		return &chargedAttr{InterpretableAttribute: x}, nil
	}
	return i, nil
}

// A wrapper embeds the node it wraps, so that the cost tracker of cel-go still reads the
// function, the arguments and the attribute from it and reports the cost cel-go reports.
type chargedCall struct {
	interpreter.InterpretableCall
	scan bool
}

func (c *chargedCall) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return budgetOf(f).charge(c.InterpretableCall.Exec(f), c.scan)
}

func (c *chargedCall) Eval(a interpreter.Activation) ref.Val { return c.Exec(interpreter.AsFrame(a)) }

type chargedAttr struct {
	interpreter.InterpretableAttribute
}

func (c *chargedAttr) Exec(f *interpreter.ExecutionFrame) ref.Val {
	return budgetOf(f).charge(c.InterpretableAttribute.Exec(f), false)
}

// cel-go reads an identifier that is the key of an index, as in m[k], through the Qualify of the
// attribute: the attribute resolves its own value and Exec never runs. The key is hashed at every
// lookup, so it is charged here, as Exec charges a read. Two attributes are charged: one that an
// identifier names, and a conditional of attributes, as in m[c ? k : k]. The planner builds the
// conditional from the bare attributes of its branches, so no wrapper of a branch ever runs.
// The condition runs once more under the charge, and its cost counts twice. A relative
// attribute, such as the key of m[f(x)], holds a call as its operand, and the wrapper of that
// call charges the result, so resolving it again here would run the call twice.
func (c *chargedAttr) Qualify(vars interpreter.Activation, obj any) (any, error) {
	c.chargeKey(vars)
	return c.InterpretableAttribute.Qualify(vars, obj)
}

func (c *chargedAttr) QualifyIfPresent(vars interpreter.Activation, obj any, presenceOnly bool) (any, bool, error) {
	c.chargeKey(vars)
	return c.InterpretableAttribute.QualifyIfPresent(vars, obj, presenceOnly)
}

func (c *chargedAttr) chargeKey(vars interpreter.Activation) {
	if _, ok := c.Attr().(interpreter.NamespacedAttribute); !ok && !isConditionalAttribute(c.Attr()) {
		return
	}
	if key, err := c.InterpretableAttribute.Resolve(vars); err == nil {
		budgetOf(vars).charge(c.Adapter().NativeToValue(key), false)
	}
}

// isConditionalAttribute matches the type by name, as chargeDecorator does for evalWatchAttr,
// because it is unexported. A test row of TestChargeStopsALongKeyOfAnIndex fails on a cel-go
// version that renames it.
func isConditionalAttribute(a interpreter.Attribute) bool {
	return reflect.TypeOf(a).String() == "*interpreter.conditionalAttribute"
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
