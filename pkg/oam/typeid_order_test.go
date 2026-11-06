package oam

import (
	"sort"
	"strings"
	"testing"
)

// The order of type versions (go-kure/launcher#1288): major, then alpha < beta
// < GA, then the number after the stage (alpha and beta) or minor and patch
// (GA). These tests pin the order as a strict weak ordering, so a sort of the
// distinct versions of one type gives one result whatever order they arrive in.
// Register refuses two spellings of one version (v2, v2.0.0), and neither is
// less than the other.

// mixedVersions is the issue's set, in the order the comparator must give.
var mixedVersions = []string{"v1alpha1", "v1beta1", "v1", "v2beta1", "v2", "v10beta1", "v10"}

func TestVersionLess_Table(t *testing.T) {
	for name, tc := range map[string]struct{ lesser, greater string }{
		"api versions compare their major numerically":    {"v2beta1", "v10beta1"},
		"a semver is before the beta of a larger major":   {"v2.0.0", "v10beta1"},
		"a semver is before the semver of a larger major": {"v2.0.0", "v10.0.0"},
		"a beta is before the semver of a larger major":   {"v1beta1", "v10.0.0"},
		"a beta of one major is before v2.0.0":            {"v1beta1", "v2.0.0"},
		"alpha before beta":                               {"v1alpha1", "v1beta1"},
		"alpha number is numeric":                         {"v1alpha2", "v1alpha10"},
		"beta before GA of the same major":                {"v1beta1", "v1"},
		"alpha before GA of the same major":               {"v1alpha1", "v1"},
		"GA before the alpha of the next major":           {"v1.5.0", "v2alpha1"},
		"a higher alpha number, same major, before beta":  {"v1alpha2", "v1beta1"},
		"minor is numeric":                                {"v1.2", "v1.10"},
		"patch is numeric":                                {"v1.0.2", "v1.0.10"},
		"major zero is before major one":                  {"v0.9.0", "v1alpha1"},
		// A text compare puts the 21-digit number first, a number longer than an
		// int must still compare by value.
		"a major longer than an int compares by length": {"v99999999999999999999", "v100000000000000000000"},
		"a minor longer than an int compares by length": {"v1.99999999999999999999", "v1.100000000000000000000"},
		"an invalid version is after every valid one":   {"v10", "x"},
		"an empty version is after every valid one":     {"v10", ""},
		"invalid versions sort by their text":           {"", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if !versionLess(tc.lesser, tc.greater) {
				t.Errorf("versionLess(%q, %q) = false, want true", tc.lesser, tc.greater)
			}
			if versionLess(tc.greater, tc.lesser) {
				t.Errorf("versionLess(%q, %q) = true, want false", tc.greater, tc.lesser)
			}
		})
	}
}

// Three spellings of one version are one version: neither is less.
func TestVersionLess_SpellingsOfOneVersionAreIncomparable(t *testing.T) {
	for _, pair := range [][2]string{{"v2", "v2.0"}, {"v2", "v2.0.0"}, {"v2.0", "v2.0.0"}, {"v1beta1", "v1beta1"}} {
		if versionLess(pair[0], pair[1]) || versionLess(pair[1], pair[0]) {
			t.Errorf("%q and %q are one version, but one is less than the other", pair[0], pair[1])
		}
	}
}

// TestVersionLess_IsAStrictWeakOrdering checks the four laws on every pair and
// triple of a set that holds each shape: semver, API versions, spellings of one
// version, a number longer than an int, and text that is no version.
func TestVersionLess_IsAStrictWeakOrdering(t *testing.T) {
	set := append([]string{
		"v2.0", "v2.0.0", "v1.1", "v0.1.0", "v99999999999999999999", "v1alpha2", "x", "", "v1beta",
	}, mixedVersions...)
	less := versionLess
	// equiv: neither is less, the incomparability of a strict weak ordering.
	equiv := func(a, b string) bool { return !less(a, b) && !less(b, a) }
	for _, a := range set {
		if less(a, a) {
			t.Errorf("irreflexivity: versionLess(%q, %q) = true", a, a)
		}
		for _, b := range set {
			if less(a, b) && less(b, a) {
				t.Errorf("asymmetry: %q and %q are each less than the other", a, b)
			}
			for _, c := range set {
				if less(a, b) && less(b, c) && !less(a, c) {
					t.Errorf("transitivity: %q < %q < %q but %q is not less than %q", a, b, c, a, c)
				}
				if equiv(a, b) && equiv(b, c) && !equiv(a, c) {
					t.Errorf("incomparability: %q ~ %q ~ %q but %q and %q are comparable", a, b, c, a, c)
				}
			}
		}
	}
}

// Every permutation of the issue's set sorts to the one order. 7! = 5040
// sorts, each of seven items.
func TestVersionLess_EveryPermutationSortsAlike(t *testing.T) {
	want := strings.Join(mixedVersions, " ")
	perm := append([]string(nil), mixedVersions...)
	n := 0
	var heap func(k int)
	heap = func(k int) {
		if k == 1 {
			n++
			got := append([]string(nil), perm...)
			sort.Slice(got, func(i, j int) bool { return versionLess(got[i], got[j]) })
			if !sort.SliceIsSorted(got, func(i, j int) bool { return versionLess(got[i], got[j]) }) {
				t.Fatalf("permutation %v: not sorted after the sort: %v", perm, got)
			}
			if s := strings.Join(got, " "); s != want {
				t.Fatalf("permutation %v sorted to %q, want %q", perm, s, want)
			}
			return
		}
		for i := 0; i < k; i++ {
			heap(k - 1)
			if k%2 == 0 {
				perm[i], perm[k-1] = perm[k-1], perm[i]
			} else {
				perm[0], perm[k-1] = perm[k-1], perm[0]
			}
		}
	}
	heap(len(perm))
	if n != 5040 {
		t.Fatalf("visited %d permutations, want 5040", n)
	}
}

func mixedRegistry(t *testing.T, order []int) *TypeRegistry {
	t.Helper()
	r := NewTypeRegistry()
	for _, i := range order {
		for _, name := range []string{"webapp.acme.example", "aaa.acme.example"} {
			if err := r.Register(TypeID{Name: name, Version: mixedVersions[i]}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return r
}

// IDs is ordered by name, then by version, whatever order the versions were
// registered in (and whatever order the registry's maps iterate in).
func TestTypeRegistry_IDsOrderIsStable(t *testing.T) {
	var want []string
	for _, name := range []string{"aaa.acme.example", "webapp.acme.example"} {
		for _, v := range mixedVersions {
			want = append(want, name+"@"+v)
		}
	}
	orders := [][]int{{0, 1, 2, 3, 4, 5, 6}, {6, 5, 4, 3, 2, 1, 0}, {3, 0, 6, 1, 5, 2, 4}, {2, 4, 6, 0, 1, 3, 5}}
	for _, order := range orders {
		for range 20 { // fresh maps iterate in a fresh order each time
			var got []string
			for _, id := range mixedRegistry(t, order).IDs() {
				got = append(got, id.String())
			}
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("registered in order %v, IDs() = %v, want %v", order, got, want)
			}
		}
	}
}

// The version list in a refusal is in the same order.
func TestResolveTypeReference_VersionListIsOrdered(t *testing.T) {
	var ss []string
	for _, i := range []int{3, 6, 0, 5, 2, 4, 1} {
		ss = append(ss, "webapp.acme.example@"+mixedVersions[i])
	}
	reg := mustIDs(t, ss...)
	list := strings.Join(mixedVersions, ", ")
	for name, tc := range map[string]struct {
		ref  string
		want string
	}{
		"omitted version, many registered": {"webapp", "(" + list + ")"},
		"unregistered version":             {"webapp@v3", "(registered: " + list + ")"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveTypeReference(tc.ref, reg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ResolveTypeReference(%q) error = %v, want it to contain %q", tc.ref, err, tc.want)
			}
		})
	}
}
