package traits

import "testing"

// A NetworkPolicy peer is the one place in this package where "absent" and
// "present but empty" are opposite security answers rather than a cosmetic
// difference: in networking.k8s.io/v1 an empty metav1.LabelSelector matches
// EVERY namespace (or every pod), while a nil one leaves the peer scoped to the
// policy's own namespace. go-kure/launcher#430 is that a TYPED nil — what an
// uninitialized Go map in a lowering rule produces — used to satisfy
// parseNPPeer's bare `.(map[string]any)` assertion with ok=true and yield the
// EMPTY selector, so one value read as the widest possible scope here and as
// absence in the repo's other metav1.LabelSelector reader (parseLabelSelector in
// components/volumeclaim_spec.go, via optionalObject).
//
// These tests pin both halves. The null rows are the fix; the empty-object rows
// are the control that the fix did not simply collapse empty into absent, which
// would silently NARROW every policy that authors `namespaceSelector: {}` on
// purpose. Without those rows a parser that returned nil for every input would
// pass this file.
//
// CLEANUP OWED, if networkpolicy_internal_test.go is present beside this file.
// go-kure/launcher#413 adds that file, whose
// TestParseNPPeer_NamespaceSelectorPresenceCases pinned this divergence as KNOWN
// while it was open. Its `typed nil diverges from authored null KNOWN` subtest is
// written to t.Skip once the divergence is closed, so it cannot fail either way —
// which is also why nothing will ever prompt its removal. Delete that subtest and
// fold its case into the file's `authored null is absent` subtest; the typed-nil
// rows here already cover it. The two files are independent otherwise and land in
// either order.

func TestParseNPPeer_NullSelectorIsAbsent(t *testing.T) {
	// Both nil shapes must read as absence, and they must agree with each other:
	// an untyped nil is what a YAML `namespaceSelector:` with no value decodes
	// to, a typed nil is what Go construction produces, and no document can
	// express the difference.
	nullShapes := map[string]any{
		"untyped nil": nil,
		"typed nil":   map[string]any(nil),
	}

	for _, key := range []string{"podSelector", "namespaceSelector"} {
		for shape, value := range nullShapes {
			t.Run(key+"/"+shape, func(t *testing.T) {
				peer, err := parseNPPeer(map[string]any{key: value}, "from[0]")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				got := peer.PodSelector
				if key == "namespaceSelector" {
					got = peer.NamespaceSelector
				}
				if got != nil {
					t.Errorf("%s: %s produced %v, want nil — a non-nil empty selector widens the peer to every %s",
						key, shape, got, map[string]string{"podSelector": "pod", "namespaceSelector": "namespace"}[key])
				}
			})
		}
	}
}

func TestParseNPPeer_EmptySelectorStaysPresent(t *testing.T) {
	// The control for the test above. An authored `{}` is a real, expressible
	// value meaning "match everything", and it must survive the null handling.
	for _, key := range []string{"podSelector", "namespaceSelector"} {
		t.Run(key, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{key: map[string]any{}}, "from[0]")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := peer.PodSelector
			if key == "namespaceSelector" {
				got = peer.NamespaceSelector
			}
			if got == nil {
				t.Fatalf("%s: {} produced a nil selector; an empty selector means ALL, a nil one means the policy's own namespace, so this silently narrows the policy", key)
			}
			if n := len(got.MatchLabels); n != 0 {
				t.Errorf("%s: {} produced %d matchLabels, want 0", key, n)
			}
		})
	}
}

func TestParseNPPeer_PopulatedSelectorUnaffected(t *testing.T) {
	peer, err := parseNPPeer(map[string]any{
		"namespaceSelector": map[string]any{"matchLabels": map[string]any{"env": "prod"}},
	}, "from[0]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if peer.NamespaceSelector == nil {
		t.Fatal("populated namespaceSelector produced a nil selector")
	}
	if got := peer.NamespaceSelector.MatchLabels["env"]; got != "prod" {
		t.Errorf("matchLabels[env] = %q, want %q", got, "prod")
	}
}

func TestParseNPPeer_NullMatchLabelsKeepsSelector(t *testing.T) {
	// A null nested under a present selector is absence too, but here absence and
	// empty coincide: the selector itself stays present with no labels, which is
	// what an authored `matchLabels: {}` already produced. Pinned so the nested
	// call site is not "fixed" later into dropping the selector.
	for shape, value := range map[string]any{"untyped nil": nil, "typed nil": map[string]any(nil)} {
		t.Run(shape, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{
				"namespaceSelector": map[string]any{"matchLabels": value},
			}, "from[0]")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if peer.NamespaceSelector == nil {
				t.Fatal("a null matchLabels dropped the whole selector; only the matchLabels key was null")
			}
			if n := len(peer.NamespaceSelector.MatchLabels); n != 0 {
				t.Errorf("produced %d matchLabels, want 0", n)
			}
		})
	}
}

func TestParseNPPeer_NullIPBlockIsAbsentNotAnError(t *testing.T) {
	// A typed-nil ipBlock used to satisfy the assertion, reach the required-cidr
	// check and fail it — so the same "null" was absence when untyped and an
	// error when typed. Under the contract both are absence, and absence of an
	// optional key is not an error.
	for shape, value := range map[string]any{"untyped nil": nil, "typed nil": map[string]any(nil)} {
		t.Run(shape, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{"ipBlock": value}, "from[0]")
			if err != nil {
				t.Fatalf("a null ipBlock must read as absent, got error: %v", err)
			}
			if peer.IPBlock != nil {
				t.Errorf("null ipBlock produced %v, want nil", peer.IPBlock)
			}
		})
	}
}

func TestParseNPPeer_PresentIPBlockStillRequiresCIDR(t *testing.T) {
	// The control for the test above: null became absence, but a real, present
	// ipBlock object must still be rejected without a cidr.
	if _, err := parseNPPeer(map[string]any{"ipBlock": map[string]any{}}, "from[0]"); err == nil {
		t.Fatal("an ipBlock with no cidr must be rejected; the null handling must not have made it absent")
	}
}
