package components_test

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// helmLowering is one run's lowering context for a component of document doc:
// the namer is shared by every lowerHelm call that passes the same context, as
// the engine shares it across one document's components.
func helmLowering(doc string) oam.LoweringContext {
	return oam.LoweringContext{
		Namer:  oam.NewNameAllocator(),
		Origin: oam.Origin{Document: doc, DocumentKind: "Application", Namespace: "default"},
	}
}

func lowerHelm(t *testing.T, lctx oam.LoweringContext, name string, props map[string]any) []oam.Component {
	t.Helper()
	lctx.Origin.Component = name
	lctx.Origin.ComponentType = "helm"
	res, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: name, Type: "helm", Properties: props}, lctx)
	if err != nil {
		t.Fatalf("LowerComponent(%s): %v", name, err)
	}
	if len(res.Documents) != 0 || len(res.Traits) != 0 || len(res.Policies) != 0 {
		t.Fatalf("LowerComponent(%s) emitted more than components: %+v", name, res)
	}
	return res.Components
}

// helmSourceName is the name the rule gives the source generated for identity
// in document doc.
func helmSourceName(doc, identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return doc + "-source-" + hex.EncodeToString(sum[:])[:10]
}

func componentByType(t *testing.T, comps []oam.Component, typ string) oam.Component {
	t.Helper()
	for _, c := range comps {
		if c.Type == typ {
			return c
		}
	}
	t.Fatalf("no %s component in %+v", typ, comps)
	return oam.Component{}
}

func TestHelmRule_ComponentType(t *testing.T) {
	if got := (components.HelmRule{}).ComponentType(); got != "helm" {
		t.Errorf("ComponentType() = %q, want helm", got)
	}
}

// TestHelmRule_FluxHelmRepository: an inline http(s) URL emits a HelmRepository
// carrying only the URL, named after the document and the URL's identity, and a
// helmrelease under the authored name whose chart template references it. Every
// passthrough key, the authored traits and the annotations reach the release
// verbatim; nothing but the URL reaches the source. valuesMode itself is never
// forwarded.
func TestHelmRule_FluxHelmRepository(t *testing.T) {
	traits := []oam.Trait{{Type: "force-replace", Properties: map[string]any{}}}
	annotations := map[string]string{"example.com/tier": "services"}
	passthrough := map[string]any{
		"values":          map[string]any{"replicaCount": 2},
		"interval":        "10m",
		"releaseName":     "podinfo-release",
		"targetNamespace": "apps",
		"driftDetection":  map[string]any{"mode": "warn"},
		"install":         map[string]any{"crds": "Create"},
		"upgrade":         map[string]any{"crds": "CreateReplace"},
		"valuesFrom":      []any{map[string]any{"kind": "Secret", "name": "extra"}},
	}
	props := map[string]any{
		"chart":      "podinfo",
		"version":    "6.5.0",
		"valuesMode": "inline",
		"source":     map[string]any{"url": "https://charts.example.com"},
	}
	for k, v := range passthrough {
		props[k] = v
	}
	lctx := helmLowering("shop")
	lctx.Origin.Component = "podinfo"
	res, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: "podinfo", Type: "helm", Properties: props, Traits: traits, Annotations: annotations}, lctx)
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	if len(res.Components) != 2 {
		t.Fatalf("emitted %d components, want a source and a release: %+v", len(res.Components), res.Components)
	}

	source := componentByType(t, res.Components, "helmrepository")
	wantName := helmSourceName("shop", "helm:https://charts.example.com")
	if source.Name != wantName {
		t.Errorf("source name = %q, want %q", source.Name, wantName)
	}
	if want := map[string]any{"url": "https://charts.example.com"}; !reflect.DeepEqual(source.Properties, want) {
		t.Errorf("source properties = %v, want %v", source.Properties, want)
	}
	if len(source.Traits) != 0 || len(source.Annotations) != 0 {
		t.Errorf("source carries traits %v / annotations %v, want none", source.Traits, source.Annotations)
	}

	release := componentByType(t, res.Components, "helmrelease")
	if release.Name != "podinfo" {
		t.Errorf("release name = %q, want the authored podinfo", release.Name)
	}
	want := map[string]any{
		"chart": map[string]any{"spec": map[string]any{
			"chart":     "podinfo",
			"version":   "6.5.0",
			"sourceRef": map[string]any{"kind": "HelmRepository", "name": wantName},
		}},
	}
	for k, v := range passthrough {
		want[k] = v
	}
	if !reflect.DeepEqual(release.Properties, want) {
		t.Errorf("release properties = %v\nwant %v", release.Properties, want)
	}
	if !reflect.DeepEqual(release.Traits, traits) {
		t.Errorf("release traits = %v, want the authored %v", release.Traits, traits)
	}
	if !reflect.DeepEqual(release.Annotations, annotations) {
		t.Errorf("release annotations = %v, want the authored %v", release.Annotations, annotations)
	}
}

// TestHelmRule_FluxOCIRepository: an inline oci:// URL emits an OCIRepository
// pinned to the version as its tag, referenced by the release's chartRef. No
// valuesMode is forwarded when none was authored.
func TestHelmRule_FluxOCIRepository(t *testing.T) {
	comps := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{
		"version": "6.5.0",
		"source":  map[string]any{"url": "oci://ghcr.io/example/charts/podinfo"},
	})
	source := componentByType(t, comps, "ocirepository")
	wantName := helmSourceName("shop", "oci:oci://ghcr.io/example/charts/podinfo:6.5.0")
	if source.Name != wantName {
		t.Errorf("source name = %q, want %q", source.Name, wantName)
	}
	wantSource := map[string]any{
		"url": "oci://ghcr.io/example/charts/podinfo",
		"ref": map[string]any{"tag": "6.5.0"},
		// The chart layer is copied, not extracted and re-archived without the
		// files Flux's ignore rules drop.
		"layerSelector": map[string]any{"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip", "operation": "copy"},
	}
	if !reflect.DeepEqual(source.Properties, wantSource) {
		t.Errorf("source properties = %v, want %v", source.Properties, wantSource)
	}
	release := componentByType(t, comps, "helmrelease")
	wantRelease := map[string]any{"chartRef": map[string]any{"kind": "OCIRepository", "name": wantName}}
	if !reflect.DeepEqual(release.Properties, wantRelease) {
		t.Errorf("release properties = %v, want %v", release.Properties, wantRelease)
	}
}

// TestHelmRule_FluxGitRepository: an inline GitRepository source generates a
// gitrepository with the URL and the one authored ref field, named after the
// JSON identity, and the release reads it through chart.spec.sourceRef with
// chart as the path. An http:// URL is accepted as well.
func TestHelmRule_FluxGitRepository(t *testing.T) {
	for _, url := range []string{"https://github.com/example/charts", "http://git.example.com/charts"} {
		t.Run(url, func(t *testing.T) {
			comps := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{
				"chart":  "./charts/podinfo",
				"source": map[string]any{"url": url, "kind": "GitRepository", "ref": map[string]any{"tag": "v6.5.0"}},
			})
			if len(comps) != 2 {
				t.Fatalf("emitted %d components, want source and release", len(comps))
			}
			source := componentByType(t, comps, "gitrepository")
			wantName := helmSourceName("shop", `git:{"url":"`+url+`","ref":{"tag":"v6.5.0"}}`)
			if source.Name != wantName {
				t.Errorf("source name = %q, want %q", source.Name, wantName)
			}
			if want := map[string]any{"url": url, "ref": map[string]any{"tag": "v6.5.0"}}; !reflect.DeepEqual(source.Properties, want) {
				t.Errorf("source properties = %v, want %v", source.Properties, want)
			}
			release := componentByType(t, comps, "helmrelease")
			wantRelease := map[string]any{"chart": map[string]any{"spec": map[string]any{
				"chart": "./charts/podinfo", "sourceRef": map[string]any{"kind": "GitRepository", "name": wantName},
				"reconcileStrategy": "Revision",
			}}}
			if !reflect.DeepEqual(release.Properties, wantRelease) {
				t.Errorf("release properties = %v, want %v", release.Properties, wantRelease)
			}
		})
	}
}

// TestHelmRule_FluxBucket: an inline Bucket source generates a bucket with the
// authored location keys only, named after the JSON identity of every key that
// locates it, and the release reads it through chart.spec.sourceRef.
func TestHelmRule_FluxBucket(t *testing.T) {
	comps := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{
		"chart":  "charts/podinfo",
		"source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts", "provider": "generic", "prefix": "podinfo/"},
	})
	if len(comps) != 2 {
		t.Fatalf("emitted %d components, want source and release", len(comps))
	}
	source := componentByType(t, comps, "bucket")
	wantName := helmSourceName("shop", `bucket:{"provider":"generic","endpoint":"minio.example.com","bucketName":"charts","region":"","prefix":"podinfo/"}`)
	if source.Name != wantName {
		t.Errorf("source name = %q, want %q", source.Name, wantName)
	}
	wantSource := map[string]any{"endpoint": "minio.example.com", "bucketName": "charts", "provider": "generic", "prefix": "podinfo/"}
	if !reflect.DeepEqual(source.Properties, wantSource) {
		t.Errorf("source properties = %v, want %v", source.Properties, wantSource)
	}
	release := componentByType(t, comps, "helmrelease")
	wantRelease := map[string]any{"chart": map[string]any{"spec": map[string]any{
		"chart": "charts/podinfo", "sourceRef": map[string]any{"kind": "Bucket", "name": wantName},
		"reconcileStrategy": "Revision",
	}}}
	if !reflect.DeepEqual(release.Properties, wantRelease) {
		t.Errorf("release properties = %v, want %v", release.Properties, wantRelease)
	}
}

// TestHelmRule_GitAndBucketSharePerIdentity: two components with the same Git
// URL and ref, or the same Bucket location, share one generated source; a
// different ref, or a different bucket, gets its own.
func TestHelmRule_GitAndBucketSharePerIdentity(t *testing.T) {
	lctx := helmLowering("shop")
	git := func(ref map[string]any) map[string]any {
		return map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": ref}
	}
	first := lowerHelm(t, lctx, "a", map[string]any{"chart": "./a", "source": git(map[string]any{"branch": "main"})})
	second := lowerHelm(t, lctx, "b", map[string]any{"chart": "./b", "source": git(map[string]any{"branch": "main"})})
	if len(second) != 1 || second[0].Type != "helmrelease" {
		t.Fatalf("second Git claimant emitted %+v, want only its release (the source is adopted)", second)
	}
	other := lowerHelm(t, lctx, "c", map[string]any{"chart": "./c", "source": git(map[string]any{"tag": "main"})})
	if a, c := componentByType(t, first, "gitrepository").Name, componentByType(t, other, "gitrepository").Name; a == c {
		t.Errorf("branch main and tag main share source %s", a)
	}

	bucket := func(name string) map[string]any {
		return map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": name}
	}
	b1 := lowerHelm(t, lctx, "d", map[string]any{"chart": "d", "source": bucket("charts")})
	b2 := lowerHelm(t, lctx, "e", map[string]any{"chart": "e", "source": bucket("charts")})
	if len(b2) != 1 || b2[0].Type != "helmrelease" {
		t.Fatalf("second Bucket claimant emitted %+v, want only its release", b2)
	}
	b3 := lowerHelm(t, lctx, "f", map[string]any{"chart": "f", "source": bucket("other")})
	if a, c := componentByType(t, b1, "bucket").Name, componentByType(t, b3, "bucket").Name; a == c {
		t.Errorf("two buckets share source %s", a)
	}
}

// TestHelmRule_OCIWithoutVersion: an inline OCI source without a version is
// accepted under delivery: flux, as the removed helmchart accepted it: the source carries no
// ref, so Flux pulls the latest tag.
func TestHelmRule_OCIWithoutVersion(t *testing.T) {
	comps := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{
		"source": map[string]any{"url": "oci://ghcr.io/example/charts/podinfo"},
	})
	source := componentByType(t, comps, "ocirepository")
	if _, ok := source.Properties["ref"]; ok {
		t.Errorf("source properties = %v, want no ref without a version", source.Properties)
	}
}

// TestHelmRule_SharesOneSourcePerIdentity is the acceptance case: two helm
// components of one document with the same URL produce exactly one source, and
// both releases reference it; different URLs, or one OCI URL at different
// versions, produce differently named sources and no error.
func TestHelmRule_SharesOneSourcePerIdentity(t *testing.T) {
	lctx := helmLowering("shop")
	repo := map[string]any{"url": "https://charts.example.com"}
	first := lowerHelm(t, lctx, "a", map[string]any{"chart": "a", "source": repo})
	second := lowerHelm(t, lctx, "b", map[string]any{"chart": "b", "source": repo})
	if len(first) != 2 {
		t.Fatalf("first claimant emitted %d components, want source and release", len(first))
	}
	if len(second) != 1 || second[0].Type != "helmrelease" {
		t.Fatalf("second claimant emitted %+v, want only its release (the source is adopted)", second)
	}
	shared := componentByType(t, first, "helmrepository").Name
	for _, rel := range []oam.Component{componentByType(t, first, "helmrelease"), second[0]} {
		spec := rel.Properties["chart"].(map[string]any)["spec"].(map[string]any)
		if got := spec["sourceRef"].(map[string]any)["name"]; got != shared {
			t.Errorf("%s references source %v, want the shared %s", rel.Name, got, shared)
		}
	}

	other := lowerHelm(t, lctx, "c", map[string]any{"chart": "c", "source": map[string]any{"url": "https://other.example.com"}})
	if name := componentByType(t, other, "helmrepository").Name; name == shared {
		t.Errorf("a different URL reused source %s", name)
	}

	v1 := lowerHelm(t, lctx, "d", map[string]any{"version": "1.0.0", "source": map[string]any{"url": "oci://ghcr.io/example/charts/d"}})
	v2 := lowerHelm(t, lctx, "e", map[string]any{"version": "2.0.0", "source": map[string]any{"url": "oci://ghcr.io/example/charts/d"}})
	if a, b := componentByType(t, v1, "ocirepository").Name, componentByType(t, v2, "ocirepository").Name; a == b {
		t.Errorf("two OCI versions share source %s", a)
	}
}

// TestHelmRule_SourceNameIsPerDocument: the document name is part of a
// generated source's name, so two documents sharing a URL get different names
// and never collide, even through one allocator.
func TestHelmRule_SourceNameIsPerDocument(t *testing.T) {
	lctx := helmLowering("shop")
	props := map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com"}}
	shop := componentByType(t, lowerHelm(t, lctx, "a", props), "helmrepository").Name
	lctx.Origin.Document = "blog"
	blog := componentByType(t, lowerHelm(t, lctx, "a", props), "helmrepository").Name
	if shop == blog {
		t.Errorf("two documents share the source name %s", shop)
	}
}

// TestHelmRule_ReferenceForm: source.name references an existing source of each
// kind, with its namespace, and nothing but the release is emitted. A
// GitRepository or Bucket source is a chart.spec.sourceRef with chart as the
// chart's path in the artifact, as a HelmRepository is.
func TestHelmRule_ReferenceForm(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		key   string
		want  any
	}{
		{"HelmRepository", map[string]any{"chart": "podinfo", "version": "6.5.0", "source": map[string]any{"name": "charts", "kind": "HelmRepository", "namespace": "flux-system"}},
			"chart", map[string]any{"spec": map[string]any{"chart": "podinfo", "version": "6.5.0", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts", "namespace": "flux-system"}}}},
		{"GitRepository", map[string]any{"chart": "./charts/podinfo", "source": map[string]any{"name": "podinfo", "kind": "GitRepository", "namespace": "flux-system"}},
			"chart", map[string]any{"spec": map[string]any{"chart": "./charts/podinfo", "sourceRef": map[string]any{"kind": "GitRepository", "name": "podinfo", "namespace": "flux-system"}, "reconcileStrategy": "Revision"}}},
		{"Bucket", map[string]any{"chart": "charts/podinfo", "source": map[string]any{"name": "artifacts", "kind": "Bucket"}},
			"chart", map[string]any{"spec": map[string]any{"chart": "charts/podinfo", "sourceRef": map[string]any{"kind": "Bucket", "name": "artifacts"}, "reconcileStrategy": "Revision"}}},
		{"OCIRepository", map[string]any{"source": map[string]any{"name": "podinfo", "kind": "OCIRepository"}},
			"chartRef", map[string]any{"kind": "OCIRepository", "name": "podinfo"}},
		{"HelmChart", map[string]any{"source": map[string]any{"name": "podinfo", "kind": "HelmChart", "namespace": "flux-system"}},
			"chartRef", map[string]any{"kind": "HelmChart", "name": "podinfo", "namespace": "flux-system"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comps := lowerHelm(t, helmLowering("shop"), "podinfo", tc.props)
			if len(comps) != 1 || comps[0].Type != "helmrelease" {
				t.Fatalf("emitted %+v, want only the release", comps)
			}
			if want := map[string]any{tc.key: tc.want}; !reflect.DeepEqual(comps[0].Properties, want) {
				t.Errorf("release properties = %v, want %v", comps[0].Properties, want)
			}
		})
	}
}

// TestHelmRule_Template: delivery: template emits one helmtemplate under the
// authored name with the URL inline, the resolved kind, and the values; no
// source. An authored valuesMode: inline is accepted and not forwarded, since
// helmtemplate declares no valuesMode.
func TestHelmRule_Template(t *testing.T) {
	traits := []oam.Trait{{Type: "force-replace", Properties: map[string]any{}}}
	lctx := helmLowering("shop")
	res, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: "podinfo", Type: "helm", Traits: traits, Properties: map[string]any{
		"delivery":   "template",
		"chart":      "podinfo",
		"version":    "6.5.0",
		"valuesMode": "inline",
		"values":     map[string]any{"replicaCount": 2},
		"source":     map[string]any{"url": "https://charts.example.com"},
	}}, lctx)
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	if len(res.Components) != 1 {
		t.Fatalf("emitted %+v, want only the helmtemplate", res.Components)
	}
	got := res.Components[0]
	if got.Name != "podinfo" || got.Type != "helmtemplate" {
		t.Errorf("emitted %s/%s, want helmtemplate podinfo", got.Type, got.Name)
	}
	want := map[string]any{
		"source":  map[string]any{"url": "https://charts.example.com", "kind": "HelmRepository"},
		"chart":   "podinfo",
		"version": "6.5.0",
		"values":  map[string]any{"replicaCount": 2},
	}
	if !reflect.DeepEqual(got.Properties, want) {
		t.Errorf("properties = %v, want %v", got.Properties, want)
	}
	if !reflect.DeepEqual(got.Traits, traits) {
		t.Errorf("traits = %v, want the authored %v", got.Traits, traits)
	}
}

// TestHelmRule_NullPassthroughIsAbsent: a passthrough key set to null is not
// forwarded, and so is not refused under delivery: template either.
func TestHelmRule_NullPassthroughIsAbsent(t *testing.T) {
	comps := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{
		"delivery": "template",
		"chart":    "podinfo",
		"interval": nil,
		"values":   nil,
		"source":   map[string]any{"url": "https://charts.example.com"},
	})
	for _, k := range []string{"interval", "values"} {
		if _, ok := comps[0].Properties[k]; ok {
			t.Errorf("null %s was forwarded: %v", k, comps[0].Properties)
		}
	}
}

// TestHelmRule_PassthroughKeyIsCanonical: the decode matches keys
// case-insensitively, so a passthrough key in another spelling is forwarded
// under the spelling the terminal declares, not the authored one.
func TestHelmRule_PassthroughKeyIsCanonical(t *testing.T) {
	comps := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{
		"chart":  "podinfo",
		"Values": map[string]any{"replicaCount": 2},
		"source": map[string]any{"url": "https://charts.example.com"},
	})
	release := componentByType(t, comps, "helmrelease")
	if _, ok := release.Properties["values"]; !ok {
		t.Errorf("release properties = %v, want values under its declared spelling", release.Properties)
	}
	if _, ok := release.Properties["Values"]; ok {
		t.Errorf("release properties = %v, kept the authored spelling Values", release.Properties)
	}
}

// TestHelmRule_Refusals pins every input the rule refuses itself, each with a
// helm: message naming what the author wrote.
func TestHelmRule_Refusals(t *testing.T) {
	repo := map[string]any{"url": "https://charts.example.com"}
	oci := map[string]any{"url": "oci://ghcr.io/example/charts/podinfo"}
	template := func(extra map[string]any) map[string]any {
		m := map[string]any{"delivery": "template", "chart": "podinfo", "source": repo}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"unknown key", map[string]any{"chart": "a", "source": repo, "replicas": 2}, `helm: properties do not decode: json: unknown field "replicas"`},
		{"unknown source key", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "branch": "x"}}, `helm: properties do not decode: json: unknown field "branch"`},
		{"unknown source.ref key", map[string]any{"chart": "a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main", "sha": "x"}}}, `helm: properties do not decode: json: unknown field "sha"`},
		{"two spellings in source.ref", map[string]any{"chart": "a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main", "Branch": "dev"}}}, "helm: source.ref.Branch and source.ref.branch are one key given more than once"},
		{"delivery native", map[string]any{"delivery": "native", "chart": "a", "source": repo}, `helm: unsupported delivery "native"; supported values: flux, template`},
		{"unknown valuesMode", map[string]any{"valuesMode": "file", "chart": "a", "source": repo}, `helm: unsupported valuesMode "file"`},
		{"configMap NaN in values", map[string]any{"valuesMode": "configMap", "chart": "a", "source": repo, "values": map[string]any{"x": math.NaN()}}, "helm: values is not representable as JSON"},
		{"configMap values not an object", map[string]any{"valuesMode": "configMap", "chart": "a", "source": repo, "values": []any{1}}, "values must be a JSON object"},
		{"configMap valuesFrom not a list", map[string]any{"valuesMode": "configMap", "chart": "a", "source": repo, "values": map[string]any{"a": 1}, "valuesFrom": "extra"}, "helm: valuesFrom must be a list, got string"},
		{"no source", map[string]any{"chart": "a"}, "helm: source is required"},
		{"url and name", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "name": "x"}}, "helm: source.url and source.name are mutually exclusive"},
		{"neither url nor name", map[string]any{"chart": "a", "source": map[string]any{"kind": "HelmRepository"}}, "helm: source requires either source.url (inline) or source.name (reference)"},
		{"namespace with url", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "namespace": "x"}}, "helm: source.namespace is only valid with source.name"},
		{"two spellings", map[string]any{"chart": "a", "source": repo, "interval": "1m", "Interval": "2m"}, "helm: Interval and interval are one key given more than once (keys match ignoring case)"},
		{"two spellings by Unicode folding", map[string]any{"chart": "a", "source": repo, "values": map[string]any{"replicas": 1}, "valueſ": map[string]any{"replicas": 9}}, "helm: values and valueſ are one key given more than once"},
		{"two spellings of a decoded key", map[string]any{"chart": "a", "Chart": "b", "source": repo}, "helm: Chart and chart are one key given more than once"},
		{"two spellings of source", map[string]any{"chart": "a", "source": repo, "Source": oci}, "helm: Source and source are one key given more than once"},
		{"two spellings in source", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "URL": "https://other.example.com"}}, "helm: source.URL and source.url are one key given more than once"},
		{"two spellings in Source", map[string]any{"chart": "a", "Source": map[string]any{"kind": "HelmRepository", "Kind": "OCIRepository", "url": "https://charts.example.com"}}, "helm: source.Kind and source.kind are one key given more than once"},
		{"inline HelmRepository without chart", map[string]any{"source": repo}, "helm: source.kind HelmRepository requires chart to be specified"},
		{"inline kind disagrees", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "kind": "OCIRepository"}}, "helm: source.kind OCIRepository requires an oci:// URL"},
		{"inline HelmChart", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "kind": "HelmChart"}}, `helm: source.kind "HelmChart" is not valid for inline source`},
		{"inline OCI with chart", map[string]any{"chart": "a", "source": oci}, "helm: chart is not used with source.kind OCIRepository"},
		{"inline url with user and token", map[string]any{"chart": "a", "source": map[string]any{"url": "https://deploy:FAKE_TOKEN@charts.example.com"}}, "helm: source.url must not carry a user or password, which would be written in plain text into the generated source; author a helmrepository or ocirepository with secretRef and reference it with source.name"},
		{"inline url with user only", map[string]any{"chart": "a", "source": map[string]any{"url": "https://deploy@charts.example.com"}}, "helm: source.url must not carry a user or password, which would be written"},
		{"inline oci url with user and token", map[string]any{"version": "1.0.0", "source": map[string]any{"url": "oci://deploy:FAKE_TOKEN@ghcr.io/example/charts/podinfo"}}, "helm: source.url must not carry a user or password, which would be written"},
		{"inline oci url with user only", map[string]any{"version": "1.0.0", "source": map[string]any{"url": "oci://deploy@ghcr.io/example/charts/podinfo"}}, "helm: source.url must not carry a user or password, which would be written"},
		{"inline url that does not parse", map[string]any{"chart": "a", "source": map[string]any{"url": "https://deploy:FAKE_TOKEN%zz@charts.example.com"}}, "helm: source.url is not a valid URL"},
		{"template url with user and token", template(map[string]any{"source": map[string]any{"url": "https://deploy:FAKE_TOKEN@charts.example.com"}}), "helm: source.url must not carry a user or password; a client-side render takes no credentials"},
		{"template oci url with user only", map[string]any{"delivery": "template", "version": "1.0.0", "source": map[string]any{"url": "oci://deploy@ghcr.io/example/charts/podinfo"}}, "helm: source.url must not carry a user or password; a client-side render takes no credentials"},
		{"reference without kind", map[string]any{"chart": "a", "source": map[string]any{"name": "x"}}, "helm: source.kind is required when source.name is set"},
		{"inline GitRepository without ref", map[string]any{"chart": "a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository"}}, "helm: an inline source.kind GitRepository requires source.ref with exactly one of branch, tag, semver, name, commit"},
		{"inline GitRepository empty ref", map[string]any{"chart": "a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{}}}, "helm: an inline source.kind GitRepository requires source.ref with exactly one of"},
		{"inline GitRepository two ref fields", map[string]any{"chart": "a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main", "tag": "v1.0.0"}}}, "helm: source.ref sets branch, tag; an inline GitRepository takes exactly one of branch, tag, semver, name, commit"},
		{"inline GitRepository oci URL", map[string]any{"chart": "a", "source": map[string]any{"url": "oci://ghcr.io/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.kind GitRepository requires an http:// or https:// URL; an ssh:// repository needs credentials, so author a gitrepository and reference it"},
		{"inline GitRepository URL with user and token", map[string]any{"chart": "a", "source": map[string]any{"url": "https://user:token@github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.url of an inline GitRepository must be an http:// or https:// URL of a host, an optional port in 1-65535 and a repository path only; user info, a query or a fragment is not taken inline (author a gitrepository with a secretRef and reference it)"},
		{"inline GitRepository URL with token only", map[string]any{"chart": "a", "source": map[string]any{"url": "https://token@github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.url of an inline GitRepository must be an http:// or https:// URL of a host, an optional port in 1-65535 and a repository path only; user info, a query or a fragment is not taken inline (author a gitrepository with a secretRef and reference it)"},
		{"inline GitRepository malformed URL with a token", map[string]any{"chart": "a", "source": map[string]any{"url": "https://user:FAKE_TOKEN@github.com/%zz", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.url of an inline GitRepository must be an http:// or https:// URL of a host, an optional port in 1-65535 and a repository path only; user info, a query or a fragment is not taken inline (author a gitrepository with a secretRef and reference it)"},
		{"inline GitRepository ssh URL", map[string]any{"chart": "a", "source": map[string]any{"url": "ssh://git@github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.kind GitRepository requires an http:// or https:// URL; an ssh:// repository needs credentials, so author a gitrepository and reference it"},
		{"inline GitRepository without chart", map[string]any{"source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.kind GitRepository requires chart to be specified"},
		{"inline GitRepository with version", map[string]any{"chart": "./charts/a", "version": "1.0.0", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: version is not used with source.kind GitRepository, whose chart is read at the source's fetched revision"},
		{"inline Bucket with url", map[string]any{"chart": "a", "source": map[string]any{"url": "https://minio.example.com", "kind": "Bucket"}}, "helm: source.kind Bucket takes source.endpoint and source.bucketName, not source.url"},
		{"inline Bucket without bucketName", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com"}}, "helm: an inline source.kind Bucket requires source.endpoint and source.bucketName"},
		{"inline Bucket without endpoint", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "bucketName": "charts"}}, "helm: an inline source.kind Bucket requires source.endpoint and source.bucketName"},
		{"inline Bucket with namespace", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts", "namespace": "x"}}, "helm: source.namespace is only valid with source.name"},
		{"inline Bucket endpoint URL with user and token", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "endpoint": "https://user:FAKE_TOKEN@minio.example.com", "bucketName": "charts"}}, "helm: source.endpoint of an inline Bucket must be a host[:port], or an https:// URL of only a host and an optional port, any port in 1-65535; user info, a path, a query, a fragment or http:// is not taken inline (author a bucket, with a secretRef or insecure: true, and reference it)"},
		{"inline Bucket endpoint host with user and token", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "endpoint": "user:FAKE_TOKEN@minio.example.com:9000", "bucketName": "charts"}}, "helm: source.endpoint of an inline Bucket must be a host[:port], or an https:// URL of only a host and an optional port, any port in 1-65535; user info, a path, a query, a fragment or http:// is not taken inline (author a bucket, with a secretRef or insecure: true, and reference it)"},
		{"inline Bucket endpoint with a signed query", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "endpoint": "https://minio.example.com/?X-Amz-Signature=FAKE_TOKEN", "bucketName": "charts"}}, "helm: source.endpoint of an inline Bucket must be a host[:port], or an https:// URL of only a host and an optional port, any port in 1-65535; user info, a path, a query, a fragment or http:// is not taken inline (author a bucket, with a secretRef or insecure: true, and reference it)"},
		{"inline Bucket endpoint with a fragment", map[string]any{"chart": "a", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com#FAKE_TOKEN", "bucketName": "charts"}}, "helm: source.endpoint of an inline Bucket must be a host[:port], or an https:// URL of only a host and an optional port, any port in 1-65535; user info, a path, a query, a fragment or http:// is not taken inline (author a bucket, with a secretRef or insecure: true, and reference it)"},
		{"inline Bucket without chart", map[string]any{"source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts"}}, "helm: source.kind Bucket requires chart to be specified"},
		{"inline Bucket with version", map[string]any{"chart": "charts/a", "version": "1.0.0", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts"}}, "helm: version is not used with source.kind Bucket, whose chart is read at the source's fetched revision"},
		{"endpoint without Bucket", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "endpoint": "minio.example.com"}}, "helm: source.endpoint is only valid with an inline source.kind Bucket"},
		{"provider on a Bucket reference", map[string]any{"chart": "a", "source": map[string]any{"name": "x", "kind": "Bucket", "provider": "aws"}}, "helm: source.provider is only valid with an inline source.kind Bucket"},
		{"ref without GitRepository", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "ref": map[string]any{"branch": "main"}}}, "helm: source.ref is only valid with an inline source.kind GitRepository"},
		{"ref on a GitRepository reference", map[string]any{"chart": "a", "source": map[string]any{"name": "x", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: source.ref is only valid with an inline source.kind GitRepository"},
		{"reference unknown kind", map[string]any{"chart": "a", "source": map[string]any{"name": "x", "kind": "ExternalArtifact"}}, `helm: source.kind "ExternalArtifact" is not valid for a source reference; must be HelmRepository, GitRepository, Bucket, OCIRepository, or HelmChart`},
		{"reference HelmRepository without chart", map[string]any{"source": map[string]any{"name": "x", "kind": "HelmRepository"}}, "helm: source.kind HelmRepository requires chart to be specified"},
		{"reference GitRepository without chart", map[string]any{"source": map[string]any{"name": "x", "kind": "GitRepository"}}, "helm: source.kind GitRepository requires chart to be specified"},
		{"reference Bucket without chart", map[string]any{"source": map[string]any{"name": "x", "kind": "Bucket"}}, "helm: source.kind Bucket requires chart to be specified"},
		{"reference GitRepository with version", map[string]any{"chart": "./charts/a", "version": "1.0.0", "source": map[string]any{"name": "x", "kind": "GitRepository"}}, "helm: version is not used with source.kind GitRepository, whose chart is read at the source's fetched revision"},
		{"reference Bucket with version", map[string]any{"chart": "charts/a", "version": "1.0.0", "source": map[string]any{"name": "x", "kind": "Bucket"}}, "helm: version is not used with source.kind Bucket, whose chart is read at the source's fetched revision"},
		{"reference OCI with chart", map[string]any{"chart": "a", "source": map[string]any{"name": "x", "kind": "OCIRepository"}}, "helm: chart is not used with source.kind OCIRepository"},
		{"reference HelmChart with version", map[string]any{"version": "1.0.0", "source": map[string]any{"name": "x", "kind": "HelmChart"}}, "helm: version is not used with a referenced source.kind HelmChart"},
		{"template reference", map[string]any{"delivery": "template", "chart": "a", "source": map[string]any{"name": "x", "kind": "HelmRepository"}}, "helm: delivery: template requires an inline source URL; source.name is not supported"},
		{"template inline GitRepository", map[string]any{"delivery": "template", "chart": "./charts/a", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, "helm: delivery: template does not support source.kind GitRepository (a client-side render fetches the chart from a HelmRepository or OCIRepository only)"},
		{"template inline Bucket", map[string]any{"delivery": "template", "chart": "charts/a", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts"}}, "helm: delivery: template does not support source.kind Bucket"},
		{"template GitRepository reference", map[string]any{"delivery": "template", "chart": "./charts/a", "source": map[string]any{"name": "x", "kind": "GitRepository"}}, "helm: delivery: template requires an inline source URL; source.name is not supported"},
		{"template valuesMode configMap", template(map[string]any{"valuesMode": "configMap"}), "helm: delivery: template does not support valuesMode: configMap"},
		{"template OCI without version", map[string]any{"delivery": "template", "source": oci}, "helm: delivery: template with an OCIRepository source requires version to be set"},
		{"template OCI with chart", map[string]any{"delivery": "template", "chart": "a", "version": "1.0.0", "source": oci}, "helm: chart is not used with source.kind OCIRepository"},
	}
	for _, key := range []string{"interval", "releaseName", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"} {
		cases = append(cases, struct {
			name  string
			props map[string]any
			want  string
		}{"template " + key, template(map[string]any{key: map[string]any{}}), "helm: delivery: template does not support " + key + " "})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: "podinfo", Type: "helm", Properties: tc.props}, helmLowering("shop"))
			if err == nil {
				t.Fatalf("LowerComponent accepted %v", tc.props)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "FAKE_TOKEN") {
				t.Errorf("error = %q quotes the credential from source.url", err)
			}
		})
	}
}

// TestHelmRule_AtSignInPathIsNotUserinfo pins that the userinfo refusal reads
// the parsed URL, not any "@": one in the path is accepted.
func TestHelmRule_AtSignInPathIsNotUserinfo(t *testing.T) {
	for _, delivery := range []string{"flux", "template"} {
		props := map[string]any{"delivery": delivery, "chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com/team@example"}}
		if _, err := (components.HelmRule{}).LowerComponent(&oam.Component{Name: "podinfo", Type: "helm", Properties: props}, helmLowering("shop")); err != nil {
			t.Errorf("delivery %s: LowerComponent refused an @ in the URL path: %v", delivery, err)
		}
	}
}

// TestHelmRule_ReconcileStrategyRevisionForGitAndBucket: a release reading its
// chart from a GitRepository or Bucket, inline or referenced, sets
// reconcileStrategy Revision, so a new source revision with an unchanged chart
// version still deploys. A HelmRepository release keeps Flux's ChartVersion
// default and carries no reconcileStrategy key.
// TestHelmRule_InlineSourceAddressShape: an inline GitRepository url must be
// exactly a host, optional port and repository path, and an inline Bucket
// endpoint exactly a host and optional port, any port in 1-65535. Everything else is refused without
// echoing the value, and every plain form is accepted.
func TestHelmRule_InlineSourceAddressShape(t *testing.T) {
	git := func(u string) map[string]any {
		return map[string]any{"chart": "./charts/a", "source": map[string]any{"url": u, "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}
	}
	bucket := func(ep string) map[string]any {
		return map[string]any{"chart": "charts/a", "source": map[string]any{"kind": "Bucket", "endpoint": ep, "bucketName": "charts"}}
	}
	lower := func(props map[string]any) error {
		_, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: "podinfo", Type: "helm", Properties: props}, helmLowering("shop"))
		return err
	}
	refused := map[string]map[string]any{
		"git query":          git("https://github.com/example/charts?token=FAKE_TOKEN"),
		"git empty query":    git("https://github.com/example/charts?"),
		"git fragment":       git("https://github.com/example/charts#FAKE_TOKEN"),
		"git empty fragment": git("https://github.com/example/charts#"),
		"git empty host":     git("https:///example/charts"),
		"git no host":        git("https://"),
		"git no path":        git("https://github.com"),
		"git root path":      git("https://github.com/"),
		"git empty port":     git("https://github.com:/example/charts"),
		"bucket path":        bucket("https://minio.example.com/FAKE_TOKEN"),
		"bucket bare path":   bucket("minio.example.com/charts"),
		"bucket scheme only": bucket("https://"),
		"bucket ftp":         bucket("ftp://minio.example.com"),
		"bucket empty port":  bucket("minio.example.com:"),
		"bucket http":        bucket("http://minio.example.com:9000"),
		// url.Parse takes any digits as a port; the range is the rule's.
		"git port 0":            git("https://git.example.com:0/example/charts"),
		"git port 65536":        git("https://git.example.com:65536/example/charts"),
		"git port 99999":        git("http://git.example.com:99999/example/charts"),
		"git port overflow":     git("https://git.example.com:99999999999999999999/example/charts"),
		"git ipv6 port 0":       git("https://[::1]:0/example/charts"),
		"bucket port 0":         bucket("minio.example.com:0"),
		"bucket port 65536":     bucket("minio.example.com:65536"),
		"bucket port 99999":     bucket("minio.example.com:99999"),
		"bucket port overflow":  bucket("minio.example.com:99999999999999999999"),
		"bucket ipv6 port 0":    bucket("[::1]:0"),
		"bucket https port 0":   bucket("https://minio.example.com:0"),
		"bucket https port big": bucket("https://minio.example.com:65536/"),
	}
	for name, props := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			err := lower(props)
			if err == nil || !strings.Contains(err.Error(), "is not taken inline") {
				t.Fatalf("error = %v, want the inline address refusal", err)
			}
			if strings.Contains(err.Error(), "FAKE_TOKEN") {
				t.Errorf("error = %q quotes the address", err)
			}
		})
	}
	accepted := map[string]map[string]any{
		"git https":          git("https://github.com/example/charts"),
		"git http with port": git("http://git.example.com:8080/example/charts.git"),
		"git trailing slash": git("https://github.com/example/charts/"),
		"bucket host":        bucket("minio.example.com"),
		"bucket host:port":   bucket("minio.example.com:9000"),
		"bucket ipv6":        bucket("[::1]:9000"),
		"bucket https":       bucket("https://minio.example.com"),
		"bucket https slash": bucket("https://minio.example.com:9000/"),
		"git port 1":         git("https://git.example.com:1/example/charts"),
		"git port 65535":     git("https://git.example.com:65535/example/charts"),
		"bucket port 1":      bucket("minio.example.com:1"),
		"bucket port 65535":  bucket("https://minio.example.com:65535"),
		"bucket leading 0":   bucket("minio.example.com:09000"),
	}
	for name, props := range accepted {
		t.Run("accepted/"+name, func(t *testing.T) {
			if err := lower(props); err != nil {
				t.Fatalf("refused a plain address: %v", err)
			}
		})
	}
}

// TestHelmRule_InlineBucketProviderEnum: the authored pipeline's schema check
// refuses an inline Bucket provider outside Flux's set, so a typo fails the build
// instead of reaching the generated bucket.
func TestHelmRule_InlineBucketProviderEnum(t *testing.T) {
	validate := func(provider string) error {
		tr := oam.NewTransformer(nil, nil)
		tr.RegisterComponentLowering(components.HelmRule{})
		return tr.ValidateAuthoredProperties(&oam.Application{Spec: oam.ApplicationSpec{
			Components: []oam.Component{{Name: "web", Type: "helm", Properties: map[string]any{
				"chart":  "charts/a",
				"source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts", "provider": provider},
			}}},
		}})
	}
	for _, p := range []string{"generic", "aws", "gcp", "azure"} {
		if err := validate(p); err != nil {
			t.Errorf("provider %q refused: %v", p, err)
		}
	}
	if err := validate("awz"); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Errorf("provider awz: error = %v, want a refusal naming provider", err)
	}
}

func TestHelmRule_ReconcileStrategyRevisionForGitAndBucket(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  bool
	}{
		{"inline HelmRepository", map[string]any{"chart": "podinfo", "version": "6.5.0", "source": map[string]any{"url": "https://charts.example.com"}}, false},
		{"referenced HelmRepository", map[string]any{"chart": "podinfo", "source": map[string]any{"name": "charts", "kind": "HelmRepository"}}, false},
		{"inline GitRepository", map[string]any{"chart": "./charts/podinfo", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}, true},
		{"referenced GitRepository", map[string]any{"chart": "./charts/podinfo", "source": map[string]any{"name": "podinfo", "kind": "GitRepository"}}, true},
		{"inline Bucket", map[string]any{"chart": "charts/podinfo", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts"}}, true},
		{"referenced Bucket", map[string]any{"chart": "charts/podinfo", "source": map[string]any{"name": "artifacts", "kind": "Bucket"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := componentByType(t, lowerHelm(t, helmLowering("shop"), "podinfo", tc.props), "helmrelease")
			spec := release.Properties["chart"].(map[string]any)["spec"].(map[string]any)
			got, set := spec["reconcileStrategy"]
			if tc.want && got != "Revision" {
				t.Errorf("chart.spec.reconcileStrategy = %v, want Revision", got)
			}
			if !tc.want && set {
				t.Errorf("chart.spec.reconcileStrategy = %v, want the key absent", got)
			}
		})
	}
}
