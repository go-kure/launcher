package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

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
