package components

import (
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// StatefulsetHandler handles OAM statefulset components.
type StatefulsetHandler struct{}

// CanHandle returns true for statefulset component type.
func (h *StatefulsetHandler) CanHandle(componentType string) bool {
	return componentType == "statefulset"
}

// PropertySchema declares the statefulset component's user-facing properties.
func (h *StatefulsetHandler) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":                {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"replicas":             {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of StatefulSet pod replicas."},
		"port":                 {Type: oam.PropertyTypeInteger, Description: "Container port to expose via the headless Service."},
		"serviceName":          {Type: oam.PropertyTypeString, Description: "Name of the headless Service (defaults to the component name)."},
		"env":                  schemaEnv(false),
		"envFrom":              schemaEnvFrom(false),
		"resources":            schemaResources(false),
		"command":              schemaStringArray(),
		"args":                 schemaStringArray(),
		"probes":               schemaProbes(false),
		"lifecycle":            schemaLifecycle(false),
		"securityContext":      schemaSecurityContext(false),
		"workingDir":           schemaWorkingDir(false),
		"volumeClaimTemplates": schemaVolumeClaimTemplates(),
		"volumes":              schemaVolumes(),
		"initContainers":       schemaContainers(),
		"sidecars":             schemaContainers(),
		"affinity":             schemaAffinity(),
	}
	maps.Copy(m, schemaPodSpec(false, false))
	return m
}

// ToApplicationConfig converts an OAM statefulset component to a StatefulsetConfig.
func (h *StatefulsetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &StatefulsetConfig{
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

	config.Replicas = parseReplicas(props, 1)
	config.explicitReplicas = hasExplicitReplicas(props)

	if p, ok := toInt32(props["port"]); ok {
		config.Port = p
	}

	if sn, ok := props["serviceName"].(string); ok && sn != "" {
		config.ServiceName = sn
	} else {
		config.ServiceName = component.Name
	}

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

	// namedPortsAllowed mirrors createContainer's own `c.Port > 0` guard: the
	// main container only gets a Name: "tcp" ContainerPort when a port was
	// actually configured, so a probe/lifecycle port resolves only in that
	// case, and only when it names that same "tcp" port.
	probes, err := parseProbes(props, config.Port > 0, "tcp")
	if err != nil {
		return nil, errors.Wrap(err, "invalid probe configuration")
	}
	config.Probes = probes
	lifecycle, err := parseLifecycle(props, config.Port > 0, "tcp")
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

	vcts, err := parseVolumeClaimTemplates(props)
	if err != nil {
		return nil, err
	}
	config.VolumeClaimTemplates = vcts

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

	affinity, err := parseAffinity(props)
	if err != nil {
		return nil, err
	}
	config.Affinity = affinity

	sidecars, err := parseSidecars(props)
	if err != nil {
		return nil, err
	}
	config.Sidecars = sidecars

	podSpec, err := parsePodSpec(props, false)
	if err != nil {
		return nil, err
	}
	config.PodSpec = podSpec

	return config, nil
}

// StatefulsetConfig implements stack.ApplicationConfig for statefulset components.
type StatefulsetConfig struct {
	Name                 string
	Namespace            string
	Image                string
	Replicas             int32
	Port                 int32
	ServiceName          string
	Env                  []corev1.EnvVar
	EnvFrom              []corev1.EnvFromSource
	Resources            ResourceRequirements
	Command              []string
	Args                 []string
	Probes               ProbeConfig
	Lifecycle            *corev1.Lifecycle
	SecurityContext      *corev1.SecurityContext
	WorkingDir           string
	VolumeClaimTemplates []VolumeClaimTemplate
	Volumes              []corev1.Volume
	VolumeMounts         []corev1.VolumeMount
	PVCs                 []PVCConfig
	InitContainers       []InitContainerConfig
	Sidecars             []SidecarContainerConfig
	Affinity             AffinityConfig
	// PodSpec holds the shared pod-level properties (see parsePodSpec).
	PodSpec          PodSpecConfig
	explicitReplicas bool
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, else the per-component ServiceAccount named after the
// component.
func (c *StatefulsetConfig) ServiceAccountName() string {
	return effectiveServiceAccountName(c.PodSpec, c.Name)
}

// ApplyPolicy applies defaults then enforces limits from the policy.
func (c *StatefulsetConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	c.Replicas = applyDefaultReplicas(c.Replicas, c.explicitReplicas, p.DefaultReplicas())
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

	if err := enforceMaxReplicas(c.Replicas, p.MaxReplicas()); err != nil {
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
	for _, pvc := range c.PVCs {
		if err := enforceMaxStorageSize(pvc.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}
	for _, vct := range c.VolumeClaimTemplates {
		if err := enforceMaxStorageSize(vct.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// ServicePort returns the port exposed by the component's headless Service, or 0 if no port is configured.
func (c *StatefulsetConfig) ServicePort() int32 { return c.Port }

// BackendServiceName returns the name of the Kubernetes Service the statefulset exposes.
func (c *StatefulsetConfig) BackendServiceName() string { return c.ServiceName }

// Generate creates Kubernetes StatefulSet, headless Service, ServiceAccount, and any standalone PVCs.
// The ServiceAccount is omitted when serviceAccountName was authored.
func (c *StatefulsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
	var err error
	c.PVCs, err = qualifyPVCNames(c.Volumes, c.PVCs, app.Name)
	if err != nil {
		return nil, err
	}

	sts, err := c.createStatefulSet(app)
	if err != nil {
		return nil, err
	}
	svc := c.createHeadlessService(app)

	stsObj := client.Object(sts)
	svcObj := client.Object(svc)

	objects := []*client.Object{&stsObj, &svcObj}
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

func (c *StatefulsetConfig) createStatefulSet(app *stack.Application) (*appsv1.StatefulSet, error) {
	labels := map[string]string{"app": app.Name}

	// Claim-template mounts precede the authored volume mounts, as before.
	mounts := make([]corev1.VolumeMount, 0, len(c.VolumeClaimTemplates)+len(c.VolumeMounts))
	for _, vct := range c.VolumeClaimTemplates {
		mounts = append(mounts, corev1.VolumeMount{Name: vct.Name, MountPath: vct.MountPath})
	}
	mounts = append(mounts, c.VolumeMounts...)
	var ports []corev1.ContainerPort
	if c.Port > 0 {
		ports = []corev1.ContainerPort{{Name: "tcp", ContainerPort: c.Port, Protocol: corev1.ProtocolTCP}}
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
		VolumeMounts:    mounts,
	})

	sts := kubernetes.CreateStatefulSet(app.Name, app.Namespace)
	sts.Labels = labels
	sts.Annotations = nil
	sts.Spec.Template.Labels = labels
	kubernetes.SetStatefulSetReplicas(sts, c.Replicas)
	kubernetes.SetStatefulSetServiceName(sts, c.ServiceName)

	podSpec, err := buildPodSpec(podSpecInput{
		Config:                    c.PodSpec,
		DefaultServiceAccountName: app.Name,
		MainContainer:             container,
		InitContainers:            c.InitContainers,
		Sidecars:                  c.Sidecars,
		Volumes:                   c.Volumes,
		Affinity:                  buildAffinity(c.Affinity, labels),
	})
	if err != nil {
		return nil, err
	}
	sts.Spec.Template.Spec = podSpec

	for _, vct := range c.VolumeClaimTemplates {
		accessModes := make([]corev1.PersistentVolumeAccessMode, 0, len(vct.AccessModes))
		for _, mode := range vct.AccessModes {
			accessModes = append(accessModes, corev1.PersistentVolumeAccessMode(mode))
		}
		if len(accessModes) == 0 {
			accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		}
		pvc := kubernetes.CreateVolumeClaimTemplate(vct.Name, kubernetes.VolumeClaimTemplateOptions{
			StorageClassName: vct.StorageClass,
			AccessModes:      accessModes,
			StorageRequest:   resource.MustParse(vct.Size),
		})
		kubernetes.AddStatefulSetVolumeClaimTemplate(sts, pvc)
	}

	return sts, nil
}

func (c *StatefulsetConfig) createHeadlessService(app *stack.Application) *corev1.Service {
	labels := map[string]string{"app": app.Name}

	svc := kubernetes.CreateService(c.ServiceName, app.Namespace)
	svc.Labels = labels
	svc.Annotations = nil
	kubernetes.SetServiceClusterIP(svc, "None")
	kubernetes.SetServiceSelector(svc, map[string]string{"app": app.Name})
	if c.Port > 0 {
		kubernetes.AddServicePort(svc, corev1.ServicePort{
			Name:       "tcp",
			Port:       c.Port,
			TargetPort: intstr.FromInt32(c.Port),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return svc
}
