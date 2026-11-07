//go:build goexperiment.jsonv2

package builtin

import "slices"

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
