package model

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"

	"github.com/go-kure/launcher/pkg/errors"
)

// Limits of the celEnv 1 environment. They are start values, stated in the README as
// such. Raising one later is cheap and lowering one breaks models.
const (
	// maxExpressionSize is the length of one expression in code points.
	maxExpressionSize = 4096
	// maxRecursionDepth is the parser recursion depth.
	maxRecursionDepth = 64
	// maxNodeCount is the number of nodes the parser may expand macros into. It is a
	// defensive bound. No input was found that reaches it under the size limit above,
	// and that is not proven.
	maxNodeCount = 10000
	// maxBindDepth is the nesting depth of cel.bind.
	maxBindDepth = 8
	// maxExpressionCost is the runtime cost of one hole. An evaluation that would spend
	// more stops with ErrCostLimit.
	maxExpressionCost = 1000000
)

// The versions of the cel-go extension libraries that celEnv 1 means. Each library
// defaults to its newest version, so without a pin a cel-go upgrade would change what
// a model means. These are the highest versions of cel-go v0.31.0. The golden file
// testdata/celenv1-functions.txt freezes the resulting function set.
const (
	stringsVersion  = 5
	listsVersion    = 3
	bindingsVersion = 1
)

// newEnv builds the celEnv 1 environment: the CEL standard library and the strings,
// lists and bindings extensions at the pinned versions, the parser limits, and the
// validators of this package. vars declare the variables of one scope.
func newEnv(vars ...cel.EnvOption) (*cel.Env, error) {
	opts := []cel.EnvOption{
		ext.Strings(ext.StringsVersion(stringsVersion)),
		ext.Lists(ext.ListsVersion(listsVersion)),
		ext.Bindings(ext.BindingsVersion(bindingsVersion)),
		cel.ParserExpressionSizeLimit(maxExpressionSize),
		cel.ParserRecursionLimit(maxRecursionDepth),
		cel.ExpressionNodeLimit(maxNodeCount),
		cel.ASTValidators(cel.ValidateBindNestingLimit(maxBindDepth), rangeValidator{}),
	}
	env, err := cel.NewEnv(append(opts, vars...)...)
	if err != nil {
		return nil, errors.Wrap(err, "model: build the CEL environment")
	}
	return env, nil
}
