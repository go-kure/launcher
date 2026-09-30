package components_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// fluxSourceHandler is what every kind-named Flux source handler implements.
type fluxSourceHandler interface {
	oam.ComponentHandler
	oam.PropertySchemaProvider
}

// fluxSourceKind describes one kind-named Flux source component
// (go-kure/launcher#347) for the table-driven tests below.
type fluxSourceKind struct {
	typ      string            // component type
	kind     string            // emitted Kind
	handler  fluxSourceHandler // the handler under test
	spec     reflect.Type      // the source-controller spec type it projects
	required []string          // the properties the CRD requires
	host     string            // the property ApplyPolicy checks
	minimal  string            // the smallest valid properties, as YAML
	full     string            // every top-level spec key, as YAML
}

// fluxSourceKinds lists the four components. The full fixtures write every
// duration in the canonical form metav1.Duration re-encodes to, so the emitted
// spec can be compared with the authored map key for key.
func fluxSourceKinds() []fluxSourceKind {
	return []fluxSourceKind{
		{
			typ: "helmrepository", kind: "HelmRepository", handler: &components.HelmRepositoryHandler{},
			spec: reflect.TypeFor[sourcev1.HelmRepositorySpec](), required: []string{"url"}, host: "url",
			minimal: `url: https://charts.example.com/stable`,
			full: `
url: https://charts.example.com/stable
secretRef: {name: creds}
certSecretRef: {name: tls}
passCredentials: true
interval: 10m0s
insecure: true
timeout: 1m0s
suspend: true
accessFrom:
  namespaceSelectors:
    - matchLabels: {team: web}
type: default
provider: generic
`,
		},
		{
			typ: "ocirepository", kind: "OCIRepository", handler: &components.OCIRepositoryHandler{},
			spec: reflect.TypeFor[sourcev1.OCIRepositorySpec](), required: []string{"url"}, host: "url",
			minimal: `url: oci://ghcr.io/org/manifests`,
			full: `
url: oci://ghcr.io/org/manifests
ref: {tag: v1.2.3, semver: ">=1.0.0", semverFilter: ".*-rc.*", digest: "sha256:0123"}
layerSelector: {mediaType: application/vnd.cncf.flux.content.v1.tar+gzip, operation: copy}
provider: aws
secretRef: {name: regcred}
verify:
  provider: cosign
  secretRef: {name: cosign-pub}
  matchOIDCIdentity:
    - {issuer: "^https://issuer.example.com$", subject: "^org/.*$"}
  trustedRootSecretRef: {name: root}
serviceAccountName: puller
certSecretRef: {name: tls}
proxySecretRef: {name: proxy}
interval: 10m0s
timeout: 1m0s
ignore: "*.md"
insecure: true
suspend: true
`,
		},
		{
			typ: "gitrepository", kind: "GitRepository", handler: &components.GitRepositoryHandler{},
			spec: reflect.TypeFor[sourcev1.GitRepositorySpec](), required: []string{"url"}, host: "url",
			minimal: `url: https://github.com/org/repo`,
			full: `
url: ssh://git@github.com/org/repo
secretRef: {name: ssh-key}
provider: azure
serviceAccountName: cloner
interval: 10m0s
timeout: 1m0s
ref: {branch: main, tag: v1, semver: ">=1", name: refs/heads/main, commit: abc123}
verify: {mode: HEAD, secretRef: {name: pgp}}
proxySecretRef: {name: proxy}
ignore: "*.md"
suspend: true
recurseSubmodules: true
include:
  - {repository: {name: shared}, fromPath: deploy, toPath: shared}
sparseCheckout: [deploy, charts]
`,
		},
		{
			typ: "bucket", kind: "Bucket", handler: &components.BucketHandler{},
			spec: reflect.TypeFor[sourcev1.BucketSpec](), required: []string{"bucketName", "endpoint"}, host: "endpoint",
			minimal: "bucketName: manifests\nendpoint: minio.example.com:9000",
			full: `
provider: generic
bucketName: manifests
endpoint: minio.example.com:9000
sts:
  provider: ldap
  endpoint: https://sts.example.com
  secretRef: {name: ldap}
  certSecretRef: {name: sts-tls}
insecure: true
region: us-east-1
prefix: deploy/
secretRef: {name: creds}
serviceAccountName: reader
certSecretRef: {name: tls}
proxySecretRef: {name: proxy}
interval: 10m0s
timeout: 1m0s
ignore: "*.md"
suspend: true
`,
		},
	}
}

func fluxSrcProps(t *testing.T, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	return m
}

func fluxSrcConfig(t *testing.T, k fluxSourceKind, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "src", Type: k.typ, Properties: props}, "demo")
	if err != nil {
		t.Fatalf("%s: ToApplicationConfig: %v", k.typ, err)
	}
	return cfg
}

// fluxSrcRender renders cfg, under fluxNS when non-empty, and returns the one
// object it emits.
func fluxSrcRender(t *testing.T, cfg stack.ApplicationConfig, fluxNS string) client.Object {
	t.Helper()
	if fluxNS != "" {
		cfg.(interface{ SetFluxNamespace(string) }).SetFluxNamespace(fluxNS)
	}
	objs, err := cfg.Generate(stack.NewApplication("x", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate emitted %d objects, want exactly 1", len(objs))
	}
	return *objs[0]
}

// fluxSrcJSON returns v as generic JSON, numbers exact.
func fluxSrcJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// fluxSrcSpec returns the emitted object's spec as generic JSON.
func fluxSrcSpec(t *testing.T, obj client.Object) map[string]any {
	t.Helper()
	return fluxSrcJSON(t, obj).(map[string]any)["spec"].(map[string]any)
}

func TestFluxSourceHandlers_CanHandle(t *testing.T) {
	kinds := fluxSourceKinds()
	for _, k := range kinds {
		for _, other := range kinds {
			if got := k.handler.CanHandle(other.typ); got != (other.typ == k.typ) {
				t.Errorf("%s handler: CanHandle(%q) = %v", k.typ, other.typ, got)
			}
		}
		if k.handler.CanHandle("oci") {
			t.Errorf("%s handler claims the oci component type", k.typ)
		}
	}
}

// fluxSrcPropertyType is the property type a spec field of type ft encodes as.
func fluxSrcPropertyType(t *testing.T, ft reflect.Type) oam.PropertyType {
	t.Helper()
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	switch {
	case ft == reflect.TypeFor[metav1.Duration](), ft.Kind() == reflect.String:
		return oam.PropertyTypeString
	case ft.Kind() == reflect.Bool:
		return oam.PropertyTypeBoolean
	case ft.Kind() == reflect.Slice:
		return oam.PropertyTypeArray
	case ft.Kind() == reflect.Struct:
		return oam.PropertyTypeObject
	}
	t.Fatalf("unmapped field type %s", ft)
	return ""
}

// TestFluxSourceHandlers_SchemaMatchesSpec ties each published schema to its
// spec struct: exactly the struct's top-level JSON keys, each with the property
// type its Go field encodes as (an array's items too), Required on exactly the
// fields the CRD requires and launcher does not default, and a description on
// every node. A source-controller bump that adds or drops a spec field turns
// this red until the schema follows.
func TestFluxSourceHandlers_SchemaMatchesSpec(t *testing.T) {
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			schema := k.handler.PropertySchema()
			want := map[string]oam.PropertyType{}
			for f := range k.spec.Fields() {
				key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				want[key] = fluxSrcPropertyType(t, f.Type)
				if f.Type.Kind() == reflect.Slice {
					items := schema[key].Items
					if items == nil {
						t.Errorf("array property %q has no items schema", key)
					} else if wantItem := fluxSrcPropertyType(t, f.Type.Elem()); items.Type != wantItem || items.Description == "" {
						t.Errorf("property %q items: type %q description %q, want type %q with a description", key, items.Type, items.Description, wantItem)
					}
				}
			}
			got := map[string]oam.PropertyType{}
			var required []string
			for key, s := range schema {
				got[key] = s.Type
				if s.Description == "" {
					t.Errorf("property %q has no description", key)
				}
				if s.Required {
					required = append(required, key)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("schema keys/types = %v\nwant %v", got, want)
			}
			sort.Strings(required)
			if !slices.Equal(required, k.required) {
				t.Errorf("required properties = %v, want %v", required, k.required)
			}
		})
	}
}

// TestFluxSourceHandlers_EveryFieldReachable is the reflection check of the
// ticket: no field of the spec type is unreachable through the strict decode the
// handler runs (DecodeStrictJSON with no owned keys), against an explicit
// exclusion list per kind that is empty today. The second half shows the check
// is live: had a handler owned a key that shadows a spec field, taking it out of
// the accepted set, the same call reports that field.
func TestFluxSourceHandlers_EveryFieldReachable(t *testing.T) {
	excluded := map[string][]string{
		"helmrepository": {},
		"ocirepository":  {},
		"gitrepository":  {},
		"bucket":         {},
	}
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			want, ok := excluded[k.typ]
			if !ok {
				t.Fatalf("no exclusion list for %s", k.typ)
			}
			if got := builtin.UnreachableJSONFields(k.spec); !slices.Equal(got, want) {
				t.Errorf("unreachable %s fields: %v, want %v", k.spec.Name(), got, want)
			}
			for _, key := range k.required {
				if got := builtin.UnreachableJSONFields(k.spec, key); !slices.Equal(got, []string{key}) {
					t.Errorf("with %q owned, unreachable fields = %v, want [%s]", key, got, key)
				}
			}
		})
	}
}

// TestFluxSourceHandlers_ProjectsEveryField: a map carrying every top-level key
// comes out as the emitted spec unchanged, key for key. The interval default
// does not apply, since the fixture authors one.
func TestFluxSourceHandlers_ProjectsEveryField(t *testing.T) {
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			props := fluxSrcProps(t, k.full)
			if len(props) != len(k.handler.PropertySchema()) {
				t.Fatalf("fixture sets %d keys; it must set all %d", len(props), len(k.handler.PropertySchema()))
			}
			obj := fluxSrcRender(t, fluxSrcConfig(t, k, props), "")
			if got, want := fluxSrcSpec(t, obj), fluxSrcJSON(t, props); !reflect.DeepEqual(any(got), want) {
				gj, _ := json.MarshalIndent(got, "", " ")
				wj, _ := json.MarshalIndent(want, "", " ")
				t.Errorf("emitted spec differs from authored properties\ngot:  %s\nwant: %s", gj, wj)
			}
		})
	}
}

// TestFluxSourceHandlers_Identity: one CR of the kind's GVK, named after the
// component, in the application namespace, or in the Flux namespace once one is
// set.
func TestFluxSourceHandlers_Identity(t *testing.T) {
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			obj := fluxSrcRender(t, fluxSrcConfig(t, k, fluxSrcProps(t, k.minimal)), "")
			gvk := obj.GetObjectKind().GroupVersionKind()
			if gvk.GroupVersion().String() != "source.toolkit.fluxcd.io/v1" || gvk.Kind != k.kind {
				t.Errorf("emitted %s, want source.toolkit.fluxcd.io/v1 %s", gvk, k.kind)
			}
			if obj.GetName() != "src" || obj.GetNamespace() != "demo" {
				t.Errorf("emitted %s/%s, want demo/src", obj.GetNamespace(), obj.GetName())
			}
			obj = fluxSrcRender(t, fluxSrcConfig(t, k, fluxSrcProps(t, k.minimal)), "flux-system")
			if obj.GetNamespace() != "flux-system" {
				t.Errorf("namespace %q under a Flux namespace, want flux-system", obj.GetNamespace())
			}
		})
	}
}

// TestFluxSourceHandlers_IntervalDefault: an unset interval becomes 60m, an
// authored one is kept, and a helmrepository of type oci gets no default.
func TestFluxSourceHandlers_IntervalDefault(t *testing.T) {
	interval := func(t *testing.T, k fluxSourceKind, props map[string]any) any {
		t.Helper()
		return fluxSrcSpec(t, fluxSrcRender(t, fluxSrcConfig(t, k, props), ""))["interval"]
	}
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			if got := interval(t, k, fluxSrcProps(t, k.minimal)); got != "1h0m0s" {
				t.Errorf("default interval = %v, want 1h0m0s", got)
			}
			props := fluxSrcProps(t, k.minimal)
			props["interval"] = "10m"
			if got := interval(t, k, props); got != "10m0s" {
				t.Errorf("authored interval = %v, want 10m0s", got)
			}
		})
	}
	helm := fluxSourceKinds()[0]
	props := map[string]any{"url": "oci://ghcr.io/org/charts", "type": "oci"}
	if got := interval(t, helm, props); got != "0s" {
		t.Errorf("helmrepository type oci: interval = %v, want no default (0s)", got)
	}
}

func TestFluxSourceHandlers_Refuses(t *testing.T) {
	type refusal struct {
		name  string
		props string // YAML merged over the kind's minimal properties
		drop  string // a minimal key to delete first
		want  string
	}
	common := []refusal{
		{name: "unknown top-level key", props: "bogus: x", want: `unknown field "bogus"`},
		{name: "unknown nested key", props: "secretRef: {name: s, namespace: other}", want: `unknown field "namespace"`},
		{name: "wrongly typed bool", props: `suspend: "yes"`, want: "suspend"},
		{name: "wrongly typed object", props: "secretRef: creds", want: "secretRef"},
		{name: "invalid duration", props: "interval: 5minutes", want: "5minutes"},
	}
	perKind := map[string][]refusal{
		"helmrepository": {
			{name: "no url", drop: "url", want: "helmrepository: url is required"},
			{name: "ftp url", props: "url: ftp://charts.example.com", want: `must start with http:// or https:// or oci://`},
			{name: "upper-case scheme", props: "url: HTTPS://charts.example.com", want: "must start with"},
			{name: "type oci over https", props: "type: oci", want: `url "https://charts.example.com/stable" must start with oci://`},
		},
		"ocirepository": {
			{name: "no url", drop: "url", want: "ocirepository: url is required"},
			{name: "https url", props: "url: https://ghcr.io/org/manifests", want: "must start with oci://"},
		},
		"gitrepository": {
			{name: "no url", drop: "url", want: "gitrepository: url is required"},
			{name: "scp-like url", props: "url: git@github.com:org/repo", want: "must start with http:// or https:// or ssh://"},
			{name: "git scheme", props: "url: git://github.com/org/repo", want: "must start with"},
			{name: "unknown key in include item", props: "include: [{repository: {name: a}, path: x}]", want: `unknown field "path"`},
		},
		"bucket": {
			{name: "no bucketName", drop: "bucketName", want: "bucket: bucketName is required"},
			{name: "no endpoint", drop: "endpoint", want: "bucket: endpoint is required"},
			{name: "unknown key in sts", props: "sts: {provider: ldap, endpoint: https://sts.example.com, region: x}", want: `unknown field "region"`},
		},
	}
	for _, k := range fluxSourceKinds() {
		for _, tc := range append(slices.Clone(common), perKind[k.typ]...) {
			t.Run(k.typ+"/"+tc.name, func(t *testing.T) {
				props := fluxSrcProps(t, k.minimal)
				delete(props, tc.drop)
				for key, v := range fluxSrcProps(t, tc.props) {
					props[key] = v
				}
				_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "src", Type: k.typ, Properties: props}, "demo")
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want one containing %q", err, tc.want)
				}
			})
		}
	}
}

// TestFluxSourceHandlers_ApplyPolicy: the fetch host — url for the three
// repositories (the user of an ssh:// URL dropped), endpoint for bucket — must
// be in the policy's allowed registries; no policy or an empty list permits it.
// An oci:// url's explicit-registry rule and a gcp Bucket's fixed host have
// their own tests below.
func TestFluxSourceHandlers_ApplyPolicy(t *testing.T) {
	cases := []struct {
		typ, value string
		allowed    []string
		ok         bool
	}{
		{"helmrepository", "https://charts.example.com/stable", []string{"charts.example.com"}, true},
		{"helmrepository", "https://charts.example.com/stable", []string{"other.example.com"}, false},
		{"helmrepository", "oci://ghcr.io/org/charts", []string{"ghcr.io"}, true},
		{"helmrepository", "https://user@charts.example.com/stable", []string{"charts.example.com"}, false},
		{"ocirepository", "oci://ghcr.io/org/manifests", []string{"ghcr.io"}, true},
		{"ocirepository", "oci://registry.local:5000/org/manifests", []string{"registry.local"}, false},
		{"ocirepository", "oci://evil.example.com/org/manifests", []string{"ghcr.io"}, false},
		{"gitrepository", "https://github.com/org/repo", []string{"github.com"}, true},
		{"gitrepository", "ssh://git@github.com/org/repo", []string{"github.com"}, true},
		{"gitrepository", "ssh://git@gitea.example.com:2222/org/repo", []string{"gitea.example.com:2222"}, true},
		{"gitrepository", "ssh://git@evil.example.com/org/repo", []string{"github.com"}, false},
		{"gitrepository", "ssh://evil.example.com/x@github.com", []string{"github.com"}, false},
		{"gitrepository", "ssh://evil.example.com?@github.com/org/repo", []string{"github.com"}, false},
		{"bucket", "minio.example.com:9000", []string{"minio.example.com:9000"}, true},
		{"bucket", "minio.example.com:9000", []string{"minio.example.com"}, false},
		{"bucket", "s3.amazonaws.com", []string{"s3.amazonaws.com"}, false}, // Amazon S3: see TestBucket_ApplyPolicyProvider
		{"bucket", "https://account.blob.core.windows.net", []string{"account.blob.core.windows.net"}, true},
		{"bucket", "evil.example.com", []string{"s3.amazonaws.com"}, false},
	}
	kinds := map[string]fluxSourceKind{}
	for _, k := range fluxSourceKinds() {
		kinds[k.typ] = k
	}
	for _, tc := range cases {
		k := kinds[tc.typ]
		props := fluxSrcProps(t, k.minimal)
		props[k.host] = tc.value
		cfg := fluxSrcConfig(t, k, props).(oam.Enforceable)
		err := cfg.ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
		if (err == nil) != tc.ok {
			t.Errorf("%s %s %q, allowed %v: error = %v, want allowed=%v", tc.typ, k.host, tc.value, tc.allowed, err, tc.ok)
		}
		if err != nil && !strings.Contains(err.Error(), tc.typ+": "+k.host+": ") {
			t.Errorf("%s: error %q does not name the component type and field", tc.typ, err)
		}
		if err := cfg.ApplyPolicy(nil); err != nil {
			t.Errorf("%s %q: nil policy: %v", tc.typ, tc.value, err)
		}
		if err := cfg.ApplyPolicy(fakeOCIPolicy{}); err != nil {
			t.Errorf("%s %q: empty allowlist: %v", tc.typ, tc.value, err)
		}
	}
}

// TestFluxSourceHandlers_ApplyPolicyOCIRegistry: under a non-empty allowlist an
// oci:// url must name its registry explicitly — a first segment that is
// localhost or contains "." or ":" — because go-containerregistry, which Flux
// parses the url with, otherwise reads the whole reference as a Docker Hub
// repository: oci://ghcr.io and oci://registry/my-artifact are pulled from
// Docker Hub, whatever host the allowlist matched. An ocirepository url also
// needs a repository path after the registry; a helmrepository url may stop at
// the registry, since Flux appends the chart name, and the rule follows its url
// scheme whatever its type. An explicit registry is matched exactly, as before;
// no policy or an empty allowlist permits every url.
func TestFluxSourceHandlers_ApplyPolicyOCIRegistry(t *testing.T) {
	const (
		implicit   = "does not name its registry explicitly"
		notAllowed = "is not in allowed registries"
	)
	cases := []struct {
		name, typ, url string
		allowed        []string
		want           string // empty: allowed; else a substring of the error
	}{
		// Host only: Flux reads oci://ghcr.io as index.docker.io/library/ghcr.io.
		{"host only", "ocirepository", "oci://ghcr.io", []string{"ghcr.io"}, implicit},
		{"host only, trailing slash", "ocirepository", "oci://ghcr.io/", []string{"ghcr.io"}, implicit},
		{"host only", "helmrepository", "oci://ghcr.io", []string{"ghcr.io"}, ""},
		{"host only, not allowed", "helmrepository", "oci://ghcr.io", []string{"quay.io"}, notAllowed},

		// A single-label first segment is a Docker Hub namespace.
		{"single label", "ocirepository", "oci://registry/my-artifact", []string{"registry"}, implicit},
		{"single label", "helmrepository", "oci://registry/charts", []string{"registry"}, implicit},
		{"single label host only", "helmrepository", "oci://registry", []string{"registry"}, implicit},

		// Explicit registries, matched exactly; the port is part of the host.
		{"localhost", "ocirepository", "oci://localhost/x", []string{"localhost"}, ""},
		{"localhost with port", "ocirepository", "oci://localhost:5000/x", []string{"localhost:5000"}, ""},
		{"localhost with port, entry without", "ocirepository", "oci://localhost:5000/x", []string{"localhost"}, notAllowed},
		{"registry with port", "ocirepository", "oci://registry.example:5000/org/x", []string{"registry.example:5000"}, ""},
		{"docker.io", "ocirepository", "oci://docker.io/library/x", []string{"docker.io"}, ""},
		{"multi-segment path", "ocirepository", "oci://ghcr.io/org/team/x", []string{"ghcr.io"}, ""},
		{"not allowed", "ocirepository", "oci://evil.example.com/org/x", []string{"ghcr.io"}, notAllowed},
		{"localhost with port", "helmrepository", "oci://localhost:5000", []string{"localhost:5000"}, ""},
		{"docker.io", "helmrepository", "oci://docker.io/org", []string{"docker.io"}, ""},

		// Userinfo still fails closed, whichever way the first segment reads.
		{"userinfo, explicit", "ocirepository", "oci://user@ghcr.io/org/x", []string{"ghcr.io"}, notAllowed},
		{"userinfo, single label", "ocirepository", "oci://user@registry/x", []string{"registry"}, implicit},
		{"userinfo with password", "helmrepository", "oci://user:pass@ghcr.io/charts", []string{"ghcr.io"}, notAllowed},
	}
	kinds := map[string]fluxSourceKind{}
	for _, k := range fluxSourceKinds() {
		kinds[k.typ] = k
	}
	for _, tc := range cases {
		k := kinds[tc.typ]
		// A helmrepository is checked by url scheme, so with and without type oci.
		helmTypes := []string{""}
		if tc.typ == "helmrepository" {
			helmTypes = []string{sourcev1.HelmRepositoryTypeOCI, sourcev1.HelmRepositoryTypeDefault}
		}
		for _, helmType := range helmTypes {
			name := tc.typ + "/" + tc.name
			if helmType != "" {
				name += "/type " + helmType
			}
			t.Run(name, func(t *testing.T) {
				props := fluxSrcProps(t, k.minimal)
				props["url"] = tc.url
				if helmType != "" {
					props["type"] = helmType
				}
				cfg := fluxSrcConfig(t, k, props).(oam.Enforceable)
				err := cfg.ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
				switch {
				case tc.want == "" && err != nil:
					t.Errorf("url %q, allowed %v: %v, want allowed", tc.url, tc.allowed, err)
				case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
					t.Errorf("url %q, allowed %v: error = %v, want one containing %q", tc.url, tc.allowed, err, tc.want)
				case err != nil && !strings.Contains(err.Error(), tc.typ+": url: "):
					t.Errorf("error %q does not name the component type and field", err)
				}
				if err := cfg.ApplyPolicy(nil); err != nil {
					t.Errorf("url %q: nil policy: %v", tc.url, err)
				}
				if err := cfg.ApplyPolicy(fakeOCIPolicy{}); err != nil {
					t.Errorf("url %q: empty allowlist: %v", tc.url, err)
				}
			})
		}
	}
}

// TestBucket_ApplyPolicyProvider: Flux's gcp provider never reads endpoint — it
// fetches from Google Cloud Storage's own host, storage.googleapis.com — so that
// is the host a gcp Bucket is checked against. Every other provider (generic,
// aws, azure, or none) fetches from endpoint, which stays the host checked —
// except that the S3 client behind generic, aws and none replaces an Amazon S3
// endpoint's host with the S3 host of spec.region (or of the bucket's
// discovered location), so under a non-empty allowlist such an endpoint is
// refused, whatever its case, scheme or port, and so is a look-alike the
// client's unescaped host patterns also match. No policy or an empty allowlist
// permits every case.
func TestBucket_ApplyPolicyProvider(t *testing.T) {
	const (
		field     = `bucket: endpoint: `
		amazonWhy = `is treated as an Amazon S3 host (it contains "amazonaws"): Flux's S3 client fetches such a bucket not from endpoint`
	)
	cases := []struct {
		name, provider, endpoint, region string
		allowed                          []string
		want                             string // empty: allowed; else a substring of the error
	}{
		{"gcp, endpoint allowed but ignored", "gcp", "minio.example.com", "", []string{"minio.example.com"},
			`bucket: provider gcp (Flux ignores endpoint): source registry "storage.googleapis.com" is not in allowed registries`},
		{"gcp, storage host allowed", "gcp", "minio.example.com", "", []string{"storage.googleapis.com"}, ""},
		{"gcp, storage host as endpoint", "gcp", "storage.googleapis.com", "", []string{"storage.googleapis.com"}, ""},
		{"gcp, Amazon endpoint ignored", "gcp", "s3.amazonaws.com", "", []string{"storage.googleapis.com"}, ""},
		{"generic", "generic", "minio.example.com", "", []string{"minio.example.com"}, ""},
		{"generic, region on a non-Amazon endpoint", "generic", "minio.example.com", "cn-north-1", []string{"minio.example.com"}, ""},
		{"generic, storage host allowed", "generic", "minio.example.com", "", []string{"storage.googleapis.com"}, "bucket: endpoint: "},
		{"azure", "azure", "https://account.blob.core.windows.net", "", []string{"account.blob.core.windows.net"}, ""},
		{"azure, storage host allowed", "azure", "https://account.blob.core.windows.net", "", []string{"storage.googleapis.com"}, "bucket: endpoint: "},
		{"azure, Amazon endpoint checked as a host", "azure", "s3.amazonaws.com", "", []string{"s3.amazonaws.com"}, ""},
		{"no provider, storage host allowed", "", "minio.example.com", "", []string{"storage.googleapis.com"}, "bucket: endpoint: "},

		// Amazon S3 endpoints on the S3 client's path, each allowlisted exactly.
		{"generic, region in another partition", "generic", "s3.us-gov-west-1.amazonaws.com", "cn-north-1",
			[]string{"s3.us-gov-west-1.amazonaws.com"}, amazonWhy},
		{"aws, global endpoint", "aws", "s3.amazonaws.com", "", []string{"s3.amazonaws.com"}, amazonWhy},
		{"no provider, global endpoint", "", "s3.amazonaws.com", "", []string{"s3.amazonaws.com"}, amazonWhy},
		{"generic, China partition", "generic", "s3.cn-north-1.amazonaws.com.cn", "", []string{"s3.cn-north-1.amazonaws.com.cn"}, amazonWhy},
		{"aws, upper case", "aws", "S3.EU-WEST-1.AMAZONAWS.COM", "", []string{"S3.EU-WEST-1.AMAZONAWS.COM"}, amazonWhy},
		{"generic, scheme and port", "generic", "https://s3.eu-west-1.amazonaws.com:443", "", []string{"s3.eu-west-1.amazonaws.com:443"}, amazonWhy},
		{"generic, look-alike the client also matches", "generic", "s3.x-amazonaws.com", "", []string{"s3.x-amazonaws.com"}, amazonWhy},
	}
	k := fluxSourceKinds()[3]
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := fluxSrcProps(t, k.minimal)
			props["endpoint"] = tc.endpoint
			if tc.provider != "" {
				props["provider"] = tc.provider
			}
			if tc.region != "" {
				props["region"] = tc.region
			}
			cfg := fluxSrcConfig(t, k, props).(oam.Enforceable)
			err := cfg.ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("allowed %v: %v, want allowed", tc.allowed, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("allowed %v: error = %v, want one containing %q", tc.allowed, err, tc.want)
			case tc.want == amazonWhy && !strings.Contains(err.Error(), field):
				t.Errorf("error %q does not name the component type and field", err)
			}
			if err := cfg.ApplyPolicy(nil); err != nil {
				t.Errorf("nil policy: %v", err)
			}
			if err := cfg.ApplyPolicy(fakeOCIPolicy{}); err != nil {
				t.Errorf("empty allowlist: %v", err)
			}
		})
	}
}

// TestFluxSourceHandlers_EmitsAutoHealthCheck: every source emits its check
// unless suspended, and a helmrepository of type oci never does.
func TestFluxSourceHandlers_EmitsAutoHealthCheck(t *testing.T) {
	emits := func(t *testing.T, k fluxSourceKind, props map[string]any) bool {
		t.Helper()
		e, ok := fluxSrcConfig(t, k, props).(interface{ EmitsAutoHealthCheck() bool })
		if !ok {
			t.Fatalf("%s config does not satisfy the autoHealthCheckEmitter shape the transform asserts on", k.typ)
		}
		return e.EmitsAutoHealthCheck()
	}
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			props := fluxSrcProps(t, k.minimal)
			if !emits(t, k, props) {
				t.Error("unsuspended source vetoes its health check")
			}
			props["suspend"] = false
			if !emits(t, k, props) {
				t.Error("suspend: false vetoes the health check")
			}
			props["suspend"] = true
			if emits(t, k, props) {
				t.Error("suspend: true keeps the health check")
			}
		})
	}
	helm := fluxSourceKinds()[0]
	if !emits(t, helm, map[string]any{"url": "https://charts.example.com", "type": "default"}) {
		t.Error("helmrepository type default vetoes its health check")
	}
	if emits(t, helm, map[string]any{"url": "oci://ghcr.io/org/charts", "type": "oci"}) {
		t.Error("helmrepository type oci keeps its health check")
	}
}

// TestFluxSourceHandlers_GenerateDoesNotAlias: rendering twice gives equal,
// independent output, and editing one render leaves the config untouched.
func TestFluxSourceHandlers_GenerateDoesNotAlias(t *testing.T) {
	for _, k := range fluxSourceKinds() {
		t.Run(k.typ, func(t *testing.T) {
			props := fluxSrcProps(t, k.full)
			delete(props, "interval")
			cfg := fluxSrcConfig(t, k, props)
			// secretRef is a pointer field every kind has.
			secretRefName := func(spec reflect.Value) reflect.Value {
				return spec.FieldByName("SecretRef").Elem().FieldByName("Name")
			}
			cfgSpec := reflect.ValueOf(cfg).Elem().FieldByName("Spec")
			authored := secretRefName(cfgSpec).String()

			first := fluxSrcRender(t, cfg, "")
			want := fluxSrcSpec(t, first)
			secretRefName(reflect.ValueOf(first).Elem().FieldByName("Spec")).SetString("mutated")

			if got := fluxSrcSpec(t, fluxSrcRender(t, cfg, "")); !reflect.DeepEqual(got, want) {
				t.Errorf("second render saw the first render's edit: %v", got)
			}
			if got := secretRefName(cfgSpec).String(); got != authored {
				t.Errorf("config's secretRef.name edited through a render: %q, want %q", got, authored)
			}
			if d := cfgSpec.FieldByName("Interval").Interface().(metav1.Duration); d.Duration != 0 {
				t.Errorf("Generate wrote the interval default into the config: %v", d.Duration)
			}
		})
	}
}

// TestFluxSourceConfigs_GenerateValidatesDirectConfig: a config built directly,
// never parsed, is checked at the emission boundary as the parse path checks it.
func TestFluxSourceConfigs_GenerateValidatesDirectConfig(t *testing.T) {
	cases := map[string]stack.ApplicationConfig{
		"helmrepository without url": &components.HelmRepositoryConfig{Name: "src"},
		"helmrepository type oci over https": &components.HelmRepositoryConfig{Name: "src", Spec: sourcev1.HelmRepositorySpec{
			URL: "https://charts.example.com", Type: sourcev1.HelmRepositoryTypeOCI,
		}},
		"ocirepository over https":  &components.OCIRepositoryConfig{Name: "src", Spec: sourcev1.OCIRepositorySpec{URL: "https://ghcr.io/x"}},
		"gitrepository without url": &components.GitRepositoryConfig{Name: "src"},
		"bucket without endpoint":   &components.BucketConfig{Name: "src", Spec: sourcev1.BucketSpec{BucketName: "b"}},
		"bucket without bucketName": &components.BucketConfig{Name: "src", Spec: sourcev1.BucketSpec{Endpoint: "s3.amazonaws.com"}},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := cases[n].Generate(nil); err == nil {
			t.Errorf("%s: Generate accepted an invalid config", n)
		}
	}
}

// fluxSrcPolicy is a complete oam.Policy whose only restriction is its allowed
// registries.
type fluxSrcPolicy struct {
	oam.Policy
	allowed []string
}

func (p fluxSrcPolicy) AllowedRegistries() []string { return p.allowed }

// TestTransform_FluxSources runs the transform pipeline over all four source
// components with a Flux namespace: each CR lands there, the auto health check
// references it there, and the suspended one carries none. The same document
// under a policy whose allowed registries miss one fetch host fails the build,
// naming that component.
func TestTransform_FluxSources(t *testing.T) {
	handlers := map[string]oam.ComponentHandler{}
	for _, k := range fluxSourceKinds() {
		handlers[k.typ] = k.handler
	}
	// app builds a fresh document each time, the gitrepository suspended.
	app := func() *oam.Application {
		var comps []oam.Component
		for _, k := range fluxSourceKinds() {
			props := fluxSrcProps(t, k.minimal)
			if k.typ == "gitrepository" {
				props["suspend"] = true
			}
			comps = append(comps, oam.Component{Name: k.typ, Type: k.typ, Properties: props})
		}
		return &oam.Application{
			Metadata: oam.Metadata{Name: "shop", Namespace: "shop"},
			Spec:     oam.ApplicationSpec{Components: comps},
		}
	}

	cluster, err := oam.NewTransformer(handlers, nil).Transform(app(), oam.TransformContext{FluxNamespace: "flux-system"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	found := map[string]bool{}
	var checks []stack.HealthCheck
	var walk func(*stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			checks = append(checks, n.Bundle.HealthChecks...)
			for _, a := range n.Bundle.Applications {
				obj := fluxSrcRender(t, a.Config, "")
				found[a.Name] = true
				if obj.GetNamespace() != "flux-system" || obj.GetName() != a.Name {
					t.Errorf("%s emitted as %s/%s, want flux-system/%s", a.Name, obj.GetNamespace(), obj.GetName(), a.Name)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)
	for _, k := range fluxSourceKinds() {
		if !found[k.typ] {
			t.Errorf("%s application not found", k.typ)
		}
		want := stack.HealthCheck{APIVersion: "source.toolkit.fluxcd.io/v1", Kind: k.kind, Name: k.typ, Namespace: "flux-system"}
		if got := slices.Contains(checks, want); got != (k.typ != "gitrepository") {
			t.Errorf("%s: health check present = %v, suspended only for gitrepository; checks %+v", k.typ, got, checks)
		}
	}

	// The transform reads the whole Policy, so the rest comes from NoopPolicy.
	policy := fluxSrcPolicy{Policy: &oam.NoopPolicy{}, allowed: []string{"charts.example.com", "ghcr.io", "github.com"}} // not the bucket endpoint
	_, err = oam.NewTransformer(handlers, nil).Transform(app(), oam.TransformContext{Policy: policy})
	if err == nil || !strings.Contains(err.Error(), `component "bucket": bucket: endpoint: `) || !strings.Contains(err.Error(), "minio.example.com:9000") {
		t.Fatalf("Transform under a policy missing the bucket endpoint: error = %v", err)
	}
	policy.allowed = append(policy.allowed, "minio.example.com:9000")
	if _, err := oam.NewTransformer(handlers, nil).Transform(app(), oam.TransformContext{Policy: policy}); err != nil {
		t.Fatalf("Transform under a policy allowing every fetch host: %v", err)
	}
}
