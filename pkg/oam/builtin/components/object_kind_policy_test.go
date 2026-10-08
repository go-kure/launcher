package components_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The object kind policy (go-kure/launcher#922) on every way a component emits
// an object: a kind component's own object, an object passthrough or a
// manifests source carries, and a chart rendered at build time. A trait's
// objects are held in the traits package.

// okPolicy restricts nothing but the kinds of object, through ObjectKindPolicy.
type okPolicy struct {
	stubPolicy
	allowed, forbidden []schema.GroupKind
	allowCluster       bool
}

func (p *okPolicy) AllowedObjectKinds() []schema.GroupKind   { return p.allowed }
func (p *okPolicy) ForbiddenObjectKinds() []schema.GroupKind { return p.forbidden }
func (p *okPolicy) AllowClusterScopedObjects() bool          { return p.allowCluster }

var _ oam.ObjectKindPolicy = (*okPolicy)(nil)

// okForbidRBAC forbids the RBAC group and allows cluster-scoped objects.
func okForbidRBAC() *okPolicy {
	return &okPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}, allowCluster: true}
}

const (
	okRole        = "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: thing\nrules: []\n"
	okConfigMap   = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  a: b\n"
	okClusterRole = "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: thing\nrules: []\n"
	// okUnknownKind is a custom resource whose CRD is not in the build, so its
	// scope is unknown; the namespace it states does not make it namespaced.
	okUnknownKind = "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: thing\n  namespace: demo\n"
)

// okWantRefusal fails unless err is component web's violation of class
// RefusalObjectKind whose text holds every fragment.
func okWantRefusal(t *testing.T, err error, fragments ...string) {
	t.Helper()
	htWantViolation(t, err, fragments...)
	var v *oam.ViolationError
	if errors.As(err, &v) && v.Class != oam.RefusalObjectKind {
		t.Errorf("violation has class %q, want %q: %v", v.Class, oam.RefusalObjectKind, err)
	}
}

// TestObjectKindPolicy_KindComponent: a kind component's own object is held to
// the policy at generation.
func TestObjectKindPolicy_KindComponent(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"deployment": &components.DeploymentHandler{}}, nil)
	app := mfApp("deployment", map[string]any{"image": "registry.example/team/app:1.2.3"})
	generate := func(policy oam.Policy) error {
		cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Policy: policy})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		_, err = oam.GenerateApplications(cluster)
		return err
	}
	okWantRefusal(t, generate(&okPolicy{forbidden: []schema.GroupKind{{Group: "apps", Kind: "Deployment"}}, allowCluster: true}),
		`Deployment "web" (apps/Deployment): the object kind policy forbids the kind`)
	okWantRefusal(t, generate(&okPolicy{allowed: []schema.GroupKind{{Kind: "ConfigMap"}}, allowCluster: true}),
		"not among those the object kind policy allows")
	if err := generate(&okPolicy{allowed: []schema.GroupKind{{Group: "apps", Kind: "*"}}}); err != nil {
		t.Errorf("GenerateApplications = %v, want the allowed, namespaced Deployment generated", err)
	}
}

// TestObjectKindPolicy_ClusterScopedKindComponent: the crd component emits a
// CustomResourceDefinition, which is cluster-scoped, and is refused under a
// policy that does not allow cluster-scoped objects.
func TestObjectKindPolicy_ClusterScopedKindComponent(t *testing.T) {
	crd := "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.io\nspec:\n" +
		"  group: example.io\n  names:\n    kind: Widget\n    plural: widgets\n  scope: Namespaced\n" +
		"  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n"
	_, err := mfTransform("crd", mfInline(crd), &okPolicy{})
	okWantRefusal(t, err, `CustomResourceDefinition "widgets.example.io"`, "cluster-scoped")
	if _, err := mfTransform("crd", mfInline(crd), &okPolicy{allowCluster: true}); err != nil {
		t.Errorf("transform = %v, want the CRD built where cluster-scoped objects are allowed", err)
	}
}

// TestObjectKindPolicy_ObjectsWrittenElsewhere: an object passthrough or a
// manifests source carries, and one a chart renders, is refused for its kind
// and for its scope, each refusal naming the object, a kind of unknown scope
// included whatever namespace it states; an allowed kind is built.
func TestObjectKindPolicy_ObjectsWrittenElsewhere(t *testing.T) {
	paths := []struct {
		name  string
		build func(t *testing.T, doc string, policy oam.Policy) error
	}{
		{"passthrough", func(t *testing.T, doc string, policy oam.Policy) error {
			_, err := ptTransform(ptObject(t, doc), policy)
			return err
		}},
		{"manifests, inline", func(_ *testing.T, doc string, policy oam.Policy) error {
			_, err := mfTransform("manifests", mfInline(doc), policy)
			return err
		}},
		{"manifests, a url source", func(t *testing.T, doc string, policy oam.Policy) error {
			srv, _ := mfServe(t, doc)
			_, err := mfTransform("manifests", map[string]any{"url": srv.URL + "/manifests.yaml"}, policy)
			return err
		}},
		{"template delivery", func(t *testing.T, doc string, policy oam.Policy) error {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"object.yaml": doc})
			_, err := htTransform(srvURL, policy)
			return err
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			okWantRefusal(t, path.build(t, okRole, okForbidRBAC()), `Role "thing" (rbac.authorization.k8s.io/Role)`, "forbids the kind")
			okWantRefusal(t, path.build(t, okClusterRole, &okPolicy{}), `ClusterRole "thing"`, "cluster-scoped")
			okWantRefusal(t, path.build(t, okUnknownKind, &okPolicy{}), `Widget "thing" (example.io/Widget)`, "does not know the kind's scope")
			if err := path.build(t, okUnknownKind, &okPolicy{allowCluster: true}); err != nil {
				t.Errorf("build = %v, want a kind of unknown scope built where cluster-scoped objects are allowed", err)
			}
			if err := path.build(t, okConfigMap, okForbidRBAC()); err != nil {
				t.Errorf("build = %v, want an allowed kind built", err)
			}
		})
	}
}

// TestObjectKindPolicy_RenderedChart: a chart rendered at build time is held to
// the policy object by object, its hook groups included: the refusal names the
// one object of a forbidden kind among the allowed ones, whether the chart is
// generated, walked by kure's layout walker, or laid out by AugmentLayout
// before any Generate.
func TestObjectKindPolicy_RenderedChart(t *testing.T) {
	chart := map[string]string{
		"settings.yaml": okConfigMap,
		"role.yaml":     okRole,
		"hook.yaml": "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: migrate\n  annotations:\n    helm.sh/hook: pre-install\nspec:\n" +
			"  template:\n    spec:\n      restartPolicy: Never\n" + htIndent(htPlainPod, "      "),
	}
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", chart)
	const role = `Role "thing" (rbac.authorization.k8s.io/Role)`

	t.Run("generated", func(t *testing.T) {
		_, err := htTransform(srvURL, okForbidRBAC())
		okWantRefusal(t, err, role)
	})

	transform := func(t *testing.T, policy oam.Policy) *oamClusterApp {
		t.Helper()
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}}, nil)
		cluster, err := tr.Transform(mfApp("helmtemplate", map[string]any{
			"chart": "testchart", "version": "0.1.0", "source": map[string]any{"url": srvURL},
		}), oam.TransformContext{Namespace: "demo", Policy: policy})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		return &oamClusterApp{cluster: cluster, augmenter: cluster.Node.Bundle.Applications[0].Config.(layout.LayoutAugmenter)}
	}
	t.Run("walked", func(t *testing.T) {
		_, err := layout.WalkCluster(transform(t, okForbidRBAC()).cluster, layout.DefaultLayoutRules())
		okWantRefusal(t, err, role)
	})
	t.Run("AugmentLayout before any Generate", func(t *testing.T) {
		err := transform(t, okForbidRBAC()).augmenter.AugmentLayout(&layout.ManifestLayout{Name: "web", Namespace: "demo"})
		okWantRefusal(t, err, role)
	})
	t.Run("a forbidden hook", func(t *testing.T) {
		policy := &okPolicy{forbidden: []schema.GroupKind{{Group: "batch", Kind: "Job"}}, allowCluster: true}
		err := transform(t, policy).augmenter.AugmentLayout(&layout.ManifestLayout{Name: "web", Namespace: "demo"})
		okWantRefusal(t, err, `Job "migrate" (batch/Job)`)
	})
}

// oamClusterApp is a transformed cluster and its one application's config as
// a layout augmenter.
type oamClusterApp struct {
	cluster   *stack.Cluster
	augmenter layout.LayoutAugmenter
}

// TestObjectKindPolicy_ReadsOnly: a policy that implements ObjectKindPolicy
// and lets every object through builds exactly what NoopPolicy builds: the
// check reads the objects and writes nothing.
func TestObjectKindPolicy_ReadsOnly(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htTemplateChart)
	noop, err := htTransform(srvURL, &oam.NoopPolicy{})
	if err != nil {
		t.Fatalf("under NoopPolicy: %v", err)
	}
	permissive, err := htTransform(srvURL, &okPolicy{allowCluster: true})
	if err != nil {
		t.Fatalf("under a permissive object kind policy: %v", err)
	}
	if !reflect.DeepEqual(okNames(noop), okNames(permissive)) || !reflect.DeepEqual(noop, permissive) {
		t.Errorf("the build differs: %v under NoopPolicy, %v under a permissive object kind policy", okNames(noop), okNames(permissive))
	}
}

func okNames(objs []client.Object) []string {
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, o.GetObjectKind().GroupVersionKind().Kind+"/"+o.GetName())
	}
	return names
}
