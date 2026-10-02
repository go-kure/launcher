package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// These tests pin the configmap trait as the configmap kind's twin
// (go-kure/launcher#741): the same properties build the same ConfigMap on both
// paths, through the real `kurel build` entry point. Only the ownership fields
// may differ — the ConfigMap's `app` label names the owning component on the
// trait path and the ConfigMap itself on the kind path — and the owner's
// decorators reach the trait's ConfigMap as the kind's own decorators reach
// its ConfigMap.

// configMapTwinApp returns an Application holding the ConfigMap both ways: the
// kind path as a configmap component named name, or the trait path as a
// configmap trait of that name on a job component named "owner". decorators
// are trait types added to the component that owns the ConfigMap on either
// path.
func configMapTwinApp(t *testing.T, viaTrait bool, name string, props map[string]any, decorators ...string) string {
	t.Helper()
	var decs []any
	for _, d := range decorators {
		decs = append(decs, map[string]any{"type": d})
	}
	var comp map[string]any
	if viaTrait {
		traitProps := map[string]any{"name": name}
		for k, v := range props {
			traitProps[k] = v
		}
		comp = map[string]any{
			"name":       "owner",
			"type":       "job",
			"properties": map[string]any{"image": "ghcr.io/example/job:v1.0.0"},
			"traits":     append([]any{map[string]any{"type": "configmap", "properties": traitProps}}, decs...),
		}
	} else {
		comp = map[string]any{"name": name, "type": "configmap", "properties": props}
		if len(decs) > 0 {
			comp["traits"] = decs
		}
	}
	app := map[string]any{
		"apiVersion": "launcher.gokure.dev/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "my-app", "namespace": "default"},
		"spec":       map[string]any{"components": []any{comp}},
	}
	out, err := yaml.Marshal(app)
	if err != nil {
		t.Fatalf("marshal app: %v", err)
	}
	return string(out)
}

// configMapDoc returns the ConfigMap named name among docs.
func configMapDoc(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		if d["kind"] == "ConfigMap" && md["name"] == name {
			return d
		}
	}
	t.Fatalf("no ConfigMap %q among %d documents", name, len(docs))
	return nil
}

// TestConfigMapTwin_SameConfigMapBothWays builds each intent as a configmap
// component and as a configmap trait and requires the two ConfigMaps to be
// identical apart from the one ownership field the trait path changes: the
// `app` label, which names the owner there.
func TestConfigMapTwin_SameConfigMapBothWays(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		decorators []string
	}{
		{name: "data", props: map[string]any{"data": map[string]any{"b.yaml": "x: 1", "A_KEY": "value", "c": ""}}},
		{name: "data and binaryData", props: map[string]any{
			"data":       map[string]any{"text": "plain"},
			"binaryData": map[string]any{"blob": "aGVsbG8=", "empty": ""},
		}},
		{name: "immutable true", props: map[string]any{"data": map[string]any{"k": "v"}, "immutable": true}},
		{name: "immutable false", props: map[string]any{"data": map[string]any{"k": "v"}, "immutable": false}},
		{name: "prune-protection decorator", props: map[string]any{"data": map[string]any{"k": "v"}}, decorators: []string{"prune-protection"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kindDocs, kindErr, err := buildPVCDocs(t, configMapTwinApp(t, false, "settings", tc.props, tc.decorators...))
			if err != nil {
				t.Fatalf("kind path: %v\n%s", err, kindErr)
			}
			traitDocs, traitErr, err := buildPVCDocs(t, configMapTwinApp(t, true, "settings", tc.props, tc.decorators...))
			if err != nil {
				t.Fatalf("trait path: %v\n%s", err, traitErr)
			}
			kindCM := configMapDoc(t, kindDocs, "settings")
			traitCM := configMapDoc(t, traitDocs, "settings")

			// The ownership field: the app label names whoever owns the ConfigMap.
			if got := labelOf(kindCM, "app"); got != "settings" {
				t.Errorf("kind ConfigMap app label = %q, want %q", got, "settings")
			}
			if got := labelOf(traitCM, "app"); got != "owner" {
				t.Errorf("trait ConfigMap app label = %q, want the owner %q", got, "owner")
			}
			delete(kindCM["metadata"].(map[string]any)["labels"].(map[string]any), "app")
			delete(traitCM["metadata"].(map[string]any)["labels"].(map[string]any), "app")

			if !reflect.DeepEqual(kindCM, traitCM) {
				k, _ := yaml.Marshal(kindCM)
				tr, _ := yaml.Marshal(traitCM)
				t.Errorf("ConfigMaps differ beyond the app label\nkind:\n%s\ntrait:\n%s", k, tr)
			}
			// Each authored field must reach the ConfigMap with its authored
			// value. Both paths share one generator, so their equality alone
			// cannot catch a value the generator gets wrong on both.
			for _, field := range []string{"data", "binaryData", "immutable"} {
				want, authored := tc.props[field]
				if !authored {
					continue
				}
				if got := traitCM[field]; !reflect.DeepEqual(got, want) {
					t.Errorf("trait ConfigMap %s = %#v, want the authored %#v", field, got, want)
				}
			}
			for _, d := range tc.decorators {
				if d != "prune-protection" {
					t.Fatalf("no expected annotation recorded for decorator %q", d)
				}
				if v, _ := docAnnotation(traitCM, "kustomize.toolkit.fluxcd.io/prune"); v != "disabled" {
					t.Errorf("%s on the owner did not reach the trait's ConfigMap: prune = %q, want %q", d, v, "disabled")
				}
			}
		})
	}
}

// TestConfigMapTwin_SameRefusalsBothWays requires both paths to refuse the same
// malformed properties with the same message: the trait reads every field
// through the kind's parser, so a non-string data value is refused on the
// trait path too instead of being stringified.
func TestConfigMapTwin_SameRefusalsBothWays(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{name: "number value", props: map[string]any{"data": map[string]any{"n": 5}}, want: "data.n: must be a string"},
		{name: "boolean value", props: map[string]any{"data": map[string]any{"b": true}}, want: "data.b: must be a string"},
		{name: "invalid key", props: map[string]any{"data": map[string]any{"a/b": "v"}}, want: `data: invalid key "a/b"`},
		{name: "binaryData key also in data", props: map[string]any{
			"data":       map[string]any{"k": "v"},
			"binaryData": map[string]any{"k": "aGVsbG8="},
		}, want: "binaryData.k: key also appears in data"},
		{name: "invalid base64", props: map[string]any{"binaryData": map[string]any{"k": "not base64!"}}, want: "binaryData.k: invalid base64"},
		{name: "over the size limit", props: map[string]any{"data": map[string]any{"k": strings.Repeat("a", corev1.MaxSecretSize+1)}}, want: "hold 1048577 bytes, over the 1048576-byte limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, viaTrait := range []bool{false, true} {
				_, stderr, err := buildPVCDocs(t, configMapTwinApp(t, viaTrait, "settings", tc.props))
				if err == nil {
					t.Errorf("viaTrait=%v: build succeeded, want a refusal containing %q", viaTrait, tc.want)
					continue
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("viaTrait=%v: error %q does not contain %q\n%s", viaTrait, err, tc.want, stderr)
				}
			}
		})
	}
}

// TestConfigMapTwin_HelmValuesConfigMapUnchanged pins the values ConfigMap a
// helm component synthesizes for `valuesMode: configMap` through the
// configmap trait (go-kure/launcher#759). The trait passes the encoded values
// as one string, so the kind's string-only typing does not refuse it, and the
// ConfigMap document is byte-identical to the one in the fixture's golden
// output, which this change leaves untouched.
func TestConfigMapTwin_HelmValuesConfigMapUnchanged(t *testing.T) {
	dir := filepath.Join("testdata", "helm-values-configmap")
	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", filepath.Join(dir, "app.yaml"), "--profile", filepath.Join(dir, "cluster.yaml")})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build: %v\n%s", err, errOut.String())
	}
	expected, err := os.ReadFile(filepath.Join(dir, "expected.yaml"))
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	got := rawConfigMapDocs(out.String())
	want := rawConfigMapDocs(string(expected))
	if len(want) != 1 {
		t.Fatalf("golden holds %d ConfigMap documents, want 1", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values ConfigMap changed\nwant:\n%s\ngot:\n%s", strings.Join(want, "\n---\n"), strings.Join(got, "\n---\n"))
	}
}

// rawConfigMapDocs returns the ConfigMap documents of a multi-document YAML
// stream as their exact text.
func rawConfigMapDocs(stream string) []string {
	var docs []string
	for _, raw := range strings.Split(stream, "\n---\n") {
		if strings.Contains("\n"+raw+"\n", "\nkind: ConfigMap\n") {
			docs = append(docs, strings.TrimSpace(raw))
		}
	}
	return docs
}
