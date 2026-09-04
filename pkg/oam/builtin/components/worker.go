package components

import (
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// WorkerHandler handles OAM worker components.
type WorkerHandler struct{}

// CanHandle returns true for worker component type.
func (h *WorkerHandler) CanHandle(componentType string) bool {
	return componentType == "worker"
}

// PropertySchema declares the worker component's user-facing properties. Like
// webservice minus `port` (worker emits no Service).
func (h *WorkerHandler) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"replicas":        {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of Deployment pod replicas."},
		"topologySpread":  {Type: oam.PropertyTypeBoolean, Default: true, Description: "Whether default topology spread constraints are applied across nodes."},
		"env":             schemaEnv(false),
		"envFrom":         schemaEnvFrom(false),
		"resources":       schemaResources(false),
		"command":         schemaStringArray(),
		"args":            schemaStringArray(),
		"probes":          schemaProbes(false),
		"lifecycle":       schemaLifecycle(false),
		"securityContext": schemaSecurityContext(false),
		"workingDir":      schemaWorkingDir(false),
		"volumes":         schemaVolumes(),
		"initContainers":  schemaContainers(),
		"sidecars":        schemaContainers(),
		"affinity":        schemaAffinity(),
	}
	maps.Copy(m, schemaPodSpec(false, false))
	return m
}

// ToApplicationConfig converts an OAM worker component to a WorkerConfig.
func (h *WorkerHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &WorkerConfig{
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
	// namedPortsAllowed=false: worker exposes no port property at all, so its
	// main container never declares a ContainerPort for the kubelet to
	// resolve a named probe/lifecycle port against.
	probes, err := parseProbes(props, false, "")
	if err != nil {
		return nil, errors.Wrap(err, "invalid probe configuration")
	}
	config.Probes = probes
	lifecycle, err := parseLifecycle(props, false, "")
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
	if ts, ok := props["topologySpread"].(bool); ok && !ts {
		config.TopologySpreadDisabled = true
	}
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

// WorkerConfig implements stack.ApplicationConfig for worker components.
type WorkerConfig struct {
	Name                   string
	Namespace              string
	Image                  string
	Replicas               int32
	Env                    []corev1.EnvVar
	EnvFrom                []corev1.EnvFromSource
	Resources              ResourceRequirements
	Command                []string
	Args                   []string
	Probes                 ProbeConfig
	Lifecycle              *corev1.Lifecycle
	SecurityContext        *corev1.SecurityContext
	WorkingDir             string
	Volumes                []corev1.Volume
	VolumeMounts           []corev1.VolumeMount
	PVCs                   []PVCConfig
	InitContainers         []InitContainerConfig
	Sidecars               []SidecarContainerConfig
	TopologySpreadDisabled bool
	Affinity               AffinityConfig
	// PodSpec holds the shared pod-level properties (see parsePodSpec).
	PodSpec          PodSpecConfig
	explicitReplicas bool
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, else the per-component ServiceAccount named after the
// component.
func (c *WorkerConfig) ServiceAccountName() string {
	return effectiveServiceAccountName(c.PodSpec, c.Name)
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// Defaults are applied first so that enforced checks run on effective post-default values.
func (c *WorkerConfig) ApplyPolicy(p oam.Policy) error {
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
	for _, pvc := range c.PVCs {
		if err := enforceMaxStorageSize(pvc.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// Generate creates a Kubernetes Deployment and ServiceAccount (no Service).
// The ServiceAccount is omitted when serviceAccountName was authored.
func (c *WorkerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	var err error
	c.PVCs, err = qualifyPVCNames(c.Volumes, c.PVCs, app.Name)
	if err != nil {
		return nil, err
	}
	deployment, err := c.createDeployment(app)
	if err != nil {
		return nil, err
	}

	depObj := client.Object(deployment)

	objects := []*client.Object{&depObj}
	if generatesServiceAccount(c.PodSpec) {
		saObj := client.Object(createServiceAccount(generationServiceAccountName(c, app.Name), app.Namespace, appLabels(app.Name)))
		objects = append(objects, &saObj)
	}
	for _, pvc := range c.PVCs {
		p, err := BuildPVC(pvc, app.Namespace, appLabels(app.Name))
		if err != nil {
			return nil, err
		}
		pObj := client.Object(p)
		objects = append(objects, &pObj)
	}

	return objects, nil
}

func (c *WorkerConfig) createDeployment(app *stack.Application) (*appsv1.Deployment, error) {
	// No Ports: worker exposes no port property (see parseProbes' namedPortsAllowed=false above).
	container := buildMainContainer(app.Name, mainContainerInput{
		Image:           c.Image,
		Command:         c.Command,
		Args:            c.Args,
		Resources:       c.Resources,
		Env:             c.Env,
		EnvFrom:         c.EnvFrom,
		Probes:          c.Probes,
		WorkingDir:      c.WorkingDir,
		Lifecycle:       c.Lifecycle,
		SecurityContext: c.SecurityContext,
		VolumeMounts:    c.VolumeMounts,
	})

	dep := kubernetes.CreateDeployment(app.Name, app.Namespace)
	dep.Labels = appLabels(app.Name)
	dep.Annotations = nil
	dep.Spec.Template.Labels = appLabels(app.Name)
	kubernetes.SetDeploymentReplicas(dep, c.Replicas)
	if hasNonRWXPVC(c.PVCs) {
		if c.Replicas > 1 {
			return nil, errors.Errorf("deployment %q: non-RWX PVC requires replicas=1, got %d", app.Name, c.Replicas)
		}
		kubernetes.SetDeploymentStrategy(dep, appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType})
	}

	var tscs []corev1.TopologySpreadConstraint
	if !c.TopologySpreadDisabled {
		tscs = buildTopologySpreadConstraints(c.Replicas, appLabels(app.Name))
	}
	podSpec, err := buildPodSpec(podSpecInput{
		Config:                    c.PodSpec,
		DefaultServiceAccountName: generationServiceAccountName(c, app.Name),
		MainContainer:             container,
		InitContainers:            c.InitContainers,
		Sidecars:                  c.Sidecars,
		Volumes:                   c.Volumes,
		TopologySpreadConstraints: tscs,
		Affinity:                  buildAffinity(c.Affinity, appLabels(app.Name)),
	})
	if err != nil {
		return nil, err
	}
	dep.Spec.Template.Spec = podSpec

	return dep, nil
}
