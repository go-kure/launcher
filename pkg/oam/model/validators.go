package model

import (
	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
)

// rangeValidator keeps a comprehension deterministic and typed. A comprehension may
// range over a list only. A map has no fixed key order in the language, and a dyn
// value has no known element type. It also refuses a call of dyn, which would hide the
// type of its argument from the rule above.
type rangeValidator struct{}

func (rangeValidator) Name() string { return "launcher.model.comprehension_range" }

func (rangeValidator) Validate(_ *cel.Env, _ cel.ValidatorConfig, a *celast.AST, iss *cel.Issues) {
	root := celast.NavigateAST(a)
	for _, c := range celast.MatchDescendants(root, celast.FunctionMatcher("dyn")) {
		iss.ReportErrorAtID(c.ID(), "dyn() is not available in celEnv 1")
	}
	for _, c := range celast.MatchDescendants(root, celast.KindMatcher(celast.ComprehensionKind)) {
		rng := c.AsComprehension().IterRange()
		t := a.GetType(rng.ID())
		if t == nil || t.Kind() != types.ListKind {
			got := "an unknown type"
			if t != nil {
				got = t.String()
			}
			iss.ReportErrorAtID(rng.ID(), "a comprehension ranges over a list in celEnv 1, got %s", got)
		}
	}
}
