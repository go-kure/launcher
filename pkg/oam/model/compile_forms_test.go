package model

import "testing"

// A CEL comment runs from `//` to the end of the line. The hole scanner must read the
// text the way the CEL lexer does: a brace or a quote inside a comment is not syntax,
// and `//` inside a string literal is not a comment.
func TestHoleComments(t *testing.T) {
	accepted := []struct {
		name, expr, want string
	}{
		{"comment, newline, then the brace", "${1 // x\n}", "int"},
		{"a brace in the comment", "${1 // }\n}", "int"},
		{"an apostrophe in the comment", "${1 // it's\n}", "int"},
		{"a double quote in the comment", "${1 // say \"hi\n}", "int"},
		{"code after the comment line", "${1 // }\n + 2}", "int"},
		// The next four pass before the fix too. They pin that the comment skip does
		// not treat `//` inside a string as a comment, which would eat the brace.
		{"slashes in a single-quoted string", "${'a//b'}", "string"},
		{"slashes in a double-quoted string", `${"a//b"}`, "string"},
		{"slashes in a triple-quoted string", "${'''a//b'''}", "string"},
		{"slashes in a raw string", "${r'a//b'}", "string"},
		{"a brace and slashes in a string", "${'}//' + 'x'}", "string"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			cm := wantCompiled(t, oneValue(t, tc.expr))
			if got := cm.valueType("v"); got != tc.want {
				t.Errorf("value v has type %q, want %q", got, tc.want)
			}
		})
	}

	// With no newline the comment runs to the end of the text, so it eats the brace and
	// the hole is not closed.
	for name, expr := range map[string]string{
		"the comment eats the brace":                       "${1 // }",
		"an apostrophe in the comment that eats the brace": "${1 // it's }",
	} {
		t.Run(name, func(t *testing.T) {
			wantRefusal(t, oneValue(t, expr), "spec.values[0].expr", "the ${ hole is not closed")
		})
	}
}

// A local variable (a cel.bind name or a comprehension variable) named `values` hides
// the model's values in its scope. The forward-reference check must follow the scope the
// way the CEL checker does, and not match the text `values.<name>`.
func TestLocalVariableNamedValues(t *testing.T) {
	later := func(t *testing.T, expr string) *Model {
		t.Helper()
		m := celBase(t)
		m.Spec.Values = []Value{{Name: "first", Expr: expr}, {Name: "v", Expr: "${2}"}}
		return m
	}
	accepted := []struct {
		name, expr, want string
	}{
		{"a bind variable", "${cel.bind(values, {'v': 1}, values.v)}", "int"},
		{"a comprehension variable", "${[{'v': 1}].map(values, values.v)}", "list(int)"},
		{"a nested bind", "${cel.bind(a, 1, cel.bind(values, {'v': a}, values.v))}", "int"},
		{"an exists variable", "${[{'v': true}].exists(values, values.v)}", "bool"},
		{"a bind inside a comprehension body", "${[1].map(x, cel.bind(values, {'v': x}, values.v))}", "list(int)"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			cm := wantCompiled(t, later(t, tc.expr))
			if got := cm.valueType("first"); got != tc.want {
				t.Errorf("value first has type %q, want %q", got, tc.want)
			}
		})
	}

	// The scope walk must not weaken the check: a read outside the shadow, in the
	// initializer or the range, or under another name, still reads the later value.
	refused := []struct{ name, expr string }{
		{"a read after the shadow ends", "${cel.bind(values, {'v': 1}, values.v) + values.v}"},
		{"the bind initializer sees the outer scope", "${cel.bind(values, values.v, 1)}"},
		{"the comprehension range sees the outer scope", "${values.v.map(values, 1)}"},
		{"another name is no shadow", "${cel.bind(other, 1, values.v)}"},
		{"a comprehension variable ends with the macro", "${[1].map(x, x) + [values.v]}"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			wantRefusal(t, later(t, tc.expr), "spec.values[0].expr", "values.v is not computed yet")
		})
	}
}

// The standard library declares `_in_` and `in` as disabled legacy names of the `in`
// operator, and `__not_strictly_false__` as a disabled legacy name too. The operator
// itself works through `@in`. This test pins both halves of the README sentence: the
// operator compiles for a list and for a map, and the disabled names cannot be called.
// It was added after the red run and passes at every commit of the branch that has the
// environment, so it is a guard on the README and not evidence for a round-1 repair.
func TestInOperatorAndDisabledNames(t *testing.T) {
	accepted := []struct{ name, expr string }{
		{"a string in a list input", "${'a' in inputs.tags}"},
		{"an int in a list literal", "${1 in [1, 2]}"},
		{"a key in a map input", "${'k' in inputs.labels}"},
		{"a key in a map literal", "${'k' in {'k': 1}}"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			cm := wantCompiled(t, oneValue(t, tc.expr))
			if got := cm.valueType("v"); got != "bool" {
				t.Errorf("value v has type %q, want bool", got)
			}
		})
	}
	for _, tc := range []struct{ name, expr string }{
		{"the legacy in function", "${_in_(1, [1])}"},
		{"the legacy not-strictly-false function", "${__not_strictly_false__(true)}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantRefusal(t, oneValue(t, tc.expr), "spec.values[0].expr", "undeclared reference")
		})
	}
}

// The checker rebuilds a list or map type without its nullable wrapper, so a value that
// copies an optional list or object input would lose the null. A value that is exactly
// one reference keeps the declared type of what it references. Any other expression
// gives the plain type, and the README states the rule.
func TestNullableAliases(t *testing.T) {
	m := celBase(t)
	m.Spec.Values = []Value{
		{Name: "list", Expr: "${inputs.tags}"},
		{Name: "map", Expr: "${inputs.labels}"},
		{Name: "obj", Expr: "${inputs.config}"},
		{Name: "str", Expr: "${inputs.nick}"}, // passes before the fix: a scalar keeps its wrapper
		{Name: "again", Expr: "${values.list}"},
		{Name: "listNull", Expr: "${values.list == null}"},
		{Name: "mapNull", Expr: "${values.map == null}"},
		{Name: "objNull", Expr: "${values.obj == null}"},
		{Name: "strNull", Expr: "${values.str == null}"},
		{Name: "againNull", Expr: "${values.again == null}"},
	}
	cm := wantCompiled(t, m)
	for _, name := range []string{"listNull", "mapNull", "objNull", "strNull", "againNull"} {
		if got := cm.valueType(name); got != "bool" {
			t.Errorf("value %s has type %q, want bool", name, got)
		}
	}

	// A required input is never null, so its alias is not nullable either.
	req := celBase(t)
	req.Spec.Values = []Value{{Name: "img", Expr: "${inputs.image}"}, {Name: "imgNull", Expr: "${values.img == null}"}}
	wantRefusal(t, req, "spec.values[1].expr", "no matching overload")

	// A value that is not one bare reference gives the plain type: this pins the rule
	// that the README states. It passes before the fix as well.
	plain := celBase(t)
	plain.Spec.Values = []Value{{Name: "plus", Expr: "${inputs.tags + []}"}, {Name: "plusNull", Expr: "${values.plus == null}"}}
	wantRefusal(t, plain, "spec.values[1].expr", "no matching overload")
}
