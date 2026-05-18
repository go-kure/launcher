package traits

import (
	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	kurevol "github.com/go-kure/kure/pkg/kubernetes/volsync"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// VolSyncHandler handles OAM volsync traits.
type VolSyncHandler struct{}

// CanHandle returns true for volsync trait type.
func (h *VolSyncHandler) CanHandle(traitType string) bool {
	return traitType == "volsync"
}

// ValidateAndApplyDefaults rejects any rendering key for this no-rendering trait.
func (h *VolSyncHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.VolSyncRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "volsync rendering")
	}
	return rendering, nil
}

// Apply creates a VolSync ReplicationSource resource appended to the bundle.
func (h *VolSyncHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	rsApp := stack.NewApplication(config.SourcePVC+"-backup", app.Namespace, config)
	bundle.Applications = append(bundle.Applications, rsApp)
	return nil
}

func (h *VolSyncHandler) parseProperties(props map[string]any, app *stack.Application) (*VolsyncConfig, error) {
	sourcePVC, ok := props["sourcePVC"].(string)
	if !ok || sourcePVC == "" {
		return nil, errors.New("required property 'sourcePVC' missing or not a string")
	}

	schedule, ok := props["schedule"].(string)
	if !ok || schedule == "" {
		return nil, errors.New("required property 'schedule' missing or not a string")
	}

	config := &VolsyncConfig{
		ComponentName:     app.Name,
		SourcePVC:         sourcePVC,
		Schedule:          schedule,
		CopyMethod:        "Snapshot",
		PruneIntervalDays: 14,
		RetainDaily:       7,
		RetainWeekly:      4,
		RetainMonthly:     3,
		Repository:        app.Name + "-volsync-secret",
	}

	if repo, ok := props["repository"].(string); ok && repo != "" {
		config.Repository = repo
	}

	if cm, ok := props["copyMethod"].(string); ok && cm != "" {
		switch cm {
		case "Snapshot", "Direct", "Clone":
			config.CopyMethod = cm
		default:
			return nil, errors.Errorf("unsupported copyMethod %q; supported: Snapshot, Direct, Clone", cm)
		}
	}

	if sc, ok := props["storageClassName"].(string); ok {
		config.StorageClassName = sc
	}
	if vsc, ok := props["volumeSnapshotClassName"].(string); ok {
		config.VolumeSnapshotClassName = vsc
	}

	if pid, ok := yamlToInt(props["pruneIntervalDays"]); ok {
		config.PruneIntervalDays = pid
	}

	if rawRetain, ok := props["retain"].(map[string]any); ok {
		if d, ok := yamlToInt(rawRetain["daily"]); ok {
			config.RetainDaily = d
		}
		if w, ok := yamlToInt(rawRetain["weekly"]); ok {
			config.RetainWeekly = w
		}
		if m, ok := yamlToInt(rawRetain["monthly"]); ok {
			config.RetainMonthly = m
		}
	}

	return config, nil
}

// VolsyncConfig implements stack.ApplicationConfig for volsync traits.
type VolsyncConfig struct {
	ComponentName           string
	SourcePVC               string
	Schedule                string
	Repository              string
	CopyMethod              string
	StorageClassName        string
	VolumeSnapshotClassName string
	PruneIntervalDays       int
	RetainDaily             int
	RetainWeekly            int
	RetainMonthly           int
}

// yamlToInt converts a numeric value (int, float64) from YAML to int.
func yamlToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// Generate creates a VolSync ReplicationSource resource.
func (c *VolsyncConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	pruneInterval := int32(c.PruneIntervalDays) //nolint:gosec
	daily := int32(c.RetainDaily)               //nolint:gosec
	weekly := int32(c.RetainWeekly)             //nolint:gosec
	monthly := int32(c.RetainMonthly)           //nolint:gosec

	mover := &kurevol.SourceResticConfig{
		Repository:        c.Repository,
		PruneIntervalDays: &pruneInterval,
		ReplicationSourceVolumeOptions: volsyncv1alpha1.ReplicationSourceVolumeOptions{
			CopyMethod: kurevol.CopyMethod(c.CopyMethod),
		},
		Retain: &volsyncv1alpha1.ResticRetainPolicy{
			Daily:   &daily,
			Weekly:  &weekly,
			Monthly: &monthly,
		},
	}
	if c.StorageClassName != "" {
		sc := c.StorageClassName
		mover.StorageClassName = &sc
	}
	if c.VolumeSnapshotClassName != "" {
		vsc := c.VolumeSnapshotClassName
		mover.VolumeSnapshotClassName = &vsc
	}

	schedule := c.Schedule
	rs := kurevol.ReplicationSource(&kurevol.ReplicationSourceConfig{
		Name:      app.Name,
		Namespace: app.Namespace,
		SourcePVC: c.SourcePVC,
		Trigger:   &kurevol.TriggerConfig{Schedule: &schedule},
		Mover:     mover,
	})

	obj := client.Object(rs)
	return []*client.Object{&obj}, nil
}
