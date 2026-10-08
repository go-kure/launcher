package kurel

import (
	"strings"
	"testing"
)

// A kind component and the trait of the same kind can name one object: the
// component's is named after the component (role "object"), the trait's by the
// trait. Both names are claimed, so the document is refused with both owners
// named, whichever of the two the document lists first.

// kindTraitPairs are the kinds a trait and a kind component both generate.
// trait is the trait as component "api" carries it, object the name its object
// takes there, and kind the properties of a kind component of that type.
var kindTraitPairs = []struct {
	typ, identity string
	trait, object string
	traitOwner    string
	kind          string
}{
	{
		// The trait's `name` is required: its object has no default name.
		typ: "cilium-networkpolicy", identity: `CiliumNetworkPolicy.cilium.io "default/api-policy"`,
		trait: claimCiliumTrait("api-policy"), object: "api-policy",
		traitOwner: `component "api" traits[0] "cilium-networkpolicy" (its own object, set by name)`,
		kind: `        spec:
          endpointSelector:
            matchLabels:
              app: api
          ingress:
            - fromEndpoints:
                - matchLabels:
                    app: frontend
`,
	},
	{
		typ: "httproute", identity: `HTTPRoute.gateway.networking.k8s.io "default/api-httproute"`,
		trait: claimHTTPRouteTrait, object: "api-httproute",
		traitOwner: `component "api" traits[0] "httproute" (role "httproute", its default)`,
		kind: `        parentRefs:
          - name: gw
        rules:
          - backendRefs:
              - name: api
                port: 8080
`,
	},
	{
		typ: "ingress", identity: `Ingress.networking.k8s.io "default/api-ingress"`,
		trait: claimIngressTrait, object: "api-ingress",
		traitOwner: `component "api" traits[0] "ingress" (role "ingress", its default)`,
		kind: `        defaultBackend:
          service:
            name: api
            port:
              number: 8080
`,
	},
	{
		// The trait's name has a role of its own; the claim space is the same.
		typ: "networkpolicy", identity: `NetworkPolicy.networking.k8s.io "default/api-allow"`,
		trait: networkPolicyTrait, object: "api-allow",
		traitOwner: `component "api" traits[0] "networkpolicy" (role "networkpolicy", its default)`,
		kind: `        podSelector:
          matchLabels:
            app: api
        policyTypes:
          - Ingress
`,
	},
}

// claimKind is a kind component of type typ.
func claimKind(name, typ, properties string) string {
	return `    - name: ` + name + `
      type: ` + typ + `
      properties:
` + properties
}

// TestNetworkPolicyKind_HeldAgainstASynthesizedPolicy: the synthesis names its
// policies under a role too, as the same kind, so a networkpolicy component
// under one of those names is refused with both named.
func TestNetworkPolicyKind_HeldAgainstASynthesizedPolicy(t *testing.T) {
	// A profile whose ingress capability names a traffic source: the synthesis
	// then opens the backend's port to it.
	const profile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    ingress:
      rendering:
        networkPolicy:
          trafficSources:
            - namespace: ingress-nginx
`
	workload := claimWorkload("api", claimIngressTrait)
	docs, err := buildWithProfile(t, duplicateApp(workload, "", ""), profile)
	if err != nil {
		t.Fatalf("kurel build: %v", err)
	}
	const synthesized = "api-allow-ingress-traffic"
	if got := docsOfKind(docs, "NetworkPolicy", "networking.k8s.io"); len(got) != 1 || got[0] != synthesized {
		t.Fatalf("the synthesized NetworkPolicies are %v, want the one named %q", got, synthesized)
	}
	kind := claimKind(synthesized, "networkpolicy", "        podSelector: {}\n")
	_, err = buildWithProfile(t, duplicateApp(workload+kind, "", ""), profile)
	for _, want := range []string{
		`name collision: NetworkPolicy.networking.k8s.io "default/` + synthesized + `" is named by `,
		`component "` + synthesized + `" (role "object", its default)`,
		`component "api" (role "netpol-synth", its default)`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	}
}

func TestKindAndTrait_NameOneObject(t *testing.T) {
	for _, tc := range kindTraitPairs {
		t.Run(tc.typ, func(t *testing.T) {
			workload := claimWorkload("api", tc.trait)
			if err := buildErr(t, claimKind("edge", tc.typ, tc.kind)+workload); err != nil {
				t.Fatalf("a kind component under a name of its own: %v, want it accepted beside the trait", err)
			}
			kind := claimKind(tc.object, tc.typ, tc.kind)
			kindOwner := `component "` + tc.object + `" (role "object", its default)`
			for order, components := range map[string]string{
				"kind first":  kind + workload,
				"trait first": workload + kind,
			} {
				err := buildErr(t, components)
				if err == nil {
					t.Errorf("%s: a %s component and a %s trait naming one object were accepted", order, tc.typ, tc.typ)
					continue
				}
				for _, want := range []string{"name collision: " + tc.identity + " is named by ", kindOwner, tc.traitOwner} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%s: err = %v\nwant it to contain %q", order, err, want)
					}
				}
			}

			// The author's objectName is the component's object name as much as
			// its default is.
			renamed := claimKind("edge", tc.typ, "        objectName: "+tc.object+"\n"+tc.kind)
			err := buildErr(t, renamed+workload)
			want := `component "edge" (role "object", set by properties.objectName)`
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), tc.traitOwner) {
				t.Errorf("objectName equal to the trait's object: err = %v\nwant it to name %q and %q", err, want, tc.traitOwner)
			}
		})
	}
}
