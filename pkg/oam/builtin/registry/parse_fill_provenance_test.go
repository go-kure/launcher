package registry_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests hold the provenance report to the values the handlers of the five
// workload kinds fill at parse time (go-kure/launcher#1580): reported as
// SourceBuiltin at the path the author would have written, under the owner of the
// properties the component was built from, one source per path. They share the
// helpers of policy_default_provenance_test.go.

// The reshaped parser of the deployment is the role member's (go-kure/launcher#1629),
// reached through `webservice`, so this runs on one and reads the member's entries.
// The raw `deployment` fills nothing and reports nothing of its own: its probe
// documents hold no fill (probe_test.go).
func TestParseFills_RoleMemberReportsBuiltinAndPolicyOncePerPath(t *testing.T) {
	comp := oam.Component{Name: "app", Type: "webservice", Properties: map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}}
	member := owner{component: "app", member: "role-deployment"}

	t.Run("no policy default", func(t *testing.T) {
		prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, comp)
		got := entriesOf(prov, member)
		for path, ref := range map[string]string{"replicas": "deployment.replicas", "ports[0].protocol": "deployment.ports[0].protocol"} {
			if want := (oam.Source{Kind: oam.SourceBuiltin, Ref: ref}); got[path] != want {
				t.Errorf("%s = %+v, want %+v\nentries: %v", path, got[path], want, got)
			}
		}
	})

	t.Run("the policy default stands", func(t *testing.T) {
		prov, _ := policyProvenanceFor(t, &defaultsPolicy{replicas: i32(3)}, comp)
		want := oam.Source{Kind: oam.SourcePolicy, Ref: "DefaultReplicas"}
		if got := entriesOf(prov, member)["replicas"]; got != want {
			t.Errorf("replicas = %+v, want %+v", got, want)
		}
	})
}

// A port protocol the author wrote is never reported as a builtin fill; the one
// left out is. Run on a worker's sidecar, a reshaped container entry that still
// takes a `ports` list (the raw kinds fill no protocol: go-kure/launcher#1629).
func TestParseFills_AuthoredPortProtocolIsNotBuiltin(t *testing.T) {
	comp := oam.Component{Name: "app", Type: "worker", Properties: map[string]any{
		"image": "ghcr.io/org/app:v1",
		"sidecars": []any{map[string]any{
			"name": "proxy", "image": "ghcr.io/org/proxy:v1",
			"ports": []any{map[string]any{"containerPort": 8080}, map[string]any{"containerPort": 9090, "protocol": "UDP"}},
		}},
	}}
	prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, comp)
	got := entriesOf(prov, owner{component: "app", member: "role-deployment"})
	if s := got["sidecars[0].ports[0].protocol"]; s.Kind != oam.SourceBuiltin {
		t.Errorf("control: sidecars[0].ports[0].protocol = %+v, want a builtin fill\nentries: %v", s, got)
	}
	if s, ok := got["sidecars[0].ports[1].protocol"]; ok && s.Kind == oam.SourceBuiltin {
		t.Errorf("sidecars[0].ports[1].protocol is reported as built in though the author wrote UDP: %+v", s)
	}
}

// The raw deployment reports no fill at all, with or without a policy default.
func TestParseFills_RawDeploymentReportsNone(t *testing.T) {
	for name, p := range map[string]oam.Policy{"no policy": &defaultsPolicy{}, "a policy with defaults": &defaultsPolicy{
		replicas: i32(3), cpuRequest: "250m", memoryRequest: "256Mi", cpuLimit: "500m", memoryLimit: "512Mi"}} {
		t.Run(name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, p, rawDeployment("app", map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "image": rawImage, "ports": []any{map[string]any{"containerPort": 8080}}}},
			}}}))
			for _, e := range prov.Entries {
				if e.Source.Kind == oam.SourceBuiltin || e.Source.Kind == oam.SourcePolicy {
					t.Errorf("the raw deployment reports a fill: %+v", e)
				}
			}
			if len(entriesOf(prov, owner{component: "app"})) == 0 {
				t.Errorf("control: the report holds no entry of the component, so an empty read proves nothing: %v", prov.Entries)
			}
		})
	}
}

// A lowered type reports on its member: the deployment the webservice rule emits
// is a deployment the handler parsed, so its fills are the member's own, true at
// the member's path. The type-level path is a separate question (go-kure/launcher#1580,
// the upper-level types), so nothing is reported on the component itself.
func TestParseFills_WebserviceReportsOnItsDeploymentMember(t *testing.T) {
	comp := oam.Component{Name: "app", Type: "webservice", Properties: map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}}
	prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, comp)

	member := entriesOf(prov, owner{component: "app", member: "role-deployment"})
	if got, want := member["replicas"], (oam.Source{Kind: oam.SourceBuiltin, Ref: "deployment.replicas"}); got != want {
		t.Errorf("member replicas = %+v, want %+v\nentries: %v", got, want, member)
	}
	if got := entriesOf(prov, owner{component: "app"})["replicas"]; got.Kind == oam.SourceBuiltin {
		t.Errorf("the component's own replicas path is reported as built in: %+v", got)
	}
}
