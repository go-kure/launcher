package oam

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reservedForTest reserves one exact key and one prefix.
var reservedForTest = []string{"example.org/tenant", "platform.example/"}

func mustReserve(t *testing.T, entries ...string) *reservedMetadataKeys {
	t.Helper()
	reserved, err := parseReservedMetadataKeys(entries)
	if err != nil {
		t.Fatalf("parseReservedMetadataKeys(%v): %v", entries, err)
	}
	return reserved
}

func TestParseReservedMetadataKeys(t *testing.T) {
	for _, entries := range [][]string{nil, {}} {
		reserved, err := parseReservedMetadataKeys(entries)
		if err != nil || reserved != nil {
			t.Errorf("parseReservedMetadataKeys(%#v) = %v, %v, want nothing reserved", entries, reserved, err)
		}
	}

	// A repeated entry and a key a prefix already covers are accepted.
	reserved := mustReserve(t, "team", "example.org/tenant", "platform.example/", "platform.example/", "platform.example/zone", "team")
	for key, want := range map[string]string{
		"team":                     "team",
		"example.org/tenant":       "example.org/tenant",
		"platform.example/zone":    "platform.example/zone", // the exact entry is named before the prefix
		"platform.example/anykey":  "platform.example/",
		"platform.example/a.b_c-d": "platform.example/",
	} {
		if got, ok := reserved.entryFor(key); !ok || got != want {
			t.Errorf("entryFor(%q) = %q, %v, want %q", key, got, ok, want)
		}
	}
	for _, key := range []string{
		"example.org/tenants",      // an exact key is no prefix
		"example.org/other",        // the domain of an exact key is not reserved
		"platform.example",         // the prefix's own name is a key without one
		"sub.platform.example/key", // another domain that ends alike
		"notplatform.example/key",
		"teams",
		"app",
	} {
		if got, ok := reserved.entryFor(key); ok {
			t.Errorf("entryFor(%q) = %q, want it not reserved", key, got)
		}
	}
	if _, ok := (*reservedMetadataKeys)(nil).entryFor("team"); ok {
		t.Error("a nil list reserves a key")
	}

	for name, tc := range map[string]struct {
		entries []string
		want    string
	}{
		"an empty entry":             {[]string{""}, `ReservedMetadataKeys[0] ""`},
		"a slash alone":              {[]string{"team", "/"}, `ReservedMetadataKeys[1] "/"`},
		"a prefix that is no domain": {[]string{"Platform.Example/"}, `the prefix before "/"`},
		"a prefix with a path":       {[]string{"platform.example/a/"}, `the prefix before "/"`},
		"a key with two slashes":     {[]string{"platform.example/a/b"}, "neither a key nor a prefix"},
		"a key with a space":         {[]string{"my key"}, "neither a key nor a prefix"},
		"a wildcard":                 {[]string{"platform.example/*"}, "neither a key nor a prefix"},
	} {
		t.Run(name, func(t *testing.T) {
			reserved, err := parseReservedMetadataKeys(tc.entries)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "TransformContext.ReservedMetadataKeys") {
				t.Fatalf("error = %v, want one naming the field and %s", err, tc.want)
			}
			if reserved != nil {
				t.Errorf("reserved = %+v beside an error", reserved)
			}
		})
	}
}

// reservedWorkload is an unstructured workload of kind whose pod template
// metadata holds field (labels or annotations) with key.
func reservedWorkload(t *testing.T, apiVersion, kind, field, key string) *unstructured.Unstructured {
	t.Helper()
	u := unstructuredWorkload(apiVersion, kind)
	path := []string{"spec", "template", "metadata", field}
	switch kind {
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "metadata", field}
	case "PodTemplate":
		path = []string{"template", "metadata", field}
	}
	if err := unstructured.SetNestedField(u.Object, map[string]any{key: "x"}, path...); err != nil {
		t.Fatal(err)
	}
	return u
}

func cnpgCluster(apiVersion, kind string, inherited map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "db"},
		"spec":       map[string]any{"inheritedMetadata": inherited},
	}}
}

// TestOwnedConfig_ReservedKeyRefused: a reserved key is refused wherever the
// check reads: the object's own labels and annotations, a pod template's, a
// CloudNativePG Cluster's inheritedMetadata, and a list member's. The refusal
// names the component, the object, the key and the entry that reserves it.
func TestOwnedConfig_ReservedKeyRefused(t *testing.T) {
	typedPods := func(labels, annotations map[string]string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Labels: labels, Annotations: annotations}
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	deployment.Spec.Template.ObjectMeta = typedPods(map[string]string{"platform.example/zone": "a"}, nil)
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	statefulSet.Spec.Template.ObjectMeta = typedPods(nil, map[string]string{"example.org/tenant": "a"})
	cronJob := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	cronJob.Spec.JobTemplate.Spec.Template.ObjectMeta = typedPods(map[string]string{"platform.example/zone": "a"}, nil)
	podTemplate := &corev1.PodTemplate{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	podTemplate.Template.ObjectMeta = typedPods(nil, map[string]string{"platform.example/zone": "a"})
	replicationController := &corev1.ReplicationController{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	replicationController.Spec.Template = &corev1.PodTemplateSpec{ObjectMeta: typedPods(map[string]string{"platform.example/zone": "a"}, nil)}

	const prefix = `the prefix "platform.example/" is reserved`
	const exact = "it is a reserved key"
	for name, tc := range map[string]struct {
		obj  client.Object
		want []string
	}{
		"a typed object's label": {
			&corev1.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}},
			[]string{`ConfigMap "c"`, `: label "example.org/tenant"`, exact},
		},
		"a typed object's annotation": {
			&networkingv1.Ingress{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"}, ObjectMeta: metav1.ObjectMeta{Name: "i", Annotations: map[string]string{"platform.example/zone": "a"}}},
			[]string{`Ingress "i"`, `: annotation "platform.example/zone"`, prefix},
		},
		// A typed object that states no kind is named by its Go type.
		"a typed object's label, no kind stated": {
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}},
			[]string{`*v1.ConfigMap "c"`, `: label "example.org/tenant"`, exact},
		},
		"an unstructured object's label": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "c", "labels": map[string]any{"platform.example/zone": "a"}}}},
			[]string{`ConfigMap "c"`, `: label "platform.example/zone"`, prefix},
		},
		// A key is read whatever its value: a value the API server would refuse
		// does not hide the key.
		"an unstructured annotation that is no string": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "c", "annotations": map[string]any{"example.org/tenant": int64(1)}}}},
			[]string{`ConfigMap "c"`, `: annotation "example.org/tenant"`, exact},
		},
		"an unstructured label that is null": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "c", "labels": map[string]any{"example.org/tenant": nil}}}},
			[]string{`ConfigMap "c"`, `: label "example.org/tenant"`, exact},
		},
		"a typed Deployment's pod template label": {
			deployment, []string{`Deployment "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"a typed StatefulSet's pod template annotation": {
			statefulSet, []string{`StatefulSet "w"`, `pod template annotation "example.org/tenant"`, exact},
		},
		"a typed CronJob's pod template label": {
			cronJob, []string{`CronJob "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"a typed PodTemplate's pod template annotation": {
			podTemplate, []string{`PodTemplate "w"`, `pod template annotation "platform.example/zone"`, prefix},
		},
		"a typed ReplicationController's pod template label": {
			replicationController, []string{`ReplicationController "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"an unstructured Deployment's pod template annotation": {
			reservedWorkload(t, "apps/v1", "Deployment", "annotations", "platform.example/zone"),
			[]string{`Deployment "w"`, `pod template annotation "platform.example/zone"`, prefix},
		},
		"an unstructured DaemonSet's pod template label": {
			reservedWorkload(t, "apps/v1", "DaemonSet", "labels", "example.org/tenant"),
			[]string{`DaemonSet "w"`, `pod template label "example.org/tenant"`, exact},
		},
		"an unstructured Job's pod template label": {
			reservedWorkload(t, "batch/v1", "Job", "labels", "example.org/tenant"),
			[]string{`Job "w"`, `pod template label "example.org/tenant"`, exact},
		},
		"an unstructured CronJob's pod template label": {
			reservedWorkload(t, "batch/v1", "CronJob", "labels", "platform.example/zone"),
			[]string{`CronJob "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"an unstructured ReplicaSet's pod template label": {
			reservedWorkload(t, "apps/v1", "ReplicaSet", "labels", "platform.example/zone"),
			[]string{`ReplicaSet "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"an unstructured PodTemplate's pod template label": {
			reservedWorkload(t, "v1", "PodTemplate", "labels", "platform.example/zone"),
			[]string{`PodTemplate "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"a Cluster's inherited label": {
			cnpgCluster("postgresql.cnpg.io/v1", "Cluster", map[string]any{"labels": map[string]any{"platform.example/zone": "a"}}),
			[]string{`Cluster "db"`, `spec.inheritedMetadata label "platform.example/zone"`, prefix},
		},
		"a Cluster's inherited annotation": {
			cnpgCluster("postgresql.cnpg.io/v1", "Cluster", map[string]any{"annotations": map[string]any{"example.org/tenant": "a"}}),
			[]string{`Cluster "db"`, `spec.inheritedMetadata annotation "example.org/tenant"`, exact},
		},
		// A typed Cluster that states no kind is told by its Go type.
		"a typed Cluster's inherited label, no kind stated": {
			&cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "db"}, Spec: cnpgv1.ClusterSpec{
				InheritedMetadata: &cnpgv1.EmbeddedObjectMetadata{Labels: map[string]string{"platform.example/zone": "a"}},
			}},
			[]string{`Cluster "db"`, `spec.inheritedMetadata label "platform.example/zone"`, prefix},
		},
		"a typed Cluster's inherited annotation, no kind stated": {
			&cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "db"}, Spec: cnpgv1.ClusterSpec{
				InheritedMetadata: &cnpgv1.EmbeddedObjectMetadata{Annotations: map[string]string{"example.org/tenant": "a"}},
			}},
			[]string{`Cluster "db"`, `spec.inheritedMetadata annotation "example.org/tenant"`, exact},
		},
		"a List member's label": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
				map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "plain"}},
				map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "member", "labels": map[string]any{"example.org/tenant": "a"}}},
			}}},
			[]string{`ConfigMap "member"`, `: label "example.org/tenant"`, exact},
		},
		"the member of a list inside a List": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
				map[string]any{"apiVersion": "v1", "kind": "ConfigMapList", "items": []any{
					map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "inner"}, "spec": map[string]any{
						"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"platform.example/zone": "a"}}},
					}},
				}},
			}}},
			[]string{`Deployment "inner"`, `pod template annotation "platform.example/zone"`, prefix},
		},
		"the members of an envelope only Flux expands": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Widget", "metadata": map[string]any{"name": "envelope"}, "items": []any{
				map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "member", "annotations": map[string]any{"example.org/tenant": "a"}}},
			}}},
			[]string{`ConfigMap "member"`, `: annotation "example.org/tenant"`, exact},
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{tc.obj}}
			wrapped := wrapOwnedConfigReserving(inner, "web", ownershipKey, mustReserve(t, reservedForTest...))
			objs, err := stack.NewApplication("web", "ns", wrapped).Generate()
			if !errors.Is(err, ErrReservedMetadataKey) {
				t.Fatalf("Generate = %v, want ErrReservedMetadataKey", err)
			}
			if objs != nil {
				t.Errorf("Generate returned %d objects beside the refusal", len(objs))
			}
			for _, want := range append([]string{`component "web"`, "TransformContext.ReservedMetadataKeys"}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			}
			// Refused before the stamp: the object is as its config made it.
			if _, stamped := tc.obj.GetLabels()[ownershipKey]; stamped {
				t.Error("the refused object carries the component label")
			}
		})
	}
}

// TestOwnedConfig_ReservedKeyNotRead is the control: what the check does not
// read, and the keys a config writes itself, pass and are stamped as without a
// list.
func TestOwnedConfig_ReservedKeyNotRead(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: map[string]string{"app": "web", ownershipKey: "authored"}}}
	deployment.Spec.Template.Labels = map[string]string{"app": "web", ownershipKey: "authored"}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	statefulSet.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: map[string]string{"example.org/tenant": "a"}}}}
	cronJob := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	cronJob.Spec.JobTemplate.Labels = map[string]string{"example.org/tenant": "a"}

	// "app" and the component label key are reserved here, as a consumer that
	// keeps the component key under a prefix of its own has them.
	reserved := mustReserve(t, "app", "launcher.gokure.dev/", "example.org/tenant", "platform.example/")
	for name, obj := range map[string]client.Object{
		"the app label and the component label, typed": deployment,
		"the app label and the component label, unstructured": &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "w", "labels": map[string]any{"app": "web", ownershipKey: "authored"}},
			"spec":     map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "web", ownershipKey: "authored"}}}},
		}},
		"the two labels a Cluster's objects inherit": cnpgCluster("postgresql.cnpg.io/v1", "Cluster",
			map[string]any{"labels": map[string]any{"app": "db", ownershipKey: "authored"}}),
		"a volume claim template's label": statefulSet,
		"a job template's label":          cronJob,
		"inheritedMetadata of a Cluster of another group": cnpgCluster("example.com/v1", "Cluster",
			map[string]any{"labels": map[string]any{"example.org/tenant": "a"}}),
		"inheritedMetadata of another kind of the group": cnpgCluster("postgresql.cnpg.io/v1", "Pooler",
			map[string]any{"labels": map[string]any{"example.org/tenant": "a"}}),
		"a pod template of a kind of another group": reservedWorkload(t, "example.com/v1", "Job", "labels", "example.org/tenant"),
		"commonMetadata a Flux object hands on": &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization", "metadata": map[string]any{"name": "k"},
			"spec": map[string]any{"commonMetadata": map[string]any{"labels": map[string]any{"example.org/tenant": "a"}}},
		}},
		"a key that only starts like an entry": &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c",
			Labels:      map[string]string{"example.org/tenants": "a", "platform.example": "a"},
			Annotations: map[string]string{"sub.platform.example/zone": "a"}}},
		"null metadata": &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": nil}},
		"null labels and annotations": &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "c", "labels": nil, "annotations": nil}}},
		"a null pod template": &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "w"}, "spec": map[string]any{"template": nil}}},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
			wrapped := wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)
			if _, err := stack.NewApplication("web", "ns", wrapped).Generate(); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if _, stamped := obj.GetLabels()[ownershipKey]; !stamped {
				t.Error("the object carries no component label")
			}
		})
	}
}

// TestOwnedConfig_ReservedExemptionsAreLabels: "app" and the component label
// key are exempt as label keys, the two labels a config writes itself. The same
// keys as annotations are checked as any other.
func TestOwnedConfig_ReservedExemptionsAreLabels(t *testing.T) {
	reserved := mustReserve(t, "app", "launcher.gokure.dev/")
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	deployment.Spec.Template.Annotations = map[string]string{ownershipKey: "x"}
	for name, tc := range map[string]struct {
		obj  client.Object
		want string
	}{
		"app as an annotation": {
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Annotations: map[string]string{"app": "web"}}},
			`annotation "app"`,
		},
		"the component key as a pod template annotation": {deployment, `pod template annotation "` + ownershipKey + `"`},
		"another key under the component key's prefix": {
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"launcher.gokure.dev/other": "x"}}},
			`label "launcher.gokure.dev/other"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{tc.obj}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)).Generate()
			if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Generate = %v, want ErrReservedMetadataKey for the %s", err, tc.want)
			}
		})
	}
}

// TestOwnedConfig_ReservedKeyOnADocumentOwnedApplication: an application the
// document as a whole owns is checked too, and the refusal says so. Its objects
// get no component label, as without a list.
func TestOwnedConfig_ReservedKeyOnADocumentOwnedApplication(t *testing.T) {
	reserved := mustReserve(t, reservedForTest...)
	refused := &ownershipObjectsConfig{objects: []client.Object{
		&corev1.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}},
	}}
	_, err := stack.NewApplication("shop-source", "ns", wrapOwnedConfigReserving(refused, "", ownershipKey, reserved)).Generate()
	if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), `the document: ConfigMap "c"`) {
		t.Fatalf("Generate = %v, want ErrReservedMetadataKey naming the document and the ConfigMap", err)
	}
	if strings.Contains(err.Error(), "component") {
		t.Errorf("refusal %q names a component for an application that has none", err)
	}

	plain := &ownershipObjectsConfig{objects: []client.Object{&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c"}}}}
	objs, err := stack.NewApplication("shop-source", "ns", wrapOwnedConfigReserving(plain, "", ownershipKey, reserved)).Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if labels := (*objs[0]).GetLabels(); len(labels) != 0 {
		t.Errorf("document-owned object labels = %v, want none", labels)
	}
}

// reservedAugmenter hands out no object and adds obj to its layout, in a child.
type reservedAugmenter struct {
	ownershipObjectsConfig
	added client.Object
}

func (c *reservedAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	l.Children = append(l.Children, &layout.ManifestLayout{Name: "hook", Resources: []client.Object{c.added}})
	return nil
}

// TestOwnedConfig_ReservedKeyInAnAugmentedLayout: what a layout augmenter adds
// outside Generate (a rendered chart's hook groups) is checked as well, for a
// component and for a document-owned application.
func TestOwnedConfig_ReservedKeyInAnAugmentedLayout(t *testing.T) {
	reserved := mustReserve(t, reservedForTest...)
	for _, component := range []string{"web", ""} {
		added := &corev1.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: "hook", Annotations: map[string]string{"platform.example/zone": "a"}}}
		aug, ok := wrapOwnedConfigReserving(&reservedAugmenter{added: added}, component, ownershipKey, reserved).(layout.LayoutAugmenter)
		if !ok {
			t.Fatal("a wrapped augmenter is no LayoutAugmenter")
		}
		err := aug.AugmentLayout(&layout.ManifestLayout{})
		if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), `ConfigMap "hook": annotation "platform.example/zone"`) {
			t.Errorf("component %q: AugmentLayout = %v, want ErrReservedMetadataKey naming the ConfigMap and the annotation", component, err)
		}
	}

	added := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "hook"}}
	aug := wrapOwnedConfigReserving(&reservedAugmenter{added: added}, "web", ownershipKey, reserved).(layout.LayoutAugmenter)
	if err := aug.AugmentLayout(&layout.ManifestLayout{}); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if got := added.Labels[ownershipKey]; got != "web" {
		t.Errorf("augmenter-added object label = %q, want web", got)
	}
}

// platformConfig is a config that vouches for annotations as the platform's.
type platformConfig struct {
	ownershipObjectsConfig
	platform map[string]string
}

func (c *platformConfig) PlatformAnnotations() map[string]string { return c.platform }

// TestOwnedConfig_PlatformAnnotations: an annotation a config states the
// platform wrote passes as that key and value pair, on the object's own
// annotations only. The same key with another value, as a label, or on a pod
// template is checked as authored.
func TestOwnedConfig_PlatformAnnotations(t *testing.T) {
	reserved := mustReserve(t, "platform.example/")
	platform := map[string]string{"platform.example/issuer": "letsencrypt"}
	generate := func(obj client.Object, platform map[string]string) error {
		inner := &platformConfig{ownershipObjectsConfig{objects: []client.Object{obj}}, platform}
		_, err := stack.NewApplication("web-ingress", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)).Generate()
		return err
	}
	ingress := func(annotations map[string]string) *networkingv1.Ingress {
		return &networkingv1.Ingress{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"}, ObjectMeta: metav1.ObjectMeta{Name: "i", Annotations: annotations}}
	}

	if err := generate(ingress(map[string]string{"platform.example/issuer": "letsencrypt", "plain": "x"}), platform); err != nil {
		t.Errorf("the platform's own pair: %v", err)
	}
	unstructuredIngress := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress",
		"metadata": map[string]any{"name": "i", "annotations": map[string]any{"platform.example/issuer": "letsencrypt"}}}}
	if err := generate(unstructuredIngress, platform); err != nil {
		t.Errorf("the platform's own pair, unstructured: %v", err)
	}

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	deployment.Spec.Template.Annotations = map[string]string{"platform.example/issuer": "letsencrypt"}
	for name, tc := range map[string]struct {
		obj      client.Object
		platform map[string]string
		want     string
	}{
		"another value":                  {ingress(map[string]string{"platform.example/issuer": "other"}), platform, `: annotation "platform.example/issuer"`},
		"another key the config writes":  {ingress(map[string]string{"platform.example/issuer": "letsencrypt", "platform.example/zone": "a"}), platform, `: annotation "platform.example/zone"`},
		"a config that vouches for none": {ingress(map[string]string{"platform.example/issuer": "letsencrypt"}), nil, `: annotation "platform.example/issuer"`},
		"the pair as a label": {
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"platform.example/issuer": "letsencrypt"}}},
			platform, `: label "platform.example/issuer"`,
		},
		"the pair on a pod template": {deployment, platform, `pod template annotation "platform.example/issuer"`},
		"a value that is no string": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
				"metadata": map[string]any{"name": "c", "annotations": map[string]any{"platform.example/issuer": true}}}},
			map[string]string{"platform.example/issuer": "true"}, `: annotation "platform.example/issuer"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := generate(tc.obj, tc.platform)
			if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Generate = %v, want ErrReservedMetadataKey for the%s", err, tc.want)
			}
		})
	}
}

// sayingWrapper is a caller's own wrapper around a config, one that says so
// (ConfigWrapper). It vouches for nothing itself: platform is what a
// vouchingWrapper answers.
type sayingWrapper struct {
	inner    stack.ApplicationConfig
	platform map[string]string
}

func (w *sayingWrapper) Generate(app *stack.Application) ([]*client.Object, error) {
	return w.inner.Generate(app)
}

func (w *sayingWrapper) WrappedApplicationConfig() stack.ApplicationConfig { return w.inner }

// vouchingWrapper is a sayingWrapper that vouches for annotations of its own.
type vouchingWrapper struct{ sayingWrapper }

func (w *vouchingWrapper) PlatformAnnotations() map[string]string { return w.platform }

// silentWrapper wraps a config without saying so: nothing can look under it.
type silentWrapper struct{ inner stack.ApplicationConfig }

func (w *silentWrapper) Generate(app *stack.Application) ([]*client.Object, error) {
	return w.inner.Generate(app)
}

// TestOwnedConfig_PlatformAnnotationsUnderAWrapper: the check finds the config
// that vouches for the platform's annotations under every wrapper that says it
// wraps (ConfigWrapper), as UnwrapConfig walks them, so a wrapper between the
// ownership wrapper and that config does not turn the platform's own pairs into
// refused ones. Each layer's pairs count. A wrapper that does not say it wraps
// hides them, and the pairs are then checked as authored.
func TestOwnedConfig_PlatformAnnotationsUnderAWrapper(t *testing.T) {
	reserved := mustReserve(t, "platform.example/")
	const issuer, zone = "platform.example/issuer", "platform.example/zone"
	ingress := func(annotations map[string]string) client.Object {
		return &networkingv1.Ingress{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"}, ObjectMeta: metav1.ObjectMeta{Name: "i", Annotations: annotations}}
	}
	vouching := func(annotations map[string]string) stack.ApplicationConfig {
		return &platformConfig{ownershipObjectsConfig{objects: []client.Object{ingress(annotations)}}, map[string]string{issuer: "letsencrypt"}}
	}
	generate := func(inner stack.ApplicationConfig) error {
		_, err := stack.NewApplication("web-ingress", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)).Generate()
		return err
	}
	own := map[string]string{issuer: "letsencrypt"}
	both := map[string]string{issuer: "letsencrypt", zone: "a"}

	for name, inner := range map[string]stack.ApplicationConfig{
		"one wrapper":  &sayingWrapper{inner: vouching(own)},
		"two wrappers": &sayingWrapper{inner: &sayingWrapper{inner: vouching(own)}},
		"a wrapper that vouches for a pair of its own": &vouchingWrapper{sayingWrapper{inner: vouching(both), platform: map[string]string{zone: "a"}}},
		"a wrapper that vouches for none":              &vouchingWrapper{sayingWrapper{inner: vouching(own)}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := generate(inner); err != nil {
				t.Fatalf("Generate: %v", err)
			}
		})
	}

	for name, tc := range map[string]struct {
		inner stack.ApplicationConfig
		want  string
	}{
		"a pair no layer vouches for":              {&sayingWrapper{inner: vouching(both)}, zone},
		"a layer's pair with another value":        {&vouchingWrapper{sayingWrapper{inner: vouching(both), platform: map[string]string{zone: "b"}}}, zone},
		"a wrapper that does not say it wraps":     {&silentWrapper{inner: vouching(own)}, issuer},
		"a saying wrapper around a silent wrapper": {&sayingWrapper{inner: &silentWrapper{inner: vouching(own)}}, issuer},
	} {
		t.Run(name, func(t *testing.T) {
			err := generate(tc.inner)
			if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), `: annotation "`+tc.want+`"`) {
				t.Fatalf("Generate = %v, want ErrReservedMetadataKey for the annotation %s", err, tc.want)
			}
		})
	}
}

// TestOwnedConfig_ReservedKeysMalformedMetadata: metadata the check cannot read
// is refused with the object named, not read as holding no key.
func TestOwnedConfig_ReservedKeysMalformedMetadata(t *testing.T) {
	reserved := mustReserve(t, reservedForTest...)
	for name, tc := range map[string]struct {
		object map[string]any
		want   string
	}{
		"metadata that is a text": {map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": "oops"}, "metadata is a"},
		"labels that are a list": {map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "c", "labels": []any{"example.org/tenant"}}}, "labels is a"},
		"annotations that are a text": {map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "c", "annotations": "example.org/tenant"}}, "annotations is a"},
		"a pod template that is a list": {map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "c"}, "spec": map[string]any{"template": []any{}}}, "template is a"},
		"inheritedMetadata that is a text": {map[string]any{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster",
			"metadata": map[string]any{"name": "c"}, "spec": map[string]any{"inheritedMetadata": "oops"}}, "inheritedMetadata is a"},
	} {
		t.Run(name, func(t *testing.T) {
			u := &unstructured.Unstructured{Object: tc.object}
			want := u.DeepCopy()
			inner := &ownershipObjectsConfig{objects: []client.Object{u}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfigReserving(inner, "", ownershipKey, reserved)).Generate()
			if err == nil || !strings.Contains(err.Error(), "reserved metadata keys: "+u.GetKind()) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Generate = %v, want one naming the %s and %q", err, u.GetKind(), tc.want)
			}
			if errors.Is(err, ErrReservedMetadataKey) {
				t.Errorf("error %q is ErrReservedMetadataKey, want it told apart from a reserved key", err)
			}
			if !reflect.DeepEqual(u.Object, want.Object) {
				t.Errorf("object = %v, want it as written: %v", u.Object, want.Object)
			}
		})
	}
}

// TestOwnedConfig_NoReservedKeys: without a list the wrapper is as before, and
// a typed nil object is skipped with one.
func TestOwnedConfig_NoReservedKeys(t *testing.T) {
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}}
	inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
	if _, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate(); err != nil {
		t.Fatalf("Generate without a list: %v", err)
	}

	var typedNil *corev1.ConfigMap
	withNil := &ownershipObjectsConfig{objects: []client.Object{typedNil}}
	wrapped := wrapOwnedConfigReserving(withNil, "web", ownershipKey, mustReserve(t, reservedForTest...))
	if _, err := stack.NewApplication("web", "ns", wrapped).Generate(); err != nil {
		t.Fatalf("Generate over a typed nil object: %v", err)
	}
}

// labelledHandler is a component whose one ConfigMap carries the labels and
// annotations its properties state.
type labelledHandler struct{}

func (labelledHandler) CanHandle(t string) bool { return t == "labelled" }

func (labelledHandler) ToApplicationConfig(c *Component, namespace string) (stack.ApplicationConfig, error) {
	cm := &corev1.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: c.Name, Namespace: namespace}}
	for field, set := range map[string]func(map[string]string){"labels": cm.SetLabels, "annotations": cm.SetAnnotations} {
		authored, _ := c.Properties[field].(map[string]any)
		values := map[string]string{}
		for k, v := range authored {
			values[k], _ = v.(string)
		}
		set(values)
	}
	return &labelledConfig{obj: cm}, nil
}

type labelledConfig struct{ obj client.Object }

func (c *labelledConfig) Generate(*stack.Application) ([]*client.Object, error) {
	return []*client.Object{&c.obj}, nil
}

// TestTransform_ReservedMetadataKeys: the context field is validated by the
// transform, and held against every application the transform returns: a
// component's own, a trait sub-application's. The component label the transform
// stamps is never read, also under a prefix the list reserves.
func TestTransform_ReservedMetadataKeys(t *testing.T) {
	transformer := func() *Transformer {
		tr := ownershipTransformer()
		tr.RegisterComponent("labelled", labelledHandler{})
		return tr
	}
	doc := func(labels map[string]any) *Application {
		app := makeApp("shop",
			Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "settings", Properties: map[string]any{"name": "web-settings"}}}},
			Component{Name: "tagged", Type: "labelled", Properties: map[string]any{"labels": labels}},
		)
		app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
		return app
	}

	_, err := transformer().Transform(doc(nil), TransformContext{ReservedMetadataKeys: []string{"team", "not a key"}})
	if err == nil || !strings.Contains(err.Error(), `TransformContext.ReservedMetadataKeys[1] "not a key"`) {
		t.Fatalf("Transform = %v, want the invalid entry refused by index", err)
	}

	// The component label key sits under a reserved prefix: the stamp is exempt.
	ctx := TransformContext{ReservedMetadataKeys: []string{"launcher.gokure.dev/", "example.org/tenant"}}
	apps := generatedByName(t, transformer(), doc(map[string]any{"team": "a"}), ctx)
	for _, name := range []string{"web", "web-settings"} {
		assertOwner(t, apps, name, ComponentLabel, "web")
	}
	assertOwner(t, apps, "tagged", ComponentLabel, "tagged")

	cluster, err := transformer().Transform(doc(map[string]any{"example.org/tenant": "a"}), ctx)
	if err != nil {
		t.Fatalf("Transform: %v, want the refusal at generation", err)
	}
	_, err = GenerateApplications(cluster)
	if !errors.Is(err, ErrReservedMetadataKey) {
		t.Fatalf("GenerateApplications = %v, want ErrReservedMetadataKey", err)
	}
	for _, want := range []string{`component "tagged"`, `ConfigMap "tagged"`, `label "example.org/tenant"`, "it is a reserved key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %s", err, want)
		}
	}
}
