package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// DaemonsetHandler handles OAM daemonset components.
type DaemonsetHandler struct{}

// CanHandle returns true for daemonset component type.
func (h *DaemonsetHandler) CanHandle(componentType string) bool {
	return componentType == "daemonset"
}

// ToApplicationConfig converts an OAM daemonset component to a DaemonsetConfig.
func (h *DaemonsetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &DaemonsetConfig{
		Name:      component.Name,
		Namespace: namespace,
	}

	props := component.Properties

	image, ok := props["image"].(string)
	if !ok {
		return nil, errors.New("required property 'image' missing or not a string")
	}
	if err := ValidateImageRef(image); err != nil {
		return nil, err
	}
	config.Image = image

	env, err := parseEnv(props)
	if err != nil {
		return nil, err
	}
	config.Env = env
	if resources, ok := props["resources"].(map[string]any); ok {
		config.Resources = parseResources(resources)
	}
	config.explicitResources = resourceExplicitFlags(props)
	config.Command = parseCommand(props)
	config.Args = parseArgs(props)
	probes, err := parseProbes(props)
	if err != nil {
		return nil, errors.Wrap(err, "invalid probe configuration")
	}
	config.Probes = probes

	tolerations, err := parseTolerations(props)
	if err != nil {
		return nil, err
	}
	config.Tolerations = tolerations
	parsed, err := parseVolumes(props)
	if err != nil {
		return nil, err
	}
	config.Volumes = parsed.Volumes
	config.VolumeMounts = parsed.Mounts
	config.PVCs = parsed.PVCs

	initContainers, err := parseInitContainers(props)
	if err != nil {
		return nil, err
	}
	config.InitContainers = initContainers

	if port, ok := toInt32(props["port"]); ok {
		config.Port = port
	}

	return config, nil
}

// DaemonsetConfig implements stack.ApplicationConfig for daemonset components.
type DaemonsetConfig struct {
	Name              string
	Namespace         string
	Image             string
	Port              int32 // when > 0, generates a ClusterIP Service exposing this port
	Env               []EnvVar
	Resources         ResourceRequirements
	Command           []string
	Args              []string
	Probes            ProbeConfig
	Tolerations       []corev1.Toleration
	Volumes           []corev1.Volume
	VolumeMounts      []corev1.VolumeMount
	InitContainers    []InitContainerConfig
	PVCs              []PVCConfig
	explicitResources explicitResourceFlags
}

// ServicePort implements servicePortProvider, making DaemonsetConfig usable as an
// implicit backend for ingress, httproute, and expose traits.
func (c *DaemonsetConfig) ServicePort() int32 { return c.Port }

// ApplyPolicy applies defaults then enforces limits from the policy.
// DaemonSets don't have replicas, so only resource and registry limits apply.
func (c *DaemonsetConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	if !c.explicitResources.cpuRequest {
		c.Resources.CPURequest = applyDefaultResource(c.Resources.CPURequest, p.DefaultCPURequest())
	}
	if !c.explicitResources.memoryRequest {
		c.Resources.MemoryRequest = applyDefaultResource(c.Resources.MemoryRequest, p.DefaultMemoryRequest())
	}
	if !c.explicitResources.cpuLimit {
		c.Resources.CPULimit = applyDefaultResource(c.Resources.CPULimit, p.DefaultCPULimit())
	}
	if !c.explicitResources.memoryLimit {
		c.Resources.MemoryLimit = applyDefaultResource(c.Resources.MemoryLimit, p.DefaultMemoryLimit())
	}

	if err := enforceMaxResource(c.Resources.CPURequest, p.MaxCPU(), "cpu request"); err != nil {
		return err
	}
	if err := enforceMaxResource(c.Resources.CPULimit, p.MaxCPU(), "cpu limit"); err != nil {
		return err
	}
	if err := enforceMaxResource(c.Resources.MemoryRequest, p.MaxMemory(), "memory request"); err != nil {
		return err
	}
	if err := enforceMaxResource(c.Resources.MemoryLimit, p.MaxMemory(), "memory limit"); err != nil {
		return err
	}
	if err := enforceAllowedRegistries(c.Image, p.AllowedRegistries()); err != nil {
		return err
	}
	for _, pvc := range c.PVCs {
		if err := enforceMaxStorageSize(pvc.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// Generate creates a Kubernetes DaemonSet, optional Service, and ServiceAccount.
// A Service is generated when Port > 0.
func (c *DaemonsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
	ds, err := c.createDaemonSet(app)
	if err != nil {
		return nil, err
	}

	dsObj := client.Object(ds)
	objects := []*client.Object{&dsObj}

	if c.Port > 0 {
		svc := c.createService(app)
		svcObj := client.Object(svc)
		objects = append(objects, &svcObj)
	}

	sa := createServiceAccount(app.Name, app.Namespace, labels)
	saObj := client.Object(sa)
	objects = append(objects, &saObj)

	for _, pvc := range c.PVCs {
		p, err := BuildPVC(pvc, app.Namespace, labels)
		if err != nil {
			return nil, err
		}
		pObj := client.Object(p)
		objects = append(objects, &pObj)
	}
	return objects, nil
}

func (c *DaemonsetConfig) createService(app *stack.Application) *corev1.Service {
	labels := map[string]string{"app": app.Name}
	svc := kubernetes.CreateService(app.Name, app.Namespace)
	svc.Labels = labels
	svc.Annotations = nil
	_ = kubernetes.SetServiceType(svc, corev1.ServiceTypeClusterIP)
	_ = kubernetes.SetServiceSelector(svc, map[string]string{"app": app.Name})
	_ = kubernetes.AddServicePort(svc, corev1.ServicePort{
		Name:       "tcp",
		Port:       c.Port,
		TargetPort: intstr.FromInt32(c.Port),
		Protocol:   corev1.ProtocolTCP,
	})
	return svc
}

func (c *DaemonsetConfig) createDaemonSet(app *stack.Application) (*appsv1.DaemonSet, error) {
	labels := map[string]string{"app": app.Name}

	container := kubernetes.CreateContainer(app.Name, c.Image, c.Command, c.Args)
	rr, err := buildResourceRequirements(c.Resources)
	if err != nil {
		return nil, errors.Wrap(err, "resource requirements")
	}
	_ = kubernetes.SetContainerResources(container, rr)
	for _, env := range buildEnvVars(c.Env) {
		_ = kubernetes.AddContainerEnv(container, env)
	}
	applyProbes(container, c.Probes)
	if c.Port > 0 {
		_ = kubernetes.AddContainerPort(container, corev1.ContainerPort{
			Name:          "tcp",
			ContainerPort: c.Port,
			Protocol:      corev1.ProtocolTCP,
		})
	}
	for _, m := range c.VolumeMounts {
		_ = kubernetes.AddContainerVolumeMount(container, m)
	}

	ds := kubernetes.CreateDaemonSet(app.Name, app.Namespace)
	ds.Labels = labels
	ds.Annotations = nil
	ds.Spec.Template.Labels = labels
	_ = kubernetes.SetDaemonSetServiceAccountName(ds, app.Name)
	// Init containers added before the main container so declaration order is
	// preserved in spec.template.spec.initContainers.
	for _, ic := range c.InitContainers {
		initContainer, err := buildInitContainer(ic)
		if err != nil {
			return nil, err
		}
		_ = kubernetes.AddDaemonSetInitContainer(ds, initContainer)
	}
	_ = kubernetes.AddDaemonSetContainer(ds, container)
	for i := range c.Tolerations {
		_ = kubernetes.AddDaemonSetToleration(ds, &c.Tolerations[i])
	}
	for i := range c.Volumes {
		_ = kubernetes.AddDaemonSetVolume(ds, &c.Volumes[i])
	}

	return ds, nil
}
