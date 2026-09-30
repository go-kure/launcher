package components

import "testing"

// TestOCINamesRegistry pins the explicit-registry rule go-containerregistry's
// name.NewRepository applies (go-kure/launcher#580): the first segment is the
// registry only when it is localhost or contains "." or ":" and, for a
// repository url, a non-empty path follows it.
func TestOCINamesRegistry(t *testing.T) {
	cases := []struct {
		value             string
		requireRepository bool
		want              bool
	}{
		{"oci://ghcr.io/org/app", true, true},
		{"oci://registry.example:5000/org/app", true, true},
		{"oci://localhost/app", true, true},
		{"oci://localhost:5000/app", true, true},
		{"oci://ghcr.io", true, false},
		{"oci://ghcr.io/", true, false},
		{"oci://registry/my-artifact", true, false},
		{"oci://library/app", true, false},
		{"oci://", true, false},
		{"https://ghcr.io/org/app", true, false},
		{"ghcr.io/org/app", true, false},
		// Without requireRepository the url may stop at the registry.
		{"oci://ghcr.io", false, true},
		{"oci://ghcr.io/", false, true},
		{"oci://registry", false, false},
	}
	for _, tc := range cases {
		if got := ociNamesRegistry(tc.value, tc.requireRepository); got != tc.want {
			t.Errorf("ociNamesRegistry(%q, %v) = %v, want %v", tc.value, tc.requireRepository, got, tc.want)
		}
	}
}
