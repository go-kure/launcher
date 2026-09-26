package traits_test

import (
	"math"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// servicePortRule lowers one component to a worker carrying a single routing trait
// whose servicePort is whatever Go value the case supplies — the path a lowering rule
// or a library caller takes, and the only one that can produce a plain int32 or int64.
type servicePortRule struct {
	traitType  string
	traitProps map[string]any
}

func (servicePortRule) ComponentType() string { return "service-port-app" }

func (r servicePortRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{
		Name:       comp.Name,
		Type:       "worker",
		Properties: map[string]any{"image": "nginx:1.25"},
		Traits:     []oam.Trait{{Type: r.traitType, Properties: r.traitProps}},
	}}}, nil
}

// transformServicePort runs Transform and Generate for a worker whose routing trait
// names the backend through the trait-level serviceName/servicePort pair.
func transformServicePort(t *testing.T, traitType string, port any) ([]client.Object, error) {
	t.Helper()
	props := map[string]any{"serviceName": "other-svc", "servicePort": port}
	switch traitType {
	case "httproute":
		props["parentRefs"] = []any{map[string]any{"name": "gw"}}
		props["rules"] = []any{map[string]any{}}
	case "ingress":
		props["rules"] = []any{map[string]any{
			"host":  "app.example.com",
			"paths": []any{map[string]any{"path": "/"}},
		}}
	}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"worker": &components.WorkerHandler{},
	}, nil)
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	tr.RegisterComponentLowering(servicePortRule{traitType: traitType, traitProps: props})
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "service-port-app", Properties: map[string]any{},
		}}},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		return nil, err
	}
	var out []client.Object
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					return err
				}
				for _, o := range objs {
					out = append(out, *o)
				}
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(cluster.Node)
}

func ingressBackendPort(t *testing.T, objs []client.Object) int32 {
	t.Helper()
	for _, o := range objs {
		if ing, ok := o.(*networkingv1.Ingress); ok {
			svc := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
			if svc == nil || svc.Name != "other-svc" {
				t.Fatalf("ingress backend = %+v, want service other-svc", svc)
			}
			return svc.Port.Number
		}
	}
	t.Fatal("no Ingress generated")
	return 0
}

// TestServicePort_EveryGoIntegerKind is go-kure/launcher#525. Validation leaves a
// plain int32 or int64 as it is (only named types and the narrower kinds are
// rewritten to int), and toIngressPort accepted float64 and int only, so a correct
// servicePort from a lowering rule or a Go caller was refused. Every integer kind
// must render the same port an int does, on both traits that read servicePort.
func TestServicePort_EveryGoIntegerKind(t *testing.T) {
	cases := []struct {
		name string
		port any
		want int32
	}{
		{"int", int(8080), 8080},
		{"int8", int8(80), 80}, // 8080 does not fit int8
		{"int16", int16(8080), 8080},
		{"int32", int32(8080), 8080},
		{"int64", int64(8080), 8080},
		{"uint", uint(8080), 8080},
		{"uint8", uint8(80), 80}, // 8080 does not fit uint8
		{"uint16", uint16(8080), 8080},
		{"uint32", uint32(8080), 8080},
		{"uint64", uint64(8080), 8080},
		{"float64", float64(8080), 8080},
		{"max port int64", int64(65535), 65535},
	}
	for _, trait := range []string{"httproute", "ingress"} {
		for _, tc := range cases {
			t.Run(trait+"/"+tc.name, func(t *testing.T) {
				objs, err := transformServicePort(t, trait, tc.port)
				if err != nil {
					t.Fatalf("servicePort %T(%v): %v", tc.port, tc.port, err)
				}
				if trait == "ingress" {
					if got := ingressBackendPort(t, objs); got != tc.want {
						t.Errorf("servicePort %T(%v) rendered port %d, want %d", tc.port, tc.port, got, tc.want)
					}
					return
				}
				backendIs(t, objs, "other-svc", tc.want)
			})
		}
	}
}

// TestServicePort_OutOfRangeIsRefused pins the other half of go-kure/launcher#525: widening the
// reader must not turn it into a truncating one. A value above 65535, zero, a
// negative, one that wraps into range as an int32 (2^32+80) and a fractional float
// are all errors naming the field — never a port.
func TestServicePort_OutOfRangeIsRefused(t *testing.T) {
	for _, trait := range []string{"httproute", "ingress"} {
		for _, port := range []any{
			int64(65536), int32(70000), uint32(70000), uint64(70000),
			int32(0), int64(-1), int8(-80),
			int64(math.MaxUint32) + 81, // 2^32+80: int32 conversion would wrap to 80
			float64(8080.5),
			uint64(math.MaxUint64),
		} {
			objs, err := transformServicePort(t, trait, port)
			if err == nil {
				t.Errorf("%s servicePort %T(%v): accepted (%d objects), want an error", trait, port, port, len(objs))
				continue
			}
			if !strings.Contains(err.Error(), "servicePort") {
				t.Errorf("%s servicePort %T(%v): error does not name the field: %v", trait, port, port, err)
			}
		}
	}
}
