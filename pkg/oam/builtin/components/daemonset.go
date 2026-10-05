package components

import (
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		"ports":           schemaMainContainerPorts(),
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
		"initContainers":  schemaInitContainers(),
		"sidecars":        schemaSidecars(),
		// The other two raw scheduling shapes scheduling.go projects, beside
		// `tolerations` above.
		"affinity":                  schemaRawAffinity(),
		"topologySpreadConstraints": schemaTopologySpreadConstraints(),
	}
	maps.Copy(m, schemaContainerFields())
	maps.Copy(m, schemaPodSpec(false, false))
	maps.Copy(m, schemaDaemonSetSpec())
	return m
}

// ToApplicationConfig converts an OAM daemonset component to a DaemonsetConfig.
func (h *DaemonsetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &DaemonsetConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Namespace:  namespace,
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
	if resources, present, err := parseObjectField(props, "resources", "resources"); err != nil {
		return nil, err
	} else if present {
		r, err := parseResources(resources)
		if err != nil {
			return nil, errors.Wrap(err, "invalid resources configuration")
		}
		config.Resources = r
	}
	command, err := parseCommand(props)
	if err != nil {
		return nil, err
	}
	config.Command = command
	args, err := parseArgs(props)
	if err != nil {
		return nil, err
	}
	config.Args = args
	ports, err := parseContainerPorts(props)
	if err != nil {
		return nil, err
	}
	config.Ports = ports
	probes, lifecycle, err := parseMainContainerHandlers(props, ports)
	if err != nil {
		return nil, err
	}
	config.Probes = probes
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
	if config.ContainerFields, err = parseContainerFields(props, false); err != nil {
		return nil, err
	}

	tolerations, err := parseTolerations(props)
	if err != nil {
		return nil, err
	}
	config.Tolerations = tolerations
	affinity, err := parseRawAffinity(props)
	if err != nil {
		return nil, err
	}
	config.Affinity = affinity
	tscs, err := parseTopologySpreadConstraints(props)
	if err != nil {
		return nil, err
	}
	config.TopologySpreadConstraints = tscs
	parsed, err := parsePodVolumes(props)
	if err != nil {
		return nil, err
	}
	config.Volumes = parsed.Volumes
	config.VolumeMounts = parsed.Mounts
	config.VolumeDevices = parsed.Devices
	config.PVCs = parsed.PVCs

	initContainers, err := parseInitContainers(props)
	if err != nil {
		return nil, err
	}
	config.InitContainers = initContainers
	sidecars, err := parseSidecars(props)
	if err != nil {
		return nil, err
	}
	config.Sidecars = sidecars
	if err := checkExtraContainerVolumeModes(declaredVolumeModes(parsed, nil), initContainers, sidecars); err != nil {
		return nil, err
	}
	if err := checkPodPortNames(config.Ports, sidecars); err != nil {
		return nil, err
	}
	if err := checkFileKeyRefVolumes(parsed.Volumes, env, initContainers, sidecars); err != nil {
		return nil, err
	}

	podSpec, err := parsePodSpec(props, false)
	if err != nil {
		return nil, err
	}
	config.PodSpec = podSpec

	dsSpec, err := parseDaemonSetSpec(props)
	if err != nil {
		return nil, err
	}
	config.DaemonSetSpec = dsSpec

	return config, nil
}

// DaemonsetConfig implements stack.ApplicationConfig for daemonset components.
type DaemonsetConfig struct {
	Name string
	// ObjectName names the DaemonSet (oam.Component.ObjectName); its labels,
	// selector and main container keep Name. Empty for the application's name.
	ObjectName      string
	Namespace       string
	Image           string
	Ports           []corev1.ContainerPort // the main container's ports; no Service
	Env             []corev1.EnvVar
	EnvFrom         []corev1.EnvFromSource
	Resources       ResourceRequirements
	Command         []string
	Args            []string
	Probes          ProbeConfig
	Lifecycle       *corev1.Lifecycle
	SecurityContext *corev1.SecurityContext
	WorkingDir      string
	// ContainerFields are the main container's fields every container of the
	// pod accepts (see parseContainerFields).
	ContainerFields ContainerFields
	Tolerations     []corev1.Toleration
	Volumes         []corev1.Volume
	VolumeMounts    []corev1.VolumeMount
	VolumeDevices   []corev1.VolumeDevice
	InitContainers  []InitContainerConfig
	PVCs            []PVCConfig
	// Sidecars run beside the main container on every node the DaemonSet
	// schedules to (see parseSidecars).
	Sidecars []SidecarContainerConfig
	// Affinity and TopologySpreadConstraints are, with Tolerations above, the
	// raw corev1 scheduling shapes (see scheduling.go), carried as the API
	// types because nothing is inferred from them.
	Affinity                  *corev1.Affinity
	TopologySpreadConstraints []corev1.TopologySpreadConstraint
	// PodSpec holds the shared pod-level properties (see parsePodSpec).
	PodSpec PodSpecConfig
	// DaemonSetSpec holds the DaemonSetSpec-level properties that are neither
	// the pod template nor the builder-managed selector (see
	// parseDaemonSetSpec).
	DaemonSetSpec DaemonSetSpecConfig
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, or "" when the pods run as no named account.
func (c *DaemonsetConfig) ServiceAccountName() (string, bool) {
	return c.PodSpec.ServiceAccountName, true
}

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
	for i, sc := range c.Sidecars {
		if err := enforceExtraContainer("sidecars", i, sc.Name, sc.Image,
			sc.Resources, sc.SecurityContext, p); err != nil {
			return err
		}
	}
	return nil
}

// Generate creates a Kubernetes DaemonSet, and nothing beside it: no
// ServiceAccount and no claim (go-kure/launcher#702). It emits no Service
// either: an authored `service` component exposes the pods.
func (c *DaemonsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	ds, err := c.createDaemonSet(app)
	if err != nil {
		return nil, err
	}
	dsObj := client.Object(ds)
	return []*client.Object{&dsObj}, nil
}

func (c *DaemonsetConfig) createDaemonSet(app *stack.Application) (*appsv1.DaemonSet, error) {
	container, err := buildMainContainer(app.Name, mainContainerInput{
		Image:           c.Image,
		Command:         c.Command,
		Args:            c.Args,
		Resources:       c.Resources,
		Ports:           c.Ports,
		Env:             c.Env,
		EnvFrom:         c.EnvFrom,
		Probes:          c.Probes,
		WorkingDir:      c.WorkingDir,
		Lifecycle:       c.Lifecycle,
		SecurityContext: c.SecurityContext,
		VolumeMounts:    c.VolumeMounts,
		VolumeDevices:   c.VolumeDevices,
		Fields:          c.ContainerFields,
	})
	if err != nil {
		return nil, err
	}

	ds := kubernetes.CreateDaemonSet(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	ds.Labels = appLabels(app.Name)
	ds.Annotations = nil
	// kure's constructor no longer injects spec.selector (removed default,
	// go-kure/kure builder-contract-release-1.md) — required by the API
	// server with no server-side default, so it must match the template
	// labels explicitly.
	ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: appLabels(app.Name)}
	ds.Spec.Template.Labels = appLabels(app.Name)

	podSpec, err := buildPodSpec(podSpecInput{
		Config:         c.PodSpec,
		MainContainer:  container,
		InitContainers: c.InitContainers,
		Volumes:        c.Volumes,
		Tolerations:    c.Tolerations,

		Sidecars:                  c.Sidecars,
		Affinity:                  c.Affinity,
		TopologySpreadConstraints: c.TopologySpreadConstraints,
	})
	if err != nil {
		return nil, err
	}
	ds.Spec.Template.Spec = podSpec
	c.DaemonSetSpec.apply(ds)

	return ds, nil
}
