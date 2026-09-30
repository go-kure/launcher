package components

import (
	"testing"
)

// TestURLHost pins the host urlHost extracts, which enforceAllowedURLHosts
// compares for equality with each allowlist entry. The rows fall in two groups.
//
// The forms the oci, crd and manifests components already passed in — oci://,
// https:// and http:// URLs — keep exactly the result they had before the Flux
// source components (go-kure/launcher#347) extended the function: the port stays
// part of the host, and anything that is not a bare host (userinfo, a query
// before the path) is left in place, so it matches no entry and is refused.
//
// The new forms are ssh:// (a GitRepository url), whose user is dropped and whose
// host ends at "/", "?" or "#" as net/url splits it, and a scheme-less Bucket
// endpoint, which is its own host.
func TestURLHost(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// Existing callers.
		{"oci", "oci://ghcr.io/org/chart", "ghcr.io"},
		{"oci with port", "oci://registry.local:5000/org/chart", "registry.local:5000"},
		{"oci host only", "oci://ghcr.io", "ghcr.io"},
		{"https", "https://charts.example.com/stable/index.yaml", "charts.example.com"},
		{"https host only", "https://charts.example.com", "charts.example.com"},
		{"http with port", "http://charts.example.com:8080/x", "charts.example.com:8080"},
		{"https userinfo kept", "https://user@charts.example.com/x", "user@charts.example.com"},
		{"https query before path kept", "https://evil.example.com?/charts.example.com", "evil.example.com?"},
		{"unknown scheme kept", "ftp://charts.example.com/x", "ftp:"},

		// Bucket endpoints, scheme-less or with a scheme.
		{"endpoint host", "minio.example.com", "minio.example.com"},
		{"endpoint host and port", "minio.example.com:9000", "minio.example.com:9000"},
		{"endpoint with https", "https://account.blob.core.windows.net", "account.blob.core.windows.net"},

		// GitRepository ssh:// URLs.
		{"ssh with user", "ssh://git@github.com/org/repo", "github.com"},
		{"ssh with user and port", "ssh://git@gitea.example.com:2222/org/repo.git", "gitea.example.com:2222"},
		{"ssh without user", "ssh://github.com/org/repo", "github.com"},
		{"ssh user with a dot", "ssh://evil.example.com@github.com/org/repo", "github.com"},
		{"ssh at-sign in path", "ssh://evil.example.com/x@github.com", "evil.example.com"},
		{"ssh at-sign in query", "ssh://evil.example.com?@github.com/x", "evil.example.com"},
		{"ssh at-sign in fragment", "ssh://evil.example.com#@github.com/x", "evil.example.com"},
		{"ssh host only", "ssh://git@github.com", "github.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := urlHost(tc.in); got != tc.want {
				t.Errorf("urlHost(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
