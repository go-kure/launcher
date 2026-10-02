package components

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/errors"
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

// TestResolve_URLErrorsHideCredential pins that a fetch error names the url by
// scheme and host only, without its userinfo, user included, its path or its
// query, in every error class: the non-2xx refusal; a transport failure, whose
// *url.Error from net/http names the url with only the password masked; a body
// read; the size cap; a malformed redirect Location, which net/http quotes
// whole; and an opaque url, which parses with its userinfo in Opaque.
func TestResolve_URLErrorsHideCredential(t *testing.T) {
	orig := maxManifestBytes
	maxManifestBytes = 64
	defer func() { maxManifestBytes = orig }()
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	// hangUp closes every connection without a response: a transport failure
	// that does not depend on a released port staying free.
	hangUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer hangUp.Close()
	badRedirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://deploy:s3cr3t@example.com/x?sig=s3cr3t#bad%zz")
		w.WriteHeader(http.StatusFound)
	}))
	defer badRedirect.Close()
	// badTrailer answers 200 with a chunked body whose trailer is malformed and
	// reflects the request's query, which net/http's read error would quote.
	badTrailer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n" + r.URL.RawQuery + "\r\n\r\n")
		_ = buf.Flush()
	}))
	defer badTrailer.Close()
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 1000)))
	}))
	defer oversized.Close()

	withCred := func(base, userinfo, rest string) string {
		return strings.Replace(base, "://", "://"+userinfo+"@", 1) + rest
	}
	// tokenPath is a capability URL's path, the token being the path segment.
	const tokenPath = "/download/s3cr3t/crds.yaml"
	cases := []struct {
		name, url, want string
	}{
		{"404, user and token", withCred(notFound.URL, "deploy:s3cr3t", "/crds.yaml"), "manifest source: fetch \"" + notFound.URL + "\": unexpected status 404"},
		{"404, token as user", withCred(notFound.URL, "s3cr3t", "/crds.yaml"), "manifest source: fetch \"" + notFound.URL + "\": unexpected status 404"},
		{"404, signed query", notFound.URL + "/crds.yaml?sig=s3cr3t", "manifest source: fetch \"" + notFound.URL + "\": unexpected status 404"},
		{"404, token in path", notFound.URL + tokenPath, "manifest source: fetch \"" + notFound.URL + "\": unexpected status 404"},
		{"transport failure, user and token", withCred(hangUp.URL, "deploy:s3cr3t", "/crds.yaml"), "manifest source: fetch \"" + hangUp.URL + "\": request failed"},
		{"transport failure, token as user", withCred(hangUp.URL, "s3cr3t", "/crds.yaml"), "manifest source: fetch \"" + hangUp.URL + "\": request failed"},
		{"transport failure, token in path", hangUp.URL + tokenPath, "manifest source: fetch \"" + hangUp.URL + "\": request failed"},
		{"malformed redirect location", badRedirect.URL + "/crds.yaml", "manifest source: fetch \"" + badRedirect.URL + "\": request failed"},
		{"opaque url", "https:deploy:s3cr3t@example.com/crds.yaml", "manifest source: fetch \"(url without a host)\": request failed"},
		{"malformed trailer reflecting the query", badTrailer.URL + "/crds.yaml?sig=s3cr3t", "manifest source: read \"" + badTrailer.URL + "\": response body could not be read"},
		{"body read, token in path", badTrailer.URL + tokenPath + "?malformed", "manifest source: read \"" + badTrailer.URL + "\": response body could not be read"},
		{"size cap, token in path", oversized.URL + tokenPath, "manifest source: \"" + oversized.URL + "\" response exceeds max size 64 bytes"},
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

// TestResolve_RedirectRefusalHidesTarget pins that a refused redirect hop is
// reported by fixed text: the target is the server's, and its scheme, host or
// userinfo can carry a reflected credential.
func TestResolve_RedirectRefusalHidesTarget(t *testing.T) {
	redirectTo := func(location string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		}))
	}
	badScheme := redirectTo("s3cr3t://example.com/x")
	defer badScheme.Close()
	otherHost := redirectTo("https://deploy:s3cr3t@other.example/x")
	defer otherHost.Close()

	cases := []struct {
		name, url string
		allowed   []string
		want      string
	}{
		{"scheme", badScheme.URL, nil, "manifest source: fetch \"" + badScheme.URL + "\": redirect to a url that is not http(s) refused"},
		{"host", otherHost.URL, []string{hostOf(otherHost.URL)}, "manifest source: fetch \"" + otherHost.URL + "\": redirect to a host not in allowed registries refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &manifestSource{url: tc.url}
			s.setAllowedHosts(tc.allowed)
			_, err := s.resolve()
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "deploy") {
				t.Errorf("error %q repeats the redirect target", err)
			}
		})
	}
}

// TestResolve_FetchErrorKeepsCause pins that a fetch or read error, whose text
// is fixed, still carries its cause for errors.Is and errors.As.
func TestResolve_FetchErrorKeepsCause(t *testing.T) {
	orig := fetchTimeout
	fetchTimeout = 50 * time.Millisecond
	defer func() { fetchTimeout = orig }()
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)

	_, err := (&manifestSource{url: slow.URL + "/download/s3cr3t/x.yaml"}).resolve()
	if err == nil {
		t.Fatal("want a timeout, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %q: errors.Is(context.DeadlineExceeded) = false, want true", err)
	}
	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Errorf("error %q: want a net.Error that is a timeout", err)
	}
	if want := "manifest source: fetch \"" + slow.URL + "\": timed out"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	badTrailer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\nmalformed\r\n\r\n")
		_ = buf.Flush()
	}))
	defer badTrailer.Close()
	_, err = (&manifestSource{url: badTrailer.URL}).resolve()
	if err == nil || !strings.Contains(err.Error(), "manifest source: read ") {
		t.Fatalf("error = %v, want a body read failure", err)
	}
	var ferr *fetchError
	if !errors.As(err, &ferr) || ferr.Unwrap() == nil {
		t.Errorf("error %q: read failure dropped its cause", err)
	}
}

// TestDisplayHost pins what displayHost and displayURL keep of a host: what
// follows the userinfo, up to a query or fragment, without an IPv6 zone, port
// included.
func TestDisplayHost(t *testing.T) {
	cases := []struct {
		in, want string
		trimmed  bool
	}{
		{"example.com", "example.com", false},
		{"example.com:8443", "example.com:8443", false},
		{"[fe80::1]:8443", "[fe80::1]:8443", false},
		{"deploy:s3cr3t@example.com", "example.com", true},
		{"s3cr3t@example.com:9000", "example.com:9000", true},
		{"example.com?sig=s3cr3t", "example.com", true},
		{"example.com#s3cr3t", "example.com", true},
		{"[fe80::1%s3cr3t]:8443", "[fe80::1]:8443", true},
		{"[fe80::1%25s3cr3t]", "[fe80::1]", true},
		{"fe80::1%s3cr3t", "fe80::1", true},
	}
	for _, tc := range cases {
		got, trimmed := displayHost(tc.in)
		if got != tc.want || trimmed != tc.trimmed {
			t.Errorf("displayHost(%q) = %q, %v, want %q, %v", tc.in, got, trimmed, tc.want, tc.trimmed)
		}
	}

	urls := []struct{ in, want string }{
		{"https://deploy:s3cr3t@example.com:8443/a/s3cr3t?sig=s3cr3t#s3cr3t", "https://example.com:8443"},
		{"https://[fe80::1%25s3cr3t]:8443/x.yaml", "https://[fe80::1]:8443"},
		{"https:deploy:s3cr3t@example.com/x", "(url without a host)"},
	}
	for _, tc := range urls {
		if got := displayURL(tc.in); got != tc.want {
			t.Errorf("displayURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFailureCause pins that a failure is named by fixed text only, never by
// the error's own text, which can quote the URL, a server-sent header or
// trailer, or a TLS certificate.
func TestFailureCause(t *testing.T) {
	const leaky = "https://deploy:s3cr3t@example.com/x?sig=s3cr3t"
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"dial refused", &url.Error{Op: "Get", URL: leaky, Err: &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}}, "dial failed: connection refused"},
		{"host lookup", &url.Error{Op: "Get", URL: leaky, Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}}}, "host lookup failed"},
		{"proxy TLS failure quoting a certificate", &url.Error{Op: "Get", URL: leaky, Err: &net.OpError{Op: "proxyconnect", Net: "tcp", Addr: addr, Err: errors.Errorf("tls: certificate is valid for %s, not localhost", leaky)}}, "proxyconnect failed"},
		{"timeout", &url.Error{Op: "Get", URL: leaky, Err: context.DeadlineExceeded}, "timed out"},
		{"malformed Location", &url.Error{Op: "Get", URL: leaky, Err: errors.Errorf("failed to parse Location header %q", leaky)}, ""},
		{"malformed trailer", errors.Errorf("malformed MIME header: missing colon: %q", "sig=s3cr3t"), ""},
	}
	for _, tc := range cases {
		if got := failureCause(tc.err); got != tc.want {
			t.Errorf("%s: failureCause = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func hostOf(rawURL string) string {
	if _, after, ok := strings.Cut(rawURL, "://"); ok {
		return after
	}
	return rawURL
}
