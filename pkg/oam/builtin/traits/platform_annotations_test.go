package traits_test

import (
	stderrors "errors"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"

	pkgerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// These tests pin how the annotations the platform sets on an Ingress stay apart
// from the authored ones (go-kure/launcher#790): the expose rule writes its own
// on the ingress trait's platform-reserved platformAnnotations property, and the
// check of the consumer's reserved metadata keys passes exactly those pairs.

const (
	kClusterIssuer    = "cert-manager.io/cluster-issuer"
	kSSLRedirect      = "nginx.ingress.kubernetes.io/ssl-redirect"
	kForceSSLRedirect = "nginx.ingress.kubernetes.io/force-ssl-redirect"
)

// exposeCapability is an expose capability rendering that offers everything the
// rule writes an annotation from.
func exposeCapability() map[string]oam.CapabilityBinding {
	return map[string]oam.CapabilityBinding{"expose": {Rendering: map[string]any{
		"controllerType":           "ingress",
		"ingressClassName":         "nginx",
		"certManagerClusterIssuer": "letsencrypt-prod",
		"authURL":                  "http://oauth2-proxy.oauth2-proxy.svc.cluster.local:4180/oauth2/auth",
		"authSigninURL":            "https://auth-proxy.example.net/oauth2/start",
		"authResponseHeaders":      "X-Auth-Request-User",
	}}}
}

func platformAnnotationsTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil, nil)
	registerWebservice(tr)
	tr.RegisterBuiltinTraitLowering(traits.ExposeRule{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
	return tr
}

func webWithTrait(trait oam.Trait) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name:       "web",
			Type:       "webservice",
			Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
			Traits:     []oam.Trait{trait},
		}}},
	}
}

// exposeEveryAnnotation is an expose trait whose properties make the rule write
// all six of its annotations; annotations are the authored ones beside them.
func exposeEveryAnnotation(annotations map[string]any) oam.Trait {
	props := map[string]any{
		"hostnames":        []any{"shop.example.com"},
		"sslRedirect":      true,
		"forceSslRedirect": false,
		"allowedGroups":    []any{"admins"},
	}
	if annotations != nil {
		props["annotations"] = annotations
	}
	return oam.Trait{Type: "expose", Properties: props}
}

// generatedIngress transforms app under ctx, generates it, and returns the one
// Ingress, or the first error.
func generatedIngress(t *testing.T, app *oam.Application, ctx oam.TransformContext) (*networkingv1.Ingress, error) {
	t.Helper()
	cluster, err := platformAnnotationsTransformer().Transform(app, ctx)
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		for _, p := range a.Objects {
			if ing, ok := (*p).(*networkingv1.Ingress); ok {
				return ing, nil
			}
		}
	}
	t.Fatal("no Ingress generated")
	return nil, nil
}

var exposeWritten = map[string]string{
	kClusterIssuer:    "letsencrypt-prod",
	kSSLRedirect:      "true",
	kForceSSLRedirect: "false",
	kAuthURL:          "http://oauth2-proxy.oauth2-proxy.svc.cluster.local:4180/oauth2/auth?allowed_groups=admins",
	kAuthSignin:       "https://auth-proxy.example.net/oauth2/start",
	kAuthRespHdrs:     "X-Auth-Request-User",
}

// reservedIngressPrefixes reserves the two prefixes the expose rule writes
// under, as a consumer that keeps ingress behaviour to its platform would.
var reservedIngressPrefixes = []string{"nginx.ingress.kubernetes.io/", "cert-manager.io/"}

// TestExposeRule_WritesPlatformAnnotations: the rule's six annotations reach the
// ingress trait as platformAnnotations, the authored ones as annotations, and
// the authored map is left as written.
func TestExposeRule_WritesPlatformAnnotations(t *testing.T) {
	authored := map[string]any{"example.com/owner": "team-a"}
	trait := exposeEveryAnnotation(authored)
	maps.Copy(trait.Properties, exposeCapability()["expose"].Rendering)
	result, err := (traits.ExposeRule{}).LowerTrait(&trait, oam.LoweringContext{Component: &oam.Component{Name: "web"}})
	if err != nil {
		t.Fatalf("LowerTrait: %v", err)
	}
	emitted := result.Traits[0]
	platform, ok := emitted.Properties["platformAnnotations"].(map[string]any)
	if !ok {
		t.Fatalf("platformAnnotations = %#v, want the rule's annotations", emitted.Properties["platformAnnotations"])
	}
	if len(platform) != len(exposeWritten) {
		t.Errorf("platformAnnotations = %v, want exactly %v", platform, exposeWritten)
	}
	for k, want := range exposeWritten {
		if got := platform[k]; got != want {
			t.Errorf("platformAnnotations[%s] = %v, want %q", k, got, want)
		}
	}
	if got := emitted.Properties["annotations"]; len(authored) != 1 || len(got.(map[string]any)) != 1 {
		t.Errorf("authored annotations = %v (emitted %v), want them as written", authored, got)
	}

	// A trait that makes the rule write none carries no such property.
	plain := oam.Trait{Type: "expose", Properties: map[string]any{
		"controllerType": "ingress", "ingressClassName": "nginx", "hostnames": []any{"shop.example.com"},
	}}
	result, err = (traits.ExposeRule{}).LowerTrait(&plain, oam.LoweringContext{Component: &oam.Component{Name: "web"}})
	if err != nil {
		t.Fatalf("LowerTrait: %v", err)
	}
	if _, has := result.Traits[0].Properties["platformAnnotations"]; has {
		t.Error("an expose trait that writes no annotation emits platformAnnotations")
	}
}

// TestExposeRule_PlatformAnnotationsPassReservedKeys: with both prefixes the
// rule writes under reserved, an expose trait still builds and its Ingress
// carries all six annotations, sealed or not, also beside an authored annotation
// that says the same. A hand-written annotation under a reserved prefix is
// refused, naming the component, the Ingress, the key and the prefix.
func TestExposeRule_PlatformAnnotationsPassReservedKeys(t *testing.T) {
	ctx := oam.TransformContext{Namespace: "default", Capabilities: exposeCapability(), ReservedMetadataKeys: reservedIngressPrefixes}

	for name, annotations := range map[string]map[string]any{
		"the rule's annotations alone":           nil,
		"beside an authored unreserved one":      {"example.com/owner": "team-a"},
		"beside an authored one saying the same": {kSSLRedirect: "true", kClusterIssuer: "letsencrypt-prod"},
	} {
		t.Run(name, func(t *testing.T) {
			ing, err := generatedIngress(t, webWithTrait(exposeEveryAnnotation(annotations)), ctx)
			if err != nil {
				t.Fatalf("transform and generate: %v", err)
			}
			for k, want := range exposeWritten {
				if got := ing.Annotations[k]; got != want {
					t.Errorf("annotation %s = %q, want %q", k, got, want)
				}
			}
			if want := len(exposeWritten) + len(annotations) - countShared(annotations, exposeWritten); len(ing.Annotations) != want {
				t.Errorf("annotations = %v, want %d of them", ing.Annotations, want)
			}
		})
	}

	_, err := generatedIngress(t, webWithTrait(exposeEveryAnnotation(map[string]any{"nginx.ingress.kubernetes.io/proxy-body-size": "1g"})), ctx)
	if !stderrors.Is(err, oam.ErrReservedMetadataKey) {
		t.Fatalf("a hand-written reserved annotation: %v, want ErrReservedMetadataKey", err)
	}
	for _, want := range []string{`component "web"`, `Ingress "`, `annotation "nginx.ingress.kubernetes.io/proxy-body-size"`, `the prefix "nginx.ingress.kubernetes.io/" is reserved`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %s", err, want)
		}
	}

	// Without the typed property the rule writes no ssl-redirect, so the same
	// annotation written by hand is an authored one, and refused.
	handWritten := oam.Trait{Type: "expose", Properties: map[string]any{
		"hostnames":   []any{"shop.example.com"},
		"annotations": map[string]any{kSSLRedirect: "true"},
	}}
	_, err = generatedIngress(t, webWithTrait(handWritten), ctx)
	if !stderrors.Is(err, oam.ErrReservedMetadataKey) || !strings.Contains(err.Error(), `annotation "`+kSSLRedirect+`"`) {
		t.Fatalf("a hand-written ssl-redirect: %v, want ErrReservedMetadataKey naming it", err)
	}
}

func countShared(a map[string]any, b map[string]string) int {
	n := 0
	for k := range a {
		if _, ok := b[k]; ok {
			n++
		}
	}
	return n
}

// TestExposeRule_AuthoredAnnotationAgainstThePlatform: an authored annotation of
// a key the rule writes, with another value, is refused for each of the six,
// naming the annotation and where the rule's value comes from.
func TestExposeRule_AuthoredAnnotationAgainstThePlatform(t *testing.T) {
	for key, source := range map[string]string{
		kClusterIssuer:    "certManagerClusterIssuer",
		kSSLRedirect:      "sslRedirect",
		kForceSSLRedirect: "forceSslRedirect",
		kAuthURL:          "allowedGroups",
		kAuthSignin:       "authSigninURL",
		kAuthRespHdrs:     "authResponseHeaders",
	} {
		t.Run(key, func(t *testing.T) {
			ctx := oam.TransformContext{Namespace: "default", Capabilities: exposeCapability()}
			_, err := generatedIngress(t, webWithTrait(exposeEveryAnnotation(map[string]any{key: "authored"})), ctx)
			var ve *pkgerrors.ValidationError
			if !stderrors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if ve.Field != "annotations."+key || ve.Component != "web" {
				t.Errorf("refusal names field %q of component %q, want annotations.%s of web", ve.Field, ve.Component, key)
			}
			for _, want := range []string{source, exposeWritten[key], "cannot be overridden"} {
				if !strings.Contains(ve.Message, want) {
					t.Errorf("refusal %q does not say %s", ve.Message, want)
				}
			}
		})
	}
}

// TestExposeRule_PlatformAnnotationsNotAuthorable: platformAnnotations is the
// ingress trait's property, which only the rule writes. On an expose trait it is
// refused on either rendering, not handed on.
func TestExposeRule_PlatformAnnotationsNotAuthorable(t *testing.T) {
	for _, base := range []map[string]any{
		{"controllerType": "ingress", "ingressClassName": "nginx", "hostnames": []any{"shop.example.com"}},
		{"controllerType": "gateway", "gatewayName": "public", "hostnames": []any{"shop.example.com"}},
	} {
		props := maps.Clone(base)
		props["platformAnnotations"] = map[string]any{kSSLRedirect: "false"}
		err := applyExpose(&oam.Trait{Type: "expose", Properties: props}, newWebApp("web", "default"), &stack.Bundle{})
		var ve *pkgerrors.ValidationError
		if !stderrors.As(err, &ve) || ve.Field != "platformAnnotations" || ve.Component != "web" {
			t.Errorf("controllerType %v: want *ValidationError on platformAnnotations of web, got %v", base["controllerType"], err)
		}
	}
}

func ingressTraitWith(extra map[string]any) oam.Trait {
	props := map[string]any{"rules": []any{map[string]any{
		"host":  "example.com",
		"paths": []any{map[string]any{"path": "/"}},
	}}}
	maps.Copy(props, extra)
	return oam.Trait{Type: "ingress", Properties: props}
}

// TestIngressHandler_PlatformAnnotations_InlineAuthoringRejected: an author
// cannot vouch for an annotation as the platform's, which would pass a reserved
// key through the check.
func TestIngressHandler_PlatformAnnotations_InlineAuthoringRejected(t *testing.T) {
	app := webWithTrait(ingressTraitWith(map[string]any{"platformAnnotations": map[string]any{kSSLRedirect: "false"}}))
	_, err := platformAnnotationsTransformer().Transform(app, oam.TransformContext{Namespace: "default", ReservedMetadataKeys: reservedIngressPrefixes})
	if !stderrors.Is(err, oam.ErrPlatformReserved) || !strings.Contains(err.Error(), "platformAnnotations") {
		t.Fatalf("Transform = %v, want ErrPlatformReserved naming platformAnnotations", err)
	}
	if schema := (&traits.IngressHandler{}).PropertySchema()["platformAnnotations"]; !schema.PlatformReserved {
		t.Error("the ingress trait does not declare platformAnnotations platform-reserved")
	}
	if _, declared := (traits.ExposeRule{}).PropertySchema()["platformAnnotations"]; declared {
		t.Error("the expose trait declares platformAnnotations: it is the ingress trait's alone")
	}
}

// TestIngressHandler_PlatformAnnotations_FromCapability: a rendering of the
// ingress capability sets them on a directly authored ingress trait. They land
// on the Ingress beside the authored ones and pass a reserved prefix; an
// authored annotation of the same key must hold the same value.
func TestIngressHandler_PlatformAnnotations_FromCapability(t *testing.T) {
	ctx := oam.TransformContext{
		Namespace:            "default",
		ReservedMetadataKeys: reservedIngressPrefixes,
		Capabilities: map[string]oam.CapabilityBinding{"ingress": {Rendering: map[string]any{
			"platformAnnotations": map[string]any{kSSLRedirect: "true"},
		}}},
	}

	for name, annotations := range map[string]map[string]any{
		"no authored annotation":          nil,
		"an authored unreserved one":      {"example.com/owner": "team-a"},
		"an authored one saying the same": {kSSLRedirect: "true"},
		// A null states no value: the platform's is written.
		"an authored null": {kSSLRedirect: nil},
	} {
		t.Run(name, func(t *testing.T) {
			extra := map[string]any{}
			if annotations != nil {
				extra["annotations"] = annotations
			}
			ing, err := generatedIngress(t, webWithTrait(ingressTraitWith(extra)), ctx)
			if err != nil {
				t.Fatalf("transform and generate: %v", err)
			}
			if got := ing.Annotations[kSSLRedirect]; got != "true" {
				t.Errorf("annotation %s = %q, want the platform's true", kSSLRedirect, got)
			}
			if owner, authored := annotations["example.com/owner"]; authored && ing.Annotations["example.com/owner"] != owner {
				t.Errorf("annotations = %v, want the authored one kept", ing.Annotations)
			}
		})
	}

	_, err := generatedIngress(t, webWithTrait(ingressTraitWith(map[string]any{"annotations": map[string]any{kSSLRedirect: "false"}})), ctx)
	if err == nil || !strings.Contains(err.Error(), "annotations."+kSSLRedirect) || !strings.Contains(err.Error(), "cannot override it") {
		t.Fatalf("an authored value against the platform's: %v, want it refused by key", err)
	}

	// Another key under the reserved prefix stays an authored one.
	_, err = generatedIngress(t, webWithTrait(ingressTraitWith(map[string]any{"annotations": map[string]any{kForceSSLRedirect: "true"}})), ctx)
	if !stderrors.Is(err, oam.ErrReservedMetadataKey) || !strings.Contains(err.Error(), `annotation "`+kForceSSLRedirect+`"`) {
		t.Fatalf("an authored reserved annotation: %v, want ErrReservedMetadataKey naming it", err)
	}
}

// TestIngressHandler_PlatformAnnotations_Malformed: a value the platform's input
// cannot hold is refused, not written.
func TestIngressHandler_PlatformAnnotations_Malformed(t *testing.T) {
	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"a list":                  {[]any{kSSLRedirect}, "platformAnnotations: expected object"},
		"a value that is a bool":  {map[string]any{kSSLRedirect: true}, "platformAnnotations." + kSSLRedirect + ": expected a string"},
		"a value that is nothing": {map[string]any{kSSLRedirect: nil}, "platformAnnotations." + kSSLRedirect + ": expected a string"},
	} {
		t.Run(name, func(t *testing.T) {
			trait := ingressTraitWith(map[string]any{"platformAnnotations": tc.value})
			err := (&traits.IngressHandler{}).Apply(&trait, newWebApp("web", "default"), &stack.Bundle{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Apply = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestIngressConfig_PlatformAnnotations: the config answers the platform's
// annotations in a map of its own, and none when the trait held none.
func TestIngressConfig_PlatformAnnotations(t *testing.T) {
	configOf := func(extra map[string]any) *traits.IngressConfig {
		t.Helper()
		bundle := &stack.Bundle{}
		trait := ingressTraitWith(extra)
		if err := (&traits.IngressHandler{}).Apply(&trait, newWebApp("web", "default"), bundle); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		cfg, ok := bundle.Applications[0].Config.(*traits.IngressConfig)
		if !ok {
			t.Fatalf("sub-application config = %T, want *IngressConfig", bundle.Applications[0].Config)
		}
		return cfg
	}

	if got := configOf(nil).PlatformAnnotations(); got != nil {
		t.Errorf("PlatformAnnotations = %v, want none", got)
	}

	cfg := configOf(map[string]any{
		"annotations":         map[string]any{"example.com/owner": "team-a"},
		"platformAnnotations": map[string]any{kSSLRedirect: "true"},
	})
	got := cfg.PlatformAnnotations()
	if len(got) != 1 || got[kSSLRedirect] != "true" {
		t.Fatalf("PlatformAnnotations = %v, want the platform's one", got)
	}
	got[kSSLRedirect] = "edited"
	got["example.com/owner"] = "vouched"
	if again := cfg.PlatformAnnotations(); len(again) != 1 || again[kSSLRedirect] != "true" {
		t.Errorf("PlatformAnnotations after the caller's edit = %v, want the config's own unchanged", again)
	}
	if len(cfg.Annotations) != 1 || cfg.Annotations["example.com/owner"] != "team-a" {
		t.Errorf("Annotations = %v, want the authored one alone", cfg.Annotations)
	}
}
