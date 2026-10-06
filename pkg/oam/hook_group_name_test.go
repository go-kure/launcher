package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// tooLongHookGroupName is the refusal of the longest name of a three-group
// chart under a 49-character prefix, as a helmtemplate config builds it.
func tooLongHookGroupName() *HookGroupNameError {
	prefix := strings.Repeat("p", 49)
	return &HookGroupNameError{
		ComponentType: "helmtemplate",
		Component:     "db",
		Role:          NameRoleHookGroup,
		Name:          prefix + "-02-post-install",
		Length:        65,
		Limit:         63,
		Prefix:        prefix,
	}
}

// TestHookGroupNameError_Text: the error prints the text the refusal has always
// had, built from its fields, and answers to its sentinel alone. The prefix
// length the text asks for is what the limit leaves beside the name's suffix.
func TestHookGroupNameError_Text(t *testing.T) {
	refusal := tooLongHookGroupName()
	want := `helmtemplate: component "db": hook-group name "` + refusal.Name + `" (role "hook-group") is 65 characters, and a Flux Kustomization name has at most 63; ` +
		`its prefix "` + refusal.Prefix + `" was set by hookGroupNamePrefix or returned by the Naming hook and is never shortened: use a prefix of at most 47 characters, or none for the default, which is shortened`
	if got := refusal.Error(); got != want {
		t.Errorf("text = %s\nwant   %s", got, want)
	}
	if !errors.Is(refusal, ErrHookGroupNameTooLong) {
		t.Error("errors.Is(refusal, ErrHookGroupNameTooLong) = false")
	}
	if errors.Is(refusal, ErrNameCollision) {
		t.Error("the refusal answers to ErrNameCollision")
	}
}

// hookGroupCheckedConfig is a config the transform holds to the policy and then
// asks for its hook-group names. It records the order of the two calls and the
// policy it was handed, and refuses with refusal.
type hookGroupCheckedConfig struct {
	calls   []string
	policy  Policy
	refusal error
}

func (c *hookGroupCheckedConfig) Generate(*stack.Application) ([]*client.Object, error) {
	return nil, nil
}

func (c *hookGroupCheckedConfig) ApplyPolicy(p Policy) error {
	c.calls, c.policy = append(c.calls, "policy"), p
	return nil
}

func (c *hookGroupCheckedConfig) CheckHookGroupNames() error {
	c.calls = append(c.calls, "names")
	return c.refusal
}

type hookGroupCheckedHandler struct{ config *hookGroupCheckedConfig }

func (h *hookGroupCheckedHandler) CanHandle(t string) bool { return t == "chart" }
func (h *hookGroupCheckedHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return h.config, nil
}

// TestTransform_ChecksHookGroupNamesAfterThePolicy: the transform asks a
// HookGroupNameChecker for its names once the policy has been applied, with a
// policy that is never nil, so a config that renders for the policy has
// rendered. What the config refuses fails the transform behind the component's
// name, as a naming refusal and no policy violation, and is still found with
// errors.Is and errors.As.
func TestTransform_ChecksHookGroupNamesAfterThePolicy(t *testing.T) {
	transform := func(config *hookGroupCheckedConfig) error {
		tr := NewTransformer(map[string]ComponentHandler{"chart": &hookGroupCheckedHandler{config: config}}, nil)
		_, err := tr.Transform(makeApp("shop", makeComponent("db", "chart")), TransformContext{})
		return err
	}

	t.Run("refused", func(t *testing.T) {
		refusal := tooLongHookGroupName()
		config := &hookGroupCheckedConfig{refusal: refusal}
		err := transform(config)
		if want := `component "db": ` + refusal.Error(); err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant %s", err, want)
		}
		if !slices.Equal(config.calls, []string{"policy", "names"}) {
			t.Errorf("calls = %v, want the policy and then the names", config.calls)
		}
		if config.policy == nil {
			t.Error("the config was handed a nil policy")
		}
		if !errors.Is(err, ErrHookGroupNameTooLong) {
			t.Error("errors.Is(err, ErrHookGroupNameTooLong) = false through the transform's prefix")
		}
		var found *HookGroupNameError
		if !errors.As(err, &found) || found != refusal {
			t.Errorf("errors.As found %+v, want the config's own refusal", found)
		}
		var violation *ViolationError
		if errors.As(err, &violation) {
			t.Error("the refusal is reported as a policy violation")
		}
	})

	t.Run("accepted", func(t *testing.T) {
		config := &hookGroupCheckedConfig{}
		if err := transform(config); err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if !slices.Equal(config.calls, []string{"policy", "names"}) {
			t.Errorf("calls = %v, want the policy and then the names", config.calls)
		}
	})
}
