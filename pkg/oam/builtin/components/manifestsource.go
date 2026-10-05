package components

import (
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	// Sorted for the same reason as stringMapStrict in common.go: four of the
	// arms below return an error naming k, so `{chart: ..., bogus: ...}` would
	// otherwise report "not yet supported" or "unknown property" at random.
	for _, k := range slices.Sorted(maps.Keys(props)) {
		v := props[k]
		if (k == "inline" || k == "url") && isExplicitNull(v) {
			continue // an explicit null reads as omission
		}
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
// "not yet supported" message. An unparsable URL is refused without the URL and
// without url.Parse's error, whose text repeats it, credential included.
func validateURLScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.Errorf("manifest source: url is not a valid URL")
	}
	switch u.Scheme {
	case "http", "https":
		return nil
	case "oci":
		return errors.Errorf("manifest source: oci:// urls are not yet supported (designed, not implemented)")
	default:
		// The scheme is not quoted: it is authored text, so it can hold anything.
		return errors.New("manifest source: unsupported url scheme (only http/https)")
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
		// The decode template delivery gives a rendered chart: a workload or a
		// claim that sets a field its API type does not declare is refused, and
		// any other registered kind that does is emitted as written.
		objs, err := decodeManifestDocuments(data)
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
// response-size cap. Its errors name the URL only as displayURL renders it.
func fetchURL(rawURL string, allowedHosts []string) ([]byte, error) {
	if err := checkURL(rawURL, allowedHosts); err != nil {
		return nil, err
	}
	shown := displayURL(rawURL)
	// redirectRefusal is this function's own refusal of a redirect hop, kept so
	// it can be reported in place of the client's error, which wraps it. It is
	// fixed text: the redirect target is the server's, and its scheme, host or
	// userinfo can carry anything, a reflected credential included.
	var redirectRefusal error
	httpClient := &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			switch target := req.URL.String(); {
			case len(via) >= maxRedirects:
				redirectRefusal = errors.Errorf("too many redirects (>%d)", maxRedirects)
			case validateURLScheme(target) != nil:
				redirectRefusal = errors.New("redirect to a url that is not http(s) refused")
			case enforceAllowedURLHosts(target, allowedHosts) != nil:
				redirectRefusal = errors.New("redirect to a host not in allowed registries refused")
			default:
				redirectRefusal = nil
			}
			return redirectRefusal
		},
	}
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		if redirectRefusal != nil {
			return nil, errors.Errorf("manifest source: fetch %q: %w", shown, redirectRefusal)
		}
		return nil, &fetchError{msg: "manifest source: fetch " + strconv.Quote(shown) + ": " + orDefault(failureCause(err), "request failed"), cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.Errorf("manifest source: fetch %q: unexpected status %d", shown, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, &fetchError{msg: "manifest source: read " + strconv.Quote(shown) + ": " + orDefault(failureCause(err), "response body could not be read"), cause: err}
	}
	if int64(len(body)) > maxManifestBytes {
		return nil, errors.Errorf("manifest source: %q response exceeds max size %d bytes", shown, maxManifestBytes)
	}
	return body, nil
}

// displayURL renders a fetch URL for an error message from its scheme and host,
// port included, only. Every other part can carry a credential: the userinfo,
// user included, since url.URL.Redacted masks only the password and a token is
// as often written as the user (https://<token>@host); the path, where a
// capability URL puts its token (https://host/download/<token>/x.yaml); and the
// query, where a signed URL does. A URL with no host, such as the opaque
// https:user:token@host, which parses with everything after the scheme in
// Opaque, is not rendered at all. The host itself goes through displayHost,
// which drops an IPv6 zone.
func displayURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "(url without a host)"
	}
	host, _ := displayHost(u.Host)
	if host == "" {
		return "(url whose host is not shown)"
	}
	return u.Scheme + "://" + host
}

// displayHost reduces a host, as urlHost or url.URL.Host returns it, to the part
// that is safe to print, or to "" when that part cannot be told apart from the
// rest. The userinfo runs to the last "@" and is dropped, unless "[", "]", "?"
// or "#", none of which a userinfo may hold, precedes that "@": the "@" may
// then sit inside IPv6 brackets or a query or fragment instead, so nothing is
// shown. A query or fragment urlHost keeps is dropped, and so is an
// IPv6 zone ("%" up to the last "]"), since net/url keeps arbitrary zone text
// in Host, "]" included. What is left is shown only when it is a plain host
// (plainHost); anything else, such as a "]" left from a userinfo inside the
// brackets or a port that is not numeric, is not shown at all. trimmed reports
// whether shown differs from host.
func displayHost(host string) (shown string, trimmed bool) {
	shown = host
	at := strings.LastIndex(shown, "@")
	if at >= 0 && strings.ContainsAny(shown[:at], "[]?#") {
		return "", true
	}
	if q := strings.IndexAny(shown, "?#"); q >= 0 {
		shown = shown[:q]
	}
	if at >= 0 {
		shown = shown[at+1:]
	}
	if i := strings.Index(shown, "%"); i >= 0 {
		rest := ""
		if j := strings.LastIndex(shown, "]"); j > i {
			rest = shown[j:]
		}
		shown = shown[:i] + rest
	}
	if !plainHost.MatchString(shown) {
		return "", host != ""
	}
	return shown, shown != host
}

// plainHost matches a host displayHost may print: a DNS name or IPv4 address,
// or a bracketed IPv6 address, with an optional numeric port.
var plainHost = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(:[0-9]+)?$`)

// fetchError is a fetch or read failure whose text is fixed (see failureCause)
// but whose cause stays reachable through errors.Is and errors.As, so a caller
// can still tell a timeout or a refused connection from other failures. Its
// Error never includes the cause's own text.
type fetchError struct {
	msg   string
	cause error
}

func (e *fetchError) Error() string { return e.msg }
func (e *fetchError) Unwrap() error { return e.cause }

// failureCause names why a request or a response read failed using fixed text
// only, or returns "" when it has none. An error's own text is never repeated:
// net/http's *url.Error names the request URL with only the password masked, a
// malformed redirect Location or response trailer is quoted whole, and a nested
// TLS error quotes the server's certificate, any of which can carry the URL's
// credential. Named are a timeout, a failed host lookup, and otherwise the
// failing network operation (dial, read, proxyconnect, …) with its system error
// (connection refused, …).
func failureCause(err error) string {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timed out"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "host lookup failed"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			return opErr.Op + " failed: " + errno.Error()
		}
		return opErr.Op + " failed"
	}
	return ""
}

// orDefault returns cause, or def when cause is empty.
func orDefault(cause, def string) string {
	if cause == "" {
		return def
	}
	return cause
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
	// The refusal names the host as displayHost renders it from the raw
	// authority, not from host: the compared value can carry the url's userinfo,
	// query or IPv6 zone, any of which can hold a credential, and urlHost's own
	// reduction of an ssh:// url could drop the context displayHost needs to
	// tell them apart. Matching above is unchanged, so such a url still fails
	// closed.
	shown, trimmed := displayHost(urlAuthority(rawURL))
	switch {
	case !trimmed:
		return errors.Errorf("source registry %q is not in allowed registries %v", shown, allowed)
	case shown != "":
		return errors.Errorf("source registry %q is not in allowed registries %v; the url's userinfo, IPv6 zone, query or fragment, which no entry matches, is not shown", shown, allowed)
	default:
		return errors.Errorf("source registry is not in allowed registries %v; it is not shown, since the url's userinfo, IPv6 zone, query or fragment cannot be told apart from its host", allowed)
	}
}

// urlHost extracts the host, port included, that a source URL or endpoint names.
// For oci://, https:// and http:// the scheme is stripped and the host ends at
// the first "/". For ssh:// (a GitRepository url) the host ends at the first "/",
// "?" or "#", and the user is dropped at the last "@", both as net/url splits
// them, so ssh://git@github.com/org/repo yields github.com. A value with no
// scheme (a Bucket endpoint, host[:port]) is its own host.
//
// enforceAllowedURLHosts compares the result for equality with each allowlist
// entry, so a value this does not reduce to a bare host — userinfo on an
// oci://, https:// or http:// URL, an unknown scheme — matches no entry and is
// refused: the check fails closed.
func urlHost(rawURL string) string {
	if rest, ok := strings.CutPrefix(rawURL, "ssh://"); ok {
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			rest = rest[:i]
		}
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			rest = rest[i+1:]
		}
		return rest
	}
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

// urlAuthority is the authority a source URL or endpoint names, unreduced: the
// value with its oci://, https://, http:// or ssh:// scheme stripped, up to the
// first "/". Unlike urlHost it keeps the userinfo of an ssh:// url, so
// displayHost sees everything before the host.
func urlAuthority(rawURL string) string {
	for _, scheme := range []string{"oci://", "https://", "http://", "ssh://"} {
		if after, ok := strings.CutPrefix(rawURL, scheme); ok {
			rawURL = after
			break
		}
	}
	before, _, _ := strings.Cut(rawURL, "/")
	return before
}

// ociNamesRegistry reports whether an oci:// url names its registry explicitly:
// its first path segment is localhost or contains "." or ":", and, with
// requireRepository, a "/" and a non-empty repository path follow it. A value
// without the oci:// scheme names none. The segment is not otherwise parsed or
// validated as an OCI reference: userinfo, a query or a fragment stays in the
// compared segment, so it cannot match a plain registry entry, and Flux's own
// parser rejects such a reference.
//
// This is the rule go-containerregistry's name.NewRepository — how Flux's
// source-controller parses an OCIRepository url — uses to pick the registry:
// without an explicit registry, an otherwise valid repository reference
// resolves against Docker Hub, whatever host urlHost returns.
func ociNamesRegistry(value string, requireRepository bool) bool {
	rest, ok := strings.CutPrefix(value, "oci://")
	if !ok {
		return false
	}
	registry, repository, _ := strings.Cut(rest, "/")
	if requireRepository && repository == "" {
		return false
	}
	return registry == "localhost" || strings.ContainsAny(registry, ".:")
}

// manifestConfig is the shared stack.ApplicationConfig behind the crd and
// manifests components. It resolves a manifestSource and runs a per-type
// `process` hook (CRD-only validation, or scope-aware namespace stamping).
type manifestConfig struct {
	name      string
	namespace string
	src       *manifestSource
	process   func(namespace string, objs []client.Object) ([]client.Object, error)

	// policy is the environment policy ApplyPolicy was given, kept so that
	// Generate holds the objects it emits to it. Nil until then.
	policy oam.Policy
}

// ApplyPolicy holds the source and the objects it yields to the environment
// policy. A nil policy checks nothing.
//
// For a url source it stores the policy registry allowlist on the source (so
// the URL resolver's redirect check has policy context) and rejects a
// disallowed configured-url host up front. It reads the allowlist through the
// oam.Policy interface (AllowedRegistries) rather than type-asserting a
// concrete type, so any policy implementation enforces correctly.
//
// The objects are held to the policy an authored workload is held to
// (enforceRenderedObjectPolicy, the check template delivery runs on the
// objects a chart renders): the image, pod security, resource, storage and
// replica rules, on every kind that check reads. An object of any other kind
// passes, a custom resource included, whatever it holds: the pods its
// controller creates are not covered. An inline source is checked here, since
// its objects are already known. A url source is fetched at generation, as it
// was, so its objects are checked there: the policy is kept, and Generate
// checks whatever it emits.
func (c *manifestConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	c.policy = p
	if c.src.url != "" {
		allowed := p.AllowedRegistries()
		c.src.setAllowedHosts(allowed)
		return enforceAllowedURLHosts(c.src.url, allowed)
	}
	objs, err := c.objects()
	if err != nil {
		return err
	}
	return enforceManifestPolicy(objs, p)
}

// enforceManifestPolicy checks every object a manifest source yields against
// p and names the object in what it refuses; each caller adds the component.
func enforceManifestPolicy(objs []client.Object, p oam.Policy) error {
	for _, obj := range objs {
		err := enforceExplicitSecretObject(obj, p)
		if err == nil {
			err = enforceRenderedObjectPolicy(obj, p)
		}
		if err != nil {
			return errors.Wrapf(err, "manifest source: object %s", renderedObjectRef(obj))
		}
	}
	return nil
}

// Generate resolves the source (cached) and applies the per-type process hook.
// Once ApplyPolicy has supplied a policy it holds the objects it is about to
// emit to it, which is where the objects of a url source are first known. A
// refusal here is the component's oam.ViolationError, as the transform reports
// one from ApplyPolicy; a source that cannot be fetched or read keeps its own
// error.
func (c *manifestConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	objs, err := c.objects()
	if err != nil {
		return nil, err
	}
	if c.policy != nil {
		if err := enforceManifestPolicy(objs, c.policy); err != nil {
			return nil, &oam.ViolationError{Component: c.name, Cause: err}
		}
	}
	out := make([]*client.Object, len(objs))
	for i := range objs {
		o := objs[i]
		out[i] = &o
	}
	return out, nil
}

// objects resolves the source (cached) and applies the per-type process hook:
// the objects Generate emits, and the ones ApplyPolicy checks.
func (c *manifestConfig) objects() ([]client.Object, error) {
	objs, err := c.src.resolve()
	if err != nil {
		return nil, err
	}
	if c.process != nil {
		return c.process(c.namespace, objs)
	}
	return objs, nil
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
