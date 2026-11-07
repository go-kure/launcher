package model

import (
	"sort"
	"strconv"

	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"

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
	declared := scopeTypes(m)
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
	parsed, iss := env.Parse(inner)
	if iss != nil && iss.Err() != nil {
		return nil, invalid(path, "%s", iss.Err())
	}
	if msg := refuseForm(parsed, notYet); msg != "" {
		return nil, invalid(path, "%s", msg)
	}
	checked, iss := env.Check(parsed)
	if iss != nil && iss.Err() != nil {
		return nil, invalid(path, "%s", iss.Err())
	}
	if root := checked.NativeRep().Expr(); root.Kind() == celast.IdentKind {
		if t := declared[root.AsIdent()]; t != nil {
			return t, nil
		}
	}
	return checked.OutputType(), nil
}

// refuseForm reads a parsed expression for two forms that the type check cannot name.
// It returns the first reason, or the empty string.
//
// has() is refused. An input is a qualified variable and not a map field, so has() has
// nothing to test. A model tests an optional input with `== null`.
//
// A read of a value that is not computed yet (a later value, or the value itself) would
// otherwise fail as an undeclared `values`, which hides the cause. The walk follows the
// scope of a bind or comprehension variable named `values`, which hides the model's
// values the way the checker does: the range and the initializer of a comprehension are
// outside it, the condition, the step and (for the accumulator) the result are inside.
func refuseForm(parsed *cel.Ast, notYet map[string]bool) string {
	return scanForm(parsed.NativeRep().Expr(), false, notYet)
}

// scanForm walks e. shadowed is true inside the scope of a local variable named values.
func scanForm(e celast.Expr, shadowed bool, notYet map[string]bool) string {
	if e == nil {
		return ""
	}
	first := func(exprs ...celast.Expr) string {
		for _, x := range exprs {
			if msg := scanForm(x, shadowed, notYet); msg != "" {
				return msg
			}
		}
		return ""
	}
	switch e.Kind() {
	case celast.SelectKind:
		sel := e.AsSelect()
		if sel.IsTestOnly() {
			return "has() is not available in celEnv 1: compare an optional input with null (inputs.x == null)"
		}
		op := sel.Operand()
		if !shadowed && op.Kind() == celast.IdentKind && op.AsIdent() == "values" && notYet[sel.FieldName()] {
			return "values." + sel.FieldName() + " is not computed yet: a value reads inputs and earlier values only"
		}
		return first(op)
	case celast.CallKind:
		call := e.AsCall()
		if call.IsMemberFunction() {
			if msg := first(call.Target()); msg != "" {
				return msg
			}
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
		inLoop := shadowed || c.IterVar() == "values" || c.IterVar2() == "values" || c.AccuVar() == "values"
		inResult := shadowed || c.AccuVar() == "values"
		if msg := scanForm(c.LoopCondition(), inLoop, notYet); msg != "" {
			return msg
		}
		if msg := scanForm(c.LoopStep(), inLoop, notYet); msg != "" {
			return msg
		}
		return scanForm(c.Result(), inResult, notYet)
	}
	return ""
}
