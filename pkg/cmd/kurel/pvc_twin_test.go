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
// path and the claim itself on the kind path — and the owner's delivery traits
// cover the trait's claim as the kind's own cover its claim.

// pvcTwinApp returns an Application holding the claim both ways: the kind
// path as a persistentvolumeclaim component named name, or the trait path as a
// pvc trait of that name on a job component named "owner". ownerTraits are
// trait types added to the component that owns the claim on either path.
func pvcTwinApp(t *testing.T, viaTrait bool, name string, props map[string]any, ownerTraits ...string) string {
	t.Helper()
	var decs []any
	for _, d := range ownerTraits {
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
	return buildPVCDocsWithProfile(t, appYAML, testClusterYAML)
}

// buildPVCDocsWithProfile is buildPVCDocs against the ClusterProfile profileYAML.
func buildPVCDocsWithProfile(t *testing.T, appYAML, profileYAML string) ([]map[string]any, string, error) {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", profileYAML)

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
		name        string
		props       map[string]any
		ownerTraits []string
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
		{name: "selector", props: map[string]any{"size": "1Gi", "selector": map[string]any{
			"matchLabels":      map[string]any{"tier": "archive"},
			"matchExpressions": []any{map[string]any{"key": "zone", "operator": "In", "values": []any{"a", "b"}}},
		}}},
		{name: "data source ref", props: map[string]any{"size": "1Gi", "dataSourceRef": map[string]any{
			"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "nightly",
		}}},
		{name: "volume name", props: map[string]any{"size": "1Gi", "storageClassName": "", "volumeName": "pv-archive-0"}},
		{name: "volume attributes class", props: map[string]any{"size": "1Gi", "volumeAttributesClassName": "gold"}},
		{name: "null claim spec fields", props: map[string]any{
			"size": "1Gi", "selector": nil, "dataSourceRef": nil, "volumeName": nil, "volumeAttributesClassName": nil,
		}},
		{name: "force-replace on the owner", props: map[string]any{"size": "1Gi"}, ownerTraits: []string{"force-replace"}},
		{name: "prune-protection on the owner", props: map[string]any{"size": "1Gi"}, ownerTraits: []string{"prune-protection"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kindDocs, kindErr, err := buildPVCDocs(t, pvcTwinApp(t, false, "data", tc.props, tc.ownerTraits...))
			if err != nil {
				t.Fatalf("kind path: %v\n%s", err, kindErr)
			}
			traitDocs, traitErr, err := buildPVCDocs(t, pvcTwinApp(t, true, "data", tc.props, tc.ownerTraits...))
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
			// The component label names the same owner (go-kure/launcher#788).
			if got := labelOf(kindClaim, kurelComponentLabel); got != "data" {
				t.Errorf("kind claim component label = %q, want %q", got, "data")
			}
			if got := labelOf(traitClaim, kurelComponentLabel); got != "owner" {
				t.Errorf("trait claim component label = %q, want the owner %q", got, "owner")
			}
			for _, claim := range []map[string]any{kindClaim, traitClaim} {
				labels := claim["metadata"].(map[string]any)["labels"].(map[string]any)
				delete(labels, "app")
				delete(labels, kurelComponentLabel)
			}

			if !reflect.DeepEqual(kindClaim, traitClaim) {
				k, _ := yaml.Marshal(kindClaim)
				tr, _ := yaml.Marshal(traitClaim)
				t.Errorf("claims differ beyond the owner's labels\nkind:\n%s\ntrait:\n%s", k, tr)
			}
			// A delivery trait must actually cover the claim, not merely leave
			// both paths alike. It writes nothing on the claim: it sets an intent
			// on the application that holds it (go-kure/launcher#782), which on
			// the trait path is a sub-application the engine carries the owner's
			// intent to.
			assertTwinDeliveryIntent(t, "data", tc.ownerTraits,
				pvcTwinApp(t, false, "data", tc.props, tc.ownerTraits...),
				pvcTwinApp(t, true, "data", tc.props, tc.ownerTraits...))
			assertNoFluxObjectKeys(t, kindClaim, traitClaim)
		})
	}
}

// pvcCapabilityClusterYAML is testClusterYAML plus a `pvc` capability that
// supplies a platform storageClassName default.
const pvcCapabilityClusterYAML = testClusterYAML + `    pvc:
      rendering:
        storageClassName: platform-ssd
`

// TestPVCTwin_CapabilityDefaultBothWays requires the kind and the trait to take
// the same storageClassName under a ClusterProfile `pvc` binding
// (go-kure/launcher#742): the platform class fills an unset or null value on
// both paths, and an authored value, "" included, wins on both.
func TestPVCTwin_CapabilityDefaultBothWays(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  any // the claim's spec.storageClassName; nil means absent
	}{
		{name: "unset takes the platform class", props: map[string]any{"size": "1Gi"}, want: "platform-ssd"},
		{name: "null takes the platform class", props: map[string]any{"size": "1Gi", "storageClassName": nil}, want: "platform-ssd"},
		{name: "authored class wins", props: map[string]any{"size": "1Gi", "storageClassName": "slow"}, want: "slow"},
		{name: "authored empty string wins", props: map[string]any{"size": "1Gi", "storageClassName": ""}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var claims []map[string]any
			for _, viaTrait := range []bool{false, true} {
				docs, stderr, err := buildPVCDocsWithProfile(t, pvcTwinApp(t, viaTrait, "data", tc.props), pvcCapabilityClusterYAML)
				if err != nil {
					t.Fatalf("viaTrait=%v: %v\n%s", viaTrait, err, stderr)
				}
				claim := claimDoc(t, docs, "data")
				spec, _ := claim["spec"].(map[string]any)
				got, present := spec["storageClassName"]
				if !present {
					got = nil
				}
				if got != tc.want {
					t.Errorf("viaTrait=%v: spec.storageClassName = %#v (present %v), want %#v", viaTrait, got, present, tc.want)
				}
				labels := claim["metadata"].(map[string]any)["labels"].(map[string]any)
				delete(labels, "app")
				delete(labels, kurelComponentLabel)
				claims = append(claims, claim)
			}
			if !reflect.DeepEqual(claims[0], claims[1]) {
				k, _ := yaml.Marshal(claims[0])
				tr, _ := yaml.Marshal(claims[1])
				t.Errorf("claims differ beyond the owner's labels\nkind:\n%s\ntrait:\n%s", k, tr)
			}
		})
	}
}

// roleVolumeTwinApp returns an Application with one role component "api" of
// kind that mounts the claim "api-data" at /data either way: as a pvc volume
// "data" describing the claim, or as an authored pvc trait "api-data" the
// volume references by claimName. volumeProps are the claim fields as the
// volume names them; the trait takes the same ones, storageClass renamed to
// its storageClassName.
func roleVolumeTwinApp(t *testing.T, kind string, viaTrait bool, volumeProps map[string]any) string {
	t.Helper()
	vol := map[string]any{"name": "data", "type": "pvc", "mountPath": "/data"}
	comp := map[string]any{"name": "api", "type": kind}
	if viaTrait {
		traitProps := map[string]any{"name": "api-data"}
		for k, v := range volumeProps {
			if k == "storageClass" {
				k = "storageClassName"
			}
			traitProps[k] = v
		}
		vol["claimName"] = "api-data"
		comp["traits"] = []any{map[string]any{"type": "pvc", "properties": traitProps}}
	} else {
		for k, v := range volumeProps {
			vol[k] = v
		}
	}
	comp["properties"] = map[string]any{"image": "ghcr.io/example/api:v1.0.0", "volumes": []any{vol}}
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

// TestPVCTwin_RoleVolumeTakesTheCapabilityDefault requires a webservice or
// worker pvc volume and an authored pvc trait to build the same claim under a
// ClusterProfile `pvc` binding (go-kure/launcher#746). The volume's claim is a
// sealed trait the rule synthesizes, which the engine merges no rendering
// into, so the rule fills the platform class itself: unset or null takes it,
// and an authored class, "" included, wins, as on the trait.
func TestPVCTwin_RoleVolumeTakesTheCapabilityDefault(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  any // the claim's spec.storageClassName; nil means absent
	}{
		{name: "unset takes the platform class", props: map[string]any{"size": "1Gi"}, want: "platform-ssd"},
		{name: "null takes the platform class", props: map[string]any{"size": "1Gi", "storageClass": nil}, want: "platform-ssd"},
		{name: "authored class wins", props: map[string]any{"size": "1Gi", "storageClass": "slow"}, want: "slow"},
		{name: "authored empty string wins", props: map[string]any{"size": "1Gi", "storageClass": ""}, want: ""},
	}
	for _, kind := range []string{"webservice", "worker"} {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				var claims []map[string]any
				for _, viaTrait := range []bool{false, true} {
					docs, stderr, err := buildPVCDocsWithProfile(t, roleVolumeTwinApp(t, kind, viaTrait, tc.props), pvcCapabilityClusterYAML)
					if err != nil {
						t.Fatalf("viaTrait=%v: %v\n%s", viaTrait, err, stderr)
					}
					claim := claimDoc(t, docs, "api-data")
					spec, _ := claim["spec"].(map[string]any)
					got, present := spec["storageClassName"]
					if !present {
						got = nil
					}
					if got != tc.want {
						t.Errorf("viaTrait=%v: spec.storageClassName = %#v (present %v), want %#v", viaTrait, got, present, tc.want)
					}
					claims = append(claims, claim)
				}
				if !reflect.DeepEqual(claims[0], claims[1]) {
					v, _ := yaml.Marshal(claims[0])
					tr, _ := yaml.Marshal(claims[1])
					t.Errorf("claims differ\nvolume:\n%s\ntrait:\n%s", v, tr)
				}
			})
		}
	}
}

// claimTemplateApp returns an Application holding one statefulset "db" whose
// single volumeClaimTemplates entry "data" carries props.
func claimTemplateApp(t *testing.T, props map[string]any) string {
	t.Helper()
	entry := map[string]any{"name": "data", "mountPath": "/data"}
	for k, v := range props {
		entry[k] = v
	}
	app := map[string]any{
		"apiVersion": "launcher.gokure.dev/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "my-app", "namespace": "default"},
		"spec": map[string]any{"components": []any{map[string]any{
			"name": "db", "type": "statefulset",
			"properties": map[string]any{"image": "ghcr.io/example/db:v1.0.0", "volumeClaimTemplates": []any{entry}},
		}}},
	}
	out, err := yaml.Marshal(app)
	if err != nil {
		t.Fatalf("marshal app: %v", err)
	}
	return string(out)
}

// TestPVCTwin_ClaimTemplateTakesTheCapabilityDefault requires a statefulset
// volumeClaimTemplates entry to take the same storageClassName under a
// ClusterProfile `pvc` binding as an authored pvc trait (go-kure/launcher#761):
// the platform class fills an unset or null value, and an authored value, ""
// included, wins. The template's spec is compared with the trait claim's spec.
func TestPVCTwin_ClaimTemplateTakesTheCapabilityDefault(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  any // spec.storageClassName; nil means absent
	}{
		{name: "unset takes the platform class", props: map[string]any{"size": "1Gi"}, want: "platform-ssd"},
		{name: "null takes the platform class", props: map[string]any{"size": "1Gi", "storageClass": nil}, want: "platform-ssd"},
		{name: "authored class wins", props: map[string]any{"size": "1Gi", "storageClass": "slow"}, want: "slow"},
		{name: "authored empty string wins", props: map[string]any{"size": "1Gi", "storageClass": ""}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs, stderr, err := buildPVCDocsWithProfile(t, claimTemplateApp(t, tc.props), pvcCapabilityClusterYAML)
			if err != nil {
				t.Fatalf("template: %v\n%s", err, stderr)
			}
			var template map[string]any
			for _, d := range docs {
				if d["kind"] != "StatefulSet" {
					continue
				}
				spec, _ := d["spec"].(map[string]any)
				templates, _ := spec["volumeClaimTemplates"].([]any)
				if len(templates) != 1 {
					t.Fatalf("StatefulSet has %d claim templates, want 1", len(templates))
				}
				template, _ = templates[0].(map[string]any)
			}
			if template == nil {
				t.Fatal("no StatefulSet in the output")
			}
			templateSpec, _ := template["spec"].(map[string]any)

			traitProps := map[string]any{}
			for k, v := range tc.props {
				if k == "storageClass" {
					k = "storageClassName"
				}
				traitProps[k] = v
			}
			docs, stderr, err = buildPVCDocsWithProfile(t, pvcTwinApp(t, true, "data", traitProps), pvcCapabilityClusterYAML)
			if err != nil {
				t.Fatalf("trait: %v\n%s", err, stderr)
			}
			traitSpec, _ := claimDoc(t, docs, "data")["spec"].(map[string]any)

			got, present := templateSpec["storageClassName"]
			if !present {
				got = nil
			}
			if got != tc.want {
				t.Errorf("template spec.storageClassName = %#v (present %v), want %#v", got, present, tc.want)
			}
			if !reflect.DeepEqual(templateSpec, traitSpec) {
				ts, _ := yaml.Marshal(templateSpec)
				tr, _ := yaml.Marshal(traitSpec)
				t.Errorf("specs differ\ntemplate:\n%s\ntrait:\n%s", ts, tr)
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

// TestPVCTwin_SameRefusalsBothWays requires both paths to refuse the same
// malformed properties with the same message. A wrongly typed or out-of-enum
// value, an undeclared key (`dataSource` among them) and a nested object
// missing a required key are refused by the shared property schema before
// either parser runs; the rest by the kind's parser, which the trait now reads
// every claim field through.
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
		{name: "data source", props: map[string]any{"size": "1Gi", "dataSource": map[string]any{"kind": "PersistentVolumeClaim", "name": "golden"}}, want: `properties: unsupported field "dataSource"`},
		{name: "empty selector", props: map[string]any{"size": "1Gi", "selector": map[string]any{}}, want: "selector: empty selector"},
		{name: "invalid volume name", props: map[string]any{"size": "1Gi", "volumeName": "PV_Archive"}, want: `volumeName: invalid name "PV_Archive"`},
		{name: "non-string volume name", props: map[string]any{"size": "1Gi", "volumeName": 7}, want: "properties.volumeName: expected string"},
		{name: "data source ref without a name", props: map[string]any{"size": "1Gi", "dataSourceRef": map[string]any{"kind": "PersistentVolumeClaim"}}, want: `properties.dataSourceRef: "name" is required`},
		{name: "invalid volume attributes class", props: map[string]any{"size": "1Gi", "volumeAttributesClassName": "Gold_Class"}, want: `volumeAttributesClassName: invalid name "Gold_Class"`},
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
