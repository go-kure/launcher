package registry_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// TestComponentHandlers_RoleMemberIsItsOwnType: the member `webservice` and
// `worker` lower into is a type of its own (go-kure/launcher#1629), registered
// beside the kind-named `deployment`, and neither handler answers for the
// other's type.
func TestComponentHandlers_RoleMemberIsItsOwnType(t *testing.T) {
	handlers := registry.ComponentHandlers()
	role, kind := handlers["role-deployment"], handlers["deployment"]
	if role == nil {
		t.Fatal("no handler registered for role-deployment")
	}
	if kind == nil {
		t.Fatal("no handler registered for deployment")
	}
	if !role.CanHandle("role-deployment") || role.CanHandle("deployment") {
		t.Errorf("the role member handler answers role-deployment=%v deployment=%v, want true and false", role.CanHandle("role-deployment"), role.CanHandle("deployment"))
	}
	if !kind.CanHandle("deployment") || kind.CanHandle("role-deployment") {
		t.Errorf("the deployment handler answers deployment=%v role-deployment=%v, want true and false", kind.CanHandle("deployment"), kind.CanHandle("role-deployment"))
	}
}
