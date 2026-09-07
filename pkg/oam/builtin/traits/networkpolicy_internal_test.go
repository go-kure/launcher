package traits

import "testing"

// TestParseNPPeer_NamespaceSelectorPresenceCases pins what parseNPPeer does with an
// EMPTY namespaceSelector versus an ABSENT one. The two are not interchangeable to
// Kubernetes: an empty metav1.LabelSelector matches every namespace, while a nil one
// leaves the peer scoped to the policy's own namespace. A refactor that collapsed them
// would silently widen or narrow a NetworkPolicy's traffic scope — a security object
// changing meaning as a side effect.
//
// Nothing pinned this before. Every other NamespaceSelector assertion in the repo
// (netpol_synthesis_test.go, netpol_egress_synthesis_test.go,
// netpol_endpoint_synthesis_test.go, networkpolicy_auto_test.go) uses populated
// matchLabels, and those all reach the field through the synthesis path, which builds
// selectors directly in Go rather than through this parser.
func TestParseNPPeer_NamespaceSelectorPresenceCases(t *testing.T) {
	t.Run("empty object selects all namespaces", func(t *testing.T) {
		peer, err := parseNPPeer(map[string]any{
			"namespaceSelector": map[string]any{},
		}, "from[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if peer.NamespaceSelector == nil {
			t.Fatal("namespaceSelector: {} produced a nil selector; an empty selector means ALL namespaces and a nil one means the policy's own, so this silently narrows the policy")
		}
		if got := len(peer.NamespaceSelector.MatchLabels); got != 0 {
			t.Errorf("namespaceSelector: {} produced %d matchLabels, want 0", got)
		}
	})

	t.Run("absent key leaves the selector nil", func(t *testing.T) {
		peer, err := parseNPPeer(map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app": "client"}},
		}, "from[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if peer.NamespaceSelector != nil {
			t.Errorf("an absent namespaceSelector produced %v, want nil — a non-nil empty selector would widen the peer to every namespace", peer.NamespaceSelector)
		}
	})

	t.Run("authored null is absent", func(t *testing.T) {
		// A YAML `namespaceSelector:` with no value decodes to an UNTYPED nil, which
		// fails the map type assertion in parseNPPeer and leaves the selector nil.
		// That is the contract's "a null is absent" holding on this path.
		peer, err := parseNPPeer(map[string]any{
			"namespaceSelector": nil,
		}, "from[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if peer.NamespaceSelector != nil {
			t.Errorf("namespaceSelector: null produced %v, want nil", peer.NamespaceSelector)
		}
	})

	t.Run("typed nil diverges from authored null KNOWN", func(t *testing.T) {
		// KNOWN DIVERGENCE, pinned deliberately rather than fixed here; tracked as
		// go-kure/launcher#430, which carries the other half of the finding — the same
		// key read by parseSchedulingSelector (components/scheduling.go:508) through
		// optionalObject, where a null IS omission.
		//
		// map[string]any(nil) is a TYPED nil. It SATISFIES the .(map[string]any)
		// assertion (ok=true, nil map), falls through the matchLabels lookup, and
		// yields an empty selector — ALL namespaces — where the untyped nil above
		// yields absence. Same "null", opposite scope, decided by a Go type a document
		// cannot express.
		//
		// It is unreachable today: only Go can construct a typed nil, and no production
		// code emits a networkpolicy trait ("networkpolicy" appears only at CanHandle
		// and in validate.go's known-type list). This test exists so the divergence is
		// visible and so closing it is a deliberate edit to this assertion rather than
		// a silent behaviour change nothing notices.
		peer, err := parseNPPeer(map[string]any{
			"namespaceSelector": map[string]any(nil),
		}, "from[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if peer.NamespaceSelector == nil {
			t.Skip("typed-nil namespaceSelector now reads as absent — the divergence this test pinned (go-kure/launcher#430) is closed; delete this subtest and fold the case into 'authored null is absent'")
		}
		if got := len(peer.NamespaceSelector.MatchLabels); got != 0 {
			t.Errorf("typed-nil namespaceSelector produced %d matchLabels, want 0", got)
		}
	})
}
