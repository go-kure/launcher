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

// DeploymentHandler handles OAM deployment components.
//
// This is the kind-named projection of appsv1.Deployment, alongside the
// role-named webservice (Deployment + Service) and worker (Deployment, no
// Service). What it adds over those two is the rest of DeploymentSpec —
// strategy, minReadySeconds, revisionHistoryLimit, paused and
// progressDeadlineSeconds (go-kure/launcher#343). What it deliberately leaves
// out is launcher's own opinions: there is no `port` (and so no Service), no
// default topology-spread constraint and no four-key `affinity` shorthand,
// none of which are DeploymentSpec fields. A workload wanting launcher to
// create its Service uses webservice.
//
// The routing traits (expose, ingress, httproute) are accepted on this kind but
// are not self-sufficient here, because no Service is emitted. `expose` lowers
// into `ingress` or `httproute`, and both resolve an implicit backend through
// the component's own service port — which this kind does not have, so an
// implicitly-backed route is rejected at build time with a "has no service
// port" error. On a deployment component they must name the target Service
// explicitly, with the trait's `serviceName` and `servicePort`, and that
// Service has to exist independently: authored as a `manifests` component, or
// belonging to some other component in the package. Nothing in this kind
// creates it.
type DeploymentHandler struct{}

// CanHandle returns true for deployment component type.
func (h *DeploymentHandler) CanHandle(componentType string) bool {
	return componentType == "deployment"
}

// PropertySchema declares the deployment component's user-facing properties:
// the shared container and pod-level surface, plus every DeploymentSpec-level
// field that is not builder-managed.
func (h *DeploymentHandler) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"replicas":        {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of Deployment pod replicas."},
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
	}
	maps.Copy(m, schemaPodSpec(false, false))
	maps.Copy(m, schemaDeploymentSpec())
	return m
}

// ToApplicationConfig converts an OAM deployment component to a DeploymentConfig.
func (h *DeploymentHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &DeploymentConfig{
		Name:      component.Name,
		Namespace: namespace,
	}

	// Null as omission, applied to this kind's whole top-level surface rather
	// than to the one field a review named: pkg/oam's property validator reads
	// an explicit null under an optional property as absent
	// (property_validate.go), while every typed helper answers "present?" with
	// a bare map lookup, so the nil reaches its type check and comes back as a
	// type error. `replicas: null`, `workingDir: null` and every other optional
	// property here would otherwise be refused by the parser after the
	// published schema accepted them.
	//
	// parseDeploymentSpec is deliberately given the AUTHORED map below, not
	// this copy: it refuses the keys that must not appear at all (`selector`,
	// `template`) before applying the same stripping itself, and that order is
	// what makes `selector: null` earn its explanatory refusal instead of
	// silently vanishing.
	props := withoutExplicitNulls(component.Properties)

	image, ok := props["image"].(string)
	if !ok {
		return nil, errors.New("required property 'image' missing or not a string")
	}
	if err := ValidateImageRef(image); err != nil {
		return nil, err
	}
	config.Image = image

	// Not parseReplicas/hasExplicitReplicas, which the other kinds use: that
	// pair runs the value through toInt32 and falls back to the default when
	// the conversion fails, so `replicas: "3"` silently becomes 1 and
	// `replicas: -1` is carried through to a Deployment the apiserver then
	// refuses (ValidateDeploymentSpec runs ValidateNonnegativeField on
	// Replicas). This kind reads it as a checked, presence-aware field
	// instead. The divergence is deliberate and one-directional — deployment
	// refuses documents the older kinds accept, never the reverse — because
	// tightening the shared helper would change what those kinds already
	// build. Tracked for them separately in go-kure/launcher#393.
	replicas, replicasAuthored, err := parseInt32Field(props, "replicas", "replicas")
	if err != nil {
		return nil, err
	}
	if replicasAuthored && replicas < 0 {
		return nil, errors.Errorf("replicas: must be >= 0, got %d", replicas)
	}
	config.Replicas = 1
	if replicasAuthored {
		config.Replicas = replicas
	}
	config.explicitReplicas = replicasAuthored

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
	// namedPortsAllowed=false: this kind publishes no port property, so its
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

	// The authored map, not the null-stripped copy — see the comment on props.
	depSpec, err := parseDeploymentSpec(component.Properties)
	if err != nil {
		return nil, err
	}
	config.DeploymentSpec = depSpec

	return config, nil
}

// DeploymentConfig implements stack.ApplicationConfig for deployment components.
type DeploymentConfig struct {
	Name            string
	Namespace       string
	Image           string
	Replicas        int32
	Env             []corev1.EnvVar
	EnvFrom         []corev1.EnvFromSource
	Resources       ResourceRequirements
	Command         []string
	Args            []string
	Probes          ProbeConfig
	Lifecycle       *corev1.Lifecycle
	SecurityContext *corev1.SecurityContext
	WorkingDir      string
	Volumes         []corev1.Volume
	VolumeMounts    []corev1.VolumeMount
	PVCs            []PVCConfig
	InitContainers  []InitContainerConfig
	Sidecars        []SidecarContainerConfig
	// PodSpec holds the shared pod-level properties (see parsePodSpec).
	PodSpec PodSpecConfig
	// DeploymentSpec holds the DeploymentSpec-level properties
	// (see parseDeploymentSpec).
	DeploymentSpec   DeploymentSpecConfig
	explicitReplicas bool
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, else the per-component ServiceAccount named after the
// component.
func (c *DeploymentConfig) ServiceAccountName() string {
	return effectiveServiceAccountName(c.PodSpec, c.Name)
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// Defaults are applied first so that enforced checks run on effective post-default values.
func (c *DeploymentConfig) ApplyPolicy(p oam.Policy) error {
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

// deploymentComponentLabels returns a FRESH label map on every call.
//
// It exists so that no two consumers share one map. The object's own metadata
// labels, the pod template's labels and any selector built from them are
// distinct maps here on purpose: a downstream merge into one of them — a trait
// adding a label to object metadata, say — must not silently rewrite a
// selector, which is immutable once the object exists and would change which
// pods the Deployment owns.
func deploymentComponentLabels(name string) map[string]string {
	return map[string]string{"app": name}
}

// Generate creates a Kubernetes Deployment and ServiceAccount (no Service).
// The ServiceAccount is omitted when serviceAccountName was authored.
func (c *DeploymentConfig) Generate(app *stack.Application) ([]*client.Object, error) {
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
		saObj := client.Object(createServiceAccount(generationServiceAccountName(c, app.Name), app.Namespace, deploymentComponentLabels(app.Name)))
		objects = append(objects, &saObj)
	}
	for _, pvc := range c.PVCs {
		p, err := BuildPVC(pvc, app.Namespace, deploymentComponentLabels(app.Name))
		if err != nil {
			return nil, err
		}
		pObj := client.Object(p)
		objects = append(objects, &pObj)
	}

	return objects, nil
}

func (c *DeploymentConfig) createDeployment(app *stack.Application) (*appsv1.Deployment, error) {
	// No Ports: this kind publishes no port property (see parseProbes'
	// namedPortsAllowed=false above).
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
	dep.Labels = deploymentComponentLabels(app.Name)
	dep.Annotations = nil
	dep.Spec.Template.Labels = deploymentComponentLabels(app.Name)
	kubernetes.SetDeploymentReplicas(dep, c.Replicas)

	if err := c.applyNonRWXConstraint(dep, app.Name); err != nil {
		return nil, err
	}
	// The authored strategy is applied after the non-RWX constraint so an
	// author who wrote `strategy: {type: Recreate}` alongside a non-RWX PVC
	// ends up with exactly that, and any conflicting authored strategy has
	// already been rejected above rather than silently overwritten.
	c.DeploymentSpec.apply(dep)

	podSpec, err := buildPodSpec(podSpecInput{
		Config:                    c.PodSpec,
		DefaultServiceAccountName: generationServiceAccountName(c, app.Name),
		MainContainer:             container,
		InitContainers:            c.InitContainers,
		Sidecars:                  c.Sidecars,
		Volumes:                   c.Volumes,
	})
	if err != nil {
		return nil, err
	}
	dep.Spec.Template.Spec = podSpec

	return dep, nil
}

// applyNonRWXConstraint enforces what a ReadWriteOnce claim implies for a
// Deployment: at most one pod may hold it, and a rolling update must not try
// to start a replacement pod while the old one still has it mounted.
//
// "At most one" is the rule, not "exactly one": `replicas: 0` is a deliberate
// scale-to-zero and holds the claim in no pod at all, so it is left alone.
//
// Kubernetes does not reject either combination — the Deployment is created
// and the second pod simply hangs unschedulable or stuck attaching — so this
// is a build-time guard rather than a mirror of an apiserver rule. A
// deliberately authored `strategy` that contradicts it is reported instead of
// being overwritten, which is the difference between this kind and the worker
// kind, where the substitution is silent because there is no strategy property
// to contradict.
func (c *DeploymentConfig) applyNonRWXConstraint(dep *appsv1.Deployment, name string) error {
	if !hasNonRWXPVC(c.PVCs) {
		return nil
	}
	if c.Replicas > 1 {
		return errors.Errorf("deployment %q: a non-RWX PVC allows at most one replica, got %d", name, c.Replicas)
	}
	if s := c.DeploymentSpec.Strategy; s != nil && s.Type != appsv1.RecreateDeploymentStrategyType {
		return errors.Errorf("deployment %q: strategy.type must be %s when a non-RWX PVC is attached, got %s; a rolling update would start the replacement pod before the old one released the volume", name, appsv1.RecreateDeploymentStrategyType, s.Type)
	}
	kubernetes.SetDeploymentStrategy(dep, appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType})
	return nil
}
