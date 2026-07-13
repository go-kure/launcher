package traits_test

import (
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// Inline secretStoreRef in trait properties works without a capability.
func TestExternalSecretHandler_InlineSecretStoreRef(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "db-password",
			"secretStoreRef": map[string]any{
				"name": "vault-backend",
				"kind": "ClusterSecretStore",
			},
			"remoteRef": map[string]any{"key": "secret/db"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply with inline secretStoreRef: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	// Verify the inline secretStoreRef is used correctly in the generated ExternalSecret.
	esApp := bundle.Applications[0]
	objs, err := esApp.Config.Generate(esApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	es, ok := (*objs[0]).(*esv1.ExternalSecret)
	if !ok {
		t.Fatal("expected *esv1.ExternalSecret")
	}
	if es.Spec.SecretStoreRef.Name != "vault-backend" {
		t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "vault-backend")
	}
	if es.Spec.SecretStoreRef.Kind != "ClusterSecretStore" {
		t.Errorf("SecretStoreRef.Kind = %q, want %q", es.Spec.SecretStoreRef.Kind, "ClusterSecretStore")
	}
}

// provider: string shorthand maps to ClusterSecretStore (downstream backward-compat).
func TestExternalSecretHandler_ProviderShorthand(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "cloud-secret",
			"provider":   "aws-secretsmanager",
			"remoteRef":  map[string]any{"key": "prod/db"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply with provider shorthand: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	// Verify the provider name is used as StoreRefName.
	esApp := bundle.Applications[0]
	objs, err := esApp.Config.Generate(esApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	es, ok := (*objs[0]).(*esv1.ExternalSecret)
	if !ok {
		t.Fatal("expected *esv1.ExternalSecret")
	}
	if es.Spec.SecretStoreRef.Name != "aws-secretsmanager" {
		t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "aws-secretsmanager")
	}
	if es.Spec.SecretStoreRef.Kind != "ClusterSecretStore" {
		t.Errorf("SecretStoreRef.Kind = %q, want %q", es.Spec.SecretStoreRef.Kind, "ClusterSecretStore")
	}
}

// Inline secretStoreRef with output assertion.
func TestExternalSecretHandler_InlineSecretStoreRef_WithOutputAssertion(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-secret",
			"secretStoreRef": map[string]any{
				"name": "inline-store",
				"kind": "ClusterSecretStore",
			},
			"remoteRef": map[string]any{"key": "secret/val"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	esApp := bundle.Applications[0]
	objs, err := esApp.Config.Generate(esApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	es, ok := (*objs[0]).(*esv1.ExternalSecret)
	if !ok {
		t.Fatal("expected *esv1.ExternalSecret")
	}
	if es.Spec.SecretStoreRef.Name != "inline-store" {
		t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "inline-store")
	}
}

// Neither inline nor capability → error with clear message.
func TestExternalSecretHandler_NoStoreRef_Error(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-secret",
			"remoteRef":  map[string]any{"key": "secret/val"},
		},
	}
	if err := h.Apply(trait, app, bundle); err == nil {
		t.Fatal("expected error when no secretStoreRef or provider")
	}
}

// Capability-only path: secretStoreRef comes exclusively from the ClusterProfile capability
// rendering and is merged into trait properties before Apply is called.
func TestExternalSecretHandler_CapabilityOnly(t *testing.T) {
	transformer := oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"webservice": &components.WebserviceHandler{},
		},
		nil,
	)
	transformer.RegisterBuiltinTrait("external-secret", &traits.ExternalSecretHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name: "api",
				Type: "webservice",
				Properties: map[string]any{
					"image": "myimage:v1.0.0",
				},
				Traits: []oam.Trait{{
					Type: "external-secret",
					Properties: map[string]any{
						"secretName": "db-secret",
						"remoteRef":  map[string]any{"key": "secret/db"},
						// No secretStoreRef here — comes exclusively from capability
					},
				}},
			}},
		},
	}

	ctx := oam.TransformContext{
		Capabilities: map[string]oam.CapabilityBinding{
			"external-secret": {Rendering: map[string]any{
				"secretStoreRef": map[string]any{
					"name": "cap-store",
					"kind": "ClusterSecretStore",
				},
			}},
		},
	}

	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	// Walk leaf bundle to find the external-secret app and generate its ExternalSecret.
	found := false
	var collectApps func(node *stack.Node)
	collectApps = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil && !node.Bundle.IsUmbrella() {
			for _, bundleApp := range node.Bundle.Applications {
				if !strings.Contains(bundleApp.Name, "-external-secret-db-secret") {
					continue
				}
				objs, err := bundleApp.Config.Generate(bundleApp)
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, op := range objs {
					es, ok := (*op).(*esv1.ExternalSecret)
					if !ok {
						continue
					}
					found = true
					if es.Spec.SecretStoreRef.Name != "cap-store" {
						t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "cap-store")
					}
				}
			}
		}
		for _, child := range node.Children {
			collectApps(child)
		}
	}
	collectApps(cluster.Node)

	if !found {
		t.Error("no ExternalSecret found in cluster")
	}
}

// Inline secretStoreRef overrides the capability rendering (inline wins).
func TestExternalSecretHandler_InlineOverridesCapability_Real(t *testing.T) {
	transformer := oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"webservice": &components.WebserviceHandler{},
		},
		nil,
	)
	transformer.RegisterBuiltinTrait("external-secret", &traits.ExternalSecretHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "api",
				Type:       "webservice",
				Properties: map[string]any{"image": "myimage:v1.0.0"},
				Traits: []oam.Trait{{
					Type: "external-secret",
					Properties: map[string]any{
						"secretName": "db-secret",
						"remoteRef":  map[string]any{"key": "secret/db"},
						// Inline secretStoreRef — should win over capability
						"secretStoreRef": map[string]any{
							"name": "inline-store",
							"kind": "ClusterSecretStore",
						},
					},
				}},
			}},
		},
	}

	ctx := oam.TransformContext{
		Capabilities: map[string]oam.CapabilityBinding{
			"external-secret": {Rendering: map[string]any{
				"secretStoreRef": map[string]any{
					"name": "cap-store",
					"kind": "ClusterSecretStore",
				},
			}},
		},
	}

	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	found := false
	var collectApps func(node *stack.Node)
	collectApps = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil && !node.Bundle.IsUmbrella() {
			for _, bundleApp := range node.Bundle.Applications {
				if !strings.Contains(bundleApp.Name, "-external-secret-db-secret") {
					continue
				}
				objs, err := bundleApp.Config.Generate(bundleApp)
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, op := range objs {
					es, ok := (*op).(*esv1.ExternalSecret)
					if !ok {
						continue
					}
					found = true
					if es.Spec.SecretStoreRef.Name != "inline-store" {
						t.Errorf("SecretStoreRef.Name = %q, want %q (inline should override capability)", es.Spec.SecretStoreRef.Name, "inline-store")
					}
				}
			}
		}
		for _, child := range node.Children {
			collectApps(child)
		}
	}
	collectApps(cluster.Node)

	if !found {
		t.Error("no ExternalSecret found in cluster")
	}
}

// Two traits with provider: shorthand → two ExternalSecret CRs with different stores.
// Uses h.Apply() directly since provider: shorthand is self-contained (no capability needed).
func TestExternalSecretHandler_MultiProvider(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("api", "default", nil)
	bundle := &stack.Bundle{}

	traits_ := []*oam.Trait{
		{
			Type: "external-secret",
			Properties: map[string]any{
				"secretName": "vault-secret",
				"provider":   "vault-backend",
				"remoteRef":  map[string]any{"key": "secret/vault"},
			},
		},
		{
			Type: "external-secret",
			Properties: map[string]any{
				"secretName": "aws-secret",
				"provider":   "aws-secretsmanager",
				"remoteRef":  map[string]any{"key": "secret/aws"},
			},
		},
	}

	for _, trait := range traits_ {
		if err := h.Apply(trait, app, bundle); err != nil {
			t.Fatalf("Apply(%s): %v", trait.Properties["secretName"], err)
		}
	}

	if len(bundle.Applications) != 2 {
		t.Fatalf("expected 2 bundle applications, got %d", len(bundle.Applications))
	}

	storeNames := map[string]string{} // secretName → SecretStoreRef.Name
	for _, bundleApp := range bundle.Applications {
		objs, err := bundleApp.Config.Generate(bundleApp)
		if err != nil {
			t.Fatalf("Generate %s: %v", bundleApp.Name, err)
		}
		for _, op := range objs {
			es, ok := (*op).(*esv1.ExternalSecret)
			if !ok {
				continue
			}
			storeNames[es.Name] = es.Spec.SecretStoreRef.Name
		}
	}

	wantStores := map[string]string{
		"vault-secret": "vault-backend",
		"aws-secret":   "aws-secretsmanager",
	}
	for secretName, wantStore := range wantStores {
		if got, ok := storeNames[secretName]; !ok {
			t.Errorf("ExternalSecret %q not found in cluster", secretName)
		} else if got != wantStore {
			t.Errorf("ExternalSecret %q SecretStoreRef.Name = %q, want %q", secretName, got, wantStore)
		}
	}
}
