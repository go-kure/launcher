//go:build !goexperiment.jsonv2

package builtin

import "slices"

// strictCase reports false for every tag. The v1 decoder reads no case option,
// so it fills a field from a key that differs from its name in case whatever the
// tag says. The other answer is in fold_order_jsonv2.go.
func strictCase(string) bool { return false }

// foldedBefore reports whether the field at index a is looked up before the
// field at index b when one key matches both names ignoring case.
//
// This build's encoding/json is the v1 decoder (GOEXPERIMENT=nojsonv2). It
// keeps the first folded match in the order of the index paths (typeFields
// sorts the surviving fields by index), so a field promoted from an embedded
// struct that is declared first comes before a shallower field that is declared
// later. The build tag is the one the standard library uses to pick its
// decoder, so this file and the decoder always agree. The other order is in
// fold_order_jsonv2.go.
func foldedBefore(a, b []int) bool {
	return slices.Compare(a, b) < 0
}
