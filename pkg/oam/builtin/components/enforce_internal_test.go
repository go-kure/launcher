package components

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestEnforceContainerCapabilities covers enforceContainerCapabilities'
// default-allow, forbidden-list-first semantics (go-kure/launcher#305 —
// Design §2 in the implementation plan): normalisation of both authored and
// policy-list values, "forbidden always wins", drop-is-never-checked, and the
// ALL wildcard special-case on both the authored side (cases 8b-8d) and the
// forbidden side (cases 9a-9b) — flagged by automated PR review on
// go-kure/launcher#314 as originally covering only the authored side.
func TestEnforceContainerCapabilities(t *testing.T) {
	cases := []struct {
		name      string
		add       []corev1.Capability
		drop      []corev1.Capability
		allowed   []string
		forbidden []string
		wantErr   bool
	}{
		{
			name:    "1: default NoopPolicy stance - empty allowed and forbidden - any capability passes",
			add:     []corev1.Capability{"NET_ADMIN"},
			wantErr: false,
		},
		{
			name:    "2a: non-empty allowed - a capability in it is accepted",
			add:     []corev1.Capability{"NET_ADMIN"},
			allowed: []string{"NET_ADMIN"},
			wantErr: false,
		},
		{
			name:    "2b: non-empty allowed - a capability not in it is rejected",
			add:     []corev1.Capability{"NET_ADMIN"},
			allowed: []string{"SYS_TIME"},
			wantErr: true,
		},
		{
			name:      "3: forbidden capability rejected even when also allowlisted - forbidden wins",
			add:       []corev1.Capability{"NET_ADMIN"},
			allowed:   []string{"NET_ADMIN"},
			forbidden: []string{"NET_ADMIN"},
			wantErr:   true,
		},
		{
			name:      "4: CAP_NET_ADMIN rejected by a forbid-list entry spelled NET_ADMIN - authored-side normalisation",
			add:       []corev1.Capability{"CAP_NET_ADMIN"},
			forbidden: []string{"NET_ADMIN"},
			wantErr:   true,
		},
		{
			name:      "5: forbid-list entry spelled non-canonically still rejects authored NET_ADMIN - policy-side normalisation",
			add:       []corev1.Capability{"NET_ADMIN"},
			forbidden: []string{"cap_net_admin"},
			wantErr:   true,
		},
		{
			name:    "6: allow-list entry spelled non-canonically still accepts authored NET_BIND_SERVICE - allow-side normalisation",
			add:     []corev1.Capability{"NET_BIND_SERVICE"},
			allowed: []string{"cap_net_bind_service"},
			wantErr: false,
		},
		{
			name:      "7: drop ALL alone passes regardless of policy configuration - drop is never checked",
			drop:      []corev1.Capability{"ALL"},
			allowed:   []string{"NET_ADMIN"},
			forbidden: []string{"ALL"},
			wantErr:   false,
		},
		{
			name:    "8a: add ALL passes under the default empty allowlist/forbidden list",
			add:     []corev1.Capability{"ALL"},
			wantErr: false,
		},
		{
			name:      "8b: add ALL rejected whenever forbidden is non-empty, even without literal ALL",
			add:       []corev1.Capability{"ALL"},
			forbidden: []string{"NET_ADMIN"},
			wantErr:   true,
		},
		{
			name:    "8c: add ALL rejected once a non-empty allowlist omits it",
			add:     []corev1.Capability{"ALL"},
			allowed: []string{"NET_ADMIN"},
			wantErr: true,
		},
		{
			name:    "8d: add ALL accepted once a non-empty allowlist explicitly includes the literal ALL entry",
			add:     []corev1.Capability{"ALL"},
			allowed: []string{"NET_ADMIN", "ALL"},
			wantErr: false,
		},
		{
			name:      "9a: forbidden literally containing ALL rejects every authored capability, not just literal ALL",
			add:       []corev1.Capability{"NET_ADMIN"},
			forbidden: []string{"ALL"},
			wantErr:   true,
		},
		{
			name:      "9b: forbidden literally containing ALL still rejects an authored ALL - no regression at the boundary between the two ALL special-cases",
			add:       []corev1.Capability{"ALL"},
			forbidden: []string{"ALL"},
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sc *corev1.SecurityContext
			if len(tc.add) > 0 || len(tc.drop) > 0 {
				sc = &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{
						Add:  tc.add,
						Drop: tc.drop,
					},
				}
			}
			err := enforceContainerCapabilities(sc, tc.allowed, tc.forbidden)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

// TestEnforceContainerCapabilities_NilSecurityContext covers the two real nil
// cases: the parser leaves SecurityContext or Capabilities nil unless add or
// drop has entries (common.go's parseSecurityContext).
func TestEnforceContainerCapabilities_NilSecurityContext(t *testing.T) {
	if err := enforceContainerCapabilities(nil, nil, []string{"NET_ADMIN"}); err != nil {
		t.Errorf("expected no error for nil SecurityContext, got %v", err)
	}
	sc := &corev1.SecurityContext{}
	if err := enforceContainerCapabilities(sc, nil, []string{"NET_ADMIN"}); err != nil {
		t.Errorf("expected no error for nil Capabilities, got %v", err)
	}
}
