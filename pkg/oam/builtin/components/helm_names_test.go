package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The names a helm or oci component gives the objects it generates
// (go-kure/launcher#787): source.name beside an inline source,
// valuesConfigMapName and valuesSecretName. These tests pin the lowering's own
// output; the Naming hook and the Flux namespace are checked end to end in
// pkg/cmd/kurel.

// releaseSourceRef returns the reference a helmrelease holds to its source:
// chart.spec.sourceRef, or chartRef for an OCIRepository.
func releaseSourceRef(t *testing.T, release oam.Component) map[string]any {
	t.Helper()
	if ref, ok := release.Properties["chartRef"].(map[string]any); ok {
		return ref
	}
	chart, _ := release.Properties["chart"].(map[string]any)
	spec, _ := chart["spec"].(map[string]any)
	ref, ok := spec["sourceRef"].(map[string]any)
	if !ok {
		t.Fatalf("release %s holds no source reference: %v", release.Name, release.Properties)
	}
	return ref
}

// TestHelmRule_SourceNameNamesTheGeneratedSource: source.name beside an inline
// source of each kind names the generated source, as written, and the release
// references it by that name with no namespace. The source's properties are
// those of the unnamed one.
func TestHelmRule_SourceNameNamesTheGeneratedSource(t *testing.T) {
	for _, tc := range []struct {
		kind, typ string
		props     map[string]any
	}{
		{"HelmRepository", "helmrepository", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com"}}},
		{"OCIRepository", "ocirepository", map[string]any{"version": "6.5.0", "source": map[string]any{"url": "oci://ghcr.io/example/charts/podinfo"}}},
		{"GitRepository", "gitrepository", map[string]any{"chart": "./charts/podinfo", "source": map[string]any{"url": "https://github.com/example/charts", "kind": "GitRepository", "ref": map[string]any{"branch": "main"}}}},
		{"Bucket", "bucket", map[string]any{"chart": "charts/podinfo", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts"}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			unnamed := componentByType(t, lowerHelm(t, helmLowering("shop"), "podinfo", tc.props), tc.typ)
			if !strings.HasPrefix(unnamed.Name, "shop-source-") {
				t.Fatalf("the unnamed source is %q, want the document's shop-source-<digest>", unnamed.Name)
			}

			named := maps2(tc.props)
			named["source"].(map[string]any)["name"] = "upstream-charts"
			comps := lowerHelm(t, helmLowering("shop"), "podinfo", named)
			if len(comps) != 2 {
				t.Fatalf("emitted %d components, want a source and a release: %+v", len(comps), comps)
			}
			source := componentByType(t, comps, tc.typ)
			if source.Name != "upstream-charts" {
				t.Errorf("source name = %q, want the authored upstream-charts", source.Name)
			}
			if !reflect.DeepEqual(source.Properties, unnamed.Properties) {
				t.Errorf("named source properties = %v, want the unnamed source's %v", source.Properties, unnamed.Properties)
			}
			if len(source.Traits) != 0 || len(source.Annotations) != 0 {
				t.Errorf("source carries traits %v / annotations %v, want none", source.Traits, source.Annotations)
			}
			release := componentByType(t, comps, "helmrelease")
			if want := map[string]any{"kind": tc.kind, "name": "upstream-charts"}; !reflect.DeepEqual(releaseSourceRef(t, release), want) {
				t.Errorf("release references %v, want %v", releaseSourceRef(t, release), want)
			}
		})
	}
}

// maps2 copies props and its source map, so that a test can add to the source.
func maps2(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[k] = v
	}
	src := make(map[string]any)
	for k, v := range props["source"].(map[string]any) {
		src[k] = v
	}
	out["source"] = src
	return out
}

// TestHelmRule_SourceNameSharing: an authored source.name is the component's
// own. Two components of one identity, one naming the source and one not, get
// two sources; two that write the same name for the same identity share one;
// the same name for two identities is a collision.
func TestHelmRule_SourceNameSharing(t *testing.T) {
	const url = "https://charts.example.com"
	plain := func(chart string) map[string]any {
		return map[string]any{"chart": chart, "source": map[string]any{"url": url}}
	}
	named := func(chart, url, name string) map[string]any {
		return map[string]any{"chart": chart, "source": map[string]any{"url": url, "name": name}}
	}

	t.Run("one names it and one does not", func(t *testing.T) {
		lctx := helmLowering("shop")
		a := lowerHelm(t, lctx, "a", named("a", url, "charts"))
		b := lowerHelm(t, lctx, "b", plain("b"))
		if len(a) != 2 || len(b) != 2 {
			t.Fatalf("a emitted %d components and b %d, want a source and a release each", len(a), len(b))
		}
		if got := componentByType(t, a, "helmrepository").Name; got != "charts" {
			t.Errorf("a's source = %q, want charts", got)
		}
		shared := helmSourceName("shop", "helm:"+url)
		if got := componentByType(t, b, "helmrepository").Name; got != shared {
			t.Errorf("b's source = %q, want the document's %s", got, shared)
		}
		// A third component that names none adopts the document's, not a's.
		c := lowerHelm(t, lctx, "c", plain("c"))
		if len(c) != 1 || releaseSourceRef(t, c[0])["name"] != shared {
			t.Errorf("c lowered to %+v, want its release alone on %s", c, shared)
		}
	})

	t.Run("the same name for the same identity", func(t *testing.T) {
		lctx := helmLowering("shop")
		a := lowerHelm(t, lctx, "a", named("a", url, "charts"))
		b := lowerHelm(t, lctx, "b", named("b", url, "charts"))
		if len(a) != 2 {
			t.Fatalf("a emitted %d components, want the source and its release", len(a))
		}
		if len(b) != 1 || b[0].Type != "helmrelease" || releaseSourceRef(t, b[0])["name"] != "charts" {
			t.Fatalf("b lowered to %+v, want its release alone on charts", b)
		}
	})

	t.Run("the same name for two identities", func(t *testing.T) {
		lctx := helmLowering("shop")
		lowerHelm(t, lctx, "a", named("a", url, "charts"))
		lctx.Origin.Component, lctx.Origin.ComponentType = "b", "helm"
		_, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: "b", Type: "helm", Properties: named("b", "https://other.example.com", "charts")}, lctx)
		const want = `helm: naming the generated source: lowering: generated name "charts" collides`
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "emitted it for different content") {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})

	t.Run("a refused component claims no name", func(t *testing.T) {
		lctx := helmLowering("shop")
		lctx.Origin.Component, lctx.Origin.ComponentType = "a", "helm"
		// version beside a Bucket source is refused after the source is decoded.
		refused := map[string]any{"chart": "a", "version": "1.0.0", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts", "name": "charts"}}
		if _, err := (components.HelmRule{}).LowerComponent(&oam.Component{Name: "a", Type: "helm", Properties: refused}, lctx); err == nil {
			t.Fatal("the Bucket source with a version was accepted")
		}
		if b := lowerHelm(t, lctx, "b", named("b", url, "charts")); len(b) != 2 {
			t.Fatalf("b lowered to %+v, want its own source charts: the refused component claimed the name", b)
		}
	})
}

// TestHelmRule_ReferenceIsNoGeneratedSource: source.name alone still references
// an existing source and generates none, whatever a component beside it names.
func TestHelmRule_ReferenceIsNoGeneratedSource(t *testing.T) {
	lctx := helmLowering("shop")
	lowerHelm(t, lctx, "a", map[string]any{"chart": "a", "source": map[string]any{"url": "https://charts.example.com", "name": "charts"}})
	b := lowerHelm(t, lctx, "b", map[string]any{"chart": "b", "source": map[string]any{"name": "charts", "kind": "HelmRepository"}})
	if len(b) != 1 || b[0].Type != "helmrelease" {
		t.Fatalf("the reference lowered to %+v, want its release alone", b)
	}
	if want := map[string]any{"kind": "HelmRepository", "name": "charts"}; !reflect.DeepEqual(releaseSourceRef(t, b[0]), want) {
		t.Errorf("the reference = %v, want %v", releaseSourceRef(t, b[0]), want)
	}
}

// TestHelmRule_ValuesNames: valuesConfigMapName and valuesSecretName name the
// generated values ConfigMap and Secret as written, with no hash, so the name
// does not move when the values do. The trait and the valuesFrom entry carry
// the same name, and everything else is as without the property.
func TestHelmRule_ValuesNames(t *testing.T) {
	props := func(values, secretValues map[string]any) map[string]any {
		return map[string]any{
			"chart":               "podinfo",
			"source":              map[string]any{"kind": "HelmRepository", "name": "podinfo"},
			"valuesMode":          "configMap",
			"values":              values,
			"secretValues":        secretValues,
			"valuesConfigMapName": "web-settings",
			"valuesSecretName":    "web-credentials",
			"valuesFrom":          []any{map[string]any{"kind": "Secret", "name": "creds"}},
		}
	}
	lower := func(t *testing.T, p map[string]any) (release oam.Component, configMap, secret oam.Trait) {
		t.Helper()
		release = lowerHelmRelease(t, "web", p)
		for _, key := range []string{"valuesConfigMapName", "valuesSecretName", "valuesMode", "secretValues", "values"} {
			if _, ok := release.Properties[key]; ok {
				t.Errorf("%s forwarded to the helmrelease: %v", key, release.Properties)
			}
		}
		if len(release.Traits) != 2 || release.Traits[0].Type != "configmap" || release.Traits[1].Type != "secret" {
			t.Fatalf("release traits = %+v, want the values configmap and the values secret", release.Traits)
		}
		return release, release.Traits[0], release.Traits[1]
	}

	release, configMap, secret := lower(t, props(map[string]any{"replicaCount": 2}, map[string]any{"password": sensitive}))
	if got := configMap.Properties["name"]; got != "web-settings" {
		t.Errorf("ConfigMap name = %v, want the authored web-settings", got)
	}
	if got := secret.Properties["name"]; got != "web-credentials" {
		t.Errorf("Secret name = %v, want the authored web-credentials", got)
	}
	if got, want := valuesFromNames(t, release), []string{"ConfigMap/web-settings", "Secret/web-credentials", "Secret/creds"}; !reflect.DeepEqual(got, want) {
		t.Errorf("valuesFrom = %v, want %v", got, want)
	}

	// The control: without the two properties each name ends in its hash.
	plain := props(map[string]any{"replicaCount": 2}, map[string]any{"password": sensitive})
	delete(plain, "valuesConfigMapName")
	delete(plain, "valuesSecretName")
	_, defaultConfigMap, defaultSecret := lower(t, plain)
	if name, _ := defaultConfigMap.Properties["name"].(string); !strings.HasPrefix(name, "web-values-") {
		t.Errorf("default ConfigMap name = %q, want web-values-<hash>", name)
	}
	if name, _ := defaultSecret.Properties["name"].(string); !strings.HasPrefix(name, "web-secret-values-") {
		t.Errorf("default Secret name = %q, want web-secret-values-<hash>", name)
	}
	if !reflect.DeepEqual(configMap.Properties["data"], defaultConfigMap.Properties["data"]) {
		t.Errorf("the named ConfigMap stores %v, want the default one's %v", configMap.Properties["data"], defaultConfigMap.Properties["data"])
	}

	// Other values, the same names.
	_, edited, editedSecret := lower(t, props(map[string]any{"replicaCount": 3}, map[string]any{"password": "other"}))
	if edited.Properties["name"] != "web-settings" || editedSecret.Properties["name"] != "web-credentials" {
		t.Errorf("after a values edit the names are %v and %v, want them unchanged", edited.Properties["name"], editedSecret.Properties["name"])
	}
	if reflect.DeepEqual(edited.Properties["data"], configMap.Properties["data"]) {
		t.Error("the edited values did not reach the ConfigMap")
	}
}

// TestHelmRule_ValuesNamesAreOneObjectEach: two components of one document that
// write one valuesConfigMapName, or one valuesSecretName, name one object
// twice, and the second is refused with both named.
func TestHelmRule_ValuesNamesAreOneObjectEach(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			"valuesConfigMapName",
			map[string]any{"chart": "a", "source": map[string]any{"kind": "HelmRepository", "name": "charts"}, "valuesMode": "configMap", "values": map[string]any{"a": 1}, "valuesConfigMapName": "shared"},
			`helm: naming the values ConfigMap: name collision: ConfigMap "shared" is named by component "a" (role "values-configmap", set by valuesConfigMapName) and by component "b" (role "values-configmap", set by valuesConfigMapName); give one of them another name`,
		},
		{
			"valuesSecretName",
			map[string]any{"chart": "a", "source": map[string]any{"kind": "HelmRepository", "name": "charts"}, "secretValues": map[string]any{"a": "b"}, "valuesSecretName": "shared"},
			`helm: naming the values Secret: name collision: Secret "shared" is named by component "a" (role "values-secret", set by valuesSecretName) and by component "b" (role "values-secret", set by valuesSecretName); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lctx := helmLowering("shop")
			lowerHelm(t, lctx, "a", tc.props)
			lctx.Origin.Component, lctx.Origin.ComponentType = "b", "helm"
			_, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: "b", Type: "helm", Properties: tc.props}, lctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %s", err, tc.want)
			}
			if strings.Contains(err.Error(), sensitive) {
				t.Errorf("error = %q carries a secret value", err)
			}
		})
	}
}

// TestOCIRule_SourceName: an authored source.name makes the oci component's
// source the shared form under that name: emitted apart from the Kustomization,
// with neither the component's annotations nor its traits, also for a component
// alone on its artifact. A component that writes none keeps its own source.
func TestOCIRule_SourceName(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	protection := oam.Trait{Type: "prune-protection", Properties: map[string]any{}}
	named := func(name string) map[string]any {
		props := ociProps(url, "1.4.0", "")
		props["source"].(map[string]any)["name"] = name
		return props
	}
	app := ociDocument(
		oam.Component{Name: "base", Type: "oci", Properties: named("platform"),
			Traits: []oam.Trait{protection}, Annotations: map[string]string{"gokure.dev/tier": "infra"}},
		oam.Component{Name: "addons", Type: "oci", Properties: named("platform")},
		oam.Component{Name: "alone", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
	)
	lctx := ociLowering("shop")

	base := lowerOCI(t, lctx, app, "base")
	if len(base) != 2 || base[0].Type != "ocirepository" || base[1].Type != "fluxcd-kustomization" {
		t.Fatalf("base lowered to %+v, want the named ocirepository and its fluxcd-kustomization", base)
	}
	if base[0].Name != "platform" {
		t.Errorf("source name = %q, want the authored platform", base[0].Name)
	}
	if len(base[0].Traits) != 0 || len(base[0].Annotations) != 0 {
		t.Errorf("the named source carries traits %+v and annotations %+v, want neither", base[0].Traits, base[0].Annotations)
	}
	if want := map[string]any{"url": url, "ref": map[string]any{"tag": "1.4.0"}}; !reflect.DeepEqual(base[0].Properties, want) {
		t.Errorf("source properties = %v, want %v", base[0].Properties, want)
	}
	if !reflect.DeepEqual(base[1].Traits, []oam.Trait{protection}) || base[1].Annotations["gokure.dev/tier"] != "infra" {
		t.Errorf("base's fluxcd-kustomization carries traits %+v and annotations %+v, want the authored ones", base[1].Traits, base[1].Annotations)
	}

	addons := lowerOCI(t, lctx, app, "addons")
	if len(addons) != 1 || addons[0].Type != "fluxcd-kustomization" {
		t.Fatalf("addons lowered to %+v, want its fluxcd-kustomization alone", addons)
	}
	for _, kz := range []oam.Component{base[1], addons[0]} {
		if ref := kz.Properties["sourceRef"].(map[string]any); ref["kind"] != "OCIRepository" || ref["name"] != "platform" {
			t.Errorf("%s: sourceRef = %+v, want OCIRepository/platform", kz.Name, ref)
		}
	}

	// The component that names none is alone among the unnamed consumers of the
	// artifact, so it keeps the same-name pair.
	alone := lowerOCI(t, lctx, app, "alone")
	if len(alone) != 2 || alone[0].Name != "alone" || alone[1].Name != "alone" {
		t.Errorf("the unnamed component lowered to %+v, want its own same-name pair", alone)
	}
}

// TestOCIRule_SourceNameRefusals: the name is refused where it is the
// component's own, no object name, or already another identity's.
func TestOCIRule_SourceNameRefusals(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	named := func(url, name string) map[string]any {
		props := ociProps(url, "1.4.0", "")
		props["source"].(map[string]any)["name"] = name
		return props
	}
	lowerErr := func(lctx oam.LoweringContext, app *oam.Application, i int) error {
		lctx.Document = app
		lctx.Origin.Component, lctx.Origin.ComponentType = app.Spec.Components[i].Name, "oci"
		_, err := components.OCIRule{}.LowerComponent(&app.Spec.Components[i], lctx)
		return err
	}

	t.Run("the component's own name", func(t *testing.T) {
		app := ociDocument(oam.Component{Name: "base", Type: "oci", Properties: named(url, "base")})
		err := lowerErr(ociLowering("shop"), app, 0)
		const want = `oci: source.name "base" is the component's own name; the generated source is a component of the document too, so give it another name`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant %s", err, want)
		}
	})
	t.Run("no object name", func(t *testing.T) {
		app := ociDocument(oam.Component{Name: "base", Type: "oci", Properties: named(url, "Platform")})
		err := lowerErr(ociLowering("shop"), app, 0)
		const want = `oci: naming the generated source: source.name "Platform" cannot be the name for role "helm-source": `
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})
	t.Run("not a string", func(t *testing.T) {
		props := ociProps(url, "1.4.0", "")
		props["source"].(map[string]any)["name"] = 7
		app := ociDocument(oam.Component{Name: "base", Type: "oci", Properties: props})
		if err := lowerErr(ociLowering("shop"), app, 0); err == nil || !strings.Contains(err.Error(), "oci: source.name") {
			t.Fatalf("err = %v, want source.name refused", err)
		}
	})
	t.Run("the same name for two identities", func(t *testing.T) {
		app := ociDocument(
			oam.Component{Name: "base", Type: "oci", Properties: named(url, "platform")},
			oam.Component{Name: "addons", Type: "oci", Properties: named("oci://registry.example.com/org/other", "platform")},
		)
		lctx := ociLowering("shop")
		if err := lowerErr(lctx, app, 0); err != nil {
			t.Fatalf("base: %v", err)
		}
		err := lowerErr(lctx, app, 1)
		const want = `oci: naming the generated source: lowering: generated name "platform" collides`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})
}

// An empty source.name reads as none, for both rules.
func TestSourceName_EmptyIsAbsent(t *testing.T) {
	props := ociProps("oci://registry.example.com/org/platform", "1.4.0", "")
	props["source"].(map[string]any)["name"] = ""
	comp := oam.Component{Name: "base", Type: "oci", Properties: props}
	if comps := lowerOCI(t, ociLowering("shop"), ociDocument(comp), "base"); len(comps) != 2 || comps[0].Name != "base" {
		t.Errorf("oci with an empty source.name lowered to %+v, want its own same-name pair", comps)
	}
	helm := lowerHelm(t, helmLowering("shop"), "podinfo", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com", "name": ""}})
	if got, want := componentByType(t, helm, "helmrepository").Name, helmSourceName("shop", "helm:https://charts.example.com"); got != want {
		t.Errorf("helm with an empty source.name names its source %q, want the document's %s", got, want)
	}
}
