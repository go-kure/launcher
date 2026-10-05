package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// HorizontalPodAutoscalerHandler handles OAM horizontalpodautoscaler
// components: the kind-named projection of an autoscaling/v2
// HorizontalPodAutoscaler (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// autoscalingv2.HorizontalPodAutoscalerSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the HorizontalPodAutoscaler, named after
// the component unless `objectName` names it, in the build namespace, and
// nothing else. `scaleTargetRef` is the author's: launcher points it at no
// component and does not check that its target exists. The `scaler` trait is
// the autoscaler launcher derives for a workload; this kind is the authored
// object. TestCoreKindSchemas_CoverSpec keeps the published key set equal to
// the upstream json tags.
type HorizontalPodAutoscalerHandler struct{}

// CanHandle returns true for the horizontalpodautoscaler component type.
func (h *HorizontalPodAutoscalerHandler) CanHandle(componentType string) bool {
	return componentType == "horizontalpodautoscaler"
}

// PropertySchema declares every top-level
// autoscalingv2.HorizontalPodAutoscalerSpec field by its json name. Structured
// fields are open objects whose content is checked by the strict decode, not
// by this schema.
func (h *HorizontalPodAutoscalerHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"scaleTargetRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. HorizontalPodAutoscaler spec.scaleTargetRef: the object to scale, by kind, name and apiVersion; kind and name are required by the API. Decoded strictly into the Kubernetes API type: see CrossVersionObjectReference in the Kubernetes API reference.",
		},
		"minReplicas": {
			Type:        oam.PropertyTypeInteger,
			Description: "HorizontalPodAutoscaler spec.minReplicas: the lower limit of the replica count. The API server defaults it to 1.",
		},
		"maxReplicas": {
			Type: oam.PropertyTypeInteger, Required: true,
			Description: "Required. HorizontalPodAutoscaler spec.maxReplicas: the upper limit of the replica count, at least 1 and not below minReplicas. Held to the EnvironmentPolicy replica maximum.",
		},
		"metrics": {
			Type:        oam.PropertyTypeArray,
			Description: "HorizontalPodAutoscaler spec.metrics: the metrics the desired replica count is calculated from; the largest result is used. Unset, the API server scales on 80% average CPU utilization.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One metric: type (Resource, ContainerResource, Pods, Object or External) and the source of that name. Decoded strictly into the Kubernetes API type: see MetricSpec in the Kubernetes API reference.",
			},
		},
		"behavior": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "HorizontalPodAutoscaler spec.behavior: the scaling rules per direction (scaleUp, scaleDown). Decoded strictly into the Kubernetes API type: see HorizontalPodAutoscalerBehavior in the Kubernetes API reference.",
		},
	}
}

// ToApplicationConfig decodes an OAM horizontalpodautoscaler component into a
// HorizontalPodAutoscalerConfig, under the package's null contract and the
// strict decode every spec-projecting kind uses.
func (h *HorizontalPodAutoscalerHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[autoscalingv2.HorizontalPodAutoscalerSpec](component.Properties, "autoscaling/v2 HorizontalPodAutoscalerSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	cfg := &HorizontalPodAutoscalerConfig{Name: component.Name, ObjectName: componentObjectName(component), Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// HorizontalPodAutoscalerConfig implements stack.ApplicationConfig for
// horizontalpodautoscaler components. Spec is the decoded
// HorizontalPodAutoscalerSpec exactly as authored.
type HorizontalPodAutoscalerConfig struct {
	Name string
	// ObjectName names the HorizontalPodAutoscaler (oam.Component.ObjectName).
	// Empty for the application's name.
	ObjectName string
	Namespace  string
	Spec       autoscalingv2.HorizontalPodAutoscalerSpec
}

// validate refuses a spec without a top-level field the API server refuses an
// autoscaler without. The Go type cannot omit either, so an unauthored
// scaleTargetRef would be written with an empty kind and name and an
// unauthored maxReplicas as 0. The API's other value rules, those inside
// scaleTargetRef, metrics and behavior included, are left to the API server.
func (c *HorizontalPodAutoscalerConfig) validate() error {
	if c.Spec.ScaleTargetRef == (autoscalingv2.CrossVersionObjectReference{}) {
		return errors.New("scaleTargetRef: required (the object to scale: its kind, name and apiVersion)")
	}
	if c.Spec.MaxReplicas == 0 {
		return errors.New("maxReplicas: required (the upper limit of the replica count, at least 1)")
	}
	return nil
}

// ApplyPolicy holds maxReplicas to the environment policy's replica maximum,
// as the scaler trait holds its own and the rendered-object check holds a
// HorizontalPodAutoscaler a chart renders, a passthrough component holds or a
// manifests source yields. It fills no default. A nil policy checks nothing.
func (c *HorizontalPodAutoscalerConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	if err := enforceMaxReplicas(c.Spec.MaxReplicas, p.MaxReplicas()); err != nil {
		return errors.Wrap(err, "maxReplicas")
	}
	return nil
}

// Generate emits the HorizontalPodAutoscaler: kure's identity-only constructor
// plus a deep copy of the spec. The parse-time refusals are repeated, since
// the config is exported.
func (c *HorizontalPodAutoscalerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	hpa := kubernetes.CreateHorizontalPodAutoscaler(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&hpa.Spec)
	obj := client.Object(hpa)
	return []*client.Object{&obj}, nil
}
