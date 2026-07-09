package traits_test

import (
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// esFromData applies an external-secret trait with the given data entries (in
// namespace "prod", secretName "app-secrets") and returns the generated ExternalSecret.
func esFromData(t *testing.T, data []any) *esv1.ExternalSecret {
	t.Helper()
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("app", "prod", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":     "app-secrets",
			"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
			"data":           data,
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
		t.Fatalf("expected *esv1.ExternalSecret, got %T", *objs[0])
	}
	return es
}

// esDataErr applies an external-secret trait with the given data and returns the
// Apply error (or fails if none).
func esDataErr(t *testing.T, data []any) string {
	t.Helper()
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("app", "prod", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":     "app-secrets",
			"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
			"data":           data,
		},
	}
	err := h.Apply(trait, app, bundle)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	return err.Error()
}

func assertData(t *testing.T, d esv1.ExternalSecretData, wantKey, wantProp, wantRef string) {
	t.Helper()
	if d.SecretKey != wantKey {
		t.Errorf("SecretKey = %q, want %q", d.SecretKey, wantKey)
	}
	if d.RemoteRef.Key != wantRef {
		t.Errorf("RemoteRef.Key = %q, want %q", d.RemoteRef.Key, wantRef)
	}
	if d.RemoteRef.Property != wantProp {
		t.Errorf("RemoteRef.Property = %q, want %q", d.RemoteRef.Property, wantProp)
	}
}

// Fully-derivable entry: {secretKey} only → key="<ns>/<secretName>", property=secretKey.
func TestExternalSecret_DataShorthand_FullDerivation(t *testing.T) {
	es := esFromData(t, []any{
		map[string]any{"secretKey": "DB_PASSWORD"},
	})
	if len(es.Spec.Data) != 1 {
		t.Fatalf("len(Data) = %d, want 1", len(es.Spec.Data))
	}
	assertData(t, es.Spec.Data[0], "DB_PASSWORD", "DB_PASSWORD", "prod/app-secrets")
}

// remoteRef.key authored → key kept, property still derived from secretKey.
func TestExternalSecret_DataShorthand_KeyOverride(t *testing.T) {
	es := esFromData(t, []any{
		map[string]any{"secretKey": "TOKEN", "remoteRef": map[string]any{"key": "shared/token"}},
	})
	assertData(t, es.Spec.Data[0], "TOKEN", "TOKEN", "shared/token")
}

// remoteRef.property authored → property kept, key still derived.
func TestExternalSecret_DataShorthand_PropertyOverride(t *testing.T) {
	es := esFromData(t, []any{
		map[string]any{"secretKey": "API_KEY", "remoteRef": map[string]any{"property": "apiKey"}},
	})
	assertData(t, es.Spec.Data[0], "API_KEY", "apiKey", "prod/app-secrets")
}

// Fully-explicit entry (back-compat) → nothing derived.
func TestExternalSecret_DataShorthand_ExplicitBackCompat(t *testing.T) {
	es := esFromData(t, []any{
		map[string]any{"secretKey": "X", "remoteRef": map[string]any{"key": "k/path", "property": "p"}},
	})
	assertData(t, es.Spec.Data[0], "X", "p", "k/path")
}

// Mixed list mirroring an opsmaster block: derived entries + the outlier with an
// explicit key that differs from <ns>/<secretName>.
func TestExternalSecret_DataShorthand_MixedWithOutlier(t *testing.T) {
	es := esFromData(t, []any{
		map[string]any{"secretKey": "USER"},
		map[string]any{"secretKey": "PASS"},
		map[string]any{"secretKey": "LEGACY", "remoteRef": map[string]any{"key": "legacy/vault/path"}},
	})
	if len(es.Spec.Data) != 3 {
		t.Fatalf("len(Data) = %d, want 3", len(es.Spec.Data))
	}
	assertData(t, es.Spec.Data[0], "USER", "USER", "prod/app-secrets")
	assertData(t, es.Spec.Data[1], "PASS", "PASS", "prod/app-secrets")
	assertData(t, es.Spec.Data[2], "LEGACY", "LEGACY", "legacy/vault/path")
}

// version/decodingStrategy authored are preserved; still derive key+property.
func TestExternalSecret_DataShorthand_PreservesOptionalRefFields(t *testing.T) {
	es := esFromData(t, []any{
		map[string]any{"secretKey": "K", "remoteRef": map[string]any{"version": "v2", "decodingStrategy": "Base64"}},
	})
	d := es.Spec.Data[0]
	assertData(t, d, "K", "K", "prod/app-secrets")
	if d.RemoteRef.Version != "v2" {
		t.Errorf("Version = %q, want v2", d.RemoteRef.Version)
	}
	if string(d.RemoteRef.DecodingStrategy) != "Base64" {
		t.Errorf("DecodingStrategy = %q, want Base64", d.RemoteRef.DecodingStrategy)
	}
}

// Unknown key at the entry level is rejected, naming the offending field.
func TestExternalSecret_DataShorthand_RejectsUnknownEntryKey(t *testing.T) {
	msg := esDataErr(t, []any{
		map[string]any{"secretKey": "X", "remteRef": map[string]any{"key": "k"}},
	})
	if !strings.Contains(msg, "unsupported field") || !strings.Contains(msg, "remteRef") {
		t.Errorf("error = %q, want mention of unsupported field 'remteRef'", msg)
	}
}

// Unknown key inside remoteRef is rejected, naming the offending field.
func TestExternalSecret_DataShorthand_RejectsUnknownRemoteRefKey(t *testing.T) {
	msg := esDataErr(t, []any{
		map[string]any{"secretKey": "X", "remoteRef": map[string]any{"ky": "typo"}},
	})
	if !strings.Contains(msg, "unsupported field") || !strings.Contains(msg, "ky") {
		t.Errorf("error = %q, want mention of unsupported field 'ky'", msg)
	}
}

// A present-but-wrong-typed remoteRef field is rejected, not silently discarded and
// derived (the failure class strict mode exists to prevent).
func TestExternalSecret_DataShorthand_RejectsNonStringRefField(t *testing.T) {
	for _, field := range []string{"key", "property", "version", "decodingStrategy"} {
		t.Run(field, func(t *testing.T) {
			msg := esDataErr(t, []any{
				map[string]any{"secretKey": "X", "remoteRef": map[string]any{field: 12345}},
			})
			if !strings.Contains(msg, field) || !strings.Contains(msg, "must be a string") {
				t.Errorf("error = %q, want '%s: must be a string'", msg, field)
			}
		})
	}
}

// remoteRef present but not an object is rejected.
func TestExternalSecret_DataShorthand_RejectsNonObjectRemoteRef(t *testing.T) {
	msg := esDataErr(t, []any{
		map[string]any{"secretKey": "X", "remoteRef": "not-an-object"},
	})
	if !strings.Contains(msg, "remoteRef") || !strings.Contains(msg, "object") {
		t.Errorf("error = %q, want remoteRef-must-be-object error", msg)
	}
}

// secretKey remains required.
func TestExternalSecret_DataShorthand_RequiresSecretKey(t *testing.T) {
	msg := esDataErr(t, []any{
		map[string]any{"remoteRef": map[string]any{"key": "k"}},
	})
	if !strings.Contains(msg, "secretKey") {
		t.Errorf("error = %q, want secretKey-required error", msg)
	}
}

// Guard: the untouched top-level remoteRef shorthand stays mutually exclusive with data.
func TestExternalSecret_TopLevelRemoteRef_StillExclusiveWithData(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("app", "prod", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":     "app-secrets",
			"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
			"remoteRef":      map[string]any{"key": "secret/db"},
			"data":           []any{map[string]any{"secretKey": "X"}},
		},
	}
	err := h.Apply(trait, app, bundle)
	if err == nil {
		t.Fatal("expected mutual-exclusion error for remoteRef + data, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("error = %q, want 'cannot be combined'", err.Error())
	}
}
