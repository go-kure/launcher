package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// Two external backend Services are two owners of their policies' names, also
// when shortening gives both one default: the transform refuses them, naming
// both Services, as the synthesis did before it left the comparison to the
// resolved names (go-kure/launcher#239, go-kure/launcher#787).
func TestResolveSynthesizedPolicyNames_ExternalBackendsOfOneShortenedDefault(t *testing.T) {
	const suffix = "-allow-ingress-traffic"
	long := strings.Repeat("a", 240)
	policy := ingressTrafficPolicyName(long)
	// The Service whose own, unshortened default is the shortened default of long.
	twin, cut := strings.CutSuffix(policy, suffix)
	if !cut || twin == long || ingressTrafficPolicyName(twin) != policy {
		t.Fatalf("no second Service shares the default %q of the first", policy)
	}
	router := func(component, service string) *stack.Application {
		return stack.NewApplication(component+"-ingress", "default", &extBackendStub{
			component: component,
			sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
			targets: []netpol.BackendTarget{{
				ServiceName: service,
				Ports:       []intstr.IntOrString{intstr.FromInt32(8081)},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": component}},
			}},
		})
	}
	cluster := twoLeafBundleCluster(router("router-a", long), router("router-b", twin))
	if err := synthesizeNetworkPolicies(cluster, nil, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if got := countClusterApps(cluster, policy); got != 2 {
		t.Fatalf("the synthesis queued %d policies named %q, want the 2 the resolver must tell apart", got, policy)
	}

	resolver := &nameResolver{application: "shop", claims: NewNameAllocator()}
	err := resolver.resolveSynthesizedPolicyNames(cluster, nil)
	want := `synthesized NetworkPolicy "` + policy + `": name collision: NetworkPolicy.networking.k8s.io "default/` + policy +
		`" is named by external backend Service "` + long + `" (role "netpol-synth", its default) and by external backend Service "` +
		twin + `" (role "netpol-synth", its default); give one of them another name`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %q", err, want)
	}
}
