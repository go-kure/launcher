package model

import "testing"

// The cache key holds the template text. Two definitions under one model identity write
// different text at the same path, and one cache compiles both: the second does not run the
// program of the first. Without the text in the key the second compile reuses the first site
// and gives the first text's result for a different expression.
func TestProgramCacheKeyHoldsText(t *testing.T) {
	mk := func(t *testing.T, text string) *Model {
		t.Helper()
		m := evalBase(t)
		m.Spec.Parts[0].Properties = map[string]any{"p": text}
		if err := validate(m); err != nil {
			t.Fatalf("test bug: %v", err)
		}
		return m
	}
	const site = "spec.parts[0].properties.p"
	cache := CreateCache()
	first, err := cache.Compile(mk(t, "${inputs.image}"))
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	one := cache.Len()
	second, err := cache.Compile(mk(t, "${inputs.image + '-x'}"))
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if got := cache.Len(); got != one+1 {
		t.Errorf("after a second text at the same identity and path the cache holds %d programs, want %d", got, one+1)
	}
	if got, _ := mustEval(t, first, site, stdScope()); got != "nginx" {
		t.Errorf("first text gave %#v, want \"nginx\"", got)
	}
	if got, _ := mustEval(t, second, site, stdScope()); got != "nginx-x" {
		t.Errorf("second text gave %#v, want \"nginx-x\"", got)
	}
}
