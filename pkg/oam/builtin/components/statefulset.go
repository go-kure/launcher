package components

import (
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
	Env                  []EnvVar
	Resources            ResourceRequirements
	Command              []string
	Args                 []string
	Probes               ProbeConfig
	VolumeClaimTemplates []VolumeClaimTemplate
	Volumes              []corev1.Volume
	VolumeMounts         []corev1.VolumeMount
	PVCs                 []PVCConfig
	InitContainers       []InitContainerConfig
	Sidecars             []SidecarContainerConfig
	Affinity             AffinityConfig
	explicitReplicas     bool
	explicitResources    explicitResourceFlags
}

// ApplyPolicy applies defaults then enforces limits from the policy.
func (c *StatefulsetConfig) ApplyPolicy(p oam.Policy) error {
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
	for _, vct := range c.VolumeClaimTemplates {
		if err := enforceMaxStorageSize(vct.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// ServicePort returns the port exposed by the component's headless Service, or 0 if no port is configured.
func (c *StatefulsetConfig) ServicePort() int32 { return c.Port }

// Generate creates Kubernetes StatefulSet, headless Service, ServiceAccount, and any standalone PVCs.
func (c *StatefulsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}

	sts, err := c.createStatefulSet(app)
	if err != nil {
		return nil, err
	}
	svc := c.createHeadlessService(app)
	sa := createServiceAccount(app.Name, app.Namespace, labels)

	stsObj := client.Object(sts)
	svcObj := client.Object(svc)
	saObj := client.Object(sa)

	objects := []*client.Object{&stsObj, &svcObj, &saObj}
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

	container := kubernetes.CreateContainer(app.Name, c.Image, c.Command, c.Args)
	rr, err := buildResourceRequirements(c.Resources)
	if err != nil {
		return nil, errors.Wrap(err, "resource requirements")
	}
	_ = kubernetes.SetContainerResources(container, rr)
	if c.Port > 0 {
		_ = kubernetes.AddContainerPort(container, corev1.ContainerPort{
			Name:          "tcp",
			ContainerPort: c.Port,
			Protocol:      corev1.ProtocolTCP,
		})
	}
	for _, env := range buildEnvVars(c.Env) {
		_ = kubernetes.AddContainerEnv(container, env)
	}
	for _, vct := range c.VolumeClaimTemplates {
		_ = kubernetes.AddContainerVolumeMount(container, corev1.VolumeMount{
			Name:      vct.Name,
			MountPath: vct.MountPath,
		})
	}
	for _, m := range c.VolumeMounts {
		_ = kubernetes.AddContainerVolumeMount(container, m)
	}
	applyProbes(container, c.Probes)

	sts := kubernetes.CreateStatefulSet(app.Name, app.Namespace)
	sts.Labels = labels
	sts.Annotations = nil
	sts.Spec.Template.Labels = labels
	_ = kubernetes.SetStatefulSetReplicas(sts, c.Replicas)
	_ = kubernetes.SetStatefulSetServiceName(sts, c.ServiceName)
	_ = kubernetes.SetStatefulSetServiceAccountName(sts, app.Name)

	for _, ic := range c.InitContainers {
		initContainer, err := buildInitContainer(ic)
		if err != nil {
			return nil, err
		}
		_ = kubernetes.AddStatefulSetInitContainer(sts, initContainer)
	}
	_ = kubernetes.AddStatefulSetContainer(sts, container)
	for _, sc := range c.Sidecars {
		sidecarContainer, err := buildSidecarContainer(sc)
		if err != nil {
			return nil, err
		}
		_ = kubernetes.AddStatefulSetContainer(sts, sidecarContainer)
	}

	if aff := buildAffinity(c.Affinity, map[string]string{"app": app.Name}); aff != nil {
		_ = kubernetes.SetStatefulSetAffinity(sts, aff)
	}

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
		_ = kubernetes.AddStatefulSetVolumeClaimTemplate(sts, pvc)
	}
	for i := range c.Volumes {
		_ = kubernetes.AddStatefulSetVolume(sts, &c.Volumes[i])
	}

	return sts, nil
}

func (c *StatefulsetConfig) createHeadlessService(app *stack.Application) *corev1.Service {
	labels := map[string]string{"app": app.Name}

	svc := kubernetes.CreateService(c.ServiceName, app.Namespace)
	svc.Labels = labels
	svc.Annotations = nil
	_ = kubernetes.SetServiceClusterIP(svc, "None")
	_ = kubernetes.SetServiceSelector(svc, map[string]string{"app": app.Name})
	if c.Port > 0 {
		_ = kubernetes.AddServicePort(svc, corev1.ServicePort{
			Name:       "tcp",
			Port:       c.Port,
			TargetPort: intstr.FromInt32(c.Port),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return svc
}
