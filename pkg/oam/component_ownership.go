package oam

import (
	"reflect"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
)

// Component ownership (go-kure/launcher#788). Every application of a
// transformed document belongs to one authored component, or to the document as
// a whole. The transform's last step records that on each application
// (markComponentOwnership): it wraps the application's config in an ownedConfig,
// which reports the owner (ComponentNamed, GeneratedApplication.Component) and
// puts the component label on every object the config generates, and on the pod
// templates among them. The label is the one the synthesized NetworkPolicies
// select (TransformContext.ComponentLabelKey), so a selector for a component
// matches that component's pods without the caller labelling anything.

// componentOwner is implemented by the ownership wrapper. GenerateApplications
// reads it ahead of ComponentNamed: only it can say "the document as a whole",
// which ComponentNamed's empty answer leaves to the application's name.
type componentOwner interface {
	// owningComponent returns the authored component the application belongs
	// to, or "" for one the document as a whole owns.
	owningComponent() string
}

// ownedConfig wraps an application's config once the transform has built the
// cluster. It is the outermost config of every application from then on: over
// the component's own config, over a trait decorator, over a sibling group.
//
// Generate labels the wrapped config's objects with the owning component, where
// they carry no value for the key yet (stampComponentLabel). An application the
// document as a whole owns (component "") is generated unchanged.
//
// It forwards every optional contract code reads on an application's config
// after the transform, each as a trait decorator forwards it: by value, so a
// caller must test what a method returns, not that the method exists
// (TestOwnedConfig_ForwardsEveryConfigContract keeps the list complete). The
// layout contracts are the exception, present only when the wrapped config has
// them: wrapOwnedConfig.
type ownedConfig struct {
	inner     stack.ApplicationConfig
	component string
	labelKey  string
}

// wrapOwnedConfig wraps inner for the component that owns its application; an
// empty component is the document as a whole. kure's layout walker reads
// LayoutAugmenter and LayoutIntentAugmenter by presence, and presence decides
// the application's placement, so the wrapper has each only when inner has it.
func wrapOwnedConfig(inner stack.ApplicationConfig, component, labelKey string) stack.ApplicationConfig {
	owned := &ownedConfig{inner: inner, component: component, labelKey: labelKey}
	augmenter, ok := inner.(layout.LayoutAugmenter)
	if !ok {
		return owned
	}
	augmenting := &augmentingOwnedConfig{ownedConfig: owned, augmenter: augmenter}
	if intent, ok := inner.(layout.LayoutIntentAugmenter); ok {
		return &intentAugmentingOwnedConfig{augmentingOwnedConfig: augmenting, intent: intent}
	}
	return augmenting
}

func (o *ownedConfig) owningComponent() string { return o.component }

// ComponentName is the authored component the application belongs to
// (ComponentNamed), or "" for an application the document as a whole owns.
func (o *ownedConfig) ComponentName() string { return o.component }

// WrappedApplicationConfig returns the config the wrapper holds (ConfigWrapper).
func (o *ownedConfig) WrappedApplicationConfig() stack.ApplicationConfig { return o.inner }

// ConfigWrapper is implemented by a config that wraps another one and says so.
// The ownership wrapper every application of a transformed document carries is
// one; a caller's own wrapper may be one too.
type ConfigWrapper interface {
	// WrappedApplicationConfig returns the config the wrapper holds.
	WrappedApplicationConfig() stack.ApplicationConfig
}

// UnwrapConfig returns the config under every ConfigWrapper around cfg: what a
// caller reads when it needs the concrete config of an application of a
// transformed document, or a contract of it the wrapper does not forward. A
// trait decorator is not a ConfigWrapper, so a decorated config is returned as
// the decorator.
func UnwrapConfig(cfg stack.ApplicationConfig) stack.ApplicationConfig {
	for {
		w, ok := cfg.(ConfigWrapper)
		if !ok {
			return cfg
		}
		inner := w.WrappedApplicationConfig()
		if inner == nil {
			return cfg
		}
		cfg = inner
	}
}

// Generate returns the wrapped config's objects, labelled with the owning
// component.
func (o *ownedConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := o.inner.Generate(app)
	if err != nil || o.component == "" {
		return objs, err
	}
	for _, p := range objs {
		if p == nil {
			continue
		}
		if err := o.stamp(*p); err != nil {
			return nil, err
		}
	}
	return objs, nil
}

func (o *ownedConfig) stamp(obj client.Object) error {
	if isNullValue(obj) {
		return nil
	}
	return stampComponentLabel(obj, o.labelKey, ComponentLabelValue(o.component))
}

// stampLayout labels every resource on l and on its child layouts. kure's
// walker hands an augmenter a layout holding only its own application's
// objects, so everything reachable from l belongs to the component.
func (o *ownedConfig) stampLayout(l *layout.ManifestLayout) error {
	if l == nil || o.component == "" {
		return nil
	}
	for _, r := range l.Resources {
		if err := o.stamp(r); err != nil {
			return err
		}
	}
	for _, c := range l.Children {
		if err := o.stampLayout(c); err != nil {
			return err
		}
	}
	return nil
}

// Validate forwards to the wrapped config's stack.Validator: kure validates
// only the outermost config of an application.
func (o *ownedConfig) Validate() error {
	if v, ok := o.inner.(stack.Validator); ok {
		return v.Validate()
	}
	return nil
}

// SetFluxNamespace forwards fluxNamespaceSettable.
func (o *ownedConfig) SetFluxNamespace(ns string) {
	if s, ok := o.inner.(fluxNamespaceSettable); ok {
		s.SetFluxNamespace(ns)
	}
}

// FluxNamespaceReads forwards fluxNamespaceReader, none when the wrapped
// config is not one.
func (o *ownedConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	if r, ok := o.inner.(fluxNamespaceReader); ok {
		return r.FluxNamespaceReads()
	}
	return nil, nil
}

// ServicePort forwards servicePortProvider, 0 when the wrapped config
// exposes no Service port.
func (o *ownedConfig) ServicePort() int32 {
	if p, ok := o.inner.(servicePortProvider); ok {
		return p.ServicePort()
	}
	return 0
}

// BackendServiceName forwards serviceBackendNamer, "" when the wrapped config
// does not name its Service.
func (o *ownedConfig) BackendServiceName() string {
	if n, ok := o.inner.(serviceBackendNamer); ok {
		return n.BackendServiceName()
	}
	return ""
}

// ServicePortName forwards the Service port name contract the routing traits
// read, "" and false when the wrapped config does not know its port names.
func (o *ownedConfig) ServicePortName() (string, bool) {
	if n, ok := o.inner.(siblingServicePortNamer); ok {
		return n.ServicePortName()
	}
	return "", false
}

// ServiceAccountName forwards ServiceAccountNamer, "" and false when the
// wrapped config is not one.
func (o *ownedConfig) ServiceAccountName() (string, bool) {
	if n, ok := o.inner.(ServiceAccountNamer); ok {
		return n.ServiceAccountName()
	}
	return "", false
}

// NonRWXClaim forwards the single-pod claim contract the scaler trait reads,
// "" when the wrapped config names none.
func (o *ownedConfig) NonRWXClaim() string {
	if n, ok := o.inner.(siblingNonRWXClaimer); ok {
		return n.NonRWXClaim()
	}
	return ""
}

// ServiceRoutingTarget forwards serviceRoutingTargeter, a nil selector when
// the wrapped config is not one.
func (o *ownedConfig) ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	if t, ok := o.inner.(serviceRoutingTargeter); ok {
		return t.ServiceRoutingTarget(servicePorts)
	}
	return nil, nil
}

// PodTemplateLabels forwards podTemplateLabeler, nil when the wrapped config
// runs no pods. The labels are the wrapped config's own: the component label
// the wrapper adds is not among them.
func (o *ownedConfig) PodTemplateLabels() map[string]string {
	if l, ok := o.inner.(podTemplateLabeler); ok {
		return l.PodTemplateLabels()
	}
	return nil
}

// IdentityTargetPorts forwards identityPortMapper, false when the wrapped
// config is not one.
func (o *ownedConfig) IdentityTargetPorts() bool {
	if m, ok := o.inner.(identityPortMapper); ok {
		return m.IdentityTargetPorts()
	}
	return false
}

// augmentingOwnedConfig is the wrapper over a config that is a kure layout
// augmenter.
type augmentingOwnedConfig struct {
	*ownedConfig
	augmenter layout.LayoutAugmenter
}

// AugmentLayout runs the wrapped augmenter, then labels the layout: what the
// augmenter adds or moves into child layouts never passed through Generate.
func (a *augmentingOwnedConfig) AugmentLayout(l *layout.ManifestLayout) error {
	if err := a.augmenter.AugmentLayout(l); err != nil {
		return err
	}
	return a.stampLayout(l)
}

// GenerateCoversAugmentLayout forwards LayoutAugmentationCoverage, false when
// the wrapped augmenter does not declare it.
func (a *augmentingOwnedConfig) GenerateCoversAugmentLayout() bool {
	cov, ok := a.augmenter.(LayoutAugmentationCoverage)
	return ok && cov.GenerateCoversAugmentLayout()
}

// intentAugmentingOwnedConfig is the wrapper over an augmenter that also
// states its layout intent.
type intentAugmentingOwnedConfig struct {
	*augmentingOwnedConfig
	intent layout.LayoutIntentAugmenter
}

// WantsOwnLayout forwards layout.LayoutIntentAugmenter.
func (a *intentAugmentingOwnedConfig) WantsOwnLayout() bool { return a.intent.WantsOwnLayout() }

var (
	_ componentOwner               = (*ownedConfig)(nil)
	_ ComponentNamed               = (*ownedConfig)(nil)
	_ layout.LayoutAugmenter       = (*augmentingOwnedConfig)(nil)
	_ LayoutAugmentationCoverage   = (*augmentingOwnedConfig)(nil)
	_ layout.LayoutIntentAugmenter = (*intentAugmentingOwnedConfig)(nil)
)

// markComponentOwnership wraps the config of every application of cluster in
// its ownership wrapper. It runs last in the transform: every step before it
// reads the configs as the handlers and traits left them.
//
// An application's owner is the authored component it came from, which lowering
// records on everything a rule emits (Origin.Component): a component's own
// application or sibling group; the applications its traits added; the
// NetworkPolicies synthesized for it. A component a rule emits under another
// name (postgresql's Pooler) is its authored component's, not its own.
//
// The document as a whole owns a generated source its application bundle holds
// (componentOrder.sources), whichever component's rule emitted it and however
// many components read it, with the applications that source's traits added,
// and the NetworkPolicy synthesized for an external backend Service.
func markComponentOwnership(cluster *stack.Cluster, order *componentOrder, subApps []traitSubApps, labelKey string) {
	if cluster == nil {
		return
	}
	// The owner of each application, and of each component after lowering by
	// name; "" is the document as a whole.
	owners := map[*stack.Application]string{}
	byEntryName := map[string]string{}
	record := func(e componentEntry, component string) {
		owners[e.app] = component
		byEntryName[e.component.Name] = component
		for _, m := range e.members {
			owners[m.app] = component
		}
	}
	for _, e := range order.sources {
		record(e, "")
	}
	for _, group := range order.groups {
		for _, e := range group {
			record(e, authoredComponent(e.component))
		}
	}
	// A trait's sub-applications follow the application the trait ran on: in a
	// sibling group that is a member, which record covers.
	for _, s := range subApps {
		for _, sub := range s.subApps {
			owners[sub] = owners[s.owner]
		}
	}
	walkBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, app := range bundle.Applications {
			component, ok := owners[app]
			if !ok {
				// What is left is what the transform itself added: a synthesized
				// NetworkPolicy, its component's or, for an external backend, the
				// document's.
				component = byEntryName[synthesizedPolicyComponent(app.Config)]
			}
			app.Config = wrapOwnedConfig(app.Config, component, labelKey)
		}
	})
}

// authoredComponent returns the name of the authored component c came from:
// the one lowering recorded on it, else its own.
func authoredComponent(c Component) string {
	if origin, ok := c.Origin(); ok && origin.Component != "" {
		return origin.Component
	}
	return c.Name
}

// synthesizedPolicyComponent returns the name of the component a synthesized
// NetworkPolicy's config was built for, "" for an external backend's.
func synthesizedPolicyComponent(cfg stack.ApplicationConfig) string {
	switch c := cfg.(type) {
	case *componentAllowPolicyConfig:
		return c.ComponentName
	case *backendIngressAllowPolicyConfig:
		return c.ComponentName
	case *componentEgressPolicyConfig:
		return c.ComponentName
	case *componentEndpointIngressPolicyConfig:
		return c.ComponentName
	}
	return ""
}

// stampComponentLabel puts key: value on obj where obj carries no value for key
// yet, and on its pod template when obj is a workload: a Deployment,
// StatefulSet, DaemonSet, Job or CronJob, typed or unstructured. A HelmRelease
// also gets the post-renderer that labels its chart's pod templates
// (componentLabelPostRenderer).
//
// A value already there stays, whatever it is: a component, a trait or an
// author set it, and with a key a workload selects on (ComponentLabelKey "app")
// an overwrite of the pod template's would part the selector from the template.
// For the same reason a pod template stays as written when the workload's own
// selector rules the label out (withComponentLabel).
func stampComponentLabel(obj client.Object, key, value string) error {
	obj.SetLabels(withMissing(obj.GetLabels(), map[string]string{key: value}))
	switch o := obj.(type) {
	case *appsv1.Deployment:
		o.Spec.Template.Labels = withComponentLabel(o.Spec.Template.Labels, o.Spec.Selector, key, value)
	case *appsv1.StatefulSet:
		o.Spec.Template.Labels = withComponentLabel(o.Spec.Template.Labels, o.Spec.Selector, key, value)
	case *appsv1.DaemonSet:
		o.Spec.Template.Labels = withComponentLabel(o.Spec.Template.Labels, o.Spec.Selector, key, value)
	case *batchv1.Job:
		o.Spec.Template.Labels = withComponentLabel(o.Spec.Template.Labels, o.Spec.Selector, key, value)
	case *batchv1.CronJob:
		job := &o.Spec.JobTemplate.Spec
		job.Template.Labels = withComponentLabel(job.Template.Labels, job.Selector, key, value)
	case *helmv2.HelmRelease:
		pr, err := componentLabelPostRenderer(key, value)
		if err != nil {
			return err
		}
		for _, existing := range o.Spec.PostRenderers {
			if reflect.DeepEqual(existing, pr) {
				return nil
			}
		}
		o.Spec.PostRenderers = append(o.Spec.PostRenderers, pr)
	case *unstructured.Unstructured:
		return stampUnstructured(o, key, value)
	}
	return nil
}

// withComponentLabel returns a pod template's labels with key: value added.
// They are returned as written when they carry the key already, and when the
// workload's own selector matches them but would not match them with the label:
// a selector that rules the key out (DoesNotExist), or this value (NotIn). The
// API server refuses a workload whose selector does not match its template, so
// such a workload's pods carry no component label.
//
// A selector that does not parse, or that does not match the template in the
// first place, holds nothing back: neither is this function's to refuse.
func withComponentLabel(podLabels map[string]string, selector *metav1.LabelSelector, key, value string) map[string]string {
	if _, exists := podLabels[key]; exists {
		return podLabels
	}
	if selector != nil {
		if sel, err := metav1.LabelSelectorAsSelector(selector); err == nil && sel.Matches(labels.Set(podLabels)) {
			labelled := make(labels.Set, len(podLabels)+1)
			for k, v := range podLabels {
				labelled[k] = v
			}
			labelled[key] = value
			if !sel.Matches(labelled) {
				return podLabels
			}
		}
	}
	return withMissing(podLabels, map[string]string{key: value})
}

// podTemplateKinds are the workload kinds whose pod template takes the
// component label, in the order the HelmRelease post-renderer patches them,
// with the path of the spec that holds the pod template ("template") and the
// workload's own selector ("selector") in each.
var podTemplateKinds = []struct {
	group, version, kind string
	spec                 []string
}{
	{"apps", "v1", "Deployment", []string{"spec"}},
	{"apps", "v1", "StatefulSet", []string{"spec"}},
	{"apps", "v1", "DaemonSet", []string{"spec"}},
	{"batch", "v1", "Job", []string{"spec"}},
	{"batch", "v1", "CronJob", []string{"spec", "jobTemplate", "spec"}},
}

// objectField returns the object m holds at field. YAML's explicit null is an
// absent value: both are reported as not found. Anything else that is no
// object is an error.
func objectField(m map[string]any, field string) (map[string]any, bool, error) {
	v, ok := m[field]
	if !ok || v == nil {
		return nil, false, nil
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, false, errors.Errorf("%s is a %T, not an object", field, v)
	}
	return o, true, nil
}

// stampUnstructured is stampComponentLabel's pod template and HelmRelease work
// on an unstructured object, told by its API group and kind.
func stampUnstructured(u *unstructured.Unstructured, key, value string) error {
	gvk := u.GroupVersionKind()
	if gvk.Group == helmv2.GroupVersion.Group && gvk.Kind == helmv2.HelmReleaseKind {
		return stampUnstructuredHelmRelease(u, key, value)
	}
	for _, k := range podTemplateKinds {
		if gvk.Group != k.group || gvk.Kind != k.kind {
			continue
		}
		if err := stampUnstructuredPodTemplate(u.Object, k.spec, key, value); err != nil {
			return errors.Errorf("component label: %s %q: pod template: %w", gvk.Kind, u.GetName(), err)
		}
		return nil
	}
	return nil
}

// stampUnstructuredPodTemplate is withComponentLabel on the pod template under
// the spec at specPath of obj. A workload with no pod template, or a null one,
// is left as it is; null metadata or labels on the template are absent ones.
func stampUnstructuredPodTemplate(obj map[string]any, specPath []string, key, value string) error {
	spec := obj
	for _, field := range specPath {
		next, found, err := objectField(spec, field)
		if err != nil || !found {
			return err
		}
		spec = next
	}
	template, found, err := objectField(spec, "template")
	if err != nil || !found {
		return err
	}
	metadata, _, err := objectField(template, "metadata")
	if err != nil {
		return err
	}
	raw, _, err := objectField(metadata, "labels")
	if err != nil {
		return err
	}
	if _, exists := raw[key]; exists {
		return nil
	}
	podLabels := make(map[string]string, len(raw))
	for name, v := range raw {
		s, ok := v.(string)
		if !ok {
			return errors.Errorf("label %q is a %T, not a string", name, v)
		}
		podLabels[name] = s
	}
	// A selector that does not decode holds nothing back, as one that does not
	// parse (withComponentLabel).
	var selector *metav1.LabelSelector
	if rawSelector, ok := spec["selector"].(map[string]any); ok {
		decoded := &metav1.LabelSelector{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(rawSelector, decoded) == nil {
			selector = decoded
		}
	}
	if _, added := withComponentLabel(podLabels, selector, key, value)[key]; !added {
		return nil
	}
	if metadata == nil {
		metadata = map[string]any{}
		template["metadata"] = metadata
	}
	if raw == nil {
		raw = map[string]any{}
		metadata["labels"] = raw
	}
	raw[key] = value
	return nil
}

func stampUnstructuredHelmRelease(u *unstructured.Unstructured, key, value string) error {
	pr, err := componentLabelPostRenderer(key, value)
	if err != nil {
		return err
	}
	entry, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pr)
	if err != nil {
		return errors.Errorf("component label: HelmRelease %q: post-renderer: %w", u.GetName(), err)
	}
	spec, found, err := objectField(u.Object, "spec")
	if err != nil {
		return errors.Errorf("component label: HelmRelease %q: %w", u.GetName(), err)
	}
	if !found {
		spec = map[string]any{}
		u.Object["spec"] = spec
	}
	var existing []any
	switch v := spec["postRenderers"].(type) {
	case nil:
	case []any:
		existing = v
	default:
		return errors.Errorf("component label: HelmRelease %q: spec.postRenderers is a %T, not a list", u.GetName(), v)
	}
	for _, e := range existing {
		if reflect.DeepEqual(e, any(entry)) {
			return nil
		}
	}
	spec["postRenderers"] = append(existing[:len(existing):len(existing)], entry)
	return nil
}

// componentLabelPostRendererName is the placeholder metadata.name of each
// patch: kustomize needs one to read a strategic merge patch, and the patch's
// target, not this name, decides which objects it merges into.
const componentLabelPostRendererName = "component-label"

// componentLabelPostRenderer builds the Flux post-renderer that puts key: value
// on the pod template of every workload a chart renders. A Flux post-renderer
// offers kustomize patches and images only, so it is one strategic merge patch
// per workload kind (podTemplateKinds), each targeting the kind in its own API
// group; a chart that renders none of a kind is left alone by that patch.
//
// A strategic merge sets the value: unlike the label on an object launcher
// generates itself, this one replaces a value the chart gave the key.
//
// The result depends on key and value only, so a HelmRelease stamped again gets
// an equal post-renderer, which stampComponentLabel does not add twice.
func componentLabelPostRenderer(key, value string) (helmv2.PostRenderer, error) {
	patches := make([]kustomize.Patch, 0, len(podTemplateKinds))
	for _, k := range podTemplateKinds {
		doc := map[string]any{
			"apiVersion": k.group + "/" + k.version,
			"kind":       k.kind,
			"metadata":   map[string]any{"name": componentLabelPostRendererName},
		}
		// Through the YAML encoder, which quotes a value that would read back as
		// a number, a boolean or null: a label value is a string.
		if err := unstructured.SetNestedStringMap(doc, map[string]string{key: value}, append(append([]string(nil), k.spec...), "template", "metadata", "labels")...); err != nil {
			return helmv2.PostRenderer{}, errors.Errorf("component label: post-renderer patch for %s: %w", k.kind, err)
		}
		raw, err := yaml.Marshal(doc)
		if err != nil {
			return helmv2.PostRenderer{}, errors.Errorf("component label: post-renderer patch for %s: %w", k.kind, err)
		}
		patches = append(patches, kustomize.Patch{
			Patch:  string(raw),
			Target: &kustomize.Selector{Group: k.group, Version: k.version, Kind: k.kind},
		})
	}
	return helmv2.PostRenderer{Kustomize: &helmv2.Kustomize{Patches: patches}}, nil
}
