package components

import (
	"maps"
	"regexp"
	"strings"
	"time"

	// Embeds the IANA zoneinfo database into this binary (including kurel,
	// which ships CGO_ENABLED=0 per .goreleaser.yml:20), so the timeZone
	// validation below is host-independent rather than relying on system
	// zoneinfo being present at runtime.
	_ "time/tzdata"

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

// cronDescriptorSchedules is the fixed set of "@"-prefixed schedule descriptors
// Kubernetes CronJob accepts (robfig/cron's ParseStandard), excluding @reboot
// (meaningless for a CronJob — there is no host to reboot) and @every, which
// takes a duration argument and is validated separately in validateCronSchedule.
var cronDescriptorSchedules = map[string]bool{
	"@yearly":   true,
	"@annually": true,
	"@monthly":  true,
	"@weekly":   true,
	"@daily":    true,
	"@midnight": true,
	"@hourly":   true,
}

// validateCronSchedule accepts the standard 5-field cron form (cronScheduleRe,
// unchanged from before this function existed — see the round-7 finding below for
// why it is not tightened further), one of the fixed @-descriptor schedules above,
// or "@every <duration>" with the duration validated via time.ParseDuration (not
// merely regex-matched: a bare-suffix regex would let "@every nope" through,
// producing a schedule Kubernetes' own CronJob controller rejects at apply time).
//
// Round-7 finding (see plan-279-cronjob-jobspec.md "What this does NOT solve"):
// the plain 5-field branch deliberately still accepts any 5 whitespace-separated
// tokens with no per-field semantic check (e.g. "99 99 99 99 99" builds
// successfully) — tightening it was drafted and reverted because the current regex
// already accepts such strings today, so rejecting them here would be a breaking
// change under this design's additive-compatibility test, not an additive one.
func validateCronSchedule(schedule string) error {
	if cronScheduleRe.MatchString(schedule) {
		return nil
	}
	if cronDescriptorSchedules[schedule] {
		return nil
	}
	if suffix, ok := strings.CutPrefix(schedule, "@every "); ok {
		if _, err := time.ParseDuration(suffix); err != nil {
			return errors.Errorf("invalid cron schedule %q: invalid @every duration: %v", schedule, err)
		}
		return nil
	}
	return errors.Errorf("invalid cron schedule %q: must be a 5-field cron expression (e.g. \"0 2 * * *\"), an @-descriptor (@yearly, @annually, @monthly, @weekly, @daily, @midnight, @hourly), or \"@every <duration>\" (e.g. \"@every 1h30m\")", schedule)
}

// CronjobHandler handles OAM cronjob components.
type CronjobHandler struct{}

// CanHandle returns true for cronjob component type.
func (h *CronjobHandler) CanHandle(componentType string) bool {
	return componentType == "cronjob"
}

// PropertySchema declares the cronjob component's user-facing properties.
func (h *CronjobHandler) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":                      {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the job container."},
		"schedule":                   {Type: oam.PropertyTypeString, Required: true, Description: "Cron schedule: a standard 5-field cron expression (e.g. \"0 2 * * *\"), an @-descriptor (@yearly, @annually, @monthly, @weekly, @daily, @midnight, @hourly), or \"@every <duration>\" (e.g. \"@every 1h30m\")."},
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
		"concurrencyPolicy":          schemaCronJobConcurrencyPolicy(),
		"suspend":                    schemaCronJobSuspend(),
		"startingDeadlineSeconds":    schemaCronJobStartingDeadlineSeconds(),
		"timeZone":                   schemaCronJobTimeZone(),
	}
	maps.Copy(m, schemaJobSpec(false))
	maps.Copy(m, schemaPodSpec(false, true))
	return m
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
	if err := validateCronSchedule(schedule); err != nil {
		return nil, err
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

	if v, present, err := parseStringField(props, "concurrencyPolicy", "concurrencyPolicy"); err != nil {
		return nil, err
	} else if present {
		switch batchv1.ConcurrencyPolicy(v) {
		case batchv1.AllowConcurrent, batchv1.ForbidConcurrent, batchv1.ReplaceConcurrent:
			cp := batchv1.ConcurrencyPolicy(v)
			config.ConcurrencyPolicy = &cp
		default:
			return nil, errors.Errorf("concurrencyPolicy: invalid value %q, must be %q, %q, or %q", v, batchv1.AllowConcurrent, batchv1.ForbidConcurrent, batchv1.ReplaceConcurrent)
		}
	}

	if suspend, err := parseBoolField(props, "suspend", "suspend"); err != nil {
		return nil, err
	} else if suspend != nil {
		config.Suspend = suspend
	}

	if v, present, err := parseInt64Field(props, "startingDeadlineSeconds", "startingDeadlineSeconds"); err != nil {
		return nil, err
	} else if present {
		if v < 0 {
			return nil, errors.Errorf("startingDeadlineSeconds: must be >= 0, got %d", v)
		}
		config.StartingDeadlineSeconds = &v
	}

	// timeZone cannot reuse parseStringField: that helper treats an authored ""
	// the same as omission (ok=false, no error), but timeZone must reject an
	// authored "" outright — time.LoadLocation("") itself returns UTC with no
	// error, which would silently accept a value the author almost certainly
	// didn't intend. "Local" is rejected case-insensitively before calling
	// time.LoadLocation for the same reason: LoadLocation("Local") succeeds
	// (it returns the process's own local zone), but Kubernetes' own CronJob
	// validation explicitly rejects "Local" as server-dependent.
	if raw, present := props["timeZone"]; present {
		tz, ok := raw.(string)
		if !ok {
			return nil, errors.Errorf("timeZone: must be a string, got %T", raw)
		}
		if tz == "" {
			return nil, errors.New("timeZone: must not be an empty string; omit the property instead")
		}
		if strings.EqualFold(tz, "Local") {
			return nil, errors.Errorf("timeZone: %q is not a valid IANA time zone name (Kubernetes rejects \"Local\" as server-dependent)", tz)
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, errors.Errorf("timeZone: %q is not a valid IANA time zone name: %v", tz, err)
		}
		config.TimeZone = &tz
	}

	jobSpec, err := parseJobSpec(props)
	if err != nil {
		return nil, err
	}
	config.JobSpec = jobSpec

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

	podSpec, err := parsePodSpec(props, true)
	if err != nil {
		return nil, err
	}
	config.PodSpec = podSpec

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
	// ConcurrencyPolicy, Suspend, StartingDeadlineSeconds, and TimeZone are all
	// presence-gated pointers. Every corresponding batchv1.CronJobSpec field IS
	// tagged `omitempty`, but that only suppresses a nil pointer (Suspend,
	// StartingDeadlineSeconds, TimeZone) or the Go zero value on a non-pointer
	// scalar (ConcurrencyPolicy's underlying string type) — none of that
	// protects against createCronJob calling a kure setter unconditionally,
	// since every one of those setters writes a non-nil/non-zero value
	// regardless of whether the author authored it. A bare (non-presence-gated)
	// value here would add a key to every generated CronJob that no author
	// asked for. See the plan's CronJobSpec-level field table.
	ConcurrencyPolicy       *batchv1.ConcurrencyPolicy
	Suspend                 *bool
	StartingDeadlineSeconds *int64
	TimeZone                *string
	JobSpec                 JobSpecConfig
	Env                     []corev1.EnvVar
	EnvFrom                 []corev1.EnvFromSource
	Resources               ResourceRequirements
	Command                 []string
	Args                    []string
	Probes                  ProbeConfig
	Lifecycle               *corev1.Lifecycle
	SecurityContext         *corev1.SecurityContext
	WorkingDir              string
	Volumes                 []corev1.Volume
	VolumeMounts            []corev1.VolumeMount
	InitContainers          []InitContainerConfig
	PVCs                    []PVCConfig
	// PodSpec holds the shared pod-level properties (see parsePodSpec). Parsed
	// with jobPods=true, so `podActiveDeadlineSeconds` (the pod's own
	// deadline) is accepted alongside the job-level `activeDeadlineSeconds`
	// from schemaJobSpec — two different fields, two different keys.
	PodSpec PodSpecConfig
}

// ServiceAccountName implements oam.ServiceAccountNamer: the authored
// serviceAccountName, else the per-component ServiceAccount named after the
// component.
func (c *CronjobConfig) ServiceAccountName() string {
	return effectiveServiceAccountName(c.PodSpec, c.Name)
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
	for _, pvc := range c.PVCs {
		if err := enforceMaxStorageSize(pvc.Size, p.MaxStorageSize()); err != nil {
			return err
		}
	}

	return nil
}

// Generate creates a Kubernetes CronJob, ServiceAccount, and any declared PVCs.
// The ServiceAccount is omitted when serviceAccountName was authored.
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

	obj := client.Object(cronjob)
	objects := []*client.Object{&obj}
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

func (c *CronjobConfig) createCronJob(app *stack.Application) (*batchv1.CronJob, error) {
	labels := map[string]string{"app": app.Name}

	// No Ports: cronjob exposes no port property (see parseProbes' namedPortsAllowed=false above).
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

	cj := kubernetes.CreateCronJob(app.Name, app.Namespace, c.Schedule)
	cj.Labels = labels
	cj.Annotations = nil
	cj.Spec.JobTemplate.Labels = labels
	cj.Spec.JobTemplate.Spec.Template.Labels = labels
	kubernetes.SetCronJobSuccessfulJobsHistoryLimit(cj, c.SuccessfulJobsHistoryLimit)
	kubernetes.SetCronJobFailedJobsHistoryLimit(cj, c.FailedJobsHistoryLimit)
	if c.ConcurrencyPolicy != nil {
		kubernetes.SetCronJobConcurrencyPolicy(cj, *c.ConcurrencyPolicy)
	}
	if c.Suspend != nil {
		kubernetes.SetCronJobSuspend(cj, *c.Suspend)
	}
	if c.StartingDeadlineSeconds != nil {
		kubernetes.SetCronJobStartingDeadlineSeconds(cj, *c.StartingDeadlineSeconds)
	}
	if c.TimeZone != nil {
		kubernetes.SetCronJobTimeZone(cj, c.TimeZone)
	}
	applyJobSpec(&cj.Spec.JobTemplate.Spec, c.JobSpec)

	// Replaces the whole pod spec, including the RestartPolicy: Never that
	// kure's CreateCronJob pre-fills — c.RestartPolicy is always set (default
	// OnFailure), so the authored/defaulted value wins as before.
	podSpec, err := buildPodSpec(podSpecInput{
		Config:                    c.PodSpec,
		DefaultServiceAccountName: app.Name,
		MainContainer:             container,
		InitContainers:            c.InitContainers,
		Volumes:                   c.Volumes,
		RestartPolicy:             c.RestartPolicy,
	})
	if err != nil {
		return nil, err
	}
	cj.Spec.JobTemplate.Spec.Template.Spec = podSpec

	return cj, nil
}
