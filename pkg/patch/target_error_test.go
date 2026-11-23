package patch

import (
	stderrors "errors"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/launcher/pkg/errors"
)

// TestTargetNotFoundError pins that each way a patch target can match no resource
// returns a *TargetNotFoundError, and that the two ways a target is ambiguous do
// not. The texts of these errors are pinned by TestLoaderErrorText.
func TestTargetNotFoundError(t *testing.T) {
	resources := []*unstructured.Unstructured{
		loaderTestResource("Deployment", "app", ""),
		loaderTestResource("Service", "app", ""),
		loaderTestResource("Deployment", "web", "prod"),
		loaderTestResource("Deployment", "web", "stage"),
	}
	patchOp := PatchOp{Op: "replace", Path: "a", Value: 1}

	rows := []struct {
		name         string
		call         func() error
		wantTarget   string
		wantExplicit bool
		wantNotFound bool
	}{
		{name: "namespaced target", call: func() error {
			_, err := ResolveTargetKey(resources, "stage/deployment.none")
			return err
		}, wantTarget: "stage/deployment.none", wantNotFound: true},
		{name: "short name", call: func() error {
			_, err := ResolveTargetKey(resources, "none")
			return err
		}, wantTarget: "none", wantNotFound: true},
		{name: "strategic patch, through its wrap", call: resolvePatchSpec(resources,
			PatchSpec{Target: "none", Strategic: &StrategicPatch{Patch: map[string]any{"a": "b"}}}),
			wantTarget: "none", wantNotFound: true},
		{name: "explicit target", call: resolvePatchSpec(resources, PatchSpec{Target: "none", Patch: patchOp}),
			wantTarget: "none", wantExplicit: true, wantNotFound: true},
		{name: "kind and name ambiguous", call: func() error {
			_, err := ResolveTargetKey(resources, "deployment.web")
			return err
		}},
		{name: "explicit target ambiguous", call: resolvePatchSpec(resources, PatchSpec{Target: "app", Patch: patchOp})},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := row.call()
			if err == nil {
				t.Fatal("no error")
			}
			var notFound *TargetNotFoundError
			if got := errors.As(err, &notFound); got != row.wantNotFound {
				t.Fatalf("errors.As(*TargetNotFoundError) = %v, want %v, for %q", got, row.wantNotFound, err)
			}
			if got := skipsPatchFile(err); got != row.wantNotFound {
				t.Errorf("skipsPatchFile = %v, want %v", got, row.wantNotFound)
			}
			if !row.wantNotFound {
				return
			}
			if notFound.Target != row.wantTarget || notFound.Explicit != row.wantExplicit {
				t.Errorf("got Target %q Explicit %v, want %q %v", notFound.Target, notFound.Explicit, row.wantTarget, row.wantExplicit)
			}
		})
	}
}

// TestSkipsPatchFile pins that the decision to skip a patch file reads the type of
// the error and never its text: a plain error that says the same words does not
// skip a file, and a typed error does whatever it is wrapped in.
func TestSkipsPatchFile(t *testing.T) {
	typed := &TargetNotFoundError{Target: "none", Explicit: true}
	rows := []struct {
		name string
		err  error
		want bool
	}{
		{name: "typed", err: typed, want: true},
		{name: "typed and wrapped", err: errors.Wrapf(typed, "failed to create patchable set"), want: true},
		{name: "typed, wrapped with fmt", err: fmt.Errorf("context: %w", typed), want: true},
		{name: "same words, plain error", err: stderrors.New(typed.Error())},
		{name: "same words as the short form", err: stderrors.New(`target "none" not found in base resources`)},
		{name: "another error", err: stderrors.New("boom")},
		{name: "nil"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := skipsPatchFile(row.err); got != row.want {
				t.Errorf("skipsPatchFile(%v) = %v, want %v", row.err, got, row.want)
			}
		})
	}
}
