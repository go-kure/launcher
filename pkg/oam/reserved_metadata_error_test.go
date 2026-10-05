package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// The refusal of a reserved key is found with errors.As, wherever the key is:
// the object's own metadata, a pod template's, a Cluster's inheritedMetadata, a
// list member's. What is found says whose object it is, which object, where on
// it the key is and the entry that reserves it, and prints the whole text, which
// says the key is reserved for the platform and names no Go field. It still answers to
// ErrReservedMetadataKey.
func TestReservedMetadataKeyError(t *testing.T) {
	const (
		byKey    = ` may not be set: the key is reserved for the platform: oam: metadata key is reserved`
		byPrefix = ` may not be set: the prefix "platform.example/" is reserved for the platform: oam: metadata key is reserved`
	)
	configMap := schema.GroupKind{Kind: "ConfigMap"}
	typedMeta := metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "shop"}}
	deployment.Spec.Template.Labels = map[string]string{"platform.example/zone": "a"}

	for name, tc := range map[string]struct {
		component string
		obj       client.Object
		want      ReservedMetadataKeyError
		text      string
	}{
		"a label of the object's own, reserved as a key": {
			component: "web",
			obj: &corev1.ConfigMap{TypeMeta: typedMeta, ObjectMeta: metav1.ObjectMeta{
				Name: "c", Namespace: "shop", Labels: map[string]string{"example.org/tenant": "a"},
			}},
			want: ReservedMetadataKeyError{
				Component: "web", Kind: configMap, Namespace: "shop", Name: "c", Object: `ConfigMap "c"`,
				Key: "example.org/tenant", Entry: "example.org/tenant",
			},
			text: `component "web": ConfigMap "c": label "example.org/tenant"` + byKey,
		},
		"an annotation of the object's own, under a reserved prefix": {
			component: "web",
			obj: &networkingv1.Ingress{
				TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
				ObjectMeta: metav1.ObjectMeta{Name: "i", Annotations: map[string]string{"platform.example/zone": "a"}},
			},
			want: ReservedMetadataKeyError{
				Component: "web", Kind: schema.GroupKind{Group: "networking.k8s.io", Kind: "Ingress"}, Name: "i", Object: `Ingress "i"`,
				Annotation: true, Key: "platform.example/zone", Entry: "platform.example/",
			},
			text: `component "web": Ingress "i": annotation "platform.example/zone"` + byPrefix,
		},
		// A typed workload that states no kind is told by its Go type.
		"a pod template's label": {
			component: "web",
			obj:       deployment,
			want: ReservedMetadataKeyError{
				Component: "web", Kind: schema.GroupKind{Group: "apps", Kind: "Deployment"}, Namespace: "shop", Name: "w", Object: `Deployment "w"`,
				Holder: ReservedKeyInPodTemplate, Key: "platform.example/zone", Entry: "platform.example/",
			},
			text: `component "web": Deployment "w": pod template label "platform.example/zone"` + byPrefix,
		},
		"a pod template's annotation, on a CronJob": {
			component: "web",
			obj:       reservedWorkload(t, "batch/v1", "CronJob", "annotations", "example.org/tenant"),
			want: ReservedMetadataKeyError{
				Component: "web", Kind: schema.GroupKind{Group: "batch", Kind: "CronJob"}, Name: "w", Object: `CronJob "w"`,
				Holder: ReservedKeyInPodTemplate, Annotation: true, Key: "example.org/tenant", Entry: "example.org/tenant",
			},
			text: `component "web": CronJob "w": pod template annotation "example.org/tenant"` + byKey,
		},
		"a Cluster's inherited annotation": {
			component: "db",
			obj:       cnpgCluster("postgresql.cnpg.io/v1", "Cluster", map[string]any{"annotations": map[string]any{"platform.example/zone": "a"}}),
			want: ReservedMetadataKeyError{
				Component: "db", Kind: schema.GroupKind{Group: "postgresql.cnpg.io", Kind: "Cluster"}, Name: "db", Object: `Cluster "db"`,
				Holder: ReservedKeyInInheritedMetadata, Annotation: true, Key: "platform.example/zone", Entry: "platform.example/",
			},
			text: `component "db": Cluster "db": spec.inheritedMetadata annotation "platform.example/zone"` + byPrefix,
		},
		// Named by its Go type in the text, and with no Kind.
		"a typed object that states no kind": {
			component: "web",
			obj:       &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}},
			want: ReservedMetadataKeyError{
				Component: "web", Name: "c", Object: `*v1.ConfigMap "c"`,
				Key: "example.org/tenant", Entry: "example.org/tenant",
			},
			text: `component "web": *v1.ConfigMap "c": label "example.org/tenant"` + byKey,
		},
		"a member of a List": {
			component: "web",
			obj: &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
				map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "plain"}},
				map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{
					"name": "member", "namespace": "shop", "labels": map[string]any{"platform.example/zone": "a"},
				}},
			}}},
			want: ReservedMetadataKeyError{
				Component: "web", Kind: configMap, Namespace: "shop", Name: "member", Object: `ConfigMap "member"`,
				Key: "platform.example/zone", Entry: "platform.example/",
			},
			text: `component "web": ConfigMap "member": label "platform.example/zone"` + byPrefix,
		},
		"an object the document as a whole owns": {
			obj: &corev1.ConfigMap{TypeMeta: typedMeta, ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}},
			want: ReservedMetadataKeyError{
				Kind: configMap, Name: "c", Object: `ConfigMap "c"`,
				Key: "example.org/tenant", Entry: "example.org/tenant",
			},
			text: `the document: ConfigMap "c": label "example.org/tenant"` + byKey,
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{tc.obj}}
			wrapped := wrapOwnedConfigReserving(inner, tc.component, ownershipKey, mustReserve(t, reservedForTest...))
			_, err := stack.NewApplication("app", "ns", wrapped).Generate()

			var got *ReservedMetadataKeyError
			if !errors.As(err, &got) {
				t.Fatalf("Generate = %v\nwant a *ReservedMetadataKeyError", err)
			}
			if !errors.Is(err, ErrReservedMetadataKey) {
				t.Errorf("Generate = %v\nwant it to answer to ErrReservedMetadataKey", err)
			}
			if *got != tc.want {
				t.Errorf("the refusal is\n  %+v\nwant\n  %+v", *got, tc.want)
			}
			if err.Error() != tc.text {
				t.Errorf("Generate = %v\nwant       %s", err, tc.text)
			}
		})
	}
}

// Only the refusal of a reserved key is one: metadata the check cannot read
// fails generation with another error, and a caller's wrapping hides neither.
func TestReservedMetadataKeyError_ToldApart(t *testing.T) {
	reserved := mustReserve(t, reservedForTest...)
	generate := func(obj client.Object) error {
		inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
		_, err := stack.NewApplication("app", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)).Generate()
		return err
	}

	err := generate(&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "c", "labels": []any{"example.org/tenant"}}}})
	if err == nil {
		t.Fatal("Generate accepted labels that are a list")
	}
	var got *ReservedMetadataKeyError
	if errors.As(err, &got) || errors.Is(err, ErrReservedMetadataKey) {
		t.Errorf("unreadable metadata answers as a reserved key: %v", err)
	}

	err = generate(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Annotations: map[string]string{"example.org/tenant": "a"}}})
	wrapped := errors.Wrap(errors.Wrap(err, "generating"), "building the cluster")
	if !errors.As(wrapped, &got) || !errors.Is(wrapped, ErrReservedMetadataKey) {
		t.Fatalf("a wrapped refusal = %v\nwant a *ReservedMetadataKeyError that answers to ErrReservedMetadataKey", wrapped)
	}
	if !got.Annotation || got.Key != "example.org/tenant" || got.Holder != ReservedKeyInObjectMetadata {
		t.Errorf("the refusal is %+v, want the annotation on the object's own metadata", *got)
	}
}
