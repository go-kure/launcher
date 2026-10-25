package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The registry access keys of an oci component's source (go-kure/launcher#999)
// are passed to the generated OCIRepository as written, joined to the source
// identity after the interval, and left out of both when unset.

// withSource is props with key set to value in its source.
func withSource(props map[string]any, key string, value any) map[string]any {
	src := maps.Clone(props["source"].(map[string]any))
	src[key] = value
	return withProp(props, "source", src)
}

func generateOCI(t *testing.T, props map[string]any) []any {
	t.Helper()
	cfg := mustOCIConfig(t, props)
	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := make([]any, len(objs))
	for i, obj := range objs {
		out[i] = *obj
	}
	return out
}

// Each access key reaches the OCIRepository; the Kustomization is unchanged.
func TestOCIRule_SourceAccess_Rendered(t *testing.T) {
	props := validOCIProps()
	props = withSource(props, "secretRef", map[string]any{"name": "regcred"})
	props = withSource(props, "provider", "aws")
	props = withSource(props, "insecure", true)
	props = withSource(props, "certSecretRef", map[string]any{"name": "registry-ca"})
	props = withSource(props, "proxySecretRef", map[string]any{"name": "registry-proxy"})
	objs := generateOCI(t, props)
	repo := objs[0].(*sourcev1.OCIRepository)
	if repo.Spec.SecretRef == nil || repo.Spec.SecretRef.Name != "regcred" {
		t.Errorf("spec.secretRef = %+v, want regcred", repo.Spec.SecretRef)
	}
	if repo.Spec.Provider != "aws" {
		t.Errorf("spec.provider = %q, want aws", repo.Spec.Provider)
	}
	if !repo.Spec.Insecure {
		t.Error("spec.insecure = false, want true")
	}
	if repo.Spec.CertSecretRef == nil || repo.Spec.CertSecretRef.Name != "registry-ca" {
		t.Errorf("spec.certSecretRef = %+v, want registry-ca", repo.Spec.CertSecretRef)
	}
	if repo.Spec.ProxySecretRef == nil || repo.Spec.ProxySecretRef.Name != "registry-proxy" {
		t.Errorf("spec.proxySecretRef = %+v, want registry-proxy", repo.Spec.ProxySecretRef)
	}
	if base := generateOCI(t, validOCIProps()); !reflect.DeepEqual(objs[1], base[1]) {
		t.Errorf("Kustomization = %+v\nwant the one without access %+v", objs[1], base[1])
	}
}

// Unset, false, empty or null access renders what a document without the keys
// does.
func TestOCIRule_SourceAccess_UnsetIsUnchanged(t *testing.T) {
	base := generateOCI(t, validOCIProps())
	unset := map[string]any{"insecure": false, "provider": "", "secretRef": nil, "certSecretRef": nil, "proxySecretRef": nil}
	all := validOCIProps()
	for key, value := range unset {
		all = withSource(all, key, value)
		if got := generateOCI(t, withSource(validOCIProps(), key, value)); !reflect.DeepEqual(got, base) {
			t.Errorf("%s %v: rendered %+v\nwant %+v", key, value, got, base)
		}
	}
	if got := generateOCI(t, all); !reflect.DeepEqual(got, base) {
		t.Errorf("all unset: rendered %+v\nwant %+v", got, base)
	}
}

// A reference's shape is the terminal's strict decode; a wrongly typed key is
// refused by its type, and a key source does not declare by schema validation.
func TestOCIRule_SourceAccess_AuthoredPipeline(t *testing.T) {
	build := func(props map[string]any) error {
		tr := oam.NewTransformer(nil, nil)
		tr.RegisterComponentLowering(components.OCIRule{})
		comp := oam.Component{Name: "checkout", Type: "oci", Properties: props}
		app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}}
		if err := tr.ValidateAuthoredProperties(app); err != nil {
			return err
		}
		_, err := ociViaRule{}.ToApplicationConfig(&comp, "checkout")
		return err
	}
	valid := validOCIProps()
	for key, value := range map[string]any{
		"secretRef": map[string]any{"name": "regcred"}, "provider": "aws", "insecure": true,
		"certSecretRef": map[string]any{"name": "registry-ca"}, "proxySecretRef": map[string]any{"name": "registry-proxy"},
	} {
		valid = withSource(valid, key, value)
	}
	if err := build(valid); err != nil {
		t.Errorf("valid access refused: %v", err)
	}
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"a key the reference does not declare", withSource(validOCIProps(), "secretRef", map[string]any{"name": "x", "namespace": "y"}),
			`ocirepository: properties do not decode as an OCIRepositorySpec: json: unknown field "namespace"`},
		{"insecure not a boolean", withSource(validOCIProps(), "insecure", "yes"), "insecure"},
		{"secretRef not an object", withSource(validOCIProps(), "secretRef", "x"), "secretRef"},
		{"a key source does not declare", withSource(validOCIProps(), "other", 1), `unsupported field "other"`},
	} {
		err := build(tc.props)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
	// Past schema validation, the rule names a wrongly typed key itself.
	for key, tc := range map[string]struct {
		value any
		want  string
	}{
		"insecure":  {"yes", "oci: source.insecure: must be a boolean, got string"},
		"secretRef": {"x", "oci: source.secretRef: must be an object, got string"},
		"provider":  {7, "oci: source.provider"},
	} {
		_, err := ociViaRule{}.ToApplicationConfig(ociComponent(withSource(validOCIProps(), key, tc.value)), "checkout")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", key, err, tc.want)
		}
	}
}

// ociAccessCase varies one access key between two components on one
// url, version and interval, the other keys unset.
type ociAccessCase struct {
	name       string
	key        string
	a, b       any  // the values written, null included
	absentB    bool // b leaves the key out
	share      bool
	identity   string // the access member of the shared identity, "" for none
	sharedProp any    // the key's value on the shared source; nil for none
}

func ociAccessCases() []ociAccessCase {
	var cases []ociAccessCase
	for _, key := range []string{"secretRef", "certSecretRef", "proxySecretRef"} {
		ref := map[string]any{"name": "r"}
		cases = append(cases,
			ociAccessCase{name: key + " equal", key: key, a: ref, b: map[string]any{"name": "r"}, share: true,
				identity: `"` + key + `":{"name":"r"}`, sharedProp: ref},
			ociAccessCase{name: key + " different", key: key, a: ref, b: map[string]any{"name": "s"}},
			ociAccessCase{name: key + " set vs absent", key: key, a: ref, absentB: true},
			ociAccessCase{name: key + " null vs absent", key: key, a: nil, absentB: true, share: true},
			ociAccessCase{name: key + " {} vs absent", key: key, a: map[string]any{}, absentB: true},
		)
	}
	return append(cases,
		ociAccessCase{name: "provider equal", key: "provider", a: "aws", b: "aws", share: true, identity: `"provider":"aws"`, sharedProp: "aws"},
		ociAccessCase{name: "provider aws vs absent", key: "provider", a: "aws", absentB: true},
		ociAccessCase{name: "provider generic vs absent", key: "provider", a: "generic", absentB: true},
		ociAccessCase{name: "provider empty vs absent", key: "provider", a: "", absentB: true, share: true},
		ociAccessCase{name: "insecure true vs true", key: "insecure", a: true, b: true, share: true, identity: `"insecure":true`, sharedProp: true},
		ociAccessCase{name: "insecure true vs absent", key: "insecure", a: true, absentB: true},
		ociAccessCase{name: "insecure false vs absent", key: "insecure", a: false, absentB: true, share: true},
	)
}

func (c ociAccessCase) props(url string, value any, absent bool) map[string]any {
	props := ociProps(url, "1.4.0", "")
	if absent {
		return props
	}
	return withSource(props, c.key, value)
}

// Unnamed components share a source only when every access key is equal: each
// key is varied alone. Both unset keep today's shared name.
func TestOCIRule_SourceAccess_SharingPerKey(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	for _, tc := range ociAccessCases() {
		t.Run(tc.name, func(t *testing.T) {
			app := ociDocument(
				oam.Component{Name: "a", Type: "oci", Properties: tc.props(url, tc.a, false)},
				oam.Component{Name: "b", Type: "oci", Properties: tc.props(url, tc.b, tc.absentB)},
			)
			lctx := ociLowering("shop")
			a := lowerOCI(t, lctx, app, "a")
			b := lowerOCI(t, lctx, app, "b")
			if !tc.share {
				for name, comps := range map[string][]oam.Component{"a": a, "b": b} {
					if len(comps) != 2 || comps[0].Name != name || comps[1].Name != name {
						t.Errorf("%s lowered to %+v, want its own same-name pair", name, comps)
					}
				}
				return
			}
			var access []string
			if tc.identity != "" {
				access = append(access, tc.identity)
			}
			shared := helmSourceName("shop", ociSourceIdentity(url, "1.4.0", "1h0m0s", access...))
			if len(a) != 2 || a[0].Name != shared {
				t.Fatalf("a lowered to %+v, want the shared source %s and its fluxcd-kustomization", a, shared)
			}
			if len(b) != 1 || b[0].Properties["sourceRef"].(map[string]any)["name"] != shared {
				t.Errorf("b lowered to %+v, want its fluxcd-kustomization on %s", b, shared)
			}
			if got := a[0].Properties[tc.key]; !reflect.DeepEqual(got, tc.sharedProp) {
				t.Errorf("shared source %s = %#v, want %#v", tc.key, got, tc.sharedProp)
			}
		})
	}
}

// One source.name with access that differs in a single key is one name for two
// identities, refused as a collision; with equal access the components share.
func TestOCIRule_SourceAccess_SourceName(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	lowerErr := func(lctx oam.LoweringContext, app *oam.Application, i int) ([]oam.Component, error) {
		lctx.Document = app
		lctx.Origin.Component, lctx.Origin.ComponentType = app.Spec.Components[i].Name, "oci"
		res, err := components.OCIRule{}.LowerComponent(&app.Spec.Components[i], lctx)
		if err != nil {
			return nil, err
		}
		return res.Components, nil
	}
	for _, tc := range ociAccessCases() {
		t.Run(tc.name, func(t *testing.T) {
			app := ociDocument(
				oam.Component{Name: "a", Type: "oci", Properties: withSource(tc.props(url, tc.a, false), "name", "platform")},
				oam.Component{Name: "b", Type: "oci", Properties: withSource(tc.props(url, tc.b, tc.absentB), "name", "platform")},
			)
			lctx := ociLowering("shop")
			if _, err := lowerErr(lctx, app, 0); err != nil {
				t.Fatalf("a: %v", err)
			}
			b, err := lowerErr(lctx, app, 1)
			if !tc.share {
				const want = `oci: naming the generated source: lowering: generated name "platform" collides`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("b: err = %v\nwant one containing %s", err, want)
				}
				return
			}
			if err != nil || len(b) != 1 || b[0].Properties["sourceRef"].(map[string]any)["name"] != "platform" {
				t.Errorf("b lowered to %+v (err %v), want its fluxcd-kustomization on platform", b, err)
			}
		})
	}
}

// A component that writes source.objectName keeps its own source, which
// carries its access.
func TestOCIRule_SourceAccess_ObjectName(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	access := withSource(ociProps(url, "1.4.0", ""), "objectName", "kept")
	for _, key := range []string{"secretRef", "certSecretRef", "proxySecretRef"} {
		access = withSource(access, key, map[string]any{"name": "regcred"})
	}
	app := ociDocument(
		oam.Component{Name: "a", Type: "oci", Properties: access},
		oam.Component{Name: "b", Type: "oci", Properties: withSource(ociProps(url, "1.4.0", ""), "secretRef", map[string]any{"name": "regcred"})},
	)
	lctx := ociLowering("shop")
	a := lowerOCI(t, lctx, app, "a")
	if len(a) != 2 || a[0].Type != "ocirepository" || a[0].Name != "a" {
		t.Fatalf("a lowered to %+v, want its own source", a)
	}
	for _, key := range []string{"secretRef", "certSecretRef", "proxySecretRef"} {
		if got := a[0].Properties[key]; !reflect.DeepEqual(got, map[string]any{"name": "regcred"}) {
			t.Errorf("a's source %s = %#v, want regcred", key, got)
		}
	}
}

// The lowered source holds a copy of an authored reference, not the map itself.
func TestOCIRule_SourceAccess_ReferenceIsCopied(t *testing.T) {
	ref := map[string]any{"name": "regcred"}
	comp := ociComponent(withSource(validOCIProps(), "secretRef", ref))
	res, err := components.OCIRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	res.Components[0].Properties["secretRef"].(map[string]any)["name"] = "changed"
	if ref["name"] != "regcred" {
		t.Error("the lowered source shares the authored secretRef map")
	}
}
