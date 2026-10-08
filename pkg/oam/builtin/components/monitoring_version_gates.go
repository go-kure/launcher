package components

import (
	"github.com/blang/semver/v4"

	"github.com/go-kure/launcher/pkg/errors"
)

// A version gate is a field of a kind whose pods the Prometheus operator runs
// (alertmanager, and the prometheus and thanosruler kinds to come) that the
// operator reads only from some version of the software on: below it, the
// operator drops the field and runs the pods without it, or refuses the object
// at reconcile. The operator compares the spec's version, or its own default
// where none is written, never the image that runs.
//
// The kind refuses such a field authored below its minimum
// (go-kure/launcher#935): the object would state a setting the pods do not
// run with. Each kind keeps its gates in a table that a test holds to the
// operator's source, vendored under testdata/upstream/prometheus-operator: a
// comparison of the version in that source without a row, or a row without
// one, fails the test.
//
// An unset version is held as the default of the operator the module is cut
// from. An operator of another release, deployed, may fill another default;
// the kind cannot know which, as it cannot for the rest of what the operator
// fills.

// versionGate is one field of a spec S that the operator reads only from
// minimum on.
type versionGate[S any] struct {
	// path is the property, as the refusal names it.
	path string
	// minimum is the first version the operator reads the field at.
	minimum string
	// below is what the operator does with the field under the minimum,
	// completing "at <version> the Prometheus operator ...".
	below string
	// authored reports whether spec sets the field as the operator tests it.
	authored func(spec *S) bool
}

// refuseVersionGates refuses the first field of gates that spec authors below
// its minimum, where version is the version the operator compares, parsed,
// and at names it for the refusal ("version v0.21.0", or the default where
// version is unset). product is the software the version is of.
func refuseVersionGates[S any](spec *S, version semver.Version, at, product string, gates []versionGate[S]) error {
	for _, g := range gates {
		if !g.authored(spec) || !version.LT(semver.MustParse(g.minimum)) {
			continue
		}
		return errors.Errorf("%s: read by the Prometheus operator only for %s %s and later; at %s it %s; raise version to %s or later, or leave %s unset", g.path, product, g.minimum, at, g.below, g.minimum, g.path)
	}
	return nil
}
