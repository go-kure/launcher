package oam

import (
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// applicationNamedConfig implements ApplicationConfig, ApplicationNameSetter
// and Enforceable, and records the calls it receives in order.
type applicationNamedConfig struct {
	calls []string
}

func (c *applicationNamedConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	return nil, nil
}

func (c *applicationNamedConfig) SetApplicationName(name string) {
	c.calls = append(c.calls, "application "+name)
}

func (c *applicationNamedConfig) ApplyPolicy(_ Policy) error {
	c.calls = append(c.calls, "policy")
	return nil
}

// applicationNamedHandler hands out the configs it is built with, one per
// component, in order.
type applicationNamedHandler struct {
	configs []*applicationNamedConfig
	next    int
}

func (h *applicationNamedHandler) CanHandle(t string) bool { return t == "named" }
func (h *applicationNamedHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	cfg := h.configs[h.next]
	h.next++
	return cfg, nil
}

// TestTransform_ApplicationNameSetter pins the ApplicationNameSetter contract:
// every component config that implements it is told the document's name once,
// before policy runs on it, and a config that does not is left alone.
func TestTransform_ApplicationNameSetter(t *testing.T) {
	named := &applicationNamedHandler{configs: []*applicationNamedConfig{{}, {}}}
	tr := NewTransformer(map[string]ComponentHandler{
		"named": named,
		"plain": &pipelineComponentHandler{typ: "plain"},
	}, nil)
	app := makeApp("shop", makeComponent("db", "named"), makeComponent("web", "plain"), makeComponent("cache", "named"))
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	want := []string{"application shop", "policy"}
	for i, cfg := range named.configs {
		if !slices.Equal(cfg.calls, want) {
			t.Errorf("config %d received %v, want %v", i, cfg.calls, want)
		}
	}
}
