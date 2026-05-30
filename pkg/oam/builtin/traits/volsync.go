package traits

import (
	"math"

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

	// Sub-app name uses sourcePVC as identifier (not component name) to match
	// crane's stable naming. Two components with the same PVC name in the same
	// bundle would collide; OAM authors are expected to use unique PVC names.
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

	if v, ok := props["pruneIntervalDays"]; ok {
		pid, ok := yamlToInt(v)
		if !ok {
			return nil, errors.New("'pruneIntervalDays' must be a positive integer")
		}
		if pid <= 0 || pid > math.MaxInt32 {
			return nil, errors.Errorf("'pruneIntervalDays' must be between 1 and %d", math.MaxInt32)
		}
		config.PruneIntervalDays = pid
	}

	if rawRetain, ok := props["retain"].(map[string]any); ok {
		for field, dst := range map[string]*int{
			"daily":   &config.RetainDaily,
			"weekly":  &config.RetainWeekly,
			"monthly": &config.RetainMonthly,
		} {
			if v, ok := rawRetain[field]; ok {
				n, ok := yamlToInt(v)
				if !ok {
					return nil, errors.Errorf("'retain.%s' must be a non-negative integer", field)
				}
				if n < 0 || n > math.MaxInt32 {
					return nil, errors.Errorf("'retain.%s' must be between 0 and %d", field, math.MaxInt32)
				}
				*dst = n
			}
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
// Non-integer float values (e.g. 1.5) are rejected.
func yamlToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
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
