package oam

import (
	"errors"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"
)

// --- stub handlers for tests ---

type stubComponentHandler struct{ typ string }

func (h *stubComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *stubComponentHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return nil, nil
}

type stubTraitHandler struct{ typ string }

func (h *stubTraitHandler) CanHandle(t string) bool { return t == h.typ }
func (h *stubTraitHandler) Apply(_ *Trait, _ *stack.Application, _ *stack.Bundle) error {
	return nil
}

type capAwareHandler struct {
	stubTraitHandler
}

func (h *capAwareHandler) CapabilityRequired() bool { return true }

type capAwareWithVAD struct {
	stubTraitHandler
}

func (h *capAwareWithVAD) CapabilityRequired() bool { return true }
func (h *capAwareWithVAD) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	return rendering, nil
}

type stubPolicyHandler struct{ typ string }

func (h *stubPolicyHandler) CanHandle(t string) bool { return t == h.typ }
func (h *stubPolicyHandler) Apply(_ *ApplicationPolicy, _ []string, _ *PolicyResult) error {
	return nil
}

// --- pipeline stubs ---

// stubAppConfig is a minimal ApplicationConfig that generates nothing.
type stubAppConfig struct{}

func (s *stubAppConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }

// pipelineComponentHandler returns a real (non-nil) stubAppConfig.
type pipelineComponentHandler struct{ typ string }

func (h *pipelineComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *pipelineComponentHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}

// enforcingConfig implements ApplicationConfig and Enforceable; fails when failPolicy is set.
type enforcingConfig struct {
	failPolicy bool
}

func (e *enforcingConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (e *enforcingConfig) ApplyPolicy(_ Policy) error {
	if e.failPolicy {
		return errors.New("policy violation")
	}
	return nil
}

// enforcingComponentHandler returns an enforcingConfig.
type enforcingComponentHandler struct {
	typ        string
	failPolicy bool
}

func (h *enforcingComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *enforcingComponentHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &enforcingConfig{failPolicy: h.failPolicy}, nil
}

// capAwarePipelineHandler is a CapabilityAware+ValidateAndApplyDefaults trait handler.
type capAwarePipelineHandler struct{ typ string }

func (h *capAwarePipelineHandler) CanHandle(t string) bool  { return t == h.typ }
func (h *capAwarePipelineHandler) CapabilityRequired() bool { return true }
func (h *capAwarePipelineHandler) Apply(_ *Trait, _ *stack.Application, _ *stack.Bundle) error {
	return nil
}
func (h *capAwarePipelineHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}

// depWritingPolicyHandler writes a dependency into PolicyResult.
type depWritingPolicyHandler struct {
	from, to string
}

func (h *depWritingPolicyHandler) CanHandle(t string) bool { return t == "dependency" }
func (h *depWritingPolicyHandler) Apply(_ *ApplicationPolicy, _ []string, result *PolicyResult) error {
	result.Dependencies[h.from] = append(result.Dependencies[h.from], h.to)
	return nil
}

// makeApp builds a minimal Application for pipeline tests.
func makeApp(name string, components ...Component) *Application {
	return &Application{
		Metadata: Metadata{Name: name, Namespace: "test"},
		Spec:     ApplicationSpec{Components: components},
	}
}

// makeComponent builds a Component with the given name and type.
func makeComponent(name, typ string) Component {
	return Component{Name: name, Type: typ}
}

// mustPanic asserts that f panics.
func mustPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got none")
		}
	}()
	f()
}

// --- NewTransformer ---

func TestNewTransformer_Empty(t *testing.T) {
	// Both nil and empty maps are valid.
	tr1 := NewTransformer(nil, nil)
	if tr1.findComponentHandler("any") != nil {
		t.Error("expected nil for unregistered component handler")
	}
	if tr1.findTraitHandler("any") != nil {
		t.Error("expected nil for unregistered trait handler")
	}

	tr2 := NewTransformer(map[string]ComponentHandler{}, map[string]TraitHandler{})
	if tr2.findComponentHandler("any") != nil {
		t.Error("expected nil for unregistered component handler (empty map)")
	}
}

func TestNewTransformer_WithHandlers(t *testing.T) {
	ch := &stubComponentHandler{typ: "webservice"}
	th := &stubTraitHandler{typ: "expose"}

	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": ch},
		map[string]TraitHandler{"expose": th},
	)

	if got := tr.findComponentHandler("webservice"); got != ch {
		t.Errorf("findComponentHandler(webservice) = %v, want %v", got, ch)
	}
	if got := tr.findTraitHandler("expose"); got != th {
		t.Errorf("findTraitHandler(expose) = %v, want %v", got, th)
	}
}

func TestNewTransformer_PanicsIfPreloadedCapabilityAwareWithoutVAD(t *testing.T) {
	mustPanic(t, func() {
		NewTransformer(nil, map[string]TraitHandler{
			"ingress": &capAwareHandler{stubTraitHandler{typ: "ingress"}},
		})
	})
}

// --- RegisterComponent ---

func TestTransformer_RegisterComponent_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	ch := &stubComponentHandler{typ: "worker"}
	tr.RegisterComponent("worker", ch)
	if got := tr.findComponentHandler("worker"); got != ch {
		t.Errorf("findComponentHandler(worker) = %v, want %v", got, ch)
	}
}

func TestTransformer_RegisterComponent_PanicsOnDuplicate(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("worker", &stubComponentHandler{typ: "worker"})
	mustPanic(t, func() {
		tr.RegisterComponent("worker", &stubComponentHandler{typ: "worker"})
	})
}

func TestTransformer_RegisterComponent_PanicsOnCanHandleMismatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterComponent("worker", &stubComponentHandler{typ: "webservice"})
	})
}

// --- RegisterTrait ---

func TestTransformer_RegisterTrait_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	th := &stubTraitHandler{typ: "expose"}
	tr.RegisterTrait("expose", th)
	if got := tr.findTraitHandler("expose"); got != th {
		t.Errorf("findTraitHandler(expose) = %v, want %v", got, th)
	}
}

func TestTransformer_RegisterTrait_PanicsIfCapabilityAwareWithoutVAD(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterTrait("ingress", &capAwareHandler{stubTraitHandler{typ: "ingress"}})
	})
}

func TestTransformer_RegisterTrait_CapabilityAwareWithVAD_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	th := &capAwareWithVAD{stubTraitHandler{typ: "ingress"}}
	tr.RegisterTrait("ingress", th)
	if got := tr.findTraitHandler("ingress"); got != th {
		t.Errorf("findTraitHandler(ingress) = %v, want %v", got, th)
	}
}

func TestTransformer_RegisterTrait_PanicsOnDuplicate(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTrait("expose", &stubTraitHandler{typ: "expose"})
	mustPanic(t, func() {
		tr.RegisterTrait("expose", &stubTraitHandler{typ: "expose"})
	})
}

func TestTransformer_RegisterTrait_PanicsOnCanHandleMismatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterTrait("expose", &stubTraitHandler{typ: "certificate"})
	})
}

// --- RegisterPolicy ---

func TestTransformer_RegisterPolicy(t *testing.T) {
	tr := NewTransformer(nil, nil)
	ph := &stubPolicyHandler{typ: "dependency"}
	tr.RegisterPolicy("dependency", ph)
	if got := tr.findPolicyHandler("dependency"); got != ph {
		t.Errorf("findPolicyHandler(dependency) = %v, want %v", got, ph)
	}
}

func TestTransformer_RegisterPolicy_PanicsOnDuplicate(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterPolicy("dependency", &stubPolicyHandler{typ: "dependency"})
	mustPanic(t, func() {
		tr.RegisterPolicy("dependency", &stubPolicyHandler{typ: "dependency"})
	})
}

func TestTransformer_RegisterPolicy_PanicsOnCanHandleMismatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterPolicy("dependency", &stubPolicyHandler{typ: "placement"})
	})
}

// --- Pipeline: resolveCapability / buildCapabilityKey ---

func TestResolveCapability_NoCapabilities(t *testing.T) {
	trait := Trait{Type: "expose", Properties: map[string]any{"port": 80}}
	got := resolveCapability(trait, nil)
	if got.Properties["port"] != 80 {
		t.Errorf("expected port 80, got %v", got.Properties["port"])
	}
	if len(got.Properties) != 1 {
		t.Errorf("expected 1 property, got %d", len(got.Properties))
	}
}

func TestResolveCapability_MergesRendering(t *testing.T) {
	caps := map[string]CapabilityBinding{
		"ingress": {Rendering: map[string]any{"host": "example.com", "tls": true}},
	}
	// OAM inline value takes precedence.
	trait := Trait{Type: "ingress", Properties: map[string]any{"host": "override.com"}}
	got := resolveCapability(trait, caps)
	if got.Properties["host"] != "override.com" {
		t.Errorf("expected OAM value to win: got %v", got.Properties["host"])
	}
	if got.Properties["tls"] != true {
		t.Errorf("expected rendering value to be merged: got %v", got.Properties["tls"])
	}
}

func TestBuildCapabilityKey_Scoped(t *testing.T) {
	trait := Trait{Type: "ingress", Properties: map[string]any{"scope": "internal"}}
	if got := buildCapabilityKey(trait); got != "ingress.internal" {
		t.Errorf("buildCapabilityKey = %q, want %q", got, "ingress.internal")
	}
}

func TestBuildCapabilityKey_Bare(t *testing.T) {
	trait := Trait{Type: "ingress", Properties: map[string]any{}}
	if got := buildCapabilityKey(trait); got != "ingress" {
		t.Errorf("buildCapabilityKey = %q, want %q", got, "ingress")
	}
}

// --- Pipeline: Transform / TransformWithPolicy ---

func TestTransform_SingleComponent_Flat(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	app := makeApp("myapp", makeComponent("web", "webservice"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster == nil {
		t.Fatal("expected non-nil cluster")
	}
	if cluster.Node == nil || cluster.Node.Bundle == nil {
		t.Fatal("expected flat cluster with root bundle")
	}
	if len(cluster.Node.Bundle.Applications) != 1 {
		t.Errorf("expected 1 application in bundle, got %d", len(cluster.Node.Bundle.Applications))
	}
}

func TestTransform_MultiTier_Hierarchical(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": &pipelineComponentHandler{typ: "webservice"},
			"daemonset":  &pipelineComponentHandler{typ: "daemonset"},
		},
		nil,
	)
	app := makeApp("myapp",
		makeComponent("web", "webservice"), // TierApps
		makeComponent("log", "daemonset"),  // TierInfra
	)
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cluster.Node == nil || cluster.Node.Bundle == nil {
		t.Fatal("expected umbrella bundle at root")
	}
	if !cluster.Node.Bundle.IsUmbrella() {
		t.Error("expected umbrella bundle for multi-tier app")
	}
	if len(cluster.Node.Bundle.Children) != 2 {
		t.Errorf("expected 2 tier children, got %d", len(cluster.Node.Bundle.Children))
	}
}

func TestTransform_DependencyPolicy_PerComponentBundles(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": &pipelineComponentHandler{typ: "webservice"},
			"postgresql": &pipelineComponentHandler{typ: "postgresql"},
		},
		nil,
	)
	tr.RegisterPolicy("dependency", &depWritingPolicyHandler{from: "web", to: "db"})

	app := &Application{
		Metadata: Metadata{Name: "myapp", Namespace: "test"},
		Spec: ApplicationSpec{
			Components: []Component{
				makeComponent("web", "webservice"),
				makeComponent("db", "postgresql"),
			},
			Policies: []ApplicationPolicy{
				{Name: "order", Type: "dependency"},
			},
		},
	}
	cluster, result, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasDependencies() {
		t.Error("expected PolicyResult to have dependencies")
	}
	// Per-component cluster: root node has children (one per component).
	if cluster.Node == nil {
		t.Fatal("expected non-nil root node")
	}
	if len(cluster.Node.Children) != 2 {
		t.Errorf("expected 2 component nodes, got %d", len(cluster.Node.Children))
	}
}

func TestTransform_NoComponentHandler(t *testing.T) {
	tr := NewTransformer(nil, nil)
	app := makeApp("myapp", makeComponent("web", "webservice"))
	_, err := tr.Transform(app, TransformContext{})
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
	var te *TransformError
	if !errors.As(err, &te) {
		t.Errorf("expected TransformError, got %T: %v", err, err)
	}
}

func TestTransform_NoTraitHandler(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	comp := Component{
		Name:   "web",
		Type:   "webservice",
		Traits: []Trait{{Type: "expose", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	_, err := tr.Transform(app, TransformContext{})
	if err == nil {
		t.Fatal("expected error for missing trait handler")
	}
	var te *TransformError
	if !errors.As(err, &te) {
		t.Errorf("expected TransformError, got %T: %v", err, err)
	}
}

func TestTransform_CapabilityMissing(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"ingress": &capAwarePipelineHandler{typ: "ingress"}},
	)
	comp := Component{
		Name:   "web",
		Type:   "webservice",
		Traits: []Trait{{Type: "ingress", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	_, err := tr.Transform(app, TransformContext{}) // no capabilities
	if err == nil {
		t.Fatal("expected error for missing capability")
	}
	if !errors.Is(err, ErrMissingCapability) {
		t.Errorf("expected ErrMissingCapability in chain, got: %v", err)
	}
}

func TestTransform_PolicyViolation(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": &enforcingComponentHandler{typ: "webservice", failPolicy: true},
		},
		nil,
	)
	app := makeApp("myapp", makeComponent("web", "webservice"))
	_, err := tr.Transform(app, TransformContext{})
	if err == nil {
		t.Fatal("expected ViolationError")
	}
	var ve *ViolationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ViolationError, got %T: %v", err, err)
	}
}

func TestTransformWithPolicy_ReturnsPolicyResult(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	app := makeApp("myapp", makeComponent("web", "webservice"))
	_, result, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil PolicyResult")
	}
}

func TestTransform_NilPolicyNormalized(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	app := makeApp("myapp", makeComponent("web", "webservice"))
	// nil Policy must not panic — normalized to NoopPolicy.
	_, err := tr.Transform(app, TransformContext{Policy: nil})
	if err != nil {
		t.Fatalf("unexpected error with nil policy: %v", err)
	}
}

// --- find* with no match ---

func TestTransformer_FindHandler_NoMatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	if got := tr.findComponentHandler("unknown"); got != nil {
		t.Errorf("findComponentHandler(unknown) = %v, want nil", got)
	}
	if got := tr.findTraitHandler("unknown"); got != nil {
		t.Errorf("findTraitHandler(unknown) = %v, want nil", got)
	}
	if got := tr.findPolicyHandler("unknown"); got != nil {
		t.Errorf("findPolicyHandler(unknown) = %v, want nil", got)
	}
}
