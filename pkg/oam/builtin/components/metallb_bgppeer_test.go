package components_test

import (
	"strings"
	"testing"

	metallbv1beta2 "go.universe.tf/metallb/api/v1beta2"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestMetalLBBGPPeerKind_SessionPassword: a session password written into the
// peer is refused under a policy that forbids explicit secrets, and the refusal
// names the field and its replacement without the value. A policy that allows
// explicit secrets, one that does not answer the question and none passed build
// it, with the password in the object. A password taken from a Secret, an empty
// one and none are built under every one of them, and no password is written.
func TestMetalLBBGPPeerKind_SessionPassword(t *testing.T) {
	const refusal = "password: holds the BGP session password in the object, and the environment policy forbids explicit secrets; " +
		"name a Secret created out of band in passwordSecret instead"
	fromSecret := map[string]any{"name": "upstream-session", "namespace": "metallb-system"}
	policies := map[string]oam.Policy{
		"forbidding":       esForbidding(),
		"allowing":         esPolicy{stubPolicy: &stubPolicy{}, allow: true},
		"no answer":        &stubPolicy{},
		"no policy passed": nil,
	}
	for name, tc := range map[string]struct {
		props    map[string]any
		refused  bool   // under the forbidding policy
		password string // in the object, where the component builds
	}{
		"a password":                    {metallbPeerOf("password", esSentinel), true, esSentinel},
		"a password beside a reference": {map[string]any{"myASN": 64512, "password": esSentinel, "passwordSecret": fromSecret}, true, esSentinel},
		"a password from a Secret":      {metallbPeerOf("passwordSecret", fromSecret), false, ""},
		// The type holds the password as a string, which is empty on a peer that
		// authors none: an empty one is no password.
		"an empty password": {metallbPeerOf("password", ""), false, ""},
		"no password":       {map[string]any{"myASN": 64512}, false, ""},
	} {
		for policyName, policy := range policies {
			t.Run(name+"/"+policyName, func(t *testing.T) {
				objs, err := pvTransform("metallb-bgppeer", &components.MetalLBBGPPeerHandler{}, tc.props, policy)
				if tc.refused && policyName == "forbidding" {
					htWantViolation(t, err, `component "web": `+refusal)
					if strings.Contains(err.Error(), esSentinel) {
						t.Errorf("the refusal carries the value: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 {
					t.Fatalf("generated %d objects, want one", len(objs))
				}
				peer, ok := objs[0].(*metallbv1beta2.BGPPeer)
				if !ok {
					t.Fatalf("generated a %T, want the BGPPeer", objs[0])
				}
				if peer.Spec.Password != tc.password {
					t.Errorf("password = %q, want %q", peer.Spec.Password, tc.password)
				}
			})
		}
	}
}
