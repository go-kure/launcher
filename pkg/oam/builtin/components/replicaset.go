package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ReplicaSetHandler handles OAM replicaset components: the kind-named
// projection of an apps/v1 ReplicaSet (go-kure/launcher#790).
//
// Its properties are the top-level json fields of appsv1.ReplicaSetSpec,
// decoded strictly (decodeKindSpec). It emits the ReplicaSet, named after the
// component in the build namespace, with the authored spec. One thing is
// added: the `app` label on the pod template, beside the authored labels,
// which every workload kind gives its pods and launcher's traits and Services
// select on (podTemplateAppLabels). The selector stays as authored, and the
// object itself gets no label, no annotation and no default of launcher's.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ReplicaSetHandler struct{}

// CanHandle returns true for the replicaset component type.
func (h *ReplicaSetHandler) CanHandle(componentType string) bool {
	return componentType == "replicaset"
}

// PropertySchema declares every top-level appsv1.ReplicaSetSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *ReplicaSetHandler) PropertySchema() map[string]oam.PropertySchema {
	selector := podObjectSchema("Required. ReplicaSet spec.selector: the label selector over the pods the ReplicaSet owns. It must match the template's labels, and it is immutable once the object exists. A selector that rules out the `app` label launcher adds to the template is refused.", "LabelSelector")
	selector.Required = true
	template := podTemplateSchema("Required. ReplicaSet spec.template: the pods the ReplicaSet creates. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; activeDeadlineSeconds is refused. metadata.labels gains the `app` label; an authored `app` with another value is refused.")
	template.Required = true
	return map[string]oam.PropertySchema{
		"replicas":        {Type: oam.PropertyTypeInteger, Description: "ReplicaSet spec.replicas: the number of pods. The API server defaults it to 1. Held to the environment policy's replica maximum; no policy default is filled."},
		"minReadySeconds": {Type: oam.PropertyTypeInteger, Description: "ReplicaSet spec.minReadySeconds: seconds a new pod must be ready, without a container crashing, to count as available. The API server defaults it to 0."},
		"selector":        selector,
		"template":        template,
	}
}

// ToApplicationConfig decodes an OAM replicaset component into a
// ReplicaSetConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. What the ReplicaSet may not hold is refused
// here, whatever the environment policy: see ReplicaSetConfig.validate,
// podTemplateDefaultedZeros and podTemplateLabelSelectorRequired. `selector`
// is not in that list: validate refuses an expression of it without a key or
// an operator, with every other selector apimachinery cannot read
// (refuseSelectorAgainstAppLabel).
func (h *ReplicaSetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[appsv1.ReplicaSetSpec](component.Properties, "apps/v1 ReplicaSetSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podTemplateDefaultedZeros(&spec.Template)); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(props, podTemplateLabelSelectorRequired()); err != nil {
		return nil, err
	}
	cfg := &ReplicaSetConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if _, err := cfg.validate(component.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ReplicaSetConfig implements stack.ApplicationConfig for replicaset
// components. Spec is the decoded ReplicaSetSpec exactly as authored.
type ReplicaSetConfig struct {
	Name string
	// ObjectName names the ReplicaSet (oam.Component.ObjectName); its labels
	// keep Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the ReplicaSet
	// (oam.Component.ObjectMetadata). They go on the ReplicaSet's own
	// metadata: its pod template keeps the labels of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      appsv1.ReplicaSetSpec
}

// validate refuses a spec the ReplicaSet cannot be emitted from and returns
// the labels of its pod template. selector must be authored: the Go type
// cannot omit it, so an unset one would be written as null, and launcher
// derives none. The template is held to validateControllerPodTemplate, its
// labels to podTemplateAppLabels, and the selector must keep matching the
// template once the `app` label is on it (refuseSelectorAgainstAppLabel).
// The API's other value rules are left to the API server.
func (c *ReplicaSetConfig) validate(component string) (map[string]string, error) {
	if c.Spec.Selector == nil {
		return nil, errors.New("selector: required (the label selector over the pods the ReplicaSet owns; it must match the template's labels)")
	}
	if err := validateControllerPodTemplate(&c.Spec.Template); err != nil {
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
// ReplicaSet's pods run as, read from the template as the pod kind reads its
// spec. No account is generated, so "" means the namespace's default account.
func (c *ReplicaSetConfig) ServiceAccountName() (string, bool) {
	if name := c.Spec.Template.Spec.ServiceAccountName; name != "" {
		return name, true
	}
	return c.Spec.Template.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the ReplicaSet to the environment policy, as the
// rendered-object check holds one a chart renders, a passthrough component
// holds or a manifests source yields: replicas (one when unset, the API
// server's default) to the replica maximum, and the pod template to
// enforcePodTemplatePolicy. It fills no default: an unset replicas stays
// unset. A nil policy checks nothing.
func (c *ReplicaSetConfig) ApplyPolicy(p oam.Policy) error {
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
	return enforcePodTemplatePolicy("template.spec", &c.Spec.Template.Spec, p)
}

// Generate emits the ReplicaSet: kure's identity-only constructor, a deep copy
// of the spec, and the `app` label on the pod template. The parse-time
// refusals the typed spec can show are repeated, since the config is exported
// and the label is valued from app.Name.
func (c *ReplicaSetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	rs := kubernetes.CreateReplicaSet(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&rs.Spec)
	rs.Spec.Template.Labels = labelled
	return kindObject(rs, c.Metadata)
}
