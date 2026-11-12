package model_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the binding of `parts.<part>.<key>` (go-kure/launcher#1261, step 3a):
// Expand asks the Transformer for the outputs of each part it builds, in document order,
// and a later part reads the object name that answer gives.

const (
	pgType       = "postgresql.launcher.gokure.dev@v1"
	secretType   = "secret@v1"
	webType      = "webservice.launcher.gokure.dev@v1"
	modelHeader  = "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Model\nmetadata:\n  name: webapp.acme.example\n  version: v1\nspec:\n  celEnv: \"1\"\n  parts:\n"
	secretEnvRef = `
        env:
          - name: DB_PASSWORD
            valueFrom:
              secretKeyRef:
                name: "${ parts.db.app_secret }"
                key: password`
)

// expandTransformer registers every builtin and declares its type identities, as a consumer does.
func expandTransformer(t *testing.T) *oam.Transformer {
	t.Helper()
	tr := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		tr.RegisterComponentLowering(r)
	}
	for name, h := range registry.TraitHandlers() {
		tr.RegisterBuiltinTrait(name, h)
	}
	for name, h := range registry.PolicyHandlers() {
		tr.RegisterPolicy(name, h)
	}
	for _, r := range registry.TraitLoweringRules() {
		tr.RegisterBuiltinTraitLowering(r)
	}
	if err := registry.DeclareTypes(tr); err != nil {
		t.Fatalf("DeclareTypes: %v", err)
	}
	return tr
}

func compileParts(t *testing.T, parts string) *model.Compiled {
	t.Helper()
	m, err := model.Load([]byte(modelHeader + parts))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c, err := model.Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return c
}

var expandScope = model.Scope{Self: model.Self{Name: "shop", Application: "shop"}}

func expandOK(t *testing.T, c *model.Compiled, naming func(oam.NameRequest) (string, bool)) []oam.Component {
	t.Helper()
	comps, err := c.Expand(expandTransformer(t), expandScope, naming, nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	return comps
}

func hookedNaming(role oam.NameRole) func(oam.NameRequest) (string, bool) {
	return func(r oam.NameRequest) (string, bool) {
		if r.Role != role {
			return "", false
		}
		return "h-" + r.Default, true
	}
}

// built returns the (kind, name) pairs a Transform of comps generates.
func built(t *testing.T, comps []oam.Component, naming func(oam.NameRequest) (string, bool)) map[[2]string]bool {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec:     oam.ApplicationSpec{Components: comps},
	}
	cluster, err := expandTransformer(t).Transform(app, oam.TransformContext{Namespace: "demo", Naming: naming})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	out := map[[2]string]bool{}
	for _, a := range apps {
		for _, o := range a.Objects {
			obj := *o
			kind := obj.GetObjectKind().GroupVersionKind().Kind
			if kind == "" {
				kind = reflect.TypeOf(obj).Elem().Name()
			}
			out[[2]string{kind, obj.GetName()}] = true
		}
	}
	return out
}

func prop(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("at %q: %T is not a map", k, v)
			}
			v = m[k]
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				t.Fatalf("at [%d]: %T is not a list of that length", k, v)
			}
			v = l[k]
		}
	}
	return v
}

func secretRefName(t *testing.T, c oam.Component) any {
	return prop(t, c.Properties, "env", 0, "valueFrom", "secretKeyRef", "name")
}

const dbThenWorker = `
    - id: db
      name: db
      type: ` + pgType + `
      properties: {}
    - id: worker
      name: worker
      type: ` + webType + `
      properties:
        image: ghcr.io/acme/worker:v1` + secretEnvRef + "\n"

// Case 1: a worker reads the Secret CloudNativePG generates for a postgresql part. The name it
// carries is the one the Cluster the build makes derives it from.
func TestExpand_WorkerReadsThePostgresqlAppSecret(t *testing.T) {
	comps := expandOK(t, compileParts(t, dbThenWorker), nil)
	if len(comps) != 2 {
		t.Fatalf("Expand gave %d components, want 2", len(comps))
	}
	if got := secretRefName(t, comps[1]); got != "db-app" {
		t.Fatalf("env secretKeyRef.name = %v, want db-app", got)
	}
	if !built(t, comps, nil)[[2]string{"Cluster", "db"}] {
		t.Fatalf("the build made no Cluster named db, so db-app is not that Cluster's Secret")
	}
}

// Case 2: a managed role reads the name of a secret part's own Secret.
func TestExpand_RoleReadsASecretPart(t *testing.T) {
	c := compileParts(t, `
    - id: roleSecret
      name: creds
      type: `+secretType+`
      properties: {}
    - id: db
      name: db
      type: `+pgType+`
      properties:
        managedRoles:
          - name: app
            login: true
            passwordSecret: "${ parts.roleSecret.object }"
`)
	comps := expandOK(t, c, nil)
	if got := prop(t, comps[1].Properties, "managedRoles", 0, "passwordSecret"); got != "creds" {
		t.Fatalf("passwordSecret = %v, want creds", got)
	}
	if !built(t, comps, nil)[[2]string{"Secret", "creds"}] {
		t.Fatalf("the build made no Secret named creds")
	}
}

// Case 3: the author's name for the Cluster moves the bound name.
func TestExpand_AuthoredClusterNameMovesTheBinding(t *testing.T) {
	c := compileParts(t, strings.Replace(dbThenWorker, "properties: {}", "properties:\n        clusterObjectName: pg", 1))
	comps := expandOK(t, c, nil)
	if got := secretRefName(t, comps[1]); got != "pg-app" {
		t.Fatalf("env secretKeyRef.name = %v, want pg-app", got)
	}
	if !built(t, comps, nil)[[2]string{"Cluster", "pg"}] {
		t.Fatalf("the build made no Cluster named pg")
	}
}

// The binding is read after the part's own expressions are evaluated: a name the author writes as
// an expression moves the bound name by the value the expression gives.
func TestExpand_BindsAfterThePropertiesAreEvaluated(t *testing.T) {
	m, err := model.Load([]byte(strings.Replace(modelHeader, "  parts:\n", "  inputs:\n    dbname:\n      type: string\n      required: true\n  parts:\n", 1) +
		strings.Replace(dbThenWorker, "properties: {}", "properties:\n        clusterObjectName: \"${ inputs.dbname }\"", 1)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c, err := model.Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	sc := expandScope
	sc.Inputs = map[string]any{"dbname": "pg2"}
	comps, err := c.Expand(expandTransformer(t), sc, nil, nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got := secretRefName(t, comps[1]); got != "pg2-app" {
		t.Fatalf("env secretKeyRef.name = %v, want pg2-app", got)
	}
}

// Case 4: a key the part does not declare fails the build and names the site, the part and the
// key. It never reads as an empty string.
func TestExpand_UndeclaredKeyFailsTheBuild(t *testing.T) {
	c := compileParts(t, strings.Replace(dbThenWorker, "parts.db.app_secret", "parts.db.nope", 1))
	comps, err := c.Expand(expandTransformer(t), expandScope, nil, nil)
	if err == nil {
		t.Fatalf("Expand accepted a read of an undeclared key: %v", comps)
	}
	for _, want := range []string{"spec.parts[1].properties.env[0].valueFrom.secretKeyRef.name", "parts.db", "nope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if comps != nil {
		t.Errorf("a failed Expand gave components: %v", comps)
	}
}

// Case 5: a part reads only an earlier part with no when and no forEach. Each other read fails at
// Compile, and the error names the site and the part that was read, not only CEL's own text.
func TestCompile_RefusesAPartThatIsNotReadable(t *testing.T) {
	ref := `
        data:
          x: "${ parts.%s.object }"`
	for _, tc := range []struct {
		name  string
		parts string
		site  string
		part  string
	}{
		{"later part", `
    - id: first
      name: first
      type: configmap@v1
      properties:` + strings.Replace(ref, "%s", "second", 1) + `
    - id: second
      name: second
      type: configmap@v1
      properties: {}
`, "spec.parts[0].properties.data.x", "second"},
		{"its own part", `
    - id: first
      name: first
      type: configmap@v1
      properties:` + strings.Replace(ref, "%s", "first", 1) + "\n", "spec.parts[0].properties.data.x", "first"},
		{"part with when", `
    - id: opt
      name: opt
      type: configmap@v1
      when: "${ true }"
      properties: {}
    - id: user
      name: user
      type: configmap@v1
      properties:` + strings.Replace(ref, "%s", "opt", 1) + "\n", "spec.parts[1].properties.data.x", "opt"},
		{"part with forEach", `
    - id: many
      name: "${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ ['a', 'b'] }"
      properties: {}
    - id: user
      name: user
      type: configmap@v1
      properties:` + strings.Replace(ref, "%s", "many", 1) + "\n", "spec.parts[1].properties.data.x", "many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := model.Load([]byte(modelHeader + tc.parts))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			_, err = model.Compile(m)
			if err == nil {
				t.Fatalf("Compile accepted a read of %s", tc.name)
			}
			if !errors.Is(err, model.ErrInvalid) {
				t.Errorf("error %q does not wrap ErrInvalid", err)
			}
			for _, want := range []string{tc.site, "parts." + tc.part} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "undeclared reference") {
				t.Errorf("error %q is CEL's own text, which hides the cause", err)
			}
		})
	}
}

// A direct Expand keeps the hook for the object of a kind part: its components are not emitted
// members, so a Transform asks the hook for them, and the bound name is the built name.
func TestExpand_KindPartObjectNameFollowsTheHook(t *testing.T) {
	c := compileParts(t, `
    - id: creds
      name: creds
      type: `+secretType+`
      properties: {}
    - id: cfg
      name: cfg
      type: configmap@v1
      properties:
        data:
          secret: "${ parts.creds.object }"
`)
	naming := hookedNaming(oam.NameRoleObject)
	comps := expandOK(t, c, naming)
	got := prop(t, comps[1].Properties, "data", "secret")
	if got != "h-creds" {
		t.Fatalf("data.secret = %v, want h-creds", got)
	}
	if !built(t, comps, naming)[[2]string{"Secret", "h-creds"}] {
		t.Fatalf("the build made no Secret named h-creds")
	}
}

// Case 6: the Naming hook moves the bound name the same as it moves the built name.
func TestExpand_NamingHookMovesTheBinding(t *testing.T) {
	naming := hookedNaming(oam.NameRolePostgresqlCluster)
	comps := expandOK(t, compileParts(t, dbThenWorker), naming)
	if got := secretRefName(t, comps[1]); got != "h-db-app" {
		t.Fatalf("env secretKeyRef.name = %v, want h-db-app", got)
	}
	if !built(t, comps, naming)[[2]string{"Cluster", "h-db"}] {
		t.Fatalf("the build made no Cluster named h-db")
	}
}

// Case 7: a part whose properties the component refuses fails with its own error and binds
// nothing, so a later read does not run against a missing answer.
func TestExpand_InvalidPartFailsWithItsOwnError(t *testing.T) {
	c := compileParts(t, strings.Replace(dbThenWorker, "properties: {}", "properties:\n        managedRoles:\n          - name: r\n            passwordSecret: 1", 1))
	comps, err := c.Expand(expandTransformer(t), expandScope, nil, nil)
	if err == nil {
		t.Fatalf("Expand accepted an invalid part: %v", comps)
	}
	if !strings.Contains(err.Error(), "passwordSecret") {
		t.Errorf("error %q is not the component's own", err)
	}
	if strings.Contains(err.Error(), "spec.parts[1]") {
		t.Errorf("error %q is about the reader, so the invalid part did not fail first", err)
	}
	if comps != nil {
		t.Errorf("a failed Expand gave components: %v", comps)
	}
}

// A part with a false when is not emitted, and a forEach part is emitted once per element.
func TestExpand_WhenAndForEach(t *testing.T) {
	c := compileParts(t, `
    - id: off
      name: off
      type: configmap@v1
      when: "${ false }"
      properties: {}
    - id: many
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ ['a', 'b'] }"
      properties: {}
`)
	comps := expandOK(t, c, nil)
	var names []string
	for _, comp := range comps {
		names = append(names, comp.Name)
	}
	if !reflect.DeepEqual(names, []string{"cfg-a", "cfg-b"}) {
		t.Fatalf("components = %v, want cfg-a and cfg-b", names)
	}
}

// An indexed read of an undeclared key is refused like a selected one, even where CEL's own
// short-circuit would hide the failed read.
func TestExpand_UndeclaredIndexedKeyFailsTheBuild(t *testing.T) {
	for name, expr := range map[string]string{
		"plain":      `${ parts.db['nope'] }`,
		"suppressed": `${ (parts.db['nope'] == '' || true) ? '' : 'unused' }`,
	} {
		t.Run(name, func(t *testing.T) {
			c := compileParts(t, `
    - id: db
      name: db
      type: `+pgType+`
      properties: {}
    - id: cfg
      name: cfg
      type: configmap@v1
      properties:
        data:
          x: "`+expr+`"
`)
			comps, err := c.Expand(expandTransformer(t), expandScope, nil, nil)
			if err == nil {
				t.Fatalf("Expand accepted an indexed read of an undeclared key: %v", comps)
			}
			for _, want := range []string{"spec.parts[1].properties.data.x", "parts.db", "nope"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// A model whose parts all stay off emits nothing, and that is an error, as the README says.
func TestExpand_RefusesAModelThatEmitsNothing(t *testing.T) {
	c := compileParts(t, `
    - id: off
      name: off
      type: configmap@v1
      when: "${ false }"
      properties: {}
`)
	comps, err := c.Expand(expandTransformer(t), expandScope, nil, nil)
	if err == nil {
		t.Fatalf("Expand accepted a model that emits nothing: %v", comps)
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Errorf("error %v is not ErrInvalid", err)
	}
}
