package oam

import (
	"maps"
	"reflect"
	"slices"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
// Generate first holds the wrapped config's objects to the consumer's reserved
// metadata keys and to the component label (check): a value an object already
// carries under the label's key is the owning component's, or generation fails.
// Then it labels them with the owning component, where they carry no value for
// the key yet (stampComponentLabel). An application the document as a whole
// owns (component "") is held to the reserved keys and otherwise generated
// unchanged.
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
	// entry is the name of the component after lowering the application came
	// from, which is the owning component's unless a lowering rule emitted it
	// under a name of its own. The kinds write the `app` label with it
	// (componentLabelValues).
	entry    string
	labelKey string
	// reserved is the consumer's reserved metadata keys, nil when it reserves
	// none (reserved_metadata.go).
	reserved *reservedMetadataKeys
}

// wrapOwnedConfig wraps inner for the component that owns its application; an
// empty component is the document as a whole. kure's layout walker reads
// LayoutAugmenter and LayoutIntentAugmenter by presence, and presence decides
// the application's placement, so the wrapper has each only when inner has it.
func wrapOwnedConfig(inner stack.ApplicationConfig, component, labelKey string) stack.ApplicationConfig {
	return wrapOwnedConfigReserving(inner, component, labelKey, nil)
}

// wrapOwnedConfigReserving is wrapOwnedConfig with the consumer's reserved
// metadata keys, which the wrapper holds the application's objects to.
func wrapOwnedConfigReserving(inner stack.ApplicationConfig, component, labelKey string, reserved *reservedMetadataKeys) stack.ApplicationConfig {
	return wrapOwnedEntryConfig(inner, component, component, labelKey, reserved)
}

// wrapOwnedEntryConfig is wrapOwnedConfigReserving for an application that came
// from the component named entry after lowering, which a rule may have emitted
// under another name than component, its owner's.
func wrapOwnedEntryConfig(inner stack.ApplicationConfig, component, entry, labelKey string, reserved *reservedMetadataKeys) stack.ApplicationConfig {
	owned := &ownedConfig{inner: inner, component: component, entry: entry, labelKey: labelKey, reserved: reserved}
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

// Generate returns the wrapped config's objects, held to the reserved metadata
// keys and labelled with the owning component.
func (o *ownedConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := o.inner.Generate(app)
	if err != nil || (o.component == "" && o.reserved == nil) {
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

// stamp holds obj to the reserved metadata keys and to the component label as
// the wrapped config left it, then labels it with the owning component. The
// checks come first, so they never read the label the wrapper itself writes.
func (o *ownedConfig) stamp(obj client.Object) error {
	if isNullValue(obj) {
		return nil
	}
	if err := o.check(obj); err != nil {
		return err
	}
	if o.component == "" {
		return nil
	}
	return stampComponentLabel(obj, o.labelKey, ComponentLabelValue(o.component))
}

// layoutResources is the resources on a layout and on its child layouts, told
// apart by identity: each is the pointer the layout holds.
type layoutResources map[client.Object]struct{}

// resourceIdentity returns obj as the key that tells it apart from every other
// object, which only a pointer is. An object of another shape has none.
func resourceIdentity(obj client.Object) (client.Object, bool) {
	if isNullValue(obj) || reflect.ValueOf(obj).Kind() != reflect.Pointer {
		return nil, false
	}
	return obj, true
}

// collect adds every resource on l and on its child layouts to r.
func (r layoutResources) collect(l *layout.ManifestLayout) {
	if l == nil {
		return
	}
	for _, obj := range l.Resources {
		if id, ok := resourceIdentity(obj); ok {
			r[id] = struct{}{}
		}
	}
	for _, c := range l.Children {
		r.collect(c)
	}
}

// stampAdded checks and labels every resource on l and on its child layouts
// that is not in before: what the wrapped augmenter added. A resource with no
// identity (resourceIdentity) is taken as added.
func (o *ownedConfig) stampAdded(l *layout.ManifestLayout, before layoutResources) error {
	if l == nil {
		return nil
	}
	for _, r := range l.Resources {
		if id, ok := resourceIdentity(r); ok {
			if _, was := before[id]; was {
				continue
			}
		}
		if err := o.stamp(r); err != nil {
			return err
		}
	}
	for _, c := range l.Children {
		if err := o.stampAdded(c, before); err != nil {
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

// AugmentLayout runs the wrapped augmenter, then checks and labels what it
// added: the resources on l and on its child layouts that were not there before
// it ran. An augmenter may add an object no Generate returned, which is read
// nowhere else; one it adds that Generate did return is read again.
//
// What was there is left as it is. kure's walker hands over the objects the
// application's outermost config generated, which passed through Generate here
// and were checked and labelled then; a config a consumer wraps around this one
// after the transform may since have added a key the consumer reserved, or an
// object of its own, and neither is read (go-kure/launcher#790).
//
// Two things follow, and neither is checked here. An object that was on the
// layout and that the wrapped augmenter edits in place is not read again: no
// augmenter of launcher's edits one. And a caller that hands over a layout
// holding objects that never passed through Generate gets them back unchecked
// and unlabelled.
func (a *augmentingOwnedConfig) AugmentLayout(l *layout.ManifestLayout) error {
	if a.component == "" && a.reserved == nil {
		return a.augmenter.AugmentLayout(l)
	}
	before := layoutResources{}
	before.collect(l)
	if err := a.augmenter.AugmentLayout(l); err != nil {
		return err
	}
	return a.stampAdded(l, before)
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
//
// It refuses the one document the wrapper's `app` exemption would let another
// component's label value through for (checkEntryLabelValues).
func markComponentOwnership(cluster *stack.Cluster, order *componentOrder, subApps []traitSubApps, labelKey string, reserved *reservedMetadataKeys) error {
	if cluster == nil {
		return nil
	}
	// The owner of each application, with the component after lowering it came
	// from, and of each component after lowering by name; "" is the document as
	// a whole.
	type owner struct{ component, entry string }
	owners := map[*stack.Application]owner{}
	byEntryName := map[string]string{}
	record := func(e componentEntry, component string) {
		owners[e.app] = owner{component: component, entry: e.component.Name}
		byEntryName[e.component.Name] = component
		for _, m := range e.members {
			// A sibling group's members share its name (stampSiblingGroups), so
			// byEntryName holds them already.
			owners[m.app] = owner{component: component, entry: m.component.Name}
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
	if err := checkEntryLabelValues(byEntryName, labelKey); err != nil {
		return err
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
			own, ok := owners[app]
			if !ok {
				// What is left is what the transform itself added: a synthesized
				// NetworkPolicy, its component's or, for an external backend, the
				// document's.
				entry := synthesizedPolicyComponent(app.Config)
				own = owner{component: byEntryName[entry], entry: entry}
			}
			app.Config = wrapOwnedEntryConfig(app.Config, own.component, own.entry, labelKey, reserved)
		}
	})
	return nil
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
// StatefulSet, DaemonSet, Job, CronJob, ReplicaSet or ReplicationController,
// typed or unstructured. A CronJob's job template gets it as well, so every Job
// the CronJob creates carries it. A PodTemplate is its pod template: it gets
// the label there too. A HelmRelease also gets the post-renderer that labels
// its chart's pod templates and job templates (componentLabelPostRenderer).
//
// A value already there stays. The wrapper has held it to the component's
// before this runs (ownedConfig.checkComponentLabel), so it is the value this
// would write, or under ComponentLabelKey "app" the one a kind wrote for an
// entry of the component. It is never overwritten: with a key a workload
// selects on, an overwrite of the pod template's would part the selector from
// the template. For the same reason a pod template stays as written when the
// workload's own selector rules the label out (withComponentLabel), a workload
// the wrapper has refused before this runs (ownedConfig.checkWorkloadSelector).
//
// An unstructured list envelope stands for its members when Flux applies it
// (appliedObjects), so each member is labelled as an object handed out on its
// own is, beside the envelope.
func stampComponentLabel(obj client.Object, key, value string) error {
	// An unstructured object's labels are read as written, not through its
	// accessor (stampUnstructured).
	if u, ok := obj.(*unstructured.Unstructured); ok {
		if err := stampUnstructured(u, key, value); err != nil {
			return err
		}
		for _, applied := range appliedObjects(u) {
			member, ok := applied.(*unstructured.Unstructured)
			if !ok || member == u {
				continue
			}
			if err := stampUnstructured(member, key, value); err != nil {
				return err
			}
		}
		return nil
	}
	obj.SetLabels(ownLabels(obj.GetLabels(), key, value))
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
		// The job template's own labels are each Job's: no selector reads them.
		o.Spec.JobTemplate.Labels = ownLabels(o.Spec.JobTemplate.Labels, key, value)
		job := &o.Spec.JobTemplate.Spec
		job.Template.Labels = withComponentLabel(job.Template.Labels, job.Selector, key, value)
	case *appsv1.ReplicaSet:
		o.Spec.Template.Labels = withComponentLabel(o.Spec.Template.Labels, o.Spec.Selector, key, value)
	case *corev1.ReplicationController:
		// Its selector is a plain label map, which a further label on the pods
		// never fails; its pod template is optional.
		if o.Spec.Template != nil {
			o.Spec.Template.Labels = withComponentLabel(o.Spec.Template.Labels, nil, key, value)
		}
	case *corev1.PodTemplate:
		// Its pod template is a field of the object itself, and it has no
		// selector.
		o.Template.Labels = withComponentLabel(o.Template.Labels, nil, key, value)
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
	}
	return nil
}

// withComponentLabel returns a pod template's labels with key: value added.
// They are returned as written when they carry the key already, and when the
// workload's own selector rules the label out (selectorRulesOut): the API
// server refuses a workload whose selector does not match its template. The
// wrapper refuses such a workload before it labels anything
// (ownedConfig.checkWorkloadSelector), so this never parts a selector from its
// template.
func withComponentLabel(podLabels map[string]string, selector *metav1.LabelSelector, key, value string) map[string]string {
	if _, exists := podLabels[key]; exists {
		return podLabels
	}
	if selectorRulesOut(selector, podLabels, key, value) {
		return podLabels
	}
	return ownLabels(podLabels, key, value)
}

// selectorRulesOut reports whether a workload's selector matches its pod
// template's labels but would not match them with key: value added: a selector
// that rules the key out (DoesNotExist), or this value (NotIn).
//
// A nil selector, one that does not parse, and one that does not match the
// template in the first place rule nothing out: neither of the last two is the
// label's to refuse.
func selectorRulesOut(selector *metav1.LabelSelector, podLabels map[string]string, key, value string) bool {
	if selector == nil {
		return false
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil || !sel.Matches(labels.Set(podLabels)) {
		return false
	}
	labelled := make(labels.Set, len(podLabels)+1)
	for k, v := range podLabels {
		labelled[k] = v
	}
	labelled[key] = value
	return !sel.Matches(labelled)
}

// ownLabels returns labels with key: value added where key is absent, in a map
// of its own. The map given stays as it is: a config may use one map for an
// object's labels, its selector and its pod template, and a selector that
// gained the key through it would differ from the one the cluster already
// holds, which a workload does not allow.
func ownLabels(labels map[string]string, key, value string) map[string]string {
	if _, exists := labels[key]; exists {
		return labels
	}
	return withMissing(maps.Clone(labels), map[string]string{key: value})
}

// podTemplateKinds are the kinds whose pod template takes the component label,
// the workload kinds and PodTemplate, in the order the HelmRelease
// post-renderer patches them, with the path of the spec that holds the pod
// template ("template") and the workload's own selector ("selector") in each.
// The core group is "": a kustomize target cannot name it, and one with no
// group matches the kind in any group, so the ReplicationController patch would
// also reach a v1 kind of that name in another group.
//
// A ReplicationController's selector is a plain label map, not a label
// selector. Read as one it either does not decode or selects every pod, so it
// holds no label back, as a label map does not.
//
// A PodTemplate holds its pod template itself, with no spec around it and no
// selector: its path is empty. It is in the core group too, so its patch has
// the reach the ReplicationController's has.
//
// labelMapSelector says the selector is that plain label map, which the check
// of the values a selector requires reads as one (labelMapRequiredValues).
//
// jobTemplate is the path of a CronJob's job template, whose metadata every
// Job the CronJob creates carries: it takes the label and is read as the pod
// template is (go-kure/launcher#790). It is nil on every other kind.
var podTemplateKinds = []struct {
	group, version, kind string
	spec                 []string
	labelMapSelector     bool
	jobTemplate          []string
}{
	{"apps", "v1", "Deployment", []string{"spec"}, false, nil},
	{"apps", "v1", "StatefulSet", []string{"spec"}, false, nil},
	{"apps", "v1", "DaemonSet", []string{"spec"}, false, nil},
	{"batch", "v1", "Job", []string{"spec"}, false, nil},
	{"batch", "v1", "CronJob", []string{"spec", "jobTemplate", "spec"}, false, []string{"spec", "jobTemplate"}},
	{"apps", "v1", "ReplicaSet", []string{"spec"}, false, nil},
	{"", "v1", "ReplicationController", []string{"spec"}, true, nil},
	{"", "v1", "PodTemplate", nil, false, nil},
}

// objectField returns the object m holds at field. YAML's explicit null is an
// absent value: both are reported as not found, and so is a nil map a config
// built in Go may hold there, which serializes as null and cannot be written
// into. Anything else that is no object is an error.
func objectField(m map[string]any, field string) (map[string]any, bool, error) {
	v, ok := m[field]
	if !ok || v == nil {
		return nil, false, nil
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, false, errors.Errorf("%s is a %T, not an object", field, v)
	}
	if o == nil {
		return nil, false, nil
	}
	return o, true, nil
}

// labelsOf reads the labels under holder's metadata as written: an object's own
// (holder is the object) or a pod template's. Null metadata or labels are
// absent ones, and a null value is the empty string, as the cluster reads it. A
// value that is no string is an error, the first by name.
func labelsOf(holder map[string]any) (map[string]string, error) {
	metadata, _, err := objectField(holder, "metadata")
	if err != nil {
		return nil, err
	}
	raw, _, err := objectField(metadata, "labels")
	if err != nil {
		return nil, err
	}
	read := make(map[string]string, len(raw))
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		switch v := raw[name].(type) {
		case nil:
			read[name] = ""
		case string:
			read[name] = v
		default:
			return nil, errors.Errorf("label %q is a %T, not a string", name, v)
		}
	}
	return read, nil
}

// setLabel writes key: value into the labels under holder's metadata, which
// labelsOf has read. It makes the metadata and the labels where they are absent
// or null and leaves every other label as written. The labels it writes are a
// copy: the map that was there may be shared (ownLabels) and stays as it is.
func setLabel(holder map[string]any, key, value string) {
	metadata, _ := holder["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		holder["metadata"] = metadata
	}
	raw, _ := metadata["labels"].(map[string]any)
	labelled := make(map[string]any, len(raw)+1)
	maps.Copy(labelled, raw)
	labelled[key] = value
	metadata["labels"] = labelled
}

// stampUnstructured is stampComponentLabel on an unstructured object, its kind
// told by its API group and kind.
//
// The object's own labels are read as written (labelsOf), not through its
// accessor: GetLabels answers nil for labels that hold a value that is no
// string, so setting the label through it would replace every label the author
// wrote with the component's alone. Such an object is refused instead.
func stampUnstructured(u *unstructured.Unstructured, key, value string) error {
	gvk := u.GroupVersionKind()
	own, err := labelsOf(u.Object)
	if err != nil {
		return errors.Errorf("component label: %s %q: %w", gvk.Kind, u.GetName(), err)
	}
	if _, exists := own[key]; !exists {
		if u.Object == nil {
			u.Object = map[string]any{}
		}
		setLabel(u.Object, key, value)
	}
	if gvk.Group == helmv2.GroupVersion.Group && gvk.Kind == helmv2.HelmReleaseKind {
		return stampUnstructuredHelmRelease(u, key, value)
	}
	for _, k := range podTemplateKinds {
		if gvk.Group != k.group || gvk.Kind != k.kind {
			continue
		}
		if err := stampUnstructuredJobTemplate(u.Object, k.jobTemplate, key, value); err != nil {
			return errors.Errorf("component label: %s %q: job template: %w", gvk.Kind, u.GetName(), err)
		}
		if err := stampUnstructuredPodTemplate(u.Object, k.spec, key, value); err != nil {
			return errors.Errorf("component label: %s %q: pod template: %w", gvk.Kind, u.GetName(), err)
		}
		return nil
	}
	return nil
}

// stampUnstructuredJobTemplate puts key: value on the job template at path of
// obj where its labels carry no value for key yet, as the object's own are
// (labelsOf, setLabel). A nil path, a kind with no job template, does nothing,
// and so does an absent or null job template.
func stampUnstructuredJobTemplate(obj map[string]any, path []string, key, value string) error {
	if len(path) == 0 {
		return nil
	}
	template, found, err := nestedObject(obj, path...)
	if err != nil || !found {
		return err
	}
	own, err := labelsOf(template)
	if err != nil {
		return err
	}
	if _, exists := own[key]; !exists {
		setLabel(template, key, value)
	}
	return nil
}

// stampUnstructuredPodTemplate is withComponentLabel on the pod template under
// the spec at specPath of obj. A workload with no pod template, or a null one,
// is left as it is. The template's labels are read as the object's own are
// (labelsOf): a label that is no string is an error whether or not the template
// gets the label.
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
	// Every label is read before the key is looked for: a template that carries
	// the key already is held to string labels like any other.
	podLabels, err := labelsOf(template)
	if err != nil {
		return err
	}
	if _, exists := podLabels[key]; exists {
		return nil
	}
	// A selector that does not decode holds nothing back, as one that does not
	// parse (withComponentLabel). A PodTemplate, the one kind with no spec
	// around its template, has no selector: a field of that name on it is not
	// read as one.
	var selector *metav1.LabelSelector
	if rawSelector, ok := spec["selector"].(map[string]any); ok && len(specPath) > 0 {
		decoded := &metav1.LabelSelector{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(rawSelector, decoded) == nil {
			selector = decoded
		}
	}
	if _, added := withComponentLabel(podLabels, selector, key, value)[key]; added {
		setLabel(template, key, value)
	}
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

// coreGroupPattern is the group of a patch target for a kind of the core API
// group: the pattern that matches the empty group name and no other.
const coreGroupPattern = "^$"

// componentLabelPostRenderer builds the Flux post-renderer that puts key: value
// on the pod template of every workload and PodTemplate a chart renders, on the
// job template of every CronJob, and on a bare Pod. A Flux post-renderer offers
// kustomize patches and images only, so it is one strategic merge patch per
// kind with a pod template (podTemplateKinds) and one for Pod, each targeting
// the kind in its own API group and no other; a chart that renders none of a
// kind is left alone by that patch.
//
// It reaches what Helm hands a post-renderer. Whether that includes a chart's
// hook and test Pods depends on the Helm the helm-controller runs, and is not
// verified here.
//
// A strategic merge sets the value: unlike the label on an object launcher
// generates itself, this one replaces a value the chart gave the key.
//
// The result depends on key and value only, so a HelmRelease stamped again gets
// an equal post-renderer, which stampComponentLabel does not add twice.
func componentLabelPostRenderer(key, value string) (helmv2.PostRenderer, error) {
	type target struct {
		group, version, kind string
		// labels are the paths of the labels the patch sets: the pod
		// template's, and a CronJob's job template's beside it.
		labels [][]string
	}
	targets := make([]target, 0, len(podTemplateKinds)+1)
	for _, k := range podTemplateKinds {
		labels := [][]string{append(slices.Clone(k.spec), "template", "metadata", "labels")}
		if k.jobTemplate != nil {
			labels = append(labels, append(slices.Clone(k.jobTemplate), "metadata", "labels"))
		}
		targets = append(targets, target{k.group, k.version, k.kind, labels})
	}
	// A bare Pod has no pod template: its own labels are its pod's. It is of the
	// core API group, as the ReplicationController and the PodTemplate are
	// (podTemplateKinds).
	targets = append(targets, target{"", "v1", "Pod", [][]string{{"metadata", "labels"}}})

	patches := make([]kustomize.Patch, 0, len(targets))
	for _, k := range targets {
		doc := map[string]any{
			"apiVersion": schema.GroupVersion{Group: k.group, Version: k.version}.String(),
			"kind":       k.kind,
			"metadata":   map[string]any{"name": componentLabelPostRendererName},
		}
		// Through the YAML encoder, which quotes a value that would read back as
		// a number, a boolean or null: a label value is a string.
		for _, labels := range k.labels {
			if err := unstructured.SetNestedStringMap(doc, map[string]string{key: value}, labels...); err != nil {
				return helmv2.PostRenderer{}, errors.Errorf("component label: post-renderer patch for %s: %w", k.kind, err)
			}
		}
		raw, err := yaml.Marshal(doc)
		if err != nil {
			return helmv2.PostRenderer{}, errors.Errorf("component label: post-renderer patch for %s: %w", k.kind, err)
		}
		// A target's group, version and kind are patterns, and kustomize does
		// not test a group the target leaves empty: a target for the core group
		// would reach a custom resource of any group whose kind has the same
		// name. The pattern for the empty group keeps it to the core group.
		group := k.group
		if group == "" {
			group = coreGroupPattern
		}
		patches = append(patches, kustomize.Patch{
			Patch:  string(raw),
			Target: &kustomize.Selector{Group: group, Version: k.version, Kind: k.kind},
		})
	}
	return helmv2.PostRenderer{Kustomize: &helmv2.Kustomize{Patches: patches}}, nil
}
