package oam

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"
)

// refusingConfig is a consumer's own Enforceable: ApplyPolicy returns err.
type refusingConfig struct{ err error }

func (c *refusingConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (c *refusingConfig) ApplyPolicy(_ Policy) error                              { return c.err }

// refusingHandler builds a refusingConfig for the component type widget.
type refusingHandler struct{ err error }

func (h *refusingHandler) CanHandle(t string) bool { return t == "widget" }
func (h *refusingHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &refusingConfig{err: h.err}, nil
}

// wantViolation fails unless err wraps a ViolationError, and returns it.
func wantViolation(t *testing.T, err error) *ViolationError {
	t.Helper()
	if err == nil {
		t.Fatal("no error, want a policy violation")
	}
	var v *ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error is %T, want it to wrap *ViolationError: %v", err, err)
	}
	return v
}

// TestPolicyRefusal_ErrorIsItsMessage: the refusal's text is its message, as
// given: nothing is added and nothing in it is read as a format.
func TestPolicyRefusal_ErrorIsItsMessage(t *testing.T) {
	const message = `image "other.example/app:1" is not from an allowed registry [%v 100%]`
	err := NewPolicyRefusal(RefusalRegistry, message)
	if got := err.Error(); got != message {
		t.Errorf("Error() = %q, want the message %q", got, message)
	}
	var r *PolicyRefusal
	if !errors.As(err, &r) {
		t.Fatalf("NewPolicyRefusal returned %T, want a *PolicyRefusal", err)
	}
	if r.Class != RefusalRegistry || r.Message != message {
		t.Errorf("refusal = %+v, want class %q and the message", r, RefusalRegistry)
	}
}

// TestRefusalClasses_ClosedSet: the documented set holds twelve distinct
// classes, none of them the unclassified value, and that value is the zero
// value of the type.
func TestRefusalClasses_ClosedSet(t *testing.T) {
	want := []RefusalClass{
		RefusalHostNamespace, RefusalPrivileged, RefusalHostPath, RefusalContainerCapability,
		RefusalRegistry, RefusalResourceMaximum, RefusalStorageMaximum, RefusalReplicaMaximum,
		RefusalExplicitSecret, RefusalTraitCapability, RefusalUnreadableObject, RefusalObjectKind,
	}
	got := RefusalClasses()
	if !slices.Equal(got, want) {
		t.Errorf("RefusalClasses() = %q, want %q", got, want)
	}
	seen := map[RefusalClass]bool{}
	for _, c := range got {
		if c == RefusalUnclassified {
			t.Error("the set of classes holds the unclassified value")
		}
		if seen[c] {
			t.Errorf("class %q is listed twice", c)
		}
		seen[c] = true
	}
	var zero RefusalClass
	if RefusalUnclassified != zero {
		t.Errorf("RefusalUnclassified = %q, want the zero value", RefusalUnclassified)
	}
	got[0] = "changed"
	if RefusalClasses()[0] != want[0] {
		t.Error("RefusalClasses returns a slice a caller can change the set through")
	}
}

// TestNewViolationError_Class: the violation takes the class of the first
// PolicyRefusal in its cause chain, and the unclassified value when the chain
// holds none. Its text and its cause are what a literal gave.
func TestNewViolationError_Class(t *testing.T) {
	registry := NewPolicyRefusal(RefusalRegistry, "registry refused")
	replicas := NewPolicyRefusal(RefusalReplicaMaximum, "replicas refused")
	cases := []struct {
		name  string
		cause error
		want  RefusalClass
	}{
		{"a plain error", errors.New("the chart does not render"), RefusalUnclassified},
		{"a refusal", registry, RefusalRegistry},
		{"a refusal under two wraps", fmt.Errorf("object X: %w", fmt.Errorf("containers[0]: %w", registry)), RefusalRegistry},
		{"a plain error wrapped beside nothing", fmt.Errorf("object X: %w", errors.New("not readable")), RefusalUnclassified},
		{"two refusals, the first decides", fmt.Errorf("%w: %w", replicas, registry), RefusalReplicaMaximum},
		{"a refusal joined after a plain error", errors.Join(errors.New("plain"), registry), RefusalRegistry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewViolationError("web", tc.cause)
			if v.Class != tc.want {
				t.Errorf("Class = %q, want %q", v.Class, tc.want)
			}
			if v.Component != "web" {
				t.Errorf("Component = %q, want %q", v.Component, "web")
			}
			if !errors.Is(v, tc.cause) {
				t.Error("the violation does not unwrap to its cause")
			}
			literal := &ViolationError{Component: "web", Cause: tc.cause}
			if v.Error() != literal.Error() {
				t.Errorf("Error() = %q, want what a literal gives, %q", v.Error(), literal.Error())
			}
		})
	}
}

// TestViolationError_LiteralIsUnclassified: a violation built as a keyed
// literal, with no Class, reports the unclassified value: no class is read from
// the cause after the fact.
func TestViolationError_LiteralIsUnclassified(t *testing.T) {
	v := &ViolationError{Component: "web", Cause: NewPolicyRefusal(RefusalRegistry, "registry refused")}
	if v.Class != RefusalUnclassified {
		t.Errorf("Class = %q, want the unclassified value", v.Class)
	}
}

// TestTransform_ConsumerEnforceableClass: a consumer's own Enforceable gets the
// unclassified value for a plain error and its class for a refusal built with
// the exported constructor, wrapped or not. The violation's text is the same
// whichever it returns.
func TestTransform_ConsumerEnforceableClass(t *testing.T) {
	const message = "widget size 9 exceeds the site limit 3"
	cases := []struct {
		name string
		err  error
		want RefusalClass
		text string
	}{
		{"a plain error", errors.New(message), RefusalUnclassified, message},
		{"the constructor", NewPolicyRefusal(RefusalResourceMaximum, message), RefusalResourceMaximum, message},
		{"the constructor, wrapped", fmt.Errorf("widget: %w", NewPolicyRefusal(RefusalResourceMaximum, message)), RefusalResourceMaximum, "widget: " + message},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(map[string]ComponentHandler{"widget": &refusingHandler{err: tc.err}}, nil)
			_, err := tr.Transform(makeApp("myapp", makeComponent("web", "widget")), TransformContext{})
			v := wantViolation(t, err)
			if v.Class != tc.want {
				t.Errorf("Class = %q, want %q", v.Class, tc.want)
			}
			if v.Component != "web" {
				t.Errorf("Component = %q, want %q", v.Component, "web")
			}
			if want := `component "web": ` + tc.text; v.Error() != want {
				t.Errorf("Error() = %q, want %q", v.Error(), want)
			}
		})
	}
}

// TestTransform_PostPolicyStepRefusalClass: a post-policy step that refuses by
// the policy fails the transform as the component's violation with the class
// of the refusal, wrapped or not, and no later step runs. A step error that is
// no refusal stays a TransformError and is no violation. The text is the same
// either way.
func TestTransform_PostPolicyStepRefusalClass(t *testing.T) {
	const message = `storageSize "1Gi" exceeds enforced maximum "512Mi"`
	refusal := NewPolicyRefusal(RefusalStorageMaximum, message)
	cases := []struct {
		name      string
		err       error
		violation bool
		class     RefusalClass
		text      string
	}{
		{"a refusal", refusal, true, RefusalStorageMaximum, message},
		{"a refusal, wrapped", fmt.Errorf("defaults: %w", refusal), true, RefusalStorageMaximum, "defaults: " + message},
		{"a plain error", errors.New(message), false, RefusalUnclassified, message},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log []string
			failing := func(stack.ApplicationConfig) error { return tc.err }
			err := stepTransform(&log, "logged", failing, loggingStep(t, &log, "after"))
			if err == nil {
				t.Fatal("no error, want the step's error to fail the transform")
			}
			if want := `component "web": ` + tc.text; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
			if !errors.Is(err, tc.err) {
				t.Error("the error does not wrap the step's error")
			}
			var v *ViolationError
			var te *TransformError
			isViolation, isTransform := errors.As(err, &v), errors.As(err, &te)
			if isViolation != tc.violation || isTransform == tc.violation {
				t.Fatalf("error is %T (violation %v, transform error %v), want violation %v and not both", err, isViolation, isTransform, tc.violation)
			}
			if tc.violation {
				if v.Class != tc.class || v.Component != "web" {
					t.Errorf("violation = component %q, class %q; want component %q, class %q", v.Component, v.Class, "web", tc.class)
				}
			} else if te.Message != `component "web"` {
				t.Errorf("TransformError.Message = %q, want %q", te.Message, `component "web"`)
			}
			if want := []string{"policy"}; !slices.Equal(log, want) {
				t.Errorf("ran %v, want %v: a step after the failing one ran, or a trait did", log, want)
			}
		})
	}
}

// TestTransform_TraitCapabilityRefusalClass: each of the three trait-capability
// constraints refuses with the trait-capability class, on the application the
// constraint is checked for, in the text it had.
func TestTransform_TraitCapabilityRefusalClass(t *testing.T) {
	exposed := Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "expose", Properties: map[string]any{}}}}
	cases := []struct {
		name   string
		comp   Component
		policy *constrainedPolicy
		text   string
	}{
		{"forbidden", exposed, &constrainedPolicy{forbidden: []string{"expose"}}, `capability "expose" is forbidden by environment policy`},
		{"not in the allowed list", exposed, &constrainedPolicy{allowed: []string{"ingress"}}, `capability "expose" is not in the allowed list`},
		{"required and missing", makeComponent("web", "webservice"), &constrainedPolicy{required: []string{"ingress"}}, `required capability "ingress" is missing`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"expose": &stubTraitHandler{typ: "expose"}},
			)
			_, err := tr.Transform(makeApp("myapp", tc.comp), TransformContext{Policy: tc.policy})
			v := wantViolation(t, err)
			if v.Class != RefusalTraitCapability {
				t.Errorf("Class = %q, want %q", v.Class, RefusalTraitCapability)
			}
			if want := `component "myapp": ` + tc.text; v.Error() != want {
				t.Errorf("Error() = %q, want %q", v.Error(), want)
			}
		})
	}
}
