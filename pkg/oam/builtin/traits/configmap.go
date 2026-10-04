package traits

import (
	"maps"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// ConfigMapHandler handles OAM configmap traits. It is the configmap kind's
// twin (go-kure/launcher#741): data, binaryData and immutable are read and the
// ConfigMap is built by the kind's own components.ParseConfigMapProperties
// and GenerateConfigMap. The trait adds only what ownership means: the
// ConfigMap's name (its `name` property), the owner's labels and namespace,
// the owner's bundle, and the optional mount into the owner's workload.
type ConfigMapHandler struct{}

// CanHandle returns true for configmap trait type.
func (h *ConfigMapHandler) CanHandle(traitType string) bool {
	return traitType == "configmap"
}

// PropertySchema declares the configmap trait's user-facing properties: the
// configmap kind's, plus the ConfigMap's `name` and the optional `mountPath`.
func (h *ConfigMapHandler) PropertySchema() map[string]oam.PropertySchema {
	schema := maps.Clone((&components.ConfigMapHandler{}).PropertySchema())
	schema["name"] = oam.PropertySchema{Type: oam.PropertyTypeString, Required: true, Description: "Name of the ConfigMap resource to create."}
	schema["mountPath"] = oam.PropertySchema{Type: oam.PropertyTypeString, Description: "Path at which the ConfigMap is mounted as a volume into the component's workload; when set, the component is decorated with the volume mount."}
	return schema
}

// ValidateAndApplyDefaults rejects any rendering key for this no-rendering trait.
func (h *ConfigMapHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.ConfigmapRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "configmap rendering")
	}
	return rendering, nil
}

// Apply creates a ConfigMap resource and optionally wraps the component's config
// with a decorator that mounts the ConfigMap as a volume.
func (h *ConfigMapHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	props := trait.Properties

	name, ok := props["name"].(string)
	if !ok || name == "" {
		return errors.New("required property 'name' missing or not a string")
	}
	if err := checkAuthoredObjectName("name", "the ConfigMap", name); err != nil {
		return err
	}

	var mountPath string
	if mp, ok := props["mountPath"].(string); ok {
		mountPath = mp
	}

	cm, err := components.ParseConfigMapProperties(props)
	if err != nil {
		return errors.Wrapf(err, "configmap trait %q", name)
	}
	cmConfig := &ConfigMapConfig{
		Name:          name,
		componentName: app.Name,
		ConfigMap:     cm,
	}
	cmApp := stack.NewApplication(name, app.Namespace, cmConfig)
	bundle.Applications = append(bundle.Applications, cmApp)

	if mountPath != "" {
		app.Config = NewConfigMapDecorator(app.Config, name, mountPath)
	}

	return nil
}

// ConfigMapConfig implements stack.ApplicationConfig for configmap traits.
type ConfigMapConfig struct {
	// Name is the ConfigMap's name.
	Name          string
	componentName string
	// ConfigMap carries data, binaryData and immutable as the configmap kind
	// parses them; its Name and Namespace are unset.
	ConfigMap components.ConfigMapConfig
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *ConfigMapConfig) ComponentName() string { return c.componentName }

// FluxNamespaceInput names the ConfigMap, so it follows its component's Flux
// object to the Flux namespace when that object reads it (a helmrelease's
// valuesFrom). Satisfies pkg/oam.fluxNamespaceInput.
func (c *ConfigMapConfig) FluxNamespaceInput() (kind, name string) { return "ConfigMap", c.Name }

// Generate builds the ConfigMap through the kind's GenerateConfigMap, under
// the trait's name and with the owning component's labels.
func (c *ConfigMapConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return components.GenerateConfigMap(c.ConfigMap, app.Name, app.Namespace, componentLabels(c.componentName))
}

// ConfigMapDecorator wraps an ApplicationConfig to add a volume and volumeMount
// for a ConfigMap to any supported workload in the generated resources.
type ConfigMapDecorator struct {
	decoratorBase
	ConfigMapName string
	MountPath     string
}

// NewConfigMapDecorator wraps inner so the named ConfigMap is mounted at mountPath.
func NewConfigMapDecorator(inner stack.ApplicationConfig, configMapName, mountPath string) stack.ApplicationConfig {
	dec := &ConfigMapDecorator{
		decoratorBase: decoratorBase{Inner: inner},
		ConfigMapName: configMapName,
		MountPath:     mountPath,
	}
	return wrapIfAugmenter(dec, inner)
}

// Generate calls the inner config's Generate and mounts the ConfigMap into any
// Deployment, StatefulSet, DaemonSet, Job, CronJob, or Pod resource found.
func (d *ConfigMapDecorator) Generate(app *stack.Application) ([]*client.Object, error) {
	objects, err := d.Inner.Generate(app)
	if err != nil {
		return nil, err
	}

	mounted := false
	for _, objPtr := range objects {
		var podSpec *corev1.PodSpec
		switch w := (*objPtr).(type) {
		case *appsv1.Deployment:
			podSpec = &w.Spec.Template.Spec
		case *appsv1.StatefulSet:
			podSpec = &w.Spec.Template.Spec
		case *appsv1.DaemonSet:
			podSpec = &w.Spec.Template.Spec
		case *batchv1.CronJob:
			podSpec = &w.Spec.JobTemplate.Spec.Template.Spec
		case *batchv1.Job:
			podSpec = &w.Spec.Template.Spec
		case *corev1.Pod:
			podSpec = &w.Spec
		default:
			continue
		}
		if err := checkVolumeCollision(podSpec, d.ConfigMapName,
			"configmap mountPath", "rename the configmap via the 'name' property"); err != nil {
			return nil, err
		}
		if err := checkMountPathCollision(podSpec, d.MountPath,
			"configmap mountPath", "change the mountPath or the colliding volume's mount"); err != nil {
			return nil, err
		}
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: d.ConfigMapName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: d.ConfigMapName},
				},
			},
		})
		if len(podSpec.Containers) > 0 {
			podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts,
				corev1.VolumeMount{Name: d.ConfigMapName, MountPath: d.MountPath})
		}
		mounted = true
	}

	if !mounted {
		return nil, errors.New("configmap mountPath requires a Deployment, StatefulSet, DaemonSet, Job, CronJob, or Pod component; no supported workload resource was found")
	}

	return objects, nil
}
