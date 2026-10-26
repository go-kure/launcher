package components

import (
	"strings"

	"github.com/blang/semver/v4"

	"github.com/go-kure/launcher/pkg/errors"
)

// A version gate is a field of a kind whose pods the Prometheus operator runs
// (alertmanager, prometheus and thanosruler) that the
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
// thanos.version, refusing the misses of gateMisses (refuseVersionMisses).
func refuseVersionGatesAt[S any](spec *S, version semver.Version, at, product, field string, gates []versionGate[S]) error {
	return refuseVersionMisses(gateMisses(spec, version, at, product, gates), authoredRange(spec, gates, nil), at, field)
}

// versionMiss is a field spec authors outside the versions the operator reads
// it at.
type versionMiss struct {
	// path is the property.
	path string
	// read names the versions the operator reads it at: "from Prometheus
	// 2.54.0", "below Prometheus 2.39.0" or "for Prometheus 2.x".
	read string
	// minimum is the version it is read from, empty for a ceiling.
	minimum string
	// alone is the refusal of the field where it is the only miss, less the
	// fix: the versions the operator reads it at, and what it does with it at
	// the version compared.
	alone string
}

// gateMisses is every path of gates that spec authors below its minimum, in
// table order, each at the highest minimum spec misses for it: a path read
// from several versions, by where the operator reads it, is refused where any
// of them would drop it, naming the version that reads it everywhere.
func gateMisses[S any](spec *S, version semver.Version, at, product string, gates []versionGate[S]) []versionMiss {
	missed := func(g versionGate[S]) bool {
		return g.authored(spec) && version.LT(semver.MustParse(g.minimum))
	}
	var misses []versionMiss
	seen := map[string]bool{}
	for i, g := range gates {
		if seen[g.path] || !missed(g) {
			continue
		}
		seen[g.path] = true
		for _, o := range gates[i+1:] {
			if o.path == g.path && missed(o) && semver.MustParse(o.minimum).GT(semver.MustParse(g.minimum)) {
				g = o
			}
		}
		misses = append(misses, versionMiss{
			path: g.path, read: "from " + product + " " + g.minimum, minimum: g.minimum,
			alone: g.path + ": read by the Prometheus operator only for " + product + " " + g.minimum + " and later; at " + at + " it " + g.below,
		})
	}
	return misses
}

// ceilingMisses is every field of ceilings that spec authors at or above its
// maximum, in table order.
func ceilingMisses[S any](spec *S, version semver.Version, at, product string, ceilings []versionCeiling[S]) []versionMiss {
	var misses []versionMiss
	for _, c := range ceilings {
		if !c.authored(spec) || version.LT(semver.MustParse(c.maximum)) {
			continue
		}
		bound, read := product+" below "+c.maximum, "below "+product+" "+c.maximum
		if c.shown != "" {
			bound, read = c.shown, "for "+c.shown
		}
		misses = append(misses, versionMiss{
			path: c.path, read: read,
			alone: c.path + ": read by the Prometheus operator only for " + bound + "; at " + at + " it " + c.above,
		})
	}
	return misses
}

// versionRange is the versions at which the operator reads every field of
// some gates and ceilings that a spec authors, missed at its version or not:
// from the highest minimum among them on (from, empty for none), and below
// the lowest maximum (below, empty for none). lower and within word that
// maximum for a fix: "below 2.39.0" both, or, for a ceiling shown as
// "Prometheus 2.x", "to Prometheus 2.x" and "within Prometheus 2.x".
type versionRange struct {
	from, below, lower, within string
}

// authoredRange is the versionRange of the gates and ceilings spec authors.
func authoredRange[S any](spec *S, gates []versionGate[S], ceilings []versionCeiling[S]) versionRange {
	var r versionRange
	for _, g := range gates {
		if g.authored(spec) && (r.from == "" || semver.MustParse(g.minimum).GT(semver.MustParse(r.from))) {
			r.from = g.minimum
		}
	}
	for _, c := range ceilings {
		if c.authored(spec) && (r.below == "" || semver.MustParse(c.maximum).LT(semver.MustParse(r.below))) {
			r.below, r.lower, r.within = c.maximum, "below "+c.maximum, "below "+c.maximum
			if c.shown != "" {
				r.lower, r.within = "to "+c.shown, "within "+c.shown
			}
		}
	}
	return r
}

// atLeast is r from minimum on: from is raised to it where below it or
// unset.
func (r versionRange) atLeast(minimum string) versionRange {
	if r.from == "" || semver.MustParse(r.from).LT(semver.MustParse(minimum)) {
		r.from = minimum
	}
	return r
}

// refuseVersionMisses refuses misses, the fields spec authors outside the
// versions the operator reads them at, judged at the version of field, named
// by at. A single miss is refused with what the operator does with it. Of
// several, each may be preempted by another the operator reaches first (a
// refusal at reconcile, before the configuration a drop is from; a panic
// generating one part of it, before another), so no single consequence is
// claimed: the refusal names every field with its versions, and states that
// the object does not run as authored (go-kure/launcher#1022). The version
// either suggests is in authored, the versions every field spec authors is
// read at, so that it does not drop a field read at the version spec names,
// missed or not (go-kure/launcher#1047); where there is none, it suggests only
// leaving the fields refused unset.
func refuseVersionMisses(misses []versionMiss, authored versionRange, at, field string) error {
	switch len(misses) {
	case 0:
		return nil
	case 1:
		return errors.New(misses[0].alone + "; " + authored.fix(field, misses[0].path))
	}
	named := make([]string, len(misses))
	for i, m := range misses {
		named[i] = m.path + " (read " + m.read + ")"
	}
	return errors.Errorf("%s: refused: fields outside their versions at %s: %s; the Prometheus operator does not run the object as authored there; %s", field, at, strings.Join(named, ", "), authored.fix(field, "those fields"))
}

// fix is how a refusal of fields, named by unset, outside their versions at
// the version of field is fixed, where r is the versionRange of the fields
// authored: a version in r, or, where r is empty, leaving them unset.
func (r versionRange) fix(field, unset string) string {
	switch {
	case r.below == "":
		return "raise " + field + " to " + r.from + " or later, or leave " + unset + " unset"
	case r.from == "":
		return "lower " + field + " " + r.lower + ", or leave " + unset + " unset"
	case semver.MustParse(r.from).LT(semver.MustParse(r.below)):
		return "name a " + field + " from " + r.from + " on and " + r.within + ", or leave " + unset + " unset"
	default:
		return "the operator reads the fields authored at no single " + field + " (from " + r.from + " on and " + r.within + "): leave " + unset + " unset"
	}
}
