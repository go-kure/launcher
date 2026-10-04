package traits_test

import (
	"encoding/base64"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The secret trait (go-kure/launcher#786): a Secret the document carries,
// emitted under data, owned by the component, refusable by policy, and never
// quoted in an error.

// secretSentinel is the value no error may carry.
const secretSentinel = "s3cr3t-sentinel-value"

// noExplicitSecrets is NoopPolicy forbidding explicit secrets.
type noExplicitSecrets struct{ *oam.NoopPolicy }

func (noExplicitSecrets) AllowExplicitSecrets() bool { return false }

// applySecret applies a secret trait named "creds" with props to component
// "api" and returns the sub-application it appended.
func applySecret(t *testing.T, props map[string]any) (*stack.Application, error) {
	t.Helper()
	p := map[string]any{"name": "creds"}
	maps.Copy(p, props)
	bundle := newBundle()
	err := (&traits.SecretHandler{}).Apply(&oam.Trait{Type: "secret", Properties: p}, newApp("api", "shop"), bundle)
	if err != nil {
		if len(bundle.Applications) != 0 {
			t.Errorf("refused trait still added %d bundle app(s)", len(bundle.Applications))
		}
		return nil, err
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle app, got %d", len(bundle.Applications))
	}
	return bundle.Applications[0], nil
}

func TestSecretHandler_CanHandle(t *testing.T) {
	h := &traits.SecretHandler{}
	if !h.CanHandle("secret") || h.CanHandle("configmap") || h.CanHandle("external-secret") {
		t.Error("CanHandle must be true for secret only")
	}
}

func TestSecretHandler_ValidateAndApplyDefaults_RejectsUnknownKey(t *testing.T) {
	h := &traits.SecretHandler{}
	if _, err := h.ValidateAndApplyDefaults(map[string]any{"unknown": true}); err == nil {
		t.Fatal("expected an error for an unknown rendering key")
	}
	if _, err := h.ValidateAndApplyDefaults(nil); err != nil {
		t.Fatalf("empty rendering: %v", err)
	}
}

// TestSecretHandler_Generate: stringData and data both land under data, decoded
// (the API serializes them base64), with the component's label, the trait's
// name, the application's namespace, the authored type and immutable, and no
// stringData.
func TestSecretHandler_Generate(t *testing.T) {
	app, err := applySecret(t, map[string]any{
		"stringData": map[string]any{"password": secretSentinel},
		"data":       map[string]any{"token": base64.StdEncoding.EncodeToString([]byte("raw\x00bytes"))},
		"type":       "kubernetes.io/basic-auth",
		"immutable":  true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("Generate returned %d objects, want 1", len(objects))
	}
	secret, ok := (*objects[0]).(*corev1.Secret)
	if !ok {
		t.Fatalf("object = %T, want *corev1.Secret", *objects[0])
	}
	if secret.Name != "creds" || secret.Namespace != "shop" {
		t.Errorf("Secret %s/%s, want shop/creds", secret.Namespace, secret.Name)
	}
	if secret.Kind != "Secret" || secret.APIVersion != "v1" {
		t.Errorf("TypeMeta = %s %s, want v1 Secret", secret.APIVersion, secret.Kind)
	}
	if want := map[string]string{"app": oam.ComponentLabelValue("api")}; !maps.Equal(secret.Labels, want) {
		t.Errorf("labels = %v, want %v", secret.Labels, want)
	}
	if len(secret.Data) != 2 || string(secret.Data["password"]) != secretSentinel || string(secret.Data["token"]) != "raw\x00bytes" {
		t.Errorf("data keys %d, want password and token decoded", len(secret.Data))
	}
	if secret.StringData != nil {
		t.Error("stringData is emitted; every entry belongs under data")
	}
	if secret.Type != "kubernetes.io/basic-auth" {
		t.Errorf("type = %q", secret.Type)
	}
	if secret.Immutable == nil || !*secret.Immutable {
		t.Errorf("immutable = %v, want true", secret.Immutable)
	}
}

// TestSecretHandler_Generate_Defaults: with only a name the Secret has no
// entries, no type and no immutable.
func TestSecretHandler_Generate_Defaults(t *testing.T) {
	app, err := applySecret(t, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	secret := (*objects[0]).(*corev1.Secret)
	if secret.Data != nil || secret.Type != "" || secret.Immutable != nil {
		t.Errorf("Secret = data %v type %q immutable %v, want all unset", secret.Data, secret.Type, secret.Immutable)
	}
}

// TestSecretHandler_Apply_Refusals: every refusal names the key or the type of
// what is wrong and never a value, a base64 error included.
func TestSecretHandler_Apply_Refusals(t *testing.T) {
	limit := corev1.MaxSecretSize
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"stringData not an object", map[string]any{"stringData": secretSentinel}, `secret trait "creds": stringData`},
		{"stringData value not a string", map[string]any{"stringData": map[string]any{"pin": 12345}},
			`secret trait "creds": stringData.pin: must be a string, got int; quote the value`},
		{"stringData nested value", map[string]any{"stringData": map[string]any{"auth": map[string]any{"password": secretSentinel}}},
			`secret trait "creds": stringData.auth: must be a string, got map[string]interface {}`},
		{"data not base64", map[string]any{"data": map[string]any{"token": secretSentinel + "!"}},
			`secret trait "creds": data.token: invalid base64`},
		{"data value not a string", map[string]any{"data": map[string]any{"token": []any{secretSentinel}}},
			`secret trait "creds": data.token: must be a base64-encoded string, got []interface {}`},
		{"key in both", map[string]any{
			"stringData": map[string]any{"password": secretSentinel},
			"data":       map[string]any{"password": base64.StdEncoding.EncodeToString([]byte(secretSentinel))},
		}, `secret trait "creds": data.password: key also appears in stringData`},
		{"invalid key", map[string]any{"stringData": map[string]any{"bad key": secretSentinel}},
			`secret trait "creds": stringData: invalid key "bad key"`},
		{"a byte over the limit", map[string]any{"stringData": map[string]any{"a": strings.Repeat("a", limit/2), "b": strings.Repeat("b", limit/2+1)}},
			`secret trait "creds": stringData and data hold 1048577 bytes, over the 1048576-byte limit`},
		{"type not a string", map[string]any{"type": 1}, `secret trait "creds": type`},
		{"immutable not a bool", map[string]any{"immutable": "yes"}, `secret trait "creds": immutable`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applySecret(t, tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Apply error = %v, want one containing %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), secretSentinel) {
				t.Errorf("the refusal carries the value: %v", err)
			}
		})
	}
}

// TestSecretHandler_Apply_AtTheSizeLimit: exactly 1 MiB of decoded values is
// accepted.
func TestSecretHandler_Apply_AtTheSizeLimit(t *testing.T) {
	if _, err := applySecret(t, map[string]any{"stringData": map[string]any{"k": strings.Repeat("a", corev1.MaxSecretSize)}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestSecretHandler_Apply_MissingName(t *testing.T) {
	err := (&traits.SecretHandler{}).Apply(&oam.Trait{Type: "secret", Properties: map[string]any{}}, newApp("api", "shop"), newBundle())
	if err == nil || !strings.Contains(err.Error(), "required property 'name'") {
		t.Fatalf("err = %v, want the missing-name refusal", err)
	}
}

// TestSecretHandler_Apply_Name: the name is the Secret's metadata.name, so it
// must be a DNS-1123 subdomain; a legal one of the longest length is accepted.
func TestSecretHandler_Apply_Name(t *testing.T) {
	apply := func(name string) error {
		bundle := newBundle()
		return (&traits.SecretHandler{}).Apply(&oam.Trait{Type: "secret", Properties: map[string]any{
			"name": name, "stringData": map[string]any{"password": secretSentinel}}}, newApp("api", "shop"), bundle)
	}
	for _, name := range []string{"BAD_NAME", "-creds", "creds.", "a/b", strings.Repeat("a", 254)} {
		err := apply(name)
		if err == nil || !strings.Contains(err.Error(), "is not a valid DNS-1123 subdomain") {
			t.Errorf("name %q: err = %v, want the name refused", name, err)
			continue
		}
		if strings.Contains(err.Error(), secretSentinel) {
			t.Errorf("name %q: the refusal carries the value: %v", name, err)
		}
	}
	for _, name := range []string{"creds", "app.creds-1", strings.Repeat("a", 253)} {
		if err := apply(name); err != nil {
			t.Errorf("name %q: %v", name, err)
		}
	}
}

// TestSecretConfig_ApplyPolicy: a policy that forbids explicit secrets refuses
// the trait, naming the Secret and no value; one that does not implement the
// optional interface, and no policy at all, allow it.
func TestSecretConfig_ApplyPolicy(t *testing.T) {
	app, err := applySecret(t, map[string]any{"stringData": map[string]any{"password": secretSentinel}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	enforceable, ok := app.Config.(oam.Enforceable)
	if !ok {
		t.Fatalf("%T does not implement oam.Enforceable", app.Config)
	}
	err = enforceable.ApplyPolicy(noExplicitSecrets{&oam.NoopPolicy{}})
	if err == nil || !strings.Contains(err.Error(), `secret "creds": the environment policy forbids explicit secrets`) {
		t.Fatalf("err = %v, want the explicit-secret refusal", err)
	}
	if strings.Contains(err.Error(), secretSentinel) {
		t.Errorf("the refusal carries the value: %v", err)
	}
	if err := enforceable.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
		t.Errorf("a policy without the optional interface refused the trait: %v", err)
	}
	if err := enforceable.ApplyPolicy(nil); err != nil {
		t.Errorf("no policy refused the trait: %v", err)
	}
}

// TestSecretConfig_FluxNamespaceInput: the sub-application names its Secret, so
// it follows a Flux object of its component that reads it.
func TestSecretConfig_FluxNamespaceInput(t *testing.T) {
	app, err := applySecret(t, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	input, ok := app.Config.(interface{ FluxNamespaceInput() (string, string) })
	if !ok {
		t.Fatalf("%T has no FluxNamespaceInput", app.Config)
	}
	if kind, name := input.FluxNamespaceInput(); kind != "Secret" || name != "creds" {
		t.Errorf("FluxNamespaceInput = %s/%s, want Secret/creds", kind, name)
	}
}

// TestExplicitSecretsAllowed pins the optional interface's default: only a
// policy that implements it and answers false forbids.
func TestExplicitSecretsAllowed(t *testing.T) {
	if !oam.ExplicitSecretsAllowed(nil) || !oam.ExplicitSecretsAllowed(&oam.NoopPolicy{}) {
		t.Error("no policy, or one without the optional interface, must allow explicit secrets")
	}
	if oam.ExplicitSecretsAllowed(noExplicitSecrets{&oam.NoopPolicy{}}) {
		t.Error("a policy answering false must forbid explicit secrets")
	}
}
