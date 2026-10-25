package components

import (
	"github.com/blang/semver/v4"

	"github.com/go-kure/launcher/pkg/errors"
)

// A version gate is a field of a kind whose pods the Prometheus operator runs
// (alertmanager, prometheus, and the thanosruler kind to come) that the
// operator reads only from some version of the software on: below it, the
// operator drops the field and runs the pods without it, or refuses the object
// at reconcile. A few the operator reads only below some version: a ceiling.
// The operator compares the spec's version, or its own default where none is
// written, never the image that runs.
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

// idGate is a versionGate under the id the test holding the kind's table to
// the operator's source names it by: its path, or, where one path is read
// from several versions, "<path>=<value>" or "<path>@<where>". An empty id is
// the path.
type idGate[S any] struct {
	id   string
	gate versionGate[S]
}

// gatesOf is the versionGates of an idGate table.
func gatesOf[S any](table []idGate[S]) []versionGate[S] {
	gates := make([]versionGate[S], len(table))
	for i, g := range table {
		gates[i] = g.gate
	}
	return gates
}

// versionCeiling is one field of a spec S that the operator reads only below
// maximum: from it on, the operator drops the field, or refuses the object.
type versionCeiling[S any] struct {
	// id names the ceiling as idGate does; empty, it is path.
	id string
	// path is the property, as the refusal names it.
	path string
	// maximum is the first version the operator no longer reads the field
	// at.
	maximum string
	// shown, where set, names the versions the field is read at in place of
	// "<product> below <maximum>": "Prometheus 2.x" for a maximum of 3.0.0-0,
	// the least version of major 3.
	shown string
	// above is what the operator does with the field from the maximum on,
	// completing "at <version> the Prometheus operator ...".
	above string
	// authored reports whether spec sets the field as the operator tests it.
	authored func(spec *S) bool
}

// refuseVersionGates refuses a field of gates that spec authors below its
// minimum, where version is the version the operator compares, parsed, and
// at names it for the refusal ("version v0.21.0", or the default where
// version is unset). product is the software the version is of, and the
// version is the spec's version field.
func refuseVersionGates[S any](spec *S, version semver.Version, at, product string, gates []versionGate[S]) error {
	return refuseVersionGatesAt(spec, version, at, product, "version", gates)
}

// refuseVersionGatesAt is refuseVersionGates for the version of field, as
// thanos.version. It refuses the first path of gates that spec authors below
// its minimum, at the highest minimum spec misses for that path: a path read
// from several versions, by where the operator reads it, is refused where any
// of them would drop it, naming the version that reads it everywhere.
func refuseVersionGatesAt[S any](spec *S, version semver.Version, at, product, field string, gates []versionGate[S]) error {
	missed := func(g versionGate[S]) bool {
		return g.authored(spec) && version.LT(semver.MustParse(g.minimum))
	}
	for i, g := range gates {
		if !missed(g) {
			continue
		}
		for _, o := range gates[i+1:] {
			if o.path == g.path && missed(o) && semver.MustParse(o.minimum).GT(semver.MustParse(g.minimum)) {
				g = o
			}
		}
		return errors.Errorf("%s: read by the Prometheus operator only for %s %s and later; at %s it %s; raise %s to %s or later, or leave %s unset", g.path, product, g.minimum, at, g.below, field, g.minimum, g.path)
	}
	return nil
}

// refuseVersionCeilings refuses the first field of ceilings that spec authors
// at or above its maximum, as refuseVersionGatesAt does under a minimum.
func refuseVersionCeilings[S any](spec *S, version semver.Version, at, product, field string, ceilings []versionCeiling[S]) error {
	for _, c := range ceilings {
		if !c.authored(spec) || version.LT(semver.MustParse(c.maximum)) {
			continue
		}
		bound, lower := product+" below "+c.maximum, "below "+c.maximum
		if c.shown != "" {
			bound, lower = c.shown, "to "+c.shown
		}
		return errors.Errorf("%s: read by the Prometheus operator only for %s; at %s it %s; lower %s %s, or leave %s unset", c.path, bound, at, c.above, field, lower, c.path)
	}
	return nil
}
