package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ReplicationControllerHandler handles OAM replicationcontroller components:
// the kind-named projection of a v1 ReplicationController
// (go-kure/launcher#790).
//
// Its properties are the top-level json fields of
// corev1.ReplicationControllerSpec, decoded strictly (decodeKindSpec). It emits
// the ReplicationController, named after the component in the build namespace,
// with the authored spec. One thing is added, as on the replicaset kind: the
// `app` label on the pod template, beside the authored labels
// (podTemplateAppLabels). The selector stays as authored, an unset one
// included, and the object itself gets no label, no annotation and no default
// of launcher's. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type ReplicationControllerHandler struct{}

// CanHandle returns true for the replicationcontroller component type.
func (h *ReplicationControllerHandler) CanHandle(componentType string) bool {
	return componentType == "replicationcontroller"
}

// PropertySchema declares every top-level corev1.ReplicationControllerSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *ReplicationControllerHandler) PropertySchema() map[string]oam.PropertySchema {
	template := podTemplateSchema("Required. ReplicationController spec.template: the pods the controller creates. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; activeDeadlineSeconds is refused. metadata.labels gains the `app` label; an authored `app` with another value is refused.")
	template.Required = true
	return map[string]oam.PropertySchema{
		"replicas":        {Type: oam.PropertyTypeInteger, Description: "ReplicationController spec.replicas: the number of pods. The API server defaults it to 1. Held to the environment policy's replica maximum; no policy default is filled."},
		"minReadySeconds": {Type: oam.PropertyTypeInteger, Description: "ReplicationController spec.minReadySeconds: seconds a new pod must be ready, without a container crashing, to count as available. The API server defaults it to 0."},
		"selector":        {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "ReplicationController spec.selector: the labels of the pods the controller owns, as a map of label to value. It must match the template's labels. Unset, the API server defaults it to the template's labels, the `app` label launcher adds included."},
		"template":        template,
	}
}

// ToApplicationConfig decodes an OAM replicationcontroller component into a
// ReplicationControllerConfig, under the package's null contract and the
// strict decode every spec-projecting kind uses. What the controller may not
// hold is refused here, whatever the environment policy: see
// ReplicationControllerConfig.validate, podTemplateDefaultedZeros and
// podTemplateLabelSelectorRequired.
func (h *ReplicationControllerHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[corev1.ReplicationControllerSpec](component.Properties, "v1 ReplicationControllerSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podTemplateDefaultedZeros(spec.Template)); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, podTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &ReplicationControllerConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if _, err := cfg.validate(component.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ReplicationControllerConfig implements stack.ApplicationConfig for
// replicationcontroller components. Spec is the decoded
// ReplicationControllerSpec exactly as authored.
type ReplicationControllerConfig struct {
	Name string
	// ObjectName names the ReplicationController (oam.Component.ObjectName);
	// its labels keep Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the
	// ReplicationController (oam.Component.ObjectMetadata). They go on the
	// ReplicationController's own metadata: its pod template keeps the labels
	// of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      corev1.ReplicationControllerSpec
}

// validate refuses a spec the ReplicationController cannot be emitted from and
// returns the labels of its pod template. template must be authored: there is
// no pod to describe without it, and the API server requires it. The template
// is held to validateControllerPodTemplate and its labels to
// podTemplateAppLabels.
//
// The selector needs no check against the `app` label, unlike a ReplicaSet's:
// it is a map of label to value, which a label added to the template cannot
// stop from matching. The API's other value rules are left to the API server.
func (c *ReplicationControllerConfig) validate(component string) (map[string]string, error) {
	if c.Spec.Template == nil {
		return nil, errors.New("template: required (the pods the ReplicationController creates)")
	}
	if err := validateControllerPodTemplate(c.Spec.Template); err != nil {
		return nil, err
	}
	return podTemplateAppLabels(component, c.Spec.Template.Labels)
}

// ServiceAccountName implements oam.ServiceAccountNamer: the account the
// controller's pods run as, read from the template as the pod kind reads its
// spec. No account is generated, so "" means the namespace's default account.
func (c *ReplicationControllerConfig) ServiceAccountName() (string, bool) {
	if c.Spec.Template == nil {
		return "", true
	}
	if name := c.Spec.Template.Spec.ServiceAccountName; name != "" {
		return name, true
	}
	return c.Spec.Template.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the ReplicationController to the environment policy, as
// the rendered-object check holds one a chart renders, a passthrough component
// holds or a manifests source yields: replicas (one when unset, the API
// server's default) to the replica maximum, and the pod template to
// enforcePodTemplatePolicy. It fills no default: an unset replicas stays
// unset. A nil policy checks nothing.
func (c *ReplicationControllerConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	replicas := int32(1)
	if c.Spec.Replicas != nil {
		replicas = *c.Spec.Replicas
	}
	if err := enforceMaxReplicas(replicas, p.MaxReplicas()); err != nil {
		return err
	}
	if c.Spec.Template == nil {
		return nil
	}
	return enforcePodTemplatePolicy("template.spec", &c.Spec.Template.Spec, p)
}

// Generate emits the ReplicationController: kure's identity-only constructor,
// a deep copy of the spec, and the `app` label on the pod template. The
// parse-time refusals the typed spec can show are repeated, since the config
// is exported and the label is valued from app.Name.
func (c *ReplicationControllerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	rc := kubernetes.CreateReplicationController(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&rc.Spec)
	rc.Spec.Template.Labels = labelled
	return kindObject(rc, c.Metadata)
}
