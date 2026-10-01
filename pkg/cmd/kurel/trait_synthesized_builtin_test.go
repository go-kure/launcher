package kurel

import (
	"os"
	"testing"
)

// TestBuiltinChains_SealedTraitsPassReservedCheck pins that the built-in lowering
// chains still build now that D3 (enforcePlatformReserved) also checks a sealed
// trait no checked rule emitted (go-kure/launcher#611). ExposeRule and WorkerRule
// both declare a schema, so the sealed traits they emit (expose's ingress/httproute,
// worker's topology-spread when topologySpread is set) are synthesized. That matters
// for expose: it forwards a capability-rendered networkPolicy, which the ingress and
// httproute schemas reserve, into the trait it emits. The "platform keys" cases
// render networkPolicy and the reserved keys ExposeRule consumes itself.
func TestBuiltinChains_SealedTraitsPassReservedCheck(t *testing.T) {
	ingressWithPlatformKeys := []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
        allowedHostnameWildcard: "*.example.com"
        certManagerClusterIssuer: letsencrypt
        networkPolicy:
          trafficSources:
            - namespace: ingress-nginx
`)
	gatewayWithPlatformKeys := []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
        gatewayName: my-gateway
        networkPolicy:
          trafficSources:
            - namespace: gateway-system
`)

	cases := []struct {
		name    string
		appPath string
		profile []byte
	}{
		{"expose ingress", "testdata/webservice-expose-ingress/app.yaml", readProfile(t, "testdata/webservice-expose-ingress/cluster.yaml")},
		{"expose gateway", "testdata/webservice-expose-gateway/app.yaml", readProfile(t, "testdata/webservice-expose-gateway/cluster.yaml")},
		{"expose ingress with platform keys", "testdata/webservice-expose-ingress/app.yaml", ingressWithPlatformKeys},
		{"expose gateway with platform keys", "testdata/webservice-expose-gateway/app.yaml", gatewayWithPlatformKeys},
		{"worker", "testdata/worker-scheduling/app.yaml", readProfile(t, "testdata/worker-scheduling/cluster.yaml")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildFixtureYAML(t, tc.appPath, tc.profile); len(got) == 0 {
				t.Fatalf("build of %q produced no manifests", tc.appPath)
			}
		})
	}
}

func readProfile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %q: %v", path, err)
	}
	return data
}
