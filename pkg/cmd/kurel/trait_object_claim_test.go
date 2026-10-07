package kurel

import (
	"strings"
	"testing"
)

// The `ingress` and `httproute` traits resolve their one object's name under
// their roles (`ingress`, `httproute`); the `cilium-networkpolicy` trait names
// its object itself, under no role, and claims the name
// (oam.Trait.ClaimObjectName). Either way a second owner of that object is
// refused with both named.

const (
	claimIngressTrait = `        - type: ingress
          properties:
            rules:
              - host: shop.example.com
                paths:
                  - path: /
`
	claimHTTPRouteTrait = `        - type: httproute
          properties:
            parentRefs:
              - name: gw
            rules:
              - backendRefs:
                  - port: 8080
`
)

// claimCiliumTrait is a cilium-networkpolicy trait, whose `name` is required.
func claimCiliumTrait(name string) string {
	return `        - type: cilium-networkpolicy
          properties:
            name: ` + name + `
            endpointSelector: {}
            ingress:
              - fromEndpoints:
                  - matchLabels:
                      app: frontend
`
}

// claimWorkload is a webservice component carrying traits.
func claimWorkload(name, traits string) string {
	return `    - name: ` + name + `
      type: webservice
      properties:
        image: ghcr.io/example/` + name + `:v1.0.0
        port: 8080
      traits:
` + traits
}

// buildErr builds components with the test cluster profile and returns the
// build's own verdict.
func buildErr(t *testing.T, components string) error {
	t.Helper()
	_, err := buildWithProfile(t, duplicateApp(components, "", ""), testClusterYAML)
	return err
}

func TestTraitObjectClaim_TwoTraitsNameOneObject(t *testing.T) {
	for _, tc := range []struct {
		name          string
		web, api, off string // web's trait, api's trait, and web's trait under a name of its own
		want          string
	}{
		{
			name: "ingress",
			web:  claimIngressTrait + "            name: api-ingress\n", api: claimIngressTrait,
			off: claimIngressTrait,
			want: `name collision: Ingress.networking.k8s.io "default/api-ingress" is named by ` +
				`component "web" traits[0] "ingress" (role "ingress", set by name) and by ` +
				`component "api" traits[0] "ingress" (role "ingress", its default); give one of them another name`,
		},
		{
			name: "httproute",
			web:  claimHTTPRouteTrait + "            name: api-httproute\n", api: claimHTTPRouteTrait,
			off: claimHTTPRouteTrait,
			want: `name collision: HTTPRoute.gateway.networking.k8s.io "default/api-httproute" is named by ` +
				`component "web" traits[0] "httproute" (role "httproute", set by name) and by ` +
				`component "api" traits[0] "httproute" (role "httproute", its default); give one of them another name`,
		},
		{
			name: "cilium-networkpolicy",
			web:  claimCiliumTrait("allow-frontend"), api: claimCiliumTrait("allow-frontend"),
			off: claimCiliumTrait("web-allow-frontend"),
			want: `name collision: CiliumNetworkPolicy.cilium.io "default/allow-frontend" is named by ` +
				`component "web" traits[0] "cilium-networkpolicy" (its own object, set by name) and by ` +
				`component "api" traits[0] "cilium-networkpolicy" (its own object, set by name); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := buildErr(t, claimWorkload("web", tc.off)+claimWorkload("api", tc.api)); err != nil {
				t.Fatalf("two names: %v, want two components with the same trait accepted", err)
			}
			err := buildErr(t, claimWorkload("web", tc.web)+claimWorkload("api", tc.api))
			if err == nil {
				t.Fatalf("two traits naming one object were accepted, want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant it to contain %q", err, tc.want)
			}
		})
	}
}

// TestTraitObjectClaim_SameTraitTwiceOnOneComponent: the two default names of
// two traits of one type on one component are one name, and the claim says
// which two traits, where the generated objects alone say only that there are
// two.
func TestTraitObjectClaim_SameTraitTwiceOnOneComponent(t *testing.T) {
	err := buildErr(t, claimWorkload("web", claimIngressTrait+claimIngressTrait))
	const want = `name collision: Ingress.networking.k8s.io "default/web-ingress" is named by ` +
		`component "web" traits[0] "ingress" (role "ingress", its default) and by ` +
		`component "web" traits[1] "ingress" (role "ingress", its default); give one of them another name`
	if err == nil {
		t.Fatalf("two ingress traits under one default name were accepted, want %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v\nwant it to contain %q", err, want)
	}
	// A scope sets them apart, as it did before the name was claimed.
	scoped := strings.Replace(claimIngressTrait, "          properties:\n", "          properties:\n            scope: internal\n", 1)
	if err := buildErr(t, claimWorkload("web", claimIngressTrait+scoped)); err != nil {
		t.Errorf("a scoped second ingress trait: %v, want it accepted", err)
	}
}
