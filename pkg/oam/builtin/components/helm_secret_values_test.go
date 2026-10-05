package components_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// helm secretValues lowers to the helmrelease plus a secret trait on it under
// delivery: flux, and is forwarded to the helmtemplate under delivery: template
// (go-kure/launcher#786). These tests pin the lowering's own output; the Secret
// the trait emits is checked end to end in pkg/cmd/kurel.

// sensitive is the value no lowered property but the secret trait's, and no
// error, may carry.
const sensitive = "s3cr3t-sentinel-value"

// helmSecretProps is a helm component referencing an existing HelmRepository
// with plain values, secretValues and one authored valuesFrom entry.
func helmSecretProps(secretValues any) map[string]any {
	return map[string]any{
		"chart":        "podinfo",
		"source":       map[string]any{"kind": "HelmRepository", "name": "podinfo"},
		"values":       map[string]any{"replicaCount": 2, "auth": map[string]any{"user": "admin"}},
		"secretValues": secretValues,
		"valuesFrom":   []any{map[string]any{"kind": "Secret", "name": "creds"}},
	}
}

// lowerHelmComponent lowers one helm component and returns the result or the error.
func lowerHelmComponent(name string, props map[string]any, traits ...oam.Trait) (oam.LoweringResult, error) {
	lctx := helmLowering("shop")
	lctx.Origin.Component = name
	return components.HelmRule{}.LowerComponent(&oam.Component{Name: name, Type: "helm", Properties: props, Traits: traits}, lctx)
}

// lowerHelmRelease lowers a helm component under delivery: flux and returns its
// helmrelease.
func lowerHelmRelease(t *testing.T, name string, props map[string]any, traits ...oam.Trait) oam.Component {
	t.Helper()
	res, err := lowerHelmComponent(name, props, traits...)
	if err != nil {
		t.Fatalf("LowerComponent(%s): %v", name, err)
	}
	return componentByType(t, res.Components, "helmrelease")
}

// secretValuesTrait returns release's one secret trait, checks the invariants
// it and its valuesFrom entry hold, and returns the Secret's name and the bytes
// it stores.
func secretValuesTrait(t *testing.T, release oam.Component) (name, data string) {
	t.Helper()
	var trait *oam.Trait
	for i := range release.Traits {
		if release.Traits[i].Type == "secret" {
			if trait != nil {
				t.Fatalf("release carries two secret traits: %v", release.Traits)
			}
			trait = &release.Traits[i]
		}
	}
	if trait == nil {
		t.Fatalf("no secret trait synthesized: %v", release.Traits)
	}
	if _, ok := release.Properties["secretValues"]; ok {
		t.Errorf("secretValues forwarded to the helmrelease")
	}
	name, _ = trait.Properties["name"].(string)
	d, _ := trait.Properties["stringData"].(map[string]any)
	if len(trait.Properties) != 2 || len(d) != 1 {
		t.Fatalf("trait has properties %d and stringData entries %d, want exactly name and a one-key stringData", len(trait.Properties), len(d))
	}
	data, _ = d["values.json"].(string)
	if data == "" {
		t.Fatal("trait stringData has no values.json string")
	}
	want := map[string]any{"kind": "Secret", "name": name, "valuesKey": "values.json"}
	from, _ := release.Properties["valuesFrom"].([]any)
	found := false
	for _, entry := range from {
		found = found || reflect.DeepEqual(entry, want)
	}
	if !found {
		t.Errorf("valuesFrom = %v, want an entry %v", from, want)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) != 0 {
		t.Errorf("Secret name %q is not a DNS-1123 subdomain: %v", name, errs)
	}
	sum := sha256.Sum256([]byte(data))
	if !strings.HasSuffix(name, "-secret-values-"+hex.EncodeToString(sum[:])[:10]) {
		t.Errorf("Secret name %q does not end in the digest of its stored bytes", name)
	}
	return name, data
}

// valuesFromNames lists the kind/name of each valuesFrom entry, in order.
func valuesFromNames(t *testing.T, release oam.Component) []string {
	t.Helper()
	from, _ := release.Properties["valuesFrom"].([]any)
	names := make([]string, len(from))
	for i, entry := range from {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("valuesFrom[%d] is %T", i, entry)
		}
		names[i] = m["kind"].(string) + "/" + m["name"].(string)
	}
	return names
}

// assertNoSensitive fails when the JSON encoding of v carries the sensitive
// value.
func assertNoSensitive(t *testing.T, what string, v any) {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if strings.Contains(string(encoded), sensitive) {
		t.Errorf("%s carries the sensitive value: %s", what, encoded)
	}
}

// TestHelmRule_SecretValues: the secretValues move into a secret trait placed
// after the authored traits; its valuesFrom entry goes ahead of the authored
// one; the plain values stay inline; nothing on the helmrelease but the trait
// carries a sensitive value; the stored bytes decode back to the authored
// secretValues with numbers exact.
func TestHelmRule_SecretValues(t *testing.T) {
	secret := map[string]any{"auth": map[string]any{"password": sensitive}, "pin": uint64(18446744073709551615)}
	authored := []oam.Trait{{Type: "force-replace", Properties: map[string]any{}}}
	release := lowerHelmRelease(t, "web", helmSecretProps(secret), authored...)
	name, data := secretValuesTrait(t, release)

	if len(release.Traits) != 2 || release.Traits[0].Type != "force-replace" || release.Traits[1].Type != "secret" {
		t.Errorf("traits = %v, want the authored one then the secret", release.Traits)
	}
	if got, want := valuesFromNames(t, release), []string{"Secret/" + name, "Secret/creds"}; !reflect.DeepEqual(got, want) {
		t.Errorf("valuesFrom = %v, want %v", got, want)
	}
	wantValues := map[string]any{"replicaCount": 2, "auth": map[string]any{"user": "admin"}}
	if !reflect.DeepEqual(release.Properties["values"], wantValues) {
		t.Errorf("values = %v, want the authored plain values %v", release.Properties["values"], wantValues)
	}
	assertNoSensitive(t, "helmrelease properties", release.Properties)
	if strings.Contains(name, sensitive) {
		t.Errorf("Secret name %q carries the sensitive value", name)
	}

	var stored map[string]any
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&stored); err != nil {
		t.Fatalf("stored secretValues: %v", err)
	}
	want := map[string]any{"auth": map[string]any{"password": sensitive}, "pin": json.Number("18446744073709551615")}
	if !reflect.DeepEqual(stored, want) {
		t.Errorf("stored secretValues do not decode back to the authored ones")
	}
}

// TestHelmRule_SecretValuesWithValuesConfigMap: under valuesMode: configMap the
// generated entries are the values ConfigMap's, then the values Secret's, then
// the authored one, so the Secret wins over the ConfigMap and an authored entry
// over both. The ConfigMap trait carries no sensitive value.
func TestHelmRule_SecretValuesWithValuesConfigMap(t *testing.T) {
	props := helmSecretProps(map[string]any{"auth": map[string]any{"password": sensitive}})
	props["valuesMode"] = "configMap"
	release := lowerHelmRelease(t, "web", props)
	secretName, _ := secretValuesTrait(t, release)

	if len(release.Traits) != 2 || release.Traits[0].Type != "configmap" || release.Traits[1].Type != "secret" {
		t.Fatalf("traits = %d, want the configmap then the secret", len(release.Traits))
	}
	cmName, _ := release.Traits[0].Properties["name"].(string)
	if got, want := valuesFromNames(t, release), []string{"ConfigMap/" + cmName, "Secret/" + secretName, "Secret/creds"}; !reflect.DeepEqual(got, want) {
		t.Errorf("valuesFrom = %v, want %v", got, want)
	}
	assertNoSensitive(t, "configmap trait", release.Traits[0].Properties)
	assertNoSensitive(t, "helmrelease properties", release.Properties)
}

// TestHelmRule_SecretValuesNameTracksContent: an edit of secretValues alone
// changes the Secret's name, and with it the helmrelease's valuesFrom; the same
// secretValues under two components carry the same digest.
func TestHelmRule_SecretValuesNameTracksContent(t *testing.T) {
	lower := func(name, password string) (oam.Component, string) {
		release := lowerHelmRelease(t, name, helmSecretProps(map[string]any{"password": password}))
		secretName, _ := secretValuesTrait(t, release)
		return release, secretName
	}
	r1, n1 := lower("web", "one")
	r2, n2 := lower("web", "two")
	if n1 == n2 {
		t.Errorf("different secretValues share Secret name %q", n1)
	}
	if reflect.DeepEqual(r1.Properties["valuesFrom"], r2.Properties["valuesFrom"]) {
		t.Errorf("the helmrelease did not change with secretValues: %v", r1.Properties["valuesFrom"])
	}
	_, nA := lower("alpha", "same")
	_, nB := lower("beta", "same")
	suffix := func(name string) string { return name[strings.LastIndex(name, "-secret-values-"):] }
	if suffix(nA) != suffix(nB) {
		t.Errorf("identical secretValues hash differently: %q vs %q", nA, nB)
	}
}

// TestHelmRule_SecretValuesEmpty: absent, null or empty secretValues synthesize
// no trait and leave valuesFrom as authored.
func TestHelmRule_SecretValuesEmpty(t *testing.T) {
	var typedNil map[string]any
	for _, secret := range []any{nil, typedNil, map[string]any{}} {
		props := helmSecretProps(secret)
		release := lowerHelmRelease(t, "web", props)
		if len(release.Traits) != 0 {
			t.Errorf("secretValues %#v: traits synthesized: %v", secret, release.Traits)
		}
		if got, want := valuesFromNames(t, release), []string{"Secret/creds"}; !reflect.DeepEqual(got, want) {
			t.Errorf("secretValues %#v: valuesFrom = %v, want %v", secret, got, want)
		}
		if _, ok := release.Properties["secretValues"]; ok {
			t.Errorf("secretValues %#v: forwarded to the helmrelease", secret)
		}
	}
	props := helmSecretProps(nil)
	delete(props, "secretValues")
	if release := lowerHelmRelease(t, "web", props); len(release.Traits) != 0 {
		t.Errorf("absent secretValues: traits synthesized: %v", release.Traits)
	}
}

// TestHelmRule_SecretValuesNameBounds: a 253-byte component name still yields a
// legal Secret name carrying the content digest, and two long names sharing the
// kept prefix still differ.
func TestHelmRule_SecretValuesNameBounds(t *testing.T) {
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 70)
	if len(long) != 253 || len(validation.IsDNS1123Subdomain(long)) != 0 {
		t.Fatalf("fixture name is %d bytes or invalid", len(long))
	}
	secret := map[string]any{"password": sensitive}
	n, _ := secretValuesTrait(t, lowerHelmRelease(t, long, helmSecretProps(secret)))
	if len(n) > 253 {
		t.Errorf("Secret name is %d bytes", len(n))
	}
	if n2, _ := secretValuesTrait(t, lowerHelmRelease(t, long[:252]+"e", helmSecretProps(secret))); n2 == n {
		t.Errorf("distinct long names collided on %q", n)
	}
}

// TestHelmRule_SecretValuesSharedPath: a path set in both values and
// secretValues is refused under every delivery and values mode, naming the path
// and never a value. Two objects at one key are compared key by key, so a
// sibling key is not a shared path.
func TestHelmRule_SecretValuesSharedPath(t *testing.T) {
	cases := []struct {
		name     string
		values   map[string]any
		secret   map[string]any
		wantPath string // "" means accepted
	}{
		{"sibling keys under one object", map[string]any{"auth": map[string]any{"user": "admin"}}, map[string]any{"auth": map[string]any{"password": sensitive}}, ""},
		{"top-level scalar", map[string]any{"token": "plain"}, map[string]any{"token": sensitive}, "token"},
		{"nested scalar", map[string]any{"auth": map[string]any{"password": "plain"}}, map[string]any{"auth": map[string]any{"password": sensitive}}, "auth.password"},
		{"object against scalar", map[string]any{"auth": map[string]any{"user": "admin"}}, map[string]any{"auth": sensitive}, "auth"},
		{"scalar against object", map[string]any{"auth": "plain"}, map[string]any{"auth": map[string]any{"password": sensitive}}, "auth"},
		{"lists", map[string]any{"hosts": []any{"a"}}, map[string]any{"hosts": []any{sensitive}}, "hosts"},
		{"first in sorted order", map[string]any{"b": 1, "a": 1}, map[string]any{"b": sensitive, "a": sensitive}, "a"},
	}
	modes := map[string]func(map[string]any){
		"flux inline":    func(map[string]any) {},
		"flux configMap": func(p map[string]any) { p["valuesMode"] = "configMap" },
		"template": func(p map[string]any) {
			p["delivery"] = "template"
			p["source"] = map[string]any{"url": "https://charts.example.com"}
			delete(p, "valuesFrom")
		},
	}
	for _, tc := range cases {
		for mode, apply := range modes {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				props := helmSecretProps(tc.secret)
				props["values"] = tc.values
				apply(props)
				_, err := lowerHelmComponent("web", props)
				if tc.wantPath == "" {
					if err != nil {
						t.Fatalf("LowerComponent: %v", err)
					}
					return
				}
				want := "helm: " + tc.wantPath + " is set in both values and secretValues"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one containing %q", err, want)
				}
				if strings.Contains(err.Error(), sensitive) {
					t.Errorf("the refusal carries the sensitive value: %v", err)
				}
			})
		}
	}
}

// TestHelmRule_SecretValuesNestedGlobal: a key named global below the top level
// of secretValues is refused under every delivery and values mode, before
// anything is lowered, naming the path and never a value (go-kure/launcher#794,
// item 9). A top-level global, a dependency's ordinary key and a list element
// are not such a key. The tree is read as JSON, so a typed map is searched too.
func TestHelmRule_SecretValuesNestedGlobal(t *testing.T) {
	cases := []struct {
		name     string
		secret   map[string]any
		wantPath string // "" means accepted
	}{
		{"top-level global", map[string]any{"global": map[string]any{"auth": map[string]any{"password": sensitive}}}, ""},
		{"a dependency's ordinary key", map[string]any{"child": map[string]any{"auth": map[string]any{"password": sensitive}}}, ""},
		{"a list element", map[string]any{"hosts": []any{map[string]any{"global": sensitive}}}, ""},
		{"a dependency's global", map[string]any{"child": map[string]any{"global": map[string]any{"auth": map[string]any{"password": sensitive}}}}, "child.global"},
		{"a plain value at the key", map[string]any{"child": map[string]any{"global": sensitive}}, "child.global"},
		{"two levels down", map[string]any{"child": map[string]any{"grand": map[string]any{"global": map[string]any{"token": sensitive}}}}, "child.grand.global"},
		{"under the top-level global", map[string]any{"global": map[string]any{"global": sensitive}}, "global.global"},
		{"first in sorted order", map[string]any{"b": map[string]any{"global": sensitive}, "a": map[string]any{"global": sensitive}}, "a.global"},
		{"under an empty key", map[string]any{"": map[string]any{"global": sensitive}}, `"".global`},
		{"a typed map", map[string]any{"child": map[string]string{"global": sensitive}}, "child.global"},
	}
	modes := map[string]func(map[string]any){
		"flux inline":    func(map[string]any) {},
		"flux configMap": func(p map[string]any) { p["valuesMode"] = "configMap" },
		"template": func(p map[string]any) {
			p["delivery"] = "template"
			p["source"] = map[string]any{"url": "https://charts.example.com"}
			delete(p, "valuesFrom")
		},
	}
	for _, tc := range cases {
		for mode, apply := range modes {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				props := helmSecretProps(tc.secret)
				apply(props)
				res, err := lowerHelmComponent("web", props)
				if tc.wantPath == "" {
					if err != nil {
						t.Fatalf("LowerComponent: %v", err)
					}
					return
				}
				want := "helm: secretValues: " + tc.wantPath + ": a key named global is allowed only at the top level"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one containing %q", err, want)
				}
				if strings.Contains(err.Error(), sensitive) {
					t.Errorf("the refusal carries the sensitive value: %v", err)
				}
				if len(res.Components) != 0 {
					t.Errorf("the refused component was lowered to %d components", len(res.Components))
				}
			})
		}
	}
}

// TestHelmRule_SecretValuesRefusalsCarryNoValue: secretValues that is not an
// object, or that does not encode as JSON, is refused by its type, not its
// content; so are two spellings of the key.
func TestHelmRule_SecretValuesRefusalsCarryNoValue(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"a string", func(p map[string]any) { p["secretValues"] = sensitive }, "helm: secretValues: must be an object, got string"},
		{"a list", func(p map[string]any) { p["secretValues"] = []any{sensitive} }, "helm: secretValues: must be an object, got []interface {}"},
		{"not JSON", func(p map[string]any) {
			p["secretValues"] = map[string]any{"password": sensitive, "f": func() {}}
		}, "helm: secretValues is not representable as JSON"},
		{"two spellings", func(p map[string]any) { p["SecretValues"] = map[string]any{"password": sensitive} },
			"helm: SecretValues and secretValues are one key given more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := helmSecretProps(map[string]any{"token": sensitive})
			tc.mutate(props)
			_, err := lowerHelmComponent("web", props)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), sensitive) {
				t.Errorf("the refusal carries the sensitive value: %v", err)
			}
		})
	}
}

// TestHelmRule_SecretValuesOtherSpelling: the key is matched ignoring case, as
// every helm key is, and lowers the same.
func TestHelmRule_SecretValuesOtherSpelling(t *testing.T) {
	props := helmSecretProps(nil)
	delete(props, "secretValues")
	props["SECRETVALUES"] = map[string]any{"password": sensitive}
	release := lowerHelmRelease(t, "web", props)
	secretValuesTrait(t, release)
	assertNoSensitive(t, "helmrelease properties", release.Properties)
}

// TestHelmRule_SecretValuesTemplateDelivery: under delivery: template the
// secretValues are forwarded to the helmtemplate as they are, beside the plain
// values, and no trait is synthesized.
func TestHelmRule_SecretValuesTemplateDelivery(t *testing.T) {
	secret := map[string]any{"auth": map[string]any{"password": sensitive}}
	props := helmSecretProps(secret)
	props["delivery"] = "template"
	props["source"] = map[string]any{"url": "https://charts.example.com"}
	delete(props, "valuesFrom")
	res, err := lowerHelmComponent("web", props)
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	tmpl := componentByType(t, res.Components, "helmtemplate")
	if len(tmpl.Traits) != 0 {
		t.Errorf("traits synthesized under template delivery: %v", tmpl.Traits)
	}
	if !reflect.DeepEqual(tmpl.Properties["secretValues"], secret) {
		t.Errorf("helmtemplate secretValues are not the authored ones")
	}
	assertNoSensitive(t, "helmtemplate values", tmpl.Properties["values"])
}
