//go:build goexperiment.jsonv2

package builtin

import (
	"slices"
	"strings"
	"unicode"
)

// strictCase reports whether options, the part of a json tag after its first
// comma, makes the field strict. The jsonv2-backed decoder fills such a field
// only from a key spelled exactly as its name, and never through a key that
// differs from it in case (encoding/json/v2/fields.go, matchFoldedName). It reads
// the value of case: as the identifier that follows the colon, so case:strict
// followed by a space is still strict. A field with both case:strict and
// case:ignore is not strict, because the parser holds both bits and its matcher
// then folds. Quoted spellings of the option are not read.
func strictCase(options string) bool {
	var strict, ignore bool
	for _, opt := range strings.Split(options, ",") {
		v, ok := strings.CutPrefix(opt, "case:")
		if !ok {
			continue
		}
		switch identifier(v) {
		case "strict":
			strict = true
		case "ignore":
			ignore = true
		}
	}
	return strict && !ignore
}

// identifier returns the leading run of letters, digits and underscores of s,
// the way the decoder's tag parser reads an option name.
func identifier(s string) string {
	n := len(s) - len(strings.TrimLeftFunc(s, func(r rune) bool {
		return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
	}))
	return s[:n]
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
