package oam

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
			object: kindObject("example.io/v1", "Widget", ""), refused: "the build does not know the kind's scope (it is neither a built-in kind nor one kure registers), so it is taken as cluster-scoped"},
		// The namespace is the author's to write, and the API server ignores it
		// on a cluster-scoped kind: it does not make an unknown kind namespaced.
		{name: "a kind of unknown scope in a namespace", policy: &kindPolicy{},
			object: kindObject("example.io/v1", "Widget", "ns"), refused: "a namespace the object states does not make it namespaced"},
		{name: "a kind of unknown scope where cluster-scoped objects are allowed", policy: &kindPolicy{allowCluster: true},
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

// listGrowingAugmenter generates its objects and, as it augments the layout,
// appends a member to the items of each list envelope on it, editing in place
// an object its Generate returned.
type listGrowingAugmenter struct {
	ownershipObjectsConfig
	member map[string]any
}

func (c *listGrowingAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	for _, r := range l.Resources {
		if u, ok := r.(*unstructured.Unstructured); ok && u.IsList() {
			u.Object["items"] = append(u.Object["items"].([]any), c.member)
		}
	}
	return nil
}

// TestObjectKindPolicy_AugmenterEditsItsOwnList: a member an augmenter appends
// to a list its own Generate returned is held to the policy, though the list
// was on the layout before it ran; an object a caller put on the layout is not.
func TestObjectKindPolicy_AugmenterEditsItsOwnList(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}, allowCluster: true}
	list := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List",
			"items": []any{kindObject("v1", "ConfigMap", "ns").Object},
		}}
	}
	role := kindObject("rbac.authorization.k8s.io/v1", "Role", "ns").Object

	generated := list()
	cfg := wrapOwnedEntryConfigKinds(&listGrowingAugmenter{ownershipObjectsConfig: ownershipObjectsConfig{objects: []client.Object{generated}}, member: role},
		"web", "web", ownershipKey, nil, mustKindRules(t, policy))
	objs, err := cfg.Generate(stack.NewApplication("web", "ns", cfg))
	if err != nil {
		t.Fatalf("Generate = %v, want the list of a ConfigMap generated", err)
	}
	l := &layout.ManifestLayout{}
	for _, p := range objs {
		if p != nil {
			l.Resources = append(l.Resources, *p)
		}
	}
	wantKindRefusal(t, cfg.(layout.LayoutAugmenter).AugmentLayout(l), "web", `Role "thing" (rbac.authorization.k8s.io/Role)`)

	// A list on the layout that this wrapper did not generate is the caller's,
	// and is not read (AugmentLayout).
	cfg = wrapOwnedEntryConfigKinds(&listGrowingAugmenter{member: role}, "web", "web", ownershipKey, nil, mustKindRules(t, policy))
	if err := cfg.(layout.LayoutAugmenter).AugmentLayout(&layout.ManifestLayout{Resources: []client.Object{list()}}); err != nil {
		t.Errorf("AugmentLayout = %v, want a caller's object left unread", err)
	}
}

// TestObjectKindPolicy_EmittedHoldsOneGeneration: a config generated again
// holds only its latest generation's objects for AugmentLayout to read again,
// so a generator that returns fresh objects on every call does not keep every
// earlier one.
func TestObjectKindPolicy_EmittedHoldsOneGeneration(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}, allowCluster: true}
	list := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List",
			"items": []any{kindObject("v1", "ConfigMap", "ns").Object},
		}}
	}
	inner := &listGrowingAugmenter{member: kindObject("rbac.authorization.k8s.io/v1", "Role", "ns").Object}
	cfg := wrapOwnedEntryConfigKinds(inner, "web", "web", ownershipKey, nil, mustKindRules(t, policy))
	owned := cfg.(*augmentingOwnedConfig).ownedConfig

	first, second := list(), list()
	for _, generated := range []client.Object{first, second} {
		inner.objects = []client.Object{generated}
		if _, err := cfg.Generate(stack.NewApplication("web", "ns", cfg)); err != nil {
			t.Fatalf("Generate = %v, want the list of a ConfigMap generated", err)
		}
	}
	if _, kept := owned.emitted[first]; kept || len(owned.emitted) != 1 {
		t.Errorf("emitted = %v, want only the latest generation's list", owned.emitted)
	}
	augmenter := cfg.(layout.LayoutAugmenter)
	if err := augmenter.AugmentLayout(&layout.ManifestLayout{Resources: []client.Object{first}}); err != nil {
		t.Errorf("AugmentLayout = %v, want an earlier generation's list left unread", err)
	}
	wantKindRefusal(t, augmenter.AugmentLayout(&layout.ManifestLayout{Resources: []client.Object{second}}),
		"web", `Role "thing" (rbac.authorization.k8s.io/Role)`)
}

// TestObjectKindPolicy_ListEnvelope: a list envelope is held to the policy by
// its members, which Flux applies, and not as an object of its own: a List with
// no namespace and of a kind no allowlist names passes when its members do.
func TestObjectKindPolicy_ListEnvelope(t *testing.T) {
	envelope := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "List",
		"items": []any{kindObject("v1", "ConfigMap", "ns").Object},
	}}
	policy := &kindPolicy{allowed: []schema.GroupKind{{Kind: "ConfigMap"}}}
	if err := generateUnderKinds(t, policy, "web", envelope); err != nil {
		t.Errorf("Generate = %v, want a List of an allowed, namespaced ConfigMap generated", err)
	}
}

// typedList is a typed client.Object that is written as a list envelope.
type typedList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Items             []runtime.RawExtension `json:"items"`
}

func (l *typedList) DeepCopyObject() runtime.Object {
	c := *l
	c.Items = slices.Clone(l.Items)
	return &c
}

// TestObjectKindPolicy_TypedList: a typed list envelope stands for its members
// as an unstructured one does, since it is written as a list Kustomize expands.
func TestObjectKindPolicy_TypedList(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}, allowCluster: true}
	list := func(member string) *typedList {
		return &typedList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "List"},
			Items:    []runtime.RawExtension{{Raw: []byte(member)}},
		}
	}
	wantKindRefusal(t, generateUnderKinds(t, policy, "web",
		list(`{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRole","metadata":{"name":"thing"}}`)),
		"web", `ClusterRole "thing" (rbac.authorization.k8s.io/ClusterRole)`)
	if err := generateUnderKinds(t, policy, "web",
		list(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"thing","namespace":"ns"}}`)); err != nil {
		t.Errorf("Generate = %v, want a typed List of a ConfigMap generated", err)
	}

	// An unstructured list whose items is a typed Go slice is written as the
	// same list.
	sliced := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "List",
		"items": []map[string]any{kindObject("rbac.authorization.k8s.io/v1", "ClusterRole", "").Object},
	}}
	wantKindRefusal(t, generateUnderKinds(t, policy, "web", sliced), "web", `ClusterRole "thing" (rbac.authorization.k8s.io/ClusterRole)`)

	// So is a list inside a list whose items is a typed Go slice, and a typed
	// member of an envelope Flux expands whatever its kind.
	nested := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "List",
		"items": []any{map[string]any{
			"apiVersion": "v1", "kind": "List",
			"items": []map[string]any{kindObject("rbac.authorization.k8s.io/v1", "ClusterRole", "").Object},
		}},
	}}
	wantKindRefusal(t, generateUnderKinds(t, policy, "web", nested), "web", `ClusterRole "thing" (rbac.authorization.k8s.io/ClusterRole)`)
	role := &unstructured.Unstructured{}
	role.SetAPIVersion("rbac.authorization.k8s.io/v1")
	role.SetKind("Role")
	role.SetName("thing")
	role.SetNamespace("ns")
	mixed := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.io/v1", "kind": "Bundle", "metadata": map[string]any{"name": "b", "namespace": "ns"},
		"items": []any{role},
	}}
	wantKindRefusal(t, generateUnderKinds(t, policy, "web", mixed), "web", `Role "thing" (rbac.authorization.k8s.io/Role)`)

	// A typed List with no items is written with items null, which applies
	// nothing: it is not held to the policy as an object of its own, at any
	// depth.
	allowConfigMaps := &kindPolicy{allowed: []schema.GroupKind{{Kind: "ConfigMap"}}}
	empty := &typedList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "List"}}
	if err := generateUnderKinds(t, allowConfigMaps, "web", empty); err != nil {
		t.Errorf("Generate = %v, want an empty typed List generated", err)
	}
	if err := generateUnderKinds(t, allowConfigMaps, "web", list(`{"apiVersion":"v1","kind":"List","items":null}`)); err != nil {
		t.Errorf("Generate = %v, want a List of an empty List generated", err)
	}
}

// TestObjectKindPolicy_NullListsOnlyForTheKindCheck: the List Kustomize drops
// for a null items is dropped for the object kind check alone. The traversal the
// component label, the reserved keys and the forced-volume warnings read keeps
// it, and the object the check reads is not changed.
func TestObjectKindPolicy_NullListsOnlyForTheKindCheck(t *testing.T) {
	nested := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List",
			"items": []any{map[string]any{"apiVersion": "v1", "kind": "List", "items": nil}},
		}}
	}
	if got := appliedObjects(nested()); len(got) != 1 || got[0].GetObjectKind().GroupVersionKind().Kind != "List" {
		t.Errorf("appliedObjects = %v, want the inner List kept", got)
	}
	checked := nested()
	if got := kindAppliedObjects(checked); len(got) != 0 {
		t.Errorf("kindAppliedObjects = %v, want nothing", got)
	}
	if !reflect.DeepEqual(checked, nested()) {
		t.Errorf("kindAppliedObjects changed its object: %v", checked.Object)
	}
}

// generatorAugmenter adds a configMapGenerator entry to the layout it augments.
type generatorAugmenter struct{ ownershipObjectsConfig }

func (c *generatorAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	l.ConfigMapGenerators = append(l.ConfigMapGenerators, layout.ConfigMapGeneratorSpec{Name: "values", Files: []string{"values.yaml"}})
	return nil
}

// TestObjectKindPolicy_AddedGenerator: a configMapGenerator entry an augmenter
// adds is held to the policy as the ConfigMap Kustomize builds from it; one
// that was on the layout before it ran is the caller's, and is not read.
func TestObjectKindPolicy_AddedGenerator(t *testing.T) {
	policy := &kindPolicy{forbidden: []schema.GroupKind{{Kind: "ConfigMap"}}}
	augment := func(l *layout.ManifestLayout) error {
		cfg := wrapOwnedEntryConfigKinds(&generatorAugmenter{}, "web", "web", ownershipKey, nil, mustKindRules(t, policy))
		return cfg.(layout.LayoutAugmenter).AugmentLayout(l)
	}
	wantKindRefusal(t, augment(&layout.ManifestLayout{Namespace: "ns"}), "web", `ConfigMap "values" (core/ConfigMap)`)
	held := &layout.ManifestLayout{Namespace: "ns", ConfigMapGenerators: []layout.ConfigMapGeneratorSpec{{Name: "values"}}}
	if err := augment(held); err != nil {
		t.Errorf("AugmentLayout = %v, want a caller's generator of the same name left unread", err)
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

// TestObjectKindPolicy_NamespacedCRDInTheBuild: a CustomResourceDefinition in
// the build that declares its kind Namespaced cannot make a custom resource of
// that kind pass a policy that does not allow cluster-scoped objects, in either
// order: the CRD is itself cluster-scoped and refused. This is why the check
// reads no CRD scope.
func TestObjectKindPolicy_NamespacedCRDInTheBuild(t *testing.T) {
	crd := kindObject("apiextensions.k8s.io/v1", "CustomResourceDefinition", "")
	crd.SetName("widgets.example.io")
	crd.Object["spec"] = map[string]any{
		"group": "example.io", "scope": "Namespaced",
		"names": map[string]any{"kind": "Widget", "plural": "widgets"},
	}
	widget := kindObject("example.io/v1", "Widget", "ns")
	err := generateUnderKinds(t, &kindPolicy{}, "web", crd, widget)
	wantKindRefusal(t, err, "web", `CustomResourceDefinition "widgets.example.io"`, "the kind is cluster-scoped")
	err = generateUnderKinds(t, &kindPolicy{}, "web", widget, crd)
	wantKindRefusal(t, err, "web", `Widget "thing" (example.io/Widget)`, "does not know the kind's scope")
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
