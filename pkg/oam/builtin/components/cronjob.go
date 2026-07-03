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
		"image":                      {Type: oam.PropertyTypeString, Required: true},
		"schedule":                   {Type: oam.PropertyTypeString, Required: true},
		"restartPolicy":              {Type: oam.PropertyTypeString, Default: "OnFailure", Enum: []any{"Never", "OnFailure"}},
		"successfulJobsHistoryLimit": {Type: oam.PropertyTypeInteger, Default: 3},
		"failedJobsHistoryLimit":     {Type: oam.PropertyTypeInteger, Default: 1},
		"env":                        schemaEnv(),
		"resources":                  schemaResources(),
		"command":                    schemaStringArray(),
		"args":                       schemaStringArray(),
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
	if resources, ok := props["resources"].(map[string]any); ok {
		config.Resources = parseResources(resources)
	}
	config.explicitResources = resourceExplicitFlags(props)
	config.Command = parseCommand(props)
	config.Args = parseArgs(props)

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
	Env                        []EnvVar
	Resources                  ResourceRequirements
	Command                    []string
	Args                       []string
	InitContainers             []InitContainerConfig
	explicitResources          explicitResourceFlags
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// CronJobs don't have replicas, so only resource and registry limits apply.
func (c *CronjobConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

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

	return nil
}

// Generate creates a Kubernetes CronJob and ServiceAccount.
func (c *CronjobConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": app.Name}
	cronjob, err := c.createCronJob(app)
	if err != nil {
		return nil, err
	}
	sa := createServiceAccount(app.Name, app.Namespace, labels)

	obj := client.Object(cronjob)
	saObj := client.Object(sa)

	return []*client.Object{&obj, &saObj}, nil
}

func (c *CronjobConfig) createCronJob(app *stack.Application) (*batchv1.CronJob, error) {
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

	return cj, nil
}
