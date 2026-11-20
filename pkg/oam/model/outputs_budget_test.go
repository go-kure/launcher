package model_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the build budget of the queries a model makes for its outputs
// (go-kure/launcher#1643): the render a query makes counts against the one budget of the build,
// as the render of a lowering does, and a query stops at the deadline of the build.

const (
	// fortyNinePartsAndASecret is 49 ConfigMaps from one forEach and a Secret: the 50 parts one
	// render may give, one of which a model can publish the record of.
	fortyNinePartsAndASecret = `
    - id: cfg
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ lists.range(49) }"
      properties:
        data:
          k: v
    - id: creds
      type: ` + secretType + `
      properties: {}
`
	publishesCreds = "  outputs:\n    creds: \"${ parts.creds.name }\"\n"
	nextPublishes  = "  outputs:\n    creds: \"${ parts.next.creds.name }\"\n"
	nextPart       = `
    - id: next
      type: ` + innerType + `
      properties: {}
`
)

// publishingChain is a model that has one part, a model of 50 parts that publishes the record of
// one of them, and publishes that record in turn.
func publishingChain(t *testing.T) *oam.Transformer {
	t.Helper()
	return registerAll(t,
		outputsDoc("inner.acme.example", publishesCreds, fortyNinePartsAndASecret),
		outputsDoc("webapp.acme.example", nextPublishes, nextPart))
}

// TestHostileBuild_QueriesForOutputsCountAgainstTheBuild: the parts of every render of one build
// are one count, the render of a query for outputs included. A component of the chain is rendered
// for its outputs, asked about and lowered, at both levels, so two components reach the 500 parts
// of the build where the same chain with no output costs the 51 of its lowering each.
func TestHostileBuild_QueriesForOutputsCountAgainstTheBuild(t *testing.T) {
	quiet := registerAll(t,
		outputsDoc("inner.acme.example", "  outputs: {}\n", fortyNinePartsAndASecret),
		outputsDoc("webapp.acme.example", "  outputs: {}\n", nextPart))
	buildOK(t, quiet, copies(9), "nine components that publish nothing (459 parts)")

	tr := publishingChain(t)
	buildOK(t, tr, copies(1), "one component that publishes (under the limit)")
	err := transformWith(tr, copies(2), nil)
	wantBuildLimit(t, err, "more than 500 parts")
	if strings.Contains(err.Error(), "in one render") {
		t.Errorf("error %q names the limit of one render, want the limit of the build", err)
	}
	// A build that failed leaves nothing behind for the next.
	buildOK(t, tr, copies(1), "one component after the failed build")
}

// TestRule_AQueryForOutputsStopsAtTheDeadline: a model that publishes an output stops its query
// at the deadline of the build, though its properties are literal and no expression runs after
// the render.
func TestRule_AQueryForOutputsStopsAtTheDeadline(t *testing.T) {
	tr, _ := registered(t, outputsDoc("webapp.acme.example", publishedOutputs, pgOnlyParts))
	c := oam.Component{Name: "orders", Type: webappType}
	if _, err := tr.ComponentOutputs(&c); err != nil {
		t.Fatalf("under the deadline: %v", err)
	}
	// The first read of the clock starts the build; every read after it is past the deadline.
	base := time.Now()
	var reads atomic.Int64
	t.Cleanup(model.SetBuildClock(func() time.Time {
		if reads.Add(1) == 1 {
			return base
		}
		return base.Add(model.BuildTimeout + time.Second)
	}))
	_, err := tr.ComponentOutputs(&c)
	wantDeadline(t, err)
}
