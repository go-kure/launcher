package components

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// URL fetch limits. Vars (not consts) so tests can lower them.
var (
	maxManifestBytes int64 = 5 << 20 // 5 MiB
	maxRedirects           = 10
	fetchTimeout           = 30 * time.Second
)

// manifestSource is the shared core behind the `crd` and `manifests` OAM
// components: exactly one of inline/url (chart is recognized but not yet
// implemented). resolve() parses/fetches once and memoizes, handing back
// defensive copies so repeated Generate() calls and downstream mutators never
// corrupt the cache.
type manifestSource struct {
	inline string
	url    string

	// allowedHosts is the policy registry allowlist, stored by the owning
	// config's ApplyPolicy so the URL resolver (and its redirect check) has
	// policy context at fetch time. Empty means unrestricted (no policy).
	allowedHosts []string

	cached []client.Object // memoized resolve() result
}

// parseManifestSource reads a component's properties strictly: exactly one of
// inline/url must be set; unknown keys and recognized-but-unsupported sources
// (chart, oci:// urls) get distinct errors.
func parseManifestSource(props map[string]any) (*manifestSource, error) {
	s := &manifestSource{}
	count := 0
	for k, v := range props {
		switch k {
		case "inline":
			str, ok := v.(string)
			if !ok {
				return nil, errors.Errorf("manifest source: property %q must be a YAML string", k)
			}
			s.inline = str
			count++
		case "url":
			str, ok := v.(string)
			if !ok {
				return nil, errors.Errorf("manifest source: property %q must be a string", k)
			}
			if err := validateURLScheme(str); err != nil {
				return nil, err
			}
			s.url = str
			count++
		case "chart":
			return nil, errors.Errorf("manifest source: %q source is not yet supported (designed, not implemented)", k)
		default:
			return nil, errors.Errorf("manifest source: unknown property %q", k)
		}
	}
	if count != 1 {
		return nil, errors.Errorf("manifest source: exactly one of inline/url is required (got %d)", count)
	}
	return s, nil
}

// validateURLScheme rejects non-http(s) schemes up front. oci:// gets a distinct
// "not yet supported" message.
func validateURLScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.Errorf("manifest source: invalid url %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "http", "https":
		return nil
	case "oci":
		return errors.Errorf("manifest source: oci:// urls are not yet supported (designed, not implemented)")
	default:
		return errors.Errorf("manifest source: unsupported url scheme %q (only http/https)", u.Scheme)
	}
}

func (s *manifestSource) setAllowedHosts(hosts []string) { s.allowedHosts = hosts }

// resolve parses inline YAML or fetches the URL (once, memoized) and returns
// defensive copies of the resulting objects.
func (s *manifestSource) resolve() ([]client.Object, error) {
	if s.cached == nil {
		var data []byte
		var err error
		switch {
		case s.inline != "":
			data = []byte(s.inline)
		case s.url != "":
			data, err = fetchURL(s.url, s.allowedHosts)
			if err != nil {
				return nil, err
			}
		default:
			return nil, errors.Errorf("manifest source: no source configured")
		}
		objs, err := kureio.ParseYAMLWithOptions(data, kureio.ParseOptions{AllowUnstructured: true})
		if err != nil {
			return nil, errors.Errorf("manifest source: parse manifests: %w", err)
		}
		s.cached = objs
	}
	return copyObjects(s.cached), nil
}

func copyObjects(objs []client.Object) []client.Object {
	out := make([]client.Object, len(objs))
	for i, o := range objs {
		out[i] = o.DeepCopyObject().(client.Object)
	}
	return out
}

// fetchURL retrieves raw manifest YAML over http(s), treating the URL as
// untrusted: scheme allowlist (initial + every redirect hop), host allowlist
// re-checked on each redirect, request timeout, non-2xx rejection, and a hard
// response-size cap.
func fetchURL(rawURL string, allowedHosts []string) ([]byte, error) {
	if err := checkURL(rawURL, allowedHosts); err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.Errorf("manifest source: too many redirects (>%d)", maxRedirects)
			}
			return checkURL(req.URL.String(), allowedHosts)
		},
	}
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, errors.Errorf("manifest source: fetch %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.Errorf("manifest source: fetch %q: unexpected status %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, errors.Errorf("manifest source: read %q: %w", rawURL, err)
	}
	if int64(len(body)) > maxManifestBytes {
		return nil, errors.Errorf("manifest source: %q response exceeds max size %d bytes", rawURL, maxManifestBytes)
	}
	return body, nil
}

// checkURL enforces the scheme allowlist and the policy host allowlist on a URL
// (used for both the initial request and every redirect hop).
func checkURL(rawURL string, allowedHosts []string) error {
	if err := validateURLScheme(rawURL); err != nil {
		return err
	}
	return enforceAllowedURLHosts(rawURL, allowedHosts)
}

// enforceAllowedURLHosts returns an error if the host extracted from a URL is
// not in the allowed registries list. An empty allowed list means all hosts are
// permitted. This is the URL-source counterpart to the image-reference allowlist
// (registryHost / enforceAllowedRegistries) — kept separate because image refs
// default to docker.io and strip tags/digests, which is wrong for fetch URLs.
func enforceAllowedURLHosts(rawURL string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	host := urlHost(rawURL)
	for _, registry := range allowed {
		if host == strings.TrimRight(registry, "/") {
			return nil
		}
	}
	return errors.Errorf("source registry %q is not in allowed registries %v", host, allowed)
}

// urlHost extracts the host from a URL with scheme (oci://, https://, http://).
func urlHost(rawURL string) string {
	for _, scheme := range []string{"oci://", "https://", "http://"} {
		if after, ok := strings.CutPrefix(rawURL, scheme); ok {
			rawURL = after
			break
		}
	}
	if before, _, ok := strings.Cut(rawURL, "/"); ok {
		return before
	}
	return rawURL
}

// manifestConfig is the shared stack.ApplicationConfig behind the crd and
// manifests components. It resolves a manifestSource and runs a per-type
// `process` hook (CRD-only validation, or scope-aware namespace stamping).
type manifestConfig struct {
	name      string
	namespace string
	src       *manifestSource
	process   func(namespace string, objs []client.Object) ([]client.Object, error)
}

// ApplyPolicy stores the policy registry allowlist on the source (so the URL
// resolver's redirect check has policy context) and rejects a disallowed
// configured-url host up front. It reads the allowlist through the oam.Policy
// interface (AllowedRegistries) rather than type-asserting a concrete type, so
// any policy implementation enforces correctly.
func (c *manifestConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil || c.src.url == "" {
		return nil
	}
	allowed := p.AllowedRegistries()
	c.src.setAllowedHosts(allowed)
	return enforceAllowedURLHosts(c.src.url, allowed)
}

// Generate resolves the source (cached) and applies the per-type process hook.
func (c *manifestConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	objs, err := c.src.resolve()
	if err != nil {
		return nil, err
	}
	if c.process != nil {
		objs, err = c.process(c.namespace, objs)
		if err != nil {
			return nil, err
		}
	}
	out := make([]*client.Object, len(objs))
	for i := range objs {
		o := objs[i]
		out[i] = &o
	}
	return out, nil
}

// validateInline runs resolve + process eagerly for inline sources so config
// errors surface at ToApplicationConfig time (offline, fail fast). url sources
// are validated at Generate time, after ApplyPolicy.
func (c *manifestConfig) validateInline() error {
	if c.src.inline == "" {
		return nil
	}
	_, err := c.Generate(nil)
	return err
}
