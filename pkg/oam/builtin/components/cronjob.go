package components

import (
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

// CronjobHandler handles OAM cronjob components: the kind-named projection of
// a batch/v1 CronJob (go-kure/launcher#1629).
//
// Its properties are the top-level json fields of batchv1.CronJobSpec, decoded
// strictly (decodeKindSpec). It emits the CronJob, named after the component
// in the build namespace, with the authored spec. One thing is added: the
// `app` label on the pod template of the job template, beside the authored
// labels, which every workload kind gives its pods and launcher's traits and
// Services select on (podTemplateAppLabelsAt). The object itself, and the job
// template's metadata, get no label, no annotation and no default of
// launcher's. The job template takes no selector: the API server generates
// one. TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CronjobHandler struct{}

// CanHandle returns true for cronjob component type.
func (h *CronjobHandler) CanHandle(componentType string) bool {
	return componentType == "cronjob"
}

// PropertySchema declares every top-level batchv1.CronJobSpec field by its json
// name. Structured fields are open objects whose content is checked by the
// strict decode, not by this schema.
func (h *CronjobHandler) PropertySchema() map[string]oam.PropertySchema {
	jobTemplate := jobTemplateSchema("Required. CronJob spec.jobTemplate: the Job each run creates, its `metadata` and its `spec`. The pod template under spec.template is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; its metadata.labels gains the `app` label, and an authored `app` with another value is refused. The Job's spec.selector and spec.manualSelector true are refused: the API server generates the selector.")
	jobTemplate.Required = true
	return oam.DeclareBounds(map[string]oam.PropertySchema{
		"schedule":                   {Type: oam.PropertyTypeString, Required: true, Description: "Required. CronJob spec.schedule: a standard 5-field cron expression (e.g. \"0 2 * * *\"), an @-descriptor (@yearly, @annually, @monthly, @weekly, @daily, @midnight, @hourly), or \"@every <duration>\" (e.g. \"@every 1h30m\")."},
		"jobTemplate":                jobTemplate,
		"timeZone":                   {Type: oam.PropertyTypeString, Description: "CronJob spec.timeZone: an IANA time zone name the schedule is read in; \"Local\" and the empty string are refused. The API server reads the schedule in the controller's zone when it is unset."},
		"startingDeadlineSeconds":    {Type: oam.PropertyTypeInteger, Description: "CronJob spec.startingDeadlineSeconds: seconds after a scheduled time within which a missed run may still start."},
		"concurrencyPolicy":          {Type: oam.PropertyTypeString, Description: "CronJob spec.concurrencyPolicy: Allow, Forbid or Replace. The API server defaults it to Allow; an authored empty string is refused."},
		"suspend":                    {Type: oam.PropertyTypeBoolean, Description: "CronJob spec.suspend: true stops the CronJob from creating Jobs."},
		"successfulJobsHistoryLimit": {Type: oam.PropertyTypeInteger, Description: "CronJob spec.successfulJobsHistoryLimit: the number of finished successful Jobs kept. The API server defaults it to 3."},
		"failedJobsHistoryLimit":     {Type: oam.PropertyTypeInteger, Description: "CronJob spec.failedJobsHistoryLimit: the number of finished failed Jobs kept. The API server defaults it to 1."},
	}, podTemplateBoundsOn("jobTemplate", false))
}

// ToApplicationConfig decodes an OAM cronjob component into a CronjobConfig,
// under the package's null contract and the strict decode every
// spec-projecting kind uses. What the CronJob may not hold is refused here,
// whatever the environment policy: see CronjobConfig.validate and
// cronjobDefaultedZeros. The CronJob's name is held to the length the API
// server creates one under.
func (h *CronjobHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	if err := validateCronJobName(component.ObjectName(), component.Name); err != nil {
		return nil, err
	}
	spec, props, err := decodeKindSpec[batchv1.CronJobSpec](component.Properties, "batch/v1 CronJobSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, cronjobDefaultedZeros(spec)); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, jobTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &CronjobConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
		Namespace:  namespace,
		Spec:       *spec,
	}
	if _, err := cfg.validate(component.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// cronjobDefaultedZeros is the defaulted-zero list of a CronJobSpec: the pod
// spec's list under jobTemplate.spec.template.spec, and concurrencyPolicy,
// which the encoding omits when empty and the API server then defaults to
// Allow. Every other field of the spec carries its zero: the pointers, and the
// schedule the config refuses when it is empty.
func cronjobDefaultedZeros(spec *batchv1.CronJobSpec) defaultedZeroFields {
	zeros := jobTemplateDefaultedZeros(&spec.JobTemplate)
	zeros.fields["concurrencyPolicy"] = `"Allow"`
	return zeros
}

// CronjobConfig implements stack.ApplicationConfig for cronjob components.
// Spec is the decoded CronJobSpec exactly as authored.
type CronjobConfig struct {
	Name string
	// ObjectName names the CronJob (oam.Component.ObjectName); its labels keep
	// Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the CronJob
	// (oam.Component.ObjectMetadata). They go on the CronJob's own metadata:
	// the job template keeps its own, and the pod template the labels of the
	// kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      batchv1.CronJobSpec
}

// validate refuses a spec the CronJob cannot be emitted from and returns the
// labels of the pod template of its job template. The schedule must be one the
// API server reads, the time zone a named IANA zone, the job template no
// selector (the API server generates it), the pod template
// validateBatchPodTemplate, and its labels podTemplateAppLabelsAt. The API's
// other value rules are left to the API server.
func (c *CronjobConfig) validate(component string) (map[string]string, error) {
	if c.Spec.Schedule == "" {
		return nil, errors.New("schedule: required (a 5-field cron expression, an @-descriptor, or \"@every <duration>\")")
	}
	if err := validateCronSchedule(c.Spec.Schedule); err != nil {
		return nil, err
	}
	if err := validateCronJobTimeZone(c.Spec.TimeZone); err != nil {
		return nil, err
	}
	job := &c.Spec.JobTemplate.Spec
	if job.Selector != nil {
		return nil, errors.New("jobTemplate.spec.selector: not supported — the API server generates the selector of a CronJob's Job and refuses an authored one; select the pods by the labels of jobTemplate.spec.template.metadata")
	}
	if job.ManualSelector != nil && *job.ManualSelector {
		return nil, errors.New("jobTemplate.spec.manualSelector: not supported — the API server allows only unset or false on a CronJob's job template, since it generates the selector")
	}
	if err := validateBatchPodTemplate("jobTemplate.spec.template.", &job.Template); err != nil {
		return nil, err
	}
	return podTemplateAppLabelsAt("jobTemplate.spec.template", component, job.Template.Labels)
}

// validateCronJobTimeZone refuses a time zone the API server refuses. An unset
// one is the API's own default. "Local" is refused case-insensitively:
// time.LoadLocation("Local") succeeds with the process's own zone, which
// Kubernetes' CronJob validation rejects as server-dependent, and the empty
// string is refused because time.LoadLocation("") would read it as UTC, which
// the author almost certainly did not intend.
func validateCronJobTimeZone(tz *string) error {
	if tz == nil {
		return nil
	}
	if *tz == "" {
		return errors.New("timeZone: must not be an empty string; omit the property instead")
	}
	if strings.EqualFold(*tz, "Local") {
		return errors.Errorf("timeZone: %q is not a valid IANA time zone name (Kubernetes rejects \"Local\" as server-dependent)", *tz)
	}
	if _, err := time.LoadLocation(*tz); err != nil {
		return errors.Errorf("timeZone: %q is not a valid IANA time zone name: %v", *tz, err)
	}
	return nil
}

// ServiceAccountName implements oam.ServiceAccountNamer: the account the pods
// of the CronJob's Jobs run as, read from the pod template as the pod kind
// reads its spec. No account is generated, so "" means the namespace's default
// account.
func (c *CronjobConfig) ServiceAccountName() (string, bool) {
	spec := &c.Spec.JobTemplate.Spec.Template.Spec
	if spec.ServiceAccountName != "" {
		return spec.ServiceAccountName, true
	}
	return spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the pod template of the CronJob's job template to the
// environment policy (enforcePodTemplatePolicy). A CronJob has no replicas, so
// no replica maximum applies, and it fills no default. A nil policy checks
// nothing.
func (c *CronjobConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforcePodTemplatePolicy("jobTemplate.spec.template.spec", &c.Spec.JobTemplate.Spec.Template.Spec, p)
}

// Generate emits the CronJob, and nothing beside it: no ServiceAccount and no
// claim (go-kure/launcher#702). It is kure's identity-only constructor, a deep
// copy of the spec, and the `app` label on the pod template of the job
// template. The parse-time refusals the typed spec can show are repeated,
// since the config is exported and the label and the name are valued from
// app.Name.
func (c *CronjobConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	name := kindObjectName(c.ObjectName, app.Name)
	if err := validateCronJobName(name, app.Name); err != nil {
		return nil, err
	}
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	cj := kubernetes.CreateCronJob(name, app.Namespace)
	c.Spec.DeepCopyInto(&cj.Spec)
	cj.Spec.JobTemplate.Spec.Template.Labels = labelled
	return kindObject(cj, c.Metadata)
}
