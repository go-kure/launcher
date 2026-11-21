package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// DaemonsetHandler handles OAM daemonset components: the kind-named projection
// of an apps/v1 DaemonSet (go-kure/launcher#1629).
//
// Its properties are the top-level json fields of appsv1.DaemonSetSpec,
// decoded strictly (decodeKindSpec), as the deployment kind's are. It emits the
// DaemonSet, named after the component in the build namespace, with the
// authored spec. One thing is added: the `app` label on the pod template,
// beside the authored labels, which every workload kind gives its pods and
// launcher's traits and Services select on (podTemplateAppLabels). The selector
// stays as authored, and the object itself gets no label, no annotation and no
// default of launcher's. The reshaped form, with the main container at the top
// level, belongs to `webservice` and `worker`.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type DaemonsetHandler struct{}

// CanHandle returns true for daemonset component type.
func (h *DaemonsetHandler) CanHandle(componentType string) bool {
	return componentType == "daemonset"
}

// PropertySchema declares every top-level appsv1.DaemonSetSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *DaemonsetHandler) PropertySchema() map[string]oam.PropertySchema {
	selector := podObjectSchema("Required. DaemonSet spec.selector: the label selector over the pods the DaemonSet owns. It must match the template's labels, and it is immutable once the object exists. A selector that rules out the `app` label launcher adds to the template is refused.", "LabelSelector")
	selector.Required = true
	template := podTemplateSchema("Required. DaemonSet spec.template: the pods the DaemonSet runs, one per node it schedules to. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; activeDeadlineSeconds is refused. metadata.labels gains the `app` label; an authored `app` with another value is refused.")
	template.Required = true
	return oam.DeclareBounds(map[string]oam.PropertySchema{
		"selector":             selector,
		"template":             template,
		"updateStrategy":       podObjectSchema("DaemonSet spec.updateStrategy: `type` (RollingUpdate or OnDelete) and `rollingUpdate` (`maxUnavailable`, `maxSurge`). The API server defaults it to RollingUpdate.", "DaemonSetUpdateStrategy"),
		"minReadySeconds":      {Type: oam.PropertyTypeInteger, Description: "DaemonSet spec.minReadySeconds: seconds a new pod must be ready, without a container crashing, to count as available. The API server defaults it to 0."},
		"revisionHistoryLimit": {Type: oam.PropertyTypeInteger, Description: "DaemonSet spec.revisionHistoryLimit: the number of old ControllerRevisions kept for rollback. The API server defaults it to 10."},
	}, podTemplateBounds(false))
}

// Endpoints implements oam.EndpointProvider: one endpoint per port the first
// container of the template declares (containerPortEndpoints). Endpoints are
// collected apart from the build, so the handler's own parse runs here too: a
// component it refuses has no endpoint, in the parse's words.
func (h *DaemonsetHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	query := component.WithImageLevel(oam.ImageLevelAny)
	cfg, err := h.ToApplicationConfig(&query, "")
	if err != nil {
		return nil, err
	}
	containers := cfg.(*DaemonsetConfig).Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return nil, nil
	}
	return containerPortEndpoints(component.Name, containers[0].Ports), nil
}

// ToApplicationConfig decodes an OAM daemonset component into a
// DaemonsetConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. What the DaemonSet may not hold is refused
// here, whatever the environment policy: see DaemonsetConfig.validate,
// podTemplateDefaultedZeros and podTemplateLabelSelectorRequired.
func (h *DaemonsetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	// The images are held to the component's level, which the config keeps for
	// Generate, where the refusals repeat (go-kure/launcher#1644).
	level := component.ImageLevel()
	spec, props, err := decodeKindSpec[appsv1.DaemonSetSpec](component.Properties, "apps/v1 DaemonSetSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podTemplateDefaultedZeros(&spec.Template)); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, podTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &DaemonsetConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
		Namespace:  namespace,
		Spec:       *spec,

		imageLevel: level,
	}
	if _, err := cfg.validate(component.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DaemonsetConfig implements stack.ApplicationConfig for daemonset components.
// Spec is the decoded DaemonSetSpec exactly as authored.
type DaemonsetConfig struct {
	Name string
	// ObjectName names the DaemonSet (oam.Component.ObjectName); its labels
	// keep Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the DaemonSet
	// (oam.Component.ObjectMetadata). They go on the DaemonSet's own metadata:
	// its pod template keeps the labels of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      appsv1.DaemonSetSpec

	// imageLevel is the level the images were held to at parse, which validate
	// holds them to again; unset is oam.ImageLevelTagged.
	imageLevel oam.ImageReferenceLevel
}

// validate refuses a spec the DaemonSet cannot be emitted from and returns the
// labels of its pod template. selector must be authored: the Go type cannot
// omit it, so an unset one would be written as null, and launcher derives none.
// The template is held to validateControllerPodTemplateAt, its labels to
// podTemplateAppLabels, and the selector must keep matching the template once
// the `app` label is on it (refuseSelectorAgainstAppLabel). The API's other
// value rules are left to the API server.
func (c *DaemonsetConfig) validate(component string) (map[string]string, error) {
	if c.Spec.Selector == nil {
		return nil, errors.New("selector: required (the label selector over the pods the DaemonSet owns; it must match the template's labels)")
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
// DaemonSet's pods run as, read from the template as the pod kind reads its
// spec. No account is generated, so "" means the namespace's default account.
func (c *DaemonsetConfig) ServiceAccountName() (string, bool) {
	if name := c.Spec.Template.Spec.ServiceAccountName; name != "" {
		return name, true
	}
	return c.Spec.Template.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the pod template to the environment policy
// (enforcePodTemplatePolicy), as the rendered-object check holds one a chart
// renders. A DaemonSet has no replica count, and no default is filled. A nil
// policy checks nothing.
func (c *DaemonsetConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforcePodTemplatePolicy("template.spec", &c.Spec.Template.Spec, p)
}

// Generate emits the DaemonSet: kure's identity-only constructor, a deep copy
// of the spec, and the `app` label on the pod template. The parse-time
// refusals the typed spec can show are repeated, since the config is exported
// and the label is valued from app.Name. No ServiceAccount, claim or Service is
// emitted beside it (go-kure/launcher#702): an authored `service` component
// exposes the pods.
func (c *DaemonsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	ds := kubernetes.CreateDaemonSet(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&ds.Spec)
	ds.Spec.Template.Labels = labelled
	return kindObject(ds, c.Metadata)
}
