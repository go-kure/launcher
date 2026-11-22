package components

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// fakeSource is a ContentSource over a map of url to copy. It records every
// key it is asked for and every copy it receives.
type fakeSource struct {
	mu        sync.Mutex
	copies    map[string]oam.Content
	lookupErr error
	storeErr  error
	looked    []oam.ContentKey
	stored    []oam.Content
	storedKey []oam.ContentKey
	// deadlines holds, per Lookup, whether its context carried a deadline.
	deadlines []bool
}

// Lookup returns the copy it holds, with lookupErr beside it where one is set, as
// a faulty source can answer a copy and an error together.
func (f *fakeSource) Lookup(ctx context.Context, key oam.ContentKey) (oam.Content, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.looked = append(f.looked, key)
	_, has := ctx.Deadline()
	f.deadlines = append(f.deadlines, has)
	c, ok := f.copies[key.URL]
	return c, ok, f.lookupErr
}

func (f *fakeSource) Store(_ context.Context, key oam.ContentKey, c oam.Content) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored = append(f.stored, c)
	f.storedKey = append(f.storedKey, key)
	return f.storeErr
}

// sourcePolicy is a Policy that also implements ContentSourcePolicy.
type sourcePolicy struct {
	fakePolicy
	src oam.ContentSource
}

func (p sourcePolicy) ContentSource() oam.ContentSource { return p.src }

func crdServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(crdYAML))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A source that holds the body serves it with the host closed, and the objects
// equal the fetched ones.
func TestResolve_URLServedFromContentSourceWithHostClosed(t *testing.T) {
	srv := crdServer(t)
	fetched, err := (&manifestSource{url: srv.URL}).resolve()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	src := &fakeSource{copies: map[string]oam.Content{srv.URL: {Data: []byte(crdYAML), Hosts: []string{hostOf(srv.URL)}}}}
	srv.Close()

	s := &manifestSource{url: srv.URL}
	s.setContentSource(src)
	got, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve with the host closed: %v", err)
	}
	if !reflect.DeepEqual(fetched, got) {
		t.Errorf("objects from the stored copy differ from the fetched ones")
	}
	if len(src.stored) != 0 {
		t.Errorf("a copy Lookup served must not be stored again, got %d Store calls", len(src.stored))
	}
	if len(src.looked) != 1 || src.looked[0] != (oam.ContentKey{Kind: "url", URL: srv.URL}) {
		t.Errorf("Lookup key = %+v, want kind url with the url as given", src.looked)
	}
}

// The url reaches Lookup and Store exactly as the document gave it, query
// included, so the caller can apply its own rule to a url that holds a token.
func TestResolve_URLReachesSourceAsGiven(t *testing.T) {
	srv := crdServer(t)
	given := srv.URL + "/m.yaml?token=abc"
	src := &fakeSource{}
	s := &manifestSource{url: given}
	s.setContentSource(src)
	if _, err := s.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := oam.ContentKey{Kind: "url", URL: given}
	if len(src.looked) != 1 || src.looked[0] != want || len(src.storedKey) != 1 || src.storedKey[0] != want {
		t.Errorf("Lookup keys %+v, Store keys %+v, want %+v for both", src.looked, src.storedKey, want)
	}
}

// Store receives the body and both hosts of a fetch that redirects across two.
func TestResolve_StoreGetsTheHostsOfARedirectedFetch(t *testing.T) {
	dest := crdServer(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	defer redir.Close()

	src := &fakeSource{}
	s := &manifestSource{url: redir.URL}
	s.setContentSource(src)
	if _, err := s.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(src.stored) != 1 {
		t.Fatalf("Store calls = %d, want 1", len(src.stored))
	}
	if got, want := src.stored[0].Hosts, []string{hostOf(redir.URL), hostOf(dest.URL)}; !reflect.DeepEqual(got, want) {
		t.Errorf("Store hosts = %v, want %v", got, want)
	}
	if string(src.stored[0].Data) != crdYAML {
		t.Errorf("Store got a body that is not the fetched one")
	}
}

// A stored copy whose recorded hosts include one the allowed registries refuse
// is a miss, and the fetch that follows is refused as a direct fetch is: the
// redirect leaves the allowed host.
func TestResolve_StoredCopyWithARefusedHostIsAMissAndTheFetchIsRefused(t *testing.T) {
	dest := crdServer(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	defer redir.Close()

	src := &fakeSource{copies: map[string]oam.Content{redir.URL: {Data: []byte(crdYAML), Hosts: []string{hostOf(redir.URL), hostOf(dest.URL)}}}}
	s := &manifestSource{url: redir.URL}
	s.setAllowedHosts([]string{hostOf(redir.URL)})
	s.setContentSource(src)
	_, err := s.resolve()
	if err == nil || !strings.Contains(err.Error(), "allowed registries") {
		t.Fatalf("want the redirect refused as a direct fetch is, got %v", err)
	}
	if len(src.looked) != 1 {
		t.Errorf("Lookup calls = %d, want 1", len(src.looked))
	}
}

// The same copy is served when every recorded host is allowed.
func TestResolve_StoredCopyWithAllHostsAllowedIsServed(t *testing.T) {
	srv := crdServer(t)
	host := hostOf(srv.URL)
	src := &fakeSource{copies: map[string]oam.Content{srv.URL: {Data: []byte(crdYAML), Hosts: []string{host}}}}
	srv.Close()
	s := &manifestSource{url: srv.URL}
	s.setAllowedHosts([]string{host})
	s.setContentSource(src)
	if _, err := s.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// A copy with no host list was not written by this library, which always
// records at least the first host, so it is a miss: the url is fetched and the
// fetched body is stored with its hosts.
func TestResolve_StoredCopyWithNoHostsIsAMiss(t *testing.T) {
	srv := crdServer(t)
	src := &fakeSource{copies: map[string]oam.Content{srv.URL: {Data: []byte(crdYAML)}}}
	s := &manifestSource{url: srv.URL}
	s.setContentSource(src)
	if _, err := s.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(src.stored) != 1 {
		t.Fatalf("Store calls = %d, want 1: a copy with no hosts is a miss and the url is fetched", len(src.stored))
	}
}

// A url on a host the policy refuses is refused before the source is asked.
func TestResolve_RefusedURLHostIsRefusedBeforeLookup(t *testing.T) {
	src := &fakeSource{copies: map[string]oam.Content{"https://evil.example.com/m.yaml": {Data: []byte(crdYAML)}}}
	s := &manifestSource{url: "https://evil.example.com/m.yaml"}
	s.setAllowedHosts([]string{"trusted.example.com"})
	s.setContentSource(src)
	if _, err := s.resolve(); err == nil {
		t.Fatal("want the url refused")
	}
	if len(src.looked) != 0 {
		t.Errorf("Lookup was asked %d times for a refused url, want 0", len(src.looked))
	}
}

// A stored body over the size cap is a miss even where it is a valid manifest:
// the fetch runs and its body is used. The stored body is the same manifest as
// the served one plus comment lines, so only the size check can refuse it; with
// the cap check removed it would be served and Store would not be called.
func TestResolve_StoredBodyOverTheCapIsAMiss(t *testing.T) {
	srv := crdServer(t)
	over := crdYAML + "\n" + strings.Repeat("# padding padding padding padding\n", int(maxManifestBytes)/30)
	if int64(len(over)) <= maxManifestBytes {
		t.Fatalf("fixture is %d bytes, want over the cap %d", len(over), maxManifestBytes)
	}
	if !parsesAsManifests([]byte(over)) {
		t.Fatal("fixture must be a valid manifest, or the parse check would refuse it")
	}
	src := &fakeSource{copies: map[string]oam.Content{srv.URL: {Data: []byte(over), Hosts: []string{hostOf(srv.URL)}}}}
	s := &manifestSource{url: srv.URL}
	s.setContentSource(src)
	objs, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(objs) != 1 {
		t.Errorf("want the fetched object, got %d", len(objs))
	}
	if len(src.stored) != 1 || string(src.stored[0].Data) != crdYAML {
		t.Errorf("Store calls = %d, want 1 with the fetched body: the fetch ran after the miss", len(src.stored))
	}
}

// The source is asked under a deadline, so a slow source cannot hold a build.
func TestResolve_LookupRunsUnderADeadline(t *testing.T) {
	srv := crdServer(t)
	src := &fakeSource{}
	s := &manifestSource{url: srv.URL}
	s.setContentSource(src)
	if _, err := s.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(src.deadlines) != 1 || !src.deadlines[0] {
		t.Errorf("Lookup context carried a deadline: %v, want one Lookup with a deadline", src.deadlines)
	}
}

// A url whose scheme the component refuses is refused before the source is asked.
func TestResolve_RefusedSchemeIsRefusedBeforeLookup(t *testing.T) {
	src := &fakeSource{}
	s := &manifestSource{url: "ftp://trusted.example.com/m.yaml"}
	s.setContentSource(src)
	if _, err := s.resolve(); err == nil {
		t.Fatal("want the scheme refused")
	}
	if len(src.looked) != 0 {
		t.Errorf("Lookup was asked %d times for a refused scheme, want 0", len(src.looked))
	}
}

// A fetch that stays on one host, redirects included, records that host once.
func TestResolve_StoreGetsARepeatedHostOnce(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(crdYAML))
	}))
	t.Cleanup(srv.Close)
	src := &fakeSource{}
	s := &manifestSource{url: srv.URL + "/start"}
	s.setContentSource(src)
	if _, err := s.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(src.stored) != 1 || !reflect.DeepEqual(src.stored[0].Hosts, []string{hostOf(srv.URL)}) {
		t.Errorf("Store = %+v, want one copy with the host once", src.stored)
	}
}

// A source that answers a usable copy together with an error is a miss: the
// copy is not served and the fetched body is.
func TestResolve_ACopyBesideAnErrorIsNotServed(t *testing.T) {
	srv := crdServer(t)
	stale := strings.Replace(crdYAML, "name:", "name: stale-", 1)
	if stale == crdYAML || !parsesAsManifests([]byte(stale)) {
		t.Fatal("fixture: want a valid manifest that differs from the served one")
	}
	src := &fakeSource{
		copies:    map[string]oam.Content{srv.URL: {Data: []byte(stale), Hosts: []string{hostOf(srv.URL)}}},
		lookupErr: errors.Errorf("lookup failed"),
	}
	s := &manifestSource{url: srv.URL}
	s.setContentSource(src)
	got, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := (&manifestSource{url: srv.URL}).resolve()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a copy beside a Lookup error was served")
	}
}

// A stored body that is not a valid manifest is a miss: the fetch runs.
func TestResolve_StoredBodyThatDoesNotParseIsAMiss(t *testing.T) {
	srv := crdServer(t)
	src := &fakeSource{copies: map[string]oam.Content{srv.URL: {Data: []byte("kind: [unclosed"), Hosts: []string{hostOf(srv.URL)}}}}
	s := &manifestSource{url: srv.URL}
	s.setContentSource(src)
	objs, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(objs) != 1 || len(src.stored) != 1 {
		t.Errorf("objects = %d, Store calls = %d, want the fetched object and one Store", len(objs), len(src.stored))
	}
}

// A Lookup error is a miss and its text, which can repeat the url and a token in
// it, never reaches the result. A Store error is ignored.
func TestResolve_SourceErrorsNeverFailOrLeak(t *testing.T) {
	srv := crdServer(t)
	secret := "tok-9f3a"
	src := &fakeSource{
		lookupErr: errors.Errorf("lookup %s?token=%s failed", srv.URL, secret),
		storeErr:  errors.Errorf("store %s?token=%s failed", srv.URL, secret),
	}
	s := &manifestSource{url: srv.URL + "?token=" + secret}
	s.setContentSource(src)
	got, err := s.resolve()
	if err != nil {
		t.Fatalf("a failing source must not fail the build: %v", err)
	}
	want, err := (&manifestSource{url: srv.URL + "?token=" + secret}).resolve()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("a failing source changed the objects")
	}

	// With the fetch failing too, the error names the fetch only.
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	src2 := &fakeSource{lookupErr: errors.Errorf("lookup %s?token=%s failed", bad.URL, secret)}
	s2 := &manifestSource{url: bad.URL + "?token=" + secret}
	s2.setContentSource(src2)
	_, err = s2.resolve()
	if err == nil {
		t.Fatal("want the fetch error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "lookup") {
		t.Errorf("error repeats the source's error text: %v", err)
	}
}

// After ApplyPolicy, a policy that implements ContentSourcePolicy has its stored
// copy served with the host closed, and a policy that does not fetches the url,
// which fails with the host closed.
func TestApplyPolicy_ContentSourceIsUsedByTheBuild(t *testing.T) {
	srv := crdServer(t)
	host := hostOf(srv.URL)
	src := &fakeSource{copies: map[string]oam.Content{srv.URL: {Data: []byte(crdYAML), Hosts: []string{host}}}}
	srv.Close()

	with := &manifestConfig{src: &manifestSource{url: srv.URL}}
	if err := with.ApplyPolicy(sourcePolicy{fakePolicy: fakePolicy{Policy: &oam.NoopPolicy{}, allowed: []string{host}}, src: src}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	objs, err := with.Generate(nil)
	if err != nil || len(objs) != 1 {
		t.Fatalf("with a source: objects = %d, err = %v, want the stored copy served", len(objs), err)
	}

	without := &manifestConfig{src: &manifestSource{url: srv.URL}}
	if err := without.ApplyPolicy(fakePolicy{Policy: &oam.NoopPolicy{}, allowed: []string{host}}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	if _, err := without.Generate(nil); err == nil {
		t.Error("a policy without ContentSourcePolicy must fetch the url, which fails with the host closed")
	}
}

func TestContentSourceOf(t *testing.T) {
	src := &fakeSource{}
	if got := oam.ContentSourceOf(sourcePolicy{src: src}); got != oam.ContentSource(src) {
		t.Errorf("ContentSourceOf = %v, want the policy's source", got)
	}
	if got := oam.ContentSourceOf(sourcePolicy{}); got != nil {
		t.Errorf("a policy that returns none must give nil, got %v", got)
	}
	if got := oam.ContentSourceOf(&oam.NoopPolicy{}); got != nil {
		t.Errorf("NoopPolicy must give nil, got %v", got)
	}
}
