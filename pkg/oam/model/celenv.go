package model

import (
	"github.com/google/cel-go/cel"

	"github.com/go-kure/launcher/pkg/errors"
)

// RED stub. The next commit replaces this file with the real environment. The limits
// are zero here so that the test that pins them fails.
const (
	maxExpressionSize = 0
	maxRecursionDepth = 0
	maxNodeCount      = 0
	maxBindDepth      = 0
)

func newEnv() (*cel.Env, error) {
	return nil, errors.New("model: newEnv is not implemented")
}
