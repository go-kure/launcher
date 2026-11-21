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

// A config that names no image reference is left alone.
func TestTransform_ConfigWithoutImageReferencesIsNotChecked(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"widget": &refusingHandler{}}, nil)
	_, err := tr.Transform(makeApp("myapp", makeComponent("web", "widget")), TransformContext{Policy: levelPolicy{&NoopPolicy{}, ImageLevelDigest}})
	if err != nil {
		t.Errorf("err = %v, want none", err)
	}
}
