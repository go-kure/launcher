package oam

import "testing"

// TestSettledTypeTables_RoleMember: the tables keyed by a component's settled
// type give the member `webservice` and `worker` lower into
// (go-kure/launcher#1629) the rows its predecessor, the kind-named
// `deployment`, held for it: a scaler that targets a Deployment and the
// disruption trait.
func TestSettledTypeTables_RoleMember(t *testing.T) {
	target, ok := ScalerTargetFor("role-deployment")
	if !ok {
		t.Fatal("no scaler target for the role member type")
	}
	if target != DefaultScaleTarget {
		t.Errorf("scaler target = %+v, want %+v", target, DefaultScaleTarget)
	}
	if !traitComponentRestrictions["scaler"]["role-deployment"] {
		t.Error("the scaler trait is not admitted on the role member type")
	}
	if !traitComponentRestrictions["disruption"]["role-deployment"] {
		t.Error("the disruption trait is not admitted on the role member type")
	}
}
