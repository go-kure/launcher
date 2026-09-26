package components

import (
	"fmt"
	"testing"
)

// A typed-nil ELEMENT (map[string]any(nil) inside an []any) asserts as an object
// with ok=true, so parseObjectList used to hand it to its caller as an empty
// object where an untyped null element is refused. For tolerations that empty
// object is an Exists toleration with no key, which tolerates every taint
// (go-kure/launcher#465). Both shapes now take the same refusal.
func TestParseObjectList_TypedNilElementRefusedLikeUntyped(t *testing.T) {
	run := func(elem any) error {
		_, _, err := parseObjectList(map[string]any{"imagePullSecrets": []any{elem}}, "imagePullSecrets")
		return err
	}
	untyped, typed := run(nil), run(map[string]any(nil))
	if untyped == nil {
		t.Fatal("untyped null element: want an error, got nil")
	}
	if fmt.Sprint(typed) != fmt.Sprint(untyped) {
		t.Fatalf("typed-nil element error = %v, untyped null element error = %v", typed, untyped)
	}
}

func TestParseTolerations_TypedNilElementDoesNotTolerateEverything(t *testing.T) {
	tols, err := parseTolerations(map[string]any{"tolerations": []any{map[string]any(nil)}})
	if err == nil {
		t.Fatalf("typed-nil toleration element: want an error, got tolerations %+v", tols)
	}
}
