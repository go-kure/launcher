package model_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the lowering rule and the registry of a model (go-kure/launcher#1261): a
// component of a model type lowers to the parts of the model inside a real Transform, a part
// is named by the allocator, and `parts.<part>.<key>` reads the name the build gives.

const webappType = "webapp.acme.example@v1"

// registered returns a Transformer that holds the model in doc, registered through a Registry.
func registered(t *testing.T, doc string) (*oam.Transformer, *model.Registry) {
	t.Helper()
	tr := expandTransformer(t)
	reg := model.NewRegistry(tr)
	m, err := model.Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return tr, reg
}

// objects is every object a Transform of comps generates, as JSON, by (kind, name).
func objects(t *testing.T, tr *oam.Transformer, comps []oam.Component, naming func(oam.NameRequest) (string, bool)) (map[[2]string]map[string]any, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec:     oam.ApplicationSpec{Components: comps},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Naming: naming})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	out := map[[2]string]map[string]any{}
	for _, a := range apps {
		for _, o := range a.Objects {
			obj := *o
			kind := obj.GetObjectKind().GroupVersionKind().Kind
			if kind == "" {
				kind = reflect.TypeOf(obj).Elem().Name()
			}
			raw, err := json.Marshal(obj)
			if err != nil {
				t.Fatalf("marshal %s %s: %v", kind, obj.GetName(), err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal %s %s: %v", kind, obj.GetName(), err)
			}
			out[[2]string{kind, obj.GetName()}] = m
		}
	}
	return out, nil
}

func objectsOK(t *testing.T, tr *oam.Transformer, comps []oam.Component, naming func(oam.NameRequest) (string, bool)) map[[2]string]map[string]any {
	t.Helper()
	objs, err := objects(t, tr, comps, naming)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	return objs
}

func names(objs map[[2]string]map[string]any, kind string) []string {
	var out []string
	for k := range objs {
		if k[0] == kind {
			out = append(out, k[1])
		}
	}
	return out
}

// workerSecretName is the name the one Deployment of the build reads its database Secret under.
func workerSecretName(t *testing.T, objs map[[2]string]map[string]any) any {
	t.Helper()
	deps := names(objs, "Deployment")
	if len(deps) != 1 {
		t.Fatalf("the build made %d Deployments (%v), want 1", len(deps), deps)
	}
	return prop(t, objs[[2]string{"Deployment", deps[0]}], "spec", "template", "spec", "containers", 0, "env", 0, "valueFrom", "secretKeyRef", "name")
}

func orders() []oam.Component {
	return []oam.Component{{Name: "orders", Type: webappType}}
}

// Case 1: a worker part reads the Secret that a postgresql part generates, and the build makes
// that Secret's Cluster under the name the Secret derives from.
func TestRule_WorkerReadsTheClusterSecretInABuild(t *testing.T) {
	tr, _ := registered(t, modelHeader+dbThenWorker)
	objs := objectsOK(t, tr, orders(), nil)
	if _, ok := objs[[2]string{"Cluster", "orders-db"}]; !ok {
		t.Fatalf("the build made no Cluster named orders-db: %v", names(objs, "Cluster"))
	}
	if got := workerSecretName(t, objs); got != "orders-db-app" {
		t.Fatalf("the worker reads the Secret %v, want orders-db-app", got)
	}
}

// Case 2: under a Naming hook the bound name is the name the hook gives the Cluster, and the
// worker reads the Secret of that Cluster.
func TestRule_BoundNameFollowsTheNamingHook(t *testing.T) {
	tr, _ := registered(t, modelHeader+dbThenWorker)
	objs := objectsOK(t, tr, orders(), hookedNaming(oam.NameRolePostgresqlCluster))
	clusters := names(objs, "Cluster")
	if len(clusters) != 1 || !strings.HasPrefix(clusters[0], "h-") {
		t.Fatalf("the hook did not name the Cluster: %v", clusters)
	}
	if got, want := workerSecretName(t, objs), clusters[0]+"-app"; got != want {
		t.Fatalf("the worker reads the Secret %v, want %v", got, want)
	}
}

// Case 2b: a kind part (a Secret) is named by the hook when the build makes it, and a later part
// reads that name.
func TestRule_BoundNameOfAKindPartFollowsTheHook(t *testing.T) {
	doc := modelHeader + `
    - id: creds
      name: creds
      type: ` + secretType + `
      properties: {}
    - id: cfg
      name: cfg
      type: configmap@v1
      properties:
        data:
          secret: "${ parts.creds.object }"
`
	tr, _ := registered(t, doc)
	objs := objectsOK(t, tr, orders(), hookedNaming(oam.NameRoleObject))
	secrets := names(objs, "Secret")
	if len(secrets) != 1 {
		t.Fatalf("the build made Secrets %v, want one", secrets)
	}
	var cm map[string]any
	for k, o := range objs {
		if k[0] == "ConfigMap" {
			cm = o
		}
	}
	if got := prop(t, cm, "data", "secret"); got != secrets[0] {
		t.Fatalf("the ConfigMap reads the Secret %v, but the build made it %s", got, secrets[0])
	}
}

// renamerRule lowers a document of kind Renamer into an Application named "<name>-new".
type renamerRule struct{}

func (renamerRule) Kind() string { return "Renamer" }

func (renamerRule) LowerDocument(doc *oam.Application, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Documents: []oam.Application{{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: doc.Metadata.Name + "-new", Namespace: doc.Metadata.Namespace},
		Spec:     oam.ApplicationSpec{Components: doc.Spec.Components},
	}}}, nil
}

// Case 2c: a document rule renames the document, and a hook that names by the document reads
// the new name in the build, so the bound name reads it too.
func TestRule_BoundNameUsesTheRenamedDocument(t *testing.T) {
	tr, _ := registered(t, modelHeader+dbThenWorker)
	tr.RegisterDocumentLowering(renamerRule{})
	naming := func(r oam.NameRequest) (string, bool) {
		if r.Role != oam.NameRolePostgresqlCluster {
			return "", false
		}
		return r.Application + "-" + r.Default, true
	}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Renamer",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec:     oam.ApplicationSpec{Components: orders()},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Naming: naming})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	var clusterName, secret string
	for _, a := range apps {
		for _, o := range a.Objects {
			obj := *o
			raw, _ := json.Marshal(obj)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			switch m["kind"] {
			case "Cluster":
				clusterName = obj.GetName()
			case "Deployment":
				secret, _ = workerSecretName(t, map[[2]string]map[string]any{{"Deployment", obj.GetName()}: m}).(string)
			}
		}
	}
	if clusterName == "" || secret != clusterName+"-app" {
		t.Fatalf("the worker reads the Secret %q, but the Cluster is %q", secret, clusterName)
	}
}

// Case 3: an input takes its default, and the component's own property overrides it.
func TestRule_InputDefaultAndOverride(t *testing.T) {
	doc := strings.Replace(modelHeader, "  parts:\n", "  inputs:\n    key:\n      type: string\n      default: dflt\n  parts:\n", 1) + `
    - id: cfg
      name: cfg
      type: configmap@v1
      properties:
        data:
          k: "${ inputs.key }"
`
	tr, _ := registered(t, doc)
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"default", nil, "dflt"},
		{"override", map[string]any{"key": "set"}, "set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := objectsOK(t, tr, []oam.Component{{Name: "orders", Type: webappType, Properties: tc.props}}, nil)
			cm, ok := objs[[2]string{"ConfigMap", "orders-cfg"}]
			if !ok {
				t.Fatalf("the build made no ConfigMap named orders-cfg: %v", names(objs, "ConfigMap"))
			}
			if got := prop(t, cm, "data", "k"); got != tc.want {
				t.Fatalf("data.k = %v, want %v", got, tc.want)
			}
		})
	}
}

// Case 4: two components of one model type get distinct part names. A forced collision fails the
// build and names the name.
func TestRule_PartNamesComeFromTheAllocator(t *testing.T) {
	tr, _ := registered(t, modelHeader+dbThenWorker)
	objs := objectsOK(t, tr, []oam.Component{
		{Name: "orders", Type: webappType},
		{Name: "billing", Type: webappType},
	}, nil)
	got := names(objs, "Cluster")
	if len(got) != 2 {
		t.Fatalf("the build made Clusters %v, want orders-db and billing-db", got)
	}
	for _, want := range []string{"orders-db", "billing-db"} {
		if _, ok := objs[[2]string{"Cluster", want}]; !ok {
			t.Errorf("no Cluster named %s in %v", want, got)
		}
	}

	// The component "a" with the part "b-c" and the component "a-b" with the part "c" both
	// want the name "a-b-c".
	tr, reg := registered(t, modelHeader+`
    - id: b_c
      name: b-c
      type: configmap@v1
      properties: {}
`)
	other, err := model.Load([]byte(strings.Replace(modelHeader, "webapp.acme.example", "other.acme.example", 1) + `
    - id: c
      name: c
      type: configmap@v1
      properties: {}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Register(other); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = objects(t, tr, []oam.Component{
		{Name: "a", Type: webappType},
		{Name: "a-b", Type: "other.acme.example@v1"},
	}, nil)
	if err == nil {
		t.Fatal("a build with two parts named a-b-c succeeded")
	}
	if !strings.Contains(err.Error(), "a-b-c") {
		t.Errorf("error %q does not name the colliding name a-b-c", err)
	}
}

// Case 5: a model that emits no component is refused with ErrInvalid.
func TestRule_ModelThatEmitsNothingIsRefused(t *testing.T) {
	tr, _ := registered(t, modelHeader+`
    - id: off
      name: off
      type: configmap@v1
      when: "${ false }"
      properties: {}
`)
	_, err := objects(t, tr, orders(), nil)
	if err == nil {
		t.Fatal("a model that emits nothing was accepted")
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Errorf("error %q does not wrap ErrInvalid", err)
	}
}

// listOf returns a CEL list literal of n strings.
func listOf(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("'%d'", i)
	}
	return "[" + strings.Join(items, ", ") + "]"
}

func manyParts(n int) string {
	return `
    - id: many
      name: "c${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ ` + listOf(n) + ` }"
      properties: {}
`
}

// Case 6: a render gives at most 50 parts, counted after when and forEach. A part that is
// skipped counts none.
func TestRule_PartLimit(t *testing.T) {
	skipped := `
    - id: skipped
      name: "s${ item }"
      type: configmap@v1
      when: "${ false }"
      forEach:
        as: item
        in: "${ ` + listOf(60) + ` }"
      properties: {}
`
	for _, tc := range []struct {
		name  string
		parts string
		ok    bool
	}{
		{"50 pass", manyParts(50), true},
		{"51 fail", manyParts(51), false},
		{"skipped parts count none", skipped + manyParts(50), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, _ := registered(t, modelHeader+tc.parts)
			_, err := objects(t, tr, orders(), nil)
			if tc.ok && err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("a render of 51 parts was accepted")
				}
				if !errors.Is(err, model.ErrLimit) {
					t.Errorf("error %q does not wrap ErrLimit", err)
				}
			}
		})
	}
}

// nested returns the document of a model named level<i> whose one part is the model of the
// level below, or a ConfigMap at the bottom.
func nested(i int, below string) string {
	doc := strings.Replace(modelHeader, "webapp.acme.example", fmt.Sprintf("level%d.acme.example", i), 1)
	return doc + `
    - id: next
      name: next
      type: ` + below + `
      properties: {}
`
}

// Case 7: models nest at most 4 deep, checked at Register. A part type that is not declared, or
// names no version, fails at Register and names the part path.
func TestRegistry_NestingDepthAndPartTypes(t *testing.T) {
	register := func(tr *oam.Transformer, reg *model.Registry, doc string) error {
		m, err := model.Load([]byte(doc))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return reg.Register(m)
	}
	tr := expandTransformer(t)
	reg := model.NewRegistry(tr)
	below := "configmap@v1"
	for i := 1; i <= 4; i++ {
		if err := register(tr, reg, nested(i, below)); err != nil {
			t.Fatalf("depth %d: %v", i, err)
		}
		below = fmt.Sprintf("level%d.acme.example@v1", i)
	}
	err := register(tr, reg, nested(5, below))
	if err == nil {
		t.Fatal("a model nested 5 deep was registered")
	}
	if !errors.Is(err, model.ErrLimit) {
		t.Errorf("error %q does not wrap ErrLimit", err)
	}
	if _, rerr := tr.ResolveComponentType("level5.acme.example@v1"); rerr == nil {
		t.Error("a refused model left its type declared")
	}

	// Load pins every part type to a version, so Register meets only a type that does not resolve.
	err = register(tr, reg, strings.Replace(modelHeader, "webapp.acme.example", "bad.acme.example", 1)+`
    - id: p
      name: p
      type: nothing.acme.example@v1
      properties: {}
`)
	if err == nil {
		t.Fatal("a part of an undeclared type was registered")
	}
	if !strings.Contains(err.Error(), "spec.parts[0].type") {
		t.Errorf("error %q does not name the part path", err)
	}
	if _, rerr := tr.ResolveComponentType("bad.acme.example@v1"); rerr == nil {
		t.Error("a refused model left its type declared")
	}
}

// A trait type that is not declared fails at Register and names the trait path.
func TestRegistry_RefusesAnUndeclaredTraitType(t *testing.T) {
	tr := expandTransformer(t)
	m, err := model.Load([]byte(modelHeader + `
    - id: p
      name: p
      type: configmap@v1
      properties: {}
      traits:
        - type: nothing.acme.example@v1
          properties: {}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = model.NewRegistry(tr).Register(m)
	if err == nil {
		t.Fatal("a trait of an undeclared type was registered")
	}
	if !strings.Contains(err.Error(), "spec.parts[0].traits[0].type") {
		t.Errorf("error %q does not name the trait path", err)
	}
}

// Case 8: a model type that is already declared is refused, and the first registration stays.
func TestRegistry_RefusesADeclaredType(t *testing.T) {
	tr, reg := registered(t, modelHeader+dbThenWorker)
	m, err := model.Load([]byte(modelHeader + dbThenWorker))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Register(m); err == nil {
		t.Fatal("the same model was registered twice")
	}
	if err := model.NewRegistry(tr).Register(m); err == nil {
		t.Fatal("a second registry registered a type that the Transformer already declares")
	}
	if got := reg.Rule(m.Key()); got == nil {
		t.Error("the refused second registration removed the first rule")
	}
}

// The depth is the Transformer's, so a second Registry of it counts the models of the first.
func TestRegistry_SecondRegistryCountsTheDepth(t *testing.T) {
	tr := expandTransformer(t)
	first := model.NewRegistry(tr)
	below := "configmap@v1"
	for i := 1; i <= 4; i++ {
		m, err := model.Load([]byte(nested(i, below)))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if err := first.Register(m); err != nil {
			t.Fatalf("depth %d: %v", i, err)
		}
		below = fmt.Sprintf("level%d.acme.example@v1", i)
	}
	m, err := model.Load([]byte(nested(5, below)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := model.NewRegistry(tr).Register(m); !errors.Is(err, model.ErrLimit) {
		t.Fatalf("a second Registry took a model nested 5 deep: %v", err)
	}
}

// A model whose type the Transformer refuses to declare (its short name is a built-in type) fails
// at Register with no rule left on the Transformer, so the same refusal comes again.
func TestRegistry_RefusedDeclarationLeavesNoRule(t *testing.T) {
	tr := expandTransformer(t)
	reg := model.NewRegistry(tr)
	m, err := model.Load([]byte(strings.Replace(modelHeader, "webapp.acme.example", "configmap.acme.example", 1) + `
    - id: p
      name: p
      type: configmap@v1
      properties: {}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var first string
	for i := 0; i < 2; i++ {
		err := reg.Register(m)
		if err == nil {
			t.Fatal("a model whose type clashes with a built-in type was registered")
		}
		if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "panic") {
			t.Fatalf("attempt %d fails for another reason: %v", i+1, err)
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Errorf("the second refusal %q differs from the first %q", err, first)
		}
		if _, ok := tr.ComponentLoweringRuleFor(m.Key()); ok {
			t.Fatal("a refused model left its rule on the Transformer")
		}
	}
}

// A model with a part of the wrong kind of expression fails at Register, and leaves nothing
// registered: the type is not declared and a second try with the repaired model works.
func TestRegistry_FailedRegisterLeavesNothingBehind(t *testing.T) {
	tr := expandTransformer(t)
	reg := model.NewRegistry(tr)
	bad, err := model.Load([]byte(modelHeader + `
    - id: p
      name: p
      type: nothing.acme.example@v1
      properties: {}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Register(bad); err == nil {
		t.Fatal("a model of an undeclared part type was registered")
	}
	if reg.Rule(bad.Key()) != nil {
		t.Error("a failed Register left a rule in the registry")
	}
	good, err := model.Load([]byte(modelHeader + `
    - id: p
      name: p
      type: configmap@v1
      properties: {}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Register(good); err != nil {
		t.Fatalf("Register after a failed Register: %v", err)
	}
}
