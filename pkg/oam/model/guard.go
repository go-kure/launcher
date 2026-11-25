package model

import (
	"math"
	"strings"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
)

// A part that has a when is emitted only when the when is true, so a read of it is safe only where
// the when is known to hold. Two places know it: the true branch of a conditional whose test holds
// every top-level && term of the when, and any site of a part whose own when holds them. The check
// is made when the model compiles, so a model that registers is safe for every input set.

// condPart is an earlier part with a when and no forEach, as the walker records it.
type condPart struct {
	terms    []celast.Expr // the top-level && terms of its when
	nullable bool          // its when can give null: an optional input read as it is
	pure     bool          // its terms read only the variables of the model (pureTerms)
}

// formScope is what the form scan knows about the parts: the names that are not readable yet, the
// parts that have a when, and the ones whose when the reading part's own when already holds.
type formScope struct {
	notYet  map[string]bool
	conds   map[string]condPart
	allowed map[string]bool
}

// scanState is the position of the scan: hidesValues and hidesParts are true inside the scope of a
// local variable of that name, which hides the model's own, inComp inside a comprehension or a bind,
// where a local variable can hide a name of a test, and guarded holds the parts whose when holds here.
type scanState struct {
	hidesValues bool
	hidesParts  bool
	inComp      bool
	guarded     map[string]bool
}

// refusePartRead says why a read of parts.<id> is refused at st, or returns the empty string.
func (f *formScope) refusePartRead(id string, st scanState) string {
	if f.notYet["parts."+id] {
		return "parts." + id + " is not readable here: a part reads only an earlier part, and never one with forEach"
	}
	c, ok := f.conds[id]
	if !ok {
		return ""
	}
	if !c.pure {
		return "parts." + id + " is not readable here: its when reads a name other than inputs, self, values and parts, so no test can be shown to be the same condition"
	}
	if !st.guarded[id] && !f.allowed[id] {
		return "parts." + id + " is not readable here: it has a when, so read it only in the true branch of a conditional whose test holds that when, or in a part whose own when holds it: for a whole value write (cond ? parts." + id + ".name : null), and for text around a hole write (cond ? parts." + id + ".name : 'none'), which gives 'none' when the part is skipped, because null in text is an error"
	}
	if c.nullable {
		return "parts." + id + " has a when that can be null (an optional input): write it as a comparison, such as inputs.x == true, so the test is true or false"
	}
	return ""
}

// guardedBy returns guarded with every part added whose when the test holds.
func (f *formScope) guardedBy(guarded map[string]bool, test celast.Expr) map[string]bool {
	have := andTerms(test)
	out := guarded
	for id, c := range f.conds {
		if guarded[id] || !holdsAll(have, c.terms) {
			continue
		}
		if len(out) == len(guarded) {
			out = make(map[string]bool, len(guarded)+1)
			for k := range guarded {
				out[k] = true
			}
		}
		out[id] = true
	}
	return out
}

// allowedBy returns the parts whose when the terms of a reading part's own when hold: the reading
// part is emitted only when they are.
func (f *formScope) allowedBy(have []celast.Expr) map[string]bool {
	var out map[string]bool
	for id, c := range f.conds {
		if holdsAll(have, c.terms) {
			if out == nil {
				out = map[string]bool{}
			}
			out[id] = true
		}
	}
	return out
}

// andTerms is the top-level && terms of e: the operands of a chain of &&, whatever its shape.
func andTerms(e celast.Expr) []celast.Expr {
	if e.Kind() == celast.CallKind && e.AsCall().FunctionName() == "_&&_" {
		var out []celast.Expr
		for _, a := range e.AsCall().Args() {
			out = append(out, andTerms(a)...)
		}
		return out
	}
	return []celast.Expr{e}
}

// holdsAll tells whether every want has an equal term in have.
func holdsAll(have, want []celast.Expr) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if sameExpr(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sameExpr tells whether two parsed expressions are the same text of CEL, ignoring ids and
// parentheses. Only identifiers, literals, selects, calls and lists compare; a comprehension, a map
// or a struct never equals anything, so a when built from one is no guard. The check stays on the
// side of refusing.
func sameExpr(a, b celast.Expr) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case celast.IdentKind:
		return a.AsIdent() == b.AsIdent()
	case celast.LiteralKind:
		// 1 and 1.0 are equal in CEL and are not the same text: the type decides what an operator does.
		// The same holds for 0.0 and -0.0, which divide to different infinities.
		x, y := a.AsLiteral(), b.AsLiteral()
		if x.Type().TypeName() != y.Type().TypeName() {
			return false
		}
		if fx, ok := x.Value().(float64); ok {
			fy, _ := y.Value().(float64)
			return math.Float64bits(fx) == math.Float64bits(fy)
		}
		return x.Equal(y) == types.True
	case celast.SelectKind:
		x, y := a.AsSelect(), b.AsSelect()
		return x.FieldName() == y.FieldName() && x.IsTestOnly() == y.IsTestOnly() && sameExpr(x.Operand(), y.Operand())
	case celast.CallKind:
		x, y := a.AsCall(), b.AsCall()
		if x.FunctionName() != y.FunctionName() || x.IsMemberFunction() != y.IsMemberFunction() || len(x.Args()) != len(y.Args()) {
			return false
		}
		if x.IsMemberFunction() && !sameExpr(x.Target(), y.Target()) {
			return false
		}
		return sameExprs(x.Args(), y.Args())
	case celast.ListKind:
		x, y := a.AsList(), b.AsList()
		return len(x.OptionalIndices()) == 0 && len(y.OptionalIndices()) == 0 && sameExprs(x.Elements(), y.Elements())
	case celast.UnspecifiedExprKind, celast.MapKind, celast.StructKind, celast.ComprehensionKind:
		// Never equal: see above.
	}
	return false
}

func sameExprs(a, b []celast.Expr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameExpr(a[i], b[i]) {
			return false
		}
	}
	return true
}

// nullScope is what mayBeNull knows of the names an expression reads: the declared type of each
// variable, and for a value of the model whether its own expression can give null. The checker types
// `inputs.db ? true : inputs.opt` as a plain bool, so the type of a value says nothing about null.
type nullScope struct {
	declared map[string]*cel.Type
	values   map[string]bool
}

// mayBeNull tells whether the value of e can be null, which a build reads as false for a when and
// a conditional cannot read at all. It fails closed: only a bool literal, a required or defaulted
// input of a declared non-null type, a value whose expression is not null, a conditional of such,
// and an operator or a string test that always gives a bool are known not to be null. A comparison
// with null is such an operator.
func (n nullScope) mayBeNull(e celast.Expr) bool {
	switch e.Kind() {
	case celast.LiteralKind:
		return e.AsLiteral() == types.NullValue
	case celast.IdentKind, celast.SelectKind:
		name, ok := qualifiedName(e)
		if null, isValue := n.values[name]; isValue {
			return null
		}
		t := n.declared[name]
		return !ok || t == nil || t.IsAssignableType(cel.NullType)
	case celast.CallKind:
		call := e.AsCall()
		args := call.Args()
		switch call.FunctionName() {
		case "_?_:_":
			return len(args) != 3 || n.mayBeNull(args[1]) || n.mayBeNull(args[2])
		case "_&&_", "_||_", "!_", "_==_", "_!=_", "_<_", "_<=_", "_>_", "_>=_", "@in",
			"startsWith", "endsWith", "contains", "matches":
			return false
		}
	case celast.UnspecifiedExprKind, celast.ListKind, celast.MapKind, celast.StructKind, celast.ComprehensionKind:
		// Not known to give a bool that is never null.
	}
	return true
}

// qualifiedName reads an identifier, or a chain of selects on one, as the dotted name the checker
// declares a variable under: `inputs.db`.
func qualifiedName(e celast.Expr) (string, bool) {
	switch e.Kind() {
	case celast.IdentKind:
		return strings.TrimPrefix(e.AsIdent(), "."), true
	case celast.SelectKind:
		sel := e.AsSelect()
		if sel.IsTestOnly() {
			return "", false
		}
		op, ok := qualifiedName(sel.Operand())
		return op + "." + sel.FieldName(), ok
	case celast.UnspecifiedExprKind, celast.CallKind, celast.LiteralKind, celast.ListKind, celast.MapKind, celast.StructKind, celast.ComprehensionKind:
		// Not a name.
	}
	return "", false
}

// holeRoot parses the one hole of src and returns its expression, as the form scan reads it.
func holeRoot(env *cel.Env, src string) (celast.Expr, error) {
	inner, err := scanHole(src)
	if err != nil {
		return nil, err
	}
	parsed, iss := env.Parse(inner)
	if iss != nil && iss.Err() != nil {
		return nil, iss.Err()
	}
	return parsed.NativeRep().Expr(), nil
}

// pureTerms tells whether every name the terms read is a variable of the model: inputs, self, values
// or parts, which a loop variable may not hide. A term that reads any other name, such as `bool`, which
// CEL reads as the type unless a loop variable of that name hides it, may mean one thing in the part
// that owns the when and another in a reader, so no test can be shown to be the same condition.
func pureTerms(terms []celast.Expr) bool {
	pure := true
	for _, t := range terms {
		celast.PreOrderVisit(t, celast.NewExprVisitor(func(e celast.Expr) {
			switch e.Kind() {
			case celast.IdentKind:
				if !scopeNames[strings.SplitN(strings.TrimPrefix(e.AsIdent(), "."), ".", 2)[0]] {
					pure = false
				}
			case celast.ComprehensionKind, celast.MapKind, celast.StructKind, celast.UnspecifiedExprKind:
				pure = false
			case celast.LiteralKind, celast.SelectKind, celast.CallKind, celast.ListKind:
			}
		}))
	}
	return pure
}
