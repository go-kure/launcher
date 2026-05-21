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
	Env                    []EnvVar
	Resources              ResourceRequirements
	Command                []string
	Args                   []string
	Probes                 ProbeConfig
	Volumes                []corev1.Volume
	VolumeMounts           []corev1.VolumeMount
	PVCs                   []PVCConfig
	InitContainers         []InitContainerConfig
	Sidecars               []SidecarContainerConfig
	TopologySpreadDisabled bool
	Affinity               AffinityConfig
	explicitReplicas       bool
	explicitResources      explicitResourceFlags
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// Defaults are applied first so that enforced checks run on effective post-default values.
func (c *WorkerConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	c.Replicas = applyDefaultReplicas(c.Replicas, c.explicitReplicas, p.DefaultReplicas())
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

	if err := enforceMaxReplicas(c.Replicas, p.MaxReplicas()); err != nil {
		return err
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

// Generate creates a Kubernetes Deployment and ServiceAccount (no Service).
func (c *WorkerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
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
	rr, err := buildResourceRequirements(c.Resources)
	if err != nil {
		return nil, errors.Wrap(err, "resource requirements")
	}
	kubernetes.SetContainerResources(container, rr)
	for _, env := range buildEnvVars(c.Env) {
		kubernetes.AddContainerEnv(container, env)
	}
	applyProbes(container, c.Probes)
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
