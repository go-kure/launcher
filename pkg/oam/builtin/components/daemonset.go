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

// PropertySchema declares the daemonset component's user-facing properties.
func (h *DaemonsetHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"port":            {Type: oam.PropertyTypeInteger, Description: "Container port to expose; when set, a ClusterIP Service is generated."},
		"env":             schemaEnv(false),
		"envFrom":         schemaEnvFrom(false),
		"resources":       schemaResources(false),
		"command":         schemaStringArray(),
		"args":            schemaStringArray(),
		"probes":          schemaProbes(false),
		"lifecycle":       schemaLifecycle(false),
		"securityContext": schemaSecurityContext(false),
		"workingDir":      schemaWorkingDir(false),
		"tolerations":     schemaTolerations(),
		"volumes":         schemaVolumes(),
		"initContainers":  schemaContainers(),
	}
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
	envFrom, err := parseEnvFrom(props)
	if err != nil {
		return nil, err
	}
	config.EnvFrom = envFrom
	if resources, ok := props["resources"].(map[string]any); ok {
		r, err := parseResources(resources)
		if err != nil {
			return nil, errors.Wrap(err, "invalid resources configuration")
		}
		config.Resources = r
	}
	config.Command = parseCommand(props)
	config.Args = parseArgs(props)
	if port, ok := toInt32(props["port"]); ok {
		config.Port = port
	}
	// namedPortsAllowed mirrors createContainer's own `c.Port > 0` guard below:
	// the main container only gets a Name: "http" ContainerPort when a port
	// was actually configured, so a probe/lifecycle port resolves only in
	// that case, and only when it names that same "http" port.
	probes, err := parseProbes(props, config.Port > 0, "http")
	if err != nil {
		return nil, errors.Wrap(err, "invalid probe configuration")
	}
	config.Probes = probes
	lifecycle, err := parseLifecycle(props, config.Port > 0, "http")
	if err != nil {
		return nil, errors.Wrap(err, "invalid lifecycle configuration")
	}
	config.Lifecycle = lifecycle
	securityContext, err := parseSecurityContext(props)
	if err != nil {
		return nil, errors.Wrap(err, "invalid securityContext configuration")
	}
	config.SecurityContext = securityContext
	if workingDir, present, err := parseStringField(props, "workingDir", "workingDir"); err != nil {
		return nil, err
	} else if present {
		config.WorkingDir = workingDir
	}

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

	return config, nil
}

// DaemonsetConfig implements stack.ApplicationConfig for daemonset components.
type DaemonsetConfig struct {
	Name            string
	Namespace       string
	Image           string
	Port            int32 // when > 0, generates a ClusterIP Service exposing this port
	Env             []corev1.EnvVar
	EnvFrom         []corev1.EnvFromSource
	Resources       ResourceRequirements
	Command         []string
	Args            []string
	Probes          ProbeConfig
	Lifecycle       *corev1.Lifecycle
	SecurityContext *corev1.SecurityContext
	WorkingDir      string
	Tolerations     []corev1.Toleration
	Volumes         []corev1.Volume
	VolumeMounts    []corev1.VolumeMount
	InitContainers  []InitContainerConfig
	PVCs            []PVCConfig
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

	if err := applyDefaultQuantity(&c.Resources.Requests, corev1.ResourceCPU, p.DefaultCPURequest()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Resources.Requests, corev1.ResourceMemory, p.DefaultMemoryRequest()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Resources.Limits, corev1.ResourceCPU, p.DefaultCPULimit()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Resources.Limits, corev1.ResourceMemory, p.DefaultMemoryLimit()); err != nil {
		return err
	}

	if err := enforceMaxResource(quantityString(c.Resources.Requests, corev1.ResourceCPU), p.MaxCPU(), "cpu request"); err != nil {
		return err
	}
	if err := enforceMaxResource(quantityString(c.Resources.Limits, corev1.ResourceCPU), p.MaxCPU(), "cpu limit"); err != nil {
		return err
	}
	if err := enforceMaxResource(quantityString(c.Resources.Requests, corev1.ResourceMemory), p.MaxMemory(), "memory request"); err != nil {
		return err
	}
	if err := enforceMaxResource(quantityString(c.Resources.Limits, corev1.ResourceMemory), p.MaxMemory(), "memory limit"); err != nil {
		return err
	}
	if err := enforceAllowedRegistries(c.Image, p.AllowedRegistries()); err != nil {
		return err
	}
	if err := enforcePrivileged(c.SecurityContext, p.AllowPrivileged()); err != nil {
		return err
	}
	if err := enforceHostPathVolumes(c.Volumes, p.AllowHostPathVolumes()); err != nil {
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
	var err error
	c.PVCs, err = qualifyPVCNames(c.Volumes, c.PVCs, app.Name)
	if err != nil {
		return nil, err
	}
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
	kubernetes.SetServiceType(svc, corev1.ServiceTypeClusterIP)
	kubernetes.SetServiceSelector(svc, map[string]string{"app": app.Name})
	kubernetes.AddServicePort(svc, corev1.ServicePort{
		Name:       "http",
		Port:       c.Port,
		TargetPort: intstr.FromInt32(c.Port),
		Protocol:   corev1.ProtocolTCP,
	})
	return svc
}

func (c *DaemonsetConfig) createDaemonSet(app *stack.Application) (*appsv1.DaemonSet, error) {
	labels := map[string]string{"app": app.Name}

	container := kubernetes.CreateContainer(app.Name, c.Image, c.Command, c.Args)
	kubernetes.SetContainerResources(container, buildResourceRequirements(c.Resources))
	for _, env := range c.Env {
		kubernetes.AddContainerEnv(container, env)
	}
	for _, ef := range c.EnvFrom {
		kubernetes.AddContainerEnvFrom(container, ef)
	}
	applyProbes(container, c.Probes)
	if c.Port > 0 {
		kubernetes.AddContainerPort(container, corev1.ContainerPort{
			Name:          "http",
			ContainerPort: c.Port,
			Protocol:      corev1.ProtocolTCP,
		})
	}
	if c.WorkingDir != "" {
		kubernetes.SetContainerWorkingDir(container, c.WorkingDir)
	}
	if c.Lifecycle != nil {
		kubernetes.SetContainerLifecycle(container, c.Lifecycle)
	}
	if c.SecurityContext != nil {
		kubernetes.SetContainerSecurityContext(container, *c.SecurityContext)
	}
	for _, m := range c.VolumeMounts {
		kubernetes.AddContainerVolumeMount(container, m)
	}

	ds := kubernetes.CreateDaemonSet(app.Name, app.Namespace)
	ds.Labels = labels
	ds.Annotations = nil
	ds.Spec.Template.Labels = labels
	kubernetes.SetDaemonSetServiceAccountName(ds, app.Name)
	// Init containers added before the main container so declaration order is
	// preserved in spec.template.spec.initContainers.
	for _, ic := range c.InitContainers {
		initContainer, err := buildInitContainer(ic)
		if err != nil {
			return nil, err
		}
		if err := kubernetes.AddDaemonSetInitContainer(ds, initContainer); err != nil {
			return nil, errors.Wrapf(err, "add init container %q", ic.Name)
		}
	}
	if err := kubernetes.AddDaemonSetContainer(ds, container); err != nil {
		return nil, errors.Wrapf(err, "add container %q", c.Name)
	}
	for i := range c.Tolerations {
		if err := kubernetes.AddDaemonSetToleration(ds, &c.Tolerations[i]); err != nil {
			return nil, errors.Wrapf(err, "add toleration %d", i)
		}
	}
	for i := range c.Volumes {
		if err := kubernetes.AddDaemonSetVolume(ds, &c.Volumes[i]); err != nil {
			return nil, errors.Wrapf(err, "add volume %q", c.Volumes[i].Name)
		}
	}

	return ds, nil
}
