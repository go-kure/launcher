package kurel

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// These tests pin the pvc trait as the persistentvolumeclaim kind's twin
// (go-kure/launcher#741): the same properties build the same claim on both
// paths, through the real `kurel build` entry point. Only the ownership fields
// may differ — the claim's `app` label names the owning component on the trait
// path and the claim itself on the kind path — and the owner's decorators reach
// the trait's claim as the kind's own decorators reach its claim.

// pvcTwinApp returns an Application holding the claim both ways: the kind
// path as a persistentvolumeclaim component named name, or the trait path as a
// pvc trait of that name on a job component named "owner". decorators are
// trait types added to the component that owns the claim on either path.
func pvcTwinApp(t *testing.T, viaTrait bool, name string, props map[string]any, decorators ...string) string {
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
			"traits":     append([]any{map[string]any{"type": "pvc", "properties": traitProps}}, decs...),
		}
	} else {
		comp = map[string]any{"name": name, "type": "persistentvolumeclaim", "properties": props}
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

// buildPVCDocs runs `kurel build` on appYAML and returns stdout split into one
// decoded object per document. Stderr is kept apart, since a force-replace
// claim prints a data-loss warning there.
func buildPVCDocs(t *testing.T, appYAML string) ([]map[string]any, string, error) {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		return nil, errOut.String(), err
	}
	var docs []map[string]any
	for _, raw := range strings.Split(out.String(), "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		docs = append(docs, obj)
	}
	return docs, errOut.String(), nil
}

// claimDoc returns the PersistentVolumeClaim named name among docs.
func claimDoc(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		if d["kind"] == "PersistentVolumeClaim" && md["name"] == name {
			return d
		}
	}
	t.Fatalf("no PersistentVolumeClaim %q among %d documents", name, len(docs))
	return nil
}

// TestPVCTwin_SameClaimBothWays builds each intent as a persistentvolumeclaim
// component and as a pvc trait and requires the two claims to be identical
// apart from the one ownership field the trait path changes: the `app` label,
// which names the owner there.
func TestPVCTwin_SameClaimBothWays(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		decorators []string
	}{
		{name: "size only", props: map[string]any{"size": "5Gi"}},
		{name: "storage class", props: map[string]any{"size": "5Gi", "storageClassName": "fast"}},
		{name: "explicit empty storage class", props: map[string]any{"size": "1Gi", "storageClassName": ""}},
		{name: "null storage class", props: map[string]any{"size": "1Gi", "storageClassName": nil}},
		{name: "access modes", props: map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteMany", "ReadOnlyMany"}}},
		{name: "read write once pod alone", props: map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteOncePod"}}},
		{name: "null access modes", props: map[string]any{"size": "1Gi", "accessModes": nil}},
		{name: "block volume mode", props: map[string]any{"size": "1Gi", "volumeMode": "Block"}},
		{name: "null volume mode", props: map[string]any{"size": "1Gi", "volumeMode": nil}},
		{name: "force-replace decorator", props: map[string]any{"size": "1Gi"}, decorators: []string{"force-replace"}},
		{name: "prune-protection decorator", props: map[string]any{"size": "1Gi"}, decorators: []string{"prune-protection"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kindDocs, kindErr, err := buildPVCDocs(t, pvcTwinApp(t, false, "data", tc.props, tc.decorators...))
			if err != nil {
				t.Fatalf("kind path: %v\n%s", err, kindErr)
			}
			traitDocs, traitErr, err := buildPVCDocs(t, pvcTwinApp(t, true, "data", tc.props, tc.decorators...))
			if err != nil {
				t.Fatalf("trait path: %v\n%s", err, traitErr)
			}
			kindClaim := claimDoc(t, kindDocs, "data")
			traitClaim := claimDoc(t, traitDocs, "data")

			// The ownership field: the app label names whoever owns the claim.
			if got := labelOf(kindClaim, "app"); got != "data" {
				t.Errorf("kind claim app label = %q, want %q", got, "data")
			}
			if got := labelOf(traitClaim, "app"); got != "owner" {
				t.Errorf("trait claim app label = %q, want the owner %q", got, "owner")
			}
			delete(kindClaim["metadata"].(map[string]any)["labels"].(map[string]any), "app")
			delete(traitClaim["metadata"].(map[string]any)["labels"].(map[string]any), "app")

			if !reflect.DeepEqual(kindClaim, traitClaim) {
				k, _ := yaml.Marshal(kindClaim)
				tr, _ := yaml.Marshal(traitClaim)
				t.Errorf("claims differ beyond the app label\nkind:\n%s\ntrait:\n%s", k, tr)
			}
			// A decorator must actually reach the claim, not merely be absent on
			// both paths.
			for _, d := range tc.decorators {
				if d == "force-replace" && !isForceAnnotated(traitClaim) {
					t.Errorf("force-replace on the owner did not reach the trait's claim")
				}
			}
		})
	}
}

func labelOf(obj map[string]any, key string) string {
	md, _ := obj["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)
	v, _ := labels[key].(string)
	return v
}

func isForceAnnotated(obj map[string]any) bool {
	v, ok := docAnnotation(obj, "kustomize.toolkit.fluxcd.io/force")
	return ok && v == "enabled"
}

// TestPVCTwin_SameRefusalsBothWays requires both paths to refuse the same
// malformed properties with the same message. A wrongly typed or out-of-enum
// value is refused by the shared property schema before either parser runs;
// the rest by the kind's parser, which the trait now reads every claim field
// through.
func TestPVCTwin_SameRefusalsBothWays(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{name: "non-string size", props: map[string]any{"size": 5}, want: "properties.size: expected string"},
		{name: "zero size", props: map[string]any{"size": "0"}, want: "size: must be positive"},
		{name: "invalid size", props: map[string]any{"size": "lots"}, want: "size: invalid quantity"},
		{name: "non-string storage class", props: map[string]any{"size": "1Gi", "storageClassName": 3}, want: "properties.storageClassName: expected string"},
		{name: "invalid storage class", props: map[string]any{"size": "1Gi", "storageClassName": "Fast_SSD"}, want: "storageClassName: invalid storageClass"},
		{name: "non-array access modes", props: map[string]any{"size": "1Gi", "accessModes": "ReadWriteOnce"}, want: "properties.accessModes: expected array"},
		{name: "empty access modes", props: map[string]any{"size": "1Gi", "accessModes": []any{}}, want: "accessModes: at least one access mode is required"},
		{name: "empty access mode element", props: map[string]any{"size": "1Gi", "accessModes": []any{""}}, want: "properties.accessModes[0]: value  not in allowed set"},
		{name: "read write once pod combined", props: map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteOncePod", "ReadOnlyMany"}}, want: "cannot be combined with other access modes"},
		{name: "empty volume mode", props: map[string]any{"size": "1Gi", "volumeMode": ""}, want: "properties.volumeMode: value  not in allowed set"},
		{name: "unknown volume mode", props: map[string]any{"size": "1Gi", "volumeMode": "Raw"}, want: "properties.volumeMode: value Raw not in allowed set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, viaTrait := range []bool{false, true} {
				_, stderr, err := buildPVCDocs(t, pvcTwinApp(t, viaTrait, "data", tc.props))
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

// TestPVCTrait_ClaimNameMustBeDNSSubdomain: the trait's `name` becomes the
// claim's name, so it is held to the same DNS-1123 subdomain rule the kind's
// component name is.
func TestPVCTrait_ClaimNameMustBeDNSSubdomain(t *testing.T) {
	_, _, err := buildPVCDocs(t, pvcTwinApp(t, true, "Shared_Data", map[string]any{"size": "1Gi"}))
	if err == nil || !strings.Contains(err.Error(), "Shared_Data") {
		t.Fatalf("err = %v, want a refusal naming the invalid claim name", err)
	}
}
