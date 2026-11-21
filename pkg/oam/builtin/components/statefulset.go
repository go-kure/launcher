package components

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// StatefulsetHandler handles OAM statefulset components: the kind-named
// projection of an apps/v1 StatefulSet (go-kure/launcher#1629).
//
// Its properties are the top-level json fields of appsv1.StatefulSetSpec,
// decoded strictly (decodeKindSpec), as the deployment kind's are. It emits the
// StatefulSet, named after the component in the build namespace, with the
// authored spec. Two things are added: the `app` label on the pod template,
// beside the authored labels, which every workload kind gives its pods and
// launcher's traits and Services select on (podTemplateAppLabels), and the
// platform's storage class on each claim template that authors none
// (FillCapabilityDefaults). The selector stays as authored, and the object
// itself gets no label, no annotation and no other default of launcher's. The
// reshaped form, with the main container at the top level, belongs to
// `webservice` and `worker`.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type StatefulsetHandler struct{}

// CanHandle returns true for statefulset component type.
func (h *StatefulsetHandler) CanHandle(componentType string) bool {
	return componentType == "statefulset"
}

// PropertySchema declares every top-level appsv1.StatefulSetSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *StatefulsetHandler) PropertySchema() map[string]oam.PropertySchema {
	selector := podObjectSchema("Required. StatefulSet spec.selector: the label selector over the pods the StatefulSet owns. It must match the template's labels, and it is immutable once the object exists. A selector that rules out the `app` label launcher adds to the template is refused.", "LabelSelector")
	selector.Required = true
	template := podTemplateSchema("Required. StatefulSet spec.template: the pods the StatefulSet creates. Its spec is held to what the pod kind holds its own to (containers required, the image rule, ephemeralContainers, priority and overhead refused) and to the environment policy; activeDeadlineSeconds is refused. metadata.labels gains the `app` label; an authored `app` with another value is refused.")
	template.Required = true
	return oam.DeclareBounds(map[string]oam.PropertySchema{
		"replicas":    {Type: oam.PropertyTypeInteger, Description: "StatefulSet spec.replicas: the number of pods. The API server defaults it to 1. Held to the environment policy's replica maximum; no policy default is filled. Left out of the object when the component carries a scaler trait, whose HorizontalPodAutoscaler owns the count."},
		"selector":    selector,
		"template":    template,
		"serviceName": {Type: oam.PropertyTypeString, Description: "StatefulSet spec.serviceName: the name of the governing Service that gives the pods their stable network identity, a headless `service` component authored beside this one. No default. Must be a valid Service name, a DNS-1123 label (a digit first is accepted at every Kubernetes release: the API validates this field as a DNS-1123 label, not as a Service name)."},
		"volumeClaimTemplates": podListSchema("StatefulSet spec.volumeClaimTemplates: the claims the controller creates for each pod, named after the template and the pod. A template that authors no `spec.storageClassName` (absent or null) gets the ClusterProfile `pvc` capability's storage class; an authored one, \"\" included, wins. Each template's storage request is held to the environment policy's storage maximum.",
			"One PersistentVolumeClaim: metadata.name and spec (access modes, resources.requests.storage, storageClassName, volumeMode, ...).", "PersistentVolumeClaim"),
		"podManagementPolicy":                  {Type: oam.PropertyTypeString, Description: "StatefulSet spec.podManagementPolicy: OrderedReady or Parallel. The API server defaults it to OrderedReady."},
		"updateStrategy":                       podObjectSchema("StatefulSet spec.updateStrategy: `type` (RollingUpdate or OnDelete) and `rollingUpdate` (`partition`, `maxUnavailable`). The API server defaults it to RollingUpdate.", "StatefulSetUpdateStrategy"),
		"minReadySeconds":                      {Type: oam.PropertyTypeInteger, Description: "StatefulSet spec.minReadySeconds: seconds a new pod must be ready, without a container crashing, to count as available. The API server defaults it to 0."},
		"revisionHistoryLimit":                 {Type: oam.PropertyTypeInteger, Description: "StatefulSet spec.revisionHistoryLimit: the number of old ControllerRevisions kept for rollback. The API server defaults it to 10."},
		"persistentVolumeClaimRetentionPolicy": podObjectSchema("StatefulSet spec.persistentVolumeClaimRetentionPolicy: `whenDeleted` and `whenScaled`, each Retain or Delete. The API server defaults both to Retain.", "StatefulSetPersistentVolumeClaimRetentionPolicy"),
		"ordinals":                             podObjectSchema("StatefulSet spec.ordinals: `start`, the number the first pod's ordinal takes. The API server defaults it to 0.", "StatefulSetOrdinals"),
	}, statefulsetBounds())
}

// statefulsetBounds are the bounds of the pod template and replicas, plus the
// storage maximum on each claim template.
func statefulsetBounds() map[string][]oam.RefusalClass {
	b := podTemplateBounds(true)
	b["volumeClaimTemplates"] = []oam.RefusalClass{oam.RefusalStorageMaximum}
	return b
}

// FillCapabilityDefaults gives each volumeClaimTemplates entry that leaves
// spec.storageClassName unauthored (absent or null) the ClusterProfile `pvc`
// capability's storageClassName, as an authored pvc trait, the
// persistentvolumeclaim kind and a webservice or worker pvc volume take it
// (go-kure/launcher#761). An authored class, "" included, wins. The binding is
// read once, and only when the list holds an entry, so `pvc` is recorded as
// consumed exactly when the component builds a claim template. A shape the
// parser refuses (a list that is not one, an entry or a spec that is not a
// mapping) is left for ToApplicationConfig to refuse.
func (h *StatefulsetHandler) FillCapabilityDefaults(props map[string]any, lctx oam.LoweringContext) (map[string]any, error) {
	entries, ok := props["volumeClaimTemplates"].([]any)
	if !ok {
		return props, nil
	}
	var platformClass any
	platformRead := false
	var filled []any
	for i, v := range entries {
		entry, ok := nullElem(v).(map[string]any)
		if !ok {
			continue
		}
		// The strict decode matches keys without regard to case, so `Spec` and
		// `StorageClassName` are authored spellings too: fill under the spelling
		// the document used, or the decode would see the field twice.
		specKey := authoredSpelling(entry, "spec")
		spec := map[string]any{}
		if raw, present := authoredValue(entry, specKey); present {
			if spec, ok = raw.(map[string]any); !ok {
				continue
			}
		}
		if !platformRead {
			platformRead = true
			sc, err := capabilityStorageClass(lctx)
			if err != nil {
				return nil, err
			}
			if sc == nil {
				return props, nil
			}
			platformClass = sc
		}
		classKey := authoredSpelling(spec, "storageClassName")
		if _, present := authoredValue(spec, classKey); present {
			continue
		}
		if filled == nil {
			filled = slices.Clone(entries)
		}
		spec = maps.Clone(spec)
		spec[classKey] = platformClass
		entry = maps.Clone(entry)
		entry[specKey] = spec
		filled[i] = entry
	}
	if filled == nil {
		return props, nil
	}
	out := maps.Clone(props)
	out["volumeClaimTemplates"] = filled
	return out, nil
}

// authoredSpelling returns the key of m that spells name, matching without
// regard to case as the strict decode does. The spelling that holds a value
// wins, because the decode skips a null and accepts a null beside a set
// spelling: a fill written under the null one would then set the field twice.
// Among equals, name itself comes first, then the others in sorted order; with
// no spelling at all the answer is name.
func authoredSpelling(m map[string]any, name string) string {
	spellings := []string{name}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != name && strings.EqualFold(k, name) {
			spellings = append(spellings, k)
		}
	}
	for _, k := range spellings {
		if _, set := authoredValue(m, k); set {
			return k
		}
	}
	// None holds a value: the first one present, else name.
	for _, k := range spellings {
		if _, present := m[k]; present {
			return k
		}
	}
	return name
}

// Endpoints implements oam.EndpointProvider: one endpoint per port the first
// container of the template declares (containerPortEndpoints). Endpoints are
// collected apart from the build, so the handler's own parse runs here too: a
// component it refuses has no endpoint, in the parse's words.
func (h *StatefulsetHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	query := component.WithImageLevel(oam.ImageLevelAny)
	cfg, err := h.ToApplicationConfig(&query, "")
	if err != nil {
		return nil, err
	}
	containers := cfg.(*StatefulsetConfig).Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return nil, nil
	}
	return containerPortEndpoints(component.Name, containers[0].Ports), nil
}

// ToApplicationConfig decodes an OAM statefulset component into a
// StatefulsetConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses. What the StatefulSet may not hold is refused
// here, whatever the environment policy: see StatefulsetConfig.validate,
// podTemplateDefaultedZeros and podTemplateLabelSelectorRequired.
func (h *StatefulsetHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	// The images are held to the component's level, which the config keeps for
	// Generate, where the refusals repeat (go-kure/launcher#1644).
	level := component.ImageLevel()
	spec, props, err := decodeKindSpec[appsv1.StatefulSetSpec](component.Properties, "apps/v1 StatefulSetSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podTemplateDefaultedZeros(&spec.Template)); err != nil {
		return nil, err
	}
	required := podTemplateLabelSelectorRequired()
	maps.Copy(required, labelSelectorRequired("volumeClaimTemplates[].spec.selector"))
	if err := refuseUnauthoredRequired(props, required); err != nil {
		return nil, err
	}
	cfg := &StatefulsetConfig{
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

// StatefulsetConfig implements stack.ApplicationConfig for statefulset
// components. Spec is the decoded StatefulSetSpec exactly as authored.
type StatefulsetConfig struct {
	Name string
	// ObjectName names the StatefulSet (oam.Component.ObjectName), and with it
	// the pods and volume claims the controller names after it; its labels keep
	// Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the StatefulSet
	// (oam.Component.ObjectMetadata). They go on the StatefulSet's own metadata:
	// its pod template keeps the labels of the kind.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      appsv1.StatefulSetSpec

	// scaled is true when the component carries a scaler trait. The
	// HorizontalPodAutoscaler then owns the replica count and the StatefulSet is
	// built without spec.replicas, whichever order the traits are declared in
	// (go-kure/launcher#1179, go-kure/launcher#1268); the scaler enforces the
	// replica maximum on its own maxReplicas.
	scaled bool

	// imageLevel is the level the images were held to at parse, which validate
	// holds them to again; unset is oam.ImageLevelTagged.
	imageLevel oam.ImageReferenceLevel
}

// validate refuses a spec the StatefulSet cannot be emitted from and returns the
// labels of its pod template. selector must be authored: the Go type cannot
// omit it, so an unset one would be written as null, and launcher derives none.
// serviceName names the governing Service, which the component does not emit, so
// it is held to the Service-name rule (validateServiceName); unset, it stays
// empty. The template is held to validateControllerPodTemplateAt, its labels to
// podTemplateAppLabels, and the selector must keep matching the template once
// the `app` label is on it (refuseSelectorAgainstAppLabel). The claim templates
// are held to checkClaimTemplateCollisions. The API's other value rules are left
// to the API server.
func (c *StatefulsetConfig) validate(component string) (map[string]string, error) {
	if c.Spec.Selector == nil {
		return nil, errors.New("selector: required (the label selector over the pods the StatefulSet owns; it must match the template's labels)")
	}
	if c.Spec.ServiceName != "" {
		if err := validateServiceName("serviceName", c.Spec.ServiceName); err != nil {
			return nil, err
		}
	}
	if err := validateControllerPodTemplateAt(&c.Spec.Template, imageLevelOrTagged(c.imageLevel)); err != nil {
		return nil, err
	}
	if err := checkClaimTemplateCollisions(c.Spec.VolumeClaimTemplates, c.Spec.Template.Spec.Volumes); err != nil {
		return nil, err
	}
	for i := range c.Spec.VolumeClaimTemplates {
		if err := validateLabelSelector(fmt.Sprintf("volumeClaimTemplates[%d].spec.selector", i), c.Spec.VolumeClaimTemplates[i].Spec.Selector); err != nil {
			return nil, err
		}
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

// checkClaimTemplateCollisions refuses a claim template that shares its name
// with another claim template or with a `template.spec.volumes` entry. The
// apiserver does not catch the overlap: StatefulSet validation skips the pod
// template's volumes. The StatefulSet controller keys claim templates by name,
// so of two sharing a name only one claim is created, and it replaces a pod
// volume named like a template with that template's claim, so the authored
// volume is never mounted.
func checkClaimTemplateCollisions(vcts []corev1.PersistentVolumeClaim, volumes []corev1.Volume) error {
	names := make(map[string]bool, len(vcts))
	for _, vct := range vcts {
		if names[vct.Name] {
			return errors.Errorf("volumeClaimTemplates: duplicate name %q; the StatefulSet controller creates one claim per template name, so one of the two would never be provisioned", vct.Name)
		}
		names[vct.Name] = true
	}
	for _, v := range volumes {
		if names[v.Name] {
			return errors.Errorf("template.spec.volumes: volume %q has the same name as a claim template; the StatefulSet controller replaces a pod volume named like a claim template with the claim, so this volume would never be mounted. Rename one of them", v.Name)
		}
	}
	return nil
}

// ServiceAccountName implements oam.ServiceAccountNamer: the account the
// StatefulSet's pods run as, read from the template as the pod kind reads its
// spec. No account is generated, so "" means the namespace's default account.
func (c *StatefulsetConfig) ServiceAccountName() (string, bool) {
	if name := c.Spec.Template.Spec.ServiceAccountName; name != "" {
		return name, true
	}
	return c.Spec.Template.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the StatefulSet to the environment policy, as the
// rendered-object check holds one a chart renders: replicas (one when unset,
// the API server's default) to the replica maximum, unless a scaler owns the
// count, the pod template to enforcePodTemplatePolicy, and each claim
// template's storage request to the storage maximum. It fills no default: an
// unset replicas stays unset. A nil policy checks nothing.
func (c *StatefulsetConfig) ApplyPolicy(p oam.Policy) error {
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
	if err := enforcePodTemplatePolicy("template.spec", &c.Spec.Template.Spec, p); err != nil {
		return err
	}
	for i, vct := range c.Spec.VolumeClaimTemplates {
		request, ok := vct.Spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			continue
		}
		label := fmt.Sprintf("volumeClaimTemplates[%d].spec.resources.requests.storage", i)
		if err := enforceMaxStorageAt(request.String(), p.MaxStorageSize(), label); err != nil {
			return err
		}
	}
	return nil
}

// Generate emits the StatefulSet: kure's identity-only constructor, a deep copy
// of the spec, and the `app` label on the pod template. Under a scaler the
// StatefulSet is built without spec.replicas. The parse-time refusals the typed
// spec can show are repeated, since the config is exported and the label is
// valued from app.Name. No ServiceAccount, claim or Service is emitted beside
// it (go-kure/launcher#702): serviceName names a Service authored beside it.
func (c *StatefulsetConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labelled, err := c.validate(app.Name)
	if err != nil {
		return nil, err
	}
	sts := kubernetes.CreateStatefulSet(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&sts.Spec)
	sts.Spec.Template.Labels = labelled
	if c.scaled {
		sts.Spec.Replicas = nil
	}
	return kindObject(sts, c.Metadata)
}
