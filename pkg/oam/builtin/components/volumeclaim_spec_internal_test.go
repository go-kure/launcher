package components

import (
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// vctProps wraps one volumeClaimTemplates entry in the props shape
// parseVolumeClaimTemplates reads.
func vctProps(entry map[string]any) map[string]any {
	return map[string]any{"volumeClaimTemplates": []any{entry}}
}

// TestVolumeClaimTemplateSchemaMatchesParser pins the published key set of one
// entry to volumeClaimTemplatePropertyKeys, and checks no rejected key is
// advertised.
func TestVolumeClaimTemplateSchemaMatchesParser(t *testing.T) {
	item := schemaVolumeClaimTemplates().Items
	if item == nil {
		t.Fatal("schemaVolumeClaimTemplates has no Items")
	}
	got := make([]string, 0, len(item.Properties))
	for k := range item.Properties {
		got = append(got, k)
	}
	slices.Sort(got)
	want := slices.Clone(volumeClaimTemplatePropertyKeys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("volumeClaimTemplates item keys = %v\nwant %v", got, want)
	}
	for k := range volumeClaimTemplateRejectedKeys {
		if _, ok := item.Properties[k]; ok {
			t.Errorf("schema publishes rejected key %q", k)
		}
	}

	// Nested levels: the top-level comparison above says nothing about them.
	assertSchemaKeysAt(t, *item, "resources", volumeClaimResourcesKeys)
	assertSchemaKeysAt(t, *item, "resources.requests", volumeClaimStorageKeys)
	assertSchemaKeysAt(t, *item, "resources.limits", volumeClaimStorageKeys)
	assertSchemaKeysAt(t, *item, "selector", volumeClaimSelectorKeys)
	assertSchemaKeysAt(t, *item, "selector.matchExpressions.[]", volumeClaimSelectorExprKeys)
	assertSchemaKeysAt(t, *item, "dataSourceRef", volumeClaimDataSourceRefKeys)
}

// assertSchemaKeysAt pins the Properties key set of the schema reached by walking
// `path` (dot-separated; the step "[]" descends into Items) to want — the same
// slice the parser hands rejectUnknownKeys at that level. Without this, a
// nested key published but never parsed, or parsed but never published, leaves
// both halves internally consistent and every other test green.
func assertSchemaKeysAt(t *testing.T, root oam.PropertySchema, path string, want []string) {
	t.Helper()
	cur := root
	for _, step := range strings.Split(path, ".") {
		if step == "[]" {
			if cur.Items == nil {
				t.Fatalf("%s: no Items to descend into", path)
			}
			cur = *cur.Items
			continue
		}
		next, ok := cur.Properties[step]
		if !ok {
			t.Fatalf("%s: schema has no property %q", path, step)
		}
		cur = next
	}
	got := make([]string, 0, len(cur.Properties))
	for k := range cur.Properties {
		got = append(got, k)
	}
	slices.Sort(got)
	w := slices.Clone(want)
	slices.Sort(w)
	if !slices.Equal(got, w) {
		t.Errorf("%s: schema keys = %v, parser accepts %v", path, got, w)
	}
}

// TestVolumeClaimTemplateSchema_EveryKeyDescribed walks the fragment: every
// property, nested property and array item carries a Description.
func TestVolumeClaimTemplateSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: missing Description", path)
		}
		for k, sub := range s.Properties {
			walk(path+"."+k, sub)
		}
		if s.Items != nil {
			walk(path+"[]", *s.Items)
		}
	}
	walk("volumeClaimTemplates", schemaVolumeClaimTemplates())
}

// TestParseVolumeClaimTemplates_SpecRoundTrip authors every claim-spec key the
// projection adds and checks the fields it lands in.
func TestParseVolumeClaimTemplates_SpecRoundTrip(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"size":      "10Gi",
		"mountPath": "/var/lib/data",
		"selector": map[string]any{
			"matchLabels": map[string]any{"tier": "fast"},
			"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In", "values": []any{"a", "b"}},
				map[string]any{"key": "legacy", "operator": "DoesNotExist"},
			},
		},
		"resources":  map[string]any{"limits": map[string]any{"storage": "20Gi"}},
		"volumeMode": "Filesystem",
		"dataSourceRef": map[string]any{
			"apiGroup":  "snapshot.storage.k8s.io",
			"kind":      "VolumeSnapshot",
			"name":      "seed",
			"namespace": "golden",
		},
		"volumeAttributesClassName": "gold",
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if len(vcts) != 1 {
		t.Fatalf("got %d templates, want 1", len(vcts))
	}
	spec := vcts[0].Spec

	if spec.Selector == nil {
		t.Fatal("Selector is nil")
	}
	if spec.Selector.MatchLabels["tier"] != "fast" {
		t.Errorf("Selector.MatchLabels = %v, want tier=fast", spec.Selector.MatchLabels)
	}
	if got := len(spec.Selector.MatchExpressions); got != 2 {
		t.Fatalf("MatchExpressions has %d entries, want 2", got)
	}
	if e := spec.Selector.MatchExpressions[0]; e.Key != "zone" ||
		e.Operator != metav1.LabelSelectorOpIn || !slices.Equal(e.Values, []string{"a", "b"}) {
		t.Errorf("MatchExpressions[0] = %+v, want zone In [a b]", e)
	}
	if e := spec.Selector.MatchExpressions[1]; e.Operator != metav1.LabelSelectorOpDoesNotExist || len(e.Values) != 0 {
		t.Errorf("MatchExpressions[1] = %+v, want DoesNotExist with no values", e)
	}
	if spec.Resources == nil {
		t.Fatal("Resources is nil")
	}
	if got := spec.Resources.Limits[corev1.ResourceStorage]; got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Errorf("Resources.Limits.storage = %v, want 20Gi", &got)
	}
	if spec.Resources.Requests != nil {
		t.Errorf("Resources.Requests = %v, want nil (size carries it)", spec.Resources.Requests)
	}
	if spec.VolumeMode == nil || *spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		t.Errorf("VolumeMode = %v, want Filesystem", spec.VolumeMode)
	}
	ref := spec.DataSourceRef
	if ref == nil || ref.Kind != "VolumeSnapshot" || ref.Name != "seed" ||
		ref.APIGroup == nil || *ref.APIGroup != "snapshot.storage.k8s.io" ||
		ref.Namespace == nil || *ref.Namespace != "golden" {
		t.Errorf("DataSourceRef = %+v, want the authored snapshot reference", ref)
	}
	if spec.VolumeAttributesClassName == nil || *spec.VolumeAttributesClassName != "gold" {
		t.Errorf("VolumeAttributesClassName = %v, want gold", spec.VolumeAttributesClassName)
	}
}

// TestParseVolumeClaimTemplates_LongSizeSpelling: resources.requests.storage
// satisfies the size requirement on its own.
func TestParseVolumeClaimTemplates_LongSizeSpelling(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"mountPath": "/var/lib/data",
		"resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}},
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if vcts[0].Size != "" {
		t.Errorf("Size = %q, want empty (only the long spelling was authored)", vcts[0].Size)
	}
	got := vcts[0].Spec.Resources.Requests[corev1.ResourceStorage]
	if got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Errorf("requests.storage = %v, want 5Gi", &got)
	}
}

// TestParseVolumeClaimTemplates_EmptySizeIsNotAuthored pins the consequence of
// reading `size` through parseStringField, which reports present=false for an
// empty string: `size: ""` is not an authored size, so pairing it with the long
// spelling is accepted rather than rejected as authoring both. Deriving
// sizeAuthored from raw key presence instead would make this an error, and
// nothing else in the suite would notice.
func TestParseVolumeClaimTemplates_EmptySizeIsNotAuthored(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"mountPath": "/var/lib/data",
		"size":      "",
		"resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}},
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if vcts[0].Size != "" {
		t.Errorf("Size = %q, want empty", vcts[0].Size)
	}
	got := vcts[0].Spec.Resources.Requests[corev1.ResourceStorage]
	if got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Errorf("requests.storage = %v, want 5Gi", &got)
	}
}

// TestParseVolumeClaimTemplates_UnauthoredSpecIsZero: an entry using none of the
// projected keys parses to the zero spec, so apply cannot move its output.
func TestParseVolumeClaimTemplates_UnauthoredSpecIsZero(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"size":      "10Gi",
		"mountPath": "/var/lib/data",
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if vcts[0].Spec != (VolumeClaimSpecConfig{}) {
		t.Errorf("Spec = %+v, want the zero config", vcts[0].Spec)
	}

	pvc := corev1.PersistentVolumeClaim{}
	vcts[0].Spec.apply(&pvc)
	if !pvc.Spec.Resources.Requests.Storage().IsZero() || pvc.Spec.Selector != nil ||
		pvc.Spec.VolumeMode != nil || pvc.Spec.DataSourceRef != nil ||
		pvc.Spec.VolumeAttributesClassName != nil {
		t.Errorf("apply wrote a field from the zero config: %+v", pvc.Spec)
	}
}

// TestVolumeClaimSpec_ApplyMergesRequests: authoring only `limits` leaves the
// constructor's requests.storage (from `size`) in place.
func TestVolumeClaimSpec_ApplyMergesRequests(t *testing.T) {
	pvc := corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		},
	}}
	cfg := VolumeClaimSpecConfig{Resources: &corev1.VolumeResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
	}}
	cfg.apply(&pvc)

	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Errorf("requests.storage = %v, want the constructor's 10Gi", &got)
	}
	if got := pvc.Spec.Resources.Limits[corev1.ResourceStorage]; got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Errorf("limits.storage = %v, want 20Gi", &got)
	}
}

func TestParseVolumeClaimTemplates_SpecErrors(t *testing.T) {
	base := map[string]any{"name": "data", "size": "10Gi", "mountPath": "/data"}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name    string
		entry   map[string]any
		wantErr string
	}{
		{"unknown key", with(map[string]any{"storageClassName": "fast"}), `unrecognized key "storageClassName"`},
		// The parity test proves the schema and the key slices agree; it does
		// not prove the parsers actually call rejectUnknownKeys with them. Each
		// nested closed object needs its own case, or the guard can be deleted
		// and the silent drop it exists to end comes back unobserved.
		{
			"unknown key in resources",
			with(map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}, "claims": []any{}}}),
			`unrecognized key "claims"`,
		},
		{
			"unknown key in a matchExpressions entry",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "Exists", "scope": "cluster"},
			}}}),
			`unrecognized key "scope"`,
		},
		// ValidateLabelSelectorRequirement runs IsValidLabelValue over every
		// value on a newly created claim template, so a space in a value is a
		// document the apiserver refuses; matchLabels was already guarded by
		// parseLabelMap and matchExpressions was not.
		{
			"invalid label value in a matchExpressions entry",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In", "values": []any{"bad value"}},
			}}}),
			`invalid label value "bad value"`,
		},
		// A storage limit below the request is inert upstream —
		// ValidatePersistentVolumeClaimSpec reads Requests[storage] only — so
		// this parser is the only place the contradiction can be reported.
		{
			"storage limit below the size shorthand",
			with(map[string]any{"resources": map[string]any{"limits": map[string]any{"storage": "5Gi"}}}),
			"resources.limits.storage (5Gi) is below the requested storage (10Gi)",
		},
		{
			"storage limit below the long request spelling",
			map[string]any{"name": "data", "mountPath": "/data", "resources": map[string]any{
				"requests": map[string]any{"storage": "10Gi"},
				"limits":   map[string]any{"storage": "5Gi"},
			}},
			"resources.limits.storage (5Gi) is below the requested storage (10Gi)",
		},
		{
			"unknown key in dataSourceRef",
			with(map[string]any{"dataSourceRef": map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "seed", "uid": "abc"}}),
			`unrecognized key "uid"`,
		},
		// The three required string leaves went through bare type assertions,
		// so a YAML number read as absent and surfaced as the requiredness
		// error for a field the author did write.
		{"non-string name", with(map[string]any{"name": 123}), "volumeClaimTemplate: name: must be a string, got int"},
		{"non-string size", with(map[string]any{"size": 1e10}), "size: must be a string, got float64"},
		{"non-string mountPath", with(map[string]any{"mountPath": 7}), "mountPath: must be a string, got int"},
		{"volumeName rejected", with(map[string]any{"volumeName": "pv-0"}), "volumeName: not authorable"},
		{"dataSource rejected", with(map[string]any{"dataSource": map[string]any{}}), "dataSource: not authorable"},
		{
			"size and long spelling together",
			with(map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}}}),
			"size and resources.requests.storage are the same field",
		},
		{
			"neither size nor long spelling",
			map[string]any{"name": "data", "mountPath": "/data"},
			"missing required field 'size'",
		},
		{
			"non-storage resource",
			with(map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "1"}}}),
			"is not a claim resource",
		},
		{
			"negative storage limit",
			with(map[string]any{"resources": map[string]any{"limits": map[string]any{"storage": "-1Gi"}}}),
			"quantity must be positive",
		},
		{
			"zero storage limit",
			with(map[string]any{"resources": map[string]any{"limits": map[string]any{"storage": "0"}}}),
			"quantity must be positive",
		},
		{
			"zero storage in the long request spelling",
			map[string]any{"name": "data", "mountPath": "/data", "resources": map[string]any{"requests": map[string]any{"storage": "0"}}},
			"quantity must be positive",
		},
		{
			"negative storage in the long request spelling",
			map[string]any{"name": "data", "mountPath": "/data", "resources": map[string]any{"requests": map[string]any{"storage": "-1Gi"}}},
			"quantity must be positive",
		},
		{
			"zero size in the short spelling",
			map[string]any{"name": "data", "mountPath": "/data", "size": "0"},
			"size must be positive",
		},
		{
			"negative size in the short spelling",
			map[string]any{"name": "data", "mountPath": "/data", "size": "-1Gi"},
			"size must be positive",
		},
		{"volumeMode enum", with(map[string]any{"volumeMode": "Raw"}), "volumeMode: invalid value"},
		{"volumeMode Block rejected", with(map[string]any{"volumeMode": "Block"}), "Block is not supported"},
		{
			"selector empty",
			with(map[string]any{"selector": map[string]any{}}),
			"empty selector",
		},
		// A nil test let these three through: parseLabelMap returns a non-nil
		// empty map, so `matchLabels: {}` produced the match-everything
		// selector the guard above exists to refuse.
		{
			"selector empty matchLabels",
			with(map[string]any{"selector": map[string]any{"matchLabels": map[string]any{}}}),
			"empty selector",
		},
		{
			"selector empty matchExpressions",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{}}}),
			"empty selector",
		},
		{
			"selector both empty",
			with(map[string]any{"selector": map[string]any{"matchLabels": map[string]any{}, "matchExpressions": []any{}}}),
			"empty selector",
		},
		{
			"storageClass invalid name",
			with(map[string]any{"storageClass": "Fast_Class"}),
			"invalid storageClass",
		},
		{
			"selector unknown key",
			with(map[string]any{"selector": map[string]any{"matchFields": []any{}}}),
			`unrecognized key "matchFields"`,
		},
		{
			"In without values",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In"},
			}}}),
			"at least one value is required for operator In",
		},
		{
			"Exists with values",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "Exists", "values": []any{"a"}},
			}}}),
			"must be empty for operator Exists",
		},
		{
			"unknown operator",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "Matches", "values": []any{"a"}},
			}}}),
			"operator: invalid value",
		},
		{
			"dataSourceRef missing kind",
			with(map[string]any{"dataSourceRef": map[string]any{"name": "seed"}}),
			"kind: required",
		},
		{
			"dataSourceRef core group with a foreign kind",
			with(map[string]any{"dataSourceRef": map[string]any{"kind": "VolumeSnapshot", "name": "seed"}}),
			"kind must be PersistentVolumeClaim when apiGroup names the core group",
		},
		{
			// The DNS-1123 check on a non-empty apiGroup, mirroring upstream
			// validateDataSourceRef. The valid-group and core-group cases both
			// pass this check, so nothing failed when it was removed.
			"dataSourceRef invalid apiGroup",
			with(map[string]any{"dataSourceRef": map[string]any{"apiGroup": "Not_Valid", "kind": "VolumeSnapshot", "name": "seed"}}),
			"dataSourceRef.apiGroup: invalid group",
		},
		{
			"dataSourceRef bad namespace",
			with(map[string]any{"dataSourceRef": map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "seed", "namespace": "Not_Valid"}}),
			"namespace: invalid name",
		},
		{
			"volumeAttributesClassName invalid",
			with(map[string]any{"volumeAttributesClassName": "Not_Valid"}),
			"volumeAttributesClassName: invalid name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseVolumeClaimTemplates(vctProps(tc.entry))
			if err == nil {
				t.Fatalf("parseVolumeClaimTemplates(%v) succeeded, want error containing %q", tc.entry, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestVolumeClaimTemplate_RejectedKeyOrderIsDeterministic: an entry authoring
// several rejected keys at once must always name the same one. The loop reads
// volumeClaimTemplateRejectedKeys, whose map iteration order is randomised, so
// without an explicit sort the reported key varies run to run — and a test that
// asserts one message passes or fails by luck.
func TestVolumeClaimTemplate_RejectedKeyOrderIsDeterministic(t *testing.T) {
	entry := map[string]any{
		"name": "data", "size": "10Gi", "mountPath": "/data",
		"volumeName": "pv-0",
		"dataSource": map[string]any{},
	}
	_, err := parseVolumeClaimTemplates(vctProps(entry))
	if err == nil {
		t.Fatal("parseVolumeClaimTemplates succeeded, want a rejected-key error")
	}
	first := err.Error()
	// Sorted order puts dataSource before volumeName, whatever the map does.
	if !strings.Contains(first, "dataSource") {
		t.Errorf("error = %q, want the sorted-first rejected key (dataSource)", first)
	}
	for i := range 50 {
		_, err := parseVolumeClaimTemplates(vctProps(entry))
		if err == nil {
			t.Fatalf("run %d: succeeded, want an error", i)
		}
		if err.Error() != first {
			t.Fatalf("run %d: error = %q, want the same message as run 0 (%q)", i, err.Error(), first)
		}
	}
}

// TestParseVolumeClaimSpec_NullIsOmission is the claim-level half of
// TestParseStatefulSetSpec_NullIsOmission: every optional field this change adds
// under a claim entry, authored as an explicit null. A lowering rule may emit
// one, pkg/oam's emission validation accepts it, so the parser must read it as
// omission rather than answering "must be an object/string, got <nil>" — which
// would let a component satisfy the published schema and then fail conversion.
func TestParseVolumeClaimSpec_NullIsOmission(t *testing.T) {
	// Every optional leaf at once. `size` carries a real value because the
	// entry needs one spelling of the storage request to be authored.
	entry := map[string]any{
		"name":                      "data",
		"mountPath":                 "/data",
		"size":                      "1Gi",
		"selector":                  nil,
		"resources":                 nil,
		"volumeMode":                nil,
		"dataSourceRef":             nil,
		"volumeAttributesClassName": nil,
	}
	got, err := parseVolumeClaimTemplates(vctProps(entry))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates(optional leaves null) error = %v, want them read as omitted", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d claim templates, want 1", len(got))
	}
	spec := got[0].Spec
	if spec.Selector != nil {
		t.Errorf("Selector = %v, want nil", spec.Selector)
	}
	if spec.VolumeMode != nil {
		t.Errorf("VolumeMode = %v, want nil", spec.VolumeMode)
	}
	if spec.DataSourceRef != nil {
		t.Errorf("DataSourceRef = %v, want nil", spec.DataSourceRef)
	}
	if spec.VolumeAttributesClassName != nil {
		t.Errorf("VolumeAttributesClassName = %v, want nil", spec.VolumeAttributesClassName)
	}

	// Nested leaves, each under an authored parent — the case the whole-object
	// nulls above cannot reach.
	for _, tc := range []struct {
		name  string
		entry map[string]any
	}{
		{"resources.requests", map[string]any{"resources": map[string]any{"requests": nil}}},
		{"resources.limits", map[string]any{"resources": map[string]any{"limits": nil}}},
		{"selector.matchLabels", map[string]any{"selector": map[string]any{
			"matchLabels": nil,
			"matchExpressions": []any{map[string]any{
				"key": "app", "operator": "Exists",
			}},
		}}},
		{"dataSourceRef.apiGroup", map[string]any{"dataSourceRef": map[string]any{
			"apiGroup": nil, "kind": "PersistentVolumeClaim", "name": "src",
		}}},
		{"dataSourceRef.namespace", map[string]any{"dataSourceRef": map[string]any{
			"kind": "PersistentVolumeClaim", "name": "src", "namespace": nil,
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}
			for k, v := range tc.entry {
				e[k] = v
			}
			if _, err := parseVolumeClaimTemplates(vctProps(e)); err != nil {
				t.Fatalf("parseVolumeClaimTemplates(%s: null) error = %v, want it read as omitted", tc.name, err)
			}
		})
	}

	// The three entry-level string leaves this change routes through the
	// checked path. They are required, so a null must surface as the
	// requiredness error — never as a type error naming <nil>.
	for _, key := range []string{"name", "size", "mountPath"} {
		t.Run(key+" null is required, not a type error", func(t *testing.T) {
			e := map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}
			e[key] = nil
			_, err := parseVolumeClaimTemplates(vctProps(e))
			if err == nil {
				t.Fatalf("parseVolumeClaimTemplates(%s: null) = nil error, want the requiredness error", key)
			}
			if strings.Contains(err.Error(), "<nil>") {
				t.Errorf("error = %q, want no type error naming <nil>", err.Error())
			}
		})
	}
}

// TestParseVolumeClaimTemplates_NotAList: a present-but-not-a-list value used
// to be indistinguishable from an absent key, so `volumeClaimTemplates: {}`
// built a StatefulSet with no claim templates and reported nothing.
func TestParseVolumeClaimTemplates_NotAList(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"object", map[string]any{"name": "data"}},
		{"string", "data"},
		{"number", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVolumeClaimTemplates(map[string]any{"volumeClaimTemplates": tc.val})
			if err == nil {
				t.Fatalf("parseVolumeClaimTemplates(%T) = %v, want an error", tc.val, got)
			}
			if !strings.Contains(err.Error(), "must be a list") {
				t.Errorf("error = %q, want it to name the type mismatch", err)
			}
		})
	}

	// An absent key is still absent, not an error.
	got, err := parseVolumeClaimTemplates(map[string]any{})
	if err != nil || got != nil {
		t.Errorf("absent volumeClaimTemplates = (%v, %v), want (nil, nil)", got, err)
	}

	// So is an explicit null. `volumeClaimTemplates:` with no value decodes to
	// a nil entry, the schema keeps the key optional, and pkg/oam's own
	// requiredness check reads a nil as absent — so a document written that way
	// is valid v1alpha1 and stays valid. This case is the reason the type check
	// above cannot be a bare "present means it must be a list": it would turn a
	// building document into a rejected one.
	got, err = parseVolumeClaimTemplates(map[string]any{"volumeClaimTemplates": nil})
	if err != nil || got != nil {
		t.Errorf("null volumeClaimTemplates = (%v, %v), want (nil, nil)", got, err)
	}
}

// TestParseStorageResourceList_RejectionOrderIsDeterministic: same class as the
// rejected-key scan above. A resources list authoring two non-storage names
// must always report the same one; without the sort the message varies run to
// run, and a test asserting one of them passes by luck.
func TestParseStorageResourceList_RejectionOrderIsDeterministic(t *testing.T) {
	entry := map[string]any{
		"name": "data", "size": "10Gi", "mountPath": "/data",
		"resources": map[string]any{"limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
	}
	_, err := parseVolumeClaimTemplates(vctProps(entry))
	if err == nil {
		t.Fatal("parseVolumeClaimTemplates succeeded, want a non-storage resource error")
	}
	first := err.Error()
	// Sorted order puts cpu before memory, whatever the map does.
	if !strings.Contains(first, `"cpu"`) {
		t.Errorf("error = %q, want the sorted-first offending key (cpu)", first)
	}
	for i := range 50 {
		_, err := parseVolumeClaimTemplates(vctProps(entry))
		if err == nil {
			t.Fatalf("run %d: succeeded, want an error", i)
		}
		if err.Error() != first {
			t.Fatalf("run %d: error = %q, want the same message as run 0 (%q)", i, err.Error(), first)
		}
	}
}
