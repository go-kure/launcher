package components

import (
	"maps"
	"slices"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		"ports":                schemaMainContainerPorts(),
		"serviceName":          {Type: oam.PropertyTypeString, Description: "Name of the governing Service that gives the pods their stable network identity, a headless `service` component authored beside this one. No default. Must be a valid Service name, a DNS-1035 label."},
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
		"initContainers":       schemaInitContainers(),
		"sidecars":             schemaSidecars(),
		"affinity":             schemaAffinity(),
		// The two raw scheduling shapes scheduling.go projects beside the
		// shorthand above; `affinity` stays the shorthand on this kind.
		"tolerations":               schemaTolerations(),
		"topologySpreadConstraints": schemaTopologySpreadConstraints(),
	}
	maps.Copy(m, schemaContainerFields())
	maps.Copy(m, schemaPodSpec(false, false))
	maps.Copy(m, schemaStatefulSetSpec())
	return m
}

// FillCapabilityDefaults gives each volumeClaimTemplates entry that leaves
// storageClass unauthored (absent or null) the ClusterProfile `pvc`
// capability's storageClassName, as an authored pvc trait, the
// persistentvolumeclaim kind and a webservice or worker pvc volume take it
// (go-kure/launcher#761). An authored class, "" included, wins. The binding is
// read once, and only when the list holds an entry, so `pvc` is recorded as
// consumed exactly when the component builds a claim template. A shape the
// parser refuses (a list that is not one, an entry that is not a mapping) is
// left for ToApplicationConfig to refuse.
func (h *StatefulsetHandler) FillCapabilityDefaults(props map[string]any, lctx oam.LoweringContext) (map[string]any, error) {
	entries, ok := props["volumeClaimTemplates"].([]any)
	if !ok {
		return props, nil
	}
	var platformClass any
	platformRead := false
	var filled []any
	for i, v := range entries {
		entry, ok := nullElem(v).(map[string]any)
		if !ok {
			continue
		}
		if !platformRead {
			platformRead = true
			sc, err := capabilityStorageClass(lctx)
			if err != nil {
				return nil, err
			}
			if sc == nil {
				return props, nil
			}
			platformClass = sc
		}
		if _, present := authoredValue(entry, "storageClass"); present {
			continue
		}
		if filled == nil {
			filled = slices.Clone(entries)
		}
		entry = maps.Clone(entry)
		entry["storageClass"] = platformClass
		filled[i] = entry
	}
	if filled == nil {
		return props, nil
	}
	out := maps.Clone(props)
	out["volumeClaimTemplates"] = filled
	return out, nil
}

// ToApplicationConfig converts an OAM statefulset component to a StatefulsetConfig.
func (h *StatefulsetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &StatefulsetConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
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

	replicas, replicasAuthored, err := parseReplicas(props, 1)
	if err != nil {
		return nil, err
	}
	config.Replicas = replicas
	config.explicitReplicas = replicasAuthored

	// serviceName names the governing Service, which the component does not
	// emit, so it is held to the Service-name rule (validateServiceName). It
	// has no default: unset, spec.serviceName stays empty.
	if sn, present, err := parseStringField(props, "serviceName", "serviceName"); err != nil {
		return nil, err
	} else if present {
		if err := validateServiceName("serviceName", sn); err != nil {
			return nil, err
		}
		config.ServiceName = sn
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

	vcts, err := parseVolumeClaimTemplates(props)
	if err != nil {
		return nil, err
	}
	config.VolumeClaimTemplates = vcts

	parsed, err := parsePodVolumes(props)
	if err != nil {
		return nil, err
	}
	config.Volumes = parsed.Volumes
	config.VolumeMounts = parsed.Mounts
	config.VolumeDevices = parsed.Devices
	config.PVCs = parsed.PVCs
	// The main container's devices and mounts come from two parsers here — the
	// claim templates and `volumes` — each of which checks only its own
	// entries.
	vctMounts, vctDevices := claimTemplateMountsAndDevices(vcts)
	mainMounts := append(vctMounts, parsed.Mounts...)
	if err := checkMainContainerVolumeDevices(mainMounts, append(vctDevices, parsed.Devices...)); err != nil {
		return nil, err
	}
	if err := checkClaimTemplateCollisions(vcts, parsed.Volumes, mainMounts); err != nil {
		return nil, err
	}

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
	tolerations, err := parseTolerations(props)
	if err != nil {
		return nil, err
	}
	config.Tolerations = tolerations
	tscs, err := parseTopologySpreadConstraints(props)
	if err != nil {
		return nil, err
	}
	config.TopologySpreadConstraints = tscs

	sidecars, err := parseSidecars(props)
	if err != nil {
		return nil, err
	}
	config.Sidecars = sidecars
	if err := checkExtraContainerVolumeModes(declaredVolumeModes(parsed, vcts), initContainers, sidecars); err != nil {
		return nil, err
	}
	if err := checkPodPortNames(config.Ports, sidecars); err != nil {
		return nil, err
	}
	// Claim templates are left out on purpose: they are persistentVolumeClaim
	// volumes, never emptyDir, so the API server refuses a fileKeyRef naming one.
	if err := checkFileKeyRefVolumes(parsed.Volumes, env, initContainers, sidecars); err != nil {
		return nil, err
	}

	podSpec, err := parsePodSpec(props, false)
	if err != nil {
		return nil, err
	}
	config.PodSpec = podSpec

	stsSpec, err := parseStatefulSetSpec(props)
	if err != nil {
		return nil, err
	}
	config.StatefulSetSpec = stsSpec

	return config, nil
}

// StatefulsetConfig implements stack.ApplicationConfig for statefulset components.
type StatefulsetConfig struct {
	Name string
	// ObjectName names the StatefulSet (oam.Component.ObjectName), and with it
	// the pods and volume claims the controller names after it; its labels,
	// selector and main container keep Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the StatefulSet
	// (oam.Component.ObjectMetadata). They go on the StatefulSet's own
	// metadata: its pod template keeps the labels of the kind.
	Metadata             oam.ObjectMetadata
	Namespace            string
	Image                string
	Replicas             int32
	Ports                []corev1.ContainerPort // the main container's ports
	ServiceName          string                 // spec.serviceName, the governing Service; empty when unset
	Env                  []corev1.EnvVar
	EnvFrom              []corev1.EnvFromSource
	Resources            ResourceRequirements
	Command              []string
	Args                 []string
	Probes               ProbeConfig
	Lifecycle            *corev1.Lifecycle
	SecurityContext      *corev1.SecurityContext
	WorkingDir           string
	ContainerFields      ContainerFields // the main container's fields every container accepts (see parseContainerFields)
	VolumeClaimTemplates []VolumeClaimTemplate
	Volumes              []corev1.Volume
	VolumeMounts         []corev1.VolumeMount
	VolumeDevices        []corev1.VolumeDevice
	PVCs                 []PVCConfig
	InitContainers       []InitContainerConfig
	Sidecars             []SidecarContainerConfig
	Affinity             AffinityConfig
	// Tolerations and TopologySpreadConstraints are the raw corev1 scheduling
	// shapes (see scheduling.go), carried as the API types because nothing is
	// inferred from them. Affinity above is the four-key shorthand.
	Tolerations               []corev1.Toleration
	TopologySpreadConstraints []corev1.TopologySpreadConstraint
	// PodSpec holds the shared pod-level properties (see parsePodSpec).
	PodSpec PodSpecConfig
	// StatefulSetSpec holds the StatefulSetSpec-level properties that are
	// neither the pod template nor the claim templates (see
	// parseStatefulSetSpec).
	StatefulSetSpec  StatefulSetSpecConfig
	explicitReplicas bool
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, or "" when the pods run as no named account.
func (c *StatefulsetConfig) ServiceAccountName() (string, bool) {
	return c.PodSpec.ServiceAccountName, true
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
	for _, vct := range c.VolumeClaimTemplates {
		if err := enforceMaxStorageSize(vct.effectiveStorageRequest(), p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// Generate creates a Kubernetes StatefulSet, and nothing beside it: no
// ServiceAccount and no standalone claim (go-kure/launcher#702). It emits no
// Service either: ServiceName names one authored beside it. A library caller
// may set ServiceName itself, so a non-empty one is held to the Service-name
// rule here too.
func (c *StatefulsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if c.ServiceName != "" {
		if err := validateServiceName("serviceName", c.ServiceName); err != nil {
			return nil, err
		}
	}
	sts, err := c.createStatefulSet(app)
	if err != nil {
		return nil, err
	}
	return kindObject(sts, c.Metadata)
}

// claimTemplateMountsAndDevices splits the claim templates into the main
// container's filesystem mounts and, for a volumeMode: Block template
// (go-kure/launcher#385), its raw block devices. Each template has exactly one
// of MountPath and DevicePath (parseVolumeClaimTemplates).
func claimTemplateMountsAndDevices(vcts []VolumeClaimTemplate) ([]corev1.VolumeMount, []corev1.VolumeDevice) {
	var mounts []corev1.VolumeMount
	var devices []corev1.VolumeDevice
	for _, vct := range vcts {
		if vct.DevicePath != "" {
			devices = append(devices, corev1.VolumeDevice{Name: vct.Name, DevicePath: vct.DevicePath})
			continue
		}
		mounts = append(mounts, corev1.VolumeMount{Name: vct.Name, MountPath: vct.MountPath})
	}
	return mounts, devices
}

// checkClaimTemplateCollisions refuses a claim template that shares its name
// with another claim template or with a `volumes` entry, and a main-container
// mountPath used twice across the claim templates and `volumes`. Each parser
// checks only its own entries, and the apiserver does not catch the overlap
// either: StatefulSet validation skips the pod template's volumes. The
// StatefulSet controller keys claim templates by name, so of two sharing a name
// only one claim is created, and it replaces a pod volume named like a template
// with that template's claim, so the authored volume is never mounted. A
// repeated mountPath is refused only when the controller creates the pods.
// mounts is the main container's full mount list, claim templates first; the
// Block (device) side is checkMainContainerVolumeDevices'.
func checkClaimTemplateCollisions(vcts []VolumeClaimTemplate, volumes []corev1.Volume, mounts []corev1.VolumeMount) error {
	names := make(map[string]bool, len(vcts))
	for _, vct := range vcts {
		if names[vct.Name] {
			return errors.Errorf("volumeClaimTemplate %q: duplicate name; the StatefulSet controller creates one claim per template name, so one of the two would never be provisioned", vct.Name)
		}
		names[vct.Name] = true
	}
	for _, v := range volumes {
		if names[v.Name] {
			return errors.Errorf("volume %q has the same name as a claim template; the StatefulSet controller replaces a pod volume named like a claim template with the claim, so this volume would never be mounted. Rename one of them", v.Name)
		}
	}
	paths := make(map[string]string, len(mounts))
	for _, m := range mounts {
		if other, dup := paths[m.MountPath]; dup {
			return errors.Errorf("volume %q: duplicate mountPath %q, already used by %q", m.Name, m.MountPath, other)
		}
		paths[m.MountPath] = m.Name
	}
	return nil
}

func (c *StatefulsetConfig) createStatefulSet(app *stack.Application) (*appsv1.StatefulSet, error) {
	// Claim-template mounts (and devices) precede the authored volume ones, as
	// before.
	vctMounts, vctDevices := claimTemplateMountsAndDevices(c.VolumeClaimTemplates)
	mounts := make([]corev1.VolumeMount, 0, len(vctMounts)+len(c.VolumeMounts))
	mounts = append(mounts, vctMounts...)
	mounts = append(mounts, c.VolumeMounts...)
	devices := make([]corev1.VolumeDevice, 0, len(vctDevices)+len(c.VolumeDevices))
	devices = append(devices, vctDevices...)
	devices = append(devices, c.VolumeDevices...)
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
		VolumeMounts:    mounts,
		VolumeDevices:   devices,
		Fields:          c.ContainerFields,
	})
	if err != nil {
		return nil, err
	}

	sts := kubernetes.CreateStatefulSet(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	sts.Labels = appLabels(app.Name)
	sts.Annotations = nil
	sts.Spec.Template.Labels = appLabels(app.Name)
	sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: appLabels(app.Name)}
	kubernetes.SetStatefulSetReplicas(sts, c.Replicas)
	sts.Spec.ServiceName = c.ServiceName
	c.StatefulSetSpec.apply(sts)

	podSpec, err := buildPodSpec(podSpecInput{
		Config:         c.PodSpec,
		MainContainer:  container,
		InitContainers: c.InitContainers,
		Sidecars:       c.Sidecars,
		Volumes:        c.Volumes,
		Affinity:       buildAffinity(c.Affinity, appLabels(app.Name)),

		Tolerations:               c.Tolerations,
		TopologySpreadConstraints: c.TopologySpreadConstraints,
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
		// vct.Size is empty exactly when the author used the long spelling,
		// resources.requests.storage, which vct.Spec.apply writes in a moment;
		// the parser guarantees one of the two is present, and MustParse would
		// panic on "".
		var request resource.Quantity
		if vct.Size != "" {
			request = resource.MustParse(vct.Size)
		}
		pvc := corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: vct.Name},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: accessModes,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: request},
				},
			},
		}
		if vct.StorageClass != "" || vct.StorageClassExplicitEmpty {
			pvc.Spec.StorageClassName = &vct.StorageClass
		}
		vct.Spec.apply(&pvc)
		kubernetes.AddStatefulSetVolumeClaimTemplate(sts, pvc)
	}

	return sts, nil
}
