package model

import (
	"github.com/google/cel-go/cel"

	"github.com/go-kure/launcher/pkg/errors"
)

// RED stub. The commit that follows replaces this file with the real compile step.

type compiledModel struct{}

// valueType returns the CEL type of a compiled value, as text.
func (c *compiledModel) valueType(string) string { return "" }

// elemType returns the CEL element type of a part's forEach.in, as text. It is empty
// for a part without forEach.
func (c *compiledModel) elemType(string) string { return "" }

func compileModel(*Model) (*compiledModel, error) {
	return nil, errors.New("model: compileModel is not implemented")
}

var _ = cel.StringType
