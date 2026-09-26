package components_test

import (
	"math"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// validateThenRenderDeployment runs the authored-property check over a one-component
// webservice document and then hands the SAME props map to the real handler, the way
// kurel build does (ValidateAuthoredProperties before Transform), returning the
// rendered Deployment.
func validateThenRenderDeployment(t *testing.T, props map[string]any) (*appsv1.Deployment, error) {
	t.Helper()
	h := &components.WebserviceHandler{}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"webservice": h}, nil)
	app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
		{Name: "app", Type: "webservice", Properties: props},
	}}}
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		return nil, err
	}
	return renderDeployment(t, h, &app.Spec.Components[0]), nil
}

// renderDeployment runs the handler alone. Validation normalizes the values it
// checks, so a reader that only knows some integer kinds renders correctly behind
// it; a handler called directly, as Transform's callers and the lowering engine
// do, sees the authored Go kind and must read it too.
func renderDeployment(t *testing.T, h *components.WebserviceHandler, comp *oam.Component) *appsv1.Deployment {
	t.Helper()
	cfg, err := h.ToApplicationConfig(comp, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objects, err := cfg.Generate(stack.NewApplication(comp.Name, "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if d, ok := (*obj).(*appsv1.Deployment); ok {
			return d
		}
	}
	t.Fatal("no Deployment rendered")
	return nil
}

// TestIntegerProperty_EveryGoIntegerKindReachesTheReader is go-kure/launcher#418.
// Validation accepted every Go integer kind by reflect.Kind, but the component readers
// (toInt32/toInt64 in common.go) type-switch on float64/int/int32/int64 only, so a
// `replicas: uint32(3)` passed validation and then rendered the schema default of 1.
//
// Asserting that validation returns nil proves nothing here — it always did. The
// assertion is on the rendered Deployment, after the real handler read the value.
func TestIntegerProperty_EveryGoIntegerKindReachesTheReader(t *testing.T) {
	cases := []struct {
		name     string
		replicas any
		port     any
	}{
		{"int", int(3), int(8080)},
		{"int8", int8(3), int16(8080)}, // 8080 does not fit int8
		{"int16", int16(3), int16(8080)},
		{"int32", int32(3), int32(8080)},
		{"int64", int64(3), int64(8080)},
		{"uint", uint(3), uint(8080)},
		{"uint8", uint8(3), uint16(8080)}, // 8080 does not fit uint8
		{"uint16", uint16(3), uint16(8080)},
		{"uint32", uint32(3), uint32(8080)},
		{"uint64", uint64(3), uint64(8080)},
		// The shape the YAML CLI actually produces for a numeric literal routed
		// through interface{}: an integral float64 must keep working.
		{"float64", float64(3), float64(8080)},
	}
	render := map[string]func(t *testing.T, props map[string]any) *appsv1.Deployment{
		"validated": func(t *testing.T, props map[string]any) *appsv1.Deployment {
			d, err := validateThenRenderDeployment(t, props)
			if err != nil {
				t.Fatalf("validation: %v", err)
			}
			return d
		},
		// go-kure/launcher#525: without validation in front, the reader itself
		// must handle the kind.
		"handler-only": func(t *testing.T, props map[string]any) *appsv1.Deployment {
			return renderDeployment(t, &components.WebserviceHandler{},
				&oam.Component{Name: "app", Type: "webservice", Properties: props})
		},
	}
	for _, tc := range cases {
		for path, run := range render {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				d := run(t, map[string]any{
					"image":    "ghcr.io/org/app:v1",
					"replicas": tc.replicas,
					"port":     tc.port,
				})
				checkReplicasAndPort(t, d, tc.replicas, tc.port)
			})
		}
	}
}

func checkReplicasAndPort(t *testing.T, d *appsv1.Deployment, replicas, port any) {
	t.Helper()
	if d.Spec.Replicas == nil {
		t.Fatalf("replicas %T(3) rendered no replicas field", replicas)
	}
	if *d.Spec.Replicas != 3 {
		t.Errorf("replicas %T(3) rendered %d, want 3 — the supplied value was dropped", replicas, *d.Spec.Replicas)
	}
	ports := d.Spec.Template.Spec.Containers[0].Ports
	if len(ports) == 0 || ports[0].ContainerPort != 8080 {
		t.Errorf("port %T(8080) rendered %v, want containerPort 8080", port, ports)
	}
}

// TestIntegerProperty_OutOfRangeIsAValidationError pins the other half of #418: an
// unsigned value that no signed reader can hold is a build error naming the field,
// not a silent default or a wrapped-around negative.
func TestIntegerProperty_OutOfRangeIsAValidationError(t *testing.T) {
	for _, v := range []any{uint64(math.MaxUint64), uint64(math.MaxInt64) + 1, uint(math.MaxUint)} {
		_, err := validateThenRenderDeployment(t, map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": v,
		})
		if err == nil || !strings.Contains(err.Error(), "properties.replicas") || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("replicas %T(%v): want an out-of-range validation error naming the field, got: %v", v, v, err)
		}
	}
}
