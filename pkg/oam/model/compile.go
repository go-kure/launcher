package model

import (
	"sort"
	"strconv"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"

	"github.com/go-kure/launcher/pkg/errors"
)

// compiledModel is the result of compiling the expressions of one model. In 2a it
// records the CEL types the expressions give. Evaluation comes later.
type compiledModel struct {
	// valueTypes maps a value name to the type of its expression.
	valueTypes map[string]*cel.Type
	// elemTypes maps a part id to the element type of its forEach.in list.
	elemTypes map[string]*cel.Type
}

// valueType returns the CEL type of a compiled value, as text. It is empty for an
// unknown value.
func (c *compiledModel) valueType(name string) string {
	if t := c.valueTypes[name]; t != nil {
		return t.String()
	}
	return ""
}

// elemType returns the CEL element type of a part's forEach.in, as text. It is empty
// for a part without forEach.
func (c *compiledModel) elemType(partID string) string {
	if t := c.elemTypes[partID]; t != nil {
		return t.String()
	}
	return ""
}

// compileModel compiles every expression of a loaded model in document order and
// returns the first failure, which wraps ErrInvalid and names the field. It does not
// evaluate anything.
//
// The scope of an expression is the inputs, `self.name` and `self.application`, and the
// values that come before the expression in the document. The fields `when` and
// `forEach.in` see every value. They do not see a loop variable.
func compileModel(m *Model) (*compiledModel, error) {
	env, err := newEnv(scopeVariables(m)...)
	if err != nil {
		return nil, err
	}
	cm := &compiledModel{valueTypes: map[string]*cel.Type{}, elemTypes: map[string]*cel.Type{}}

	declared := scopeTypes(m)
	notYet := make(map[string]bool, len(m.Spec.Values))
	for _, v := range m.Spec.Values {
		notYet[v.Name] = true
	}
	for i, v := range m.Spec.Values {
		path := "spec.values[" + strconv.Itoa(i) + "].expr"
		out, err := compileHole(env, path, v.Expr, notYet, declared)
		if err != nil {
			return nil, err
		}
		cm.valueTypes[v.Name] = out
		declared["values."+v.Name] = out
		delete(notYet, v.Name)
		if env, err = env.Extend(cel.Variable("values."+v.Name, out)); err != nil {
			return nil, errors.Wrapf(err, "model: declare value %q", v.Name)
		}
	}

	for i, p := range m.Spec.Parts {
		base := "spec.parts[" + strconv.Itoa(i) + "]"
		if p.When != "" {
			out, err := compileHole(env, base+".when", p.When, nil, declared)
			if err != nil {
				return nil, err
			}
			if out.Kind() != types.BoolKind {
				return nil, invalid(base+".when", "must give a bool, got %s", out)
			}
		}
		if p.ForEach != nil {
			out, err := compileHole(env, base+".forEach.in", p.ForEach.In, nil, declared)
			if err != nil {
				return nil, err
			}
			if out.Kind() != types.ListKind {
				return nil, invalid(base+".forEach.in", "must give a list, got %s", out)
			}
			cm.elemTypes[p.ID] = out.Parameters()[0]
		}
	}
	return cm, nil
}

// scopeTypes maps `inputs.<name>`, `self.name` and `self.application` to their CEL
// types. A qualified name is one variable, so a comprehension over `inputs.tags` sees
// the type of the schema.
func scopeTypes(m *Model) map[string]*cel.Type {
	declared := make(map[string]*cel.Type, len(m.Spec.Inputs)+2)
	for name, schema := range m.Spec.Inputs {
		declared["inputs."+name] = inputType(schema)
	}
	declared["self.name"] = cel.StringType
	declared["self.application"] = cel.StringType
	return declared
}

// scopeVariables declares the variables of scopeTypes. The names are sorted so the
// environment does not depend on map order.
func scopeVariables(m *Model) []cel.EnvOption {
	return variables(scopeTypes(m))
}

// variables declares each name of declared, in sorted order.
func variables(declared map[string]*cel.Type) []cel.EnvOption {
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	vars := make([]cel.EnvOption, 0, len(names))
	for _, name := range names {
		vars = append(vars, cel.Variable(name, declared[name]))
	}
	return vars
}

// compileHole compiles the one `${ }` hole in src and returns the type it gives.
// notYet holds the names of the values that are not computed at this site. declared
// maps each variable name to its declared type.
//
// The checker rebuilds a list or map type without its nullable wrapper, so the output
// type of `inputs.tags` is a plain list. An expression that is exactly one reference
// to a declared variable therefore gives the declared type, which keeps the null.
// Every other expression gives the type the checker reports.
func compileHole(env *cel.Env, path, src string, notYet map[string]bool, declared map[string]*cel.Type) (*cel.Type, error) {
	inner, err := scanHole(src)
	if err != nil {
		return nil, invalid(path, "%s", err)
	}
	_, out, err := checkHole(env, path, inner, &formScope{notYet: notYet}, declared)
	return out, err
}

// checkHole parses, form-checks and type-checks the CEL source inner of one hole. It
// returns the checked expression, for a program, and the type as compileHole defines it.
func checkHole(env *cel.Env, path, inner string, forms *formScope, declared map[string]*cel.Type) (*cel.Ast, *cel.Type, error) {
	parsed, iss := env.Parse(inner)
	if iss != nil && iss.Err() != nil {
		return nil, nil, invalid(path, "%s", iss.Err())
	}
	if msg := refuseForm(parsed, forms); msg != "" {
		return nil, nil, invalid(path, "%s", msg)
	}
	checked, iss := env.Check(parsed)
	if iss != nil && iss.Err() != nil {
		return nil, nil, invalid(path, "%s", iss.Err())
	}
	if root := checked.NativeRep().Expr(); root.Kind() == celast.IdentKind {
		if t := declared[root.AsIdent()]; t != nil {
			return checked, t, nil
		}
	}
	return checked, checked.OutputType(), nil
}

// refuseForm reads a parsed expression for two forms that the type check cannot name.
// It returns the first reason, or the empty string.
//
// has() is refused. An input is a qualified variable and not a map field, so has() has
// nothing to test. A model tests an optional input with `== null`, and a key of an object
// input with `'key' in inputs.obj`.
//
// A read of a value that is not computed yet (a later value, or the value itself) would
// otherwise fail as an undeclared `values`, which hides the cause. The walk follows the
// scope of a bind or comprehension variable named `values`, which hides the model's
// values the way the checker does: the range and the initializer of a comprehension are
// outside it, the condition, the step and (for the accumulator) the result are inside.
//
// A read of an earlier part that has a when is refused unless the when holds there (guard.go).
func refuseForm(parsed *cel.Ast, forms *formScope) string {
	return scanForm(parsed.NativeRep().Expr(), scanState{}, forms)
}

// scanForm walks e from the position st.
func scanForm(e celast.Expr, st scanState, forms *formScope) string {
	if e == nil {
		return ""
	}
	first := func(exprs ...celast.Expr) string {
		for _, x := range exprs {
			if msg := scanForm(x, st, forms); msg != "" {
				return msg
			}
		}
		return ""
	}
	switch e.Kind() {
	case celast.SelectKind:
		sel := e.AsSelect()
		if sel.IsTestOnly() {
			return "has() is not available in celEnv 1: compare an optional input with null (inputs.x == null), and test a key of an object input with 'key' in inputs.obj"
		}
		op := sel.Operand()
		if !st.hidesValues && op.Kind() == celast.IdentKind && op.AsIdent() == "values" && forms.notYet[sel.FieldName()] {
			return "values." + sel.FieldName() + " is not computed yet: a value reads inputs and earlier values only"
		}
		// A leading dot names the global even where a local variable hides it.
		if op.Kind() == celast.IdentKind && (op.AsIdent() == ".parts" || !st.hidesParts && op.AsIdent() == "parts") {
			if msg := forms.refusePartRead(sel.FieldName(), st); msg != "" {
				return msg
			}
		}
		return first(op)
	case celast.CallKind:
		call := e.AsCall()
		if call.IsMemberFunction() {
			if msg := first(call.Target()); msg != "" {
				return msg
			}
		}
		if args := call.Args(); call.FunctionName() == "_?_:_" && len(args) == 3 {
			// The true branch runs only when the test is true, so it may read every part whose
			// when the test holds. A test inside the body of a comprehension or a bind grants
			// none: a local variable there can hide a name the test reads. The range and the initial
			// value (a bind's initializer) run outside that scope and grant as usual.
			yes := st
			if !st.inComp {
				yes.guarded = forms.guardedBy(st.guarded, args[0])
			}
			if msg := scanForm(args[0], st, forms); msg != "" {
				return msg
			}
			if msg := scanForm(args[1], yes, forms); msg != "" {
				return msg
			}
			return scanForm(args[2], st, forms)
		}
		return first(call.Args()...)
	case celast.ListKind:
		return first(e.AsList().Elements()...)
	case celast.MapKind:
		for _, entry := range e.AsMap().Entries() {
			me := entry.AsMapEntry()
			if msg := first(me.Key(), me.Value()); msg != "" {
				return msg
			}
		}
	case celast.StructKind:
		for _, field := range e.AsStruct().Fields() {
			if msg := first(field.AsStructField().Value()); msg != "" {
				return msg
			}
		}
	case celast.UnspecifiedExprKind, celast.IdentKind, celast.LiteralKind:
		// A leaf holds no sub-expression to scan.
	case celast.ComprehensionKind:
		c := e.AsComprehension()
		if msg := first(c.IterRange(), c.AccuInit()); msg != "" {
			return msg
		}
		hides := func(name string) bool { return c.IterVar() == name || c.IterVar2() == name || c.AccuVar() == name }
		inLoop := scanState{hidesValues: st.hidesValues || hides("values"), hidesParts: st.hidesParts || hides("parts"), inComp: true, guarded: st.guarded}
		inResult := scanState{hidesValues: st.hidesValues || c.AccuVar() == "values", hidesParts: st.hidesParts || c.AccuVar() == "parts", inComp: true, guarded: st.guarded}
		if msg := scanForm(c.LoopCondition(), inLoop, forms); msg != "" {
			return msg
		}
		if msg := scanForm(c.LoopStep(), inLoop, forms); msg != "" {
			return msg
		}
		return scanForm(c.Result(), inResult, forms)
	}
	return ""
}
