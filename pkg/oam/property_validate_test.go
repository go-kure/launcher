package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// ingressLikeSchema mirrors the shape of IngressHandler.PropertySchema()
// (pkg/oam/builtin/traits/ingress.go:79-108: required array "rules" of objects with
// required "host" + nested "paths", an enum-constrained "pathType", and an
// AdditionalProperties-open "annotations"). Package oam cannot import
// pkg/oam/builtin/traits directly — that would be the import cycle the spike's
// Approach section rules out (rule implementations live outside pkg/oam) — so this
// table test proves validateProperties against a schema of the same shape instead of
// the live handler.
var ingressLikeSchema = map[string]PropertySchema{
	"rules": {
		Type:     PropertyTypeArray,
		Required: true,
		Items: &PropertySchema{
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"host": {Type: PropertyTypeString, Required: true},
				"paths": {
					Type: PropertyTypeArray,
					Items: &PropertySchema{
						Type: PropertyTypeObject,
						Properties: map[string]PropertySchema{
							"path":     {Type: PropertyTypeString, Default: "/"},
							"pathType": {Type: PropertyTypeString, Default: "Prefix", Enum: []any{"Prefix", "Exact", "ImplementationSpecific"}},
							"backend":  {Type: PropertyTypeString},
						},
					},
				},
			},
		},
	},
	"annotations": {Type: PropertyTypeObject, AdditionalProperties: true},
}

// TestValidateProperties_IngressLikeSchema is the C3 table-test proof: validateProperties
// checked against a real trait handler's declared schema shape (D4's target schema
// for a trait emitted with Type "ingress").
func TestValidateProperties_IngressLikeSchema(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string // substring expected in the error, "" means no error
	}{
		{
			name: "valid minimal rules",
			props: map[string]any{
				"rules": []any{
					map[string]any{
						"host": "example.com",
						"paths": []any{
							map[string]any{"path": "/", "backend": "web"},
						},
					},
				},
			},
		},
		{
			name:    "missing required rules",
			props:   map[string]any{},
			wantErr: `"rules" is required`,
		},
		{
			name: "wrong type for rules",
			props: map[string]any{
				"rules": "not-an-array",
			},
			wantErr: "expected array",
		},
		{
			name: "unsupported top-level key",
			props: map[string]any{
				"rules": []any{
					map[string]any{"host": "example.com", "paths": []any{}},
				},
				"bogus": "value",
			},
			wantErr: `unsupported field "bogus"`,
		},
		{
			name: "missing required nested host",
			props: map[string]any{
				"rules": []any{
					map[string]any{"paths": []any{}},
				},
			},
			wantErr: `"host" is required`,
		},
		{
			name: "enum violation on nested pathType",
			props: map[string]any{
				"rules": []any{
					map[string]any{
						"host": "example.com",
						"paths": []any{
							map[string]any{"path": "/", "pathType": "Bogus"},
						},
					},
				},
			},
			wantErr: "not in allowed set",
		},
		{
			name: "additionalProperties true allows arbitrary annotation keys",
			props: map[string]any{
				"rules":       []any{map[string]any{"host": "example.com", "paths": []any{}}},
				"annotations": map[string]any{"anything.example.com/key": "value"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProperties(ingressLikeSchema, tc.props, "properties")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// stubValidatingComponentRule emits a fixed set of properties for a "webservice"
// target component, letting the test control whether they pass validation.
type stubValidatingComponentRule struct {
	emitProps map[string]any
}

func (r stubValidatingComponentRule) ComponentType() string { return "stub-source" }

func (r stubValidatingComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{
		{Name: comp.Name + "-web", Type: "webservice", Properties: r.emitProps},
	}}, nil
}

// stubWebserviceHandler is a minimal PropertySchemaProvider-implementing
// ComponentHandler standing in for the real webservice handler, so the test does not
// depend on builtin/components (avoiding an unrelated import).
type stubWebserviceHandler struct{}

func (stubWebserviceHandler) CanHandle(componentType string) bool {
	return componentType == "webservice"
}

func (stubWebserviceHandler) ToApplicationConfig(component *Component, namespace string) (stack.ApplicationConfig, error) {
	return nil, nil
}

func (stubWebserviceHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"image": {Type: PropertyTypeString, Required: true},
	}
}

// TestLower_EmittedComponent_SchemaViolation_OriginFirst is the C3 proof that an
// emitted element failing D4 validation errors with the AUTHORED origin first
// (D7 provenance-first), not just the schema violation.
func TestLower_EmittedComponent_SchemaViolation_OriginFirst(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice": stubWebserviceHandler{},
	}, nil)
	tr.RegisterComponentLowering(stubValidatingComponentRule{
		emitProps: map[string]any{"image": "nginx", "unknownKey": "boom"},
	})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "src", Type: "stub-source", Properties: map[string]any{}}},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected an error from the schema-violating emitted component")
	}
	msg := err.Error()
	originIdx := strings.Index(msg, `component "src"`)
	causeIdx := strings.Index(msg, `unsupported field "unknownKey"`)
	if originIdx == -1 || causeIdx == -1 {
		t.Fatalf("expected both the authored origin and the cause in the error, got: %v", msg)
	}
	if originIdx > causeIdx {
		t.Fatalf("expected the authored origin BEFORE the cause, got: %v", msg)
	}
}
