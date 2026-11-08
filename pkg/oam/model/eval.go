package model

import (
	"github.com/go-kure/launcher/pkg/errors"
)

// ErrCostLimit is wrapped by the failure of an evaluation that spent more than the cost
// limit of one expression.
var ErrCostLimit = errors.New("model: expression cost limit exceeded")

// ErrNonFinite is wrapped by the failure of an evaluation whose result holds a NaN or an
// infinite double. A document cannot encode such a value.
var ErrNonFinite = errors.New("model: result is not a finite number")

// errNotBuilt marks the stub of the red commit: every function below fails with it.
var errNotBuilt = errors.New("model: not built yet")

// Self is the `self` variable of an expression: the names of the component being
// built and of its application.
type Self struct {
	Name        string
	Application string
}

// Scope is what one evaluation reads: the inputs, `self`, the values computed so far,
// and the loop variable of a forEach part.
type Scope struct {
	Inputs map[string]any
	Self   Self
	Values map[string]any
	Loop   any
}

// Compiled is a model whose expressions are compiled and typed. It is safe for
// concurrent use.
type Compiled struct{}

// Cache holds compiled programs keyed by the model identity, the site path and the
// expression text. It is safe for concurrent use.
type Cache struct{}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{} }

// Len returns the number of cached programs.
func (c *Cache) Len() int { return -1 }

// Compile compiles every expression site of m in document order.
func (c *Cache) Compile(m *Model) (*Compiled, error) { return nil, errNotBuilt }

// Compile compiles m with a cache of its own.
func Compile(m *Model) (*Compiled, error) { return NewCache().Compile(m) }

// Eval runs the expression site named by path and returns the Go value and the actual
// cost.
func (c *Compiled) Eval(path string, sc Scope) (any, int64, error) { return nil, 0, errNotBuilt }
