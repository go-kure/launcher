package components_test

import (
	"crypto/sha256"
	"encoding/hex"
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
// verbatim; nothing but the URL reaches the source.
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
		"valuesMode": "configMap",
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
		"valuesMode": "configMap",
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
	wantSource := map[string]any{"url": "oci://ghcr.io/example/charts/podinfo", "ref": map[string]any{"tag": "6.5.0"}}
	if !reflect.DeepEqual(source.Properties, wantSource) {
		t.Errorf("source properties = %v, want %v", source.Properties, wantSource)
	}
	release := componentByType(t, comps, "helmrelease")
	wantRelease := map[string]any{"chartRef": map[string]any{"kind": "OCIRepository", "name": wantName}}
	if !reflect.DeepEqual(release.Properties, wantRelease) {
		t.Errorf("release properties = %v, want %v", release.Properties, wantRelease)
	}
}

// TestHelmRule_OCIWithoutVersion: an inline OCI source without a version is
// accepted under delivery: flux, as helmchart accepts it: the source carries no
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
// kind, with its namespace, and nothing but the release is emitted.
func TestHelmRule_ReferenceForm(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		key   string
		want  any
	}{
		{"HelmRepository", map[string]any{"chart": "podinfo", "version": "6.5.0", "source": map[string]any{"name": "charts", "kind": "HelmRepository", "namespace": "flux-system"}},
			"chart", map[string]any{"spec": map[string]any{"chart": "podinfo", "version": "6.5.0", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts", "namespace": "flux-system"}}}},
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
		{"unknown source key", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "ref": "x"}}, `helm: properties do not decode: json: unknown field "ref"`},
		{"delivery native", map[string]any{"delivery": "native", "chart": "a", "source": repo}, `helm: unsupported delivery "native"; supported values: flux, template`},
		{"unknown valuesMode", map[string]any{"valuesMode": "file", "chart": "a", "source": repo}, `helm: unsupported valuesMode "file"`},
		{"no source", map[string]any{"chart": "a"}, "helm: source is required"},
		{"url and name", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "name": "x"}}, "helm: source.url and source.name are mutually exclusive"},
		{"neither url nor name", map[string]any{"chart": "a", "source": map[string]any{"kind": "HelmRepository"}}, "helm: source requires either source.url (inline) or source.name (reference)"},
		{"namespace with url", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "namespace": "x"}}, "helm: source.namespace is only valid with source.name"},
		{"two spellings", map[string]any{"chart": "a", "source": repo, "interval": "1m", "Interval": "2m"}, "is given more than once"},
		{"inline HelmRepository without chart", map[string]any{"source": repo}, "helm: source.kind HelmRepository requires chart to be specified"},
		{"inline kind disagrees", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "kind": "OCIRepository"}}, "helm: source.kind OCIRepository requires an oci:// URL"},
		{"inline HelmChart", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "kind": "HelmChart"}}, `helm: source.kind "HelmChart" is not valid for inline source`},
		{"inline OCI with chart", map[string]any{"chart": "a", "source": oci}, "helm: chart is not used with source.kind OCIRepository"},
		{"reference without kind", map[string]any{"chart": "a", "source": map[string]any{"name": "x"}}, "helm: source.kind is required when source.name is set"},
		{"reference HelmRepository without chart", map[string]any{"source": map[string]any{"name": "x", "kind": "HelmRepository"}}, "helm: source.kind HelmRepository requires chart to be specified"},
		{"reference OCI with chart", map[string]any{"chart": "a", "source": map[string]any{"name": "x", "kind": "OCIRepository"}}, "helm: chart is not used with source.kind OCIRepository"},
		{"reference HelmChart with version", map[string]any{"version": "1.0.0", "source": map[string]any{"name": "x", "kind": "HelmChart"}}, "helm: version is not used with a referenced source.kind HelmChart"},
		{"template reference", map[string]any{"delivery": "template", "chart": "a", "source": map[string]any{"name": "x", "kind": "HelmRepository"}}, "helm: delivery: template requires an inline source URL; source.name is not supported"},
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
		})
	}
}
