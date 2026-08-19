package components

import (
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
	return map[string]oam.PropertySchema{
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
	explicitReplicas       bool
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

// Generate creates a Kubernetes Deployment and ServiceAccount (no Service).
func (c *WorkerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
	c.PVCs = qualifyPVCNames(c.Volumes, c.PVCs, app.Name)
	deployment, err := c.createDeployment(app)
	if err != nil {
		return nil, err
	}
	sa := createServiceAccount(app.Name, app.Namespace, labels)

	depObj := client.Object(deployment)
	saObj := client.Object(sa)

	objects := []*client.Object{&depObj, &saObj}
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

func (c *WorkerConfig) createDeployment(app *stack.Application) (*appsv1.Deployment, error) {
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

	dep := kubernetes.CreateDeployment(app.Name, app.Namespace)
	dep.Labels = labels
	dep.Annotations = nil
	dep.Spec.Template.Labels = labels
	kubernetes.SetDeploymentReplicas(dep, c.Replicas)
	if hasNonRWXPVC(c.PVCs) {
		if c.Replicas > 1 {
			return nil, errors.Errorf("deployment %q: non-RWX PVC requires replicas=1, got %d", app.Name, c.Replicas)
		}
		kubernetes.SetDeploymentStrategy(dep, appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType})
	}
	kubernetes.SetDeploymentServiceAccountName(dep, app.Name)
	if !c.TopologySpreadDisabled {
		for _, tsc := range buildTopologySpreadConstraints(c.Replicas, map[string]string{"app": app.Name}) {
			if err := kubernetes.AddDeploymentTopologySpreadConstraints(dep, &tsc); err != nil {
				return nil, errors.Wrapf(err, "add topology spread constraint")
			}
		}
	}
	for _, ic := range c.InitContainers {
		initContainer, err := buildInitContainer(ic)
		if err != nil {
			return nil, err
		}
		if err := kubernetes.AddDeploymentInitContainer(dep, initContainer); err != nil {
			return nil, errors.Wrapf(err, "add init container %q", ic.Name)
		}
	}
	if err := kubernetes.AddDeploymentContainer(dep, container); err != nil {
		return nil, errors.Wrapf(err, "add container %q", c.Name)
	}
	for _, sc := range c.Sidecars {
		sidecarContainer, err := buildSidecarContainer(sc)
		if err != nil {
			return nil, err
		}
		if err := kubernetes.AddDeploymentContainer(dep, sidecarContainer); err != nil {
			return nil, errors.Wrapf(err, "add sidecar container %q", sc.Name)
		}
	}
	for i := range c.Volumes {
		if err := kubernetes.AddDeploymentVolume(dep, &c.Volumes[i]); err != nil {
			return nil, errors.Wrapf(err, "add volume %q", c.Volumes[i].Name)
		}
	}
	if aff := buildAffinity(c.Affinity, map[string]string{"app": app.Name}); aff != nil {
		kubernetes.SetDeploymentAffinity(dep, aff)
	}

	return dep, nil
}
