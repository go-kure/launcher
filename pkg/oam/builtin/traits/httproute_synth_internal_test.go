package traits

import "testing"

func TestSynthesizeParentRef(t *testing.T) {
	got := synthesizeParentRef("gw", "")
	if got["name"] != "gw" || got["namespace"] != "gateway-system" {
		t.Errorf("synthesizeParentRef(gw, \"\") = %v, want name=gw namespace=gateway-system", got)
	}
	got = synthesizeParentRef("gw", "infra")
	if got["namespace"] != "infra" {
		t.Errorf("explicit namespace not respected: %v", got)
	}
}
