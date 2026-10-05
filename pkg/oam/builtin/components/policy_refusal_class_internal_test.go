package components

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPolicyRefusalClass_RedirectRefusal: a manifest source refuses a redirect
// to a host outside the allowed registries while it fetches, which is not an
// ApplyPolicy and so not a violation; the error still carries the registry
// refusal, in the fixed text it had. A redirect refused for its scheme is the
// source's own rule, and carries none.
func TestPolicyRefusalClass_RedirectRefusal(t *testing.T) {
	redirectTo := func(location string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	otherHost := redirectTo("https://other.example/x")
	s := &manifestSource{url: otherHost.URL}
	s.setAllowedHosts([]string{hostOf(otherHost.URL)})
	_, err := s.resolve()
	if want := "manifest source: fetch \"" + otherHost.URL + "\": redirect to a host not in allowed registries refused"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	var r *oam.PolicyRefusal
	if !errors.As(err, &r) {
		t.Fatalf("the redirect refusal carries no *oam.PolicyRefusal: %v", err)
	}
	if r.Class != oam.RefusalRegistry {
		t.Errorf("the redirect refusal has class %q, want %q", r.Class, oam.RefusalRegistry)
	}

	badScheme := redirectTo("ftp://example.com/x")
	s = &manifestSource{url: badScheme.URL}
	_, err = s.resolve()
	if err == nil {
		t.Fatal("a redirect to a url that is not http(s) was followed")
	}
	if errors.As(err, &r) {
		t.Errorf("a redirect refused for its scheme carries a refusal of class %q: %v", r.Class, err)
	}
}
