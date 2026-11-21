package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// JobHandler handles OAM job components: the kind-named projection of a
// batch/v1 Job (go-kure/launcher#1629).
//
// Its properties are the top-level json fields of batchv1.JobSpec, decoded
// strictly (decodeKindSpec), as the replicaset kind's are. It emits the Job,
// named after the component in the build namespace, with the authored spec.
// One thing is added: the `app` label on the pod template, beside the authored
// labels, which every workload kind gives its pods and launcher's traits and
// Services select on (podTemplateAppLabels). The object itself gets no label,
// no annotation and no default of launcher's. A selector is the author's
// alone: the job controller generates one when none is written.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type JobHandler struct{}

// CanHandle returns true for the job component type.
func (h *JobHandler) CanHandle(componentType string) bool {
	return componentType == "job"
}

// PropertySchema declares every top-level batchv1.JobSpec field by its json
// name. Structured fields are open objects whose content is checked by the
// strict decode, not by this schema.
func (h *JobHandler) PropertySchema() map[string]oam.PropertySchema {
	template := podTemplateSchema("Required. Job spec.template: the pod the Job runs. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; restartPolicy must be Never or OnFailure, which the API server checks when the object is applied. metadata.labels gains the `app` label; an authored `app` with another value is refused.")
	template.Required = true
	return oam.DeclareBounds(map[string]oam.PropertySchema{
		"template":                template,
		"parallelism":             {Type: oam.PropertyTypeInteger, Description: "Job spec.parallelism: the number of pods that may run at once. The API server defaults it to 1."},
		"completions":             {Type: oam.PropertyTypeInteger, Description: "Job spec.completions: the number of pods that must finish successfully. An Indexed job requires it."},
		"activeDeadlineSeconds":   {Type: oam.PropertyTypeInteger, Description: "Job spec.activeDeadlineSeconds: seconds the Job may run before it is terminated."},
		"backoffLimit":            {Type: oam.PropertyTypeInteger, Description: "Job spec.backoffLimit: the number of retries before the Job is marked failed. The API server defaults it to 6."},
		"backoffLimitPerIndex":    {Type: oam.PropertyTypeInteger, Description: "Job spec.backoffLimitPerIndex: the retries of each index of an Indexed job."},
		"maxFailedIndexes":        {Type: oam.PropertyTypeInteger, Description: "Job spec.maxFailedIndexes: the number of failed indexes after which an Indexed job is marked failed."},
		"ttlSecondsAfterFinished": {Type: oam.PropertyTypeInteger, Description: "Job spec.ttlSecondsAfterFinished: seconds after which a finished Job is deleted."},
		"completionMode":          {Type: oam.PropertyTypeString, Description: "Job spec.completionMode: NonIndexed (the default) or Indexed."},
		"suspend":                 {Type: oam.PropertyTypeBoolean, Description: "Job spec.suspend: true creates the Job without running its pods."},
		"podReplacementPolicy":    {Type: oam.PropertyTypeString, Description: "Job spec.podReplacementPolicy: when a terminating pod is replaced, TerminatingOrFailed or Failed."},
		"managedBy":               {Type: oam.PropertyTypeString, Description: "Job spec.managedBy: the controller that syncs this Job, instead of the built-in one."},
		"manualSelector":          {Type: oam.PropertyTypeBoolean, Description: "Job spec.manualSelector: true takes the authored selector as it is. Without it the API server adds a controller-uid label to the selector and to the template's labels."},
		"selector":                podObjectSchema("Job spec.selector: the label selector over the pods the Job owns. Leave it out and the job controller generates one. An authored selector that rules out the `app` label launcher adds to the template is refused.", "LabelSelector"),
		"podFailurePolicy":        podObjectSchema("Job spec.podFailurePolicy: what the Job does about a failed pod, by exit code or pod condition.", "PodFailurePolicy"),
		"successPolicy":           podObjectSchema("Job spec.successPolicy: when an Indexed job counts as succeeded.", "SuccessPolicy"),
		"scheduling":              podObjectSchema("Job spec.scheduling: the pod placement of a suspended Job, which a controller may update before it starts.", "JobSchedulingConfiguration"),
	}, podTemplateBounds(false))
}

// ToApplicationConfig decodes an OAM job component into a JobConfig, under the
// package's null contract and the strict decode every spec-projecting kind
// uses. What the Job may not hold is refused here, whatever the environment
// policy: see JobConfig.validate, podTemplateDefaultedZeros and
// podTemplateLabelSelectorRequired. The Job's name is held to the length the
// API server creates one under and, on an Indexed job, to one its pods can be
// named after.
func (h *JobHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	// The images are held to the component's level, which the config keeps for
	// Generate, where the refusals repeat (go-kure/launcher#1644).
	level := component.ImageLevel()
	if err := validateJobName(component.ObjectName(), component.Name); err != nil {
		return nil, err
	}
	spec, props, err := decodeKindSpec[batchv1.JobSpec](component.Properties, "batch/v1 JobSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podTemplateDefaultedZeros(&spec.Template)); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, podTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &JobConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
		Namespace:  namespace,
		Spec:       *spec,

		imageLevel: level,
	}
	if err := validateJobNameAllowsCompletions(kindObjectName(cfg.ObjectName, component.Name), component.Name, cfg.Spec); err != nil {
		return nil, err
	}
	if _, err := cfg.validate(component.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// JobConfig implements stack.ApplicationConfig for job components. Spec is the
// decoded JobSpec exactly as authored.
type JobConfig struct {
	Name string
	// ObjectName names the Job (oam.Component.ObjectName); its labels keep
	// Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Job
	// (oam.Component.ObjectMetadata). They go on the Job's own metadata: its
	// pod template keeps the labels of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      batchv1.JobSpec

	// imageLevel is the level the images were held to at parse, which validate
	// holds them to again; unset is oam.ImageLevelTagged.
	imageLevel oam.ImageReferenceLevel
}

// validate refuses a spec the Job cannot be emitted from and returns the
// labels of its pod template. The template is held to
// validateBatchPodTemplateAt, its labels to podTemplateAppLabels, and an authored
// selector must keep matching the template once the `app` label is on it
// (refuseSelectorAgainstAppLabel). The API's other value rules are left to the
// API server.
func (c *JobConfig) validate(component string) (map[string]string, error) {
	if err := validateBatchPodTemplateAt("template.", &c.Spec.Template, imageLevelOrTagged(c.imageLevel)); err != nil {
		return nil, err
	}
	labelled, err := podTemplateAppLabels(component, c.Spec.Template.Labels)
	if err != nil {
		return nil, err
	}
	if c.Spec.Selector != nil {
		if err := refuseSelectorAgainstAppLabel(component, c.Spec.Selector, c.Spec.Template.Labels, labelled); err != nil {
			return nil, err
		}
	}
	return labelled, nil
}

// ServiceAccountName implements oam.ServiceAccountNamer: the account the Job's
// pods run as, read from the template as the pod kind reads its spec. No
// account is generated, so "" means the namespace's default account.
func (c *JobConfig) ServiceAccountName() (string, bool) {
	if name := c.Spec.Template.Spec.ServiceAccountName; name != "" {
		return name, true
	}
	return c.Spec.Template.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the Job's pod template to the environment policy
// (enforcePodTemplatePolicy). A Job has no replicas, so no replica maximum
// applies, and it fills no default. A nil policy checks nothing.
func (c *JobConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforcePodTemplatePolicy("template.spec", &c.Spec.Template.Spec, p)
}

// Generate emits the Job, and nothing beside it: no ServiceAccount and no
// claim (go-kure/launcher#702). It is kure's identity-only constructor, a deep
// copy of the spec, and the `app` label on the pod template. The parse-time
// refusals the typed spec can show are repeated, since the config is exported
// and the label and the name are valued from app.Name.
func (c *JobConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	name := kindObjectName(c.ObjectName, app.Name)
	if err := validateJobName(name, app.Name); err != nil {
		return nil, err
	}
	if err := validateJobNameAllowsCompletions(name, app.Name, c.Spec); err != nil {
		return nil, err
	}
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	job := kubernetes.CreateJob(name, app.Namespace)
	c.Spec.DeepCopyInto(&job.Spec)
	job.Spec.Template.Labels = labelled
	return kindObject(job, c.Metadata)
}
