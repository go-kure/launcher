package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// stubEndpointHandler is a ComponentHandler that also implements EndpointProvider.
type stubEndpointHandler struct {
	typeName string
	eps      []netpol.Endpoint
	err      error
}

func (h *stubEndpointHandler) CanHandle(t string) bool { return t == h.typeName }
func (h *stubEndpointHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}
func (h *stubEndpointHandler) Endpoints(_ *Component) ([]netpol.Endpoint, error) {
	return h.eps, h.err
}

// stubPlainHandler is a ComponentHandler that is NOT an EndpointProvider.
type stubPlainHandler struct{ typeName string }

func (h *stubPlainHandler) CanHandle(t string) bool { return t == h.typeName }
func (h *stubPlainHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}

func TestComponentEndpoints_Dispatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("db", &stubEndpointHandler{typeName: "db", eps: []netpol.Endpoint{validEndpoint()}})
	tr.RegisterComponent("plain", &stubPlainHandler{typeName: "plain"})

	// provider → its endpoints
	eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "db"})
	if err != nil || len(eps) != 1 || eps[0].PodSelector.MatchLabels["cnpg.io/cluster"] != "pg" {
		t.Errorf("provider dispatch: eps=%v err=%v", eps, err)
	}
	// registered non-provider → (nil, nil)
	if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "plain"}); eps != nil || err != nil {
		t.Errorf("non-provider: want (nil,nil), got (%v,%v)", eps, err)
	}
	// unknown type → (nil, nil)
	if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "nope"}); eps != nil || err != nil {
		t.Errorf("unknown: want (nil,nil), got (%v,%v)", eps, err)
	}
	// nil component → (nil, nil), no panic
	if eps, err := tr.ComponentEndpoints(nil); eps != nil || err != nil {
		t.Errorf("nil comp: want (nil,nil), got (%v,%v)", eps, err)
	}
}

func TestComponentEndpoints_MalformedProviderErrors(t *testing.T) {
	malformed := []netpol.Endpoint{{ // nil selector, empty ports
		PodSelector: &metav1.LabelSelector{},
		Ports:       []intstr.IntOrString{},
	}}
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("db", &stubEndpointHandler{typeName: "db", eps: malformed})
	if _, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "db"}); err == nil {
		t.Error("expected error for malformed provider endpoint, got nil")
	}
}

// stubEndpointRule is a ComponentLoweringRule that also implements EndpointProvider.
type stubEndpointRule struct {
	typeName string
	eps      []netpol.Endpoint
}

func (r stubEndpointRule) ComponentType() string { return r.typeName }
func (r stubEndpointRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{*c}}, nil
}
func (r stubEndpointRule) Endpoints(_ *Component) ([]netpol.Endpoint, error) { return r.eps, nil }

// stubPlainRule is a ComponentLoweringRule that is NOT an EndpointProvider.
type stubPlainRule struct{ typeName string }

func (r stubPlainRule) ComponentType() string { return r.typeName }
func (r stubPlainRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{*c}}, nil
}

// TestComponentEndpoints_RuleDispatch: a lowerable type has no handler, so its
// endpoints come from the ComponentLoweringRule that claims it.
func TestComponentEndpoints_RuleDispatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stubEndpointRule{typeName: "web", eps: []netpol.Endpoint{validEndpoint()}})
	tr.RegisterComponentLowering(stubPlainRule{typeName: "plain-rule"})

	eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "web"})
	if err != nil || len(eps) != 1 || eps[0].PodSelector.MatchLabels["cnpg.io/cluster"] != "pg" {
		t.Errorf("rule provider dispatch: eps=%v err=%v", eps, err)
	}
	if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "plain-rule"}); eps != nil || err != nil {
		t.Errorf("rule non-provider: want (nil,nil), got (%v,%v)", eps, err)
	}
}

// stubNamedEndpointRule is a ComponentLoweringRule whose endpoint selects pods
// by a name it resolves under the pooler role.
type stubNamedEndpointRule struct{ stubEndpointRule }

func (r stubNamedEndpointRule) EndpointsNamed(c *Component, lctx LoweringContext) ([]netpol.Endpoint, error) {
	name, err := lctx.ResolveName(c.Name, "pooler", NameSpec{Role: NameRolePooler, Kind: poolerKind})
	if err != nil {
		return nil, err
	}
	ep := validEndpoint()
	ep.PodSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"pooler": name}}
	return []netpol.Endpoint{ep}, nil
}

// TestComponentEndpoints_NamedProvider: a NamedEndpointProvider is called in
// place of Endpoints by both accessors. ComponentEndpoints gives it no hook;
// ComponentEndpointsNamed gives it the caller's, asked with the caller's
// application and the component.
func TestComponentEndpoints_NamedProvider(t *testing.T) {
	tr := NewTransformer(nil, nil)
	// Endpoints would answer the plain endpoint: it must not be the one called.
	tr.RegisterComponentLowering(stubNamedEndpointRule{stubEndpointRule{typeName: "db", eps: []netpol.Endpoint{validEndpoint()}}})
	comp := &Component{Name: "pg", Type: "db"}
	selected := func(t *testing.T, eps []netpol.Endpoint, err error) string {
		t.Helper()
		if err != nil || len(eps) != 1 {
			t.Fatalf("eps=%v err=%v, want one endpoint", eps, err)
		}
		return eps[0].PodSelector.MatchLabels["pooler"]
	}

	eps, err := tr.ComponentEndpoints(comp)
	if got := selected(t, eps, err); got != "pg-pooler" {
		t.Errorf("ComponentEndpoints selects %q, want the default", got)
	}

	var asked []NameRequest
	hook := func(req NameRequest) (string, bool) {
		asked = append(asked, req)
		return "chosen", true
	}
	eps, err = tr.ComponentEndpointsNamed("shop", comp, hook)
	if got := selected(t, eps, err); got != "chosen" {
		t.Errorf("ComponentEndpointsNamed selects %q, want the hook's name", got)
	}
	want := NameRequest{Application: "shop", Component: "pg", Role: NameRolePooler, Kind: "Pooler.postgresql.cnpg.io", Default: "pg-pooler"}
	if len(asked) != 1 || asked[0] != want {
		t.Errorf("the hook was asked %+v, want once with %+v", asked, want)
	}

	eps, err = tr.ComponentEndpointsNamed("shop", comp, nil)
	if got := selected(t, eps, err); got != "pg-pooler" {
		t.Errorf("ComponentEndpointsNamed with no hook selects %q, want the default", got)
	}

	// A provider that is not named is called as before, whatever the hook.
	tr.RegisterComponentLowering(stubEndpointRule{typeName: "web", eps: []netpol.Endpoint{validEndpoint()}})
	eps, err = tr.ComponentEndpointsNamed("shop", &Component{Name: "x", Type: "web"}, hook)
	if err != nil || len(eps) != 1 || eps[0].PodSelector.MatchLabels["cnpg.io/cluster"] != "pg" {
		t.Errorf("plain provider under ComponentEndpointsNamed: eps=%v err=%v", eps, err)
	}

	// An answer the transform refuses is refused here, in the same words.
	_, err = tr.ComponentEndpointsNamed("shop", comp, func(NameRequest) (string, bool) { return "No_Name", true })
	const refusal = `the Naming hook returned "No_Name" for role "pooler" in place of "pg-pooler": not a valid DNS-1035 label: `
	if err == nil || !strings.Contains(err.Error(), refusal) {
		t.Errorf("err = %v, want one containing %q", err, refusal)
	}
}

func TestComponentEndpoints_MalformedRuleProviderErrors(t *testing.T) {
	malformed := []netpol.Endpoint{{PodSelector: &metav1.LabelSelector{}, Ports: []intstr.IntOrString{}}}
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stubEndpointRule{typeName: "web", eps: malformed})
	if _, err := tr.ComponentEndpoints(&Component{Name: "x", Type: "web"}); err == nil {
		t.Error("expected error for malformed rule provider endpoint, got nil")
	}
}
