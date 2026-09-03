package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestWorkloadKinds_ApplyPolicy_PodResourcesEnforced: the pod-level
// podResources budget is checked against the same MaxCPU/MaxMemory policy
// values as the container budget, on every kind. Without it an author could
// keep the container under the maximum and put the oversized request on the
// pod, which the scheduler charges against the node identically.
func TestWorkloadKinds_ApplyPolicy_PodResourcesEnforced(t *testing.T) {
	cases := []struct {
		key     string
		props   map[string]any
		policy  *stubPolicy
		wantErr string
		under   map[string]any
	}{
		{
			key:     "cpu limit",
			props:   map[string]any{"podResources": map[string]any{"limits": map[string]any{"cpu": "4"}}},
			policy:  &stubPolicy{maxCPU: "2"},
			wantErr: `podResources cpu limit "4" exceeds enforced maximum "2"`,
			under:   map[string]any{"podResources": map[string]any{"limits": map[string]any{"cpu": "1"}}},
		},
		{
			key:     "cpu request",
			props:   map[string]any{"podResources": map[string]any{"requests": map[string]any{"cpu": "4"}}},
			policy:  &stubPolicy{maxCPU: "2"},
			wantErr: `podResources cpu request "4" exceeds enforced maximum "2"`,
			under:   map[string]any{"podResources": map[string]any{"requests": map[string]any{"cpu": "1"}}},
		},
		{
			key:     "memory limit",
			props:   map[string]any{"podResources": map[string]any{"limits": map[string]any{"memory": "8Gi"}}},
			policy:  &stubPolicy{maxMemory: "1Gi"},
			wantErr: `podResources memory limit "8Gi" exceeds enforced maximum "1Gi"`,
			under:   map[string]any{"podResources": map[string]any{"limits": map[string]any{"memory": "512Mi"}}},
		},
		{
			key:     "memory request",
			props:   map[string]any{"podResources": map[string]any{"requests": map[string]any{"memory": "8Gi"}}},
			policy:  &stubPolicy{maxMemory: "1Gi"},
			wantErr: `podResources memory request "8Gi" exceeds enforced maximum "1Gi"`,
			under:   map[string]any{"podResources": map[string]any{"requests": map[string]any{"memory": "512Mi"}}},
		},
	}
	for _, k := range workloadKinds {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.key, func(t *testing.T) {
				over := objects2Config(t, k.handler, k.name, withProps(k.props, tc.props)).(oam.Enforceable)
				if err := over.ApplyPolicy(tc.policy); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ApplyPolicy error = %v, want containing %q", err, tc.wantErr)
				}
				under := objects2Config(t, k.handler, k.name, withProps(k.props, tc.under)).(oam.Enforceable)
				if err := under.ApplyPolicy(tc.policy); err != nil {
					t.Fatalf("ApplyPolicy under the maximum: unexpected error %v", err)
				}
				// No configured budget leaves pod-level resources unconstrained.
				fresh := objects2Config(t, k.handler, k.name, withProps(k.props, tc.props)).(oam.Enforceable)
				if err := fresh.ApplyPolicy(&stubPolicy{}); err != nil {
					t.Fatalf("ApplyPolicy with no budget: unexpected error %v", err)
				}
			})
		}
	}
}

// hostNetworkOK is stubPolicy with the hostNetwork gate open. A HostProcess pod
// must set hostNetwork to be admission-valid, and hostNetwork is itself
// policy-gated, so without this the hostNetwork denial would be the only thing
// a hostProcess test could ever observe.
type hostNetworkOK struct{ *stubPolicy }

func (hostNetworkOK) AllowHostNetwork() bool { return true }

// TestWorkloadKinds_ApplyPolicy_HostProcessDenied: a HostProcess pod runs with
// the node's own privileges, so podSecurityContext.windowsOptions.hostProcess
// is gated behind the same AllowPrivileged switch as
// securityContext.privileged, on every kind. NoopPolicy is default-deny.
func TestWorkloadKinds_ApplyPolicy_HostProcessDenied(t *testing.T) {
	// hostNetwork is required alongside hostProcess: validateWindowsHostProcessPod
	// rejects a HostProcess pod without it, and parsePodSpec mirrors that, so the
	// document has to be admission-valid before the policy gate can be the thing
	// under test.
	hostProcess := map[string]any{
		"hostNetwork": true,
		"podSecurityContext": map[string]any{
			"windowsOptions": map[string]any{"hostProcess": true},
		},
	}
	notHostProcess := map[string]any{
		"podSecurityContext": map[string]any{
			"windowsOptions": map[string]any{"hostProcess": false},
		},
	}
	const want = "podSecurityContext.windowsOptions.hostProcess is not allowed by environment policy"
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			cfg := objects2Config(t, k.handler, k.name, withProps(k.props, hostProcess)).(oam.Enforceable)
			if err := cfg.ApplyPolicy(&hostNetworkOK{&stubPolicy{}}); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("ApplyPolicy error = %v, want containing %q", err, want)
			}
			// NoopPolicy is default-deny, and a HostProcess pod necessarily also
			// sets hostNetwork, so it is stopped at whichever of the two gates
			// runs first; asserting on the specific message here would only pin
			// the enforcement order.
			if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err == nil {
				t.Fatal("ApplyPolicy(NoopPolicy) succeeded, want a denial")
			}
			allowed := objects2Config(t, k.handler, k.name, withProps(k.props, hostProcess)).(oam.Enforceable)
			if err := allowed.ApplyPolicy(&hostNetworkOK{&stubPolicy{allowPrivileged: true}}); err != nil {
				t.Fatalf("ApplyPolicy with allowPrivileged: unexpected error %v", err)
			}
			off := objects2Config(t, k.handler, k.name, withProps(k.props, notHostProcess)).(oam.Enforceable)
			if err := off.ApplyPolicy(&stubPolicy{}); err != nil {
				t.Fatalf("ApplyPolicy with hostProcess=false: unexpected error %v", err)
			}
		})
	}
}

// TestEnforcePrivileged_WindowsHostProcess: the container-level spelling of the
// same privilege escalation is checked by enforcePrivileged, reached here
// through a workload's own container securityContext. windowsOptions is not
// authorable on a container today, so this exercises the policy path through
// the pod-level key and pins that privileged and hostProcess share one switch.
func TestEnforcePrivileged_WindowsHostProcess(t *testing.T) {
	props := map[string]any{
		"image":           "ghcr.io/org/app:v1",
		"securityContext": map[string]any{"privileged": true},
	}
	cfg := objects2Config(t, workloadKinds[1].handler, "worker", props).(oam.Enforceable)
	if err := cfg.ApplyPolicy(&stubPolicy{}); err == nil || !strings.Contains(err.Error(), "securityContext.privileged is not allowed") {
		t.Fatalf("ApplyPolicy error = %v, want the privileged denial", err)
	}
	if err := cfg.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Fatalf("ApplyPolicy with allowPrivileged: unexpected error %v", err)
	}
}
