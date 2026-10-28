package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// madeObject is the engine's componentObjectMaker, which a kind config answers
// to report the object its controller makes (go-kure/launcher#1086).
type madeObject interface {
	MadeObject(objectName string) (kind, name string, ok bool)
}

// TestMadeObject_CertificateAndExternalSecret: a Certificate's controller
// makes the Secret secretName names; an ExternalSecret's makes its target,
// named target.name, else after the ExternalSecret, under every
// creationPolicy that creates it, and none the engine can prove under Merge,
// None or a target.manifest.
func TestMadeObject_CertificateAndExternalSecret(t *testing.T) {
	store := map[string]any{"name": "vault", "kind": "ClusterSecretStore"}
	data := []any{map[string]any{"secretKey": "k", "remoteRef": map[string]any{"key": "prod/app"}}}
	externalSecret := func(target map[string]any) map[string]any {
		props := map[string]any{"secretStoreRef": store, "data": data}
		if target != nil {
			props["target"] = target
		}
		return props
	}
	for _, tc := range []struct {
		name       string
		handler    oam.ComponentHandler
		typ        string
		props      map[string]any
		objectName string
		wantName   string
		wantOK     bool
	}{
		{"certificate: secretName", &components.CertificateHandler{}, "certificate", map[string]any{
			"secretName": "web-tls", "dnsNames": []any{"web.example.com"},
			"issuerRef": map[string]any{"name": "selfsigned", "kind": "ClusterIssuer"}}, "", "web-tls", true},
		{"externalsecret: named after the component", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(nil), "", "creds", true},
		{"externalsecret: named after its objectName", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(nil), "other", "other", true},
		{"externalsecret: target.name", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"name": "target"}), "other", "target", true},
		{"externalsecret: Owner", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"creationPolicy": "Owner"}), "", "creds", true},
		{"externalsecret: Orphan", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"creationPolicy": "Orphan"}), "", "creds", true},
		{"externalsecret: CreateOrMerge", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"creationPolicy": "CreateOrMerge"}), "", "creds", true},
		{"externalsecret: Merge", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"creationPolicy": "Merge"}), "", "", false},
		{"externalsecret: None", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"creationPolicy": "None"}), "", "", false},
		{"externalsecret: target.manifest", &components.ExternalSecretHandler{}, "externalsecret", externalSecret(map[string]any{"manifest": map[string]any{"apiVersion": "v1", "kind": "ConfigMap"}}), "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "creds"
			if tc.typ == "certificate" {
				name = "web"
			}
			cfg, err := tc.handler.ToApplicationConfig(&oam.Component{Name: name, Type: tc.typ, Properties: tc.props}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			maker, ok := cfg.(madeObject)
			if !ok {
				t.Fatalf("%T does not report the object its controller makes", cfg)
			}
			objectName := tc.objectName
			if objectName == "" {
				objectName = name
			}
			kind, got, ok := maker.MadeObject(objectName)
			if ok != tc.wantOK || got != tc.wantName || (ok && kind != "Secret") {
				t.Errorf("MadeObject(%q) = %q, %q, %v; want Secret, %q, %v", objectName, kind, got, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}
