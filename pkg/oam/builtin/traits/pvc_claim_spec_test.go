package traits_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The pvc trait reads selector, dataSourceRef, volumeName and
// volumeAttributesClassName as the persistentvolumeclaim kind does
// (go-kure/launcher#790); `kurel build` compares the two claims in
// pkg/cmd/kurel (TestPVCTwin_SameClaimBothWays).

// TestPVCHandler_SchemaIsTheKindsPlusName: the trait publishes every key the
// kind publishes, and `name` on top.
func TestPVCHandler_SchemaIsTheKindsPlusName(t *testing.T) {
	kind := slices.Sorted(maps.Keys((&components.PersistentVolumeClaimHandler{}).PropertySchema()))
	trait := slices.Sorted(maps.Keys((&traits.PVCHandler{}).PropertySchema()))
	want := slices.Sorted(slices.Values(append(slices.Clone(kind), "name")))
	if !slices.Equal(trait, want) {
		t.Fatalf("pvc trait schema keys = %v\nwant the kind's plus name: %v", trait, want)
	}
	for _, key := range []string{"selector", "dataSourceRef", "volumeName", "volumeAttributesClassName"} {
		if !slices.Contains(trait, key) {
			t.Errorf("pvc trait schema does not publish %q", key)
		}
	}
}

func TestPVCHandler_Apply_ClaimSpecFields(t *testing.T) {
	bundle := newBundle()
	err := (&traits.PVCHandler{}).Apply(&oam.Trait{Type: "pvc", Properties: map[string]any{
		"name":                      "disk",
		"size":                      "5Gi",
		"selector":                  map[string]any{"matchLabels": map[string]any{"tier": "archive"}},
		"dataSourceRef":             map[string]any{"kind": "PersistentVolumeClaim", "name": "golden"},
		"volumeName":                "pv-archive-0",
		"volumeAttributesClassName": "gold",
	}}, newApp("api", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	pvc := (*objs[0]).(*corev1.PersistentVolumeClaim)
	if sel := pvc.Spec.Selector; sel == nil || sel.MatchLabels["tier"] != "archive" {
		t.Errorf("selector = %+v, want tier=archive", sel)
	}
	if ref := pvc.Spec.DataSourceRef; ref == nil || ref.Kind != "PersistentVolumeClaim" || ref.Name != "golden" {
		t.Errorf("dataSourceRef = %+v, want the claim golden", ref)
	}
	if pvc.Spec.VolumeName != "pv-archive-0" {
		t.Errorf("volumeName = %q, want pv-archive-0", pvc.Spec.VolumeName)
	}
	if got := pvc.Spec.VolumeAttributesClassName; got == nil || *got != "gold" {
		t.Errorf("volumeAttributesClassName = %v, want gold", got)
	}
}

// The trait refuses what the kind refuses, with the kind's message under the
// trait's own prefix.
func TestPVCHandler_Apply_RejectsClaimSpecFields(t *testing.T) {
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"dataSource":          {map[string]any{"dataSource": map[string]any{"kind": "PersistentVolumeClaim", "name": "golden"}}, `PVC "disk": dataSource: not authorable — superseded by dataSourceRef`},
		"empty selector":      {map[string]any{"selector": map[string]any{}}, `PVC "disk": selector: empty selector`},
		"invalid volume name": {map[string]any{"volumeName": "PV_Archive"}, `PVC "disk": volumeName: invalid name "PV_Archive"`},
		"data source ref":     {map[string]any{"dataSourceRef": map[string]any{"kind": "VolumeSnapshot", "name": "nightly"}}, `PVC "disk": dataSourceRef: kind must be PersistentVolumeClaim`},
		"attributes class":    {map[string]any{"volumeAttributesClassName": "Gold_Class"}, `PVC "disk": volumeAttributesClassName: invalid name "Gold_Class"`},
	} {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"name": "disk", "size": "5Gi"}
			maps.Copy(props, tc.props)
			err := (&traits.PVCHandler{}).Apply(&oam.Trait{Type: "pvc", Properties: props}, newApp("api", "default"), newBundle())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
