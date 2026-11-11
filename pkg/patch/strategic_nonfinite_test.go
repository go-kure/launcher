package patch

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The tests below pin go-kure/launcher#1414: a strategic merge patch goes through
// JSON, which has no NaN and no infinity, so a resource or a patch that holds one is
// refused with an error that names the number and the path of the field. Both routes
// are covered, the known kind (typed merge, jsonTyped) and the unknown kind (JSON
// merge fallback).

// nonFiniteWant is the whole text the refusal carries: where the number is, which it
// is, its path, and the reason.
func nonFiniteWant(where, number, path string) string {
	return fmt.Sprintf("%s holds the non-finite number %s at %s, and a strategic merge patch goes through JSON, which has no NaN or Inf", where, number, path)
}

// nonFiniteKinds are the two routes. Widget is in no scheme, so the lookup does not
// know it and the JSON merge fallback runs; Deployment is known, so the typed merge runs.
var nonFiniteKinds = []struct {
	name, apiVersion, kind string
}{
	{"unknown kind", "example.dev/v1", "Widget"},
	{"known kind", "apps/v1", "Deployment"},
}

// nonFiniteNumbers are the three values encoding/json refuses, with the text the refusal
// gives each.
var nonFiniteNumbers = []struct {
	text  string
	value float64
}{
	{"NaN", math.NaN()},
	{"+Inf", math.Inf(1)},
	{"-Inf", math.Inf(-1)},
}

// nonFinitePlacements put the number in a document and name the path the refusal gives.
// The last one holds two numbers, and the refusal names the first key in sorted order,
// so that the path does not depend on the order a map is walked.
var nonFinitePlacements = []struct {
	name string
	spec func(v float64) map[string]any
	path string
}{
	{"key of the spec", func(v float64) map[string]any {
		return map[string]any{"value": v}
	}, "spec.value"},
	{"list item", func(v float64) map[string]any {
		return map[string]any{"extra": []any{
			map[string]any{"name": "a", "value": 1.5},
			map[string]any{"name": "b", "value": v},
		}}
	}, "spec.extra[1].value"},
	{"two numbers", func(v float64) map[string]any {
		return map[string]any{"b": v, "a": v}
	}, "spec.a"},
}

func nonFiniteResource(apiVersion, kind string, spec map[string]any) *unstructured.Unstructured {
	spec["replicas"] = int64(1)
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "x", "namespace": "default"},
		"spec":       spec,
	}}
}

func TestApplyStrategicMergePatch_RefusesNonFiniteNumbers(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	// Control: the lookup knows Deployment and not Widget, so the two rows below run the
	// two routes they name.
	if _, ok := lookup.LookupKind(nonFiniteResource("apps/v1", "Deployment", map[string]any{}).GroupVersionKind()); !ok {
		t.Fatal("control: the lookup does not know Deployment")
	}
	if _, ok := lookup.LookupKind(nonFiniteResource("example.dev/v1", "Widget", map[string]any{}).GroupVersionKind()); ok {
		t.Fatal("control: the lookup knows Widget, so the unknown-kind rows do not run the fallback")
	}

	for _, k := range nonFiniteKinds {
		for _, n := range nonFiniteNumbers {
			for _, p := range nonFinitePlacements {
				t.Run(k.name+"/resource/"+n.text+"/"+p.name, func(t *testing.T) {
					resource := nonFiniteResource(k.apiVersion, k.kind, p.spec(n.value))
					patch := map[string]any{"spec": map[string]any{"replicas": int64(2)}}
					err := ApplyStrategicMergePatch(resource, patch, lookup)
					if err == nil {
						t.Fatalf("want the resource refused, got none")
					}
					if want := nonFiniteWant("the resource", n.text, p.path); !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
					// The resource is left as it was.
					if got := resource.Object["spec"].(map[string]any)["replicas"]; got != int64(1) {
						t.Errorf("the resource changed on a refusal: replicas is %v, want 1", got)
					}
				})
				t.Run(k.name+"/patch/"+n.text+"/"+p.name, func(t *testing.T) {
					resource := nonFiniteResource(k.apiVersion, k.kind, map[string]any{})
					patch := map[string]any{"spec": p.spec(n.value)}
					err := ApplyStrategicMergePatch(resource, patch, lookup)
					if err == nil {
						t.Fatalf("want the patch refused, got none")
					}
					if want := nonFiniteWant("the patch", n.text, p.path); !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
					if got := resource.Object["spec"].(map[string]any)["replicas"]; got != int64(1) {
						t.Errorf("the resource changed on a refusal: replicas is %v, want 1", got)
					}
				})
			}
		}
	}
}

// selfContaining returns a map that holds itself under two keys. A walk that treats
// "too deep" as "nothing found here" and goes on to the next sibling visits about
// 2^maxDepth paths of it.
func selfContaining() map[string]any {
	m := map[string]any{}
	m["a"] = m
	m["b"] = m
	return m
}

// withinSeconds runs f and fails the test when it has not returned after five
// seconds, so that a walk that does not end is a failure and not a hung run. The
// goroutine that cannot end is left behind: the test run ends with the process.
func withinSeconds(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not return within five seconds")
		return nil
	}
}

// The refusal reads a document with the same structure check the normalization makes,
// so a document that nests too deeply or holds itself is reported as before, at once,
// and a non-finite number never takes its place. The rows put the structural fault
// and the number in the places that matter for the order of the two checks: the
// resource is read before the patch, and a structural fault anywhere in an input
// comes before a number in the same input.
func TestApplyStrategicMergePatch_StructureErrorsComeBeforeNonFinite(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	rows := []struct {
		name     string
		resource func() map[string]any
		patch    func() map[string]any
	}{
		{"resource holds itself", selfContaining,
			func() map[string]any { return map[string]any{"replicas": int64(2)} }},
		{"resource holds itself, patch holds a NaN", selfContaining,
			func() map[string]any { return map[string]any{"value": math.NaN()} }},
		{"resource holds a NaN and, under a later key, itself", func() map[string]any {
			return map[string]any{"a": math.NaN(), "z": selfContaining()}
		}, func() map[string]any { return map[string]any{"replicas": int64(2)} }},
	}
	for _, k := range nonFiniteKinds {
		for _, r := range rows {
			t.Run(k.name+"/"+r.name, func(t *testing.T) {
				resource := nonFiniteResource(k.apiVersion, k.kind, r.resource())
				patch := map[string]any{"spec": r.patch()}
				err := withinSeconds(t, func() error { return ApplyStrategicMergePatch(resource, patch, lookup) })
				if err == nil {
					t.Fatal("want an error, got none")
				}
				if !strings.Contains(err.Error(), errTooDeep.Error()) {
					t.Errorf("want the structure error %q, got %v", errTooDeep, err)
				}
				if strings.Contains(err.Error(), "non-finite") {
					t.Errorf("the structure error was replaced by a non-finite refusal: %v", err)
				}
			})
		}
	}
}

// A float32 is as unfit for JSON as a float64 is, and a code-built patch may hold one.
func TestApplyStrategicMergePatch_RefusesNonFiniteFloat32(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	for _, k := range nonFiniteKinds {
		t.Run(k.name, func(t *testing.T) {
			resource := nonFiniteResource(k.apiVersion, k.kind, map[string]any{})
			patch := map[string]any{"spec": map[string]any{"value": float32(math.Inf(1))}}
			err := ApplyStrategicMergePatch(resource, patch, lookup)
			if err == nil {
				t.Fatal("want the patch refused, got none")
			}
			if want := nonFiniteWant("the patch", "+Inf", "spec.value"); !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		})
	}
}

// The control of the rows above: a finite float, and the largest one, still patches on
// both routes, and the value comes out as it went in. The known kind holds it in a
// field its type has, as the typed merge refuses a field the type lacks.
func TestApplyStrategicMergePatch_FiniteFloatsStillPatch(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	for _, k := range nonFiniteKinds {
		key := "value"
		if k.kind == "Deployment" {
			key = "minReadySeconds"
		}
		for _, v := range []float64{1.5, -2.25, math.MaxFloat64} {
			t.Run(fmt.Sprintf("%s/%v", k.name, v), func(t *testing.T) {
				resource := nonFiniteResource(k.apiVersion, k.kind, map[string]any{key: v})
				patch := map[string]any{"spec": map[string]any{"replicas": int64(2)}}
				if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
					t.Fatalf("a finite float must patch, got %v", err)
				}
				spec := resource.Object["spec"].(map[string]any)
				if spec[key] != v {
					t.Errorf("%s is %v (%T), want %v", key, spec[key], spec[key], v)
				}
				if spec["replicas"] != int64(2) {
					t.Errorf("replicas is %v, want 2", spec["replicas"])
				}
			})
		}
	}
}

// The repro of the issue: a document with `.nan` in a list item on an unknown kind, and
// a strategic patch on another field, through the document route that writes back.
func TestApplyStrategicPatchesToDocument_RefusesNaNInDocument(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	doc := loadOneDocument(t, `apiVersion: example.dev/v1
kind: Widget
metadata:
  name: x
  namespace: default
spec:
  replicas: 1
  extra:
    - name: a
      value: 1.5
    - name: b
      value: .nan
`)
	patch := StrategicPatch{Patch: map[string]any{"spec": map[string]any{"replicas": int64(2)}}}
	err = doc.ApplyStrategicPatchesToDocument([]StrategicPatch{patch}, lookup)
	if err == nil {
		t.Fatal("want the document refused, got none")
	}
	if want := nonFiniteWant("the resource", "NaN", "spec.extra[1].value"); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q lacks %q", err, want)
	}
}
