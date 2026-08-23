package components

import (
	"regexp"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// cronScheduleRe matches a standard 5-field cron expression (no special @syntax).
var cronScheduleRe = regexp.MustCompile(`^(\S+\s+){4}\S+$`)

// CronjobHandler handles OAM cronjob components.
type CronjobHandler struct{}

// CanHandle returns true for cronjob component type.
func (h *CronjobHandler) CanHandle(componentType string) bool {
	return componentType == "cronjob"
}

// PropertySchema declares the cronjob component's user-facing properties.
func (h *CronjobHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"image":                      {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the job container."},
		"schedule":                   {Type: oam.PropertyTypeString, Required: true, Description: "Cron schedule in standard 5-field format (e.g. \"0 2 * * *\")."},
		"restartPolicy":              {Type: oam.PropertyTypeString, Default: "OnFailure", Enum: []any{"Never", "OnFailure"}, Description: "Pod restart policy for the job's containers."},
		"successfulJobsHistoryLimit": {Type: oam.PropertyTypeInteger, Default: 3, Description: "Number of successful finished jobs to retain."},
		"failedJobsHistoryLimit":     {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of failed finished jobs to retain."},
		"env":                        schemaEnv(false),
		"envFrom":                    schemaEnvFrom(false),
		"resources":                  schemaResources(false),
		"command":                    schemaStringArray(),
		"args":                       schemaStringArray(),
		"probes":                     schemaProbes(false),
		"lifecycle":                  schemaLifecycle(false),
		"securityContext":            schemaSecurityContext(false),
		"workingDir":                 schemaWorkingDir(false),
		"volumes":                    schemaVolumes(),
		"initContainers":             schemaContainers(),
	}
}

// ToApplicationConfig converts an OAM cronjob component to a CronjobConfig.
func (h *CronjobHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &CronjobConfig{
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

	schedule, ok := props["schedule"].(string)
	if !ok {
		return nil, errors.New("required property 'schedule' missing or not a string")
	}
	if !cronScheduleRe.MatchString(schedule) {
		return nil, errors.Errorf("invalid cron schedule %q: must be a 5-field cron expression (e.g. \"0 2 * * *\")", schedule)
	}
	config.Schedule = schedule

	config.RestartPolicy = corev1.RestartPolicyOnFailure
	if rp, ok := props["restartPolicy"].(string); ok {
		switch rp {
		case string(corev1.RestartPolicyNever):
			config.RestartPolicy = corev1.RestartPolicyNever
		case string(corev1.RestartPolicyOnFailure):
			config.RestartPolicy = corev1.RestartPolicyOnFailure
		default:
			return nil, errors.Errorf("invalid restartPolicy %q, must be 'Never' or 'OnFailure'", rp)
		}
	}

	config.SuccessfulJobsHistoryLimit = 3
	if raw, ok := props["successfulJobsHistoryLimit"]; ok {
		limit, err := parseHistoryLimit("successfulJobsHistoryLimit", raw)
		if err != nil {
			return nil, err
		}
		config.SuccessfulJobsHistoryLimit = limit
	}

	config.FailedJobsHistoryLimit = 1
	if raw, ok := props["failedJobsHistoryLimit"]; ok {
		limit, err := parseHistoryLimit("failedJobsHistoryLimit", raw)
		if err != nil {
			return nil, err
		}
		config.FailedJobsHistoryLimit = limit
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
	// namedPortsAllowed=false: cronjob exposes no port property at all, so its
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

	return config, nil
}

// CronjobConfig implements stack.ApplicationConfig for cronjob components.
type CronjobConfig struct {
	Name                       string
	Namespace                  string
	Image                      string
	Schedule                   string
	RestartPolicy              corev1.RestartPolicy
	SuccessfulJobsHistoryLimit int32
	FailedJobsHistoryLimit     int32
	Env                        []corev1.EnvVar
	EnvFrom                    []corev1.EnvFromSource
	Resources                  ResourceRequirements
	Command                    []string
	Args                       []string
	Probes                     ProbeConfig
	Lifecycle                  *corev1.Lifecycle
	SecurityContext            *corev1.SecurityContext
	WorkingDir                 string
	Volumes                    []corev1.Volume
	VolumeMounts               []corev1.VolumeMount
	InitContainers             []InitContainerConfig
	PVCs                       []PVCConfig
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// CronJobs don't have replicas, so only resource and registry limits apply.
func (c *CronjobConfig) ApplyPolicy(p oam.Policy) error {
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
	for _, pvc := range c.PVCs {
		if err := enforceMaxStorageSize(pvc.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// Generate creates a Kubernetes CronJob, ServiceAccount, and any declared PVCs.
func (c *CronjobConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
	var err error
	c.PVCs, err = qualifyPVCNames(c.Volumes, c.PVCs, app.Name)
	if err != nil {
		return nil, err
	}
	cronjob, err := c.createCronJob(app)
	if err != nil {
		return nil, err
	}
	sa := createServiceAccount(app.Name, app.Namespace, labels)

	obj := client.Object(cronjob)
	saObj := client.Object(sa)
	objects := []*client.Object{&obj, &saObj}

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

func (c *CronjobConfig) createCronJob(app *stack.Application) (*batchv1.CronJob, error) {
	labels := map[string]string{"app": app.Name}

	container := kubernetes.CreateContainer(app.Name, c.Image, c.Command, c.Args)
	kubernetes.SetContainerResources(container, buildResourceRequirements(c.Resources))
	for _, env := range c.Env {
		kubernetes.AddContainerEnv(container, env)
	}
	for _, ef := range c.EnvFrom {
		kubernetes.AddContainerEnvFrom(container, ef)
	}
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
	applyProbes(container, c.Probes)

	cj := kubernetes.CreateCronJob(app.Name, app.Namespace, c.Schedule)
	cj.Labels = labels
	cj.Annotations = nil
	cj.Spec.JobTemplate.Labels = labels
	cj.Spec.JobTemplate.Spec.Template.Labels = labels
	cj.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy = c.RestartPolicy
	kubernetes.SetCronJobSuccessfulJobsHistoryLimit(cj, c.SuccessfulJobsHistoryLimit)
	kubernetes.SetCronJobFailedJobsHistoryLimit(cj, c.FailedJobsHistoryLimit)
	kubernetes.SetCronJobServiceAccountName(cj, app.Name)
	// Init containers added before the main container so declaration order is
	// preserved in spec.template.spec.initContainers.
	for _, ic := range c.InitContainers {
		initContainer, err := buildInitContainer(ic)
		if err != nil {
			return nil, err
		}
		if err := kubernetes.AddCronJobInitContainer(cj, initContainer); err != nil {
			return nil, errors.Wrapf(err, "add init container %q", ic.Name)
		}
	}
	if err := kubernetes.AddCronJobContainer(cj, container); err != nil {
		return nil, errors.Wrapf(err, "add container %q", c.Name)
	}
	for i := range c.Volumes {
		if err := kubernetes.AddCronJobVolume(cj, &c.Volumes[i]); err != nil {
			return nil, errors.Wrapf(err, "add volume %q", c.Volumes[i].Name)
		}
	}

	return cj, nil
}
