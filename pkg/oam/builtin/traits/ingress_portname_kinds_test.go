package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// go-kure/launcher#545: an ingress path whose implicit backend is addressed by portName is held to
// the name of the component's own Service port, for every workload kind that generates a Service
// (webservice, since go-kure/launcher#690 left daemonset and statefulset without one),
// just as a numbered port is held to its number. A name the Service does not carry built an
// Ingress whose backend port could not resolve.

func workloadKindTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil, nil)
	registerWebservice(tr)
	tr.RegisterComponent("statefulset", &components.StatefulsetHandler{})
	tr.RegisterComponent("daemonset", &components.DaemonsetHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	tr.RegisterBuiltinTrait("prune-protection", &traits.PruneProtectionHandler{})
	return tr
}

func TestTransform_WorkloadKinds_ImplicitBackendPortName(t *testing.T) {
	pruneFirst := oam.Trait{Type: "prune-protection", Properties: map[string]any{}}
	kinds := []struct {
		kind     string
		props    map[string]any
		portName string // the name the kind's generated Service gives its port
		other    string // a real port name of another kind, absent from this kind's Service
		self     string // the component's own Service name, as an explicit self backend
	}{
		{"webservice", map[string]any{"image": "nginx:1.25", "port": 8080}, "http", "tcp", "web"},
	}
	for _, k := range kinds {
		cases := []struct {
			name    string
			traits  []oam.Trait
			wantErr string // empty: the build succeeds
		}{
			{"matching name", []oam.Trait{ingressTrait(map[string]any{"path": "/", "portName": k.portName})}, ""},
			{"matching name, self-named backend", []oam.Trait{ingressTrait(map[string]any{"path": "/", "backend": k.self, "portName": k.portName})}, ""},
			{"matching name behind a decorator", []oam.Trait{pruneFirst, ingressTrait(map[string]any{"path": "/", "portName": k.portName})}, ""},
			{"unknown name", []oam.Trait{ingressTrait(map[string]any{"path": "/", "portName": "nope"})},
				`cannot route implicit backend to port "nope"`},
			{"another kind's name", []oam.Trait{ingressTrait(map[string]any{"path": "/", "portName": k.other})},
				`cannot route implicit backend to port "` + k.other + `"`},
			{"unknown name, self-named backend", []oam.Trait{ingressTrait(map[string]any{"path": "/", "backend": k.self, "portName": "nope"})},
				`cannot route implicit backend to port "nope"`},
			{"unknown name behind a decorator", []oam.Trait{pruneFirst, ingressTrait(map[string]any{"path": "/", "portName": "nope"})},
				`cannot route implicit backend to port "nope"`},
			{"unknown name on an explicit other backend", []oam.Trait{ingressTrait(map[string]any{"path": "/", "backend": "elsewhere", "portName": "nope"})}, ""},
		}
		for _, tc := range cases {
			t.Run(k.kind+"/"+tc.name, func(t *testing.T) {
				app := &oam.Application{
					APIVersion: oam.SupportedAPIVersion,
					Kind:       "Application",
					Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
					Spec: oam.ApplicationSpec{Components: []oam.Component{{
						Name: "web", Type: k.kind, Properties: k.props, Traits: tc.traits,
					}}},
				}
				_, _, err := workloadKindTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("TransformWithPolicy: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected an error containing %q, got %v", tc.wantErr, err)
				}
			})
		}
	}
}

// go-kure/launcher#690: daemonset and statefulset generate no Service, so an ingress on one has no
// implicit backend, with or without container ports, as on deployment. The route names an
// authored `service` instead.
func TestTransform_NoServiceKinds_ImplicitBackendRefused(t *testing.T) {
	ports := []any{map[string]any{"name": "http", "containerPort": 9100}}
	for _, k := range []struct {
		kind  string
		props map[string]any
	}{
		{"statefulset", map[string]any{"image": "postgres:16", "ports": ports, "serviceName": "web-headless"}},
		{"daemonset", map[string]any{"image": "prom/node-exporter:v1.0.0", "ports": ports}},
	} {
		for name, path := range map[string]map[string]any{
			"implicit":         {"path": "/"},
			"implicit by name": {"path": "/", "portName": "http"},
		} {
			t.Run(k.kind+"/"+name, func(t *testing.T) {
				app := &oam.Application{
					APIVersion: oam.SupportedAPIVersion,
					Kind:       "Application",
					Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
					Spec: oam.ApplicationSpec{Components: []oam.Component{{
						Name: "web", Type: k.kind, Properties: k.props, Traits: []oam.Trait{ingressTrait(path)},
					}}},
				}
				_, _, err := workloadKindTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
				if want := `component "web" has no service port`; err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("expected an error containing %q, got %v", want, err)
				}
			})
		}
	}
}
