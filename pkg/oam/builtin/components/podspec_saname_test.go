package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// serviceAccountObjectName returns the name of the generated ServiceAccount, or
// "" when the kind emitted none.
func serviceAccountObjectName(objects []*client.Object) string {
	for _, o := range objects {
		if sa, ok := (*o).(*corev1.ServiceAccount); ok {
			return sa.Name
		}
	}
	return ""
}

// TestWorkloadKinds_ServiceAccountIdentityIsSingleSourced pins the invariant the
// oam.ServiceAccountNamer contract rests on: for every kind, the account the
// namer reports (the rbac trait's RoleBinding subject) is the same string as the
// account the pod template runs as, and as the account a role kind emits.
//
// It is checked against an Application whose name differs from the component the
// config was built from. The transformer never produces that pairing — it builds
// the Application from the same component (pkg/oam/transform.go) — but nothing
// in the type system forbids it, and the Application's name must not stand in
// for the namer's answer. A pod kind without an authored account answers "",
// and its pod names none (go-kure/launcher#702).
func TestWorkloadKinds_ServiceAccountIdentityIsSingleSourced(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				props map[string]any
			}{
				{"default account", k.props},
				{"authored account", withProps(k.props, map[string]any{"serviceAccountName": "shared-sa"})},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cfg, err := k.handler.ToApplicationConfig(
						&oam.Component{Name: "component-name", Type: k.name, Properties: tc.props}, "default")
					if err != nil {
						t.Fatalf("ToApplicationConfig: %v", err)
					}
					namer, ok := cfg.(oam.ServiceAccountNamer)
					if !ok {
						t.Fatal("config does not implement oam.ServiceAccountNamer")
					}
					want, _ := namer.ServiceAccountName()

					// A deliberately mismatched Application name: the generation
					// path must not reach for it over the namer's answer.
					objects, err := cfg.Generate(stack.NewApplication("other-app-name", "default", cfg))
					if err != nil {
						t.Fatalf("Generate: %v", err)
					}

					if got := podTemplateSpec(t, objects).ServiceAccountName; got != want {
						t.Errorf("pod ServiceAccountName = %q, want %q (the namer's answer)", got, want)
					}
					if sa := serviceAccountObjectName(objects); sa != "" && sa != want {
						t.Errorf("generated ServiceAccount named %q, want %q (the namer's answer)", sa, want)
					}
				})
			}
		})
	}
}
