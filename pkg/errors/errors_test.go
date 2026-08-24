package errors

import (
	"errors"
	"testing"
)

func TestPatchErrorError(t *testing.T) {
	cause := errors.New("boom")

	tests := []struct {
		name string
		err  *PatchError
		want string
	}{
		{
			name: "operation only",
			err:  NewPatchError("replace", "", "", "", nil),
			want: `patch "replace" failed`,
		},
		{
			name: "operation and resource",
			err:  NewPatchError("delete", "", "deployment/web", "", nil),
			want: `patch "delete" failed on "deployment/web"`,
		},
		{
			name: "operation and path",
			err:  NewPatchError("resolve", "spec.containers[0].image", "", "", nil),
			want: `patch "resolve" failed at "spec.containers[0].image"`,
		},
		{
			name: "operation, resource, path and reason",
			err:  NewPatchError("replace", "spec.replicas", "deployment/web", "value out of range", nil),
			want: `patch "replace" failed on "deployment/web" at "spec.replicas": value out of range`,
		},
		{
			name: "full fields including cause",
			err:  NewPatchError("replace", "spec.replicas", "deployment/web", "value out of range", cause),
			want: `patch "replace" failed on "deployment/web" at "spec.replicas": value out of range: boom`,
		},
		{
			name: "cause only, no reason",
			err:  NewPatchError("resolve", "", "", "", cause),
			want: `patch "resolve" failed: boom`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPatchErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := NewPatchError("replace", "spec.replicas", "deployment/web", "value out of range", cause)

	if got := err.Unwrap(); !errors.Is(got, cause) {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}

	var pe *PatchError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(err, &pe) = false, want true")
	}
	if pe != err {
		t.Errorf("errors.As unwrapped to %v, want %v", pe, err)
	}
}

func TestPatchErrorUnwrapNilCause(t *testing.T) {
	err := NewPatchError("replace", "", "", "no cause", nil)
	if got := err.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}
