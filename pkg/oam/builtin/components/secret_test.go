package components_test

import (
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The secret kind component (go-kure/launcher#790) emits exactly one Secret,
// named after the component and carrying the component's `app` label, built by
// the parser and the generator the secret trait runs. Every entry lands under
// data, a policy may forbid it, and no refusal carries a value.

func TestSecretHandler_CanHandle(t *testing.T) {
	h := &components.SecretHandler{}
	if !h.CanHandle("secret") || h.CanHandle("configmap") || h.CanHandle("external-secret") {
		t.Error("CanHandle must be true for secret only")
	}
}

// An unauthored type and immutable stay unset: the kind is a projection, and
// Opaque is what the API server stores for an unset type.
func TestSecretHandler_Generate(t *testing.T) {
	h := &components.SecretHandler{}

	cfg, err := kindConfig(t, h, "secret", "creds", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	secret := generateOne(t, cfg, "creds").(*corev1.Secret)
	if secret.Data != nil || secret.StringData != nil || secret.Type != "" || secret.Immutable != nil {
		t.Errorf("empty secret = data %d, stringData %d, type %q, immutable %v; want all unset",
			len(secret.Data), len(secret.StringData), secret.Type, secret.Immutable)
	}
	if secret.Kind != "Secret" || secret.APIVersion != "v1" {
		t.Errorf("TypeMeta = %s %s, want v1 Secret", secret.APIVersion, secret.Kind)
	}

	cfg, err = kindConfig(t, h, "secret", "creds", map[string]any{
		"stringData": map[string]any{"password": esSentinel, "empty": ""},
		"data":       map[string]any{"token": base64.StdEncoding.EncodeToString([]byte("raw\x00bytes"))},
		"type":       "kubernetes.io/basic-auth",
		"immutable":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret = generateOne(t, cfg, "creds").(*corev1.Secret)
	want := map[string]string{"password": esSentinel, "empty": "", "token": "raw\x00bytes"}
	if len(secret.Data) != len(want) {
		t.Errorf("data holds %d entries, want %d", len(secret.Data), len(want))
	}
	for k, v := range want {
		if got, ok := secret.Data[k]; !ok || string(got) != v {
			t.Errorf("data[%s] is not the authored value decoded (present %t)", k, ok)
		}
	}
	if secret.StringData != nil {
		t.Error("stringData is emitted; every entry belongs under data")
	}
	if secret.Type != "kubernetes.io/basic-auth" {
		t.Errorf("type = %q, want the authored one", secret.Type)
	}
	if secret.Immutable == nil || !*secret.Immutable {
		t.Errorf("immutable = %v, want true", secret.Immutable)
	}
	if labels := secret.GetLabels(); !maps.Equal(labels, map[string]string{"app": "creds"}) {
		t.Errorf("labels = %v, want the app label alone", labels)
	}
}

// Every refusal names the key or the type of what is wrong and never a value, a
// base64 error included.
func TestSecretHandler_Refusals(t *testing.T) {
	h := &components.SecretHandler{}
	limit := corev1.MaxSecretSize
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"stringData not an object":      {map[string]any{"stringData": esSentinel}, "stringData"},
		"stringData value not a string": {map[string]any{"stringData": map[string]any{"pin": 12345}}, "stringData.pin: must be a string, got int; quote the value"},
		"stringData nested value": {map[string]any{"stringData": map[string]any{"auth": map[string]any{"password": esSentinel}}},
			"stringData.auth: must be a string, got map[string]interface {}"},
		"stringData bad key": {map[string]any{"stringData": map[string]any{"bad key": esSentinel}}, `stringData: invalid key "bad key"`},
		"data not an object": {map[string]any{"data": esSentinel}, "data"},
		"data bad key": {map[string]any{"data": map[string]any{"a/b": base64.StdEncoding.EncodeToString([]byte(esSentinel))}},
			`data: invalid key "a/b"`},
		"data not base64": {map[string]any{"data": map[string]any{"token": esSentinel + "!"}}, "data.token: invalid base64"},
		"data value not a string": {map[string]any{"data": map[string]any{"token": []any{esSentinel}}},
			"data.token: must be a base64-encoded string, got []interface {}"},
		"key in both": {map[string]any{
			"stringData": map[string]any{"password": esSentinel},
			"data":       map[string]any{"password": base64.StdEncoding.EncodeToString([]byte(esSentinel))},
		}, "data.password: key also appears in stringData"},
		"a byte over the limit": {map[string]any{
			"stringData": map[string]any{"a": strings.Repeat("a", limit/2)},
			"data":       map[string]any{"b": base64.StdEncoding.EncodeToString(make([]byte, limit-limit/2+1))},
		}, "stringData and data hold 1048577 bytes, over the 1048576-byte limit"},
		"type not a string":    {map[string]any{"type": 1}, "type"},
		"immutable not a bool": {map[string]any{"immutable": "yes"}, "immutable: must be a boolean"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kindConfig(t, h, "secret", "creds", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
			for i, form := range secretForms(tc.props) {
				if strings.Contains(err.Error(), form) {
					t.Errorf("the refusal carries a value of the case (form %d of secretForms)", i)
				}
			}
		})
	}
}

// secretForms returns each value props carries under stringData and data in
// the forms a refusal could leak it: as authored, base64-encoded and, where it
// is base64, decoded, each also as %q writes it, which escapes the bytes that
// do not print. Keys are left out, since a refusal names them.
func secretForms(props map[string]any) []string {
	var forms []string
	add := func(s string) {
		quoted := strconv.Quote(s)
		forms = append(forms, s, quoted[1:len(quoted)-1])
	}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case nil:
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		default:
			s := fmt.Sprint(v)
			add(s)
			add(base64.StdEncoding.EncodeToString([]byte(s)))
			if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
				add(string(decoded))
			}
		}
	}
	walk(props["stringData"])
	walk(props["data"])
	return slices.DeleteFunc(forms, func(s string) bool { return s == "" })
}

// TestSecretForms_FindEachLeakedForm is the control of the no-value assertion
// above: a refusal that carried a value in any of the forms is found, each by
// its own form, and a refusal that carries none is not.
func TestSecretForms_FindEachLeakedForm(t *testing.T) {
	plain, binary := "pass\x00word", []byte("tok\x01en")
	forms := secretForms(map[string]any{
		"stringData": map[string]any{"k": plain},
		"data":       map[string]any{"t": base64.StdEncoding.EncodeToString(binary)},
	})
	found := func(msg string) bool {
		return slices.ContainsFunc(forms, func(form string) bool { return strings.Contains(msg, form) })
	}
	for name, leak := range map[string]string{
		"a stringData value":               plain,
		"a stringData value, quoted":       fmt.Sprintf("%q", plain),
		"a stringData value, base64":       base64.StdEncoding.EncodeToString([]byte(plain)),
		"a data value as authored":         base64.StdEncoding.EncodeToString(binary),
		"a data value, decoded":            string(binary),
		"a data value, decoded and quoted": fmt.Sprintf("%q", binary),
	} {
		if !found("data.t: refused: " + leak) {
			t.Errorf("%s in a refusal is not found", name)
		}
	}
	if found("data.t: invalid base64") {
		t.Error("a value is found in a refusal that carries none")
	}
}

// The summed size of the decoded values may reach corev1.MaxSecretSize and no
// further, as ValidateSecret allows, whichever of the two fields holds them.
func TestSecretHandler_AtTheSizeLimit(t *testing.T) {
	h := &components.SecretHandler{}
	half := corev1.MaxSecretSize / 2
	if _, err := kindConfig(t, h, "secret", "creds", map[string]any{
		"stringData": map[string]any{"a": strings.Repeat("a", half)},
		"data":       map[string]any{"b": base64.StdEncoding.EncodeToString(make([]byte, corev1.MaxSecretSize-half))},
	}); err != nil {
		t.Fatalf("err = %v, want the Secret accepted at the limit", err)
	}
}

// With several bad entries, the one reported is the first in sorted key order,
// whatever order the map iterates in. Five bad keys and fifty runs make an
// unsorted loop report "a" every time with a chance of about 5^-50.
func TestSecretHandler_RefusalsInSortedKeyOrder(t *testing.T) {
	h := &components.SecretHandler{}
	five := func(suffix string, v any) map[string]any {
		m := map[string]any{}
		for _, k := range []string{"e", "c", "a", "d", "b"} {
			m[k+suffix] = v
		}
		return m
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"invalid stringData keys":      {map[string]any{"stringData": five("/x", "x")}, `stringData: invalid key "a/x"`},
		"non-string stringData values": {map[string]any{"stringData": five("", 1)}, "stringData.a: must be a string"},
		"invalid data keys":            {map[string]any{"data": five("/x", "eA==")}, `data: invalid key "a/x"`},
		"bad base64 values":            {map[string]any{"data": five("", "not base64!")}, "data.a: invalid base64"},
	} {
		t.Run(name, func(t *testing.T) {
			for range 50 {
				_, err := kindConfig(t, h, "secret", "creds", tc.props)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
				}
			}
		})
	}
}

// A policy that forbids explicit secrets refuses the component, without the
// value; one that allows them, one that does not implement the optional
// interface, and no policy at all, allow it.
func TestSecretComponentConfig_ApplyPolicy(t *testing.T) {
	cfg, err := kindConfig(t, &components.SecretHandler{}, "secret", "creds",
		map[string]any{"stringData": map[string]any{"password": esSentinel}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.(oam.Enforceable); !ok {
		t.Fatalf("%T does not implement oam.Enforceable", cfg)
	}
	enforceable := cfg.(policyApplier)

	err = enforceable.ApplyPolicy(esForbidding())
	if err == nil || !strings.Contains(err.Error(), "secret: the environment policy forbids explicit secrets") {
		t.Fatalf("err = %v, want the explicit-secret refusal", err)
	}
	if strings.Contains(err.Error(), esSentinel) {
		t.Errorf("the refusal carries the value: %v", err)
	}
	if err := enforceable.ApplyPolicy(esPolicy{stubPolicy: &stubPolicy{}, allow: true}); err != nil {
		t.Errorf("a policy that allows explicit secrets refused the component: %v", err)
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("a policy without the optional interface refused the component: %v", err)
	}
	if err := enforceable.ApplyPolicy(nil); err != nil {
		t.Errorf("no policy refused the component: %v", err)
	}
}
