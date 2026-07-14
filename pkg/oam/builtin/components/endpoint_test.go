package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestWebserviceHandler_Endpoints(t *testing.T) {
	h := &components.WebserviceHandler{}

	tests := []struct {
		name     string
		props    map[string]any
		wantPort int32
	}{
		{name: "default port", props: nil, wantPort: 80},
		{name: "explicit port", props: map[string]any{"port": 8080}, wantPort: 8080},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eps, err := h.Endpoints(&oam.Component{Name: "api", Type: "webservice", Properties: tt.props})
			if err != nil {
				t.Fatalf("Endpoints: %v", err)
			}
			if len(eps) != 1 {
				t.Fatalf("expected 1 endpoint, got %d", len(eps))
			}
			sel := eps[0].PodSelector
			if sel == nil || sel.MatchLabels["app"] != "api" || len(sel.MatchLabels) != 1 {
				t.Errorf("selector = %v, want app=api", sel)
			}
			if len(eps[0].Ports) != 1 || eps[0].Ports[0].IntVal != tt.wantPort {
				t.Errorf("ports = %v, want [%d]", eps[0].Ports, tt.wantPort)
			}
		})
	}
}

// TestWorkerHandler_NotEndpointProvider documents the #225 decision: worker declares no
// in-cluster port and emits no Service, so it deliberately does not implement EndpointProvider
// (ComponentEndpoints then returns (nil,nil) for a worker component).
func TestWorkerHandler_NotEndpointProvider(t *testing.T) {
	var h any = &components.WorkerHandler{}
	if _, ok := h.(oam.EndpointProvider); ok {
		t.Error("WorkerHandler should not implement oam.EndpointProvider (worker has no in-cluster port)")
	}
}

func TestPostgresqlHandler_Endpoints(t *testing.T) {
	h := &components.PostgresqlHandler{}
	eps, err := h.Endpoints(&oam.Component{Name: "orders-db", Type: "postgresql"})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	sel := eps[0].PodSelector
	if sel == nil || sel.MatchLabels["cnpg.io/cluster"] != "orders-db" || len(sel.MatchLabels) != 1 {
		t.Errorf("selector = %v, want cnpg.io/cluster=orders-db", sel)
	}
	if len(eps[0].Ports) != 1 || eps[0].Ports[0].IntVal != 5432 {
		t.Errorf("ports = %v, want [5432]", eps[0].Ports)
	}
}

func TestPostgresqlHandler_Endpoints_Pooler(t *testing.T) {
	h := &components.PostgresqlHandler{}

	// Pooler disabled (or absent) → only the direct-cluster endpoint.
	eps, err := h.Endpoints(&oam.Component{Name: "orders-db", Type: "postgresql",
		Properties: map[string]any{"pooler": map[string]any{"enabled": false}}})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("pooler disabled: expected 1 endpoint, got %d", len(eps))
	}

	// Pooler enabled → a second endpoint on the pooler pods.
	eps, err = h.Endpoints(&oam.Component{Name: "orders-db", Type: "postgresql",
		Properties: map[string]any{"pooler": map[string]any{"enabled": true}}})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("pooler enabled: expected 2 endpoints, got %d", len(eps))
	}
	// First is the direct cluster endpoint; second is the pooler.
	if eps[0].PodSelector.MatchLabels["cnpg.io/cluster"] != "orders-db" {
		t.Errorf("endpoint[0] selector = %v, want cnpg.io/cluster=orders-db", eps[0].PodSelector)
	}
	pooler := eps[1].PodSelector
	if pooler == nil || pooler.MatchLabels["cnpg.io/poolerName"] != "orders-db-pooler" || len(pooler.MatchLabels) != 1 {
		t.Errorf("endpoint[1] selector = %v, want cnpg.io/poolerName=orders-db-pooler", pooler)
	}
	if len(eps[1].Ports) != 1 || eps[1].Ports[0].IntVal != 5432 {
		t.Errorf("pooler ports = %v, want [5432]", eps[1].Ports)
	}
}
