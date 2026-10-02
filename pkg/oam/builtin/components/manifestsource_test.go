package components

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// fakePolicy implements oam.Policy via an embedded (nil) interface and overrides
// only AllowedRegistries — proving ApplyPolicy reads the interface method, not a
// concrete policy type.
type fakePolicy struct {
	oam.Policy
	allowed []string
}

func (f fakePolicy) AllowedRegistries() []string { return f.allowed }

func TestApplyPolicy_UsesInterfaceAndStoresAllowlist(t *testing.T) {
	deny := &manifestConfig{src: &manifestSource{url: "https://evil.example.com/x.yaml"}}
	if err := deny.ApplyPolicy(fakePolicy{allowed: []string{"trusted.example.com"}}); err == nil {
		t.Error("want host denial through the Policy interface")
	}

	ok := &manifestConfig{src: &manifestSource{url: "https://trusted.example.com/x.yaml"}}
	if err := ok.ApplyPolicy(fakePolicy{allowed: []string{"trusted.example.com"}}); err != nil {
		t.Errorf("allowed host should pass: %v", err)
	}
	if len(ok.src.allowedHosts) != 1 {
		t.Error("ApplyPolicy must store the allowlist for redirect revalidation")
	}

	if err := (&manifestConfig{src: &manifestSource{url: "https://x/y"}}).ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy must be a no-op, got %v", err)
	}
}

const crdYAML = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
`

// --- parseManifestSource ---

func TestParseManifestSource_InlineOnly(t *testing.T) {
	s, err := parseManifestSource(map[string]any{"inline": crdYAML})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.inline == "" || s.url != "" {
		t.Errorf("want inline set, url empty; got inline=%q url=%q", s.inline, s.url)
	}
}

func TestParseManifestSource_URLOnly(t *testing.T) {
	s, err := parseManifestSource(map[string]any{"url": "https://example.com/crds.yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.url == "" || s.inline != "" {
		t.Error("want url set, inline empty")
	}
}

func TestParseManifestSource_RejectsBothSources(t *testing.T) {
	_, err := parseManifestSource(map[string]any{"inline": crdYAML, "url": "https://x/y.yaml"})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("want 'exactly one' error, got %v", err)
	}
}

func TestParseManifestSource_RejectsNoSource(t *testing.T) {
	_, err := parseManifestSource(map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("want 'exactly one' error, got %v", err)
	}
}

func TestParseManifestSource_UnknownPropertyDistinctFromUnsupportedSource(t *testing.T) {
	_, err := parseManifestSource(map[string]any{"inline": crdYAML, "bogus": "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown property") {
		t.Errorf("want 'unknown property' error, got %v", err)
	}
	_, err = parseManifestSource(map[string]any{"chart": map[string]any{"name": "x"}})
	if err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("want 'not yet supported' for chart, got %v", err)
	}
}

func TestParseManifestSource_RejectsOCIAndUnknownScheme(t *testing.T) {
	if _, err := parseManifestSource(map[string]any{"url": "oci://r/x:1"}); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("want 'not yet supported' for oci, got %v", err)
	}
	if _, err := parseManifestSource(map[string]any{"url": "file:///etc/passwd"}); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Errorf("want scheme error for file://, got %v", err)
	}
}

// TestParseManifestSource_UnparsableURLHidesCredential pins that the refusal of
// a url that does not parse repeats neither the url nor url.Parse's error, both
// of which would print a credential in its userinfo.
func TestParseManifestSource_UnparsableURLHidesCredential(t *testing.T) {
	for _, rawURL := range []string{
		"https://deploy:s3cr3t%zz@example.com/x.yaml", // bad escape in the password
		"https://deploy:s3cr3t@exa mple.com/x.yaml",   // bad host
	} {
		_, err := parseManifestSource(map[string]any{"url": rawURL})
		if err == nil || err.Error() != "manifest source: url is not a valid URL" {
			t.Errorf("url %q: want the bare invalid-url refusal, got %v", rawURL, err)
			continue
		}
		if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "deploy") {
			t.Errorf("url %q: error %q repeats the credential", rawURL, err)
		}
	}
}

// --- resolve (inline + caching) ---

func TestResolve_InlineParsesAndCachesDefensiveCopies(t *testing.T) {
	s, err := parseManifestSource(map[string]any{"inline": crdYAML})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(a) != 1 || a[0].GetObjectKind().GroupVersionKind().Kind != "CustomResourceDefinition" {
		t.Fatalf("want one CRD, got %d", len(a))
	}
	// Mutate the first result; a second resolve must be unaffected (defensive copy).
	a[0].SetName("mutated")
	b, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve#2: %v", err)
	}
	if b[0].GetName() != "widgets.example.com" {
		t.Errorf("resolve must return defensive copies; got mutated name %q", b[0].GetName())
	}
}

// --- url fetch safety ---

func TestResolve_URLSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(crdYAML))
	}))
	defer srv.Close()
	s := &manifestSource{url: srv.URL}
	objs, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve url: %v", err)
	}
	if len(objs) != 1 {
		t.Errorf("want 1 object, got %d", len(objs))
	}
}

func TestResolve_URLRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := &manifestSource{url: srv.URL}
	if _, err := s.resolve(); err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("want status error, got %v", err)
	}
}

func TestResolve_URLRejectsOversizedBody(t *testing.T) {
	orig := maxManifestBytes
	maxManifestBytes = 64
	defer func() { maxManifestBytes = orig }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 1000)))
	}))
	defer srv.Close()
	s := &manifestSource{url: srv.URL}
	if _, err := s.resolve(); err == nil || !strings.Contains(err.Error(), "size") {
		t.Errorf("want size error, got %v", err)
	}
}

func TestResolve_URLRejectsRedirectToDisallowedHost(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(crdYAML))
	}))
	defer dest.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	defer redir.Close()

	// Allowlist only the redirecting host; the redirect target is disallowed.
	s := &manifestSource{url: redir.URL}
	s.setAllowedHosts([]string{hostOf(redir.URL)})
	if _, err := s.resolve(); err == nil {
		t.Error("want error when a redirect leaves the allowed host")
	}
}

// TestResolve_URLErrorsHideCredential pins that a fetch error names the url
// without its userinfo, user included, and without its query, while keeping
// host and path: the non-2xx refusal, and a transport failure, whose *url.Error
// from net/http names the url with only the password masked.
func TestResolve_URLErrorsHideCredential(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	withCred := func(base, userinfo, rest string) string {
		return strings.Replace(base, "://", "://"+userinfo+"@", 1) + rest
	}
	cases := []struct {
		name, url, want string
	}{
		{"404, user and token", withCred(notFound.URL, "deploy:s3cr3t", "/crds.yaml"), notFound.URL + "/crds.yaml\": unexpected status 404"},
		{"404, token as user", withCred(notFound.URL, "s3cr3t", "/crds.yaml"), notFound.URL + "/crds.yaml\": unexpected status 404"},
		{"404, signed query", notFound.URL + "/crds.yaml?sig=s3cr3t", notFound.URL + "/crds.yaml\": unexpected status 404"},
		{"transport failure, user and token", withCred(closedURL, "deploy:s3cr3t", "/crds.yaml"), "manifest source: fetch \"" + closedURL + "/crds.yaml\": "},
		{"transport failure, token as user", withCred(closedURL, "s3cr3t", "/crds.yaml"), "manifest source: fetch \"" + closedURL + "/crds.yaml\": "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&manifestSource{url: tc.url}).resolve()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "deploy") {
				t.Errorf("error %q repeats the credential", err)
			}
		})
	}
}

func hostOf(rawURL string) string {
	if _, after, ok := strings.Cut(rawURL, "://"); ok {
		return after
	}
	return rawURL
}
