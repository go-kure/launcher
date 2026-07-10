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

// constrainedPolicy is a Policy stub with configurable capability constraints.
type constrainedPolicy struct {
	NoopPolicy
	forbidden []string
	allowed   []string
	required  []string
}

func (p *constrainedPolicy) ForbiddenCapabilities() []string { return p.forbidden }
func (p *constrainedPolicy) AllowedCapabilities() []string   { return p.allowed }
func (p *constrainedPolicy) RequiredCapabilities() []string  { return p.required }

func TestTransform_CapabilityConstraint_Forbidden(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"expose": &stubTraitHandler{typ: "expose"}},
	)
	comp := Component{
		Name:   "web",
		Type:   "webservice",
		Traits: []Trait{{Type: "expose", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	policy := &constrainedPolicy{forbidden: []string{"expose"}}
	_, err := tr.Transform(app, TransformContext{Policy: policy})
	if err == nil {
		t.Fatal("expected ViolationError for forbidden capability")
	}
	var ve *ViolationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ViolationError, got %T: %v", err, err)
	}
}

func TestTransform_CapabilityConstraint_NotInAllowedList(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"expose": &stubTraitHandler{typ: "expose"}},
	)
	comp := Component{
		Name:   "web",
		Type:   "webservice",
		Traits: []Trait{{Type: "expose", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	policy := &constrainedPolicy{allowed: []string{"ingress"}} // "expose" not in list
	_, err := tr.Transform(app, TransformContext{Policy: policy})
	if err == nil {
		t.Fatal("expected ViolationError for capability not in allowed list")
	}
	var ve *ViolationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ViolationError, got %T: %v", err, err)
	}
}

func TestTransform_CapabilityConstraint_RequiredMissing(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	app := makeApp("myapp", makeComponent("web", "webservice")) // no traits
	policy := &constrainedPolicy{required: []string{"ingress"}}
	_, err := tr.Transform(app, TransformContext{Policy: policy})
	if err == nil {
		t.Fatal("expected ViolationError for missing required capability")
	}
	var ve *ViolationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ViolationError, got %T: %v", err, err)
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

// --- EvaluateProfile ---

type errVADHandler struct {
	stubTraitHandler
	err error
	out map[string]any
}

func (h *errVADHandler) CapabilityRequired() bool { return true }
func (h *errVADHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if h.err != nil {
		return nil, h.err
	}
	if h.out != nil {
		return h.out, nil
	}
	return rendering, nil
}

func TestEvaluateProfile_NilProfile(t *testing.T) {
	tr := NewTransformer(nil, nil)
	got, err := tr.EvaluateProfile(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nil profile, got %v", got)
	}
}

func TestEvaluateProfile_EmptyCapabilities(t *testing.T) {
	tr := NewTransformer(nil, nil)
	profile := &ClusterProfile{Metadata: ClusterProfileMetadata{Name: "test"}}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != profile {
		t.Error("expected same pointer for profile with no capabilities")
	}
}

func TestEvaluateProfile_UnknownCapability_Passthrough(t *testing.T) {
	tr := NewTransformer(nil, nil)
	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"unknown-trait": {Rendering: map[string]any{"x": "y"}},
			},
		},
	}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Capabilities["unknown-trait"].Rendering["x"] != "y" {
		t.Error("unknown capability rendering should pass through unchanged")
	}
}

func TestEvaluateProfile_GitopsEnginePreserved(t *testing.T) {
	tr := NewTransformer(nil, nil)
	// Must include at least one capability so EvaluateProfile reaches the spec-rebuild
	// path (it returns early when Capabilities is empty, which would not catch the bug).
	profile := &ClusterProfile{
		Metadata: ClusterProfileMetadata{Name: "test"},
		Spec: ClusterProfileSpec{
			GitopsEngine: "fluxcd",
			Capabilities: map[string]CapabilityBinding{
				"unknown-cap": {Rendering: map[string]any{"k": "v"}},
			},
		},
	}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.GitopsEngine != "fluxcd" {
		t.Errorf("GitopsEngine = %q, want %q", got.Spec.GitopsEngine, "fluxcd")
	}
}

// dedupTrackingConfig implements ApplicationConfig and SourceDeduplicatable for dedup tests.
type dedupTrackingConfig struct {
	name       string
	sourceKey  string
	suppressed bool
	sharedRef  string
}

func (c *dedupTrackingConfig) GetSourceKey() string     { return c.sourceKey }
func (c *dedupTrackingConfig) GetSourceRefName() string { return c.name }
func (c *dedupTrackingConfig) SuppressSourceGeneration(ref string) {
	c.suppressed = true
	c.sharedRef = ref
}
func (c *dedupTrackingConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	return nil, nil
}

func TestTransform_HelmchartSourceDedup(t *testing.T) {
	cfgA := &dedupTrackingConfig{name: "comp-a", sourceKey: "helm:https://example.com/charts"}
	cfgB := &dedupTrackingConfig{name: "comp-b", sourceKey: "helm:https://example.com/charts"}

	entries := []componentEntry{
		{component: Component{Name: "comp-a"}, app: stack.NewApplication("comp-a", "default", cfgA)},
		{component: Component{Name: "comp-b"}, app: stack.NewApplication("comp-b", "default", cfgB)},
	}

	deduplicateSourceRefs(entries)

	if cfgA.suppressed {
		t.Error("comp-a should not be suppressed (first component with this source key)")
	}
	if !cfgB.suppressed {
		t.Error("comp-b should be suppressed (shares source key with comp-a)")
	}
	if cfgB.sharedRef != "comp-a" {
		t.Errorf("comp-b.sharedRef = %q, want %q", cfgB.sharedRef, "comp-a")
	}
}

func TestTransform_HelmchartMixedDelivery_SameSourceURL(t *testing.T) {
	// template delivery returns "" from GetSourceKey — it must not claim the source key
	// and must not cause the subsequent native component to be suppressed.
	cfgTemplate := &dedupTrackingConfig{name: "app-template", sourceKey: ""}
	cfgNative := &dedupTrackingConfig{name: "app-native", sourceKey: "helm:https://example.com/charts"}

	entries := []componentEntry{
		{component: Component{Name: "app-template"}, app: stack.NewApplication("app-template", "default", cfgTemplate)},
		{component: Component{Name: "app-native"}, app: stack.NewApplication("app-native", "default", cfgNative)},
	}

	deduplicateSourceRefs(entries)

	if cfgTemplate.suppressed {
		t.Error("template component should not be suppressed (empty source key skips dedup)")
	}
	if cfgNative.suppressed {
		t.Error("native component should not be suppressed (first native component with this source key)")
	}
}

func TestEvaluateProfile_NonVADHandler_Passthrough(t *testing.T) {
	// A plain TraitHandler (no CapabilityAware, no ValidateAndApplyDefaults)
	// is registered successfully; EvaluateProfile must pass its rendering through.
	tr := NewTransformer(nil, map[string]TraitHandler{
		"expose": &stubTraitHandler{typ: "expose"},
	})
	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"expose": {Rendering: map[string]any{"controllerType": "ingress"}},
			},
		},
	}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Capabilities["expose"].Rendering["controllerType"] != "ingress" {
		t.Error("non-VAD handler should pass rendering through unchanged")
	}
}

func TestEvaluateProfile_ValidCapability_AppliesDefaults(t *testing.T) {
	enriched := map[string]any{"controllerType": "ingress", "ingressClassName": "nginx"}
	handler := &errVADHandler{stubTraitHandler: stubTraitHandler{typ: "expose"}, out: enriched}
	tr := NewTransformer(nil, map[string]TraitHandler{"expose": handler})
	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"expose": {Rendering: map[string]any{"controllerType": "ingress"}},
			},
		},
	}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Capabilities["expose"].Rendering["ingressClassName"] != "nginx" {
		t.Error("expected enriched rendering from VAD handler")
	}
}

func TestEvaluateProfile_InvalidCapability_ReturnsError(t *testing.T) {
	handler := &errVADHandler{stubTraitHandler: stubTraitHandler{typ: "expose"}, err: errors.New("bad rendering")}
	tr := NewTransformer(nil, map[string]TraitHandler{"expose": handler})
	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"expose": {Rendering: map[string]any{"controllerType": "unknown"}},
			},
		},
	}
	_, err := tr.EvaluateProfile(profile)
	if err == nil {
		t.Fatal("expected error for invalid capability rendering")
	}
	var te *TransformError
	if !errors.As(err, &te) {
		t.Errorf("expected TransformError, got %T: %v", err, err)
	}
}

func TestEvaluateProfile_ScopedKey(t *testing.T) {
	enriched := map[string]any{"controllerType": "ingress", "ingressClassName": "traefik"}
	handler := &errVADHandler{stubTraitHandler: stubTraitHandler{typ: "expose"}, out: enriched}
	tr := NewTransformer(nil, map[string]TraitHandler{"expose": handler})
	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"expose.prod": {Rendering: map[string]any{"controllerType": "ingress"}},
			},
		},
	}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Capabilities["expose.prod"].Rendering["ingressClassName"] != "traefik" {
		t.Error("scoped key should resolve to base type and call VAD handler")
	}
}

// --- CapabilityDefinition tests ---

// customVADHandler is a trait handler for test custom types that accepts any rendering.
type customVADHandler struct{ stubTraitHandler }

func (h *customVADHandler) CapabilityRequired() bool { return false }
func (h *customVADHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}

// capDefFixture returns a CapabilityDefinition with one required integer property.
func capDefFixture(traitType string) *CapabilityDefinition {
	return &CapabilityDefinition{
		APIVersion: "launcher.gokure.dev/v1alpha1",
		Kind:       "CapabilityDefinition",
		Metadata:   Metadata{Name: traitType},
		Spec: CapabilityDefSpec{
			Rendering: CapabilityRenderingSchema{
				Properties: map[string]PropertySchema{
					"timeout": {Type: "integer", Required: true},
					"mode":    {Type: "string", Default: "auto"},
				},
			},
		},
	}
}

func TestEvaluateProfile_CapabilityDefinition_AppliesSchemaBeforeVAD(t *testing.T) {
	// Custom trait registered via RegisterTrait; definition loaded; valid rendering.
	// The definition applies a default for "mode", so VAD should receive it.
	tr := NewTransformer(nil, nil)
	tr.RegisterTrait("custom", &customVADHandler{stubTraitHandler: stubTraitHandler{typ: "custom"}})
	tr.SetCapabilityDefs(map[string]*CapabilityDefinition{"custom": capDefFixture("custom")})

	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"custom": {Rendering: map[string]any{"timeout": float64(30)}},
			},
		},
	}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "mode" default should have been applied.
	if got.Spec.Capabilities["custom"].Rendering["mode"] != "auto" {
		t.Errorf("mode = %v, want %q", got.Spec.Capabilities["custom"].Rendering["mode"], "auto")
	}
}

func TestEvaluateProfile_CapabilityDefinition_SchemaViolationReturnsError(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTrait("custom", &customVADHandler{stubTraitHandler: stubTraitHandler{typ: "custom"}})
	tr.SetCapabilityDefs(map[string]*CapabilityDefinition{"custom": capDefFixture("custom")})

	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				// timeout is required but missing → definition schema rejects this.
				"custom": {Rendering: map[string]any{"mode": "fast"}},
			},
		},
	}
	_, err := tr.EvaluateProfile(profile)
	if err == nil {
		t.Fatal("expected error for missing required property in definition schema")
	}
}

func TestEvaluateProfile_CapabilityDefinition_BuiltinIgnoresDefinition(t *testing.T) {
	// Definition for a built-in type declares timeout as required; even though the
	// rendering omits it, no error fires because built-ins skip definition schema.
	tr := NewTransformer(nil, nil)
	tr.RegisterBuiltinTrait("expose", &customVADHandler{stubTraitHandler: stubTraitHandler{typ: "expose"}})
	tr.SetCapabilityDefs(map[string]*CapabilityDefinition{"expose": capDefFixture("expose")})

	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"expose": {Rendering: map[string]any{"controllerType": "ingress"}},
			},
		},
	}
	_, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("built-in type should ignore definition schema; got error: %v", err)
	}
}

// minimalApp returns an OAM Application with one component and one trait for pipeline tests.
func minimalApp(componentType, traitType string) *Application {
	return &Application{
		APIVersion: "core.oam.dev/v1beta1",
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name:       "comp",
					Type:       componentType,
					Properties: map[string]any{},
					Traits: []Trait{
						{Type: traitType, Properties: map[string]any{}},
					},
				},
			},
		},
	}
}

func TestTransform_CustomTrait_CapabilityResolved_NoDefinition_Warns(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"comp": &pipelineComponentHandler{typ: "comp"}},
		map[string]TraitHandler{"custom": &stubTraitHandler{typ: "custom"}},
	)
	var warned string
	tr.SetWarningHandler(func(msg string) { warned = msg })

	ctx := TransformContext{
		ClusterID:    "test",
		Capabilities: map[string]CapabilityBinding{"custom": {Rendering: map[string]any{"x": "y"}}},
	}
	_, err := tr.Transform(minimalApp("comp", "custom"), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warned == "" {
		t.Error("expected warning for custom trait with resolved capability but no definition")
	}
}

func TestTransform_CustomTrait_CapabilityResolved_NoDefinition_StrictErrors(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"comp": &pipelineComponentHandler{typ: "comp"}},
		map[string]TraitHandler{"custom": &stubTraitHandler{typ: "custom"}},
	)
	tr.SetStrictCapabilities(true)

	ctx := TransformContext{
		ClusterID:    "test",
		Capabilities: map[string]CapabilityBinding{"custom": {Rendering: map[string]any{"x": "y"}}},
	}
	_, err := tr.Transform(minimalApp("comp", "custom"), ctx)
	if err == nil {
		t.Fatal("expected error with strict-capabilities and no definition")
	}
}

func TestTransform_CustomTrait_NoCapabilityResolved_NoWarn(t *testing.T) {
	// No capability in profile → check never fires even for custom types.
	var warned string
	tr := NewTransformer(
		map[string]ComponentHandler{"comp": &pipelineComponentHandler{typ: "comp"}},
		map[string]TraitHandler{"custom": &stubTraitHandler{typ: "custom"}},
	)
	tr.SetStrictCapabilities(true)
	tr.SetWarningHandler(func(msg string) { warned = msg })

	ctx := TransformContext{ClusterID: "test", Capabilities: map[string]CapabilityBinding{}}
	_, err := tr.Transform(minimalApp("comp", "custom"), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warned != "" {
		t.Errorf("unexpected warning: %q", warned)
	}
}

func TestTransform_BuiltinTrait_CapabilityResolved_NoDefinition_NoWarn(t *testing.T) {
	// Built-in trait with resolved capability and no definition: no warning, no error.
	var warned string
	tr := NewTransformer(
		map[string]ComponentHandler{"comp": &pipelineComponentHandler{typ: "comp"}},
		nil,
	)
	tr.RegisterBuiltinTrait("expose", &stubTraitHandler{typ: "expose"})
	tr.SetStrictCapabilities(true)
	tr.SetWarningHandler(func(msg string) { warned = msg })

	ctx := TransformContext{
		ClusterID:    "test",
		Capabilities: map[string]CapabilityBinding{"expose": {Rendering: map[string]any{"type": "ingress"}}},
	}
	_, err := tr.Transform(minimalApp("comp", "expose"), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warned != "" {
		t.Errorf("unexpected warning for built-in trait: %q", warned)
	}
}

// --- applyAutoHealthChecks namespace + delivery=template veto (#234) ---

// fluxHCConfig implements ApplicationConfig + fluxNamespaceSettable (like a
// native helmchart whose HelmRelease is relocated to the flux namespace).
type fluxHCConfig struct{ ns string }

func (c *fluxHCConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (c *fluxHCConfig) SetFluxNamespace(ns string)                              { c.ns = ns }

// templateHCConfig is a fluxHCConfig that vetoes its auto health check (like a
// helmchart with delivery=template: no HelmRelease object emitted).
type templateHCConfig struct{ fluxHCConfig }

func (c *templateHCConfig) EmitsAutoHealthCheck() bool { return false }

// plainHCConfig implements only ApplicationConfig (a workload component whose
// object stays in the app namespace; not flux-relocated).
type plainHCConfig struct{}

func (c *plainHCConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }

func leafClusterWith(app *stack.Application) *stack.Cluster {
	return &stack.Cluster{Node: &stack.Node{Bundle: &stack.Bundle{Applications: []*stack.Application{app}}}}
}

func helmchartEntryMap(app *stack.Application, typ string) map[string]componentEntry {
	return map[string]componentEntry{app.Name: {component: Component{Name: app.Name, Type: typ}, app: app}}
}

func TestApplyAutoHealthChecks_FluxConfigUsesFluxNamespace(t *testing.T) {
	app := stack.NewApplication("redis", "demo", &fluxHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "helmchart"), nil, "flux-system")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 {
		t.Fatalf("expected 1 health check, got %d", len(hc))
	}
	if hc[0].Kind != "HelmRelease" || hc[0].Namespace != "flux-system" {
		t.Errorf("got %s/%s in %q, want HelmRelease in flux-system", hc[0].Kind, hc[0].Name, hc[0].Namespace)
	}
}

func TestApplyAutoHealthChecks_EmptyFluxNamespaceUsesAppNamespace(t *testing.T) {
	app := stack.NewApplication("redis", "demo", &fluxHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "helmchart"), nil, "")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 || hc[0].Namespace != "demo" {
		t.Fatalf("expected HelmRelease check in app namespace 'demo', got %+v", hc)
	}
}

func TestApplyAutoHealthChecks_WorkloadKeepsAppNamespace(t *testing.T) {
	app := stack.NewApplication("web", "demo", &plainHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "webservice"), nil, "flux-system")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 || hc[0].Kind != "Deployment" || hc[0].Namespace != "demo" {
		t.Fatalf("expected Deployment check in app namespace 'demo', got %+v", hc)
	}
}

func TestApplyAutoHealthChecks_SettableWorkloadKeepsAppNamespace(t *testing.T) {
	// A workload (webservice → Deployment) whose config is fluxNamespaceSettable
	// — e.g. wrapped by configmap/prune-protection, whose wrapper implements
	// SetFluxNamespace even though the Deployment stays in the app namespace.
	// The check must NOT move to the flux namespace (target is not a Flux CR).
	app := stack.NewApplication("web", "demo", &fluxHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "webservice"), nil, "flux-system")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 || hc[0].Kind != "Deployment" || hc[0].Namespace != "demo" {
		t.Fatalf("settable workload check must stay in app namespace 'demo', got %+v", hc)
	}
}

func TestApplyAutoHealthChecks_TemplateDeliverySkipped(t *testing.T) {
	app := stack.NewApplication("rendered", "demo", &templateHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "helmchart"), nil, "flux-system")

	if hc := cluster.Node.Bundle.HealthChecks; len(hc) != 0 {
		t.Fatalf("expected no auto health check for template delivery, got %+v", hc)
	}
}

func TestApplyAutoHealthChecks_OverridesAppendedVerbatim(t *testing.T) {
	app := stack.NewApplication("rendered", "demo", &templateHCConfig{})
	cluster := leafClusterWith(app)
	override := stack.HealthCheck{APIVersion: "helm.toolkit.fluxcd.io/v2", Kind: "HelmRelease", Name: "external", Namespace: "other-ns"}
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "helmchart"), []stack.HealthCheck{override}, "flux-system")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 || hc[0] != override {
		t.Fatalf("expected the user override appended verbatim (namespace untouched), got %+v", hc)
	}
}

func TestApplyAutoHealthChecks_OCIUsesFluxNamespace(t *testing.T) {
	// An oci component emits a Flux Kustomization (a kustomize.toolkit.fluxcd.io
	// control-plane CR relocated to the flux namespace), so its inferred health
	// check must reference the Kustomization GVK in the flux namespace.
	app := stack.NewApplication("checkout", "demo", &fluxHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "oci"), nil, "flux-system")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 {
		t.Fatalf("expected 1 health check, got %d", len(hc))
	}
	if hc[0].APIVersion != "kustomize.toolkit.fluxcd.io/v1" || hc[0].Kind != "Kustomization" {
		t.Errorf("got %s/%s, want Kustomization (kustomize.toolkit.fluxcd.io/v1)", hc[0].APIVersion, hc[0].Kind)
	}
	if hc[0].Name != "checkout" || hc[0].Namespace != "flux-system" {
		t.Errorf("got %q in %q, want checkout in flux-system", hc[0].Name, hc[0].Namespace)
	}
}

func TestApplyAutoHealthChecks_OCIEmptyFluxNamespaceUsesAppNamespace(t *testing.T) {
	// With no flux namespace configured, the Kustomization stays in the app
	// namespace, so the health check must follow it there.
	app := stack.NewApplication("checkout", "demo", &fluxHCConfig{})
	cluster := leafClusterWith(app)
	applyAutoHealthChecks(cluster, helmchartEntryMap(app, "oci"), nil, "")

	hc := cluster.Node.Bundle.HealthChecks
	if len(hc) != 1 || hc[0].Kind != "Kustomization" || hc[0].Namespace != "demo" {
		t.Fatalf("expected Kustomization check in app namespace 'demo', got %+v", hc)
	}
}
