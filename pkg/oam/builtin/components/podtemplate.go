package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// PodTemplateHandler handles OAM podtemplate components: the kind-named
// projection of a v1 PodTemplate (go-kure/launcher#790).
//
// A PodTemplate has no spec: beside its identity it holds one field,
// `template`, and that is the component's one property, decoded strictly
// (decodeKindSpec). It emits the PodTemplate, named after the component in the
// build namespace, with the authored template. Nothing is added: no label, no
// annotation and no default of launcher's.
//
// A PodTemplate is stored, not run: no controller creates pods from it. So the
// template gets no `app` label, the component is not a target of the traits
// that change a pod spec, and it reports no ServiceAccount. Its pod spec is
// still held to what a pod may hold and to the environment policy, since
// whatever reads the template creates pods from it.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags, less the object's own identity.
type PodTemplateHandler struct{}

// CanHandle returns true for the podtemplate component type.
func (h *PodTemplateHandler) CanHandle(componentType string) bool {
	return componentType == "podtemplate"
}

// podTemplateProperties is what a podtemplate component authors of a
// corev1.PodTemplate: its `template` field. The object's kind, apiVersion and
// metadata are launcher's to set, so the strict decode refuses them as unknown
// keys. TestPodTemplateProperties_MatchUpstream holds the field to the upstream
// one.
type podTemplateProperties struct {
	Template corev1.PodTemplateSpec `json:"template,omitempty"`
}

// PropertySchema declares the one authorable corev1.PodTemplate field by its
// json name: an open object whose content is checked by the strict decode, not
// by this schema.
func (h *PodTemplateHandler) PropertySchema() map[string]oam.PropertySchema {
	template := podTemplateSchema("Required. PodTemplate template: the pod the template describes. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy. metadata is carried as authored; no label is added.")
	template.Required = true
	return map[string]oam.PropertySchema{"template": template}
}

// ToApplicationConfig decodes an OAM podtemplate component into a
// PodTemplateConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. What a pod may not hold is refused here,
// whatever the environment policy: see validateAuthoredPodSpec,
// podTemplateDefaultedZeros and podTemplateLabelSelectorRequired.
func (h *PodTemplateHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	authored, props, err := decodeKindSpec[podTemplateProperties](component.Properties, "v1 PodTemplate (a podtemplate component authors its `template` only)")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, authored, podTemplateDefaultedZeros()); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, podTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &PodTemplateConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Template: authored.Template}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// PodTemplateConfig implements stack.ApplicationConfig for podtemplate
// components. Template is the decoded PodTemplateSpec exactly as authored.
//
// It does not implement oam.ServiceAccountNamer: a PodTemplate runs no pods,
// so there is no account of its own for a trait to bind.
type PodTemplateConfig struct {
	Name string
	// ObjectName names the PodTemplate (oam.Component.ObjectName). Empty for
	// the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the PodTemplate
	// (oam.Component.ObjectMetadata). They go on the PodTemplate's own
	// metadata: its pod template keeps the labels of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Template  corev1.PodTemplateSpec
}

// validate refuses a template the PodTemplate cannot be emitted from: what a
// pod authored through a kind component may not hold
// (validateAuthoredPodSpec), an unauthored template included, which has no
// containers. activeDeadlineSeconds is allowed, unlike on a controller's
// template: the API server accepts it on a PodTemplate. The API's other value
// rules are left to the API server.
func (c *PodTemplateConfig) validate() error {
	return validateAuthoredPodSpec("template.spec.", &c.Template.Spec)
}

// ApplyPolicy holds the template's pod spec to the environment policy, through
// the check the rendered-object check runs on a PodTemplate a chart renders, a
// passthrough component holds or a manifests source yields
// (enforcePodTemplatePolicy). It fills no default. A nil policy checks
// nothing.
func (c *PodTemplateConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforcePodTemplatePolicy("template.spec", &c.Template.Spec, p)
}

// Generate emits the PodTemplate: kure's identity-only constructor and a deep
// copy of the template. The parse-time refusals the typed template can show are
// repeated, since the config is exported.
func (c *PodTemplateConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	pt := kubernetes.CreatePodTemplate(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Template.DeepCopyInto(&pt.Template)
	return kindObject(pt, c.Metadata)
}
