package oam

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// Reserved metadata keys (go-kure/launcher#790). A consumer that keeps label and
// annotation keys to itself names them in TransformContext.ReservedMetadataKeys.
// One check holds every application to the list, and it sits in the ownership
// wrapper (ownedConfig): the transform's last step wraps every application's
// config in one, so the check reads each object as its config generated it,
// whatever wrote the key there: a passthrough or manifests object, a rendered
// chart, a trait's annotations, a component's own property.
//
// It runs before the wrapper's own component label goes on, and before the
// bundle's labels and annotations do (GenerateApplications), so neither stamp is
// ever read. The two labels a config writes itself are exempt as keys:
// appLabelKey and the component label key, which a consumer may well configure
// under a prefix it reserves. An annotation launcher wrote from the platform's
// own input is exempt as a key and value pair, which the config states
// (platformAnnotated), also from under a wrapper (platformAnnotationsUnder).

// appLabelKey is the label launcher's own configs put on what they generate,
// valued ComponentLabelValue(component).
const appLabelKey = "app"

// cnpgGroup and cnpgClusterKind name the CloudNativePG Cluster, whose
// spec.inheritedMetadata the operator copies onto every object it creates for
// the cluster.
const (
	cnpgGroup       = "postgresql.cnpg.io"
	cnpgClusterKind = "Cluster"
)

// platformAnnotated is implemented by a config that writes annotations launcher
// took from the platform's own input, not from the document: the ones the
// `expose` rule derives from a ClusterProfile capability, which the `ingress`
// trait carries apart from the authored ones. The reserved-key check passes an
// annotation of the config's objects whose key and value are both in the answer.
// It vouches for a pair, not for a key: the same key with another value is
// checked as authored.
type platformAnnotated interface {
	// PlatformAnnotations returns the annotations the config writes from the
	// platform's input, nil when it writes none.
	PlatformAnnotations() map[string]string
}

// platformAnnotationLayers is how many configs, one under the other, the check
// asks for the platform's annotations (platformAnnotationsUnder). Launcher's own
// configs make one layer: the bound is there to end a chain that has no end, and
// it cuts a finite chain that is longer as well.
const platformAnnotationLayers = 32

// reservedMetadataKeys is TransformContext.ReservedMetadataKeys as the check
// reads it. A nil one reserves nothing.
type reservedMetadataKeys struct {
	exact map[string]struct{}
	// prefixes each end in "/", in sorted order.
	prefixes []string
}

// parseReservedMetadataKeys validates entries and returns them as the check
// reads them, nil for an empty list. An entry is a label or annotation key (a
// Kubernetes qualified name), or a key prefix followed by "/" (a DNS-1123
// subdomain), which reserves every key under that prefix. A repeated entry, or a
// key that a prefix entry already covers, reserves nothing more and is accepted.
func parseReservedMetadataKeys(entries []string) (*reservedMetadataKeys, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	reserved := &reservedMetadataKeys{exact: map[string]struct{}{}}
	for i, entry := range entries {
		if prefix, isPrefix := strings.CutSuffix(entry, "/"); isPrefix {
			if errs := validation.IsDNS1123Subdomain(prefix); len(errs) > 0 {
				return nil, errors.Errorf("invalid TransformContext.ReservedMetadataKeys[%d] %q: the prefix before \"/\": %s", i, entry, strings.Join(errs, "; "))
			}
			if !slices.Contains(reserved.prefixes, entry) {
				reserved.prefixes = append(reserved.prefixes, entry)
			}
			continue
		}
		if errs := validation.IsQualifiedName(entry); len(errs) > 0 {
			return nil, errors.Errorf("invalid TransformContext.ReservedMetadataKeys[%d] %q: neither a key nor a prefix ending in \"/\": %s", i, entry, strings.Join(errs, "; "))
		}
		reserved.exact[entry] = struct{}{}
	}
	slices.Sort(reserved.prefixes)
	return reserved, nil
}

// entryFor returns the entry that reserves key: the key itself, else the prefix
// entry the key starts with.
func (r *reservedMetadataKeys) entryFor(key string) (string, bool) {
	if r == nil {
		return "", false
	}
	if _, ok := r.exact[key]; ok {
		return key, true
	}
	for _, prefix := range r.prefixes {
		if strings.HasPrefix(key, prefix) {
			return prefix, true
		}
	}
	return "", false
}

// checkReserved refuses obj when it carries a reserved key the config's owner
// may not set, with ErrReservedMetadataKey. It reads, on obj and on every object
// obj stands for when Flux applies it (appliedObjects, a list envelope's
// members):
//
//   - the object's own labels and annotations;
//   - the pod template's, on a kind that has one (podTemplateKinds): they become
//     the metadata of the pods;
//   - spec.inheritedMetadata of a CloudNativePG Cluster, which is not metadata of
//     an object launcher writes: the operator copies it onto every object it
//     creates for the cluster.
//
// A key that is a string map's key is read whatever its value. Nothing else is
// read: not the metadata a Flux object hands on to what it applies
// (spec.commonMetadata), not a volume claim template's or a job template's, and
// not what a chart that Flux installs renders in the cluster.
func (o *ownedConfig) checkReserved(obj client.Object) error {
	if o.reserved == nil {
		return nil
	}
	if err := o.checkReservedObject(obj); err != nil {
		return err
	}
	if u, ok := obj.(*unstructured.Unstructured); ok {
		for _, applied := range appliedObjects(u) {
			if applied == obj {
				continue
			}
			if err := o.checkReservedObject(applied); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *ownedConfig) checkReservedObject(obj client.Object) error {
	group, kind := statedOrTypedKind(obj)
	where := fmt.Sprintf("%s %q", kind, obj.GetName())
	if kind == "" {
		// A typed object that states no kind is named by its Go type.
		where = fmt.Sprintf("%T %q", obj, obj.GetName())
	}
	content, err := objectContent(obj)
	if err != nil {
		return errors.Errorf("reserved metadata keys: %s: %w", where, err)
	}
	platform := platformAnnotationsUnder(o.inner)

	metadata, _, err := objectField(content, "metadata")
	if err != nil {
		return errors.Errorf("reserved metadata keys: %s: %w", where, err)
	}
	if err := o.checkReservedHolder(metadata, where, "", platform); err != nil {
		return err
	}

	for _, k := range podTemplateKinds {
		if group != k.group || kind != k.kind {
			continue
		}
		template, found, err := nestedObject(content, append(slices.Clone(k.spec), "template", "metadata")...)
		if err != nil {
			return errors.Errorf("reserved metadata keys: %s: %w", where, err)
		}
		if found {
			if err := o.checkReservedHolder(template, where, "pod template ", nil); err != nil {
				return err
			}
		}
	}

	if group == cnpgGroup && kind == cnpgClusterKind {
		inherited, found, err := nestedObject(content, "spec", "inheritedMetadata")
		if err != nil {
			return errors.Errorf("reserved metadata keys: %s: %w", where, err)
		}
		if found {
			if err := o.checkReservedHolder(inherited, where, "spec.inheritedMetadata ", nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// platformAnnotationsUnder returns what cfg and every config under it vouch for
// (platformAnnotated), one answer per layer that has any. It looks under each
// wrapper that says it wraps (ConfigWrapper), down to the config UnwrapConfig
// returns, so a wrapper around the config that writes the platform's annotations
// does not hide them, and needs no method of its own to hand them on. A wrapper
// that is no ConfigWrapper ends the walk: what it wraps is not read.
//
// The walk runs inside Generate on configs a caller wrote, so it ends on any
// chain: at a layer that is nil, a typed nil included, which it calls no method
// of, and after platformAnnotationLayers layers, whatever the chain: wrappers
// that say they wrap themselves or each other, and a finite chain that is longer.
// What it has read by then counts, and a pair stated below is checked as authored.
func platformAnnotationsUnder(cfg stack.ApplicationConfig) []map[string]string {
	var platform []map[string]string
	for range platformAnnotationLayers {
		if isNullValue(cfg) {
			break
		}
		if p, ok := cfg.(platformAnnotated); ok {
			if pairs := p.PlatformAnnotations(); len(pairs) > 0 {
				platform = append(platform, pairs)
			}
		}
		w, ok := cfg.(ConfigWrapper)
		if !ok {
			break
		}
		cfg = w.WrappedApplicationConfig()
	}
	return platform
}

// checkReservedHolder checks the labels and annotations holder holds, which is
// an object's metadata, a pod template's, or a Cluster's inheritedMetadata. part
// says which in the refusal, "" for the object's own. platform holds the
// annotation pairs that are exempt, on an object's own metadata only: a pair any
// of its layers states.
func (o *ownedConfig) checkReservedHolder(holder map[string]any, where, part string, platform []map[string]string) error {
	labels, _, err := objectField(holder, "labels")
	if err != nil {
		return errors.Errorf("reserved metadata keys: %s: %s%w", where, part, err)
	}
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		if key == appLabelKey || key == o.labelKey {
			continue
		}
		if entry, ok := o.reserved.entryFor(key); ok {
			return o.reservedKeyError(where, part+"label", key, entry)
		}
	}
	annotations, _, err := objectField(holder, "annotations")
	if err != nil {
		return errors.Errorf("reserved metadata keys: %s: %s%w", where, part, err)
	}
	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		if got, isString := annotations[key].(string); isString && slices.ContainsFunc(platform, func(pairs map[string]string) bool {
			want, isPlatform := pairs[key]
			return isPlatform && got == want
		}) {
			continue
		}
		if entry, ok := o.reserved.entryFor(key); ok {
			return o.reservedKeyError(where, part+"annotation", key, entry)
		}
	}
	return nil
}

// reservedKeyError is the refusal of one reserved key: it names the owner (the
// component, or the document for an application the document as a whole owns),
// the object, the key and the entry that reserves it.
func (o *ownedConfig) reservedKeyError(where, what, key, entry string) error {
	owner := "the document"
	if o.component != "" {
		owner = fmt.Sprintf("component %q", o.component)
	}
	reason := "it is a reserved key"
	if entry != key {
		reason = fmt.Sprintf("the prefix %q is reserved", entry)
	}
	return errors.Wrapf(ErrReservedMetadataKey,
		"%s: %s: %s %q may not be set: %s (TransformContext.ReservedMetadataKeys)", owner, where, what, key, reason)
}

// objectContent returns obj as the object the cluster would be sent: an
// unstructured object's own content, a typed object's converted. The check only
// reads it.
func objectContent(obj client.Object) (map[string]any, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u.Object, nil
	}
	return runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
}

// nestedObject returns the object at path below m, each step read as
// objectField reads it: an absent or null step is not found, a step that is no
// object is an error.
func nestedObject(m map[string]any, path ...string) (map[string]any, bool, error) {
	for _, field := range path {
		next, found, err := objectField(m, field)
		if err != nil || !found {
			return nil, false, err
		}
		m = next
	}
	return m, true, nil
}

// statedOrTypedKind returns obj's API group and kind: the ones it states, else,
// for a typed object that states none and is of a kind the check reads more
// than the metadata of (a pod template, a Cluster's inheritedMetadata), its Go
// type's, as stampComponentLabel tells the pod template kinds.
func statedOrTypedKind(obj client.Object) (group, kind string) {
	if gvk := obj.GetObjectKind().GroupVersionKind(); gvk.Kind != "" {
		return gvk.Group, gvk.Kind
	}
	switch obj.(type) {
	case *cnpgv1.Cluster:
		return cnpgGroup, cnpgClusterKind
	case *appsv1.Deployment:
		return appsv1.GroupName, "Deployment"
	case *appsv1.StatefulSet:
		return appsv1.GroupName, "StatefulSet"
	case *appsv1.DaemonSet:
		return appsv1.GroupName, "DaemonSet"
	case *appsv1.ReplicaSet:
		return appsv1.GroupName, "ReplicaSet"
	case *batchv1.Job:
		return batchv1.GroupName, "Job"
	case *batchv1.CronJob:
		return batchv1.GroupName, "CronJob"
	case *corev1.ReplicationController:
		return corev1.GroupName, "ReplicationController"
	case *corev1.PodTemplate:
		return corev1.GroupName, "PodTemplate"
	}
	return "", ""
}
