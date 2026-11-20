package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the outputs of a model (go-kure/launcher#1643): spec.outputs is mandatory,
// each output is one read of the name of a record of an earlier part, and the model publishes
// them as outputs of its own component.

// outputsDoc is a model named name with the given spec.outputs block (the lines below
// `  outputs:`, or empty for no key at all) and the given parts.
func outputsDoc(name, outputs, parts string) string {
	head := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Model\nmetadata:\n  name: " + name + "\n  version: v1\nspec:\n  celEnv: \"1\"\n"
	return head + outputs + "  parts:\n" + parts
}

const pgOnlyParts = `
    - id: creds
      type: ` + secretType + `
      properties: {}
    - id: db
      type: ` + pgType + `
      properties: {}
`

// TestLoadOutputs_PresenceIsKept: the key left out and a null are no mapping, and an empty
// mapping is one, in YAML and in JSON, so the registry can tell a model that publishes nothing
// from one that never said.
func TestLoadOutputs_PresenceIsKept(t *testing.T) {
	for name, tc := range map[string]struct {
		doc     string
		present bool
	}{
		"left out":      {outputsDoc("webapp.acme.example", "", pgOnlyParts), false},
		"null":          {outputsDoc("webapp.acme.example", "  outputs: null\n", pgOnlyParts), false},
		"empty":         {outputsDoc("webapp.acme.example", "  outputs: {}\n", pgOnlyParts), true},
		"one output":    {outputsDoc("webapp.acme.example", "  outputs:\n    own: \"${ parts.creds.name }\"\n", pgOnlyParts), true},
		"json empty":    {`{"apiVersion":"launcher.gokure.dev/v1alpha1","kind":"Model","metadata":{"name":"webapp.acme.example","version":"v1"},"spec":{"celEnv":"1","outputs":{},"parts":[{"id":"creds","type":"` + secretType + `"}]}}`, true},
		"json null":     {`{"apiVersion":"launcher.gokure.dev/v1alpha1","kind":"Model","metadata":{"name":"webapp.acme.example","version":"v1"},"spec":{"celEnv":"1","outputs":null,"parts":[{"id":"creds","type":"` + secretType + `"}]}}`, false},
		"json left out": {`{"apiVersion":"launcher.gokure.dev/v1alpha1","kind":"Model","metadata":{"name":"webapp.acme.example","version":"v1"},"spec":{"celEnv":"1","parts":[{"id":"creds","type":"` + secretType + `"}]}}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			m, err := model.Load([]byte(tc.doc))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := m.Spec.Outputs != nil; got != tc.present {
				t.Fatalf("spec.outputs present = %v, want %v", got, tc.present)
			}
		})
	}
}

// TestLoadOutputs_RefusesABadKeyOrValue: a key is one identifier that is no field of a record,
// and a value is not empty.
func TestLoadOutputs_RefusesABadKeyOrValue(t *testing.T) {
	for name, tc := range map[string]struct {
		outputs string
		wants   []string
	}{
		"a reserved key name": {"  outputs:\n    name: \"${ parts.creds.name }\"\n", []string{"spec.outputs.name", "reserved"}},
		"a reserved key kind": {"  outputs:\n    kind: \"${ parts.creds.name }\"\n", []string{"spec.outputs.kind", "reserved"}},
		"a hyphen":            {"  outputs:\n    a-b: \"${ parts.creds.name }\"\n", []string{"spec.outputs.a-b", "identifier"}},
		"a leading digit":     {"  outputs:\n    1x: \"${ parts.creds.name }\"\n", []string{"spec.outputs.1x", "identifier"}},
		"an empty value":      {"  outputs:\n    own: \"\"\n", []string{"spec.outputs.own", "required"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := model.Load([]byte(outputsDoc("webapp.acme.example", tc.outputs, pgOnlyParts)))
			if err == nil {
				t.Fatal("Load accepted it")
			}
			if !errors.Is(err, model.ErrInvalid) {
				t.Errorf("error %q does not wrap ErrInvalid", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// TestRegister_NeedsTheOutputsKey: a model with no spec.outputs is refused, naming the model
// and the key, and `outputs: {}` is how a model says it publishes nothing.
func TestRegister_NeedsTheOutputsKey(t *testing.T) {
	for name, outputs := range map[string]string{"left out": "", "null": "  outputs: null\n"} {
		t.Run(name, func(t *testing.T) {
			m, err := model.Load([]byte(outputsDoc("webapp.acme.example", outputs, pgOnlyParts)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			err = model.NewRegistry(expandTransformer(t)).Register(m)
			if err == nil {
				t.Fatal("Register accepted a model with no spec.outputs")
			}
			for _, want := range []string{webappType, "spec.outputs", "outputs: {}"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
	registered(t, outputsDoc("webapp.acme.example", "  outputs: {}\n", pgOnlyParts))
}

// TestCompileOutputs_RefusesWhatIsNotOneRecordName: an output is exactly one read of the name of
// a record of an earlier part that has no when and no forEach. Each refusal names the site and
// the two forms that are allowed.
func TestCompileOutputs_RefusesWhatIsNotOneRecordName(t *testing.T) {
	parts := pgOnlyParts + `
    - id: maybe
      type: ` + secretType + `
      when: "${ false }"
      properties: {}
`
	forms := []string{"parts.<id>.name", "parts.<id>.<key>.name"}
	for name, tc := range map[string]struct {
		value string
		wants []string
	}{
		"a computed name":        {"${ parts.creds.name + '-x' }", forms},
		"text around a read":     {"x-${ parts.creds.name }", forms},
		"a literal":              {"literal", forms},
		"a value":                {"${ 'a' }", forms},
		"a read of a record":     {"${ parts.db.cluster }", forms},
		"a read of the part":     {"${ parts.db }", forms},
		"a field of a record":    {"${ parts.db.cluster.kind }", forms},
		"a part that is skipped": {"${ parts.maybe.name }", []string{"parts.maybe", "not readable"}},
		"a part not declared":    {"${ parts.nope.name }", []string{"nope"}},
		"two reads":              {"${ parts.creds.name + parts.db.cluster.name }", forms},
	} {
		t.Run(name, func(t *testing.T) {
			m, err := model.Load([]byte(outputsDoc("webapp.acme.example", "  outputs:\n    own: \""+tc.value+"\"\n", parts)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			_, err = model.Compile(m)
			if err == nil {
				t.Fatal("Compile accepted it")
			}
			if !errors.Is(err, model.ErrInvalid) {
				t.Errorf("error %q does not wrap ErrInvalid", err)
			}
			for _, want := range append([]string{"spec.outputs.own"}, tc.wants...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// outputKinds answers the outputs of a component of the type, as key -> "Kind/name".
func outputKinds(t *testing.T, tr *oam.Transformer, naming func(oam.NameRequest) (string, bool), c oam.Component) map[string]string {
	t.Helper()
	outs, err := tr.ComponentOutputsNamed("shop", &c, naming)
	if err != nil {
		t.Fatalf("ComponentOutputsNamed(%s): %v", c.Type, err)
	}
	got := map[string]string{}
	for _, o := range outs {
		got[o.Key] = o.Kind.Kind + "/" + o.Name
	}
	return got
}

const publishedOutputs = `  outputs:
    creds: "${ parts.creds.name }"
    cluster: "${ parts.db.cluster.name }"
    secret: "${ parts.db.app_secret.name }"
`

// TestRule_PublishesTheForwardedRecords: the model answers, for each output, the kind and the
// name its part publishes, under the key of the output, and the Naming hook reaches them.
func TestRule_PublishesTheForwardedRecords(t *testing.T) {
	tr, _ := registered(t, outputsDoc("webapp.acme.example", publishedOutputs, pgOnlyParts))
	c := oam.Component{Name: "orders", Type: webappType}

	want := map[string]string{"creds": "Secret/orders-creds", "cluster": "Cluster/orders-db", "secret": "Secret/orders-db-app"}
	if got := outputKinds(t, tr, nil, c); !mapsEqual(got, want) {
		t.Fatalf("outputs = %v, want %v", got, want)
	}

	hooked := outputKinds(t, tr, hookedNaming(oam.NameRolePostgresqlCluster), c)
	if hooked["cluster"] != "Cluster/h-orders-db" || hooked["secret"] != "Secret/h-orders-db-app" || hooked["creds"] != "Secret/orders-creds" {
		t.Fatalf("outputs under a Naming hook = %v", hooked)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestRule_ASilentModelPublishesNothing: `outputs: {}` gives no output and costs no render.
func TestRule_ASilentModelPublishesNothing(t *testing.T) {
	tr, _ := registered(t, outputsDoc("webapp.acme.example", "  outputs: {}\n", pgOnlyParts))
	if got := outputKinds(t, tr, nil, oam.Component{Name: "orders", Type: webappType}); len(got) != 0 {
		t.Fatalf("outputs = %v, want none", got)
	}
}

// TestRule_RefusesAnOutputItsPartDoesNotDeclare: the shape of a part's outputs is known only
// when the part is built, so the refusal comes with the query and names the model, the output,
// the part and what the part declares.
func TestRule_RefusesAnOutputItsPartDoesNotDeclare(t *testing.T) {
	tr, _ := registered(t, outputsDoc("webapp.acme.example", "  outputs:\n    own: \"${ parts.db.nope.name }\"\n", pgOnlyParts))
	_, err := tr.ComponentOutputs(&oam.Component{Name: "orders", Type: webappType})
	if err == nil {
		t.Fatal("the query accepted an output its part does not declare")
	}
	for _, want := range []string{"spec.outputs.own", "parts.db.nope", "app_secret", "cluster"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

const consumerType = "shop.acme.example@v1"

// consumerParts is a model that emits the model above as one part and a ConfigMap that reads
// the records the part publishes.
const consumerParts = `
    - id: app
      type: ` + webappType + `
      properties: {}
    - id: cfg
      type: configmap@v1
      properties:
        data:
          cluster: "${ parts.app.cluster.name }"
          secret: "${ parts.app.secret.name }"
          creds: "${ parts.app.creds.name }"
`

// TestRule_AModelReadsTheOutputsOfAModelPart: a model part reads the records the model it emits
// publishes, and each read gives the name of the object the build makes.
func TestRule_AModelReadsTheOutputsOfAModelPart(t *testing.T) {
	tr := registerAll(t,
		outputsDoc("webapp.acme.example", publishedOutputs, pgOnlyParts),
		outputsDoc("shop.acme.example", "  outputs:\n    cluster: \"${ parts.app.cluster.name }\"\n", consumerParts))
	objs := objectsOK(t, tr, []oam.Component{{Name: "orders", Type: consumerType}}, nil)

	cms := names(objs, "ConfigMap")
	if len(cms) != 1 {
		t.Fatalf("the build made %d ConfigMaps (%v), want 1", len(cms), cms)
	}
	data := objs[[2]string{"ConfigMap", cms[0]}]["data"].(map[string]any)
	clusters := names(objs, "Cluster")
	if len(clusters) != 1 {
		t.Fatalf("the build made %d Clusters (%v), want 1", len(clusters), clusters)
	}
	if data["cluster"] != clusters[0] || data["secret"] != clusters[0]+"-app" {
		t.Fatalf("the ConfigMap reads %v, but the build made the Cluster %s", data, clusters[0])
	}
	if got := outputKinds(t, tr, nil, oam.Component{Name: "orders", Type: consumerType}); got["cluster"] != "Cluster/"+clusters[0] {
		t.Fatalf("the outer model publishes %v, but the build made the Cluster %s", got, clusters[0])
	}
}

// TestRule_AForwardedSecretSurvivesAHookOnTheRoleObject: the build names the object of a kind part
// (a Secret) after its component and never asks the Naming hook, so the forwarded-output check
// and the read of the name give the same answer when the hook renames the role object.
func TestRule_AForwardedSecretSurvivesAHookOnTheRoleObject(t *testing.T) {
	tr := registerAll(t,
		outputsDoc("webapp.acme.example", publishedOutputs, pgOnlyParts),
		outputsDoc("shop.acme.example", "  outputs:\n    cluster: \"${ parts.app.cluster.name }\"\n", consumerParts))
	objs := objectsOK(t, tr, []oam.Component{{Name: "orders", Type: consumerType}}, hookedNaming(oam.NameRoleObject))

	cms := names(objs, "ConfigMap")
	if len(cms) != 1 {
		t.Fatalf("the build made %d ConfigMaps (%v), want 1", len(cms), cms)
	}
	creds, _ := objs[[2]string{"ConfigMap", cms[0]}]["data"].(map[string]any)["creds"].(string)
	if _, ok := objs[[2]string{"Secret", creds}]; !ok {
		t.Fatalf("the ConfigMap reads the Secret %q, which the build did not make (Secrets: %v)", creds, names(objs, "Secret"))
	}
}

// TestCompile_ReadsAreRefusedWhateverTheirSpelling: a read of a part is checked against what the
// part declares however it is written. A key with a dot in it is one key and not two, and a
// leading dot reaches the global `parts` where a comprehension variable of that name shadows it.
func TestCompile_ReadsAreRefusedWhateverTheirSpelling(t *testing.T) {
	for name, tc := range map[string]struct {
		value string
		want  string
	}{
		"a dotted key next to the path it looks like": {"${ parts.db.app_secret.name + parts.db['app_secret.name'] }", `declares no output "app_secret.name"`},
		"a global identifier under a shadow":          {"${ [0].map(parts, .parts.db.nope.name)[0] }", `declares no output "nope"`},
	} {
		t.Run(name, func(t *testing.T) {
			parts := pgOnlyParts + `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          x: "` + tc.value + `"
`
			tr := registerAll(t, outputsDoc("webapp.acme.example", "  outputs: {}\n", parts))
			_, err := objects(t, tr, []oam.Component{{Name: "orders", Type: webappType}}, nil)
			if err == nil {
				t.Fatal("the build accepted it")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}
