package components

import (
	"slices"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// DeploymentHandler handles OAM deployment components: the kind-named
// projection of an apps/v1 Deployment (go-kure/launcher#1629).
//
// Its properties are the top-level json fields of appsv1.DeploymentSpec,
// decoded strictly (decodeKindSpec), as the replicaset kind's are. It emits the
// Deployment, named after the component in the build namespace, with the
// authored spec. One thing is added: the `app` label on the pod template,
// beside the authored labels, which every workload kind gives its pods and
// launcher's traits and Services select on (podTemplateAppLabels). The selector
// stays as authored, and the object itself gets no label, no annotation and no
// default of launcher's. The reshaped form, with the main container at the top
// level, belongs to `webservice` and `worker` (RoleDeploymentHandler).
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type DeploymentHandler struct{}

// CanHandle returns true for the deployment component type, never for the role
// member type.
func (h *DeploymentHandler) CanHandle(componentType string) bool {
	return componentType == "deployment"
}

// PropertySchema declares every top-level appsv1.DeploymentSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *DeploymentHandler) PropertySchema() map[string]oam.PropertySchema {
	selector := podObjectSchema("Required. Deployment spec.selector: the label selector over the pods the Deployment owns. It must match the template's labels, and it is immutable once the object exists. A selector that rules out the `app` label launcher adds to the template is refused.", "LabelSelector")
	selector.Required = true
	template := podTemplateSchema("Required. Deployment spec.template: the pods the Deployment creates. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; activeDeadlineSeconds is refused. metadata.labels gains the `app` label; an authored `app` with another value is refused.")
	template.Required = true
	return oam.DeclareBounds(map[string]oam.PropertySchema{
		"replicas":                {Type: oam.PropertyTypeInteger, Description: "Deployment spec.replicas: the number of pods. The API server defaults it to 1. Held to the environment policy's replica maximum; no policy default is filled. Left out of the object when the component carries a scaler trait, whose HorizontalPodAutoscaler owns the count."},
		"selector":                selector,
		"template":                template,
		"strategy":                podObjectSchema("Deployment spec.strategy: `type` (Recreate or RollingUpdate) and `rollingUpdate` (`maxUnavailable`, `maxSurge`). The API server defaults it to RollingUpdate.", "DeploymentStrategy"),
		"minReadySeconds":         {Type: oam.PropertyTypeInteger, Description: "Deployment spec.minReadySeconds: seconds a new pod must be ready, without a container crashing, to count as available. The API server defaults it to 0."},
		"revisionHistoryLimit":    {Type: oam.PropertyTypeInteger, Description: "Deployment spec.revisionHistoryLimit: the number of old ReplicaSets kept for rollback. The API server defaults it to 10."},
		"paused":                  {Type: oam.PropertyTypeBoolean, Description: "Deployment spec.paused: true pauses the rollout of changes to the pod template."},
		"progressDeadlineSeconds": {Type: oam.PropertyTypeInteger, Description: "Deployment spec.progressDeadlineSeconds: seconds the Deployment controller waits for progress before it reports the rollout as failed. The API server defaults it to 600."},
	}, podTemplateBounds(true))
}

// Endpoints implements oam.EndpointProvider: one endpoint per port the first
// container of the template declares (containerPortEndpoints). Endpoints are
// collected apart from the build, so the handler's own parse runs here too: a
// component it refuses has no endpoint, in the parse's words.
func (h *DeploymentHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	query := component.WithImageLevel(oam.ImageLevelAny)
	cfg, err := h.ToApplicationConfig(&query, "")
	if err != nil {
		return nil, err
	}
	containers := cfg.(*DeploymentConfig).Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return nil, nil
	}
	return containerPortEndpoints(component.Name, containers[0].Ports), nil
}

// ToApplicationConfig decodes an OAM deployment component into a
// DeploymentConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. What the Deployment may not hold is refused
// here, whatever the environment policy: see DeploymentConfig.validate,
// podTemplateDefaultedZeros and podTemplateLabelSelectorRequired.
func (h *DeploymentHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	// The images are held to the component's level, which the config keeps for
	// Generate, where the refusals repeat (go-kure/launcher#1644).
	level := component.ImageLevel()
	spec, props, err := decodeKindSpec[appsv1.DeploymentSpec](component.Properties, "apps/v1 DeploymentSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podTemplateDefaultedZeros(&spec.Template)); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, podTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &DeploymentConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
		Namespace:  namespace,
		Spec:       *spec,
		scaled:     slices.ContainsFunc(component.Traits, func(t oam.Trait) bool { return t.Type == "scaler" }),

		imageLevel: level,
	}
	if _, err := cfg.validate(component.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DeploymentConfig implements stack.ApplicationConfig for deployment
// components. Spec is the decoded DeploymentSpec exactly as authored.
type DeploymentConfig struct {
	Name string
	// ObjectName names the Deployment (oam.Component.ObjectName); its labels
	// keep Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Deployment
	// (oam.Component.ObjectMetadata). They go on the Deployment's own metadata:
	// its pod template keeps the labels of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      appsv1.DeploymentSpec

	// scaled is true when the component carries a scaler trait. The
	// HorizontalPodAutoscaler then owns the replica count and the Deployment is
	// built without spec.replicas, whichever order the traits are declared in
	// (go-kure/launcher#1179); the scaler enforces the replica maximum on its
	// own maxReplicas.
	scaled bool

	// imageLevel is the level the images were held to at parse, which validate
	// holds them to again; unset is oam.ImageLevelTagged.
	imageLevel oam.ImageReferenceLevel
}

// validate refuses a spec the Deployment cannot be emitted from and returns the
// labels of its pod template. selector must be authored: the Go type cannot
// omit it, so an unset one would be written as null, and launcher derives none.
// The template is held to validateControllerPodTemplate, its labels to
// podTemplateAppLabels, and the selector must keep matching the template once
// the `app` label is on it (refuseSelectorAgainstAppLabel). The API's other
// value rules are left to the API server.
func (c *DeploymentConfig) validate(component string) (map[string]string, error) {
	if c.Spec.Selector == nil {
		return nil, errors.New("selector: required (the label selector over the pods the Deployment owns; it must match the template's labels)")
	}
	if err := validateControllerPodTemplateAt(&c.Spec.Template, imageLevelOrTagged(c.imageLevel)); err != nil {
		return nil, err
	}
	labelled, err := podTemplateAppLabels(component, c.Spec.Template.Labels)
	if err != nil {
		return nil, err
	}
	if err := refuseSelectorAgainstAppLabel(component, c.Spec.Selector, c.Spec.Template.Labels, labelled); err != nil {
		return nil, err
	}
	return labelled, nil
}

// ServiceAccountName implements oam.ServiceAccountNamer: the account the
// Deployment's pods run as, read from the template as the pod kind reads its
// spec. No account is generated, so "" means the namespace's default account.
func (c *DeploymentConfig) ServiceAccountName() (string, bool) {
	if name := c.Spec.Template.Spec.ServiceAccountName; name != "" {
		return name, true
	}
	return c.Spec.Template.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the Deployment to the environment policy, as the
// rendered-object check holds one a chart renders: replicas (one when unset,
// the API server's default) to the replica maximum, unless a scaler owns the
// count, and the pod template to enforcePodTemplatePolicy. It fills no default:
// an unset replicas stays unset. A nil policy checks nothing.
func (c *DeploymentConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	if !c.scaled {
		replicas := int32(1)
		if c.Spec.Replicas != nil {
			replicas = *c.Spec.Replicas
		}
		if err := enforceMaxReplicas(replicas, p.MaxReplicas()); err != nil {
			return err
		}
	}
	return enforcePodTemplatePolicy("template.spec", &c.Spec.Template.Spec, p)
}

// Generate emits the Deployment: kure's identity-only constructor, a deep copy
// of the spec, and the `app` label on the pod template. Under a scaler the
// Deployment is built without spec.replicas. The parse-time refusals the typed
// spec can show are repeated, since the config is exported and the label is
// valued from app.Name.
func (c *DeploymentConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	dep := kubernetes.CreateDeployment(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&dep.Spec)
	dep.Spec.Template.Labels = labelled
	if c.scaled {
		dep.Spec.Replicas = nil
	}
	return kindObject(dep, c.Metadata)
}
