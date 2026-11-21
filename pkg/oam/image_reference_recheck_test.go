package oam

import (
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"
)

// imagedConfig is a consumer's config that names images and implements
// nothing else of the policy surface: no ApplyPolicy, no level awareness.
type imagedConfig struct{ refs []ImageReference }

func (c *imagedConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (c *imagedConfig) ImageReferences() []ImageReference                       { return c.refs }

// imagedHandler builds an imagedConfig for the component type widget.
type imagedHandler struct{ refs []ImageReference }

func (h *imagedHandler) CanHandle(t string) bool { return t == "widget" }
func (h *imagedHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &imagedConfig{refs: h.refs}, nil
}

// levelPolicy is a Policy that names an image reference level.
type levelPolicy struct {
	*NoopPolicy
	level ImageReferenceLevel
}

func (p levelPolicy) ImageReferenceLevel() ImageReferenceLevel { return p.level }

// TestTransform_ImageReferencesAreCheckedAtThePolicyLevel: after a handler, the
// Transformer holds the references its config names to the level of the
// policy, whatever the handler implements. A stricter level is enforced on a
// handler that knows nothing of levels; a handler that knows nothing is held
// to tagged under a policy that names no level (go-kure/launcher#1644).
func TestTransform_ImageReferencesAreCheckedAtThePolicyLevel(t *testing.T) {
	const digest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		name      string
		policy    Policy
		reference string
		wantErr   string // "" accepts
	}{
		{"no policy refuses latest", nil, "nginx:latest", `image "nginx:latest" rejected`},
		{"no policy accepts a tag", nil, "nginx:1.0", ""},
		{"NoopPolicy refuses an untagged image", &NoopPolicy{}, "nginx", `image "nginx" rejected`},
		{"a policy that names no level refuses latest", levelPolicy{NoopPolicy: &NoopPolicy{}}, "nginx:latest", `image "nginx:latest" rejected`},
		{"tagged accepts a branch tag", levelPolicy{&NoopPolicy{}, ImageLevelTagged}, "nginx:main", ""},
		{"digest refuses a tag", levelPolicy{&NoopPolicy{}, ImageLevelDigest}, "nginx:1.0", `level "digest"`},
		{"digest accepts a digest", levelPolicy{&NoopPolicy{}, ImageLevelDigest}, "nginx" + digest, ""},
		{"version refuses a branch tag", levelPolicy{&NoopPolicy{}, ImageLevelVersion}, "nginx:main", `level "version"`},
		{"version accepts a version tag", levelPolicy{&NoopPolicy{}, ImageLevelVersion}, "nginx:v1.2", ""},
		{"any accepts latest", levelPolicy{&NoopPolicy{}, ImageLevelAny}, "nginx:latest", ""},
		{"an unknown level is refused, not read as the default", levelPolicy{&NoopPolicy{}, "strict"}, "nginx:1.0", `"strict"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := []ImageReference{{Field: `containers[0] "app"`, Reference: tc.reference}}
			tr := NewTransformer(map[string]ComponentHandler{"widget": &imagedHandler{refs: refs}}, nil)
			_, err := tr.Transform(makeApp("myapp", makeComponent("web", "widget")), TransformContext{Policy: tc.policy})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want the build to pass", err)
				}
				return
			}
			if err == nil {
				t.Fatal("built, want the image refused")
			}
			v := wantViolation(t, err)
			if v.Component != "web" {
				t.Errorf("Component = %q, want %q", v.Component, "web")
			}
			for _, want := range []string{`containers[0] "app"`, tc.wantErr} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want one holding %q", err, want)
				}
			}
			var refusal *PolicyRefusal
			if errors.As(err, &refusal) {
				t.Errorf("err carries a policy refusal of class %q, want a plain error", refusal.Class)
			}
		})
	}
}

// levelHandler is a handler that reads the level off the component it is given:
// it records it, and builds a config that names a reference only a looser level
// accepts.
type levelHandler struct {
	asked *ImageReferenceLevel
}

func (h *levelHandler) CanHandle(t string) bool { return t == "widget" }
func (h *levelHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	level := c.ImageLevel()
	h.asked = &level
	return &imagedConfig{refs: []ImageReference{{Reference: "nginx:latest"}}}, CheckImageReference("nginx:latest", level)
}

// TestTransform_HandlerIsGivenThePolicyLevel: the component a handler is given
// carries the policy's level, so a handler that reads it can be looser than the
// default; the Transformer does not build it at the default level and then hold
// it to the policy's.
func TestTransform_HandlerIsGivenThePolicyLevel(t *testing.T) {
	cases := []struct {
		name    string
		policy  Policy
		want    ImageReferenceLevel
		refused bool
	}{
		{"no policy", nil, ImageLevelTagged, true},
		{"a policy that names tagged", levelPolicy{&NoopPolicy{}, ImageLevelTagged}, ImageLevelTagged, true},
		{"any", levelPolicy{&NoopPolicy{}, ImageLevelAny}, ImageLevelAny, false},
		{"digest", levelPolicy{&NoopPolicy{}, ImageLevelDigest}, ImageLevelDigest, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &levelHandler{}
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
			_, err := tr.Transform(makeApp("myapp", makeComponent("web", "widget")), TransformContext{Policy: tc.policy})
			if h.asked == nil || *h.asked != tc.want {
				t.Errorf("asked at %v, want %q", h.asked, tc.want)
			}
			if (err != nil) != tc.refused {
				t.Errorf("err = %v, want refused = %v", err, tc.refused)
			}
		})
	}
}

// wrapperHandler is a type a consumer writes around a handler: it embeds it and
// overrides ToApplicationConfig, here to count its own runs. copies says whether
// it hands the embedded handler a copy of the component it was given, which keeps
// the level, or a fresh component of the same name and properties, which does not.
type wrapperHandler struct {
	*levelHandler
	copies bool
	ran    *int
}

func (w wrapperHandler) ToApplicationConfig(c *Component, ns string) (stack.ApplicationConfig, error) {
	*w.ran++
	inner := *c
	if !w.copies {
		inner = Component{Name: c.Name, Type: c.Type, Properties: c.Properties}
	}
	return w.levelHandler.ToApplicationConfig(&inner, ns)
}

// TestTransform_EmbeddingWrapperRunsItsOverrideAtEveryLevel: a type that embeds a
// handler and overrides ToApplicationConfig runs its override whatever the
// policy names, since nothing is promoted that could bypass it, and the level
// reaches the embedded handler when the wrapper copies the component it was
// given. A wrapper that builds a fresh component reads the default level: it is
// held to the default under `any`, which fails closed, and the check of the image
// references holds it under a stricter level (go-kure/launcher#1644).
func TestTransform_EmbeddingWrapperRunsItsOverrideAtEveryLevel(t *testing.T) {
	cases := []struct {
		name        string
		policy      Policy
		copies      bool
		wantLevel   ImageReferenceLevel
		wantRefused bool
	}{
		{"no policy", nil, true, ImageLevelTagged, true},
		{"any, copying the component", levelPolicy{&NoopPolicy{}, ImageLevelAny}, true, ImageLevelAny, false},
		{"digest, copying the component", levelPolicy{&NoopPolicy{}, ImageLevelDigest}, true, ImageLevelDigest, true},
		{"any, building a fresh component: the default level, so :latest is refused", levelPolicy{&NoopPolicy{}, ImageLevelAny}, false, ImageLevelTagged, true},
		{"digest, building a fresh component: the default level, and the check holds the stricter one", levelPolicy{&NoopPolicy{}, ImageLevelDigest}, false, ImageLevelTagged, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ran := 0
			inner := &levelHandler{}
			h := wrapperHandler{levelHandler: inner, copies: tc.copies, ran: &ran}
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
			_, err := tr.Transform(makeApp("myapp", makeComponent("web", "widget")), TransformContext{Policy: tc.policy})
			if ran != 1 {
				t.Errorf("the override ran %d times, want 1", ran)
			}
			if inner.asked == nil || *inner.asked != tc.wantLevel {
				t.Errorf("the embedded handler was asked at %v, want %q", inner.asked, tc.wantLevel)
			}
			if (err != nil) != tc.wantRefused {
				t.Errorf("err = %v, want refused = %v", err, tc.wantRefused)
			}
		})
	}
}

// A config that names no image reference is left alone.
func TestTransform_ConfigWithoutImageReferencesIsNotChecked(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"widget": &refusingHandler{}}, nil)
	_, err := tr.Transform(makeApp("myapp", makeComponent("web", "widget")), TransformContext{Policy: levelPolicy{&NoopPolicy{}, ImageLevelDigest}})
	if err != nil {
		t.Errorf("err = %v, want none", err)
	}
}
