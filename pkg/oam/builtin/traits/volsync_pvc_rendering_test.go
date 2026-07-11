package traits_test

import (
	"errors"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// volsyncSourceFromProps applies a volsync trait with the given inline properties
// and returns the generated ReplicationSource.
func volsyncSourceFromProps(t *testing.T, props map[string]any) *volsyncv1alpha1.ReplicationSource {
	t.Helper()
	h := &traits.VolSyncHandler{}
	bundle := newBundle()
	if err := h.Apply(&oam.Trait{Type: "volsync", Properties: props}, newApp("db", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	rs, ok := (*objs[0]).(*volsyncv1alpha1.ReplicationSource)
	if !ok {
		t.Fatalf("expected *ReplicationSource, got %T", *objs[0])
	}
	return rs
}

// --- VolSyncHandler ValidateAndApplyDefaults: class keys accepted ---

func TestVolSyncHandler_ValidateAndApplyDefaults_AcceptsClassKeys(t *testing.T) {
	h := &traits.VolSyncHandler{}
	out, err := h.ValidateAndApplyDefaults(map[string]any{
		"storageClassName":        "fast",
		"volumeSnapshotClassName": "csi-snapclass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["storageClassName"] != "fast" || out["volumeSnapshotClassName"] != "csi-snapclass" {
		t.Errorf("expected class keys preserved, got %v", out)
	}
}

// --- copyMethod-aware class injection in Generate ---

func TestVolsyncGenerate_Snapshot_InjectsBothClasses(t *testing.T) {
	rs := volsyncSourceFromProps(t, map[string]any{
		"sourcePVC":               "data",
		"schedule":                "@daily",
		"copyMethod":              "Snapshot",
		"storageClassName":        "fast",
		"volumeSnapshotClassName": "csi-snapclass",
	})
	if rs.Spec.Restic.StorageClassName == nil || *rs.Spec.Restic.StorageClassName != "fast" {
		t.Errorf("Snapshot: expected storageClassName=fast, got %v", rs.Spec.Restic.StorageClassName)
	}
	if rs.Spec.Restic.VolumeSnapshotClassName == nil || *rs.Spec.Restic.VolumeSnapshotClassName != "csi-snapclass" {
		t.Errorf("Snapshot: expected volumeSnapshotClassName=csi-snapclass, got %v", rs.Spec.Restic.VolumeSnapshotClassName)
	}
}

func TestVolsyncGenerate_Clone_InjectsStorageClassOnly(t *testing.T) {
	rs := volsyncSourceFromProps(t, map[string]any{
		"sourcePVC":               "data",
		"schedule":                "@daily",
		"copyMethod":              "Clone",
		"storageClassName":        "fast",
		"volumeSnapshotClassName": "csi-snapclass",
	})
	if rs.Spec.Restic.StorageClassName == nil || *rs.Spec.Restic.StorageClassName != "fast" {
		t.Errorf("Clone: expected storageClassName=fast, got %v", rs.Spec.Restic.StorageClassName)
	}
	if rs.Spec.Restic.VolumeSnapshotClassName != nil {
		t.Errorf("Clone: expected volumeSnapshotClassName dropped, got %v", *rs.Spec.Restic.VolumeSnapshotClassName)
	}
}

func TestVolsyncGenerate_Direct_DropsBothInlineClasses(t *testing.T) {
	rs := volsyncSourceFromProps(t, map[string]any{
		"sourcePVC":               "data",
		"schedule":                "@daily",
		"copyMethod":              "Direct",
		"storageClassName":        "fast",
		"volumeSnapshotClassName": "csi-snapclass",
	})
	if rs.Spec.Restic.StorageClassName != nil {
		t.Errorf("Direct: expected storageClassName dropped, got %v", *rs.Spec.Restic.StorageClassName)
	}
	if rs.Spec.Restic.VolumeSnapshotClassName != nil {
		t.Errorf("Direct: expected volumeSnapshotClassName dropped, got %v", *rs.Spec.Restic.VolumeSnapshotClassName)
	}
}

// --- End-to-end: capability rendering delivers/drops the classes (rendered source) ---

// renderComponentHandler is a trivial component handler used to wire a component
// carrying a volsync trait through the full Transform pipeline.
type renderComponentHandler struct{ typ string }

func (h *renderComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *renderComponentHandler) ToApplicationConfig(_ *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return &emptyGenerateConfig{}, nil
}

type emptyGenerateConfig struct{}

func (emptyGenerateConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }

// volsyncSourceFromCluster walks the transformed cluster, finds the volsync
// sub-app, and returns its generated ReplicationSource.
func volsyncSourceFromCluster(t *testing.T, cluster *stack.Cluster) *volsyncv1alpha1.ReplicationSource {
	t.Helper()
	var apps []*stack.Application
	var walk func(n *stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			apps = append(apps, n.Bundle.Applications...)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)

	for _, a := range apps {
		if _, ok := a.Config.(*traits.VolsyncConfig); !ok {
			continue
		}
		objs, err := a.Config.Generate(a)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(objs) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objs))
		}
		rs, ok := (*objs[0]).(*volsyncv1alpha1.ReplicationSource)
		if !ok {
			t.Fatalf("expected *ReplicationSource, got %T", *objs[0])
		}
		return rs
	}
	t.Fatal("no volsync sub-app found in cluster")
	return nil
}

func transformVolsyncWithRendering(t *testing.T, copyMethod string) *volsyncv1alpha1.ReplicationSource {
	t.Helper()
	tr := oam.NewTransformer(
		map[string]oam.ComponentHandler{"webservice": &renderComponentHandler{typ: "webservice"}},
		map[string]oam.TraitHandler{"volsync": &traits.VolSyncHandler{}},
	)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web",
			Type: "webservice",
			Traits: []oam.Trait{{
				Type: "volsync",
				Properties: map[string]any{
					"sourcePVC":  "data",
					"schedule":   "@daily",
					"copyMethod": copyMethod,
				},
			}},
		}}},
	}
	// storageClassName + volumeSnapshotClassName arrive only via capability rendering,
	// never authored inline: this exercises the rendered (crane-supplied) source.
	ctx := oam.TransformContext{Capabilities: map[string]oam.CapabilityBinding{
		"volsync": {Rendering: map[string]any{
			"storageClassName":        "fast",
			"volumeSnapshotClassName": "csi-snapclass",
		}},
	}}
	cluster, err := tr.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	return volsyncSourceFromCluster(t, cluster)
}

func TestVolSync_CapabilityRendering_Clone_InjectsRenderedStorageClass(t *testing.T) {
	rs := transformVolsyncWithRendering(t, "Clone")
	if rs.Spec.Restic.StorageClassName == nil || *rs.Spec.Restic.StorageClassName != "fast" {
		t.Errorf("Clone: expected rendered storageClassName=fast, got %v", rs.Spec.Restic.StorageClassName)
	}
	if rs.Spec.Restic.VolumeSnapshotClassName != nil {
		t.Errorf("Clone: expected volumeSnapshotClassName dropped, got %v", *rs.Spec.Restic.VolumeSnapshotClassName)
	}
}

func TestVolSync_CapabilityRendering_Direct_DropsRenderedClasses(t *testing.T) {
	rs := transformVolsyncWithRendering(t, "Direct")
	if rs.Spec.Restic.StorageClassName != nil {
		t.Errorf("Direct: expected rendered storageClassName dropped, got %v", *rs.Spec.Restic.StorageClassName)
	}
	if rs.Spec.Restic.VolumeSnapshotClassName != nil {
		t.Errorf("Direct: expected rendered volumeSnapshotClassName dropped, got %v", *rs.Spec.Restic.VolumeSnapshotClassName)
	}
}

// --- EvaluateProfile: real handlers accept valid rendering, reject typos ---

func TestEvaluateProfile_VolSyncRendering_Accepted(t *testing.T) {
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{"volsync": &traits.VolSyncHandler{}})
	profile := &oam.ClusterProfile{Spec: oam.ClusterProfileSpec{Capabilities: map[string]oam.CapabilityBinding{
		"volsync": {Rendering: map[string]any{
			"storageClassName":        "fast",
			"volumeSnapshotClassName": "csi-snapclass",
		}},
	}}}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Capabilities["volsync"].Rendering["storageClassName"] != "fast" {
		t.Errorf("expected storageClassName preserved, got %v", got.Spec.Capabilities["volsync"].Rendering)
	}
}

func TestEvaluateProfile_VolSyncRendering_RejectsUnknownKey(t *testing.T) {
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{"volsync": &traits.VolSyncHandler{}})
	profile := &oam.ClusterProfile{Spec: oam.ClusterProfileSpec{Capabilities: map[string]oam.CapabilityBinding{
		"volsync": {Rendering: map[string]any{"storageClass": "fast"}}, // typo
	}}}
	_, err := tr.EvaluateProfile(profile)
	if err == nil {
		t.Fatal("expected error for unknown volsync rendering key")
	}
	var te *oam.TransformError
	if !errors.As(err, &te) {
		t.Errorf("expected *TransformError, got %T: %v", err, err)
	}
}

// --- PVCHandler ValidateAndApplyDefaults + EvaluateProfile ---

func TestPVCHandler_ValidateAndApplyDefaults_AcceptsStorageClassName(t *testing.T) {
	h := &traits.PVCHandler{}
	out, err := h.ValidateAndApplyDefaults(map[string]any{"storageClassName": "fast"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["storageClassName"] != "fast" {
		t.Errorf("expected storageClassName preserved, got %v", out)
	}
}

func TestPVCHandler_ValidateAndApplyDefaults_RejectsUnknownKey(t *testing.T) {
	h := &traits.PVCHandler{}
	if _, err := h.ValidateAndApplyDefaults(map[string]any{"storageClas": "fast"}); err == nil {
		t.Fatal("expected error for unknown pvc rendering key")
	}
}

func TestEvaluateProfile_PVCRendering_Accepted(t *testing.T) {
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{"pvc": &traits.PVCHandler{}})
	profile := &oam.ClusterProfile{Spec: oam.ClusterProfileSpec{Capabilities: map[string]oam.CapabilityBinding{
		"pvc": {Rendering: map[string]any{"storageClassName": "fast"}},
	}}}
	got, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Spec.Capabilities["pvc"].Rendering["storageClassName"] != "fast" {
		t.Errorf("expected storageClassName preserved, got %v", got.Spec.Capabilities["pvc"].Rendering)
	}
}

func TestEvaluateProfile_PVCRendering_RejectsUnknownKey(t *testing.T) {
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{"pvc": &traits.PVCHandler{}})
	profile := &oam.ClusterProfile{Spec: oam.ClusterProfileSpec{Capabilities: map[string]oam.CapabilityBinding{
		"pvc": {Rendering: map[string]any{"storageClas": "fast"}}, // typo
	}}}
	_, err := tr.EvaluateProfile(profile)
	if err == nil {
		t.Fatal("expected error for unknown pvc rendering key")
	}
	var te *oam.TransformError
	if !errors.As(err, &te) {
		t.Errorf("expected *TransformError, got %T: %v", err, err)
	}
}
