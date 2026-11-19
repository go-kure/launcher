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

func TestParseFills_DeploymentReportsBuiltinAndPolicyOncePerPath(t *testing.T) {
	props := map[string]any{
		"image": "ghcr.io/org/app:v1",
		"ports": []any{map[string]any{"containerPort": 8080}, map[string]any{"containerPort": 9090, "protocol": "UDP"}},
	}
	comp := oam.Component{Name: "app", Type: "deployment", Properties: props}

	t.Run("no policy default", func(t *testing.T) {
		prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, comp)
		got := entriesOf(prov, owner{component: "app"})
		for path, ref := range map[string]string{"replicas": "deployment.replicas", "ports[0].protocol": "deployment.ports[0].protocol"} {
			if want := (oam.Source{Kind: oam.SourceBuiltin, Ref: ref}); got[path] != want {
				t.Errorf("%s = %+v, want %+v\nentries: %v", path, got[path], want, got)
			}
		}
		if s, ok := got["ports[1].protocol"]; ok && s.Kind == oam.SourceBuiltin {
			t.Errorf("ports[1].protocol is reported as built in though the author wrote UDP: %+v", s)
		}
	})

	t.Run("the policy default stands", func(t *testing.T) {
		prov, _ := policyProvenanceFor(t, &defaultsPolicy{replicas: i32(3)}, comp)
		want := oam.Source{Kind: oam.SourcePolicy, Ref: "DefaultReplicas"}
		if got := entriesOf(prov, owner{component: "app"})["replicas"]; got != want {
			t.Errorf("replicas = %+v, want %+v", got, want)
		}
	})
}

// A job reports its restart policy, and a cronjob that and its history limits.
func TestParseFills_JobKinds(t *testing.T) {
	cases := []struct {
		name  string
		comp  oam.Component
		paths []string
	}{
		{"job", oam.Component{Name: "j", Type: "job", Properties: map[string]any{"image": "ghcr.io/org/app:v1"}}, []string{"restartPolicy"}},
		{"cronjob", oam.Component{Name: "j", Type: "cronjob", Properties: map[string]any{"image": "ghcr.io/org/app:v1", "schedule": "0 2 * * *"}},
			[]string{"restartPolicy", "successfulJobsHistoryLimit", "failedJobsHistoryLimit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, tc.comp)
			got := entriesOf(prov, owner{component: "j"})
			for _, p := range tc.paths {
				if got[p].Kind != oam.SourceBuiltin {
					t.Errorf("%s = %+v, want a built-in fill\nentries: %v", p, got[p], got)
				}
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
