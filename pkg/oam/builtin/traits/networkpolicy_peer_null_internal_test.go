package traits

import (
	"strings"
	"testing"
)

// A NetworkPolicy peer is the one place in this package where "absent" and
// "present but empty" are opposite security answers rather than a cosmetic
// difference: in networking.k8s.io/v1 an empty metav1.LabelSelector matches
// EVERY namespace (or every pod), while a nil one applies no constraint on that
// axis — which alongside a podSelector leaves the peer scoped to the policy's own
// namespace (k8s.io/api networking/v1/types.go:199-222), and with no sibling
// selector at all leaves a peer that names nothing rather than a narrow one.
// go-kure/launcher#430 is that a TYPED nil — what an
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
				t.Fatalf("%s: {} produced a nil selector; an empty selector means ALL, a nil one applies no constraint on that axis, so this silently changes what the peer selects", key)
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

func TestParseNPPeer_NullLabelValueIsRejected(t *testing.T) {
	// One depth below every other test here: the VALUE of a matchLabels entry.
	// The selector is read through the null contract, but the values were then
	// formatted with %v, which turns a null into the literal string "<nil>" — and
	// a typed nil map or slice into "map[]" or "[]". Those are not label values;
	// the document renders and the API server rejects it. Dropping the entry
	// instead would remove an authored constraint and widen the selector, so this
	// is an error.
	for shape, value := range map[string]any{
		"untyped nil":       nil,
		"typed nil map":     map[string]any(nil),
		"typed nil slice":   []any(nil),
		"typed nil pointer": (*string)(nil),
	} {
		t.Run(shape, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{
				"namespaceSelector": map[string]any{"matchLabels": map[string]any{"env": value}},
			}, "from[0]")
			if err == nil {
				t.Fatalf("a null label value must be rejected, got %+v", peer.NamespaceSelector)
			}
			if got, want := err.Error(), `from[0].namespaceSelector.matchLabels: "env" has no value`; got != want {
				t.Errorf("diagnostic = %q, want %q", got, want)
			}
		})
	}
}

func TestParseNPPeer_CompositeLabelValueIsRejected(t *testing.T) {
	// The other half of the null guard above, and the case that guard's own stated
	// reason already covered: %v renders a map as "map[a:1]" and a slice as
	// "[x y]". Those are no more label values than "<nil>" is — the document
	// renders and the API server refuses it one layer from the cause. The
	// empty-map row is the one the null guard cannot reach: an allocated empty map
	// is PRESENT, so IsNullValue says false and %v yields "map[]".
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"populated map", map[string]any{"a": 1}, `from[0].namespaceSelector.matchLabels: "env" must be a string, number or boolean, got map[string]interface {}`},
		{"empty map", map[string]any{}, `from[0].namespaceSelector.matchLabels: "env" must be a string, number or boolean, got map[string]interface {}`},
		{"slice", []any{"x", "y"}, `from[0].namespaceSelector.matchLabels: "env" must be a string, number or boolean, got []interface {}`},
		{"empty slice", []any{}, `from[0].namespaceSelector.matchLabels: "env" must be a string, number or boolean, got []interface {}`},
		{"non-nil pointer", new(string), `from[0].namespaceSelector.matchLabels: "env" must be a string, number or boolean, got *string`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{
				"namespaceSelector": map[string]any{"matchLabels": map[string]any{"env": tc.value}},
			}, "from[0]")
			if err == nil {
				t.Fatalf("a composite label value must be rejected, got %+v", peer.NamespaceSelector)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseNPPeer_NonStringLabelValueStillFormats(t *testing.T) {
	// The control for both value guards: only NULL and COMPOSITE values are
	// rejected. A number or boolean is a perfectly ordinary label value in a YAML
	// document and must keep its existing %v rendering — including every numeric
	// kind a decoder produces, since sigs.k8s.io/yaml routes through JSON and
	// yields float64 where a YAML-native decoder yields int.
	peer, err := parseNPPeer(map[string]any{
		"podSelector": map[string]any{"matchLabels": map[string]any{
			"port":     8080,
			"tls":      true,
			"replicas": float64(3),
			"gen":      int64(7),
			"tier":     "web",
		}},
	}, "from[0]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{
		"port":     "8080",
		"tls":      "true",
		"replicas": "3",
		"gen":      "7",
		"tier":     "web",
	} {
		if got := peer.PodSelector.MatchLabels[key]; got != want {
			t.Errorf("matchLabels[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestParseNPPeer_UnknownSelectorKeyIsRejected(t *testing.T) {
	// The peer's key set is checked by name one level up (validNPPeerKeys); the
	// selector one level down had no equivalent, so any key that is not
	// matchLabels was read, found absent, and left the selector ALLOCATED with no
	// labels — an empty metav1.LabelSelector, which matches EVERY namespace (or
	// every pod). That is the same fail-open widening a wrong-TYPED selector was
	// made an error for; an unrecognized KEY took the same silent path.
	//
	// matchExpressions is the row that matters: it is a real, valid
	// metav1.LabelSelector field this parser does not implement, so a document
	// written against Kubernetes' own schema silently became match-all. An error
	// is the correct answer — a constraint the parser cannot honour must not
	// become the widest possible one.
	//
	// Both selector keys are exercised because one function serves both call
	// sites; asserting that would not prove it.
	for _, key := range []string{"podSelector", "namespaceSelector"} {
		for _, tc := range []struct {
			name     string
			selector map[string]any
			want     string
		}{
			{"matchExpressions", map[string]any{"matchExpressions": []any{}}, `unsupported key "matchExpressions"`},
			{"misspelt matchLabels", map[string]any{"matchLabel": map[string]any{"env": "prod"}}, `unsupported key "matchLabel"`},
			{"beside a real matchLabels", map[string]any{
				"matchLabels":      map[string]any{"env": "prod"},
				"matchExpressions": []any{},
			}, `unsupported key "matchExpressions"`},
		} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				peer, err := parseNPPeer(map[string]any{key: tc.selector}, "from[0]")
				if err == nil {
					t.Fatalf("an unrecognized selector key must be rejected, got %+v", peer)
				}
				if got, want := err.Error(), "from[0]."+key+": "+tc.want; got != want {
					t.Errorf("diagnostic = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestParseNPPeer_UnknownKeyDiagnosticIsDeterministic(t *testing.T) {
	// The rule: when several keys are unrecognized, the diagnostic names the
	// lexicographically first one. Go randomizes map iteration order, so an
	// unsorted loop names a different key from run to run and two CI runs of the
	// same document disagree.
	//
	// The repetition is not the rule — the rule is the exact key asserted below —
	// it is detection power. An unsorted loop over n keys still names the expected
	// one with probability 1/n, so a single pass at n=2 misses the defect half the
	// time; measured, a two-key version of this check caught an unsorted mutant on
	// 0 of 2 subtests in one run. At n=4 over 20 passes the miss probability is
	// 4^-20 per site.
	for _, tc := range []struct {
		name  string
		parse func() error
	}{
		{"peer keys", func() error {
			_, err := parseNPPeer(map[string]any{
				"aardvark": 1, "badger": 2, "coyote": 3, "zebra": 4,
			}, "from[0]")
			return err
		}},
		{"selector keys", func() error {
			_, err := parseNPPeer(map[string]any{"namespaceSelector": map[string]any{
				"aardvark": 1, "badger": 2, "coyote": 3, "zebra": 4,
			}}, "from[0]")
			return err
		}},
		{"ipBlock keys", func() error {
			_, err := parseNPPeer(map[string]any{"ipBlock": map[string]any{
				"aardvark": 1, "badger": 2, "coyote": 3, "zebra": 4,
			}}, "from[0]")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				err := tc.parse()
				if err == nil {
					t.Fatalf("pass %d: unrecognized keys must be rejected", i)
				}
				if got := err.Error(); !strings.Contains(got, `unsupported key "aardvark"`) {
					t.Fatalf("pass %d: diagnostic = %q, want the lexicographically first key", i, got)
				}
			}
		})
	}
}

func TestParseNPPeer_MalformedIPBlockIsRejectedNotDropped(t *testing.T) {
	// The last depth in this peer with bare comma-ok reads. `except` is an
	// EXCLUSION list, so discarding a mistyped or misspelt one renders a block
	// WIDER than the document authored — the same direction of failure as the
	// selector cases above, one key over. A mistyped `cidr` was already an error,
	// but reported as "you forgot it".
	for _, tc := range []struct {
		name    string
		ipBlock map[string]any
		want    string
	}{
		{"wrong name for except", map[string]any{"cidr": "10.0.0.0/8", "exclude": []any{"10.1.0.0/16"}}, `from[0].ipBlock: unsupported key "exclude"`},
		{"string except", map[string]any{"cidr": "10.0.0.0/8", "except": "10.1.0.0/16"}, `from[0].ipBlock.except: expected array, got string`},
		{"object except", map[string]any{"cidr": "10.0.0.0/8", "except": map[string]any{"a": "b"}}, `from[0].ipBlock.except: expected array, got map[string]interface {}`},
		{"numeric cidr", map[string]any{"cidr": 10}, `from[0].ipBlock.cidr: expected string, got int`},
		{"object cidr", map[string]any{"cidr": map[string]any{}}, `from[0].ipBlock.cidr: expected string, got map[string]interface {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{"ipBlock": tc.ipBlock}, "from[0]")
			if err == nil {
				t.Fatalf("a malformed ipBlock must be rejected, got %+v", peer.IPBlock)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseNPPeer_WellFormedIPBlockStillParses(t *testing.T) {
	// The control for the test above: the guards must reject the malformed shapes
	// only. A null `except` is absence, not an error — the same answer the null
	// contract gives everywhere else — and a missing or null `cidr` keeps the
	// required-key diagnostic rather than being folded into the mistyped one.
	t.Run("cidr with except", func(t *testing.T) {
		peer, err := parseNPPeer(map[string]any{
			"ipBlock": map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.1.0.0/16"}},
		}, "from[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if peer.IPBlock.CIDR != "10.0.0.0/8" {
			t.Errorf("cidr = %q, want %q", peer.IPBlock.CIDR, "10.0.0.0/8")
		}
		if len(peer.IPBlock.Except) != 1 || peer.IPBlock.Except[0] != "10.1.0.0/16" {
			t.Errorf("except = %v, want [10.1.0.0/16]", peer.IPBlock.Except)
		}
	})

	for shape, value := range map[string]any{"untyped nil": nil, "typed nil": []any(nil)} {
		t.Run("null except/"+shape, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{
				"ipBlock": map[string]any{"cidr": "10.0.0.0/8", "except": value},
			}, "from[0]")
			if err != nil {
				t.Fatalf("a null 'except' must read as absent, got: %v", err)
			}
			if peer.IPBlock.Except != nil {
				t.Errorf("null except produced %v, want nil", peer.IPBlock.Except)
			}
		})
	}

	for shape, value := range map[string]any{"untyped nil": nil, "typed nil": (*string)(nil)} {
		t.Run("null cidr/"+shape, func(t *testing.T) {
			_, err := parseNPPeer(map[string]any{
				"ipBlock": map[string]any{"cidr": value},
			}, "from[0]")
			if err == nil {
				t.Fatal("a null cidr must be rejected")
			}
			if got, want := err.Error(), "from[0].ipBlock: 'cidr' is required"; got != want {
				t.Errorf("diagnostic = %q, want %q", got, want)
			}
		})
	}
}

func TestParseNPPeer_MalformedSelectorIsRejectedNotDropped(t *testing.T) {
	// The third answer a selector read can give, beside "absent" and "present".
	// A wrong-typed value used to be discarded silently, which for the NESTED case
	// is the same widening this whole file exists to prevent: a string matchLabels
	// left the selector allocated with no labels, and an empty selector matches
	// EVERY namespace. Not a lint: it turns a malformed constraint into the widest
	// possible one, silently, at render time.
	for _, tc := range []struct {
		name  string
		peer  map[string]any
		error string
	}{
		{
			name:  "string namespaceSelector",
			peer:  map[string]any{"namespaceSelector": "prod"},
			error: "from[0].namespaceSelector: expected object, got string",
		},
		{
			name:  "string podSelector",
			peer:  map[string]any{"podSelector": "web"},
			error: "from[0].podSelector: expected object, got string",
		},
		{
			name:  "string matchLabels widens the selector to every namespace",
			peer:  map[string]any{"namespaceSelector": map[string]any{"matchLabels": "prod"}},
			error: "from[0].namespaceSelector.matchLabels: expected object, got string",
		},
		{
			name:  "list ipBlock",
			peer:  map[string]any{"ipBlock": []any{"10.0.0.0/8"}},
			error: "from[0].ipBlock: expected object, got []interface {}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer, err := parseNPPeer(tc.peer, "from[0]")
			if err == nil {
				t.Fatalf("a malformed selector must be rejected, got peer %+v", peer)
			}
			if got := err.Error(); got != tc.error {
				t.Errorf("diagnostic = %q, want %q", got, tc.error)
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

func TestParseNPPeer_NullPeerEnvelopeIsRejected(t *testing.T) {
	// The envelope one level above every test in this file. A typed nil satisfied
	// the bare `.(map[string]any)` assertion with a nil map, which has no keys —
	// so the unknown-key loop found nothing to reject, all three selector reads
	// missed, and the peer was ACCEPTED as an empty one. An untyped nil failed the
	// same assertion and was rejected. The two nil shapes therefore disagreed at
	// the envelope while agreeing inside it, which is the divergence the rest of
	// this file exists to remove.
	for shape, value := range map[string]any{"untyped nil": nil, "typed nil": map[string]any(nil)} {
		t.Run(shape, func(t *testing.T) {
			if _, err := parseNPPeer(value, "from[0]"); err == nil {
				t.Fatal("a null peer must be rejected, not accepted as an empty peer that names no source at all")
			}
		})
	}
}

func TestParseNPPeer_PresentEmptyPeerStillParses(t *testing.T) {
	// The control for the test above, and it is the one that stops the fix from
	// being "reject anything falsy". An authored `- {}` is a present, empty peer:
	// distinct from a null, and this parser has always accepted it. If the null
	// guard were keyed on emptiness rather than on nil-ness, this would break.
	peer, err := parseNPPeer(map[string]any{}, "from[0]")
	if err != nil {
		t.Fatalf("an authored empty peer object must still parse, got: %v", err)
	}
	if peer.PodSelector != nil || peer.NamespaceSelector != nil || peer.IPBlock != nil {
		t.Errorf("an empty peer produced %+v, want all three fields nil", peer)
	}
}
