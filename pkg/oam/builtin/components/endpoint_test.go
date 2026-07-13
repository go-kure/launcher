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
