package components_test

import (
	"reflect"
	"strings"
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// fakeOCIPolicy implements oam.Policy via an embedded (nil) interface and
// overrides only AllowedRegistries — enough to exercise ApplyPolicy's host check.
type fakeOCIPolicy struct {
	oam.Policy
	allowed []string
}

func (f fakeOCIPolicy) AllowedRegistries() []string { return f.allowed }

func ociComponent(props map[string]any) *oam.Component {
	return &oam.Component{Name: "checkout", Type: "oci", Properties: props}
}

func validOCIProps() map[string]any {
	return map[string]any{
		"source":  map[string]any{"url": "oci://registry.example.com/charts/checkout"},
		"version": "0.3.0",
	}
}

// mustOCIConfig lowers an oci component through the rule and converts the two
// components it emits (ociViaRule).
func mustOCIConfig(t *testing.T, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := ociViaRule{}.ToApplicationConfig(ociComponent(props), "checkout")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

// ociDocument is an authored document holding the given components.
func ociDocument(comps ...oam.Component) *oam.Application {
	return &oam.Application{Spec: oam.ApplicationSpec{Components: comps}}
}

// lowerOCI runs OCIRule.LowerComponent on the named component of app, with the
// lowering context the engine gives it: the document, and a namer shared by
// every call that passes the same lctx.
func lowerOCI(t *testing.T, lctx oam.LoweringContext, app *oam.Application, name string) []oam.Component {
	t.Helper()
	for i := range app.Spec.Components {
		comp := &app.Spec.Components[i]
		if comp.Name != name {
			continue
		}
		lctx.Document = app
		lctx.Origin.Component = name
		lctx.Origin.ComponentType = "oci"
		res, err := components.OCIRule{}.LowerComponent(comp, lctx)
		if err != nil {
			t.Fatalf("LowerComponent(%s): %v", name, err)
		}
		if len(res.Documents) != 0 || len(res.Traits) != 0 || len(res.Policies) != 0 {
			t.Fatalf("LowerComponent(%s) emitted more than components: %+v", name, res)
		}
		return res.Components
	}
	t.Fatalf("no component %q in the document", name)
	return nil
}

// ociLowering is one run's lowering context for the components of document doc.
func ociLowering(doc string) oam.LoweringContext {
	return oam.LoweringContext{
		Namer:  oam.NewNameAllocator(),
		Origin: oam.Origin{Document: doc, DocumentKind: "Application", Namespace: "default"},
	}
}

// ociSourceIdentity is the identity the rule gives a source: the url, the
// version and the effective interval, as time.Duration prints it.
func ociSourceIdentity(url, version, interval string) string {
	return `oci-artifact:{"url":"` + url + `","version":"` + version + `","interval":"` + interval + `"}`
}

func ociProps(url, version, interval string) map[string]any {
	props := map[string]any{"source": map[string]any{"url": url}, "version": version}
	if interval != "" {
		props["interval"] = interval
	}
	return props
}

func TestOCIRule_ComponentType(t *testing.T) {
	if got := (components.OCIRule{}).ComponentType(); got != "oci" {
		t.Errorf("ComponentType() = %q, want oci", got)
	}
}

// TestOCIRule_LowersToSameNamePair: a component whose source no other shares
// lowers to an ocirepository and a fluxcd-kustomization, both named after it,
// the source first. Each carries exactly the properties its object needs; the
// annotations go to both, as do the two traits that decorate every object a
// component generates, and every other trait goes to the fluxcd-kustomization.
func TestOCIRule_LowersToSameNamePair(t *testing.T) {
	protection := oam.Trait{Type: "prune-protection", Properties: map[string]any{}}
	replace := oam.Trait{Type: "force-replace", Properties: map[string]any{}}
	other := oam.Trait{Type: "configmap", Properties: map[string]any{"name": "settings"}}
	props := validOCIProps()
	props["interval"] = "10m"
	props["path"] = "./deploy"
	props["prune"] = false
	props["targetNamespace"] = "checkout-workload"
	props["healthChecks"] = []any{
		map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "checkout-api", "namespace": "checkout"},
		map[string]any{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition", "name": "orders.shop.example.com"},
	}
	comp := oam.Component{
		Name: "checkout", Type: "oci", Properties: props,
		Traits:      []oam.Trait{protection, other, replace},
		Annotations: map[string]string{"gokure.dev/tier": "infra"},
	}
	app := ociDocument(comp)

	for name, comps := range map[string][]oam.Component{
		"in a document":      lowerOCI(t, ociLowering("shop"), app, "checkout"),
		"without a document": mustLowerOCIAlone(t, &comp),
	} {
		t.Run(name, func(t *testing.T) {
			want := []oam.Component{
				{
					Name: "checkout", Type: "ocirepository",
					Properties: map[string]any{
						"url":      "oci://registry.example.com/charts/checkout",
						"ref":      map[string]any{"tag": "0.3.0"},
						"interval": "10m",
					},
					Traits:      []oam.Trait{protection, replace},
					Annotations: map[string]string{"gokure.dev/tier": "infra"},
				},
				{
					Name: "checkout", Type: "fluxcd-kustomization",
					Properties: map[string]any{
						"interval":        "10m",
						"path":            "./deploy",
						"prune":           false,
						"targetNamespace": "checkout-workload",
						"sourceRef":       map[string]any{"kind": "OCIRepository", "name": "checkout"},
						"healthChecks": []any{
							map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "checkout-api", "namespace": "checkout"},
							map[string]any{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition", "name": "orders.shop.example.com"},
						},
					},
					Traits:      []oam.Trait{protection, other, replace},
					Annotations: map[string]string{"gokure.dev/tier": "infra"},
				},
			}
			if !reflect.DeepEqual(comps, want) {
				t.Errorf("lowered to\n%+v\nwant\n%+v", comps, want)
			}
		})
	}

	// The members' annotations are their own maps: editing one member's leaves
	// the other's and the authored component's alone.
	comps := lowerOCI(t, ociLowering("shop"), app, "checkout")
	comps[0].Annotations["edited"] = "yes"
	if _, leaked := comps[1].Annotations["edited"]; leaked {
		t.Error("the two members share one annotations map")
	}
	if _, leaked := comp.Annotations["edited"]; leaked {
		t.Error("a member shares the authored component's annotations map")
	}
}

// mustLowerOCIAlone runs the rule with no document, as a direct driver does.
func mustLowerOCIAlone(t *testing.T, comp *oam.Component) []oam.Component {
	t.Helper()
	res, err := components.OCIRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	return res.Components
}

// TestOCIRule_DefaultsAreTheTerminals: with nothing but the source and the
// version authored, the rule writes the path and prune defaults and leaves the
// interval, targetNamespace, wait and healthChecks out, for the terminals to
// default or omit.
func TestOCIRule_DefaultsAreTheTerminals(t *testing.T) {
	comps := mustLowerOCIAlone(t, ociComponent(validOCIProps()))
	want := []oam.Component{
		{Name: "checkout", Type: "ocirepository", Properties: map[string]any{
			"url": "oci://registry.example.com/charts/checkout",
			"ref": map[string]any{"tag": "0.3.0"},
		}},
		{Name: "checkout", Type: "fluxcd-kustomization", Properties: map[string]any{
			"path":      "./",
			"prune":     true,
			"sourceRef": map[string]any{"kind": "OCIRepository", "name": "checkout"},
		}},
	}
	if !reflect.DeepEqual(comps, want) {
		t.Errorf("lowered to\n%+v\nwant\n%+v", comps, want)
	}
}

// TestOCIRule_SharedSource_EqualIntervals: two oci components on one artifact
// whose effective intervals are equal (one unset, one the 60m default written
// as 1h) share one source. It belongs to the document, not to the component
// that comes first: it is named <document>-source-<digest>, emitted once, by
// whichever component is lowered first, with no annotation or trait of either,
// and each component lowers to its own fluxcd-kustomization referencing it.
func TestOCIRule_SharedSource_EqualIntervals(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	protection := oam.Trait{Type: "prune-protection", Properties: map[string]any{}}
	app := ociDocument(
		oam.Component{Name: "base", Type: "oci", Properties: withProp(ociProps(url, "1.4.0", ""), "path", "./base"),
			Traits: []oam.Trait{protection}, Annotations: map[string]string{"gokure.dev/tier": "infra"}},
		oam.Component{Name: "addons", Type: "oci", Properties: withProp(ociProps(url, "1.4.0", "1h"), "path", "./addons")},
		// Another artifact: it shares nothing.
		oam.Component{Name: "other", Type: "oci", Properties: ociProps("oci://registry.example.com/org/other", "1.4.0", "")},
	)
	shared := helmSourceName("shop", ociSourceIdentity(url, "1.4.0", "1h0m0s"))

	for _, order := range [][2]string{{"base", "addons"}, {"addons", "base"}} {
		t.Run(order[0]+" first", func(t *testing.T) {
			lctx := ociLowering("shop")
			first := lowerOCI(t, lctx, app, order[0])
			second := lowerOCI(t, lctx, app, order[1])

			if len(first) != 2 || first[0].Type != "ocirepository" || first[1].Type != "fluxcd-kustomization" {
				t.Fatalf("the first consumer lowered to %+v, want the shared ocirepository and its fluxcd-kustomization", first)
			}
			src := first[0]
			if src.Name != shared {
				t.Errorf("shared source is named %q, want %q", src.Name, shared)
			}
			if len(src.Traits) != 0 || len(src.Annotations) != 0 {
				t.Errorf("shared source carries traits %+v and annotations %+v, want neither", src.Traits, src.Annotations)
			}
			if got := src.Properties["url"]; got != url {
				t.Errorf("shared source url = %v, want %s", got, url)
			}
			if len(second) != 1 || second[0].Type != "fluxcd-kustomization" {
				t.Fatalf("the second consumer lowered to %+v, want its fluxcd-kustomization alone", second)
			}
			for i, kz := range []oam.Component{first[1], second[0]} {
				if kz.Name != order[i] {
					t.Errorf("fluxcd-kustomization %d is named %q, want its component's name %q", i, kz.Name, order[i])
				}
				ref := kz.Properties["sourceRef"].(map[string]any)
				if ref["kind"] != "OCIRepository" || ref["name"] != shared {
					t.Errorf("%s: sourceRef = %+v, want OCIRepository/%s", kz.Name, ref, shared)
				}
			}
			base := first[1]
			if order[0] != "base" {
				base = second[0]
			}
			if !reflect.DeepEqual(base.Traits, []oam.Trait{protection}) || base.Annotations["gokure.dev/tier"] != "infra" {
				t.Errorf("base's fluxcd-kustomization carries traits %+v and annotations %+v, want the authored ones", base.Traits, base.Annotations)
			}

			// The third component, alone on its artifact, keeps the same-name pair.
			other := lowerOCI(t, lctx, app, "other")
			if len(other) != 2 || other[0].Name != "other" || other[1].Name != "other" {
				t.Errorf("a component alone on its artifact lowered to %+v, want the same-name pair", other)
			}
		})
	}
}

// TestOCIRule_SharedSource_DifferentIntervals: two oci components on one
// artifact whose intervals differ do not share. Each keeps a source of its own,
// named after it and polling at its own interval, so neither component's
// interval is silently replaced by the other's.
func TestOCIRule_SharedSource_DifferentIntervals(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	app := ociDocument(
		oam.Component{Name: "base", Type: "oci", Properties: ociProps(url, "1.4.0", "5m")},
		oam.Component{Name: "addons", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
	)
	lctx := ociLowering("shop")
	for name, interval := range map[string]any{"base": "5m", "addons": nil} {
		comps := lowerOCI(t, lctx, app, name)
		if len(comps) != 2 || comps[0].Type != "ocirepository" || comps[1].Type != "fluxcd-kustomization" {
			t.Fatalf("%s lowered to %+v, want its own ocirepository and fluxcd-kustomization", name, comps)
		}
		if comps[0].Name != name || comps[1].Name != name {
			t.Errorf("%s: members are named %q and %q, want both named after the component", name, comps[0].Name, comps[1].Name)
		}
		if got := comps[0].Properties["interval"]; got != interval {
			t.Errorf("%s: source interval = %v, want %v", name, got, interval)
		}
		if ref := comps[1].Properties["sourceRef"].(map[string]any); ref["name"] != name {
			t.Errorf("%s: sourceRef.name = %v, want the component's own source", name, ref["name"])
		}
	}
}

// TestOCIRule_SharingIsPerIdentity: of three components on one url, the two
// with the same version and interval share and the one on another version
// keeps its own source. A component whose properties the rule refuses is no
// consumer: it does not make a lone valid component share.
func TestOCIRule_SharingIsPerIdentity(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	app := ociDocument(
		oam.Component{Name: "a", Type: "oci", Properties: ociProps(url, "1.4.0", "10m")},
		oam.Component{Name: "b", Type: "oci", Properties: ociProps(url, "1.4.0", "600s")},
		oam.Component{Name: "c", Type: "oci", Properties: ociProps(url, "1.5.0", "10m")},
	)
	lctx := ociLowering("shop")
	shared := helmSourceName("shop", ociSourceIdentity(url, "1.4.0", "10m0s"))
	if a := lowerOCI(t, lctx, app, "a"); len(a) != 2 || a[0].Name != shared {
		t.Errorf("a lowered to %+v, want the shared source %s and its fluxcd-kustomization", a, shared)
	}
	if b := lowerOCI(t, lctx, app, "b"); len(b) != 1 || b[0].Properties["sourceRef"].(map[string]any)["name"] != shared {
		t.Errorf("b lowered to %+v, want its fluxcd-kustomization on %s", b, shared)
	}
	if c := lowerOCI(t, lctx, app, "c"); len(c) != 2 || c[0].Name != "c" {
		t.Errorf("c lowered to %+v, want its own source", c)
	}

	invalid := ociDocument(
		oam.Component{Name: "a", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
		oam.Component{Name: "broken", Type: "oci", Properties: withProp(ociProps(url, "1.4.0", ""), "prune", "yes")},
		// A helm component on the same artifact is no oci consumer either.
		oam.Component{Name: "chart", Type: "helm", Properties: map[string]any{"chart": url, "version": "1.4.0"}},
	)
	if a := lowerOCI(t, ociLowering("shop"), invalid, "a"); len(a) != 2 || a[0].Name != "a" {
		t.Errorf("a lowered to %+v, want its own source: neither a refused oci component nor a helm component shares it", a)
	}
}

// TestOCIRule_SourceNameIsPerDocument: two documents with a shared artifact
// each get a source of their own.
func TestOCIRule_SourceNameIsPerDocument(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	app := ociDocument(
		oam.Component{Name: "a", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
		oam.Component{Name: "b", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
	)
	shop := lowerOCI(t, ociLowering("shop"), app, "a")[0].Name
	blog := lowerOCI(t, ociLowering("blog"), app, "a")[0].Name
	if !strings.HasPrefix(shop, "shop-source-") || !strings.HasPrefix(blog, "blog-source-") {
		t.Errorf("shared sources are named %q and %q, want each prefixed with its document", shop, blog)
	}
}

func TestOCIRule_Validation(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"no source", map[string]any{"version": "0.3.0"}, "oci: source is required"},
		{"source without url", map[string]any{"source": map[string]any{}, "version": "0.3.0"}, "oci: source.url is required"},
		{"non-oci url", map[string]any{"source": map[string]any{"url": "https://example.com/x"}, "version": "0.3.0"}, "oci: source.url must use the oci:// scheme"},
		{"no version", map[string]any{"source": map[string]any{"url": "oci://registry.example.com/x"}}, "oci: version is required (a tag, or sha256:<digest>)"},
		{"invalid interval", map[string]any{
			"source": map[string]any{"url": "oci://registry.example.com/x"}, "version": "0.3.0", "interval": "5minutes",
		}, "oci: interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := components.OCIRule{}.LowerComponent(ociComponent(tc.props), oam.LoweringContext{})
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestOCIRule_Generate_Tag(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects (OCIRepository + Kustomization), got %d", len(objs))
	}

	repo, ok := (*objs[0]).(*sourcev1.OCIRepository)
	if !ok {
		t.Fatalf("objects[0]: expected *sourcev1.OCIRepository, got %T", *objs[0])
	}
	if repo.Name != "checkout" || repo.Namespace != "checkout" {
		t.Errorf("OCIRepository is %s/%s, want checkout/checkout", repo.Namespace, repo.Name)
	}
	if repo.Spec.URL != "oci://registry.example.com/charts/checkout" {
		t.Errorf("OCIRepository.Spec.URL = %q", repo.Spec.URL)
	}
	if repo.Spec.Reference == nil || repo.Spec.Reference.Tag != "0.3.0" {
		t.Errorf("OCIRepository ref = %+v, want tag 0.3.0", repo.Spec.Reference)
	}
	if repo.Spec.Reference != nil && repo.Spec.Reference.Digest != "" {
		t.Errorf("OCIRepository digest = %q, want empty for a tag", repo.Spec.Reference.Digest)
	}

	kz, ok := (*objs[1]).(*kustv1.Kustomization)
	if !ok {
		t.Fatalf("objects[1]: expected *kustv1.Kustomization, got %T", *objs[1])
	}
	if kz.Name != "checkout" || kz.Namespace != "checkout" {
		t.Errorf("Kustomization is %s/%s, want checkout/checkout", kz.Namespace, kz.Name)
	}
	if kz.Spec.Path != "./" {
		t.Errorf("Kustomization.Spec.Path = %q, want ./", kz.Spec.Path)
	}
	if !kz.Spec.Prune {
		t.Error("Kustomization.Spec.Prune = false, want true (default)")
	}
	if kz.Spec.SourceRef.Kind != "OCIRepository" || kz.Spec.SourceRef.Name != "checkout" {
		t.Errorf("Kustomization sourceRef = %+v, want OCIRepository/checkout", kz.Spec.SourceRef)
	}
}

func TestOCIRule_Generate_Digest(t *testing.T) {
	props := validOCIProps()
	props["version"] = "sha256:abc123def4567890"
	cfg := mustOCIConfig(t, props)
	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	repo := (*objs[0]).(*sourcev1.OCIRepository)
	if repo.Spec.Reference == nil || repo.Spec.Reference.Digest != "sha256:abc123def4567890" {
		t.Errorf("OCIRepository ref = %+v, want digest", repo.Spec.Reference)
	}
	if repo.Spec.Reference.Tag != "" {
		t.Errorf("OCIRepository tag = %q, want empty for a digest", repo.Spec.Reference.Tag)
	}
}

func TestOCIRule_DefaultInterval(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	objs, _ := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	repo := (*objs[0]).(*sourcev1.OCIRepository)
	if repo.Spec.Interval.Duration.String() != "1h0m0s" {
		t.Errorf("OCIRepository interval = %q, want 1h0m0s (default)", repo.Spec.Interval.Duration.String())
	}
	kz := (*objs[1]).(*kustv1.Kustomization)
	if kz.Spec.Interval.Duration.String() != "1h0m0s" {
		t.Errorf("Kustomization interval = %q, want 1h0m0s (default)", kz.Spec.Interval.Duration.String())
	}
}

// TestOCIRule_IntervalFluxDuration: interval must be a duration the Flux
// CRDs accept, not merely one time.ParseDuration accepts. A signed value or a
// sub-millisecond unit used to build cleanly and fail at apply time, and so did
// 0.5ms, which matches Flux's pattern but is emitted as 500µs. A zero interval
// is one the terminals read as unset, so both objects take the 60m default.
func TestOCIRule_IntervalFluxDuration(t *testing.T) {
	refused := []struct {
		interval string
		wantSub  string
	}{
		{"-5m", "must be a Flux duration"},
		{"500us", "must be a Flux duration"},
		{"1µs", "must be a Flux duration"},
		{"0.5ms", `emitted as "500µs"`},
		// Positive, but truncated to zero by time.ParseDuration.
		{"0.0000000001ms", `emitted as "0s"`},
	}
	for _, tc := range refused {
		t.Run("refused "+tc.interval, func(t *testing.T) {
			props := validOCIProps()
			props["interval"] = tc.interval
			_, err := ociViaRule{}.ToApplicationConfig(ociComponent(props), "checkout")
			if err == nil {
				t.Fatalf("interval %q: expected error, got nil", tc.interval)
			}
			if !strings.Contains(err.Error(), tc.wantSub) || !strings.Contains(err.Error(), "oci: interval") {
				t.Errorf("interval %q: error = %q, want it to name oci: interval and contain %q", tc.interval, err, tc.wantSub)
			}
		})
	}
	for _, tc := range []struct{ interval, emitted string }{
		{"10m", "10m0s"},
		{"1h30m", "1h30m0s"},
		{"1.5h", "1h30m0s"},
		{"0s", "1h0m0s"},
	} {
		t.Run("accepted "+tc.interval, func(t *testing.T) {
			props := validOCIProps()
			props["interval"] = tc.interval
			cfg := mustOCIConfig(t, props)
			objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			repo := (*objs[0]).(*sourcev1.OCIRepository)
			kz := (*objs[1]).(*kustv1.Kustomization)
			if got := repo.Spec.Interval.Duration.String(); got != tc.emitted {
				t.Errorf("OCIRepository interval = %q, want %q", got, tc.emitted)
			}
			if got := kz.Spec.Interval.Duration.String(); got != tc.emitted {
				t.Errorf("Kustomization interval = %q, want %q", got, tc.emitted)
			}
		})
	}
}

func TestOCIRule_PathPruneTargetNamespace_Override(t *testing.T) {
	props := validOCIProps()
	props["path"] = "./deploy"
	props["prune"] = false
	props["targetNamespace"] = "checkout-workload"
	cfg := mustOCIConfig(t, props)
	objs, _ := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	kz := (*objs[1]).(*kustv1.Kustomization)
	if kz.Spec.Path != "./deploy" {
		t.Errorf("path = %q, want ./deploy", kz.Spec.Path)
	}
	if kz.Spec.Prune {
		t.Error("prune = true, want false (overridden)")
	}
	if kz.Spec.TargetNamespace != "checkout-workload" {
		t.Errorf("targetNamespace = %q, want checkout-workload", kz.Spec.TargetNamespace)
	}
}

// TestOCIRule_TargetNamespaceNeverDefaulted: unset, the Kustomization carries
// no targetNamespace, in the application namespace and under a Flux namespace
// alike: a default would override the namespace of every object the artifact
// holds (go-kure/launcher#622).
func TestOCIRule_TargetNamespaceNeverDefaulted(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	for _, fluxNS := range []string{"", "flux-system"} {
		if fluxNS != "" {
			cfg.(interface{ SetFluxNamespace(string) }).SetFluxNamespace(fluxNS)
		}
		objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if got := (*objs[1]).(*kustv1.Kustomization).Spec.TargetNamespace; got != "" {
			t.Errorf("flux namespace %q: targetNamespace = %q, want none", fluxNS, got)
		}
	}
}

func TestOCIRule_SetFluxNamespace(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	setter, ok := cfg.(interface{ SetFluxNamespace(string) })
	if !ok {
		t.Fatal("the config does not implement SetFluxNamespace")
	}
	setter.SetFluxNamespace("flux-system")

	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, objPtr := range objs {
		obj := *objPtr
		if ns := obj.GetNamespace(); ns != "flux-system" {
			t.Errorf("%T.Namespace = %q, want flux-system", obj, ns)
		}
	}
}

func TestOCIRule_ApplyPolicy_RegistryAllowlist(t *testing.T) {
	deny := mustOCIConfig(t, validOCIProps()).(oam.Enforceable)
	if err := deny.ApplyPolicy(fakeOCIPolicy{allowed: []string{"trusted.example.com"}}); err == nil {
		t.Error("want host denial: registry.example.com not in allowlist")
	}

	ok := mustOCIConfig(t, validOCIProps()).(oam.Enforceable)
	if err := ok.ApplyPolicy(fakeOCIPolicy{allowed: []string{"registry.example.com"}}); err != nil {
		t.Errorf("allowed registry rejected: %v", err)
	}

	if err := mustOCIConfig(t, validOCIProps()).(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy must be a no-op, got %v", err)
	}
}

// TestOCIRule_ApplyPolicy_ExplicitRegistry pins go-kure/launcher#580: under
// a non-empty allowlist the url must name its registry the way Flux's
// source-controller (go-containerregistry name.NewRepository) reads it —
// oci://<registry>/<repository> with a registry that is localhost or contains
// "." or ":". Anything else is a Docker Hub repository, so matching its first
// segment against the allowlist would authorize Docker Hub. The check is the
// ocirepository terminal's, which the oci component's url reaches unchanged.
func TestOCIRule_ApplyPolicy_ExplicitRegistry(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		allowed []string
		wantErr string // substring; empty means accepted
	}{
		{"bare dotted host resolves to Docker Hub", "oci://ghcr.io", []string{"ghcr.io"}, "does not name its registry explicitly"},
		{"empty repository after registry", "oci://ghcr.io/", []string{"ghcr.io"}, "does not name its registry explicitly"},
		{"undotted first segment is a Docker Hub namespace", "oci://registry/my-artifact", []string{"registry"}, "does not name its registry explicitly"},
		{"explicit registry listed", "oci://registry.example.com/x", []string{"registry.example.com"}, ""},
		{"explicit registry not listed", "oci://registry.example.com/x", []string{"other.example"}, "not in allowed registries"},
		{"host:port registry listed", "oci://registry.example:5000/org/app", []string{"registry.example:5000"}, ""},
		{"host:port registry not listed", "oci://registry.example:5000/org/app", []string{"other.example"}, "not in allowed registries"},
		{"localhost registry listed", "oci://localhost/app", []string{"localhost"}, ""},
		{"localhost registry not listed", "oci://localhost/app", []string{"ghcr.io"}, "not in allowed registries"},
		{"bare localhost has no repository", "oci://localhost", []string{"localhost"}, "does not name its registry explicitly"},
		{"empty allowlist permits a Docker Hub url", "oci://ghcr.io", nil, ""},
		{"empty non-nil allowlist permits a Docker Hub url", "oci://registry/my-artifact", []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := validOCIProps()
			props["source"] = map[string]any{"url": tc.url}
			err := mustOCIConfig(t, props).(oam.Enforceable).ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("ApplyPolicy(%q, %v): unexpected error %v", tc.url, tc.allowed, err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("ApplyPolicy(%q, %v): want error containing %q, got nil", tc.url, tc.allowed, tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("ApplyPolicy(%q, %v): error %q, want it to contain %q", tc.url, tc.allowed, err, tc.wantErr)
			}
		})
	}

	// A nil policy accepts a url that names no registry.
	props := validOCIProps()
	props["source"] = map[string]any{"url": "oci://ghcr.io"}
	if err := mustOCIConfig(t, props).(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy must be a no-op, got %v", err)
	}

	// The refusal names the terminal's field and the form to write, but not the
	// value, whose first segment can be userinfo carrying a credential.
	props["source"] = map[string]any{"url": "oci://ghcr.io"}
	err := mustOCIConfig(t, props).(oam.Enforceable).ApplyPolicy(fakeOCIPolicy{allowed: []string{"ghcr.io"}})
	if err == nil {
		t.Fatal("want refusal for oci://ghcr.io under [ghcr.io]")
	}
	for _, want := range []string{"ocirepository: url: ", "Docker Hub", "oci://<registry>/<repository>", "localhost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), `"oci://ghcr.io"`) {
		t.Errorf("refusal %q quotes the url", err)
	}
}
