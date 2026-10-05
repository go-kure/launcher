package kurel

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the secret trait as the secret kind's twin
// (go-kure/launcher#790): the same properties build the same Secret on both
// paths, through the real `kurel build` entry point, and both are refused for
// the same reasons, the environment policy's included. Only the ownership
// fields may differ: the Secret's `app` label names the owning component on the
// trait path and the Secret itself on the kind path.

// secretTwinApp returns an Application holding the Secret both ways: the kind
// path as a secret component named name, or the trait path as a secret trait of
// that name on a job component named "owner".
func secretTwinApp(t *testing.T, viaTrait bool, name string, props map[string]any, ownerTraits ...string) string {
	t.Helper()
	return kindTraitTwinApp(t, "secret", viaTrait, name, props, ownerTraits...)
}

// secretDoc returns the Secret named name among docs.
func secretDoc(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		if d["kind"] == "Secret" && md["name"] == name {
			return d
		}
	}
	t.Fatalf("no Secret %q among %d documents", name, len(docs))
	return nil
}

// TestSecretTwin_SameSecretBothWays builds each intent as a secret component
// and as a secret trait and requires the two Secrets to be identical apart from
// the labels that name the owner.
func TestSecretTwin_SameSecretBothWays(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name        string
		props       map[string]any
		ownerTraits []string
		// wantData is the Secret's data as the output carries it.
		wantData map[string]any
	}{
		{name: "stringData", props: map[string]any{"stringData": map[string]any{"password": "hunter2", "empty": ""}},
			wantData: map[string]any{"password": b64("hunter2"), "empty": ""}},
		{name: "data", props: map[string]any{"data": map[string]any{"token": b64("raw\x00bytes")}},
			wantData: map[string]any{"token": b64("raw\x00bytes")}},
		{name: "stringData and data, typed", props: map[string]any{
			"stringData": map[string]any{"username": "admin"},
			"data":       map[string]any{"password": b64("hunter2")},
			"type":       "kubernetes.io/basic-auth",
		}, wantData: map[string]any{"username": b64("admin"), "password": b64("hunter2")}},
		{name: "immutable true", props: map[string]any{"stringData": map[string]any{"k": "v"}, "immutable": true},
			wantData: map[string]any{"k": b64("v")}},
		{name: "immutable false", props: map[string]any{"stringData": map[string]any{"k": "v"}, "immutable": false},
			wantData: map[string]any{"k": b64("v")}},
		{name: "prune-protection on the owner", props: map[string]any{"stringData": map[string]any{"k": "v"}},
			ownerTraits: []string{"prune-protection"}, wantData: map[string]any{"k": b64("v")}},
		// No entry, type or immutable authored: the Secret carries none.
		{name: "no properties", props: map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kindApp := secretTwinApp(t, false, "creds", tc.props, tc.ownerTraits...)
			traitApp := secretTwinApp(t, true, "creds", tc.props, tc.ownerTraits...)
			kindDocs, kindErr, err := buildPVCDocs(t, kindApp)
			if err != nil {
				t.Fatalf("kind path: %v\n%s", err, kindErr)
			}
			traitDocs, traitErr, err := buildPVCDocs(t, traitApp)
			if err != nil {
				t.Fatalf("trait path: %v\n%s", err, traitErr)
			}
			kindSecret := secretDoc(t, kindDocs, "creds")
			traitSecret := secretDoc(t, traitDocs, "creds")

			// The ownership fields: both labels name whoever owns the Secret.
			for _, label := range []string{"app", kurelComponentLabel} {
				if got := labelOf(kindSecret, label); got != "creds" {
					t.Errorf("kind Secret label %s = %q, want %q", label, got, "creds")
				}
				if got := labelOf(traitSecret, label); got != "owner" {
					t.Errorf("trait Secret label %s = %q, want the owner %q", label, got, "owner")
				}
			}
			for _, s := range []map[string]any{kindSecret, traitSecret} {
				labels := s["metadata"].(map[string]any)["labels"].(map[string]any)
				delete(labels, "app")
				delete(labels, kurelComponentLabel)
			}

			if !reflect.DeepEqual(kindSecret, traitSecret) {
				k, _ := yaml.Marshal(kindSecret)
				tr, _ := yaml.Marshal(traitSecret)
				t.Errorf("Secrets differ beyond the owner's labels\nkind:\n%s\ntrait:\n%s", k, tr)
			}
			// Each authored field must reach the Secret with its authored value.
			// Both paths share one generator, so their equality alone cannot
			// catch a value the generator gets wrong on both.
			if got, emitted := traitSecret["data"]; emitted != (tc.wantData != nil) || emitted && !reflect.DeepEqual(got, tc.wantData) {
				t.Errorf("Secret data = %#v (emitted %t), want %#v", got, emitted, tc.wantData)
			}
			// The object is a v1 Secret in the application's namespace.
			if ns := traitSecret["metadata"].(map[string]any)["namespace"]; traitSecret["apiVersion"] != "v1" || ns != "default" {
				t.Errorf("Secret is %v in namespace %v, want v1 in %q", traitSecret["apiVersion"], ns, "default")
			}
			if _, emitted := traitSecret["stringData"]; emitted {
				t.Error("stringData is emitted; every entry belongs under data")
			}
			for _, field := range []string{"type", "immutable"} {
				want, authored := tc.props[field]
				if got, emitted := traitSecret[field]; emitted != authored || !reflect.DeepEqual(got, want) {
					t.Errorf("Secret %s = %#v (emitted %t), want %#v (authored %t)", field, got, emitted, want, authored)
				}
			}
			assertTwinDeliveryIntent(t, "creds", tc.ownerTraits, kindApp, traitApp)
			assertNoFluxObjectKeys(t, kindSecret, traitSecret)
		})
	}
}

// TestSecretTwin_SameRefusalsBothWays requires both paths to refuse the same
// malformed properties with the same message, and neither to quote the value.
func TestSecretTwin_SameRefusalsBothWays(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{name: "number value", props: map[string]any{"stringData": map[string]any{"pin": 12345}}, want: "stringData.pin: must be a string, got int; quote the value"},
		{name: "nested value", props: map[string]any{"stringData": map[string]any{"auth": map[string]any{"password": secretSentinel}}},
			want: "stringData.auth: must be a string"},
		{name: "invalid key", props: map[string]any{"stringData": map[string]any{"a/b": secretSentinel}}, want: `stringData: invalid key "a/b"`},
		{name: "data key also in stringData", props: map[string]any{
			"stringData": map[string]any{"k": secretSentinel},
			"data":       map[string]any{"k": base64.StdEncoding.EncodeToString([]byte(secretSentinel))},
		}, want: "data.k: key also appears in stringData"},
		{name: "invalid base64", props: map[string]any{"data": map[string]any{"k": secretSentinel + "!"}}, want: "data.k: invalid base64"},
		{name: "over the size limit", props: map[string]any{"stringData": map[string]any{"k": strings.Repeat("a", corev1.MaxSecretSize+1)}},
			want: "stringData and data hold 1048577 bytes, over the 1048576-byte limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, viaTrait := range []bool{false, true} {
				_, stderr, err := buildPVCDocs(t, secretTwinApp(t, viaTrait, "creds", tc.props))
				if err == nil {
					t.Errorf("viaTrait=%v: build succeeded, want a refusal containing %q", viaTrait, tc.want)
					continue
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("viaTrait=%v: error does not contain %q", viaTrait, tc.want)
				}
				for i, form := range secretForms(tc.props) {
					if strings.Contains(err.Error(), form) || strings.Contains(stderr, form) {
						t.Errorf("viaTrait=%v: the refusal carries a value of the case (form %d of secretForms)", viaTrait, i)
					}
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

// TestSecretTwin_FormsFindEachLeakedForm is the control of the no-value
// assertion above: a refusal that carried a value in any of the forms is found,
// each by its own form, and a refusal that carries none is not.
func TestSecretTwin_FormsFindEachLeakedForm(t *testing.T) {
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

// TestSecretTwin_PolicyForbidsBothWays: a policy that forbids explicit secrets
// refuses the secret component as it refuses the secret trait, with a violation
// naming the component and no value; a policy that does not implement the
// optional interface, and no policy, build both.
func TestSecretTwin_PolicyForbidsBothWays(t *testing.T) {
	entries := map[string]any{"stringData": map[string]any{"password": secretSentinel}}
	cases := map[string]oam.Component{
		"kind": {Name: "creds", Type: "secret", Properties: entries},
		"trait": {Name: "owner", Type: "configmap", Properties: map[string]any{"data": map[string]any{"k": "v"}},
			Traits: []oam.Trait{{Type: "secret", Properties: map[string]any{
				"name": "creds", "stringData": map[string]any{"password": secretSentinel}}}}},
	}
	for path, comp := range cases {
		t.Run(path, func(t *testing.T) {
			_, err := generateSecretApp("", noExplicitSecrets{&oam.NoopPolicy{}}, comp)
			assertNoSentinel(t, err)
			assertViolation(t, err, comp.Name)

			for name, policy := range map[string]oam.Policy{"a policy without the optional interface": permissivePolicy{&oam.NoopPolicy{}}, "no policy": nil} {
				apps, err := generateSecretApp("", policy, comp)
				if err != nil {
					t.Errorf("%s refused it: %v", name, err)
					continue
				}
				var secrets []string
				for _, a := range apps {
					for _, o := range a.Objects {
						if s, ok := (*o).(*corev1.Secret); ok {
							secrets = append(secrets, s.Namespace+"/"+s.Name)
						}
					}
				}
				if !reflect.DeepEqual(secrets, []string{"default/creds"}) {
					t.Errorf("%s: built Secrets %v, want default/creds alone", name, secrets)
				}
			}
		})
	}
}

// TestSecretKind_HeldAgainstAHelmValuesSecret: the Secret a helm component
// generates for secretValues is named under role "values-secret", as a Secret,
// so a secret component under that name is refused with both named, whichever
// the document lists first.
func TestSecretKind_HeldAgainstAHelmValuesSecret(t *testing.T) {
	kind := claimKind(chartSecretDefault, "secret", "        stringData:\n          k: v\n")
	if err := transformErr(t, helmNamesApp(namingChart+claimKind("other", "secret", "        stringData:\n          k: v\n")), oam.TransformContext{}); err != nil {
		t.Fatalf("a secret component under a name of its own: %v, want it accepted beside the chart", err)
	}
	for order, components := range map[string]string{"kind first": kind + namingChart, "chart first": namingChart + kind} {
		err := transformErr(t, helmNamesApp(components), oam.TransformContext{})
		for _, want := range []string{
			`name collision: Secret "default/` + chartSecretDefault + `" is named by `,
			`component "` + chartSecretDefault + `" (role "object", its default)`,
			`component "chart" (role "values-secret", its default)`,
		} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %v\nwant it to contain %q", order, err, want)
			}
		}
	}
}
