package oam

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// kindPolicy is a Policy that implements ObjectKindPolicy.
type kindPolicy struct {
	NoopPolicy
	allowed, forbidden []schema.GroupKind
	allowCluster       bool
}

func (p *kindPolicy) AllowedObjectKinds() []schema.GroupKind   { return p.allowed }
func (p *kindPolicy) ForbiddenObjectKinds() []schema.GroupKind { return p.forbidden }
func (p *kindPolicy) AllowClusterScopedObjects() bool          { return p.allowCluster }

var _ ObjectKindPolicy = (*kindPolicy)(nil)

// mustKindRules is p as the check reads it.
func mustKindRules(t *testing.T, p Policy) *objectKindRules {
	t.Helper()
	rules, err := objectKindRulesOf(p)
	if err != nil {
		t.Fatalf("objectKindRulesOf: %v", err)
	}
	return rules
}

// kindObject is an unstructured object of the given apiVersion and kind, named
// thing, in namespace when that is not empty.
func kindObject(apiVersion, kind, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "thing"},
	}}
	if namespace != "" {
		u.SetNamespace(namespace)
	}
	return u
}

// generateUnderKinds generates an application whose config emits objs, wrapped
// for component under p's object kind rules.
func generateUnderKinds(t *testing.T, p Policy, component string, objs ...client.Object) error {
	t.Helper()
	cfg := wrapOwnedEntryConfigKinds(&ownershipObjectsConfig{objects: objs}, component, component, ownershipKey, nil, mustKindRules(t, p))
	_, err := stack.NewApplication("web", "ns", cfg).Generate()
	return err
}

// wantKindRefusal fails unless err is the violation of component owner, of
// class RefusalObjectKind, whose text holds every fragment.
func wantKindRefusal(t *testing.T, err error, owner string, fragments ...string) {
	t.Helper()
	var v *ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error = %v, want a *ViolationError", err)
	}
	if v.Component != owner || v.Class != RefusalObjectKind {
		t.Errorf("violation = component %q class %q, want %q %q: %v", v.Component, v.Class, owner, RefusalObjectKind, err)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("refusal %q lacks %q", err, f)
		}
	}
}

// TestObjectKindPolicy_Rules: each rule of the policy on one object.
func TestObjectKindPolicy_Rules(t *testing.T) {
	apps := func(kind string) schema.GroupKind { return schema.GroupKind{Group: "apps", Kind: kind} }
	core := func(kind string) schema.GroupKind { return schema.GroupKind{Kind: kind} }
	cases := []struct {
		name   string
		policy *kindPolicy
		object client.Object
		// refused is a fragment of the refusal, "" for an object that passes.
		refused string
	}{
		{name: "a forbidden kind", policy: &kindPolicy{forbidden: []schema.GroupKind{apps("Deployment")}, allowCluster: true},
			object: kindObject("apps/v1", "Deployment", "ns"), refused: `Deployment "thing" (apps/Deployment): the object kind policy forbids the kind`},
		{name: "a forbidden kind in another version", policy: &kindPolicy{forbidden: []schema.GroupKind{apps("Deployment")}, allowCluster: true},
			object: kindObject("apps/v1beta2", "Deployment", "ns"), refused: "forbids the kind"},
		{name: "a forbidden group", policy: &kindPolicy{forbidden: []schema.GroupKind{apps("*")}, allowCluster: true},
			object: kindObject("apps/v1", "StatefulSet", "ns"), refused: "forbids the kind"},
		{name: "a forbidden group spares the core group", policy: &kindPolicy{forbidden: []schema.GroupKind{apps("*")}, allowCluster: true},
			object: kindObject("v1", "ConfigMap", "ns")},
		{name: "a forbidden core kind", policy: &kindPolicy{forbidden: []schema.GroupKind{core("Secret")}, allowCluster: true},
			object: kindObject("v1", "Secret", "ns"), refused: `Secret "thing" (core/Secret)`},
		{name: "a kind not allowed", policy: &kindPolicy{allowed: []schema.GroupKind{core("ConfigMap")}, allowCluster: true},
			object: kindObject("apps/v1", "Deployment", "ns"), refused: "the kind is not among those the object kind policy allows"},
		{name: "an allowed kind", policy: &kindPolicy{allowed: []schema.GroupKind{core("ConfigMap")}, allowCluster: true},
			object: kindObject("v1", "ConfigMap", "ns")},
		{name: "an allowed group", policy: &kindPolicy{allowed: []schema.GroupKind{apps("*")}, allowCluster: true},
			object: kindObject("apps/v1", "DaemonSet", "ns")},
		{name: "forbidden wins over allowed", policy: &kindPolicy{allowed: []schema.GroupKind{apps("*")}, forbidden: []schema.GroupKind{apps("DaemonSet")}, allowCluster: true},
			object: kindObject("apps/v1", "DaemonSet", "ns"), refused: "forbids the kind"},
		{name: "a cluster-scoped kind", policy: &kindPolicy{},
			object: kindObject("rbac.authorization.k8s.io/v1", "ClusterRole", ""), refused: "the kind is cluster-scoped, and the object kind policy does not allow cluster-scoped objects"},
		{name: "a cluster-scoped kind that states a namespace", policy: &kindPolicy{},
			object: kindObject("v1", "Namespace", "ns"), refused: "the kind is cluster-scoped"},
		{name: "a CustomResourceDefinition", policy: &kindPolicy{},
			object: kindObject("apiextensions.k8s.io/v1", "CustomResourceDefinition", ""), refused: "the kind is cluster-scoped"},
		{name: "a namespaced kind with no namespace", policy: &kindPolicy{},
			object: kindObject("apps/v1", "Deployment", "")},
		{name: "a kind of unknown scope with no namespace", policy: &kindPolicy{},
			object: kindObject("example.io/v1", "Widget", ""), refused: "the build does not know the kind's scope and the object carries no namespace, so it is taken as cluster-scoped"},
		{name: "a kind of unknown scope in a namespace", policy: &kindPolicy{},
			object: kindObject("example.io/v1", "Widget", "ns")},
		{name: "cluster-scoped objects allowed", policy: &kindPolicy{allowCluster: true},
			object: kindObject("rbac.authorization.k8s.io/v1", "ClusterRole", "")},
		{name: "a typed object that states no kind", policy: &kindPolicy{forbidden: []schema.GroupKind{core("ConfigMap")}, allowCluster: true},
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "thing"}}, refused: `ConfigMap "thing" (core/ConfigMap)`},
		{name: "a typed cluster-scoped object that states no kind", policy: &kindPolicy{},
			object: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "thing"}}, refused: "the kind is cluster-scoped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := generateUnderKinds(t, tc.policy, "web", tc.object)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("Generate = %v, want the object to pass", err)
				}
				return
			}
			wantKindRefusal(t, err, "web", tc.refused)
		})
	}
}

// TestObjectKindPolicy_EveryObjectIsChecked: each object an application emits
// is held to the policy on its own, and so is each member of a list envelope,
// as Flux applies it. The refusal names the object that is refused.
func TestObjectKindPolicy_EveryObjectIsChecked(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}, allowCluster: true}
	allowed := kindObject("v1", "ConfigMap", "ns")

	t.Run("the second object", func(t *testing.T) {
		err := generateUnderKinds(t, policy, "web", allowed, kindObject("rbac.authorization.k8s.io/v1", "Role", "ns"))
		wantKindRefusal(t, err, "web", `Role "thing"`)
	})
	t.Run("a list member", func(t *testing.T) {
		list := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List", "metadata": map[string]any{},
			"items": []any{allowed.Object, kindObject("rbac.authorization.k8s.io/v1", "RoleBinding", "ns").Object},
		}}
		err := generateUnderKinds(t, policy, "web", list)
		wantKindRefusal(t, err, "web", `RoleBinding "thing"`)
	})
	t.Run("a list of allowed members", func(t *testing.T) {
		list := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMapList", "metadata": map[string]any{},
			"items": []any{allowed.Object},
		}}
		if err := generateUnderKinds(t, policy, "web", list); err != nil {
			t.Fatalf("Generate = %v, want a list of allowed members to pass", err)
		}
	})
}

// kindAugmenter hands out no object and adds its objects to its layout, in a
// child, as a rendered chart's hook groups are added.
type kindAugmenter struct {
	ownershipObjectsConfig
	added []client.Object
}

func (c *kindAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	l.Children = append(l.Children, &layout.ManifestLayout{Name: "hook", Resources: c.added})
	return nil
}

// TestObjectKindPolicy_AugmentedLayout: what a layout augmenter adds outside
// Generate is held to the policy, each object on its own, for a component and
// for an application the document as a whole owns, which the refusal names by
// the entry it came from.
func TestObjectKindPolicy_AugmentedLayout(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Group: "batch", Kind: "Job"}}}
	for _, owner := range []struct{ component, entry string }{{"web", "web"}, {"", "shop-source-1"}} {
		added := []client.Object{kindObject("v1", "ConfigMap", "ns"), kindObject("batch/v1", "Job", "ns")}
		cfg := wrapOwnedEntryConfigKinds(&kindAugmenter{added: added}, owner.component, owner.entry, ownershipKey, nil, mustKindRules(t, policy))
		aug, ok := cfg.(layout.LayoutAugmenter)
		if !ok {
			t.Fatal("a wrapped augmenter is no LayoutAugmenter")
		}
		err := aug.AugmentLayout(&layout.ManifestLayout{})
		wantKindRefusal(t, err, owner.entry, `Job "thing" (batch/Job)`)
	}

	// A cluster-scoped object the augmenter adds, under a policy that allows
	// every kind but no cluster-scoped object.
	added := []client.Object{kindObject("v1", "ConfigMap", "ns"), kindObject("rbac.authorization.k8s.io/v1", "ClusterRole", "")}
	aug := wrapOwnedEntryConfigKinds(&kindAugmenter{added: added}, "web", "web", ownershipKey, nil, mustKindRules(t, &kindPolicy{})).(layout.LayoutAugmenter)
	wantKindRefusal(t, aug.AugmentLayout(&layout.ManifestLayout{}), "web", `ClusterRole "thing"`, "cluster-scoped")

	// The objects that were on the layout passed through Generate and are not
	// read again.
	there := &layout.ManifestLayout{Resources: []client.Object{kindObject("batch/v1", "Job", "ns")}}
	aug = wrapOwnedEntryConfigKinds(&kindAugmenter{}, "web", "web", ownershipKey, nil, mustKindRules(t, policy)).(layout.LayoutAugmenter)
	if err := aug.AugmentLayout(there); err != nil {
		t.Errorf("AugmentLayout = %v, want what was on the layout left to Generate", err)
	}
}

// TestObjectKindPolicy_DocumentOwnedApplication: an application the document as
// a whole owns is held to the policy too, and its refusal names its entry.
func TestObjectKindPolicy_DocumentOwnedApplication(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Kind: "ConfigMap"}}, allowCluster: true}
	cfg := wrapOwnedEntryConfigKinds(&ownershipObjectsConfig{objects: []client.Object{kindObject("v1", "ConfigMap", "ns")}}, "", "shop-source-1", ownershipKey, nil, mustKindRules(t, policy))
	_, err := stack.NewApplication("shop-source-1", "ns", cfg).Generate()
	wantKindRefusal(t, err, "shop-source-1", `ConfigMap "thing"`)
}

// TestObjectKindPolicy_WithoutTheInterface: a Policy that does not implement
// ObjectKindPolicy gives no rules, NoopPolicy included, and the wrapper then
// never reaches the check: an object of a kind whose scope is unknown, with no
// namespace, which the check would take as cluster-scoped, is generated as it
// was, by a component's wrapper and by a document-owned one.
func TestObjectKindPolicy_WithoutTheInterface(t *testing.T) {
	for _, p := range []Policy{&NoopPolicy{}, nil, (*kindPolicy)(nil)} {
		if rules, err := objectKindRulesOf(p); rules != nil || err != nil {
			t.Errorf("objectKindRulesOf(%T) = %v, %v, want no rules", p, rules, err)
		}
	}
	for _, component := range []string{"web", ""} {
		obj := kindObject("example.io/v1", "Widget", "")
		before := obj.DeepCopy()
		cfg := wrapOwnedEntryConfigKinds(&ownershipObjectsConfig{objects: []client.Object{obj}}, component, component, ownershipKey, nil, nil)
		if _, err := stack.NewApplication("web", "ns", cfg).Generate(); err != nil {
			t.Fatalf("component %q: Generate = %v, want the object generated", component, err)
		}
		if component == "" && !reflect.DeepEqual(obj, before) {
			t.Errorf("document-owned object changed: %v, want %v", obj.Object, before.Object)
		}
	}
}

// TestObjectKindPolicy_EntryWithoutKind: an entry with no Kind is refused when
// the rules are read, on either list, naming the method and the index.
func TestObjectKindPolicy_EntryWithoutKind(t *testing.T) {
	for name, p := range map[string]*kindPolicy{
		"AllowedObjectKinds()[1]":   {allowed: []schema.GroupKind{{Kind: "ConfigMap"}, {Group: "apps"}}},
		"ForbiddenObjectKinds()[0]": {forbidden: []schema.GroupKind{{Group: "apps"}}},
	} {
		_, err := objectKindRulesOf(p)
		if err == nil || !strings.Contains(err.Error(), "ObjectKindPolicy."+name) {
			t.Errorf("objectKindRulesOf = %v, want a refusal naming %s", err, name)
		}
	}
}

// TestObjectKindPolicy_Transform: the transform reads the policy's rules once,
// refuses an entry with no Kind before it builds anything, and wraps every
// application with the rules, so that generation refuses a forbidden kind.
func TestObjectKindPolicy_Transform(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"objects": &ownershipHandler{}}, nil)
	app := &Application{
		Metadata: Metadata{Name: "shop"},
		Spec:     ApplicationSpec{Components: []Component{{Name: "web", Type: "objects"}}},
	}

	_, err := tr.Transform(app, TransformContext{Namespace: "ns", Policy: &kindPolicy{forbidden: []schema.GroupKind{{Group: "apps"}}}})
	if err == nil || !strings.Contains(err.Error(), "ObjectKindPolicy.ForbiddenObjectKinds()[0]") {
		t.Fatalf("Transform = %v, want the entry with no kind refused", err)
	}

	cluster, err := tr.Transform(app, TransformContext{Namespace: "ns", Policy: &kindPolicy{forbidden: []schema.GroupKind{{Group: "apps", Kind: "Deployment"}}, allowCluster: true}})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	_, err = GenerateApplications(cluster)
	wantKindRefusal(t, err, "web", `Deployment "web"`)

	cluster, err = tr.Transform(app, TransformContext{Namespace: "ns", Policy: &kindPolicy{allowed: []schema.GroupKind{{Group: "apps", Kind: "*"}}}})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if _, err := GenerateApplications(cluster); err != nil {
		t.Fatalf("GenerateApplications = %v, want the allowed Deployment generated", err)
	}
}

// ownershipHandler emits one Deployment named after its component.
type ownershipHandler struct{}

func (*ownershipHandler) CanHandle(componentType string) bool { return componentType == "objects" }

func (*ownershipHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	return &ownershipObjectsConfig{objects: []client.Object{&appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: c.Name},
	}}}, nil
}
