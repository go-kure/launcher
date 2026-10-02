package components

// Tests of the client-side Helm render and hook-group partition
// (helmtemplate_render.go), driven through the helmtemplate terminal.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDecodeKubeManifests_ErrorOnMalformedYAML(t *testing.T) {
	_, err := decodeKubeManifests([]byte("key: [unclosed"))
	if err == nil {
		t.Fatal("expected error on malformed YAML")
	}
}

func TestDecodeKubeManifests_ErrorOnMappingWithoutAPIVersion(t *testing.T) {
	_, err := decodeKubeManifests([]byte("kind: ConfigMap\nmetadata:\n  name: cm"))
	if err == nil {
		t.Fatal("expected error for map without apiVersion")
	}
}

// TestDecodeKubeManifests_SkipsNonMapDoc: a document that is a scalar, nil
// (null, ~, a comment alone, nothing at all) or an empty mapping carries no
// object and is skipped; the document after it still decodes.
func TestDecodeKubeManifests_SkipsNonMapDoc(t *testing.T) {
	for _, skipped := range []string{
		"just a string\n",
		"42\n",
		"null\n",
		"~\n",
		"# a comment alone\n",
		"",
		"{}\n",
	} {
		t.Run(fmt.Sprintf("%q", skipped), func(t *testing.T) {
			objects, err := decodeKubeManifests([]byte(skipped + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm"))
			if err != nil {
				t.Fatalf("decodeKubeManifests: %v", err)
			}
			if names := resourceNames(objects); !slices.Equal(names, []string{"cm"}) {
				t.Fatalf("decoded %v, want [cm] (the first document skipped)", names)
			}
		})
	}
}

// TestDecodeKubeManifests_TopLevelNonStringKeyIsAnError: a key that is not a
// string at a document's own top level makes yaml.v3 decode the whole
// document to map[any]any, not a nested mapping only. That document is an
// error naming the object and the top level, as a nested mapping's is, not
// skipped as a document that holds no object; with no apiVersion or kind
// among its string keys it is the missing-apiVersion error. The same key
// quoted is a string, and the document decodes as any other.
func TestDecodeKubeManifests_TopLevelNonStringKeyIsAnError(t *testing.T) {
	const head = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"
	const next = "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: next\n"
	for _, key := range []string{"1: x", "true: y", "1.5: z", "~: n"} {
		t.Run(key, func(t *testing.T) {
			doc := head + key + "\n"
			var plain any
			if err := yaml.Unmarshal([]byte(doc), &plain); err != nil {
				t.Fatalf("yaml.v3 decode: %v", err)
			}
			if _, ok := plain.(map[any]any); !ok {
				t.Fatalf("yaml.v3 decodes the document to %T, want map[any]any; the case no longer pins a top-level non-string key", plain)
			}
			_, err := decodeKubeManifests([]byte(doc + next))
			assertErrorMentions(t, err, `ConfigMap "cm"`, "top level", "not a string")
		})
	}
	t.Run("no apiVersion or kind", func(t *testing.T) {
		_, err := decodeKubeManifests([]byte("1: x\ntrue: y\n" + next))
		assertErrorMentions(t, err, "missing apiVersion or kind")
	})
	t.Run("quoted key", func(t *testing.T) {
		objects, err := decodeKubeManifests([]byte(head + "\"1\": x\n\"true\": y\n"))
		if err != nil {
			t.Fatalf("decodeKubeManifests: %v", err)
		}
		if len(objects) != 1 {
			t.Fatalf("got %d objects, want 1", len(objects))
		}
		u := objects[0].(*unstructured.Unstructured)
		if u.Object["1"] != "x" || u.Object["true"] != "y" {
			t.Errorf(`top-level "1" = %#v, "true" = %#v, want "x" and "y"`, u.Object["1"], u.Object["true"])
		}
	})
}

// TestDecodeKubeManifests_JSONTypedAndWrittenUnchanged covers each Go type
// yaml.v3 yields for a scalar a chart can render: the decoded value, at the
// top of spec, in a list and in a nested mapping, must be one
// runtime.DeepCopyJSONValue accepts (it panics on yaml.v3's int, uint64 and
// time.Time), and the object must encode to the same JSON as the yaml.v3
// decode alone — kure writes an object from its JSON encoding, so the written
// file is unchanged. .inf and .nan stay float64, which encoding/json refuses
// to write either way. Timestamps at the edges of RFC 3339's range become
// the string time.Time.MarshalJSON writes; a five-digit year is no yaml.v3
// timestamp (it parses exactly four digits) and stays a string.
func TestDecodeKubeManifests_JSONTypedAndWrittenUnchanged(t *testing.T) {
	cases := []struct {
		scalar string
		want   any
	}{
		{"0", int64(0)},
		{"8080", int64(8080)},
		{"-1", int64(-1)},
		{"0x1F", int64(31)},
		{"9223372036854775807", int64(math.MaxInt64)},
		{"9223372036854775808", json.Number("9223372036854775808")},
		{"18446744073709551615", json.Number("18446744073709551615")},
		{"18446744073709551616", 1.8446744073709552e19},
		{"1.5", 1.5},
		{"1.0", 1.0},
		{".inf", math.Inf(1)},
		{".nan", math.NaN()},
		{"2001-12-14", "2001-12-14T00:00:00Z"},
		{"2001-12-14t21:59:43.10-05:00", "2001-12-14T21:59:43.1-05:00"},
		{"0000-01-01T00:00:00Z", "0000-01-01T00:00:00Z"},
		{"9999-12-31T23:59:59.999999999+23:59", "9999-12-31T23:59:59.999999999+23:59"},
		{"10000-01-01T00:00:00Z", "10000-01-01T00:00:00Z"},
		{"!!binary aGVsbG8=", "hello"},
		{"true", true},
		{"null", nil},
		{"text", "text"},
	}
	same := func(got, want any) bool {
		g, gok := got.(float64)
		w, wok := want.(float64)
		if gok && wok && math.IsNaN(g) && math.IsNaN(w) {
			return true
		}
		return reflect.DeepEqual(got, want)
	}
	for _, tc := range cases {
		t.Run(tc.scalar, func(t *testing.T) {
			doc := "apiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\nspec:\n  v: " + tc.scalar +
				"\n  list:\n  - " + tc.scalar + "\n  nested:\n    v: " + tc.scalar + "\n"
			objects, err := decodeKubeManifests([]byte(doc))
			if err != nil {
				t.Fatalf("decodeKubeManifests: %v", err)
			}
			if len(objects) != 1 {
				t.Fatalf("got %d objects, want 1", len(objects))
			}
			u := objects[0].(*unstructured.Unstructured)
			spec := u.Object["spec"].(map[string]any)
			for where, got := range map[string]any{
				"spec.v":        spec["v"],
				"spec.list[0]":  spec["list"].([]any)[0],
				"spec.nested.v": spec["nested"].(map[string]any)["v"],
			} {
				if !same(got, tc.want) {
					t.Errorf("%s = %#v (%T), want %#v (%T)", where, got, got, tc.want, tc.want)
				}
			}
			assertDeepCopyable(t, objects)

			var plain map[string]any
			if err := yaml.Unmarshal([]byte(doc), &plain); err != nil {
				t.Fatalf("yaml.v3 decode: %v", err)
			}
			wantJSON, wantErr := json.Marshal(plain)
			gotJSON, gotErr := json.Marshal(u.Object)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || string(gotJSON) != string(wantJSON) {
				t.Errorf("JSON = %s (err %v), want the yaml.v3 decode's %s (err %v)", gotJSON, gotErr, wantJSON, wantErr)
			}
		})
	}
}

// TestDecodeKubeManifests_NonStringMappingKeyIsAnError: yaml.v3 decodes a
// mapping with a non-string key to map[any]any, which
// runtime.DeepCopyJSONValue panics on and encoding/json refuses to write. It
// is an error naming the object and the mapping, not a later panic.
func TestDecodeKubeManifests_NonStringMappingKeyIsAnError(t *testing.T) {
	_, err := decodeKubeManifests([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  1: one\n"))
	if err == nil {
		t.Fatal("expected an error for a mapping with a non-string key")
	}
	for _, want := range []string{`ConfigMap "cm"`, ".data", "not a string"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// assertErrorMentions fails t unless err is non-nil and its text contains
// every one of wants.
func assertErrorMentions(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestDecodeKubeManifests_TimestampOutsideRFC3339IsAnError: Go's time.Parse,
// which yaml.v3 parses an unquoted timestamp with, accepts a UTC offset hour
// up to 24 and minute up to 60, so a rendered offset of 24 hours or more
// decodes to a time.Time that RFC 3339 cannot express and
// time.Time.MarshalJSON refuses. kure writes an object from its JSON
// encoding, so such a manifest never built; the decode refuses it too,
// naming the object and the path, instead of converting it to a string that
// writes.
func TestDecodeKubeManifests_TimestampOutsideRFC3339IsAnError(t *testing.T) {
	for _, scalar := range []string{
		"2001-12-14T21:59:43+24:00",
		"2001-12-14T21:59:43-24:00",
		"2001-12-14T21:59:43+23:60",
	} {
		t.Run(scalar, func(t *testing.T) {
			doc := "apiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\nspec:\n  nested:\n    v: " + scalar + "\n"
			var plain map[string]any
			if err := yaml.Unmarshal([]byte(doc), &plain); err != nil {
				t.Fatalf("yaml.v3 decode: %v", err)
			}
			if _, err := json.Marshal(plain); err == nil {
				t.Fatalf("encoding/json writes the yaml.v3 decode of %s; the case no longer pins a refused timestamp", scalar)
			}
			_, err := decodeKubeManifests([]byte(doc))
			assertErrorMentions(t, err, `Thing "t"`, ".spec.nested.v", "timezone hour outside of range")
		})
	}
}

// TestDecodeKubeManifests_UnemittableDocumentOfADroppedHookIsSkipped: hook
// grouping drops a document whose helm.sh/hook annotation is one of kure's
// excluded phases, or a comma-separated list of nothing else, before
// anything is written, so the keys and values it holds were never refused: a
// non-string mapping key, nested or at the document's own top level, or an
// out-of-range timestamp in it is skipped with the document. Any other hook
// value is still refused, naming where. Each row's dropped column is first
// checked against parseChartManifests on a valid document, so the table
// cannot drift from what grouping actually drops.
func TestDecodeKubeManifests_UnemittableDocumentOfADroppedHookIsSkipped(t *testing.T) {
	const hooked = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: hooked\n  annotations:\n    helm.sh/hook: %q\n"
	const mainDoc = "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: main\n"
	unemittable := []struct{ name, body, where string }{
		{"non-string key", "data:\n  1: one\n", ".data"},
		{"top-level non-string key", "1: one\n", "top level"},
		{"top-level bool key", "true: yes\n", "top level"},
		{"out-of-range timestamp", "data:\n  at: 2001-12-14T21:59:43+24:00\n", ".data.at"},
	}
	for _, tc := range []struct {
		hook    string
		dropped bool
	}{
		{"test", true},
		{"pre-delete", true},
		{"post-rollback", true},
		{"pre-delete,post-delete", true},
		{" test , pre-rollback ", true},
		{"test,pre-install", false},
		{"post-install", false},
		{" test", false},
		{",", false},
		{"", false},
	} {
		t.Run(fmt.Sprintf("%q", tc.hook), func(t *testing.T) {
			groups, err := parseChartManifests([]byte(fmt.Sprintf(hooked, tc.hook)))
			if err != nil {
				t.Fatalf("parseChartManifests: %v", err)
			}
			if grouped := len(groups) > 0; grouped == tc.dropped {
				t.Fatalf("hook grouping keeps the document = %v, but the row says dropped = %v", grouped, tc.dropped)
			}
			for _, u := range unemittable {
				objects, err := decodeKubeManifests([]byte(fmt.Sprintf(hooked, tc.hook) + u.body + mainDoc))
				if !tc.dropped {
					assertErrorMentions(t, err, `ConfigMap "hooked"`, u.where)
					continue
				}
				if err != nil {
					t.Errorf("%s: decodeKubeManifests: %v", u.name, err)
					continue
				}
				if names := resourceNames(objects); !slices.Equal(names, []string{"main"}) {
					t.Errorf("%s: decoded %v, want [main]", u.name, names)
				}
			}
		})
	}
}

// TestToJSONTypes_TimeOutsideRFC3339IsAnError: every time.Time
// time.Time.MarshalJSON refuses — a year outside [0,9999], a UTC offset of
// 24 hours or more — is an error naming its path, as encoding/json's refusal
// to write it was. yaml.v3 only yields four-digit years (see
// TestDecodeKubeManifests_JSONTypedAndWrittenUnchanged), so the year cases
// are Go values.
func TestToJSONTypes_TimeOutsideRFC3339IsAnError(t *testing.T) {
	cases := []struct {
		name string
		v    time.Time
		want string
	}{
		{"year 10000", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), "year outside of range"},
		{"year -1", time.Date(-1, 12, 31, 0, 0, 0, 0, time.UTC), "year outside of range"},
		{"offset +24:00", time.Date(2001, 12, 14, 21, 59, 43, 0, time.FixedZone("", 24*3600)), "timezone hour outside of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := json.Marshal(tc.v); err == nil {
				t.Fatalf("encoding/json writes %v; the case no longer pins a refused time", tc.v)
			}
			_, err := toJSONTypes(map[string]any{"list": []any{tc.v}}, "")
			assertErrorMentions(t, err, ".list[0]", tc.want)
		})
	}
}

// TestToJSONTypes_TimeIsItsMarshalJSONString: a time.Time
// time.Time.MarshalJSON accepts becomes a string that encodes to exactly the
// JSON MarshalJSON writes, so the written manifest is unchanged.
func TestToJSONTypes_TimeIsItsMarshalJSONString(t *testing.T) {
	for _, v := range []time.Time{
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("", 23*3600+59*60)),
		time.Date(2001, 12, 14, 21, 59, 43, 100000000, time.FixedZone("", -5*3600)),
		time.Date(2001, 12, 14, 21, 59, 43, 0, time.Local),
	} {
		want, err := v.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON(%v): %v", v, err)
		}
		got, err := toJSONTypes(v, "")
		if err != nil {
			t.Fatalf("toJSONTypes(%v): %v", v, err)
		}
		if _, ok := got.(string); !ok {
			t.Fatalf("toJSONTypes(%v) = %#v (%T), want a string", v, got, got)
		}
		if gotJSON, err := json.Marshal(got); err != nil || string(gotJSON) != string(want) {
			t.Errorf("toJSONTypes(%v) encodes to %s (err %v), want MarshalJSON's %s", v, gotJSON, err, want)
		}
	}
}

func TestGenerate_FlattensHookGroupsInExecutionOrder(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objects))
	}
	var names []string
	for _, o := range objects {
		u, ok := (*o).(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("object = %T, want *unstructured.Unstructured", *o)
		}
		names = append(names, u.GetName())
	}
	want := []string{"pre", "main", "post"}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("execution order = %v, want %v", names, want)
			break
		}
	}
}

func TestAugmentLayout_SingleGroup_NoChildren(t *testing.T) {
	raw := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n")
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 0 {
		t.Errorf("ml.Children has %d entries, want 0 (a single hook group is a no-op)", len(ml.Children))
	}
	if ml.ApplicationFileMode != layout.AppFileUnset {
		t.Errorf("ml.ApplicationFileMode = %v, want AppFileUnset (the directory pin applies only when partitioning)", ml.ApplicationFileMode)
	}
}

func TestAugmentLayout_MultiGroup_PartitionsAndChains(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{
		Name:                "myapp",
		Namespace:           "team", // the enclosing layout's path, as kure's walker sets it
		Resources:           []client.Object{&unstructured.Unstructured{}},
		Mode:                layout.KustomizationExplicit,
		FluxPlacement:       layout.FluxIntegratedPerLayout,
		FileNaming:          layout.FileNamingKindName,
		FilePer:             layout.FilePerKind,
		ApplicationFileMode: layout.AppFileSingle, // must NOT propagate to children
	}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if ml.Resources != nil {
		t.Errorf("ml.Resources = %v, want nil after partitioning", ml.Resources)
	}
	if ml.ApplicationFileMode != layout.AppFileSingle {
		t.Errorf("ml.ApplicationFileMode = %v, want the caller's explicit AppFileSingle kept", ml.ApplicationFileMode)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3", len(ml.Children))
	}

	wantNames := []string{"myapp-00-pre-install", "myapp-01-main", "myapp-02-post-install"}
	var prevName string
	for i, child := range ml.Children {
		if child.Name != wantNames[i] {
			t.Errorf("Children[%d].Name = %q, want %q", i, child.Name, wantNames[i])
		}
		if child.Namespace != "team/myapp" {
			t.Errorf("Children[%d].Namespace = %q, want the parent path %q", i, child.Namespace, "team/myapp")
		}
		// One level below the component's directory, never nested twice.
		if wantPath := "team/myapp/" + wantNames[i]; child.FullRepoPath() != wantPath {
			t.Errorf("Children[%d].FullRepoPath() = %q, want %q", i, child.FullRepoPath(), wantPath)
		}
		if child.Mode != ml.Mode {
			t.Errorf("Children[%d].Mode = %v, want %v", i, child.Mode, ml.Mode)
		}
		if child.FluxPlacement != ml.FluxPlacement {
			t.Errorf("Children[%d].FluxPlacement = %v, want %v", i, child.FluxPlacement, ml.FluxPlacement)
		}
		if child.FileNaming != ml.FileNaming {
			t.Errorf("Children[%d].FileNaming = %v, want %v", i, child.FileNaming, ml.FileNaming)
		}
		if child.FilePer != ml.FilePer {
			t.Errorf("Children[%d].FilePer = %v, want %v", i, child.FilePer, ml.FilePer)
		}
		if child.ApplicationFileMode != layout.AppFileUnset {
			t.Errorf("Children[%d].ApplicationFileMode = %v, want AppFileUnset (must not inherit ml's AppFileSingle)", i, child.ApplicationFileMode)
		}
		if i == 0 {
			if len(child.DependsOn) != 0 {
				t.Errorf("Children[0].DependsOn = %v, want empty", child.DependsOn)
			}
		} else if len(child.DependsOn) != 1 || child.DependsOn[0] != prevName {
			t.Errorf("Children[%d].DependsOn = %v, want [%q]", i, child.DependsOn, prevName)
		}
		prevName = child.Name
	}
}

// findLayoutByName returns the first layout named name in ml's tree, or nil.
func findLayoutByName(ml *layout.ManifestLayout, name string) *layout.ManifestLayout {
	if ml == nil {
		return nil
	}
	if ml.Name == name {
		return ml
	}
	for _, c := range ml.Children {
		if found := findLayoutByName(c, name); found != nil {
			return found
		}
	}
	return nil
}

// kustomizationResources returns the resources entries of dir/kustomization.yaml.
func kustomizationResources(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read kustomization.yaml: %v", err)
	}
	var k struct {
		Resources []string `yaml:"resources"`
	}
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("parse %s/kustomization.yaml: %v", dir, err)
	}
	return k.Resources
}

// reachableObjectNames follows dir's kustomization.yaml resources entries the
// way a kustomize build does — a file entry adds its objects, a directory
// entry adds that directory's own build — and returns every object name
// reached. An entry that does not resolve on disk fails the test.
func reachableObjectNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	var build func(d string)
	build = func(d string) {
		for _, e := range kustomizationResources(t, d) {
			p := filepath.Join(d, e)
			fi, err := os.Stat(p)
			if err != nil {
				t.Errorf("%s/kustomization.yaml: resources entry %q does not resolve (%v)", d, e, err)
				continue
			}
			if fi.IsDir() {
				build(p)
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			objs, err := decodeKubeManifests(data)
			if err != nil {
				t.Fatalf("decode %s: %v", p, err)
			}
			for _, o := range objs {
				names[o.GetName()] = true
			}
		}
	}
	build(dir)
	return names
}

func TestExcludedHookPhasesAreDropped(t *testing.T) {
	excludedPhases := []string{"pre-delete", "post-delete", "pre-rollback", "post-rollback", "test"}
	var raw strings.Builder
	for i, phase := range excludedPhases {
		if i > 0 {
			raw.WriteString("---\n")
		}
		fmt.Fprintf(&raw, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: %s-pod\n  annotations:\n    helm.sh/hook: %s\n", phase, phase)
	}
	raw.WriteString("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kept\n")

	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return []byte(raw.String()), nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 surviving object (the 5 excluded-phase objects dropped), got %d", len(objects))
	}
	u, ok := (*objects[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("objects[0] = %T, want *unstructured.Unstructured", *objects[0])
	}
	if u.GetName() != "kept" {
		t.Errorf("surviving object name = %q, want %q", u.GetName(), "kept")
	}
}

func TestHookGroupDir_EmptyPhaseIsMain(t *testing.T) {
	if got := hookGroupDir(helm.HookGroup{Phase: ""}); got != "main" {
		t.Errorf("hookGroupDir(empty phase) = %q, want %q", got, "main")
	}
}

func TestHookGroupDir_SanitizesUnsafePhase(t *testing.T) {
	cases := []struct{ phase, want string }{
		{"pre-install,post-install", "pre-install-post-install"},
		{"PRE-INSTALL", "pre-install"},
		{"weird/phase", "weird-phase"},
		{"../../etc", "etc"},
		{"!!!", "unknown"}, // punctuation-only phase strips to an empty slug — pins the "unknown" fallback
	}
	for _, c := range cases {
		if got := hookGroupDir(helm.HookGroup{Phase: c.phase}); got != c.want {
			t.Errorf("hookGroupDir(%q) = %q, want %q", c.phase, got, c.want)
		}
	}
}

func TestHookGroupDir_TruncatesLongPhase(t *testing.T) {
	long := strings.Repeat("a", 80)
	got := hookGroupDir(helm.HookGroup{Phase: long})
	if len(got) > 40 {
		t.Errorf("len(hookGroupDir(80-char phase)) = %d, want <= 40", len(got))
	}
	if got != strings.Repeat("a", 40) {
		t.Errorf("hookGroupDir(80-char phase) = %q, want 40 a's", got)
	}
}

// TestAugmentLayout_ChildNameStaysWithinDNS1123Limit exercises
// hookGroupChildName directly with near-253-char ml.Names (validate.go's
// DNS-1123 subdomain max), including one whose truncation boundary lands
// right after a '.', and pins both the within-name and cross-name uniqueness
// guarantees hookGroupChildName's doc comment claims.
func TestAugmentLayout_ChildNameStaysWithinDNS1123Limit(t *testing.T) {
	groups := []helm.HookGroup{
		{Phase: "pre-install"},
		{Phase: strings.Repeat("x", 80)}, // slugs+truncates to 40 x's via hookGroupDir
	}

	// mlNameA's truncation boundary (prefixLen=229 for group 0's suffix
	// "-00-pre-install", len 15: maxPrefix=253-15=238, prefixLen=238-8-1=229)
	// lands right after a literal '.': mlNameA[:229] ends in ".", exercising
	// the TrimRight(name, "-.") cleanup mirrored from boundedResourceName.
	mlNameA := strings.Repeat("a", 228) + "." + strings.Repeat("b", 24)
	if len(mlNameA) != 253 {
		t.Fatalf("test setup: len(mlNameA) = %d, want 253", len(mlNameA))
	}
	if mlNameA[228] != '.' {
		t.Fatalf("test setup: mlNameA[228] = %q, want '.'", mlNameA[228])
	}

	namesA := make([]string, len(groups))
	for i, g := range groups {
		dn := hookGroupChildName(mlNameA, i, g)
		if len(dn) > 253 {
			t.Errorf("group %d: len(%q) = %d, want <= 253", i, dn, len(dn))
		}
		if errs := validation.IsDNS1123Subdomain(dn); len(errs) != 0 {
			t.Errorf("group %d: IsDNS1123Subdomain(%q) = %v, want no errors", i, dn, errs)
		}
		if strings.HasSuffix(dn, ".") || strings.HasSuffix(dn, "-") {
			t.Errorf("group %d: %q has a dangling '-'/'.' artifact from truncation", i, dn)
		}
		namesA[i] = dn
	}
	if namesA[0] == namesA[1] {
		t.Fatalf("hookGroupChildName collided across groups for one ml.Name: both produced %q", namesA[0])
	}

	// A second near-253-char ml.Name sharing mlNameA's truncated prefix must
	// still yield a distinct dirName set — the sha256 prefix, not just the
	// group index, is what prevents cross-name collision (mirrors
	// TestBoundedResourceName_TruncationPreservesUniqueness).
	mlNameB := strings.Repeat("a", 228) + "." + strings.Repeat("c", 24)
	if len(mlNameB) != 253 {
		t.Fatalf("test setup: len(mlNameB) = %d, want 253", len(mlNameB))
	}
	if mlNameA == mlNameB {
		t.Fatal("test setup: mlNameA and mlNameB must differ")
	}
	for i, g := range groups {
		dnA := hookGroupChildName(mlNameA, i, g)
		dnB := hookGroupChildName(mlNameB, i, g)
		if dnA == dnB {
			t.Errorf("group %d: hookGroupChildName collided across ml.Names: mlNameA=%q mlNameB=%q both produced %q", i, mlNameA, mlNameB, dnA)
		}
	}
}

// generateNames runs Generate and returns the resulting objects' names in
// order, failing the test on any error or non-unstructured object.
func generateNames(t *testing.T, cfg *HelmTemplateConfig) []string {
	t.Helper()
	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	names := make([]string, len(objects))
	for i, o := range objects {
		u, ok := (*o).(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("objects[%d] = %T, want *unstructured.Unstructured", i, *o)
		}
		names[i] = u.GetName()
	}
	return names
}

// TestGenerate_MultiEventHookPicksEarliestByPriorityNotPosition covers a
// distinct branch untested by every other multi-event test: those all list
// their tokens in ascending priority order already
// ("pre-install,pre-upgrade"), so a naive
// implementation that simply picked the FIRST recognized token (rather than
// the earliest by kure's own hookPhaseOrder priority) would pass every one
// of them. "pre-upgrade,pre-install" (tokens in descending priority order)
// distinguishes the two: it must still resolve to the pre-install group.
func TestGenerate_MultiEventHookPicksEarliestByPriorityNotPosition(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-upgrade,pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	if len(cfg.hookGroups) != 2 {
		t.Fatalf("expected 2 hook groups, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "pre-install" {
		t.Errorf("hookGroups[0].Phase = %q, want %q (earliest by priority, not by listed position)", got, "pre-install")
	}
}

// TestGenerate_MultiEventHookDropsExcludedTokenKeepsEarliestPhase covers a
// multi-value annotation mixing a recognized ordered phase with an excluded
// one ("pre-install,pre-delete"): the excluded token must not suppress the
// whole object, and the object must still land in the pre-install group.
func TestGenerate_MultiEventHookDropsExcludedTokenKeepsEarliestPhase(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-install,pre-delete
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"multi", "main"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("execution order = %v, want %v (multi must survive, ordered ahead of main)", got, want)
		}
	}
}

// TestGenerate_MultiEventHookAllExcludedIsDropped covers a multi-value
// annotation whose tokens are entirely excluded phases ("test,pre-delete"):
// kure's exact-string-match excludedHookPhases lookup (hooks.go:20-26,49)
// never excludes the combined string, so without normalization this object
// would wrongly survive into the mis-sorted unknown bucket instead of being
// dropped, same as a single excluded phase is today.
func TestGenerate_MultiEventHookAllExcludedIsDropped(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: test,pre-delete
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kept
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"kept"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected only the non-hook object to survive, got %v", got)
	}
}

// TestGenerate_MultiEventCustomHooksStayUnknown proves the fix does not
// overcorrect: a multi-value annotation made entirely of unrecognized custom
// hook names ("crd-install,some-custom-hook" — no member of the excluded or
// four-ordered sets) has no defined ordering priority among its tokens, so it
// must be left exactly as kure's own unknown-bucket fallback already handles
// it — sorted alphabetically after post-upgrade, annotation untouched.
func TestGenerate_MultiEventCustomHooksStayUnknown(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: crd-install,some-custom-hook
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
	u, ok := (*objects[1]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("objects[1] = %T, want *unstructured.Unstructured", *objects[1])
	}
	if u.GetName() != "multi" {
		t.Fatalf("execution order: objects[1].Name = %q, want %q (unrecognized custom hook must sort last, unchanged)", u.GetName(), "multi")
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != "crd-install,some-custom-hook" {
		t.Errorf("multi's helm.sh/hook annotation = %q, want unchanged %q", got, "crd-install,some-custom-hook")
	}
	// Pin the grouping key itself, not just execution order and the emitted
	// annotation: a broken normalizer could rewrite the grouping key (e.g. to
	// just "crd-install", dropping "some-custom-hook") while still leaving
	// the emitted object's own annotation untouched and this object sorting
	// last purely by chance of the two orderings comparing equal here — the
	// Phase assertion is the one check that would catch that.
	if len(cfg.hookGroups) != 2 {
		t.Fatalf("expected 2 hook groups, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[1].Phase; got != "crd-install,some-custom-hook" {
		t.Errorf("hookGroups[1].Phase = %q, want unchanged %q (grouping key for an all-custom annotation must not be rewritten)", got, "crd-install,some-custom-hook")
	}
}

// TestGenerate_MultiEventHookDropsExcludedTokenAmongCustomHooks covers a
// branch not handled by an earlier version of this fix: a
// multi-value annotation mixing an excluded phase with an unrecognized
// custom hook name ("test,crd-install") has no recognized ordered phase
// among its tokens, so it does not take the earliest-phase branch — but it
// is not "every token unrecognized" either (one token, "test", IS a member
// of excludedHookPhases), so it must not take the leave-unchanged branch
// either. The excluded token must be dropped from the grouping key, leaving
// just "crd-install" — an excluded phase must never influence the unknown-
// bucket grouping decision for an object that survives (is not entirely
// excluded).
func TestGenerate_MultiEventHookDropsExcludedTokenAmongCustomHooks(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: test,crd-install
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	if len(cfg.hookGroups) != 1 {
		t.Fatalf("expected 1 hook group (multi survives, not all tokens excluded), got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "crd-install" {
		t.Errorf("hookGroups[0].Phase = %q, want %q (excluded token \"test\" dropped from grouping key)", got, "crd-install")
	}
	if len(cfg.hookGroups[0].Resources) != 1 {
		t.Fatalf("hookGroups[0].Resources has %d entries, want 1", len(cfg.hookGroups[0].Resources))
	}
	u, ok := cfg.hookGroups[0].Resources[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("hookGroups[0].Resources[0] = %T, want *unstructured.Unstructured", cfg.hookGroups[0].Resources[0])
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != "test,crd-install" {
		t.Errorf("emitted object's helm.sh/hook annotation = %q, want unchanged %q", got, "test,crd-install")
	}
}

// TestGenerate_CommaOnlyHookAnnotationIsNotDropped covers a degenerate
// annotation with no actual token content at all ("," — every split token is
// empty after trimming). Before this multi-event fix existed, kure's
// SplitByHookWeight would have treated the literal string "," as one opaque
// unknown-phase string — not excluded (excludedHookPhases has no "," entry)
// and not dropped. normalizeHookAnnotationForGrouping must reach the same
// outcome: "no non-empty token was ever excluded" is a different condition
// from "every non-empty token was excluded", and only the latter should
// route to the drop-via-"test" branch.
func TestGenerate_CommaOnlyHookAnnotationIsNotDropped(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ","
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	var total int
	for _, g := range cfg.hookGroups {
		total += len(g.Resources)
	}
	if total != 1 {
		t.Fatalf("expected the comma-only-hook object to survive (not all tokens excluded — there were no tokens at all), got %d resources across %d groups", total, len(cfg.hookGroups))
	}
	// Pin the grouping key itself: a broken normalizer could rewrite "," to
	// "" (or some other value) while still leaving the object present, which
	// the resource-count check above alone would not catch.
	if len(cfg.hookGroups) != 1 {
		t.Fatalf("expected 1 hook group, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "," {
		t.Errorf("hookGroups[0].Phase = %q, want unchanged %q (a degenerate annotation must not be rewritten)", got, ",")
	}
}

// TestAugmentLayout_MultiEventHookAnnotationUnchangedInOutput is the
// test called for by the fix's own hazard: the grouping-key rewrite
// (normalizeHookAnnotationForGrouping) must never leak into the object that
// ends up in emitted output. Exercises both Generate (flattened union) and
// AugmentLayout (repartitioned into
// child layouts) — a no-op implementation that simply left the multi-event
// annotation untouched would satisfy the "annotation unchanged" half of this
// test but fail its "correct group placement" half, and a broken
// implementation that mutated the object in place would fail the reverse —
// only a correct fix (copy-for-grouping, restore-original-for-output)
// satisfies both halves at once.
func TestAugmentLayout_MultiEventHookAnnotationUnchangedInOutput(t *testing.T) {
	const wantHook = "pre-install,pre-upgrade"
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ` + wantHook + `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	renderChart := func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	}

	// Generate path: correct placement (first) and unchanged annotation.
	genCfg := helmTemplateFixture(t, renderChart)
	objects, err := genCfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objects))
	}
	u, ok := (*objects[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("objects[0] = %T, want *unstructured.Unstructured", *objects[0])
	}
	if u.GetName() != "multi" {
		t.Fatalf("Generate execution order: objects[0].Name = %q, want %q (earliest-phase placement)", u.GetName(), "multi")
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != wantHook {
		t.Errorf("Generate: multi's helm.sh/hook annotation = %q, want unchanged %q", got, wantHook)
	}

	// AugmentLayout path: correct child group placement (dirName derived from
	// the earliest phase, "pre-install") and unchanged annotation on the
	// resource inside that child.
	augCfg := helmTemplateFixture(t, renderChart)
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := augCfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3", len(ml.Children))
	}
	if ml.Children[0].Name != "myapp-00-pre-install" {
		t.Fatalf("Children[0].Name = %q, want %q (multi's group keyed by its earliest phase)", ml.Children[0].Name, "myapp-00-pre-install")
	}
	if len(ml.Children[0].Resources) != 1 {
		t.Fatalf("Children[0].Resources has %d entries, want 1", len(ml.Children[0].Resources))
	}
	child, ok := ml.Children[0].Resources[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("Children[0].Resources[0] = %T, want *unstructured.Unstructured", ml.Children[0].Resources[0])
	}
	if child.GetName() != "multi" {
		t.Fatalf("Children[0].Resources[0].Name = %q, want %q", child.GetName(), "multi")
	}
	if got := child.GetAnnotations()["helm.sh/hook"]; got != wantHook {
		t.Errorf("AugmentLayout: multi's helm.sh/hook annotation = %q, want unchanged %q", got, wantHook)
	}
}
