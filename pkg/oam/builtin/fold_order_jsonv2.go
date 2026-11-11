//go:build goexperiment.jsonv2

package builtin

import (
	"slices"
	"strings"
)

// strictCase reports whether options, the part of a json tag after its first
// comma, holds case:strict. The jsonv2-backed decoder fills such a field only
// from a key spelled exactly as its name, and never through a key that differs
// from it in case (encoding/json/v2/fields.go, matchFoldedName). Quoted
// spellings of the option are not read.
func strictCase(options string) bool {
	return slices.Contains(strings.Split(options, ","), "case:strict")
}

// foldedBefore reports whether the field at index a is looked up before the
// field at index b when one key matches both names ignoring case.
//
// This build's encoding/json is the jsonv2-backed one (the default on the Go
// version go.mod names). It sorts such candidates in breadth-first order, so a
// shallower field comes first and, at one depth, the one declared earlier
// (encoding/json/v2/fields.go, byFoldedName). The build tag is the one the
// standard library uses to pick its decoder, so this file and the decoder
// always agree. The other order is in fold_order_jsonv1.go.
func foldedBefore(a, b []int) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return slices.Compare(a, b) < 0
}
