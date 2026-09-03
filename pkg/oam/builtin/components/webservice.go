package components

import (
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// WebserviceHandler handles OAM webservice components.
type WebserviceHandler struct{}

// CanHandle returns true for webservice component type.
func (h *WebserviceHandler) CanHandle(componentType string) bool {
	return componentType == "webservice"
}

// Endpoints implements oam.EndpointProvider: a webservice's in-cluster endpoint is its own pods
// (labelled app=<component name>) on the declared container/service port. This lets a downstream
// platform consumer synthesize generic app→app connections whose target is a webservice, the same
// way it does for a postgresql target. The webservice's single `port` property drives both the
// container port and the Service port (TargetPort == Port), so there is one endpoint per component.
func (h *WebserviceHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	port := int32(80)
	if p, ok := toInt32(component.Properties["port"]); ok {
		port = p
	}
	return []netpol.Endpoint{{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": component.Name}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(port)},
	}}, nil
}

// PropertySchema declares the webservice component's user-facing properties.
func (h *WebserviceHandler) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"port":            {Type: oam.PropertyTypeInteger, Default: 80, Description: "Container port exposed by the Deployment and its ClusterIP Service."},
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

// ToApplicationConfig converts an OAM webservice component to a WebserviceConfig.
func (h *WebserviceHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &WebserviceConfig{
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

	config.Port = 80
	if p, ok := toInt32(props["port"]); ok {
		config.Port = p
	}

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
	// namedPortsAllowed=true, matchName="http": webservice always attaches a
	// Name: "http" ContainerPort to the main container (createDeployment,
	// unconditional — config.Port defaults to 80), so a probe/lifecycle port
	// resolves only when it names that same "http" port.
	probes, err := parseProbes(props, true, "http")
	if err != nil {
		return nil, errors.Wrap(err, "invalid probe configuration")
	}
	config.Probes = probes
	lifecycle, err := parseLifecycle(props, true, "http")
	if err != nil {
		return nil, errors.Wrap(err, "invalid lifecycle configuration")
	}
	config.Lifecycle = lifecycle
	securityContext, err := parseSecurityContext(props)
	if err != nil {
		return nil, errors.Wrap(err, "invalid securityContext configuration")
	}
	config.SecurityContext = securityContext
	if wd, present, err := parseStringField(props, "workingDir", "workingDir"); err != nil {
		return nil, err
	} else if present {
		config.WorkingDir = wd
	}

	parsed, err := parseVolumes(props)
	if err != nil {
		return nil, err
	}
	config.Volumes = parsed.Volumes
	config.VolumeMounts = parsed.Mounts
	config.PVCs = parsed.PVCs

	// Init containers must be added before the main container so they
	// appear first in spec.template.spec.initContainers; kube preserves
	// declaration order on the pod spec and kustomize build output stays stable.
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

// WebserviceConfig implements stack.ApplicationConfig for webservice components.
type WebserviceConfig struct {
	Name                   string
	Namespace              string
	Image                  string
	Port                   int32
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
func (c *WebserviceConfig) ServiceAccountName() string {
	return effectiveServiceAccountName(c.PodSpec, c.Name)
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// Defaults are applied first so that enforced checks run on effective post-default values.
func (c *WebserviceConfig) ApplyPolicy(p oam.Policy) error {
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

// ServicePort returns the port exposed by the component's Service.
func (c *WebserviceConfig) ServicePort() int32 { return c.Port }

// Generate creates Kubernetes Deployment, Service, and ServiceAccount resources.
// The ServiceAccount is omitted when serviceAccountName was authored (the pod
// then runs as that pre-existing account).
func (c *WebserviceConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
	var err error
	c.PVCs, err = qualifyPVCNames(c.Volumes, c.PVCs, app.Name)
	if err != nil {
		return nil, err
	}
	deployment, err := c.createDeployment(app)
	if err != nil {
		return nil, err
	}
	service := c.createService(app)

	depObj := client.Object(deployment)
	svcObj := client.Object(service)

	objects := []*client.Object{&depObj, &svcObj}
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

func (c *WebserviceConfig) createDeployment(app *stack.Application) (*appsv1.Deployment, error) {
	labels := map[string]string{"app": app.Name}

	container := buildMainContainer(app.Name, mainContainerInput{
		Image:     c.Image,
		Command:   c.Command,
		Args:      c.Args,
		Resources: c.Resources,
		// Unconditional: config.Port defaults to 80, and parseProbes/parseLifecycle
		// were told a named "http" port always exists.
		Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: c.Port, Protocol: corev1.ProtocolTCP}},
		Env:             c.Env,
		EnvFrom:         c.EnvFrom,
		Probes:          c.Probes,
		WorkingDir:      c.WorkingDir,
		Lifecycle:       c.Lifecycle,
		SecurityContext: c.SecurityContext,
		VolumeMounts:    c.VolumeMounts,
	})

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

	var tscs []corev1.TopologySpreadConstraint
	if !c.TopologySpreadDisabled {
		tscs = buildTopologySpreadConstraints(c.Replicas, labels)
	}
	podSpec, err := buildPodSpec(podSpecInput{
		Config:                    c.PodSpec,
		DefaultServiceAccountName: app.Name,
		MainContainer:             container,
		InitContainers:            c.InitContainers,
		Sidecars:                  c.Sidecars,
		Volumes:                   c.Volumes,
		TopologySpreadConstraints: tscs,
		Affinity:                  buildAffinity(c.Affinity, labels),
	})
	if err != nil {
		return nil, err
	}
	dep.Spec.Template.Spec = podSpec

	return dep, nil
}

func (c *WebserviceConfig) createService(app *stack.Application) *corev1.Service {
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
