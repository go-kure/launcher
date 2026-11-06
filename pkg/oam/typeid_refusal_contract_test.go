package oam

import (
	"errors"
	"testing"

	kureerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// Every entry point that resolves types refuses a reference the resolver refuses
// with a *ValidationError whose Cause is the resolver's error: Parse, Transform and
// the authored checks do, and so does the exported ResolveTypes.
func TestResolveTypes_RefusalIsAValidationError(t *testing.T) {
	tr, _ := declared(t)
	for name, tc := range map[string]struct {
		app  *Application
		want error
	}{
		"a component version that does not exist": {
			&Application{Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget@v9"}}}}, ErrTypeReferenceUnknown,
		},
		"a trait version that does not exist": {
			&Application{Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget", Traits: []Trait{{Type: "sticky@v9"}}}}}}, ErrTypeReferenceUnknown,
		},
		"a policy version that does not exist": {
			&Application{Spec: ApplicationSpec{Policies: []ApplicationPolicy{{Name: "p", Type: "placement@v9"}}}}, ErrTypeReferenceUnknown,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tr.ResolveTypes(tc.app)
			var ve *kureerrors.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("ResolveTypes error %T is not a *ValidationError: %v", err, err)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("errors.Is(err, %v) is false for %v", tc.want, err)
			}
		})
	}
}

// An ambiguous short name is refused the same way, and errors.Is still matches it.
func TestResolveTypes_AmbiguousRefusalIsAValidationError(t *testing.T) {
	tr := twoVersions(t)
	app := makeApp("myapp", makeComponent("w", "widget"))
	_, err := tr.ResolveTypes(app)
	var ve *kureerrors.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("ResolveTypes error %T is not a *ValidationError: %v", err, err)
	}
	if !errors.Is(err, ErrTypeReferenceAmbiguous) {
		t.Errorf("errors.Is(err, ErrTypeReferenceAmbiguous) is false for %v", err)
	}
}

// The authored checks do not judge whether a type exists: a plain type nobody
// declared and no handler or rule claims passes them in every position, and the
// later stage (Transform, or Parse's allowlist) refuses it. One source of "unknown
// type" truth: this pins that the authored checks are not a second one.
func TestAuthoredChecks_DoNotRefuseAnUnknownPlainType(t *testing.T) {
	tr, _ := declared(t)
	for name, app := range map[string]*Application{
		"component": makeApp("myapp", makeComponent("w", "nosuchcomponent")),
		"trait": makeApp("myapp", Component{
			Name: "w", Type: "widget", Traits: []Trait{{Type: "nosuchtrait"}},
		}),
		"policy": {
			Metadata: Metadata{Name: "myapp"},
			Spec: ApplicationSpec{
				Components: []Component{makeComponent("w", "widget")},
				Policies:   []ApplicationPolicy{{Name: "p", Type: "nosuchpolicy"}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
			if _, err := tr.ValidateAuthoredProperties(app); err != nil {
				t.Errorf("ValidateAuthoredProperties refused an unknown plain %s type: %v", name, err)
			}
			if _, err := tr.Transform(app, TransformContext{}); err == nil {
				t.Errorf("Transform accepted an unknown plain %s type", name)
			}
		})
	}
}

// sinkEndpointRule is a ComponentLoweringRule and EndpointProvider that lowers its
// component into the terminal handler type `sink`, so a document of its type
// transforms: the rule runs, then a handler. stubEndpointRule lowers a component to
// itself and never reaches a handler.
type sinkEndpointRule struct {
	typeName string
	eps      []netpol.Endpoint
}

func (r sinkEndpointRule) ComponentType() string { return r.typeName }
func (r sinkEndpointRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: c.Name, Type: "sink"}}}, nil
}
func (r sinkEndpointRule) Endpoints(_ *Component) ([]netpol.Endpoint, error) { return r.eps, nil }
