package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestURLRefusalsHideCredential pins that no refusal of a source url or
// endpoint prints a credential it carries, at every site that refuses one: the
// policy host allowlist (crd, manifests, oci and the four Flux sources), the
// scheme checks, the explicit-registry rule for oci:// urls, the refusal of a
// credential in a repository source's url, and the Amazon S3 endpoint rule. A
// repository source's url that carries a credential reaches the allowlist and
// the explicit-registry rule only from a config built directly (direct), since
// the parse path refuses it first. Each row's value carries the token "s3cr3t", in the userinfo
// (as a password or as the user), a query or an IPv6 zone; the refusal must
// still happen and show the host without it (the Flux sources also name their
// component and field).
func TestURLRefusalsHideCredential(t *testing.T) {
	handlers := map[string]oam.ComponentHandler{
		"crd":            &components.CRDHandler{},
		"manifests":      &components.ManifestsHandler{},
		"oci":            ociViaRule{},
		"helmrepository": &components.HelmRepositoryHandler{},
		"ocirepository":  &components.OCIRepositoryHandler{},
		"gitrepository":  &components.GitRepositoryHandler{},
		"bucket":         &components.BucketHandler{},
	}
	url := func(u string) map[string]any { return map[string]any{"url": u} }
	ociSrc := func(u string) map[string]any {
		return map[string]any{"source": map[string]any{"url": u}, "version": "0.3.0"}
	}
	bucket := func(endpoint, provider string) map[string]any {
		props := map[string]any{"bucketName": "manifests", "endpoint": endpoint}
		if provider != "" {
			props["provider"] = provider
		}
		return props
	}
	const (
		notAllowed = " is not in allowed registries "
		hidden     = "the url's userinfo, IPv6 zone, query or fragment, which no entry matches, is not shown"
		implicit   = "the url does not name its registry explicitly"
		notShown   = "it is not shown, since the url's userinfo, IPv6 zone, query or fragment cannot be told apart from its host"

		noCredential = "must not carry a user or password"
		// direct suffixes a repository kind whose config the row builds as a
		// library caller does (fluxSrcDirectConfig): the parse path refuses a
		// credential in url first, so only that config reaches ApplyPolicy's own
		// refusal of such a url.
		direct = "built directly"
	)
	cases := []struct {
		name, typ string
		props     map[string]any
		allowed   []string
		want      []string // substrings of the refusal
	}{
		// The policy host allowlist.
		{"crd, user and token", "crd", url("https://deploy:s3cr3t@example.com/crds.yaml"), []string{"example.com"},
			[]string{`source registry "example.com"` + notAllowed, hidden}},
		{"manifests, user and token", "manifests", url("https://deploy:s3cr3t@example.com/x.yaml"), []string{"example.com"},
			[]string{`source registry "example.com"` + notAllowed, hidden}},
		{"manifests, token as user", "manifests", url("https://s3cr3t@example.com/x.yaml"), []string{"example.com"},
			[]string{`source registry "example.com"` + notAllowed}},
		{"manifests, signed query", "manifests", url("https://example.com?sig=s3cr3t"), []string{"example.com"},
			[]string{`source registry "example.com"` + notAllowed, hidden}},
		{"manifests, IPv6 zone", "manifests", url("https://[fe80::1%25s3cr3t]:8443/x.yaml"), []string{"[fe80::1]:8443"},
			[]string{`source registry "[fe80::1]:8443"` + notAllowed, hidden}},
		{"manifests, IPv6 zone holding a bracket", "manifests", url("https://[fe80::1%25zone]s3cr3t]:8443/x.yaml"), []string{"allowed.example"},
			[]string{`source registry "[fe80::1]:8443"` + notAllowed, hidden}},
		{"ocirepository, userinfo holding a ?", "ocirepository " + direct, url("oci://deploy:s3?cr3t@ghcr.io/org/app"), []string{"ghcr.io"},
			[]string{"source registry is not in allowed registries", notShown}},
		{"helmrepository, userinfo holding a #", "helmrepository " + direct, url("oci://deploy:s3#cr3t@ghcr.io/charts"), []string{"ghcr.io"},
			[]string{"helmrepository: url: source registry is not in allowed registries", notShown}},
		{"gitrepository, IPv6 zone holding the userinfo", "gitrepository " + direct, url("https://[fe80::1%zone@s3cr3t]:8443/org/repo"), []string{"[fe80::1]:8443"},
			[]string{"gitrepository: url: source registry is not in allowed registries", notShown}},
		{"gitrepository, IPv6 zone holding the userinfo and a ?", "gitrepository " + direct, url("https://[fe80::1%zone@s3cr3t?x]:8443/org/repo"), []string{"[fe80::1]:8443"},
			[]string{"gitrepository: url: source registry is not in allowed registries", notShown}},
		{"gitrepository ssh, IPv6 zone holding the userinfo", "gitrepository " + direct, url("ssh://[fe80::1%zone@s3cr3t/repo"), []string{"[fe80::1]:22"},
			[]string{"gitrepository: url: source registry is not in allowed registries", notShown}},
		{"gitrepository ssh, user", "gitrepository", url("ssh://deploy@github.com/org/repo"), []string{"gitlab.com"},
			[]string{`gitrepository: url: source registry "github.com"` + notAllowed}},
		{"bucket endpoint, IPv6 zone holding the userinfo", "bucket", bucket("[fe80::1%zone@s3cr3t]:9000", ""), []string{"[fe80::1]:9000"},
			[]string{"bucket: endpoint: source registry is not in allowed registries", notShown}},
		{"helmrepository https, user and token", "helmrepository " + direct, url("https://deploy:s3cr3t@charts.example.com/stable"), []string{"charts.example.com"},
			[]string{"helmrepository: url: ", `source registry "charts.example.com"` + notAllowed}},
		{"helmrepository oci, user and token", "helmrepository " + direct, url("oci://deploy:s3cr3t@ghcr.io/charts"), []string{"ghcr.io"},
			[]string{"helmrepository: url: ", `source registry "ghcr.io"` + notAllowed}},
		{"ocirepository, user and token", "ocirepository " + direct, url("oci://deploy:s3cr3t@ghcr.io/org/x"), []string{"ghcr.io"},
			[]string{"ocirepository: url: ", `source registry "ghcr.io"` + notAllowed}},
		{"gitrepository https, user and token", "gitrepository " + direct, url("https://deploy:s3cr3t@github.com/org/repo"), []string{"github.com"},
			[]string{"gitrepository: url: ", `source registry "github.com"` + notAllowed}},
		{"bucket endpoint, user and token", "bucket", bucket("deploy:s3cr3t@minio.example.com:9000", ""), []string{"minio.example.com:9000"},
			[]string{"bucket: endpoint: ", `source registry "minio.example.com:9000"` + notAllowed}},

		// The scheme checks.
		{"crd, unsupported scheme", "crd", url("s3cr3t://example.com/crds.yaml"), nil,
			[]string{"manifest source: unsupported url scheme (only http/https)"}},
		{"manifests, unsupported scheme", "manifests", url("s3cr3t://example.com/x.yaml"), nil,
			[]string{"manifest source: unsupported url scheme (only http/https)"}},
		{"oci, not oci://", "oci", ociSrc("https://deploy:s3cr3t@ghcr.io/org/app"), nil,
			[]string{"oci: source.url must use the oci:// scheme"}},
		{"helmrepository, unsupported scheme", "helmrepository", url("ftp://deploy:s3cr3t@charts.example.com"), nil,
			[]string{"helmrepository: url must start with http:// or https:// or oci://"}},
		{"helmrepository type oci, not oci://", "helmrepository", map[string]any{"url": "https://deploy:s3cr3t@charts.example.com", "type": "oci"}, nil,
			[]string{"helmrepository: url must start with oci://"}},
		{"ocirepository, not oci://", "ocirepository", url("https://deploy:s3cr3t@ghcr.io/org/x"), nil,
			[]string{"ocirepository: url must start with oci://"}},
		{"gitrepository, unsupported scheme", "gitrepository", url("git://deploy:s3cr3t@github.com/org/repo"), nil,
			[]string{"gitrepository: url must start with"}},

		// The explicit-registry rule: a token as the user leaves a first segment
		// with no "." or ":", which Flux reads as a Docker Hub namespace.
		{"helmrepository, implicit registry", "helmrepository " + direct, url("oci://s3cr3t@registry"), []string{"registry"},
			[]string{"helmrepository: url: " + implicit}},
		{"ocirepository, implicit registry", "ocirepository " + direct, url("oci://s3cr3t@registry/x"), []string{"registry"},
			[]string{"ocirepository: url: " + implicit}},

		// The refusal of a credential in the url of the three repository
		// sources, which the parse path runs before any policy. The oci
		// component's refusal is its ocirepository terminal's.
		{"oci, user and token", "oci", ociSrc("oci://deploy:s3cr3t@ghcr.io/org/app"), []string{"ghcr.io"},
			[]string{"ocirepository: url " + noCredential}},
		{"oci, token as user", "oci", ociSrc("oci://s3cr3t@registry/app"), []string{"registry"},
			[]string{"ocirepository: url " + noCredential}},
		{"oci, userinfo holding a ?", "oci", ociSrc("oci://deploy:s3?cr3t@ghcr.io/org/app"), []string{"ghcr.io"},
			[]string{"ocirepository: url is not a valid URL"}},
		{"helmrepository https, user and token", "helmrepository", url("https://deploy:s3cr3t@charts.example.com/stable"), nil,
			[]string{"helmrepository: url " + noCredential}},
		{"helmrepository oci, token as user", "helmrepository", url("oci://s3cr3t@ghcr.io/charts"), nil,
			[]string{"helmrepository: url " + noCredential}},
		{"helmrepository, userinfo holding a #", "helmrepository", url("oci://deploy:s3#cr3t@ghcr.io/charts"), nil,
			[]string{"helmrepository: url is not a valid URL"}},
		{"ocirepository, user and token", "ocirepository", url("oci://deploy:s3cr3t@ghcr.io/org/x"), nil,
			[]string{"ocirepository: url " + noCredential}},
		{"gitrepository https, user and token", "gitrepository", url("https://deploy:s3cr3t@github.com/org/repo"), nil,
			[]string{"gitrepository: url " + noCredential}},
		{"gitrepository https, an @ and no host", "gitrepository", url("https:///deploy:s3cr3t@github.com/org/repo"), nil,
			[]string{"gitrepository: url holds an @ and is no URL with a host"}},
		{"gitrepository, IPv6 zone holding the userinfo", "gitrepository", url("https://[fe80::1%zone@s3cr3t]:8443/org/repo"), nil,
			[]string{"gitrepository: url is not a valid URL"}},
		{"gitrepository ssh, user and password", "gitrepository", url("ssh://deploy:s3cr3t@github.com/org/repo"), nil,
			[]string{"gitrepository: url must not carry a password"}},
		{"gitrepository ssh, an @ and no host", "gitrepository", url("ssh:///deploy:s3cr3t@github.com/org/repo"), nil,
			[]string{"gitrepository: url holds an @ and is no URL with a host"}},
		{"gitrepository ssh, IPv6 zone holding the userinfo", "gitrepository", url("ssh://[fe80::1%zone@s3cr3t/repo"), nil,
			[]string{"gitrepository: url is not a valid URL"}},

		// The Amazon S3 endpoint rule.
		{"bucket, Amazon S3 endpoint", "bucket", bucket("deploy:s3cr3t@s3.amazonaws.com", "aws"), []string{"s3.amazonaws.com"},
			[]string{`bucket: endpoint: "s3.amazonaws.com" is treated as an Amazon S3 host`}},
		{"bucket, Amazon S3 endpoint, userinfo holding a ?", "bucket", bucket("deploy:s3?cr3t@s3.amazonaws.com", "aws"), []string{"s3.amazonaws.com"},
			[]string{"bucket: endpoint: (host not shown) is treated as an Amazon S3 host"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" ("+tc.typ+")", func(t *testing.T) {
			var err error
			if typ, ok := strings.CutSuffix(tc.typ, " "+direct); ok {
				err = fluxSrcDirectConfig(t, typ, tc.props["url"].(string), "").ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
			} else {
				var cfg stack.ApplicationConfig
				cfg, err = handlers[tc.typ].ToApplicationConfig(&oam.Component{Name: "src", Type: tc.typ, Properties: tc.props}, "demo")
				if err == nil {
					err = cfg.(oam.Enforceable).ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
				}
			}
			if err == nil {
				t.Fatal("want a refusal, got nil")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q, want it to contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "deploy") {
				t.Errorf("error %q repeats the credential", err)
			}
		})
	}
}
