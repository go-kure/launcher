package oam_test

import (
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// go-kure/launcher#790: a real ExternalName `service` config, beside a real
// `deployment` config, through each place a sibling group reads
// ServiceRoutingTarget. None of them may take the ExternalName member's
// selector, which has no labels, for one that selects the deployment's pods.

type routingTargeter interface {
	ServiceRoutingTarget([]intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString)
}

func siblingMemberConfig(t *testing.T, h oam.ComponentHandler, typ string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "db", Type: typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("%s ToApplicationConfig: %v", typ, err)
	}
	return cfg
}

// externalNameGroup returns a group of a deployment and an ExternalName service
// that lists the port the deployment would be reached on, and the service
// member's own config.
func externalNameGroup(t *testing.T) (group, svc stack.ApplicationConfig) {
	t.Helper()
	dep := siblingMemberConfig(t, &components.DeploymentHandler{}, "deployment", map[string]any{"image": "ghcr.io/example/db:1.0"})
	svc = siblingMemberConfig(t, &components.ServiceHandler{}, "service", map[string]any{
		"type": "ExternalName", "externalName": "db.example.com",
		"ports": []any{map[string]any{"name": "pg", "port": 5432}},
	})
	return oam.NewSiblingGroupForTest("db", "default", dep, svc), svc
}

// The group forwards the member's answer unchanged: the selector without
// labels, and no port for a port the Service lists, by number or by name.
func TestSiblingGroup_ExternalNameMember_ForwardsNoPort(t *testing.T) {
	group, _ := externalNameGroup(t)
	for name, routed := range map[string][]intstr.IntOrString{
		"no port":       nil,
		"listed number": {intstr.FromInt32(5432)},
		"listed name":   {intstr.FromString("pg")},
	} {
		t.Run(name, func(t *testing.T) {
			sel, ports := group.(routingTargeter).ServiceRoutingTarget(routed)
			if sel == nil || len(sel.MatchLabels) != 0 || len(sel.MatchExpressions) != 0 {
				t.Errorf("selector = %v, want a non-nil one without labels", sel)
			}
			if len(ports) != 0 {
				t.Errorf("ports = %v, want none", ports)
			}
		})
	}
}

// The group does not read the ExternalName member as routing to the
// deployment's pods.
func TestSiblingGroup_ExternalNameMember_DoesNotRouteToOwnPods(t *testing.T) {
	group, _ := externalNameGroup(t)
	if oam.SiblingGroupRoutesToOwnPodsForTest(group) {
		t.Error("routesToOwnPods = true for a group whose Service is an ExternalName one")
	}
}

// The ExternalName member answers the routing-target contract, so a second
// member answering it is refused as for any other Service; it also names its
// Service and knows its ports, and has no service port.
func TestSiblingGroup_ExternalNameMember_Answers(t *testing.T) {
	_, svc := externalNameGroup(t)
	got := oam.SiblingAnswersForTest(svc)
	want := []string{"BackendServiceName", "ServicePortName", "ServiceRoutingTarget"}
	if !slices.Equal(got, want) {
		t.Errorf("siblingAnswers = %v, want %v", got, want)
	}
}
