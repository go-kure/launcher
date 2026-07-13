package oam

import (
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
