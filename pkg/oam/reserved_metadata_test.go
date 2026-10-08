package oam

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
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
// CronJob's job template's, what
// an operator hands on (a CloudNativePG Cluster's inheritedMetadata, a Pooler's
// pod template, the Prometheus operator's podMetadata), and a list member's. The refusal
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
	cronJobJobs := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	cronJobJobs.Spec.JobTemplate.ObjectMeta = typedPods(map[string]string{"example.org/tenant": "a"}, nil)
	podTemplate := &corev1.PodTemplate{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	podTemplate.Template.ObjectMeta = typedPods(nil, map[string]string{"platform.example/zone": "a"})
	replicationController := &corev1.ReplicationController{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	replicationController.Spec.Template = &corev1.PodTemplateSpec{ObjectMeta: typedPods(map[string]string{"platform.example/zone": "a"}, nil)}
	claimTemplates := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	claimTemplates.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		{ObjectMeta: metav1.ObjectMeta{Name: "data"}},
		{ObjectMeta: typedPods(map[string]string{"example.org/tenant": "a"}, nil)},
	}

	const prefix = `the prefix "platform.example/" is reserved for the platform`
	const exact = "the key is reserved for the platform"
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
		// Every claim template of the list is read, on a typed StatefulSet that
		// states no kind as well.
		"a typed StatefulSet's volume claim template label, in a later template": {
			claimTemplates, []string{`StatefulSet "w"`, `volume claim template label "example.org/tenant" (spec.volumeClaimTemplates[1].metadata.labels)`, exact},
		},
		"a typed CronJob's pod template label": {
			cronJob, []string{`CronJob "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		// A CronJob's job template is the metadata of every Job it creates.
		"a typed CronJob's job template label": {
			cronJobJobs, []string{`CronJob "w"`, `job template label "example.org/tenant"`, exact},
		},
		"an unstructured CronJob's job template annotation": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "batch/v1", "kind": "CronJob", "metadata": map[string]any{"name": "w"},
				"spec": map[string]any{"jobTemplate": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"platform.example/zone": "a"}}}}}},
			[]string{`CronJob "w"`, `job template annotation "platform.example/zone"`, prefix},
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
		// A Pooler's pod template, and the pod metadata of the Prometheus
		// operator's pod-running kinds: the operator puts both on its pods.
		"a Pooler's pod template label": {
			holding(t, unstructuredObject("postgresql.cnpg.io/v1", "Pooler"), map[string]string{"platform.example/zone": "a"}, "spec", "template", "metadata"),
			[]string{`Pooler "w"`, `pod template label "platform.example/zone"`, prefix},
		},
		"a typed Pooler's pod template annotation, no kind stated": {
			&cnpgv1.Pooler{ObjectMeta: metav1.ObjectMeta{Name: "w"}, Spec: cnpgv1.PoolerSpec{
				Template: &cnpgv1.PodTemplateSpec{ObjectMeta: cnpgv1.Metadata{Annotations: map[string]string{"example.org/tenant": "a"}}},
			}},
			[]string{`Pooler "w"`, `pod template annotation "example.org/tenant"`, exact},
		},
		"a Prometheus's pod label": {
			holding(t, unstructuredObject("monitoring.coreos.com/v1", "Prometheus"), map[string]string{"platform.example/zone": "a"}, "spec", "podMetadata"),
			[]string{`Prometheus "w"`, `spec.podMetadata label "platform.example/zone"`, prefix},
		},
		"a PrometheusAgent's pod label": {
			holding(t, unstructuredObject("monitoring.coreos.com/v1alpha1", "PrometheusAgent"), map[string]string{"example.org/tenant": "a"}, "spec", "podMetadata"),
			[]string{`PrometheusAgent "w"`, `spec.podMetadata label "example.org/tenant"`, exact},
		},
		"an Alertmanager's pod annotation": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "monitoring.coreos.com/v1", "kind": "Alertmanager", "metadata": map[string]any{"name": "w"},
				"spec": map[string]any{"podMetadata": map[string]any{"annotations": map[string]any{"platform.example/zone": "a"}}}}},
			[]string{`Alertmanager "w"`, `spec.podMetadata annotation "platform.example/zone"`, prefix},
		},
		"a ThanosRuler's pod label": {
			holding(t, unstructuredObject("monitoring.coreos.com/v1", "ThanosRuler"), map[string]string{"platform.example/zone": "a"}, "spec", "podMetadata"),
			[]string{`ThanosRuler "w"`, `spec.podMetadata label "platform.example/zone"`, prefix},
		},
		"a ReplicationSource's mover pod label": {
			holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", "ReplicationSource"), map[string]string{"platform.example/zone": "a"}, "spec", "restic", "moverPodLabels"),
			[]string{`ReplicationSource "w"`, `mover pod label "platform.example/zone"`, prefix},
		},
		"a ReplicationDestination's mover pod label": {
			holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", "ReplicationDestination"), map[string]string{"example.org/tenant": "a"}, "spec", "rsyncTLS", "moverPodLabels"),
			[]string{`ReplicationDestination "w"`, `mover pod label "example.org/tenant"`, exact},
		},
		// Every solver of the list is read, under both ways a solver answers.
		"an Issuer's solver pod label, in a later solver": {
			solverIssuer("Issuer", map[string]any{"dns01": map[string]any{}}, nil,
				solverPod("ingress", map[string]any{"labels": map[string]any{"platform.example/zone": "a"}})),
			[]string{`Issuer "w"`, `solver pod template label "platform.example/zone"`, prefix},
		},
		"a ClusterIssuer's solver pod annotation, of a solver on a Gateway": {
			solverIssuer("ClusterIssuer", solverPod("gatewayHTTPRoute", map[string]any{"annotations": map[string]any{"example.org/tenant": "a"}})),
			[]string{`ClusterIssuer "w"`, `solver pod template annotation "example.org/tenant"`, exact},
		},
		"a Gateway's infrastructure label": {
			holding(t, unstructuredObject("gateway.networking.k8s.io/v1", "Gateway"), map[string]string{"platform.example/zone": "a"}, "spec", "infrastructure"),
			[]string{`Gateway "w"`, `spec.infrastructure label "platform.example/zone"`, prefix},
		},
		"a Gateway's infrastructure annotation": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway", "metadata": map[string]any{"name": "w"},
				"spec": map[string]any{"infrastructure": map[string]any{"annotations": map[string]any{"example.org/tenant": "a"}}}}},
			[]string{`Gateway "w"`, `spec.infrastructure annotation "example.org/tenant"`, exact},
		},
		// What a Flux object's controller puts on everything it applies, renders
		// or generates.
		"a Kustomization's common label": {
			holding(t, unstructuredObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization"), map[string]string{"platform.example/zone": "a"}, "spec", "commonMetadata"),
			[]string{`Kustomization "w"`, `spec.commonMetadata label "platform.example/zone"`, prefix},
		},
		"a HelmRelease's common annotation": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "helm.toolkit.fluxcd.io/v2", "kind": "HelmRelease", "metadata": map[string]any{"name": "w"},
				"spec": map[string]any{"commonMetadata": map[string]any{"annotations": map[string]any{"example.org/tenant": "a"}}}}},
			[]string{`HelmRelease "w"`, `spec.commonMetadata annotation "example.org/tenant"`, exact},
		},
		"an ArtifactGenerator's common label": {
			holding(t, unstructuredObject("source.extensions.fluxcd.io/v1beta1", "ArtifactGenerator"), map[string]string{"example.org/tenant": "a"}, "spec", "commonMetadata"),
			[]string{`ArtifactGenerator "w"`, `spec.commonMetadata label "example.org/tenant"`, exact},
		},
		"a ResourceSet's common label": {
			holding(t, unstructuredObject("fluxcd.controlplane.io/v1", "ResourceSet"), map[string]string{"platform.example/zone": "a"}, "spec", "commonMetadata"),
			[]string{`ResourceSet "w"`, `spec.commonMetadata label "platform.example/zone"`, prefix},
		},
		"a FluxInstance's common annotation": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "fluxcd.controlplane.io/v1", "kind": "FluxInstance", "metadata": map[string]any{"name": "w"},
				"spec": map[string]any{"commonMetadata": map[string]any{"annotations": map[string]any{"example.org/tenant": "a"}}}}},
			[]string{`FluxInstance "w"`, `spec.commonMetadata annotation "example.org/tenant"`, exact},
		},
		// A typed Flux object that states no kind is told by its Go type.
		"a typed Kustomization's common label, no kind stated": {
			&kustv1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: "w"}, Spec: kustv1.KustomizationSpec{
				CommonMetadata: &kustv1.CommonMetadata{Labels: map[string]string{"platform.example/zone": "a"}},
			}},
			[]string{`Kustomization "w"`, `spec.commonMetadata label "platform.example/zone"`, prefix},
		},
		"a typed HelmRelease's common annotation, no kind stated": {
			&helmv2.HelmRelease{ObjectMeta: metav1.ObjectMeta{Name: "w"}, Spec: helmv2.HelmReleaseSpec{
				CommonMetadata: &helmv2.CommonMetadata{Annotations: map[string]string{"example.org/tenant": "a"}},
			}},
			[]string{`HelmRelease "w"`, `spec.commonMetadata annotation "example.org/tenant"`, exact},
		},
		"a typed ArtifactGenerator's common label, no kind stated": {
			&swv1beta1.ArtifactGenerator{ObjectMeta: metav1.ObjectMeta{Name: "w"}, Spec: swv1beta1.ArtifactGeneratorSpec{
				CommonMetadata: &swv1beta1.CommonMetadata{Labels: map[string]string{"example.org/tenant": "a"}},
			}},
			[]string{`ArtifactGenerator "w"`, `spec.commonMetadata label "example.org/tenant"`, exact},
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
			for _, want := range append([]string{`component "web"`}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			}
			// The text is the document author's: it names no Go field.
			if strings.Contains(err.Error(), "TransformContext") {
				t.Errorf("refusal %q names a Go field", err)
			}
			// Refused before the stamp: the object is as its config made it.
			if _, stamped := tc.obj.GetLabels()[ownershipKey]; stamped {
				t.Error("the refused object carries the component label")
			}
		})
	}
}

// holdingThrough returns base with leaf at path, where a listStep holds a list
// of one element. path starts at a field of the object's top level.
func holdingThrough(base *unstructured.Unstructured, leaf any, path ...string) *unstructured.Unstructured {
	held := leaf
	for _, step := range slices.Backward(path) {
		if step == listStep {
			held = []any{held}
			continue
		}
		held = map[string]any{step: held}
	}
	maps.Copy(base.Object, held.(map[string]any))
	return base
}

// noPodHolderRow is one place an operator copies metadata from onto objects it
// creates that are no pods.
type noPodHolderRow struct {
	name, apiVersion, kind string
	in                     ReservedKeyHolder
	// path is where the object holds the metadata: the object with the
	// `labels` and the `annotations`, or the map itself of a labels or an
	// annotations shape.
	path  []string
	shape string
}

// object returns the row's object, holding key in its labels (annotation
// false) or its annotations.
func (r noPodHolderRow) object(annotation bool, key, value string) *unstructured.Unstructured {
	var leaf any = map[string]any{key: value}
	if r.shape == "metadata" {
		in := "labels"
		if annotation {
			in = "annotations"
		}
		leaf = map[string]any{in: leaf}
	}
	return holdingThrough(unstructuredObject(r.apiVersion, r.kind), leaf, r.path...)
}

// mapPath is the path of the labels or the annotations as a refusal names it,
// with the one element of each list.
func (r noPodHolderRow) mapPath(annotation bool) string {
	at := strings.ReplaceAll(strings.Join(r.path, "."), "."+listStep, "[0]")
	switch {
	case r.shape != "metadata":
		return at
	case annotation:
		return at + ".annotations"
	default:
		return at + ".labels"
	}
}

func noPodHolderRows() []noPodHolderRow {
	solver := func(steps ...string) []string {
		return append([]string{"spec", "acme", "solvers", listStep, "http01"}, steps...)
	}
	return []noPodHolderRow{
		{"an Issuer's solver ingress template", "cert-manager.io/v1", "Issuer", ReservedKeyInSolverIngressTemplate, solver("ingress", "ingressTemplate", "metadata"), "metadata"},
		{"a ClusterIssuer's solver ingress template", "cert-manager.io/v1", "ClusterIssuer", ReservedKeyInSolverIngressTemplate, solver("ingress", "ingressTemplate", "metadata"), "metadata"},
		{"an Issuer's solver HTTPRoute labels", "cert-manager.io/v1", "Issuer", ReservedKeyInSolverHTTPRoute, solver("gatewayHTTPRoute", "labels"), "labels"},
		{"a ClusterIssuer's solver HTTPRoute labels", "cert-manager.io/v1", "ClusterIssuer", ReservedKeyInSolverHTTPRoute, solver("gatewayHTTPRoute", "labels"), "labels"},
		{"a Certificate's secret template", "cert-manager.io/v1", "Certificate", ReservedKeyInSecretTemplate, []string{"spec", "secretTemplate"}, "metadata"},
		{"a Cluster's additional service template", "postgresql.cnpg.io/v1", "Cluster", ReservedKeyInServiceTemplate,
			[]string{"spec", "managed", "services", "additional", listStep, "serviceTemplate", "metadata"}, "metadata"},
		{"a Cluster's service account template", "postgresql.cnpg.io/v1", "Cluster", ReservedKeyInServiceAccountTemplate, []string{"spec", "serviceAccountTemplate", "metadata"}, "metadata"},
		{"a Cluster's volume snapshots", "postgresql.cnpg.io/v1", "Cluster", ReservedKeyInVolumeSnapshot, []string{"spec", "backup", "volumeSnapshot"}, "metadata"},
		{"a Pooler's service template", "postgresql.cnpg.io/v1", "Pooler", ReservedKeyInServiceTemplate, []string{"spec", "serviceTemplate", "metadata"}, "metadata"},
		{"an ExternalSecret's secret template", "external-secrets.io/v1", "ExternalSecret", ReservedKeyInSecretTemplate, []string{"spec", "target", "template", "metadata"}, "metadata"},
		{"a ClusterExternalSecret's external secret metadata", "external-secrets.io/v1", "ClusterExternalSecret", ReservedKeyInExternalSecretMetadata,
			[]string{"spec", "externalSecretMetadata"}, "metadata"},
		{"a ClusterExternalSecret's secret template", "external-secrets.io/v1", "ClusterExternalSecret", ReservedKeyInSecretTemplate,
			[]string{"spec", "externalSecretSpec", "target", "template", "metadata"}, "metadata"},
		{"a HelmRelease's chart template", "helm.toolkit.fluxcd.io/v2", "HelmRelease", ReservedKeyInChartTemplate, []string{"spec", "chart", "metadata"}, "metadata"},
		{"an ArtifactGenerator's common metadata", "source.extensions.fluxcd.io/v1beta1", "ArtifactGenerator", ReservedKeyInCommonMetadata,
			[]string{"spec", "commonMetadata"}, "metadata"},
		{"a FluxInstance's common metadata", "fluxcd.controlplane.io/v1", "FluxInstance", ReservedKeyInCommonMetadata,
			[]string{"spec", "commonMetadata"}, "metadata"},
		{"a ReplicationDestination's rsync service annotations", "volsync.backube/v1alpha1", "ReplicationDestination", ReservedKeyInMoverService,
			[]string{"spec", "rsync", "serviceAnnotations"}, "annotations"},
		{"a ReplicationDestination's rsyncTLS service annotations", "volsync.backube/v1alpha1", "ReplicationDestination", ReservedKeyInMoverService,
			[]string{"spec", "rsyncTLS", "serviceAnnotations"}, "annotations"},
		{"a StatefulSet's volume claim template", "apps/v1", "StatefulSet", ReservedKeyInVolumeClaimTemplate,
			[]string{"spec", "volumeClaimTemplates", listStep, "metadata"}, "metadata"},
		{"a Prometheus's storage claim template", "monitoring.coreos.com/v1", "Prometheus", ReservedKeyInVolumeClaimTemplate,
			[]string{"spec", "storage", "volumeClaimTemplate", "metadata"}, "metadata"},
		{"a PrometheusAgent's storage claim template", "monitoring.coreos.com/v1alpha1", "PrometheusAgent", ReservedKeyInVolumeClaimTemplate,
			[]string{"spec", "storage", "volumeClaimTemplate", "metadata"}, "metadata"},
		{"an Alertmanager's storage claim template", "monitoring.coreos.com/v1", "Alertmanager", ReservedKeyInVolumeClaimTemplate,
			[]string{"spec", "storage", "volumeClaimTemplate", "metadata"}, "metadata"},
		{"a ThanosRuler's storage claim template", "monitoring.coreos.com/v1", "ThanosRuler", ReservedKeyInVolumeClaimTemplate,
			[]string{"spec", "storage", "volumeClaimTemplate", "metadata"}, "metadata"},
	}
}

// TestOwnedConfig_NoPodMetadata: metadata an operator copies onto objects it
// creates that are no pods is held to the reserved keys, as a label and as an
// annotation wherever the holder has them, and the refusal names the holder
// and the path. The component label is not read there, since nothing selects
// those objects by it: another component's value builds, and the wrapper writes
// nothing there.
func TestOwnedConfig_NoPodMetadata(t *testing.T) {
	for _, row := range noPodHolderRows() {
		var shapes []bool
		if row.shape != "annotations" {
			shapes = append(shapes, false)
		}
		if row.shape != "labels" {
			shapes = append(shapes, true)
		}
		for _, annotation := range shapes {
			what := "label"
			if annotation {
				what = "annotation"
			}
			t.Run(row.name+", a reserved "+what, func(t *testing.T) {
				obj := row.object(annotation, "platform.example/zone", "a")
				inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
				_, err := stack.NewApplication("web", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, mustReserve(t, reservedForTest...))).Generate()
				var got *ReservedMetadataKeyError
				if !errors.As(err, &got) {
					t.Fatalf("Generate = %v, want a *ReservedMetadataKeyError", err)
				}
				if got.Holder != row.in || got.Path != row.mapPath(annotation) || got.Annotation != annotation || got.Key != "platform.example/zone" {
					t.Errorf("refusal = %+v, want holder %q, path %q", *got, row.in, row.mapPath(annotation))
				}
				if want := string(row.in) + " " + what + ` "platform.example/zone"`; !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			})
		}
		if row.shape == "annotations" {
			continue
		}
		t.Run(row.name+", another component's label", func(t *testing.T) {
			obj := row.object(false, ownershipKey, "db")
			inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
			if _, err := stack.NewApplication("web", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, mustReserve(t, reservedForTest...))).Generate(); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got := obj.GetLabels()[ownershipKey]; got != "web" {
				t.Errorf("object label = %q, want web", got)
			}
			// The holder's labels are as written. A kind's own stamp elsewhere in
			// the spec (a HelmRelease's post-renderers) is not this holder's.
			all, err := metadataHolder{path: row.path, labelMap: row.shape == "labels"}.held(obj.Object)
			if err != nil || len(all) != 1 {
				t.Fatalf("held = %v, %v, want the one holder", all, err)
			}
			if got := all[0].metadata["labels"]; !reflect.DeepEqual(got, map[string]any{ownershipKey: "db"}) {
				t.Errorf("%s = %v, want it as written", all[0].labels, got)
			}
		})
	}
}

// TestOwnedConfig_ReservedKeyNotRead is the control: what the check does not
// read, and the keys a config writes itself, pass and are stamped as without a
// list.
func TestOwnedConfig_ReservedKeyNotRead(t *testing.T) {
	// The component label key is exempt as a key. Its value is the other check's
	// (TestOwnedConfig_ComponentLabelIsAuthoritative): here it is the component's.
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: map[string]string{"app": "web", ownershipKey: "web"}}}
	deployment.Spec.Template.Labels = map[string]string{"app": "web", ownershipKey: "web"}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	statefulSet.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: map[string]string{"app": "web", ownershipKey: "web"}}}}

	// "app" and the component label key are reserved here, as a consumer that
	// keeps the component key under a prefix of its own has them.
	reserved := mustReserve(t, "app", "launcher.gokure.dev/", "example.org/tenant", "platform.example/")
	for name, obj := range map[string]client.Object{
		"the app label and the component label, typed": deployment,
		"the app label and the component label, unstructured": &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "w", "labels": map[string]any{"app": "web", ownershipKey: "web"}},
			"spec":     map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "web", ownershipKey: "web"}}}},
		}},
		"the two labels a Cluster's objects inherit": cnpgCluster("postgresql.cnpg.io/v1", "Cluster",
			map[string]any{"labels": map[string]any{"app": "db", ownershipKey: "web"}}),
		"the two labels in a volume claim template": statefulSet,
		"a volume claim template of a StatefulSet of another group": holdingThrough(unstructuredObject("example.com/v1", "StatefulSet"),
			map[string]any{"labels": map[string]any{"example.org/tenant": "a"}}, "spec", "volumeClaimTemplates", listStep, "metadata"),
		"a job template of a kind of another group": holding(t, unstructuredWorkload("example.com/v1", "CronJob"),
			map[string]string{"example.org/tenant": "a"}, "spec", "jobTemplate", "metadata"),
		"the two labels in a job template": holding(t, unstructuredWorkload("batch/v1", "CronJob"),
			map[string]string{"app": "web", ownershipKey: "web"}, "spec", "jobTemplate", "metadata"),
		"inheritedMetadata of a Cluster of another group": cnpgCluster("example.com/v1", "Cluster",
			map[string]any{"labels": map[string]any{"example.org/tenant": "a"}}),
		"inheritedMetadata of another kind of the group": cnpgCluster("postgresql.cnpg.io/v1", "Pooler",
			map[string]any{"labels": map[string]any{"example.org/tenant": "a"}}),
		"a pod template of a kind of another group": reservedWorkload(t, "example.com/v1", "Job", "labels", "example.org/tenant"),
		"podMetadata of an Alertmanager of another group": holding(t, unstructuredObject("example.com/v1", "Alertmanager"),
			map[string]string{"example.org/tenant": "a"}, "spec", "podMetadata"),
		"podMetadata of another kind of the group": holding(t, unstructuredObject("monitoring.coreos.com/v1", "ServiceMonitor"),
			map[string]string{"example.org/tenant": "a"}, "spec", "podMetadata"),
		"a storage claim template of an Alertmanager of another group": holding(t, unstructuredObject("example.com/v1", "Alertmanager"),
			map[string]string{"example.org/tenant": "a"}, "spec", "storage", "volumeClaimTemplate", "metadata"),
		// The claim template of ephemeral storage goes onto a claim of a pod,
		// not onto the StatefulSet's volume claim templates.
		"an Alertmanager's ephemeral storage claim template": holding(t, unstructuredObject("monitoring.coreos.com/v1", "Alertmanager"),
			map[string]string{"example.org/tenant": "a"}, "spec", "storage", "ephemeral", "volumeClaimTemplate", "metadata"),
		"the two labels in a mover's pod labels": holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", "ReplicationSource"),
			map[string]string{"app": "db", ownershipKey: "web"}, "spec", "restic", "moverPodLabels"),
		"moverPodLabels of a ReplicationSource of another group": holdingLabelMap(t, unstructuredObject("example.com/v1", "ReplicationSource"),
			map[string]string{"example.org/tenant": "a"}, "spec", "restic", "moverPodLabels"),
		"moverPodLabels of a mover the kind has none of": holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", "ReplicationDestination"),
			map[string]string{"example.org/tenant": "a"}, "spec", "syncthing", "moverPodLabels"),
		"the two labels in a solver's pod template": solverIssuer("Issuer",
			solverPod("ingress", map[string]any{"labels": map[string]any{"app": "db", ownershipKey: "web"}})),
		"the two labels in a Gateway's infrastructure": holding(t, unstructuredObject("gateway.networking.k8s.io/v1", "Gateway"),
			map[string]string{"app": "db", ownershipKey: "web"}, "spec", "infrastructure"),
		// Metadata an operator copies onto objects that are no pods: the two
		// labels are exempt there too, and a holder is read on its own kind only.
		"the two labels in a Certificate's secret template": holding(t, unstructuredObject("cert-manager.io/v1", "Certificate"),
			map[string]string{"app": "db", ownershipKey: "web"}, "spec", "secretTemplate"),
		"the two labels in a solver's HTTPRoute labels": solverIssuer("ClusterIssuer", map[string]any{"http01": map[string]any{"gatewayHTTPRoute": map[string]any{
			"labels": map[string]any{"app": "db", ownershipKey: "web"}}}}),
		"secretTemplate of another kind of the group": holding(t, unstructuredObject("cert-manager.io/v1", "Issuer"),
			map[string]string{"example.org/tenant": "a"}, "spec", "secretTemplate"),
		"serviceTemplate of a Pooler of another group": holding(t, unstructuredObject("example.com/v1", "Pooler"),
			map[string]string{"example.org/tenant": "a"}, "spec", "serviceTemplate", "metadata"),
		"serviceAnnotations of a mover the kind has none of": holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", "ReplicationSource"),
			map[string]string{"example.org/tenant": "a"}, "spec", "rsync", "serviceAnnotations"),
		"a solver pod template of another kind of the group": solverIssuer("Certificate",
			solverPod("ingress", map[string]any{"labels": map[string]any{"example.org/tenant": "a"}})),
		"infrastructure of another kind of the group": holding(t, unstructuredObject("gateway.networking.k8s.io/v1", "HTTPRoute"),
			map[string]string{"example.org/tenant": "a"}, "spec", "infrastructure"),
		"a null solver": solverIssuer("Issuer", nil),
		"the two labels in a Kustomization's commonMetadata": holding(t, unstructuredObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization"),
			map[string]string{"app": "db", ownershipKey: "web"}, "spec", "commonMetadata"),
		"commonMetadata of a Kustomization of another group": holding(t, unstructuredObject("example.com/v1", "Kustomization"),
			map[string]string{"example.org/tenant": "a"}, "spec", "commonMetadata"),
		"commonMetadata of another kind of the group": holding(t, unstructuredObject("helm.toolkit.fluxcd.io/v2", "HelmChart"),
			map[string]string{"example.org/tenant": "a"}, "spec", "commonMetadata"),
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

// TestOwnedConfig_ReservedKeyBeforeComponentLabel: a reserved key is refused as
// one wherever it is among an object and the members it stands for, whatever
// the component label beside it holds: the reserved keys are read on all of
// them before the component label is read on any. A list envelope that carries
// another component's label and holds a member with a reserved key is refused
// for the key, so a caller that tells the refusals apart still finds it.
func TestOwnedConfig_ReservedKeyBeforeComponentLabel(t *testing.T) {
	list := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "List",
		"metadata": map[string]any{"labels": map[string]any{ownershipKey: "db"}},
		"items": []any{map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "c", "labels": map[string]any{"example.org/tenant": "a"}},
		}},
	}}
	inner := &ownershipObjectsConfig{objects: []client.Object{list}}
	_, err := stack.NewApplication("web", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, mustReserve(t, reservedForTest...))).Generate()
	if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), `label "example.org/tenant"`) {
		t.Fatalf("Generate = %v, want ErrReservedMetadataKey for the member's label", err)
	}
	// Without the reserved list the envelope's own label is what is refused.
	_, err = stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
	if err == nil || errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), `"db" is not the component label of component "web"`) {
		t.Fatalf("Generate = %v, want the component label refusal", err)
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

// movingAugmenter moves the resources on its layout into a child layout, as a
// rendered chart's hook groups are, and adds none.
type movingAugmenter struct{ ownershipObjectsConfig }

func (c *movingAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	l.Children = append(l.Children, &layout.ManifestLayout{Name: "hook", Resources: l.Resources})
	l.Resources = nil
	return nil
}

// TestOwnedConfig_AugmentLayoutReadsOnlyWhatTheAugmenterAdded: what is on the
// layout before the wrapped augmenter runs is neither checked nor labelled,
// where it lies and wherever the augmenter moves it. The walker puts there what
// the application's outermost config returned, so a key under a reserved prefix
// on it is one a config around the wrapper added (go-kure/launcher#790). An
// unstructured object with a label that is no string, which the component label
// cannot be written beside, is such a config's own too.
func TestOwnedConfig_AugmentLayoutReadsOnlyWhatTheAugmenterAdded(t *testing.T) {
	reserved := mustReserve(t, reservedForTest...)
	there := func() []client.Object {
		return []client.Object{
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "labelled", Labels: map[string]string{"platform.example/owner": "platform"}}},
			&unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "ConfigMap",
				"metadata": map[string]any{"name": "odd", "labels": map[string]any{"n": int64(1)}},
			}},
		}
	}
	layouts := map[string]func() *layout.ManifestLayout{
		"on the layout": func() *layout.ManifestLayout { return &layout.ManifestLayout{Resources: there()} },
		"on a layout below it": func() *layout.ManifestLayout {
			return &layout.ManifestLayout{Children: []*layout.ManifestLayout{{Name: "below", Resources: there()}}}
		},
	}
	augmenters := map[string]func() stack.ApplicationConfig{
		"an augmenter that adds an object": func() stack.ApplicationConfig {
			return &reservedAugmenter{added: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "hook"}}}
		},
		"an augmenter that moves what is there": func() stack.ApplicationConfig { return &movingAugmenter{} },
	}
	var all func(l *layout.ManifestLayout) []client.Object
	all = func(l *layout.ManifestLayout) []client.Object {
		objs := append([]client.Object(nil), l.Resources...)
		for _, c := range l.Children {
			objs = append(objs, all(c)...)
		}
		return objs
	}
	// With no reserved key only the label is at stake, which the odd object
	// would be refused for. With no component either, the wrapper has nothing
	// to check or to label and hands AugmentLayout on: those rows are controls,
	// which held when every resource on the layout was read.
	lists := map[string]*reservedMetadataKeys{"keys reserved": reserved, "none reserved": nil}
	for where, newLayout := range layouts {
		for which, newAugmenter := range augmenters {
			for _, component := range []string{"web", ""} {
				for list, reserved := range lists {
					t.Run(where+"/"+which+"/component "+component+"/"+list, func(t *testing.T) {
						l := newLayout()
						aug := wrapOwnedConfigReserving(newAugmenter(), component, ownershipKey, reserved).(layout.LayoutAugmenter)
						if err := aug.AugmentLayout(l); err != nil {
							t.Fatalf("AugmentLayout = %v, want what was on the layout left unread", err)
						}
						seen := map[string]bool{}
						for _, obj := range all(l) {
							seen[obj.GetName()] = true
							_, stamped := obj.GetLabels()[ownershipKey]
							if u, ok := obj.(*unstructured.Unstructured); ok {
								_, stamped = u.Object["metadata"].(map[string]any)["labels"].(map[string]any)[ownershipKey]
							}
							// Only the object the augmenter added, for a component.
							if want := obj.GetName() == "hook" && component != ""; stamped != want {
								t.Errorf("%s: component label present = %v, want %v", obj.GetName(), stamped, want)
							}
						}
						if !seen["labelled"] || !seen["odd"] {
							t.Errorf("objects on the layout = %v, want the two that were there", seen)
						}
					})
				}
			}
		}
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

// loopingWrapperAnswers is how often a loopingWrapper says what it wraps before
// it answers nothing: well past the bound of the walk, and an end of its own for
// a walk that has none.
const loopingWrapperAnswers = 4 * platformAnnotationLayers

// loopingWrapper says it wraps whatever next is: itself, or a wrapper that says
// it wraps this one. It vouches for one pair. It counts how often it is asked
// what it wraps (asked) and answers nothing past loopingWrapperAnswers, so a walk
// with no bound of its own ends here too, and the count shows it had none.
type loopingWrapper struct {
	ownershipObjectsConfig
	next     stack.ApplicationConfig
	platform map[string]string
	asked    int
}

func (w *loopingWrapper) WrappedApplicationConfig() stack.ApplicationConfig {
	w.asked++
	if w.asked > loopingWrapperAnswers {
		return nil
	}
	return w.next
}

func (w *loopingWrapper) PlatformAnnotations() map[string]string { return w.platform }

// TestOwnedConfig_PlatformAnnotationsUnderABrokenWrapper: a wrapper chain the
// walk cannot follow to an end still generates what it generated before the walk
// existed. A wrapper that says it wraps itself, or two that say they wrap each
// other, are asked what they wrap as often as the bound allows
// (platformAnnotationLayers) and no more; a wrapper that holds a typed nil ends
// the walk like one that holds nothing. The pairs read up to there count: each
// object carries one, under a reserved prefix.
func TestOwnedConfig_PlatformAnnotationsUnderABrokenWrapper(t *testing.T) {
	reserved := mustReserve(t, "platform.example/")
	const issuer = "platform.example/issuer"
	pair := map[string]string{issuer: "letsencrypt"}
	objects := func() ownershipObjectsConfig {
		return ownershipObjectsConfig{objects: []client.Object{
			&networkingv1.Ingress{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"}, ObjectMeta: metav1.ObjectMeta{Name: "i", Annotations: maps.Clone(pair)}},
		}}
	}
	generate := func(inner stack.ApplicationConfig) error {
		_, err := stack.NewApplication("web-ingress", "ns", wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)).Generate()
		return err
	}

	self := &loopingWrapper{ownershipObjectsConfig: objects(), platform: pair}
	self.next = self
	first := &loopingWrapper{ownershipObjectsConfig: objects(), platform: pair}
	second := &loopingWrapper{next: first}
	first.next = second

	for name, chain := range map[string][]*loopingWrapper{
		"a wrapper that says it wraps itself":        {self},
		"two wrappers that say they wrap each other": {first, second},
	} {
		t.Run(name, func(t *testing.T) {
			if err := generate(chain[0]); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			asked := 0
			for _, w := range chain {
				asked += w.asked
			}
			if asked != platformAnnotationLayers {
				t.Fatalf("the walk asked %d times what a layer wraps, want %d (platformAnnotationLayers)", asked, platformAnnotationLayers)
			}
		})
	}

	for name, inner := range map[string]stack.ApplicationConfig{
		"a wrapper that holds a typed nil that would vouch":     &loopingWrapper{ownershipObjectsConfig: objects(), platform: pair, next: (*platformConfig)(nil)},
		"a wrapper that holds a typed nil that would wrap":      &loopingWrapper{ownershipObjectsConfig: objects(), platform: pair, next: (*vouchingWrapper)(nil)},
		"a wrapper that holds a typed nil of the wrapper's own": &loopingWrapper{ownershipObjectsConfig: objects(), platform: pair, next: (*ownedConfig)(nil)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := generate(inner); err != nil {
				t.Fatalf("Generate: %v", err)
			}
		})
	}
}

// TestOwnedConfig_PlatformAnnotationsLayerBound: the bound counts the layers of
// every chain, a finite one included. The config that vouches is read as the last
// layer the walk asks, and not one layer further down: there its pair is checked
// as authored, like under a wrapper that does not say it wraps.
func TestOwnedConfig_PlatformAnnotationsLayerBound(t *testing.T) {
	reserved := mustReserve(t, "platform.example/")
	const issuer = "platform.example/issuer"
	generate := func(wrappers int) error {
		pair := map[string]string{issuer: "letsencrypt"}
		var cfg stack.ApplicationConfig = &platformConfig{ownershipObjectsConfig{objects: []client.Object{
			&networkingv1.Ingress{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"}, ObjectMeta: metav1.ObjectMeta{Name: "i", Annotations: maps.Clone(pair)}},
		}}, pair}
		for range wrappers {
			cfg = &sayingWrapper{inner: cfg}
		}
		_, err := stack.NewApplication("web-ingress", "ns", wrapOwnedConfigReserving(cfg, "web", ownershipKey, reserved)).Generate()
		return err
	}

	if err := generate(platformAnnotationLayers - 1); err != nil {
		t.Fatalf("Generate with the vouching config as layer %d: %v", platformAnnotationLayers, err)
	}
	err := generate(platformAnnotationLayers)
	if !errors.Is(err, ErrReservedMetadataKey) || !strings.Contains(err.Error(), `: annotation "`+issuer+`"`) {
		t.Fatalf("Generate with the vouching config as layer %d = %v, want ErrReservedMetadataKey for the annotation %s", platformAnnotationLayers+1, err, issuer)
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
		"solvers that are an object": {map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
			"metadata": map[string]any{"name": "c"}, "spec": map[string]any{"acme": map[string]any{"solvers": map[string]any{}}}}, "solvers is a map[string]interface {}, not a list"},
		"a solver that is a text": {map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
			"metadata": map[string]any{"name": "c"}, "spec": map[string]any{"acme": map[string]any{"solvers": []any{"oops"}}}}, "solvers[0] is a string, not an object"},
		"infrastructure that is a list": {map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway",
			"metadata": map[string]any{"name": "c"}, "spec": map[string]any{"infrastructure": []any{}}}, "infrastructure is a"},
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
	for _, want := range []string{`component "tagged"`, `ConfigMap "tagged"`, `label "example.org/tenant"`, "the key is reserved for the platform"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %s", err, want)
		}
	}
}
