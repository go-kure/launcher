package model

import (
	"time"

	"github.com/go-kure/launcher/pkg/oam"
)

// QueryOutputsWith returns sc with the query of a part's outputs replaced, so a test sees which
// parts the Transformer is asked about.
func QueryOutputsWith(sc Scope, q func(*oam.Component, func(oam.NameRequest) (string, bool)) ([]oam.Output, error)) Scope {
	sc.queryOutputs = q
	return sc
}

// ExpandedParts expands c and returns what a document may read (the bound outputs of each part)
// and the ids of the parts whose outputs the build published.
func ExpandedParts(c *Compiled, tr *oam.Transformer, sc Scope) (map[string]map[string]any, map[string]bool, error) {
	exp, err := c.expand(tr, sc, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	published := make(map[string]bool, len(exp.published))
	for id := range exp.published {
		published[id] = true
	}
	return exp.parts, published, nil
}

// SetBuildClock replaces the time source of the build deadline and returns the undo.
func SetBuildClock(f func() time.Time) func() {
	old := buildClock
	buildClock = f
	return func() { buildClock = old }
}

// SetBuildCostLimit replaces the cost limit of a build and returns the undo.
func SetBuildCostLimit(n int64) func() {
	old := buildCostLimit
	buildCostLimit = n
	return func() { buildCostLimit = old }
}
