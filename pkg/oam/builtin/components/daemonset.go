package components

import (
	"maps"

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
	m := map[string]oam.PropertySchema{
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
	maps.Copy(m, schemaPodSpec(false, false))
	return m
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

	podSpec, err := parsePodSpec(props, false)
	if err != nil {
		return nil, err
	}
	config.PodSpec = podSpec

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
	// PodSpec holds the shared pod-level properties (see parsePodSpec).
	PodSpec PodSpecConfig
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, else the per-component ServiceAccount named after the
// component.
func (c *DaemonsetConfig) ServiceAccountName() string {
	return effectiveServiceAccountName(c.PodSpec, c.Name)
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

	if err := enforceMaxResources(c.Resources, p.MaxCPU(), p.MaxMemory()); err != nil {
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
	if err := enforceHostNamespaces(c.PodSpec, p); err != nil {
		return err
	}
	if err := enforcePodResources(c.PodSpec, p.MaxCPU(), p.MaxMemory()); err != nil {
		return err
	}
	if err := enforcePodHostProcess(c.PodSpec, p.AllowPrivileged()); err != nil {
		return err
	}
	if err := enforceContainerCapabilities(c.SecurityContext, p.AllowedContainerCapabilities(), p.ForbiddenContainerCapabilities()); err != nil {
		return err
	}
	for i, ic := range c.InitContainers {
		if err := enforceExtraContainer("initContainers", i, ic.Name, ic.Image,
			ic.Resources, ic.SecurityContext, p); err != nil {
			return err
		}
	}
	for _, pvc := range c.PVCs {
		if err := enforceMaxStorageSize(pvc.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// Generate creates a Kubernetes DaemonSet, optional Service, and ServiceAccount.
// A Service is generated when Port > 0. The ServiceAccount is omitted when
// serviceAccountName was authored.
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

	if generatesServiceAccount(c.PodSpec) {
		saObj := client.Object(createServiceAccount(app.Name, app.Namespace, labels))
		objects = append(objects, &saObj)
	}

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

	var ports []corev1.ContainerPort
	if c.Port > 0 {
		ports = []corev1.ContainerPort{{Name: "http", ContainerPort: c.Port, Protocol: corev1.ProtocolTCP}}
	}
	container := buildMainContainer(app.Name, mainContainerInput{
		Image:           c.Image,
		Command:         c.Command,
		Args:            c.Args,
		Resources:       c.Resources,
		Ports:           ports,
		Env:             c.Env,
		EnvFrom:         c.EnvFrom,
		Probes:          c.Probes,
		WorkingDir:      c.WorkingDir,
		Lifecycle:       c.Lifecycle,
		SecurityContext: c.SecurityContext,
		VolumeMounts:    c.VolumeMounts,
	})

	ds := kubernetes.CreateDaemonSet(app.Name, app.Namespace)
	ds.Labels = labels
	ds.Annotations = nil
	ds.Spec.Template.Labels = labels

	podSpec, err := buildPodSpec(podSpecInput{
		Config:                    c.PodSpec,
		DefaultServiceAccountName: app.Name,
		MainContainer:             container,
		InitContainers:            c.InitContainers,
		Volumes:                   c.Volumes,
		Tolerations:               c.Tolerations,
	})
	if err != nil {
		return nil, err
	}
	ds.Spec.Template.Spec = podSpec

	return ds, nil
}
