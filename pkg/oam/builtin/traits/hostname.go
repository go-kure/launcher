package traits

import (
	"fmt"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// clusterIssuerAnnotation is the cert-manager ingress-shim annotation that names
// the ClusterIssuer used to provision the ingress TLS certificate.
const clusterIssuerAnnotation = "cert-manager.io/cluster-issuer"

// sslRedirectAnnotation / forceSSLRedirectAnnotation are the nginx-ingress
// annotations the expose trait writes from its sslRedirect / forceSslRedirect
// properties (platform default via capability rendering, override-able inline).
const (
	sslRedirectAnnotation      = "nginx.ingress.kubernetes.io/ssl-redirect"
	forceSSLRedirectAnnotation = "nginx.ingress.kubernetes.io/force-ssl-redirect"
)

// nginx external-auth (oauth2-proxy) annotations the expose trait writes from its
// allowedGroups / authSigninURL properties plus the capability auth-url / auth-response-headers.
const (
	authURLAnnotation             = "nginx.ingress.kubernetes.io/auth-url"
	authSigninAnnotation          = "nginx.ingress.kubernetes.io/auth-signin"
	authResponseHeadersAnnotation = "nginx.ingress.kubernetes.io/auth-response-headers"
)

// matchHostnameWildcard reports whether host is permitted by pattern. An empty
// pattern permits everything. A pattern without a leading "*." must match host
// exactly. A "*.suffix" pattern matches exactly one DNS label in place of the
// star: "*.apps.example.com" matches "foo.apps.example.com" but not
// "a.b.apps.example.com" or "apps.example.com".
func matchHostnameWildcard(pattern, host string) bool {
	if pattern == "" {
		return true
	}
	if !strings.HasPrefix(pattern, "*.") {
		return host == pattern
	}
	suffix := pattern[1:] // ".apps.example.com"
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := host[:len(host)-len(suffix)]
	return label != "" && !strings.Contains(label, ".")
}

// validateHostnames returns a *errors.ValidationError for the first host not
// permitted by wildcard. An empty wildcard skips validation.
func validateHostnames(hosts []string, wildcard, component string) error {
	if wildcard == "" {
		return nil
	}
	for _, h := range hosts {
		if !matchHostnameWildcard(wildcard, h) {
			return &errors.ValidationError{
				Field:     "hostname",
				Value:     h,
				Component: component,
				Message: fmt.Sprintf("hostname %q is not permitted by allowedHostnameWildcard %q",
					h, wildcard),
			}
		}
	}
	return nil
}

// uniqueStrings returns s with duplicates removed, preserving first-seen order.
func uniqueStrings(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
