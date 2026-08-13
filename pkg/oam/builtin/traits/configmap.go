package traits

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// ConfigMapHandler handles OAM configmap traits.
type ConfigMapHandler struct{}

// CanHandle returns true for configmap trait type.
func (h *ConfigMapHandler) CanHandle(traitType string) bool {
	return traitType == "configmap"
}

// PropertySchema declares the configmap trait's user-facing properties so the
// downstream runtime can validate them before invocation. `data` is an open map (escape hatch).
func (h *ConfigMapHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"name":      {Type: oam.PropertyTypeString, Required: true, Description: "Name of the ConfigMap resource to create."},
		"mountPath": {Type: oam.PropertyTypeString, Description: "Path at which the ConfigMap is mounted as a volume into the component's workload; when set, the component is decorated with the volume mount."},
		"data":      {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Key/value pairs stored in the ConfigMap data (values are stringified)."},
	}
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

	var mountPath string
	if mp, ok := props["mountPath"].(string); ok {
		mountPath = mp
	}

	data := make(map[string]string)
	if rawData, ok := props["data"].(map[string]any); ok {
		for k, v := range rawData {
			data[k] = fmt.Sprintf("%v", v)
		}
	}

	cmConfig := &ConfigMapConfig{
		Name:          name,
		componentName: app.Name,
		Data:          data,
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
	Name          string
	componentName string
	Data          map[string]string
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *ConfigMapConfig) ComponentName() string { return c.componentName }

// Generate creates a Kubernetes ConfigMap resource.
func (c *ConfigMapConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	cm := kubernetes.CreateConfigMap(app.Name, app.Namespace)
	kubernetes.SetConfigMapLabels(cm, map[string]string{"app": c.componentName})
	cm.Annotations = nil
	if len(c.Data) > 0 {
		kubernetes.AddConfigMapDataMap(cm, c.Data)
	}

	obj := client.Object(cm)
	return []*client.Object{&obj}, nil
}

// ConfigMapDecorator wraps an ApplicationConfig to add a volume and volumeMount
// for a ConfigMap to any supported workload in the generated resources.
type ConfigMapDecorator struct {
	decoratorBase
	ConfigMapName string
	MountPath     string
}

// NewConfigMapDecorator wraps inner so the named ConfigMap is mounted at mountPath.
func NewConfigMapDecorator(inner stack.ApplicationConfig, configMapName, mountPath string) *ConfigMapDecorator {
	return &ConfigMapDecorator{
		decoratorBase: decoratorBase{Inner: inner},
		ConfigMapName: configMapName,
		MountPath:     mountPath,
	}
}

// Generate calls the inner config's Generate and mounts the ConfigMap into any
// Deployment, StatefulSet, DaemonSet, or CronJob resource found.
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
		default:
			continue
		}
		if err := checkVolumeCollision(podSpec, d.ConfigMapName,
			"configmap mountPath", "rename the configmap via the 'name' property"); err != nil {
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
		return nil, errors.New("configmap mountPath requires a Deployment, StatefulSet, DaemonSet, or CronJob component; no supported workload resource was found")
	}

	return objects, nil
}
