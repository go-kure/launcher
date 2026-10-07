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
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// holding returns base with labels at path, the object that holds labels and
// annotations there. Nil labels leave base as it is.
func holding(t *testing.T, base *unstructured.Unstructured, labels map[string]string, path ...string) *unstructured.Unstructured {
	t.Helper()
	return holdingLabelMap(t, base, labels, append(slices.Clone(path), "labels")...)
}

// holdingLabelMap returns base with labels as the map at path. Nil labels leave
// base as it is.
func holdingLabelMap(t *testing.T, base *unstructured.Unstructured, labels map[string]string, path ...string) *unstructured.Unstructured {
	t.Helper()
	if labels == nil {
		return base
	}
	raw := make(map[string]any, len(labels))
	for k, v := range labels {
		raw[k] = v
	}
	if err := unstructured.SetNestedField(base.Object, raw, path...); err != nil {
		t.Fatal(err)
	}
	return base
}

// unstructuredObject is an object of kind named w, with nothing but its name.
func unstructuredObject(apiVersion, kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "w"},
	}}
}

// solverIssuer is a cert-manager issuer of kind named w, with the ACME solvers
// given.
func solverIssuer(kind string, solvers ...any) *unstructured.Unstructured {
	u := unstructuredObject("cert-manager.io/v1", kind)
	u.Object["spec"] = map[string]any{"acme": map[string]any{"solvers": solvers}}
	return u
}

// solverPod is an HTTP01 solver whose pod template, under http01.<via>, has
// the metadata given.
func solverPod(via string, metadata map[string]any) map[string]any {
	return map[string]any{"http01": map[string]any{via: map[string]any{"podTemplate": map[string]any{"metadata": metadata}}}}
}

// labelHeldAt returns the value obj holds for key in the labels at path, and
// whether it holds one.
func labelHeldAt(t *testing.T, obj client.Object, key string, path ...string) (string, bool) {
	t.Helper()
	return labelInMapAt(t, obj, key, append(slices.Clone(path), "labels")...)
}

// labelInMapAt returns the value obj holds for key in the label map at path,
// and whether it holds one.
func labelInMapAt(t *testing.T, obj client.Object, key string, path ...string) (string, bool) {
	t.Helper()
	content, err := objectContent(obj)
	if err != nil {
		t.Fatal(err)
	}
	labels, _, err := nestedObject(content, path...)
	if err != nil {
		t.Fatal(err)
	}
	got, held := labels[key].(string)
	return got, held
}

// labelHolderRow is one place the component label check reads, on one kind of
// object, typed or unstructured.
type labelHolderRow struct {
	name string
	// where is the object as a refusal names it, and path where it holds the
	// labels: the object with the `labels`, or, of a label map (labelMap), the
	// map itself.
	where    string
	path     []string
	labelMap bool
	// filled says the wrapper writes the label there when the key is absent. It
	// reads the other places and writes nothing into them.
	filled bool
	// build returns the object the config hands out, with labels at path, and
	// the object that holds them: the object itself, or a member of the list it
	// is.
	build func(t *testing.T, labels map[string]string) (obj, holder client.Object)
}

// labels is the path of the row's labels.
func (r labelHolderRow) labels() []string {
	if r.labelMap {
		return r.path
	}
	return append(slices.Clone(r.path), "labels")
}

func labelHolderRows() []labelHolderRow {
	own := func(build func(t *testing.T, labels map[string]string) client.Object) func(*testing.T, map[string]string) (client.Object, client.Object) {
		return func(t *testing.T, labels map[string]string) (client.Object, client.Object) {
			obj := build(t, labels)
			return obj, obj
		}
	}
	typed := func(build func(labels map[string]string) client.Object) func(*testing.T, map[string]string) (client.Object, client.Object) {
		return own(func(_ *testing.T, labels map[string]string) client.Object { return build(labels) })
	}
	unstructuredAt := func(base func() *unstructured.Unstructured, path ...string) func(*testing.T, map[string]string) (client.Object, client.Object) {
		return own(func(t *testing.T, labels map[string]string) client.Object { return holding(t, base(), labels, path...) })
	}
	// member returns a list holding the object build returns, at depth lists.
	member := func(depth int, build func(*testing.T, map[string]string) (client.Object, client.Object)) func(*testing.T, map[string]string) (client.Object, client.Object) {
		return func(t *testing.T, labels map[string]string) (client.Object, client.Object) {
			_, holder := build(t, labels)
			items := []any{holder.(*unstructured.Unstructured).Object}
			for range depth - 1 {
				items = []any{map[string]any{"apiVersion": "v1", "kind": "List", "items": items}}
			}
			return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": items}}, holder
		}
	}
	workload := func(apiVersion, kind string) func() *unstructured.Unstructured {
		return func() *unstructured.Unstructured { return unstructuredWorkload(apiVersion, kind) }
	}
	object := func(apiVersion, kind string) func() *unstructured.Unstructured {
		return func() *unstructured.Unstructured { return unstructuredObject(apiVersion, kind) }
	}

	metadata := []string{"metadata"}
	template := []string{"spec", "template", "metadata"}
	cronTemplate := []string{"spec", "jobTemplate", "spec", "template", "metadata"}
	jobTemplate := []string{"spec", "jobTemplate", "metadata"}
	bareTemplate := []string{"template", "metadata"}
	inherited := []string{"spec", "inheritedMetadata"}
	podMetadata := []string{"spec", "podMetadata"}
	infrastructure := []string{"spec", "infrastructure"}
	commonMetadata := []string{"spec", "commonMetadata"}
	named := metav1.ObjectMeta{Name: "w"}

	rows := []labelHolderRow{
		// An object's own labels. A typed object that states no kind is named by
		// its Go type.
		{name: "typed ConfigMap", where: `*v1.ConfigMap "w"`, path: metadata, filled: true, build: typed(func(l map[string]string) client.Object {
			return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: l}}
		})},
		{name: "typed Pod", where: `Pod "w"`, path: metadata, filled: true, build: typed(func(l map[string]string) client.Object {
			return &corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: l}}
		})},
		{name: "unstructured ConfigMap", where: `ConfigMap "w"`, path: metadata, filled: true, build: unstructuredAt(object("v1", "ConfigMap"), metadata...)},
		{name: "unstructured Pod", where: `Pod "w"`, path: metadata, filled: true, build: unstructuredAt(object("v1", "Pod"), metadata...)},
		{name: "unstructured Deployment, its own labels", where: `Deployment "w"`, path: metadata, filled: true, build: unstructuredAt(workload("apps/v1", "Deployment"), metadata...)},

		// The eight pod templates, typed.
		{name: "typed Deployment", where: `Deployment "w"`, path: template, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &appsv1.Deployment{ObjectMeta: named}
			o.Spec.Template.Labels = l
			return o
		})},
		{name: "typed StatefulSet", where: `StatefulSet "w"`, path: template, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &appsv1.StatefulSet{ObjectMeta: named}
			o.Spec.Template.Labels = l
			return o
		})},
		{name: "typed DaemonSet", where: `DaemonSet "w"`, path: template, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &appsv1.DaemonSet{ObjectMeta: named}
			o.Spec.Template.Labels = l
			return o
		})},
		{name: "typed Job", where: `Job "w"`, path: template, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &batchv1.Job{ObjectMeta: named}
			o.Spec.Template.Labels = l
			return o
		})},
		{name: "typed CronJob", where: `CronJob "w"`, path: cronTemplate, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &batchv1.CronJob{ObjectMeta: named}
			o.Spec.JobTemplate.Spec.Template.Labels = l
			return o
		})},
		{name: "typed ReplicaSet", where: `ReplicaSet "w"`, path: template, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &appsv1.ReplicaSet{ObjectMeta: named}
			o.Spec.Template.Labels = l
			return o
		})},
		{name: "typed ReplicationController", where: `ReplicationController "w"`, path: template, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &corev1.ReplicationController{ObjectMeta: named}
			o.Spec.Template = &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: l}}
			return o
		})},
		{name: "typed PodTemplate", where: `PodTemplate "w"`, path: bareTemplate, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &corev1.PodTemplate{ObjectMeta: named}
			o.Template.Labels = l
			return o
		})},

		// The same eight, unstructured.
		{name: "unstructured Deployment", where: `Deployment "w"`, path: template, filled: true, build: unstructuredAt(workload("apps/v1", "Deployment"), template...)},
		{name: "unstructured StatefulSet", where: `StatefulSet "w"`, path: template, filled: true, build: unstructuredAt(workload("apps/v1", "StatefulSet"), template...)},
		{name: "unstructured DaemonSet", where: `DaemonSet "w"`, path: template, filled: true, build: unstructuredAt(workload("apps/v1", "DaemonSet"), template...)},
		{name: "unstructured Job", where: `Job "w"`, path: template, filled: true, build: unstructuredAt(workload("batch/v1", "Job"), template...)},
		{name: "unstructured CronJob", where: `CronJob "w"`, path: cronTemplate, filled: true, build: unstructuredAt(workload("batch/v1", "CronJob"), cronTemplate...)},
		{name: "unstructured ReplicaSet", where: `ReplicaSet "w"`, path: template, filled: true, build: unstructuredAt(workload("apps/v1", "ReplicaSet"), template...)},
		{name: "unstructured ReplicationController", where: `ReplicationController "w"`, path: template, filled: true, build: unstructuredAt(workload("v1", "ReplicationController"), template...)},
		{name: "unstructured PodTemplate", where: `PodTemplate "w"`, path: bareTemplate, filled: true, build: unstructuredAt(workload("v1", "PodTemplate"), bareTemplate...)},

		// A CronJob's job template: the metadata of every Job it creates.
		{name: "typed CronJob, its job template", where: `CronJob "w"`, path: jobTemplate, filled: true, build: typed(func(l map[string]string) client.Object {
			o := &batchv1.CronJob{ObjectMeta: named}
			o.Spec.JobTemplate.Labels = l
			return o
		})},
		{name: "unstructured CronJob, its job template", where: `CronJob "w"`, path: jobTemplate, filled: true, build: unstructuredAt(workload("batch/v1", "CronJob"), jobTemplate...)},
		{name: "a List member's job template", where: `CronJob "w"`, path: jobTemplate, filled: true, build: member(1, unstructuredAt(workload("batch/v1", "CronJob"), jobTemplate...))},

		// The members of a list envelope, as Flux applies them.
		{name: "a List member", where: `ConfigMap "w"`, path: metadata, filled: true, build: member(1, unstructuredAt(object("v1", "ConfigMap"), metadata...))},
		{name: "a List member's pod template", where: `Deployment "w"`, path: template, filled: true, build: member(1, unstructuredAt(workload("apps/v1", "Deployment"), template...))},
		{name: "the member of a list inside a List", where: `StatefulSet "w"`, path: template, filled: true, build: member(2, unstructuredAt(workload("apps/v1", "StatefulSet"), template...))},

		// Metadata an operator hands on: read, and never written.
		{name: "typed Cluster", where: `Cluster "w"`, path: inherited, build: typed(func(l map[string]string) client.Object {
			return &cnpgv1.Cluster{ObjectMeta: named, Spec: cnpgv1.ClusterSpec{InheritedMetadata: &cnpgv1.EmbeddedObjectMetadata{Labels: l}}}
		})},
		{name: "unstructured Cluster", where: `Cluster "w"`, path: inherited, build: unstructuredAt(object("postgresql.cnpg.io/v1", "Cluster"), inherited...)},
		{name: "typed Pooler", where: `Pooler "w"`, path: template, build: typed(func(l map[string]string) client.Object {
			return &cnpgv1.Pooler{ObjectMeta: named, Spec: cnpgv1.PoolerSpec{Template: &cnpgv1.PodTemplateSpec{ObjectMeta: cnpgv1.Metadata{Labels: l}}}}
		})},
		{name: "unstructured Pooler", where: `Pooler "w"`, path: template, build: unstructuredAt(object("postgresql.cnpg.io/v1", "Pooler"), template...)},
		{name: "Prometheus", where: `Prometheus "w"`, path: podMetadata, build: unstructuredAt(object("monitoring.coreos.com/v1", "Prometheus"), podMetadata...)},
		{name: "PrometheusAgent", where: `PrometheusAgent "w"`, path: podMetadata, build: unstructuredAt(object("monitoring.coreos.com/v1alpha1", "PrometheusAgent"), podMetadata...)},
		{name: "Alertmanager", where: `Alertmanager "w"`, path: podMetadata, build: unstructuredAt(object("monitoring.coreos.com/v1", "Alertmanager"), podMetadata...)},
		{name: "ThanosRuler", where: `ThanosRuler "w"`, path: podMetadata, build: unstructuredAt(object("monitoring.coreos.com/v1", "ThanosRuler"), podMetadata...)},
		{name: "Gateway", where: `Gateway "w"`, path: infrastructure, build: unstructuredAt(object("gateway.networking.k8s.io/v1", "Gateway"), infrastructure...)},

		// What a Flux object hands on to what it applies: read, and never written.
		{name: "typed Kustomization", where: `Kustomization "w"`, path: commonMetadata, build: typed(func(l map[string]string) client.Object {
			return &kustv1.Kustomization{ObjectMeta: named, Spec: kustv1.KustomizationSpec{CommonMetadata: &kustv1.CommonMetadata{Labels: l}}}
		})},
		{name: "unstructured Kustomization", where: `Kustomization "w"`, path: commonMetadata, build: unstructuredAt(object("kustomize.toolkit.fluxcd.io/v1", "Kustomization"), commonMetadata...)},
		{name: "typed HelmRelease", where: `HelmRelease "w"`, path: commonMetadata, build: typed(func(l map[string]string) client.Object {
			return &helmv2.HelmRelease{ObjectMeta: named, Spec: helmv2.HelmReleaseSpec{CommonMetadata: &helmv2.CommonMetadata{Labels: l}}}
		})},
		{name: "unstructured HelmRelease", where: `HelmRelease "w"`, path: commonMetadata, build: unstructuredAt(object("helm.toolkit.fluxcd.io/v2", "HelmRelease"), commonMetadata...)},
		// What the Flux Operator puts on every object a ResourceSet generates.
		{name: "ResourceSet", where: `ResourceSet "w"`, path: commonMetadata, build: unstructuredAt(object("fluxcd.controlplane.io/v1", "ResourceSet"), commonMetadata...)},
	}
	// The labels of a VolSync mover's pods: a label map, of every mover of the
	// two kinds.
	for kind, movers := range map[string][]string{
		"ReplicationSource":      {"rsync", "rsyncTLS", "rclone", "restic", "syncthing"},
		"ReplicationDestination": {"rsync", "rsyncTLS", "rclone", "restic"},
	} {
		for _, mover := range movers {
			path := []string{"spec", mover, "moverPodLabels"}
			rows = append(rows, labelHolderRow{
				name: kind + " " + mover, where: kind + ` "w"`, path: path, labelMap: true,
				build: own(func(t *testing.T, labels map[string]string) client.Object {
					return holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", kind), labels, path...)
				}),
			})
		}
	}
	return rows
}

// TestOwnedConfig_ComponentLabelIsAuthoritative: wherever an object holds
// labels that reach pods or objects in the cluster, a value under the component
// label's key is the owning component's. Another component's is refused, with
// the component, the object and the path; the component's own stays; and where
// the key is absent the wrapper writes it, except into metadata an operator
// hands on, which it only reads.
func TestOwnedConfig_ComponentLabelIsAuthoritative(t *testing.T) {
	generate := func(obj client.Object) ([]*client.Object, error) {
		inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
		return stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
	}
	for _, row := range labelHolderRows() {
		labelsPath := strings.Join(row.labels(), ".")
		held := func(t *testing.T, holder client.Object, key string) (string, bool) {
			t.Helper()
			return labelInMapAt(t, holder, key, row.labels()...)
		}

		t.Run(row.name+"/another component's value is refused", func(t *testing.T) {
			obj, holder := row.build(t, map[string]string{"tier": "front", ownershipKey: "db"})
			objs, err := generate(obj)
			if err == nil {
				t.Fatal("Generate accepted another component's value")
			}
			if objs != nil {
				t.Errorf("Generate returned %d objects beside the refusal", len(objs))
			}
			for _, want := range []string{row.where, labelsPath + `["` + ownershipKey + `"]`, `"db"`, `component "web" ("web")`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			}
			// Refused before the stamp: the object is as its config made it.
			if got, _ := held(t, holder, ownershipKey); got != "db" {
				t.Errorf("%s[%s] = %q after the refusal, want it as written", labelsPath, ownershipKey, got)
			}
			if len(row.path) > 1 {
				if got, stamped := labelHeldAt(t, holder, ownershipKey, "metadata"); stamped {
					t.Errorf("the refused object carries the component label %q", got)
				}
			}
		})

		t.Run(row.name+"/the component's value stays", func(t *testing.T) {
			obj, holder := row.build(t, map[string]string{"tier": "front", ownershipKey: "web"})
			for range 2 {
				if _, err := generate(obj); err != nil {
					t.Fatalf("Generate: %v", err)
				}
			}
			if got, _ := held(t, holder, ownershipKey); got != "web" {
				t.Errorf("%s[%s] = %q, want web", labelsPath, ownershipKey, got)
			}
			if got, _ := held(t, holder, "tier"); got != "front" {
				t.Errorf("%s[tier] = %q, want it as written", labelsPath, got)
			}
		})

		t.Run(row.name+"/an absent key", func(t *testing.T) {
			obj, holder := row.build(t, nil)
			for range 2 {
				if _, err := generate(obj); err != nil {
					t.Fatalf("Generate: %v", err)
				}
			}
			if got, _ := labelHeldAt(t, holder, ownershipKey, "metadata"); got != "web" {
				t.Errorf("object label = %q, want web", got)
			}
			got, written := held(t, holder, ownershipKey)
			if row.filled && got != "web" {
				t.Errorf("%s[%s] = %q, want the wrapper's web", labelsPath, ownershipKey, got)
			}
			if !row.filled && written {
				t.Errorf("%s[%s] = %q, want nothing written there", labelsPath, ownershipKey, got)
			}
		})
	}
}

// TestOwnedConfig_ComponentLabelInASolverPodTemplate: the pod template of an
// HTTP01 solver of a cert-manager issuer is held as any other, in every solver
// of the list and under both ways a solver answers (ingress, gatewayHTTPRoute).
// The refusal names the solver by its index. A solver with no pod template, and
// a null one, hold nothing; nothing is written into any.
func TestOwnedConfig_ComponentLabelInASolverPodTemplate(t *testing.T) {
	generate := func(obj client.Object) error {
		inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
		_, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
		return err
	}
	for _, kind := range []string{"Issuer", "ClusterIssuer"} {
		for _, via := range []string{"ingress", "gatewayHTTPRoute"} {
			t.Run(kind+"/"+via, func(t *testing.T) {
				own := solverPod(via, map[string]any{"labels": map[string]any{"tier": "front", ownershipKey: "web"}})
				bare := solverPod(via, map[string]any{"annotations": map[string]any{ownershipKey: "db"}})
				others := []any{map[string]any{"dns01": map[string]any{}}, nil, own, bare}

				issuer := solverIssuer(kind, others...)
				if err := generate(issuer); err != nil {
					t.Fatalf("Generate, with the component's own value: %v", err)
				}
				if got := issuer.GetLabels()[ownershipKey]; got != "web" {
					t.Errorf("object label = %q, want web", got)
				}
				want := solverIssuer(kind, map[string]any{"dns01": map[string]any{}}, nil,
					solverPod(via, map[string]any{"labels": map[string]any{"tier": "front", ownershipKey: "web"}}),
					solverPod(via, map[string]any{"annotations": map[string]any{ownershipKey: "db"}}))
				if !reflect.DeepEqual(issuer.Object["spec"], want.Object["spec"]) {
					t.Errorf("spec = %v\nwant it as written: %v", issuer.Object["spec"], want.Object["spec"])
				}

				foreign := solverPod(via, map[string]any{"labels": map[string]any{ownershipKey: "db"}})
				err := generate(solverIssuer(kind, append(slices.Clone(others), foreign)...))
				var got *ComponentLabelError
				if !errors.As(err, &got) {
					t.Fatalf("Generate = %v, want a *ComponentLabelError", err)
				}
				path := "spec.acme.solvers[4].http01." + via + ".podTemplate.metadata.labels"
				wantRefusal := ComponentLabelError{
					Refused: ComponentLabelForeignValue, Component: "web",
					Kind: schema.GroupKind{Group: "cert-manager.io", Kind: kind}, Name: "w", Object: kind + ` "w"`,
					Path: path, Key: ownershipKey, Value: "db", Want: "web",
				}
				if !reflect.DeepEqual(*got, wantRefusal) {
					t.Errorf("refusal = %+v\nwant      %+v", *got, wantRefusal)
				}
				if text := kind + ` "w": ` + path + `["` + ownershipKey + `"]: "db" is not the component label of component "web" ("web")`; !strings.Contains(err.Error(), text) {
					t.Errorf("refusal %q does not say %s", err, text)
				}
			})
		}
	}

	// A list the check cannot read fails generation: it is not read as holding
	// no key.
	for name, tc := range map[string]struct {
		solvers any
		want    string
	}{
		"solvers that are an object":  {map[string]any{"http01": map[string]any{}}, "solvers is a map[string]interface {}, not a list"},
		"a solver that is a text":     {[]any{map[string]any{}, "oops"}, "solvers[1] is a string, not an object"},
		"a pod template that is text": {[]any{map[string]any{"http01": map[string]any{"ingress": map[string]any{"podTemplate": "oops"}}}}, "podTemplate is a string, not an object"},
	} {
		t.Run(name, func(t *testing.T) {
			u := unstructuredObject("cert-manager.io/v1", "Issuer")
			u.Object["spec"] = map[string]any{"acme": map[string]any{"solvers": tc.solvers}}
			err := generate(u)
			if err == nil || !strings.Contains(err.Error(), `component label: Issuer "w"`) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Generate = %v, want an error naming the Issuer and %s", err, tc.want)
			}
			if errors.Is(err, ErrComponentLabelValue) {
				t.Errorf("error %q is ErrComponentLabelValue, want it told apart from a foreign value", err)
			}
		})
	}
}

// TestOwnedConfig_ComponentLabelValueAsWritten: an unstructured object's value
// is read as written. A null is the empty string the cluster reads it as, which
// is no component's value, and a value that is no string is refused as such.
func TestOwnedConfig_ComponentLabelValueAsWritten(t *testing.T) {
	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"a null value":     {nil, `"" is not the component label of component "web"`},
		"an empty value":   {"", `"" is not the component label of component "web"`},
		"a number":         {int64(1), `is a int64, not a string`},
		"a nested object":  {map[string]any{}, `not a string`},
		"a value in a mix": {"db ", `"db " is not the component label`},
	} {
		t.Run(name, func(t *testing.T) {
			u := unstructuredWorkload("apps/v1", "Deployment")
			if err := unstructured.SetNestedField(u.Object, map[string]any{ownershipKey: tc.value}, "spec", "template", "metadata", "labels"); err != nil {
				t.Fatal(err)
			}
			inner := &ownershipObjectsConfig{objects: []client.Object{u}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `Deployment "w"`) {
				t.Fatalf("Generate = %v, want a refusal of the Deployment saying %s", err, tc.want)
			}
			if !strings.Contains(err.Error(), `spec.template.metadata.labels["`+ownershipKey+`"]`) {
				t.Errorf("refusal %q does not name the path", err)
			}
		})
	}

	// Metadata the check cannot read fails generation: it is not read as holding
	// no key.
	for name, tc := range map[string]struct {
		set  any
		path []string
		want string
	}{
		"labels that are a list":            {[]any{"a"}, []string{"spec", "podMetadata", "labels"}, "spec.podMetadata: labels is a"},
		"metadata that is a text":           {"oops", []string{"spec", "podMetadata"}, "podMetadata is a"},
		"a spec above it that is no object": {"oops", []string{"spec"}, "spec is a"},
	} {
		t.Run(name, func(t *testing.T) {
			u := unstructuredObject("monitoring.coreos.com/v1", "Alertmanager")
			if err := unstructured.SetNestedField(u.Object, tc.set, tc.path...); err != nil {
				t.Fatal(err)
			}
			inner := &ownershipObjectsConfig{objects: []client.Object{u}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
			if err == nil || !strings.Contains(err.Error(), `Alertmanager "w"`) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Generate = %v, want an error naming the Alertmanager and %s", err, tc.want)
			}
		})
	}
}

// TestOwnedConfig_ComponentLabelNotRead is the control: what the check does not
// read holds any value, and so does every object of an application the document
// as a whole owns, which has no component to be held to.
func TestOwnedConfig_ComponentLabelNotRead(t *testing.T) {
	foreign := map[string]string{ownershipKey: "db"}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	statefulSet.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: foreign}}}
	annotated := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w", Annotations: foreign}}
	annotated.Spec.Template.Annotations = foreign

	for name, obj := range map[string]client.Object{
		"a volume claim template's label": statefulSet,
		"the key as an annotation":        annotated,
		"a job template of a kind of another group": holding(t, unstructuredWorkload("example.com/v1", "CronJob"),
			foreign, "spec", "jobTemplate", "metadata"),
		"commonMetadata of a Kustomization of another group": holding(t, unstructuredObject("example.com/v1", "Kustomization"),
			foreign, "spec", "commonMetadata"),
		"commonMetadata of another kind of the group": holding(t, unstructuredObject("helm.toolkit.fluxcd.io/v2", "HelmChart"),
			foreign, "spec", "commonMetadata"),
		"a pod template of a kind of another group": holding(t, unstructuredWorkload("example.com/v1", "Job"),
			foreign, "spec", "template", "metadata"),
		"inheritedMetadata of a Cluster of another group": holding(t, unstructuredObject("example.com/v1", "Cluster"),
			foreign, "spec", "inheritedMetadata"),
		"podMetadata of an Alertmanager of another group": holding(t, unstructuredObject("example.com/v1", "Alertmanager"),
			foreign, "spec", "podMetadata"),
		"podMetadata of another kind of the group": holding(t, unstructuredObject("monitoring.coreos.com/v1", "ServiceMonitor"),
			foreign, "spec", "podMetadata"),
		"inheritedMetadata of a Pooler": holding(t, unstructuredObject("postgresql.cnpg.io/v1", "Pooler"),
			foreign, "spec", "inheritedMetadata"),
		"moverPodLabels of a ReplicationSource of another group": holdingLabelMap(t, unstructuredObject("example.com/v1", "ReplicationSource"),
			foreign, "spec", "restic", "moverPodLabels"),
		"moverPodLabels of a mover the kind has none of": holdingLabelMap(t, unstructuredObject("volsync.backube/v1alpha1", "ReplicationDestination"),
			foreign, "spec", "syncthing", "moverPodLabels"),
		// Metadata an operator copies onto objects it creates that are no pods:
		// the reserved keys are read there, and the component label is not
		// (TestOwnedConfig_NoPodMetadata holds every such holder).
		"the ingress template of a solver": solverIssuer("Issuer", map[string]any{"http01": map[string]any{"ingress": map[string]any{
			"ingressTemplate": map[string]any{"metadata": map[string]any{"labels": map[string]any{ownershipKey: "db"}}}}}}),
		"the labels of a solver's HTTPRoutes": solverIssuer("ClusterIssuer", map[string]any{"http01": map[string]any{"gatewayHTTPRoute": map[string]any{
			"labels": map[string]any{ownershipKey: "db"}}}}),
		"a solver pod template of an issuer of another group": func() client.Object {
			u := solverIssuer("Issuer", solverPod("ingress", map[string]any{"labels": map[string]any{ownershipKey: "db"}}))
			u.SetAPIVersion("example.com/v1")
			return u
		}(),
		"a solver pod template of another kind of the group": solverIssuer("Certificate",
			solverPod("ingress", map[string]any{"labels": map[string]any{ownershipKey: "db"}})),
		"infrastructure of a Gateway of another group": holding(t, unstructuredObject("example.com/v1", "Gateway"),
			foreign, "spec", "infrastructure"),
		"infrastructure of another kind of the group": holding(t, unstructuredObject("gateway.networking.k8s.io/v1", "HTTPRoute"),
			foreign, "spec", "infrastructure"),
		// An ArtifactGenerator's commonMetadata goes onto the ExternalArtifacts it
		// generates, which are no pods; typed and stating no kind, as
		// TestOwnedConfig_NoPodMetadata holds it unstructured.
		"commonMetadata of a typed ArtifactGenerator": &swv1beta1.ArtifactGenerator{ObjectMeta: metav1.ObjectMeta{Name: "w"},
			Spec: swv1beta1.ArtifactGeneratorSpec{CommonMetadata: &swv1beta1.CommonMetadata{Labels: map[string]string{ownershipKey: "db"}}}},
		// A FluxInstance's goes onto the objects of the Flux installation.
		"commonMetadata of a FluxInstance": holding(t, unstructuredObject("fluxcd.controlplane.io/v1", "FluxInstance"),
			foreign, "spec", "commonMetadata"),
		"commonMetadata of a ResourceSet of another group": holding(t, unstructuredObject("example.com/v1", "ResourceSet"),
			foreign, "spec", "commonMetadata"),
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
			if _, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate(); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got := obj.GetLabels()[ownershipKey]; got != "web" {
				t.Errorf("object label = %q, want web", got)
			}
		})
	}

	t.Run("an object of the document's", func(t *testing.T) {
		source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{ownershipKey: "db"}}}
		inner := &ownershipObjectsConfig{objects: []client.Object{source}}
		for _, reserved := range []*reservedMetadataKeys{nil, mustReserve(t, reservedForTest...)} {
			if _, err := stack.NewApplication("shop-source", "ns", wrapOwnedConfigReserving(inner, "", ownershipKey, reserved)).Generate(); err != nil {
				t.Fatalf("Generate: %v", err)
			}
		}
		if got := source.Labels[ownershipKey]; got != "db" {
			t.Errorf("label = %q, want it as written", got)
		}
	})
}

// TestOwnedConfig_ComponentLabelSelector: a workload whose own selector
// requires a value for the component label's key that is not the component's is
// refused. A selector that requires the component's value, or that rules only
// another value out, is not. One that rules the label out is refused as such
// (TestOwnedConfig_ComponentLabelSelectorRulesOut).
func TestOwnedConfig_ComponentLabelSelector(t *testing.T) {
	requirement := func(op metav1.LabelSelectorOperator, values ...string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: ownershipKey, Operator: op, Values: values}}}
	}
	deployment := func(selector *metav1.LabelSelector) client.Object {
		d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
		d.Spec.Selector = selector
		return d
	}
	cronJob := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	cronJob.Spec.JobTemplate.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{ownershipKey: "db"}}
	typedRC := func(selector map[string]string) client.Object {
		rc := &corev1.ReplicationController{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
		rc.Spec.Selector = selector
		rc.Spec.Template = &corev1.PodTemplateSpec{}
		return rc
	}
	withSelector := func(u *unstructured.Unstructured, selector any, path ...string) *unstructured.Unstructured {
		if err := unstructured.SetNestedField(u.Object, selector, append(slices.Clone(path), "selector")...); err != nil {
			t.Fatal(err)
		}
		return u
	}
	const hasMember = "spec.selector requires"

	for name, tc := range map[string]struct {
		obj client.Object
		// want is what the refusal says; empty when the object passes.
		want string
	}{
		"matchLabels with another value": {
			deployment(&metav1.LabelSelector{MatchLabels: map[string]string{ownershipKey: "db"}}), hasMember + ` "db" for the label`,
		},
		"In with other values only": {deployment(requirement(metav1.LabelSelectorOpIn, "db", "cache")), hasMember + ` one of "db", "cache" for the label`},
		"a CronJob's job selector":  {cronJob, `spec.jobTemplate.spec.selector requires "db"`},
		"a typed ReplicationController's label map": {
			typedRC(map[string]string{ownershipKey: "db"}), hasMember + ` "db"`,
		},
		"an unstructured ReplicationController's label map": {
			withSelector(unstructuredWorkload("v1", "ReplicationController"), map[string]any{ownershipKey: "db"}, "spec"), hasMember + ` "db"`,
		},
		"an unstructured ReplicationController's null value": {
			withSelector(unstructuredWorkload("v1", "ReplicationController"), map[string]any{ownershipKey: nil}, "spec"), hasMember + ` ""`,
		},
		"an unstructured Deployment's matchLabels": {
			withSelector(unstructuredWorkload("apps/v1", "Deployment"), map[string]any{"matchLabels": map[string]any{ownershipKey: "db"}}, "spec"), hasMember + ` "db"`,
		},
		"an unstructured CronJob's In": {
			withSelector(unstructuredWorkload("batch/v1", "CronJob"), map[string]any{"matchExpressions": []any{
				map[string]any{"key": ownershipKey, "operator": "In", "values": []any{"db"}},
			}}, "spec", "jobTemplate", "spec"), `spec.jobTemplate.spec.selector requires "db"`,
		},
		"a List member's selector": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
				withSelector(unstructuredWorkload("apps/v1", "ReplicaSet"), map[string]any{"matchLabels": map[string]any{ownershipKey: "db"}}, "spec").Object,
			}}}, `ReplicaSet "w": ` + hasMember + ` "db"`,
		},

		"matchLabels with the component's value": {deployment(&metav1.LabelSelector{MatchLabels: map[string]string{ownershipKey: "web"}}), ""},
		"matchLabels on another key":             {deployment(&metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}}), ""},
		"In that holds the component's value":    {deployment(requirement(metav1.LabelSelectorOpIn, "db", "web")), ""},
		"NotIn another value":                    {deployment(requirement(metav1.LabelSelectorOpNotIn, "db")), ""},
		"Exists":                                 {deployment(requirement(metav1.LabelSelectorOpExists)), ""},
		"no selector":                            {deployment(nil), ""},
		// No selector the cluster accepts, and not this check's to refuse.
		"In with no value":                    {deployment(requirement(metav1.LabelSelectorOpIn)), ""},
		"a ReplicationController's own value": {typedRC(map[string]string{ownershipKey: "web"}), ""},
		// A ReplicationController's selector is a label map: a key named like a
		// label selector's field is a label.
		"a ReplicationController label named matchLabels": {
			withSelector(unstructuredWorkload("v1", "ReplicationController"), map[string]any{"matchLabels": "kept"}, "spec"), "",
		},
		"a kind of another group": {
			withSelector(unstructuredWorkload("example.com/v1", "Job"), map[string]any{"matchLabels": map[string]any{ownershipKey: "db"}}, "spec"), "",
		},
		// A PodTemplate has no selector: a field of that name is not one.
		"a PodTemplate": {
			withSelector(unstructuredWorkload("v1", "PodTemplate"), map[string]any{"matchLabels": map[string]any{ownershipKey: "db"}}), "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{tc.obj}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Generate accepted a selector that requires another value")
			}
			for _, want := range []string{tc.want, `"` + ownershipKey + `"`, `component "web" ("web")`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			}
		})
	}
}

// TestOwnedConfig_UnreadableSelector: a workload selector the check cannot read
// fails generation, as metadata it cannot read does, rather than pass a
// workload it never held to the label (go-kure/launcher#790). The kinds refuse
// such a selector at their strict decode, and a passthrough document is refused
// when the environment policy reads it, both before generation; a consumer's own
// config, as here, reaches the check with it. It is no
// *ComponentLabelError: no value is refused. A label map entry under another
// key is not read.
func TestOwnedConfig_UnreadableSelector(t *testing.T) {
	withSelector := func(u *unstructured.Unstructured, selector any, path ...string) *unstructured.Unstructured {
		if err := unstructured.SetNestedField(u.Object, selector, append(slices.Clone(path), "selector")...); err != nil {
			t.Fatal(err)
		}
		return u
	}
	for name, tc := range map[string]struct {
		obj client.Object
		// want is what the error says; empty when the object passes.
		want string
	}{
		"a selector that does not decode": {
			withSelector(unstructuredWorkload("apps/v1", "Deployment"), map[string]any{"matchLabels": "oops"}, "spec"),
			`Deployment "w": spec.selector: the selector cannot be read`,
		},
		"an expression whose values are no list": {
			withSelector(unstructuredWorkload("apps/v1", "Deployment"), map[string]any{"matchExpressions": []any{
				map[string]any{"key": "app", "operator": "In", "values": "web"},
			}}, "spec"),
			`Deployment "w": spec.selector: the selector cannot be read`,
		},
		"a selector that is no object": {
			withSelector(unstructuredWorkload("apps/v1", "Deployment"), "oops", "spec"),
			`Deployment "w": spec: selector is a string, not an object`,
		},
		"a CronJob's job selector that does not decode": {
			withSelector(unstructuredWorkload("batch/v1", "CronJob"), map[string]any{"matchLabels": "oops"}, "spec", "jobTemplate", "spec"),
			`CronJob "w": spec.jobTemplate.spec.selector: the selector cannot be read`,
		},
		"a ReplicationController entry for the key that is no string": {
			withSelector(unstructuredWorkload("v1", "ReplicationController"), map[string]any{ownershipKey: int64(1)}, "spec"),
			`ReplicationController "w": spec.selector: the selector's entry "` + ownershipKey + `" is a int64, not a string`,
		},

		"a ReplicationController entry for another key that is no string": {
			withSelector(unstructuredWorkload("v1", "ReplicationController"), map[string]any{"app": int64(1)}, "spec"), "",
		},
		"a null selector": {withSelector(unstructuredWorkload("apps/v1", "Deployment"), nil, "spec"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{tc.obj}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Generate accepted a selector the check cannot read")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %s", err, tc.want)
			}
			var refused *ComponentLabelError
			if errors.As(err, &refused) {
				t.Errorf("error %v is a ComponentLabelError (%q), want another error", err, refused.Refused)
			}
		})
	}
}

// TestOwnedConfig_ComponentLabelSelectorRulesOut: a workload whose own selector
// matches a pod template that carries no value for the component label's key,
// and would not match it with the label, is refused: launcher could not label
// the pods, and no NetworkPolicy generated for the component would select them.
// The refusal names the selector. A selector that matches no template in the
// first place, or that rules out only another value, is not this check's.
func TestOwnedConfig_ComponentLabelSelectorRulesOut(t *testing.T) {
	ruledOut := func(op metav1.LabelSelectorOperator, values ...string) *metav1.LabelSelector {
		return &metav1.LabelSelector{
			MatchLabels:      map[string]string{"app": "web"},
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: ownershipKey, Operator: op, Values: values}},
		}
	}
	podLabels := map[string]string{"app": "web"}
	deployment := func(selector *metav1.LabelSelector, labels map[string]string) client.Object {
		d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
		d.Spec.Selector, d.Spec.Template.Labels = selector, maps.Clone(labels)
		return d
	}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	statefulSet.Spec.Selector, statefulSet.Spec.Template.Labels = ruledOut(metav1.LabelSelectorOpDoesNotExist), maps.Clone(podLabels)
	daemonSet := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	daemonSet.Spec.Selector, daemonSet.Spec.Template.Labels = ruledOut(metav1.LabelSelectorOpNotIn, "web"), maps.Clone(podLabels)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	job.Spec.Selector, job.Spec.Template.Labels = ruledOut(metav1.LabelSelectorOpDoesNotExist), maps.Clone(podLabels)
	cronJob := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
	cronJob.Spec.JobTemplate.Spec.Selector = ruledOut(metav1.LabelSelectorOpNotIn, "web")
	cronJob.Spec.JobTemplate.Spec.Template.Labels = maps.Clone(podLabels)
	rawSelector, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ruledOut(metav1.LabelSelectorOpDoesNotExist))
	if err != nil {
		t.Fatal(err)
	}
	unstructuredWith := func(apiVersion, kind string, spec ...string) *unstructured.Unstructured {
		u := unstructuredWorkload(apiVersion, kind)
		if err := unstructured.SetNestedMap(u.Object, rawSelector, append(slices.Clone(spec), "selector")...); err != nil {
			t.Fatal(err)
		}
		if err := unstructured.SetNestedStringMap(u.Object, podLabels, append(slices.Clone(spec), "template", "metadata", "labels")...); err != nil {
			t.Fatal(err)
		}
		return u
	}
	const rulesOut = "spec.selector rules out the label"

	for name, tc := range map[string]struct {
		obj client.Object
		// want is what the refusal says; empty when the object passes.
		want string
	}{
		"DoesNotExist":                {deployment(ruledOut(metav1.LabelSelectorOpDoesNotExist), podLabels), rulesOut},
		"NotIn the component's value": {deployment(ruledOut(metav1.LabelSelectorOpNotIn, "db", "web"), podLabels), rulesOut},
		"an empty pod template":       {deployment(&metav1.LabelSelector{MatchExpressions: ruledOut(metav1.LabelSelectorOpDoesNotExist).MatchExpressions}, nil), rulesOut},
		"a typed StatefulSet":         {statefulSet, rulesOut},
		"a typed DaemonSet":           {daemonSet, rulesOut},
		"a typed Job":                 {job, rulesOut},
		"an unstructured DaemonSet":   {unstructuredWith("apps/v1", "DaemonSet", "spec"), rulesOut},
		"an unstructured Job":         {unstructuredWith("batch/v1", "Job", "spec"), rulesOut},
		"a typed CronJob's job selector": {
			cronJob, "spec.jobTemplate.spec.selector rules out the label",
		},
		"an unstructured Deployment": {unstructuredWith("apps/v1", "Deployment", "spec"), rulesOut},
		"an unstructured CronJob": {
			unstructuredWith("batch/v1", "CronJob", "spec", "jobTemplate", "spec"), "spec.jobTemplate.spec.selector rules out the label",
		},
		"a List member": {
			&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
				unstructuredWith("apps/v1", "ReplicaSet", "spec").Object,
			}}}, `ReplicaSet "w": ` + rulesOut,
		},

		"NotIn another value only": {deployment(ruledOut(metav1.LabelSelectorOpNotIn, "db"), podLabels), ""},
		// The template matches no selector the cluster would accept with it: not
		// this check's to refuse.
		"a selector that matches no template": {deployment(ruledOut(metav1.LabelSelectorOpDoesNotExist), map[string]string{"app": "elsewhere"}), ""},
		// A template that carries the component's value is held to it, and this
		// selector does not match it; the cluster refuses that workload.
		"a template that carries the component's value": {
			deployment(ruledOut(metav1.LabelSelectorOpDoesNotExist), map[string]string{"app": "web", ownershipKey: "web"}), "",
		},
		"a selector that does not parse": {
			deployment(&metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: ownershipKey, Operator: "Sometimes"}}}, podLabels), "",
		},
		// A ReplicationController's selector is a plain label map, which a further
		// label on the pods never fails.
		"a ReplicationController": {
			func() client.Object {
				rc := &corev1.ReplicationController{ObjectMeta: metav1.ObjectMeta{Name: "w"}}
				rc.Spec.Selector = map[string]string{"app": "web"}
				rc.Spec.Template = &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: maps.Clone(podLabels)}}
				return rc
			}(), "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &ownershipObjectsConfig{objects: []client.Object{tc.obj}}
			_, err := stack.NewApplication("web", "ns", wrapOwnedConfig(inner, "web", ownershipKey)).Generate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Generate accepted a selector that rules the component label out")
			}
			for _, want := range []string{tc.want, `"` + ownershipKey + `"`, `component "web" ("web")`, "take the label out of the selector"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			}
			var refused *ComponentLabelError
			if !errors.As(err, &refused) || refused.Refused != ComponentLabelSelectorRulesOut {
				t.Errorf("refusal %v is not a ComponentLabelSelectorRulesOut", err)
			}
		})
	}
}

// appLabelled is a Deployment as a kind writes one for the component after
// lowering named name: the `app` label on the object, in the selector and on
// the pod template.
func appLabelled(name string) *appsv1.Deployment {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app": name}}}
	d.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}
	d.Spec.Template.Labels = map[string]string{"app": name}
	return d
}

// TestOwnedConfig_ComponentLabelUnderAppKey: with the component label key set
// to `app`, the label the kinds write themselves, an entry a lowering rule
// emitted under a name of its own carries that name's value, which launcher
// wrote. It is accepted on that entry's objects and on no other, and only under
// that key; every other value is refused as under any key.
func TestOwnedConfig_ComponentLabelUnderAppKey(t *testing.T) {
	generate := func(component, entry, key string, obj client.Object) error {
		inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
		_, err := stack.NewApplication(entry, "ns", wrapOwnedEntryConfig(inner, component, entry, key, nil)).Generate()
		return err
	}

	t.Run("the entry's own value is kept", func(t *testing.T) {
		d := appLabelled("web-renamed")
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web-renamed", Labels: map[string]string{"app": "web-renamed"}}}
		for _, obj := range []client.Object{d, service} {
			if err := generate("web", "web-renamed", "app", obj); err != nil {
				t.Fatalf("Generate: %v", err)
			}
		}
		if d.Labels["app"] != "web-renamed" || d.Spec.Template.Labels["app"] != "web-renamed" || service.Labels["app"] != "web-renamed" {
			t.Errorf("labels = %v / %v / %v, want the entry's value as the kinds wrote it", d.Labels, d.Spec.Template.Labels, service.Labels)
		}
	})

	t.Run("the owner's value is kept, and written where the key is absent", func(t *testing.T) {
		d := appLabelled("web")
		bare := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "bare"}}
		for _, obj := range []client.Object{d, bare} {
			if err := generate("web", "web-renamed", "app", obj); err != nil {
				t.Fatalf("Generate: %v", err)
			}
		}
		if d.Labels["app"] != "web" || d.Spec.Template.Labels["app"] != "web" {
			t.Errorf("labels = %v / %v, want the owner's value kept", d.Labels, d.Spec.Template.Labels)
		}
		if bare.Labels["app"] != "web" || bare.Spec.Template.Labels["app"] != "web" {
			t.Errorf("labels = %v / %v, want the owner's value written", bare.Labels, bare.Spec.Template.Labels)
		}
	})

	for name, tc := range map[string]struct {
		component, entry, key string
		obj                   client.Object
		want                  string
	}{
		"another component's value on the entry's object": {
			"web", "web-renamed", "app", appLabelled("db"), `metadata.labels["app"]: "db" is not the component label of component "web" ("web")`,
		},
		"another component's value on the pod template": {
			"web", "web-renamed", "app", func() client.Object {
				d := appLabelled("web-renamed")
				d.Spec.Template.Labels = map[string]string{"app": "db"}
				return d
			}(), `spec.template.metadata.labels["app"]: "db"`,
		},
		"a selector that requires another component's value": {
			"web", "web-renamed", "app", func() client.Object {
				d := appLabelled("web-renamed")
				d.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}}
				return d
			}(), `spec.selector requires "db" for the label "app"`,
		},
		// The exemption is the entry's: an entry under its component's own name
		// has one value.
		"another entry's value on a component's own entry": {
			"web", "web", "app", appLabelled("web-renamed"), `"web-renamed" is not the component label of component "web"`,
		},
		// And it is the `app` label's: under another key the kinds write nothing,
		// so the entry's name there is an authored value.
		"the entry's name under another key": {
			"web", "web-renamed", ownershipKey, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{ownershipKey: "web-renamed"}}},
			`"web-renamed" is not the component label of component "web"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := generate(tc.component, tc.entry, tc.key, tc.obj)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Generate = %v, want a refusal saying %s", err, tc.want)
			}
		})
	}
}

// renamedPartsRule lowers a "renamed-parts" component into a webservice named
// "<name>-renamed" and a worker named "<name>-other".
type renamedPartsRule struct{}

func (renamedPartsRule) ComponentType() string { return "renamed-parts" }

func (renamedPartsRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{
		{Name: comp.Name + "-renamed", Type: "webservice", Traits: comp.Traits},
		{Name: comp.Name + "-other", Type: "worker"},
	}}, nil
}

// TestTransform_ComponentLabelUnderAppKey_EntryOfEachApplication: the transform
// tells the wrapper of each application the entry it came from, so the entry's
// own `app` value passes on its application, on the sub-application a trait
// added to it and on its synthesized policies, and on no other application of
// the component.
func TestTransform_ComponentLabelUnderAppKey_EntryOfEachApplication(t *testing.T) {
	tr := ownershipTransformer()
	tr.RegisterComponentLowering(renamedPartsRule{})
	app := makeApp("shop", Component{Name: "api", Type: "renamed-parts", Traits: []Trait{
		{Type: "settings", Properties: map[string]any{"name": "api-settings"}},
		{Type: "routed", Properties: map[string]any{}},
	}})
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	cluster, err := tr.Transform(app, TransformContext{ComponentLabelKey: "app"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	entries := map[string]string{}
	walkBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, a := range bundle.Applications {
			owned, ok := a.Config.(componentOwner)
			if !ok {
				t.Fatalf("application %q has no ownership wrapper", a.Name)
			}
			if got := owned.owningComponent(); got != "api" {
				t.Errorf("application %q is owned by %q, want api", a.Name, got)
			}
			var w *ownedConfig
			switch c := a.Config.(type) {
			case *ownedConfig:
				w = c
			case *augmentingOwnedConfig:
				w = c.ownedConfig
			case *intentAugmentingOwnedConfig:
				w = c.ownedConfig
			default:
				t.Fatalf("application %q config is a %T", a.Name, a.Config)
			}
			entries[a.Name] = w.entry
		}
	})
	want := map[string]string{
		"api-renamed":                       "api-renamed",
		"api-settings":                      "api-renamed",
		"api-renamed-route":                 "api-renamed",
		"api-renamed-allow-ingress-traffic": "api-renamed",
		"api-other":                         "api-other",
	}
	for name, entry := range want {
		if got, ok := entries[name]; !ok || got != entry {
			t.Errorf("application %q: entry = %q (found %v), want %q; have %v", name, got, ok, entry, entries)
		}
	}
}

// TestTransform_ComponentLabelUnderAppKey_EntryNamedAsAnotherComponent: the
// entry's own value is accepted under the key `app` because it is no other
// component's. A rule that emits, for one component, an entry named as another
// authored component would make it one: that component is lowered into entries
// under other names, so no name collides, and its NetworkPolicies select by the
// value the first component's pods would carry. The transform refuses the
// document under `app`, and builds it under a key the kinds do not write.
func TestTransform_ComponentLabelUnderAppKey_EntryNamedAsAnotherComponent(t *testing.T) {
	document := func() *Application {
		app := makeApp("shop",
			Component{Name: "api", Type: "renamed-parts"},
			Component{Name: "api-renamed", Type: "renamed-parts"},
		)
		app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
		return app
	}
	tr := ownershipTransformer()
	tr.RegisterComponentLowering(renamedPartsRule{})
	_, err := tr.Transform(document(), TransformContext{ComponentLabelKey: "app"})
	want := `component "api": its lowering emitted "api-renamed", whose ` + "`app`" + ` label value "api-renamed" is the component label of component "api-renamed"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Transform under app = %v, want a refusal saying %s", err, want)
	}
	if _, err := tr.Transform(document(), TransformContext{}); err != nil {
		t.Fatalf("Transform under the default key: %v", err)
	}
}

// TestComponentLabelError: each refusal of the wrapper and of the entry check is
// a *ComponentLabelError that answers to ErrComponentLabelValue, and its fields
// say what its text says, so that a caller reads the component, the object, the
// path, the key and the value without parsing it. A refusal for another reason
// is neither. The fourth, of a kind's `labels` property, is held to the same in
// the components package, which has the kinds.
func TestComponentLabelError(t *testing.T) {
	generate := func(reserved *reservedMetadataKeys, obj client.Object) error {
		inner := &ownershipObjectsConfig{objects: []client.Object{obj}}
		_, err := stack.NewApplication("web", "shop", wrapOwnedConfigReserving(inner, "web", ownershipKey, reserved)).Generate()
		return err
	}
	transform := func() error {
		app := makeApp("shop",
			Component{Name: "api", Type: "renamed-parts"},
			Component{Name: "api-renamed", Type: "renamed-parts"},
		)
		app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
		tr := ownershipTransformer()
		tr.RegisterComponentLowering(renamedPartsRule{})
		_, err := tr.Transform(app, TransformContext{ComponentLabelKey: "app"})
		return err
	}

	for name, tc := range map[string]struct {
		err  error
		want ComponentLabelError
	}{
		"a value on a typed workload's pod template": {
			generate(nil, func() client.Object {
				d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}}
				d.Spec.Template.Labels = map[string]string{ownershipKey: "db"}
				return d
			}()),
			ComponentLabelError{
				Refused: ComponentLabelForeignValue, Component: "web",
				Kind: schema.GroupKind{Group: "apps", Kind: "Deployment"}, Namespace: "shop", Name: "web",
				Object: `Deployment "web"`, Path: "spec.template.metadata.labels",
				Key: ownershipKey, Value: "db", Want: "web",
			},
		},
		// On a list envelope the object is the member, and a namespace it does
		// not state stays empty.
		"a value on a List member": {
			generate(nil, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "List",
				"items": []any{map[string]any{
					"apiVersion": "v1", "kind": "ConfigMap",
					"metadata": map[string]any{"name": "c", "labels": map[string]any{ownershipKey: ""}},
				}},
			}}),
			ComponentLabelError{
				Refused: ComponentLabelForeignValue, Component: "web",
				Kind: schema.GroupKind{Kind: "ConfigMap"}, Name: "c",
				Object: `ConfigMap "c"`, Path: "metadata.labels",
				Key: ownershipKey, Value: "", Want: "web",
			},
		},
		"a selector that requires other values": {
			generate(nil, func() client.Object {
				d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}}
				d.Spec.Selector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: ownershipKey, Operator: metav1.LabelSelectorOpIn, Values: []string{"db", "cache"}},
				}}
				return d
			}()),
			ComponentLabelError{
				Refused: ComponentLabelSelectorRequiresAnother, Component: "web",
				Kind: schema.GroupKind{Group: "apps", Kind: "Deployment"}, Namespace: "shop", Name: "web",
				Object: `Deployment "web"`, Path: "spec.selector",
				Key: ownershipKey, Required: []string{"db", "cache"}, Want: "web",
			},
		},
		"a selector that rules the label out": {
			generate(nil, func() client.Object {
				d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}}
				d.Spec.Selector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: ownershipKey, Operator: metav1.LabelSelectorOpDoesNotExist},
				}}
				return d
			}()),
			ComponentLabelError{
				Refused: ComponentLabelSelectorRulesOut, Component: "web",
				Kind: schema.GroupKind{Group: "apps", Kind: "Deployment"}, Namespace: "shop", Name: "web",
				Object: `Deployment "web"`, Path: "spec.selector",
				Key: ownershipKey, Want: "web",
			},
		},
		"an entry whose value is another component's": {
			transform(),
			ComponentLabelError{
				Refused: ComponentLabelOfAnotherComponent, Component: "api",
				Key: "app", Value: "api-renamed", Want: "api",
				Entry: "api-renamed", Other: "api-renamed",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(tc.err, ErrComponentLabelValue) {
				t.Fatalf("the refusal %v does not answer to ErrComponentLabelValue", tc.err)
			}
			if errors.Is(tc.err, ErrReservedMetadataKey) {
				t.Errorf("the refusal %v answers to ErrReservedMetadataKey", tc.err)
			}
			var got *ComponentLabelError
			if !errors.As(tc.err, &got) {
				t.Fatalf("the refusal %v holds no *ComponentLabelError", tc.err)
			}
			if !reflect.DeepEqual(*got, tc.want) {
				t.Errorf("refusal = %+v\nwant      %+v", *got, tc.want)
			}
			if !strings.Contains(tc.err.Error(), got.Error()) {
				t.Errorf("the error %q does not hold the refusal's text %q", tc.err, got)
			}
		})
	}

	for name, err := range map[string]error{
		"a reserved key": generate(mustReserve(t, reservedForTest...),
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"example.org/tenant": "a"}}}),
		"a label value that is no string": generate(nil, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "c", "labels": map[string]any{ownershipKey: int64(1)}},
		}}),
	} {
		t.Run(name+" is not one", func(t *testing.T) {
			if err == nil {
				t.Fatal("Generate accepted the object")
			}
			var refusal *ComponentLabelError
			if errors.Is(err, ErrComponentLabelValue) || errors.As(err, &refusal) {
				t.Errorf("the refusal %v answers to ErrComponentLabelValue", err)
			}
		})
	}
}

// TestCheckEntryLabelValues holds the comparison to the label values, which a
// long name is projected onto, and to the key `app`.
func TestCheckEntryLabelValues(t *testing.T) {
	long := strings.Repeat("a", 70)
	for name, tc := range map[string]struct {
		owners  map[string]string
		key     string
		refused bool
	}{
		"an entry under its component's name":       {map[string]string{"web": "web", "db": "db"}, "app", false},
		"a renamed entry no component is named as":  {map[string]string{"web-main": "web", "db": "db"}, "app", false},
		"a renamed entry named as another":          {map[string]string{"db": "web", "db-main": "db"}, "app", true},
		"named as the projection of another's name": {map[string]string{ComponentLabelValue(long): "web", "x": long}, "app", true},
		"an entry of the document as a whole":       {map[string]string{"db": "", "db-main": "db"}, "app", false},
		"another key":                               {map[string]string{"db": "web", "db-main": "db"}, ownershipKey, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkEntryLabelValues(tc.owners, tc.key)
			if (err != nil) != tc.refused {
				t.Fatalf("checkEntryLabelValues = %v, want refused %v", err, tc.refused)
			}
		})
	}
}
