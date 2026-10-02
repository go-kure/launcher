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

// helm valuesMode: configMap lowers to the helmrelease plus a configmap trait
// on it (go-kure/launcher#702). These tests pin the lowering's own output; the
// ConfigMap the trait emits is checked end to end in pkg/cmd/kurel.

// helmValuesProps is a helm component referencing an existing HelmRepository,
// under valuesMode: configMap, with two authored valuesFrom entries.
func helmValuesProps(values map[string]any) map[string]any {
	props := map[string]any{
		"chart":      "podinfo",
		"valuesMode": "configMap",
		"source":     map[string]any{"kind": "HelmRepository", "name": "podinfo"},
		"valuesFrom": []any{map[string]any{"kind": "Secret", "name": "creds"}, map[string]any{"kind": "ConfigMap", "name": "extra"}},
	}
	if values != nil {
		props["values"] = values
	}
	return props
}

// lowerHelmValues lowers a helm component carrying traits and returns its
// helmrelease, and the values configmap trait (nil when none was synthesized).
func lowerHelmValues(t *testing.T, name string, props map[string]any, traits ...oam.Trait) (oam.Component, *oam.Trait) {
	t.Helper()
	lctx := helmLowering("shop")
	lctx.Origin.Component = name
	res, err := components.HelmRule{}.LowerComponent(&oam.Component{Name: name, Type: "helm", Properties: props, Traits: traits}, lctx)
	if err != nil {
		t.Fatalf("LowerComponent(%s): %v", name, err)
	}
	release := componentByType(t, res.Components, "helmrelease")
	if _, ok := release.Properties["valuesMode"]; ok {
		t.Errorf("valuesMode forwarded to the helmrelease: %v", release.Properties)
	}
	if len(release.Traits) < len(traits) || (len(traits) > 0 && !reflect.DeepEqual(release.Traits[:len(traits)], traits)) {
		t.Fatalf("release traits = %v, want the authored %v first", release.Traits, traits)
	}
	switch extra := release.Traits[len(traits):]; len(extra) {
	case 0:
		return release, nil
	case 1:
		return release, &extra[0]
	default:
		t.Fatalf("release carries %d synthesized traits, want at most one: %v", len(extra), extra)
		return release, nil
	}
}

// assertValuesTrait checks the invariants every synthesized values trait and
// its valuesFrom entry hold, and returns the ConfigMap name and stored bytes.
func assertValuesTrait(t *testing.T, release oam.Component, trait *oam.Trait) (name, data string) {
	t.Helper()
	if trait == nil {
		t.Fatal("no values configmap trait synthesized")
	}
	if trait.Type != "configmap" {
		t.Fatalf("synthesized trait type %q, want configmap", trait.Type)
	}
	if _, ok := release.Properties["values"]; ok {
		t.Errorf("values still on the helmrelease: %v", release.Properties["values"])
	}
	name, _ = trait.Properties["name"].(string)
	d, _ := trait.Properties["data"].(map[string]any)
	if len(trait.Properties) != 2 || len(d) != 1 {
		t.Fatalf("trait properties = %v, want exactly name and a one-key data", trait.Properties)
	}
	data, _ = d["values.json"].(string)
	if data == "" {
		t.Fatalf("trait data = %v, want a values.json string", d)
	}
	from, _ := release.Properties["valuesFrom"].([]any)
	if len(from) == 0 {
		t.Fatal("no valuesFrom entry")
	}
	want := map[string]any{"kind": "ConfigMap", "name": name, "valuesKey": "values.json"}
	if !reflect.DeepEqual(from[0], want) {
		t.Errorf("valuesFrom[0] = %v, want %v", from[0], want)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) != 0 {
		t.Errorf("ConfigMap name %q is not a DNS-1123 subdomain: %v", name, errs)
	}
	sum := sha256.Sum256([]byte(data))
	if !strings.HasSuffix(name, "-values-"+hex.EncodeToString(sum[:])[:10]) {
		t.Errorf("ConfigMap name %q does not end in the digest of its stored bytes", name)
	}
	return name, data
}

// loweredValuesTrait lowers a helm component with no authored traits and
// returns its values ConfigMap name and stored bytes.
func loweredValuesTrait(t *testing.T, name string, props map[string]any) (cmName, data string) {
	t.Helper()
	release, trait := lowerHelmValues(t, name, props)
	return assertValuesTrait(t, release, trait)
}

// TestHelmRule_ValuesConfigMap: the values move into a configmap trait placed
// after the authored traits; the valuesFrom entry naming it goes ahead of the
// authored entries, which keep their order; the stored bytes decode back to
// the authored values with numbers exact.
func TestHelmRule_ValuesConfigMap(t *testing.T) {
	values := map[string]any{"replicaCount": 2, "image": map[string]any{"tag": "1.2"}, "big": uint64(18446744073709551615)}
	authored := []oam.Trait{{Type: "force-replace", Properties: map[string]any{}}}
	release, trait := lowerHelmValues(t, "web", helmValuesProps(values), authored...)
	_, data := assertValuesTrait(t, release, trait)

	from := release.Properties["valuesFrom"].([]any)
	if len(from) != 3 || from[1].(map[string]any)["name"] != "creds" || from[2].(map[string]any)["name"] != "extra" {
		t.Errorf("valuesFrom = %v, want generated, creds, extra", from)
	}
	var stored map[string]any
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&stored); err != nil {
		t.Fatalf("stored values: %v", err)
	}
	want := map[string]any{"replicaCount": json.Number("2"), "image": map[string]any{"tag": "1.2"}, "big": json.Number("18446744073709551615")}
	if !reflect.DeepEqual(stored, want) {
		t.Errorf("stored values %v, want %v", stored, want)
	}
}

// TestHelmRule_ValuesConfigMapNameTracksValues: the name moves with the
// values; two components with the same values carry the same hash and bytes.
func TestHelmRule_ValuesConfigMapNameTracksValues(t *testing.T) {
	values := func() map[string]any { return map[string]any{"b": 2, "a": map[string]any{"y": true, "x": "s"}} }
	n1, _ := loweredValuesTrait(t, "web", helmValuesProps(map[string]any{"a": 1}))
	n2, _ := loweredValuesTrait(t, "web", helmValuesProps(map[string]any{"a": 2}))
	if n1 == n2 {
		t.Errorf("different values share ConfigMap name %q", n1)
	}
	nA, dA := loweredValuesTrait(t, "alpha", helmValuesProps(values()))
	nB, dB := loweredValuesTrait(t, "beta", helmValuesProps(values()))
	suffix := func(name string) string { return name[strings.LastIndex(name, "-values-"):] }
	if suffix(nA) != suffix(nB) || dA != dB {
		t.Errorf("identical values hash differently: %q vs %q", nA, nB)
	}
}

// TestHelmRule_ValuesConfigMapEmptyValues: absent or empty values synthesize
// no trait and leave the authored valuesFrom as it was; empty values are not
// forwarded either.
func TestHelmRule_ValuesConfigMapEmptyValues(t *testing.T) {
	for _, values := range []map[string]any{nil, {}} {
		release, trait := lowerHelmValues(t, "web", helmValuesProps(values))
		if trait != nil {
			t.Errorf("values %v: trait synthesized: %v", values, trait)
		}
		if _, ok := release.Properties["values"]; ok {
			t.Errorf("values %v: values forwarded", values)
		}
		if from, _ := release.Properties["valuesFrom"].([]any); len(from) != 2 || from[0].(map[string]any)["name"] != "creds" {
			t.Errorf("values %v: valuesFrom = %v, want the two authored entries", values, from)
		}
	}
}

// TestHelmRule_ValuesConfigMapNameBounds: a 253-byte component name, the
// longest validate.go admits, still yields a legal ConfigMap name carrying the
// values hash; two long names sharing the kept prefix still differ; a kept
// prefix ending in '.' is trimmed so the name never carries ".-".
func TestHelmRule_ValuesConfigMapNameBounds(t *testing.T) {
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 70)
	if len(long) != 253 || len(validation.IsDNS1123Subdomain(long)) != 0 {
		t.Fatalf("fixture name is %d bytes or invalid", len(long))
	}
	n, _ := loweredValuesTrait(t, long, helmValuesProps(map[string]any{"a": 1}))
	if len(n) > 253 {
		t.Errorf("ConfigMap name is %d bytes", len(n))
	}
	other := long[:252] + "e"
	if n2, _ := loweredValuesTrait(t, other, helmValuesProps(map[string]any{"a": 1})); n2 == n {
		t.Errorf("distinct long names collided on %q", n)
	}

	// 253 - len("-values-<10>") - 8 (name digest) - 1 = 226 bytes kept.
	dotted := strings.Repeat("a", 225) + "." + strings.Repeat("b", 27)
	if len(dotted) != 253 || dotted[225] != '.' {
		t.Fatalf("test setup: %d-byte name with %q at 225", len(dotted), dotted[225])
	}
	got, _ := loweredValuesTrait(t, dotted, helmValuesProps(map[string]any{"a": 1}))
	if strings.Contains(got, ".-") || len(got) > 253 {
		t.Errorf("ConfigMap name %q (%d bytes) kept the boundary dot or overran 253", got, len(got))
	}
}
