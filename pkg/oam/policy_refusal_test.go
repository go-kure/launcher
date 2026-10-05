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

// TestRefusalClasses_ClosedSet: the documented set holds eleven distinct
// classes, none of them the unclassified value, and that value is the zero
// value of the type.
func TestRefusalClasses_ClosedSet(t *testing.T) {
	want := []RefusalClass{
		RefusalHostNamespace, RefusalPrivileged, RefusalHostPath, RefusalContainerCapability,
		RefusalRegistry, RefusalResourceMaximum, RefusalStorageMaximum, RefusalReplicaMaximum,
		RefusalExplicitSecret, RefusalTraitCapability, RefusalUnreadableObject,
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
